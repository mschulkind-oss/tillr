package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

const graphExitFile = "graph-exit.json"

type graphExitNode struct {
	NodeID  string `json:"node_id"`
	RunID   string `json:"run_id"`
	Outcome string `json:"outcome"`
}
type graphExitRecord struct {
	ItemID         string          `json:"item_id"`
	Workflow       string          `json:"workflow"`
	WorkflowSHA256 string          `json:"workflow_sha256"`
	Attempt        int             `json:"attempt"`
	BaseSHA        string          `json:"base_sha"`
	Nodes          []graphExitNode `json:"nodes"`
	Landed         bool            `json:"landed"`
	Reason         string          `json:"reason"`
}

// Persist evidence before any verdict or state mutation. Replaying settlement
// reads the same record; the terminal node is never dispatched again.
func (l *Loop) completeGraphWithoutCommit(ctx context.Context, d Decision, c Candidate) error {
	path := filepath.Join(run.DurableDir(l.laneDirPath(d.LaneID), c.RunID), graphExitFile)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		s, openErr := store.Open(ctx, l.cfg.Store, false)
		if openErr != nil {
			return openErr
		}
		exit := graphExitRecord{ItemID: d.ItemID, Workflow: d.Workflow.Name + fmt.Sprintf("@v%d", d.Workflow.Version), WorkflowSHA256: d.Workflow.SHA256, Attempt: d.Attempt, BaseSHA: c.BaseSHA, Reason: "no commit by design"}
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			rows, err := effectiveNodeRuns(r, d.ItemID, d.Attempt)
			if err != nil {
				return err
			}
			for _, n := range rows {
				exit.Nodes = append(exit.Nodes, graphExitNode{NodeID: n.NodeID, RunID: n.RunID, Outcome: n.Outcome})
			}
			return nil
		})
		s.Close()
		if err != nil {
			return err
		}
		raw, err = json.MarshalIndent(exit, "", "  ")
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		// The rename publishes a complete record, never a partially written JSON file.
		if err = os.WriteFile(path+".pending", raw, 0644); err != nil {
			return err
		}
		if err = os.Rename(path+".pending", path); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	var exit graphExitRecord
	if err = json.Unmarshal(raw, &exit); err != nil {
		return fmt.Errorf("reading graph exit %s: %w", path, err)
	}
	if exit.ItemID != d.ItemID || exit.Workflow != d.Workflow.Name+fmt.Sprintf("@v%d", d.Workflow.Version) || exit.Attempt != d.Attempt || exit.BaseSHA != c.BaseSHA || c.HeadSHA != c.BaseSHA || workflowCandidatePolicy(d.Workflow) == "commit_required" || exit.WorkflowSHA256 != d.Workflow.SHA256 || exit.Landed || exit.Reason != "no commit by design" {
		return fmt.Errorf("graph exit record does not bind this attempt: %s", path)
	}
	evidence := []string{path}
	for _, n := range exit.Nodes {
		if n.Outcome != "skipped" {
			evidence = append(evidence, "run:"+n.RunID)
		}
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		// The worker can write its scratch directory. An existing file must
		// bind the store's settled graph, never authorize unfinished work.
		outcomes, err := workflowOutcomes(tx.Reader(), d.ItemID, d.Attempt)
		if err != nil {
			return err
		}
		if !workflowDone(d.Workflow, outcomes) || len(exit.Nodes) != len(d.Workflow.Stages) {
			return fmt.Errorf("graph exit does not name a completed graph: %s", path)
		}
		seen := map[string]bool{}
		for _, n := range exit.Nodes {
			source, err := nodeSourceAttempt(tx.Reader(), d.ItemID, d.Attempt, n.NodeID)
			if err != nil {
				return err
			}
			var runID string
			if err := tx.QueryRow(`SELECT run_id FROM node_runs WHERE item_id=? AND attempt=? AND node_id=?`, d.ItemID, source, n.NodeID).Scan(&runID); err != nil {
				return err
			}
			if seen[n.NodeID] || outcomes[n.NodeID] != n.Outcome || runID != n.RunID {
				return fmt.Errorf("graph exit node differs from store: %s", n.NodeID)
			}
			seen[n.NodeID] = true
		}
		var settled bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind='drain.graph-completed' AND object_id=?)`, c.RunID).Scan(&settled); err != nil {
			return err
		}
		if settled {
			return nil
		}
		rows, err := tx.Query(`SELECT criterion_id,text FROM item_criteria WHERE item_id=? AND claim_class=? ORDER BY criterion_id`, d.ItemID, item.ClaimVerification)
		if err != nil {
			return err
		}
		type criterion struct{ id, text string }
		var criteria []criterion
		for rows.Next() {
			var cr criterion
			if err := rows.Scan(&cr.id, &cr.text); err != nil {
				rows.Close()
				return err
			}
			criteria = append(criteria, cr)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, cr := range criteria {
			payload, err := json.Marshal(map[string]any{"item_id": d.ItemID, "criterion_id": cr.id, "criterion_hash": item.CriterionHash(cr.text), "verdict": item.VerdictDone, "claim_class": item.ClaimVerification, "reason": exit.Reason, "evidence": evidence, "evaluated_head": exit.BaseSHA, "actor_kind": item.ActorMechanical, "actor": "drain/graph-exit"})
			if err != nil {
				return err
			}
			if _, err = item.RecordVerdict(ctx, tx, record.Envelope{RecordVersion: record.SupportedVersion, Kind: item.VerdictRecordKind, ID: c.RunID + "-exit-" + cr.id, Producer: record.Producer{System: record.System, Utility: Utility, Version: Version}, CreatedAt: l.cfg.Now(), Payload: payload}); err != nil {
				return err
			}
		}
		return appendEffect(tx, "drain.graph-completed", c.RunID, map[string]any{"item_id": d.ItemID, "attempt": d.Attempt, "landed": false, "reason": exit.Reason, "record": path}, l.cfg.Now().UTC())
	})
	if err != nil {
		return err
	}
	if err := l.cfg.Steps.ItemState(ctx, c.ItemID, c.RunID, StateLanded); err != nil {
		return err
	}
	return l.endItemUnfinishedRuns(ctx, c.ItemID, c.RunID)
}
