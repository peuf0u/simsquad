package util

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// RunResult mirrors Python's util.RunResult: an exit code plus captured
// stdout/stderr buffers as strings.
type RunResult struct {
	Code   int
	Stdout string
	Stderr string
}

// RunOpts tunes a single Run call. Zero-value Opts means "no timeout, current
// working dir, default environment, capture output".
type RunOpts struct {
	Dir       string
	Env       []string // os/exec semantics: nil = inherit, []string{} = empty
	Timeout   time.Duration
	NoCapture bool // when true, inherit stdio (use for interactive subcommands)
	Check     bool // when true, non-zero exit returns an error
}

// Run invokes a subprocess with the given args. The first element is the
// binary; subsequent elements are arguments. Returns a populated RunResult
// even on non-zero exit (callers can choose to ignore the code).
//
// On Timeout > 0, a context is used to kill the process; the returned
// RunResult.Code is -1 in that case and the wrapped error is context.DeadlineExceeded.
func Run(name string, args []string, opts RunOpts) (RunResult, error) {
	ctx := context.Background()
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, name, args...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if opts.Env != nil {
		cmd.Env = opts.Env
	}

	var outBuf, errBuf bytes.Buffer
	if !opts.NoCapture {
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
	}

	runErr := cmd.Run()

	r := RunResult{
		Stdout: outBuf.String(),
		Stderr: errBuf.String(),
	}

	if cmd.ProcessState != nil {
		r.Code = cmd.ProcessState.ExitCode()
	} else {
		r.Code = -1
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return r, fmt.Errorf("%s timed out after %s: %w", name, opts.Timeout, ctx.Err())
	}
	if opts.Check && runErr != nil {
		return r, fmt.Errorf("%s exited %d: %w", name, r.Code, runErr)
	}
	return r, nil
}

// WhichRequired returns the absolute path to binary or a friendly error if it
// isn't on $PATH.
func WhichRequired(binary string) (string, error) {
	p, err := exec.LookPath(binary)
	if err != nil {
		return "", fmt.Errorf("required tool not on PATH: %s", binary)
	}
	return p, nil
}
