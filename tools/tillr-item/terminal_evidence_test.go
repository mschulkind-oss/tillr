package main_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func init() {
	for _, kind := range []string{"landing.recorded", "drain.graph-completed"} {
		if err := event.Register(kind, "terminal-evidence-test"); err != nil {
			panic(err)
		}
	}
}

func addTerminalEvidence(t *testing.T, db, id, kind string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, db, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		body, _ := json.Marshal(map[string]string{"item_id": id})
		seq, err := tx.Append(event.Event{Kind: kind, ObjectType: "run", ObjectID: &id, Payload: body, At: time.Now()})
		if err != nil {
			return err
		}
		if kind == "landing.recorded" {
			if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS landings (item_id TEXT, result TEXT, event_seq INTEGER)`); err != nil {
				return err
			}
			_, err = tx.Exec(`INSERT INTO landings VALUES (?, 'landed', ?)`, id, seq)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func finishCriteriaForEvidence(t *testing.T, db, id string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var verdicts []verdictInput
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT criterion_id, text, state FROM item_criteria WHERE item_id=?`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var criterion, text, state string
			if err := rows.Scan(&criterion, &text, &state); err != nil {
				return err
			}
			if state != "done" {
				v := mechanicalVerdict()
				v.ItemID, v.CriterionID, v.CriterionHash = id, criterion, hashOf(text)
				verdicts = append(verdicts, v)
			}
		}
		return rows.Err()
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range verdicts {
		if code, out := recordVerdict(t, db, v); code != 0 {
			t.Fatalf("verdict: %d %s", code, out)
		}
	}
}

func TestTerminalEvidencePreconditions(t *testing.T) {
	for _, kind := range []string{"landing.recorded", "drain.graph-completed"} {
		t.Run(kind, func(t *testing.T) {
			db := storedWithCriterion(t, "terminal-evidence", "open")
			code, out := state(t, db, "item_042", "landed", "2026-11-10T00:10:00Z")
			assertRefused(t, code, out, "landed-criteria-unmet")
			if !strings.Contains(string(out), "c1") {
				t.Fatalf("unmet criterion unnamed: %s", out)
			}
			if code, out := recordVerdict(t, db, mechanicalVerdict()); code != 0 {
				t.Fatalf("verdict: %d %s", code, out)
			}
			code, out = state(t, db, "item_042", "landed", "2026-11-10T00:10:00Z")
			assertRefused(t, code, out, "landed-receipt-missing")
			addTerminalEvidence(t, db, "item_042", kind)
			if code, out = state(t, db, "item_042", "landed", "2026-11-10T00:10:00Z"); code != 0 {
				t.Fatalf("land: %d %s", code, out)
			}
			if code, out = findingState(t, db, "item_042", "in-queue", "--reason", "reverted", "--by", "loop"); code != 0 {
				t.Fatalf("return: %d %s", code, out)
			}
			code, out = state(t, db, "item_042", "landed", "2026-11-10T00:10:00Z")
			assertRefused(t, code, out, "landed-receipt-missing")
			addTerminalEvidence(t, db, "item_042", kind)
			if code, out = state(t, db, "item_042", "landed", "2026-11-10T00:10:00Z"); code != 0 {
				t.Fatalf("reland: %d %s", code, out)
			}
		})
	}
}

func TestTerminalRetryIgnoresNewVerdictRequirement(t *testing.T) {
	db := filepath.Join(scratch(t, "terminal-retry"), "state.db")
	p := base("item-retry", "legacy terminal")
	p.State = "landed"
	if code, out := apply(t, db, envelope(t, "item.record", "seed", p)); code != 0 {
		t.Fatalf("seed: %d %s", code, out)
	}
	before := eventsOfKind(t, db, "item.update")
	code, out := run(t, "state", "--store", db, "--item", p.ItemID, "--state", "landed", "--require-verdicts", "--now", "2026-11-10T00:10:00Z")
	if code != 0 || eventsOfKind(t, db, "item.update") != before {
		t.Fatalf("retry: %d %s", code, out)
	}
}

func TestTerminalReturnFromLegacyRowInvalidatesReceipt(t *testing.T) {
	db := filepath.Join(scratch(t, "terminal-legacy-return"), "state.db")
	ctx := context.Background()
	s, err := store.Open(ctx, db, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES('item-old','legacy','landed',0)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO item_criteria(item_id,criterion_id,text,state) VALUES('item-old','c1','old','done')`)
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	addTerminalEvidence(t, db, "item-old", "landing.recorded")
	if code, out := findingState(t, db, "item-old", "in-queue", "--reason", "reverted", "--by", "loop"); code != 0 {
		t.Fatalf("return: %d %s", code, out)
	}
	code, out := state(t, db, "item-old", "landed", "2026-11-11T00:00:00Z")
	assertRefused(t, code, out, "landed-receipt-missing")
}

func TestTerminalLegacyZeroSequenceReceipt(t *testing.T) {
	db := storedWithCriterion(t, "terminal-zero-seq", "done")
	ctx := context.Background()
	s, err := store.Open(ctx, db, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE landings (item_id TEXT, result TEXT, event_seq INTEGER)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO landings VALUES ('item_042','landed',0)`)
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if code, out := state(t, db, "item_042", "landed", "2026-11-10T00:10:00Z"); code != 0 {
		t.Fatalf("land: %d %s", code, out)
	}
}

func TestTerminalApplyEvidenceAndEmptyCriteria(t *testing.T) {
	db := filepath.Join(scratch(t, "terminal-empty"), "state.db")
	p := base("item-empty", "empty")
	p.Criteria = nil
	p.State = "in-queue"
	if code, out := apply(t, db, envelope(t, "item.record", "seed", p)); code != 0 {
		t.Fatalf("seed: %d %s", code, out)
	}
	code, out := state(t, db, p.ItemID, "landed", "2026-11-10T00:10:00Z")
	assertRefused(t, code, out, "landed-criteria-unmet")
	p.State = "landed"
	code, out = apply(t, db, envelope(t, "item.record", "move", p))
	assertRefused(t, code, out, "landed-criteria-unmet")
	p.State = "resolved-elsewhere"
	code, out = apply(t, db, envelope(t, "item.record", "resolve", p))
	assertRefused(t, code, out, "resolved-pointer-missing")
}

func TestTerminalApplyCannotReturnWithReceipt(t *testing.T) {
	db := filepath.Join(scratch(t, "terminal-apply-return"), "state.db")
	p := base("item-return", "finished")
	p.State = "landed"
	if code, out := apply(t, db, envelope(t, "item.record", "seed", p)); code != 0 {
		t.Fatalf("seed: %d %s", code, out)
	}
	p.State = "in-queue"
	code, out := run(t, "apply", "--store", db, "--input", input(t, envelope(t, "item.record", "return", p)), "--now", "2026-11-10T00:10:00Z", "--reason", "reverted", "--by", "loop")
	assertRefused(t, code, out, "state-terminal")
}

func TestTerminalApplyCannotAddOpenCriterionWhileLanding(t *testing.T) {
	db := filepath.Join(scratch(t, "terminal-apply-criterion"), "state.db")
	p := base("item-apply-land", "apply landing")
	p.State = "in-queue"
	p.Criteria[0].State = "done"
	if code, out := apply(t, db, envelope(t, "item.record", "seed", p)); code != 0 {
		t.Fatalf("seed: %d %s", code, out)
	}
	addTerminalEvidence(t, db, p.ItemID, "landing.recorded")
	p.State = "landed"
	p.Criteria = append(p.Criteria, criterion{"c2", "new criterion", "open", ""})
	code, out := apply(t, db, envelope(t, "item.record", "move", p))
	assertRefused(t, code, out, "landed-criteria-unmet")
	if !strings.Contains(string(out), "c2") {
		t.Fatalf("new criterion unnamed: %s", out)
	}
}

func TestTerminalApplyResolutionPointer(t *testing.T) {
	db := filepath.Join(scratch(t, "terminal-apply-pointer"), "state.db")
	p := base("item-apply", "apply resolution")
	p.State = "in-queue"
	if code, out := apply(t, db, envelope(t, "item.record", "seed", p)); code != 0 {
		t.Fatalf("seed: %d %s", code, out)
	}
	p.State = "resolved-elsewhere"
	code, out := run(t, "apply", "--store", db, "--input", input(t, envelope(t, "item.record", "resolve", p)), "--now", "2026-11-10T00:10:00Z", "--resolves-via", "doc:docs/proof.md", "--reason", "covered", "--by", "loop")
	if code != 0 {
		t.Fatalf("resolve apply: %d %s", code, out)
	}
	code, out = run(t, "show", "--store", db, "--item", p.ItemID)
	if code != 0 || !strings.Contains(string(out), `"resolves_via":"doc:docs/proof.md"`) {
		t.Fatalf("show: %d %s", code, out)
	}
}

func TestTerminalResolutionPointerShown(t *testing.T) {
	db := storedWithCriterion(t, "terminal-pointer", "open")
	code, out := findingState(t, db, "item_042", "resolved-elsewhere", "--resolves-via", "item:item_042")
	assertRefused(t, code, out, "pointer-unknown")
	code, out = findingState(t, db, "item_042", "resolved-elsewhere", "--resolves-via", "commit:abc123", "--reason", "already covered", "--by", "loop")
	if code != 0 {
		t.Fatalf("resolve: %d %s", code, out)
	}
	code, out = run(t, "show", "--store", db, "--item", "item_042")
	if code != 0 || !strings.Contains(string(out), `"resolves_via":"commit:abc123"`) {
		t.Fatalf("show: %d %s", code, out)
	}
}

func TestTerminalResolutionEvidence(t *testing.T) {
	db := storedWithCriterion(t, "terminal-resolution", "open")
	code, out := state(t, db, "item_042", "resolved-elsewhere", "2026-11-10T00:10:00Z")
	assertRefused(t, code, out, "resolved-pointer-missing")
	addTerminalEvidence(t, db, "item_042", "landing.recorded")
	ctx := context.Background()
	s, err := store.Open(ctx, db, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TABLE landing_claims (run_id TEXT, item_id TEXT); INSERT INTO landing_claims VALUES ('run-new','item_042')`)
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	code, out = state(t, db, "item_042", "resolved-elsewhere", "2026-11-10T00:10:00Z")
	assertRefused(t, code, out, "already-landed")
	code, out = findingState(t, db, "item_042", "resolved-elsewhere", "--resolves-via", "commit:abc")
	assertRefused(t, code, out, "already-landed")
}
