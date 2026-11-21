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

// RefusalVerifyNoVerdict is a machinery ending, not a finding about the candidate.
const RefusalVerifyNoVerdict = "verify-no-verdict"

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
	if len(writers) == 0 || c.Evaluator == nil {
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
	allowed := fenceEntries(l.laneFileBody(c.LaneID, ItemFenceFile))
	stage := w.Stages[index]
	session := &WorkerSession{
		LaneDir: l.laneDirPath(c.LaneID), LaneID: c.LaneID,
		BaseSHA: c.BaseSHA, HeadSHA: c.HeadSHA, Attempt: decision.Attempt,
		Workflow: &w,
	}
	if stage.Kind == "agent" && stage.Access == "write" && (stage.Seat == "build" || stage.Seat == "") &&
		(stage.WriteScope.Paths == nil || scopeWithin(fencePaths(scopeForLane(allowed, "lane-1")), fencePaths(scopeForLane(stage.WriteScope.Paths, "lane-1")))) {
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
	insertVerifyWorkStage(&w, evaluator)
	session.NodeIndex = evaluator
	session.NodeBaseSHA = c.HeadSHA
	return session, nil
}

// insertVerifyWorkStage adds the build stage the expanded graph lacks, immediately before
// the evaluator: it runs after every required stage and before the evaluator, and is
// required to pass.
func insertVerifyWorkStage(w *Workflow, evaluator int) {
	var planned []string
	for _, s := range w.Stages[:evaluator] {
		if s.Access == "write" {
			planned = append(planned, fmt.Sprintf("- %s (seat %s): %s", s.ID, s.Seat, strings.TrimSpace(s.Prompt)))
		}
	}
	prompt := "Repair the candidate commit this run starts from against the verifier's findings. The plan divided the work into these pieces:\n" + strings.Join(planned, "\n")
	stage := WorkflowStage{ID: verifyReturnWorkStage, Parent: verifyReturnWorkStage, Kind: "agent", Seat: "build", Access: "write", Prompt: prompt}
	// The scope is inherited from the item's fence, which staging remaps for the lane the
	// run lands on; a literal path list would pin the lane the verifier ran on.
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
		if obstacle.Kind == ObstacleFence {
			return "", false
		}
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
	if filepath.Clean(c.Worktree) == filepath.Clean(l.lanePath(c.LaneID)) {
		return l.pin(ctx, c.RunID, c.HeadSHA)
	}
	return l.pinFrom(ctx, c.Worktree, c.RunID, c.HeadSHA)
}
