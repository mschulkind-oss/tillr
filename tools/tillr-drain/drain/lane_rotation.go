package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
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
	// ReasonLaneChildren holds an idle lane while an ended run's marked processes
	// have working directories inside it.
	ReasonLaneChildren = "lane-run-children-live"
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
	LaneID        string
	Reason        string
	Remedy        string
	LeftBy        string
	ExcludedRunID string
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
// Existing drain.lane-refused events carried only lane_id, reason, detail and remedy;
// left_by is the single added field attributing which run/item left the work, while
// drain.lane-restored ({"lane_id": lane}) remains unchanged.
type laneRefusal struct {
	LaneID string `json:"lane_id"`
	Reason string `json:"reason"`
	Detail string `json:"detail"`
	Remedy string `json:"remedy"`
	LeftBy string `json:"left_by"`
}

// laneCondition is one lane's refusal as this loop session knows it. Out is whether the lane
// is out of rotation now; a condition stays known after a re-check puts the lane back, so a
// re-check that refuses the same way records nothing new.
type laneCondition struct {
	reason, detail, remedy, leftBy string
	fingerprint                    string
	at                             time.Time
	out                            bool
	// recorded is whether this exact condition is already a drain.lane-refused event, and
	// printed whether this session has printed its line.
	recorded, printed bool
}

// rotationLanes answers the configured lanes not out of rotation.
func (l *Loop) rotationLanes() []string {
	l.laneMu.Lock()
	defer l.laneMu.Unlock()
	offered := l.lanes()
	lanes := make([]string, 0, len(offered))
	for _, lane := range offered {
		if c := l.laneConditions[lane]; c != nil && c.out {
			continue
		}
		lanes = append(lanes, lane)
	}
	return lanes
}

// hasLaneCondition matches even a condition provisionally put back in rotation.
func (l *Loop) hasLaneCondition(lane, reason string) bool {
	l.laneMu.Lock()
	defer l.laneMu.Unlock()
	c := l.laneConditions[lane]
	return c != nil && (reason == "" || c.reason == reason)
}

// takeLaneOutOfRotation records a lane-owned refusal: one event and one line per condition,
// and nothing while the same condition persists.
func (l *Loop) takeLaneOutOfRotation(ctx context.Context, refusal *LaneRefusalError) error {
	if l.cfg.Store != "" {
		_ = l.loadLaneConditions(ctx)
	}
	l.laneMu.Lock()
	_, err := l.recordLaneRefusal(ctx, refusal)
	l.laneMu.Unlock()
	if err == nil {
		l.signalPassWake()
	}
	return err
}

func (l *Loop) recordLaneRefusal(ctx context.Context, refusal *LaneRefusalError) (time.Time, error) {
	lane := refusal.LaneID
	if l.laneConditions == nil {
		l.laneConditions = map[string]*laneCondition{}
	}
	c := l.laneConditions[lane]
	same := c != nil && c.reason == refusal.Reason && c.detail == refusal.Msg
	if !same {
		leftBy := refusal.LeftBy
		if leftBy == "" {
			leftBy = l.resolveLaneLeftBy(ctx, lane, refusal.ExcludedRunID)
		}
		c = &laneCondition{reason: refusal.Reason, detail: refusal.Msg, remedy: refusal.Remedy, leftBy: leftBy}
		l.laneConditions[lane] = c
	}
	c.out, c.at = true, l.cfg.Now().UTC()
	if c.reason != ReasonLaneChildren {
		c.fingerprint = l.laneFingerprint(ctx, lane)
	}
	if !c.recorded {
		if err := appendLaneEvent(ctx, l.cfg.Store, laneRefusedKind, lane, laneRefusal{LaneID: lane, Reason: c.reason, Detail: c.detail, Remedy: c.remedy, LeftBy: c.leftBy}, c.at); err != nil {
			return c.at, err
		}
		c.recorded = true
	}
	if !c.printed {
		recheck := fmt.Sprintf("when its HEAD or status changes, or every %s", laneRecheckInterval)
		if c.reason == ReasonLaneChildren {
			recheck = "on every pass, until no marked process remains inside it"
		}
		l.reportf("loop: lane %s is out of rotation (%s): %s. Remedy: %s. It is re-checked %s\n",
			lane, c.reason, c.detail, c.remedy, recheck)
		c.printed = true
	}
	return c.at, nil
}

// restoreLane records a lane that staged after a refusal back in rotation.
func (l *Loop) restoreLane(ctx context.Context, lane string) error {
	l.laneMu.Lock()
	if l.laneConditions[lane] == nil {
		l.laneMu.Unlock()
		return nil
	}
	delete(l.laneConditions, lane)
	at := l.cfg.Now().UTC()
	err := appendLaneEvent(ctx, l.cfg.Store, laneRestoredKind, lane, struct {
		LaneID string `json:"lane_id"`
	}{lane}, at)
	l.laneMu.Unlock()
	if err != nil {
		return err
	}
	l.signalPassWake()
	l.reportf("loop: lane %s is back in rotation: it is available to stage again\n", lane)
	return nil
}

// recheckLanes returns to rotation each lane whose fingerprint changed or whose slow
// interval passed. The next staging on it is the re-check: a lane refused the same way goes
// back out silently.
func (l *Loop) recheckLanes(ctx context.Context) error {
	if err := l.recheckLaneChildren(ctx); err != nil {
		return err
	}
	if l.replanning {
		return nil
	}
	now := l.cfg.Now().UTC()
	l.laneMu.Lock()
	restored := false
	var cleanLanes []string
	for lane, c := range l.laneConditions {
		if c.reason == ReasonLaneChildren {
			continue
		}
		if c.reason == ReasonLaneUncommittedWork && !strings.HasPrefix(c.detail, "fork checkout ") {
			if l.laneWorktreeClean(ctx, lane) {
				cleanLanes = append(cleanLanes, lane)
			}
			continue
		}
		if !c.out {
			continue
		}
		retained := l.laneHeadRetained(ctx, lane, c)
		if retained || now.Sub(c.at) >= laneRecheckInterval || l.laneFingerprint(ctx, lane) != c.fingerprint {
			if !retained && l.laneStillRefuses(ctx, lane, c) {
				// The lane refuses the same way: it stays out, and no item is
				// allocated to it to find that out.
				c.at, c.fingerprint = now, l.laneFingerprint(ctx, lane)
				continue
			}
			c.out = false
			restored = true
		}
	}
	l.laneMu.Unlock()
	slices.Sort(cleanLanes)
	for _, lane := range cleanLanes {
		if err := l.restoreLane(ctx, lane); err != nil {
			return err
		}
	}
	if restored {
		l.signalPassWake()
	}
	return nil
}

// laneWorktreeClean answers whether lane's worktree exists and has empty git status.
// It reads with optional locks off, so it never writes the index.
func (l *Loop) laneWorktreeClean(ctx context.Context, lane string) bool {
	if l.cfg.LaneRoot == "" {
		return false
	}
	worktree := l.lanePath(lane)
	if _, err := os.Stat(worktree); err != nil {
		return false
	}
	cleanDir := func(dir string) bool {
		cmd := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain", "--untracked-files=all")
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		out, err := cmd.Output()
		return err == nil && strings.TrimSpace(string(out)) == ""
	}
	if !cleanDir(worktree) {
		return false
	}
	if target := l.laneTarget(lane); target != worktree {
		if _, err := os.Stat(target); err == nil && !cleanDir(target) {
			return false
		}
	}
	return true
}

// LaneProber is a Stager that can answer whether a lane's own state still refuses staging,
// without a claim, a run or a write. It reports the lane-owned refusal, or nil when the lane
// would stage.
type LaneProber interface {
	ProbeLane(ctx context.Context, lane, base string) error
}

// ProbeLane is refuseUnlandedCommit on the lane's worktree: the one lane-owned refusal that
// persists with no change to the lane itself, because only the trunk moving can clear it.
func (s *LaneStager) ProbeLane(ctx context.Context, lane, base string) error {
	return s.refuseUnlandedCommit(ctx, StageSpec{LaneID: lane, BaseSHA: base}, filepath.Join(s.LaneRoot, lane, WorktreeDir))
}

// laneStillRefuses re-checks a lane out of rotation for an unlanded change before it is
// offered again. An allocation made only to learn that the lane still refuses is an attempt
// number and a pass spent on no work, so a stager that can probe is asked first. A probe
// that cannot answer returns the lane to rotation, where staging decides as it always did.
func (l *Loop) laneStillRefuses(ctx context.Context, lane string, c *laneCondition) bool {
	if c.reason != ReasonLaneUnlandedChange {
		return false
	}
	prober, ok := l.cfg.Stage.(LaneProber)
	if !ok {
		return false
	}
	base, err := l.revParse(ctx, l.cfg.Root, l.trunkRef())
	if err != nil {
		return false
	}
	refusal := asLaneRefusal(prober.ProbeLane(ctx, lane, strings.TrimSpace(base)))
	return refusal != nil && refusal.Reason == c.reason && refusal.Msg == c.detail
}

// laneHeadRetained answers whether a lane refused for holding an unlanded change now holds
// a commit that is retained elsewhere: a run pin contains its HEAD, or trunk does. Pinning
// an offered commit changes neither the lane's HEAD nor its status, so the fingerprint
// cannot see it, yet staging now clears such a copy (clearableLaneCopy) instead of
// refusing it. The lane returns to rotation, and its next staging is the re-check.
func (l *Loop) laneHeadRetained(ctx context.Context, lane string, c *laneCondition) bool {
	if c.reason != ReasonLaneUnlandedChange {
		return false
	}
	head, err := l.revParse(ctx, l.lanePath(lane), "HEAD")
	if err != nil || head == "" {
		return false
	}
	if out, err := l.git(ctx, l.cfg.Root, "for-each-ref", "--contains", head, "--format=%(refname)", "refs/tillr/runs/"); err == nil && strings.TrimSpace(out) != "" {
		return true
	}
	contained, err := containsCommit(ctx, l.cfg.Root, head, l.trunkRef())
	return err == nil && contained
}

// loadLaneConditions seeds the session with the conditions the store still records — a
// drain.lane-refused with no later drain.lane-restored — so a restarted loop does not
// record an unchanged condition again. Each starts in rotation: the first staging re-checks
// it.
func (l *Loop) loadLaneConditions(ctx context.Context) error {
	l.laneMu.Lock()
	defer l.laneMu.Unlock()
	if l.laneConditionsLoaded {
		return nil
	}
	if l.laneConditions == nil {
		l.laneConditions = map[string]*laneCondition{}
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	defer s.Close()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
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
			l.laneConditions[lane] = &laneCondition{reason: p.Reason, detail: p.Detail, remedy: p.Remedy, leftBy: p.LeftBy, recorded: true}
		}
		return rows.Err()
	})
	l.laneConditionsLoaded = err == nil
	return err
}

// laneFingerprint is what a re-check compares: the lane worktree's HEAD and status,
// lane branch, and any other worktree holding that branch. It reads with optional locks
// off, so a fingerprint never writes the index.
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
	branch := LaneBranch(l.cfg.LaneRoot, lane)
	holder, err := branchHolder(ctx, l.cfg.Root, branch, worktree)
	if err != nil {
		holder = "error"
	}
	return strings.Join([]string{
		read(worktree, "rev-parse", "HEAD"),
		read(worktree, "status", "--porcelain"),
		read(l.cfg.Root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch),
		holder,
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

// outLanes answers the lanes a refusal has taken out of rotation.
func (l *Loop) outLanes() map[string]bool {
	l.laneMu.Lock()
	defer l.laneMu.Unlock()
	out := map[string]bool{}
	for lane, c := range l.laneConditions {
		if c != nil && c.out {
			out[lane] = true
		}
	}
	return out
}

func formatLeftBy(runID, itemID string) string {
	runID = strings.TrimSpace(runID)
	itemID = strings.TrimSpace(itemID)
	switch {
	case runID != "" && itemID != "":
		return fmt.Sprintf("%s (%s)", runID, itemID)
	case runID != "":
		return runID
	case itemID != "":
		return itemID
	default:
		return "unknown"
	}
}

func (l *Loop) resolveLaneLeftBy(ctx context.Context, lane, excludedRunID string) string {
	if l.cfg.Store == "" || lane == "" {
		return "unknown"
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "unknown"
	}
	defer s.Close()
	var runID, itemID string
	_ = s.ReadTx(ctx, func(r *store.Reader) error {
		runID, itemID = lookupLaneLeftBy(r, lane, excludedRunID)
		return nil
	})
	return formatLeftBy(runID, itemID)
}

func lookupLaneLeftBy(r *store.Reader, lane, excludedRunID string) (string, string) {
	var runID, itemID string
	err := r.QueryRow(
		`SELECT run_id, COALESCE(item_id, '') FROM runs WHERE lane_id = ? AND (? = '' OR run_id != ?) ORDER BY event_seq DESC, rowid DESC LIMIT 1`,
		lane, excludedRunID, excludedRunID,
	).Scan(&runID, &itemID)
	if err == nil && (runID != "" || itemID != "") {
		return runID, itemID
	}

	rows, err := r.Query(
		`SELECT payload FROM events WHERE kind = ? AND object_type = 'lane' AND object_id = ? ORDER BY seq DESC`,
		run.AllocateKind, lane,
	)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if scanErr := rows.Scan(&raw); scanErr != nil {
				break
			}
			var p struct {
				RunID  string `json:"run_id"`
				ItemID string `json:"item_id"`
			}
			if jsonErr := json.Unmarshal(raw, &p); jsonErr != nil {
				continue
			}
			if excludedRunID != "" && p.RunID == excludedRunID {
				continue
			}
			if p.RunID != "" || p.ItemID != "" {
				return p.RunID, p.ItemID
			}
		}
	}

	runID, itemID = "", ""
	err = r.QueryRow(
		`SELECT COALESCE(run_id, ''), item_id FROM dispatches WHERE lane_id = ? AND (? = '' OR COALESCE(run_id, '') != ?) ORDER BY dispatched_at DESC, rowid DESC LIMIT 1`,
		lane, excludedRunID, excludedRunID,
	).Scan(&runID, &itemID)
	if err == nil && (runID != "" || itemID != "") {
		return runID, itemID
	}

	return "", ""
}

func (l *Loop) resolveRunLeftBy(ctx context.Context, lane, runID string) string {
	if runID == "" {
		return "unknown"
	}
	if l.cfg.Store == "" {
		return formatLeftBy(runID, "")
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return formatLeftBy(runID, "")
	}
	defer s.Close()
	var itemID string
	_ = s.ReadTx(ctx, func(r *store.Reader) error {
		itemID = lookupRunItemID(r, lane, runID)
		return nil
	})
	return formatLeftBy(runID, itemID)
}

func lookupRunItemID(r *store.Reader, lane, runID string) string {
	if runID == "" {
		return ""
	}
	var itemID string
	if err := r.QueryRow(`SELECT COALESCE(item_id, '') FROM runs WHERE run_id = ? LIMIT 1`, runID).Scan(&itemID); err == nil && itemID != "" {
		return itemID
	}
	rows, err := r.Query(
		`SELECT payload FROM events WHERE kind = ? AND object_type = 'lane' AND object_id = ? ORDER BY seq DESC`,
		run.AllocateKind, lane,
	)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if scanErr := rows.Scan(&raw); scanErr != nil {
				break
			}
			var p struct {
				RunID  string `json:"run_id"`
				ItemID string `json:"item_id"`
			}
			if jsonErr := json.Unmarshal(raw, &p); jsonErr != nil {
				continue
			}
			if p.RunID == runID && p.ItemID != "" {
				return p.ItemID
			}
		}
	}
	if err := r.QueryRow(
		`SELECT item_id FROM dispatches WHERE run_id = ? ORDER BY dispatched_at DESC, rowid DESC LIMIT 1`,
		runID,
	).Scan(&itemID); err == nil && itemID != "" {
		return itemID
	}
	return ""
}
