package repo

import (
	"hsync/internal/utils"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openRepo(t *testing.T, dir string) *Repo {
	t.Helper()

	r, err := Open(dir)
	require.NoError(t, err)
	return r
}

func writeNote(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, utils.WriteFile(utils.LocalPath(dir, name), content))
}

func blob(content string) (string, map[string]string) {
	id := utils.CalculateHash(content)
	return id, map[string]string{id: content}
}

func TestOpenCommitsExistingNotes(t *testing.T) {
	dir := t.TempDir()
	writeNote(t, dir, "a.txt", "A\n")
	writeNote(t, dir, "nested/b.txt", "B\n")

	head := openRepo(t, dir).Head()
	assert.Equal(t, index("a.txt", "A\n", "nested/b.txt", "B\n"), head.Files)
}

func TestEmptyRepositoryStartsAtRoot(t *testing.T) {
	r := openRepo(t, t.TempDir())

	assert.Equal(t, r.Root(), r.Head().ID)
	assert.Empty(t, r.Head().Files)
}

func TestPushCreatesCommitChain(t *testing.T) {
	dir := t.TempDir()
	r := openRepo(t, dir)

	idA, blobsA := blob("A\n")
	first, err := r.Push(r.Head().ID, map[string]string{"a.txt": idA}, blobsA)
	require.NoError(t, err)

	idB, blobsB := blob("B\n")
	second, err := r.Push(first.ID, map[string]string{"a.txt": idA, "b.txt": idB}, blobsB)
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.Parent)
	assert.Equal(t, second.ID, r.Head().ID)

	content, err := os.ReadFile(utils.LocalPath(dir, "b.txt"))
	require.NoError(t, err)
	assert.Equal(t, "B\n", string(content))
}

func TestPushRejectsUnknownParent(t *testing.T) {
	r := openRepo(t, t.TempDir())

	_, err := r.Push(utils.CalculateHash("nope"), map[string]string{}, nil)
	assert.ErrorIs(t, err, ErrUnknownParent)
}

func TestPushRejectsMissingBlob(t *testing.T) {
	r := openRepo(t, t.TempDir())

	_, err := r.Push(r.Head().ID, map[string]string{"a.txt": utils.CalculateHash("A\n")}, nil)
	assert.ErrorIs(t, err, ErrMissingBlob)
}

func TestHistoryIsReadableAfterReopen(t *testing.T) {
	dir := t.TempDir()
	r := openRepo(t, dir)

	idA, blobsA := blob("A\n")
	first, err := r.Push(r.Head().ID, map[string]string{"a.txt": idA}, blobsA)
	require.NoError(t, err)

	reopened := openRepo(t, dir)
	assert.Equal(t, first.ID, reopened.Head().ID)

	commit, ok, err := reopened.Commit(first.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, idA, commit.Files["a.txt"])

	idB, blobsB := blob("B\n")
	merged, err := reopened.Push(first.ID, map[string]string{"a.txt": idA, "b.txt": idB}, blobsB)
	require.NoError(t, err, "pushing onto an old commit must work")
	assert.Contains(t, merged.Files, "b.txt")
}

func TestWorkingTreeEditsBecomeCommits(t *testing.T) {
	dir := t.TempDir()
	r := openRepo(t, dir)

	idA, blobsA := blob("A\n")
	first, err := r.Push(r.Head().ID, map[string]string{"a.txt": idA}, blobsA)
	require.NoError(t, err)

	writeNote(t, dir, "a.txt", "A edited by hand\n")
	require.NoError(t, r.Refresh())

	assert.NotEqual(t, first.ID, r.Head().ID)
	assert.Equal(t, utils.CalculateHash("A edited by hand\n"), r.Head().Files["a.txt"])
}

func TestCheckoutRemovesEmptyDirectories(t *testing.T) {
	dir := t.TempDir()
	r := openRepo(t, dir)

	idA, blobsA := blob("A\n")
	first, err := r.Push(r.Head().ID, map[string]string{"nested/deep/a.txt": idA}, blobsA)
	require.NoError(t, err)

	_, err = r.Push(first.ID, map[string]string{}, nil)
	require.NoError(t, err)

	assert.NoDirExists(t, filepath.Join(dir, "nested"))
}
