package drain

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/preserved"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	workPreservedKind = preserved.Kind
	workPrunedKind    = preserved.PrunedKind
	preservedPrefix   = preserved.Prefix
)

type PreservedWork = preserved.PreservedWork
type PreservedFile = preserved.PreservedFile

var ReadPreservedWork = preserved.ReadPreservedWork

type workSnapshot struct {
	head, tree, indexTree string
	files, indexFiles     []PreservedFile
	summary, indexSummary string
	// unmerged are the paths the lane's index held unmerged, preserved from the working
	// copy; indexNote says why no staged snapshot was kept apart, when none was.
	unmerged  []string
	indexNote string
}

// preserveWork finishes all Git work before the short event write. The separate
// index leaves the real index and branch untouched. A second parent keeps staged
// bytes when they differ from the working copy. The commit's message retains the
// record for a crash between creating its ref and writing the settlement event.
func (l *Loop) preserveWork(ctx context.Context, lane, runID, reason string) error {
	return l.preserveWorkFromBase(ctx, lane, runID, "", reason)
}

// baseInCheckout answers base when the lane's checkout holds it as a commit, and the
// lane's own recorded base otherwise. A fork item's later workflow node was recorded with
// this repository's trunk commit as its node base (2026-11-20), which no git in the fork
// checkout can read.
func (l *Loop) baseInCheckout(ctx context.Context, lane, base string) string {
	if base == "" {
		return base
	}
	if _, err := l.git(ctx, l.laneTarget(lane), "cat-file", "-e", base+"^{commit}"); err != nil {
		return l.laneBase(lane)
	}
	return base
}

func (l *Loop) preserveWorkFromBase(ctx context.Context, lane, runID, base, reason string) error {
	if runID == "" {
		return nil
	}
	if err := checkRunID(runID); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pinTimeout)
	defer cancel()
	// Retention is authoritative even when a crash left settlement pending. Do not
	// recreate a dropped ref from untracked files left behind by a successful reset.
	old, terminal, err := l.preservationDisposition(ctx, runID)
	if err != nil {
		return err
	}
	if terminal || (old != nil && old.Pruned) {
		return nil
	}
	target := l.laneTarget(lane)
	base = l.baseInCheckout(ctx, lane, base)
	ref := preservedPrefix + runID
	refs, err := l.git(ctx, target, "for-each-ref", "--format=%(refname)", ref)
	if err != nil {
		return err
	}
	exists := false
	for _, name := range strings.Split(refs, "\n") {
		if name == ref {
			exists = true
		}
	}
	var p PreservedWork
	if exists {
		body, err := l.git(ctx, target, "show", "-s", "--format=%B", ref)
		if err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			return fmt.Errorf("reading preserved work %s: %w", ref, err)
		}
		if p.RunID != runID || p.Ref != ref {
			return Externalf("preserved ref %s names a different run", ref)
		}
	} else if old != nil {
		return Externalf("preserved ref %s is missing without a retention record; retain lane %s", ref, lane)
	}
	// A lock a killed git left would refuse the lane's reset after this (index_lock.go).
	if err := l.clearStaleIndexLock(ctx, lane, runID); err != nil {
		return err
	}
	status, err := l.git(ctx, target, "--no-optional-locks", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		snapshot, err := l.snapshotWork(ctx, lane, runID)
		if err != nil {
			return err
		}
		if exists {
			// Only compare current dirty paths: clean paths may have changed when an
			// earlier reset advanced the lane to a newer trunk. Every dirty byte must
			// already be covered before a replay is allowed to reset it away.
			if err := l.snapshotCovered(ctx, target, ref, snapshot.tree, snapshot.files); err != nil {
				return err
			}
			indexRef := p.IndexRef
			if indexRef == "" {
				indexRef = ref
			}
			if err := l.snapshotCovered(ctx, target, indexRef, snapshot.indexTree, snapshot.indexFiles); err != nil {
				return err
			}
		} else {
			if base != "" && base != snapshot.head {
				fenceBody, err := os.ReadFile(filepath.Join(l.laneDirPath(lane), FenceFile))
				if err != nil && !os.IsNotExist(err) {
					return err
				}
				fence := fenceEntries(string(fenceBody))
				prefix := l.fencePrefix(ctx, target)
				stats, err := l.git(ctx, target, "diff", "--numstat", "-z", "--no-renames", base, snapshot.tree)
				if err != nil {
					return err
				}
				snapshot.files, snapshot.summary, err = snapshotSummary(stats, prefix, fence)
				if err != nil {
					return err
				}
			}
			if len(snapshot.files) == 0 && len(snapshot.indexFiles) == 0 {
				return nil
			}
			p = PreservedWork{RunID: runID, LaneID: lane, Ref: ref, Repository: target, Reason: reason, Files: snapshot.files, Summary: snapshot.summary, Unmerged: snapshot.unmerged}
			p.ItemID, err = l.preservationItem(ctx, runID)
			if err != nil {
				return err
			}
			if same, ok := l.sharesRepository(ctx, target); ok && same {
				p.Repository = l.cfg.Root
			}
			args := []string{"commit-tree", snapshot.tree, "-p", snapshot.head}
			if snapshot.indexTree != snapshot.head && snapshot.indexTree != snapshot.tree {
				staged, err := l.git(ctx, target, "commit-tree", snapshot.indexTree, "-p", snapshot.head, "-m", "Staged contents of settled run "+runID)
				if err != nil {
					return err
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
			if err := l.writePreservedRef(ctx, target, p, args...); err != nil {
				return err
			}
			exists = true
		}
	} else if base != "" && !exists {
		head, err := l.revParse(ctx, target, "HEAD")
		if err != nil {
			return err
		}
		if head != base {
			fenceBody, err := os.ReadFile(filepath.Join(l.laneDirPath(lane), FenceFile))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			fence := fenceEntries(string(fenceBody))
			prefix := l.fencePrefix(ctx, target)
			stats, err := l.git(ctx, target, "diff", "--numstat", "-z", "--no-renames", base, head)
			if err != nil {
				return err
			}
			files, summary, err := snapshotSummary(stats, prefix, fence)
			if err != nil {
				return err
			}
			p = PreservedWork{RunID: runID, LaneID: lane, Ref: ref, Repository: target, Reason: reason, Files: files, Summary: summary}
			p.ItemID, err = l.preservationItem(ctx, runID)
			if err != nil {
				return err
			}
			if same, ok := l.sharesRepository(ctx, target); ok && same {
				p.Repository = l.cfg.Root
			}
			headTree, err := l.revParse(ctx, target, "HEAD^{tree}")
			if err != nil {
				return err
			}
			if err := l.writePreservedRef(ctx, target, p, "commit-tree", headTree, "-p", head); err != nil {
				return err
			}
			exists = true
		}
	}
	if !exists || old != nil {
		return nil
	}
	if err := appendJudgment(ctx, l.cfg.Store, workPreservedKind, "run", runID, p, l.cfg.Now().UTC()); err != nil {
		return err
	}
	l.reportf("loop: earlier work exists at %s: %s (%s)\n", p.Ref, p.Summary, p.Reason)
	return nil
}

func (l *Loop) preservationItem(ctx context.Context, runID string) (string, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", err
	}
	defer s.Close()
	var itemID string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		err := r.QueryRow(`SELECT item_id FROM runs WHERE run_id = ?`, runID).Scan(&itemID)
		if err == sql.ErrNoRows {
			err = r.QueryRow(`SELECT item_id FROM dispatches WHERE run_id = ? ORDER BY dispatched_at DESC LIMIT 1`, runID).Scan(&itemID)
		}
		if err == sql.ErrNoRows {
			var hasNodeRuns bool
			if tableErr := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='node_runs')`).Scan(&hasNodeRuns); tableErr == nil && hasNodeRuns {
				err = r.QueryRow(`SELECT item_id FROM node_runs WHERE run_id = ? LIMIT 1`, runID).Scan(&itemID)
			}
		}
		return err
	})
	if err != nil {
		return "", fmt.Errorf("identifying dirty run %s: %w", runID, err)
	}
	return itemID, nil
}

// writePreservedRef keeps the full per-file record in the commit message, never in
// argv or diagnostics. A large record can exceed the OS's per-argument size limit.
func (l *Loop) writePreservedRef(ctx context.Context, target string, p PreservedWork, args ...string) error {
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

func (l *Loop) preservationDisposition(ctx context.Context, runID string) (*PreservedWork, bool, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, false, err
	}
	defer s.Close()
	var old *PreservedWork
	terminal := false
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		saved, err := ReadPreservedWork(r)
		if err != nil {
			return err
		}
		for i := range saved {
			if saved[i].RunID == runID {
				old = &saved[i]
			}
		}
		if old != nil {
			var state string
			err := r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, old.ItemID).Scan(&state)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			declared, readErr := definition.ForReader(r)
			if readErr != nil {
				return readErr
			}
			terminal = err == sql.ErrNoRows || finishedState(declared.Parsed, state)
		}
		return nil
	})
	return old, terminal, err
}

func (l *Loop) snapshotWork(ctx context.Context, lane, runID string) (workSnapshot, error) {
	target := l.laneTarget(lane)
	var snapshot workSnapshot
	head, err := l.git(ctx, target, "rev-parse", "HEAD")
	if err != nil {
		return snapshot, err
	}
	snapshot.head = strings.TrimSpace(head)
	dir := run.DurableDir(l.laneDirPath(lane), runID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return snapshot, err
	}
	index, err := os.CreateTemp(dir, "preserve-index-")
	if err != nil {
		return snapshot, err
	}
	name := index.Name()
	if err := index.Close(); err != nil {
		return snapshot, err
	}
	if err := os.Remove(name); err != nil {
		return snapshot, err
	}
	defer os.Remove(name)
	withIndex := func(index string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", target}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+index, "GIT_OPTIONAL_LOCKS=0")
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", Externalf("preserving %s: git %s: %v: %s", runID, strings.Join(args, " "), err, stderr.String())
		}
		return string(out), nil
	}
	git := func(args ...string) (string, error) { return withIndex(name, args...) }
	// The staged snapshot is written from a copy of the lane's index, never the index
	// itself: write-tree on the lane's index takes its lock (a stale index.lock refused it
	// on 2026-11-18) and refuses an unmerged entry (lane-14 the same day, a conflict the
	// worker resolved in the working copy and never added).
	if err := l.stagedSnapshot(ctx, target, dir, &snapshot, withIndex); err != nil {
		return snapshot, err
	}
	if _, err := git("read-tree", snapshot.head); err != nil {
		return snapshot, err
	}
	if _, err := git("add", "-A", "--", "."); err != nil {
		return snapshot, err
	}
	tree, err := git("write-tree")
	if err != nil {
		return snapshot, err
	}
	snapshot.tree = strings.TrimSpace(tree)
	fenceBody, err := os.ReadFile(filepath.Join(l.laneDirPath(lane), FenceFile))
	if err != nil && !os.IsNotExist(err) {
		return snapshot, err
	}
	fence := fenceEntries(string(fenceBody))
	prefix := l.fencePrefix(ctx, target)
	stats, err := git("diff", "--numstat", "-z", "--no-renames", snapshot.head, snapshot.tree)
	if err != nil {
		return snapshot, err
	}
	snapshot.files, snapshot.summary, err = snapshotSummary(stats, prefix, fence)
	if err != nil {
		return snapshot, err
	}
	if snapshot.indexTree == "" {
		snapshot.indexTree = snapshot.tree
	}
	stats, err = git("diff", "--numstat", "-z", "--no-renames", snapshot.head, snapshot.indexTree)
	if err != nil {
		return snapshot, err
	}
	snapshot.indexFiles, snapshot.indexSummary, err = snapshotSummary(stats, prefix, fence)
	return snapshot, err
}

// stagedSnapshot sets snapshot.indexTree from a temporary copy of the lane's index. An
// unmerged path is taken from the working copy into that copy (its resolution, when the
// worker resolved it there) and named in snapshot.unmerged. An index that cannot be read
// is no reason to lose the working copy's snapshot: the staged snapshot is then the
// working copy's, and snapshot.indexNote says why.
func (l *Loop) stagedSnapshot(ctx context.Context, target, dir string, snapshot *workSnapshot, withIndex func(string, ...string) (string, error)) error {
	snapshot.indexTree = snapshot.head
	out, err := l.git(ctx, target, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return err
	}
	body, err := os.ReadFile(strings.TrimSpace(out))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	copied, err := os.CreateTemp(dir, "preserve-staged-")
	if err != nil {
		return err
	}
	name := copied.Name()
	defer os.Remove(name)
	defer os.Remove(name + ".lock")
	if _, err := copied.Write(body); err != nil {
		copied.Close()
		return err
	}
	if err := copied.Close(); err != nil {
		return err
	}
	unreadable := func(err error) error {
		snapshot.indexTree = ""
		snapshot.indexNote = "the lane's index could not be read (" + heavyBrief(err.Error()) + "), so no staged snapshot was kept apart from the working copy's"
		return nil
	}
	listed, err := withIndex(name, "ls-files", "-u", "-z")
	if err != nil {
		return unreadable(err)
	}
	seen := map[string]bool{}
	for _, entry := range strings.Split(listed, "\x00") {
		if _, path, ok := strings.Cut(entry, "\t"); ok && !seen[path] {
			seen[path] = true
			snapshot.unmerged = append(snapshot.unmerged, path)
		}
	}
	if len(snapshot.unmerged) > 0 {
		if _, err := withIndex(name, append([]string{"--literal-pathspecs", "add", "-A", "--"}, snapshot.unmerged...)...); err != nil {
			return unreadable(err)
		}
	}
	tree, err := withIndex(name, "write-tree")
	if err != nil {
		return unreadable(err)
	}
	snapshot.indexTree = strings.TrimSpace(tree)
	return nil
}

func snapshotSummary(stats, prefix string, fence []string) ([]PreservedFile, string, error) {
	files := []PreservedFile{}
	added, deleted, outside, binary := 0, 0, 0, 0
	for _, line := range strings.Split(stats, "\x00") {
		if line == "" {
			continue
		}
		cells := strings.SplitN(line, "\t", 3)
		if len(cells) != 3 {
			return nil, "", fmt.Errorf("invalid preservation numstat: %q", line)
		}
		file := PreservedFile{Path: cells[2], Added: cells[0], Deleted: cells[1], OutsideFence: !insideFence(prefix+cells[2], fence)}
		files = append(files, file)
		if file.OutsideFence {
			outside++
		}
		if file.Added == "-" {
			binary++
		} else {
			a, _ := strconv.Atoi(file.Added)
			d, _ := strconv.Atoi(file.Deleted)
			added += a
			deleted += d
		}
	}
	return files, fmt.Sprintf("%d files, +%d -%d lines; %d binary files; %d outside fence", len(files), added, deleted, binary, outside), nil
}

func (l *Loop) snapshotCovered(ctx context.Context, target, ref, tree string, files []PreservedFile) error {
	if len(files) == 0 {
		return nil
	}
	// Intersect the diff with the dirty paths instead of passing an unbounded
	// path list on argv. Disable rename pairing so both paths remain visible.
	changed, err := l.git(ctx, target, "diff", "--name-only", "-z", "--no-renames", ref, tree, "--")
	if err != nil {
		return err
	}
	paths := make(map[string]bool)
	for _, name := range strings.Split(changed, "\x00") {
		paths[name] = true
	}
	uncovered := 0
	for _, file := range files {
		if paths[file.Path] {
			uncovered++
		}
	}
	if uncovered > 0 {
		return Externalf("lane contains newer dirty contents not covered by %s; retain it rather than discard %d changed files", ref, uncovered)
	}
	return nil
}

// prunePreservedWork is pass-time retention, including owner-closed or removed
// items. No Git command runs inside the read or write transaction.
func (l *Loop) prunePreservedWork(ctx context.Context) error {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var drop []PreservedWork
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		saved, err := ReadPreservedWork(r)
		if err != nil {
			return err
		}
		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		for _, p := range saved {
			if p.Pruned {
				continue
			}
			var state string
			err := r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, p.ItemID).Scan(&state)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if err == sql.ErrNoRows || finishedState(declared.Parsed, state) {
				drop = append(drop, p)
			}
		}
		return nil
	})
	s.Close()
	if err != nil {
		return err
	}
	for _, p := range drop {
		if _, err := l.git(ctx, p.Repository, "update-ref", "--no-deref", "-d", p.Ref); err != nil {
			return err
		}
		if err := appendJudgment(ctx, l.cfg.Store, workPrunedKind, "run", p.RunID, p, l.cfg.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func preservedFacts(ctx context.Context, storePath string, spec StageSpec) (string, error) {
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return "", err
	}
	defer s.Close()
	var b strings.Builder
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		saved, err := ReadPreservedWork(r)
		if err != nil {
			return err
		}
		itemID := spec.ItemID
		if spec.BlockConflict != nil {
			itemID = spec.BlockConflict.ItemID
		}
		for _, p := range saved {
			if p.ItemID != itemID || p.Pruned {
				continue
			}
			fmt.Fprintf(&b, "\nEarlier work exists at %s in %s from run %s: %s. That run stopped: %s. Inspect it before starting over; this is recovery evidence, not a candidate to gate or land.\n", p.Ref, p.Repository, p.RunID, p.Summary, p.Reason)
			if p.IndexRef != "" {
				fmt.Fprintf(&b, "The staged index's different contents are preserved at %s.\n", p.IndexRef)
			}
		}
		return nil
	})
	return b.String(), err
}

func (l *Loop) workflowPredecessorRun(ctx context.Context, d Decision) (string, error) {
	if l.cfg.Store == "" || d.Workflow == nil || d.Attempt <= 0 {
		return "", nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", err
	}
	defer s.Close()
	current := ""
	if d.RunRequest != nil {
		current = d.RunRequest.RunID
	}
	var fallback, staged string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var hasNodes bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='node_runs')`).Scan(&hasNodes); err != nil || !hasNodes {
			return err
		}
		rows, err := effectiveNodeRuns(r, d.ItemID, d.Attempt)
		if err != nil {
			return err
		}
		for _, n := range rows {
			if n.RunID == "" || n.RunID == current {
				continue
			}
			fallback = n.RunID
			if _, statErr := os.Stat(RunDir(l.cfg.Root, n.RunID)); statErr == nil {
				staged = n.RunID
			}
		}
		return nil
	})
	if staged != "" {
		return staged, err
	}
	return fallback, err
}

func (l *Loop) preservedWorkForDecision(ctx context.Context, d Decision, runID string) (*PreservedWork, error) {
	if l.cfg.Store == "" {
		return nil, nil
	}
	itemID := d.ItemID
	if itemID == "" && d.RunRequest != nil {
		itemID = d.RunRequest.RunID
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var found *PreservedWork
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		saved, err := ReadPreservedWork(r)
		if err != nil {
			return err
		}
		findByRun := func(targetRun string) *PreservedWork {
			if targetRun == "" {
				return nil
			}
			var match *PreservedWork
			for i := range saved {
				p := saved[i]
				if !p.Pruned && p.RunID == targetRun {
					copy := p
					match = &copy
				}
			}
			return match
		}
		if found = findByRun(runID); found != nil {
			return nil
		}
		if found = findByRun(d.SupersedesRunID); found != nil {
			return nil
		}
		if d.Workflow != nil && d.Attempt > 0 {
			var hasNodes bool
			if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='node_runs')`).Scan(&hasNodes); err != nil {
				return err
			}
			if hasNodes {
				rows, err := effectiveNodeRuns(r, itemID, d.Attempt)
				if err != nil {
					return err
				}
				attemptRuns := make(map[string]bool, len(rows))
				for _, n := range rows {
					if n.RunID != "" {
						attemptRuns[n.RunID] = true
					}
				}
				for i := range saved {
					p := saved[i]
					if !p.Pruned && attemptRuns[p.RunID] {
						copy := p
						found = &copy
					}
				}
				return nil
			}
		}
		if runID == "" && d.Attempt == 0 {
			for i := range saved {
				p := saved[i]
				if !p.Pruned && p.ItemID == itemID {
					copy := p
					found = &copy
				}
			}
		}
		return nil
	})
	return found, err
}

type preservedConflict struct {
	Ref   string
	Paths []string
}

func (c preservedConflict) brief() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nKept work at %s could not be applied. No partial replay was kept in this checkout. Inspect the kept ref and reconcile its changes with this checkout before continuing.\n", c.Ref)
	for _, path := range c.Paths {
		fmt.Fprintf(&b, "- Kept-work conflict: %q\n", path)
	}
	return b.String()
}

// applyPreservedWork replays onto a clean destination lane. A content conflict
// leaves that lane clean, including when the index replay succeeded but the
// working-tree replay failed. The kept refs still hold both snapshots.
func (l *Loop) applyPreservedWork(ctx context.Context, lane, target string, p PreservedWork) (*preservedConflict, error) {
	if p.Ref == "" {
		return nil, nil
	}
	if _, err := l.git(ctx, target, "rev-parse", "--verify", "--quiet", p.Ref); err != nil {
		return nil, nil
	}
	status, err := l.git(ctx, target, "--no-optional-locks", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	if status != "" {
		first, _, _ := strings.Cut(status, "\n")
		return nil, laneRefusalf(lane, ReasonLaneUncommittedWork, "commit or remove the files, or reset the lane deliberately",
			"lane %s holds uncommitted work (%s)", lane, first)
	}

	from := p.Ref + "^1"
	if p.IndexRef != "" {
		if _, err := l.git(ctx, target, "rev-parse", "--verify", "--quiet", p.IndexRef); err == nil {
			paths, err := l.applyPreservedDiff(ctx, target, p.IndexRef+"^1", p.IndexRef, true)
			if err != nil || len(paths) > 0 {
				return l.finishPreservedReplay(ctx, target, p.Ref, paths, err)
			}
			from = p.IndexRef
		}
	}
	paths, err := l.applyPreservedDiff(ctx, target, from, p.Ref, false)
	return l.finishPreservedReplay(ctx, target, p.Ref, paths, err)
}

func (l *Loop) finishPreservedReplay(ctx context.Context, target, ref string, paths []string, err error) (*preservedConflict, error) {
	if err == nil && len(paths) == 0 {
		return nil, nil
	}
	// The direct apply is atomic on a content mismatch; a three-way conflict
	// and the first (index) patch both leave their changes tracked in the index.
	if _, resetErr := l.git(ctx, target, "reset", "--hard", "HEAD"); resetErr != nil {
		return nil, Externalf("rolling back preserved work %s: %v (apply error: %v)", ref, resetErr, err)
	}
	if err != nil {
		return nil, err
	}
	return &preservedConflict{Ref: ref, Paths: paths}, nil
}

func (l *Loop) applyPreservedDiff(ctx context.Context, target, from, to string, index bool) ([]string, error) {
	patch, err := l.git(ctx, target, "diff", "--binary", from, to)
	if err != nil || patch == "" {
		return nil, err
	}
	args := []string{"-C", target, "apply"}
	if index {
		args = append(args, "--index")
	}
	apply := func(args []string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
		cmd.Stdin = strings.NewReader(patch)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stderr.String(), err
	}
	if _, err := apply(args); err == nil {
		return nil, nil
	}
	stderr, err := apply(append(args, "--3way"))
	if err == nil {
		return nil, nil
	}
	paths, contentFailure := applyMismatchPaths(stderr)
	if ctx.Err() == nil && contentFailure {
		unmerged, err := l.git(ctx, target, "diff", "--name-only", "--diff-filter=U", "-z")
		if err != nil {
			return nil, err
		}
		for _, path := range strings.Split(strings.TrimSuffix(unmerged, "\x00"), "\x00") {
			if path != "" && !slices.Contains(paths, path) {
				paths = append(paths, path)
			}
		}
		if len(paths) > 0 {
			return paths, nil
		}
	}
	return nil, Externalf("applying preserved work (%s..%s): %v: %s", from, to, err, strings.TrimSpace(stderr))
}

// Some mismatches (such as a deleted path) cannot leave unmerged entries.
// Recognize Git's direct-apply mismatch diagnostics, not arbitrary failures
// such as an index lock, missing object, or inability to write the checkout.
func applyMismatchPaths(stderr string) (paths []string, contentFailure bool) {
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "fatal: ") {
			return nil, false
		}
		// A different file's mismatch can stop Git before it writes the
		// unmerged entries it already diagnosed.
		if path, ok := strings.CutPrefix(line, "Applied patch to '"); ok {
			if path, ok := strings.CutSuffix(path, "' with conflicts."); ok {
				paths = append(paths, path)
			}
			continue
		}
		msg, ok := strings.CutPrefix(line, "error: ")
		if !ok || strings.HasPrefix(msg, "patch failed: ") {
			continue
		}
		matched := false
		for _, suffix := range []string{": patch does not apply", ": does not exist in index", ": already exists in index", ": already exists in working directory", ": does not match index", ": wrong type"} {
			if path, ok := strings.CutSuffix(msg, suffix); ok {
				if !slices.Contains(paths, path) {
					paths = append(paths, path)
				}
				matched = true
				break
			}
		}
		if !matched {
			return nil, false
		}
	}
	return paths, true
}
