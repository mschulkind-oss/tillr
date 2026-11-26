package watch

import (
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const bundleBudget = 8192

type bundleGuidance struct {
	Summary string   `json:"summary"`
	Check   []string `json:"check"`
	Do      []string `json:"do"`
}

type omission struct {
	Events     int  `json:"events"`
	Check      int  `json:"check"`
	Do         int  `json:"do"`
	SummaryCut bool `json:"summary_cut"`
	SubjectCut bool `json:"subject_cut"`
}

type TriageHeader struct {
	RunID        string   `json:"run_id"`
	Outcome      string   `json:"outcome"`
	OutcomeKind  string   `json:"outcome_kind,omitempty"`
	EscalatedTo  string   `json:"escalated_to,omitempty"`
	ItemID       string   `json:"item_id,omitempty"`
	Finding      string   `json:"finding"`
	ActionNeeded string   `json:"action_needed,omitempty"`
	Question     string   `json:"question,omitempty"`
	Options      []string `json:"options,omitempty"`
}

type Bundle struct {
	RuleID   string         `json:"rule_id"`
	Subject  string         `json:"subject"`
	Severity string         `json:"severity"`
	Triage   *TriageHeader  `json:"triage,omitempty"`
	Guidance bundleGuidance `json:"guidance"`
	Events   []observation  `json:"events"`
	Omitted  omission       `json:"omitted"`
}

func trackedPersistPrefix(itemID, itemState string) string {
	switch itemState {
	case "landed":
		return "tracked fix " + itemID + " landed and the condition persists: "
	case "closed", "missing":
		return "tracked fix " + itemID + " closed and the condition persists: "
	default:
		return "tracked fix " + itemID + " closed (" + itemState + ") and the condition persists: "
	}
}

func makeBundle(r Rule, key, id string, c *condition) ([]byte, error) {
	return makeBundleWithPrefix(r, key, id, c, "")
}

func makeBundleWithPrefix(r Rule, key, id string, c *condition, summaryPrefix string) ([]byte, error) {
	events := slices.Clone(c.events)
	slices.Reverse(events)
	var newestLookup func(string) any
	if len(c.events) > 0 {
		newestLookup = c.events[len(c.events)-1].lookup
	}
	return encodeConditionBundle(r, key, id, c, r.Guidance.Summary, summaryPrefix, events, newestLookup)
}

func makeLagReopenBundle(r Rule, key, id string, c *condition, summaryPrefix string) ([]byte, error) {
	summaryTemplate := r.Guidance.Summary
	if r.Guidance.ReopenSummary != "" {
		summaryTemplate = r.Guidance.ReopenSummary
	}
	events := []observation{}
	var newestLookup func(string) any
	if c.lag != nil && c.lag.reopenEvent != nil {
		events = []observation{*c.lag.reopenEvent}
		newestLookup = c.lag.reopenEvent.lookup
	}
	return encodeConditionBundle(r, key, id, c, summaryTemplate, summaryPrefix, events, newestLookup)
}

func encodeConditionBundle(r Rule, key, id string, c *condition, summaryTemplate, summaryPrefix string, events []observation, newestLookup func(string) any) ([]byte, error) {
	count := int64(len(events))
	var elapsed int64
	switch {
	case c.lag != nil:
		if events == nil {
			events = []observation{}
		}
		count, elapsed = c.lag.count, c.lag.elapsed.Milliseconds()
	case r.When.Pattern == DurationWithoutEvent:
		elapsed = c.waitAge.Milliseconds()
	case len(events) > 0:
		oldest, newest := events[0].TS, events[0].TS
		for _, event := range events[1:] {
			oldest = min(oldest, event.TS)
			newest = max(newest, event.TS)
		}
		elapsed = newest - oldest
	}
	elapsedText := strconv.FormatInt(elapsed/1000, 10) + "s"
	if elapsed >= 60_000 {
		elapsedText = strconv.FormatInt(elapsed/60_000, 10) + "m"
	}
	base := map[string]string{
		"subject":  key,
		"rule_id":  r.ID,
		"alert_id": id,
		"count":    strconv.FormatInt(count, 10),
		"elapsed":  elapsedText,
	}
	if c.lag != nil {
		short := func(oid string) string { return oid[:min(12, len(oid))] }
		base["leader"] = r.When.Leader
		base["follower"] = r.When.Follower
		base["leader_short"] = short(c.lag.leaderOID)
		base["follower_short"] = short(c.lag.followerOID)
	}
	if r.When.Pattern == AlternatingPair {
		base["pair_a"] = c.pair[0]
		base["pair_b"] = c.pair[1]
	}
	if (r.When.Pattern == CountInWindow || r.When.Pattern == ConsecutiveCount) && r.When.DistinctBy != "" {
		vals := make([]string, 0, len(c.events))
		for _, ev := range c.events {
			s := ev.distinct
			if s == "" && ev.lookup != nil {
				s, _ = ev.lookup(r.When.DistinctBy).(string)
			}
			if s != "" {
				vals = append(vals, s)
			}
		}
		base["distinct_values"] = strings.Join(vals, ", ")
	}
	vars := extractVariables(r, key, c)
	for _, name := range r.VariableNames() {
		val, ok := vars[name]
		if !ok || val == "" {
			val = "unknown"
		}
		base["var."+name] = val
	}
	render := func(lines []string) []string {
		out := make([]string, len(lines))
		for i, line := range lines {
			out[i] = renderGuidanceLine(line, base, newestLookup)
		}
		return out
	}
	b := Bundle{RuleID: r.ID, Subject: key, Severity: r.Severity, Events: events, Guidance: bundleGuidance{Summary: summaryPrefix + renderGuidanceLine(summaryTemplate, base, newestLookup), Check: render(r.Guidance.Check), Do: render(r.Guidance.Do)}}
	return b.encode()
}

func renderGuidanceLine(line string, base map[string]string, newestLookup func(string) any) string {
	var b strings.Builder
	pos := 0
	for {
		rel := strings.Index(line[pos:], "{{")
		if rel < 0 {
			b.WriteString(line[pos:])
			return b.String()
		}
		start := pos + rel
		endRel := strings.Index(line[start+2:], "}}")
		if endRel < 0 {
			b.WriteString(line[pos:])
			return b.String()
		}
		end := start + 2 + endRel
		token := line[start+2 : end]
		if rep, ok := base[token]; ok {
			b.WriteString(line[pos:start])
			b.WriteString(rep)
			pos = end + 2
			continue
		}
		if newestLookup != nil && strings.HasPrefix(token, "event.") {
			name := strings.TrimPrefix(token, "event.")
			if validEventPath(name) && !strings.HasPrefix(name, "event.") {
				b.WriteString(line[pos:start])
				b.WriteString(formatEventValue(newestLookup(name)))
				pos = end + 2
				continue
			}
		}
		b.WriteString(line[pos : start+2])
		pos = start + 2
	}
}

func formatEventScalar(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		if x != "" {
			return x, true
		}
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	default:
		if i, ok := integer(v); ok {
			return strconv.FormatInt(i, 10), true
		}
	}
	return "", false
}

func formatEventValue(v any) string {
	if s, ok := formatEventScalar(v); ok {
		return s
	}
	switch xs := v.(type) {
	case []any:
		if len(xs) == 0 {
			return "unknown"
		}
		parts := make([]string, len(xs))
		for i, elem := range xs {
			s, ok := formatEventScalar(elem)
			if !ok {
				return "unknown"
			}
			parts[i] = s
		}
		return strings.Join(parts, ", ")
	case []string:
		if len(xs) == 0 {
			return "unknown"
		}
		for _, elem := range xs {
			if elem == "" {
				return "unknown"
			}
		}
		return strings.Join(xs, ", ")
	}
	return "unknown"
}

func (b Bundle) encode() ([]byte, error) {
	data, err := json.Marshal(b)
	if err != nil || len(data) <= bundleBudget {
		return data, err
	}
	// Length is monotone as each suffix is cut. Find the first cut that
	// fits without encoding the same large event or text once per element.
	trim := func(n int, cut func(int)) bool {
		i := sort.Search(n+1, func(i int) bool { cut(i); data, err = json.Marshal(b); return err != nil || len(data) <= bundleBudget })
		cut(min(i, n))
		data, err = json.Marshal(b)
		return err != nil || len(data) <= bundleBudget
	}
	events := b.Events
	if trim(len(events), func(n int) { b.Events = events[:len(events)-n]; b.Omitted.Events = n }) {
		return data, err
	}
	do := b.Guidance.Do
	if trim(len(do), func(n int) { b.Guidance.Do = do[:len(do)-n]; b.Omitted.Do = n }) {
		return data, err
	}
	check := b.Guidance.Check
	if trim(len(check), func(n int) { b.Guidance.Check = check[:len(check)-n]; b.Omitted.Check = n }) {
		return data, err
	}
	summary := []rune(b.Guidance.Summary)
	if trim(len(summary), func(n int) { b.Guidance.Summary = string(summary[:len(summary)-n]); b.Omitted.SummaryCut = n != 0 }) {
		return data, err
	}
	subject := []rune(b.Subject)
	trim(len(subject), func(n int) { b.Subject = string(subject[:len(subject)-n]); b.Omitted.SubjectCut = n != 0 })
	return data, err
}
