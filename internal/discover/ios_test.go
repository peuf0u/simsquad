package discover

import (
	"os"
	"path/filepath"
	"testing"
)

func loadRuntimesFixture(t *testing.T) []byte {
	t.Helper()
	p := filepath.Join("testdata", "simctl_runtimes.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

func TestResolveRuntimeIdentifierFromGolden(t *testing.T) {
	raw := loadRuntimesFixture(t)

	cases := []struct {
		name, input, want string
	}{
		{
			name:  "name match returns full id",
			input: "iOS 26.4",
			want:  "com.apple.CoreSimulator.SimRuntime.iOS-26-4",
		},
		{
			name:  "version match returns full id",
			input: "26.4",
			want:  "com.apple.CoreSimulator.SimRuntime.iOS-26-4",
		},
		{
			name:  "full id passes through unchanged",
			input: "com.apple.CoreSimulator.SimRuntime.iOS-17-0",
			want:  "com.apple.CoreSimulator.SimRuntime.iOS-17-0",
		},
		{
			name:  "short id suffix match",
			input: "iOS-26-4",
			want:  "com.apple.CoreSimulator.SimRuntime.iOS-26-4",
		},
		{
			name:  "unavailable runtime is rejected (no match → input echoed)",
			input: "iOS 18.0",
			want:  "iOS 18.0",
		},
		{
			name:  "unknown runtime echoes input",
			input: "iOS 99.0",
			want:  "iOS 99.0",
		},
		{
			name:  "non-iOS platform ignored even if name matches",
			input: "watchOS 11.0",
			want:  "watchOS 11.0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveRuntimeIdentifierFrom(raw, tc.input)
			if got != tc.want {
				t.Errorf("ResolveRuntimeIdentifier(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestResolveRuntimeIdentifierShortCircuitsFullID(t *testing.T) {
	// Don't even consult the fixture — pre-resolved values must pass through
	// without invoking simctl. We assert this by passing a deliberately
	// malformed-looking but SimRuntime-containing string.
	input := "com.apple.CoreSimulator.SimRuntime.iOS-99-9"
	got := ResolveRuntimeIdentifier(input)
	if got != input {
		t.Errorf("expected pass-through for full id; got %q", got)
	}
}

func TestParseIosRuntimesSortAndFilter(t *testing.T) {
	raw := loadRuntimesFixture(t)
	runtimes := parseIosRuntimes(raw)
	if len(runtimes) != 3 {
		// 2 iOS available + 1 iOS unavailable; watchOS is filtered out.
		t.Fatalf("expected 3 iOS runtimes; got %d", len(runtimes))
	}
	// Newest first: 26.4, then 18.0 (unavailable), then 17.0.
	if runtimes[0].Version != "26.4" {
		t.Errorf("expected newest first to be 26.4; got %q", runtimes[0].Version)
	}
	if runtimes[len(runtimes)-1].Version != "17.0" {
		t.Errorf("expected oldest last to be 17.0; got %q", runtimes[len(runtimes)-1].Version)
	}
}
