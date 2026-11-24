package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

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
	if runID == "" && itemID != "" {
		if claims, children, ok, err := readWorkerRecordsOfItem(r, itemID); ok || err != nil {
			return claims, children, err
		}
	}
	rows, err := r.Query(`SELECT kind,payload FROM events WHERE kind IN ('run.progress','run.child-session','run.children-recorded','run.observation-error') AND (?='' OR object_id=?) AND (?='' OR json_extract(payload,'$.item_id')=?) ORDER BY seq`, runID, runID, itemID, itemID)
	if err != nil {
		return nil, nil, err
	}
	return scanWorkerRecords(rows)
}

// readWorkerRecordsOfItem reads the records that name the item. Matching the item_id in every
// worker record's payload decodes each of the million child-session events a live store holds,
// three seconds for an Item page; so the child-session records come through events_by_object
// for the runs the item has had, and the few other records are matched by payload as before.
// A child-session event is written under its own run, whose item it names. ok is false when the store has no table to name the item's runs, and the
// caller reads every record as before.
func readWorkerRecordsOfItem(r *store.Reader, itemID string) ([]progressClaim, map[string]childrenView, bool, error) {
	// Every place that ties a run to the item: the runs table, the drain's dispatches, the
	// workflow's node runs, and the small records that name the item and sit on a run.
	var marks []string
	var args []any
	seen := map[string]bool{}
	for _, q := range []string{
		`SELECT run_id FROM runs WHERE item_id = ?`,
		`SELECT run_id FROM dispatches WHERE item_id = ?`,
		`SELECT run_id FROM node_runs WHERE item_id = ?`,
		`SELECT proposing_run FROM graph_revisions WHERE item_id = ?`,
		`SELECT object_id FROM events WHERE kind = 'drain.started' AND json_extract(payload,'$.decision.item_id') = ?`,
		`SELECT object_id FROM events WHERE kind IN ('run.progress','run.children-recorded','run.observation-error') AND json_extract(payload,'$.item_id') = ?`,
	} {
		runRows, err := r.Query(q, itemID)
		if err != nil {
			if isMissingTable(err) || isMissingColumn(err) {
				if strings.Contains(q, "FROM events") {
					return nil, nil, false, nil
				}
				continue
			}
			return nil, nil, false, err
		}
		for runRows.Next() {
			var id *string
			if err := runRows.Scan(&id); err != nil {
				runRows.Close()
				return nil, nil, false, err
			}
			if id != nil && *id != "" && !seen[*id] {
				seen[*id] = true
				marks = append(marks, "?")
				args = append(args, *id)
			}
		}
		err = runRows.Err()
		runRows.Close()
		if err != nil {
			return nil, nil, false, err
		}
	}
	args = append(args, itemID, itemID)
	rows, err := r.Query(`SELECT kind,payload FROM (SELECT seq,kind,payload FROM events WHERE object_id IN (`+strings.Join(append(marks, "''"), ",")+`) AND +kind = 'run.child-session' AND json_extract(payload,'$.item_id')=?
 UNION ALL SELECT seq,kind,payload FROM events WHERE kind IN ('run.progress','run.children-recorded','run.observation-error') AND json_extract(payload,'$.item_id')=?)
 ORDER BY seq`, args...)
	if err != nil {
		return nil, nil, false, err
	}
	claims, children, err := scanWorkerRecords(rows)
	return claims, children, true, err
}

// readWorkerRecordsForRuns reads the records of just these runs, through the events_by_object
// index. Historical stores hold over a million child-session rows from pre-merge sampling, so
// reading the kind store-wide decodes a million rows; new runs write child-session events
// only on lifecycle changes (start and end).
func readWorkerRecordsForRuns(r *store.Reader, runs map[string]bool) ([]progressClaim, map[string]childrenView, error) {
	if len(runs) == 0 {
		return []progressClaim{}, map[string]childrenView{}, nil
	}
	marks := make([]string, 0, len(runs))
	args := make([]any, 0, len(runs))
	for id := range runs {
		marks = append(marks, "?")
		args = append(args, id)
	}
	rows, err := r.Query(`SELECT kind,payload FROM events WHERE object_type='run' AND object_id IN (`+strings.Join(append(marks, "''"), ",")+`) AND +kind IN ('run.progress','run.child-session','run.children-recorded','run.observation-error') ORDER BY seq`, args...)
	if err != nil {
		return nil, nil, err
	}
	return scanWorkerRecords(rows)
}

func scanWorkerRecords(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}) ([]progressClaim, map[string]childrenView, error) {
	claims := []progressClaim{}
	children := map[string]childrenView{}
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

func enrichWorkerRecords(r *store.Reader, rows []laneRow, stats *laneReadStats) error {
	if err := enrichWorkerStoreRecords(r, rows); err != nil {
		return err
	}
	applyChildTranscriptFiles(rows, stats)
	return nil
}

func enrichWorkerStoreRecords(r *store.Reader, rows []laneRow) error {
	rowRuns := make([]map[string]bool, len(rows))
	wanted := map[string]bool{}
	for i := range rows {
		row := &rows[i]
		runs := map[string]bool{row.RunID: true}
		for _, n := range row.Nodes {
			if n.Attempt == row.Attempt {
				runs[n.RunID] = true
			}
		}
		rowRuns[i] = runs
		for id := range runs {
			if id != "" {
				wanted[id] = true
			}
		}
	}
	claims, children, err := readWorkerRecordsForRuns(r, wanted)
	if err != nil {
		return err
	}
	for i := range rows {
		row := &rows[i]
		row.Progress = []progressClaim{}
		row.ChildSessions = map[string]childrenView{}
		runs := rowRuns[i]
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

func applyChildTranscriptFiles(rows []laneRow, stats *laneReadStats) {
	resolved := map[string]childrenView{}
	for i := range rows {
		row := &rows[i]
		var cloned map[string]childrenView
		for id, group := range row.ChildSessions {
			if prev, ok := resolved[id]; ok {
				if cloned == nil {
					cloned = make(map[string]childrenView, len(row.ChildSessions))
					for k, v := range row.ChildSessions {
						cloned[k] = v
					}
				}
				cloned[id] = prev
				continue
			}
			var sessions []childSessionView
			for j := range group.Sessions {
				s := group.Sessions[j]
				if s.EndedAt == nil && s.Turns == nil && s.Transcript != "" {
					facts := liveChildTranscriptFacts(s.Transcript, stats)
					changed := false
					if facts.started != nil && s.StartedAt == nil {
						s.StartedAt = facts.started
						changed = true
					}
					if facts.turns != nil {
						s.Turns = facts.turns
						changed = true
					}
					if changed {
						if sessions == nil {
							sessions = append([]childSessionView(nil), group.Sessions...)
						}
						sessions[j] = s
					}
				}
			}
			if sessions != nil {
				updated := childrenView{Recorded: group.Recorded, Sessions: sessions}
				resolved[id] = updated
				if cloned == nil {
					cloned = make(map[string]childrenView, len(row.ChildSessions))
					for k, v := range row.ChildSessions {
						cloned[k] = v
					}
				}
				cloned[id] = updated
			}
		}
		if cloned != nil {
			row.ChildSessions = cloned
		}
	}
}

type childTranscriptFacts struct {
	started *int64
	turns   *int
}

// liveChildTranscriptFacts reads start clock and assistant turn count directly from an
// open child's JSONL transcript so the lane view stays current while the child runs
// without appending a run.child-session event on every assistant turn.
func liveChildTranscriptFacts(path string, stats *laneReadStats) childTranscriptFacts {
	return memoLaneFile("child-transcript", path, "", stats, func() childTranscriptFacts {
		f, err := os.Open(path)
		if err != nil {
			stats.dep(fileDep{path: path})
			return childTranscriptFacts{}
		}
		defer f.Close()
		if info, err := f.Stat(); err == nil {
			stats.dep(depOf(path, info))
		} else {
			stats.dep(fileDep{path: path})
		}
		var started *int64
		turns := 0
		reader := bufio.NewReader(f)
		for {
			raw, err := reader.ReadBytes('\n')
			if err == io.EOF {
				break
			}
			if err != nil {
				return childTranscriptFacts{}
			}
			stats.add(len(raw))
			var entry struct {
				Type      string `json:"type"`
				Timestamp string `json:"timestamp"`
				Message   struct {
					Role      string `json:"role"`
					Timestamp int64  `json:"timestamp"`
				} `json:"message"`
			}
			if err := json.Unmarshal(raw, &entry); err != nil {
				return childTranscriptFacts{}
			}
			if entry.Type == "session" {
				if at, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
					n := at.UnixMilli()
					started = &n
				}
			}
			if entry.Type == "message" && entry.Message.Role == "assistant" && (started == nil || entry.Message.Timestamp >= *started) {
				turns++
			}
		}
		return childTranscriptFacts{started: started, turns: &turns}
	})
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
