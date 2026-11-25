package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The fence is derived, and the derivation is a rule with cases: a path the rule does not
// cover is a lane refused by `fenceViolation` after the work is done, which on 2026-11-14 cost
// `item-obs-archive` a whole item.
//
// Each case asserts the whole derived SET rather than looking for a member. A member check
// passes when the rule also derives something it should not, so the set is what makes a
// reverted rule fail: drop any one derivation from `fencePaths` and one of these cases stops
// matching.
//
// The last case is the negative control the set comparison needs a partner for: a fence naming
// no derived path derives NOTHING, so `fencePaths` is a rule about the paths it was given and
// not a blanket widening of every fence.
func TestFencePathsDeriveTheGateCompanions(t *testing.T) {
	skill := "yolo-packs/tillr/skills/tillr-operating/SKILL.md"
	docsTest := "tools/tillr/tillrdocs/"
	docsTestMirror := "yolo-packs/tillr/files/local/share/tillr/tillr/tillrdocs/"
	mirror := "yolo-packs/tillr/files/local/share/tillr/"

	cases := []struct {
		name    string
		allowed []string
		want    []string
		why     string
	}{
		{
			name:    "a module's own directory",
			allowed: []string{"tools/tillr-drain"},
			want: []string{
				"tools/tillr-drain",
				mirror + "tillr-drain/",
				skill,
				docsTest,
				docsTestMirror,
			},
			why: "the module's directory already covers its README, so no README entry is derived",
		},
		{
			name:    "a sub-path inside a module",
			allowed: []string{"tools/tillr-view/web/"},
			want: []string{
				"tools/tillr-view/web/",
				mirror + "tillr-view/",
				"tools/tillr-view/README.md",
				skill,
				docsTest,
				docsTestMirror,
			},
			why: "a sub-path does not cover the module's README, which the documentation suite holds",
		},
		{
			name:    "the run adapters",
			allowed: []string{"tools/tillr-run/backends/"},
			want: []string{
				"tools/tillr-run/backends/",
				mirror + "tillr-run/",
				"tools/tillr-run/README.md",
				skill,
				docsTest,
				docsTestMirror,
				"tests/tillr-muse-backend.test.py",
			},
			why: "the adapters' suite is derived from the directory that holds them",
		},
		{
			name:    "the loop program inside a module's directory",
			allowed: []string{"tools/tillr-loop/main.go"},
			want: []string{
				"tools/tillr-loop/main.go",
				mirror + "tillr-loop/",
				"tools/tillr-loop/README.md",
				skill,
				docsTest,
				docsTestMirror,
				"tools/tillr-loop/",
			},
			why: "a fence naming only the program barred the lane from the test and README judged with it",
		},
		{
			name:    "a path that names none of them",
			allowed: []string{"docs/reference/tillr-muse-backend.md"},
			want:    []string{"docs/reference/tillr-muse-backend.md"},
			why:     "the derivation is a rule about the paths given, not a widening of every fence",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sortedCopy(fencePaths(c.allowed))
			want := sortedCopy(c.want)
			if len(got) != len(want) {
				t.Fatalf("fencePaths(%v) = %v, want %v: %s", c.allowed, got, want, c.why)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("fencePaths(%v) = %v, want %v: %s", c.allowed, got, want, c.why)
				}
			}
			// Staging derives the fence from ScopePaths and then passes that scope to fenceText,
			// so re-expanding a derived module fence must not widen it into tools/tillr/.
			if c.name != "the loop program inside a module's directory" {
				again := sortedCopy(fencePaths(got))
				if len(again) != len(got) {
					t.Fatalf("fencePaths(fencePaths(%v)) = %v, want idempotent %v", c.allowed, again, got)
				}
				for i := range got {
					if again[i] != got[i] {
						t.Fatalf("fencePaths(fencePaths(%v)) = %v, want idempotent %v", c.allowed, again, got)
					}
				}
			}
		})
	}
}

// The fence's derived entries are additions: the item's own paths are the fence's head, in
// the order the item stated them, and a reader of `fence.txt` sees what the item asked for
// before it sees what the derivation added.
func TestFencePathsKeepTheItemsOwnPathsFirst(t *testing.T) {
	allowed := []string{"tools/tillr-drain/", "docs/reference/tillr-muse-backend.md"}
	got := fencePaths(allowed)
	if len(got) < len(allowed) {
		t.Fatalf("fencePaths dropped an item path: %v", got)
	}
	for i, want := range allowed {
		if got[i] != want {
			t.Fatalf("fencePaths(%v)[%d] = %q, want the item's own path %q", allowed, i, got[i], want)
		}
	}
}

func sortedCopy(paths []string) []string {
	out := append([]string(nil), paths...)
	sort.Strings(out)
	return out
}

// A lane is not a session. Two unrelated runs dispatched on one lane each get their own
// harness session, because the `session-id` left beside the brief belongs to the run that was
// there before.
//
// Measured 2026-11-15: `item-stateless-squash-lander` and `item-lockless-per-lane-drain` both
// ran on `lane-1` under `--session-id 70a4f422-ae04-4b91-a1c2-b63c9c9caa13`, so the second
// item's context carried the first item's transcript. The case fails when staging reads the
// lane's file back, which is exactly the rule this pins.
func TestTwoRunsOnOneLaneGetTwoSessions(t *testing.T) {
	f := newStagingFixture(t, "candidate.txt")
	stager := f.stager()
	laneDir := filepath.Join(f.laneRoot, "lane-1")

	if err := stager.Stage(context.Background(), f.spec("run-item_041-1")); err != nil {
		t.Fatalf("staging the first run failed: %v", err)
	}
	first, err := ReadSessionID(laneDir)
	if err != nil {
		t.Fatalf("reading the first run's session id failed: %v", err)
	}
	if first == "" {
		t.Fatalf("the first run staged no session id at %s", filepath.Join(laneDir, SessionIDFile))
	}
	if err := stager.Stage(context.Background(), f.spec("run-item_041-2")); err != nil {
		t.Fatalf("staging the second run failed: %v", err)
	}
	second, err := ReadSessionID(laneDir)
	if err != nil {
		t.Fatalf("reading the second run's session id failed: %v", err)
	}
	if second == first {
		t.Fatalf("both runs were staged under the session %s: a lane is reused by unrelated runs, and one transcript is not two", first)
	}
	// The per-run copy is the one a timeline reads, so it has to carry the same answer.
	for _, runID := range []string{"run-item_041-1", "run-item_041-2"} {
		want := first
		if runID == "run-item_041-2" {
			want = second
		}
		raw, err := os.ReadFile(filepath.Join(RunDir(f.root, runID), SessionIDFile))
		if err != nil {
			t.Fatalf("reading %s's session id failed: %v", runID, err)
		}
		if got := strings.TrimSpace(string(raw)); got != want {
			t.Fatalf("%s was staged under %q, want %q", runID, got, want)
		}
	}
}

// A resume is the one dispatch that keeps a lane's session: it names the id it continues, and
// staging keeps a pinned id. Without this the rule above would be satisfied by a fresh mint
// for everything, and a resume would pay for the transcript twice while recording itself as a
// continuation.
func TestAResumeStageKeepsTheLanesOwnSession(t *testing.T) {
	f := newStagingFixture(t, "candidate.txt")
	stager := f.stager()
	laneDir := filepath.Join(f.laneRoot, "lane-1")

	if err := stager.Stage(context.Background(), f.spec("run-item_041-1")); err != nil {
		t.Fatalf("staging the first run failed: %v", err)
	}
	own, err := ReadSessionID(laneDir)
	if err != nil {
		t.Fatalf("reading the lane's session id failed: %v", err)
	}
	if own == "" {
		t.Fatalf("the lane carries no session id to resume")
	}
	resume := f.spec("run-item_041-1")
	resume.SessionID = own
	if err := stager.Stage(context.Background(), resume); err != nil {
		t.Fatalf("re-staging the run as a resume failed: %v", err)
	}
	kept, err := ReadSessionID(laneDir)
	if err != nil {
		t.Fatalf("reading the resumed session id failed: %v", err)
	}
	if kept != own {
		t.Fatalf("the resume was staged under %q, want the lane's own %q: a continuation needs the transcript it continues", kept, own)
	}
	// The negative control: a pinned id that is not a session id is refused, so the case
	// above passes on the value being kept and not on every pin being accepted.
	broken := f.spec("run-item_041-1")
	broken.SessionID = "not-a-session"
	err = stager.Stage(context.Background(), broken)
	if err == nil {
		t.Fatalf("staging accepted a session id that is not a UUID")
	}
	if !strings.Contains(err.Error(), "not-a-session") {
		t.Fatalf("the refusal does not name the value it refused: %v", err)
	}
}

// stagingFixture is a real staging fixture inside this package: a repository with a trunk
// commit, a lane root, and a store holding one item whose fence is the paths given.
type stagingFixture struct {
	root, laneRoot, storePath, base, itemID string
}

func newStagingFixture(t *testing.T, allowed ...string) stagingFixture {
	t.Helper()
	dir := scratchUnderDurable(t)
	root := filepath.Join(dir, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("creating the staging repository failed: %v", err)
	}
	runGit(t, root, "init", "-b", "main")
	writeForkFile(t, filepath.Join(root, "base.txt"), "base\n")
	runGit(t, root, "add", "base.txt")
	runGit(t, root, "commit", "-m", "base")
	laneRoot := filepath.Join(dir, "lanes")
	if err := os.MkdirAll(laneRoot, 0o755); err != nil {
		t.Fatalf("creating the staging lane root failed: %v", err)
	}
	storePath := filepath.Join(dir, "board.sqlite3")
	const itemID = "item_041"
	seedStagedItem(t, storePath, itemID, allowed...)
	return stagingFixture{
		root: root, laneRoot: laneRoot, storePath: storePath, itemID: itemID,
		base: runGit(t, root, "rev-parse", "HEAD"),
	}
}

func (f stagingFixture) stager() *LaneStager {
	return &LaneStager{
		Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      func() time.Time { return time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC) },
	}
}

func (f stagingFixture) spec(runID string) StageSpec {
	return StageSpec{
		LaneID: "lane-1", ItemID: f.itemID, RunID: runID, Backend: "pi",
		BaseSHA: f.base, WorkingDir: WorktreeDir, TimeoutMS: 60000,
	}
}

// seedStagedItem files one item the stager can read, through the same apply the item face
// uses: a store seeded by hand would keep passing after the record's shape moved.
func seedStagedItem(t *testing.T, storePath, itemID string, allowed ...string) {
	t.Helper()
	ctx := context.Background()
	payload, err := json.Marshal(map[string]any{
		"item_id":       itemID,
		"title":         "the item " + itemID,
		"body_ref":      "briefs/" + itemID + ".md",
		"state":         "in-queue",
		"blocked_by":    []string{},
		"allowed_paths": allowed,
		"criteria": []map[string]string{
			{"criterion_id": "c1", "text": "the first criterion of " + itemID, "state": ""},
		},
	})
	if err != nil {
		t.Fatalf("encoding the item failed: %v", err)
	}
	s, err := store.InitFixture(ctx, storePath)
	if err != nil {
		t.Fatalf("creating the fixture store failed: %v", err)
	}
	defer s.Close()
	env := record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          item.RecordKind,
		ID:            "item-" + itemID,
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
		Payload:       payload,
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.Apply(ctx, tx, env)
		return err
	}); err != nil {
		t.Fatalf("seeding the item failed: %v", err)
	}
}
