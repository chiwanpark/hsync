package repo

import (
	"hsync/internal/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryBlobs struct {
	contents map[string]string
}

func newMemoryBlobs(contents ...string) *memoryBlobs {
	blobs := &memoryBlobs{contents: map[string]string{}}
	for _, content := range contents {
		blobs.contents[utils.CalculateHash(content)] = content
	}
	return blobs
}

func (m *memoryBlobs) load(id string) (string, error) {
	return m.contents[id], nil
}

func (m *memoryBlobs) store(content string) (string, error) {
	id := utils.CalculateHash(content)
	m.contents[id] = content
	return id, nil
}

func index(names ...string) map[string]string {
	result := make(map[string]string, len(names)/2)
	for i := 0; i+1 < len(names); i += 2 {
		result[names[i]] = utils.CalculateHash(names[i+1])
	}
	return result
}

func TestMergeIndex(t *testing.T) {
	cases := []struct {
		name   string
		base   map[string]string
		head   map[string]string
		theirs map[string]string
		want   map[string]string
	}{
		{
			name:   "server edit is kept when the client did not change the note",
			base:   index("a.txt", "A"),
			head:   index("a.txt", "A on the server"),
			theirs: index("a.txt", "A"),
			want:   index("a.txt", "A on the server"),
		},
		{
			name:   "client edit is taken when the server did not change the note",
			base:   index("a.txt", "A"),
			head:   index("a.txt", "A"),
			theirs: index("a.txt", "A by the client"),
			want:   index("a.txt", "A by the client"),
		},
		{
			name:   "identical new note is adopted without merging",
			base:   map[string]string{},
			head:   index("a.txt", "Same"),
			theirs: index("a.txt", "Same"),
			want:   index("a.txt", "Same"),
		},
		{
			name:   "clean deletion is applied",
			base:   index("a.txt", "A", "b.txt", "B"),
			head:   index("a.txt", "A", "b.txt", "B"),
			theirs: index("a.txt", "A"),
			want:   index("a.txt", "A"),
		},
		{
			name:   "deletion loses against a concurrent server edit",
			base:   index("a.txt", "A"),
			head:   index("a.txt", "A on the server"),
			theirs: map[string]string{},
			want:   index("a.txt", "A on the server"),
		},
		{
			name:   "unsynchronized local edit survives a remote deletion",
			base:   index("a.txt", "A"),
			head:   map[string]string{},
			theirs: index("a.txt", "A by the client"),
			want:   index("a.txt", "A by the client"),
		},
		{
			name:   "move is applied",
			base:   index("a.txt", "A"),
			head:   index("a.txt", "A"),
			theirs: index("moved/a.txt", "A"),
			want:   index("moved/a.txt", "A"),
		},
		{
			name:   "server edit follows a move made by the client",
			base:   index("a.txt", "A"),
			head:   index("a.txt", "A on the server"),
			theirs: index("moved/a.txt", "A"),
			want:   index("moved/a.txt", "A on the server"),
		},
		{
			name:   "client edit follows a move made on the server",
			base:   index("a.txt", "A"),
			head:   index("moved/a.txt", "A"),
			theirs: index("a.txt", "A by the client"),
			want:   index("moved/a.txt", "A by the client"),
		},
		{
			name:   "competing moves converge on the committed one",
			base:   index("a.txt", "A"),
			head:   index("alpha/a.txt", "A"),
			theirs: index("beta/a.txt", "A"),
			want:   index("alpha/a.txt", "A"),
		},
		{
			name:   "note added by the client is kept next to the server notes",
			base:   index("a.txt", "A"),
			head:   index("a.txt", "A", "b.txt", "B"),
			theirs: index("a.txt", "A", "c.txt", "C"),
			want:   index("a.txt", "A", "b.txt", "B", "c.txt", "C"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blobs := newMemoryBlobs("A", "B", "C", "Same", "A on the server", "A by the client")

			got, err := mergeIndex(tc.base, tc.head, tc.theirs, blobs)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMergeIndexCombinesConcurrentEdits(t *testing.T) {
	blobs := newMemoryBlobs("Line one\n", "Line one\nServer line\n", "Client line\nLine one\n")

	got, err := mergeIndex(
		index("a.txt", "Line one\n"),
		index("a.txt", "Line one\nServer line\n"),
		index("a.txt", "Client line\nLine one\n"),
		blobs,
	)
	require.NoError(t, err)

	merged := blobs.contents[got["a.txt"]]
	assert.Contains(t, merged, "Server line")
	assert.Contains(t, merged, "Client line")
}

func TestDetectMoves(t *testing.T) {
	base := index("a.txt", "A", "b.txt", "B")
	next := index("moved/a.txt", "A", "b.txt", "B")

	assert.Equal(t, map[string]string{"a.txt": "moved/a.txt"}, detectMoves(base, next))
}

func TestMergeWithoutCommonAncestor(t *testing.T) {
	note := "Shopping list\n- milk\n- bread\n"

	cases := []struct {
		name   string
		theirs string
		want   string
	}{
		{name: "identical content", theirs: note, want: note},
		{name: "extra trailing newline", theirs: note + "\n", want: note},
		{name: "windows line endings", theirs: strings.ReplaceAll(note, "\n", "\r\n"), want: note},
		{name: "line added by the other side", theirs: note + "- coffee\n", want: note + "- coffee\n"},
		{name: "line inserted by the other side", theirs: "Shopping list\n- milk\n- eggs\n- bread\n", want: "Shopping list\n- milk\n- eggs\n- bread\n"},
		{name: "line missing on the other side", theirs: "Shopping list\n- milk\n", want: note},
		{name: "both sides have their own line", theirs: "Shopping list\n- milk\n- tea\n", want: note + "- tea\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blobs := newMemoryBlobs(note, tc.theirs, tc.want)

			got, err := mergeIndex(map[string]string{}, index("note.txt", note), index("note.txt", tc.theirs), blobs)
			require.NoError(t, err)

			require.Len(t, got, 1, "a note without an ancestor must not be copied")
			assert.Equal(t, tc.want, blobs.contents[got["note.txt"]])
		})
	}
}

func TestMergeWithoutCommonAncestorKeepsEachLineOnce(t *testing.T) {
	note := "Line one\nLine two\nLine three\n"
	theirs := "Line one\nLine two\nLine three\nLine four\n"
	blobs := newMemoryBlobs(note, theirs)

	got, err := mergeIndex(map[string]string{}, index("note.txt", note), index("note.txt", theirs), blobs)
	require.NoError(t, err)

	merged := blobs.contents[got["note.txt"]]
	for _, line := range []string{"Line one", "Line two", "Line three", "Line four"} {
		assert.Equal(t, 1, strings.Count(merged, line), "%s appears more than once in %q", line, merged)
	}
}
