package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// pieceScopeSteps runs a plan stage and two pieces, one/ and two/, each scoped to its own
// directory under the item's tools/item-a/. A piece's nth run does what acts[node][n] says:
// "inside" commits in its own directory, "escape" commits in the sibling piece's directory,
// "outside" commits outside the item's scope, and "plan-wrong" ends blocked saying the
// plan was wrong.
type pieceScopeSteps struct {
	*workflowSteps
	acts   map[string][]string
	nodes  []string
	briefs []string
}

func (s *pieceScopeSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	node := req.NodeID
	n := 0
	for _, seen := range s.nodes {
		if seen == node {
			n++
		}
	}
	s.nodes = append(s.nodes, node)
	lane := filepath.Join(s.f.laneRoot, req.LaneID)
	target := filepath.Join(lane, WorktreeDir)
	brief, err := os.ReadFile(filepath.Join(lane, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	s.briefs = append(s.briefs, string(brief))
	act := "inside"
	if acts := s.acts[node]; n < len(acts) {
		act = acts[n]
	}
	report := "RESULT: done\n"
	if node != "plan" {
		file := ""
		sibling := map[string]string{"one": "two", "two": "one"}[node]
		switch act {
		case "inside":
			file = "tools/item-a/" + node + "/result.txt"
		case "escape":
			file = "tools/item-a/" + sibling + "/from-" + node + ".txt"
		case "outside":
			file = "outside/from-" + node + ".txt"
		case "plan-wrong":
			report = "PLAN-WRONG: piece " + node + " needs tools/item-a/" + sibling + "/\nRESULT: blocked\n"
		}
		if file != "" {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(target, file)), 0755); err != nil {
				return run.Record{}, "", err
			}
			writeForkFile(s.t, filepath.Join(target, file), node+"\n")
			runGit(s.t, target, "add", ".")
			runGit(s.t, target, "commit", "-qm", node+" "+act)
		}
	}
	reportPath := filepath.Join(lane, "report.md")
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		return run.Record{}, "", err
	}
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func (s *pieceScopeSteps) Gate(context.Context, Candidate) (GateOutcome, error) {
	s.gates++
	return GateOutcome{Outcome: GatePassed}, nil
}

func pieceScopeFixture(t *testing.T, acts map[string][]string) (refusalLoopFixture, *Loop, *pieceScopeSteps) {
	t.Helper()
	f, l, old := workflowLoopFixture(t, "")
	steps := &pieceScopeSteps{workflowSteps: old, acts: acts}
	l.cfg.Steps = steps
	l.cfg.Policy.Seats["plan"] = "fixture"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
		return err
	})
	// A repository item's scope is the whole repository less the common deny-list.
	writeForkFile(t, filepath.Join(f.root, denyListPath), `{"entries":[{"path":"outside","reason":"not this item's to write"}]}`+"\n")
	raw := `{"name":"scout-then-build","version":1,"stages":[` +
		`{"id":"plan","kind":"agent","seat":"plan","access":"read_only"},` +
		`{"id":"one","kind":"agent","seat":"build","access":"write","write_scope":["tools/item-a/one/"]},` +
		`{"id":"two","kind":"agent","seat":"build","access":"write","write_scope":["tools/item-a/two/"]}],` +
		`"edges":[{"from_node":"plan","to_node":"one"},{"from_node":"one","to_node":"two"}]}`
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), raw)
	runGit(t, f.root, "add", "config")
	runGit(t, f.root, "commit", "-qm", "piece graph")
	return f, l, steps
}

// drivePieceScope runs passes until the item lands or a pass leaves it held.
func drivePieceScope(t *testing.T, l *Loop) (*LoopResult, Decision) {
	t.Helper()
	result := &LoopResult{}
	for i := 0; i < 24 && result.Landed == 0; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
		if result.Landed > 0 {
			break
		}
		for _, p := range mustPlan(t, l) {
			if p.decision.ItemID == "item-a" && p.decision.Action == ActionHold {
				return result, p.decision
			}
		}
	}
	return result, Decision{}
}

func pieceScopeRefusals(t *testing.T, f refusalLoopFixture) []refusalPayload {
	t.Helper()
	var out []refusalPayload
	fixtureRead(t, f, func(r *store.Reader) error {
		events, err := r.Events(0)
		if err != nil {
			return err
		}
		for _, e := range events {
			if e.Kind != RefusalKind {
				continue
			}
			var p refusalPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return err
			}
			out = append(out, p)
		}
		return nil
	})
	return out
}

// A piece that writes into its sibling's files, inside the item's scope, goes back to the
// builder with the escaped path named: the same piece runs again, from its own start, and
// the item lands with no operator hold and no second plan run.
func TestPieceScopeEscapeInsideItemReturnsToBuilder(t *testing.T) {
	f, l, steps := pieceScopeFixture(t, map[string][]string{"one": {"escape", "inside"}})
	result, held := drivePieceScope(t, l)
	if held.Reason != "" || result.Landed != 1 {
		t.Fatalf("held=%+v result=%+v nodes=%v", held, result, steps.nodes)
	}
	if got := strings.Join(steps.nodes, ","); got != "plan,one,one,two" {
		t.Fatalf("nodes ran %s, want plan,one,one,two", got)
	}
	refusals := pieceScopeRefusals(t, f)
	if len(refusals) != 1 || refusals[0].Step != RefusalFence || strings.Join(refusals[0].ScopeEscapes, ",") != "tools/item-a/two/from-one.txt" {
		t.Fatalf("refusals = %+v", refusals)
	}
	returned := steps.briefs[2]
	if !strings.Contains(returned, "tools/item-a/two/from-one.txt") || !strings.Contains(returned, "inside its write_scope") || !strings.Contains(returned, "plan-wrong:") {
		t.Fatalf("returned brief does not name the escaped path and both ways out:\n%s", returned)
	}
	// The returned piece starts from its own start, not from the escaping commit.
	if _, err := os.Stat(filepath.Join(f.root, "tools/item-a/two/from-one.txt")); err == nil {
		t.Fatal("the escaping commit landed")
	}
}

// A piece returned for a piece-scope escape that escapes again, or says it cannot finish
// without the paths, sends the item back to the plan step as a wrong plan, with the paths
// named, and no operator hold.
func TestPieceScopeEscapeInsideItemReturnsToPlanWhenThePieceCannotFinish(t *testing.T) {
	for name, second := range map[string]string{"escapes again": "escape", "builder says plan-wrong": "plan-wrong"} {
		t.Run(name, func(t *testing.T) {
			f, l, steps := pieceScopeFixture(t, map[string][]string{"one": {"escape", second, "inside"}})
			result, held := drivePieceScope(t, l)
			if held.Reason != "" || result.Landed != 1 {
				t.Fatalf("held=%+v result=%+v nodes=%v refusals=%+v", held, result, steps.nodes, pieceScopeRefusals(t, f))
			}
			if got := strings.Join(steps.nodes, ","); got != "plan,one,one,plan,one,two" {
				t.Fatalf("nodes ran %s, want plan,one,one,plan,one,two", got)
			}
			refusals := pieceScopeRefusals(t, f)
			if len(refusals) != 2 || refusals[0].Step != RefusalFence || refusals[1].Step != RefusalPlanWrong {
				t.Fatalf("refusals = %+v", refusals)
			}
			if !strings.Contains(refusals[1].Detail, "tools/item-a/two/from-one.txt") {
				t.Fatalf("plan-wrong refusal does not name the escaped path: %s", refusals[1].Detail)
			}
			if replan := steps.briefs[3]; !strings.Contains(replan, "tools/item-a/two/from-one.txt") {
				t.Fatalf("plan brief does not name the escaped path:\n%s", replan)
			}
		})
	}
}

// A write outside the item's own scope (here a deny-listed path) is still refused as
// boundary-scope-escape and held, with no return to the builder.
func TestPieceScopeEscapeOutsideItemStillHolds(t *testing.T) {
	f, l, steps := pieceScopeFixture(t, map[string][]string{"one": {"outside"}})
	result, held := drivePieceScope(t, l)
	if held.Reason != ReasonBoundaryScopeEscape || result.Landed != 0 {
		t.Fatalf("held=%+v result=%+v nodes=%v", held, result, steps.nodes)
	}
	if got := strings.Join(steps.nodes, ","); got != "plan,one" {
		t.Fatalf("nodes ran %s, want plan,one", got)
	}
	refusals := pieceScopeRefusals(t, f)
	if len(refusals) != 1 || refusals[0].Step != RefusalFence || len(refusals[0].ScopeEscapes) != 0 ||
		!strings.Contains(refusals[0].Detail, "boundary-scope-escape: outside/from-one.txt matches denied entry") {
		t.Fatalf("refusals = %+v", refusals)
	}
}
