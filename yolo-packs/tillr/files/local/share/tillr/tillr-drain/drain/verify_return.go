package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// A failed verify goes back to the build step, on the candidate the verifier read, with
// the verifier's findings in its brief word for word. The plan step runs again in two
// cases only, and this is the rule that decides (documented in
// config/tillr/workflows/planned-build@v1.json's verify prompt and the tillr-operating
// skill):
//
//  1. the verifier says the plan was wrong: a not-done reason that begins `plan-wrong:`
//     (any case) sets Candidate.VerifyReplan, and the refusal is recorded with Replan;
//  2. the builder says it cannot meet the findings within the plan: it ends blocked with
//     an obstacle `definition` whose EVIDENCE begins `plan-wrong:` or a report line that
//     begins `PLAN-WRONG:` (settleNode, planWrongReport).
//
// Every other not-done finding, including `uncovered:`, goes to the build step.

// PlanWrongPrefix is the lower-case spelling a verifier's reason or a builder's evidence
// starts with to say the plan was wrong; it is compared case-insensitively.
const PlanWrongPrefix = "plan-wrong:"

// recordMissingVerify pins the exact candidate and retains the expanded graph and all
// preceding node runs. A subsequent attempt starts a fresh evaluator, not a builder.
func (l *Loop) recordMissingVerify(ctx context.Context, c Candidate, runID string, preflight bool) error {
	if err := l.pinCandidateHead(ctx, c); err != nil {
		return err
	}
	var decision Decision
	err := l.readStore(ctx, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='run' AND object_id=? ORDER BY seq DESC LIMIT 1`, dispatchStartedKind, c.RunID).Scan(&raw); err != nil {
			return err
		}
		var work pendingWork
		if err := json.Unmarshal(raw, &work); err != nil {
			return err
		}
		decision = work.Decision
		return nil
	})
	if err != nil {
		return err
	}
	s := &WorkerSession{RunID: c.RunID, LaneDir: l.laneDirPath(c.LaneID), LaneID: c.LaneID, BaseSHA: c.BaseSHA, HeadSHA: c.HeadSHA, Workflow: decision.Workflow, NodeIndex: decision.NodeIndex, NodeBaseSHA: c.HeadSHA, Attempt: decision.Attempt}
	p := refusalPayload{Step: RefusalVerifyNoVerdict, ItemID: c.ItemID, LaneID: c.LaneID, Detail: c.RefusalDetail, BaseSHA: c.BaseSHA, HeadSHA: c.HeadSHA, WorkerSession: s}
	p.RefusalCauses = computeRefusalCauses(p)
	return appendJudgmentThen(ctx, l.cfg.Store, RefusalKind, "run", runID, p, l.cfg.Now(), func(tx *store.Tx) error {
		if preflight {
			c.RunID = runID
			return l.holdItemFaultTx(tx, c, ReasonAdapterRefused, c.RefusalDetail)
		}
		return nil
	})
}

// verifyReturnWorkStage is the id of the build stage inserted before the evaluator when the
// plan expanded the work into pieces that no single build stage can repair as a whole.
const verifyReturnWorkStage = "work"

// verifyBuildSession names the build step a verify-not-done refusal returns to, on the
// candidate head the verifier read. It writes the writer's session id into the lane, so the
// planner can resume that session when the harness can, and otherwise starts the same step
// fresh on the candidate. A workflow with no writing stage before the evaluator returns nil.
func (l *Loop) verifyBuildSession(ctx context.Context, c Candidate) (*WorkerSession, error) {
	writers := c.Writers
	if len(writers) == 0 && c.Judge != nil {
		writers = c.Judge.Writers
	}
	if c.Evaluator == nil {
		return nil, nil
	}
	var decision Decision
	found := false
	err := l.readStore(ctx, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='run' AND object_id=? ORDER BY seq DESC LIMIT 1`, dispatchStartedKind, c.RunID).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		var w pendingWork
		if err := json.Unmarshal(raw, &w); err != nil {
			return err
		}
		decision, found = w.Decision, w.Decision.Workflow != nil
		return nil
	})
	if err != nil || !found {
		return nil, err
	}
	if len(writers) == 0 && decision.SplitDonor == nil {
		return nil, nil
	}
	// Work on a copy: the decision's workflow belongs to the evaluator's record.
	raw, err := json.Marshal(decision.Workflow)
	if err != nil {
		return nil, err
	}
	var w Workflow
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	evaluator := -1
	for i, s := range w.Stages {
		if s.ID == evaluatorSlot(&w) {
			evaluator = i
		}
	}
	if evaluator < 0 {
		return nil, nil
	}
	if len(writers) == 0 && decision.SplitDonor != nil {
		writeIdx := -1
		for i := evaluator - 1; i >= 0; i-- {
			s := w.Stages[i]
			if s.Kind == "agent" && s.Access == "write" && (s.Seat == "build" || s.Seat == "") && s.WriteScope.Paths == nil {
				writeIdx = i
				break
			}
		}
		if writeIdx < 0 {
			insertVerifyWorkStage(&w, evaluator)
			writeIdx = evaluator
		}
		evaluatorID := evaluatorSlot(&w)
		w.Stages = append([]WorkflowStage(nil), w.Stages[writeIdx:]...)
		kept := make(map[string]bool, len(w.Stages))
		for _, s := range w.Stages {
			kept[s.ID] = true
		}
		var edges []WorkflowEdge
		for _, e := range w.Edges {
			if kept[e.From] && kept[e.To] {
				edges = append(edges, e)
			}
		}
		w.Edges = edges
		if w.Exit == nil {
			w.Exit = &WorkflowExit{}
		}
		w.Exit.RequiresNodes = []string{w.Stages[0].ID, evaluatorID}
		return &WorkerSession{
			LaneDir: l.laneDirPath(c.LaneID), LaneID: c.LaneID,
			BaseSHA: c.BaseSHA, HeadSHA: c.HeadSHA, Attempt: decision.Attempt,
			NodeIndex: 0, NodeBaseSHA: c.HeadSHA, Workflow: &w,
		}, nil
	}
	writer := writers[len(writers)-1]
	index := -1
	for i, s := range w.Stages {
		if s.ID == writer.NodeID {
			index = i
		}
	}
	if index < 0 || index >= evaluator {
		return nil, nil
	}
	stage := w.Stages[index]
	session := &WorkerSession{
		LaneDir: l.laneDirPath(c.LaneID), LaneID: c.LaneID,
		BaseSHA: c.BaseSHA, HeadSHA: c.HeadSHA, Attempt: decision.Attempt,
		Workflow: &w,
	}
	if stage.Kind == "agent" && stage.Access == "write" && (stage.Seat == "build" || stage.Seat == "") &&
		stage.WriteScope.Paths == nil {
		session.NodeIndex = index
		session.SessionID, session.Backend, session.Route, session.RunID = writer.SessionID, writer.Backend, writer.Route, writer.RunID
		session.NodeBaseSHA = c.HeadSHA
		if writer.SessionID != "" {
			// A call-suspends writer ends by call: its capability goes with the session id, or
			// the resumed run would be staged as a footer run.
			capability, err := stagedEndingCapability(RunDir(l.cfg.Root, writer.RunID))
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(session.LaneDir, SessionIDFile), []byte(writer.SessionID+"\n"), 0644); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(session.LaneDir, endingCapabilityFile), []byte(capability+"\n"), 0644); err != nil {
				return nil, err
			}
		}
		return session, nil
	}
	for i := evaluator - 1; i >= 0; i-- {
		s := w.Stages[i]
		if s.Kind == "agent" && s.Access == "write" && (s.Seat == "build" || s.Seat == "") && s.WriteScope.Paths == nil {
			session.NodeIndex = i
			session.NodeBaseSHA = c.HeadSHA
			return session, nil
		}
	}
	insertVerifyWorkStage(&w, evaluator)
	session.NodeIndex = evaluator
	session.NodeBaseSHA = c.HeadSHA
	return session, nil
}

// insertVerifyWorkStage adds the build stage the expanded graph lacks, immediately before
// the evaluator: it runs after every required stage and before the evaluator, and is
// required to pass.
func insertVerifyWorkStage(w *Workflow, evaluator int) {
	insertRepairWorkStage(w, evaluator, "the verifier's findings")
}

func insertRepairWorkStage(w *Workflow, evaluator int, findings string) {
	var planned []string
	for _, s := range w.Stages[:evaluator] {
		if s.Access == "write" {
			planned = append(planned, fmt.Sprintf("- %s (seat %s): %s", s.ID, s.Seat, strings.TrimSpace(s.Prompt)))
		}
	}
	prompt := "Repair the candidate commit this run starts from against " + findings + ". The plan divided the work into these pieces:\n" + strings.Join(planned, "\n")
	stage := WorkflowStage{ID: verifyReturnWorkStage, Parent: verifyReturnWorkStage, Kind: "agent", Seat: "build", Access: "write", Prompt: prompt}
	// A repair may need to change any piece of the candidate, not just the last writer's scope.
	stage.WriteScope = WorkflowScope{}
	evaluatorID := w.Stages[evaluator].ID
	stages := append([]WorkflowStage(nil), w.Stages[:evaluator]...)
	stages = append(stages, stage)
	w.Stages = append(stages, w.Stages[evaluator:]...)
	required := []string{}
	if w.Exit != nil {
		for _, id := range w.Exit.RequiresNodes {
			if id != evaluatorID {
				required = append(required, id)
			}
		}
	} else {
		for _, s := range w.Stages {
			if s.ID != evaluatorID && s.ID != verifyReturnWorkStage {
				required = append(required, s.ID)
			}
		}
	}
	for _, id := range required {
		if id != verifyReturnWorkStage {
			w.Edges = append(w.Edges, WorkflowEdge{From: id, To: verifyReturnWorkStage})
		}
	}
	w.Edges = append(w.Edges, WorkflowEdge{From: verifyReturnWorkStage, To: evaluatorID})
	if w.Exit != nil {
		w.Exit.RequiresNodes = append(append([]string(nil), required...), verifyReturnWorkStage, evaluatorID)
	}
}

// laneFileBody is a staged lane file's whole text, or "".
func (l *Loop) laneFileBody(lane, name string) string {
	body, err := os.ReadFile(filepath.Join(l.laneDirPath(lane), name))
	if err != nil {
		return ""
	}
	return string(body)
}

// builderPlanWrong reads a blocked write stage's report for the second route back to the
// plan step: the builder cannot meet the findings within the plan. It answers the builder's
// explanation and true when the stage is a write stage of a graph whose first stage is the
// plan, it ended blocked, and its report says so in a definition obstacle whose EVIDENCE
// begins `plan-wrong:` or a line beginning `PLAN-WRONG:`.
func (l *Loop) builderPlanWrong(d Decision, stage WorkflowStage, footer, log string) (string, bool) {
	if footer != "blocked" || stage.Access != "write" || stage.Kind != "agent" || d.Workflow == nil || len(d.Workflow.Stages) == 0 || d.Workflow.Stages[0].Seat != "plan" || d.NodeIndex == 0 {
		return "", false
	}
	obstacle := parseObstacle(log)
	if obstacle.Problem == "" {
		if obstacle.Kind == ObstacleDefinition && strings.HasPrefix(strings.ToLower(obstacle.Field("EVIDENCE")), PlanWrongPrefix) {
			return strings.TrimSpace(log), true
		}
	}
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line)), "PLAN-WRONG:") {
			return strings.TrimSpace(log), true
		}
	}
	return "", false
}

// pinCandidateHead uses the recorded checkout, not the lane's current assignment.
func (l *Loop) pinCandidateHead(ctx context.Context, c Candidate) error {
	repo := l.cfg.Root
	if c.Worktree != "" && filepath.Clean(c.Worktree) != filepath.Clean(l.lanePath(c.LaneID)) {
		repo = c.Worktree
	}
	if err := writeRunPin(ctx, l.git, repo, c.RunID, c.HeadSHA, false); err != nil {
		return err
	}
	return writeItemPin(ctx, l.git, repo, c.ItemID, c.HeadSHA)
}

// A piece is one writing node the plan step cut, with a write_scope of its own that is
// narrower than its item's. A piece that changes a path outside its own write_scope but
// inside its item's is not a boundary question for the operator: either the worker reached
// past its piece, or the plan cut the piece too narrowly. Both are put right automatically
// (owner's standing direction of 2026-11-26), and this is the rule that tells them apart:
//
//  1. the first such escape of a piece goes back to the builder: the same piece runs again
//     from its own start, with the escaped paths named, and is told to finish inside its
//     write_scope or, if it cannot, to end blocked with `plan-wrong:` evidence naming them;
//  2. the plan step runs again as a wrong plan (the existing RefusalPlanWrong route, with
//     the escaped paths named) when that returned piece says it cannot finish without the
//     paths (builderPlanWrong) or escapes its write_scope a second time.
//
// A path outside the item's own scope, a deny-list entry, a dirty candidate and every other
// boundary refusal are refused and held as boundary-scope-escape, as before.

// pieceScopeEscape is a piece-scope fence refusal's return: the piece's session to run
// again and the paths it changed outside its write_scope.
type pieceScopeEscape struct {
	session *WorkerSession
	paths   []string
}

// pieceScopeReturn reports whether an ending sends the item back to one piece after that
// piece wrote outside its own write_scope but inside its item's.
func pieceScopeReturn(e *ReturnedEnding) bool {
	return e != nil && e.Cause == RefusalFence && len(e.ScopeEscapes) > 0 && e.WorkerSession != nil && e.WorkerSession.Workflow != nil &&
		e.WorkerSession.NodeIndex >= 0 && e.WorkerSession.NodeIndex < len(e.WorkerSession.Workflow.Stages)
}

// pieceScopeReturnTo reports whether d is the returned run of stage after a piece-scope
// escape. A later node dispatched under the same decision carries the same ending, so the
// stage is compared by id.
func pieceScopeReturnTo(d Decision, stage WorkflowStage) bool {
	return pieceScopeReturn(d.Returned) && d.Returned.WorkerSession.Workflow.Stages[d.Returned.WorkerSession.NodeIndex].ID == stage.ID
}

// planStepReachable reports whether a wrong plan can go back to a plan step: the graph's
// first stage is the plan and this node is not it.
func planStepReachable(d Decision) bool {
	return d.Workflow != nil && len(d.Workflow.Stages) > 0 && d.Workflow.Stages[0].Seat == "plan" && d.NodeIndex > 0
}

// insideItemScope reports whether every path is inside the lane's whole-item scope file:
// the fork checkout for a fork item, and no entries for a repository item, whose scope is
// the whole repository less the common deny-list (checked before any scope comparison, so a
// deny-listed path never reaches here). A scope file that cannot be found answers false,
// which keeps the operator hold.
func (l *Loop) insideItemScope(lane string, paths []string) (bool, error) {
	fencePath, err := l.itemFencePath(lane)
	if err != nil {
		return false, err
	}
	body, err := os.ReadFile(fencePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	fence := fenceEntries(string(body))
	if len(fence) == 0 {
		return true, nil
	}
	for _, p := range paths {
		if !insideFence(p, fence) {
			return false, nil
		}
	}
	return true, nil
}

// pieceScopeDetail is the refusal's words for a piece-scope escape: the piece, every
// escaped path, and what the next run is to do.
func pieceScopeDetail(stage WorkflowStage, scopeFile string, paths []string, replan bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: piece %s changed paths outside its own write_scope (the scope file is %s) but inside its item's scope:\n", ScopeEscape, stage.ID, scopeFile)
	for _, p := range paths {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	fmt.Fprintf(&b, "Its write_scope is: %s\n", strings.Join(stage.WriteScope.Paths, ", "))
	if replan {
		fmt.Fprintf(&b, "%s the piece was returned once to finish inside its write_scope and could not; revise the plan so a piece's write_scope holds the paths above.", PlanWrongPrefix)
	} else {
		b.WriteString("Redo the piece inside its write_scope. If it cannot be finished without the paths above, end with RESULT: blocked, OBSTACLE: definition, CRITERIA: plan, and EVIDENCE: plan-wrong: <the paths and why the piece needs them>.")
	}
	return b.String()
}
