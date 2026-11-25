package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

type startViewEntry struct {
	decision Decision
	// An unassigned entry is versioned without a lane. A proposal has no claim authority.
	version     string
	versionLane string // concrete lane at versioning, before nextStart assigns one
	express     bool
}

// Capture loop-owned configuration on the loop goroutine; the watcher must not
// read its mutable holds, questions or rotation maps.
func (l *Loop) startViewPass() Pass {
	available := l.lanes()
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
	return readStartSnapshotWith(ctx, path, at, policy, func(r *store.Reader, s *startSnapshot) (func() error, error) {
		return s.prepareRepair(ctx, r, at, policy, tick, questions, previous, nil)
	})
}

// Use recorded refusals at the same read boundary as leases. Local rotation
// notes overlay this baseline when the snapshot is published.
func shadowRotation(r *store.Reader, tick *Pass) (map[string]laneRefusal, error) {
	lanes := make([]string, 0, len(*tick.AvailableLanes))
	refused := map[string]laneRefusal{}
	// Recorded refusals supplement the caller's rotation observation.
	out := make(map[string]bool, len(tick.OutLanes))
	maps.Copy(out, tick.OutLanes)
	tick.OutLanes = out
	for _, lane := range *tick.AvailableLanes {
		var kind string
		var payload []byte
		err := r.QueryRow(`SELECT kind, payload FROM events WHERE object_type='lane' AND object_id=? AND kind IN (?, ?) ORDER BY seq DESC LIMIT 1`, lane, laneRefusedKind, laneRestoredKind).Scan(&kind, &payload)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if kind == laneRefusedKind {
			var refusal laneRefusal
			if err := json.Unmarshal(payload, &refusal); err != nil {
				return nil, err
			}
			tick.OutLanes[lane] = true
			refused[lane] = refusal
		} else {
			lanes = append(lanes, lane)
		}
	}
	tick.AvailableLanes = &lanes
	return refused, nil
}

func (s *startSnapshot) buildPlanner(r *store.Reader, at time.Time, policy *Policy, tick Pass, questions *VantageQuestions, rows []itemRow, inputs map[string]*RunInputs, unproved map[string]string) (*planner, error) {
	lanes, owners := s.heldLanes(), maps.Clone(s.deskOwners)
	for _, work := range s.pending.works {
		if !work.Done && !work.LaneReleased && !work.LaneBusyByOther {
			owners[work.Decision.LaneID] = work.Decision.ItemID
		}
	}
	waits, commentHolds := map[string]item.WaitingOn{}, map[string]string{}
	for _, row := range rows {
		if wait := s.waits[row.ItemID]; s.conflicts[row.ItemID].HasIndependentWait(&wait) {
			waits[row.ItemID] = wait
		}
		if hasPrerequisite(s.def, definition.PrerequisiteDesignQuestions) && item.IsCommentItem(row.ItemID) && !finishedState(s.def, row.State) {
			commentHolds[plannerDocumentKey(questions.Root, row.DesignDoc)] = row.ItemID
		}
	}
	var (
		obstacleHolds     map[string]obstacles.Hold
		releasedObstacles map[string]string
		err               error
	)
	if s.preparedRepair != nil {
		obstacleHolds, releasedObstacles = s.preparedRepair.obstacleHolds, s.preparedRepair.releasedObstacles
	} else {
		obstacleHolds, releasedObstacles, err = ReadObstacleHolds(r)
		if err != nil {
			return nil, err
		}
	}
	classes, latest := indexPriorRuns(s.priors)
	p := &planner{
		preRead:    s.preparedRepair,
		definition: &s.def, commentHolds: commentHolds, documentRoot: questions.Root,
		conflicts: s.conflicts, shut: s.shut, quotaShut: s.quotaShut, infrastructureClosed: s.infrastructureClosed, forks: s.forks,
		tick: tick, policy: policy, held: lanes, deskOwners: owners, itemStates: s.states, history: s.history,
		priors: s.attemptPriors, retries: s.retries, classes: classes, latest: latest, latestFinished: s.latestFinished,
		obstacleHolds: obstacleHolds, releasedObstacles: releasedObstacles, questions: questions,
		waits: waits, escalated: s.escalated, at: at,
		runningAgentLimit: ordinaryLaneCount(*tick.AvailableLanes), activeOrdinaryAgents: maps.Clone(s.activeOrdinaryAgents),
		fixerOpen: trunkFixerOpen(rows, s.def), unproved: unproved,
		designHistory: s.pending.byRun, inputs: inputs, inputDecisions: s.inputDecisions,
		works: s.pending.works, attempts: s.attempts, nodeCounts: s.nodeCounts, requests: s.requests,
	}
	p.reserveWorkerLanes(rows, nil)
	return p, nil
}

func (s *startSnapshot) rebuildEntries(ctx context.Context, r *store.Reader, at time.Time, policy *Policy, tick Pass, questions *VantageQuestions, previous startSnapshot) error {
	return s.repairEntries(ctx, r, at, policy, tick, questions, previous, nil)
}

func (s *startSnapshot) stageRepairInputs(r *store.Reader, at time.Time, policy *Policy, tick Pass, root string, previous startSnapshot, touched map[string]bool, bases *startBases, mayWiden bool) error {
	in, err := s.readRepairInputs(r, at, policy, tick, root, previous, touched, bases, mayWiden)
	if err != nil {
		return err
	}
	s.preparedRepair = &in
	return nil
}

func (s *startSnapshot) prepareRepair(ctx context.Context, r *store.Reader, at time.Time, policy *Policy, tick Pass, questions *VantageQuestions, previous startSnapshot, touched map[string]bool) (func() error, error) {
	if err := s.stageRepairInputs(r, at, policy, tick, questions.Root, previous, touched, nil, basesMayWiden(s.def, questions.Root, nil)); err != nil {
		return nil, err
	}
	return func() error {
		return s.repairEntriesAt(ctx, nil, at, policy, tick, questions, previous, touched, nil)
	}, nil
}

func basesMayWiden(def definition.Definition, root string, bases *startBases) bool {
	return def.Completes == "landing" && root != "" && bases == nil
}

func (s *startSnapshot) repairEntries(ctx context.Context, r *store.Reader, at time.Time, policy *Policy, tick Pass, questions *VantageQuestions, previous startSnapshot, touched map[string]bool) error {
	return s.repairEntriesAt(ctx, r, at, policy, tick, questions, previous, touched, nil)
}

func (s *startSnapshot) repairEntriesAt(ctx context.Context, r *store.Reader, at time.Time, policy *Policy, tick Pass, questions *VantageQuestions, previous startSnapshot, touched map[string]bool, bases *startBases) error {
	in := s.preparedRepair
	defer func() { s.preparedRepair = nil }()
	if in == nil {
		read, err := s.readRepairInputs(r, at, policy, tick, questions.Root, previous, touched, bases, basesMayWiden(s.def, questions.Root, bases))
		if err != nil {
			return err
		}
		in = &read
		s.preparedRepair = in
	}
	tick = in.tick
	if in.noop || (touched != nil && len(touched) == 0) {
		return nil
	}
	root := questions.Root
	unshared, rewriteHolds, rewriteOwnerHolds := s.unshared, s.rewriteHolds, s.rewriteOwnerHolds
	docInputsChanged := touched == nil
	for id := range touched {
		docInputsChanged = docInputsChanged || s.contentVersions[id] != previous.contentVersions[id]
		if IsTrunkFixer(id) && s.states[id] != previous.states[id] {
			touched = nil // the fixer's disposition can change ordinary holds
		}
	}
	if s.def.Completes == "landing" && root != "" {
		if bases == nil {
			read, err := refreshTrunk(ctx, root, s.def)
			if err != nil {
				return err
			}
			bases = &read
		}
		s.tip, s.green = bases.tip, bases.green
		if (docInputsChanged || s.tip != previous.tip) && hasPrerequisite(s.def, definition.PrerequisiteDesignDocument) {
			var err error
			unshared, rewriteHolds, rewriteOwnerHolds, err = documentPrerequisitesFromInputs(root, in.docSync, in.ideaIntakes, s.states)
			if err != nil {
				return err
			}
		}
	}
	if s.tip != previous.tip || s.green != previous.green ||
		!maps.Equal(s.unshared, unshared) || !maps.Equal(s.rewriteHolds, rewriteHolds) || !maps.Equal(s.rewriteOwnerHolds, rewriteOwnerHolds) {
		touched = nil
	}
	s.unshared, s.rewriteHolds, s.rewriteOwnerHolds = unshared, rewriteHolds, rewriteOwnerHolds
	edges := s.edges
	if touched != nil {
		edges = map[string][]string{}
		for id := range touched {
			edges[id] = s.edges[id]
		}
	}
	unproved, err := unprovedBlockersFromCommits(ctx, root, s.green, s.tip, s.states, edges, in.blockerCommits)
	if err != nil {
		return err
	}
	buildHash, err := tick.Build.Hash()
	if err != nil {
		return err
	}
	rows := make([]itemRow, 0, len(s.items))
	repair := make([]itemRow, 0, len(touched))
	inputs := map[string]*RunInputs{}
	for _, q := range s.items {
		row := s.itemsByID[q.id]
		if tick.examineItem != "" && tick.examineItem != row.ItemID {
			continue
		}
		rows = append(rows, row)
		if touched != nil && !touched[row.ItemID] {
			continue
		}
		if doc := deskDocument(row, tick.Comments); doc != "" {
			row.Designer = in.designers[doc]
		}
		if row.State == s.def.Ready && !unshared[row.ItemID] {
			inputs[row.ItemID], err = currentRunInputsFromStored(ctx, row, in.blockFields[row.ItemID], policy, root, *tick.Build)
			if err != nil {
				return err
			}
		}
		repair = append(repair, row)
	}
	p, err := s.buildPlanner(nil, at, policy, tick, questions, rows, inputs, unproved)
	if err != nil {
		return err
	}
	pausedReserved, err := p.reserveDrainingWork(nil, rows, s.edges)
	if err != nil {
		return err
	}
	s.pausedReserved = pausedReserved
	if touched == nil {
		s.holds, s.entries, s.readyAt = map[string]string{}, map[string]startViewEntry{}, map[string]time.Time{}
	} else {
		s.holds, s.entries, s.readyAt = maps.Clone(s.holds), maps.Clone(s.entries), maps.Clone(s.readyAt)
		for id := range touched {
			delete(s.holds, id)
			delete(s.entries, id)
			delete(s.readyAt, id)
		}
	}
	p.captureStart = func(row itemRow, d Decision) error {
		return s.evaluateEntry(nil, row, d, at, previous)
	}
	_, _, err = p.planItems(nil, repair, s.edges, s.faults, s.pending.works, inputs, s.retries, rewriteOwnerHolds, rewriteHolds, unshared, s.hasNodes, buildHash, policy, s.shut, tick, root, s.def, at)
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

func (s startSnapshot) heldLanes() map[string]bool {
	lanes := maps.Clone(s.storeLanes)
	for _, work := range s.pending.works {
		if !work.Done && !work.LaneReleased && !work.LaneBusyByOther {
			lanes[work.Decision.LaneID] = true
		}
	}
	return lanes
}

func (s *startSnapshot) updateFreeLanes(tick Pass, previous startSnapshot, at time.Time) {
	s.occupied = s.heldLanes()
	s.rotation = *tick.AvailableLanes
	s.laneFreeAt = map[string]time.Time{}
	s.freeLanes = nil
	for _, lane := range *tick.AvailableLanes {
		if !s.occupied[lane] {
			s.freeLanes = append(s.freeLanes, lane)
			s.laneFreeAt[lane] = since(previous.laneFreeAt[lane], at)
		}
	}
}

func since(previous, at time.Time) time.Time {
	if !previous.IsZero() {
		return previous
	}
	return at
}

func (s *startSnapshot) evaluateEntry(r *store.Reader, row itemRow, d Decision, at time.Time, previous startSnapshot) error {
	e := startViewEntry{decision: d, versionLane: d.LaneID, express: row.Express}
	dispatch := isDispatch(d) && d.RunRequest != nil
	if dispatch || (d.Action == ActionHold && persistentHold(d)) {
		folds := versionFolds{works: s.pending.works, faults: s.faults, contentVersions: s.contentVersions}
		var (
			version string
			err     error
		)
		if s.preparedRepair != nil {
			version, err = s.preparedRepair.inputVersion(d, folds)
		} else {
			version, err = inputVersionFrom(r, d, folds)
		}
		if err != nil {
			return err
		}
		e.version = version
	}
	if dispatch {
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
	return s.selectStart(false, 0)
}

func (s startSnapshot) nextFreshStart() (startViewEntry, bool) {
	return s.selectStart(true, 0)
}

func (s startSnapshot) nextFreshStartWithGates(gates int) (startViewEntry, bool) {
	return s.selectStart(true, gates)
}

func (s startSnapshot) selectStart(freshOnly bool, gates int) (startViewEntry, bool) {
	limit := ordinaryLaneCount(s.rotation)
	activeCount := len(s.activeOrdinaryAgents)
	var claimed map[string]bool
	for _, express := range s.pausedReserved {
		if !express {
			activeCount++
		}
		if lane := s.firstFreeLaneExcept(express, claimed); lane != "" {
			if claimed == nil {
				claimed = map[string]bool{}
			}
			claimed[lane] = true
		}
	}
	var itemOrder map[string]int
	for idx, q := range s.items {
		var work *pendingWork
		if s.pending != nil {
			work = s.pending.works[q.id]
		}
		e := s.entries[q.id]
		d := e.decision
		if work != nil && !work.Done {
			// A higher-placed pending item whose lane was released or left rotation
			// needs a free lane from the pass; withhold its lane so a lower-placed
			// fresh start cannot claim it first.
			if d.LaneID == "" && (d.Action == ActionJudge || (freshOnly && isDispatch(d))) {
				if work.GateStall == nil || work.GateStall.canRetry(gates) {
					isCommand := isCommandDecision(d)
					runKey := ""
					if d.RunRequest != nil {
						runKey = d.RunRequest.RunID
					}
					alreadyActive := runKey != "" && s.activeOrdinaryAgents[runKey] != ""
					if e.express || isCommand || alreadyActive || activeCount < limit {
						lane := s.firstFreeLaneExcept(e.express, claimed)
						if lane == "" && d.Action == ActionJudge {
							if itemOrder == nil {
								itemOrder = make(map[string]int, len(s.items))
								for i, item := range s.items {
									itemOrder[item.id] = i
								}
							}
							lane = s.firstFreeLaneOverLaterReservation(e.express, claimed, itemOrder, idx)
						}
						if lane != "" {
							if claimed == nil {
								claimed = map[string]bool{}
							}
							claimed[lane] = true
							if !e.express && !isCommand && !alreadyActive && !isExpressLane(lane) && !strings.HasPrefix(lane, "desk-") {
								activeCount++
							}
						}
					}
				}
				continue
			}
			if freshOnly {
				continue // Retained attempts belong to the pass and its completion handoff.
			}
		}
		if !isDispatch(d) || d.RunRequest == nil {
			continue
		}
		isCommand := isCommandDecision(d)
		isDesk := d.DeskDocument != "" || strings.HasPrefix(d.LaneID, "desk-")
		alreadyActive := s.activeOrdinaryAgents[d.RunRequest.RunID] != ""
		slotOpen := e.express || isCommand || isDesk || alreadyActive || activeCount < limit
		lane := d.LaneID // concrete lanes include desks and retained continuations
		if lane == "" {
			if slotOpen {
				lane = s.firstFreeLaneExcept(e.express, claimed)
			}
			if lane == "" && (IsTrunkFixer(q.id) || IsRemoteSyncItem(q.id)) && !s.occupied["desk-trunk-fixer"] {
				lane = "desk-trunk-fixer"
			}
		} else if !slotOpen {
			continue
		} else if owner := s.reservations[lane]; (owner != "" && owner != q.id) || (freshOnly && s.occupied[lane]) || claimed[lane] {
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
	return s.firstFreeLaneExcept(express, nil)
}

func (s startSnapshot) firstFreeLaneExcept(express bool, claimed map[string]bool) string {
	if express {
		for _, lane := range s.freeLanes {
			if isExpressLane(lane) && s.reservations[lane] == "" && !claimed[lane] {
				return lane
			}
		}
	}
	for _, lane := range s.freeLanes {
		if !isExpressLane(lane) && !strings.HasPrefix(lane, "desk-") && s.reservations[lane] == "" && !claimed[lane] {
			return lane
		}
	}
	return ""
}

func (s startSnapshot) firstFreeLaneOverLaterReservation(express bool, claimed map[string]bool, itemOrder map[string]int, before int) string {
	takes := func(lane string) bool {
		if claimed[lane] {
			return false
		}
		owner := s.reservations[lane]
		if owner == "" {
			return true
		}
		pos, ok := itemOrder[owner]
		return ok && pos > before
	}
	if express {
		for _, lane := range s.freeLanes {
			if isExpressLane(lane) && takes(lane) {
				return lane
			}
		}
	}
	for _, lane := range s.freeLanes {
		if !isExpressLane(lane) && !strings.HasPrefix(lane, "desk-") && takes(lane) {
			return lane
		}
	}
	return ""
}
