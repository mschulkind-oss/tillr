// Package db opens the SQLite store and provides the minimal query
// surface the post-reset app needs: projects, features, comments.
//
// Migrations are intentionally absent. The schema is applied via
// CREATE IF NOT EXISTS on Open. As soon as we need real schema
// evolution (any column rename / drop / type change), migrations come
// back. Until then, fresh-db-per-rev is fine.
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
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  TEXT    NOT NULL REFERENCES projects(id),
    title       TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL DEFAULT 'draft',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_features_project ON features(project_id);

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
	return database, nil
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

// AddFeature inserts a new feature.
func AddFeature(database *sql.DB, projectID, title, description string) (*models.Feature, error) {
	res, err := database.Exec(
		"INSERT INTO features (project_id, title, description) VALUES (?, ?, ?)",
		projectID, title, description,
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
		`SELECT id, project_id, title, description, status, created_at, updated_at
		 FROM features WHERE id = ?`,
		id,
	).Scan(&f.ID, &f.ProjectID, &f.Title, &f.Description, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// ListFeatures returns all features for a project, newest first.
func ListFeatures(database *sql.DB, projectID string) ([]models.Feature, error) {
	rows, err := database.Query(
		`SELECT id, project_id, title, description, status, created_at, updated_at
		 FROM features WHERE project_id = ? ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Feature
	for rows.Next() {
		var f models.Feature
		if err := rows.Scan(
			&f.ID, &f.ProjectID, &f.Title, &f.Description,
			&f.Status, &f.CreatedAt, &f.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
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
