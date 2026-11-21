package obstacles

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestNewHoldFormatsEveryObstacleKind(t *testing.T) {
	cases := []struct {
		kind          string
		fields        map[string]string
		checks        []string
		wantReason    string
		wantWhySub    []string
		wantMoverPref string
		wantMoverSub  string
	}{
		{
			kind:          "owner",
			fields:        map[string]string{"QUESTION": "Which exit code should unknown flags return?"},
			wantReason:    "obstacle-owner",
			wantWhySub:    []string{"Which exit code should unknown flags return?"},
			wantMoverPref: "owner; ",
			wantMoverSub:  "answer the question",
		},
		{
			kind:          "trunk",
			fields:        map[string]string{"CHECKS": "go-tool/unit, tillr-docs", "BASE": "abc1234"},
			checks:        []string{"go-tool/unit", "tillr-docs"},
			wantReason:    "obstacle-trunk",
			wantWhySub:    []string{"go-tool/unit, tillr-docs"},
			wantMoverPref: "drain; ",
			wantMoverSub:  "go-tool/unit, tillr-docs",
		},
		{
			kind:          "tooling",
			fields:        map[string]string{"COMMAND": "go test ./...", "EVIDENCE": "exec: gcc: executable file not found\nin $PATH", "PATHS": "tools/tillr-drain/"},
			wantReason:    "obstacle-tooling",
			wantWhySub:    []string{"go test ./...", "exec: gcc: executable file not found"},
			wantMoverPref: "operator; ",
			wantMoverSub:  "exec: gcc: executable file not found",
		},
		{
			kind:          "seat",
			fields:        map[string]string{"TRIED": "rewrote scheduler", "MISSING": "linearizability proof"},
			wantReason:    "obstacle-seat",
			wantWhySub:    []string{"tried: rewrote scheduler; missing: linearizability proof"},
			wantMoverPref: "operator; ",
			wantMoverSub:  "tried: rewrote scheduler; missing: linearizability proof",
		},
		{
			kind:          "definition",
			fields:        map[string]string{"CRITERIA": "request", "EVIDENCE": "flag --foo does not exist"},
			wantReason:    "obstacle-definition",
			wantWhySub:    []string{"flag --foo does not exist"},
			wantMoverPref: "operator; ",
			wantMoverSub:  "flag --foo does not exist",
		},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			h := NewHold("item-request-1", "run-1", tc.kind, tc.fields, tc.checks, "abc1234", []string{"tools/tillr-drain/"})
			if h.ReasonCode() != tc.wantReason {
				t.Fatalf("ReasonCode() = %q, want %q", h.ReasonCode(), tc.wantReason)
			}
			why := h.WhyText()
			for _, sub := range tc.wantWhySub {
				if !strings.Contains(why, sub) {
					t.Fatalf("WhyText() = %q, lacks %q", why, sub)
				}
			}
			mover := h.MoverText()
			if !strings.HasPrefix(mover, tc.wantMoverPref) || !strings.Contains(mover, tc.wantMoverSub) {
				t.Fatalf("MoverText() = %q, want prefix %q and %q", mover, tc.wantMoverPref, tc.wantMoverSub)
			}
		})
	}
}

func TestReadTracksReleaseAcrossOwnerUnblockAndTrunkClear(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "obstacles.sqlite3")
	st, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	appendEvt := func(t *testing.T, kind, objType, objID string, payload any, sec int) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO events (ts, kind, object_type, object_id, payload) VALUES (?, ?, ?, ?, ?)`,
			1000+sec, kind, objType, objID, string(raw)); err != nil {
			t.Fatal(err)
		}
	}

	readCheck := func(t *testing.T, fn func(r *store.Reader) error) {
		t.Helper()
		rs, err := store.OpenRead(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		defer rs.Close()
		if err := rs.ReadTx(ctx, fn); err != nil {
			t.Fatal(err)
		}
	}

	ownerHold := NewHold("item-owner", "run-owner-1", "owner", map[string]string{"QUESTION": "Proceed?"}, nil, "aaa", []string{"a/"})
	trunkHold := NewHold("item-trunk", "run-trunk-1", "trunk", map[string]string{"CHECKS": "go-tool/unit"}, []string{"go-tool/unit"}, "aaa", []string{"a/"})

	appendEvt(t, HeldKind, "item", "item-owner", ownerHold, 1)
	appendEvt(t, HeldKind, "item", "item-trunk", trunkHold, 3)

	// Unrelated metadata update on the trunk item keeps its hold.
	appendEvt(t, "item.update", "item", "item-trunk", map[string]any{"state": "in-queue"}, 4)

	readCheck(t, func(r *store.Reader) error {
		active, released, err := Read(r)
		if err != nil {
			return err
		}
		if len(active) != 2 || len(released) != 0 {
			t.Fatalf("active=%v released=%v", active, released)
		}
		return nil
	})

	// Owner unblock (item.update -> in-queue) and trunk clear release both.
	appendEvt(t, "item.update", "item", "item-owner", map[string]any{"state": "in-queue", "allowed_paths": []string{"a/"}}, 5)
	appendEvt(t, ClearedKind, "item", "item-trunk", ClearPayload{ItemID: "item-trunk", RunID: "run-trunk-1", Reason: "checks green"}, 7)

	readCheck(t, func(r *store.Reader) error {
		active, released, err := Read(r)
		if err != nil {
			return err
		}
		if len(active) != 0 {
			t.Fatalf("expected all holds cleared, got %v", active)
		}
		if released["item-owner"] != "run-owner-1" || released["item-trunk"] != "run-trunk-1" {
			t.Fatalf("released = %v", released)
		}
		return nil
	})

	// A historical fence hold is released without any operator edit to the item.
	appendEvt(t, HeldKind, "item", "item-fence", Hold{ItemID: "item-fence", RunID: "run-fence-1", Kind: "fence", Mover: "operator", Action: "widen allowed_paths"}, 8)
	readCheck(t, func(r *store.Reader) error {
		active, released, err := Read(r)
		if err != nil {
			return err
		}
		if _, held := active["item-fence"]; held || released["item-fence"] != "run-fence-1" {
			t.Fatalf("retired fence hold: active=%v released=%v", active, released)
		}
		return nil
	})

	// Allocating the next run consumes the released marker.
	appendEvt(t, "run.allocate", "run", "run-trunk-2", map[string]string{"item_id": "item-trunk", "run_id": "run-trunk-2"}, 9)
	readCheck(t, func(r *store.Reader) error {
		_, released, err := Read(r)
		if err != nil {
			return err
		}
		if _, still := released["item-trunk"]; still {
			t.Fatalf("expected item-trunk release consumed after run.allocate: %v", released)
		}
		return nil
	})
}

func TestReadOmitsCloseRemedyWhenItemHasLandedRow(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "obstacles-landed.sqlite3")
	st, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE landings (landing_id TEXT PRIMARY KEY, item_id TEXT NOT NULL, result TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO landings (landing_id, item_id, result) VALUES ('l1', 'item-landed-owner', 'landed'), ('l2', 'item-landed-def', 'landed'), ('l3', 'item-refused', 'refused')`); err != nil {
		t.Fatal(err)
	}

	for i, h := range []Hold{
		NewHold("item-landed-owner", "run-lo-2", "owner", map[string]string{"QUESTION": "Keep old format?"}, nil, "aaa", []string{"a/"}),
		NewHold("item-landed-def", "run-ld-2", "definition", map[string]string{"CRITERIA": "request", "EVIDENCE": "flag removed"}, nil, "aaa", []string{"a/"}),
		NewHold("item-refused", "run-rf-2", "owner", map[string]string{"QUESTION": "Keep old format?"}, nil, "aaa", []string{"a/"}),
	} {
		raw, err := json.Marshal(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO events (ts, kind, object_type, object_id, payload) VALUES (?, ?, 'item', ?, ?)`,
			2000+i, HeldKind, h.ItemID, string(raw)); err != nil {
			t.Fatal(err)
		}
	}

	rs, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if err := rs.ReadTx(ctx, func(r *store.Reader) error {
		active, _, err := Read(r)
		if err != nil {
			return err
		}
		for _, id := range []string{"item-landed-owner", "item-landed-def"} {
			got := active[id].MoverText()
			if strings.Contains(got, "close") {
				t.Errorf("%s still offers close after landed row: %q", id, got)
			}
			if !strings.Contains(got, "tillr drain retry --item "+id) {
				t.Errorf("%s missing working retry command: %q", id, got)
			}
		}
		if !strings.Contains(active["item-refused"].MoverText(), "tillr item close --item item-refused") {
			t.Errorf("unlanded item lost close remedy: %q", active["item-refused"].MoverText())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadIgnoresPartialLandingsSchemaWithoutItemID(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "obstacles-partial-landings.sqlite3")
	st, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Matches synthetic heavy-status fixtures that create landings(trunk_after, result) without item_id.
	if _, err := db.Exec(`CREATE TABLE landings(trunk_after TEXT, result TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO landings(trunk_after, result) VALUES ('abc123', 'landed')`); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(NewHold("item-partial", "run-p1", "owner", map[string]string{"QUESTION": "Keep?"}, nil, "aaa", []string{"a/"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events (ts, kind, object_type, object_id, payload) VALUES (2000, ?, 'item', 'item-partial', ?)`, HeldKind, string(raw)); err != nil {
		t.Fatal(err)
	}

	rs, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if err := rs.ReadTx(ctx, func(r *store.Reader) error {
		active, _, err := Read(r)
		if err != nil {
			return err
		}
		if _, ok := active["item-partial"]; !ok {
			t.Fatalf("missing active hold: %+v", active)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
