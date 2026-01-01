package main

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
)

var (
	serverURL = flag.String("server", "http://localhost:8080", "Server URL")
	key       = flag.String("key", "default-secret", "Shared key for authentication")
	dirPath   = flag.String("dir", getDefaultDir(), "Path to the local sync directory")
	interval  = flag.Duration("interval", 5*time.Second, "Sync interval")
)

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

func main() {
	flag.Parse()

	// Ensure local dir exists
	if err := os.MkdirAll(*dirPath, 0755); err != nil {
		log.Fatal(err)
	}

	// 3-1. Initial Sync
	syncWithServer()

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	for range ticker.C {
		// Periodically check server for updates
		syncWithServer()
		// Check local changes
		checkAndUpload()
	}
}

func syncWithServer() {
	// 1. Get List of Hashes
	req, err := http.NewRequest("GET", *serverURL+"/sync", nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return
	}
	req.Header.Set("X-Sync-Key", *key)

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
			// Check if we have local changes that would be overwritten?
			// For simplicity:
			// If local file exists and is different from base -> We have local changes.
			// If we also have server changes -> CONFLICT.
			// Current strategy: If server changed, we fetch server version.
			// If we had local changes, we might lose them or need to merge?
			// The safest quick fix: Trigger an upload with OLD base. Server merges.
			
			// Let's implement: Download content.
			content, err := downloadFile(filename)
			if err != nil {
				log.Printf("Failed to download %s: %v", filename, err)
				continue
			}

			// Update base
			baseContents[filename] = content

			// Update local file IF it was clean (same as old base)
			localPath := filepath.Join(*dirPath, filename)
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
					// Local was dirty. We updated Base.
					// Next checkAndUpload() will see local != newBase (very likely).
					// But wait, if we update Base to ServerContent, and Local is Dirty (based on OldBase),
					// diff(NewBase, Local) might be huge or wrong context.
					// Ideally: Leave Base as is if dirty?
					// If we leave Base as OldBase, checkAndUpload sends (OldBase, Local). Server merges (OldBase, Local, ServerContent).
					// This is CORRECT for 3-way merge.
					
					// SO: Only update Base if we update Local.
					// If Local is dirty, DO NOT update Base yet. Let Upload happen.
					// But how do we know if we need to sync?
					
					// Re-eval:
					// If Local is clean (== OldBase): Update Local & Base to ServerContent.
					// If Local is dirty (!= OldBase): Ignore Server Update. Trigger Upload.
					// The Upload will use (OldBase, Local). Server has ServerContent.
					// Merge(OldBase, Local, ServerContent) -> NewContent.
					// Server saves NewContent. Returns NewContent.
					// Client updates Local & Base to NewContent.
					// Perfect.
					
					log.Printf("Skipping download for %s (local changes detected). Will attempt merge via upload.", filename)
				}
			}
		}
	}
}

func downloadFile(filename string) (string, error) {
	req, err := http.NewRequest("GET", *serverURL+"/sync?filename="+filename, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Sync-Key", *key)

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

func checkAndUpload() {
	entries, err := os.ReadDir(*dirPath)
	if err != nil {
		log.Printf("Error reading directory: %v", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		filename := entry.Name()
		localPath := filepath.Join(*dirPath, filename)
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
		syncFile(filename, base, currentContent)
	}
}

func syncFile(filename, base, current string) {
	reqBody := protocol.SyncRequest{
		Filename: filename,
		Base:     base,
		Latest:   current,
	}
	jsonBody, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", *serverURL+"/sync", bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return
	}
	req.Header.Set("X-Sync-Key", *key)
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
		localPath := filepath.Join(*dirPath, filename)
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