package watch

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	openedKind   = "watch.alert.opened"
	resolvedKind = "watch.alert.resolved"
)

func init() {
	if err := store.RegisterDDL("watch", schema); err != nil {
		panic(err)
	}
	for _, col := range []struct{ name, decl string }{
		{"tracking_item_id", "TEXT"},
		{"tracked_lag_pair", "TEXT"},
		{"acked_seq", "INTEGER"},
		{"acked_fingerprint", "TEXT"},
		{"acked_lag_pair", "TEXT"},
	} {
		if err := store.RegisterColumn("watch", "alert_episodes", col.name, col.decl); err != nil {
			panic(err)
		}
	}
	for _, kind := range []string{openedKind, resolvedKind, acknowledgedKind, trackedKind, silencedKind, reopenedKind, ruleSetKind, ruleResetKind} {
		if err := event.Register(kind, "watch"); err != nil {
			panic(err)
		}
	}
}

const schema = `
CREATE TABLE IF NOT EXISTS alert_episodes (
 alert_id TEXT PRIMARY KEY,
 rule_id TEXT NOT NULL,
 subject_key TEXT NOT NULL,
 episode_num INTEGER NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('open', 'acknowledged', 'silenced', 'resolved')),
 severity TEXT NOT NULL,
 condition_fingerprint TEXT NOT NULL,
 opened_seq INTEGER NOT NULL,
 opened_at INTEGER NOT NULL,
 last_observed_seq INTEGER NOT NULL,
 last_observed_at INTEGER NOT NULL,
 observation_count INTEGER NOT NULL DEFAULT 1,
 acked_by TEXT,
 acked_note TEXT,
 acked_at INTEGER,
 acked_seq INTEGER,
 acked_fingerprint TEXT,
 acked_lag_pair TEXT,
 silence_until_ms INTEGER,
 silence_on_fingerprint TEXT,
 active_triage_run_id TEXT,
 tracking_item_id TEXT,
 tracked_lag_pair TEXT,
 resolved_seq INTEGER,
 resolved_at INTEGER,
 resolved_reason TEXT,
 bundle_json TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS alert_episodes_one_active
    ON alert_episodes(rule_id, subject_key) WHERE state != 'resolved';
CREATE INDEX IF NOT EXISTS alert_episodes_history ON alert_episodes(rule_id,subject_key,episode_num);
CREATE TABLE IF NOT EXISTS foreground_delivery (
 owner_handle TEXT PRIMARY KEY,
 seen_seq INTEGER NOT NULL DEFAULT 0,
 shown_seq INTEGER NOT NULL DEFAULT 0,
 waiting_since INTEGER,
 last_wait_at INTEGER
);
` + ruleOverridesDDL + ruleBaselinesDDL

// Evaluator derives rule conditions from a single store snapshot per pass.
// Only the watch episode transitions and their corresponding events are written.
type Evaluator struct {
	Store  *store.Store
	Source ObservationSource
	Rules  []Rule // validated file rules
	// Root is the repository whose Git refs projections read. It must be explicit.
	Root  string
	Clock func() time.Time

	// Test seams for concurrent commits before and during planning, and for
	// observing event queries and Git subprocesses without timing.
	afterGit, afterPlan func()
	onEventQuery        func(kind string, sinceSeq int64, rows int)
	onGitExec           func(args []string)

	cache *evalCache
	mem   *memoryState
}

func (e Evaluator) storeSource() *TillrStoreSource {
	return &TillrStoreSource{Store: e.Store, Clock: e.Clock}
}

func (e Evaluator) gitCheckIO() func(string) error {
	if e.Store != nil {
		return e.storeSource().checkIO
	}
	if ts, ok := e.Source.(*TillrStoreSource); ok && ts != nil {
		return ts.checkIO
	}
	return nil
}

func (e Evaluator) now() time.Time {
	if e.Clock != nil {
		return e.Clock()
	}
	return time.Now()
}

// RuleResult describes an evaluated rule and any runtime unreadability.
type RuleResult struct {
	ID, GroupBy             string
	Disabled                bool
	Skipped                 int
	Unsupported, Unreadable string
	OutOfScope              string
}

func (r RuleResult) Diagnostic() string {
	switch {
	case r.Unsupported != "":
		return r.Unsupported
	case r.Unreadable != "":
		return r.Unreadable
	case r.OutOfScope != "":
		return "the store's charter is " + r.OutOfScope
	default:
		return ""
	}
}

func skippedCounts(results []RuleResult) map[string]int {
	counts := map[string]int{}
	for _, r := range results {
		if !r.Disabled && r.Diagnostic() == "" {
			counts[r.ID] = r.Skipped
		}
	}
	return counts
}

// Skipped returns the legacy command-facing counts without writing. Use
// RuleResults for the extended patterns, projections and diagnostics.
func (e Evaluator) Skipped(ctx context.Context) (map[string]int, error) {
	e.Rules = commandRules(e.Rules)
	results, err := e.RuleResults(ctx)
	return skippedCounts(results), err
}

// Evaluate reconciles episodes for rules supported by the legacy command.
// Use EvaluateRules to opt into the extended evaluator.
func (e *Evaluator) Evaluate(ctx context.Context) (map[string]int, error) {
	results, err := e.evaluateRules(ctx, false)
	return skippedCounts(results), err
}

func commandRules(rules []Rule) []Rule {
	var eligible []Rule
	for _, r := range rules {
		if r.Enabled && r.UnevaluatedReason() == "" {
			eligible = append(eligible, r)
		}
	}
	return eligible
}

// RuleResults derives conditions without changing episodes.
func (e Evaluator) RuleResults(ctx context.Context) ([]RuleResult, error) {
	if e.Store == nil {
		if e.Source == nil {
			return nil, fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		now := e.now()
		git, err := readGit(ctx, e.Root, e.Rules, nil, e.gitCheckIO(), e.onGitExec)
		if err != nil {
			return nil, err
		}
		results, _, _, _, _, err := e.deriveSourceRules(ctx, e.Source, e.Rules, git, now)
		return results, err
	}
	var results []RuleResult
	err := e.readEvaluation(ctx, e.Rules, func(_ *store.Reader, rows []RuleResult) error {
		results = rows
		return nil
	})
	return results, err
}

// readEvaluation reads Git before the store snapshot. The caller provides one
// fixed set of rules for both reads and may inspect other rows in that snapshot.
func (e Evaluator) readEvaluation(ctx context.Context, rules []Rule, visit func(*store.Reader, []RuleResult) error) error {
	now := e.now()
	git, err := readGit(ctx, e.Root, rules, nil, e.gitCheckIO(), e.onGitExec)
	if err != nil {
		return err
	}
	return e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		head, err := rd.EventHeadSeq()
		if err != nil {
			return err
		}
		baselines, err := readRuleBaselines(rd)
		if err != nil {
			return err
		}
		results, _, err := e.deriveRules(rd, rules, git, now, nil, baselines, head, nil)
		if err != nil {
			return err
		}
		return visit(rd, results)
	})
}

// CheckRules validates effective rules before wait records any delivery state.
// The actual evaluation checks again against the rules of that pass.
func (e Evaluator) CheckRules(ctx context.Context) error {
	if e.Store == nil {
		if e.Source == nil {
			return fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		return requireRoot(e.Root, e.Rules)
	}
	return e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		_, _, err := e.effective(rd)
		return err
	})
}

func (e Evaluator) effective(rd *store.Reader) ([]Rule, map[string]bool, error) {
	rules, disabledOverrides, err := readEffectiveRules(rd, e.Rules)
	if err == nil {
		err = requireRoot(e.Root, rules)
	}
	return rules, disabledOverrides, err
}

var errNewGitFact = errors.New("effective rules need another Git fact")

// EvaluateRules uses live overrides and leaves episodes with unreadable projections untouched.
func (e *Evaluator) EvaluateRules(ctx context.Context) ([]RuleResult, error) {
	return e.evaluateRules(ctx, true)
}

func (e *Evaluator) evaluateRules(ctx context.Context, live bool) ([]RuleResult, error) {
	if e.Store == nil {
		if e.Source == nil {
			return nil, fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		rules := e.Rules
		if !live {
			rules = commandRules(e.Rules)
		}
		if err := requireRoot(e.Root, rules); err != nil {
			return nil, err
		}
		if e.mem == nil {
			e.mem = newMemoryState()
		}
		now := e.now()
		git, err := readGit(ctx, e.Root, rules, &e.mem.git, e.gitCheckIO(), e.onGitExec)
		if err != nil {
			return nil, err
		}
		if e.afterGit != nil {
			e.afterGit()
		}
		results, derived, startHead, passEpoch, hasCursor, err := e.deriveSourceRules(ctx, e.Source, rules, git, now)
		if err != nil {
			return nil, err
		}
		lookup := newSourceItemTerminal(ctx, e.Source)
		ruleByID := make(map[string]*Rule, len(e.Rules)+len(rules))
		for i := range e.Rules {
			r := e.Rules[i]
			ruleByID[r.ID] = &r
		}
		for i := range rules {
			r := rules[i]
			ruleByID[r.ID] = &r
		}
		reconciled := make(map[string]bool, len(rules))
		baselines := e.mem.prepareEpoch(hasCursor, passEpoch)
		for i, subjects := range derived {
			if subjects == nil {
				continue
			}
			reconciled[rules[i].ID] = true
			loadedSeq := effectiveLoadedSeq(rules[i], baselines, startHead)
			if err := e.mem.reconcileRule(rules[i], subjects, startHead, loadedSeq, now, lookup); err != nil {
				return nil, fmt.Errorf("rule %s: %w", rules[i].ID, err)
			}
		}
		if err := e.mem.resolveTerminalRemaining(ruleByID, reconciled, startHead, now, lookup); err != nil {
			return nil, err
		}
		fullRules := rules
		if !live {
			fullRules = e.Rules
		}
		e.mem.updateBaselines(rules, results, fullRules, live, startHead, now)
		return results, nil
	}
	if e.cache == nil || e.cache.sourceKey != e.Store {
		e.cache = newEvalCache(e.Store)
	}
	readRules := e.effective
	if !live {
		eligible := commandRules(e.Rules)
		// The legacy event-only API evaluates the caller's fixed rules.
		readRules = func(*store.Reader) ([]Rule, map[string]bool, error) { return eligible, nil, nil }
	}
	for {
		now := e.now()
		var startHead int64
		var rules []Rule
		if err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
			var err error
			startHead, err = rd.EventHeadSeq()
			if err == nil {
				rules, _, err = readRules(rd)
			}
			return err
		}); err != nil {
			return nil, err
		}
		git, err := readGit(ctx, e.Root, rules, &e.cache.git, e.gitCheckIO(), e.onGitExec)
		if err != nil {
			return nil, err
		}
		if e.afterGit != nil {
			e.afterGit()
		}
		var results []RuleResult
		err = e.Store.ApplyPlan(ctx, func(rd *store.Reader) (func(*store.Tx) error, error) {
			rules, disabledOverrides, err := readRules(rd)
			if err != nil {
				return nil, err
			}
			if !git.covers(rules) {
				return nil, errNewGitFact
			}
			baselines, err := readRuleBaselines(rd)
			if err != nil {
				return nil, err
			}
			activeSnap, err := loadActiveEpisodes(rd, e.cache)
			if err != nil {
				return nil, err
			}
			var derived []map[string]*condition
			results, derived, err = e.deriveRules(rd, rules, git, now, e.cache, baselines, startHead, &activeSnap)
			if err != nil {
				return nil, err
			}
			var lookup itemTerminalChecker
			if candidateIDs := candidateItemTerminalIDs(derived, rules, activeSnap); len(candidateIDs) > 0 {
				lookup, err = newStoreItemTerminal(rd, e.cache, candidateIDs)
				if err != nil {
					return nil, err
				}
			}
			snapHead, err := rd.EventHeadSeq()
			if err != nil {
				return nil, err
			}
			ruleByID := make(map[string]*Rule, len(e.Rules)+len(rules))
			for i := range e.Rules {
				r := e.Rules[i]
				ruleByID[r.ID] = &r
			}
			for i := range rules {
				r := rules[i]
				ruleByID[r.ID] = &r
			}
			reconciled := make(map[string]bool, len(rules))
			var changes episodeChanges
			for i, subjects := range derived {
				rule := rules[i]
				if subjects == nil {
					if !disabledOverrides[rule.ID] {
						continue
					}
					// A disabled override no longer fires for any subject.
					subjects = make(map[string]*condition)
				}
				reconciled[rule.ID] = true
				loadedSeq := effectiveLoadedSeq(rule, baselines, startHead)
				if err := changes.reconcile(rd, rule, subjects, activeSnap.forRule(rule.ID), startHead, snapHead, loadedSeq, now, lookup); err != nil {
					return nil, fmt.Errorf("rule %s: %w", rule.ID, err)
				}
			}
			if err := changes.resolveTerminalRemaining(rd, activeSnap, ruleByID, reconciled, snapHead, now, lookup); err != nil {
				return nil, err
			}
			fullRules := rules
			if !live {
				fullRules = e.Rules
			}
			changes.planBaselines(rules, results, fullRules, live, baselines, startHead, now)
			if len(changes) == 0 {
				return nil, nil
			}
			if e.afterPlan != nil {
				e.afterPlan()
			}
			return changes.apply, nil
		})
		if errors.Is(err, errNewGitFact) || errors.Is(err, store.ErrStalePlan) {
			// No mutation was made. Re-derive from current facts, not from the
			// invalidated snapshot. SQLite lock errors are not retried here.
			continue
		}
		if err != nil {
			return nil, err
		}
		return results, nil
	}
}

func (e Evaluator) deriveRules(rd *store.Reader, rules []Rule, git gitSnapshot, now time.Time, cache *evalCache, baselines map[string]ruleBaseline, defaultHead int64, activeSnap *activeSnapshot) ([]RuleResult, []map[string]*condition, error) {
	ts := e.storeSource()
	if cache != nil {
		if err := ts.syncKinds(rd, cache, rules, now, baselines, defaultHead, e.onEventQuery); err != nil {
			return nil, nil, err
		}
	}
	facts := ts.readFacts(rd)
	activeSubjects := func(ruleID string, n int) (map[string][]string, []string, error) {
		if activeSnap != nil && activeSnap.loaded {
			return activeSnap.compositeSubjects(ruleID, n), activeSnap.sortedCompositeKeys(ruleID, n), nil
		}
		return ts.activeCompositeSubjects(rd, ruleID, n)
	}
	results := make([]RuleResult, len(rules))
	derived := make([]map[string]*condition, len(rules))
	for i, r := range rules {
		result := &results[i]
		groupBy := r.When.GroupBy
		if len(r.When.GroupByList) >= 2 {
			groupBy = strings.Join(r.When.GroupByList, ", ")
		}
		*result = RuleResult{ID: r.ID, GroupBy: groupBy, Disabled: !r.Enabled, Unsupported: r.evaluatorReason()}
		if result.Disabled || result.Unsupported != "" {
			continue
		}
		if r.Scope != "" && r.Scope != "*" && !isTillrScope(r.Scope) {
			result.Unsupported = fmt.Sprintf("unsupported scope %q", r.Scope)
			continue
		}
		rf, outOfScope, err := facts.forRule(r, git)
		if err != nil {
			result.Unreadable = err.Error()
			continue
		}
		if outOfScope != "" {
			result.OutOfScope = outOfScope
			continue
		}
		if r.When.Pattern == LagBehind {
			lag, err := deriveLag(r, git, now)
			if err != nil {
				result.Unreadable = err.Error()
				continue
			}
			derived[i] = map[string]*condition{"global": lag}
			continue
		}
		firstLoadedSeq := effectiveFirstLoadedSeq(r, baselines, defaultHead)
		var subjects map[string]*condition
		var n int
		if cache != nil {
			subjects, n, err = deriveCached(r, now, rf, cache, firstLoadedSeq, activeSubjects)
		} else {
			subjects, n, err = ts.deriveRule(rd, r, now, rf, firstLoadedSeq)
		}
		var unreadable unreadableProjection
		if errors.As(err, &unreadable) {
			result.Unreadable = unreadable.Error()
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("rule %s: %w", r.ID, err)
		}
		result.Skipped, derived[i] = n, subjects
	}
	return results, derived, nil
}

func (e Evaluator) deriveSourceRules(ctx context.Context, src ObservationSource, rules []Rule, git gitSnapshot, now time.Time) ([]RuleResult, []map[string]*condition, int64, int64, bool, error) {
	var activeSubjects activeCompositeFunc
	if e.mem != nil {
		activeSubjects = e.mem.activeCompositeSubjects
	}
	results := make([]RuleResult, len(rules))
	derived := make([]map[string]*condition, len(rules))
	var (
		maxHead   int64
		passEpoch int64
		hasCursor bool
	)
	for i, r := range rules {
		result := &results[i]
		groupBy := r.When.GroupBy
		if len(r.When.GroupByList) >= 2 {
			groupBy = strings.Join(r.When.GroupByList, ", ")
		}
		*result = RuleResult{ID: r.ID, GroupBy: groupBy, Disabled: !r.Enabled, Unsupported: r.evaluatorReason()}
		if result.Disabled || result.Unsupported != "" {
			continue
		}
		rf, outOfScope, unsupported, err := prepareSourceRule(ctx, src, r, git)
		if err != nil {
			result.Unreadable = err.Error()
			continue
		}
		if unsupported != "" {
			result.Unsupported = unsupported
			continue
		}
		if outOfScope != "" {
			result.OutOfScope = outOfScope
			continue
		}
		if r.When.Pattern == LagBehind {
			lag, err := deriveLag(r, git, now)
			if err != nil {
				result.Unreadable = err.Error()
				continue
			}
			derived[i] = map[string]*condition{"global": lag}
			continue
		}
		subjects, n, cursor, err := deriveFromSource(ctx, src, r, now, rf, e.mem.resolveFirstLoadedSeq, activeSubjects)
		var unreadable unreadableProjection
		if errors.As(err, &unreadable) {
			result.Unreadable = unreadable.Error()
			continue
		}
		if err != nil {
			return nil, nil, 0, 0, false, fmt.Errorf("rule %s: %w", r.ID, err)
		}
		if !hasCursor {
			hasCursor = true
			passEpoch = cursor.Epoch
		}
		if cursor.Seq > maxHead {
			maxHead = cursor.Seq
		}
		result.Skipped, derived[i] = n, subjects
	}
	return results, derived, maxHead, passEpoch, hasCursor, nil
}

type episode struct {
	id, state, severity  string
	opened, last         int64
	conditionFingerprint string
	silenceUntil         sql.NullInt64
	silenceOn            sql.NullString
	trackingItem         sql.NullString
	trackedLagPair       sql.NullString
	ackedSeq             sql.NullInt64
	ackedFingerprint     sql.NullString
	ackedLagPair         sql.NullString
	hasAckCols           bool
}

func (ep episode) reopenCause(r Rule, now time.Time, newest observation, lagPair string) string {
	switch ep.state {
	case "silenced":
		if ep.silenceUntil.Valid && !now.Before(time.UnixMilli(ep.silenceUntil.Int64)) {
			return "silence-expired"
		}
		if ep.silenceOn.Valid && ep.silenceOn.String != newest.fingerprint {
			return "fingerprint-changed"
		}
	case "acknowledged":
		ackedFP := ep.conditionFingerprint
		if ep.ackedFingerprint.Valid {
			ackedFP = ep.ackedFingerprint.String
		}
		if newest.fingerprint != ackedFP {
			return "new-instance"
		}
		if r.When.Pattern == LagBehind {
			if ep.ackedLagPair.Valid {
				if lagPair != ep.ackedLagPair.String {
					return "new-instance"
				}
			} else if ep.trackedLagPair.Valid && ep.trackedLagPair.String != "" {
				if lagPair != ep.trackedLagPair.String {
					return "new-instance"
				}
			}
			return ""
		}
		ackedSeq := ep.last
		if ep.ackedSeq.Valid {
			ackedSeq = ep.ackedSeq.Int64
		}
		if newest.Seq > ackedSeq {
			return "new-instance"
		}
	}
	return ""
}

type activeEpisodeRow struct {
	ruleID  string
	subject string
	epNum   int64
	ep      episode
}

type activeSnapshot struct {
	loaded  bool
	byRule  map[string]map[string]episode
	ordered []activeEpisodeRow
}

func (s activeSnapshot) forRule(ruleID string) map[string]episode {
	return s.byRule[ruleID]
}

func (s activeSnapshot) compositeSubjects(ruleID string, n int) map[string][]string {
	eps := s.byRule[ruleID]
	if len(eps) == 0 {
		return nil
	}
	tuples := map[string][]string{}
	for key := range eps {
		if tuple, ok := decodeCompositeSubject(key, n); ok {
			tuples[key] = tuple
		}
	}
	return tuples
}

func (s activeSnapshot) sortedCompositeKeys(ruleID string, n int) []string {
	eps := s.byRule[ruleID]
	if len(eps) == 0 {
		return nil
	}
	var keys []string
	for key := range eps {
		if _, ok := decodeCompositeSubject(key, n); ok {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

func loadActiveEpisodes(rd *store.Reader, cache *evalCache) (activeSnapshot, error) {
	var hasTracking, hasLagPair, hasAckCols bool
	if cache != nil && cache.hasAlertTrackingCols {
		hasTracking, hasLagPair = true, true
	} else {
		var err error
		hasTracking, err = alertEpisodesHasColumn(rd, "tracking_item_id")
		if err != nil {
			return activeSnapshot{}, err
		}
		hasLagPair, err = alertEpisodesHasColumn(rd, "tracked_lag_pair")
		if err != nil {
			return activeSnapshot{}, err
		}
		if cache != nil && hasTracking && hasLagPair {
			cache.hasAlertTrackingCols = true
		}
	}
	if cache != nil && cache.hasAlertAckCols {
		hasAckCols = true
	} else {
		ackedSeqCol, err := alertEpisodesHasColumn(rd, "acked_seq")
		if err != nil {
			return activeSnapshot{}, err
		}
		ackedFPCol, err := alertEpisodesHasColumn(rd, "acked_fingerprint")
		if err != nil {
			return activeSnapshot{}, err
		}
		ackedLagCol, err := alertEpisodesHasColumn(rd, "acked_lag_pair")
		if err != nil {
			return activeSnapshot{}, err
		}
		hasAckCols = ackedSeqCol && ackedFPCol && ackedLagCol
		if cache != nil && hasAckCols {
			cache.hasAlertAckCols = true
		}
	}
	cols := "rule_id,subject_key,episode_num,alert_id,severity,opened_seq,last_observed_seq,condition_fingerprint,state,silence_until_ms,silence_on_fingerprint"
	if hasTracking {
		cols += ",tracking_item_id"
	}
	if hasLagPair {
		cols += ",tracked_lag_pair"
	}
	if hasAckCols {
		cols += ",acked_seq,acked_fingerprint,acked_lag_pair"
	}
	rows, err := rd.Query("SELECT " + cols + " FROM alert_episodes WHERE state!='resolved'")
	if err != nil {
		if isMissingTableError(err, "alert_episodes") {
			return activeSnapshot{loaded: true}, nil
		}
		return activeSnapshot{}, err
	}
	defer rows.Close()
	snap := activeSnapshot{loaded: true}
	for rows.Next() {
		var row activeEpisodeRow
		row.ep.hasAckCols = hasAckCols
		dest := []any{&row.ruleID, &row.subject, &row.epNum, &row.ep.id, &row.ep.severity, &row.ep.opened, &row.ep.last, &row.ep.conditionFingerprint, &row.ep.state, &row.ep.silenceUntil, &row.ep.silenceOn}
		if hasTracking {
			dest = append(dest, &row.ep.trackingItem)
		}
		if hasLagPair {
			dest = append(dest, &row.ep.trackedLagPair)
		}
		if hasAckCols {
			dest = append(dest, &row.ep.ackedSeq, &row.ep.ackedFingerprint, &row.ep.ackedLagPair)
		}
		scanErr := rows.Scan(dest...)
		if scanErr != nil {
			return activeSnapshot{}, scanErr
		}
		if snap.byRule == nil {
			snap.byRule = map[string]map[string]episode{}
		}
		bySubject := snap.byRule[row.ruleID]
		if bySubject == nil {
			bySubject = map[string]episode{}
			snap.byRule[row.ruleID] = bySubject
		}
		bySubject[row.subject] = row.ep
		snap.ordered = append(snap.ordered, row)
	}
	if err := rows.Err(); err != nil {
		return activeSnapshot{}, err
	}
	if len(snap.ordered) > 1 {
		slices.SortFunc(snap.ordered, func(a, b activeEpisodeRow) int {
			if c := cmp.Compare(a.epNum, b.epNum); c != 0 {
				return c
			}
			return cmp.Compare(a.ep.id, b.ep.id)
		})
	}
	return snap, nil
}

func candidateItemTerminalIDs(derived []map[string]*condition, rules []Rule, activeSnap activeSnapshot) []string {
	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		if id == "" || id == "global" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	addSubject := func(r *Rule, subject string) {
		if subject == "" || subject == "global" {
			return
		}
		if r != nil {
			if len(r.When.GroupByList) >= 2 {
				if tuple, ok := decodeCompositeSubject(subject, len(r.When.GroupByList)); ok {
					add(tuple[0])
				}
				return
			}
			if r.When.GroupBy == "global" {
				return
			}
			add(subject)
			return
		}
		add(subject)
		if strings.Contains(subject, "/") {
			seg, _, _ := strings.Cut(subject, "/")
			if u, ok := unescapeSubjectPart(seg); ok {
				add(u)
			}
		}
	}
	for i, subjects := range derived {
		var rPtr *Rule
		if i < len(rules) {
			rPtr = &rules[i]
		}
		for key := range subjects {
			addSubject(rPtr, key)
		}
	}
	for _, row := range activeSnap.ordered {
		addSubject(nil, row.subject)
		if row.ep.trackingItem.Valid {
			add(row.ep.trackingItem.String)
		}
	}
	return ids
}

func newStoreItemTerminal(rd *store.Reader, evalC *evalCache, preloadIDs []string) (itemTerminalChecker, error) {
	var hasEventSeq bool
	if evalC != nil && evalC.hasItemsEventSeq {
		hasEventSeq = true
	} else {
		hasItems, err := tableExists(rd, "items")
		if err != nil || !hasItems {
			return itemTerminalChecker{}, err
		}
		if err := rd.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name = 'event_seq')`).Scan(&hasEventSeq); err != nil {
			return itemTerminalChecker{}, err
		}
		if evalC != nil && hasEventSeq {
			evalC.hasItemsEventSeq = true
		}
	}
	rec, err := definition.ForReader(rd)
	if err != nil {
		return itemTerminalChecker{}, nil
	}
	d := rec.Parsed
	type cachedItem struct {
		state    sql.NullString
		eventSeq int64
	}
	cache := map[string]cachedItem{}
	if len(preloadIDs) >= 2 && len(preloadIDs) <= 128 {
		args := make([]any, len(preloadIDs))
		for i, id := range preloadIDs {
			args[i] = id
			cache[id] = cachedItem{}
		}
		q := `SELECT item_id, state FROM items WHERE item_id IN (?` + strings.Repeat(",?", len(preloadIDs)-1) + `)`
		if hasEventSeq {
			q = `SELECT item_id, state, COALESCE(event_seq, 0) FROM items WHERE item_id IN (?` + strings.Repeat(",?", len(preloadIDs)-1) + `)`
		}
		rows, err := rd.Query(q, args...)
		if err != nil {
			return itemTerminalChecker{}, err
		}
		for rows.Next() {
			var id string
			var item cachedItem
			var scanErr error
			if hasEventSeq {
				scanErr = rows.Scan(&id, &item.state, &item.eventSeq)
			} else {
				scanErr = rows.Scan(&id, &item.state)
			}
			if scanErr != nil {
				rows.Close()
				return itemTerminalChecker{}, scanErr
			}
			cache[id] = item
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return itemTerminalChecker{}, err
		}
	}
	load := func(itemID string) (cachedItem, error) {
		if cached, ok := cache[itemID]; ok {
			return cached, nil
		}
		var item cachedItem
		var err error
		if hasEventSeq {
			err = rd.QueryRow(`SELECT state, COALESCE(event_seq, 0) FROM items WHERE item_id = ?`, itemID).Scan(&item.state, &item.eventSeq)
		} else {
			err = rd.QueryRow(`SELECT state FROM items WHERE item_id = ?`, itemID).Scan(&item.state)
		}
		if errors.Is(err, sql.ErrNoRows) {
			cache[itemID] = cachedItem{}
			return cachedItem{}, nil
		}
		if err != nil {
			return cachedItem{}, err
		}
		cache[itemID] = item
		return item, nil
	}
	return itemTerminalChecker{
		ready:       true,
		closedState: d.Closed,
		stateOf: func(itemID string) (string, bool, error) {
			item, err := load(itemID)
			if err != nil {
				return "", false, err
			}
			return item.state.String, item.state.Valid && item.state.String != "", nil
		},
		seqOf: func(itemID string) int64 {
			item, err := load(itemID)
			if err != nil {
				return 0
			}
			return item.eventSeq
		},
		isTerminal: func(state string) bool {
			return state != "" && (state == d.Closed || slices.Contains(d.Finished, state))
		},
	}, nil
}

// episodeChanges holds only the mutations a read-only evaluation found necessary.
// Bundles and transition payloads are prepared before the writer is acquired.
type episodeChanges []func(*store.Tx) error

func (changes episodeChanges) apply(tx *store.Tx) error {
	for _, change := range changes {
		if err := change(tx); err != nil {
			return err
		}
	}
	return nil
}

func (changes *episodeChanges) reconcile(rd *store.Reader, r Rule, subjects map[string]*condition, episodes map[string]episode, startHead, snapHead, loadedSeq int64, now time.Time, lookup itemTerminalChecker) error {
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
				rMeta := r
				if ep.severity != "" {
					rMeta.Severity = ep.severity
				}
				if err := changes.resolveWithReason(rMeta, key, ep.id, ep.trackingItem, snapHead, now, termReason); err != nil {
					return err
				}
			}
			continue
		}
		firing := r.Enabled && c.fires(r.When)
		firstClear := sort.Search(len(c.clears), func(i int) bool { return c.clears[i] > ep.last })
		if exists && ep.trackingItem.Valid && ep.trackingItem.String != "" {
			if !lookup.ready {
				continue
			}
			trackedID := ep.trackingItem.String
			itemState, itemExists, err := lookup.stateOf(trackedID)
			if err != nil {
				continue
			}
			if itemExists && !lookup.isTerminal(itemState) {
				if firing && firstClear == len(c.clears) {
					if r.When.Pattern == LagBehind {
						pair := c.lag.leaderOID + ":" + c.lag.followerOID
						if !ep.trackedLagPair.Valid {
							*changes = append(*changes, func(tx *store.Tx) error {
								_, err := tx.Exec(`UPDATE alert_episodes SET tracked_lag_pair=? WHERE alert_id=?`, pair, ep.id)
								return err
							})
						} else if ep.trackedLagPair.String != pair {
							nextSeq := max(ep.last, startHead)
							*changes = append(*changes, func(tx *store.Tx) error {
								_, err := tx.Exec(`UPDATE alert_episodes SET tracked_lag_pair=?,last_observed_seq=?,last_observed_at=?,observation_count=observation_count+1 WHERE alert_id=?`, pair, nextSeq, now.UnixMilli(), ep.id)
								return err
							})
						}
					} else {
						newest := c.events[len(c.events)-1]
						if newest.Seq > ep.last {
							*changes = append(*changes, func(tx *store.Tx) error {
								_, err := tx.Exec(`UPDATE alert_episodes SET last_observed_seq=?,last_observed_at=?,observation_count=observation_count+1,condition_fingerprint=? WHERE alert_id=?`, newest.Seq, now.UnixMilli(), newest.fingerprint, ep.id)
								return err
							})
						}
					}
					continue
				}
			} else {
				if !firing {
					seq := startHead
					if firstClear < len(c.clears) {
						seq = c.clears[firstClear]
					}
					if err := changes.resolve(r, key, ep.id, ep.trackingItem, seq, now); err != nil {
						return err
					}
					continue
				}
				newest := observation{Seq: startHead}
				var lagPair any
				if r.When.Pattern == LagBehind {
					pair := c.lag.leaderOID + ":" + c.lag.followerOID
					lagPair = pair
					if ep.trackedLagPair.Valid && ep.trackedLagPair.String != pair {
						nextSeq := max(ep.last, startHead)
						*changes = append(*changes, func(tx *store.Tx) error {
							_, err := tx.Exec(`UPDATE alert_episodes SET last_observed_seq=?,last_observed_at=?,observation_count=observation_count+1 WHERE alert_id=?`, nextSeq, now.UnixMilli(), ep.id)
							return err
						})
					}
				} else {
					newest = c.events[len(c.events)-1]
					if isWindowedEventRule(r.When) {
						cutoff := max(ep.last, lookup.terminalSeq(trackedID))
						if newest.Seq <= cutoff {
							if newest.Seq > ep.last {
								*changes = append(*changes, func(tx *store.Tx) error {
									_, err := tx.Exec(`UPDATE alert_episodes SET last_observed_seq=?,last_observed_at=?,observation_count=observation_count+1,condition_fingerprint=? WHERE alert_id=?`, newest.Seq, now.UnixMilli(), newest.fingerprint, ep.id)
									return err
								})
							}
							continue
						}
					}
					if newest.Seq > ep.last {
						*changes = append(*changes, func(tx *store.Tx) error {
							_, err := tx.Exec(`UPDATE alert_episodes SET last_observed_seq=?,last_observed_at=?,observation_count=observation_count+1 WHERE alert_id=?`, newest.Seq, now.UnixMilli(), ep.id)
							return err
						})
					}
				}
				cause, recordedState := lookup.reopenCause(itemState, itemExists)
				if err := changes.reopenTracked(r, key, ep.id, c, newest.fingerprint, lagPair, cause, trackedID, recordedState, now); err != nil {
					return err
				}
				continue
			}
		}
		if exists && (!firing || firstClear < len(c.clears)) {
			seq := startHead
			if firstClear < len(c.clears) {
				seq = c.clears[firstClear]
			}
			if err := changes.resolve(r, key, ep.id, ep.trackingItem, seq, now); err != nil {
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
			if r.When.Pattern != LagBehind && newest.Seq <= loadedSeq {
				continue
			}
			if err := changes.open(rd, r, key, c, newest, now); err != nil {
				return err
			}
			continue
		}
		var currentLagPair string
		if r.When.Pattern == LagBehind {
			currentLagPair = c.lag.leaderOID + ":" + c.lag.followerOID
			if !ep.trackedLagPair.Valid {
				*changes = append(*changes, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE alert_episodes SET tracked_lag_pair=? WHERE alert_id=?`, currentLagPair, ep.id)
					return err
				})
			} else if ep.trackedLagPair.String != currentLagPair {
				nextSeq := max(ep.last, startHead)
				*changes = append(*changes, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE alert_episodes SET tracked_lag_pair=?,last_observed_seq=?,last_observed_at=?,observation_count=observation_count+1 WHERE alert_id=?`, currentLagPair, nextSeq, now.UnixMilli(), ep.id)
					return err
				})
			}
		}
		if r.When.Pattern != LagBehind && newest.Seq > ep.last {
			*changes = append(*changes, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE alert_episodes SET last_observed_seq=?,last_observed_at=?,observation_count=observation_count+1,condition_fingerprint=? WHERE alert_id=?`, newest.Seq, now.UnixMilli(), newest.fingerprint, ep.id)
				return err
			})
		}
		if cause := ep.reopenCause(r, now, newest, currentLagPair); cause != "" {
			if err := changes.reopen(r, key, ep.id, c, newest.fingerprint, cause, now); err != nil {
				return err
			}
		} else if ep.state == "acknowledged" && ep.hasAckCols && !ep.ackedSeq.Valid {
			var backfillLag any
			if r.When.Pattern == LagBehind {
				backfillLag = currentLagPair
			}
			*changes = append(*changes, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE alert_episodes SET acked_seq=last_observed_seq,acked_fingerprint=condition_fingerprint,acked_lag_pair=? WHERE alert_id=? AND state='acknowledged' AND acked_seq IS NULL`, backfillLag, ep.id)
				return err
			})
		}
	}
	return nil
}

func (changes *episodeChanges) resolveTerminalRemaining(rd *store.Reader, activeSnap activeSnapshot, ruleByID map[string]*Rule, reconciled map[string]bool, startHead int64, now time.Time, lookup itemTerminalChecker) error {
	if !lookup.ready || len(activeSnap.ordered) == 0 {
		return nil
	}
	for _, m := range activeSnap.ordered {
		if reconciled[m.ruleID] {
			continue
		}
		rPtr := ruleByID[m.ruleID]
		termReason, err := lookup.reason(rPtr, m.subject)
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
			_ = rd.QueryRow(
				`SELECT COALESCE(json_extract(payload, '$.receiver'), '') FROM events WHERE kind = ? AND object_id = ? ORDER BY seq DESC LIMIT 1`,
				openedKind, m.ep.id,
			).Scan(&receiver)
			if receiver == "" {
				receiver = "foreground"
			}
		}
		rMeta := Rule{
			ID:       m.ruleID,
			Severity: m.ep.severity,
			Delivery: Delivery{Receiver: receiver},
		}
		if err := changes.resolveWithReason(rMeta, m.subject, m.ep.id, m.ep.trackingItem, startHead, now, termReason); err != nil {
			return err
		}
	}
	return nil
}

func (changes *episodeChanges) open(rd *store.Reader, r Rule, key string, c *condition, newest observation, now time.Time) error {
	var previous int64
	var reason sql.NullString
	var resolvedSeq sql.NullInt64
	err := rd.QueryRow(`SELECT episode_num,resolved_reason,resolved_seq FROM alert_episodes WHERE rule_id=? AND subject_key=? ORDER BY episode_num DESC LIMIT 1`, r.ID, key).Scan(&previous, &reason, &resolvedSeq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if isSuppressiveResolutionReason(reason.String) && newest.Seq <= resolvedSeq.Int64 {
		return nil
	}
	var randomID [16]byte
	rand.Read(randomID[:])
	id := "alrt-" + hex.EncodeToString(randomID[:])
	bundle, err := makeBundle(r, key, id, c)
	if err != nil {
		return err
	}
	var lagPair any
	if r.When.Pattern == LagBehind {
		lagPair = c.lag.leaderOID + ":" + c.lag.followerOID
	}
	return changes.transition(r, key, id, openedKind, now, map[string]any{"opened_seq": newest.Seq}, func(tx *store.Tx) error {
		hasLagCol, err := alertEpisodesHasColumn(tx.Reader(), "tracked_lag_pair")
		if err != nil {
			return err
		}
		if hasLagCol {
			_, err = tx.Exec(`INSERT INTO alert_episodes (alert_id,rule_id,subject_key,episode_num,state,severity,condition_fingerprint,opened_seq,opened_at,last_observed_seq,last_observed_at,tracked_lag_pair,bundle_json)
 VALUES (?,?,?,?,'open',?,?,?,?,?,?,?,?)`, id, r.ID, key, previous+1, r.Severity, newest.fingerprint, newest.Seq, now.UnixMilli(), newest.Seq, now.UnixMilli(), lagPair, string(bundle))
			return err
		}
		_, err = tx.Exec(`INSERT INTO alert_episodes (alert_id,rule_id,subject_key,episode_num,state,severity,condition_fingerprint,opened_seq,opened_at,last_observed_seq,last_observed_at,bundle_json)
 VALUES (?,?,?,?,'open',?,?,?,?,?,?,?)`, id, r.ID, key, previous+1, r.Severity, newest.fingerprint, newest.Seq, now.UnixMilli(), newest.Seq, now.UnixMilli(), string(bundle))
		return err
	})
}

func (changes *episodeChanges) reopen(r Rule, key, id string, c *condition, fingerprint, cause string, now time.Time) error {
	bundle, err := makeBundle(r, key, id, c)
	if err != nil {
		return err
	}
	return changes.transition(r, key, id, reopenedKind, now, map[string]any{"cause": cause}, func(tx *store.Tx) error {
		hasAck, err := alertEpisodesHasColumn(tx.Reader(), "acked_seq")
		if err != nil {
			return err
		}
		if hasAck {
			_, err = tx.Exec(`UPDATE alert_episodes SET state='open',silence_until_ms=NULL,silence_on_fingerprint=NULL,
 acked_by=NULL,acked_note=NULL,acked_at=NULL,acked_seq=NULL,acked_fingerprint=NULL,acked_lag_pair=NULL,condition_fingerprint=?,bundle_json=? WHERE alert_id=?`, fingerprint, string(bundle), id)
			return err
		}
		_, err = tx.Exec(`UPDATE alert_episodes SET state='open',silence_until_ms=NULL,silence_on_fingerprint=NULL,
 acked_by=NULL,acked_note=NULL,acked_at=NULL,condition_fingerprint=?,bundle_json=? WHERE alert_id=?`, fingerprint, string(bundle), id)
		return err
	})
}

func (changes *episodeChanges) reopenTracked(r Rule, key, id string, c *condition, fingerprint string, lagPair any, cause, itemID, itemState string, now time.Time) error {
	bundle, err := makeBundleWithPrefix(r, key, id, c, trackedPersistPrefix(itemID, itemState))
	if err != nil {
		return err
	}
	body := map[string]any{
		"cause":      cause,
		"item_id":    itemID,
		"item_state": itemState,
	}
	return changes.transition(r, key, id, reopenedKind, now, body, func(tx *store.Tx) error {
		hasAck, err := alertEpisodesHasColumn(tx.Reader(), "acked_seq")
		if err != nil {
			return err
		}
		if hasAck {
			_, err = tx.Exec(`UPDATE alert_episodes SET state='open',tracking_item_id=NULL,tracked_lag_pair=?,silence_until_ms=NULL,silence_on_fingerprint=NULL,
 acked_by=NULL,acked_note=NULL,acked_at=NULL,acked_seq=NULL,acked_fingerprint=NULL,acked_lag_pair=NULL,condition_fingerprint=?,bundle_json=? WHERE alert_id=?`,
				lagPair, fingerprint, string(bundle), id)
			return err
		}
		_, err = tx.Exec(`UPDATE alert_episodes SET state='open',tracking_item_id=NULL,tracked_lag_pair=?,silence_until_ms=NULL,silence_on_fingerprint=NULL,
 acked_by=NULL,acked_note=NULL,acked_at=NULL,condition_fingerprint=?,bundle_json=? WHERE alert_id=?`,
			lagPair, fingerprint, string(bundle), id)
		return err
	})
}

func (changes *episodeChanges) resolve(r Rule, key, id string, trackingItem sql.NullString, seq int64, now time.Time) error {
	return changes.resolveWithReason(r, key, id, trackingItem, seq, now, "condition-cleared")
}

func (changes *episodeChanges) resolveWithReason(r Rule, key, id string, trackingItem sql.NullString, seq int64, now time.Time, reason string) error {
	body := map[string]any{"resolved_seq": seq, "reason": reason}
	if trackingItem.Valid && trackingItem.String != "" {
		body["item_id"] = trackingItem.String
	}
	return changes.transition(r, key, id, resolvedKind, now, body, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE alert_episodes SET state='resolved',resolved_seq=?,resolved_at=?,resolved_reason=? WHERE alert_id=?`, seq, now.UnixMilli(), reason, id)
		return err
	})
}

func (changes *episodeChanges) transition(r Rule, key, id, kind string, now time.Time, body map[string]any, update func(*store.Tx) error) error {
	body["alert_id"], body["rule_id"], body["subject_key"] = id, r.ID, key
	body["severity"], body["receiver"] = r.Severity, r.Delivery.Receiver
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	*changes = append(*changes, func(tx *store.Tx) error {
		if err := update(tx); err != nil {
			return err
		}
		_, err := tx.Append(event.Event{Kind: kind, At: now, ObjectType: "alert", ObjectID: &id, Payload: payload})
		return err
	})
	return nil
}
