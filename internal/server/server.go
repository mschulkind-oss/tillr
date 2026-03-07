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

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/models"
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
func StartWithDBPath(database *sql.DB, port int, dbPath string) error {
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

	hub := newHub()
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/status", apiHandler(database, handleStatus))
	mux.HandleFunc("/api/features", apiHandler(database, handleFeatures))
	mux.HandleFunc("/api/features/", apiHandler(database, handleFeatures))
	mux.HandleFunc("/api/milestones", apiHandler(database, handleMilestones))
	mux.HandleFunc("/api/roadmap", apiHandler(database, handleRoadmap))
	mux.HandleFunc("/api/roadmap/", apiHandler(database, handleRoadmapStatus))
	mux.HandleFunc("/api/cycles", apiHandler(database, handleCycles))
	mux.HandleFunc("/api/cycles/", apiHandler(database, handleCycles))
	mux.HandleFunc("/api/history", apiHandler(database, handleHistory))
	mux.HandleFunc("/api/search", apiHandler(database, handleSearch))
	mux.HandleFunc("/api/stats", apiHandler(database, handleStats))
	mux.HandleFunc("/api/stats/burndown", apiHandler(database, handleStatsBurndown))
	mux.HandleFunc("/api/qa/", apiHandler(database, handleQA))
	mux.HandleFunc("/api/discussions", apiHandler(database, handleDiscussions))
	mux.HandleFunc("/api/discussions/", apiHandler(database, handleDiscussionDetail))
	mux.HandleFunc("/api/dependencies", apiHandler(database, handleDependencies))

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
		// Prevent caching of static assets during development
		if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}
		fileServer.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf(":%d", port)

	// Watch DB file for changes and broadcast to WebSocket clients
	if dbPath != "" {
		go watchDBFile(dbPath, hub)
	}

	// Wrap mux with panic recovery
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rv := recover(); rv != nil {
				log.Printf("PANIC recovered in %s %s: %v", r.Method, r.URL.Path, rv)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		mux.ServeHTTP(w, r)
	})

	return http.ListenAndServe(addr, handler)
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

func apiHandler(database *sql.DB, fn apiFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
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
		// Check for /api/features/{id}/deps endpoint
		if rest, found := strings.CutSuffix(id, "/deps"); found && rest != "" {
			return handleFeatureDeps(database, w, rest)
		}

		// Handle PATCH for status updates
		if r.Method == "PATCH" {
			var body struct {
				Status   string `json:"status"`
				Priority int    `json:"priority"`
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
				if err := db.UpdateFeature(database, id, map[string]any{"status": body.Status}); err != nil {
					return err
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
			var steps []string
			for _, ct := range models.CycleTypes {
				if ct.Name == cycle.CycleType {
					steps = ct.Steps
					break
				}
			}
			if steps == nil {
				steps = []string{}
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
