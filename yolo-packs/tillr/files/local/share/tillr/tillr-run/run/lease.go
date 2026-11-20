// Architecture and invariants: docs/reference/tillr-drain-successor.md
package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// LeaseGraceMS is the fixed grace a lease carries past the run's own timeout: the time
// the kill and the terminal record write are allowed before the run's lease is treated
// as expired (design §5.6). It is fixed rather than configured because a run's timeout
// is its own and the grace is not: two runs with the same timeout must be reaped by the
// same rule, whether or not they were dispatched by the same policy.
const LeaseGraceMS = 300_000

// LaneStateIdle is the state a released lane is in: free for the next run, its worktree
// and everything else about it kept (design §5.6, "the lane itself persists across its
// runs").
const LaneStateIdle = "idle"

// The release result, the release refusal reasons, and the reaper's own reason.
const (
	// ReleaseReleased is a lane this release freed.
	ReleaseReleased = "released"
	// ReleaseRefused is a lane this release declined to free: nothing was written.
	ReleaseRefused = "refused"

	// ReasonLeaseExpired is the reason the reaper records. It is the reaper's own string
	// rather than a caller's, so a reaped lane is tellable from an operator's release in
	// the log without reading who ran what.
	ReasonLeaseExpired = "lease-expired"
	// ReasonLeaseUnexpired is a lane whose lease has not run out. It is refused because
	// the longest an operator ever waits out a wedged lane is one timeout plus the grace
	// (design §5.6), and because a live lease belongs to a run that may be writing its
	// terminal record right now.
	ReasonLeaseUnexpired = "lease-unexpired"
	// ReasonLaneFree is a lane holding no lease: the release already happened, and this
	// release wrote nothing.
	ReasonLaneFree = "lane-free"
	// ReasonLaneUnknown is a lane the store does not hold.
	ReasonLaneUnknown = "lane-unknown"
)

// Release is the run.release payload: one lane freed, the run whose lease it held, and
// the reason it was freed — or the release that was refused and why.
//
// Result is what the face decided, in land's vocabulary (§5.4's landed/isolated/refused
// read the same way): a refusal is a completed negative answer with a registered reason,
// not an error. Reason is the register's string on a refusal and the caller's own words
// on a release, which is why a gate on the reason a caller supplies cannot be the same
// check as the reason this utility chose.
//
// TerminalRecord says a terminal run.record was written by this release: the lane's run
// had left no record of its own, so the release closed it rather than merely unleased it.
// Without that field a reaped run and a run whose record the reaper only tidied around
// would be the same record.
type Release struct {
	LaneID         string `json:"lane_id"`
	RunID          string `json:"run_id,omitempty"`
	LeasedUntil    string `json:"leased_until,omitempty"`
	ReleasedAt     string `json:"released_at"`
	Result         string `json:"result"`
	Reason         string `json:"reason"`
	TerminalRecord bool   `json:"terminal_record"`
}

// ClaimLease writes a lane's lease in the caller's transaction: the lane is allocated to
// runID, leased until the run's start plus its timeout plus the fixed grace — or, for a
// run with no wall, leased with `leased_until = NULL`, an expiry no clock will ever reach.
//
// It is the claim half of design §5.6's lease, and it belongs in the same transaction as
// the record of the run's start — a lease written in one transaction and a start written
// in another could disagree about whether the lane is held at all. The clock is the
// store host's, Unix milliseconds UTC, the same spelling as runs.started_at.
//
// `NULL` is a real lease, not a missing one: `expiredLeases` skips it, `activeLeases` and
// `readLanes` hold it, `Loop.planNothing` stops with `StopLanesLeased` naming
// `tillr run release <lane>`, and `ReleaseLane` frees it immediately. So an unbounded run
// still holds its lane, and the operator still has one command to take it back (design
// D-RW1).
//
// A negative timeout is refused here as well as at the request: a wall cannot run
// backwards, and a request naming one is a staging mistake rather than a choice.
func ClaimLease(ctx context.Context, tx *store.Tx, req Request, laneID string, startedAt time.Time) (int64, error) {
	if laneID == "" || req.RunID == "" {
		return 0, Internalf("a claim needs a lane id and a run id, got %q / %q", laneID, req.RunID)
	}
	if req.TimeoutMS < 0 {
		return 0, Usagef("run %q names timeout_ms %d: a lane is leased for the run's timeout plus %d ms of grace, and 0 is an unbounded run whose lease carries no expiry", req.RunID, req.TimeoutMS, LeaseGraceMS)
	}
	// A nil expiry is SQL NULL: the lane is leased and no clock reaches it, and the
	// reaper's `leased_until IS NOT NULL` is what tells the two shapes apart.
	var leasedUntil *int64
	if req.TimeoutMS > 0 {
		until := startedAt.Add(time.Duration(req.TimeoutMS)*time.Millisecond + time.Duration(LeaseGraceMS)*time.Millisecond).UnixMilli()
		leasedUntil = &until
	}
	leaseID := req.RunID
	if req.ReadOnlyGroup != "" {
		if req.ItemID == "" || req.NodeID == "" {
			return 0, Usagef("a read-only group needs an item and node id")
		}
		leaseID, leasedUntil = req.ReadOnlyGroup, nil
		var live int
		if err := tx.Reader().QueryRow(`SELECT COUNT(*) FROM lanes l WHERE lane_id=? AND state=? AND run_id!=? AND NOT EXISTS(SELECT 1 FROM read_only_runs m WHERE m.run_id=l.run_id AND m.group_id=? AND m.item_id=? AND m.lane_id=l.lane_id)`, laneID, LaneStateLeased, leaseID, leaseID, req.ItemID).Scan(&live); err != nil {
			return 0, err
		}
		if live != 0 {
			return 0, Usagef("lane is leased outside the read-only group")
		}
		var conflicting int
		if err := tx.Reader().QueryRow(`SELECT COUNT(*) FROM read_only_runs WHERE group_id=? AND (item_id!=? OR lane_id!=?)`, leaseID, req.ItemID, laneID).Scan(&conflicting); err != nil {
			return 0, err
		}
		if conflicting != 0 {
			return 0, Usagef("read-only group belongs to another item or lane")
		}
		if err := tx.Reader().QueryRow(`SELECT COUNT(*) FROM read_only_runs WHERE run_id=? AND (group_id!=? OR item_id!=? OR lane_id!=?)`, req.RunID, leaseID, req.ItemID, laneID).Scan(&conflicting); err != nil {
			return 0, err
		}
		if conflicting != 0 {
			return 0, Usagef("run already belongs to another read-only group")
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO lanes (lane_id, state, run_id, leased_until) VALUES (?, ?, ?, ?)
		 ON CONFLICT(lane_id) DO UPDATE SET
		   state = excluded.state, run_id = excluded.run_id, leased_until = excluded.leased_until`,
		laneID, LaneStateLeased, leaseID, leasedUntil,
	); err != nil {
		return 0, Internalf("leasing lane %q to %q: %v", laneID, req.RunID, err)
	}
	memberAllocated := false
	if req.ReadOnlyGroup != "" {
		result, err := tx.Exec(`INSERT INTO read_only_runs(run_id,group_id,item_id,lane_id) VALUES(?,?,?,?) ON CONFLICT(run_id) DO NOTHING`, req.RunID, req.ReadOnlyGroup, req.ItemID, laneID)
		if err != nil {
			return 0, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		// Membership and allocation commit together, so an existing member
		// already has a claim; no scan through the lane's history is needed.
		memberAllocated = n == 0
		if req.Resume {
			if _, err := tx.Exec(`UPDATE read_only_runs SET settled=0 WHERE run_id=?`, req.RunID); err != nil {
				return 0, err
			}
		}
	}
	// A drain claims before dispatching exec. Refreshing that same claim must
	// not record a second attempt; continuations likewise only refresh the lease.
	var prior []byte
	priorErr := tx.Reader().QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_type = 'lane' AND object_id = ? ORDER BY seq DESC LIMIT 1`, AllocateKind, laneID).Scan(&prior)
	if priorErr != nil && priorErr != sql.ErrNoRows {
		return 0, Internalf("reading prior allocation: %v", priorErr)
	}
	var allocated claim
	if len(prior) > 0 {
		if err := json.Unmarshal(prior, &allocated); err != nil {
			return 0, Internalf("reading allocation payload: %v", err)
		}
	}
	if memberAllocated || req.Resume || allocated.RunID == req.RunID {
		if leasedUntil == nil {
			return 0, nil
		}
		return *leasedUntil, nil
	}
	// The start is recorded in the log as well as on the lane: a run that dies without a
	// terminal record still has to be answerable — its item and the moment it started are
	// what the reaper writes its record from, and neither is on the runs row, which the
	// terminal write creates.
	body, err := json.Marshal(claim{
		RunID:         req.RunID,
		ItemID:        req.ItemID,
		Backend:       req.Backend,
		LaneID:        laneID,
		StartedAt:     startedAt.UTC().Format(time.RFC3339),
		LeasedUntil:   leasedUntil,
		CorrelationID: req.CorrelationID,
	})
	if err != nil {
		return 0, Internalf("encoding the %s event: %v", AllocateKind, err)
	}
	if _, err := tx.Append(event.Event{
		Kind:       AllocateKind,
		ObjectType: "lane",
		ObjectID:   &laneID,
		Payload:    body,
		At:         startedAt,
	}); err != nil {
		return 0, Internalf("appending the %s event: %v", AllocateKind, err)
	}
	if leasedUntil == nil {
		return 0, nil
	}
	return *leasedUntil, nil
}

// claim is the run.allocate event's payload: the run's start as the claim recorded it,
// with the three fields a terminal record cannot be written without — the run it is
// about (run_id), the work it was dispatched for (item_id), and the adapter that
// produced it (backend).
type claim struct {
	RunID     string `json:"run_id"`
	ItemID    string `json:"item_id,omitempty"`
	Backend   string `json:"backend"`
	LaneID    string `json:"lane_id"`
	StartedAt string `json:"started_at"`
	// CorrelationID is the tick the request named, copied from the request so the
	// run.allocate event carries the same key as the run.request record that dispatched
	// it. A request no tick emitted claims no correlation, and the field is then absent.
	CorrelationID string `json:"correlation_id,omitempty"`
	// LeasedUntil is the lease's expiry in Unix milliseconds, absent for a run with no
	// wall: `NULL` on the lane is what an unbounded lease is, and a 0 here would read as
	// an expiry at the epoch. `closeRun` reads the absence to stamp a manual release with
	// the release's own time rather than 1970.
	LeasedUntil *int64 `json:"leased_until,omitempty"`
}

// ReapExpiredLeases frees every lane whose lease has expired at now, in the caller's
// transaction, and answers what it did.
//
// It reaps the whole expired set in one transaction rather than one per lane: a reaper
// that committed lane by lane would leave a store where some expired lanes are free and
// others are not, which is precisely the state the planner must never see — `drain once`
// calls this before it plans, and its tick is one transaction because of it. The clock is
// the caller's — the tick's injected now against the store host's own times — so a tick
// is reproducible.
//
// A lane is expired when it is leased and its leased_until is at or before now (design
// §5.5). A lane holding no lease at all is not this face's: it has no expiry to judge,
// and `ReleaseLane` is the operator's way to free one a crashed run left as `leased` with no
// lease written.
func ReapExpiredLeases(ctx context.Context, tx *store.Tx, now time.Time) ([]Release, error) {
	expired, err := expiredLeases(tx.Reader(), now)
	if err != nil {
		return nil, err
	}
	releases := make([]Release, 0, len(expired))
	for _, lane := range expired {
		release, err := freeLane(ctx, tx, lane, ReasonLeaseExpired, now)
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	return releases, nil
}

// ReleaseLaneInTx closes a proven-dead group's members in the caller's ownership transaction.
func ReleaseLaneInTx(ctx context.Context, tx *store.Tx, laneID, reason string, now time.Time) (Release, error) {
	lane, found, err := readLane(tx.Reader(), laneID)
	if err != nil {
		return Release{}, err
	}
	if !found || lane.state != LaneStateLeased {
		return refused(laneID, ReasonLaneFree, now), nil
	}
	return freeLane(ctx, tx, lane, reason, now)
}

// ReleaseLane is design §5.6's standalone release face: it frees one lane and records
// why.
//
// A reason is required. A lane freed without one is a lane nobody can account for — the
// release event would say a lane was taken back from a run and not who took it or why —
// so the face refuses an empty reason before it reads the store.
//
// It refuses two other shapes by name: a lane whose lease has not run out, and a lane
// holding no lease at all. Both are recorded answers rather than errors, so a caller
// reads the reason it got instead of parsing a diagnostic.
func ReleaseLane(ctx context.Context, s *store.Store, laneID, reason string, now time.Time) (Release, error) {
	if strings.TrimSpace(reason) == "" {
		return Release{}, Usagef("releasing lane %q needs a reason: a lane freed without one is a lane nobody can account for", laneID)
	}
	var release Release
	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		lane, found, err := readLane(tx.Reader(), laneID)
		if err != nil {
			return err
		}
		switch {
		case !found:
			release = refused(laneID, ReasonLaneUnknown, now)
			return nil
		case lane.state != LaneStateLeased:
			release = refused(laneID, ReasonLaneFree, now)
			return nil
		case lane.leasedUntil != nil && *lane.leasedUntil > now.UnixMilli():
			// The lease is live: the run that holds it may be writing its own terminal
			// record, and the longest anyone waits it out is its timeout plus the grace.
			release = refused(laneID, ReasonLeaseUnexpired, now)
			return nil
		}
		release, err = freeLane(ctx, tx, lane, reason, now)
		return err
	})
	if err != nil {
		return Release{}, err
	}
	return release, nil
}

// refused is a release the face declined to make: a completed negative answer.
func refused(laneID, reason string, now time.Time) Release {
	return Release{
		LaneID:     laneID,
		ReleasedAt: now.UTC().Format(time.RFC3339),
		Result:     ReleaseRefused,
		Reason:     reason,
	}
}

// lease is one lane row as the lease faces read it.
type lease struct {
	laneID      string
	state       string
	runID       *string
	leasedUntil *int64
}

// expiredLeases answers the lanes whose lease is at or before now, in lane-id order so
// two reaps over one store do the same work in the same order.
func expiredLeases(r *store.Reader, now time.Time) ([]lease, error) {
	rows, err := r.Query(
		`SELECT lane_id, state, run_id, leased_until FROM lanes
		  WHERE state = ? AND leased_until IS NOT NULL AND leased_until <= ?
		  ORDER BY lane_id`,
		LaneStateLeased, now.UnixMilli(),
	)
	if err != nil {
		return nil, readFailure(err)
	}
	defer rows.Close()
	lanes := []lease{}
	for rows.Next() {
		var (
			lane lease
			run  sql.NullString
			from sql.NullInt64
		)
		if err := rows.Scan(&lane.laneID, &lane.state, &run, &from); err != nil {
			return nil, readFailure(err)
		}
		lane.runID, lane.leasedUntil = nullableString(run), nullableInt(from)
		lanes = append(lanes, lane)
	}
	if err := rows.Err(); err != nil {
		return nil, readFailure(err)
	}
	return lanes, nil
}

// readLane answers one lane row, and whether the store holds it.
func readLane(r *store.Reader, laneID string) (lease, bool, error) {
	var (
		l    lease
		run  sql.NullString
		from sql.NullInt64
	)
	err := r.QueryRow(
		`SELECT lane_id, state, run_id, leased_until FROM lanes WHERE lane_id = ?`, laneID,
	).Scan(&l.laneID, &l.state, &run, &from)
	if err == sql.ErrNoRows {
		return lease{laneID: laneID}, false, nil
	}
	if err != nil {
		return lease{}, false, readFailure(err)
	}
	l.runID, l.leasedUntil = nullableString(run), nullableInt(from)
	return l, true, nil
}

// nullableString and nullableInt keep "absent" and "empty" apart: a lane with no run and
// a lease with no expiry are answers, not zeroes.
func nullableString(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullableInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

// releaseLane frees one lane in the caller's transaction, closing its run if the run
// left no record of its own, and appends the release event. It is the one implementation
// of the release: the reaper and the standalone face differ in which lanes they reach,
// not in what releasing one means.
//
// Closing the run is what keeps a reaped run from looking like a run that finished: the
// record it writes is a `timed-out` run.record — the state §5.6 already gives a run that
// outlived its bound, written through the same Apply every other record is — and the
// release event beside it says a release, not the run, ended it. A run that already has
// a row is left alone: its own record is the better account, and the release then only
// unleases the lane.
func freeLane(ctx context.Context, tx *store.Tx, lane lease, reason string, now time.Time) (Release, error) {
	release := Release{
		LaneID:      lane.laneID,
		ReleasedAt:  now.UTC().Format(time.RFC3339),
		Result:      ReleaseReleased,
		Reason:      reason,
		LeasedUntil: formatMillis(lane.leasedUntil),
	}
	if lane.runID != nil {
		release.RunID = *lane.runID
	}
	if lane.runID != nil {
		rows, err := tx.Reader().Query(`SELECT run_id FROM read_only_runs WHERE group_id=? AND settled=0`, *lane.runID)
		if err != nil {
			return Release{}, err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return Release{}, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return Release{}, err
		}
		for _, id := range ids {
			member := lane
			member.runID = &id
			if _, err = closeRun(ctx, tx, member, now); err != nil {
				return Release{}, err
			}
		}
		// A continued member can retain an earlier terminal row, so closeRun
		// may have nothing to write. Releasing the group still settles every
		// abandoned membership before any replacement reader claims its lane.
		if _, err := tx.Exec(`UPDATE read_only_runs SET settled=1 WHERE group_id=? AND lane_id=?`, *lane.runID, lane.laneID); err != nil {
			return Release{}, err
		}
		closed, err := closeRun(ctx, tx, lane, now)
		if err != nil {
			return Release{}, err
		}
		release.TerminalRecord = closed
	}
	// The clear is guarded by the run the lease was read with, so a release can only ever
	// free the lane it decided about: a lane another run has since claimed is left alone,
	// which is the same rule the terminal record's own release follows.
	if _, err := tx.Exec(
		`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL
		  WHERE lane_id = ? AND state = ? AND run_id IS ?`,
		LaneStateIdle, lane.laneID, LaneStateLeased, lane.runID,
	); err != nil {
		return Release{}, Internalf("releasing lane %q: %v", lane.laneID, err)
	}
	body, err := json.Marshal(release)
	if err != nil {
		return Release{}, Internalf("encoding the release of lane %q: %v", lane.laneID, err)
	}
	if _, err := tx.Append(event.Event{
		Kind:       ReleaseKind,
		ObjectType: "lane",
		ObjectID:   &lane.laneID,
		Payload:    body,
		At:         now,
	}); err != nil {
		return Release{}, Internalf("appending the %s event: %v", ReleaseKind, err)
	}
	return release, nil
}

// closeRun writes the terminal record of a run that left none, and answers whether it
// wrote one.
//
// The record's two times are the claim's, not the reaper's: it started when the claim
// says it started, and the last instant it was entitled to be running is the lease's
// expiry. Stamping the reaper's now as the finish would claim the run ran on until a
// tick happened to notice it, which is a fact about the reaper rather than about the run.
//
// A run the claim does not describe — a lane seeded as leased by hand, or one written by
// a binary that predates the claim — is not closed: there is no start to state, and a
// record invented around an absence is worse than none. The release event is still
// written, so the lane is freed and the freeing is accounted for.
func closeRun(ctx context.Context, tx *store.Tx, lane lease, now time.Time) (bool, error) {
	runID := *lane.runID
	var existing int
	if err := tx.QueryRow(`SELECT count(*) FROM runs WHERE run_id = ?`, runID).Scan(&existing); err != nil {
		return false, Internalf("reading run %q: %v", runID, err)
	}
	if existing > 0 {
		return false, nil
	}
	claimed, found, err := readClaim(tx.Reader(), lane.laneID, runID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	started, err := time.Parse(time.RFC3339, claimed.StartedAt)
	if err != nil {
		return false, Internalf("the claim of run %q carries started_at %q: %v", runID, claimed.StartedAt, err)
	}
	// An unbounded run has no expiry to stamp, so the release's own clock is the last
	// instant it can honestly be said to have been running. `time.UnixMilli(0)` would
	// claim every unbounded run finished at the Unix epoch.
	finished := now.UTC()
	if claimed.LeasedUntil != nil && *claimed.LeasedUntil > 0 {
		finished = time.UnixMilli(*claimed.LeasedUntil).UTC()
	}
	payload := Record{
		RunID:      runID,
		ItemID:     claimed.ItemID,
		LaneID:     lane.laneID,
		Backend:    claimed.Backend,
		State:      StateTimedOut,
		EndCause:   EndLeaseReclaimed,
		StartedAt:  started.UTC().Format(time.RFC3339),
		FinishedAt: finished.Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, Internalf("encoding the terminal record of run %q: %v", runID, err)
	}
	_, err = Apply(ctx, tx, record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          RecordKind,
		ID:            "run-record-" + runID,
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     now.UTC(),
		Subject:       &record.Subject{Kind: "run", ID: runID},
		Payload:       body,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// readClaim answers the start the claim recorded for a lane's run.
//
// It reads the log rather than a row because that is where the start is: the runs row is
// written by the terminal record, and a run that never wrote one has no row to ask. The
// event is the run's own claim — the run.allocate event whose payload names the run and
// the lane — so a lane claimed twice answers its latest claim.
func readClaim(r *store.Reader, laneID, runID string) (claim, bool, error) {
	events, err := r.EventsOfKind(0, AllocateKind)
	if err != nil {
		return claim{}, false, readFailure(err)
	}
	var latest claim
	var at int64
	for _, e := range events {
		if e.Kind != AllocateKind || e.ObjectType != "lane" || e.ObjectID == nil || *e.ObjectID != laneID {
			continue
		}
		var carried claim
		if err := json.Unmarshal(e.Payload, &carried); err != nil {
			return claim{}, false, Internalf("reading the %s event at seq %d: %v", AllocateKind, e.Seq, err)
		}
		if carried.RunID != runID {
			continue
		}
		if carried.StartedAt == "" {
			// A claim written before this face existed records the allocation and not the
			// start, so it cannot describe the run either.
			continue
		}
		if e.Seq >= at {
			at, latest = e.Seq, carried
		}
	}
	return latest, latest.RunID != "", nil
}

// formatMillis answers a stored Unix-millisecond time in the record's spelling, and an
// empty string for a lease that is not there.
func formatMillis(ms *int64) string {
	if ms == nil {
		return ""
	}
	return time.UnixMilli(*ms).UTC().Format(time.RFC3339)
}
