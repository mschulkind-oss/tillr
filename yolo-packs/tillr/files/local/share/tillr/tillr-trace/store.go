package main

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"
)

type StoreRun struct {
	RunID        string
	ItemID       *string
	LaneID       *string
	Backend      *string
	State        *string
	ExitCode     *int64
	StartedAt    *int64
	FinishedAt   *int64
	ArtifactRefs *string
}

type StoreDispatch struct {
	DecisionID   string
	ItemID       *string
	LaneID       *string
	RunID        *string
	Action       string
	Reason       string
	DispatchedAt *int64
}

type Store struct {
	Path   string
	DB     *sql.DB
	State  string
	Detail string
	Tables map[string]bool
}

func openStore(path string) *Store {
	s := &Store{
		Path:   path,
		State:  "ok",
		Tables: map[string]bool{},
	}
	if !fileExists(path) {
		s.State = "missing"
		s.Detail = fmt.Sprintf("no store at %s", path)
		return s
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		s.State = "unreadable"
		s.Detail = err.Error()
		return s
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA query_only = ON"); err != nil {
		_ = db.Close()
		s.State = "unreadable"
		s.Detail = err.Error()
		return s
	}
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		_ = db.Close()
		s.State = "unreadable"
		s.Detail = err.Error()
		return s
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			s.Tables[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = db.Close()
		s.State = "unreadable"
		s.Detail = err.Error()
		return s
	}
	s.DB = db
	return s
}

func (s *Store) Close() {
	if s.DB != nil {
		_ = s.DB.Close()
		s.DB = nil
	}
}

func (s *Store) runs() []StoreRun {
	if s.DB == nil {
		return nil
	}
	rows, err := s.DB.Query(
		"SELECT run_id, item_id, lane_id, backend, state, exit_code," +
			" started_at, finished_at, artifact_refs FROM runs",
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []StoreRun
	for rows.Next() {
		var r StoreRun
		if err := rows.Scan(
			&r.RunID, &r.ItemID, &r.LaneID, &r.Backend, &r.State, &r.ExitCode,
			&r.StartedAt, &r.FinishedAt, &r.ArtifactRefs,
		); err != nil {
			return nil
		}
		out = append(out, r)
	}
	return out
}

func (s *Store) dispatches() []StoreDispatch {
	if s.DB == nil {
		return nil
	}
	rows, err := s.DB.Query(
		"SELECT decision_id, item_id, lane_id, run_id, action, reason," +
			" dispatched_at FROM dispatches",
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []StoreDispatch
	for rows.Next() {
		var d StoreDispatch
		var action, reason sql.NullString
		if err := rows.Scan(
			&d.DecisionID, &d.ItemID, &d.LaneID, &d.RunID, &action, &reason, &d.DispatchedAt,
		); err != nil {
			return nil
		}
		d.Action = action.String
		d.Reason = reason.String
		out = append(out, d)
	}
	return out
}

func parseArtifactRefs(raw *string) map[string]string {
	if raw == nil || *raw == "" {
		return map[string]string{}
	}
	var refs []map[string]any
	if err := json.Unmarshal([]byte(*raw), &refs); err != nil {
		return map[string]string{}
	}
	out := make(map[string]string)
	for _, ref := range refs {
		if ref == nil {
			continue
		}
		kind, _ := asString(ref["kind"])
		val, _ := asString(ref["ref"])
		if val == "" {
			val, _ = asString(ref["Ref"])
		}
		if kind != "" && val != "" {
			out[kind] = val
		}
	}
	return out
}
