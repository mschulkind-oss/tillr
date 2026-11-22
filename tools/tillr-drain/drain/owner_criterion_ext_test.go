package drain_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestPlanOwnerCriterionHoldAndHumanRelease(t *testing.T) {
	const text = "Choose the deployment policy.\nThe worker cannot choose it."
	raw := envelope("item.record", "item-owner", map[string]any{
		"item_id": "item-owner", "title": "Owner ruling", "state": "in-queue",
		"allowed_paths": []string{"tools/owner/"},
		"criteria":      []any{map[string]any{"criterion_id": "c1", "text": text, "state": "not-done", "claim_class": "owner-only"}},
	})
	path := fixture(t, raw)
	pass := tick(t, "2026-11-17T00:00:00Z", []string{"lane-1", "lane-2"}, 2, policy(t, "/bin/true"))
	decisions := plan(t, path, pass)
	dispatches := 0
	for _, row := range decisions {
		if row.Payload.Action == drain.ActionDispatch {
			dispatches++
		}
	}
	got := decisionFor(t, decisions, "item-owner")
	if dispatches != 0 || got.Action != drain.ActionHold || got.Reason != "owner-only-owed" || got.LaneID != "" || got.RunRequest != nil {
		t.Fatalf("two free lanes: dispatches=%d, decision=%+v", dispatches, got)
	}
	code, output := invoke(t, "why", "item-owner", "--store", path, "--policy", policy(t, "/bin/true"), "--root", repoRoot(t), "--questions-cmd", indexStub(t), "--json")
	if code != 0 {
		t.Fatalf("why: %d %s", code, output)
	}
	var why drain.WhyRow
	if err := json.Unmarshal([]byte(output), &why); err != nil {
		t.Fatal(err)
	}
	if why.Class != "needs you" || why.Mover != "the owner; rule on c1" || why.Why != "owner-only-owed: c1: Choose the deployment policy." {
		t.Fatalf("why: %+v", why)
	}
	ctx := context.Background()
	s, err := store.Open(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var verdict record.Envelope
	if err := json.Unmarshal([]byte(envelope("item.verdict", "human-ruling", map[string]any{
		"item_id": "item-owner", "criterion_id": "c1", "criterion_hash": item.CriterionHash(text),
		"verdict": "done", "claim_class": "owner-only", "reason": "Owner chose the policy", "actor_kind": "human", "evidence": []string{"owner ruling"},
	})), &verdict); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error { _, err := item.RecordVerdict(ctx, tx, verdict); return err }); err != nil {
		t.Fatal(err)
	}
	got = decisionFor(t, plan(t, path, pass), "item-owner")
	if got.Action != drain.ActionDispatch || got.RunRequest == nil || got.LaneID == "" {
		t.Fatalf("first pass after human ruling: %+v", got)
	}
}
