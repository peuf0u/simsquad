package agenttest

import (
	"bytes"
	"io"
	"sort"

	"github.com/peuf0u/simsquad/internal/contract"
)

// maxPartialLine bounds how much of an unfinished line lineRedactor holds
// back. Output that never ends a line (a spinner redrawn with \r) is still
// written once this much has piled up, minus a tail long enough to hold the
// start of a secret.
const maxPartialLine = 64 << 10

// lineRedactor writes to w with every secret value replaced by
// contract.SecretMask. It works line by line, so a secret the worker writes
// in pieces (one write ends inside it) is still caught whole. Flush writes
// whatever is left once the worker is done.
type lineRedactor struct {
	w       io.Writer
	secrets [][]byte // longest first, so a secret containing another goes whole
	longest int
	buf     []byte
}

// newLineRedactor redacts the given secret values; empty ones are ignored.
func newLineRedactor(w io.Writer, secrets []string) *lineRedactor {
	r := &lineRedactor{w: w}
	for _, s := range secrets {
		if s == "" {
			continue
		}
		r.secrets = append(r.secrets, []byte(s))
		r.longest = max(r.longest, len(s))
	}
	sort.SliceStable(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
	return r
}

// Write takes the worker's output. It reports the whole of p as written once
// it is buffered; an error is the log file's.
func (r *lineRedactor) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	if i := bytes.LastIndexByte(r.buf, '\n'); i >= 0 {
		if err := r.emit(i + 1); err != nil {
			return 0, err
		}
	}
	if len(r.buf) > maxPartialLine {
		// Keep back what could be the start of a secret that the next
		// write completes.
		if err := r.emit(len(r.buf) - (r.longest - 1)); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush writes the unfinished last line.
func (r *lineRedactor) Flush() error { return r.emit(len(r.buf)) }

// emit redacts and writes the first n buffered bytes.
func (r *lineRedactor) emit(n int) error {
	if n <= 0 {
		return nil
	}
	out := r.buf[:n]
	for _, s := range r.secrets {
		out = bytes.ReplaceAll(out, s, []byte(contract.SecretMask))
	}
	if _, err := r.w.Write(out); err != nil {
		return err
	}
	r.buf = append(r.buf[:0], r.buf[n:]...)
	return nil
}
