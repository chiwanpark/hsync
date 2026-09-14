package utils

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// ErrInvalidPath is returned when a sync path is not a safe relative path.
var ErrInvalidPath = errors.New("invalid sync path")

func CanonicalSyncName(name string) string {
	return norm.NFC.String(strings.ReplaceAll(name, `\`, "/"))
}

// NormalizeSyncPath validates name and returns it as a slash-separated relative path.
func NormalizeSyncPath(name string) (string, error) {
	rel := CanonicalSyncName(name)
	if rel == "" || !strings.HasSuffix(rel, ".txt") {
		return "", ErrInvalidPath
	}
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", ErrInvalidPath
	}

	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrInvalidPath
		}
	}
	return rel, nil
}

// LocalPath converts a slash-separated relative path into a local path under root.
func LocalPath(root, name string) string {
	return filepath.Join(root, filepath.FromSlash(name))
}

// ResolveSyncPath validates name and returns the matching local path under root.
func ResolveSyncPath(root, name string) (string, error) {
	rel, err := NormalizeSyncPath(name)
	if err != nil {
		return "", err
	}
	return LocalPath(root, rel), nil
}

// WriteSyncFile writes content, creating parent directories when needed.
func WriteSyncFile(localPath, content string) error {
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".hsync-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, localPath); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func IsCaseInsensitiveDir(dir string) bool {
	probe, err := os.CreateTemp(dir, "hsync-case-*.tmp")
	if err != nil {
		return false
	}
	name := probe.Name()
	probe.Close()
	defer os.Remove(name)

	upper := filepath.Join(filepath.Dir(name), strings.ToUpper(filepath.Base(name)))
	if upper == name {
		return false
	}

	_, err = os.Stat(upper)
	return err == nil
}

func PruneEmptyDirs(root, localPath string) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return
	}

	dir, err := filepath.Abs(filepath.Dir(localPath))
	if err != nil {
		return
	}

	for dir != absRoot && strings.HasPrefix(dir, absRoot+string(filepath.Separator)) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// ListTextFiles walks root recursively and returns slash-separated relative paths of .txt files.
func ListTextFiles(root string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".txt") {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

func ListSyncFiles(root string) (map[string]string, error) {
	names, err := ListTextFiles(root)
	if err != nil {
		return nil, err
	}

	files := make(map[string]string, len(names))
	for _, name := range names {
		canonical := CanonicalSyncName(name)
		if existing, ok := files[canonical]; ok && existing == canonical {
			continue
		}
		files[canonical] = name
	}
	return files, nil
}
