package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type commentSteps struct {
	recordingSteps
	t                    *testing.T
	f                    refusalLoopFixture
	mode                 string
	sessions             []string
	requests             []run.Request
	briefs               []string
	gates, lands         int
	resumeFailureSession string
	replacementTerminal  chan struct{}
	replacementReturn    <-chan struct{}
}

func (s *commentSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.requests = append(s.requests, req)
	dir := filepath.Join(s.f.laneRoot, req.LaneID)
	session, err := ReadSessionID(dir)
	if err != nil {
		return run.Record{}, "", err
	}
	s.sessions = append(s.sessions, session)
	raw, err := os.ReadFile(filepath.Join(dir, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	s.briefs = append(s.briefs, string(raw))
	if item.IsCommentItem(req.ItemID) && !strings.Contains(string(raw), "For a reply-only answer, the task's no-commit exception overrides the ordinary commit requirement below.") {
		s.t.Error("brief still requires a commit for an answer-only run")
	}
	if !strings.HasPrefix(req.LaneID, "desk-") {
		s.t.Errorf("not a desk: %s", req.LaneID)
	}
	target := filepath.Join(dir, WorktreeDir)
	if session == s.resumeFailureSession {
		log := filepath.Join(run.DurableDir(dir, req.RunID), "stream.jsonl")
		if err := os.MkdirAll(filepath.Dir(log), 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, log, "{\"type\":\"session\"}\n")
		rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, Route: "fixture-model", State: run.StateExited, ExitCode: countPtr(1), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "log", Ref: log}}}
		fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		return rec, "", nil
	}
	if s.mode != "answered" && s.mode != "missing" && s.mode != "foreign" && s.mode != "no-round" && s.mode != "duplicate" {
		writeForkFile(s.t, filepath.Join(target, "docs/design/example.md"), fmt.Sprintf("# Example design\n\n## Shape\n\nRevised %d.\n", len(s.requests)))
		runGit(s.t, target, "add", "docs/design/example.md")
		runGit(s.t, target, "commit", "-qm", "docs: revise shape")
	}
	var batch *item.CommentBatch
	st, err := store.OpenRead(ctx, s.f.storePath)
	if err != nil {
		return run.Record{}, "", err
	}
	err = st.ReadTx(ctx, func(r *store.Reader) error { var e error; batch, e = item.ReadCommentBatch(r, req.ItemID); return e })
	st.Close()
	if err != nil {
		return run.Record{}, "", err
	}
	durable := run.DurableDir(dir, req.RunID)
	if err = os.MkdirAll(durable, 0755); err != nil {
		return run.Record{}, "", err
	}
	replyPath := filepath.Join(durable, commentRepliesFile)
	replies := ""
	var comments []item.ReviewComment
	if batch != nil {
		comments = batch.Comments
	}
	for _, c := range comments {
		r := commentReply{ID: c.ID, Round: len(c.Reactions), Summary: "The Shape section explains this choice."}
		if s.mode == "foreign" {
			r.ID = "not-in-batch"
		}
		b, _ := json.Marshal(r)
		if s.mode == "no-round" {
			b = []byte(fmt.Sprintf(`{"id":%q,"summary":"Answer."}`, r.ID))
		}
		replies += string(b) + "\n"
		if s.mode == "duplicate" {
			replies += string(b) + "\n"
		}
	}
	if s.mode == "missing" {
		replies = ""
	}
	writeForkFile(s.t, replyPath, replies)
	log := filepath.Join(durable, "stream.jsonl")
	transcript := filepath.Join(durable, "transcript.jsonl")
	stream := fmt.Sprintf(`{"type":"session","id":%q,"transcript":%q}
{"type":"turn_end","message":{"role":"assistant","model":"fixture-model","timestamp":1794700800000,"usage":{"input":10,"cacheRead":50,"cacheWrite":20,"output":5,"cacheLifetimeSeconds":3600}}}
`, session, transcript)
	writeForkFile(s.t, transcript, stream)
	writeForkFile(s.t, log, stream)
	report := "Answered.\nRESULT: done\n"
	if s.mode == "failed" {
		report = "Could not finish.\nRESULT: failed\n"
	}
	reportPath := filepath.Join(durable, "report.md")
	writeForkFile(s.t, reportPath, report)
	tokens, cost := int64(85), 0.42
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, Route: "fixture-model", State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", Usage: &run.Usage{Tokens: &tokens, CostUSD: &cost}, ArtifactRefs: []run.ArtifactRef{{Kind: "log", Ref: log}, {Kind: "report", Ref: reportPath}}}
	st, err = store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	if err == nil && s.replacementTerminal != nil && session != s.resumeFailureSession {
		close(s.replacementTerminal)
		select {
		case <-s.replacementReturn:
		case <-ctx.Done():
			return rec, report, ctx.Err()
		}
	}
	return rec, report, err
}
func (s *commentSteps) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	s.gates++
	if files, _ := filepath.Glob(filepath.Join(s.f.root, ".vantage/inbox/*.jsonl")); len(files) != 0 {
		s.t.Fatal("reply before gate")
	}
	if s.mode == "refused" {
		return GateOutcome{Outcome: GateFailed}, nil
	}
	return GateOutcome{Outcome: GatePassed}, nil
}
func (s *commentSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.lands++
	if files, _ := filepath.Glob(filepath.Join(s.f.root, ".vantage/inbox/*.jsonl")); len(files) != 0 {
		s.t.Fatal("reply before landing")
	}
	runGit(s.t, s.f.root, "merge", "--squash", c.HeadSHA)
	runGit(s.t, s.f.root, "commit", "-qm", "docs: land revision")
	// The stub lander records its landing before returning, like the real land face.
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error {
		return recordFixtureLanding(tx, c.ItemID, c.RunID, runGit(s.t, s.f.root, "rev-parse", "HEAD"), 1)
	})
	if s.mode == "owner-edit" {
		writeForkFile(s.t, filepath.Join(s.f.root, "docs/design/example.md"), "Owner's unsaved text.\n")
	}
	return LandOutcome{Result: LandLanded}, nil
}
func (s *commentSteps) ItemState(ctx context.Context, id, runID, state string, waiting ...item.WaitingOn) error {
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET state=? WHERE item_id=?", state, id)
		return err
	})
}

func commentFixture(t *testing.T, mode string) (refusalLoopFixture, *Loop, *commentSteps) {
	f := newRefusalLoopFixture(t, "item-other")
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec("UPDATE items SET state='closed'"); return err })
	if err := os.MkdirAll(filepath.Join(f.root, "docs/design"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "docs/design/example.md"), "# Example design\n\n## Shape\n\nA passage.\n")
	writeForkFile(t, filepath.Join(f.root, ".gitignore"), ".tillr/\n.vantage/\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "docs: fixture")
	stage := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	steps := &commentSteps{t: t, f: f, mode: mode}
	l := f.loop(t, steps, stage)
	l.cfg.Report = os.Stdout
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	l.cfg.Policy.Backends["fixture"] = Backend{Adapter: "fixture", Model: "fixture-model"}
	holdWALKeeper(t, f.storePath)
	return f, l, steps
}

func takeFixtureComment(t *testing.T, f refusalLoopFixture, server string) string {
	t.Helper()
	ctx := context.Background()
	comments, err := item.ReadReview(ctx, server, "fixture", "docs/design/example.md")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var id string
	err = st.WriteTx(ctx, func(tx *store.Tx) error {
		var e error
		id, e = item.TakeComments(ctx, tx, f.root, "docs/design/example.md", comments, time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func stubReview(t *testing.T, number *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"file_path":"docs/design/example.md","comments":[{"id":"comment-%d","comment":"Why this shape?","created_at":1794700000,"captured_block":"A passage.","anchor":{"source_line":5},"question_id":"OQ-1","root_text_version":"version-%d","reactions":[]}]}`, *number, *number)
	}))
}
func TestCommentBatchHoldsUndispatchedBuildItems(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	number := 1
	server := stubReview(t, &number)
	defer server.Close()
	id := takeFixtureComment(t, f, server.URL)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET state='in-queue',design_doc='docs/design/example.md' WHERE item_id='item-other'")
		return err
	})
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-other" && (p.decision.Action != ActionHold || p.decision.Reason != "design-under-comment" || p.decision.DeferredBy != id) {
			t.Fatalf("build was not held for its comment batch: %+v", p.decision)
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(context.Background(), tx, id, "answered", "fixture", l.cfg.Now())
		return err
	})
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-other" && p.decision.Action != ActionDispatch {
			t.Fatalf("closed batch still holds its build: %+v", p.decision)
		}
	}
}

func TestCommentPathTwice(t *testing.T) {
	f, l, steps := commentFixture(t, "")
	number := 1
	server := stubReview(t, &number)
	defer server.Close()
	desk := filepath.Join(f.laneRoot, "desk-example", WorktreeDir)
	for i := 1; i <= 2; i++ {
		number = i
		if _, err := os.Stat(desk); !os.IsNotExist(err) {
			t.Fatalf("worktree before run: %v", err)
		}
		id := takeFixtureComment(t, f, server.URL)
		plans := mustPlan(t, l)
		var dispatch Decision
		for _, p := range plans {
			if p.decision.ItemID == id {
				dispatch = p.decision
			}
		}
		reason := "no-designer"
		if i == 2 {
			reason = "resumed-warm"
		}
		if dispatch.LaneID != "desk-example" || dispatch.DesignerReason != reason {
			t.Fatalf("dispatch %+v", dispatch)
		}
		ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
		defer cancel()
		result, err := l.Run(ctx)
		if err != nil {
			t.Fatalf("loop: %+v %v", result, err)
		}
		if _, err := os.Stat(desk); !os.IsNotExist(err) {
			t.Fatalf("worktree after run: %v", err)
		}
		files, _ := filepath.Glob(filepath.Join(f.root, ".vantage/inbox/*.jsonl"))
		if len(files) != 1 {
			t.Fatalf("inbox %v result %+v", files, result)
		}
		var reply commentReply
		raw, _ := os.ReadFile(files[0])
		if err = json.Unmarshal(raw, &reply); err != nil {
			t.Fatal(err)
		}
		if reply.ID != fmt.Sprintf("comment-%d", i) || reply.Round != 0 || reply.Path != "docs/design/example.md" || reply.Nonce == "" || reply.RootTextVersion != fmt.Sprintf("version-%d", i) {
			t.Fatalf("reply %+v", reply)
		}
		st, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		err = st.ReadTx(context.Background(), func(r *store.Reader) error {
			var tokens int64
			var cost float64
			var recordedReason string
			var landed int
			req := steps.requests[len(steps.requests)-1]
			if err := r.QueryRow("SELECT r.usage_tokens,r.usage_cost_usd,c.reason FROM runs r JOIN comment_runs c USING(run_id) WHERE run_id=?", req.RunID).Scan(&tokens, &cost, &recordedReason); err != nil {
				return err
			}
			if tokens != 85 || cost != 0.42 || recordedReason != reason {
				t.Fatalf("spend/reason %d %f %s", tokens, cost, recordedReason)
			}
			if err := r.QueryRow("SELECT count(*) FROM landings WHERE run_id=?", req.RunID).Scan(&landed); err != nil {
				return err
			}
			if landed != 1 {
				t.Fatal("inbox without recorded landing")
			}
			d, err := readDesigner(r, "docs/design/example.md")
			if err != nil {
				return err
			}
			if d == nil || d.SessionID != steps.sessions[i-1] || d.Backend != "fixture" || d.Model != "fixture-model" || d.Desk != "desk-example" || d.RunID != req.RunID || d.LastRequest == "" || d.ContextTokens == nil || *d.ContextTokens != 85 || d.CacheLifetimeSeconds == nil || *d.CacheLifetimeSeconds != 3600 || d.BlobID == "" || d.Transcript == "" {
				t.Fatalf("designer %+v", d)
			}
			return nil
		})
		st.Close()
		if err != nil {
			t.Fatal(err)
		}
		c := Candidate{ItemID: id, LaneID: "desk-example", RunID: steps.requests[i-1].RunID}
		if err = l.deliverCommentReplies(context.Background(), c); err != nil {
			t.Fatal(err)
		}
		replay, _ := os.ReadFile(files[0])
		if string(replay) != string(raw) {
			t.Fatalf("delivery changed nonce: %s / %s", raw, replay)
		}
		os.Remove(files[0])
		// Build a fresh supervisor to prove no process-local state supplies continuity.
		stage := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
		l = f.loop(t, steps, stage)
		l.cfg.Lanes = []string{"lane-1"}
		l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
		l.cfg.Policy.Backends["fixture"] = Backend{Adapter: "fixture", Model: "fixture-model"}
	}
	if steps.sessions[0] != steps.sessions[1] {
		t.Fatalf("session changed %v", steps.sessions)
	}
	for i, name := range []string{"comment-new-task.txt", "comment-resumed-task.txt"} {
		_, task, ok := strings.Cut(steps.briefs[i], "## Task\n\n")
		if !ok {
			t.Fatal("missing task")
		}
		task, _, _ = strings.Cut(task, "\nThe item's criteria")
		dir := filepath.Join(f.laneRoot, "desk-example")
		task = strings.ReplaceAll(task, run.DurableDir(dir, steps.requests[i].RunID), "/SCRATCH")
		if i > 0 {
			task = strings.ReplaceAll(task, run.DurableDir(dir, steps.requests[i-1].RunID), "/PREVIOUS")
		}
		fixture, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(task) != strings.TrimSpace(string(fixture)) {
			t.Fatalf("staged task differs from %s:\n%s", name, task)
		}
	}
	for _, brief := range steps.briefs {
		for _, want := range []string{"Comment id:", "Turn count (Vantage round): 0", "Heading: Shape", "Passage:\nA passage.", "Question id: OQ-1", "Reviewer: Why this shape?", "Never copy the thread into the document."} {
			if !strings.Contains(brief, want) {
				t.Errorf("brief missing %q", want)
			}
		}
	}
	if !strings.Contains(steps.briefs[0], "new session; read the document") || !strings.Contains(steps.briefs[1], "What moved: unchanged") {
		t.Fatalf("movement briefs %v", steps.briefs)
	}
}

func TestCommentDeliveryWaitsAndReplaysStoredBytes(t *testing.T) {
	f, l, steps := commentFixture(t, "owner-edit")
	number := 1
	server := stubReview(t, &number)
	defer server.Close()
	takeFixtureComment(t, f, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	result, err := l.Run(ctx)
	if err != nil {
		t.Fatalf("loop %+v: %v", result, err)
	}
	files, _ := filepath.Glob(filepath.Join(f.root, ".vantage/inbox/*.jsonl"))
	if len(files) != 0 {
		t.Fatal("delivered against owner edit")
	}
	if steps.lands != 1 {
		t.Fatalf("landings %d", steps.lands)
	}
	req := steps.requests[0]
	// Delete the session's reply file to prove the durable pending delivery is sufficient.
	if err := os.Remove(filepath.Join(run.DurableDir(filepath.Join(f.laneRoot, req.LaneID), req.RunID), commentRepliesFile)); err != nil {
		t.Fatal(err)
	}
	clean, err := l.git(ctx, f.root, "show", "HEAD:docs/design/example.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, edited := range []string{clean + "\n", strings.TrimRight(clean, "\n"), clean + " "} {
		writeForkFile(t, filepath.Join(f.root, "docs/design/example.md"), edited)
		if err := l.flushCommentDeliveries(ctx); err != nil {
			t.Fatal(err)
		}
		if files, _ := filepath.Glob(filepath.Join(f.root, ".vantage/inbox/*.jsonl")); len(files) != 0 {
			t.Fatal("delivered against a byte-different document")
		}
	}
	writeForkFile(t, filepath.Join(f.root, "docs/design/example.md"), clean)
	if err = l.flushCommentDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	files, _ = filepath.Glob(filepath.Join(f.root, ".vantage/inbox/*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("pending delivery lost: %v", files)
	}
	first, _ := os.ReadFile(files[0])
	os.Remove(files[0])
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec("UPDATE comment_deliveries SET written=0"); return err })
	if err = l.flushCommentDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(files[0])
	if string(first) != string(second) {
		t.Fatal("replay changed delivery bytes")
	}
}

func TestCommentBriefCarriesWholeThread(t *testing.T) {
	thread := json.RawMessage(`{"actor":"reviewer","summary":"A second question.\\nVerbatim wording.","kind":"needs_clarification","timestamp":42}`)
	batch := &item.CommentBatch{Document: "docs/design/example.md", Comments: []item.ReviewComment{{ID: "comment-1", Comment: "Original question.", Reactions: []json.RawMessage{thread}}}}
	task := commentThreadTask(batch, "unchanged", "", "/scratch")
	if !strings.Contains(task, string(thread)) || !strings.Contains(task, "Turn count (Vantage round): 1") {
		t.Fatalf("lost thread: %s", task)
	}
}

func TestCommentRefusedAndAnswerOnly(t *testing.T) {
	for _, mode := range []string{"refused", "answered", "missing", "foreign", "failed", "no-round", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			f, l, steps := commentFixture(t, mode)
			number := 1
			server := stubReview(t, &number)
			defer server.Close()
			id := takeFixtureComment(t, f, server.URL)
			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			defer cancel()
			result, err := l.Run(ctx)
			if err != nil {
				t.Fatalf("loop: %+v %v", result, err)
			}
			files, _ := filepath.Glob(filepath.Join(f.root, ".vantage/inbox/*.jsonl"))
			if mode == "answered" {
				if len(files) != 1 || steps.gates != 0 || steps.lands != 0 {
					t.Fatalf("answer-only files=%v gates=%d lands=%d", files, steps.gates, steps.lands)
				}
			} else if len(files) != 0 || steps.lands != 0 {
				t.Fatalf("refused delivered: %v lands %d", files, steps.lands)
			}
			if mode == "failed" && steps.gates != 0 {
				t.Fatal("committed failed-footer run was gated")
			}
			st, err := store.OpenRead(context.Background(), f.storePath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			err = st.ReadTx(context.Background(), func(r *store.Reader) error {
				if mode == "answered" {
					var state, outcome, reason, by string
					if err := r.QueryRow("SELECT i.state,c.outcome,i.close_reason,i.closed_by FROM items i JOIN comment_runs c USING(item_id) WHERE item_id=?", id).Scan(&state, &outcome, &reason, &by); err != nil {
						return err
					}
					if state != "closed" || outcome != "answered" || reason == "" || by != "drain" {
						t.Fatalf("answer state %s %s", state, outcome)
					}
				}
				if mode == "missing" || mode == "foreign" || mode == "no-round" || mode == "duplicate" {
					var state string
					if err := r.QueryRow("SELECT state FROM item_criteria WHERE item_id=? AND criterion_id='c1'", id).Scan(&state); err != nil {
						return err
					}
					if state != item.VerdictNotDone {
						t.Fatalf("criterion 1 %s", state)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
