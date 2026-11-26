package loop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

const stubTillrGate = `#!/bin/sh
if [ "$1" = classify ]; then
  own=$(cd "$(dirname "$0")" && pwd -P)
  oldifs=$IFS; IFS=:
  for dir in $PATH; do
    IFS=$oldifs
    [ "$(cd "$dir" 2>/dev/null && pwd -P)" = "$own" ] && continue
    [ -x "$dir/tillr-gate" ] && exec "$dir/tillr-gate" "$@"
  done
  exit 127
fi
log() { printf '%s\n' "$*" >> "$STUB_LOG"; }
log "$@"
case "$1" in
  plan) exit "${STUB_PLAN:-0}" ;;
  checks)
    log "checks-cwd: $(pwd)"
    log "spans-out: ${TILLR_GATE_SPANS_OUT:-}"
    printf 'revision stdout\n'
    printf 'revision stderr\n' >&2
    exit "${STUB_REVISION:-0}" ;;
  run)
    program=""
    args=""
    while [ $# -gt 0 ]; do
      case "$1" in
        --gate) program="$2"; shift 2 ;;
        --gate-arg) args="$args $2"; shift 2 ;;
        *) shift ;;
      esac
    done
    if [ -n "${STUB_EXEC_GATE:-}" ] && [ -n "$program" ]; then
      # shellcheck disable=SC2086
      $program $args
      exit $?
    fi
    exit "${STUB_RUN:-0}" ;;
esac
exit 0
`

const stubTillrLand = `#!/bin/sh
log() { printf '%s\n' "$*" >> "$STUB_LOG"; }
log "$@"
case "$1" in
  pass) exit "${STUB_LAND:-0}" ;;
esac
exit 0
`

const stubTillrItem = `#!/bin/sh
log() { printf '%s\n' "$*" >> "$STUB_LOG"; }
log "$@"
case "$1" in
  show) cat "$STUB_ITEM_SHOW"; exit 0 ;;
  verdict) exit "${STUB_VERDICT:-0}" ;;
esac
exit 0
`

const stubPython3 = `#!/bin/sh
log() { printf '%s\n' "$*" >> "$STUB_LOG"; }
log "python3-argv: $*"
log "python3-cwd: $(pwd)"
exit 0
`

type testFixture struct {
	t        *testing.T
	tmp      string
	repo     string
	base     string
	head     string
	bin      string
	log      string
	itemShow string
	workDir  string
	run      string
	runDir   string
	durable  string
}

func clearGitRepoEnv(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
			"GIT_IMPLICIT_WORK_TREE", "GIT_NAMESPACE", "GIT_GRAFT_FILE",
			"GIT_SHALLOW_FILE", "GIT_REPLACE_REF_BASE", "GIT_NO_REPLACE_OBJECTS",
			"GIT_PREFIX", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT":
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func runGit(t *testing.T, dir string, extraEnv []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(clearGitRepoEnv(os.Environ()), extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func newFixture(t *testing.T, withStubs bool) *testFixture {
	t.Helper()
	tmp := resolvePath(t.TempDir())
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	ident := []string{
		"GIT_AUTHOR_NAME=tillr loop test",
		"GIT_AUTHOR_EMAIL=tillr-loop@example.invalid",
		"GIT_COMMITTER_NAME=tillr loop test",
		"GIT_COMMITTER_EMAIL=tillr-loop@example.invalid",
	}
	runGit(t, repo, ident, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, ident, "add", "file.txt")
	runGit(t, repo, ident, "commit", "-q", "-m", "base")
	base := runGit(t, repo, nil, "rev-parse", "HEAD")

	runGit(t, repo, ident, "checkout", "-q", "-b", "tillr/lane-1")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("base\ncandidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, ident, "commit", "-q", "-am", "candidate")
	head := runGit(t, repo, nil, "rev-parse", "HEAD")
	runGit(t, repo, nil, "checkout", "-q", "main")

	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if withStubs {
		for name, body := range map[string]string{
			"tillr-gate": stubTillrGate,
			"tillr-land": stubTillrLand,
			"tillr-item": stubTillrItem,
		} {
			p := filepath.Join(bin, name)
			if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	logFile := filepath.Join(tmp, "calls.log")
	if err := os.WriteFile(logFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	itemShowData := map[string]any{
		"record_version": 1,
		"kind":           "item.record",
		"id":             "item-1",
		"producer":       map[string]any{"system": "tillr", "utility": "tillr-item", "version": "0.1.0"},
		"created_at":     "2026-01-01T00:00:00Z",
		"payload": map[string]any{
			"item_id": "item-1",
			"title":   "the item under test",
			"state":   "ready",
			"criteria": []map[string]any{
				{"criterion_id": "c1", "text": "the first criterion", "state": "pending", "claim_class": "verification-only"},
				{"criterion_id": "c2", "text": "the second criterion", "state": "pending", "claim_class": "verification-only"},
			},
		},
	}
	itemShowBytes, _ := json.Marshal(itemShowData)
	itemShowFile := filepath.Join(tmp, "item-show.json")
	if err := os.WriteFile(itemShowFile, itemShowBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	workDir := filepath.Join(tmp, "work")
	run := "run-1"
	durable := filepath.Join(tmp, "durable")

	f := &testFixture{
		t:        t,
		tmp:      tmp,
		repo:     repo,
		base:     base,
		head:     head,
		bin:      bin,
		log:      logFile,
		itemShow: itemShowFile,
		workDir:  workDir,
		run:      run,
		runDir:   filepath.Join(workDir, run),
		durable:  durable,
	}
	t.Setenv("TILLR_INSTALL_ROOT", "")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STUB_LOG", logFile)
	t.Setenv("STUB_ITEM_SHOW", itemShowFile)
	t.Setenv("YOLO_DURABLE_DIR", durable)
	return f
}

func (f *testFixture) writeStub(name, body string) string {
	f.t.Helper()
	p := filepath.Join(f.bin, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *testFixture) gateVerdict() {
	f.t.Helper()
	if err := os.MkdirAll(f.runDir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.runDir, "gate.verdict.json"), []byte("{}\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *testFixture) declare(checkID, scope, tier string) {
	f.t.Helper()
	dir := filepath.Join(f.repo, "tools", "gate", "checks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"id": checkID, "scope": scope, "tier": tier})
	if err := os.WriteFile(filepath.Join(dir, checkID+".json"), raw, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *testFixture) calls() []string {
	f.t.Helper()
	data, err := os.ReadFile(f.log)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func (f *testFixture) clearLog() {
	f.t.Helper()
	if err := os.WriteFile(f.log, nil, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *testFixture) candidate() []string {
	return []string{
		"--item", "item-1", "--lane", "lane-1", "--run", f.run,
		"--worktree", f.repo, "--base", f.base, "--head", f.head,
	}
}

func (f *testFixture) common() []string {
	return []string{
		"--root", f.repo, "--store", filepath.Join(f.tmp, "state.db"),
		"--work-dir", f.workDir, "--now", "2026-01-01T00:00:00Z",
	}
}

func (f *testFixture) runAdapter(kind string, extra ...string) (int, string, string) {
	f.t.Helper()
	argv := append([]string{kind}, f.candidate()...)
	argv = append(argv, f.common()...)
	argv = append(argv, extra...)
	return runMain(argv...)
}

func runMain(args ...string) (int, string, string) {
	var outBuf, errBuf bytes.Buffer
	code := Main(args, strings.NewReader(""), &outBuf, &errBuf)
	return code, outBuf.String(), errBuf.String()
}

func repoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return resolvePath(filepath.Join(cwd, "..", "..", ".."))
}

func pathWithoutJudges(pathEnv string) string {
	var kept []string
	for _, entry := range filepath.SplitList(pathEnv) {
		dir := entry
		if dir == "" {
			dir = "."
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "tillr-*"))
		if len(matches) > 0 {
			continue
		}
		kept = append(kept, entry)
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

func TestGateExitMapping(t *testing.T) {
	f := newFixture(t, true)
	t.Chdir(repoRoot(t))
	reg := filepath.Join(f.tmp, "registry.json")

	cases := []struct {
		stubRun  string
		expected int
	}{
		{"0", ExitPass},
		{"1", ExitFail},
		{"4", ExitExternal},
		{"6", ExitStalled},
		{"7", ExitExternal},
	}
	for _, tc := range cases {
		t.Setenv("STUB_RUN", tc.stubRun)
		code, _, stderr := f.runAdapter("gate", "--registry", reg)
		if code != tc.expected {
			t.Fatalf("stub_run=%s: got exit %d, want %d; stderr=%s", tc.stubRun, code, tc.expected, stderr)
		}
	}

	t.Setenv("STUB_RUN", "0")
	t.Setenv("STUB_PLAN", "3")
	code, _, stderr := f.runAdapter("gate", "--registry", reg)
	if code != ExitExternal {
		t.Fatalf("stub_plan=3: got exit %d, want %d; stderr=%s", code, ExitExternal, stderr)
	}
}

func TestGateNamesGateProgramAndArgv(t *testing.T) {
	f := newFixture(t, true)
	root := repoRoot(t)
	t.Chdir(root)
	f.writeStub("python3", stubPython3)
	t.Setenv("STUB_EXEC_GATE", "1")
	reg := filepath.Join(f.tmp, "registry.json")

	code, _, stderr := f.runAdapter("gate", "--registry", reg)
	if code != ExitPass {
		t.Fatalf("got exit %d, want 0; stderr=%s", code, stderr)
	}
	installedGate := filepath.Join(root, "tools", "tillr-gate", "change-gate.py")
	want := "--logs " + filepath.Join(f.runDir, "logs") +
		" --gate python3 --gate-arg " + installedGate +
		" --gate-arg --base --gate-arg " + f.base +
		" --gate-arg --head --gate-arg " + f.head +
		" --gate-arg --runner --gate-arg tillr-gate" +
		" --gate-arg --logs --gate-arg " + filepath.Join(f.runDir, "change-logs")
	calls := f.calls()
	var runs []string
	var pyArgvs []string
	for _, c := range calls {
		if strings.HasPrefix(c, "run ") {
			runs = append(runs, c)
		}
		if strings.HasPrefix(c, "python3-argv: ") {
			pyArgvs = append(pyArgvs, c)
		}
	}
	if len(runs) == 0 || !strings.Contains(runs[len(runs)-1], want) {
		t.Fatalf("run argv mismatch: got %v, want substring %q", runs, want)
	}
	wantPy := "python3-argv: " + installedGate + " --base " + f.base + " --head " + f.head +
		" --runner tillr-gate --logs " + filepath.Join(f.runDir, "change-logs")
	if len(pyArgvs) != 1 || pyArgvs[0] != wantPy {
		t.Fatalf("python3-argv got %v, want [%q]", pyArgvs, wantPy)
	}
	if !slices.Contains(calls, "python3-cwd: "+f.repo) {
		t.Fatalf("missing python3-cwd: %v", calls)
	}
}

func TestGatePlansAgainstCandidateDeclarations(t *testing.T) {
	f := newFixture(t, true)
	t.Chdir(repoRoot(t))

	code, _, stderr := f.runAdapter("gate")
	if code != ExitPass {
		t.Fatalf("got %d: %s", code, stderr)
	}
	want := "--registry " + filepath.Join(f.repo, "tools", "gate", "checks")
	var lastPlan string
	for _, c := range f.calls() {
		if strings.HasPrefix(c, "plan ") {
			lastPlan = c
		}
	}
	if !strings.Contains(lastPlan, want) {
		t.Fatalf("expected %q in plan %q", want, lastPlan)
	}

	f.clearLog()
	named := filepath.Join(f.tmp, "registry.json")
	code, _, stderr = f.runAdapter("gate", "--registry", named)
	if code != ExitPass {
		t.Fatalf("got %d: %s", code, stderr)
	}
	lastPlan = ""
	for _, c := range f.calls() {
		if strings.HasPrefix(c, "plan ") {
			lastPlan = c
		}
	}
	if !strings.Contains(lastPlan, "--registry "+named) || strings.Contains(lastPlan, want) {
		t.Fatalf("named registry plan mismatch: %q", lastPlan)
	}
}

func TestGateDerivesChangeWithGateBinary(t *testing.T) {
	f := newFixture(t, true)
	t.Chdir(repoRoot(t))
	reg := filepath.Join(f.tmp, "registry.json")

	code, _, stderr := f.runAdapter("gate", "--registry", reg)
	if code != ExitPass {
		t.Fatalf("got %d: %s", code, stderr)
	}
	calls := f.calls()
	want := "change --item item-1 --lane lane-1 --run " + f.run +
		" --worktree " + f.repo + " --base " + f.base + " --head " + f.head +
		" --now 2026-01-01T00:00:00Z --output " + filepath.Join(f.runDir, "gate.change.json")
	if len(calls) == 0 || calls[0] != want {
		t.Fatalf("change call got %v, want %q", calls, want)
	}
	planIdx := -1
	for i, c := range calls {
		if strings.HasPrefix(c, "plan ") {
			planIdx = i
		}
	}
	if planIdx <= 0 || !strings.Contains(calls[planIdx], "--input "+filepath.Join(f.runDir, "gate.change.json")) {
		t.Fatalf("plan call missing or before change: %v", calls)
	}
	if _, err := os.Stat(filepath.Join(f.runDir, "gate.change.json")); err == nil {
		t.Fatalf("adapter should not write gate.change.json itself")
	}
}

func TestLandExitMappingAndFlags(t *testing.T) {
	f := newFixture(t, true)
	f.gateVerdict()

	for _, tc := range []struct {
		stub string
		want int
	}{
		{"0", ExitPass},
		{"1", ExitFail},
		{"2", ExitExternal},
		{"3", ExitExternal},
	} {
		f.clearLog()
		t.Setenv("STUB_LAND", tc.stub)
		code, _, stderr := f.runAdapter("land")
		if code != tc.want {
			t.Fatalf("stub=%s: got %d, want %d (%s)", tc.stub, code, tc.want, stderr)
		}
	}
	for _, c := range f.calls() {
		if strings.HasPrefix(c, "enqueue ") {
			t.Fatalf("unexpected enqueue call: %v", f.calls())
		}
	}

	f.clearLog()
	t.Setenv("STUB_LAND", "0")
	code, _, stderr := f.runAdapter("land")
	if code != ExitPass {
		t.Fatalf("got %d: %s", code, stderr)
	}
	calls := f.calls()
	wantPlain := "pass --store " + filepath.Join(f.tmp, "state.db") +
		" --repo " + f.repo + " --item item-1 --run " + f.run +
		" --base " + f.base + " --head " + f.head +
		" --verdict " + filepath.Join(f.runDir, "gate.verdict.json") +
		" --base-ref main --now 2026-01-01T00:00:00Z --run-dir " + f.runDir
	if len(calls) != 1 || calls[0] != wantPlain || strings.Contains(calls[0], "--candidate") ||
		strings.Contains(calls[0], "--message-from") || strings.Contains(calls[0], "--observed-trunk") {
		t.Fatalf("plain pass got %v, want %q", calls, wantPlain)
	}

	f.clearLog()
	code, _, stderr = f.runAdapter("land", "--message-from", "worker-sha", "--observed-trunk", f.base)
	if code != ExitPass {
		t.Fatalf("got %d: %s", code, stderr)
	}
	calls = f.calls()
	want := wantPlain + " --message-from worker-sha --observed-trunk " + f.base
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("pass got %v, want %q", calls, want)
	}
}

func TestLandWithoutVerdictRefuses(t *testing.T) {
	f := newFixture(t, true)
	code, _, stderr := f.runAdapter("land")
	if code != ExitExternal {
		t.Fatalf("got %d, want %d", code, ExitExternal)
	}
	if !strings.Contains(stderr, "verdict") {
		t.Fatalf("expected verdict in stderr: %s", stderr)
	}
	if len(f.calls()) != 0 {
		t.Fatalf("expected no calls, got %v", f.calls())
	}
}

func TestDescribeContract(t *testing.T) {
	emptyDir := t.TempDir()
	t.Chdir(emptyDir)
	code, stdout, stderr := runMain("describe")
	if code != ExitPass {
		t.Fatalf("describe failed: %d %s", code, stderr)
	}
	if entries, err := os.ReadDir(emptyDir); err != nil || len(entries) != 0 {
		t.Fatalf("describe wrote files: %v (err=%v)", entries, err)
	}
	var doc struct {
		Hooks map[string][]string `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	expected := map[string][]string{
		"gate":       {"--item", "--lane", "--run", "--worktree", "--base", "--head", "--root"},
		"land":       {"--item", "--lane", "--run", "--worktree", "--base", "--head", "--root", "--observed-trunk", "--base-ref", "--message-from"},
		"verdict":    {"--item", "--lane", "--run", "--worktree", "--base", "--head", "--root", "--store", "--verdict"},
		"fixer-gate": {"--item", "--lane", "--run", "--worktree", "--base", "--head", "--root", "--only"},
		"replay":     {"--only"},
		"rolling":    {"--all", "--since"},
	}
	if len(doc.Hooks) != len(expected) {
		t.Fatalf("hooks keys mismatch: %v", doc.Hooks)
	}
	for name, req := range expected {
		got, ok := doc.Hooks[name]
		if !ok {
			t.Fatalf("missing hook %s", name)
		}
		for _, r := range req {
			if !slices.Contains(got, r) {
				t.Fatalf("hook %s missing flag %s in %v", name, r, got)
			}
		}
		var accepted []string
		for _, def := range flagsFor(name) {
			accepted = append(accepted, def.name)
		}
		for _, g := range got {
			if !slices.Contains(accepted, g) {
				t.Fatalf("hook %s advertises unaccepted flag %s (accepted=%v)", name, g, accepted)
			}
		}
	}

	code, _, _ = runMain("describe", "--all")
	if code != ExitUsage {
		t.Fatalf("describe --all: got %d, want 2", code)
	}
}

func TestHookDispatchMatchesDrainFlagCases(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("go", "test", "./drain", "-run", "^TestTillrLoopDefinitionHookFlagParity$")
	cmd.Dir = filepath.Join(root, "tools", "tillr-drain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("drain hook flag parity failed: %v\n%s", err, string(out))
	}
}

func TestEveryArgvDrainSendsIsAccepted(t *testing.T) {
	root := repoRoot(t)
	f := newFixture(t, true)
	f.writeStub("python3", stubPython3)
	runGit(t, f.repo, nil, "checkout", "-q", "tillr/lane-1")
	t.Chdir(f.repo)

	raw, err := os.ReadFile(filepath.Join(root, "tools", "tillr-loop", "drain-argv.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Case string   `json:"case"`
		Face string   `json:"face"`
		Run  string   `json:"run"`
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}

	store := filepath.Join(f.tmp, "state.db")
	t.Setenv("TILLR_LOOP_REGISTRY", filepath.Join(f.tmp, "registry.json"))
	installRoot := filepath.Join(f.tmp, "install")
	_ = os.MkdirAll(filepath.Join(installRoot, "tillr-gate"), 0o755)
	_ = os.WriteFile(filepath.Join(installRoot, "tillr-gate", "change-gate.py"), []byte("# stub\n"), 0o644)
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)

	seenFaces := map[string]bool{}
	parseDir := t.TempDir()
	for _, tc := range cases {
		seenFaces[tc.Face] = true
		// check-argv mode with raw placeholder args
		t.Chdir(parseDir)
		checkArgs := append([]string{tc.Face, "--check-argv"}, tc.Args...)
		if code, _, stderr := runMain(checkArgs...); code != ExitPass {
			t.Fatalf("check-argv %s failed: %d %s", tc.Case, code, stderr)
		}
		if entries, err := os.ReadDir(parseDir); err != nil || len(entries) != 0 {
			t.Fatalf("check-argv wrote files for %s: %v (err=%v)", tc.Case, entries, err)
		}
		t.Chdir(f.repo)

		if tc.Face == "fixer-gate" {
			idx := slices.Index(tc.Args, "--only")
			dir := filepath.Join(f.repo, "tools", "gate", "checks")
			_ = os.MkdirAll(dir, 0o755)
			raw, _ := json.Marshal(map[string]string{"id": tc.Args[idx+1], "scope": "tree", "tier": "heavy"})
			_ = os.WriteFile(filepath.Join(dir, "fixture.json"), raw, 0o644)
		}
		args := make([]string, len(tc.Args))
		for i, w := range tc.Args {
			w = strings.ReplaceAll(w, "<worktree>", f.repo)
			w = strings.ReplaceAll(w, "<store>", store)
			w = strings.ReplaceAll(w, "<root>", f.repo)
			w = strings.ReplaceAll(w, "<head>", f.head)
			args[i] = w
		}
		rd := filepath.Join(f.repo, ".tillr", "loop", tc.Run)
		_ = os.MkdirAll(rd, 0o755)
		_ = os.WriteFile(filepath.Join(rd, "gate.verdict.json"), []byte("{}\n"), 0o644)
		f.clearLog()

		code, _, stderr := runMain(append([]string{tc.Face}, args...)...)
		if code != ExitPass {
			t.Fatalf("case %s failed: %d %s", tc.Case, code, stderr)
		}
		var filtered []string
		for _, c := range f.calls() {
			if !strings.HasPrefix(c, "spans-out: ") {
				filtered = append(filtered, c)
			}
		}
		last := ""
		if len(filtered) > 0 {
			last = filtered[len(filtered)-1]
		}
		wantPrefix := map[string]string{
			"gate":       "run ",
			"fixer-gate": "checks-cwd: ",
			"land":       "pass ",
			"verdict":    "verdict record ",
		}[tc.Face]
		if !strings.HasPrefix(last, wantPrefix) {
			t.Fatalf("case %s: last call %q does not start with %q (calls=%v)", tc.Case, last, wantPrefix, filtered)
		}
		if tc.Face == "land" {
			for _, flag := range []string{"--observed-trunk", "--message-from", "--base-ref"} {
				if idx := slices.Index(args, flag); idx >= 0 {
					if !strings.Contains(last, flag+" "+args[idx+1]) {
						t.Fatalf("case %s: missing %s %s in %q", tc.Case, flag, args[idx+1], last)
					}
				}
			}
		}
	}
	for _, wantFace := range []string{"gate", "fixer-gate", "land", "verdict"} {
		if !seenFaces[wantFace] {
			t.Fatalf("drain-argv.json missing face %s (got %v)", wantFace, seenFaces)
		}
	}

	code, stdout, _ := runMain("land", "--help")
	if code != ExitPass || !strings.Contains(stdout, "tillr-step-argv-check-v1") {
		t.Fatalf("land --help missing marker: code=%d stdout=%s", code, stdout)
	}
	badArgs := append([]string{"land", "--check-argv"}, cases[3].Args...)
	badArgs = append(badArgs, "--unknown")
	code, _, stderr := runMain(badArgs...)
	if code != ExitUsage || !strings.Contains(stderr, "--unknown") {
		t.Fatalf("expected exit 2 for --unknown: code=%d stderr=%s", code, stderr)
	}
}

func TestRevisionHooksAndFixerGate(t *testing.T) {
	f := newFixture(t, true)
	t.Chdir(f.repo)
	f.declare("heavy-one", "tree", "heavy")
	f.declare("fast-one", "tree", "fast")
	f.declare("change-one", "change", "fast")

	installRoot := filepath.Join(f.tmp, "install")
	_ = os.MkdirAll(filepath.Join(installRoot, "tillr-gate"), 0o755)
	_ = os.WriteFile(filepath.Join(installRoot, "tillr-gate", "tree-check.py"), []byte(
		"import os, sys\n"+
			"open(os.environ['STUB_LOG'], 'a').write('tree-check ' + ' '.join(sys.argv[1:]) + '\\n')\n"+
			"sys.exit(int(os.environ.get('STUB_REVISION', '0')))\n",
	), 0o644)

	logs := filepath.Join(f.durable, "tillr-loop-revision-logs")
	heavy := "checks --heavy-tree --base HEAD --head HEAD --worktree . --checks tools/gate/checks --logs " + logs
	spansOut := filepath.Join(f.tmp, "spans.jsonl")
	t.Setenv("TILLR_GATE_SPANS_OUT", spansOut)
	for _, tc := range []struct {
		name   string
		flags  []string
		suffix string
	}{
		{"replay", []string{"--only", "heavy-one"}, " --only heavy-one"},
		{"rolling", []string{"--all"}, ""},
		{"rolling", []string{"--since", f.base}, ""},
	} {
		for _, codeStr := range []string{"0", "1", "2", "4", "6"} {
			f.clearLog()
			t.Setenv("STUB_REVISION", codeStr)
			code, stdout, stderr := runMain(append([]string{tc.name}, tc.flags...)...)
			wantCode := int(codeStr[0] - '0')
			if code != wantCode || stdout != "revision stdout\n" || stderr != "revision stderr\n" {
				t.Fatalf("%s %v code=%d want=%d stdout=%q stderr=%q", tc.name, tc.flags, code, wantCode, stdout, stderr)
			}
			calls := f.calls()
			if len(calls) != 3 || !strings.HasPrefix(calls[0], heavy+"/") {
				t.Fatalf("%s %v calls=%v", tc.name, tc.flags, calls)
			}
			if tc.suffix != "" && !strings.HasSuffix(calls[0], tc.suffix) {
				t.Fatalf("%s %v missing suffix %q in %q", tc.name, tc.flags, tc.suffix, calls[0])
			}
			if (tc.suffix != "") != strings.Contains(calls[0], " --only ") {
				t.Fatalf("%s %v --only mismatch in %q", tc.name, tc.flags, calls[0])
			}
			if calls[1] != "checks-cwd: "+f.repo || calls[2] != "spans-out: "+spansOut {
				t.Fatalf("%s %v trailing calls=%v", tc.name, tc.flags, calls[1:])
			}
		}
	}

	t.Setenv("TILLR_INSTALL_ROOT", installRoot)
	for _, codeStr := range []string{"0", "1", "6"} {
		t.Setenv("STUB_REVISION", codeStr)
		f.clearLog()
		code, _, _ := runMain("replay", "--only", "fast-one")
		wantCode := int(codeStr[0] - '0')
		if code != wantCode {
			t.Fatalf("fast-one replay got %d, want %d", code, wantCode)
		}
		if calls := f.calls(); len(calls) != 1 || !strings.HasPrefix(calls[0], "tree-check --only fast-one --base HEAD --head HEAD --runner ") {
			t.Fatalf("unexpected calls: %v", calls)
		}
	}

	for _, refused := range []string{"change-one", "absent"} {
		f.clearLog()
		code, _, stderr := runMain("replay", "--only", refused)
		if code != ExitExternal || !strings.Contains(stderr, "is not a whole-tree declaration") || len(f.calls()) != 0 {
			t.Fatalf("refused %s: code=%d stderr=%s calls=%v", refused, code, stderr, f.calls())
		}
	}

	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"replay", nil},
		{"replay", []string{"--only", "x", "--replay"}},
		{"rolling", nil},
		{"rolling", []string{"--all", "--since", f.base}},
		{"rolling", []string{"--tier", "rolling", "--all"}},
	} {
		code, _, _ := runMain(append([]string{tc.name}, tc.flags...)...)
		if code != ExitUsage {
			t.Fatalf("expected usage error for %s %v, got %d", tc.name, tc.flags, code)
		}
	}

	// fixer-gate on candidate head
	runGit(t, f.repo, nil, "checkout", "-q", "tillr/lane-1")
	f.declare("heavy-one", "tree", "heavy")
	f.clearLog()
	t.Setenv("STUB_REVISION", "0")
	code, _, stderr := f.runAdapter("fixer-gate", "--only", "heavy-one")
	if code != ExitPass {
		t.Fatalf("fixer-gate got %d: %s", code, stderr)
	}
	calls := f.calls()
	if len(calls) < 2 || !strings.HasPrefix(calls[0], "checks --heavy-tree --base HEAD --head HEAD") ||
		!strings.HasSuffix(calls[0], " --only heavy-one") || calls[1] != "checks-cwd: "+f.repo {
		t.Fatalf("fixer-gate calls=%v", calls)
	}
	for _, obsolete := range [][]string{{"--all"}, {"--since", f.base}} {
		for _, face := range []string{"gate", "fixer-gate"} {
			code, _, stderr := f.runAdapter(face, obsolete...)
			if code != ExitUsage {
				t.Fatalf("obsolete %s %v: got %d, want %d (%s)", face, obsolete, code, ExitUsage, stderr)
			}
		}
	}
}

func TestVerdictFlowAndBuildNames(t *testing.T) {
	f := newFixture(t, true)
	f.gateVerdict()

	code, _, stderr := f.runAdapter("verdict", "--verdict", "pass")
	if code != ExitPass {
		t.Fatalf("verdict pass failed: %d %s", code, stderr)
	}
	records, _ := filepath.Glob(filepath.Join(f.runDir, "item.verdict.*.json"))
	slices.Sort(records)
	if len(records) != 2 {
		t.Fatalf("expected 2 verdict records, got %v", records)
	}
	raw, _ := os.ReadFile(records[0])
	var rec map[string]any
	_ = json.Unmarshal(raw, &rec)
	payloadMap, _ := rec["payload"].(map[string]any)
	if rec["kind"] != "item.verdict" || rec["record_version"] != float64(1) ||
		payloadMap["verdict"] != "done" || payloadMap["actor_kind"] != "mechanical" || payloadMap["criterion_hash"] == "" {
		t.Fatalf("unexpected record: %v", rec)
	}
	var verdictCalls []string
	for _, c := range f.calls() {
		if strings.HasPrefix(c, "verdict record ") {
			verdictCalls = append(verdictCalls, c)
		}
	}
	if len(verdictCalls) == 0 || !strings.Contains(verdictCalls[0], "--input "+records[0]) {
		t.Fatalf("verdict record call mismatch: %v", verdictCalls)
	}

	code, _, stderr = f.runAdapter("verdict", "--verdict", "pass-inherited")
	if code != ExitPass {
		t.Fatalf("verdict pass-inherited failed: %d %s", code, stderr)
	}

	t.Setenv("STUB_VERDICT", "2")
	code, _, stderr = f.runAdapter("verdict", "--verdict", "pass")
	if code != 2 {
		t.Fatalf("verdict stub 2 got %d, want 2 (%s)", code, stderr)
	}
	t.Setenv("STUB_VERDICT", "0")

	// failing verdict is usage error
	for _, r := range records {
		_ = os.Remove(r)
	}
	f.clearLog()
	code, _, stderr = f.runAdapter("verdict", "--verdict", "fail")
	if code != ExitUsage || !strings.Contains(stderr, "docs/design/tillr-judge-and-lifecycle.md#prevent-a-failed-landing-from-freezing-the-items-criteria") || len(f.calls()) != 0 {
		t.Fatalf("verdict fail got %d stderr=%s calls=%v", code, stderr, f.calls())
	}
	if rem, _ := filepath.Glob(filepath.Join(f.runDir, "item.verdict.*.json")); len(rem) != 0 {
		t.Fatalf("verdict fail wrote records: %v", rem)
	}

	// owner-only criterion is left
	var shown map[string]any
	b, _ := os.ReadFile(f.itemShow)
	_ = json.Unmarshal(b, &shown)
	payload := shown["payload"].(map[string]any)
	crits := payload["criteria"].([]any)
	crits[0].(map[string]any)["claim_class"] = "owner-only"
	nb, _ := json.Marshal(shown)
	_ = os.WriteFile(f.itemShow, nb, 0o644)

	code, stdout, stderr := f.runAdapter("verdict", "--verdict", "pass")
	if code != ExitPass || !strings.Contains(stdout, "c1") || !strings.Contains(stdout, "owner-only") {
		t.Fatalf("owner-only leave failed: %d %s %s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(f.runDir, "item.verdict.c1.json")); err == nil {
		t.Fatalf("c1 verdict file should not exist")
	}
	records, _ = filepath.Glob(filepath.Join(f.runDir, "item.verdict.*.json"))
	if len(records) != 1 {
		t.Fatalf("expected 1 verdict record, got %v", records)
	}
	raw, _ = os.ReadFile(records[0])
	_ = json.Unmarshal(raw, &rec)
	payloadMap = rec["payload"].(map[string]any)
	if payloadMap["criterion_id"] != "c2" || payloadMap["verdict"] != "done" {
		t.Fatalf("unexpected remaining record: %v", rec)
	}
	_ = os.Remove(records[0])

	// all non-mechanical criteria are left without writing or recording
	origCrits := crits
	payload["criteria"] = []map[string]any{
		{"criterion_id": "c1", "text": "a judgment", "claim_class": "red-gate-triage"},
		{"criterion_id": "c2", "text": "a judgment", "claim_class": "direction"},
		{"criterion_id": "c3", "text": "a judgment", "claim_class": "owner-only"},
	}
	nb, _ = json.Marshal(shown)
	_ = os.WriteFile(f.itemShow, nb, 0o644)
	f.clearLog()
	code, stdout, stderr = f.runAdapter("verdict", "--verdict", "pass")
	if code != ExitPass || strings.Count(stdout, "left criterion") != 3 {
		t.Fatalf("all non-mechanical failed: %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if rem, _ := filepath.Glob(filepath.Join(f.runDir, "item.verdict.*.json")); len(rem) != 0 {
		t.Fatalf("all non-mechanical wrote records: %v", rem)
	}
	for _, c := range f.calls() {
		if strings.HasPrefix(c, "verdict record ") {
			t.Fatalf("all non-mechanical called verdict record: %v", f.calls())
		}
	}

	// missing claim_class names both builds
	payload["criteria"] = origCrits
	delete(origCrits[0].(map[string]any), "claim_class")
	nb, _ = json.Marshal(shown)
	_ = os.WriteFile(f.itemShow, nb, 0o644)
	stamp := "adapter-revision-fixture digest adapter-digest exe /build/tillr-drain"
	t.Setenv("TILLR_LOOP_ADAPTER_BUILD", stamp)
	itemStubBytes, _ := os.ReadFile(filepath.Join(f.bin, "tillr-item"))
	itemStubSum := sha256.Sum256(itemStubBytes)
	code, _, stderr = f.runAdapter("verdict", "--verdict", "pass")
	if code != ExitExternal ||
		!strings.Contains(stderr, "tillr-item build is older than the adapter") ||
		!strings.Contains(stderr, stamp) ||
		!strings.Contains(stderr, resolvePath(filepath.Join(f.bin, "tillr-item"))) ||
		!strings.Contains(stderr, hex.EncodeToString(itemStubSum[:])) ||
		!strings.Contains(stderr, "launcher") ||
		strings.Contains(stderr, "no valid stored claim_class") {
		t.Fatalf("missing claim_class mismatch: code=%d stderr=%s", code, stderr)
	}
	if rem, _ := filepath.Glob(filepath.Join(f.runDir, "item.verdict.*.json")); len(rem) != 0 {
		t.Fatalf("missing claim_class wrote records: %v", rem)
	}

	// unknown / null / empty claim_class refusal
	for _, val := range []any{"unknown-class", nil, ""} {
		origCrits[0].(map[string]any)["claim_class"] = val
		nb, _ = json.Marshal(shown)
		_ = os.WriteFile(f.itemShow, nb, 0o644)
		code, _, stderr = f.runAdapter("verdict", "--verdict", "pass")
		if code != ExitExternal || strings.TrimSpace(stderr) != "tillr-loop verdict: criterion c1 has no valid stored claim_class" {
			t.Fatalf("unknown claim_class (%v) mismatch: code=%d stderr=%s", val, code, stderr)
		}
		if rem, _ := filepath.Glob(filepath.Join(f.runDir, "item.verdict.*.json")); len(rem) != 0 {
			t.Fatalf("unknown claim_class wrote records: %v", rem)
		}
	}

	// itemBuildName revision and unstamped paths
	nativeBin := filepath.Join(f.tmp, "native-tillr-item")
	nativeBytes := []byte("fixture native binary without a stamp")
	if err := os.WriteFile(nativeBin, nativeBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	goStubDir := t.TempDir()
	goStub := filepath.Join(goStubDir, "go")
	if err := os.WriteFile(goStub, []byte("#!/bin/sh\nprintf '\\tbuild\\tvcs.revision=fixture-revision\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", goStubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got := itemBuildName(nativeBin); got != "revision fixture-revision" {
		t.Fatalf("itemBuildName with go stamp = %q, want %q", got, "revision fixture-revision")
	}
	if err := os.WriteFile(goStub, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	unstamped := itemBuildName(nativeBin)
	nativeSum := sha256.Sum256(nativeBytes)
	if !strings.Contains(unstamped, resolvePath(nativeBin)) || !strings.Contains(unstamped, hex.EncodeToString(nativeSum[:])) {
		t.Fatalf("unstamped itemBuildName = %q", unstamped)
	}
	launcher := filepath.Join(repoRoot(t), "yolo-packs", "tillr", "programs", "tillr-item", "run")
	launcherBytes, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatal(err)
	}
	launcherSum := sha256.Sum256(launcherBytes)
	gotLauncher := itemBuildName(launcher)
	if !strings.Contains(gotLauncher, "launcher") || !strings.Contains(gotLauncher, resolvePath(launcher)) ||
		!strings.Contains(gotLauncher, hex.EncodeToString(launcherSum[:])) {
		t.Fatalf("launcher itemBuildName = %q", gotLauncher)
	}
}

func TestSelfCheckAndMissingJudge(t *testing.T) {
	f := newFixture(t, true)
	f.gateVerdict()
	root := repoRoot(t)
	t.Chdir(root)

	code, stdout, stderr := f.runAdapter("self-check", "--registry", filepath.Join(f.tmp, "registry.json"))
	if code != ExitPass {
		t.Fatalf("self-check failed: %d %s", code, stderr)
	}
	if len(f.calls()) != 0 {
		t.Fatalf("self-check ran stubs: %v", f.calls())
	}
	installedGate := filepath.Join(root, "tools", "tillr-gate", "change-gate.py")
	for _, want := range []string{
		"tillr-loop self-check: one tick's three judgments",
		"tools/tillr-loop/tillr-loop gate",
		"tools/tillr-loop/tillr-loop land",
		"tools/tillr-loop/tillr-loop verdict",
		"tillr-gate plan",
		"tillr-gate run",
		"--gate python3 --gate-arg " + installedGate,
		"--gate-arg --base --gate-arg " + f.base,
		"--gate-arg --head --gate-arg " + f.head,
		"--logs " + filepath.Join(f.runDir, "logs"),
		"tillr-land pass",
		"tillr-item verdict record",
		"head=" + f.head,
		"base=" + f.base,
		"--worktree " + f.repo,
		filepath.Join(f.runDir, "gate.verdict.json"),
		"tillr drain loop --root",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("self-check missing %q in:\n%s", want, stdout)
		}
	}

	// self-check without --registry shows the declarations directory
	code, stdout, stderr = f.runAdapter("self-check")
	if code != ExitPass {
		t.Fatalf("self-check (default registry) failed: %d %s", code, stderr)
	}
	if len(f.calls()) != 0 {
		t.Fatalf("self-check (default registry) ran stubs: %v", f.calls())
	}
	if !strings.Contains(stdout, "tillr-loop self-check: one tick's three judgments") ||
		!strings.Contains(stdout, "--registry "+filepath.Join(f.repo, "tools", "gate", "checks")) {
		t.Fatalf("self-check (default registry) output mismatch:\n%s", stdout)
	}

	// missing judge
	noJudges := pathWithoutJudges(os.Getenv("PATH"))
	t.Setenv("PATH", noJudges)
	for _, bin := range []string{"tillr-gate", "tillr-land", "tillr-item"} {
		if p, err := exec.LookPath(bin); err == nil {
			t.Fatalf("pathWithoutJudges still resolves %s to %s", bin, p)
		}
	}
	for _, tc := range []struct {
		kind  string
		extra []string
	}{
		{"gate", []string{"--registry", filepath.Join(f.tmp, "reg.json")}},
		{"land", nil},
		{"verdict", []string{"--verdict", "pass"}},
	} {
		code, _, stderr = f.runAdapter(tc.kind, tc.extra...)
		if code != ExitExternal || !strings.Contains(stderr, "not on PATH") {
			t.Fatalf("missing judge %s: code=%d stderr=%s", tc.kind, code, stderr)
		}
	}
}

func TestWorktreeRootAgreementAndNoRootRefusal(t *testing.T) {
	f := newFixture(t, true)
	installRoot := filepath.Join(f.tmp, "install")
	_ = os.MkdirAll(filepath.Join(installRoot, "tillr-gate"), 0o755)
	_ = os.WriteFile(filepath.Join(installRoot, "tillr-gate", "change-gate.py"), []byte("# stub\n"), 0o644)
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)
	t.Chdir(repoRoot(t))

	lane := filepath.Join(f.tmp, "lane-worktree")
	runGit(t, f.repo, nil, "worktree", "add", "-q", "--detach", lane, f.head)

	cand := []string{
		"--item", "item-1", "--lane", "lane-1", "--run", f.run,
		"--worktree", lane, "--base", f.base, "--head", f.head,
		"--registry", filepath.Join(f.tmp, "reg.json"),
	}
	want := filepath.Join(f.repo, ".tillr", "loop", f.run)
	for _, dir := range []string{f.repo, lane} {
		t.Chdir(dir)
		_, stdout, stderr := runMain(append([]string{"self-check"}, cand...)...)
		if !strings.Contains(stdout+stderr, want) {
			t.Fatalf("from %s: expected run dir %q in output:\n%s%s", dir, want, stdout, stderr)
		}
	}

	// no root refusal
	elsewhere := filepath.Join(f.tmp, "not-a-repo")
	_ = os.MkdirAll(elsewhere, 0o755)
	t.Setenv("GIT_CEILING_DIRECTORIES", f.tmp)
	t.Chdir(elsewhere)
	code, _, stderr := runMain(
		"land", "--item", "item-1", "--lane", "lane-1", "--run", f.run,
		"--worktree", elsewhere, "--base", f.base, "--head", f.head,
		"--work-dir", f.workDir, "--base-ref", "main",
	)
	if code != ExitUsage || !strings.Contains(stderr, "no root") {
		t.Fatalf("no root refusal: code=%d stderr=%s", code, stderr)
	}
}

func TestInstallRootAndDecoyRejection(t *testing.T) {
	f := newFixture(t, true)
	f.writeStub("python3", stubPython3)
	runGit(t, f.repo, nil, "checkout", "-q", "tillr/lane-1")
	f.declare("fast-one", "tree", "fast")
	_ = os.WriteFile(filepath.Join(f.repo, "tools", "gate", "change-gate.py"), []byte("import sys; sys.exit(99)\n"), 0o644)
	_ = os.WriteFile(filepath.Join(f.repo, "tools", "gate", "tree-check.py"), []byte("import sys; sys.exit(99)\n"), 0o644)

	installRoot := filepath.Join(f.tmp, "install")
	_ = os.MkdirAll(filepath.Join(installRoot, "tillr-gate"), 0o755)
	installedChange := filepath.Join(installRoot, "tillr-gate", "change-gate.py")
	installedTree := filepath.Join(installRoot, "tillr-gate", "tree-check.py")
	_ = os.WriteFile(installedChange, []byte("# installed\n"), 0o644)
	_ = os.WriteFile(installedTree, []byte(
		"import os, sys\n"+
			"open(os.environ['STUB_LOG'], 'a').write('installed-tree-check ' + ' '.join(sys.argv[1:]) + '\\n')\n"+
			"sys.exit(0)\n",
	), 0o644)

	t.Setenv("TILLR_INSTALL_ROOT", installRoot)
	t.Setenv("STUB_EXEC_GATE", "1")
	t.Chdir(f.repo)

	code, _, stderr := f.runAdapter("gate")
	if code != ExitPass {
		t.Fatalf("gate with TILLR_INSTALL_ROOT failed: %d %s", code, stderr)
	}
	wantCall := "python3-argv: " + installedChange + " --base " + f.base + " --head " + f.head +
		" --runner tillr-gate --logs " + filepath.Join(f.runDir, "change-logs")
	if !slices.Contains(f.calls(), wantCall) {
		t.Fatalf("expected %q in %v", wantCall, f.calls())
	}

	// self-check also prints the installed runner
	f.gateVerdict()
	code, stdout, stderr := f.runAdapter("self-check")
	if code != ExitPass || !strings.Contains(stdout, "--gate python3 --gate-arg "+installedChange) {
		t.Fatalf("self-check with TILLR_INSTALL_ROOT: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	// replay and fixer-gate resolve tree-check.py from TILLR_INSTALL_ROOT, ignoring decoy
	for _, tc := range []struct {
		kind string
		args []string
	}{
		{"replay", []string{"--only", "fast-one"}},
		{"fixer-gate", append(f.candidate(), "--only", "fast-one")},
	} {
		f.clearLog()
		code, _, stderr := runMain(append([]string{tc.kind}, tc.args...)...)
		if code != ExitPass {
			t.Fatalf("%s with TILLR_INSTALL_ROOT failed: %d %s", tc.kind, code, stderr)
		}
		found := false
		for _, c := range f.calls() {
			if strings.HasPrefix(c, "python3-argv: "+installedTree+" --only fast-one ") {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s did not invoke installed tree-check: %v", tc.kind, f.calls())
		}
	}

	// consumer repo without TILLR_INSTALL_ROOT refuses
	t.Setenv("TILLR_INSTALL_ROOT", "")
	code, _, stderr = f.runAdapter("gate")
	if code != ExitExternal || !strings.Contains(stderr, "TILLR_INSTALL_ROOT") {
		t.Fatalf("consumer without TILLR_INSTALL_ROOT: code=%d stderr=%s", code, stderr)
	}

	// relative TILLR_INSTALL_ROOT refuses
	t.Setenv("TILLR_INSTALL_ROOT", "relative/install")
	code, _, stderr = f.runAdapter("gate")
	if code != ExitExternal || !strings.Contains(stderr, "TILLR_INSTALL_ROOT") {
		t.Fatalf("relative install root: code=%d stderr=%s", code, stderr)
	}

	// missing runner refuses gate, self-check, replay, fixer-gate; --gate-program override works
	emptyInstall := filepath.Join(f.tmp, "empty-install")
	_ = os.MkdirAll(filepath.Join(emptyInstall, "tillr-gate"), 0o755)
	missingChange := filepath.Join(emptyInstall, "tillr-gate", "change-gate.py")
	missingTree := filepath.Join(emptyInstall, "tillr-gate", "tree-check.py")
	t.Setenv("TILLR_INSTALL_ROOT", emptyInstall)
	for _, kind := range []string{"gate", "self-check"} {
		f.clearLog()
		code, _, stderr = f.runAdapter(kind)
		if code != ExitExternal || !strings.Contains(stderr, missingChange) || len(f.calls()) != 0 {
			t.Fatalf("missing runner (%s): code=%d stderr=%s calls=%v", kind, code, stderr, f.calls())
		}
	}
	for _, tc := range []struct {
		kind string
		args []string
	}{
		{"replay", []string{"--only", "fast-one"}},
		{"fixer-gate", append(f.candidate(), "--only", "fast-one")},
	} {
		code, _, stderr = runMain(append([]string{tc.kind}, tc.args...)...)
		if code != ExitExternal || !strings.Contains(stderr, missingTree) {
			t.Fatalf("missing tree-check (%s): code=%d stderr=%s", tc.kind, code, stderr)
		}
	}
	t.Setenv("STUB_EXEC_GATE", "")
	f.clearLog()
	code, _, stderr = f.runAdapter("gate", "--gate-program", "custom-gate --flag")
	if code != ExitPass {
		t.Fatalf("override failed: %d %s", code, stderr)
	}
	var runCall string
	for _, c := range f.calls() {
		if strings.HasPrefix(c, "run ") {
			runCall = c
		}
	}
	if !strings.Contains(runCall, "--gate custom-gate --gate-arg --flag") {
		t.Fatalf("override run call mismatch: %q", runCall)
	}

	// itemBuildName digest helper check
	binFile := filepath.Join(f.tmp, "mybin")
	_ = os.WriteFile(binFile, []byte("#!/bin/sh\necho hi\n"), 0o755)
	sum := sha256.Sum256([]byte("#!/bin/sh\necho hi\n"))
	gotBuild := itemBuildName(binFile)
	if !strings.HasPrefix(gotBuild, "launcher ") || !strings.Contains(gotBuild, hex.EncodeToString(sum[:])) {
		t.Fatalf("unexpected itemBuildName: %s", gotBuild)
	}
}

func TestResolveRootNonLandingStoreNeverRunsGitRevParse(t *testing.T) {
	outer := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", outer).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	plainRoot := filepath.Join(outer, "sub", "plain")
	dbPath := filepath.Join(plainRoot, ".tillr", "state.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), dbPath, true)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defJSON := []byte(`{
		"definition": "verify-only",
		"version": 1,
		"states": ["queued", "blocked", "closed", "verified"],
		"ready": "queued",
		"blocked": "blocked",
		"closed": "closed",
		"finished": ["verified"],
		"transitions": [{"from": "queued", "to": "verified"}],
		"prerequisites": [],
		"completes": "hook",
		"brief_template": "template.md",
		"hooks": {"argv": ["true"], "names": ["complete-item"]}
	}`)
	recJSON, err := json.Marshal(map[string]any{
		"definition": "verify-only",
		"version":    1,
		"content":    string(defJSON),
		"sha256":     "unused",
		"default":    false,
	})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('store.definition', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(recJSON))
		return err
	}); err != nil {
		s.Close()
		t.Fatalf("writing store.definition: %v", err)
	}
	s.Close()

	workDir := filepath.Join(plainRoot, ".tillr", "lanes", "lane-1", "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Put a git spy on PATH that records every call and fails.
	binDir := t.TempDir()
	gitLog := filepath.Join(t.TempDir(), "git-calls.log")
	spy := filepath.Join(binDir, "git")
	if err := os.WriteFile(spy, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+gitLog+"\"\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(plainRoot)

	// Explicit --store without --root: must refuse without invoking git rev-parse.
	code, _, stderr := runMain("gate",
		"--item", "it-1", "--lane", "lane-1", "--run", "run-1",
		"--worktree", workDir, "--base", "aaa", "--head", "bbb",
		"--store", dbPath)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage without --root for non-landing store, got code=%d stderr=%s", code, stderr)
	}
	if calls, err := os.ReadFile(gitLog); err == nil && strings.TrimSpace(string(calls)) != "" {
		t.Fatalf("git was invoked for non-landing store:\n%s", calls)
	}

	// Implicit ./.tillr/state.db in cwd without --root or --store: must also refuse without git rev-parse.
	code, _, stderr = runMain("gate",
		"--item", "it-1", "--lane", "lane-1", "--run", "run-1",
		"--worktree", workDir, "--base", "aaa", "--head", "bbb")
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage for implicit non-landing store in cwd, got code=%d stderr=%s", code, stderr)
	}
	if calls, err := os.ReadFile(gitLog); err == nil && strings.TrimSpace(string(calls)) != "" {
		t.Fatalf("git was invoked for implicit non-landing store:\n%s", calls)
	}
}

func TestGateUsesExplicitTillrGateForClassify(t *testing.T) {
	f := newFixture(t, true)
	// Replace the PATH tillr-gate with a broken binary that fails on classify.
	brokenPathGate := filepath.Join(f.bin, "tillr-gate")
	if err := os.WriteFile(brokenPathGate, []byte("#!/bin/sh\necho 'broken path tillr-gate invoked' >&2\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Put a working candidate tillr-gate outside PATH and pass it via --tillr-gate.
	candDir := filepath.Join(f.tmp, "candidate-bin")
	if err := os.MkdirAll(candDir, 0o755); err != nil {
		t.Fatal(err)
	}
	candGate := filepath.Join(candDir, "tillr-gate")
	candLog := filepath.Join(f.tmp, "cand-gate.log")
	candScript := `#!/bin/sh
echo "cand:$*" >> ` + candLog + `
if [ "$1" = classify ]; then
  printf '[{"outcome":"pass","detail":"exit 0"}]\n'
  exit 0
fi
exit 0
`
	if err := os.WriteFile(candGate, []byte(candScript), 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := f.runAdapter("gate", "--tillr-gate", candGate, "--gate-program", "true")
	if code != ExitPass {
		t.Fatalf("gate with explicit --tillr-gate failed: code=%d stderr=%s", code, stderr)
	}
	raw, err := os.ReadFile(candLog)
	if err != nil {
		t.Fatalf("read candidate log: %v", err)
	}
	if !strings.Contains(string(raw), "cand:classify") {
		t.Fatalf("expected explicit --tillr-gate binary to handle classify, log:\n%s", raw)
	}
}
