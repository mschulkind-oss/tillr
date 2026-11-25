package drain

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The drain appends the candidate, and every optional flag its branches add, to the step
// programs it is given. In this repository those programs are the `tillr-loop` adapter's
// faces, which parse with argparse and refuse a flag they do not declare (exit 2). On
// 2026-11-17 `Land` had been sending `--observed-trunk` since 1e3541886 while the adapter did
// not declare it, so every landing faulted the whole loop. The cases below drive the drain's
// own ProcessSteps code paths into the REAL adapter, so a flag added to a step without
// teaching the adapter fails here, on the side that changed.
//
// They also pin the argv each branch generates in tools/tillr-loop/drain-argv.json, which
// tools/tillr-loop/test.py replays against the adapter's parsers. That is the other
// direction: an adapter change that drops a flag the drain sends fails the adapter's own
// suite, which the landing tier runs. Regenerate it with TILLR_UPDATE_DRAIN_ARGV=1.

// drainArgvFixture is the file both suites read, relative to the repository root.
const drainArgvFixture = "tools/tillr-loop/drain-argv.json"

// repositoryFile answers a path in the checkout this package is tested from, or skips when the
// package is tested outside it: the adapter is this repository's, not the drain module's.
func repositoryFile(t *testing.T, rel string) (root, path string) {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("not tested from a checkout, so there is no tillr-loop adapter to drive: %v", err)
	}
	root = strings.TrimSpace(string(out))
	path = filepath.Join(root, rel)
	if _, err := os.Stat(path); err != nil {
		t.Skipf("this checkout has no %s: %v", rel, err)
	}
	return root, path
}

// adapterStubs puts recording stand-ins for tillr-gate, tillr-land and tillr-item first on
// PATH. Each appends one line per invocation, `<name> <args>`, to the returned log. The
// adapter composes their argv; the stand-ins answer 0 so every face reaches its last step.
func adapterStubs(t *testing.T) (log string) {
	t.Helper()
	dir := scratchUnderDurable(t)
	log = filepath.Join(dir, "calls.log")
	show := filepath.Join(dir, "item-show.json")
	writeForkFile(t, show, `{"record_version":1,"kind":"item.record","id":"item-a","producer":{"system":"tillr","utility":"tillr-item","version":"0.1.0"},"created_at":"2026-01-01T00:00:00Z","payload":{"item_id":"item-a","criteria":[{"criterion_id":"c1","text":"the criterion","state":"pending","claim_class":"verification-only"}]}}`)
	for _, name := range []string{"tillr-gate", "tillr-land", "tillr-item"} {
		body := "#!/bin/sh\n"
		if name == "tillr-gate" {
			body += `if [ "$1" = classify ]; then
  own=$(cd "$(dirname "$0")" && pwd -P)
  oldifs=$IFS; IFS=:
  for dir in $PATH; do
    IFS=$oldifs
    [ "$(cd "$dir" 2>/dev/null && pwd -P)" = "$own" ] && continue
    [ -x "$dir/tillr-gate" ] && exec "$dir/tillr-gate" "$@"
  done
  exit 127
fi
`
		}
		body += "printf '%s\\n' \"" + name + " $*\" >> " + log + "\n"
		if name == "tillr-item" {
			body += "[ \"$1\" = show ] && cat " + show + "\n"
		}
		if err := writeForkSafe(filepath.Join(dir, name), []byte(body+"exit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The gate face exports the tree's registry unless one is named; this one is a name only,
	// because the stand-in plan never reads it.
	t.Setenv("TILLR_LOOP_REGISTRY", filepath.Join(dir, "registry.json"))
	return log
}

// writeRunRecord writes a run's gate verdict where the adapter's land face looks for it.
func writeRunRecord(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(dir, GateVerdictRecord), body)
}

func stubCalls(t *testing.T, log string) []string {
	t.Helper()
	file, err := os.Open(log)
	if err != nil {
		t.Fatalf("no stand-in was invoked: %v", err)
	}
	defer file.Close()
	var calls []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		calls = append(calls, scanner.Text())
	}
	return calls
}

// lastSpanArgv is the argv the drain executed for a run's latest step, from its own span file.
func lastSpanArgv(t *testing.T, root, runID string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(RunDir(root, runID), StepsFile))
	if err != nil {
		t.Fatalf("the step recorded no span: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var span StepSpan
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &span); err != nil {
		t.Fatal(err)
	}
	return span.Argv
}

// argvCase is one generated argv as both suites read it: the adapter face it reaches and the
// words after the face, with the fixture's paths spelled as placeholders.
type argvCase struct {
	Case string   `json:"case"`
	Face string   `json:"face"`
	Run  string   `json:"run"`
	Args []string `json:"args"`
}

const (
	fixtureBase     = "1111111111111111111111111111111111111111"
	fixtureHead     = "2222222222222222222222222222222222222222"
	fixtureObserved = "4444444444444444444444444444444444444444"
	fixtureWorker   = "5555555555555555555555555555555555555555"
)

func TestEveryArgvTheDrainSendsIsAcceptedByTheTillrLoopAdapter(t *testing.T) {
	repo, _ := repositoryFile(t, "tools/tillr-loop/tillr-loop")
	f, _ := passFixture(t, "item-a")
	log := adapterStubs(t)
	installRoot := scratchUnderDurable(t)
	_, changeGateSrc := repositoryFile(t, "tools/tillr-gate/change-gate.py")
	changeGateRaw, err := os.ReadFile(changeGateSrc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(installRoot, "tillr-gate"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(installRoot, "tillr-gate", "change-gate.py"), string(changeGateRaw))
	writeForkFile(t, filepath.Join(installRoot, "tillr-gate", "tree-check.py"), "import sys\nassert sys.argv[1:3] == ['--only', 'fixture/check']\n")
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)
	worktree := filepath.Join(f.laneRoot, "lane-1", WorktreeDir)
	// Like a consumer repository, the fixture worktree has no adapter source.
	// The keeper's commands and the fixer default use the running build's copy.
	steps := &ProcessSteps{
		Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Now: time.Now,
		GateProgram:      "builtin:tillr-loop gate",
		LandProgram:      "builtin:tillr-loop land",
		VerdictProgram:   "builtin:tillr-loop verdict",
		FixerGateProgram: DefaultFixerGateProgram,
	}
	candidate := func(item, run string) Candidate {
		return Candidate{ItemID: item, LaneID: "lane-1", RunID: run, Worktree: worktree, BaseSHA: fixtureBase, HeadSHA: fixtureHead}
	}
	var generated []argvCase
	keep := func(name, run, face string, prefix int) {
		argv := lastSpanArgv(t, f.root, run)
		args := make([]string, 0, len(argv))
		for _, word := range argv[prefix:] {
			word = strings.ReplaceAll(word, worktree, "<worktree>")
			word = strings.ReplaceAll(word, f.storePath, "<store>")
			args = append(args, strings.ReplaceAll(word, f.root, "<root>"))
		}
		generated = append(generated, argvCase{Case: name, Face: face, Run: run, Args: args})
	}
	ctx := context.Background()

	// Both ordinary and fixer candidates use the same ordinary gate face.
	for _, g := range []struct{ name, item string }{{"gate", "item-a"}, {"fixer-ordinary", TrunkFixerID(fixtureBase)}} {
		run := "run-" + g.name
		out, err := steps.Gate(ctx, candidate(g.item, run))
		if err != nil || out.Outcome != GatePassed {
			t.Fatalf("%s: gate argv: %+v, %v", g.name, out, err)
		}
		keep(g.name, run, "gate", 3)
	}
	c := candidate(TrunkFixerID(fixtureBase), "run-fixer-named")
	c.HeadSHA = runGit(t, worktree, "rev-parse", "HEAD")
	// The named check is a fast whole-tree declaration, so the adapter hands it to the
	// tree's own tree-check program; the stand-in answers a pass.
	if err := os.MkdirAll(filepath.Join(worktree, "tools", "gate", "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(worktree, "tools", "gate", "checks", "fixture.json"), `{"id": "fixture/check", "scope": "tree", "tier": "fast"}`+"\n")
	if answer, output := steps.fixerCheck(ctx, c, "fixture/check"); answer != "pass" {
		t.Fatalf("named check %s: %s", answer, output)
	}
	keep("fixer-named", c.RunID, "fixer-gate", 3)
	args := generated[len(generated)-1].Args
	args[slices.Index(args, "--head")+1] = "<head>"

	// Land: every optional flag Land appends, alone and together.
	lands := []struct {
		name   string
		mutate func(*Candidate)
		want   []string
	}{
		{"land", func(*Candidate) {}, nil},
		{"land-observed-trunk", func(c *Candidate) { c.ObservedTrunk = fixtureObserved }, []string{"--observed-trunk " + fixtureObserved}},
		{"land-base-ref", func(c *Candidate) { c.BaseRef = "refs/heads/patch/pi" }, []string{"--base-ref refs/heads/patch/pi"}},
		{"land-message-from", func(c *Candidate) { c.WorkerHeadSHA = fixtureWorker }, []string{"--message-from " + fixtureWorker}},
		{"land-every-flag", func(c *Candidate) {
			c.ObservedTrunk, c.BaseRef, c.WorkerHeadSHA = fixtureObserved, "refs/heads/patch/pi", fixtureWorker
		}, []string{"--observed-trunk " + fixtureObserved, "--base-ref refs/heads/patch/pi", "--message-from " + fixtureWorker}},
	}
	for _, l := range lands {
		run := "run-" + l.name
		writeRunRecord(t, RunDir(f.root, run), "{}\n")
		c := candidate("item-a", run)
		l.mutate(&c)
		out, err := steps.Land(ctx, c)
		if err != nil || out.Result != LandLanded {
			t.Fatalf("%s: the adapter did not accept the drain's land argv: %+v, %v", l.name, out, err)
		}
		calls := stubCalls(t, log)
		last := calls[len(calls)-1]
		if !strings.HasPrefix(last, "tillr-land pass ") {
			t.Fatalf("%s: the lander was not the adapter's last call: %s", l.name, last)
		}
		for _, want := range l.want {
			if !strings.Contains(last, want) {
				t.Fatalf("%s: the adapter dropped %q on the way to the lander: %s", l.name, want, last)
			}
		}
		if l.name == "land" && strings.Contains(last, "--observed-trunk") {
			t.Fatalf("the adapter invented an observation the drain never made: %s", last)
		}
		keep(l.name, run, "land", 3)
	}

	// Verdict: the only verdict the loop records.
	run := "run-verdict"
	writeRunRecord(t, RunDir(f.root, run), "{}\n")
	if err := steps.Verdict(ctx, candidate("item-a", run), "pass"); err != nil {
		t.Fatalf("the adapter did not accept the drain's verdict argv: %v", err)
	}
	keep("verdict", run, "verdict", 3)

	path := filepath.Join(repo, drainArgvFixture)
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(generated); err != nil {
		t.Fatal(err)
	}
	body := encoded.Bytes()
	if os.Getenv("TILLR_UPDATE_DRAIN_ARGV") == "1" {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (regenerate it with TILLR_UPDATE_DRAIN_ARGV=1)", drainArgvFixture, err)
	}
	var pinned []argvCase
	if err := json.Unmarshal(raw, &pinned); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pinned, generated) {
		t.Fatalf("the drain's step argv changed, and %s, which the adapter's suite replays, no longer says what the drain sends.\n"+
			"Teach tools/tillr-loop/tillr-loop the change, then regenerate the file with TILLR_UPDATE_DRAIN_ARGV=1.\nwant:\n%s\ngot:\n%s",
			drainArgvFixture, raw, body)
	}
}

// TestTheDrainLandsThroughTheRealAdapterAndTheRealLander is the landing the observed loop
// could not make: the drain's own Land, the repository's adapter and the real tillr-land, with
// a trunk observation in the store. The observation is honored rather than swallowed: the
// same landing refuses with trunk-rewritten when the observed tip is no longer on the trunk.
func TestTheDrainLandsThroughTheRealAdapterAndTheRealLander(t *testing.T) {
	_, adapter := repositoryFile(t, "tools/tillr-loop/tillr-loop")
	root := scratchUnderDurable(t)
	config := filepath.Join(root, "gitconfig")
	writeForkFile(t, config, "[user]\n name = tillr\n email = tillr@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	bin := filepath.Join(root, "tillr-land")
	declareSiblingInputs(t)
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = filepath.Join("..", "..", "tillr-land")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}

	for _, name := range []string{"observed-tip-on-trunk", "observed-tip-lost", "rewrite-accepted"} {
		rewritten, accepted := name == "observed-tip-lost", name == "rewrite-accepted"
		t.Run(name, func(t *testing.T) {
			repo := filepath.Join(root, name)
			if err := os.MkdirAll(repo, 0o755); err != nil {
				t.Fatal(err)
			}
			runGit(t, repo, "init", "-q", "-b", "main")
			writeForkFile(t, filepath.Join(repo, "base.txt"), "base\n")
			runGit(t, repo, "add", ".")
			runGit(t, repo, "commit", "-qm", "base")
			if accepted {
				// The lane is staged on a commit only the old history has.
				writeForkFile(t, filepath.Join(repo, "second.txt"), "second\n")
				runGit(t, repo, "add", ".")
				runGit(t, repo, "commit", "-qm", "second")
			}
			base := runGit(t, repo, "rev-parse", "HEAD")
			observed := base
			if rewritten {
				// A tip the loop observed that the trunk no longer contains: a rewrite.
				runGit(t, repo, "checkout", "-q", "-b", "lost")
				writeForkFile(t, filepath.Join(repo, "lost.txt"), "lost\n")
				runGit(t, repo, "add", ".")
				runGit(t, repo, "commit", "-qm", "the commit a rewrite dropped")
				observed = runGit(t, repo, "rev-parse", "HEAD")
				runGit(t, repo, "checkout", "-q", "main")
			}
			lane := filepath.Join(root, name+"-lanes", "lane-1", WorktreeDir)
			runGit(t, repo, "worktree", "add", "-q", "-b", "tillr/lane-1", lane, base)
			writeForkFile(t, filepath.Join(lane, "candidate.txt"), "candidate\n")
			runGit(t, lane, "add", ".")
			runGit(t, lane, "commit", "-qm", "feat: the candidate")
			head := runGit(t, lane, "rev-parse", "HEAD")
			tree := runGit(t, lane, "rev-parse", "HEAD^{tree}")

			db := filepath.Join(root, name+".db")
			s, err := store.Open(context.Background(), db, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
				if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS trunk_history(id INTEGER PRIMARY KEY,observed_tip TEXT NOT NULL,lost_tip TEXT NOT NULL)`); err != nil {
					return err
				}
				_, err := tx.Exec(`INSERT OR REPLACE INTO trunk_history VALUES(1,?,'')`, observed)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if accepted {
				// The trunk is re-authored after the lane was staged, and the operator
				// accepts it: the loop's next observation is the rewritten tip.
				current := reauthor(t, repo, base+"~1", base)
				runGit(t, repo, "reset", "-q", "--hard", current)
				if _, err := AcceptRewrite(context.Background(), AcceptRewriteRequest{Root: repo, Store: db, Lost: base, Current: current, By: "owner", Why: "re-authored", At: time.Now()}); err != nil {
					t.Fatalf("accept: %v", err)
				}
				observed = current
			}

			run := "run-item-a-a1"
			payload, err := json.Marshal(map[string]any{"verdict_id": "verdict-a", "plan_id": "plan-a", "registry_digest": "sha256:fixture", "base_sha": base, "tree_sha": tree, "result": "pass", "checks": []any{}})
			if err != nil {
				t.Fatal(err)
			}
			verdict, err := record.Encode(record.Envelope{RecordVersion: record.SupportedVersion, Kind: "gate.verdict", ID: "verdict-a", Producer: record.Producer{System: record.System, Utility: "gate", Version: "0.1.0"}, CreatedAt: time.Date(2026, 11, 17, 2, 8, 36, 0, time.UTC), Payload: payload})
			if err != nil {
				t.Fatal(err)
			}
			writeRunRecord(t, RunDir(repo, run), string(verdict))

			steps := &ProcessSteps{Root: repo, Store: db, Now: time.Now,
				LandProgram: "python3 " + adapter + " land --tillr-land " + bin}
			out, err := steps.Land(context.Background(), Candidate{
				ItemID: "item-a", LaneID: "lane-1", RunID: run, Worktree: lane,
				BaseSHA: base, HeadSHA: head, ObservedTrunk: observed,
			})
			if err != nil {
				t.Fatalf("the drain could not land through the adapter: %v", err)
			}
			tip := runGit(t, repo, "rev-parse", "main")
			if rewritten {
				if out.Result != LandRefused || out.Reason != "trunk-rewritten" || tip != base {
					t.Fatalf("a lost observed tip did not hold the landing: %+v, trunk %s", out, tip)
				}
				return
			}
			if out.Result != LandLanded || tip == base || tip == observed {
				t.Fatalf("the candidate did not land: %+v, trunk %s", out, tip)
			}
			if got := runGit(t, repo, "rev-parse", "main^{tree}"); got != tree {
				t.Fatalf("the landed tree is %s, want the gated %s", got, tree)
			}
		})
	}
}
