package teardown

import "testing"

// TestMatchOrphan locks the orphan regex to the exact name shape produced by
// util.SimName. Regressions here are silent and dangerous: a loosened regex
// risks deleting the user's hand-created sims; a tightened one leaves real
// debris on disk after an interrupted deploy.
func TestMatchOrphan(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"canonical ios", "simsquad-abcd-ios-0", true},
		{"canonical android", "simsquad-pr-1234-android-3", true},
		{"hex tag", "simsquad-9f3a-ios-12", true},
		{"multi-segment tag", "simsquad-pr-1234-feature-x-ios-0", true},

		{"wrong prefix qa-pool", "qa-pool-abcd-ios-0", false},
		{"missing prefix", "abcd-ios-0", false},
		{"uppercase letters", "simsquad-ABCD-ios-0", false},
		{"missing platform", "simsquad-abcd-0", false},
		{"unknown platform", "simsquad-abcd-tvos-0", false},
		{"trailing non-digit", "simsquad-abcd-ios-final", false},
		{"underscore", "simsquad_abcd_ios_0", false},
		{"empty", "", false},
		{"trailing space", "simsquad-abcd-ios-0 ", false},

		// Hand-named user sims that must NOT be pruned. The user's "simsquad
		// scratch sim" doesn't follow the suffix grammar, so it stays.
		{"user-created lookalike", "simsquad-my-test-sim", false},
		{"build-target dest", "simsquad-build-target", false},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := MatchOrphan(c.in); got != c.want {
				t.Fatalf("MatchOrphan(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
