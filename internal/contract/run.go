package contract

// RunRecordFile is the name of the run's details file inside its run folder
// (<repo>/.simsquad/runs/<run-id>/run.json), written by `run new` and read by
// `run report`.
const RunRecordFile = "run.json"

// RunFeatureFile is the name of the verbatim feature-file copy inside the run
// folder.
const RunFeatureFile = "feature.feature"

// RunReportFile marks a run as finished: `run new` refuses a second run on a
// squad while an earlier run on it has no report.json yet.
const RunReportFile = "report.json"

// RunNewOutput is the stdout of `simsquad run new`.
type RunNewOutput struct {
	RunID           string   `json:"run_id"`
	RunDir          string   `json:"run_dir"`
	SquadName       string   `json:"squad_name"`
	Fresh           bool     `json:"fresh"`
	Platforms       []string `json:"platforms"`
	DeadlineSeconds int      `json:"deadline_seconds"`
	WorkerModel     string   `json:"worker_model"`
}

// RunRecord is run.json: everything `run new` decided about a test run, so
// later steps (`run report`, the skill) never re-parse the feature file.
type RunRecord struct {
	RunID        string `json:"run_id"`
	CreatedAt    string `json:"created_at"`
	FeatureTitle string `json:"feature_title"`
	// FeatureFile is the path the run was started from, as given on the
	// command line. The run folder holds a copy at RunFeatureFile.
	FeatureFile string `json:"feature_file"`
	// Source is the value of the feature description's `Source:` line
	// (ticket key, issue URL or free text); empty when absent.
	Source          string   `json:"source,omitempty"`
	SquadName       string   `json:"squad_name"`
	Fresh           bool     `json:"fresh"`
	Platforms       []string `json:"platforms"`
	DeadlineSeconds int      `json:"deadline_seconds"`
	WorkerModel     string   `json:"worker_model"`
	// Scenarios are the scripted scenarios in file order, with Background
	// steps prepended and Scenario Outlines expanded one per Examples row.
	Scenarios []RunScenario `json:"scenarios"`
	// Exploration is the `@explore` scenario, if the feature has one.
	Exploration *RunExploration `json:"exploration,omitempty"`
}

// RunScenario is one executable scenario of a test run.
type RunScenario struct {
	Name string `json:"name"`
	// Steps carry their Gherkin keyword ("Given …", "And …"); doc strings
	// and data tables follow their step as extra lines.
	Steps []string `json:"steps"`
	// Tags include those inherited from the feature, with the leading '@'.
	// Tags simsquad doesn't act on are kept here and otherwise ignored.
	Tags []string `json:"tags"`
	// Platforms are the run's platforms this scenario applies to.
	Platforms []string `json:"platforms"`
}

// RunExploration is the `@explore` scenario: its description is the charter
// (including any risks) that guides timeboxed free testing.
type RunExploration struct {
	Name      string   `json:"name"`
	Charter   string   `json:"charter"`
	Steps     []string `json:"steps,omitempty"`
	Tags      []string `json:"tags"`
	Platforms []string `json:"platforms"`
}
