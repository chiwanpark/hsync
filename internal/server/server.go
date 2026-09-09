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
			// Security check: reject paths escaping the data directory
			path, err := utils.ResolveSyncPath(s.DataDir, filename)
			if err != nil {
				http.Error(w, "Invalid Filename", http.StatusBadRequest)
				return
			}
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

		// Case 2: List files with hashes, including nested directories
		names, err := utils.ListTextFiles(s.DataDir)
		if err != nil {
			log.Printf("ListTextFiles error: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		files := make(map[string]string)
		for _, name := range names {
			content, err := os.ReadFile(utils.LocalPath(s.DataDir, name))
			if err != nil {
				log.Printf("ReadFile error (%s): %v", name, err)
				continue
			}
			files[name] = utils.CalculateHash(string(content))
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

		// Security check: reject paths escaping the data directory
		filename, err := utils.NormalizeSyncPath(req.Filename)
		if err != nil {
			http.Error(w, "Invalid Filename", http.StatusBadRequest)
			return
		}

		serverPath := utils.LocalPath(s.DataDir, filename)
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
		if err := utils.WriteSyncFile(serverPath, merged); err != nil {
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
