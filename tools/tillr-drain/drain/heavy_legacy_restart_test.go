package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeavyLegacyRestartPreservesPlannerSchedulerAndCompletedProof(t *testing.T) {
	testHeavyLegacyRestart(t, false)
}

func TestHeavyLegacyRestartReusesSuccessfulStallRetry(t *testing.T) {
	testHeavyLegacyRestart(t, true)
}

func testHeavyLegacyRestart(t *testing.T, stalled bool) {
	t.Helper()
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	calls := filepath.Join(work, "calls")
	entered := filepath.Join(work, "entered-b")
	if err := os.MkdirAll(filepath.Join(root, "tools", "gate", "checks"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools", "gate", "checks", ".keep"), "")
	source, err := os.ReadFile(filepath.Join("..", "..", "fork-check.py"))
	if err != nil {
		t.Fatal(err)
	}
	source = []byte(strings.Replace(string(source), "if __name__ == \"__main__\":\n    sys.exit(main(sys.argv[1:]))", "", 1))
	aCommand := "echo a >> " + calls
	bCommand := "echo b >> " + calls + "; if [ ! -e " + entered + " ]; then touch " + entered + "; sleep 1000; fi"
	if stalled {
		aCommand += "; if [ ! -e " + work + "/first-a ]; then touch " + work + "/first-a; exit 6; fi"
		bCommand = "echo b >> " + calls + "; if [ ! -e " + work + "/first-b ]; then touch " + work + "/first-b; exit 6; fi; if [ ! -e " + entered + " ]; then touch " + entered + "; sleep 1000; fi"
	}
	// Keep the real gate's scheduler and execution APIs. Exit 6 is a fixture's
	// stalled observation; a's retry passes before b's retry is interrupted.
	source = append(source, []byte(`
fixture_execute = execute_check
def execute_check(check, *args, **kwargs):
    fixture_execute(check, *args, **kwargs)
    if check.rc == 6:
        check.rc = CHECK_STALLED_RC

def main(argv):
    import os
    if "--registry-json" in argv:
        print(json.dumps({"declarations": [{"id": "fixture/" + x, "kind": "fixture", "label": x, "watches": [{"path": "value"}]} for x in ("a", "b")]}))
        return 0
    root = Path(__file__).resolve().parents[1]
    plan = Plan()
    plan.checks = [
        Check("fixture", "a", ["sh", "-c", "`+aCommand+`"], "fixture", 0),
        Check("fixture", "b", ["sh", "-c", "`+bCommand+`"], "fixture", 0, after=("a",)),
    ]
    if "--only" in argv:
        label = argv[argv.index("--only") + 1].split(":", 1)[1]
        check = next(c for c in plan.checks if c.label == label)
        execute_check(check, root, None)
        return 0 if check.rc == 0 else 1
    run_plan_checks(plan, root, None, 1)
    raw = {"check_spans": [{"check_id": declaration_id(c.kind,c.label,root), "kind": c.kind, "label": c.label, "status": check_status(c), "attributes_json": "{}"} for c in plan.checks], "skipped_checks": []}
    Path(os.environ["TILLR_GATE_SPANS_OUT"]).write_text(json.dumps(raw))
    return 0

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
`)...)
	writeForkFile(t, filepath.Join(root, "tools", "fork-check.py"), string(source))
	tip := heavyCommit(t, root, "ready")
	steps := &ProcessSteps{Root: root, LaneRoot: work, RollingProgram: "python3 tools/fork-check.py {since} --tier rolling", ReplayProgram: "python3 tools/fork-check.py --only {only}"}
	runner := heavyFixture(t, root, steps.JudgeHeavy)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := runner.Run(ctx); done <- err }()
	deadline := time.After(20 * time.Second)
	for {
		if _, err := os.Stat(entered); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("ended before b: %v", err)
		case <-deadline:
			cancel()
			<-done
			t.Fatal("b did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancel passed")
	}
	if stalled {
		dir, err := heavyDirectory(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		p, err := loadHeavyProgress(dir, tip)
		if err != nil || p.Checks["fixture/a"].Result != "pass" {
			t.Fatalf("retry not saved before b: %+v %v", p, err)
		}
	}
	// A fresh ProcessSteps must recover from disk, not its replay-selector map.
	steps = &ProcessSteps{Root: root, LaneRoot: work, RollingProgram: steps.RollingProgram, ReplayProgram: steps.ReplayProgram}
	runner.Judge = steps.JudgeHeavy
	out, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := "a\nb\nb\n"
	if stalled {
		want = "a\nb\na\nb\nb\n"
	}
	if string(raw) != want || len(out.Runs[0].ReusedChecks) != 1 || !strings.Contains(out.Runs[0].ReusedChecks[0], "fixture/a") {
		t.Fatalf("calls=%q want=%q out=%+v", raw, want, out)
	}
}
