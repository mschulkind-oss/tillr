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

type observationFacts interface {
	forObservation(subject string, eventLookup func(string) any) (lookup func(projectionOperand) any, inScope bool, err error)
}

type ruleScopePreparer interface {
	prepareRule(ctx context.Context, r Rule, g gitSnapshot, ancestors fixerAncestorFacts) (facts subjectFacts, outOfScope string, unsupported string, err error)
}

func prepareSourceRule(ctx context.Context, src ObservationSource, r Rule, g gitSnapshot, ancestors fixerAncestorFacts) (subjectFacts, string, string, error) {
	if prep, ok := src.(ruleScopePreparer); ok {
		return prep.prepareRule(ctx, r, g, ancestors)
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
		case proj == "git_refs" || strings.HasPrefix(proj, "git_refs:") || strings.HasPrefix(proj, "green_proof:"):
		case proj == "items" || proj == "charter" || proj == "workflow_selections" || proj == "fixer_checks":
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
	alertID               string
	ruleID                string
	subjectKey            string
	episodeNum            int64
	state                 string
	severity              string
	conditionFingerprint  string
	openedSeq             int64
	openedAt              int64
	lastObservedSeq       int64
	lastObservedAt        int64
	observationCount      int64
	ackedBy               sql.NullString
	ackedNote             sql.NullString
	ackedAt               sql.NullInt64
	ackedSeq              sql.NullInt64
	ackedFingerprint      sql.NullString
	ackedLagPair          sql.NullString
	silenceUntilMS        sql.NullInt64
	silenceOnFingerprint  sql.NullString
	silenceID             sql.NullString
	trackingItemID        sql.NullString
	trackedLagPair        sql.NullString
	resolvedSeq           sql.NullInt64
	resolvedAt            sql.NullInt64
	resolvedReason        sql.NullString
	bundleJSON            string
	variablesJSON         string
	settlementState       string
	investigatedBy        sql.NullString
	investigatedAt        sql.NullInt64
	investigationCause    sql.NullString
	investigationEvidence sql.NullString
	outcome               sql.NullString
	itemLinkKind          sql.NullString
	settledBy             sql.NullString
	settledAt             sql.NullInt64
	settledSeq            sql.NullInt64
	settledFingerprint    sql.NullString
	settledLagPair        sql.NullString
	reopenCountForItem    int64
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
	hasEpoch      bool
	epoch         int64
	baselines     map[string]ruleBaseline
	episodes      []*memEpisode
	events        []memAlertEvent
	silences      []*SilenceRecord
	owners        map[string]*memDeliveryCursor
}

func newMemoryState() *memoryState {
	return &memoryState{
		baselines: map[string]ruleBaseline{},
		owners:    map[string]*memDeliveryCursor{},
	}
}

func (m *memoryState) resolveFirstLoadedSeq(ruleID string, cursor SourceCursor) int64 {
	if m == nil {
		return cursor.Seq
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasEpoch || m.epoch != cursor.Epoch {
		return cursor.Seq
	}
	if b, ok := m.baselines[ruleID]; ok {
		return b.firstLoadedSeq
	}
	return cursor.Seq
}

func (m *memoryState) prepareEpoch(hasCursor bool, epoch int64) map[string]ruleBaseline {
	m.mu.Lock()
	defer m.mu.Unlock()
	if hasCursor {
		if m.hasEpoch && m.epoch != epoch {
			m.baselines = map[string]ruleBaseline{}
		}
		m.hasEpoch = true
		m.epoch = epoch
	}
	snap := make(map[string]ruleBaseline, len(m.baselines))
	maps.Copy(snap, m.baselines)
	return snap
}

func (m *memoryState) updateBaselines(rules []Rule, results []RuleResult, fullRules []Rule, live bool, startHead int64, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	nowMS := now.UnixMilli()
	seen := make(map[string]bool, len(rules))
	for i, r := range rules {
		seen[r.ID] = true
		res := results[i]
		if !res.Disabled && res.Diagnostic() == "" {
			hash := definitionHash(r)
			row, exists := m.baselines[r.ID]
			if !exists {
				m.baselines[r.ID] = ruleBaseline{
					ruleID:         r.ID,
					definitionHash: hash,
					enabled:        true,
					firstLoadedSeq: startHead,
					firstLoadedAt:  nowMS,
					loadedSeq:      startHead,
					loadedAt:       nowMS,
				}
			} else if !row.enabled || row.definitionHash != hash {
				row.definitionHash = hash
				row.enabled = true
				row.loadedSeq = startHead
				row.loadedAt = nowMS
				m.baselines[r.ID] = row
			}
		} else if !r.Enabled {
			if row, exists := m.baselines[r.ID]; exists && row.enabled {
				row.enabled = false
				m.baselines[r.ID] = row
			}
		}
	}
	if !live {
		for _, r := range fullRules {
			if seen[r.ID] || r.Enabled {
				continue
			}
			if row, exists := m.baselines[r.ID]; exists && row.enabled {
				row.enabled = false
				m.baselines[r.ID] = row
			}
		}
		return
	}
	for id, row := range m.baselines {
		if !seen[id] && row.enabled {
			row.enabled = false
			m.baselines[id] = row
		}
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
			if tuple, ok := decodeSubjectKey(ep.subjectKey, n); ok {
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
			ackedWatermark := ep.lastObservedSeq
			if ep.ackedSeq.Valid {
				ackedWatermark = ep.ackedSeq.Int64
			} else {
				for _, ev := range m.events {
					if ev.objectID == ep.alertID && (ev.kind == acknowledgedKind || ev.kind == trackedKind) && ev.seq > ackedWatermark {
						ackedWatermark = ev.seq
					}
				}
			}
			settlementState := ep.settlementState
			if settlementState == "" {
				settlementState = "uninvestigated"
			}
			out[ep.subjectKey] = episode{
				id:                   ep.alertID,
				state:                ep.state,
				severity:             ep.severity,
				opened:               ep.openedSeq,
				last:                 ep.lastObservedSeq,
				conditionFingerprint: ep.conditionFingerprint,
				silenceUntil:         ep.silenceUntilMS,
				silenceOn:            ep.silenceOnFingerprint,
				silenceID:            ep.silenceID,
				variablesJSON:        ep.variablesJSON,
				trackingItem:         ep.trackingItemID,
				trackedLagPair:       ep.trackedLagPair,
				ackedSeq:             ep.ackedSeq,
				ackedFingerprint:     ep.ackedFingerprint,
				ackedLagPair:         ep.ackedLagPair,
				ackedWatermark:       ackedWatermark,
				hasAckCols:           true,
				settlementState:      settlementState,
				settledSeq:           ep.settledSeq,
				settledFingerprint:   ep.settledFingerprint,
				settledLagPair:       ep.settledLagPair,
				reopenCountForItem:   ep.reopenCountForItem,
				hasSettlementCols:    true,
			}
		}
	}
	return out
}

func (m *memoryState) lastEpisodeLocked(ruleID, subjectKey string) (int64, string, int64, int64) {
	var best *memEpisode
	for _, ep := range m.episodes {
		if ep.ruleID == ruleID && ep.subjectKey == subjectKey {
			if best == nil || ep.episodeNum > best.episodeNum {
				best = ep
			}
		}
	}
	if best == nil {
		return 0, "", 0, 0
	}
	return best.episodeNum, best.resolvedReason.String, best.resolvedSeq.Int64, max(best.openedAt, best.resolvedAt.Int64)
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
			Vars:     decodeVariablesJSON(ep.variablesJSON),
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
		if ep.trackingItemID.Valid && ep.trackingItemID.String != "" {
			v := ep.trackingItemID.String
			alert.ItemID = &v
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

func isSuppressiveResolutionReason(w When, reason string) bool {
	if reason == "manual" {
		return true
	}
	return !w.KeepOnTerminalItem && strings.HasPrefix(reason, "item-")
}

func isWindowedEventRule(w When) bool {
	return w.Window > 0 && w.Pattern != LagBehind && w.Pattern != DurationWithoutEvent
}

func conditionNewest(r Rule, c *condition, fallbackSeq int64) observation {
	if r.When.Pattern == LagBehind {
		return observation{Seq: fallbackSeq}
	}
	if r.isProjectionPredicateMatch() {
		if c.subjectSeq > 0 {
			return observation{Seq: c.subjectSeq}
		}
		return observation{Seq: fallbackSeq}
	}
	return c.events[len(c.events)-1]
}

type itemTerminalChecker struct {
	ready       bool
	closedState string
	stateOf     func(itemID string) (string, bool, error)
	seqOf       func(itemID string) int64
	isTerminal  func(state string) bool
}

func (c itemTerminalChecker) terminalSeq(itemID string) int64 {
	if c.seqOf == nil {
		return 0
	}
	return c.seqOf(itemID)
}

func (c itemTerminalChecker) reopenCause(state string, exists bool) (string, string) {
	if !exists {
		return "recurred-after-item-missing", "missing"
	}
	return "recurred-after-item-" + state, state
}

func validateSourceSettleItem(ctx context.Context, src ObservationSource, a episodeAction, id, itemID string) error {
	lookup := newSourceItemTerminal(ctx, src)
	if !lookup.ready {
		// Standalone mode without items/charter projections accepts any non-empty reference (D-AW10).
		return nil
	}
	state, exists, err := lookup.stateOf(itemID)
	if err != nil {
		return fmt.Errorf("%w: %w", a.refuse(id), err)
	}
	if !exists {
		return fmt.Errorf("%w: item %s not found", a.refuse(id), itemID)
	}
	if state == lookup.closedState {
		return fmt.Errorf("%w: item %s is %s; cannot settle with a closed item", a.refuse(id), itemID, state)
	}
	if lookup.isTerminal(state) {
		itemSeq := lookup.terminalSeq(itemID)
		if itemSeq <= a.lastObservedSeq {
			return fmt.Errorf("%w: item %s is %s (seq %d <= last observed seq %d); file a new item", a.refuse(id), itemID, state, itemSeq, a.lastObservedSeq)
		}
	}
	return nil
}

func (c itemTerminalChecker) reason(rule *Rule, subject string) (string, error) {
	if !c.ready || subject == "" || subject == "global" {
		return "", nil
	}
	if rule != nil {
		if rule.When.KeepOnTerminalItem {
			return "", nil
		}
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
	itemSeqs := map[string]int64{}
	extractSeq := func(entry map[string]any) int64 {
		if seq, ok := integer(entry["item.event_seq"]); ok && seq > 0 {
			return seq
		}
		if seq, ok := integer(entry["event_seq"]); ok && seq > 0 {
			return seq
		}
		return 0
	}
	switch items := rawItems.(type) {
	case map[string]map[string]any:
		for id, entry := range items {
			if s, ok := entry["item.state"].(string); ok && s != "" {
				itemStates[id] = s
			} else if s, ok := entry["state"].(string); ok && s != "" {
				itemStates[id] = s
			}
			if seq := extractSeq(entry); seq > 0 {
				itemSeqs[id] = seq
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
				if seq := extractSeq(entry); seq > 0 {
					itemSeqs[id] = seq
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
		ready:       true,
		closedState: closed,
		stateOf: func(itemID string) (string, bool, error) {
			s, ok := itemStates[itemID]
			return s, ok, nil
		},
		seqOf: func(itemID string) int64 {
			return itemSeqs[itemID]
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
	body := map[string]any{
		"alert_id": memEp.alertID, "rule_id": ruleID, "subject_key": subjectKey,
		"severity": severity, "receiver": receiver,
		"resolved_seq": seq, "reason": reason,
	}
	if memEp.trackingItemID.Valid && memEp.trackingItemID.String != "" {
		body["item_id"] = memEp.trackingItemID.String
	}
	return m.appendEventLocked(resolvedKind, memEp.alertID, now, body)
}

func (m *memoryState) reconcileRule(r Rule, subjects map[string]*condition, startHead, loadedSeq int64, now time.Time, lookup itemTerminalChecker, silences silenceEvalState) error {
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
		firstClear := sort.Search(len(c.clears), func(i int) bool { return c.clears[i] > ep.last })
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
		newest := conditionNewest(r, c, startHead)
		if !exists {
			if r.When.Pattern != LagBehind && !r.isProjectionPredicateMatch() && newest.Seq <= loadedSeq {
				continue
			}
			previous, reason, resolvedSeq, lastEpTime := m.lastEpisodeLocked(r.ID, key)
			if isSuppressiveResolutionReason(r.When, reason) && newest.Seq <= resolvedSeq {
				continue
			}
			var randomID [16]byte
			rand.Read(randomID[:])
			id := "alrt-" + hex.EncodeToString(randomID[:])
			bundle, err := makeBundle(r, key, id, c)
			if err != nil {
				return err
			}
			var lagPair sql.NullString
			if r.When.Pattern == LagBehind {
				lagPair = sql.NullString{String: c.lag.leaderOID + ":" + c.lag.followerOID, Valid: true}
			}
			varsJSON := encodeVariablesJSON(extractVariables(r, key, c))
			sil := silences.matchActive(r.ID, varsJSON)
			memEp := &memEpisode{
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
				trackedLagPair:       lagPair,
				bundleJSON:           string(bundle),
				variablesJSON:        varsJSON,
				settlementState:      "uninvestigated",
			}
			openedBody := map[string]any{
				"alert_id": id, "rule_id": r.ID, "subject_key": key,
				"severity": r.Severity, "receiver": r.Delivery.Receiver,
				"opened_seq": newest.Seq,
			}
			if sil != nil {
				memEp.state = "silenced"
				memEp.silenceID = sql.NullString{String: sil.SilenceID, Valid: true}
				if sil.SilenceUntilMS != nil {
					memEp.silenceUntilMS = sql.NullInt64{Int64: *sil.SilenceUntilMS, Valid: true}
				}
				if sil.SilenceOnFingerprint != "" {
					memEp.silenceOnFingerprint = sql.NullString{String: sil.SilenceOnFingerprint, Valid: true}
				}
				memEp.trackingItemID = sql.NullString{String: sil.TrackingItemID, Valid: true}
				memEp.settlementState = "settled"
				memEp.investigatedBy = sql.NullString{String: sil.CreatedBy, Valid: true}
				memEp.investigatedAt = sql.NullInt64{Int64: sil.CreatedAt, Valid: true}
				memEp.investigationCause = sql.NullString{String: sil.InvestigationCause, Valid: true}
				memEp.investigationEvidence = sql.NullString{String: sil.InvestigationEvidence, Valid: true}
				memEp.outcome = sql.NullString{String: "silence", Valid: true}
				memEp.settledBy = sql.NullString{String: sil.CreatedBy, Valid: true}
				memEp.settledAt = sql.NullInt64{Int64: sil.CreatedAt, Valid: true}
				memEp.settledSeq = sql.NullInt64{Int64: newest.Seq, Valid: true}
				memEp.settledFingerprint = sql.NullString{String: newest.fingerprint, Valid: true}
				memEp.settledLagPair = lagPair
				openedBody["state"] = "silenced"
				openedBody["silence_id"] = sil.SilenceID
			} else if liftCause := silences.recentItemLiftCause(r.ID, varsJSON, lastEpTime); liftCause != "" {
				openedBody["cause"] = liftCause
			}
			m.episodes = append(m.episodes, memEp)
			if err := m.appendEventLocked(openedKind, id, now, openedBody); err != nil {
				return err
			}
			continue
		}
		memEp := m.findAlertLocked(ep.id)
		var currentLagPair string
		if r.When.Pattern == LagBehind {
			currentLagPair = c.lag.leaderOID + ":" + c.lag.followerOID
			watermark := ep.ackedWatermark
			if ep.settlementState == "settled" && ep.settledSeq.Valid {
				watermark = max(watermark, ep.settledSeq.Int64)
			}
			if ep.state != "silenced" && (ep.state == "acknowledged" || ep.settlementState == "settled") && r.When.ReopenWhen != nil &&
				c.lag != nil && c.lag.reopenEvent != nil &&
				c.lag.reopenEvent.Seq > max(watermark, loadedSeq) {
				nextSeq := max(ep.last, max(startHead, c.lag.reopenEvent.Seq))
				if ep.settlementState == "settled" && ep.trackingItem.Valid && ep.trackingItem.String != "" && lookup.ready {
					trackedID := ep.trackingItem.String
					itemState, itemExists, err := lookup.stateOf(trackedID)
					if err == nil && itemExists && !lookup.isTerminal(itemState) {
						if err := m.reopenLagWhenSettledOpenItemLocked(memEp, r, key, c, currentLagPair, nextSeq, trackedID, now); err != nil {
							return err
						}
						continue
					}
					if err == nil {
						recCause, recState := lookup.reopenCause(itemState, itemExists)
						if err := m.reopenSettledTerminalLocked(memEp, r, key, c, newest.fingerprint, currentLagPair, nextSeq, recCause, trackedID, recState, now); err != nil {
							return err
						}
						continue
					}
				}
				if err := m.reopenLagWhenLocked(memEp, r, key, c, currentLagPair, nextSeq, "", now); err != nil {
					return err
				}
				continue
			}
			if !memEp.trackedLagPair.Valid {
				memEp.trackedLagPair = sql.NullString{String: currentLagPair, Valid: true}
			} else if memEp.trackedLagPair.String != currentLagPair {
				memEp.trackedLagPair = sql.NullString{String: currentLagPair, Valid: true}
				memEp.lastObservedSeq = max(ep.last, startHead)
				memEp.lastObservedAt = now.UnixMilli()
				memEp.observationCount++
			}
		}
		if r.When.Pattern != LagBehind {
			newest = conditionNewest(r, c, ep.last)
			if newest.Seq > ep.last {
				bundle, err := makeBundle(r, key, ep.id, c)
				if err != nil {
					return err
				}
				memEp.lastObservedSeq = newest.Seq
				memEp.lastObservedAt = now.UnixMilli()
				memEp.observationCount++
				memEp.conditionFingerprint = newest.fingerprint
				memEp.bundleJSON = string(bundle)
				memEp.variablesJSON = encodeVariablesJSON(extractVariables(r, key, c))
			}
		}
		if cause := ep.reopenCause(r, now, newest, currentLagPair, silences); cause != "" {
			if ep.state != "silenced" && ep.settlementState == "settled" && ep.trackingItem.Valid && ep.trackingItem.String != "" && lookup.ready {
				trackedID := ep.trackingItem.String
				itemState, itemExists, err := lookup.stateOf(trackedID)
				if err == nil && itemExists && !lookup.isTerminal(itemState) {
					if err := m.reopenSettledOpenItemLocked(memEp, r, key, c, newest.fingerprint, currentLagPair, cause, trackedID, now); err != nil {
						return err
					}
					continue
				}
				if err == nil {
					recCause, recState := lookup.reopenCause(itemState, itemExists)
					if err := m.reopenSettledTerminalLocked(memEp, r, key, c, newest.fingerprint, currentLagPair, 0, recCause, trackedID, recState, now); err != nil {
						return err
					}
					continue
				}
			}
			bundle, err := makeBundle(r, key, ep.id, c)
			if err != nil {
				return err
			}
			if memEp.state == "silenced" {
				memEp.trackingItemID = sql.NullString{}
			}
			memEp.state = "open"
			memEp.silenceID = sql.NullString{}
			memEp.silenceUntilMS = sql.NullInt64{}
			memEp.silenceOnFingerprint = sql.NullString{}
			memEp.ackedBy = sql.NullString{}
			memEp.ackedNote = sql.NullString{}
			memEp.ackedAt = sql.NullInt64{}
			memEp.ackedSeq = sql.NullInt64{}
			memEp.ackedFingerprint = sql.NullString{}
			memEp.ackedLagPair = sql.NullString{}
			clearMemSettlement(memEp)
			memEp.conditionFingerprint = newest.fingerprint
			memEp.bundleJSON = string(bundle)
			memEp.variablesJSON = encodeVariablesJSON(extractVariables(r, key, c))
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

func clearMemSettlement(memEp *memEpisode) {
	if memEp.settlementState == "investigating" || memEp.settlementState == "settled" || memEp.settlementState == "" {
		memEp.settlementState = "uninvestigated"
	}
	memEp.outcome = sql.NullString{}
	memEp.itemLinkKind = sql.NullString{}
	memEp.settledBy = sql.NullString{}
	memEp.settledAt = sql.NullInt64{}
	memEp.settledSeq = sql.NullInt64{}
	memEp.settledFingerprint = sql.NullString{}
	memEp.settledLagPair = sql.NullString{}
}

func (m *memoryState) reopenSettledOpenItemLocked(memEp *memEpisode, r Rule, key string, c *condition, fingerprint, lagPair, cause, itemID string, now time.Time) error {
	bundle, err := makeBundle(r, key, memEp.alertID, c)
	if err != nil {
		return err
	}
	memEp.state = "open"
	memEp.silenceUntilMS = sql.NullInt64{}
	memEp.silenceOnFingerprint = sql.NullString{}
	memEp.ackedBy = sql.NullString{}
	memEp.ackedNote = sql.NullString{}
	memEp.ackedAt = sql.NullInt64{}
	memEp.ackedSeq = sql.NullInt64{}
	memEp.ackedFingerprint = sql.NullString{}
	memEp.ackedLagPair = sql.NullString{}
	clearMemSettlement(memEp)
	memEp.reopenCountForItem++
	memEp.conditionFingerprint = fingerprint
	memEp.bundleJSON = string(bundle)
	memEp.variablesJSON = encodeVariablesJSON(extractVariables(r, key, c))
	return m.appendEventLocked(reopenedKind, memEp.alertID, now, map[string]any{
		"alert_id": memEp.alertID, "rule_id": r.ID, "subject_key": key,
		"severity": r.Severity, "receiver": r.Delivery.Receiver,
		"cause": cause, "item_id": itemID,
	})
}

func (m *memoryState) reopenSettledTerminalLocked(memEp *memEpisode, r Rule, key string, c *condition, fingerprint, lagPair string, nextSeq int64, cause, itemID, itemState string, now time.Time) error {
	bundle, err := makeBundleWithPrefix(r, key, memEp.alertID, c, trackedPersistPrefix(itemID, itemState))
	if err != nil {
		return err
	}
	memEp.state = "open"
	memEp.trackingItemID = sql.NullString{}
	if lagPair != "" {
		memEp.trackedLagPair = sql.NullString{String: lagPair, Valid: true}
	}
	memEp.silenceUntilMS = sql.NullInt64{}
	memEp.silenceOnFingerprint = sql.NullString{}
	memEp.ackedBy = sql.NullString{}
	memEp.ackedNote = sql.NullString{}
	memEp.ackedAt = sql.NullInt64{}
	memEp.ackedSeq = sql.NullInt64{}
	memEp.ackedFingerprint = sql.NullString{}
	memEp.ackedLagPair = sql.NullString{}
	clearMemSettlement(memEp)
	if nextSeq > 0 {
		memEp.lastObservedSeq = nextSeq
		memEp.lastObservedAt = now.UnixMilli()
		memEp.observationCount++
	}
	memEp.conditionFingerprint = fingerprint
	memEp.bundleJSON = string(bundle)
	memEp.variablesJSON = encodeVariablesJSON(extractVariables(r, key, c))
	return m.appendEventLocked(reopenedKind, memEp.alertID, now, map[string]any{
		"alert_id": memEp.alertID, "rule_id": r.ID, "subject_key": key,
		"severity": r.Severity, "receiver": r.Delivery.Receiver,
		"cause": cause, "item_id": itemID, "item_state": itemState,
	})
}

func (m *memoryState) reopenLagWhenSettledOpenItemLocked(memEp *memEpisode, r Rule, key string, c *condition, lagPair string, nextSeq int64, trackedID string, now time.Time) error {
	bundle, err := makeLagReopenBundle(r, key, memEp.alertID, c, "")
	if err != nil {
		return err
	}
	memEp.state = "open"
	memEp.trackedLagPair = sql.NullString{String: lagPair, Valid: true}
	memEp.silenceUntilMS = sql.NullInt64{}
	memEp.silenceOnFingerprint = sql.NullString{}
	memEp.ackedBy = sql.NullString{}
	memEp.ackedNote = sql.NullString{}
	memEp.ackedAt = sql.NullInt64{}
	memEp.ackedSeq = sql.NullInt64{}
	memEp.ackedFingerprint = sql.NullString{}
	memEp.ackedLagPair = sql.NullString{}
	clearMemSettlement(memEp)
	memEp.reopenCountForItem++
	memEp.lastObservedSeq = nextSeq
	memEp.lastObservedAt = now.UnixMilli()
	memEp.observationCount++
	memEp.bundleJSON = string(bundle)
	memEp.variablesJSON = encodeVariablesJSON(extractVariables(r, key, c))
	return m.appendEventLocked(reopenedKind, memEp.alertID, now, map[string]any{
		"alert_id": memEp.alertID, "rule_id": r.ID, "subject_key": key,
		"severity": r.Severity, "receiver": r.Delivery.Receiver,
		"cause": "reopen-when", "item_id": trackedID,
	})
}

func (m *memoryState) reopenLagWhenLocked(memEp *memEpisode, r Rule, key string, c *condition, lagPair string, nextSeq int64, trackedID string, now time.Time) error {
	bundle, err := makeLagReopenBundle(r, key, memEp.alertID, c, "")
	if err != nil {
		return err
	}
	memEp.state = "open"
	memEp.trackingItemID = sql.NullString{}
	memEp.trackedLagPair = sql.NullString{String: lagPair, Valid: true}
	memEp.silenceUntilMS = sql.NullInt64{}
	memEp.silenceOnFingerprint = sql.NullString{}
	memEp.ackedBy = sql.NullString{}
	memEp.ackedNote = sql.NullString{}
	memEp.ackedAt = sql.NullInt64{}
	memEp.ackedSeq = sql.NullInt64{}
	memEp.ackedFingerprint = sql.NullString{}
	memEp.ackedLagPair = sql.NullString{}
	clearMemSettlement(memEp)
	memEp.lastObservedSeq = nextSeq
	memEp.lastObservedAt = now.UnixMilli()
	memEp.observationCount++
	memEp.bundleJSON = string(bundle)
	memEp.variablesJSON = encodeVariablesJSON(extractVariables(r, key, c))
	body := map[string]any{
		"alert_id": memEp.alertID, "rule_id": r.ID, "subject_key": key,
		"severity": r.Severity, "receiver": r.Delivery.Receiver,
		"cause": "reopen-when",
	}
	if trackedID != "" {
		body["item_id"] = trackedID
	}
	return m.appendEventLocked(reopenedKind, memEp.alertID, now, body)
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
	settlementState := ep.settlementState
	if settlementState == "" {
		settlementState = "uninvestigated"
	}
	return ep, episodeAction{
		state:             ep.state,
		fingerprint:       ep.conditionFingerprint,
		lastObservedSeq:   ep.lastObservedSeq,
		ackedBy:           ep.ackedBy,
		ackedNote:         ep.ackedNote,
		trackingItemID:    ep.trackingItemID,
		trackedLagPair:    ep.trackedLagPair,
		settlementState:   settlementState,
		outcome:           ep.outcome,
		settledBy:         ep.settledBy,
		hasTrackingCol:    true,
		hasLagPairCol:     true,
		hasAckCols:        true,
		hasSettlementCols: true,
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

func (m *memoryState) settle(id string, now time.Time, change func(*memEpisode, episodeAction) (bool, error), opts SettleOptions) error {
	if m == nil {
		return fmt.Errorf("alert %s %w", id, ErrAlertNotFound)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ep, a, err := m.readEpisodeActionLocked(id)
	if err != nil {
		return err
	}
	alreadySettled := a.settlementState == "settled"
	inline, err := change(ep, a)
	if err != nil {
		return err
	}
	if alreadySettled {
		return nil
	}
	if inline {
		if err := m.appendEventLocked(investigatedKind, id, now, map[string]any{
			"alert_id":    id,
			"rule_id":     ep.ruleID,
			"subject_key": ep.subjectKey,
			"severity":    ep.severity,
			"by":          opts.By,
			"cause":       opts.Cause,
			"evidence":    opts.Evidence,
		}); err != nil {
			return err
		}
	}
	body := map[string]any{
		"alert_id":    id,
		"rule_id":     ep.ruleID,
		"subject_key": ep.subjectKey,
		"severity":    ep.severity,
		"by":          opts.By,
		"outcome":     opts.Outcome,
		"item_id":     opts.ItemID,
		"settled_seq": ep.settledSeq.Int64,
	}
	if opts.LinkKind != "" {
		body["link_kind"] = opts.LinkKind
	}
	return m.appendEventLocked(settledKind, id, now, body)
}

func (m *memoryState) appendSilenceEventLocked(kind, silenceID string, now time.Time, body map[string]any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	m.nextSeq++
	m.events = append(m.events, memAlertEvent{
		seq:      m.nextSeq,
		ts:       now.UnixMilli(),
		kind:     kind,
		objectID: silenceID,
		payload:  payload,
	})
	return nil
}

func (m *memoryState) evaluateSilences(rules []Rule, derived []map[string]*condition, startHead int64, now time.Time, lookup itemTerminalChecker) (silenceEvalState, error) {
	var state silenceEvalState
	if m == nil {
		return state, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sil := range m.silences {
		if sil.LiftedAt == nil {
			state.active = append(state.active, *sil)
		} else if strings.HasPrefix(sil.LiftedReason, "item-") {
			state.recentItemLifted = append(state.recentItemLifted, *sil)
		}
	}
	if len(state.active) == 0 {
		return state, nil
	}
	state.evaluateAutoLift(rules, derived, m.activeLocked, startHead, now, lookup)
	for _, lifted := range state.liftedNow {
		for _, sil := range m.silences {
			if sil.SilenceID == lifted.SilenceID && sil.LiftedAt == nil {
				sil.LiftedAt = lifted.LiftedAt
				sil.LiftedBy = lifted.LiftedBy
				sil.LiftedReason = lifted.LiftedReason
				break
			}
		}
		if err := m.appendSilenceEventLocked(silenceLiftedKind, lifted.SilenceID, now, map[string]any{
			"silence_id": lifted.SilenceID,
			"rule_id":    lifted.RuleID,
			"vars":       lifted.Vars,
			"item_id":    lifted.TrackingItemID,
			"by":         "evaluator",
			"reason":     lifted.LiftedReason,
		}); err != nil {
			return state, err
		}
	}
	return state, nil
}

func (e Evaluator) silenceMemory(ctx context.Context, opts SilenceOptions) (SilenceRecord, error) {
	if err := validateSilenceActorAndReason(opts); err != nil {
		return SilenceRecord{}, err
	}
	now := e.now()
	var untilMS *int64
	if opts.Until != nil {
		expires, err := silenceTime(*opts.Until, now)
		if err != nil {
			return SilenceRecord{}, err
		}
		ms := expires.UnixMilli()
		if expires.After(time.UnixMilli(ms)) {
			ms++
		}
		untilMS = &ms
	}
	if err := validateSourceSilenceItem(ctx, e.Source, opts.ItemID); err != nil {
		return SilenceRecord{}, err
	}
	if e.mem == nil {
		return SilenceRecord{}, fmt.Errorf("alert %s %w", opts.AlertID, ErrAlertNotFound)
	}
	e.mem.mu.Lock()
	defer e.mem.mu.Unlock()

	var invCause, invEvidence, ruleID, varsJSON, fp, sourceAlert string
	var inlineInv, resolvedOnly bool
	var targetIDs []string
	if opts.Cause != "" || opts.Evidence != "" {
		if strings.TrimSpace(opts.Cause) == "" || strings.EqualFold(strings.TrimSpace(opts.Cause), "unknown") || strings.TrimSpace(opts.Evidence) == "" {
			return SilenceRecord{}, fmt.Errorf("%w: inline --cause and --evidence must both be non-empty and --cause must not be %q", ErrInvestigationRequired, "unknown")
		}
		inlineInv = true
		invCause = opts.Cause
		invEvidence = opts.Evidence
	}

	if opts.AlertID != "" {
		ep, a, err := e.mem.readEpisodeActionLocked(opts.AlertID)
		if err != nil {
			return SilenceRecord{}, err
		}
		if a.settlementState == "settled" {
			return SilenceRecord{}, fmt.Errorf("alert %s is already settled", opts.AlertID)
		}
		if !inlineInv {
			if a.settlementState != "investigated" || strings.TrimSpace(ep.investigationCause.String) == "" || strings.TrimSpace(ep.investigationEvidence.String) == "" {
				return SilenceRecord{}, fmt.Errorf("%w: %s", ErrInvestigationRequired, a.refuse(opts.AlertID))
			}
			invCause = ep.investigationCause.String
			invEvidence = ep.investigationEvidence.String
		}
		ruleID = ep.ruleID
		sourceAlert = opts.AlertID
		if len(opts.Vars) > 0 {
			r, found, _ := e.findRuleRead(nil, ruleID)
			if found {
				vj, err := validateSilenceRuleVars(r, opts.Vars)
				if err != nil {
					return SilenceRecord{}, err
				}
				varsJSON = vj
			} else {
				varsJSON = encodeVariablesJSON(opts.Vars)
			}
		} else {
			varsJSON = encodeVariablesJSON(decodeVariablesJSON(ep.variablesJSON))
		}
		if opts.UntilChange {
			if a.state == "resolved" {
				return SilenceRecord{}, fmt.Errorf("%s: --until-change requires an active episode", a.refuse(opts.AlertID))
			}
			r, found, _ := e.findRuleRead(nil, ruleID)
			if (found && len(r.When.Fingerprint) == 0) || a.fingerprint == "" {
				return SilenceRecord{}, fmt.Errorf("%s and has no fingerprint; use --until", a.refuse(opts.AlertID))
			}
			fp = a.fingerprint
		}
		if a.state == "resolved" {
			resolvedOnly = true
		} else {
			targetIDs = []string{opts.AlertID}
		}
	} else {
		r, found, _ := e.findRuleRead(nil, opts.RuleID)
		if !found {
			return SilenceRecord{}, fmt.Errorf("rule %q not found", opts.RuleID)
		}
		if !inlineInv {
			return SilenceRecord{}, fmt.Errorf("%w: silencing by --rule requires --cause and --evidence", ErrInvestigationRequired)
		}
		vj, err := validateSilenceRuleVars(r, opts.Vars)
		if err != nil {
			return SilenceRecord{}, err
		}
		ruleID = opts.RuleID
		varsJSON = vj
		for _, ep := range e.mem.episodes {
			if ep.ruleID == ruleID && ep.state != "resolved" && silenceMatches(varsJSON, ep.variablesJSON) {
				targetIDs = append(targetIDs, ep.alertID)
				if fp == "" && ep.conditionFingerprint != "" {
					fp = ep.conditionFingerprint
				}
			}
		}
		if opts.UntilChange {
			if len(r.When.Fingerprint) == 0 {
				return SilenceRecord{}, fmt.Errorf("rule %s has no fingerprint; use --until", opts.RuleID)
			}
			if len(targetIDs) == 0 || fp == "" {
				return SilenceRecord{}, fmt.Errorf("rule %s has no active matching episode with a fingerprint; use --until", opts.RuleID)
			}
		}
	}

	for _, existing := range e.mem.silences {
		if existing.RuleID == ruleID && existing.VarsJSON == varsJSON && existing.LiftedAt == nil {
			return SilenceRecord{}, fmt.Errorf("rule %s with vars %s is already silenced by %s", ruleID, varsJSON, existing.SilenceID)
		}
	}

	silID := newSilenceID()
	vars := decodeVariablesJSON(varsJSON)
	rec := &SilenceRecord{
		SilenceID:             silID,
		RuleID:                ruleID,
		Vars:                  vars,
		VarsJSON:              varsJSON,
		TrackingItemID:        opts.ItemID,
		CreatedBy:             opts.By,
		CreatedAt:             now.UnixMilli(),
		Reason:                opts.Reason,
		InvestigationCause:    invCause,
		InvestigationEvidence: invEvidence,
		SourceAlertID:         sourceAlert,
		SilenceUntilMS:        untilMS,
		SilenceOnFingerprint:  fp,
	}
	e.mem.silences = append(e.mem.silences, rec)

	updateEp := func(aid string, keepResolved bool) error {
		ep := e.mem.findAlertLocked(aid)
		if ep == nil {
			return nil
		}
		settledSeq := ep.lastObservedSeq
		if ep.trackedLagPair.Valid && ep.trackedLagPair.String != "" {
			settledSeq = max(settledSeq, e.mem.lastSourceSeq)
		}
		if inlineInv {
			ep.investigatedBy = sql.NullString{String: opts.By, Valid: true}
			ep.investigatedAt = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
			ep.investigationCause = sql.NullString{String: invCause, Valid: true}
			ep.investigationEvidence = sql.NullString{String: invEvidence, Valid: true}
			if err := e.mem.appendEventLocked(investigatedKind, aid, now, map[string]any{
				"alert_id":    aid,
				"rule_id":     ep.ruleID,
				"subject_key": ep.subjectKey,
				"severity":    ep.severity,
				"by":          opts.By,
				"cause":       invCause,
				"evidence":    invEvidence,
			}); err != nil {
				return err
			}
		}
		if !keepResolved {
			ep.state = "silenced"
		}
		ep.silenceID = sql.NullString{String: silID, Valid: true}
		if untilMS != nil {
			ep.silenceUntilMS = sql.NullInt64{Int64: *untilMS, Valid: true}
		} else {
			ep.silenceUntilMS = sql.NullInt64{}
		}
		if fp != "" {
			ep.silenceOnFingerprint = sql.NullString{String: fp, Valid: true}
		} else {
			ep.silenceOnFingerprint = sql.NullString{}
		}
		ep.trackingItemID = sql.NullString{String: opts.ItemID, Valid: true}
		ep.settlementState = "settled"
		ep.outcome = sql.NullString{String: "silence", Valid: true}
		ep.settledBy = sql.NullString{String: opts.By, Valid: true}
		ep.settledAt = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
		ep.settledSeq = sql.NullInt64{Int64: settledSeq, Valid: true}
		ep.settledFingerprint = sql.NullString{String: ep.conditionFingerprint, Valid: true}
		ep.settledLagPair = ep.trackedLagPair
		silPayload := map[string]any{
			"alert_id":    aid,
			"rule_id":     ep.ruleID,
			"subject_key": ep.subjectKey,
			"severity":    ep.severity,
			"silence_id":  silID,
			"by":          opts.By,
			"item_id":     opts.ItemID,
			"reason":      opts.Reason,
			"vars":        vars,
		}
		if untilMS != nil {
			silPayload["until"] = time.UnixMilli(*untilMS).UTC().Format(time.RFC3339Nano)
		}
		if opts.UntilChange {
			silPayload["until_change"] = true
		}
		return e.mem.appendEventLocked(silencedKind, aid, now, silPayload)
	}
	if resolvedOnly && opts.AlertID != "" {
		if err := updateEp(opts.AlertID, true); err != nil {
			return SilenceRecord{}, err
		}
	} else {
		for _, aid := range targetIDs {
			if err := updateEp(aid, false); err != nil {
				return SilenceRecord{}, err
			}
		}
	}
	return *rec, nil
}

func (m *memoryState) listSilences(includeLifted bool) []SilenceRecord {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SilenceRecord
	for _, sil := range m.silences {
		if !includeLifted && sil.LiftedAt != nil {
			continue
		}
		cp := *sil
		var count int64
		for _, ep := range m.episodes {
			if ep.silenceID.Valid && ep.silenceID.String == sil.SilenceID {
				count++
			}
		}
		cp.MatchedAlerts = count
		out = append(out, cp)
	}
	return out
}

func (m *memoryState) liftSilence(silenceID, by, reason string, now time.Time) error {
	if m == nil {
		return fmt.Errorf("silence %s not found", silenceID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var target *SilenceRecord
	for _, sil := range m.silences {
		if sil.SilenceID == silenceID {
			target = sil
			break
		}
	}
	if target == nil {
		return fmt.Errorf("silence %s not found", silenceID)
	}
	if target.LiftedAt != nil {
		return fmt.Errorf("silence %s is already lifted", silenceID)
	}
	liftedAt := now.UnixMilli()
	target.LiftedAt = &liftedAt
	target.LiftedBy = by
	target.LiftedReason = reason
	if err := m.appendSilenceEventLocked(silenceLiftedKind, silenceID, now, map[string]any{
		"silence_id": silenceID,
		"rule_id":    target.RuleID,
		"vars":       target.Vars,
		"item_id":    target.TrackingItemID,
		"by":         by,
		"reason":     reason,
	}); err != nil {
		return err
	}
	for _, ep := range m.episodes {
		if ep.state == "silenced" && ep.silenceID.Valid && ep.silenceID.String == silenceID {
			ep.state = "open"
			ep.silenceID = sql.NullString{}
			ep.silenceUntilMS = sql.NullInt64{}
			ep.silenceOnFingerprint = sql.NullString{}
			ep.trackingItemID = sql.NullString{}
			ep.ackedBy = sql.NullString{}
			ep.ackedNote = sql.NullString{}
			ep.ackedAt = sql.NullInt64{}
			ep.ackedSeq = sql.NullInt64{}
			ep.ackedFingerprint = sql.NullString{}
			ep.ackedLagPair = sql.NullString{}
			clearMemSettlement(ep)
			if err := m.appendEventLocked(reopenedKind, ep.alertID, now, map[string]any{
				"alert_id":    ep.alertID,
				"rule_id":     ep.ruleID,
				"subject_key": ep.subjectKey,
				"severity":    ep.severity,
				"cause":       "silence-lifted",
				"silence_id":  silenceID,
				"by":          by,
				"reason":      reason,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
