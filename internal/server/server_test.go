package server

import (
	"bytes"
	"encoding/json"
	"hsync/internal/protocol"
	"hsync/internal/utils"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T, seed map[string]string) (*httptest.Server, string) {
	t.Helper()

	dataDir := t.TempDir()
	for name, content := range seed {
		writeFile(t, dataDir, name, content)
	}
	return serve(t, dataDir), dataDir
}

func serve(t *testing.T, dataDir string) *httptest.Server {
	t.Helper()

	handler, err := NewHandler(dataDir, "test-key")
	require.NoError(t, err)

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	require.NoError(t, utils.WriteFile(utils.LocalPath(root, name), content))
}

func do(t *testing.T, ts *httptest.Server, method, path string, body io.Reader) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, ts.URL+path, body)
	require.NoError(t, err)
	req.Header.Set("X-Sync-Key", "test-key")

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	return resp
}

func index(t *testing.T, ts *httptest.Server) protocol.IndexResponse {
	t.Helper()

	resp := do(t, ts, http.MethodGet, "/index", nil)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var parsed protocol.IndexResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&parsed))
	return parsed
}

func push(t *testing.T, ts *httptest.Server, req protocol.PushRequest) (protocol.PushResponse, int) {
	t.Helper()

	body, err := json.Marshal(req)
	require.NoError(t, err)

	resp := do(t, ts, http.MethodPost, "/push", bytes.NewReader(body))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return protocol.PushResponse{}, resp.StatusCode
	}

	var parsed protocol.PushResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&parsed))
	return parsed, resp.StatusCode
}

func blobs(contents ...string) map[string]string {
	result := make(map[string]string, len(contents))
	for _, content := range contents {
		result[utils.CalculateHash(content)] = content
	}
	return result
}

func TestIndexListsWorkingTreeNotes(t *testing.T) {
	ts, _ := newTestServer(t, map[string]string{
		"note.txt":                "Note\n",
		"projects/alpha/deep.txt": "Deep\n",
	})

	parsed := index(t, ts)
	assert.NotEmpty(t, parsed.Root)
	assert.NotEqual(t, parsed.Root, parsed.Commit, "head should have advanced past the root commit")
	assert.Equal(t, map[string]string{
		"note.txt":                utils.CalculateHash("Note\n"),
		"projects/alpha/deep.txt": utils.CalculateHash("Deep\n"),
	}, parsed.Files)
}

func TestPushRejections(t *testing.T) {
	ts, dataDir := newTestServer(t, nil)
	head := index(t, ts).Commit
	noteHash := utils.CalculateHash("Note\n")

	cases := []struct {
		name    string
		request protocol.PushRequest
		want    int
	}{
		{
			name:    "without a parent",
			request: protocol.PushRequest{Files: map[string]string{"note.txt": noteHash}, Blobs: blobs("Note\n")},
			want:    http.StatusBadRequest,
		},
		{
			name:    "with an unknown parent",
			request: protocol.PushRequest{Parent: utils.CalculateHash("nope"), Files: map[string]string{"note.txt": noteHash}, Blobs: blobs("Note\n")},
			want:    http.StatusConflict,
		},
		{
			name:    "without the note content",
			request: protocol.PushRequest{Parent: head, Files: map[string]string{"note.txt": utils.CalculateHash("Never uploaded\n")}},
			want:    http.StatusUnprocessableEntity,
		},
		{
			name:    "with content that does not match its hash",
			request: protocol.PushRequest{Parent: head, Files: map[string]string{"note.txt": noteHash}, Blobs: map[string]string{noteHash: "Different\n"}},
			want:    http.StatusBadRequest,
		},
		{
			name:    "with a traversal path",
			request: protocol.PushRequest{Parent: head, Files: map[string]string{"../escape.txt": utils.CalculateHash("Escaped\n")}, Blobs: blobs("Escaped\n")},
			want:    http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, status := push(t, ts, tc.request)
			assert.Equal(t, tc.want, status)
		})
	}

	assert.NoFileExists(t, filepath.Join(dataDir, "..", "escape.txt"))
}

func TestPushWritesWorkingTree(t *testing.T) {
	ts, dataDir := newTestServer(t, nil)
	start := index(t, ts)

	result, status := push(t, ts, protocol.PushRequest{
		Parent: start.Commit,
		Files: map[string]string{
			"note.txt":        utils.CalculateHash("Note\n"),
			"projects/深い.txt": utils.CalculateHash("Nested\n"),
		},
		Blobs: blobs("Note\n", "Nested\n"),
	})
	require.Equal(t, http.StatusOK, status)
	assert.NotEqual(t, start.Commit, result.Commit, "push should create a commit")
	assert.FileExists(t, utils.LocalPath(dataDir, "projects/深い.txt"))
}

func TestPushFromStaleParentKeepsBothChanges(t *testing.T) {
	ts, dataDir := newTestServer(t, map[string]string{"a.txt": "A\n"})
	start := index(t, ts)

	_, status := push(t, ts, protocol.PushRequest{
		Parent: start.Commit,
		Files:  map[string]string{"a.txt": start.Files["a.txt"], "b.txt": utils.CalculateHash("B\n")},
		Blobs:  blobs("B\n"),
	})
	require.Equal(t, http.StatusOK, status)

	result, status := push(t, ts, protocol.PushRequest{
		Parent: start.Commit,
		Files:  map[string]string{"a.txt": start.Files["a.txt"], "c.txt": utils.CalculateHash("C\n")},
		Blobs:  blobs("C\n"),
	})
	require.Equal(t, http.StatusOK, status)

	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		assert.Contains(t, result.Files, name)
		assert.FileExists(t, utils.LocalPath(dataDir, name))
	}
}

func TestPushWithoutChangesKeepsHead(t *testing.T) {
	ts, _ := newTestServer(t, map[string]string{"a.txt": "A\n"})
	start := index(t, ts)

	result, status := push(t, ts, protocol.PushRequest{Parent: start.Commit, Files: start.Files})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, start.Commit, result.Commit)
}

func TestBlobIsServedByHash(t *testing.T) {
	ts, _ := newTestServer(t, map[string]string{"a.txt": "A\n"})
	parsed := index(t, ts)

	resp := do(t, ts, http.MethodGet, "/blob?"+url.Values{"hash": {parsed.Files["a.txt"]}}.Encode(), nil)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	content, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "A\n", string(content))
}

func TestUnknownBlobIsNotFound(t *testing.T) {
	ts, _ := newTestServer(t, nil)

	resp := do(t, ts, http.MethodGet, "/blob?hash="+utils.CalculateHash("missing"), nil)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHistorySurvivesRestart(t *testing.T) {
	dataDir := t.TempDir()
	writeFile(t, dataDir, "a.txt", "A\n")

	start := index(t, serve(t, dataDir))
	restarted := serve(t, dataDir)
	assert.Equal(t, start.Commit, index(t, restarted).Commit)

	result, status := push(t, restarted, protocol.PushRequest{
		Parent: start.Commit,
		Files:  map[string]string{"a.txt": start.Files["a.txt"], "b.txt": utils.CalculateHash("B\n")},
		Blobs:  blobs("B\n"),
	})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, result.Files, "b.txt", "an old commit must stay usable as a parent")
}

func TestMetadataIsNotListedAsNote(t *testing.T) {
	ts, dataDir := newTestServer(t, map[string]string{"a.txt": "A\n"})

	assert.Len(t, index(t, ts).Files, 1)

	notes, err := utils.ListNotes(dataDir)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a.txt": "a.txt"}, notes)
}

func TestUnauthorizedRequestsAreRejected(t *testing.T) {
	ts, _ := newTestServer(t, nil)

	for method, path := range map[string]string{http.MethodGet: "/index", http.MethodPost: "/push"} {
		req, err := http.NewRequest(method, ts.URL+path, strings.NewReader("{}"))
		require.NoError(t, err)

		resp, err := ts.Client().Do(req)
		require.NoError(t, err)
		resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, path)
	}
}

func TestDoubledSlashKeepsTheMethod(t *testing.T) {
	ts, dataDir := newTestServer(t, nil)

	body, err := json.Marshal(protocol.PushRequest{
		Parent: index(t, ts).Commit,
		Files:  map[string]string{"a.txt": utils.CalculateHash("A\n")},
		Blobs:  blobs("A\n"),
	})
	require.NoError(t, err)

	resp := do(t, ts, http.MethodPost, "//push", bytes.NewReader(body))
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.FileExists(t, utils.LocalPath(dataDir, "a.txt"))
}

func TestConcurrentPushesAreSerialized(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	start := index(t, ts)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			name := string(rune('a'+i)) + ".txt"
			content := string(rune('a'+i)) + "\n"

			_, status := push(t, ts, protocol.PushRequest{
				Parent: start.Commit,
				Files:  map[string]string{name: utils.CalculateHash(content)},
				Blobs:  blobs(content),
			})
			assert.Equal(t, http.StatusOK, status)
		}(i)
	}
	wg.Wait()

	assert.Len(t, index(t, ts).Files, 8)
}
