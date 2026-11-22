package watch

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

type observation struct {
	Seq   int64  `json:"seq"`
	TS    int64  `json:"ts"`
	Kind  string `json:"kind"`
	Value *any   `json:"value,omitempty"`
}

type condition struct {
	events      []observation // counted events, in seq order
	clears      []int64
	pair        [2]string
	previous    observation
	hadPrevious bool
	waitAge     time.Duration
	lag         *lagCondition
}

func (c *condition) fires(w When) bool {
	switch w.Pattern {
	case DurationWithoutEvent:
		return len(c.events) != 0 && c.waitAge >= w.ThresholdDuration
	case LagBehind:
		return c.lag != nil && c.lag.fires(w)
	default:
		return int64(len(c.events)) >= w.Threshold
	}
}

func (c *condition) observe(r When, e observation, values expressionValues) {
	clear := r.ClearWhen != nil && r.ClearWhen.eval(values)
	if r.Pattern == DurationWithoutEvent {
		if !clear && r.After.eval(values) {
			c.events = append(c.events[:0], e)
			c.waitAge = time.UnixMilli(values.nowMillis).Sub(time.UnixMilli(e.TS))
		} else if clear || r.Match != nil && r.Match.eval(values) {
			c.events = c.events[:0]
		} else {
			return
		}
		c.clears = append(c.clears, e.Seq)
		return
	}
	if clear {
		c.events = c.events[:0]
		c.hadPrevious = false
		c.clears = append(c.clears, e.Seq)
		return
	}
	if r.Pattern == AlternatingPair {
		value := values.event(r.Field)
		if value == nil {
			return // Missing and null names have no value to observe.
		}
		e.Value = &value
		if c.hadPrevious && reflect.DeepEqual(*c.previous.Value, value) {
			return
		}
		count := len(c.events)
		if pair, ok := firstPair(r.Pairs, value); !ok {
			c.events = c.events[:0]
		} else if shared, ok := sharedPair(r.Pairs, value, c.previous, c.hadPrevious); ok {
			if count >= 2 && c.pair == shared {
				c.events = append(c.events, e)
			} else {
				c.events = append(c.events[:0], c.previous, e)
			}
			c.pair = shared
		} else {
			// A singleton can belong to more than one entry. Use the first one.
			c.pair = pair
			c.events = append(c.events[:0], e)
		}
		if len(c.events) != count+1 {
			c.clears = append(c.clears, e.Seq)
		}
		c.previous, c.hadPrevious = e, true
		return
	}
	match := r.Match == nil || r.Match.eval(values)
	if r.Pattern == ConsecutiveCount && ((r.Reset == nil && !match) || (r.Reset != nil && r.Reset.eval(values))) {
		c.events = c.events[:0]
		c.clears = append(c.clears, e.Seq)
	} else if match {
		c.events = append(c.events, e)
	}
}

func firstPair(pairs [][2]string, v any) ([2]string, bool) {
	if s, ok := v.(string); ok {
		for _, p := range pairs {
			if p[0] == s || p[1] == s {
				return p, true
			}
		}
	}
	return [2]string{}, false
}

func sharedPair(pairs [][2]string, v any, previous observation, hasPrevious bool) ([2]string, bool) {
	if hasPrevious {
		a, aOK := v.(string)
		b, bOK := (*previous.Value).(string)
		if aOK && bOK && a != b {
			for _, p := range pairs {
				if p == [2]string{a, b} || p == [2]string{b, a} {
					return p, true
				}
			}
		}
	}
	return [2]string{}, false
}

func derive(rd *store.Reader, r Rule, now time.Time, facts ruleFacts) (map[string]*condition, int, error) {
	kinds := r.Reads.Events
	args := []any{now.Add(-r.When.Window).UnixMilli(), now.UnixMilli()}
	for _, k := range kinds {
		args = append(args, k)
	}
	if len(kinds) == 0 {
		return nil, 0, nil
	}
	rows, err := rd.Query(`SELECT seq,ts,kind,seat,object_type,object_id,revision,payload FROM events
 WHERE ts >= ? AND ts <= ? AND kind IN (?`+strings.Repeat(",?", len(kinds)-1)+`) ORDER BY seq`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	subjects := map[string]*condition{}
	skipped := 0
	for rows.Next() {
		var e observation
		var seat, typ, id sql.NullString
		var rev sql.NullInt64
		var raw []byte
		if err := rows.Scan(&e.Seq, &e.TS, &e.Kind, &seat, &typ, &id, &rev, &raw); err != nil {
			return nil, 0, err
		}
		var payload any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			return nil, 0, err
		}
		lookup := func(name string) any {
			switch name {
			case "seq":
				return e.Seq
			case "ts":
				return e.TS
			case "kind":
				return e.Kind
			case "seat":
				if seat.Valid {
					return seat.String
				}
				return nil
			case "object_type":
				if typ.Valid {
					return typ.String
				}
				return nil
			case "object_id":
				if id.Valid {
					return id.String
				}
				return nil
			case "revision":
				if rev.Valid {
					return rev.Int64
				}
				return nil
			}
			v := payload
			for _, key := range strings.Split(name, ".") {
				m, _ := v.(map[string]any)
				v = m[key]
			}
			return v
		}
		subject := "global"
		if r.When.GroupBy != "global" {
			var ok bool
			subject, ok = lookup(r.When.GroupBy).(string)
			if !ok {
				skipped++
				continue
			}
		}
		projection, inScope, err := facts.forSubject(subject)
		if err != nil {
			return nil, 0, err
		}
		if !inScope {
			continue
		}
		c := subjects[subject]
		if c == nil {
			c = &condition{}
			subjects[subject] = c
		}
		c.observe(r.When, e, expressionValues{event: lookup, projection: projection, nowMillis: now.UnixMilli()})
	}
	return subjects, skipped, rows.Err()
}
