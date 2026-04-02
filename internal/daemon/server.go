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
	"github.com/mschulkind-oss/tillr/internal/server"
)

// ProjectHandle holds an open database and config for a single project.
type ProjectHandle struct {
	Slug   string
	Path   string
	DB     *sql.DB
	Config *config.Config
	Name   string // project name from DB
}

// Registry holds all loaded projects.
type Registry struct {
	mu       sync.RWMutex
	projects map[string]*ProjectHandle // slug → handle
	order    []string                  // insertion order for listing
}

// NewRegistry creates a registry from a daemon config, opening all project DBs.
func NewRegistry(cfg *DaemonConfig) (*Registry, error) {
	reg := &Registry{
		projects: make(map[string]*ProjectHandle),
	}

	for _, entry := range cfg.Projects {
		handle, err := openProject(entry)
		if err != nil {
			// Close already-opened DBs on error
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
	// Verify project dir exists and has a config
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

	// Get project name from DB
	project, err := db.GetProject(database)
	if err != nil {
		database.Close() //nolint:errcheck
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
		h.DB.Close() //nolint:errcheck
	}
}

// ProjectInfo is the JSON response for the project list endpoint.
type ProjectInfo struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// StartDaemon starts the multi-project HTTP server.
func StartDaemon(cfg *DaemonConfig) error {
	// Signal handling
	signal.Ignore(syscall.SIGPIPE, syscall.SIGHUP, syscall.SIGURG,
		syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGWINCH,
		syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU)
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

	// /api/projects — list all projects
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		projects := make([]ProjectInfo, 0)
		for _, h := range reg.List() {
			projects = append(projects, ProjectInfo{
				Slug: h.Slug,
				Name: h.Name,
				Path: h.Path,
			})
		}
		json.NewEncoder(w).Encode(projects) //nolint:errcheck
	})

	// /api/health — daemon health check
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
	})

	// /api/p/{slug}/... — proxy to single-project server logic
	// We create a per-project sub-mux using the existing server.BuildMux().
	projectMuxes := make(map[string]http.Handler)
	for _, h := range reg.List() {
		projectMux := server.BuildMux(h.DB, server.ServerConfig{
			Port:       cfg.Port,
			DBPath:     h.Config.DBPath,
			VantageURL: h.Config.VantageURL,
		})
		projectMuxes[h.Slug] = projectMux
	}

	mux.HandleFunc("/api/p/", func(w http.ResponseWriter, r *http.Request) {
		// Parse /api/p/{slug}/... → strip prefix to get /api/...
		rest := strings.TrimPrefix(r.URL.Path, "/api/p/")
		slashIdx := strings.Index(rest, "/")
		if slashIdx < 0 {
			http.Error(w, `{"error":"missing API path after project slug"}`, http.StatusBadRequest)
			return
		}
		slug := rest[:slashIdx]
		apiPath := rest[slashIdx:] // e.g., "/status"

		projectMux, ok := projectMuxes[slug]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "project not found: " + slug}) //nolint:errcheck
			return
		}

		// Rewrite URL to /api/... so the existing handlers match
		r.URL.Path = "/api" + apiPath
		if r.URL.RawPath != "" {
			r.URL.RawPath = "/api" + apiPath
		}
		projectMux.ServeHTTP(w, r)
	})

	// Serve the SPA frontend — same embedded assets as single-project mode
	if err := server.ServeSPAFromEmbedded(mux); err != nil {
		return fmt.Errorf("loading frontend assets: %w", err)
	}

	// Wrap with logging
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s", r.Method, r.URL.RequestURI())
		}
		mux.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Tillr daemon starting on http://localhost:%d", cfg.Port)
	log.Printf("Serving %d projects", len(reg.List()))
	return http.ListenAndServe(addr, handler)
}
