package utils

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalizeSyncPathValid(t *testing.T) {
	cases := map[string]string{
		"note.txt":                    "note.txt",
		"projects/alpha/nested.txt":   "projects/alpha/nested.txt",
		`projects\alpha\nested.txt`:   "projects/alpha/nested.txt",
		"a/b/c/d/e/deeply nested.txt": "a/b/c/d/e/deeply nested.txt",
	}

	for input, want := range cases {
		got, err := NormalizeSyncPath(input)
		if err != nil {
			t.Fatalf("NormalizeSyncPath(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Errorf("NormalizeSyncPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeSyncPathInvalid(t *testing.T) {
	inputs := []string{
		"",
		"note.md",
		"note.txt/",
		"/abs/note.txt",
		"../escape.txt",
		"projects/../../escape.txt",
		"projects/./note.txt",
		"projects//note.txt",
		`..\escape.txt`,
	}

	for _, input := range inputs {
		if got, err := NormalizeSyncPath(input); err == nil {
			t.Errorf("NormalizeSyncPath(%q) = %q, want error", input, got)
		}
	}
}

func TestResolveSyncPath(t *testing.T) {
	root := t.TempDir()

	got, err := ResolveSyncPath(root, "projects/alpha/nested.txt")
	if err != nil {
		t.Fatalf("ResolveSyncPath returned error: %v", err)
	}
	want := filepath.Join(root, "projects", "alpha", "nested.txt")
	if got != want {
		t.Errorf("ResolveSyncPath = %q, want %q", got, want)
	}

	if _, err := ResolveSyncPath(root, "../escape.txt"); err == nil {
		t.Error("ResolveSyncPath accepted a traversal path")
	}
}

func TestWriteSyncFileCreatesParents(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "projects", "beta", "deep", "note.txt")

	if err := WriteSyncFile(target, "hello"); err != nil {
		t.Fatalf("WriteSyncFile returned error: %v", err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(content) != "hello" {
		t.Errorf("content = %q, want %q", string(content), "hello")
	}
}

func TestPruneEmptyDirs(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "projects", "beta", "deep", "note.txt")

	if err := WriteSyncFile(target, "hello"); err != nil {
		t.Fatalf("WriteSyncFile returned error: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}

	PruneEmptyDirs(root, target)

	if _, err := os.Stat(filepath.Join(root, "projects")); !os.IsNotExist(err) {
		t.Error("empty parent directories were not pruned")
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("root directory was removed: %v", err)
	}
}

func TestPruneEmptyDirsKeepsNonEmptyParents(t *testing.T) {
	root := t.TempDir()
	removed := filepath.Join(root, "projects", "beta", "note.txt")
	kept := filepath.Join(root, "projects", "beta", "other.txt")

	for _, path := range []string{removed, kept} {
		if err := WriteSyncFile(path, "hello"); err != nil {
			t.Fatalf("WriteSyncFile returned error: %v", err)
		}
	}
	if err := os.Remove(removed); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}

	PruneEmptyDirs(root, removed)

	if _, err := os.Stat(kept); err != nil {
		t.Errorf("sibling file was removed: %v", err)
	}
}

func TestListTextFiles(t *testing.T) {
	root := t.TempDir()

	files := []string{
		"note1.txt",
		filepath.Join("projects", "alpha", "nested.txt"),
		filepath.Join("projects", "beta", "deep", "note4.txt"),
		filepath.Join("projects", "ignored.md"),
	}
	for _, f := range files {
		if err := WriteSyncFile(filepath.Join(root, f), "x"); err != nil {
			t.Fatalf("WriteSyncFile(%q) returned error: %v", f, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	got, err := ListTextFiles(root)
	if err != nil {
		t.Fatalf("ListTextFiles returned error: %v", err)
	}

	want := []string{
		"note1.txt",
		"projects/alpha/nested.txt",
		"projects/beta/deep/note4.txt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListTextFiles = %v, want %v", got, want)
	}
}

func TestListTextFilesMissingRoot(t *testing.T) {
	if _, err := ListTextFiles(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("ListTextFiles accepted a missing root")
	}
}

func TestCanonicalSyncName(t *testing.T) {
	cases := map[string]string{
		"\u1102\u1169\u1110\u1173.txt": "\uB178\uD2B8.txt",
		"\uB178\uD2B8.txt":             "\uB178\uD2B8.txt",
		`projects\\alpha\\nested.txt`:  "projects//alpha//nested.txt",
		"cafe\u0301.txt":               "caf\u00e9.txt",
	}

	for input, want := range cases {
		if got := CanonicalSyncName(input); got != want {
			t.Errorf("CanonicalSyncName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeSyncPathNormalizesUnicode(t *testing.T) {
	got, err := NormalizeSyncPath("projects/\u1102\u1169\u1110\u1173.txt")
	if err != nil {
		t.Fatalf("NormalizeSyncPath returned error: %v", err)
	}
	if want := "projects/\uB178\uD2B8.txt"; got != want {
		t.Errorf("NormalizeSyncPath = %q, want %q", got, want)
	}
}

func TestListSyncFilesKeysAreCanonical(t *testing.T) {
	root := t.TempDir()
	nfd := "\u1102\u1169\u1110\u1173.txt"
	nfc := "\uB178\uD2B8.txt"

	if err := os.WriteFile(filepath.Join(root, nfd), []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	files, err := ListSyncFiles(root)
	if err != nil {
		t.Fatalf("ListSyncFiles returned error: %v", err)
	}
	actual, ok := files[nfc]
	if !ok {
		t.Fatalf("ListSyncFiles = %v, want a %q key", files, nfc)
	}
	if actual != nfd {
		t.Errorf("actual path = %q, want %q", actual, nfd)
	}
}

func TestWriteSyncFileReplacesAtomically(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "note.txt")

	if err := WriteSyncFile(target, "first"); err != nil {
		t.Fatalf("WriteSyncFile returned error: %v", err)
	}
	if err := WriteSyncFile(target, "second"); err != nil {
		t.Fatalf("WriteSyncFile returned error: %v", err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(content) != "second" {
		t.Errorf("content = %q, want %q", string(content), "second")
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only the note", len(entries))
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
}
