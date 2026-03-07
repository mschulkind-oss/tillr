# Lifecycle Project Analysis - Complete Documentation
## FTS5 Search Enhancement & Export Formats Implementation

**Generated:** March 7, 2025

---

## 📚 Documents Created

This analysis includes **2 comprehensive guides** with complete architecture documentation and ready-to-use implementation code.

### 1. **ARCHITECTURE_REPORT.md** (774 lines, 25KB)
   
**What it contains:**
- Complete project architecture overview
- Database schema documentation (13 migrations)
- All table structures with full column definitions
- Existing query functions documented
- Server routes and endpoints listed
- CLI command structure and patterns
- Model struct definitions
- Pattern analysis and recommendations

**Use this to:**
- Understand the current system structure
- Learn coding patterns and conventions
- See how migrations, queries, and routes work
- Reference table schemas and indices

**Key Sections:**
1. Database Layer (migrations, schema, tables)
2. Database Queries (existing functions, patterns)
3. Server Layer (routes, endpoints, responses)
4. CLI Structure (commands, flags, subcommands)
5. Models (Feature, RoadmapItem, IdeaQueueItem, etc.)
6. Implementation Recommendations
7. Key Patterns Summary
8. Files to Create/Modify

---

### 2. **IMPLEMENTATION_GUIDE.md** (988 lines, 26KB)

**What it contains:**
- Complete source code for ALL new functions
- Step-by-step implementation instructions
- Exact line numbers for file modifications
- Testing procedures and verification steps
- Copy-paste ready code blocks

**Use this to:**
- Implement FTS5 search enhancement
- Implement export format support
- Add new database queries
- Update server endpoints
- Create export package

**Implementation Sections:**

#### Part 1: FTS5 Search Enhancement
1. **db/db.go** - Migration 14 SQL (complete)
   - 4 FTS5 virtual tables
   - 12 auto-sync triggers
   
2. **db/queries.go** - 4 New Functions (complete)
   - SearchFeatures(projectID, query)
   - SearchRoadmapItems(projectID, query)
   - SearchIdeas(projectID, query)
   - SearchDiscussions(projectID, query)

3. **models/models.go** - 2 New Structs (complete)
   - SearchResultItem
   - SearchResults

4. **server/server.go** - Updated Handler (complete)
   - Enhanced handleSearch() function
   - Searches all 5 types
   - Type filtering support

#### Part 2: Export Formats
1. **internal/export/export.go** - NEW Package (complete)
   - JSON exporters (features, roadmap, ideas)
   - Markdown exporters (features, roadmap, ideas)
   - CSV exporters (features, roadmap, ideas)

2. **server/server.go** - Export Endpoints (complete)
   - /api/export/features
   - /api/export/roadmap
   - /api/export/ideas

---

## 🚀 Quick Start

### Step 1: Understand the Architecture (30 minutes)
```bash
cat /workspace/ARCHITECTURE_REPORT.md | head -300
# Read sections 1-3 to understand:
# - How migrations work
# - Current database schema
# - How queries are structured
```

### Step 2: Implement FTS5 Search (2 hours)
```bash
# Follow IMPLEMENTATION_GUIDE.md Part 1:
# 1. Add Migration 14 to db/db.go
# 2. Add 4 search functions to db/queries.go
# 3. Add structs to models/models.go
# 4. Update handleSearch in server/server.go
```

### Step 3: Test FTS5 (15 minutes)
```bash
# Start server
lifecycle serve

# Test search (should return unified results)
curl "http://localhost:3847/api/search?q=test"

# Test type filtering
curl "http://localhost:3847/api/search?q=test&type=feature,roadmap"
```

### Step 4: Implement Export Formats (2 hours)
```bash
# Follow IMPLEMENTATION_GUIDE.md Part 2:
# 1. Create internal/export/export.go
# 2. Add export handlers to server/server.go
# 3. Add route registrations to server/server.go
```

### Step 5: Test Exports (15 minutes)
```bash
# Test various export formats
curl "http://localhost:3847/api/export/roadmap?format=json"
curl "http://localhost:3847/api/export/roadmap?format=csv"
curl "http://localhost:3847/api/export/features?format=markdown"
```

---

## 📋 Implementation Checklist

### FTS5 Search Enhancement
- [ ] Read Architecture Report Section 1 (Database Layer)
- [ ] Read Implementation Guide Part 1
- [ ] Copy Migration 14 code to db/db.go
- [ ] Copy 4 search functions to db/queries.go
- [ ] Copy SearchResult structs to models/models.go
- [ ] Replace handleSearch in server/server.go
- [ ] Test migration runs: `lifecycle serve`
- [ ] Test search API: `curl /api/search?q=test`
- [ ] Test type filtering: `curl /api/search?q=test&type=feature`

### Export Formats
- [ ] Read Implementation Guide Part 2
- [ ] Create internal/export/export.go
- [ ] Add export handlers to server/server.go (3 endpoints)
- [ ] Add route registrations to server/server.go
- [ ] Test JSON export: `curl /api/export/roadmap?format=json`
- [ ] Test CSV export: `curl /api/export/roadmap?format=csv`
- [ ] Test Markdown export: `curl /api/export/roadmap?format=markdown`

---

## 🎯 Key Findings

### Current State
✅ **Database**: Fully-featured with 13 migrations  
✅ **API**: 40+ endpoints serving JSON  
✅ **CLI**: 20+ Cobra commands with subcommands  
✅ **Search**: Exists but limited (LIKE-based, Events only)  
✅ **Export**: Roadmap only, CLI only (markdown/json)  

### What's Missing
❌ **FTS5**: No full-text search implementation  
❌ **Cross-type search**: Can't search features + roadmap + ideas together  
❌ **Ranking**: No relevance scoring  
❌ **CSV export**: No tabular format support  
❌ **API exports**: No server-side export endpoints  

### Implementation Impact
- **FTS5 Search**: ~200 lines of code (migration + queries + handler)
- **Export Formats**: ~300 lines of code (exporters + handlers)
- **Total**: ~500 lines of new code
- **Estimated time**: 4-6 hours
- **No new dependencies**: Uses SQLite FTS5 + stdlib

---

## 🏗️ Architecture Overview

```
┌─────────────────────────────────────────┐
│     Frontend (React/Vue SPA)             │
│  /workspace/web/assets                  │
└─────────────┬───────────────────────────┘
              │ HTTP + WebSocket
┌─────────────▼───────────────────────────┐
│     Server (Go)                         │
│  /workspace/internal/server             │
│  - 40+ REST endpoints                   │
│  - WebSocket for real-time updates      │
│  - Port 3847                            │
└─────────────┬───────────────────────────┘
              │ SQL queries
┌─────────────▼───────────────────────────┐
│     Database (SQLite)                   │
│  ~/.lifecycle/lifecycle.db              │
│  - 13 migrations                        │
│  - 20+ tables                           │
│  - FTS5 virtual tables (to add)         │
└─────────────────────────────────────────┘

CLI (Cobra)
└─ commands/subcommands for project mgmt
   - feature, roadmap, idea, search, etc.
```

---

## 📁 File Structure

```
/workspace/
├── ARCHITECTURE_REPORT.md      ← Read this first
├── IMPLEMENTATION_GUIDE.md     ← Use this to code
├── ANALYSIS_INDEX.md           ← You are here
├── internal/
│   ├── db/
│   │   ├── db.go              ← Add Migration 14
│   │   └── queries.go         ← Add search functions
│   ├── models/
│   │   └── models.go          ← Add SearchResult structs
│   ├── server/
│   │   └── server.go          ← Update handlers, add routes
│   ├── export/                ← Create this package
│   │   └── export.go          ← New file (200+ lines)
│   └── cli/
│       ├── root.go
│       ├── search.go
│       └── ...
└── web/
    └── assets/                ← Frontend (React/Vue)
```

---

## 🔍 Schema Quick Reference

### Features Table
```sql
id, project_id, milestone_id, name, description, spec,
status (draft/planning/implementing/agent-qa/human-qa/done/blocked),
priority (integer), assigned_cycle, roadmap_item_id,
created_at, updated_at, previous_status
```

### Roadmap Items Table
```sql
id, project_id, title, description, category, priority,
status (proposed/accepted/in-progress/done/deferred/rejected),
effort (xs/s/m/l/xl), sort_order, created_at, updated_at
```

### Idea Queue Table
```sql
id, project_id, title, raw_input,
idea_type (feature/bug/feedback),
status (pending/processing/spec-ready/approved/rejected/implementing/done),
spec_md, auto_implement, submitted_by, assigned_agent, feature_id,
created_at, updated_at
```

---

## 📖 Reading Guide

**For architects & senior developers:**
1. Read ARCHITECTURE_REPORT.md completely (45 min)
2. Understand patterns and conventions
3. Assign implementation tasks

**For implementers:**
1. Read IMPLEMENTATION_GUIDE.md Part 1 (15 min)
2. Copy code and follow step-by-step (2 hours)
3. Read and implement Part 2 (2 hours)
4. Test and verify (30 min)

**For QA/Testers:**
1. Read Testing sections in IMPLEMENTATION_GUIDE.md
2. Use provided curl commands to verify
3. Test all format combinations
4. Test search type filtering

---

## 🛠️ Technology Stack

**Backend:**
- Go 1.19+
- Cobra CLI framework
- Gorilla WebSocket
- SQLite with modernc.org/sqlite
- Standard library (http, json, csv, etc.)

**Database:**
- SQLite (file-based)
- WAL mode enabled
- FTS5 virtual tables (to add)
- Versioned migrations

**Frontend:**
- React or Vue SPA
- WebSocket connection for real-time updates
- Served from embedded assets

---

## 💡 Key Patterns

### Migration Pattern
```go
var migrations = []string{
  "CREATE TABLE ...",
  "CREATE TABLE ...",
  "ALTER TABLE ...",
  // New migrations added here
}
```

### Query Pattern
```go
rows, err := db.Query(query, args...)
defer rows.Close()
for rows.Next() {
  // scan and append
}
return out, rows.Err()
```

### Handler Pattern
```go
func handleSomething(database *sql.DB, w http.ResponseWriter, r *http.Request) error {
  // Get data
  // Process
  return writeJSON(w, result)
}
```

### Command Pattern
```go
var someCmd = &cobra.Command{
  Use: "subcommand",
  RunE: func(cmd *cobra.Command, args []string) error {
    db, _, _ := openDB()
    flag, _ := cmd.Flags().GetString("flag")
    if jsonOutput { return printJSON(data) }
    fmt.Printf(...)
  },
}
```

---

## 📞 Reference Links in Documents

### ARCHITECTURE_REPORT.md
- Line references for all functions
- Database schema starting at line 100
- Migration details at line 68
- Model definitions starting at line 1

### IMPLEMENTATION_GUIDE.md
- Complete code for each section
- File paths and line numbers
- Copy-paste ready code blocks
- Testing procedures

---

## 🎓 Learning Resources

From the codebase, you can learn:
1. **Go web development** - Server setup, routing, JSON encoding
2. **SQLite best practices** - Migrations, indices, queries
3. **CLI design** - Cobra framework, subcommands, flags
4. **API design** - RESTful endpoints, error handling
5. **Database patterns** - FTS5, transactions, foreign keys

---

## ⚡ Next Steps

1. **Open ARCHITECTURE_REPORT.md**
   - Spend 45 minutes understanding the system
   
2. **Open IMPLEMENTATION_GUIDE.md**
   - Follow Part 1 for FTS5 (2 hours)
   - Follow Part 2 for Export (2 hours)
   
3. **Implement in order:**
   - Migrations first
   - Queries second
   - Models third
   - Handlers last
   
4. **Test each step:**
   - Use provided curl commands
   - Verify database changes
   - Check API responses

---

## 📊 Implementation Statistics

| Component | Lines | Files | Difficulty |
|-----------|-------|-------|------------|
| FTS5 Migration | 80 | 1 | Easy |
| Search Functions | 120 | 1 | Easy |
| SearchResult Structs | 20 | 1 | Easy |
| Updated Handler | 80 | 1 | Medium |
| Export Package | 200 | 1 | Medium |
| Export Handlers | 60 | 1 | Medium |
| **TOTAL** | **560** | **6** | **Moderate** |

**Time Estimate:** 4-6 hours hands-on coding

---

## ✅ Success Criteria

FTS5 Search:
- [ ] Migration 14 runs without errors
- [ ] FTS5 tables created in database
- [ ] `/api/search?q=test` returns results from all types
- [ ] Results include feature, roadmap, idea, discussion entries
- [ ] Type filtering works: `?type=feature,roadmap`

Export Formats:
- [ ] `/api/export/roadmap?format=json` returns JSON
- [ ] `/api/export/roadmap?format=csv` returns CSV
- [ ] `/api/export/roadmap?format=markdown` returns Markdown
- [ ] Same for features and ideas endpoints
- [ ] All formats include proper headers and structure

---

**Last Updated:** March 7, 2025  
**Status:** Complete and Ready for Implementation

For detailed instructions, see:
- **Architecture details** → `/workspace/ARCHITECTURE_REPORT.md`
- **Implementation code** → `/workspace/IMPLEMENTATION_GUIDE.md`
