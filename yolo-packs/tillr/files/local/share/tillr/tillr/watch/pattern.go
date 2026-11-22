package watch

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"maps"
	"math"
	"reflect"
	"slices"
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

type cachedEvent struct {
	obs     observation
	seat    sql.NullString
	typ     sql.NullString
	id      sql.NullString
	rev     sql.NullInt64
	payload any
}

func scanCachedEvent(rows *sql.Rows) (cachedEvent, error) {
	var ev cachedEvent
	var raw []byte
	if err := rows.Scan(&ev.obs.Seq, &ev.obs.TS, &ev.obs.Kind, &ev.seat, &ev.typ, &ev.id, &ev.rev, &raw); err != nil {
		return cachedEvent{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&ev.payload); err != nil {
		return cachedEvent{}, err
	}
	return ev, nil
}

func (ev *cachedEvent) lookup(name string) any {
	switch name {
	case "seq":
		return ev.obs.Seq
	case "ts":
		return ev.obs.TS
	case "kind":
		return ev.obs.Kind
	case "seat":
		if ev.seat.Valid {
			return ev.seat.String
		}
		return nil
	case "object_type":
		if ev.typ.Valid {
			return ev.typ.String
		}
		return nil
	case "object_id":
		if ev.id.Valid {
			return ev.id.String
		}
		return nil
	case "revision":
		if ev.rev.Valid {
			return ev.rev.Int64
		}
		return nil
	}
	v := ev.payload
	for _, key := range strings.Split(name, ".") {
		m, _ := v.(map[string]any)
		v = m[key]
	}
	return v
}

// kindCache holds every event of one kind with seq <= lastSeq and ts >= loadedFromTS,
// in strictly increasing seq order. Because events is append-only, later passes read
// only the tail after lastSeq.
type kindCache struct {
	loadedFromTS int64
	lastSeq      int64
	gen          uint64
	events       []cachedEvent
}

type ruleCacheEntry struct {
	rule             Rule
	kindGens         []uint64
	minIncludedTS    int64
	maxIncludedTS    int64
	maxExcludedOldTS int64
	minFutureTS      int64
	subjects         map[string]*condition
	skipped          int
}

type evalCache struct {
	store *store.Store
	kinds map[string]*kindCache
	rules map[string]*ruleCacheEntry
	git   gitCache
}

func newEvalCache(s *store.Store) *evalCache {
	return &evalCache{
		store: s,
		kinds: map[string]*kindCache{},
		rules: map[string]*ruleCacheEntry{},
	}
}

func (c *evalCache) syncKinds(rd *store.Reader, rules []Rule, now time.Time, onQuery func(kind string, sinceSeq int64, rows int)) error {
	kindWindowStart := map[string]int64{}
	for _, r := range rules {
		if !r.Enabled || r.evaluatorReason() != "" || r.When.Pattern == LagBehind {
			continue
		}
		ws := now.Add(-r.When.Window).UnixMilli()
		for _, k := range r.Reads.Events {
			if prev, ok := kindWindowStart[k]; !ok || ws < prev {
				kindWindowStart[k] = ws
			}
		}
	}
	if len(kindWindowStart) == 0 {
		return nil
	}
	snapHead, err := rd.EventHeadSeq()
	if err != nil {
		return err
	}
	for _, kind := range slices.Sorted(maps.Keys(kindWindowStart)) {
		windowStart := kindWindowStart[kind]
		kc := c.kinds[kind]
		if kc != nil && snapHead >= kc.lastSeq && windowStart >= kc.loadedFromTS {
			if snapHead > kc.lastSeq {
				since := kc.lastSeq
				rows, err := rd.Query(`SELECT seq,ts,kind,seat,object_type,object_id,revision,payload FROM events
 WHERE kind = ? AND seq > ? AND seq <= ? ORDER BY seq`, kind, since, snapHead)
				if err != nil {
					return err
				}
				scanned := 0
				added := false
				for rows.Next() {
					scanned++
					ev, err := scanCachedEvent(rows)
					if err != nil {
						rows.Close()
						return err
					}
					if ev.obs.TS >= windowStart {
						kc.events = append(kc.events, ev)
						added = true
					}
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				if onQuery != nil {
					onQuery(kind, since, scanned)
				}
				kc.lastSeq = snapHead
				if added {
					kc.gen++
				}
			}
			if windowStart > kc.loadedFromTS {
				if len(kc.events) > 0 {
					kept := kc.events[:0]
					for _, ev := range kc.events {
						if ev.obs.TS >= windowStart {
							kept = append(kept, ev)
						}
					}
					clear(kc.events[len(kept):])
					kc.events = kept
				}
				kc.loadedFromTS = windowStart
			}
			continue
		}
		var nextGen uint64 = 1
		if kc != nil {
			nextGen = kc.gen + 1
		}
		if snapHead == 0 {
			c.kinds[kind] = &kindCache{loadedFromTS: windowStart, lastSeq: 0, gen: nextGen}
			continue
		}
		rows, err := rd.Query(`SELECT seq,ts,kind,seat,object_type,object_id,revision,payload FROM events
 WHERE kind = ? AND seq <= ? AND ts >= ? ORDER BY seq`, kind, snapHead, windowStart)
		if err != nil {
			return err
		}
		var events []cachedEvent
		for rows.Next() {
			ev, err := scanCachedEvent(rows)
			if err != nil {
				rows.Close()
				return err
			}
			events = append(events, ev)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if onQuery != nil {
			onQuery(kind, 0, len(events))
		}
		c.kinds[kind] = &kindCache{
			loadedFromTS: windowStart,
			lastSeq:      snapHead,
			gen:          nextGen,
			events:       events,
		}
	}
	return nil
}

func ruleCacheable(r Rule) bool {
	if r.When.Pattern == LagBehind || r.When.Pattern == DurationWithoutEvent {
		return false
	}
	if len(r.Reads.Projections) != 0 || namedScope(r.Scope, "workflow:") != "" || namedScope(r.Scope, "charter:") != "" {
		return false
	}
	for _, expr := range r.expressions() {
		if expr != nil && expr.usesAge {
			return false
		}
	}
	return true
}

func exprEqual(a, b *Expression) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.source == b.source
}

func ruleEqual(a, b Rule) bool {
	return a.ID == b.ID &&
		a.Scope == b.Scope &&
		a.Enabled == b.Enabled &&
		slices.Equal(a.Reads.Events, b.Reads.Events) &&
		slices.Equal(a.Reads.Projections, b.Reads.Projections) &&
		a.When.GroupBy == b.When.GroupBy &&
		a.When.Pattern == b.When.Pattern &&
		a.When.Field == b.When.Field &&
		slices.Equal(a.When.Pairs, b.When.Pairs) &&
		a.When.Threshold == b.When.Threshold &&
		a.When.Window == b.When.Window &&
		exprEqual(a.When.Match, b.When.Match) &&
		exprEqual(a.When.Reset, b.When.Reset) &&
		exprEqual(a.When.ClearWhen, b.When.ClearWhen) &&
		exprEqual(a.When.After, b.When.After)
}

func (c *evalCache) kindGens(kinds []string) []uint64 {
	gens := make([]uint64, len(kinds))
	for i, k := range kinds {
		if kc := c.kinds[k]; kc != nil {
			gens[i] = kc.gen
		}
	}
	return gens
}

func (c *evalCache) gensMatch(want []uint64, kinds []string) bool {
	if len(want) != len(kinds) {
		return false
	}
	for i, k := range kinds {
		kc := c.kinds[k]
		if kc == nil || kc.gen != want[i] {
			return false
		}
	}
	return true
}

func (c *evalCache) forEachEvent(kinds []string, visit func(*cachedEvent) error) error {
	if len(kinds) == 1 {
		kc := c.kinds[kinds[0]]
		if kc == nil {
			return nil
		}
		for i := range kc.events {
			if err := visit(&kc.events[i]); err != nil {
				return err
			}
		}
		return nil
	}
	slicesList := make([][]cachedEvent, len(kinds))
	indices := make([]int, len(kinds))
	for i, k := range kinds {
		if kc := c.kinds[k]; kc != nil {
			slicesList[i] = kc.events
		}
	}
	for {
		best := -1
		var bestSeq int64
		for i, evs := range slicesList {
			if indices[i] >= len(evs) {
				continue
			}
			seq := evs[indices[i]].obs.Seq
			if best == -1 || seq < bestSeq {
				best, bestSeq = i, seq
			}
		}
		if best == -1 {
			return nil
		}
		ev := &slicesList[best][indices[best]]
		indices[best]++
		if err := visit(ev); err != nil {
			return err
		}
	}
}

func deriveCached(r Rule, now time.Time, facts ruleFacts, cache *evalCache) (map[string]*condition, int, error) {
	kinds := r.Reads.Events
	if len(kinds) == 0 {
		return nil, 0, nil
	}
	winLo := now.Add(-r.When.Window).UnixMilli()
	winHi := now.UnixMilli()
	cacheable := ruleCacheable(r)
	if cacheable {
		if entry := cache.rules[r.ID]; entry != nil &&
			ruleEqual(entry.rule, r) &&
			cache.gensMatch(entry.kindGens, kinds) &&
			winLo <= entry.minIncludedTS &&
			winLo > entry.maxExcludedOldTS &&
			winHi < entry.minFutureTS &&
			winHi >= entry.maxIncludedTS {
			return entry.subjects, entry.skipped, nil
		}
	}
	subjects := map[string]*condition{}
	skipped := 0
	var (
		minIncludedTS    int64 = math.MaxInt64
		maxIncludedTS    int64 = math.MinInt64
		maxExcludedOldTS int64 = math.MinInt64
		minFutureTS      int64 = math.MaxInt64
	)
	err := cache.forEachEvent(kinds, func(ev *cachedEvent) error {
		if ev.obs.TS < winLo {
			if ev.obs.TS > maxExcludedOldTS {
				maxExcludedOldTS = ev.obs.TS
			}
			return nil
		}
		if ev.obs.TS > winHi {
			if ev.obs.TS < minFutureTS {
				minFutureTS = ev.obs.TS
			}
			return nil
		}
		if ev.obs.TS < minIncludedTS {
			minIncludedTS = ev.obs.TS
		}
		if ev.obs.TS > maxIncludedTS {
			maxIncludedTS = ev.obs.TS
		}
		subject := "global"
		if r.When.GroupBy != "global" {
			var ok bool
			subject, ok = ev.lookup(r.When.GroupBy).(string)
			if !ok {
				skipped++
				return nil
			}
		}
		projection, inScope, err := facts.forSubject(subject)
		if err != nil {
			return err
		}
		if !inScope {
			return nil
		}
		c := subjects[subject]
		if c == nil {
			c = &condition{}
			subjects[subject] = c
		}
		c.observe(r.When, ev.obs, expressionValues{event: ev.lookup, projection: projection, nowMillis: winHi})
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if cacheable {
		cache.rules[r.ID] = &ruleCacheEntry{
			rule:             r,
			kindGens:         cache.kindGens(kinds),
			minIncludedTS:    minIncludedTS,
			maxIncludedTS:    maxIncludedTS,
			maxExcludedOldTS: maxExcludedOldTS,
			minFutureTS:      minFutureTS,
			subjects:         subjects,
			skipped:          skipped,
		}
	}
	return subjects, skipped, nil
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
		ev, err := scanCachedEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		subject := "global"
		if r.When.GroupBy != "global" {
			var ok bool
			subject, ok = ev.lookup(r.When.GroupBy).(string)
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
		c.observe(r.When, ev.obs, expressionValues{event: ev.lookup, projection: projection, nowMillis: now.UnixMilli()})
	}
	return subjects, skipped, rows.Err()
}
