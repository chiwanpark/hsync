package client

import (
	"fmt"
	"hsync/internal/server"
	"hsync/internal/utils"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type testClient struct {
	cfg   *Config
	state *syncState
	http  *http.Client
}

func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	dataDir := t.TempDir()
	handler, err := server.NewHandler(dataDir, "test-key")
	if err != nil {
		t.Fatalf("NewHandler returned error: %v", err)
	}

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, dataDir
}

func newTestClient(t *testing.T, ts *httptest.Server) *testClient {
	t.Helper()

	dir := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.json")
	st, _, err := loadState(statePath)
	if err != nil {
		t.Fatalf("loadState returned error: %v", err)
	}

	return &testClient{
		cfg:   &Config{ServerURL: ts.URL, Key: "test-key", DirPath: dir, StatePath: statePath},
		state: st,
		http:  ts.Client(),
	}
}

func (c *testClient) sync(force bool) {
	runCycle(c.cfg, c.http, c.state, force)
}

func (c *testClient) path(name string) string {
	return utils.LocalPath(c.cfg.DirPath, name)
}

func (c *testClient) write(t *testing.T, name, content string) {
	t.Helper()
	if err := utils.WriteSyncFile(c.path(name), content); err != nil {
		t.Fatalf("WriteSyncFile(%q) returned error: %v", name, err)
	}
}

func (c *testClient) move(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(c.path(to)), 0755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.Rename(c.path(from), c.path(to)); err != nil {
		t.Fatalf("Rename(%q, %q) returned error: %v", from, to, err)
	}
}

func writeServerFile(t *testing.T, dataDir, name, content string) {
	t.Helper()
	if err := utils.WriteSyncFile(utils.LocalPath(dataDir, name), content); err != nil {
		t.Fatalf("WriteSyncFile(%q) returned error: %v", name, err)
	}
}

func assertContent(t *testing.T, root, name, want string) {
	t.Helper()

	data, err := os.ReadFile(utils.LocalPath(root, name))
	if err != nil {
		t.Fatalf("ReadFile(%q) returned error: %v", name, err)
	}
	if string(data) != want {
		t.Errorf("%s = %q, want %q", name, string(data), want)
	}
}

func assertMissing(t *testing.T, root, name string) {
	t.Helper()

	if _, err := os.Stat(utils.LocalPath(root, name)); !os.IsNotExist(err) {
		t.Errorf("%s still exists under %s", name, root)
	}
}

func TestMoveNoteIntoDirectory(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "note.txt", "Initial Note")

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync(true)
	b.sync(true)
	assertContent(t, a.cfg.DirPath, "note.txt", "Initial Note")

	a.move(t, "note.txt", "projects/note.txt")
	a.sync(false)

	assertContent(t, dataDir, "projects/note.txt", "Initial Note")
	assertMissing(t, dataDir, "note.txt")

	b.sync(false)
	assertContent(t, b.cfg.DirPath, "projects/note.txt", "Initial Note")
	assertMissing(t, b.cfg.DirPath, "note.txt")

	a.sync(false)
	b.sync(false)
	assertMissing(t, a.cfg.DirPath, "note.txt")
	assertMissing(t, b.cfg.DirPath, "note.txt")
}

func TestMoveSurvivesClientRestart(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "note.txt", "Initial Note")

	a := newTestClient(t, ts)
	a.sync(true)
	a.move(t, "note.txt", "projects/note.txt")
	a.sync(false)

	restarted := &testClient{cfg: a.cfg, http: a.http}
	st, existed, err := loadState(a.cfg.StatePath)
	if err != nil {
		t.Fatalf("loadState returned error: %v", err)
	}
	if !existed {
		t.Fatal("expected persisted sync state after a cycle")
	}
	restarted.state = st
	restarted.sync(false)

	assertMissing(t, a.cfg.DirPath, "note.txt")
	assertContent(t, a.cfg.DirPath, "projects/note.txt", "Initial Note")
}

func TestMoveKeepsNestedEditsMerged(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "projects/alpha/nested.txt", "Nested Note")

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync(true)
	b.sync(true)

	a.move(t, "projects/alpha/nested.txt", "archive/2024/nested.txt")
	a.write(t, "archive/2024/nested.txt", "Nested Note edited")
	a.sync(false)
	b.sync(false)

	assertContent(t, dataDir, "archive/2024/nested.txt", "Nested Note edited")
	assertContent(t, b.cfg.DirPath, "archive/2024/nested.txt", "Nested Note edited")
	assertMissing(t, dataDir, "projects/alpha/nested.txt")
	assertMissing(t, b.cfg.DirPath, "projects/alpha/nested.txt")
	assertMissing(t, b.cfg.DirPath, "projects/alpha")
}

func TestSameMoveOnTwoClientsKeepsSingleCopy(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "note.txt", "Shared Note Line\n")

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync(true)
	b.sync(true)

	a.move(t, "note.txt", "projects/note.txt")
	b.move(t, "note.txt", "projects/note.txt")
	a.sync(false)
	b.sync(false)

	assertContent(t, dataDir, "projects/note.txt", "Shared Note Line\n")
	assertContent(t, a.cfg.DirPath, "projects/note.txt", "Shared Note Line\n")
	assertContent(t, b.cfg.DirPath, "projects/note.txt", "Shared Note Line\n")
}

func TestDeletePropagatesToOtherClient(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "projects/note.txt", "Note")

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync(true)
	b.sync(true)

	if err := os.Remove(a.path("projects/note.txt")); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	a.sync(false)
	b.sync(false)

	assertMissing(t, dataDir, "projects/note.txt")
	assertMissing(t, b.cfg.DirPath, "projects/note.txt")
	assertMissing(t, b.cfg.DirPath, "projects")
}

func TestDeleteKeepsUnsyncedLocalEdits(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "note.txt", "Note")

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync(true)
	b.sync(true)

	a.write(t, "note.txt", "Edited while offline")
	if err := os.Remove(b.path("note.txt")); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	b.sync(false)
	assertMissing(t, dataDir, "note.txt")

	a.sync(false)
	assertContent(t, a.cfg.DirPath, "note.txt", "Edited while offline")
	assertContent(t, dataDir, "note.txt", "Edited while offline")

	b.sync(false)
	assertContent(t, b.cfg.DirPath, "note.txt", "Edited while offline")
}

func TestStaleDeleteIsRefused(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "note.txt", "Note")

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync(true)
	b.sync(true)

	a.write(t, "note.txt", "Updated by A")
	a.sync(false)

	if err := os.Remove(b.path("note.txt")); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	b.sync(false)

	assertContent(t, dataDir, "note.txt", "Updated by A")
	b.sync(false)
	assertContent(t, b.cfg.DirPath, "note.txt", "Updated by A")
}

func TestIdenticalNewFileIsAdoptedWithoutMerge(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "projects/note.txt", "Same Content\n")

	a := newTestClient(t, ts)
	a.write(t, "projects/note.txt", "Same Content\n")
	a.sync(false)

	assertContent(t, dataDir, "projects/note.txt", "Same Content\n")
	assertContent(t, a.cfg.DirPath, "projects/note.txt", "Same Content\n")
}

func TestTombstoneStateIsPersisted(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "note.txt", "Note")

	a := newTestClient(t, ts)
	a.sync(true)
	a.move(t, "note.txt", "projects/note.txt")
	a.sync(false)
	a.sync(false)

	st, existed, err := loadState(a.cfg.StatePath)
	if err != nil {
		t.Fatalf("loadState returned error: %v", err)
	}
	if !existed {
		t.Fatal("expected a persisted state file")
	}
	if _, ok := st.Base["projects/note.txt"]; !ok {
		t.Error("state is missing the moved file")
	}
	if _, ok := st.Base["note.txt"]; ok {
		t.Error("state still tracks the old path")
	}
	if _, ok := st.Tombstones["note.txt"]; !ok {
		t.Error("state is missing the applied tombstone")
	}
}

func TestPreexistingLocalNoteIsUploadedOnFirstStart(t *testing.T) {
	ts, dataDir := newTestServer(t)

	a := newTestClient(t, ts)
	a.write(t, "A.txt", "Local only note")
	a.write(t, "projects/B.txt", "Local only nested note")
	a.sync(true)

	assertContent(t, a.cfg.DirPath, "A.txt", "Local only note")
	assertContent(t, a.cfg.DirPath, "projects/B.txt", "Local only nested note")
	assertContent(t, dataDir, "A.txt", "Local only note")
	assertContent(t, dataDir, "projects/B.txt", "Local only nested note")
}

func TestPreexistingLocalNoteSurvivesOldTombstone(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "A.txt", "Deleted elsewhere")

	b := newTestClient(t, ts)
	b.sync(true)
	if err := os.Remove(b.path("A.txt")); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	b.sync(false)
	assertMissing(t, dataDir, "A.txt")

	a := newTestClient(t, ts)
	a.write(t, "A.txt", "Never synced note")
	a.sync(true)

	assertContent(t, a.cfg.DirPath, "A.txt", "Never synced note")
	assertContent(t, dataDir, "A.txt", "Never synced note")
}

func TestServerDataLossRestoresTrackedNote(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "A.txt", "Synced note")

	a := newTestClient(t, ts)
	a.sync(true)

	if err := os.Remove(utils.LocalPath(dataDir, "A.txt")); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	a.sync(false)

	assertContent(t, a.cfg.DirPath, "A.txt", "Synced note")
	assertContent(t, dataDir, "A.txt", "Synced note")
}

func TestServerDataLossKeepsLocalEdits(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "A.txt", "Synced note")

	a := newTestClient(t, ts)
	a.sync(true)

	if err := os.Remove(utils.LocalPath(dataDir, "A.txt")); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	a.write(t, "A.txt", "Synced note with a local edit")
	a.sync(false)

	assertContent(t, a.cfg.DirPath, "A.txt", "Synced note with a local edit")
	assertContent(t, dataDir, "A.txt", "Synced note with a local edit")
}

func snapshot(t *testing.T, root string) string {
	t.Helper()

	names, err := utils.ListTextFiles(root)
	if err != nil {
		t.Fatalf("ListTextFiles(%s) returned error: %v", root, err)
	}

	var sb strings.Builder
	for _, name := range names {
		data, err := os.ReadFile(utils.LocalPath(root, name))
		if err != nil {
			t.Fatalf("ReadFile returned error: %v", err)
		}
		fmt.Fprintf(&sb, "%s=%q\n", name, string(data))
	}
	return sb.String()
}

func assertConverged(t *testing.T, dataDir string, clients []*testClient) {
	t.Helper()

	want := snapshot(t, dataDir)
	for i, c := range clients {
		if got := snapshot(t, c.cfg.DirPath); got != want {
			t.Errorf("client %d diverged from the server:\ngot:\n%swant:\n%s", i, got, want)
		}
	}
}

func TestMoveDoesNotDropAConcurrentEdit(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "X.txt", "Line one\n")

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync(true)
	b.sync(true)

	b.write(t, "X.txt", "Line one\nLine two by B\n")

	staleFiles, err := fetchFileList(b.cfg, b.http)
	if err != nil {
		t.Fatalf("fetchFileList returned error: %v", err)
	}

	a.move(t, "X.txt", "Y.txt")
	a.sync(false)

	stalePaths, err := utils.ListSyncFiles(b.cfg.DirPath)
	if err != nil {
		t.Fatalf("ListSyncFiles returned error: %v", err)
	}
	pushLocalChanges(b.cfg, b.http, b.state, staleFiles, stalePaths)
	assertContent(t, b.cfg.DirPath, "X.txt", "Line one\nLine two by B\n")

	b.sync(false)
	a.sync(false)

	assertContent(t, dataDir, "Y.txt", "Line one\nLine two by B\n")
	assertMissing(t, dataDir, "X.txt")
	assertMissing(t, b.cfg.DirPath, "X.txt")
	assertConverged(t, dataDir, []*testClient{a, b})
}

func TestCompetingMovesConvergeToOneNote(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "X.txt", "Shared\n")

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync(true)
	b.sync(true)

	a.move(t, "X.txt", "alpha/X.txt")
	b.move(t, "X.txt", "beta/X.txt")

	for round := 0; round < 4; round++ {
		a.sync(false)
		b.sync(false)
	}

	names, err := utils.ListTextFiles(dataDir)
	if err != nil {
		t.Fatalf("ListTextFiles returned error: %v", err)
	}
	if len(names) != 1 {
		t.Errorf("server holds %v, want a single note", names)
	}
	assertConverged(t, dataDir, []*testClient{a, b})
}

func TestThreeClientsConverge(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "note1.txt", "One\n")
	writeServerFile(t, dataDir, "projects/note2.txt", "Two\n")
	writeServerFile(t, dataDir, "projects/note3.txt", "Three\n")

	clients := []*testClient{newTestClient(t, ts), newTestClient(t, ts), newTestClient(t, ts)}
	for _, c := range clients {
		c.sync(true)
	}
	a, b, c := clients[0], clients[1], clients[2]

	a.move(t, "note1.txt", "archive/note1.txt")
	b.write(t, "projects/note2.txt", "Two edited by B\n")
	if err := os.Remove(c.path("projects/note3.txt")); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	c.write(t, "new/fromC.txt", "Created by C\n")

	for round := 0; round < 6; round++ {
		for _, cl := range clients {
			cl.sync(false)
		}
	}

	assertContent(t, dataDir, "archive/note1.txt", "One\n")
	assertContent(t, dataDir, "projects/note2.txt", "Two edited by B\n")
	assertContent(t, dataDir, "new/fromC.txt", "Created by C\n")
	assertMissing(t, dataDir, "projects/note3.txt")
	assertConverged(t, dataDir, clients)
}

func TestConcurrentClientsConverge(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "shared.txt", "Base\n")

	clients := []*testClient{newTestClient(t, ts), newTestClient(t, ts), newTestClient(t, ts), newTestClient(t, ts)}
	for _, c := range clients {
		c.sync(true)
	}

	var wg sync.WaitGroup
	for i, cl := range clients {
		wg.Add(1)
		go func(i int, cl *testClient) {
			defer wg.Done()
			for round := 0; round < 5; round++ {
				cl.write(t, fmt.Sprintf("client%d.txt", i), fmt.Sprintf("round %d\n", round))
				cl.sync(false)
			}
		}(i, cl)
	}
	wg.Wait()

	for round := 0; round < 4; round++ {
		for _, cl := range clients {
			cl.sync(false)
		}
	}

	assertConverged(t, dataDir, clients)
}

func TestNoteNameNormalizationAcrossMachines(t *testing.T) {
	nfc := "\uB178\uD2B8.txt"
	nfd := "\u1102\u1169\u1110\u1173.txt"

	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, nfc, "Korean note\n")

	linux := newTestClient(t, ts)
	linux.sync(true)

	mac := newTestClient(t, ts)
	mac.write(t, nfd, "Korean note\n")
	mac.sync(true)
	mac.sync(false)
	linux.sync(false)

	names, err := utils.ListTextFiles(dataDir)
	if err != nil {
		t.Fatalf("ListTextFiles returned error: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("server holds %q, want a single note", names)
	}
	if names[0] != nfc {
		t.Errorf("server note name = %q, want the NFC form", names[0])
	}
	assertContent(t, dataDir, nfc, "Korean note\n")
}

func TestDecomposedLocalNameIsNotDuplicated(t *testing.T) {
	nfc := "\uB178\uD2B8.txt"
	nfd := "\u1102\u1169\u1110\u1173.txt"

	ts, dataDir := newTestServer(t)

	mac := newTestClient(t, ts)
	mac.write(t, nfd, "Written on a decomposing filesystem\n")
	mac.sync(true)

	assertContent(t, dataDir, nfc, "Written on a decomposing filesystem\n")

	mac.write(t, nfd, "Edited on a decomposing filesystem\n")
	mac.sync(false)
	mac.sync(false)

	assertContent(t, dataDir, nfc, "Edited on a decomposing filesystem\n")

	local, err := utils.ListTextFiles(mac.cfg.DirPath)
	if err != nil {
		t.Fatalf("ListTextFiles returned error: %v", err)
	}
	if len(local) != 1 {
		t.Errorf("client holds %q, want a single note", local)
	}
}

func TestServerCanonicalizesLegacyNames(t *testing.T) {
	nfc := "\uB178\uD2B8.txt"
	nfd := "\u1102\u1169\u1110\u1173.txt"

	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, nfd), []byte("Legacy\n"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	handler, err := server.NewHandler(dataDir, "test-key")
	if err != nil {
		t.Fatalf("NewHandler returned error: %v", err)
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	names, err := utils.ListTextFiles(dataDir)
	if err != nil {
		t.Fatalf("ListTextFiles returned error: %v", err)
	}
	if len(names) != 1 || names[0] != nfc {
		t.Errorf("server holds %q, want the NFC name", names)
	}
}

func TestCaseOnlyCollisionIsSkippedOnCaseInsensitiveDevices(t *testing.T) {
	ts, dataDir := newTestServer(t)
	writeServerFile(t, dataDir, "Note.txt", "Upper\n")
	writeServerFile(t, dataDir, "note.txt", "Lower\n")

	sensitive := newTestClient(t, ts)
	sensitive.sync(true)
	if got := mustCount(t, sensitive.cfg.DirPath); got != 2 {
		t.Errorf("case sensitive device holds %d notes, want 2", got)
	}

	insensitive := newTestClient(t, ts)
	insensitive.cfg.caseInsensitive = true
	insensitive.sync(true)
	if got := mustCount(t, insensitive.cfg.DirPath); got != 1 {
		t.Errorf("case insensitive device holds %d notes, want 1", got)
	}
}

func mustCount(t *testing.T, root string) int {
	t.Helper()
	names, err := utils.ListTextFiles(root)
	if err != nil {
		t.Fatalf("ListTextFiles returned error: %v", err)
	}
	return len(names)
}
