package server

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/engine"
	"github.com/mschulkind-oss/tillr/internal/export"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/mschulkind-oss/tillr/internal/vcs"
)

//go:embed all:assets
var embeddedAssets embed.FS

// WebSocket hub
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

func (h *wsHub) add(conn *websocket.Conn) {
	h.mu.Lock()
	h.clients[conn] = true
	h.mu.Unlock()
}

func (h *wsHub) remove(conn *websocket.Conn) {
	h.mu.Lock()
	delete(h.clients, conn)
	h.mu.Unlock()
}

func (h *wsHub) broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for conn := range h.clients {
		if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			conn.Close() //nolint:errcheck
			delete(h.clients, conn)
		}
	}
}

// Start launches the HTTP server with WebSocket support and DB file watching.
func Start(database *sql.DB, port int) error {
	return StartWithDBPath(database, port, "")
}

// StartWithDBPath launches the server and watches the given DB file for changes.
// ServerConfig holds configuration for the HTTP server.
type ServerConfig struct {
	Port       int
	DBPath     string
	RateLimit  float64 // requests per second (0 = disabled)
	RateBurst  int     // burst capacity
	ApiKey     string  // API key for authentication (empty = no auth)
	VantageURL string  // Vantage documentation viewer URL (optional)
}

// StartWithDBPath starts the server with default rate limiting disabled.
// Prefer StartWithConfig for full configuration.
func StartWithDBPath(database *sql.DB, port int, dbPath string) error {
	return StartWithConfig(database, ServerConfig{
		Port:   port,
		DBPath: dbPath,
	})
}

// BuildMux creates the HTTP mux with all API routes for a single project.
// This is used by both the single-project serve command and the multi-project daemon.
func BuildMux(database *sql.DB, cfg ServerConfig) *http.ServeMux {
	hub := newHub()
	mux := http.NewServeMux()

	// Health check (exempt from auth and rate limiting)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
	})

	// API routes
	mux.HandleFunc("/api/status", apiHandler(database, handleStatus))
	mux.HandleFunc("/api/features", apiHandler(database, handleFeatures))
	mux.HandleFunc("/api/features/", apiHandler(database, handleFeatures))
	mux.HandleFunc("/api/tags", apiHandler(database, handleTags))
	mux.HandleFunc("/api/milestones", apiHandler(database, handleMilestones))
	mux.HandleFunc("/api/milestones/", apiHandler(database, handleMilestoneDetail))
	mux.HandleFunc("/api/roadmap", apiHandler(database, handleRoadmap))
	mux.HandleFunc("/api/roadmap/", apiHandler(database, handleRoadmapStatus))
	mux.HandleFunc("/api/cycles", apiHandler(database, handleCycles))
	mux.HandleFunc("/api/cycles/", apiHandler(database, handleCycles))
	mux.HandleFunc("/api/history", apiHandler(database, handleHistory))
	mux.HandleFunc("/api/search", apiHandler(database, handleSearch))
	mux.HandleFunc("/api/stats", apiHandler(database, handleStats))
	mux.HandleFunc("/api/stats/burndown", apiHandler(database, handleStatsBurndown))
	mux.HandleFunc("/api/stats/heatmap", apiHandler(database, handleStatsHeatmap))
	mux.HandleFunc("/api/stats/activity-heatmap", apiHandler(database, handleStatsActivityHeatmap))
	mux.HandleFunc("/api/qa/", apiHandler(database, handleQA))
	mux.HandleFunc("/api/discussions", apiHandler(database, handleDiscussions))
	mux.HandleFunc("/api/discussions/", apiHandler(database, handleDiscussionDetail))
	mux.HandleFunc("/api/dependencies", apiHandler(database, handleDependencies))

	// Agent session routes
	mux.HandleFunc("/api/agents/coordination", apiHandler(database, handleAgentCoordination))
	mux.HandleFunc("/api/agents/status", apiHandler(database, handleAgentStatus))
	mux.HandleFunc("/api/agents", apiHandler(database, handleAgents))
	mux.HandleFunc("/api/agents/", apiHandler(database, handleAgentDetail))

	// Worktree routes
	mux.HandleFunc("/api/worktrees", apiHandler(database, handleWorktrees))
	mux.HandleFunc("/api/worktrees/", apiHandler(database, handleWorktreeDetail))

	// Git/VCS routes
	mux.HandleFunc("/api/git/log", apiHandler(database, handleGitLog))
	mux.HandleFunc("/api/git/branches", apiHandler(database, handleGitBranches))

	// Idea queue routes
	mux.HandleFunc("/api/ideas", apiHandler(database, handleIdeas))
	mux.HandleFunc("/api/ideas/", apiHandler(database, handleIdeaDetail))

	// Queue management route
	mux.HandleFunc("/api/queue", apiHandler(database, handleQueue))

	// Client config (exposes non-sensitive settings to the frontend)
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"vantage_url": cfg.VantageURL}) //nolint:errcheck
	})

	// Workstream routes
	mux.HandleFunc("/api/workstreams", apiHandler(database, handleWorkstreams))
	mux.HandleFunc("/api/workstreams/", apiHandler(database, handleWorkstreamDetail))

	// Context routes
	mux.HandleFunc("/api/context", apiHandler(database, handleContext))
	mux.HandleFunc("/api/context/", apiHandler(database, handleContextDetail))

	// Spec document route
	mux.HandleFunc("/api/spec-document", apiHandler(database, handleSpecDocument))

	// Decision log (ADRs) routes
	mux.HandleFunc("/api/decisions", apiHandler(database, handleDecisions))
	mux.HandleFunc("/api/decisions/", apiHandler(database, handleDecisionDetail))

	// Dashboard config routes
	mux.HandleFunc("/api/dashboards", apiHandler(database, handleDashboards))
	mux.HandleFunc("/api/dashboards/", apiHandler(database, handleDashboardDetail))

	// Notification routes
	mux.HandleFunc("/api/notifications", apiHandler(database, handleNotifications))
	mux.HandleFunc("/api/notifications/", apiHandler(database, handleNotificationAction))

	// Export routes
	mux.HandleFunc("/api/export/features", handleExport(database, "features"))
	mux.HandleFunc("/api/export/roadmap", handleExport(database, "roadmap"))
	mux.HandleFunc("/api/export/decisions", handleExport(database, "decisions"))
	mux.HandleFunc("/api/export/all", handleExport(database, "all"))

	// API documentation page
	mux.HandleFunc("/api/docs", handleAPIDocs)
	mux.HandleFunc("/api/openapi.json", handleOpenAPISpec)

	// WebSocket endpoint
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.add(conn)
		defer func() {
			if rv := recover(); rv != nil {
				log.Printf("PANIC in WebSocket handler: %v", rv)
			}
			hub.remove(conn)
		}()
		// Keep connection alive — read messages (pongs) until disconnect
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	})

	// Watch DB file for changes and broadcast to WebSocket clients
	if cfg.DBPath != "" {
		go watchDBFile(cfg.DBPath, hub)
	}

	// Periodic stale agent cleanup (every 5 minutes)
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		time.Sleep(10 * time.Second)
		cleanupStaleAgents(database)
		for range ticker.C {
			cleanupStaleAgents(database)
		}
	}()

	return mux
}

// ServeSPAFromEmbedded adds SPA (single-page app) serving of the embedded React
// build to the given mux. Handles client-side routing by falling back to index.html.
func ServeSPAFromEmbedded(mux *http.ServeMux) error {
	distFS, distErr := fs.Sub(embeddedAssets, "assets/dist")
	if distErr != nil {
		return fmt.Errorf("loading embedded assets: %w", distErr)
	}
	if _, err := fs.Stat(distFS, "index.html"); err != nil {
		return fmt.Errorf("react build not found — run 'cd web && pnpm build' first")
	}
	assetsFS := distFS
	log.Printf("Serving React frontend from embedded dist/")
	fileServer := http.FileServer(http.FS(assetsFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// Skip API and WebSocket routes
		if strings.HasPrefix(path, "/api/") || path == "/ws" {
			http.NotFound(w, r)
			return
		}
		cleanPath := strings.TrimPrefix(path, "/")
		if cleanPath != "" {
			if _, err := fs.Stat(assetsFS, cleanPath); err != nil {
				r.URL.Path = "/"
			}
		}
		if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") {
			if strings.Contains(path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			}
		}
		fileServer.ServeHTTP(w, r)
	})
	return nil
}

// StartWithConfig starts the HTTP server with the given configuration.
func StartWithConfig(database *sql.DB, cfg ServerConfig) error {
	port := cfg.Port
	// Ignore signals that could terminate the server unexpectedly
	signal.Ignore(syscall.SIGPIPE, syscall.SIGHUP, syscall.SIGURG,
		syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGWINCH,
		syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU)
	// Only SIGINT/SIGTERM cause graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("Received %v, shutting down", sig)
		os.Exit(0)
	}()

	mux := BuildMux(database, cfg)

	// Static assets — serve React build from assets/dist
	if err := ServeSPAFromEmbedded(mux); err != nil {
		return err
	}

	addr := fmt.Sprintf(":%d", port)

	// Wrap mux with request logging + panic recovery
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rv := recover(); rv != nil {
				log.Printf("PANIC recovered in %s %s: %v", r.Method, r.URL.Path, rv)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		// Log API requests (skip static assets and WebSocket upgrades)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s", r.Method, r.URL.RequestURI())
		}
		mux.ServeHTTP(w, r)
	})

	// Apply rate limiting to /api/* routes if configured.
	if cfg.RateLimit > 0 {
		rl := NewRateLimiter(cfg.RateLimit, cfg.RateBurst)
		handler = RateLimitMiddleware(rl, handler)
		log.Printf("Rate limiting enabled: %.0f req/s, burst %d", cfg.RateLimit, cfg.RateBurst)
	}

	// Apply API key authentication if configured (outermost = runs first).
	// Uses AuthMiddlewareWithDB to support both config API key and DB-backed tokens.
	if cfg.ApiKey != "" {
		handler = AuthMiddlewareWithDB(cfg.ApiKey, database, handler)
		log.Printf("API key authentication enabled (config key + DB tokens)")
	}

	return http.ListenAndServe(addr, handler)
}

// cleanupStaleAgents marks active agent sessions with no updates for 30+ minutes
// as failed and reclaims their work items.
func cleanupStaleAgents(database *sql.DB) {
	// Mark stale agent sessions as failed
	res, err := database.Exec(`UPDATE agent_sessions SET status = 'failed', updated_at = datetime('now')
		WHERE status = 'active' AND updated_at < datetime('now', '-30 minutes')`)
	if err != nil {
		log.Printf("stale agent cleanup error: %v", err)
		return
	}
	n, _ := res.RowsAffected()
	// Reclaim any orphaned work items
	reclaimed, err := engine.ReclaimStaleWorkItems(database, 30)
	if err != nil {
		log.Printf("stale work reclaim error: %v", err)
	}
	if n > 0 || reclaimed > 0 {
		log.Printf("stale cleanup: marked %d agents failed, reclaimed %d work items", n, reclaimed)
	}
}

func watchDBFile(dbPath string, hub *wsHub) {
	defer func() {
		if rv := recover(); rv != nil {
			log.Printf("PANIC in file watcher: %v", rv)
		}
	}()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("Warning: could not start file watcher: %v", err)
		return
	}
	defer watcher.Close() //nolint:errcheck

	// Watch the DB file and its WAL/SHM companions
	if err := watcher.Add(dbPath); err != nil {
		log.Printf("Warning: could not watch %s: %v", dbPath, err)
		return
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) {
				hub.broadcast([]byte(`{"type":"refresh"}`))
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("File watcher error: %v", err)
		}
	}
}

type apiFunc func(*sql.DB, http.ResponseWriter, *http.Request) error

// statusTrackingWriter wraps http.ResponseWriter to track whether a status code has been written.
type statusTrackingWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *statusTrackingWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *statusTrackingWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true // implicit 200 on first Write
	return w.ResponseWriter.Write(b)
}

func apiHandler(database *sql.DB, fn apiFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			return
		}
		sw := &statusTrackingWriter{ResponseWriter: w}
		if err := fn(database, sw, r); err != nil {
			if !sw.wroteHeader {
				sw.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(sw).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck
			} else {
				log.Printf("API error (response already started): %v", err)
			}
		}
	}
}

func timeNowUnixMilli() int64 {
	return time.Now().UnixMilli()
}

func timeNowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
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
		// POST /api/features/batch
		if id == "batch" && r.Method == "POST" {
			var body struct {
				FeatureIDs []string `json:"feature_ids"`
				Action     string   `json:"action"`
				Value      string   `json:"value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return writeJSON(w, map[string]string{"error": "invalid request body"})
			}
			if len(body.FeatureIDs) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				return writeJSON(w, map[string]string{"error": "feature_ids is required"})
			}
			if body.Value == "" {
				w.WriteHeader(http.StatusBadRequest)
				return writeJSON(w, map[string]string{"error": "value is required"})
			}
			fieldMap := map[string]string{
				"set_status":    "status",
				"set_milestone": "milestone_id",
				"set_priority":  "priority",
			}
			field, ok := fieldMap[body.Action]
			if !ok {
				w.WriteHeader(http.StatusBadRequest)
				return writeJSON(w, map[string]string{"error": "invalid action: must be set_status, set_milestone, or set_priority"})
			}
			if field == "status" {
				validStatuses := map[string]bool{
					"draft": true, "planning": true, "implementing": true,
					"agent-qa": true, "human-qa": true, "done": true, "blocked": true,
				}
				if !validStatuses[body.Value] {
					w.WriteHeader(http.StatusBadRequest)
					return writeJSON(w, map[string]string{"error": "invalid status value"})
				}
			}
			updated, err := db.BatchUpdateFeatures(database, body.FeatureIDs, field, body.Value)
			if err != nil {
				return fmt.Errorf("batch updating features: %w", err)
			}
			return writeJSON(w, map[string]any{"ok": true, "updated": updated})
		}

		// POST /api/features/reorder
		if id == "reorder" && r.Method == "POST" {
			var body struct {
				Items []db.FeaturePriorityItem `json:"items"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return writeJSON(w, map[string]string{"error": "invalid request body"})
			}
			if len(body.Items) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				return writeJSON(w, map[string]string{"error": "items array is required"})
			}
			if err := db.ReorderFeaturePriorities(database, body.Items); err != nil {
				return fmt.Errorf("reordering feature priorities: %w", err)
			}
			return writeJSON(w, map[string]bool{"ok": true})
		}

		// Check for /api/features/{id}/deps endpoint
		if rest, found := strings.CutSuffix(id, "/deps"); found && rest != "" {
			return handleFeatureDeps(database, w, rest)
		}

		// Check for /api/features/{id}/prs endpoint
		if rest, found := strings.CutSuffix(id, "/prs"); found && rest != "" {
			return handleFeaturePRs(database, w, rest)
		}

		// Handle PATCH for feature updates
		if r.Method == "PATCH" {
			var body struct {
				Status      string `json:"status"`
				Priority    int    `json:"priority"`
				Name        string `json:"name"`
				Description string `json:"description"`
				Spec        string `json:"spec"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return writeJSON(w, map[string]string{"error": "invalid request body"})
			}
			if body.Status != "" {
				validStatuses := map[string]bool{
					"draft": true, "planning": true, "implementing": true,
					"agent-qa": true, "human-qa": true, "done": true, "blocked": true,
				}
				if !validStatuses[body.Status] {
					w.WriteHeader(http.StatusBadRequest)
					return writeJSON(w, map[string]string{"error": "invalid status"})
				}
				p, err := db.GetProject(database)
				if err != nil {
					return err
				}
				if err := engine.TransitionFeature(database, p.ID, id, body.Status); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return writeJSON(w, map[string]string{"error": err.Error()})
				}
			}
			// Apply non-status field updates
			updates := map[string]any{}
			if body.Name != "" {
				updates["name"] = body.Name
			}
			if body.Description != "" {
				updates["description"] = body.Description
			}
			if body.Spec != "" {
				updates["spec"] = body.Spec
			}
			if len(updates) > 0 {
				if err := db.UpdateFeature(database, id, updates); err != nil {
					return fmt.Errorf("updating feature: %w", err)
				}
			}
			f, err := db.GetFeature(database, id)
			if err != nil {
				return err
			}
			return writeJSON(w, f)
		}

		f, err := db.GetFeature(database, id)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return writeJSON(w, map[string]string{"error": "feature not found"})
		}
		// Enrich with work items and cycle scores
		workItems, _ := db.ListWorkItemsForFeature(database, id)
		if workItems == nil {
			workItems = []models.WorkItem{}
		}
		cycles, _ := db.ListCycleHistory(database, id)
		if cycles == nil {
			cycles = []models.CycleInstance{}
		}
		// Build enriched scores from all cycles
		var allScores []models.CycleScore
		for _, c := range cycles {
			scores, _ := db.ListCycleScores(database, c.ID)
			allScores = append(allScores, scores...)
		}
		if allScores == nil {
			allScores = []models.CycleScore{}
		}
		return writeJSON(w, map[string]any{
			"feature":    f,
			"work_items": workItems,
			"cycles":     cycles,
			"scores":     allScores,
		})
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

func handleTags(database *sql.DB, w http.ResponseWriter, _ *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}
	tags, err := db.ListAllTags(database, p.ID)
	if err != nil {
		return err
	}
	if tags == nil {
		tags = []models.TagCount{}
	}
	return writeJSON(w, tags)
}

func handleFeatureDeps(database *sql.DB, w http.ResponseWriter, featureID string) error {
	f, err := db.GetFeature(database, featureID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return writeJSON(w, map[string]string{"error": "feature not found"})
	}

	type depInfo struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}

	// Direct dependencies
	var dependsOn []depInfo
	for _, depID := range f.DependsOn {
		dep, err := db.GetFeature(database, depID)
		if err != nil {
			dependsOn = append(dependsOn, depInfo{ID: depID, Name: depID, Status: "unknown"})
			continue
		}
		dependsOn = append(dependsOn, depInfo{ID: dep.ID, Name: dep.Name, Status: dep.Status})
	}
	if dependsOn == nil {
		dependsOn = []depInfo{}
	}

	// Features that depend on this one
	dependents, _ := db.GetFeatureDependents(database, featureID)
	var dependedBy []depInfo
	for _, dep := range dependents {
		dependedBy = append(dependedBy, depInfo{ID: dep.ID, Name: dep.Name, Status: dep.Status})
	}
	if dependedBy == nil {
		dependedBy = []depInfo{}
	}

	// Build blocking chain: transitive deps that aren't done
	tree, _ := db.GetFeatureDependencyTree(database, featureID)
	var blockingChain []string
	for _, node := range tree {
		if node.ID == featureID {
			continue
		}
		if node.Status != "done" {
			blockingChain = append(blockingChain, fmt.Sprintf("%s (%s)", node.ID, node.Status))
		}
	}
	if blockingChain == nil {
		blockingChain = []string{}
	}

	return writeJSON(w, map[string]any{
		"feature":        f,
		"depends_on":     dependsOn,
		"depended_by":    dependedBy,
		"blocking_chain": blockingChain,
	})
}

func handleFeaturePRs(database *sql.DB, w http.ResponseWriter, featureID string) error {
	if _, err := db.GetFeature(database, featureID); err != nil {
		w.WriteHeader(http.StatusNotFound)
		return writeJSON(w, map[string]string{"error": "feature not found"})
	}

	prs, err := db.ListFeaturePRs(database, featureID)
	if err != nil {
		return fmt.Errorf("listing feature PRs: %w", err)
	}
	if prs == nil {
		prs = []models.FeaturePR{}
	}
	return writeJSON(w, prs)
}

func handleMilestones(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}

	if r.Method == "POST" {
		var body struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			SortOrder   int    `json:"sort_order"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if body.ID == "" || body.Name == "" {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "id and name are required"})
		}
		m := &models.Milestone{
			ID:          body.ID,
			ProjectID:   p.ID,
			Name:        body.Name,
			Description: body.Description,
			SortOrder:   body.SortOrder,
		}
		if err := db.CreateMilestone(database, m); err != nil {
			return fmt.Errorf("creating milestone: %w", err)
		}
		created, err := db.GetMilestone(database, body.ID)
		if err != nil {
			return fmt.Errorf("fetching created milestone: %w", err)
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, created)
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

func handleMilestoneDetail(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	id := strings.TrimPrefix(r.URL.Path, "/api/milestones/")
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "milestone id required"})
	}

	if r.Method == "PATCH" {
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		updates := map[string]any{}
		if body.Name != "" {
			updates["name"] = body.Name
		}
		if body.Description != "" {
			updates["description"] = body.Description
		}
		if len(updates) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "no fields to update"})
		}
		if err := db.UpdateMilestone(database, id, updates); err != nil {
			if err == sql.ErrNoRows {
				w.WriteHeader(http.StatusNotFound)
				return writeJSON(w, map[string]string{"error": "milestone not found"})
			}
			return fmt.Errorf("updating milestone: %w", err)
		}
		m, err := db.GetMilestone(database, id)
		if err != nil {
			return fmt.Errorf("fetching updated milestone: %w", err)
		}
		return writeJSON(w, m)
	}

	// GET — return milestone detail
	m, err := db.GetMilestone(database, id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return writeJSON(w, map[string]string{"error": "milestone not found"})
	}
	return writeJSON(w, m)
}

func handleRoadmap(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}

	if r.Method == "POST" {
		var body struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Category    string `json:"category"`
			Priority    string `json:"priority"`
			Effort      string `json:"effort"`
			Status      string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if body.ID == "" || body.Title == "" {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "id and title are required"})
		}
		if body.Priority == "" {
			body.Priority = "medium"
		}
		ri := &models.RoadmapItem{
			ID:          body.ID,
			ProjectID:   p.ID,
			Title:       body.Title,
			Description: body.Description,
			Category:    body.Category,
			Priority:    body.Priority,
			Effort:      body.Effort,
		}
		if body.Status != "" && validRoadmapStatuses[body.Status] {
			ri.Status = body.Status
		}
		if err := db.CreateRoadmapItem(database, ri); err != nil {
			return fmt.Errorf("creating roadmap item: %w", err)
		}
		item, err := db.GetRoadmapItem(database, body.ID)
		if err != nil {
			return fmt.Errorf("fetching created roadmap item: %w", err)
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, item)
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

	// POST /api/cycles/{id}/advance — approve or reject a human-owned cycle step
	if strings.HasSuffix(path, "/advance") && r.Method == "POST" {
		idStr := strings.TrimPrefix(path, "/api/cycles/")
		idStr = strings.TrimSuffix(idStr, "/advance")
		var cycleID int
		if _, err := fmt.Sscanf(idStr, "%d", &cycleID); err != nil {
			return fmt.Errorf("invalid cycle ID: %s", idStr)
		}

		var body struct {
			Action string `json:"action"` // "approve" or "reject"
			Notes  string `json:"notes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return fmt.Errorf("invalid request body: %w", err)
		}
		if body.Action != "approve" && body.Action != "reject" {
			return fmt.Errorf("action must be 'approve' or 'reject'")
		}

		cycle, err := db.GetCycleByID(database, cycleID)
		if err != nil {
			return fmt.Errorf("cycle not found: %w", err)
		}

		// Resolve cycle type (built-in or custom template)
		var ct *models.CycleType
		for i := range models.CycleTypes {
			if models.CycleTypes[i].Name == cycle.CycleType {
				ct = &models.CycleTypes[i]
				break
			}
		}
		if ct == nil {
			if t, err := db.GetCycleTemplate(database, cycle.CycleType); err == nil && t != nil {
				ct = &models.CycleType{Name: t.Name, Description: t.Description, Steps: t.Steps}
			}
		}
		if ct == nil {
			return fmt.Errorf("unknown cycle type: %s", cycle.CycleType)
		}
		if cycle.CurrentStep >= len(ct.Steps) {
			return fmt.Errorf("cycle is beyond its defined steps")
		}
		if !ct.IsHumanStep(cycle.CurrentStep) {
			return fmt.Errorf("current step %q is not human-owned", ct.Steps[cycle.CurrentStep].Name)
		}

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}
		stepName := ct.Steps[cycle.CurrentStep].Name

		if body.Action == "reject" {
			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				FeatureID: cycle.EntityID,
				EventType: "cycle.step.rejected",
				Data:      fmt.Sprintf(`{"step":%q,"notes":%q}`, stepName, body.Notes),
			})
			return writeJSON(w, map[string]any{
				"feature": cycle.EntityID,
				"step":    stepName,
				"action":  "rejected",
				"notes":   body.Notes,
			})
		}

		// Approve: log event
		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			FeatureID: cycle.EntityID,
			EventType: "cycle.step.approved",
			Data:      fmt.Sprintf(`{"step":%q,"notes":%q}`, stepName, body.Notes),
		})

		nextStep := cycle.CurrentStep + 1
		if nextStep >= len(ct.Steps) {
			// Complete the cycle
			if err := db.UpdateCycleInstance(database, cycle.ID, cycle.CurrentStep, cycle.Iteration, "completed"); err != nil {
				return fmt.Errorf("completing cycle: %w", err)
			}
			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				FeatureID: cycle.EntityID,
				EventType: "cycle.completed",
				Data:      fmt.Sprintf(`{"cycle_type":%q}`, cycle.CycleType),
			})
			return writeJSON(w, map[string]any{
				"feature": cycle.EntityID,
				"step":    stepName,
				"action":  "approved",
				"result":  "completed",
			})
		}

		// Advance to next step
		if err := db.UpdateCycleInstance(database, cycle.ID, nextStep, cycle.Iteration, "active"); err != nil {
			return fmt.Errorf("advancing cycle: %w", err)
		}
		nextStepName := ct.Steps[nextStep].Name
		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			FeatureID: cycle.EntityID,
			EventType: "cycle.advanced",
			Data:      fmt.Sprintf(`{"from":%q,"to":%q}`, stepName, nextStepName),
		})

		// Create work item for agent steps
		if !ct.Steps[nextStep].Human {
			_ = db.CreateWorkItem(database, &models.WorkItem{
				FeatureID: cycle.EntityID,
				WorkType:  nextStepName,
			})
		}

		return writeJSON(w, map[string]any{
			"feature":   cycle.EntityID,
			"step":      stepName,
			"action":    "approved",
			"next_step": nextStepName,
		})
	}

	// /api/cycles/{id}/scores
	if strings.HasSuffix(path, "/scores") {
		idStr := strings.TrimPrefix(path, "/api/cycles/")
		idStr = strings.TrimSuffix(idStr, "/scores")
		var cycleID int
		if _, err := fmt.Sscanf(idStr, "%d", &cycleID); err != nil {
			return fmt.Errorf("invalid cycle ID: %s", idStr)
		}
		scores, err := db.ListCycleScores(database, cycleID)
		if err != nil {
			return err
		}
		if scores == nil {
			scores = []models.CycleScore{}
		}
		return writeJSON(w, scores)
	}
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

	// GET /api/cycles/types — list predefined cycle types
	if strings.TrimPrefix(path, "/api/cycles/") == "types" && r.Method == "GET" {
		return writeJSON(w, models.CycleTypes)
	}

	// GET /api/cycles/{id} — single cycle detail with scores and step names
	trimmed := strings.TrimPrefix(path, "/api/cycles/")
	if trimmed != "" && trimmed != path {
		var cycleID int
		if _, err := fmt.Sscanf(trimmed, "%d", &cycleID); err == nil {
			cycle, err := db.GetCycleByID(database, cycleID)
			if err != nil {
				return fmt.Errorf("cycle not found: %w", err)
			}
			scores, err := db.ListCycleScores(database, cycleID)
			if err != nil {
				return err
			}
			if scores == nil {
				scores = []models.CycleScore{}
			}
			// Resolve step names from cycle type
			var steps []models.CycleStep
			for _, ct := range models.CycleTypes {
				if ct.Name == cycle.CycleType {
					steps = ct.Steps
					break
				}
			}
			if steps == nil {
				steps = []models.CycleStep{}
			}
			return writeJSON(w, models.CycleDetail{
				Cycle:  *cycle,
				Scores: scores,
				Steps:  steps,
			})
		}
	}

	// List all cycles (active + completed) with enriched data
	active, err := db.ListActiveCycles(database)
	if err != nil {
		return err
	}
	completed, err := db.ListAllCycles(database)
	if err != nil {
		// fallback if ListAllCycles doesn't exist yet
		completed = []models.CycleInstance{}
	}
	// Merge — include completed ones not in active
	activeIDs := make(map[int]bool)
	for _, c := range active {
		activeIDs[c.ID] = true
	}
	all := append([]models.CycleInstance{}, active...)
	for _, c := range completed {
		if !activeIDs[c.ID] {
			all = append(all, c)
		}
	}
	if all == nil {
		all = []models.CycleInstance{}
	}
	return writeJSON(w, all)
}

func handleHistory(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}
	featureID := r.URL.Query().Get("feature")
	eventType := r.URL.Query().Get("type")
	since := r.URL.Query().Get("since")
	events, err := db.ListEvents(database, p.ID, featureID, eventType, since, 100)
	if err != nil {
		return err
	}
	if events == nil {
		events = []models.Event{}
	}
	return writeJSON(w, events)
}

func handleSearch(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query().Get("q")
	if q == "" {
		return writeJSON(w, []models.SearchResult{})
	}
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if n, err := fmt.Sscanf(limitStr, "%d", &limit); n != 1 || err != nil {
			limit = 50
		}
	}
	results, err := db.SearchFTS(database, q, limit)
	if err != nil {
		// Fall back to the old LIKE-based event search if FTS fails
		p, pErr := db.GetProject(database)
		if pErr != nil {
			return pErr
		}
		events, eErr := db.SearchEvents(database, p.ID, q)
		if eErr != nil {
			return eErr
		}
		return writeJSON(w, events)
	}
	return writeJSON(w, results)
}

func handleStats(database *sql.DB, w http.ResponseWriter, _ *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}
	stats, err := db.GetProjectStats(database, p.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, stats)
}

func handleStatsBurndown(database *sql.DB, w http.ResponseWriter, _ *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}
	data, err := db.GetBurndownData(database, p.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, data)
}

func handleStatsHeatmap(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}
	days := 365
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err2 := fmt.Sscanf(d, "%d", &days); n != 1 || err2 != nil {
			days = 365
		}
		if days < 1 || days > 730 {
			days = 365
		}
	}
	heatmap, err := db.GetActivityHeatmap(database, p.ID, days)
	if err != nil {
		return err
	}
	return writeJSON(w, models.HeatmapResponse{Days: heatmap})
}

func handleStatsActivityHeatmap(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}
	days := 365
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err2 := fmt.Sscanf(d, "%d", &days); n != 1 || err2 != nil {
			days = 365
		}
		if days < 1 || days > 730 {
			days = 365
		}
	}
	counts, err := db.GetDailyActivityCounts(database, p.ID, days)
	if err != nil {
		return err
	}
	return writeJSON(w, counts)
}

func handleQA(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	path := strings.TrimPrefix(r.URL.Path, "/api/qa/")

	// GET /api/qa/pending — list features awaiting QA
	if r.Method == "GET" && path == "pending" {
		p, err := db.GetProject(database)
		if err != nil {
			return err
		}
		// Features in human-qa or agent-qa status
		humanQA, err := db.ListFeatures(database, p.ID, "human-qa", "")
		if err != nil {
			return err
		}
		agentQA, err := db.ListFeatures(database, p.ID, "agent-qa", "")
		if err != nil {
			return err
		}
		pending := append(humanQA, agentQA...)
		return writeJSON(w, pending)
	}

	// GET /api/qa/history — QA-related events
	if r.Method == "GET" && path == "history" {
		p, err := db.GetProject(database)
		if err != nil {
			return err
		}
		events, err := db.ListEvents(database, p.ID, "", "qa", "", 50)
		if err != nil {
			return err
		}
		return writeJSON(w, events)
	}

	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return writeJSON(w, map[string]string{"error": "POST required"})
	}

	parts := strings.Split(path, "/")
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
	"done":        true,
	"deferred":    true,
}

func handleRoadmapStatus(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		return nil
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/roadmap/")

	// POST /api/roadmap/reorder
	if path == "reorder" {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return writeJSON(w, map[string]string{"error": "POST required"})
		}
		var body struct {
			Items []db.ReorderItem `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if len(body.Items) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "items array is required"})
		}
		if err := db.ReorderRoadmapItems(database, body.Items); err != nil {
			return fmt.Errorf("reordering roadmap items: %w", err)
		}
		return writeJSON(w, map[string]bool{"ok": true})
	}

	// GET /api/roadmap/{id} — return single item
	if r.Method == "GET" {
		id := strings.Split(path, "/")[0]
		item, err := db.GetRoadmapItem(database, id)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return writeJSON(w, map[string]string{"error": "roadmap item not found"})
		}
		return writeJSON(w, item)
	}

	if r.Method != "PATCH" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return writeJSON(w, map[string]string{"error": "PATCH required"})
	}

	parts := strings.Split(path, "/")

	// PATCH /api/roadmap/{id}/status — dedicated status endpoint
	if len(parts) == 2 && parts[1] == "status" && parts[0] != "" {
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

	// PATCH /api/roadmap/{id} — general field updates
	if len(parts) == 1 && parts[0] != "" {
		id := parts[0]
		var body struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Priority    string `json:"priority"`
			Effort      string `json:"effort"`
			Category    string `json:"category"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		updates := map[string]any{}
		if body.Title != "" {
			updates["title"] = body.Title
		}
		if body.Description != "" {
			updates["description"] = body.Description
		}
		if body.Priority != "" {
			updates["priority"] = body.Priority
		}
		if body.Effort != "" {
			updates["effort"] = body.Effort
		}
		if body.Category != "" {
			updates["category"] = body.Category
		}
		if len(updates) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "no fields to update"})
		}
		if err := db.UpdateRoadmapItem(database, id, updates); err != nil {
			return fmt.Errorf("updating roadmap item: %w", err)
		}
		item, err := db.GetRoadmapItem(database, id)
		if err != nil {
			return fmt.Errorf("fetching updated roadmap item: %w", err)
		}
		return writeJSON(w, item)
	}

	w.WriteHeader(http.StatusBadRequest)
	return writeJSON(w, map[string]string{"error": "invalid path"})
}

func handleDiscussions(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}

	if r.Method == "POST" {
		var body struct {
			Title     string `json:"title"`
			Body      string `json:"body"`
			FeatureID string `json:"feature_id"`
			Author    string `json:"author"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if body.Title == "" {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "title is required"})
		}
		if body.Author == "" {
			body.Author = "human"
		}
		d := &models.Discussion{
			ProjectID: p.ID,
			Title:     body.Title,
			Body:      body.Body,
			FeatureID: body.FeatureID,
			Author:    body.Author,
		}
		if err := db.CreateDiscussion(database, d); err != nil {
			return fmt.Errorf("creating discussion: %w", err)
		}
		created, err := db.GetDiscussion(database, d.ID)
		if err != nil {
			return fmt.Errorf("fetching created discussion: %w", err)
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, created)
	}

	featureID := r.URL.Query().Get("feature")
	status := r.URL.Query().Get("status")

	discussions, err := db.ListDiscussions(database, p.ID, featureID, status)
	if err != nil {
		return err
	}

	return writeJSON(w, discussions)
}

func handleDiscussionDetail(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	path := strings.TrimPrefix(r.URL.Path, "/api/discussions/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "discussion ID required"})
	}

	id := 0
	for _, c := range parts[0] {
		if c >= '0' && c <= '9' {
			id = id*10 + int(c-'0')
		} else {
			break
		}
	}

	// POST /api/discussions/{id}/replies
	if r.Method == "POST" && len(parts) >= 2 && parts[1] == "replies" {
		var body struct {
			Body   string `json:"body"`
			Author string `json:"author"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if body.Body == "" {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "body is required"})
		}
		if body.Author == "" {
			body.Author = "human"
		}
		c := &models.DiscussionComment{
			DiscussionID: id,
			Author:       body.Author,
			Content:      body.Body,
			CommentType:  "comment",
		}
		if err := db.AddDiscussionComment(database, c); err != nil {
			return fmt.Errorf("adding reply: %w", err)
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, c)
	}

	// POST /api/discussions/{id}/votes
	if r.Method == "POST" && len(parts) >= 2 && parts[1] == "votes" {
		var body struct {
			Reaction string `json:"reaction"`
			Voter    string `json:"voter"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if !db.ValidReactions[body.Reaction] {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid reaction"})
		}
		if body.Voter == "" {
			body.Voter = "human"
		}
		v := &models.DiscussionVote{
			DiscussionID: id,
			Voter:        body.Voter,
			Reaction:     body.Reaction,
		}
		if err := db.AddDiscussionVote(database, v); err != nil {
			return fmt.Errorf("adding vote: %w", err)
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, v)
	}

	// GET /api/discussions/{id}/votes
	if r.Method == "GET" && len(parts) >= 2 && parts[1] == "votes" {
		summary, err := db.GetDiscussionVotes(database, id)
		if err != nil {
			return fmt.Errorf("getting votes: %w", err)
		}
		return writeJSON(w, summary)
	}

	d, err := db.GetDiscussion(database, id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return writeJSON(w, map[string]string{"error": "discussion not found"})
	}

	return writeJSON(w, d)
}

func handleDependencies(database *sql.DB, w http.ResponseWriter, _ *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}

	features, err := db.ListFeatures(database, p.ID, "", "")
	if err != nil {
		return err
	}

	type node struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	type edge struct {
		From string `json:"from"`
		To   string `json:"to"`
	}

	var nodes []node
	var edges []edge
	for _, f := range features {
		nodes = append(nodes, node{ID: f.ID, Name: f.Name, Status: f.Status})
		for _, dep := range f.DependsOn {
			edges = append(edges, edge{From: f.ID, To: dep})
		}
	}

	return writeJSON(w, map[string]any{
		"nodes": nodes,
		"edges": edges,
	})
}

// --- Agent Sessions ---

func handleAgentCoordination(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}
	status, err := engine.GetCoordinationStatus(database, p.ID)
	if err != nil {
		return fmt.Errorf("getting coordination status: %w", err)
	}
	return writeJSON(w, status)
}

func handleAgentStatus(database *sql.DB, w http.ResponseWriter, _ *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}
	dashboard, err := db.GetAgentStatusDashboard(database, p.ID)
	if err != nil {
		return fmt.Errorf("getting agent status dashboard: %w", err)
	}
	return writeJSON(w, dashboard)
}

func handleAgents(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}

	if r.Method == "POST" {
		var body struct {
			Name            string `json:"name"`
			TaskDescription string `json:"task_description"`
			FeatureID       string `json:"feature_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if body.Name == "" {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "name is required"})
		}
		s := &models.AgentSession{
			ID:              fmt.Sprintf("agent-%d", timeNowUnixMilli()),
			ProjectID:       p.ID,
			Name:            body.Name,
			TaskDescription: body.TaskDescription,
			FeatureID:       body.FeatureID,
			Status:          "active",
		}
		if err := db.CreateAgentSession(database, s); err != nil {
			return fmt.Errorf("creating agent session: %w", err)
		}
		created, err := db.GetAgentSession(database, s.ID)
		if err != nil {
			return fmt.Errorf("fetching created agent session: %w", err)
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, created)
	}

	status := r.URL.Query().Get("status")
	sessions, err := db.ListAgentSessions(database, p.ID, status)
	if err != nil {
		return err
	}
	if sessions == nil {
		sessions = []models.AgentSession{}
	}
	return writeJSON(w, sessions)
}

func handleAgentDetail(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "agent session ID required"})
	}
	id := parts[0]

	// POST /api/agents/{id}/update
	if r.Method == "POST" && len(parts) >= 2 && parts[1] == "update" {
		var body struct {
			MessageMD   string `json:"message_md"`
			ProgressPct *int   `json:"progress_pct"`
			Phase       string `json:"phase"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		u := &models.StatusUpdate{
			AgentSessionID: id,
			MessageMD:      body.MessageMD,
			ProgressPct:    body.ProgressPct,
			Phase:          body.Phase,
		}
		if err := db.InsertStatusUpdate(database, u); err != nil {
			return fmt.Errorf("inserting status update: %w", err)
		}
		// Also update agent session fields if provided
		updates := map[string]any{}
		if body.ProgressPct != nil {
			updates["progress_pct"] = *body.ProgressPct
		}
		if body.Phase != "" {
			updates["current_phase"] = body.Phase
		}
		if len(updates) > 0 {
			_ = db.UpdateAgentSession(database, id, updates)
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, u)
	}

	// PATCH /api/agents/{id}
	if r.Method == "PATCH" {
		var body struct {
			ProgressPct  *int   `json:"progress_pct"`
			CurrentPhase string `json:"current_phase"`
			ETA          string `json:"eta"`
			Status       string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		updates := map[string]any{}
		if body.ProgressPct != nil {
			updates["progress_pct"] = *body.ProgressPct
		}
		if body.CurrentPhase != "" {
			updates["current_phase"] = body.CurrentPhase
		}
		if body.ETA != "" {
			updates["eta"] = body.ETA
		}
		if body.Status != "" {
			updates["status"] = body.Status
		}
		if err := db.UpdateAgentSession(database, id, updates); err != nil {
			return fmt.Errorf("updating agent session: %w", err)
		}
		s, err := db.GetAgentSession(database, id)
		if err != nil {
			return err
		}
		return writeJSON(w, s)
	}

	// GET /api/agents/{id}
	s, err := db.GetAgentSession(database, id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return writeJSON(w, map[string]string{"error": "agent session not found"})
	}
	updates, _ := db.ListStatusUpdates(database, id)
	if updates == nil {
		updates = []models.StatusUpdate{}
	}
	// Include linked worktree if any
	var worktree *models.Worktree
	if wt, wtErr := db.GetWorktreeByAgent(database, id); wtErr == nil {
		worktree = wt
	}
	return writeJSON(w, map[string]any{
		"session":  s,
		"updates":  updates,
		"worktree": worktree,
	})
}

// --- Worktrees ---

func handleWorktrees(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	if r.Method == "POST" {
		var body struct {
			Name   string `json:"name"`
			Path   string `json:"path"`
			Branch string `json:"branch"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if body.Name == "" || body.Path == "" {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "name and path are required"})
		}
		wt := &models.Worktree{
			ID:     fmt.Sprintf("wt-%d", timeNowUnixMilli()),
			Name:   body.Name,
			Path:   body.Path,
			Branch: body.Branch,
		}
		if err := db.CreateWorktree(database, wt); err != nil {
			return fmt.Errorf("creating worktree: %w", err)
		}
		created, err := db.GetWorktree(database, wt.ID)
		if err != nil {
			return fmt.Errorf("fetching created worktree: %w", err)
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, created)
	}

	// GET /api/worktrees
	worktrees, err := db.ListWorktrees(database)
	if err != nil {
		return err
	}
	if worktrees == nil {
		worktrees = []models.Worktree{}
	}
	return writeJSON(w, worktrees)
}

func handleWorktreeDetail(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	path := strings.TrimPrefix(r.URL.Path, "/api/worktrees/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "worktree ID required"})
	}
	id := parts[0]

	// POST /api/worktrees/{id}/link
	if r.Method == "POST" && len(parts) >= 2 && parts[1] == "link" {
		var body struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if body.AgentID == "" {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "agent_id is required"})
		}
		if err := db.LinkWorktreeToAgent(database, id, body.AgentID); err != nil {
			return fmt.Errorf("linking worktree: %w", err)
		}
		return writeJSON(w, map[string]bool{"ok": true})
	}

	// DELETE /api/worktrees/{id}
	if r.Method == "DELETE" {
		if err := db.DeleteWorktree(database, id); err != nil {
			return fmt.Errorf("deleting worktree: %w", err)
		}
		return writeJSON(w, map[string]bool{"ok": true})
	}

	// GET /api/worktrees/{id}
	wt, err := db.GetWorktree(database, id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return writeJSON(w, map[string]string{"error": "worktree not found"})
	}
	// Include linked agent if any
	var agent *models.AgentSession
	if wt.AgentSessionID != "" {
		if a, aErr := db.GetAgentSession(database, wt.AgentSessionID); aErr == nil {
			agent = a
		}
	}
	return writeJSON(w, map[string]any{
		"worktree": wt,
		"agent":    agent,
	})
}

// --- Idea Queue ---

func handleIdeas(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}

	if r.Method == "POST" {
		var body struct {
			Title         string `json:"title"`
			RawInput      string `json:"raw_input"`
			IdeaType      string `json:"idea_type"`
			AutoImplement bool   `json:"auto_implement"`
			SubmittedBy   string `json:"submitted_by"`
			SourcePage    string `json:"source_page"`
			Context       string `json:"context"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if body.Title == "" {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "title is required"})
		}
		if body.IdeaType == "" {
			body.IdeaType = "feature"
		}
		if body.SubmittedBy == "" {
			body.SubmittedBy = "human"
		}
		idea := &models.IdeaQueueItem{
			ProjectID:     p.ID,
			Title:         body.Title,
			RawInput:      body.RawInput,
			IdeaType:      body.IdeaType,
			Status:        "pending",
			AutoImplement: body.AutoImplement,
			SubmittedBy:   body.SubmittedBy,
			SourcePage:    body.SourcePage,
			Context:       body.Context,
		}
		if err := db.InsertIdea(database, idea); err != nil {
			return fmt.Errorf("creating idea: %w", err)
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, idea)
	}

	status := r.URL.Query().Get("status")
	ideaType := r.URL.Query().Get("type")
	ideas, err := db.ListIdeas(database, p.ID, status, ideaType)
	if err != nil {
		return err
	}
	if ideas == nil {
		ideas = []models.IdeaQueueItem{}
	}

	// History view: enrich ideas with linked feature details
	if r.URL.Query().Get("view") == "history" {
		type linkedFeature struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Status   string `json:"status"`
			Priority int    `json:"priority"`
		}
		type ideaWithFeature struct {
			models.IdeaQueueItem
			LinkedFeature *linkedFeature `json:"linked_feature,omitempty"`
		}
		result := make([]ideaWithFeature, 0, len(ideas))
		featureCache := map[string]*linkedFeature{}
		for _, idea := range ideas {
			item := ideaWithFeature{IdeaQueueItem: idea}
			if idea.FeatureID != "" {
				if lf, ok := featureCache[idea.FeatureID]; ok {
					item.LinkedFeature = lf
				} else if f, fErr := db.GetFeature(database, idea.FeatureID); fErr == nil {
					lf = &linkedFeature{ID: f.ID, Name: f.Name, Status: f.Status, Priority: f.Priority}
					featureCache[idea.FeatureID] = lf
					item.LinkedFeature = lf
				}
			}
			result = append(result, item)
		}
		return writeJSON(w, result)
	}

	return writeJSON(w, ideas)
}

func handleIdeaDetail(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	path := strings.TrimPrefix(r.URL.Path, "/api/ideas/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "idea ID required"})
	}

	id := 0
	for _, c := range parts[0] {
		if c >= '0' && c <= '9' {
			id = id*10 + int(c-'0')
		} else {
			break
		}
	}
	if id == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "invalid idea ID"})
	}

	// POST /api/ideas/{id}/spec
	if r.Method == "POST" && len(parts) >= 2 && parts[1] == "spec" {
		var body struct {
			SpecMD string `json:"spec_md"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if err := db.SetIdeaSpec(database, id, body.SpecMD); err != nil {
			return fmt.Errorf("setting idea spec: %w", err)
		}
		idea, err := db.GetIdea(database, id)
		if err != nil {
			return err
		}
		return writeJSON(w, idea)
	}

	// POST /api/ideas/{id}/approve
	if r.Method == "POST" && len(parts) >= 2 && parts[1] == "approve" {
		var body struct {
			Notes     string `json:"notes"`
			FeatureID string `json:"feature_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if err := db.ApproveIdea(database, id, body.FeatureID); err != nil {
			return fmt.Errorf("approving idea: %w", err)
		}
		idea, err := db.GetIdea(database, id)
		if err != nil {
			return err
		}
		return writeJSON(w, idea)
	}

	// POST /api/ideas/{id}/reject
	if r.Method == "POST" && len(parts) >= 2 && parts[1] == "reject" {
		if err := db.UpdateIdeaStatus(database, id, "rejected"); err != nil {
			return fmt.Errorf("rejecting idea: %w", err)
		}
		idea, err := db.GetIdea(database, id)
		if err != nil {
			return err
		}
		return writeJSON(w, idea)
	}

	// GET /api/ideas/{id}
	idea, err := db.GetIdea(database, id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return writeJSON(w, map[string]string{"error": "idea not found"})
	}
	return writeJSON(w, idea)
}

// --- Context ---

func handleContext(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}

	if r.Method == "POST" {
		var body struct {
			FeatureID   string `json:"feature_id"`
			ContextType string `json:"context_type"`
			Title       string `json:"title"`
			ContentMD   string `json:"content_md"`
			Author      string `json:"author"`
			Tags        string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if body.Title == "" {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "title is required"})
		}
		if body.ContextType == "" {
			body.ContextType = "note"
		}
		if body.Author == "" {
			body.Author = "human"
		}
		e := &models.ContextEntry{
			ProjectID:   p.ID,
			FeatureID:   body.FeatureID,
			ContextType: body.ContextType,
			Title:       body.Title,
			ContentMD:   body.ContentMD,
			Author:      body.Author,
			Tags:        body.Tags,
		}
		if err := db.InsertContext(database, e); err != nil {
			return fmt.Errorf("creating context entry: %w", err)
		}
		created, err := db.GetContextEntry(database, e.ID)
		if err != nil {
			return fmt.Errorf("fetching created context entry: %w", err)
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, created)
	}

	featureID := r.URL.Query().Get("feature_id")
	entries, err := db.ListContext(database, p.ID, featureID)
	if err != nil {
		return err
	}
	if entries == nil {
		entries = []models.ContextEntry{}
	}
	return writeJSON(w, entries)
}

func handleContextDetail(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	path := strings.TrimPrefix(r.URL.Path, "/api/context/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "context entry ID or action required"})
	}

	// GET /api/context/search?q=...
	if parts[0] == "search" {
		p, err := db.GetProject(database)
		if err != nil {
			return err
		}
		q := r.URL.Query().Get("q")
		if q == "" {
			return writeJSON(w, []models.ContextEntry{})
		}
		results, err := db.SearchContext(database, p.ID, q)
		if err != nil {
			return err
		}
		if results == nil {
			results = []models.ContextEntry{}
		}
		return writeJSON(w, results)
	}

	// Parse numeric ID
	id := 0
	for _, c := range parts[0] {
		if c >= '0' && c <= '9' {
			id = id*10 + int(c-'0')
		} else {
			break
		}
	}
	if id == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "invalid context entry ID"})
	}

	e, err := db.GetContextEntry(database, id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return writeJSON(w, map[string]string{"error": "context entry not found"})
	}
	return writeJSON(w, e)
}

// --- Spec Document ---

func handleSpecDocument(database *sql.DB, w http.ResponseWriter, _ *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}

	milestones, err := db.ListMilestones(database, p.ID)
	if err != nil {
		return err
	}

	features, err := db.ListFeatures(database, p.ID, "", "")
	if err != nil {
		return err
	}

	roadmapItems, err := db.ListRoadmapItems(database, p.ID)
	if err != nil {
		return err
	}

	discussions, err := db.ListDiscussions(database, p.ID, "", "")
	if err != nil {
		discussions = []models.Discussion{}
	}

	events, err := db.ListEvents(database, p.ID, "", "", "", 50)
	if err != nil {
		events = []models.Event{}
	}

	// Count features by status
	statusCounts := map[string]int{}
	for _, f := range features {
		statusCounts[f.Status]++
	}

	// Build executive summary
	milestoneNames := make([]string, len(milestones))
	for i, m := range milestones {
		milestoneNames[i] = m.Name
	}
	summaryMD := fmt.Sprintf("## Executive Summary\n\n**%s** is a project with %d features across %d milestones",
		p.Name, len(features), len(milestones))
	if len(milestoneNames) > 0 {
		summaryMD += fmt.Sprintf(" (%s)", strings.Join(milestoneNames, ", "))
	}
	summaryMD += fmt.Sprintf(".\n\n**Feature Status:** %d done, %d in progress, %d planning, %d blocked, %d draft\n\n",
		statusCounts["done"], statusCounts["implementing"], statusCounts["planning"],
		statusCounts["blocked"], statusCounts["draft"])
	summaryMD += fmt.Sprintf("**Roadmap Items:** %d total\n", len(roadmapItems))

	type specFeature struct {
		ID           string   `json:"id"`
		Name         string   `json:"name"`
		Status       string   `json:"status"`
		Priority     int      `json:"priority"`
		SpecMD       string   `json:"spec_md,omitempty"`
		Description  string   `json:"description,omitempty"`
		Dependencies []string `json:"dependencies,omitempty"`
	}

	type specSection struct {
		ID        string        `json:"id"`
		Title     string        `json:"title"`
		ContentMD string        `json:"content_md"`
		Level     int           `json:"level"`
		Features  []specFeature `json:"features,omitempty"`
	}

	sections := []specSection{
		{
			ID:        "executive-summary",
			Title:     "Executive Summary",
			ContentMD: summaryMD,
			Level:     1,
		},
	}

	// Group features by milestone
	featuresByMilestone := map[string][]models.Feature{}
	var unassigned []models.Feature
	for _, f := range features {
		if f.MilestoneID != "" {
			featuresByMilestone[f.MilestoneID] = append(featuresByMilestone[f.MilestoneID], f)
		} else {
			unassigned = append(unassigned, f)
		}
	}

	for i, m := range milestones {
		mFeatures := featuresByMilestone[m.ID]
		contentMD := fmt.Sprintf("## Phase %d: %s\n\n", i+1, m.Name)
		if m.Description != "" {
			contentMD += m.Description + "\n\n"
		}
		contentMD += fmt.Sprintf("**Features:** %d total, %d done\n", m.TotalFeatures, m.DoneFeatures)

		sf := make([]specFeature, len(mFeatures))
		for j, f := range mFeatures {
			sf[j] = specFeature{
				ID:           f.ID,
				Name:         f.Name,
				Status:       f.Status,
				Priority:     f.Priority,
				SpecMD:       f.Spec,
				Description:  f.Description,
				Dependencies: f.DependsOn,
			}
		}

		sections = append(sections, specSection{
			ID:        fmt.Sprintf("milestone-%s", m.ID),
			Title:     fmt.Sprintf("Phase %d: %s — %s", i+1, m.ID, m.Name),
			ContentMD: contentMD,
			Level:     1,
			Features:  sf,
		})
	}

	// Unassigned features
	if len(unassigned) > 0 {
		sf := make([]specFeature, len(unassigned))
		for j, f := range unassigned {
			sf[j] = specFeature{
				ID:           f.ID,
				Name:         f.Name,
				Status:       f.Status,
				Priority:     f.Priority,
				SpecMD:       f.Spec,
				Description:  f.Description,
				Dependencies: f.DependsOn,
			}
		}
		sections = append(sections, specSection{
			ID:        "unassigned",
			Title:     "Unassigned Features",
			ContentMD: fmt.Sprintf("## Unassigned Features\n\n%d features not yet assigned to a milestone.\n", len(unassigned)),
			Level:     1,
			Features:  sf,
		})
	}

	// Discussions section
	if len(discussions) > 0 {
		discMD := "## Active Discussions\n\n"
		for _, d := range discussions {
			discMD += fmt.Sprintf("- **%s** (%s) — %s\n", d.Title, d.Status, d.Author)
		}
		sections = append(sections, specSection{
			ID:        "discussions",
			Title:     "Active Discussions",
			ContentMD: discMD,
			Level:     1,
		})
	}

	// Recent activity
	if len(events) > 0 {
		actMD := "## Recent Activity\n\n"
		shown := events
		if len(shown) > 20 {
			shown = shown[:20]
		}
		for _, e := range shown {
			actMD += fmt.Sprintf("- [%s] %s — %s\n", e.EventType, e.Data, e.CreatedAt)
		}
		sections = append(sections, specSection{
			ID:        "recent-activity",
			Title:     "Recent Activity",
			ContentMD: actMD,
			Level:     1,
		})
	}

	result := map[string]any{
		"title":        p.Name + " — Software Specification",
		"generated_at": timeNowISO(),
		"sections":     sections,
		"stats": map[string]int{
			"total_features":      len(features),
			"done":                statusCounts["done"],
			"in_progress":         statusCounts["implementing"],
			"blocked":             statusCounts["blocked"],
			"total_milestones":    len(milestones),
			"total_roadmap_items": len(roadmapItems),
		},
	}

	return writeJSON(w, result)
}

// ── Git/VCS handlers ──

func handleGitLog(_ *sql.DB, w http.ResponseWriter, _ *http.Request) error {
	vcsType, commits, err := vcs.GetLog(20)
	if err != nil {
		return fmt.Errorf("reading git log: %w", err)
	}
	if commits == nil {
		commits = []vcs.CommitInfo{}
	}
	return writeJSON(w, map[string]any{"vcs": vcsType, "commits": commits})
}

func handleGitBranches(_ *sql.DB, w http.ResponseWriter, _ *http.Request) error {
	vcsType, branches, err := vcs.GetBranches()
	if err != nil {
		return fmt.Errorf("reading git branches: %w", err)
	}
	if branches == nil {
		branches = []vcs.BranchInfo{}
	}
	return writeJSON(w, map[string]any{"vcs": vcsType, "branches": branches})
}

func handleQueue(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	if r.Method == "POST" {
		// POST /api/queue — reclaim stale work items
		reclaimed, err := engine.ReclaimStaleWorkItems(database, 30)
		if err != nil {
			return fmt.Errorf("reclaiming stale work items: %w", err)
		}
		return writeJSON(w, map[string]any{"reclaimed": reclaimed})
	}

	queue, err := db.GetQueuedWorkItems(database)
	if err != nil {
		return fmt.Errorf("getting queue: %w", err)
	}
	if queue == nil {
		queue = []models.QueueEntry{}
	}
	stats, err := db.GetQueueStats(database)
	if err != nil {
		return fmt.Errorf("getting queue stats: %w", err)
	}
	return writeJSON(w, models.QueueResponse{Queue: queue, Stats: *stats})
}

// --- Decision log (ADR) handlers ---

func handleDecisions(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	if r.Method == "POST" {
		var body struct {
			Title        string `json:"title"`
			Status       string `json:"status"`
			Context      string `json:"context"`
			Decision     string `json:"decision"`
			Consequences string `json:"consequences"`
			FeatureID    string `json:"feature_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		if body.Title == "" {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "title is required"})
		}
		if body.Status == "" {
			body.Status = "proposed"
		}
		id := strings.ToLower(strings.ReplaceAll(body.Title, " ", "-"))
		d := &models.Decision{
			ID:           id,
			Title:        body.Title,
			Status:       body.Status,
			Context:      body.Context,
			Decision:     body.Decision,
			Consequences: body.Consequences,
			FeatureID:    body.FeatureID,
		}
		if err := db.CreateDecision(database, d); err != nil {
			return fmt.Errorf("creating decision: %w", err)
		}
		created, err := db.GetDecision(database, id)
		if err != nil {
			return fmt.Errorf("fetching created decision: %w", err)
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, created)
	}

	status := r.URL.Query().Get("status")
	decisions, err := db.ListDecisions(database, status)
	if err != nil {
		return err
	}
	if decisions == nil {
		decisions = []models.Decision{}
	}
	return writeJSON(w, decisions)
}

func handleDecisionDetail(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	id := strings.TrimPrefix(r.URL.Path, "/api/decisions/")
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "decision ID required"})
	}

	if r.Method == "PATCH" {
		var body struct {
			Title        *string `json:"title"`
			Status       *string `json:"status"`
			Context      *string `json:"context"`
			Decision     *string `json:"decision"`
			Consequences *string `json:"consequences"`
			SupersededBy *string `json:"superseded_by"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return writeJSON(w, map[string]string{"error": "invalid request body"})
		}
		updates := map[string]any{}
		if body.Title != nil {
			updates["title"] = *body.Title
		}
		if body.Status != nil {
			validStatuses := map[string]bool{
				"proposed": true, "accepted": true, "rejected": true,
				"superseded": true, "deprecated": true,
			}
			if !validStatuses[*body.Status] {
				w.WriteHeader(http.StatusBadRequest)
				return writeJSON(w, map[string]string{"error": "invalid status"})
			}
			updates["status"] = *body.Status
		}
		if body.Context != nil {
			updates["context"] = *body.Context
		}
		if body.Decision != nil {
			updates["decision"] = *body.Decision
		}
		if body.Consequences != nil {
			updates["consequences"] = *body.Consequences
		}
		if body.SupersededBy != nil {
			updates["superseded_by"] = *body.SupersededBy
		}
		if len(updates) > 0 {
			if err := db.UpdateDecision(database, id, updates); err != nil {
				return fmt.Errorf("updating decision: %w", err)
			}
		}
		d, err := db.GetDecision(database, id)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return writeJSON(w, map[string]string{"error": "decision not found"})
		}
		return writeJSON(w, d)
	}

	d, err := db.GetDecision(database, id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return writeJSON(w, map[string]string{"error": "decision not found"})
	}
	return writeJSON(w, d)
}

func handleDashboards(database *sql.DB, w http.ResponseWriter, _ *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}
	configs, err := db.ListDashboardConfigs(database, p.ID)
	if err != nil {
		return err
	}
	if configs == nil {
		configs = []models.DashboardConfig{}
	}
	return writeJSON(w, configs)
}

func handleDashboardDetail(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	id := strings.TrimPrefix(r.URL.Path, "/api/dashboards/")
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		return writeJSON(w, map[string]string{"error": "dashboard id required"})
	}
	dc, err := db.GetDashboardConfig(database, id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return writeJSON(w, map[string]string{"error": "dashboard not found"})
	}
	return writeJSON(w, dc)
}

func handleExport(database *sql.DB, entity string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			return
		}

		format := r.URL.Query().Get("format")
		if format == "" {
			format = "json"
		}

		p, err := db.GetProject(database)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck
			return
		}

		switch format {
		case "csv":
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s-%s.csv", p.Name, entity))
		case "md", "markdown":
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s-%s.md", p.Name, entity))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s-%s.json", p.Name, entity))
		}

		var exportErr error
		switch entity {
		case "features":
			features, fErr := db.ListFeatures(database, p.ID, "", "")
			if fErr != nil {
				exportErr = fErr
				break
			}
			exportErr = export.Features(features, w, format)
		case "roadmap":
			items, rErr := db.ListRoadmapItems(database, p.ID)
			if rErr != nil {
				exportErr = rErr
				break
			}
			exportErr = export.Roadmap(items, w, format)
		case "decisions":
			decisions, dErr := db.ListDecisions(database, "")
			if dErr != nil {
				exportErr = dErr
				break
			}
			exportErr = export.Decisions(decisions, w, format)
		case "all":
			if format == "csv" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "CSV format not supported for 'all' export"}) //nolint:errcheck
				return
			}
			features, _ := db.ListFeatures(database, p.ID, "", "")
			items, _ := db.ListRoadmapItems(database, p.ID)
			decisions, _ := db.ListDecisions(database, "")
			exportErr = export.All(p.Name, features, items, decisions, w, format)
		}

		if exportErr != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": exportErr.Error()}) //nolint:errcheck
		}
	}
}

func handleAPIDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, apiDocsHTML) //nolint:errcheck
}

// GenerateOpenAPISpec builds a complete OpenAPI 3.0 specification for the tillr API,
// enumerating all registered routes with their methods, summaries, and tags.
func GenerateOpenAPISpec() map[string]any {
	type apiRoute struct{ method, path, summary, desc, tag string }
	rr := []apiRoute{
		{"GET", "/api/status", "Project overview dashboard", "", "Project"},
		{"GET", "/api/stats", "Project statistics", "", "Project"},
		{"GET", "/api/stats/burndown", "Burndown chart data", "", "Project"},
		{"GET", "/api/stats/heatmap", "Activity heatmap data", "", "Project"},
		{"GET", "/api/stats/activity-heatmap", "Daily activity heatmap", "", "Project"},
		{"GET", "/api/search", "Full-text search", "Query: ?q=term", "Project"},
		{"GET", "/api/history", "Event history", "Params: ?feature, ?type, ?since, ?limit", "Project"},
		{"GET", "/api/features", "List features", "Params: ?status, ?milestone", "Features"},
		{"GET", "/api/features/{id}", "Feature details", "", "Features"},
		{"GET", "/api/tags", "List feature tags", "", "Features"},
		{"GET", "/api/milestones", "List milestones", "", "Milestones"},
		{"GET", "/api/milestones/{id}", "Milestone details", "", "Milestones"},
		{"GET", "/api/roadmap", "List roadmap items", "", "Roadmap"},
		{"PATCH", "/api/roadmap/{id}", "Update roadmap status", "", "Roadmap"},
		{"GET", "/api/cycles", "List active cycles", "", "Cycles"},
		{"GET", "/api/cycles/{id}", "Cycle details", "", "Cycles"},
		{"GET", "/api/qa/{feature-id}", "QA results", "", "QA"},
		{"POST", "/api/qa/{feature-id}", "Submit QA result", "", "QA"},
		{"GET", "/api/ideas", "List ideas", "", "Ideas"},
		{"POST", "/api/ideas", "Submit idea", "", "Ideas"},
		{"GET", "/api/ideas/{id}", "Idea details", "", "Ideas"},
		{"GET", "/api/decisions", "List decisions", "", "Decisions"},
		{"GET", "/api/decisions/{id}", "Decision details", "", "Decisions"},
		{"GET", "/api/discussions", "List discussions", "", "Discussions"},
		{"GET", "/api/discussions/{id}", "Discussion details", "", "Discussions"},
		{"GET", "/api/agents", "List agent sessions", "", "Agents"},
		{"GET", "/api/agents/{id}", "Agent details", "", "Agents"},
		{"GET", "/api/agents/coordination", "Coordination status", "", "Agents"},
		{"GET", "/api/agents/status", "Heartbeat dashboard", "", "Agents"},
		{"GET", "/api/worktrees", "List worktrees", "", "Worktrees"},
		{"GET", "/api/worktrees/{id}", "Worktree details", "", "Worktrees"},
		{"GET", "/api/git/log", "Git commit log", "", "Git"},
		{"GET", "/api/git/branches", "Git branches", "", "Git"},
		{"GET", "/api/context", "List context entries", "", "Context"},
		{"GET", "/api/context/{id}", "Context entry details", "", "Context"},
		{"GET", "/api/queue", "Work queue with stats", "", "Queue"},
		{"GET", "/api/export/features", "Export features", "?format=json|md|csv", "Export"},
		{"GET", "/api/export/roadmap", "Export roadmap", "?format=json|md|csv", "Export"},
		{"GET", "/api/export/decisions", "Export decisions", "?format=json|md|csv", "Export"},
		{"GET", "/api/export/all", "Export all data", "?format=json|md", "Export"},
		{"GET", "/api/dashboards", "List dashboards", "", "Dashboards"},
		{"GET", "/api/dashboards/{id}", "Dashboard details", "", "Dashboards"},
		{"GET", "/api/spec-document", "Aggregate spec document", "", "Spec"},
		{"GET", "/api/docs", "API docs page", "", "Docs"},
		{"GET", "/api/openapi.json", "OpenAPI 3.0 spec", "", "Docs"},
		{"GET", "/api/dependencies", "Dependency graph", "", "Dependencies"},
		{"GET", "/ws", "WebSocket updates", "", "WebSocket"},
	}
	paths := map[string]any{}
	tagSet := map[string]bool{}
	for _, r := range rr {
		tagSet[r.tag] = true
		m := strings.ToLower(r.method)
		op := map[string]any{"summary": r.summary, "tags": []string{r.tag}, "responses": map[string]any{"200": map[string]any{"description": "OK"}}}
		if r.desc != "" {
			op["description"] = r.desc
		}
		if _, ok := paths[r.path]; !ok {
			paths[r.path] = map[string]any{}
		}
		paths[r.path].(map[string]any)[m] = op
	}
	var tagList []map[string]string
	for t := range tagSet {
		tagList = append(tagList, map[string]string{"name": t})
	}
	return map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "Tillr API", "description": "REST API for tillr project management.", "version": "1.0.0"},
		"servers": []map[string]any{{"url": "http://localhost:3847", "description": "Local development server"}},
		"paths":   paths,
		"tags":    tagList,
	}
}

func handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	spec := GenerateOpenAPISpec()
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(spec)
}

const apiDocsHTML = `<!DOCTYPE html>
<html lang="en" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Tillr API Documentation</title>
<style>
  :root {
    --bg-primary: #0d1117;
    --bg-secondary: #161b22;
    --bg-tertiary: #21262d;
    --bg-card: #1c2128;
    --text-primary: #e6edf3;
    --text-secondary: #8b949e;
    --text-muted: #848d97;
    --accent: #58a6ff;
    --accent-hover: #79c0ff;
    --success: #3fb950;
    --warning: #d29922;
    --danger: #f85149;
    --purple: #bc8cff;
    --border: #30363d;
    --border-hover: #484f58;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif;
    background: linear-gradient(145deg, #0d1117 0%, #101820 50%, #0d1117 100%);
    color: var(--text-primary);
    line-height: 1.6;
    min-height: 100vh;
  }
  .container { max-width: 960px; margin: 0 auto; padding: 2rem 1.5rem; }
  h1 { font-size: 2rem; font-weight: 600; margin-bottom: 0.25rem; }
  .subtitle { color: var(--text-secondary); margin-bottom: 2rem; font-size: 0.95rem; }
  .toc {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 1.25rem 1.5rem;
    margin-bottom: 2rem;
  }
  .toc h2 { font-size: 0.85rem; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-secondary); margin-bottom: 0.75rem; }
  .toc-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 0.35rem 1.5rem; }
  .toc a { color: var(--accent); text-decoration: none; font-size: 0.85rem; }
  .toc a:hover { color: var(--accent-hover); text-decoration: underline; }
  .section { margin-bottom: 2.5rem; }
  .section-title {
    font-size: 1.15rem; font-weight: 600; color: var(--text-primary);
    border-bottom: 1px solid var(--border); padding-bottom: 0.5rem; margin-bottom: 1rem;
  }
  .endpoint {
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 8px;
    margin-bottom: 0.75rem;
    overflow: hidden;
  }
  .endpoint-header {
    display: flex; align-items: center; gap: 0.75rem;
    padding: 0.75rem 1rem; cursor: pointer; user-select: none;
  }
  .endpoint-header:hover { background: var(--bg-tertiary); }
  .method {
    display: inline-block; padding: 0.15rem 0.5rem; border-radius: 4px;
    font-size: 0.7rem; font-weight: 700;
    font-family: 'SFMono-Regular', Consolas, monospace;
    min-width: 3.5rem; text-align: center; letter-spacing: 0.03em;
  }
  .method-get { background: rgba(63,185,80,0.15); color: var(--success); border: 1px solid rgba(63,185,80,0.3); }
  .method-post { background: rgba(88,166,255,0.15); color: var(--accent); border: 1px solid rgba(88,166,255,0.3); }
  .method-patch { background: rgba(210,153,34,0.15); color: var(--warning); border: 1px solid rgba(210,153,34,0.3); }
  .method-delete { background: rgba(248,81,73,0.15); color: var(--danger); border: 1px solid rgba(248,81,73,0.3); }
  .method-ws { background: rgba(188,140,255,0.15); color: var(--purple); border: 1px solid rgba(188,140,255,0.3); }
  .path { font-family: 'SFMono-Regular', Consolas, monospace; font-size: 0.85rem; color: var(--text-primary); }
  .path-param { color: var(--warning); }
  .desc { color: var(--text-secondary); font-size: 0.8rem; margin-left: auto; text-align: right; white-space: nowrap; }
  .endpoint-body { display: none; padding: 0 1rem 1rem 1rem; border-top: 1px solid var(--border); }
  .endpoint.open .endpoint-body { display: block; }
  .endpoint-body p { color: var(--text-secondary); font-size: 0.85rem; margin: 0.75rem 0 0.5rem 0; }
  .params-table { width: 100%; border-collapse: collapse; margin: 0.5rem 0; font-size: 0.8rem; }
  .params-table th {
    text-align: left; color: var(--text-muted); font-weight: 600;
    padding: 0.35rem 0.75rem; border-bottom: 1px solid var(--border);
    font-size: 0.7rem; text-transform: uppercase; letter-spacing: 0.05em;
  }
  .params-table td { padding: 0.35rem 0.75rem; border-bottom: 1px solid rgba(48,54,61,0.5); color: var(--text-secondary); }
  .params-table code { color: var(--accent); font-size: 0.8rem; }
  .try-btn {
    display: inline-flex; align-items: center; gap: 0.35rem;
    background: rgba(88,166,255,0.1); color: var(--accent);
    border: 1px solid rgba(88,166,255,0.3); border-radius: 6px;
    padding: 0.35rem 0.85rem; font-size: 0.8rem; cursor: pointer; margin-top: 0.5rem;
    transition: background 0.15s;
  }
  .try-btn:hover { background: rgba(88,166,255,0.2); }
  .try-btn:disabled { opacity: 0.5; cursor: wait; }
  .response-area {
    margin-top: 0.5rem; background: var(--bg-primary); border: 1px solid var(--border);
    border-radius: 6px; padding: 0.75rem;
    font-family: 'SFMono-Regular', Consolas, monospace; font-size: 0.75rem;
    color: var(--text-secondary); max-height: 300px; overflow: auto;
    white-space: pre-wrap; word-break: break-word; display: none;
  }
  .response-area.visible { display: block; }
  .chevron { color: var(--text-muted); font-size: 0.7rem; transition: transform 0.15s; margin-left: -0.25rem; }
  .endpoint.open .chevron { transform: rotate(90deg); }
  @media (max-width: 640px) {
    .container { padding: 1rem; }
    .desc { display: none; }
    .toc-grid { grid-template-columns: 1fr 1fr; }
  }
</style>
</head>
<body>
<div class="container">
<h1>Tillr API</h1>
<p class="subtitle">REST API reference for the Tillr project management server. All endpoints return JSON unless noted.</p>

<div class="toc">
<h2>Sections</h2>
<div class="toc-grid">
  <a href="#project">Project</a>
  <a href="#features">Features</a>
  <a href="#milestones">Milestones</a>
  <a href="#roadmap">Roadmap</a>
  <a href="#cycles">Cycles</a>
  <a href="#qa">QA</a>
  <a href="#ideas">Ideas</a>
  <a href="#decisions">Decisions</a>
  <a href="#discussions">Discussions</a>
  <a href="#agents">Agents</a>
  <a href="#worktrees">Worktrees</a>
  <a href="#git">Git / VCS</a>
  <a href="#context">Context</a>
  <a href="#queue">Queue</a>
  <a href="#export">Export</a>
  <a href="#websocket">WebSocket</a>
</div>
</div>

<!-- ===== Project ===== -->
<div class="section" id="project">
<h2 class="section-title">Project</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/status</span>
  <span class="desc">Project overview dashboard</span>
</div>
<div class="endpoint-body">
  <p>Returns project name, feature counts by status, milestone progress, active cycles, and recent activity.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/status')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/stats</span>
  <span class="desc">Project statistics</span>
</div>
<div class="endpoint-body">
  <p>Returns aggregate project statistics including feature counts, velocity, and completion rates.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/stats')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/stats/burndown</span>
  <span class="desc">Burndown chart data</span>
</div>
<div class="endpoint-body">
  <p>Returns time-series data for burndown charts showing feature completion over time.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/stats/burndown')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/stats/heatmap</span>
  <span class="desc">Activity heatmap data</span>
</div>
<div class="endpoint-body">
  <p>Returns daily activity counts for heatmap visualization. Defaults to last 365 days.</p>
  <table class="params-table"><tr><th>Param</th><th>Type</th><th>Description</th></tr>
  <tr><td><code>days</code></td><td>int</td><td>Number of days to include (default: 365)</td></tr>
  </table>
  <button class="try-btn" onclick="tryIt(this, '/api/stats/heatmap')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/stats/activity-heatmap</span>
  <span class="desc">Daily event counts (flat array)</span>
</div>
<div class="endpoint-body">
  <p>Returns daily event counts for the last 365 days as a flat array of {date, count} objects.</p>
  <table class="params-table"><tr><th>Param</th><th>Type</th><th>Description</th></tr>
  <tr><td><code>days</code></td><td>int</td><td>Number of days to include (default: 365)</td></tr>
  </table>
  <button class="try-btn" onclick="tryIt(this, '/api/stats/activity-heatmap')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/search</span>
  <span class="desc">Full-text search</span>
</div>
<div class="endpoint-body">
  <p>Searches across all project data using FTS5. Falls back to LIKE-based search if FTS is unavailable.</p>
  <table class="params-table"><tr><th>Param</th><th>Type</th><th>Description</th></tr>
  <tr><td><code>q</code></td><td>string</td><td>Search query (required)</td></tr>
  </table>
  <button class="try-btn" onclick="tryIt(this, '/api/search?q=test')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/history</span>
  <span class="desc">Event history</span>
</div>
<div class="endpoint-body">
  <p>Returns event history with optional filtering. Supports pagination.</p>
  <table class="params-table"><tr><th>Param</th><th>Type</th><th>Description</th></tr>
  <tr><td><code>feature</code></td><td>string</td><td>Filter by feature ID</td></tr>
  <tr><td><code>type</code></td><td>string</td><td>Filter by event type</td></tr>
  <tr><td><code>since</code></td><td>string</td><td>ISO 8601 timestamp lower bound</td></tr>
  </table>
  <button class="try-btn" onclick="tryIt(this, '/api/history')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/dependencies</span>
  <span class="desc">Dependency graph</span>
</div>
<div class="endpoint-body">
  <p>Returns feature dependency graph as nodes and edges for visualization.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/dependencies')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/tags</span>
  <span class="desc">List all tags</span>
</div>
<div class="endpoint-body">
  <p>Returns all tags with their usage counts.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/tags')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/spec-document</span>
  <span class="desc">Full specification document</span>
</div>
<div class="endpoint-body">
  <p>Returns a comprehensive specification document with sections, features, and project statistics.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/spec-document')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

</div>

<!-- ===== Features ===== -->
<div class="section" id="features">
<h2 class="section-title">Features</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/features</span>
  <span class="desc">List features</span>
</div>
<div class="endpoint-body">
  <p>Returns all features. Supports filtering by status, milestone, and tags.</p>
  <table class="params-table"><tr><th>Param</th><th>Type</th><th>Description</th></tr>
  <tr><td><code>status</code></td><td>string</td><td>Filter by status</td></tr>
  <tr><td><code>milestone</code></td><td>string</td><td>Filter by milestone</td></tr>
  </table>
  <button class="try-btn" onclick="tryIt(this, '/api/features')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/features/<span class="path-param">{id}</span></span>
  <span class="desc">Feature detail</span>
</div>
<div class="endpoint-body">
  <p>Returns a single feature with its work items, cycles, scores, and dependencies.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/features/1')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/features/<span class="path-param">{id}</span>/deps</span>
  <span class="desc">Feature dependencies</span>
</div>
<div class="endpoint-body">
  <p>Returns the dependency list for a specific feature.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/features/1/deps')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-patch">PATCH</span>
  <span class="path">/api/features/<span class="path-param">{id}</span></span>
  <span class="desc">Update a feature</span>
</div>
<div class="endpoint-body">
  <p>Updates feature fields. Send a JSON body with the fields to update (name, description, status, priority, etc.).</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/features/batch</span>
  <span class="desc">Batch update features</span>
</div>
<div class="endpoint-body">
  <p>Updates multiple features in a single request. Send a JSON array of feature update objects.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/features/reorder</span>
  <span class="desc">Reorder feature priorities</span>
</div>
<div class="endpoint-body">
  <p>Reorders feature priorities. Send a JSON object with ordered feature IDs.</p>
</div>
</div>

</div>

<!-- ===== Milestones ===== -->
<div class="section" id="milestones">
<h2 class="section-title">Milestones</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/milestones</span>
  <span class="desc">List milestones</span>
</div>
<div class="endpoint-body">
  <p>Returns all milestones with progress information.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/milestones')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/milestones/<span class="path-param">{id}</span></span>
  <span class="desc">Milestone detail</span>
</div>
<div class="endpoint-body">
  <p>Returns a single milestone with its associated features and progress.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/milestones/1')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-patch">PATCH</span>
  <span class="path">/api/milestones/<span class="path-param">{id}</span></span>
  <span class="desc">Update milestone</span>
</div>
<div class="endpoint-body">
  <p>Updates milestone fields. Send a JSON body with the fields to update.</p>
</div>
</div>

</div>

<!-- ===== Roadmap ===== -->
<div class="section" id="roadmap">
<h2 class="section-title">Roadmap</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/roadmap</span>
  <span class="desc">List roadmap items</span>
</div>
<div class="endpoint-body">
  <p>Returns roadmap items. Supports filtering and sorting.</p>
  <table class="params-table"><tr><th>Param</th><th>Type</th><th>Description</th></tr>
  <tr><td><code>category</code></td><td>string</td><td>Filter by category</td></tr>
  <tr><td><code>priority</code></td><td>string</td><td>Filter by priority</td></tr>
  <tr><td><code>status</code></td><td>string</td><td>Filter by status</td></tr>
  <tr><td><code>sort</code></td><td>string</td><td>Sort field</td></tr>
  </table>
  <button class="try-btn" onclick="tryIt(this, '/api/roadmap')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-patch">PATCH</span>
  <span class="path">/api/roadmap/<span class="path-param">{id}</span></span>
  <span class="desc">Update roadmap item</span>
</div>
<div class="endpoint-body">
  <p>Updates roadmap item fields such as title, description, priority, category, or effort.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-patch">PATCH</span>
  <span class="path">/api/roadmap/<span class="path-param">{id}</span>/status</span>
  <span class="desc">Update roadmap item status</span>
</div>
<div class="endpoint-body">
  <p>Updates the status of a roadmap item.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/roadmap/reorder</span>
  <span class="desc">Reorder roadmap items</span>
</div>
<div class="endpoint-body">
  <p>Reorders roadmap item priorities. Send a JSON object with ordered item IDs.</p>
</div>
</div>

</div>

<!-- ===== Cycles ===== -->
<div class="section" id="cycles">
<h2 class="section-title">Cycles</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/cycles</span>
  <span class="desc">List active cycles</span>
</div>
<div class="endpoint-body">
  <p>Returns all active iteration cycles with their current step and progress.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/cycles')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/cycles/<span class="path-param">{id}</span></span>
  <span class="desc">Cycle detail with scores</span>
</div>
<div class="endpoint-body">
  <p>Returns a single cycle with its steps, scores, and iteration history.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/cycles/1')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/cycles/<span class="path-param">{id}</span>/scores</span>
  <span class="desc">Cycle scores</span>
</div>
<div class="endpoint-body">
  <p>Returns score history for a specific cycle.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/cycles/1/scores')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/cycles/<span class="path-param">{id}</span>/history</span>
  <span class="desc">Cycle iteration history</span>
</div>
<div class="endpoint-body">
  <p>Returns the iteration history for a cycle, showing progression through steps.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/cycles/1/history')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

</div>

<!-- ===== QA ===== -->
<div class="section" id="qa">
<h2 class="section-title">QA</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/qa/pending</span>
  <span class="desc">Pending QA items</span>
</div>
<div class="endpoint-body">
  <p>Returns features awaiting QA review.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/qa/pending')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/qa/history</span>
  <span class="desc">QA event history</span>
</div>
<div class="endpoint-body">
  <p>Returns QA-related events (approvals, rejections).</p>
  <button class="try-btn" onclick="tryIt(this, '/api/qa/history')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/qa/<span class="path-param">{id}</span>/approve</span>
  <span class="desc">Approve feature QA</span>
</div>
<div class="endpoint-body">
  <p>Approves a feature through QA. Optionally include notes in the JSON body.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/qa/<span class="path-param">{id}</span>/reject</span>
  <span class="desc">Reject feature QA</span>
</div>
<div class="endpoint-body">
  <p>Rejects a feature, sending it back to development. Include rejection notes in the JSON body.</p>
</div>
</div>

</div>

<!-- ===== Ideas ===== -->
<div class="section" id="ideas">
<h2 class="section-title">Ideas</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/ideas</span>
  <span class="desc">List ideas</span>
</div>
<div class="endpoint-body">
  <p>Returns all ideas. Use <code>view=history</code> to include enriched history data.</p>
  <table class="params-table"><tr><th>Param</th><th>Type</th><th>Description</th></tr>
  <tr><td><code>view</code></td><td>string</td><td>Set to "history" for enriched view</td></tr>
  </table>
  <button class="try-btn" onclick="tryIt(this, '/api/ideas')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/ideas/<span class="path-param">{id}</span></span>
  <span class="desc">Idea detail</span>
</div>
<div class="endpoint-body">
  <p>Returns a single idea with full details.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/ideas/1')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/ideas</span>
  <span class="desc">Create idea</span>
</div>
<div class="endpoint-body">
  <p>Creates a new idea. Send a JSON body with title, description, and source fields.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/ideas/<span class="path-param">{id}</span>/spec</span>
  <span class="desc">Set idea spec</span>
</div>
<div class="endpoint-body">
  <p>Attaches a specification to an idea. Send a JSON body with the spec content.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/ideas/<span class="path-param">{id}</span>/approve</span>
  <span class="desc">Approve idea</span>
</div>
<div class="endpoint-body">
  <p>Approves an idea, promoting it toward feature creation.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/ideas/<span class="path-param">{id}</span>/reject</span>
  <span class="desc">Reject idea</span>
</div>
<div class="endpoint-body">
  <p>Rejects an idea with optional notes.</p>
</div>
</div>

</div>

<!-- ===== Decisions ===== -->
<div class="section" id="decisions">
<h2 class="section-title">Decisions (ADRs)</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/decisions</span>
  <span class="desc">List decisions</span>
</div>
<div class="endpoint-body">
  <p>Returns all architecture decision records. Filterable by status.</p>
  <table class="params-table"><tr><th>Param</th><th>Type</th><th>Description</th></tr>
  <tr><td><code>status</code></td><td>string</td><td>Filter by status</td></tr>
  </table>
  <button class="try-btn" onclick="tryIt(this, '/api/decisions')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/decisions/<span class="path-param">{id}</span></span>
  <span class="desc">Decision detail</span>
</div>
<div class="endpoint-body">
  <p>Returns a single decision with full details.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/decisions/1')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/decisions</span>
  <span class="desc">Create decision</span>
</div>
<div class="endpoint-body">
  <p>Creates a new decision record. Send JSON body with title, context, and decision fields.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-patch">PATCH</span>
  <span class="path">/api/decisions/<span class="path-param">{id}</span></span>
  <span class="desc">Update decision</span>
</div>
<div class="endpoint-body">
  <p>Updates decision fields such as status, context, or consequences.</p>
</div>
</div>

</div>

<!-- ===== Discussions ===== -->
<div class="section" id="discussions">
<h2 class="section-title">Discussions</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/discussions</span>
  <span class="desc">List discussions</span>
</div>
<div class="endpoint-body">
  <p>Returns all discussions.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/discussions')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/discussions/<span class="path-param">{id}</span></span>
  <span class="desc">Discussion detail</span>
</div>
<div class="endpoint-body">
  <p>Returns a single discussion with all comments/replies.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/discussions/1')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/discussions</span>
  <span class="desc">Create discussion</span>
</div>
<div class="endpoint-body">
  <p>Creates a new discussion thread. Send JSON body with title, content, and optional feature link.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/discussions/<span class="path-param">{id}</span>/replies</span>
  <span class="desc">Add reply</span>
</div>
<div class="endpoint-body">
  <p>Adds a reply/comment to an existing discussion.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/discussions/<span class="path-param">{id}</span>/votes</span>
  <span class="desc">Get vote counts</span>
</div>
<div class="endpoint-body">
  <p>Returns reaction counts for a discussion.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/discussions/1/votes')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/discussions/<span class="path-param">{id}</span>/votes</span>
  <span class="desc">Add vote/reaction</span>
</div>
<div class="endpoint-body">
  <p>Adds a reaction to a discussion. Send JSON body with reaction (👍 👎 🎉 ❤️ 🤔) and voter.</p>
</div>
</div>

</div>

<!-- ===== Agents ===== -->
<div class="section" id="agents">
<h2 class="section-title">Agents</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/agents</span>
  <span class="desc">List agent sessions</span>
</div>
<div class="endpoint-body">
  <p>Returns all agent sessions with their status and linked worktrees.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/agents')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/agents/<span class="path-param">{id}</span></span>
  <span class="desc">Agent session detail</span>
</div>
<div class="endpoint-body">
  <p>Returns a single agent session with status updates and linked worktree.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/agents/1')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/agents/coordination</span>
  <span class="desc">Agent coordination status</span>
</div>
<div class="endpoint-body">
  <p>Returns coordination information for multi-agent orchestration.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/agents/coordination')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/agents</span>
  <span class="desc">Create agent session</span>
</div>
<div class="endpoint-body">
  <p>Creates a new agent session. Send JSON body with agent name and configuration.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/agents/<span class="path-param">{id}</span>/update</span>
  <span class="desc">Post agent status update</span>
</div>
<div class="endpoint-body">
  <p>Posts a status update from an agent. Used for heartbeat and progress reporting.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-patch">PATCH</span>
  <span class="path">/api/agents/<span class="path-param">{id}</span></span>
  <span class="desc">Update agent session</span>
</div>
<div class="endpoint-body">
  <p>Updates agent session fields such as status.</p>
</div>
</div>

</div>

<!-- ===== Worktrees ===== -->
<div class="section" id="worktrees">
<h2 class="section-title">Worktrees</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/worktrees</span>
  <span class="desc">List worktrees</span>
</div>
<div class="endpoint-body">
  <p>Returns all registered worktrees.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/worktrees')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/worktrees/<span class="path-param">{id}</span></span>
  <span class="desc">Worktree detail</span>
</div>
<div class="endpoint-body">
  <p>Returns a single worktree with its linked agent session.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/worktrees/1')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/worktrees</span>
  <span class="desc">Create worktree</span>
</div>
<div class="endpoint-body">
  <p>Registers a new worktree. Send JSON body with path and branch information.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/worktrees/<span class="path-param">{id}</span>/link</span>
  <span class="desc">Link agent to worktree</span>
</div>
<div class="endpoint-body">
  <p>Links an agent session to a worktree.</p>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-delete">DELETE</span>
  <span class="path">/api/worktrees/<span class="path-param">{id}</span></span>
  <span class="desc">Delete worktree</span>
</div>
<div class="endpoint-body">
  <p>Removes a worktree registration.</p>
</div>
</div>

</div>

<!-- ===== Git / VCS ===== -->
<div class="section" id="git">
<h2 class="section-title">Git / VCS</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/git/log</span>
  <span class="desc">Recent commits</span>
</div>
<div class="endpoint-body">
  <p>Returns the VCS type and the last 20 commits from the repository.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/git/log')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/git/branches</span>
  <span class="desc">List branches</span>
</div>
<div class="endpoint-body">
  <p>Returns the VCS type and list of branches in the repository.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/git/branches')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

</div>

<!-- ===== Context ===== -->
<div class="section" id="context">
<h2 class="section-title">Context</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/context</span>
  <span class="desc">List context entries</span>
</div>
<div class="endpoint-body">
  <p>Returns all context entries (shared knowledge, notes, references).</p>
  <button class="try-btn" onclick="tryIt(this, '/api/context')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/context/<span class="path-param">{id}</span></span>
  <span class="desc">Context entry detail</span>
</div>
<div class="endpoint-body">
  <p>Returns a single context entry.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/context/1')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/context/search</span>
  <span class="desc">Search context</span>
</div>
<div class="endpoint-body">
  <p>Searches context entries by query string.</p>
  <table class="params-table"><tr><th>Param</th><th>Type</th><th>Description</th></tr>
  <tr><td><code>q</code></td><td>string</td><td>Search query</td></tr>
  </table>
  <button class="try-btn" onclick="tryIt(this, '/api/context/search?q=test')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/context</span>
  <span class="desc">Create context entry</span>
</div>
<div class="endpoint-body">
  <p>Creates a new context entry. Send JSON body with key, value, and optional category.</p>
</div>
</div>

</div>

<!-- ===== Queue ===== -->
<div class="section" id="queue">
<h2 class="section-title">Queue</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/queue</span>
  <span class="desc">Work queue status</span>
</div>
<div class="endpoint-body">
  <p>Returns queued work items with queue statistics.</p>
  <button class="try-btn" onclick="tryIt(this, '/api/queue')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-post">POST</span>
  <span class="path">/api/queue</span>
  <span class="desc">Reclaim stale items</span>
</div>
<div class="endpoint-body">
  <p>Reclaims stale work items that were abandoned by failed agents.</p>
</div>
</div>

</div>

<!-- ===== Export ===== -->
<div class="section" id="export">
<h2 class="section-title">Export</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/export/features</span>
  <span class="desc">Export features</span>
</div>
<div class="endpoint-body">
  <p>Exports feature data. Supports multiple formats.</p>
  <table class="params-table"><tr><th>Param</th><th>Type</th><th>Description</th></tr>
  <tr><td><code>format</code></td><td>string</td><td>Export format: json, csv, or markdown (default: json)</td></tr>
  </table>
  <button class="try-btn" onclick="tryIt(this, '/api/export/features')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/export/roadmap</span>
  <span class="desc">Export roadmap</span>
</div>
<div class="endpoint-body">
  <p>Exports roadmap data in the specified format.</p>
  <table class="params-table"><tr><th>Param</th><th>Type</th><th>Description</th></tr>
  <tr><td><code>format</code></td><td>string</td><td>Export format: json, csv, or markdown (default: json)</td></tr>
  </table>
  <button class="try-btn" onclick="tryIt(this, '/api/export/roadmap')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/export/decisions</span>
  <span class="desc">Export decisions</span>
</div>
<div class="endpoint-body">
  <p>Exports decision records in the specified format.</p>
  <table class="params-table"><tr><th>Param</th><th>Type</th><th>Description</th></tr>
  <tr><td><code>format</code></td><td>string</td><td>Export format: json, csv, or markdown (default: json)</td></tr>
  </table>
  <button class="try-btn" onclick="tryIt(this, '/api/export/decisions')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/export/all</span>
  <span class="desc">Export all data</span>
</div>
<div class="endpoint-body">
  <p>Exports all project data (features, roadmap, decisions) in a combined format.</p>
  <table class="params-table"><tr><th>Param</th><th>Type</th><th>Description</th></tr>
  <tr><td><code>format</code></td><td>string</td><td>Export format: json, csv, or markdown (default: json)</td></tr>
  </table>
  <button class="try-btn" onclick="tryIt(this, '/api/export/all')">&#9654; Try it</button>
  <pre class="response-area"></pre>
</div>
</div>

</div>

<!-- ===== WebSocket ===== -->
<div class="section" id="websocket">
<h2 class="section-title">WebSocket</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-ws">WS</span>
  <span class="path">/ws</span>
  <span class="desc">Live updates</span>
</div>
<div class="endpoint-body">
  <p>WebSocket endpoint for real-time updates. The server broadcasts a <code>{"type":"refresh"}</code> message whenever the database changes. Clients should reconnect automatically on disconnect.</p>
  <table class="params-table"><tr><th>Direction</th><th>Format</th><th>Description</th></tr>
  <tr><td>Server &#8594; Client</td><td><code>{"type":"refresh"}</code></td><td>Sent when database file changes (via fsnotify watcher)</td></tr>
  <tr><td>Client &#8594; Server</td><td>any</td><td>Messages are read to keep the connection alive (pong handling)</td></tr>
  </table>
</div>
</div>

</div>

<!-- ===== Meta ===== -->
<div class="section">
<h2 class="section-title">Meta</h2>

<div class="endpoint">
<div class="endpoint-header" onclick="toggle(this)">
  <span class="chevron">&#9654;</span>
  <span class="method method-get">GET</span>
  <span class="path">/api/docs</span>
  <span class="desc">This page</span>
</div>
<div class="endpoint-body">
  <p>Returns this API documentation page.</p>
</div>
</div>

</div>

</div>

<script>
function toggle(header) {
  header.parentElement.classList.toggle('open');
}

function tryIt(btn, url) {
  var area = btn.nextElementSibling;
  if (area.classList.contains('visible')) {
    area.classList.remove('visible');
    area.textContent = '';
    return;
  }
  btn.disabled = true;
  btn.textContent = '\u23F3 Loading...';
  fetch(url)
    .then(function(r) {
      var status = r.status + ' ' + r.statusText;
      return r.text().then(function(text) {
        try {
          var json = JSON.parse(text);
          return status + '\n\n' + JSON.stringify(json, null, 2);
        } catch(e) {
          return status + '\n\n' + text;
        }
      });
    })
    .catch(function(err) {
      return 'Error: ' + err.message;
    })
    .then(function(result) {
      area.textContent = result;
      area.classList.add('visible');
      btn.disabled = false;
      btn.textContent = '\u25B6 Try it';
    });
}
</script>
</body>
</html>`

// --- Notification Handlers ---

func handleNotifications(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}

	switch r.Method {
	case http.MethodGet:
		recipient := r.URL.Query().Get("recipient")
		unreadOnly := r.URL.Query().Get("unread") == "true"
		notifications, err := db.ListNotifications(database, p.ID, recipient, unreadOnly, 100)
		if err != nil {
			return err
		}
		if notifications == nil {
			notifications = []models.Notification{}
		}
		unread, _ := db.CountUnreadNotifications(database, p.ID, recipient)
		return writeJSON(w, map[string]any{
			"notifications": notifications,
			"unread_count":  unread,
		})
	case http.MethodPost:
		var n models.Notification
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			return fmt.Errorf("invalid request body: %w", err)
		}
		n.ProjectID = p.ID
		if err := db.CreateNotification(database, &n); err != nil {
			return fmt.Errorf("creating notification: %w", err)
		}
		return writeJSON(w, n)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return nil
	}
}

func handleNotificationAction(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	p, err := db.GetProject(database)
	if err != nil {
		return err
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/notifications/")

	if path == "clear" && r.Method == http.MethodPost {
		recipient := r.URL.Query().Get("recipient")
		if err := db.ClearNotifications(database, p.ID, recipient); err != nil {
			return err
		}
		return writeJSON(w, map[string]string{"status": "cleared"})
	}

	// /api/notifications/<id>/read
	parts := strings.Split(path, "/")
	if len(parts) >= 1 {
		id := 0
		_, _ = fmt.Sscanf(parts[0], "%d", &id)
		if id == 0 {
			http.Error(w, "invalid notification ID", http.StatusBadRequest)
			return nil
		}

		if len(parts) >= 2 && parts[1] == "read" && r.Method == http.MethodPost {
			if err := db.MarkNotificationRead(database, id); err != nil {
				return err
			}
			return writeJSON(w, map[string]any{"id": id, "read": true})
		}
	}

	http.Error(w, "Not found", http.StatusNotFound)
	return nil
}

// --- Workstream handlers ---

func handleWorkstreams(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	switch r.Method {
	case http.MethodGet:
		status := r.URL.Query().Get("status")
		if status == "" {
			status = "active"
		}
		projectID := r.URL.Query().Get("project_id")
		ws, err := db.ListWorkstreams(database, projectID, status)
		if err != nil {
			return err
		}
		if ws == nil {
			ws = []models.Workstream{}
		}
		return writeJSON(w, ws)

	case http.MethodPost:
		var body struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			ParentID    string `json:"parent_id"`
			Tags        string `json:"tags"`
			ProjectID   string `json:"project_id"`
			SortOrder   int    `json:"sort_order"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		if body.Name == "" {
			http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
			return nil
		}
		id := body.ID
		if id == "" {
			id = slugify(body.Name)
		}
		ws := &models.Workstream{
			ID:          id,
			ProjectID:   body.ProjectID,
			ParentID:    body.ParentID,
			Name:        body.Name,
			Description: body.Description,
			Status:      "active",
			Tags:        body.Tags,
			SortOrder:   body.SortOrder,
		}
		if err := db.CreateWorkstream(database, ws); err != nil {
			return fmt.Errorf("creating workstream: %w", err)
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, ws)
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	return nil
}

func handleWorkstreamDetail(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
	// Parse path: /api/workstreams/{id}[/notes[/{nid}]][/links[/{lid}]]
	path := strings.TrimPrefix(r.URL.Path, "/api/workstreams/")
	parts := strings.SplitN(path, "/", 3)
	id := parts[0]

	if len(parts) >= 2 && parts[1] == "notes" {
		return handleWorkstreamNotes(database, w, r, id, parts)
	}
	if len(parts) >= 2 && parts[1] == "links" {
		return handleWorkstreamLinks(database, w, r, id, parts)
	}
	if len(parts) >= 2 && parts[1] == "features" && r.Method == "GET" {
		features, err := db.ListWorkstreamFeatures(database, id)
		if err != nil {
			return err
		}
		if features == nil {
			features = []models.WorkstreamFeature{}
		}
		return writeJSON(w, features)
	}

	switch r.Method {
	case http.MethodGet:
		detail, err := db.GetWorkstreamDetail(database, id)
		if err != nil {
			http.Error(w, `{"error":"workstream not found"}`, http.StatusNotFound)
			return nil
		}
		return writeJSON(w, detail)

	case http.MethodPatch:
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		allowed := map[string]bool{"name": true, "description": true, "status": true, "tags": true, "parent_id": true, "sort_order": true}
		updates := make(map[string]any)
		for k, v := range body {
			if allowed[k] {
				updates[k] = v
			}
		}
		if err := db.UpdateWorkstream(database, id, updates); err != nil {
			return err
		}
		ws, _ := db.GetWorkstream(database, id)
		return writeJSON(w, ws)

	case http.MethodDelete:
		if err := db.ArchiveWorkstream(database, id); err != nil {
			return err
		}
		return writeJSON(w, map[string]string{"archived": id})
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	return nil
}

func handleWorkstreamNotes(database *sql.DB, w http.ResponseWriter, r *http.Request, wsID string, parts []string) error {
	switch r.Method {
	case http.MethodGet:
		notes, err := db.ListWorkstreamNotes(database, wsID)
		if err != nil {
			return err
		}
		if notes == nil {
			notes = []models.WorkstreamNote{}
		}
		return writeJSON(w, notes)

	case http.MethodPost:
		var body struct {
			Content  string `json:"content"`
			NoteType string `json:"note_type"`
			Source   string `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		if body.NoteType == "" {
			body.NoteType = "note"
		}
		n := &models.WorkstreamNote{
			WorkstreamID: wsID,
			Content:      body.Content,
			NoteType:     body.NoteType,
			Source:       body.Source,
		}
		if err := db.CreateWorkstreamNote(database, n); err != nil {
			return err
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, n)

	case http.MethodPatch:
		if len(parts) < 3 {
			http.Error(w, "note ID required", http.StatusBadRequest)
			return nil
		}
		noteID := 0
		_, _ = fmt.Sscanf(parts[2], "%d", &noteID)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		allowed := map[string]bool{"content": true, "resolved": true, "note_type": true}
		updates := make(map[string]any)
		for k, v := range body {
			if allowed[k] {
				updates[k] = v
			}
		}
		return db.UpdateWorkstreamNote(database, noteID, updates)

	case http.MethodDelete:
		if len(parts) < 3 {
			http.Error(w, "note ID required", http.StatusBadRequest)
			return nil
		}
		noteID := 0
		_, _ = fmt.Sscanf(parts[2], "%d", &noteID)
		return db.DeleteWorkstreamNote(database, noteID)
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	return nil
}

func handleWorkstreamLinks(database *sql.DB, w http.ResponseWriter, r *http.Request, wsID string, parts []string) error {
	switch r.Method {
	case http.MethodGet:
		links, err := db.ListWorkstreamLinks(database, wsID)
		if err != nil {
			return err
		}
		if links == nil {
			links = []models.WorkstreamLink{}
		}
		return writeJSON(w, links)

	case http.MethodPost:
		var body struct {
			LinkType  string `json:"link_type"`
			TargetID  string `json:"target_id"`
			TargetURL string `json:"target_url"`
			Label     string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		l := &models.WorkstreamLink{
			WorkstreamID: wsID,
			LinkType:     body.LinkType,
			TargetID:     body.TargetID,
			TargetURL:    body.TargetURL,
			Label:        body.Label,
		}
		if err := db.CreateWorkstreamLink(database, l); err != nil {
			return err
		}
		w.WriteHeader(http.StatusCreated)
		return writeJSON(w, l)

	case http.MethodDelete:
		if len(parts) < 3 {
			http.Error(w, "link ID required", http.StatusBadRequest)
			return nil
		}
		linkID := 0
		_, _ = fmt.Sscanf(parts[2], "%d", &linkID)
		return db.DeleteWorkstreamLink(database, linkID)
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	return nil
}

// slugify creates a URL-safe slug from a name.
func slugify(name string) string {
	s := strings.ToLower(name)
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		if r == ' ' || r == '-' || r == '_' {
			return '-'
		}
		return -1
	}, s)
	// Collapse multiple dashes
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}
