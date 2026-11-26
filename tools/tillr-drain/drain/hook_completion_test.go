package drain_test

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
	f := loopFixture{root: root, store: db, laneRoot: filepath.Join(scratch(t, "hook-lanes"), "lanes"), count: filepath.Join(root, "calls"), policy: &drain.Policy{DefaultBackend: "fixture", Backends: map[string]drain.Backend{"fixture": {Adapter: stub(t, "adapter", "printf 'RESULT: done\\n'"), Model: "fixture", TimeoutSeconds: 0}}}}
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
	if done, err := steps.CompleteItem(context.Background(), "finding", "relative-run", "", d); err != nil || !done {
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

func (s stopAfterHookMove) CompleteItem(ctx context.Context, itemID, runID, artifactDir string, d definition.Definition) (bool, error) {
	done, err := s.ProcessSteps.CompleteItem(ctx, itemID, runID, artifactDir, d)
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

func TestNonLandingLoopInsideEnclosingGitRepo(t *testing.T) {
	outer := scratch(t, "enclosing-repo")
	if out, err := exec.Command("git", "init", "-q", "-b", "main", outer).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", outer, "config", "user.name", "Test User").CombinedOutput(); err != nil {
		t.Fatalf("git config name: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", outer, "config", "user.email", "test@example.com").CombinedOutput(); err != nil {
		t.Fatalf("git config email: %v: %s", err, out)
	}
	writeFile(t, filepath.Join(outer, "README.md"), "outer repo\n")
	if out, err := exec.Command("git", "-C", outer, "add", "README.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", outer, "commit", "-q", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	// Pre-populate the enclosing repo's .tillr directory so we can prove it is untouched.
	outerTillr := filepath.Join(outer, ".tillr")
	if err := os.MkdirAll(filepath.Join(outerTillr, "loop"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(outerTillr, "sentinel.txt"), "keep-me\n")
	writeFile(t, filepath.Join(outerTillr, "loop", "existing.json"), "{}\n")
	writeFile(t, filepath.Join(outer, ".gitignore"), ".tillr/\n")
	if out, err := exec.Command("git", "-C", outer, "add", ".gitignore").CombinedOutput(); err != nil {
		t.Fatalf("git add .gitignore: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", outer, "commit", "-q", "-m", "ignore tillr").CombinedOutput(); err != nil {
		t.Fatalf("git commit .gitignore: %v: %s", err, out)
	}

	// Plain directory inside the enclosing Git repo's tree.
	plainRoot := filepath.Join(outer, "sub", "plain")
	plainStore := filepath.Join(plainRoot, ".tillr", "state.db")
	plainLanes := filepath.Join(plainRoot, ".tillr", "lanes")
	if err := os.MkdirAll(filepath.Dir(plainStore), 0o755); err != nil {
		t.Fatal(err)
	}

	// Build sibling binaries before installing the git spy on PATH.
	runBin := sibling(t, "run")
	itemBin := sibling(t, "item")
	sibling(t, "view")

	// Locate the real run-pi adapter in this tree.
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	runPi := filepath.Join(repoRoot, "tools", "tillr-run", "backends", "pi", "run-pi")
	backendsDir := filepath.Join(repoRoot, "tools", "tillr-run", "backends")

	// Seed the non-landing store with one queued item.
	s, err := store.Open(context.Background(), plainStore, true)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	s.Close()
	itemJSON := fencedItemRecord("finding", "in-queue")
	itemInput := filepath.Join(scratch(t, "item-input"), "item.json")
	writeFile(t, itemInput, itemJSON)
	if out, err := exec.Command(itemBin, "apply", "--store", plainStore, "--input", itemInput, "--now", "2026-11-25T00:00:00Z").CombinedOutput(); err != nil {
		t.Fatalf("item apply: %v: %s", err, out)
	}

	hookWorkFile := filepath.Join(plainRoot, "hook-worked.txt")
	hookScript := fmt.Sprintf(`set -eu
python3 - "$@" <<'PY'
import sys, subprocess, json, hashlib, os
args = dict(zip(sys.argv[2::2], sys.argv[3::2]))
root = args['--root']
with open(os.path.join(root, 'hook-worked.txt'), 'w') as f:
    f.write(args['--run'] + '\n')
p = json.loads(subprocess.check_output([%q, 'show', '--store', args['--store'], '--item', args['--item']]))['payload']
for c in p['criteria']:
    v = {
        'record_version': 1, 'kind': 'item.verdict', 'id': 'verdict-' + c['criterion_id'],
        'producer': {'system': 'tillr', 'utility': 'item', 'version': '0.1.0'},
        'created_at': '2026-11-25T00:00:00Z',
        'payload': {
            'item_id': p['item_id'], 'criterion_id': c['criterion_id'],
            'criterion_hash': hashlib.sha256(c['text'].encode()).hexdigest(),
            'verdict': 'done', 'claim_class': 'verification-only',
            'reason': 'checked', 'evidence': ['doc:evidence.md'],
            'actor_kind': 'human', 'actor': 'owner',
        },
    }
    vp = os.path.join(root, 'verdict.json')
    with open(vp, 'w') as f:
        json.dump(v, f)
    subprocess.run([%q, 'verdict', 'record', '--store', args['--store'], '--input', vp, '--now', '2026-11-25T00:00:00Z'], stdout=subprocess.DEVNULL, check=True)
PY
echo '{"state":"confirmed","reason":"evidence checked","resolves_via":"doc:evidence.md"}'
`, itemBin, itemBin)
	hook := stub(t, "hook", hookScript)

	d := plannerFixtureDefinition(t)
	d.Ready = "in-queue"
	d.Transitions = append(d.Transitions, definition.Transition{From: "in-queue", To: "confirmed"})
	d.BriefTemplate = "template.md"
	d.Hooks.Argv = []string{hook}
	plainBriefTemplate := `# {{ITEM_ID}} — {{TITLE}}

## Header
- **Item:** {{ITEM_ID}}
- **Run:** {{RUN_ID}}
- **Backend:** {{BACKEND}}
- **Target:** {{TARGET}} ({{TARGET_KIND}})
- **Date:** {{DATE}}
- **Timeout:** {{TIMEOUT_SECONDS}}

## Task
{{TASK}}
{{CRITERION_1}}

## Scope and boundaries
Plain directory {{TARGET}}, scratch {{DURABLE_DIR}}.

## Seats
{{SEAT_LINES}}

## Settled decisions
{{SETTLED}}

## Facts carried
{{FACTS}}

## Method
{{METHOD}}

## Verification commands
{{VERIFICATION}}

## Report shape
End with RESULT: done.

## Stop conditions
None.
`
	writeFile(t, filepath.Join(plainRoot, d.BriefTemplate), plainBriefTemplate)
	setPlannerDefinition(t, plainStore, d)

	// Fake pi binary that emits a minimal JSONL stream with RESULT: done and runs a bash command
	// so the recording seam (BASH_ENV / TILLR_TRACE_DIR) writes proc-exec.jsonl.
	binDir := scratch(t, "spy-bin")
	piStub := filepath.Join(binDir, "pi")
	piBody := `#!/bin/sh
if [ "${1:-}" = "--features" ]; then
    printf '[]\n'
    exit 0
fi
cat >/dev/null
bash -c 'true'
printf '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"RESULT: done"}]}}\n'
`
	if err := os.WriteFile(piStub, []byte(piBody), 0o755); err != nil {
		t.Fatal(err)
	}
	// Git spy: records any `git rev-parse` of top-level or common dir and fails it.
	gitRevParseLog := filepath.Join(scratch(t, "git-spy"), "rev-parse.log")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	gitSpy := filepath.Join(binDir, "git")
	gitSpyBody := fmt.Sprintf(`#!/bin/sh
case " $* " in
    *"rev-parse"*"--show-toplevel"*|*"rev-parse"*"--git-common-dir"*)
        printf '%%s\n' "$*" >> %q
        exit 99
        ;;
esac
exec %q "$@"
`, gitRevParseLog, realGit)
	if err := os.WriteFile(gitSpy, []byte(gitSpyBody), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(plainRoot)

	policy := &drain.Policy{
		DefaultBackend: "pi",
		Backends: map[string]drain.Backend{
			"pi": {Adapter: runPi, Model: "stub-model", Effort: "low", TimeoutSeconds: 0},
		},
	}
	now := func() time.Time { return time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC) }
	steps := &drain.ProcessSteps{
		RunBin:      runBin,
		ItemBin:     itemBin,
		Store:       plainStore,
		LaneRoot:    plainLanes,
		Root:        plainRoot,
		BackendsDir: backendsDir,
		Now:         now,
	}
	steps.ConfigureHooks(d.Hooks, nil)
	stager := &drain.LaneStager{Root: plainRoot, Store: plainStore, LaneRoot: plainLanes, Now: now}
	loop, err := drain.NewLoop(drain.LoopConfig{
		Root:            plainRoot,
		Store:           plainStore,
		LaneRoot:        plainLanes,
		Lanes:           []string{"lane-1"},
		Policy:          policy,
		Steps:           steps,
		Stage:           stager,
		Now:             now,
		Report:          os.Stderr,
		ExitWhenDrained: true,
		BackoffBase:     time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop.Run: %v (result=%+v)", err, result)
	}
	if result.Dispatched != 1 || result.Landed != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}

	// 1. No git rev-parse of top-level or common directory was run.
	if calls, err := os.ReadFile(gitRevParseLog); err == nil && strings.TrimSpace(string(calls)) != "" {
		t.Fatalf("git rev-parse was called on non-landing loop:\n%s", calls)
	}

	// 2. Run directories, lanes, and hook working files are all under plainRoot.
	loopEntries, err := os.ReadDir(filepath.Join(plainRoot, ".tillr", "loop"))
	if err != nil || len(loopEntries) == 0 {
		t.Fatalf("expected run directories under %s/.tillr/loop, got entries=%v err=%v", plainRoot, loopEntries, err)
	}
	if _, err := os.Stat(filepath.Join(plainLanes, "lane-1", "work")); err != nil {
		t.Fatalf("expected plain lane work dir under %s: %v", plainLanes, err)
	}
	if _, err := os.Stat(hookWorkFile); err != nil {
		t.Fatalf("expected hook working file %s: %v", hookWorkFile, err)
	}
	if _, err := os.Stat(filepath.Join(plainRoot, ".tillr", "loop", "run-finding-a1", "proc-exec.jsonl")); err != nil {
		t.Fatalf("expected proc-exec.jsonl under %s/.tillr/loop/run-finding-a1: %v", plainRoot, err)
	}
	// Verify item reached finished state.
	st, err := store.OpenRead(context.Background(), plainStore)
	if err != nil {
		t.Fatal(err)
	}
	var finalState string
	if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT state FROM items WHERE item_id='finding'`).Scan(&finalState)
	}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()
	if finalState != "confirmed" {
		t.Fatalf("item state = %q, want confirmed", finalState)
	}

	// 3. Enclosing Git repository's .tillr directory is completely unchanged.
	outerTillrEntries, err := os.ReadDir(outerTillr)
	if err != nil {
		t.Fatalf("reading outer .tillr: %v", err)
	}
	var outerNames []string
	for _, e := range outerTillrEntries {
		outerNames = append(outerNames, e.Name())
	}
	if strings.Join(outerNames, ",") != "loop,sentinel.txt" {
		t.Fatalf("enclosing .tillr changed: got %v, want [loop sentinel.txt]", outerNames)
	}
	outerLoopEntries, err := os.ReadDir(filepath.Join(outerTillr, "loop"))
	if err != nil {
		t.Fatalf("reading outer .tillr/loop: %v", err)
	}
	var outerLoopNames []string
	for _, e := range outerLoopEntries {
		outerLoopNames = append(outerLoopNames, e.Name())
	}
	if strings.Join(outerLoopNames, ",") != "existing.json" {
		t.Fatalf("enclosing .tillr/loop changed: got %v, want [existing.json]", outerLoopNames)
	}
	// Check enclosing repo git status outside sub/plain is clean.
	statusOut, err := exec.Command(realGit, "-C", outer, "status", "--porcelain", "--", ".", ":!sub/plain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v: %s", err, statusOut)
	}
	if strings.TrimSpace(string(statusOut)) != "" {
		t.Fatalf("enclosing git repo tree changed:\n%s", statusOut)
	}
}

func TestHookCompletionTaskArtifactDirAndDefaultCriteria(t *testing.T) {
	root := scratch(t, "consumer-root")
	lanes := filepath.Join(scratch(t, "consumer-lanes"), "lanes")
	db := filepath.Join(root, ".tillr", "state.db")
	itemBin := sibling(t, "item")
	runBin := sibling(t, "run")
	sibling(t, "view")
	adapter := stub(t, "adapter", "printf 'RESULT: done\\n'")
	backendsDir := filepath.Join(repoRoot(t), "config", "tillr", "backends")

	hookScript := filepath.Join(root, "hook.py")
	observedArtifactFile := filepath.Join(root, "observed-artifact-dir.txt")
	hookCode := fmt.Sprintf(`#!/usr/bin/env python3
import argparse, hashlib, json, subprocess, sys
from pathlib import Path

flags = ["--item", "--run", "--store", "--root", "--artifact-dir"]
if sys.argv[1:] == ["describe"]:
    print(json.dumps({"hooks": {"complete-item": flags}}))
    sys.exit(0)

parser = argparse.ArgumentParser()
parser.add_argument("hook", choices=["complete-item"])
for flag in flags:
    parser.add_argument(flag, required=True)
args = parser.parse_args()

art = Path(args.artifact_dir)
if not (art / "brief.md").is_file() or not (art / f"{args.run}.log").is_file():
    sys.stderr.write(f"missing files in {art}: {list(art.iterdir())}\n")
    sys.exit(2)
Path(%q).write_text(str(art))

shown = json.loads(subprocess.check_output([%q, "show", "--store", args.store, "--item", args.item]))["payload"]
for c in shown["criteria"]:
    v = {
        "record_version": 1,
        "kind": "item.verdict",
        "id": f"{args.run}-{c['criterion_id']}",
        "producer": {"system": "tillr", "utility": "item", "version": "0.1.0"},
        "created_at": "2026-11-26T00:00:00Z",
        "payload": {
            "item_id": args.item,
            "criterion_id": c["criterion_id"],
            "criterion_hash": hashlib.sha256(c["text"].encode()).hexdigest(),
            "verdict": "done",
            "claim_class": c["claim_class"],
            "reason": "verified",
            "evidence": ["doc:proof.txt"],
            "actor_kind": "mechanical",
            "actor": "test-hook",
        },
    }
    vpath = Path(args.root) / f"{args.item}-{c['criterion_id']}-verdict.json"
    vpath.write_text(json.dumps(v))
    subprocess.run([%q, "verdict", "record", "--store", args.store, "--input", str(vpath), "--now", "2026-11-26T00:00:00Z"], check=True, stdout=subprocess.DEVNULL)

print(json.dumps({"state": "confirmed", "reason": "verified", "resolves_via": "doc:proof.txt"}))
`, observedArtifactFile, itemBin, itemBin)
	writeFile(t, hookScript, hookCode)
	if err := os.Chmod(hookScript, 0o755); err != nil {
		t.Fatal(err)
	}

	d := plannerFixtureDefinition(t)
	d.Ready = "in-queue"
	d.Transitions = append(d.Transitions, definition.Transition{From: "in-queue", To: "confirmed"})
	d.BriefTemplate = "template.md"
	d.DefaultCriteria = []definition.DefaultCriterion{
		{CriterionID: "dc1", Text: "Finding is verified", ClaimClass: "verification-only"},
	}
	d.Hooks = &definition.Hooks{Argv: []string{"python3", hookScript}, Names: []string{"complete-item"}}
	writeFile(t, filepath.Join(root, d.BriefTemplate), "# {{ITEM_ID}}\n\n## Task\n\n{{TASK}}\n\n## Criteria\n\n1. {{CRITERION_1}}\n")

	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	setPlannerDefinition(t, db, d)

	// Apply an item with task words and no criteria of its own.
	itemJSON := `{"record_version":1,"kind":"item.record","id":"req-1","producer":{"system":"tillr","utility":"test","version":"0.1.0"},"created_at":"2026-11-26T00:00:00Z","payload":{"item_id":"finding","title":"Check alert","body_ref":"alerts/1.json","task":"Reproduce alert 1 in staging and capture its output.","state":"in-queue"}}`
	itemFile := filepath.Join(root, "item.json")
	writeFile(t, itemFile, itemJSON)
	if out, err := exec.Command(itemBin, "apply", "--store", db, "--input", itemFile, "--now", "2026-11-26T00:00:00Z").CombinedOutput(); err != nil {
		t.Fatalf("item apply: %v: %s", err, out)
	}

	binDir := scratch(t, "consumer-bin")
	piStub := filepath.Join(binDir, "pi")
	writeFile(t, piStub, "#!/bin/sh\nif [ \"${1:-}\" = \"--features\" ]; then printf '[]\\n'; exit 0; fi\ncat >/dev/null\nprintf '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"RESULT: done\"}]}}\\n'\n")
	if err := os.Chmod(piStub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(root)

	now := func() time.Time { return time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC) }
	steps := &drain.ProcessSteps{
		RunBin:      runBin,
		ItemBin:     itemBin,
		Store:       db,
		LaneRoot:    lanes,
		Root:        root,
		BackendsDir: backendsDir,
		Now:         now,
	}
	steps.ConfigureHooks(d.Hooks, nil)
	stager := &drain.LaneStager{Root: root, Store: db, LaneRoot: lanes, Now: now}
	loop, err := drain.NewLoop(drain.LoopConfig{
		Root:            root,
		Store:           db,
		LaneRoot:        lanes,
		Lanes:           []string{"lane-1"},
		Policy:          &drain.Policy{DefaultBackend: "fixture", Backends: map[string]drain.Backend{"fixture": {Adapter: adapter, Model: "fixture"}}},
		Steps:           steps,
		Stage:           stager,
		Now:             now,
		Report:          os.Stderr,
		ExitWhenDrained: true,
		BackoffBase:     time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop.Run: %v (result=%+v)", err, result)
	}
	if result.Dispatched != 1 {
		t.Fatalf("Dispatched = %d, want 1", result.Dispatched)
	}

	// Verify brief contained task words and default criterion.
	briefBytes, err := os.ReadFile(filepath.Join(lanes, "lane-1", "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	brief := string(briefBytes)
	if !strings.Contains(brief, "Reproduce alert 1 in staging and capture its output.") {
		t.Fatalf("brief missing task words:\n%s", brief)
	}
	if !strings.Contains(brief, "Finding is verified") {
		t.Fatalf("brief missing default criterion:\n%s", brief)
	}

	// Verify hook received --artifact-dir pointing to lane-1.
	gotArt, err := os.ReadFile(observedArtifactFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotArt) != filepath.Join(lanes, "lane-1") {
		t.Fatalf("--artifact-dir = %q, want %q", string(gotArt), filepath.Join(lanes, "lane-1"))
	}

	// Verify item reached confirmed state.
	st, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var finalState string
	if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT state FROM items WHERE item_id='finding'`).Scan(&finalState)
	}); err != nil {
		t.Fatal(err)
	}
	if finalState != "confirmed" {
		t.Fatalf("item state = %q, want confirmed", finalState)
	}
}
