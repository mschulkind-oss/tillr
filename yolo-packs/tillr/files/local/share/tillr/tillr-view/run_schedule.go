package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// The drain's own scheduling decisions about one run, in chronological order: its
// allocation, staging/execution/gate/landing steps, gate plan and verdict, refusals,
// conflicts/obstacle holds, landings, and retries. Nothing here writes.

const (
	scheduleAllocation = "allocation"
	scheduleGate       = "gate"
	scheduleRefusal    = "refusal"
	scheduleLanding    = "landing"
	scheduleRetry      = "retry"
	scheduleOther      = "other"
)

// scheduleEntry is one scheduling decision the drain recorded about the run.
// AtMS is nil when no clock was recorded on the row, so a missing clock renders as
// "no clock" rather than 1970-01-01.
type scheduleEntry struct {
	AtMS           *int64            `json:"at_ms"`
	Seq            *int64            `json:"seq,omitempty"`
	Category       string            `json:"category"`
	Kind           string            `json:"kind"`
	Summary        string            `json:"summary"`
	Source         string            `json:"source"`
	Links          map[string]string `json:"links,omitempty"`
	EpistemicState string            `json:"epistemic_state"`
}

func msPtr(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	return &v
}

func clockState(at *int64) string {
	if at == nil {
		return stateNoneClock
	}
	return stateObserved
}

// scheduleNoise are events about a run that record the agent's own progress or token tally
// rather than a scheduling decision of the drain; the transcript and usage figures carry them.
var scheduleNoise = map[string]bool{
	"run.progress": true, "run.usage": true, "run.usage.copied": true,
	"run.observation-error": true, "run.child-session": true, "run.children-recorded": true,
}

func scheduleCategory(kind string) string {
	switch {
	case strings.HasPrefix(kind, "drain.refusal"), strings.HasPrefix(kind, "item.conflict"),
		strings.HasPrefix(kind, "drain.obstacle"):
		return scheduleRefusal
	case strings.HasPrefix(kind, "gate."), strings.HasPrefix(kind, "boundary."):
		return scheduleGate
	case strings.HasPrefix(kind, "land."), strings.HasPrefix(kind, "drain.revert"):
		return scheduleLanding
	case strings.Contains(kind, "retry"), strings.Contains(kind, "supersed"):
		return scheduleRetry
	case strings.HasPrefix(kind, "run."), strings.HasPrefix(kind, "drain.started"),
		strings.HasPrefix(kind, "drain.node-advanced"), strings.HasPrefix(kind, "drain.dispatch"),
		strings.HasPrefix(kind, "drain.decision"):
		return scheduleAllocation
	}
	return scheduleOther
}

func stepScheduleCategory(step string) string {
	switch step {
	case "gate", "fixer-gate", "boundary", "fence":
		return scheduleGate
	case "land":
		return scheduleLanding
	case "stage", "run", "graph":
		return scheduleAllocation
	case "retry":
		return scheduleRetry
	default:
		return scheduleOther
	}
}

func gatePayloadDetails(kind string, payload map[string]any, gateWaitMS int64, spanFailed []string) []string {
	body := payload
	if inner, ok := payload["payload"].(map[string]any); ok {
		body = inner
	}
	var extra []string
	if checks, ok := body["checks"].([]any); ok {
		extra = append(extra, fmt.Sprintf("checks: %d", len(checks)))
		if kind == "gate.verdict" {
			var failed []string
			for _, item := range checks {
				cm, ok := item.(map[string]any)
				if !ok {
					continue
				}
				res := strings.ToLower(fmt.Sprint(cm["result"]))
				status := strings.ToLower(fmt.Sprint(cm["status"]))
				if res == "fail" || res == "stalled" || status == "fail" || status == "err" || status == "stalled" {
					if id := fmt.Sprint(cm["check_id"]); id != "" && id != "<nil>" {
						failed = append(failed, id)
					}
				}
			}
			if len(failed) > 0 {
				extra = append(extra, "failed: "+strings.Join(failed, ", "))
			}
		}
	} else if kind == "gate.verdict" && len(spanFailed) > 0 {
		extra = append(extra, "failed: "+strings.Join(spanFailed, ", "))
	}
	if kind == "gate.verdict" && gateWaitMS > 0 {
		extra = append(extra, fmt.Sprintf("gate_slot_wait_ms: %d", gateWaitMS))
	}
	return extra
}

func scheduleSummary(kind, runID, objectType, objectID string, payload map[string]any, gateWaitMS int64, spanFailed []string) string {
	parts := []string{kind}
	if objectType == "run" && objectID != "" && objectID != runID {
		parts = append(parts, "by_run: "+objectID)
	}
	for _, key := range []string{"node_id", "node", "step", "action", "result", "verdict", "outcome", "status", "waiting_on", "acknowledge", "reason", "detail", "text", "seat", "backend", "lane_id"} {
		if v, ok := payload[key]; ok {
			text := fmt.Sprint(v)
			if text != "" && text != "<nil>" {
				if len(text) > 200 {
					text = text[:200] + "…"
				}
				parts = append(parts, key+": "+text)
			}
		}
	}
	if d, ok := payload["decision"].(map[string]any); ok {
		for _, key := range []string{"seat", "action", "reason"} {
			if v, ok := d[key]; ok && fmt.Sprint(v) != "" && fmt.Sprint(v) != "<nil>" {
				parts = append(parts, key+": "+fmt.Sprint(v))
			}
		}
	}
	if kind == "gate.plan" || kind == "gate.verdict" {
		parts = append(parts, gatePayloadDetails(kind, payload, gateWaitMS, spanFailed)...)
	}
	return strings.Join(parts, " · ")
}

func scheduleLinks(runID, objectType, objectID string, payload map[string]any) map[string]string {
	links := map[string]string{}
	if objectType == "run" && objectID != "" && objectID != runID {
		links["evaluating_run_id"] = objectID
	}
	for _, key := range []string{"item_id", "lane_id", "pass_id", "verdict_id", "landing_id"} {
		if v, ok := payload[key].(string); ok && v != "" {
			links[key] = v
		}
	}
	for _, key := range []string{"fix_run_id", "related_run_id", "supersedes_run_id", "next_run_id", "parent_run_id", "writer_run_id", "design_run_id"} {
		if v, ok := payload[key].(string); ok && v != "" && v != runID {
			links[key] = v
		}
	}
	if ws, ok := payload["worker_session"].(map[string]any); ok {
		if v, ok := ws["run_id"].(string); ok && v != "" && v != runID {
			links["worker_run_id"] = v
		}
	}
	for _, key := range []string{"base_sha", "landed_sha", "red_sha", "trunk_after"} {
		if v, ok := payload[key].(string); ok && v != "" {
			links[key] = v
		}
	}
	if len(links) == 0 {
		return nil
	}
	return links
}

func summarizeGateSpanWaits(r *store.Reader, runID string) (int64, []string) {
	spans, err := readGateSpans(r, runID)
	if err != nil {
		return 0, nil
	}
	var totalWait float64
	var failed []string
	seenFail := map[string]bool{}
	for _, sp := range spans {
		st := strings.ToLower(sp.Status)
		if (st == "fail" || st == "err" || st == "stalled") && sp.CheckID != "" && !seenFail[sp.CheckID] {
			seenFail[sp.CheckID] = true
			failed = append(failed, sp.CheckID)
		}
		if sp.WaitedMSJSON == "" || sp.WaitedMSJSON == "{}" {
			continue
		}
		var waits map[string]float64
		if json.Unmarshal([]byte(sp.WaitedMSJSON), &waits) == nil {
			for _, w := range waits {
				if w > 0 {
					totalWait += w
				}
			}
		}
	}
	return int64(totalWait + 0.5), failed
}

// readRunSchedule answers every scheduling decision the drain recorded about one run,
// oldest first. Missing optional tables or columns yield a shorter list rather than a 500.
func readRunSchedule(r *store.Reader, run storeRun, loopDir string) ([]scheduleEntry, error) {
	runID := run.RunID
	out := []scheduleEntry{}
	seenEventSeqs := map[int64]bool{}
	gatePlanSeen := false
	gateVerdictSeen := false
	gateWaitMS, spanFailed := summarizeGateSpanWaits(r, runID)

	rows, err := r.Query(`SELECT seq, ts, kind, COALESCE(object_type, ''), COALESCE(object_id, ''), payload FROM events
 WHERE object_id = ?
    OR (json_valid(payload) AND (
          json_extract(payload, '$.run_id') = ?
       OR json_extract(payload, '$.worker_session.run_id') = ?
       OR json_extract(payload, '$.writer_run_id') = ?
       OR json_extract(payload, '$.parent_run_id') = ?
       OR json_extract(payload, '$.supersedes_run_id') = ?
       OR json_extract(payload, '$.next_run_id') = ?
       OR json_extract(payload, '$.fix_run_id') = ?
       OR json_extract(payload, '$.design_run_id') = ?
       OR json_extract(payload, '$.decision.run_request.run_id') = ?
    ))
 ORDER BY seq`, runID, runID, runID, runID, runID, runID, runID, runID, runID, runID)
	if isMissingColumn(err) {
		rows, err = r.Query(`SELECT seq, ts, kind, '', COALESCE(object_id, ''), payload FROM events
 WHERE object_id = ? OR (json_valid(payload) AND json_extract(payload, '$.run_id') = ?) ORDER BY seq`, runID, runID)
	}
	if err != nil && !isMissingTable(err) && !isMissingColumn(err) {
		return nil, err
	}
	if err == nil {
		for rows.Next() {
			var seq, ts int64
			var kind, objType, object string
			var raw []byte
			if err := rows.Scan(&seq, &ts, &kind, &objType, &object, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			seenEventSeqs[seq] = true
			if scheduleNoise[kind] {
				continue
			}
			if kind == "gate.plan" {
				gatePlanSeen = true
			}
			if kind == "gate.verdict" {
				gateVerdictSeen = true
			}
			payload := map[string]any{}
			_ = json.Unmarshal(raw, &payload)
			s := seq
			at := msPtr(ts)
			out = append(out, scheduleEntry{
				AtMS: at, Seq: &s, Category: scheduleCategory(kind), Kind: kind,
				Summary:        scheduleSummary(kind, runID, objType, object, payload, gateWaitMS, spanFailed),
				Source:         "events",
				Links:          scheduleLinks(runID, objType, object, payload),
				EpistemicState: clockState(at),
			})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}

	drows, err := r.Query(`SELECT decision_id, item_id, COALESCE(lane_id, ''), action, reason, dispatched_at,
 COALESCE(pass_id, '') FROM dispatches WHERE run_id = ? ORDER BY dispatched_at, rowid`, runID)
	if isMissingColumn(err) {
		drows, err = r.Query(`SELECT decision_id, item_id, COALESCE(lane_id, ''), action, reason, dispatched_at,
 '' FROM dispatches WHERE run_id = ? ORDER BY dispatched_at`, runID)
	}
	if err != nil && !isMissingTable(err) && !isMissingColumn(err) {
		return nil, err
	}
	if err == nil {
		for drows.Next() {
			var id, item, lane, action, reason, pass string
			var at int64
			if err := drows.Scan(&id, &item, &lane, &action, &reason, &at, &pass); err != nil {
				drows.Close()
				return nil, err
			}
			links := map[string]string{"item_id": item}
			if lane != "" {
				links["lane_id"] = lane
			}
			if pass != "" {
				links["pass_id"] = pass
			}
			ts := msPtr(at)
			out = append(out, scheduleEntry{
				AtMS: ts, Category: scheduleAllocation, Kind: "dispatch",
				Summary:        fmt.Sprintf("dispatch decision %s · action: %s · reason: %s", id, action, reason),
				Source:         "dispatches",
				Links:          links,
				EpistemicState: clockState(ts),
			})
		}
		err = drows.Err()
		drows.Close()
		if err != nil {
			return nil, err
		}
	}

	retrySchedule, err := readRetrySchedule(r, run, seenEventSeqs)
	if err != nil {
		return nil, err
	}
	out = append(out, retrySchedule...)

	landingSchedule, err := readLandingSchedule(r, runID, seenEventSeqs)
	if err != nil {
		return nil, err
	}
	out = append(out, landingSchedule...)

	if loopDir != "" {
		runLoopDir := filepath.Join(loopDir, runID)
		stepsEntries, seenSteps := readStepSchedule(filepath.Join(runLoopDir, "steps.jsonl"))
		out = append(out, stepsEntries...)
		out = append(out, readPipelineSchedule(filepath.Join(runLoopDir, "pipeline.jsonl"), seenSteps)...)
		if !gatePlanSeen {
			if entry, ok := readGateFileSchedule(filepath.Join(runLoopDir, "gate.plan.json"), "gate.plan", gateWaitMS, spanFailed); ok {
				out = append(out, entry)
			}
		}
		if !gateVerdictSeen {
			if entry, ok := readGateFileSchedule(filepath.Join(runLoopDir, "gate.verdict.json"), "gate.verdict", gateWaitMS, spanFailed); ok {
				out = append(out, entry)
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i].AtMS, out[j].AtMS
		if ai == nil || aj == nil {
			if ai == nil && aj == nil {
				if out[i].Seq != nil && out[j].Seq != nil {
					return *out[i].Seq < *out[j].Seq
				}
				return false
			}
			return ai != nil
		}
		if *ai != *aj {
			return *ai < *aj
		}
		if out[i].Seq != nil && out[j].Seq != nil {
			return *out[i].Seq < *out[j].Seq
		}
		return false
	})
	return out, nil
}

func firstSubsequentRun(r *store.Reader, itemID, supersededRunID string, requestedAt int64) string {
	if itemID == "" {
		return ""
	}
	rows, err := r.Query(`SELECT run_id FROM runs WHERE item_id = ? AND run_id != ? AND started_at >= ? ORDER BY started_at, rowid LIMIT 1`,
		itemID, supersededRunID, requestedAt)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var next string
	if rows.Next() {
		_ = rows.Scan(&next)
	}
	return next
}

func readRetrySchedule(r *store.Reader, run storeRun, seenEventSeqs map[int64]bool) ([]scheduleEntry, error) {
	var out []scheduleEntry
	type retryRow struct {
		id, itemID, supersedesRunID, requestedBy, reason string
		requestedAt                                      int64
	}
	var retries []retryRow
	q := `SELECT retry_id, item_id, supersedes_run_id, COALESCE(requested_by, ''), COALESCE(reason, ''), requested_at
 FROM retries WHERE supersedes_run_id = ?`
	args := []any{run.RunID}
	if run.ItemID != "" {
		q += ` OR item_id = ?`
		args = append(args, run.ItemID)
	}
	q += ` ORDER BY requested_at, retry_id`
	rows, err := r.Query(q, args...)
	if isMissingTable(err) || isMissingColumn(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rr retryRow
		if err := rows.Scan(&rr.id, &rr.itemID, &rr.supersedesRunID, &rr.requestedBy, &rr.reason, &rr.requestedAt); err != nil {
			rows.Close()
			return nil, err
		}
		retries = append(retries, rr)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	for _, rr := range retries {
		nextRun := firstSubsequentRun(r, rr.itemID, rr.supersedesRunID, rr.requestedAt)
		switch {
		case rr.supersedesRunID == run.RunID:
			links := map[string]string{"item_id": rr.itemID}
			if nextRun != "" && nextRun != run.RunID {
				links["next_run_id"] = nextRun
			}
			at := msPtr(rr.requestedAt)
			out = append(out, scheduleEntry{
				AtMS: at, Category: scheduleRetry, Kind: "retry",
				Summary:        fmt.Sprintf("retry %s · by: %s · reason: %s", rr.id, rr.requestedBy, rr.reason),
				Source:         "retries",
				Links:          links,
				EpistemicState: clockState(at),
			})
			ackEntries, err := readRetryEvents(r, rr.id, run.RunID, seenEventSeqs)
			if err != nil {
				return nil, err
			}
			out = append(out, ackEntries...)
		case nextRun == run.RunID:
			links := map[string]string{"item_id": rr.itemID}
			if rr.supersedesRunID != "" {
				links["supersedes_run_id"] = rr.supersedesRunID
			}
			at := msPtr(rr.requestedAt)
			out = append(out, scheduleEntry{
				AtMS: at, Category: scheduleRetry, Kind: "retry.authorized",
				Summary:        fmt.Sprintf("authorized by retry %s superseding %s · by: %s · reason: %s", rr.id, rr.supersedesRunID, rr.requestedBy, rr.reason),
				Source:         "retries",
				Links:          links,
				EpistemicState: clockState(at),
			})
		}
	}
	return out, nil
}

func readRetryEvents(r *store.Reader, retryID, runID string, seenEventSeqs map[int64]bool) ([]scheduleEntry, error) {
	rows, err := r.Query(`SELECT seq, ts, kind, payload FROM events WHERE object_type = 'retry' AND object_id = ? ORDER BY seq`, retryID)
	if isMissingTable(err) || isMissingColumn(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []scheduleEntry
	for rows.Next() {
		var seq, ts int64
		var kind string
		var raw []byte
		if err := rows.Scan(&seq, &ts, &kind, &raw); err != nil {
			return nil, err
		}
		if seenEventSeqs[seq] {
			continue
		}
		seenEventSeqs[seq] = true
		payload := map[string]any{}
		_ = json.Unmarshal(raw, &payload)
		s := seq
		at := msPtr(ts)
		out = append(out, scheduleEntry{
			AtMS: at, Seq: &s, Category: scheduleRetry, Kind: kind,
			Summary:        scheduleSummary(kind, runID, "retry", retryID, payload, 0, nil),
			Source:         "events",
			Links:          scheduleLinks(runID, "retry", retryID, payload),
			EpistemicState: clockState(at),
		})
	}
	return out, rows.Err()
}

func readLandingSchedule(r *store.Reader, runID string, seenEventSeqs map[int64]bool) ([]scheduleEntry, error) {
	rows, err := r.Query(`SELECT landing_id, item_id, COALESCE(verdict_id, ''), result, COALESCE(reason, ''), COALESCE(trunk_after, ''), COALESCE(event_seq, 0)
 FROM landings WHERE run_id = ? ORDER BY event_seq, landing_id`, runID)
	if isMissingTable(err) || isMissingColumn(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type landingRow struct {
		id, itemID, verdictID, result, reason, trunkAfter string
		eventSeq                                          int64
	}
	var lrows []landingRow
	for rows.Next() {
		var lr landingRow
		if err := rows.Scan(&lr.id, &lr.itemID, &lr.verdictID, &lr.result, &lr.reason, &lr.trunkAfter, &lr.eventSeq); err != nil {
			rows.Close()
			return nil, err
		}
		lrows = append(lrows, lr)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	var out []scheduleEntry
	for _, lr := range lrows {
		if lr.eventSeq > 0 && seenEventSeqs[lr.eventSeq] {
			continue
		}
		var ts int64
		if lr.eventSeq > 0 {
			evRows, evErr := r.Query(`SELECT ts FROM events WHERE seq = ?`, lr.eventSeq)
			if evErr == nil {
				if evRows.Next() {
					_ = evRows.Scan(&ts)
				}
				evRows.Close()
			}
		}
		at := msPtr(ts)
		var seqPtr *int64
		if lr.eventSeq > 0 {
			s := lr.eventSeq
			seqPtr = &s
		}
		parts := []string{"landing " + lr.id, "result: " + lr.result}
		if lr.reason != "" {
			parts = append(parts, "reason: "+lr.reason)
		}
		links := map[string]string{"landing_id": lr.id}
		if lr.itemID != "" {
			links["item_id"] = lr.itemID
		}
		if lr.verdictID != "" {
			links["verdict_id"] = lr.verdictID
		}
		if lr.trunkAfter != "" {
			links["landed_sha"] = lr.trunkAfter
		}
		out = append(out, scheduleEntry{
			AtMS: at, Seq: seqPtr, Category: scheduleLanding, Kind: "landing",
			Summary:        strings.Join(parts, " · "),
			Source:         "landings",
			Links:          links,
			EpistemicState: clockState(at),
		})
	}
	return out, nil
}

// readStepSchedule turns `<loop-dir>/<run-id>/steps.jsonl` into start and end entries per step,
// returning which (step, bound) pairs were recorded so `pipeline.jsonl` only fills gaps.
func readStepSchedule(path string) ([]scheduleEntry, map[string]bool) {
	seen := map[string]bool{}
	stream, err := openJSONStream(path, 0)
	if err != nil {
		return nil, seen
	}
	defer stream.close()
	var out []scheduleEntry
	for {
		raw, _, err := stream.next()
		if err != nil {
			return out, seen
		}
		var step stepLine
		if json.Unmarshal(raw, &step) != nil || step.Step == "" {
			continue
		}
		category := stepScheduleCategory(step.Step)
		if step.StartedAtMS > 0 {
			seen[step.Step+":start"] = true
			at := msPtr(step.StartedAtMS)
			out = append(out, scheduleEntry{
				AtMS: at, Category: category, Kind: "step.start",
				Summary: "step " + step.Step + " started", Source: "steps", EpistemicState: stateObserved,
			})
		}
		if step.FinishedAtMS > 0 {
			seen[step.Step+":end"] = true
			summary := "step " + step.Step + " finished"
			if step.ExitCode != nil {
				summary += fmt.Sprintf(" · exit %d", *step.ExitCode)
			}
			at := msPtr(step.FinishedAtMS)
			out = append(out, scheduleEntry{
				AtMS: at, Category: category, Kind: "step.end",
				Summary: summary, Source: "steps", EpistemicState: stateObserved,
			})
		}
	}
}

// readPipelineSchedule reads `<loop-dir>/<run-id>/pipeline.jsonl`, which writes a step's
// `started_at_ms` before the step exits (so an in-progress gate appears immediately).
func readPipelineSchedule(path string, seenSteps map[string]bool) []scheduleEntry {
	stream, err := openJSONStream(path, 0)
	if err != nil {
		return nil
	}
	defer stream.close()
	type pipeState struct {
		step    string
		started int64
		ended   *int64
	}
	var order []string
	byStep := map[string]*pipeState{}
	for {
		raw, _, err := stream.next()
		if err != nil {
			break
		}
		var row struct {
			Step         string `json:"step"`
			StartedAtMS  int64  `json:"started_at_ms"`
			FinishedAtMS *int64 `json:"finished_at_ms"`
		}
		if json.Unmarshal(raw, &row) != nil || row.Step == "" {
			continue
		}
		st, exists := byStep[row.Step]
		if !exists {
			st = &pipeState{step: row.Step}
			byStep[row.Step] = st
			order = append(order, row.Step)
		}
		if row.StartedAtMS > 0 {
			st.started = row.StartedAtMS
		}
		if row.FinishedAtMS != nil && *row.FinishedAtMS > 0 {
			end := *row.FinishedAtMS
			st.ended = &end
		}
	}
	var out []scheduleEntry
	for _, name := range order {
		st := byStep[name]
		category := stepScheduleCategory(name)
		if st.started > 0 && !seenSteps[name+":start"] {
			summary := "step " + name + " started"
			if st.ended == nil {
				summary += " (in progress)"
			}
			at := msPtr(st.started)
			out = append(out, scheduleEntry{
				AtMS: at, Category: category, Kind: "step.start",
				Summary: summary, Source: "pipeline", EpistemicState: stateObserved,
			})
		}
		if st.ended != nil && !seenSteps[name+":end"] {
			at := msPtr(*st.ended)
			out = append(out, scheduleEntry{
				AtMS: at, Category: category, Kind: "step.end",
				Summary: "step " + name + " finished", Source: "pipeline", EpistemicState: stateObserved,
			})
		}
	}
	return out
}

func readGateFileSchedule(path, kind string, gateWaitMS int64, spanFailed []string) (scheduleEntry, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return scheduleEntry{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return scheduleEntry{}, false
	}
	payload := map[string]any{}
	if json.Unmarshal(raw, &payload) != nil {
		return scheduleEntry{}, false
	}
	body := payload
	if inner, ok := payload["payload"].(map[string]any); ok {
		body = inner
	}
	state := stateDerivedClock
	atMS := info.ModTime().UnixMilli()
	if created, ok := payload["created_at"].(string); ok {
		if parsed, ok := parseISOClock(created); ok {
			atMS = parsed
			state = stateObserved
		}
	}
	at := msPtr(atMS)
	return scheduleEntry{
		AtMS:           at,
		Category:       scheduleGate,
		Kind:           kind,
		Summary:        scheduleSummary(kind, "", "", "", body, gateWaitMS, spanFailed),
		Source:         filepath.Base(path),
		Links:          scheduleLinks("", "", "", body),
		EpistemicState: state,
	}, true
}
