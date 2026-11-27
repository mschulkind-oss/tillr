package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestAnsweredObstacleRepeat(t *testing.T) {
	for _, tc := range []struct{ name, kind, first, next, waiter string }{
		{"definition/repeated-evidence", "definition", "CRITERIA: c1\nEVIDENCE: first count", "CRITERIA: c1\nEVIDENCE: first\n  count", "operator"},
		{"definition/tillr-watch-distinct-evidence", "definition", "CRITERIA: c1\nEVIDENCE: the design doesn't say how status distinguishes a killed watcher from an idle one", "CRITERIA: c1\nEVIDENCE: launch examples omit required --store and --rules", "operator"},
		{"definition/different-criteria", "definition", "CRITERIA: c1\nEVIDENCE: first count", "CRITERIA: c2\nEVIDENCE: first count", "design"},
		{"owner/repeated-question", "owner", "QUESTION: publish the branch?", "QUESTION: publish the branch?", "owner"},
		{"owner/different-question", "owner", "QUESTION: publish the branch?", "QUESTION: publish the other branch?", "design"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, work := obstacleFixture(t, &obstacleSteps{}, "item-a")
			ctx := context.Background()
			report := func(fields string) string { return "OBSTACLE: " + tc.kind + "\n" + fields + "\nRESULT: blocked" }
			settleReport(t, f, l, work, report(tc.first))
			c, ok := conflictOf(t, f, "item-a")
			if !ok || c.WaitingOn != "design" {
				t.Fatalf("first obstacle: %+v", c)
			}
			path := filepath.Join(f.root, c.BodyRef)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, path, "# Keep the criterion\n\nThe original criterion still applies.\n\n"+string(original))
			runGit(t, f.root, "add", c.BodyRef)
			runGit(t, f.root, "commit", "-qm", "explain the criterion")
			head := runGit(t, f.root, "rev-parse", TrunkRef)
			if err := l.recordBlockAnswer(ctx, Candidate{ItemID: "item-a", HeadSHA: head, BlockConflict: &c}); err != nil {
				t.Fatal(err)
			}
			p := decisionFor(t, l, "item-a")
			claimed, nextWork, err := l.applyPlan(ctx, p, head, l.cfg.Now())
			if err != nil || !claimed {
				t.Fatalf("claim worker after answer: %v %v (%+v)", claimed, err, p.decision)
			}
			settleReport(t, f, l, nextWork, report(tc.next))
			l.cfg.Policy.Seats["design"] = "fixture"
			next, _ := conflictOf(t, f, "item-a")
			if tc.waiter == "design" {
				if next.Status != "raised" || next.WaitingOn != "design" || strings.Contains(next.Text, repeatedObstacleMarker) {
					t.Fatalf("new obstacle not raised for design: %+v", next)
				}
			} else if next.Status != "owner" || next.WaitingOn != "owner" || !strings.Contains(next.Text, "Held for the "+tc.waiter) || !strings.Contains(next.Text, report(tc.first)) || !strings.Contains(next.Text, report(tc.next)) {
				t.Fatalf("repeat not held with both reports: %+v", next)
			}
			d := decisionFor(t, l, "item-a").decision
			if tc.waiter == "design" {
				if d.Action != ActionDispatch || d.Seat != "design" {
					t.Fatalf("different obstacle should dispatch design: %+v", d)
				}
			} else if d.Action != ActionHold || d.Seat != tc.waiter {
				t.Fatalf("repeat must hold for %s: %+v", tc.waiter, d)
			}
			s := openFixture(t, f.storePath)
			defer s.Close()
			if err := s.ReadTx(ctx, func(r *store.Reader) error {
				faultsMap, err := ReadItemFaults(r)
				if err != nil {
					return err
				}
				if fault, ok := faultsMap["item-a"]; ok {
					t.Fatalf("unexpected item fault recorded: %+v", fault)
				}
				answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
				if err != nil {
					return err
				}
				mover := answer.Rows[0].Mover
				if !strings.HasSuffix(mover, "; answered obstacles: 1") {
					t.Fatalf("answer count missing from why: %s", mover)
				}
				if tc.waiter != "design" && (!strings.HasPrefix(mover, tc.waiter+";") || !strings.Contains(mover, collapseSingleLine(report(tc.first))) || !strings.Contains(mover, collapseSingleLine(report(tc.next)))) {
					t.Fatalf("why does not show both reports and the next actor: %s", mover)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDesignAnswerReachesNewAndResumedBuilderBrief(t *testing.T) {
	ctx := context.Background()
	template := filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")

	requestFixture := func(t *testing.T) (refusalLoopFixture, *Loop, string, string) {
		t.Helper()
		f := newRefusalLoopFixtureWithLanes(t, 1)
		steps := &obstacleSteps{recordingSteps: &recordingSteps{}}
		stager := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: template}
		l := f.loop(t, steps, stager)
		stager.Now = l.cfg.Now
		l.cfg.Policy.Backends = map[string]Backend{"fixture": {Adapter: "fixture"}}
		l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "fixture"}
		holdWALKeeper(t, f.storePath)
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		runGit(t, f.root, "update-ref", GreenRef, base)
		var itemID string
		fixtureWrite(t, f, func(tx *store.Tx) error {
			env, err := item.FileRequest(ctx, tx, item.RequestFiling{
				Title:       "Request needing design clarification",
				Words:       []byte("Clarify the API contract and implement it."),
				Criteria:    []string{"API contract is implemented"},
				Workflow:    item.SingleWorkerWorkflow,
				Ordinary:    true,
				Destination: item.Destination{To: "first"},
				By:          "operator",
				Via:         "cli",
				At:          l.cfg.Now(),
			})
			if err != nil {
				return err
			}
			itemID = env.ID
			return nil
		})
		return f, l, itemID, base
	}

	t.Run("filed-request/new-session", func(t *testing.T) {
		f, l, itemID, base := requestFixture(t)

		// First builder run raises a definition obstacle on the filed request.
		p1 := decisionFor(t, l, itemID)
		claimed, work1, err := l.applyPlan(ctx, p1, base, l.cfg.Now())
		if err != nil || !claimed {
			t.Fatalf("claim first builder: %v %v (%+v)", claimed, err, p1.decision)
		}
		settleReport(t, f, l, work1, "OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: API contract is unspecified\nRESULT: blocked")
		c, ok := conflictOf(t, f, itemID)
		if !ok || c.WaitingOn != "design" || c.BodyRef != "" {
			t.Fatalf("first obstacle conflict: %+v (%v)", c, ok)
		}

		// Design run lands a record under docs/design/.
		docRel := "docs/design/api-contract.md"
		docPath := filepath.Join(f.root, docRel)
		if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, docPath, "# API Contract\n\n## Settled API behavior\n\nReturn 200 with JSON.\n")
		runGit(t, f.root, "add", docRel)
		runGit(t, f.root, "commit", "-qm", "docs: settle API contract")
		head := runGit(t, f.root, "rev-parse", TrunkRef)
		c.DesignRunID = "run-design-1"
		if err := l.recordBlockAnswer(ctx, Candidate{ItemID: itemID, RunID: c.DesignRunID, BaseSHA: base, HeadSHA: head, BlockConflict: &c}); err != nil {
			t.Fatal(err)
		}

		// Next builder runs in a new session: its staged brief must carry the landed design record's path and heading.
		p2 := decisionFor(t, l, itemID)
		if p2.decision.Action != ActionDispatch || p2.decision.Reason != ReasonConflictAnswered || p2.decision.RunRequest.SessionTurn {
			t.Fatalf("expected new builder dispatch after design answer, got %+v", p2.decision)
		}
		claimed, work2, err := l.applyPlan(ctx, p2, head, l.cfg.Now())
		if err != nil || !claimed {
			t.Fatalf("claim second builder: %v %v", claimed, err)
		}
		if err := l.cfg.Stage.Stage(ctx, StageSpec{
			ItemID:     itemID,
			LaneID:     p2.decision.LaneID,
			RunID:      p2.decision.RunRequest.RunID,
			Backend:    p2.decision.RunRequest.Backend,
			WorkingDir: p2.decision.RunRequest.WorkingDir,
			BaseSHA:    head,
		}); err != nil {
			t.Fatal(err)
		}
		briefPath := filepath.Join(l.laneDirPath(p2.decision.LaneID), BriefFile)
		rawBrief, err := os.ReadFile(briefPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := probePreflight(t, briefPath); err != nil {
			t.Fatalf("preflight rejected brief with Design answers: %v", err)
		}
		brief := string(rawBrief)
		for _, want := range []string{"## Design answers", docRel, "# API Contract"} {
			if !strings.Contains(brief, want) {
				t.Fatalf("new builder brief missing %q:\n%s", want, brief)
			}
		}

		// If the next builder still raises the same obstacle with different EVIDENCE text,
		// it is held for the operator and no settle-failed fault is recorded.
		settleReport(t, f, l, work2, "OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: completely different phrasing of the same gap\nRESULT: blocked")
		next, ok := conflictOf(t, f, itemID)
		if !ok || next.Status != "owner" || next.WaitingOn != "owner" || !strings.Contains(next.Text, "Held for the operator") {
			t.Fatalf("repeated obstacle not held for operator: %+v", next)
		}
		d := decisionFor(t, l, itemID).decision
		if d.Action != ActionHold || d.Seat != "operator" {
			t.Fatalf("expected hold for operator, got %+v", d)
		}
		s := openFixture(t, f.storePath)
		defer s.Close()
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			faultsMap, err := ReadItemFaults(r)
			if err != nil {
				return err
			}
			if fault, ok := faultsMap[itemID]; ok {
				t.Fatalf("unexpected fault recorded for repeated obstacle: %+v", fault)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("filed-request/resumed-obstacle-call", func(t *testing.T) {
		f, l, itemID, base := requestFixture(t)
		declareWorkerResumeFixture(t, l)

		p1 := decisionFor(t, l, itemID)
		claimed, work1, err := l.applyPlan(ctx, p1, base, l.cfg.Now())
		if err != nil || !claimed {
			t.Fatalf("claim first builder: %v %v", claimed, err)
		}
		settleReport(t, f, l, work1, "OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: API contract is unspecified\nRESULT: blocked")
		c, ok := conflictOf(t, f, itemID)
		if !ok || c.WaitingOn != "design" {
			t.Fatalf("first obstacle: %+v", c)
		}
		laneDir := l.laneDirPath("lane-1")
		if err := os.MkdirAll(laneDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(laneDir, SessionIDFile), []byte("44444444-4444-4444-8444-444444444444\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(laneDir, BaseSHAFile), []byte(base+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return appendLandingCallEvent(tx, callOffered, "call-req-def", landingCall{
				landingOffer: landingOffer{CallID: "call-req-def", Tool: "obstacle", Base: base, Transcript: filepath.Join(laneDir, "def.jsonl")},
				ItemID:       itemID,
				RunID:        c.RunID,
				SessionID:    "44444444-4444-4444-8444-444444444444",
				RunLog:       filepath.Join(laneDir, c.RunID+".log"),
				Worktree:     l.laneTarget("lane-1"),
				Capability:   run.EndingCallSuspends,
				Tool:         "obstacle",
			}, l.cfg.Now())
		})

		docRel := "docs/design/resumed-answer.md"
		docPath := filepath.Join(f.root, docRel)
		if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, docPath, "# Resumed Answer\n\n## Chosen contract\n\nReturn 201.\n")
		runGit(t, f.root, "add", docRel)
		runGit(t, f.root, "commit", "-qm", "docs: add resumed answer")
		head := runGit(t, f.root, "rev-parse", TrunkRef)
		c.DesignRunID = "run-design-resumed"
		if err := l.recordBlockAnswer(ctx, Candidate{ItemID: itemID, RunID: c.DesignRunID, BaseSHA: base, HeadSHA: head, BlockConflict: &c}); err != nil {
			t.Fatal(err)
		}

		nextPlan := decisionFor(t, l, itemID)
		if !nextPlan.decision.RunRequest.SessionTurn || nextPlan.decision.Reason != ReasonConflictAnswered {
			t.Fatalf("expected resumed build turn for answered obstacle, got %+v", nextPlan.decision)
		}
		l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: template, Now: l.cfg.Now}
		next := applyReturnedTurn(t, l, nextPlan, head)
		raw, err := os.ReadFile(filepath.Join(run.DurableDir(laneDir, next.RunRequest.RunID), landingAnswerFile))
		if err != nil {
			t.Fatal(err)
		}
		var ans landingAnswer
		if err := json.Unmarshal(raw, &ans); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Design answer commit:", docRel, "# Resumed Answer"} {
			if !strings.Contains(ans.Text, want) {
				t.Fatalf("resumed obstacle answer missing %q:\n%s", want, ans.Text)
			}
		}
	})

	t.Run("next-actor-build/new-session", func(t *testing.T) {
		f, l, work := obstacleFixture(t, &obstacleSteps{}, "item-a")
		l.cfg.Policy.Seats["design"] = "fixture"
		settleReport(t, f, l, work, "OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: unclear helper\nRESULT: blocked")
		c, ok := conflictOf(t, f, "item-a")
		if !ok || c.WaitingOn != "design" {
			t.Fatalf("first obstacle: %+v", c)
		}
		designDecision := Decision{
			ItemID:        "item-a",
			BlockConflict: &c,
			RunRequest:    &run.Request{RunID: "run-design-obj"},
		}
		if err := l.reportedBlock(ctx, designDecision, "Use the existing helper in foo.go.\nNEXT-ACTOR: build\nRESULT: blocked\n"); err != nil {
			t.Fatal(err)
		}
		guidance, err := itemGuidanceBrief(ctx, f.storePath, "item-a", f.root)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"## Design answers", "Use the existing helper in foo.go."} {
			if !strings.Contains(guidance, want) {
				t.Fatalf("new builder guidance missing %q:\n%s", want, guidance)
			}
		}
	})
}

func TestLandedDesignRecordLogLineDoesNotSayItemLanded(t *testing.T) {
	ctx := context.Background()
	f, l, work := obstacleFixture(t, &obstacleSteps{}, "item-a")
	settleReport(t, f, l, work, "OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: unclear\nRESULT: blocked")
	c, ok := conflictOf(t, f, "item-a")
	if !ok || c.WaitingOn != "design" {
		t.Fatalf("first obstacle: %+v", c)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	path := filepath.Join(f.root, c.BodyRef)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, path, "# Clarified\n\n"+string(original))
	runGit(t, f.root, "add", c.BodyRef)
	runGit(t, f.root, "commit", "-qm", "docs: clarify item-a")
	head := runGit(t, f.root, "rev-parse", TrunkRef)
	laneWT := l.laneTarget("lane-1")
	if err := os.MkdirAll(filepath.Dir(laneWT), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(laneWT); os.IsNotExist(err) {
		runGit(t, f.root, "worktree", "add", "--detach", laneWT, head)
	} else {
		runGit(t, laneWT, "reset", "--hard", head)
	}

	var report bytes.Buffer
	l.cfg.Report = &report
	cand := Candidate{
		ItemID:        "item-a",
		LaneID:        "lane-1",
		RunID:         "run-design-1",
		Worktree:      laneWT,
		BaseSHA:       base,
		HeadSHA:       head,
		BlockConflict: &c,
	}
	var res LoopResult
	stop, landed, err := l.Verify(ctx, cand, &res, nil)
	if err != nil || stop != "" || !landed {
		s := openFixture(t, f.storePath)
		defer s.Close()
		var faultsMap map[string]ItemFault
		_ = s.ReadTx(ctx, func(r *store.Reader) error {
			faultsMap, _ = ReadItemFaults(r)
			return nil
		})
		t.Fatalf("Verify design candidate = (%q, %v, %v), want landed; faults=%+v; report:\n%s", stop, landed, err, faultsMap, report.String())
	}
	got := report.String()
	if !strings.Contains(got, "loop: landed design record for item-a ("+head+")") {
		t.Fatalf("expected design record log line, got:\n%s", got)
	}
	if strings.Contains(got, "loop: landed item-a") {
		t.Fatalf("design record log line must not say item landed:\n%s", got)
	}
}
