package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

const findingsDefinition = `{"definition":"findings","version":1,"states":["queued","waiting","archived","confirmed","rejected"],"ready":"queued","blocked":"waiting","closed":"archived","finished":["confirmed","rejected"],"transitions":[{"from":"queued","to":"confirmed"},{"from":"queued","to":"rejected"},{"from":"rejected","to":"queued"},{"from":"queued","to":"waiting"},{"from":"waiting","to":"queued"},{"from":"queued","to":"archived"},{"from":"waiting","to":"archived"},{"from":"archived","to":"queued"},{"from":"archived","to":"waiting"}],"completes":"landing"}`

func findingsStore(t *testing.T) string {
	t.Helper()
	db := filepath.Join(scratch(t, "definition-lifecycle"), "state.db")
	s, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := definition.Set(context.Background(), s, []byte(findingsDefinition)); err != nil {
		t.Fatal(err)
	}
	return db
}

func findingsItem(t *testing.T, db, id string) {
	t.Helper()
	p := base(id, "a finding")
	p.State = "queued"
	if code, out := apply(t, db, envelope(t, "item.record", id, p)); code != 0 {
		t.Fatalf("apply fixture: %d %s", code, out)
	}
}

func findingState(t *testing.T, db, id, target string, extra ...string) (int, []byte) {
	t.Helper()
	return run(t, append([]string{"state", "--store", db, "--item", id, "--state", target, "--now", "2026-11-17T12:00:00Z"}, extra...)...)
}

func assertLifecycleEvent(t *testing.T, db, id string) {
	t.Helper()
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var raw string
		if err := r.QueryRow(`SELECT payload FROM events WHERE object_id = ? AND kind = 'item.update' ORDER BY seq DESC LIMIT 1`, id).Scan(&raw); err != nil {
			return err
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return err
		}
		if p["lifecycle_reason"] != "new evidence" || p["lifecycle_by"] != "reviewer" {
			t.Fatalf("receipt not recorded: %s", raw)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDefinitionFinishedMoves(t *testing.T) {
	db := findingsStore(t)
	findingsItem(t, db, "confirmed-item")
	if code, out := findingState(t, db, "confirmed-item", "confirmed"); code != 0 {
		t.Fatalf("confirm: %d %s", code, out)
	}
	code, out := findingState(t, db, "confirmed-item", "queued", "--reason", "new evidence", "--by", "reviewer")
	assertRefused(t, code, out, "transition-not-allowed")
	for _, word := range []string{"confirmed", "queued", "findings"} {
		if !strings.Contains(string(out), word) {
			t.Fatalf("missing %s: %s", word, out)
		}
	}
	findingsItem(t, db, "rejected-item")
	if code, out := findingState(t, db, "rejected-item", "rejected"); code != 0 {
		t.Fatalf("reject: %d %s", code, out)
	}
	for _, extra := range [][]string{nil, {"--reason", "new evidence"}, {"--by", "reviewer"}, {"--reason", " ", "--by", "reviewer"}, {"--reason", "new evidence", "--by", " "}} {
		code, out := findingState(t, db, "rejected-item", "queued", extra...)
		if code == 0 {
			t.Fatalf("reopened without complete receipt: %s", out)
		}
		if stateRow(t, db, "rejected-item") != "rejected" {
			t.Fatal("refusal moved item")
		}
	}
	if code, out := findingState(t, db, "rejected-item", "queued", "--reason", "new evidence", "--by", "reviewer"); code != 0 {
		t.Fatalf("reopen: %d %s", code, out)
	}
	assertLifecycleEvent(t, db, "rejected-item")
}

func TestDefinitionApplyMoves(t *testing.T) {
	db := findingsStore(t)
	findingsItem(t, db, "finding")
	p := base("finding", "a finding")
	p.State = "rejected"
	if code, out := apply(t, db, envelope(t, "item.record", "reject", p)); code != 0 {
		t.Fatalf("apply reject: %d %s", code, out)
	}
	p.State = "queued"
	code, out := apply(t, db, envelope(t, "item.record", "reopen", p))
	if code == 0 {
		t.Fatalf("apply reopened without receipt: %s", out)
	}
	input := filepath.Join(scratch(t, "apply-receipt"), "reopen.json")
	if err := os.WriteFile(input, []byte(envelope(t, "item.record", "reopen-with-receipt", p)), 0600); err != nil {
		t.Fatal(err)
	}
	code, out = run(t, "apply", "--store", db, "--input", input, "--now", "2026-11-17T12:00:00Z", "--reason", "new evidence", "--by", "reviewer")
	if code != 0 {
		t.Fatalf("apply reopen: %d %s", code, out)
	}
	assertLifecycleEvent(t, db, "finding")
	p.State = "waiting"
	if code, out := apply(t, db, envelope(t, "item.record", "wait", p)); code != 0 {
		t.Fatalf("apply wait: %d %s", code, out)
	}
	p.State = "confirmed"
	code, out = apply(t, db, envelope(t, "item.record", "unlisted", p))
	assertRefused(t, code, out, "transition-not-allowed")
	p.State = "queued"
	if code, out := apply(t, db, envelope(t, "item.record", "ready", p)); code != 0 {
		t.Fatalf("apply ready: %d %s", code, out)
	}
	p.State = "confirmed"
	if code, out := apply(t, db, envelope(t, "item.record", "confirm", p)); code != 0 {
		t.Fatalf("apply confirm: %d %s", code, out)
	}
	p.State = "queued"
	code, out = apply(t, db, envelope(t, "item.record", "forbidden", p))
	assertRefused(t, code, out, "transition-not-allowed")
}

func TestDefinitionRolesAndAllowedMoves(t *testing.T) {
	db := findingsStore(t)
	findingsItem(t, db, "item_042")
	if code, out := findingState(t, db, "item_042", "waiting", "--waiter", "owner", "--reason", "decision"); code != 0 {
		t.Fatalf("block: %d %s", code, out)
	}
	code, out := findingState(t, db, "item_042", "confirmed")
	assertRefused(t, code, out, "transition-not-allowed")
	if code, out := lifecycle(t, db, "close", "not wanted"); code != 0 || lifecyclePayload(t, out)["state"] != "archived" {
		t.Fatalf("close: %d %s", code, out)
	}
	if code, out := lifecycle(t, db, "reopen", "wanted"); code != 0 || lifecyclePayload(t, out)["state"] != "waiting" {
		t.Fatalf("reopen: %d %s", code, out)
	}
	if code, out := findingState(t, db, "item_042", "queued"); code != 0 || lifecyclePayload(t, out)["waiting_on"] != nil {
		t.Fatalf("ready: %d %s", code, out)
	}
	if code, out := findingState(t, db, "item_042", "rejected"); code != 0 {
		t.Fatalf("reject: %d %s", code, out)
	}
	code, out = lifecycle(t, db, "close", "not wanted")
	assertRefused(t, code, out, "transition-not-allowed")
}

func TestDefinitionReopenRequiresListedMove(t *testing.T) {
	db := findingsStore(t)
	findingsItem(t, db, "item_042")
	if code, out := lifecycle(t, db, "close", "not wanted"); code != 0 {
		t.Fatalf("close: %d %s", code, out)
	}
	s, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Replace(findingsDefinition, `,{"from":"archived","to":"queued"}`, "", 1)
	_, err = definition.Set(context.Background(), s, []byte(content))
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	code, out := lifecycle(t, db, "reopen", "wanted")
	assertRefused(t, code, out, "transition-not-allowed")
	if stateRow(t, db, "item_042") != "archived" {
		t.Fatal("refused reopen moved item")
	}
}

func TestDefinitionSyncFinishedStates(t *testing.T) {
	db := findingsStore(t)
	repo := syncRepo(t, map[string]string{"docs/items.md": syncGood})
	if code, out := run(t, "sync", "--store", db, "--root", repo, "--json"); code != 0 {
		t.Fatalf("sync: %d %s", code, out)
	}
	if stateRow(t, db, "item-good") != "queued" {
		t.Fatal("sync did not use ready role")
	}
	if code, out := findingState(t, db, "item-good", "confirmed"); code != 0 {
		t.Fatalf("confirm: %d %s", code, out)
	}
	empty := syncRepo(t, map[string]string{"docs/empty.md": "# Empty\n"})
	code, out := run(t, "sync", "--store", db, "--root", empty, "--json")
	if code != 0 || decodeSync(t, out).Payload.UnsharedOpen != 0 {
		t.Fatalf("finished residue: %d %s", code, out)
	}
	repo = syncRepo(t, map[string]string{"docs/items.md": strings.Replace(syncGood, "### Good", "### Changed", 1)})
	code, out = run(t, "sync", "--store", db, "--root", repo, "--json")
	if code != 0 || !strings.Contains(string(out), `"state-terminal"`) {
		t.Fatalf("finished edit: %d %s", code, out)
	}
}

func TestDefinitionSyncDoesNotAssumeLandedIsFinished(t *testing.T) {
	db := findingsStore(t)
	s, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	// This use calls its ready state landed; that spelling must not imply completion.
	content := strings.ReplaceAll(findingsDefinition, "queued", "landed")
	_, err = definition.Set(context.Background(), s, []byte(content))
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	repo := syncRepo(t, map[string]string{"docs/items.md": syncGood})
	if code, out := run(t, "sync", "--store", db, "--root", repo, "--json"); code != 0 {
		t.Fatalf("initial sync: %d %s", code, out)
	}
	if stateRow(t, db, "item-good") != "landed" {
		t.Fatal("sync did not use the renamed ready state")
	}
	repo = syncRepo(t, map[string]string{"docs/items.md": strings.Replace(syncGood, "### Good", "### Changed", 1)})
	code, out := run(t, "sync", "--store", db, "--root", repo, "--json")
	if code != 0 || decodeSync(t, out).Payload.StaleDocBlocks != 0 {
		t.Fatalf("unfinished item classified as stale: %d %s", code, out)
	}
	code, out = run(t, "show", "--store", db, "--item", "item-good", "--root", repo)
	if code != 0 || lifecyclePayload(t, out)["title"] != "Changed" {
		t.Fatalf("unfinished item was not updated: %d %s", code, out)
	}
}

func TestDefinitionSyncIncludesLegacyFrozenStates(t *testing.T) {
	for _, target := range []string{"confirmed", "rejected", "abandoned", "deleted", "in-progress"} {
		t.Run(target, func(t *testing.T) {
			db := findingsStore(t)
			findingsItem(t, db, "finding")
			s, err := store.Open(context.Background(), db, false)
			if err != nil {
				t.Fatal(err)
			}
			err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = 'finding'`, target)
				return err
			})
			s.Close()
			if err != nil {
				t.Fatal(err)
			}
			repo := syncRepo(t, map[string]string{"docs/empty.md": "# Empty\n"})
			code, out := run(t, "sync", "--store", db, "--root", repo, "--json")
			if code != 0 || decodeSync(t, out).Payload.UnsharedOpen != 0 {
				t.Fatalf("finished residue %s: %d %s", target, code, out)
			}
		})
	}
}

func TestDefinitionGitLandingPreservesFinishedWaitingMetadata(t *testing.T) {
	db := filepath.Join(scratch(t, "definition-waiting"), "state.db")
	if code, out := apply(t, db, envelope(t, "item.record", "item", base("item_042", "waiting item"))); code != 0 {
		t.Fatalf("setup: %d %s", code, out)
	}
	code, out := run(t, "state", "--store", db, "--item", "item_042", "--state", "blocked", "--waiter", "owner", "--reason", "decision", "--now", "2026-11-17T12:00:00Z")
	if code != 0 {
		t.Fatalf("block: %d %s", code, out)
	}
	if code, out = findingState(t, db, "item_042", "resolved-elsewhere", "--resolves-via", "doc:docs/decision.md"); code != 0 {
		t.Fatalf("finish: %d %s", code, out)
	}
	p := lifecyclePayload(t, out)
	if p["waiting_on"] == nil || p["underlying_state"] != "not-started" {
		t.Fatalf("Git-landing finished move cleared historical waiting metadata: %s", out)
	}
}

func TestDefinitionLegacyStoreDrains(t *testing.T) {
	// Use the captured pre-change SQL, not a store created by the new writers.
	db := legacyAutoQAStore(t)
	s, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	d, err := definition.ForStore(context.Background(), s)
	s.Close()
	if err != nil || !d.Default {
		t.Fatalf("legacy definition: %+v %v", d, err)
	}
	addTerminalEvidence(t, db, "item_042", "landing.recorded")
	for _, target := range []string{"in-queue", "landed"} {
		if code, out := state(t, db, "item_042", target, "2026-11-17T12:00:00Z"); code != 0 {
			t.Fatalf("legacy drain %s: %d %s", target, code, out)
		}
	}
	if code, out := run(t, "show", "--store", db, "--item", "item_042"); code != 0 || lifecyclePayload(t, out)["state"] != "landed" {
		t.Fatalf("legacy drained item: %d %s", code, out)
	}
}
