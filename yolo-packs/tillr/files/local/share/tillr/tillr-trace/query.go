package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type TickRow struct {
	TS         string
	Lane       *string
	Item       *string
	Run        *string
	Kind       string
	Summary    *string
	DurationMs *int64
	ExitCode   *int64
}

type TickResult struct {
	TickID string
	Reason string
	Rows   []TickRow
}

func qTick(db *sql.DB, tickID string) TickResult {
	if tickID == "" {
		return TickResult{Reason: "no tick_id recorded in any artifact"}
	}
	rows, err := db.Query(
		"SELECT ts_start, lane_id, item_id, run_id, kind, summary, duration_ms, payload"+
			" FROM obs_events WHERE tick_id = ? ORDER BY ts_start ASC, seq ASC",
		tickID,
	)
	if err != nil {
		return TickResult{TickID: tickID}
	}
	defer rows.Close()
	out := TickResult{TickID: tickID}
	for rows.Next() {
		var r TickRow
		var payloadStr string
		if err := rows.Scan(&r.TS, &r.Lane, &r.Item, &r.Run, &r.Kind, &r.Summary, &r.DurationMs, &payloadStr); err != nil {
			continue
		}
		var payload map[string]any
		dec := json.NewDecoder(strings.NewReader(payloadStr))
		dec.UseNumber()
		if err := dec.Decode(&payload); err == nil && payload != nil {
			r.ExitCode = int64Ptr(payload["exit_code"])
		}
		out.Rows = append(out.Rows, r)
	}
	return out
}

type AuditTurn struct {
	Model      *string
	Thinking   *string
	TokensIn   *int64
	TokensOut  *int64
	CostUSD    *float64
	DurationMs *int64
}

type AuditCommand struct {
	TS       string
	Kind     string
	Summary  *string
	ExitCode *int64
	Cwd      *string
}

type AuditClaim struct {
	ClaimKind         string
	ClaimText         string
	ObservedStatus    string
	DiscrepancyReason string
	LastDirEditAt     *string
}

type AuditResult struct {
	Run        RunMeta
	Turns      []AuditTurn
	Commands   []AuditCommand
	Unreadable []string
	Claims     []AuditClaim
}

func scanRunMeta(row *sql.Row) (*RunMeta, error) {
	var r RunMeta
	err := row.Scan(
		&r.RunID, &r.LaneID, &r.ItemID, &r.Backend, &r.StoreState,
		&r.ExitCode, &r.StartedAt, &r.FinishedAt, &r.LogPath, &r.LogState,
		&r.ProcPath, &r.ProcState, &r.GitPath, &r.GitState, &r.BriefPath,
		&r.FencePath, &r.BaseSHA, &r.HeadSHA, &r.TreeSHA, &r.Detail,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func qAudit(db *sql.DB, runID string) *AuditResult {
	run, err := scanRunMeta(db.QueryRow(
		"SELECT run_id, lane_id, item_id, backend, store_state, exit_code, started_at,"+
			" finished_at, log_path, log_state, proc_path, proc_state, git_path, git_state,"+
			" brief_path, fence_path, base_sha, head_sha, tree_sha, detail"+
			" FROM runs_index WHERE run_id = ?",
		runID,
	))
	if err != nil {
		return nil
	}

	res := &AuditResult{Run: *run}

	evRows, err := db.Query(
		"SELECT ts_start, kind, summary, duration_ms, payload FROM obs_events"+
			" WHERE run_id = ? ORDER BY ts_start ASC, seq ASC",
		runID,
	)
	if err == nil {
		defer evRows.Close()
		for evRows.Next() {
			var ts, kind, payloadStr string
			var summary *string
			var durationMs *int64
			if err := evRows.Scan(&ts, &kind, &summary, &durationMs, &payloadStr); err != nil {
				continue
			}
			var payload map[string]any
			dec := json.NewDecoder(strings.NewReader(payloadStr))
			dec.UseNumber()
			_ = dec.Decode(&payload)
			if payload == nil {
				payload = map[string]any{}
			}
			if kind == Unreadable {
				res.Unreadable = append(res.Unreadable, fmt.Sprintf("%v: %v", payload["artifact"], payload["reason"]))
			} else if kind == "llm.turn" {
				model := strPtr(payload["response_model"])
				if model == nil {
					model = strPtr(payload["model"])
				}
				tokens := asMap(payload["tokens"])
				var costPtr *float64
				if f, ok := asFloat(payload["cost_usd"]); ok {
					costPtr = &f
				}
				res.Turns = append(res.Turns, AuditTurn{
					Model:      model,
					Thinking:   strPtr(payload["thinking_level"]),
					TokensIn:   int64Ptr(tokens["in"]),
					TokensOut:  int64Ptr(tokens["out"]),
					CostUSD:    costPtr,
					DurationMs: durationMs,
				})
			} else if kind == "proc.exec" || kind == "tool.call" {
				cwd := strPtr(payload["repo_rel_cwd"])
				if cwd == nil {
					cwd = strPtr(payload["cwd"])
				}
				res.Commands = append(res.Commands, AuditCommand{
					TS:       ts,
					Kind:     kind,
					Summary:  summary,
					ExitCode: int64Ptr(payload["exit_code"]),
					Cwd:      cwd,
				})
			}
		}
	}

	clRows, err := db.Query(
		"SELECT claim_kind, claim_text, observed_status, discrepancy_reason,"+
			" last_dir_edit_at FROM claim_checks WHERE run_id = ?",
		runID,
	)
	if err == nil {
		defer clRows.Close()
		for clRows.Next() {
			var cl AuditClaim
			if err := clRows.Scan(&cl.ClaimKind, &cl.ClaimText, &cl.ObservedStatus, &cl.DiscrepancyReason, &cl.LastDirEditAt); err == nil {
				res.Claims = append(res.Claims, cl)
			}
		}
	}

	return res
}

type RunEvent struct {
	ObsID      string          `json:"obs_id"`
	Kind       string          `json:"kind"`
	TsStart    string          `json:"ts_start"`
	TsEnd      *string         `json:"ts_end"`
	DurationMs *int64          `json:"duration_ms"`
	Seam       string          `json:"seam"`
	SpanID     string          `json:"span_id"`
	Summary    string          `json:"summary"`
	Payload    json.RawMessage `json:"payload"`
}

type RunClaim struct {
	ClaimID           string `json:"claim_id"`
	ClaimKind         string `json:"claim_kind"`
	ClaimText         string `json:"claim_text"`
	ObservedStatus    string `json:"observed_status"`
	DiscrepancyReason string `json:"discrepancy_reason"`
	ObservedAt        string `json:"observed_at"`
}

type RunDocument struct {
	Run    *RunMeta   `json:"run"`
	Claims []RunClaim `json:"claims"`
	Events []RunEvent `json:"events"`
}

func qRun(db *sql.DB, runID string) RunDocument {
	run, err := scanRunMeta(db.QueryRow(
		"SELECT run_id, lane_id, item_id, backend, store_state, exit_code, started_at,"+
			" finished_at, log_path, log_state, proc_path, proc_state, git_path, git_state,"+
			" brief_path, fence_path, base_sha, head_sha, tree_sha, detail"+
			" FROM runs_index WHERE run_id = ?",
		runID,
	))
	if err != nil {
		return RunDocument{
			Run:    nil,
			Claims: []RunClaim{},
			Events: []RunEvent{},
		}
	}

	events := []RunEvent{}
	evRows, err := db.Query(
		"SELECT obs_id, kind, ts_start, ts_end, duration_ms, seam, span_id, summary,"+
			" payload FROM obs_events WHERE run_id = ? ORDER BY ts_start, seq",
		runID,
	)
	if err == nil {
		defer evRows.Close()
		for evRows.Next() {
			var ev RunEvent
			var seam, spanID, summary sql.NullString
			var payload string
			if err := evRows.Scan(
				&ev.ObsID, &ev.Kind, &ev.TsStart, &ev.TsEnd, &ev.DurationMs,
				&seam, &spanID, &summary, &payload,
			); err != nil {
				continue
			}
			ev.Seam = seam.String
			ev.SpanID = spanID.String
			ev.Summary = summary.String
			if payload == "" {
				payload = "{}"
			}
			ev.Payload = json.RawMessage(payload)
			events = append(events, ev)
		}
	}

	claims := []RunClaim{}
	clRows, err := db.Query(
		"SELECT claim_id, claim_kind, claim_text, observed_status, discrepancy_reason,"+
			" observed_at FROM claim_checks WHERE run_id = ? ORDER BY claim_id",
		runID,
	)
	if err == nil {
		defer clRows.Close()
		for clRows.Next() {
			var cl RunClaim
			if err := clRows.Scan(
				&cl.ClaimID, &cl.ClaimKind, &cl.ClaimText,
				&cl.ObservedStatus, &cl.DiscrepancyReason, &cl.ObservedAt,
			); err == nil {
				claims = append(claims, cl)
			}
		}
	}

	return RunDocument{
		Run:    run,
		Claims: claims,
		Events: events,
	}
}

func writeRun(doc RunDocument, out io.Writer) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(doc)
}

type CostRow struct {
	Lane       string
	Route      string
	Model      *string
	Runs       int64
	RetryTurns int64
	LLMWallS   float64
	TokIn      int64
	TokCacheR  int64
	TokOut     int64
	TokThink   int64
	CostUSD    float64
}

func qCost(db *sql.DB) []CostRow {
	rows, err := db.Query(
		"SELECT COALESCE(lane_id, 'coordinator/subagent') AS lane," +
			" COALESCE(route_id, 'unrouted') AS route," +
			" json_extract(payload, '$.response_model') AS model," +
			" COUNT(DISTINCT run_id) AS runs," +
			" COALESCE(SUM(CASE WHEN json_extract(payload, '$.stop_reason') IN ('error','abort')" +
			"     THEN 1 ELSE 0 END), 0) AS retry_turns," +
			" COALESCE(ROUND(SUM(duration_ms) / 1000.0, 1), 0.0) AS llm_wall_s," +
			" COALESCE(SUM(json_extract(payload, '$.tokens.in')), 0) AS tok_in," +
			" COALESCE(SUM(json_extract(payload, '$.tokens.cache_read')), 0) AS tok_cache_r," +
			" COALESCE(SUM(json_extract(payload, '$.tokens.out')), 0) AS tok_out," +
			" COALESCE(SUM(json_extract(payload, '$.tokens.reasoning')), 0) AS tok_think," +
			" COALESCE(ROUND(SUM(json_extract(payload, '$.cost_usd')), 6), 0.0) AS cost_usd" +
			" FROM obs_events WHERE kind = 'llm.turn'" +
			" GROUP BY lane, route, model ORDER BY cost_usd DESC",
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []CostRow
	for rows.Next() {
		var r CostRow
		if err := rows.Scan(
			&r.Lane, &r.Route, &r.Model, &r.Runs, &r.RetryTurns,
			&r.LLMWallS, &r.TokIn, &r.TokCacheR, &r.TokOut, &r.TokThink, &r.CostUSD,
		); err == nil {
			out = append(out, r)
		}
	}
	return out
}

type DiscrepancyRow struct {
	ObservedAt        string
	RunID             string
	LaneID            *string
	ItemID            *string
	ClaimKind         string
	ClaimText         string
	ObservedStatus    string
	DiscrepancyReason string
	LastDirEditAt     *string
}

func qDiscrepancies(db *sql.DB, since string, includeUnverified bool) []DiscrepancyRow {
	var sqlStr string
	var args []any
	if includeUnverified {
		sqlStr = "SELECT observed_at, run_id, lane_id, item_id, claim_kind, claim_text," +
			" observed_status, discrepancy_reason, last_dir_edit_at FROM claim_checks" +
			" WHERE observed_status IN ('disproved', 'unverified')"
	} else {
		sqlStr = "SELECT observed_at, run_id, lane_id, item_id, claim_kind, claim_text," +
			" observed_status, discrepancy_reason, last_dir_edit_at FROM claim_checks" +
			" WHERE observed_status IN ('disproved')"
	}
	if since != "" {
		sqlStr += " AND observed_at >= ?"
		args = append(args, since)
	}
	sqlStr += " ORDER BY observed_at DESC, run_id ASC"

	rows, err := db.Query(sqlStr, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []DiscrepancyRow
	for rows.Next() {
		var r DiscrepancyRow
		if err := rows.Scan(
			&r.ObservedAt, &r.RunID, &r.LaneID, &r.ItemID, &r.ClaimKind, &r.ClaimText,
			&r.ObservedStatus, &r.DiscrepancyReason, &r.LastDirEditAt,
		); err == nil {
			out = append(out, r)
		}
	}
	return out
}
