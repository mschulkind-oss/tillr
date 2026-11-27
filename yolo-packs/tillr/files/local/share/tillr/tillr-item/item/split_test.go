package item

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
	_ "modernc.org/sqlite"
)

func boolPtr(v bool) *bool { return &v }

func TestSplitPartIDAndReadHelpers(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")

	s, err := store.Open(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Date(2026, 11, 27, 6, 0, 0, 0, time.UTC)
	var parentID string
	var partIDs map[string]string

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		env, err := FileRequest(ctx, tx, RequestFiling{
			Title:       "Parent item",
			Words:       []byte("Build A and B."),
			Criteria:    []string{"A works; B works."},
			Ordinary:    true,
			At:          now,
			Destination: Destination{To: "last"},
		})
		if err != nil {
			return err
		}
		parentID = env.ID
		st := SplitStatement{
			Parts: []SplitPart{
				{Label: "a", Title: "Part A", Share: "Deliver A", Leaves: map[string]string{"b": "Deliver B"}, Required: boolPtr(true)},
				{Label: "b", Title: "Part B", Share: "Deliver B", Leaves: map[string]string{"a": "Deliver A"}, Follows: []string{"a"}, Required: boolPtr(true)},
				{Label: "opt", Title: "Optional", Share: "Extra", Required: boolPtr(false)},
			},
			Coverage: map[string][]SplitCoverage{
				"c5": {
					{Clause: "A works;", Part: "a"},
					{Clause: "B works.", Part: "b"},
				},
			},
		}
		partIDs, err = FileSplit(ctx, tx, parentID, st, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		if got := SplitPartID(parentID, "a"); got != partIDs["a"] || !strings.HasPrefix(got, "item-part-") {
			t.Fatalf("SplitPartID = %q, want %q", got, partIDs["a"])
		}
		if _, found, err := ReadSplitStatement(r, "item-missing"); err != nil || found {
			t.Fatalf("missing ReadSplitStatement = (%v, %v), want (false, nil)", found, err)
		}
		st, found, err := ReadSplitStatement(r, parentID)
		if err != nil || !found || st == nil {
			t.Fatalf("ReadSplitStatement = (%v, %v, %v)", st, found, err)
		}
		if len(st.Parts) != 3 || st.Parts[0].Label != "a" || st.Parts[1].Follows[0] != "a" {
			t.Fatalf("unexpected statement: %+v", st)
		}
		all, err := ReadAllSplits(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 1 || !reflect.DeepEqual(all[parentID], *st) {
			t.Fatalf("ReadAllSplits = %+v", all)
		}
		idx := SplitPartIndex(all)
		if len(idx) != 3 {
			t.Fatalf("SplitPartIndex len = %d, want 3", len(idx))
		}
		mA, ok := idx[partIDs["a"]]
		if !ok || mA.ParentID != parentID || mA.Label != "a" || mA.Part.Share != "Deliver A" {
			t.Fatalf("unexpected index entry for a: %+v (ok=%v)", mA, ok)
		}
		mOpt, ok := idx[partIDs["opt"]]
		if !ok || mOpt.ParentID != parentID || mOpt.Label != "opt" || mOpt.Part.Required == nil || *mOpt.Part.Required {
			t.Fatalf("unexpected index entry for opt: %+v (ok=%v)", mOpt, ok)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadSplitMissingTable(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "old.db")

	s, err := store.Open(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `DROP TABLE item_splits`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	rs, err := store.OpenRead(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()

	if err := rs.ReadTx(ctx, func(r *store.Reader) error {
		st, found, err := ReadSplitStatement(r, "item-any")
		if err != nil || found || st != nil {
			t.Fatalf("ReadSplitStatement on missing table = (%v, %v, %v), want (nil, false, nil)", st, found, err)
		}
		all, err := ReadAllSplits(r)
		if err != nil || len(all) != 0 {
			t.Fatalf("ReadAllSplits on missing table = (%v, %v), want (empty, nil)", all, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func openSplitTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, err := store.Open(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO items (item_id, title, state, updated_at) VALUES ('item-1', 'Test item', 'in-queue', 1000)`,
		)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, "item-1"
}

func TestSplitCallLifecycleAndNoSplitS1Tracking(t *testing.T) {
	ctx := context.Background()
	s, itemID := openSplitTestStore(t)
	t0 := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

	var recorded SplitCall
	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		recorded, err = RecordSplitCall(ctx, tx, SplitCall{
			Caller:    SplitCallerVerifier,
			Timestamp: t0,
			ItemID:    itemID,
			RunID:     "run-1",
			DonorRef:  "refs/tillr/runs/run-1",
			DonorSHA:  "aaaa1111",
			BaseSHA:   "00001111",
			Signals: []SplitSignal{{
				Signal:         SplitSignalS1,
				Files:          30,
				AddedLines:     500,
				FileLimit:      27,
				AddedLineLimit: 1200,
			}},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Status != SplitCallStatusPending || recorded.CallID == 0 {
		t.Fatalf("got %+v, want pending with non-zero CallID", recorded)
	}

	// Second call while pending merges signals into the existing pending call.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := RecordSplitCall(ctx, tx, SplitCall{
			Caller:    "planner",
			Timestamp: t0.Add(time.Minute),
			ItemID:    itemID,
			RunID:     "run-2",
			Signals: []SplitSignal{{
				Signal:         "S5",
				Files:          30,
				AddedLines:     500,
				FileLimit:      27,
				AddedLineLimit: 1200,
			}},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	var pending *SplitCall
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		pending, err = PendingSplitCall(r, itemID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if pending == nil || len(pending.Signals) != 2 || pending.Signals[0].Signal != SplitSignalS1 || pending.Signals[1].Signal != "S5" {
		t.Fatalf("pending = %+v, want merged S1 and S5 signals", pending)
	}

	// Malformed statement rejection is recorded on the pending call.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		return RecordSplitRejection(ctx, tx, itemID, "design-1", "a split needs at least two parts")
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		pending, err = PendingSplitCall(r, itemID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if pending.LastRejectionReason != "a split needs at least two parts" || pending.DesignRunID != "design-1" {
		t.Fatalf("rejection not recorded: %+v", pending)
	}

	// Answering "no split" clears pending and records answered S1 readings.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := AnswerSplitNoSplit(ctx, tx, itemID, "design-2", "shared invariant across files", t0.Add(2*time.Minute))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		p, err := PendingSplitCall(r, itemID)
		if err != nil || p != nil {
			t.Fatalf("expected no pending call after no-split, got %+v, %v", p, err)
		}
		calls, err := ReadSplitCalls(r, itemID)
		if err != nil || len(calls) != 1 {
			t.Fatalf("calls = %+v, %v", calls, err)
		}
		if calls[0].Status != SplitCallStatusNoSplit || calls[0].NoSplit == nil || calls[0].NoSplit.Reason != "shared invariant across files" || calls[0].LastRejectionReason != "" {
			t.Fatalf("unexpected answered call: %+v", calls[0])
		}
		for _, tc := range []struct {
			name string
			sig  SplitSignal
			want bool
		}{
			{"equal", SplitSignal{Signal: SplitSignalS1, Files: 30, AddedLines: 500}, true},
			{"smaller", SplitSignal{Signal: SplitSignalS1, Files: 28, AddedLines: 400}, true},
			{"more-files", SplitSignal{Signal: SplitSignalS1, Files: 31, AddedLines: 500}, false},
			{"more-lines", SplitSignal{Signal: SplitSignalS1, Files: 30, AddedLines: 501}, false},
		} {
			got, err := S1AnsweredNoSplit(r, itemID, tc.sig)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("%s: S1AnsweredNoSplit = %v, want %v", tc.name, got, tc.want)
			}
		}
		donors, err := ReadSplitDonorRefs(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(donors) != 0 {
			t.Fatalf("no-split call should not protect donor ref, got %+v", donors)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
