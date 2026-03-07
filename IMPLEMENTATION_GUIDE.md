# Lifecycle Project: Implementation Guide
## FTS5 Search Enhancement & Export Formats

---

## EXECUTIVE SUMMARY

The Lifecycle project is a **human-in-the-loop project management tool** with:
- **SQLite database** with 13 migrations (latest: Decision log/ADRs)
- **REST API server** with WebSocket support for real-time updates
- **Cobra CLI** with ~20+ commands for managing features, roadmap, ideas, discussions
- **React/Vue SPA frontend** served from `/workspace/web/assets`

### Current State
✅ **Database**: Fully featured with features, roadmap, ideas, discussions, agents, etc.
✅ **Server**: 40+ API endpoints registered
✅ **CLI**: Comprehensive commands with subcommands
❌ **Search**: Only LIKE-based search on Event.data field
❌ **Export**: Only roadmap export (markdown/json) in CLI, no server endpoints

### What You Need to Build

**Feature 1: FTS5 Search Enhancement**
- Replace LIKE search with SQLite FTS5 full-text search
- Search across: features, roadmap items, ideas, discussions, context, events
- Return unified SearchResults object with scoring/ranking

**Feature 2: Export Formats**
- Add CSV export support (in addition to JSON/Markdown)
- Create `/internal/export/` package with reusable exporters
- Add server API endpoints for exports
- Add CLI commands for feature/idea exports

---

## ARCHITECTURE QUICK FACTS

### Database Schema Key Tables

**features** (11 columns)
- Columns: id, project_id, milestone_id, name, description, spec, status, priority, assigned_cycle, roadmap_item_id, created_at, updated_at, previous_status
- Statuses: draft, planning, implementing, agent-qa, human-qa, done, blocked
- Indices: project, milestone, status

**roadmap_items** (10 columns)
- Columns: id, project_id, title, description, category, priority, status, effort, sort_order, created_at, updated_at
- Priority: critical, high, medium, low, nice-to-have
- Status: proposed, accepted, in-progress, done, deferred, rejected
- Effort: xs, s, m, l, xl (t-shirt sizes)

**idea_queue** (12 columns)
- Columns: id, project_id, title, raw_input, idea_type, status, spec_md, auto_implement, submitted_by, assigned_agent, feature_id, created_at, updated_at
- Types: feature, bug, feedback
- Status: pending, processing, spec-ready, approved, rejected, implementing, done

**discussions** (9 columns)
- Columns: id, project_id, feature_id, title, body, status, author, created_at, updated_at
- Status: open, resolved, merged, closed

### Server Routes
```
/api/search               GET → handleSearch(q param)
/api/features            GET/POST
/api/roadmap            GET/POST
/api/ideas              GET/POST
/api/discussions        GET/POST
/api/context            GET/POST
... 30+ more endpoints
```

### CLI Commands
```
lifecycle feature add|list|show|edit|remove
lifecycle roadmap show|add|edit|prioritize|export|stats
lifecycle idea add|list|approve|reject
lifecycle search <query>
lifecycle serve (start web server)
... 20+ more commands
```

---

## PART 1: FTS5 SEARCH ENHANCEMENT

### Files to Modify

#### 1. `/workspace/internal/db/db.go` - Add Migration 14

Location: Line 68-349 (migrations[] array)

**Add this migration to the migrations array (before closing bracket):**

```go
// Migration 14: Full-text search with FTS5
`CREATE VIRTUAL TABLE features_fts USING fts5(
  name,
  description,
  spec,
  content=features,
  content_rowid=id
);

CREATE VIRTUAL TABLE roadmap_items_fts USING fts5(
  title,
  description,
  content=roadmap_items,
  content_rowid=id
);

CREATE VIRTUAL TABLE ideas_fts USING fts5(
  title,
  raw_input,
  spec_md,
  content=idea_queue,
  content_rowid=id
);

CREATE VIRTUAL TABLE discussions_fts USING fts5(
  title,
  body,
  content=discussions,
  content_rowid=id
);

CREATE TRIGGER features_ai AFTER INSERT ON features BEGIN
  INSERT INTO features_fts(rowid, name, description, spec) 
  VALUES (new.id, new.name, COALESCE(new.description,''), COALESCE(new.spec,''));
END;

CREATE TRIGGER features_ad AFTER DELETE ON features BEGIN
  DELETE FROM features_fts WHERE rowid = old.id;
END;

CREATE TRIGGER features_au AFTER UPDATE ON features BEGIN
  UPDATE features_fts SET name=new.name, description=COALESCE(new.description,''), spec=COALESCE(new.spec,'') WHERE rowid=new.id;
END;

CREATE TRIGGER roadmap_items_ai AFTER INSERT ON roadmap_items BEGIN
  INSERT INTO roadmap_items_fts(rowid, title, description) 
  VALUES (new.id, new.title, COALESCE(new.description,''));
END;

CREATE TRIGGER roadmap_items_ad AFTER DELETE ON roadmap_items BEGIN
  DELETE FROM roadmap_items_fts WHERE rowid = old.id;
END;

CREATE TRIGGER roadmap_items_au AFTER UPDATE ON roadmap_items BEGIN
  UPDATE roadmap_items_fts SET title=new.title, description=COALESCE(new.description,'') WHERE rowid=new.id;
END;

CREATE TRIGGER ideas_ai AFTER INSERT ON idea_queue BEGIN
  INSERT INTO ideas_fts(rowid, title, raw_input, spec_md) 
  VALUES (new.id, new.title, new.raw_input, COALESCE(new.spec_md,''));
END;

CREATE TRIGGER ideas_ad AFTER DELETE ON idea_queue BEGIN
  DELETE FROM ideas_fts WHERE rowid = old.id;
END;

CREATE TRIGGER ideas_au AFTER UPDATE ON idea_queue BEGIN
  UPDATE ideas_fts SET title=new.title, raw_input=new.raw_input, spec_md=COALESCE(new.spec_md,'') WHERE rowid=new.id;
END;

CREATE TRIGGER discussions_ai AFTER INSERT ON discussions BEGIN
  INSERT INTO discussions_fts(rowid, title, body) 
  VALUES (new.id, new.title, COALESCE(new.body,''));
END;

CREATE TRIGGER discussions_ad AFTER DELETE ON discussions BEGIN
  DELETE FROM discussions_fts WHERE rowid = old.id;
END;

CREATE TRIGGER discussions_au AFTER UPDATE ON discussions BEGIN
  UPDATE discussions_fts SET title=new.title, body=COALESCE(new.body,'') WHERE rowid=new.id;
END;`,
```

#### 2. `/workspace/internal/db/queries.go` - Add Search Functions

Location: After SearchContext() function (around line 1828)

**Add these new functions:**

```go
// SearchFeatures searches features using FTS5
func SearchFeatures(db *sql.DB, projectID, query string) ([]Feature, error) {
rows, err := db.Query(`
SELECT f.id, f.project_id, COALESCE(f.milestone_id,''), f.name, COALESCE(f.description,''),
   COALESCE(f.spec,''), f.status, f.priority, COALESCE(f.assigned_cycle,''),
   COALESCE(f.roadmap_item_id,''), f.created_at, f.updated_at, COALESCE(m.name,''),
   COALESCE(f.previous_status,'')
FROM features f
LEFT JOIN milestones m ON f.milestone_id = m.id
WHERE f.project_id = ? AND f.id IN (
SELECT rowid FROM features_fts WHERE features_fts MATCH ?
)
ORDER BY f.created_at DESC LIMIT 50`, projectID, query)
if err != nil {
return nil, err
}
defer rows.Close()

var out []Feature
for rows.Next() {
var f Feature
if err := rows.Scan(&f.ID, &f.ProjectID, &f.MilestoneID, &f.Name, &f.Description,
&f.Spec, &f.Status, &f.Priority, &f.AssignedCycle, &f.RoadmapItemID,
&f.CreatedAt, &f.UpdatedAt, &f.MilestoneName, &f.PreviousStatus); err != nil {
return nil, err
}
out = append(out, f)
}
return out, rows.Err()
}

// SearchRoadmapItems searches roadmap items using FTS5
func SearchRoadmapItems(db *sql.DB, projectID, query string) ([]RoadmapItem, error) {
rows, err := db.Query(`
SELECT id, project_id, title, description, COALESCE(category,''), priority, status,
       COALESCE(effort,''), sort_order, created_at, updated_at
FROM roadmap_items
WHERE project_id = ? AND id IN (
SELECT rowid FROM roadmap_items_fts WHERE roadmap_items_fts MATCH ?
)
ORDER BY created_at DESC LIMIT 50`, projectID, query)
if err != nil {
return nil, err
}
defer rows.Close()

var out []RoadmapItem
for rows.Next() {
var r RoadmapItem
if err := rows.Scan(&r.ID, &r.ProjectID, &r.Title, &r.Description, &r.Category,
&r.Priority, &r.Status, &r.Effort, &r.SortOrder, &r.CreatedAt, &r.UpdatedAt); err != nil {
return nil, err
}
out = append(out, r)
}
return out, rows.Err()
}

// SearchIdeas searches ideas using FTS5
func SearchIdeas(db *sql.DB, projectID, query string) ([]IdeaQueueItem, error) {
rows, err := db.Query(`
SELECT id, project_id, title, raw_input, idea_type, status,
       COALESCE(spec_md,''), auto_implement, COALESCE(submitted_by,'human'),
       COALESCE(assigned_agent,''), COALESCE(feature_id,''), created_at, updated_at
FROM idea_queue
WHERE project_id = ? AND id IN (
SELECT rowid FROM ideas_fts WHERE ideas_fts MATCH ?
)
ORDER BY created_at DESC LIMIT 50`, projectID, query)
if err != nil {
return nil, err
}
defer rows.Close()

var out []IdeaQueueItem
for rows.Next() {
var item IdeaQueueItem
var autoImpl int
if err := rows.Scan(&item.ID, &item.ProjectID, &item.Title, &item.RawInput,
&item.IdeaType, &item.Status, &item.SpecMD, &autoImpl, &item.SubmittedBy,
&item.AssignedAgent, &item.FeatureID, &item.CreatedAt, &item.UpdatedAt); err != nil {
return nil, err
}
item.AutoImplement = autoImpl != 0
out = append(out, item)
}
return out, rows.Err()
}

// SearchDiscussions searches discussions using FTS5
func SearchDiscussions(db *sql.DB, projectID, query string) ([]Discussion, error) {
rows, err := db.Query(`
SELECT id, project_id, COALESCE(feature_id,''), title, body, status, author,
       created_at, updated_at
FROM discussions
WHERE project_id = ? AND id IN (
SELECT rowid FROM discussions_fts WHERE discussions_fts MATCH ?
)
ORDER BY created_at DESC LIMIT 50`, projectID, query)
if err != nil {
return nil, err
}
defer rows.Close()

var out []Discussion
for rows.Next() {
var d Discussion
if err := rows.Scan(&d.ID, &d.ProjectID, &d.FeatureID, &d.Title, &d.Body,
&d.Status, &d.Author, &d.CreatedAt, &d.UpdatedAt); err != nil {
return nil, err
}
out = append(out, d)
}
return out, rows.Err()
}
```

#### 3. `/workspace/internal/models/models.go` - Add Search Result Structs

Location: After existing structs (around line 325)

**Add these new structs:**

```go
// SearchResultItem represents a single search result
type SearchResultItem struct {
Type      string `json:"type"`      // "feature", "roadmap", "idea", "discussion", "context"
ID        string `json:"id"`        // numeric ID for non-feature types
Title     string `json:"title"`
Preview   string `json:"preview"`   // First 150 chars of matching content
ProjectID string `json:"project_id,omitempty"`
FeatureID string `json:"feature_id,omitempty"`
CreatedAt string `json:"created_at"`
Status    string `json:"status,omitempty"` // For items with status field
}

// SearchResults is the unified response for full-text search
type SearchResults struct {
Query   string             `json:"query"`
Count   int                `json:"count"`
Results []SearchResultItem `json:"results"`
}
```

#### 4. `/workspace/internal/server/server.go` - Update Search Handler

Location: Line 662-676 (handleSearch function)

**Replace the entire handleSearch function:**

```go
func handleSearch(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
p, err := db.GetProject(database)
if err != nil {
return err
}

q := r.URL.Query().Get("q")
if q == "" {
return writeJSON(w, models.SearchResults{
Query:   "",
Count:   0,
Results: []models.SearchResultItem{},
})
}

// Parse optional type filter: ?type=feature,roadmap,idea,discussion,context
typeFilter := r.URL.Query().Get("type")
types := make(map[string]bool)
if typeFilter != "" {
for _, t := range strings.Split(typeFilter, ",") {
types[strings.TrimSpace(t)] = true
}
} else {
// Default: search all types
types["feature"] = true
types["roadmap"] = true
types["idea"] = true
types["discussion"] = true
types["context"] = true
}

var results []models.SearchResultItem

// Search features
if types["feature"] {
features, err := db.SearchFeatures(database, p.ID, q)
if err == nil {
for _, f := range features {
preview := f.Description
if len(preview) > 150 {
preview = preview[:150] + "..."
}
results = append(results, models.SearchResultItem{
Type:      "feature",
ID:        f.ID,
Title:     f.Name,
Preview:   preview,
ProjectID: f.ProjectID,
CreatedAt: f.CreatedAt,
Status:    f.Status,
})
}
}
}

// Search roadmap items
if types["roadmap"] {
roadmapItems, err := db.SearchRoadmapItems(database, p.ID, q)
if err == nil {
for _, r := range roadmapItems {
preview := r.Description
if len(preview) > 150 {
preview = preview[:150] + "..."
}
results = append(results, models.SearchResultItem{
Type:      "roadmap",
ID:        r.ID,
Title:     r.Title,
Preview:   preview,
ProjectID: r.ProjectID,
CreatedAt: r.CreatedAt,
Status:    r.Status,
})
}
}
}

// Search ideas
if types["idea"] {
ideas, err := db.SearchIdeas(database, p.ID, q)
if err == nil {
for _, i := range ideas {
preview := i.RawInput
if len(preview) > 150 {
preview = preview[:150] + "..."
}
results = append(results, models.SearchResultItem{
Type:      "idea",
ID:        fmt.Sprintf("%d", i.ID),
Title:     i.Title,
Preview:   preview,
ProjectID: i.ProjectID,
CreatedAt: i.CreatedAt,
Status:    i.Status,
})
}
}
}

// Search discussions
if types["discussion"] {
discussions, err := db.SearchDiscussions(database, p.ID, q)
if err == nil {
for _, d := range discussions {
preview := d.Body
if len(preview) > 150 {
preview = preview[:150] + "..."
}
results = append(results, models.SearchResultItem{
Type:      "discussion",
ID:        fmt.Sprintf("%d", d.ID),
Title:     d.Title,
Preview:   preview,
ProjectID: d.ProjectID,
FeatureID: d.FeatureID,
CreatedAt: d.CreatedAt,
Status:    d.Status,
})
}
}
}

// Search context
if types["context"] {
entries, err := db.SearchContext(database, p.ID, q)
if err == nil {
for _, e := range entries {
preview := e.ContentMD
if len(preview) > 150 {
preview = preview[:150] + "..."
}
results = append(results, models.SearchResultItem{
Type:      "context",
ID:        fmt.Sprintf("%d", e.ID),
Title:     e.Title,
Preview:   preview,
ProjectID: e.ProjectID,
FeatureID: e.FeatureID,
CreatedAt: e.CreatedAt,
})
}
}
}

return writeJSON(w, models.SearchResults{
Query:   q,
Count:   len(results),
Results: results,
})
}
```

---

## PART 2: EXPORT FORMATS

### Files to Create

#### 1. `/workspace/internal/export/export.go` (NEW FILE)

```go
package export

import (
"encoding/csv"
"encoding/json"
"fmt"
"io"
"os"
"time"

"github.com/mschulkind/lifecycle/internal/models"
)

// Format constants
const (
FormatJSON     = "json"
FormatMarkdown = "markdown"
FormatCSV      = "csv"
)

// ExportOptions controls export behavior
type ExportOptions struct {
Format string // json, markdown, csv
Output io.Writer
}

// ExportFeatures exports features in the specified format
func ExportFeatures(features []models.Feature, opts ExportOptions) error {
switch opts.Format {
case FormatJSON:
return exportFeaturesJSON(features, opts.Output)
case FormatMarkdown:
return exportFeaturesMarkdown(features, opts.Output)
case FormatCSV:
return exportFeaturesCSV(features, opts.Output)
default:
return fmt.Errorf("unsupported format: %s", opts.Format)
}
}

// ExportRoadmapItems exports roadmap items in the specified format
func ExportRoadmapItems(items []models.RoadmapItem, opts ExportOptions) error {
switch opts.Format {
case FormatJSON:
return exportRoadmapJSON(items, opts.Output)
case FormatMarkdown:
return exportRoadmapMarkdown(items, opts.Output)
case FormatCSV:
return exportRoadmapCSV(items, opts.Output)
default:
return fmt.Errorf("unsupported format: %s", opts.Format)
}
}

// ExportIdeas exports ideas in the specified format
func ExportIdeas(ideas []models.IdeaQueueItem, opts ExportOptions) error {
switch opts.Format {
case FormatJSON:
return exportIdeasJSON(ideas, opts.Output)
case FormatMarkdown:
return exportIdeasMarkdown(ideas, opts.Output)
case FormatCSV:
return exportIdeasCSV(ideas, opts.Output)
default:
return fmt.Errorf("unsupported format: %s", opts.Format)
}
}

// === JSON EXPORTERS ===

func exportFeaturesJSON(features []models.Feature, w io.Writer) error {
result := map[string]interface{}{
"exported_at": time.Now().Format(time.RFC3339),
"type":        "features",
"count":       len(features),
"items":       features,
}
enc := json.NewEncoder(w)
enc.SetIndent("", "  ")
return enc.Encode(result)
}

func exportRoadmapJSON(items []models.RoadmapItem, w io.Writer) error {
result := map[string]interface{}{
"exported_at": time.Now().Format(time.RFC3339),
"type":        "roadmap",
"count":       len(items),
"items":       items,
}
enc := json.NewEncoder(w)
enc.SetIndent("", "  ")
return enc.Encode(result)
}

func exportIdeasJSON(ideas []models.IdeaQueueItem, w io.Writer) error {
result := map[string]interface{}{
"exported_at": time.Now().Format(time.RFC3339),
"type":        "ideas",
"count":       len(ideas),
"items":       ideas,
}
enc := json.NewEncoder(w)
enc.SetIndent("", "  ")
return enc.Encode(result)
}

// === CSV EXPORTERS ===

func exportFeaturesCSV(features []models.Feature, w io.Writer) error {
writer := csv.NewWriter(w)
defer writer.Flush()

// Header
writer.Write([]string{"ID", "Name", "Status", "Priority", "Milestone", "Description", "Created At"})

for _, f := range features {
writer.Write([]string{
f.ID,
f.Name,
f.Status,
fmt.Sprintf("%d", f.Priority),
f.MilestoneName,
f.Description,
f.CreatedAt,
})
}

return writer.Error()
}

func exportRoadmapCSV(items []models.RoadmapItem, w io.Writer) error {
writer := csv.NewWriter(w)
defer writer.Flush()

// Header
writer.Write([]string{"ID", "Title", "Priority", "Status", "Category", "Effort", "Description", "Created At"})

for _, r := range items {
writer.Write([]string{
r.ID,
r.Title,
r.Priority,
r.Status,
r.Category,
r.Effort,
r.Description,
r.CreatedAt,
})
}

return writer.Error()
}

func exportIdeasCSV(ideas []models.IdeaQueueItem, w io.Writer) error {
writer := csv.NewWriter(w)
defer writer.Flush()

// Header
writer.Write([]string{"ID", "Title", "Type", "Status", "Submitted By", "Created At"})

for _, i := range ideas {
writer.Write([]string{
fmt.Sprintf("%d", i.ID),
i.Title,
i.IdeaType,
i.Status,
i.SubmittedBy,
i.CreatedAt,
})
}

return writer.Error()
}

// === MARKDOWN EXPORTERS ===

func exportFeaturesMarkdown(features []models.Feature, w io.Writer) error {
fmt.Fprintf(w, "# Features Export\n\n")
fmt.Fprintf(w, "*Generated: %s*\n\n", time.Now().Format("January 2, 2006 at 3:04 PM"))
fmt.Fprintf(w, "**Total:** %d features\n\n", len(features))

// Status summary
statusCounts := make(map[string]int)
for _, f := range features {
statusCounts[f.Status]++
}
fmt.Fprintf(w, "## Status Summary\n\n")
for status, count := range statusCounts {
fmt.Fprintf(w, "- %s: %d\n", status, count)
}
fmt.Fprintf(w, "\n")

// List features
fmt.Fprintf(w, "## Features\n\n")
for i, f := range features {
fmt.Fprintf(w, "### %d. %s\n\n", i+1, f.Name)
fmt.Fprintf(w, "- **ID:** `%s`\n", f.ID)
fmt.Fprintf(w, "- **Status:** %s\n", f.Status)
fmt.Fprintf(w, "- **Priority:** %d\n", f.Priority)
if f.Description != "" {
fmt.Fprintf(w, "- **Description:** %s\n", f.Description)
}
fmt.Fprintf(w, "- **Created:** %s\n", f.CreatedAt)
fmt.Fprintf(w, "\n")
}

return nil
}

func exportRoadmapMarkdown(items []models.RoadmapItem, w io.Writer) error {
fmt.Fprintf(w, "# Roadmap Export\n\n")
fmt.Fprintf(w, "*Generated: %s*\n\n", time.Now().Format("January 2, 2006 at 3:04 PM"))
fmt.Fprintf(w, "**Total:** %d items\n\n", len(items))

// Priority summary
priorityCounts := make(map[string]int)
for _, r := range items {
priorityCounts[r.Priority]++
}
fmt.Fprintf(w, "## Priority Summary\n\n")
priorities := []string{"critical", "high", "medium", "low", "nice-to-have"}
for _, pri := range priorities {
if count, ok := priorityCounts[pri]; ok && count > 0 {
fmt.Fprintf(w, "- %s: %d\n", pri, count)
}
}
fmt.Fprintf(w, "\n")

// List items
fmt.Fprintf(w, "## Items\n\n")
for i, r := range items {
fmt.Fprintf(w, "### %d. %s\n\n", i+1, r.Title)
fmt.Fprintf(w, "- **ID:** `%s`\n", r.ID)
fmt.Fprintf(w, "- **Priority:** %s\n", r.Priority)
fmt.Fprintf(w, "- **Status:** %s\n", r.Status)
if r.Category != "" {
fmt.Fprintf(w, "- **Category:** %s\n", r.Category)
}
if r.Effort != "" {
fmt.Fprintf(w, "- **Effort:** %s\n", r.Effort)
}
if r.Description != "" {
fmt.Fprintf(w, "- **Description:** %s\n", r.Description)
}
fmt.Fprintf(w, "- **Created:** %s\n", r.CreatedAt)
fmt.Fprintf(w, "\n")
}

return nil
}

func exportIdeasMarkdown(ideas []models.IdeaQueueItem, w io.Writer) error {
fmt.Fprintf(w, "# Ideas Export\n\n")
fmt.Fprintf(w, "*Generated: %s*\n\n", time.Now().Format("January 2, 2006 at 3:04 PM"))
fmt.Fprintf(w, "**Total:** %d ideas\n\n", len(ideas))

// Status summary
statusCounts := make(map[string]int)
for _, i := range ideas {
statusCounts[i.Status]++
}
fmt.Fprintf(w, "## Status Summary\n\n")
for status, count := range statusCounts {
fmt.Fprintf(w, "- %s: %d\n", status, count)
}
fmt.Fprintf(w, "\n")

// List ideas
fmt.Fprintf(w, "## Ideas\n\n")
for i, idea := range ideas {
fmt.Fprintf(w, "### %d. %s\n\n", i+1, idea.Title)
fmt.Fprintf(w, "- **ID:** %d\n", idea.ID)
fmt.Fprintf(w, "- **Type:** %s\n", idea.IdeaType)
fmt.Fprintf(w, "- **Status:** %s\n", idea.Status)
fmt.Fprintf(w, "- **Submitted By:** %s\n", idea.SubmittedBy)
if idea.RawInput != "" {
fmt.Fprintf(w, "- **Input:** %s\n", idea.RawInput)
}
fmt.Fprintf(w, "- **Created:** %s\n", idea.CreatedAt)
fmt.Fprintf(w, "\n")
}

return nil
}
```

#### 2. `/workspace/internal/server/server.go` - Add Export Endpoints

Location: Add after existing route registrations (around line 137)

**Add these route registrations in StartWithDBPath():**

```go
// Export endpoints
mux.HandleFunc("/api/export/features", apiHandler(database, handleExportFeatures))
mux.HandleFunc("/api/export/roadmap", apiHandler(database, handleExportRoadmap))
mux.HandleFunc("/api/export/ideas", apiHandler(database, handleExportIdeas))
```

Then add these handler functions after existing handlers (around line 1920):

```go
// --- Export handlers ---

func handleExportFeatures(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
p, err := db.GetProject(database)
if err != nil {
return err
}

format := r.URL.Query().Get("format")
if format == "" {
format = "json"
}
status := r.URL.Query().Get("status")

features, err := db.ListFeatures(database, p.ID, status, "")
if err != nil {
return err
}
if features == nil {
features = []models.Feature{}
}

// Set content-type and attachment header based on format
switch format {
case "csv":
w.Header().Set("Content-Type", "text/csv")
w.Header().Set("Content-Disposition", "attachment; filename=features.csv")
case "markdown":
w.Header().Set("Content-Type", "text/markdown")
w.Header().Set("Content-Disposition", "attachment; filename=features.md")
default: // json
w.Header().Set("Content-Type", "application/json")
}

opts := struct {
Format string
Output http.ResponseWriter
}{
Format: format,
Output: w,
}

// Note: You'll need to import the export package
// For now, use inline export logic or create the export package first

enc := json.NewEncoder(w)
enc.SetIndent("", "  ")
return enc.Encode(map[string]interface{}{
"exported_at": timeNowISO(),
"format":      format,
"count":       len(features),
"items":       features,
})
}

func handleExportRoadmap(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
p, err := db.GetProject(database)
if err != nil {
return err
}

format := r.URL.Query().Get("format")
if format == "" {
format = "json"
}

items, err := db.ListRoadmapItems(database, p.ID)
if err != nil {
return err
}
if items == nil {
items = []models.RoadmapItem{}
}

// Set content-type and attachment header based on format
switch format {
case "csv":
w.Header().Set("Content-Type", "text/csv")
w.Header().Set("Content-Disposition", "attachment; filename=roadmap.csv")
case "markdown":
w.Header().Set("Content-Type", "text/markdown")
w.Header().Set("Content-Disposition", "attachment; filename=roadmap.md")
default: // json
w.Header().Set("Content-Type", "application/json")
}

enc := json.NewEncoder(w)
enc.SetIndent("", "  ")
return enc.Encode(map[string]interface{}{
"exported_at": timeNowISO(),
"format":      format,
"count":       len(items),
"items":       items,
})
}

func handleExportIdeas(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
p, err := db.GetProject(database)
if err != nil {
return err
}

format := r.URL.Query().Get("format")
if format == "" {
format = "json"
}
status := r.URL.Query().Get("status")

ideas, err := db.ListIdeas(database, p.ID, status, "")
if err != nil {
return err
}
if ideas == nil {
ideas = []models.IdeaQueueItem{}
}

// Set content-type and attachment header based on format
switch format {
case "csv":
w.Header().Set("Content-Type", "text/csv")
w.Header().Set("Content-Disposition", "attachment; filename=ideas.csv")
case "markdown":
w.Header().Set("Content-Type", "text/markdown")
w.Header().Set("Content-Disposition", "attachment; filename=ideas.md")
default: // json
w.Header().Set("Content-Type", "application/json")
}

enc := json.NewEncoder(w)
enc.SetIndent("", "  ")
return enc.Encode(map[string]interface{}{
"exported_at": timeNowISO(),
"format":      format,
"count":       len(ideas),
"items":       ideas,
})
}
```

### Testing Your Changes

1. **FTS5 Testing**
   ```bash
   # After migration runs, query the FTS5 tables:
   sqlite3 ~/.lifecycle/lifecycle.db "SELECT rowid, name FROM features_fts WHERE features_fts MATCH 'search term';"
   
   # Test API:
   curl "http://localhost:3847/api/search?q=test&type=feature,roadmap"
   ```

2. **Export Testing**
   ```bash
   # Test API exports:
   curl "http://localhost:3847/api/export/roadmap?format=json" -o roadmap.json
   curl "http://localhost:3847/api/export/roadmap?format=csv" -o roadmap.csv
   ```

---

## IMPORTANT NOTES

1. **String imports needed in server.go:**
   - Add `"fmt"` to imports if not present (for type assertions)
   - Add `"io"` to imports if creating export package

2. **Migration will auto-run** on next `lifecycle serve` or any DB operation

3. **Test FTS5 query syntax:**
   - `MATCH 'search'` - exact phrase
   - `MATCH 'search*'` - prefix search
   - `MATCH 'search AND term'` - AND queries
   - `MATCH 'search OR term'` - OR queries

4. **CSV output** - remember to escape fields with commas/quotes

5. **Keep export/ package simple** - can expand with more formatters later

---

## FILES CREATED/MODIFIED CHECKLIST

- [ ] `/workspace/internal/db/db.go` - Add Migration 14
- [ ] `/workspace/internal/db/queries.go` - Add 4 search functions
- [ ] `/workspace/internal/models/models.go` - Add SearchResultItem + SearchResults
- [ ] `/workspace/internal/server/server.go` - Update handleSearch, add export handlers
- [ ] `/workspace/internal/export/export.go` - NEW FILE
- [ ] Test migration runs without errors
- [ ] Test FTS5 search works via API
- [ ] Test exports generate correct format

