package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

type seatRecordSteps struct {
	*ProcessSteps
	t         *testing.T
	root      string
	laneRoot  string
	adapter   string
	proposed  bool
	committed bool
}

func (s *seatRecordSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	switch {
	case req.Node == "plan":
		if !s.proposed {
			s.proposed = true
			proposalArtifact(s.t, s.root, req.RunID, pieceProposal([]map[string]any{
				piece("one", "build", "write"),
			}))
		}
		req.Command = []string{s.adapter, "-c", "echo 'Plan ready.'; echo 'RESULT: done'"}
	case req.Node == "work/piece-one" || req.Node == "worker":
		if !s.committed {
			s.committed = true
			req.Command = []string{s.adapter, "-c", "mkdir -p tools/item-a && printf 'work\\n' >> tools/item-a/work.txt && git add tools/item-a/work.txt && git commit -qm 'worker change' && echo 'RESULT: done'"}
		} else {
			req.Command = []string{s.adapter, "-c", "echo 'RESULT: done'"}
		}
	case req.Node == "evaluate" || req.Node == "judge":
		req.Command = []string{s.adapter, "-c", "echo 'RESULT: done'"}
	default:
		req.Command = []string{s.adapter, "-c", "echo 'Reader ok.'; echo 'RESULT: done'"}
	}
	return s.ProcessSteps.Run(ctx, req)
}

func (s *seatRecordSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	return SyncOutcome{}, nil
}

func (s *seatRecordSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *seatRecordSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	return LandOutcome{Result: LandLanded}, nil
}

func (s *seatRecordSteps) Verdict(ctx context.Context, c Candidate, outcome string) error {
	return nil
}

func (s *seatRecordSteps) ItemState(ctx context.Context, id, _, state string, _ ...item.WaitingOn) error {
	st, err := store.Open(ctx, s.Store, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, state, id)
		return err
	})
}

func drainActiveRuns(t *testing.T, l *Loop, result *LoopResult) {
	t.Helper()
	for len(l.active) > 0 {
		c := <-l.completions
		if stop, err := l.takeCompletion(c, result); stop != "" || err != nil {
			t.Fatalf("completion: %s %v", stop, err)
		}
	}
}

func assertRunStamped(t *testing.T, root, laneRoot, storePath, runID, wantSeat, wantWorkflow, wantNode, wantNodeID string) {
	t.Helper()
	// 1. .requests/<run_id>.request.json
	reqPath := filepath.Join(laneRoot, ".requests", runID+".request.json")
	reqRaw, err := os.ReadFile(reqPath)
	if err != nil {
		t.Fatalf("read %s: %v", reqPath, err)
	}
	var reqEnv record.Envelope
	if err := json.Unmarshal(reqRaw, &reqEnv); err != nil {
		t.Fatalf("decode %s: %v", reqPath, err)
	}
	var req run.Request
	if err := json.Unmarshal(reqEnv.Payload, &req); err != nil {
		t.Fatalf("decode request payload %s: %v", runID, err)
	}
	if req.Seat != wantSeat || req.Workflow != wantWorkflow || req.Node != wantNode || req.NodeID != wantNodeID {
		t.Fatalf("%s request.json = (seat=%q, wf=%q, node=%q, node_id=%q), want (%q, %q, %q, %q)",
			runID, req.Seat, req.Workflow, req.Node, req.NodeID, wantSeat, wantWorkflow, wantNode, wantNodeID)
	}

	// 2. run.record.json
	recPath := filepath.Join(RunDir(root, runID), "run.record.json")
	recRaw, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("read %s: %v", recPath, err)
	}
	var recEnv record.Envelope
	if err := json.Unmarshal(recRaw, &recEnv); err != nil {
		t.Fatalf("decode %s: %v", recPath, err)
	}
	var rec run.Record
	if err := json.Unmarshal(recEnv.Payload, &rec); err != nil {
		t.Fatalf("decode record payload %s: %v", runID, err)
	}
	if rec.Seat != wantSeat || rec.Workflow != wantWorkflow || rec.Node != wantNode {
		t.Fatalf("%s run.record.json = (seat=%q, wf=%q, node=%q), want (%q, %q, %q)",
			runID, rec.Seat, rec.Workflow, rec.Node, wantSeat, wantWorkflow, wantNode)
	}

	// 3. run.allocate event & runs table
	s, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		var allocRaw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND json_extract(payload,'$.run_id')=? ORDER BY seq DESC LIMIT 1`, run.AllocateKind, runID).Scan(&allocRaw); err != nil {
			t.Fatalf("%s run.allocate query: %v", runID, err)
		}
		var alloc struct {
			Seat     string `json:"seat"`
			Workflow string `json:"workflow"`
			Node     string `json:"node"`
		}
		if err := json.Unmarshal(allocRaw, &alloc); err != nil {
			t.Fatalf("%s decode run.allocate: %v", runID, err)
		}
		if alloc.Seat != wantSeat || alloc.Workflow != wantWorkflow || alloc.Node != wantNode {
			t.Fatalf("%s run.allocate = (seat=%q, wf=%q, node=%q), want (%q, %q, %q)",
				runID, alloc.Seat, alloc.Workflow, alloc.Node, wantSeat, wantWorkflow, wantNode)
		}

		var seat, wf, node *string
		if err := r.QueryRow(`SELECT seat, workflow, node FROM runs WHERE run_id=?`, runID).Scan(&seat, &wf, &node); err != nil {
			t.Fatalf("%s runs query: %v", runID, err)
		}
		gotSeat, gotWF, gotNode := "", "", ""
		if seat != nil {
			gotSeat = *seat
		}
		if wf != nil {
			gotWF = *wf
		}
		if node != nil {
			gotNode = *node
		}
		if gotSeat != wantSeat || gotWF != wantWorkflow || gotNode != wantNode {
			t.Fatalf("%s runs row = (seat=%q, wf=%q, node=%q), want (%q, %q, %q)",
				runID, gotSeat, gotWF, gotNode, wantSeat, wantWorkflow, wantNode)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDispatchStampsWorkflowAndSingleWorkerRunRecords(t *testing.T) {
	runBin := continuationRunBin(t)
	adapter := continuationShell(t)

	t.Run("multi-node-workflow", func(t *testing.T) {
		f, l := passFixture(t, "item-a")
		l.cfg.Lanes = []string{"lane-1"}
		if err := os.MkdirAll(filepath.Join(f.root, "config/tillr/workflows"), 0o755); err != nil {
			t.Fatal(err)
		}
		wfJSON := `{
  "name": "review-build",
  "version": 1,
  "stages": [
    {"id": "plan", "kind": "agent", "seat": "plan", "access": "read_only", "outputs": {"proposal": "graph.proposal"}, "may_replace": {"work": ["build-pieces@v1"]}, "prompt": "Plan"},
    {"id": "scout-a", "kind": "agent", "seat": "scout", "access": "read_only", "prompt": "Scout A"},
    {"id": "scout-b", "kind": "agent", "seat": "scout", "access": "read_only", "prompt": "Scout B"},
    {"id": "work", "kind": "agent", "seat": "build", "access": "write", "prompt": "Build"},
    {"id": "evaluate", "kind": "agent", "seat": "review", "access": "read_only", "judge_classes": ["verification-only"], "prompt": "Review"}
  ],
  "edges": [
    {"from_node": "plan", "to_node": "scout-a"},
    {"from_node": "plan", "to_node": "scout-b"},
    {"from_node": "scout-a", "to_node": "work"},
    {"from_node": "scout-b", "to_node": "work"},
    {"from_node": "work", "to_node": "evaluate"}
  ],
  "exit": {"requires_nodes": ["plan", "scout-a", "scout-b", "work", "evaluate"], "evaluator_slot": "evaluate"}
}`
		writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/review-build@v1.json"), wfJSON)
		bpRaw, err := os.ReadFile(filepath.Join("..", "..", "..", "config/tillr/workflows", "build-pieces@v1.json"))
		if err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/build-pieces@v1.json"), string(bpRaw))
		l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
		if err := os.MkdirAll(filepath.Join(f.root, "briefs"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(f.root, "briefs/item-a.md"), "# Task\nBuild a result.\n")
		runGit(t, f.root, "add", "config/tillr/workflows", "briefs/item-a.md")
		runGit(t, f.root, "commit", "-qm", "add test workflows")
		l.cfg.Policy = &Policy{
			Root: f.root,
			Seats: map[string]string{
				"plan":   "fixture",
				"scout":  "fixture",
				"build":  "fixture",
				"review": "fixture",
				"verify": "fixture",
			},
			Backends: map[string]Backend{
				"fixture": {Adapter: adapter},
			},
		}
		s, err := store.Open(context.Background(), f.storePath, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
			if _, err := tx.Exec(`UPDATE items SET workflow='review-build@v1' WHERE item_id='item-a'`); err != nil {
				return err
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		s.Close()

		steps := &seatRecordSteps{
			ProcessSteps: &ProcessSteps{
				RunBin:   runBin,
				Root:     f.root,
				Store:    f.storePath,
				LaneRoot: f.laneRoot,
				Now:      l.cfg.Now,
			},
			t:        t,
			root:     f.root,
			laneRoot: f.laneRoot,
			adapter:  adapter,
		}
		l.cfg.Steps = steps

		result := &LoopResult{}
		for i := 0; i < 12; i++ {
			if _, err := l.pass(context.Background(), result); err != nil {
				t.Fatalf("pass %d: %v", i, err)
			}
			drainActiveRuns(t, l, result)
			if result.Landed > 0 {
				break
			}
		}

		// Verify all runs in the DB for item-a:
		// initial (plan), parallel readers (scout-a, scout-b), proposed piece (extra-check),
		// writer (implement), evaluator (evaluate).
		sr, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer sr.Close()
		var runIDs []string
		if err := sr.ReadTx(context.Background(), func(r *store.Reader) error {
			rows, err := r.Query(`SELECT run_id FROM runs WHERE item_id='item-a' ORDER BY rowid`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					return err
				}
				runIDs = append(runIDs, id)
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		if len(runIDs) < 5 {
			t.Fatalf("expected >= 5 runs (plan, scout-a, scout-b, work/piece-one, evaluate), got %v", runIDs)
		}

		wantByNode := map[string]string{
			"plan":           "plan",
			"scout-a":        "scout",
			"scout-b":        "scout",
			"work/piece-one": "build",
			"evaluate":       "review",
		}
		seenNodes := map[string]bool{}
		for _, id := range runIDs {
			reqRaw, err := os.ReadFile(filepath.Join(f.laneRoot, ".requests", id+".request.json"))
			if err != nil {
				t.Fatal(err)
			}
			var env record.Envelope
			if err := json.Unmarshal(reqRaw, &env); err != nil {
				t.Fatal(err)
			}
			var req run.Request
			if err := json.Unmarshal(env.Payload, &req); err != nil {
				t.Fatal(err)
			}
			wantSeat, ok := wantByNode[req.Node]
			if !ok {
				t.Fatalf("unexpected node %q for run %s", req.Node, id)
			}
			seenNodes[req.Node] = true
			assertRunStamped(t, f.root, f.laneRoot, f.storePath, id, wantSeat, "review-build@v1", req.Node, req.Node)
		}
		for node := range wantByNode {
			if !seenNodes[node] {
				t.Fatalf("missing node %q in runs %v", node, runIDs)
			}
		}
	})

	t.Run("single-worker", func(t *testing.T) {
		f, l := passFixture(t, "item-b")
		l.cfg.Lanes = []string{"lane-1"}
		l.cfg.Policy = &Policy{
			Root: f.root,
			Seats: map[string]string{
				"build":  "fixture",
				"review": "fixture",
			},
			Backends: map[string]Backend{
				"fixture": {Adapter: adapter},
			},
		}
		s, err := store.Open(context.Background(), f.storePath, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET seat='build', workflow='' WHERE item_id='item-b'`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		s.Close()

		steps := &seatRecordSteps{
			ProcessSteps: &ProcessSteps{
				RunBin:   runBin,
				Root:     f.root,
				Store:    f.storePath,
				LaneRoot: f.laneRoot,
				Now:      l.cfg.Now,
			},
			t:        t,
			root:     f.root,
			laneRoot: f.laneRoot,
			adapter:  adapter,
		}
		l.cfg.Steps = steps

		result := &LoopResult{}
		for i := 0; i < 4; i++ {
			if _, err := l.pass(context.Background(), result); err != nil {
				t.Fatalf("pass %d: %v", i, err)
			}
			drainActiveRuns(t, l, result)
			if result.Landed > 0 {
				break
			}
		}

		sr, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer sr.Close()
		var runIDs []string
		if err := sr.ReadTx(context.Background(), func(r *store.Reader) error {
			rows, err := r.Query(`SELECT run_id FROM runs WHERE item_id='item-b' ORDER BY rowid`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					return err
				}
				runIDs = append(runIDs, id)
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		if len(runIDs) == 0 {
			t.Fatal("expected at least 1 run for item-b")
		}
		workerID := runIDs[0]
		if strings.HasSuffix(workerID, "-judge") {
			t.Fatalf("expected worker run first, got %s", workerID)
		}
		assertRunStamped(t, f.root, f.laneRoot, f.storePath, workerID, "build", singleWorkerWorkflow, "worker", "")
	})
}

func TestEndNonTerminalAndResumePreserveSeatWorkflowAndNode(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "board.sqlite3")
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id) VALUES ('lane-paused', 'leased', 'run-paused-seat')`); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO runs(run_id,item_id,lane_id,backend,seat,workflow,node,state,started_at,finished_at,artifact_refs,event_seq)
			 VALUES ('run-paused-seat','item-paused','lane-paused','pi','design','scouted-design@v1','design','paused',?,?,'[]',1)`,
			now.UnixMilli(), now.Add(time.Second).UnixMilli(),
		); err != nil {
			return err
		}
		return endNonTerminalRunRow(ctx, tx, "run-paused-seat", "test-reclaim", false, now.Add(2*time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	row, err := readRunForResume(ctx, storePath, "run-paused-seat")
	if err != nil {
		t.Fatal(err)
	}
	if row.Seat != "design" || row.Workflow != "scouted-design@v1" || row.Node != "design" {
		t.Fatalf("readRunForResume = (%q, %q, %q), want (design, scouted-design@v1, design)", row.Seat, row.Workflow, row.Node)
	}
	resumed, err := resumeRequest(ResumeConfig{LaneRoot: dir}, row, ResumeSpec{
		RunID: "run-paused-seat",
		Request: &run.Request{
			RunID:   "run-paused-seat",
			LaneID:  "lane-paused",
			Backend: "pi",
			Command: []string{"/bin/true"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Seat != "design" || resumed.Workflow != "scouted-design@v1" || resumed.Node != "design" {
		t.Fatalf("resumeRequest = (%q, %q, %q), want (design, scouted-design@v1, design)", resumed.Seat, resumed.Workflow, resumed.Node)
	}
}
