package drain

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// BoundarySpec is the span one boundary snapshot covers: the repository whose refs and
// worktrees are the boundary, the worktrees this bracket's runs own — each one's own
// working tree is its run's work and is not boundary movement — and the other worktrees
// the caller holds.
//
// Targets is a set of *worktrees*, never a set of paths: naming a worktree here excuses
// that worktree's own working tree and the branch it has checked out. Writes in a watched
// sibling worktree remain violations. Ordinary external refs and main-checkout content
// are outside the snapshot pair's attribution scope, whether or not a target names them.
// A reader looking for "paths this bracket may write" is looking for the wrong thing.
//
// Lanes are named by the caller and are not discovered, because discovery is what makes
// this check too expensive to run per tick: a checkout of this repository carries
// hundreds of registered worktrees, most of them abandoned artifacts of earlier eras, and
// a `git status` in each is seconds per snapshot and a verdict on none of them. The lane
// set is the set the loop can stage or land from, so it is the set whose content a run
// must not touch.
//
// Store is optional and must be left empty when the bracket wraps `tillr run exec`: run
// writes the claim and the terminal run.record into the store inside that bracket, by
// design, so a store delta there has a legitimate cause and cannot be a stop signal. Name
// it only from a caller whose bracket wraps the adapter process alone.
type BoundarySpec struct {
	Repo    string
	Targets []string
	Lanes   []string
	Store   string
	// PatchRefs are refs this bracket's runs may move besides the branch their own
	// worktree has checked out: a fork-series lane's `refs/heads/patch/<fork>`. They are
	// named by the caller because a patch ref belongs to the item's fence, not to the
	// worktree: the run edits a checkout under `.forks/lanes/<lane>/<fork>` and exports
	// its commit to the patch branch, which is a delivery and not an escape.
	PatchRefs []string
	// SiblingDirs are the lane directories of the lanes this bracket's runs do not own.
	// A worktree a sibling's run registers or removes inside its own lane directory is
	// the sibling's doing, and the sibling's bracket is the one that answers for it.
	SiblingDirs []string
	// LaneRoot is the loop's lane root. Under it the loop itself creates and removes
	// worktrees as part of its own designed work — the rolling tier's detached checkout,
	// the trunk-already-red replay's, and the review-comment desk lanes — and a run whose
	// bracket straddles one of those is not convicted of it (loopPlaces.worktree). Empty names no
	// lane root, and then nothing is excused by it.
	LaneRoot string
}

// StoreState is the store file's identity in one snapshot: what a file stat can see of
// it, which is the only thing this check can see of it without opening it.
type StoreState struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	MTime string `json:"mtime"`
}

// Worktree is one registered worktree in a snapshot: where it is, what its HEAD says, and
// — for the worktrees this snapshot reads the working tree of — every path `git status`
// reports, so a delta names a path instead of a subtree.
type Worktree struct {
	Path   string        `json:"path"`
	Head   string        `json:"head"`
	Branch string        `json:"branch,omitempty"`
	Status []StatusEntry `json:"status,omitempty"`
	// Prunable is git's own mark on a registration whose directory is already gone. Its
	// pruning during a bracket is not a run's doing: the directory was gone before the
	// bracket opened.
	Prunable bool `json:"prunable,omitempty"`
}

// StatusEntry is one `git status --porcelain` entry: the two-letter code, the path, and
// the source path of a rename or copy.
type StatusEntry struct {
	Code string `json:"code"`
	Path string `json:"path"`
	From string `json:"from,omitempty"`
}

// Boundary is one snapshot of the span a run must stay inside. It is a value a caller
// writes to a file before a run and reads back after it, so it is plain data with no
// handles and no absolute-on-this-machine assumptions beyond the paths it names.
type Boundary struct {
	Repo string `json:"repo"`
	// Targets are the worktrees this bracket's runs own, in the spelling `git worktree
	// list` uses. Their own working trees are not read, and their own checked-out branches
	// are the refs Compare excuses.
	Targets   []string `json:"targets"`
	Lanes     []string `json:"lanes,omitempty"`
	PatchRefs []string `json:"patch_refs,omitempty"`
	// SiblingDirs are the other lanes' directories. CompareRun does not convict this
	// bracket's run of a worktree that appeared or went away inside one of them.
	SiblingDirs []string `json:"sibling_dirs,omitempty"`
	// LaneRoot is the loop's lane root, canonical, and LaneScope is the component its lane
	// branches carry (LaneBranch). Together they name the places only the loop writes: see
	// loopPlaces.
	LaneRoot  string            `json:"lane_root,omitempty"`
	LaneScope string            `json:"lane_scope,omitempty"`
	Store     *StoreState       `json:"store,omitempty"`
	Refs      map[string]string `json:"refs"`
	Worktrees []Worktree        `json:"worktrees"`
}

// Violation is one difference between a before and an after snapshot: state outside the
// run's target that was not that way before the run. Every one of them is a stop for an
// unattended loop, and each names exactly what changed.
type Violation struct {
	// Ref, Old and New retain the complete identity of a ref observation. Empty
	// endpoints mean creation or deletion; Detail remains the human-readable delta.
	Ref string `json:"ref,omitempty"`
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
	// Kind is ref, worktree, path or store — the four things a snapshot records.
	Kind string `json:"kind"`
	// Where is the worktree path a path belongs to, and empty for a ref.
	Where string `json:"where,omitempty"`
	// Detail is the change itself: the ref and how it moved, the path and its status
	// codes, the worktree's fate.
	Detail string `json:"detail"`
}

// String is the violation as one line, so the stop message and a log agree.
func (v Violation) String() string {
	parts := make([]string, 0, 3)
	parts = append(parts, v.Kind)
	if v.Where != "" {
		parts = append(parts, v.Where)
	}
	if v.Detail != "" {
		parts = append(parts, v.Detail)
	}
	return strings.Join(parts, " ")
}

// TakeBoundary records one snapshot: the repository's refs, its registered worktrees with
// each one's HEAD, the working-tree status of the main checkout and of every named lane,
// and the store file's stat when the caller named one.
//
// It reads and never writes, and it is four git commands plus one per worktree it reads —
// no tree hash, no index scan of the whole repository. A snapshot it cannot take is an
// error, never an empty snapshot: an unattended loop that cannot see the boundary must
// stop, and an empty snapshot compared against the next one would report a clean pass.
func TakeBoundary(ctx context.Context, spec BoundarySpec) (*Boundary, error) {
	if spec.Repo == "" || len(spec.Targets) == 0 {
		return nil, Usagef("a boundary snapshot needs a repo and at least one target worktree: the main checkout and the worktrees this bracket's runs own")
	}
	repo, err := canonicalDir(spec.Repo)
	if err != nil {
		return nil, err
	}
	worktrees, err := listWorktrees(ctx, repo)
	if err != nil {
		return nil, err
	}
	targets := make([]string, 0, len(spec.Targets))
	for _, target := range spec.Targets {
		path, err := canonicalDir(target)
		if err != nil {
			return nil, err
		}
		if !hasWorktree(worktrees, path) {
			return nil, Usagef("the target %s is not a worktree of %s: a run's boundary is a worktree of the repository it was given",
				target, repo)
		}
		if isTarget(targets, path) {
			return nil, Usagef("the target %s is named twice: one bracket owns one worktree per run, and the same worktree twice is two runs in one tree",
				target)
		}
		targets = append(targets, path)
	}
	lanes := make([]string, 0, len(spec.Lanes))
	for _, lane := range spec.Lanes {
		path, err := canonicalDir(lane)
		if err != nil {
			return nil, err
		}
		if !hasWorktree(worktrees, path) {
			return nil, Usagef("the lane %s is not a worktree of %s: a lane that is not there cannot be watched", lane, repo)
		}
		if isTarget(targets, path) {
			return nil, Usagef("the lane %s is a target: a run's own worktree is not a lane to watch", lane)
		}
		lanes = append(lanes, path)
	}
	sort.Strings(targets)
	sort.Strings(lanes)
	patchRefs := make([]string, 0, len(spec.PatchRefs))
	for _, ref := range spec.PatchRefs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		patchRefs = append(patchRefs, ref)
	}
	sort.Strings(patchRefs)
	patchRefs = dedupe(patchRefs)
	// A sibling directory that is not there yet has nothing inside it to excuse, so it is
	// dropped rather than refused: a lane is created by its first staging.
	siblingDirs := make([]string, 0, len(spec.SiblingDirs))
	for _, dir := range spec.SiblingDirs {
		path, err := canonicalDir(dir)
		if err != nil {
			continue
		}
		siblingDirs = append(siblingDirs, path)
	}
	sort.Strings(siblingDirs)
	siblingDirs = dedupe(siblingDirs)
	// A lane root that is not there yet holds no worktree to excuse, so it names nothing
	// rather than refusing the snapshot, exactly as a sibling directory does.
	laneRoot, laneScope := "", ""
	if spec.LaneRoot != "" {
		if path, err := canonicalDir(spec.LaneRoot); err == nil {
			laneRoot, laneScope = path, laneRootScope(spec.LaneRoot)
		}
	}

	refs, err := listRefs(ctx, repo)
	if err != nil {
		return nil, err
	}
	// The main checkout is the worktree git lists first. Its HEAD and branch are recorded
	// whatever the caller names, because `git worktree list` reports them for every
	// worktree; its content is read unless a caller names it as a target. Main content and
	// HEAD movement are not run convictions; a checked-out branch change still is.
	main := worktrees[0].Path
	for _, path := range append([]string{main}, lanes...) {
		if isTarget(targets, path) {
			continue
		}
		status, err := readStatus(ctx, path)
		if err != nil {
			return nil, err
		}
		for i := range worktrees {
			if worktrees[i].Path == path {
				worktrees[i].Status = status
			}
		}
	}

	snapshot := &Boundary{
		Repo:        repo,
		Targets:     targets,
		Lanes:       lanes,
		PatchRefs:   patchRefs,
		SiblingDirs: siblingDirs,
		LaneRoot:    laneRoot,
		LaneScope:   laneScope,
		Refs:        refs,
		Worktrees:   worktrees,
	}
	if spec.Store != "" {
		state, err := statStore(spec.Store)
		if err != nil {
			return nil, err
		}
		snapshot.Store = state
	}
	return snapshot, nil
}

// Compare answers the differences between a before and an after snapshot: state outside the
// targets that the bracket's runs changed. An empty answer is a clean boundary.
//
// A target worktree contributes no path deltas and no HEAD or branch delta.
// Its checked-out branch and explicitly assigned patch refs are deliveries, not
// escapes. Ordinary refs outside tillr's namespaces are ignored: a snapshot pair
// cannot attribute another Git user's fetch, tag, branch or trunk commit to a run.
// Unassigned refs inside tillr's namespaces remain convictions. The loop observes
// trunk ancestry separately and holds landings, never runs, on a rewrite.
func Compare(before, after *Boundary) ([]Violation, error) {
	if err := sameSpan(before, after); err != nil {
		return nil, err
	}
	violations := make([]Violation, 0)
	violations = append(violations, compareRefs(before, after)...)
	violations = append(violations, compareWorktrees(before, after)...)
	violations = append(violations, compareStatus(before, after)...)
	if before.Store != nil {
		if before.Store.Size != after.Store.Size || before.Store.MTime != after.Store.MTime {
			violations = append(violations, Violation{
				Where:  before.Store.Path,
				Kind:   "store",
				Detail: fmt.Sprintf("size %d -> %d, mtime %s -> %s", before.Store.Size, after.Store.Size, before.Store.MTime, after.Store.MTime),
			})
		}
	}
	return violations, nil
}

// sameSpan answers whether two snapshots cover one span: the same repository, targets,
// lanes, patch refs and store. A pair that differs is a caller's mistake, never a clean
// boundary.
func sameSpan(before, after *Boundary) error {
	if before == nil || after == nil {
		return Usagef("comparing a boundary needs two snapshots")
	}
	if before.Repo != after.Repo || strings.Join(before.Targets, "\x00") != strings.Join(after.Targets, "\x00") {
		return Usagef("the snapshots cover different spans: %s -> %s before, %s -> %s after",
			before.Repo, strings.Join(before.Targets, ", "), after.Repo, strings.Join(after.Targets, ", "))
	}
	if strings.Join(before.Lanes, "\x00") != strings.Join(after.Lanes, "\x00") {
		return Usagef("the snapshots name different lanes: %v before, %v after", before.Lanes, after.Lanes)
	}
	if strings.Join(before.PatchRefs, "\x00") != strings.Join(after.PatchRefs, "\x00") {
		return Usagef("the snapshots excuse different patch refs: %v before, %v after", before.PatchRefs, after.PatchRefs)
	}
	if strings.Join(before.SiblingDirs, "\x00") != strings.Join(after.SiblingDirs, "\x00") {
		return Usagef("the snapshots name different sibling lane directories: %v before, %v after", before.SiblingDirs, after.SiblingDirs)
	}
	// A snapshot recorded before the lane root was named (an older loop's dispatch bracket,
	// which a resume or a settle closes) names none; the other side's is then the bracket's.
	// Two different lane roots are not one bracket.
	if before.LaneRoot != "" && after.LaneRoot != "" && (before.LaneRoot != after.LaneRoot || before.LaneScope != after.LaneScope) {
		return Usagef("the snapshots name different lane roots: %q before, %q after", before.LaneRoot, after.LaneRoot)
	}
	if (before.Store == nil) != (after.Store == nil) || (before.Store != nil && before.Store.Path != after.Store.Path) {
		return Usagef("the snapshots name different stores: the store stat of one bracket must be the stat of one file")
	}
	return nil
}

// CompareRun answers the differences one run's bracket convicts: CompareRunObserved's
// violations alone.
func CompareRun(before, after *Boundary) ([]Violation, error) {
	violations, _, err := CompareRunObserved(before, after)
	return violations, err
}

// CompareRunObserved answers what one run's bracket convicts, and, apart, what it only
// observed: movements in the repository outside the run's lane that two snapshots can place
// in the run's window but cannot attribute to the run.
//
// A run is convicted on attribution, never on correlation. The owner ruled on 2026-11-17:
// "why do we convict anyone for removing worktrees? that sounds stupid? we should be free
// to act with normal tools without breaking the drain, goes along with ad hoc commits". The
// owner and other agents use ordinary Git here while the drain runs — commit, pull, branch,
// tag, stash, fetch, gc, `git worktree add` and `remove`, a fork export moving
// refs/heads/patch/<fork>, an operator's hand-written refs/tillr/held/ pin — and every one
// of those moves a ref or a worktree registration exactly as an escaping worker would. A
// snapshot pair cannot tell them apart, so none of them convicts: each is observed, and the
// loop records it on the bracket's boundary.check as information.
//
// What still convicts is what only the drain's own lanes hold, where ordinary use of the
// repository does not reach: a path whose status changed in a watched, idle sibling lane
// worktree (another item's candidate, under the lane root), and the run's own lane worktree
// going away. The fence check (fenceViolation) still convicts a candidate whose committed
// change leaves its allowed paths or that leaves uncommitted work behind, and the gate
// still judges the candidate, so an escape that would reach the trunk is caught by the
// checks that judge what lands. What is given up is the stop of a worker that writes only
// outside the repository's lanes — a rogue ref, a stray worktree — which no longer holds
// its item; the observation names it for an operator.
func CompareRunObserved(before, after *Boundary) (violations, observed []Violation, err error) {
	if err := sameSpan(before, after); err != nil {
		return nil, nil, err
	}
	violations, observed = make([]Violation, 0), make([]Violation, 0)
	observed = append(observed, compareRunRefs(before, after)...)
	lanes, elsewhere := compareRunWorktrees(before, after)
	violations = append(violations, lanes...)
	observed = append(observed, elsewhere...)
	violations = append(violations, compareRunStatus(before, after)...)
	return violations, observed, nil
}

// tillrRef names the namespaces the loop owns. Ordinary Git activity outside
// them cannot be attributed to a run from two snapshots.
func tillrRef(ref string) bool {
	return strings.HasPrefix(ref, "refs/heads/tillr/") || strings.HasPrefix(ref, "refs/heads/patch/") || strings.HasPrefix(ref, "refs/tillr/")
}

func trunkHistoryRef(ref string) bool {
	return ref == observedTrunkRef || ref == lostTrunkRef || strings.HasPrefix(ref, acceptedTrunkPrefix)
}

// landerRef names the refs tillr-land writes for every landing: the run pin at the head
// (refs/tillr/runs/<run_id>) and the landed marker at the squash commit
// (refs/tillr/landed/<run_id>), which is how a retry finds a landing whose receipt was
// lost. A sibling lane's landing writes both while this bracket's run is in flight.
func landerRef(ref string) bool {
	// The heavy runner owns the proof ref, just as the lander owns its receipt refs.
	return ref == GreenRef || strings.HasPrefix(ref, "refs/tillr/runs/") || strings.HasPrefix(ref, "refs/tillr/landed/") || strings.HasPrefix(ref, preservedPrefix) || strings.HasPrefix(ref, ownerRecoveryPrefix)
}

// ownerRecoveryPrefix is the lander's recovery copy of the owner's uncommitted content on the
// paths a landing replaces. It is its own prefix, apart from refs/stash, which is the owner's.
const ownerRecoveryPrefix = "refs/tillr/owner-recovery/"

// runRefExcused answers whether a ref may move without convicting this run.
// Ordinary refs belong to other Git users; registered sibling branches and the
// lander's run pins and landed markers are moved by the loop. An unassigned ref
// inside tillr's namespaces is not.
func runRefExcused(ref string, before *Boundary) bool {
	if trunkHistoryRef(ref) || !tillrRef(ref) || landerRef(ref) {
		return true
	}
	for _, wt := range before.Worktrees {
		if !isTarget(before.Targets, wt.Path) && wt.Branch == ref {
			return true
		}
	}
	for _, patch := range before.PatchRefs {
		if ref == patch {
			return true
		}
	}
	return false
}

// compareRunRefs answers a delta in an unassigned ref inside tillr's namespaces, which a
// run's bracket observes and does not convict (CompareRunObserved). Ordinary refs are
// outside even the observation: they move all day.
func compareRunRefs(before, after *Boundary) []Violation {
	excused := excusedRefs(before, after)
	landed := landedDuring(before, after)
	places := loopPlacesOf(before, after)
	return refViolations(before, after, func(name string) bool {
		return excused[name] || runRefExcused(name, before) || places.deskBranch(name) || landedPatchMove(name, after, landed)
	})
}

// loopPlaces is the lane root a bracket names and its lane-branch scope: the places only the
// loop itself writes (worktree, deskBranch).
type loopPlaces struct {
	root, scope string
	targets     []string
}

// loopPlacesOf answers the bracket's lane root: either side's, since sameSpan admits a
// side that names none only beside one that does.
func loopPlacesOf(before, after *Boundary) loopPlaces {
	places := loopPlaces{root: before.LaneRoot, scope: before.LaneScope, targets: before.Targets}
	if places.root == "" {
		places.root, places.scope = after.LaneRoot, after.LaneScope
	}
	return places
}

// landedDuring answers the commits the lander marked during this bracket: every
// refs/tillr/landed/<run> that appeared or moved between the two snapshots. The lander
// writes that marker at the squash commit before it moves the ref it lands onto, so the
// marker is its own record of exactly which commit a landing published.
func landedDuring(before, after *Boundary) map[string]bool {
	landed := map[string]bool{}
	for ref, sha := range after.Refs {
		if strings.HasPrefix(ref, "refs/tillr/landed/") && before.Refs[ref] != sha {
			landed[sha] = true
		}
	}
	return landed
}

// landedPatchMove answers whether a patch branch's move is a sibling's fork-series landing:
// refs/heads/patch/<fork> now points at a commit the lander marked during this bracket. A
// patch branch moved to anything else is not excused here — a run's own patch branch is
// excused by PatchRefs, and any other move is an escape.
func landedPatchMove(ref string, after *Boundary, landed map[string]bool) bool {
	if !strings.HasPrefix(ref, "refs/heads/patch/") {
		return false
	}
	sha, ok := after.Refs[ref]
	return ok && landed[sha]
}

// compareRunWorktrees answers, apart, the run's own lane worktree going away (a violation)
// and every other worktree that appeared or went away or a main checkout branch switch
// (observed only: ordinary Git use by anyone does each of these). Main HEAD movement can
// be a coordinator's landing even while owner content remains dirty; dirt cannot turn that
// movement into guilt.
func compareRunWorktrees(before, after *Boundary) (lanes, observed []Violation) {
	violations := make([]Violation, 0)
	lanes = make([]Violation, 0)
	main := mainWorktreePath(after)
	places := loopPlacesOf(before, after)
	for _, wt := range after.Worktrees {
		was := worktreeFor(before.Worktrees, wt.Path)
		if was == nil {
			if insideSiblingDir(after.SiblingDirs, wt.Path) {
				// A sibling's run made it inside the sibling's own lane directory. The
				// sibling's bracket convicts it if it is still there when that run ends.
				continue
			}
			if places.worktree(wt) {
				// The loop made it, at a place and in a shape only the loop uses.
				continue
			}
			violations = append(violations, Violation{Kind: "worktree", Where: wt.Path, Detail: "appeared"})
			continue
		}
		if isTarget(before.Targets, wt.Path) {
			continue
		}
		if wt.Path != main {
			// A sibling lane's own HEAD and branch move when its own run commits or the
			// next stage resets it, which is the lane's deliverable and not a write into
			// it. A write into a sibling lane is a status change, which compareRunStatus
			// convicts by path.
			continue
		}
		if was.Branch != wt.Branch {
			violations = append(violations, Violation{Kind: "worktree", Where: wt.Path, Detail: fmt.Sprintf("branch %s -> %s", spelledRef(was.Branch), spelledRef(wt.Branch))})
			continue
		}
	}
	for _, was := range before.Worktrees {
		if worktreeFor(after.Worktrees, was.Path) == nil {
			if insideSiblingDir(after.SiblingDirs, was.Path) || places.worktree(was) || was.Prunable {
				continue
			}
			if isTarget(before.Targets, was.Path) {
				// The run's own lane worktree is the drain's, under the lane root.
				lanes = append(lanes, Violation{Kind: "worktree", Where: was.Path, Detail: "went away"})
				continue
			}
			violations = append(violations, Violation{Kind: "worktree", Where: was.Path, Detail: "went away"})
		}
	}
	return lanes, violations
}

// worktree answers whether a worktree is one the loop itself creates and removes as part
// of its own designed work, which no run's bracket answers for. Each is a reserved place
// under the lane root AND a reserved shape, and nothing else matches:
//
//   - <lane root>/.rolling/worktree, detached: the rolling tier's checkout of the tip
//     (ProcessSteps.rollingWorktree);
//   - <lane root>/.order-record/run-order-record-<seq>-<claim>/worktree, detached:
//     the loop's record-only maintenance candidate;
//   - <lane root>/.replay/<sha>, detached: the trunk-already-red replay's checkout of one
//     revision (ProcessSteps.replayWorktree);
//   - <lane root>/desk-<doc>/worktree, on that desk's own lane branch: a review-comment
//     desk lane, which staging creates and its reset removes (commentDesk, resetLane).
//
// The rule is a reserved location rather than a recorded fact because the location is
// already exact: the loop writes nowhere else under these names, and a run is confined to
// its own lane worktree, where its fence and the bracket's other comparisons still read
// every write. What the excuse cannot hide is bounded by the shape: a worker that registers
// a detached checkout at one of these exact paths gains a checkout of objects with no ref,
// at a path the loop's next rolling run or replay removes before it adds its own, and a
// branch at a desk path is excused only when it is that desk's lane branch.
func (p loopPlaces) worktree(wt Worktree) bool {
	if p.root == "" || isTarget(p.targets, wt.Path) {
		// A run's own worktree is never excused: its going away is the run's to answer for.
		return false
	}
	root := strings.TrimSuffix(p.root, "/")
	dir, base := filepath.Dir(wt.Path), filepath.Base(wt.Path)
	switch {
	case wt.Path == filepath.Join(root, ".rolling", "worktree"):
		return wt.Branch == ""
	case base == WorktreeDir && filepath.Dir(dir) == filepath.Join(root, ".order-record"):
		parts := strings.Split(strings.TrimPrefix(filepath.Base(dir), "run-order-record-"), "-")
		if !strings.HasPrefix(filepath.Base(dir), "run-order-record-") || len(parts) != 2 {
			return false
		}
		for _, part := range parts {
			if part == "" || strings.Trim(part, "0123456789") != "" {
				return false
			}
		}
		return wt.Branch == ""
	case dir == filepath.Join(root, ".replay"):
		return wt.Branch == ""
	case base == WorktreeDir && filepath.Dir(dir) == root && strings.HasPrefix(filepath.Base(dir), "desk-"):
		return p.scope != "" && wt.Branch == "refs/heads/tillr/"+p.scope+"/"+filepath.Base(dir)
	}
	return false
}

// deskBranch answers whether ref is the lane branch of a review-comment desk lane in
// this bracket's lane root. The desk's first staging creates it and every later staging
// re-points it; neither is registered as a sibling lane, so without this a desk staged
// during a run convicted that run of a ref it never touched.
func (p loopPlaces) deskBranch(ref string) bool {
	if p.scope == "" {
		return false
	}
	lane, ok := strings.CutPrefix(ref, "refs/heads/tillr/"+p.scope+"/")
	return ok && strings.HasPrefix(lane, "desk-") && !strings.Contains(lane, "/")
}

// insideSiblingDir answers whether a worktree path lies strictly inside one of the sibling
// lane directories. The separator is part of the prefix, so `lane-5` does not claim
// `lane-50`.
func insideSiblingDir(dirs []string, path string) bool {
	for _, dir := range dirs {
		if strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/") {
			return true
		}
	}
	return false
}

// compareRunStatus answers writes into sibling lane worktrees. Uncommitted
// main-checkout content is the owner's, regardless of when it appeared.
func compareRunStatus(before, after *Boundary) []Violation {
	violations := make([]Violation, 0)
	main := mainWorktreePath(after)
	for _, now := range after.Worktrees {
		was := worktreeFor(before.Worktrees, now.Path)
		if was == nil || isTarget(before.Targets, now.Path) || now.Path == main {
			continue
		}
		wasByPath := make(map[string]StatusEntry, len(was.Status))
		for _, entry := range was.Status {
			wasByPath[entry.Path] = entry
		}
		for _, entry := range now.Status {
			previous, had := wasByPath[entry.Path]
			delete(wasByPath, entry.Path)
			if had && previous.Code == entry.Code {
				continue
			}
			if had {
				violations = append(violations, Violation{Kind: "path", Where: now.Path, Detail: fmt.Sprintf("%s changed status %q -> %q", entry.Path, previous.Code, entry.Code)})
				continue
			}
			violations = append(violations, Violation{Kind: "path", Where: now.Path, Detail: fmt.Sprintf("%s appeared with status %q", entry.Path, entry.Code)})
		}
		left := make([]string, 0, len(wasByPath))
		for path := range wasByPath {
			left = append(left, path)
		}
		sort.Strings(left)
		for _, path := range left {
			violations = append(violations, Violation{Kind: "path", Where: now.Path, Detail: fmt.Sprintf("%s is clean now, and was %q before the run", path, wasByPath[path].Code)})
		}
	}
	return violations
}

// mainWorktreePath answers the main checkout's path: the first worktree `git worktree list`
// reports.
func mainWorktreePath(b *Boundary) string {
	if b == nil || len(b.Worktrees) == 0 {
		return ""
	}
	return b.Worktrees[0].Path
}

// compareRefs answers every ref that moved, appeared or went away, except the refs the
// targets' own checkouts name on both sides (excusedRefs).
func compareRefs(before, after *Boundary) []Violation {
	excused := excusedRefs(before, after)
	for _, ref := range before.PatchRefs {
		excused[ref] = true
	}
	return refViolations(before, after, func(name string) bool { return !tillrRef(name) || trunkHistoryRef(name) || excused[name] })
}

// refViolations answers every ref that moved, appeared or went away, except the ones the
// caller excuses. Both comparisons share it, so the two cannot disagree about how a moved
// ref reads.
func refViolations(before, after *Boundary, excused func(string) bool) []Violation {
	names := make([]string, 0, len(before.Refs)+len(after.Refs))
	seen := make(map[string]bool, len(before.Refs)+len(after.Refs))
	for _, refs := range []map[string]string{before.Refs, after.Refs} {
		for name := range refs {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	violations := make([]Violation, 0)
	for _, name := range names {
		if excused(name) {
			continue
		}
		was, hadBefore := before.Refs[name]
		now, hasAfter := after.Refs[name]
		switch {
		case !hadBefore:
			violations = append(violations, Violation{Kind: "ref", Ref: name, New: now, Detail: fmt.Sprintf("%s appeared at %s", name, now)})
		case !hasAfter:
			violations = append(violations, Violation{Kind: "ref", Ref: name, Old: was, Detail: fmt.Sprintf("%s went away from %s", name, was)})
		case was != now:
			violations = append(violations, refMoveViolation(name, was, now))
		}
	}
	return violations
}

// excusedRefs answers the refs a bracket may move: for each target worktree, the branch it
// had checked out in both snapshots. A target that changed branch, or is detached on either
// side, excuses nothing.
func excusedRefs(before, after *Boundary) map[string]bool {
	excused := make(map[string]bool, len(before.Targets))
	for _, path := range before.Targets {
		was, now := worktreeFor(before.Worktrees, path), worktreeFor(after.Worktrees, path)
		if was == nil || now == nil || was.Branch == "" {
			continue
		}
		if was.Branch == now.Branch {
			excused[was.Branch] = true
		}
	}
	return excused
}

// isTarget answers whether a path is one of the worktrees this bracket's runs own.
func isTarget(targets []string, path string) bool {
	for _, target := range targets {
		if target == path {
			return true
		}
	}
	return false
}

// compareWorktrees answers lifecycle changes and non-target branch changes. Only
// HEAD movement on a tillr-owned branch can convict; ordinary commits elsewhere
// belong to other Git users.
func compareWorktrees(before, after *Boundary) []Violation {
	violations := make([]Violation, 0)
	for _, wt := range after.Worktrees {
		was := worktreeFor(before.Worktrees, wt.Path)
		if was == nil {
			violations = append(violations, Violation{Kind: "worktree", Where: wt.Path, Detail: "appeared"})
			continue
		}
		if isTarget(before.Targets, wt.Path) {
			continue
		}
		switch {
		case was.Branch != wt.Branch:
			violations = append(violations, Violation{Kind: "worktree", Where: wt.Path, Detail: fmt.Sprintf("branch %s -> %s", spelledRef(was.Branch), spelledRef(wt.Branch))})
		case was.Head != wt.Head && tillrRef(wt.Branch):
			violations = append(violations, headMoveViolation(wt.Path, was.Head, wt.Head))
		}
	}
	for _, was := range before.Worktrees {
		if worktreeFor(after.Worktrees, was.Path) == nil {
			violations = append(violations, Violation{Kind: "worktree", Where: was.Path, Detail: "went away"})
		}
	}
	return violations
}

// compareStatus answers every path whose status differs in a worktree this snapshot read,
// named by worktree and by path so the operator knows where to look. A worktree the
// snapshots did not read contributes no path deltas, which is what makes the lane set the
// caller's decision rather than a discovery.
func compareStatus(before, after *Boundary) []Violation {
	violations := make([]Violation, 0)
	for _, now := range after.Worktrees {
		was := worktreeFor(before.Worktrees, now.Path)
		if was == nil || now.Path == mainWorktreePath(after) || len(was.Status) == 0 && len(now.Status) == 0 {
			continue
		}
		wasByPath := make(map[string]StatusEntry, len(was.Status))
		for _, entry := range was.Status {
			wasByPath[entry.Path] = entry
		}
		for _, entry := range now.Status {
			previous, had := wasByPath[entry.Path]
			if had && previous.Code == entry.Code {
				delete(wasByPath, entry.Path)
				continue
			}
			delete(wasByPath, entry.Path)
			if had {
				violations = append(violations, Violation{Kind: "path", Where: now.Path, Detail: fmt.Sprintf("%s changed status %q -> %q", entry.Path, previous.Code, entry.Code)})
				continue
			}
			violations = append(violations, Violation{Kind: "path", Where: now.Path, Detail: fmt.Sprintf("%s appeared with status %q", entry.Path, entry.Code)})
		}
		left := make([]string, 0, len(wasByPath))
		for path := range wasByPath {
			left = append(left, path)
		}
		sort.Strings(left)
		for _, path := range left {
			violations = append(violations, Violation{Kind: "path", Where: now.Path, Detail: fmt.Sprintf("%s is clean now, and was %q before the run", path, wasByPath[path].Code)})
		}
	}
	return violations
}

// listWorktrees reads `git worktree list --porcelain`, which is one command for the set of
// worktrees, each one's HEAD sha and each one's checked-out branch. It is the whole
// worktree half of a snapshot: a worktree appearing or disappearing, and a HEAD moving
// anywhere, come from here with no per-worktree command.
func listWorktrees(ctx context.Context, repo string) ([]Worktree, error) {
	var out string
	err := withWorktreeRegistryLock(ctx, repo, func() error {
		var err error
		out, err = boundaryGit(ctx, repo, "worktree", "list", "--porcelain")
		return err
	})
	if err != nil {
		return nil, err
	}
	worktrees := make([]Worktree, 0, 8)
	var current *Worktree
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			if current != nil {
				worktrees = append(worktrees, *current)
				current = nil
			}
			continue
		}
		key, rest, _ := strings.Cut(line, " ")
		if key == "worktree" {
			if current != nil {
				worktrees = append(worktrees, *current)
			}
			current = &Worktree{Path: rest}
			continue
		}
		if current == nil {
			return nil, Internalf("git worktree list answered %q before any worktree", line)
		}
		switch key {
		case "HEAD":
			current.Head = rest
		case "branch":
			current.Branch = rest
		case "prunable":
			current.Prunable = true
		case "detached", "bare", "locked":
			// Nothing in the boundary turns on these, and a bare or locked worktree is
			// still a worktree this snapshot names.
		}
	}
	if current != nil {
		worktrees = append(worktrees, *current)
	}
	if len(worktrees) == 0 {
		return nil, Externalf("git worktree list answered no worktrees for %s: a boundary snapshot cannot be taken", repo)
	}
	return worktrees, nil
}

// listRefs reads every ref and its object name in one command. A tag, a branch, a stash
// and a remote-tracking ref are all the same question: did something move a pointer.
func listRefs(ctx context.Context, repo string) (map[string]string, error) {
	out, err := boundaryGit(ctx, repo, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		return nil, err
	}
	refs := make(map[string]string, 64)
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		name, sha, ok := strings.Cut(line, " ")
		if !ok {
			return nil, Internalf("git for-each-ref answered %q without an object name", line)
		}
		refs[name] = sha
	}
	return refs, nil
}

// readStatus reads one worktree's `git status --porcelain -z`, which is the content half
// of a snapshot: which paths differ from HEAD and which are untracked, in one command that
// touches only the index and the working tree.
//
// The ignored set is not read. Most ignored paths in a worktree a run may write are the
// build caches and tool scratch the boundary admits, so reading them would produce stops
// for work the design allows — and `--ignored` on a checkout this size is the expensive
// scan this check exists to avoid. What that leaves unseen is stated where it belongs:
// writes into an ignored path, and a new file inside an untracked directory (git reports
// the directory), are invisible here.
func readStatus(ctx context.Context, path string) ([]StatusEntry, error) {
	out, err := boundaryGit(ctx, path, "status", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	entries := make([]StatusEntry, 0, 8)
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if len(field) < 4 {
			continue
		}
		entry := StatusEntry{Code: field[:2], Path: field[3:]}
		if entry.Code[0] == 'R' || entry.Code[0] == 'C' {
			if i+1 < len(fields) {
				entry.From = fields[i+1]
				i++
			}
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Path < entries[b].Path })
	return entries, nil
}

// statStore answers the store file's stat. A store the caller named and the loop cannot
// stat is a snapshot it cannot take, not a store that did not change.
func statStore(path string) (*StoreState, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, Externalf("statting the store %s: %v: a boundary snapshot cannot be taken", path, err)
	}
	return &StoreState{Path: path, Size: info.Size(), MTime: info.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z07:00")}, nil
}

// boundaryGit runs one read-only git command in dir and answers its stdout.
//
// The repository variables are stripped from the child's environment rather than
// overridden: a snapshot describes the repository --repo names, and an inherited GIT_DIR
// would quietly make every command answer about a different one — the exact silent wrong
// answer an unattended loop cannot afford.
func boundaryGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = boundaryGitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", Externalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// boundaryGitEnv is this process's environment without the repository variables.
func boundaryGitEnv() []string {
	kept := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// canonicalDir answers a path as the one spelling two snapshots can compare: absolute and
// with symbolic links resolved, which is the spelling `git worktree list` reports.
func canonicalDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", Usagef("resolving %s: %v", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", Externalf("resolving %s: %v: a boundary snapshot cannot be taken", path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", Externalf("statting %s: %v: a boundary snapshot cannot be taken", path, err)
	}
	if !info.IsDir() {
		return "", Usagef("%s is not a directory, and a worktree is one", path)
	}
	return resolved, nil
}

// hasWorktree answers whether path is one of the registered worktrees.
func hasWorktree(worktrees []Worktree, path string) bool { return worktreeFor(worktrees, path) != nil }

// worktreeFor answers the worktree at path, and nil when there is none.
func worktreeFor(worktrees []Worktree, path string) *Worktree {
	for i := range worktrees {
		if worktrees[i].Path == path {
			return &worktrees[i]
		}
	}
	return nil
}

// shortSHA is a sha as a diagnostic reads best, and the whole sha when it is not one.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// refMoveViolation is the violation one ref move produces: the ref, and the two object
// names it moved between. The landing excuse builds the same value, so the comparison and
// the excuse cannot disagree about what a trunk move looks like.
func refMoveViolation(ref, was, now string) Violation {
	return Violation{Kind: "ref", Ref: ref, Old: was, New: now, Detail: fmt.Sprintf("%s moved %s -> %s", ref, was, now)}
}

// headMoveViolation is the violation one worktree HEAD move produces. The shas are spelled
// short, which is the only place a reader sees them here.
func headMoveViolation(path, was, now string) Violation {
	return Violation{Kind: "worktree", Where: path, Detail: fmt.Sprintf("HEAD moved %s -> %s", shortSHA(was), shortSHA(now))}
}

// spelledRef names a ref in a violation, and a detached HEAD as what it is.
func spelledRef(ref string) string {
	if ref == "" {
		return "detached"
	}
	return ref
}
