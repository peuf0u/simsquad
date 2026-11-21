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
// contract.SecretMask. It holds output back until a line ends (or
// maxPartialLine piles up), then writes the buffer up to a cut that keeps
// its last longest-1 bytes back, raw: they could be the start of a secret
// the next write completes, a secret with a newline in it included. A
// secret found whole across the cut moves the cut back to its start. The
// buffer is scanned left to right, trying the longest secret first at each
// byte, and every byte before the cut is decided by bytes already there,
// so the log is the same however the worker's writes split its output.
// Flush writes the rest.
//
// Each secret is also matched in its JSON-escaped form: simsquad prints a
// secret that way (`devices --reveal` turns `pa"ss` into `pa\"ss`), and a
// worker log repeats that output.
type lineRedactor struct {
	w       io.Writer
	secrets [][]byte // longest first, so a secret containing another goes whole
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
	last := 0
	for i := 0; i < cut; {
		s := r.matchAt(i)
		if s == nil {
			i++
			continue
		}
		if i+len(s) > cut {
			// Found whole but across the cut: keep it back with the tail.
			cut = i
			break
		}
		out = append(out, r.buf[last:i]...)
		out = append(out, contract.SecretMask...)
		i += len(s)
		last = i
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

// matchAt returns the longest secret found whole at r.buf[i:], or nil.
func (r *lineRedactor) matchAt(i int) []byte {
	for _, s := range r.secrets {
		if bytes.HasPrefix(r.buf[i:], s) {
			return s
		}
	}
	return nil
}
