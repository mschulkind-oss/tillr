package drain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// examineWorkerEnd consumes the same terminal record the pass would settle,
// after the worker boundary has closed. Keep the run active until the handoff
// answers so a concurrent pass cannot also claim the work. The durable claim
// still arbitrates with other loop processes and changes to the item's inputs.
func (l *Loop) examineWorkerEnd(ctx context.Context, d Decision, watched []string, completion *runCompletion) {
	// Join shutdown only once the worker is done. Cancellation and the
	// shutdown barrier prevent adding an examination after the join starts.
	l.mu.Lock()
	if ctx.Err() != nil {
		l.mu.Unlock()
		return
	}
	l.examinations.Add(1)
	l.mu.Unlock()
	defer l.examinations.Done()
	if stopped, err := l.stopRequested(ctx); stopped || err != nil {
		completion.outcome.err = err
		return
	}
	ctx, unlock, err := l.lockRestartMechanical(ctx)
	if err != nil {
		completion.outcome.err = err
		return
	}
	defer unlock()
	at := l.cfg.Now().UTC()
	p, err := l.planWorkerSettlement(ctx, d, at)
	if err != nil || p == nil {
		completion.outcome.err = err
		return
	}
	timing := &PassTiming{AtMS: at.UnixMilli(), GateBudgetMS: l.gateBudget.Milliseconds()}
	claimed, work, err := l.applyPlanTrunk(ctx, *p, new(string), at, &timing.TickID)
	if err != nil || !claimed {
		completion.outcome.err = err
		return
	}
	d = p.decision
	req := *d.RunRequest
	req.CorrelationID = timing.TickID
	d.RunRequest = &req
	examined := l.examineClaimed(ctx, d, work, at, timing, watched)
	timing.Applied = 1
	timing.DecisionCounts = map[string]int{ActionSettle: 1}
	timing.EndMS = l.cfg.Now().UnixMilli()
	examined.dispatchTiming = completion.timing
	if completion.result != nil {
		mergeExamination(examined.result, completion.result)
	}
	*completion = examined
}

func (l *Loop) planWorkerSettlement(ctx context.Context, d Decision, at time.Time) (*plannedApplication, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var plan *plannedApplication
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		canFinish, err := l.workerMayFinishGraph(r, d)
		if err != nil || !canFinish {
			return err
		}
		available, err := availableRotationLanes(r, []string{d.LaneID})
		if err != nil || len(available) == 0 {
			return err
		}
		declared, err := definition.ForReader(r)
		if err != nil {
			return readFailure(err)
		}
		tick := Pass{
			examineItem: d.ItemID, Now: at.Format(time.RFC3339Nano), AvailableLanes: &available,
			Comments: l.cfg.Comments, WorkflowDefaults: l.cfg.WorkflowDefaults,
			GatesInFlight: l.gateCount(), Build: &l.build,
		}
		if stager, ok := l.cfg.Stage.(*LaneStager); ok {
			tick.BriefTemplate = &stager.Template
		}
		questions := &VantageQuestions{Root: l.cfg.Root, Command: l.cfg.QuestionsCmd, Context: ctx}
		envs, err := planDefinition(r, tick, l.currentPolicy(), questions, declared.Parsed)
		if err != nil {
			return err
		}
		for _, env := range envs {
			var next Decision
			if err := json.Unmarshal(env.Payload, &next); err != nil {
				return err
			}
			if next.Action != ActionSettle || next.WorkflowRelease || next.ItemID != d.ItemID || next.LaneID != d.LaneID || next.RunRequest == nil || next.RunRequest.RunID != d.RunRequest.RunID {
				continue
			}
			folds, err := readVersionFolds(r)
			if err != nil {
				return err
			}
			work := folds.works[d.ItemID]
			if work == nil || work.LaneReleased || work.Decision.LaneID != d.LaneID {
				return nil
			}
			version, err := inputVersionFrom(r, next, folds)
			if err != nil {
				return err
			}
			plan = &plannedApplication{decision: next, version: version, definitionSHA: declared.SHA256, definition: &declared.Parsed}
			break
		}
		return nil
	})
	return plan, err
}

// Intermediate nodes keep the existing completion path. Only take the inline
// examination if accepting this worker (or reader group) could finish its graph.
// Settlement itself still validates its actual outcome, outputs and candidate.
func (l *Loop) workerMayFinishGraph(r *store.Reader, d Decision) (bool, error) {
	if d.Workflow == nil {
		return true, nil
	}
	if quick, err := planTookQuickWay(l.cfg.Root, d); quick || err != nil {
		return quick, err
	}
	if workflowCandidatePolicy(d.Workflow) == "no_commit" {
		return false, nil
	}
	outcomes, err := workflowOutcomes(r, d.ItemID, d.Attempt)
	if err != nil {
		return false, err
	}
	for _, node := range parallelNodes(d) {
		outcomes[d.Workflow.Stages[node.NodeIndex].ID] = "passed"
	}
	return workflowDone(d.Workflow, outcomes), nil
}
