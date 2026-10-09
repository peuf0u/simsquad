package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// runRoot executes the real root command with args and returns stdout and
// the exit code (0, an ExitError code, or -1 for any other error).
func runRoot(t *testing.T, args ...string) (string, int) {
	t.Helper()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return out.String(), 0
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return out.String(), ee.Code
	}
	return out.String(), -1
}

func TestOutWritesStdoutJSONToFile(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "nested", "dir", "status.json")

	stdout, code := runRoot(t, "status", "--out", path)
	if code != 0 {
		t.Fatalf("status --out: exit %d", code)
	}
	if stdout != "{\n  \"squads\": []\n}\n" {
		t.Fatalf("stdout changed: %q", stdout)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read --out file: %v", err)
	}
	if string(got) != stdout {
		t.Fatalf("--out file = %q, want stdout %q", got, stdout)
	}
}

// TestOutReachesEveryCommand guards the two ways a verb could silently lose
// --out: not inheriting the flag, or declaring its own persistent pre-run,
// which cobra runs instead of the root's stale-file cleanup.
func TestOutReachesEveryCommand(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Flags().Lookup("out") == nil && sub.InheritedFlags().Lookup("out") == nil {
				t.Errorf("%s: no --out flag", sub.CommandPath())
			}
			if sub.PersistentPreRun != nil || sub.PersistentPreRunE != nil {
				t.Errorf("%s: own persistent pre-run shadows the root --out hook", sub.CommandPath())
			}
			walk(sub)
		}
	}
	walk(NewRootCmd())
}

func TestOutLeavesNoFileWhenCommandFailsWithoutJSON(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "status.json")
	if err := os.WriteFile(path, []byte(`{"stale": true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, code := runRoot(t, "status", "--name", "missing", "--out", path)
	if code == 0 || stdout != "" {
		t.Fatalf("status of missing squad: exit %d stdout %q, want failure with no JSON", code, stdout)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("--out file still exists after failed command (stat err %v)", err)
	}
}

func TestOutWritesJSONOnNonZeroExitOfNestedCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validate.json")

	stdout, code := runRoot(t, "run", "validate", t.TempDir(), "--out", path)
	if code != 1 || stdout == "" {
		t.Fatalf("run validate on empty dir: exit %d stdout %q, want exit 1 with JSON", code, stdout)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read --out file: %v", err)
	}
	if string(got) != stdout {
		t.Fatalf("--out file = %q, want stdout %q", got, stdout)
	}
}
