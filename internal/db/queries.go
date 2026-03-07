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
			f.created_at, f.updated_at, COALESCE(m.name,'') AS ms_name
		FROM features f
		LEFT JOIN milestones m ON f.milestone_id = m.id
		WHERE f.id = ?`, id)
	f := &models.Feature{}
	err := row.Scan(&f.ID, &f.ProjectID, &f.MilestoneID, &f.Name, &f.Description, &f.Spec,
		&f.Status, &f.Priority, &f.AssignedCycle, &f.RoadmapItemID, &f.CreatedAt, &f.UpdatedAt, &f.MilestoneName)
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
			f.created_at, f.updated_at, COALESCE(m.name,'')
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
			&f.Status, &f.Priority, &f.AssignedCycle, &f.RoadmapItemID, &f.CreatedAt, &f.UpdatedAt, &f.MilestoneName); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
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
		COALESCE(started_at,''), COALESCE(completed_at,''), created_at
		FROM work_items WHERE status = 'active' LIMIT 1`)
	w := &models.WorkItem{}
	err := row.Scan(&w.ID, &w.FeatureID, &w.WorkType, &w.Status, &w.AgentPrompt, &w.Result,
		&w.StartedAt, &w.CompletedAt, &w.CreatedAt)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func GetNextPendingWorkItem(db *sql.DB) (*models.WorkItem, error) {
	row := db.QueryRow(`SELECT id, feature_id, work_type, status, agent_prompt, COALESCE(result,''),
		COALESCE(started_at,''), COALESCE(completed_at,''), created_at
		FROM work_items WHERE status = 'pending' ORDER BY created_at LIMIT 1`)
	w := &models.WorkItem{}
	err := row.Scan(&w.ID, &w.FeatureID, &w.WorkType, &w.Status, &w.AgentPrompt, &w.Result,
		&w.StartedAt, &w.CompletedAt, &w.CreatedAt)
	if err != nil {
		return nil, err
	}
	return w, nil
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
		COALESCE(started_at,''), COALESCE(completed_at,''), created_at
		FROM work_items WHERE feature_id = ? AND status IN ('done','failed') ORDER BY completed_at DESC`, featureID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []models.WorkItem
	for rows.Next() {
		var w models.WorkItem
		if err := rows.Scan(&w.ID, &w.FeatureID, &w.WorkType, &w.Status, &w.AgentPrompt, &w.Result,
			&w.StartedAt, &w.CompletedAt, &w.CreatedAt); err != nil {
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

// --- Helpers ---

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
