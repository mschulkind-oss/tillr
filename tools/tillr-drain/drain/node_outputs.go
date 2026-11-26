package drain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/workflow"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func outputNameValid(name string) bool {
	return workflow.OutputNameValid(name)
}
func outputTypeValid(typ string) bool {
	return workflow.OutputTypeValid(typ)
}
func sortedNames[V any](m map[string]V) []string {
	return workflow.SortedNames(m)
}

func validateOutputWiring(w *Workflow) error {
	return workflow.ValidateOutputWiring(w)
}

func workflowHasNamedFiles(w *Workflow) bool {
	for _, s := range w.Stages {
		if len(s.Inputs) > 0 || len(s.Outputs) > 0 {
			return true
		}
	}
	return false
}

// workflowProposalOnlyPredecessors reports whether stage index declares no explicit
// inputs, no graph.proposal replacement has occurred, and every incoming edge
// comes from a read-only sibling stage whose only declared outputs are optional
// graph.proposal files.
func workflowProposalOnlyPredecessors(w *Workflow, index int) bool {
	_, ok := workflowProposalPredecessorIDs(w, index)
	return ok
}

func workflowProposalPredecessorIDs(w *Workflow, index int) ([]string, bool) {
	if w == nil || w.Revision != 0 || index < 0 || index >= len(w.Stages) {
		return nil, false
	}
	stage := w.Stages[index]
	if len(stage.Inputs) > 0 || stage.CopiesOf != "" {
		return nil, false
	}
	byID := make(map[string]WorkflowStage, len(w.Stages))
	for _, s := range w.Stages {
		byID[s.ID] = s
	}
	var preds []string
	seen := map[string]bool{}
	for _, e := range w.Edges {
		if e.To != stage.ID {
			continue
		}
		if e.FromOutput != "" || e.ToInput != "" {
			return nil, false
		}
		pred, ok := byID[e.From]
		if !ok || pred.Access != "read_only" || pred.Parent != stage.Parent || !hasOnlyProposalOutputs(pred) {
			return nil, false
		}
		if !seen[pred.ID] {
			seen[pred.ID] = true
			preds = append(preds, pred.ID)
		}
	}
	if len(preds) == 0 {
		return nil, false
	}
	return preds, true
}

func hasOnlyProposalOutputs(stage WorkflowStage) bool {
	if len(stage.Outputs) == 0 {
		return false
	}
	for _, typ := range stage.Outputs {
		if typ != "graph.proposal" {
			return false
		}
	}
	return true
}

func (l *Loop) proposalPredecessorReport(ctx context.Context, d Decision) (string, string, error) {
	preds, ok := workflowProposalPredecessorIDs(d.Workflow, d.NodeIndex)
	if !ok {
		return "", "", nil
	}
	var paths, texts []string
	if l.cfg.Store != "" {
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return "", "", err
		}
		defer s.Close()
		var split bool
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			hasNodes, err := tableExists(r, "node_runs")
			if err != nil || !hasNodes {
				return err
			}
			hasRevisions, err := tableExists(r, "graph_revisions")
			if err != nil {
				return err
			}
			hasRuns, err := tableExists(r, "runs")
			if err != nil {
				return err
			}
			for _, predID := range preds {
				source, err := nodeSourceAttempt(r, d.ItemID, d.Attempt, predID)
				if err != nil {
					return err
				}
				var runID string
				err = r.QueryRow(
					`SELECT run_id FROM node_runs WHERE item_id=? AND attempt=? AND node_id=? AND outcome='passed'`,
					d.ItemID, source, predID,
				).Scan(&runID)
				if errors.Is(err, sql.ErrNoRows) && d.ReusedAttempt > 0 && slices.Contains(d.ReusedNodes, predID) {
					source, err = nodeSourceAttempt(r, d.ItemID, d.ReusedAttempt, predID)
					if err != nil {
						return err
					}
					err = r.QueryRow(
						`SELECT run_id FROM node_runs WHERE item_id=? AND attempt=? AND node_id=? AND outcome='passed'`,
						d.ItemID, source, predID,
					).Scan(&runID)
				}
				if errors.Is(err, sql.ErrNoRows) || runID == "" {
					continue
				}
				if err != nil {
					return err
				}
				if hasRevisions {
					var accepted bool
					if err := r.QueryRow(
						`SELECT EXISTS(SELECT 1 FROM graph_revisions WHERE proposing_run=? AND rule='')`,
						runID,
					).Scan(&accepted); err != nil {
						return err
					}
					if accepted {
						split = true
						return nil
					}
				}
				path, text, err := l.readNodeReportForRun(r, hasRuns, runID)
				if err != nil {
					return err
				}
				if path != "" {
					paths = append(paths, path)
				}
				if strings.TrimSpace(text) != "" {
					texts = append(texts, text)
				}
			}
			return nil
		})
		if err != nil {
			return "", "", err
		}
		if split {
			return "", "", nil
		}
	}
	if len(paths) == 0 && d.PreviousReport != "" {
		paths = append(paths, d.PreviousReport)
		if raw, err := os.ReadFile(d.PreviousReport); err == nil {
			if strings.TrimSpace(string(raw)) != "" {
				texts = append(texts, string(raw))
			}
		} else if !os.IsNotExist(err) {
			return "", "", err
		}
	}
	path := ""
	if len(paths) > 0 {
		path = paths[len(paths)-1]
	}
	return path, strings.Join(texts, "\n\n"), nil
}

func (l *Loop) readNodeReportForRun(r *store.Reader, hasRuns bool, runID string) (string, string, error) {
	nodeReport := filepath.Join(RunDir(l.cfg.Root, runID), "node-report.md")
	if raw, err := os.ReadFile(nodeReport); err == nil {
		return nodeReport, string(raw), nil
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	if !hasRuns {
		return "", "", nil
	}
	var refsJSON string
	if err := r.QueryRow(`SELECT artifact_refs FROM runs WHERE run_id=?`, runID).Scan(&refsJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", nil
		}
		return "", "", err
	}
	var refs []run.ArtifactRef
	if json.Unmarshal([]byte(refsJSON), &refs) != nil {
		return "", "", nil
	}
	for _, ref := range refs {
		if ref.Kind == "report" && ref.Ref != "" {
			if raw, err := os.ReadFile(ref.Ref); err == nil {
				return ref.Ref, string(raw), nil
			} else if !os.IsNotExist(err) {
				return "", "", err
			}
		}
	}
	if tail := logTail(refs); strings.TrimSpace(tail) != "" {
		return "", tail, nil
	}
	return "", "", nil
}

func nodeOutputsDir(root, runID string) string {
	return filepath.Join(RunDir(root, runID), "artifacts")
}

// Input paths come from the accepted producer row, not a guessed run id: a
// restarted producer may have a different run id from its original dispatch.
type NodeInput = workflow.NodeInput

func (l *Loop) nodeInputPaths(ctx context.Context, d Decision) (map[string][]NodeInput, error) {
	paths := map[string][]NodeInput{}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		for _, e := range d.Workflow.Edges {
			if e.To != d.Workflow.Stages[d.NodeIndex].ID || e.ToInput == "" {
				continue
			}
			source, err := nodeSourceAttempt(r, d.ItemID, d.Attempt, e.From)
			if err != nil {
				return err
			}
			var path string
			if err := r.QueryRow(`SELECT a.location FROM node_artifacts a JOIN node_runs n ON n.item_id=a.item_id AND n.attempt=a.attempt AND n.node_id=a.node_id AND n.run_id=a.run_id WHERE a.item_id=? AND a.attempt=? AND a.node_id=? AND a.name=? AND n.outcome='passed'`, d.ItemID, source, e.From, e.FromOutput).Scan(&path); err != nil {
				return fmt.Errorf("node-input-missing: %s.%s from %s.%s: %w", e.To, e.ToInput, e.From, e.FromOutput, err)
			}
			paths[e.ToInput] = append(paths[e.ToInput], NodeInput{Producer: e.From, Path: path})
		}
		return nil
	})
	return paths, err
}

type nodeArtifact struct{ Name, Type, SHA256, Location string }

func checkNodeOutputs(root string, d Decision) ([]nodeArtifact, error) {
	stage := d.Workflow.Stages[d.NodeIndex]
	artifacts := []nodeArtifact{}
	dir := nodeOutputsDir(root, d.RunRequest.RunID)
	for _, name := range sortedNames(stage.Outputs) {
		path := filepath.Join(dir, name)
		if stage.Outputs[name] == "graph.proposal" {
			if _, err := os.Lstat(path); os.IsNotExist(err) {
				continue
			}
			if len(stage.MayReplace) == 0 {
				return nil, fmt.Errorf("proposal-not-allowed: %s", stage.ID)
			}
		}
		// No symlinks or special files: an output is the producer's regular file.
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("node-output-missing: %s.%s: output directory is missing or not a directory", stage.ID, name)
		}
		info, err = os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("node-output-missing: %s.%s: file missing or not a regular file", stage.ID, name)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("node-output-missing: %s.%s: %w", stage.ID, name, err)
		}
		if stage.Outputs[name] == "json" {
			if err := uniqueJSON(raw); err != nil {
				err = fmt.Errorf("node-output-missing: %s.%s: invalid json: %w", stage.ID, name, err)
				if name == deliveryOutput && classificationNode(stage) {
					return nil, deliveryParseError{Err: err}
				}
				return nil, err
			}
		}
		artifacts = append(artifacts, nodeArtifact{Name: name, Type: stage.Outputs[name], SHA256: fmt.Sprintf("%x", sha256.Sum256(raw)), Location: path})
	}
	return artifacts, nil
}

// Token traversal keeps object keys at every depth; unmarshaling into a map
// would silently replace the first occurrence of a duplicate key.
func uniqueJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value func() error
	value = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return fmt.Errorf("expected object key")
				}
				if keys[name] {
					return fmt.Errorf("duplicate key %q", name)
				}
				keys[name] = true
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected delimiter %s", delim)
		}
		_, err = decoder.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

// Acceptance and the passed outcome share a transaction. A crash cannot leave
// a passed node whose declared artifacts were never recorded.
func (l *Loop) acceptNodeOutputs(ctx context.Context, d Decision, artifacts []nodeArtifact) error {
	return l.acceptNodeSettlement(ctx, &d, artifacts)
}

func (l *Loop) acceptNodeSettlement(ctx context.Context, decision *Decision, artifacts []nodeArtifact) error {
	return l.acceptNodeSettlementWithDelivery(ctx, decision, artifacts, nil)
}

func (l *Loop) acceptNodeSettlementWithDelivery(ctx context.Context, decision *Decision, artifacts []nodeArtifact, delivery *deliveryDecision) error {
	d := *decision
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, a := range artifacts {
			if _, err := tx.Exec(`INSERT INTO node_artifacts(item_id,attempt,node_id,run_id,name,type,sha256,location) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(item_id,attempt,node_id,name) DO NOTHING`, d.ItemID, d.Attempt, d.Workflow.Stages[d.NodeIndex].ID, d.RunRequest.RunID, a.Name, a.Type, a.SHA256, a.Location); err != nil {
				return err
			}
			var stored nodeArtifact
			var runID string
			if err := tx.QueryRow(`SELECT run_id,name,type,sha256,location FROM node_artifacts WHERE item_id=? AND attempt=? AND node_id=? AND name=?`, d.ItemID, d.Attempt, d.Workflow.Stages[d.NodeIndex].ID, a.Name).Scan(&runID, &stored.Name, &stored.Type, &stored.SHA256, &stored.Location); err != nil {
				return err
			}
			if stored != a || runID != d.RunRequest.RunID {
				return fmt.Errorf("node artifact %s.%s already recorded with different contents", d.Workflow.Stages[d.NodeIndex].ID, a.Name)
			}
		}
		if err := l.settleProposal(tx, &d, artifacts); err != nil {
			return err
		}
		if delivery != nil {
			old, err := readDelivery(tx.Reader(), d.ItemID)
			if err != nil {
				return err
			}
			if old.Kind != "" {
				if old != *delivery {
					return fmt.Errorf("delivery for %s is already recorded", d.ItemID)
				}
			} else if err := appendEffectOn(tx, deliveryDecisionKind, "item", d.ItemID, *delivery, l.cfg.Now().UTC()); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`UPDATE node_runs SET outcome='passed' WHERE run_id=?`, d.RunRequest.RunID)
		if err == nil {
			*decision = d
		}
		return err
	})
}
