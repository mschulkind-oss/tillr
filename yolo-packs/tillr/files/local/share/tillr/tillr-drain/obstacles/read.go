// Package obstacles reads recorded obstacle holds without registering writer schemas.
package obstacles

import (
	"encoding/json"
	"strings"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	HeldKind    = "drain.obstacle-hold"
	ClearedKind = "drain.obstacle-hold-cleared"
)

// Hold records one run's reported OBSTACLE ending for an item without a document block.
type Hold struct {
	ItemID       string            `json:"item_id"`
	RunID        string            `json:"run_id"`
	Kind         string            `json:"kind"`
	Fields       map[string]string `json:"fields"`
	Checks       []string          `json:"checks,omitempty"`
	BaseSHA      string            `json:"base_sha,omitempty"`
	HeadSHA      string            `json:"head_sha,omitempty"`
	AllowedPaths []string          `json:"allowed_paths,omitempty"`
	Reason       string            `json:"reason"`
	Mover        string            `json:"mover"`
	Action       string            `json:"action"`
	AtMS         int64             `json:"-"`
	Seq          int64             `json:"-"`
}

// ClearPayload clears one recorded obstacle hold when its condition resolves.
type ClearPayload struct {
	ItemID string `json:"item_id"`
	RunID  string `json:"run_id,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// NewHold builds a Hold from a parsed obstacle's kind and fields.
func NewHold(itemID, runID, kind string, rawFields map[string]string, checks []string, baseSHA string, allowedPaths []string) Hold {
	fields := make(map[string]string, len(rawFields))
	for k, v := range rawFields {
		fields[strings.ToUpper(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	h := Hold{
		ItemID:       itemID,
		RunID:        runID,
		Kind:         kind,
		Fields:       fields,
		Checks:       append([]string(nil), checks...),
		BaseSHA:      strings.TrimSpace(baseSHA),
		AllowedPaths: append([]string(nil), allowedPaths...),
	}
	switch kind {
	case "owner":
		q := OneLine(fields["QUESTION"])
		h.Mover = "owner"
		h.Reason = q
		if runID != "" {
			h.Action = "answer the question with tillr drain retry --item " + itemID + " --run " + runID + " --by <who> --why <reason>, or close the item (tillr item close --item " + itemID + ")"
		} else {
			h.Action = "answer the question and retry the item, or close the item (tillr item close --item " + itemID + ")"
		}
	case "trunk":
		if len(checks) > 0 {
			checksText := strings.Join(checks, ", ")
			h.Mover = "drain"
			h.Reason = "checks: " + checksText
			h.Action = "returns when checks " + checksText + " are green"
		} else {
			cmdText := OneLine(fields["CHECKS"])
			h.Mover = "operator"
			h.Reason = "command outside gate checks: " + cmdText
			if runID != "" {
				h.Action = "check trunk failure for " + cmdText + " and retry run " + runID + " (tillr drain retry --item " + itemID + " --run " + runID + " --by <who> --why <reason>)"
			} else {
				h.Action = "check trunk failure for " + cmdText + " and retry the item"
			}
		}
	case "tooling":
		cmd := OneLine(fields["COMMAND"])
		ev := FirstLine(fields["EVIDENCE"])
		h.Mover = "operator"
		if cmd != "" && ev != "" {
			h.Reason = cmd + ": " + ev
		} else if ev != "" {
			h.Reason = ev
		} else {
			h.Reason = cmd
		}
		h.Action = ev + "; repair tooling and retry run " + runID
	case "seat":
		tried := FirstLine(fields["TRIED"])
		missing := FirstLine(fields["MISSING"])
		extra := FirstLine(fields["EVIDENCE"])
		ev := "tried: " + tried + "; missing: " + missing
		if extra != "" {
			ev += "; " + extra
		}
		h.Mover = "operator"
		h.Reason = ev
		h.Action = ev + "; change seat or split the request, then retry run " + runID
	default:
		criteria := OneLine(fields["CRITERIA"])
		ev := FirstLine(fields["EVIDENCE"])
		h.Mover = "operator"
		if criteria != "" && ev != "" {
			h.Reason = "criteria " + criteria + ": " + ev
		} else if ev != "" {
			h.Reason = ev
		} else {
			h.Reason = criteria
		}
		h.Action = ev + "; refile or close the item, or retry run " + runID
	}
	return h
}

// ReasonCode answers the planner hold reason for this obstacle kind.
func (h Hold) ReasonCode() string {
	switch h.Kind {
	case "owner", "trunk", "tooling", "seat", "definition":
		return "obstacle-" + h.Kind
	default:
		return "obstacle-definition"
	}
}

// WhyText answers the full why line shown by `tillr drain why` and `tillr view waiting`.
func (h Hold) WhyText() string {
	if h.Reason == "" {
		return h.ReasonCode()
	}
	return h.ReasonCode() + ": " + h.Reason
}

// MoverText answers `<mover>; <action>`.
func (h Hold) MoverText() string {
	action := h.Action
	if action == "" {
		return h.Mover
	}
	return h.Mover + "; " + action
}

// WithoutCloseRemedy returns a copy of h whose action focuses on retrying or
// refiling work when the item already has a landing receipt.
func (h Hold) WithoutCloseRemedy() Hold {
	switch h.Kind {
	case "owner":
		if h.RunID != "" {
			h.Action = "answer the question with tillr drain retry --item " + h.ItemID + " --run " + h.RunID + " --by <who> --why <reason>"
		} else {
			h.Action = "answer the question and retry the item"
		}
	case "trunk", "tooling", "seat":
		h.Action = StripCloseRemedy(h.Action)
	default:
		ev := FirstLine(h.Fields["EVIDENCE"])
		retry := "retry the item"
		if h.RunID != "" {
			retry = "retry run " + h.RunID + " (tillr drain retry --item " + h.ItemID + " --run " + h.RunID + " --by <who> --why <reason>)"
		}
		if ev != "" {
			h.Action = ev + "; refile or " + retry
		} else {
			h.Action = "refile or " + retry
		}
	}
	return h
}

// StripCloseRemedy removes `tillr item close` / "close the item" clauses from
// hold prose for items that already have a landing receipt.
func StripCloseRemedy(s string) string {
	s = stripParenthesizedClose(s, " (or close the item with tillr item close --item ")
	s = stripParenthesizedClose(s, ", or close the item (tillr item close --item ")
	replacer := strings.NewReplacer(
		" Or close the item with `tillr item close`.", "",
		", or close the item", "",
		", or close it", "",
		"refile or close the item, or ", "refile, or ",
		"close the item or ", "",
		", or should the item be closed?", "?",
		", or the item closed?", "?",
		"Should the item be closed (for a trunk fixer, resolved by a heavy pass of its check), its criterion changed, or ", "Should its criterion be changed, or ",
	)
	return replacer.Replace(s)
}

func stripParenthesizedClose(s, prefix string) string {
	for {
		idx := strings.Index(s, prefix)
		if idx < 0 {
			return s
		}
		end := strings.IndexByte(s[idx+len(prefix):], ')')
		if end < 0 {
			return s
		}
		s = s[:idx] + s[idx+len(prefix)+end+1:]
	}
}

func landingsHasItemResultColumns(r *store.Reader) (bool, error) {
	var cols int
	if err := r.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('landings') WHERE name IN ('item_id', 'result')`).Scan(&cols); err != nil {
		return false, err
	}
	return cols == 2, nil
}

// HasLanded reports whether the `landings` table has a `landed` row for itemID.
// When `landings` has not been created yet or lacks `item_id`/`result` columns
// (for example in minimal read-only fixtures), it returns false, nil.
func HasLanded(r *store.Reader, itemID string) (bool, error) {
	if itemID == "" {
		return false, nil
	}
	ok, err := landingsHasItemResultColumns(r)
	if err != nil || !ok {
		return false, err
	}
	var count int
	if err := r.QueryRow(`SELECT COUNT(*) FROM landings WHERE item_id = ? AND result = 'landed'`, itemID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// LandedItems answers the set of item IDs that have at least one `landed` row in `landings`.
// Stores without a complete `landings(item_id, result)` schema answer an empty map and nil error.
func LandedItems(r *store.Reader) (map[string]bool, error) {
	ok, err := landingsHasItemResultColumns(r)
	if err != nil || !ok {
		return nil, err
	}
	rows, err := r.Query(`SELECT DISTINCT item_id FROM landings WHERE result = 'landed'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// LandedItemsFor answers which of itemIDs have a landed row in landings.
func LandedItemsFor(r *store.Reader, itemIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	ok, err := landingsHasItemResultColumns(r)
	if err != nil || !ok {
		return out, err
	}
	const chunk = 400
	for start := 0; start < len(itemIDs); start += chunk {
		end := start + chunk
		if end > len(itemIDs) {
			end = len(itemIDs)
		}
		batch := itemIDs[start:end]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := r.Query(`SELECT DISTINCT item_id FROM landings WHERE result = 'landed' AND item_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// OneLine collapses whitespace runs (including newlines) into single spaces.
func OneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// FirstLine answers the first non-empty trimmed line of s.
func FirstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return OneLine(trimmed)
		}
	}
	return ""
}

// Read folds the obstacle-hold events and answers the currently active holds per item
// together with each item's latest released obstacle-hold run id.
func Read(r *store.Reader) (map[string]Hold, map[string]string, error) {
	events, err := r.EventsOfKind(0,
		HeldKind,
		ClearedKind,
		"item.state",
		"item.reopen",
		"item.close",
		"item.retire",
		"item.resolve",
		"item.lifecycle",
		"item.update",
		"run.allocate",
		"drain.started",
	)
	if err != nil {
		return nil, nil, err
	}
	return foldObstacleEvents(r, events)
}

// ReadForItems folds obstacle-hold events scoped to itemIDs and runIDs via events_by_object.
func ReadForItems(r *store.Reader, itemIDs, runIDs []string) (map[string]Hold, map[string]string, error) {
	if len(itemIDs) == 0 && len(runIDs) == 0 {
		return map[string]Hold{}, map[string]string{}, nil
	}
	objectIDs := make([]string, 0, len(itemIDs)+len(runIDs))
	objectIDs = append(objectIDs, itemIDs...)
	objectIDs = append(objectIDs, runIDs...)
	events, err := r.EventsForObjects(0, objectIDs,
		HeldKind,
		ClearedKind,
		"item.state",
		"item.reopen",
		"item.close",
		"item.retire",
		"item.resolve",
		"item.lifecycle",
		"item.update",
		"run.allocate",
		"drain.started",
	)
	if err != nil {
		return nil, nil, err
	}
	return foldObstacleEvents(r, events)
}

func foldObstacleEvents(r *store.Reader, events []event.Event) (map[string]Hold, map[string]string, error) {
	active := map[string]Hold{}
	released := map[string]string{}
	for _, e := range events {
		switch e.Kind {
		case HeldKind:
			var h Hold
			if err := json.Unmarshal(e.Payload, &h); err != nil {
				return nil, nil, err
			}
			if h.Kind == "fence" {
				delete(active, h.ItemID)
				released[h.ItemID] = h.RunID
				continue
			}
			h.AtMS = e.At.UnixMilli()
			h.Seq = e.Seq
			active[h.ItemID] = h
			delete(released, h.ItemID)
		case ClearedKind:
			var c ClearPayload
			if err := json.Unmarshal(e.Payload, &c); err != nil {
				return nil, nil, err
			}
			if h, ok := active[c.ItemID]; ok && (c.RunID == "" || c.RunID == h.RunID) {
				delete(active, c.ItemID)
				released[c.ItemID] = h.RunID
			}
		case "item.state", "item.reopen":
			if e.ObjectType != "item" || e.ObjectID == nil {
				continue
			}
			id := *e.ObjectID
			h, ok := active[id]
			if !ok || e.Seq <= h.Seq {
				continue
			}
			var p struct {
				State string `json:"state"`
			}
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				continue
			}
			switch p.State {
			case "in-queue":
				delete(active, id)
				released[id] = h.RunID
			case "closed", "resolved-elsewhere", "landed":
				delete(active, id)
			}
		case "item.close", "item.retire", "item.resolve", "item.lifecycle":
			if e.ObjectType == "item" && e.ObjectID != nil {
				delete(active, *e.ObjectID)
			}
		case "item.update":
			if e.ObjectType != "item" || e.ObjectID == nil {
				continue
			}
			id := *e.ObjectID
			h, ok := active[id]
			if !ok || e.Seq <= h.Seq {
				continue
			}
			var p struct {
				State        string   `json:"state"`
				AllowedPaths []string `json:"allowed_paths"`
			}
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				continue
			}
			switch p.State {
			case "closed", "resolved-elsewhere", "landed":
				delete(active, id)
				continue
			case "in-queue":
				if h.Kind == "owner" {
					delete(active, id)
					released[id] = h.RunID
					continue
				}
			}
		case "run.allocate":
			if len(active) == 0 && len(released) == 0 {
				continue
			}
			var p struct {
				ItemID string `json:"item_id"`
				RunID  string `json:"run_id"`
			}
			if err := json.Unmarshal(e.Payload, &p); err != nil || p.ItemID == "" || p.RunID == "" {
				continue
			}
			if h, ok := active[p.ItemID]; ok && e.Seq > h.Seq && p.RunID != h.RunID {
				delete(active, p.ItemID)
			}
			if relRun, ok := released[p.ItemID]; ok && p.RunID != relRun {
				delete(released, p.ItemID)
			}
		case "drain.started":
			if len(active) == 0 && len(released) == 0 {
				continue
			}
			var p struct {
				Decision struct {
					ItemID     string `json:"item_id"`
					RunRequest *struct {
						RunID string `json:"run_id"`
					} `json:"run_request"`
				} `json:"decision"`
			}
			if err := json.Unmarshal(e.Payload, &p); err != nil || p.Decision.ItemID == "" || p.Decision.RunRequest == nil || p.Decision.RunRequest.RunID == "" {
				continue
			}
			itemID, runID := p.Decision.ItemID, p.Decision.RunRequest.RunID
			if h, ok := active[itemID]; ok && e.Seq > h.Seq && runID != h.RunID {
				delete(active, itemID)
			}
			if relRun, ok := released[itemID]; ok && runID != relRun {
				delete(released, itemID)
			}
		}
	}
	if len(active) > 0 {
		activeIDs := make([]string, 0, len(active))
		for id := range active {
			activeIDs = append(activeIDs, id)
		}
		landed, err := LandedItemsFor(r, activeIDs)
		if err != nil {
			return nil, nil, err
		}
		for id, h := range active {
			if landed[id] {
				active[id] = h.WithoutCloseRemedy()
			}
		}
	}
	return active, released, nil
}
