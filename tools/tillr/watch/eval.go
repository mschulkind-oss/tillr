package watch

import (
	"context"
	"encoding/json"
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
	Rules []Rule
	Clock func() time.Time
}

func (e Evaluator) now() time.Time {
	if e.Clock != nil {
		return e.Clock()
	}
	return time.Now()
}
func (r Rule) evaluated() bool { return r.Enabled && r.UnevaluatedReason() == "" }

// Skipped derives per-rule counts without changing the store.
func (e Evaluator) Skipped(ctx context.Context) (map[string]int, error) {
	now := e.now()
	counts := map[string]int{}
	err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		for _, r := range e.Rules {
			if !r.evaluated() {
				continue
			}
			_, n, err := derive(rd, r, now)
			if err != nil {
				return fmt.Errorf("rule %s: %w", r.ID, err)
			}
			counts[r.ID] = n
		}
		return nil
	})
	return counts, err
}

// Evaluate reconciles episodes and returns the same skipped-event counts as Skipped.
func (e Evaluator) Evaluate(ctx context.Context) (map[string]int, error) {
	now := e.now()
	counts := map[string]int{}
	err := e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		rd := tx.Reader()
		head, err := rd.EventHeadSeq()
		if err != nil {
			return err
		}
		// Derive all conditions before appending any watch events. Every rule sees
		// the facts at the start of this pass, even if it reads watch event kinds.
		derived := make([]map[string]*condition, len(e.Rules))
		for i, r := range e.Rules {
			if !r.evaluated() {
				continue
			}
			subjects, n, err := derive(rd, r, now)
			if err != nil {
				return fmt.Errorf("rule %s: %w", r.ID, err)
			}
			counts[r.ID] = n
			derived[i] = subjects
		}
		for i, subjects := range derived {
			if subjects != nil {
				r := e.Rules[i]
				if err := reconcile(tx, r, subjects, head, now); err != nil {
					return fmt.Errorf("rule %s: %w", r.ID, err)
				}
			}
		}
		return nil
	})
	return counts, err
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

func reconcile(tx *store.Tx, r Rule, subjects map[string]*condition, head int64, now time.Time) error {
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
		firing := int64(len(c.events)) >= r.When.Threshold
		firstClear := sort.Search(len(c.clears), func(i int) bool { return c.clears[i] > ep.opened })
		if exists && (!firing || firstClear < len(c.clears)) {
			seq := head
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
		newest := c.events[len(c.events)-1]
		if !exists {
			if err := openEpisode(tx, r, key, c, newest, now); err != nil {
				return err
			}
		} else if newest.Seq > ep.last {
			if _, err := tx.Exec(`UPDATE alert_episodes SET last_observed_seq=?,last_observed_at=?,observation_count=observation_count+1 WHERE alert_id=?`, newest.Seq, now.UnixMilli(), ep.id); err != nil {
				return err
			}
		}
	}
	return nil
}

func openEpisode(tx *store.Tx, r Rule, key string, c *condition, newest observation, now time.Time) error {
	var n int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(episode_num),0)+1 FROM alert_episodes WHERE rule_id=? AND subject_key=?`, r.ID, key).Scan(&n); err != nil {
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
 VALUES (?,?,?,?,'open',?,'',?,?,?,?,?)`, id, r.ID, key, n, r.Severity, newest.Seq, now.UnixMilli(), newest.Seq, now.UnixMilli(), string(bundle)); err != nil {
		return err
	}
	return appendTransition(tx, r, key, id, openedKind, newest.Seq, now)
}

func resolve(tx *store.Tx, r Rule, key, id string, seq int64, now time.Time) error {
	if _, err := tx.Exec(`UPDATE alert_episodes SET state='resolved',resolved_seq=?,resolved_at=?,resolved_reason='condition-cleared' WHERE alert_id=?`, seq, now.UnixMilli(), id); err != nil {
		return err
	}
	return appendTransition(tx, r, key, id, resolvedKind, seq, now)
}

func appendTransition(tx *store.Tx, r Rule, key, id, kind string, seq int64, now time.Time) error {
	body := map[string]any{"alert_id": id, "rule_id": r.ID, "subject_key": key, "severity": r.Severity, "receiver": r.Delivery.Receiver}
	if kind == openedKind {
		body["opened_seq"] = seq
	} else {
		body["resolved_seq"] = seq
		body["reason"] = "condition-cleared"
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: kind, At: now, ObjectType: "alert", ObjectID: &id, Payload: payload})
	return err
}
