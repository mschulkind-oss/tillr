package drain

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type startClaimSnapshot struct {
	head    int64
	pending *pendingHistoryState
	faults  map[string]ItemFault
}

// snapshotForClaim extends the immutable proposal's folds at a read boundary.
// The writer checks the delta after head instead of folding any history.
func (s startSnapshot) snapshotForClaim(r *store.Reader) (startClaimSnapshot, error) {
	head, err := r.EventHeadSeq()
	if err != nil {
		return startClaimSnapshot{}, err
	}
	seq := s.lastEventSeq
	pending := clonePendingHistoryState(s.pending)
	byReason := make(map[string]map[string]ItemFault, len(s.faultsByItemReason))
	for id, reasons := range s.faultsByItemReason {
		byReason[id] = maps.Clone(reasons)
	}
	if err := foldPendingHistory(r, seq, pending, nil); err != nil {
		return startClaimSnapshot{}, err
	}
	if err := faults.Fold(r, seq, byReason, nil); err != nil {
		return startClaimSnapshot{}, err
	}
	return startClaimSnapshot{head, pending, faults.Resolve(byReason)}, nil
}

func (l *Loop) fastStartBase(ctx context.Context, r *store.Reader, proposal startSnapshot, itemID string, continuing bool) (base string, ready bool, err error) {
	base = proposal.tip
	if !l.nonLanding && !IsTrunkFixer(itemID) && proposal.green != "" {
		// The content version binds these edges and the blockers' states.
		edges := map[string][]string{itemID: proposal.edges[itemID]}
		waits, err := unprovedBlockers(ctx, r, l.cfg.Root, proposal.green, "", proposal.states, edges)
		if err != nil || waits[itemID] != "" {
			return "", false, err
		}
		if !continuing {
			base, err = blockBaseAt(ctx, r, l.cfg.Root, proposal.green, proposal.tip, itemID)
			return base, err == nil, err
		}
	}
	return base, true, nil
}

// claimFastStart records a proposed dispatch but never stages it.
func (l *Loop) claimFastStart(ctx context.Context, v *startView, proposal startSnapshot, e startViewEntry, at time.Time) (bool, *pendingWork, string, Decision, error) {
	d := e.decision
	if !isDispatch(d) || d.RunRequest == nil || d.LaneID == "" || proposal.pending == nil {
		return false, nil, "", Decision{}, nil
	}
	policy := l.currentPolicy()
	owner, err := currentLoopOwner()
	if err != nil {
		return false, nil, "", Decision{}, err
	}
	reader, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, nil, "", Decision{}, err
	}
	var snap startClaimSnapshot
	var plainInputs *RunInputs
	prepared := d
	var designBase string
	var designErr error
	closed := proposal.infrastructureClosed
	gctx, facts := gatheringFor(ctx)
	err = reader.ReadTx(gctx, func(r *store.Reader) error {
		var err error
		snap, err = proposal.snapshotForClaim(r)
		if err != nil {
			return err
		}
		changed, err := backendAvailabilityChanged(r, proposal.lastEventSeq)
		if err != nil {
			return err
		}
		if !changed {
			changed, err = pendingHistoryChanged(r, proposal.lastEventSeq)
			if err != nil {
				return err
			}
		}
		if changed {
			if closed, err = readInfrastructureClosedFrom(r, snap.pending.byRun); err != nil {
				return err
			}
		}
		// Populate the I/O keys against the observed tip. The writer still
		// validates its own rows, and writeGatheredFrom handles any misses.
		work := snap.pending.works[d.ItemID]
		_, _, _ = l.fastStartBase(gctx, r, proposal, d.ItemID, work != nil && (work.Ready || d.WorkflowRestart))
		if d.BlockConflict != nil {
			designErr = l.prepareDesignContinuation(gctx, r, &prepared, snap.pending.byRun[d.BlockConflict.DesignRunID], &designBase)
		}
		if d.Inputs != nil {
			row, found, err := readItemByID(r, d.ItemID)
			if err != nil || !found {
				return err
			}
			plainInputs, err = currentRunInputs(gctx, r, row, policy, l.cfg.Root, l.build)
			if l.nonLanding {
				return err
			}
			if err == nil && *plainInputs != *d.Inputs {
				_, _ = l.revParse(gctx, l.cfg.Root, TrunkRef)
			}
		}
		return nil
	})
	reader.Close()
	if err != nil {
		return false, nil, "", Decision{}, err
	}
	if l.nonLanding && d.Inputs != nil && (plainInputs == nil || *plainInputs != *d.Inputs) {
		return false, nil, "", Decision{}, nil
	}

	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, nil, "", Decision{}, err
	}
	defer s.Close()
	if l.cfg.LiveScale && !strings.HasPrefix(d.LaneID, "desk-") {
		l.laneOfferMu.RLock()
		defer l.laneOfferMu.RUnlock()
		if !slices.Contains(l.lanes(), d.LaneID) {
			return false, nil, "", Decision{}, nil
		}
	}
	works, byRun := snap.pending.works, snap.pending.byRun
	var claimed *pendingWork
	// Any Git/file fact a validation needs is fetched by writeGathered only
	// after rolling back. The next attempt rechecks the SQLite facts.
	err = writeGatheredFrom(ctx, s, facts, seatClaim(policy, func(ctx context.Context, tx *store.Tx) error {
		claimed = nil
		r := tx.Reader()
		current, err := currentDefinition(r, plannedApplication{definitionSHA: proposal.definitionSHA})
		if err != nil || !current {
			return err
		}
		changed, err := pendingHistoryChanged(r, snap.head)
		if err != nil || changed {
			return err
		}
		changed, err = backendAvailabilityChanged(r, snap.head)
		if err != nil || changed {
			return err
		}
		// Pending history already covers fault clear and boundary convictions;
		// an additional fault or obstacle hold also invalidates this proposal.
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE seq > ? AND kind = ?
			UNION ALL SELECT 1 FROM events WHERE seq > ? AND kind IN (?, ?) AND object_id = ?)`,
			snap.head, faults.HeldKind, proposal.lastEventSeq, obstacles.HeldKind, obstacles.ClearedKind, d.ItemID).Scan(&changed); err != nil || changed {
			return err
		}
		v.mu.Lock()
		hold, held := v.holds[d.ItemID]
		v.mu.Unlock()
		if held && (hold.contentVersion == "" || hold.contentVersion == proposal.contentVersions[d.ItemID]) {
			return nil
		}

		versioned := d
		versioned.LaneID = e.versionLane
		version, err := inputVersionFrom(r, versioned, versionFolds{works: works, faults: snap.faults})
		if errors.Is(err, sql.ErrNoRows) {
			return nil // Item removed after the proposal.
		}
		if err != nil || version != e.version {
			return err
		}
		row, found, err := readItemByID(r, d.ItemID)
		if err != nil || !found {
			return err
		}
		if !l.nonLanding && d.Inputs != nil {
			now, err := currentRunInputs(ctx, r, row, policy, l.cfg.Root, l.build)
			if err != nil {
				return err
			}
			if *now != *d.Inputs {
				// Like a legacy planned start, reject an edit at the same tip,
				// not a later landing. The proposal keeps its decided base.
				head, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
				if err != nil || head == proposal.tip {
					return err
				}
			}
		}

		work := works[d.ItemID]
		continuing := work != nil && (work.Ready || d.WorkflowRestart)
		if work != nil && !work.Done && !continuing {
			return nil
		}
		for _, other := range works {
			if !other.Done && !other.LaneReleased && other.Decision.LaneID == d.LaneID && !(other == work && continuing) {
				return nil
			}
		}
		leases, err := activeLeases(r, at, []string{d.LaneID})
		if err != nil || len(leases) != 0 {
			return err
		}
		var kind string
		err = r.QueryRow(`SELECT kind FROM events WHERE object_type='lane' AND object_id=? AND kind IN (?, ?) ORDER BY seq DESC LIMIT 1`,
			d.LaneID, laneRefusedKind, laneRestoredKind).Scan(&kind)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if kind == laneRefusedKind {
			return nil
		}
		if d.DeskDocument != "" && l.cfg.Comments != nil {
			held, err := readLanes(r, at)
			if err != nil {
				return err
			}
			for _, other := range works {
				if !other.Done && !other.LaneReleased {
					held[other.Decision.LaneID] = true
				}
			}
			delete(held, d.LaneID)
			if countDocumentDesks(held, nil) >= l.cfg.Comments.MaxRunning {
				return nil
			}
		}
		if !l.nonLanding && !isExpressLane(d.LaneID) {
			conflicts, err := item.ReadConflicts(r)
			if err != nil {
				return err
			}
			for id, c := range conflicts {
				if id == d.ItemID {
					continue
				}
				if origin := designContinuationWork(c, byRun[c.DesignRunID]); origin != nil && origin.Decision.LaneID == d.LaneID {
					var state string
					err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state)
					if errors.Is(err, sql.ErrNoRows) {
						continue
					}
					if err != nil {
						return err
					}
					wait, _, err := item.ReadWaitingOn(r, id)
					if err != nil {
						return err
					}
					if state == StateBlocked && !c.HasIndependentWait(wait) {
						return nil
					}
				}
			}
		}
		fork := row.Delivery.activeFork()
		if fork != "" && !(d.Workflow != nil && classificationNode(d.Workflow.Stages[d.NodeIndex])) {
			forks, err := liveForks(r, at, works)
			if err != nil {
				return err
			}
			for _, held := range forks {
				if held.fork == fork && !(continuing && held.itemID == d.ItemID) {
					return nil
				}
			}
		}

		base, ready, err := l.fastStartBase(ctx, r, proposal, d.ItemID, continuing)
		if err != nil || !ready {
			return err
		}
		if continuing && work.BaseSHA != "" {
			base = work.BaseSHA
		}
		if designErr != nil {
			return designErr
		}
		if designBase != "" {
			base = designBase
		}
		d := prepared
		quota, err := readQuotaOnlyShut(r, l.cfg.Now(), policy)
		if err != nil {
			return err
		}
		parallelShut := withInfrastructureClosed(quota, closed)
		shut := parallelShut
		if d.Action == ActionRetry {
			shut = quota // An owner-authorized retry may test a closed backend.
		}
		open, err := dispatchQuotaOpen(r, d, shut, policy)
		if err != nil || !open || (d.DesignContinuationFrom != "" && d.RunRequest.SessionTurn && shut[d.RunRequest.Backend]) {
			return err
		}
		claimed, err = l.recordFastStart(ctx, tx, d, base, fork, owner, byRun, policy, shut, parallelShut, at)
		return err
	}))
	if errors.Is(err, errSeatWindowClosed) {
		return false, nil, "", Decision{}, nil
	}
	var collision *decisionCollisionError
	if errors.As(err, &collision) {
		if holdErr := l.holdItemFault(ctx, Candidate{ItemID: d.ItemID}, ReasonDecisionCollision, decisionCollisionDetail(collision, d.Action)); holdErr != nil {
			return false, nil, "", Decision{}, holdErr
		}
		return false, nil, "", Decision{}, nil
	}
	if err != nil || claimed == nil {
		return false, nil, "", Decision{}, err
	}
	return true, claimed, claimed.Decision.PassID, claimed.Decision, nil
}

func (l *Loop) recordFastStart(ctx context.Context, tx *store.Tx, d Decision, base, fork string, owner *presence.Owner, byRun map[string]*pendingWork, policy *Policy, shut, parallelShut map[string]bool, at time.Time) (*pendingWork, error) {
	if d.BlockConflict != nil {
		c := *d.BlockConflict
		c.Status, c.DesignRunID = "awaiting", d.RunRequest.RunID
		d.BlockConflict = &c
		if err := item.RecordConflict(tx, c, at); err != nil {
			return nil, err
		}
	} else if d.ConflictID != "" || (d.Action == ActionRetry && d.SupersedesRunID != "") {
		conflicts, err := item.ReadConflicts(tx.Reader())
		if err != nil {
			return nil, err
		}
		c := conflicts[d.ItemID]
		if d.ConflictID != "" && (c.ConflictID != d.ConflictID || c.Status != "answered") {
			return nil, nil
		}
		if d.ConflictID != "" || (c.Status == "owner" && c.RunID == d.SupersedesRunID && strings.HasPrefix(c.Text, conflictAfterRestartPrefix)) {
			c.Status = "closed"
			if err := item.RecordConflict(tx, c, at); err != nil {
				return nil, err
			}
		}
	}
	generation, err := allocateGeneration(tx)
	if err != nil {
		return nil, err
	}
	correlate := func(node *Decision) {
		node.LaneID, node.PassID = d.LaneID, generation
		if node.RunRequest != nil {
			req := *node.RunRequest
			req.LaneID, req.CorrelationID = d.LaneID, generation
			node.RunRequest = &req
		}
	}
	correlate(&d)
	d.Parallel = append([]Decision(nil), d.Parallel...)
	for i := range d.Parallel {
		correlate(&d.Parallel[i])
	}
	if d.Workflow != nil {
		if d.NodeBaseSHA == "" && fork == "" {
			d.NodeBaseSHA = base
		}
		if err := clearSupersededNodeClaims(tx, d.ItemID); err != nil {
			return nil, err
		}
	}
	if err := carryRetainedReaders(tx, d); err != nil {
		return nil, err
	}
	if err := l.claimParallelWithShut(tx, &d, byRun, parallelShut); err != nil {
		return nil, err
	}
	nodes := parallelNodes(d)
	keep := make(map[string]bool, len(nodes))
	for i := range nodes {
		ok, err := selectSeatDispatch(tx, &nodes[i], policy, shut, at)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errSeatWindowClosed
		}
		if nodes[i].RunRequest != nil {
			keep[nodes[i].RunRequest.RunID] = true
		}
	}
	d = nodes[0]
	d.Parallel = nodes[1:]
	if err := recordWorkflowSelection(tx, d); err != nil {
		return nil, err
	}
	if err := carryReturnedNodeInputs(tx, d); err != nil {
		return nil, err
	}
	if err := supersedeItemUnfinishedRunsInTx(ctx, tx, d.ItemID, d.Attempt, keep, "superseded by "+d.RunRequest.RunID, at); err != nil {
		return nil, err
	}
	for _, node := range nodes {
		if len(nodes) > 1 {
			rec, err := terminalRecord(tx.Reader(), node.RunRequest.RunID)
			if err != nil {
				return nil, err
			}
			if rec != nil {
				continue
			}
		}
		stampDecisionRequest(node, node.RunRequest)
		if _, err := run.ClaimLease(ctx, tx, *node.RunRequest, node.LaneID, at); err != nil {
			return nil, err
		}
		if err := startNode(tx, node); err != nil {
			return nil, err
		}
	}
	work := &pendingWork{Decision: d, BaseSHA: base, Owner: owner, LoopID: l.currentSessionID()}
	if err := appendEffect(tx, dispatchStartedKind, d.RunRequest.RunID, work, at); err != nil {
		return nil, err
	}
	if err := recordPassDecision(tx, d, at); err != nil {
		return nil, err
	}
	return work, nil
}
