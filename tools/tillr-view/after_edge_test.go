package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestUnmetBlockerAfterVsBlockedBy(t *testing.T) {
	states := map[string]string{
		"dep-closed":   "closed",
		"dep-landed":   "landed",
		"dep-resolved": "resolved-elsewhere",
		"dep-queued":   "in-queue",
	}
	edges := map[string][]storeEdge{
		"after-closed":   {{ItemID: "after-closed", BlockedBy: "dep-closed", Kind: edgeKindAfter}},
		"after-landed":   {{ItemID: "after-landed", BlockedBy: "dep-landed", Kind: edgeKindAfter}},
		"after-resolved": {{ItemID: "after-resolved", BlockedBy: "dep-resolved", Kind: edgeKindAfter}},
		"after-queued":   {{ItemID: "after-queued", BlockedBy: "dep-queued", Kind: edgeKindAfter}},
		"after-missing":  {{ItemID: "after-missing", BlockedBy: "dep-missing", Kind: edgeKindAfter}},
		"block-closed":   {{ItemID: "block-closed", BlockedBy: "dep-closed", Kind: edgeKindBlockedBy}},
		"block-landed":   {{ItemID: "block-landed", BlockedBy: "dep-landed", Kind: edgeKindBlockedBy}},
		"block-resolved": {{ItemID: "block-resolved", BlockedBy: "dep-resolved", Kind: edgeKindBlockedBy}},
		"block-queued":   {{ItemID: "block-queued", BlockedBy: "dep-queued", Kind: edgeKindBlockedBy}},
	}
	for _, id := range []string{"after-closed", "after-landed", "after-resolved", "block-landed", "block-resolved"} {
		if unmetBlocker(states, edges, id) {
			t.Errorf("%s: unmetBlocker = true, want false", id)
		}
	}
	for _, id := range []string{"after-queued", "after-missing", "block-closed", "block-queued"} {
		if !unmetBlocker(states, edges, id) {
			t.Errorf("%s: unmetBlocker = false, want true", id)
		}
	}
}

func TestAfterEdgeWaitingAndClosedBlockers(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`ALTER TABLE item_edges ADD COLUMN kind TEXT NOT NULL DEFAULT 'blocked_by'`)
	h.exec(`DELETE FROM dispatches`)
	h.exec(`DELETE FROM item_edges`)
	h.exec(`INSERT INTO items (item_id,title,state,updated_at) VALUES
  ('dep-closed','closed dep','closed',1000),
  ('dep-landed','landed dep','landed',1000),
  ('dep-resolved','resolved dep','resolved-elsewhere',1000),
  ('dep-queued','queued dep','in-queue',1000),
  ('wait-after-closed','after closed','in-queue',1000),
  ('wait-after-queued','after queued','in-queue',1000),
  ('wait-blocked-closed','blocked closed','in-queue',1000),
  ('wait-mixed','after closed plus blocked queued','in-queue',1000)`)
	h.exec(`INSERT INTO item_edges (item_id,blocked_by,kind) VALUES
  ('wait-after-closed','dep-closed','after'),
  ('wait-after-queued','dep-queued','after'),
  ('wait-blocked-closed','dep-closed','blocked_by'),
  ('wait-mixed','dep-closed','after'),
  ('wait-mixed','dep-queued','blocked_by')`)
	h.exec(`INSERT INTO dispatches (decision_id,item_id,action,reason,dispatched_at) VALUES
  ('d1','wait-after-closed','hold','blocked',1000),
  ('d2','wait-after-queued','hold','blocked',1000),
  ('d3','wait-blocked-closed','hold','blocked',1000)`)

	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]waitingRow{}
	for _, row := range rows {
		byID[row.ItemID] = row
	}

	// An after edge on a closed item must not trigger "needs you" or "closed without landing".
	afterClosed := byID["wait-after-closed"]
	if afterClosed.Class != "waits on work" {
		t.Errorf("wait-after-closed class = %q, want waits on work", afterClosed.Class)
	}
	if strings.Contains(afterClosed.Why, "closed without landing") || strings.Contains(afterClosed.Mover, "remove the dependency") {
		t.Errorf("wait-after-closed unexpectedly reported closed blocker: why=%q mover=%q", afterClosed.Why, afterClosed.Mover)
	}

	// An unfinished after edge stays "waits on work" naming the target.
	afterQueued := byID["wait-after-queued"]
	if afterQueued.Class != "waits on work" {
		t.Errorf("wait-after-queued class = %q, want waits on work", afterQueued.Class)
	}
	if !strings.Contains(afterQueued.Why, "dep-queued") {
		t.Errorf("wait-after-queued why = %q, want dep-queued", afterQueued.Why)
	}

	// A blocked_by edge on a closed item keeps today's "needs you" hold and text.
	blockedClosed := byID["wait-blocked-closed"]
	if blockedClosed.Class != "needs you" {
		t.Errorf("wait-blocked-closed class = %q, want needs you", blockedClosed.Class)
	}
	if !strings.Contains(blockedClosed.Why, "dep-closed") || !strings.Contains(blockedClosed.Why, "closed without landing") {
		t.Errorf("wait-blocked-closed why = %q, want dep-closed closed without landing", blockedClosed.Why)
	}

	// Mixed: after(closed) + blocked_by(in-queue) waits on dep-queued only, not needs you.
	mixed := byID["wait-mixed"]
	if mixed.Class != "waits on work" {
		t.Errorf("wait-mixed class = %q, want waits on work", mixed.Class)
	}
	if strings.Contains(mixed.Why, "dep-closed") || !strings.Contains(mixed.Why, "dep-queued") {
		t.Errorf("wait-mixed why = %q, want only dep-queued", mixed.Why)
	}

	// Verify readClosedBlockers directly.
	s, err := store.OpenRead(context.Background(), h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		gotAfter, err := readClosedBlockers(r, "wait-after-closed", "")
		if err != nil {
			return err
		}
		if len(gotAfter) != 0 {
			t.Errorf("readClosedBlockers(wait-after-closed) = %v, want empty", gotAfter)
		}
		gotBlocked, err := readClosedBlockers(r, "wait-blocked-closed", "")
		if err != nil {
			return err
		}
		if len(gotBlocked) != 1 || gotBlocked[0] != "dep-closed" {
			t.Errorf("readClosedBlockers(wait-blocked-closed) = %v, want [dep-closed]", gotBlocked)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAfterEdgeSnapshotAndItemDetailProjection(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`ALTER TABLE item_edges ADD COLUMN kind TEXT NOT NULL DEFAULT 'blocked_by'`)
	h.exec(`DELETE FROM dispatches`)
	h.exec(`DELETE FROM item_edges`)
	h.exec(`INSERT INTO items (item_id,title,state,updated_at,allowed_paths) VALUES
  ('dep-closed','closed dep','closed',1000,''),
  ('dep-landed','landed dep','landed',1000,''),
  ('dep-resolved','resolved dep','resolved-elsewhere',1000,''),
  ('dep-queued','queued dep','in-queue',1000,''),
  ('after-closed','after closed','in-queue',2000,''),
  ('after-landed','after landed','in-queue',2000,''),
  ('after-resolved','after resolved','in-queue',2000,''),
  ('after-queued','after queued','in-queue',2000,''),
  ('blocked-closed','blocked closed','in-queue',2000,'')`)
	h.exec(`INSERT INTO item_edges (item_id,blocked_by,kind) VALUES
  ('after-closed','dep-closed','after'),
  ('after-landed','dep-landed','after'),
  ('after-resolved','dep-resolved','after'),
  ('after-queued','dep-queued','after'),
  ('blocked-closed','dep-closed','blocked_by')`)
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES
  (1000,'item.record','item','dep-queued','{"state":"in-queue"}'),
  (2000,'item.record','item','after-closed','{"state":"in-queue"}'),
  (2000,'item.record','item','after-landed','{"state":"in-queue"}'),
  (2000,'item.record','item','after-resolved','{"state":"in-queue"}'),
  (2000,'item.record','item','after-queued','{"state":"in-queue"}'),
  (2000,'item.record','item','blocked-closed','{"state":"in-queue"}')`)

	status, body, raw := h.get("/api/v1/snapshot")
	if status != http.StatusOK {
		t.Fatalf("snapshot: %d %s", status, raw)
	}
	items := map[string]map[string]any{}
	for _, v := range body["data"].(map[string]any)["items"].([]any) {
		it := v.(map[string]any)
		items[it["item_id"].(string)] = it
	}

	for _, id := range []string{"after-closed", "after-landed", "after-resolved"} {
		proj := items[id]["projected_state"].(map[string]any)
		if proj["state"] != "in_queue" {
			t.Errorf("snapshot %s projected_state.state = %v, want in_queue", id, proj["state"])
		}
	}
	if got := items["after-queued"]["projected_state"].(map[string]any)["state"]; got != "blocked" {
		t.Errorf("snapshot after-queued projected_state.state = %v, want blocked", got)
	}
	if got := items["blocked-closed"]["projected_state"].(map[string]any)["state"]; got != "blocked" {
		t.Errorf("snapshot blocked-closed projected_state.state = %v, want blocked", got)
	}

	// Check item detail for after-closed vs blocked-closed vs after-queued.
	for _, tc := range []struct {
		id              string
		wantState       string
		wantClosedCount int
	}{
		{"after-closed", "in_queue", 0},
		{"after-landed", "in_queue", 0},
		{"after-resolved", "in_queue", 0},
		{"after-queued", "blocked", 0},
		{"blocked-closed", "blocked", 1},
	} {
		st, detailBody, detailRaw := h.get("/api/v1/items/" + tc.id)
		if st != http.StatusOK {
			t.Fatalf("item %s: %d %s", tc.id, st, detailRaw)
		}
		data := detailBody["data"].(map[string]any)
		proj := data["item"].(map[string]any)["projected_state"].(map[string]any)
		if proj["state"] != tc.wantState {
			t.Errorf("detail %s projected_state.state = %v, want %s", tc.id, proj["state"], tc.wantState)
		}
		closedBlockers := data["closed_blockers"].([]any)
		if len(closedBlockers) != tc.wantClosedCount {
			t.Errorf("detail %s closed_blockers = %v, want len %d", tc.id, closedBlockers, tc.wantClosedCount)
		}
	}
}
