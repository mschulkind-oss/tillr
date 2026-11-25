package main_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
	_ "modernc.org/sqlite"
)

func receiptPayload(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var e struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	return e.Payload
}

func latestItemPayload(t *testing.T, db, id string) map[string]any {
	t.Helper()
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var raw []byte
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE object_id=? AND kind='item.update' ORDER BY seq DESC LIMIT 1`, id).Scan(&raw)
	}); err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFinishedStateReceipt(t *testing.T) {
	for _, target := range []string{"confirmed", "rejected", "landed", "resolved-elsewhere"} {
		for _, pointer := range []string{"item:other", "commit:0123456789abcdef0123456789abcdef01234567", "doc:docs/findings.md"} {
			t.Run(target+"/"+pointer, func(t *testing.T) {
				db := findingsStore(t)
				if target == "landed" || target == "resolved-elsewhere" {
					db = filepath.Join(scratch(t, "finished-git"), "state.db")
					for _, id := range []string{"finding", "other"} {
						p := base(id, "finding")
						p.State = "in-queue"
						if code, out := apply(t, db, envelope(t, "item.record", id, p)); code != 0 {
							t.Fatalf("seed: %d %s", code, out)
						}
					}
				} else {
					findingsItem(t, db, "finding")
					findingsItem(t, db, "other")
				}
				if target == "landed" {
					finishCriteriaForEvidence(t, db, "finding")
					addTerminalEvidence(t, db, "finding", "landing.recorded")
				}
				code, out := findingState(t, db, "finding", target, "--reason", "evidence checked", "--by", "reviewer", "--resolves-via", pointer)
				if code != 0 {
					t.Fatalf("finish: %d %s", code, out)
				}
				want := map[string]string{"state": target, "lifecycle_reason": "evidence checked", "lifecycle_by": "reviewer", "resolves_via": pointer}
				check := func(p map[string]any) {
					t.Helper()
					for k, v := range want {
						if p[k] != v {
							t.Fatalf("%s=%v, want %s", k, p[k], v)
						}
					}
				}
				check(receiptPayload(t, out))
				check(latestItemPayload(t, db, "finding"))
				code, out = run(t, "show", "--store", db, "--item", "finding")
				if code != 0 {
					t.Fatalf("show: %d %s", code, out)
				}
				check(receiptPayload(t, out))
				n := eventsOfKind(t, db, "item.update")
				code, out = findingState(t, db, "finding", target, "--reason", "replacement", "--by", "other", "--resolves-via", "doc:other.md")
				if code != 0 {
					t.Fatalf("repeat: %d %s", code, out)
				}
				check(receiptPayload(t, out))
				if eventsOfKind(t, db, "item.update") != n {
					t.Fatal("repeat appended an event")
				}
			})
		}
	}
}

func TestStateFlagNotStored(t *testing.T) {
	for _, tc := range []struct {
		target string
		flags  []string
		flag   string
	}{
		{"queued", []string{"--reason", "lost reason"}, "--reason"},
		{"queued", []string{"--reason", ""}, "--reason"},
		{"queued", []string{"--by", "reviewer"}, "--by"},
		{"queued", []string{"--resolves-via", "doc:a.md"}, "--resolves-via"},
		{"confirmed", []string{"--waiter", "owner"}, "--waiter"},
		{"waiting", []string{"--waiter", "owner", "--reason", "waiting", "--by", "reviewer"}, "--by"},
	} {
		t.Run(tc.target+tc.flag+strings.Join(tc.flags, ""), func(t *testing.T) {
			db := findingsStore(t)
			findingsItem(t, db, "finding")
			n := eventsOfKind(t, db, "item.update")
			code, out := findingState(t, db, "finding", tc.target, tc.flags...)
			assertRefused(t, code, out, "flag-not-stored")
			if !strings.Contains(decode(t, out).Payload.Reason, tc.flag) {
				t.Fatalf("flag unnamed: %s", out)
			}
			if stateRow(t, db, "finding") != "queued" || eventsOfKind(t, db, "item.update") != n {
				t.Fatal("refusal wrote state/event")
			}
		})
	}
}

func TestStateReadyMoveRefusesReason(t *testing.T) {
	db := filepath.Join(scratch(t, "ready-refuses-reason"), "state.db")
	p := base("item-not-started", "work")
	p.State = "not-started"
	if code, out := apply(t, db, envelope(t, "item.record", p.ItemID, p)); code != 0 {
		t.Fatalf("seed: %d %s", code, out)
	}
	for _, tc := range []struct {
		flags []string
		flag  string
	}{
		{[]string{"--reason", "would be lost"}, "--reason"},
		{[]string{"--by", "operator"}, "--by"},
	} {
		code, out := findingState(t, db, p.ItemID, "in-queue", tc.flags...)
		assertRefused(t, code, out, "flag-not-stored")
		if !strings.Contains(decode(t, out).Payload.Reason, tc.flag) {
			t.Fatalf("flag unnamed: %s", out)
		}
		if stateRow(t, db, p.ItemID) != "not-started" {
			t.Fatal("refusal moved item")
		}
	}
}

func TestBlockedReleaseRecordsByAndReason(t *testing.T) {
	t.Run("git-landing blocked to in-queue", func(t *testing.T) {
		db := filepath.Join(scratch(t, "blocked-release-git"), "state.db")
		p := base("item-held", "held work")
		p.State = "in-queue"
		if code, out := apply(t, db, envelope(t, "item.record", p.ItemID, p)); code != 0 {
			t.Fatalf("seed: %d %s", code, out)
		}
		if code, out := findingState(t, db, p.ItemID, "blocked", "--waiter", "owner", "--reason", "waiting on owner"); code != 0 {
			t.Fatalf("block: %d %s", code, out)
		}
		// Unstored flags on a release remain refused.
		for _, tc := range []struct {
			flags []string
			flag  string
		}{
			{[]string{"--resolves-via", "doc:a.md"}, "--resolves-via"},
			{[]string{"--waiter", "owner"}, "--waiter"},
		} {
			code, out := findingState(t, db, p.ItemID, "in-queue", tc.flags...)
			assertRefused(t, code, out, "flag-not-stored")
			if !strings.Contains(decode(t, out).Payload.Reason, tc.flag) {
				t.Fatalf("flag unnamed: %s", out)
			}
			if stateRow(t, db, p.ItemID) != "blocked" {
				t.Fatal("refusal moved item")
			}
		}
		code, out := findingState(t, db, p.ItemID, "in-queue", "--by", "operator", "--reason", "owner approved release")
		if code != 0 {
			t.Fatalf("release: %d %s", code, out)
		}
		if stateRow(t, db, p.ItemID) != "in-queue" {
			t.Fatalf("state = %q, want in-queue", stateRow(t, db, p.ItemID))
		}
		ev := latestItemPayload(t, db, p.ItemID)
		for k, want := range map[string]string{
			"state":            "in-queue",
			"lifecycle_action": "release",
			"lifecycle_by":     "operator",
			"lifecycle_reason": "owner approved release",
		} {
			if ev[k] != want {
				t.Fatalf("event %s=%v, want %q (event=%v)", k, ev[k], want, ev)
			}
		}
		code, out = run(t, "show", "--store", db, "--item", p.ItemID)
		if code != 0 {
			t.Fatalf("show: %d %s", code, out)
		}
		shown := receiptPayload(t, out)
		if shown["waiting_on"] != nil || shown["underlying_state"] != nil {
			t.Fatalf("blocked metadata not cleared: %s", out)
		}
		for _, k := range []string{"lifecycle_reason", "lifecycle_by"} {
			if _, ok := shown[k]; ok {
				t.Fatalf("release wrote finished-state column %s: %s", k, out)
			}
		}
	})

	t.Run("custom definition blocked to ready", func(t *testing.T) {
		db := findingsStore(t)
		findingsItem(t, db, "finding")
		if code, out := findingState(t, db, "finding", "waiting", "--waiter", "owner", "--reason", "needs review"); code != 0 {
			t.Fatalf("block: %d %s", code, out)
		}
		if code, out := findingState(t, db, "finding", "queued", "--by", "reviewer", "--reason", "approved"); code != 0 {
			t.Fatalf("release: %d %s", code, out)
		}
		ev := latestItemPayload(t, db, "finding")
		if ev["lifecycle_action"] != "release" || ev["lifecycle_by"] != "reviewer" || ev["lifecycle_reason"] != "approved" {
			t.Fatalf("event=%v", ev)
		}
	})

	t.Run("release does not invalidate existing landing receipt", func(t *testing.T) {
		db := storedWithCriterion(t, "blocked-release-landing", "open")
		if code, out := recordVerdict(t, db, mechanicalVerdict()); code != 0 {
			t.Fatalf("verdict: %d %s", code, out)
		}
		addTerminalEvidence(t, db, "item_042", "landing.recorded")
		if code, out := findingState(t, db, "item_042", "blocked", "--waiter", "owner", "--reason", "check"); code != 0 {
			t.Fatalf("block: %d %s", code, out)
		}
		if code, out := findingState(t, db, "item_042", "in-queue", "--by", "operator", "--reason", "ok to land"); code != 0 {
			t.Fatalf("release: %d %s", code, out)
		}
		if code, out := state(t, db, "item_042", "landed", "2026-11-17T12:05:00Z"); code != 0 {
			t.Fatalf("land after release should keep landing receipt: %d %s", code, out)
		}
	})
}

func TestFinishedStateExitRetainsEntryReceipt(t *testing.T) {
	db := findingsStore(t)
	findingsItem(t, db, "finding")
	if code, out := findingState(t, db, "finding", "rejected", "--reason", "original evidence", "--by", "first-reviewer", "--resolves-via", "doc:original.md"); code != 0 {
		t.Fatalf("finish: %d %s", code, out)
	}
	code, out := findingState(t, db, "finding", "queued", "--reason", "new evidence", "--by", "reviewer", "--resolves-via", "doc:new.md")
	assertRefused(t, code, out, "flag-not-stored")
	if code, out := findingState(t, db, "finding", "queued", "--reason", "new evidence", "--by", "reviewer"); code != 0 {
		t.Fatalf("leave: %d %s", code, out)
	}
	assertLifecycleEvent(t, db, "finding")
	code, out = run(t, "show", "--store", db, "--item", "finding")
	if code != 0 {
		t.Fatalf("show: %d %s", code, out)
	}
	p := receiptPayload(t, out)
	if p["lifecycle_reason"] != "original evidence" || p["lifecycle_by"] != "first-reviewer" || p["resolves_via"] != "doc:original.md" {
		t.Fatalf("entry receipt replaced by exit: %s", out)
	}
	if code, out := findingState(t, db, "finding", "confirmed"); code != 0 {
		t.Fatalf("finish again: %d %s", code, out)
	}
	code, out = run(t, "show", "--store", db, "--item", "finding")
	if code != 0 {
		t.Fatalf("show: %d %s", code, out)
	}
	p = receiptPayload(t, out)
	for _, key := range []string{"lifecycle_reason", "lifecycle_by", "resolves_via"} {
		if _, ok := p[key]; ok {
			t.Fatalf("receipt leaked into new entry: %s", out)
		}
	}
}

func TestFinishedStatePointerUnknown(t *testing.T) {
	db := findingsStore(t)
	findingsItem(t, db, "finding")
	code, out := findingState(t, db, "finding", "rejected", "--resolves-via", "item:missing")
	assertRefused(t, code, out, "pointer-unknown")
	if !strings.Contains(string(out), "missing") {
		t.Fatalf("pointer unnamed: %s", out)
	}
	if stateRow(t, db, "finding") != "queued" {
		t.Fatal("refusal moved item")
	}
}

func TestFinishedStatePointerMalformed(t *testing.T) {
	for _, pointer := range []string{"", "item:", "doc:", "commit:", "other:value", "a.md", "commit:not-a-sha"} {
		t.Run(pointer, func(t *testing.T) {
			db := findingsStore(t)
			findingsItem(t, db, "finding")
			code, out := findingState(t, db, "finding", "rejected", "--resolves-via", pointer)
			if code == 0 {
				t.Fatalf("malformed pointer accepted: %s", out)
			}
			if stateRow(t, db, "finding") != "queued" {
				t.Fatal("refusal moved item")
			}
		})
	}
}

func TestFinishedStateLegacyReceiptRead(t *testing.T) {
	db := findingsStore(t)
	findingsItem(t, db, "finding")
	if code, out := findingState(t, db, "finding", "confirmed"); code != 0 {
		t.Fatalf("finish: %d %s", code, out)
	}
	raw, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"lifecycle_reason", "lifecycle_by", "resolves_via"} {
		if _, err := raw.Exec(`ALTER TABLE items DROP COLUMN ` + col); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	code, out := run(t, "show", "--store", db, "--item", "finding")
	if code != 0 {
		t.Fatalf("legacy show: %d %s", code, out)
	}
	p := receiptPayload(t, out)
	if p["state"] != "confirmed" {
		t.Fatalf("legacy state changed: %s", out)
	}
	for _, k := range []string{"lifecycle_reason", "lifecycle_by", "resolves_via"} {
		if _, ok := p[k]; ok {
			t.Fatalf("invented legacy receipt: %s", out)
		}
	}
	if code, out := findingState(t, db, "finding", "confirmed"); code != 0 {
		t.Fatalf("legacy writable open: %d %s", code, out)
	}
}
