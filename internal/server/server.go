// Package server is the post-reset minimal HTTP server.
//
// Surface:
//   - GET  /api/health             — liveness probe
//   - GET  /api/project            — current project
//   - GET  /api/features           — list features
//   - POST /api/features           — create a feature
//   - GET  /api/features/{id}      — show one feature
//   - GET  /api/features/{id}/comments — list comments
//   - POST /api/features/{id}/comments — add a comment
//   - GET  /ws                     — WebSocket (scaffolding; no
//     producers yet — Stage 1 will
//     push comment events)
//   - GET  /*                      — embedded SPA (web/dist)
package server

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
)

//go:embed all:assets
var embeddedAssets embed.FS

// Config holds runtime parameters for Start.
type Config struct {
	Port   int
	ApiKey string
}

// Start runs the HTTP server until the process receives SIGINT/SIGTERM.
func Start(database *sql.DB, cfg Config) error {
	hub := newHub()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", handleHealth)
	mux.HandleFunc("/api/project", withDB(database, handleProject))
	mux.HandleFunc("/api/features", withDB(database, handleFeatures))
	mux.HandleFunc("/api/features/", withDB(database, handleFeatureSubtree))
	mux.HandleFunc("/ws", hub.handleWS)
	mux.Handle("/", spaHandler())

	var handler http.Handler = mux
	if cfg.ApiKey != "" {
		handler = AuthMiddleware(cfg.ApiKey, handler)
	}
	handler = corsMiddleware(handler)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	idleConnsClosed := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Println("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		close(idleConnsClosed)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-idleConnsClosed
	return nil
}

func withDB(database *sql.DB, h func(*sql.DB, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { h(database, w, r) }
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleProject(database *sql.DB, w http.ResponseWriter, _ *http.Request) {
	project, err := db.GetProject(database)
	if err != nil {
		writeError(w, http.StatusNotFound, "no project found")
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func handleFeatures(database *sql.DB, w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		project, err := db.GetProject(database)
		if err != nil {
			writeError(w, http.StatusNotFound, "no project found")
			return
		}
		features, err := db.ListFeatures(database, project.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if features == nil {
			features = []models.Feature{}
		}
		writeJSON(w, http.StatusOK, features)
	case http.MethodPost:
		var body struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if strings.TrimSpace(body.Title) == "" {
			writeError(w, http.StatusBadRequest, "title is required")
			return
		}
		project, err := db.GetProject(database)
		if err != nil {
			writeError(w, http.StatusNotFound, "no project found")
			return
		}
		feature, err := db.AddFeature(database, project.ID, body.Title, body.Description)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, feature)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleFeatureSubtree dispatches /api/features/{id} and
// /api/features/{id}/comments.
func handleFeatureSubtree(database *sql.DB, w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/features/")
	if rest == "" {
		writeError(w, http.StatusBadRequest, "feature id required")
		return
	}
	parts := strings.Split(rest, "/")
	idStr := parts[0]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid feature id")
		return
	}

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		feature, err := db.GetFeature(database, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "feature not found")
			return
		}
		writeJSON(w, http.StatusOK, feature)
		return
	}

	if parts[1] == "comments" {
		handleFeatureComments(database, id, w, r)
		return
	}

	writeError(w, http.StatusNotFound, "not found")
}

func handleFeatureComments(database *sql.DB, featureID int64, w http.ResponseWriter, r *http.Request) {
	if _, err := db.GetFeature(database, featureID); err != nil {
		writeError(w, http.StatusNotFound, "feature not found")
		return
	}
	entityID := strconv.FormatInt(featureID, 10)

	switch r.Method {
	case http.MethodGet:
		comments, err := db.ListComments(database, "feature", entityID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if comments == nil {
			comments = []models.Comment{}
		}
		writeJSON(w, http.StatusOK, comments)
	case http.MethodPost:
		var body struct {
			Body       string `json:"body"`
			AuthorType string `json:"author_type"`
			AuthorRole string `json:"author_role"`
			Metadata   string `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if strings.TrimSpace(body.Body) == "" {
			writeError(w, http.StatusBadRequest, "body is required")
			return
		}
		if body.AuthorType == "" {
			body.AuthorType = "human"
		}
		comment, err := db.AddComment(database, &models.Comment{
			EntityType: "feature",
			EntityID:   entityID,
			AuthorType: body.AuthorType,
			AuthorRole: body.AuthorRole,
			Body:       body.Body,
			Metadata:   body.Metadata,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, comment)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// spaHandler serves the embedded Vite build, falling back to index.html
// for client-side routes.
func spaHandler() http.Handler {
	dist, err := fs.Sub(embeddedAssets, "assets/dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "frontend not built", http.StatusInternalServerError)
		})
	}
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(dist, path); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// ------------- WebSocket hub ---------------

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

type wsHub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]bool
}

func newHub() *wsHub {
	return &wsHub{clients: make(map[*websocket.Conn]bool)}
}

func (h *wsHub) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	h.mu.Lock()
	h.clients[conn] = true
	h.mu.Unlock()
	go func() {
		defer func() {
			h.mu.Lock()
			delete(h.clients, conn)
			h.mu.Unlock()
			_ = conn.Close()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}
