package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The template's placeholders, filled here rather than read from the template's specimen so
// a case can state every value the brief's ten sections require. The values are inert: this
// case is about the adapter's backend check and the extractor's report, not about the brief.
var briefValues = map[string]string{
	"ITEM_ID":            "item_042",
	"TITLE":              "a seated run reaches its adapter",
	"RUN_ID":             "run-item_042-20261115T000000Z",
	"BACKEND":            "pi-designer",
	"BASE_SHA":           strings.Repeat("0", 40),
	"TARGET":             "worktree",
	"TARGET_KIND":        "lane worktree",
	"DATE":               "2026-11-15",
	"TIMEOUT_SECONDS":    "1800",
	"DRIVER_REASON":      "unused in the worktree form",
	"MAIN_STATUS_BEFORE": "unused in the worktree form",
	"TASK":               "Say the one line the item asks for.",
	"CRITERION_1":        "the extractor reads a RESULT footer",
	"OUT_OF_FENCE_PATHS": "- everything else",
	"SEAT_LINES":         "- `build` — Build the change.",
	"SETTLED":            "- a backend is the adapter's name or a `<adapter>-<role>` alias.",
	"FACTS":              "- backends/ holds one directory per adapter.",
	"METHOD":             "Run the adapter, then the extractor.",
	"VERIFICATION":       "```\ntrue\n```",
}

// filledBrief answers the template with every placeholder filled, which is the only form the
// adapter's preflight accepts.
func filledBrief(t *testing.T, target string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("backends", "brief-template.md"))
	if err != nil {
		t.Fatalf("reading the brief template failed: %v", err)
	}
	text := string(raw)
	for name, value := range briefValues {
		if name == "TARGET" {
			value = target
		}
		text = strings.ReplaceAll(text, "{{"+name+"}}", value)
	}
	return text
}

func TestBriefPreflightRequiresSeats(t *testing.T) {
	base := scratch(t, "missing-seats")
	brief := filepath.Join(base, "brief.md")
	body := strings.Replace(filledBrief(t, base), "## Seats\n", "## Omitted seats\n", 1)
	if err := os.WriteFile(brief, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	preflight, err := filepath.Abs(filepath.Join("backends", "preflight.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `. "$1"; PROG=test; preflight_brief "$2"`, "test", preflight, brief)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "no '## Seats' section") {
		t.Fatalf("missing Seats accepted or wrong refusal: %v: %s", err, out)
	}
}

// writeExecutable writes one executable shell script.
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s failed: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("writing %s failed: %v", path, err)
	}
}

// TestThePiAdapterAcceptsASeatAliasAndItsExtractorReadsTheReport is criterion 1 end to end.
// The design seat's backend is `pi-designer`, the same adapter on the design seat's model,
// so the adapter's preflight must accept a backend name that is an alias of its own
// (`<adapter>-<role>`) and the `extract-report` beside that adapter must read the report the
// run produced. The 2026-11-15 incident was this exact run exiting 2 in 0s — the preflight
// compared the backend name to the adapter's name and refused `pi-designer` — which the loop
// then read as an infrastructure failure and answered on the floor model.
func TestThePiAdapterDeliversLargeBriefsWithoutArgvLimits(t *testing.T) {
	adapter, err := filepath.Abs(filepath.Join("backends", "pi", "run-pi"))
	if err != nil {
		t.Fatal(err)
	}
	base := scratch(t, "large-pi-brief")
	bin := filepath.Join(base, "bin")
	input := filepath.Join(base, "input.txt")
	resumePrompt := filepath.Join(base, "enclosing-resume.txt")
	if err := os.WriteFile(resumePrompt, []byte("enclosing continuation, not the fixture brief"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_RESUME_PROMPT_FILE", resumePrompt)
	t.Setenv("TILLR_SESSION_MODE", "resume")
	writeExecutable(t, filepath.Join(bin, "pi"), "#!/bin/sh\ncat > \"$STUB_INPUT\"\n")
	worktree := filepath.Join(base, "lane", "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	brief := filledBrief(t, worktree) + "\n" + strings.Repeat("conflict evidence\n", 20000)
	if err := os.WriteFile(filepath.Join(base, "lane", "brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "medium")
	cmd.Dir = worktree
	// This fixture starts fresh even when its enclosing worker is a continuation.
	// Keep the parent's environment intact and remove only continuation inputs.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "TILLR_RESUME_PROMPT_FILE=") && !strings.HasPrefix(entry, "TILLR_SESSION_MODE=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "STUB_INPUT="+input)
	if out, err := cmd.CombinedOutput(); err != nil {
		stderr, _ := os.ReadFile(filepath.Join(base, "lane", "stream.err"))
		t.Fatalf("large brief failed to launch: %v: %s %s", err, out, stderr)
	}
	got, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != brief {
		t.Fatalf("stdin brief changed: got %d bytes, want %d", len(got), len(brief))
	}
}

func TestThePiAdapterAcceptsASeatAliasAndItsExtractorReadsTheReport(t *testing.T) {
	adapter, err := filepath.Abs(filepath.Join("backends", "pi", "run-pi"))
	if err != nil {
		t.Fatalf("resolving the adapter failed: %v", err)
	}
	extractor, err := filepath.Abs(filepath.Join("backends", "pi", "extract-report"))
	if err != nil {
		t.Fatalf("resolving the extractor failed: %v", err)
	}
	stream, err := filepath.Abs(filepath.Join("backends", "pi", "testdata", "complete.jsonl"))
	if err != nil {
		t.Fatalf("resolving the fixture stream failed: %v", err)
	}

	// The harness stub: `pi` prints the fixture stream and exits 0. The adapter's job is to
	// run it and let the stream reach stdout, so the stub is the whole model here.
	bin := filepath.Join(scratch(t, "stub"), "bin")
	stub := filepath.Join(bin, "pi")
	writeExecutable(t, stub, "#!/bin/sh\ncat \"$STUB_STREAM\"\n")

	lane := filepath.Join(scratch(t, "lane"), "lane")
	worktree := filepath.Join(lane, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("creating the worktree failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lane, "brief.md"), []byte(filledBrief(t, worktree)), 0o644); err != nil {
		t.Fatalf("writing the brief failed: %v", err)
	}

	run := exec.Command(adapter, "--backend", "pi-designer", "--model", "fixture-model", "--effort", "medium")
	run.Dir = worktree
	run.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_STREAM="+stream,
	)
	out, err := run.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("the adapter refused backend pi-designer with exit %d: %s", exit.ExitCode(), exit.Stderr)
		}
		t.Fatalf("running the adapter failed: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("the adapter produced no stream, so the run log would be empty")
	}

	// The stream the adapter produced is the run's log; the extractor beside the adapter must
	// read it and its report must carry the RESULT footer the brief asks for.
	extract := exec.Command(extractor, "--backend", "pi-designer", "--model", "fixture-model", "--effort", "medium", "--exit-code", "0", stream)
	report, err := extract.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("the extractor answered exit %d: %s", exit.ExitCode(), exit.Stderr)
		}
		t.Fatalf("running the extractor failed: %v", err)
	}
	body := string(report)
	for _, want := range []string{"backend: pi-designer", "complete: yes", "result: done", "RESULT: done"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the report does not carry %q:\n%s", want, body)
		}
	}
}

// The claude adapter takes the same seat alias form (`claude-designer` is the design seat's
// backend on the work machine), passes the policy row's harness arguments through after its
// own fixed flags, and its extractor reads the report and the usage the stream's `result`
// line carries (docs/reference/tillr-claude-backend.md, "The adapter" and
// "Controlling what a seat may use").
func TestTheClaudeAdapterAcceptsASeatAliasAndPassesItsHarnessArgs(t *testing.T) {
	adapter, err := filepath.Abs(filepath.Join("backends", "claude", "run-claude"))
	if err != nil {
		t.Fatalf("resolving the adapter failed: %v", err)
	}
	extractor, err := filepath.Abs(filepath.Join("backends", "claude", "extract-report"))
	if err != nil {
		t.Fatalf("resolving the extractor failed: %v", err)
	}
	stream, err := filepath.Abs(filepath.Join("backends", "claude", "testdata", "complete.jsonl"))
	if err != nil {
		t.Fatalf("resolving the fixture stream failed: %v", err)
	}

	// The harness stub records its argv one argument per line and replays the fixture.
	bin := filepath.Join(scratch(t, "stub"), "bin")
	argvLog := filepath.Join(filepath.Dir(bin), "argv.log")
	writeExecutable(t, filepath.Join(bin, "claude"),
		"#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > \"$STUB_ARGV\"\ncat \"$STUB_STREAM\"\n")

	lane := filepath.Join(scratch(t, "lane"), "lane")
	worktree := filepath.Join(lane, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("creating the worktree failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lane, "brief.md"), []byte(filledBrief(t, worktree)), 0o644); err != nil {
		t.Fatalf("writing the brief failed: %v", err)
	}

	run := exec.Command(adapter, "--backend", "claude-designer", "--model", "model-x", "--effort", "medium",
		"--", "--disallowed-tools", "WebSearch", "Bash(git push *)")
	run.Dir = worktree
	run.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_STREAM="+stream,
		"STUB_ARGV="+argvLog,
	)
	if out, err := run.Output(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("the adapter refused backend claude-designer with exit %d: %s", exit.ExitCode(), exit.Stderr)
		}
		t.Fatalf("running the adapter failed: %v", err)
	} else if len(out) == 0 {
		t.Fatalf("the adapter produced no stream, so the run log would be empty")
	}
	recorded, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("the stub recorded no argv: %v", err)
	}
	argv := strings.Split(strings.TrimSuffix(string(recorded), "\n"), "\n")
	want := []string{"--strict-mcp-config", "--dangerously-skip-permissions", "--disallowed-tools", "WebSearch", "Bash(git push *)"}
	if len(argv) < len(want) || strings.Join(argv[len(argv)-len(want):], "|") != strings.Join(want, "|") {
		t.Fatalf("the harness argv %q does not end with the fixed flags and then the harness arguments %q", argv, want)
	}

	report, err := exec.Command(extractor, "--backend", "claude-designer", "--model", "model-x", "--effort", "medium", "--exit-code", "0", stream).Output()
	if err != nil {
		t.Fatalf("the extractor failed on the complete stream: %v", err)
	}
	for _, want := range []string{"backend: claude-designer", "complete: yes", "result: done", "tokens: 58831", "cost usd: 0.004100"} {
		if !strings.Contains(string(report), want) {
			t.Fatalf("the report does not carry %q:\n%s", want, report)
		}
	}
}

func TestTheClaudeAdapterAccountSubscriptionStripsBedrockEnvAndPassesSettings(t *testing.T) {
	adapter, err := filepath.Abs(filepath.Join("backends", "claude", "run-claude"))
	if err != nil {
		t.Fatalf("resolving the adapter failed: %v", err)
	}
	extractor, err := filepath.Abs(filepath.Join("backends", "claude", "extract-report"))
	if err != nil {
		t.Fatalf("resolving the extractor failed: %v", err)
	}
	stream, err := filepath.Abs(filepath.Join("backends", "claude", "testdata", "complete.jsonl"))
	if err != nil {
		t.Fatalf("resolving the fixture stream failed: %v", err)
	}
	rateLimited, err := filepath.Abs(filepath.Join("backends", "claude", "testdata", "rate-limited.jsonl"))
	if err != nil {
		t.Fatalf("resolving the rate-limited fixture failed: %v", err)
	}

	bin := filepath.Join(scratch(t, "stub"), "bin")
	argvLog := filepath.Join(filepath.Dir(bin), "argv.log")
	envLog := filepath.Join(filepath.Dir(bin), "env.log")
	writeExecutable(t, filepath.Join(bin, "claude"),
		"#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > \"$STUB_ARGV\"\nenv > \"$STUB_ENV\"\ncat \"$STUB_STREAM\"\n")

	lane := filepath.Join(scratch(t, "lane"), "lane")
	worktree := filepath.Join(lane, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("creating the worktree failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lane, "brief.md"), []byte(filledBrief(t, worktree)), 0o644); err != nil {
		t.Fatalf("writing the brief failed: %v", err)
	}

	run := exec.Command(adapter, "--backend", "claude", "--model", "model-x", "--effort", "medium", "--account", "subscription")
	run.Dir = worktree
	run.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_STREAM="+stream,
		"STUB_ARGV="+argvLog,
		"STUB_ENV="+envLog,
		"CLAUDE_CODE_USE_BEDROCK=1",
		"ANTHROPIC_BEDROCK_REGION_PREFIX=global",
		"ANTHROPIC_SMALL_FAST_MODEL=fixture-small",
		"ANTHROPIC_DEFAULT_MODEL=fixture-default",
		"ANTHROPIC_DEFAULT_CUSTOM_MODEL=fixture-custom",
	)
	if out, err := run.Output(); err != nil {
		t.Fatalf("running adapter with --account subscription failed: %v: %s", err, out)
	}
	recordedArgv, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("reading argv log failed: %v", err)
	}
	if !strings.Contains(string(recordedArgv), "--settings\n") || !strings.Contains(string(recordedArgv), `"CLAUDE_CODE_USE_BEDROCK":""`) {
		t.Fatalf("subscription run did not pass --settings overriding CLAUDE_CODE_USE_BEDROCK:\n%s", recordedArgv)
	}
	recordedEnv, err := os.ReadFile(envLog)
	if err != nil {
		t.Fatalf("reading env log failed: %v", err)
	}
	for _, forbidden := range []string{
		"CLAUDE_CODE_USE_BEDROCK=",
		"ANTHROPIC_BEDROCK_REGION_PREFIX=",
		"ANTHROPIC_SMALL_FAST_MODEL=",
		"ANTHROPIC_DEFAULT_MODEL=",
		"ANTHROPIC_DEFAULT_CUSTOM_MODEL=",
	} {
		if strings.Contains(string(recordedEnv), forbidden) {
			t.Fatalf("subscription run leaked %q into claude env", forbidden)
		}
	}

	// Unknown --account values are refused at preflight (exit 2).
	bad := exec.Command(adapter, "--backend", "claude", "--model", "model-x", "--effort", "medium", "--account", "bogus")
	bad.Dir = worktree
	bad.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := bad.CombinedOutput(); err == nil {
		t.Fatalf("expected exit 2 for unknown --account, got 0: %s", out)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
		t.Fatalf("expected exit 2 for unknown --account, got %v: %s", err, out)
	}

	// The rate-limited fixture extracts the rejected five_hour window.
	report, err := exec.Command(extractor, "--backend", "claude", "--model", "model-x", "--effort", "medium", "--exit-code", "1", rateLimited).CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit from extractor on rate-limited fixture, got 0:\n%s", report)
	}
	for _, want := range []string{"rate limit kind: five_hour", "fraction: 1.02", "status: rejected", "reset: 1795128600"} {
		if !strings.Contains(string(report), want) {
			t.Fatalf("rate-limited report does not carry %q:\n%s", want, report)
		}
	}
}

func TestDefaultBriefTemplateHasNoRepoSpecificRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("backends", "brief-template.md"))
	if err != nil {
		t.Fatalf("reading default brief template: %v", err)
	}
	body := string(raw)
	for _, forbidden := range []string{
		"pack mirror",
		"pack-sync",
		"go-test-shards",
		"just done",
		"tools/tillr-drain",
		"tools/tillr-gate",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("default brief-template.md must not carry this repository's rule %q", forbidden)
		}
	}
}

func TestNonLandingBriefTemplatePassesPiPreflight(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("backends", "brief-template-non-landing.md"))
	if err != nil {
		t.Fatalf("reading non-landing brief template: %v", err)
	}
	body := string(raw)
	for _, forbidden := range []string{
		"pack mirror",
		"pack-sync",
		"go-test-shards",
		"just done",
		"{{BASE_SHA}}",
		"{{OUT_OF_FENCE_PATHS}}",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("brief-template-non-landing.md must not contain %q", forbidden)
		}
	}

	adapter, err := filepath.Abs(filepath.Join("backends", "pi", "run-pi"))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := filepath.Abs(filepath.Join("backends", "pi", "testdata", "complete.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	base := scratch(t, "non-landing-pi")
	bin := filepath.Join(base, "bin")
	writeExecutable(t, filepath.Join(bin, "pi"), "#!/bin/sh\ncat \"$STUB_STREAM\"\n")
	lane := filepath.Join(base, "lane")
	work := filepath.Join(lane, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	plainValues := map[string]string{
		"ITEM_ID":         "finding-001",
		"TITLE":           "verify non-landing brief preflight",
		"RUN_ID":          "run-finding-001-a1",
		"BACKEND":         "pi",
		"TARGET":          work,
		"TARGET_KIND":     "plain directory",
		"DATE":            "2026-11-26",
		"TIMEOUT_SECONDS": "none",
		"DURABLE_DIR":     filepath.Join(lane, "durable"),
		"TASK":            "Investigate the finding and record evidence.",
		"CRITERION_1":     "the pi adapter accepts the staged brief",
		"SEAT_LINES":      "- `investigate` — Investigate the finding.",
		"SETTLED":         "None recorded.",
		"FACTS":           "None carried.",
		"METHOD":          "Run the investigation.",
		"VERIFICATION":    "None recorded.",
	}
	rendered := body
	for k, v := range plainValues {
		rendered = strings.ReplaceAll(rendered, "{{"+k+"}}", v)
	}
	if err := os.WriteFile(filepath.Join(lane, "brief.md"), []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "low")
	cmd.Dir = work
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "TILLR_RESUME_PROMPT_FILE=") && !strings.HasPrefix(entry, "TILLR_SESSION_MODE=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_STREAM="+stream,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		stderr, _ := os.ReadFile(filepath.Join(lane, "stream.err"))
		t.Fatalf("pi adapter refused non-landing brief: %v: %s %s", err, out, stderr)
	}
}

func TestTriageBriefTemplatePreflightAndNoLandingHooks(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("backends", "brief-template-triage.md"))
	if err != nil {
		t.Fatalf("reading brief-template-triage.md: %v", err)
	}
	template := string(raw)

	requiredPlaceholders := []string{
		"ALERT_ID", "RULE_ID", "RUN_ID", "BACKEND", "STORE_PATH",
		"REPO_ROOT", "TARGET", "DURABLE_DIR", "DATE", "TIMEOUT_SECONDS",
		"SEAT_LINES", "EPISODE_METADATA", "TIMELINE_BUNDLE", "CORRELATION_CONTEXT",
	}
	for _, ph := range requiredPlaceholders {
		if !strings.Contains(template, "{{"+ph+"}}") {
			t.Fatalf("brief-template-triage.md missing placeholder {{%s}}", ph)
		}
	}
	if !strings.Contains(template, "- **Target:** {{TARGET}} (read-only root view)") {
		t.Fatalf("brief-template-triage.md must set `- **Target:** {{TARGET}} (read-only root view)`")
	}

	requiredHeadings := []string{
		"## Header",
		"## Task",
		"## Scope and boundaries",
		"## Seats",
		"## Settled decisions",
		"## Facts carried",
		"## Method",
		"## Verification commands",
		"## Report shape",
		"## Stop conditions",
	}
	for _, h := range requiredHeadings {
		if !strings.Contains(template, h) {
			t.Fatalf("brief-template-triage.md missing heading %q", h)
		}
	}

	base := scratch(t, "triage-brief")
	lane := filepath.Join(base, "lane")
	work := filepath.Join(lane, "work")
	durable := filepath.Join(lane, "durable")
	for _, d := range []string{work, durable} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	values := map[string]string{
		"ALERT_ID":            "alert-001",
		"RULE_ID":             "drain-item-fault",
		"RUN_ID":              "run-triage-1",
		"BACKEND":             "pi-scout",
		"STORE_PATH":          filepath.Join(base, ".tillr", "state.db"),
		"REPO_ROOT":           base,
		"TARGET":              work,
		"DURABLE_DIR":         durable,
		"DATE":                "2026-11-26",
		"TIMEOUT_SECONDS":     "300",
		"SEAT_LINES":          "- `triage` (read-only) — Triage alerts.",
		"EPISODE_METADATA":    "state=open, repeat_count=1",
		"TIMELINE_BUNDLE":     "seq=10 drain.item-fault",
		"CORRELATION_CONTEXT": "none",
	}
	rendered := template
	for k, v := range values {
		rendered = strings.ReplaceAll(rendered, "{{"+k+"}}", v)
	}
	briefPath := filepath.Join(lane, "brief.md")
	if err := os.WriteFile(briefPath, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}

	preflight, err := filepath.Abs(filepath.Join("backends", "preflight.sh"))
	if err != nil {
		t.Fatal(err)
	}
	preflightCmd := exec.Command("sh", "-c", `. "$1"; PROG=test; preflight_brief "$2"`, "test", preflight, briefPath)
	if out, err := preflightCmd.CombinedOutput(); err != nil {
		t.Fatalf("preflight_brief rejected rendered triage brief: %v: %s", err, out)
	}

	// Verify run-pi accepts it and does not enable lane-worktree keep-going or landing hooks.
	adapter, err := filepath.Abs(filepath.Join("backends", "pi", "run-pi"))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := filepath.Abs(filepath.Join("backends", "pi", "testdata", "complete.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(base, "bin")
	argvFile := filepath.Join(base, "argv.txt")
	writeExecutable(t, filepath.Join(bin, "pi"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE_ARGV\"\ncat \"$STUB_STREAM\"\n")
	if err := os.WriteFile(filepath.Join(lane, "base_sha"), []byte(strings.Repeat("0", 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(adapter, "--backend", "pi-scout", "--model", "fixture-model", "--effort", "medium")
	cmd.Dir = work
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "TILLR_RESUME_PROMPT_FILE=") && !strings.HasPrefix(entry, "TILLR_SESSION_MODE=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_STREAM="+stream,
		"CAPTURE_ARGV="+argvFile,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		stderr, _ := os.ReadFile(filepath.Join(lane, "stream.err"))
		t.Fatalf("pi adapter refused triage brief: %v: %s %s", err, out, stderr)
	}
	argvBytes, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	argvStr := string(argvBytes)
	if strings.Contains(argvStr, "keep-going.ts") || strings.Contains(argvStr, "land.ts") {
		t.Fatalf("triage brief unexpectedly enabled lane-worktree hooks:\n%s", argvStr)
	}
}
