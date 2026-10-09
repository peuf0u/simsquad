package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/skills"
	"github.com/peuf0u/simsquad/internal/util"
)

// mobilecliMinimum is the oldest mobilecli release simsquad was tested
// with. `go install` users don't get it through brew, so skill status tells
// them exactly which version to install.
const mobilecliMinimum = "1.0.13"

func newSkillStatusCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check the installed skills and mobilecli fit this binary",
		Long: "Compares the skill contract of the skills installed in this app repo with\n" +
			"this binary's, and checks the mobilecli on PATH against the minimum version\n" +
			"simsquad was tested with (" + mobilecliMinimum + ").\n" +
			"\n" +
			"stdout: {installed, skill_contract, binary_contract, in_sync, warning?,\n" +
			"         mobilecli: {found, version, minimum, meets_minimum}}.\n" +
			"\n" +
			"Exit codes: 0 = skills in sync, older, hand-edited or missing files\n" +
			"(warning: re-run `simsquad skill install`) or not installed; 1 = skills\n" +
			"newer than this binary, so a skill\n" +
			"would call commands it doesn't have. Redirect stdout only (> out.json).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dir != "" {
				abs, err := filepath.Abs(dir)
				if err != nil {
					return fmt.Errorf("skill status: --dir: %w", err)
				}
				dir = abs
			}
			in, err := skills.ReadInstalled(repoRoot(), dir)
			if err != nil {
				return fmt.Errorf("skill status: %w", err)
			}
			st := compareSkills(in)
			st.Mobilecli = probeMobilecli()

			if err := writeJSON(cmd, st); err != nil {
				return err
			}
			if st.SkillContract > st.BinaryContract {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "read the skills from this directory instead of .agents/skills")
	return cmd
}

// compareSkills compares the installed skills (nil when none) with this
// binary's skill contract.
func compareSkills(in *skills.Installed) contract.SkillStatus {
	st := contract.SkillStatus{BinaryContract: skills.Contract}
	if in == nil {
		st.Warning = "no skills installed; run `simsquad skill install`"
		return st
	}
	st.Installed = true
	st.SkillContract = in.Contract
	switch {
	case in.Contract > skills.Contract:
		st.Warning = fmt.Sprintf("skills use contract %d but this simsquad supports %d; upgrade simsquad", in.Contract, skills.Contract)
	case in.Contract < skills.Contract:
		st.Warning = fmt.Sprintf("skills use contract %d, older than this simsquad's %d; re-run `simsquad skill install`", in.Contract, skills.Contract)
	case len(in.Missing) > 0:
		st.Warning = fmt.Sprintf("skill files missing: %v; re-run `simsquad skill install`", in.Missing)
	case in.Edited:
		st.Warning = "skill files were edited by hand; put project knowledge in qa/README.md and re-run `simsquad skill install --force`"
	default:
		st.InSync = true
	}
	return st
}

var semverRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// probeMobilecli looks mobilecli up on PATH and compares the version it
// reports (`mobilecli version 1.0.13`) with mobilecliMinimum.
func probeMobilecli() contract.MobilecliStatus {
	st := contract.MobilecliStatus{Minimum: mobilecliMinimum}
	path, err := exec.LookPath("mobilecli")
	if err != nil {
		return st
	}
	st.Found = true
	r, err := util.Run(path, []string{"--version"}, util.RunOpts{Timeout: 10 * time.Second})
	if err != nil {
		return st
	}
	v := semverRe.FindString(r.Stdout)
	if v == "" {
		return st
	}
	st.Version = v
	st.MeetsMinimum = compareVersions(v, mobilecliMinimum) >= 0
	return st
}

// compareVersions compares two dotted x.y.z versions numerically.
func compareVersions(a, b string) int {
	pa, pb := semverRe.FindStringSubmatch(a), semverRe.FindStringSubmatch(b)
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
