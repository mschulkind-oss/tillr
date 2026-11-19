package main_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRequestBlockedByFlag(t *testing.T) {
	path, s := placeFixture(t, "a", "b")
	root := scratch(t, "request-dep-root")
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "tillr", "workflows", "planned-build@v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "config/tillr/workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config/tillr/workflows/planned-build@v1.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	const words = "Implement feature after items 0 and 1 land.\n"
	args := []string{
		"--store", path, "--root", root,
		"--title", "Wait on 0 and 1",
		"--path", "tools/tillr-item/",
		"--blocked-by", "1",
		"--blocked-by", "0",
		"--now", "2026-11-19T10:00:00Z",
	}
	code, out := requestRun(t, words, args...)
	if code != 0 {
		t.Fatalf("request with --blocked-by: %d: %s", code, out)
	}
	if !strings.Contains(out, `"blocked_by":["0","1"]`) {
		t.Fatalf("request output missing normalized blocked_by: %s", out)
	}

	var env struct {
		Payload struct {
			ItemID    string   `json:"item_id"`
			BlockedBy []string `json:"blocked_by"`
		} `json:"payload"`
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "{") {
			if err := json.Unmarshal([]byte(line), &env); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if env.Payload.ItemID == "" {
		t.Fatalf("missing JSON envelope in output: %s", out)
	}
	var edges []string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT blocked_by FROM item_edges WHERE item_id = ? ORDER BY blocked_by`, env.Payload.ItemID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var dep string
			if err := rows.Scan(&dep); err != nil {
				return err
			}
			edges = append(edges, dep)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(edges, []string{"0", "1"}) {
		t.Fatalf("stored edges = %v, want [0 1]", edges)
	}

	// Refiling with the same deps in a different order and duplicated is idempotent.
	refileArgs := []string{
		"--store", path, "--root", root,
		"--title", "Wait on 0 and 1",
		"--path", "tools/tillr-item/",
		"--blocked-by", "0",
		"--blocked-by", "1",
		"--blocked-by", "0",
		"--now", "2026-11-19T10:01:00Z",
	}
	code, again := requestRun(t, words, refileArgs...)
	if code != 0 || !strings.Contains(again, env.Payload.ItemID) {
		t.Fatalf("idempotent refile failed: %d: %s", code, again)
	}

	// Refiling the same words with different deps is refused.
	diffArgs := []string{
		"--store", path, "--root", root,
		"--title", "Wait on 0 and 1",
		"--path", "tools/tillr-item/",
		"--blocked-by", "0",
		"--now", "2026-11-19T10:02:00Z",
	}
	code, out = requestRun(t, words, diffArgs...)
	if code == 0 || !strings.Contains(out, "request-differs") {
		t.Fatalf("refile with different deps: %d: %s", code, out)
	}

	// Refusing a missing blocker writes nothing to the store.
	const badWords = "This request names a missing blocker.\n"
	code, out = requestRun(t, badWords,
		"--store", path, "--root", root,
		"--title", "Missing blocker",
		"--path", "tools/tillr-item/",
		"--blocked-by", "item-does-not-exist",
		"--now", "2026-11-19T10:03:00Z",
	)
	if code != 1 || !strings.Contains(out, "edge-target-missing") {
		t.Fatalf("missing blocker: %d: %s", code, out)
	}
	var count int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM items WHERE title = 'Missing blocker'`).Scan(&count)
	}); err != nil || count != 0 {
		t.Fatalf("missing blocker wrote item: count=%d err=%v", count, err)
	}
}

func TestDepFaceAddRemoveAndPreservesFields(t *testing.T) {
	db := filepath.Join(scratch(t, "dep-face"), "board.sqlite3")
	for _, id := range []string{"dep-a", "dep-b", "dep-c"} {
		it := base(id, "dependency "+id)
		it.Rank = "0" + string(id[len(id)-1])
		if code, out := apply(t, db, envelope(t, "item.record", "setup-"+id, it)); code != 0 {
			t.Fatalf("apply %s: %d: %s", id, code, out)
		}
	}
	target := base("target", "item whose deps change")
	target.Rank = "b"
	target.BlockedBy = []string{"dep-a"}
	if code, out := apply(t, db, envelope(t, "item.record", "setup-target", target)); code != 0 {
		t.Fatalf("apply target: %d: %s", code, out)
	}

	itemQuery := `SELECT * FROM items WHERE item_id = ?`
	before := rankStoredRows(t, db, itemQuery, "target")
	if len(before) != 1 {
		t.Fatalf("expected 1 target row, got %d", len(before))
	}

	// Add dep-b and dep-c, remove dep-a.
	code, out := run(t, "dep", "--store", db, "--item", "target",
		"--add", "dep-b", "--add", "dep-c", "--remove", "dep-a", "--now", "2026-11-19T11:00:00Z")
	if code != 0 {
		t.Fatalf("dep add/remove: %d: %s", code, out)
	}
	got := decode(t, out)
	if !slices.Equal(got.Payload.BlockedBy, []string{"dep-b", "dep-c"}) {
		t.Fatalf("dep echo blocked_by = %v, want [dep-b dep-c]", got.Payload.BlockedBy)
	}
	if !strings.Contains(string(out), `"rank":"b"`) {
		t.Fatalf("dep echo missing rank b: %s", out)
	}

	after := rankStoredRows(t, db, itemQuery, "target")
	for column, value := range before[0] {
		gotVal, present := after[0][column]
		switch column {
		case "updated_at", "event_seq":
			if reflect.DeepEqual(value, gotVal) {
				t.Errorf("%s did not advance: %v", column, gotVal)
			}
		default:
			if !present || !reflect.DeepEqual(value, gotVal) {
				t.Errorf("%s changed: %v -> %v", column, value, gotVal)
			}
		}
	}

	// Check that item.update event was appended.
	conn, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	var eventCount int
	if err := conn.QueryRow(`SELECT count(*) FROM events WHERE object_id = 'target' AND kind = 'item.update'`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if eventCount != 1 {
		t.Fatalf("item.update event count = %d, want 1", eventCount)
	}

	// Idempotent no-op appends no event.
	code, out = run(t, "dep", "--store", db, "--item", "target",
		"--add", "dep-b", "--remove", "dep-a", "--now", "2026-11-19T11:01:00Z")
	if code != 0 {
		t.Fatalf("dep idempotent no-op: %d: %s", code, out)
	}
	conn, err = sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	var eventCountAfter int
	if err := conn.QueryRow(`SELECT count(*) FROM events WHERE object_id = 'target' AND kind = 'item.update'`).Scan(&eventCountAfter); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if eventCountAfter != 1 {
		t.Fatalf("idempotent dep appended event: %d -> %d", eventCount, eventCountAfter)
	}

	// Refuses unknown item, missing target, self-edge, and cycle.
	for _, tc := range []struct {
		name string
		args []string
		rule string
	}{
		{"unknown-item", []string{"dep", "--store", db, "--item", "missing", "--add", "dep-a"}, "not-found"},
		{"missing-add-target", []string{"dep", "--store", db, "--item", "target", "--add", "missing"}, "edge-target-missing"},
		{"missing-remove-target", []string{"dep", "--store", db, "--item", "target", "--remove", "missing"}, "edge-target-missing"},
		{"self-edge", []string{"dep", "--store", db, "--item", "target", "--add", "target"}, "edge-self"},
		{"cycle", []string{"dep", "--store", db, "--item", "dep-b", "--add", "target"}, "edge-cycle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := run(t, tc.args...)
			if code != 1 {
				t.Fatalf("exit %d, want 1: %s", code, out)
			}
			rej := decode(t, out)
			if rej.Kind != "item.rejection" || rej.Payload.Rule != tc.rule {
				t.Fatalf("got %s (rule %q), want rule %q", out, rej.Payload.Rule, tc.rule)
			}
		})
	}

	if exitOf(exec.Command(bin, "dep", "--help").Run()) != 0 {
		t.Fatal("dep --help failed")
	}
}

func TestShowToApplyRoundTripPreservesPlaceAndRank(t *testing.T) {
	db := filepath.Join(scratch(t, "show-apply-roundtrip"), "board.sqlite3")
	for _, spec := range []struct{ id, rank string }{
		{"first", "a"},
		{"second", "b"},
		{"third", "c"},
	} {
		it := base(spec.id, "item "+spec.id)
		it.State = "in-queue"
		it.Rank = spec.rank
		if code, out := apply(t, db, envelope(t, "item.record", "setup-"+spec.id, it)); code != 0 {
			t.Fatalf("apply %s: %d: %s", spec.id, code, out)
		}
	}

	// Show second: it has "place": 2 and omits "rank".
	code, shown := run(t, "show", "--store", db, "--item", "second")
	if code != 0 {
		t.Fatalf("show second: %d: %s", code, shown)
	}
	if !strings.Contains(string(shown), `"place":2`) || strings.Contains(string(shown), `"rank"`) {
		t.Fatalf("unexpected show shape: %s", shown)
	}

	// Feed show's output directly back to apply unchanged.
	code, out := apply(t, db, string(shown))
	if code != 0 {
		t.Fatalf("round-trip apply failed: %d: %s", code, out)
	}
	code, shownAfter := run(t, "show", "--store", db, "--item", "second")
	if code != 0 {
		t.Fatalf("show second after round-trip: %d: %s", code, shownAfter)
	}
	if !strings.Contains(string(shownAfter), `"place":2`) {
		t.Fatalf("place changed after round-trip apply: %s", shownAfter)
	}

	// Edit blocked_by on show's output (keeping "place": 2, omitting "rank") and apply.
	var shownEnv map[string]any
	if err := json.Unmarshal(shown, &shownEnv); err != nil {
		t.Fatal(err)
	}
	payload := shownEnv["payload"].(map[string]any)
	payload["blocked_by"] = []string{"first"}
	edited, err := json.Marshal(shownEnv)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := apply(t, db, string(edited)); code != 0 {
		t.Fatalf("edited round-trip apply failed: %d: %s", code, out)
	}
	code, shownEdited := run(t, "show", "--store", db, "--item", "second")
	if code != 0 {
		t.Fatalf("show second after edited round-trip: %d: %s", code, shownEdited)
	}
	if !strings.Contains(string(shownEdited), `"place":2`) || !strings.Contains(string(shownEdited), `"blocked_by":["first"]`) {
		t.Fatalf("place or blocked_by lost after edited round-trip: %s", shownEdited)
	}

	// Explicit "rank": "" still clears rank (moves second after third -> place 3).
	payload["rank"] = ""
	cleared, err := json.Marshal(shownEnv)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := apply(t, db, string(cleared)); code != 0 {
		t.Fatalf("explicit empty rank apply failed: %d: %s", code, out)
	}
	code, shownCleared := run(t, "show", "--store", db, "--item", "second")
	if code != 0 {
		t.Fatalf("show second after clearing rank: %d: %s", code, shownCleared)
	}
	if !strings.Contains(string(shownCleared), `"place":3`) {
		t.Fatalf("explicit empty rank did not clear rank/move to place 3: %s", shownCleared)
	}

	// Finished (landed) ranked item round-trip via show -> apply succeeds unchanged.
	landed := base("done", "finished ranked item")
	landed.State = "landed"
	landed.Rank = "d"
	landed.Criteria[0].State = "pass"
	if code, out := apply(t, db, envelope(t, "item.record", "setup-done", landed)); code != 0 {
		t.Fatalf("apply landed: %d: %s", code, out)
	}
	code, shownLanded := run(t, "show", "--store", db, "--item", "done")
	if code != 0 {
		t.Fatalf("show landed: %d: %s", code, shownLanded)
	}
	if code, out := apply(t, db, string(shownLanded)); code != 0 {
		t.Fatalf("round-trip apply on landed ranked item failed: %d: %s", code, out)
	}
}
