package watch

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	itemColumns          = `SELECT state,seat,waiting_on FROM items`
	workflowColumn       = `SELECT workflow FROM workflow_selections`
	defaultStorePollTick = 60 * time.Second
)

// TillrStoreSource adapts a .tillr/state.db store into an ObservationSource,
// isolating events queries, items/charter/workflow_selections projections, and
// core/charter:*/workflow:* scope checks from the source-neutral engine.
type TillrStoreSource struct {
	Store *store.Store
	Clock func() time.Time
}

// NewTillrStoreSource wraps s as an ObservationSource.
func NewTillrStoreSource(s *store.Store) *TillrStoreSource {
	return &TillrStoreSource{Store: s}
}

func (s *TillrStoreSource) now() time.Time {
	if s != nil && s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func (s *TillrStoreSource) checkIO(what string) error {
	if s != nil && s.Store != nil {
		return store.CheckIO(what)
	}
	return nil
}

// ReadWindow returns matching events from .tillr/state.db in [sinceMillis, now]
// ordered by ascending sequence number.
func (s *TillrStoreSource) ReadWindow(ctx context.Context, sinceMillis int64, kinds []string) ([]Observation, SourceCursor, SkippedLines, error) {
	if s == nil || s.Store == nil {
		return nil, SourceCursor{}, 0, fmt.Errorf("tillr store source requires a non-nil Store")
	}
	nowMillis := s.now().UnixMilli()
	var (
		out  []Observation
		head int64
	)
	err := s.Store.ReadTx(ctx, func(rd *store.Reader) error {
		var err error
		head, err = rd.EventHeadSeq()
		if err != nil || head == 0 || len(kinds) == 0 {
			return err
		}
		args := make([]any, 0, 2+len(kinds))
		args = append(args, sinceMillis, nowMillis)
		for _, k := range kinds {
			args = append(args, k)
		}
		rows, err := rd.Query(`SELECT seq,ts,kind,seat,object_type,object_id,revision,payload FROM events
 WHERE ts >= ? AND ts <= ? AND kind IN (?`+strings.Repeat(",?", len(kinds)-1)+`) ORDER BY seq`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			obs, err := scanTillrObservation(rows)
			if err != nil {
				return err
			}
			out = append(out, obs)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, SourceCursor{}, 0, err
	}
	return out, SourceCursor{Epoch: 1, Seq: head}, 0, nil
}

// Project reads sidecar projections ("charter", "items", "workflow_selections")
// from .tillr/state.db in a single read transaction.
func (s *TillrStoreSource) Project(ctx context.Context, names []string) (map[string]any, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("tillr store source requires a non-nil Store")
	}
	out := map[string]any{}
	if len(names) == 0 {
		return out, nil
	}
	err := s.Store.ReadTx(ctx, func(rd *store.Reader) error {
		for _, name := range names {
			switch name {
			case "charter":
				rec, err := definition.ForReader(rd)
				if err != nil {
					return unreadableProjection{"charter", err}
				}
				d := rec.Parsed
				for k, v := range charterProjectionMap(CharterFacts{
					Name: d.Name, Ready: d.Ready, Blocked: d.Blocked,
					Closed: d.Closed, States: d.States, Finished: d.Finished,
				}) {
					out[k] = v
				}
			case "items":
				items, err := loadAllItems(rd)
				if err != nil {
					return unreadableProjection{"items", err}
				}
				itemMap := make(map[string]map[string]any, len(items))
				for id, f := range items {
					entry := map[string]any{}
					if f.value.state.Valid {
						entry["item.state"] = f.value.state.String
					}
					if f.value.seat.Valid {
						entry["item.seat"] = f.value.seat.String
					}
					if f.value.waitingOn.Valid {
						entry["item.waiting_on"] = f.value.waitingOn.String
					}
					itemMap[id] = entry
				}
				out["items"] = itemMap
			case "workflow_selections":
				wfs, err := loadAllWorkflows(rd)
				if err != nil {
					return unreadableProjection{"workflow_selections", err}
				}
				wfMap := make(map[string]string, len(wfs))
				for id, wf := range wfs {
					if wf.value.Valid {
						wfMap[id] = wf.value.String
					}
				}
				out["workflow_selections"] = wfMap
			default:
				return fmt.Errorf("unsupported projection %q", name)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// WatchTargets returns the SQLite WAL path and fallback poll interval for wake.
func (s *TillrStoreSource) WatchTargets() ([]string, time.Duration) {
	if s == nil || s.Store == nil || s.Store.Path() == "" {
		return nil, defaultStorePollTick
	}
	return []string{s.Store.Path() + "-wal"}, defaultStorePollTick
}

func (s *TillrStoreSource) prepareRule(ctx context.Context, r Rule, g gitSnapshot) (subjectFacts, string, string, error) {
	if s == nil || s.Store == nil {
		return nil, "", "", fmt.Errorf("tillr store source requires a non-nil Store")
	}
	if r.Scope != "" && r.Scope != "*" && !isTillrScope(r.Scope) {
		return nil, "", fmt.Sprintf("unsupported scope %q", r.Scope), nil
	}
	var (
		facts      tillrRuleFacts
		outOfScope string
	)
	err := s.Store.ReadTx(ctx, func(rd *store.Reader) error {
		sf := s.readFacts(rd)
		var err error
		facts, outOfScope, err = sf.forRule(r, g)
		if err != nil || outOfScope != "" {
			return err
		}
		if facts.items {
			items, err := loadAllItems(rd)
			if err != nil {
				return unreadableProjection{"items", err}
			}
			sf.items = items
			sf.itemsLoaded = true
		}
		if facts.workflow != "" {
			wfs, err := loadAllWorkflows(rd)
			if err != nil {
				return unreadableProjection{"workflow_selections", err}
			}
			sf.workflows = wfs
			sf.workflowsLoaded = true
		}
		sf.rd = nil
		return nil
	})
	if err != nil {
		return nil, "", "", err
	}
	return facts, outOfScope, "", nil
}

func loadAllItems(rd *store.Reader) (map[string]projectionFact[itemFacts], error) {
	rows, err := rd.Query(`SELECT item_id, state, seat, waiting_on FROM items`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]projectionFact[itemFacts]{}
	for rows.Next() {
		var id string
		var f itemFacts
		if err := rows.Scan(&id, &f.state, &f.seat, &f.waitingOn); err != nil {
			return nil, err
		}
		out[id] = projectionFact[itemFacts]{value: f}
	}
	return out, rows.Err()
}

func loadAllWorkflows(rd *store.Reader) (map[string]projectionFact[sql.NullString], error) {
	rows, err := rd.Query(`SELECT item_id, workflow FROM workflow_selections`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]projectionFact[sql.NullString]{}
	for rows.Next() {
		var id string
		var wf sql.NullString
		if err := rows.Scan(&id, &wf); err != nil {
			return nil, err
		}
		out[id] = projectionFact[sql.NullString]{value: wf}
	}
	return out, rows.Err()
}

type itemFacts struct{ state, seat, waitingOn sql.NullString }

// storeFacts live for one snapshot, including negative lookups. A rule never
// sees different values for the same subject, even across event kinds.
type storeFacts struct {
	rd                           *store.Reader
	charter                      func() (CharterFacts, error)
	checkItems, checkWorkflows   func() error
	items                        map[string]projectionFact[itemFacts]
	workflows                    map[string]projectionFact[sql.NullString]
	itemsLoaded, workflowsLoaded bool
}

func readStoreFacts(rd *store.Reader) *storeFacts {
	return (*TillrStoreSource)(nil).readFacts(rd)
}

func (s *TillrStoreSource) readFacts(rd *store.Reader) *storeFacts {
	probe := func(query string) func() error {
		return sync.OnceValue(func() error {
			rows, err := rd.Query(query + " WHERE item_id IS NULL LIMIT 0")
			if err != nil {
				return err
			}
			return rows.Close()
		})
	}
	charter := sync.OnceValues(func() (CharterFacts, error) {
		rec, err := definition.ForReader(rd)
		if err != nil {
			return CharterFacts{}, err
		}
		d := rec.Parsed
		return CharterFacts{d.Name, d.Ready, d.Blocked, d.Closed, d.States, d.Finished}, nil
	})
	return &storeFacts{
		rd: rd, charter: charter,
		checkItems: probe(itemColumns), checkWorkflows: probe(workflowColumn),
		items: map[string]projectionFact[itemFacts]{}, workflows: map[string]projectionFact[sql.NullString]{},
	}
}

func (p *storeFacts) item(subject string) (itemFacts, error) {
	fact, ok := p.items[subject]
	if !ok {
		if p.itemsLoaded {
			return itemFacts{}, nil
		}
		fact.err = p.rd.QueryRow(itemColumns+` WHERE item_id=?`, subject).Scan(&fact.value.state, &fact.value.seat, &fact.value.waitingOn)
		if errors.Is(fact.err, sql.ErrNoRows) {
			fact = projectionFact[itemFacts]{}
		}
		p.items[subject] = fact
	}
	return fact.value, fact.err
}

func (p *storeFacts) workflow(subject string) (sql.NullString, error) {
	fact, ok := p.workflows[subject]
	if !ok {
		if p.workflowsLoaded {
			return sql.NullString{}, nil
		}
		fact.err = p.rd.QueryRow(workflowColumn+` WHERE item_id=?`, subject).Scan(&fact.value)
		if errors.Is(fact.err, sql.ErrNoRows) {
			fact = projectionFact[sql.NullString]{}
		}
		p.workflows[subject] = fact
	}
	return fact.value, fact.err
}

type tillrRuleFacts struct {
	store    *storeFacts
	git      gitSnapshot
	charter  map[string]any
	workflow string
	items    bool
}

func charterProjectionMap(d CharterFacts) map[string]any {
	return map[string]any{
		"charter.name":     d.Name,
		"charter.ready":    d.Ready,
		"charter.blocked":  d.Blocked,
		"charter.closed":   d.Closed,
		"charter.states":   textList(d.States),
		"charter.finished": textList(d.Finished),
	}
}

// forRule checks the charter scope first. An out-of-scope rule does not read
// store projections. Git was acquired outside the store transaction.
func (p *storeFacts) forRule(r Rule, g gitSnapshot) (facts tillrRuleFacts, outOfScope string, err error) {
	facts = tillrRuleFacts{
		store:    p,
		git:      g,
		workflow: namedScope(r.Scope, "workflow:"),
		items:    slices.Contains(r.Reads.Projections, "items"),
	}
	name := namedScope(r.Scope, "charter:")
	readsCharter := slices.Contains(r.Reads.Projections, "charter")
	if name != "" || readsCharter {
		d, err := p.charter()
		if err != nil {
			return facts, "", unreadableProjection{"charter", err}
		}
		if name != "" && name != d.Name {
			return facts, d.Name, nil
		}
		if readsCharter {
			facts.charter = charterProjectionMap(d)
		}
	}
	if err := g.ruleError(r); err != nil {
		return facts, "", err
	}
	if facts.items {
		if err := p.checkItems(); err != nil {
			return facts, "", unreadableProjection{"items", err}
		}
	}
	if facts.workflow != "" {
		if err := p.checkWorkflows(); err != nil {
			return facts, "", unreadableProjection{"workflow_selections", err}
		}
	}
	return facts, "", nil
}

func (p tillrRuleFacts) forSubject(subject string) (lookup func(projectionOperand) any, inScope bool, err error) {
	if p.workflow != "" {
		workflow, err := p.store.workflow(subject)
		if err != nil {
			return nil, false, unreadableProjection{"workflow_selections", err}
		}
		if !workflow.Valid || workflow.String != p.workflow {
			return nil, false, nil
		}
	}
	var item itemFacts
	if p.items {
		item, err = p.store.item(subject)
		if err != nil {
			return nil, false, unreadableProjection{"items", err}
		}
	}
	return func(op projectionOperand) any {
		var column sql.NullString
		switch op.name {
		case "item.state":
			column = item.state
		case "item.seat":
			column = item.seat
		case "item.waiting_on":
			column = item.waitingOn
		case "git.ref":
			return p.git.refs[op.ref].value
		default:
			return p.charter[op.name]
		}
		if !column.Valid {
			return nil
		}
		return column.String
	}, true, nil
}

func scanTillrObservation(rows *sql.Rows) (Observation, error) {
	var (
		obs     Observation
		seat    sql.NullString
		typ     sql.NullString
		id      sql.NullString
		rev     sql.NullInt64
		raw     []byte
		payload any
	)
	if err := rows.Scan(&obs.Seq, &obs.TS, &obs.Kind, &seat, &typ, &id, &rev, &raw); err != nil {
		return Observation{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return Observation{}, err
	}
	fields, _ := payload.(map[string]any)
	if seat.Valid || typ.Valid || id.Valid || rev.Valid {
		if fields == nil {
			fields = make(map[string]any, 4)
		}
		setEnvelope := func(key string, val any) {
			if _, exists := fields[key]; !exists {
				fields[key] = val
			}
		}
		if seat.Valid {
			setEnvelope("seat", seat.String)
		}
		if typ.Valid {
			setEnvelope("object_type", typ.String)
		}
		if id.Valid {
			setEnvelope("object_id", id.String)
		}
		if rev.Valid {
			setEnvelope("revision", rev.Int64)
		}
	}
	obs.Fields = fields
	return obs, nil
}

func (s *TillrStoreSource) syncKinds(rd *store.Reader, c *evalCache, rules []Rule, now time.Time, baselines map[string]ruleBaseline, defaultHead int64, onQuery func(kind string, sinceSeq int64, rows int)) error {
	kindWindowStart := map[string]int64{}
	kindMinSeq := map[string]int64{}
	for _, r := range rules {
		if !r.Enabled || r.evaluatorReason() != "" || r.When.Pattern == LagBehind {
			continue
		}
		ws := windowStartMillis(r.When, now)
		fls := effectiveFirstLoadedSeq(r, baselines, defaultHead)
		for _, k := range r.Reads.Events {
			if prev, ok := kindWindowStart[k]; !ok || ws < prev {
				kindWindowStart[k] = ws
			}
			if prev, ok := kindMinSeq[k]; !ok || fls < prev {
				kindMinSeq[k] = fls
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
		minSeq := kindMinSeq[kind]
		kc := c.kinds[kind]
		if kc != nil && snapHead >= kc.lastSeq && windowStart >= kc.loadedFromTS && minSeq >= kc.loadedFromSeq {
			if snapHead > kc.lastSeq {
				since := max(kc.lastSeq, minSeq)
				added := false
				if snapHead > since {
					rows, err := rd.Query(`SELECT seq,ts,kind,seat,object_type,object_id,revision,payload FROM events
 WHERE kind = ? AND seq > ? AND seq <= ? ORDER BY seq`, kind, since, snapHead)
					if err != nil {
						return err
					}
					scanned := 0
					for rows.Next() {
						scanned++
						ev, err := scanTillrObservation(rows)
						if err != nil {
							rows.Close()
							return err
						}
						if ev.TS >= windowStart && ev.Seq > minSeq {
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
				}
				kc.lastSeq = snapHead
				if added {
					kc.gen++
				}
			}
			if windowStart > kc.loadedFromTS || minSeq > kc.loadedFromSeq {
				if len(kc.events) > 0 {
					kept := kc.events[:0]
					for _, ev := range kc.events {
						if ev.TS >= windowStart && ev.Seq > minSeq {
							kept = append(kept, ev)
						}
					}
					clear(kc.events[len(kept):])
					kc.events = kept
				}
				kc.loadedFromTS = windowStart
				kc.loadedFromSeq = minSeq
			}
			continue
		}
		var nextGen uint64 = 1
		if kc != nil {
			nextGen = kc.gen + 1
		}
		if snapHead == 0 {
			c.kinds[kind] = &kindCache{loadedFromTS: windowStart, loadedFromSeq: minSeq, lastSeq: 0, gen: nextGen}
			continue
		}
		rows, err := rd.Query(`SELECT seq,ts,kind,seat,object_type,object_id,revision,payload FROM events
 WHERE kind = ? AND seq > ? AND seq <= ? AND ts >= ? ORDER BY seq`, kind, minSeq, snapHead, windowStart)
		if err != nil {
			return err
		}
		var events []Observation
		for rows.Next() {
			ev, err := scanTillrObservation(rows)
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
			onQuery(kind, minSeq, len(events))
		}
		c.kinds[kind] = &kindCache{
			loadedFromTS:  windowStart,
			loadedFromSeq: minSeq,
			lastSeq:       snapHead,
			gen:           nextGen,
			events:        events,
		}
	}
	return nil
}

func (s *TillrStoreSource) activeCompositeSubjects(rd *store.Reader, ruleID string, n int) (map[string][]string, []string, error) {
	exists, err := tableExists(rd, "alert_episodes")
	if err != nil || !exists {
		return nil, nil, err
	}
	rows, err := rd.Query(`SELECT subject_key FROM alert_episodes WHERE rule_id=? AND state!='resolved'`, ruleID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	tuples := map[string][]string{}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, nil, err
		}
		if tuple, ok := decodeCompositeSubject(key, n); ok {
			tuples[key] = tuple
			keys = append(keys, key)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	slices.Sort(keys)
	return tuples, keys, nil
}

func (s *TillrStoreSource) deriveRule(rd *store.Reader, r Rule, now time.Time, facts subjectFacts, firstLoadedSeq int64) (map[string]*condition, int, error) {
	kinds := r.Reads.Events
	if len(kinds) == 0 {
		return nil, 0, nil
	}
	names := r.When.groupByNames()
	subjects := map[string]*condition{}
	subjectTuples := map[string][]string{}
	if len(names) >= 2 {
		seededTuples, _, err := s.activeCompositeSubjects(rd, r.ID, len(names))
		if err != nil {
			return nil, 0, err
		}
		for key, tuple := range seededTuples {
			subjects[key] = &condition{}
			subjectTuples[key] = tuple
		}
	}
	args := []any{firstLoadedSeq, windowStartMillis(r.When, now), now.UnixMilli()}
	for _, k := range kinds {
		args = append(args, k)
	}
	rows, err := rd.Query(`SELECT seq,ts,kind,seat,object_type,object_id,revision,payload FROM events
 WHERE seq > ? AND ts >= ? AND ts <= ? AND kind IN (?`+strings.Repeat(",?", len(kinds)-1)+`) ORDER BY seq`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	skipped := 0
	nowMillis := now.UnixMilli()
	for rows.Next() {
		obs, err := scanTillrObservation(rows)
		if err != nil {
			return nil, 0, err
		}
		if err := observeRecord(r, names, obs, facts, nowMillis, subjects, subjectTuples, &skipped); err != nil {
			return nil, 0, err
		}
	}
	return subjects, skipped, rows.Err()
}
