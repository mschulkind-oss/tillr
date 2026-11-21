package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/redcap"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// revertSteps is the loop's steps for the automatic revert: a judge over the checkout,
// a fast gate with a fixed answer, a lander that moves the trunk the way tillr-land
// does (refusing a moved trunk it cannot squash onto) and records the landing row, and
// the item face's return of a landed item.
type revertSteps struct {
	heavyTestSteps
	root, store string
	gate        string
	mu          *sync.Mutex
	gated       *[]Candidate
	returned    *[]string
	required    map[string]string
	requiredLog string
}

func (s revertSteps) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	s.mu.Lock()
	*s.gated = append(*s.gated, c)
	s.mu.Unlock()
	if s.gate != "" {
		return GateOutcome{Outcome: s.gate, FailingChecks: []string{"fast"}}, nil
	}
	return GateOutcome{Outcome: GatePassed}, nil
}
func (s revertSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	tip, err := heavyGitOutput(ctx, s.root, "rev-parse", TrunkRef)
	if err != nil {
		return LandOutcome{}, err
	}
	if tip != c.BaseSHA {
		return LandOutcome{Result: LandSquashConflict}, nil
	}
	if _, err := heavyGitOutput(ctx, s.root, "update-ref", "refs/heads/"+TrunkRef, c.HeadSHA, tip); err != nil {
		return LandOutcome{}, err
	}
	if err := writeFixture(s.store, func(tx *store.Tx) error {
		return recordFixtureLanding(tx, c.ItemID, c.RunID, c.HeadSHA, 100)
	}); err != nil {
		return LandOutcome{}, err
	}
	return LandOutcome{Result: LandLanded, TrunkAfter: c.HeadSHA}, nil
}
func (s revertSteps) ReturnLanded(ctx context.Context, itemID, runID, reason string) error {
	s.mu.Lock()
	*s.returned = append(*s.returned, itemID+": "+reason)
	s.mu.Unlock()
	return writeFixture(s.store, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, StateInQueue, itemID)
		return err
	})
}
func (s revertSteps) JudgeRequired(_ context.Context, _ string, id, _ string) (HeavyCheck, error) {
	return HeavyCheck{CheckID: id, Result: s.required[id], Detail: "required output", LogPath: s.requiredLog}, nil
}

func writeFixture(path string, fn func(*store.Tx) error) error {
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(context.Background(), fn)
}

// revertHarness is a repository whose pointer is green at base, a store, and a loop with
// automatic revert on.
type revertHarness struct {
	t                *testing.T
	root, base, path string
	loop             *Loop
	steps            *revertSteps
	report           *bytes.Buffer
	seq              int
}

// fileRed judges "unit" red while any of the named files exists in the checkout.
func fileRed(names ...string) func(context.Context, string, string) ([]HeavyCheck, error) {
	return func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		result := "pass"
		for _, name := range names {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				result = "fail"
			}
		}
		return []HeavyCheck{{CheckID: "unit", Result: result, Detail: "unit output: " + result, Reads: []string{"a", "b"}}}, nil
	}
}

func newRevertHarness(t *testing.T, judge func(context.Context, string, string) ([]HeavyCheck, error)) *revertHarness {
	t.Helper()
	root, base := heavyRepo(t)
	heavyGit(t, root, "config", "user.name", "Fixture")
	heavyGit(t, root, "config", "user.email", "fixture@example.invalid")
	heavyGit(t, root, "update-ref", GreenRef, base)
	s, err := store.InitFixture(context.Background(), filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	path := s.Path()
	s.Close()
	steps := &revertSteps{heavyTestSteps: heavyTestSteps{judge: judge}, root: root, store: path, mu: &sync.Mutex{}, gated: &[]Candidate{}, returned: &[]string{}}
	report := &bytes.Buffer{}
	l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge", AutoRevert: true}, cfg: LoopConfig{Root: root, Store: path, LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: report}}
	return &revertHarness{t: t, root: root, base: base, path: path, loop: l, steps: steps, report: report}
}

// land commits one file as a recorded landing of item.
func (h *revertHarness) land(item, name, content string) string {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.root, name), []byte(content), 0644); err != nil {
		h.t.Fatal(err)
	}
	heavyGit(h.t, h.root, "add", ".")
	heavyGit(h.t, h.root, "commit", "-qm", "feat: "+item)
	sha := heavyGit(h.t, h.root, "rev-parse", "HEAD")
	h.seq++
	if err := writeFixture(h.path, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO items(item_id,title,state,rank,updated_at) VALUES(?,?,?,?,0)`, item, item, StateLanded, item); err != nil {
			return err
		}
		return recordFixtureLanding(tx, item, "run-"+item, sha, h.seq)
	}); err != nil {
		h.t.Fatal(err)
	}
	return sha
}

func (h *revertHarness) proof() {
	h.t.Helper()
	tip := heavyGit(h.t, h.root, "rev-parse", TrunkRef)
	h.loop.heavyAttempted = ""
	if err := h.loop.startHeavy(context.Background(), tip); err != nil {
		h.t.Fatal(err)
	}
	h.loop.heavyWG.Wait()
}

func (h *revertHarness) query(q string, args ...any) []string {
	h.t.Helper()
	s, err := store.OpenRead(context.Background(), h.path)
	if err != nil {
		h.t.Fatal(err)
	}
	defer s.Close()
	var out []string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	}); err != nil {
		h.t.Fatal(err)
	}
	return out
}

func (h *revertHarness) reverts() []revertPayload {
	h.t.Helper()
	var out []revertPayload
	for _, raw := range h.query(`SELECT payload FROM events WHERE kind=? ORDER BY seq`, RevertKind) {
		var p revertPayload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func (h *revertHarness) fixers() []string {
	return h.query(`SELECT item_id FROM items WHERE item_id LIKE 'fixer-main-%' ORDER BY item_id`)
}

// A red the bisection names one landing for is reverted on the tip: the revert applies
// cleanly, passes the fast gate, and the failing check passes with it, so it lands with
// no fixer. The landing's item is back with its worker, carrying the check's output and
// the check as a required one, and the next proof is green past the revert, which
// releases every hold.
func TestHeavyRedRevertsTheLandingAndTheTrunkIsGreenAgain(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	culprit := h.land("item-broke", "a", "broken\n")
	tip := h.land("item-later", "c", "unrelated\n")
	h.proof()
	reverts := h.reverts()
	if len(reverts) != 1 || reverts[0].Result != "reverted" || reverts[0].Landing != culprit || reverts[0].ItemID != "item-broke" || reverts[0].Tip != tip {
		t.Fatalf("reverts=%+v", reverts)
	}
	trunk := heavyGit(t, h.root, "rev-parse", TrunkRef)
	if reverts[0].Revert != trunk {
		t.Fatalf("the revert %s is not the trunk %s", reverts[0].Revert, trunk)
	}
	if files := heavyGit(t, h.root, "ls-tree", "--name-only", trunk); strings.Contains(files, "a\n") || !strings.Contains(files, "c") {
		t.Fatalf("the revert did not take out exactly the culprit: %q", files)
	}
	if subject := heavyGit(t, h.root, "show", "-s", "--format=%s%n%b", trunk); !strings.HasPrefix(subject, "revert: feat: item-broke") || !strings.Contains(subject, "This reverts commit "+culprit) {
		t.Fatalf("revert message: %s", subject)
	}
	if green := heavyGit(t, h.root, "rev-parse", GreenRef); green != trunk {
		t.Fatalf("the proof after the revert left the pointer at %s, want %s", green, trunk)
	}
	if f := h.fixers(); len(f) != 0 {
		t.Fatalf("a reverted red filed fixers %v", f)
	}
	if len(*h.steps.returned) != 1 || !strings.HasPrefix((*h.steps.returned)[0], "item-broke: landing "+culprit+" reverted: unit") {
		t.Fatalf("returned=%v", *h.steps.returned)
	}
	gated := *h.steps.gated
	if len(gated) != 1 || gated[0].ItemID != RevertItemID(culprit) || gated[0].BaseSHA != tip {
		t.Fatalf("gated=%+v", gated)
	}
	ending := h.query(`SELECT payload FROM events WHERE kind=? AND object_id=?`, RefusalKind, "run-item-broke")
	if len(ending) != 1 {
		t.Fatalf("endings=%v", ending)
	}
	var p refusalPayload
	if err := json.Unmarshal([]byte(ending[0]), &p); err != nil {
		t.Fatal(err)
	}
	if p.Step != RefusalReverted || len(p.Checks) != 1 || p.Checks[0].CheckID != "unit" || !strings.Contains(p.Detail, "unit output: fail") || p.Evidence == "" {
		t.Fatalf("ending=%+v", p)
	}
	ids, _, err := h.loop.requiredChecks(context.Background(), "item-broke")
	if err != nil || len(ids) != 1 || ids[0] != "unit" {
		t.Fatalf("required=%v %v", ids, err)
	}
	if hold := redcapHold(t, h.path, h.root, trunk); hold != nil {
		t.Fatalf("a green trunk still holds: %+v", hold)
	}
	log := h.report.String()
	for _, line := range []string{
		"loop: heavy unit red from landing " + shortSHA(culprit) + " (item item-broke); reverting it on tip " + shortSHA(tip),
		"loop: reverted " + shortSHA(culprit) + " as " + shortSHA(trunk) + "; unit passes on the tip with it",
		"loop: reverted " + shortSHA(culprit) + ", item-broke back with its author",
		"loop: heavy run green at " + shortSHA(trunk),
	} {
		if !strings.Contains(log, line) {
			t.Errorf("log lacks %q:\n%s", line, log)
		}
	}
	// Nothing is counted: a second red from another landing is reverted the same way.
	second := h.land("item-broke-again", "b", "broken\n")
	h.steps.judge = fileRed("a", "b")
	h.proof()
	if r := h.reverts(); len(r) != 2 || r[1].Landing != second || r[1].Result != "reverted" {
		t.Fatalf("second red: %+v", r)
	}
	if f := h.fixers(); len(f) != 0 {
		t.Fatalf("fixers=%v", f)
	}
}

func redcapHold(t *testing.T, path, root, tip string) *redcap.Hold {
	t.Helper()
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var hold *redcap.Hold
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		hold, err = redcap.Check(context.Background(), r, root, TrunkRef, tip, "item-ordinary", time.Now().Add(24*time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return hold
}

// fixerNotes answers the criteria text of the one fixer filed.
func (h *revertHarness) fixerText(id string) string {
	return strings.Join(h.query(`SELECT text FROM item_criteria WHERE item_id=?`, id), "\n")
}

// Each way the revert path ends "not possible" files the fixer, with the reason in its
// brief, and leaves the trunk where it was.
func TestHeavyRevertNotPossibleFilesTheFixer(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(h *revertHarness) (culprit, against string)
		reason string
	}{
		{"does not apply", func(h *revertHarness) (string, string) {
			culprit := h.land("item-broke", "a", "broken\n")
			h.land("item-later", "a", "broken further\n")
			return culprit, culprit
		}, "does not apply cleanly"},
		{"still red with the landing reverted", func(h *revertHarness) (string, string) {
			culprit := h.land("item-broke", "a", "broken\n")
			tip := h.land("item-later", "b", "also broken\n")
			return culprit, tip
		}, "still fails on the tip"},
		{"a revert is not reverted", func(h *revertHarness) (string, string) {
			culprit := h.land(RevertItemID("0123456789abcdef"), "a", "broken\n")
			return culprit, culprit
		}, "itself an automatic revert"},
		{"a fixer's landing is not reverted", func(h *revertHarness) (string, string) {
			culprit := h.land(TrunkFixerID("0123456789abcdef"), "a", "broken\n")
			return culprit, culprit
		}, "is trunk fixer"},
		{"a landed dependent", func(h *revertHarness) (string, string) {
			culprit := h.land("item-broke", "a", "broken\n")
			h.land("item-dependent", "c", "builds on it\n")
			if err := writeFixture(h.path, func(tx *store.Tx) error {
				_, err := tx.Exec(`INSERT INTO item_edges(item_id,blocked_by) VALUES('item-dependent','item-broke')`)
				return err
			}); err != nil {
				h.t.Fatal(err)
			}
			return culprit, culprit
		}, "landed item item-dependent declares a dependency on item-broke"},
		{"the fast gate refuses", func(h *revertHarness) (string, string) {
			h.steps.gate = GateFailed
			h.loop.cfg.Steps = h.steps
			culprit := h.land("item-broke", "a", "broken\n")
			return culprit, culprit
		}, "the fast gate did not pass with the revert: fail (fast)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRevertHarness(t, fileRed("a", "b"))
			culprit, against := tc.setup(h)
			before := heavyGit(t, h.root, "rev-parse", TrunkRef)
			h.proof()
			if after := heavyGit(t, h.root, "rev-parse", TrunkRef); after != before {
				t.Fatalf("a refused revert moved the trunk %s -> %s", before, after)
			}
			r := h.reverts()
			if len(r) != 1 || r[0].Result != "not-possible" || r[0].Landing != culprit || !strings.Contains(r[0].Reason, tc.reason) {
				t.Fatalf("reverts=%+v, want reason %q", r, tc.reason)
			}
			fixers := []string{}
			for _, id := range h.fixers() {
				// The fixture's own fixer landing is an item row too; it is not filed here.
				if id != TrunkFixerID("0123456789abcdef") {
					fixers = append(fixers, id)
				}
			}
			if len(fixers) != 1 || fixers[0] != TrunkFixerID(against) {
				t.Fatalf("fixers=%v, want one against %s", fixers, shortSHA(against))
			}
			if text := h.fixerText(fixers[0]); !strings.Contains(text, "Automatic revert of "+shortSHA(culprit)+" for unit was not possible") || !strings.Contains(text, tc.reason) {
				t.Fatalf("fixer brief lacks the reason: %s", text)
			}
			if len(*h.steps.returned) != 0 {
				t.Fatalf("a refused revert returned %v", *h.steps.returned)
			}
			if !strings.Contains(h.report.String(), "automatic revert not possible: ") {
				t.Fatalf("log: %s", h.report.String())
			}
		})
	}
}

// A red with no landing named (red at the pointer too) is never reverted: the fixer is
// filed against the tip, as with automatic revert off.
func TestHeavyUnattributedRedIsNotReverted(t *testing.T) {
	h := newRevertHarness(t, fileRed("value"))
	tip := h.land("item-innocent", "c", "unrelated\n")
	h.proof()
	if r := h.reverts(); len(r) != 0 {
		t.Fatalf("an unattributed red was offered for revert: %+v", r)
	}
	if f := h.fixers(); len(f) != 1 || f[0] != TrunkFixerID(tip) {
		t.Fatalf("fixers=%v, want one against the tip", f)
	}
	if len(*h.steps.gated) != 0 {
		t.Fatalf("gated=%v", *h.steps.gated)
	}
}

// The returned item's next gate includes the check its revert named, until it lands
// again: a candidate that still fails it is refused back to its worker with the output.
func TestRevertedItemsGateRunsTheFailingCheck(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	h.land("item-broke", "a", "broken\n")
	h.land("item-later", "c", "unrelated\n")
	h.proof()
	h.steps.required = map[string]string{"unit": "fail"}
	h.loop.cfg.Steps = h.steps
	// The fast gate passed, but the required replay found two failing cases. Its
	// source log is temporary; the recorded refusal must keep its own copy.
	source := filepath.Join(scratchUnderDurable(t), "required-unit.log")
	output := "fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:\n" +
		"  triage unit: required unit tests\n" +
		"       case: TestRequiredReplayKeepsFirstFailure -- tests/unit_test.go\n" +
		"       assertion: first required regression failed\n" +
		"       case: TestRequiredReplayKeepsSecondFailure -- tests/unit_test.go\n" +
		"       assertion: second required regression failed\n"
	if err := os.WriteFile(source, []byte(output), 0600); err != nil {
		t.Fatal(err)
	}
	h.steps.requiredLog = source
	candidate := Candidate{ItemID: "item-broke", RunID: "run-required-replay", LaneID: "lane-1", Worktree: h.root}
	runDir := RunDir(h.root, candidate.RunID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, GateVerdictRecord), []byte(`{"payload":{"result":"pass","checks":[]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	detail, err := h.loop.judgeRequired(context.Background(), &candidate)
	if err != nil || !strings.Contains(detail, "unit failed") || !strings.Contains(detail, "required output") {
		t.Fatalf("detail=%q err=%v", detail, err)
	}
	candidate.RefusalDetail = detail
	if err := h.loop.recordRefusal(context.Background(), candidate, RefusalGate); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	endings := h.query(`SELECT payload FROM events WHERE kind=? AND object_id=?`, RefusalKind, candidate.RunID)
	if len(endings) != 1 {
		t.Fatalf("required replay endings=%v", endings)
	}
	var ending refusalPayload
	if err := json.Unmarshal([]byte(endings[0]), &ending); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TestRequiredReplayKeepsFirstFailure", "TestRequiredReplayKeepsSecondFailure"} {
		if !strings.Contains(ending.Detail, name) {
			t.Errorf("required replay refusal lost failing case %q: %s", name, ending.Detail)
		}
	}
	if strings.Contains(ending.Detail, "the gate produced no usable failure verdict") {
		t.Errorf("required replay refusal contradicts its passing fast gate: %s", ending.Detail)
	}
	if len(ending.Checks) != 1 || ending.Checks[0].CheckID != "unit" {
		t.Errorf("required replay refusal lost its check: %+v", ending.Checks)
	} else {
		check := ending.Checks[0]
		if len(check.Failing) != 1 || len(check.Failing[0].Cases) != 2 ||
			check.Failing[0].Cases[0].Name != "TestRequiredReplayKeepsFirstFailure" ||
			check.Failing[0].Cases[1].Name != "TestRequiredReplayKeepsSecondFailure" {
			t.Errorf("required replay refusal lost structured failing cases: %+v", check.Failing)
		}
		rel, err := filepath.Rel(runDir, check.LogRef)
		if check.LogRef == "" || err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("required replay log is not retained under RunDir %s: %q (%v)", runDir, check.LogRef, err)
		}
		if !strings.Contains(ending.Detail, check.LogRef) {
			t.Errorf("required replay refusal did not name the retained log: %s", ending.Detail)
		}
		if body, err := os.ReadFile(check.LogRef); err != nil || string(body) != output {
			t.Errorf("required replay log did not survive source removal: %q (%v)", body, err)
		}
	}
	h.steps.required = map[string]string{"unit": "pass"}
	if detail, err := h.loop.judgeRequired(context.Background(), &Candidate{ItemID: "item-broke", Worktree: h.root}); err != nil || detail != "" {
		t.Fatalf("a passing required check refused: %q %v", detail, err)
	}
	// Unproven is never red: no verdict does not refuse.
	h.steps.required = map[string]string{"unit": "error"}
	if detail, err := h.loop.judgeRequired(context.Background(), &Candidate{ItemID: "item-broke", Worktree: h.root}); err != nil || detail != "" {
		t.Fatalf("a check with no verdict refused: %q %v", detail, err)
	}
	// Once the item lands again the requirement is spent.
	h.seq = 1000
	h.land("item-broke", "d", "redone\n")
	if ids, _, err := h.loop.requiredChecks(context.Background(), "item-broke"); err != nil || len(ids) != 0 {
		t.Fatalf("required after relanding=%v %v", ids, err)
	}
}

// The red cap never holds a revert, as it never holds a fixer.
func TestRedCapExemptsARevert(t *testing.T) {
	if !redcap.Exempt("main", RevertItemID("0123456789abcdef")) || redcap.Exempt("main", "item-ordinary") {
		t.Fatal("the red cap exemption does not cover reverts only")
	}
}

// The reverted ending goes back to the worker as a returned ending with its own words.
func TestRevertedEndingIsReturnedWithItsBrief(t *testing.T) {
	if returnedReasons[ReasonLandingReverted] != RefusalReverted || !returnedCauses[RefusalReverted] {
		t.Fatal("the reverted ending is not a returned cause")
	}
	brief := returnedBrief(ReturnedEnding{RunID: "run-x", Cause: RefusalReverted, Detail: "Your landing abc was reverted", Checks: []EndingCheck{{CheckID: "unit", LogRef: "/logs/unit.log"}}})
	for _, want := range []string{"tillr reverted it", "Your next gate runs the failing check", "Your landing abc was reverted", "`unit`: /logs/unit.log"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief lacks %q:\n%s", want, brief)
		}
	}
}

// revertedBlock is item-broke's document block, with the given first criterion.
func revertedBlock(criterionText string) item.SyncDocument {
	return item.SyncDocument{Path: "docs/broke.md", Content: []byte("# Doc\n\n### Break it\n\n<!-- tillr: item id=\"item-broke\" paths=\"a\" -->\n\n- **c1:** " + criterionText + "\n- **c2:** A test covers it.\n")}
}

// syncBlock applies doc to the harness store the way a landed design answer does.
func (h *revertHarness) syncBlock(doc item.SyncDocument) item.SyncReport {
	h.t.Helper()
	var report item.SyncReport
	if err := writeFixture(h.path, func(tx *store.Tx) error {
		var err error
		report, err = item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{Partial: true, Documents: []item.SyncDocument{doc}, At: time.Now().UTC()})
		return err
	}); err != nil {
		h.t.Fatal(err)
	}
	return report
}

// judgedLanded declares item-broke from its block and records it judged and landed, as a
// landing leaves it: every criterion done.
func (h *revertHarness) judgedLanded() {
	h.t.Helper()
	h.syncBlock(revertedBlock("It works."))
	if err := writeFixture(h.path, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE item_criteria SET state = 'done' WHERE item_id = 'item-broke'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = 'item-broke'`, StateLanded)
		return err
	}); err != nil {
		h.t.Fatal(err)
	}
}

// A revert withdraws what the reverted landing settled: the item's criterion states are
// cleared with a record naming the landing and the revert, so a design answer that then
// amends the block (its first criterion, here) syncs instead of being refused definition-frozen,
// the hold item-gate-stall-watch-without-perf-counter met on 2026-11-18.
func TestARevertWithdrawsItsLandingsVerdictsAndADesignAnswerAfterItSyncs(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	h.judgedLanded()
	culprit := h.land("item-broke", "a", "broken\n")
	h.land("item-later", "c", "unrelated\n")
	h.proof()
	reverts := h.reverts()
	if len(reverts) != 1 || reverts[0].Result != "reverted" {
		t.Fatalf("reverts=%+v", reverts)
	}
	if states := h.query(`SELECT criterion_id || '=' || state FROM item_criteria WHERE item_id = 'item-broke' ORDER BY criterion_id`); strings.Join(states, ",") != "c1=,c2=" {
		t.Fatalf("criterion states after the revert: %v", states)
	}
	withdrawn := h.query(`SELECT payload FROM events WHERE kind = ? AND object_id = 'item-broke'`, item.VerdictsWithdrawnKind)
	if len(withdrawn) != 1 || !strings.Contains(withdrawn[0], culprit) || !strings.Contains(withdrawn[0], reverts[0].Revert) || !strings.Contains(withdrawn[0], `"by":"tillr-drain"`) || !strings.Contains(withdrawn[0], `"state":"done"`) {
		t.Fatalf("withdrawal record: %v", withdrawn)
	}
	report := h.syncBlock(revertedBlock("It works after the repair."))
	if len(report.Frozen) != 0 || strings.Join(report.Updated, ",") != "item-broke" {
		t.Fatalf("the design answer after the revert did not sync: updated %v frozen %+v", report.Updated, report.Frozen)
	}
	if texts := h.query(`SELECT text FROM item_criteria WHERE item_id = 'item-broke' AND criterion_id = 'c1'`); len(texts) != 1 || texts[0] != "It works after the repair." {
		t.Fatalf("criterion text=%v", texts)
	}
	// Repeating the rule records nothing more.
	if err := h.loop.withdrawRevertedVerdicts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := h.query(`SELECT count(*) FROM events WHERE kind = ?`, item.VerdictsWithdrawnKind); n[0] != "1" {
		t.Fatalf("withdrawals=%v", n)
	}
}

// An item a revert returned before the withdrawal existed (the two live items of
// 2026-11-18) is repaired by the next document sync, which a loop start always runs: its
// verdicts are withdrawn and its amended block applies. A verdict recorded after the
// revert (the item was judged again) is left alone.
func TestTheNextSyncWithdrawsVerdictsAnEarlierRevertLeftStanding(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	h.judgedLanded()
	// The build of 2026-11-18 recorded the revert, returned the item and withdrew nothing.
	if err := h.loop.appendRevert(context.Background(), revertPayload{Landing: "0123456789abcdef", ItemID: "item-broke", RunID: "run-item-broke", Tip: "fedcba9876543210", Checks: []string{"unit"}, Result: "reverted", Revert: "aaaabbbbccccdddd"}, nil); err != nil {
		t.Fatal(err)
	}
	h.exec(`UPDATE items SET state = ? WHERE item_id = 'item-broke'`, StateInQueue)
	if report := h.syncBlock(revertedBlock("It works after the repair.")); len(report.Frozen) != 1 {
		t.Fatalf("the fixture is not the live state: frozen %+v", report.Frozen)
	}
	h.loop.cfg.Steps = syncedRevertSteps{h.steps}
	if err := h.loop.syncDocuments(context.Background(), &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if states := h.query(`SELECT state FROM item_criteria WHERE item_id = 'item-broke' AND state != ''`); len(states) != 0 {
		t.Fatalf("the next sync left the reverted verdicts standing: %v", states)
	}
	if !strings.Contains(h.report.String(), "loop: withdrew the verdicts of reverted landings: item-broke (0123456789ab)") {
		t.Fatalf("the repair is not reported:\n%s", h.report.String())
	}
	if report := h.syncBlock(revertedBlock("It works after the repair.")); len(report.Frozen) != 0 || len(report.Updated) != 1 {
		t.Fatalf("after the repair: updated %v frozen %+v", report.Updated, report.Frozen)
	}
	if texts := h.query(`SELECT text FROM item_criteria WHERE item_id = 'item-broke' AND criterion_id = 'c1'`); len(texts) != 1 || texts[0] != "It works after the repair." {
		t.Fatalf("criterion text=%v", texts)
	}
	// A verdict recorded after the revert stands.
	h.exec(`UPDATE item_criteria SET state = 'done' WHERE item_id = 'item-broke'`)
	if err := writeFixture(h.path, func(tx *store.Tx) error {
		id := "item-broke"
		_, err := tx.Append(event.Event{Kind: item.VerdictRecordKind, ObjectType: "item", ObjectID: &id, Payload: []byte(`{}`), At: time.Now()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.loop.withdrawRevertedVerdicts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if states := h.query(`SELECT state FROM item_criteria WHERE item_id = 'item-broke' AND state = 'done'`); len(states) != 2 {
		t.Fatalf("a verdict recorded after the revert was withdrawn: %v", states)
	}
}

func (h *revertHarness) exec(q string, args ...any) {
	h.t.Helper()
	if err := writeFixture(h.path, func(tx *store.Tx) error {
		_, err := tx.Exec(q, args...)
		return err
	}); err != nil {
		h.t.Fatal(err)
	}
}

// syncedRevertSteps answers the document sync with nothing to report.
type syncedRevertSteps struct{ *revertSteps }

func (syncedRevertSteps) Sync(context.Context) (SyncOutcome, error) { return SyncOutcome{}, nil }
