package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	return fileFixtureRequestWith(t, f, requestWords, seat, workflow, paths, false)
}

func fileFixtureRequestWith(t *testing.T, f refusalLoopFixture, words, seat, workflow string, paths []string, ordinary bool) string {
	t.Helper()
	var id string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		env, err := item.FileRequest(context.Background(), tx, item.RequestFiling{
			Words: []byte(words), Title: "Remove the index cache", Seat: seat, Workflow: workflow, Paths: paths,
			Ordinary: ordinary, Destination: item.Destination{To: "first"}, At: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), By: "owner", Via: "cli",
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
	f, l, s := judgeRouteFixture(t, "pass")
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
	receipt.Writers = []JudgeWriter{{NodeID: "work", RunID: worker.RunID, SessionID: "worker-session-sentinel"}}
	report := filepath.Join(RunDir(l.cfg.Root, worker.RunID), "node-report.md")
	if err := os.MkdirAll(filepath.Dir(report), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, report, "Worker claims that tests passed.\nRESULT: done\n")
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
	if strings.Contains(string(raw), report) || strings.Contains(string(raw), "Worker claims") ||
		strings.Contains(string(raw), "worker-session-sentinel") || !strings.Contains(string(raw), "Writing node: node:work") ||
		!strings.Contains(string(raw), "Judge the candidate commit against only the item's words and criteria") ||
		!strings.Contains(string(raw), "Run the checks you need yourself") {
		t.Fatalf("judge brief does not isolate independent verification:\n%s", raw)
	}
	if err := probePreflight(t, brief); err != nil {
		t.Fatalf("the adapter refused the judge's brief: %v", err)
	}
}

func TestRequestExpressAndOrdinaryLaneAllocation(t *testing.T) {
	// Case 1: default filed request takes express-1 when ordinary lanes are busy.
	f, l := passFixture(t, "item-one", "item-two")
	l.cfg.Lanes = []string{"lane-1", "lane-2", "express-1"}
	l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for i, id := range []string{"item-one", "item-two"} {
			lane := l.cfg.Lanes[i]
			req := run.Request{RunID: "run-" + id, ItemID: id, Backend: "fixture"}
			if _, err := run.ClaimLease(context.Background(), tx, req, lane, l.cfg.Now()); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES(?,?,?,'fixture','running',1,0,'[]',0)`, req.RunID, id, lane); err != nil {
				return err
			}
		}
		return nil
	})
	expressID := fileFixtureRequestWith(t, f, "Express request.\n", "build", item.SingleWorkerWorkflow, []string{"docs/notes.md"}, false)
	ordinaryID := fileFixtureRequestWith(t, f, "Ordinary request.\n", "build", item.SingleWorkerWorkflow, []string{"docs/other.md"}, true)
	for _, p := range mustPlan(t, l) {
		d := p.decision
		if d.ItemID == expressID && (d.Action != ActionDispatch || d.LaneID != "express-1") {
			t.Fatalf("default request did not take express-1: %+v", d)
		}
		if d.ItemID == ordinaryID && d.Action != ActionHold {
			t.Fatalf("ordinary request used express lane or dispatched while ordinary lanes busy: %+v", d)
		}
	}

	// Case 2: default filed request falls back to an ordinary lane when express-1 is busy.
	f2, l2 := passFixture(t, "item-busy")
	l2.cfg.Lanes = []string{"lane-1", "express-1"}
	l2.cfg.Policy.Seats = map[string]string{"build": "fixture"}
	fixtureWrite(t, f2, func(tx *store.Tx) error {
		req := run.Request{RunID: "run-busy", ItemID: "item-busy", Backend: "fixture"}
		if _, err := run.ClaimLease(context.Background(), tx, req, "express-1", l2.cfg.Now()); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES(?,?,?,'fixture','running',1,0,'[]',0)`, req.RunID, "item-busy", "express-1")
		return err
	})
	fallbackID := fileFixtureRequestWith(t, f2, "Fallback request.\n", "build", item.SingleWorkerWorkflow, []string{"docs/notes.md"}, false)
	found := false
	for _, p := range mustPlan(t, l2) {
		if p.decision.ItemID == fallbackID {
			found = true
			if p.decision.Action != ActionDispatch || p.decision.LaneID != "lane-1" {
				t.Fatalf("default request did not fall back to lane-1: %+v", p.decision)
			}
		}
	}
	if !found {
		t.Fatal("fallback request not planned")
	}
}

type requestVerifySteps struct {
	*workflowSteps
	plans        int
	works        int
	verifies     int
	repeatSame   bool
	workRuns     []run.Request
	workSessions []string
	briefs       []string
	nodeIDs      []string
}

func (s *requestVerifySteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	s.nodeIDs = append(s.nodeIDs, req.NodeID)
	lane := filepath.Join(s.f.laneRoot, req.LaneID)
	target := filepath.Join(lane, WorktreeDir)
	brief, err := os.ReadFile(filepath.Join(lane, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	s.briefs = append(s.briefs, string(brief))
	report := "RESULT: done\n"
	switch req.NodeID {
	case "plan":
		s.plans++
	case "work":
		s.works++
		s.workRuns = append(s.workRuns, req)
		sess, err := ReadSessionID(lane)
		if err != nil {
			return run.Record{}, "", err
		}
		s.workSessions = append(s.workSessions, sess)
		if s.repeatSame {
			s.t.Setenv("GIT_AUTHOR_DATE", "2026-11-15T00:00:00Z")
			s.t.Setenv("GIT_COMMITTER_DATE", "2026-11-15T00:00:00Z")
			runGit(s.t, target, "reset", "--hard", "-q", s.firstHead)
		}
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			return run.Record{}, "", err
		}
		body := fmt.Sprintf("work %d\n", s.works)
		msg := fmt.Sprintf("work %d", s.works)
		if s.repeatSame {
			body = "work 1\n"
			msg = "work 1"
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/result.txt"), body)
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", msg)
	case "verify":
		s.verifies++
		head := runGit(s.t, target, "rev-parse", "HEAD")
		tree := runGit(s.t, target, "rev-parse", head+"^{tree}")
		var verdicts []item.JudgeEntry
		fixtureRead(s.t, s.f, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT criterion_id,text,claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, req.ItemID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var cid, text, class string
				if err := rows.Scan(&cid, &text, &class); err != nil {
					return err
				}
				verdict := item.VerdictDone
				reason := "verified"
				if s.verifies == 1 && (cid == "c1" || cid == "c2") {
					verdict = item.VerdictNotDone
					reason = fmt.Sprintf("first verify finding for %s", cid)
				} else if s.verifies == 2 && s.repeatSame && (cid == "c1" || cid == "c2") {
					verdict = item.VerdictNotDone
					reason = fmt.Sprintf("first verify finding for %s", cid)
				} else if s.verifies == 2 && !s.repeatSame && cid == "c1" {
					verdict = item.VerdictNotDone
					reason = fmt.Sprintf("second distinct finding for %s", cid)
				}
				verdicts = append(verdicts, item.JudgeEntry{
					CriterionID:   cid,
					CriterionHash: item.CriterionHash(text),
					Verdict:       verdict,
					ClaimClass:    class,
					Reason:        reason,
					Evidence:      []string{"candidate.diff"},
				})
			}
			return rows.Err()
		})
		outcome := "pass"
		if s.verifies <= 2 {
			outcome = "fail"
		}
		raw, _ := json.Marshal(item.JudgeOutput{EvaluatedHead: head, EvaluatedTree: tree, Outcome: outcome, Verdicts: verdicts})
		writeForkFile(s.t, filepath.Join(run.DurableDir(lane, req.RunID), "eval.output"), string(raw))
	default:
		return run.Record{}, "", fmt.Errorf("unexpected node %s", req.NodeID)
	}
	path := filepath.Join(run.DurableDir(lane, req.RunID), "report.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return run.Record{}, "", err
	}
	writeForkFile(s.t, path, report)
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func TestRequestPlannedVerifyFailReturnsToBuilderSessionAndLands(t *testing.T) {
	f, l, base := plannedFixture(t)
	declareWorkerResumeFixture(t, l)
	l.cfg.Policy.Backends["reviewer"] = Backend{Adapter: l.cfg.Policy.Backends["fixture"].Adapter, Model: "review-model"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	id := fileFixtureRequest(t, f, "build", "", requestFence)
	base.firstHead = runGit(t, f.root, "rev-parse", TrunkRef)
	steps := &requestVerifySteps{workflowSteps: base}
	l.cfg.Steps = steps
	result := &LoopResult{}

	// Run through first verify (fail on c1, c2).
	for i := 0; i < 15 && steps.verifies < 1; i++ {
		graphPass(t, l, result)
	}
	if steps.verifies != 1 {
		t.Fatalf("first verify did not run: nodes=%v", steps.nodeIDs)
	}

	// Assert item is not blocked or held: Why reports returned-with-evidence and mover "the next pass", no conflict.
	var state string
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state)
	})
	if state == StateBlocked {
		t.Fatalf("request state became blocked after verify fail")
	}
	var whyAns WhyAnswer
	fixtureRead(t, f, func(r *store.Reader) error {
		var err error
		whyAns, err = Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
		return err
	})
	var foundWhy bool
	for _, row := range whyAns.Rows {
		if row.ItemID == id {
			foundWhy = true
			if row.Reason != ReasonReturned && !strings.Contains(row.Why, ReasonReturned) {
				t.Errorf("Reason=%q Why=%q, want %q", row.Reason, row.Why, ReasonReturned)
			}
			if !strings.Contains(row.Mover, "the next pass") {
				t.Errorf("Mover = %q, want 'the next pass'", row.Mover)
			}
		}
	}
	if !foundWhy {
		t.Fatalf("request %s missing from Why rows", id)
	}

	// Next pass resumes the builder node (work) in its same session with c1 and c2 findings.
	var resumedPlan bool
	for _, p := range mustPlan(t, l) {
		d := p.decision
		if d.ItemID == id {
			if d.Action != ActionDispatch || d.Reason != ReasonReturned || d.Workflow == nil || d.Workflow.Stages[d.NodeIndex].ID != "work" || !d.RunRequest.SessionTurn {
				t.Fatalf("expected resumed work dispatch, got: %+v", d)
			}
			if !strings.Contains(d.RunRequest.ResumePrompt, "c1: first verify finding for c1") || !strings.Contains(d.RunRequest.ResumePrompt, "c2: first verify finding for c2") {
				t.Fatalf("resume prompt lacks findings:\n%s", d.RunRequest.ResumePrompt)
			}
			if strings.Contains(d.RunRequest.ResumePrompt, "Failing gate checks") {
				t.Fatalf("resume prompt unexpectedly emitted gate check header:\n%s", d.RunRequest.ResumePrompt)
			}
			resumedPlan = true
		}
	}
	if !resumedPlan {
		t.Fatal("resumed work decision not planned")
	}

	// Run through second verify (different c1/c2 findings -> must also return, not conflict) and third verify (pass -> land).
	for i := 0; i < 30 && result.Landed == 0; i++ {
		graphPass(t, l, result)
	}
	if result.Landed != 1 {
		t.Fatalf("request did not land after repair: result=%+v nodes=%v", result, steps.nodeIDs)
	}
	if steps.plans != 1 {
		t.Fatalf("plan ran %d times, want 1 (reused via node_reused)", steps.plans)
	}
	if steps.works != 3 || steps.verifies != 3 {
		t.Fatalf("works=%d verifies=%d, want 3 each", steps.works, steps.verifies)
	}
	for i := 1; i < len(steps.workRuns); i++ {
		if !steps.workRuns[i].SessionTurn || steps.workSessions[i] != steps.workSessions[0] {
			t.Errorf("work run %d did not resume session %s: turn=%v got=%s", i, steps.workSessions[0], steps.workRuns[i].SessionTurn, steps.workSessions[i])
		}
		if strings.Contains(steps.workRuns[i].ResumePrompt, "Failing gate checks") {
			t.Errorf("work run %d resume prompt unexpectedly contained 'Failing gate checks'", i)
		}
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var reused int
		if err := r.QueryRow(`SELECT COUNT(*) FROM node_reused WHERE item_id=? AND node_id='plan'`, id).Scan(&reused); err != nil {
			return err
		}
		if reused < 1 {
			t.Errorf("plan was not recorded in node_reused")
		}
		var conflicts int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id=?`, id).Scan(&conflicts); err != nil {
			return err
		}
		if conflicts != 0 {
			t.Errorf("unexpected conflicts recorded: %d", conflicts)
		}
		return nil
	})
}

func TestRequestPlannedVerifyIdenticalRepeatHoldsForOwner(t *testing.T) {
	f, l, base := plannedFixture(t)
	declareWorkerResumeFixture(t, l)
	l.cfg.Policy.Backends["reviewer"] = Backend{Adapter: l.cfg.Policy.Backends["fixture"].Adapter, Model: "review-model"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	id := fileFixtureRequest(t, f, "build", "", requestFence)
	base.firstHead = runGit(t, f.root, "rev-parse", TrunkRef)
	steps := &requestVerifySteps{workflowSteps: base, repeatSame: true}
	l.cfg.Steps = steps
	result := &LoopResult{}

	for i := 0; i < 20 && steps.verifies < 2; i++ {
		graphPass(t, l, result)
	}
	if steps.verifies != 2 {
		t.Fatalf("second verify did not run: nodes=%v", steps.nodeIDs)
	}
	// Run one more pass so repeated ending settlement runs.
	graphPass(t, l, result)

	var c item.Conflict
	fixtureRead(t, f, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind='item.conflict' AND object_id=? ORDER BY seq DESC LIMIT 1`, id).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &c)
	})
	if c.Status != "owner" || c.WaitingOn != "owner" {
		t.Fatalf("identical repeat conflict status=%q waiting_on=%q, want owner/owner", c.Status, c.WaitingOn)
	}
}

type requestSingleJudgeSteps struct {
	*workflowSteps
	works        int
	judges       int
	workReqs     []run.Request
	workSessions []string
	reportOnly   bool
	offerBackend string
	judgedHeads  []string
}

func (s *requestSingleJudgeSteps) offer(ctx context.Context, lane, scratch, target string, offer landingOffer) error {
	var cmd *exec.Cmd
	switch s.offerBackend {
	case "pi":
		tool, err := filepath.Abs("../../tillr-run/backends/pi/land.ts")
		if err != nil {
			return err
		}
		cmd = exec.CommandContext(ctx, "node", "--input-type=module", "-e", `
const [tool, id, target, scratch, base, fence] = process.argv.slice(1);
const { executeLand } = await import(tool);
if (!executeLand(id, target, scratch, base, fence, () => []).park) process.exit(1);
`, tool, offer.CallID, target, scratch, offer.Base, filepath.Join(lane, FenceFile))
	case "claude":
		cmd = exec.CommandContext(ctx, "../../tillr-run/backends/claude/landing", "pre-tool-use", "--durable", scratch, "--base", offer.Base)
		event, err := json.Marshal(map[string]any{
			"tool_name": "mcp__tillr__land", "tool_use_id": offer.CallID, "cwd": target,
			"tool_input": map[string]string{},
		})
		if err != nil {
			return err
		}
		cmd.Stdin = strings.NewReader(string(event))
	default:
		return fmt.Errorf("unsupported fixture backend %q", s.offerBackend)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s land: %w: %s", s.offerBackend, err, out)
	}
	if s.offerBackend == "claude" && !strings.Contains(string(out), `"permissionDecision": "defer"`) {
		return fmt.Errorf("Claude did not defer the offer: %s", out)
	}
	return nil
}

func (s *requestSingleJudgeSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	lane := filepath.Join(s.f.laneRoot, req.LaneID)
	target := filepath.Join(lane, WorktreeDir)
	brief, err := os.ReadFile(filepath.Join(lane, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	if strings.HasPrefix(string(brief), "# Judge candidate") {
		s.judges++
		head := runGit(s.t, target, "rev-parse", "HEAD")
		s.judgedHeads = append(s.judgedHeads, head)
		if s.offerBackend != "" {
			writer := s.workReqs[len(s.workReqs)-1]
			path := filepath.Join(RunDir(s.f.root, writer.RunID), "node-report.md")
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				s.t.Fatalf("a commit offer should not copy a report for judgment: %s: %v", path, err)
			}
		}
		if s.reportOnly && s.judges == 2 && (!req.Resume || !strings.Contains(req.ResumePrompt, "outside the scope")) {
			s.t.Fatalf("report-only finding did not return to the verifier: %+v", req)
		}
		tree := runGit(s.t, target, "rev-parse", head+"^{tree}")
		var verdicts []item.JudgeEntry
		fixtureRead(s.t, s.f, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT criterion_id,text,claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, req.ItemID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var cid, text, class string
				if err := rows.Scan(&cid, &text, &class); err != nil {
					return err
				}
				verdict := item.VerdictDone
				reason := "verified"
				if s.reportOnly && s.judges == 1 && cid == "c2" {
					verdict, reason = item.VerdictNotDone, "report-only: report lacks verification"
				} else if !s.reportOnly && s.judges == 1 && (cid == "c1" || cid == "c2") {
					verdict = item.VerdictNotDone
					reason = fmt.Sprintf("single-worker finding for %s", cid)
				}
				verdicts = append(verdicts, item.JudgeEntry{
					CriterionID:   cid,
					CriterionHash: item.CriterionHash(text),
					Verdict:       verdict,
					ClaimClass:    class,
					Reason:        reason,
					Evidence:      []string{"candidate.diff"},
				})
			}
			return rows.Err()
		})
		outcome := "pass"
		if s.judges == 1 {
			outcome = "fail"
		}
		raw, _ := json.Marshal(item.JudgeOutput{EvaluatedHead: head, EvaluatedTree: tree, Outcome: outcome, Verdicts: verdicts})
		writeForkFile(s.t, filepath.Join(run.DurableDir(lane, req.RunID), "eval.output"), string(raw))
	} else {
		s.works++
		s.workReqs = append(s.workReqs, req)
		sess, err := ReadSessionID(lane)
		if err != nil {
			return run.Record{}, "", err
		}
		s.workSessions = append(s.workSessions, sess)
		if s.offerBackend == "" || s.works == 1 {
			writeForkFile(s.t, filepath.Join(target, "candidate.txt"), fmt.Sprintf("single %d\n", s.works))
			runGit(s.t, target, "add", ".")
			runGit(s.t, target, "commit", "-qm", fmt.Sprintf("single %d", s.works))
		}
		if s.offerBackend != "" {
			raw, err := os.ReadFile(filepath.Join(lane, BaseSHAFile))
			if err != nil {
				return run.Record{}, "", err
			}
			staged := strings.TrimSpace(string(raw))
			head := runGit(s.t, target, "rev-parse", "HEAD")
			if head == staged {
				return run.Record{}, "", fmt.Errorf("There is no commit to offer: HEAD is still the staged base")
			}
			scratch := run.DurableDir(lane, req.RunID)
			if err := s.offer(ctx, lane, scratch, target, landingOffer{CallID: fmt.Sprintf("offer-%d", s.works), Commit: head, Base: staged}); err != nil {
				return run.Record{}, "", err
			}
		}
	}
	report := "RESULT: done\n"
	if s.offerBackend != "" && !strings.HasPrefix(string(brief), "# Judge candidate") {
		backend := s.offerBackend
		extractor, err := filepath.Abs(filepath.Join("../../tillr-run/backends", backend, "extract-report"))
		if err != nil {
			return run.Record{}, "", err
		}
		cmd := exec.CommandContext(ctx, extractor, "--backend", backend, "--landing-offer", filepath.Join(run.DurableDir(lane, req.RunID), landingOfferFile))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return run.Record{}, "", fmt.Errorf("extracting offer report: %w: %s", err, out)
		}
		report = string(out)
	}
	path := filepath.Join(run.DurableDir(lane, req.RunID), "report.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return run.Record{}, "", err
	}
	writeForkFile(s.t, path, report)
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func TestRequestSingleWorkerJudgeNotDoneReturnsToBuilderSession(t *testing.T) {
	testSingleWorkerJudgeReturn(t, false, "")
}

func TestReportOnlyFindingCannotRefuseCandidate(t *testing.T) {
	testSingleWorkerJudgeReturn(t, true, "")
}

func TestJudgeCannotUseWorkerReportAsEvidence(t *testing.T) {
	_, l := passFixture(t)
	c := Candidate{ItemID: "item-a", RunID: "writer", Worktree: filepath.Join(l.cfg.Root, "worktree")}
	report := filepath.Join(RunDir(l.cfg.Root, c.RunID), "node-report.md")
	rel, err := filepath.Rel(c.Worktree, report)
	if err != nil {
		t.Fatal(err)
	}
	entries := []item.JudgeEntry{{CriterionID: "c2", Verdict: item.VerdictNotDone, Reason: "missing test output"}}
	for _, ref := range []string{"file:" + report + ":4", "path:" + rel + "#L4", "report:worker"} {
		entries[0].Evidence = []string{ref}
		if err := l.checkJudgeScope(c, entries); !isJudgeFormRejection(err) {
			t.Fatalf("worker report citation %s was not a verifier-form error: %v", ref, err)
		}
	}
	entries[0].Evidence = []string{"file:docs/report.md:4"}
	if err := l.checkJudgeScope(c, entries); err != nil {
		t.Fatalf("candidate file citation was refused: %v", err)
	}
}

func TestOperatorRetryReoffersJudgeCandidate(t *testing.T) {
	for _, backend := range []string{"pi", "claude"} {
		t.Run(backend, func(t *testing.T) { testSingleWorkerJudgeReturn(t, false, backend) })
	}
}

func testSingleWorkerJudgeReturn(t *testing.T, reportOnly bool, retryBackend string) {
	t.Helper()
	f, l, base := workflowLoopFixture(t, "")
	declareWorkerResumeFixture(t, l)
	l.cfg.Policy.Seats["review"] = "reviewer"
	l.cfg.Policy.Backends["reviewer"] = Backend{Adapter: l.cfg.Policy.Backends["fixture"].Adapter, Model: "review-model"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='closed'`); err != nil {
			return err
		}
		return nil
	})
	id := fileFixtureRequest(t, f, "build", item.SingleWorkerWorkflow, []string{"candidate.txt"})
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET judge_mode='agent' WHERE item_id=?`, id)
		return err
	})
	base.firstHead = runGit(t, f.root, "rev-parse", TrunkRef)
	steps := &requestSingleJudgeSteps{workflowSteps: base, reportOnly: reportOnly, offerBackend: retryBackend}
	l.cfg.Steps = steps
	result := &LoopResult{}

	// First pass runs the worker and judge, including verifier-form corrections.
	for i := 0; i < 6 && steps.judges < 1; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if reportOnly {
		if steps.works != 1 || steps.judges != 2 || result.Landed != 1 || result.Rejected != 0 || steps.judgedHeads[0] != steps.judgedHeads[1] {
			t.Fatalf("out-of-scope finding did not stay with verifier: works=%d judges=%d heads=%v result=%+v", steps.works, steps.judges, steps.judgedHeads, result)
		}
		return
	}
	if steps.judges != 1 {
		t.Fatalf("first judge did not run")
	}

	retrying := retryBackend != ""
	if retrying {
		writer := steps.workReqs[0]
		fixtureWrite(t, f, func(tx *store.Tx) error {
			var latest string
			if err := tx.QueryRow(`SELECT run_id FROM runs WHERE item_id=? ORDER BY started_at DESC,event_seq DESC,rowid DESC LIMIT 1`, id).Scan(&latest); err != nil {
				return err
			}
			_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{ItemID: id, RunID: latest, By: "owner", Why: "Recheck the retained candidate", At: l.cfg.Now()})
			return err
		})
		tip := conflictTestCommit(t, f.root, "later-trunk.txt", "new trunk\n", "advance trunk after judge refusal")
		runGit(t, l.laneTarget(writer.LaneID), "reset", "--hard", tip)
	}
	// An operator retry reoffers the candidate in a fresh session; the automatic
	// return still resumes its builder with both findings.
	var returned bool
	for _, p := range mustPlan(t, l) {
		d := p.decision
		if d.ItemID == id {
			if retrying {
				if d.Action != ActionRetry || d.RetryCandidate == nil || !d.RetryCandidate.Available || d.RunRequest == nil || d.RunRequest.SessionTurn {
					t.Fatalf("expected retained candidate retry: %+v", d)
				}
				returned = true
				continue
			}
			if d.Action != ActionDispatch || d.Reason != ReasonReturned || d.RunRequest == nil || !d.RunRequest.SessionTurn {
				t.Fatalf("expected returned session dispatch, got: %+v", d)
			}
			if !strings.Contains(d.RunRequest.ResumePrompt, "c1: single-worker finding for c1") || !strings.Contains(d.RunRequest.ResumePrompt, "c2: single-worker finding for c2") {
				t.Fatalf("resume prompt lacks findings:\n%s", d.RunRequest.ResumePrompt)
			}
			if strings.Contains(d.RunRequest.ResumePrompt, "Failing gate checks") {
				t.Fatalf("resume prompt unexpectedly contained 'Failing gate checks':\n%s", d.RunRequest.ResumePrompt)
			}
			returned = true
		}
	}
	if !returned {
		t.Fatal("returned dispatch not planned")
	}

	// Complete the second worker + judge + gate + land.
	for i := 0; i < 10 && result.Landed == 0; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if result.Landed != 1 || steps.works != 2 || steps.judges != 2 {
		t.Fatalf("landed=%d works=%d judges=%d result=%+v", result.Landed, steps.works, steps.judges, result)
	}
	if retrying {
		if steps.judgedHeads[0] != steps.judgedHeads[1] {
			t.Errorf("retry changed the judged commit: %v", steps.judgedHeads)
		}
		if steps.workReqs[1].SessionTurn || steps.workSessions[1] == steps.workSessions[0] {
			t.Fatal("operator retry unexpectedly resumed the previous conversation")
		}
		return
	}
	if !steps.workReqs[1].SessionTurn || steps.workSessions[1] != steps.workSessions[0] {
		t.Errorf("second worker run did not resume session %s: turn=%v got=%s", steps.workSessions[0], steps.workReqs[1].SessionTurn, steps.workSessions[1])
	}
}

type requestVerifySuspendsSteps struct {
	*workflowSteps
	transcript string
	sessionID  string
	h1         string
	h2         string
	plans      int
	works      int
	verifies   int
	nodeIDs    []string
	workReqs   []run.Request
	laneCaps   []string
	laneBases  []string
	itemBases  []string
	answers    []landingAnswer
}

func (s *requestVerifySuspendsSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	out, err := s.workflowSteps.Land(ctx, c)
	if err != nil {
		return out, err
	}
	if out.Result == LandLanded && out.TrunkAfter == "" {
		out.TrunkAfter = runGit(s.t, s.f.root, "rev-parse", "HEAD")
	}
	return out, nil
}

func (s *requestVerifySuspendsSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	s.nodeIDs = append(s.nodeIDs, req.NodeID)
	lane := filepath.Join(s.f.laneRoot, req.LaneID)
	target := filepath.Join(lane, WorktreeDir)
	scr := run.DurableDir(lane, req.RunID)
	if err := os.MkdirAll(scr, 0755); err != nil {
		return run.Record{}, "", err
	}
	report := "RESULT: done\n"
	var exitCode int
	switch req.NodeID {
	case "plan":
		s.plans++
	case "work":
		s.works++
		s.workReqs = append(s.workReqs, req)
		capRaw, err := os.ReadFile(filepath.Join(lane, endingCapabilityFile))
		if err != nil {
			return run.Record{}, "", err
		}
		s.laneCaps = append(s.laneCaps, strings.TrimSpace(string(capRaw)))
		baseRaw, err := os.ReadFile(filepath.Join(lane, BaseSHAFile))
		if err != nil {
			return run.Record{}, "", err
		}
		s.laneBases = append(s.laneBases, strings.TrimSpace(string(baseRaw)))
		// Like run-pi's TILLR_GOAL_BASE, the offer uses the archived staged base,
		// not the HEAD at which this turn resumed.
		goalBaseRaw, err := os.ReadFile(filepath.Join(RunDir(s.f.root, req.RunID), BaseSHAFile))
		if err != nil {
			return run.Record{}, "", err
		}
		goalBase := strings.TrimSpace(string(goalBaseRaw))
		itemBaseRaw, err := os.ReadFile(filepath.Join(lane, ItemBaseSHAFile))
		if err != nil {
			return run.Record{}, "", err
		}
		s.itemBases = append(s.itemBases, strings.TrimSpace(string(itemBaseRaw)))

		if s.works == 1 {
			sess, err := ReadSessionID(lane)
			if err != nil {
				return run.Record{}, "", err
			}
			s.sessionID = sess
			if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
				return run.Record{}, "", err
			}
			writeForkFile(s.t, filepath.Join(target, "tools/item-a/result.txt"), "work 1\n")
			runGit(s.t, target, "add", ".")
			runGit(s.t, target, "commit", "-qm", "work 1")
			s.h1 = runGit(s.t, target, "rev-parse", "HEAD")

			parked := `{"type":"session","id":` + strconv.Quote(sess) + "}\n" +
				`{"type":"message","id":"offered-1","parentId":null,"message":{"role":"assistant","content":[{"type":"toolCall","name":"land","id":"call-1"}]}}` + "\n"
			writeForkFile(s.t, s.transcript, parked)
			offer, _ := json.Marshal(landingOffer{
				CallID:     "call-1",
				Tool:       "land",
				Commit:     s.h1,
				Base:       goalBase,
				Transcript: s.transcript,
			})
			writeForkFile(s.t, filepath.Join(scr, landingOfferFile), string(offer))
			report = ""
		} else {
			if head := runGit(s.t, target, "rev-parse", "HEAD"); head != s.h1 {
				return run.Record{}, "", fmt.Errorf("resumed checkout HEAD=%s, want kept candidate %s", head, s.h1)
			}
			ansRaw, err := os.ReadFile(filepath.Join(scr, landingAnswerFile))
			if err != nil {
				return run.Record{}, "", err
			}
			var ans landingAnswer
			if err := json.Unmarshal(ansRaw, &ans); err != nil {
				return run.Record{}, "", err
			}
			s.answers = append(s.answers, ans)
			// Append toolResult for call-1 to session.jsonl.
			trLine, _ := json.Marshal(map[string]any{
				"type":     "message",
				"id":       "result-1",
				"parentId": "offered-1",
				"message": map[string]any{
					"role":       "toolResult",
					"toolName":   "land",
					"toolCallId": ans.CallID,
					"isError":    true,
					"content":    []map[string]string{{"type": "text", "text": ans.Text}},
					"details":    ans,
				},
			})
			f, err := os.OpenFile(s.transcript, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return run.Record{}, "", err
			}
			_, err = f.WriteString(string(trLine) + "\n")
			_ = f.Close()
			if err != nil {
				return run.Record{}, "", err
			}

			writeForkFile(s.t, filepath.Join(target, "tools/item-a/result.txt"), "work 2\n")
			runGit(s.t, target, "add", ".")
			runGit(s.t, target, "commit", "-qm", "work 2")
			s.h2 = runGit(s.t, target, "rev-parse", "HEAD")

			offer, _ := json.Marshal(landingOffer{
				CallID:     "call-2",
				Tool:       "land",
				Commit:     s.h2,
				Base:       goalBase,
				Transcript: s.transcript,
			})
			writeForkFile(s.t, filepath.Join(scr, landingOfferFile), string(offer))
			report = ""
		}
	case "verify":
		s.verifies++
		head := runGit(s.t, target, "rev-parse", "HEAD")
		tree := runGit(s.t, target, "rev-parse", head+"^{tree}")
		var verdicts []item.JudgeEntry
		fixtureRead(s.t, s.f, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT criterion_id,text,claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, req.ItemID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var cid, text, class string
				if err := rows.Scan(&cid, &text, &class); err != nil {
					return err
				}
				verdict := item.VerdictDone
				reason := "verified"
				if s.verifies == 1 && (cid == "c1" || cid == "c2") {
					verdict = item.VerdictNotDone
					reason = fmt.Sprintf("suspends verify finding for %s", cid)
				}
				verdicts = append(verdicts, item.JudgeEntry{
					CriterionID:   cid,
					CriterionHash: item.CriterionHash(text),
					Verdict:       verdict,
					ClaimClass:    class,
					Reason:        reason,
					Evidence:      []string{"candidate.diff"},
				})
			}
			return rows.Err()
		})
		outcome := "pass"
		if s.verifies == 1 {
			outcome = "fail"
		}
		raw, _ := json.Marshal(item.JudgeOutput{EvaluatedHead: head, EvaluatedTree: tree, Outcome: outcome, Verdicts: verdicts})
		writeForkFile(s.t, filepath.Join(scr, "eval.output"), string(raw))
	default:
		return run.Record{}, "", fmt.Errorf("unexpected node %s", req.NodeID)
	}
	var refs []run.ArtifactRef
	if report != "" {
		path := filepath.Join(scr, "report.md")
		writeForkFile(s.t, path, report)
		refs = []run.ArtifactRef{{Kind: "report", Ref: path}}
	}
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(exitCode), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: refs}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func TestRequestPlannedVerifyCallSuspendsEndToEnd(t *testing.T) {
	f, l, base := plannedFixture(t)
	declareWorkerResumeFixture(t, l)
	suspendsDir := filepath.Join(scratchUnderDurable(t), "suspends-adapter")
	if err := os.MkdirAll(suspendsDir, 0755); err != nil {
		t.Fatal(err)
	}
	suspendsAdapter := filepath.Join(suspendsDir, "run-suspends")
	writeForkFile(t, suspendsAdapter, "#!/bin/sh\nexit 0\n")
	writeForkFile(t, filepath.Join(suspendsDir, "pause-kind"), run.PauseAtGap+"\n")
	writeForkFile(t, filepath.Join(suspendsDir, "ending-capability"), run.EndingCallSuspends+"\n")
	l.cfg.Policy.Backends["build-suspends"] = Backend{Adapter: suspendsAdapter, Model: "fixture-model"}
	l.cfg.Policy.Backends["reviewer"] = Backend{Adapter: l.cfg.Policy.Backends["fixture"].Adapter, Model: "review-model"}
	l.cfg.Policy.Seats["build"] = "build-suspends"
	l.endingCapabilities = map[string]string{
		l.cfg.Policy.Backends["fixture"].Adapter:  run.EndingFooter,
		l.cfg.Policy.Backends["reviewer"].Adapter: run.EndingFooter,
		suspendsAdapter: run.EndingCallSuspends,
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	id := fileFixtureRequest(t, f, "build", "", requestFence)
	t0 := runGit(t, f.root, "rev-parse", TrunkRef)
	base.firstHead = t0
	transcript := filepath.Join(scratchUnderDurable(t), "session.jsonl")
	steps := &requestVerifySuspendsSteps{
		workflowSteps: base,
		transcript:    transcript,
	}
	l.cfg.Steps = steps
	result := &LoopResult{}

	// Attempt 1: plan -> work (call-suspends, call-1) -> verify (fails c1, c2 not-done).
	for i := 0; i < 15 && steps.verifies < 1; i++ {
		graphPass(t, l, result)
	}
	if steps.verifies != 1 || steps.works != 1 {
		t.Fatalf("attempt 1 did not finish verify: works=%d verifies=%d nodes=%v", steps.works, steps.verifies, steps.nodeIDs)
	}

	// Verify call.answered is recorded for call-1 with Kind=='refused', Stage=='judge', Criterion findings, and no 'Check: c1' lines.
	var ans landingAnswer
	fixtureRead(t, f, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='call' AND object_id='call-1'`, callAnswered).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &ans)
	})
	if ans.Kind != "refused" || ans.Stage != "judge" {
		t.Fatalf("call-1 answer kind/stage = %q/%q, want refused/judge", ans.Kind, ans.Stage)
	}
	if !strings.Contains(ans.Text, "Criterion c1 (content): suspends verify finding for c1") || !strings.Contains(ans.Text, "Criterion c2 (content): suspends verify finding for c2") {
		t.Fatalf("call-1 answer text missing criterion findings:\n%s", ans.Text)
	}
	if strings.Contains(ans.Text, "Check: c1") {
		t.Fatalf("call-1 answer text unexpectedly contained 'Check: c1':\n%s", ans.Text)
	}

	// Run attempt 2 + verify pass + gate + land.
	for i := 0; i < 20 && result.Landed == 0; i++ {
		graphPass(t, l, result)
	}
	if result.Landed != 1 {
		t.Fatalf("expected Landed==1, got %+v (nodes=%v)", result, steps.nodeIDs)
	}
	if steps.works != 2 || steps.verifies != 2 {
		t.Fatalf("works=%d verifies=%d, want 2 each", steps.works, steps.verifies)
	}
	if !steps.workReqs[1].SessionTurn {
		t.Fatalf("attempt 2 work was not a resumed SessionTurn")
	}
	if steps.laneCaps[1] != run.EndingCallSuspends {
		t.Errorf("attempt 2 lane/ending-capability = %q, want %q", steps.laneCaps[1], run.EndingCallSuspends)
	}
	if steps.laneBases[1] != t0 {
		t.Errorf("attempt 2 lane/base_sha = %q, want T0=%q for the kept candidate", steps.laneBases[1], t0)
	}
	if steps.itemBases[1] != t0 {
		t.Errorf("attempt 2 lane/item_base_sha = %q, want T0=%q", steps.itemBases[1], t0)
	}
	if len(steps.answers) != 1 || steps.answers[0].CallID != "call-1" {
		t.Fatalf("attempt 2 staged landing-answer.json = %+v, want call-1", steps.answers)
	}

	// Verify state == landed, call.delivered recorded for call-1, and call.landed recorded for call-2.
	fixtureRead(t, f, func(r *store.Reader) error {
		var state string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state); err != nil {
			return err
		}
		if state != StateLanded {
			t.Errorf("item state = %q, want %q", state, StateLanded)
		}
		var delivered, landed int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_type='call' AND object_id='call-1'`, callDelivered).Scan(&delivered); err != nil {
			return err
		}
		if delivered != 1 {
			t.Errorf("call-1 call.delivered count = %d, want 1", delivered)
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_type='call' AND object_id='call-2'`, callLanded).Scan(&landed); err != nil {
			return err
		}
		if landed != 1 {
			t.Errorf("call-2 call.landed count = %d, want 1", landed)
		}
		return nil
	})
}
