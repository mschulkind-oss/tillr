package drain

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

type endedLaneCandidate struct {
	lane      string
	runID     string
	itemID    string
	itemState string
}

// reclaimEndedItemLanes saves uncommitted work left in idle lanes whose most recent run
// belonged to an item that has ended for good (closed, resolved-elsewhere, landed, or
// removed), records drain.ended-work-saved, and restores each lane's worktree so the next
// item can stage onto it without hitting lane-holds-uncommitted-work.
func (l *Loop) reclaimEndedItemLanes(ctx context.Context) error {
	if l.nonLanding || l.cfg.Store == "" || l.cfg.LaneRoot == "" {
		return nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var candidates []endedLaneCandidate
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		states := map[string]string{}
		rows, err := r.Query(`SELECT item_id, state FROM items`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, state string
			if err := rows.Scan(&id, &state); err != nil {
				rows.Close()
				return err
			}
			states[id] = state
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		busy := map[string]bool{}
		rows, err = r.Query(`SELECT lane_id FROM lanes WHERE state = 'leased'`)
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

		works, err := readPending(r)
		if err != nil {
			return err
		}
		for _, w := range works {
			if !w.Done && !w.LaneReleased && w.Decision.LaneID != "" {
				busy[w.Decision.LaneID] = true
			}
		}

		type laneOwner struct {
			runID  string
			itemID string
			seq    int64
		}
		owners := map[string]laneOwner{}
		rows, err = r.Query(`SELECT seq, kind, object_id, payload FROM events WHERE kind IN (?, ?) AND object_type = 'run' ORDER BY seq`, dispatchStartedKind, nodeAdvancedKind)
		if err != nil {
			return err
		}
		for rows.Next() {
			var seq int64
			var kind, runID string
			var raw []byte
			if err := rows.Scan(&seq, &kind, &runID, &raw); err != nil {
				rows.Close()
				return err
			}
			var w pendingWork
			if err := json.Unmarshal(raw, &w); err != nil {
				continue
			}
			lane := w.Decision.LaneID
			if lane == "" || w.Decision.ItemID == "" {
				continue
			}
			if kind == nodeAdvancedKind && w.ParentRunID != "" {
				runID = w.ParentRunID
			}
			owners[lane] = laneOwner{runID: runID, itemID: w.Decision.ItemID, seq: seq}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		rows, err = r.Query(`SELECT run_id, COALESCE(item_id, ''), lane_id, event_seq FROM runs ORDER BY event_seq, finished_at, started_at, run_id`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var runID, itemID, lane string
			var seq int64
			if err := rows.Scan(&runID, &itemID, &lane, &seq); err != nil {
				rows.Close()
				return err
			}
			if lane == "" || itemID == "" || runID == "" {
				continue
			}
			prev, ok := owners[lane]
			if !ok || seq >= prev.seq {
				owners[lane] = laneOwner{runID: runID, itemID: itemID, seq: seq}
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		lanes := make([]string, 0, len(owners))
		for lane := range owners {
			lanes = append(lanes, lane)
		}
		sort.Strings(lanes)
		for _, lane := range lanes {
			if busy[lane] {
				continue
			}
			owner := owners[lane]
			state, exists := states[owner.itemID]
			if exists && !finishedState(declared.Parsed, state) {
				continue
			}
			if !exists {
				state = "removed"
			}
			candidates = append(candidates, endedLaneCandidate{
				lane:      lane,
				runID:     owner.runID,
				itemID:    owner.itemID,
				itemState: state,
			})
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
		if !saved {
			continue
		}
		if head, err := l.revParse(ctx, target, "HEAD"); err == nil && head != "" {
			if err := l.pinLane(ctx, c.lane, c.runID, head); err != nil {
				l.reportf("loop: lane %s head could not be pinned before restore: %v\n", c.lane, err)
				continue
			}
		}
		if err := l.restoreForkDelivery(ctx, c.lane, c.runID); err != nil {
			l.reportf("loop: lane %s fork checkout could not be restored after saving ended item %s: %v\n", c.lane, c.itemID, err)
			continue
		}
		if err := l.resetLane(ctx, c.lane); err != nil {
			l.reportf("loop: lane %s could not be restored after saving ended item %s: %v\n", c.lane, c.itemID, err)
			continue
		}
		if l.hasLaneCondition(c.lane, ReasonLaneUncommittedWork) {
			_ = l.restoreLane(ctx, c.lane)
		}
	}
	return nil
}
