package main

import (
	"path/filepath"

	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr/store"
)

type laneNode struct {
	NodeID  string `json:"node_id"`
	RunID   string `json:"run_id"`
	Attempt int    `json:"attempt"`
	State   string `json:"state"`
}

// rowid preserves the order the drain recorded, not lexical node-id order.
func laneNodes(r *store.Reader, itemID string) ([]laneNode, error) {
	return laneNodesIn(r, itemID, "")
}

func laneNodesIn(r *store.Reader, itemID, loopDir string) ([]laneNode, error) {
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
		storePath := readerStorePath(r)
		dead, err := presence.DeadRuns(r, storePath, defaultLoopDir(storePath, loopDir))
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

func enrichLaneNodes(r *store.Reader, rows []laneRow, loop string, stats *laneReadStats) error {
	for i := range rows {
		row := &rows[i]
		nodes, err := laneNodesIn(r, row.ItemID, loop)
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
		current := []laneNode{}
		for _, n := range nodes {
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
	return nil
}
