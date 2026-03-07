package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		_ = db.Close()
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
				// ALTER TABLE ADD COLUMN is idempotent: skip if column already exists.
				if strings.Contains(m, "ADD COLUMN") && strings.Contains(err.Error(), "duplicate column") {
					// Column already exists — safe to skip.
				} else {
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
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

	// Migration 2: Cycle tables
	`CREATE TABLE cycle_instances (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		feature_id TEXT NOT NULL REFERENCES features(id),
		cycle_type TEXT NOT NULL,
		current_step INTEGER NOT NULL DEFAULT 0,
		iteration INTEGER NOT NULL DEFAULT 1,
		status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','completed','failed')),
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE cycle_scores (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		cycle_id INTEGER NOT NULL REFERENCES cycle_instances(id),
		step INTEGER NOT NULL,
		iteration INTEGER NOT NULL,
		score REAL NOT NULL,
		notes TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX idx_cycles_feature ON cycle_instances(feature_id);
	CREATE INDEX idx_cycles_status ON cycle_instances(status);
	CREATE INDEX idx_scores_cycle ON cycle_scores(cycle_id);`,

	// Migration 3: Add effort column to roadmap_items
	`ALTER TABLE roadmap_items ADD COLUMN effort TEXT NOT NULL DEFAULT '' CHECK(effort IN ('', 'xs', 's', 'm', 'l', 'xl'));`,

	// Migration 4: Add status column to roadmap_items (may already exist from migration 1)
	`ALTER TABLE roadmap_items ADD COLUMN status TEXT NOT NULL DEFAULT 'proposed' CHECK(status IN ('proposed','accepted','in-progress','completed','deferred'));`,

	// Migration 5: Add spec and roadmap_item_id columns to features for in-band context
	`ALTER TABLE features ADD COLUMN spec TEXT NOT NULL DEFAULT '';
	 ALTER TABLE features ADD COLUMN roadmap_item_id TEXT NOT NULL DEFAULT '' REFERENCES roadmap_items(id);`,

	// Migration 6: Discussion/RFC system for agent collaboration
	`CREATE TABLE discussions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id TEXT NOT NULL REFERENCES projects(id),
		feature_id TEXT REFERENCES features(id),
		title TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open','resolved','merged','closed')),
		author TEXT NOT NULL DEFAULT 'system',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE discussion_comments (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		discussion_id INTEGER NOT NULL REFERENCES discussions(id),
		author TEXT NOT NULL DEFAULT 'agent',
		content TEXT NOT NULL,
		parent_id INTEGER REFERENCES discussion_comments(id),
		comment_type TEXT NOT NULL DEFAULT 'comment' CHECK(comment_type IN ('comment','proposal','approval','objection','revision','decision')),
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX idx_discussions_project ON discussions(project_id);
	CREATE INDEX idx_discussions_feature ON discussions(feature_id);
	CREATE INDEX idx_discussions_status ON discussions(status);
	CREATE INDEX idx_comments_discussion ON discussion_comments(discussion_id);
	CREATE INDEX idx_comments_parent ON discussion_comments(parent_id);`,

	// Migration 7: Add body column to discussions
	`ALTER TABLE discussions ADD COLUMN body TEXT NOT NULL DEFAULT '';`,

	// Migration 8: Agent-first lifecycle tables
	`CREATE TABLE IF NOT EXISTS agent_sessions (
		id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES projects(id),
		feature_id TEXT REFERENCES features(id),
		name TEXT NOT NULL,
		task_description TEXT,
		status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','paused','completed','failed','abandoned')),
		progress_pct INTEGER DEFAULT 0,
		current_phase TEXT,
		eta TEXT,
		context_snapshot TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS status_updates (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		agent_session_id TEXT NOT NULL REFERENCES agent_sessions(id),
		message_md TEXT NOT NULL,
		progress_pct INTEGER,
		phase TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS idea_queue (
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

	CREATE TABLE IF NOT EXISTS context_entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id TEXT NOT NULL REFERENCES projects(id),
		feature_id TEXT REFERENCES features(id),
		context_type TEXT NOT NULL DEFAULT 'note' CHECK(context_type IN ('source-analysis','doc','spec','research','note','status-update','decision')),
		title TEXT NOT NULL,
		content_md TEXT NOT NULL,
		author TEXT DEFAULT 'system',
		tags TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX idx_agent_sessions_project ON agent_sessions(project_id);
	CREATE INDEX idx_agent_sessions_status ON agent_sessions(status);
	CREATE INDEX idx_status_updates_session ON status_updates(agent_session_id);
	CREATE INDEX idx_idea_queue_project ON idea_queue(project_id);
	CREATE INDEX idx_idea_queue_status ON idea_queue(status);
	CREATE INDEX idx_context_entries_project ON context_entries(project_id);
	CREATE INDEX idx_context_entries_feature ON context_entries(feature_id);`,

	// Migration 9: Worktree/workspace management
	`CREATE TABLE IF NOT EXISTS worktrees (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		path TEXT NOT NULL,
		branch TEXT DEFAULT '',
		agent_session_id TEXT DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (agent_session_id) REFERENCES agent_sessions(id)
	);

	ALTER TABLE agent_sessions ADD COLUMN worktree_id TEXT DEFAULT '';`,

	// Migration 10: Add 'feedback' to idea_type CHECK constraint
	`CREATE TABLE IF NOT EXISTS idea_queue_new (
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
	INSERT INTO idea_queue_new SELECT * FROM idea_queue;
	DROP TABLE idea_queue;
	ALTER TABLE idea_queue_new RENAME TO idea_queue;`,

	// Migration 11: Add previous_status column for blocking cascade
	`ALTER TABLE features ADD COLUMN previous_status TEXT DEFAULT '';`,

	// Migration 12: Add assigned_agent to work_items for multi-agent coordination
	`ALTER TABLE work_items ADD COLUMN assigned_agent TEXT DEFAULT '';`,
}
