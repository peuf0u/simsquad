package state

import (
	"errors"
	"io/fs"
	"os"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/util"
)

// loadIndex reads ~/.cache/simsquad/squads.json. A missing file yields an
// empty SquadIndex. Caller must hold the index lock.
func loadIndex() (contract.SquadIndex, error) {
	path := util.SquadIndexPath()
	var idx contract.SquadIndex
	if err := util.ReadJSON(path, &idx); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return contract.SquadIndex{Squads: map[string]contract.SquadIndexEntry{}}, nil
		}
		return contract.SquadIndex{}, err
	}
	if idx.Squads == nil {
		idx.Squads = map[string]contract.SquadIndexEntry{}
	}
	return idx, nil
}

// saveIndex writes the index atomically. Caller must hold the index lock.
func saveIndex(idx contract.SquadIndex) error {
	return util.WriteJSON(util.SquadIndexPath(), idx)
}

// ListSquads returns every squad currently registered. Result is a slice in
// arbitrary order; callers sort if they need a stable view.
func ListSquads() ([]contract.SquadIndexEntry, error) {
	return withIndexLock(func() ([]contract.SquadIndexEntry, error) {
		idx, err := loadIndex()
		if err != nil {
			return nil, err
		}
		out := make([]contract.SquadIndexEntry, 0, len(idx.Squads))
		for _, e := range idx.Squads {
			out = append(out, e)
		}
		return out, nil
	})
}

// FindSquad returns the registry entry for the named squad, or (zero, false)
// if no match exists. Since the squad name is the registry key, this is a
// direct map lookup — it resolves both user and ephemeral squads by name.
func FindSquad(name string) (contract.SquadIndexEntry, bool, error) {
	type result struct {
		entry contract.SquadIndexEntry
		ok    bool
	}
	r, err := withIndexLock(func() (result, error) {
		idx, err := loadIndex()
		if err != nil {
			return result{}, err
		}
		entry, ok := idx.Squads[name]
		return result{entry: entry, ok: ok}, nil
	})
	return r.entry, r.ok, err
}

// RegisterSquadInput captures the arguments to RegisterSquad.
type RegisterSquadInput struct {
	Name      string
	StateFile string
}

// RegisterSquad inserts a new entry into the registry. Acquires the index
// lock.
func RegisterSquad(in RegisterSquadInput) error {
	return WithIndexLockE(func() error { return RegisterSquadLocked(in) })
}

// RegisterSquadLocked is the unlocked variant of RegisterSquad. Caller MUST
// already hold the index lock (via WithIndexLockE). Used by transactions that
// chain registry queries and the squad registration as one atomic block.
func RegisterSquadLocked(in RegisterSquadInput) error {
	idx, err := loadIndex()
	if err != nil {
		return err
	}
	now := util.NowISO()
	idx.Squads[in.Name] = contract.SquadIndexEntry{
		Name:       in.Name,
		CreatedAt:  now,
		LastUsedAt: now,
		StateFile:  in.StateFile,
	}
	return saveIndex(idx)
}

// TouchSquad refreshes the LastUsedAt timestamp on an existing entry. A
// missing squad is silently ignored.
func TouchSquad(name string) error {
	_, err := withIndexLock(func() (struct{}, error) {
		idx, err := loadIndex()
		if err != nil {
			return struct{}{}, err
		}
		entry, ok := idx.Squads[name]
		if !ok {
			return struct{}{}, nil
		}
		entry.LastUsedAt = util.NowISO()
		idx.Squads[name] = entry
		return struct{}{}, saveIndex(idx)
	})
	return err
}

// RemoveSquad drops the entry from the registry. Per-squad state and any
// sidecar files are NOT removed here — the teardown package handles that.
func RemoveSquad(name string) error {
	_, err := withIndexLock(func() (struct{}, error) {
		idx, err := loadIndex()
		if err != nil {
			return struct{}{}, err
		}
		delete(idx.Squads, name)
		return struct{}{}, saveIndex(idx)
	})
	return err
}

// SnapshotSquadsLocked returns every persisted squad record. Caller MUST hold
// the index lock (via WithIndexLockE) so this set is consistent with any
// reservations made in the same transaction. A missing or malformed per-squad
// file is silently skipped — the alternative is failing a claim because one
// stale record on disk is corrupt.
func SnapshotSquadsLocked() ([]contract.SquadRecord, error) {
	idx, err := loadIndex()
	if err != nil {
		return nil, err
	}
	out := make([]contract.SquadRecord, 0, len(idx.Squads))
	for name := range idx.Squads {
		rec, err := LoadRecord(name)
		if err != nil || rec == nil {
			continue
		}
		out = append(out, *rec)
	}
	return out, nil
}

// LoadRecord reads ~/.cache/simsquad/<name>.json. Returns (nil, nil) when the
// file does not exist — distinguished from a parse error.
func LoadRecord(name string) (*contract.SquadRecord, error) {
	path := util.StateFileFor(name)
	var rec contract.SquadRecord
	if err := util.ReadJSON(path, &rec); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

// SaveRecord persists a squad record atomically.
func SaveRecord(rec *contract.SquadRecord) error {
	return util.WriteJSON(util.StateFileFor(rec.Name), rec)
}

// RemoveRecord deletes the per-squad state file. Missing file is not an error.
func RemoveRecord(name string) error {
	err := os.Remove(util.StateFileFor(name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
