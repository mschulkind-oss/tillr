package run_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestExecRecordsSeatWorkflowAndNode(t *testing.T) {
	for _, tc := range []struct {
		name         string
		req          run.Request
		command      []string
		timeoutMS    int
		startFailure bool
		wantNode     string
	}{
		{
			name: "explicit-node",
			req: run.Request{
				RunID:    "run-seat-explicit",
				ItemID:   "item-1",
				LaneID:   "lane-seat",
				Backend:  "fixture",
				Seat:     "edit",
				Workflow: "work",
				Node:     "work/piece-a",
				NodeID:   "legacy-node",
			},
			command:  []string{"/bin/true"},
			wantNode: "work/piece-a",
		},
		{
			name: "fallback-to-node-id",
			req: run.Request{
				RunID:    "run-seat-nodeid",
				ItemID:   "item-2",
				LaneID:   "lane-seat",
				Backend:  "fixture",
				Seat:     "review",
				Workflow: "verify",
				NodeID:   "verify/review",
			},
			command:  []string{"/bin/true"},
			wantNode: "verify/review",
		},
		{
			name: "failed-to-start",
			req: run.Request{
				RunID:    "run-seat-fail",
				ItemID:   "item-3",
				LaneID:   "lane-seat",
				Backend:  "fixture",
				Seat:     "build",
				Workflow: "work",
				Node:     "work/build",
			},
			command:      []string{"/no/such/tillr-seat-cmd"},
			startFailure: true,
			wantNode:     "work/build",
		},
		{
			name: "timed-out",
			req: run.Request{
				RunID:    "run-seat-timeout",
				ItemID:   "item-4",
				LaneID:   "lane-seat",
				Backend:  "fixture",
				Seat:     "test-run",
				Workflow: "work",
				Node:     "work/test",
			},
			command:   []string{"/bin/sleep", "10"},
			timeoutMS: 10,
			wantNode:  "work/test",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, root := leaseFixture(t)
			ctx := context.Background()
			req := tc.req
			req.Command = tc.command
			req.WorkingDir = "."
			req.TimeoutMS = tc.timeoutMS

			env, err := run.Exec(ctx, s, req, run.ExecOptions{
				LaneRoot: filepath.Join(root, "lanes"),
				Now:      time.Now().UTC(),
			})
			if (err != nil) != tc.startFailure {
				t.Fatalf("Exec err = %v, want startFailure=%v", err, tc.startFailure)
			}

			var rec run.Record
			if err := json.Unmarshal(env.Payload, &rec); err != nil {
				t.Fatal(err)
			}
			if rec.Seat != tc.req.Seat || rec.Workflow != tc.req.Workflow || rec.Node != tc.wantNode {
				t.Fatalf("record = (%q, %q, %q), want (%q, %q, %q)",
					rec.Seat, rec.Workflow, rec.Node, tc.req.Seat, tc.req.Workflow, tc.wantNode)
			}

			if err := s.ReadTx(ctx, func(r *store.Reader) error {
				status, found, err := run.StatusRecord(r, tc.req.RunID)
				if err != nil {
					return err
				}
				if !found {
					t.Fatal("StatusRecord did not find run")
				}
				var payload struct {
					Run run.Record `json:"run"`
				}
				if err := json.Unmarshal(status.Payload, &payload); err != nil {
					return err
				}
				if payload.Run.Seat != tc.req.Seat || payload.Run.Workflow != tc.req.Workflow || payload.Run.Node != tc.wantNode {
					t.Errorf("StatusRecord = (%q, %q, %q), want (%q, %q, %q)",
						payload.Run.Seat, payload.Run.Workflow, payload.Run.Node, tc.req.Seat, tc.req.Workflow, tc.wantNode)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			events, err := eventsOf(t, s)
			if err != nil {
				t.Fatal(err)
			}
			var sawFinish bool
			for _, ev := range events {
				if ev.Kind != run.FinishKind {
					continue
				}
				sawFinish = true
				var fin run.Record
				if err := json.Unmarshal(ev.Payload, &fin); err != nil {
					t.Fatal(err)
				}
				if fin.Seat != tc.req.Seat || fin.Workflow != tc.req.Workflow || fin.Node != tc.wantNode {
					t.Errorf("finish event = (%q, %q, %q), want (%q, %q, %q)",
						fin.Seat, fin.Workflow, fin.Node, tc.req.Seat, tc.req.Workflow, tc.wantNode)
				}
			}
			if !sawFinish {
				t.Fatal("missing run.finish event")
			}
		})
	}
}

func TestClaimLeaseAndCloseRunPreserveSeatWorkflowAndNode(t *testing.T) {
	s, _ := leaseFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	var until int64
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		until, err = run.ClaimLease(ctx, tx, run.Request{
			RunID:    "run-reclaim-seat",
			ItemID:   "item-reclaim",
			Backend:  "fixture",
			Seat:     "design",
			Workflow: "design",
			Node:     "design/spec",
			NodeID:   "ignored-fallback",
		}, "lane-reclaim", now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if until != 0 {
		t.Fatalf("unbounded lease returned %d, want 0", until)
	}

	// Reap via ReleaseLane (closeRun path for unbounded or expired leases).
	rel, err := run.ReleaseLane(ctx, s, "lane-reclaim", "operator-reclaim", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !rel.TerminalRecord {
		t.Fatalf("expected TerminalRecord=true, got %+v", rel)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		status, found, err := run.StatusRecord(r, "run-reclaim-seat")
		if err != nil {
			return err
		}
		if !found {
			t.Fatal("StatusRecord did not find reclaimed run")
		}
		var payload struct {
			Run run.Record `json:"run"`
		}
		if err := json.Unmarshal(status.Payload, &payload); err != nil {
			return err
		}
		if payload.Run.Seat != "design" || payload.Run.Workflow != "design" || payload.Run.Node != "design/spec" {
			t.Errorf("reclaimed run = (%q, %q, %q), want (design, design, design/spec)",
				payload.Run.Seat, payload.Run.Workflow, payload.Run.Node)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyRecordsAndUnmigratedRunsTableReadable(t *testing.T) {
	s, root := leaseFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Apply a pre-change run.record JSON without seat, workflow, or node keys.
	legacyPayload := map[string]any{
		"run_id":        "run-legacy-seat",
		"lane_id":       "lane-legacy",
		"backend":       "fixture",
		"state":         run.StateExited,
		"exit_code":     0,
		"started_at":    now.Format(time.RFC3339),
		"finished_at":   now.Add(time.Second).Format(time.RFC3339),
		"artifact_refs": []any{},
		"spend_status":  "unknown",
	}
	rawPayload, err := json.Marshal(legacyPayload)
	if err != nil {
		t.Fatal(err)
	}
	env := record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          run.RecordKind,
		ID:            "run-record-run-legacy-seat",
		Producer:      record.Producer{System: record.System, Utility: run.Utility, Version: run.Version},
		CreatedAt:     now,
		Subject:       &record.Subject{Kind: "run", ID: "run-legacy-seat"},
		Payload:       rawPayload,
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := run.Apply(ctx, tx, env)
		return err
	}); err != nil {
		t.Fatalf("Apply legacy record: %v", err)
	}

	// Simulate an unmigrated store written before seat, workflow, and node columns existed.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, col := range []string{"seat", "workflow", "node"} {
			if _, err := tx.Exec(`ALTER TABLE runs DROP COLUMN ` + col); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	ro, err := store.OpenRead(ctx, filepath.Join(root, "board.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		status, found, err := run.StatusRecord(r, "run-legacy-seat")
		if err != nil {
			return err
		}
		if !found {
			t.Fatal("StatusRecord did not find legacy run")
		}
		var payload struct {
			Run run.Record `json:"run"`
		}
		if err := json.Unmarshal(status.Payload, &payload); err != nil {
			return err
		}
		if payload.Run.Seat != "" || payload.Run.Workflow != "" || payload.Run.Node != "" {
			t.Errorf("legacy run unexpectedly carried (%q, %q, %q)",
				payload.Run.Seat, payload.Run.Workflow, payload.Run.Node)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
