package land

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
		ReasonForeignIdentity,
		ReasonAlreadyOnTrunk,
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
	LockObservation *LockObservation `json:"lock_observation,omitempty"`
	LandingID       string           `json:"landing_id"`
	ItemID          string           `json:"item_id"`
	RunID           string           `json:"run_id"`
	VerdictID       string           `json:"verdict_id"`
	Result          string           `json:"result"`
	Reason          string           `json:"reason"`
	// BaseRef is retained in the event payload for marker lifetime readers. Older
	// rows/events omit it; their cleanup is conservative while a run is in flight.
	BaseRef           string       `json:"base_ref,omitempty"`
	BaseSHA           string       `json:"base_sha"`
	HeadSHA           string       `json:"head_sha"`
	TrunkBefore       string       `json:"trunk_before"`
	TrunkAfter        string       `json:"trunk_after"`
	GatedTree         string       `json:"gated_tree"`
	LandedTree        string       `json:"landed_tree"`
	ConflictPaths     []string     `json:"conflict_paths"`
	GateSkippedReason string       `json:"gate_skipped_reason"`
	RedCap            *redcap.Hold `json:"red_cap,omitempty"`
	// Faults name what the landing could not finish cleanly: a recovery ref an earlier
	// landing left, or bytes the owner saved after the trunk moved, with the ref that
	// holds them. A landing with no fault leaves no recovery ref.
	Faults []string `json:"faults,omitempty"`
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
	// MessageFrom bounds the worker commit range in base..head, excluding any
	// loop-owned commits after it (such as document-block retirement), never the
	// gated tree. Empty uses base..head; an explicit bound must belong to base..head.
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
// The land lock is held for the whole landing transaction, including its receipt. The
// store's write lock is not held across any of it: each store write is a few statements
// with no git, subprocess or file inside (publishLanding).
// The gate ran before this function was called — in another process, in the drain's case — so no lock spans it, and a tick
// that takes minutes to judge does not hold the trunk. Under the lock this function reads
// the trunk tip, squashes onto it, refuses a clash in the trunk's worktree, records the
// row, and moves the ref. A non-landing takes the same lock briefly to pin the head and
// write its row, so a head the gate saw and the landing refused survives a garbage
// collection.
//
// The squash is the landing: `git merge-tree --write-tree --merge-base=<base>` re-applies
// the change onto the tip, `git commit-tree` writes one commit with the repository's
// scope-checked identity and the candidate range's message, naming no item, and `Trunk.Move`
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
		BaseRef:   req.BaseRef,
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

	// A touched path the owner saved after the prediction sends the landing back to predict
	// again, under the same lock: the transaction changed nothing.
	decided := landing
	busyPause := 25 * time.Millisecond
	// changedAt is the readings of the prediction whose install answered that the owner changed
	// a path. The next prediction must read something differently; when it reads the same, the
	// owner changed nothing and the install itself is at fault, so the landing ends as a failed
	// install does instead of repeating.
	changedAt := ""
	for {
		prepared, err := prepareLanding(ctx, s, t, decided, req)
		if err != nil {
			t.dropRecovery(prepared.plan)
			return Landing{}, err
		}
		if prepared.stored != nil {
			return *prepared.stored, nil
		}
		if changedAt != "" && prepared.plan != nil && prepared.plan.readings() == changedAt {
			failure := &InstallFailure{Err: errors.New("the install answered that the owner changed a touched path, and the next prediction read every touched path, its index entry and the branch as before")}
			return t.settleOwner(ctx, s, prepared, req, &publication{}, decided, failure)
		}
		// No store write is open across the move: a short write claims the landing, the
		// trunk moves with nothing held, and a short write records it (publishLanding).
		pub := &publication{}
		landing, err = publishLanding(ctx, s, t, prepared, req, pub)
		if errors.Is(err, ErrIndexBusy) {
			// Ordinary Git work holds the checkout's index lock. That is contention, not a
			// finding about the owner's content: nothing is held or refused, and the landing
			// tries again after a short pause that the request's context can end.
			landing = decided
			timer := time.NewTimer(busyPause)
			select {
			case <-ctx.Done():
				timer.Stop()
				return Landing{}, ctx.Err()
			case <-timer.C:
			}
			if busyPause < time.Second {
				busyPause *= 2
			}
			continue
		}
		if errors.Is(err, ErrOwnerChanged) {
			landing = decided
			changedAt = ""
			if prepared.plan != nil {
				changedAt = prepared.plan.readings()
			}
			if err := ctx.Err(); err != nil {
				return Landing{}, err
			}
			continue
		}
		return t.settleOwner(ctx, s, prepared, req, pub, landing, err)
	}
}

// publication is the install a transaction made, if its trunk move happened.
type publication struct{ install *OwnerInstall }

// settleOwner closes what the owner overlay left open once the transaction has ended, in
// whatever way: the last read of the replaced files comes after the receipt is durable or has
// failed, a published checkout is never undone, a fault is written in a transaction of its own,
// and a preparation that installed nothing removes its own recovery copy.
func (t *Trunk) settleOwner(ctx context.Context, s *store.Store, p preparedLanding, req LandingRequest, pub *publication, landing Landing, err error) (Landing, error) {
	// What the owner overlay owes is settled with a context the request cannot cancel: bytes
	// must not disappear because the request was canceled and Git then refused to run.
	cleanup := *t
	cleanup.ctx = context.WithoutCancel(ctx)
	t = &cleanup
	if pub.install != nil {
		pub.install.t = t
	}
	var failure *InstallFailure
	switch {
	case pub.install != nil:
		faults := pub.install.Finish()
		if len(faults) == 0 {
			break
		}
		landing.Faults = append(landing.Faults, faults...)
		if ferr := recordFault(ctx, s, req, landing.LandingID, faults); ferr != nil {
			err = errors.Join(err, ferr)
		}
		if err != nil {
			err = errors.Join(err, errors.New(strings.Join(faults, "; ")))
		}
	case errors.As(err, &failure):
		// The owner's content was put back; the recovery copy stays and the fault names it.
		ref := ""
		if p.plan != nil {
			ref = p.plan.RecoveryRef
		}
		restored := "the owner's content was put back exactly"
		if failure.Fault != "" {
			restored = failure.Fault
		}
		fault := fmt.Sprintf("installing the landing failed: %v; %s; recovery copy %s", failure.Err, restored, ref)
		if ferr := recordFault(ctx, s, req, "", []string{fault}); ferr != nil {
			err = errors.Join(err, ferr)
		}
		return Landing{}, errors.Join(err, errors.New("fault recorded: "+fault))
	default:
		t.dropRecovery(p.plan)
	}
	if err == nil {
		req.observation.note("receipt")
	}
	return landing, err
}

// recordFault appends the land.fault event in a write transaction of its own, after the
// landing's transaction ended. Nothing is computed inside it.
func recordFault(ctx context.Context, s *store.Store, req LandingRequest, landingID string, faults []string) error {
	if OwnerHooks.FailFaultEvent != nil {
		if err := OwnerHooks.FailFaultEvent(); err != nil {
			return err
		}
	}
	body, err := json.Marshal(map[string]any{"run_id": req.RunID, "landing_id": landingID, "faults": faults})
	if err != nil {
		return Internalf("encoding the %s event: %v", FaultKind, err)
	}
	item := req.ItemID
	return s.WriteTx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: FaultKind, ObjectType: "item", ObjectID: &item, Payload: body, At: req.At})
		return err
	})
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
	// plan is the prediction for the trunk's checkout, read before the write transaction.
	plan *OwnerPlan
	// stored is the receipt this run already recorded for this head at its marker: the
	// request is a retry of a recorded landing, answered with that row and nothing written.
	stored *Landing
	// staleClaims are claims of landings whose move never happened, which the publication's
	// first write clears (staleClaims).
	staleClaims []landingClaim
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
	l := Landing{BaseRef: req.BaseRef}
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
		// What an earlier attempt of this run left for the owner is reported all the same.
		if err == nil {
			faults, rerr := t.reconcileOwner(ctx, s, req)
			if rerr != nil {
				return p, rerr
			}
			p.stored.Faults = append(p.stored.Faults, faults...)
		}
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
			faults, rerr := t.reconcileOwner(ctx, s, req)
			if rerr != nil {
				return p, rerr
			}
			p.landing.Faults = append(p.landing.Faults, faults...)
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
		if !rewritten && !p.recovered {
			err = s.ReadTx(ctx, func(r *store.Reader) error {
				h, err := ReadIdentityHistory(r)
				if err != nil {
					return err
				}
				// History observation is not identity certification: a commit
				// can arrive between the loop's two reads. Check from both
				// the durable checked tip and this request's observed tip.
				checked := h.Tip
				h, err = t.CheckIdentities(r, h, checked, p.observationTip)
				if err == nil && req.ObservedTrunk != checked {
					h, err = t.CheckIdentities(r, h, req.ObservedTrunk, p.observationTip)
				}
				if err == nil && len(h.Pending) != 0 {
					p.landing.Result, p.landing.Reason = ResultRefused, ReasonForeignIdentity
				}
				return err
			})
			if err != nil {
				return p, err
			}
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
		if p.squash.Empty {
			p.landing.Result, p.landing.Reason = ResultRefused, ReasonAlreadyOnTrunk
		} else if p.squash.Conflicted() {
			p.landing.Result, p.landing.ConflictPaths = ResultSquashConflict, p.squash.Conflicts
		} else if failures, berr := t.buildFailures(req, p.landing.GatedTree, p.squash.Tree); berr != nil {
			return p, berr
		} else if len(failures) > 0 {
			p.landing.Result, p.landing.ConflictPaths = ResultSquashConflict, failures
		} else if p.landing.Landed() {
			p.landing.LandedTree = p.squash.Tree
			if p.plan, err = t.PlanOwner(req.BaseRef, req.RunID, trunkBefore, p.squash.Commit); err != nil {
				return p, err
			}
			p.landing.Faults = append(p.landing.Faults, p.plan.faults()...)
			if held := p.plan.Held(); len(held) > 0 {
				p.landing.Result, p.landing.Reason = ResultRefused, ReasonTrunkUncommitted
				p.landing.ConflictPaths = held
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
	if p.staleClaims, err = t.staleClaims(ctx, s); err != nil {
		return p, err
	}
	return p, ctx.Err()
}

// publishLanding is the landing's publication, in three steps, and no store write is open
// across any git command or file in it.
//
//  1. A short write claims the landing (claimLanding): it rechecks what preparation read from
//     the store — the item is still landable, no receipt was recorded meanwhile, the trunk
//     history row is unchanged — and inserts a landing_claims row. While that row stands,
//     `tillr-item close` and a move to resolved-elsewhere refuse the item, which is what the
//     write lock used to exclude for the length of the move.
//  2. With nothing held, the trunk is reread and moved (Trunk.MoveOwner), the fork cache is
//     refreshed and the trunk-history refs are pinned. land.lock keeps every other lander out
//     for all of it, and the move itself is guarded by the tip it was decided against.
//  3. A short write records the receipt and deletes the claim (recordPublished), so a
//     reader sees the claim or the receipt and never neither.
//
// A kill between the steps is finished by the next attempt of the same run: after step 1
// the marker ref (refs/tillr/landed/<run>) names a commit the trunk does not carry, so the
// retry squashes again and its own claim replaces the old one; after the move in step 2 the
// trunk carries the marker's commit, so the retry recovers it — moves nothing, records the
// landing once, and deletes the claim in that same receipt write. A claim whose commit no
// trunk carries is cleared by the next landing of any run (staleClaims), because land.lock
// proves no landing is in flight.
//
// A non-landing (a refusal, a squash conflict) moves nothing, so it claims nothing: its
// history pins run with nothing held and its one write records the row.
func publishLanding(ctx context.Context, s *store.Store, t *Trunk, p preparedLanding, req LandingRequest, pub *publication) (Landing, error) {
	landing := p.landing
	if err := checkTips(t, p, req); err != nil {
		return Landing{}, err
	}
	claimed := false
	if landing.Landed() {
		var err error
		if landing, claimed, err = claimLanding(ctx, s, p, req, landing); err != nil {
			return Landing{}, err
		}
	}
	if OwnerHooks.Publishing != nil {
		OwnerHooks.Publishing()
	}
	if claimed {
		if err := ctx.Err(); err != nil {
			return Landing{}, errors.Join(err, t.abandonClaim(ctx, s, p, req, landing))
		}
		// A recovered landing's move already happened: its row is written and nothing moves,
		// so the history keeps the tip read under land.lock.
		if !p.recovered {
			req.observation.note("trunk-move")
			install, err := t.MoveOwner(p.plan, req.BaseRef, p.squash.Commit, p.tip)
			if err != nil {
				return Landing{}, errors.Join(err, t.abandonClaim(ctx, s, p, req, landing))
			}
			pub.install = install
			landing.TrunkAfter = p.squash.Commit
			if p.history != nil && PatchRef(req.BaseRef) == "" {
				p.history.observed = p.squash.Commit
			}
		}
		if PatchRef(req.BaseRef) != "" {
			cacheTip := landing.TrunkAfter
			if p.recovered {
				cacheTip = p.tip
			}
			// The delivery moved; a cache that could not follow it leaves the claim for the
			// retry, which recovers the move and refreshes the cache before its receipt.
			if err := t.RefreshForkCache(req.BaseRef, cacheTip); err != nil {
				return Landing{}, err
			}
		}
	}
	if p.history != nil {
		if err := t.pinHistory(p.history); err != nil {
			return Landing{}, err
		}
	}
	landing, saved, err := recordPublished(ctx, s, p, req, landing, claimed)
	if err != nil {
		return Landing{}, err
	}
	if saved && p.history.lost == "" {
		// The row no longer names a lost tip, so its retention ref goes, after the row and
		// with nothing held. A kill before this leaves one extra ref the next save removes.
		if err := t.dropLostHistoryRef(); err != nil {
			return landing, err
		}
	}
	return landing, nil
}

// checkTips rereads, with nothing held, the trunk tips preparation decided against.
func checkTips(t *Trunk, p preparedLanding, req LandingRequest) error {
	tip, err := t.Head(req.BaseRef)
	if err != nil {
		return err
	}
	if tip != p.tip {
		return Externalf("trunk %s changed during landing preparation: expected %s, got %s", req.BaseRef, p.tip, tip)
	}
	if p.observationRef != "" && p.observationRef != req.BaseRef {
		tip, err = t.Head(p.observationRef)
		if err != nil {
			return err
		}
		if tip != p.observationTip {
			return Externalf("observed trunk changed during landing preparation; no move made")
		}
	}
	return nil
}

// claimLanding is the publication's first write: it rechecks the store against what
// preparation read and claims the landing. It runs no git and touches no file. A landing
// whose item was closed meanwhile answers as the refusal it now is, with no claim.
func claimLanding(ctx context.Context, s *store.Store, p preparedLanding, req LandingRequest, landing Landing) (Landing, bool, error) {
	claimed := false
	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		claimed = false
		notLandable, err := itemNotLandable(tx, req.ItemID)
		if err != nil {
			return err
		}
		if notLandable && p.recovered {
			return Externalf("%s already carries %s, the landing of run %s, but item %s was closed while the receipt was being prepared: nothing was recorded or moved; decide by hand whether the item stays closed with its change on the trunk, or revert %s",
				req.BaseRef, landing.TrunkAfter, req.RunID, req.ItemID, landing.TrunkAfter)
		}
		if err := dropStaleClaims(tx, p.staleClaims); err != nil {
			return err
		}
		if notLandable {
			landing.Result, landing.Reason = ResultRefused, ReasonItemNotLandable
			landing.LandedTree, landing.ConflictPaths = "", nil
			return nil
		}
		if p.recovered {
			recorded, err := landingRecorded(tx, landing.TrunkAfter)
			if err != nil {
				return err
			}
			if recorded {
				return Externalf("the landing of run %s at %s was recorded during preparation; nothing was recorded twice", req.RunID, landing.TrunkAfter)
			}
		}
		if p.observationRef != "" {
			current, err := readHistory(tx)
			if err != nil {
				return err
			}
			if current != p.historyBefore {
				return Externalf("trunk history changed during landing preparation; no move made")
			}
		}
		commit := p.squash.Commit
		if p.recovered {
			commit = landing.TrunkAfter
		}
		if _, err := tx.Exec(`INSERT INTO landing_claims (run_id, item_id, base_ref, head_sha, commit_sha, trunk_before, claimed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(run_id) DO UPDATE SET item_id = excluded.item_id, base_ref = excluded.base_ref,
			  head_sha = excluded.head_sha, commit_sha = excluded.commit_sha,
			  trunk_before = excluded.trunk_before, claimed_at = excluded.claimed_at`,
			req.RunID, req.ItemID, req.BaseRef, req.HeadSHA, commit, p.tip, req.At.UnixMilli()); err != nil {
			return Internalf("claiming the landing of run %s: %v", req.RunID, err)
		}
		claimed = true
		return nil
	})
	if err != nil {
		return Landing{}, false, err
	}
	return landing, claimed, nil
}

// recordPublished is the publication's last write: the receipt, the history row and the
// claim's removal, in one transaction. It runs no git and touches no file. It answers
// whether it saved the history row.
//
// A claimed landing has published: its receipt is written whatever changed meanwhile,
// because the trunk carries the change. A trunk history row another observer rewrote since
// preparation is that observer's and is left as it is; the next landing or observation
// reads it again. An unclaimed landing moved nothing, so a changed history still refuses it.
func recordPublished(ctx context.Context, s *store.Store, p preparedLanding, req LandingRequest, landing Landing, claimed bool) (Landing, bool, error) {
	var answer Landing
	saved := false
	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		recorded, saveHistory := landing, p.history != nil
		saved = false
		if !claimed {
			if err := dropStaleClaims(tx, p.staleClaims); err != nil {
				return err
			}
			notLandable, err := itemNotLandable(tx, req.ItemID)
			if err != nil {
				return err
			}
			if notLandable {
				recorded.Result, recorded.Reason = ResultRefused, ReasonItemNotLandable
				recorded.LandedTree, recorded.ConflictPaths = "", nil
				saveHistory = false
			}
		}
		if saveHistory && p.observationRef != "" {
			current, err := readHistory(tx)
			if err != nil {
				return err
			}
			if current != p.historyBefore {
				if !claimed {
					return Externalf("trunk history changed during landing preparation; no move made")
				}
				saveHistory = false
			}
		}
		if saveHistory {
			if err := writeHistoryRow(tx, p.history); err != nil {
				return err
			}
			saved = true
		}
		var err error
		if answer, err = recordLanding(ctx, tx, recorded, req.At); err != nil {
			return err
		}
		if claimed {
			if _, err := tx.Exec(`DELETE FROM landing_claims WHERE run_id = ?`, req.RunID); err != nil {
				return Internalf("releasing the claim of run %s: %v", req.RunID, err)
			}
		}
		return nil
	})
	if err != nil {
		return Landing{}, false, err
	}
	return answer, saved, nil
}

// abandonClaim releases this run's claim after a publication that failed before or during
// the move, when the trunk does not carry the claimed commit. The check is git, so it runs
// with nothing held; the release is its own short write. A trunk that carries the commit
// keeps the claim, because the change is published and the retry records it.
func (t *Trunk) abandonClaim(ctx context.Context, s *store.Store, p preparedLanding, req LandingRequest, landing Landing) error {
	cleanup := *t
	cleanup.ctx = context.WithoutCancel(ctx)
	commit := p.squash.Commit
	if p.recovered {
		commit = landing.TrunkAfter
	}
	if commit == "" {
		return nil
	}
	tip, err := cleanup.Head(req.BaseRef)
	if err != nil {
		return err
	}
	published, err := cleanup.historyAncestor(commit, tip)
	if err != nil || published {
		return err
	}
	return s.WriteTx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		_, err := tx.Exec(`DELETE FROM landing_claims WHERE run_id = ? AND commit_sha = ?`, req.RunID, commit)
		return err
	})
}

// landingClaim is one landing_claims row: a landing that began publishing and has not yet
// recorded its receipt.
type landingClaim struct{ runID, baseRef, commit string }

// staleClaims answers the claims no landing will finish: under land.lock no other landing is
// in flight, so a claim whose commit its trunk does not carry is one whose move never
// happened. A claim whose commit is on the trunk is not stale — the change is published and
// its run's retry records it — so it stays, and the item stays unclosable, which is true.
// The reads are a read transaction and git, with no write open.
func (t *Trunk) staleClaims(ctx context.Context, s *store.Store) ([]landingClaim, error) {
	var claims []landingClaim
	err := s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT run_id, base_ref, commit_sha FROM landing_claims ORDER BY run_id`)
		if err != nil {
			return Internalf("reading the landing claims: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var c landingClaim
			if err := rows.Scan(&c.runID, &c.baseRef, &c.commit); err != nil {
				return Internalf("reading a landing claim: %v", err)
			}
			claims = append(claims, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	var stale []landingClaim
	for _, c := range claims {
		tip, err := t.Head(c.baseRef)
		if err != nil {
			return nil, err
		}
		published, err := t.historyAncestor(c.commit, tip)
		if err != nil {
			return nil, err
		}
		if !published {
			stale = append(stale, c)
		}
	}
	return stale, nil
}

// dropStaleClaims deletes the claims staleClaims found, each only if it still names the
// commit it was judged by.
func dropStaleClaims(tx *store.Tx, stale []landingClaim) error {
	for _, c := range stale {
		if _, err := tx.Exec(`DELETE FROM landing_claims WHERE run_id = ? AND commit_sha = ?`, c.runID, c.commit); err != nil {
			return Internalf("clearing the stale claim of run %s: %v", c.runID, err)
		}
	}
	return nil
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
	case VerdictPass, VerdictPassInherited:
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

// reconcileOwner reports what an earlier attempt of this run left for the owner and records, once,
// the fault events that never reached the store: a surviving recovery copy that no longer matches the
// disk, and a saved-bytes ref. It runs on every path that answers without a fresh install, and
// every failure it meets is returned: an answer that could not make its faults durable is not a
// clean one.
func (t *Trunk) reconcileOwner(ctx context.Context, s *store.Store, req LandingRequest) ([]string, error) {
	type artifact struct{ ref, text string }
	var found []artifact
	worktree, err := t.WorktreeFor(req.BaseRef)
	if err != nil {
		return nil, err
	}
	if worktree != "" {
		stale, err := t.settleRecovery(worktree)
		if err != nil {
			return nil, err
		}
		for _, f := range stale {
			found = append(found, artifact{f.Ref, f.Text})
		}
	}
	refs, described, err := t.pendingSaved(req.RunID)
	if err != nil {
		return nil, err
	}
	for i, ref := range refs {
		found = append(found, artifact{ref, described[i]})
	}
	if len(found) == 0 {
		return nil, nil
	}
	events, err := s.Events(ctx, 0)
	if err != nil {
		return nil, Externalf("reading the fault events: %v", err)
	}
	var faults []string
	for _, a := range found {
		faults = append(faults, a.text)
		recorded := false
		for _, e := range events {
			if e.Kind == FaultKind && strings.Contains(string(e.Payload), a.ref) {
				recorded = true
			}
		}
		if !recorded {
			if err := recordFault(ctx, s, req, "", []string{a.text}); err != nil {
				return faults, Externalf("the fault for %s could not be recorded: %v", a.ref, err)
			}
		}
	}
	return faults, nil
}

// buildFailures is the build check of a squash that is not the tree the gate judged. A
// squash that is the gated tree needs none: the gate built exactly that tree.
func (t *Trunk) buildFailures(req LandingRequest, gated, squashed string) ([]string, error) {
	if gated == squashed {
		return nil, nil
	}
	return t.CheckBuild(req.BaseSHA, req.HeadSHA, squashed)
}
