package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckNameAccepts(t *testing.T) {
	cases := map[string]string{
		"note.txt":                     "note.txt",
		"projects/alpha/nested.txt":    "projects/alpha/nested.txt",
		`projects\alpha\nested.txt`:    "projects/alpha/nested.txt",
		"a/b/c/d/e/deeply nested.txt":  "a/b/c/d/e/deeply nested.txt",
		"\u1102\u1169\u1110\u1173.txt": "\uB178\uD2B8.txt",
		"cafe\u0301.txt":               "caf\u00e9.txt",
	}

	for input, want := range cases {
		got, err := CheckName(input)
		require.NoError(t, err, input)
		assert.Equal(t, want, got, input)
	}
}

func TestCheckNameRejects(t *testing.T) {
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
		_, err := CheckName(input)
		assert.ErrorIs(t, err, ErrInvalidPath, input)
	}
}

func TestLocalPath(t *testing.T) {
	got := LocalPath("/root", "projects/alpha/nested.txt")
	assert.Equal(t, filepath.Join("/root", "projects", "alpha", "nested.txt"), got)
}

func TestListNotes(t *testing.T) {
	root := t.TempDir()
	nfd := "\u1102\u1169\u1110\u1173.txt"

	for _, name := range []string{"a.txt", "nested/b.txt", "ignored.md", nfd} {
		require.NoError(t, WriteFile(LocalPath(root, name), "x"))
	}
	require.NoError(t, WriteFile(filepath.Join(root, ".hidden", "c.txt"), "x"))

	notes, err := ListNotes(root)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"a.txt":            "a.txt",
		"nested/b.txt":     "nested/b.txt",
		"\uB178\uD2B8.txt": nfd,
	}, notes)
}

func TestWriteFileReplacesAtomically(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "note.txt")

	require.NoError(t, WriteFile(target, "first"))
	require.NoError(t, WriteFile(target, "second"))

	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "second", string(content))

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the temporary file should be gone")

	info, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
}

func TestRemoveFilePrunesEmptyDirectories(t *testing.T) {
	root := t.TempDir()
	nested := LocalPath(root, "a/b/c/note.txt")
	kept := LocalPath(root, "a/keep.txt")

	for _, path := range []string{nested, kept} {
		require.NoError(t, WriteFile(path, "x"))
	}
	require.NoError(t, RemoveFile(root, nested))

	assert.NoDirExists(t, filepath.Join(root, "a", "b"))
	assert.DirExists(t, filepath.Join(root, "a"), "a directory that still holds a note must stay")
}
