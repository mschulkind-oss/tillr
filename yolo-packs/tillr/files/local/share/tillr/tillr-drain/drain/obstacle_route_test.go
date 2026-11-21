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
	"github.com/mschulkind-oss/tillr/store"
)

// obstacleSteps records the item states a route asks for and answers a base replay.
type obstacleSteps struct {
	*recordingSteps
	states []string
	red    []string
	known  bool
	// byRev, when set, answers each revision's red checks instead of red, and revs
	// records which revisions were replayed.
	byRev map[string][]string
	revs  []string
}

func (s *obstacleSteps) ItemState(_ context.Context, id, _, state string, waiting ...item.WaitingOn) error {
	entry := id + "=" + state
	if len(waiting) > 0 {
		entry += " " + waiting[0].Waiter + " (" + waiting[0].Reason + ")"
	}
	s.states = append(s.states, entry)
	return nil
}

func (s *obstacleSteps) Replay(_ context.Context, _ Candidate, rev string, _ []string) (ReplayOutcome, error) {
	s.revs = append(s.revs, rev)
	if s.byRev != nil {
		return ReplayOutcome{Rev: rev, Red: s.byRev[rev], Known: s.known}, nil
	}
	return ReplayOutcome{Rev: rev, Red: s.red, Known: s.known}, nil
}

// obstacleFixture claims item-a on a base that carries its block, the way a real dispatch
// does, and answers the loop and the claimed work.
func obstacleFixture(t *testing.T, steps *obstacleSteps, ids ...string) (refusalLoopFixture, *Loop, *pendingWork) {
	t.Helper()
	f := newRefusalLoopFixture(t, ids...)
	steps.recordingSteps = &recordingSteps{}
	l := f.loop(t, steps, &itemContentStager{})
	l.cfg.Policy.Backends = map[string]Backend{"fixture": {Adapter: "fixture"}, "stronger": {Adapter: "fixture"}}
	l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
	l.cfg.Policy.Escalate = map[string][]string{"build": {"fixture", "stronger"}}
	holdWALKeeper(t, f.storePath)
	base := commitBlock(t, f.root, ids[0], "- **c1:** the only version")
	runGit(t, f.root, "update-ref", GreenRef, base)
	p := decisionFor(t, l, ids[0])
	ok, work, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", err, ok)
	}
	return f, l, work
}

func settleReport(t *testing.T, f refusalLoopFixture, l *Loop, work *pendingWork, report string) {
	t.Helper()
	d := work.Decision
	rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{}}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	outcome := l.settle(context.Background(), d, rec, report, work.BaseSHA, "tick", l.cfg.Now(), &LoopResult{}, &PassTiming{}, nil, false)
	if outcome.err != nil {
		t.Fatalf("settle: %+v: %v", outcome, outcome.err)
	}
	if err := l.markExamined(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

func refusalOf(t *testing.T, f refusalLoopFixture, runID string) refusalPayload {
	t.Helper()
	var p refusalPayload
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var raw []byte
		if err := tx.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, RefusalKind, runID).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &p)
	})
	return p
}

// A report that is neither the commit nor a complete obstacle is a missing output: no
// conflict is raised, and the run goes back to its worker told what the two endings are.
func TestAnIncompleteObstacleGoesBackToItsWorker(t *testing.T) {
	for _, report := range []string{
		"Cannot finish this.\nRESULT: blocked\n",
		"I read the files and gave up.\nRESULT: failed\n",
		"OBSTACLE: fence\nEVIDENCE: needs more paths\nRESULT: blocked\n",
	} {
		t.Run(strings.SplitN(report, "\n", 2)[0], func(t *testing.T) {
			f, l, work := obstacleFixture(t, &obstacleSteps{}, "item-a")
			// The run committed nothing: its lane is still at its base.
			runGit(t, l.laneTarget(work.Decision.LaneID), "reset", "-q", "--hard", work.BaseSHA)
			settleReport(t, f, l, work, report)
			if c, ok := conflictOf(t, f, "item-a"); ok {
				t.Fatalf("an incomplete ending raised a conflict: %+v", c)
			}
			p := refusalOf(t, f, work.Decision.RunRequest.RunID)
			if p.Step != RefusalNoOp || !strings.Contains(p.Detail, "OBSTACLE: <kind>") {
				t.Fatalf("refusal %+v", p)
			}
		})
	}
}

// With the fence obstacle retired (owner ruling 2026-11-19), an old report naming
// OBSTACLE: fence is read as a missing output and returned to the worker, raising no conflict.
func TestAFenceObstacleAsksTheDesignSeatToWidenOrSplit(t *testing.T) {
	f, l, work := obstacleFixture(t, &obstacleSteps{}, "item-a")
	runGit(t, l.laneTarget(work.Decision.LaneID), "reset", "-q", "--hard", work.BaseSHA)
	settleReport(t, f, l, work, "OBSTACLE: fence\nPATHS: tools/tillr-view/waiting.go\nEVIDENCE: the view must show the hold\nRESULT: blocked\n")
	if c, ok := conflictOf(t, f, "item-a"); ok {
		t.Fatalf("retired fence obstacle raised a conflict: %+v", c)
	}
	p := refusalOf(t, f, work.Decision.RunRequest.RunID)
	if p.Step != RefusalNoOp || !strings.Contains(p.Detail, "OBSTACLE: <kind>") {
		t.Fatalf("refusal %+v, want missing-output return to worker", p)
	}
}

// An owner obstacle goes to the design seat with its question, never to a direct owner hold.
func TestAnOwnerObstacleGoesToTheDesignSeatWithItsQuestion(t *testing.T) {
	f, l, work := obstacleFixture(t, &obstacleSteps{}, "item-a")
	settleReport(t, f, l, work, "OBSTACLE: owner\nQUESTION: may the drain push to the remote?\nRESULT: blocked\n")
	c, ok := conflictOf(t, f, "item-a")
	if !ok || c.WaitingOn != "design" || !strings.Contains(c.Text, "may the drain push to the remote?") || !strings.Contains(c.Text, DesignNextActorField+": owner") {
		t.Fatalf("conflict %+v", c)
	}
}

// A trunk obstacle the base replay confirms holds the item behind the trunk's repair; one
// it does not confirm goes back to the worker with what the replay found.
func TestATrunkObstacleIsReplayedOnTheBase(t *testing.T) {
	report := "OBSTACLE: trunk\nCHECKS: go-build\nBASE: 0123\nRESULT: blocked\n"
	t.Run("confirmed", func(t *testing.T) {
		steps := &obstacleSteps{red: []string{"go-build"}, known: true}
		f, l, work := obstacleFixture(t, steps, "item-a")
		settleReport(t, f, l, work, report)
		p := refusalOf(t, f, work.Decision.RunRequest.RunID)
		if p.Step != RefusalTrunkAlreadyRed || !IsTrunkFixer(p.FixerItem) {
			t.Fatalf("refusal %+v", p)
		}
		if len(steps.states) != 1 || !strings.Contains(steps.states[0], "item:"+p.FixerItem) {
			t.Fatalf("states %v", steps.states)
		}
		if _, ok := conflictOf(t, f, "item-a"); ok {
			t.Fatal("a confirmed trunk obstacle raised a conflict")
		}
	})
	t.Run("refuted", func(t *testing.T) {
		steps := &obstacleSteps{known: true}
		f, l, work := obstacleFixture(t, steps, "item-a")
		settleReport(t, f, l, work, report)
		p := refusalOf(t, f, work.Decision.RunRequest.RunID)
		if p.Step != RefusalNoOp || !strings.Contains(p.Detail, "none of them red") || len(steps.states) != 0 {
			t.Fatalf("refusal %+v, states %v", p, steps.states)
		}
	})
}

// A tooling obstacle files one defect item per command, holds the item behind it with the
// tooling as mover, and the planner holds the item for that reason until it lands.
func TestAToolingObstacleWaitsOnOneDefectItem(t *testing.T) {
	steps := &obstacleSteps{}
	f, l, work := obstacleFixture(t, steps, "item-a")
	report := "OBSTACLE: tooling\nCOMMAND: go test ./tools/tillr-drain/...\nEVIDENCE:\npanic: test timed out after 10m0s\nPATHS: tools/tillr-drain/drain/hang_test.go\nRESULT: blocked\n"
	settleReport(t, f, l, work, report)
	defect := DefectItemID("go test ./tools/tillr-drain/...")
	p := refusalOf(t, f, work.Decision.RunRequest.RunID)
	if p.Step != RefusalToolingDefect || p.DefectItem != defect {
		t.Fatalf("refusal %+v", p)
	}
	if len(steps.states) != 1 || !strings.Contains(steps.states[0], "item:"+defect+" ("+RefusalToolingDefect+": ") {
		t.Fatalf("states %v", steps.states)
	}
	var paths, criterion, rank string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := tx.QueryRow(`SELECT allowed_paths, rank FROM items WHERE item_id=?`, defect).Scan(&paths, &rank); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT text FROM item_criteria WHERE item_id=? AND criterion_id=?`, defect, DefectCriterion).Scan(&criterion)
	})
	if !strings.Contains(paths, "hang_test.go") || rank != TrunkFixerRank || !strings.Contains(criterion, "no longer reproduces") || !strings.Contains(criterion, "timed out") || strings.Contains(strings.ToLower(criterion), "report") {
		t.Fatalf("defect item paths %s rank %s criterion %q", paths, rank, criterion)
	}
	// A second report of the same command attaches: one item, a wider fence.
	again := Obstacle{Kind: ObstacleTooling, Fields: map[string]string{"COMMAND": "go test  ./tools/tillr-drain/...", "EVIDENCE": "hung again", "PATHS": "tools/tillr-drain/drain/other_test.go"}}
	if id, err := l.fileDefectItem(context.Background(), "item-b", again); err != nil || id != defect {
		t.Fatalf("attach: %q %v", id, err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var count int
		if err := tx.QueryRow(`SELECT count(*) FROM items WHERE item_id LIKE 'defect-%'`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("%d defect items", count)
		}
		return tx.QueryRow(`SELECT allowed_paths FROM items WHERE item_id=?`, defect).Scan(&paths)
	})
	if !strings.Contains(paths, "other_test.go") || !strings.Contains(paths, "hang_test.go") {
		t.Fatalf("attached fence %s", paths)
	}
}

// A seat obstacle moves the item to the next backend on its seat's list for every later
// run, and is not an identical repeat of anything; on the last backend it asks the design
// seat for a plan.
func TestASeatObstacleMovesTheItemToAStrongerBackend(t *testing.T) {
	f, l, work := obstacleFixture(t, &obstacleSteps{}, "item-a")
	report := "OBSTACLE: seat\nTRIED: two designs for the lock order\nMISSING: a way to keep both locks\nRESULT: blocked\n"
	settleReport(t, f, l, work, report)
	p := refusalOf(t, f, work.Decision.RunRequest.RunID)
	if p.Step != RefusalNoOp || p.Evidence != "seat:fixture" || !strings.Contains(p.Detail, "moves to backend stronger") {
		t.Fatalf("refusal %+v", p)
	}
	next := decisionFor(t, l, "item-a")
	if next.decision.RunRequest == nil || next.decision.RunRequest.Backend != "stronger" {
		t.Fatalf("next dispatch %+v", next.decision)
	}
	// On the list's last backend the design seat is asked.
	d := work.Decision
	d.RunRequest = &run.Request{RunID: "run-item-a-strong", ItemID: "item-a", Backend: "stronger"}
	outcome, handled := l.routeObstacle(context.Background(), d, *d.RunRequest, d.LaneID, "tick", work.BaseSHA, parseObstacle(report), &LoopResult{})
	if handled || outcome.err != nil {
		t.Fatalf("the last backend was escalated again: %+v", outcome)
	}
}

// A seat obstacle from a run that changed nothing is a missing output: its evidence is the
// attempt, so the run goes back to its worker and the item stays on its backend.
func TestASeatObstacleWithNoAttemptGoesBackToItsWorker(t *testing.T) {
	f, l, work := obstacleFixture(t, &obstacleSteps{}, "item-a")
	runGit(t, l.laneTarget(work.Decision.LaneID), "reset", "-q", "--hard", work.BaseSHA)
	settleReport(t, f, l, work, "OBSTACLE: seat\nTRIED: read the code\nMISSING: the integration\nRESULT: blocked\n")
	p := refusalOf(t, f, work.Decision.RunRequest.RunID)
	if p.Step != RefusalNoOp || !strings.Contains(p.Detail, "names no attempt") {
		t.Fatalf("refusal %+v", p)
	}
	if next := decisionFor(t, l, "item-a"); next.decision.RunRequest != nil && next.decision.RunRequest.Backend == "stronger" {
		t.Fatalf("a seat obstacle with no attempt escalated: %+v", next.decision)
	}
}

// An obstacle submitted as a tool call routes identically to the same obstacle in a footer,
// and replies (design answers and refuted trunk claims) reach the session as the call result.
func TestObstacleCallRoutesEqualToFooterAndDeliversReplies(t *testing.T) {
	rawCases, err := os.ReadFile(filepath.Join("..", "..", "tillr-run", "backends", "pi", "testdata", "obstacle-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string `json:"name"`
		Report string `json:"report"`
		Ending bool   `json:"ending"`
		Kind   string `json:"kind"`
	}
	if err := json.Unmarshal(rawCases, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if !c.Ending || c.Kind == "" {
			continue
		}
		t.Run("route/"+c.Name, func(t *testing.T) {
			obs := parseObstacle(c.Report)
			runForm := func(useTool bool) (string, string, string) {
				steps := &obstacleSteps{known: true}
				if obs.Kind == ObstacleTrunk {
					steps.red = []string{"go-build"}
				}
				f, l, work := obstacleFixture(t, steps, "item-a")
				d := work.Decision
				report := c.Report
				if useTool {
					dir := run.DurableDir(l.laneDirPath(d.LaneID), d.RunRequest.RunID)
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(RunDir(f.root, d.RunRequest.RunID), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(RunDir(f.root, d.RunRequest.RunID), endingCapabilityFile), []byte(run.EndingCallSuspends+"\n"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(l.laneDirPath(d.LaneID), SessionIDFile), []byte("11111111-1111-4111-8111-111111111111\n"), 0600); err != nil {
						t.Fatal(err)
					}
					fields := make(map[string]string, len(obs.Fields))
					for k, v := range obs.Fields {
						fields[strings.ToLower(k)] = v
					}
					if err := writeLandingArtifact(dir, landingOfferFile, landingOffer{
						CallID:   "call-obs",
						Tool:     "obstacle",
						Base:     work.BaseSHA,
						Obstacle: &landingOfferObstacle{Kind: obs.Kind, Fields: fields},
					}); err != nil {
						t.Fatal(err)
					}
					report = ""
				}
				settleReport(t, f, l, work, report)
				var conflictWaiting, refusalStep string
				if conf, ok := conflictOf(t, f, "item-a"); ok {
					conflictWaiting = conf.WaitingOn
				}
				fixtureWrite(t, f, func(tx *store.Tx) error {
					var s string
					_ = tx.QueryRow(`SELECT COALESCE(json_extract(payload,'$.step'),'') FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, RefusalKind, d.RunRequest.RunID).Scan(&s)
					refusalStep = s
					return nil
				})
				var state string
				if len(steps.states) > 0 {
					state = steps.states[0]
				}
				return conflictWaiting, refusalStep, state
			}
			w1, r1, s1 := runForm(false)
			w2, r2, s2 := runForm(true)
			if w1 != w2 || r1 != r2 || (s1 != "") != (s2 != "") {
				t.Fatalf("footer=(%q,%q,%q) != tool=(%q,%q,%q)", w1, r1, s1, w2, r2, s2)
			}
		})
	}

	t.Run("reply/refuted-trunk", func(t *testing.T) {
		steps := &obstacleSteps{known: true}
		f, l, work := obstacleFixture(t, steps, "item-a")
		declareWorkerResumeFixture(t, l)
		d := work.Decision
		lane := l.laneDirPath(d.LaneID)
		if err := os.MkdirAll(RunDir(f.root, d.RunRequest.RunID), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(RunDir(f.root, d.RunRequest.RunID), endingCapabilityFile), []byte(run.EndingCallSuspends+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(lane, SessionIDFile), []byte("22222222-2222-4222-8222-222222222222\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(lane, BaseSHAFile), []byte(work.BaseSHA+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		dir := run.DurableDir(lane, d.RunRequest.RunID)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := writeLandingArtifact(dir, landingOfferFile, landingOffer{
			CallID:     "call-trunk",
			Tool:       "obstacle",
			Base:       work.BaseSHA,
			Transcript: "/tmp/trunk.jsonl",
			Obstacle:   &landingOfferObstacle{Kind: ObstacleTrunk, Fields: map[string]string{"checks": "go-build", "base": work.BaseSHA}},
		}); err != nil {
			t.Fatal(err)
		}
		settleReport(t, f, l, work, "")
		p := mustPlan(t, l)[0]
		if !p.decision.RunRequest.SessionTurn {
			t.Fatalf("refuted trunk did not plan session turn: %+v", p.decision)
		}
		l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
		next := applyReturnedTurn(t, l, p, work.BaseSHA)
		raw, err := os.ReadFile(filepath.Join(run.DurableDir(lane, next.RunRequest.RunID), landingAnswerFile))
		if err != nil {
			t.Fatal(err)
		}
		var ans landingAnswer
		if err := json.Unmarshal(raw, &ans); err != nil {
			t.Fatal(err)
		}
		if ans.CallID != "call-trunk" || ans.Kind != "obstacle-answer" || !strings.Contains(ans.Text, "none of them red") {
			t.Fatalf("unexpected trunk reply: %+v", ans)
		}
	})

	t.Run("reply/design-answer", func(t *testing.T) {
		f, l, _, base := designAnswerFixture(t)
		declareWorkerResumeFixture(t, l)
		// designAnswerFixture already raised conflict-a on item-a; record an obstacle call for its raising run.
		c, ok := conflictOf(t, f, "item-a")
		if !ok {
			t.Fatal("missing conflict")
		}
		lane := l.laneDirPath("lane-1")
		if err := os.MkdirAll(lane, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(lane, SessionIDFile), []byte("33333333-3333-4333-8333-333333333333\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(lane, BaseSHAFile), []byte(base+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		backend := "fixture"
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if err := appendEffect(tx, dispatchStartedKind, c.RunID, pendingWork{Done: true, Decision: Decision{ItemID: "item-a", RunRequest: &run.Request{RunID: c.RunID, Backend: backend}}}, l.cfg.Now()); err != nil {
				return err
			}
			if err := appendEffect(tx, examinationDoneKind, c.RunID, struct{}{}, l.cfg.Now()); err != nil {
				return err
			}
			return appendLandingCallEvent(tx, callOffered, "call-def", landingCall{
				landingOffer: landingOffer{CallID: "call-def", Tool: "obstacle", Base: base, Transcript: "/tmp/def.jsonl"},
				ItemID:       "item-a",
				RunID:        c.RunID,
				SessionID:    "33333333-3333-4333-8333-333333333333",
				RunLog:       filepath.Join(lane, c.RunID+".log"),
				Worktree:     l.laneTarget("lane-1"),
				Capability:   run.EndingCallSuspends,
				Tool:         "obstacle",
			}, l.cfg.Now())
		})
		cand := Candidate{ItemID: "item-a", RunID: "run-design-1", BaseSHA: base, HeadSHA: base, BlockConflict: &c}
		if err := l.recordBlockAnswer(context.Background(), cand); err != nil {
			t.Fatal(err)
		}
		nextPlan := mustPlan(t, l)[0]
		if !nextPlan.decision.RunRequest.SessionTurn || nextPlan.decision.Reason != ReasonConflictAnswered {
			t.Fatalf("expected resumed build turn for answered obstacle, got %+v", nextPlan.decision)
		}
		l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
		next := applyReturnedTurn(t, l, nextPlan, base)
		raw, err := os.ReadFile(filepath.Join(run.DurableDir(lane, next.RunRequest.RunID), landingAnswerFile))
		if err != nil {
			t.Fatal(err)
		}
		var ans landingAnswer
		if err := json.Unmarshal(raw, &ans); err != nil {
			t.Fatal(err)
		}
		if ans.CallID != "call-def" || ans.Kind != "design-answer" || !strings.Contains(ans.Text, "Design answer commit:") {
			t.Fatalf("unexpected design answer reply: %+v", ans)
		}
	})
}
