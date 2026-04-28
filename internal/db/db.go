// Package db opens the SQLite store and provides the minimal query
// surface the post-reset app needs: projects, features, comments,
// config.
//
// Schema is applied via CREATE IF NOT EXISTS on Open. Per-column
// migrations use idempotent ALTER TABLE checks via PRAGMA table_info.
// This is intentionally simple — when the schema needs real
// versioning (rename / drop / type change), introduce a real
// migration system.
package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/internal/models"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS projects (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS features (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id      TEXT    NOT NULL REFERENCES projects(id),
    title           TEXT    NOT NULL,
    description     TEXT    NOT NULL DEFAULT '',
    status          TEXT    NOT NULL DEFAULT 'draft',
    target_persona  TEXT    NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_features_project ON features(project_id);
CREATE INDEX IF NOT EXISTS idx_features_persona ON features(target_persona);
CREATE INDEX IF NOT EXISTS idx_features_status  ON features(status);

CREATE TABLE IF NOT EXISTS comments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT    NOT NULL,
    entity_id   TEXT    NOT NULL,
    author_type TEXT    NOT NULL DEFAULT 'human',
    author_role TEXT    NOT NULL DEFAULT '',
    body        TEXT    NOT NULL,
    metadata    TEXT    NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_comments_entity ON comments(entity_type, entity_id);

CREATE TABLE IF NOT EXISTS config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
`

// Open returns a *sql.DB pointed at the given path, with the minimal
// schema applied and WAL mode + foreign keys enabled.
func Open(path string) (*sql.DB, error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	for _, pragma := range []string{
		"PRAGMA foreign_keys=ON",
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := database.Exec(pragma); err != nil {
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	if _, err := database.Exec(schema); err != nil {
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	if err := migrate(database); err != nil {
		return nil, fmt.Errorf("migrating: %w", err)
	}
	return database, nil
}

// migrate runs idempotent column-level migrations against existing
// tables. Each step checks pragma_table_info before adding.
func migrate(database *sql.DB) error {
	if !columnExists(database, "features", "target_persona") {
		_, err := database.Exec(
			`ALTER TABLE features ADD COLUMN target_persona TEXT NOT NULL DEFAULT ''`,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func columnExists(database *sql.DB, table, col string) bool {
	rows, err := database.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var (
			cid         int
			name, ctype string
			notnull, pk int
			defaultVal  sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &defaultVal, &pk); err != nil {
			return false
		}
		if name == col {
			return true
		}
	}
	return false
}

// CreateProject inserts a new project record. The slug-derived ID is
// stable for the project's lifetime.
func CreateProject(database *sql.DB, name string) (*models.Project, error) {
	id := slug(name)
	if _, err := database.Exec(
		"INSERT INTO projects (id, name) VALUES (?, ?)",
		id, name,
	); err != nil {
		return nil, err
	}
	return GetProject(database)
}

// GetProject returns the (single) project record. tillr is one-project-
// per-DB.
func GetProject(database *sql.DB) (*models.Project, error) {
	var p models.Project
	err := database.QueryRow(
		"SELECT id, name, created_at FROM projects ORDER BY created_at LIMIT 1",
	).Scan(&p.ID, &p.Name, &p.CreatedAt)
	return &p, err
}

// AddFeature inserts a new feature with optional target_persona.
// Pass an empty persona for an untyped feature.
func AddFeature(database *sql.DB, projectID, title, description, persona string) (*models.Feature, error) {
	res, err := database.Exec(
		"INSERT INTO features (project_id, title, description, target_persona) VALUES (?, ?, ?, ?)",
		projectID, title, description, persona,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return GetFeature(database, id)
}

// GetFeature returns a single feature by ID.
func GetFeature(database *sql.DB, id int64) (*models.Feature, error) {
	var f models.Feature
	err := database.QueryRow(
		`SELECT id, project_id, title, description, status, target_persona,
		        created_at, updated_at
		 FROM features WHERE id = ?`,
		id,
	).Scan(&f.ID, &f.ProjectID, &f.Title, &f.Description, &f.Status,
		&f.TargetPersona, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// ListFeaturesFilter holds optional filters for ListFeatures.
type ListFeaturesFilter struct {
	Persona string // exact match; "" = no filter
	Status  string // exact match; "" = no filter
}

// ListFeatures returns features for a project, newest first, with
// optional persona/status filters.
func ListFeatures(database *sql.DB, projectID string, filter ListFeaturesFilter) ([]models.Feature, error) {
	q := `SELECT id, project_id, title, description, status, target_persona,
	             created_at, updated_at
	      FROM features WHERE project_id = ?`
	args := []any{projectID}
	if filter.Persona != "" {
		q += " AND target_persona = ?"
		args = append(args, filter.Persona)
	}
	if filter.Status != "" {
		q += " AND status = ?"
		args = append(args, filter.Status)
	}
	q += " ORDER BY created_at DESC"

	rows, err := database.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Feature
	for rows.Next() {
		var f models.Feature
		if err := rows.Scan(
			&f.ID, &f.ProjectID, &f.Title, &f.Description, &f.Status,
			&f.TargetPersona, &f.CreatedAt, &f.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SetFeatureStatus updates a feature's status and bumps updated_at.
func SetFeatureStatus(database *sql.DB, id int64, status string) (*models.Feature, error) {
	_, err := database.Exec(
		`UPDATE features SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return nil, err
	}
	return GetFeature(database, id)
}

// ClaimNextFeature finds the next feature targeted at the given
// persona with status 'draft' or 'queued', transitions it to
// 'claimed', and returns it. Returns nil if no claimable feature
// exists. Idempotent under concurrent claims (sets updated_at).
func ClaimNextFeature(database *sql.DB, projectID, persona string) (*models.Feature, error) {
	if persona == "" {
		return nil, fmt.Errorf("persona is required")
	}
	var id int64
	err := database.QueryRow(
		`SELECT id FROM features
		 WHERE project_id = ? AND target_persona = ?
		   AND status IN ('draft', 'queued')
		 ORDER BY created_at ASC LIMIT 1`,
		projectID, persona,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return SetFeatureStatus(database, id, "claimed")
}

// AddComment inserts a comment. ID and CreatedAt are populated on return.
func AddComment(database *sql.DB, c *models.Comment) (*models.Comment, error) {
	res, err := database.Exec(
		`INSERT INTO comments (entity_type, entity_id, author_type, author_role, body, metadata)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		c.EntityType, c.EntityID, c.AuthorType, c.AuthorRole, c.Body, c.Metadata,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	c.ID = id
	c.CreatedAt = time.Now().UTC()
	return c, nil
}

// ListComments returns comments for an entity in chronological order.
func ListComments(database *sql.DB, entityType, entityID string) ([]models.Comment, error) {
	rows, err := database.Query(
		`SELECT id, entity_type, entity_id, author_type, author_role, body, metadata, created_at
		 FROM comments WHERE entity_type = ? AND entity_id = ? ORDER BY created_at ASC`,
		entityType, entityID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Comment
	for rows.Next() {
		var c models.Comment
		if err := rows.Scan(
			&c.ID, &c.EntityType, &c.EntityID, &c.AuthorType,
			&c.AuthorRole, &c.Body, &c.Metadata, &c.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ConfigGet returns the value for a key, or "" if not set.
func ConfigGet(database *sql.DB, key string) (string, error) {
	var v string
	err := database.QueryRow("SELECT value FROM config WHERE key = ?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// ConfigSet inserts or updates a config key/value pair.
func ConfigSet(database *sql.DB, key, value string) error {
	_, err := database.Exec(
		`INSERT INTO config (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

// ConfigList returns all config key/value pairs.
func ConfigList(database *sql.DB) (map[string]string, error) {
	rows, err := database.Query("SELECT key, value FROM config ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// slug normalizes a project name into an ID-safe slug.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '_', r == '-':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "project"
	}
	return out
}
