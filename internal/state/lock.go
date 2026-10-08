// Package state holds the per-user squad registry and per-squad records under
// ~/.cache/simsquad/. Every read or write that touches squads.json passes
// through an exclusive flock on .index.lock so concurrent simsquad invocations
// don't race each other.
package state

import (
	"fmt"

	"github.com/gofrs/flock"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/util"
)

// withIndexLock runs fn while holding an exclusive lock on the index file.
// The lock is released regardless of fn's outcome; fn's error is returned
// verbatim.
func withIndexLock[T any](fn func() (T, error)) (T, error) {
	var zero T
	if err := util.EnsureCacheRoot(); err != nil {
		return zero, fmt.Errorf("state.withIndexLock: ensure cache: %w", err)
	}
	lk := flock.New(util.IndexLockPath())
	if err := lk.Lock(); err != nil {
		return zero, fmt.Errorf("state.withIndexLock: acquire: %w", err)
	}
	defer func() { _ = lk.Unlock() }()
	return fn()
}

// WithIndexLockE runs fn while holding the exclusive index lock. The closure
// MUST use the *Locked variants of registry mutators (RegisterSquadLocked,
// SaveIndexLocked) — calling the public, self-locking helpers (RegisterSquad,
// ListSquads, TouchSquad, FindSquad, RemoveSquad) from inside fn will
// deadlock the goroutine on flock re-acquire.
//
// Used by the Android physical-device claim block so list → claim → save
// happens as one atomic transaction across concurrent simsquad invocations
// (Landmine #7 in the plan), and by the lease claim path.
func WithIndexLockE(fn func() error) error {
	_, err := withIndexLock(func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}

// LoadIndexLocked exposes the unlocked loader for callers already inside
// WithIndexLockE. See WithIndexLockE for the contract.
func LoadIndexLocked() (contract.SquadIndex, error) { return loadIndex() }

// SaveIndexLocked exposes the unlocked saver for callers already inside
// WithIndexLockE.
func SaveIndexLocked(idx contract.SquadIndex) error { return saveIndex(idx) }
