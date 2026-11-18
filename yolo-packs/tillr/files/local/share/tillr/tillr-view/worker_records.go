package main

import (
	"encoding/json"
	"github.com/mschulkind-oss/tillr/store"
)

// Copies of tillr-run's event payloads. Claims never alter criterion state.
type progressClaim struct {
	RunID       string `json:"run_id"`
	ItemID      string `json:"item_id"`
	CriterionID string `json:"criterion_id"`
}
type childSessionView struct {
	RunID      string `json:"run_id"`
	ItemID     string `json:"item_id"`
	LaneID     string `json:"lane_id"`
	SessionID  string `json:"session_id"`
	Role       string `json:"role"`
	StartedAt  *int64 `json:"started_at_ms"`
	EndedAt    *int64 `json:"ended_at_ms"`
	Turns      *int   `json:"turn_count"`
	EndClock   string `json:"end_clock"`
	Transcript string `json:"transcript"`
}
type childrenView struct {
	Note     string             `json:"note,omitempty"`
	Recorded bool               `json:"recorded"`
	Sessions []childSessionView `json:"sessions"`
}

func readWorkerRecords(r *store.Reader, runID, itemID string) ([]progressClaim, map[string]childrenView, error) {
	claims := []progressClaim{}
	children := map[string]childrenView{}
	rows, err := r.Query(`SELECT kind,payload FROM events WHERE kind IN ('run.progress','run.child-session','run.children-recorded','run.observation-error') AND (?='' OR object_id=?) AND (?='' OR json_extract(payload,'$.item_id')=?) ORDER BY seq`, runID, runID, itemID, itemID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var raw []byte
		if err := rows.Scan(&kind, &raw); err != nil {
			return nil, nil, err
		}
		if kind == "run.progress" {
			var c progressClaim
			if err := json.Unmarshal(raw, &c); err != nil {
				return nil, nil, err
			}
			claims = append(claims, c)
			continue
		}
		var c childSessionView
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, nil, err
		}
		group := children[c.RunID]
		if kind == "run.observation-error" {
			var note struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(raw, &note); err != nil {
				return nil, nil, err
			}
			group.Note = note.Error
			children[c.RunID] = group
			continue
		}
		group.Recorded = true
		if group.Sessions == nil {
			group.Sessions = []childSessionView{}
		}
		if kind == "run.child-session" {
			found := false
			for i, s := range group.Sessions {
				if s.SessionID == c.SessionID {
					group.Sessions[i] = c
					found = true
					break
				}
			}
			if !found {
				group.Sessions = append(group.Sessions, c)
			}
		}
		children[c.RunID] = group
	}
	return claims, children, rows.Err()
}

func enrichWorkerRecords(r *store.Reader, rows []laneRow) error {
	claims, children, err := readWorkerRecords(r, "", "")
	if err != nil {
		return err
	}
	for i := range rows {
		row := &rows[i]
		row.Progress = []progressClaim{}
		row.ChildSessions = map[string]childrenView{}
		runs := map[string]bool{row.RunID: true}
		for _, n := range row.Nodes {
			if n.Attempt == row.Attempt {
				runs[n.RunID] = true
			}
		}
		for _, c := range claims {
			if runs[c.RunID] {
				row.Progress = append(row.Progress, c)
			}
		}
		for id, c := range children {
			if runs[id] {
				row.ChildSessions[id] = c
			}
		}
	}
	return nil
}

func readChildRun(r *store.Reader, id string) (storeRun, bool, error) {
	var raw []byte
	rows, err := r.Query(`SELECT payload FROM events WHERE kind='run.child-session' AND json_extract(payload,'$.session_id')=? ORDER BY seq DESC LIMIT 1`, id)
	if err != nil {
		return storeRun{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return storeRun{}, false, rows.Err()
	}
	if err := rows.Scan(&raw); err != nil {
		return storeRun{}, false, err
	}
	var c childSessionView
	if err := json.Unmarshal(raw, &c); err != nil {
		return storeRun{}, false, err
	}
	run := storeRun{RunID: id, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "pi", State: "running", TranscriptPath: c.Transcript}
	if c.StartedAt != nil {
		run.StartedAtMS = *c.StartedAt
	}
	if c.EndedAt != nil {
		run.FinishedAtMS = *c.EndedAt
		run.State = "exited"
	}
	return run, true, nil
}
