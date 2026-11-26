package drain

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

type triageConflictKey struct {
	itemID  string
	version string
}

type pendingWorkStoreInputs struct {
	paused           bool
	liveDraining     bool
	drainingDecision pendingStoreDecision
	terminalDecision Decision
	terminalSettled  bool
	operatorFinished bool
	liveFirstSegment bool
	planDecision     pendingStoreDecision
	unfinishedRun    bool
}

type backendWindowDetail struct {
	reset time.Time
	kind  string
	runID string
}

type itemVersionStoreInputs struct {
	itemState sql.NullString
	rank      sql.NullString
	allowed   sql.NullString
	design    sql.NullString
	seat      sql.NullString
	express   bool
	updated   int64
	itemSeq   int64
	latest    sql.NullString
	seq       sql.NullInt64
	ownerOwed bool
}

// startRepairInputs holds every store fact repairEntriesAt needs, gathered inside
// one ReadTx so all git and external subprocesses run after the transaction ends.
type startRepairInputs struct {
	tick              Pass
	noop              bool
	versionSchema     versionSchema
	obstacleHolds     map[string]obstacles.Hold
	releasedObstacles map[string]string

	hasDocSync  bool
	docSync     item.SyncStoreState
	ideaIntakes map[string]ideaIntakeResult

	blockerCommits map[string]landedBlockerCommit

	laneRows  []rawLaneRow
	lanesByID map[string]rawLaneRow

	designers          map[string]*lastDesigner
	blockFields        map[string]storedBlockFields
	proseByItem        map[string]string
	requestWords       map[string][]byte
	triageConflicts    map[triageConflictKey][]byte
	workflowSelections map[string]storedWorkflowSelection
	retainedOutcomes   map[string]map[string]string
	commandHolds       map[string]string
	versionItems       map[string]itemVersionStoreInputs

	pending map[string]pendingWorkStoreInputs

	parallelTerminals   map[string]bool
	closedBackendCauses map[string]string
	backendWindows      map[string]backendWindowDetail
	quotaStatuses       []QuotaStatus
}

func (in *startRepairInputs) activeLeases(now time.Time, lanes []string, exceptLane, exceptRunID string) []LaneLease {
	return activeLeasesFromRows(in.laneRows, now, lanes, exceptLane, exceptRunID)
}

func (in *startRepairInputs) itemWordsSources(itemID string, hasProse, hasCriteria bool) ([]string, error) {
	var sources []string
	if words := in.requestWords[itemID]; len(words) > 0 {
		sources = append(sources, string(words))
	}
	if hasProse {
		if prose := in.proseByItem[itemID]; prose != "" {
			sources = append(sources, prose)
		}
	}
	if hasCriteria {
		var b strings.Builder
		for _, c := range in.blockFields[itemID].Criteria {
			if c[1] != "" {
				b.WriteString(c[1])
				b.WriteByte('\n')
			}
		}
		if b.Len() > 0 {
			sources = append(sources, b.String())
		}
	}
	res := in.ideaIntakes[itemID]
	if res.err != nil {
		return nil, res.err
	}
	if res.intake != nil && res.intake.intake != "" {
		if raw, err := os.ReadFile(res.intake.intake); err == nil && len(raw) > 0 {
			sources = append(sources, string(raw))
		}
	}
	return sources, nil
}

func (in *startRepairInputs) describeQuotaHold(d *Decision, policy *Policy, at time.Time) {
	name := d.DeferredBy
	if win, ok := in.backendWindows[name]; ok {
		d.BackendHoldCause = fmt.Sprintf("%s quota refusal recorded by run %s", win.kind, win.runID)
		d.BackendHoldMover = "provider quota resets at " + win.reset.Format(time.RFC3339) + "; after an early reset, record a successful probe-backend"
		return
	}
	describeQuotaHoldFromStatuses(d, policy.Backends[name], name, in.quotaStatuses, at)
}

func (in *startRepairInputs) inputVersion(d Decision, folds versionFolds) (string, error) {
	vi, ok := in.versionItems[d.ItemID]
	if !ok {
		return "", sql.ErrNoRows
	}
	var itemInputs []any
	if d.Action == ActionDispatch || d.Action == ActionRetry {
		version, exists := folds.contentVersions[d.ItemID]
		if !exists {
			return "", sql.ErrNoRows
		}
		itemInputs = []any{version}
	} else {
		itemInputs = []any{vi.itemState, vi.rank, vi.allowed, vi.design, vi.seat, vi.express, vi.updated, vi.itemSeq}
	}
	var laneState, laneRun sql.NullString
	var until sql.NullInt64
	if d.LaneID != "" {
		if lane, exists := in.lanesByID[d.LaneID]; exists {
			laneState = sql.NullString{String: lane.state, Valid: true}
			laneRun = lane.runID
			until = lane.leasedUntil
		}
	}
	work, faults := folds.works[d.ItemID], folds.faults
	triageVersion := ""
	if d.BlockConflict != nil && d.BlockConflict.TriageVersion != "" {
		fields, exists := in.blockFields[d.ItemID]
		if !exists {
			return "", sql.ErrNoRows
		}
		var err error
		triageVersion, _, err = requestVersionFromStored(fields)
		if err != nil {
			return "", err
		}
	}
	var resumeSeqs map[string]int64
	if work != nil && len(work.resumes) != 0 {
		resumeSeqs = make(map[string]int64, len(work.resumes))
		for id, segment := range work.resumes {
			resumeSeqs[id] = segment.seq
		}
	}
	raw, err := json.Marshal(append(itemInputs, vi.latest, vi.seq, laneState, laneRun, until, work, work != nil && work.Done, resumeSeqs, faults[d.ItemID], faults[d.ItemID].Seq, triageVersion, vi.ownerOwed))
	return string(raw), err
}

func (s *startSnapshot) readRepairInputs(r *store.Reader, at time.Time, policy *Policy, tick Pass, root string, previous startSnapshot, touched map[string]bool, bases *startBases, mayWiden bool) (startRepairInputs, error) {
	if _, err := shadowRotation(r, &tick); err != nil {
		return startRepairInputs{}, err
	}
	s.updateFreeLanes(tick, previous, at)
	if !mayWiden && touched != nil && len(touched) == 0 {
		return startRepairInputs{tick: tick, noop: true}, nil
	}

	docInputsChanged := touched == nil
	for id := range touched {
		docInputsChanged = docInputsChanged || s.contentVersions[id] != previous.contentVersions[id]
		if IsTrunkFixer(id) && s.states[id] != previous.states[id] {
			touched = nil
		}
	}
	if s.def.Completes == "landing" && root != "" && bases != nil && (bases.tip != previous.tip || bases.green != previous.green) {
		touched = nil
	}
	needTrunk := mayWiden || (s.def.Completes == "landing" && root != "" && bases == nil)
	needDocPrereqs := s.def.Completes == "landing" && root != "" &&
		hasPrerequisite(s.def, definition.PrerequisiteDesignDocument) &&
		(docInputsChanged || needTrunk || (bases != nil && bases.tip != previous.tip))
	readAll := touched == nil || needTrunk || needDocPrereqs

	out := startRepairInputs{
		tick:                tick,
		versionSchema:       s.versionSchema,
		lanesByID:           map[string]rawLaneRow{},
		designers:           map[string]*lastDesigner{},
		blockFields:         map[string]storedBlockFields{},
		proseByItem:         map[string]string{},
		requestWords:        map[string][]byte{},
		triageConflicts:     map[triageConflictKey][]byte{},
		workflowSelections:  map[string]storedWorkflowSelection{},
		retainedOutcomes:    map[string]map[string]string{},
		commandHolds:        map[string]string{},
		versionItems:        map[string]itemVersionStoreInputs{},
		pending:             map[string]pendingWorkStoreInputs{},
		parallelTerminals:   map[string]bool{},
		closedBackendCauses: map[string]string{},
		backendWindows:      map[string]backendWindowDetail{},
	}

	var err error
	if out.obstacleHolds, out.releasedObstacles, err = ReadObstacleHolds(r); err != nil {
		return startRepairInputs{}, err
	}
	if out.laneRows, err = readRawLanes(r); err != nil {
		return startRepairInputs{}, err
	}
	for _, lr := range out.laneRows {
		out.lanesByID[lr.laneID] = lr
	}

	iceboxEnabled := holdEnabled(s.def, ReasonIcebox) && root != ""
	if needDocPrereqs || iceboxEnabled {
		if out.ideaIntakes, err = readAllIdeaIntakes(r); err != nil {
			return startRepairInputs{}, err
		}
	}
	if needDocPrereqs {
		if out.docSync, err = item.ReadSyncStoreState(r); err != nil {
			return startRepairInputs{}, err
		}
		out.hasDocSync = true
	}

	if s.def.Completes == "landing" && root != "" && (needTrunk || (bases != nil && bases.green != "")) {
		edges := s.edges
		if !readAll {
			edges = map[string][]itemEdge{}
			for id := range touched {
				edges[id] = s.edges[id]
			}
		}
		if out.blockerCommits, err = readLandedBlockerCommits(r, s.states, edges); err != nil {
			return startRepairInputs{}, err
		}
	}

	_, latestByItem := indexPriorRuns(s.priors)
	hasExpress := s.versionSchema.itemColumns["express"]
	hasProse := s.versionSchema.itemColumns["prose"]
	hasClaimClass := s.versionSchema.hasClaimClass

	waits := map[string]bool{}
	for _, q := range s.items {
		row := s.itemsByID[q.id]
		if tick.examineItem != "" && tick.examineItem != row.ItemID {
			continue
		}
		if wait := s.waits[row.ItemID]; s.conflicts[row.ItemID].HasIndependentWait(&wait) {
			waits[row.ItemID] = true
		}
	}

	for itemID, work := range s.pending.works {
		if work == nil || work.Done {
			continue
		}
		var pw pendingWorkStoreInputs
		if !work.LaneReleased {
			if pw.paused, err = pausedPendingWork(r, work); err != nil {
				return startRepairInputs{}, err
			}
			if pw.liveDraining, err = liveDrainingWorker(r, work, at); err != nil {
				return startRepairInputs{}, err
			}
		}
		waiting := waits[itemID]
		if pw.paused && !work.LaneBusyByOther && tick.AvailableLanes != nil && tick.examineItem == "" {
			ownLeases := out.activeLeases(at, []string{work.Decision.LaneID}, "", "")
			if len(ownLeases) == 0 {
				if pw.drainingDecision, err = pendingDecisionStoreOnly(r, work, at, policy, s.shut, tick.GatesInFlight, tick.ownExaminationLane, tick.ownExaminationRunID, waiting); err != nil {
					return startRepairInputs{}, err
				}
			}
		}
		itemState := s.states[itemID]
		if finishedState(s.def, itemState) && work.Decision.Workflow == nil {
			if pw.terminalDecision, pw.terminalSettled, err = terminalSettlementExcept(r, work, at, tick.ownExaminationLane, tick.ownExaminationRunID); err != nil {
				return startRepairInputs{}, err
			}
		}
		if !pw.terminalSettled && !work.BoundaryFailed && !closedState(s.def, itemState) && itemState == StateLanded {
			if pw.operatorFinished, err = operatorFinishedLanding(r, itemID); err != nil {
				return startRepairInputs{}, err
			}
		}
		if !pw.terminalSettled && len(tick.runFaces) != 0 && (waiting || evaluatorNode(work.Decision)) {
			if pw.liveFirstSegment, err = liveFirstSegmentWorker(r, work, at, tick.runFaces); err != nil {
				return startRepairInputs{}, err
			}
		}
		if !pw.terminalSettled && !pw.liveFirstSegment {
			if pw.planDecision, err = pendingDecisionStoreOnly(r, definitionWork(s.def, work), at, policy, s.shut, tick.GatesInFlight, tick.ownExaminationLane, tick.ownExaminationRunID, waiting); err != nil {
				return startRepairInputs{}, err
			}
		}
		if pw.unfinishedRun, err = workHasUnfinishedRun(r, work); err != nil {
			return startRepairInputs{}, err
		}
		for _, node := range parallelNodes(work.Decision) {
			if node.RunRequest == nil {
				continue
			}
			rec, err := terminalRecord(r, node.RunRequest.RunID)
			if err != nil {
				return startRepairInputs{}, err
			}
			out.parallelTerminals[node.RunRequest.RunID] = rec != nil
		}
		out.pending[itemID] = pw
	}

	if policy != nil {
		for name, outcome := range s.infrastructureClosed {
			failure, err := readRecordedInfrastructureFailure(r, outcome.Rec.ItemID, "$.run_id", outcome.Rec.RunID)
			if err != nil {
				return startRepairInputs{}, err
			}
			cause := failure.Detail
			if cause == "" {
				err := r.QueryRow(`SELECT COALESCE(json_extract(payload,'$.cause'),'') FROM events
 WHERE kind=? AND object_id=? AND json_extract(payload,'$.terminal_seq')=?`, backendOutcomeKind, name, outcome.Seq).Scan(&cause)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return startRepairInputs{}, err
				}
			}
			out.closedBackendCauses[name] = cause
		}
		windows, err := readBackendWindows(r, at)
		if err != nil {
			return startRepairInputs{}, err
		}
		for name, reset := range windows {
			var kind, runID string
			if err := r.QueryRow(`SELECT kind, run_id FROM backend_windows WHERE backend=?`, name).Scan(&kind, &runID); err != nil {
				return startRepairInputs{}, err
			}
			out.backendWindows[name] = backendWindowDetail{reset: reset, kind: kind, runID: runID}
		}
		if out.quotaStatuses, err = readQuotaStatuses(r); err != nil {
			return startRepairInputs{}, err
		}
	}

	for _, q := range s.items {
		row := s.itemsByID[q.id]
		if tick.examineItem != "" && tick.examineItem != row.ItemID {
			continue
		}
		if !readAll && !touched[row.ItemID] {
			continue
		}
		if doc := deskDocument(row, tick.Comments); doc != "" {
			if _, ok := out.designers[doc]; !ok {
				designer, err := readDesigner(r, doc)
				if err != nil {
					return startRepairInputs{}, err
				}
				out.designers[doc] = designer
			}
		}
		mayTriage := !finishedState(s.def, row.State) && !IsGeneratedItem(row.ItemID) && (s.unshared[row.ItemID] || needDocPrereqs)
		if row.State == s.def.Ready || mayTriage {
			fields, err := readStoredBlockFields(r, row.ItemID)
			if err != nil {
				return startRepairInputs{}, err
			}
			out.blockFields[row.ItemID] = fields
			if mayTriage {
				version, _, err := requestVersionFromStored(fields)
				if err != nil {
					return startRepairInputs{}, err
				}
				var raw []byte
				err = r.QueryRow(`SELECT payload FROM events WHERE kind='item.conflict' AND json_extract(payload,'$.item_id')=? AND json_extract(payload,'$.triage_version')=? ORDER BY seq DESC LIMIT 1`, row.ItemID, version).Scan(&raw)
				if err == nil {
					out.triageConflicts[triageConflictKey{itemID: row.ItemID, version: version}] = raw
				} else if !errors.Is(err, sql.ErrNoRows) {
					return startRepairInputs{}, err
				}
			}
		}
		if iceboxEnabled && row.State == s.def.Ready {
			words, err := item.RequestWords(r, row.ItemID)
			if err != nil {
				return startRepairInputs{}, err
			}
			if len(words) > 0 {
				out.requestWords[row.ItemID] = words
			}
			if hasProse {
				var prose string
				err := r.QueryRow(`SELECT COALESCE(prose, '') FROM items WHERE item_id=?`, row.ItemID).Scan(&prose)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return startRepairInputs{}, err
				}
				if prose != "" {
					out.proseByItem[row.ItemID] = prose
				}
			}
		}
		work := s.pending.works[row.ItemID]
		if work == nil || work.Done {
			if !IsGeneratedRepair(row.ItemID) && !isForkRebaseItem(row.ItemID) {
				selected, source, graph, err := item.ReadWorkflowSelection(r, row.ItemID)
				if err != nil {
					return startRepairInputs{}, err
				}
				out.workflowSelections[row.ItemID] = storedWorkflowSelection{selected: selected, source: source, graph: graph}
			}
			_, hasRetry := s.retries[row.ItemID]
			if hasRetry && s.hasNodes {
				nextAtt := s.attempts[row.ItemID]
				if nextAtt <= 0 {
					nextAtt = len(s.attemptPriors[row.ItemID]) + 1
				}
				reusedFrom := nextAtt - 1
				if n := s.nodeCounts[row.ItemID]; n > 0 {
					reusedFrom = n
				}
				if reusedFrom >= 1 {
					outcomes, err := workflowOutcomes(r, row.ItemID, reusedFrom)
					if err != nil {
						return startRepairInputs{}, err
					}
					out.retainedOutcomes[row.ItemID] = outcomes
				}
			}
			latest := latestByItem[row.ItemID]
			obstacleReleased := latest != "" && out.releasedObstacles[row.ItemID] == latest
			waitReleased := false
			if latest != "" && s.history.waitReleases[row.ItemID] == latest {
				waitReleased = true
			}
			if s.hasNodes && !finishedState(s.def, row.State) && !hasRetry && !obstacleReleased && !waitReleased {
				hold, err := commandNodeHold(r, row.ItemID)
				if err != nil {
					return startRepairInputs{}, err
				}
				out.commandHolds[row.ItemID] = hold
			}
		}
		if err := out.readVersionItem(r, row.ItemID, hasExpress, hasClaimClass); err != nil {
			return startRepairInputs{}, err
		}
	}
	return out, nil
}

func (in *startRepairInputs) readVersionItem(r *store.Reader, itemID string, hasExpress, hasClaimClass bool) error {
	var vi itemVersionStoreInputs
	if hasExpress {
		if err := r.QueryRow(`SELECT express FROM items WHERE item_id=?`, itemID).Scan(&vi.express); err != nil {
			return err
		}
	}
	if err := r.QueryRow(`SELECT state, rank, allowed_paths, design_doc, seat, updated_at, event_seq FROM items WHERE item_id = ?`, itemID).
		Scan(&vi.itemState, &vi.rank, &vi.allowed, &vi.design, &vi.seat, &vi.updated, &vi.itemSeq); err != nil {
		return err
	}
	err := r.QueryRow(`SELECT run_id, event_seq FROM runs WHERE item_id = ? ORDER BY started_at DESC, event_seq DESC, rowid DESC LIMIT 1`, itemID).Scan(&vi.latest, &vi.seq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if hasClaimClass {
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM item_criteria WHERE item_id=? AND claim_class='owner-only' AND state <> 'done')`, itemID).Scan(&vi.ownerOwed); err != nil {
			return err
		}
	}
	in.versionItems[itemID] = vi
	return nil
}
