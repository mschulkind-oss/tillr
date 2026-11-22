package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/pelletier/go-toml/v2"
)

const (
	ruleSetKind      = "watch.rule.set"
	ruleResetKind    = "watch.rule.reset"
	ruleOverridesDDL = `
CREATE TABLE IF NOT EXISTS watch_rule_overrides (
 rule_id TEXT PRIMARY KEY,
 rule_toml TEXT NOT NULL,
 set_by TEXT NOT NULL,
 set_at INTEGER NOT NULL
);
`
)

// EffectiveRule is a whole loaded rule with its effective source ("file" or "store").
type EffectiveRule struct {
	Rule
	Source string
}

// EffectiveRules returns the validated effective rules in ID order with their source.
// A stored rule replaces the whole file rule of the same ID, even when disabled.
func EffectiveRules(rd *store.Reader, fileRules []Rule) ([]EffectiveRule, error) {
	stored, err := ReadStoredRules(rd)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]EffectiveRule, len(fileRules)+len(stored))
	for _, r := range fileRules {
		byID[r.ID] = EffectiveRule{Rule: r, Source: "file"}
	}
	for _, r := range stored {
		byID[r.ID] = EffectiveRule{Rule: r, Source: "store"}
	}
	result := make([]EffectiveRule, 0, len(byID))
	for _, id := range slices.Sorted(maps.Keys(byID)) {
		result = append(result, byID[id])
	}
	return result, nil
}

// ReadEffectiveRules returns the validated rules in ID order. A stored rule
// replaces the whole file rule, even when disabled. It never migrates the store.
func ReadEffectiveRules(rd *store.Reader, fileRules []Rule) ([]Rule, error) {
	rules, _, err := readEffectiveRules(rd, fileRules)
	return rules, err
}

func readEffectiveRules(rd *store.Reader, fileRules []Rule) ([]Rule, map[string]bool, error) {
	effective, err := EffectiveRules(rd, fileRules)
	if err != nil {
		return nil, nil, err
	}
	disabledOverrides := make(map[string]bool)
	result := make([]Rule, len(effective))
	for i, r := range effective {
		result[i] = r.Rule
		if r.Source == "store" && !r.Enabled {
			disabledOverrides[r.ID] = true
		}
	}
	return result, disabledOverrides, nil
}

// ReadStoredRules returns validated stored rules in ID order. A store without
// watch_rule_overrides has no stored rules, so readers need not migrate the store.
func ReadStoredRules(rd *store.Reader) ([]Rule, error) {
	exists, err := tableExists(rd, "watch_rule_overrides")
	if err != nil || !exists {
		return nil, err
	}
	rows, err := rd.Query(`SELECT rule_id, rule_toml FROM watch_rule_overrides ORDER BY rule_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stored []Rule
	for rows.Next() {
		var id, text string
		if err := rows.Scan(&id, &text); err != nil {
			return nil, err
		}
		source := fmt.Sprintf("store (stored rule %q)", id)
		rules, err := LoadRuleDocument(source, []byte(text))
		if err != nil {
			return nil, err
		}
		if len(rules) != 1 || rules[0].ID != id {
			return nil, fmt.Errorf("%s: expected one rule with the row's id", source)
		}
		stored = append(stored, rules[0])
	}
	return stored, rows.Err()
}

func tableExists(rd *store.Reader, table string) (bool, error) {
	var exists bool
	err := rd.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name=?)`, table).Scan(&exists)
	return exists, err
}

// RuleStore writes rule overrides and their events in one store transaction.
// FileRules are the validated baseline rules loaded from --rules.
type RuleStore struct {
	Store     *store.Store
	FileRules []Rule
	Clock     func() time.Time
}

func (s RuleStore) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func (s RuleStore) Add(ctx context.Context, source string, doc []byte, by string) error {
	if by == "" {
		return fmt.Errorf("--by is required and must not be empty")
	}
	rules, err := LoadRuleDocument(source, doc)
	if err != nil {
		return err
	}
	return s.change(ctx, by, func(tx *store.Tx, _ []EffectiveRule, now time.Time) error {
		return setRules(tx, rules, by, now)
	})
}

func (s RuleStore) Tune(ctx context.Context, id string, sets []string, by string) error {
	if len(sets) == 0 {
		return fmt.Errorf("tune requires at least one --set")
	}
	return s.change(ctx, by, func(tx *store.Tx, effective []EffectiveRule, now time.Time) error {
		i := slices.IndexFunc(effective, func(r EffectiveRule) bool { return r.ID == id })
		if i < 0 {
			return fmt.Errorf("no effective rule %q", id)
		}
		table := cloneTable(effective[i].table)
		if table == nil {
			return fmt.Errorf("rule %q was not loaded from TOML", id)
		}
		for _, set := range sets {
			if err := setRuleKey(table, set); err != nil {
				return err
			}
		}
		text, err := EncodeRules([]Rule{{ID: id, table: table}})
		if err != nil {
			return err
		}
		rules, err := LoadRuleDocument("store", text)
		if err != nil {
			return err
		}
		return setRules(tx, rules, by, now)
	})
}

func (s RuleStore) Reset(ctx context.Context, id, by string) error {
	return s.change(ctx, by, func(tx *store.Tx, effective []EffectiveRule, now time.Time) error {
		if !slices.ContainsFunc(effective, func(r EffectiveRule) bool { return r.ID == id && r.Source == "store" }) {
			return fmt.Errorf("no stored rule %q", id)
		}
		if _, err := tx.Exec(`DELETE FROM watch_rule_overrides WHERE rule_id=?`, id); err != nil {
			return err
		}
		return appendRuleChange(tx, ruleResetKind, id, by, now)
	})
}

func (s RuleStore) change(ctx context.Context, by string, apply func(*store.Tx, []EffectiveRule, time.Time) error) error {
	if by == "" {
		return fmt.Errorf("--by is required and must not be empty")
	}
	return s.Store.WriteTx(ctx, func(tx *store.Tx) error {
		effective, err := EffectiveRules(tx.Reader(), s.FileRules)
		if err != nil {
			return err
		}
		return apply(tx, effective, s.now())
	})
}

func setRules(tx *store.Tx, rules []Rule, by string, now time.Time) error {
	if len(rules) == 0 {
		return nil
	}
	// A pre-watch store can accept its first override without a separate
	// migration commit. If any override or event fails, this DDL rolls back too.
	if _, err := tx.Exec(ruleOverridesDDL); err != nil {
		return err
	}
	for _, r := range rules {
		text, err := EncodeRules([]Rule{r})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO watch_rule_overrides(rule_id, rule_toml, set_by, set_at)
VALUES (?,?,?,?) ON CONFLICT(rule_id) DO UPDATE SET
rule_toml=excluded.rule_toml, set_by=excluded.set_by, set_at=excluded.set_at`, r.ID, string(text), by, now.UnixMilli()); err != nil {
			return err
		}
		if err := appendRuleChange(tx, ruleSetKind, r.ID, by, now); err != nil {
			return err
		}
	}
	return nil
}

func appendRuleChange(tx *store.Tx, kind, id, by string, now time.Time) error {
	payload, err := json.Marshal(map[string]string{"rule_id": id, "by": by})
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: kind, ObjectType: "rule", ObjectID: &id, Payload: payload, At: now})
	return err
}

func setRuleKey(table map[string]any, set string) error {
	path, raw, ok := strings.Cut(set, "=")
	if !ok || !validEventPath(path) {
		return fmt.Errorf("invalid --set %q: expected key=value", set)
	}
	parts := strings.Split(path, ".")
	if parts[0] == "id" {
		return fmt.Errorf("--set cannot change id")
	}
	var value map[string]any
	if err := toml.Unmarshal([]byte("value = "+raw), &value); err != nil {
		return fmt.Errorf("--set %s: %w", path, err)
	}
	v, ok := value["value"]
	if !ok || len(value) != 1 {
		return fmt.Errorf("--set %s: expected one TOML value", path)
	}
	for _, key := range parts[:len(parts)-1] {
		next := make(map[string]any)
		if existing, ok := table[key]; ok {
			old, ok := existing.(map[string]any)
			if !ok {
				return fmt.Errorf("--set %s: %s is not a table", path, key)
			}
			next = cloneTable(old)
		}
		table[key] = next
		table = next
	}
	table[parts[len(parts)-1]] = v
	return nil
}

func cloneTable(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneTOMLValue(v)
	}
	return out
}

func cloneTOMLValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return cloneTable(x)
	case []any:
		out := make([]any, len(x))
		for i, elem := range x {
			out[i] = cloneTOMLValue(elem)
		}
		return out
	default:
		return v
	}
}
