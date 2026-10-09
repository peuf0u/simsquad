package contract

import _ "embed"

// ResultSchema is the JSON Schema (draft 2020-12) a worker's result.json must
// satisfy. It is embedded so the binary validates results without any file
// on disk; ResultSchemaURL is its $id, under which validators register it
// (the report schema will $ref its $defs by that URL).
//
//go:embed schemas/result.schema.json
var ResultSchema []byte

// ResultSchemaURL is the $id of ResultSchema.
const ResultSchemaURL = "https://github.com/peuf0u/simsquad/schemas/result.schema.json"

// ResultFileName is the file a worker writes into its worker dir.
const ResultFileName = "result.json"

// Scenario result statuses (WorkerScenario.Status).
const (
	ScenarioPassed  = "passed"
	ScenarioFailed  = "failed"
	ScenarioBlocked = "blocked"
)

// Finding types (WorkerFinding.Type). "blocked" is a scenario status, not a
// finding type.
const (
	FindingBug      = "bug"
	FindingQuestion = "question"
	FindingNote     = "note"
)

// WorkerResult is a worker's result.json: one device's scenario results,
// findings and exploration. Its shape is fixed by ResultSchema; paths in
// Evidence and Screenshot are relative to the worker dir. Exploration is nil
// unless the run has an @explore scenario.
type WorkerResult struct {
	Device      WorkerDevice       `json:"device"`
	Scenarios   []WorkerScenario   `json:"scenarios"`
	Findings    []WorkerFinding    `json:"findings"`
	Exploration *WorkerExploration `json:"exploration,omitempty"`
}

// WorkerDevice identifies the device a result was produced on. The schema
// allows extra keys; only these are typed.
type WorkerDevice struct {
	Platform  string `json:"platform"`
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Model     string `json:"model,omitempty"`
	OSVersion string `json:"os_version,omitempty"`
	BundleID  string `json:"bundle_id,omitempty"`
}

// WorkerScenario is one scenario's outcome on the worker's device.
type WorkerScenario struct {
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Observation string   `json:"observation"`
	Evidence    []string `json:"evidence,omitempty"`
}

// WorkerExploration is the timeboxed free-testing section of an @explore
// scenario. Findings made while exploring go in WorkerResult.Findings.
type WorkerExploration struct {
	Status        string         `json:"status"`
	BlockedReason string         `json:"blocked_reason,omitempty"`
	Actions       []WorkerAction `json:"actions"`
}

// WorkerAction is one step the worker took while exploring.
type WorkerAction struct {
	Index       int    `json:"index"`
	Kind        string `json:"kind"`
	Target      string `json:"target,omitempty"`
	Observation string `json:"observation"`
	Screenshot  string `json:"screenshot,omitempty"`
}

// WorkerFinding is a bug, question or note backed by screenshot evidence.
type WorkerFinding struct {
	Type       string   `json:"type"`
	Severity   string   `json:"severity,omitempty"`
	Title      string   `json:"title"`
	Evidence   []string `json:"evidence,omitempty"`
	ReproSteps []string `json:"repro_steps,omitempty"`
	Expected   string   `json:"expected,omitempty"`
	Actual     string   `json:"actual,omitempty"`
}

// Validation is the stdout of `simsquad run validate`. Errors is always a
// list (never null); Valid is true exactly when it is empty.
type Validation struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
}
