package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

const trunkMovedDecline = "trunk moved and the planned inputs or fence are not known to be unchanged"

// A design landing can answer the conflict between document refresh and planning.
// The store's item/run/lease version can then be current while the pass's base is old.
func TestDispatchDefersWhenTrunkMovesAfterDocumentRefresh(t *testing.T) {
	for _, movement := range []string{"before-plan", "after-plan"} {
		t.Run(movement, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			ctx := context.Background()
			var result LoopResult
			base, err := l.refreshDocuments(ctx, &result)
			if err != nil {
				t.Fatal(err)
			}
			move := func() {
				runGit(t, f.root, "commit", "--allow-empty", "-qm", "design amendment landed")
			}
			if movement == "before-plan" {
				move()
			}
			plan := mustPlan(t, l)[0]
			if movement == "after-plan" {
				move()
			}
			before := eventCount(t, f.storePath)
			generation := ""
			ok, work, err := l.applyPlanGeneration(ctx, plan, base, l.cfg.Now(), &generation)
			if err != nil || ok || work != nil {
				t.Fatalf("dispatch used stale refreshed trunk %s: applied=%v work=%+v error=%v", base, ok, work, err)
			}
			if generation != "" {
				t.Fatalf("stale dispatch allocated generation %q", generation)
			}
			assertOnlyDispatchDecline(t, f.storePath, before, plan.decision, trunkMovedDecline)
			fresh, err := l.refreshDocuments(ctx, &result)
			if err != nil {
				t.Fatal(err)
			}
			if fresh == base {
				t.Fatal("refresh did not observe the landing")
			}
			ok, work, err = l.applyPlan(ctx, mustPlan(t, l)[0], fresh, l.cfg.Now())
			if err != nil || !ok || work == nil || work.BaseSHA != fresh {
				t.Fatalf("refreshed dispatch: applied=%v work=%+v error=%v", ok, work, err)
			}
		})
	}
}

type midPassLandingStager struct {
	inner     Stager
	onFirst   func()
	triggered bool
	staged    map[string]string
}

func (s *midPassLandingStager) Stage(ctx context.Context, spec StageSpec) error {
	if err := s.inner.Stage(ctx, spec); err != nil {
		return err
	}
	if s.staged == nil {
		s.staged = map[string]string{}
	}
	s.staged[spec.ItemID] = spec.BaseSHA
	if !s.triggered && s.onFirst != nil {
		s.triggered = true
		s.onFirst()
	}
	return nil
}

func writeRepoFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, full, body)
}

func TestMidPassSiblingLandingStartsUnrelatedItemOnNewTrunk(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	ctx := context.Background()
	writeRepoFile(t, f.root, "briefs/item-a.md", "# item-a\n\ndo item-a\n")
	writeRepoFile(t, f.root, "briefs/item-b.md", "# item-b\n\ndo item-b\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "seed briefs")
	initialTrunk := runGit(t, f.root, "rev-parse", TrunkRef)

	steps := &recordingSteps{}
	l.cfg.Steps = steps
	var siblingTrunk string
	stager := &midPassLandingStager{
		inner: &LaneStager{
			Root:     f.root,
			LaneRoot: f.laneRoot,
			Store:    f.storePath,
			Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
			Now:      l.cfg.Now,
		},
		onFirst: func() {
			writeRepoFile(t, f.root, "tools/item-sibling/sibling.go", "package sibling\n")
			runGit(t, f.root, "add", "tools/item-sibling/sibling.go")
			runGit(t, f.root, "commit", "-qm", "sibling landed mid-pass")
			siblingTrunk = runGit(t, f.root, "rev-parse", TrunkRef)
		},
	}
	l.cfg.Stage = stager
	if err := l.stageLanes(ctx); err != nil {
		t.Fatal(err)
	}

	var result LoopResult
	stop, err := l.pass(ctx, &result)
	if err != nil || stop != "" {
		t.Fatalf("pass stopped: stop=%q err=%v", stop, err)
	}
	if siblingTrunk == "" || siblingTrunk == initialTrunk {
		t.Fatalf("sibling landing did not advance trunk: initial=%s sibling=%s", initialTrunk, siblingTrunk)
	}
	drainOwned(t, l, &result)

	if !steps.ran("item-a") || !steps.ran("item-b") {
		t.Fatalf("unrelated start cancelled mid-pass: ran item-a=%v item-b=%v", steps.ran("item-a"), steps.ran("item-b"))
	}
	if got := stager.staged["item-a"]; got != initialTrunk {
		t.Fatalf("item-a staged at %s, want initial trunk %s", got, initialTrunk)
	}
	if got := stager.staged["item-b"]; got != siblingTrunk {
		t.Fatalf("item-b staged at %s, want new trunk %s", got, siblingTrunk)
	}
	if head := runGit(t, l.laneTarget("lane-2"), "rev-parse", "HEAD"); head != siblingTrunk {
		t.Fatalf("lane-2 worktree HEAD=%s, want new trunk %s", head, siblingTrunk)
	}
	raw, err := os.ReadFile(filepath.Join(l.laneDirPath("lane-2"), BaseSHAFile))
	if err != nil || strings.TrimSpace(string(raw)) != siblingTrunk {
		t.Fatalf("lane-2 base_sha=%q err=%v, want %s", string(raw), err, siblingTrunk)
	}
}

func TestMidPassLandingTouchingInputsOrFenceCancelsStart(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f refusalLoopFixture)
		touch func(t *testing.T, f refusalLoopFixture)
	}{
		{
			name: "body-ref-section",
			setup: func(t *testing.T, f refusalLoopFixture) {
				writeRepoFile(t, f.root, "briefs/item-a.md", "# Doc\n\n## the-item\n\ninitial body\n\n## other\n\nother text\n")
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET body_ref = 'briefs/item-a.md#the-item' WHERE item_id = 'item-a'`)
					return err
				})
			},
			touch: func(t *testing.T, f refusalLoopFixture) {
				writeRepoFile(t, f.root, "briefs/item-a.md", "# Doc\n\n## the-item\n\namended body\n\n## other\n\nother text\n")
			},
		},
		{
			name: "body-ref-file",
			setup: func(t *testing.T, f refusalLoopFixture) {
				writeRepoFile(t, f.root, "briefs/item-a.md", "# Doc\n\n## the-item\n\ninitial body\n\n## other\n\nother text\n")
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET body_ref = 'briefs/item-a.md#the-item' WHERE item_id = 'item-a'`)
					return err
				})
			},
			touch: func(t *testing.T, f refusalLoopFixture) {
				writeRepoFile(t, f.root, "briefs/item-a.md", "# Doc\n\n## the-item\n\ninitial body\n\n## other\n\namended other section\n")
			},
		},
		{
			name: "design-doc",
			setup: func(t *testing.T, f refusalLoopFixture) {
				writeRepoFile(t, f.root, "briefs/item-a.md", "# item-a\n\nbody\n")
				writeRepoFile(t, f.root, "docs/design/spec.md", "# Spec\n\nsettled\n")
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET design_doc = 'docs/design/spec.md' WHERE item_id = 'item-a'`)
					return err
				})
			},
			touch: func(t *testing.T, f refusalLoopFixture) {
				writeRepoFile(t, f.root, "docs/design/spec.md", "# Spec\n\nnew open question\n")
			},
		},
		{
			name: "workflow-definition",
			setup: func(t *testing.T, f refusalLoopFixture) {
				writeRepoFile(t, f.root, "briefs/item-a.md", "# item-a\n\nbody\n")
				writeRepoFile(t, f.root, "config/tillr/workflows/custom@v1.json",
					`{"name":"custom","version":1,"stages":[{"id":"work","kind":"agent","seat":"build","access":"write"}],"edges":[]}`)
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET seat = 'build', workflow = 'custom@v1' WHERE item_id = 'item-a'`)
					return err
				})
			},
			touch: func(t *testing.T, f refusalLoopFixture) {
				writeRepoFile(t, f.root, "config/tillr/workflows/custom@v1.json",
					`{"name":"custom","version":1,"stages":[{"id":"work","kind":"agent","seat":"build","access":"write","prompt":"updated"}],"edges":[]}`)
			},
		},
		{
			name: "allowed-fence",
			setup: func(t *testing.T, f refusalLoopFixture) {
				writeRepoFile(t, f.root, "briefs/item-a.md", "# item-a\n\nbody\n")
				writeRepoFile(t, f.root, "tools/item-a/code.go", "package itema\n")
			},
			touch: func(t *testing.T, f refusalLoopFixture) {
				writeRepoFile(t, f.root, "tools/item-a/code.go", "package itema\n// sibling change inside fence\n")
			},
		},
		{
			name: "derived-pack-fence",
			setup: func(t *testing.T, f refusalLoopFixture) {
				writeRepoFile(t, f.root, "briefs/item-a.md", "# item-a\n\nbody\n")
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET allowed_paths = '["tools/tillr-drain/"]' WHERE item_id = 'item-a'`)
					return err
				})
				writeRepoFile(t, f.root, "yolo-packs/tillr/files/local/share/tillr/tillr-drain/mirror.go", "package mirror\n")
			},
			touch: func(t *testing.T, f refusalLoopFixture) {
				writeRepoFile(t, f.root, "yolo-packs/tillr/files/local/share/tillr/tillr-drain/mirror.go", "package mirror\n// touched companion\n")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
			l.cfg.Policy.SeatLists = map[string][]string{"build": {"fixture"}}
			tc.setup(t, f)
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "setup")

			ctx := context.Background()
			var result LoopResult
			base, err := l.refreshDocuments(ctx, &result)
			if err != nil {
				t.Fatal(err)
			}
			plan := mustPlan(t, l)[0]
			if plan.decision.Action != ActionDispatch {
				t.Fatalf("planned %+v, want dispatch", plan.decision)
			}

			tc.touch(t, f)
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "concurrent landing touching item")

			before := eventCount(t, f.storePath)
			generation := ""
			trunk := base
			ok, work, err := l.applyPlanTrunk(ctx, plan, &trunk, l.cfg.Now(), &generation)
			if err != nil || ok || work != nil {
				t.Fatalf("touched dispatch applied: ok=%v work=%+v err=%v", ok, work, err)
			}
			if trunk != base {
				t.Fatalf("cancelled dispatch mutated pass trunk: got %s, want %s", trunk, base)
			}
			if generation != "" {
				t.Fatalf("cancelled dispatch allocated generation %q", generation)
			}
			assertOnlyDispatchDecline(t, f.storePath, before, plan.decision, trunkMovedDecline)
		})
	}
}

func TestMidPassTrunkRewriteCancelsStart(t *testing.T) {
	f, l := passFixture(t, "item-a")
	writeRepoFile(t, f.root, "briefs/item-a.md", "# item-a\n\nbody\n")
	writeRepoFile(t, f.root, "unrelated.txt", "one\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "one")

	ctx := context.Background()
	var result LoopResult
	base, err := l.refreshDocuments(ctx, &result)
	if err != nil {
		t.Fatal(err)
	}
	plan := mustPlan(t, l)[0]

	// Rewrite trunk (non-fast-forward) with an unrelated file change.
	writeRepoFile(t, f.root, "unrelated.txt", "rewritten\n")
	runGit(t, f.root, "add", "unrelated.txt")
	runGit(t, f.root, "commit", "--amend", "-qm", "rewritten")

	before := eventCount(t, f.storePath)
	generation := ""
	ok, work, err := l.applyPlanGeneration(ctx, plan, base, l.cfg.Now(), &generation)
	if err != nil || ok || work != nil {
		t.Fatalf("dispatch applied across non-fast-forward trunk rewrite: ok=%v work=%+v err=%v", ok, work, err)
	}
	if generation != "" {
		t.Fatalf("cancelled dispatch allocated generation %q", generation)
	}
	assertOnlyDispatchDecline(t, f.storePath, before, plan.decision, trunkMovedDecline)
}
