package drain

import (
	"context"
	"database/sql"
	"maps"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

type startViewEntry struct {
	decision Decision
	// An unassigned entry is versioned without a lane. A proposal has no claim authority.
	version string
	express bool
}

// Capture loop-owned configuration on the loop goroutine; the watcher must not
// read its mutable holds, questions or rotation maps.
func (l *Loop) startViewPass() Pass {
	available := append([]string(nil), l.cfg.Lanes...)
	build := l.build
	tick := Pass{Comments: l.cfg.Comments, WorkflowDefaults: l.cfg.WorkflowDefaults,
		Build: &build, AvailableLanes: &available, StorePath: l.cfg.Store,
		ownerMigration: l.ownerMigrationItem != "", examineItem: l.ownerMigrationItem}
	if stager, ok := l.cfg.Stage.(*LaneStager); ok {
		template := stager.Template
		tick.BriefTemplate = &template
	}
	return tick
}

func readProposedStarts(ctx context.Context, path string, at time.Time, policy *Policy, tick Pass, questions *VantageQuestions, previous startSnapshot) (startSnapshot, error) {
	tick.Now = at.Format(time.RFC3339Nano)
	return readStartSnapshotWith(ctx, path, at, policy, func(r *store.Reader, s *startSnapshot) error {
		return s.rebuildEntries(ctx, r, at, policy, tick, questions, previous)
	})
}

// Use recorded refusals at the same read boundary as leases. Local-only loop
// state is not observed until the loop starts publishing its own notes.
func shadowRotation(r *store.Reader, tick *Pass) error {
	lanes := make([]string, 0, len(*tick.AvailableLanes))
	tick.OutLanes = map[string]bool{}
	for _, lane := range *tick.AvailableLanes {
		var kind string
		err := r.QueryRow(`SELECT kind FROM events WHERE object_type='lane' AND object_id=? AND kind IN (?, ?) ORDER BY seq DESC LIMIT 1`, lane, laneRefusedKind, laneRestoredKind).Scan(&kind)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if kind == laneRefusedKind {
			tick.OutLanes[lane] = true
		} else {
			lanes = append(lanes, lane)
		}
	}
	tick.AvailableLanes = &lanes
	return nil
}

func (s *startSnapshot) buildPlanner(r *store.Reader, at time.Time, policy *Policy, tick Pass, questions *VantageQuestions, rows []itemRow, inputs map[string]*RunInputs, unproved map[string]bool) (*planner, error) {
	lanes, owners := maps.Clone(s.storeLanes), maps.Clone(s.deskOwners)
	for _, work := range s.pending.works {
		if !work.Done && !work.LaneReleased {
			lanes[work.Decision.LaneID] = true
			owners[work.Decision.LaneID] = work.Decision.ItemID
		}
	}
	waits, commentHolds := map[string]item.WaitingOn{}, map[string]string{}
	for _, row := range rows {
		if row.State == s.def.Blocked {
			wait, _, err := item.ReadWaitingOn(r, row.ItemID)
			if err != nil {
				return nil, err
			}
			if s.conflicts[row.ItemID].HasIndependentWait(wait) {
				waits[row.ItemID] = *wait
			}
		}
		if hasPrerequisite(s.def, definition.PrerequisiteDesignQuestions) && item.IsCommentItem(row.ItemID) && !finishedState(s.def, row.State) {
			commentHolds[plannerDocumentKey(questions.Root, row.DesignDoc)] = row.ItemID
		}
	}
	obstacleHolds, releasedObstacles, err := ReadObstacleHolds(r)
	if err != nil {
		return nil, err
	}
	classes, latest := indexPriorRuns(s.priors)
	p := &planner{
		definition: &s.def, commentHolds: commentHolds, documentRoot: questions.Root,
		conflicts: s.conflicts, shut: s.shut, quotaShut: s.quotaShut, forks: s.forks,
		tick: tick, policy: policy, held: lanes, deskOwners: owners, itemStates: s.states, history: s.history,
		priors: s.attemptPriors, retries: s.retries, classes: classes, latest: latest,
		obstacleHolds: obstacleHolds, releasedObstacles: releasedObstacles, questions: questions,
		waits: waits, escalated: s.escalated, at: at, fixerOpen: trunkFixerOpen(rows, s.def), unproved: unproved,
		designHistory: s.pending.byRun, inputs: inputs, inputDecisions: s.inputDecisions,
		works: s.pending.works, attempts: s.attempts, nodeCounts: s.nodeCounts, requests: s.requests,
	}
	p.reserveWorkerLanes(rows, nil)
	return p, nil
}

func (s *startSnapshot) rebuildEntries(ctx context.Context, r *store.Reader, at time.Time, policy *Policy, tick Pass, questions *VantageQuestions, previous startSnapshot) error {
	root := questions.Root
	unshared, rewriteHolds, rewriteOwnerHolds := map[string]bool{}, map[string]string{}, map[string]string{}
	if s.def.Completes == "landing" && root != "" {
		var err error
		s.tip, err = heavyGitOutput(ctx, root, "rev-parse", TrunkRef)
		if err != nil {
			return err
		}
		gatePolicy, _, err := ReadGatePolicy(root)
		if err != nil {
			return err
		}
		s.green, err = provedLaneBaseAt(ctx, root, gatePolicy.LaneBase, s.tip)
		if err != nil {
			return err
		}
		if hasPrerequisite(s.def, definition.PrerequisiteDesignDocument) {
			unshared, rewriteHolds, rewriteOwnerHolds, err = documentPrerequisites(r, root)
			if err != nil {
				return err
			}
		}
	}
	unproved, err := unprovedBlockers(ctx, r, root, s.green, s.states, s.edges)
	if err != nil {
		return err
	}
	buildHash, err := tick.Build.Hash()
	if err != nil {
		return err
	}
	rows := make([]itemRow, 0, len(s.items))
	inputs := map[string]*RunInputs{}
	for _, q := range s.items {
		row := s.itemsByID[q.id]
		if tick.examineItem != "" && tick.examineItem != row.ItemID {
			continue
		}
		if doc := deskDocument(row, tick.Comments); doc != "" {
			row.Designer, err = readDesigner(r, doc)
			if err != nil {
				return err
			}
		}
		if row.State == s.def.Ready && !unshared[row.ItemID] {
			inputs[row.ItemID], err = currentRunInputs(ctx, r, row, policy, root, *tick.Build)
			if err != nil {
				return err
			}
		}
		rows = append(rows, row)
	}
	if err := shadowRotation(r, &tick); err != nil {
		return err
	}
	p, err := s.buildPlanner(r, at, policy, tick, questions, rows, inputs, unproved)
	if err != nil {
		return err
	}
	s.occupied = p.held
	s.holds, s.entries = map[string]string{}, map[string]startViewEntry{}
	s.readyAt, s.laneFreeAt = map[string]time.Time{}, map[string]time.Time{}
	s.freeLanes = nil
	for _, lane := range *tick.AvailableLanes {
		if !s.occupied[lane] {
			s.freeLanes = append(s.freeLanes, lane)
			s.laneFreeAt[lane] = since(previous.laneFreeAt[lane], at)
		}
	}
	p.captureStart = func(row itemRow, d Decision) error {
		return s.evaluateEntry(r, row, d, at, previous)
	}
	_, _, err = p.planItems(r, rows, s.edges, s.faults, s.pending.works, inputs, s.retries, rewriteOwnerHolds, rewriteHolds, unshared, s.hasNodes, buildHash, policy, s.shut, tick, root, s.def, at)
	if err != nil {
		return err
	}
	s.reservations = map[string]string{}
	for lane, id := range p.workerLanes {
		if e := s.entries[id]; isDispatch(e.decision) && e.decision.LaneID == lane {
			s.reservations[lane] = id
		}
	}
	return nil
}

func since(previous, at time.Time) time.Time {
	if !previous.IsZero() {
		return previous
	}
	return at
}

func (s *startSnapshot) evaluateEntry(r *store.Reader, row itemRow, d Decision, at time.Time, previous startSnapshot) error {
	e := startViewEntry{decision: d, express: row.Express}
	if isDispatch(d) && d.RunRequest != nil {
		version, err := inputVersionFrom(r, d, versionFolds{works: s.pending.works, faults: s.faults, contentVersions: s.contentVersions})
		if err != nil {
			return err
		}
		e.version = version
		s.readyAt[row.ItemID] = since(previous.readyAt[row.ItemID], at)
		if d.DeskDocument != "" {
			s.laneFreeAt[d.LaneID] = since(previous.laneFreeAt[d.LaneID], at)
		}
	} else {
		reason := d.Reason
		if reason == "" {
			reason = d.Action
		}
		s.holds[row.ItemID] = reason
	}
	s.entries[row.ItemID] = e
	return nil
}

// nextStart only reads the published snapshot. Selection cannot do I/O,
// consume a lane or change any entry that another reader may observe.
func (s startSnapshot) nextStart() (startViewEntry, bool) {
	for _, q := range s.items {
		e := s.entries[q.id]
		d := e.decision
		if !isDispatch(d) || d.RunRequest == nil {
			continue
		}
		lane := d.LaneID // concrete lanes include desks and retained continuations
		if lane == "" {
			lane = s.firstFreeLane(e.express)
			if lane == "" && IsTrunkFixer(q.id) && !s.occupied["desk-trunk-fixer"] {
				lane = "desk-trunk-fixer"
			}
		} else if owner := s.reservations[lane]; owner != "" && owner != q.id {
			continue
		}
		if lane != "" {
			request := *d.RunRequest
			d.LaneID, request.LaneID = lane, lane
			d.RunRequest = &request
			identifyDecision(&d)
			e.decision = d
			return e, true
		}
	}
	return startViewEntry{}, false
}

func (s startSnapshot) firstFreeLane(express bool) string {
	if express {
		for _, lane := range s.freeLanes {
			if isExpressLane(lane) && s.reservations[lane] == "" {
				return lane
			}
		}
	}
	for _, lane := range s.freeLanes {
		if !isExpressLane(lane) && !strings.HasPrefix(lane, "desk-") && s.reservations[lane] == "" {
			return lane
		}
	}
	return ""
}
