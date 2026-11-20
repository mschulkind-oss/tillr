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
			kind:          "fence",
			fields:        map[string]string{"PATHS": "tools/tillr-item/item/waiting.go", "EVIDENCE": "checkWaitingOn lives outside allowed_paths"},
			wantReason:    "obstacle-fence",
			wantWhySub:    []string{"tools/tillr-item/item/waiting.go", "checkWaitingOn lives outside allowed_paths"},
			wantMoverPref: "operator; ",
			wantMoverSub:  "tools/tillr-item/item/waiting.go",
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

func TestReadTracksReleaseAcrossOwnerUnblockFenceWidenAndTrunkClear(t *testing.T) {
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
	fenceHold := NewHold("item-fence", "run-fence-1", "fence", map[string]string{"PATHS": "b/"}, nil, "aaa", []string{"a/"})
	trunkHold := NewHold("item-trunk", "run-trunk-1", "trunk", map[string]string{"CHECKS": "go-tool/unit"}, []string{"go-tool/unit"}, "aaa", []string{"a/"})

	appendEvt(t, HeldKind, "item", "item-owner", ownerHold, 1)
	appendEvt(t, HeldKind, "item", "item-fence", fenceHold, 2)
	appendEvt(t, HeldKind, "item", "item-trunk", trunkHold, 3)

	// Unrelated metadata update on fence item with unchanged allowed_paths keeps hold.
	appendEvt(t, "item.update", "item", "item-fence", map[string]any{"state": "in-queue", "allowed_paths": []string{"a/"}}, 4)

	readCheck(t, func(r *store.Reader) error {
		active, released, err := Read(r)
		if err != nil {
			return err
		}
		if len(active) != 3 || len(released) != 0 {
			t.Fatalf("active=%v released=%v", active, released)
		}
		return nil
	})

	// Owner unblock (item.update -> in-queue), fence widen (allowed_paths changed), and trunk clear release all three.
	appendEvt(t, "item.update", "item", "item-owner", map[string]any{"state": "in-queue", "allowed_paths": []string{"a/"}}, 5)
	appendEvt(t, "item.update", "item", "item-fence", map[string]any{"state": "in-queue", "allowed_paths": []string{"a/", "b/"}}, 6)
	appendEvt(t, ClearedKind, "item", "item-trunk", ClearPayload{ItemID: "item-trunk", RunID: "run-trunk-1", Reason: "checks green"}, 7)

	readCheck(t, func(r *store.Reader) error {
		active, released, err := Read(r)
		if err != nil {
			return err
		}
		if len(active) != 0 {
			t.Fatalf("expected all holds cleared, got %v", active)
		}
		if released["item-owner"] != "run-owner-1" || released["item-fence"] != "run-fence-1" || released["item-trunk"] != "run-trunk-1" {
			t.Fatalf("released = %v", released)
		}
		return nil
	})

	// Allocating the next run consumes the released marker.
	appendEvt(t, "run.allocate", "run", "run-trunk-2", map[string]string{"item_id": "item-trunk", "run_id": "run-trunk-2"}, 8)
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
