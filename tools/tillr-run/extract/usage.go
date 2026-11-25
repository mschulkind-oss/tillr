package extract

import (
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
)

var usageFields = map[string][4]string{
	"pi":     {"input", "output", "cacheRead", "cacheWrite"},
	"claude": {"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"},
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func parseISO8601WithTZ(s string) (time.Time, bool) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999-07:00",
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04:05Z07:00",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func stamp(record map[string]any) *float64 {
	v, ok := record["timestamp"]
	if !ok || v == nil {
		return nil
	}
	if f, ok := asFloat(v); ok {
		return &f
	}
	if s, ok := asString(v); ok {
		if t, ok := parseISO8601WithTZ(s); ok {
			ms := float64(t.UnixNano()) / 1e6
			return &ms
		}
	}
	return nil
}

func nonNegInt64(v any) *int64 {
	f, ok := asFloat(v)
	if !ok || f < 0 {
		return nil
	}
	n := int64(f)
	return &n
}

func hasAnyCounter(m run.ModelUsage) bool {
	return m.Input != nil || m.Output != nil || m.CacheRead != nil || m.CacheWrite != nil || m.Tokens != nil
}

func counters(usage map[string]any, backend string, model *string) run.ModelUsage {
	var out run.ModelUsage
	if model != nil && *model != "" {
		m := *model
		out.Model = &m
	}
	fields := usageFields[backend]
	if usage != nil {
		out.Input = nonNegInt64(usage[fields[0]])
		out.Output = nonNegInt64(usage[fields[1]])
		out.CacheRead = nonNegInt64(usage[fields[2]])
		out.CacheWrite = nonNegInt64(usage[fields[3]])
	}

	var total *int64
	if backend == "pi" && usage != nil {
		total = nonNegInt64(usage["totalTokens"])
	}
	if total != nil {
		out.Tokens = total
	} else if usage != nil {
		var sum int64
		saw := false
		for _, f := range fields {
			if n := nonNegInt64(usage[f]); n != nil {
				sum += *n
				saw = true
			}
		}
		if backend == "pi" {
			if n := nonNegInt64(usage["reasoning"]); n != nil {
				sum += *n
				saw = true
			}
		}
		if saw {
			out.Tokens = &sum
		}
	}
	return out
}

func summed(rows []run.TurnUsage, model *string) run.ModelUsage {
	out := run.ModelUsage{Model: model}
	if len(rows) == 0 {
		return out
	}

	sumField := func(get func(run.TurnUsage) *int64) *int64 {
		var total int64
		for _, r := range rows {
			v := get(r)
			if v == nil {
				return nil
			}
			total += *v
		}
		return &total
	}

	out.Input = sumField(func(r run.TurnUsage) *int64 { return r.Input })
	out.Output = sumField(func(r run.TurnUsage) *int64 { return r.Output })
	out.CacheRead = sumField(func(r run.TurnUsage) *int64 { return r.CacheRead })
	out.CacheWrite = sumField(func(r run.TurnUsage) *int64 { return r.CacheWrite })
	out.Tokens = sumField(func(r run.TurnUsage) *int64 { return r.Tokens })
	return out
}

type turnState struct {
	start        *float64
	firstSeen    bool
	first        *float64
	end          *float64
	id           string
	counters     *run.ModelUsage
	completionID *string
}

func Details(records []map[string]any, backend string) *run.UsageDetails {
	var states []*turnState
	byID := make(map[string]*turnState)
	var active *turnState
	var model *string
	var runStart *float64
	if len(records) > 0 {
		runStart = stamp(records[0])
	}
	runFirstSeen := false
	var runFirstAt *float64

	newState := func(at *float64, key string) *turnState {
		id := key
		if id == "" {
			id = fmt.Sprintf("turn-%d", len(states)+1)
		}
		st := &turnState{
			start: at,
			id:    id,
		}
		states = append(states, st)
		if key != "" {
			byID[key] = st
		}
		return st
	}

	firstDelta := func(st *turnState, at *float64) {
		if !runFirstSeen {
			runFirstSeen = true
			runFirstAt = at
		}
		if !st.firstSeen {
			st.firstSeen = true
			st.first = at
		}
	}

	for index, record := range records {
		kind, _ := asString(record["type"])
		at := stamp(record)
		message := asMap(record["message"])

		if kind == "system" {
			if sub, _ := asString(record["subtype"]); sub == "init" {
				if m, ok := asString(record["model"]); ok && m != "" {
					model = &m
				} else {
					model = nil
				}
			}
		}

		if backend == "pi" {
			switch kind {
			case "turn_start":
				active = newState(at, "")
			case "message_start":
				if role, _ := asString(message["role"]); role == "assistant" {
					if active == nil {
						active = newState(at, "")
					}
					if id, ok := asString(message["id"]); ok && id != "" {
						active.id = id
					}
				}
			case "message_update":
				delta := asMap(record["assistantMessageEvent"])
				dt, _ := asString(delta["type"])
				dText, _ := asString(delta["delta"])
				if (dt == "text_delta" || dt == "thinking_delta") && dText != "" {
					if active == nil {
						active = newState(nil, "")
					}
					firstDelta(active, at)
				}
			case "message_end":
				if role, _ := asString(message["role"]); role == "assistant" {
					if active == nil {
						active = newState(nil, "")
					}
					active.end = at
					if id, ok := asString(message["id"]); ok {
						idCopy := id
						active.completionID = &idCopy
					} else {
						active.completionID = nil
					}
				}
			case "turn_end":
				if active == nil {
					active = newState(nil, "")
				}
				var msgModel *string
				if m, ok := asString(message["model"]); ok && m != "" {
					msgModel = &m
				}
				c := counters(asMap(message["usage"]), backend, msgModel)
				active.counters = &c
				if id, ok := asString(message["id"]); ok && id != "" {
					active.id = id
					if active.completionID != nil && *active.completionID != id {
						active.end = nil
					}
				}
				active = nil
			}
		} else {
			if kind == "stream_event" {
				ev := asMap(record["event"])
				evType, _ := asString(ev["type"])
				switch evType {
				case "message_start":
					evMsg := asMap(ev["message"])
					key, _ := asString(evMsg["id"])
					active = newState(at, key)
				case "content_block_delta":
					delta := asMap(ev["delta"])
					dText, _ := asString(delta["text"])
					dThink, _ := asString(delta["thinking"])
					if dText != "" || dThink != "" {
						if active == nil {
							active = newState(nil, "")
						}
						firstDelta(active, at)
					}
				case "message_stop":
					if active != nil {
						active.end = at
					}
				}
				continue
			}
			if kind == "assistant" {
				key, _ := asString(message["id"])
				var st *turnState
				if key != "" {
					st = byID[key]
				}
				if st == nil {
					if active != nil && (key == "" || strings.HasPrefix(active.id, "turn-") || active.id == key) {
						st = active
					} else {
						fallbackKey := key
						if fallbackKey == "" {
							fallbackKey = fmt.Sprintf("line-%d", index)
						}
						st = newState(nil, fallbackKey)
					}
					if key != "" {
						st.id = key
						byID[key] = st
					}
				}
				if u := asMap(message["usage"]); u != nil {
					msgModel := model
					if m, ok := asString(message["model"]); ok && m != "" {
						msgModel = &m
					}
					c := counters(u, backend, msgModel)
					st.counters = &c
				}
			}
		}
	}

	turns := []run.TurnUsage{}
	for _, st := range states {
		var row run.TurnUsage
		if st.counters != nil {
			row.ModelUsage = *st.counters
		}
		if !hasAnyCounter(row.ModelUsage) {
			continue
		}
		row.ID = st.id
		if st.start != nil {
			v := *st.start
			row.StartedMS = &v
		}
		if st.end != nil {
			v := *st.end
			row.FinishedMS = &v
		}
		if st.first != nil {
			v := *st.first
			row.FirstOutputAtMS = &v
		}
		if st.start != nil && st.first != nil && *st.first >= *st.start {
			v := *st.first - *st.start
			row.FirstOutputMS = &v
		}
		if st.first != nil && st.end != nil && *st.end > *st.first && row.Output != nil {
			tps := float64(*row.Output) * 1000 / (*st.end - *st.first)
			row.OutputTPS = &tps
		}
		turns = append(turns, row)
	}

	var modelOrder []*string
	seenNil := false
	seenNamed := make(map[string]bool)
	for _, t := range turns {
		if t.Model == nil {
			if !seenNil {
				seenNil = true
				modelOrder = append(modelOrder, nil)
			}
		} else if !seenNamed[*t.Model] {
			seenNamed[*t.Model] = true
			m := *t.Model
			modelOrder = append(modelOrder, &m)
		}
	}

	models := make([]run.ModelUsage, 0, len(modelOrder))
	for _, name := range modelOrder {
		var rows []run.TurnUsage
		for _, t := range turns {
			if (name == nil && t.Model == nil) || (name != nil && t.Model != nil && *name == *t.Model) {
				rows = append(rows, t)
			}
		}
		models = append(models, summed(rows, name))
	}

	if backend == "claude" && len(models) <= 1 {
		var final map[string]any
		for i := len(records) - 1; i >= 0; i-- {
			if t, _ := asString(records[i]["type"]); t == "result" {
				final = records[i]
				break
			}
		}
		if final != nil {
			if u := asMap(final["usage"]); u != nil {
				m := model
				if len(models) > 0 {
					m = models[0].Model
				}
				measured := counters(u, backend, m)
				if hasAnyCounter(measured) {
					models = []run.ModelUsage{measured}
				}
			}
		}
	}

	if len(models) == 0 {
		return nil
	}

	out := &run.UsageDetails{
		Models: models,
		Turns:  turns,
	}
	if runStart != nil && runFirstAt != nil && *runFirstAt >= *runStart {
		v := *runFirstAt - *runStart
		out.FirstOutputMS = &v
	}
	if len(turns) > 0 && len(turns) == len(states) {
		allTPS := true
		for _, t := range turns {
			if t.OutputTPS == nil {
				allTPS = false
				break
			}
		}
		allOutput := true
		var totalOutput int64
		for _, m := range models {
			if m.Output == nil {
				allOutput = false
				break
			}
			totalOutput += *m.Output
		}
		if allTPS && allOutput {
			var seconds float64
			for _, t := range turns {
				seconds += (*t.FinishedMS - *t.FirstOutputAtMS) / 1000.0
			}
			if seconds > 0 {
				tps := float64(totalOutput) / seconds
				out.OutputTPS = &tps
			}
		}
	}
	return out
}
