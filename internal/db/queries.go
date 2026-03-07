package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind/lifecycle/internal/models"
)

// --- Projects ---

func CreateProject(db *sql.DB, p *models.Project) error {
	_, err := db.Exec(
		`INSERT INTO projects (id, name, description) VALUES (?, ?, ?)`,
		p.ID, p.Name, p.Description,
	)
	return err
}

func GetProject(db *sql.DB) (*models.Project, error) {
	row := db.QueryRow(`SELECT id, name, description, created_at, updated_at FROM projects LIMIT 1`)
	p := &models.Project{}
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// --- Milestones ---

func CreateMilestone(db *sql.DB, m *models.Milestone) error {
	_, err := db.Exec(
		`INSERT INTO milestones (id, project_id, name, description, sort_order) VALUES (?, ?, ?, ?, ?)`,
		m.ID, m.ProjectID, m.Name, m.Description, m.SortOrder,
	)
	return err
}

func GetMilestone(db *sql.DB, id string) (*models.Milestone, error) {
	row := db.QueryRow(`SELECT id, project_id, name, description, sort_order, status, created_at, updated_at FROM milestones WHERE id = ?`, id)
	m := &models.Milestone{}
	err := row.Scan(&m.ID, &m.ProjectID, &m.Name, &m.Description, &m.SortOrder, &m.Status, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func ListMilestones(db *sql.DB, projectID string) ([]models.Milestone, error) {
	rows, err := db.Query(`
		SELECT m.id, m.project_id, m.name, m.description, m.sort_order, m.status, m.created_at, m.updated_at,
			COUNT(f.id) AS total, COALESCE(SUM(CASE WHEN f.status = 'done' THEN 1 ELSE 0 END), 0) AS done
		FROM milestones m
		LEFT JOIN features f ON f.milestone_id = m.id
		WHERE m.project_id = ?
		GROUP BY m.id
		ORDER BY m.sort_order`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Milestone
	for rows.Next() {
		var m models.Milestone
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.Name, &m.Description, &m.SortOrder, &m.Status,
			&m.CreatedAt, &m.UpdatedAt, &m.TotalFeatures, &m.DoneFeatures); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- Features ---

func CreateFeature(db *sql.DB, f *models.Feature) error {
	_, err := db.Exec(
		`INSERT INTO features (id, project_id, milestone_id, name, description, spec, priority, roadmap_item_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.ProjectID, nullStr(f.MilestoneID), f.Name, f.Description, f.Spec, f.Priority, f.RoadmapItemID,
	)
	return err
}

func GetFeature(db *sql.DB, id string) (*models.Feature, error) {
	row := db.QueryRow(`
		SELECT f.id, f.project_id, COALESCE(f.milestone_id,''), f.name, COALESCE(f.description,''), COALESCE(f.spec,''),
			f.status, f.priority, COALESCE(f.assigned_cycle,''), COALESCE(f.roadmap_item_id,''),
			f.created_at, f.updated_at, COALESCE(m.name,'') AS ms_name, COALESCE(f.previous_status,'')
		FROM features f
		LEFT JOIN milestones m ON f.milestone_id = m.id
		WHERE f.id = ?`, id)
	f := &models.Feature{}
	err := row.Scan(&f.ID, &f.ProjectID, &f.MilestoneID, &f.Name, &f.Description, &f.Spec,
		&f.Status, &f.Priority, &f.AssignedCycle, &f.RoadmapItemID, &f.CreatedAt, &f.UpdatedAt, &f.MilestoneName, &f.PreviousStatus)
	if err != nil {
		return nil, err
	}
	deps, _ := featureDeps(db, id)
	f.DependsOn = deps
	return f, nil
}

func ListFeatures(db *sql.DB, projectID, status, milestoneID string) ([]models.Feature, error) {
	q := `SELECT f.id, f.project_id, COALESCE(f.milestone_id,''), f.name, COALESCE(f.description,''), COALESCE(f.spec,''),
			f.status, f.priority, COALESCE(f.assigned_cycle,''), COALESCE(f.roadmap_item_id,''),
			f.created_at, f.updated_at, COALESCE(m.name,''), COALESCE(f.previous_status,'')
		FROM features f LEFT JOIN milestones m ON f.milestone_id = m.id
		WHERE f.project_id = ?`
	args := []any{projectID}
	if status != "" {
		q += " AND f.status = ?"
		args = append(args, status)
	}
	if milestoneID != "" {
		q += " AND f.milestone_id = ?"
		args = append(args, milestoneID)
	}
	q += " ORDER BY f.priority DESC, f.created_at"

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Feature
	for rows.Next() {
		var f models.Feature
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.MilestoneID, &f.Name, &f.Description, &f.Spec,
			&f.Status, &f.Priority, &f.AssignedCycle, &f.RoadmapItemID, &f.CreatedAt, &f.UpdatedAt, &f.MilestoneName, &f.PreviousStatus); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Bulk-load dependencies for all features
	if len(out) > 0 {
		depRows, err := db.Query("SELECT feature_id, depends_on FROM feature_deps")
		if err == nil {
			defer depRows.Close() //nolint:errcheck
			depMap := make(map[string][]string)
			for depRows.Next() {
				var fid, dep string
				if err := depRows.Scan(&fid, &dep); err == nil {
					depMap[fid] = append(depMap[fid], dep)
				}
			}
			for i := range out {
				if deps, ok := depMap[out[i].ID]; ok {
					out[i].DependsOn = deps
				}
			}
		}
	}

	return out, nil
}

func UpdateFeature(db *sql.DB, id string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	var setClauses []string
	var args []any
	for col, val := range updates {
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}
	setClauses = append(setClauses, "updated_at = datetime('now')")
	args = append(args, id)
	_, err := db.Exec(
		fmt.Sprintf("UPDATE features SET %s WHERE id = ?", strings.Join(setClauses, ", ")),
		args...,
	)
	return err
}

func DeleteFeature(db *sql.DB, id string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec("DELETE FROM feature_deps WHERE feature_id = ? OR depends_on = ?", id, id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM work_items WHERE feature_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM qa_results WHERE feature_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM heartbeats WHERE feature_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM features WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func BatchUpdateFeatures(database *sql.DB, featureIDs []string, field, value string) (int, error) {
	validFields := map[string]string{
		"status":       "status",
		"milestone_id": "milestone_id",
		"priority":     "priority",
	}
	col, ok := validFields[field]
	if !ok {
		return 0, fmt.Errorf("invalid field for batch update: %s", field)
	}
	if len(featureIDs) == 0 {
		return 0, nil
	}

	placeholders := make([]string, len(featureIDs))
	args := make([]any, 0, len(featureIDs)+1)
	args = append(args, value)
	for i, id := range featureIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	q := fmt.Sprintf("UPDATE features SET %s = ?, updated_at = datetime('now') WHERE id IN (%s)",
		col, strings.Join(placeholders, ","))

	result, err := database.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

func AddFeatureDep(db *sql.DB, featureID, dependsOn string) error {
	_, err := db.Exec("INSERT OR IGNORE INTO feature_deps (feature_id, depends_on) VALUES (?, ?)", featureID, dependsOn)
	return err
}

func featureDeps(db *sql.DB, featureID string) ([]string, error) {
	rows, err := db.Query("SELECT depends_on FROM feature_deps WHERE feature_id = ?", featureID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var deps []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		deps = append(deps, d)
	}
	return deps, rows.Err()
}

// --- Work Items ---

func CreateWorkItem(db *sql.DB, w *models.WorkItem) error {
	res, err := db.Exec(
		`INSERT INTO work_items (feature_id, work_type, agent_prompt) VALUES (?, ?, ?)`,
		w.FeatureID, w.WorkType, w.AgentPrompt,
	)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	w.ID = int(id)
	return nil
}

func GetActiveWorkItem(db *sql.DB) (*models.WorkItem, error) {
	row := db.QueryRow(`SELECT id, feature_id, work_type, status, agent_prompt, COALESCE(result,''),
		COALESCE(assigned_agent,''), COALESCE(started_at,''), COALESCE(completed_at,''), created_at
		FROM work_items WHERE status = 'active' LIMIT 1`)
	w := &models.WorkItem{}
	err := row.Scan(&w.ID, &w.FeatureID, &w.WorkType, &w.Status, &w.AgentPrompt, &w.Result,
		&w.AssignedAgent, &w.StartedAt, &w.CompletedAt, &w.CreatedAt)
	if err != nil {
		return nil, err
	}
	return w, nil
}

// GetActiveWorkItemForAgent returns the active work item assigned to a specific agent.
func GetActiveWorkItemForAgent(db *sql.DB, agentID string) (*models.WorkItem, error) {
	row := db.QueryRow(`SELECT id, feature_id, work_type, status, agent_prompt, COALESCE(result,''),
		COALESCE(assigned_agent,''), COALESCE(started_at,''), COALESCE(completed_at,''), created_at
		FROM work_items WHERE status = 'active' AND assigned_agent = ? LIMIT 1`, agentID)
	w := &models.WorkItem{}
	err := row.Scan(&w.ID, &w.FeatureID, &w.WorkType, &w.Status, &w.AgentPrompt, &w.Result,
		&w.AssignedAgent, &w.StartedAt, &w.CompletedAt, &w.CreatedAt)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func GetNextPendingWorkItem(database *sql.DB) (*models.WorkItem, error) {
	// Priority ordering:
	// 1. Feature priority DESC (higher priority features first)
	// 2. Cycle step order ASC (earlier steps first — via cycle_instances current_step)
	// 3. Creation time ASC (older items first)
	row := database.QueryRow(`SELECT w.id, w.feature_id, w.work_type, w.status, w.agent_prompt, COALESCE(w.result,''),
		COALESCE(w.assigned_agent,''), COALESCE(w.started_at,''), COALESCE(w.completed_at,''), w.created_at
		FROM work_items w
		LEFT JOIN features f ON w.feature_id = f.id
		LEFT JOIN cycle_instances ci ON ci.feature_id = w.feature_id AND ci.status = 'active'
		WHERE w.status = 'pending'
		ORDER BY COALESCE(f.priority, 0) DESC, COALESCE(ci.current_step, 0) ASC, w.created_at ASC
		LIMIT 1`)
	w := &models.WorkItem{}
	err := row.Scan(&w.ID, &w.FeatureID, &w.WorkType, &w.Status, &w.AgentPrompt, &w.Result,
		&w.AssignedAgent, &w.StartedAt, &w.CompletedAt, &w.CreatedAt)
	if err != nil {
		return nil, err
	}
	return w, nil
}

// ClaimWorkItem atomically claims a pending work item for an agent.
func ClaimWorkItem(database *sql.DB, workItemID int, agentID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := database.Exec(
		`UPDATE work_items SET status = 'active', assigned_agent = ?, started_at = ?
		 WHERE id = ? AND status = 'pending'`, agentID, now, workItemID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("work item %d is not pending (already claimed or completed)", workItemID)
	}
	return nil
}

func UpdateWorkItemStatus(db *sql.DB, id int, status, result string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	switch status {
	case "active":
		_, err := db.Exec("UPDATE work_items SET status = ?, started_at = ? WHERE id = ?", status, now, id)
		return err
	case "done", "failed":
		_, err := db.Exec("UPDATE work_items SET status = ?, result = ?, completed_at = ? WHERE id = ?", status, result, now, id)
		return err
	default:
		_, err := db.Exec("UPDATE work_items SET status = ? WHERE id = ?", status, id)
		return err
	}
}

// ListWorkItemsForFeature returns completed work items for a feature, most recent first.
func ListWorkItemsForFeature(db *sql.DB, featureID string) ([]models.WorkItem, error) {
	rows, err := db.Query(`SELECT id, feature_id, work_type, status, COALESCE(agent_prompt,''), COALESCE(result,''),
		COALESCE(assigned_agent,''), COALESCE(started_at,''), COALESCE(completed_at,''), created_at
		FROM work_items WHERE feature_id = ? AND status IN ('done','failed') ORDER BY completed_at DESC`, featureID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []models.WorkItem
	for rows.Next() {
		var w models.WorkItem
		if err := rows.Scan(&w.ID, &w.FeatureID, &w.WorkType, &w.Status, &w.AgentPrompt, &w.Result,
			&w.AssignedAgent, &w.StartedAt, &w.CompletedAt, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// --- Events ---

func InsertEvent(db *sql.DB, e *models.Event) error {
	_, err := db.Exec(
		`INSERT INTO events (project_id, feature_id, event_type, data) VALUES (?, ?, ?, ?)`,
		e.ProjectID, nullStr(e.FeatureID), e.EventType, e.Data,
	)
	return err
}

func ListEvents(db *sql.DB, projectID, featureID, eventType, since string, limit int) ([]models.Event, error) {
	q := `SELECT id, project_id, COALESCE(feature_id,''), event_type, COALESCE(data,''), created_at
		FROM events WHERE project_id = ?`
	args := []any{projectID}
	if featureID != "" {
		q += " AND feature_id = ?"
		args = append(args, featureID)
	}
	if eventType != "" {
		q += " AND event_type = ?"
		args = append(args, eventType)
	}
	if since != "" {
		q += " AND created_at >= ?"
		args = append(args, since)
	}
	q += " ORDER BY created_at DESC"
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Event
	for rows.Next() {
		var e models.Event
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.FeatureID, &e.EventType, &e.Data, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- Roadmap Items ---

func CreateRoadmapItem(db *sql.DB, r *models.RoadmapItem) error {
	_, err := db.Exec(
		`INSERT INTO roadmap_items (id, project_id, title, description, category, priority, sort_order, effort) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ProjectID, r.Title, r.Description, r.Category, r.Priority, r.SortOrder, r.Effort,
	)
	return err
}

func GetRoadmapItem(db *sql.DB, id string) (*models.RoadmapItem, error) {
	row := db.QueryRow(`SELECT id, project_id, title, description, COALESCE(category,''), priority, status, COALESCE(effort,''), sort_order, created_at, updated_at
		FROM roadmap_items WHERE id = ?`, id)
	r := &models.RoadmapItem{}
	err := row.Scan(&r.ID, &r.ProjectID, &r.Title, &r.Description, &r.Category, &r.Priority, &r.Status, &r.Effort, &r.SortOrder, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func ListRoadmapItems(db *sql.DB, projectID string) ([]models.RoadmapItem, error) {
	rows, err := db.Query(`SELECT id, project_id, title, description, COALESCE(category,''), priority, status, COALESCE(effort,''), sort_order, created_at, updated_at
		FROM roadmap_items WHERE project_id = ? ORDER BY sort_order, created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.RoadmapItem
	for rows.Next() {
		var r models.RoadmapItem
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Title, &r.Description, &r.Category, &r.Priority, &r.Status, &r.Effort, &r.SortOrder, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func ListRoadmapItemsFiltered(database *sql.DB, projectID, category, priority, status, sort string) ([]models.RoadmapItem, error) {
	q := `SELECT id, project_id, title, description, COALESCE(category,''), priority, status, COALESCE(effort,''), sort_order, created_at, updated_at
		FROM roadmap_items WHERE project_id = ?`
	args := []any{projectID}

	if category != "" {
		q += " AND category = ?"
		args = append(args, category)
	}
	if priority != "" {
		q += " AND priority = ?"
		args = append(args, priority)
	}
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}

	switch sort {
	case "title":
		q += " ORDER BY title ASC, created_at ASC"
	case "category":
		q += " ORDER BY category ASC, created_at ASC"
	case "created_at":
		q += " ORDER BY created_at ASC"
	default:
		// Sort by priority weight: critical first, then high, medium, low, nice-to-have
		q += ` ORDER BY CASE priority
			WHEN 'critical' THEN 1
			WHEN 'high' THEN 2
			WHEN 'medium' THEN 3
			WHEN 'low' THEN 4
			WHEN 'nice-to-have' THEN 5
			ELSE 99
		END ASC, created_at ASC`
	}

	rows, err := database.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("querying filtered roadmap items: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var out []models.RoadmapItem
	for rows.Next() {
		var r models.RoadmapItem
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Title, &r.Description, &r.Category, &r.Priority, &r.Status, &r.Effort, &r.SortOrder, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func UpdateRoadmapItem(db *sql.DB, id string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	var setClauses []string
	var args []any
	for col, val := range updates {
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}
	setClauses = append(setClauses, "updated_at = datetime('now')")
	args = append(args, id)
	_, err := db.Exec(
		fmt.Sprintf("UPDATE roadmap_items SET %s WHERE id = ?", strings.Join(setClauses, ", ")),
		args...,
	)
	return err
}

func UpdateRoadmapItemStatus(db *sql.DB, id, status string) error {
	res, err := db.Exec(
		`UPDATE roadmap_items SET status = ?, updated_at = datetime('now') WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("updating roadmap item status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func UpdateMilestone(db *sql.DB, id string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	var setClauses []string
	var args []any
	for col, val := range updates {
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}
	setClauses = append(setClauses, "updated_at = datetime('now')")
	args = append(args, id)
	res, err := db.Exec(
		fmt.Sprintf("UPDATE milestones SET %s WHERE id = ?", strings.Join(setClauses, ", ")),
		args...,
	)
	if err != nil {
		return fmt.Errorf("updating milestone: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// --- Roadmap Reorder ---

// ReorderItem represents an item ID and its new sort order.
type ReorderItem struct {
	ID        string `json:"id"`
	SortOrder int    `json:"sort_order"`
}

// ReorderRoadmapItems updates the sort_order for multiple roadmap items in a transaction.
func ReorderRoadmapItems(database *sql.DB, items []ReorderItem) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	for _, item := range items {
		if _, err := tx.Exec(
			"UPDATE roadmap_items SET sort_order = ?, updated_at = datetime('now') WHERE id = ?",
			item.SortOrder, item.ID,
		); err != nil {
			return fmt.Errorf("updating roadmap item %s: %w", item.ID, err)
		}
	}
	return tx.Commit()
}

// FeaturePriorityItem represents a feature ID and its new priority.
type FeaturePriorityItem struct {
	ID       string `json:"id"`
	Priority int    `json:"priority"`
}

// ReorderFeaturePriorities updates the priority for multiple features in a transaction.
func ReorderFeaturePriorities(database *sql.DB, items []FeaturePriorityItem) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	for _, item := range items {
		if _, err := tx.Exec(
			"UPDATE features SET priority = ?, updated_at = datetime('now') WHERE id = ?",
			item.Priority, item.ID,
		); err != nil {
			return fmt.Errorf("updating feature priority %s: %w", item.ID, err)
		}
	}
	return tx.Commit()
}

// --- Roadmap Stats ---

// RoadmapStats holds aggregated roadmap statistics.
type RoadmapStats struct {
	Total      int            `json:"total"`
	ByPriority map[string]int `json:"by_priority"`
	ByCategory map[string]int `json:"by_category"`
	ByStatus   map[string]int `json:"by_status"`
}

// GetRoadmapStats returns aggregated counts for roadmap items.
func GetRoadmapStats(d *sql.DB, projectID string) (*RoadmapStats, error) {
	stats := &RoadmapStats{
		ByPriority: make(map[string]int),
		ByCategory: make(map[string]int),
		ByStatus:   make(map[string]int),
	}

	// Total count
	row := d.QueryRow(`SELECT COUNT(*) FROM roadmap_items WHERE project_id = ?`, projectID)
	if err := row.Scan(&stats.Total); err != nil {
		return nil, fmt.Errorf("counting roadmap items: %w", err)
	}

	// By priority
	rows, err := d.Query(`SELECT priority, COUNT(*) FROM roadmap_items WHERE project_id = ? GROUP BY priority`, projectID)
	if err != nil {
		return nil, fmt.Errorf("querying priority counts: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return nil, err
		}
		stats.ByPriority[key] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// By category
	rows2, err := d.Query(`SELECT COALESCE(NULLIF(category,''),'uncategorized'), COUNT(*) FROM roadmap_items WHERE project_id = ? GROUP BY COALESCE(NULLIF(category,''),'uncategorized')`, projectID)
	if err != nil {
		return nil, fmt.Errorf("querying category counts: %w", err)
	}
	defer rows2.Close() //nolint:errcheck
	for rows2.Next() {
		var key string
		var count int
		if err := rows2.Scan(&key, &count); err != nil {
			return nil, err
		}
		stats.ByCategory[key] = count
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	// By status
	rows3, err := d.Query(`SELECT status, COUNT(*) FROM roadmap_items WHERE project_id = ? GROUP BY status`, projectID)
	if err != nil {
		return nil, fmt.Errorf("querying status counts: %w", err)
	}
	defer rows3.Close() //nolint:errcheck
	for rows3.Next() {
		var key string
		var count int
		if err := rows3.Scan(&key, &count); err != nil {
			return nil, err
		}
		stats.ByStatus[key] = count
	}
	if err := rows3.Err(); err != nil {
		return nil, err
	}

	return stats, nil
}

// --- QA Results ---

func CreateQAResult(db *sql.DB, q *models.QAResult) error {
	passed := 0
	if q.Passed {
		passed = 1
	}
	_, err := db.Exec(
		`INSERT INTO qa_results (feature_id, qa_type, passed, notes, checklist) VALUES (?, ?, ?, ?, ?)`,
		q.FeatureID, q.QAType, passed, q.Notes, q.Checklist,
	)
	return err
}

func ListQAResults(db *sql.DB, featureID string) ([]models.QAResult, error) {
	rows, err := db.Query(`SELECT id, feature_id, qa_type, passed, COALESCE(notes,''), COALESCE(checklist,''), created_at
		FROM qa_results WHERE feature_id = ? ORDER BY created_at DESC`, featureID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.QAResult
	for rows.Next() {
		var q models.QAResult
		var passed int
		if err := rows.Scan(&q.ID, &q.FeatureID, &q.QAType, &passed, &q.Notes, &q.Checklist, &q.CreatedAt); err != nil {
			return nil, err
		}
		q.Passed = passed != 0
		out = append(out, q)
	}
	return out, rows.Err()
}

// --- Heartbeats ---

func CreateHeartbeat(db *sql.DB, h *models.Heartbeat) error {
	_, err := db.Exec(
		`INSERT INTO heartbeats (feature_id, agent_id, message) VALUES (?, ?, ?)`,
		h.FeatureID, h.AgentID, h.Message,
	)
	return err
}

// --- Cycles ---

func CreateCycleInstance(db *sql.DB, c *models.CycleInstance) error {
	res, err := db.Exec(
		`INSERT INTO cycle_instances (feature_id, cycle_type, current_step, iteration, status) VALUES (?, ?, ?, ?, ?)`,
		c.FeatureID, c.CycleType, c.CurrentStep, c.Iteration, c.Status,
	)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	c.ID = int(id)
	return nil
}

func GetActiveCycle(db *sql.DB, featureID string) (*models.CycleInstance, error) {
	row := db.QueryRow(`SELECT id, feature_id, cycle_type, current_step, iteration, status, created_at, updated_at
		FROM cycle_instances WHERE feature_id = ? AND status = 'active' LIMIT 1`, featureID)
	c := &models.CycleInstance{}
	err := row.Scan(&c.ID, &c.FeatureID, &c.CycleType, &c.CurrentStep, &c.Iteration, &c.Status, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func GetCycleByID(db *sql.DB, id int) (*models.CycleInstance, error) {
	row := db.QueryRow(`SELECT id, feature_id, cycle_type, current_step, iteration, status, created_at, updated_at
		FROM cycle_instances WHERE id = ?`, id)
	c := &models.CycleInstance{}
	err := row.Scan(&c.ID, &c.FeatureID, &c.CycleType, &c.CurrentStep, &c.Iteration, &c.Status, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func ListActiveCycles(db *sql.DB) ([]models.CycleInstance, error) {
	rows, err := db.Query(`SELECT id, feature_id, cycle_type, current_step, iteration, status, created_at, updated_at
		FROM cycle_instances WHERE status = 'active' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.CycleInstance
	for rows.Next() {
		var c models.CycleInstance
		if err := rows.Scan(&c.ID, &c.FeatureID, &c.CycleType, &c.CurrentStep, &c.Iteration, &c.Status, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func ListAllCycles(db *sql.DB) ([]models.CycleInstance, error) {
	rows, err := db.Query(`SELECT id, feature_id, cycle_type, current_step, iteration, status, created_at, updated_at
		FROM cycle_instances ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.CycleInstance
	for rows.Next() {
		var c models.CycleInstance
		if err := rows.Scan(&c.ID, &c.FeatureID, &c.CycleType, &c.CurrentStep, &c.Iteration, &c.Status, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func UpdateCycleInstance(db *sql.DB, id int, step, iteration int, status string) error {
	_, err := db.Exec(`UPDATE cycle_instances SET current_step = ?, iteration = ?, status = ?, updated_at = datetime('now') WHERE id = ?`,
		step, iteration, status, id)
	return err
}

func ListCycleHistory(db *sql.DB, featureID string) ([]models.CycleInstance, error) {
	rows, err := db.Query(`SELECT id, feature_id, cycle_type, current_step, iteration, status, created_at, updated_at
		FROM cycle_instances WHERE feature_id = ? ORDER BY created_at`, featureID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.CycleInstance
	for rows.Next() {
		var c models.CycleInstance
		if err := rows.Scan(&c.ID, &c.FeatureID, &c.CycleType, &c.CurrentStep, &c.Iteration, &c.Status, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func CreateCycleScore(db *sql.DB, s *models.CycleScore) error {
	_, err := db.Exec(
		`INSERT INTO cycle_scores (cycle_id, step, iteration, score, notes) VALUES (?, ?, ?, ?, ?)`,
		s.CycleID, s.Step, s.Iteration, s.Score, s.Notes,
	)
	return err
}

func ListCycleScores(db *sql.DB, cycleID int) ([]models.CycleScore, error) {
	rows, err := db.Query(`SELECT id, cycle_id, step, iteration, score, COALESCE(notes,''), created_at
		FROM cycle_scores WHERE cycle_id = ? ORDER BY created_at`, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.CycleScore
	for rows.Next() {
		var s models.CycleScore
		if err := rows.Scan(&s.ID, &s.CycleID, &s.Step, &s.Iteration, &s.Score, &s.Notes, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- Search ---

func SearchEvents(db *sql.DB, projectID, query string) ([]models.Event, error) {
	q := `SELECT id, project_id, COALESCE(feature_id,''), event_type, COALESCE(data,''), created_at
		FROM events WHERE project_id = ? AND data LIKE ? ORDER BY created_at DESC LIMIT 50`
	rows, err := db.Query(q, projectID, "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Event
	for rows.Next() {
		var e models.Event
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.FeatureID, &e.EventType, &e.Data, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- Discussions ---

func CreateDiscussion(db *sql.DB, d *models.Discussion) error {
	res, err := db.Exec(
		`INSERT INTO discussions (project_id, feature_id, title, body, author) VALUES (?, ?, ?, ?, ?)`,
		d.ProjectID, nullStr(d.FeatureID), d.Title, d.Body, d.Author,
	)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	d.ID = int(id)
	return nil
}

func GetDiscussion(db *sql.DB, id int) (*models.Discussion, error) {
	row := db.QueryRow(`SELECT id, project_id, COALESCE(feature_id,''), title, body, status, author, created_at, updated_at
		FROM discussions WHERE id = ?`, id)
	d := &models.Discussion{}
	err := row.Scan(&d.ID, &d.ProjectID, &d.FeatureID, &d.Title, &d.Body, &d.Status, &d.Author, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	comments, _ := ListDiscussionComments(db, d.ID)
	d.Comments = comments
	d.CommentCount = len(comments)
	return d, nil
}

func ListDiscussions(db *sql.DB, projectID, featureID, status string) ([]models.Discussion, error) {
	q := `SELECT d.id, d.project_id, COALESCE(d.feature_id,''), d.title, d.body, d.status, d.author, d.created_at, d.updated_at,
			(SELECT COUNT(*) FROM discussion_comments WHERE discussion_id = d.id) as comment_count
		FROM discussions d WHERE d.project_id = ?`
	args := []any{projectID}
	if featureID != "" {
		q += " AND d.feature_id = ?"
		args = append(args, featureID)
	}
	if status != "" {
		q += " AND d.status = ?"
		args = append(args, status)
	}
	q += " ORDER BY d.updated_at DESC"

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Discussion
	for rows.Next() {
		var d models.Discussion
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.FeatureID, &d.Title, &d.Body, &d.Status, &d.Author,
			&d.CreatedAt, &d.UpdatedAt, &d.CommentCount); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func UpdateDiscussionStatus(db *sql.DB, id int, status string) error {
	_, err := db.Exec("UPDATE discussions SET status = ?, updated_at = datetime('now') WHERE id = ?", status, id)
	return err
}

func AddDiscussionComment(db *sql.DB, c *models.DiscussionComment) error {
	res, err := db.Exec(
		`INSERT INTO discussion_comments (discussion_id, author, content, parent_id, comment_type) VALUES (?, ?, ?, ?, ?)`,
		c.DiscussionID, c.Author, c.Content, nullInt(c.ParentID), c.CommentType,
	)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	c.ID = int(id)
	// Touch discussion updated_at
	_, _ = db.Exec("UPDATE discussions SET updated_at = datetime('now') WHERE id = ?", c.DiscussionID)
	return nil
}

func ListDiscussionComments(db *sql.DB, discussionID int) ([]models.DiscussionComment, error) {
	rows, err := db.Query(`SELECT id, discussion_id, author, content, COALESCE(parent_id,0), comment_type, created_at
		FROM discussion_comments WHERE discussion_id = ? ORDER BY created_at`, discussionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.DiscussionComment
	for rows.Next() {
		var c models.DiscussionComment
		if err := rows.Scan(&c.ID, &c.DiscussionID, &c.Author, &c.Content, &c.ParentID, &c.CommentType, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- Helpers ---

func nullInt(i int) any {
	if i == 0 {
		return nil
	}
	return i
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// FeatureCounts returns a map of status → count.
func FeatureCounts(db *sql.DB, projectID string) (map[string]int, error) {
	rows, err := db.Query(`SELECT status, COUNT(*) FROM features WHERE project_id = ? GROUP BY status`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

func PendingQAFeatures(db *sql.DB, projectID string) ([]models.Feature, error) {
	return ListFeatures(db, projectID, "human-qa", "")
}

// SetFeatureStatus directly updates a feature's status, bypassing transition validation.
// Used during onboarding to set status on newly created features.
func SetFeatureStatus(db *sql.DB, featureID, status string) error {
	_, err := db.Exec(`UPDATE features SET status = ?, updated_at = datetime('now') WHERE id = ?`, status, featureID)
	return err
}

// ProjectStats holds all aggregated statistics for the project overview.
type ProjectStats struct {
	FeatureStats   FeatureStats    `json:"feature_stats"`
	CycleStats     CycleStatsData  `json:"cycle_stats"`
	RoadmapStats   *RoadmapStats   `json:"roadmap_stats"`
	MilestoneStats []MilestoneStat `json:"milestone_stats"`
	Activity       ActivityStats   `json:"activity"`
}

type FeatureStats struct {
	Total          int            `json:"total"`
	ByStatus       map[string]int `json:"by_status"`
	CompletionRate float64        `json:"completion_rate"`
}

type CycleStatsData struct {
	TotalCycles     int              `json:"total_cycles"`
	TotalIterations int              `json:"total_iterations"`
	AvgScore        float64          `json:"avg_score"`
	ScoresOverTime  []ScoreDataPoint `json:"scores_over_time"`
}

type ScoreDataPoint struct {
	Date  string  `json:"date"`
	Score float64 `json:"score"`
	Cycle string  `json:"cycle"`
}

type MilestoneStat struct {
	Name     string  `json:"name"`
	Total    int     `json:"total"`
	Done     int     `json:"done"`
	Progress float64 `json:"progress"`
}

type ActivityStats struct {
	TotalEvents      int `json:"total_events"`
	EventsLast7Days  int `json:"events_last_7_days"`
	EventsLast30Days int `json:"events_last_30_days"`
}

// GetProjectStats returns aggregated project statistics for the stats page.
func GetProjectStats(database *sql.DB, projectID string) (*ProjectStats, error) {
	stats := &ProjectStats{}

	// Feature stats
	featureCounts, err := FeatureCounts(database, projectID)
	if err != nil {
		return nil, fmt.Errorf("getting feature counts: %w", err)
	}
	total := 0
	for _, c := range featureCounts {
		total += c
	}
	done := featureCounts["done"]
	completionRate := 0.0
	if total > 0 {
		completionRate = float64(done) / float64(total) * 100
	}
	stats.FeatureStats = FeatureStats{
		Total:          total,
		ByStatus:       featureCounts,
		CompletionRate: completionRate,
	}

	// Cycle stats
	var totalCycles, totalIterations int
	var avgScore sql.NullFloat64
	err = database.QueryRow(`SELECT COUNT(*), COALESCE(SUM(iteration), 0) FROM cycle_instances WHERE feature_id IN (SELECT id FROM features WHERE project_id = ?)`, projectID).Scan(&totalCycles, &totalIterations)
	if err != nil {
		return nil, fmt.Errorf("getting cycle counts: %w", err)
	}
	err = database.QueryRow(`SELECT AVG(cs.score) FROM cycle_scores cs JOIN cycle_instances ci ON cs.cycle_id = ci.id WHERE ci.feature_id IN (SELECT id FROM features WHERE project_id = ?)`, projectID).Scan(&avgScore)
	if err != nil {
		return nil, fmt.Errorf("getting avg score: %w", err)
	}
	avg := 0.0
	if avgScore.Valid {
		avg = avgScore.Float64
	}

	// Scores over time
	scoreRows, err := database.Query(`SELECT date(cs.created_at) AS d, cs.score, ci.cycle_type
		FROM cycle_scores cs
		JOIN cycle_instances ci ON cs.cycle_id = ci.id
		WHERE ci.feature_id IN (SELECT id FROM features WHERE project_id = ?)
		ORDER BY cs.created_at`, projectID)
	if err != nil {
		return nil, fmt.Errorf("getting scores over time: %w", err)
	}
	defer scoreRows.Close() //nolint:errcheck
	var scoresOverTime []ScoreDataPoint
	for scoreRows.Next() {
		var s ScoreDataPoint
		if err := scoreRows.Scan(&s.Date, &s.Score, &s.Cycle); err != nil {
			return nil, err
		}
		scoresOverTime = append(scoresOverTime, s)
	}
	if err := scoreRows.Err(); err != nil {
		return nil, err
	}
	if scoresOverTime == nil {
		scoresOverTime = []ScoreDataPoint{}
	}

	stats.CycleStats = CycleStatsData{
		TotalCycles:     totalCycles,
		TotalIterations: totalIterations,
		AvgScore:        avg,
		ScoresOverTime:  scoresOverTime,
	}

	// Roadmap stats (reuse existing function)
	roadmapStats, err := GetRoadmapStats(database, projectID)
	if err != nil {
		return nil, fmt.Errorf("getting roadmap stats: %w", err)
	}
	stats.RoadmapStats = roadmapStats

	// Milestone stats
	milestones, err := ListMilestones(database, projectID)
	if err != nil {
		return nil, fmt.Errorf("getting milestones: %w", err)
	}
	var milestoneStats []MilestoneStat
	for _, m := range milestones {
		progress := 0.0
		if m.TotalFeatures > 0 {
			progress = float64(m.DoneFeatures) / float64(m.TotalFeatures) * 100
		}
		milestoneStats = append(milestoneStats, MilestoneStat{
			Name:     m.Name,
			Total:    m.TotalFeatures,
			Done:     m.DoneFeatures,
			Progress: progress,
		})
	}
	if milestoneStats == nil {
		milestoneStats = []MilestoneStat{}
	}
	stats.MilestoneStats = milestoneStats

	// Activity stats
	var totalEvents int
	err = database.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ?`, projectID).Scan(&totalEvents)
	if err != nil {
		return nil, fmt.Errorf("getting total events: %w", err)
	}
	var events7 int
	err = database.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ? AND created_at >= datetime('now', '-7 days')`, projectID).Scan(&events7)
	if err != nil {
		return nil, fmt.Errorf("getting 7-day events: %w", err)
	}
	var events30 int
	err = database.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ? AND created_at >= datetime('now', '-30 days')`, projectID).Scan(&events30)
	if err != nil {
		return nil, fmt.Errorf("getting 30-day events: %w", err)
	}
	stats.Activity = ActivityStats{
		TotalEvents:      totalEvents,
		EventsLast7Days:  events7,
		EventsLast30Days: events30,
	}

	return stats, nil
}

// CountFeaturesWithoutSpecs returns the number of features missing specs.
func CountFeaturesWithoutSpecs(db *sql.DB, projectID string) (int, int, error) {
	var total, withSpecs int
	err := db.QueryRow(`SELECT COUNT(*) FROM features WHERE project_id = ?`, projectID).Scan(&total)
	if err != nil {
		return 0, 0, err
	}
	err = db.QueryRow(`SELECT COUNT(*) FROM features WHERE project_id = ? AND spec != ''`, projectID).Scan(&withSpecs)
	if err != nil {
		return 0, 0, err
	}
	return total, withSpecs, nil
}

// CountFeaturesWithoutRoadmap returns features missing roadmap item links.
func CountFeaturesWithoutRoadmap(db *sql.DB, projectID string) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM features WHERE project_id = ? AND roadmap_item_id = ''`, projectID).Scan(&count)
	return count, err
}

// CountDiscussions returns the total number of discussions.
func CountDiscussions(db *sql.DB, projectID string) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM discussions WHERE project_id = ?`, projectID).Scan(&count)
	return count, err
}

// CountMilestones returns the total number of milestones.
func CountMilestones(db *sql.DB, projectID string) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM milestones WHERE project_id = ?`, projectID).Scan(&count)
	return count, err
}

// GetFeatureDependencyTree returns a feature and all its transitive dependencies.
// It walks the feature_deps graph breadth-first to collect the full tree.
func GetFeatureDependencyTree(database *sql.DB, featureID string) ([]models.Feature, error) {
	seen := map[string]bool{}
	queue := []string{featureID}
	var result []models.Feature

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current] {
			continue
		}
		seen[current] = true

		f, err := GetFeature(database, current)
		if err != nil {
			continue // skip missing features
		}
		result = append(result, *f)

		for _, dep := range f.DependsOn {
			if !seen[dep] {
				queue = append(queue, dep)
			}
		}
	}
	return result, nil
}

// GetFeatureDependents returns features that directly depend on the given feature.
func GetFeatureDependents(database *sql.DB, featureID string) ([]models.Feature, error) {
	rows, err := database.Query(`SELECT feature_id FROM feature_deps WHERE depends_on = ?`, featureID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var result []models.Feature
	for _, id := range ids {
		f, err := GetFeature(database, id)
		if err != nil {
			continue
		}
		result = append(result, *f)
	}
	return result, nil
}

// GetBlockedFeatures returns features that have incomplete dependencies.
// A feature is considered blocked if any of its dependencies are not "done".
func GetBlockedFeatures(database *sql.DB) ([]models.Feature, error) {
	rows, err := database.Query(`
		SELECT DISTINCT fd.feature_id
		FROM feature_deps fd
		JOIN features f ON fd.depends_on = f.id
		WHERE f.status != 'done'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var result []models.Feature
	for _, id := range ids {
		f, err := GetFeature(database, id)
		if err != nil {
			continue
		}
		result = append(result, *f)
	}
	return result, nil
}

// AreAllDependenciesClear checks if all of a feature's dependencies are not blocked.
func AreAllDependenciesClear(database *sql.DB, featureID string) (bool, error) {
	var count int
	err := database.QueryRow(`
		SELECT COUNT(*) FROM feature_deps fd
		JOIN features f ON fd.depends_on = f.id
		WHERE fd.feature_id = ? AND f.status = 'blocked'`, featureID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

// SavePreviousStatus stores the feature's current status before blocking, so it can be restored on unblock.
func SavePreviousStatus(database *sql.DB, featureID, status string) error {
	_, err := database.Exec(`UPDATE features SET previous_status = ?, updated_at = datetime('now') WHERE id = ?`, status, featureID)
	return err
}

// GetPreviousStatus retrieves the saved previous status for a feature.
func GetPreviousStatus(database *sql.DB, featureID string) (string, error) {
	var status string
	err := database.QueryRow(`SELECT COALESCE(previous_status,'') FROM features WHERE id = ?`, featureID).Scan(&status)
	return status, err
}

// GetAllTransitiveDependents walks the reverse dependency graph via BFS and returns
// all features that transitively depend on the given feature.
func GetAllTransitiveDependents(database *sql.DB, featureID string) ([]models.Feature, error) {
	visited := map[string]bool{}
	queue := []string{featureID}
	var result []models.Feature

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if visited[current] {
			continue
		}
		visited[current] = true

		deps, err := GetFeatureDependents(database, current)
		if err != nil {
			return nil, err
		}
		for _, d := range deps {
			if !visited[d.ID] {
				result = append(result, d)
				queue = append(queue, d.ID)
			}
		}
	}
	return result, nil
}

// BurndownPoint represents a single day's data for burndown/velocity charts.
type BurndownPoint struct {
	Date      string `json:"date"`
	Remaining int    `json:"remaining"`
	Done      int    `json:"done"`
	Total     int    `json:"total"`
}

// BurndownData contains all data needed for burndown and velocity charts.
type BurndownData struct {
	Points   []BurndownPoint `json:"points"`
	Velocity []WeekVelocity  `json:"velocity"`
}

// WeekVelocity represents features completed in a given week.
type WeekVelocity struct {
	Week      string `json:"week"`
	Completed int    `json:"completed"`
}

// GetBurndownData computes burndown chart data from feature events.
func GetBurndownData(database *sql.DB, projectID string) (*BurndownData, error) {
	rows, err := database.Query(
		`SELECT event_type, COALESCE(data,''), created_at FROM events
		 WHERE project_id = ? AND event_type IN ('feature.created','feature.status_changed')
		 ORDER BY created_at ASC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("querying burndown events: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	type dayEvent struct {
		date      string
		eventType string
		data      string
	}
	var events []dayEvent
	for rows.Next() {
		var ev dayEvent
		var ts string
		if err := rows.Scan(&ev.eventType, &ev.data, &ts); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		if len(ts) >= 10 {
			ev.date = ts[:10]
		} else {
			ev.date = ts
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating events: %w", err)
	}

	if len(events) == 0 {
		return &BurndownData{Points: []BurndownPoint{}, Velocity: []WeekVelocity{}}, nil
	}

	total := 0
	done := 0
	dayMap := make(map[string]BurndownPoint)
	var dateOrder []string

	for _, ev := range events {
		if _, exists := dayMap[ev.date]; !exists {
			dateOrder = append(dateOrder, ev.date)
		}

		switch ev.eventType {
		case "feature.created":
			total++
		case "feature.status_changed":
			from := extractJSONField(ev.data, "from")
			to := extractJSONField(ev.data, "to")
			if to == "done" && from != "done" {
				done++
			} else if from == "done" && to != "done" {
				done--
			}
		}

		dayMap[ev.date] = BurndownPoint{
			Date:      ev.date,
			Remaining: total - done,
			Done:      done,
			Total:     total,
		}
	}

	// Fill gaps between dates and extend to today
	points := make([]BurndownPoint, 0, len(dateOrder)+30)
	today := time.Now().Format("2006-01-02")

	if len(dateOrder) > 0 {
		startDate, _ := time.Parse("2006-01-02", dateOrder[0])
		endDate, _ := time.Parse("2006-01-02", today)
		if endDate.Before(startDate) {
			endDate = startDate
		}

		lastPoint := BurndownPoint{Date: dateOrder[0], Total: 0, Done: 0, Remaining: 0}
		for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
			ds := d.Format("2006-01-02")
			if p, ok := dayMap[ds]; ok {
				lastPoint = p
			} else {
				lastPoint = BurndownPoint{Date: ds, Remaining: lastPoint.Remaining, Done: lastPoint.Done, Total: lastPoint.Total}
			}
			points = append(points, lastPoint)
		}
	}

	// Compute weekly velocity
	weeklyDone := make(map[string]int)
	var weekOrder []string
	prevDone := 0
	for _, p := range points {
		t, err := time.Parse("2006-01-02", p.Date)
		if err != nil {
			continue
		}
		year, week := t.ISOWeek()
		weekKey := fmt.Sprintf("%d-W%02d", year, week)
		if _, exists := weeklyDone[weekKey]; !exists {
			weekOrder = append(weekOrder, weekKey)
			weeklyDone[weekKey] = 0
		}
		if p.Done > prevDone {
			weeklyDone[weekKey] += p.Done - prevDone
		}
		prevDone = p.Done
	}

	velocity := make([]WeekVelocity, 0, len(weekOrder))
	for _, wk := range weekOrder {
		velocity = append(velocity, WeekVelocity{Week: wk, Completed: weeklyDone[wk]})
	}

	return &BurndownData{Points: points, Velocity: velocity}, nil
}

// --- Agent Sessions ---

func CreateAgentSession(db *sql.DB, s *models.AgentSession) error {
	_, err := db.Exec(
		`INSERT INTO agent_sessions (id, project_id, feature_id, name, task_description, status, progress_pct, current_phase, eta, context_snapshot)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.ProjectID, nullStr(s.FeatureID), s.Name, nullStr(s.TaskDescription),
		s.Status, s.ProgressPct, nullStr(s.CurrentPhase), nullStr(s.ETA), nullStr(s.ContextSnapshot),
	)
	return err
}

func GetAgentSession(db *sql.DB, id string) (*models.AgentSession, error) {
	row := db.QueryRow(`SELECT id, project_id, COALESCE(feature_id,''), name, COALESCE(task_description,''),
		status, progress_pct, COALESCE(current_phase,''), COALESCE(eta,''), COALESCE(context_snapshot,''),
		created_at, updated_at
		FROM agent_sessions WHERE id = ?`, id)
	s := &models.AgentSession{}
	err := row.Scan(&s.ID, &s.ProjectID, &s.FeatureID, &s.Name, &s.TaskDescription,
		&s.Status, &s.ProgressPct, &s.CurrentPhase, &s.ETA, &s.ContextSnapshot,
		&s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func ListAgentSessions(db *sql.DB, projectID, status string) ([]models.AgentSession, error) {
	q := `SELECT id, project_id, COALESCE(feature_id,''), name, COALESCE(task_description,''),
		status, progress_pct, COALESCE(current_phase,''), COALESCE(eta,''), COALESCE(context_snapshot,''),
		created_at, updated_at
		FROM agent_sessions WHERE project_id = ?`
	args := []any{projectID}
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}
	q += " ORDER BY updated_at DESC"

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.AgentSession
	for rows.Next() {
		var s models.AgentSession
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.FeatureID, &s.Name, &s.TaskDescription,
			&s.Status, &s.ProgressPct, &s.CurrentPhase, &s.ETA, &s.ContextSnapshot,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func UpdateAgentSession(db *sql.DB, id string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	var setClauses []string
	var args []any
	for col, val := range updates {
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}
	setClauses = append(setClauses, "updated_at = datetime('now')")
	args = append(args, id)
	_, err := db.Exec(
		fmt.Sprintf("UPDATE agent_sessions SET %s WHERE id = ?", strings.Join(setClauses, ", ")),
		args...,
	)
	return err
}

func EndAgentSession(db *sql.DB, id, status string) error {
	_, err := db.Exec(
		"UPDATE agent_sessions SET status = ?, updated_at = datetime('now') WHERE id = ?",
		status, id,
	)
	return err
}

func InsertStatusUpdate(db *sql.DB, u *models.StatusUpdate) error {
	res, err := db.Exec(
		`INSERT INTO status_updates (agent_session_id, message_md, progress_pct, phase) VALUES (?, ?, ?, ?)`,
		u.AgentSessionID, u.MessageMD, u.ProgressPct, nullStr(u.Phase),
	)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	u.ID = int(id)
	return nil
}

func ListStatusUpdates(db *sql.DB, agentSessionID string) ([]models.StatusUpdate, error) {
	rows, err := db.Query(`SELECT id, agent_session_id, message_md, progress_pct, COALESCE(phase,''), created_at
		FROM status_updates WHERE agent_session_id = ? ORDER BY created_at DESC`, agentSessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.StatusUpdate
	for rows.Next() {
		var u models.StatusUpdate
		if err := rows.Scan(&u.ID, &u.AgentSessionID, &u.MessageMD, &u.ProgressPct, &u.Phase, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// --- Idea Queue ---

func InsertIdea(db *sql.DB, idea *models.IdeaQueueItem) error {
	res, err := db.Exec(
		`INSERT INTO idea_queue (project_id, title, raw_input, idea_type, status, spec_md, auto_implement, submitted_by, assigned_agent, feature_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		idea.ProjectID, idea.Title, idea.RawInput, idea.IdeaType, idea.Status,
		nullStr(idea.SpecMD), boolToInt(idea.AutoImplement), idea.SubmittedBy,
		nullStr(idea.AssignedAgent), nullStr(idea.FeatureID),
	)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	idea.ID = int(id)
	return nil
}

func GetIdea(db *sql.DB, id int) (*models.IdeaQueueItem, error) {
	row := db.QueryRow(`SELECT id, project_id, title, raw_input, idea_type, status,
		COALESCE(spec_md,''), auto_implement, COALESCE(submitted_by,'human'), COALESCE(assigned_agent,''),
		COALESCE(feature_id,''), created_at, updated_at
		FROM idea_queue WHERE id = ?`, id)
	item := &models.IdeaQueueItem{}
	var autoImpl int
	err := row.Scan(&item.ID, &item.ProjectID, &item.Title, &item.RawInput, &item.IdeaType, &item.Status,
		&item.SpecMD, &autoImpl, &item.SubmittedBy, &item.AssignedAgent,
		&item.FeatureID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	item.AutoImplement = autoImpl != 0
	return item, nil
}

func ListIdeas(db *sql.DB, projectID, status, ideaType string) ([]models.IdeaQueueItem, error) {
	q := `SELECT id, project_id, title, raw_input, idea_type, status,
		COALESCE(spec_md,''), auto_implement, COALESCE(submitted_by,'human'), COALESCE(assigned_agent,''),
		COALESCE(feature_id,''), created_at, updated_at
		FROM idea_queue WHERE project_id = ?`
	args := []any{projectID}
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}
	if ideaType != "" {
		q += " AND idea_type = ?"
		args = append(args, ideaType)
	}
	q += " ORDER BY created_at DESC"

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.IdeaQueueItem
	for rows.Next() {
		var item models.IdeaQueueItem
		var autoImpl int
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Title, &item.RawInput, &item.IdeaType, &item.Status,
			&item.SpecMD, &autoImpl, &item.SubmittedBy, &item.AssignedAgent,
			&item.FeatureID, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.AutoImplement = autoImpl != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

func UpdateIdeaStatus(db *sql.DB, id int, status string) error {
	_, err := db.Exec("UPDATE idea_queue SET status = ?, updated_at = datetime('now') WHERE id = ?", status, id)
	return err
}

func SetIdeaSpec(db *sql.DB, id int, specMD string) error {
	_, err := db.Exec("UPDATE idea_queue SET spec_md = ?, status = 'spec-ready', updated_at = datetime('now') WHERE id = ?", specMD, id)
	return err
}

func ApproveIdea(db *sql.DB, id int, featureID string) error {
	_, err := db.Exec("UPDATE idea_queue SET status = 'approved', feature_id = ?, updated_at = datetime('now') WHERE id = ?", featureID, id)
	return err
}

func GetNextIdeaForSpec(db *sql.DB, projectID string) (*models.IdeaQueueItem, error) {
	row := db.QueryRow(`SELECT id, project_id, title, raw_input, idea_type, status,
		COALESCE(spec_md,''), auto_implement, COALESCE(submitted_by,'human'), COALESCE(assigned_agent,''),
		COALESCE(feature_id,''), created_at, updated_at
		FROM idea_queue WHERE project_id = ? AND status = 'pending'
		ORDER BY created_at ASC LIMIT 1`, projectID)
	item := &models.IdeaQueueItem{}
	var autoImpl int
	err := row.Scan(&item.ID, &item.ProjectID, &item.Title, &item.RawInput, &item.IdeaType, &item.Status,
		&item.SpecMD, &autoImpl, &item.SubmittedBy, &item.AssignedAgent,
		&item.FeatureID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	item.AutoImplement = autoImpl != 0
	return item, nil
}

// --- Context Entries ---

func InsertContext(db *sql.DB, e *models.ContextEntry) error {
	res, err := db.Exec(
		`INSERT INTO context_entries (project_id, feature_id, context_type, title, content_md, author, tags)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ProjectID, nullStr(e.FeatureID), e.ContextType, e.Title, e.ContentMD, e.Author, nullStr(e.Tags),
	)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	e.ID = int(id)
	return nil
}

func GetContextEntry(database *sql.DB, id int) (*models.ContextEntry, error) {
	row := database.QueryRow(`SELECT id, project_id, COALESCE(feature_id,''), context_type, title, content_md,
		COALESCE(author,'system'), COALESCE(tags,''), created_at
		FROM context_entries WHERE id = ?`, id)
	e := &models.ContextEntry{}
	err := row.Scan(&e.ID, &e.ProjectID, &e.FeatureID, &e.ContextType, &e.Title, &e.ContentMD,
		&e.Author, &e.Tags, &e.CreatedAt)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func ListContext(db *sql.DB, projectID, featureID string) ([]models.ContextEntry, error) {
	q := `SELECT id, project_id, COALESCE(feature_id,''), context_type, title, content_md,
		COALESCE(author,'system'), COALESCE(tags,''), created_at
		FROM context_entries WHERE project_id = ?`
	args := []any{projectID}
	if featureID != "" {
		q += " AND feature_id = ?"
		args = append(args, featureID)
	}
	q += " ORDER BY created_at DESC"

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.ContextEntry
	for rows.Next() {
		var e models.ContextEntry
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.FeatureID, &e.ContextType, &e.Title, &e.ContentMD,
			&e.Author, &e.Tags, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func SearchContext(db *sql.DB, projectID, query string) ([]models.ContextEntry, error) {
	pattern := "%" + query + "%"
	rows, err := db.Query(`SELECT id, project_id, COALESCE(feature_id,''), context_type, title, content_md,
		COALESCE(author,'system'), COALESCE(tags,''), created_at
		FROM context_entries WHERE project_id = ? AND (title LIKE ? OR content_md LIKE ?)
		ORDER BY created_at DESC`, projectID, pattern, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.ContextEntry
	for rows.Next() {
		var e models.ContextEntry
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.FeatureID, &e.ContextType, &e.Title, &e.ContentMD,
			&e.Author, &e.Tags, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// extractJSONField extracts a simple string field value from a JSON string.
func extractJSONField(data, field string) string {
	key := `"` + field + `":"`
	idx := strings.Index(data, key)
	if idx < 0 {
		return ""
	}
	start := idx + len(key)
	end := strings.Index(data[start:], `"`)
	if end < 0 {
		return ""
	}
	return data[start : start+end]
}

// --- Worktrees ---

func CreateWorktree(db *sql.DB, w *models.Worktree) error {
	_, err := db.Exec(
		`INSERT INTO worktrees (id, name, path, branch, agent_session_id) VALUES (?, ?, ?, ?, ?)`,
		w.ID, w.Name, w.Path, w.Branch, nullStr(w.AgentSessionID),
	)
	return err
}

func GetWorktree(db *sql.DB, id string) (*models.Worktree, error) {
	row := db.QueryRow(`SELECT id, name, path, COALESCE(branch,''), COALESCE(agent_session_id,''), created_at
		FROM worktrees WHERE id = ?`, id)
	w := &models.Worktree{}
	err := row.Scan(&w.ID, &w.Name, &w.Path, &w.Branch, &w.AgentSessionID, &w.CreatedAt)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func GetWorktreeByName(db *sql.DB, name string) (*models.Worktree, error) {
	row := db.QueryRow(`SELECT id, name, path, COALESCE(branch,''), COALESCE(agent_session_id,''), created_at
		FROM worktrees WHERE name = ?`, name)
	w := &models.Worktree{}
	err := row.Scan(&w.ID, &w.Name, &w.Path, &w.Branch, &w.AgentSessionID, &w.CreatedAt)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func ListWorktrees(db *sql.DB) ([]models.Worktree, error) {
	rows, err := db.Query(`SELECT id, name, path, COALESCE(branch,''), COALESCE(agent_session_id,''), created_at
		FROM worktrees ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Worktree
	for rows.Next() {
		var w models.Worktree
		if err := rows.Scan(&w.ID, &w.Name, &w.Path, &w.Branch, &w.AgentSessionID, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func DeleteWorktree(db *sql.DB, id string) error {
	// Clear any agent session references first
	_, _ = db.Exec("UPDATE agent_sessions SET worktree_id = '' WHERE worktree_id = ?", id)
	_, err := db.Exec("DELETE FROM worktrees WHERE id = ?", id)
	return err
}

func LinkWorktreeToAgent(db *sql.DB, worktreeID, agentSessionID string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	_, err = tx.Exec("UPDATE worktrees SET agent_session_id = ? WHERE id = ?", agentSessionID, worktreeID)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE agent_sessions SET worktree_id = ? WHERE id = ?", worktreeID, agentSessionID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func GetWorktreeByAgent(db *sql.DB, agentSessionID string) (*models.Worktree, error) {
	row := db.QueryRow(`SELECT id, name, path, COALESCE(branch,''), COALESCE(agent_session_id,''), created_at
		FROM worktrees WHERE agent_session_id = ?`, agentSessionID)
	w := &models.Worktree{}
	err := row.Scan(&w.ID, &w.Name, &w.Path, &w.Branch, &w.AgentSessionID, &w.CreatedAt)
	if err != nil {
		return nil, err
	}
	return w, nil
}

// --- Multi-Agent Coordination ---

// UpdateAgentHeartbeat updates the updated_at timestamp and optionally inserts a status update.
func UpdateAgentHeartbeat(database *sql.DB, agentID string, message string) error {
	_, err := database.Exec(
		`UPDATE agent_sessions SET updated_at = datetime('now') WHERE id = ?`, agentID)
	if err != nil {
		return fmt.Errorf("updating agent heartbeat: %w", err)
	}
	if message != "" {
		u := &models.StatusUpdate{
			AgentSessionID: agentID,
			MessageMD:      message,
			Phase:          "heartbeat",
		}
		return InsertStatusUpdate(database, u)
	}
	return nil
}

// GetActiveAgents returns agents with recent heartbeats (updated within the last N minutes).
func GetActiveAgents(database *sql.DB, projectID string, recentMins int) ([]models.AgentSession, error) {
	rows, err := database.Query(`SELECT id, project_id, COALESCE(feature_id,''), name,
		COALESCE(task_description,''), status, progress_pct, COALESCE(current_phase,''),
		COALESCE(eta,''), COALESCE(context_snapshot,''), created_at, updated_at
		FROM agent_sessions
		WHERE project_id = ? AND status = 'active'
		AND updated_at >= datetime('now', ? || ' minutes')
		ORDER BY updated_at DESC`, projectID, fmt.Sprintf("-%d", recentMins))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []models.AgentSession
	for rows.Next() {
		var s models.AgentSession
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.FeatureID, &s.Name,
			&s.TaskDescription, &s.Status, &s.ProgressPct, &s.CurrentPhase,
			&s.ETA, &s.ContextSnapshot, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetStaleAgents returns active agents whose last heartbeat is older than the threshold.
func GetStaleAgents(database *sql.DB, projectID string, staleMins int) ([]models.AgentSession, error) {
	rows, err := database.Query(`SELECT id, project_id, COALESCE(feature_id,''), name,
		COALESCE(task_description,''), status, progress_pct, COALESCE(current_phase,''),
		COALESCE(eta,''), COALESCE(context_snapshot,''), created_at, updated_at
		FROM agent_sessions
		WHERE project_id = ? AND status = 'active'
		AND updated_at < datetime('now', ? || ' minutes')
		ORDER BY updated_at ASC`, projectID, fmt.Sprintf("-%d", staleMins))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []models.AgentSession
	for rows.Next() {
		var s models.AgentSession
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.FeatureID, &s.Name,
			&s.TaskDescription, &s.Status, &s.ProgressPct, &s.CurrentPhase,
			&s.ETA, &s.ContextSnapshot, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DetectConflicts finds features with multiple agents working on active items simultaneously.
func DetectConflicts(database *sql.DB) ([]models.Conflict, error) {
	rows, err := database.Query(`SELECT wi.feature_id, COALESCE(f.name,''), GROUP_CONCAT(DISTINCT wi.assigned_agent)
		FROM work_items wi
		LEFT JOIN features f ON wi.feature_id = f.id
		WHERE wi.status = 'active' AND wi.assigned_agent != ''
		GROUP BY wi.feature_id
		HAVING COUNT(DISTINCT wi.assigned_agent) > 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []models.Conflict
	for rows.Next() {
		var featureID, featureName, agents string
		if err := rows.Scan(&featureID, &featureName, &agents); err != nil {
			return nil, err
		}
		out = append(out, models.Conflict{
			FeatureID:   featureID,
			FeatureName: featureName,
			Agents:      strings.Split(agents, ","),
		})
	}
	return out, rows.Err()
}

// CountPendingWorkItems returns the number of unclaimed pending work items.
func CountPendingWorkItems(database *sql.DB) (int, error) {
	var count int
	err := database.QueryRow(`SELECT COUNT(*) FROM work_items WHERE status = 'pending'`).Scan(&count)
	return count, err
}

// CountClaimedWorkItems returns the number of active (claimed) work items.
func CountClaimedWorkItems(database *sql.DB) (int, error) {
	var count int
	err := database.QueryRow(`SELECT COUNT(*) FROM work_items WHERE status = 'active'`).Scan(&count)
	return count, err
}

// GetWorkItemByID returns a work item by its ID.
func GetWorkItemByID(database *sql.DB, id int) (*models.WorkItem, error) {
	row := database.QueryRow(`SELECT id, feature_id, work_type, status, agent_prompt, COALESCE(result,''),
		COALESCE(assigned_agent,''), COALESCE(started_at,''), COALESCE(completed_at,''), created_at
		FROM work_items WHERE id = ?`, id)
	w := &models.WorkItem{}
	err := row.Scan(&w.ID, &w.FeatureID, &w.WorkType, &w.Status, &w.AgentPrompt, &w.Result,
		&w.AssignedAgent, &w.StartedAt, &w.CompletedAt, &w.CreatedAt)
	if err != nil {
		return nil, err
	}
	return w, nil
}

// --- Queue Management ---

// GetQueuedWorkItems returns all pending and active work items in priority order
// with enriched feature context.
func GetQueuedWorkItems(database *sql.DB) ([]models.QueueEntry, error) {
	rows, err := database.Query(`SELECT w.id, w.feature_id, COALESCE(f.name,''), w.work_type,
		COALESCE(f.priority, 0), COALESCE(ci.cycle_type,''), COALESCE(w.assigned_agent,''),
		w.status, w.created_at
		FROM work_items w
		LEFT JOIN features f ON w.feature_id = f.id
		LEFT JOIN cycle_instances ci ON ci.feature_id = w.feature_id AND ci.status = 'active'
		WHERE w.status IN ('pending','active')
		ORDER BY
			CASE w.status WHEN 'active' THEN 0 ELSE 1 END,
			COALESCE(f.priority, 0) DESC,
			COALESCE(ci.current_step, 0) ASC,
			w.created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []models.QueueEntry
	for rows.Next() {
		var e models.QueueEntry
		if err := rows.Scan(&e.WorkItemID, &e.FeatureID, &e.FeatureName, &e.WorkType,
			&e.Priority, &e.CycleType, &e.AssignedAgent, &e.Status, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetQueueStats returns aggregate statistics about the work queue.
func GetQueueStats(database *sql.DB) (*models.QueueStats, error) {
	s := &models.QueueStats{}
	if err := database.QueryRow(`SELECT COUNT(*) FROM work_items WHERE status = 'pending'`).Scan(&s.TotalPending); err != nil {
		return nil, err
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM work_items WHERE status = 'active'`).Scan(&s.TotalClaimed); err != nil {
		return nil, err
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM work_items WHERE status = 'done' AND completed_at >= date('now')`).Scan(&s.TotalCompletedDay); err != nil {
		return nil, err
	}
	return s, nil
}

// ReleaseWorkItem resets a claimed (active) work item back to pending status.
func ReleaseWorkItem(database *sql.DB, workItemID int) error {
	res, err := database.Exec(
		`UPDATE work_items SET status = 'pending', assigned_agent = '', started_at = ''
		 WHERE id = ? AND status = 'active'`, workItemID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("work item %d is not active (cannot release)", workItemID)
	}
	return nil
}

// ReclaimStaleWorkItems resets work items that have been claimed by agents with
// no heartbeat for the given duration. Returns the number of reclaimed items.
func ReclaimStaleWorkItems(database *sql.DB, staleMins int) (int, error) {
	res, err := database.Exec(`UPDATE work_items SET status = 'pending', assigned_agent = '', started_at = ''
		WHERE status = 'active' AND assigned_agent != ''
		AND NOT EXISTS (
			SELECT 1 FROM heartbeats h
			WHERE h.agent_id = work_items.assigned_agent
			AND h.created_at >= datetime('now', ? || ' minutes')
		)
		AND started_at < datetime('now', ? || ' minutes')`,
		fmt.Sprintf("-%d", staleMins), fmt.Sprintf("-%d", staleMins))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// --- Decisions (ADRs) ---

func CreateDecision(database *sql.DB, d *models.Decision) error {
	if d.Status == "" {
		d.Status = "proposed"
	}
	_, err := database.Exec(
		`INSERT INTO decisions (id, title, status, context, decision, consequences, feature_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Title, d.Status, nullStr(d.Context), nullStr(d.Decision), nullStr(d.Consequences), nullStr(d.FeatureID),
	)
	return err
}

func GetDecision(database *sql.DB, id string) (*models.Decision, error) {
	row := database.QueryRow(`SELECT id, title, status, COALESCE(context,''), COALESCE(decision,''), COALESCE(consequences,''), COALESCE(feature_id,''), created_at, updated_at
		FROM decisions WHERE id = ?`, id)
	d := &models.Decision{}
	err := row.Scan(&d.ID, &d.Title, &d.Status, &d.Context, &d.Decision, &d.Consequences, &d.FeatureID, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func ListDecisions(database *sql.DB, status string) ([]models.Decision, error) {
	q := `SELECT id, title, status, COALESCE(context,''), COALESCE(decision,''), COALESCE(consequences,''), COALESCE(feature_id,''), created_at, updated_at
		FROM decisions`
	var args []any
	if status != "" {
		q += " WHERE status = ?"
		args = append(args, status)
	}
	q += " ORDER BY created_at DESC"

	rows, err := database.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("querying decisions: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Decision
	for rows.Next() {
		var d models.Decision
		if err := rows.Scan(&d.ID, &d.Title, &d.Status, &d.Context, &d.Decision, &d.Consequences, &d.FeatureID, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func UpdateDecision(database *sql.DB, id string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	var setClauses []string
	var args []any
	for col, val := range updates {
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}
	setClauses = append(setClauses, "updated_at = datetime('now')")
	args = append(args, id)
	_, err := database.Exec(
		fmt.Sprintf("UPDATE decisions SET %s WHERE id = ?", strings.Join(setClauses, ", ")),
		args...,
	)
	return err
}

// --- FTS5 Search ---

// SearchFTS performs a full-text search across features, roadmap items, and ideas
// using the FTS5 index. Results are ranked by relevance and include highlighted snippets.
func SearchFTS(database *sql.DB, query string, limit int) ([]models.SearchResult, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := database.Query(`
		SELECT entity_type, entity_id, title,
			snippet(search_fts, 3, '<mark>', '</mark>', '...', 32) as snippet,
			rank
		FROM search_fts
		WHERE search_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("FTS search: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var out []models.SearchResult
	for rows.Next() {
		var r models.SearchResult
		if err := rows.Scan(&r.EntityType, &r.EntityID, &r.Title, &r.Snippet, &r.Rank); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// IndexEntity adds or replaces an entity in the FTS5 search index.
func IndexEntity(database *sql.DB, entityType, entityID, title, content string) error {
	// Remove existing entry first, then insert fresh
	_, _ = database.Exec(
		`DELETE FROM search_fts WHERE entity_type = ? AND entity_id = ?`,
		entityType, entityID,
	)
	_, err := database.Exec(
		`INSERT INTO search_fts (entity_type, entity_id, title, content) VALUES (?, ?, ?, ?)`,
		entityType, entityID, title, content,
	)
	return err
}

// RemoveFromIndex removes an entity from the FTS5 search index.
func RemoveFromIndex(database *sql.DB, entityType, entityID string) error {
	_, err := database.Exec(
		`DELETE FROM search_fts WHERE entity_type = ? AND entity_id = ?`,
		entityType, entityID,
	)
	return err
}
