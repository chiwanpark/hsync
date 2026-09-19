package client

import (
	"fmt"
	"hsync/internal/server"
	"hsync/internal/utils"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testClient struct {
	cfg   *Config
	state *state
	http  *http.Client
}

func newTestServer(t *testing.T, seed map[string]string) (*httptest.Server, string) {
	t.Helper()

	dataDir := t.TempDir()
	for name, content := range seed {
		writeServerFile(t, dataDir, name, content)
	}

	handler, err := server.NewHandler(dataDir, "test-key")
	require.NoError(t, err)

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, dataDir
}

func newTestClient(t *testing.T, ts *httptest.Server) *testClient {
	t.Helper()
	return clientAt(t, ts.URL, ts.Client(), filepath.Join(t.TempDir(), "state.json"))
}

func clientAt(t *testing.T, serverURL string, http *http.Client, statePath string) *testClient {
	t.Helper()

	st, err := loadState(statePath)
	require.NoError(t, err)

	return &testClient{
		cfg:   &Config{ServerURL: serverURL, Key: "test-key", DirPath: t.TempDir(), StatePath: statePath},
		state: st,
		http:  http,
	}
}

func (c *testClient) sync() {
	runCycle(c.cfg, c.http, c.state)
}

func (c *testClient) path(name string) string {
	return utils.LocalPath(c.cfg.DirPath, name)
}

func (c *testClient) write(t *testing.T, name, content string) {
	t.Helper()
	require.NoError(t, utils.WriteFile(c.path(name), content))
}

func (c *testClient) remove(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, os.Remove(c.path(name)))
}

func (c *testClient) move(t *testing.T, from, to string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(c.path(to)), 0755))
	require.NoError(t, os.Rename(c.path(from), c.path(to)))
}

func (c *testClient) restart(t *testing.T) *testClient {
	t.Helper()

	st, err := loadState(c.cfg.StatePath)
	require.NoError(t, err)
	require.NotEmpty(t, st.Commit, "expected a persisted sync state")

	return &testClient{cfg: c.cfg, state: st, http: c.http}
}

func writeServerFile(t *testing.T, dataDir, name, content string) {
	t.Helper()
	require.NoError(t, utils.WriteFile(utils.LocalPath(dataDir, name), content))
}

func readNote(t *testing.T, root, name string) string {
	t.Helper()

	data, err := os.ReadFile(utils.LocalPath(root, name))
	require.NoError(t, err)
	return string(data)
}

func assertContent(t *testing.T, root, name, want string) {
	t.Helper()
	assert.Equal(t, want, readNote(t, root, name), name)
}

func assertContains(t *testing.T, root, name string, wants ...string) {
	t.Helper()

	content := readNote(t, root, name)
	for _, want := range wants {
		assert.Contains(t, content, want, name)
	}
}

func assertMissing(t *testing.T, root, name string) {
	t.Helper()
	assert.NoFileExists(t, utils.LocalPath(root, name))
}

func snapshot(t *testing.T, root string) string {
	t.Helper()

	var sb strings.Builder
	for _, name := range noteNames(t, root) {
		fmt.Fprintf(&sb, "%s=%q\n", name, readNote(t, root, name))
	}
	return sb.String()
}

func assertConverged(t *testing.T, dataDir string, clients []*testClient) {
	t.Helper()

	want := snapshot(t, dataDir)
	for i, c := range clients {
		assert.Equal(t, want, snapshot(t, c.cfg.DirPath), "client %d diverged from the server", i)
	}
}

func TestInitialSyncDownloadsServerNotes(t *testing.T) {
	ts, _ := newTestServer(t, map[string]string{
		"note.txt":                 "Note\n",
		"projects/alpha/deep.txt":  "Deep\n",
		"projects/alpha/other.txt": "Other\n",
	})

	a := newTestClient(t, ts)
	a.sync()

	assertContent(t, a.cfg.DirPath, "note.txt", "Note\n")
	assertContent(t, a.cfg.DirPath, "projects/alpha/deep.txt", "Deep\n")
	assert.NotEmpty(t, a.state.Commit, "client did not record a commit")
}

func TestMoveNoteIntoDirectory(t *testing.T) {
	ts, dataDir := newTestServer(t, map[string]string{"note.txt": "Initial Note"})

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync()
	b.sync()
	assertContent(t, a.cfg.DirPath, "note.txt", "Initial Note")

	a.move(t, "note.txt", "projects/note.txt")
	a.sync()

	assertContent(t, dataDir, "projects/note.txt", "Initial Note")
	assertMissing(t, dataDir, "note.txt")

	b.sync()
	assertContent(t, b.cfg.DirPath, "projects/note.txt", "Initial Note")
	assertMissing(t, b.cfg.DirPath, "note.txt")

	a.sync()
	b.sync()
	assertMissing(t, a.cfg.DirPath, "note.txt")
	assertMissing(t, b.cfg.DirPath, "note.txt")
}

func TestMoveSurvivesClientRestart(t *testing.T) {
	ts, _ := newTestServer(t, map[string]string{"note.txt": "Initial Note"})

	a := newTestClient(t, ts)
	a.sync()
	a.move(t, "note.txt", "projects/note.txt")
	a.sync()

	restarted := a.restart(t)
	restarted.sync()

	assertMissing(t, a.cfg.DirPath, "note.txt")
	assertContent(t, a.cfg.DirPath, "projects/note.txt", "Initial Note")
}

func TestDeletePropagatesToOtherClient(t *testing.T) {
	ts, dataDir := newTestServer(t, map[string]string{"projects/note.txt": "Note"})

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync()
	b.sync()

	a.remove(t, "projects/note.txt")
	a.sync()
	b.sync()

	assertMissing(t, dataDir, "projects/note.txt")
	assertMissing(t, b.cfg.DirPath, "projects/note.txt")
	assertMissing(t, b.cfg.DirPath, "projects")
}

func TestDeleteKeepsUnsyncedLocalEdits(t *testing.T) {
	ts, dataDir := newTestServer(t, map[string]string{"note.txt": "Note"})

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync()
	b.sync()

	a.write(t, "note.txt", "Edited while offline")
	b.remove(t, "note.txt")
	b.sync()
	assertMissing(t, dataDir, "note.txt")

	a.sync()
	assertContent(t, a.cfg.DirPath, "note.txt", "Edited while offline")
	assertContent(t, dataDir, "note.txt", "Edited while offline")

	b.sync()
	assertContent(t, b.cfg.DirPath, "note.txt", "Edited while offline")
}

func TestStateTracksCommitAndFiles(t *testing.T) {
	ts, _ := newTestServer(t, map[string]string{"note.txt": "Note"})

	a := newTestClient(t, ts)
	a.sync()
	a.move(t, "note.txt", "projects/note.txt")
	a.sync()

	st, err := loadState(a.cfg.StatePath)
	require.NoError(t, err)
	assert.NotEmpty(t, st.Commit)
	assert.Contains(t, st.Files, "projects/note.txt")
	assert.NotContains(t, st.Files, "note.txt")
}

func TestPreexistingLocalNoteIsUploadedOnFirstStart(t *testing.T) {
	ts, dataDir := newTestServer(t, nil)

	a := newTestClient(t, ts)
	a.write(t, "A.txt", "Local only note")
	a.write(t, "projects/B.txt", "Local only nested note")
	a.sync()

	assertContent(t, a.cfg.DirPath, "A.txt", "Local only note")
	assertContent(t, a.cfg.DirPath, "projects/B.txt", "Local only nested note")
	assertContent(t, dataDir, "A.txt", "Local only note")
	assertContent(t, dataDir, "projects/B.txt", "Local only nested note")
}

func TestManualServerEditReachesClients(t *testing.T) {
	ts, dataDir := newTestServer(t, map[string]string{"A.txt": "Synced note"})

	a := newTestClient(t, ts)
	a.sync()

	writeServerFile(t, dataDir, "A.txt", "Edited on the server")
	writeServerFile(t, dataDir, "notes/new.txt", "Added on the server")
	a.sync()

	assertContent(t, a.cfg.DirPath, "A.txt", "Edited on the server")
	assertContent(t, a.cfg.DirPath, "notes/new.txt", "Added on the server")
}

func TestLostServerHistoryIsRepopulated(t *testing.T) {
	ts, _ := newTestServer(t, map[string]string{"A.txt": "Synced note"})

	a := newTestClient(t, ts)
	a.sync()

	fresh, freshDir := newTestServer(t, nil)
	a.cfg.ServerURL = fresh.URL
	a.http = fresh.Client()

	a.write(t, "B.txt", "Written while the server was empty")
	a.sync()

	assertContent(t, freshDir, "A.txt", "Synced note")
	assertContent(t, freshDir, "B.txt", "Written while the server was empty")
	assertContent(t, a.cfg.DirPath, "A.txt", "Synced note")
}

func TestStaleClientPushIsMergedIntoNewerHistory(t *testing.T) {
	ts, dataDir := newTestServer(t, map[string]string{"shared.txt": "Line one\n"})

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync()
	b.sync()

	staleCommit := b.state.Commit

	for i := 0; i < 5; i++ {
		a.write(t, fmt.Sprintf("a%d.txt", i), fmt.Sprintf("Note %d\n", i))
		a.sync()
	}
	a.write(t, "shared.txt", "Line one\nLine two by A\n")
	a.sync()

	require.Equal(t, staleCommit, b.state.Commit, "client B should still be behind")

	b.write(t, "shared.txt", "Line one\nLine three by B\n")
	b.sync()

	assertContains(t, dataDir, "shared.txt", "Line two by A", "Line three by B")
	assertContent(t, dataDir, "a0.txt", "Note 0\n")
	assertContent(t, b.cfg.DirPath, "a4.txt", "Note 4\n")

	a.sync()
	b.sync()
	assertConverged(t, dataDir, []*testClient{a, b})
}

func TestStaleClientMoveIsMergedIntoNewerHistory(t *testing.T) {
	ts, dataDir := newTestServer(t, map[string]string{"X.txt": "Line one\n"})

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync()
	b.sync()

	a.write(t, "X.txt", "Line one\nLine two by A\n")
	a.sync()
	a.write(t, "unrelated.txt", "Unrelated\n")
	a.sync()

	b.move(t, "X.txt", "moved/X.txt")
	b.sync()

	assertContent(t, dataDir, "moved/X.txt", "Line one\nLine two by A\n")
	assertMissing(t, dataDir, "X.txt")

	a.sync()
	b.sync()
	assertConverged(t, dataDir, []*testClient{a, b})
}

func TestMoveDoesNotDropAConcurrentEdit(t *testing.T) {
	ts, dataDir := newTestServer(t, map[string]string{"X.txt": "Line one\n"})

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync()
	b.sync()

	b.write(t, "X.txt", "Line one\nLine two by B\n")

	a.move(t, "X.txt", "Y.txt")
	a.sync()

	b.sync()
	a.sync()

	assertContent(t, dataDir, "Y.txt", "Line one\nLine two by B\n")
	assertMissing(t, dataDir, "X.txt")
	assertMissing(t, b.cfg.DirPath, "X.txt")
	assertConverged(t, dataDir, []*testClient{a, b})
}

func TestCompetingMovesConvergeToOneNote(t *testing.T) {
	ts, dataDir := newTestServer(t, map[string]string{"X.txt": "Shared\n"})

	a := newTestClient(t, ts)
	b := newTestClient(t, ts)
	a.sync()
	b.sync()

	a.move(t, "X.txt", "alpha/X.txt")
	b.move(t, "X.txt", "beta/X.txt")

	for round := 0; round < 4; round++ {
		a.sync()
		b.sync()
	}

	assert.Len(t, noteNames(t, dataDir), 1, "the server should hold a single note")
	assertConverged(t, dataDir, []*testClient{a, b})
}

func TestThreeClientsConverge(t *testing.T) {
	ts, dataDir := newTestServer(t, map[string]string{
		"note1.txt":          "One\n",
		"projects/note2.txt": "Two\n",
		"projects/note3.txt": "Three\n",
	})

	clients := []*testClient{newTestClient(t, ts), newTestClient(t, ts), newTestClient(t, ts)}
	for _, c := range clients {
		c.sync()
	}
	a, b, c := clients[0], clients[1], clients[2]

	a.move(t, "note1.txt", "archive/note1.txt")
	b.write(t, "projects/note2.txt", "Two edited by B\n")
	c.remove(t, "projects/note3.txt")
	c.write(t, "new/fromC.txt", "Created by C\n")

	for round := 0; round < 6; round++ {
		for _, cl := range clients {
			cl.sync()
		}
	}

	assertContent(t, dataDir, "archive/note1.txt", "One\n")
	assertContent(t, dataDir, "projects/note2.txt", "Two edited by B\n")
	assertContent(t, dataDir, "new/fromC.txt", "Created by C\n")
	assertMissing(t, dataDir, "projects/note3.txt")
	assertConverged(t, dataDir, clients)
}

func TestConcurrentClientsConverge(t *testing.T) {
	ts, dataDir := newTestServer(t, map[string]string{"shared.txt": "Base\n"})

	clients := []*testClient{newTestClient(t, ts), newTestClient(t, ts), newTestClient(t, ts), newTestClient(t, ts)}
	for _, c := range clients {
		c.sync()
	}

	var wg sync.WaitGroup
	for i, cl := range clients {
		wg.Add(1)
		go func(i int, cl *testClient) {
			defer wg.Done()
			for round := 0; round < 5; round++ {
				cl.write(t, fmt.Sprintf("client%d.txt", i), fmt.Sprintf("round %d\n", round))
				cl.sync()
			}
		}(i, cl)
	}
	wg.Wait()

	for round := 0; round < 4; round++ {
		for _, cl := range clients {
			cl.sync()
		}
	}

	assertConverged(t, dataDir, clients)
}

func TestNoteNameNormalizationAcrossMachines(t *testing.T) {
	nfc := "\uB178\uD2B8.txt"
	nfd := "\u1102\u1169\u1110\u1173.txt"

	ts, dataDir := newTestServer(t, map[string]string{nfc: "Korean note\n"})

	linux := newTestClient(t, ts)
	linux.sync()

	mac := newTestClient(t, ts)
	mac.write(t, nfd, "Korean note\n")
	mac.sync()
	mac.sync()
	linux.sync()

	assert.Equal(t, []string{nfc}, noteNames(t, dataDir), "the server should hold one note in NFC form")
	assertContent(t, dataDir, nfc, "Korean note\n")
}

func TestDecomposedLocalNameIsNotDuplicated(t *testing.T) {
	nfc := "\uB178\uD2B8.txt"
	nfd := "\u1102\u1169\u1110\u1173.txt"

	ts, dataDir := newTestServer(t, nil)

	mac := newTestClient(t, ts)
	mac.write(t, nfd, "Written on a decomposing filesystem\n")
	mac.sync()

	assertContent(t, dataDir, nfc, "Written on a decomposing filesystem\n")

	mac.write(t, nfd, "Edited on a decomposing filesystem\n")
	mac.sync()
	mac.sync()

	assertContent(t, dataDir, nfc, "Edited on a decomposing filesystem\n")

	assert.Len(t, noteNames(t, mac.cfg.DirPath), 1, "the client should hold a single note")
}

func TestServerCanonicalizesLegacyNames(t *testing.T) {
	nfc := "\uB178\uD2B8.txt"
	nfd := "\u1102\u1169\u1110\u1173.txt"

	_, dataDir := newTestServer(t, map[string]string{nfd: "Legacy\n"})

	assert.Equal(t, []string{nfc}, noteNames(t, dataDir))
}

func TestCaseOnlyCollisionIsSkippedOnCaseInsensitiveDevices(t *testing.T) {
	ts, _ := newTestServer(t, map[string]string{
		"Note.txt": "Upper\n",
		"note.txt": "Lower\n",
	})

	sensitive := newTestClient(t, ts)
	sensitive.sync()
	assert.Len(t, noteNames(t, sensitive.cfg.DirPath), 2, "a case sensitive device keeps both notes")

	insensitive := newTestClient(t, ts)
	insensitive.cfg.caseInsensitive = true
	insensitive.sync()
	assert.Len(t, noteNames(t, insensitive.cfg.DirPath), 1, "a case insensitive device skips the colliding note")

	insensitive.sync()
	insensitive.sync()
	assert.Len(t, noteNames(t, insensitive.cfg.DirPath), 1)
	assert.Len(t, noteNames(t, sensitive.cfg.DirPath), 2, "the skipped note must not be deleted for everyone")

	sensitive.sync()
	assertContent(t, sensitive.cfg.DirPath, "Note.txt", "Upper\n")
	assertContent(t, sensitive.cfg.DirPath, "note.txt", "Lower\n")
}

func TestNormalizeServerURL(t *testing.T) {
	valid := map[string]string{
		"http://localhost:8080":         "http://localhost:8080",
		"http://localhost:8080/":        "http://localhost:8080",
		"https://example.com/hsync/":    "https://example.com/hsync",
		"https://example.com/hsync///":  "https://example.com/hsync",
		"  https://example.com/hsync  ": "https://example.com/hsync",
	}

	for input, want := range valid {
		got, err := normalizeServerURL(input)
		if assert.NoError(t, err, input) {
			assert.Equal(t, want, got, input)
		}
	}

	for _, input := range []string{"", "localhost:8080", "ftp://example.com", "http://example.com/hsync?key=1"} {
		_, err := normalizeServerURL(input)
		assert.Error(t, err, input)
	}
}

func TestSyncThroughReverseProxySubpath(t *testing.T) {
	dataDir := t.TempDir()
	writeServerFile(t, dataDir, "seed.txt", "Seed\n")

	handler, err := server.NewHandler(dataDir, "test-key")
	require.NoError(t, err)

	backend := httptest.NewServer(handler)
	t.Cleanup(backend.Close)

	target, err := url.Parse(backend.URL)
	require.NoError(t, err)

	proxy := httptest.NewServer(http.StripPrefix("/heynote", httputil.NewSingleHostReverseProxy(target)))
	t.Cleanup(proxy.Close)

	for _, base := range []string{proxy.URL + "/heynote", proxy.URL + "/heynote/"} {
		normalized, err := normalizeServerURL(base)
		require.NoError(t, err)

		c := clientAt(t, normalized, proxy.Client(), filepath.Join(t.TempDir(), "state.json"))
		c.sync()
		assertContent(t, c.cfg.DirPath, "seed.txt", "Seed\n")

		c.write(t, "pushed.txt", "Pushed through the proxy\n")
		c.sync()
		assertContent(t, dataDir, "pushed.txt", "Pushed through the proxy\n")

		require.NoError(t, os.Remove(utils.LocalPath(dataDir, "pushed.txt")))
	}
}

func noteNames(t *testing.T, root string) []string {
	t.Helper()

	notes, err := utils.ListNotes(root)
	require.NoError(t, err)

	names := make([]string, 0, len(notes))
	for name := range notes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
