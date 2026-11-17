package drain

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// ReasonTrunkPathDirty means owner content overlaps a retained landing. Committing
// or restoring those paths reopens the landing without dispatching another run.
const ReasonTrunkPathDirty = "trunk-path-dirty"
const dirtyHeldKind = "drain.trunk-path-dirty"

var errDirtyHeld = errors.New("landing waits on owner content")

type dirtyHold struct {
	Root      string    `json:"root"`
	Candidate Candidate `json:"candidate"`
}

func init() {
	if err := event.Register(dirtyHeldKind, Utility); err != nil {
		panic(err)
	}
}

func dirtyReason(paths []string) string {
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	return ReasonTrunkPathDirty + ": " + strings.Join(paths, ", ")
}

// Recheck the candidate's change reapplied onto today's trunk, never the stale
// original path set. A conflict must reach Land's conflict handling; it is not
// evidence that owner dirt is still blocking a clean landing.
func (h *dirtyHold) paths(ctx context.Context) ([]string, error) {
	ref := h.Candidate.BaseRef
	if ref == "" {
		ref = TrunkRef
	}
	trunk, err := boundaryGit(ctx, h.Root, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return nil, err
	}
	trunk = strings.TrimSpace(trunk)
	cmd := exec.CommandContext(ctx, "git", "-C", h.Root, "merge-tree", "--write-tree", "--merge-base="+h.Candidate.BaseSHA, trunk, h.Candidate.HeadSHA)
	cmd.Env = boundaryGitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && bytes.Contains(out, []byte("CONFLICT")) {
			return nil, nil // Retry Land so it records and routes the conflict.
		}
		return nil, Externalf("reapplying retained candidate %s onto %s: %v: %s", h.Candidate.RunID, trunk, err, strings.TrimSpace(stderr.String()))
	}
	tree, _, _ := strings.Cut(string(out), "\n")
	if tree == "" {
		return nil, Externalf("reapplying retained candidate %s answered no tree", h.Candidate.RunID)
	}
	tracked, err := boundaryGit(ctx, h.Root, "diff", "--no-renames", "--name-only", "-z", "HEAD")
	if err != nil {
		return nil, err
	}
	untracked, err := boundaryGit(ctx, h.Root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	changed, err := boundaryGit(ctx, h.Root, "diff", "--no-renames", "--name-only", "-z", trunk, tree)
	if err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for _, p := range strings.Split(tracked+untracked, "\x00") {
		if p != "" {
			held[p] = true
		}
	}
	var paths []string
	for _, p := range strings.Split(changed, "\x00") {
		if held[p] {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func (l *Loop) holdDirty(ctx context.Context, c Candidate, paths []string) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	at := l.cfg.Now().UTC()
	hold := dirtyHold{Root: l.cfg.Root, Candidate: c}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := refuseTerminalItem(tx.Reader(), c.ItemID, "trunk-path-dirty"); err != nil {
			return err
		}
		if err := appendEffect(tx, dirtyHeldKind, c.RunID, hold, at); err != nil {
			return err
		}
		d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, RunRequest: c.Request, Seat: c.Seat, From: "candidate", Action: ActionHold, Reason: dirtyReason(paths), PassID: c.TickID}
		identifyDecision(&d)
		return recordDirtyDecision(tx, d, at)
	})
	if err != nil {
		return err
	}
	l.reportf("loop: holding %s (%s); commit or restore the path\n", c.ItemID, dirtyReason(paths))
	return errDirtyHeld
}

// Store a new decision only when the wait changes, including a return to a
// previously seen path set. Ordinary stable decision IDs would deduplicate that
// return and leave the face showing the intervening wait forever.
func recordDirtyDecision(tx *store.Tx, d Decision, at time.Time) error {
	if err := requireHold(tx.Reader(), d.Reason); err != nil {
		return err
	}
	var action, reason string
	var latest int64
	err := tx.QueryRow(`SELECT action,reason,dispatched_at FROM dispatches WHERE item_id=? ORDER BY dispatched_at DESC,decision_id DESC LIMIT 1`, d.ItemID).Scan(&action, &reason, &latest)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && action == ActionHold && reason == d.Reason {
		return nil
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE item_id=?`, d.ItemID).Scan(&count); err != nil {
		return err
	}
	d.DecisionID += fmt.Sprintf("-wait-%d", count)
	if at.UnixMilli() <= latest {
		at = time.UnixMilli(latest + 1).UTC()
	}
	return recordPassDecision(tx, d, at)
}
