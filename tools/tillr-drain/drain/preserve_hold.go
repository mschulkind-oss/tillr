package drain

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// ReasonPreserveFailed holds an item whose settled run left work in its lane
// that the loop could not preserve. The lane is retained as it stands — not reset, and not
// released for the next staging to clear — and the item waits for the operator, who
// repairs the cause and clears the fault so the next pass settles the run again.
const ReasonPreserveFailed = "preserve-failed"

// stopLaneRetained is the settlement's stop for that case. It ends the one examination
// without recording it examined — which would release the lane — and the loop takes it as
// that lane's condition, not its own: the hold keeps the item from being re-examined, so
// nothing spins, and every other lane goes on.
const stopLaneRetained = "lane-retained"

// retainUnpreserved answers abandon when preservation failed. It does not hide that
// failure: the cause is reported and recorded as the item's fault detail. Only a store
// that cannot record the hold stops the loop, because then nothing would keep the lane's
// work from the next staging.
//
// On 2026-11-17 the loop stopped on exactly this ("lane lane-14 retained: could not
// preserve uncommitted work: ... database is locked"), so one lane's settlement took
// every other lane's work down with it.
//
// An item that is already terminal is not held, and its lane is not retained: there is
// nothing to keep a closed item's lane for beyond its head, which is pinned. What the lane
// held besides is recorded and discarded, and the lane is reset and returned to rotation
// (discardTerminalLane). On 2026-11-18 the loop stopped on exactly this ("lane lane-1
// retained, and its item could not be held: item fixer-main-5ca0b0ff82bc is already
// closed"), over a lane a failed fixer rebase had left with unmerged paths.
func (l *Loop) retainUnpreserved(ctx context.Context, lane, runID, headSHA string, cause error) string {
	l.reportf("loop: lane %s retained: could not preserve its work: %v\n", lane, cause)
	itemID, err := l.runItem(ctx, runID)
	if err == nil && itemID == "" {
		err = errors.New("no item to hold for the retained lane")
	}
	if err == nil {
		err = l.holdItemFault(ctx, Candidate{ItemID: itemID, RunID: runID, LaneID: lane}, ReasonPreserveFailed, cause.Error())
	}
	if terminal := asItemTerminal(err); terminal != nil {
		return l.discardTerminalLane(ctx, lane, runID, headSHA, terminal, cause)
	}
	if err != nil {
		l.reportf("loop: lane %s retained, and its item could not be held: %v\n", lane, err)
		return StopFault
	}
	return stopLaneRetained
}

// runItem answers the item a run belongs to, or "" for no run.
func (l *Loop) runItem(ctx context.Context, runID string) (string, error) {
	if runID == "" {
		return "", nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", err
	}
	defer s.Close()
	var itemID sql.NullString
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT item_id FROM runs WHERE run_id = ?`, runID).Scan(&itemID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return itemID.String, err
}

// laneDiscardedKind records what a terminal item's lane held when its work could not be
// preserved and was discarded.
const laneDiscardedKind = "drain.lane-discarded"

func init() {
	if err := event.Register(laneDiscardedKind, Utility); err != nil {
		panic(err)
	}
}

// laneDiscarded is drain.lane-discarded's payload. Status is the lane's `git status
// --porcelain` before the reset: the record of what was discarded.
type laneDiscarded struct {
	RunID     string   `json:"run_id"`
	ItemID    string   `json:"item_id"`
	ItemState string   `json:"item_state"`
	LaneID    string   `json:"lane_id"`
	HeadSHA   string   `json:"head_sha,omitempty"`
	Status    []string `json:"status"`
	Cause     string   `json:"cause"`
}

// discardTerminalLane settles the lane of a terminal item whose work could not be
// preserved: the head is pinned (it is already a ref once pinned), what else the lane held
// is recorded, and the lane is reset and returned to rotation. Only a store that cannot
// record the discard stops the loop, because then the discarded state would have no record.
func (l *Loop) discardTerminalLane(ctx context.Context, lane, runID, headSHA string, terminal *itemTerminalError, cause error) string {
	target := l.laneTarget(lane)
	status, _ := l.git(ctx, target, "status", "--porcelain", "--untracked-files=all")
	lines := []string{}
	for _, line := range strings.Split(status, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	// Mid-rebase, HEAD is the rebase's own position on the trunk, not the lane's work: the
	// abort restores the branch, and that head is the one pinned.
	if l.abortInProgress(ctx, target) || headSHA == "" {
		headSHA, _ = l.revParse(ctx, target, "HEAD")
	}
	if err := l.pinLane(ctx, lane, runID, headSHA); err != nil {
		if holdErr := l.holdItemFault(ctx, Candidate{ItemID: terminal.ItemID, LaneID: lane}, ReasonSettleFailed, err.Error()); holdErr != nil {
			l.reportf("loop: lane %s retained, and its pin failure could not be recorded: %v\n", lane, holdErr)
			return StopFault
		}
		return stopLaneRetained
	}
	body := laneDiscarded{RunID: runID, ItemID: terminal.ItemID, ItemState: terminal.State, LaneID: lane, HeadSHA: headSHA, Status: lines, Cause: cause.Error()}
	if err := appendJudgment(ctx, l.cfg.Store, laneDiscardedKind, "run", runID, body, l.cfg.Now().UTC()); err != nil {
		l.reportf("loop: lane %s's discard for the %s item %s could not be recorded: %v\n", lane, terminal.State, terminal.ItemID, err)
		return StopFault
	}
	l.reportf("loop: lane %s: item %s is already %s, so its unpreservable state is discarded (%d paths recorded) and the lane is reset\n",
		lane, terminal.ItemID, terminal.State, len(lines))
	if err := l.resetLane(ctx, lane); err != nil {
		// The next staging resets the lane again; one lane's reset is not the loop's fault.
		l.reportf("loop: lane %s could not be reset: %v\n", lane, err)
	}
	return ""
}

// abortInProgress aborts a rebase, merge, cherry-pick or revert left in progress in one
// checkout, and answers whether one was found. Each is looked for first, so a clean
// checkout costs one git call.
func (l *Loop) abortInProgress(ctx context.Context, dir string) bool {
	names := []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"}
	args := []string{"rev-parse"}
	for _, name := range names {
		args = append(args, "--git-path", name)
	}
	out, err := l.git(ctx, dir, args...)
	if err != nil {
		return false
	}
	abort := map[string][]string{
		"rebase-merge": {"rebase", "--abort"}, "rebase-apply": {"rebase", "--abort"},
		"MERGE_HEAD": {"merge", "--abort"}, "CHERRY_PICK_HEAD": {"cherry-pick", "--abort"}, "REVERT_HEAD": {"revert", "--abort"},
	}
	found := false
	paths := strings.Split(strings.TrimSpace(out), "\n")
	for i, path := range paths {
		if i >= len(names) {
			break
		}
		path = strings.TrimSpace(path)
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		found = true
		if _, err := l.git(ctx, dir, abort[names[i]]...); err != nil {
			l.reportf("loop: aborting the %s left in %s failed: %v\n", abort[names[i]][0], dir, err)
		} else {
			l.reportf("loop: aborted the %s left in progress in %s\n", abort[names[i]][0], dir)
		}
	}
	return found
}

// laneRetainedError is a settlement step's answer when abandon retained its lane
// (stopLaneRetained). The step's caller cannot return a stop, so the retention travels as
// this error, and the examination's completion turns it back into the lane's stop
// (retainedOutcome): one lane's retained work is that lane's condition, never the loop's.
type laneRetainedError struct{ lane string }

func (e *laneRetainedError) Error() string {
	return "lane " + e.lane + " retained after preservation failed"
}

// retainedAfter answers the error for a non-empty abandon stop: a retained lane is the
// lane's condition, and only StopFault — the store could not record the hold or the
// discard — is the loop's.
func retainedAfter(lane, stop string) error {
	if stop == stopLaneRetained {
		return &laneRetainedError{lane: lane}
	}
	return Externalf("lane %s retained after preservation failed", lane)
}

// retainedOutcome turns a settlement's laneRetainedError back into the lane's stop.
func retainedOutcome(o laneOutcome) laneOutcome {
	var retained *laneRetainedError
	if o.stop == "" && errors.As(o.err, &retained) {
		return laneOutcome{stop: stopLaneRetained}
	}
	return o
}
