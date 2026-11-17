package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func terminalSnapshot(t *testing.T, db, id string) []byte {
	t.Helper()
	code, out := run(t, "show", "--store", db, "--item", id)
	if code != 0 {
		t.Fatalf("show exited %d: %s", code, out)
	}
	return out
}

func TestTerminalStateMoves(t *testing.T) {
	for _, from := range []string{"landed", "resolved-elsewhere"} {
		for _, to := range []string{"not-started", "blocked", "in-queue", "landed", "resolved-elsewhere", "closed", "abandoned", "deleted", "in-progress"} {
			t.Run(from+"/"+to, func(t *testing.T) {
				db := filepath.Join(scratch(t, "terminal-state"), "state.db")
				p := base("item_042", "finished work")
				p.State = from
				if code, out := apply(t, db, envelope(t, "item.record", "seed", p)); code != 0 {
					t.Fatalf("seed: %d %s", code, out)
				}
				before := terminalSnapshot(t, db, p.ItemID)
				updates := eventsOfKind(t, db, "item.update")
				code, out := state(t, db, p.ItemID, to, "2026-11-10T00:10:00Z")
				if to == from {
					if code != 0 {
						t.Fatalf("same state: %d %s", code, out)
					}
				} else {
					assertRefused(t, code, out, "state-terminal")
					got := decode(t, out)
					if got.Payload.ItemID != p.ItemID || !strings.Contains(got.Payload.Reason, from) {
						t.Fatalf("refusal omits item or state: %s", out)
					}
				}
				if after := terminalSnapshot(t, db, p.ItemID); !bytes.Equal(before, after) {
					t.Fatalf("state changed stored item: %s", after)
				}
				if eventsOfKind(t, db, "item.update") != updates {
					t.Fatal("state appended an event")
				}
			})
		}
	}
}

func TestTerminalApplyChanges(t *testing.T) {
	cases := []struct {
		name, rule string
		edit       func(map[string]any)
	}{
		{"same", "", func(p map[string]any) {}},
		{"state", "state-terminal", func(p map[string]any) { p["state"] = "in-queue" }},
		{"title", "state-terminal", func(p map[string]any) { p["title"] = "rewritten" }},
		{"body", "state-terminal", func(p map[string]any) { p["body_ref"] = "other.md" }},
		{"design", "state-terminal", func(p map[string]any) { p["design_doc"] = "other.md" }},
		{"rank", "state-terminal", func(p map[string]any) { p["rank"] = "a" }},
		{"seat", "state-terminal", func(p map[string]any) { p["seat"] = "build" }},
		{"prose", "state-terminal", func(p map[string]any) { p["prose"] = "2" }},
		{"workflow", "state-terminal", func(p map[string]any) { p["workflow"] = "other" }},
		{"blockers", "state-terminal", func(p map[string]any) { p["blocked_by"] = []string{"item_other"} }},
		{"fence", "state-terminal", func(p map[string]any) { p["allowed_paths"] = []string{"tools/"} }},
		{"new criterion", "state-terminal", func(p map[string]any) {
			p["criteria"] = []criterion{{"c1", "the first criterion", "open", ""}, {"c2", "new", "", ""}}
		}},
		{"criterion state", "criterion-state", func(p map[string]any) { p["criteria"] = []criterion{{"c1", "the first criterion", "done", ""}} }},
		{"criterion text", "criterion-frozen", func(p map[string]any) { p["criteria"] = []criterion{{"c1", "changed", "open", ""}} }},
		{"criterion removed", "criterion-frozen", func(p map[string]any) { p["criteria"] = []criterion{} }},
	}
	for _, from := range []string{"landed", "resolved-elsewhere"} {
		for _, c := range cases {
			for _, mixed := range []bool{false, true} {
				if mixed && !strings.HasPrefix(c.rule, "criterion-") {
					continue
				}
				t.Run(from+"/"+c.name+map[bool]string{true: "/mixed"}[mixed], func(t *testing.T) {
					db := filepath.Join(scratch(t, "terminal-apply"), "state.db")
					p := base("item_042", "finished work")
					p.State = from
					if code, out := apply(t, db, envelope(t, "item.record", "seed", p)); code != 0 {
						t.Fatalf("seed: %d %s", code, out)
					}
					before := terminalSnapshot(t, db, p.ItemID)
					var env struct {
						Payload map[string]any `json:"payload"`
					}
					if err := json.Unmarshal(before, &env); err != nil {
						t.Fatal(err)
					}
					c.edit(env.Payload)
					if mixed {
						env.Payload["title"] = "also changed"
						env.Payload["state"] = "deleted"
						env.Payload["allowed_paths"] = []string{"*"}
						env.Payload["rank"] = "!"
						env.Payload["seat"] = "unknown"
					}
					updates := eventsOfKind(t, db, "item.update")
					code, out := apply(t, db, envelope(t, "item.record", "edit", env.Payload))
					if c.rule == "" {
						if code != 0 {
							t.Fatalf("identical apply: %d %s", code, out)
						}
					} else {
						assertRefused(t, code, out, c.rule)
					}
					if after := terminalSnapshot(t, db, p.ItemID); !bytes.Equal(before, after) {
						t.Fatalf("apply changed stored item: %s", after)
					}
					if eventsOfKind(t, db, "item.update") != updates {
						t.Fatal("apply appended an event")
					}
				})
			}
		}
	}
}

func TestTerminalProductQAReopening(t *testing.T) {
	db := filepath.Join(scratch(t, "product-qa"), "state.db")
	p := base("item_042", "original title")
	if code, out := apply(t, db, envelope(t, "item.record", "seed", p)); code != 0 {
		t.Fatalf("seed: %d %s", code, out)
	}
	if code, out := state(t, db, p.ItemID, "landed", "2026-11-10T00:10:00Z"); code != 0 {
		t.Fatalf("land: %d %s", code, out)
	}
	before := terminalSnapshot(t, db, p.ItemID)
	p.State, p.Title = "in-queue", "new title"
	code, out := apply(t, db, envelope(t, "item.record", "reopen", p))
	assertRefused(t, code, out, "state-terminal")
	if after := terminalSnapshot(t, db, p.ItemID); !bytes.Equal(before, after) {
		t.Fatalf("reopened: %s", after)
	}
}

func TestTerminalSyncSkipsStaleBlockAndSyncsSibling(t *testing.T) {
	for _, st := range []string{"landed", "resolved-elsewhere"} {
		t.Run(st, func(t *testing.T) { terminalSyncSibling(t, st) })
	}
}

func terminalSyncSibling(t *testing.T, st string) {
	t.Helper()
	repo := syncRepo(t, map[string]string{"docs/items.md": syncGood})
	db := filepath.Join(scratch(t, "terminal-sync"), "state.db")
	if code, out := run(t, "sync", "--store", db, "--root", repo, "--json"); code != 0 {
		t.Fatalf("sync: %d %s", code, out)
	}
	if code, out := state(t, db, "item-good", st, "2026-11-10T00:10:00Z"); code != 0 {
		t.Fatalf("land: %d %s", code, out)
	}
	snapshot := func() []byte {
		code, out := run(t, "show", "--store", db, "--root", repo, "--item", "item-good")
		if code != 0 {
			t.Fatalf("show: %d %s", code, out)
		}
		return out
	}
	before := snapshot()
	repo = syncRepo(t, map[string]string{"docs/items.md": strings.Replace(syncGood, "### Good", "### Changed", 1) + strings.ReplaceAll(syncGood, "item-good", "item-sibling")})
	updates := eventsOfKind(t, db, "item.update")
	code, out := run(t, "sync", "--store", db, "--root", repo, "--json")
	if code != 0 {
		t.Fatalf("sync: %d %s", code, out)
	}
	got := decodeSync(t, out)
	wantStale := 0
	if st == "landed" {
		wantStale = 1
	}
	if got.Payload.StaleDocBlocks != wantStale || strings.Join(got.Payload.Created, ",") != "item-sibling" {
		t.Fatalf("sync report: %s", out)
	}
	if after := snapshot(); !bytes.Equal(before, after) {
		t.Fatalf("stale item changed: %s", after)
	}
	if eventsOfKind(t, db, "item.update") != updates {
		t.Fatal("sync appended stale update")
	}
	if stateRow(t, db, "item-sibling") != "in-queue" {
		t.Fatal("sibling was not synced")
	}
}

func TestTerminalLoopVerdictsThenLand(t *testing.T) {
	db := storedWithCriterion(t, "terminal-loop", "open")
	if code, out := state(t, db, "item_042", "in-queue", "2026-11-10T00:09:00Z"); code != 0 {
		t.Fatalf("queue: %d %s", code, out)
	}
	if code, out := recordVerdict(t, db, mechanicalVerdict()); code != 0 {
		t.Fatalf("verdict: %d %s", code, out)
	}
	if code, out := state(t, db, "item_042", "landed", "2026-11-10T00:10:00Z"); code != 0 {
		t.Fatalf("land: %d %s", code, out)
	}
	got := decode(t, terminalSnapshot(t, db, "item_042"))
	if got.Payload.State != "landed" || got.Payload.Criteria[0].State != "done" {
		t.Fatalf("loop result: %+v", got.Payload)
	}
}

// Insert the rows directly, as a pre-change store did, rather than exercising the new writer.
func TestTerminalOldStoreRemainsReadable(t *testing.T) {
	db := filepath.Join(scratch(t, "terminal-old-store"), "state.db")
	ctx := context.Background()
	s, err := store.Open(ctx, db, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, st := range []string{"landed", "resolved-elsewhere"} {
			if _, err := tx.Exec(`INSERT INTO items (item_id, title, state, updated_at) VALUES (?, ?, ?, 0)`, st, "old "+st, st); err != nil {
				return err
			}
		}
		return nil
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Fingerprint(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []string{"landed", "resolved-elsewhere"} {
		got := decode(t, terminalSnapshot(t, db, st))
		if got.Payload.State != st || got.Payload.Title != "old "+st {
			t.Fatalf("legacy row changed: %+v", got.Payload)
		}
	}
	after, err := store.Fingerprint(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("reads changed old store")
	}
}
