package watch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWatchRuleStoreRollback(t *testing.T) {
	register(t, "test.watch")
	ctx := context.Background()
	s := watchStore(t)
	change := RuleStore{Store: s}
	first := strings.Replace(validRule, `id = "case"`, `id = "first"`, 1)
	// Fail on the second event after the first rule and event have been written.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER refuse_second_rule BEFORE INSERT ON events
		WHEN NEW.kind='watch.rule.set' AND NEW.object_id='case' BEGIN SELECT RAISE(ABORT,'refuse case'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := change.Add(ctx, "incoming", []byte(first+validRule), "operator"); !hasError(err, "refuse case") {
		t.Fatalf("want append failure, got %v", err)
	}
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		rules, err := ReadStoredRules(rd)
		if err != nil || len(rules) != 0 {
			t.Fatalf("partial rule commit: %v, %v", rules, err)
		}
		head, err := rd.EventHeadSeq()
		if err != nil || head != 0 {
			t.Fatalf("partial event commit: %d, %v", head, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWatchRuleStoreSnapshots(t *testing.T) {
	register(t, "test.watch")
	ctx := context.Background()
	s := watchStore(t)
	files, err := LoadRuleDocument("file", []byte(validRule))
	if err != nil {
		t.Fatal(err)
	}
	changes := RuleStore{Store: s, FileRules: files}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TABLE watch_rule_overrides`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := changes.Tune(ctx, "case", []string{"enabled=false", "when.threshold=0"}, "operator"); !hasError(err, "when.threshold") {
		t.Fatalf("invalid tune: %v", err)
	}
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		var exists bool
		if err := rd.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE name='watch_rule_overrides')`).Scan(&exists); err != nil || exists {
			t.Fatalf("refusal must not create overrides table: %t, %v", exists, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := changes.Tune(ctx, "case", []string{"enabled=false", "when.threshold=5"}, "operator"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		rules, err := EffectiveRules(tx.Reader(), files)
		if err != nil || len(rules) != 1 || rules[0].Source != "store" || rules[0].Enabled || rules[0].When.Threshold != 5 {
			t.Fatalf("transactional effective read: %v, %v", rules, err)
		}
		var actor string
		var at int64
		if err := tx.QueryRow(`SELECT set_by, set_at FROM watch_rule_overrides WHERE rule_id='case'`).Scan(&actor, &at); err != nil || actor != "operator" || at <= 0 {
			t.Fatalf("override metadata: %q, %d, %v", actor, at, err)
		}
		var payload string
		if err := tx.QueryRow(`SELECT payload FROM events WHERE object_type='rule' AND object_id='case' AND kind='watch.rule.set' AND ts=?`, at).Scan(&payload); err != nil {
			return err
		}
		var body map[string]string
		if err := json.Unmarshal([]byte(payload), &body); err != nil || len(body) != 2 || body["rule_id"] != "case" || body["by"] != "operator" {
			t.Fatalf("event metadata: %s, %v", payload, err)
		}
		// Update a row directly to emulate an override from another version.
		if _, err := tx.Exec(`UPDATE watch_rule_overrides SET rule_toml=?`, strings.Replace(validRule, "test.watch", "unknown.kind", 1)); err != nil {
			return err
		}
		if rules, err := EffectiveRules(tx.Reader(), files); rules != nil || !hasError(err, "store", "case", "unknown.kind") {
			t.Fatalf("invalid stored rule returned as effective: %v, %v", rules, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := changes.Reset(ctx, "case", "operator"); !hasError(err, "store", "unknown.kind") {
		t.Fatalf("invalid stored rule was reset: %v", err)
	}
	if !files[0].Enabled || files[0].When.Threshold != 1 {
		t.Fatal("tuning mutated the cached file rule")
	}
}
