package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/state"
)

func newDevicesCmd() *cobra.Command {
	var (
		name        string
		readyOnly   bool
		platformStr string
	)

	cmd := &cobra.Command{
		Use:   "devices",
		Short: "Emit the device list for one squad or all squads",
		Args:  cobra.NoArgs,
		Long: "Pure-query verb: prints a uniform per-device view (platform, id, name,\n" +
			"model, os_version, status, ready_at) on stdout. Does not touch the lease.\n\n" +
			"With --name, emits {name, devices} for that squad. Without --name, emits\n" +
			"{squads: [{name, devices}, ...]} across every squad. Use --ready to filter\n" +
			"to ready devices, --platform to filter by OS.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDevices(cmd, name, readyOnly, platformStr)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "squad to query (omit to list every squad's devices)")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "include only devices with status=ready")
	cmd.Flags().StringVar(&platformStr, "platform", "", "restrict to one platform: ios or android")
	return cmd
}

func runDevices(cmd *cobra.Command, name string, readyOnly bool, platformStr string) error {
	wantPlatform, err := parseDevicePlatform(platformStr)
	if err != nil {
		return err
	}

	// Whole-fleet view: no --name → every squad's devices, grouped by squad,
	// mirroring how `status` (no args) lists every squad.
	if name == "" {
		entries, err := state.ListSquads()
		if err != nil {
			return fmt.Errorf("devices: list squads: %w", err)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		groups := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			rec, err := state.LoadRecord(entry.Name)
			if err != nil || rec == nil {
				continue // tolerate a stale registry entry with no state file
			}
			groups = append(groups, map[string]any{
				"name":    rec.Name,
				"devices": deviceViews(rec, readyOnly, wantPlatform),
			})
		}
		return writeJSON(cmd, map[string]any{"squads": groups})
	}

	entry, ok, err := state.FindSquad(canonName(name))
	if err != nil {
		return fmt.Errorf("devices: lookup squad: %w", err)
	}
	if !ok {
		return fmt.Errorf("devices: no squad named %q", name)
	}
	rec, err := state.LoadRecord(entry.Name)
	if err != nil {
		return fmt.Errorf("devices: load record %s: %w", entry.Name, err)
	}
	if rec == nil {
		return fmt.Errorf("devices: no state file for squad %s", entry.Name)
	}
	return writeJSON(cmd, map[string]any{
		"name":    rec.Name,
		"devices": deviceViews(rec, readyOnly, wantPlatform),
	})
}

// deviceViews projects a squad's devices into DeviceViews, applying the ready
// and platform filters. Always returns a non-nil slice so the JSON emits `[]`
// rather than `null` for an empty result.
func deviceViews(rec *contract.SquadRecord, readyOnly bool, wantPlatform contract.Platform) []contract.DeviceView {
	views := make([]contract.DeviceView, 0, len(rec.Devices))
	for _, d := range rec.Devices {
		if readyOnly && d.Status != contract.StatusReady {
			continue
		}
		if wantPlatform != "" && d.Platform != wantPlatform {
			continue
		}
		views = append(views, contract.NewDeviceView(d, rec.CreatedAt, rec.Env))
	}
	return views
}

func parseDevicePlatform(platformStr string) (contract.Platform, error) {
	switch platformStr {
	case "":
		return "", nil
	case "ios":
		return contract.PlatformIOS, nil
	case "android":
		return contract.PlatformAndroid, nil
	default:
		return "", fmt.Errorf("devices: --platform must be ios or android, got %q", platformStr)
	}
}
