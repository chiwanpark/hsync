package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hsync/internal/protocol"
	"hsync/internal/utils"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	dataDir := t.TempDir()
	handler, err := NewHandler(dataDir, "test-key")
	if err != nil {
		t.Fatalf("NewHandler returned error: %v", err)
	}

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, dataDir
}

func do(t *testing.T, ts *httptest.Server, method, path string, body string) *http.Response {
	t.Helper()

	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}

	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	req.Header.Set("X-Sync-Key", "test-key")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("request %s %s returned error: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func writeFile(t *testing.T, dataDir, name, content string) {
	t.Helper()
	if err := utils.WriteSyncFile(utils.LocalPath(dataDir, name), content); err != nil {
		t.Fatalf("WriteSyncFile returned error: %v", err)
	}
}

func deleteQuery(name, base string) string {
	return "/sync?" + url.Values{"filename": {name}, "base": {utils.CalculateHash(base)}}.Encode()
}

func tombstones(t *testing.T, ts *httptest.Server) map[string]protocol.Tombstone {
	t.Helper()

	resp := do(t, ts, http.MethodGet, "/deleted", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /deleted status = %d, want 200", resp.StatusCode)
	}

	out := make(map[string]protocol.Tombstone)
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode returned error: %v", err)
	}
	return out
}

func TestDeleteRemovesNestedFileAndRecordsTombstone(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeFile(t, dataDir, "projects/alpha/nested.txt", "Nested")

	resp := do(t, ts, http.MethodDelete, deleteQuery("projects/alpha/nested.txt", "Nested"), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", resp.StatusCode)
	}

	if _, err := os.Stat(utils.LocalPath(dataDir, "projects/alpha/nested.txt")); !os.IsNotExist(err) {
		t.Error("file still exists after delete")
	}
	if _, err := os.Stat(utils.LocalPath(dataDir, "projects")); !os.IsNotExist(err) {
		t.Error("empty parent directories were not pruned")
	}

	entry, ok := tombstones(t, ts)["projects/alpha/nested.txt"]
	if !ok {
		t.Fatal("tombstone was not recorded")
	}
	if entry.RenamedTo != "" {
		t.Errorf("RenamedTo = %q, want empty", entry.RenamedTo)
	}
}

func TestDeleteRejectsStaleBase(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeFile(t, dataDir, "note.txt", "Changed on server")

	resp := do(t, ts, http.MethodDelete, deleteQuery("note.txt", "Old content"), "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("DELETE status = %d, want 409", resp.StatusCode)
	}
	if _, err := os.Stat(utils.LocalPath(dataDir, "note.txt")); err != nil {
		t.Errorf("file was removed despite the stale base: %v", err)
	}
}

func TestDeleteRejectsTraversal(t *testing.T) {
	ts, _ := newTestServer(t)

	resp := do(t, ts, http.MethodDelete, "/sync?filename=..%2Fescape.txt", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("DELETE status = %d, want 400", resp.StatusCode)
	}
}

func TestRenameMovesFileAndRecordsTombstone(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeFile(t, dataDir, "note.txt", "Note")

	body, err := json.Marshal(protocol.RenameRequest{From: "note.txt", To: "projects/note.txt", Base: "Note"})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	resp := do(t, ts, http.MethodPost, "/rename", string(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /rename status = %d, want 200", resp.StatusCode)
	}

	var syncResp protocol.SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		t.Fatalf("decode returned error: %v", err)
	}
	if syncResp.Synced != "Note" {
		t.Errorf("Synced = %q, want %q", syncResp.Synced, "Note")
	}

	if _, err := os.Stat(utils.LocalPath(dataDir, "note.txt")); !os.IsNotExist(err) {
		t.Error("source file still exists after rename")
	}
	data, err := os.ReadFile(utils.LocalPath(dataDir, "projects/note.txt"))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(data) != "Note" {
		t.Errorf("target content = %q, want %q", string(data), "Note")
	}

	entry, ok := tombstones(t, ts)["note.txt"]
	if !ok {
		t.Fatal("tombstone was not recorded")
	}
	if entry.RenamedTo != "projects/note.txt" {
		t.Errorf("RenamedTo = %q, want %q", entry.RenamedTo, "projects/note.txt")
	}
}

func TestRenameKeepsExistingTarget(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeFile(t, dataDir, "note.txt", "Source")
	writeFile(t, dataDir, "projects/note.txt", "Target already merged")

	body, err := json.Marshal(protocol.RenameRequest{From: "note.txt", To: "projects/note.txt", Base: "Source"})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	resp := do(t, ts, http.MethodPost, "/rename", string(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /rename status = %d, want 200", resp.StatusCode)
	}

	data, err := os.ReadFile(utils.LocalPath(dataDir, "projects/note.txt"))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(data) != "Target already merged" {
		t.Errorf("target content = %q, want %q", string(data), "Target already merged")
	}
	if _, err := os.Stat(utils.LocalPath(dataDir, "note.txt")); !os.IsNotExist(err) {
		t.Error("source file still exists after rename")
	}
}

func TestRenameRejectsInvalidPaths(t *testing.T) {
	ts, _ := newTestServer(t)

	bodies := []string{
		`{"from":"../escape.txt","to":"note.txt"}`,
		`{"from":"note.txt","to":"../escape.txt"}`,
		`{"from":"note.txt","to":"note.txt"}`,
		`{"from":"note.md","to":"note.txt"}`,
	}
	for _, body := range bodies {
		resp := do(t, ts, http.MethodPost, "/rename", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST /rename %s status = %d, want 400", body, resp.StatusCode)
		}
	}
}

func TestUploadClearsTombstone(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeFile(t, dataDir, "note.txt", "Note")

	do(t, ts, http.MethodDelete, deleteQuery("note.txt", "Note"), "")
	if _, ok := tombstones(t, ts)["note.txt"]; !ok {
		t.Fatal("tombstone was not recorded")
	}

	body, err := json.Marshal(protocol.SyncRequest{Filename: "note.txt", Base: "", Latest: "Recreated"})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	resp := do(t, ts, http.MethodPost, "/sync", string(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /sync status = %d, want 200", resp.StatusCode)
	}

	if _, ok := tombstones(t, ts)["note.txt"]; ok {
		t.Error("tombstone survived a re-upload")
	}
}

func TestTombstonesSurviveRestart(t *testing.T) {
	dataDir := t.TempDir()
	writeFile(t, dataDir, "note.txt", "Note")

	handler, err := NewHandler(dataDir, "test-key")
	if err != nil {
		t.Fatalf("NewHandler returned error: %v", err)
	}
	first := httptest.NewServer(handler)
	do(t, first, http.MethodDelete, deleteQuery("note.txt", "Note"), "")
	first.Close()

	restarted, err := NewHandler(dataDir, "test-key")
	if err != nil {
		t.Fatalf("NewHandler returned error: %v", err)
	}
	second := httptest.NewServer(restarted)
	t.Cleanup(second.Close)

	if _, ok := tombstones(t, second)["note.txt"]; !ok {
		t.Error("tombstone was lost across a restart")
	}
}

func TestTombstoneFileIsNotListedAsNote(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeFile(t, dataDir, "note.txt", "Note")
	do(t, ts, http.MethodDelete, deleteQuery("note.txt", "Note"), "")

	resp := do(t, ts, http.MethodGet, "/sync", "")
	files := make(map[string]string)
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		t.Fatalf("decode returned error: %v", err)
	}
	for name := range files {
		if strings.HasPrefix(name, ".hsync") {
			t.Errorf("internal file %q was listed as a note", name)
		}
	}
}

func TestUnauthorizedRequestsAreRejected(t *testing.T) {
	ts, _ := newTestServer(t)

	for _, path := range []string{"/sync", "/deleted", "/rename"} {
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatalf("NewRequest returned error: %v", err)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("request returned error: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s status = %d, want 401", path, resp.StatusCode)
		}
	}
}

func TestUploadToRenamedPathIsRejected(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeFile(t, dataDir, "X.txt", "Line one\n")

	renameBody, err := json.Marshal(protocol.RenameRequest{From: "X.txt", To: "Y.txt", Base: "Line one\n"})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	do(t, ts, http.MethodPost, "/rename", string(renameBody))

	uploadBody, err := json.Marshal(protocol.SyncRequest{
		Filename: "X.txt",
		Base:     "Line one\n",
		Latest:   "Line one\nLine two\n",
	})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	resp := do(t, ts, http.MethodPost, "/sync", string(uploadBody))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("POST /sync status = %d, want 409", resp.StatusCode)
	}
	if _, err := os.Stat(utils.LocalPath(dataDir, "X.txt")); !os.IsNotExist(err) {
		t.Error("the renamed away path was recreated")
	}
}

func TestUploadRecreatesDeletedFile(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeFile(t, dataDir, "X.txt", "Note")
	do(t, ts, http.MethodDelete, deleteQuery("X.txt", "Note"), "")

	body, err := json.Marshal(protocol.SyncRequest{Filename: "X.txt", Base: "", Latest: "Recreated"})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	resp := do(t, ts, http.MethodPost, "/sync", string(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /sync status = %d, want 200", resp.StatusCode)
	}
	if _, err := os.Stat(utils.LocalPath(dataDir, "X.txt")); err != nil {
		t.Errorf("file was not recreated: %v", err)
	}
}

func TestRenameFollowsAnEarlierRename(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeFile(t, dataDir, "X.txt", "Shared")

	first, err := json.Marshal(protocol.RenameRequest{From: "X.txt", To: "alpha/X.txt", Base: "Shared"})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	do(t, ts, http.MethodPost, "/rename", string(first))

	second, err := json.Marshal(protocol.RenameRequest{From: "X.txt", To: "beta/X.txt", Base: "Shared"})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	resp := do(t, ts, http.MethodPost, "/rename", string(second))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /rename status = %d, want 200", resp.StatusCode)
	}

	if _, err := os.Stat(utils.LocalPath(dataDir, "alpha/X.txt")); !os.IsNotExist(err) {
		t.Error("the intermediate path still holds a copy")
	}
	data, err := os.ReadFile(utils.LocalPath(dataDir, "beta/X.txt"))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(data) != "Shared" {
		t.Errorf("content = %q, want %q", string(data), "Shared")
	}

	entries := tombstones(t, ts)
	if entries["X.txt"].RenamedTo != "beta/X.txt" {
		t.Errorf("X.txt tombstone = %q, want beta/X.txt", entries["X.txt"].RenamedTo)
	}
	if entries["alpha/X.txt"].RenamedTo != "beta/X.txt" {
		t.Errorf("alpha/X.txt tombstone = %q, want beta/X.txt", entries["alpha/X.txt"].RenamedTo)
	}
}

func TestConcurrentRequestsAreSerialized(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeFile(t, dataDir, "shared.txt", "Base\n")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, err := json.Marshal(protocol.SyncRequest{
				Filename: fmt.Sprintf("note%d.txt", i),
				Base:     "",
				Latest:   fmt.Sprintf("content %d\n", i),
			})
			if err != nil {
				t.Errorf("Marshal returned error: %v", err)
				return
			}
			do(t, ts, http.MethodPost, "/sync", string(body))
			do(t, ts, http.MethodGet, "/sync", "")
			do(t, ts, http.MethodGet, "/deleted", "")
		}(i)
	}
	wg.Wait()

	names, err := utils.ListTextFiles(dataDir)
	if err != nil {
		t.Fatalf("ListTextFiles returned error: %v", err)
	}
	if len(names) != 9 {
		t.Errorf("server holds %d notes, want 9: %v", len(names), names)
	}
}
