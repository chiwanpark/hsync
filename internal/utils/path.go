package utils

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrInvalidPath is returned when a sync path is not a safe relative path.
var ErrInvalidPath = errors.New("invalid sync path")

// NormalizeSyncPath validates name and returns it as a slash-separated relative path.
func NormalizeSyncPath(name string) (string, error) {
	rel := strings.ReplaceAll(name, `\`, "/")
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
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(localPath, []byte(content), 0644)
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
