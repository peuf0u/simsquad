package contract

import _ "embed"

// ReportSchema is the JSON Schema (draft 2020-12) report.json satisfies. It
// $refs ResultSchema's $defs by relative URL, so validators register both
// schemas under their $ids.
//
//go:embed schemas/report.schema.json
var ReportSchema []byte

// ReportSchemaURL is the $id of ReportSchema.
const ReportSchemaURL = "https://github.com/peuf0u/simsquad/schemas/report.schema.json"

// RunReportMarkdown is the human report `run report` renders next to
// report.json (RunReportFile).
const RunReportMarkdown = "report.md"

// RunDeployFile is the stdout of `simsquad deploy` (SquadPublic) that the
// skill redirects into the run folder; `run report` takes the device rows
// and the env from it.
const RunDeployFile = "deploy.json"

// RunWorkersDir holds one worker dir per device, named by device id.
const RunWorkersDir = "workers"

// Verdicts of a test run, also `run report`'s exit code: passed 0,
// failed 1, infra 2.
const (
	VerdictPassed = "passed"
	VerdictFailed = "failed"
	VerdictInfra  = "infra"
)

// Worker statuses on a report device row: ok, or the kind of the line in
// the worker's status file. A missing or invalid result.json is "error".
const (
	WorkerOK      = "ok"
	WorkerBlocked = "blocked"
	WorkerError   = "error"
)

// MatrixSkipped is the matrix cell of a scenario that doesn't apply to the
// device's platform. The other cells are the scenario statuses.
const MatrixSkipped = "skipped"

// Report is report.json: one test run's results across the squad, with the
// verdict. Paths inside it are relative to the run dir.
type Report struct {
	RunID        string `json:"run_id"`
	FeatureTitle string `json:"feature_title"`
	FeatureFile  string `json:"feature_file"`
	// Source is the feature's `Source:` line as written (ticket key, URL or
	// free text); empty when the feature has none.
	Source     string        `json:"source,omitempty"`
	StartedAt  string        `json:"started_at"`
	FinishedAt string        `json:"finished_at"`
	Squad      ReportSquad   `json:"squad"`
	Summary    ReportSummary `json:"summary"`
	// Matrix has one row per scenario in run.json order, the exploration
	// last, and one cell per device.
	Matrix  []ReportMatrixRow `json:"matrix"`
	Devices []ReportDevice    `json:"devices"`
	// Undeployed lists the devices of the run's platforms that deploy did
	// not get ready; they had no worker and don't count toward the verdict.
	Undeployed []ReportUndeployed `json:"undeployed,omitempty"`
}

// ReportSquad names the squad and carries its env verbatim from deploy.json.
type ReportSquad struct {
	Name string            `json:"name"`
	Env  map[string]string `json:"env"`
}

// ReportSummary is the verdict plus the counts it was decided from. Reason
// explains an infra verdict.
type ReportSummary struct {
	Verdict string       `json:"verdict"`
	Reason  string       `json:"reason,omitempty"`
	Counts  ReportCounts `json:"counts"`
}

// ReportCounts are the run's totals. Scenario counts are matrix cells
// (scenario × device), skipped cells excluded; finding counts span every
// device, including partial results of blocked or errored workers.
type ReportCounts struct {
	Devices          int `json:"devices"`
	WorkersOK        int `json:"workers_ok"`
	ScenariosPassed  int `json:"scenarios_passed"`
	ScenariosFailed  int `json:"scenarios_failed"`
	ScenariosBlocked int `json:"scenarios_blocked"`
	Bugs             int `json:"bugs"`
	Questions        int `json:"questions"`
	Notes            int `json:"notes"`
}

// ReportMatrixRow is one scenario's status on every device, keyed by device
// id: passed, failed, blocked, or skipped. A scenario a worker didn't report
// (it died, or skipped it) is blocked; a completed exploration is passed.
type ReportMatrixRow struct {
	Scenario    string            `json:"scenario"`
	Exploration bool              `json:"exploration,omitempty"`
	Results     map[string]string `json:"results"`
}

// ReportDevice is one ready device with its worker's outcome and results.
// A blocked or errored worker keeps whatever valid partial result it left.
type ReportDevice struct {
	Platform     string `json:"platform"`
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	Model        string `json:"model,omitempty"`
	OSVersion    string `json:"os_version,omitempty"`
	BundleID     string `json:"bundle_id,omitempty"`
	WorkerStatus string `json:"worker_status"`
	// WorkerReason is the status-file line, or why the result was unusable.
	WorkerReason string `json:"worker_reason,omitempty"`
	// ValidationErrors are `run validate`'s errors for an invalid result.
	ValidationErrors []string           `json:"validation_errors,omitempty"`
	Scenarios        []WorkerScenario   `json:"scenarios"`
	Findings         []WorkerFinding    `json:"findings"`
	Exploration      *WorkerExploration `json:"exploration,omitempty"`
}

// ReportUndeployed is a device deploy reported but couldn't get ready.
type ReportUndeployed struct {
	Platform string `json:"platform"`
	Name     string `json:"name"`
	ID       string `json:"id,omitempty"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

// ReportOutput is the stdout of `simsquad run report`.
type ReportOutput struct {
	Verdict    string       `json:"verdict"`
	ReportJSON string       `json:"report_json"`
	ReportMD   string       `json:"report_md"`
	Counts     ReportCounts `json:"counts"`
}
