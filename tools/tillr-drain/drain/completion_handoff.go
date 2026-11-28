package drain

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// markStepActive requires mu so callers can register cancellation atomically.
func (l *Loop) markStepActive(itemID, runID string) {
	if l.active == nil {
		l.active = map[string]bool{}
	}
	l.active[runID] = true
	if l.activeItems == nil {
		l.activeItems = map[string]string{}
	}
	l.activeItems[itemID] = runID
}

func (l *Loop) planStillActive(p plannedApplication) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := p.decision
	// A parallel group can replace its primary run before its shared boundary
	// closes. Its item remains active even though the current pending ID changed.
	return l.activeItems[d.ItemID] != "" || l.stageLaunch.HasItem(d.ItemID) || l.examiningItems[d.ItemID] > 0 || l.gateLand.HasItem(d.ItemID) ||
		l.active[p.parentRunID] || l.stageLaunch.HasRun(p.parentRunID) || l.examiningRuns[p.parentRunID] || l.gateLand.HasRun(p.parentRunID) ||
		((d.Action == ActionSettle || d.Action == ActionJudge) && d.RunRequest != nil && (l.active[d.RunRequest.RunID] || l.stageLaunch.HasRun(d.RunRequest.RunID) || l.examiningRuns[d.RunRequest.RunID] || l.gateLand.HasRun(d.RunRequest.RunID)))
}

// The pass may stop consuming notifications before shutdown joins examinations.
// Their durable outcomes belong to the successor; they must not block this loop's exit.
func (l *Loop) sendCompletion(ctx context.Context, c runCompletion) {
	select {
	case l.completions <- c:
	case <-ctx.Done():
	}
}

func (l *Loop) drainCompletions(ctx context.Context, result *LoopResult) (string, error) {
	_, stop, err := l.drainCompletionsCount(ctx, result)
	return stop, err
}

func (l *Loop) drainCompletionsCount(ctx context.Context, result *LoopResult) (int, string, error) {
	// Drain only the completions already queued at entry so newly arriving
	// completions wait for the next pass instead of postponing planning.
	limit := len(l.completions)
	if limit == 0 {
		select {
		case c := <-l.completions:
			if stop, err := l.finishRunStep(ctx, c, result); stop != "" || err != nil {
				return 1, stop, err
			}
			if l.cfg.ItemWorktrees && !l.nonLanding && !l.pollOnly {
				if _, colErr := l.collectIdleWorktrees(ctx); colErr != nil {
					l.reportf("loop: idle worktree collection after node end failed: %v\n", colErr)
				}
			}
			return 1, "", nil
		default:
			return 0, "", nil
		}
	}
	drained := 0
	for drained < limit {
		select {
		case c := <-l.completions:
			drained++
			if stop, err := l.finishRunStep(ctx, c, result); stop != "" || err != nil {
				return drained, stop, err
			}
		default:
			goto done
		}
	}
done:
	if drained > 0 && l.cfg.ItemWorktrees && !l.nonLanding && !l.pollOnly {
		if _, colErr := l.collectIdleWorktrees(ctx); colErr != nil {
			l.reportf("loop: idle worktree collection after node end failed: %v\n", colErr)
		}
	}
	return drained, "", nil
}

// finishRunStep lets a completion advance its own retained reservation. The
// handoff cannot offer a fresh lane: readPassPlans binds it to unfinished work
// on the same lane. Fresh starts belong to the coordinator; the pass still reaps
// and does queue-wide maintenance.
func (l *Loop) finishRunStep(ctx context.Context, c runCompletion, result *LoopResult) (stop string, err error) {
	if stop, err = l.takeCompletion(c, result); stop != "" || err != nil {
		return stop, err
	}
	if l.pollOnly || c.itemID == "" || c.outcome.err != nil || c.outcome.stop != "" || c.laneRefusal != nil {
		return "", nil
	}
	if err := l.checkStopRequested(ctx); err != nil {
		return "", err
	}
	if err := l.refreshPolicy(); err != nil {
		return "", err
	}
	var mainSHA string
	if !l.nonLanding {
		mainSHA, err = l.revParse(ctx, l.cfg.Root, l.trunkRef())
		if err != nil {
			return "", err
		}
	}
	at := l.cfg.Now().UTC()
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", err
	}
	var plans []plannedApplication
	var watched []string
	epochs := l.idleLaneEpochs()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		if err := l.readPassPlans(ctx, r, at, c.itemID, nil, &plans, nil, nil); err != nil || len(plans) == 0 {
			return err
		}
		busy, err := readLanes(r, at)
		if err != nil {
			return err
		}
		works, err := readPending(r)
		if err != nil {
			return err
		}
		for _, work := range works {
			if !work.Done && !work.LaneReleased {
				busy[work.Decision.LaneID] = true
			}
		}
		for _, lane := range l.lanes() {
			if busy[lane] {
				continue
			}
			if l.hasLaneCondition(lane, "") {
				if _, err := os.Stat(l.lanePath(lane)); os.IsNotExist(err) {
					continue
				}
			}
			watched = append(watched, lane)
		}
		return nil
	})
	s.Close()
	if err != nil || len(plans) == 0 {
		return "", err
	}
	p := plans[0]
	d := p.decision
	if (!isDispatch(d) && d.Action != ActionSettle && d.Action != ActionJudge) || l.planStillActive(p) {
		return "", nil
	}
	if d.Action == ActionJudge && l.coordinatorOwnsJudge(p.work) {
		return "", nil
	}
	if p.checksComments() && item.IsCommentItem(d.ItemID) && isDispatch(d) {
		ready, _, err := l.checkCommentDispatch(ctx, p, at)
		if err != nil || !ready {
			return "", err
		}
	}
	answers, counts, err := passAnswers(plans)
	if err != nil {
		return "", err
	}
	timing := &PassTiming{AtMS: at.UnixMilli(), GateBudgetMS: l.gateBudget.Milliseconds(), DecisionCounts: counts}
	_, _, err = l.applyReadyPlan(ctx, p, &mainSHA, at, timing, &laneWatch{watched, epochs}, result)
	if timing.TickID != "" {
		l.countGeneration(result, timing.TickID)
		if recordErr := l.observePass(timing, answers); err == nil {
			err = recordErr
		}
	}
	return "", err
}

// Item handoffs can allocate a generation inside a pass that already has one.
// Their observations need not be published in generation order.
func (l *Loop) countGeneration(result *LoopResult, tickID string) {
	if tickID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	result.Ticks++
	id := strings.TrimPrefix(tickID, "gen-")
	n, _ := strconv.ParseInt(id, 10, 64)
	first, _ := strconv.ParseInt(result.FirstGeneration, 10, 64)
	last, _ := strconv.ParseInt(result.LastGeneration, 10, 64)
	if result.FirstGeneration == "" || n < first {
		result.FirstGeneration = id
	}
	if result.LastGeneration == "" || n > last {
		result.LastGeneration = id
	}
}
