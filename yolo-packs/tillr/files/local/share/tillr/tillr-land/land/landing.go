package land

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr-drain/redcap"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The outcomes a landing records.
const (
	// ResultLanded is a landing that moved the trunk ref to the squashed commit.
	ResultLanded = "landed"
	// ResultSquashConflict is a change that does not squash onto the trunk the landing
	// would publish onto. It is its own outcome rather than a refusal because the change
	// is what conflicts: the verdict stands, and the head is pinned so a conflict-fixer or
	// a person can still read it.
	ResultSquashConflict = "squash-conflict"
	// ResultRefused is a landing land declined to make. Nothing moves and the head is
	// pinned, so the refusal is readable after the process that saw it exits.
	ResultRefused = "refused"
)

// The refusal reasons this utility can emit. Every reason a `pass` or `one` can record is
// here, and a test asserts the set so a new one cannot be added without being named.
const (
	// ReasonVerdictMismatch is a verdict that does not bind the head's tree and the
	// landing's base sha.
	ReasonVerdictMismatch = "verdict-mismatch"
	// ReasonVerdictFailed is a completed negative verdict: the gate ran and said no.
	ReasonVerdictFailed = "verdict-failed"
	// ReasonVerdictError is a verdict that could not be completed. It is distinct from
	// verdict-failed because §5.7 says an error verdict is never a failure of the
	// candidate, so it must not read as one.
	ReasonVerdictError = "verdict-error"
	// ReasonTrunkUncommitted is a landing the trunk's worktree would not take: it holds
	// uncommitted content on a path the squash changes, so the fast-forward would stop on
	// it. The refusal is about the repository rather than about the change: the verdict
	// stands, and the landing is attempted again once `git status` in that worktree is
	// clean.
	ReasonTrunkUncommitted = "trunk-worktree-uncommitted"
	// ReasonPatchRewind is a fork-series landing whose head does not descend from the
	// patch branch's current tip. A patch branch is the delivery rather than a derived
	// export, so moving it back would drop commits another writer published.
	ReasonPatchRewind = "patch-rewind"
	// ReasonPatchAtHead is a fork-series landing whose patch branch already points at the
	// head. The run's own export delivered it, which is the successful case; a second move
	// would be a second delivery of one change.
	ReasonPatchAtHead = "patch-at-head"
	// ReasonItemNotLandable means the owner closed or resolved the item before landing.
	ReasonItemNotLandable = "item-not-landable"
	// ReasonTrunkRewritten means the locked trunk no longer contains the observed tip.
	ReasonTrunkRewritten = "trunk-rewritten"
	// ReasonAlreadyOnTrunk is a change the trunk already carries: the squash answers the
	// tip's own tree, so the only commit it could write is an empty one. It is a fact about
	// the trunk, not a failure of the candidate (landing principle 10), and nothing moves.
	// A retry of this run's own unrecorded landing is not this refusal: that is recorded as
	// the landing it was.
	ReasonAlreadyOnTrunk = "already-on-trunk"
	// ReasonRedCap is a repository proof-range hold, not a candidate failure.
	ReasonRedCap = redcap.Reason
)

// Reasons answers every refusal reason this utility can emit.
func Reasons() []string {
	return []string{
		ReasonVerdictMismatch,
		ReasonVerdictFailed,
		ReasonVerdictError,
		ReasonTrunkUncommitted,
		ReasonPatchRewind,
		ReasonPatchAtHead,
		ReasonItemNotLandable,
		ReasonTrunkRewritten,
		ReasonAlreadyOnTrunk,
		ReasonRedCap,
	}
}

// Landing is one row of the `landings` table and the `land.landing` payload: one stateless
// squash of one judged head, and what it did.
//
// Reason is empty on a landing: there is nothing to explain about a landing that
// happened. ConflictPaths names the paths git reported on a squash conflict (and the paths
// the trunk's worktree held uncommitted content on for a trunk-worktree refusal), so a
// reader answers what a repair has to look at without re-running the merge.
//
// GateSkippedReason is empty for every landing this utility records: the verdict is a
// required input, so the gate ran. It exists for the drain, the one caller that can land a
// tree whose gate it chose to skip, and it is recorded on the row rather than inferred so a
// reader never has to reconstruct a skip from a missing verdict.
type Landing struct {
	// LockObservation is returned after release; it is not part of the receipt.
	LockObservation   *LockObservation `json:"lock_observation,omitempty"`
	LandingID         string           `json:"landing_id"`
	ItemID            string           `json:"item_id"`
	RunID             string           `json:"run_id"`
	VerdictID         string           `json:"verdict_id"`
	Result            string           `json:"result"`
	Reason            string           `json:"reason"`
	BaseSHA           string           `json:"base_sha"`
	HeadSHA           string           `json:"head_sha"`
	TrunkBefore       string           `json:"trunk_before"`
	TrunkAfter        string           `json:"trunk_after"`
	GatedTree         string           `json:"gated_tree"`
	LandedTree        string           `json:"landed_tree"`
	ConflictPaths     []string         `json:"conflict_paths"`
	GateSkippedReason string           `json:"gate_skipped_reason"`
	RedCap            *redcap.Hold     `json:"red_cap,omitempty"`
}

// Landed reports whether this landing moved the trunk ref.
func (l Landing) Landed() bool { return l.Result == ResultLanded }

// LandingRecord wraps a landing in design §5.1's envelope, with the item as its subject so
// a reader can reassemble an item's landings from the record stream.
func LandingRecord(l Landing, at record.Envelope) record.Envelope {
	body, err := json.Marshal(l)
	if err != nil {
		// The payload is a struct of strings and a slice of strings; a marshal failure is
		// a bug, and the caller cannot be handed a record that does not describe the
		// landing.
		panic(fmt.Sprintf("land: encoding the %s payload: %v", LandingKind, err))
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          LandingKind,
		ID:            l.LandingID,
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     at.CreatedAt,
		Subject:       &record.Subject{Kind: "item", ID: l.ItemID},
		CorrelationID: at.CorrelationID,
		Payload:       body,
	}
}

// rowQueryer is the one read a landing id needs, satisfied by both a store.Reader and a
// store.Tx so the id is derived inside the transaction that writes the row.
type rowQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

// NextLandingID answers the id for the next landing on an item. It is derived from the
// store rather than generated, so two runs of the same case produce the same record: §5.1
// makes fixture and test modes reproducible, and a random id is the one field that would
// break that. The count is also the serialization: two landings racing on one item compute
// the same id, and the second insert fails on the primary key rather than writing a second
// landing for one head.
func NextLandingID(r rowQueryer, itemID string) (string, error) {
	var n int
	if err := r.QueryRow(`SELECT count(*) FROM landings WHERE item_id = ?`, itemID).Scan(&n); err != nil {
		return "", Internalf("counting the landings of %q: %v", itemID, err)
	}
	return fmt.Sprintf("landing_%s_%d", itemID, n+1), nil
}

// LandingRequest is one landing: the head the gate judged, the base it was judged against,
// the ref it lands onto, and the verdict that judged it.
//
// There is no queue between them. The request names the item and the run it is for so the
// landing can be filed, and the values it lands with are the ones the caller was handed —
// nothing is read back out of a candidate row, which is what makes the squash stateless.
type LandingRequest struct {
	// ObserveLock adds live measurements to the returned record, never the receipt.
	ObserveLock bool
	observation *LockObservation
	ItemID      string
	RunID       string
	BaseRef     string
	BaseSHA     string
	// ObservedTrunk is the loop's last observed trunk tip, not the candidate's base.
	// Empty is a manual landing with no loop observation to compare.
	ObservedTrunk string
	HeadSHA       string
	// MessageFrom selects the message and author date, never the gated tree.
	// Empty keeps the head's message; an explicit source must belong to base..head.
	MessageFrom string
	// RunDir is the commit-list directory; empty uses <git-common-dir>/tillr/runs/<run-id>.
	RunDir  string
	Verdict Verdict
	// At is the clock the landing records and the squash dates its commit with.
	At time.Time
	// CapNow samples the minute cap after taking the landing lock. Nil uses the
	// live clock; At remains the record clock, never landing authorization.
	CapNow func() time.Time
	// LockPath names another lander's lock file, for the migration; empty takes the
	// repository's own land lock.
	LockPath string
	// LockWait is how long the landing waits for the land lock. Zero waits until the
	// context ends.
	LockWait time.Duration
}

// Land gates nothing and judges nothing: it takes a verdict that already binds the head,
// squashes the change onto the trunk, and moves the trunk. It is the whole landing
// transaction, and both faces call it.
//
// The land lock is held for the whole landing transaction, including its receipt.
// The gate ran before this function was called — in another process, in the drain's case — so no lock spans it, and a tick
// that takes minutes to judge does not hold the trunk. Under the lock this function reads
// the trunk tip, squashes onto it, refuses a clash in the trunk's worktree, records the
// row, and moves the ref. A non-landing takes the same lock briefly to pin the head and
// write its row, so a head the gate saw and the landing refused survives a garbage
// collection.
//
// The squash is the landing: `git merge-tree --write-tree --merge-base=<base>` re-applies
// the change onto the tip, `git commit-tree` writes one commit with the repository's
// scope-checked identity and the selected source commit's message, naming no item, and `Trunk.Move`
// fast-forwards the trunk onto it. A squash that conflicts is its own outcome, not a repair: the verdict
// stands, the head is pinned, and a repair lane reads the conflict paths from the row.
//
// Landing is idempotent across the one boundary that is not atomic: the trunk move is a
// Git write and the receipt is a SQLite commit, and a crash can keep the first and lose the
// second. Before the move, the squash commit is recorded at refs/tillr/landed/<run_id>. A
// later call for the same run finds that commit in the trunk's history, checks it is exactly
// this head's squash onto its parent, and records the row for it without squashing again.
// A squash that answers the trunk's own tree — the change is already there, and no marker
// says this run put it there — is refused as already-on-trunk rather than published as an
// empty commit.
func Land(ctx context.Context, s *store.Store, t *Trunk, req LandingRequest) (answer Landing, err error) {
	// Preserve cancellation identity even when a Git helper adds its exit-code diagnostic.
	defer func() {
		if err != nil && ctx.Err() != nil && !errors.Is(err, ctx.Err()) {
			err = errors.Join(err, ctx.Err())
		}
	}()
	// Scope cancellation to this request without mutating a trunk shared by callers.
	requestTrunk := *t
	requestTrunk.ctx = ctx
	t = &requestTrunk
	if err := ctx.Err(); err != nil {
		return Landing{}, err
	}
	if err := checkLandingRequest(req); err != nil {
		return Landing{}, err
	}
	if err := t.HasCommit(req.BaseSHA); err != nil {
		return Landing{}, err
	}
	if err := t.HasCommit(req.HeadSHA); err != nil {
		return Landing{}, err
	}
	if req.ObservedTrunk != "" {
		if err := t.HasCommit(req.ObservedTrunk); err != nil {
			return Landing{}, err
		}
	}
	if req.MessageFrom != "" {
		source, err := t.MessageSource(req.BaseSHA, req.HeadSHA, req.MessageFrom)
		if err != nil {
			return Landing{}, err
		}
		req.MessageFrom = source
	} else {
		req.MessageFrom = req.HeadSHA
	}
	tree, err := t.TreeOf(req.HeadSHA)
	if err != nil {
		return Landing{}, err
	}

	landing := Landing{
		ItemID:    req.ItemID,
		RunID:     req.RunID,
		VerdictID: req.Verdict.VerdictID,
		BaseSHA:   req.BaseSHA,
		HeadSHA:   req.HeadSHA,
		GatedTree: tree,
	}
	landing.Result, landing.Reason = decide(req, tree)
	// A patch branch is a delivery, not a trunk a squash can move forward. When the run
	// already exported the head, the branch is at the head and a second move would deliver
	// the same change twice; when the branch has moved somewhere the head does not descend
	// from, moving it back would drop the commits between. Both are refusals, and neither
	// writes a ref.
	if landing.Result == ResultLanded && PatchRef(req.BaseRef) != "" {
		trunkHead, err := t.Head(req.BaseRef)
		if err != nil {
			return Landing{}, err
		}
		switch {
		case trunkHead == req.HeadSHA:
			landing.Result, landing.Reason = ResultRefused, ReasonPatchAtHead
		case !t.IsAncestor(trunkHead, req.HeadSHA):
			landing.Result, landing.Reason = ResultRefused, ReasonPatchRewind
		}
	}

	lock, err := t.LandLock()
	if err != nil {
		return Landing{}, err
	}
	if req.LockPath != "" {
		if lock, err = OpenLock(req.LockPath); err != nil {
			return Landing{}, err
		}
	}
	if _, err := lock.Acquire(ctx, req.LockWait); err != nil {
		return Landing{}, err
	}
	defer func() {
		lock.Release()
		if req.observation != nil {
			req.observation.Events = append(req.observation.Events, LockEvent{Kind: "released", AtNS: lock.releasedNS})
		}
		answer.LockObservation = req.observation
	}()
	if req.ObserveLock {
		req.observation = &LockObservation{RunID: req.RunID, Events: []LockEvent{{Kind: "acquired", AtNS: lock.acquiredNS}}}
	}

	prepared, err := prepareLanding(ctx, s, t, landing, req)
	if err != nil {
		return Landing{}, err
	}
	if prepared.stored != nil {
		return *prepared.stored, nil
	}
	// Only the final check, Move and receipt need SQLite's single writer. Close
	// either wins during preparation or waits here and sees the committed receipt.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		landing, err = finishLanding(ctx, tx, t, prepared, req)
		return err
	})
	if err == nil {
		req.observation.note("receipt")
	}
	return landing, err
}

type preparedLanding struct {
	landing                        Landing
	squash                         Squash
	historyBefore                  historySnapshot
	history                        *trunkHistory
	observationRef, observationTip string
	// tip is the trunk tip read under land.lock, which the final check compares against.
	// It is landing.TrunkBefore except for a recovered landing, whose row records the move
	// it already made.
	tip string
	// recovered is a landing this run already published and the store never recorded:
	// the row is written for that commit and the trunk is not moved again.
	recovered bool
	// stored is the receipt this run already recorded for this head at its marker: the
	// request is a retry of a recorded landing, answered with that row and nothing written.
	stored *Landing
}

// landingRecorded answers whether a landed row already names commit as its trunk_after.
func landingRecorded(r rowQueryer, commit string) (bool, error) {
	var recorded bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM landings WHERE result = ? AND trunk_after = ?)`, ResultLanded, commit).Scan(&recorded); err != nil {
		return false, Internalf("checking for a recorded landing at %s: %v", commit, err)
	}
	return recorded, nil
}

// storedLanding answers the landed row this item's run already recorded for this head at
// commit, if there is one: the receipt a retry of a recorded landing is answered with.
func storedLanding(r rowQueryer, req LandingRequest, commit string) (*Landing, error) {
	var l Landing
	var conflicts string
	err := r.QueryRow(`SELECT landing_id, item_id, run_id, verdict_id, result, reason, base_sha, head_sha,
		       trunk_before, trunk_after, gated_tree, landed_tree, conflict_paths, gate_skipped_reason
		  FROM landings
		 WHERE result = ? AND trunk_after = ? AND item_id = ? AND run_id = ? AND head_sha = ?
		 ORDER BY rowid LIMIT 1`, ResultLanded, commit, req.ItemID, req.RunID, req.HeadSHA).Scan(
		&l.LandingID, &l.ItemID, &l.RunID, &l.VerdictID, &l.Result, &l.Reason, &l.BaseSHA, &l.HeadSHA,
		&l.TrunkBefore, &l.TrunkAfter, &l.GatedTree, &l.LandedTree, &conflicts, &l.GateSkippedReason)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, Internalf("reading the recorded landing at %s: %v", commit, err)
	}
	if err := json.Unmarshal([]byte(conflicts), &l.ConflictPaths); err != nil {
		return nil, Internalf("decoding the conflict paths of landing %s: %v", l.LandingID, err)
	}
	if len(l.ConflictPaths) == 0 {
		// A landed row stores none, and the answer the landing first gave carried none.
		l.ConflictPaths = nil
	}
	return &l, nil
}

func itemNotLandable(r rowQueryer, id string) (bool, error) {
	var hasItems bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'items')`).Scan(&hasItems); err != nil {
		return false, Internalf("checking item schema: %v", err)
	}
	var notLandable bool
	if hasItems {
		if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM items WHERE item_id = ? AND state IN ('closed', 'resolved-elsewhere'))`, id).Scan(&notLandable); err != nil {
			return false, Internalf("reading item disposition: %v", err)
		}
	}
	return notLandable, nil
}

// prepareLanding holds land.lock but no SQLite transaction across Git or disk work.
func prepareLanding(ctx context.Context, s *store.Store, t *Trunk, landing Landing, req LandingRequest) (preparedLanding, error) {
	p := preparedLanding{landing: landing}
	// A verdict that passes is the only request whose run may already have landed; the
	// patch-branch refusals Land decided are what a recovered landing looks like from the
	// branch's side, so they do not hide one.
	verdictPasses, _ := decide(req, landing.GatedTree)
	marker, hasMarker := "", false
	if verdictPasses == ResultLanded {
		var err error
		if marker, hasMarker, err = t.LandedMarker(req.RunID); err != nil {
			return p, err
		}
	}
	notLandable, markerRecorded := false, false
	err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		if notLandable, err = itemNotLandable(r, req.ItemID); err != nil {
			return err
		}
		if hasMarker {
			if p.stored, err = storedLanding(r, req, marker); err != nil || p.stored != nil {
				return err
			}
			if markerRecorded, err = landingRecorded(r, marker); err != nil {
				return err
			}
		}
		if req.ObservedTrunk != "" {
			p.historyBefore, err = readHistory(r)
		}
		return err
	})
	if err != nil || p.stored != nil {
		// A retry of a recorded landing: its receipt is the answer. Nothing is squashed,
		// pinned, moved or written, so a retry cannot add a second row or a second commit.
		return p, err
	}
	trunkBefore, err := t.Head(req.BaseRef)
	if err != nil {
		return p, err
	}
	p.tip = trunkBefore
	p.landing.TrunkBefore, p.landing.TrunkAfter = trunkBefore, trunkBefore
	if hasMarker && !markerRecorded {
		parent, ok, err := t.RecoveredLanding(req.BaseSHA, req.HeadSHA, marker, trunkBefore)
		if PatchRef(req.BaseRef) != "" {
			parent, ok, err = req.BaseSHA, marker == req.HeadSHA && t.IsAncestor(marker, trunkBefore), nil
		}
		if err != nil {
			return p, err
		}
		if ok {
			if notLandable {
				return p, Externalf("%s already carries %s, the landing of run %s, but the store has no receipt for it and item %s is now closed or resolved elsewhere: nothing was recorded or moved; decide by hand whether the item stays closed with its change on the trunk, or revert %s",
					req.BaseRef, marker, req.RunID, req.ItemID, marker)
			}
			landedTree, err := t.TreeOf(marker)
			if err != nil {
				return p, err
			}
			p.recovered = true
			p.landing.Result, p.landing.Reason = ResultLanded, ""
			p.landing.TrunkBefore, p.landing.TrunkAfter, p.landing.LandedTree = parent, marker, landedTree
		}
	}
	if notLandable {
		p.landing.Result, p.landing.Reason = ResultRefused, ReasonItemNotLandable
	}
	if p.landing.Landed() && req.ObservedTrunk != "" {
		p.observationRef, p.observationTip = req.BaseRef, trunkBefore
		if PatchRef(req.BaseRef) != "" {
			p.observationRef, err = t.Branch()
			if err != nil {
				return p, err
			}
			p.observationTip, err = t.Head(p.observationRef)
			if err != nil {
				return p, err
			}
		}
		var rewritten bool
		p.history, rewritten, err = t.prepareHistory(p.historyBefore, req.ObservedTrunk, p.observationTip)
		if err != nil {
			return p, err
		}
		if rewritten {
			p.landing.Result, p.landing.Reason = ResultRefused, ReasonTrunkRewritten
			p.recovered = false
			p.landing.TrunkBefore, p.landing.TrunkAfter, p.landing.LandedTree = trunkBefore, trunkBefore, ""
		}
	}
	if p.landing.Landed() && PatchRef(req.BaseRef) != "" {
		// The pre-lock read cannot authorize a move over an external delivery made
		// while this request waited. Recheck the tip preparation actually locked.
		if !p.recovered {
			switch {
			case p.tip == req.HeadSHA:
				p.landing.Result, p.landing.Reason = ResultRefused, ReasonPatchAtHead
			case !t.IsAncestor(p.tip, req.HeadSHA):
				p.landing.Result, p.landing.Reason = ResultRefused, ReasonPatchRewind
			}
		}
		if p.landing.Landed() {
			if err := t.ensureForkCache(req, p.tip); err != nil {
				return p, err
			}
			cacheTip := req.HeadSHA
			if p.recovered {
				cacheTip = p.tip
			}
			if err := t.prepareForkCache(req.BaseRef, cacheTip); err != nil {
				return p, err
			}
		}
	}
	if p.recovered {
		// The range was preserved before the original move; preserving it again is
		// idempotent and keeps the commit list if the crash came before it was flushed.
		if err := t.PreserveRange(req.RunID, req.BaseSHA, req.HeadSHA, req.RunDir); err != nil {
			return p, err
		}
	} else if p.landing.Landed() {
		if PatchRef(req.BaseRef) != "" {
			// Fork commits are the delivery, not a diff to squash. Publish exactly the
			// built head; ancestry and the guarded move still refuse a rewind.
			p.squash = Squash{Commit: req.HeadSHA, Tree: p.landing.GatedTree}
		} else {
			req.observation.note("squash")
			p.squash, err = t.SquashWithMessage(req.BaseSHA, trunkBefore, req.HeadSHA, req.MessageFrom, req.At)
		}
		if err != nil {
			return p, err
		}
		// A cap limits trunk movement, not a finding that no movement is needed.
		// Compute the squash first so superseded candidates settle even while red.
		if !p.squash.Empty && !p.squash.Conflicted() {
			err = s.ReadTx(ctx, func(r *store.Reader) error {
				clock := req.CapNow
				if clock == nil {
					clock = time.Now
				}
				hold, err := redcap.Check(ctx, r, t.repo, req.BaseRef, trunkBefore, req.ItemID, clock())
				if err != nil {
					return Externalf("checking red cap: %v", err)
				}
				if hold != nil {
					p.landing.Result, p.landing.Reason, p.landing.RedCap = ResultRefused, ReasonRedCap, hold
				}
				return nil
			})
			if err != nil {
				return p, err
			}
		}
		if p.squash.Empty {
			p.landing.Result, p.landing.Reason = ResultRefused, ReasonAlreadyOnTrunk
		} else if p.squash.Conflicted() {
			p.landing.Result, p.landing.ConflictPaths = ResultSquashConflict, p.squash.Conflicts
		} else if p.landing.Landed() {
			p.landing.LandedTree = p.squash.Tree
			clashing, err := t.ClashingPaths(req.BaseRef, trunkBefore, p.squash.Commit)
			if err != nil {
				return p, err
			}
			if len(clashing) > 0 {
				p.landing.Result, p.landing.Reason = ResultRefused, ReasonTrunkUncommitted
				p.landing.ConflictPaths = clashing
			} else if err := t.PreserveRange(req.RunID, req.BaseSHA, req.HeadSHA, req.RunDir); err != nil {
				return p, err
			} else if err := t.MarkLanded(req.RunID, p.squash.Commit); err != nil {
				return p, err
			}
		}
	}
	// Pin before acquiring the writer, including successful preparations that a
	// concurrent Close may turn into a refusal at the final check.
	if err := t.Pin(req.RunID, req.HeadSHA); err != nil {
		return p, err
	}
	return p, ctx.Err()
}

func finishLanding(ctx context.Context, tx *store.Tx, t *Trunk, p preparedLanding, req LandingRequest) (Landing, error) {
	landing := p.landing
	notLandable, err := itemNotLandable(tx, req.ItemID)
	if err != nil {
		return Landing{}, err
	}
	if notLandable && p.recovered {
		return Landing{}, Externalf("%s already carries %s, the landing of run %s, but item %s was closed while the receipt was being prepared: nothing was recorded or moved; decide by hand whether the item stays closed with its change on the trunk, or revert %s",
			req.BaseRef, landing.TrunkAfter, req.RunID, req.ItemID, landing.TrunkAfter)
	}
	if notLandable {
		landing.Result, landing.Reason = ResultRefused, ReasonItemNotLandable
		landing.LandedTree, landing.ConflictPaths = "", nil
	}
	if p.recovered {
		recorded, err := landingRecorded(tx, landing.TrunkAfter)
		if err != nil {
			return Landing{}, err
		}
		if recorded {
			return Landing{}, Externalf("the landing of run %s at %s was recorded during preparation; nothing was recorded twice", req.RunID, landing.TrunkAfter)
		}
	}
	tip, err := t.Head(req.BaseRef)
	if err != nil {
		return Landing{}, err
	}
	if tip != p.tip {
		return Landing{}, Externalf("trunk %s changed during landing preparation: expected %s, got %s", req.BaseRef, p.tip, tip)
	}
	if !notLandable && p.observationRef != "" {
		current, err := readHistory(tx)
		if err != nil {
			return Landing{}, err
		}
		if current != p.historyBefore {
			return Landing{}, Externalf("trunk history changed during landing preparation; no move made")
		}
		if p.observationRef != req.BaseRef {
			tip, err = t.Head(p.observationRef)
			if err != nil {
				return Landing{}, err
			}
			if tip != p.observationTip {
				return Landing{}, Externalf("observed trunk changed during landing preparation; no move made")
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Landing{}, err
	}
	// A recovered landing's move already happened: its row is written and nothing moves,
	// so the history keeps the tip read under the lock.
	if landing.Landed() && !p.recovered {
		req.observation.note("trunk-move")
		if err := t.Move(req.BaseRef, p.squash.Commit, p.tip); err != nil {
			return Landing{}, err
		}
		landing.TrunkAfter = p.squash.Commit
		if p.history != nil && PatchRef(req.BaseRef) == "" {
			p.history.observed = p.squash.Commit
		}
	}
	if !notLandable && p.history != nil {
		if err := t.saveHistory(tx, p.history); err != nil {
			return Landing{}, err
		}
	}
	if landing.Landed() && PatchRef(req.BaseRef) != "" {
		cacheTip := landing.TrunkAfter
		if p.recovered {
			cacheTip = p.tip
		}
		if err := t.RefreshForkCache(req.BaseRef, cacheTip); err != nil {
			return Landing{}, err
		}
	}
	return recordLanding(ctx, tx, landing, req.At)
}

// recordLanding writes one landing row and appends its event in the same transaction, and
// answers the landing as stored. It is the one place a landing becomes state, so the
// landed and non-landed paths cannot disagree about what a recorded landing is.
func recordLanding(ctx context.Context, tx *store.Tx, landing Landing, at time.Time) (Landing, error) {
	conflicts, err := json.Marshal(landing.ConflictPaths)
	if err != nil {
		return Landing{}, Internalf("encoding the conflict paths of %q: %v", landing.HeadSHA, err)
	}
	if landing.ConflictPaths == nil {
		conflicts = []byte("[]")
	}
	err = func() error {
		id, err := NextLandingID(tx, landing.ItemID)
		if err != nil {
			return err
		}
		landing.LandingID = id
		if _, err := tx.Exec(
			`INSERT INTO landings
			   (landing_id, item_id, run_id, verdict_id, result, reason, base_sha, head_sha,
			    trunk_before, trunk_after, gated_tree, landed_tree, conflict_paths,
			    gate_skipped_reason)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			landing.LandingID, landing.ItemID, landing.RunID, landing.VerdictID, landing.Result,
			landing.Reason, landing.BaseSHA, landing.HeadSHA, landing.TrunkBefore,
			landing.TrunkAfter, landing.GatedTree, landing.LandedTree, string(conflicts),
			landing.GateSkippedReason,
		); err != nil {
			return Internalf("writing the landing: %v", err)
		}
		seq, err := appendLanding(ctx, tx, landing, at)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE landings SET event_seq = ? WHERE landing_id = ?`,
			seq, landing.LandingID); err != nil {
			return Internalf("recording the event sequence: %v", err)
		}
		return nil
	}()
	if err != nil {
		return Landing{}, err
	}
	return landing, nil
}

// appendLanding appends one landing's event in the caller's transaction and answers its
// sequence. The event names the item as its object and carries the whole landing as its
// payload, so a reader joins it to the row without a second lookup and reads the result,
// the reason and the conflict paths without decoding a face's stdout.
func appendLanding(ctx context.Context, tx *store.Tx, l Landing, at time.Time) (int64, error) {
	body, err := json.Marshal(l)
	if err != nil {
		return 0, Internalf("encoding the %s event: %v", LandingKind, err)
	}
	item := l.ItemID
	return tx.Append(event.Event{
		Kind:       LandingKind,
		ObjectType: "item",
		ObjectID:   &item,
		Payload:    body,
		At:         at,
	})
}

// decide is the whole of land's judgement about a verdict, as a pure function: the verdict
// must bind the head's tree and the landing's base, and only then does its result decide.
// It touches no store and no repository, and it moves nothing.
func decide(req LandingRequest, tree string) (result, reason string) {
	if req.Verdict.TreeSHA != tree || req.Verdict.BaseSHA != req.BaseSHA {
		return ResultRefused, ReasonVerdictMismatch
	}
	switch req.Verdict.Result {
	case VerdictPass:
		if PatchRef(req.BaseRef) != "" {
			built := false
			for _, check := range req.Verdict.Checks {
				if check.CheckID == "fork-build" && check.Result == VerdictPass {
					built = true
				}
			}
			if !built {
				return ResultRefused, ReasonVerdictMismatch
			}
		}
		return ResultLanded, ""
	case VerdictFail:
		return ResultRefused, ReasonVerdictFailed
	default:
		// checkVerdict has already refused any result outside the three, so this is the
		// error verdict: a check that could not run, which is not a failure of the
		// candidate and must not read as one.
		return ResultRefused, ReasonVerdictError
	}
}

// checkLandingRequest refuses a request the landing cannot describe. A landing with no item,
// no run, no head, no base or no verdict is not a decision about a change: it is a caller
// mistake, and it is refused before anything is read.
func checkLandingRequest(req LandingRequest) error {
	for _, field := range []struct{ name, value string }{
		{"item_id", req.ItemID},
		{"run_id", req.RunID},
		{"base_sha", req.BaseSHA},
		{"head_sha", req.HeadSHA},
		{"verdict_id", req.Verdict.VerdictID},
	} {
		if field.value == "" {
			return Usagef("a landing needs a %s", field.name)
		}
	}
	if req.BaseRef == "" {
		return Usagef("a landing needs a base ref: a landing onto no branch is a landing in the wrong place")
	}
	return nil
}
