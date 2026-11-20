package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// staleBaseKind records that a run was staged on a base lacking a commit that made
// its item ready, so its report is about the staging and not about the item. The
// planner reads it as an interruption: the next dispatch restages the item instead
// of holding it for a retry, a design ruling or the owner.
const staleBaseKind = "drain.stale-base"

// ReasonRestagedStaleBase is the dispatch reason of the attempt that follows one
// staged on a stale base.
const ReasonRestagedStaleBase = "restaged: the earlier run's base lacked what made the item ready"

func init() {
	if err := event.Register(staleBaseKind, Utility); err != nil {
		panic(err)
	}
}

// staleBase is the payload of a drain.stale-base event.
type staleBase struct {
	RunID      string `json:"run_id"`
	ItemID     string `json:"item_id"`
	BaseSHA    string `json:"base_sha"`
	TrunkSHA   string `json:"trunk_sha"`
	Lacks      string `json:"lacks"`
	ConflictID string `json:"conflict_id,omitempty"`
}

// dispatchRecord is what drain.started recorded for a run's first dispatch.
type dispatchRecord struct {
	seq     int64
	base    string
	itemID  string
	inputs  *RunInputs
	present bool
}

func readDispatch(r *store.Reader, runID string) (dispatchRecord, error) {
	var (
		d   dispatchRecord
		raw []byte
	)
	err := r.QueryRow(`SELECT seq, payload FROM events WHERE kind=? AND object_type='run' AND object_id=? ORDER BY seq LIMIT 1`, dispatchStartedKind, runID).Scan(&d.seq, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	var work pendingWork
	if err := json.Unmarshal(raw, &work); err != nil {
		return d, err
	}
	d.base, d.itemID, d.inputs, d.present = work.BaseSHA, work.Decision.ItemID, work.Decision.Inputs, true
	return d, nil
}

// itemAsOf is the item's record as the last item event before seq wrote it: the
// fields the run's dispatch hashed, whatever sync has written since.
func itemAsOf(r *store.Reader, itemID string, seq int64) (blockFields, []string, bool, error) {
	var raw []byte
	err := r.QueryRow(`SELECT payload FROM events WHERE kind IN (?, ?, ?) AND object_id=? AND seq<? ORDER BY seq DESC LIMIT 1`, item.RecordKind, item.AddKind, item.UpdateKind, itemID, seq).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return blockFields{}, nil, false, nil
	}
	if err != nil {
		return blockFields{}, nil, false, err
	}
	var payload struct {
		Title     string   `json:"title"`
		BodyRef   string   `json:"body_ref"`
		DesignDoc string   `json:"design_doc"`
		Seat      string   `json:"seat"`
		Workflow  string   `json:"workflow"`
		BlockedBy []string `json:"blocked_by"`
		Criteria  []struct {
			ID   string `json:"criterion_id"`
			Text string `json:"text"`
		} `json:"criteria"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return blockFields{}, nil, false, err
	}
	f := blockFields{Title: payload.Title, BodyRef: payload.BodyRef, DesignDoc: payload.DesignDoc, Seat: payload.Seat, Workflow: payload.Workflow, Criteria: [][2]string{}}
	for _, c := range payload.Criteria {
		f.Criteria = append(f.Criteria, [2]string{c.ID, c.Text})
	}
	sort.Slice(f.Criteria, func(i, j int) bool { return f.Criteria[i][0] < f.Criteria[j][0] })
	return f, payload.BlockedBy, true, nil
}

// runBaseLacks answers what the run's recorded base is missing of the commits that
// made its item ready, read against trunk: the version of the item's own block the
// dispatch hashed, and the landing of each item it was blocked by. It names a missing
// commit only on evidence — the block version found on a trunk commit after the base,
// a landing recorded before the dispatch — so an empty answer is also the answer for
// a run whose inputs predate the record.
func runBaseLacks(ctx context.Context, r *store.Reader, root, runID, trunk string) (staleBase, error) {
	out := staleBase{RunID: runID, TrunkSHA: trunk}
	d, err := readDispatch(r, runID)
	if err != nil || !d.present || d.base == "" || IsTrunkFixer(d.itemID) {
		return out, err
	}
	out.ItemID, out.BaseSHA = d.itemID, d.base
	if d.base == trunk {
		return out, nil
	}
	fields, blockers, found, err := itemAsOf(r, d.itemID, d.seq)
	if err != nil || !found {
		return out, err
	}
	lacks := []string{}
	cache := blockSource{}
	if d.inputs != nil && d.inputs.ItemBlock != "" && fields.BodyRef != "" {
		carrier, err := blockCarrier(ctx, root, d.base, trunk, d.itemID, fields, d.inputs.ItemBlock, cache)
		if err != nil {
			return out, err
		}
		if carrier != "" {
			lacks = append(lacks, fmt.Sprintf("its block %s as dispatched (on trunk from %s)", fields.BodyRef, shortSHA(carrier)))
		}
	}
	for _, blocker := range blockers {
		var landed string
		err := r.QueryRow(`SELECT trunk_after FROM landings WHERE item_id=? AND result='landed' AND event_seq<? ORDER BY event_seq DESC LIMIT 1`, blocker, d.seq).Scan(&landed)
		if errors.Is(err, sql.ErrNoRows) || (err != nil && strings.Contains(err.Error(), "no such table")) {
			continue
		}
		if err != nil {
			return out, err
		}
		var fence string
		if err := r.QueryRow(`SELECT COALESCE(allowed_paths, '') FROM items WHERE item_id=?`, blocker).Scan(&fence); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if allowed, err := decodeFence(fence); err != nil {
			return out, err
		} else if forkOf(allowed) != "" {
			// A fork landing is delivered to its patch branch, which a trunk base never holds.
			continue
		}
		onTrunk, err := containsCommit(ctx, root, landed, trunk)
		if err != nil || !onTrunk {
			if err != nil {
				return out, err
			}
			continue
		}
		inBase, err := containsCommit(ctx, root, landed, d.base)
		if err != nil {
			return out, err
		}
		if !inBase {
			lacks = append(lacks, fmt.Sprintf("the landing of %s (%s)", blocker, shortSHA(landed)))
		}
	}
	out.Lacks = strings.Join(lacks, " and ")
	return out, nil
}

// blockCarrier returns the oldest trunk commit after base whose block hashes to want,
// or "" when base already carries it or no trunk commit does: a version nobody
// committed is not one a different base could supply.
func blockCarrier(ctx context.Context, root, base, trunk, itemID string, f blockFields, want string, cache blockSource) (string, error) {
	matches := func(sha string) (bool, error) {
		body, ok, err := blockBodyAt(ctx, root, sha, f.BodyRef, itemID, cache)
		if err != nil || !ok {
			return false, err
		}
		got, err := itemBlockHash(f, body)
		return got == want, err
	}
	if ok, err := matches(base); err != nil || ok {
		return "", err
	}
	file, _, _ := strings.Cut(f.BodyRef, "#")
	list, err := heavyGitOutput(ctx, root, "rev-list", "--reverse", base+".."+trunk, "--", file)
	if err != nil {
		return "", err
	}
	for _, sha := range strings.Fields(list) {
		if ok, err := matches(sha); err != nil || ok {
			if err != nil {
				return "", err
			}
			return sha, nil
		}
	}
	return "", nil
}

// recordStaleBase appends the finding; it is information for the record, and the
// planner's restage is what acts on it.
func recordStaleBase(tx *store.Tx, s staleBase, at time.Time) error {
	return appendEffect(tx, staleBaseKind, s.RunID, s, at.UTC())
}

// releaseStaleBaseConflicts answers an owner conflict whose raising run was staged on a
// base lacking what made its item ready. Such a report is about the staging, which no
// owner act can change, so the loop answers it itself: the conflict moves to answered,
// the item returns to the queue, and the next dispatch restages it on a base the
// dispatch rules accept. Each conflict is read once per loop process; its run's base
// and dispatch record do not change.
func (l *Loop) releaseStaleBaseConflicts(ctx context.Context) error {
	if l.staleBaseChecked == nil {
		l.staleBaseChecked = map[string]bool{}
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	trunk, err := heavyGitOutput(ctx, l.cfg.Root, "rev-parse", TrunkRef)
	if err != nil {
		return err
	}
	type finding struct {
		conflict item.Conflict
		stale    staleBase
	}
	found := []finding{}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		conflicts, err := item.ReadConflicts(r)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(conflicts))
		for id := range conflicts {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			c := conflicts[id]
			if c.Status != "owner" || c.RunID == "" || c.BodyRef == "" || c.TriageVersion != "" || IsTrunkFixer(c.ItemID) || l.staleBaseChecked[c.ConflictID] {
				continue
			}
			stale, err := runBaseLacks(ctx, r, l.cfg.Root, c.RunID, trunk)
			if err != nil {
				return err
			}
			if stale.Lacks == "" {
				l.staleBaseChecked[c.ConflictID] = true
				continue
			}
			found = append(found, finding{c, stale})
		}
		return nil
	})
	if err != nil || len(found) == 0 {
		return err
	}
	at := l.cfg.Now().UTC()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		current, err := item.ReadConflicts(tx.Reader())
		if err != nil {
			return err
		}
		for _, f := range found {
			// An owner's newer disposition wins over this earlier read.
			if c := current[f.conflict.ItemID]; c.ConflictID != f.conflict.ConflictID || c.Status != "owner" {
				continue
			}
			if err := refuseTerminalItem(tx.Reader(), f.conflict.ItemID, "stale-base"); err != nil {
				continue
			}
			c := f.conflict
			c.Status, c.WaitingOn = "answered", "build"
			c.Text += "\n\nStale base: run " + f.stale.RunID + " was staged on " + shortSHA(f.stale.BaseSHA) + ", which lacks " + f.stale.Lacks + ". The loop restages the item; no owner act was needed."
			if err := item.RecordConflict(tx, c, at); err != nil {
				return err
			}
			if isObstacleCallRun(tx.Reader(), c.RunID) {
				if err := l.answerLandingRefusal(tx, Candidate{RunID: c.RunID}, refusalPayload{Step: IdenticalRepeatPrefix, Detail: c.Text}, refusalEvidence{}, at); err != nil {
					return err
				}
			}
			f.stale.ConflictID = c.ConflictID
			if err := recordStaleBase(tx, f.stale, at); err != nil {
				return err
			}
			l.reportf("loop: %s conflict %s answered as a stale base: run %s on %s lacks %s\n", c.ItemID, c.ConflictID, f.stale.RunID, shortSHA(f.stale.BaseSHA), f.stale.Lacks)
			l.staleBaseChecked[c.ConflictID] = true
		}
		return nil
	})
}

// staleBlockedReport decides a run's `blocked` report before it becomes a conflict.
// A build run whose base lacked what made the item ready is recorded as a stale base
// and returns true: nothing is raised, and the next dispatch restages the item. A
// design run answering a conflict raised by such a build run answers that conflict
// the same way.
func (l *Loop) staleBlockedReport(ctx context.Context, d Decision) (bool, error) {
	if d.RunRequest == nil || IsTrunkFixer(d.ItemID) || (d.BlockConflict != nil && d.BlockConflict.TriageVersion != "") {
		return false, nil
	}
	trunk, err := heavyGitOutput(ctx, l.cfg.Root, "rev-parse", TrunkRef)
	if err != nil {
		return false, err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, err
	}
	defer s.Close()
	stale := false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		runID := d.RunRequest.RunID
		if d.BlockConflict != nil {
			runID = d.BlockConflict.RunID
		}
		finding, err := runBaseLacks(ctx, tx.Reader(), l.cfg.Root, runID, trunk)
		if err != nil || finding.Lacks == "" {
			return err
		}
		if err := refuseTerminalItem(tx.Reader(), d.ItemID, "stale-base"); err != nil {
			return nil
		}
		stale = true
		at := l.cfg.Now().UTC()
		if d.BlockConflict != nil {
			// The design run's own report is not an attempt at the item; the build run
			// that raised the conflict is the one restaged.
			c := *d.BlockConflict
			c.Status, c.WaitingOn = "answered", "build"
			c.Text += "\n\nStale base: run " + finding.RunID + " was staged on " + shortSHA(finding.BaseSHA) + ", which lacks " + finding.Lacks + ". The loop restages the item; design run " + d.RunRequest.RunID + " reported on that run's report."
			if err := item.RecordConflict(tx, c, at); err != nil {
				return err
			}
			if isObstacleCallRun(tx.Reader(), c.RunID) {
				if err := l.answerLandingRefusal(tx, Candidate{RunID: c.RunID}, refusalPayload{Step: IdenticalRepeatPrefix, Detail: c.Text}, refusalEvidence{}, at); err != nil {
					return err
				}
			}
			finding.ConflictID = c.ConflictID
		}
		if err := recordStaleBase(tx, finding, at); err != nil {
			return err
		}
		l.reportf("loop: %s reported blocked on a stale base: run %s on %s lacks %s; restaging\n", d.ItemID, finding.RunID, shortSHA(finding.BaseSHA), finding.Lacks)
		return nil
	})
	return stale, err
}
