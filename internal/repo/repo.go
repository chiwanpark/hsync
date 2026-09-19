package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"hsync/internal/utils"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const metaDir = ".hsync"

var (
	ErrUnknownParent = errors.New("unknown parent commit")
	ErrMissingBlob   = errors.New("missing blob")
	ErrCorruptBlob   = errors.New("blob does not match its hash")
)

type Commit struct {
	ID        string            `json:"id"`
	Parent    string            `json:"parent"`
	Merged    string            `json:"merged,omitempty"`
	CreatedAt int64             `json:"createdAt"`
	Files     map[string]string `json:"files"`
}

type Repo struct {
	dir  string
	root string
	head Commit
}

func Open(dir string) (*Repo, error) {
	r := &Repo{dir: dir}

	for _, sub := range []string{"objects", "commits"} {
		if err := os.MkdirAll(filepath.Join(dir, metaDir, sub), 0755); err != nil {
			return nil, err
		}
	}

	root, err := r.writeCommit(Commit{Files: map[string]string{}})
	if err != nil {
		return nil, err
	}
	r.root = root.ID
	r.head = root

	id, err := os.ReadFile(filepath.Join(dir, metaDir, "HEAD"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if head, ok, err := r.Commit(strings.TrimSpace(string(id))); err != nil {
		return nil, err
	} else if ok {
		r.head = head
	}

	return r, r.Refresh()
}

func (r *Repo) Head() Commit {
	return r.head
}

func (r *Repo) Root() string {
	return r.root
}

func (r *Repo) Commit(id string) (Commit, bool, error) {
	data, err := os.ReadFile(r.commitPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return Commit{}, false, nil
	}
	if err != nil {
		return Commit{}, false, err
	}

	var c Commit
	if err := json.Unmarshal(data, &c); err != nil {
		return Commit{}, false, err
	}
	return c, true, nil
}

func (r *Repo) Blob(id string) (string, bool, error) {
	content, err := os.ReadFile(r.blobPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(content), true, nil
}

func (r *Repo) PutBlob(content string) (string, error) {
	id := utils.CalculateHash(content)
	if _, err := os.Stat(r.blobPath(id)); err == nil {
		return id, nil
	}
	return id, utils.WriteFile(r.blobPath(id), content)
}

func (r *Repo) Refresh() error {
	notes, err := utils.ListNotes(r.dir)
	if err != nil {
		return err
	}

	index := make(map[string]string, len(notes))
	for name, actual := range notes {
		local := utils.LocalPath(r.dir, actual)
		if actual != name {
			canonical := utils.LocalPath(r.dir, name)
			if _, err := os.Stat(canonical); err != nil {
				if err := os.Rename(local, canonical); err != nil {
					return err
				}
				log.Printf("Normalized note name %s -> %s", actual, name)
				local = canonical
			}
		}

		content, err := os.ReadFile(local)
		if err != nil {
			return err
		}
		id, err := r.PutBlob(string(content))
		if err != nil {
			return err
		}
		index[name] = id
	}

	if sameIndex(index, r.head.Files) {
		return nil
	}

	_, err = r.commit(index, "")
	return err
}

func (r *Repo) Push(parent string, files, blobs map[string]string) (Commit, error) {
	index := make(map[string]string, len(files))
	for name, id := range files {
		canonical, err := utils.CheckName(name)
		if err != nil {
			return Commit{}, fmt.Errorf("%w: %s", utils.ErrInvalidPath, name)
		}
		index[canonical] = id
	}

	base, ok, err := r.Commit(parent)
	if err != nil {
		return Commit{}, err
	}
	if !ok {
		return Commit{}, fmt.Errorf("%w: %s", ErrUnknownParent, parent)
	}

	for id, content := range blobs {
		if utils.CalculateHash(content) != id {
			return Commit{}, fmt.Errorf("%w: %s", ErrCorruptBlob, id)
		}
		if _, err := r.PutBlob(content); err != nil {
			return Commit{}, err
		}
	}

	for name, id := range index {
		if _, ok, err := r.Blob(id); err != nil {
			return Commit{}, err
		} else if !ok {
			return Commit{}, fmt.Errorf("%w: %s for %s", ErrMissingBlob, id, name)
		}
	}

	merged, err := mergeIndex(base.Files, r.head.Files, index, r)
	if err != nil {
		return Commit{}, err
	}
	if sameIndex(merged, r.head.Files) {
		return r.head, nil
	}
	return r.commit(merged, parent)
}

func (r *Repo) commit(index map[string]string, merged string) (Commit, error) {
	c, err := r.writeCommit(Commit{
		Parent:    r.head.ID,
		Merged:    merged,
		CreatedAt: time.Now().Unix(),
		Files:     index,
	})
	if err != nil {
		return Commit{}, err
	}

	if err := r.checkout(c); err != nil {
		return Commit{}, err
	}
	if err := utils.WriteFile(filepath.Join(r.dir, metaDir, "HEAD"), c.ID+"\n"); err != nil {
		return Commit{}, err
	}

	r.head = c
	return c, nil
}

func (r *Repo) checkout(target Commit) error {
	for name, id := range target.Files {
		local := utils.LocalPath(r.dir, name)
		if content, err := os.ReadFile(local); err == nil && utils.CalculateHash(string(content)) == id {
			continue
		}

		content, err := r.load(id)
		if err != nil {
			return err
		}
		if err := utils.WriteFile(local, content); err != nil {
			return err
		}
	}

	for name := range r.head.Files {
		if _, ok := target.Files[name]; ok {
			continue
		}
		if err := utils.RemoveFile(r.dir, utils.LocalPath(r.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (r *Repo) load(id string) (string, error) {
	if id == "" {
		return "", nil
	}

	content, ok, err := r.Blob(id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrMissingBlob, id)
	}
	return content, nil
}

func (r *Repo) store(content string) (string, error) {
	return r.PutBlob(content)
}

func (r *Repo) writeCommit(c Commit) (Commit, error) {
	if c.Files == nil {
		c.Files = map[string]string{}
	}
	c.ID = commitID(c)

	if _, err := os.Stat(r.commitPath(c.ID)); err == nil {
		return c, nil
	}

	data, err := json.Marshal(c)
	if err != nil {
		return Commit{}, err
	}
	return c, utils.WriteFile(r.commitPath(c.ID), string(data))
}

func (r *Repo) commitPath(id string) string {
	if id == "" {
		id = "none"
	}
	return filepath.Join(r.dir, metaDir, "commits", id+".json")
}

func (r *Repo) blobPath(id string) string {
	if len(id) < 4 {
		id = "none-none"
	}
	return filepath.Join(r.dir, metaDir, "objects", id[:2], id[2:])
}

func commitID(c Commit) string {
	var sb strings.Builder
	sb.WriteString("parent " + c.Parent + "\n")
	sb.WriteString("merged " + c.Merged + "\n")
	for _, name := range sortedNames(c.Files) {
		sb.WriteString(c.Files[name] + " " + name + "\n")
	}
	return utils.CalculateHash(sb.String())
}

func sameIndex(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for name, id := range a {
		if b[name] != id {
			return false
		}
	}
	return true
}

func sortedNames(index map[string]string) []string {
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
