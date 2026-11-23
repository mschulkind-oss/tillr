package drain

import (
	"context"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

type trunkFailureLinked struct {
	ItemID      string `json:"item_id"`
	RedSHA      string `json:"red_sha"`
	CheckID     string `json:"check_id"`
	MatchedName string `json:"matched_name"`
	Detail      string `json:"detail,omitempty"`
	LogRef      string `json:"log_ref,omitempty"`
}

// heavyCheckSearchNames returns the test and check identifiers a matching open
// item's title or words might name for a failing heavy check.
func heavyCheckSearchNames(check HeavyCheck) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	if strings.HasPrefix(check.CheckID, goTestPrefix) {
		parts := strings.SplitN(strings.TrimPrefix(check.CheckID, goTestPrefix), ":", 3)
		if len(parts) == 3 && parts[2] != "" {
			add(parts[2])
			if root := testRootName(parts[2]); root != parts[2] {
				add(root)
			}
		}
		add(check.CheckID)
		return out
	}
	add(check.CheckID)
	if check.LogPath != "" {
		if found, ok := failingTestsFromLog(check.LogPath); ok {
			for _, name := range collapseSingleTest(found) {
				add(name)
			}
		}
	}
	return out
}

// findOpenCheckFixer searches open non-fixer items in queue order for one whose
// title, words, criteria, or prose names any of searchNames as the test or check
// it fixes.
func (l *Loop) findOpenCheckFixer(ctx context.Context, searchNames []string) (string, string, error) {
	if len(searchNames) == 0 || l.cfg.Store == "" {
		return "", "", nil
	}
	var foundID, foundName string
	err := l.readStore(ctx, func(r *store.Reader) error {
		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		rows, err := r.Query(`SELECT item_id, title, state, COALESCE(prose, '') FROM items ORDER BY rank, item_id`)
		if err != nil {
			return err
		}
		type openRow struct {
			id, title, state, prose string
		}
		var open []openRow
		for rows.Next() {
			var row openRow
			if err := rows.Scan(&row.id, &row.title, &row.state, &row.prose); err != nil {
				rows.Close()
				return err
			}
			if IsTrunkFixer(row.id) {
				continue
			}
			if row.state == "not-started" || finishedState(declared.Parsed, row.state) || closedState(declared.Parsed, row.state) {
				continue
			}
			open = append(open, row)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		for _, row := range open {
			criteria, err := readItemCriteriaTexts(r, row.id)
			if err != nil {
				return err
			}
			words, err := item.RequestWords(r, row.id)
			if err != nil {
				return err
			}
			for _, name := range searchNames {
				if itemFixesTest(row.title, criteria, row.prose, string(words), name) {
					foundID, foundName = row.id, name
					return nil
				}
			}
		}
		return nil
	})
	return foundID, foundName, err
}

// linkMatchingOpenFixers links unattributed trunk proof failures (no single red
// commit) to any open item that already names that check's test or check ID as
// the thing it fixes, recording the link on the item instead of filing a new
// trunk fixer. Checks with a single red commit or no matching open item are
// returned for ordinary fixer filing.
func (l *Loop) linkMatchingOpenFixers(ctx context.Context, sha string, checks []HeavyCheck) ([]HeavyCheck, error) {
	var remaining []HeavyCheck
	var linked []trunkFailureLinked
	for _, check := range checks {
		if check.FirstRedLanding != "" {
			remaining = append(remaining, check)
			continue
		}
		itemID, matchedName, err := l.findOpenCheckFixer(ctx, heavyCheckSearchNames(check))
		if err != nil {
			return nil, err
		}
		if itemID == "" {
			remaining = append(remaining, check)
			continue
		}
		logRef := check.LogRef
		if logRef == "" {
			logRef = check.LogPath
		}
		linked = append(linked, trunkFailureLinked{
			ItemID:      itemID,
			RedSHA:      sha,
			CheckID:     check.CheckID,
			MatchedName: matchedName,
			Detail:      strings.TrimSpace(check.Detail),
			LogRef:      logRef,
		})
	}
	if len(linked) == 0 {
		return remaining, nil
	}
	now := time.Now().UTC()
	if l.cfg.Now != nil {
		now = l.cfg.Now().UTC()
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return nil, Incompatiblef("%v", err)
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, rec := range linked {
			if err := appendEffectOn(tx, TrunkFailureLinkedKind, "item", rec.ItemID, rec, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, classifyWrite(err)
	}
	for _, rec := range linked {
		l.reportf("loop: heavy %s at %s linked to open item %s (matches %s); no fixer filed\n",
			rec.CheckID, shortSHA(rec.RedSHA), rec.ItemID, rec.MatchedName)
	}
	return remaining, nil
}
