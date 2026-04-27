// Package daemon serves multiple tillr projects from a single HTTP
// server (multi-project mode). Each project's database lives at its
// own .tillr.json root; the daemon proxies API requests by slug.
//
// Post-reset note: the daemon currently exposes only project listing
// + a per-project pass-through to the basic project server. Routes
// that the consulting-firm roadmap will add (comments, cycles,
// philosophies, etc.) plug in via the per-project handler.
package daemon

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/mschulkind-oss/tillr/internal/config"
	"github.com/mschulkind-oss/tillr/internal/db"
)

// ProjectHandle holds an open database and config for a single project.
type ProjectHandle struct {
	Slug   string
	Path   string
	DB     *sql.DB
	Config *config.Config
	Name   string
}

// Registry holds all loaded projects.
type Registry struct {
	mu       sync.RWMutex
	projects map[string]*ProjectHandle
	order    []string
}

// NewRegistry opens every project in the config and returns the registry.
func NewRegistry(cfg *DaemonConfig) (*Registry, error) {
	reg := &Registry{projects: make(map[string]*ProjectHandle)}
	for _, entry := range cfg.Projects {
		handle, err := openProject(entry)
		if err != nil {
			reg.Close()
			return nil, fmt.Errorf("project %q (%s): %w", entry.Slug, entry.Path, err)
		}
		reg.projects[entry.Slug] = handle
		reg.order = append(reg.order, entry.Slug)
		log.Printf("Loaded project %q (%s) from %s", handle.Name, entry.Slug, entry.Path)
	}
	return reg, nil
}

func openProject(entry ProjectEntry) (*ProjectHandle, error) {
	configPath := filepath.Join(entry.Path, config.ConfigFileName)
	if _, err := os.Stat(configPath); err != nil {
		return nil, fmt.Errorf("no %s found in %s", config.ConfigFileName, entry.Path)
	}
	cfg, err := config.Load(entry.Path)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	project, err := db.GetProject(database)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("reading project: %w", err)
	}
	return &ProjectHandle{
		Slug:   entry.Slug,
		Path:   entry.Path,
		DB:     database,
		Config: cfg,
		Name:   project.Name,
	}, nil
}

// Get returns a project handle by slug.
func (r *Registry) Get(slug string) *ProjectHandle {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.projects[slug]
}

// List returns all project handles in insertion order.
func (r *Registry) List() []*ProjectHandle {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handles := make([]*ProjectHandle, 0, len(r.order))
	for _, slug := range r.order {
		handles = append(handles, r.projects[slug])
	}
	return handles
}

// Close closes all project databases.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, h := range r.projects {
		_ = h.DB.Close()
	}
}

// ProjectInfo is the JSON shape for the /api/projects endpoint.
type ProjectInfo struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// StartDaemon starts the multi-project HTTP server. Blocks until SIGINT/SIGTERM.
func StartDaemon(cfg *DaemonConfig) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("Received %v, shutting down", sig)
		os.Exit(0)
	}()

	reg, err := NewRegistry(cfg)
	if err != nil {
		return fmt.Errorf("loading projects: %w", err)
	}
	defer reg.Close()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		out := make([]ProjectInfo, 0)
		for _, h := range reg.List() {
			out = append(out, ProjectInfo{Slug: h.Slug, Name: h.Name, Path: h.Path})
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/api/p/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/p/")
		slashIdx := strings.Index(rest, "/")
		if slashIdx < 0 {
			http.Error(w, `{"error":"missing API path after project slug"}`, http.StatusBadRequest)
			return
		}
		slug := rest[:slashIdx]
		apiPath := rest[slashIdx:]

		handle := reg.Get(slug)
		if handle == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "project not found: " + slug})
			return
		}

		r2 := r.Clone(r.Context())
		r2.URL.Path = "/api" + apiPath
		if r2.URL.RawPath != "" {
			r2.URL.RawPath = "/api" + apiPath
		}
		projectHandler(handle.DB).ServeHTTP(w, r2)
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Tillr daemon starting on http://localhost:%d", cfg.Port)
	log.Printf("Serving %d projects", len(reg.List()))
	return http.ListenAndServe(addr, mux) //nolint:gosec // bound to localhost in dev
}

// projectHandler returns a small http.Handler for the per-project minimal API.
func projectHandler(database *sql.DB) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/project", func(w http.ResponseWriter, _ *http.Request) {
		project, err := db.GetProject(database)
		if err != nil {
			writeJSONErr(w, http.StatusNotFound, "no project found")
			return
		}
		writeJSON(w, http.StatusOK, project)
	})
	mux.HandleFunc("/api/features", func(w http.ResponseWriter, _ *http.Request) {
		project, err := db.GetProject(database)
		if err != nil {
			writeJSONErr(w, http.StatusNotFound, "no project found")
			return
		}
		features, err := db.ListFeatures(database, project.ID)
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, features)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
