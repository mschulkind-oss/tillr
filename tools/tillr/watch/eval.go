package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
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
	for _, kind := range []string{openedKind, resolvedKind} {
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
`

// Evaluator derives rule conditions from a single store snapshot per pass.
// Only the watch episode transitions and their corresponding events are written.
type Evaluator struct {
	Store *store.Store
	Rules []Rule // validated file rules
	// Root is the repository whose Git refs projections read. It must be explicit.
	Root  string
	Clock func() time.Time

	// Test seam: a concurrent event can commit after Git and before WriteTx.
	afterGit func()
}

func (e Evaluator) now() time.Time {
	if e.Clock != nil {
		return e.Clock()
	}
	return time.Now()
}

// RuleResult describes an evaluated rule and any runtime unreadability.
type RuleResult struct {
	ID                      string
	Disabled                bool
	Skipped                 int
	Unsupported, Unreadable string
}

func (r RuleResult) Diagnostic() string {
	switch {
	case r.Unsupported != "":
		return r.Unsupported
	case r.Unreadable != "":
		return r.Unreadable
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
func (e Evaluator) Evaluate(ctx context.Context) (map[string]int, error) {
	e.Rules = commandRules(e.Rules)
	results, err := e.EvaluateRules(ctx)
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
	now := e.now()
	git, err := readGit(ctx, e.Root, e.Rules)
	if err != nil {
		return nil, err
	}
	var results []RuleResult
	err = e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		var err error
		results, _, err = e.deriveRules(rd, e.Rules, git, now)
		return err
	})
	return results, err
}

// EvaluateRules reports unreadable projections while leaving their episodes untouched.
func (e Evaluator) EvaluateRules(ctx context.Context) ([]RuleResult, error) {
	now := e.now()
	var startHead int64
	if err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		var err error
		startHead, err = rd.EventHeadSeq()
		return err
	}); err != nil {
		return nil, err
	}
	git, err := readGit(ctx, e.Root, e.Rules)
	if err != nil {
		return nil, err
	}
	if e.afterGit != nil {
		e.afterGit()
	}
	var results []RuleResult
	err = e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		var derived []map[string]*condition
		var err error
		results, derived, err = e.deriveRules(tx.Reader(), e.Rules, git, now)
		if err != nil {
			return err
		}
		for i, subjects := range derived {
			if subjects == nil {
				continue
			}
			if err := reconcile(tx, e.Rules[i], subjects, startHead, now); err != nil {
				return fmt.Errorf("rule %s: %w", e.Rules[i].ID, err)
			}
		}
		return nil
	})
	return results, err
}

func (e Evaluator) deriveRules(rd *store.Reader, rules []Rule, git gitSnapshot, now time.Time) ([]RuleResult, []map[string]*condition, error) {
	facts := readStoreFacts(rd)
	results := make([]RuleResult, len(rules))
	derived := make([]map[string]*condition, len(rules))
	for i, r := range rules {
		result := &results[i]
		*result = RuleResult{ID: r.ID, Disabled: !r.Enabled, Unsupported: r.evaluatorReason()}
		if result.Disabled || result.Unsupported != "" {
			continue
		}
		rf, err := facts.forRule(r, git)
		if err != nil {
			result.Unreadable = err.Error()
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
		subjects, n, err := derive(rd, r, now, rf)
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
	id           string
	opened, last int64
}

func active(tx *store.Tx, ruleID string) (map[string]episode, error) {
	rows, err := tx.Query(`SELECT subject_key,alert_id,opened_seq,last_observed_seq FROM alert_episodes WHERE rule_id=? AND state!='resolved'`, ruleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	episodes := map[string]episode{}
	for rows.Next() {
		var key string
		var ep episode
		if err := rows.Scan(&key, &ep.id, &ep.opened, &ep.last); err != nil {
			return nil, err
		}
		episodes[key] = ep
	}
	return episodes, rows.Err()
}

func reconcile(tx *store.Tx, r Rule, subjects map[string]*condition, startHead int64, now time.Time) error {
	episodes, err := active(tx, r.ID)
	if err != nil {
		return err
	}
	for key := range episodes {
		if subjects[key] == nil {
			subjects[key] = &condition{}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(subjects)) {
		c := subjects[key]
		ep, exists := episodes[key]
		firing := r.Enabled && c.fires(r.When)
		firstClear := sort.Search(len(c.clears), func(i int) bool { return c.clears[i] > ep.opened })
		if exists && (!firing || firstClear < len(c.clears)) {
			seq := startHead
			if firstClear < len(c.clears) {
				seq = c.clears[firstClear]
			}
			if err := resolve(tx, r, key, ep.id, seq, now); err != nil {
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
			if err := openEpisode(tx, r, key, c, newest, now); err != nil {
				return err
			}
			continue
		}
		if r.When.Pattern != LagBehind && newest.Seq > ep.last {
			if _, err := tx.Exec(`UPDATE alert_episodes SET last_observed_seq=?,last_observed_at=?,observation_count=observation_count+1 WHERE alert_id=?`, newest.Seq, now.UnixMilli(), ep.id); err != nil {
				return err
			}
		}
	}
	return nil
}

func openEpisode(tx *store.Tx, r Rule, key string, c *condition, newest observation, now time.Time) error {
	var previous int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(episode_num),0) FROM alert_episodes WHERE rule_id=? AND subject_key=?`, r.ID, key).Scan(&previous); err != nil {
		return err
	}

	var id string
	if err := tx.QueryRow(`SELECT 'alrt-' || lower(hex(randomblob(16)))`).Scan(&id); err != nil {
		return err
	}
	bundle, err := makeBundle(r, key, id, c)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO alert_episodes (alert_id,rule_id,subject_key,episode_num,state,severity,condition_fingerprint,opened_seq,opened_at,last_observed_seq,last_observed_at,bundle_json)
 VALUES (?,?,?,?,'open',?,?,?,?,?,?,?)`, id, r.ID, key, previous+1, r.Severity, "", newest.Seq, now.UnixMilli(), newest.Seq, now.UnixMilli(), string(bundle)); err != nil {
		return err
	}
	return appendTransition(tx, r, key, id, openedKind, now, map[string]any{"opened_seq": newest.Seq})
}

func resolve(tx *store.Tx, r Rule, key, id string, seq int64, now time.Time) error {
	if _, err := tx.Exec(`UPDATE alert_episodes SET state='resolved',resolved_seq=?,resolved_at=?,resolved_reason='condition-cleared' WHERE alert_id=?`, seq, now.UnixMilli(), id); err != nil {
		return err
	}
	return appendTransition(tx, r, key, id, resolvedKind, now, map[string]any{"resolved_seq": seq, "reason": "condition-cleared"})
}

func appendTransition(tx *store.Tx, r Rule, key, id, kind string, now time.Time, body map[string]any) error {
	body["alert_id"], body["rule_id"], body["subject_key"] = id, r.ID, key
	body["severity"], body["receiver"] = r.Severity, r.Delivery.Receiver
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: kind, At: now, ObjectType: "alert", ObjectID: &id, Payload: payload})
	return err
}
