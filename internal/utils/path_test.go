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
