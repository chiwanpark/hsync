package server

import (
	"encoding/json"
	"errors"
	"hsync/internal/protocol"
	"os"
	"path/filepath"
	"time"
)

const (
	tombstoneFile      = ".hsync-tombstones.json"
	tombstoneRetention = 90 * 24 * time.Hour
	maxRenameDepth     = 16
)

type tombstoneStore struct {
	path    string
	entries map[string]protocol.Tombstone
}

func newTombstoneStore(dataDir string) *tombstoneStore {
	return &tombstoneStore{
		path:    filepath.Join(dataDir, tombstoneFile),
		entries: make(map[string]protocol.Tombstone),
	}
}

func (t *tombstoneStore) load() error {
	data, err := os.ReadFile(t.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	entries := make(map[string]protocol.Tombstone)
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	t.entries = entries
	t.prune()
	return nil
}

func (t *tombstoneStore) save() error {
	t.prune()

	data, err := json.MarshalIndent(t.entries, "", "  ")
	if err != nil {
		return err
	}

	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, t.path)
}

func (t *tombstoneStore) prune() {
	cutoff := time.Now().Add(-tombstoneRetention).UnixNano()
	for name, entry := range t.entries {
		if entry.DeletedAt < cutoff {
			delete(t.entries, name)
		}
	}
}

func (t *tombstoneStore) add(name, renamedTo string) {
	t.entries[name] = protocol.Tombstone{
		DeletedAt: time.Now().UnixNano(),
		RenamedTo: renamedTo,
	}
}

func (t *tombstoneStore) clear(name string) {
	delete(t.entries, name)
}

func (t *tombstoneStore) has(name string) bool {
	_, ok := t.entries[name]
	return ok
}

func (t *tombstoneStore) resolve(name string) string {
	current := name
	seen := map[string]bool{name: true}

	for i := 0; i < maxRenameDepth; i++ {
		entry, ok := t.entries[current]
		if !ok || entry.RenamedTo == "" || seen[entry.RenamedTo] {
			return current
		}
		current = entry.RenamedTo
		seen[current] = true
	}
	return current
}

func (t *tombstoneStore) all() map[string]protocol.Tombstone {
	out := make(map[string]protocol.Tombstone, len(t.entries))
	for name, entry := range t.entries {
		out[name] = entry
	}
	return out
}
