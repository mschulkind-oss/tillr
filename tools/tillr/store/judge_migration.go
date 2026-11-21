package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/event"
)

const archivedJudgeGrants = "store.judge-grants-archived"

func init() {
	if err := event.Register(archivedJudgeGrants, "store"); err != nil {
		panic(err)
	}
}

// migrateJudgeSteps runs under the schema writer, including for programs that
// do not link the item or drain packages. Archive and drop the old table in the
// same transaction as the graph upgrade and schema marker.
func migrateJudgeSteps(ctx context.Context, tx migrationsExec) error {
	exists := func(table string) (bool, error) {
		var n int
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
		return n != 0, err
	}
	if old, err := exists("item_grants"); err != nil {
		return err
	} else if old {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO events(ts, kind, object_type, object_id, payload)
SELECT unixepoch('subsec')*1000, ?, 'store', 'judge-grants',
       json_object('rows', json_group_array(json_object(
         'grant_id', grant_id, 'claim_classes', claim_classes, 'grantor', grantor,
         'issued_at', issued_at, 'expires_at', expires_at)))
FROM (SELECT * FROM item_grants ORDER BY grant_id);`, archivedJudgeGrants); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE item_grants`); err != nil {
			return err
		}
	}
	if hasSelections, err := exists("workflow_selections"); err != nil || !hasSelections {
		return err
	}
	// A writer that does not link drain may be the first to open this store.
	for name, decl := range map[string]string{"judge_graph": "TEXT NOT NULL DEFAULT ''", "judge_attempt": "INTEGER NOT NULL DEFAULT 0"} {
		present, err := columnExists(ctx, tx, "workflow_selections", name)
		if err != nil {
			return err
		}
		if !present {
			if _, err := tx.ExecContext(ctx, "ALTER TABLE workflow_selections ADD COLUMN "+name+" "+decl); err != nil {
				return err
			}
		}
	}
	mode, err := columnExists(ctx, tx, "items", "judge_mode")
	if err != nil {
		return err
	}
	query := `SELECT s.item_id,s.workflow,s.graph,0 FROM workflow_selections s`
	if mode {
		query = `SELECT s.item_id,s.workflow,s.graph,COALESCE(i.judge_mode='agent' AND i.state NOT IN ('closed','landed','resolved-elsewhere'),0)
FROM workflow_selections s LEFT JOIN items i ON i.item_id=s.item_id`
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	type upgrade struct{ id, graph string }
	var upgrades []upgrade
	for rows.Next() {
		var id, workflow, graph string
		var review bool
		if err := rows.Scan(&id, &workflow, &graph, &review); err != nil {
			return err
		}
		updated := upgradeJudgeGraph(workflow, graph, review, true)
		if updated != graph {
			upgrades = append(upgrades, upgrade{id, updated})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, u := range upgrades {
		if _, err := tx.ExecContext(ctx, `UPDATE workflow_selections SET graph=? WHERE item_id=?`, u.graph, u.id); err != nil {
			return err
		}
	}
	if mode {
		nodes, err := exists("node_runs")
		if err != nil {
			return err
		}
		revisions, err := exists("graph_revisions")
		if err != nil {
			return err
		}
		if nodes && revisions {
			return migrateJudgeRevisions(ctx, tx)
		}
	}
	return nil
}

func migrateJudgeRevisions(ctx context.Context, tx migrationsExec) error {
	rows, err := tx.QueryContext(ctx, `SELECT s.item_id,s.workflow,r.after_graph,r.attempt,i.judge_mode='agent'
FROM workflow_selections s JOIN items i USING(item_id)
JOIN graph_revisions r ON r.revision_id=(SELECT max(g.revision_id)
  FROM graph_revisions g WHERE g.item_id=s.item_id AND g.rule=''
    AND g.attempt=(SELECT max(attempt) FROM node_runs n WHERE n.item_id=s.item_id))
WHERE i.state NOT IN ('landed','closed','resolved-elsewhere')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type revision struct {
		id, graph string
		attempt   int
	}
	var revisions []revision
	for rows.Next() {
		var r revision
		var ref string
		var review bool
		if err := rows.Scan(&r.id, &ref, &r.graph, &r.attempt, &review); err != nil {
			return err
		}
		r.graph = upgradeJudgeGraph(ref, r.graph, review, true)
		revisions = append(revisions, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, r := range revisions {
		if _, err := tx.ExecContext(ctx, `UPDATE workflow_selections SET judge_graph=?, judge_attempt=? WHERE item_id=?`, r.graph, r.attempt, r.id); err != nil {
			return err
		}
	}
	return nil
}

// CompatibilityJudgeGraph records an older agent opt-in as a fixed workflow
// step. Drain's legacy review transport carries out this step after graph exit,
// rather than giving the graph runner a second review to dispatch.
func CompatibilityJudgeGraph(workflow, raw string) string {
	return upgradeJudgeGraph(workflow, raw, true, false)
}

func upgradeJudgeGraph(workflow, raw string, review, legacy bool) string {
	type object = map[string]json.RawMessage
	graph, exit := object{}, object{}
	var stages []object
	var slot string
	if raw != "" {
		if json.Unmarshal([]byte(raw), &graph) != nil || graph == nil || json.Unmarshal(graph["stages"], &stages) != nil {
			return raw // Leave malformed definitions for intake to diagnose.
		}
		if v := graph["exit"]; len(v) != 0 && json.Unmarshal(v, &exit) != nil {
			return raw
		}
		if exit == nil {
			exit = object{}
		}
		if v := exit["evaluator_slot"]; len(v) != 0 && json.Unmarshal(v, &slot) != nil {
			return raw
		}
	}
	encode := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			panic(fmt.Sprintf("encoding workflow migration: %v", err))
		}
		return b
	}
	classes := json.RawMessage(`["verification-only"]`)
	if slot != "" {
		if !legacy {
			return raw
		}
		for _, stage := range stages {
			var id string
			if json.Unmarshal(stage["id"], &id) == nil && id == slot && stage["judge_classes"] == nil {
				stage["judge_classes"] = classes
				graph["stages"] = encode(stages)
				return string(encode(graph))
			}
		}
		return raw
	}
	if !review {
		return raw
	}
	ids := make([]string, 0, len(stages))
	for _, stage := range stages {
		var id string
		if json.Unmarshal(stage["id"], &id) != nil || id == "" {
			return raw
		}
		ids = append(ids, id)
	}
	id := "compatibility-review"
	for slices.Contains(ids, id) {
		id += "-review"
	}
	var required []string
	if v := exit["requires_nodes"]; len(v) != 0 && json.Unmarshal(v, &required) != nil {
		return raw
	}
	if len(required) == 0 {
		required = ids
	}
	if raw == "" {
		name, suffix, _ := strings.Cut(workflow, "@v")
		version, err := strconv.Atoi(suffix)
		if err != nil || name == "" || version < 1 {
			return raw
		}
		graph["name"], graph["version"], graph["edges"] = encode(name), encode(version), json.RawMessage(`[]`)
	}
	var edges []object
	if v := graph["edges"]; len(v) != 0 && json.Unmarshal(v, &edges) != nil {
		return raw
	}
	for _, producer := range ids {
		edges = append(edges, object{"from_node": encode(producer), "to_node": encode(id)})
	}
	graph["compatibility_review"] = encode(id)
	stages = append(stages, object{"id": encode(id), "kind": encode("agent"), "seat": encode("review"), "access": encode("read_only"), "judge_classes": classes})
	exit["evaluator_slot"], exit["requires_nodes"] = encode(id), encode(append(required, id))
	graph["exit"], graph["stages"], graph["edges"] = encode(exit), encode(stages), encode(edges)
	return string(encode(graph))
}
