package item_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func openNonLandingStore(t *testing.T, charterJSON string) *store.Store {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := definition.Set(ctx, s, []byte(charterJSON)); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestApplyAndShowTask(t *testing.T) {
	ctx := context.Background()
	charter := `{"definition":"findings","version":1,"states":["queued","waiting","closed","done"],"ready":"queued","blocked":"waiting","closed":"closed","finished":["done","closed"],"transitions":[{"from":"queued","to":"done"},{"from":"queued","to":"waiting"},{"from":"queued","to":"closed"}],"prerequisites":[],"completes":"hook","hooks":{"argv":["hook"],"names":["complete-item"]}}`
	s := openNonLandingStore(t, charter)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	payload, err := json.Marshal(map[string]any{
		"item_id":  "finding-1",
		"title":    "Investigate alert",
		"body_ref": "alerts/1.json",
		"task":     "Check whether alert 1 is reproducible in staging.",
		"state":    "queued",
		"criteria": []map[string]any{
			{"criterion_id": "c1", "text": "Reproduction checked", "claim_class": "verification-only"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := record.Envelope{
		RecordVersion: 1,
		Kind:          item.RecordKind,
		ID:            "req-1",
		Producer:      record.Producer{System: "tillr", Utility: "test", Version: "0.1.0"},
		CreatedAt:     now,
		Payload:       payload,
	}

	var applied record.Envelope
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		applied, err = item.Apply(ctx, tx, env)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var gotApplied struct {
		Task string `json:"task"`
	}
	if err := json.Unmarshal(applied.Payload, &gotApplied); err != nil {
		t.Fatal(err)
	}
	if gotApplied.Task != "Check whether alert 1 is reproducible in staging." {
		t.Fatalf("applied task = %q", gotApplied.Task)
	}

	var shown record.Envelope
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		shown, err = item.Show(r, "finding-1", t.TempDir())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var gotShown struct {
		Task string `json:"task"`
	}
	if err := json.Unmarshal(shown.Payload, &gotShown); err != nil {
		t.Fatal(err)
	}
	if gotShown.Task != "Check whether alert 1 is reproducible in staging." {
		t.Fatalf("shown task = %q", gotShown.Task)
	}
}

func TestApplyDefaultCriteriaOnNonLandingStore(t *testing.T) {
	ctx := context.Background()
	charter := `{"definition":"findings","version":1,"states":["queued","waiting","closed","done"],"ready":"queued","blocked":"waiting","closed":"closed","finished":["done","closed"],"transitions":[{"from":"queued","to":"done"},{"from":"queued","to":"waiting"},{"from":"queued","to":"closed"}],"prerequisites":[],"completes":"hook","default_criteria":[{"criterion_id":"dc1","text":"Finding is verified"},{"criterion_id":"dc2","text":"Disposition is recorded","claim_class":"direction"}],"hooks":{"argv":["hook"],"names":["complete-item"]}}`
	s := openNonLandingStore(t, charter)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	applyRaw := func(id string, body map[string]any) record.Envelope {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		env := record.Envelope{
			RecordVersion: 1,
			Kind:          item.RecordKind,
			ID:            id,
			Producer:      record.Producer{System: "tillr", Utility: "test", Version: "0.1.0"},
			CreatedAt:     now,
			Payload:       payload,
		}
		var out record.Envelope
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			var err error
			out, err = item.Apply(ctx, tx, env)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// 1. Item applied without criteria receives charter's default_criteria.
	out := applyRaw("req-default", map[string]any{
		"item_id": "item-default",
		"title":   "Item without criteria",
		"state":   "queued",
	})
	var got struct {
		Criteria []struct {
			CriterionID string `json:"criterion_id"`
			Text        string `json:"text"`
			ClaimClass  string `json:"claim_class"`
			State       string `json:"state"`
		} `json:"criteria"`
	}
	if err := json.Unmarshal(out.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Criteria) != 2 ||
		got.Criteria[0].CriterionID != "dc1" || got.Criteria[0].Text != "Finding is verified" || got.Criteria[0].ClaimClass != "verification-only" ||
		got.Criteria[1].CriterionID != "dc2" || got.Criteria[1].Text != "Disposition is recorded" || got.Criteria[1].ClaimClass != "direction" {
		t.Fatalf("unexpected default criteria: %+v", got.Criteria)
	}

	// 2. Freeze dc1 with a verdict, then re-apply without criteria: must remain idempotent.
	h := sha256.Sum256([]byte("Finding is verified"))
	verdictPayload, err := json.Marshal(map[string]any{
		"item_id":        "item-default",
		"criterion_id":   "dc1",
		"criterion_hash": hex.EncodeToString(h[:]),
		"verdict":        "done",
		"claim_class":    "verification-only",
		"reason":         "verified",
		"evidence":       []string{"doc:proof.txt"},
		"actor_kind":     "mechanical",
		"actor":          "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.RecordVerdict(ctx, tx, record.Envelope{
			RecordVersion: 1,
			Kind:          item.VerdictRecordKind,
			ID:            "v-1",
			Producer:      record.Producer{System: "tillr", Utility: "test", Version: "0.1.0"},
			CreatedAt:     now,
			Payload:       verdictPayload,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	reapplied := applyRaw("req-default-2", map[string]any{
		"item_id": "item-default",
		"title":   "Item without criteria",
		"state":   "queued",
	})
	if err := json.Unmarshal(reapplied.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Criteria) != 2 || got.Criteria[0].State != "done" {
		t.Fatalf("reapply after verdict: %+v", got.Criteria)
	}

	// 3. Item applied with its own criteria keeps its own.
	custom := applyRaw("req-custom", map[string]any{
		"item_id": "item-custom",
		"title":   "Item with own criteria",
		"state":   "queued",
		"criteria": []map[string]any{
			{"criterion_id": "own1", "text": "Own check", "claim_class": "verification-only"},
		},
	})
	if err := json.Unmarshal(custom.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Criteria) != 1 || got.Criteria[0].CriterionID != "own1" || got.Criteria[0].Text != "Own check" {
		t.Fatalf("custom criteria overridden: %+v", got.Criteria)
	}
}
