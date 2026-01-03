package client

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"hsync/internal/protocol"
	"hsync/internal/utils"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config holds the client configuration
type Config struct {
	ServerURL string        `toml:"server"`
	Key       string        `toml:"key"`
	DirPath   string        `toml:"dir"`
	Interval  time.Duration `toml:"-"`
}

type configTOML struct {
	ServerURL string `toml:"server"`
	Key       string `toml:"key"`
	DirPath   string `toml:"dir"`
	Interval  string `toml:"interval"`
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

var (
	baseContents = make(map[string]string)
)

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

	var tomlCfg configTOML
	if err := toml.Unmarshal(data, &tomlCfg); err != nil {
		log.Fatalf("Error parsing config file: %v", err)
	}

	var cfg Config
	cfg.ServerURL = tomlCfg.ServerURL
	cfg.Key = tomlCfg.Key
	cfg.DirPath = tomlCfg.DirPath

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

	if tomlCfg.Interval != "" {
		parsedDuration, err := time.ParseDuration(tomlCfg.Interval)
		if err != nil {
			log.Fatalf("Invalid interval format: %v", err)
		}
		cfg.Interval = parsedDuration
	} else {
		cfg.Interval = 5 * time.Second
	}

	// Ensure local dir exists
	if err := os.MkdirAll(cfg.DirPath, 0755); err != nil {
		log.Fatal(err)
	}

	log.Printf("Starting client syncing to %s with dir %s", cfg.ServerURL, cfg.DirPath)

	// 3-1. Initial Sync
	syncWithServer(&cfg)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for range ticker.C {
		// Periodically check server for updates
		syncWithServer(&cfg)
		// Check local changes
		checkAndUpload(&cfg)
	}
}

func syncWithServer(cfg *Config) {
	// 1. Get List of Hashes
	req, err := http.NewRequest("GET", cfg.ServerURL+"/sync", nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return
	}
	req.Header.Set("X-Sync-Key", cfg.Key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Failed to list files: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Server returned status: %d", resp.StatusCode)
		return
	}

	var serverFiles map[string]string // filename -> hash
	if err := json.NewDecoder(resp.Body).Decode(&serverFiles); err != nil {
		log.Printf("Error decoding file list: %v", err)
		return
	}

	// 2. Compare and Download if needed
	for filename, serverHash := range serverFiles {
		localBaseContent, exists := baseContents[filename]

		// If we don't have it, or our base is outdated
		if !exists || utils.CalculateHash(localBaseContent) != serverHash {
			// Let's implement: Download content.
			content, err := downloadFile(cfg, filename)
			if err != nil {
				log.Printf("Failed to download %s: %v", filename, err)
				continue
			}

			// Update base
			baseContents[filename] = content

			// Update local file IF it was clean (same as old base)
			localPath := filepath.Join(cfg.DirPath, filename)
			currentBytes, err := os.ReadFile(localPath)
			if os.IsNotExist(err) {
				// File doesn't exist locally, just write it
				os.WriteFile(localPath, []byte(content), 0644)
				log.Printf("Downloaded new file: %s", filename)
			} else if err == nil {
				if exists && string(currentBytes) == localBaseContent {
					// Local was clean, safe to update
					os.WriteFile(localPath, []byte(content), 0644)
					log.Printf("Updated file from server: %s", filename)
				} else {
					log.Printf("Skipping download for %s (local changes detected). Will attempt merge via upload.", filename)
				}
			}
		}
	}
}

func downloadFile(cfg *Config, filename string) (string, error) {
	req, err := http.NewRequest("GET", cfg.ServerURL+"/sync?filename="+filename, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Sync-Key", cfg.Key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func checkAndUpload(cfg *Config) {
	entries, err := os.ReadDir(cfg.DirPath)
	if err != nil {
		log.Printf("Error reading directory: %v", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		filename := entry.Name()
		localPath := filepath.Join(cfg.DirPath, filename)
		contentBytes, err := os.ReadFile(localPath)
		if err != nil {
			log.Printf("Error reading %s: %v", filename, err)
			continue
		}
		currentContent := string(contentBytes)

		base, exists := baseContents[filename]
		if !exists {
			// New file detected
			base = ""
		}

		if currentContent == base {
			continue // No change
		}

		log.Printf("File changed: %s", filename)
		syncFile(cfg, filename, base, currentContent)
	}
}

func syncFile(cfg *Config, filename, base, current string) {
	reqBody := protocol.SyncRequest{
		Filename: filename,
		Base:     base,
		Latest:   current,
	}
	jsonBody, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", cfg.ServerURL+"/sync", bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return
	}
	req.Header.Set("X-Sync-Key", cfg.Key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Upload failed for %s: %v", filename, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Upload failed for %s (status %d): %s", filename, resp.StatusCode, string(body))
		return
	}

	var syncResp protocol.SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		log.Printf("Error decoding response for %s: %v", filename, err)
		return
	}

	// Update local file and base
	if syncResp.Synced != current {
		localPath := filepath.Join(cfg.DirPath, filename)
		if err := os.WriteFile(localPath, []byte(syncResp.Synced), 0644); err != nil {
			log.Printf("Error writing merged file %s: %v", filename, err)
			return
		}
		log.Printf("File %s updated with merged content.", filename)
	} else {
		log.Printf("Upload for %s complete (no merge conflicts).", filename)
	}

	baseContents[filename] = syncResp.Synced
}

