package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// A brace pair the renderer meets is one of two different faults, and the fault decides who
// fixes it. The template's own residue is the template's — it refuses every item staged from
// it, and the operator edits tools/tillr-run/backends/brief-template.md. A brace pair that
// arrived inside an item's field is the record's, and the operator edits the item. Both used
// to be one message ("the brief ... still carries a '{{'"), which sent an operator to the
// template for a bad criterion.
//
// The table is one case per field because the attribution is the whole point: a renderer
// that names "a value" is not different from the one this replaces, and only a case per
// field proves the field is read rather than inferred.
func TestConflictCodeWithBracePairsSurvivesBriefPreflight(t *testing.T) {
	markers := "### tools/tillr-drain/drain/pass.go\n<<<<<<< trunk\nrows := []row{{ID: 1}}\n=======\nrows := []row{{ID: 2}}\n>>>>>>> candidate\n// Literal template-shaped evidence: {{TITLE}}\n"
	recorded := refusalStagedItem("item-a")
	spec := refusalSpec(recorded.ItemID)
	spec.Conflict = &ConflictBrief{Markers: markers, HeadSHA: strings.Repeat("1", 40), TrunkSHA: strings.Repeat("2", 40), OriginalRunID: "run-original"}
	values, err := briefValuesFor(recorded, spec, "/wt", "lane worktree", strings.Repeat("0", 40), "/lane/durable", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	facts := values.byName()["FACTS"]
	markerPath := filepath.Join("/lane/durable", "conflict-markers.txt")
	if !strings.Contains(facts, markerPath) || strings.Contains(facts, "{{") {
		t.Fatalf("conflict facts must reference evidence without embedding code: %s", facts)
	}
	rendered, err := renderBrief(writeTemplate(t, refusalTemplate(values)), recorded.ItemID, values)
	if err != nil {
		t.Fatalf("rendering conflict code: %v", err)
	}
	brief := filepath.Join(scratchUnderDurable(t), "brief.md")
	// Use all required sections to exercise the real adapter's preflight as well.
	if err := os.WriteFile(brief, []byte(strings.Replace(refusalBrief("resolve the conflict"), "## Facts carried\n\nnone", "## Facts carried\n\n"+facts, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, markerPath) {
		t.Fatal("rendered brief lost conflict evidence reference")
	}
	preflight, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "preflight.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `. "$1"; PROG=probe; preflight_brief "$2"`, "preflight", preflight, brief)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("adapter refused conflict evidence: %v: %s", err, out)
	}
}

func TestABracePairInAnItemFieldIsReportedAgainstThatField(t *testing.T) {
	for _, c := range []struct {
		name   string
		itemID string
		item   func(stagedItem) stagedItem
		field  string
	}{
		{
			name: "the item id", itemID: "item-{{x}}", field: "item_id",
			item: func(s stagedItem) stagedItem { return s },
		},
		{
			name: "the title", field: "title",
			item: func(s stagedItem) stagedItem { s.Title = "a {{x}} title"; return s },
		},
		{
			name: "the body reference", field: "body_ref",
			item: func(s stagedItem) stagedItem { s.BodyRef = "briefs/{{x}}.md"; return s },
		},
		{
			name: "a criterion", field: "criteria",
			item: func(s stagedItem) stagedItem {
				s.Criteria[0].Text = "the thing {{x}} happens"
				return s
			},
		},
		{
			name: "an allowed path", field: "allowed_paths",
			item: func(s stagedItem) stagedItem { s.AllowedPaths = []string{"tools/{{x}}/"}; return s },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			recorded := c.item(refusalStagedItem("item-a"))
			if c.itemID != "" {
				recorded.ItemID = c.itemID
			}
			values, err := briefValuesFor(recorded, refusalSpec(recorded.ItemID), "/wt", "lane worktree", strings.Repeat("0", 40), "/lane/durable", time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("building the brief's values failed before the brace pair could be seen: %v", err)
			}
			templatePath := writeTemplate(t, refusalTemplate(values))
			_, err = renderBrief(templatePath, recorded.ItemID, values)
			if err == nil {
				t.Fatalf("the renderer accepted a %s carrying a brace pair, so the brief it would launch has an unfilled placeholder", c.field)
			}
			var content *ItemContentError
			if !errors.As(err, &content) {
				t.Fatalf("a brace pair in the %s was classified as infrastructure (%T), so the loop would back the whole queue off for one record: %v", c.field, err, err)
			}
			if content.ItemID != recorded.ItemID || content.Field != c.field {
				t.Fatalf("the refusal names item %q field %q, want %q and %q", content.ItemID, content.Field, recorded.ItemID, c.field)
			}
			if !strings.Contains(err.Error(), recorded.ItemID) || !strings.Contains(err.Error(), c.field) {
				t.Fatalf("the refusal does not name both the item and the field: %v", err)
			}
			if strings.Contains(err.Error(), templatePath) {
				t.Fatalf("the refusal for the %s blames the template, so an operator would edit the wrong file: %v", c.field, err)
			}
		})
	}
}

// The other direction: a template that is itself broken is the template's fault, it is
// refused naming the template, and it is not an item-content refusal — so the loop treats it
// as infrastructure and backs off rather than holding every item staged from it.
func TestABrokenTemplateIsRefusedNamingTheTemplate(t *testing.T) {
	recorded := refusalStagedItem("item-a")
	values, err := briefValuesFor(recorded, refusalSpec(recorded.ItemID), "/wt", "lane worktree", strings.Repeat("0", 40), "/lane/durable", time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("building the brief's values failed: %v", err)
	}
	// `{{not-a-placeholder}}` is a brace pair the template carries that no value can fill:
	// the name is not the uppercase form staging substitutes.
	templatePath := writeTemplate(t, refusalTemplate(values)+"\n{{not-a-placeholder}}\n")
	_, err = renderBrief(templatePath, recorded.ItemID, values)
	if err == nil {
		t.Fatalf("the renderer accepted a template whose own brace pair no value can fill")
	}
	if IsItemContent(err) {
		t.Fatalf("a broken template was classified as the item's own fault, so the loop would hold every item staged from it: %v", err)
	}
	if ExitCode(err) != record.ExitExternal {
		t.Fatalf("a broken template exited %d, want the external class %d: %v", ExitCode(err), record.ExitExternal, err)
	}
	if !strings.Contains(err.Error(), templatePath) {
		t.Fatalf("the refusal does not name the template %s: %v", templatePath, err)
	}
}

// The adapters' own preflight draws the same line, because an operator reads its refusal too
// and must not be sent to a template that is not at fault. The shell file is run as the
// adapters run it — sourced, with `preflight_brief` called on a brief — and not re-spelled
// here, so the case fails if the two spellings drift apart.
func TestTheAdaptersPreflightTellsATemplateFaultFromAnItemFault(t *testing.T) {
	preflight, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "preflight.sh"))
	if err != nil {
		t.Fatalf("resolving the adapters' preflight path failed: %v", err)
	}
	if _, err := os.Stat(preflight); err != nil {
		t.Fatalf("the adapters' preflight %s is not there: %v", preflight, err)
	}
	for _, c := range []struct {
		name  string
		value string
		want  string
	}{
		{"an unfilled template placeholder", "a title {{TITLE}} never filled", "backends/brief-template.md is the thing to fix"},
		{"a literal brace pair from an item field", "a title with {{x}} in it", "the item's record is the thing to fix"},
	} {
		t.Run(c.name, func(t *testing.T) {
			brief := filepath.Join(t.TempDir(), "brief.md")
			if err := os.WriteFile(brief, []byte(refusalBrief(c.value)), 0o644); err != nil {
				t.Fatalf("writing the brief failed: %v", err)
			}
			cmd := exec.Command("sh", "-c", `. "$1"; PROG=probe; preflight_brief "$2"`, "preflight", preflight, brief)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 2 {
				t.Fatalf("the preflight over %s answered %v, want exit 2: %s", c.name, err, out)
			}
			if !strings.Contains(string(out), c.want) {
				t.Fatalf("the refusal for %s does not carry %q: %s", c.name, c.want, out)
			}
		})
	}
}

// refusalBrief is a brief with all ten of the template's sections and the given line in the
// Task section, so the section check cannot fire before the brace-pair check does.
func refusalBrief(task string) string {
	return "# a brief\n\n## Header\n\n- **Item:** item-a\n\n## Task\n\n" + task + "\n\n" +
		"## Fence\n\n- tools/item-a/\n\n## Seats\n\nnone\n\n## Settled decisions\n\nnone\n\n" +
		"## Facts carried\n\nnone\n\n## Method\n\nnone\n\n## Verification commands\n\nnone\n\n" +
		"## Report shape\n\nnone\n\n## Stop conditions\n\nnone\n"
}

// An item the item model itself cannot describe — no title, no criterion, no fence — is
// refused as the record's fault too, because staging it again cannot succeed.
func TestAnItemWithNoTitleOrCriterionOrFenceIsRefusedAgainstItsOwnField(t *testing.T) {
	cases := []struct {
		name   string
		field  string
		damage func(*stagedItem)
	}{
		{"no title", "title", func(s *stagedItem) { s.Title = "" }},
		{"no criterion", "criteria", func(s *stagedItem) { s.Criteria = nil }},
		{"no fence", "allowed_paths", func(s *stagedItem) { s.AllowedPaths = nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recorded := refusalStagedItem("item-a")
			c.damage(&recorded)
			_, err := briefValuesFor(recorded, refusalSpec(recorded.ItemID), "/wt", "lane worktree", strings.Repeat("0", 40), "/lane/durable", time.Now())
			if err == nil {
				t.Fatalf("staging accepted an item with %s", c.name)
			}
			var content *ItemContentError
			if !errors.As(err, &content) {
				t.Fatalf("an item with %s was not refused as the record's own fault: %v", c.name, err)
			}
			if content.Field != c.field || content.ItemID != recorded.ItemID {
				t.Fatalf("the refusal names item %q field %q, want %q and %q", content.ItemID, content.Field, recorded.ItemID, c.field)
			}
		})
	}
}

// The loop's half. One item's record carries a brace pair in its criterion, so staging
// refuses it; its sibling is staged and dispatched anyway, on the same tick, and the refused
// item is held per item — recorded as a `drain.refusal` keyed by the item, with the reason —
// rather than counted as an infrastructure failure that backs the whole loop off.
//
// The negative control is the counterfactual this replaces: with the refusal classified as
// infrastructure, `InfraFailures` is 1 and the batch is unchanged, so the assertions below
// fail on the count and on the hold even though the sibling happens to dispatch.
func TestAnItemItsOwnRecordRefusesIsHeldAndItsSiblingDispatches(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	steps := &recordingSteps{}
	loop := f.loop(t, steps, &itemContentStager{refusing: "item-a"})
	var result LoopResult
	stop, err := loop.pass(context.Background(), &result)
	if len(loop.active) > 0 {
		completion := <-loop.completions
		loop.takeCompletion(completion, &result)
	}
	if err != nil {
		t.Fatalf("dispatching the tick failed: %v", err)
	}
	if stop != "" {
		t.Fatalf("the tick stopped %q, want it to continue past one held item", stop)
	}
	if !steps.ran("item-b") {
		t.Fatalf("the sibling was not dispatched on the same tick: a single unstagleable item stalled the queue")
	}
	if steps.ran("item-a") {
		t.Fatalf("the item whose record cannot be staged was dispatched anyway")
	}
	if result.InfraFailures != 0 {
		t.Fatalf("the item's own refusal counted %d infrastructure failure(s), so the loop would back the whole queue off for one record", result.InfraFailures)
	}
	if loop.infraStreak != 0 {
		t.Fatalf("the item's own refusal left an infrastructure streak of %d", loop.infraStreak)
	}
	if got := loop.held["item-a"].reason; got != ReasonItemUnstagleable {
		t.Fatalf("the refused item is held with %q, want %q", got, ReasonItemUnstagleable)
	}
	if _, held := loop.held["item-b"]; held {
		t.Fatalf("the sibling was held too, so the hold is not per item")
	}
	// The refusal is on disk, keyed by the item and naming what has to be edited.
	event := findItemRefusal(t, f.storePath, "item-a")
	if event.Reason != ReasonItemUnstagleable || event.Step != RefusalStaging {
		t.Fatalf("the refusal reads step=%q reason=%q, want %q and %q",
			event.Step, event.Reason, RefusalStaging, ReasonItemUnstagleable)
	}
	if !strings.Contains(event.Detail, "criteria") {
		t.Fatalf("the refusal does not name the field to edit: %q", event.Detail)
	}
}

// A staging refusal changes the next plan's position and identity, not a stored
// dispatch's payload. Replanning the unchanged record repeats an unwritten hold.
func TestStagingRefusalPlansADifferentIdentityWithoutRecordingAHold(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	steps := &recordingSteps{}
	loop := f.loop(t, steps, &itemContentStager{refusing: "item-a"})
	loop.cfg.Policy.Backends = map[string]Backend{"fixture": {Adapter: "/bin/true"}}
	ctx := context.Background()
	plans := mustPlan(t, loop)
	if len(plans) != 1 || plans[0].decision.Action != ActionDispatch {
		t.Fatalf("initial plan: %+v", plans)
	}
	original := plans[0].decision
	var result LoopResult
	if _, err := loop.pass(ctx, &result); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := loop.pass(ctx, &result); err != nil {
			t.Fatal(err)
		}
		holds := mustPlan(t, loop)
		if len(holds) != 1 || holds[0].decision.DecisionID == original.DecisionID || holds[0].decision.From != "held" || holds[0].decision.To != "" {
			t.Fatalf("staging hold: %+v", holds)
		}
	}
	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var count int
		if err := r.QueryRow(`SELECT COUNT(*) FROM dispatches`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("failed staging left %d dispatch rows", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A hold is about the record as it was. An item whose bad field an operator has edited is a
// different input, so the hold must drop rather than keep a fixed item out of the queue for
// the life of the process — while a record nobody touched stays held, which is what stops
// the loop re-staging the same refusal on every tick.
func TestTheHoldDropsWhenTheItemRecordIsWrittenAgain(t *testing.T) {
	scratchDir := scratchUnderDurable(t)
	storePath := filepath.Join(scratchDir, "board.sqlite3")
	s, err := store.InitFixture(context.Background(), storePath)
	if err != nil {
		t.Fatalf("creating the fixture store failed: %v", err)
	}
	defer s.Close()
	seedRefusalItem(t, s, "item-a")
	heldAt := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	loop := &Loop{cfg: LoopConfig{Store: storePath}, held: map[string]heldItem{
		"item-a": {reason: ReasonItemUnstagleable, at: heldAt},
	}}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		if reason, held := loop.heldFor(r, "item-a"); !held || reason != ReasonItemUnstagleable {
			t.Fatalf("an unedited record dropped its hold: held=%v reason=%q", held, reason)
		}
		return nil
	}); err != nil {
		t.Fatalf("reading the store failed: %v", err)
	}
	// The operator fixes the item: a new record, written after the refusal.
	seedRefusalItemAt(t, s, "item-a", heldAt.Add(time.Minute))
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		if reason, held := loop.heldFor(r, "item-a"); held {
			t.Fatalf("an edited record is still held as %q, so a fixed item never dispatches again", reason)
		}
		return nil
	}); err != nil {
		t.Fatalf("reading the store failed: %v", err)
	}
	if _, held := loop.held["item-a"]; held {
		t.Fatalf("the dropped hold is still in the loop's map")
	}
}

// The other direction, at the loop's boundary: a staging failure the item's record did not
// cause is still infrastructure. It counts, it feeds the backoff, nothing is held, and the
// whole batch waits — because a template that will not render is the machine's fault and
// retrying it is right.
func TestABrokenTemplateStillFailsTheWholeStageAndBacksOff(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	steps := &recordingSteps{}
	loop := f.loop(t, steps, &brokenTemplateStager{template: "/repo/tools/tillr-run/backends/brief-template.md"})
	var result LoopResult
	stop, err := loop.pass(context.Background(), &result)
	if err != nil {
		t.Fatalf("dispatching the tick failed: %v", err)
	}
	if stop != "" {
		t.Fatalf("the tick stopped %q, want the batch to be reported and retried", stop)
	}
	if len(steps.items()) != 0 {
		t.Fatalf("a batch with no stagable item dispatched %v", steps.items())
	}
	if result.InfraFailures != 2 || loop.infraStreak != 2 {
		t.Fatalf("the broken template counted %d infrastructure failure(s) and a streak of %d, want 2 and 2",
			result.InfraFailures, loop.infraStreak)
	}
	if len(loop.held) != 0 {
		t.Fatalf("a broken template held %v: the item is not the fault", loop.held)
	}
}

// itemContentStager refuses one item as its own record's fault and stages the rest.
type itemContentStager struct{ refusing string }

func (s *itemContentStager) Stage(_ context.Context, spec StageSpec) error {
	if spec.ItemID == s.refusing {
		return ItemContentf(spec.ItemID, "criteria", "item %s: its criteria field carries a literal '{{', which a brief cannot carry. The template is fine, so the record is the thing to fix", spec.ItemID)
	}
	return nil
}

// brokenTemplateStager refuses every item the way a template that will not render does: an
// external failure, which names the template and blames nobody's record.
type brokenTemplateStager struct{ template string }

func (s *brokenTemplateStager) Stage(_ context.Context, _ StageSpec) error {
	return Externalf("the brief template %s carries a '{{' that is not one of its placeholders", s.template)
}

// recordingSteps answers a completed run that committed nothing, and records which items
// reached it. A run that made no commit is enough to prove the dispatch happened: what the
// loop does with a candidate is not this case's subject.
type recordingSteps struct {
	mu  sync.Mutex
	hit map[string]bool
}

func (s *recordingSteps) Run(_ context.Context, request run.Request) (run.Record, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hit == nil {
		s.hit = map[string]bool{}
	}
	s.hit[request.ItemID] = true
	exit := 0
	return run.Record{RunID: request.RunID, Backend: request.Backend, State: run.StateExited, ExitCode: &exit}, "", nil
}

func (s *recordingSteps) ran(itemID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hit[itemID]
}

func (s *recordingSteps) items() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.hit))
	for id := range s.hit {
		out = append(out, id)
	}
	return out
}

func (s *recordingSteps) Gate(_ context.Context, _ Candidate) (GateOutcome, error) {
	return GateOutcome{Outcome: GatePassed}, nil
}
func (s *recordingSteps) Land(_ context.Context, _ Candidate) (LandOutcome, error) {
	return LandOutcome{Result: LandLanded}, nil
}
func (s *recordingSteps) Verdict(_ context.Context, _ Candidate, _ string) error { return nil }
func (s *recordingSteps) ItemState(_ context.Context, _, _, _ string, _ ...item.WaitingOn) error {
	return nil
}
func (s *recordingSteps) Index(_ context.Context) error { return nil }
func (s *recordingSteps) Sync(_ context.Context) (SyncOutcome, error) {
	return SyncOutcome{Skipped: "a recording step set reads no documents"}, nil
}

// refusalLoopFixture is a repository with one worktree per item and a store holding those
// items, which is the minimum a real tick needs: the boundary bracket reads the worktrees,
// and the refusal is written to the store.
type refusalLoopFixture struct {
	root      string
	laneRoot  string
	storePath string
	items     []string
}

func newRefusalLoopFixture(t *testing.T, ids ...string) refusalLoopFixture {
	t.Helper()
	// Fixture commits and the deployed identity check must agree, without
	// writing the owner's shared Git configuration.
	config := filepath.Join(scratchUnderDurable(t), "gitconfig")
	writeForkFile(t, config, "[user]\n name = tillr\n email = tillr@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	root := filepath.Join(scratchUnderDurable(t), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("creating the fixture repository failed: %v", err)
	}
	runGit(t, root, "init", "-q", "-b", TrunkRef)
	writeForkFile(t, filepath.Join(root, "base.txt"), "base\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "base")
	laneRoot := filepath.Join(scratchUnderDurable(t), "lanes")
	for i := range ids {
		lane := fmt.Sprintf("lane-%d", i+1)
		path := filepath.Join(laneRoot, lane, WorktreeDir)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the fixture lane failed: %v", err)
		}
		runGit(t, root, "worktree", "add", "-q", "-b", LaneBranch(laneRoot, lane), path, TrunkRef)
	}
	f := refusalLoopFixture{
		root: root, laneRoot: laneRoot, items: ids,
		storePath: filepath.Join(scratchUnderDurable(t), "board.sqlite3"),
	}
	s, err := store.InitFixture(context.Background(), f.storePath)
	if err != nil {
		t.Fatalf("creating the fixture store failed: %v", err)
	}
	defer s.Close()
	for _, id := range ids {
		seedRefusalItem(t, s, id)
	}
	return f
}

// decisions is the tick's batch: one dispatch per item on its own lane, which is what the
// planner would have produced.
func (f refusalLoopFixture) decisions() []Decision {
	decisions := make([]Decision, 0, len(f.items))
	for i, id := range f.items {
		lane := fmt.Sprintf("lane-%d", i+1)
		decisions = append(decisions, Decision{
			ItemID: id, Action: ActionDispatch, Reason: ReasonReady, LaneID: lane,
			RunRequest: &run.Request{
				RunID: "run-" + id + "-1", ItemID: id, LaneID: lane, Backend: "fixture",
				WorkingDir: WorktreeDir, TimeoutMS: 60000,
			},
		})
	}
	return decisions
}

func (f refusalLoopFixture) loop(t *testing.T, steps Steps, stage Stager) *Loop {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) }
	loop, err := NewLoop(LoopConfig{
		Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, ExitWhenDrained: true,
		Lanes:  []string{"lane-1", "lane-2"},
		Policy: &Policy{DefaultBackend: "fixture", Preference: []string{"fixture"}, Backends: map[string]Backend{"fixture": {Adapter: "fixture"}}},
		Steps:  steps, Stage: stage, Now: now,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	return loop
}

// seedRefusalItem writes one item with a criterion and a fence: the minimum staging reads,
// and the values the loop's hold and the renderer's attribution are about.
func seedRefusalItem(t *testing.T, s *store.Store, itemID string) {
	t.Helper()
	seedRefusalItemAt(t, s, itemID, time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
}

// seedRefusalItemAt writes the item as of one record timestamp, which is the item's
// `updated_at`: the value the hold is compared against.
func seedRefusalItemAt(t *testing.T, s *store.Store, itemID string, at time.Time) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"item_id":       itemID,
		"title":         "the item " + itemID,
		"body_ref":      "briefs/" + itemID + ".md",
		"state":         StateInQueue,
		"blocked_by":    []string{},
		"allowed_paths": []string{"tools/" + itemID + "/"},
		"criteria": []map[string]string{
			{"criterion_id": "c1", "text": "the first criterion of " + itemID, "state": ""},
		},
	})
	if err != nil {
		t.Fatalf("encoding the item failed: %v", err)
	}
	env := record.Envelope{
		RecordVersion: 1,
		Kind:          item.RecordKind,
		ID:            "item-" + itemID,
		Producer:      record.Producer{System: "tillr", Utility: "drain", Version: "0.1.0"},
		CreatedAt:     at,
		Payload:       body,
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := item.Apply(context.Background(), tx, env)
		return err
	}); err != nil {
		t.Fatalf("seeding item %s failed: %v", itemID, err)
	}
}

// findItemRefusal answers the `drain.refusal` the loop wrote for one item.
func findItemRefusal(t *testing.T, storePath, itemID string) refusalPayload {
	t.Helper()
	read := openFixture(t, storePath)
	defer read.Close()
	var found []refusalPayload
	if err := read.ReadTx(context.Background(), func(r *store.Reader) error {
		events, err := r.Events(0)
		if err != nil {
			return err
		}
		for _, e := range events {
			if e.Kind != RefusalKind || e.ObjectType != "item" || e.ObjectID == nil || *e.ObjectID != itemID {
				continue
			}
			var payload refusalPayload
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				return err
			}
			found = append(found, payload)
		}
		return nil
	}); err != nil {
		t.Fatalf("reading the refusal events failed: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("item %s has %d item-keyed refusal event(s), want exactly 1", itemID, len(found))
	}
	return found[0]
}

// refusalStagedItem is a valid item: every field staging needs, so a case that breaks one on
// purpose is testing that field and nothing else.
func refusalStagedItem(itemID string) stagedItem {
	s := stagedItem{
		ItemID:       itemID,
		Title:        "the item " + itemID,
		BodyRef:      "briefs/" + itemID + ".md",
		AllowedPaths: []string{"tools/" + itemID + "/"},
	}
	s.Criteria = append(s.Criteria, struct {
		CriterionID string `json:"criterion_id"`
		Text        string `json:"text"`
	}{CriterionID: "c1", Text: "the first criterion of " + itemID})
	return s
}

func refusalSpec(itemID string) StageSpec {
	return StageSpec{
		LaneID: "lane-1", ItemID: itemID, RunID: "run-" + itemID + "-1",
		Backend: "pi", BaseSHA: strings.Repeat("0", 40), WorkingDir: WorktreeDir, TimeoutMS: 60000,
	}
}

// refusalTemplate spells every placeholder the values cover, so the renderer's `missing`
// check cannot fire before the brace pair the case is about.
func refusalTemplate(values briefValues) string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, "{{"+value.placeholder+"}}")
	}
	return strings.Join(names, "\n") + "\n"
}

func writeTemplate(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brief-template.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing the template failed: %v", err)
	}
	return path
}

// compile-time proof that the fixtures satisfy the interfaces the loop drives, so a
// signature change is a build failure rather than a nil-method panic in a case.
var (
	_ Stager = (*itemContentStager)(nil)
	_ Stager = (*brokenTemplateStager)(nil)
	_ Steps  = (*recordingSteps)(nil)
)
