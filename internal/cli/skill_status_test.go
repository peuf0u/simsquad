package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/skills"
)

type skillStatusOut struct {
	Installed      bool   `json:"installed"`
	SkillContract  int    `json:"skill_contract"`
	BinaryContract int    `json:"binary_contract"`
	InSync         bool   `json:"in_sync"`
	Warning        string `json:"warning"`
	Mobilecli      struct {
		Found        bool   `json:"found"`
		Version      string `json:"version"`
		Minimum      string `json:"minimum"`
		MeetsMinimum bool   `json:"meets_minimum"`
	} `json:"mobilecli"`
}

// withMobilecli puts a fake mobilecli on PATH that prints `mobilecli version
// <version>` for --version. An empty version leaves mobilecli off PATH.
func withMobilecli(t *testing.T, version string) {
	t.Helper()
	bin := t.TempDir()
	if version != "" {
		script := "#!/bin/sh\necho 'mobilecli version " + version + "'\n"
		if err := os.WriteFile(filepath.Join(bin, "mobilecli"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
}

// setInstalledContract rewrites the contract number in each installed
// skill's header, as if a different simsquad had installed them.
func setInstalledContract(t *testing.T, repo string, n int) {
	t.Helper()
	for _, name := range skills.Names {
		p := filepath.Join(repo, skills.DefaultDir, name, "SKILL.md")
		b := readFile(t, p)
		from := "skill contract " + strconv.Itoa(skills.Contract) + "."
		if !strings.Contains(b, from) {
			t.Fatalf("%s: no %q in header", p, from)
		}
		b = strings.Replace(b, from, "skill contract "+strconv.Itoa(n)+".", 1)
		if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func skillStatus(t *testing.T, args ...string) (skillStatusOut, error) {
	t.Helper()
	raw, err := runSkill(t, append([]string{"status"}, args...)...)
	var got skillStatusOut
	if uerr := json.Unmarshal([]byte(raw), &got); uerr != nil {
		t.Fatalf("unmarshal: %v\nout=%s", uerr, raw)
	}
	return got, err
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}

func TestSkillStatusNoSkillsInstalled(t *testing.T) {
	newAppRepo(t)
	withMobilecli(t, "1.0.13")

	got, err := skillStatus(t)
	if exitCode(err) != 0 {
		t.Fatalf("err = %v, want exit 0", err)
	}
	if got.Installed || got.InSync {
		t.Errorf("installed=%v in_sync=%v, want both false", got.Installed, got.InSync)
	}
	if got.BinaryContract != skills.Contract {
		t.Errorf("binary_contract = %d, want %d", got.BinaryContract, skills.Contract)
	}
}

func TestSkillStatusContractComparison(t *testing.T) {
	cases := []struct {
		name       string
		contract   int
		wantSync   bool
		wantWarn   bool
		wantExitCd int
	}{
		{"equal", skills.Contract, true, false, 0},
		{"older", skills.Contract - 1, false, true, 0},
		{"newer", skills.Contract + 1, false, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newAppRepo(t)
			withMobilecli(t, "1.0.13")
			mustInstall(t)
			if tc.contract != skills.Contract {
				setInstalledContract(t, repo, tc.contract)
			}

			got, err := skillStatus(t)
			if c := exitCode(err); c != tc.wantExitCd {
				t.Fatalf("exit = %d (err %v), want %d", c, err, tc.wantExitCd)
			}
			if !got.Installed {
				t.Error("installed = false, want true")
			}
			if got.SkillContract != tc.contract || got.BinaryContract != skills.Contract {
				t.Errorf("skill_contract=%d binary_contract=%d, want %d/%d",
					got.SkillContract, got.BinaryContract, tc.contract, skills.Contract)
			}
			if got.InSync != tc.wantSync {
				t.Errorf("in_sync = %v, want %v", got.InSync, tc.wantSync)
			}
			if (got.Warning != "") != tc.wantWarn {
				t.Errorf("warning = %q, want present=%v", got.Warning, tc.wantWarn)
			}
		})
	}
	t.Run("older warning says to re-run skill install", func(t *testing.T) {
		repo := newAppRepo(t)
		withMobilecli(t, "1.0.13")
		mustInstall(t)
		setInstalledContract(t, repo, skills.Contract-1)
		got, _ := skillStatus(t)
		if !strings.Contains(got.Warning, "simsquad skill install") {
			t.Errorf("warning = %q, want it to mention `simsquad skill install`", got.Warning)
		}
	})
}

func TestSkillStatusMobilecli(t *testing.T) {
	cases := []struct {
		name      string
		version   string
		wantFound bool
		wantMeets bool
	}{
		{"missing", "", false, false},
		{"older", "1.0.12", true, false},
		{"older minor beats lexical order", "0.10.99", true, false},
		{"matching", "1.0.13", true, true},
		{"newer", "1.2.0", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newAppRepo(t)
			withMobilecli(t, tc.version)
			mustInstall(t)

			got, err := skillStatus(t)
			if exitCode(err) != 0 {
				t.Fatalf("err = %v, want exit 0", err)
			}
			m := got.Mobilecli
			if m.Found != tc.wantFound || m.MeetsMinimum != tc.wantMeets {
				t.Errorf("found=%v meets_minimum=%v, want %v/%v", m.Found, m.MeetsMinimum, tc.wantFound, tc.wantMeets)
			}
			if m.Version != tc.version {
				t.Errorf("version = %q, want %q", m.Version, tc.version)
			}
			if m.Minimum != "1.0.13" {
				t.Errorf("minimum = %q, want 1.0.13", m.Minimum)
			}
		})
	}
}
