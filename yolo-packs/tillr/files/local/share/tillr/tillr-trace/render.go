package main

import (
	"fmt"
	"strconv"
	"strings"
)

func pyStr(p *string) string {
	if p == nil {
		return "None"
	}
	return *p
}

func pyInt(p *int64) string {
	if p == nil {
		return "None"
	}
	return strconv.FormatInt(*p, 10)
}

func pyFloat(p *float64) string {
	if p == nil {
		return "None"
	}
	return strconv.FormatFloat(*p, 'g', -1, 64)
}

func sliceClock(ts string) string {
	if len(ts) <= 11 {
		return ""
	}
	end := 23
	if len(ts) < end {
		end = len(ts)
	}
	return ts[11:end]
}

func slice19(ts string) string {
	if len(ts) <= 19 {
		return ts
	}
	return ts[:19]
}

func renderTick(result TickResult) string {
	if result.TickID == "" {
		return fmt.Sprintf("tick: no rows (%s)", result.Reason)
	}
	lines := []string{fmt.Sprintf("tick %s — %d observed row(s)", result.TickID, len(result.Rows))}
	if len(result.Rows) == 0 {
		lines = append(lines, "  (no obs.event row carries this tick_id; the store answered nothing either — an empty tick, not a verified quiet one)")
	}
	for _, row := range result.Rows {
		clock := sliceClock(row.TS)
		ec := ""
		if row.ExitCode != nil {
			ec = fmt.Sprintf(" rc=%d", *row.ExitCode)
		}
		lane := "-"
		if row.Lane != nil && *row.Lane != "" {
			lane = *row.Lane
		}
		item := "-"
		if row.Item != nil && *row.Item != "" {
			item = *row.Item
		}
		summary := ""
		if row.Summary != nil {
			summary = *row.Summary
		}
		lines = append(lines, fmt.Sprintf("  %s %-8s %-5s %-16s %s%s", clock, lane, item, row.Kind, summary, ec))
	}
	return strings.Join(lines, "\n")
}

func renderAudit(result *AuditResult) string {
	if result == nil {
		return "audit: no run row — no store row and no trace directory for that run-id"
	}
	run := result.Run
	lines := []string{
		fmt.Sprintf("run %s  lane=%s item=%s store_state=%s exit=%s",
			run.RunID, pyStr(run.LaneID), pyStr(run.ItemID), pyStr(run.StoreState), pyInt(run.ExitCode)),
		fmt.Sprintf("  log %s [%s]  proc [%s]",
			pyStr(run.LogPath), pyStr(run.LogState), pyStr(run.ProcState)),
		fmt.Sprintf("  base_sha=%s head_sha=%s tree_sha=%s",
			pyStr(run.BaseSHA), pyStr(run.HeadSHA), pyStr(run.TreeSHA)),
	}
	if run.Detail != "" {
		lines = append(lines, fmt.Sprintf("  detail: %s", run.Detail))
	}
	for _, note := range result.Unreadable {
		lines = append(lines, fmt.Sprintf("  unreadable: %s", note))
	}
	if len(result.Turns) > 0 {
		var total float64
		var tokens int64
		for _, turn := range result.Turns {
			if turn.CostUSD != nil {
				total += *turn.CostUSD
			}
			if turn.TokensOut != nil {
				tokens += *turn.TokensOut
			}
		}
		lines = append(lines, fmt.Sprintf("  asked: %d model turn(s), %d output token(s), $%.6f", len(result.Turns), tokens, total))
	}
	limitTurns := len(result.Turns)
	if limitTurns > 10 {
		limitTurns = 10
	}
	for _, turn := range result.Turns[:limitTurns] {
		lines = append(lines, fmt.Sprintf("    turn %s thinking=%s in=%s out=%s $%s %sms",
			pyStr(turn.Model), pyStr(turn.Thinking), pyInt(turn.TokensIn),
			pyInt(turn.TokensOut), pyFloat(turn.CostUSD), pyInt(turn.DurationMs)))
	}
	if len(result.Commands) > 0 {
		lines = append(lines, fmt.Sprintf("  ran: %d observed call(s)", len(result.Commands)))
	}
	limitCmds := len(result.Commands)
	if limitCmds > 25 {
		limitCmds = 25
	}
	for _, cmd := range result.Commands[:limitCmds] {
		ec := ""
		if cmd.ExitCode != nil {
			ec = fmt.Sprintf(" rc=%d", *cmd.ExitCode)
		}
		summary := ""
		if cmd.Summary != nil {
			summary = *cmd.Summary
		}
		lines = append(lines, fmt.Sprintf("    %s %-10s %s%s", sliceClock(cmd.TS), cmd.Kind, summary, ec))
	}
	if len(result.Claims) > 0 {
		lines = append(lines, "  claims:")
	}
	for _, claim := range result.Claims {
		reason := ""
		if claim.DiscrepancyReason != "" {
			reason = " — " + claim.DiscrepancyReason
		}
		lines = append(lines, fmt.Sprintf("    %-10s %-14s %s%s", claim.ObservedStatus, claim.ClaimKind, claim.ClaimText, reason))
	}
	return strings.Join(lines, "\n")
}

func renderCost(rows []CostRow) string {
	if len(rows) == 0 {
		return "cost: no llm.turn row — no run log held a model turn (see `audit <run-id>`)"
	}
	head := fmt.Sprintf("%-22s %-58s %4s %5s %8s %8s %9s %7s %6s %10s",
		"lane", "route", "runs", "retry", "wall_s", "tok_in", "cache_r", "tok_out", "think", "cost_usd")
	lines := []string{head}
	for _, row := range rows {
		lane := row.Lane
		if len(lane) > 22 {
			lane = lane[:22]
		}
		wallStr := "0"
		if row.LLMWallS != 0 {
			wallStr = strconv.FormatFloat(row.LLMWallS, 'f', -1, 64)
		}
		lines = append(lines, fmt.Sprintf(
			"%-22s %-58s %4d %5d %8s %8d %9d %7d %6d %10.6f",
			lane, row.Route, row.Runs, row.RetryTurns, wallStr,
			row.TokIn, row.TokCacheR, row.TokOut, row.TokThink, row.CostUSD,
		))
	}
	lines = append(lines, "(retry = turns whose stop_reason is error/abort; llm.retry rows are Slice 3, so a 429 retried inside one turn is not counted)")
	return strings.Join(lines, "\n")
}

func renderDiscrepancies(rows []DiscrepancyRow, includeUnverified bool) string {
	if len(rows) == 0 {
		what := "disproved claim"
		if includeUnverified {
			what = "claim"
		}
		return fmt.Sprintf("discrepancies: none (no %s in the runs read)", what)
	}
	lines := []string{fmt.Sprintf("discrepancies: %d row(s)", len(rows))}
	for _, row := range rows {
		lines = append(lines, fmt.Sprintf("  %s %-10s %s %-14s %s",
			slice19(row.ObservedAt), row.ObservedStatus, row.RunID, row.ClaimKind, row.ClaimText))
		if row.DiscrepancyReason != "" {
			lines = append(lines, fmt.Sprintf("      %s", row.DiscrepancyReason))
		}
	}
	return strings.Join(lines, "\n")
}
