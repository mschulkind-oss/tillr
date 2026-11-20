// Package obstacles reads recorded obstacle holds without registering writer schemas.
package obstacles

import (
	"encoding/json"
	"strings"

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
	case "fence":
		paths := OneLine(fields["PATHS"])
		ev := FirstLine(fields["EVIDENCE"])
		h.Mover = "operator"
		h.Reason = "paths: " + paths
		if ev != "" {
			h.Reason += "; " + ev
		}
		h.Action = "widen allowed_paths (" + paths + ") or split the request, then retry run " + runID
	case "trunk":
		checksText := strings.Join(checks, ", ")
		if checksText == "" {
			checksText = OneLine(fields["CHECKS"])
		}
		h.Mover = "drain"
		h.Reason = "checks: " + checksText
		h.Action = "returns when checks " + checksText + " are green"
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
	case "owner", "fence", "trunk", "tooling", "seat", "definition":
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
	if h.Action == "" {
		return h.Mover
	}
	return h.Mover + "; " + h.Action
}

// WithoutCloseRemedy returns h with any `tillr item close` / `close the item` clause
// replaced by the retry remedy alone. Once an item has a landed receipt in landings,
// `tillr item close` refuses it with already-landed (D-JL4).
func (h Hold) WithoutCloseRemedy() Hold {
	switch h.Kind {
	case "owner":
		if h.RunID != "" {
			h.Action = "answer the question with tillr drain retry --item " + h.ItemID + " --run " + h.RunID + " --by <who> --why <reason>"
		} else {
			h.Action = "answer the question and retry the item"
		}
	case "", "definition":
		ev := FirstLine(h.Fields["EVIDENCE"])
		if h.RunID != "" {
			retryCmd := "retry run " + h.RunID + " with tillr drain retry --item " + h.ItemID + " --run " + h.RunID + " --by <who> --why <reason>"
			if ev != "" {
				h.Action = ev + "; " + retryCmd
			} else {
				h.Action = retryCmd
			}
		} else if ev != "" {
			h.Action = ev + "; retry the item"
		} else {
			h.Action = "retry the item"
		}
	default:
		h.Action = strings.ReplaceAll(h.Action, ", or close the item (tillr item close --item "+h.ItemID+")", "")
		h.Action = strings.ReplaceAll(h.Action, ", or close the item", "")
	}
	return h
}

// StripCloseRemedy removes close-item remedy clauses from hold text or mover text for
// an item that already has a landed receipt in landings and therefore cannot be closed.
func StripCloseRemedy(s, itemID string) string {
	if itemID != "" {
		s = strings.ReplaceAll(s, " (or close the item with tillr item close --item "+itemID+")", "")
		s = strings.ReplaceAll(s, ", or close the item (tillr item close --item "+itemID+")", "")
	}
	s = strings.ReplaceAll(s, ". Or close the item with `tillr item close`.", ".")
	s = strings.ReplaceAll(s, ". Or close the item with tillr item close.", ".")
	s = strings.ReplaceAll(s, ", or should the item be closed?", "?")
	s = strings.ReplaceAll(s, ", or the item closed?", "?")
	s = strings.ReplaceAll(s, "refile or close the item, or ", "")
	s = strings.ReplaceAll(s, "close the item or ", "")
	s = strings.ReplaceAll(s, "close it or ", "")
	s = strings.ReplaceAll(s, ", or close the item", "")
	s = strings.ReplaceAll(s, " or close the item", "")
	s = strings.ReplaceAll(s, ", or close it", "")
	s = strings.ReplaceAll(s, " or close it", "")
	return s
}

// HasLanded reports whether landings records at least one landed receipt for itemID.
// A store created without tillr-land's schema has no landings table and answers false.
func HasLanded(r *store.Reader, itemID string) (bool, error) {
	var hasLandings bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'landings')`).Scan(&hasLandings); err != nil {
		return false, err
	}
	if !hasLandings {
		return false, nil
	}
	var landed bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM landings WHERE item_id = ? AND result = 'landed')`, itemID).Scan(&landed); err != nil {
		return false, err
	}
	return landed, nil
}

// LandedItems answers the set of item IDs that have at least one landed receipt in landings.
func LandedItems(r *store.Reader) (map[string]bool, error) {
	var hasLandings bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'landings')`).Scan(&hasLandings); err != nil {
		return nil, err
	}
	if !hasLandings {
		return nil, nil
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
	active := map[string]Hold{}
	released := map[string]string{}
	for _, e := range events {
		switch e.Kind {
		case HeldKind:
			var h Hold
			if err := json.Unmarshal(e.Payload, &h); err != nil {
				return nil, nil, err
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
			if h.Kind == "fence" && p.AllowedPaths != nil && !sameStrings(p.AllowedPaths, h.AllowedPaths) {
				delete(active, id)
				released[id] = h.RunID
			}
		case "run.allocate":
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
	landed, err := LandedItems(r)
	if err != nil {
		return nil, nil, err
	}
	for id, h := range active {
		if landed[id] {
			active[id] = h.WithoutCloseRemedy()
		}
	}
	return active, released, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
