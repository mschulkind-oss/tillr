package watch

import (
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
	for _, kind := range []string{openedKind, resolvedKind, acknowledgedKind, silencedKind, reopenedKind} {
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
 silence_until_ms INTEGER,
 silence_on_fingerprint TEXT,
 active_triage_run_id TEXT,
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
` + ruleOverridesDDL

// Evaluator derives rule conditions from a single store snapshot per pass.
// Only the watch episode transitions and their corresponding events are written.
type Evaluator struct {
	Store *store.Store
	Rules []Rule // validated file rules
	// Root is the repository whose Git refs projections read. It must be explicit.
	Root  string
	Clock func() time.Time

	// Test seams for concurrent commits before and during planning, and for
	// observing event queries and Git subprocesses without timing.
	afterGit, afterPlan func()
	onEventQuery        func(kind string, sinceSeq int64, rows int)
	onGitExec           func(args []string)

	cache *evalCache
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
	git, err := readGit(ctx, e.Root, rules, nil, e.onGitExec)
	if err != nil {
		return err
	}
	return e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		results, _, err := e.deriveRules(rd, rules, git, now, nil)
		if err != nil {
			return err
		}
		return visit(rd, results)
	})
}

// CheckRules validates effective rules before wait records any delivery state.
// The actual evaluation checks again against the rules of that pass.
func (e Evaluator) CheckRules(ctx context.Context) error {
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
	if e.cache == nil || e.cache.store != e.Store {
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
		git, err := readGit(ctx, e.Root, rules, &e.cache.git, e.onGitExec)
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
			var derived []map[string]*condition
			results, derived, err = e.deriveRules(rd, rules, git, now, e.cache)
			if err != nil {
				return nil, err
			}
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
				if err := changes.reconcile(rd, rule, subjects, startHead, now); err != nil {
					return nil, fmt.Errorf("rule %s: %w", rule.ID, err)
				}
			}
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

func (e Evaluator) deriveRules(rd *store.Reader, rules []Rule, git gitSnapshot, now time.Time, cache *evalCache) ([]RuleResult, []map[string]*condition, error) {
	if cache != nil {
		if err := cache.syncKinds(rd, rules, now, e.onEventQuery); err != nil {
			return nil, nil, err
		}
	}
	facts := readStoreFacts(rd)
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
		var subjects map[string]*condition
		var n int
		if cache != nil {
			subjects, n, err = deriveCached(rd, r, now, rf, cache)
		} else {
			subjects, n, err = derive(rd, r, now, rf)
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

type episode struct {
	id, state    string
	opened, last int64
	silenceUntil sql.NullInt64
	silenceOn    sql.NullString
}

func (ep episode) reopenCause(now time.Time, fingerprint string) string {
	if ep.state == "silenced" {
		if ep.silenceUntil.Valid && !now.Before(time.UnixMilli(ep.silenceUntil.Int64)) {
			return "silence-expired"
		}
		if ep.silenceOn.Valid && ep.silenceOn.String != fingerprint {
			return "fingerprint-changed"
		}
	}
	return ""
}

func active(rd *store.Reader, ruleID string) (map[string]episode, error) {
	rows, err := rd.Query(`SELECT subject_key,alert_id,opened_seq,last_observed_seq,state,silence_until_ms,silence_on_fingerprint FROM alert_episodes WHERE rule_id=? AND state!='resolved'`, ruleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	episodes := map[string]episode{}
	for rows.Next() {
		var key string
		var ep episode
		if err := rows.Scan(&key, &ep.id, &ep.opened, &ep.last, &ep.state, &ep.silenceUntil, &ep.silenceOn); err != nil {
			return nil, err
		}
		episodes[key] = ep
	}
	return episodes, rows.Err()
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

func (changes *episodeChanges) reconcile(rd *store.Reader, r Rule, subjects map[string]*condition, startHead int64, now time.Time) error {
	episodes, err := active(rd, r.ID)
	if err != nil {
		return err
	}
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
		firing := r.Enabled && c.fires(r.When)
		firstClear := sort.Search(len(c.clears), func(i int) bool { return c.clears[i] > ep.opened })
		if exists && (!firing || firstClear < len(c.clears)) {
			seq := startHead
			if firstClear < len(c.clears) {
				seq = c.clears[firstClear]
			}
			if err := changes.resolve(r, key, ep.id, seq, now); err != nil {
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
			if err := changes.open(rd, r, key, c, newest, now); err != nil {
				return err
			}
			continue
		}
		if r.When.Pattern != LagBehind && newest.Seq > ep.last {
			*changes = append(*changes, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE alert_episodes SET last_observed_seq=?,last_observed_at=?,observation_count=observation_count+1,condition_fingerprint=? WHERE alert_id=?`, newest.Seq, now.UnixMilli(), newest.fingerprint, ep.id)
				return err
			})
		}
		if cause := ep.reopenCause(now, newest.fingerprint); cause != "" {
			if err := changes.reopen(r, key, ep.id, c, newest.fingerprint, cause, now); err != nil {
				return err
			}
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
	if reason.String == "manual" && newest.Seq <= resolvedSeq.Int64 {
		return nil
	}
	var randomID [16]byte
	rand.Read(randomID[:])
	id := "alrt-" + hex.EncodeToString(randomID[:])
	bundle, err := makeBundle(r, key, id, c)
	if err != nil {
		return err
	}
	return changes.transition(r, key, id, openedKind, now, map[string]any{"opened_seq": newest.Seq}, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO alert_episodes (alert_id,rule_id,subject_key,episode_num,state,severity,condition_fingerprint,opened_seq,opened_at,last_observed_seq,last_observed_at,bundle_json)
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
		_, err := tx.Exec(`UPDATE alert_episodes SET state='open',silence_until_ms=NULL,silence_on_fingerprint=NULL,
 acked_by=NULL,acked_note=NULL,acked_at=NULL,condition_fingerprint=?,bundle_json=? WHERE alert_id=?`, fingerprint, string(bundle), id)
		return err
	})
}

func (changes *episodeChanges) resolve(r Rule, key, id string, seq int64, now time.Time) error {
	return changes.transition(r, key, id, resolvedKind, now, map[string]any{"resolved_seq": seq, "reason": "condition-cleared"}, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE alert_episodes SET state='resolved',resolved_seq=?,resolved_at=?,resolved_reason='condition-cleared' WHERE alert_id=?`, seq, now.UnixMilli(), id)
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
