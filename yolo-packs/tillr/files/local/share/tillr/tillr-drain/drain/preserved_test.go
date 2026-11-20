package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestAbandonPreservesUncommittedWork(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	lane := filepath.Join(f.laneRoot, "lane-1", WorktreeDir)
	writeForkFile(t, filepath.Join(f.laneRoot, "lane-1", FenceFile), "inside/\n")
	writeForkFile(t, filepath.Join(lane, "base.txt"), "edited\nsecond line\n")
	if err := os.MkdirAll(filepath.Join(lane, "inside"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane, "inside/new.txt"), "inside\n")
	writeForkFile(t, filepath.Join(lane, "outside.txt"), "outside\n")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes (lane_id,state) VALUES ('lane-1','idle')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO runs (run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES ('run-a','item-a','lane-1','fixture','finished',1,2,'[]',1)`)
		return err
	})
	if stop := l.abandon(context.Background(), "lane-1", "run-a", l.headOf(context.Background(), "lane-1")); stop != "" {
		t.Fatal(stop)
	}
	ref := "refs/tillr/preserved/run-a"
	for path, want := range map[string]string{"base.txt": "edited\nsecond line", "inside/new.txt": "inside", "outside.txt": "outside"} {
		if got := runGit(t, f.root, "show", ref+":"+path); got != want {
			t.Fatalf("preserved %s = %q, want %q", path, got, want)
		}
	}
	if got := runGit(t, lane, "show", "HEAD:base.txt"); got != "base" {
		t.Fatal(got)
	}
	if got := runGit(t, f.root, "rev-parse", "refs/tillr/runs/run-a"); got != runGit(t, f.root, "rev-parse", TrunkRef) {
		t.Fatal("saved work became candidate pin")
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		events, err := r.Events(0)
		if err != nil {
			return err
		}
		for _, e := range events {
			if e.Kind != "drain.work-preserved" {
				continue
			}
			var p struct {
				Ref, Summary string
				Files        []struct {
					Path           string
					Added, Deleted string
					Outside        bool `json:"outside_fence"`
				}
			}
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return err
			}
			if p.Ref != ref || !strings.Contains(p.Summary, "3 files") || !strings.Contains(p.Summary, "+4 -1") {
				t.Fatalf("record: %s", e.Payload)
			}
			if len(p.Files) != 3 {
				t.Fatalf("files: %s", e.Payload)
			}
			for _, file := range p.Files {
				if file.Outside != (file.Path != "inside/new.txt") {
					t.Fatalf("scope: %+v", file)
				}
			}
			return nil
		}
		t.Fatal("no preservation settlement event")
		return nil
	})
	// The reset clears the untracked file, because the preserved ref above holds it
	// (TestAbandonClearsTheUntrackedFilesItPreserved).
	if _, err := os.Stat(filepath.Join(lane, "inside/new.txt")); !os.IsNotExist(err) {
		t.Fatalf("the preserved untracked file is still in the lane: %v", err)
	}
}

func TestAbandonCleanLaneCreatesNoPreservationRef(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	head := l.headOf(context.Background(), "lane-1")
	// Existing committed-head retention is independent of dirty-work preservation.
	if err := l.pin(context.Background(), "run-clean", head); err != nil {
		t.Fatal(err)
	}
	before := runGit(t, f.root, "for-each-ref")
	if stop := l.abandon(context.Background(), "lane-1", "run-clean", head); stop != "" {
		t.Fatal(stop)
	}
	if got := runGit(t, f.root, "for-each-ref"); got != before {
		t.Fatalf("clean lane made ref: %s", got)
	}
}

func TestSiblingPreservationIsLanderRef(t *testing.T) {
	own := Worktree{Path: "/lanes/own", Head: "base"}
	before := &Boundary{Repo: "/repo", Targets: []string{own.Path}, Refs: map[string]string{}, Worktrees: []Worktree{own}}
	after := &Boundary{Repo: before.Repo, Targets: before.Targets, Refs: map[string]string{"refs/tillr/preserved/run-sibling": "saved"}, Worktrees: before.Worktrees}
	convicted, observed, err := CompareRunObserved(before, after)
	if err != nil || len(convicted) != 0 || len(observed) != 0 {
		t.Fatalf("sibling preservation convicted an in-flight run: %v / %v / %v", convicted, observed, err)
	}
}

// A run id too long to be a ref's file name is pinned under a digest of the name, so the pin
// succeeds and the commit stays reachable. A name that fits is pinned under itself.
func TestPinStoresAnOverlongRunIDUnderItsDigest(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	ctx := context.Background()
	long := "run-item-a-a1-n1" + strings.Repeat("-failover", 40)
	trunk := strings.TrimSpace(runGit(t, f.root, "rev-parse", TrunkRef))
	off := strings.TrimSpace(runGit(t, f.root, "commit-tree", trunk+"^{tree}", "-p", trunk, "-m", "off trunk"))
	if err := l.pin(ctx, long, off); err != nil {
		t.Fatalf("an overlong run id was not pinned: %v", err)
	}
	ref := pinRef(long)
	if !strings.HasPrefix(ref, "refs/tillr/runs-long/") {
		t.Fatalf("overlong run id pinned at %s", ref)
	}
	if got := strings.TrimSpace(runGit(t, f.root, "rev-parse", ref)); got != off {
		t.Fatalf("pin = %s, want %s", got, off)
	}
	if err := l.pin(ctx, "run-item-a-a1-n1", off); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(runGit(t, f.root, "rev-parse", "refs/tillr/runs/run-item-a-a1-n1")); got != off {
		t.Fatalf("pin = %s, want %s", got, off)
	}
}
