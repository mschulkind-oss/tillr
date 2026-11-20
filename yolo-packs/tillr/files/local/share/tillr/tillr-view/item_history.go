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
	Seat         string   `json:"seat,omitempty"`
	Workflow     string   `json:"workflow,omitempty"`
	Node         string   `json:"node,omitempty"`
	State        string   `json:"state"`
	ExitCode     *int     `json:"exit_code"`
	StartedAtMS  int64    `json:"started_at_ms"`
	FinishedAtMS int64    `json:"finished_at_ms"`
	EndCause     *string  `json:"end_cause"`
	UsageTokens  *int64   `json:"usage_tokens"`
	CostUSD      *float64 `json:"cost_usd"`
}

func readItemRunHistory(r *store.Reader, runs []storeRun) ([]itemRunView, error) {
	dispatched, err := dispatchSeats(r)
	if err != nil {
		return nil, err
	}
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
		seat := run.Seat
		if seat == "" {
			seat = dispatched[run.RunID]
		}
		out = append(out, itemRunView{
			RunID:        run.RunID,
			LaneID:       run.LaneID,
			Backend:      run.Backend,
			Seat:         seat,
			Workflow:     run.Workflow,
			Node:         run.Node,
			State:        run.State,
			ExitCode:     run.ExitCode,
			StartedAtMS:  run.StartedAtMS,
			FinishedAtMS: run.FinishedAtMS,
			EndCause:     end,
			UsageTokens:  run.UsageTokens,
			CostUSD:      run.CostUSD,
		})
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

// verifyRefusalView is one verify-not-done refusal and the step that ran after it. NextStep is
// the workflow node (`work`, `plan`, `work/piece-one`) of the next run the drain started for
// the item, or that run's seat when it ran outside a workflow node. The Next fields are null
// until a later step has started: nothing ran next yet is not an empty step name. Replan is
// the drain's own marker that the findings said the plan was wrong.
type verifyRefusalView struct {
	EventSeq     int64   `json:"event_seq"`
	AtMS         int64   `json:"at_ms"`
	RunID        string  `json:"run_id"`
	Attempt      *int    `json:"attempt"`
	Detail       string  `json:"detail"`
	Replan       bool    `json:"replan"`
	NextRunID    *string `json:"next_run_id"`
	NextAttempt  *int    `json:"next_attempt"`
	NextStep     *string `json:"next_step"`
	NextSeat     *string `json:"next_seat"`
	NextEventSeq *int64  `json:"next_event_seq"`
	NextAtMS     *int64  `json:"next_at_ms"`
}

func readVerifyRefusals(r *store.Reader, itemID string) ([]verifyRefusalView, error) {
	out := []verifyRefusalView{}
	rows, err := r.Query(`SELECT seq, ts, COALESCE(object_id, ''), COALESCE(json_extract(payload, '$.detail'), ''), COALESCE(json_extract(payload, '$.replan'), 0)
 FROM events WHERE kind = 'drain.refusal' AND object_type = 'run' AND json_valid(payload)
 AND json_extract(payload, '$.item_id') = ? AND json_extract(payload, '$.step') = 'verify-not-done' ORDER BY seq`, itemID)
	if err != nil {
		return nil, err
	}
	refusals := []verifyRefusalView{}
	for rows.Next() {
		var x verifyRefusalView
		var replan int
		if err := rows.Scan(&x.EventSeq, &x.AtMS, &x.RunID, &x.Detail, &replan); err != nil {
			rows.Close()
			return nil, err
		}
		x.Replan = replan != 0
		refusals = append(refusals, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(refusals) == 0 {
		return out, err
	}
	nodes, err := nodeRunsByRun(r, itemID)
	if err != nil {
		return nil, err
	}
	type started struct {
		seq, atMS       int64
		runID, seat, id string
	}
	var starts []started
	srows, err := r.Query(`SELECT seq, ts, COALESCE(object_id, ''), payload FROM events
 WHERE kind IN ('drain.started', 'drain.node-advanced') AND object_type = 'run' AND json_valid(payload)
 AND json_extract(payload, '$.decision.item_id') = ? ORDER BY seq`, itemID)
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var s started
		var raw []byte
		if err := srows.Scan(&s.seq, &s.atMS, &s.runID, &raw); err != nil {
			srows.Close()
			return nil, err
		}
		var d struct {
			Decision struct {
				Seat      string     `json:"seat"`
				NodeIndex int        `json:"node_index"`
				Workflow  *itemGraph `json:"workflow"`
			} `json:"decision"`
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			srows.Close()
			return nil, err
		}
		s.seat = d.Decision.Seat
		if w := d.Decision.Workflow; w != nil && d.Decision.NodeIndex >= 0 && d.Decision.NodeIndex < len(w.Stages) {
			s.id = w.Stages[d.Decision.NodeIndex].ID
			if s.seat == "" {
				s.seat = w.Stages[d.Decision.NodeIndex].Seat
			}
		}
		starts = append(starts, s)
	}
	err = srows.Err()
	srows.Close()
	if err != nil {
		return nil, err
	}
	for _, v := range refusals {
		if n, ok := nodes[v.RunID]; ok {
			attempt := n.Attempt
			v.Attempt = &attempt
		}
		for _, s := range starts {
			if s.seq <= v.EventSeq || s.runID == v.RunID {
				continue
			}
			step := s.id
			if n, ok := nodes[s.runID]; ok {
				step = n.NodeID
				attempt := n.Attempt
				v.NextAttempt = &attempt
			}
			if step == "" {
				step = s.seat
			}
			run, seq, at := s.runID, s.seq, s.atMS
			v.NextRunID, v.NextEventSeq, v.NextAtMS = &run, &seq, &at
			if step != "" {
				v.NextStep = &step
			}
			if s.seat != "" {
				seat := s.seat
				v.NextSeat = &seat
			}
			break
		}
		out = append(out, v)
	}
	return out, nil
}

func nodeRunsByRun(r *store.Reader, itemID string) (map[string]laneNode, error) {
	nodes, err := laneNodes(r, itemID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]laneNode, len(nodes))
	for _, n := range nodes {
		out[n.RunID] = n
	}
	return out, nil
}
