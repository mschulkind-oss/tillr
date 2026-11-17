package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The lane conditions a staging refusal names. A lane-owned refusal is about the lane's own
// state, never the item offered it: the next item offered that lane would be refused the
// same way, so the lane — not the item — leaves rotation.
const (
	// ReasonLaneUnlandedChange is a lane whose worktree or branch holds a commit the base
	// does not contain (refuseUnlandedCommit): a candidate nobody landed, which staging
	// will not reset.
	ReasonLaneUnlandedChange = "lane-holds-unlanded-change"
	// ReasonLaneBranchHeld is a lane whose branch another worktree has checked out
	// (refuseBranchHeld).
	ReasonLaneBranchHeld = "lane-branch-held"
	// ReasonLaneUncommittedWork is a lane whose worktree holds uncommitted or untracked
	// files when a pinned candidate is staged onto it (stagePinnedCandidate), which
	// staging will not overwrite.
	ReasonLaneUncommittedWork = "lane-holds-uncommitted-work"
)

const (
	// laneRefusedKind records a lane taken out of rotation, once per condition: its
	// object is the lane, and its payload is laneRefusal.
	laneRefusedKind = "drain.lane-refused"
	// laneRestoredKind records that lane back in rotation: it staged again.
	laneRestoredKind = "drain.lane-restored"
)

// laneRecheckInterval is how long an unchanged lane stays out of rotation before the loop
// stages it again to see whether the condition still holds. A lane whose HEAD, branch or
// status changes is re-checked on the next pass instead.
const laneRecheckInterval = 10 * time.Minute

func init() {
	for _, kind := range []string{laneRefusedKind, laneRestoredKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

// LaneRefusalError is a staging refusal the lane's own state caused. It keeps the external
// exit class a face answering one item reports, and adds what an operator needs: the lane,
// the condition and the remedy.
type LaneRefusalError struct {
	*ExitError
	LaneID string
	Reason string
	Remedy string
}

func (e *LaneRefusalError) Unwrap() error { return e.ExitError }

// laneRefusalf is the lane-refusal constructor.
func laneRefusalf(lane, reason, remedy, format string, args ...any) error {
	return &LaneRefusalError{
		ExitError: &ExitError{Code: record.ExitExternal, Msg: fmt.Sprintf(format, args...)},
		LaneID:    lane, Reason: reason, Remedy: remedy,
	}
}

// unlandedLaneRefusal is refuseUnlandedCommit's refusal.
func unlandedLaneRefusal(lane, sha, base string) error {
	return laneRefusalf(lane, ReasonLaneUnlandedChange,
		"land the change, or reset the lane deliberately (tillr-drain README, \"Resetting a lane\")",
		"lane %s holds a change nobody landed: %s is not on the base %s, and staging does not reset a candidate. Land it, or reset the lane deliberately",
		lane, sha, base)
}

func asLaneRefusal(err error) *LaneRefusalError {
	var e *LaneRefusalError
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// laneRefusal is drain.lane-refused's payload.
type laneRefusal struct {
	LaneID string `json:"lane_id"`
	Reason string `json:"reason"`
	Detail string `json:"detail"`
	Remedy string `json:"remedy"`
}

// laneCondition is one lane's refusal as this loop session knows it. Out is whether the lane
// is out of rotation now; a condition stays known after a re-check puts the lane back, so a
// re-check that refuses the same way records nothing new.
type laneCondition struct {
	reason, detail, remedy string
	fingerprint            string
	at                     time.Time
	out                    bool
	// recorded is whether this exact condition is already a drain.lane-refused event, and
	// printed whether this session has printed its line.
	recorded, printed bool
}

// rotationLanes answers the configured lanes not out of rotation.
func (l *Loop) rotationLanes() []string {
	lanes := make([]string, 0, len(l.cfg.Lanes))
	for _, lane := range l.cfg.Lanes {
		if c := l.laneConditions[lane]; c != nil && c.out {
			continue
		}
		lanes = append(lanes, lane)
	}
	return lanes
}

// takeLaneOutOfRotation records a lane-owned refusal: one event and one line per condition,
// and nothing while the same condition persists.
func (l *Loop) takeLaneOutOfRotation(ctx context.Context, refusal *LaneRefusalError) error {
	lane := refusal.LaneID
	if l.laneConditions == nil {
		l.laneConditions = map[string]*laneCondition{}
	}
	c := l.laneConditions[lane]
	same := c != nil && c.reason == refusal.Reason && c.detail == refusal.Msg
	if !same {
		c = &laneCondition{reason: refusal.Reason, detail: refusal.Msg, remedy: refusal.Remedy}
		l.laneConditions[lane] = c
	}
	c.out, c.at, c.fingerprint = true, l.cfg.Now().UTC(), l.laneFingerprint(ctx, lane)
	if !c.recorded {
		if err := appendLaneEvent(ctx, l.cfg.Store, laneRefusedKind, lane, laneRefusal{LaneID: lane, Reason: c.reason, Detail: c.detail, Remedy: c.remedy}, c.at); err != nil {
			return err
		}
		c.recorded = true
	}
	if !c.printed {
		l.reportf("loop: lane %s is out of rotation (%s): %s. Remedy: %s. It is re-checked when its HEAD or status changes, or every %s\n",
			lane, c.reason, c.detail, c.remedy, laneRecheckInterval)
		c.printed = true
	}
	return nil
}

// restoreLane records a lane that staged after a refusal back in rotation.
func (l *Loop) restoreLane(ctx context.Context, lane string) error {
	c := l.laneConditions[lane]
	if c == nil {
		return nil
	}
	delete(l.laneConditions, lane)
	if err := appendLaneEvent(ctx, l.cfg.Store, laneRestoredKind, lane, struct {
		LaneID string `json:"lane_id"`
	}{lane}, l.cfg.Now().UTC()); err != nil {
		return err
	}
	l.reportf("loop: lane %s is back in rotation: it stages again\n", lane)
	return nil
}

// recheckLanes returns to rotation each lane whose fingerprint changed or whose slow
// interval passed. The next staging on it is the re-check: a lane refused the same way goes
// back out silently.
func (l *Loop) recheckLanes(ctx context.Context) error {
	if l.laneConditions == nil {
		l.laneConditions = map[string]*laneCondition{}
	}
	if !l.laneConditionsLoaded {
		if err := l.loadLaneConditions(ctx); err != nil {
			return err
		}
		l.laneConditionsLoaded = true
	}
	now := l.cfg.Now().UTC()
	for lane, c := range l.laneConditions {
		if !c.out {
			continue
		}
		if now.Sub(c.at) >= laneRecheckInterval || l.laneFingerprint(ctx, lane) != c.fingerprint {
			c.out = false
		}
	}
	return nil
}

// loadLaneConditions seeds the session with the conditions the store still records — a
// drain.lane-refused with no later drain.lane-restored — so a restarted loop does not
// record an unchanged condition again. Each starts in rotation: the first staging re-checks
// it.
func (l *Loop) loadLaneConditions(ctx context.Context) error {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT kind, object_id, payload FROM events WHERE kind IN (?, ?) AND object_type = 'lane' ORDER BY seq`, laneRefusedKind, laneRestoredKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var kind, lane string
			var raw []byte
			if err := rows.Scan(&kind, &lane, &raw); err != nil {
				return err
			}
			if kind == laneRestoredKind {
				delete(l.laneConditions, lane)
				continue
			}
			var p laneRefusal
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			l.laneConditions[lane] = &laneCondition{reason: p.Reason, detail: p.Detail, remedy: p.Remedy, recorded: true}
		}
		return rows.Err()
	})
}

// laneFingerprint is what a re-check compares: the lane worktree's HEAD and status and the
// lane branch. It reads with optional locks off, so a fingerprint never writes the index.
func (l *Loop) laneFingerprint(ctx context.Context, lane string) string {
	read := func(dir string, args ...string) string {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		out, err := cmd.Output()
		if err != nil {
			return "error"
		}
		return strings.TrimSpace(string(out))
	}
	worktree := l.lanePath(lane)
	return strings.Join([]string{
		read(worktree, "rev-parse", "HEAD"),
		read(worktree, "status", "--porcelain"),
		read(l.cfg.Root, "rev-parse", "--verify", "--quiet", "refs/heads/"+LaneBranch(l.cfg.LaneRoot, lane)),
	}, "\x00")
}

// appendLaneEvent appends one event whose object is a lane.
func appendLaneEvent(ctx context.Context, storePath, kind, lane string, payload any, at time.Time) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return err
	}
	defer s.Close()
	id := lane
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: kind, ObjectType: "lane", ObjectID: &id, Payload: body, At: at})
		return err
	})
}
