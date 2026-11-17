package run_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func copiedStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.InitFixture(context.Background(), filepath.Join(scratch(t), "copied.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state) VALUES('lane', 'idle')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,usage_tokens,usage_cost_usd) VALUES
   ('original','item','lane','pi','exited',1000,1100,1500,.03),
   ('copy','item','lane','pi','exited',2000,2100,1500,.03),
   ('different','item','lane','pi','exited',3000,3100,2000,.04)`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func copiedSnapshot(t *testing.T, s *store.Store) any {
	t.Helper()
	snap, err := s.ReadSnapshot(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var rows []run.Record
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		for _, id := range []string{"original", "copy", "different"} {
			env, _, err := run.StatusRecord(r, id)
			if err != nil {
				return err
			}
			var rec run.Record
			if err := json.Unmarshal(env.Payload, &rec); err != nil {
				return err
			}
			rows = append(rows, rec)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return struct {
		Snapshot store.Snapshot
		Rows     []run.Record
	}{snap, rows}
}

func TestCopiedUsageMarkPreservesEvidenceAndClearsRow(t *testing.T) {
	s := copiedStore(t)
	at := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	err := run.MarkCopiedUsage(context.Background(), s, "copy", "original", "preceding report reused", at)
	if err != nil {
		t.Fatal(err)
	}
	rec, found := storedRun(t, s, "copy")
	if !found || rec.Usage != nil || rec.SpendStatus != "unreported" {
		t.Fatalf("corrected row: %+v", rec)
	}
	events, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != run.CopiedUsageKind || events[0].ObjectID == nil || *events[0].ObjectID != "copy" || !events[0].At.Equal(at) {
		t.Fatalf("events: %+v", events)
	}
	var ev run.CopiedUsage
	if err := json.Unmarshal(events[0].Payload, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.RunID != "copy" || ev.SourceRunID != "original" || ev.Tokens != 1500 || ev.CostUSD != .03 || ev.Reason != "preceding report reused" || rec.EventSeq != events[0].Seq {
		t.Fatalf("evidence: %+v, row: %+v", ev, rec)
	}
	original, _ := storedRun(t, s, "original")
	if original.Usage == nil || *original.Usage.Tokens != 1500 || *original.Usage.CostUSD != .03 {
		t.Fatalf("source changed: %+v", original)
	}
	before := copiedSnapshot(t, s)
	if err := run.MarkCopiedUsage(context.Background(), s, "copy", "original", "again", at); err == nil {
		t.Fatal("second mark accepted")
	}
	if !reflect.DeepEqual(before, copiedSnapshot(t, s)) {
		t.Fatal("second mark wrote")
	}
}

func TestCopiedUsageRefusalsWriteNothing(t *testing.T) {
	for _, tc := range []struct{ name, id, source, reason, change string }{
		{"tokens differ", "copy", "original", "reason", `UPDATE runs SET usage_tokens=1501 WHERE run_id='copy'`},
		{"cost differs", "copy", "original", "reason", `UPDATE runs SET usage_cost_usd=.031 WHERE run_id='copy'`},
		{"source later", "original", "copy", "reason", ""},
		{"same run continued", "copy", "copy", "reason", ""},
		{"source simultaneous", "copy", "original", "reason", `UPDATE runs SET started_at=2000 WHERE run_id='original'`},
		{"unknown tokens", "copy", "original", "reason", `UPDATE runs SET usage_tokens=NULL WHERE run_id='copy'`},
		{"unknown cost", "copy", "original", "reason", `UPDATE runs SET usage_cost_usd=NULL WHERE run_id='copy'`},
		{"missing run", "missing", "original", "reason", ""},
		{"missing source", "copy", "missing", "reason", ""},
		{"blank reason", "copy", "original", "  ", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := copiedStore(t)
			if tc.change != "" {
				if err := s.WriteTx(context.Background(), func(tx *store.Tx) error { _, err := tx.Exec(tc.change); return err }); err != nil {
					t.Fatal(err)
				}
			}
			before := copiedSnapshot(t, s)
			err := run.MarkCopiedUsage(context.Background(), s, tc.id, tc.source, tc.reason, time.Now())
			if err == nil || run.ExitCode(err) != 2 {
				t.Fatalf("refusal: %v", err)
			}
			if !reflect.DeepEqual(before, copiedSnapshot(t, s)) {
				t.Fatal("refusal wrote")
			}
		})
	}
}

func TestCopiedUsageDetectorAndBatch(t *testing.T) {
	s := copiedStore(t)
	matches, err := run.DetectCopiedUsage(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].RunID != "copy" || matches[0].SourceRunID != "original" {
		t.Fatalf("matches: %+v", matches)
	}
	got, err := run.CorrectCopiedUsage(context.Background(), s, "preceding report reused", time.Now())
	if err != nil || len(got) != 1 {
		t.Fatalf("correction: %+v %v", got, err)
	}
	before := copiedSnapshot(t, s)
	matches, err = run.DetectCopiedUsage(context.Background(), s)
	if err != nil || len(matches) != 0 {
		t.Fatalf("after correction: %+v %v", matches, err)
	}
	got, err = run.CorrectCopiedUsage(context.Background(), s, "again", time.Now())
	if err != nil || len(got) != 0 || !reflect.DeepEqual(before, copiedSnapshot(t, s)) {
		t.Fatalf("empty correction wrote: %+v %v", got, err)
	}
}

func TestCopiedUsageBatchUsesEarliestSourceAndRollsBackTogether(t *testing.T) {
	s := copiedStore(t)
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE runs SET usage_tokens=1500,usage_cost_usd=.03 WHERE run_id='different'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	matches, err := run.DetectCopiedUsage(context.Background(), s)
	if err != nil || len(matches) != 2 || matches[1].SourceRunID != "original" {
		t.Fatalf("chain: %+v %v", matches, err)
	}
	// Force the second event to fail after the first row/event have been written.
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER fail_second BEFORE INSERT ON events WHEN NEW.object_id='different' BEGIN SELECT RAISE(ABORT,'second event refused'); END`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	before := copiedSnapshot(t, s)
	got, err := run.CorrectCopiedUsage(context.Background(), s, "reason", time.Now())
	if err == nil || len(got) != 0 || !reflect.DeepEqual(before, copiedSnapshot(t, s)) {
		t.Fatalf("batch not atomic: %+v %v", got, err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error { _, err := tx.Exec(`DROP TRIGGER fail_second`); return err })
	if err != nil {
		t.Fatal(err)
	}
	got, err = run.CorrectCopiedUsage(context.Background(), s, "reason", time.Now())
	if err != nil || len(got) != 2 {
		t.Fatalf("batch: %+v %v", got, err)
	}
}

func TestCopiedUsageDetectorIgnoresZeroPartialAndTies(t *testing.T) {
	for _, change := range []string{
		`UPDATE runs SET usage_tokens=0,usage_cost_usd=0`,
		`UPDATE runs SET usage_tokens=0`,
		`UPDATE runs SET usage_cost_usd=0`,
		`UPDATE runs SET usage_tokens=NULL`,
		`UPDATE runs SET usage_cost_usd=NULL`,
		`UPDATE runs SET started_at=1000`,
		`UPDATE runs SET usage_tokens=usage_tokens+started_at`,
	} {
		t.Run(change, func(t *testing.T) {
			s := copiedStore(t)
			if err := s.WriteTx(context.Background(), func(tx *store.Tx) error { _, err := tx.Exec(change); return err }); err != nil {
				t.Fatal(err)
			}
			before := copiedSnapshot(t, s)
			matches, err := run.DetectCopiedUsage(context.Background(), s)
			if err != nil || len(matches) != 0 {
				t.Fatalf("matches: %+v %v", matches, err)
			}
			got, err := run.CorrectCopiedUsage(context.Background(), s, "reason", time.Now())
			if err != nil || len(got) != 0 || !reflect.DeepEqual(before, copiedSnapshot(t, s)) {
				t.Fatalf("no-copy correction wrote: %+v %v", got, err)
			}
		})
	}
}
