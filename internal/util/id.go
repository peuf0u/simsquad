package util

import (
	"strings"
)

// SimNamePrefix is the prefix every simulator/emulator name takes. Used by the
// orphan pruner to identify devices created by simsquad vs. ones the user
// created manually.
const SimNamePrefix = "simsquad"

// SanitiseName reduces an arbitrary string to [a-z0-9-]+ so it's safe to use
// inside filenames, sim names, and AVD names. Runs of non-alnum chars collapse
// to a single dash; leading/trailing dashes are stripped. An empty result
// falls back to "squad" to keep downstream paths valid.
func SanitiseName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	lastDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "squad"
	}
	return s
}

// SimName builds a unique-per-slot sim/AVD name from the squad name. Matches
// the orphan pruner's `^simsquad-[a-z0-9-]+-(ios|android)-\d+$` regex (the
// squad name is already sanitised to [a-z0-9-]+).
func SimName(name, platform string, index int) string {
	return SimNamePrefix + "-" + name + "-" + platform + "-" + itoa(index)
}

func itoa(i int) string {
	// Inlining strconv to keep this file dep-free; util.id has no other strconv users yet.
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
