package contract

// WorkerStatus is the stdout of `simsquad run worker`. Status is "ok" when
// the worker exited 0 in time and left a result.json, otherwise the line
// written to the worker's status file: "blocked: timeout",
// "error: no result.json", "error: usage limit" or
// "error: worker exited <code>".
type WorkerStatus struct {
	Status string `json:"status"`
}
