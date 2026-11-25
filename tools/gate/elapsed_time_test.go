package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestElapsedTimeDetectsGoJsPyAndIgnoresDeclaredOrNonTest(t *testing.T) {
	f := newChangeFixture(t, true)
	goTest := "package pkg\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestTiming(t *testing.T) {\n\tbegan := time.Now()\n\twaited := time.Since(began)\n\tbound := time.Second\n\tif waited > bound {\n\t\tt.Fatal(waited)\n\t}\n}\n"
	if err := os.MkdirAll(filepath.Join(f.repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "fairness_test.go"), []byte(goTest), 0o644); err != nil {
		t.Fatal(err)
	}
	redHead := f.commit()

	code, out, errOut := f.check("elapsed-time", f.base, redHead)
	if code != 1 || !strings.Contains(out, "pkg/fairness_test.go:12:") {
		t.Fatalf("undeclared timing: code=%d out=%s err=%s", code, out, errOut)
	}

	decl := `{
  "declarations": [
    {
      "file": "pkg/fairness_test.go",
      "snippet": "if waited > bound {",
      "kind": "liveness",
      "reason": "fixture liveness guard",
      "repair": "replace with channel signal"
    }
  ]
}
`
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "gate-timing-declarations.json"), []byte(decl), 0o644); err != nil {
		t.Fatal(err)
	}
	declaredHead := f.commit()
	if code, out, errOut := f.check("elapsed-time", f.base, declaredHead); code != 0 {
		t.Fatalf("declared timing: code=%d out=%s err=%s", code, out, errOut)
	}

	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "fairness_test.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	staleHead := f.commit()
	code, out, errOut = f.check("elapsed-time", declaredHead, staleHead)
	if code != 1 || !strings.Contains(out, "silencer") {
		t.Fatalf("stale timing: code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestElapsedTimeScopesToChangedTestsAndValidatesDeclarations(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.MkdirAll(filepath.Join(f.repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacySrc := "package pkg\nimport (\n\t\"testing\"\n\t\"time\"\n)\nfunc TestLegacy(t *testing.T) {\n\tbegan := time.Now()\n\tif time." + "Since(began) > time.Second { t.Fatal() }\n}\n"
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "legacy_test.go"), []byte(legacySrc), 0o644); err != nil {
		t.Fatal(err)
	}
	baseWithDebt := f.commit()

	cleanSrc := "package pkg\nimport (\n\t\"testing\"\n\t\"time\"\n)\nconst hangGuard = 5 * time.Minute\nfunc TestDeadline(t *testing.T) {\n\tdeadline := time.Now().Add(hangGuard)\n\tif time.Now().Before(deadline) {}\n}\n"
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "clean_test.go"), []byte(cleanSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	delivered := filepath.Join(f.repo, "yolo-packs", "example", "files", "pkg", "copied_test.go")
	if err := os.MkdirAll(filepath.Dir(delivered), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(delivered, []byte(strings.ReplaceAll(legacySrc, "TestLegacy", "TestCopy")), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanHead := f.commit()
	if code, out, errOut := f.check("elapsed-time", baseWithDebt, cleanHead); code != 0 {
		t.Fatalf("clean change: code=%d out=%s err=%s", code, out, errOut)
	}

	// Touching legacy_test.go brings it into the scan.
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "legacy_test.go"), []byte(legacySrc+"// touched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touchedHead := f.commit()
	code, out, errOut := f.check("elapsed-time", cleanHead, touchedHead)
	if code != 1 || !strings.Contains(out, "pkg/legacy_test.go:8: if time."+"Since(began) > time.Second { t.Fatal() }") {
		t.Fatalf("touched legacy: code=%d out=%s err=%s", code, out, errOut)
	}

	// JS and Python test files are scanned too.
	if err := os.MkdirAll(filepath.Join(f.repo, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	pyExpr := "time.monotonic() - began > 1.0"
	if err := os.WriteFile(filepath.Join(f.repo, "tests", "test_clock.py"), []byte("import time\nbegan = time.monotonic()\nassert not ("+pyExpr+")\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "tests", "clock.test.js"), []byte("const began = Date.now();\nif (Date.now() - began > 100) throw new Error('slow');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pyJsHead := f.commit()
	code, out, errOut = f.check("elapsed-time", touchedHead, pyJsHead)
	if code != 1 || !strings.Contains(out, "tests/test_clock.py:3:") || !strings.Contains(out, "tests/clock.test.js:2:") {
		t.Fatalf("py/js: code=%d out=%s err=%s", code, out, errOut)
	}

	// Non-test files are ignored.
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "helper.go"), []byte("package pkg\nimport \"time\"\nfunc f() bool { b := time.Now(); return time."+"Since(b) > time.Second }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nonTestHead := f.commit()
	if code, out, errOut := f.check("elapsed-time", pyJsHead, nonTestHead); code != 0 {
		t.Fatalf("non-test: code=%d out=%s err=%s", code, out, errOut)
	}

	// Invalid schema fails.
	badDecl, _ := json.Marshal(map[string]any{
		"declarations": []map[string]string{{
			"file": "tests/test_clock.py", "snippet": "assert not (" + pyExpr + ")",
			"kind": "backlog", "reason": "fixture", "repair": "",
		}},
	})
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "gate-timing-declarations.json"), append(badDecl, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	invalidHead := f.commit()
	code, out, errOut = f.check("elapsed-time", pyJsHead, invalidHead)
	if code != 1 || !strings.Contains(out, "repair") {
		t.Fatalf("invalid schema: code=%d out=%s err=%s", code, out, errOut)
	}

	// Stale silencer fails.
	staleDecl, _ := json.Marshal(map[string]any{
		"declarations": []map[string]string{{
			"file": "pkg/clean_test.go", "snippet": "if waited > bound {",
			"kind": "backlog", "reason": "fixture", "repair": "remove stale silencer",
		}},
	})
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "gate-timing-declarations.json"), append(staleDecl, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	staleHead := f.commit()
	code, out, errOut = f.check("elapsed-time", pyJsHead, staleHead)
	if code != 1 || !strings.Contains(out, "no longer matches a hit") {
		t.Fatalf("stale silencer: code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestElapsedTimeCandidateAssertionRefusedAndDeclaredPasses(t *testing.T) {
	root, _ := loadTestContext(t)
	runner := filepath.Join(t.TempDir(), "tillr-gate")
	build := exec.Command("go", "build", "-o", runner, ".")
	build.Dir = filepath.Join(root, "tools", "tillr-gate")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build tillr-gate: %v: %s", err, out)
	}

	f := newChangeFixture(t, true)
	copyChecksAndStubTree(t, root, f.repo, f.gateBin)
	base := f.commit()

	goTest := "package pkg\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestTiming(t *testing.T) {\n\tbegan := time.Now()\n\twaited := time.Since(began)\n\tbound := time.Second\n\tif waited > bound {\n\t\tt.Fatal(waited)\n\t}\n}\n"
	if err := os.MkdirAll(filepath.Join(f.repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "fairness_test.go"), []byte(goTest), 0o644); err != nil {
		t.Fatal(err)
	}
	redHead := f.commit()

	cmd := exec.Command(f.gateBin, "change-gate", "--base", base, "--head", redHead, "--runner", runner, "--logs", filepath.Join(f.repo, "logs-red"))
	cmd.Dir = f.repo
	cmd.Env = f.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("expected exit 1, got err=%v out=%s err=%s", err, stdout.String(), stderr.String())
	}
	var envRed struct {
		Payload struct {
			Checks []struct {
				CheckID string `json:"check_id"`
				Result  string `json:"result"`
				LogRef  string `json:"log_ref"`
			} `json:"checks"`
		} `json:"payload"`
	}
	firstLine := strings.Split(strings.TrimSpace(stdout.String()), "\n")[0]
	if err := json.Unmarshal([]byte(firstLine), &envRed); err != nil {
		t.Fatalf("parse json: %v: %s", err, stdout.String())
	}
	for _, c := range envRed.Payload.Checks {
		if c.CheckID == "elapsed-time" {
			if c.Result != "fail" {
				t.Fatalf("elapsed-time result=%q", c.Result)
			}
			logBytes, _ := os.ReadFile(c.LogRef)
			if !strings.Contains(string(logBytes), "pkg/fairness_test.go:12: if waited > bound {") {
				t.Fatalf("log missing hit: %s", logBytes)
			}
		}
	}

	f.git("reset", "--hard", base)
	if err := os.MkdirAll(filepath.Join(f.repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "fairness_test.go"), []byte(goTest), 0o644); err != nil {
		t.Fatal(err)
	}
	decl, _ := json.Marshal(map[string]any{
		"declarations": []map[string]string{{
			"file": "pkg/fairness_test.go", "snippet": "if waited > bound {",
			"kind": "backlog", "reason": "fixture elapsed-time assertion under test",
			"repair": "replace wall-clock bound with an event-order assertion",
		}},
	})
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "gate-timing-declarations.json"), append(decl, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	greenHead := f.commit()

	cmd = exec.Command(f.gateBin, "change-gate", "--base", base, "--head", greenHead, "--runner", runner, "--logs", filepath.Join(f.repo, "logs-green"))
	cmd.Dir = f.repo
	cmd.Env = f.env
	outGreen, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected pass, got err=%v out=%s", err, outGreen)
	}
}

func TestElapsedTimeRefusesShortPositiveDeadlinesAndAllowsWaitsAndGuards(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.MkdirAll(filepath.Join(f.repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Passing cases: hangGuard, heartbeatStallBound, 5 * time.Minute, negative waits,
	// polling ticks, and non-test Go files.
	prodSrc := "package pkg\nimport \"time\"\n" +
		"func Wait(ev <-chan struct{}) {\n" +
		"\tselect {\n\tcase <-ev:\n\tcase <-time.After(5 * time.Second):\n\t\tpanic(\"boom\")\n\t}\n" +
		"}\n"
	goodSrc := "package pkg\nimport (\n\t\"testing\"\n\t\"time\"\n)\n" +
		"const (\n\thangGuard = 5 * time.Minute\n\theartbeatStallBound = 10 * time.Minute\n)\n" +
		"func TestGuardsAndNegativeWaits(t *testing.T) {\n" +
		"\tev := make(chan struct{})\n" +
		"\tselect {\n\tcase <-ev:\n\tcase <-time.After(hangGuard):\n\t\tt.Fatal(\"hang\")\n\t}\n" +
		"\tselect {\n\tcase <-ev:\n\tcase <-time.After(heartbeatStallBound):\n\t\tt.Fatal(\"stall\")\n\t}\n" +
		"\tselect {\n\tcase <-ev:\n\tcase <-time.After(5 * time.Minute):\n\t\tt.Fatal(\"five min\")\n\t}\n" +
		"\t// Negative wait: fail if event arrives, empty body on short timer.\n" +
		"\tselect {\n\tcase <-ev:\n\t\tt.Fatal(\"unexpected event\")\n\tcase <-time.After(200 * time.Millisecond):\n\t}\n" +
		"\t// Polling tick: empty body on short timer.\n" +
		"\tfor i := 0; i < 1; i++ {\n" +
		"\t\tselect {\n\t\tcase <-ev:\n\t\tcase <-time.After(10 * time.Millisecond):\n\t\t\t// retry\n\t\t}\n" +
		"\t}\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "prod.go"), []byte(prodSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "good_test.go"), []byte(goodSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	goodHead := f.commit()
	if code, out, errOut := f.check("elapsed-time", f.base, goodHead); code != 0 {
		t.Fatalf("good cases: code=%d out=%s err=%s", code, out, errOut)
	}

	// 2. Direct short case <-time.After(5 * time.Second): calling t.Fatal is refused.
	directSrc := "package pkg\nimport (\n\t\"testing\"\n\t\"time\"\n)\n" +
		"func TestDirectShort(t *testing.T) {\n" +
		"\tev := make(chan struct{})\n" +
		"\tselect {\n" +
		"\tcase <-ev:\n" +
		"\tcase <-time.After(5 * time.Second):\n" +
		"\t\tt.Fatal(\"did not happen\")\n" +
		"\t}\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "direct_short_test.go"), []byte(directSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	directHead := f.commit()
	code, out, errOut := f.check("elapsed-time", goodHead, directHead)
	if code != 1 || !strings.Contains(out, "pkg/direct_short_test.go:10: case <-time.After(5 * time.Second):") || !strings.Contains(out, "hangGuard") {
		t.Fatalf("direct short: code=%d out=%s err=%s", code, out, errOut)
	}

	// 3a. Assigned deadline := time.After(10 * time.Second) in failing case <-deadline: is refused.
	assignedCaseSrc := "package pkg\nimport (\n\t\"testing\"\n\t\"time\"\n)\n" +
		"func TestAssignedCase(t *testing.T) {\n" +
		"\tev := make(chan struct{})\n" +
		"\tdeadline := time.After(10 * time.Second)\n" +
		"\tselect {\n" +
		"\tcase <-ev:\n" +
		"\tcase <-deadline:\n" +
		"\t\tt.Fatalf(\"timeout\")\n" +
		"\t}\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "assigned_case_test.go"), []byte(assignedCaseSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	assignedCaseHead := f.commit()
	code, out, errOut = f.check("elapsed-time", directHead, assignedCaseHead)
	if code != 1 || !strings.Contains(out, "pkg/assigned_case_test.go:8: deadline := time.After(10 * time.Second)") {
		t.Fatalf("assigned case: code=%d out=%s err=%s", code, out, errOut)
	}

	// 3b. Assigned deadline := time.After(10 * time.Second) passed to a call is refused.
	assignedCallSrc := "package pkg\nimport (\n\t\"context\"\n\t\"testing\"\n\t\"time\"\n)\n" +
		"func TestAssignedCall(t *testing.T) {\n" +
		"\tdeadline := time.After(10 * time.Second)\n" +
		"\t_ = wakeNext(context.Background(), deadline)\n" +
		"}\n" +
		"func wakeNext(ctx context.Context, ch <-chan time.Time) error { return nil }\n"
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "assigned_call_test.go"), []byte(assignedCallSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	assignedCallHead := f.commit()
	code, out, errOut = f.check("elapsed-time", assignedCaseHead, assignedCallHead)
	if code != 1 || !strings.Contains(out, "pkg/assigned_call_test.go:8: deadline := time.After(10 * time.Second)") {
		t.Fatalf("assigned call: code=%d out=%s err=%s", code, out, errOut)
	}

	// 4. File-local const shortGuard = 30 * time.Second in failing case <-time.After(shortGuard): is refused.
	constShortSrc := "package pkg\nimport (\n\t\"testing\"\n\t\"time\"\n)\n" +
		"const shortGuard = 30 * time.Second\n" +
		"func TestConstShort(t *testing.T) {\n" +
		"\tev := make(chan struct{})\n" +
		"\tselect {\n" +
		"\tcase <-ev:\n" +
		"\tcase <-time.After(shortGuard):\n" +
		"\t\tt.Error(\"too slow\")\n" +
		"\t}\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "const_short_test.go"), []byte(constShortSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	constHead := f.commit()
	code, out, errOut = f.check("elapsed-time", assignedCallHead, constHead)
	if code != 1 || !strings.Contains(out, "pkg/const_short_test.go:11: case <-time.After(shortGuard):") {
		t.Fatalf("const short: code=%d out=%s err=%s", code, out, errOut)
	}

	// 5. A short deadline declared in tools/gate-timing-declarations.json passes.
	decl, _ := json.Marshal(map[string]any{
		"declarations": []map[string]string{{
			"file":    "pkg/const_short_test.go",
			"snippet": "case <-time.After(shortGuard):",
			"kind":    "backlog",
			"reason":  "fixture short deadline declaration",
			"repair":  "replace shortGuard with hangGuard",
		}},
	})
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "gate-timing-declarations.json"), append(decl, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	declaredHead := f.commit()
	if code, out, errOut := f.check("elapsed-time", assignedCallHead, declaredHead); code != 0 {
		t.Fatalf("declared short deadline: code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestElapsedTimeRefusesShortPollingDeadlinesAndAllowsNonPollingWaits(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.MkdirAll(filepath.Join(f.repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Passing cases: hangGuard, 5 * time.Minute, negative time.Now().Add,
	// non-polling time.Now().Add fixture timestamp, and negative-wait time.NewTimer.
	okSrc := "package pkg\nimport (\n\t\"testing\"\n\t\"time\"\n)\n" +
		"const hangGuard = 5 * time.Minute\n" +
		"func TestPollingAllowed(t *testing.T) {\n" +
		"\td1 := time.Now().Add(hangGuard)\n" +
		"\tfor !time.Now().After(d1) {\n\t\tbreak\n\t}\n" +
		"\td2 := time.Now().Add(5 * time.Minute)\n" +
		"\tif time.Now().Before(d2) {}\n" +
		"\tpast := time.Now().Add(-500 * time.Millisecond)\n" +
		"\t_ = time.Now().After(past)\n" +
		"\tfuture := time.Now().Add(time.Second)\n" +
		"\t_ = future\n" +
		"\ttimer := time.NewTimer(200 * time.Millisecond)\n" +
		"\tdefer timer.Stop()\n" +
		"\tev := make(chan struct{})\n" +
		"\tselect {\n\tcase <-ev:\n\t\tt.Fatal(\"unexpected event\")\n\tcase <-timer.C:\n\t\treturn\n\t}\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "polling_ok_test.go"), []byte(okSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	okHead := f.commit()
	if code, out, errOut := f.check("elapsed-time", f.base, okHead); code != 0 {
		t.Fatalf("polling ok: code=%d out=%s err=%s", code, out, errOut)
	}

	// 2. Assigned deadline := time.Now().Add(90 * time.Second) with time.Now().After(deadline) is refused.
	pollAfterSrc := "package pkg\nimport (\n\t\"testing\"\n\t\"time\"\n)\n" +
		"func TestPollAfter(t *testing.T) {\n" +
		"\tdeadline := time.Now().Add(90 * time.Second)\n" +
		"\tfor {\n" +
		"\t\tif time.Now().After(deadline) {\n" +
		"\t\t\tt.Fatalf(\"never answered\")\n" +
		"\t\t}\n" +
		"\t}\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "poll_after_test.go"), []byte(pollAfterSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	afterHead := f.commit()
	code, out, errOut := f.check("elapsed-time", okHead, afterHead)
	if code != 1 || !strings.Contains(out, "pkg/poll_after_test.go:7: deadline := time.Now().Add(90 * time.Second)") || !strings.Contains(out, "hangGuard") {
		t.Fatalf("poll after: code=%d out=%s err=%s", code, out, errOut)
	}

	// 3. Inline for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); is refused.
	pollForSrc := "package pkg\nimport (\n\t\"testing\"\n\t\"time\"\n)\n" +
		"func TestPollFor(t *testing.T) {\n" +
		"\tfor deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {\n" +
		"\t}\n" +
		"\tt.Fatal(\"timed out\")\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "poll_for_test.go"), []byte(pollForSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	forHead := f.commit()
	code, out, errOut = f.check("elapsed-time", afterHead, forHead)
	if code != 1 || !strings.Contains(out, "pkg/poll_for_test.go:7: for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {") {
		t.Fatalf("poll for: code=%d out=%s err=%s", code, out, errOut)
	}

	// 4. Assigned deadline := time.NewTimer(15 * time.Second) with failing case <-deadline.C: is refused.
	pollTimerSrc := "package pkg\nimport (\n\t\"testing\"\n\t\"time\"\n)\n" +
		"func TestPollTimer(t *testing.T) {\n" +
		"\tdeadline := time.NewTimer(15 * time.Second)\n" +
		"\tdefer deadline.Stop()\n" +
		"\tselect {\n" +
		"\tcase <-deadline.C:\n" +
		"\t\tt.Fatal(\"did not publish\")\n" +
		"\t}\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "poll_timer_test.go"), []byte(pollTimerSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	timerHead := f.commit()
	code, out, errOut = f.check("elapsed-time", forHead, timerHead)
	if code != 1 || !strings.Contains(out, "pkg/poll_timer_test.go:7: deadline := time.NewTimer(15 * time.Second)") {
		t.Fatalf("poll timer: code=%d out=%s err=%s", code, out, errOut)
	}

	// 5. Declaration matching and staleness for polling deadlines.
	decl, _ := json.Marshal(map[string]any{
		"declarations": []map[string]string{{
			"file":    "pkg/poll_timer_test.go",
			"snippet": "deadline := time.NewTimer(15 * time.Second)",
			"kind":    "backlog",
			"reason":  "fixture short timer declaration",
			"repair":  "replace 15 * time.Second with hangGuard",
		}},
	})
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "gate-timing-declarations.json"), append(decl, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	declaredHead := f.commit()
	if code, out, errOut := f.check("elapsed-time", forHead, declaredHead); code != 0 {
		t.Fatalf("declared polling timer: code=%d out=%s err=%s", code, out, errOut)
	}

	pollTimerFixedSrc := "package pkg\nimport (\n\t\"testing\"\n\t\"time\"\n)\n" +
		"const hangGuard = 5 * time.Minute\n" +
		"func TestPollTimer(t *testing.T) {\n" +
		"\tdeadline := time.NewTimer(hangGuard)\n" +
		"\tdefer deadline.Stop()\n" +
		"\tselect {\n" +
		"\tcase <-deadline.C:\n" +
		"\t\tt.Fatal(\"did not publish\")\n" +
		"\t}\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(f.repo, "pkg", "poll_timer_test.go"), []byte(pollTimerFixedSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	staleHead := f.commit()
	code, out, errOut = f.check("elapsed-time", declaredHead, staleHead)
	if code != 1 || !strings.Contains(out, "timing declaration no longer matches a hit") {
		t.Fatalf("stale polling timer: code=%d out=%s err=%s", code, out, errOut)
	}
}
