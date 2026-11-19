package item

import (
	"strings"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// currentTerminalEvidence compares receipts to the latest actual landed-to-queue
// transition, not wall-clock time. The item events carry the resulting state.
func currentTerminalEvidence(r *store.Reader, id string) (bool, error) {
	var returned int64
	err := r.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM (
        SELECT seq, json_extract(payload, '$.state') AS state,
          json_extract(payload, '$.lifecycle_action') AS action,
          LAG(json_extract(payload, '$.state')) OVER (ORDER BY seq) AS previous
        FROM events WHERE object_type='item' AND object_id=? AND kind IN ('item.add','item.update')
    ) WHERE state='in-queue' AND (previous='landed' OR action='state')`, id).Scan(&returned)
	if err != nil {
		return false, Internalf("checking landing returns: %v", err)
	}
	var graph, hasLandings bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind='drain.graph-completed' AND json_extract(payload, '$.item_id')=? AND seq>?)`, id, returned).Scan(&graph); err != nil {
		return false, Internalf("checking graph completion: %v", err)
	}
	if graph {
		return true, nil
	}
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='landings')`).Scan(&hasLandings); err != nil {
		return false, Internalf("checking landing schema: %v", err)
	}
	if !hasLandings {
		return false, nil
	}
	var landed bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM landings WHERE item_id=? AND result='landed' AND (?=0 OR event_seq>?))`, id, returned, returned).Scan(&landed); err != nil {
		return false, Internalf("checking landing receipts: %v", err)
	}
	return landed, nil
}

func checkLandedCriteria(id string, criteria []criterion) error {
	var unmet []string
	for _, c := range criteria {
		if c.State != "done" {
			unmet = append(unmet, c.CriterionID)
		}
	}
	if len(criteria) == 0 || len(unmet) != 0 {
		return Rejectionf("landed-criteria-unmet", id, "item %q needs at least one criterion and every criterion done; unmet: %s", id, strings.Join(unmet, ", "))
	}
	return nil
}

func checkTerminalEvidence(r *store.Reader, d definition.Definition, id, from, to string, receipt MoveReceipt) error {
	if d.Name != definition.GitLandingName || from == "" || from == to || (to != "landed" && to != "resolved-elsewhere") {
		return nil
	}
	if to == "landed" {
		p, _, err := readStoredItem(r, id)
		if err != nil {
			return err
		}
		if err := checkLandedCriteria(id, p.Criteria); err != nil {
			return err
		}
	}
	current, err := currentTerminalEvidence(r, id)
	if err != nil {
		return err
	}
	if to == "landed" && !current {
		return Rejectionf("landed-receipt-missing", id, "item %q has no current landing or graph completion receipt", id)
	}
	if to == "resolved-elsewhere" {
		if current {
			return Rejectionf("already-landed", id, "item %q already has a current landing receipt", id)
		}
		if strings.TrimSpace(receipt.ResolvesVia) == "" {
			return Rejectionf("resolved-pointer-missing", id, "item %q needs --resolves-via", id)
		}
	}
	return nil
}
