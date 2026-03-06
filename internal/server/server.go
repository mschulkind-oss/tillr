package server

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/models"
)

//go:embed all:assets
var embeddedAssets embed.FS

// Start launches the HTTP server.
func Start(database *sql.DB, port int) error {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/status", apiHandler(database, handleStatus))
	mux.HandleFunc("/api/features", apiHandler(database, handleFeatures))
	mux.HandleFunc("/api/milestones", apiHandler(database, handleMilestones))
	mux.HandleFunc("/api/roadmap", apiHandler(database, handleRoadmap))
	mux.HandleFunc("/api/roadmap/", apiHandler(database, handleRoadmapStatus))
	mux.HandleFunc("/api/cycles", apiHandler(database, handleCycles))
	mux.HandleFunc("/api/history", apiHandler(database, handleHistory))
	mux.HandleFunc("/api/search", apiHandler(database, handleSearch))
	mux.HandleFunc("/api/qa/", apiHandler(database, handleQA))

	// WebSocket placeholder
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprintf(w, "WebSocket endpoint - connect with a WebSocket client")
	})

	// Static assets
	assetsFS, err := fs.Sub(embeddedAssets, "assets")
	if err != nil {
		return fmt.Errorf("loading embedded assets: %w", err)
	}
	fileServer := http.FileServer(http.FS(assetsFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// SPA: serve index.html for non-file paths
		path := r.URL.Path
		if path == "/" || (!strings.Contains(path, ".") && !strings.HasPrefix(path, "/api/") && path != "/ws") {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf(":%d", port)
	return http.ListenAndServe(addr, mux)
}

type apiFunc func(*sql.DB, http.ResponseWriter, *http.Request) error

func apiHandler(database *sql.DB, fn apiFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			return
		}
		if err := fn(database, w, r); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func handleStatus(database *sql.DB, w http.ResponseWriter, _ *http.Request) error {
	overview, err := engine.GetStatusOverview(database)
	if err != nil {
		return err
	}
	return writeJSON(w, overview)
}

func handleFeatures(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	// Check if it's a feature detail request: /api/features/{id}
	path := r.URL.Path
	if id := strings.TrimPrefix(path, "/api/features/"); id != "" && id != path {
		f, err := db.GetFeature(database, id)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return writeJSON(w, map[string]string{"error": "feature not found"})
		}
		return writeJSON(w, f)
	}

	p, err := db.GetProject(database)
	if err != nil {
		return err
	}
	status := r.URL.Query().Get("status")
	milestone := r.URL.Query().Get("milestone")
	features, err := db.ListFeatures(database, p.ID, status, milestone)
	if err != nil {
		return err
	}
	if features == nil {
		features = []models.Feature{}
	}
	return writeJSON(w, features)
}

func handleMilestones(database *sql.DB, w http.ResponseWriter, _ *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}
	milestones, err := db.ListMilestones(database, p.ID)
	if err != nil {
		return err
	}
	if milestones == nil {
		milestones = []models.Milestone{}
	}
	return writeJSON(w, milestones)
}

func handleRoadmap(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}

	category := r.URL.Query().Get("category")
	priority := r.URL.Query().Get("priority")
	status := r.URL.Query().Get("status")
	sort := r.URL.Query().Get("sort")

	items, err := db.ListRoadmapItemsFiltered(database, p.ID, category, priority, status, sort)
	if err != nil {
		return err
	}
	if items == nil {
		items = []models.RoadmapItem{}
	}
	return writeJSON(w, items)
}

func handleCycles(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Path
	// /api/cycles/{id}/history
	if strings.HasSuffix(path, "/history") {
		featureID := strings.TrimPrefix(path, "/api/cycles/")
		featureID = strings.TrimSuffix(featureID, "/history")
		cycles, err := db.ListCycleHistory(database, featureID)
		if err != nil {
			return err
		}
		return writeJSON(w, cycles)
	}

	cycles, err := db.ListActiveCycles(database)
	if err != nil {
		return err
	}
	if cycles == nil {
		cycles = []models.CycleInstance{}
	}
	return writeJSON(w, cycles)
}

func handleHistory(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}
	events, err := db.ListEvents(database, p.ID, "", "", "", 100)
	if err != nil {
		return err
	}
	if events == nil {
		events = []models.Event{}
	}
	return writeJSON(w, events)
}

func handleSearch(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		return writeJSON(w, []models.Event{})
	}
	events, err := db.SearchEvents(database, p.ID, q)
	if err != nil {
		return err
	}
	return writeJSON(w, events)
}

func handleQA(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return writeJSON(w, map[string]string{"error": "POST required"})
	}

	path := r.URL.Path
	parts := strings.Split(strings.TrimPrefix(path, "/api/qa/"), "/")
	if len(parts) != 2 {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "invalid path"})
	}

	featureID := parts[0]
	action := parts[1]

	p, err := db.GetProject(database)
	if err != nil {
		return err
	}

	var body struct {
		Notes string `json:"notes"`
	}
	json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck

	switch action {
	case "approve":
		if err := engine.ApproveFeatureQA(database, p.ID, featureID, body.Notes); err != nil {
			return err
		}
		return writeJSON(w, map[string]string{"status": "approved"})
	case "reject":
		if err := engine.RejectFeatureQA(database, p.ID, featureID, body.Notes); err != nil {
			return err
		}
		return writeJSON(w, map[string]string{"status": "rejected"})
	default:
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "unknown action: " + action})
	}
}

var validRoadmapStatuses = map[string]bool{
	"proposed":    true,
	"accepted":    true,
	"in-progress": true,
	"completed":   true,
	"deferred":    true,
}

func handleRoadmapStatus(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Methods", "PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		return nil
	}
	if r.Method != "PATCH" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return writeJSON(w, map[string]string{"error": "PATCH required"})
	}

	// Parse /api/roadmap/{id}/status
	path := strings.TrimPrefix(r.URL.Path, "/api/roadmap/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "status" || parts[0] == "" {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "invalid path, expected /api/roadmap/{id}/status"})
	}
	id := parts[0]

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "invalid request body"})
	}

	if !validRoadmapStatuses[body.Status] {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "invalid status: " + body.Status})
	}

	if err := db.UpdateRoadmapItemStatus(database, id, body.Status); err != nil {
		if err == sql.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			return writeJSON(w, map[string]string{"error": "roadmap item not found"})
		}
		return fmt.Errorf("updating roadmap item status: %w", err)
	}

	return writeJSON(w, map[string]bool{"ok": true})
}
