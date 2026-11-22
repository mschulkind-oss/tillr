package drain_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

// failingGateBody writes the two records a red gate leaves behind and exits 1. The failing
// check is `unit`, which reads `src/`; the plan's watch set is what the fixer's fence takes
// its second half from.
const failingGateBody = `dir=$(dirname "$TILLR_GATE_SPANS_OUT")
mkdir -p "$dir"
printf '%s\n' '{"record_version":1,"kind":"gate.verdict","id":"verdict-1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-13T06:21:56Z","payload":{"plan_id":"plan-1","registry_digest":"d1","base_sha":"b1","tree_sha":"t1","result":"fail","checks":[{"check_id":"unit","result":"fail","duration_ms":1,"log_ref":""}]}}' > "$dir/gate.verdict.json"
printf '%s\n' '{"record_version":1,"kind":"gate.plan","id":"plan-1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-13T06:21:56Z","payload":{"plan_id":"plan-1","base_sha":"b1","tree_sha":"t1","registry_digest":"d1","checks":[{"check_id":"unit","kind":"unit","label":"the unit suite","reads":["src/"],"writes":[]}],"in_process":[]}}' > "$dir/gate.plan.json"
exit 1
`

// trunkSteps is a real ProcessSteps with the two optional capabilities stubbed: the base
// replay and the rolling tier. Everything else — the run, the gate, the land, the verdict —
// is the loop's real path, so a case proves the wiring and not a second implementation.
type trunkSteps struct {
	*drain.ProcessSteps
	mu sync.Mutex
	// replays maps a revision to the replay's answer. A revision with no entry replays
	// green.
	replays map[string]drain.ReplayOutcome
	// rolling is the queue of rolling answers; empty answers green.
	rolling []drain.RollingOutcome

	replayCalls  []string
	rollingCalls []string
	// beforeReplay, when set, runs once before the first replay: a case moves the trunk
	// there, the way other landings move it while a run's candidate waits to be judged.
	beforeReplay func()
}

func (s *trunkSteps) Land(ctx context.Context, c drain.Candidate) (drain.LandOutcome, error) {
	return (&evidenceProcessSteps{s.ProcessSteps}).Land(ctx, c)
}
func (s *trunkSteps) Verdict(ctx context.Context, c drain.Candidate, verdict string) error {
	return (&evidenceProcessSteps{s.ProcessSteps}).Verdict(ctx, c, verdict)
}

func (s *trunkSteps) Replay(_ context.Context, _ drain.Candidate, rev string, checks []string) (drain.ReplayOutcome, error) {
	s.mu.Lock()
	if hook := s.beforeReplay; hook != nil {
		s.beforeReplay = nil
		hook()
	}
	s.replayCalls = append(s.replayCalls, rev)
	out, ok := s.replays[rev]
	s.mu.Unlock()
	if ok {
		if out.Rev == "" {
			out.Rev = rev
		}
		return out, nil
	}
	return drain.ReplayOutcome{Rev: rev, Known: true}, nil
}

func (s *trunkSteps) RollingConfigured() bool { return true }

func (s *trunkSteps) Rolling(_ context.Context, base, tip string) (drain.RollingOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollingCalls = append(s.rollingCalls, base+".."+tip)
	if len(s.rolling) == 0 {
		return drain.RollingOutcome{Green: true, Known: true}, nil
	}
	out := s.rolling[0]
	s.rolling = s.rolling[1:]
	return out, nil
}

func (s *trunkSteps) calls() (replays, rollings []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.replayCalls...), append([]string{}, s.rollingCalls...)
}

// withSeats maps the build seat the fixer asks for onto the fixture backend.
func (f loopFixture) withSeats() loopFixture {
	policy := *f.policy
	policy.Seats = map[string]string{"build": "fixture"}
	f.policy = &policy
	return f
}

// trunkStager also provisions desks that were not among the fixture's initial lanes.
type trunkStager struct {
	passStager
	t    *testing.T
	root string
}

func (s trunkStager) Stage(ctx context.Context, spec drain.StageSpec) error {
	target := filepath.Join(s.laneRoot, spec.LaneID, "worktree")
	if _, err := os.Stat(target); os.IsNotExist(err) {
		git(s.t, s.root, "worktree", "add", "--detach", target, spec.BaseSHA)
	} else if err != nil {
		return err
	}
	return s.passStager.Stage(ctx, spec)
}

func TestTrunkStagerProvisionsNewFixerDesk(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	base := git(t, f.root, "rev-parse", "main")
	target := filepath.Join(f.laneRoot, "desk-trunk-fixer", "worktree")
	stager := trunkStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, t: t, root: f.root}
	if err := stager.Stage(context.Background(), drain.StageSpec{LaneID: "desk-trunk-fixer", WorkingDir: "worktree", BaseSHA: base}); err != nil {
		t.Fatal(err)
	}
	if head := git(t, target, "rev-parse", "HEAD"); head != base {
		t.Fatalf("desk head=%s want=%s", head, base)
	}
}

// runTrunkLoop drives the loop with the trunk capabilities stubbed, and answers the result
// and the steps so a case can read what ran.
func runTrunkLoop(t *testing.T, f loopFixture, steps *trunkSteps, report io.Writer) (drain.LoopResult, error) {
	t.Helper()
	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
		Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{laneName(1)},
		Policy: f.policy, Steps: steps,
		Stage: trunkStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, t: t, root: f.root},
		Now:   steps.Now, Report: report,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	return loop.Run(context.Background())
}

// itemExists answers whether the store holds an item row.
func itemExists(t *testing.T, storePath, itemID string) bool {
	t.Helper()
	s, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	var found int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM items WHERE item_id = ?`, itemID).Scan(&found)
	}); err != nil {
		t.Fatalf("reading item %q failed: %v", itemID, err)
	}
	return found > 0
}

// refusalOf answers the first `drain.refusal` event whose step is the one named.
func refusalOf(t *testing.T, storePath, step string) map[string]any {
	t.Helper()
	for _, e := range eventsOf(t, storePath) {
		if e.Kind != drain.RefusalKind {
			continue
		}
		payload := eventPayload(t, e)
		if payload["step"] == step {
			return payload
		}
	}
	return nil
}

// rollingStateOf reads the one rolling state row.
func rollingStateOf(t *testing.T, storePath string) (watermark string, landings int) {
	t.Helper()
	s, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT watermark_sha, landings_since FROM rolling_state WHERE id = 1`).Scan(&watermark, &landings)
	}); err != nil {
		t.Fatalf("reading the rolling state: %v", err)
	}
	return watermark, landings
}

// A gate failure whose failing checks are red on the candidate's base and whose reads
// intersect the candidate's changed paths is the trunk's failure: the item is acquitted
// with no failing verdict, held blocked, and one fixer is filed for the red sha.
func TestAGateFailureRedOnTheBaseAcquitsTheItem(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	base := git(t, f.root, "rev-parse", "main")
	// Check `unit` reads `candidate.txt` (intersecting committingAdapter's change).
	intersectingGate := strings.ReplaceAll(failingGateBody, `"reads":["src/"]`, `"reads":["candidate.txt"]`)
	gate := stub(t, "gate", intersectingGate)
	verdict := always(t, "verdict", 0)
	steps := &trunkSteps{
		ProcessSteps: f.steps(t, gate, always(t, "land", 0), verdict),
		replays:      map[string]drain.ReplayOutcome{base: {Red: []string{"unit"}, Known: true}},
	}
	result, err := runTrunkLoop(t, f, steps, io.Discard)
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.TrunkAcquitted != 1 {
		t.Fatalf("the loop acquitted %d candidate(s), want 1 (stop %q: %s)", result.TrunkAcquitted, result.Stop, result.Detail)
	}
	if result.Landed != 0 {
		t.Fatalf("an acquitted item landed %d candidate(s)", result.Landed)
	}
	// The verdict program is what retires a criterion: an acquittal must never reach it.
	if countMatching(lines(t, f.count), "verdict") != 0 {
		t.Fatalf("an acquitted candidate was given a verdict: %v", lines(t, f.count))
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "blocked" {
		t.Fatalf("the acquitted item is %q, want blocked", got)
	}
	fixer := drain.TrunkFixerID(base)
	if !itemExists(t, f.store, fixer) {
		t.Fatalf("no fixer %q was filed for the red trunk", fixer)
	}
	// The fixer is real work: the loop dispatches it, and when its own candidate repeats a
	// refusal it waits on its mover like any other item. Either way it was filed and kept.
	if got := itemStateOf(t, f.store, fixer); got != "in-queue" && got != "blocked" {
		t.Fatalf("the fixer is %q, want in-queue or blocked", got)
	}
	refusal := refusalOf(t, f.store, drain.RefusalTrunkAlreadyRed)
	if refusal == nil {
		t.Fatalf("no trunk-already-red refusal was recorded: %v", kinds(eventsOf(t, f.store)))
	}
	if refusal["red_sha"] != base {
		t.Fatalf("the refusal names red_sha %v, want %s", refusal["red_sha"], base)
	}
	if refusal["fixer_item"] != fixer {
		t.Fatalf("the refusal names fixer_item %v, want %s", refusal["fixer_item"], fixer)
	}
}

// A gate failure red on the candidate's base but green on the trunk tip names a repair that
// already happened: the trunk moved past the base while the candidate waited. No fixer is
// filed for either commit, the lane is brought onto the tip, and the ordinary refusal goes
// back to the worker saying the trunk is repaired there (the owner's ruling of 2026-11-18:
// work is always built against the latest trunk).
func TestAGateFailureRedOnlyOnAnOldBaseFilesNoFixer(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	base := git(t, f.root, "rev-parse", "main")
	var tip string
	steps := &trunkSteps{
		ProcessSteps: f.steps(t, stub(t, "gate", failingGateBody), always(t, "land", 0), always(t, "verdict", 0)),
		replays:      map[string]drain.ReplayOutcome{base: {Red: []string{"unit"}, Known: true}},
		beforeReplay: func() {
			if err := os.WriteFile(filepath.Join(f.root, "repair.txt"), []byte("repaired\n"), 0o644); err != nil {
				t.Error(err)
			}
			git(t, f.root, "add", "repair.txt")
			git(t, f.root, "commit", "-qm", "repair the trunk")
			tip = git(t, f.root, "rev-parse", "main")
		},
	}
	var report strings.Builder
	result, err := runTrunkLoop(t, f, steps, &report)
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.TrunkAcquitted != 0 {
		t.Fatalf("a base the trunk has repaired acquitted the item: %d", result.TrunkAcquitted)
	}
	for _, sha := range []string{base, tip} {
		if itemExists(t, f.store, drain.TrunkFixerID(sha)) {
			t.Fatalf("a fixer was filed for %s; the trunk is repaired at %s", sha, tip)
		}
	}
	refusal := refusalOf(t, f.store, drain.RefusalGate)
	if refusal == nil {
		t.Fatalf("no gate refusal was recorded: %v", kinds(eventsOf(t, f.store)))
	}
	detail, _ := refusal["detail"].(string)
	if !strings.Contains(detail, "repaired at "+tip[:12]) || !strings.Contains(detail, "rebased this lane") {
		t.Fatalf("the refusal does not send the worker to the tip: %q\n%s", detail, report.String())
	}
	session, _ := refusal["worker_session"].(map[string]any)
	if session != nil && session["base_sha"] != tip {
		t.Fatalf("the returned session keeps base %v, want the tip %s", session["base_sha"], tip)
	}
}

// A gate failure whose failing checks are green on the base is the candidate's: the
// ordinary gate refusal stands, no fixer is filed, and the item stays in the queue. The
// replay is what makes the difference between the two, so a loop that could not replay
// must land here and never on the acquittal above.
func TestAGateFailureGreenOnTheBaseIsTheCandidatesRefusal(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	base := git(t, f.root, "rev-parse", "main")
	steps := &trunkSteps{
		ProcessSteps: f.steps(t, stub(t, "gate", failingGateBody), always(t, "land", 0), always(t, "verdict", 0)),
		replays:      map[string]drain.ReplayOutcome{},
	}
	result, err := runTrunkLoop(t, f, steps, io.Discard)
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.TrunkAcquitted != 0 {
		t.Fatalf("a green base acquitted the item: %d", result.TrunkAcquitted)
	}
	// The refusal goes back to the item's worker with the evidence, and the identical
	// second refusal waits on its mover (the owner's ruling of 2026-11-17).
	if result.Rejected != 2 || result.Landed != 0 {
		t.Fatalf("the loop rejected %d and landed %d, want 2 and 0", result.Rejected, result.Landed)
	}
	if !strings.Contains(result.Detail, "item_041 ("+drain.ReasonConflictOwner+")") {
		t.Fatalf("the repeated refusal does not wait on its mover: %s", result.Detail)
	}
	if itemExists(t, f.store, drain.TrunkFixerID(base)) {
		t.Fatal("a green base filed a fixer")
	}
	if refusal := refusalOf(t, f.store, drain.RefusalGate); refusal == nil {
		t.Fatalf("no ordinary gate refusal was recorded: %v", kinds(eventsOf(t, f.store)))
	}
	if _, replayCalls := steps.calls(); len(replayCalls) == 0 {
		t.Fatal("the loop did not replay the failing checks at all")
	}
}

// A fixer must repair the check it names, even when that check is red on its base.
// Only unrelated gate failures are eligible for base replay.
func TestAFixerIsNeverAcquittedByItsOwnBase(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	base := git(t, f.root, "rev-parse", "main")
	fixer := drain.TrunkFixerID(base)
	fixtureAt(t, f.store, envelope("item.record", "item-"+fixer, map[string]any{
		"item_id": fixer, "title": "fix unit", "state": "in-queue", "seat": "build",
		"blocked_by": []string{}, "allowed_paths": []string{"candidate.txt"},
		"criteria": []map[string]string{{
			"criterion_id": "trunk-green", "text": "the full gate is green (red: unit)", "state": "not-done",
		}},
	}))
	steps := &trunkSteps{
		ProcessSteps: f.steps(t, stub(t, "gate", failingGateBody), always(t, "land", 0), always(t, "verdict", 0)),
		replays:      map[string]drain.ReplayOutcome{base: {Red: []string{"unit"}, Known: true}},
	}
	result, err := runTrunkLoop(t, f, steps, io.Discard)
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Rejected == 0 || result.Landed != 0 || result.TrunkAcquitted != 0 {
		t.Fatalf("the fixer's own red check did not refuse it: %+v", result)
	}
	if replayCalls, _ := steps.calls(); len(replayCalls) != 0 {
		t.Fatalf("a fixer's gate failure was replayed on its base: %v", replayCalls)
	}
}

// Under OQ-GP7, an open fixer does not hold the ordinary queue from starting and
// running: every item may start and run while trunk is red (only landing waits on path
// intersection). The fixer itself is also dispatchable.
func TestAnOpenFixerHoldsTheOrdinaryQueue(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	base := git(t, f.root, "rev-parse", "main")
	fixer := drain.TrunkFixerID(base)
	fixtureAt(t, f.store,
		itemRecordSeated(fixer, "blocked", "build"),
		itemRecord("item_041", "in-queue"),
	)
	steps := &trunkSteps{ProcessSteps: f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))}
	result, err := runTrunkLoop(t, f, steps, io.Discard)
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("the loop stopped %q, want %q", result.Stop, drain.StopQueueDrained)
	}
	if result.Landed != 1 {
		t.Fatalf("item_041 did not run and land while fixer open: landed=%d", result.Landed)
	}
	if len(lines(t, f.count)) == 0 {
		t.Fatalf("the adapter did not run while fixer was open")
	}
}

// A red rolling run files the same fixer a gate acquittal would, and advances no
// watermark: the watermark is the newest trunk commit the rolling tier has proved, and a
// red range proves nothing.
func TestARedRollingRunFilesTheFixerAndHoldsTheQueue(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	base := git(t, f.root, "rev-parse", "main")
	steps := &trunkSteps{
		ProcessSteps: f.steps(t, stub(t, "gate", failingGateBody), always(t, "land", 0), always(t, "verdict", 0)),
		rolling: []drain.RollingOutcome{{
			Known: false, Green: false, FailingChecks: []string{"unit"}, SuspectPaths: []string{"candidate.txt"},
		}},
	}
	steps.rolling[0].Known = true
	result, err := runTrunkLoop(t, f, steps, io.Discard)
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.RollingRed != 1 || result.RollingGreen != 0 {
		t.Fatalf("rolling green=%d red=%d, want 0 and 1", result.RollingGreen, result.RollingRed)
	}
	fixer := drain.TrunkFixerID(base)
	if !itemExists(t, f.store, fixer) {
		t.Fatalf("a red rolling run filed no fixer %q", fixer)
	}
	if watermark, _ := rollingStateOf(t, f.store); watermark != "" {
		t.Fatalf("a red rolling run advanced the watermark to %q", watermark)
	}
}

// A single red check creates exactly one fixer; a machine-local inability to
// judge creates none, even if a stale span still names a failing check.
func TestRollingFilesExactlyOneFixerOnlyForAJudgedRedCheck(t *testing.T) {
	for _, tc := range []struct {
		name  string
		known bool
		want  int
	}{
		{"red check", true, 1},
		{"could not run here", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
			steps := &trunkSteps{
				ProcessSteps: f.steps(t, stub(t, "gate", failingGateBody), always(t, "land", 0), always(t, "verdict", 0)),
				rolling:      []drain.RollingOutcome{{Known: tc.known, FailingChecks: []string{"unit"}, SuspectPaths: []string{"candidate.txt"}}},
			}
			result, err := runTrunkLoop(t, f, steps, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			s, err := store.OpenRead(context.Background(), f.store)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var count int
			err = s.ReadTx(context.Background(), func(r *store.Reader) error {
				return r.QueryRow(`SELECT count(*) FROM items WHERE item_id LIKE ?`, drain.TrunkFixerPrefix+"%").Scan(&count)
			})
			if err != nil || count != tc.want || result.RollingRed != tc.want {
				t.Fatalf("fixers=%d rolling red=%d, want %d; error=%v", count, result.RollingRed, tc.want, err)
			}
		})
	}
}

// A green rolling run advances the watermark to the tip it judged and resets the landing
// count. Nothing else moves it.
func TestAGreenRollingRunAdvancesTheWatermark(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	base := git(t, f.root, "rev-parse", "main")
	steps := &trunkSteps{ProcessSteps: f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))}
	result, err := runTrunkLoop(t, f, steps, io.Discard)
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.RollingGreen != 1 || result.RollingRed != 0 {
		t.Fatalf("rolling green=%d red=%d, want 1 and 0", result.RollingGreen, result.RollingRed)
	}
	watermark, landings := rollingStateOf(t, f.store)
	if watermark != base {
		t.Fatalf("the watermark is %q, want the judged tip %q", watermark, base)
	}
	if landings != 0 {
		t.Fatalf("the landing count is %d after a green run, want 0", landings)
	}
}
