package client

import (
	"errors"
	"hsync/internal/protocol"
	"hsync/internal/utils"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
)

type note struct {
	hash  string
	local string
}

type scan struct {
	notes    map[string]note
	contents map[string]string
}

func runCycle(cfg *Config, client *http.Client, st *state) {
	remote, err := fetchIndex(cfg, client)
	if err != nil {
		log.Printf("Failed to fetch the server index: %v", err)
		return
	}

	local, err := scanNotes(cfg.DirPath)
	if err != nil {
		log.Printf("Error reading %s: %v", cfg.DirPath, err)
		return
	}

	index := local.index(st)
	target := protocol.PushResponse{Commit: remote.Commit, Files: remote.Files}

	pushing := !sameIndex(index, st.Files)
	switch {
	case pushing:
		if target, err = pushIndex(cfg, client, st, local, remote, index); err != nil {
			log.Printf("Failed to push local changes: %v", err)
			return
		}
	case remote.Commit == st.Commit && len(st.Missing) == 0:
		return
	}

	checkout(cfg, client, st, local, target, pushing)

	if err := st.save(); err != nil {
		log.Printf("Error saving sync state: %v", err)
	}
}

func pushIndex(cfg *Config, client *http.Client, st *state, local *scan, remote protocol.IndexResponse, index map[string]string) (protocol.PushResponse, error) {
	parent := st.Commit
	if parent == "" {
		parent = remote.Root
		log.Printf("Merging %d local notes into the server history", len(index))
	}

	resp, err := pushCommit(cfg, client, parent, index, local.blobs(index, st, parent == remote.Root))
	switch {
	case errors.Is(err, errUnknownParent):
		log.Printf("The server does not know commit %s, merging into its current history", short(parent))
		return pushCommit(cfg, client, remote.Root, index, local.blobs(index, st, true))
	case errors.Is(err, errMissingBlob):
		log.Printf("The server is missing note contents, uploading all of them")
		return pushCommit(cfg, client, parent, index, local.blobs(index, st, true))
	}
	return resp, err
}

func checkout(cfg *Config, client *http.Client, st *state, local *scan, target protocol.PushResponse, pushing bool) {
	missing := make(map[string]string)
	folded := make(map[string]string, len(target.Files))

	for _, name := range sortedNames(target.Files) {
		hash := target.Files[name]

		if cfg.caseInsensitive {
			fold := strings.ToLower(name)
			if other, ok := folded[fold]; ok {
				log.Printf("Skipping %s: it differs from %s only in letter case, which this device cannot store separately", name, other)
				missing[name] = hash
				continue
			}
			folded[fold] = name
		}

		if err := local.writeNote(cfg.DirPath, name, hash, func() (string, error) {
			return fetchBlob(cfg, client, hash)
		}); err != nil {
			log.Printf("Failed to update %s: %v", name, err)
			if _, ok := local.notes[name]; !ok {
				missing[name] = hash
			}
		}
	}

	previous := st.Files
	if pushing {
		previous = union(previous, local.index(st))
	}
	for _, name := range sortedNames(previous) {
		if _, ok := target.Files[name]; !ok {
			local.removeNote(cfg.DirPath, name)
		}
	}

	if st.Commit != target.Commit {
		log.Printf("Synchronized at commit %s with %d notes", short(target.Commit), len(target.Files))
	}

	st.Commit = target.Commit
	st.Files = target.Files
	st.Missing = missing
}

func scanNotes(dir string) (*scan, error) {
	names, err := utils.ListNotes(dir)
	if err != nil {
		return nil, err
	}

	s := &scan{
		notes:    make(map[string]note, len(names)),
		contents: make(map[string]string, len(names)),
	}

	for name, actual := range names {
		content, err := os.ReadFile(utils.LocalPath(dir, actual))
		if err != nil {
			log.Printf("Error reading %s: %v", name, err)
			continue
		}
		s.notes[name] = note{hash: utils.CalculateHash(string(content)), local: actual}
		s.contents[name] = string(content)
	}
	return s, nil
}

func (s *scan) index(st *state) map[string]string {
	index := make(map[string]string, len(s.notes))
	for name, n := range s.notes {
		index[name] = n.hash
	}
	for name, hash := range st.Missing {
		if _, ok := index[name]; !ok {
			index[name] = hash
		}
	}
	return index
}

func (s *scan) blobs(index map[string]string, st *state, all bool) map[string]string {
	blobs := make(map[string]string)
	for name, hash := range index {
		if content, ok := s.contents[name]; ok && (all || st.Files[name] != hash) {
			blobs[hash] = content
		}
	}
	return blobs
}

func (s *scan) path(dir, name string) string {
	if n, ok := s.notes[name]; ok {
		return utils.LocalPath(dir, n.local)
	}
	return utils.LocalPath(dir, name)
}

func (s *scan) writeNote(dir, name, hash string, fetch func() (string, error)) error {
	if s.notes[name].hash == hash {
		return nil
	}

	local := s.path(dir, name)
	current, err := os.ReadFile(local)
	switch {
	case err != nil && !os.IsNotExist(err):
		return err
	case err == nil && utils.CalculateHash(string(current)) != s.notes[name].hash:
		log.Printf("Keeping %s: it changed while syncing", name)
		return nil
	}

	content, err := fetch()
	if err != nil {
		return err
	}
	if err := utils.WriteFile(local, content); err != nil {
		return err
	}

	if _, ok := s.notes[name]; ok {
		log.Printf("Updated note from the server: %s", name)
	} else {
		log.Printf("Downloaded new note: %s", name)
	}
	return nil
}

func (s *scan) removeNote(dir, name string) {
	local := s.path(dir, name)

	current, err := os.ReadFile(local)
	switch {
	case os.IsNotExist(err):
		return
	case err != nil:
		log.Printf("Error reading %s: %v", name, err)
		return
	case utils.CalculateHash(string(current)) != s.notes[name].hash:
		log.Printf("Keeping %s: it changed while syncing", name)
		return
	}

	if err := utils.RemoveFile(dir, local); err != nil {
		log.Printf("Error removing %s: %v", name, err)
		return
	}
	log.Printf("Removed note deleted on the server: %s", name)
}

func sameIndex(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for name, hash := range a {
		if b[name] != hash {
			return false
		}
	}
	return true
}

func union(indexes ...map[string]string) map[string]string {
	all := make(map[string]string)
	for _, index := range indexes {
		for name, hash := range index {
			all[name] = hash
		}
	}
	return all
}

func sortedNames(index map[string]string) []string {
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func short(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
