package state

import (
	"errors"
	"io/fs"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/util"
)

// AndroidPortRange holds the canonical Android Debug Bridge port slots —
// even ports from 5554 through 5582 inclusive (15 slots). Emulators bind a
// pair (data + console); only the data port is tracked here.
var AndroidPortRange = func() []int {
	out := make([]int, 0, 15)
	for p := 5554; p <= 5582; p += 2 {
		out = append(out, p)
	}
	return out
}()

// UsedAndroidPorts returns the set of ports currently claimed across every
// persisted squad. Acquires the index lock for the registry read; callers
// composing list → claim → save under a single transaction should use
// UsedAndroidPortsLocked from inside WithIndexLockE instead.
func UsedAndroidPorts() (map[int]struct{}, error) {
	idx, err := withIndexLock(func() (contract.SquadIndex, error) {
		return loadIndex()
	})
	if err != nil {
		return nil, err
	}
	return collectUsedPorts(idx), nil
}

// UsedAndroidPortsLocked is the unlocked variant for callers already inside
// WithIndexLockE. Same semantics as UsedAndroidPorts; included so a single
// transaction can scan and write without re-acquiring the lock.
func UsedAndroidPortsLocked() (map[int]struct{}, error) {
	idx, err := loadIndex()
	if err != nil {
		return nil, err
	}
	return collectUsedPorts(idx), nil
}

func collectUsedPorts(idx contract.SquadIndex) map[int]struct{} {
	used := map[int]struct{}{}
	for _, entry := range idx.Squads {
		var rec contract.SquadRecord
		if err := util.ReadJSON(entry.StateFile, &rec); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			// Tolerate a single bad record; the alternative is failing every
			// allocation just because one stale state file is corrupt.
			continue
		}
		for _, port := range rec.Ports {
			used[port] = struct{}{}
		}
	}
	return used
}

// AllocateAndroidPort returns the lowest port in AndroidPortRange not held by
// any persisted squad or in reservedLocal. Returns 0 (with a non-nil error)
// when the range is exhausted. Acquires the index lock; for transaction use
// see AllocateAndroidPortLocked.
func AllocateAndroidPort(reservedLocal map[int]struct{}) (int, error) {
	used, err := UsedAndroidPorts()
	if err != nil {
		return 0, err
	}
	return pickPort(used, reservedLocal)
}

// AllocateAndroidPortLocked is the unlocked variant of AllocateAndroidPort.
// Caller MUST hold the index lock.
func AllocateAndroidPortLocked(reservedLocal map[int]struct{}) (int, error) {
	used, err := UsedAndroidPortsLocked()
	if err != nil {
		return 0, err
	}
	return pickPort(used, reservedLocal)
}

func pickPort(used, reservedLocal map[int]struct{}) (int, error) {
	for _, port := range AndroidPortRange {
		if _, taken := used[port]; taken {
			continue
		}
		if _, taken := reservedLocal[port]; taken {
			continue
		}
		return port, nil
	}
	return 0, errors.New("no free Android port in 5554..5582")
}
