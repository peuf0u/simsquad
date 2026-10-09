// Package agenttest is the agent-test core behind the `simsquad run`
// commands: it turns a feature file and the equipment into a test run,
// supervises workers, validates their results and aggregates them into a
// report with a verdict. It is the Go port of simsquad-pilot's scripts.
package agenttest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/peuf0u/simsquad/internal/contract"
)

var (
	resultSchemaOnce sync.Once
	resultSchema     *jsonschema.Schema
	resultSchemaErr  error
)

// compiledResultSchema compiles the embedded result schema once per process.
func compiledResultSchema() (*jsonschema.Schema, error) {
	resultSchemaOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(contract.ResultSchema))
		if err != nil {
			resultSchemaErr = fmt.Errorf("embedded result schema: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(contract.ResultSchemaURL, doc); err != nil {
			resultSchemaErr = fmt.Errorf("embedded result schema: %w", err)
			return
		}
		resultSchema, resultSchemaErr = c.Compile(contract.ResultSchemaURL)
	})
	return resultSchema, resultSchemaErr
}

// ValidateWorkerDir checks <dir>/result.json against the embedded result
// schema and then the evidence rule. It never fails: an unreadable or
// unparseable file is reported as a validation error, so `run report` can
// record the worker as invalid instead of crashing.
//
// The returned result is the parsed file when it is structurally valid
// (even if the evidence rule fails), and nil otherwise. The returned errors
// are human-readable, each prefixed with a JSONPath-like location ("$",
// "$.scenarios[0].evidence[0]"); an empty slice means valid.
func ValidateWorkerDir(dir string) (*contract.WorkerResult, []string) {
	path := filepath.Join(dir, contract.ResultFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, []string{fmt.Sprintf("cannot read %s: %v", contract.ResultFileName, err)}
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, []string{fmt.Sprintf("cannot parse %s: %v", contract.ResultFileName, err)}
	}
	sch, err := compiledResultSchema()
	if err != nil {
		return nil, []string{err.Error()}
	}
	if err := sch.Validate(inst); err != nil {
		return nil, schemaErrors(err)
	}

	var res contract.WorkerResult
	if err := json.Unmarshal(raw, &res); err != nil {
		// Unreachable for schema-valid input; kept so a schema/type drift
		// surfaces as an error rather than a zero-valued result.
		return nil, []string{fmt.Sprintf("cannot decode %s: %v", contract.ResultFileName, err)}
	}
	// Semantic checks run only on structurally valid input, as in pilot's
	// validate.py: their messages would be noise on a malformed document.
	return &res, evidenceErrors(dir, &res)
}

// evidenceErrors applies the evidence rule: bugs carry a severity; every bug,
// question and blocked scenario has screenshot evidence; and every referenced
// screenshot exists inside the worker dir.
func evidenceErrors(dir string, res *contract.WorkerResult) []string {
	errs := []string{}
	for i, s := range res.Scenarios {
		loc := fmt.Sprintf("$.scenarios[%d]", i)
		if s.Status == contract.ScenarioBlocked && len(s.Evidence) == 0 {
			errs = append(errs, loc+": blocked scenario requires non-empty evidence")
		}
		for j, p := range s.Evidence {
			errs = appendMissing(errs, dir, fmt.Sprintf("%s.evidence[%d]", loc, j), p)
		}
	}
	if res.Exploration != nil {
		for i, a := range res.Exploration.Actions {
			if a.Screenshot != "" {
				errs = appendMissing(errs, dir, fmt.Sprintf("$.exploration.actions[%d].screenshot", i), a.Screenshot)
			}
		}
	}
	for i, f := range res.Findings {
		loc := fmt.Sprintf("$.findings[%d]", i)
		if f.Type == contract.FindingBug && f.Severity == "" {
			errs = append(errs, loc+": bug finding requires severity")
		}
		if (f.Type == contract.FindingBug || f.Type == contract.FindingQuestion) && len(f.Evidence) == 0 {
			errs = append(errs, fmt.Sprintf("%s: %s finding requires non-empty evidence", loc, f.Type))
		}
		for j, p := range f.Evidence {
			errs = appendMissing(errs, dir, fmt.Sprintf("%s.evidence[%d]", loc, j), p)
		}
	}
	return errs
}

// appendMissing records an error when rel doesn't name an existing file
// inside dir. Absolute paths and paths escaping dir are rejected: evidence
// must live in the worker folder so the report can link it.
func appendMissing(errs []string, dir, loc, rel string) []string {
	if filepath.IsAbs(rel) || !filepath.IsLocal(rel) {
		return append(errs, fmt.Sprintf("%s: path is outside the worker folder: %s", loc, rel))
	}
	info, err := os.Stat(filepath.Join(dir, rel))
	if err != nil || info.IsDir() {
		return append(errs, fmt.Sprintf("%s: file does not exist: %s", loc, rel))
	}
	return errs
}

var errPrinter = message.NewPrinter(language.English)

// schemaErrors flattens a jsonschema validation error into one message per
// leaf cause, located by instance path.
func schemaErrors(err error) []string {
	verr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []string{err.Error()}
	}
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			out = append(out, jsonPath(e.InstanceLocation)+": "+e.ErrorKind.LocalizedString(errPrinter))
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(verr)
	return out
}

// jsonPath renders an instance location as "$.a.b[0]".
func jsonPath(loc []string) string {
	var b strings.Builder
	b.WriteString("$")
	for _, tok := range loc {
		if isIndex(tok) {
			b.WriteString("[" + tok + "]")
		} else {
			b.WriteString("." + tok)
		}
	}
	return b.String()
}

func isIndex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
