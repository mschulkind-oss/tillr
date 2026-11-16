package run_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// leaseGrace is design §5.6's fixed grace, spelled here rather than read from the
// constant: a test that compared against run.LeaseGraceMS would pass for any value the
// constant held, which is the one thing the number must not be.
const leaseGrace = 300_000

// leaseFixture is one store a lease case works in, and the directory it and its lanes
// live under.
func leaseFixture(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := scratch(t)
	s, err := store.InitFixture(context.Background(), filepath.Join(dir, "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating the fixture store failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

// claim leases one lane through the claim the runner uses, in one transaction, and
// answers the lease it wrote.
func claim(t *testing.T, s *store.Store, laneID, runID string, startedAt time.Time, timeoutMS int) int64 {
	t.Helper()
	req := run.Request{
		RunID: runID, ItemID: "item_042", LaneID: laneID, Backend: "muse",
		Command: []string{"/bin/true"}, WorkingDir: ".", TimeoutMS: timeoutMS,
	}
	var leasedUntil int64
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var err error
		leasedUntil, err = run.ClaimLease(context.Background(), tx, req, laneID, startedAt)
		return err
	}); err != nil {
		t.Fatalf("claiming lane %q for run %q failed: %v", laneID, runID, err)
	}
	return leasedUntil
}

// reap runs the reaper as `drain once` does — inside one write transaction of the
// caller's, at the caller's clock.
func reap(t *testing.T, s *store.Store, now time.Time) []run.Release {
	t.Helper()
	var releases []run.Release
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var err error
		releases, err = run.ReapExpiredLeases(context.Background(), tx, now)
		return err
	}); err != nil {
		t.Fatalf("reaping at %s failed: %v", now.Format(time.RFC3339), err)
	}
	return releases
}

// laneState is one lane as the read face answers it.
type laneState struct {
	State       string  `json:"state"`
	RunID       *string `json:"run_id"`
	LeasedUntil *string `json:"leased_until"`
}

func lane(t *testing.T, s *store.Store, laneID string) (laneState, bool) {
	t.Helper()
	var lanes []struct {
		LaneID string `json:"lane_id"`
		laneState
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		env, err := run.LanesRecord(r)
		if err != nil {
			return err
		}
		var body struct {
			Lanes []struct {
				LaneID string `json:"lane_id"`
				laneState
			} `json:"lanes"`
		}
		if err := json.Unmarshal(env.Payload, &body); err != nil {
			return err
		}
		lanes = body.Lanes
		return nil
	}); err != nil {
		t.Fatalf("reading the lanes failed: %v", err)
	}
	for _, l := range lanes {
		if l.LaneID == laneID {
			return l.laneState, true
		}
	}
	return laneState{}, false
}

// claims answers the run.allocate events the store holds, through the substrate's event
// reader: the start a run's lease was written from is a record, not a row.
func claims(t *testing.T, s *store.Store) []struct {
	RunID         string `json:"run_id"`
	ItemID        string `json:"item_id"`
	Backend       string `json:"backend"`
	LaneID        string `json:"lane_id"`
	StartedAt     string `json:"started_at"`
	LeasedUntil   int64  `json:"leased_until"`
	CorrelationID string `json:"correlation_id"`
} {
	t.Helper()
	var found []struct {
		RunID         string `json:"run_id"`
		ItemID        string `json:"item_id"`
		Backend       string `json:"backend"`
		LaneID        string `json:"lane_id"`
		StartedAt     string `json:"started_at"`
		LeasedUntil   int64  `json:"leased_until"`
		CorrelationID string `json:"correlation_id"`
	}
	events, err := eventsOf(t, s)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	for _, e := range events {
		if e.Kind != run.AllocateKind {
			continue
		}
		var carried struct {
			RunID         string `json:"run_id"`
			ItemID        string `json:"item_id"`
			Backend       string `json:"backend"`
			LaneID        string `json:"lane_id"`
			StartedAt     string `json:"started_at"`
			LeasedUntil   int64  `json:"leased_until"`
			CorrelationID string `json:"correlation_id"`
		}
		if err := json.Unmarshal(e.Payload, &carried); err != nil {
			t.Fatalf("reading a %s event failed: %v", run.AllocateKind, err)
		}
		found = append(found, carried)
	}
	return found
}

// eventsOf answers the store's whole log through the substrate's reader, inside one read
// transaction so the events describe one moment.
func eventsOf(t *testing.T, s *store.Store) ([]event.Event, error) {
	t.Helper()
	var events []event.Event
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		events, err = r.Events(0)
		return err
	})
	return events, err
}

// kinds answers how many events of each kind the store holds.
func kinds(t *testing.T, s *store.Store) map[string]int {
	t.Helper()
	counts := map[string]int{}
	events, err := eventsOf(t, s)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	for _, e := range events {
		counts[e.Kind]++
	}
	return counts
}

// The claim is the lease: the lane is held for the run's own timeout plus the fixed
// grace, and the start it was computed from is recorded beside it. Two claims with
// different timeouts produce different leases, which is the whole of why a fixed TTL is
// refused — one TTL would have to reap a live long run or hold a dead short one.
func TestTheClaimLeasesTheLaneForTheRunsTimeoutPlusTheFixedGrace(t *testing.T) {
	s, _ := leaseFixture(t)
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

	short := claim(t, s, "lane_short", "run_short", start, 1_000)
	long := claim(t, s, "lane_long", "run_long", start, 600_000)

	if want := start.UnixMilli() + 1_000 + leaseGrace; short != want {
		t.Fatalf("a 1s timeout was leased until %d, want %d (start + timeout + %d grace)", short, want, leaseGrace)
	}
	if want := start.UnixMilli() + 600_000 + leaseGrace; long != want {
		t.Fatalf("a 600s timeout was leased until %d, want %d (start + timeout + %d grace)", long, want, leaseGrace)
	}
	if long-short != 599_000 {
		t.Fatalf("the two leases differ by %d ms: the lease is not the run's own timeout", long-short)
	}
	if held, _ := lane(t, s, "lane_short"); held.State != run.LaneStateLeased || held.LeasedUntil == nil {
		t.Fatalf("the claimed lane is %q with lease %v, want leased with an expiry", held.State, held.LeasedUntil)
	}
	// The start is on the log as well as the lane: a run that dies without a terminal
	// record is answered from here, and the runs row it would otherwise be read from
	// does not exist yet.
	recorded := claims(t, s)
	if len(recorded) != 2 {
		t.Fatalf("the store holds %d claim events, want 2", len(recorded))
	}
	if recorded[0].RunID != "run_short" || recorded[0].ItemID != "item_042" ||
		recorded[0].Backend != "muse" || recorded[0].StartedAt != "2026-11-12T09:00:00Z" ||
		recorded[0].LeasedUntil != short {
		t.Fatalf("the claim event reads %+v, want the request's run, item and backend and the start it was computed from", recorded[0])
	}
}

// A claim carries the correlation id the request named, so the run.allocate event joins to
// the run.request that dispatched it by key rather than by a search through payloads.
func TestTheClaimCarriesTheRequestsCorrelationID(t *testing.T) {
	s, _ := leaseFixture(t)
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	req := run.Request{
		RunID: "run_item_042", ItemID: "item_042", LaneID: "lane_001", Backend: "muse",
		Command: []string{"/bin/true"}, WorkingDir: ".", TimeoutMS: 60_000,
		CorrelationID: "tick-20261112T090000Z-run-item_042-20261112T090000Z",
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := run.ClaimLease(context.Background(), tx, req, "lane_001", start)
		return err
	}); err != nil {
		t.Fatalf("claiming the lane failed: %v", err)
	}
	recorded := claims(t, s)
	if len(recorded) != 1 {
		t.Fatalf("the store holds %d claim events, want 1", len(recorded))
	}
	if recorded[0].CorrelationID != req.CorrelationID {
		t.Fatalf("the claim carries correlation %q, want the request's %q",
			recorded[0].CorrelationID, req.CorrelationID)
	}
}

// A negative timeout is refused: a wall cannot run backwards, so the claim refuses it as
// well as the request. The refusal is before anything is written, so no caller can hold a
// lane with one.
func TestTheClaimRefusesANegativeTimeout(t *testing.T) {
	s, _ := leaseFixture(t)
	req := run.Request{
		RunID: "run_001", Backend: "muse", Command: []string{"/bin/true"},
		WorkingDir: ".", TimeoutMS: -1,
	}
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := run.ClaimLease(context.Background(), tx, req, "lane_001", time.Now().UTC())
		return err
	})
	if err == nil {
		t.Fatalf("a claim with timeout_ms -1 was accepted")
	}
	if code := run.ExitCode(err); code != 2 {
		t.Fatalf("a claim with timeout_ms -1 exits %d, want 2: %v", code, err)
	}
	if held, found := lane(t, s, "lane_001"); found {
		t.Fatalf("a refused claim left the lane %q", held.State)
	}
	if n := kinds(t, s)[run.AllocateKind]; n != 0 {
		t.Fatalf("a refused claim appended %d claim events", n)
	}
}

// An unbounded run holds its lane with `leased_until` NULL: no clock reaches the expiry,
// so the reaper never takes a run that is still working, and the operator releases it by
// hand. The claim answers 0 for that lease, which is the spelling of "no expiry" here.
func TestAnUnboundedClaimHoldsTheLaneWithNoExpiry(t *testing.T) {
	s, _ := leaseFixture(t)
	start := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	if leased := claim(t, s, "lane_001", "run_001", start, 0); leased != 0 {
		t.Fatalf("an unbounded claim answered lease %d, want 0 (no expiry)", leased)
	}
	held, found := lane(t, s, "lane_001")
	if !found {
		t.Fatalf("an unbounded claim left no lane row: an unbounded run still holds its lane")
	}
	if held.State != run.LaneStateLeased || held.LeasedUntil != nil {
		t.Fatalf("the unbounded lane reads state %q lease %v, want leased with no expiry", held.State, held.LeasedUntil)
	}
	// The claim event carries the absence too, so a reader of the log cannot mistake the
	// lane for one leased until the epoch.
	recorded := claims(t, s)
	if len(recorded) != 1 || recorded[0].LeasedUntil != 0 {
		t.Fatalf("the claim event reads %+v, want one claim with no expiry", recorded)
	}
	// The reaper skips it: a run with no wall is not expired by a clock.
	if releases := reap(t, s, start.Add(365*24*time.Hour)); len(releases) != 0 {
		t.Fatalf("the reaper freed an unbounded lease: %+v", releases)
	}
	if held, _ := lane(t, s, "lane_001"); held.State != run.LaneStateLeased {
		t.Fatalf("the reaper left the unbounded lane %q, want it held", held.State)
	}
}

// A manual release of an unbounded lane frees it immediately and stamps the release's own
// clock on the terminal record it writes. `time.UnixMilli(0)` would claim the run finished
// at the Unix epoch.
func TestReleasingAnUnboundedLaneStampsTheReleasesClock(t *testing.T) {
	s, _ := leaseFixture(t)
	start := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	claim(t, s, "lane_001", "run_001", start, 0)
	releasedAt := start.Add(90 * time.Minute)
	release, err := run.ReleaseLane(context.Background(), s, "lane_001", "the operator stopped it", releasedAt)
	if err != nil {
		t.Fatalf("releasing the unbounded lane failed: %v", err)
	}
	if release.Result != run.ReleaseReleased || release.LeasedUntil != "" {
		t.Fatalf("the release reads %+v, want released with no expiry", release)
	}
	if !release.TerminalRecord {
		t.Fatalf("the release wrote no terminal record for the unbounded run")
	}
	closed, found := storedRun(t, s, "run_001")
	if !found {
		t.Fatalf("the release wrote no run.record for the unbounded run")
	}
	if closed.State != run.StateTimedOut {
		t.Fatalf("the released run reads state %q, want %q", closed.State, run.StateTimedOut)
	}
	if closed.FinishedAt != releasedAt.Format(time.RFC3339) {
		t.Fatalf("the released run finished at %q, want the release's own clock %q",
			closed.FinishedAt, releasedAt.Format(time.RFC3339))
	}
}

// The reaper frees an expired lease and closes the run it held: the lane becomes
// available again, and the run is terminal with its item, its start and the moment its
// lease said it was over. The release is recorded as a release, so a run the reaper
// closed cannot be read as a run that finished.
func TestAnExpiredLeaseIsReapedAndItsRunClosed(t *testing.T) {
	s, _ := leaseFixture(t)
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	leasedUntil := claim(t, s, "lane_001", "run_001", start, 60_000)

	releases := reap(t, s, time.UnixMilli(leasedUntil))
	if len(releases) != 1 {
		t.Fatalf("the reap released %d lanes, want 1", len(releases))
	}
	got := releases[0]
	if got.LaneID != "lane_001" || got.RunID != "run_001" || got.Result != run.ReleaseReleased ||
		got.Reason != run.ReasonLeaseExpired || !got.TerminalRecord {
		t.Fatalf("the release reads %+v, want lane_001 released from run_001 by the lease expiring, with a record written", got)
	}

	// The lane is free: the state a planner reads as available, with no lease and no run
	// left holding it.
	if held, _ := lane(t, s, "lane_001"); held.State != run.LaneStateIdle || held.RunID != nil || held.LeasedUntil != nil {
		t.Fatalf("the reaped lane is %q held by %v until %v, want idle and unheld", held.State, held.RunID, held.LeasedUntil)
	}

	// The run is closed with the vocabulary §5.6 already has for a run that outlived its
	// bound — not a state of the reaper's own — and with the times the claim recorded.
	closed, found := storedRun(t, s, "run_001")
	if !found {
		t.Fatalf("the reaped run left no terminal record, so nothing says what became of it")
	}
	if closed.State != run.StateTimedOut {
		t.Fatalf("the reaped run is %q, want %q", closed.State, run.StateTimedOut)
	}
	if closed.ItemID != "item_042" || closed.StartedAt != "2026-11-12T09:00:00Z" ||
		closed.FinishedAt != time.UnixMilli(leasedUntil).UTC().Format(time.RFC3339) {
		t.Fatalf("the reaped run reads item %q from %q to %q", closed.ItemID, closed.StartedAt, closed.FinishedAt)
	}
	if closed.ExitCode != nil {
		t.Fatalf("the reaped run carries exit_code %d: there was no exit to record", *closed.ExitCode)
	}
	counts := kinds(t, s)
	if counts[run.FinishKind] != 1 || counts[run.ReleaseKind] != 1 {
		t.Fatalf("the store holds %d finish and %d release events, want 1 of each", counts[run.FinishKind], counts[run.ReleaseKind])
	}
}

// The negative control: a lease that has not run out is not reaped. Without it a reaper
// that freed any lane it was shown would pass the case above, and would take lanes away
// from runs that are working.
func TestALiveLeaseIsNotReaped(t *testing.T) {
	s, _ := leaseFixture(t)
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	leasedUntil := claim(t, s, "lane_001", "run_001", start, 60_000)
	before, _ := lane(t, s, "lane_001")

	releases := reap(t, s, time.UnixMilli(leasedUntil-1))
	if len(releases) != 0 {
		t.Fatalf("a live lease was reaped: %+v", releases)
	}
	after, _ := lane(t, s, "lane_001")
	if after.State != run.LaneStateLeased || after.RunID == nil || *after.RunID != "run_001" ||
		after.LeasedUntil == nil || *after.LeasedUntil != *before.LeasedUntil {
		t.Fatalf("the live lane reads %+v, want it leased to run_001 until %v", after, before.LeasedUntil)
	}
	if _, found := storedRun(t, s, "run_001"); found {
		t.Fatalf("a live run was given a terminal record")
	}
	if n := kinds(t, s)[run.ReleaseKind]; n != 0 {
		t.Fatalf("a live lease produced %d release events", n)
	}
}

// The terminal record is the release: a run that finishes frees its own lane in the
// transaction that writes the record, so a lane is never left held by a run that is
// over — and no reaper is needed for the ordinary case.
func TestTheTerminalRecordReleasesTheLaneWithoutAReaper(t *testing.T) {
	s, dir := leaseFixture(t)
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	stored, err := run.Exec(context.Background(), s, run.Request{
		RunID: "run_001", ItemID: "item_042", Backend: "muse",
		Command: []string{"/bin/true"}, WorkingDir: ".", TimeoutMS: 60_000,
	}, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), Now: now})
	if err != nil {
		t.Fatalf("executing the request failed: %v", err)
	}
	if got := recordOf(t, stored).State; got != run.StateExited {
		t.Fatalf("the run finished %q, want %q", got, run.StateExited)
	}
	laneID := recordOf(t, stored).LaneID
	held, _ := lane(t, s, laneID)
	if held.State != run.LaneStateIdle || held.RunID != nil || held.LeasedUntil != nil {
		t.Fatalf("a finished run left its lane %q held by %v until %v", held.State, held.RunID, held.LeasedUntil)
	}
	// The claim happened before the record: its event is what a reaper would have read,
	// and it carries the lease the record then released.
	if n := kinds(t, s)[run.AllocateKind]; n != 1 {
		t.Fatalf("the store holds %d claim events, want 1", n)
	}
	if n := kinds(t, s)[run.ReleaseKind]; n != 0 {
		t.Fatalf("a finished run wrote %d release events: the terminal record is the release", n)
	}
	if releases := reap(t, s, now.Add(24*time.Hour)); len(releases) != 0 {
		t.Fatalf("a finished run's lane was reaped: %+v", releases)
	}
}

// The standalone release frees a lane and records the operator's reason with it.
func TestReleaseFreesAnExpiredLaneAndRecordsTheReason(t *testing.T) {
	s, _ := leaseFixture(t)
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	leasedUntil := claim(t, s, "lane_001", "run_001", start, 1_000)

	release, err := run.ReleaseLane(context.Background(), s, "lane_001", "the agent's process was killed by hand", time.UnixMilli(leasedUntil))
	if err != nil {
		t.Fatalf("releasing the lane failed: %v", err)
	}
	if release.Result != run.ReleaseReleased || release.Reason != "the agent's process was killed by hand" {
		t.Fatalf("the release reads %+v, want released with the operator's reason", release)
	}
	if !release.TerminalRecord {
		t.Fatalf("the release freed the lane without closing the run it left behind")
	}
	if held, _ := lane(t, s, "lane_001"); held.State != run.LaneStateIdle || held.LeasedUntil != nil {
		t.Fatalf("the released lane is %q until %v", held.State, held.LeasedUntil)
	}
	// The reason travels in the event, which is the audit line: the lane's own row says
	// only that it is free now.
	events, err := eventsOf(t, s)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	var recorded run.Release
	for _, e := range events {
		if e.Kind != run.ReleaseKind {
			continue
		}
		if err := json.Unmarshal(e.Payload, &recorded); err != nil {
			t.Fatalf("reading the %s event failed: %v", run.ReleaseKind, err)
		}
	}
	if recorded.Reason != "the agent's process was killed by hand" || recorded.LaneID != "lane_001" {
		t.Fatalf("the release event reads %+v, want the operator's reason on lane_001", recorded)
	}
}

// A release with no reason is refused before the store is read: a lane freed without one
// is a lane nobody can account for.
func TestReleaseRefusesAReleaseWithNoReason(t *testing.T) {
	s, _ := leaseFixture(t)
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	leasedUntil := claim(t, s, "lane_001", "run_001", start, 1_000)

	for _, reason := range []string{"", "   "} {
		_, err := run.ReleaseLane(context.Background(), s, "lane_001", reason, time.UnixMilli(leasedUntil))
		if err == nil {
			t.Fatalf("a release with reason %q was accepted", reason)
		}
		if code := run.ExitCode(err); code != 2 {
			t.Fatalf("a release with no reason exits %d, want 2: %v", code, err)
		}
	}
	if held, _ := lane(t, s, "lane_001"); held.State != run.LaneStateLeased {
		t.Fatalf("a refused release left the lane %q, want it still leased", held.State)
	}
	if n := kinds(t, s)[run.ReleaseKind]; n != 0 {
		t.Fatalf("a refused release wrote %d release events", n)
	}
}

// The shapes the release declines are recorded answers with a registered reason, not
// errors: a caller reads which one it got. The negative control is the case above, where
// the same face on an expired lease releases it.
func TestReleaseRefusesTheShapesTheDesignRefuses(t *testing.T) {
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name    string
		laneID  string
		claimed bool
		at      time.Time
		reason  string
	}{
		{name: "a lease that has not run out", laneID: "lane_001", claimed: true, at: start.Add(time.Second), reason: run.ReasonLeaseUnexpired},
		{name: "a lane holding no lease", laneID: "lane_001", at: start, reason: run.ReasonLaneFree},
		{name: "a lane the store does not hold", laneID: "lane_999", at: start, reason: run.ReasonLaneUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, dir := leaseFixture(t)
			// A claimed lane is released by a run that finishes, which leaves an idle lane
			// for the "holds no lease" case: the same face, on a lane with nothing to free.
			if c.claimed {
				claim(t, s, "lane_001", "run_001", start, 1_000)
			} else if c.laneID == "lane_001" {
				if _, err := run.Exec(context.Background(), s, run.Request{
					RunID: "run_000", LaneID: "lane_001", Backend: "muse",
					Command: []string{"/bin/true"}, WorkingDir: ".", TimeoutMS: 1_000,
				}, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), Now: start}); err != nil {
					t.Fatalf("creating the free lane failed: %v", err)
				}
			}
			release, err := run.ReleaseLane(context.Background(), s, c.laneID, "tidy up", c.at)
			if err != nil {
				t.Fatalf("the release reported an error rather than a refusal: %v", err)
			}
			if release.Result != run.ReleaseRefused || release.Reason != c.reason {
				t.Fatalf("the release reads %+v / %q, want refused / %q", release, release.Result, c.reason)
			}
			if release.TerminalRecord {
				t.Fatalf("a refused release wrote a terminal record")
			}
		})
	}
}
