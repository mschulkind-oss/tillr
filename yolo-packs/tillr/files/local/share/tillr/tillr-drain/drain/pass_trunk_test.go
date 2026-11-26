package drain

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// A new plan sees a landing after document refresh, but an existing plan keeps its tip.
func TestDispatchStartsWhenTrunkMovesAfterDocumentRefresh(t *testing.T) {
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
			want := base
			if movement == "before-plan" {
				want = runGit(t, f.root, "rev-parse", TrunkRef)
			}
			generation := ""
			ok, work, err := l.applyPlanGeneration(ctx, plan, base, l.cfg.Now(), &generation)
			if err != nil || !ok || work == nil || work.BaseSHA != want || generation == "" {
				t.Fatalf("dispatch at decided tip %s: applied=%v work=%+v generation=%q error=%v", want, ok, work, generation, err)
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

type midPassLandingSteps struct {
	recordingSteps
	finishFirst <-chan struct{}
}

func (s *midPassLandingSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if req.ItemID == "item-a" {
		select {
		case <-s.finishFirst:
		case <-ctx.Done():
			return run.Record{}, "", ctx.Err()
		}
	}
	return s.recordingSteps.Run(ctx, req)
}

func TestMidPassSiblingLandingStartsUnrelatedItemOnDecidedTrunk(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	ctx := context.Background()
	writeRepoFile(t, f.root, "briefs/item-a.md", "# item-a\n\ndo item-a\n")
	writeRepoFile(t, f.root, "briefs/item-b.md", "# item-b\n\ndo item-b\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "seed briefs")
	initialTrunk := runGit(t, f.root, "rev-parse", TrunkRef)

	finishFirst := make(chan struct{})
	defer func() {
		select {
		case <-finishFirst:
		default:
			close(finishFirst)
		}
	}()
	steps := &midPassLandingSteps{finishFirst: finishFirst}
	l.cfg.Steps = steps
	var report bytes.Buffer
	l.cfg.Report = &report
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

	// Finish item-a's run bracket while item-b's claim has released the writer
	// to gather Git inputs, after it has already folded pending history.
	finishedDuringClaim := false
	writeGatheredHooks.Fetch = func(string) {
		if siblingTrunk == "" || finishedDuringClaim {
			return
		}
		close(finishFirst)
		select {
		case c := <-l.completions:
			l.completions <- c
			if c.itemID != "item-a" || c.outcome.err != nil || c.outcome.stop != "" {
				t.Fatalf("unexpected item-a completion: %+v", c)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var clean bool
				err := r.QueryRow(`SELECT json_extract(payload, '$.clean') FROM events
					WHERE kind=? AND json_extract(payload, '$.runs[0].run_id')=?`, BoundaryCheckKind, c.id).Scan(&clean)
				if err != nil || !clean {
					t.Fatalf("item-a did not record a clean boundary check: clean=%v err=%v", clean, err)
				}
				return nil
			})
			finishedDuringClaim = true
		case <-time.After(hangGuard):
			t.Fatal("item-a did not finish while item-b gathered its claim")
		}
	}
	t.Cleanup(func() { writeGatheredHooks.Fetch = nil })

	var result LoopResult
	stop, err := l.pass(ctx, &result)
	if err != nil || stop != "" {
		t.Fatalf("pass stopped: stop=%q err=%v", stop, err)
	}
	if siblingTrunk == "" || siblingTrunk == initialTrunk {
		t.Fatalf("sibling landing did not advance trunk: initial=%s sibling=%s", initialTrunk, siblingTrunk)
	}
	writeGatheredHooks.Fetch = nil
	drainOwned(t, l, &result)
	if !finishedDuringClaim {
		t.Fatal("item-a's bracket did not finish during item-b's claim")
	}

	if !steps.ran("item-a") || !steps.ran("item-b") {
		t.Fatalf("unrelated start cancelled mid-pass: ran item-a=%v item-b=%v\n%s", steps.ran("item-a"), steps.ran("item-b"), report.String())
	}
	if got := stager.staged["item-a"]; got != initialTrunk {
		t.Fatalf("item-a staged at %s, want initial trunk %s", got, initialTrunk)
	}
	if got := stager.staged["item-b"]; got != initialTrunk {
		t.Fatalf("item-b staged at %s, want decided trunk %s", got, initialTrunk)
	}
	if head := runGit(t, l.laneTarget("lane-2"), "rev-parse", "HEAD"); head != initialTrunk {
		t.Fatalf("lane-2 worktree HEAD=%s, want decided trunk %s", head, initialTrunk)
	}
	raw, err := os.ReadFile(filepath.Join(l.laneDirPath("lane-2"), BaseSHAFile))
	if err != nil || strings.TrimSpace(string(raw)) != initialTrunk {
		t.Fatalf("lane-2 base_sha=%q err=%v, want %s", string(raw), err, initialTrunk)
	}
}

func TestMidPassLandingTouchingInputsOrFenceKeepsDecidedStart(t *testing.T) {
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
			l.cfg.QuestionsCmd = []string{"sh", "-c", `printf '%s\n' '{"index":{"documents":[{"path":"docs/design/spec.md","stage":"DESIGN","questions":[]}]}}'`}
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

			generation := ""
			trunk := base
			ok, work, err := l.applyPlanTrunk(ctx, plan, &trunk, l.cfg.Now(), &generation)
			if err != nil || !ok || work == nil || work.BaseSHA != base || trunk != base || generation == "" {
				t.Fatalf("dispatch at decided tip %s: ok=%v work=%+v trunk=%s generation=%q err=%v", base, ok, work, trunk, generation, err)
			}
		})
	}
}

func TestDecidedStartKeepsBlockBaseAcrossTrunkAndGreenAdvance(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	ctx := context.Background()
	l.gatePolicy.LaneBase = "green"
	commit := func(body string) string {
		writeRepoFile(t, f.root, "briefs/item-a.md", "# item-a\n\n"+body+"\n")
		runGit(t, f.root, "add", "briefs/item-a.md")
		runGit(t, f.root, "commit", "-qm", body)
		return runGit(t, f.root, "rev-parse", TrunkRef)
	}
	green := commit("old block")
	runGit(t, f.root, "update-ref", GreenRef, green)
	carrier := commit("decided block")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "unrelated landing")
	observed := runGit(t, f.root, "rev-parse", TrunkRef)
	plan := mustPlan(t, l)[0]

	latest := commit("later block")
	runGit(t, f.root, "update-ref", GreenRef, latest)
	ok, work, err := l.applyPlan(ctx, plan, observed, l.cfg.Now())
	if err != nil || !ok || work == nil || work.BaseSHA != carrier {
		t.Fatalf("start must keep its block carrier %s: ok=%v work=%+v err=%v", carrier, ok, work, err)
	}
	for _, fresh := range mustPlan(t, l) {
		if fresh.decision.ItemID != "item-b" || fresh.decision.Action != ActionDispatch {
			continue
		}
		ok, work, err = l.applyPlan(ctx, fresh, observed, l.cfg.Now())
		if err != nil || !ok || work == nil || work.BaseSHA != latest {
			t.Fatalf("new start must use new green %s: ok=%v work=%+v err=%v", latest, ok, work, err)
		}
		return
	}
	t.Fatal("item-b was not offered a start")
}

func TestUncommittedBodyEditStillCancelsStart(t *testing.T) {
	f, l := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	plan := mustPlan(t, l)[0]
	writeRepoFile(t, f.root, "briefs/item-a.md", "# item-a\n\nedited after planning\n")
	before := eventCount(t, f.storePath)
	ok, work, err := l.applyPlan(context.Background(), plan, base, l.cfg.Now())
	if err != nil || ok || work != nil {
		t.Fatalf("start with edited body was claimed: ok=%v work=%+v err=%v", ok, work, err)
	}
	assertOnlyDispatchDecline(t, f.storePath, before, plan.decision, "run inputs changed since planning")
}

func TestMidPassTrunkRewriteDoesNotCancelStart(t *testing.T) {
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

	generation := ""
	ok, work, err := l.applyPlanGeneration(ctx, plan, base, l.cfg.Now(), &generation)
	if err != nil || !ok || work == nil || work.BaseSHA != base || generation == "" {
		t.Fatalf("dispatch at decided tip %s across rewrite: ok=%v work=%+v generation=%q err=%v", base, ok, work, generation, err)
	}
}
