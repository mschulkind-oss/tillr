package main

import (
	"fmt"

	"github.com/mschulkind-oss/tillr/store"
)

type graphRevisionView struct {
	RevisionID    int64  `json:"revision_id"`
	ProposingRun  string `json:"proposing_run"`
	ProposingNode string `json:"proposing_node"`
	Target        string `json:"target"`
	Workflow      string `json:"workflow"`
	Rule          string `json:"rule"`
	Detail        string `json:"detail"`
	BeforeNodes   int    `json:"before_nodes"`
	AfterNodes    int    `json:"after_nodes"`
	RecordedAtMS  int64  `json:"recorded_at_ms"`
}

func readGraphRevisions(r *store.Reader, itemID string) ([]graphRevisionView, error) {
	out := []graphRevisionView{}
	rows, err := r.Query(`SELECT revision_id,proposing_run,proposing_node,target,workflow,rule,detail,before_nodes,after_nodes,recorded_at FROM graph_revisions WHERE item_id=? ORDER BY revision_id`, itemID)
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v graphRevisionView
		if err := rows.Scan(&v.RevisionID, &v.ProposingRun, &v.ProposingNode, &v.Target, &v.Workflow, &v.Rule, &v.Detail, &v.BeforeNodes, &v.AfterNodes, &v.RecordedAtMS); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func addGraphRevisionHistory(r *store.Reader, rows []waitingRow) error {
	for i := range rows {
		revisions, err := readGraphRevisions(r, rows[i].ItemID)
		if err != nil {
			return err
		}
		changes := []string{}
		for _, v := range revisions {
			if v.Rule == "" {
				changes = append(changes, fmt.Sprintf("graph changed by %s (%s): %d → %d nodes", v.ProposingNode, v.ProposingRun, v.BeforeNodes, v.AfterNodes))
			}
		}
		rows[i].GraphChanges = changes
	}
	return nil
}
