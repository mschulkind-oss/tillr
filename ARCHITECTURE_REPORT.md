# Lifecycle Project Architecture Report
## For FTS5 Search Enhancement & Export Formats Implementation

---

## 1. DATABASE LAYER (`/workspace/internal/db/db.go`)

### Latest Migration Number
**Migration 13** (as of last update) — Decision log (ADRs)

### Migrations Pattern
The project uses a **versioned migration system** stored in a `schema_version` table:

```go
// Migration execution flow:
1. CreateTable schema_version on first run (line 34-37)
2. Query MAX(version) from schema_version (line 43-47)
3. For each migration in migrations[] slice (line 49-63):
   - If migration index > current version:
     - Execute SQL
     - Handle "duplicate column" errors (idempotent ADD COLUMN)
     - INSERT version number into schema_version
```

**Key characteristics:**
- Migrations array is `[]string` (line 68)
- Each migration is a complete SQL block (may contain multiple statements)
- Uses comments to document migration purpose
- Idempotent for ALTER TABLE ADD COLUMN operations
- SQLite WAL mode enabled: `_journal_mode=WAL&_busy_timeout=5000` (line 20)

### Current Schema for Key Tables

#### **features** table (Migration 1)
```sql
CREATE TABLE features (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id),
  milestone_id TEXT REFERENCES milestones(id),
  name TEXT NOT NULL,
  description TEXT,
  status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','planning','implementing','agent-qa','human-qa','done','blocked')),
  priority INTEGER NOT NULL DEFAULT 0,
  assigned_cycle TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Migration 5 additions:
spec TEXT NOT NULL DEFAULT '';
roadmap_item_id TEXT NOT NULL DEFAULT '' REFERENCES roadmap_items(id);

-- Migration 11 addition:
previous_status TEXT DEFAULT '';

-- Indices:
CREATE INDEX idx_features_project ON features(project_id);
CREATE INDEX idx_features_milestone ON features(milestone_id);
CREATE INDEX idx_features_status ON features(status);
```

#### **roadmap_items** table (Migration 1)
```sql
CREATE TABLE roadmap_items (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id),
  title TEXT NOT NULL,
  description TEXT,
  category TEXT,
  priority TEXT NOT NULL DEFAULT 'medium' CHECK(priority IN ('critical','high','medium','low','nice-to-have')),
  status TEXT NOT NULL DEFAULT 'proposed' CHECK(status IN ('proposed','accepted','in-progress','done','deferred','rejected')),
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Migration 3 addition:
effort TEXT NOT NULL DEFAULT '' CHECK(effort IN ('', 'xs', 's', 'm', 'l', 'xl'));

-- Index:
CREATE INDEX idx_roadmap_project ON roadmap_items(project_id);
```

#### **idea_queue** table (Migration 8)
```sql
CREATE TABLE idea_queue (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id TEXT NOT NULL REFERENCES projects(id),
  title TEXT NOT NULL,
  raw_input TEXT NOT NULL,
  idea_type TEXT NOT NULL DEFAULT 'feature' CHECK(idea_type IN ('feature','bug','feedback')),
  status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','spec-ready','approved','rejected','implementing','done')),
  spec_md TEXT,
  auto_implement INTEGER NOT NULL DEFAULT 0,
  submitted_by TEXT DEFAULT 'human',
  assigned_agent TEXT,
  feature_id TEXT REFERENCES features(id),
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Index:
CREATE INDEX idx_idea_queue_project ON idea_queue(project_id);
CREATE INDEX idx_idea_queue_status ON idea_queue(status);
```

---

## 2. DATABASE QUERIES (`/workspace/internal/db/queries.go`)

### Existing Search Functions
**Only LIKE-based search exists** — no FTS5 yet!

```go
// SearchEvents (line 863) - Uses LIKE on data column
func SearchEvents(db *sql.DB, projectID, query string) ([]models.Event, error) {
  q := `SELECT ... FROM events WHERE project_id = ? AND data LIKE ? ORDER BY created_at DESC LIMIT 50`
  rows, err := db.Query(q, projectID, "%"+query+"%")
}

// SearchContext (line 1807) - Uses LIKE on title and content_md
func SearchContext(db *sql.DB, projectID, query string) ([]models.ContextEntry, error) {
  pattern := "%" + query + "%"
  rows, err := db.Query(`SELECT ... FROM context_entries 
    WHERE project_id = ? AND (title LIKE ? OR content_md LIKE ?)
    ORDER BY created_at DESC`, projectID, pattern, pattern)
}
```

### Feature Query Functions
- **GetFeature(id)** (line 88) — Returns single feature with milestone name, work items, cycles (from server)
- **ListFeatures(projectID, status, milestoneID)** (line 107) — Returns all features with optional filters
  - Bulk-loads dependencies via feature_deps table (line 144-161)
  - Returns in priority DESC order
- **UpdateFeature(id, updates map)** (line 166) — Generic map-based update
- **DeleteFeature(id)** (line 185) — Cascading delete (deps, work items, QA results, heartbeats)
- **FeatureCounts(projectID)** (line 1001) — Returns map[status]count

### Roadmap Query Functions
- **GetRoadmapItem(id)** (line 406) — Single item
- **ListRoadmapItems(projectID)** (line 417) — All items ordered by sort_order, created_at
- **ListRoadmapItemsFiltered(projectID, category, priority, status, sort)** (line 436) — With full filtering and custom sort logic
- **UpdateRoadmapItem(id, updates)** (line 490) — Generic map update
- **UpdateRoadmapItemStatus(id, status)** (line 509) — Specific status update
- **GetRoadmapStats(projectID)** (line 619) — Aggregated counts by priority/category/status

### Idea Queue Query Functions
- **InsertIdea()** (line 1646) → **ListIdeas(projectID, status, ideaType)** (line 1679)
- **GetIdea(id)** (line 1662)
- **GetNextIdeaForSpec(projectID)** (line 1731) — Pending ideas, oldest first
- **UpdateIdeaStatus(id, status)** (line 1716)
- **SetIdeaSpec(id, specMD)** (line 1721)
- **ApproveIdea(id, featureID)** (line 1726)

### Result Return Pattern
**Consistent pattern used:**
```go
// 1. Prepare query + args
q := `SELECT ... FROM table WHERE ...`
args := []any{...}

// 2. Query and defer close
rows, err := db.Query(q, args...)
if err != nil { return nil, err }
defer rows.Close()

// 3. Scan into slice of structs
var out []TypeName
for rows.Next() {
  var item TypeName
  if err := rows.Scan(&item.Field1, &item.Field2, ...); err != nil {
    return nil, err
  }
  out = append(out, item)
}

// 4. Return with error check
return out, rows.Err()
```

---

## 3. SERVER LAYER (`/workspace/internal/server/server.go`)

### Route Registration Pattern
All routes registered in `StartWithDBPath()` function (line 72):

```go
// Pattern: mux.HandleFunc(path, apiHandler(database, handlerFunc))
mux.HandleFunc("/api/status", apiHandler(database, handleStatus))
mux.HandleFunc("/api/features", apiHandler(database, handleFeatures))
mux.HandleFunc("/api/features/", apiHandler(database, handleFeatures))
// ... 40+ routes registered

// WebSocket endpoint
mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
  conn, err := upgrader.Upgrade(w, r, nil)
  // Keep connection alive, read pongs until disconnect
})

// Static assets (SPA)
mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
  // SPA routing: serve index.html for non-file paths
})
```

### Existing `/api/search` Endpoint (line 662)
```go
func handleSearch(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
  p, err := db.GetProject(database)
  if err != nil { return err }
  
  q := r.URL.Query().Get("q")
  if q == "" {
    return writeJSON(w, []models.Event{})
  }
  
  events, err := db.SearchEvents(database, p.ID, q)  // ← Uses SearchEvents (LIKE)
  if err != nil { return err }
  
  return writeJSON(w, events)
}
```

**Current limitations:**
- Only searches Event data (LIKE pattern)
- Returns ONLY Event objects
- Doesn't search features, roadmap, ideas, context, discussions
- No scoring/ranking

### JSON Response Pattern
**Helper function** (line 263):
```go
func writeJSON(w http.ResponseWriter, v any) error {
  enc := json.NewEncoder(w)
  enc.SetIndent("", "  ")  // Pretty-printed JSON
  return enc.Encode(v)
}
```

**Error responses** (line 248-250):
```go
w.WriteHeader(http.StatusInternalServerError)
json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
```

### WebSocket & Broadcast Pattern (lines 29-64)
```go
type wsHub struct {
  mu      sync.Mutex
  clients map[*websocket.Conn]bool
}

// Broadcast to all connected clients:
func (h *wsHub) broadcast(msg []byte) {
  h.mu.Lock()
  defer h.mu.Unlock()
  for conn := range h.clients {
    if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
      conn.Close()
      delete(h.clients, conn)
    }
  }
}

// DB file watcher triggers broadcast (line 200-235):
hub.broadcast([]byte(`{"type":"refresh"}`))
```

### Existing Export Endpoints
**NO server export endpoints currently exist.** Only CLI export commands (roadmap).

---

## 4. CLI STRUCTURE (`/workspace/internal/cli/`)

### All CLI Files
```
root.go           ← Command registration (rootCmd)
features.go       ← Feature commands
roadmap.go        ← Roadmap commands (includes export)
milestone.go      ← Milestone commands
cycles.go         ← Cycle iteration commands
qa.go             ← QA approve/reject
discussions.go    ← Discussion/RFC commands
ideas.go          ← Idea queue commands
context.go        ← Context/notes commands
agent.go          ← Agent session commands
worktree.go       ← Git worktree commands
git.go            ← VCS/git commands
queue.go          ← Work queue commands
decisions.go      ← Decision log (ADR) commands
history.go        ← Event history commands
search.go         ← Search command (uses SearchEvents)
onboard.go        ← Project onboarding
serve.go          ← Start web server
...and more
```

### Command Registration Pattern (root.go, lines 91-124)
```go
var rootCmd = &cobra.Command{
  Use: "lifecycle",
  // Long help text with command examples
}

func init() {
  // Global flag
  rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
  
  // Register all subcommands
  rootCmd.AddCommand(featureCmd)
  rootCmd.AddCommand(roadmapCmd)
  rootCmd.AddCommand(cycleCmd)
  // ... ~20 commands
}

func Execute() {
  if err := rootCmd.Execute(); err != nil {
    fmt.Fprintln(os.Stderr, err)
    os.Exit(1)
  }
}
```

### Subcommand Pattern (features.go, lines 12-43)
```go
var featureCmd = &cobra.Command{
  Use: "feature",
  Short: "Manage features",
}

func init() {
  featureCmd.AddCommand(featureAddCmd)
  featureCmd.AddCommand(featureListCmd)
  featureCmd.AddCommand(featureShowCmd)
  featureCmd.AddCommand(featureEditCmd)
  featureCmd.AddCommand(featureRemoveCmd)
  
  // Command-specific flags
  featureAddCmd.Flags().String("milestone", "", "Assign to milestone")
  featureAddCmd.Flags().Int("priority", 0, "Priority")
  featureAddCmd.Flags().StringSlice("depends-on", nil, "Dependencies")
  featureAddCmd.Flags().String("description", "", "Description")
  featureAddCmd.Flags().String("spec", "", "Spec/acceptance criteria")
  featureAddCmd.Flags().String("status", "draft", "Initial status")
}

var featureAddCmd = &cobra.Command{
  Use: "add <name>",
  Short: "Add a new feature",
  Args: cobra.ExactArgs(1),
  Example: `  # Add a new feature
  lifecycle feature add "User Auth" --description "..." --priority 8`,
  RunE: func(cmd *cobra.Command, args []string) error {
    database, _, err := openDB()
    if err != nil { return err }
    defer database.Close()
    
    p, err := db.GetProject(database)
    if err != nil { return err }
    
    milestone, _ := cmd.Flags().GetString("milestone")
    priority, _ := cmd.Flags().GetInt("priority")
    // ... get other flags
    
    f, err := engine.AddFeature(database, p.ID, args[0], desc, spec, milestone, priority, deps, roadmapItem)
    if err != nil { return err }
    
    if jsonOutput {
      return printJSON(f)
    }
    fmt.Printf("✓ Added feature %q (id: %s)\n", f.Name, f.ID)
    return nil
  },
}
```

### Format Flags & Export Pattern (roadmap.go, lines 29, 44)
**Show command:**
```go
roadmapShowCmd.Flags().String("format", "table", "Output format (table, json, markdown)")
```

**Export command:**
```go
roadmapExportCmd.Flags().String("format", "markdown", "Export format (markdown, json)")

// Implementation (line 243-270):
var roadmapExportCmd = &cobra.Command{
  Use: "export",
  RunE: func(cmd *cobra.Command, args []string) error {
    // Get data
    items, err := db.ListRoadmapItems(database, p.ID)
    
    format, _ := cmd.Flags().GetString("format")
    if format == "json" {
      return printRoadmapJSON(p.Name, items)
    }
    return printRoadmapMarkdown(p.Name, items)
  },
}
```

### Export Implementation Examples (roadmap.go, lines 357-510)

**Markdown Export:**
```go
func printRoadmapMarkdown(projectName string, items []models.RoadmapItem) error {
  fmt.Printf("# 🗺️ Project Roadmap — %s\n\n", projectName)
  fmt.Printf("*Generated: %s*\n\n", time.Now().Format("January 2, 2006"))
  
  // Summary stats table
  fmt.Println("| Metric | Count |")
  // ... count by priority, status, category
  
  // Group by priority and output each group
  groups := map[string][]models.RoadmapItem{}
  for _, r := range items {
    groups[r.Priority] = append(groups[r.Priority], r)
  }
  
  for _, pri := range priorities {
    ritems := groups[pri]
    fmt.Printf("## %s %s\n\n", priorityEmoji(pri), priorityLabel(pri))
    for _, r := range ritems {
      fmt.Printf("### %d. %s\n\n", itemNum, r.Title)
      fmt.Printf("- **Category:** %s\n", r.Category)
      fmt.Printf("- **Status:** %s\n", titleCase(r.Status))
      // ... more fields
    }
  }
  
  // Category index
  // ...
  
  return nil
}
```

**JSON Export:**
```go
func printRoadmapJSON(projectName string, items []models.RoadmapItem) error {
  // Aggregate stats
  priorityCounts := map[string]int{}
  statusCounts := map[string]int{}
  
  export := struct {
    Project    string               `json:"project"`
    Generated  string               `json:"generated"`
    TotalItems int                  `json:"total_items"`
    ByPriority map[string]int       `json:"by_priority"`
    ByStatus   map[string]int       `json:"by_status"`
    ByCategory map[string]int       `json:"by_category"`
    Items      []models.RoadmapItem `json:"items"`
  }{}
  
  enc := json.NewEncoder(os.Stdout)
  enc.SetIndent("", "  ")
  return enc.Encode(export)
}
```

### Global Helper Functions
```go
func openDB() (*sql.DB, *config.Config, error)  // Open DB
func printJSON(v any) error                      // Pretty-print JSON to stdout
```

---

## 5. MODELS (`/workspace/internal/models/models.go`)

### Feature Struct (lines 26-45)
```go
type Feature struct {
  ID            string `json:"id"`
  ProjectID     string `json:"project_id"`
  MilestoneID   string `json:"milestone_id,omitempty"`
  Name          string `json:"name"`
  Description   string `json:"description,omitempty"`
  Spec          string `json:"spec,omitempty"`
  Status        string `json:"status"`
  Priority      int    `json:"priority"`
  AssignedCycle string `json:"assigned_cycle,omitempty"`
  RoadmapItemID string `json:"roadmap_item_id,omitempty"`
  CreatedAt     string `json:"created_at"`
  UpdatedAt     string `json:"updated_at"`
  
  // Computed fields
  PreviousStatus string   `json:"previous_status,omitempty"`
  DependsOn      []string `json:"depends_on,omitempty"`
  MilestoneName  string   `json:"milestone_name,omitempty"`
}
```

### RoadmapItem Struct (lines 85-97)
```go
type RoadmapItem struct {
  ID          string `json:"id"`
  ProjectID   string `json:"project_id"`
  Title       string `json:"title"`
  Description string `json:"description,omitempty"`
  Category    string `json:"category,omitempty"`
  Priority    string `json:"priority"`  // critical, high, medium, low, nice-to-have
  Status      string `json:"status"`    // proposed, accepted, in-progress, done, deferred, rejected
  Effort      string `json:"effort"`    // xs, s, m, l, xl
  SortOrder   int    `json:"sort_order"`
  CreatedAt   string `json:"created_at"`
  UpdatedAt   string `json:"updated_at"`
}
```

### IdeaQueueItem Struct (lines 235-249)
```go
type IdeaQueueItem struct {
  ID            int    `json:"id"`
  ProjectID     string `json:"project_id"`
  Title         string `json:"title"`
  RawInput      string `json:"raw_input"`
  IdeaType      string `json:"idea_type"`  // feature, bug, feedback
  Status        string `json:"status"`
  SpecMD        string `json:"spec_md,omitempty"`
  AutoImplement bool   `json:"auto_implement"`
  SubmittedBy   string `json:"submitted_by"`
  AssignedAgent string `json:"assigned_agent,omitempty"`
  FeatureID     string `json:"feature_id,omitempty"`
  CreatedAt     string `json:"created_at"`
  UpdatedAt     string `json:"updated_at"`
}
```

### Other Key Structs for Search Results
```go
type Event struct {
  ID        int    `json:"id"`
  ProjectID string `json:"project_id"`
  FeatureID string `json:"feature_id,omitempty"`
  EventType string `json:"event_type"`
  Data      string `json:"data,omitempty"`
  CreatedAt string `json:"created_at"`
}

type ContextEntry struct {
  ID          int    `json:"id"`
  ProjectID   string `json:"project_id"`
  FeatureID   string `json:"feature_id,omitempty"`
  ContextType string `json:"context_type"`
  Title       string `json:"title"`
  ContentMD   string `json:"content_md"`
  Author      string `json:"author"`
  Tags        string `json:"tags,omitempty"`
  CreatedAt   string `json:"created_at"`
}

type Discussion struct {
  ID        int    `json:"id"`
  ProjectID string `json:"project_id"`
  FeatureID string `json:"feature_id,omitempty"`
  Title     string `json:"title"`
  Body      string `json:"body,omitempty"`
  Status    string `json:"status"`
  Author    string `json:"author"`
  CreatedAt string `json:"created_at"`
  UpdatedAt string `json:"updated_at"`
  
  CommentCount int                 `json:"comment_count,omitempty"`
  Comments     []DiscussionComment `json:"comments,omitempty"`
}
```

---

## 6. WEB TEMPLATES (`/workspace/web/templates/`)

**Status:** Templates directory is **EMPTY** — likely using a React/Vue SPA served from `/workspace/web/assets`

---

## 7. EXPORT CODE STATUS

**No `/workspace/internal/export/` directory exists.**

**Export code currently located in:**
- `/workspace/internal/cli/roadmap.go` - `printRoadmapMarkdown()` and `printRoadmapJSON()`

---

## IMPLEMENTATION RECOMMENDATIONS

### FTS5 Search Enhancement

**Step 1: Add FTS5 Migration (Migration 14)**
```go
// Create virtual FTS5 tables for full-text search
CREATE VIRTUAL TABLE features_fts USING fts5(
  name,        // text from features.name
  description, // text from features.description
  spec,        // text from features.spec
  content=features,
  content_rowid=id
);

CREATE VIRTUAL TABLE roadmap_items_fts USING fts5(
  title,       // roadmap_items.title
  description, // roadmap_items.description
  content=roadmap_items,
  content_rowid=id
);

CREATE VIRTUAL TABLE ideas_fts USING fts5(
  title,       // idea_queue.title
  raw_input,   // idea_queue.raw_input
  spec_md,     // idea_queue.spec_md
  content=idea_queue,
  content_rowid=id
);

CREATE VIRTUAL TABLE discussions_fts USING fts5(
  title,       // discussions.title
  body,        // discussions.body
  content=discussions,
  content_rowid=id
);

// Create triggers to keep FTS tables in sync
CREATE TRIGGER features_ai AFTER INSERT ON features BEGIN
  INSERT INTO features_fts(rowid, name, description, spec) 
  VALUES (new.id, new.name, new.description, new.spec);
END;
// ... similar for UPDATE and DELETE
```

**Step 2: Add FTS5 Query Functions in db/queries.go**
```go
// SearchAll - unified search across all content types
func SearchAll(db *sql.DB, projectID, query string) (SearchResults, error)

// SearchFeatures - FTS5 feature search with ranking
func SearchFeatures(db *sql.DB, projectID, query string) ([]Feature, error)

// SearchRoadmapItems - FTS5 roadmap search
func SearchRoadmapItems(db *sql.DB, projectID, query string) ([]RoadmapItem, error)

// SearchIdeas - FTS5 idea queue search
func SearchIdeas(db *sql.DB, projectID, query string) ([]IdeaQueueItem, error)

// SearchDiscussions - FTS5 discussion search
func SearchDiscussions(db *sql.DB, projectID, query string) ([]Discussion, error)
```

**Step 3: Create SearchResult struct in models.go**
```go
type SearchResult struct {
  Type      string      `json:"type"`    // "feature", "roadmap", "idea", "discussion", "context", "event"
  ID        string      `json:"id"`
  Title     string      `json:"title"`
  Preview   string      `json:"preview"` // First 200 chars of matching content
  Score     float64     `json:"score"`   // FTS5 BM25 score (if applicable)
  CreatedAt string      `json:"created_at"`
}

type SearchResults struct {
  Query   string         `json:"query"`
  Count   int            `json:"count"`
  Results []SearchResult `json:"results"`
}
```

**Step 4: Update server search endpoint** (server.go handleSearch)
- Call new SearchAll() function
- Return unified SearchResults object
- Support filtering by type via query parameter (?type=feature,roadmap)

### Export Formats Enhancement

**Step 1: Create `/workspace/internal/export/export.go`**
```go
package export

import (
  "fmt"
  "encoding/csv"
  "encoding/json"
  "database/sql"
  "github.com/mschulkind/lifecycle/internal/models"
)

// ExportFormat constants
const (
  FormatJSON     = "json"
  FormatMarkdown = "markdown"
  FormatCSV      = "csv"
)

// ExportOptions controls export behavior
type ExportOptions struct {
  Format  string
  Scope   string  // "all", "features", "roadmap", "ideas"
  Filter  string  // e.g., "status=done"
}

// ExportFeatures(db, projectID, opts) ([]byte, error)
// ExportRoadmap(db, projectID, opts) ([]byte, error)
// ExportIdeas(db, projectID, opts) ([]byte, error)
// ExportAll(db, projectID, opts) ([]byte, error)
```

**Step 2: Implement exporters for each format**
- JSON: Structured output with metadata
- Markdown: Human-readable with sections, summaries, tables
- CSV: Tabular format for spreadsheet import

**Step 3: Add API export endpoints** (server.go)
```
GET /api/export/features?format=json|markdown|csv&status=done
GET /api/export/roadmap?format=json|markdown|csv
GET /api/export/ideas?format=json|markdown|csv
GET /api/export/all?format=json|markdown
```

**Step 4: Add CLI export commands**
```
lifecycle feature export --format json|markdown|csv [--status done]
lifecycle roadmap export --format json|markdown|csv [--priority critical]
lifecycle ideas export --format json|markdown|csv [--status approved]
lifecycle export-all --format json|markdown
```

---

## KEY PATTERNS SUMMARY

| Pattern | Location | Usage |
|---------|----------|-------|
| Migration versioning | db/db.go line 34-63 | Add new features to migrations[] slice |
| Query construction | db/queries.go | Build dynamic SQL with args, defer close, scan slice |
| JSON responses | server/server.go line 263-266 | Use writeJSON() helper, pretty-print |
| WebSocket broadcast | server/server.go line 55-64 | hub.broadcast([]byte) to all clients |
| CLI commands | cli/*.go | Cobra command with flags, use openDB(), print output |
| Export helpers | cli/roadmap.go | Group data, use emoji/formatting, write to stdout |
| Flag handling | cli/features.go | GetString/Int/StringSlice from cmd.Flags() |

---

## FILES TO CREATE/MODIFY

### For FTS5 Search
- [ ] **db/db.go** - Add Migration 14 (FTS5 tables + triggers)
- [ ] **db/queries.go** - Add SearchFeatures, SearchRoadmapItems, SearchIdeas, SearchDiscussions, SearchAll
- [ ] **models/models.go** - Add SearchResult, SearchResults structs
- [ ] **server/server.go** - Update handleSearch() to use new search functions
- [ ] **cli/search.go** - Update search command to show formatted results

### For Export Formats
- [ ] **internal/export/export.go** - New package with export functions
- [ ] **internal/export/json.go** - JSON export implementations
- [ ] **internal/export/markdown.go** - Markdown export implementations
- [ ] **internal/export/csv.go** - CSV export implementations
- [ ] **server/server.go** - Add /api/export/* endpoints
- [ ] **cli/features.go** - Add feature export subcommand
- [ ] **cli/roadmap.go** - Enhance roadmap export (add CSV)
- [ ] **cli/ideas.go** - Add idea export subcommand
- [ ] **cli/export.go** - New CLI command for unified export

---

## DEPENDENCIES ALREADY IN PROJECT
- `github.com/spf13/cobra` - CLI framework
- `github.com/fsnotify/fsnotify` - File watching for DB changes
- `github.com/gorilla/websocket` - WebSocket support
- `modernc.org/sqlite` - SQLite driver
- Standard library: encoding/json, encoding/csv, database/sql

