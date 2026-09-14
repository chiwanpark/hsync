package client

import (
	"crypto/tls"
	"errors"
	"flag"
	"hsync/internal/protocol"
	"hsync/internal/utils"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config holds the client configuration
type Config struct {
	ServerURL          string `toml:"server"`
	Key                string `toml:"key"`
	DirPath            string `toml:"dir"`
	StatePath          string `toml:"state"`
	Interval           string `toml:"interval"`
	Timeout            string `toml:"timeout"`
	InsecureSkipVerify bool   `toml:"insecureSkipVerify"`

	caseInsensitive bool
}

func getDefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}

	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Heynote", "notes")
	case "linux":
		return filepath.Join(home, ".config", "Heynote", "notes")
	default:
		return "."
	}
}

func getHTTPClient(cfg *Config, timeout time.Duration) *http.Client {
	client := &http.Client{Timeout: timeout}
	if cfg.InsecureSkipVerify {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	return client
}

func Run(args []string) {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	var configPath string
	fs.StringVar(&configPath, "config", "", "Path to configuration file")

	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Could not determine home directory: %v", err)
		}
		configPath = filepath.Join(home, ".config", "hsync.toml")
	}

	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Fatalf("Configuration file not found: %s", configPath)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("Error parsing config file: %v", err)
	}

	// Set defaults if missing in TOML
	if cfg.ServerURL == "" {
		cfg.ServerURL = "http://localhost:8080"
	}
	if cfg.Key == "" {
		cfg.Key = "default-secret"
	}
	if cfg.DirPath == "" {
		cfg.DirPath = getDefaultDir()
	}
	if cfg.StatePath == "" {
		cfg.StatePath = defaultStatePath(cfg.DirPath)
	}

	var interval time.Duration
	if cfg.Interval == "" {
		interval = 5 * time.Second
	} else {
		var err error
		interval, err = time.ParseDuration(cfg.Interval)
		if err != nil {
			log.Fatalf("Error parsing interval: %v", err)
		}
	}

	timeout := 60 * time.Second
	if cfg.Timeout != "" {
		parsed, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			log.Fatalf("Error parsing timeout: %v", err)
		}
		timeout = parsed
	}

	// Ensure local dir exists
	if err := os.MkdirAll(cfg.DirPath, 0755); err != nil {
		log.Fatal(err)
	}

	log.Printf("Starting client syncing to %s with dir %s", cfg.ServerURL, cfg.DirPath)
	if cfg.InsecureSkipVerify {
		log.Println("WARNING: TLS certificate verification skipped")
	}

	cfg.caseInsensitive = utils.IsCaseInsensitiveDir(cfg.DirPath)
	if cfg.caseInsensitive {
		log.Println("Note directory is case insensitive; notes whose names differ only in letter case are skipped")
	}

	httpClient := getHTTPClient(&cfg, timeout)

	st, existed, err := loadState(cfg.StatePath)
	if err != nil {
		log.Fatalf("Error loading sync state from %s: %v", cfg.StatePath, err)
	}
	if existed {
		log.Printf("Loaded sync state for %d files from %s", len(st.Base), cfg.StatePath)
	} else {
		log.Printf("No sync state found, downloading server copy into %s", cfg.DirPath)
	}

	runCycle(&cfg, httpClient, st, !existed)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		runCycle(&cfg, httpClient, st, false)
	}
}

func runCycle(cfg *Config, client *http.Client, st *syncState, force bool) {
	serverFiles, err := fetchFileList(cfg, client)
	if err != nil {
		log.Printf("Failed to list files: %v", err)
		return
	}

	tombstones, err := fetchTombstones(cfg, client)
	if err != nil {
		log.Printf("Failed to list server deletions: %v", err)
		tombstones = map[string]protocol.Tombstone{}
	}

	paths, ok := localPaths(cfg)
	if !ok {
		return
	}

	if force {
		for name, tombstone := range tombstones {
			st.Tombstones[name] = tombstone.DeletedAt
		}
		pullFromServer(cfg, client, st, serverFiles, paths, nil, true)

		if paths, ok = localPaths(cfg); !ok {
			return
		}
		pushLocalChanges(cfg, client, st, serverFiles, paths)
	} else {
		applyTombstones(cfg, st, tombstones, paths)

		if paths, ok = localPaths(cfg); !ok {
			return
		}
		handled := pushLocalChanges(cfg, client, st, serverFiles, paths)

		if paths, ok = localPaths(cfg); !ok {
			return
		}
		pullFromServer(cfg, client, st, serverFiles, paths, handled, false)
	}

	known := make(map[string]int64, len(tombstones))
	for name, tombstone := range tombstones {
		known[name] = tombstone.DeletedAt
	}
	st.pruneTombstones(known)

	if err := st.save(); err != nil {
		log.Printf("Error saving sync state: %v", err)
	}
}

func localPaths(cfg *Config) (map[string]string, bool) {
	paths, err := utils.ListSyncFiles(cfg.DirPath)
	if err != nil {
		log.Printf("Error reading directory: %v", err)
		return nil, false
	}
	return paths, true
}

func localPathFor(cfg *Config, paths map[string]string, name string) string {
	if actual, ok := paths[name]; ok {
		return utils.LocalPath(cfg.DirPath, actual)
	}
	return utils.LocalPath(cfg.DirPath, name)
}

func applyTombstones(cfg *Config, st *syncState, tombstones map[string]protocol.Tombstone, paths map[string]string) {
	names := make([]string, 0, len(tombstones))
	for name := range tombstones {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		tombstone := tombstones[name]
		if applied, ok := st.Tombstones[name]; ok && applied >= tombstone.DeletedAt {
			continue
		}

		if _, err := utils.NormalizeSyncPath(name); err != nil {
			log.Printf("Skipping invalid remote path: %s", name)
			continue
		}
		localPath := localPathFor(cfg, paths, name)

		contentBytes, err := os.ReadFile(localPath)
		if err != nil && !os.IsNotExist(err) {
			log.Printf("Error reading %s: %v", name, err)
			continue
		}

		if os.IsNotExist(err) {
			st.Tombstones[name] = tombstone.DeletedAt
			continue
		}

		base, tracked := st.Base[name]
		content := string(contentBytes)
		clean := tracked && content == base

		if tombstone.RenamedTo == "" {
			if clean {
				removeLocalFile(cfg, localPath)
				log.Printf("Removed local file deleted on server: %s", name)
			} else {
				log.Printf("Keeping %s: deleted on server but changed locally", name)
			}
			delete(st.Base, name)
			st.Tombstones[name] = tombstone.DeletedAt
			continue
		}

		if _, err := utils.NormalizeSyncPath(tombstone.RenamedTo); err != nil {
			log.Printf("Skipping invalid remote path: %s", tombstone.RenamedTo)
			st.Tombstones[name] = tombstone.DeletedAt
			continue
		}
		targetPath := localPathFor(cfg, paths, tombstone.RenamedTo)

		_, targetErr := os.Stat(targetPath)
		switch {
		case os.IsNotExist(targetErr) && tracked:
			if err := utils.WriteSyncFile(targetPath, content); err != nil {
				log.Printf("Error moving %s to %s: %v", name, tombstone.RenamedTo, err)
				continue
			}
			removeLocalFile(cfg, localPath)
			st.Base[tombstone.RenamedTo] = base
			delete(st.Base, name)
			log.Printf("Moved local file %s to %s", name, tombstone.RenamedTo)
		case targetErr == nil && clean:
			removeLocalFile(cfg, localPath)
			delete(st.Base, name)
			log.Printf("Removed local file %s already present as %s", name, tombstone.RenamedTo)
		default:
			log.Printf("Keeping %s: moved to %s on server but changed locally", name, tombstone.RenamedTo)
			delete(st.Base, name)
		}
		st.Tombstones[name] = tombstone.DeletedAt
	}
}

func pushLocalChanges(cfg *Config, client *http.Client, st *syncState, serverFiles, paths map[string]string) map[string]bool {
	handled := make(map[string]bool)

	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)

	local := make(map[string]string, len(names))
	for _, name := range names {
		contentBytes, err := os.ReadFile(localPathFor(cfg, paths, name))
		if err != nil {
			log.Printf("Error reading %s: %v", name, err)
			continue
		}
		local[name] = string(contentBytes)
	}

	removed := make([]string, 0)
	for name := range st.Base {
		if _, ok := local[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)

	added := make([]string, 0)
	for name := range local {
		if _, ok := st.Base[name]; !ok {
			added = append(added, name)
		}
	}
	sort.Strings(added)

	claimed := make(map[string]bool)
	for _, from := range removed {
		base := st.Base[from]
		if _, err := os.Stat(localPathFor(cfg, paths, from)); err == nil {
			continue
		}

		to := ""
		for _, candidate := range added {
			if !claimed[candidate] && local[candidate] == base {
				to = candidate
				break
			}
		}

		if to != "" {
			synced, err := renameRemoteFile(cfg, client, from, to, base)
			if err != nil {
				log.Printf("Failed to move %s to %s: %v", from, to, err)
				handled[from] = true
				continue
			}

			claimed[to] = true
			delete(st.Base, from)
			st.Base[to] = synced
			handled[from] = true
			handled[to] = true

			if synced != local[to] {
				if err := utils.WriteSyncFile(localPathFor(cfg, paths, to), synced); err != nil {
					log.Printf("Error writing moved file %s: %v", to, err)
				}
				local[to] = synced
			}
			log.Printf("Moved %s to %s on server", from, to)
			continue
		}

		err := deleteRemoteFile(cfg, client, from, base)
		switch {
		case err == nil:
			delete(st.Base, from)
			handled[from] = true
			log.Printf("Deleted %s on server", from)
		case errors.Is(err, errDeleteConflict):
			delete(st.Base, from)
			log.Printf("Kept %s on server: it changed after the local deletion", from)
		default:
			handled[from] = true
			log.Printf("Failed to delete %s: %v", from, err)
		}
	}

	for _, name := range names {
		content, ok := local[name]
		if !ok || handled[name] {
			continue
		}

		base, tracked := st.Base[name]
		if _, onServer := serverFiles[name]; tracked && !onServer {
			log.Printf("Restoring %s: the server no longer has it", name)
			base = ""
			tracked = false
		}

		if tracked && content == base {
			continue
		}

		if !tracked {
			if hash, ok := serverFiles[name]; ok && hash == utils.CalculateHash(content) {
				st.Base[name] = content
				handled[name] = true
				log.Printf("Adopted identical server copy: %s", name)
				continue
			}
		} else {
			log.Printf("File changed: %s", name)
		}

		if uploadAndStore(cfg, client, st, paths, name, base, content) {
			handled[name] = true
		}
	}

	return handled
}

func uploadAndStore(cfg *Config, client *http.Client, st *syncState, paths map[string]string, filename, base, current string) bool {
	synced, err := uploadFile(cfg, client, filename, base, current)
	if errors.Is(err, errUploadConflict) {
		log.Printf("Postponing upload of %s: it was moved or deleted on the server", filename)
		return true
	}
	if err != nil {
		log.Printf("Upload failed for %s: %v", filename, err)
		return false
	}

	if synced != current {
		if err := utils.WriteSyncFile(localPathFor(cfg, paths, filename), synced); err != nil {
			log.Printf("Error writing merged file %s: %v", filename, err)
			return false
		}
		log.Printf("File %s updated with merged content.", filename)
	} else {
		log.Printf("Upload for %s complete (no merge conflicts).", filename)
	}

	st.Base[filename] = synced
	return true
}

func pullFromServer(cfg *Config, client *http.Client, st *syncState, serverFiles, paths map[string]string, handled map[string]bool, force bool) {
	names := make([]string, 0, len(serverFiles))
	for name := range serverFiles {
		names = append(names, name)
	}
	sort.Strings(names)

	folded := make(map[string]string, len(names))

	for _, filename := range names {
		if _, err := utils.NormalizeSyncPath(filename); err != nil {
			log.Printf("Skipping invalid remote path: %s", filename)
			continue
		}

		if cfg.caseInsensitive {
			fold := strings.ToLower(filename)
			if other, ok := folded[fold]; ok {
				log.Printf("Skipping %s: it differs from %s only in letter case, which this device cannot store separately", filename, other)
				continue
			}
			folded[fold] = filename
		}

		if handled[filename] {
			continue
		}

		localPath := localPathFor(cfg, paths, filename)

		base, tracked := st.Base[filename]
		if !force && tracked && utils.CalculateHash(base) == serverFiles[filename] {
			continue
		}

		content, err := downloadFile(cfg, client, filename)
		if err != nil {
			log.Printf("Failed to download %s: %v", filename, err)
			continue
		}

		if force {
			if err := utils.WriteSyncFile(localPath, content); err != nil {
				log.Printf("Error writing forced file %s: %v", filename, err)
				continue
			}
			st.Base[filename] = content
			log.Printf("Force downloaded file: %s", filename)
			continue
		}

		currentBytes, err := os.ReadFile(localPath)
		switch {
		case os.IsNotExist(err):
			if err := utils.WriteSyncFile(localPath, content); err != nil {
				log.Printf("Error writing new file %s: %v", filename, err)
				continue
			}
			st.Base[filename] = content
			log.Printf("Downloaded new file: %s", filename)
		case err != nil:
			log.Printf("Error reading %s: %v", filename, err)
		case string(currentBytes) == content:
			st.Base[filename] = content
		case tracked && string(currentBytes) == base:
			if err := utils.WriteSyncFile(localPath, content); err != nil {
				log.Printf("Error writing file %s: %v", filename, err)
				continue
			}
			st.Base[filename] = content
			log.Printf("Updated file from server: %s", filename)
		default:
			log.Printf("Skipping download for %s (local changes detected). Will attempt merge via upload.", filename)
		}
	}
}

func removeLocalFile(cfg *Config, localPath string) {
	if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
		log.Printf("Error removing %s: %v", localPath, err)
		return
	}
	utils.PruneEmptyDirs(cfg.DirPath, localPath)
}
