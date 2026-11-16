package land

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

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
	LandingID         string   `json:"landing_id"`
	ItemID            string   `json:"item_id"`
	RunID             string   `json:"run_id"`
	VerdictID         string   `json:"verdict_id"`
	Result            string   `json:"result"`
	Reason            string   `json:"reason"`
	BaseSHA           string   `json:"base_sha"`
	HeadSHA           string   `json:"head_sha"`
	TrunkBefore       string   `json:"trunk_before"`
	TrunkAfter        string   `json:"trunk_after"`
	GatedTree         string   `json:"gated_tree"`
	LandedTree        string   `json:"landed_tree"`
	ConflictPaths     []string `json:"conflict_paths"`
	GateSkippedReason string   `json:"gate_skipped_reason"`
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
	ItemID  string
	RunID   string
	BaseRef string
	BaseSHA string
	HeadSHA string
	Verdict Verdict
	// At is the clock the landing records and the squash dates its commit with.
	At time.Time
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
// The land lock is held for the swap and nothing else. The gate ran before this function
// was called — in another process, in the drain's case — so no lock spans it, and a tick
// that takes minutes to judge does not hold the trunk. Under the lock this function reads
// the trunk tip, squashes onto it, refuses a clash in the trunk's worktree, records the
// row, and moves the ref. A non-landing takes the same lock briefly to pin the head and
// write its row, so a head the gate saw and the landing refused survives a garbage
// collection.
//
// The squash is the landing: `git merge-tree --write-tree --merge-base=<base>` re-applies
// the change onto the tip, `git commit-tree` writes one commit with the repository's
// scope-checked identity and the head's own message, naming no item, and `Trunk.Move`
// fast-forwards the trunk onto it. A squash that conflicts is its own outcome, not a repair: the verdict
// stands, the head is pinned, and a repair lane reads the conflict paths from the row.
func Land(ctx context.Context, s *store.Store, t *Trunk, req LandingRequest) (Landing, error) {
	if err := checkLandingRequest(req); err != nil {
		return Landing{}, err
	}
	if err := t.HasCommit(req.BaseSHA); err != nil {
		return Landing{}, err
	}
	if err := t.HasCommit(req.HeadSHA); err != nil {
		return Landing{}, err
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
	defer lock.Release()

	trunkBefore, err := t.Head(req.BaseRef)
	if err != nil {
		return Landing{}, err
	}
	landing.TrunkBefore = trunkBefore
	landing.TrunkAfter = trunkBefore

	if !landing.Landed() {
		return pinAndRecord(ctx, s, t, landing, req)
	}

	squash, err := t.Squash(req.BaseSHA, trunkBefore, req.HeadSHA, req.At)
	if err != nil {
		return Landing{}, err
	}
	if squash.Conflicted() {
		landing.Result = ResultSquashConflict
		landing.ConflictPaths = squash.Conflicts
		return pinAndRecord(ctx, s, t, landing, req)
	}
	landing.LandedTree = squash.Tree
	clashing, err := t.ClashingPaths(req.BaseRef, trunkBefore, squash.Commit)
	if err != nil {
		return Landing{}, err
	}
	if len(clashing) > 0 {
		landing.Result = ResultRefused
		landing.Reason = ReasonTrunkUncommitted
		landing.ConflictPaths = clashing
		return pinAndRecord(ctx, s, t, landing, req)
	}
	if err := t.Move(req.BaseRef, squash.Commit, trunkBefore); err != nil {
		return Landing{}, err
	}
	landing.TrunkAfter = squash.Commit
	return recordLanding(ctx, s, landing, req.At)
}

// pinAndRecord pins the run's head and records a non-landing row. The pin is what makes a
// refusal and a squash conflict survive: without it the head is reachable only from the
// lane's branch, and a garbage collection between this process and the conflict-fixer or
// the person reading the row takes it. The caller holds the land lock, so the pin happens
// before the release that lets another writer in.
func pinAndRecord(ctx context.Context, s *store.Store, t *Trunk, landing Landing, req LandingRequest) (Landing, error) {
	if err := t.Pin(req.RunID, req.HeadSHA); err != nil {
		return Landing{}, err
	}
	return recordLanding(ctx, s, landing, req.At)
}

// recordLanding writes one landing row and appends its event in the same transaction, and
// answers the landing as stored. It is the one place a landing becomes state, so the
// landed and non-landed paths cannot disagree about what a recorded landing is.
func recordLanding(ctx context.Context, s *store.Store, landing Landing, at time.Time) (Landing, error) {
	conflicts, err := json.Marshal(landing.ConflictPaths)
	if err != nil {
		return Landing{}, Internalf("encoding the conflict paths of %q: %v", landing.HeadSHA, err)
	}
	if landing.ConflictPaths == nil {
		conflicts = []byte("[]")
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
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
	})
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
