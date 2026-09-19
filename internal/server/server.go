package server

import (
	"encoding/json"
	"errors"
	"flag"
	"hsync/internal/protocol"
	"hsync/internal/repo"
	"hsync/internal/utils"
	"log"
	"net/http"
	"os"
	"sync"
)

const maxPushBytes = 256 << 20

type server struct {
	key  string
	repo *repo.Repo
	mu   sync.Mutex
}

func NewHandler(dataDir, key string) (http.Handler, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	r, err := repo.Open(dataDir)
	if err != nil {
		return nil, err
	}
	log.Printf("History head is %s with %d notes", short(r.Head().ID), len(r.Head().Files))

	s := &server{key: key, repo: r}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /index", s.handle(s.index))
	mux.HandleFunc("POST /push", s.handle(s.push))
	mux.HandleFunc("GET /blob", s.handle(s.blob))
	return mux, nil
}

func Run(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "Address to listen on")
	key := fs.String("key", "default-secret", "Shared key for authentication")
	dir := fs.String("dir", "data", "Path to the server-side data directory")

	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	handler, err := NewHandler(*dir, *key)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Server listening on %s with data directory %s", *addr, *dir)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
	}
}

func (s *server) handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Sync-Key") != s.key {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		s.mu.Lock()
		defer s.mu.Unlock()

		if err := fn(w, r); err != nil {
			code := status(err)
			log.Printf("%s %s: %d %v", r.Method, r.URL.Path, code, err)
			http.Error(w, http.StatusText(code), code)
		}
	}
}

func status(err error) int {
	switch {
	case errors.Is(err, repo.ErrUnknownParent):
		return http.StatusConflict
	case errors.Is(err, repo.ErrMissingBlob):
		return http.StatusUnprocessableEntity
	case errors.Is(err, utils.ErrInvalidPath), errors.Is(err, repo.ErrCorruptBlob), errors.Is(err, errBadRequest):
		return http.StatusBadRequest
	case errors.Is(err, errNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

var (
	errBadRequest = errors.New("bad request")
	errNotFound   = errors.New("not found")
)

func (s *server) index(w http.ResponseWriter, r *http.Request) error {
	if err := s.repo.Refresh(); err != nil {
		return err
	}

	head := s.repo.Head()
	return write(w, protocol.IndexResponse{Commit: head.ID, Root: s.repo.Root(), Files: head.Files})
}

func (s *server) blob(w http.ResponseWriter, r *http.Request) error {
	content, ok, err := s.repo.Blob(r.URL.Query().Get("hash"))
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err = w.Write([]byte(content))
	return err
}

func (s *server) push(w http.ResponseWriter, r *http.Request) error {
	var req protocol.PushRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushBytes)).Decode(&req); err != nil {
		return errBadRequest
	}
	if req.Parent == "" {
		return errBadRequest
	}

	if err := s.repo.Refresh(); err != nil {
		return err
	}

	commit, err := s.repo.Push(req.Parent, req.Files, req.Blobs)
	if err != nil {
		return err
	}
	if commit.ID != req.Parent {
		log.Printf("Committed %s with %d notes on top of %s", short(commit.ID), len(commit.Files), short(req.Parent))
	}
	return write(w, protocol.PushResponse{Commit: commit.ID, Files: commit.Files})
}

func write(w http.ResponseWriter, body any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(body)
}

func short(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
