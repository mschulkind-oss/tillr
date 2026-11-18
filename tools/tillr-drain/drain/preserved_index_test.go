package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// preservationFixture is a lane with one finished run, run-a, whose work the test leaves.
func preservationFixture(t *testing.T) (refusalLoopFixture, *Loop, string) {
	t.Helper()
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	l.cfg.Report = &bytes.Buffer{}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes (lane_id,state) VALUES ('lane-1','idle')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO runs (run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES ('run-a','item-a','lane-1','fixture','finished',1,2,'[]',1)`)
		return err
	})
	return f, l, filepath.Join(f.laneRoot, "lane-1", WorktreeDir)
}

func preservedRecord(t *testing.T, f refusalLoopFixture) map[string]any {
	t.Helper()
	var out map[string]any
	fixtureRead(t, f, func(r *store.Reader) error {
		events, err := r.Events(0)
		if err != nil {
			return err
		}
		for _, e := range events {
			if e.Kind == workPreservedKind {
				out = map[string]any{}
				if err := json.Unmarshal(e.Payload, &out); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if out == nil {
		t.Fatal("no preservation settlement event")
	}
	return out
}

// A worker that resolved a conflict in the working copy but never added it leaves the
// lane's index unmerged, and `git write-tree` on that index fails (lane-14, 2026-11-18).
// The snapshot is written from temporary indexes, so the work is preserved, the record
// names the unmerged path, and the lane's own index is untouched until the reset.
func TestPreservationSnapshotsALaneWhoseIndexIsUnmerged(t *testing.T) {
	f, l, lane := preservationFixture(t)
	runGit(t, lane, "config", "user.name", "Fixture")
	runGit(t, lane, "config", "user.email", "fixture@example.invalid")
	base := runGit(t, lane, "rev-parse", "HEAD")
	runGit(t, lane, "checkout", "-q", "-b", "side")
	writeForkFile(t, filepath.Join(lane, "base.txt"), "side\n")
	runGit(t, lane, "commit", "-qam", "side")
	runGit(t, lane, "checkout", "-q", "--detach", base)
	writeForkFile(t, filepath.Join(lane, "base.txt"), "ours\n")
	runGit(t, lane, "commit", "-qam", "ours")
	head := runGit(t, lane, "rev-parse", "HEAD")
	cmd := exec.Command("git", "-C", lane, "cherry-pick", "--no-commit", "side")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("the fixture's cherry-pick did not conflict: %s", out)
	}
	runGit(t, lane, "cherry-pick", "--quit")
	// The worker resolves the file by hand and never adds it.
	writeForkFile(t, filepath.Join(lane, "base.txt"), "resolved\n")
	writeForkFile(t, filepath.Join(lane, "staged.txt"), "staged\n")
	runGit(t, lane, "add", "staged.txt")
	writeForkFile(t, filepath.Join(lane, "staged.txt"), "staged\nand edited after\n")
	if unmerged := runGit(t, lane, "ls-files", "-u"); unmerged == "" {
		t.Fatal("the fixture's index is not unmerged")
	}
	if out, err := exec.Command("git", "-C", lane, "write-tree").CombinedOutput(); err == nil {
		t.Fatalf("write-tree took an unmerged index: %s", out)
	}
	if stop := l.abandon(context.Background(), "lane-1", "run-a", head); stop != "" {
		t.Fatalf("an unmerged index blocked the preservation: %s", stop)
	}
	ref := "refs/tillr/preserved/run-a"
	if got := runGit(t, f.root, "show", ref+":base.txt"); got != "resolved" {
		t.Fatalf("preserved base.txt = %q, want the working copy's resolution", got)
	}
	if got := runGit(t, f.root, "show", ref+"^2:staged.txt"); got != "staged" {
		t.Fatalf("the staged snapshot lost the staged file: %q", got)
	}
	p := preservedRecord(t, f)
	if unmerged, _ := json.Marshal(p["unmerged"]); string(unmerged) != `["base.txt"]` {
		t.Fatalf("the record does not name the unmerged path: %v", p)
	}
	if summary, _ := p["summary"].(string); !strings.Contains(summary, "unmerged in the lane's index: base.txt") {
		t.Fatalf("summary %q", summary)
	}
}

// lockPath is the lane's index.lock, wherever its git directory is.
func lockPath(t *testing.T, lane string) string {
	t.Helper()
	path := runGit(t, lane, "rev-parse", "--path-format=absolute", "--git-path", "index.lock")
	return path
}

// A zero-byte index.lock a killed git left behind (lane-18, 2026-11-18) blocks every git
// write in the lane. With no process holding it open or working in the lane, it is
// stale: the loop removes it, records the removal, and preserves and resets the lane.
func TestAStaleIndexLockIsRemovedWithARecordAndTheLaneIsPreserved(t *testing.T) {
	f, l, lane := preservationFixture(t)
	head := l.headOf(context.Background(), "lane-1")
	writeForkFile(t, filepath.Join(lane, "base.txt"), "edited\n")
	lock := lockPath(t, lane)
	if err := os.WriteFile(lock, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if stop := l.abandon(context.Background(), "lane-1", "run-a", head); stop != "" {
		t.Fatalf("a stale lock blocked the preservation: %s", stop)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("the stale lock is still there: %v", err)
	}
	if got := runGit(t, f.root, "show", "refs/tillr/preserved/run-a:base.txt"); got != "edited" {
		t.Fatalf("preserved %q", got)
	}
	if got := runGit(t, lane, "status", "--porcelain"); got != "" {
		t.Fatalf("the lane was not reset: %q", got)
	}
	var records []string
	fixtureRead(t, f, func(r *store.Reader) error {
		events, err := r.Events(0)
		for _, e := range events {
			if e.Kind == staleIndexLockKind {
				records = append(records, string(e.Payload))
			}
		}
		return err
	})
	if len(records) != 1 || !strings.Contains(records[0], `"lane_id":"lane-1"`) || !strings.Contains(records[0], `"run_id":"run-a"`) || !strings.Contains(records[0], filepath.Base(filepath.Dir(lock))) {
		t.Fatalf("removal records: %v", records)
	}
	if !strings.Contains(l.cfg.Report.(*bytes.Buffer).String(), "loop: lane lane-1: removed stale "+lock) {
		t.Fatalf("the removal is not reported:\n%s", l.cfg.Report.(*bytes.Buffer).String())
	}
}

// A lock a live process holds open is not stale: it stays, and the preservation fails
// naming the holder, so the hold's mover is that process.
func TestAHeldIndexLockIsKeptAndItsHolderNamed(t *testing.T) {
	_, l, lane := preservationFixture(t)
	writeForkFile(t, filepath.Join(lane, "base.txt"), "edited\n")
	lock := lockPath(t, lane)
	file, err := os.Create(lock)
	if err != nil {
		t.Fatal(err)
	}
	holder := exec.Command("sleep", "60")
	holder.ExtraFiles = []*os.File{file}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	file.Close()
	t.Cleanup(func() { holder.Process.Kill(); holder.Wait() })
	err = l.preserveWork(context.Background(), "lane-1", "run-a", "settled")
	if err == nil || !strings.Contains(err.Error(), "held by PID "+strconv.Itoa(holder.Process.Pid)) {
		t.Fatalf("a held lock was not named: %v", err)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("a held lock was removed: %v", err)
	}
}
