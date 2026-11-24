package main

import (
	"path/filepath"

	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr/store"
)

type laneNode struct {
	NodeID  string `json:"node_id"`
	RunID   string `json:"run_id"`
	Seat    string `json:"seat,omitempty"`
	Attempt int    `json:"attempt"`
	State   string `json:"state"`
}

// rowid preserves the order the drain recorded, not lexical node-id order.
// deadRunsOf is presence.DeadRuns, a variable so a test can count the replays.
var deadRunsOf = presence.DeadRuns

func laneNodes(r *store.Reader, itemID string) ([]laneNode, error) {
	return laneNodesIn(r, itemID, "")
}

func laneNodesIn(r *store.Reader, itemID, loopDir string) ([]laneNode, error) {
	return laneNodesWith(r, itemID, deadRunsReader(r, loopDir))
}

// deadRunsReader answers the dead runs of one store snapshot, read once on first use: the
// answer replays the drain's event history, and one lanes answer needs it from the lane
// reader and from every row's node reader.
func deadRunsReader(r *store.Reader, loopDir string) func() (map[string]bool, error) {
	var dead map[string]bool
	var err error
	done := false
	return func() (map[string]bool, error) {
		if !done {
			storePath := readerStorePath(r)
			dead, err = deadRunsOf(r, storePath, defaultLoopDir(storePath, loopDir))
			done = true
		}
		return dead, err
	}
}

func laneNodesWith(r *store.Reader, itemID string, deadRuns func() (map[string]bool, error)) ([]laneNode, error) {
	rows, err := r.Query(`SELECT node_id, run_id, attempt, outcome FROM node_runs WHERE item_id=? ORDER BY rowid`, itemID)
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	nodes := []laneNode{}
	hasRunning := false
	for rows.Next() {
		var n laneNode
		if err := rows.Scan(&n.NodeID, &n.RunID, &n.Attempt, &n.State); err != nil {
			return nil, err
		}
		switch n.State {
		case "running":
			hasRunning = true
		case "skipped":
		case "passed", "handled-fail":
			n.State = "passed"
		default:
			n.State = "failed"
		}
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if hasRunning {
		dead, err := deadRuns()
		if err != nil && !isMissingTable(err) {
			return nil, err
		}
		for i := range nodes {
			if nodes[i].State == "running" && dead[nodes[i].RunID] {
				nodes[i].State = "failed"
			}
		}
	}
	return nodes, nil
}

func enrichLaneNodes(r *store.Reader, rows []laneRow, loop string, deadRuns func() (map[string]bool, error), stats *laneReadStats) error {
	if err := enrichLaneNodeRows(r, rows, deadRuns); err != nil {
		return err
	}
	applyLaneNodePipelines(rows, loop, stats)
	return nil
}

func enrichLaneNodeRows(r *store.Reader, rows []laneRow, deadRuns func() (map[string]bool, error)) error {
	for i := range rows {
		row := &rows[i]
		nodes, err := laneNodesWith(r, row.ItemID, deadRuns)
		if err != nil {
			return err
		}
		row.Nodes = nodes
		if len(nodes) == 0 {
			continue
		}
		row.Attempt = 0
		for _, n := range nodes {
			if n.Attempt > row.Attempt {
				row.Attempt = n.Attempt
			}
		}
		// A newer attempt on another lane must not move this page's subject.
		for _, n := range nodes {
			if n.RunID == row.RunID {
				row.Attempt = n.Attempt
				break
			}
		}
	}
	return nil
}

func applyLaneNodePipelines(rows []laneRow, loop string, stats *laneReadStats) {
	for i := range rows {
		row := &rows[i]
		if len(row.Nodes) == 0 {
			continue
		}
		current := []laneNode{}
		for _, n := range row.Nodes {
			if n.Attempt == row.Attempt {
				current = append(current, n)
			}
		}
		combined := []pipelineStep{}
		var graph *pipelineStep
		for _, n := range current {
			steps, _ := readLanePipeline(filepath.Join(loop, n.RunID, "pipeline.jsonl"), stats)
			for _, step := range steps {
				if step.Step == "graph" {
					if graph == nil {
						copy := step
						graph = &copy
					} else {
						if step.StartedAtMS < graph.StartedAtMS {
							graph.StartedAtMS = step.StartedAtMS
						}
						graph.FinishedAtMS = step.FinishedAtMS
					}
				} else if step.Step == "stage" {
					if len(combined) == 0 {
						combined = append(combined, step)
					}
				} else {
					combined = append(combined, step)
				}
			}
		}
		if graph != nil {
			combined = append(combined, *graph)
		}
		row.Pipeline = combined
		if len(combined) > 0 {
			row.PipelineNote = ""
		}
	}
}
