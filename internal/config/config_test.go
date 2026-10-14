package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func loadTOML(t *testing.T, body string) *Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ProjectFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestContext(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		toml       string
		want       map[string]string
		wantLegacy bool
	}{
		{"none", "[project]\n", nil, false},
		{"context", "[context]\nPERSONA = \"alice\"\n", map[string]string{"PERSONA": "alice"}, false},
		{"legacy env", "[env]\nPERSONA = \"alice\"\n", map[string]string{"PERSONA": "alice"}, true},
		{"context wins over env", "[context]\nPERSONA = \"bob\"\n[env]\nPERSONA = \"alice\"\n", map[string]string{"PERSONA": "bob"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, legacy := loadTOML(t, c.toml).Context()
			if !reflect.DeepEqual(got, c.want) || legacy != c.wantLegacy {
				t.Fatalf("Context() = %v, %v; want %v, %v", got, legacy, c.want, c.wantLegacy)
			}
		})
	}
}
