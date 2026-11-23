package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// /why shows the loop's last recorded pass instead of planning again. The planner folds the
// store's whole event log (10 to 50 seconds on the live store), and the loop writes every few
// seconds, so a recomputed answer was nearly never cached. The loop already records each
// decision it makes in `dispatches` (decision_id, action, reason, lane, run, seat, pass_id),
// and each pass in `drain_passes`; this reads both with indexed or single-scan queries.
//
// A recorded pass applies the decisions it acted on, not one per item, so an item's row is
// its newest recorded decision, and the pass named in the answer is the newest pass: the
// answer is "what the loop last said", with the pass's age beside it. An item the loop has
// not decided yet says so.

// whyPassDecision is the `decision` object of a row read from the record.
type whyPassDecision struct {
	DecisionID string `json:"decision_id"`
	Action     string `json:"action"`
	Reason     string `json:"reason"`
	Position   string `json:"position"`
	Seat       string `json:"seat,omitempty"`
	PassID     string `json:"pass_id,omitempty"`
	LaneID     string `json:"lane_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
}

type whyPassRow struct {
	waitingRow
	State     string           `json:"state"`
	Reason    string           `json:"reason"`
	WaitingOn []string         `json:"waiting_on"`
	Decision  *whyPassDecision `json:"decision,omitempty"`
}

// whySource says where the answer came from, so a reader can tell a recorded pass from a
// fresh plan and why the plan was used.
type whySource struct {
	Kind     string `json:"kind"` // "pass" or "planner"
	PassID   string `json:"pass_id,omitempty"`
	PassAtMS int64  `json:"pass_at_ms,omitempty"`
	AgeS     int64  `json:"age_s"`
	Fallback string `json:"fallback,omitempty"`
	Text     string `json:"text"`
}

// passFallback is why the recorded pass was not used.
type passFallback struct{ reason string }

func (f passFallback) Error() string { return f.reason }

// whyFromPass builds the /why answer from the last recorded pass. A passFallback error means
// the caller should plan instead.
func (s *server) whyFromPass(ctx context.Context, now time.Time) (json.RawMessage, int64, error) {
	scope, err := designRoadmapScope(s.root)
	if err != nil {
		return nil, 0, err
	}
	var doc map[string]any
	var head int64
	var passAt int64
	var passID string
	err = s.store.ReadTx(ctx, func(r *store.Reader) error {
		step := time.Now()
		mark := func(name string) { phase(ctx, name, map[string]any{"ms": msSince(time.Now, step)}); step = time.Now() }
		session, live, note, err := readLoopSession(r, now)
		mark("pass_session")
		if err != nil {
			return err
		}
		if !live {
			return passFallback{"no loop is running (" + note + ")"}
		}
		lanes := session.Lanes
		if lanes == nil {
			lanes = []string{}
		}
		err = r.QueryRow(`SELECT tick_id, started_at FROM drain_passes ORDER BY started_at DESC LIMIT 1`).Scan(&passID, &passAt)
		if errors.Is(err, sql.ErrNoRows) || isMissingTable(err) {
			return passFallback{"no pass is recorded"}
		}
		if err != nil {
			return err
		}
		if head, err = r.EventHeadSeq(); err != nil {
			return err
		}
		items, err := readItemsBase(r)
		if err != nil {
			return err
		}
		mark("pass_items")
		var open []storeItem
		for _, it := range items {
			if !waitingTerminal(it.State) {
				open = append(open, it)
			}
		}
		places := itemPlaces(open)
		started := time.Now()
		latest, err := latestDecisions(r)
		if err != nil {
			return err
		}
		phase(ctx, "pass_decisions", map[string]any{"ms": msSince(time.Now, started), "items": len(open)})
		rows := make([]whyPassRow, 0, len(open))
		for _, it := range open {
			rows = append(rows, passRow4(it, places[it.ItemID], latest[it.ItemID]))
		}
		sortPassRows(rows)
		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		active, err := readAlarms(r, s.root, s.policyRef, now)
		if err != nil {
			return err
		}
		mark("pass_alarms")
		designs, err := waitingDesignWithoutItems(r, scope)
		if err != nil {
			return err
		}
		started = time.Now()
		trunk, line := whyTrunkStatus(ctx, s.root, s.storePath, declared.Name, now)
		phase(ctx, "pass_trunk", map[string]any{"ms": msSince(time.Now, started)})
		doc = map[string]any{
			"alarms": active, "definition": declared.Name, "event_head_seq": head,
			"lanes": lanes, "lanes_source": "live loop", "rows": rows,
			"summary": passSummary(rows, len(lanes), active), "designs_waiting": nonNil(designs),
		}
		if trunk != nil {
			doc["trunk"], doc["trunk_line"] = trunk, line
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	age := now.UnixMilli() - passAt
	if age < 0 {
		age = 0
	}
	doc["source"] = whySource{Kind: "pass", PassID: passID, PassAtMS: passAt, AgeS: age / 1000,
		Text: fmt.Sprintf("as of pass %s, %d s ago", passID, age/1000)}
	raw, err := json.Marshal(doc)
	return raw, head, err
}

func nonNil(rows []waitingRow) []waitingRow {
	if rows == nil {
		return []waitingRow{}
	}
	return rows
}

// latestDecisions is each open item's newest recorded decision, in one scan of dispatches.
// Audit rows the item page also skips (identity holds) are left out.
func latestDecisions(r *store.Reader) (map[string]whyPassDecisionRow, error) {
	// A store older than a column reads it as empty, as the item page does.
	have := map[string]bool{}
	info, err := r.Query(`SELECT name FROM pragma_table_info('dispatches')`)
	if err != nil {
		return nil, err
	}
	for info.Next() {
		var name string
		if err := info.Scan(&name); err != nil {
			info.Close()
			return nil, err
		}
		have[name] = true
	}
	info.Close()
	if len(have) == 0 {
		return nil, nil
	}
	column := func(name string) string {
		if have[name] {
			return "COALESCE(" + name + ",'')"
		}
		return "''"
	}
	rows, err := r.Query(`SELECT item_id, decision_id, action, reason, dispatched_at, ` + column("lane_id") + `, ` + column("run_id") + `,
 ` + column("seat") + `, ` + column("pass_id") + `, ` + column("deferred_by") + ` FROM (
  SELECT d.*, ROW_NUMBER() OVER (PARTITION BY d.item_id ORDER BY d.dispatched_at DESC, d.decision_id DESC) AS n
  FROM dispatches d WHERE d.item_id IN (SELECT item_id FROM items WHERE state NOT IN ('landed','closed','resolved-elsewhere'))
   AND d.reason <> 'judge-grant-absent'
   AND NOT (d.action = 'hold' AND (d.reason = 'trunk-foreign-identity' OR d.reason GLOB 'trunk-foreign-identity: *'))
 ) WHERE n = 1`)
	if isMissingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]whyPassDecisionRow{}
	for rows.Next() {
		var id string
		var d whyPassDecisionRow
		if err := rows.Scan(&id, &d.DecisionID, &d.Action, &d.Reason, &d.AtMS, &d.LaneID, &d.RunID, &d.Seat, &d.PassID, &d.DeferredBy); err != nil {
			return nil, err
		}
		out[id] = d
	}
	return out, rows.Err()
}

type whyPassDecisionRow struct {
	DecisionID, Action, Reason, LaneID, RunID, Seat, PassID, DeferredBy string
	AtMS                                                                int64
}

func passRow4(it storeItem, place int, d whyPassDecisionRow) whyPassRow {
	row := whyPassRow{State: it.State, WaitingOn: []string{}}
	row.Place, row.ItemID, row.ClassSource = place, it.ItemID, "pass"
	if d.DecisionID == "" {
		row.Class, row.Where, row.Why, row.Mover, row.Since = "waits on the loop", "queue", "not yet planned", "the next pass", "no pass yet"
		row.Class, row.ClassSource = "waits on the loop", "rule"
		row.Reason = row.Why
		return row
	}
	position := "held"
	if d.Action == "hold" {
		rule := waitingRuleFor(d.Reason)
		row.Class, row.Where, row.Mover = rule.class, rule.where, rule.mover
		if d.DeferredBy != "" {
			row.Mover = d.DeferredBy
			row.WaitingOn = append(row.WaitingOn, d.DeferredBy)
		}
		if row.Class == "moving" {
			position = "running"
		}
	} else {
		position = "running"
		row.Class, row.Where, row.Mover = "ready", "queue", "the next pass"
		if d.RunID != "" {
			row.Class, row.Where, row.Mover = "moving", "lane", d.RunID
		}
	}
	row.Position, row.Reason, row.Why = position, d.Reason, d.Reason
	row.LaneID, row.RunID = d.LaneID, d.RunID
	row.Since = time.UnixMilli(d.AtMS).UTC().Format(time.RFC3339Nano)
	row.Decision = &whyPassDecision{DecisionID: d.DecisionID, Action: d.Action, Reason: d.Reason, Position: position,
		Seat: d.Seat, PassID: d.PassID, LaneID: d.LaneID, RunID: d.RunID}
	return row
}

func sortPassRows(rows []whyPassRow) {
	less := func(a, b whyPassRow) bool {
		if (a.Place == 0) != (b.Place == 0) {
			return a.Place != 0
		}
		if a.Place != b.Place {
			return a.Place < b.Place
		}
		return a.ItemID < b.ItemID
	}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && less(rows[j], rows[j-1]); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

// passSummary tallies the rows the way the planner's summary partitions them. Lane
// occupancy beyond the offered count is the planner's to know; it is not guessed here.
func passSummary(rows []whyPassRow, offered int, active []alarms.Alarm) map[string]any {
	held := map[string]int{}
	running, ready, owner, drainNext, design, operator := 0, 0, 0, 0, 0, 0
	busy := map[string]bool{}
	for _, row := range rows {
		switch {
		case row.Class == "moving":
			running++
			if row.LaneID != "" {
				busy[row.LaneID] = true
			}
			continue
		case row.Class == "ready":
			ready++
			continue
		}
		held[row.Reason]++
		switch {
		case strings.HasPrefix(row.Mover, "owner"):
			owner++
		case strings.Contains(row.Mover, "design seat"):
			design++
		case strings.HasPrefix(row.Mover, "operator;") || row.Class == "needs you" || row.Class == "needs a cause":
			operator++
		default:
			drainNext++
		}
	}
	return map[string]any{"alarms": active, "running": running, "could_run": ready, "held": held,
		"owner_waiting": owner, "drain_next": drainNext, "design_next": design, "operator_next": operator,
		"lanes_offered": offered, "lanes_busy": len(busy)}
}

// whyTrunkStatus answers the trunk's proof when the trunk is unproved, as the planner face does.
func whyTrunkStatus(ctx context.Context, root, storePath, def string, now time.Time) (*heavy.HeavyStatus, string) {
	if def != definition.GitLandingName {
		return nil, ""
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return nil, ""
	}
	status, err := heavy.ReadStatus(ctx, root, storePath)
	if err != nil || status.GreenCommit == "" && status.Result == "unjudged" {
		return nil, ""
	}
	line := heavy.UnprovedLine(status, now)
	if line == "" {
		return nil, ""
	}
	return &status, line
}

// withWhySource adds the source note to a planner answer and reshapes it to the slim form:
// closed work is dropped and design documents move to `designs_waiting`.
func withWhySource(raw json.RawMessage, fallback string) (json.RawMessage, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(document["rows"], &rows); err != nil {
		return nil, err
	}
	kept, designs := []json.RawMessage{}, []json.RawMessage{}
	for _, row := range rows {
		var probe struct {
			Class    string `json:"class"`
			Document string `json:"document"`
		}
		if err := json.Unmarshal(row, &probe); err != nil {
			return nil, err
		}
		switch {
		case probe.Document != "":
			designs = append(designs, row)
		case probe.Class != "done":
			kept = append(kept, row)
		}
	}
	document["rows"], _ = json.Marshal(kept)
	document["designs_waiting"], _ = json.Marshal(designs)
	source, _ := json.Marshal(whySource{Kind: "planner", Fallback: fallback,
		Text: "planned just now: " + fallback})
	document["source"] = source
	return json.Marshal(document)
}
