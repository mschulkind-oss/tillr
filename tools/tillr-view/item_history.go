package main

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// itemRunView keeps termination separate from state: a finished run need not have
// exited normally. A null end cause or spend means the store did not record it.
type itemRunView struct {
	RunID        string   `json:"run_id"`
	LaneID       string   `json:"lane_id"`
	Backend      string   `json:"backend"`
	State        string   `json:"state"`
	ExitCode     *int     `json:"exit_code"`
	StartedAtMS  int64    `json:"started_at_ms"`
	FinishedAtMS int64    `json:"finished_at_ms"`
	EndCause     *string  `json:"end_cause"`
	UsageTokens  *int64   `json:"usage_tokens"`
	CostUSD      *float64 `json:"cost_usd"`
}

func readItemRunHistory(r *store.Reader, runs []storeRun) ([]itemRunView, error) {
	out := []itemRunView{}
	for _, run := range runs {
		var cause sql.NullString
		err := r.QueryRow(`SELECT end_cause FROM runs WHERE run_id = ?`, run.RunID).Scan(&cause)
		if err != nil && !isMissingColumn(err) && err != sql.ErrNoRows {
			return nil, err
		}
		var end *string
		if cause.Valid && cause.String != "" {
			v := cause.String
			end = &v
		}
		out = append(out, itemRunView{run.RunID, run.LaneID, run.Backend, run.State, run.ExitCode, run.StartedAtMS, run.FinishedAtMS, end, run.UsageTokens, run.CostUSD})
	}
	return out, nil
}

// ownerDecisionView reads the owner's ledger, not dispatches. Keep superseded and
// revoked entries: an audit needs the earlier ruling as well as the current one.
type ownerDecisionView struct {
	DecisionID  string `json:"decision_id"`
	Text        string `json:"text"`
	ScopeKind   string `json:"scope_kind"`
	ScopeRef    string `json:"scope_ref"`
	DecidedBy   string `json:"decided_by"`
	DecidedAtMS int64  `json:"decided_at_ms"`
	Status      string `json:"status"`
	Supersedes  string `json:"supersedes"`
	SessionID   string `json:"session_id"`
}

func readOwnerDecisions(r *store.Reader, itemID string) ([]ownerDecisionView, error) {
	out := []ownerDecisionView{}
	rows, err := r.Query(`SELECT decision_id, text, scope_kind, scope_ref, decided_by, decided_at, status, COALESCE(supersedes,''), session_id FROM decisions WHERE scope_kind = 'items' ORDER BY decided_at, decision_id`)
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d ownerDecisionView
		if err := rows.Scan(&d.DecisionID, &d.Text, &d.ScopeKind, &d.ScopeRef, &d.DecidedBy, &d.DecidedAtMS, &d.Status, &d.Supersedes, &d.SessionID); err != nil {
			return nil, err
		}
		for _, id := range strings.Split(d.ScopeRef, ",") {
			if strings.TrimSpace(id) == itemID {
				out = append(out, d)
				break
			}
		}
	}
	return out, rows.Err()
}

// stateMoveView is a change observed in the event log. From is empty when the log
// supplies no earlier state; editing an item's definition without moving it is not
// a state move. The sequence identifies the source even when clocks tie.
type stateMoveView struct {
	EventSeq int64  `json:"event_seq"`
	Kind     string `json:"kind"`
	AtMS     int64  `json:"at_ms"`
	From     string `json:"from"`
	To       string `json:"to"`
	Action   string `json:"action"`
	Reason   string `json:"reason"`
	Actor    string `json:"actor"`
}

func readStateMoves(r *store.Reader, itemID string) ([]stateMoveView, error) {
	rows, err := r.Query(`SELECT seq, ts, kind, payload, COALESCE(actor, '') FROM events WHERE object_type = 'item' AND object_id = ? ORDER BY seq`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []stateMoveView{}
	previous := ""
	for rows.Next() {
		var m stateMoveView
		var raw string
		if err := rows.Scan(&m.EventSeq, &m.AtMS, &m.Kind, &raw, &m.Actor); err != nil {
			return nil, err
		}
		var p struct {
			State           string `json:"state"`
			Action          string `json:"lifecycle_action"`
			LifecycleReason string `json:"lifecycle_reason"`
			LifecycleBy     string `json:"lifecycle_by"`
			Reason          string `json:"reason"`
			Actor           string `json:"actor"`
			WaitingOn       *struct {
				Reason string `json:"reason"`
			} `json:"waiting_on"`
		}
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		if p.State == "" {
			continue
		}
		if p.State == previous {
			continue
		}
		m.From, m.To, m.Action = previous, p.State, p.Action
		m.Reason = p.Reason
		if p.Actor != "" {
			m.Actor = p.Actor
		}
		if p.WaitingOn != nil && m.Reason == "" {
			m.Reason = p.WaitingOn.Reason
		}
		if p.Action != "" {
			m.Reason = p.LifecycleReason
			if p.LifecycleBy != "" {
				m.Actor = p.LifecycleBy
			}
		}
		out = append(out, m)
		previous = p.State
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].AtMS < out[j].AtMS })
	return out, rows.Err()
}
