package client

import (
	"encoding/json"
	"errors"
	"hsync/internal/utils"
	"os"
	"path/filepath"
)

type syncState struct {
	path       string
	Base       map[string]string `json:"base"`
	Tombstones map[string]int64  `json:"tombstones"`
}

func defaultStatePath(dirPath string) string {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(dirPath, ".hsync-state.json")
		}
		stateHome = filepath.Join(home, ".local", "state")
	}

	abs, err := filepath.Abs(dirPath)
	if err != nil {
		abs = dirPath
	}
	return filepath.Join(stateHome, "hsync", utils.CalculateHash(abs)[:16]+".json")
}

func loadState(path string) (*syncState, bool, error) {
	st := &syncState{
		path:       path,
		Base:       make(map[string]string),
		Tombstones: make(map[string]int64),
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, false, nil
	}
	if err != nil {
		return st, false, err
	}

	if err := json.Unmarshal(data, st); err != nil {
		return st, false, err
	}
	if st.Base == nil {
		st.Base = make(map[string]string)
	}
	if st.Tombstones == nil {
		st.Tombstones = make(map[string]int64)
	}
	return st, true, nil
}

func (s *syncState) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}

	data, err := json.Marshal(s)
	if err != nil {
		return err
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *syncState) pruneTombstones(known map[string]int64) {
	for name := range s.Tombstones {
		if _, ok := known[name]; !ok {
			delete(s.Tombstones, name)
		}
	}
}
