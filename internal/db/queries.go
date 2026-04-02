package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind/tillr/internal/models"
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

func ListProjects(db *sql.DB) ([]models.Project, error) {
	rows, err := db.Query(`SELECT id, name, description, created_at, updated_at FROM projects ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var projects []models.Project
	for rows.Next() {
		var p models.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func GetProjectByID(db *sql.DB, id string) (*models.Project, error) {
	row := db.QueryRow(`SELECT id, name, description, created_at, updated_at FROM projects WHERE id = ?`, id)
	p := &models.Project{}
	if err := row.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return p, nil
}

func UpdateProject(db *sql.DB, id, description string) error {
	_, err := db.Exec(`UPDATE projects SET description = ?, updated_at = datetime('now') WHERE id = ?`, description, id)
	return err
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
		`INSERT INTO features (id, project_id, milestone_id, name, description, spec, priority, roadmap_item_id, estimate_points, estimate_size) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.ProjectID, nullStr(f.MilestoneID), f.Name, f.Description, f.Spec, f.Priority, f.RoadmapItemID, f.EstimatePoints, f.EstimateSize,
	)
	return err
}

func GetFeature(db *sql.DB, id string) (*models.Feature, error) {
	row := db.QueryRow(`
		SELECT f.id, f.project_id, COALESCE(f.milestone_id,''), f.name, COALESCE(f.description,''), COALESCE(f.spec,''),
			f.status, f.priority, COALESCE(f.assigned_cycle,''), COALESCE(f.roadmap_item_id,''),
			f.created_at, f.updated_at, COALESCE(m.name,'') AS ms_name, COALESCE(f.previous_status,''),
			COALESCE(f.estimate_points,0), COALESCE(f.estimate_size,'')
		FROM features f
		LEFT JOIN milestones m ON f.milestone_id = m.id
		WHERE f.id = ?`, id)
	f := &models.Feature{}
	err := row.Scan(&f.ID, &f.ProjectID, &f.MilestoneID, &f.Name, &f.Description, &f.Spec,
		&f.Status, &f.Priority, &f.AssignedCycle, &f.RoadmapItemID, &f.CreatedAt, &f.UpdatedAt, &f.MilestoneName, &f.PreviousStatus,
		&f.EstimatePoints, &f.EstimateSize)
	if err != nil {
		return nil, err
	}
	deps, _ := featureDeps(db, id)
	f.DependsOn = deps
	tags, _ := GetFeatureTags(db, id)
	f.Tags = tags
	return f, nil
}

func ListFeatures(db *sql.DB, projectID, status, milestoneID string) ([]models.Feature, error) {
	q := `SELECT f.id, f.project_id, COALESCE(f.milestone_id,''), f.name, COALESCE(f.description,''), COALESCE(f.spec,''),
			f.status, f.priority, COALESCE(f.assigned_cycle,''), COALESCE(f.roadmap_item_id,''),
			f.created_at, f.updated_at, COALESCE(m.name,''), COALESCE(f.previous_status,''),
			COALESCE(f.estimate_points,0), COALESCE(f.estimate_size,'')
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
			&f.Status, &f.Priority, &f.AssignedCycle, &f.RoadmapItemID, &f.CreatedAt, &f.UpdatedAt, &f.MilestoneName, &f.PreviousStatus,
			&f.EstimatePoints, &f.EstimateSize); err != nil {
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

	// Bulk-load tags for all features
	if len(out) > 0 {
		tagRows, err := db.Query("SELECT feature_id, tag FROM feature_tags ORDER BY tag")
		if err == nil {
			defer tagRows.Close() //nolint:errcheck
			tagMap := make(map[string][]string)
			for tagRows.Next() {
				var fid, t string
				if err := tagRows.Scan(&fid, &t); err == nil {
					tagMap[fid] = append(tagMap[fid], t)
				}
			}
			for i := range out {
				if tags, ok := tagMap[out[i].ID]; ok {
					out[i].Tags = tags
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
		"status":          "status",
		"milestone_id":    "milestone_id",
		"priority":        "priority",
		"estimate_points": "estimate_points",
		"estimate_size":   "estimate_size",
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

// GetEstimationSummary returns aggregate estimation data for a project, optionally filtered by milestone.
func GetEstimationSummary(database *sql.DB, projectID, milestoneID string) (*models.EstimationSummary, error) {
	q := `SELECT COALESCE(SUM(estimate_points),0), COALESCE(SUM(CASE WHEN status='done' THEN estimate_points ELSE 0 END),0)
		FROM features WHERE project_id = ?`
	args := []any{projectID}
	if milestoneID != "" {
		q += " AND milestone_id = ?"
		args = append(args, milestoneID)
	}

	var total, completed int
	if err := database.QueryRow(q, args...).Scan(&total, &completed); err != nil {
		return nil, err
	}

	sizeQ := `SELECT COALESCE(estimate_size,'') AS sz, COUNT(*) AS cnt,
			SUM(CASE WHEN status='done' THEN 1 ELSE 0 END) AS done_cnt
		FROM features WHERE project_id = ? AND estimate_size != ''`
	sizeArgs := []any{projectID}
	if milestoneID != "" {
		sizeQ += " AND milestone_id = ?"
		sizeArgs = append(sizeArgs, milestoneID)
	}
	sizeQ += " GROUP BY estimate_size ORDER BY CASE estimate_size WHEN 'XS' THEN 1 WHEN 'S' THEN 2 WHEN 'M' THEN 3 WHEN 'L' THEN 4 WHEN 'XL' THEN 5 ELSE 6 END"

	sizeRows, err := database.Query(sizeQ, sizeArgs...)
	if err != nil {
		return nil, err
	}
	defer sizeRows.Close() //nolint:errcheck

	var entries []models.EstimationSizeEntry
	for sizeRows.Next() {
		var e models.EstimationSizeEntry
		if err := sizeRows.Scan(&e.Size, &e.Total, &e.Done); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := sizeRows.Err(); err != nil {
		return nil, err
	}

	unQ := `SELECT COUNT(*) FROM features WHERE project_id = ? AND estimate_points = 0 AND (estimate_size = '' OR estimate_size IS NULL)`
	unArgs := []any{projectID}
	if milestoneID != "" {
		unQ += " AND milestone_id = ?"
		unArgs = append(unArgs, milestoneID)
	}
	var unestimated int
	if err := database.QueryRow(unQ, unArgs...).Scan(&unestimated); err != nil {
		return nil, err
	}

	return &models.EstimationSummary{
		TotalPoints:     total,
		CompletedPoints: completed,
		RemainingPoints: total - completed,
		BySizeEntries:   entries,
		Unestimated:     unestimated,
	}, nil
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
		LEFT JOIN cycle_instances ci ON ci.entity_id = w.feature_id AND ci.status = 'active'
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

// WebhookDispatchFunc is called after every event insertion with the DB
// handle and the event. Set by the server package to dispatch webhooks.
var WebhookDispatchFunc func(*sql.DB, *models.Event)

func InsertEvent(db *sql.DB, e *models.Event) error {
	res, err := db.Exec(
		`INSERT INTO events (project_id, feature_id, event_type, data) VALUES (?, ?, ?, ?)`,
		e.ProjectID, nullStr(e.FeatureID), e.EventType, e.Data,
	)
	if err != nil {
		return err
	}
	if id, idErr := res.LastInsertId(); idErr == nil {
		e.ID = int(id)
	}
	if e.CreatedAt == "" {
		e.CreatedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	// Dispatch webhooks asynchronously with a small delay so the caller
	// can finish its remaining DB operations before the webhook goroutine
	// tries to query the webhooks table. This prevents SQLITE_BUSY errors
	// when InsertEvent is called mid-sequence (e.g. during ScoreCycleStep).
	if WebhookDispatchFunc != nil {
		dispatch := WebhookDispatchFunc
		evt := *e // copy so the goroutine doesn't race on the caller's pointer
		go func() {
			time.Sleep(100 * time.Millisecond)
			dispatch(db, &evt)
		}()
	}
	return nil
}

func ListEvents(db *sql.DB, projectID, featureID, eventType, since string, limit int) ([]models.Event, error) {
	return ListEventsFiltered(db, projectID, featureID, eventType, since, "", limit)
}

// ListEventsFiltered queries events with optional until filter in addition to ListEvents filters.
func ListEventsFiltered(db *sql.DB, projectID, featureID, eventType, since, until string, limit int) ([]models.Event, error) {
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
	if until != "" {
		q += " AND created_at <= ?"
		args = append(args, until)
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
	if r.Status != "" {
		_, err := db.Exec(
			`INSERT INTO roadmap_items (id, project_id, title, description, category, priority, sort_order, effort, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.ProjectID, r.Title, r.Description, r.Category, r.Priority, r.SortOrder, r.Effort, r.Status,
		)
		return err
	}
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
	if c.EntityType == "" {
		c.EntityType = "feature"
	}
	res, err := db.Exec(
		`INSERT INTO cycle_instances (entity_type, entity_id, cycle_type, current_step, iteration, status) VALUES (?, ?, ?, ?, ?, ?)`,
		c.EntityType, c.EntityID, c.CycleType, c.CurrentStep, c.Iteration, c.Status,
	)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	c.ID = int(id)
	return nil
}

// GetActiveCycle finds an active cycle for the given entity (defaults to entity_type="feature").
func GetActiveCycle(db *sql.DB, entityID string) (*models.CycleInstance, error) {
	return GetActiveCycleForEntity(db, "feature", entityID)
}

// GetActiveCycleForEntity finds an active cycle for any entity type.
func GetActiveCycleForEntity(db *sql.DB, entityType, entityID string) (*models.CycleInstance, error) {
	row := db.QueryRow(`SELECT id, entity_type, entity_id, cycle_type, current_step, iteration, status, created_at, updated_at
		FROM cycle_instances WHERE entity_type = ? AND entity_id = ? AND status = 'active' LIMIT 1`, entityType, entityID)
	c := &models.CycleInstance{}
	err := row.Scan(&c.ID, &c.EntityType, &c.EntityID, &c.CycleType, &c.CurrentStep, &c.Iteration, &c.Status, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func GetCycleByID(db *sql.DB, id int) (*models.CycleInstance, error) {
	row := db.QueryRow(`SELECT id, entity_type, entity_id, cycle_type, current_step, iteration, status, created_at, updated_at
		FROM cycle_instances WHERE id = ?`, id)
	c := &models.CycleInstance{}
	err := row.Scan(&c.ID, &c.EntityType, &c.EntityID, &c.CycleType, &c.CurrentStep, &c.Iteration, &c.Status, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func ListActiveCycles(db *sql.DB) ([]models.CycleInstance, error) {
	rows, err := db.Query(`SELECT id, entity_type, entity_id, cycle_type, current_step, iteration, status, created_at, updated_at
		FROM cycle_instances WHERE status = 'active' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.CycleInstance
	for rows.Next() {
		var c models.CycleInstance
		if err := rows.Scan(&c.ID, &c.EntityType, &c.EntityID, &c.CycleType, &c.CurrentStep, &c.Iteration, &c.Status, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func ListAllCycles(db *sql.DB) ([]models.CycleInstance, error) {
	rows, err := db.Query(`SELECT id, entity_type, entity_id, cycle_type, current_step, iteration, status, created_at, updated_at
		FROM cycle_instances ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.CycleInstance
	for rows.Next() {
		var c models.CycleInstance
		if err := rows.Scan(&c.ID, &c.EntityType, &c.EntityID, &c.CycleType, &c.CurrentStep, &c.Iteration, &c.Status, &c.CreatedAt, &c.UpdatedAt); err != nil {
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

func ListCycleHistory(db *sql.DB, entityID string) ([]models.CycleInstance, error) {
	rows, err := db.Query(`SELECT id, entity_type, entity_id, cycle_type, current_step, iteration, status, created_at, updated_at
		FROM cycle_instances WHERE entity_id = ? ORDER BY created_at`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.CycleInstance
	for rows.Next() {
		var c models.CycleInstance
		if err := rows.Scan(&c.ID, &c.EntityType, &c.EntityID, &c.CycleType, &c.CurrentStep, &c.Iteration, &c.Status, &c.CreatedAt, &c.UpdatedAt); err != nil {
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
	d.Votes = getDiscussionVoteCounts(db, d.ID)
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
		d.Votes = getDiscussionVoteCounts(db, d.ID)
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

// --- Discussion Votes ---

// ValidReactions is the set of allowed discussion reactions.
var ValidReactions = map[string]bool{
	"👍": true, "👎": true, "🎉": true, "❤️": true, "🤔": true,
}

func AddDiscussionVote(db *sql.DB, v *models.DiscussionVote) error {
	_, err := db.Exec(
		`INSERT OR IGNORE INTO discussion_votes (discussion_id, voter, reaction) VALUES (?, ?, ?)`,
		v.DiscussionID, v.Voter, v.Reaction,
	)
	return err
}

func RemoveDiscussionVote(db *sql.DB, discussionID int, voter, reaction string) error {
	_, err := db.Exec(
		`DELETE FROM discussion_votes WHERE discussion_id = ? AND voter = ? AND reaction = ?`,
		discussionID, voter, reaction,
	)
	return err
}

func GetDiscussionVotes(db *sql.DB, discussionID int) (*models.VoteSummary, error) {
	rows, err := db.Query(
		`SELECT reaction, COUNT(*) FROM discussion_votes WHERE discussion_id = ? GROUP BY reaction ORDER BY COUNT(*) DESC`,
		discussionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	summary := &models.VoteSummary{
		DiscussionID: discussionID,
		Counts:       make(map[string]int),
	}
	for rows.Next() {
		var reaction string
		var count int
		if err := rows.Scan(&reaction, &count); err != nil {
			return nil, err
		}
		summary.Counts[reaction] = count
		summary.Total += count
	}
	return summary, rows.Err()
}

func getDiscussionVoteCounts(db *sql.DB, discussionID int) map[string]int {
	summary, err := GetDiscussionVotes(db, discussionID)
	if err != nil || summary.Total == 0 {
		return nil
	}
	return summary.Counts
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
	err = database.QueryRow(`SELECT COUNT(*), COALESCE(SUM(iteration), 0) FROM cycle_instances WHERE entity_id IN (SELECT id FROM features WHERE project_id = ?)`, projectID).Scan(&totalCycles, &totalIterations)
	if err != nil {
		return nil, fmt.Errorf("getting cycle counts: %w", err)
	}
	err = database.QueryRow(`SELECT AVG(cs.score) FROM cycle_scores cs JOIN cycle_instances ci ON cs.cycle_id = ci.id WHERE ci.entity_id IN (SELECT id FROM features WHERE project_id = ?)`, projectID).Scan(&avgScore)
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
		WHERE ci.entity_type = 'feature' AND ci.entity_id IN (SELECT id FROM features WHERE project_id = ?)
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

// CountOpenDiscussions returns the number of open discussions.
func CountOpenDiscussions(db *sql.DB, projectID string) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM discussions WHERE project_id = ? AND status = 'open'`, projectID).Scan(&count)
	return count, err
}

// --- Discussion Templates ---

// CreateDiscussionTemplate inserts a new discussion template.
func CreateDiscussionTemplate(db *sql.DB, t *models.DiscussionTemplate) error {
	_, err := db.Exec(`INSERT INTO discussion_templates (name, description, body, is_builtin) VALUES (?, ?, ?, ?)`,
		t.Name, t.Description, t.Body, boolToInt(t.IsBuiltin))
	return err
}

// GetDiscussionTemplate retrieves a discussion template by name.
func GetDiscussionTemplate(db *sql.DB, name string) (*models.DiscussionTemplate, error) {
	t := &models.DiscussionTemplate{}
	var isBuiltin int
	err := db.QueryRow(`SELECT name, description, body, is_builtin, created_at FROM discussion_templates WHERE name = ?`, name).
		Scan(&t.Name, &t.Description, &t.Body, &isBuiltin, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	t.IsBuiltin = isBuiltin != 0
	return t, nil
}

// ListDiscussionTemplates returns all discussion templates.
func ListDiscussionTemplates(db *sql.DB) ([]models.DiscussionTemplate, error) {
	rows, err := db.Query(`SELECT name, description, body, is_builtin, created_at FROM discussion_templates ORDER BY is_builtin DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var templates []models.DiscussionTemplate
	for rows.Next() {
		var t models.DiscussionTemplate
		var isBuiltin int
		if err := rows.Scan(&t.Name, &t.Description, &t.Body, &isBuiltin, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.IsBuiltin = isBuiltin != 0
		templates = append(templates, t)
	}
	return templates, nil
}

// UpdateDiscussionTemplate updates a discussion template.
func UpdateDiscussionTemplate(db *sql.DB, name string, fields map[string]any) error {
	var sets []string
	var vals []any
	for k, v := range fields {
		sets = append(sets, k+" = ?")
		vals = append(vals, v)
	}
	if len(sets) == 0 {
		return nil
	}
	vals = append(vals, name)
	_, err := db.Exec("UPDATE discussion_templates SET "+strings.Join(sets, ", ")+" WHERE name = ?", vals...)
	return err
}

// DeleteDiscussionTemplate removes a discussion template.
func DeleteDiscussionTemplate(db *sql.DB, name string) error {
	_, err := db.Exec(`DELETE FROM discussion_templates WHERE name = ?`, name)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
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

// UpdateAgentSessionStatus updates only the status of an agent session.
func UpdateAgentSessionStatus(db *sql.DB, id, status string) error {
	_, err := db.Exec(
		"UPDATE agent_sessions SET status = ?, updated_at = datetime('now') WHERE id = ?",
		status, id,
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
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	idea.CreatedAt = now
	idea.UpdatedAt = now
	res, err := db.Exec(
		`INSERT INTO idea_queue (project_id, title, raw_input, idea_type, status, spec_md, auto_implement, submitted_by, assigned_agent, feature_id, source_page, context, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		idea.ProjectID, idea.Title, idea.RawInput, idea.IdeaType, idea.Status,
		nullStr(idea.SpecMD), boolToInt(idea.AutoImplement), idea.SubmittedBy,
		nullStr(idea.AssignedAgent), nullStr(idea.FeatureID),
		idea.SourcePage, idea.Context,
		idea.CreatedAt, idea.UpdatedAt,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting last insert id: %w", err)
	}
	idea.ID = int(id)
	return nil
}

func GetIdea(db *sql.DB, id int) (*models.IdeaQueueItem, error) {
	row := db.QueryRow(`SELECT id, project_id, title, raw_input, idea_type, status,
		COALESCE(spec_md,''), auto_implement, COALESCE(submitted_by,'human'), COALESCE(assigned_agent,''),
		COALESCE(feature_id,''), COALESCE(source_page,''), COALESCE(context,''), created_at, updated_at
		FROM idea_queue WHERE id = ?`, id)
	item := &models.IdeaQueueItem{}
	var autoImpl int
	err := row.Scan(&item.ID, &item.ProjectID, &item.Title, &item.RawInput, &item.IdeaType, &item.Status,
		&item.SpecMD, &autoImpl, &item.SubmittedBy, &item.AssignedAgent,
		&item.FeatureID, &item.SourcePage, &item.Context, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	item.AutoImplement = autoImpl != 0
	return item, nil
}

func ListIdeas(db *sql.DB, projectID, status, ideaType string) ([]models.IdeaQueueItem, error) {
	q := `SELECT id, project_id, title, raw_input, idea_type, status,
		COALESCE(spec_md,''), auto_implement, COALESCE(submitted_by,'human'), COALESCE(assigned_agent,''),
		COALESCE(feature_id,''), COALESCE(source_page,''), COALESCE(context,''), created_at, updated_at
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
			&item.FeatureID, &item.SourcePage, &item.Context, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func UpdateIdeaType(db *sql.DB, id int, ideaType string) error {
	_, err := db.Exec("UPDATE idea_queue SET idea_type = ?, updated_at = datetime('now') WHERE id = ?", ideaType, id)
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
		LEFT JOIN cycle_instances ci ON ci.entity_id = w.feature_id AND ci.status = 'active'
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
		`INSERT INTO decisions (id, title, status, context, decision, consequences, feature_id, superseded_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Title, d.Status, nullStr(d.Context), nullStr(d.Decision), nullStr(d.Consequences), nullStr(d.FeatureID), nullStr(d.SupersededBy),
	)
	return err
}

func GetDecision(database *sql.DB, id string) (*models.Decision, error) {
	row := database.QueryRow(`SELECT id, title, status, COALESCE(context,''), COALESCE(decision,''), COALESCE(consequences,''), COALESCE(superseded_by,''), COALESCE(feature_id,''), created_at, updated_at
		FROM decisions WHERE id = ?`, id)
	d := &models.Decision{}
	err := row.Scan(&d.ID, &d.Title, &d.Status, &d.Context, &d.Decision, &d.Consequences, &d.SupersededBy, &d.FeatureID, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func ListDecisions(database *sql.DB, status string) ([]models.Decision, error) {
	q := `SELECT id, title, status, COALESCE(context,''), COALESCE(decision,''), COALESCE(consequences,''), COALESCE(superseded_by,''), COALESCE(feature_id,''), created_at, updated_at
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
		if err := rows.Scan(&d.ID, &d.Title, &d.Status, &d.Context, &d.Decision, &d.Consequences, &d.SupersededBy, &d.FeatureID, &d.CreatedAt, &d.UpdatedAt); err != nil {
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

// SupersedeDecision marks oldID as superseded by newID.
func SupersedeDecision(database *sql.DB, oldID, newID string) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	_, err = tx.Exec(
		`UPDATE decisions SET status = 'superseded', superseded_by = ?, updated_at = datetime('now') WHERE id = ?`,
		newID, oldID,
	)
	if err != nil {
		return fmt.Errorf("updating old decision: %w", err)
	}

	return tx.Commit()
}

// --- FTS5 Search ---

// BuildFTSQuery converts a user query string into FTS5 syntax with prefix matching.
// It handles special characters, multi-word queries, and adds * suffix for prefix matching.
func BuildFTSQuery(userQuery string) string {
	userQuery = strings.TrimSpace(userQuery)
	if userQuery == "" {
		return ""
	}

	// Remove FTS5 special characters that could cause syntax errors
	replacer := strings.NewReplacer(
		"(", " ", ")", " ",
		":", " ", "\"", " ",
		"'", " ", ";", " ",
		"!", " ", "^", " ",
		"{", " ", "}", " ",
		"[", " ", "]", " ",
	)
	cleaned := replacer.Replace(userQuery)

	words := strings.Fields(cleaned)
	if len(words) == 0 {
		return ""
	}

	// Each word gets a * suffix for prefix matching
	var terms []string
	for _, w := range words {
		if w == "AND" || w == "OR" || w == "NOT" || w == "NEAR" {
			w = strings.ToLower(w)
		}
		terms = append(terms, w+"*")
	}
	return strings.Join(terms, " ")
}

// SearchFTS performs a full-text search across features, roadmap items, and ideas
// using the FTS5 index. Results are ranked by relevance and include highlighted snippets.
func SearchFTS(database *sql.DB, query string, limit int) ([]models.SearchResult, error) {
	return SearchFTSFiltered(database, query, "", limit)
}

// SearchFTSFiltered performs a full-text search with optional entity type filtering.
// entityType can be empty (search all), or one of: feature, roadmap, idea, event, discussion.
func SearchFTSFiltered(database *sql.DB, query string, entityType string, limit int) ([]models.SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	ftsQuery := BuildFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	var rows *sql.Rows
	var err error
	if entityType != "" {
		rows, err = database.Query(`
			SELECT entity_type, entity_id, title,
				snippet(search_fts, 3, '>>>>', '<<<<', '...', 48) as snippet,
				rank
			FROM search_fts
			WHERE search_fts MATCH ? AND entity_type = ?
			ORDER BY rank
			LIMIT ?
		`, ftsQuery, entityType, limit)
	} else {
		rows, err = database.Query(`
			SELECT entity_type, entity_id, title,
				snippet(search_fts, 3, '>>>>', '<<<<', '...', 48) as snippet,
				rank
			FROM search_fts
			WHERE search_fts MATCH ?
			ORDER BY rank
			LIMIT ?
		`, ftsQuery, limit)
	}
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

// FuzzySearch performs fuzzy matching across features, roadmap items, and ideas.
// It uses substring matching with edit-distance scoring to tolerate typos.
func FuzzySearch(database *sql.DB, query string, entityType string, limit int) ([]models.SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return nil, nil
	}

	type candidate struct {
		entityType string
		entityID   string
		title      string
		content    string
	}

	var candidates []candidate

	// Load features
	if entityType == "" || entityType == "feature" {
		rows, err := database.Query(`SELECT id, name, COALESCE(description, '') || ' ' || COALESCE(spec, '') FROM features`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var c candidate
				rows.Scan(&c.entityID, &c.title, &c.content)
				c.entityType = "feature"
				candidates = append(candidates, c)
			}
		}
	}

	// Load roadmap items
	if entityType == "" || entityType == "roadmap" {
		rows, err := database.Query(`SELECT id, title, COALESCE(description, '') FROM roadmap_items`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var c candidate
				rows.Scan(&c.entityID, &c.title, &c.content)
				c.entityType = "roadmap"
				candidates = append(candidates, c)
			}
		}
	}

	// Load ideas
	if entityType == "" || entityType == "idea" {
		rows, err := database.Query(`SELECT CAST(id AS TEXT), title, COALESCE(raw_input, '') FROM idea_queue`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var c candidate
				rows.Scan(&c.entityID, &c.title, &c.content)
				c.entityType = "idea"
				candidates = append(candidates, c)
			}
		}
	}

	type scored struct {
		models.SearchResult
		score int
	}

	var results []scored
	for _, c := range candidates {
		titleLower := strings.ToLower(c.title)
		contentLower := strings.ToLower(c.content)
		fullText := titleLower + " " + contentLower

		score := fuzzyScore(query, titleLower, contentLower)
		if score > 0 {
			snippet := fuzzySnippet(query, fullText)
			results = append(results, scored{
				SearchResult: models.SearchResult{
					EntityType: c.entityType,
					EntityID:   c.entityID,
					Title:      c.title,
					Snippet:    snippet,
					Rank:       float64(-score), // negative so higher score = better rank
				},
				score: score,
			})
		}
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if len(results) > limit {
		results = results[:limit]
	}

	out := make([]models.SearchResult, len(results))
	for i, r := range results {
		out[i] = r.SearchResult
	}
	return out, nil
}

// fuzzyScore returns a relevance score (0 = no match) for how well query matches the title/content.
func fuzzyScore(query, title, content string) int {
	score := 0

	// Exact substring match in title (highest score)
	if strings.Contains(title, query) {
		score += 100
		// Bonus for matching at word boundary
		if strings.HasPrefix(title, query) || strings.Contains(title, " "+query) {
			score += 50
		}
	}

	// Exact substring match in content
	if strings.Contains(content, query) {
		score += 30
	}

	// Per-word matching (for multi-word queries)
	queryWords := strings.Fields(query)
	if len(queryWords) > 1 {
		matchedWords := 0
		for _, w := range queryWords {
			if strings.Contains(title, w) || strings.Contains(content, w) {
				matchedWords++
			}
		}
		if matchedWords > 0 {
			score += matchedWords * 20
		}
	}

	// Subsequence matching (characters appear in order — tolerates missing chars)
	if score == 0 {
		if fuzzySubsequenceMatch(query, title) {
			score += 15
		} else if fuzzySubsequenceMatch(query, content) {
			score += 5
		}
	}

	// Trigram matching for typo tolerance
	if score == 0 {
		titleSim := trigramSimilarity(query, title)
		contentSim := trigramSimilarity(query, content)
		if titleSim > 0.3 {
			score += int(titleSim * 40)
		} else if contentSim > 0.3 {
			score += int(contentSim * 15)
		}
	}

	return score
}

// fuzzySubsequenceMatch checks if all chars of query appear in order within text.
func fuzzySubsequenceMatch(query, text string) bool {
	qi := 0
	for i := 0; i < len(text) && qi < len(query); i++ {
		if text[i] == query[qi] {
			qi++
		}
	}
	return qi == len(query)
}

// trigramSimilarity returns a similarity score [0,1] based on shared trigrams.
func trigramSimilarity(a, b string) float64 {
	if len(a) < 3 || len(b) < 3 {
		return 0
	}
	trigramsA := make(map[string]bool)
	for i := 0; i <= len(a)-3; i++ {
		trigramsA[a[i:i+3]] = true
	}
	trigramsB := make(map[string]bool)
	for i := 0; i <= len(b)-3; i++ {
		trigramsB[b[i:i+3]] = true
	}

	shared := 0
	for t := range trigramsA {
		if trigramsB[t] {
			shared++
		}
	}
	total := len(trigramsA) + len(trigramsB) - shared
	if total == 0 {
		return 0
	}
	return float64(shared) / float64(total)
}

// fuzzySnippet extracts a snippet around the first match of query in text.
func fuzzySnippet(query, text string) string {
	idx := strings.Index(text, query)
	if idx < 0 {
		// Try first word
		words := strings.Fields(query)
		if len(words) > 0 {
			idx = strings.Index(text, words[0])
		}
	}
	if idx < 0 {
		if len(text) > 100 {
			return text[:100] + "..."
		}
		return text
	}

	start := idx - 30
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + 70
	if end > len(text) {
		end = len(text)
	}

	snippet := text[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(text) {
		snippet = snippet + "..."
	}
	return snippet
}

// SearchFeaturesFTS searches features by name, description, and spec via FTS5.
// Returns matching features with context snippets.
func SearchFeaturesFTS(database *sql.DB, projectID, query string, limit int) ([]models.FeatureSearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	ftsQuery := BuildFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	rows, err := database.Query(`
		SELECT f.id, f.name, f.status,
			snippet(search_fts, 3, '>>>>', '<<<<', '...', 48) as snippet
		FROM search_fts s
		JOIN features f ON s.entity_id = f.id AND s.entity_type = 'feature'
		WHERE search_fts MATCH ? AND f.project_id = ?
		ORDER BY s.rank
		LIMIT ?
	`, ftsQuery, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("feature FTS search: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var out []models.FeatureSearchResult
	for rows.Next() {
		var r models.FeatureSearchResult
		if err := rows.Scan(&r.ID, &r.Name, &r.Status, &r.Snippet); err != nil {
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

// GetActivityHeatmap returns daily event counts for the given number of past days.
func GetActivityHeatmap(database *sql.DB, projectID string, days int) ([]models.HeatmapDay, error) {
	rows, err := database.Query(`
		SELECT date(created_at) AS day, event_type, COUNT(*) AS cnt
		FROM events
		WHERE project_id = ? AND created_at >= datetime('now', ? || ' days')
		GROUP BY day, event_type
		ORDER BY day`, projectID, fmt.Sprintf("-%d", days))
	if err != nil {
		return nil, fmt.Errorf("querying activity heatmap: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	dayMap := make(map[string]*models.HeatmapDay)
	for rows.Next() {
		var day, eventType string
		var cnt int
		if err := rows.Scan(&day, &eventType, &cnt); err != nil {
			return nil, fmt.Errorf("scanning heatmap row: %w", err)
		}
		d, ok := dayMap[day]
		if !ok {
			d = &models.HeatmapDay{Date: day, Events: make(map[string]int)}
			dayMap[day] = d
		}
		d.Events[eventType] = cnt
		d.Count += cnt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]models.HeatmapDay, 0, len(dayMap))
	for _, d := range dayMap {
		out = append(out, *d)
	}
	// Sort by date ascending
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i].Date > out[j].Date {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// GetDailyActivityCounts returns simple date+count pairs for the last N days.
func GetDailyActivityCounts(database *sql.DB, projectID string, days int) ([]models.ActivityDayCount, error) {
	rows, err := database.Query(`
		SELECT date(created_at) AS day, COUNT(*) AS cnt
		FROM events
		WHERE project_id = ? AND created_at >= datetime('now', ? || ' days')
		GROUP BY day
		ORDER BY day`, projectID, fmt.Sprintf("-%d", days))
	if err != nil {
		return nil, fmt.Errorf("querying daily activity counts: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var out []models.ActivityDayCount
	for rows.Next() {
		var d models.ActivityDayCount
		if err := rows.Scan(&d.Date, &d.Count); err != nil {
			return nil, fmt.Errorf("scanning daily activity row: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RemoveFromIndex removes an entity from the FTS5 search index.
func RemoveFromIndex(database *sql.DB, entityType, entityID string) error {
	_, err := database.Exec(
		`DELETE FROM search_fts WHERE entity_type = ? AND entity_id = ?`,
		entityType, entityID,
	)
	return err
}

// GetWorkItemsWithTime returns work items for a feature that have both started_at and completed_at.
func GetWorkItemsWithTime(database *sql.DB, featureID string) ([]models.WorkItemTime, error) {
	rows, err := database.Query(`
		SELECT id, feature_id, work_type, status, started_at, completed_at,
			(julianday(completed_at) - julianday(started_at)) * 86400 AS duration_sec
		FROM work_items
		WHERE feature_id = ? AND started_at != '' AND completed_at != ''
		ORDER BY started_at ASC`, featureID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []models.WorkItemTime
	for rows.Next() {
		var w models.WorkItemTime
		if err := rows.Scan(&w.ID, &w.FeatureID, &w.WorkType, &w.Status,
			&w.StartedAt, &w.CompletedAt, &w.DurationSec); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetProjectTimeSummary returns aggregated time tracking data for the whole project.
func GetProjectTimeSummary(database *sql.DB) (*models.ProjectTimeSummary, error) {
	summary := &models.ProjectTimeSummary{}

	// Total time
	err := database.QueryRow(`
		SELECT COALESCE(SUM((julianday(completed_at) - julianday(started_at)) * 86400), 0)
		FROM work_items
		WHERE started_at != '' AND completed_at != ''`).Scan(&summary.TotalSec)
	if err != nil {
		return nil, fmt.Errorf("querying total time: %w", err)
	}

	// Average time per work type
	rows, err := database.Query(`
		SELECT work_type, COUNT(*) AS cnt,
			SUM((julianday(completed_at) - julianday(started_at)) * 86400) AS total_sec,
			AVG((julianday(completed_at) - julianday(started_at)) * 86400) AS avg_sec
		FROM work_items
		WHERE started_at != '' AND completed_at != ''
		GROUP BY work_type
		ORDER BY total_sec DESC`)
	if err != nil {
		return nil, fmt.Errorf("querying by work type: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var wt models.WorkTypeAvg
		if err := rows.Scan(&wt.WorkType, &wt.Count, &wt.TotalSec, &wt.AvgSec); err != nil {
			return nil, err
		}
		summary.ByWorkType = append(summary.ByWorkType, wt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Top 5 features by time
	rows2, err := database.Query(`
		SELECT w.feature_id, COALESCE(f.name, w.feature_id) AS name,
			SUM((julianday(w.completed_at) - julianday(w.started_at)) * 86400) AS total_sec
		FROM work_items w
		LEFT JOIN features f ON w.feature_id = f.id
		WHERE w.started_at != '' AND w.completed_at != ''
		GROUP BY w.feature_id
		ORDER BY total_sec DESC
		LIMIT 5`)
	if err != nil {
		return nil, fmt.Errorf("querying top features: %w", err)
	}
	defer rows2.Close() //nolint:errcheck
	for rows2.Next() {
		var ft models.FeatureTimeSummary
		if err := rows2.Scan(&ft.FeatureID, &ft.Name, &ft.TotalSec); err != nil {
			return nil, err
		}
		summary.TopFeatures = append(summary.TopFeatures, ft)
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	// Time by feature status
	rows3, err := database.Query(`
		SELECT COALESCE(f.status, 'unknown') AS status,
			SUM((julianday(w.completed_at) - julianday(w.started_at)) * 86400) AS total_sec
		FROM work_items w
		LEFT JOIN features f ON w.feature_id = f.id
		WHERE w.started_at != '' AND w.completed_at != ''
		GROUP BY f.status
		ORDER BY total_sec DESC`)
	if err != nil {
		return nil, fmt.Errorf("querying by status: %w", err)
	}
	defer rows3.Close() //nolint:errcheck
	for rows3.Next() {
		var st models.StatusTime
		if err := rows3.Scan(&st.Status, &st.TotalSec); err != nil {
			return nil, err
		}
		summary.ByStatus = append(summary.ByStatus, st)
	}
	return summary, rows3.Err()
}

// --- Feature Tags ---

func AddFeatureTag(database *sql.DB, featureID, tag string) error {
	_, err := database.Exec("INSERT OR IGNORE INTO feature_tags (feature_id, tag) VALUES (?, ?)", featureID, tag)
	return err
}

func RemoveFeatureTag(database *sql.DB, featureID, tag string) error {
	_, err := database.Exec("DELETE FROM feature_tags WHERE feature_id = ? AND tag = ?", featureID, tag)
	return err
}

func GetFeatureTags(database *sql.DB, featureID string) ([]string, error) {
	rows, err := database.Query("SELECT tag FROM feature_tags WHERE feature_id = ? ORDER BY tag", featureID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

func ListAllTags(database *sql.DB, projectID string) ([]models.TagCount, error) {
	rows, err := database.Query(`
		SELECT ft.tag, COUNT(*) as cnt
		FROM feature_tags ft
		JOIN features f ON ft.feature_id = f.id
		WHERE f.project_id = ?
		GROUP BY ft.tag
		ORDER BY cnt DESC, ft.tag`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []models.TagCount
	for rows.Next() {
		var tc models.TagCount
		if err := rows.Scan(&tc.Tag, &tc.Count); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

func GetFeaturesByTag(database *sql.DB, projectID, tag string) ([]models.Feature, error) {
	q := `SELECT f.id, f.project_id, COALESCE(f.milestone_id,''), f.name, COALESCE(f.description,''), COALESCE(f.spec,''),
			f.status, f.priority, COALESCE(f.assigned_cycle,''), COALESCE(f.roadmap_item_id,''),
			f.created_at, f.updated_at, COALESCE(m.name,''), COALESCE(f.previous_status,''),
			COALESCE(f.estimate_points,0), COALESCE(f.estimate_size,'')
		FROM features f
		LEFT JOIN milestones m ON f.milestone_id = m.id
		JOIN feature_tags ft ON f.id = ft.feature_id
		WHERE f.project_id = ? AND ft.tag = ?
		ORDER BY f.priority DESC, f.created_at`
	rows, err := database.Query(q, projectID, tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Feature
	for rows.Next() {
		var f models.Feature
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.MilestoneID, &f.Name, &f.Description, &f.Spec,
			&f.Status, &f.Priority, &f.AssignedCycle, &f.RoadmapItemID, &f.CreatedAt, &f.UpdatedAt, &f.MilestoneName, &f.PreviousStatus,
			&f.EstimatePoints, &f.EstimateSize); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Bulk-load tags for returned features
	if len(out) > 0 {
		tagRows, err := database.Query("SELECT feature_id, tag FROM feature_tags ORDER BY tag")
		if err == nil {
			defer tagRows.Close() //nolint:errcheck
			tagMap := make(map[string][]string)
			for tagRows.Next() {
				var fid, t string
				if err := tagRows.Scan(&fid, &t); err == nil {
					tagMap[fid] = append(tagMap[fid], t)
				}
			}
			for i := range out {
				if tags, ok := tagMap[out[i].ID]; ok {
					out[i].Tags = tags
				}
			}
		}
	}

	return out, nil
}

// GetCompletedFeatures returns features matching given statuses, optionally filtered by milestone and date.
func GetCompletedFeatures(database *sql.DB, projectID, since, milestoneID string, statuses []string) ([]models.Feature, error) {
	q := `SELECT f.id, f.project_id, COALESCE(f.milestone_id,''), f.name, COALESCE(f.description,''), COALESCE(f.spec,''),
f.status, f.priority, COALESCE(f.assigned_cycle,''), COALESCE(f.roadmap_item_id,''),
f.created_at, f.updated_at, COALESCE(m.name,''), COALESCE(f.previous_status,''),
COALESCE(f.estimate_points,0), COALESCE(f.estimate_size,'')
FROM features f LEFT JOIN milestones m ON f.milestone_id = m.id
WHERE f.project_id = ?`
	args := []any{projectID}
	if len(statuses) > 0 {
		placeholders := make([]string, len(statuses))
		for i, s := range statuses {
			placeholders[i] = "?"
			args = append(args, s)
		}
		q += " AND f.status IN (" + strings.Join(placeholders, ",") + ")"
	}
	if milestoneID != "" {
		q += " AND f.milestone_id = ?"
		args = append(args, milestoneID)
	}
	if since != "" {
		q += " AND f.updated_at >= ?"
		args = append(args, since)
	}
	q += " ORDER BY f.updated_at DESC"
	rows, err := database.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []models.Feature
	for rows.Next() {
		var f models.Feature
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.MilestoneID, &f.Name, &f.Description, &f.Spec,
			&f.Status, &f.Priority, &f.AssignedCycle, &f.RoadmapItemID, &f.CreatedAt, &f.UpdatedAt, &f.MilestoneName, &f.PreviousStatus,
			&f.EstimatePoints, &f.EstimateSize); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetAgentStatusDashboard returns enriched agent session data for the heartbeat dashboard.
func GetAgentStatusDashboard(database *sql.DB, projectID string) (*models.AgentStatusDashboard, error) {
	const staleMins = 5
	now := time.Now()

	sessions, err := ListAgentSessions(database, projectID, "")
	if err != nil {
		return nil, fmt.Errorf("listing agent sessions: %w", err)
	}

	var agents []models.AgentHeartbeatInfo
	var activeCount, staleCount, failedCount, completedCount, totalWorkDone int

	for _, s := range sessions {
		info := models.AgentHeartbeatInfo{Session: s}

		// Determine heartbeat status
		switch s.Status {
		case "completed":
			info.HeartbeatStatus = "completed"
			completedCount++
		case "failed", "abandoned":
			info.HeartbeatStatus = "failed"
			failedCount++
		case "active":
			updatedAt, parseErr := time.Parse("2006-01-02 15:04:05", s.UpdatedAt)
			if parseErr != nil {
				updatedAt = now
			}
			minutesAgo := now.Sub(updatedAt).Minutes()
			if minutesAgo <= float64(staleMins) {
				info.HeartbeatStatus = "active"
				activeCount++
			} else {
				info.HeartbeatStatus = "stale"
				staleCount++
			}
		default:
			info.HeartbeatStatus = s.Status
		}

		// Session duration
		createdAt, parseErr := time.Parse("2006-01-02 15:04:05", s.CreatedAt)
		if parseErr == nil {
			info.SessionDuration = int64(now.Sub(createdAt).Seconds())
		}

		// Fetch feature name
		if s.FeatureID != "" {
			var fname sql.NullString
			_ = database.QueryRow(`SELECT name FROM features WHERE id = ?`, s.FeatureID).Scan(&fname)
			if fname.Valid {
				info.FeatureName = fname.String
			}
		}

		// Current active work item for this agent
		row := database.QueryRow(`SELECT id, feature_id, work_type, status,
			COALESCE(agent_prompt,''), COALESCE(result,''), COALESCE(assigned_agent,''),
			COALESCE(started_at,''), COALESCE(completed_at,''), created_at
			FROM work_items
			WHERE assigned_agent = ? AND status = 'active'
			ORDER BY started_at DESC LIMIT 1`, s.ID)
		var wi models.WorkItem
		if scanErr := row.Scan(&wi.ID, &wi.FeatureID, &wi.WorkType, &wi.Status,
			&wi.AgentPrompt, &wi.Result, &wi.AssignedAgent,
			&wi.StartedAt, &wi.CompletedAt, &wi.CreatedAt); scanErr == nil {
			info.CurrentWorkItem = &wi
		}

		// Completed/failed work item counts
		_ = database.QueryRow(`SELECT COUNT(*) FROM work_items WHERE assigned_agent = ? AND status = 'done'`,
			s.ID).Scan(&info.CompletedCount)
		_ = database.QueryRow(`SELECT COUNT(*) FROM work_items WHERE assigned_agent = ? AND status = 'failed'`,
			s.ID).Scan(&info.FailedCount)
		totalWorkDone += info.CompletedCount

		agents = append(agents, info)
	}

	return &models.AgentStatusDashboard{
		Agents:         agents,
		TotalSessions:  len(sessions),
		ActiveCount:    activeCount,
		StaleCount:     staleCount,
		FailedCount:    failedCount,
		CompletedCount: completedCount,
		TotalWorkDone:  totalWorkDone,
	}, nil
}

// --- Webhooks ---

func CreateWebhook(db *sql.DB, w *models.Webhook) error {
	_, err := db.Exec(
		`INSERT INTO webhooks (id, url, secret, events, active) VALUES (?, ?, ?, ?, ?)`,
		w.ID, w.URL, nullStr(w.Secret), w.Events, boolToInt(w.Active),
	)
	return err
}

func GetWebhook(db *sql.DB, id string) (*models.Webhook, error) {
	row := db.QueryRow(`SELECT id, url, COALESCE(secret,''), events, active, created_at FROM webhooks WHERE id = ?`, id)
	w := &models.Webhook{}
	var active int
	err := row.Scan(&w.ID, &w.URL, &w.Secret, &w.Events, &active, &w.CreatedAt)
	if err != nil {
		return nil, err
	}
	w.Active = active != 0
	return w, nil
}

func ListWebhooks(db *sql.DB) ([]models.Webhook, error) {
	rows, err := db.Query(`SELECT id, url, COALESCE(secret,''), events, active, created_at FROM webhooks ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Webhook
	for rows.Next() {
		var w models.Webhook
		var active int
		if err := rows.Scan(&w.ID, &w.URL, &w.Secret, &w.Events, &active, &w.CreatedAt); err != nil {
			return nil, err
		}
		w.Active = active != 0
		out = append(out, w)
	}
	return out, rows.Err()
}

func ListActiveWebhooks(db *sql.DB) ([]models.Webhook, error) {
	rows, err := db.Query(`SELECT id, url, COALESCE(secret,''), events, active, created_at FROM webhooks WHERE active = 1 ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Webhook
	for rows.Next() {
		var w models.Webhook
		var active int
		if err := rows.Scan(&w.ID, &w.URL, &w.Secret, &w.Events, &active, &w.CreatedAt); err != nil {
			return nil, err
		}
		w.Active = active != 0
		out = append(out, w)
	}
	return out, rows.Err()
}

func DeleteWebhook(db *sql.DB, id string) error {
	res, err := db.Exec(`DELETE FROM webhooks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("webhook %q not found", id)
	}
	return nil
}

// GetAgentStats computes aggregate agent performance metrics.
// If agentFilter is non-empty, results are scoped to that agent name.
func GetAgentStats(database *sql.DB, projectID, agentFilter string) (*models.AgentStats, error) {
	stats := &models.AgentStats{}

	// --- Total completed / failed work items ---
	completedQuery := `SELECT COUNT(*) FROM work_items w
		JOIN features f ON w.feature_id = f.id
		WHERE f.project_id = ? AND w.status = 'done'`
	failedQuery := `SELECT COUNT(*) FROM work_items w
		JOIN features f ON w.feature_id = f.id
		WHERE f.project_id = ? AND w.status = 'failed'`
	completedArgs := []any{projectID}
	failedArgs := []any{projectID}

	if agentFilter != "" {
		completedQuery += ` AND w.assigned_agent IN (SELECT id FROM agent_sessions WHERE name = ?)`
		failedQuery += ` AND w.assigned_agent IN (SELECT id FROM agent_sessions WHERE name = ?)`
		completedArgs = append(completedArgs, agentFilter)
		failedArgs = append(failedArgs, agentFilter)
	}

	if err := database.QueryRow(completedQuery, completedArgs...).Scan(&stats.TotalCompleted); err != nil {
		return nil, fmt.Errorf("counting completed items: %w", err)
	}
	if err := database.QueryRow(failedQuery, failedArgs...).Scan(&stats.TotalFailed); err != nil {
		return nil, fmt.Errorf("counting failed items: %w", err)
	}

	// --- Average completion time by work type ---
	avgQuery := `SELECT w.work_type, COUNT(*) AS cnt,
			AVG((julianday(w.completed_at) - julianday(w.started_at)) * 86400) AS avg_sec
		FROM work_items w
		JOIN features f ON w.feature_id = f.id
		WHERE f.project_id = ? AND w.started_at != '' AND w.completed_at != ''
			AND w.status = 'done'`
	avgArgs := []any{projectID}
	if agentFilter != "" {
		avgQuery += ` AND w.assigned_agent IN (SELECT id FROM agent_sessions WHERE name = ?)`
		avgArgs = append(avgArgs, agentFilter)
	}
	avgQuery += ` GROUP BY w.work_type ORDER BY avg_sec DESC`

	rows, err := database.Query(avgQuery, avgArgs...)
	if err != nil {
		return nil, fmt.Errorf("querying avg by work type: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var wt models.AgentWorkTypeStat
		if err := rows.Scan(&wt.WorkType, &wt.Count, &wt.AvgSec); err != nil {
			return nil, err
		}
		stats.AvgByWorkType = append(stats.AvgByWorkType, wt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// --- Success rate per agent ---
	srQuery := `SELECT a.name,
			SUM(CASE WHEN a.status = 'completed' THEN 1 ELSE 0 END) AS completed,
			SUM(CASE WHEN a.status = 'failed' THEN 1 ELSE 0 END) AS failed,
			COUNT(*) AS total
		FROM agent_sessions a
		WHERE a.project_id = ? AND a.status IN ('completed', 'failed')`
	srArgs := []any{projectID}
	if agentFilter != "" {
		srQuery += ` AND a.name = ?`
		srArgs = append(srArgs, agentFilter)
	}
	srQuery += ` GROUP BY a.name ORDER BY total DESC`

	rows2, err := database.Query(srQuery, srArgs...)
	if err != nil {
		return nil, fmt.Errorf("querying success rates: %w", err)
	}
	defer rows2.Close() //nolint:errcheck
	for rows2.Next() {
		var sr models.AgentSuccessRate
		if err := rows2.Scan(&sr.AgentName, &sr.Completed, &sr.Failed, &sr.Total); err != nil {
			return nil, err
		}
		if sr.Total > 0 {
			sr.SuccessRate = float64(sr.Completed) / float64(sr.Total) * 100
		}
		stats.SuccessRates = append(stats.SuccessRates, sr)
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	// --- Active agents and current tasks ---
	activeQuery := `SELECT a.name, a.id, COALESCE(a.task_description, ''),
			COALESCE(a.current_phase, ''), a.progress_pct, a.created_at
		FROM agent_sessions a
		WHERE a.project_id = ? AND a.status = 'active'`
	activeArgs := []any{projectID}
	if agentFilter != "" {
		activeQuery += ` AND a.name = ?`
		activeArgs = append(activeArgs, agentFilter)
	}
	activeQuery += ` ORDER BY a.updated_at DESC`

	rows3, err := database.Query(activeQuery, activeArgs...)
	if err != nil {
		return nil, fmt.Errorf("querying active agents: %w", err)
	}
	defer rows3.Close() //nolint:errcheck
	for rows3.Next() {
		var at models.AgentActiveTask
		if err := rows3.Scan(&at.AgentName, &at.SessionID, &at.TaskDescription,
			&at.CurrentPhase, &at.ProgressPct, &at.StartedAt); err != nil {
			return nil, err
		}
		stats.ActiveAgents = append(stats.ActiveAgents, at)
	}
	if err := rows3.Err(); err != nil {
		return nil, err
	}

	// --- Throughput (items per hour over 24h, 7d, 30d) ---
	type windowDef struct {
		label string
		hours float64
	}
	windows := []windowDef{
		{"last 24h", 24},
		{"last 7d", 24 * 7},
		{"last 30d", 24 * 30},
	}
	for _, w := range windows {
		since := time.Now().UTC().Add(-time.Duration(w.hours) * time.Hour).Format("2006-01-02T15:04:05")
		tpQuery := `SELECT COUNT(*) FROM work_items wi
			JOIN features f ON wi.feature_id = f.id
			WHERE f.project_id = ? AND wi.status = 'done'
				AND wi.completed_at >= ?`
		tpArgs := []any{projectID, since}
		if agentFilter != "" {
			tpQuery += ` AND wi.assigned_agent IN (SELECT id FROM agent_sessions WHERE name = ?)`
			tpArgs = append(tpArgs, agentFilter)
		}
		var count int
		if err := database.QueryRow(tpQuery, tpArgs...).Scan(&count); err != nil {
			return nil, fmt.Errorf("querying throughput %s: %w", w.label, err)
		}
		iph := 0.0
		if w.hours > 0 {
			iph = float64(count) / w.hours
		}
		stats.Throughput = append(stats.Throughput, models.AgentThroughput{
			Period:       w.label,
			ItemsTotal:   count,
			HoursSpan:    w.hours,
			ItemsPerHour: iph,
		})
	}

	return stats, nil
}

// --- Sprints ---

func CreateSprint(db *sql.DB, s *models.Sprint) error {
	_, err := db.Exec(
		`INSERT INTO sprints (id, project_id, name, goal, start_date, end_date, status) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.ProjectID, s.Name, s.Goal, s.StartDate, s.EndDate, s.Status,
	)
	return err
}

func GetSprint(db *sql.DB, id string) (*models.Sprint, error) {
	row := db.QueryRow(`
		SELECT s.id, s.project_id, s.name, s.goal, s.start_date, s.end_date, s.status,
			s.created_at, s.updated_at,
			COUNT(sf.feature_id) AS total,
			COALESCE(SUM(CASE WHEN f.status = 'done' THEN 1 ELSE 0 END), 0) AS done,
			COALESCE(SUM(CASE WHEN f.status IN ('implementing','agent-qa','human-qa') THEN 1 ELSE 0 END), 0) AS in_prog,
			COALESCE(SUM(CASE WHEN f.status IN ('draft','planning','blocked') THEN 1 ELSE 0 END), 0) AS not_started
		FROM sprints s
		LEFT JOIN sprint_features sf ON sf.sprint_id = s.id
		LEFT JOIN features f ON f.id = sf.feature_id
		WHERE s.id = ?
		GROUP BY s.id`, id)
	s := &models.Sprint{}
	err := row.Scan(&s.ID, &s.ProjectID, &s.Name, &s.Goal, &s.StartDate, &s.EndDate, &s.Status,
		&s.CreatedAt, &s.UpdatedAt, &s.TotalFeatures, &s.DoneFeatures, &s.InProgFeatures, &s.NotStartFeatures)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func ListSprints(db *sql.DB, projectID string) ([]models.Sprint, error) {
	rows, err := db.Query(`
		SELECT s.id, s.project_id, s.name, s.goal, s.start_date, s.end_date, s.status,
			s.created_at, s.updated_at,
			COUNT(sf.feature_id) AS total,
			COALESCE(SUM(CASE WHEN f.status = 'done' THEN 1 ELSE 0 END), 0) AS done,
			COALESCE(SUM(CASE WHEN f.status IN ('implementing','agent-qa','human-qa') THEN 1 ELSE 0 END), 0) AS in_prog,
			COALESCE(SUM(CASE WHEN f.status IN ('draft','planning','blocked') THEN 1 ELSE 0 END), 0) AS not_started
		FROM sprints s
		LEFT JOIN sprint_features sf ON sf.sprint_id = s.id
		LEFT JOIN features f ON f.id = sf.feature_id
		WHERE s.project_id = ?
		GROUP BY s.id
		ORDER BY s.start_date DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Sprint
	for rows.Next() {
		var s models.Sprint
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.Name, &s.Goal, &s.StartDate, &s.EndDate, &s.Status,
			&s.CreatedAt, &s.UpdatedAt, &s.TotalFeatures, &s.DoneFeatures, &s.InProgFeatures, &s.NotStartFeatures); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func GetActiveSprint(db *sql.DB, projectID string) (*models.Sprint, error) {
	row := db.QueryRow(`
		SELECT s.id, s.project_id, s.name, s.goal, s.start_date, s.end_date, s.status,
			s.created_at, s.updated_at,
			COUNT(sf.feature_id) AS total,
			COALESCE(SUM(CASE WHEN f.status = 'done' THEN 1 ELSE 0 END), 0) AS done,
			COALESCE(SUM(CASE WHEN f.status IN ('implementing','agent-qa','human-qa') THEN 1 ELSE 0 END), 0) AS in_prog,
			COALESCE(SUM(CASE WHEN f.status IN ('draft','planning','blocked') THEN 1 ELSE 0 END), 0) AS not_started
		FROM sprints s
		LEFT JOIN sprint_features sf ON sf.sprint_id = s.id
		LEFT JOIN features f ON f.id = sf.feature_id
		WHERE s.project_id = ? AND s.status = 'active'
		GROUP BY s.id
		LIMIT 1`, projectID)
	s := &models.Sprint{}
	err := row.Scan(&s.ID, &s.ProjectID, &s.Name, &s.Goal, &s.StartDate, &s.EndDate, &s.Status,
		&s.CreatedAt, &s.UpdatedAt, &s.TotalFeatures, &s.DoneFeatures, &s.InProgFeatures, &s.NotStartFeatures)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func CloseSprint(db *sql.DB, id string) error {
	_, err := db.Exec(`UPDATE sprints SET status = 'closed', updated_at = datetime('now') WHERE id = ?`, id)
	return err
}

func AddFeatureToSprint(db *sql.DB, sprintID, featureID string) error {
	_, err := db.Exec(`INSERT INTO sprint_features (sprint_id, feature_id) VALUES (?, ?)`, sprintID, featureID)
	return err
}

func RemoveFeatureFromSprint(db *sql.DB, sprintID, featureID string) error {
	res, err := db.Exec(`DELETE FROM sprint_features WHERE sprint_id = ? AND feature_id = ?`, sprintID, featureID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("feature %q is not in sprint %q", featureID, sprintID)
	}
	return nil
}

func ListSprintFeatures(db *sql.DB, sprintID string) ([]models.Feature, error) {
	rows, err := db.Query(`
		SELECT f.id, f.project_id, COALESCE(f.milestone_id,''), f.name, COALESCE(f.description,''), COALESCE(f.spec,''),
			f.status, f.priority, COALESCE(f.assigned_cycle,''), COALESCE(f.roadmap_item_id,''),
			f.created_at, f.updated_at, COALESCE(m.name,'') AS ms_name, COALESCE(f.previous_status,''),
			COALESCE(f.estimate_points,0), COALESCE(f.estimate_size,'')
		FROM features f
		JOIN sprint_features sf ON sf.feature_id = f.id
		LEFT JOIN milestones m ON f.milestone_id = m.id
		WHERE sf.sprint_id = ?
		ORDER BY f.priority DESC, f.name`, sprintID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Feature
	for rows.Next() {
		var f models.Feature
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.MilestoneID, &f.Name, &f.Description, &f.Spec,
			&f.Status, &f.Priority, &f.AssignedCycle, &f.RoadmapItemID, &f.CreatedAt, &f.UpdatedAt,
			&f.MilestoneName, &f.PreviousStatus, &f.EstimatePoints, &f.EstimateSize); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func HasActiveSprint(db *sql.DB, projectID string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sprints WHERE project_id = ? AND status = 'active'`, projectID).Scan(&count)
	return count > 0, err
}

// --- Feature PRs ---

func LinkFeaturePR(db *sql.DB, pr *models.FeaturePR) error {
	_, err := db.Exec(
		`INSERT INTO feature_prs (feature_id, pr_url, pr_number, repo, status) VALUES (?, ?, ?, ?, ?)`,
		pr.FeatureID, pr.PRURL, pr.PRNumber, pr.Repo, pr.Status,
	)
	return err
}

func UnlinkFeaturePR(db *sql.DB, featureID, prURL string) error {
	res, err := db.Exec(`DELETE FROM feature_prs WHERE feature_id = ? AND pr_url = ?`, featureID, prURL)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("no PR link found for feature %q and URL %q", featureID, prURL)
	}
	return nil
}

func ListFeaturePRs(db *sql.DB, featureID string) ([]models.FeaturePR, error) {
	rows, err := db.Query(
		`SELECT feature_id, pr_url, COALESCE(pr_number,0), COALESCE(repo,''), status, created_at
		 FROM feature_prs WHERE feature_id = ? ORDER BY created_at`, featureID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.FeaturePR
	for rows.Next() {
		var pr models.FeaturePR
		if err := rows.Scan(&pr.FeatureID, &pr.PRURL, &pr.PRNumber, &pr.Repo, &pr.Status, &pr.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

func ListAllPRs(db *sql.DB) ([]models.FeaturePR, error) {
	rows, err := db.Query(
		`SELECT feature_id, pr_url, COALESCE(pr_number,0), COALESCE(repo,''), status, created_at
		 FROM feature_prs ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.FeaturePR
	for rows.Next() {
		var pr models.FeaturePR
		if err := rows.Scan(&pr.FeatureID, &pr.PRURL, &pr.PRNumber, &pr.Repo, &pr.Status, &pr.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// --- Cycle Templates ---

func InsertCycleTemplate(db *sql.DB, name, description string, steps []models.CycleStep) error {
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return fmt.Errorf("marshaling steps: %w", err)
	}
	_, err = db.Exec(
		`INSERT INTO cycle_templates (name, description, steps, is_builtin) VALUES (?, ?, ?, 0)`,
		name, description, string(stepsJSON),
	)
	return err
}

func ListCycleTemplates(db *sql.DB) ([]models.CycleTemplate, error) {
	rows, err := db.Query(`SELECT name, description, steps, is_builtin, created_at FROM cycle_templates ORDER BY is_builtin DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.CycleTemplate
	for rows.Next() {
		var t models.CycleTemplate
		var stepsJSON string
		var isBuiltin int
		if err := rows.Scan(&t.Name, &t.Description, &stepsJSON, &isBuiltin, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.IsBuiltin = isBuiltin != 0
		steps, parseErr := models.ParseStepsJSON(stepsJSON)
		if parseErr != nil {
			return nil, fmt.Errorf("unmarshaling steps for %s: %w", t.Name, parseErr)
		}
		t.Steps = steps
		out = append(out, t)
	}
	return out, rows.Err()
}

func GetCycleTemplate(db *sql.DB, name string) (*models.CycleTemplate, error) {
	row := db.QueryRow(`SELECT name, description, steps, is_builtin, created_at FROM cycle_templates WHERE name = ?`, name)
	var t models.CycleTemplate
	var stepsJSON string
	var isBuiltin int
	if err := row.Scan(&t.Name, &t.Description, &stepsJSON, &isBuiltin, &t.CreatedAt); err != nil {
		return nil, err
	}
	t.IsBuiltin = isBuiltin != 0
	steps, parseErr := models.ParseStepsJSON(stepsJSON)
	if parseErr != nil {
		return nil, fmt.Errorf("unmarshaling steps for %s: %w", t.Name, parseErr)
	}
	t.Steps = steps
	return &t, nil
}

func DeleteCycleTemplate(db *sql.DB, name string) error {
	res, err := db.Exec(`DELETE FROM cycle_templates WHERE name = ? AND is_builtin = 0`, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("template %q not found or is built-in", name)
	}
	return nil
}

// InsertCommandMetric records a CLI command execution metric.
func InsertCommandMetric(database *sql.DB, command string, durationMs float64, success bool, dbQueries int) error {
	s := 0
	if success {
		s = 1
	}
	_, err := database.Exec(
		`INSERT INTO command_metrics (command, duration_ms, success, db_queries) VALUES (?, ?, ?, ?)`,
		command, durationMs, s, dbQueries,
	)
	return err
}

// GetPerfSummary returns aggregated performance metrics.
func GetPerfSummary(database *sql.DB, limit int) (*models.PerfSummary, error) {
	if limit <= 0 {
		limit = 100
	}

	summary := &models.PerfSummary{}

	// Overall stats
	err := database.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(AVG(duration_ms), 0),
			COALESCE(SUM(CASE WHEN success = 1 THEN 1.0 ELSE 0.0 END) / NULLIF(COUNT(*), 0) * 100, 0)
		FROM command_metrics
	`).Scan(&summary.TotalCommands, &summary.AvgDurationMs, &summary.SuccessRate)
	if err != nil {
		return nil, fmt.Errorf("querying perf summary: %w", err)
	}

	// P95
	_ = database.QueryRow(`
		SELECT COALESCE(duration_ms, 0)
		FROM command_metrics
		ORDER BY duration_ms ASC
		LIMIT 1 OFFSET (SELECT MAX(CAST(COUNT(*) * 0.95 AS INTEGER) - 1, 0) FROM command_metrics)
	`).Scan(&summary.P95DurationMs)

	// Per-command breakdown
	rows, err := database.Query(`
		SELECT
			command,
			COUNT(*),
			COALESCE(AVG(duration_ms), 0),
			COALESCE(MAX(duration_ms), 0),
			COALESCE(SUM(CASE WHEN success = 1 THEN 1.0 ELSE 0.0 END) / NULLIF(COUNT(*), 0) * 100, 0)
		FROM command_metrics
		GROUP BY command
		ORDER BY COUNT(*) DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("querying per-command stats: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	for rows.Next() {
		var s models.CommandPerfStats
		if err := rows.Scan(&s.Command, &s.Count, &s.AvgDurationMs, &s.MaxDurationMs, &s.SuccessRate); err != nil {
			return nil, err
		}
		summary.ByCommand = append(summary.ByCommand, s)
	}

	// Recent slow commands (top 10 by duration)
	slowRows, err := database.Query(`
		SELECT id, command, duration_ms, success, db_queries, created_at
		FROM command_metrics
		ORDER BY duration_ms DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("querying slow commands: %w", err)
	}
	defer slowRows.Close() //nolint:errcheck

	for slowRows.Next() {
		var m models.CommandMetric
		var success int
		if err := slowRows.Scan(&m.ID, &m.Command, &m.DurationMs, &success, &m.DBQueries, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Success = success == 1
		summary.RecentSlow = append(summary.RecentSlow, m)
	}

	return summary, nil
}

// --- Dashboard Configs ---

func CreateDashboardConfig(db *sql.DB, dc *models.DashboardConfig) error {
	_, err := db.Exec(
		`INSERT INTO dashboard_configs (id, project_id, name, layout, is_default) VALUES (?, ?, ?, ?, ?)`,
		dc.ID, dc.ProjectID, dc.Name, string(dc.Layout), boolToInt(dc.IsDefault),
	)
	return err
}

func GetDashboardConfig(db *sql.DB, id string) (*models.DashboardConfig, error) {
	row := db.QueryRow(`SELECT id, project_id, name, layout, is_default, created_at FROM dashboard_configs WHERE id = ?`, id)
	dc := &models.DashboardConfig{}
	var isDefault int
	var layout string
	err := row.Scan(&dc.ID, &dc.ProjectID, &dc.Name, &layout, &isDefault, &dc.CreatedAt)
	if err != nil {
		return nil, err
	}
	dc.Layout = json.RawMessage(layout)
	dc.IsDefault = isDefault == 1
	return dc, nil
}

func ListDashboardConfigs(db *sql.DB, projectID string) ([]models.DashboardConfig, error) {
	rows, err := db.Query(
		`SELECT id, project_id, name, layout, is_default, created_at FROM dashboard_configs WHERE project_id = ? ORDER BY created_at`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.DashboardConfig
	for rows.Next() {
		var dc models.DashboardConfig
		var isDefault int
		var layout string
		if err := rows.Scan(&dc.ID, &dc.ProjectID, &dc.Name, &layout, &isDefault, &dc.CreatedAt); err != nil {
			return nil, err
		}
		dc.Layout = json.RawMessage(layout)
		dc.IsDefault = isDefault == 1
		out = append(out, dc)
	}
	return out, rows.Err()
}

func DeleteDashboardConfig(db *sql.DB, id string) error {
	res, err := db.Exec(`DELETE FROM dashboard_configs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func SetDefaultDashboard(db *sql.DB, projectID, id string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Clear existing default for this project.
	if _, err := tx.Exec(`UPDATE dashboard_configs SET is_default = 0 WHERE project_id = ?`, projectID); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE dashboard_configs SET is_default = 1 WHERE id = ? AND project_id = ?`, id, projectID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// AddAgentCapability registers a capability for an agent.
func AddAgentCapability(db *sql.DB, agentID, capability string) error {
	_, err := db.Exec(
		`INSERT OR IGNORE INTO agent_capabilities (agent_id, capability) VALUES (?, ?)`,
		agentID, capability,
	)
	return err
}

// RemoveAgentCapability removes a capability from an agent.
func RemoveAgentCapability(db *sql.DB, agentID, capability string) error {
	res, err := db.Exec(
		`DELETE FROM agent_capabilities WHERE agent_id = ? AND capability = ?`,
		agentID, capability,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("capability %q not found for agent %q", capability, agentID)
	}
	return nil
}

// ListAgentCapabilities returns capabilities for a specific agent.
func ListAgentCapabilities(db *sql.DB, agentID string) ([]string, error) {
	rows, err := db.Query(
		`SELECT capability FROM agent_capabilities WHERE agent_id = ? ORDER BY capability`,
		agentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var caps []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		caps = append(caps, c)
	}
	return caps, rows.Err()
}

// ListAllAgentCapabilities returns a map of agent ID → capabilities.
func ListAllAgentCapabilities(db *sql.DB) (map[string][]string, error) {
	rows, err := db.Query(
		`SELECT agent_id, capability FROM agent_capabilities ORDER BY agent_id, capability`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	out := make(map[string][]string)
	for rows.Next() {
		var agentID, cap string
		if err := rows.Scan(&agentID, &cap); err != nil {
			return nil, err
		}
		out[agentID] = append(out[agentID], cap)
	}
	return out, rows.Err()
}

// --- Undo Log ---

func InsertUndoEntry(database *sql.DB, entry *models.UndoEntry) error {
	_, err := database.Exec(
		`INSERT INTO undo_log (project_id, operation, entity_type, entity_id, before_data, after_data)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		entry.ProjectID, entry.Operation, entry.EntityType, entry.EntityID, entry.BeforeData, entry.AfterData,
	)
	return err
}

func GetLastUndoEntry(database *sql.DB, projectID string) (*models.UndoEntry, error) {
	row := database.QueryRow(`
		SELECT id, project_id, operation, entity_type, entity_id, before_data, after_data, undone, created_at
		FROM undo_log
		WHERE project_id = ? AND undone = 0
		ORDER BY id DESC LIMIT 1`, projectID)
	e := &models.UndoEntry{}
	var undone int
	err := row.Scan(&e.ID, &e.ProjectID, &e.Operation, &e.EntityType, &e.EntityID,
		&e.BeforeData, &e.AfterData, &undone, &e.CreatedAt)
	if err != nil {
		return nil, err
	}
	e.Undone = undone == 1
	return e, nil
}

func GetLastUndoneEntry(database *sql.DB, projectID string) (*models.UndoEntry, error) {
	row := database.QueryRow(`
		SELECT id, project_id, operation, entity_type, entity_id, before_data, after_data, undone, created_at
		FROM undo_log
		WHERE project_id = ? AND undone = 1
		ORDER BY id DESC LIMIT 1`, projectID)
	e := &models.UndoEntry{}
	var undone int
	err := row.Scan(&e.ID, &e.ProjectID, &e.Operation, &e.EntityType, &e.EntityID,
		&e.BeforeData, &e.AfterData, &undone, &e.CreatedAt)
	if err != nil {
		return nil, err
	}
	e.Undone = undone == 1
	return e, nil
}

func ListUndoEntries(database *sql.DB, projectID string, limit int) ([]models.UndoEntry, error) {
	rows, err := database.Query(`
		SELECT id, project_id, operation, entity_type, entity_id, before_data, after_data, undone, created_at
		FROM undo_log
		WHERE project_id = ?
		ORDER BY id DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.UndoEntry
	for rows.Next() {
		var e models.UndoEntry
		var undone int
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.Operation, &e.EntityType, &e.EntityID,
			&e.BeforeData, &e.AfterData, &undone, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Undone = undone == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

func MarkUndone(database *sql.DB, id int64) error {
	_, err := database.Exec(`UPDATE undo_log SET undone = 1 WHERE id = ?`, id)
	return err
}

func MarkRedone(database *sql.DB, id int64) error {
	_, err := database.Exec(`UPDATE undo_log SET undone = 0 WHERE id = ?`, id)
	return err
}

// CountPendingHighPriorityItems returns the number of pending ideas and
// high-priority features (priority >= 8) that are not done.
func CountPendingHighPriorityItems(database *sql.DB) (int, int, error) {
	var ideaCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM idea_queue WHERE status = 'pending'`).Scan(&ideaCount); err != nil {
		return 0, 0, fmt.Errorf("counting pending ideas: %w", err)
	}
	var featureCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM features WHERE priority >= 8 AND status NOT IN ('done')`).Scan(&featureCount); err != nil {
		return 0, 0, fmt.Errorf("counting high-priority features: %w", err)
	}
	return ideaCount, featureCount, nil
}

// --- Notifications ---

// CreateNotification inserts a new notification.
func CreateNotification(database *sql.DB, n *models.Notification) error {
	result, err := database.Exec(
		`INSERT INTO notifications (project_id, recipient, type, message, entity_type, entity_id) VALUES (?, ?, ?, ?, ?, ?)`,
		n.ProjectID, n.Recipient, n.Type, n.Message, n.EntityType, n.EntityID,
	)
	if err != nil {
		return err
	}
	id, _ := result.LastInsertId()
	n.ID = int(id)
	return nil
}

// ListNotifications returns notifications for a project, optionally filtered by recipient.
func ListNotifications(database *sql.DB, projectID, recipient string, unreadOnly bool, limit int) ([]models.Notification, error) {
	query := `SELECT id, project_id, recipient, type, message, entity_type, entity_id, read, created_at FROM notifications WHERE project_id = ?`
	args := []any{projectID}
	if recipient != "" {
		query += ` AND recipient = ?`
		args = append(args, recipient)
	}
	if unreadOnly {
		query += ` AND read = 0`
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := database.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var notifications []models.Notification
	for rows.Next() {
		var n models.Notification
		if err := rows.Scan(&n.ID, &n.ProjectID, &n.Recipient, &n.Type, &n.Message, &n.EntityType, &n.EntityID, &n.Read, &n.CreatedAt); err != nil {
			return nil, err
		}
		notifications = append(notifications, n)
	}
	return notifications, rows.Err()
}

// MarkNotificationRead marks a notification as read.
func MarkNotificationRead(database *sql.DB, id int) error {
	_, err := database.Exec(`UPDATE notifications SET read = 1 WHERE id = ?`, id)
	return err
}

// ClearNotifications marks all notifications as read for a project/recipient.
func ClearNotifications(database *sql.DB, projectID, recipient string) error {
	query := `UPDATE notifications SET read = 1 WHERE project_id = ?`
	args := []any{projectID}
	if recipient != "" {
		query += ` AND recipient = ?`
		args = append(args, recipient)
	}
	_, err := database.Exec(query, args...)
	return err
}

// CountUnreadNotifications returns count of unread notifications.
func CountUnreadNotifications(database *sql.DB, projectID, recipient string) (int, error) {
	query := `SELECT COUNT(*) FROM notifications WHERE project_id = ? AND read = 0`
	args := []any{projectID}
	if recipient != "" {
		query += ` AND recipient = ?`
		args = append(args, recipient)
	}
	var count int
	err := database.QueryRow(query, args...).Scan(&count)
	return count, err
}

// --- Discussion Polls ---

// CreateDiscussionPoll creates a poll in a discussion.
func CreateDiscussionPoll(database *sql.DB, poll *models.DiscussionPoll, options []string) error {
	result, err := database.Exec(
		`INSERT INTO discussion_polls (discussion_id, question, poll_type, created_by) VALUES (?, ?, ?, ?)`,
		poll.DiscussionID, poll.Question, poll.PollType, poll.CreatedBy,
	)
	if err != nil {
		return err
	}
	id, _ := result.LastInsertId()
	poll.ID = int(id)

	for i, opt := range options {
		optResult, oErr := database.Exec(
			`INSERT INTO discussion_poll_options (poll_id, label, sort_order) VALUES (?, ?, ?)`,
			poll.ID, opt, i,
		)
		if oErr != nil {
			return oErr
		}
		optID, _ := optResult.LastInsertId()
		poll.Options = append(poll.Options, models.DiscussionPollOption{
			ID:        int(optID),
			PollID:    poll.ID,
			Label:     opt,
			SortOrder: i,
		})
	}
	return nil
}

// GetDiscussionPoll retrieves a poll with its options and vote counts.
func GetDiscussionPoll(database *sql.DB, pollID int) (*models.DiscussionPoll, error) {
	poll := &models.DiscussionPoll{}
	err := database.QueryRow(
		`SELECT id, discussion_id, question, poll_type, status, created_by, created_at FROM discussion_polls WHERE id = ?`,
		pollID,
	).Scan(&poll.ID, &poll.DiscussionID, &poll.Question, &poll.PollType, &poll.Status, &poll.CreatedBy, &poll.CreatedAt)
	if err != nil {
		return nil, err
	}

	rows, err := database.Query(
		`SELECT o.id, o.poll_id, o.label, o.sort_order, COUNT(v.voter) as votes
		FROM discussion_poll_options o
		LEFT JOIN discussion_poll_votes v ON v.option_id = o.id
		WHERE o.poll_id = ?
		GROUP BY o.id ORDER BY o.sort_order`, pollID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var opt models.DiscussionPollOption
		if err := rows.Scan(&opt.ID, &opt.PollID, &opt.Label, &opt.SortOrder, &opt.Votes); err != nil {
			return nil, err
		}
		poll.Options = append(poll.Options, opt)
	}
	return poll, rows.Err()
}

// ListDiscussionPolls returns all polls for a discussion.
func ListDiscussionPolls(database *sql.DB, discussionID int) ([]models.DiscussionPoll, error) {
	rows, err := database.Query(
		`SELECT id, discussion_id, question, poll_type, status, created_by, created_at FROM discussion_polls WHERE discussion_id = ? ORDER BY created_at`,
		discussionID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var polls []models.DiscussionPoll
	for rows.Next() {
		var p models.DiscussionPoll
		if err := rows.Scan(&p.ID, &p.DiscussionID, &p.Question, &p.PollType, &p.Status, &p.CreatedBy, &p.CreatedAt); err != nil {
			return nil, err
		}
		polls = append(polls, p)
	}
	return polls, rows.Err()
}

// VoteOnPoll records a vote on a poll option.
func VoteOnPoll(database *sql.DB, pollID, optionID int, voter string) error {
	_, err := database.Exec(
		`INSERT OR IGNORE INTO discussion_poll_votes (poll_id, option_id, voter) VALUES (?, ?, ?)`,
		pollID, optionID, voter,
	)
	return err
}

// CloseDiscussionPoll closes a poll.
func CloseDiscussionPoll(database *sql.DB, pollID int) error {
	_, err := database.Exec(`UPDATE discussion_polls SET status = 'closed' WHERE id = ?`, pollID)
	return err
}

// --- API Tokens ---

// CreateAPIToken inserts a new API token record and returns its ID.
func CreateAPIToken(database *sql.DB, projectID, name, tokenHash string, scopes []string, expiresAt string) (int64, error) {
	scopesJSON, _ := json.Marshal(scopes)
	var res sql.Result
	var err error
	if expiresAt != "" {
		res, err = database.Exec(
			`INSERT INTO api_tokens (project_id, name, token_hash, scopes, expires_at) VALUES (?, ?, ?, ?, ?)`,
			projectID, name, tokenHash, string(scopesJSON), expiresAt,
		)
	} else {
		res, err = database.Exec(
			`INSERT INTO api_tokens (project_id, name, token_hash, scopes) VALUES (?, ?, ?, ?)`,
			projectID, name, tokenHash, string(scopesJSON),
		)
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListAPITokens returns all API tokens for a project (including revoked).
func ListAPITokens(database *sql.DB, projectID string) ([]models.APIToken, error) {
	rows, err := database.Query(
		`SELECT id, project_id, name, token_hash, scopes, created_at, COALESCE(expires_at,''), COALESCE(revoked_at,'')
		 FROM api_tokens WHERE project_id = ? ORDER BY created_at DESC`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var tokens []models.APIToken
	for rows.Next() {
		var t models.APIToken
		var scopesJSON string
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Name, &t.TokenHash, &scopesJSON, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(scopesJSON), &t.Scopes)
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// GetAPITokenByHash returns a non-revoked, non-expired token by its hash.
func GetAPITokenByHash(database *sql.DB, tokenHash string) (*models.APIToken, error) {
	row := database.QueryRow(
		`SELECT id, project_id, name, token_hash, scopes, created_at, COALESCE(expires_at,''), COALESCE(revoked_at,'')
		 FROM api_tokens WHERE token_hash = ? AND revoked_at IS NULL
		 AND (expires_at IS NULL OR expires_at > datetime('now'))`, tokenHash,
	)
	var t models.APIToken
	var scopesJSON string
	if err := row.Scan(&t.ID, &t.ProjectID, &t.Name, &t.TokenHash, &scopesJSON, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(scopesJSON), &t.Scopes)
	return &t, nil
}

// RevokeAPIToken marks a token as revoked.
func RevokeAPIToken(database *sql.DB, tokenID int) error {
	res, err := database.Exec(
		`UPDATE api_tokens SET revoked_at = datetime('now') WHERE id = ? AND revoked_at IS NULL`, tokenID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("token not found or already revoked")
	}
	return nil
}

// EventStat holds a count for a single event type.
type EventStat struct {
	EventType string `json:"event_type"`
	Count     int    `json:"count"`
}

// GetEventStats returns event counts grouped by event_type.
func GetEventStats(database *sql.DB, projectID string) ([]EventStat, error) {
	rows, err := database.Query(`
		SELECT event_type, COUNT(*) as cnt
		FROM events
		WHERE project_id = ?
		GROUP BY event_type
		ORDER BY cnt DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []EventStat
	for rows.Next() {
		var s EventStat
		if err := rows.Scan(&s.EventType, &s.Count); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListEventsPaginated returns events with pagination support.
func ListEventsPaginated(database *sql.DB, projectID, featureID, eventType, since, until string, limit, offset int) ([]models.Event, int, error) {
	// Count total
	countQ := `SELECT COUNT(*) FROM events WHERE project_id = ?`
	args := []any{projectID}
	if featureID != "" {
		countQ += " AND feature_id = ?"
		args = append(args, featureID)
	}
	if eventType != "" {
		countQ += " AND event_type = ?"
		args = append(args, eventType)
	}
	if since != "" {
		countQ += " AND created_at >= ?"
		args = append(args, since)
	}
	if until != "" {
		countQ += " AND created_at <= ?"
		args = append(args, until)
	}

	var total int
	if err := database.QueryRow(countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Build the data query using the same filters
	dataQ := `SELECT id, project_id, COALESCE(feature_id,''), event_type, COALESCE(data,''), created_at
		FROM events WHERE project_id = ?`
	dataArgs := []any{projectID}
	if featureID != "" {
		dataQ += " AND feature_id = ?"
		dataArgs = append(dataArgs, featureID)
	}
	if eventType != "" {
		dataQ += " AND event_type = ?"
		dataArgs = append(dataArgs, eventType)
	}
	if since != "" {
		dataQ += " AND created_at >= ?"
		dataArgs = append(dataArgs, since)
	}
	if until != "" {
		dataQ += " AND created_at <= ?"
		dataArgs = append(dataArgs, until)
	}
	dataQ += " ORDER BY created_at DESC"
	if limit > 0 {
		dataQ += fmt.Sprintf(" LIMIT %d", limit)
	}
	if offset > 0 {
		dataQ += fmt.Sprintf(" OFFSET %d", offset)
	}

	rows, err := database.Query(dataQ, dataArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.Event
	for rows.Next() {
		var e models.Event
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.FeatureID, &e.EventType, &e.Data, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// HeatmapGrid holds aggregated counts by day-of-week (0-6) and hour-of-day (0-23).
type HeatmapGrid struct {
	Cells    [168]int `json:"cells"` // 7 days * 24 hours
	MaxCount int      `json:"max_count"`
}

// GetHeatmapGrid returns event counts by hour-of-day and day-of-week.
func GetHeatmapGrid(database *sql.DB, projectID string) (*HeatmapGrid, error) {
	rows, err := database.Query(`
		SELECT CAST(strftime('%w', created_at) AS INTEGER) AS dow,
		       CAST(strftime('%H', created_at) AS INTEGER) AS hour,
		       COUNT(*) AS cnt
		FROM events
		WHERE project_id = ?
		GROUP BY dow, hour`, projectID)
	if err != nil {
		return nil, fmt.Errorf("querying heatmap grid: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	grid := &HeatmapGrid{}
	for rows.Next() {
		var dow, hour, cnt int
		if err := rows.Scan(&dow, &hour, &cnt); err != nil {
			return nil, err
		}
		idx := dow*24 + hour
		if idx >= 0 && idx < 168 {
			grid.Cells[idx] = cnt
			if cnt > grid.MaxCount {
				grid.MaxCount = cnt
			}
		}
	}
	return grid, rows.Err()
}

// --- Workstreams ---

func CreateWorkstream(db *sql.DB, w *models.Workstream) error {
	_, err := db.Exec(
		`INSERT INTO workstreams (id, project_id, parent_id, name, description, status, tags) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.ProjectID, nullStr(w.ParentID), w.Name, w.Description, w.Status, w.Tags,
	)
	return err
}

func GetWorkstream(db *sql.DB, id string) (*models.Workstream, error) {
	row := db.QueryRow(`SELECT id, project_id, COALESCE(parent_id,''), name, description, status, tags, created_at, updated_at FROM workstreams WHERE id = ?`, id)
	w := &models.Workstream{}
	if err := row.Scan(&w.ID, &w.ProjectID, &w.ParentID, &w.Name, &w.Description, &w.Status, &w.Tags, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return nil, err
	}
	return w, nil
}

func ListWorkstreams(db *sql.DB, projectID, status string) ([]models.Workstream, error) {
	query := `SELECT id, project_id, COALESCE(parent_id,''), name, description, status, tags, created_at, updated_at FROM workstreams WHERE 1=1`
	var args []any
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	if status != "" && status != "all" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY updated_at DESC`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []models.Workstream
	for rows.Next() {
		var w models.Workstream
		if err := rows.Scan(&w.ID, &w.ProjectID, &w.ParentID, &w.Name, &w.Description, &w.Status, &w.Tags, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func UpdateWorkstream(db *sql.DB, id string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	setClauses := []string{"updated_at = datetime('now')"}
	var args []any
	for col, val := range updates {
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}
	args = append(args, id)
	_, err := db.Exec(
		fmt.Sprintf(`UPDATE workstreams SET %s WHERE id = ?`, strings.Join(setClauses, ", ")),
		args...,
	)
	return err
}

func ArchiveWorkstream(db *sql.DB, id string) error {
	_, err := db.Exec(`UPDATE workstreams SET status = 'archived', updated_at = datetime('now') WHERE id = ?`, id)
	return err
}

// --- Workstream Notes ---

func CreateWorkstreamNote(db *sql.DB, n *models.WorkstreamNote) error {
	res, err := db.Exec(
		`INSERT INTO workstream_notes (workstream_id, content, note_type, source, resolved) VALUES (?, ?, ?, ?, ?)`,
		n.WorkstreamID, n.Content, n.NoteType, n.Source, n.Resolved,
	)
	if err != nil {
		return err
	}
	if id, idErr := res.LastInsertId(); idErr == nil {
		n.ID = int(id)
	}
	_, _ = db.Exec(`UPDATE workstreams SET updated_at = datetime('now') WHERE id = ?`, n.WorkstreamID)
	return nil
}

func ListWorkstreamNotes(db *sql.DB, workstreamID string) ([]models.WorkstreamNote, error) {
	rows, err := db.Query(
		`SELECT id, workstream_id, content, note_type, source, resolved, created_at FROM workstream_notes WHERE workstream_id = ? ORDER BY created_at DESC`,
		workstreamID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []models.WorkstreamNote
	for rows.Next() {
		var n models.WorkstreamNote
		if err := rows.Scan(&n.ID, &n.WorkstreamID, &n.Content, &n.NoteType, &n.Source, &n.Resolved, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func UpdateWorkstreamNote(db *sql.DB, id int, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	var setClauses []string
	var args []any
	for col, val := range updates {
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}
	args = append(args, id)
	_, err := db.Exec(
		fmt.Sprintf(`UPDATE workstream_notes SET %s WHERE id = ?`, strings.Join(setClauses, ", ")),
		args...,
	)
	return err
}

func DeleteWorkstreamNote(db *sql.DB, id int) error {
	_, err := db.Exec(`DELETE FROM workstream_notes WHERE id = ?`, id)
	return err
}

// --- Workstream Links ---

func CreateWorkstreamLink(db *sql.DB, l *models.WorkstreamLink) error {
	res, err := db.Exec(
		`INSERT INTO workstream_links (workstream_id, link_type, target_id, target_url, label) VALUES (?, ?, ?, ?, ?)`,
		l.WorkstreamID, l.LinkType, l.TargetID, l.TargetURL, l.Label,
	)
	if err != nil {
		return err
	}
	if id, idErr := res.LastInsertId(); idErr == nil {
		l.ID = int(id)
	}
	_, _ = db.Exec(`UPDATE workstreams SET updated_at = datetime('now') WHERE id = ?`, l.WorkstreamID)
	return nil
}

func ListWorkstreamLinks(db *sql.DB, workstreamID string) ([]models.WorkstreamLink, error) {
	rows, err := db.Query(
		`SELECT id, workstream_id, link_type, target_id, target_url, label, created_at FROM workstream_links WHERE workstream_id = ? ORDER BY created_at DESC`,
		workstreamID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []models.WorkstreamLink
	for rows.Next() {
		var l models.WorkstreamLink
		if err := rows.Scan(&l.ID, &l.WorkstreamID, &l.LinkType, &l.TargetID, &l.TargetURL, &l.Label, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func DeleteWorkstreamLink(db *sql.DB, id int) error {
	_, err := db.Exec(`DELETE FROM workstream_links WHERE id = ?`, id)
	return err
}

func GetWorkstreamDetail(db *sql.DB, id string) (*models.WorkstreamDetail, error) {
	w, err := GetWorkstream(db, id)
	if err != nil {
		return nil, err
	}
	notes, err := ListWorkstreamNotes(db, id)
	if err != nil {
		return nil, err
	}
	links, err := ListWorkstreamLinks(db, id)
	if err != nil {
		return nil, err
	}
	children, err := listWorkstreamChildren(db, id)
	if err != nil {
		return nil, err
	}
	if notes == nil {
		notes = []models.WorkstreamNote{}
	}
	if links == nil {
		links = []models.WorkstreamLink{}
	}
	if children == nil {
		children = []models.Workstream{}
	}
	return &models.WorkstreamDetail{
		Workstream: *w,
		Notes:      notes,
		Links:      links,
		Children:   children,
	}, nil
}

func listWorkstreamChildren(db *sql.DB, parentID string) ([]models.Workstream, error) {
	rows, err := db.Query(
		`SELECT id, project_id, COALESCE(parent_id,''), name, description, status, tags, created_at, updated_at FROM workstreams WHERE parent_id = ? ORDER BY created_at`,
		parentID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []models.Workstream
	for rows.Next() {
		var w models.Workstream
		if err := rows.Scan(&w.ID, &w.ProjectID, &w.ParentID, &w.Name, &w.Description, &w.Status, &w.Tags, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
