package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/skills"
	"github.com/peuf0u/simsquad/internal/wizard"
)

func newEquipCmd() *cobra.Command {
	var (
		force         bool
		show          bool
		installSkills bool
		iosSpecs      []string
		androidSpec   []string
	)

	cmd := &cobra.Command{
		Use:   "equip",
		Short: "Show or edit simsquad.toml + simsquad.local.toml",
		Args:  cobra.NoArgs,
		Long: "Without config, opens the setup wizard. With existing config, opens a\n" +
			"config editor that previews current project paths and matrix rows before\n" +
			"saving simsquad.toml and simsquad.local.toml.\n" +
			"\n" +
			"After saving, equip offers to install the agent skills into the repo (the\n" +
			"same as `simsquad skill install`). The offer is skipped when stdin is not\n" +
			"a terminal or the skills are already current; --install-skills answers it\n" +
			"without asking. Redirect stdout only (> equip.json).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			addIOS, err := parseEquipIOSSpecs(iosSpecs)
			if err != nil {
				return err
			}
			addAndroid, err := parseEquipAndroidSpecs(androidSpec)
			if err != nil {
				return err
			}
			res, err := wizard.RunEquipWizard(wizard.Options{
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
			out := map[string]any{
				"written": []string{res.ProjectFile, res.LocalFile},
				"gitignore": map[string]string{
					"path":  res.Gitignore,
					"entry": "simsquad.local.toml",
				},
				"config": res.Summary,
			}
			var answer *bool
			if cmd.Flags().Changed("install-skills") {
				answer = &installSkills
			}
			installed, err := maybeInstallSkills(cmd.ErrOrStderr(), answer)
			if err != nil {
				return err
			}
			if installed != nil {
				out["skills"] = installed
			}
			return writeJSON(cmd, out)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing simsquad TOML files when creating config")
	cmd.Flags().BoolVar(&show, "show", false, "print merged simsquad config as JSON without opening the editor")
	cmd.Flags().BoolVar(&installSkills, "install-skills", false,
		"after saving, install the agent skills without asking (=false declines the offer)")
	cmd.Flags().StringArrayVar(&iosSpecs, "add-ios-spec", nil, "append an iOS slot spec: 'DEVICE:RUNTIME:COUNT'")
	cmd.Flags().StringArrayVar(&androidSpec, "add-android-spec", nil, "append an Android slot spec: 'DEVICE:IMAGE:COUNT[:TARGET]'")
	return cmd
}

// offerSkillInstall asks whether to install the agent skills after equip
// saved its config. It renders on w (stderr) so stdout stays the JSON
// contract. Tests replace it to answer without a terminal.
var offerSkillInstall = func(w io.Writer) (bool, error) {
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false, nil // no one to ask: leave the repo untouched
	}
	install := true
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Install the simsquad agent skills into this repo").
			Description("Same as `simsquad skill install`: skills, agent links, worker permissions.").
			Value(&install),
	)).WithInput(os.Stdin).WithOutput(w).Run()
	if err != nil {
		return false, fmt.Errorf("equip: skill install offer: %w", err)
	}
	return install, nil
}

// maybeInstallSkills runs the post-equip skill offer. answer is the
// --install-skills flag when given (no prompt); nil means ask, unless the
// skills this binary would write are already installed untouched. It
// returns the install result, or nil when nothing was installed.
func maybeInstallSkills(w io.Writer, answer *bool) (*skills.Result, error) {
	root := repoRoot()
	if answer == nil {
		if skillsCurrent(root) {
			return nil, nil
		}
		yes, err := offerSkillInstall(w)
		if err != nil {
			return nil, err
		}
		answer = &yes
	}
	if !*answer {
		return nil, nil
	}
	// Identical call to `skill install` with no flags.
	res, err := skills.Install(skills.Options{Root: root, Version: Version})
	if err != nil {
		return nil, fmt.Errorf("equip: skill install: %w", err)
	}
	return res, nil
}

// skillsCurrent reports whether every skill this binary ships is already
// installed at the default location, unedited and at this version, so
// re-running equip does not nag about a no-op install.
func skillsCurrent(root string) bool {
	in, err := skills.ReadInstalled(root, "")
	return err == nil && in != nil && len(in.Missing) == 0 && !in.Edited &&
		in.Version == Version && in.Contract == skills.Contract
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
