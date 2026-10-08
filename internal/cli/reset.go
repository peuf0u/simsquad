package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/android"
	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/ios"
	"github.com/peuf0u/simsquad/internal/progress"
	"github.com/peuf0u/simsquad/internal/state"
)

func newResetCmd() *cobra.Command {
	var (
		name    string
		devices []string
	)

	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Return apps to a clean state on a squad's devices (terminate + wipe data)",
		Args:  cobra.NoArgs,
		Long: "Terminates the installed app and wipes its data container on each ready\n" +
			"device in the squad, without re-provisioning — a consumer gets a virgin\n" +
			"app instance between test scenarios. iOS wipes the data container; Android\n" +
			"runs `pm clear`. Restrict to specific devices with --device (repeatable);\n" +
			"the default is every ready device. Emits the reset device ids as JSON.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReset(cmd, name, devices)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "squad to reset (required)")
	cmd.Flags().StringArrayVar(&devices, "device", nil, "restrict to a device id (repeatable; default all ready)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

type resetFailure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

func runReset(cmd *cobra.Command, name string, only []string) error {
	if name == "" {
		return errors.New("reset: --name is required")
	}
	name = canonName(name)

	entry, ok, err := state.FindSquad(name)
	if err != nil {
		return fmt.Errorf("reset: lookup squad: %w", err)
	}
	if !ok {
		return fmt.Errorf("reset: no squad named %q", name)
	}
	rec, err := state.LoadRecord(entry.Name)
	if err != nil {
		return fmt.Errorf("reset: load record %s: %w", entry.Name, err)
	}
	if rec == nil {
		return fmt.Errorf("reset: no state file for squad %s", entry.Name)
	}

	// Validate any explicit --device ids against the squad up front, so a typo
	// fails loudly instead of silently resetting nothing.
	want := map[string]bool{}
	for _, id := range only {
		want[id] = true
	}
	if len(want) > 0 {
		known := map[string]bool{}
		for _, dev := range rec.Devices {
			known[dev.UDID] = true
		}
		for _, id := range only {
			if !known[id] {
				return fmt.Errorf("reset: device %q is not in squad %s", id, entry.Name)
			}
		}
	}

	logger := progress.New()
	defer logger.Close()

	resetIDs := []string{}
	var failures []resetFailure
	matched := 0

	for _, dev := range rec.Devices {
		if dev.Status != contract.StatusReady || dev.UDID == "" {
			continue
		}
		if len(want) > 0 && !want[dev.UDID] {
			continue
		}
		matched++
		done := logger.Step("reset", progress.F("id", dev.UDID), progress.F("platform", string(dev.Platform)))
		var rerr error
		switch dev.Platform {
		case contract.PlatformIOS:
			rerr = ios.ResetApp(dev.UDID, dev.BundleID)
		case contract.PlatformAndroid:
			rerr = android.ResetApp(dev.UDID, dev.BundleID)
		default:
			rerr = fmt.Errorf("unknown platform %q", dev.Platform)
		}
		done(rerr)
		if rerr != nil {
			failures = append(failures, resetFailure{ID: dev.UDID, Error: rerr.Error()})
			continue
		}
		resetIDs = append(resetIDs, dev.UDID)
	}

	if matched == 0 {
		return fmt.Errorf("reset: squad %s has no ready devices to reset", entry.Name)
	}
	logger.Close()

	out := map[string]any{"name": entry.Name, "reset": resetIDs}
	if len(failures) > 0 {
		out["errors"] = failures
	}
	return writeJSON(cmd, out)
}
