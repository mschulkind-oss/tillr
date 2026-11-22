package watch

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Observation is one normalized record read from an ObservationSource.
type Observation struct {
	Seq    int64          `json:"seq"`
	TS     int64          `json:"ts"`
	Kind   string         `json:"kind"`
	Fields map[string]any `json:"fields,omitempty"`
}

// Lookup resolves top-level observation fields ("seq", "ts", "kind") and
// dotted paths inside Fields.
func (o Observation) Lookup(name string) any {
	switch name {
	case "seq":
		return o.Seq
	case "ts":
		return o.TS
	case "kind":
		return o.Kind
	}
	var v any = o.Fields
	for _, key := range strings.Split(name, ".") {
		m, _ := v.(map[string]any)
		v = m[key]
	}
	return v
}

// SourceCursor tracks monotonic progress within an ObservationSource's current epoch.
type SourceCursor struct {
	Epoch      int64
	Seq        int64
	ByteOffset int64
	Dev        uint64
	Ino        uint64
}

// SkippedLines counts malformed input records skipped while reading a window.
type SkippedLines = int

// ObservationSource yields timestamped, monotonically numbered records and
// optional sidecar projections without mutating the system it reads.
type ObservationSource interface {
	ReadWindow(ctx context.Context, sinceMillis int64, kinds []string) ([]Observation, SourceCursor, SkippedLines, error)
	Project(ctx context.Context, names []string) (map[string]any, error)
	WatchTargets() (fsPaths []string, pollInterval time.Duration)
}

type subjectFacts interface {
	forSubject(subject string) (lookup func(projectionOperand) any, inScope bool, err error)
}

type ruleScopePreparer interface {
	prepareRule(ctx context.Context, r Rule, g gitSnapshot) (facts subjectFacts, outOfScope string, unsupported string, err error)
}

func prepareSourceRule(ctx context.Context, src ObservationSource, r Rule, g gitSnapshot) (subjectFacts, string, string, error) {
	if prep, ok := src.(ruleScopePreparer); ok {
		return prep.prepareRule(ctx, r, g)
	}
	switch r.Scope {
	case "", "*":
	default:
		if isTillrScope(r.Scope) {
			return nil, "", fmt.Sprintf("not evaluated: scope %q requires a tillr store source", r.Scope), nil
		}
		return nil, "", fmt.Sprintf("unsupported scope %q", r.Scope), nil
	}
	var customNames []string
	for _, proj := range r.Reads.Projections {
		switch {
		case proj == "git_refs" || strings.HasPrefix(proj, "git_refs:"):
		case proj == "items" || proj == "charter" || proj == "workflow_selections":
			return nil, "", fmt.Sprintf("not evaluated: projection %q requires a tillr store source", proj), nil
		default:
			customNames = append(customNames, proj)
		}
	}
	if err := g.ruleError(r); err != nil {
		return nil, "", "", err
	}
	var projected map[string]any
	if len(customNames) > 0 && src != nil {
		var err error
		projected, err = src.Project(ctx, customNames)
		if err != nil {
			return nil, "", "", err
		}
	}
	return gitRuleFacts{git: g, extra: projected}, "", "", nil
}

type memEpisode struct {
	alertID              string
	ruleID               string
	subjectKey           string
	episodeNum           int64
	state                string
	severity             string
	conditionFingerprint string
	openedSeq            int64
	openedAt             int64
	lastObservedSeq      int64
	lastObservedAt       int64
	observationCount     int64
	ackedBy              sql.NullString
	ackedNote            sql.NullString
	ackedAt              sql.NullInt64
	silenceUntilMS       sql.NullInt64
	silenceOnFingerprint sql.NullString
	resolvedSeq          sql.NullInt64
	resolvedAt           sql.NullInt64
	resolvedReason       sql.NullString
	bundleJSON           string
}

type memAlertEvent struct {
	seq      int64
	ts       int64
	kind     string
	objectID string
	payload  []byte
}

type memDeliveryCursor struct {
	seenSeq      int64
	shownSeq     int64
	waitingSince sql.NullInt64
	lastWaitAt   sql.NullInt64
}

type memoryState struct {
	mu            sync.Mutex
	git           gitCache
	nextSeq       int64
	lastSourceSeq int64
	episodes      []*memEpisode
	events        []memAlertEvent
	owners        map[string]*memDeliveryCursor
}

func newMemoryState() *memoryState {
	return &memoryState{
		owners: map[string]*memDeliveryCursor{},
	}
}

func (m *memoryState) appendEventLocked(kind, alertID string, now time.Time, body map[string]any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	m.nextSeq++
	m.events = append(m.events, memAlertEvent{
		seq:      m.nextSeq,
		ts:       now.UnixMilli(),
		kind:     kind,
		objectID: alertID,
		payload:  payload,
	})
	return nil
}

func (m *memoryState) activeCompositeSubjects(ruleID string, n int) (map[string][]string, []string, error) {
	if m == nil {
		return nil, nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tuples := map[string][]string{}
	var keys []string
	for _, ep := range m.episodes {
		if ep.ruleID == ruleID && ep.state != "resolved" {
			if tuple, ok := decodeCompositeSubject(ep.subjectKey, n); ok {
				tuples[ep.subjectKey] = tuple
				keys = append(keys, ep.subjectKey)
			}
		}
	}
	slices.Sort(keys)
	return tuples, keys, nil
}

func (m *memoryState) activeLocked(ruleID string) map[string]episode {
	out := map[string]episode{}
	for _, ep := range m.episodes {
		if ep.ruleID == ruleID && ep.state != "resolved" {
			out[ep.subjectKey] = episode{
				id:           ep.alertID,
				state:        ep.state,
				opened:       ep.openedSeq,
				last:         ep.lastObservedSeq,
				silenceUntil: ep.silenceUntilMS,
				silenceOn:    ep.silenceOnFingerprint,
			}
		}
	}
	return out
}

func (m *memoryState) lastEpisodeLocked(ruleID, subjectKey string) (int64, string, int64) {
	var best *memEpisode
	for _, ep := range m.episodes {
		if ep.ruleID == ruleID && ep.subjectKey == subjectKey {
			if best == nil || ep.episodeNum > best.episodeNum {
				best = ep
			}
		}
	}
	if best == nil {
		return 0, "", 0
	}
	return best.episodeNum, best.resolvedReason.String, best.resolvedSeq.Int64
}

func (m *memoryState) findAlertLocked(id string) *memEpisode {
	for _, ep := range m.episodes {
		if ep.alertID == id {
			return ep
		}
	}
	return nil
}

func (m *memoryState) pending(owner string, fileRules []Rule) ([]Notice, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fg := foregroundRules(fileRules)
	var seen int64
	if cur := m.owners[owner]; cur != nil {
		seen = cur.seenSeq
	}
	type candidate struct {
		seq    int64
		notice Notice
	}
	var list []candidate
	for _, ep := range m.episodes {
		if ep.state != "open" || !fg[ep.ruleID] {
			continue
		}
		var latestSeq int64
		for _, ev := range m.events {
			if ev.objectID == ep.alertID && (ev.kind == openedKind || ev.kind == reopenedKind) && ev.seq > latestSeq {
				latestSeq = ev.seq
			}
		}
		if latestSeq <= seen {
			continue
		}
		var b Bundle
		if err := json.Unmarshal([]byte(ep.bundleJSON), &b); err != nil {
			return nil, err
		}
		list = append(list, candidate{
			seq: latestSeq,
			notice: Notice{
				Seq:     latestSeq,
				AlertID: ep.alertID,
				Bundle:  b,
			},
		})
	}
	slices.SortFunc(list, func(a, b candidate) int {
		if a.seq < b.seq {
			return -1
		}
		if a.seq > b.seq {
			return 1
		}
		return 0
	})
	notices := make([]Notice, len(list))
	for i, c := range list {
		notices[i] = c.notice
	}
	return notices, nil
}

func (m *memoryState) seen(owner string, seq int64) error {
	if seq < 0 {
		return fmt.Errorf("seen cursor must be nonnegative")
	}
	if m == nil {
		if seq > 0 {
			return fmt.Errorf("seen cursor %d exceeds shown_seq 0 for %q", seq, owner)
		}
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var shown, curSeen int64
	if cur := m.owners[owner]; cur != nil {
		shown, curSeen = cur.shownSeq, cur.seenSeq
	}
	if seq > shown {
		return fmt.Errorf("seen cursor %d exceeds shown_seq %d for %q", seq, shown, owner)
	}
	if seq <= curSeen {
		return nil
	}
	m.owners[owner].seenSeq = seq
	return nil
}

func (m *memoryState) shown(owner string, seq int64) error {
	if m == nil {
		return fmt.Errorf("watch: cannot record shown cursor before evaluation")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.owners[owner]
	if cur == nil {
		cur = &memDeliveryCursor{}
		m.owners[owner] = cur
	}
	if seq > cur.shownSeq {
		cur.shownSeq = seq
	}
	return nil
}

func (m *memoryState) beginWait(owner string, now time.Time) time.Time {
	if m == nil {
		return now
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.owners[owner]
	if cur == nil {
		cur = &memDeliveryCursor{}
		m.owners[owner] = cur
	}
	ms := now.UnixMilli()
	cur.waitingSince = sql.NullInt64{Int64: ms, Valid: true}
	cur.lastWaitAt = sql.NullInt64{Int64: ms, Valid: true}
	return now
}

func (m *memoryState) endWait(owner string, start time.Time, now time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.owners[owner]
	if cur != nil && cur.waitingSince.Valid && cur.waitingSince.Int64 == start.UnixMilli() {
		cur.waitingSince = sql.NullInt64{}
		cur.lastWaitAt = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
	}
}

func (m *memoryState) readStatusInto(s *Status, rules []Rule) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	activeEps := make([]*memEpisode, 0, len(m.episodes))
	for _, ep := range m.episodes {
		if ep.state != "resolved" {
			activeEps = append(activeEps, ep)
		}
	}
	slices.SortFunc(activeEps, func(a, b *memEpisode) int {
		if a.openedSeq != b.openedSeq {
			if a.openedSeq < b.openedSeq {
				return -1
			}
			return 1
		}
		return strings.Compare(a.alertID, b.alertID)
	})
	for _, ep := range activeEps {
		alert := AlertStatus{
			ID:       ep.alertID,
			RuleID:   ep.ruleID,
			Subject:  ep.subjectKey,
			State:    ep.state,
			Severity: ep.severity,
			Opened:   statusTime(ep.openedAt),
			Observed: ep.observationCount,
		}
		switch ep.state {
		case "acknowledged":
			if ep.ackedBy.Valid {
				by := ep.ackedBy.String
				alert.By = &by
			}
		case "silenced":
			alert.Until = "change"
			if ep.silenceUntilMS.Valid {
				alert.Until = statusTime(ep.silenceUntilMS.Int64)
			}
		}
		s.Alerts = append(s.Alerts, alert)
	}
	fg := foregroundRules(rules)
	for _, handle := range slices.Sorted(maps.Keys(m.owners)) {
		cur := m.owners[handle]
		owner := OwnerStatus{Owner: handle}
		if cur.waitingSince.Valid {
			v := statusTime(cur.waitingSince.Int64)
			owner.Since = &v
		}
		if cur.lastWaitAt.Valid {
			v := statusTime(cur.lastWaitAt.Int64)
			owner.LastWait = &v
		}
		for _, ep := range m.episodes {
			if ep.state != "open" || !fg[ep.ruleID] {
				continue
			}
			for _, ev := range m.events {
				if ev.objectID == ep.alertID && (ev.kind == openedKind || ev.kind == reopenedKind) && ev.seq > cur.seenSeq {
					owner.Pending++
					break
				}
			}
		}
		s.Owners = append(s.Owners, owner)
	}
}

func isSuppressiveResolutionReason(reason string) bool {
	return reason == "manual" || strings.HasPrefix(reason, "item-")
}

type itemTerminalChecker struct {
	ready      bool
	stateOf    func(itemID string) (string, bool, error)
	isTerminal func(state string) bool
}

func (c itemTerminalChecker) reason(rule *Rule, subject string) (string, error) {
	if !c.ready || subject == "" || subject == "global" {
		return "", nil
	}
	if rule != nil {
		var candidate string
		if len(rule.When.GroupByList) >= 2 {
			tuple, ok := decodeCompositeSubject(subject, len(rule.When.GroupByList))
			if !ok || tuple[0] == "" || tuple[0] == "global" {
				return "", nil
			}
			candidate = tuple[0]
		} else {
			if rule.When.GroupBy == "global" {
				return "", nil
			}
			candidate = subject
		}
		state, exists, err := c.stateOf(candidate)
		if err != nil || !exists || !c.isTerminal(state) {
			return "", err
		}
		return "item-" + state, nil
	}
	state, exists, err := c.stateOf(subject)
	if err != nil {
		return "", err
	}
	if exists {
		if c.isTerminal(state) {
			return "item-" + state, nil
		}
		return "", nil
	}
	if strings.Contains(subject, "/") {
		seg, _, _ := strings.Cut(subject, "/")
		if u, ok := unescapeSubjectPart(seg); ok && u != "" && u != "global" {
			state, exists, err := c.stateOf(u)
			if err != nil || !exists || !c.isTerminal(state) {
				return "", err
			}
			return "item-" + state, nil
		}
	}
	return "", nil
}

func newSourceItemTerminal(ctx context.Context, src ObservationSource) itemTerminalChecker {
	if src == nil {
		return itemTerminalChecker{}
	}
	projected, err := src.Project(ctx, []string{"charter", "items"})
	if err != nil || projected == nil {
		return itemTerminalChecker{}
	}
	closed, _ := projected["charter.closed"].(string)
	rawFinished := projected["charter.finished"]
	if cm, ok := projected["charter"].(map[string]any); ok {
		if closed == "" {
			closed, _ = cm["closed"].(string)
		}
		if rawFinished == nil {
			rawFinished = cm["finished"]
		}
	}
	if closed == "" {
		return itemTerminalChecker{}
	}
	rawItems, ok := projected["items"]
	if !ok || rawItems == nil {
		return itemTerminalChecker{}
	}
	itemStates := map[string]string{}
	switch items := rawItems.(type) {
	case map[string]map[string]any:
		for id, entry := range items {
			if s, ok := entry["item.state"].(string); ok && s != "" {
				itemStates[id] = s
			} else if s, ok := entry["state"].(string); ok && s != "" {
				itemStates[id] = s
			}
		}
	case map[string]any:
		for id, v := range items {
			switch entry := v.(type) {
			case string:
				if entry != "" {
					itemStates[id] = entry
				}
			case map[string]any:
				if s, ok := entry["item.state"].(string); ok && s != "" {
					itemStates[id] = s
				} else if s, ok := entry["state"].(string); ok && s != "" {
					itemStates[id] = s
				}
			}
		}
	default:
		return itemTerminalChecker{}
	}
	terminal := map[string]bool{closed: true}
	switch list := rawFinished.(type) {
	case []any:
		for _, elem := range list {
			if s, ok := elem.(string); ok && s != "" {
				terminal[s] = true
			}
		}
	case []string:
		for _, s := range list {
			if s != "" {
				terminal[s] = true
			}
		}
	}
	return itemTerminalChecker{
		ready: true,
		stateOf: func(itemID string) (string, bool, error) {
			s, ok := itemStates[itemID]
			return s, ok, nil
		},
		isTerminal: func(state string) bool {
			return state != "" && terminal[state]
		},
	}
}

func (m *memoryState) resolveEpisodeLocked(memEp *memEpisode, ruleID, subjectKey, severity, receiver string, seq int64, now time.Time, reason string) error {
	memEp.state = "resolved"
	memEp.resolvedSeq = sql.NullInt64{Int64: seq, Valid: true}
	memEp.resolvedAt = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
	memEp.resolvedReason = sql.NullString{String: reason, Valid: true}
	return m.appendEventLocked(resolvedKind, memEp.alertID, now, map[string]any{
		"alert_id": memEp.alertID, "rule_id": ruleID, "subject_key": subjectKey,
		"severity": severity, "receiver": receiver,
		"resolved_seq": seq, "reason": reason,
	})
}

func (m *memoryState) reconcileRule(r Rule, subjects map[string]*condition, startHead int64, now time.Time, lookup itemTerminalChecker) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if startHead > m.lastSourceSeq {
		m.lastSourceSeq = startHead
	}
	headSeq := startHead
	if headSeq == 0 {
		headSeq = m.lastSourceSeq
	}
	episodes := m.activeLocked(r.ID)
	keys := make([]string, 0, len(subjects)+len(episodes))
	for key := range subjects {
		keys = append(keys, key)
	}
	for key := range episodes {
		if _, ok := subjects[key]; !ok {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		c := subjects[key]
		if c == nil {
			c = &condition{}
		}
		ep, exists := episodes[key]
		termReason, err := lookup.reason(&r, key)
		if err != nil {
			return err
		}
		if termReason != "" {
			if exists {
				memEp := m.findAlertLocked(ep.id)
				if err := m.resolveEpisodeLocked(memEp, r.ID, key, memEp.severity, r.Delivery.Receiver, headSeq, now, termReason); err != nil {
					return err
				}
			}
			continue
		}
		firing := r.Enabled && c.fires(r.When)
		firstClear := sort.Search(len(c.clears), func(i int) bool { return c.clears[i] > ep.opened })
		if exists && (!firing || firstClear < len(c.clears)) {
			seq := startHead
			if firstClear < len(c.clears) {
				seq = c.clears[firstClear]
			}
			memEp := m.findAlertLocked(ep.id)
			if err := m.resolveEpisodeLocked(memEp, r.ID, key, r.Severity, r.Delivery.Receiver, seq, now, "condition-cleared"); err != nil {
				return err
			}
			exists = false
		}
		if !firing {
			continue
		}
		newest := observation{Seq: startHead}
		if r.When.Pattern != LagBehind {
			newest = c.events[len(c.events)-1]
		}
		if !exists {
			previous, reason, resolvedSeq := m.lastEpisodeLocked(r.ID, key)
			if isSuppressiveResolutionReason(reason) && newest.Seq <= resolvedSeq {
				continue
			}
			var randomID [16]byte
			rand.Read(randomID[:])
			id := "alrt-" + hex.EncodeToString(randomID[:])
			bundle, err := makeBundle(r, key, id, c)
			if err != nil {
				return err
			}
			m.episodes = append(m.episodes, &memEpisode{
				alertID:              id,
				ruleID:               r.ID,
				subjectKey:           key,
				episodeNum:           previous + 1,
				state:                "open",
				severity:             r.Severity,
				conditionFingerprint: newest.fingerprint,
				openedSeq:            newest.Seq,
				openedAt:             now.UnixMilli(),
				lastObservedSeq:      newest.Seq,
				lastObservedAt:       now.UnixMilli(),
				observationCount:     1,
				bundleJSON:           string(bundle),
			})
			if err := m.appendEventLocked(openedKind, id, now, map[string]any{
				"alert_id": id, "rule_id": r.ID, "subject_key": key,
				"severity": r.Severity, "receiver": r.Delivery.Receiver,
				"opened_seq": newest.Seq,
			}); err != nil {
				return err
			}
			continue
		}
		memEp := m.findAlertLocked(ep.id)
		if r.When.Pattern != LagBehind && newest.Seq > ep.last {
			memEp.lastObservedSeq = newest.Seq
			memEp.lastObservedAt = now.UnixMilli()
			memEp.observationCount++
			memEp.conditionFingerprint = newest.fingerprint
		}
		if cause := ep.reopenCause(now, newest.fingerprint); cause != "" {
			bundle, err := makeBundle(r, key, ep.id, c)
			if err != nil {
				return err
			}
			memEp.state = "open"
			memEp.silenceUntilMS = sql.NullInt64{}
			memEp.silenceOnFingerprint = sql.NullString{}
			memEp.ackedBy = sql.NullString{}
			memEp.ackedNote = sql.NullString{}
			memEp.ackedAt = sql.NullInt64{}
			memEp.conditionFingerprint = newest.fingerprint
			memEp.bundleJSON = string(bundle)
			if err := m.appendEventLocked(reopenedKind, ep.id, now, map[string]any{
				"alert_id": ep.id, "rule_id": r.ID, "subject_key": key,
				"severity": r.Severity, "receiver": r.Delivery.Receiver,
				"cause": cause,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *memoryState) resolveTerminalRemaining(ruleByID map[string]*Rule, reconciled map[string]bool, startHead int64, now time.Time, lookup itemTerminalChecker) error {
	if m == nil || !lookup.ready {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if startHead > m.lastSourceSeq {
		m.lastSourceSeq = startHead
	}
	headSeq := startHead
	if headSeq == 0 {
		headSeq = m.lastSourceSeq
	}
	for _, ep := range m.episodes {
		if ep.state == "resolved" || reconciled[ep.ruleID] {
			continue
		}
		rPtr := ruleByID[ep.ruleID]
		termReason, err := lookup.reason(rPtr, ep.subjectKey)
		if err != nil {
			return err
		}
		if termReason == "" {
			continue
		}
		receiver := ""
		if rPtr != nil {
			receiver = rPtr.Delivery.Receiver
		}
		if receiver == "" {
			for i := len(m.events) - 1; i >= 0; i-- {
				ev := m.events[i]
				if ev.objectID == ep.alertID && ev.kind == openedKind {
					var body struct {
						Receiver string `json:"receiver"`
					}
					if json.Unmarshal(ev.payload, &body) == nil && body.Receiver != "" {
						receiver = body.Receiver
					}
					break
				}
			}
			if receiver == "" {
				receiver = "foreground"
			}
		}
		if err := m.resolveEpisodeLocked(ep, ep.ruleID, ep.subjectKey, ep.severity, receiver, headSeq, now, termReason); err != nil {
			return err
		}
	}
	return nil
}

func (m *memoryState) readEpisodeActionLocked(id string) (*memEpisode, episodeAction, error) {
	if m == nil {
		return nil, episodeAction{}, fmt.Errorf("alert %s %w", id, ErrAlertNotFound)
	}
	ep := m.findAlertLocked(id)
	if ep == nil {
		return nil, episodeAction{}, fmt.Errorf("alert %s %w", id, ErrAlertNotFound)
	}
	return ep, episodeAction{
		state:       ep.state,
		fingerprint: ep.conditionFingerprint,
		ackedBy:     ep.ackedBy,
		payload: map[string]any{
			"alert_id": id, "rule_id": ep.ruleID, "subject_key": ep.subjectKey, "severity": ep.severity,
		},
	}, nil
}

func (m *memoryState) refuseEpisode(id string, reason error) error {
	if m == nil {
		return fmt.Errorf("alert %s %w", id, ErrAlertNotFound)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, a, err := m.readEpisodeActionLocked(id)
	if err != nil {
		return err
	}
	return fmt.Errorf("%s: %w", a.refuse(id), reason)
}

func isTillrScope(scope string) bool {
	if scope == "core" {
		return true
	}
	prefix, name, ok := strings.Cut(scope, ":")
	return ok && name != "" && (prefix == "charter" || prefix == "workflow")
}

func (m *memoryState) act(id, kind string, now time.Time, change func(*memEpisode, episodeAction) error) error {
	if m == nil {
		return fmt.Errorf("alert %s %w", id, ErrAlertNotFound)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ep, a, err := m.readEpisodeActionLocked(id)
	if err != nil {
		return err
	}
	if err := change(ep, a); err != nil {
		return err
	}
	return m.appendEventLocked(kind, id, now, a.payload)
}
