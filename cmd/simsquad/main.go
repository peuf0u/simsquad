// Command simsquad is the entrypoint for the device-squad provisioner.
//
// The binary delegates argument parsing to cobra (via fang for prettier
// help/errors) and dispatches to the verbs defined in internal/cli.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/charmbracelet/fang"

	"github.com/peuf0u/simsquad/internal/cli"
)

// Linked at release time via -ldflags by goreleaser. `go install` builds don't
// get ldflags, so resolveVersion falls back to the module build info.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	_ = date // reserved for a future `simsquad version` verb

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	resolveVersion()
	opts := []fang.Option{fang.WithVersion(version)}
	if commit != "" {
		opts = append(opts, fang.WithCommit(commit))
	}

	if err := fang.Execute(ctx, cli.NewRootCmd(), opts...); err != nil {
		// fang already prints a styled error; just propagate the exit code.
		os.Exit(1)
	}
}

// resolveVersion fills version/commit from the embedded build info when
// ldflags didn't set them. `go install …@v0.2.0` records the module version
// there; local `go build` records "(devel)" plus VCS stamps.
func resolveVersion() {
	if version != "dev" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		version = v
	}
	if commit == "" {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				commit = s.Value
			}
		}
	}
}
