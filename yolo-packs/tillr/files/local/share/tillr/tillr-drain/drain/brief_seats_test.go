package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBriefPolicySeatsAndPiIsolation(t *testing.T) {
	template, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "pi", "run-pi"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	lane := filepath.Join(base, "lane")
	target := filepath.Join(lane, "worktree")
	for _, d := range []string{bin, target} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE_ARGS\"\nenv -0 | sort -z > \"$CAPTURE_ENV\"\n"
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	old := &Policy{Seats: map[string]string{"build": "pi", "design": "pi", "review": "pi", "scout": "pi"}, ReadOnlySeats: []string{"scout"}, SeatPurposes: map[string]string{"build": "Build the change.", "scout": "Find evidence."}}
	expanded := &Policy{Seats: map[string]string{}, ReadOnlySeats: []string{"scout", "test-run"}, SeatPurposes: map[string]string{}}
	for k, v := range old.Seats {
		expanded.Seats[k] = v
	}
	for k, v := range old.SeatPurposes {
		expanded.SeatPurposes[k] = v
	}
	for _, seat := range []string{"plan", "verify", "edit", "test-write", "test-run", "docs"} {
		expanded.Seats[seat] = "pi"
		expanded.SeatPurposes[seat] = "Purpose for " + seat + "."
	}
	var priorArgs, priorEnv []byte
	for i, policy := range []*Policy{old, expanded} {
		recorded := refusalStagedItem("item-a")
		spec := refusalSpec(recorded.ItemID)
		spec.Policy = policy
		values, err := briefValuesFor(recorded, spec, target, "lane worktree", strings.Repeat("0", 40), base, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		brief, err := renderBrief(template, recorded.ItemID, values)
		if err != nil {
			t.Fatal(err)
		}
		_, section, ok := strings.Cut(brief, "## Seats\n")
		if !ok {
			t.Fatal("missing Seats section")
		}
		section = strings.SplitN(section, "\n## ", 2)[0]
		if !strings.Contains(section, "tillr runs as nodes") || !strings.Contains(section, "worker cannot start") {
			t.Fatalf("seat boundary absent: %s", section)
		}
		if !strings.Contains(section, "Give mechanical searching, editing and checking to the cheaper sub-agents your own harness offers.") || !strings.Contains(section, "Your harness's configuration chooses those sub-agents' models; tillr's policy does not.") {
			t.Fatalf("delegation paragraph absent: %s", section)
		}
		got := map[string]string{}
		for _, line := range strings.Split(section, "\n") {
			if strings.HasPrefix(line, "- `") {
				name, rest, ok := strings.Cut(strings.TrimPrefix(line, "- `"), "`")
				if !ok {
					t.Fatalf("bad line %q", line)
				}
				if _, exists := got[name]; exists {
					t.Fatalf("duplicate seat %s", name)
				}
				got[name] = rest
			}
		}
		want := map[string]string{}
		for seat := range policy.Seats {
			line := ""
			for _, ro := range policy.ReadOnlySeats {
				if seat == ro {
					line += " (read-only)"
				}
			}
			if purpose := policy.SeatPurposes[seat]; purpose != "" {
				line += " — " + purpose
			}
			want[seat] = line
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seats got %#v want %#v", got, want)
		}
		if err := os.WriteFile(filepath.Join(lane, "brief.md"), []byte(brief), 0644); err != nil {
			t.Fatal(err)
		}
		argsPath, envPath := filepath.Join(base, "args"), filepath.Join(base, "env")
		cmd := exec.Command(adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "medium")
		cmd.Dir = target
		cmd.Env = []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + base, "CAPTURE_ARGS=" + argsPath, "CAPTURE_ENV=" + envPath, "TILLR_CHILD_SESSIONS_ROOT=" + filepath.Join(base, "children"), "TILLR_CHILD_SESSIONS_MARKER=" + filepath.Join(base, "marker")}
		if out, err := cmd.CombinedOutput(); err != nil {
			stderr, _ := os.ReadFile(filepath.Join(lane, "stream.err"))
			t.Fatalf("adapter: %v %s %s", err, out, stderr)
		}
		args, err := os.ReadFile(argsPath)
		if err != nil {
			t.Fatal(err)
		}
		env, err := os.ReadFile(envPath)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			priorArgs, priorEnv = args, env
		} else if !reflect.DeepEqual(args, priorArgs) || !reflect.DeepEqual(env, priorEnv) {
			t.Fatalf("policy seats changed pi argv or environment: %q vs %q; %q vs %q", priorArgs, args, priorEnv, env)
		}
	}
}

func TestBriefSeatLinesListOnlyAndMultilinePurpose(t *testing.T) {
	p := &Policy{SeatLists: map[string][]string{"list-only": {"backend"}}, SeatPurposes: map[string]string{"list-only": "Find\n  evidence."}, ReadOnlySeats: []string{"list-only"}}
	if got, want := seatLines(p), "- `list-only` (read-only) — Find evidence."; got != want {
		t.Fatalf("seat line = %q; want %q", got, want)
	}
}

func TestBriefSeatsFollowLoopPolicy(t *testing.T) {
	f, l := designSeatFixture(t)
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("apply: %v %v", ok, err)
	}
	l.cfg.Policy.Seats["fixture-seat"] = p.decision.RunRequest.Backend
	l.cfg.Policy.ReadOnlySeats = append(l.cfg.Policy.ReadOnlySeats, "fixture-seat")
	if l.cfg.Policy.SeatPurposes == nil {
		l.cfg.Policy.SeatPurposes = map[string]string{}
	}
	l.cfg.Policy.SeatPurposes["fixture-seat"] = "A dynamically supplied purpose."
	staged, err := l.stageDispatches(context.Background(), []Decision{p.decision}, base, &LoopResult{})
	if err != nil || len(staged) != 1 {
		t.Fatalf("stage: %v %+v", err, staged)
	}
	brief, err := os.ReadFile(filepath.Join(f.laneRoot, p.decision.LaneID, BriefFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(brief), "- `fixture-seat` (read-only) — A dynamically supplied purpose.") {
		t.Fatalf("staged brief did not use current policy: %s", brief)
	}
}
