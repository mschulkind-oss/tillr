package drain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// requestWords carries template braces and a fake suffix heading, which must reach the
// worker byte for byte without tripping the adapter's template checks.
const requestWords = "Remove the {{TASK}} index cache; readers answer from source records.\n## Saved request words (verbatim; 3 bytes)\n## Task\n"

var requestFence = []string{"tools/item-a/", "docs/notes.md"}

// fileFixtureRequest files a request in the fixture store through the filing writer itself.
func fileFixtureRequest(t *testing.T, f refusalLoopFixture, seat, workflow string, paths []string) string {
	t.Helper()
	var id string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		env, err := item.FileRequest(context.Background(), tx, item.RequestFiling{
			Words: []byte(requestWords), Title: "Remove the index cache", Seat: seat, Workflow: workflow, Paths: paths,
			Destination: item.Destination{To: "first"}, At: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), By: "owner", Via: "cli",
		})
		id = env.ID
		return err
	})
	return id
}

// probePreflight runs the adapter's own brief check on a staged brief.
func probePreflight(t *testing.T, brief string) error {
	t.Helper()
	preflight, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "preflight.sh"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", `. "$1"; PROG=probe; preflight_brief "$2"`, "preflight", preflight, brief).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}

// In a document-fed store a filed request has no block and is not sent to triage for one:
// the planner dispatches it on its own seat, and staging writes a brief whose task is the
// request's, whose fence is exactly the filed paths, and which ends with the words verbatim
// in a suffix the adapter's preflight accepts.
func TestARequestIsDispatchedWithItsWordsAndFence(t *testing.T) {
	f, l := generatedFixture(t)
	id := fileFixtureRequest(t, f, "build", item.SingleWorkerWorkflow, []string{"docs/notes.md"})
	d := planFor(t, l, id)
	if d.Action != ActionDispatch || d.Seat != "build" || d.Reason == ReasonTriageNoBlock || d.BlockConflict != nil {
		t.Fatalf("request not dispatched: %+v", d)
	}
	s := &LaneStager{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	spec := StageSpec{ItemID: d.ItemID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, Backend: d.RunRequest.Backend, WorkingDir: WorktreeDir, BaseSHA: runGit(t, f.root, "rev-parse", TrunkRef)}
	if err := s.Stage(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(f.laneRoot, d.LaneID, BriefFile)
	raw, err := os.ReadFile(brief)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Remove the index cache", "filed from the command line as a request", "the loop refuses a candidate that touches one", "No `<!-- tillr: item … -->` comment is added"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("brief lacks %q", want)
		}
	}
	if suffix := requestWordsSuffix([]byte(requestWords)); !strings.HasSuffix(string(raw), suffix) {
		t.Fatalf("brief does not end with the words:\n%s", raw)
	}
	fence, err := os.ReadFile(filepath.Join(f.laneRoot, d.LaneID, FenceFile))
	if err != nil || string(fence) != fenceText([]string{"docs/notes.md"}) {
		t.Fatalf("fence %q %v", fence, err)
	}
	if err := probePreflight(t, brief); err != nil {
		t.Fatalf("the adapter refused the request's brief: %v", err)
	}
	if err := os.WriteFile(brief, append(raw, "extra"...), 0o600); err != nil {
		t.Fatal(err)
	}
	if probePreflight(t, brief) == nil {
		t.Fatal("the adapter accepted a words suffix whose byte count is wrong")
	}
}

// requestSteps runs a two-node request: the read-only scout reports, and the writer
// commits its result. With block set, the writer's first turn also declares an item block.
type requestSteps struct {
	recordingSteps
	t      *testing.T
	f      refusalLoopFixture
	block  bool
	writes int
	briefs []string
	fences []string
	nodes  []string
}

func (s *requestSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	lane := filepath.Join(s.f.laneRoot, req.LaneID)
	target := filepath.Join(lane, WorktreeDir)
	brief, err := os.ReadFile(filepath.Join(lane, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	fence, err := os.ReadFile(filepath.Join(lane, FenceFile))
	if err != nil {
		return run.Record{}, "", err
	}
	s.briefs, s.fences, s.nodes = append(s.briefs, string(brief)), append(s.fences, string(fence)), append(s.nodes, req.NodeID)
	if req.NodeID == "work" {
		s.writes++
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0o755); err != nil {
			return run.Record{}, "", err
		}
		if err := os.WriteFile(filepath.Join(target, "tools/item-a/result.txt"), []byte(fmt.Sprintf("built %d\n", s.writes)), 0o644); err != nil {
			return run.Record{}, "", err
		}
		notes := filepath.Join(target, "docs/notes.md")
		if s.block && s.writes == 1 {
			if err := os.MkdirAll(filepath.Dir(notes), 0o755); err != nil {
				return run.Record{}, "", err
			}
			if err := os.WriteFile(notes, []byte(triageBlock), 0o644); err != nil {
				return run.Record{}, "", err
			}
		} else if _, err := os.Stat(notes); err == nil {
			if err := os.Remove(notes); err != nil {
				return run.Record{}, "", err
			}
		}
		for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "build"}} {
			if out, err := exec.Command("git", append([]string{"-C", target}, args...)...).CombinedOutput(); err != nil {
				return run.Record{}, "", fmt.Errorf("git %v: %v: %s", args, err, out)
			}
		}
	}
	report := "Done.\nRESULT: done\n"
	path := filepath.Join(lane, "report.md")
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return run.Record{}, "", err
	}
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	return rec, report, st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
}

func (s *requestSteps) Land(_ context.Context, c Candidate) (LandOutcome, error) {
	runGit(s.t, s.f.root, "merge", "--squash", c.HeadSHA)
	runGit(s.t, s.f.root, "commit", "-qm", "land request")
	return LandOutcome{Result: LandLanded}, nil
}

func (s *requestSteps) ItemState(ctx context.Context, id, _, state string, _ ...item.WaitingOn) error {
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, state, id)
		return err
	})
}

// runRequestLoop files a scout-then-build request and runs the loop until it lands.
func runRequestLoop(t *testing.T, block bool) (refusalLoopFixture, *requestSteps, *LoopResult, string) {
	t.Helper()
	f, l, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	id := fileFixtureRequest(t, f, "build", "scout-then-build@v1", requestFence)
	steps := &requestSteps{t: t, f: f, block: block}
	l.cfg.Steps = steps
	result := &LoopResult{}
	for i := 0; i < 12 && result.Landed == 0; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	return f, steps, result, id
}

// A request runs its workflow's nodes in order — every node's brief carrying the words and
// its fence the filed paths — and lands through the normal gate with no document block
// anywhere: its record is its declaration.
func TestARequestLandsWithoutADocumentBlock(t *testing.T) {
	f, steps, result, id := runRequestLoop(t, false)
	if result.Landed != 1 || len(steps.nodes) != 2 || steps.nodes[0] != "scout" || steps.nodes[1] != "work" {
		t.Fatalf("landed=%d nodes=%v result=%+v", result.Landed, steps.nodes, result)
	}
	for i, brief := range steps.briefs {
		if !strings.HasSuffix(brief, requestWordsSuffix([]byte(requestWords))) || !strings.Contains(brief, "filed from the command line as a request") {
			t.Errorf("node %s brief lacks the request:\n%s", steps.nodes[i], brief)
		}
		if steps.fences[i] != fenceText(requestFence) {
			t.Errorf("node %s fence %q", steps.nodes[i], steps.fences[i])
		}
	}
	if !strings.Contains(steps.briefs[0], "Run only node scout on the scout seat; access: read_only.") {
		t.Errorf("scout brief:\n%s", steps.briefs[0])
	}
	var state string
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state)
	})
	if state != StateLanded {
		t.Fatalf("request state %q", state)
	}
	if raw, err := os.ReadFile(filepath.Join(f.root, "tools/item-a/result.txt")); err != nil || string(raw) != "built 1\n" {
		t.Fatalf("landed tree: %q %v", raw, err)
	}
}

// A request candidate that declares an item block is refused before the gate, as a fence
// refusal that goes back to the item's worker with the reason; the next turn, without the
// block, lands, and the trunk holds no block.
func TestARequestThatAddsAnItemBlockIsReturnedToItsWorker(t *testing.T) {
	f, steps, result, _ := runRequestLoop(t, true)
	if result.Landed != 1 || result.RefusedSteps[RefusalFence] != 1 || steps.writes != 2 {
		t.Fatalf("landed=%d refused=%v writes=%d nodes=%v", result.Landed, result.RefusedSteps, steps.writes, steps.nodes)
	}
	// The returned turn's first brief carries the refusal's words. (A backend that can
	// resume the worker's session returns to the work node itself; the fixture cannot, so
	// the returned graph starts again at its first node.)
	if len(steps.briefs) < 3 || !strings.Contains(steps.briefs[2], RequestBlockRefusal+": docs/notes.md") {
		t.Fatalf("the returned turn does not carry the refusal: nodes %v", steps.nodes)
	}
	for _, b := range steps.briefs[:2] {
		if strings.Contains(b, RequestBlockRefusal+": docs/notes.md") {
			t.Fatal("a first-turn brief already carries the refusal")
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, "docs/notes.md")); !os.IsNotExist(err) {
		t.Fatalf("a block reached the trunk: %v", err)
	}
}

// The judge reads a request's criteria against its words, so its brief ends with the same
// byte-counted suffix, which the adapter's preflight accepts.
func TestTheJudgeOfARequestReadsItsWords(t *testing.T) {
	f, l, s := judgeRouteFixture(t, "pass", true)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO request_words(item_id, words_hash, words, input_hash) VALUES('item-a', 'h', ?, 'i')`, []byte(requestWords))
		return err
	})
	lane := l.laneDirPath("lane-1")
	session, err := NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane, BriefFile), "worker brief\n")
	writeForkFile(t, filepath.Join(lane, SessionIDFile), session+"\n")
	base := runGit(t, l.lanePath("lane-1"), "rev-parse", "HEAD")
	worker := run.Request{RunID: "run-item-a-a1", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture"}
	if _, _, err := s.Run(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{ItemID: worker.ItemID, LaneID: worker.LaneID, RunID: worker.RunID, Worktree: l.lanePath(worker.LaneID), BaseSHA: base, HeadSHA: s.workerHead}
	receipt := &JudgeReceipt{Request: *l.cfg.Policy.request(worker.ItemID, worker.LaneID, worker.RunID+"-judge", "reviewer")}
	receipt.Options.Head = candidate.HeadSHA
	receipt.Options.Tree = runGit(t, candidate.Worktree, "rev-parse", candidate.HeadSHA+"^{tree}")
	if err := l.stageJudge(context.Background(), candidate, receipt, session); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(lane, BriefFile)
	raw, err := os.ReadFile(brief)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "# Judge candidate") || !strings.HasSuffix(string(raw), requestWordsSuffix([]byte(requestWords))) {
		t.Fatalf("judge brief:\n%s", raw)
	}
	if err := probePreflight(t, brief); err != nil {
		t.Fatalf("the adapter refused the judge's brief: %v", err)
	}
}
