package drain

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	// endedWorkPrefix names commits holding uncommitted work left in a lane when its
	// item ended for good (closed, resolved-elsewhere, landed, or another finished state).
	// Unlike refs/tillr/preserved/, sweepRefs never deletes these refs automatically.
	endedWorkPrefix = "refs/tillr/ended/"
	// endedWorkSavedKind records uncommitted work saved from an ended item's lane before
	// restoring the lane's worktree.
	endedWorkSavedKind = "drain.ended-work-saved"
)

func init() {
	if err := event.Register(endedWorkSavedKind, Utility); err != nil {
		panic(err)
	}
}

// EndedWorkSaved is drain.ended-work-saved's payload.
type EndedWorkSaved struct {
	RunID      string          `json:"run_id"`
	ItemID     string          `json:"item_id"`
	ItemState  string          `json:"item_state"`
	LaneID     string          `json:"lane_id"`
	Ref        string          `json:"ref"`
	IndexRef   string          `json:"index_ref,omitempty"`
	Repository string          `json:"repository"`
	Reason     string          `json:"reason"`
	Files      []PreservedFile `json:"files"`
	IndexFiles []PreservedFile `json:"index_files,omitempty"`
	Unmerged   []string        `json:"unmerged,omitempty"`
	Summary    string          `json:"summary"`
}

// terminalItemState answers the item's state and whether it has ended for good (any
// finished state in the store's definition, or removed from items).
func (l *Loop) terminalItemState(ctx context.Context, itemID string) (string, bool, error) {
	if l.cfg.Store == "" || itemID == "" {
		return "", false, nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", false, err
	}
	defer s.Close()
	var state string
	var ended bool
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		err = r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, itemID).Scan(&state)
		if err == sql.ErrNoRows {
			state, ended = "removed", true
			return nil
		}
		if err != nil {
			return err
		}
		ended = finishedState(declared.Parsed, state)
		return nil
	})
	return state, ended, err
}

// runItemTerminalState answers the item, its state, and whether it has ended for good.
func (l *Loop) runItemTerminalState(ctx context.Context, runID string) (itemID, state string, ended bool, err error) {
	if l.cfg.Store == "" || runID == "" {
		return "", "", false, nil
	}
	itemID, err = l.preservationItem(ctx, runID)
	if err != nil {
		return "", "", false, nil
	}
	state, ended, err = l.terminalItemState(ctx, itemID)
	return itemID, state, ended, err
}

// runItemTerminal answers whether the item owning runID is already in a finished state.
func (l *Loop) runItemTerminal(ctx context.Context, runID string) (bool, error) {
	_, _, ended, err := l.runItemTerminalState(ctx, runID)
	return ended, err
}

// saveEndedItemWork snapshots uncommitted files left in lane by an ended item's run
// under refs/tillr/ended/<runID> and records drain.ended-work-saved. It never discards
// uncommitted work: if saving fails, it returns the error before any caller resets the lane.
func (l *Loop) saveEndedItemWork(ctx context.Context, lane, runID, itemID, itemState, reason string) (bool, error) {
	if l.nonLanding || runID == "" {
		return false, nil
	}
	if err := checkRunID(runID); err != nil {
		return false, err
	}
	target := l.laneTarget(lane)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pinTimeout)
	defer cancel()
	if err := l.clearStaleIndexLock(ctx, lane, runID); err != nil {
		return false, err
	}
	status, err := l.git(ctx, target, "--no-optional-locks", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(status) == "" {
		return false, nil
	}
	snapshot, err := l.snapshotWork(ctx, lane, runID)
	if err != nil {
		return false, err
	}
	if len(snapshot.files) == 0 && len(snapshot.indexFiles) == 0 {
		return false, nil
	}
	ref := endedWorkPrefix + runID
	refs, err := l.git(ctx, target, "for-each-ref", "--format=%(refname)", ref)
	if err != nil {
		return false, err
	}
	exists := false
	for _, name := range strings.Split(refs, "\n") {
		if name == ref {
			exists = true
		}
	}
	var p EndedWorkSaved
	if exists {
		body, err := l.git(ctx, target, "show", "-s", "--format=%B", ref)
		if err != nil {
			return false, err
		}
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			return false, fmt.Errorf("reading saved ended work %s: %w", ref, err)
		}
		if p.RunID != runID || p.Ref != ref {
			return false, Externalf("saved ended work ref %s names a different run", ref)
		}
		if err := l.snapshotCovered(ctx, target, ref, snapshot.tree, snapshot.files); err != nil {
			return false, err
		}
		indexRef := p.IndexRef
		if indexRef == "" {
			indexRef = ref
		}
		if err := l.snapshotCovered(ctx, target, indexRef, snapshot.indexTree, snapshot.indexFiles); err != nil {
			return false, err
		}
	} else {
		if itemState == "" {
			itemState = "closed"
		}
		p = EndedWorkSaved{
			RunID:      runID,
			ItemID:     itemID,
			ItemState:  itemState,
			LaneID:     lane,
			Ref:        ref,
			Repository: target,
			Reason:     reason,
			Files:      snapshot.files,
			Summary:    snapshot.summary,
			Unmerged:   snapshot.unmerged,
		}
		if same, ok := l.sharesRepository(ctx, target); ok && same {
			p.Repository = l.cfg.Root
		}
		args := []string{"commit-tree", snapshot.tree, "-p", snapshot.head}
		if snapshot.indexTree != snapshot.head && snapshot.indexTree != snapshot.tree {
			staged, err := l.git(ctx, target, "commit-tree", snapshot.indexTree, "-p", snapshot.head, "-m", "Staged contents of ended item run "+runID)
			if err != nil {
				return false, err
			}
			args = append(args, "-p", strings.TrimSpace(staged))
			p.IndexRef = ref + "^2"
			p.IndexFiles = snapshot.indexFiles
			p.Summary += "; staged index: " + snapshot.indexSummary
		}
		if len(snapshot.unmerged) > 0 {
			p.Summary += "; unmerged in the lane's index: " + strings.Join(snapshot.unmerged, ", ") + " (kept as the working copy has them)"
		}
		if snapshot.indexNote != "" {
			p.Summary += "; " + snapshot.indexNote
		}
		if err := l.writeEndedWorkRef(ctx, target, p, args...); err != nil {
			return false, err
		}
	}
	recorded, err := l.endedWorkRecorded(ctx, runID, ref)
	if err != nil {
		return false, err
	}
	if !recorded {
		if err := appendJudgment(ctx, l.cfg.Store, endedWorkSavedKind, "run", runID, p, l.cfg.Now().UTC()); err != nil {
			return false, err
		}
		l.reportf("loop: saved uncommitted work from ended item %s (%s) on %s at %s: %s\n", p.ItemID, p.ItemState, lane, p.Ref, p.Summary)
	}
	return true, nil
}

func (l *Loop) writeEndedWorkRef(ctx context.Context, target string, p EndedWorkSaved, args ...string) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", target}, args...)...)
	cmd.Stdin = bytes.NewReader(body)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	sha, err := cmd.Output()
	if err != nil {
		return Externalf("writing %s (%d files, %d staged files): git commit-tree in %s: %v: %s", p.Ref, len(p.Files), len(p.IndexFiles), target, err, strings.TrimSpace(stderr.String()))
	}
	_, err = l.git(ctx, target, "update-ref", p.Ref, strings.TrimSpace(string(sha)), "")
	return err
}

func (l *Loop) endedWorkRecorded(ctx context.Context, runID, ref string) (bool, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer s.Close()
	var exists bool
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? AND json_extract(payload, '$.ref') = ?)`, endedWorkSavedKind, runID, ref).Scan(&exists)
	})
	return exists, err
}

var endedLaneQueryHook func(query string, args []any)

func queryEndedLaneRows(r *store.Reader, query string, args ...any) (*sql.Rows, error) {
	if endedLaneQueryHook != nil {
		endedLaneQueryHook(query, args)
	}
	return r.Query(query, args...)
}

func queryEndedLaneRow(r *store.Reader, query string, args ...any) *sql.Row {
	if endedLaneQueryHook != nil {
		endedLaneQueryHook(query, args)
	}
	return r.QueryRow(query, args...)
}

type endedLaneOwner struct {
	runID      string
	itemID     string
	seq        int64
	fromRuns   bool
	finishedAt int64
	startedAt  int64
}

func (c endedLaneOwner) newerOrEqual(prev endedLaneOwner) bool {
	if c.seq != prev.seq {
		return c.seq > prev.seq
	}
	if c.fromRuns != prev.fromRuns {
		return c.fromRuns
	}
	if !c.fromRuns {
		return true
	}
	if c.finishedAt != prev.finishedAt {
		return c.finishedAt > prev.finishedAt
	}
	if c.startedAt != prev.startedAt {
		return c.startedAt > prev.startedAt
	}
	return c.runID >= prev.runID
}

type reclaimedCleanLane struct {
	runID string
	epoch int
}

type endedLaneHistory struct {
	storePath   string
	lastSeq     int64
	eventOwners map[string]endedLaneOwner
	cleanLanes  map[string]reclaimedCleanLane
}

type endedLaneEventPayload struct {
	ParentRunID string `json:"parent_run_id"`
	Decision    struct {
		ItemID string `json:"item_id"`
		LaneID string `json:"lane_id"`
	} `json:"decision"`
}

type endedLaneCandidate struct {
	lane      string
	runID     string
	itemID    string
	itemState string
}

func (l *Loop) recordCleanEndedLane(lane, runID string, epochBefore int, restored bool) {
	l.mu.Lock()
	staging := l.stagingLanes[lane] > 0
	epochNow := l.laneChanges[lane]
	l.mu.Unlock()
	if staging || (!restored && epochNow != epochBefore) {
		return
	}
	if l.endedLanes.cleanLanes == nil {
		l.endedLanes.cleanLanes = map[string]reclaimedCleanLane{}
	}
	l.endedLanes.cleanLanes[lane] = reclaimedCleanLane{runID: runID, epoch: epochNow}
}

// reclaimEndedItemLanes saves uncommitted work left in idle lanes whose most recent run
// belonged to an item that has ended for good (closed, resolved-elsewhere, landed, or
// removed), records drain.ended-work-saved, and restores each lane's worktree so the next
// item can stage onto it without hitting lane-holds-uncommitted-work.
func (l *Loop) reclaimEndedItemLanes(ctx context.Context) error {
	if l.nonLanding || l.cfg.Store == "" || l.cfg.LaneRoot == "" {
		return nil
	}
	entries, err := os.ReadDir(l.cfg.LaneRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var onDisk []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		lane := entry.Name()
		if l.hasLaneCondition(lane, ReasonLaneChildren) {
			continue
		}
		if _, err := os.Stat(l.laneTarget(lane)); err != nil {
			continue
		}
		onDisk = append(onDisk, lane)
	}
	if len(onDisk) == 0 {
		return nil
	}
	sort.Strings(onDisk)

	l.endedLaneMu.Lock()
	defer l.endedLaneMu.Unlock()

	epochsBefore := make(map[string]int, len(onDisk))
	l.mu.Lock()
	filtered := onDisk[:0]
	for _, lane := range onDisk {
		if l.stagingLanes[lane] > 0 {
			continue
		}
		epochsBefore[lane] = l.laneChanges[lane]
		filtered = append(filtered, lane)
	}
	onDisk = filtered
	l.mu.Unlock()
	if len(onDisk) == 0 {
		return nil
	}

	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var candidates []endedLaneCandidate
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		busy := map[string]bool{}
		rows, err := queryEndedLaneRows(r, `SELECT lane_id FROM lanes WHERE state = 'leased'`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var lane string
			if err := rows.Scan(&lane); err != nil {
				rows.Close()
				return err
			}
			busy[lane] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		idleLanes := make([]string, 0, len(onDisk))
		for _, lane := range onDisk {
			if !busy[lane] {
				idleLanes = append(idleLanes, lane)
			}
		}
		if len(idleLanes) == 0 {
			return nil
		}

		head, err := r.EventHeadSeq()
		if err != nil {
			return err
		}
		since := l.endedLanes.lastSeq
		var nextEventOwners map[string]endedLaneOwner
		if l.endedLanes.storePath != l.cfg.Store || head < since || l.endedLanes.eventOwners == nil {
			since = 0
			nextEventOwners = map[string]endedLaneOwner{}
			l.endedLanes.cleanLanes = nil
		} else {
			nextEventOwners = maps.Clone(l.endedLanes.eventOwners)
		}
		if head > since {
			for _, kind := range []string{dispatchStartedKind, nodeAdvancedKind} {
				rows, err = queryEndedLaneRows(r, `SELECT seq, object_id, payload FROM events WHERE kind = ? AND seq > ? AND object_type = 'run'`, kind, since)
				if err != nil {
					return err
				}
				for rows.Next() {
					var seq int64
					var runID string
					var raw []byte
					if err := rows.Scan(&seq, &runID, &raw); err != nil {
						rows.Close()
						return err
					}
					var p endedLaneEventPayload
					if err := json.Unmarshal(raw, &p); err != nil {
						continue
					}
					lane, itemID := p.Decision.LaneID, p.Decision.ItemID
					if lane == "" || itemID == "" {
						continue
					}
					if kind == nodeAdvancedKind && p.ParentRunID != "" {
						runID = p.ParentRunID
					}
					cand := endedLaneOwner{runID: runID, itemID: itemID, seq: seq}
					if prev, ok := nextEventOwners[lane]; !ok || cand.newerOrEqual(prev) {
						nextEventOwners[lane] = cand
					}
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					return err
				}
				rows.Close()
			}
		}

		owners := make(map[string]endedLaneOwner, len(idleLanes))
		args := make([]any, len(idleLanes))
		conds := make([]string, len(idleLanes))
		for i, lane := range idleLanes {
			args[i] = lane
			conds[i] = "lane_id = ?"
			if owner, ok := nextEventOwners[lane]; ok {
				owners[lane] = owner
			}
		}
		rows, err = queryEndedLaneRows(r, `SELECT run_id, COALESCE(item_id, ''), lane_id, event_seq, finished_at, started_at FROM runs WHERE `+strings.Join(conds, " OR "), args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var runID, itemID, lane string
			var seq, finishedAt, startedAt int64
			if err := rows.Scan(&runID, &itemID, &lane, &seq, &finishedAt, &startedAt); err != nil {
				rows.Close()
				return err
			}
			if lane == "" || itemID == "" || runID == "" {
				continue
			}
			cand := endedLaneOwner{
				runID:      runID,
				itemID:     itemID,
				seq:        seq,
				fromRuns:   true,
				finishedAt: finishedAt,
				startedAt:  startedAt,
			}
			if prev, ok := owners[lane]; !ok || cand.newerOrEqual(prev) {
				owners[lane] = cand
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		l.endedLanes.storePath = l.cfg.Store
		l.endedLanes.lastSeq = head
		l.endedLanes.eventOwners = nextEventOwners

		unchecked := make([]string, 0, len(idleLanes))
		for _, lane := range idleLanes {
			owner, ok := owners[lane]
			if !ok {
				continue
			}
			if clean, ok := l.endedLanes.cleanLanes[lane]; ok && clean.runID == owner.runID && clean.epoch == epochsBefore[lane] {
				continue
			}
			unchecked = append(unchecked, lane)
		}
		if len(unchecked) == 0 {
			return nil
		}

		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		states := make(map[string]string, len(unchecked))
		existsByItem := make(map[string]bool, len(unchecked))
		var endedIdle []endedLaneCandidate
		for _, lane := range unchecked {
			owner := owners[lane]
			state, seen := states[owner.itemID]
			exists := existsByItem[owner.itemID]
			if !seen {
				err := queryEndedLaneRow(r, `SELECT state FROM items WHERE item_id = ?`, owner.itemID).Scan(&state)
				if err == sql.ErrNoRows {
					exists = false
				} else if err != nil {
					return err
				} else {
					exists = true
				}
				states[owner.itemID], existsByItem[owner.itemID] = state, exists
			}
			if exists && !finishedState(declared.Parsed, state) {
				continue
			}
			if !exists {
				state = "removed"
			}
			endedIdle = append(endedIdle, endedLaneCandidate{
				lane:      lane,
				runID:     owner.runID,
				itemID:    owner.itemID,
				itemState: state,
			})
		}
		if len(endedIdle) == 0 {
			return nil
		}

		works, err := readPending(r)
		if err != nil {
			return err
		}
		for _, w := range works {
			if !w.Done && !w.LaneReleased && w.Decision.LaneID != "" {
				busy[w.Decision.LaneID] = true
			}
		}
		for _, c := range endedIdle {
			if !busy[c.lane] {
				candidates = append(candidates, c)
			}
		}
		return nil
	})
	s.Close()
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if l.hasLaneCondition(c.lane, ReasonLaneChildren) {
			continue
		}
		target := l.laneTarget(c.lane)
		if _, err := os.Stat(target); err != nil {
			continue
		}
		saved, err := l.saveEndedItemWork(ctx, c.lane, c.runID, c.itemID, c.itemState, "item ended ("+c.itemState+")")
		if err != nil {
			l.reportf("loop: lane %s holds uncommitted work from ended item %s (%s) that could not be saved: %v\n", c.lane, c.itemID, c.itemState, err)
			continue
		}
		if saved {
			if head, err := l.revParse(ctx, target, "HEAD"); err == nil && head != "" {
				if err := l.pinLane(ctx, c.lane, c.runID, head); err != nil {
					l.reportf("loop: lane %s head could not be pinned before restore: %v\n", c.lane, err)
					continue
				}
			}
			if !l.restoreReclaimedLane(ctx, c) {
				continue
			}
			if l.hasLaneCondition(c.lane, ReasonLaneUncommittedWork) {
				_ = l.restoreLane(ctx, c.lane)
			}
		}
		l.recordCleanEndedLane(c.lane, c.runID, epochsBefore[c.lane], saved)
	}
	return nil
}

func (l *Loop) restoreReclaimedLane(ctx context.Context, c endedLaneCandidate) bool {
	// Like staging, restoration is a loop-owned checkout write. Invalidate sibling
	// snapshots even if it only partly completes. Clean lanes never enter this bracket.
	end := l.beginLaneStaging(c.lane)
	defer end()
	if err := l.restoreForkDelivery(ctx, c.lane, c.runID); err != nil {
		l.reportf("loop: lane %s fork checkout could not be restored after saving ended item %s: %v\n", c.lane, c.itemID, err)
		return false
	}
	if err := l.resetLane(ctx, c.lane); err != nil {
		l.reportf("loop: lane %s could not be restored after saving ended item %s: %v\n", c.lane, c.itemID, err)
		return false
	}
	return true
}
