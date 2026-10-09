// BuildReport aggregates a finished test run into report.json's shape and
// decides the verdict. It is the Go port of simsquad-pilot's
// scripts/aggregate.py, reading run.json from `run new`, deploy.json (the
// redirected deploy stdout) and one worker dir per ready device.

package agenttest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/peuf0u/simsquad/internal/contract"
)

// BuildReport reads the run dir and returns its report. Only an unusable
// run.json is an error: every other problem (no deploy output, no ready
// device, dead or invalid workers) is recorded in the report and decides
// its verdict, so a broken run still gets a report and is marked finished.
func BuildReport(runDir string, now time.Time) (*contract.Report, error) {
	var run contract.RunRecord
	raw, err := os.ReadFile(filepath.Join(runDir, contract.RunRecordFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", contract.RunRecordFile, err)
	}
	if err := json.Unmarshal(raw, &run); err != nil {
		return nil, fmt.Errorf("parse %s: %w", contract.RunRecordFile, err)
	}

	rep := &contract.Report{
		RunID:        run.RunID,
		FeatureTitle: run.FeatureTitle,
		FeatureFile:  run.FeatureFile,
		Source:       run.Source,
		StartedAt:    run.CreatedAt,
		FinishedAt:   now.UTC().Format(time.RFC3339),
		Squad:        contract.ReportSquad{Name: run.SquadName, Env: map[string]string{}},
		Matrix:       []contract.ReportMatrixRow{},
		Devices:      []contract.ReportDevice{},
	}

	squad, deployErr := readDeploy(runDir)
	if deployErr == nil {
		for k, v := range squad.Env {
			rep.Squad.Env[k] = v
		}
		for _, d := range squad.Devices {
			if !appliesTo(run.Platforms, string(d.Platform)) {
				continue
			}
			if d.Status != contract.StatusReady {
				rep.Undeployed = append(rep.Undeployed, contract.ReportUndeployed{
					Platform: string(d.Platform), Name: d.Name, ID: d.UDID,
					Status: string(d.Status), Error: d.ErrorMessage,
				})
				continue
			}
			v := contract.NewDeviceView(d, squad.CreatedAt, nil)
			rep.Devices = append(rep.Devices, loadDevice(runDir, v))
		}
	}

	rep.Matrix = buildMatrix(&run, rep.Devices)
	rep.Summary.Counts = countReport(rep)
	rep.Summary.Verdict, rep.Summary.Reason = decideVerdict(rep, deployErr, workersRan(runDir))
	return rep, nil
}

// readDeploy parses deploy.json. A missing, unparseable or nameless file
// means deploy failed (its stdout is empty or not JSON when it crashes).
func readDeploy(runDir string) (*contract.SquadPublic, error) {
	raw, err := os.ReadFile(filepath.Join(runDir, contract.RunDeployFile))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", contract.RunDeployFile, err)
	}
	var sq contract.SquadPublic
	if err := json.Unmarshal(raw, &sq); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", contract.RunDeployFile, err)
	}
	if sq.Name == "" {
		return nil, fmt.Errorf("%s is not deploy output (no squad name)", contract.RunDeployFile)
	}
	return &sq, nil
}

// appliesTo reports whether a scenario or run restricted to platforms
// covers platform; no restriction covers every platform.
func appliesTo(platforms []string, platform string) bool {
	return len(platforms) == 0 || slices.Contains(platforms, platform)
}

// loadDevice resolves one device's worker outcome, as pilot's load_device:
// a status file overrides even a valid result; otherwise the result must
// pass `run validate`. Any structurally valid result is kept, partial or
// not, with its paths rewritten relative to the run dir.
func loadDevice(runDir string, v contract.DeviceView) contract.ReportDevice {
	dev := contract.ReportDevice{
		Platform:  string(v.Platform),
		ID:        v.ID,
		Name:      v.Name,
		Model:     v.Model,
		OSVersion: v.OSVersion,
		BundleID:  v.BundleID,
		Scenarios: []contract.WorkerScenario{},
		Findings:  []contract.WorkerFinding{},
	}
	wdir := filepath.Join(runDir, contract.RunWorkersDir, v.ID)
	status := readStatus(wdir)
	_, statErr := os.Stat(filepath.Join(wdir, contract.ResultFileName))
	hasResult := statErr == nil
	res, errs := ValidateWorkerDir(wdir)

	switch {
	case status != "":
		dev.WorkerStatus = statusKind(status)
		dev.WorkerReason = status
	case !hasResult:
		dev.WorkerStatus = contract.WorkerError
		dev.WorkerReason = "error: no " + contract.ResultFileName
	case len(errs) > 0:
		dev.WorkerStatus = contract.WorkerError
		dev.WorkerReason = "error: invalid result"
	default:
		dev.WorkerStatus = contract.WorkerOK
	}
	if hasResult && len(errs) > 0 {
		dev.ValidationErrors = errs
	}
	if res != nil {
		prefix := contract.RunWorkersDir + "/" + v.ID + "/"
		rewritePaths(res, prefix)
		if res.Scenarios != nil {
			dev.Scenarios = res.Scenarios
		}
		if res.Findings != nil {
			dev.Findings = res.Findings
		}
		dev.Exploration = res.Exploration
	}
	return dev
}

// readStatus returns the worker's status line, or "" when there is none.
func readStatus(wdir string) string {
	b, err := os.ReadFile(filepath.Join(wdir, StatusFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// statusKind maps a status line to a worker status: its prefix before ':'
// when that is blocked or error, else error (as pilot).
func statusKind(status string) string {
	kind, _, _ := strings.Cut(status, ":")
	if kind = strings.TrimSpace(kind); kind == contract.WorkerBlocked {
		return contract.WorkerBlocked
	}
	return contract.WorkerError
}

// rewritePaths prefixes worker-relative evidence and screenshot paths so
// they resolve from the run dir, where report.md sits.
func rewritePaths(res *contract.WorkerResult, prefix string) {
	for i := range res.Scenarios {
		prefixAll(res.Scenarios[i].Evidence, prefix)
	}
	for i := range res.Findings {
		prefixAll(res.Findings[i].Evidence, prefix)
	}
	if res.Exploration != nil {
		for i := range res.Exploration.Actions {
			if a := &res.Exploration.Actions[i]; a.Screenshot != "" {
				a.Screenshot = prefix + a.Screenshot
			}
		}
	}
}

func prefixAll(paths []string, prefix string) {
	for i := range paths {
		paths[i] = prefix + paths[i]
	}
}

// buildMatrix lays out the scenario × device matrix in run.json order, the
// exploration last. Results are matched to scenarios by name, each result
// used once, so expanded outline rows sharing a name map in order.
func buildMatrix(run *contract.RunRecord, devices []contract.ReportDevice) []contract.ReportMatrixRow {
	rows := make([]contract.ReportMatrixRow, 0, len(run.Scenarios)+1)
	for _, sc := range run.Scenarios {
		rows = append(rows, contract.ReportMatrixRow{Scenario: sc.Name, Results: map[string]string{}})
	}
	if run.Exploration != nil {
		rows = append(rows, contract.ReportMatrixRow{
			Scenario: run.Exploration.Name, Exploration: true, Results: map[string]string{},
		})
	}
	for _, d := range devices {
		used := make([]bool, len(d.Scenarios))
		for i, sc := range run.Scenarios {
			rows[i].Results[d.ID] = scenarioCell(sc.Platforms, d, used, sc.Name)
		}
		if run.Exploration != nil {
			rows[len(rows)-1].Results[d.ID] = explorationCell(run.Exploration.Platforms, d)
		}
	}
	return rows
}

// scenarioCell is the device's status for the named scenario: skipped when
// the scenario isn't for its platform, blocked when the worker didn't
// report it.
func scenarioCell(platforms []string, d contract.ReportDevice, used []bool, name string) string {
	if !appliesTo(platforms, d.Platform) {
		return contract.MatrixSkipped
	}
	for j, r := range d.Scenarios {
		if !used[j] && r.Name == name {
			used[j] = true
			return r.Status
		}
	}
	return contract.ScenarioBlocked
}

// explorationCell maps the exploration's status onto the matrix: completed
// is passed; blocked, error or a missing section is blocked.
func explorationCell(platforms []string, d contract.ReportDevice) string {
	if !appliesTo(platforms, d.Platform) {
		return contract.MatrixSkipped
	}
	if d.Exploration != nil && d.Exploration.Status == "completed" {
		return contract.ScenarioPassed
	}
	return contract.ScenarioBlocked
}

// countReport totals the matrix cells and every device's findings.
func countReport(rep *contract.Report) contract.ReportCounts {
	c := contract.ReportCounts{Devices: len(rep.Devices)}
	for _, d := range rep.Devices {
		if d.WorkerStatus == contract.WorkerOK {
			c.WorkersOK++
		}
		for _, f := range d.Findings {
			switch f.Type {
			case contract.FindingBug:
				c.Bugs++
			case contract.FindingQuestion:
				c.Questions++
			case contract.FindingNote:
				c.Notes++
			}
		}
	}
	for _, row := range rep.Matrix {
		for _, cell := range row.Results {
			switch cell {
			case contract.ScenarioPassed:
				c.ScenariosPassed++
			case contract.ScenarioFailed:
				c.ScenariosFailed++
			case contract.ScenarioBlocked:
				c.ScenariosBlocked++
			}
		}
	}
	return c
}

// decideVerdict applies the spec's rules. infra: deploy failed, no ready
// device, or no worker left a usable result. passed: every scenario passed
// on every device, zero bugs and every worker ok. Anything else is failed —
// including blocked scenarios and partial worker loss, which pilot also
// counted as failed: neither is "every scenario passed", and neither is an
// infrastructure-only outcome while some worker did test the app.
func decideVerdict(rep *contract.Report, deployErr error, workersRan bool) (string, string) {
	c := rep.Summary.Counts
	switch {
	case deployErr != nil:
		return contract.VerdictInfra, "deploy failed: " + deployErr.Error()
	case c.Devices == 0:
		return contract.VerdictInfra, "no ready device"
	case !workersRan:
		return contract.VerdictInfra, "no worker results"
	case c.WorkersOK == 0:
		return contract.VerdictInfra, "every worker was blocked or errored"
	case c.ScenariosFailed > 0 || c.ScenariosBlocked > 0 || c.Bugs > 0 || c.WorkersOK < c.Devices:
		return contract.VerdictFailed, ""
	default:
		return contract.VerdictPassed, ""
	}
}

// workersRan reports whether any worker dir exists in the run.
func workersRan(runDir string) bool {
	entries, err := os.ReadDir(filepath.Join(runDir, contract.RunWorkersDir))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			return true
		}
	}
	return false
}

var (
	reportSchemaOnce sync.Once
	reportSchema     *jsonschema.Schema
	reportSchemaErr  error
)

// compiledReportSchema compiles the embedded report schema, with the
// result schema it $refs, once per process.
func compiledReportSchema() (*jsonschema.Schema, error) {
	reportSchemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		for url, src := range map[string][]byte{
			contract.ResultSchemaURL: contract.ResultSchema,
			contract.ReportSchemaURL: contract.ReportSchema,
		} {
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(src))
			if err == nil {
				err = c.AddResource(url, doc)
			}
			if err != nil {
				reportSchemaErr = fmt.Errorf("embedded schema %s: %w", url, err)
				return
			}
		}
		reportSchema, reportSchemaErr = c.Compile(contract.ReportSchemaURL)
	})
	return reportSchema, reportSchemaErr
}

// ValidateReport checks report.json against the embedded report schema
// and, when it is structurally valid, that summary.counts match the matrix
// and devices (pilot's check_report_semantics). An empty slice means valid.
func ValidateReport(raw []byte) []string {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return []string{fmt.Sprintf("cannot parse report: %v", err)}
	}
	sch, err := compiledReportSchema()
	if err != nil {
		return []string{err.Error()}
	}
	if err := sch.Validate(inst); err != nil {
		return schemaErrors(err)
	}
	var rep contract.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return []string{fmt.Sprintf("cannot decode report: %v", err)}
	}
	if want := countReport(&rep); want != rep.Summary.Counts {
		return []string{fmt.Sprintf("$.summary.counts: %+v does not match the report, which has %+v",
			rep.Summary.Counts, want)}
	}
	return []string{}
}

// ErrInvalidReport wraps a self-check failure of a built report: a bug in
// simsquad, not in the run.
var ErrInvalidReport = errors.New("built report is invalid")
