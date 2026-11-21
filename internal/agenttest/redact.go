package agenttest

import (
	"bytes"
	"encoding/json"
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
// contract.SecretMask. Secrets that overlap or touch in the output are
// masked as one: every byte any secret's match covers is hidden, with one
// mask per contiguous run, so masking one secret never leaves the rest of
// another readable (secrets "xyz12" and "12345678" in "xyz12345678").
//
// It holds output back until a line ends (or maxPartialLine piles up),
// then writes the buffer up to a cut that keeps its last longest-1 bytes
// back, raw: they could be the start of a secret the next write completes,
// a secret with a newline in it included. Every match starting before the
// cut is then found whole, and a run that reaches the cut moves the cut
// back to the run's start, since a match the next write completes could
// extend it. So every byte before the cut is decided by bytes already
// there, and the log is the same however the worker's writes split its
// output, except for a run held back past maxPartialLine (see emit). Flush
// writes the rest.
//
// Each secret is also matched in its JSON-escaped form: simsquad prints a
// secret that way (`devices --reveal` turns `pa"ss` into `pa\"ss`), and a
// worker log repeats that output.
type lineRedactor struct {
	w       io.Writer
	secrets [][]byte // longest first (matchAt)
	longest int
	buf     []byte
}

// newLineRedactor redacts the given secret values and their JSON-escaped
// forms; empty values are ignored.
func newLineRedactor(w io.Writer, secrets []string) *lineRedactor {
	r := &lineRedactor{w: w}
	add := func(b []byte) {
		for _, have := range r.secrets {
			if bytes.Equal(have, b) {
				return
			}
		}
		r.secrets = append(r.secrets, b)
		r.longest = max(r.longest, len(b))
	}
	for _, s := range secrets {
		if s == "" {
			continue
		}
		add([]byte(s))
		for _, esc := range jsonEscaped(s) {
			add(esc)
		}
	}
	sort.SliceStable(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
	return r
}

// jsonEscaped returns s as a JSON string's contents, without the quotes:
// once as simsquad's own encoder writes it (HTML characters verbatim) and
// once as json.Marshal does (HTML characters as \u003c…).
func jsonEscaped(s string) [][]byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	plain := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	marshalled, _ := json.Marshal(s)
	return [][]byte{plain[1 : len(plain)-1], marshalled[1 : len(marshalled)-1]}
}

// Write takes the worker's output. It reports the whole of p as written once
// it is buffered; an error is the log file's.
func (r *lineRedactor) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	if bytes.IndexByte(p, '\n') >= 0 || len(r.buf) > maxPartialLine {
		if err := r.emit(false); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush writes what is left.
func (r *lineRedactor) Flush() error { return r.emit(true) }

// emit masks and writes the buffer up to the cut (all of it when final) and
// keeps the rest, raw.
func (r *lineRedactor) emit(final bool) error {
	cut := len(r.buf)
	if !final {
		cut -= r.longest - 1
	}
	if cut <= 0 {
		return nil
	}
	var out []byte
	last := 0            // end of the buffer already copied or masked into out
	start, end := -1, -1 // the current run of covered bytes, [start, end)
	mask := func() {
		out = append(out, r.buf[last:start]...)
		out = append(out, contract.SecretMask...)
		last = end
	}
	for i := 0; i < cut; i++ {
		s := r.matchAt(i)
		if s == nil {
			continue
		}
		if start >= 0 && i <= end {
			end = max(end, i+len(s))
			continue
		}
		if start >= 0 {
			mask()
		}
		start, end = i, i+len(s)
	}
	if start >= 0 {
		switch {
		case final || end < cut:
			mask()
		case start == 0 && len(r.buf) > maxPartialLine:
			// A chain of touching matches ("ab" in "ababab…" with no
			// newline) reaches the cut on every write and would be held
			// back, rescanned and grown without limit. Past maxPartialLine
			// the run goes out masked up to its end, taking in every whole
			// match that starts inside it. Only in this degenerate case can
			// the log depend on how the writes split: a match touching the
			// run's end that the next write completes gets a mask of its
			// own, and the rest of a secret that starts inside the run but
			// isn't whole yet is no longer recognised.
			for i := cut; i < end; i++ {
				if s := r.matchAt(i); s != nil {
					end = max(end, i+len(s))
				}
			}
			mask()
			cut = end
		default:
			// The run reaches the cut: keep it back with the tail.
			cut = start
		}
	}
	out = append(out, r.buf[last:cut]...)
	if len(out) > 0 {
		if _, err := r.w.Write(out); err != nil {
			return err
		}
	}
	r.buf = append(r.buf[:0], r.buf[cut:]...)
	return nil
}

// matchAt returns the longest secret found whole at r.buf[i:], or nil. The
// longest match reaches furthest, which is all a run needs.
func (r *lineRedactor) matchAt(i int) []byte {
	for _, s := range r.secrets {
		if bytes.HasPrefix(r.buf[i:], s) {
			return s
		}
	}
	return nil
}
