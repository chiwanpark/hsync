package server

import (
	"encoding/json"
	"flag"
	"hsync/internal/merger"
	"hsync/internal/protocol"
	"hsync/internal/utils"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Server struct {
	Addr    string
	Key     string
	DataDir string
	mu      sync.Mutex
}

func Run(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	s := &Server{}
	fs.StringVar(&s.Addr, "addr", ":8080", "Address to listen on")
	fs.StringVar(&s.Key, "key", "default-secret", "Shared key for authentication")
	fs.StringVar(&s.DataDir, "dir", "data", "Path to the server-side data directory")

	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	// Ensure data directory exists
	if err := os.MkdirAll(s.DataDir, 0755); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/sync", s.handleSync)

	srv := &http.Server{
		Addr:    s.Addr,
		Handler: mux,
	}

	log.Printf("Server listening on %s", s.Addr)
	log.Printf("Data directory: %s", s.DataDir)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	// Apply Auth
	if r.Header.Get("X-Sync-Key") != s.Key {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if r.Method == http.MethodGet {
		filename := r.URL.Query().Get("filename")

		// Case 1: Download specific file content
		if filename != "" {
			// Security check
			cleanName := filepath.Base(filename)
			if cleanName == "." || cleanName == "/" || !strings.HasSuffix(cleanName, ".txt") {
				http.Error(w, "Invalid Filename", http.StatusBadRequest)
				return
			}
			path := filepath.Join(s.DataDir, cleanName)
			content, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				http.Error(w, "Not Found", http.StatusNotFound)
				return
			} else if err != nil {
				log.Printf("ReadFile error: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write(content)
			return
		}

		// Case 2: List files with hashes
		files := make(map[string]string)
		entries, err := os.ReadDir(s.DataDir)
		if err != nil {
			log.Printf("ReadDir error: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".txt") {
				content, err := os.ReadFile(filepath.Join(s.DataDir, entry.Name()))
				if err != nil {
					log.Printf("ReadFile error (%s): %v", entry.Name(), err)
					continue
				}
				files[entry.Name()] = utils.CalculateHash(string(content))
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(files)
		return
	}

	if r.Method == http.MethodPost {
		var req protocol.SyncRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}

		// Security check: simple sanitize
		filename := filepath.Base(req.Filename)
		if filename == "." || filename == "/" {
			http.Error(w, "Invalid Filename", http.StatusBadRequest)
			return
		}
		// Enforce .txt extension for safety/simplicity per requirement context
		if !strings.HasSuffix(filename, ".txt") {
			http.Error(w, "Only .txt files allowed", http.StatusBadRequest)
			return
		}

		serverPath := filepath.Join(s.DataDir, filename)
		serverContentBytes, err := os.ReadFile(serverPath)
		serverContent := ""
		if err == nil {
			serverContent = string(serverContentBytes)
		} else if !os.IsNotExist(err) {
			log.Printf("ReadFile error: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Perform 3-way merge
		merged, err := merger.ThreeWayMerge(req.Base, req.Latest, serverContent)
		if err != nil {
			log.Printf("Merge error: %v", err)
			http.Error(w, "Merge Error", http.StatusInternalServerError)
			return
		}

		// Save merged content
		if err := os.WriteFile(serverPath, []byte(merged), 0644); err != nil {
			log.Printf("Write error: %v", err)
			http.Error(w, "Write Error", http.StatusInternalServerError)
			return
		}

		resp := protocol.SyncResponse{
			Synced: merged,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}
