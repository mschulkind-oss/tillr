// Package alarms projects operator alarms from store facts without linking the drain writer.
package alarms

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/mschulkind-oss/tillr-drain/quota"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	Closed = "backend-closed"
	Window = "backend-window"
	Judge  = "judge-backend-unavailable"
)

var eastern = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return loc
}()

func easternTime(t time.Time) string { return t.In(eastern).Format("2006-01-02 15:04:05 MST") }

type Alarm struct {
	Kind    string   `json:"kind"`
	Backend string   `json:"backend"`
	Since   string   `json:"since"`
	Cause   string   `json:"cause"`
	Clears  string   `json:"clears"`
	Command string   `json:"command"`
	Items   []string `json:"items"`
	Lanes   []string `json:"lanes"`
}

func (a Alarm) Key() string { return a.Kind + ":" + a.Backend }

func (a Alarm) String() string {
	return fmt.Sprintf("ALARM %s (%s), since %s: %s; %d item(s) [%s], %d lane(s) [%s]; clears: %s; operator: %s",
		a.Kind, a.Backend, a.Since, a.Cause, len(a.Items), strings.Join(a.Items, ", "), len(a.Lanes), strings.Join(a.Lanes, ", "), a.Clears, a.Command)
}

func Print(w io.Writer, active []Alarm) error {
	for _, a := range active {
		if _, err := fmt.Fprintln(w, a); err != nil {
			return err
		}
	}
	return nil
}

// Policy describes only the quota readers selected in the active policy.
// With no policy, recorded readings themselves describe their command and reserve.
type Policy struct {
	Backends map[string]Backend `json:"backends"`
	Since    time.Time          `json:"-"`
}

type Backend struct {
	Quota   []string `json:"quota"`
	Reserve float64  `json:"reserve"`
}

func Read(r *store.Reader, now time.Time, policy *Policy, current ...Hold) ([]Alarm, error) {
	infra, err := infrastructure(r)
	if err != nil {
		return nil, err
	}
	windows, err := quotaAlarms(r, now, policy)
	if err != nil {
		return nil, err
	}
	rolls, err := storeRollAlarm(r)
	if err != nil {
		return nil, err
	}
	active := append(append(infra, windows...), rolls...)
	if len(active) == 0 {
		return active, nil
	}
	holds, err := readHolds(r)
	if err != nil {
		return nil, err
	}
	return attribute(active, append(holds, current...)), nil
}

// The saved closes bit is the measured availability fact, not an inference from
// a nonzero exit code. A later ending or successful probe supersedes it in event order.
func infrastructure(r *store.Reader) ([]Alarm, error) {
	rows, err := r.Query(`WITH endings AS (
  SELECT e.seq, e.ts, e.payload, o.closes,
   json_extract(e.payload,'$.backend') AS backend,
   row_number() OVER (PARTITION BY json_extract(e.payload,'$.backend') ORDER BY e.seq DESC) AS newest
  FROM backend_outcomes o JOIN events e ON e.seq=o.terminal_seq
  WHERE o.class <> 'interrupted'
 ) SELECT backend, ts, json_extract(payload,'$.run_id'), json_extract(payload,'$.state'),
   coalesce('exit ' || json_extract(payload,'$.exit_code'), 'no exit code'),
   coalesce((SELECT json_extract(o.payload,'$.cause') FROM events o
     WHERE o.kind='drain.backend-outcome' AND o.object_id=endings.backend
     AND json_extract(o.payload,'$.terminal_seq')=endings.seq), '')
 FROM endings WHERE newest=1 AND closes=1 AND backend NOT IN ('', 'command')
 AND NOT EXISTS (SELECT 1 FROM events p WHERE p.kind='drain.backend-probe' AND p.object_id=backend
  AND p.seq>endings.seq AND json_extract(p.payload,'$.success')=1)
 AND NOT EXISTS (SELECT 1 FROM events p WHERE p.kind='call.offered'
  AND json_extract(p.payload,'$.terminal_seq')=endings.seq)`)
	if missingTable(err, "backend_outcomes") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alarm
	for rows.Next() {
		var name, runID, state, exit, cause string
		var since int64
		if err := rows.Scan(&name, &since, &runID, &state, &exit, &cause); err != nil {
			return nil, err
		}
		if cause == "" {
			cause = fmt.Sprintf("infrastructure failure: run %s (%s, %s)", runID, state, exit)
		}
		out = append(out, Alarm{Kind: Closed, Backend: name, Since: easternTime(time.UnixMilli(since)),
			Cause:   cause,
			Clears:  "a successful probe or a later non-infrastructure ending",
			Command: fmt.Sprintf("fix the reported cause, then tillr drain probe-backend --store <store> --backend %s -- <smoke-check>", name)})
	}
	return out, rows.Err()
}

func quotaAlarms(r *store.Reader, now time.Time, policy *Policy) ([]Alarm, error) {
	statuses, err := quota.ReadStatuses(r)
	if err != nil {
		return nil, err
	}
	byBackend := map[string]quota.QuotaStatus{}
	for _, q := range statuses {
		byBackend[q.Backend] = q
	}
	active, err := refusalWindows(r, now)
	if err != nil {
		return nil, err
	}
	configured := map[string]Backend{}
	if policy != nil {
		configured = policy.Backends
	} else {
		for name, q := range byBackend {
			configured[name] = Backend{Reserve: q.Reserve}
		}
	}
	for name, b := range configured {
		if policy != nil && len(b.Quota) == 0 {
			continue
		}
		q, found := byBackend[name]
		command, _ := json.Marshal(b.Quota)
		cause := ""
		since := q.ObservedAt
		var reset time.Time
		switch {
		case !found:
			cause = "no quota reading recorded"
			since = policy.Since
		case policy != nil && q.Command != string(command):
			cause = "no reading for the active quota command"
		case q.Error != "":
			cause = "quota probe failed: " + q.Error
		case now.Before(q.ObservedAt.Truncate(time.Second)) || now.Sub(q.ObservedAt) > quota.ProbeInterval:
			cause = "quota reading is stale"
		default:
			var limits []string
			for _, w := range q.Windows {
				if w.ResetAt != nil && !w.ResetAt.After(now) {
					cause = "quota reading predates the provider reset"
					break
				}
				if w.Fraction >= 1-b.Reserve {
					limits = append(limits, fmt.Sprintf("%s quota usage %g reached the admission limit %g", w.Kind, w.Fraction, 1-b.Reserve))
					if w.ResetAt != nil && w.ResetAt.After(reset) {
						reset = *w.ResetAt
					}
				}
			}
			if cause == "" {
				cause = strings.Join(limits, "; ")
			} else {
				reset = time.Time{}
			}
		}
		if cause == "" {
			continue
		}
		clears := "a fresh successful quota reading below the reserve limit"
		if !reset.IsZero() {
			clears += "; provider reset " + easternTime(reset)
		}
		if old, ok := active[name]; ok {
			old.Cause += "; " + cause
			old.Clears += " and " + clears
			active[name] = old
			continue
		}
		sinceText := "not recorded"
		if !since.IsZero() {
			sinceText = easternTime(since)
		}
		active[name] = Alarm{Kind: Window, Backend: name, Since: sinceText, Cause: cause, Clears: clears, Command: quotaMove}
	}
	out := make([]Alarm, 0, len(active))
	for _, a := range active {
		out = append(out, a)
	}
	return out, nil
}

const quotaMove = "wait for the provider reset or repair the quota probe; inspect with tillr view waiting --store <store>, or deliberately edit the backend policy"

func refusalWindows(r *store.Reader, now time.Time) (map[string]Alarm, error) {
	out := map[string]Alarm{}
	rows, err := r.Query(`SELECT w.backend,w.kind,w.run_id,w.reset_at,
  (SELECT max(e.ts) FROM events e WHERE e.kind='run.finish' AND e.object_id=w.run_id)
  FROM backend_windows w WHERE w.reset_at>?`, now.UnixMilli())
	if missingTable(err, "backend_windows") {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, kind, runID string
		var reset int64
		var since *int64
		if err := rows.Scan(&name, &kind, &runID, &reset, &since); err != nil {
			return nil, err
		}
		stamp := "not recorded"
		if since != nil {
			stamp = easternTime(time.UnixMilli(*since))
		}
		out[name] = Alarm{Kind: Window, Backend: name, Since: stamp, Cause: fmt.Sprintf("%s quota refusal recorded by run %s", kind, runID),
			Clears: "provider reset " + easternTime(time.UnixMilli(reset)), Command: quotaMove}
	}
	return out, rows.Err()
}

func missingTable(err error, name string) bool {
	return err != nil && strings.Contains(err.Error(), "no such table: "+name)
}

// Hold is an observed item decision used only to attribute an active alarm.
type Hold struct {
	Item, Lane, Reason, Backends string
	Judge                        bool
}

func readHolds(r *store.Reader) ([]Hold, error) {
	// Old stores may lack dispatch metadata. Those stores cannot attribute a
	// backend hold, but can still surface measured backend facts.
	rows, err := r.Query(`WITH latest AS (
  SELECT *,row_number() OVER (PARTITION BY item_id ORDER BY dispatched_at DESC,rowid DESC) AS newest FROM dispatches
 ) SELECT d.item_id,coalesce(d.lane_id,''),d.reason,coalesce(d.deferred_by,''),
 coalesce(d.seat='review' AND EXISTS (SELECT 1 FROM events c WHERE c.object_id=d.run_id
  AND c.kind IN ('drain.candidate','drain.prepared-candidate')),0)
 FROM latest d JOIN items i ON i.item_id=d.item_id
 WHERE newest=1 AND action='hold' AND reason IN ('backend-closed','backend-window')
 AND i.state IN ('in-queue','in-progress','blocked')`)
	var out []Hold
	if err == nil {
		for rows.Next() {
			var h Hold
			if err := rows.Scan(&h.Item, &h.Lane, &h.Reason, &h.Backends, &h.Judge); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, h)
		}
		err = rows.Err()
		rows.Close()
	}
	if err != nil && !missingTable(err, "dispatches") && !missingTable(err, "items") && !strings.Contains(err.Error(), "no such column: d.") {
		return nil, err
	}
	rows, err = r.Query(`SELECT p.item_id,coalesce(l.lane_id,''),p.backend FROM quota_pauses p
  LEFT JOIN lanes l ON l.lane_id=p.lane_id AND l.run_id=p.run_id WHERE p.state='waiting'`)
	if missingTable(err, "quota_pauses") {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		h := Hold{Reason: Window}
		if err := rows.Scan(&h.Item, &h.Lane, &h.Backends); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func attribute(active []Alarm, holds []Hold) []Alarm {
	judges := map[string]Alarm{}
	for i, a := range active {
		items, lanes := map[string]bool{}, map[string]bool{}
		for _, h := range holds {
			if h.Reason != a.Kind {
				continue
			}
			matched := false
			for _, name := range strings.Split(h.Backends, ",") {
				matched = matched || strings.TrimSpace(name) == a.Backend
			}
			if !matched {
				continue
			}
			items[h.Item] = true
			if h.Lane != "" {
				lanes[h.Lane] = true
			}
			if h.Judge {
				j, ok := judges[a.Backend]
				if !ok {
					j = a
					j.Kind = Judge
					j.Cause = "review seat: " + a.Cause
				}
				if ok && !strings.Contains(j.Cause, a.Cause) {
					j.Cause += "; " + a.Cause
					j.Clears += "; " + a.Clears
					j.Command += "; " + a.Command
				}
				j.Items = appendUnique(j.Items, h.Item)
				if h.Lane != "" {
					j.Lanes = appendUnique(j.Lanes, h.Lane)
				}
				judges[a.Backend] = j
			}
		}
		a.Items, a.Lanes = sorted(items), sorted(lanes)
		active[i] = a
	}
	for _, j := range judges {
		sort.Strings(j.Items)
		if j.Lanes == nil {
			j.Lanes = []string{}
		}
		sort.Strings(j.Lanes)
		active = append(active, j)
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Key() < active[j].Key() })
	return active
}

func appendUnique(xs []string, x string) []string {
	for _, s := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, x)
}
func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for x := range set {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}
