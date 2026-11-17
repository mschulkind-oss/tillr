package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/preserved"
	"github.com/mschulkind-oss/tillr-run/run"
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
}

// preserveWork finishes all Git work before the short event write. The separate
// index leaves the real index and branch untouched. A second parent keeps staged
// bytes when they differ from the working copy. The commit's message retains the
// record for a crash between creating its ref and writing the settlement event.
func (l *Loop) preserveWork(ctx context.Context, lane, runID, reason string) error {
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
	status, err := l.git(ctx, target, "status", "--porcelain", "--untracked-files=all")
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
			if len(snapshot.files) == 0 && len(snapshot.indexFiles) == 0 {
				return nil
			}
			p = PreservedWork{RunID: runID, LaneID: lane, Ref: ref, Repository: target, Reason: reason, Files: snapshot.files, Summary: snapshot.summary}
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
			body, err := json.Marshal(p)
			if err != nil {
				return err
			}
			args = append(args, "-m", string(body))
			sha, err := l.git(ctx, target, args...)
			if err != nil {
				return err
			}
			if _, err := l.git(ctx, target, "update-ref", ref, strings.TrimSpace(sha), ""); err != nil {
				return err
			}
			exists = true
		}
	}
	if !exists {
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
		return err
	})
	if err != nil {
		return "", fmt.Errorf("identifying dirty run %s: %w", runID, err)
	}
	return itemID, nil
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
			terminal = err == sql.ErrNoRows || terminalStates[state]
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
	staged, err := l.git(ctx, target, "write-tree")
	if err != nil {
		return snapshot, err
	}
	snapshot.indexTree = strings.TrimSpace(staged)
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
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", target}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+name)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", Externalf("preserving %s: git %s: %v: %s", runID, strings.Join(args, " "), err, stderr.String())
		}
		return string(out), nil
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
	stats, err = git("diff", "--numstat", "-z", "--no-renames", snapshot.head, snapshot.indexTree)
	if err != nil {
		return snapshot, err
	}
	snapshot.indexFiles, snapshot.indexSummary, err = snapshotSummary(stats, prefix, fence)
	return snapshot, err
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
	args := []string{"--literal-pathspecs", "diff", "--name-only", "-z", ref, tree, "--"}
	for _, file := range files {
		args = append(args, file.Path)
	}
	changed, err := l.git(ctx, target, args...)
	if err != nil {
		return err
	}
	if changed != "" {
		return Externalf("lane contains newer dirty contents not covered by %s; retain it rather than discard %s", ref, strings.ReplaceAll(strings.TrimSuffix(changed, "\x00"), "\x00", ", "))
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
		for _, p := range saved {
			if p.Pruned {
				continue
			}
			var state string
			err := r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, p.ItemID).Scan(&state)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if err == sql.ErrNoRows || terminalStates[state] {
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
		if _, err := l.git(ctx, p.Repository, "update-ref", "-d", p.Ref); err != nil {
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
