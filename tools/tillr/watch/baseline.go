package watch

import (
	"database/sql"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

const ruleBaselinesDDL = `
CREATE TABLE IF NOT EXISTS watch_rule_baselines (
 rule_id TEXT PRIMARY KEY,
 definition_hash TEXT NOT NULL,
 enabled INTEGER NOT NULL,
 first_loaded_seq INTEGER NOT NULL,
 first_loaded_at INTEGER NOT NULL,
 loaded_seq INTEGER NOT NULL,
 loaded_at INTEGER NOT NULL
);
`

func readRuleBaselines(rd *store.Reader) (map[string]ruleBaseline, error) {
	exists, err := tableExists(rd, "watch_rule_baselines")
	if err != nil || !exists {
		return nil, err
	}
	rows, err := rd.Query(`SELECT rule_id, definition_hash, enabled, first_loaded_seq, first_loaded_at, loaded_seq, loaded_at FROM watch_rule_baselines`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ruleBaseline{}
	for rows.Next() {
		var b ruleBaseline
		var enabled int
		if err := rows.Scan(&b.ruleID, &b.definitionHash, &enabled, &b.firstLoadedSeq, &b.firstLoadedAt, &b.loadedSeq, &b.loadedAt); err != nil {
			return nil, err
		}
		b.enabled = enabled != 0
		out[b.ruleID] = b
	}
	return out, rows.Err()
}

func readRuleBaselineRow(q interface {
	QueryRow(string, ...any) *sql.Row
}, ruleID string) (ruleBaseline, bool, error) {
	var b ruleBaseline
	var enabled int
	err := q.QueryRow(`SELECT rule_id, definition_hash, enabled, first_loaded_seq, first_loaded_at, loaded_seq, loaded_at FROM watch_rule_baselines WHERE rule_id=?`, ruleID).
		Scan(&b.ruleID, &b.definitionHash, &enabled, &b.firstLoadedSeq, &b.firstLoadedAt, &b.loadedSeq, &b.loadedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ruleBaseline{}, false, nil
	}
	if err != nil {
		return ruleBaseline{}, false, err
	}
	b.enabled = enabled != 0
	return b, true, nil
}

func (changes *episodeChanges) planBaselines(rules []Rule, results []RuleResult, fullRules []Rule, live bool, baselines map[string]ruleBaseline, startHead int64, now time.Time) {
	nowMS := now.UnixMilli()
	seen := make(map[string]bool, len(rules))
	for i, r := range rules {
		seen[r.ID] = true
		res := results[i]
		if !res.Disabled && res.Diagnostic() == "" {
			hash := definitionHash(r)
			row, exists := baselines[r.ID]
			if !exists {
				ruleID := r.ID
				*changes = append(*changes, func(tx *store.Tx) error {
					_, err := tx.Exec(`INSERT INTO watch_rule_baselines(rule_id, definition_hash, enabled, first_loaded_seq, first_loaded_at, loaded_seq, loaded_at) VALUES (?,?,1,?,?,?,?)`,
						ruleID, hash, startHead, nowMS, startHead, nowMS)
					return err
				})
			} else if !row.enabled || row.definitionHash != hash {
				ruleID := r.ID
				*changes = append(*changes, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE watch_rule_baselines SET definition_hash=?, enabled=1, loaded_seq=?, loaded_at=? WHERE rule_id=?`,
						hash, startHead, nowMS, ruleID)
					return err
				})
			}
		} else if !r.Enabled {
			if row, exists := baselines[r.ID]; exists && row.enabled {
				ruleID := r.ID
				*changes = append(*changes, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE watch_rule_baselines SET enabled=0 WHERE rule_id=?`, ruleID)
					return err
				})
			}
		}
	}
	if !live {
		for _, r := range fullRules {
			if seen[r.ID] || r.Enabled {
				continue
			}
			if row, exists := baselines[r.ID]; exists && row.enabled {
				ruleID := r.ID
				*changes = append(*changes, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE watch_rule_baselines SET enabled=0 WHERE rule_id=?`, ruleID)
					return err
				})
			}
		}
		return
	}
	for _, id := range slices.Sorted(maps.Keys(baselines)) {
		if seen[id] || !baselines[id].enabled {
			continue
		}
		ruleID := id
		*changes = append(*changes, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE watch_rule_baselines SET enabled=0 WHERE rule_id=?`, ruleID)
			return err
		})
	}
}

func updateBaselineForRule(tx *store.Tx, r Rule, changeSeq, nowMS int64) error {
	row, exists, err := readRuleBaselineRow(tx, r.ID)
	if err != nil {
		return err
	}
	if r.Enabled && r.evaluatorReason() == "" {
		hash := definitionHash(r)
		if !exists {
			_, err = tx.Exec(`INSERT INTO watch_rule_baselines(rule_id, definition_hash, enabled, first_loaded_seq, first_loaded_at, loaded_seq, loaded_at) VALUES (?,?,1,?,?,?,?)`,
				r.ID, hash, changeSeq, nowMS, changeSeq, nowMS)
			return err
		}
		if !row.enabled || row.definitionHash != hash {
			_, err = tx.Exec(`UPDATE watch_rule_baselines SET definition_hash=?, enabled=1, loaded_seq=?, loaded_at=? WHERE rule_id=?`,
				hash, changeSeq, nowMS, r.ID)
			return err
		}
		return nil
	}
	if !r.Enabled && exists && row.enabled {
		_, err = tx.Exec(`UPDATE watch_rule_baselines SET enabled=0 WHERE rule_id=?`, r.ID)
		return err
	}
	return nil
}

func updateBaselineOnReset(tx *store.Tx, id string, fileRules []Rule, changeSeq, nowMS int64) error {
	if i := slices.IndexFunc(fileRules, func(r Rule) bool { return r.ID == id }); i >= 0 {
		fr := fileRules[i]
		if fr.Enabled && fr.evaluatorReason() == "" {
			return updateBaselineForRule(tx, fr, changeSeq, nowMS)
		}
	}
	row, exists, err := readRuleBaselineRow(tx, id)
	if err != nil {
		return err
	}
	if exists && row.enabled {
		_, err = tx.Exec(`UPDATE watch_rule_baselines SET enabled=0 WHERE rule_id=?`, id)
		return err
	}
	return nil
}
