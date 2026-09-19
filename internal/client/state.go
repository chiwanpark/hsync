package client

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type state struct {
	path    string
	Commit  string            `json:"commit"`
	Files   map[string]string `json:"files"`
	Missing map[string]string `json:"missing,omitempty"`
}

func loadState(path string) (*state, error) {
	st := &state{path: path, Files: map[string]string{}, Missing: map[string]string{}}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, err
	}

	if st.Files == nil {
		st.Files = map[string]string{}
	}
	if st.Missing == nil {
		st.Missing = map[string]string{}
	}
	return st, nil
}

func (s *state) save() error {
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
