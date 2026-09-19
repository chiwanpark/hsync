package utils

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

var ErrInvalidPath = errors.New("invalid sync path")

func CanonicalName(name string) string {
	return norm.NFC.String(strings.ReplaceAll(name, `\`, "/"))
}

func CheckName(name string) (string, error) {
	rel := CanonicalName(name)
	if rel == "" || !strings.HasSuffix(rel, ".txt") || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", ErrInvalidPath
	}

	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrInvalidPath
		}
	}
	return rel, nil
}

func LocalPath(root, name string) string {
	return filepath.Join(root, filepath.FromSlash(name))
}

func ListNotes(root string) (map[string]string, error) {
	notes := make(map[string]string)

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil && path == root:
			return err
		case err != nil:
			return nil
		case entry.IsDir():
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		case !strings.HasSuffix(entry.Name(), ".txt"):
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		name := filepath.ToSlash(rel)
		canonical := CanonicalName(name)
		if actual, ok := notes[canonical]; !ok || actual != canonical {
			notes[canonical] = name
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return notes, nil
}

func WriteFile(localPath, content string) error {
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".hsync-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()

	_, err = tmp.WriteString(content)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(name, 0644)
	}
	if err == nil {
		err = os.Rename(name, localPath)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

func RemoveFile(root, localPath string) error {
	if err := os.Remove(localPath); err != nil {
		return err
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	dir, err := filepath.Abs(filepath.Dir(localPath))
	if err != nil {
		return nil
	}

	for dir != absRoot && strings.HasPrefix(dir, absRoot+string(filepath.Separator)) {
		if entries, err := os.ReadDir(dir); err != nil || len(entries) > 0 {
			return nil
		}
		if err := os.Remove(dir); err != nil {
			return nil
		}
		dir = filepath.Dir(dir)
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
