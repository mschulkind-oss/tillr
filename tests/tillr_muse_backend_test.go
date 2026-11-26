package backend_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type dialectSpec struct {
	harness string
	fixture string
}

var dialects = map[string]dialectSpec{
	"muse":   {"muse", "complete.jsonl"},
	"pi":     {"pi", "complete.jsonl"},
	"agy":    {"agy", "complete.json"},
	"claude": {"claude", "complete.jsonl"},
}

var fixtureSpend = map[string][2]string{
	"muse":   {"15", "unknown"},
	"pi":     {"1500", "0.030000"},
	"agy":    {"21212", "unknown"},
	"claude": {strconv.Itoa(4 + 17547 + 40945 + 335), "0.004100"},
}

var claudeTruncatedTokens = strconv.Itoa((2 + 17547 + 20000 + 120) + (2 + 0 + 20945 + 215))

const (
	expectedBody              = "paths changed: src/example.go\nverification: go test ./... -> ok\n\nRESULT: done\n"
	expectedBodyWithoutFooter = "paths changed: src/example.go\nverification: go test ./... -> ok\n"
)

var expectedHeaderKeys = []string{
	"backend", "model", "route", "effort", "session", "exit code",
	"started", "finished", "stream lines", "events", "unparseable lines",
	"tokens", "cost usd", "rate limit kind", "fraction", "status", "reset",
	"complete", "result",
}

const stubHarnessScript = `#!/bin/sh
{
  printf 'call\n'
  printf 'cwd=%s\n' "$(pwd -P)"
  printf 'env:BOARD_DRAIN_BRIEF=%s\n' "${BOARD_DRAIN_BRIEF-}"
  printf 'env:BOARD_DRAIN_RUN=%s\n' "${BOARD_DRAIN_RUN-}"
  printf 'env:BOARD_DRAIN_SESSION=%s\n' "${BOARD_DRAIN_SESSION-}"
  printf 'env:YOLO_NO_LAUNCH_FLAGS=%s\n' "${YOLO_NO_LAUNCH_FLAGS-}"
  printf 'parent=%s\n' "$(tr '\0' ' ' < "/proc/$PPID/cmdline" 2>/dev/null)"
  for a in "$@"; do printf 'arg=%s\n' "$(printf '%s' "$a" | tr '\n' '\001')"; done
  first=$(head -n 1 2>/dev/null)
  printf 'stdin=%s\n' "$first"
} >> "$STUB_LOG"
if [ "${1-}" = model-profile ]; then exit 0; fi
printf 'stub stderr\n' >&2
cat "$STUB_STREAM"
exit "${STUB_EXIT-0}"
`

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(wd) == "tests" {
		return filepath.Dir(wd)
	}
	return wd
}

func splitReport(text string) ([]string, map[string]string, string) {
	head, body, _ := strings.Cut(text, "\n---\n")
	var keys []string
	header := make(map[string]string)
	for _, line := range strings.Split(head, "\n") {
		if k, v, ok := strings.Cut(line, ": "); ok {
			keys = append(keys, k)
			header[k] = v
		}
	}
	return keys, header, body
}

func runExtract(t *testing.T, backendsDir, backend, stream string, args ...string) (int, string, string) {
	t.Helper()
	all := append(args, stream)
	cmd := exec.Command(filepath.Join(backendsDir, backend, "extract-report"), all...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("extract-report %s: %v", backend, err)
		}
	}
	return code, stdout.String(), stderr.String()
}

func TestExtractors(t *testing.T) {
	t.Setenv("TILLR_PI_FEATURES", "parked-tool-calls")
	root := repoRoot(t)
	backendsDir := filepath.Join(root, "tools", "tillr-run", "backends")
	scratch := t.TempDir()

	for backend, spec := range dialects {
		complete := filepath.Join(backendsDir, backend, "testdata", spec.fixture)
		truncated := filepath.Join(backendsDir, backend, "testdata", strings.ReplaceAll(spec.fixture, "complete", "truncated"))
		model := "fixture-model-" + backend

		code, stdout, stderr := runExtract(t, backendsDir, backend, complete, "--backend", backend, "--model", model)
		if code != 0 {
			t.Fatalf("%s: complete exited %d: %s", backend, code, stderr)
		}
		keys, header, body := splitReport(stdout)
		if strings.Join(keys, ",") != strings.Join(expectedHeaderKeys, ",") {
			t.Fatalf("%s: header keys = %v, want %v", backend, keys, expectedHeaderKeys)
		}
		windowKeys := []string{"rate limit kind", "fraction", "status", "reset"}
		wantWindow := []string{"unknown", "unknown", "unknown", "unknown"}
		if backend == "claude" {
			wantWindow = []string{"five_hour", "0.5", "allowed", "1794794400"}
		}
		for i, wk := range windowKeys {
			if header[wk] != wantWindow[i] {
				t.Fatalf("%s: %s=%q want %q", backend, wk, header[wk], wantWindow[i])
			}
		}
		if header["backend"] != backend || header["complete"] != "yes" || header["result"] != "done" {
			t.Fatalf("%s: unexpected header %v", backend, header)
		}
		if body != expectedBody || strings.Count(body, "RESULT: done") != 1 {
			t.Fatalf("%s: unexpected body %q", backend, body)
		}
		if header["session"] == "unknown" {
			t.Fatalf("%s: missing session id", backend)
		}
		wantTokens, wantCost := fixtureSpend[backend][0], fixtureSpend[backend][1]
		if header["tokens"] != wantTokens || header["cost usd"] != wantCost {
			t.Fatalf("%s: spend tokens=%s cost=%s, want %s %s", backend, header["tokens"], header["cost usd"], wantTokens, wantCost)
		}
		if header["model"] != model || header["route"] != "unknown" {
			t.Fatalf("%s: model=%s route=%s", backend, header["model"], header["route"])
		}

		// Negative control: truncated stream
		mCode, mOut, mErr := runExtract(t, backendsDir, backend, truncated, "--backend", backend, "--model", model)
		if mCode != 4 || !strings.Contains(mErr, "INCOMPLETE") {
			t.Fatalf("%s: truncated exited %d stderr=%q", backend, mCode, mErr)
		}
		_, mHeader, mBody := splitReport(mOut)
		if mHeader["complete"] != "no" || mHeader["result"] != "missing" || mBody != expectedBodyWithoutFooter || !strings.HasPrefix(expectedBody, mBody) {
			t.Fatalf("%s: truncated header=%v body=%q", backend, mHeader, mBody)
		}
		if backend == "claude" {
			completeRaw, _ := os.ReadFile(complete)
			var filtered []string
			for _, line := range strings.Split(string(completeRaw), "\n") {
				if line != "" && !strings.Contains(line, `"rate_limit_event"`) {
					filtered = append(filtered, line)
				}
			}
			noWin := filepath.Join(scratch, "claude-no-window.jsonl")
			_ = os.WriteFile(noWin, []byte(strings.Join(filtered, "\n")+"\n"), 0o644)
			_, absHeader, _ := splitReport(func() string { _, s, _ := runExtract(t, backendsDir, backend, noWin); return s }())
			for _, wk := range windowKeys {
				if absHeader[wk] != "unknown" {
					t.Fatalf("claude no window %s=%q", wk, absHeader[wk])
				}
			}
			lastWin := filepath.Join(scratch, "claude-last-window.jsonl")
			ev, _ := json.Marshal(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"rateLimitType": "seven_day", "utilization": 1, "status": "rejected", "resetsAt": 1795000000}})
			_ = os.WriteFile(lastWin, append(completeRaw, append(ev, '\n')...), 0o644)
			_, lOut, _ := runExtract(t, backendsDir, backend, lastWin)
			_, lastH, lastB := splitReport(lOut)
			if lastH["rate limit kind"] != "seven_day" || lastH["fraction"] != "1" || lastH["status"] != "rejected" || lastH["reset"] != "1795000000" || lastB != expectedBody {
				t.Fatalf("claude last window: %v", lastH)
			}
			if mHeader["tokens"] != claudeTruncatedTokens || mHeader["cost usd"] != "unknown" || mHeader["session"] != "33333333-3333-4333-8333-333333333333" {
				t.Fatalf("claude truncated spend/session: %v", mHeader)
			}
		}

		_, rOut, _ := runExtract(t, backendsDir, backend, complete, "--backend", backend, "--route", "fixture-route-"+backend)
		_, rHeader, _ := splitReport(rOut)
		if rHeader["route"] != "fixture-route-"+backend {
			t.Fatalf("%s: route=%q", backend, rHeader["route"])
		}
		if backend == "muse" {
			_, dOut, _ := runExtract(t, backendsDir, backend, complete, "--backend", backend)
			_, dHeader, _ := splitReport(dOut)
			if dHeader["model"] != "fixture-model-1" {
				t.Fatalf("muse derived model=%q", dHeader["model"])
			}
		}
		if backend == "pi" {
			_, pOut, _ := runExtract(t, backendsDir, backend, complete, "--backend", backend)
			_, pHeader, _ := splitReport(pOut)
			if pHeader["session"] != "22222222-2222-7222-8222-222222222222" {
				t.Fatalf("pi session=%q", pHeader["session"])
			}
		}

		completeRaw, _ := os.ReadFile(complete)
		for _, tc := range []struct {
			verdict string
			want    int
		}{
			{"failed", 1},
			{"blocked", 3},
			{"trunk-broken", 5},
		} {
			rewritten := filepath.Join(scratch, tc.verdict+"-"+backend+"-"+spec.fixture)
			_ = os.WriteFile(rewritten, bytes.ReplaceAll(completeRaw, []byte("RESULT: done"), []byte("RESULT: "+tc.verdict)), 0o644)
			vCode, vOut, _ := runExtract(t, backendsDir, backend, rewritten, "--backend", backend)
			if vCode != tc.want {
				t.Fatalf("%s %s exited %d want %d", backend, tc.verdict, vCode, tc.want)
			}
			_, vHeader, _ := splitReport(vOut)
			if vHeader["result"] != tc.verdict || vHeader["complete"] != "yes" {
				t.Fatalf("%s %s header=%v", backend, tc.verdict, vHeader)
			}
		}
	}
}

func checkNoModelIDs(t *testing.T, root, commit string, ids []string, expectFail bool) {
	t.Helper()
	uniq := make(map[string]bool)
	for _, id := range ids {
		if id != "" {
			uniq[id] = true
		}
	}
	var sorted []string
	for id := range uniq {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	args := []string{"grep", "-a", "-z", "-o", "-w", "-F", "--no-textconv", "--no-recurse-submodules", "--no-color", "--no-line-number", "--no-column"}
	for _, id := range sorted {
		args = append(args, "-e", id)
	}
	args = append(args, commit, "--", "tools/tillr*")
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			if expectFail {
				t.Fatalf("expected model id match in %s", commit)
			}
			return
		}
		t.Fatalf("git grep: %v", err)
	}
	if !expectFail {
		t.Fatalf("unexpected model id in tillr tree: %s", out)
	}
}

func TestPolicyAndModelIDScan(t *testing.T) {
	root := repoRoot(t)
	commitOut, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.TrimSpace(string(commitOut))
	rawPolicy, err := exec.Command("git", "-C", root, "show", commit+":config/tillr/backend-policy.json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		About          string `json:"about"`
		DefaultBackend string `json:"default_backend"`
		Backends       map[string]struct {
			Adapter        string   `json:"adapter"`
			Model          string   `json:"model"`
			Models         []string `json:"models"`
			Effort         string   `json:"effort"`
			TimeoutSeconds *int     `json:"timeout_seconds"`
		} `json:"backends"`
	}
	if err := json.Unmarshal(rawPolicy, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.About == "" {
		t.Fatal("policy missing about")
	}
	if _, ok := policy.Backends[policy.DefaultBackend]; !ok {
		t.Fatalf("default_backend %q not in backends", policy.DefaultBackend)
	}
	knownAdapters := make(map[string]bool)
	for name := range dialects {
		knownAdapters["tools/tillr-run/backends/"+name+"/run-"+name] = true
	}
	var ids []string
	for name, entry := range policy.Backends {
		if !knownAdapters[entry.Adapter] {
			t.Fatalf("%s: unknown adapter %q", name, entry.Adapter)
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(entry.Adapter)))
		if err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("%s: adapter not executable", name)
		}
		if entry.Model == "" || entry.TimeoutSeconds != nil {
			t.Fatalf("%s: invalid model or timeout_seconds", name)
		}
		if len(entry.Models) > 0 && entry.Models[0] != entry.Model {
			t.Fatalf("%s: models[0] != model", name)
		}
		ids = append(ids, entry.Model)
		ids = append(ids, entry.Models...)
	}
	if policy.Backends["muse"].Effort == "" || policy.Backends["pi"].Effort == "" || policy.Backends["agy"].Effort != "" {
		t.Fatal("unexpected effort configuration on muse/pi/agy")
	}
	checkNoModelIDs(t, root, commit, ids, false)

	// Detached model ID scan positive/negative controls
	proof := t.TempDir()
	runProofGit := func(args ...string) string {
		all := append([]string{"-C", proof, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid"}, args...)
		cmd := exec.Command("git", all...)
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"GIT_CEILING_DIRECTORIES=" + filepath.Dir(proof),
			"GIT_CONFIG_GLOBAL=" + os.DevNull,
			"GIT_CONFIG_SYSTEM=" + os.DevNull,
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runProofGit("init", "-q", "--template=")
	relName := "tools/tillr-fixture/model-scan.txt"
	p := filepath.Join(proof, filepath.FromSlash(relName))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	scanIDs := []string{"opus", "muse-ember-1.0-contributor"}
	clean := "autopush stopped; muse-ember-1x0-contributor is not a model ID\n"
	_ = os.WriteFile(p, []byte(clean), 0o644)
	runProofGit("add", "--", relName)
	runProofGit("commit", "--no-gpg-sign", "-qm", "clean")
	cleanCommit := runProofGit("rev-parse", "HEAD")
	checkNoModelIDs(t, proof, cleanCommit, scanIDs, false)
	for _, mid := range scanIDs {
		_ = os.WriteFile(p, []byte(fmt.Sprintf("model = %q\n", mid)), 0o644)
		runProofGit("add", "--", relName)
		runProofGit("commit", "--no-gpg-sign", "-qm", "red")
		redCommit := runProofGit("rev-parse", "HEAD")
		_ = os.WriteFile(p, []byte(clean), 0o644)
		checkNoModelIDs(t, proof, cleanCommit, scanIDs, false)
		checkNoModelIDs(t, proof, redCommit, scanIDs, true)
	}
}

type backendWorkspace struct {
	t           *testing.T
	root        string
	bin         string
	log         string
	backendsDir string
	template    string
	lanes       int
}

func newBackendWorkspace(t *testing.T) *backendWorkspace {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	_ = os.MkdirAll(bin, 0o755)
	for _, spec := range dialects {
		_ = os.WriteFile(filepath.Join(bin, spec.harness), []byte(stubHarnessScript), 0o755)
	}
	logPath := filepath.Join(root, "calls.log")
	_ = os.WriteFile(logPath, nil, 0o644)
	repo := repoRoot(t)
	backendsDir := filepath.Join(repo, "tools", "tillr-run", "backends")
	tmplRaw, err := os.ReadFile(filepath.Join(backendsDir, "brief-template.md"))
	if err != nil {
		t.Fatal(err)
	}
	return &backendWorkspace{
		t:           t,
		root:        root,
		bin:         bin,
		log:         logPath,
		backendsDir: backendsDir,
		template:    string(tmplRaw),
	}
}

func (w *backendWorkspace) filledBrief(lane string) string {
	vals := map[string]string{
		"ITEM_ID":            "7",
		"TITLE":              "a staged brief for the acceptance test",
		"RUN_ID":             "run_7",
		"BACKEND":            "muse",
		"BASE_SHA":           strings.Repeat("0", 40),
		"TARGET":             filepath.Join(lane, "worktree"),
		"TARGET_KIND":        "lane worktree",
		"DATE":               "2026-11-12",
		"TIMEOUT_SECONDS":    "1800",
		"DRIVER_REASON":      "unused in the worktree form",
		"MAIN_STATUS_BEFORE": "unused in the worktree form",
		"TASK":               "Add the one line the item asks for.",
		"CRITERION_1":        "the acceptance test passes (not command-verifiable in a fixture)",
		"OUT_OF_FENCE_PATHS": "- everything else",
		"SEAT_LINES":         "- `build` — Build the change.",
		"SETTLED":            "- the report shape is fixed by the extractor, not by the harness.",
		"FACTS":              "- backends/ holds one directory per harness.",
		"METHOD":             "Write the failing case first.",
		"VERIFICATION":       "```\ngo test -count=1 tests/tillr_muse_backend_test.go\n```",
	}
	text := w.template
	for k, v := range vals {
		text = strings.ReplaceAll(text, "{{"+k+"}}", v)
	}
	return text
}

func (w *backendWorkspace) newLane(brief string) string {
	w.lanes++
	lane := filepath.Join(w.root, fmt.Sprintf("lane-%d", w.lanes))
	_ = os.MkdirAll(filepath.Join(lane, "worktree"), 0o755)
	if brief == "" {
		brief = w.filledBrief(lane)
	}
	_ = os.WriteFile(filepath.Join(lane, "brief.md"), []byte(brief), 0o644)
	_ = os.WriteFile(filepath.Join(lane, "fence.txt"), []byte("tools/tillr-run/backends\n"), 0o644)
	_ = os.WriteFile(filepath.Join(lane, "base_sha"), []byte(strings.Repeat("0", 40)+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(lane, "session-id"), []byte("44444444-4444-7444-8444-444444444444\n"), 0o644)
	return lane
}

func (w *backendWorkspace) invoke(backend, lane, stream, pathOverride string, exitCode int, args ...string) (int, string, string) {
	w.t.Helper()
	if stream == "" {
		stream = filepath.Join(w.root, "empty.stream")
	}
	cmd := exec.Command(filepath.Join(w.backendsDir, backend, "run-"+backend), args...)
	cmd.Dir = filepath.Join(lane, "worktree")
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TILLR_RESUME_PROMPT_FILE=") || strings.HasPrefix(e, "TILLR_SESSION_MODE=") || strings.HasPrefix(e, "PATH=") {
			continue
		}
		env = append(env, e)
	}
	p := w.bin + string(os.PathListSeparator) + os.Getenv("PATH")
	if pathOverride != "" {
		p = pathOverride
	}
	env = append(env,
		"PATH="+p,
		"STUB_LOG="+w.log,
		"STUB_STREAM="+stream,
		"STUB_EXIT="+strconv.Itoa(exitCode),
	)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			w.t.Fatalf("run-%s: %v", backend, err)
		}
	}
	return code, stdout.String(), stderr.String()
}

func (w *backendWorkspace) calls() [][]string {
	raw, _ := os.ReadFile(w.log)
	var out [][]string
	for _, chunk := range strings.Split(string(raw), "call\n") {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		out = append(out, strings.Split(strings.TrimSpace(chunk), "\n"))
	}
	return out
}

func callArgs(call []string) []string {
	var out []string
	for _, line := range call {
		if strings.HasPrefix(line, "arg=") {
			out = append(out, strings.ReplaceAll(strings.TrimPrefix(line, "arg="), "\001", "\n"))
		}
	}
	return out
}

func callField(call []string, prefix string) string {
	for _, line := range call {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}

func TestPreflightAndAdapterArgv(t *testing.T) {
	t.Setenv("TILLR_PI_FEATURES", "parked-tool-calls")
	w := newBackendWorkspace(t)

	for backend := range dialects {
		req := []string{"--backend", backend}
		if backend != "agy" {
			req = append(req, "--effort", "medium")
		}
		type preflightCase struct {
			args  []string
			brief string
		}
		cases := map[string]preflightCase{
			"no model":        {req, ""},
			"another backend": {append([]string{"--backend", "some-other-adapter", "--model", "fixture-model-x"}, func() []string {
				if backend != "agy" {
					return []string{"--effort", "medium"}
				}
				return nil
			}()...), ""},
			"no brief":      {append(append([]string(nil), req...), "--model", "fixture-model-x"), "delete"},
			"no sections":   {append(append([]string(nil), req...), "--model", "fixture-model-x"), "sectionless"},
			"a placeholder": {append(append([]string(nil), req...), "--model", "fixture-model-x"), "placeholder"},
		}
		if backend == "agy" {
			cases["an effort flag"] = preflightCase{append(append([]string(nil), req...), "--model", "fixture-model-x", "--effort", "high"), ""}
		}
		if backend == "claude" {
			cases["no effort"] = preflightCase{[]string{"--backend", "claude", "--model", "fixture-model-x"}, ""}
			cases["an unknown flag before --"] = preflightCase{append(append([]string(nil), req...), "--model", "fixture-model-x", "--tools", "Read", "--", "--verbose"), ""}
		} else {
			cases["a harness-argument tail"] = preflightCase{append(append([]string(nil), req...), "--model", "fixture-model-x", "--", "--tools", "Read"), ""}
		}

		for label, tc := range cases {
			lane := w.newLane("")
			switch tc.brief {
			case "delete":
				_ = os.Remove(filepath.Join(lane, "brief.md"))
			case "sectionless":
				_ = os.WriteFile(filepath.Join(lane, "brief.md"), []byte(strings.ReplaceAll(w.filledBrief(lane), "## Scope and boundaries", "## Not the scope")), 0o644)
			case "placeholder":
				_ = os.WriteFile(filepath.Join(lane, "brief.md"), []byte(w.filledBrief(lane)+"\n{{UNFILLED}}\n"), 0o644)
			}
			_ = os.WriteFile(w.log, nil, 0o644)
			code, _, stderr := w.invoke(backend, lane, "", "", 0, tc.args...)
			if code != 2 || strings.TrimSpace(stderr) == "" || len(w.calls()) != 0 {
				t.Fatalf("%s %s: code=%d stderr=%q calls=%d", backend, label, code, stderr, len(w.calls()))
			}
		}
	}

	// Muse argv & timeout
	lane := w.newLane("")
	museStream := filepath.Join(w.backendsDir, "muse", "testdata", "complete.jsonl")
	_ = os.WriteFile(w.log, nil, 0o644)
	code, stdout, stderr := w.invoke("muse", lane, museStream, "", 0, "--backend", "muse", "--model", "fixture-model-1", "--effort", "high")
	if code != 0 {
		t.Fatalf("muse exited %d: %s", code, stderr)
	}
	museRaw, _ := os.ReadFile(museStream)
	if stdout != string(museRaw) {
		t.Fatal("muse stdout != stream")
	}
	calls := w.calls()
	if len(calls) != 2 {
		t.Fatalf("muse calls = %d", len(calls))
	}
	argv := callArgs(calls[1])
	if argv[0] != "exec" || strings.HasPrefix(callField(calls[1], "parent="), "timeout ") {
		t.Fatalf("muse unexpected argv/parent: %v", argv)
	}

	// Pi argv & env
	lane = w.newLane("")
	piStream := filepath.Join(w.backendsDir, "pi", "testdata", "complete.jsonl")
	_ = os.WriteFile(w.log, nil, 0o644)
	code, _, stderr = w.invoke("pi", lane, piStream, "", 0, "--backend", "pi", "--model", "fixture-model-pi", "--effort", "medium")
	if code != 0 {
		t.Fatalf("pi exited %d: %s", code, stderr)
	}
	piCalls := w.calls()
	if len(piCalls) != 1 || callField(piCalls[0], "env:BOARD_DRAIN_BRIEF=") != filepath.Join(lane, "brief.md") {
		t.Fatalf("pi unexpected calls: %v", piCalls)
	}

	// Agy argv & env
	lane = w.newLane("")
	agyStream := filepath.Join(w.backendsDir, "agy", "testdata", "complete.json")
	_ = os.WriteFile(w.log, nil, 0o644)
	code, _, stderr = w.invoke("agy", lane, agyStream, "", 0, "--backend", "agy", "--model", "fixture-model-agy", "--timeout", "90")
	if code != 0 {
		t.Fatalf("agy exited %d: %s", code, stderr)
	}
	agyCalls := w.calls()
	if len(agyCalls) != 1 || !strings.HasPrefix(callField(agyCalls[0], "parent="), "timeout 90 ") || callField(agyCalls[0], "env:YOLO_NO_LAUNCH_FLAGS=") != "1" {
		t.Fatalf("agy unexpected calls: %v", agyCalls)
	}

	// Claude argv, tail, subscription, and missing binary
	lane = w.newLane("")
	claudeStream := filepath.Join(w.backendsDir, "claude", "testdata", "complete.jsonl")
	_ = os.WriteFile(w.log, nil, 0o644)
	tail := []string{"--tools", "Read,Edit,Bash", "--append-system-prompt", "two words\nand a second line"}
	args := append([]string{"--backend", "claude-designer", "--model", "fixture-model-claude", "--effort", "medium", "--"}, tail...)
	code, _, stderr = w.invoke("claude", lane, claudeStream, "", 0, args...)
	if code != 0 {
		t.Fatalf("claude exited %d: %s", code, stderr)
	}
	cCalls := w.calls()
	if len(cCalls) != 1 {
		t.Fatalf("claude calls = %d", len(cCalls))
	}
	cArgv := callArgs(cCalls[0])
	if len(cArgv) < len(tail) || strings.Join(cArgv[len(cArgv)-len(tail):], "|") != strings.Join(tail, "|") {
		t.Fatalf("claude tail mismatch: %v", cArgv)
	}

	bare := filepath.Join(w.root, "bare-bin")
	_ = os.MkdirAll(bare, 0o755)
	for _, tool := range []string{"awk", "dirname", "grep", "wc"} {
		if p, err := exec.LookPath(tool); err == nil {
			_ = os.Symlink(p, filepath.Join(bare, tool))
		}
	}
	lane = w.newLane("")
	code, _, stderr = w.invoke("claude", lane, "", bare, 0, "--backend", "claude", "--model", "fixture-model-claude", "--effort", "medium")
	if code != 2 || !strings.Contains(stderr, "'claude' is not on PATH") {
		t.Fatalf("claude missing binary: code=%d stderr=%q", code, stderr)
	}
}
