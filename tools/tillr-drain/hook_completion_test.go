package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func hookFixture(t *testing.T, answer string, verdict bool) (loopFixture, *drain.ProcessSteps, *drain.LaneStager) {
	t.Helper()
	root := scratch(t, "hook-root")
	db := fixture(t, fencedItemRecord("finding", "in-queue"))
	f := loopFixture{root: root, store: db, laneRoot: filepath.Join(scratch(t, "hook-lanes"), "lanes"), count: filepath.Join(root, "calls"), policy: &drain.Policy{DefaultBackend: "fixture", Preference: []string{"fixture"}, Backends: map[string]drain.Backend{"fixture": {Adapter: stub(t, "adapter", "printf 'RESULT: done\\n'"), Model: "fixture", TimeoutSeconds: 60}}}}
	itemBin := sibling(t, "item")
	sibling(t, "view") // Build observers before installing the Git sentinel.
	body := "set -e\necho called >> " + f.count + "\n"
	if verdict {
		// Read frozen criterion text and record through the real item utility.
		body += fmt.Sprintf(`python3 - "$@" <<'PY'
import sys, subprocess, json, hashlib
args=dict(zip(sys.argv[2::2],sys.argv[3::2]))
p=json.loads(subprocess.check_output([%q,'show','--store',args['--store'],'--item',args['--item']]))['payload']
for c in p['criteria']:
 v={'record_version':1,'kind':'item.verdict','id':'verdict-'+c['criterion_id'],'producer':{'system':'tillr','utility':'item','version':'0.1.0'},'created_at':'2026-11-17T00:00:00Z','payload':{'item_id':p['item_id'],'criterion_id':c['criterion_id'],'criterion_hash':hashlib.sha256(c['text'].encode()).hexdigest(),'verdict':'done','claim_class':'verification-only','reason':'checked','evidence':['doc:evidence.md'],'actor_kind':'human','actor':'owner'}}
 path=args['--root']+'/verdict.json'
 with open(path,'w') as f: json.dump(v,f)
 subprocess.run([%q,'verdict','record','--store',args['--store'],'--input',path,'--now','2026-11-17T00:00:00Z'],stdout=subprocess.DEVNULL,check=True)
PY
`, itemBin, itemBin)
	}
	hook := stub(t, "hook", body+answer)
	d := plannerFixtureDefinition(t)
	d.Ready = "in-queue"
	d.Transitions = append(d.Transitions, definition.Transition{From: "in-queue", To: "confirmed"})
	d.BriefTemplate = "template.md"
	d.Hooks.Argv = []string{hook}
	writeFile(t, filepath.Join(root, d.BriefTemplate), "Finding {{ITEM_ID}}\n{{TASK}}\n")
	setPlannerDefinition(t, db, d)
	steps := f.steps(t, "", "", "")
	steps.ConfigureHooks(d.Hooks, nil)
	stager := &drain.LaneStager{Root: root, Store: db, LaneRoot: f.laneRoot, Now: steps.Now}
	gitSentinel := filepath.Join(root, "git-called")
	forbidden := stub(t, "git", "echo called \"$*\" >> "+gitSentinel+"\nexit 99")
	t.Setenv("PATH", filepath.Dir(forbidden)+":"+os.Getenv("PATH"))
	t.Cleanup(func() {
		if b, err := os.ReadFile(gitSentinel); err == nil {
			t.Errorf("Git ran: %s", b)
		}
	})
	return f, steps, stager
}

func TestHookCompletionRelativePaths(t *testing.T) {
	f, steps, _ := hookFixture(t, `echo '{"state":"confirmed","reason":"evidence checked","resolves_via":"doc:evidence.md"}'`, true)
	cwd := filepath.Dir(f.root)
	t.Chdir(cwd)
	steps.Root = filepath.Base(f.root)
	var err error
	steps.Store, err = filepath.Rel(cwd, f.store)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.OpenRead(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	var d definition.Definition
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		rec, err := definition.ForReader(r)
		d = rec.Parsed
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if done, err := steps.CompleteItem(context.Background(), "finding", "relative-run", d); err != nil || !done {
		t.Fatalf("relative completion: done=%v err=%v", done, err)
	}
	assertHookReceipt(t, f)
}

func TestHookCompletionParallelPlainDirectory(t *testing.T) {
	f, steps, stager := hookFixture(t, `echo '{"state":"confirmed","reason":"evidence checked","resolves_via":"doc:evidence.md"}'`, true)
	f.policy.Seats = map[string]string{"build": "fixture"}
	if err := os.MkdirAll(filepath.Join(f.root, "config/tillr/workflows"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, "config/tillr/workflows/readers@v1.json"), `{"name":"readers","version":1,"stages":[{"id":"first","kind":"agent","seat":"build","access":"read_only"},{"id":"second","kind":"agent","seat":"build","access":"read_only"}],"edges":[]}`)
	s, err := store.Open(context.Background(), f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='readers@v1' WHERE item_id='finding'`)
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.newLoopWith(t, 1, steps, stager, os.Stderr).Run(context.Background())
	if err != nil || result.Landed != 0 || result.Candidates != 0 {
		t.Fatalf("parallel completion: %+v %v", result, err)
	}
	assertHookReceipt(t, f)
}

func TestHookCompletionCommandWorkflowDirectoryAndEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		commandFirst, evaluator bool
	}{
		{name: "command-first", commandFirst: true},
		{name: "command-last"},
		{name: "evaluator-refused", commandFirst: true, evaluator: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			commandFirst := tc.commandFirst
			f, steps, stager := hookFixture(t, `echo '{"state":"confirmed","reason":"evidence checked","resolves_via":"doc:evidence.md"}'`, true)
			f.policy.Seats = map[string]string{"build": "fixture"}
			f.policy.SeatLists = map[string][]string{"build": {"fixture"}}
			if err := os.MkdirAll(filepath.Join(f.root, "config/tillr/workflows"), 0755); err != nil {
				t.Fatal(err)
			}
			observations := filepath.Join(scratch(t, "node-observations"), "calls")
			program := func(name string) string {
				return stub(t, name, fmt.Sprintf("set -eu\nprintf '%%s|%%s|%%s|%%s\\n' %s \"$PWD\" \"${TILLR_ITEM_ID-}\" \"${TILLR_NODE_ID-}\" >> %q\nprintf 'RESULT: done\\n'", name, observations))
			}
			backend := f.policy.Backends["fixture"]
			backend.Adapter = program("agent")
			f.policy.Backends["fixture"] = backend
			writeFile(t, filepath.Join(f.root, "config/tillr/commands.json"), fmt.Sprintf(`{"probe":{"argv":[%q],"flags":{}}}`, program("command")))
			command := `{"id":"probe","kind":"command","command_ref":"probe","access":"read_only"}`
			agent := `{"id":"investigate","kind":"agent","seat":"build","access":"read_only","prompt":"Investigate the reproduction"}`
			if tc.evaluator {
				agent = strings.Replace(agent, `"access":"read_only"`, `"access":"read_only","judge_classes":["verification-only"]`, 1)
			}
			first, second, from, to := command, agent, "probe", "investigate"
			if !commandFirst {
				first, second, from, to = agent, command, to, from
			}
			exit := ""
			if tc.evaluator {
				exit = `,"exit":{"requires_nodes":["probe","investigate"],"evaluator_slot":"investigate"}`
			}
			writeFile(t, filepath.Join(f.root, "config/tillr/workflows/probe@v1.json"), fmt.Sprintf(`{"name":"probe","version":1,"stages":[%s,%s],"edges":[{"from_node":%q,"to_node":%q}]%s}`, first, second, from, to, exit))
			s, err := store.Open(context.Background(), f.store, true)
			if err != nil {
				t.Fatal(err)
			}
			err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET workflow='probe@v1' WHERE item_id='finding'`)
				return err
			})
			s.Close()
			if err != nil {
				t.Fatal(err)
			}
			result, err := f.newLoopWith(t, 1, steps, stager, os.Stderr).Run(context.Background())
			if tc.evaluator {
				if err != nil || result.Dispatched != 0 || !strings.Contains(result.Detail, "workflow-evaluator-requires-landing") {
					t.Fatalf("evaluator refusal: %+v %v", result, err)
				}
				if _, err := os.Stat(f.count); !os.IsNotExist(err) {
					t.Fatalf("refused evaluator invoked completion hook: %v", err)
				}
				return
			}
			if err != nil || result.Dispatched != 2 || result.Landed != 0 || result.Candidates != 0 {
				t.Fatalf("command completion: %+v %v", result, err)
			}
			cwd := filepath.Join(f.laneRoot, "lane-1", drain.PlainDir)
			wantCommand := "command|" + cwd + "|finding|probe\n"
			wantAgent := "agent|" + cwd + "|finding|investigate\n"
			want := wantCommand + wantAgent
			if !commandFirst {
				want = wantAgent + wantCommand
			}
			got, err := os.ReadFile(observations)
			if err != nil || string(got) != want {
				t.Fatalf("node programs: got %q, want %q: %v", got, want, err)
			}
			agentRun := "run-finding-a1-n1"
			if commandFirst {
				agentRun = "run-finding-a1-n2"
			}
			brief := readAll(t, filepath.Join(drain.RunDir(f.root, agentRun), drain.BriefFile))
			if !strings.Contains(brief, "Node task: Investigate the reproduction") {
				t.Fatalf("agent node prompt missing: %s", brief)
			}
			if commandFirst && !strings.Contains(brief, "Read the previous node report:") {
				t.Fatalf("command report handoff missing: %s", brief)
			}
			assertHookReceipt(t, f)
		})
	}
}

func TestHookCompletionPlainDirectory(t *testing.T) {
	f, steps, stager := hookFixture(t, `printf '%s\n' '{"state":"confirmed","reason":"evidence checked","resolves_via":"doc:evidence.md"}'`, true)
	result, err := f.newLoopWith(t, 1, steps, stager, os.Stderr).Run(context.Background())
	if err != nil || result.Dispatched != 1 || result.Landed != 0 || result.Candidates != 0 {
		t.Fatalf("result: %+v %v", result, err)
	}
	assertHookReceipt(t, f)
}

func assertHookReceipt(t *testing.T, f loopFixture) {
	t.Helper()
	s, err := store.OpenRead(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		var state, reason, by, pointer string
		if err := r.QueryRow(`SELECT state,lifecycle_reason,lifecycle_by,resolves_via FROM items WHERE item_id='finding'`).Scan(&state, &reason, &by, &pointer); err != nil {
			return err
		}
		if state != "confirmed" || reason != "evidence checked" || by != "definition:findings" || pointer != "doc:evidence.md" {
			t.Fatalf("receipt: %s %s %s %s", state, reason, by, pointer)
		}
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind='item.update' AND json_extract(payload,'$.state')='confirmed'`).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("completion events: %d", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(f.count)
	if err != nil || strings.Count(string(calls), "called") != 1 {
		t.Fatalf("hook calls: %s %v", calls, err)
	}
}

func TestHookCompletionHoldsAndNegativeAnswer(t *testing.T) {
	for _, tc := range []struct{ name, answer, want string }{
		{"unjudged", `echo '{"state":"confirmed","reason":"unchecked"}'`, "criteria-unjudged"},
		{"negative", "exit 1", "candidate-no-op"},
		{"fault", "exit 3", "complete-item-fault"},
		{"bad-json", "echo invalid", "complete-item-fault"},
		{"not-finished", `echo '{"state":"in-queue","reason":"bad"}'`, "complete-item-fault"},
		{"missing-reason", `echo '{"state":"confirmed"}'`, "complete-item-fault"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, steps, stager := hookFixture(t, tc.answer, false)
			result, err := f.newLoopWith(t, 1, steps, stager, os.Stderr).Run(context.Background())
			if err != nil {
				t.Fatalf("loop: %+v %v", result, err)
			}
			cmd := exec.Command(sibling(t, "view"), "waiting", "--store", f.store, "--root", f.root, "--history")
			out, runErr := cmd.CombinedOutput()
			code := 0
			if runErr != nil {
				code = 1
			}
			if tc.name == "negative" {
				// A refused run goes back to its worker uncounted (the owner's ruling of
				// 2026-11-17), so the hook's exit 1 is refused as a no-op twice: once
				// returned with its evidence, and once as the identical repeat.
				// The loop then drains with the item blocked for the owner, not held
				// queued with nothing to move it.
				if result.Dispatched != 2 || result.Rejected != 2 || result.RefusedSteps[drain.RefusalNoOp] != 2 || result.Stop != "queue-drained" {
					t.Fatalf("negative answer: %+v", result)
				}
			} else if code != 0 || !strings.Contains(string(out), tc.want) {
				t.Fatalf("waiting: %d %s", code, out)
			}
			s, err := store.OpenRead(context.Background(), f.store)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				var state string
				err := r.QueryRow(`SELECT state FROM items WHERE item_id='finding'`).Scan(&state)
				// The repeat of the negative answer is the one case the owner is
				// asked about; every other answer leaves the item queued.
				want := "in-queue"
				if tc.name == "negative" {
					want = "blocked"
				}
				if state != want {
					t.Errorf("item state %s, want %s", state, want)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Stop at the real subprocess boundary after the move is committed, before the
// caller can write examination records. Restart uses the durable pending run.
type stopAfterHookMove struct{ *drain.ProcessSteps }

func (s stopAfterHookMove) CompleteItem(ctx context.Context, itemID, runID string, d definition.Definition) (bool, error) {
	done, err := s.ProcessSteps.CompleteItem(ctx, itemID, runID, d)
	if err == nil && done {
		return done, context.Canceled
	}
	return done, err
}
func TestHookCompletionRestartAfterMove(t *testing.T) {
	f, steps, stager := hookFixture(t, `echo '{"state":"confirmed","reason":"evidence checked","resolves_via":"doc:evidence.md"}'`, true)
	newLoop := func(s drain.Steps) *drain.Loop {
		l, err := drain.NewLoop(drain.LoopConfig{Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{"lane-1"}, Policy: f.policy, Steps: s, Stage: stager, Now: steps.Now, Report: os.Stderr, ExitWhenDrained: true, BackoffBase: time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	if _, err := newLoop(stopAfterHookMove{steps}).Run(context.Background()); err == nil {
		t.Fatal("stop boundary not reached")
	}
	assertHookReceipt(t, f)
	if _, err := newLoop(steps).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertHookReceipt(t, f)
	s, err := store.OpenRead(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	examined := 0
	for _, e := range events {
		if e.Kind == "drain.examined" {
			examined++
		}
		if e.Kind == "drain.candidate-ready" {
			var p map[string]any
			json.Unmarshal(e.Payload, &p)
			t.Fatalf("candidate: %v", p)
		}
	}
	if examined != 1 {
		t.Fatalf("missing/duplicate end records: %d", examined)
	}
}
