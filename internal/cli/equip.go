package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/wizard"
)

func newEquipCmd() *cobra.Command {
	var (
		force       bool
		show        bool
		iosSpecs    []string
		androidSpec []string
	)

	cmd := &cobra.Command{
		Use:   "equip",
		Short: "Show or edit simsquad.toml + simsquad.local.toml",
		Args:  cobra.NoArgs,
		Long: "Without config, opens the setup wizard. With existing config, opens a\n" +
			"config editor that previews current project paths and matrix rows before\n" +
			"saving simsquad.toml and simsquad.local.toml.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			addIOS, err := parseEquipIOSSpecs(iosSpecs)
			if err != nil {
				return err
			}
			addAndroid, err := parseEquipAndroidSpecs(androidSpec)
			if err != nil {
				return err
			}
			res, err := wizard.RunEquipWizard(wizard.WizardOptions{
				Force:      force,
				Show:       show,
				AddIOS:     addIOS,
				AddAndroid: addAndroid,
			})
			if err != nil {
				return err
			}
			if !res.Wrote {
				return writeJSON(cmd, res.Summary)
			}
			return writeJSON(cmd, map[string]any{
				"written": []string{res.ProjectFile, res.LocalFile},
				"gitignore": map[string]string{
					"path":  res.Gitignore,
					"entry": "simsquad.local.toml",
				},
				"config": res.Summary,
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing simsquad TOML files when creating config")
	cmd.Flags().BoolVar(&show, "show", false, "print merged simsquad config as JSON without opening the editor")
	cmd.Flags().StringArrayVar(&iosSpecs, "add-ios-spec", nil, "append an iOS slot spec: 'DEVICE:RUNTIME:COUNT'")
	cmd.Flags().StringArrayVar(&androidSpec, "add-android-spec", nil, "append an Android slot spec: 'DEVICE:IMAGE:COUNT[:TARGET]'")
	return cmd
}

func parseEquipIOSSpecs(raw []string) ([]contract.IosSpec, error) {
	out := make([]contract.IosSpec, 0, len(raw))
	for _, value := range raw {
		spec, err := parseIosSpec(value)
		if err != nil {
			return nil, fmt.Errorf("equip: --add-ios-spec %q: %w", value, err)
		}
		out = append(out, spec)
	}
	return out, nil
}

func parseEquipAndroidSpecs(raw []string) ([]contract.AndroidSpec, error) {
	out := make([]contract.AndroidSpec, 0, len(raw))
	for _, value := range raw {
		spec, err := parseAndroidSpec(value)
		if err != nil {
			return nil, fmt.Errorf("equip: --add-android-spec %q: %w", value, err)
		}
		out = append(out, spec)
	}
	return out, nil
}
