package watch

import (
	"fmt"
	"maps"
	"slices"

	"github.com/mschulkind-oss/tillr/store"
)

const ruleOverridesDDL = `
CREATE TABLE IF NOT EXISTS watch_rule_overrides (
 rule_id TEXT PRIMARY KEY,
 rule_toml TEXT NOT NULL,
 set_by TEXT NOT NULL,
 set_at INTEGER NOT NULL
);
`

// ReadEffectiveRules returns the validated rules in ID order. A stored rule
// replaces the whole file rule, even when disabled. It never migrates the store.
func ReadEffectiveRules(rd *store.Reader, fileRules []Rule) ([]Rule, error) {
	rules, _, err := readEffectiveRules(rd, fileRules)
	return rules, err
}

func readEffectiveRules(rd *store.Reader, fileRules []Rule) ([]Rule, map[string]bool, error) {
	stored, err := readStoredRules(rd)
	if err != nil {
		return nil, nil, err
	}
	disabledOverrides := make(map[string]bool)
	byID := make(map[string]Rule, len(fileRules)+len(stored))
	for _, r := range fileRules {
		byID[r.ID] = r
	}
	for _, r := range stored {
		byID[r.ID] = r
		if !r.Enabled {
			disabledOverrides[r.ID] = true
		}
	}
	result := make([]Rule, 0, len(byID))
	for _, id := range slices.Sorted(maps.Keys(byID)) {
		result = append(result, byID[id])
	}
	return result, disabledOverrides, nil
}

func readStoredRules(rd *store.Reader) ([]Rule, error) {
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
		rules, err := LoadRuleDocument(fmt.Sprintf("stored rule %q", id), []byte(text))
		if err != nil {
			return nil, err
		}
		if len(rules) != 1 || rules[0].ID != id {
			return nil, fmt.Errorf("stored rule %q: expected one rule with the row's id", id)
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
