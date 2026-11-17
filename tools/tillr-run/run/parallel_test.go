package run_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestParallelReadersRetainLeaseAndOwnLogs(t *testing.T) {
	s, _ := leaseFixture(t)
	ctx := context.Background()
	root := scratch(t)
	now := time.Now()
	requests := []run.Request{}
	for _, id := range []string{"one", "two", "three", "four"} {
		req := run.Request{RunID: id, ItemID: "item", NodeID: id, LaneID: "lane", Backend: "command", ReadOnlyGroup: "one", WorkingDir: "worktree", Command: []string{"sh", "-c", "pwd; echo " + id}}
		requests = append(requests, req)
		if err := s.WriteTx(ctx, func(tx *store.Tx) error { _, err := run.ClaimLease(ctx, tx, req, "lane", now); return err }); err != nil {
			t.Fatal(err)
		}
	}
	for i, req := range requests {
		env, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: root, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		var rec run.Record
		if err = json.Unmarshal(env.Payload, &rec); err != nil {
			t.Fatal(err)
		}
		if rec.ExitCode == nil || *rec.ExitCode != 0 {
			t.Fatalf("record: %+v", rec)
		}
		expected := filepath.Join(run.NodeDir(filepath.Join(root, "lane"), req.RunID), req.RunID+".log")
		if _, err = os.Stat(expected); err != nil {
			t.Fatal(err)
		}
		got, found := lane(t, s, "lane")
		if !found {
			t.Fatal("missing lane")
		}
		if i < 3 && got.State != run.LaneStateLeased {
			t.Fatalf("early release after %s: %+v", req.RunID, got)
		}
		if i == 3 && got.State != run.LaneStateIdle {
			t.Fatalf("lease retained after last run: %+v", got)
		}
	}
	// Settlement can be controlled by another member after a primary failover.
	// A continued sibling still belongs to the original group, not that control id.
	req := requests[2]
	req.Resume = true
	if err := os.WriteFile(filepath.Join(run.NodeDir(filepath.Join(root, "lane"), req.RunID), run.SessionIDFile), []byte("11111111-1111-4111-8111-111111111111\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE lanes SET state=?,run_id='two' WHERE lane_id='lane'`, run.LaneStateLeased); err != nil {
			return err
		}
		_, err := run.ClaimLease(ctx, tx, req, "lane", now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var active int
		err := r.QueryRow(`SELECT COUNT(*) FROM read_only_runs WHERE settled=0`).Scan(&active)
		if active != 1 {
			t.Errorf("continued group has %d active nodes", active)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: root, Now: now}); err != nil {
		t.Fatal(err)
	}
	if got, _ := lane(t, s, "lane"); got.State != run.LaneStateIdle {
		t.Fatalf("continued sibling retained group: %+v", got)
	}
	if got := kinds(t, s)[run.AllocateKind]; got != 4 {
		t.Fatalf("%d allocations, want one per node", got)
	}
}

func TestParallelReadersReleaseSettlesKilledContinuationBeforeReplacement(t *testing.T) {
	s, _ := leaseFixture(t)
	ctx := context.Background()
	now := time.Now()
	root := scratch(t)
	req := run.Request{RunID: "one", ItemID: "item", NodeID: "scout", LaneID: "lane", Backend: "command", ReadOnlyGroup: "one", WorkingDir: "worktree", Command: []string{"sh", "-c", "echo done"}}
	if _, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: root, Now: now}); err != nil {
		t.Fatal(err)
	}
	req.Resume = true
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := run.ClaimLease(ctx, tx, req, "lane", now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// The resumed process dies before it replaces its earlier terminal record.
	if _, err := run.ReleaseLane(ctx, s, "lane", "continuation died", now); err != nil {
		t.Fatal(err)
	}
	req.Resume = false
	req.RunID = "one-restart"
	if _, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: root, Now: now}); err != nil {
		t.Fatal(err)
	}
	if got, _ := lane(t, s, "lane"); got.State != run.LaneStateIdle {
		t.Fatalf("released continuation kept replacement lease alive: %+v", got)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var active int
		if err := r.QueryRow(`SELECT COUNT(*) FROM read_only_runs WHERE settled=0`).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			t.Fatalf("released group still has %d active members", active)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestParallelReadersReleaseClosesEveryUnfinishedRun(t *testing.T) {
	s, _ := leaseFixture(t)
	ctx := context.Background()
	now := time.Now()
	for _, id := range []string{"one", "two", "three"} {
		req := run.Request{RunID: id, ItemID: "item", NodeID: id, LaneID: "lane", Backend: "fixture", ReadOnlyGroup: "one"}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error { _, err := run.ClaimLease(ctx, tx, req, "lane", now); return err }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := run.ReleaseLane(ctx, s, "lane", "operator stop", now); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var count int
		err := r.QueryRow(`SELECT COUNT(*) FROM runs WHERE end_cause='lease-reclaimed'`).Scan(&count)
		if count != 3 {
			t.Errorf("closed %d, want 3", count)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
