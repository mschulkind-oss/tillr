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

func TestFileSplitDonorCarvingAndReplay(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")

	s, err := store.Open(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Date(2026, 11, 27, 7, 0, 0, 0, time.UTC)
	var parentID string
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		env, err := FileRequest(ctx, tx, RequestFiling{
			Title:       "Oversized request",
			Words:       []byte("Build X, Y, and Z."),
			Criteria:    []string{"X works; Y works; Z works."},
			Ordinary:    true,
			At:          now,
			Destination: Destination{To: "last"},
		})
		if err != nil {
			return err
		}
		parentID = env.ID
		_, err = RecordSplitCall(ctx, tx, SplitCall{
			Caller:    SplitCallerVerifier,
			Timestamp: now,
			ItemID:    parentID,
			RunID:     "run-donor-1",
			DonorRef:  "refs/tillr/runs/run-donor-1",
			DonorSHA:  "donor-sha-111",
			BaseSHA:   "base-sha-000",
			Signals:   []SplitSignal{{Signal: SplitSignalS1, Files: 30, AddedLines: 1400, FileLimit: 27, AddedLineLimit: 1200}},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	st := SplitStatement{
		Parts: []SplitPart{
			{
				Label:    "whole-part",
				Title:    "Part whole",
				Share:    "Deliver X and Y",
				Leaves:   map[string]string{"carved-part": "Deliver Z"},
				Takes:    SplitTakesWhole,
				Required: boolPtr(true),
			},
			{
				Label:             "carved-part",
				Title:             "Part carved",
				Share:             "Deliver Z",
				Takes:             SplitTakesNamed,
				Paths:             []string{"tools/z/main.go", "tools/z/main_test.go"},
				SharedFiles:       []string{"tools/shared/wire.go"},
				InheritedFindings: []string{"c5 [gap] (Z missing): tools/z/main.go"},
				Required:          boolPtr(true),
			},
			{
				Label:             "fresh-part",
				Title:             "Part fresh",
				Share:             "Extra cleanup",
				Takes:             SplitTakesNothing,
				InheritedFindings: []string{"c5 [regression] (cleanup broke): tools/clean.go"},
				Required:          boolPtr(false),
			},
		},
		Coverage: map[string][]SplitCoverage{
			"c5": {
				{Clause: "X works; Y works;", Part: "whole-part"},
				{Clause: "Z works.", Part: "carved-part"},
			},
		},
	}

	var partIDs map[string]string
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		partIDs, err = FileSplit(ctx, tx, parentID, st, now.Add(time.Minute))
		return err
	}); err != nil {
		t.Fatalf("FileSplit with auto-populated donor failed: %v", err)
	}

	// Exact replay with Donor == nil must succeed as a no-op because split_calls
	// (now status=split) auto-populates the exact same Donor before marshaling.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		replayed, err := FileSplit(ctx, tx, parentID, st, now.Add(2*time.Minute))
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(replayed, partIDs) {
			t.Fatalf("replayed ids = %v, want %v", replayed, partIDs)
		}
		return nil
	}); err != nil {
		t.Fatalf("exact replay failed: %v", err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		stored, ok, err := ReadSplitStatement(r, parentID)
		if err != nil || !ok {
			t.Fatalf("ReadSplitStatement = %v, %v, %v", stored, ok, err)
		}
		if stored.Donor == nil || stored.Donor.SHA != "donor-sha-111" || stored.Donor.BaseSHA != "base-sha-000" || stored.Donor.RunID != "run-donor-1" || stored.Donor.Ref != "refs/tillr/runs/run-donor-1" {
			t.Fatalf("stored.Donor = %+v", stored.Donor)
		}
		ctxInfo, err := SplitPartContext(r, partIDs["carved-part"])
		if err != nil || ctxInfo == nil {
			t.Fatalf("SplitPartContext = %+v, %v", ctxInfo, err)
		}
		if ctxInfo.Donor == nil || ctxInfo.Donor.SHA != "donor-sha-111" {
			t.Fatalf("ctxInfo.Donor = %+v", ctxInfo.Donor)
		}
		if ctxInfo.Part.Takes != SplitTakesNamed || len(ctxInfo.Part.Paths) != 2 || len(ctxInfo.Part.SharedFiles) != 1 || len(ctxInfo.Part.InheritedFindings) != 1 {
			t.Fatalf("ctxInfo.Part = %+v", ctxInfo.Part)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFileSplitCarvingValidationRejections(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 27, 7, 30, 0, 0, time.UTC)

	newStoreWithParent := func(t *testing.T) (*store.Store, string) {
		t.Helper()
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		var parentID string
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			env, err := FileRequest(ctx, tx, RequestFiling{
				Title:       "Parent",
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
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return s, parentID
	}

	donor := &SplitDonor{RunID: "r1", Ref: "refs/tillr/runs/r1", SHA: "sha-1", BaseSHA: "base-1"}
	cov := map[string][]SplitCoverage{"c5": {{Clause: "A works;", Part: "a"}, {Clause: "B works.", Part: "b"}}}

	cases := []struct {
		name      string
		statement SplitStatement
		wantErr   string
	}{
		{
			name: "invalid takes",
			statement: SplitStatement{
				Donor: donor,
				Parts: []SplitPart{
					{Label: "a", Title: "A", Share: "A", Takes: "partial", Required: boolPtr(true)},
					{Label: "b", Title: "B", Share: "B", Required: boolPtr(true)},
				},
				Coverage: cov,
			},
			wantErr: "invalid takes",
		},
		{
			name: "whole without donor",
			statement: SplitStatement{
				Parts: []SplitPart{
					{Label: "a", Title: "A", Share: "A", Takes: SplitTakesWhole, Required: boolPtr(true)},
					{Label: "b", Title: "B", Share: "B", Required: boolPtr(true)},
				},
				Coverage: cov,
			},
			wantErr: "no donor sha and base_sha",
		},
		{
			name: "whole missing base_sha",
			statement: SplitStatement{
				Donor: &SplitDonor{SHA: "sha-1"},
				Parts: []SplitPart{
					{Label: "a", Title: "A", Share: "A", Takes: SplitTakesWhole, Required: boolPtr(true)},
					{Label: "b", Title: "B", Share: "B", Required: boolPtr(true)},
				},
				Coverage: cov,
			},
			wantErr: "no donor sha and base_sha",
		},
		{
			name: "whole with paths",
			statement: SplitStatement{
				Donor: donor,
				Parts: []SplitPart{
					{Label: "a", Title: "A", Share: "A", Takes: SplitTakesWhole, Paths: []string{"a.go"}, Required: boolPtr(true)},
					{Label: "b", Title: "B", Share: "B", Required: boolPtr(true)},
				},
				Coverage: cov,
			},
			wantErr: "must not list paths or shared_files",
		},
		{
			name: "nothing with shared_files",
			statement: SplitStatement{
				Donor: donor,
				Parts: []SplitPart{
					{Label: "a", Title: "A", Share: "A", Takes: SplitTakesNothing, SharedFiles: []string{"a.go"}, Required: boolPtr(true)},
					{Label: "b", Title: "B", Share: "B", Required: boolPtr(true)},
				},
				Coverage: cov,
			},
			wantErr: "must not list paths or shared_files",
		},
		{
			name: "named without donor",
			statement: SplitStatement{
				Parts: []SplitPart{
					{Label: "a", Title: "A", Share: "A", Takes: SplitTakesNamed, Paths: []string{"a.go"}, Required: boolPtr(true)},
					{Label: "b", Title: "B", Share: "B", Required: boolPtr(true)},
				},
				Coverage: cov,
			},
			wantErr: "no donor sha",
		},
		{
			name: "named without paths",
			statement: SplitStatement{
				Donor: donor,
				Parts: []SplitPart{
					{Label: "a", Title: "A", Share: "A", Takes: SplitTakesNamed, Required: boolPtr(true)},
					{Label: "b", Title: "B", Share: "B", Required: boolPtr(true)},
				},
				Coverage: cov,
			},
			wantErr: "must list at least one path",
		},
		{
			name: "named with absolute path",
			statement: SplitStatement{
				Donor: donor,
				Parts: []SplitPart{
					{Label: "a", Title: "A", Share: "A", Takes: SplitTakesNamed, Paths: []string{"/etc/passwd"}, Required: boolPtr(true)},
					{Label: "b", Title: "B", Share: "B", Required: boolPtr(true)},
				},
				Coverage: cov,
			},
			wantErr: "invalid repo-relative path",
		},
		{
			name: "named with dotdot path",
			statement: SplitStatement{
				Donor: donor,
				Parts: []SplitPart{
					{Label: "a", Title: "A", Share: "A", Takes: SplitTakesNamed, Paths: []string{"../escape.go"}, Required: boolPtr(true)},
					{Label: "b", Title: "B", Share: "B", Required: boolPtr(true)},
				},
				Coverage: cov,
			},
			wantErr: "invalid repo-relative path",
		},
		{
			name: "named with blank shared_files",
			statement: SplitStatement{
				Donor: donor,
				Parts: []SplitPart{
					{Label: "a", Title: "A", Share: "A", Takes: SplitTakesNamed, Paths: []string{"a.go"}, SharedFiles: []string{" "}, Required: boolPtr(true)},
					{Label: "b", Title: "B", Share: "B", Required: boolPtr(true)},
				},
				Coverage: cov,
			},
			wantErr: "invalid repo-relative shared_files path",
		},
		{
			name: "blank inherited_findings",
			statement: SplitStatement{
				Donor: donor,
				Parts: []SplitPart{
					{Label: "a", Title: "A", Share: "A", InheritedFindings: []string{"  "}, Required: boolPtr(true)},
					{Label: "b", Title: "B", Share: "B", Required: boolPtr(true)},
				},
				Coverage: cov,
			},
			wantErr: "blank inherited_findings",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, parentID := newStoreWithParent(t)
			err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := FileSplit(ctx, tx, parentID, tc.statement, now)
				return err
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}
