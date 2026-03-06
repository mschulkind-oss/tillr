package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Open opens or creates a lifecycle SQLite database at the given path.
func Open(dbPath string) (*sql.DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return db, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY
		);
	`)
	if err != nil {
		return err
	}

	var version int
	err = db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&version)
	if err != nil {
		return err
	}

	for i, m := range migrations {
		if i+1 > version {
			if _, err := db.Exec(m); err != nil {
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			if _, err := db.Exec("INSERT INTO schema_version (version) VALUES (?)", i+1); err != nil {
				return err
			}
		}
	}

	return nil
}

var migrations = []string{
	// Migration 1: Core tables
	`CREATE TABLE projects (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE milestones (
		id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES projects(id),
		name TEXT NOT NULL,
		description TEXT,
		sort_order INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','blocked','done')),
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

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

	CREATE TABLE feature_deps (
		feature_id TEXT NOT NULL REFERENCES features(id),
		depends_on TEXT NOT NULL REFERENCES features(id),
		PRIMARY KEY (feature_id, depends_on)
	);

	CREATE TABLE work_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		feature_id TEXT NOT NULL REFERENCES features(id),
		work_type TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','active','done','failed')),
		agent_prompt TEXT,
		result TEXT,
		started_at TEXT,
		completed_at TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id TEXT NOT NULL REFERENCES projects(id),
		feature_id TEXT REFERENCES features(id),
		event_type TEXT NOT NULL,
		data TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

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

	CREATE TABLE qa_results (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		feature_id TEXT NOT NULL REFERENCES features(id),
		qa_type TEXT NOT NULL CHECK(qa_type IN ('agent','human')),
		passed INTEGER NOT NULL,
		notes TEXT,
		checklist TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE heartbeats (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		feature_id TEXT NOT NULL REFERENCES features(id),
		agent_id TEXT,
		message TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX idx_features_project ON features(project_id);
	CREATE INDEX idx_features_milestone ON features(milestone_id);
	CREATE INDEX idx_features_status ON features(status);
	CREATE INDEX idx_work_items_feature ON work_items(feature_id);
	CREATE INDEX idx_events_project ON events(project_id);
	CREATE INDEX idx_events_created ON events(created_at);
	CREATE INDEX idx_roadmap_project ON roadmap_items(project_id);
	CREATE INDEX idx_heartbeats_feature ON heartbeats(feature_id);`,
}
