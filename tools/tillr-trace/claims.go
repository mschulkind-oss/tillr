package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	moduleRoots = map[string]int{
		"tools":      2,
		"yolo-packs": 2,
		"tests":      1,
	}

	countWords = map[string]int{
		"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	}

	quantifierRe = regexp.MustCompile(
		`(?i)all\s+(\d+|one|two|three|four|five|six|seven|eight|nine|ten)\s+` +
			`(suites|tests|packages|modules)\s+` +
			`(pass|passed|exit 0|exits 0|succeeded|succeed|are green|green)`,
	)

	backtickRe   = regexp.MustCompile("`([^`\\n]{3,120})`")
	resultDoneRe = regexp.MustCompile(`RESULT:\s*done`)
)

func moduleOf(path string) string {
	if path == "" {
		return ""
	}
	rawParts := strings.Split(path, "/")
	parts := make([]string, 0, len(rawParts))
	for _, p := range rawParts {
		if p != "" && p != "." {
			parts = append(parts, p)
		}
	}
	for i, part := range parts {
		depth, ok := moduleRoots[part]
		if ok && len(parts) >= i+depth {
			return strings.Join(parts[i:i+depth], "/")
		}
	}
	return ""
}

type observedEdit struct {
	ts     string
	module string
}

type observedTest struct {
	ts       string
	module   string
	exitCode *int64
	spanID   string
	command  string
}

func observedEdits(db *sql.DB, runID string) []observedEdit {
	rows, err := db.Query(
		"SELECT ts_start, payload FROM obs_events WHERE run_id = ? AND kind = 'tool.call'",
		runID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var edits []observedEdit
	for rows.Next() {
		var ts, payloadStr string
		if err := rows.Scan(&ts, &payloadStr); err != nil {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(payloadStr), &data); err != nil || data == nil {
			continue
		}
		toolName, _ := asString(data["tool_name"])
		targetPath, _ := asString(data["target_path"])
		if editTools[toolName] && targetPath != "" {
			if mod := moduleOf(targetPath); mod != "" {
				edits = append(edits, observedEdit{ts: ts, module: mod})
			}
		}
	}
	return edits
}

func observedTests(db *sql.DB, runID string) []observedTest {
	rows, err := db.Query(
		"SELECT ts_start, span_id, payload FROM obs_events WHERE run_id = ? AND kind = 'proc.exec'",
		runID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var tests []observedTest
	for rows.Next() {
		var ts string
		var span sql.NullString
		var payloadStr string
		if err := rows.Scan(&ts, &span, &payloadStr); err != nil {
			continue
		}
		var data map[string]any
		dec := json.NewDecoder(strings.NewReader(payloadStr))
		dec.UseNumber()
		if err := dec.Decode(&data); err != nil || data == nil {
			continue
		}
		var argv []string
		if rawArgv, ok := data["argv"].([]any); ok {
			for _, a := range rawArgv {
				argv = append(argv, fmt.Sprint(a))
			}
		}
		command := strings.Join(argv, " ")
		if !testCommandRe.MatchString(command) {
			continue
		}
		mod, _ := asString(data["repo_rel_cwd"])
		if mod == "" {
			cwd, _ := asString(data["cwd"])
			mod = moduleOf(cwd)
		}
		ec := int64Ptr(data["exit_code"])
		tests = append(tests, observedTest{
			ts:       ts,
			module:   mod,
			exitCode: ec,
			spanID:   span.String,
			command:  command,
		})
	}
	return tests
}

type ClaimRow struct {
	ClaimID           string
	RunID             string
	LaneID            *string
	ItemID            *string
	ClaimKind         string
	ClaimText         string
	ObservedStatus    string
	DiscrepancyReason string
	EvidenceSpanIDs   string
	LastDirEditAt     *string
	ObservedAt        string
}

func runClock(run RunMeta) string {
	var ms *int64
	if run.FinishedAt != nil && *run.FinishedAt != 0 {
		ms = run.FinishedAt
	} else if run.StartedAt != nil && *run.StartedAt != 0 {
		ms = run.StartedAt
	}
	if iso := isoFromIntMs(ms); iso != nil {
		return *iso
	}
	return ""
}

func makeClaimRow(run RunMeta, kind, text, status, reason string, evidence []string, lastEdit *string) ClaimRow {
	sum := sha256.Sum256([]byte(text))
	if evidence == nil {
		evidence = []string{}
	}
	return ClaimRow{
		ClaimID:           fmt.Sprintf("claim-%s-%s-%s", run.RunID, kind, hex.EncodeToString(sum[:])[:8]),
		RunID:             run.RunID,
		LaneID:            run.LaneID,
		ItemID:            run.ItemID,
		ClaimKind:         kind,
		ClaimText:         truncateRunes(text, ExcerptMax),
		ObservedStatus:    status,
		DiscrepancyReason: truncateRunes(reason, ExcerptMax),
		EvidenceSpanIDs:   marshalJSONNoEscape(evidence),
		LastDirEditAt:     lastEdit,
		ObservedAt:        runClock(run),
	}
}

func verifyRun(db *sql.DB, run RunMeta, summary LogSummary) []ClaimRow {
	var claims []ClaimRow
	procState := ""
	if run.ProcState != nil {
		procState = *run.ProcState
	}
	haveProc := procState == "ok"
	edits := observedEdits(db, run.RunID)
	tests := observedTests(db, run.RunID)

	var passed []observedTest
	for _, t := range tests {
		if t.exitCode != nil && *t.exitCode == 0 {
			passed = append(passed, t)
		}
	}

	// Class 1: owed suites.
	modSet := map[string]bool{}
	for _, e := range edits {
		modSet[e.module] = true
	}
	modules := make([]string, 0, len(modSet))
	for m := range modSet {
		modules = append(modules, m)
	}
	sort.Strings(modules)

	for _, module := range modules {
		lastEdit := ""
		for _, e := range edits {
			if e.module == module && e.ts > lastEdit {
				lastEdit = e.ts
			}
		}
		var after, before []observedTest
		for _, t := range passed {
			if t.module == module && t.ts != "" {
				if t.ts > lastEdit {
					after = append(after, t)
				} else {
					before = append(before, t)
				}
			}
		}
		text := fmt.Sprintf("owed suite: %s", module)
		lastEditCopy := lastEdit
		if len(after) > 0 {
			ev := make([]string, len(after))
			for i, t := range after {
				ev[i] = t.spanID
			}
			claims = append(claims, makeClaimRow(run, "owed_suite", text, "verified", "", ev, &lastEditCopy))
		} else if !haveProc {
			claims = append(claims, makeClaimRow(
				run, "owed_suite", text, "unverified",
				fmt.Sprintf("no proc.exec record for this run (proc-exec.jsonl: %s)", procState),
				nil, &lastEditCopy,
			))
		} else if len(before) > 0 {
			ev := make([]string, len(before))
			for i, t := range before {
				ev[i] = t.spanID
			}
			claims = append(claims, makeClaimRow(
				run, "owed_suite", text, "disproved",
				fmt.Sprintf("%s: suite passed only before the last edit at %s", module, lastEdit),
				ev, &lastEditCopy,
			))
		} else {
			claims = append(claims, makeClaimRow(
				run, "owed_suite", text, "disproved",
				fmt.Sprintf("%s: no suite ran at all (last edit %s)", module, lastEdit),
				nil, &lastEditCopy,
			))
		}
	}

	// Class 2a: prose quantifiers from the final assistant message.
	finalText := summary.FinalText
	for _, m := range quantifierRe.FindAllStringSubmatch(finalText, -1) {
		raw := strings.ToLower(m[1])
		noun := strings.ToLower(m[2])
		claimed, err := strconv.Atoi(raw)
		if err != nil {
			claimed = countWords[raw]
		}
		if claimed == 0 {
			continue
		}
		passingModSet := map[string]bool{}
		passedSpans := make([]string, 0, len(passed))
		for _, t := range passed {
			if t.module != "" {
				passingModSet[t.module] = true
			}
			passedSpans = append(passedSpans, t.spanID)
		}
		text := m[0]
		if claimed <= len(passingModSet) {
			claims = append(claims, makeClaimRow(run, "suite_count", text, "verified", "", passedSpans, nil))
		} else if !haveProc {
			claims = append(claims, makeClaimRow(
				run, "suite_count", text, "unverified",
				fmt.Sprintf("no proc.exec record for this run (proc-exec.jsonl: %s)", procState),
				nil, nil,
			))
		} else {
			claims = append(claims, makeClaimRow(
				run, "suite_count", text, "disproved",
				fmt.Sprintf("claimed %d passing %s; observed %d distinct passing module(s)", claimed, noun, len(passingModSet)),
				passedSpans, nil,
			))
		}
	}

	// Class 2b: backticked command claims.
	seenCmds := map[string]bool{}
	for _, m := range backtickRe.FindAllStringSubmatch(finalText, -1) {
		command := m[1]
		if seenCmds[command] {
			continue
		}
		seenCmds[command] = true
		if !testCommandRe.MatchString(command) {
			continue
		}
		parts := strings.Split(strings.TrimSpace(command), "&&")
		needle := truncateRunes(strings.TrimSpace(parts[len(parts)-1]), 60)
		var matches []observedTest
		for _, t := range tests {
			if needle != "" && strings.Contains(t.command, needle) {
				matches = append(matches, t)
			}
		}
		text := fmt.Sprintf("`%s`", needle)
		hasZero := false
		matchSpans := make([]string, 0, len(matches))
		ecs := make([]string, 0, len(matches))
		for _, t := range matches {
			matchSpans = append(matchSpans, t.spanID)
			if t.exitCode != nil && *t.exitCode == 0 {
				hasZero = true
			}
			if t.exitCode != nil {
				ecs = append(ecs, strconv.FormatInt(*t.exitCode, 10))
			} else {
				ecs = append(ecs, "None")
			}
		}
		if hasZero {
			claims = append(claims, makeClaimRow(run, "command_exit", text, "verified", "", matchSpans, nil))
		} else if len(matches) > 0 {
			claims = append(claims, makeClaimRow(
				run, "command_exit", text, "disproved",
				"command ran and exited nonzero: "+strings.Join(ecs, ","),
				matchSpans, nil,
			))
		} else if !haveProc {
			claims = append(claims, makeClaimRow(
				run, "command_exit", text, "unverified",
				fmt.Sprintf("no proc.exec record for this run (proc-exec.jsonl: %s)", procState),
				nil, nil,
			))
		} else {
			claims = append(claims, makeClaimRow(
				run, "command_exit", text, "unverified",
				"command appears in no proc.exec row",
				nil, nil,
			))
		}
	}

	// Class 3: RESULT: done on a run that committed nothing.
	if resultDoneRe.MatchString(finalText) {
		base := derefStr(run.BaseSHA)
		head := derefStr(run.HeadSHA)
		tree := derefStr(run.TreeSHA)
		if head != "" && base != "" && tree != "" {
			if head == base && tree != base {
				claims = append(claims, makeClaimRow(
					run, "candidate_commit", "RESULT: done", "disproved",
					"uncommitted_worktree_with_result_done: head_sha == base_sha but tree_sha differs",
					nil, nil,
				))
			} else if head == base {
				claims = append(claims, makeClaimRow(
					run, "candidate_commit", "RESULT: done", "disproved",
					"no candidate commit: head_sha == base_sha and tree unchanged",
					nil, nil,
				))
			} else {
				claims = append(claims, makeClaimRow(run, "candidate_commit", "RESULT: done", "verified", "", nil, nil))
			}
		} else {
			claims = append(claims, makeClaimRow(
				run, "candidate_commit", "RESULT: done", "unverified",
				"no candidate shas recorded for this run", nil, nil,
			))
		}
	}

	return claims
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
