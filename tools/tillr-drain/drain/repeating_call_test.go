package drain

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

const repeatedArgs = `{"limit":80,"offset":300,"path":"tools/tillr-drain/drain/pass.go"}`

// endRepeating claims item-a's planned dispatch and settles its run as pi ending the
// session for a repeating call. It answers the settled decision.
func endRepeating(t *testing.T, f refusalLoopFixture, l *Loop) Decision {
	t.Helper()
	p := decisionFor(t, l, "item-a")
	if p.decision.Action != ActionDispatch {
		t.Fatalf("item-a is not dispatched: %+v", p.decision)
	}
	ok, work, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok || work == nil {
		t.Fatalf("claim: %v %v %+v", err, ok, work)
	}
	d := work.Decision
	rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndRepeatingCall, ArtifactRefs: []run.ArtifactRef{},
		RepeatingCall: &run.RepeatingCall{Tool: "read", Args: repeatedArgs, ArgsDigest: "digest", Streak: 40}}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	outcome := l.settle(context.Background(), d, rec, "", work.BaseSHA, "tick", l.cfg.Now(), &LoopResult{}, &PassTiming{}, nil, false)
	if outcome.err != nil || outcome.stop != "" {
		t.Fatalf("settle: %+v", outcome)
	}
	if err := l.markExamined(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d
}

func repeatedEndings(t *testing.T, f refusalLoopFixture) map[string]RepeatedCall {
	t.Helper()
	var out map[string]RepeatedCall
	fixtureRead(t, f, func(r *store.Reader) error {
		var err error
		out, err = readRepeatedCalls(r)
		return err
	})
	return out
}

// The owner's route for a session pi ended for a repeating call: a fresh session on the
// same backend, then the failover, then a hold naming the operator once the walk would
// return to a backend that already repeated. Nothing is counted: each step reads the
// chain the previous dispatch carried.
func TestARepeatingCallStartsFreshThenFailsOverThenHolds(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Policy.Backends["second"] = Backend{Adapter: "fixture"}
	l.cfg.Policy.Preference = []string{"fixture", "second"}

	first := endRepeating(t, f, l)
	if got := repeatedEndings(t, f)[first.RunRequest.RunID]; got.Route != repeatRouteFresh || got.NextBackend != "fixture" || got.Tool != "read" || got.Args != repeatedArgs || got.Streak != 40 {
		t.Fatalf("first ending recorded %+v", got)
	}
	fresh := decisionFor(t, l, "item-a").decision
	if fresh.Action != ActionDispatch || fresh.Reason != ReasonRepeatingCallFresh || fresh.RunRequest.Backend != "fixture" {
		t.Fatalf("not a fresh dispatch on the same backend: %+v", fresh)
	}
	if fresh.RepeatedCall == nil || fresh.RepeatedCall.RunID != first.RunRequest.RunID || fresh.RunRequest.SessionTurn || fresh.Returned != nil {
		t.Fatalf("the fresh dispatch does not carry the ending it answers, or continues a session: %+v", fresh)
	}

	second := endRepeating(t, f, l)
	if got := repeatedEndings(t, f)[second.RunRequest.RunID]; got.Route != repeatRouteFailover || got.NextBackend != "second" {
		t.Fatalf("second ending recorded %+v", got)
	}
	failover := decisionFor(t, l, "item-a").decision
	if failover.Action != ActionDispatch || failover.Reason != ReasonRepeatingCallFailover || failover.RunRequest.Backend != "second" {
		t.Fatalf("not the failover: %+v", failover)
	}

	third := endRepeating(t, f, l)
	if got := repeatedEndings(t, f)[third.RunRequest.RunID]; got.Route != repeatRouteHold || strings.Join(got.Backends, ",") != "fixture,second" {
		t.Fatalf("third ending recorded %+v", got)
	}
	held := decisionFor(t, l, "item-a").decision
	if held.Action != ActionHold || held.Reason != ReasonRepeatingCall || held.SupersedesRunID != third.RunRequest.RunID {
		t.Fatalf("not held for the operator: %+v", held)
	}
	if rule := holdrules.For(ReasonRepeatingCall); rule == holdrules.Unknown || !strings.Contains(rule.Mover, "operator") {
		t.Fatalf("the hold does not name the operator as its mover: %+v", rule)
	}
}

// The route is a function of the chain the dispatch carried and the backend list alone:
// a first ending starts fresh, a repeat fails over, and a walk that would return to a
// backend in the chain, or a list with no other backend, holds.
func TestARepeatingCallRouteReadsOnlyTheChain(t *testing.T) {
	policy := &Policy{Preference: []string{"a", "b", "c"}}
	for name, tc := range map[string]struct {
		prior                *RepeatedCall
		backend, route, next string
		chain                string
	}{
		"first ending":        {nil, "a", repeatRouteFresh, "a", "a"},
		"fresh session again": {&RepeatedCall{Backends: []string{"a"}}, "a", repeatRouteFailover, "b", "a"},
		"failover repeated":   {&RepeatedCall{Backends: []string{"a"}}, "b", repeatRouteFailover, "c", "a,b"},
		"walk would wrap":     {&RepeatedCall{Backends: []string{"a", "b"}}, "c", repeatRouteHold, "", "a,b,c"},
	} {
		t.Run(name, func(t *testing.T) {
			route, next, chain := repeatingRoute(policy, "", tc.prior, tc.backend)
			if route != tc.route || next != tc.next || strings.Join(chain, ",") != tc.chain {
				t.Fatalf("route %s next %q chain %v, want %s %q %s", route, next, chain, tc.route, tc.next, tc.chain)
			}
		})
	}
	if route, _, _ := repeatingRoute(&Policy{}, "", &RepeatedCall{Backends: []string{"a"}}, "a"); route != repeatRouteHold {
		t.Fatalf("a backend list with no other backend fails over: %s", route)
	}
}

func TestARepeatedCallBriefNamesTheCallAndThePreservedWork(t *testing.T) {
	brief := repeatedCallBrief(RepeatedCall{RunID: "run-item-a-a1", Tool: "read", Args: repeatedArgs, Streak: 40, NextBackend: "second"})
	for _, want := range []string{"## Your previous session", "run-item-a-a1", "40 times in a row", "read " + repeatedArgs, "new session on second", "not a count against the item", "refs/tillr/runs/run-item-a-a1", preservedPrefix + "run-item-a-a1"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief lacks %q:\n%s", want, brief)
		}
	}
}

// repeatingNodeSteps ends its first `repeat` runs as pi ending the session for a repeating
// call, each after committing a file the restart must preserve, and records every brief.
type repeatingNodeSteps struct {
	*restartWorkflowSteps
	repeat  int
	briefs  []string
	backend []string
}

func (s *repeatingNodeSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	brief, err := os.ReadFile(filepath.Join(s.f.laneRoot, req.LaneID, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	s.briefs, s.backend = append(s.briefs, string(brief)), append(s.backend, req.Backend)
	if len(s.briefs) > s.repeat {
		return s.restartWorkflowSteps.Run(ctx, req)
	}
	s.runs = append(s.runs, req)
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
		return run.Record{}, "", err
	}
	writeForkFile(s.t, filepath.Join(target, "tools/item-a/"+req.RunID+".txt"), "work before the loop\n")
	runGit(s.t, target, "add", ".")
	runGit(s.t, target, "commit", "-qm", "work before the loop")
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:00:00Z", EndCause: run.EndRepeatingCall, ArtifactRefs: []run.ArtifactRef{},
		RepeatingCall: &run.RepeatingCall{Tool: "read", Args: repeatedArgs, ArgsDigest: "digest", Streak: 40}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, "", err
	}
	defer st.Close()
	return rec, "", st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
}

func repeatingNodeFixture(t *testing.T, repeat int) (refusalLoopFixture, *Loop, *repeatingNodeSteps) {
	t.Helper()
	f, l, restart := restartGraphFixture(t)
	l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "fixture"}
	l.cfg.Policy.Preference = []string{"fixture", "alternate"}
	l.cfg.Policy.SeatLists = map[string][]string{"build": {"fixture", "alternate"}}
	steps := &repeatingNodeSteps{restartWorkflowSteps: restart, repeat: repeat}
	l.cfg.Steps = steps
	return f, l, steps
}

// A workflow node pi ends for a repeating call is dispatched again at once, on its lane,
// in a fresh session whose brief names the call; a repeat takes the seat's next backend;
// and once the walk would return to a backend that repeated, the item is held for the
// operator. The earlier run's work is preserved every time.
func TestARepeatingWorkflowNodeStartsFreshThenFailsOverThenHolds(t *testing.T) {
	for _, tc := range []struct {
		repeat  int
		runIDs  []string
		backend []string
	}{
		{1, []string{"run-item-a-a1-n1", "run-item-a-a1-n1-fresh"}, []string{"fixture", "fixture"}},
		{2, []string{"run-item-a-a1-n1", "run-item-a-a1-n1-fresh", "run-item-a-a1-n1-fresh-failover"}, []string{"fixture", "fixture", "alternate"}},
		{3, []string{"run-item-a-a1-n1", "run-item-a-a1-n1-fresh", "run-item-a-a1-n1-fresh-failover"}, []string{"fixture", "fixture", "alternate"}},
	} {
		t.Run(strconv.Itoa(tc.repeat), func(t *testing.T) {
			f, l, steps := repeatingNodeFixture(t, tc.repeat)
			result := &LoopResult{}
			for i := 0; i < 6; i++ {
				graphPass(t, l, result)
			}
			if len(steps.runs) < len(tc.runIDs) {
				t.Fatalf("runs %+v, want at least %v", steps.runs, tc.runIDs)
			}
			for i, want := range tc.runIDs {
				if steps.runs[i].RunID != want || steps.backend[i] != tc.backend[i] || steps.runs[i].LaneID != "lane-1" {
					t.Fatalf("run %d is %s on %s, want %s on %s", i, steps.runs[i].RunID, steps.backend[i], want, tc.backend[i])
				}
				if i > 0 && (!strings.Contains(steps.briefs[i], "## Your previous session") || !strings.Contains(steps.briefs[i], tc.runIDs[i-1]) || !strings.Contains(steps.briefs[i], repeatedArgs)) {
					t.Fatalf("brief of %s does not name the repeated call:\n%s", want, steps.briefs[i])
				}
			}
			for _, ended := range tc.runIDs[:min(tc.repeat, len(tc.runIDs))] {
				if got := runGit(t, f.root, "for-each-ref", "--format=%(refname)", preservedPrefix+ended, "refs/tillr/runs/"+ended); got == "" {
					t.Fatalf("the work of %s was not kept", ended)
				}
			}
			if tc.repeat < 3 {
				return
			}
			if len(steps.runs) != 3 {
				t.Fatalf("a held item ran again: %+v", steps.runs)
			}
			held := decisionFor(t, l, "item-a").decision
			if held.Action != ActionHold || held.Reason != ReasonRepeatingCall {
				t.Fatalf("not held for the operator: %+v", held)
			}
		})
	}
}
