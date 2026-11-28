package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

func (l *Loop) graphExitReadPath(laneID, runID string) string {
	runPath := filepath.Join(RunDir(l.cfg.Root, runID), graphExitFile)
	if _, err := os.Stat(runPath); err == nil {
		return runPath
	}
	return filepath.Join(run.DurableDir(l.laneDirPath(laneID), runID), graphExitFile)
}

// Persist evidence before any verdict or state mutation. Replaying settlement
// reads the same record; the terminal node is never dispatched again.
func (l *Loop) completeGraphWithoutCommit(ctx context.Context, d Decision, c Candidate) error {
	runPath := filepath.Join(RunDir(l.cfg.Root, c.RunID), graphExitFile)
	scratchPath := filepath.Join(run.DurableDir(l.laneDirPath(d.LaneID), c.RunID), graphExitFile)
	path := l.graphExitReadPath(d.LaneID, c.RunID)
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
		// Write beside the run's log (outside scratch) so collection never loses it,
		// and mirror into scratch for active-run callers. The rename publishes a
		// complete record, never a partially written JSON file.
		for _, dst := range []string{runPath, scratchPath} {
			if err = os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
				return err
			}
			if err = os.WriteFile(dst+".pending", raw, 0644); err != nil {
				return err
			}
			if err = os.Rename(dst+".pending", dst); err != nil {
				return err
			}
		}
		path = runPath
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

func (l *Loop) completeNoChangeExit(ctx context.Context, d Decision, c Candidate) error {
	if c.NoChange == nil {
		return fmt.Errorf("no_change exit requires a finding")
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	at := l.cfg.Now().UTC()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		seen, err := judgmentSeen(tx, "drain.no-change-closed", "item", c.ItemID)
		if err != nil || seen != 0 {
			return err
		}
		closedBy := "run:" + c.NoChange.RunID
		judgeRun := ""
		if receipt := c.Judge; receipt != nil {
			options := receipt.Options
			options.Landed = ""
			options.At = at
			if err := bindSavedJudge(tx.Reader(), c, receipt, &options); err != nil {
				return err
			}
			if _, err := item.RecordJudge(ctx, tx, receipt.Raw, options); err != nil {
				return err
			}
			closedBy = options.Actor
			judgeRun = receipt.Request.RunID
		}
		if err := item.RecordFinding(ctx, tx, c.ItemID, item.Finding{
			Cause:         c.NoChange.Cause,
			Evidence:      c.NoChange.Evidence,
			WhyNoChange:   c.NoChange.WhyNoChange,
			EvaluatedHead: c.BaseSHA,
			WorkerRun:     c.NoChange.RunID,
			JudgeRun:      judgeRun,
		}); err != nil {
			return err
		}
		if _, err := item.Close(ctx, tx, c.ItemID, "no_change: "+c.NoChange.WhyNoChange, closedBy, at); err != nil {
			return err
		}
		var callID string
		err = tx.QueryRow(`SELECT object_id FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=? AND json_extract(payload,'$.tool')='no_change' ORDER BY seq DESC LIMIT 1`,
			callOffered, c.NoChange.RunID).Scan(&callID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if callID != "" {
			answered, err := judgmentSeen(tx, callAnswered, "call", callID)
			if err != nil {
				return err
			}
			closed, err := judgmentSeen(tx, callClosed, "call", callID)
			if err != nil {
				return err
			}
			if answered == 0 && closed == 0 {
				if err := appendLandingCallEvent(tx, callClosed, callID, map[string]string{"reason": "closed-no-change"}, at); err != nil {
					return err
				}
			}
		}
		if err := endItemUnfinishedRunsInTx(ctx, tx, c.ItemID, c.RunID, at); err != nil {
			return err
		}
		return appendEffect(tx, "drain.no-change-closed", c.ItemID, map[string]any{
			"item_id":    c.ItemID,
			"attempt":    d.Attempt,
			"worker_run": c.NoChange.RunID,
			"judge_run":  judgeRun,
			"base_sha":   c.BaseSHA,
		}, at)
	})
}

func needsCompanionPlanSliceHold(d Decision, stage WorkflowStage, log string) (string, bool) {
	if !strings.HasPrefix(d.ItemID, "item-design-") || stage.ID != "plan" {
		return "", false
	}
	obs := parseObstacle(log)
	if obs.Problem != "" || obs.Kind != ObstacleDefinition {
		return "", false
	}
	ev := strings.TrimSpace(obs.Field("EVIDENCE"))
	if !strings.HasPrefix(strings.ToLower(ev), ReasonNeedsCompanionPlanSlice) {
		return "", false
	}
	return ev, true
}

func stampDesignStageBuilt(raw []byte) ([]byte, bool) {
	text := string(raw)
	lines := strings.Split(text, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return raw, false
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
		if strings.HasPrefix(lines[i], "stage:") {
			if strings.TrimSpace(strings.TrimPrefix(lines[i], "stage:")) == "BUILT" {
				return raw, false
			}
			lines[i] = "stage: BUILT"
			return []byte(strings.Join(lines, "\n")), true
		}
	}
	if end < 0 {
		return raw, false
	}
	updated := make([]string, 0, len(lines)+1)
	updated = append(updated, lines[:end]...)
	updated = append(updated, "stage: BUILT")
	updated = append(updated, lines[end:]...)
	return []byte(strings.Join(updated, "\n")), true
}

const stampGateLogExcerptMax = 4096

func stampGateFailingChecks(runDir, runID string, gate GateOutcome) []EndingCheck {
	if checks := gateFailingChecks(runDir, runID); len(checks) > 0 {
		return checks
	}
	byID := map[string]EndingCheck{}
	for _, id := range gate.FailingChecks {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, seen := byID[id]; seen {
			continue
		}
		logRef := gate.CheckLogs[id]
		c := EndingCheck{CheckID: id, LogRef: logRef}
		if logRef != "" {
			if output, err := os.ReadFile(logRef); err == nil {
				c.Digest = evidenceDigest(string(output), runID)
			}
			c.Failing = readTriageBlock(logRef)
		}
		byID[id] = c
	}
	checks := make([]EndingCheck, 0, len(byID))
	for _, c := range byID {
		checks = append(checks, c)
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].CheckID < checks[j].CheckID })
	return checks
}

func stampGateRefusalDetail(docPath, outcome string, checks []EndingCheck) string {
	ids := make([]string, 0, len(checks))
	for _, c := range checks {
		ids = append(ids, c.CheckID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "stamping %s BUILT failed gate (%s): %s", docPath, outcome, strings.Join(ids, ", "))
	budget := stampGateLogExcerptMax
	for _, c := range checks {
		if len(c.Failing) > 0 || c.LogRef == "" || budget <= 0 {
			continue
		}
		raw, err := os.ReadFile(c.LogRef)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			continue
		}
		if len(text) > budget {
			text = strings.TrimSpace(text[:budget]) + "\n...(truncated; see " + c.LogRef + ")"
		}
		fmt.Fprintf(&b, "\n%s output:\n%s", c.CheckID, text)
		budget -= len(text)
	}
	return b.String()
}

func isStampBuiltGateFailureDetail(detail string) bool {
	return strings.Contains(detail, "stamping ") && strings.Contains(detail, " BUILT failed gate (fail): ")
}

func stampGateSettleFaults(r *store.Reader) ([]ItemFault, error) {
	faults, err := ReadItemFaults(r)
	if err != nil {
		return nil, err
	}
	var out []ItemFault
	for _, f := range faults {
		if f.Reason == ReasonSettleFailed && isStampBuiltGateFailureDetail(f.Detail) {
			out = append(out, f)
		}
	}
	return out, nil
}

// releaseStampGateSettleFaults clears settle-failed holds left by the earlier
// BUILT-stamp gate error path so those items re-enter settlement and return
// their recorded document check output to their builder without operator action.
func (l *Loop) releaseStampGateSettleFaults(ctx context.Context) error {
	if l.cfg.Store == "" {
		return nil
	}
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var toClear []ItemFault
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		var err error
		toClear, err = stampGateSettleFaults(tx)
		return err
	})
	r.Close()
	if err != nil || len(toClear) == 0 {
		return err
	}
	st, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer st.Close()
	at := l.cfg.Now().UTC()
	var cleared []ItemFault
	err = st.WriteTx(ctx, func(tx *store.Tx) error {
		current, err := stampGateSettleFaults(tx.Reader())
		if err != nil {
			return err
		}
		for _, f := range current {
			if err := clearItemFault(tx, f, at); err != nil {
				return err
			}
			cleared = append(cleared, f)
		}
		return nil
	})
	if err == nil {
		for _, f := range cleared {
			l.reportf("loop: cleared %s hold on %s: BUILT stamp gate failure now returns to builder\n", f.Reason, f.ItemID)
		}
	}
	return err
}

func (l *Loop) stampMirroredDesignBuiltOnNoChange(ctx context.Context, d Decision, c Candidate, result *LoopResult) (bool, Candidate, error) {
	if !strings.HasPrefix(c.ItemID, "item-design-") || l.nonLanding || c.Worktree == "" {
		return false, Candidate{}, nil
	}
	var designDoc string
	if err := l.readStore(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT COALESCE(design_doc, '') FROM items WHERE item_id = ?`, c.ItemID).Scan(&designDoc)
	}); err != nil {
		return false, Candidate{}, err
	}
	docPath, _, _ := strings.Cut(designDoc, "#")
	docPath = strings.TrimSpace(docPath)
	if docPath == "" {
		return false, Candidate{}, nil
	}
	fullPath := filepath.Join(c.Worktree, filepath.FromSlash(docPath))
	raw, err := os.ReadFile(fullPath)
	if os.IsNotExist(err) {
		return false, Candidate{}, nil
	}
	if err != nil {
		return false, Candidate{}, err
	}
	updated, changed := stampDesignStageBuilt(raw)
	var stampHead string
	if changed {
		if err := os.WriteFile(fullPath, updated, 0o644); err != nil {
			return false, Candidate{}, err
		}
		if _, err := l.git(ctx, c.Worktree, "add", "--", docPath); err != nil {
			return false, Candidate{}, err
		}
		if _, err := l.git(ctx, c.Worktree, "commit", "-m", "docs: mark "+docPath+" built"); err != nil {
			return false, Candidate{}, err
		}
		newHead, err := l.git(ctx, c.Worktree, "rev-parse", "HEAD")
		if err != nil {
			return false, Candidate{}, err
		}
		stampHead = strings.TrimSpace(newHead)
	} else {
		curHead, err := l.git(ctx, c.Worktree, "rev-parse", "HEAD")
		if err != nil {
			return false, Candidate{}, err
		}
		stampHead = strings.TrimSpace(curHead)
		if stampHead == "" || stampHead == c.BaseSHA {
			return false, Candidate{}, nil
		}
	}
	stampCand := Candidate{
		ItemID:   c.ItemID,
		LaneID:   c.LaneID,
		RunID:    c.RunID,
		Worktree: c.Worktree,
		BaseSHA:  c.BaseSHA,
		HeadSHA:  stampHead,
	}
	if !changed {
		if priorChecks := gateFailingChecks(RunDir(l.cfg.Root, stampCand.RunID), stampCand.RunID); len(priorChecks) > 0 {
			stampCand.requiredFailures = priorChecks
			stampCand.RefusalDetail = stampGateRefusalDetail(docPath, GateFailed, priorChecks)
			if err := l.refuse(ctx, stampCand, result, RefusalGate); err != nil {
				return false, Candidate{}, err
			}
			return true, stampCand, nil
		}
	}
	gate, err := l.cfg.Steps.Gate(ctx, stampCand)
	if err != nil {
		return false, Candidate{}, err
	}
	if gate.Outcome == GateFailed {
		checks := stampGateFailingChecks(RunDir(l.cfg.Root, stampCand.RunID), stampCand.RunID, gate)
		stampCand.requiredFailures = checks
		stampCand.RefusalDetail = stampGateRefusalDetail(docPath, gate.Outcome, checks)
		if err := l.refuse(ctx, stampCand, result, RefusalGate); err != nil {
			return false, Candidate{}, err
		}
		return true, stampCand, nil
	}
	if gate.Outcome != GatePassed && gate.Outcome != GatePassedInherited {
		return false, Candidate{}, Externalf("stamping %s BUILT failed gate (%s): %s", docPath, gate.Outcome, strings.Join(gate.FailingChecks, ", "))
	}
	land, err := l.cfg.Steps.Land(ctx, stampCand)
	if err != nil {
		return false, Candidate{}, err
	}
	if land.Result != LandLanded {
		return false, Candidate{}, Externalf("stamping %s BUILT refused to land (%s): %s", docPath, land.Result, land.Reason)
	}
	return false, Candidate{}, nil
}
