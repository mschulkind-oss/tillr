package drain

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRefreshSessionRecordsLanesForLiveLoopLanes(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1", "lane-2", "express-1"}
	if err := l.refreshSession(ctx, l.cfg.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.endSession)

	var err error
	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var got []string
	var live bool
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		got, live, err = LiveLoopLanes(r, f.storePath, l.cfg.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !live || !reflect.DeepEqual(got, []string{"lane-1", "lane-2", "express-1"}) {
		t.Fatalf("LiveLoopLanes() = (%v, %v), want ([lane-1 lane-2 express-1], true)", got, live)
	}
}

func TestSegmentPredatesReplacementUsesTheCurrentSegment(t *testing.T) {
	f, _ := passFixture(t, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO drain_sessions(session_id,started_at,refreshed_at,next_pass_at,ended_at) VALUES ('original',10,20,30,42)`)
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		for _, tc := range []struct {
			name     string
			loop     *liveLoopSession
			resumeAt int64
			want     bool
		}{
			{"no current loop", nil, 0, false},
			{"same session", &liveLoopSession{id: "original", startedAt: 10}, 20, false},
			{"unknown segment start", &liveLoopSession{id: "replacement", startedAt: 40}, 0, false},
			{"first segment survived", &liveLoopSession{id: "replacement", startedAt: 40}, 15, true},
			{"resumed segment survived", &liveLoopSession{id: "replacement", startedAt: 40}, 25, true},
			{"ambiguous same millisecond", &liveLoopSession{id: "replacement", startedAt: 40}, 40, false},
			{"new segment after replacement", &liveLoopSession{id: "replacement", startedAt: 40}, 45, false},
		} {
			got, err := segmentPredatesReplacement(r, tc.loop, "original", tc.resumeAt)
			if err != nil || got != tc.want {
				t.Errorf("%s: got (%t, %v), want %t", tc.name, got, err, tc.want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFirstSegmentRequiresMoreThanALaneClaim(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	d := mustPlan(t, l)[0].decision
	now := l.cfg.Now()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := run.ClaimLease(ctx, tx, *d.RunRequest, d.LaneID, now)
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		for _, face := range []bool{false, true} {
			live, err := liveFirstSegmentWorker(r, &pendingWork{Decision: d}, now, map[string]bool{d.RunRequest.RunID: face})
			if err != nil || live != face {
				t.Errorf("leased run with face=%v: live=%v, err=%v", face, live, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWhyDistinguishesAResumedSegmentFromItsFinishAndExamination(t *testing.T) {
	f, l := passFixture(t, "item-a")
	d := pausedThenResumedAndKilled(t, f, l)
	check := func(wantLive bool) {
		t.Helper()
		fixtureRead(t, f, func(r *store.Reader) error {
			answer, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339Nano)}, l.cfg.Policy, nil)
			if err != nil {
				return err
			}
			row := answer.Rows[0]
			if got := strings.Contains(row.Mover, "worker "+d.RunRequest.RunID+" on "+d.LaneID); got != wantLive {
				t.Errorf("live worker=%t, want %t: %s (%s)", got, wantLive, row.Why, row.Mover)
			}
			if wantLive && (row.Position != "running" || row.Reason != ReasonRunLive) {
				t.Errorf("live segment described as %s / %s", row.Position, row.Reason)
			}
			return nil
		})
	}
	check(true)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		req := d.RunRequest
		// The fixture uses the same clock for both finishes and the resume.
		// Event order, not timestamp precision, distinguishes the segments.
		at := l.cfg.Now().Format(time.RFC3339Nano)
		if err := applyTerminal(tx, run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit}); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state='leased', run_id=?, leased_until=NULL WHERE lane_id=?`, req.RunID, req.LaneID)
		return err
	})
	check(false)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, examinationClaimedKind, d.RunRequest.RunID, LoopOwner{}, l.cfg.Now())
	})
	check(false)
}

func TestSavedQuestionsWritesAndReusesIndexAtSameTrunk(t *testing.T) {
	root := t.TempDir()
	docPath := filepath.Join(root, "docs", "design.md")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte("# Design\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(t.TempDir(), "calls")
	script := filepath.Join(t.TempDir(), "index.sh")
	body := "#!/bin/sh\nprintf '1\\n' >> " + counter + "\nprintf '%s' '{\"index\":{\"documents\":[{\"path\":\"docs/design.md\",\"questions\":[{\"id\":\"OQ-1\",\"state\":\"open\"}]}]}}'\n"
	if err := writeForkSafe(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := []string{script}
	var calls atomic.Int32

	q1 := newSavedQuestions(root, "abc123", cmd, context.Background())
	doc, err := q1.Document("docs/design.md")
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Present || len(doc.Questions) != 1 || doc.Questions[0].ID != "OQ-1" {
		t.Fatalf("first Document: %+v", doc)
	}
	if _, err := os.Stat(QuestionsIndexFile(root)); err != nil {
		t.Fatalf("saved index file missing: %v", err)
	}

	// Second reader at the same trunk SHA reuses the saved file without running script.
	q2 := newSavedQuestions(root, "abc123", cmd, context.Background())
	doc2, err := q2.Document("docs/design.md")
	if err != nil {
		t.Fatal(err)
	}
	if !doc2.Present || len(doc2.Questions) != 1 || doc2.Questions[0].ID != "OQ-1" {
		t.Fatalf("second Document: %+v", doc2)
	}
	rawCalls, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range rawCalls {
		if b == '\n' {
			calls.Add(1)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("index command ran %d times, want 1", calls.Load())
	}
}
