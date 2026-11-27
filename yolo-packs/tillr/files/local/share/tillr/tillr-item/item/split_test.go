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
