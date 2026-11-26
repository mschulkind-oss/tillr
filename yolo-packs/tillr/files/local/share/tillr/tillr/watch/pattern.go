package watch

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"
)

type observation struct {
	Seq   int64  `json:"seq"`
	TS    int64  `json:"ts"`
	Kind  string `json:"kind"`
	Value *any   `json:"value,omitempty"`

	fingerprint string           // internal cause facts; not part of the timeline bundle
	distinct    string           // internal distinct_by value; not part of the timeline bundle
	lookup      func(string) any // internal event field lookup; not part of the timeline bundle
}

func fingerprint(names []string, lookup func(string) any) string {
	if len(names) == 0 {
		return ""
	}
	h := sha256.New()
	for _, name := range names {
		if s, ok := lookup(name).(string); ok {
			var header [9]byte
			header[0] = 1
			binary.BigEndian.PutUint64(header[1:], uint64(len(s)))
			h.Write(header[:])
			h.Write([]byte(s))
		} else {
			h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

type condition struct {
	events          []observation // counted events, in seq order
	clears          []int64
	pair            [2]string
	previous        observation
	hadPrevious     bool
	armed           bool
	waitAge         time.Duration
	lag             *lagCondition
	projectionMatch bool
	subjectSeq      int64
}

func (c *condition) fires(w When) bool {
	switch w.Pattern {
	case DurationWithoutEvent:
		return len(c.events) != 0 && c.waitAge >= w.ThresholdDuration
	case LagBehind:
		return c.lag != nil && c.lag.fires(w)
	case PredicateMatch:
		if c.projectionMatch {
			return true
		}
		return int64(len(c.events)) >= w.Threshold
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
		c.armed = false
		c.clears = append(c.clears, e.Seq)
		return
	}
	if r.Pattern == PredicateMatch && r.After != nil {
		if r.Reset != nil && r.Reset.eval(values) {
			c.armed = false
			return
		}
		if r.After.eval(values) {
			c.armed = true
			return
		}
		if c.armed && r.Match != nil && r.Match.eval(values) {
			c.events = append(c.events, e)
			c.armed = false
		}
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
		if r.DistinctBy != "" {
			var raw any
			if values.event != nil {
				raw = values.event(r.DistinctBy)
			} else if e.lookup != nil {
				raw = e.lookup(r.DistinctBy)
			}
			s, ok := raw.(string)
			if !ok || s == "" {
				return
			}
			e.distinct = s
			c.events = slices.DeleteFunc(c.events, func(prev observation) bool {
				return prev.distinct == s
			})
		}
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

type ruleBaseline struct {
	ruleID         string
	definitionHash string
	enabled        bool
	firstLoadedSeq int64
	firstLoadedAt  int64
	loadedSeq      int64
	loadedAt       int64
}

type ruleDefinitionDigestInput struct {
	Scope              string            `json:"scope"`
	Variables          map[string]string `json:"variables,omitempty"`
	Events             []string          `json:"events"`
	Projections        []string          `json:"projections"`
	GroupBy            string            `json:"group_by"`
	GroupByList        []string          `json:"group_by_list"`
	GroupByAny         []string          `json:"group_by_any,omitempty"`
	Pattern            string            `json:"pattern"`
	Field              string            `json:"field"`
	DistinctBy         string            `json:"distinct_by,omitempty"`
	ForEach            string            `json:"for_each,omitempty"`
	BroadcastEvents    []string          `json:"broadcast_events,omitempty"`
	Pairs              [][2]string       `json:"pairs"`
	Threshold          int64             `json:"threshold"`
	ThresholdCommits   int64             `json:"threshold_commits"`
	Window             int64             `json:"window"`
	ThresholdDuration  int64             `json:"threshold_duration"`
	Leader             string            `json:"leader"`
	Follower           string            `json:"follower"`
	Mode               string            `json:"mode"`
	Match              string            `json:"match"`
	Reset              string            `json:"reset"`
	ClearWhen          string            `json:"clear_when"`
	After              string            `json:"after"`
	ReopenWhen         string            `json:"reopen_when"`
	KeepOnTerminalItem bool              `json:"keep_on_terminal_item,omitempty"`
}

func normalizeStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func normalizePairs(p [][2]string) [][2]string {
	if len(p) == 0 {
		return nil
	}
	return p
}

func exprSource(e *Expression) string {
	if e == nil {
		return ""
	}
	return e.source
}

func definitionHash(r Rule) string {
	in := ruleDefinitionDigestInput{
		Scope:              r.Scope,
		Variables:          r.Variables,
		Events:             normalizeStrings(r.Reads.Events),
		Projections:        normalizeStrings(r.Reads.Projections),
		GroupBy:            r.When.GroupBy,
		GroupByList:        normalizeStrings(r.When.GroupByList),
		GroupByAny:         normalizeStrings(r.When.GroupByAny),
		Pattern:            r.When.Pattern,
		Field:              r.When.Field,
		DistinctBy:         r.When.DistinctBy,
		ForEach:            r.When.ForEach,
		BroadcastEvents:    normalizeStrings(r.When.BroadcastEvents),
		Pairs:              normalizePairs(r.When.Pairs),
		Threshold:          r.When.Threshold,
		ThresholdCommits:   r.When.ThresholdCommits,
		Window:             int64(r.When.Window),
		ThresholdDuration:  int64(r.When.ThresholdDuration),
		Leader:             r.When.Leader,
		Follower:           r.When.Follower,
		Mode:               r.When.Mode,
		Match:              exprSource(r.When.Match),
		Reset:              exprSource(r.When.Reset),
		ClearWhen:          exprSource(r.When.ClearWhen),
		After:              exprSource(r.When.After),
		ReopenWhen:         exprSource(r.When.ReopenWhen),
		KeepOnTerminalItem: r.When.KeepOnTerminalItem,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func effectiveFirstLoadedSeq(r Rule, baselines map[string]ruleBaseline, defaultHead int64) int64 {
	if b, ok := baselines[r.ID]; ok {
		return b.firstLoadedSeq
	}
	return defaultHead
}

func effectiveLoadedSeq(r Rule, baselines map[string]ruleBaseline, defaultHead int64) int64 {
	if b, ok := baselines[r.ID]; ok && b.enabled && b.definitionHash == definitionHash(r) {
		return b.loadedSeq
	}
	return defaultHead
}

// kindCache holds every observation of one kind with seq > loadedFromSeq,
// seq <= lastSeq, and ts >= loadedFromTS, in strictly increasing seq order.
// Because streams are append-only within an epoch, later passes read only the
// tail after lastSeq.
type kindCache struct {
	loadedFromTS  int64
	loadedFromSeq int64
	lastSeq       int64
	gen           uint64
	events        []Observation
}

type ruleCacheEntry struct {
	rule             Rule
	firstLoadedSeq   int64
	kindGens         []uint64
	activeKeys       []string
	minIncludedTS    int64
	maxIncludedTS    int64
	maxExcludedOldTS int64
	minFutureTS      int64
	subjects         map[string]*condition
	skipped          int
}

type evalCache struct {
	sourceKey              any
	kinds                  map[string]*kindCache
	rules                  map[string]*ruleCacheEntry
	git                    gitCache
	hasAlertTrackingCols   bool
	hasAlertAckCols        bool
	hasAlertSettlementCols bool
	hasItemsEventSeq       bool
}

func newEvalCache(sourceKey any) *evalCache {
	return &evalCache{
		sourceKey: sourceKey,
		kinds:     map[string]*kindCache{},
		rules:     map[string]*ruleCacheEntry{},
	}
}

func windowStartMillis(w When, now time.Time) int64 {
	if (w.Pattern == PredicateMatch && w.ClearWhen != nil && w.Window == 0) ||
		(w.Pattern == LagBehind && w.ReopenWhen != nil) {
		return math.MinInt64
	}
	return now.Add(-w.Window).UnixMilli()
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
		maps.Equal(a.Variables, b.Variables) &&
		slices.Equal(a.Reads.Events, b.Reads.Events) &&
		slices.Equal(a.Reads.Projections, b.Reads.Projections) &&
		slices.Equal(a.When.Fingerprint, b.When.Fingerprint) &&
		a.When.GroupBy == b.When.GroupBy &&
		slices.Equal(a.When.GroupByList, b.When.GroupByList) &&
		slices.Equal(a.When.GroupByAny, b.When.GroupByAny) &&
		a.When.Pattern == b.When.Pattern &&
		a.When.Field == b.When.Field &&
		a.When.DistinctBy == b.When.DistinctBy &&
		a.When.ForEach == b.When.ForEach &&
		slices.Equal(a.When.BroadcastEvents, b.When.BroadcastEvents) &&
		slices.Equal(a.When.Pairs, b.When.Pairs) &&
		a.When.Threshold == b.When.Threshold &&
		a.When.Window == b.When.Window &&
		a.When.KeepOnTerminalItem == b.When.KeepOnTerminalItem &&
		exprEqual(a.When.Match, b.When.Match) &&
		exprEqual(a.When.Reset, b.When.Reset) &&
		exprEqual(a.When.ClearWhen, b.When.ClearWhen) &&
		exprEqual(a.When.After, b.When.After) &&
		exprEqual(a.When.ReopenWhen, b.When.ReopenWhen) &&
		a.Guidance.ReopenSummary == b.Guidance.ReopenSummary
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

func (c *evalCache) forEachEvent(kinds []string, visit func(Observation) error) error {
	if len(kinds) == 1 {
		kc := c.kinds[kinds[0]]
		if kc == nil {
			return nil
		}
		for _, ev := range kc.events {
			if err := visit(ev); err != nil {
				return err
			}
		}
		return nil
	}
	slicesList := make([][]Observation, len(kinds))
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
			seq := evs[indices[i]].Seq
			if best == -1 || seq < bestSeq {
				best, bestSeq = i, seq
			}
		}
		if best == -1 {
			return nil
		}
		ev := slicesList[best][indices[best]]
		indices[best]++
		if err := visit(ev); err != nil {
			return err
		}
	}
}

func (w When) groupByNames() []string {
	if len(w.GroupByList) >= 2 {
		return w.GroupByList
	}
	if w.GroupBy != "" {
		return []string{w.GroupBy}
	}
	return nil
}

func escapeSubjectPart(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "/", "%2F")
	return s
}

func encodeCompositeSubject(parts []string) string {
	escaped := make([]string, len(parts))
	for i, p := range parts {
		escaped[i] = escapeSubjectPart(p)
	}
	return strings.Join(escaped, "/")
}

func unescapeSubjectPart(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", false
		}
		switch s[i+1 : i+3] {
		case "25":
			b.WriteByte('%')
		case "2F":
			b.WriteByte('/')
		default:
			return "", false
		}
		i += 2
	}
	return b.String(), true
}

func decodeSubjectKey(key string, n int) ([]string, bool) {
	if n == 1 {
		return []string{key}, true
	}
	return decodeCompositeSubject(key, n)
}

func decodeCompositeSubject(key string, n int) ([]string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != n {
		return nil, false
	}
	out := make([]string, n)
	for i, p := range parts {
		u, ok := unescapeSubjectPart(p)
		if !ok {
			return nil, false
		}
		out[i] = u
	}
	return out, true
}

type activeCompositeFunc func(ruleID string, n int) (map[string][]string, []string, error)

func toBundleObs(rec Observation, fpNames []string) observation {
	lookup := rec.Lookup
	return observation{
		Seq:         rec.Seq,
		TS:          rec.TS,
		Kind:        rec.Kind,
		fingerprint: fingerprint(fpNames, lookup),
		lookup:      lookup,
	}
}

func observeCompositeRecord(
	r Rule,
	names []string,
	rec Observation,
	facts subjectFacts,
	nowMillis int64,
	subjects map[string]*condition,
	subjectTuples map[string][]string,
	skipped *int,
	seenInEvent map[string]bool,
) error {
	n := len(names)
	tuple := make([]string, 0, n)
	seenNonString := false
	prefixValid := true
	lookup := rec.Lookup
	for _, name := range names {
		v := lookup(name)
		if !seenNonString {
			if s, ok := v.(string); ok {
				tuple = append(tuple, s)
				continue
			}
			seenNonString = true
		}
		if v != nil {
			prefixValid = false
		}
	}
	if len(tuple) == n {
		subject := encodeCompositeSubject(tuple)
		projection, inScope, err := lookupSubjectFacts(facts, subject, lookup)
		if err != nil {
			return err
		}
		if !inScope {
			return nil
		}
		if seenInEvent != nil {
			seenInEvent[subject] = true
		}
		subjectTuples[subject] = tuple
		c := subjects[subject]
		if c == nil {
			c = &condition{}
			subjects[subject] = c
		}
		obs := toBundleObs(rec, r.When.Fingerprint)
		c.observe(r.When, obs, expressionValues{event: lookup, projection: projection, nowMillis: nowMillis})
		return nil
	}
	if prefixValid && len(tuple) >= 1 && len(tuple) < n && r.When.ClearWhen != nil {
		projection, _, err := facts.forSubject("")
		if err != nil {
			return err
		}
		if r.When.ClearWhen.eval(expressionValues{event: lookup, projection: projection, nowMillis: nowMillis}) {
			for subject, subTuple := range subjectTuples {
				if len(subTuple) == n && slices.Equal(subTuple[:len(tuple)], tuple) {
					c := subjects[subject]
					c.events = c.events[:0]
					c.hadPrevious = false
					c.armed = false
					c.clears = append(c.clears, rec.Seq)
				}
			}
			return nil
		}
	}
	*skipped++
	return nil
}

func observeRecord(
	r Rule,
	names []string,
	rec Observation,
	facts subjectFacts,
	nowMillis int64,
	subjects map[string]*condition,
	subjectTuples map[string][]string,
	skipped *int,
) error {
	if len(r.When.BroadcastEvents) > 0 && slices.Contains(r.When.BroadcastEvents, rec.Kind) {
		return observeBroadcastRecord(r, rec, facts, nowMillis, subjects)
	}
	if r.When.ForEach != "" {
		return observeForEachRecord(r, names, rec, facts, nowMillis, subjects, subjectTuples, skipped)
	}
	return observeSingleRecord(r, names, rec, facts, nowMillis, subjects, subjectTuples, skipped, nil)
}

func observeBroadcastRecord(
	r Rule,
	rec Observation,
	facts subjectFacts,
	nowMillis int64,
	subjects map[string]*condition,
) error {
	lookup := rec.Lookup
	for subject, c := range subjects {
		projection, inScope, err := lookupSubjectFacts(facts, subject, lookup)
		if err != nil {
			return err
		}
		if !inScope {
			continue
		}
		values := expressionValues{event: lookup, projection: projection, nowMillis: nowMillis}
		if r.When.ClearWhen != nil && r.When.ClearWhen.eval(values) {
			c.events = c.events[:0]
			c.hadPrevious = false
			c.armed = false
			c.clears = append(c.clears, rec.Seq)
			continue
		}
		if r.When.Reset != nil && r.When.Reset.eval(values) {
			if r.When.Pattern == PredicateMatch && r.When.After != nil {
				c.armed = false
			} else if r.When.Pattern == ConsecutiveCount {
				c.events = c.events[:0]
				c.clears = append(c.clears, rec.Seq)
			}
		}
	}
	return nil
}

func observeForEachRecord(
	r Rule,
	names []string,
	rec Observation,
	facts subjectFacts,
	nowMillis int64,
	subjects map[string]*condition,
	subjectTuples map[string][]string,
	skipped *int,
) error {
	raw := rec.Lookup(r.When.ForEach)
	var elems []any
	switch list := raw.(type) {
	case []any:
		elems = list
	case []map[string]any:
		elems = make([]any, len(list))
		for i, m := range list {
			elems[i] = m
		}
	default:
		*skipped++
		return nil
	}
	elemRule := r
	elemRule.When.ForEach = ""
	seenInEvent := make(map[string]bool, len(elems))
	for _, elem := range elems {
		elemMap, ok := elem.(map[string]any)
		if !ok || elemMap == nil {
			*skipped++
			continue
		}
		merged := make(map[string]any, len(rec.Fields)+len(elemMap))
		maps.Copy(merged, rec.Fields)
		maps.Copy(merged, elemMap)
		for _, col := range []string{"seat", "object_type", "object_id", "revision"} {
			if v, exists := rec.Fields[col]; exists {
				merged[col] = v
			} else {
				delete(merged, col)
			}
		}
		child := Observation{
			Seq:    rec.Seq,
			TS:     rec.TS,
			Kind:   rec.Kind,
			Fields: merged,
		}
		if err := observeSingleRecord(elemRule, names, child, facts, nowMillis, subjects, subjectTuples, skipped, seenInEvent); err != nil {
			return err
		}
	}
	if r.When.Pattern == ConsecutiveCount && r.When.Reset == nil {
		for sub, c := range subjects {
			if !seenInEvent[sub] {
				c.events = c.events[:0]
				c.clears = append(c.clears, rec.Seq)
			}
		}
	}
	return nil
}

func observeSingleRecord(
	r Rule,
	names []string,
	rec Observation,
	facts subjectFacts,
	nowMillis int64,
	subjects map[string]*condition,
	subjectTuples map[string][]string,
	skipped *int,
	seenInEvent map[string]bool,
) error {
	if len(names) >= 2 {
		return observeCompositeRecord(r, names, rec, facts, nowMillis, subjects, subjectTuples, skipped, seenInEvent)
	}
	lookup := rec.Lookup
	subject := "global"
	if len(r.When.GroupByAny) > 0 {
		var found bool
		for _, col := range r.When.GroupByAny {
			if s, ok := lookup(col).(string); ok && s != "" {
				subject = s
				found = true
				break
			}
		}
		if !found {
			*skipped++
			return nil
		}
	} else if r.When.GroupBy != "global" {
		var ok bool
		subject, ok = lookup(r.When.GroupBy).(string)
		if !ok {
			*skipped++
			return nil
		}
	}
	projection, inScope, err := lookupSubjectFacts(facts, subject, lookup)
	if err != nil {
		return err
	}
	if !inScope {
		return nil
	}
	if seenInEvent != nil {
		seenInEvent[subject] = true
	}
	c := subjects[subject]
	if c == nil {
		c = &condition{}
		subjects[subject] = c
	}
	obs := toBundleObs(rec, r.When.Fingerprint)
	c.observe(r.When, obs, expressionValues{event: lookup, projection: projection, nowMillis: nowMillis})
	return nil
}

func lookupSubjectFacts(facts subjectFacts, subject string, eventLookup func(string) any) (func(projectionOperand) any, bool, error) {
	if obsFacts, ok := facts.(observationFacts); ok {
		return obsFacts.forObservation(subject, eventLookup)
	}
	return facts.forSubject(subject)
}

func deriveCached(r Rule, now time.Time, facts subjectFacts, cache *evalCache, firstLoadedSeq int64, activeSubjects activeCompositeFunc) (map[string]*condition, int, error) {
	kinds := r.Reads.Events
	if len(kinds) == 0 {
		return nil, 0, nil
	}
	names := r.When.groupByNames()
	var (
		seededTuples map[string][]string
		activeKeys   []string
	)
	if (len(names) >= 2 || r.When.ForEach != "" || len(r.When.BroadcastEvents) > 0) && activeSubjects != nil {
		var err error
		seededTuples, activeKeys, err = activeSubjects(r.ID, len(names))
		if err != nil {
			return nil, 0, err
		}
	}
	winLo := windowStartMillis(r.When, now)
	winHi := now.UnixMilli()
	cacheable := ruleCacheable(r)
	if cacheable {
		if entry := cache.rules[r.ID]; entry != nil &&
			entry.firstLoadedSeq == firstLoadedSeq &&
			ruleEqual(entry.rule, r) &&
			cache.gensMatch(entry.kindGens, kinds) &&
			slices.Equal(entry.activeKeys, activeKeys) &&
			(winLo == math.MinInt64 || (winLo <= entry.minIncludedTS && winLo > entry.maxExcludedOldTS)) &&
			winHi < entry.minFutureTS &&
			winHi >= entry.maxIncludedTS {
			return entry.subjects, entry.skipped, nil
		}
	}
	subjects := map[string]*condition{}
	subjectTuples := map[string][]string{}
	for key, tuple := range seededTuples {
		subjects[key] = &condition{}
		if len(names) >= 2 {
			subjectTuples[key] = tuple
		}
	}
	skipped := 0
	var (
		minIncludedTS    int64 = math.MaxInt64
		maxIncludedTS    int64 = math.MinInt64
		maxExcludedOldTS int64 = math.MinInt64
		minFutureTS      int64 = math.MaxInt64
	)
	err := cache.forEachEvent(kinds, func(rec Observation) error {
		if rec.Seq <= firstLoadedSeq {
			return nil
		}
		if rec.TS < winLo {
			if rec.TS > maxExcludedOldTS {
				maxExcludedOldTS = rec.TS
			}
			return nil
		}
		if rec.TS > winHi {
			if rec.TS < minFutureTS {
				minFutureTS = rec.TS
			}
			return nil
		}
		if rec.TS < minIncludedTS {
			minIncludedTS = rec.TS
		}
		if rec.TS > maxIncludedTS {
			maxIncludedTS = rec.TS
		}
		return observeRecord(r, names, rec, facts, winHi, subjects, subjectTuples, &skipped)
	})
	if err != nil {
		return nil, 0, err
	}
	if cacheable {
		cache.rules[r.ID] = &ruleCacheEntry{
			rule:             r,
			firstLoadedSeq:   firstLoadedSeq,
			kindGens:         cache.kindGens(kinds),
			activeKeys:       activeKeys,
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

func deriveFromSource(
	ctx context.Context,
	src ObservationSource,
	r Rule,
	now time.Time,
	facts subjectFacts,
	resolveBaseline func(ruleID string, cursor SourceCursor) int64,
	activeSubjects activeCompositeFunc,
) (map[string]*condition, int, SourceCursor, error) {
	kinds := r.Reads.Events
	if len(kinds) == 0 || src == nil {
		return nil, 0, SourceCursor{}, nil
	}
	names := r.When.groupByNames()
	subjects := map[string]*condition{}
	subjectTuples := map[string][]string{}
	if (len(names) >= 2 || r.When.ForEach != "" || len(r.When.BroadcastEvents) > 0) && activeSubjects != nil {
		seededTuples, _, err := activeSubjects(r.ID, len(names))
		if err != nil {
			return nil, 0, SourceCursor{}, err
		}
		for key, tuple := range seededTuples {
			subjects[key] = &condition{}
			if len(names) >= 2 {
				subjectTuples[key] = tuple
			}
		}
	}
	winLo := windowStartMillis(r.When, now)
	winHi := now.UnixMilli()
	records, cursor, skippedLines, err := src.ReadWindow(ctx, winLo, kinds)
	if err != nil {
		return nil, 0, SourceCursor{}, err
	}
	firstLoadedSeq := cursor.Seq
	if resolveBaseline != nil {
		firstLoadedSeq = resolveBaseline(r.ID, cursor)
	}
	skipped := skippedLines
	for _, rec := range records {
		if rec.Seq <= firstLoadedSeq {
			continue
		}
		if rec.TS < winLo || rec.TS > winHi {
			continue
		}
		if !slices.Contains(kinds, rec.Kind) {
			continue
		}
		if err := observeRecord(r, names, rec, facts, winHi, subjects, subjectTuples, &skipped); err != nil {
			return nil, 0, SourceCursor{}, err
		}
	}
	return subjects, skipped, cursor, nil
}

func (r Rule) isProjectionPredicateMatch() bool {
	return r.When.Pattern == PredicateMatch && len(r.Reads.Events) == 0
}

type projectionSubjectFacts interface {
	subjectFacts
	candidateSubjects(r Rule) []string
	subjectSeq(subject string) int64
}

func deriveProjectionMatch(r Rule, facts subjectFacts, now time.Time) (map[string]*condition, error) {
	pf, ok := facts.(projectionSubjectFacts)
	if !ok {
		return nil, fmt.Errorf("projection-only predicate_match requires item projection subjects")
	}
	candidates := pf.candidateSubjects(r)
	subjects := map[string]*condition{}
	nowMillis := now.UnixMilli()
	for _, subject := range candidates {
		projection, inScope, err := pf.forSubject(subject)
		if err != nil {
			return nil, err
		}
		if !inScope {
			continue
		}
		if r.When.Match != nil && r.When.Match.eval(expressionValues{projection: projection, nowMillis: nowMillis}) {
			subjects[subject] = &condition{
				projectionMatch: true,
				subjectSeq:      pf.subjectSeq(subject),
				lag:             &lagCondition{count: 1},
			}
		}
	}
	return subjects, nil
}
