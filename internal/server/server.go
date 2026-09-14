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
	"sync"
)

type Server struct {
	Addr       string
	Key        string
	DataDir    string
	tombstones *tombstoneStore
	mu         sync.Mutex
}

func NewHandler(dataDir, key string) (http.Handler, error) {
	s := &Server{Key: key, DataDir: dataDir}

	if err := os.MkdirAll(s.DataDir, 0755); err != nil {
		return nil, err
	}

	s.tombstones = newTombstoneStore(s.DataDir)
	if err := s.tombstones.load(); err != nil {
		return nil, err
	}

	if err := canonicalizeNames(s.DataDir); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/sync", s.handleSync)
	mux.HandleFunc("/deleted", s.handleDeleted)
	mux.HandleFunc("/rename", s.handleRename)
	return mux, nil
}

func canonicalizeNames(dataDir string) error {
	names, err := utils.ListTextFiles(dataDir)
	if err != nil {
		return err
	}

	for _, name := range names {
		canonical := utils.CanonicalSyncName(name)
		if canonical == name {
			continue
		}

		canonicalPath := utils.LocalPath(dataDir, canonical)
		if _, err := os.Stat(canonicalPath); err == nil {
			log.Printf("Keeping %s: %s already exists", name, canonical)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(canonicalPath), 0755); err != nil {
			return err
		}
		if err := os.Rename(utils.LocalPath(dataDir, name), canonicalPath); err != nil {
			return err
		}
		log.Printf("Normalized file name %s -> %s", name, canonical)
	}
	return nil
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

	handler, err := NewHandler(s.DataDir, s.Key)
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{
		Addr:    s.Addr,
		Handler: handler,
	}

	log.Printf("Server listening on %s", s.Addr)
	log.Printf("Data directory: %s", s.DataDir)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func (s *Server) authorized(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Sync-Key") != s.Key {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
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
		entries, err := utils.ListSyncFiles(s.DataDir)
		if err != nil {
			log.Printf("ListSyncFiles error: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		files := make(map[string]string, len(entries))
		for name, actual := range entries {
			content, err := os.ReadFile(utils.LocalPath(s.DataDir, actual))
			if err != nil {
				log.Printf("ReadFile error (%s): %v", actual, err)
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
		serverContentBytes, readErr := os.ReadFile(serverPath)
		serverContent := ""
		if readErr == nil {
			serverContent = string(serverContentBytes)
		} else if !os.IsNotExist(readErr) {
			log.Printf("ReadFile error: %v", readErr)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if os.IsNotExist(readErr) && req.Base != "" && s.tombstones.has(filename) {
			http.Error(w, "Conflict", http.StatusConflict)
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

		s.tombstones.clear(filename)
		if err := s.tombstones.save(); err != nil {
			log.Printf("Tombstone save error: %v", err)
		}

		resp := protocol.SyncResponse{
			Synced: merged,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	if r.Method == http.MethodDelete {
		filename, err := utils.NormalizeSyncPath(r.URL.Query().Get("filename"))
		if err != nil {
			http.Error(w, "Invalid Filename", http.StatusBadRequest)
			return
		}

		serverPath := utils.LocalPath(s.DataDir, filename)
		content, err := os.ReadFile(serverPath)
		if err != nil && !os.IsNotExist(err) {
			log.Printf("ReadFile error: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if err == nil && utils.CalculateHash(string(content)) != r.URL.Query().Get("base") {
			http.Error(w, "Conflict", http.StatusConflict)
			return
		}

		if err == nil {
			if err := os.Remove(serverPath); err != nil {
				log.Printf("Remove error: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			utils.PruneEmptyDirs(s.DataDir, serverPath)
		}

		s.tombstones.add(filename, "")
		if err := s.tombstones.save(); err != nil {
			log.Printf("Tombstone save error: %v", err)
		}

		log.Printf("Deleted %s", filename)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleDeleted(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.tombstones.all())
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req protocol.RenameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	from, err := utils.NormalizeSyncPath(req.From)
	if err != nil {
		http.Error(w, "Invalid Filename", http.StatusBadRequest)
		return
	}
	to, err := utils.NormalizeSyncPath(req.To)
	if err != nil {
		http.Error(w, "Invalid Filename", http.StatusBadRequest)
		return
	}
	if from == to {
		http.Error(w, "Invalid Filename", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	source := from
	fromPath := utils.LocalPath(s.DataDir, source)
	toPath := utils.LocalPath(s.DataDir, to)

	fromContent, fromErr := os.ReadFile(fromPath)
	if fromErr != nil && !os.IsNotExist(fromErr) {
		log.Printf("ReadFile error: %v", fromErr)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if os.IsNotExist(fromErr) {
		if resolved := s.tombstones.resolve(from); resolved != from && resolved != to {
			resolvedPath := utils.LocalPath(s.DataDir, resolved)
			if content, err := os.ReadFile(resolvedPath); err == nil {
				source = resolved
				fromPath = resolvedPath
				fromContent = content
				fromErr = nil
				log.Printf("Following rename %s -> %s", from, resolved)
			}
		}
	}

	toContent, toErr := os.ReadFile(toPath)
	if toErr != nil && !os.IsNotExist(toErr) {
		log.Printf("ReadFile error: %v", toErr)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	synced := ""
	switch {
	case toErr == nil:
		synced = string(toContent)
	case fromErr == nil:
		synced = string(fromContent)
		if err := utils.WriteSyncFile(toPath, synced); err != nil {
			log.Printf("Write error: %v", err)
			http.Error(w, "Write Error", http.StatusInternalServerError)
			return
		}
	default:
		synced = req.Base
		if err := utils.WriteSyncFile(toPath, synced); err != nil {
			log.Printf("Write error: %v", err)
			http.Error(w, "Write Error", http.StatusInternalServerError)
			return
		}
	}

	if fromErr == nil {
		if err := os.Remove(fromPath); err != nil {
			log.Printf("Remove error: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		utils.PruneEmptyDirs(s.DataDir, fromPath)
	}

	s.tombstones.add(from, to)
	if source != from {
		s.tombstones.add(source, to)
	}
	s.tombstones.clear(to)
	if err := s.tombstones.save(); err != nil {
		log.Printf("Tombstone save error: %v", err)
	}

	log.Printf("Renamed %s -> %s", source, to)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(protocol.SyncResponse{Synced: synced})
}
