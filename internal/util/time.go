package util

import "time"

// UTCNow returns the current time in UTC. Wrapped so tests can swap it.
var UTCNow = func() time.Time { return time.Now().UTC() }

// ISO formats a time in the second-precision UTC ISO-8601 shape
// used across the JSON contract (e.g. "2026-05-21T14:30:00Z"). Microseconds
// are dropped to match the Python reference.
func ISO(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
}

// NowISO is a shorthand for ISO(UTCNow()).
func NowISO() string { return ISO(UTCNow()) }
