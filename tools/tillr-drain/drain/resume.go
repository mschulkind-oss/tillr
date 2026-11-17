package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Resuming a run is continuing one attempt, not spending a second one. The distinction is
// what the store's two tables say:
//
//   - the run id is unchanged, so the runs row is one row (`Apply` upserts it) and the
//     resume is a new span on the same run — a `run.resume` event appended by the claim —
//     rather than a second `runs` row;
//   - no retry authorization is written, read or spent, and no failover hop is walked: the
//     retry budget belongs to attempts, and this is the same attempt with a second segment.
//
// What a resume reuses is everything the run was already given: the lane, its worktree, the
// brief, and the harness session staging pinned in `session-id`. What it takes again is the
// bracket and the fence, because both describe the state the segment starts from:
//
//   - the boundary snapshot is taken at resume, not at dispatch. A run killed after its
//     worker committed leaves a worktree whose HEAD is the candidate; a bracket taken before
//     that commit would compare the resumed segment against state the first segment had
//     already left behind, and the target worktree's own tree is excused by Compare
//     (boundary.go), so nothing else would notice.
//   - the fence is re-derived from the item's own allowed_paths rather than read back out of
//     the staged `fence.txt`. A fence edited between the kill and the resume is the item's
//     current word, and a resume that kept the staged text would judge the continuation
//     against a fence nobody is holding the item to.
//
// The negative control the tests pin: the stale bracket does not catch the killed segment's
// committed change — a target worktree contributes no path deltas — while the fence refuses
// it. That is why the fence is re-derived and the bracket re-taken, rather than either being
// reused.

// RunStep is the one thing a resume does to a run: execute its request. It is an interface so
// a case can drive a resume without a repository and an adapter, and it is deliberately not
// the loop's whole step set — a resume runs one thing and judges nothing.
type RunStep interface {
	Run(ctx context.Context, req run.Request) (run.Record, string, error)
}

// ResumeSpec is one run to continue.
type ResumeSpec struct {
	// RunID is the run whose session is continued. It is also the id the resumed execution
	// runs under, because a resume is one attempt continued.
	RunID string
	// Request is the request to re-execute. Nil reads the one the lane root kept for the
	// run, `<lane-root>/.requests/<run-id>.request.json`, which is where every dispatch
	// writes it. It is refused when neither is available: a resume does not invent the
	// command a run was started with.
	Request *run.Request
}

// ResumeConfig is what a resume needs that the lane directory does not carry.
type ResumeConfig struct {
	// Root is the repository whose boundary the bracket covers.
	Root string
	// Store is the store the run's row, the item's fence and the bracket's event come from
	// and go to.
	Store string
	// LaneRoot is where the lanes live: <lane-root>/<lane-id> is the lane directory.
	LaneRoot string
	// Lanes are the sibling lanes the loop holds. They are named to the bracket so another
	// run's write into one is a violation, exactly as a dispatch's bracket names them. Empty
	// reads them from the store's own lanes table, which is where a face that was given only
	// a store finds them (StoreLanes).
	Lanes []string
	// Steps runs the resumed request.
	Steps RunStep
	// Now is the injected clock the bracket and the resume record are stamped from.
	Now func() time.Time
}

// ResumeResult is what one resume observed, and it is the whole of the answer: the resumed
// run's terminal record, where its log is, the session it continued, the fence it was held
// to, both sides of the bracket, and anything that moved outside the bracket's targets.
type ResumeResult struct {
	Run       run.Record
	Log       string
	LaneID    string
	ItemID    string
	SessionID string
	Fence     []string
	Before    *Boundary
	After     *Boundary
	// Violations are the bracket's findings. A resume reports them rather than deciding
	// anything about them: the caller stops, as a dispatch's tick does.
	Violations []Violation
}

// Resume continues one run that died without a verdict, from its own session.
func Resume(ctx context.Context, cfg ResumeConfig, spec ResumeSpec) (ResumeResult, error) {
	if spec.RunID == "" {
		return ResumeResult{}, Usagef("a resume needs a run: the run whose session is continued")
	}
	if cfg.Root == "" || cfg.Store == "" || cfg.LaneRoot == "" {
		return ResumeResult{}, Usagef("a resume needs a root, a store and a lane root")
	}
	if cfg.Steps == nil {
		return ResumeResult{}, Usagef("a resume needs the step that runs the request")
	}
	now := time.Now
	if cfg.Now != nil {
		now = cfg.Now
	}
	at := now().UTC()
	if len(cfg.Lanes) == 0 {
		lanes, err := StoreLanes(ctx, cfg.Store)
		if err != nil {
			return ResumeResult{}, err
		}
		cfg.Lanes = lanes
	}
	row, err := readRunForResume(ctx, cfg.Store, spec.RunID)
	if err != nil {
		return ResumeResult{}, err
	}
	if err := refuseJudged(ctx, cfg.Store, spec.RunID); err != nil {
		return ResumeResult{}, err
	}
	laneDir := filepath.Join(cfg.LaneRoot, row.LaneID)
	worktree := filepath.Join(laneDir, WorktreeDir)
	if row.ReadOnlyGroup != "" {
		laneDir = run.NodeDir(laneDir, spec.RunID)
	}
	// The lane layout is read before the session: a lane whose brief is gone is not a lane a
	// resume can continue, and saying so is cheaper than the adapter's failure later.
	if _, err := os.Stat(filepath.Join(laneDir, BriefFile)); err != nil {
		return ResumeResult{}, Externalf("the lane %s holds no %s: a resume continues a staged run, and this lane has no brief to continue (%v)",
			row.LaneID, BriefFile, err)
	}
	sessionID, err := ReadSessionID(laneDir)
	if err != nil {
		return ResumeResult{}, err
	}
	if sessionID == "" {
		return ResumeResult{}, Externalf("run %s cannot be resumed: the lane %s carries no session id, so there is no session to continue and the resume would pay for the transcript again (%s)",
			spec.RunID, row.LaneID, filepath.Join(laneDir, SessionIDFile))
	}
	fence, err := rederiveFence(ctx, cfg, row, laneDir, spec.RunID)
	if err != nil {
		return ResumeResult{}, err
	}
	request, err := resumeRequest(cfg, row, spec)
	if err != nil {
		return ResumeResult{}, err
	}
	targets := []string{worktree}
	before, err := boundaryFor(ctx, cfg, row.LaneID, targets)
	if err != nil {
		return ResumeResult{}, err
	}
	rec, log, runErr := cfg.Steps.Run(ctx, *request)
	// The bracket is closed on the state the resume left, whatever the run answered: a run
	// that failed still wrote, and a bracket left open is a bracket nobody compared.
	after, err := boundaryFor(ctx, cfg, row.LaneID, targets)
	if err != nil {
		return ResumeResult{}, err
	}
	// A resumed segment is a run, judged by the run rule: convicted on what it can be
	// shown to have done, with the rest of the repository's movement observed only.
	violations, observed, err := CompareRunObserved(before, after)
	if err != nil {
		return ResumeResult{}, err
	}
	result := ResumeResult{
		Run: rec, Log: log, LaneID: row.LaneID, ItemID: row.ItemID, SessionID: sessionID,
		Fence: fence, Before: before, After: after, Violations: violations,
	}
	// The bracket is recorded whether it was clean or not, for the reason a dispatch's is:
	// a stop whose bracket is in no record is the hole the observatory exists to close.
	// The object id is the run plus this segment's stamp rather than the run alone, because
	// appendJudgment is idempotent per object and two real resumes of one run are two
	// brackets, not a replay of one.
	if err := appendBoundaryCheck(ctx, cfg.Store, BoundaryScopeResume, spec.RunID+"@"+stamp(at), spec.RunID, row, targets, violations, at, observed...); err != nil {
		return result, err
	}
	return result, runErr
}

// StoreLanes answers every lane id the store holds, in lane-id order. It is how a resume
// learned from nothing but a store names the sibling lanes to its bracket: a lane the store
// does not hold is not a lane another run can write into, and a resume that named none would
// stop noticing a write into a lane the loop is holding.
func StoreLanes(ctx context.Context, storePath string) ([]string, error) {
	r, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return nil, Incompatiblef("opening the store %s: %v", storePath, err)
	}
	defer r.Close()
	lanes := []string{}
	err = r.ReadTx(ctx, func(reader *store.Reader) error {
		rows, err := reader.Query(`SELECT lane_id FROM lanes ORDER BY lane_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var lane string
			if err := rows.Scan(&lane); err != nil {
				return err
			}
			lanes = append(lanes, lane)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, readFailure(err)
	}
	return lanes, nil
}

// resumeRun is one run's row as a resume reads it: what it belongs to, and what it ran on.
type resumeRun struct {
	ReadOnlyGroup string
	LaneID        string
	ItemID        string
	Backend       string
	State         string
}

// readRunForResume answers the row of the run a resume continues, refusing a run the store
// cannot name. A run killed before it wrote a terminal record has no row until the reaper
// closes it, and a resume of a run with no row would have to invent the backend and the lane
// it continued: the reaper's `timed-out` record is what makes such a run resumable, and a
// resume that ran before it is a resume of nothing.
func readRunForResume(ctx context.Context, storePath, runID string) (resumeRun, error) {
	r, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return resumeRun{}, Incompatiblef("opening the store %s: %v", storePath, err)
	}
	defer r.Close()
	var row resumeRun
	var itemID *string
	err = r.ReadTx(ctx, func(reader *store.Reader) error {
		if err := reader.QueryRow(`SELECT lane_id,item_id,backend,state FROM runs WHERE run_id=?`, runID).Scan(&row.LaneID, &itemID, &row.Backend, &row.State); err != nil {
			return err
		}
		var exists bool
		if err := reader.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='read_only_runs')`).Scan(&exists); err != nil {
			return err
		}
		if exists {
			err := reader.QueryRow(`SELECT group_id FROM read_only_runs WHERE run_id=?`, runID).Scan(&row.ReadOnlyGroup)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return resumeRun{}, Usagef("the store holds no run %s: a resume continues a run the store can name, and a run killed before it wrote a terminal record becomes resumable once the reaper has closed it (`tillr-run release`, or the loop's own reap)", runID)
	}
	if err != nil {
		return resumeRun{}, readFailure(err)
	}
	if itemID != nil {
		row.ItemID = *itemID
	}
	return row, nil
}

// refuseJudged refuses a run the gate has already judged. A judged run's continuation is a
// second attempt at judged work, which is a person's decision (the retry authorization) and
// not a resume's: the resume exists for the run that died before anything judged it.
func refuseJudged(ctx context.Context, storePath, runID string) error {
	r, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return Incompatiblef("opening the store %s: %v", storePath, err)
	}
	defer r.Close()
	var judged int
	if err := r.ReadTx(ctx, func(reader *store.Reader) error {
		return reader.QueryRow(
			`SELECT count(*) FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ?`,
			GateVerdictKind, runID,
		).Scan(&judged)
	}); err != nil {
		return readFailure(err)
	}
	if judged > 0 {
		return Usagef("run %s was judged by the gate (%d verdict event(s)): a resume continues a run that died without a verdict, and continuing judged work is a retry — a person authorizes one, and it spends the attempt budget this resume deliberately leaves alone", runID, judged)
	}
	return nil
}

// rederiveFence writes the current item fence and the saved node's narrower scope
// to both the lane and run copies, and answers the resumed run's scope.
//
// The lane's `fence.txt` is the staged text; re-deriving it is what makes a fence edited
// after the kill the one the continuation is judged by. The item is read from the store, so
// an item whose allowed_paths were cleared or emptied is refused here rather than resumed
// under a fence that admits nothing.
func rederiveFence(ctx context.Context, cfg ResumeConfig, row resumeRun, laneDir, runID string) ([]string, error) {
	if row.ItemID == "" {
		return nil, Externalf("run %s names no item: a resume re-derives the fence from the item's allowed_paths, and a run with no item has no fence to derive", runID)
	}
	recorded, err := readStagedItem(ctx, cfg.Store, cfg.Root, row.ItemID)
	if err != nil {
		return nil, err
	}
	// A triage continuation remains a one-document run. The ordinary item's
	// build fence must not broaden the design run's original authority.
	r, err := store.OpenRead(ctx, cfg.Store)
	if err != nil {
		return nil, err
	}
	var node *WorkflowStage
	err = r.ReadTx(ctx, func(reader *store.Reader) error {
		_, works, err := readPendingHistory(reader)
		if err != nil {
			return err
		}
		if work := works[runID]; work != nil && work.Decision.ItemID == row.ItemID {
			d := work.Decision
			if c := d.BlockConflict; c != nil && c.TriageVersion != "" {
				recorded.AllowedPaths = []string{triageDocument(c.BodyRef)}
			}
			if row.LaneID == "" {
				row.LaneID = d.LaneID
			}
			if d.Workflow != nil {
				if d.NodeIndex < 0 || d.NodeIndex >= len(d.Workflow.Stages) {
					return Externalf("run %s has no saved workflow node", runID)
				}
				node = &d.Workflow.Stages[d.NodeIndex]
			}
		}
		return nil
	})
	r.Close()
	if err != nil {
		return nil, err
	}
	if len(recorded.AllowedPaths) == 0 {
		return nil, Externalf("item %s names no allowed_paths: a resume refuses to continue under a fence that admits nothing", recorded.ItemID)
	}
	fence := fencePaths(recorded.AllowedPaths)
	if node != nil {
		if err := (&Workflow{Stages: []WorkflowStage{*node}}).checkScopes(recorded.AllowedPaths); err != nil {
			return nil, err
		}
		fence, err = node.scopePaths(recorded.AllowedPaths)
		if err != nil {
			return nil, err
		}
	}
	itemFence := fencePaths(recorded.AllowedPaths)
	for _, paths := range [][]string{fence, itemFence} {
		for i, raw := range paths {
			if _, name, suffix, ok := item.ForkCheckoutPath(raw); ok {
				paths[i] = ".forks/lanes/" + row.LaneID + "/" + name + suffix
			}
		}
	}
	for _, dir := range []string{laneDir, RunDir(cfg.Root, runID)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, Externalf("creating %s: %v", dir, err)
		}
		for name, paths := range map[string][]string{FenceFile: fence, ItemFenceFile: itemFence} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(fenceText(paths)), 0o644); err != nil {
				return nil, Externalf("writing %s: %v", filepath.Join(dir, name), err)
			}
		}
	}
	return fence, nil
}

// resumeRequest answers the request the resumed segment executes: the one the run was
// dispatched with, marked as a resume.
//
// The request is read rather than rebuilt because a resume must run the same command, model
// and bound the first segment did: a request drawn again from today's policy could reach a
// different model under the same run id, which would make the record's route a lie about the
// transcript it names.
func resumeRequest(cfg ResumeConfig, row resumeRun, spec ResumeSpec) (*run.Request, error) {
	request := spec.Request
	if request == nil {
		path := filepath.Join(cfg.LaneRoot, ".requests", spec.RunID+".request.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, Externalf("reading the request run %s was dispatched with, at %s: %v (pass the request explicitly when the lane root did not keep one)", spec.RunID, path, err)
		}
		env, err := record.Decode(raw)
		if err != nil {
			return nil, Usagef("reading %s: %v", path, err)
		}
		if env.Kind != run.RequestKind {
			return nil, Usagef("%s holds a %q record, want %s", path, env.Kind, run.RequestKind)
		}
		var decoded run.Request
		if err := json.Unmarshal(env.Payload, &decoded); err != nil {
			return nil, Usagef("reading the %s payload in %s: %v", run.RequestKind, path, err)
		}
		request = &decoded
	}
	resumed := *request
	if resumed.RunID != spec.RunID {
		return nil, Usagef("the request names run %q, but the resume continues %q: a resume re-executes the run it continues", resumed.RunID, spec.RunID)
	}
	if resumed.LaneID != "" && resumed.LaneID != row.LaneID {
		return nil, Usagef("the request names lane %q, but run %s is on lane %q: a resume continues the lane its candidate is in", resumed.LaneID, spec.RunID, row.LaneID)
	}
	if len(resumed.Command) == 0 {
		return nil, Externalf("the request for run %s carries no command: nothing to execute", spec.RunID)
	}
	resumed.LaneID = row.LaneID
	resumed.Backend = row.Backend
	resumed.Resume = true
	return &resumed, nil
}

// boundaryFor takes one side of the resume's bracket: the lane worktree the segment owns, and
// the sibling lanes the loop holds (snapshot's own rule, applied to a resume's target set).
func boundaryFor(ctx context.Context, cfg ResumeConfig, laneID string, targets []string) (*Boundary, error) {
	spec := BoundarySpec{Repo: cfg.Root, Targets: targets, LaneRoot: cfg.LaneRoot}
	for _, lane := range cfg.Lanes {
		// A lane is watched at its worktree, which is the directory a run writes in:
		// the lane directory beside it holds the brief and the log, and is not a
		// checkout a boundary can name.
		path := filepath.Join(cfg.LaneRoot, lane, WorktreeDir)
		if isTarget(targets, path) {
			continue
		}
		spec.Lanes = append(spec.Lanes, path)
	}
	// The patch ref a fork-series lane delivers through is named beside the worktree it
	// owns, exactly as the loop's own bracket names it: a delivery is not an escape.
	if ref := laneFileValue(cfg.LaneRoot, laneID, PatchRefFile); ref != "" {
		spec.PatchRefs = append(spec.PatchRefs, ref)
	}
	return TakeBoundary(ctx, spec)
}

// laneFileValue answers one staged file of a lane, trimmed, or "" when it is not there. It is
// a read of the lane layout and never a default: a lane with no patch ref is a lane that
// names no fork, which is the ordinary case.
func laneFileValue(laneRoot, lane, name string) string {
	raw, err := os.ReadFile(filepath.Join(laneRoot, lane, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
