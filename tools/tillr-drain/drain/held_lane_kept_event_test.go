package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func readLaneKeptEvents(t *testing.T, storePath, lane string) []laneKept {
	t.Helper()
	s := openFixture(t, storePath)
	defer s.Close()
	var got []laneKept
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(
			`SELECT object_type, object_id, payload FROM events WHERE kind = ? AND object_id = ? ORDER BY seq`,
			laneKeptKind, lane,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var objType, objID string
			var raw []byte
			if err := rows.Scan(&objType, &objID, &raw); err != nil {
				return err
			}
			if objType != "lane" || objID != lane {
				t.Fatalf("drain.lane-kept object = (%q, %q), want (\"lane\", %q)", objType, objID, lane)
			}
			var payload laneKept
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			got = append(got, payload)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestKeptLaneAppendsRegisteredEventAndDeduplicates(t *testing.T) {
	kind, err := event.Lookup(laneKeptKind)
	if err != nil {
		t.Fatalf("laneKeptKind %q not registered: %v", laneKeptKind, err)
	}
	if kind.Owner != Utility {
		t.Fatalf("laneKeptKind owner = %q, want %q", kind.Owner, Utility)
	}

	f, l, c := heldCandidateFixture(t)
	if err := l.holdItemFault(context.Background(), c, StopGateHarnessError, "gate failed"); err != nil {
		t.Fatal(err)
	}

	// Keep 1: foreign-domain live writer keeps the lane inside writeRelease.
	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	boot, _, _ := strings.Cut(here.Domain, ":")
	foreignOwner := *here
	foreignOwner.Domain = boot + ":pid:[1]"
	foreignOwner.PID = 8300
	foreignRunID := "run-item-foreign-kept-event-a1"
	foreignWork := pendingWork{
		Owner: &foreignOwner,
		Decision: Decision{
			ItemID: "item-foreign",
			LaneID: c.LaneID,
			RunRequest: &run.Request{
				RunID:   foreignRunID,
				ItemID:  "item-foreign",
				LaneID:  c.LaneID,
				Backend: "fixture",
			},
		},
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at,seat) VALUES('item-foreign','item-foreign','landed',0,'')`); err != nil {
			return err
		}
		if err := appendEffect(tx, dispatchStartedKind, foreignRunID, foreignWork, l.cfg.Now().Add(-time.Minute)); err != nil {
			return err
		}
		return appendEffect(tx, examinationDoneKind, foreignRunID, struct{}{}, l.cfg.Now().Add(-30*time.Second))
	})
	paths := runStreams(t, f, foreignRunID)
	writer, err := os.OpenFile(paths[1], os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	ctx := context.Background()
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	const foreignReason = "lane may have a live run in another process domain"
	wantFirst := laneKept{
		LaneID: c.LaneID,
		ItemID: c.ItemID,
		RunID:  c.RunID,
		Reason: foreignReason,
	}
	events := readLaneKeptEvents(t, f.storePath, c.LaneID)
	if len(events) != 1 || events[0] != wantFirst {
		t.Fatalf("after first keep: events = %+v, want [%+v]", events, wantFirst)
	}

	// Repeated keeps with the same reason append no second event, both through
	// releaseIdleHeldCandidates and with a stale pendingWork snapshot.
	for i := 0; i < 2; i++ {
		if err := l.releaseIdleHeldCandidates(ctx); err != nil {
			t.Fatal(err)
		}
	}
	stale := &pendingWork{Decision: mustPlan(t, l)[0].decision, Candidate: &c}
	if released, err := l.releaseHeldCandidate(ctx, stale, StopGateHarnessError); err != nil || released {
		t.Fatalf("stale repeat: released=%v err=%v", released, err)
	}
	if events := readLaneKeptEvents(t, f.storePath, c.LaneID); len(events) != 1 {
		t.Fatalf("repeated same-reason keeps appended %d events, want 1: %+v", len(events), events)
	}

	// Keep 2: when the reason changes on the same lane, a second event is appended.
	writeForkFile(t, filepath.Join(c.Worktree, "candidate.txt"), "unbound later commit\n")
	runGit(t, c.Worktree, "commit", "-qam", "unbound")
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	const unrecordedReason = "unrecorded lane HEAD needs inspection"
	wantSecond := laneKept{
		LaneID: c.LaneID,
		ItemID: c.ItemID,
		RunID:  c.RunID,
		Reason: unrecordedReason,
	}
	events = readLaneKeptEvents(t, f.storePath, c.LaneID)
	if len(events) != 2 || events[1] != wantSecond {
		t.Fatalf("after reason change: events = %+v, want second [%+v]", events, wantSecond)
	}
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	if events := readLaneKeptEvents(t, f.storePath, c.LaneID); len(events) != 2 {
		t.Fatalf("repeated changed-reason keep appended %d events, want 2: %+v", len(events), events)
	}
}

func TestKeptLaneAppendsAgainAfterRelease(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	ctx := context.Background()
	if err := l.holdItemFault(ctx, c, StopGateHarnessError, "gate failed"); err != nil {
		t.Fatal(err)
	}

	writeForkFile(t, filepath.Join(c.Worktree, "candidate.txt"), "unbound before release\n")
	runGit(t, c.Worktree, "commit", "-qam", "unbound before release")
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	want := laneKept{
		LaneID: c.LaneID,
		ItemID: c.ItemID,
		RunID:  c.RunID,
		Reason: "unrecorded lane HEAD needs inspection",
	}
	events := readLaneKeptEvents(t, f.storePath, c.LaneID)
	if len(events) != 1 || events[0] != want {
		t.Fatalf("before release: events = %+v, want [%+v]", events, want)
	}

	// Clearing the keep condition releases the lane; reassigning and keeping it
	// again with the same reason must append a second drain.lane-kept event.
	runGit(t, c.Worktree, "reset", "--hard", c.HeadSHA)
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	if got := laneStateOf(t, f.storePath, c.ItemID); got.Status != "released" {
		t.Fatalf("lane state after clearing keep condition = %+v, want released", got)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		w := works[c.ItemID]
		d := w.Decision
		d.LaneID = c.LaneID
		return l.assignPinnedCandidate(tx, w, &d, l.cfg.Now().UTC())
	})
	if err := l.stagePinnedCandidate(ctx, pendingForTest(t, f, c.ItemID)); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(c.Worktree, "candidate.txt"), "unbound after release\n")
	runGit(t, c.Worktree, "commit", "-qam", "unbound after release")
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	events = readLaneKeptEvents(t, f.storePath, c.LaneID)
	if len(events) != 2 || events[1] != want {
		t.Fatalf("after release and re-keep: events = %+v, want two [%+v]", events, want)
	}
}
