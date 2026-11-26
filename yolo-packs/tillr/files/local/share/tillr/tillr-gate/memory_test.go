package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr/record"
)

func clearGitRepoEnvMain(env []string) []string {
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

func requireFixtureCommitRepo(t testing.TB, dir string, args []string) {
	t.Helper()
	commit := false
	for _, arg := range args {
		if arg == "commit" || arg == "commit-tree" {
			commit = true
			break
		}
	}
	if !commit {
		return
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolving fixture git dir %q: %v", dir, err)
	}
	cmd := exec.Command("git", "-C", absDir, "rev-parse", "--git-dir")
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range clearGitRepoEnvMain(os.Environ()) {
		if strings.HasPrefix(entry, "GIT_CEILING_DIRECTORIES=") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(absDir))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("refusing git %v in %s: directory is not its own repository (%v: %s)", args, dir, err, strings.TrimSpace(string(out)))
	}
}

func memoryGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	requireFixtureCommitRepo(t, root, args)
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = clearGitRepoEnvMain(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func memoryWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
func memoryRepo(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	memoryGit(t, root, "init", "-q")
	memoryWrite(t, filepath.Join(root, "input/value"), "bad\n")
	memoryWrite(t, filepath.Join(root, "outside"), "one\n")
	memoryGit(t, root, "add", ".")
	memoryGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")
	return root, memoryGit(t, root, "rev-parse", "HEAD^{tree}")
}
func memoryPlan(t *testing.T, tree string, checks ...PlanCheck) record.Envelope {
	t.Helper()
	return planEnvelope(t, PlanPayload{PlanID: "memory-plan", BaseSHA: "base", TreeSHA: tree, RegistryDigest: "registry", Checks: checks})
}
func memoryRun(t *testing.T, root, tree string, check PlanCheck) (VerdictCheck, error) {
	t.Helper()
	envelope, err := Run(memoryPlan(t, tree, check), root, t.TempDir())
	var verdict VerdictPayload
	if decodeErr := json.Unmarshal(envelope.Payload, &verdict); decodeErr != nil {
		t.Fatalf("verdict: %v (run: %v)", decodeErr, err)
	}
	if len(verdict.Checks) != 1 {
		t.Fatalf("checks: %+v", verdict)
	}
	return verdict.Checks[0], err
}
func memoryCheck(marker string) PlanCheck {
	return PlanCheck{CheckID: "tree-check", Scope: "tree", Reads: []string{"input/"}, Command: []string{"sh", "-c", "echo ran >> '" + marker + "'"}}
}
func memoryStore(t *testing.T, root string) resultStore {
	t.Helper()
	store, err := openResultStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
func memoryFiles(t *testing.T, store resultStore) []os.DirEntry {
	t.Helper()
	files, err := os.ReadDir(store.dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestMemoryFingerprintInputs(t *testing.T) {
	root, tree := memoryRepo(t)
	check := memoryCheck(filepath.Join(t.TempDir(), "marker"))
	check.DeclarationDigest = "declaration-one"
	tool := filepath.Join(t.TempDir(), "tool")
	memoryWrite(t, tool, "#!/bin/sh\necho v1\n")
	if err := os.Chmod(tool, 0755); err != nil {
		t.Fatal(err)
	}
	check.Tools = []string{tool}
	fingerprint := func(tree string, c PlanCheck) string {
		inputs, err := readTreeInputs(root, tree)
		if err != nil {
			t.Fatal(err)
		}
		key, err := treeFingerprint(c, inputs, root)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	first := fingerprint(tree, check)
	memoryWrite(t, filepath.Join(root, "outside"), "two\n")
	memoryGit(t, root, "add", ".")
	outsideTree := memoryGit(t, root, "write-tree")
	if fingerprint(outsideTree, check) != first {
		t.Fatal("outside reads changed fingerprint")
	}
	whole := check
	whole.Reads = nil
	if fingerprint(tree, whole) == fingerprint(outsideTree, whole) {
		t.Fatal("omitted reads did not cover whole tree")
	}
	memoryWrite(t, filepath.Join(root, "input/value"), "good\n")
	memoryGit(t, root, "add", ".")
	if fingerprint(memoryGit(t, root, "write-tree"), check) == first {
		t.Fatal("input change did not change fingerprint")
	}
	changed := check
	changed.DeclarationDigest = "declaration-two"
	if fingerprint(tree, changed) == first {
		t.Fatal("declaration change did not change fingerprint")
	}
	memoryWrite(t, tool, "#!/bin/sh\necho v2\n")
	if fingerprint(tree, check) == first {
		t.Fatal("tool version change did not change fingerprint")
	}
}

func TestMemoryPassReuseColdAndOptOut(t *testing.T) {
	root, tree := memoryRepo(t)
	marker := filepath.Join(t.TempDir(), "marker")
	check := memoryCheck(marker)
	first, err := memoryRun(t, root, tree, check)
	if err != nil || first.Result != ResultPass || first.Memory != "passed" || first.PassTime == "" {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := memoryRun(t, root, tree, check)
	if err != nil || second.Memory != "remembered" || second.PassTime != first.PassTime {
		t.Fatalf("second: %+v %v", second, err)
	}
	raw, _ := os.ReadFile(marker)
	if string(raw) != "ran\n" {
		t.Fatalf("remembered check ran: %q", raw)
	}
	store := memoryStore(t, root)
	if files := memoryFiles(t, store); len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".pass.json") {
		t.Fatalf("store files: %v", files)
	}
	if err := os.RemoveAll(store.dir); err != nil {
		t.Fatal(err)
	}
	cold, err := memoryRun(t, root, tree, check)
	if err != nil || cold.Memory != "passed" || cold.Result != first.Result {
		t.Fatalf("cold: %+v %v", cold, err)
	}
	no := false
	check.Remember = &no
	for i := 0; i < 2; i++ {
		got, err := memoryRun(t, root, tree, check)
		if err != nil || got.Memory == "remembered" {
			t.Fatalf("opt-out: %+v %v", got, err)
		}
	}
	raw, _ = os.ReadFile(marker)
	if string(raw) != "ran\nran\nran\nran\n" {
		t.Fatalf("execution count: %q", raw)
	}
}

func TestMemoryFailRetriesAndFlakyPass(t *testing.T) {
	root, tree := memoryRepo(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	switchFile := filepath.Join(dir, "pass")
	check := memoryCheck(marker)
	check.Command[2] += "; test -f '" + switchFile + "'"
	for i := 0; i < 2; i++ {
		got, err := memoryRun(t, root, tree, check)
		if err == nil || got.Result != ResultFail {
			t.Fatalf("fail: %+v %v", got, err)
		}
	}
	store := memoryStore(t, root)
	files := memoryFiles(t, store)
	if len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".fail.json") {
		t.Fatalf("fail files: %v", files)
	}
	failBefore, err := os.ReadFile(filepath.Join(store.dir, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	memoryWrite(t, switchFile, "pass")
	passed, err := memoryRun(t, root, tree, check)
	if err != nil || passed.Memory != "flaky" || passed.FailTime == "" || passed.PassTime == "" {
		t.Fatalf("flaky: %+v %v", passed, err)
	}
	failAfter, _ := os.ReadFile(filepath.Join(store.dir, files[0].Name()))
	if string(failAfter) != string(failBefore) {
		t.Fatal("pass modified fail file")
	}
	remembered, err := memoryRun(t, root, tree, check)
	if err != nil || remembered.Memory != "flaky" || remembered.PassTime != passed.PassTime {
		t.Fatalf("remembered flaky: %+v %v", remembered, err)
	}
	raw, _ := os.ReadFile(marker)
	if string(raw) != "ran\nran\nran\n" {
		t.Fatalf("fail retry/pass reuse: %q", raw)
	}
}

func TestMemoryOppositeWritersNeverErase(t *testing.T) {
	for _, failFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "fail-before-pass", false: "pass-before-fail"}[failFirst], func(t *testing.T) {
			root, tree := memoryRepo(t)
			marker := filepath.Join(t.TempDir(), "marker")
			check := memoryCheck(marker)
			inputs, err := readTreeInputs(root, tree)
			if err != nil {
				t.Fatal(err)
			}
			key, err := treeFingerprint(check, inputs, root)
			if err != nil {
				t.Fatal(err)
			}
			store := memoryStore(t, root)
			if _, _, err := store.read(key); err != nil {
				t.Fatal(err)
			}
			pass := storedResult{CheckID: check.CheckID, Result: ResultPass, Time: "2026-11-16T01:02:03Z", Tree: tree}
			fail := storedResult{CheckID: check.CheckID, Result: ResultFail, Time: "2026-11-16T01:02:02Z", Tree: tree}
			writes := []storedResult{pass, fail}
			if failFirst {
				writes = []storedResult{fail, pass}
			}
			for _, result := range writes {
				if err := store.publish(key, result); err != nil {
					t.Fatal(err)
				}
			}
			gotPass, gotFail, err := store.read(key)
			if err != nil || gotPass == nil || gotFail == nil {
				t.Fatalf("both records: %v %v %v", gotPass, gotFail, err)
			}
			got, err := memoryRun(t, root, tree, check)
			if err != nil || got.Memory != "flaky" || got.PassTime != pass.Time || got.FailTime != fail.Time {
				t.Fatalf("flaky: %+v %v", got, err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("remembered flaky check executed")
			}
		})
	}
}

func TestMemoryConcurrentWorktreesPublishOnePass(t *testing.T) {
	root, tree := memoryRepo(t)
	second := filepath.Join(t.TempDir(), "worktree")
	memoryGit(t, root, "worktree", "add", "--detach", second, "HEAD")
	check := memoryCheck(filepath.Join(t.TempDir(), "marker"))
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, worktree := range []string{root, second} {
		logs := t.TempDir()
		wg.Add(1)
		go func(worktree string) {
			defer wg.Done()
			<-start
			_, err := Run(memoryPlan(t, tree, check), worktree, logs)
			errs <- err
		}(worktree)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	store := memoryStore(t, root)
	files := memoryFiles(t, store)
	if len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".pass.json") {
		t.Fatalf("files: %v", files)
	}
	key := strings.TrimSuffix(files[0].Name(), ".pass.json")
	pass, fail, err := store.read(key)
	if err != nil || pass == nil || fail != nil {
		t.Fatalf("records: %v %v %v", pass, fail, err)
	}
	if memoryStore(t, second).dir != store.dir {
		t.Fatal("worktrees do not share store")
	}
}

func TestMemoryUnboundDirtyTrackedColdAndUntracked(t *testing.T) {
	root, tree := memoryRepo(t)
	marker := filepath.Join(t.TempDir(), "marker")
	check := memoryCheck(marker)
	check.Command[2] += "; test \"$(cat input/value)\" = good"
	memoryWrite(t, filepath.Join(root, "input/value"), "good\n")
	got, err := memoryRun(t, root, tree, check)
	if err != nil || got.Result != ResultPass || got.Memory != "not remembered" || got.UnboundPath != "input/value" {
		t.Fatalf("dirty: %+v %v", got, err)
	}
	store := memoryStore(t, root)
	if len(memoryFiles(t, store)) != 0 {
		t.Fatal("dirty pass published")
	}
	memoryGit(t, root, "restore", "input/value")
	got, err = memoryRun(t, root, tree, check)
	if err == nil || got.Result != ResultFail || got.Memory == "remembered" {
		t.Fatalf("restored: %+v %v", got, err)
	}
	if err := os.RemoveAll(store.dir); err != nil {
		t.Fatal(err)
	}
	cold, err := memoryRun(t, root, tree, check)
	if err == nil || cold.Result != got.Result {
		t.Fatalf("cold: %+v %v", cold, err)
	}
	memoryWrite(t, filepath.Join(root, "input/new"), "new")
	untracked, err := memoryRun(t, root, tree, check)
	if err == nil || untracked.Memory != "not remembered" || untracked.UnboundPath != "input/new" {
		t.Fatalf("untracked: %+v %v", untracked, err)
	}
}

func TestMemoryOutsideDirtyStillRememberedAndPostCheckDirtyNotStored(t *testing.T) {
	root, tree := memoryRepo(t)
	check := memoryCheck(filepath.Join(t.TempDir(), "marker"))
	if _, err := memoryRun(t, root, tree, check); err != nil {
		t.Fatal(err)
	}
	memoryWrite(t, filepath.Join(root, "outside"), "dirty")
	got, err := memoryRun(t, root, tree, check)
	if err != nil || got.Memory != "remembered" {
		t.Fatalf("outside: %+v %v", got, err)
	}
	check.Command[2] += "; echo good > input/value"
	got, err = memoryRun(t, root, tree, check)
	if err != nil || got.Memory != "not remembered" || got.UnboundPath != "input/value" {
		t.Fatalf("postcheck: %+v %v", got, err)
	}
	if len(memoryFiles(t, memoryStore(t, root))) != 1 {
		t.Fatal("check's changed inputs published a second pass")
	}
}

func TestMemoryDifferentHEADAndRenameAreUnbound(t *testing.T) {
	root, tree := memoryRepo(t)
	check := memoryCheck(filepath.Join(t.TempDir(), "marker"))
	memoryGit(t, root, "mv", "input/value", "outside-renamed")
	got, err := memoryRun(t, root, tree, check)
	if err != nil || got.Memory != "not remembered" || got.UnboundPath != "input/value" {
		t.Fatalf("rename: %+v %v", got, err)
	}
	memoryGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "rename")
	got, err = memoryRun(t, root, tree, check)
	if err != nil || got.Memory != "not remembered" || got.UnboundPath != "HEAD" {
		t.Fatalf("head: %+v %v", got, err)
	}
	if len(memoryFiles(t, memoryStore(t, root))) != 0 {
		t.Fatal("unbound result published")
	}
}

// Whitespace is part of a declaration's bytes even when its decoded fields are
// unchanged; another check's declaration must not invalidate this check's pass.
func TestMemoryDeclarationBytesSurvivePlanning(t *testing.T) {
	root, tree := memoryRepo(t)
	declarations := t.TempDir()
	raw := `{"id":"tree-check","label":"tree check","scope":"tree","tier":"fast","budget_ms":100,"reads":["input/"],"command":["true"]}`
	path := filepath.Join(declarations, "check.json")
	memoryWrite(t, path, raw)
	keyForPlan := func() string {
		registry, err := LoadRegistry(declarations)
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := Plan(registry, Change{BaseSHA: "base", TreeSHA: tree, ChangedPaths: []string{"input/value"}}, root)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := planPayload(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Checks) != 1 || plan.Checks[0].DeclarationDigest == "" {
			t.Fatalf("declaration not carried: %+v", plan)
		}
		inputs, err := readTreeInputs(root, tree)
		if err != nil {
			t.Fatal(err)
		}
		key, err := treeFingerprint(plan.Checks[0], inputs, root)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	first := keyForPlan()
	memoryWrite(t, path, raw+"\n")
	if first == keyForPlan() {
		t.Fatal("byte-only declaration change was lost")
	}
	memoryWrite(t, path, raw)
	memoryWrite(t, filepath.Join(declarations, "other.json"), strings.ReplaceAll(strings.ReplaceAll(raw, "tree-check", "other-check"), "input/", "other/"))
	if first != keyForPlan() {
		t.Fatal("another declaration changed the fingerprint")
	}
}

func TestMemoryRunFaceReportsRememberedTime(t *testing.T) {
	root, tree := memoryRepo(t)
	marker := filepath.Join(t.TempDir(), "marker")
	envelope := memoryPlan(t, tree, memoryCheck(marker))
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	output := filepath.Join(dir, "verdict.json")
	memoryWrite(t, planPath, string(raw))
	report, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = report
	defer func() { os.Stderr = original; report.Close() }()
	args := []string{"--input", planPath, "--worktree", root, "--output", output, "--logs", filepath.Join(dir, "logs")}
	for i := 0; i < 2; i++ {
		if code := runFace(args); code != 0 {
			t.Fatalf("run face exit %d", code)
		}
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var verdict struct {
		Payload VerdictPayload `json:"payload"`
	}
	if err := json.Unmarshal(body, &verdict); err != nil {
		t.Fatal(err)
	}
	check := verdict.Payload.Checks[0]
	if check.Memory != "remembered" || check.PassTime == "" {
		t.Fatalf("remembered record: %+v", check)
	}
	logged, err := os.ReadFile(report.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "remembered pass at "+check.PassTime) {
		t.Fatalf("report: %s", logged)
	}
	executed, _ := os.ReadFile(marker)
	if string(executed) != "ran\n" {
		t.Fatalf("run face executed: %q", executed)
	}
}

func TestMemoryGoToolUsesSupportedVersionCommand(t *testing.T) {
	root, tree := memoryRepo(t)
	marker := filepath.Join(t.TempDir(), "marker")
	check := memoryCheck(marker)
	check.Tools = []string{"go"}
	check.Command[2] += "; go version"
	first, err := memoryRun(t, root, tree, check)
	if err != nil || first.Result != ResultPass || first.Fingerprint == "" {
		t.Fatalf("Go check: %+v %v", first, err)
	}
	second, err := memoryRun(t, root, tree, check)
	if err != nil || second.Memory != "remembered" || second.Fingerprint != first.Fingerprint {
		t.Fatalf("Go reuse: %+v %v", second, err)
	}
	executed, _ := os.ReadFile(marker)
	if string(executed) != "ran\n" {
		t.Fatalf("remembered Go check ran: %q", executed)
	}
}

func TestMemoryShToolWithoutVersionFlag(t *testing.T) {
	root, tree := memoryRepo(t)
	shDir := t.TempDir()
	shBin := filepath.Join(shDir, "sh")
	realSh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("LookPath sh: %v", err)
	}
	script := "#!/bin/sh\nif [ \"$#\" -eq 1 ] && [ \"$1\" = \"--version\" ]; then\n  echo \"sh: 0: Illegal option --\" >&2\n  exit 2\nfi\nexec " + realSh + " \"$@\"\n"
	if err := os.WriteFile(shBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	marker := filepath.Join(t.TempDir(), "marker")
	check := memoryCheck(marker)
	check.Tools = []string{"sh"}
	first, err := memoryRun(t, root, tree, check)
	if err != nil || first.Result != ResultPass || first.Fingerprint == "" {
		t.Fatalf("sh check: %+v %v", first, err)
	}
	second, err := memoryRun(t, root, tree, check)
	if err != nil || second.Memory != "remembered" || second.Fingerprint != first.Fingerprint {
		t.Fatalf("sh reuse: %+v %v", second, err)
	}
	executed, _ := os.ReadFile(marker)
	if string(executed) != "ran\n" {
		t.Fatalf("remembered sh check ran: %q", executed)
	}
}

func TestMemoryMissingVersionAndCorruptStoreAreErrors(t *testing.T) {
	root, tree := memoryRepo(t)
	marker := filepath.Join(t.TempDir(), "marker")
	check := memoryCheck(marker)
	check.Tools = []string{filepath.Join(t.TempDir(), "missing-tool")}
	got, err := memoryRun(t, root, tree, check)
	if err == nil || got.Result != ResultError || !strings.Contains(err.Error(), "version of tool") {
		t.Fatalf("missing tool: %+v %v", got, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("check executed without a tool identity")
	}
	check.Tools = nil
	got, err = memoryRun(t, root, tree, check)
	if err != nil {
		t.Fatal(err)
	}
	store := memoryStore(t, root)
	memoryWrite(t, filepath.Join(store.dir, got.Fingerprint+".pass.json"), "invalid")
	got, err = memoryRun(t, root, tree, check)
	if err == nil || got.Result != ResultError {
		t.Fatalf("corrupt store silently ignored: %+v %v", got, err)
	}
}

func TestMemoryWholeTreeDirtyAndChangeCommandsNeverReuse(t *testing.T) {
	root, tree := memoryRepo(t)
	marker := filepath.Join(t.TempDir(), "marker")
	check := memoryCheck(marker)
	check.Reads = nil
	memoryWrite(t, filepath.Join(root, "outside"), "dirty")
	got, err := memoryRun(t, root, tree, check)
	if err != nil || got.Memory != "not remembered" || got.UnboundPath != "outside" {
		t.Fatalf("whole-tree: %+v %v", got, err)
	}
	memoryGit(t, root, "restore", "outside")
	check.Scope = "change"
	for i := 0; i < 2; i++ {
		got, err = memoryRun(t, root, tree, check)
		if err != nil || got.Memory != "" {
			t.Fatalf("change: %+v %v", got, err)
		}
	}
	if len(memoryFiles(t, memoryStore(t, root))) != 0 {
		t.Fatal("change check stored a result")
	}
	executed, _ := os.ReadFile(marker)
	if string(executed) != "ran\nran\nran\n" {
		t.Fatalf("executions: %q", executed)
	}
}

func memoryRunInLogs(t *testing.T, root, tree, logs string, check PlanCheck) (VerdictCheck, error) {
	t.Helper()
	envelope, err := Run(memoryPlan(t, tree, check), root, logs)
	var verdict VerdictPayload
	if decodeErr := json.Unmarshal(envelope.Payload, &verdict); decodeErr != nil {
		t.Fatalf("verdict: %v (run: %v)", decodeErr, err)
	}
	if len(verdict.Checks) != 1 {
		t.Fatalf("checks: %+v", verdict)
	}
	return verdict.Checks[0], err
}

func TestMemoryRerunAfterFailKeepsFailingLogRef(t *testing.T) {
	t.Run("fail-then-pass", func(t *testing.T) {
		root, tree := memoryRepo(t)
		logs := filepath.Join(t.TempDir(), "logs")
		switchFile := filepath.Join(t.TempDir(), "pass")
		check := PlanCheck{
			CheckID: "go-test-shards-test",
			Scope:   "tree",
			Reads:   []string{"input/"},
			Command: []string{
				"sh", "-c",
				"if [ -f '" + switchFile + "' ]; then echo 'Ran 40 tests in 37.514s OK'; exit 0; fi; echo 'FAIL: TestShardTimeout'; exit 1",
			},
		}
		first, err := memoryRunInLogs(t, root, tree, logs, check)
		if err == nil || first.Result != ResultFail {
			t.Fatalf("first run: %+v %v", first, err)
		}
		if want := filepath.Join(logs, "00-go-test-shards-test.log"); first.LogRef != want {
			t.Fatalf("first log_ref = %q, want %q", first.LogRef, want)
		}
		store := memoryStore(t, root)
		passRec, failRec, err := store.read(first.Fingerprint)
		if err != nil || passRec != nil || failRec == nil || failRec.LogRef != first.LogRef {
			t.Fatalf("stored after fail: pass=%+v fail=%+v err=%v", passRec, failRec, err)
		}

		memoryWrite(t, switchFile, "pass")
		second, err := memoryRunInLogs(t, root, tree, logs, check)
		if err != nil || second.Result != ResultPass || second.Memory != "flaky" {
			t.Fatalf("second run: %+v %v", second, err)
		}
		if second.LogRef == first.LogRef {
			t.Fatalf("rerun reused failed run's log file %q", second.LogRef)
		}
		if want := filepath.Join(logs, "00-go-test-shards-test.attempt-2.log"); second.LogRef != want {
			t.Fatalf("second log_ref = %q, want %q", second.LogRef, want)
		}

		passRec, failRec, err = store.read(first.Fingerprint)
		if err != nil || passRec == nil || failRec == nil {
			t.Fatalf("stored after rerun: pass=%+v fail=%+v err=%v", passRec, failRec, err)
		}
		if failRec.LogRef != first.LogRef || passRec.LogRef != second.LogRef || passRec.LogRef == failRec.LogRef {
			t.Fatalf("stored log_refs: fail=%q pass=%q", failRec.LogRef, passRec.LogRef)
		}
		failBody, err := os.ReadFile(failRec.LogRef)
		if err != nil || !strings.Contains(string(failBody), "FAIL: TestShardTimeout\n") || strings.Contains(string(failBody), "Ran 40 tests") {
			t.Fatalf("fail log_ref %q = %q, err = %v", failRec.LogRef, failBody, err)
		}
		passBody, err := os.ReadFile(passRec.LogRef)
		if err != nil || !strings.Contains(string(passBody), "Ran 40 tests in 37.514s OK\n") || strings.Contains(string(passBody), "FAIL: TestShardTimeout") {
			t.Fatalf("pass log_ref %q = %q, err = %v", passRec.LogRef, passBody, err)
		}
	})

	t.Run("fail-then-fail-then-pass", func(t *testing.T) {
		root, tree := memoryRepo(t)
		logs := filepath.Join(t.TempDir(), "logs")
		countFile := filepath.Join(t.TempDir(), "count")
		check := PlanCheck{
			CheckID: "tree-check",
			Scope:   "tree",
			Reads:   []string{"input/"},
			Command: []string{
				"sh", "-c",
				"n=0; [ -f '" + countFile + "' ] && n=$(cat '" + countFile + "'); n=$((n + 1)); printf '%s' \"$n\" > '" + countFile + "'; " +
					"if [ \"$n\" -lt 3 ]; then echo \"fail-attempt-$n\"; exit 1; fi; echo \"pass-attempt-$n\"; exit 0",
			},
		}
		r1, err := memoryRunInLogs(t, root, tree, logs, check)
		if err == nil || r1.Result != ResultFail {
			t.Fatalf("attempt 1: %+v %v", r1, err)
		}
		r2, err := memoryRunInLogs(t, root, tree, logs, check)
		if err == nil || r2.Result != ResultFail {
			t.Fatalf("attempt 2: %+v %v", r2, err)
		}
		r3, err := memoryRunInLogs(t, root, tree, logs, check)
		if err != nil || r3.Result != ResultPass || r3.Memory != "flaky" {
			t.Fatalf("attempt 3: %+v %v", r3, err)
		}
		if r1.LogRef == r2.LogRef || r2.LogRef == r3.LogRef || r1.LogRef == r3.LogRef {
			t.Fatalf("attempts shared log_ref: %q, %q, %q", r1.LogRef, r2.LogRef, r3.LogRef)
		}
		store := memoryStore(t, root)
		passRec, failRec, err := store.read(r1.Fingerprint)
		if err != nil || passRec == nil || failRec == nil {
			t.Fatalf("stored records: pass=%+v fail=%+v err=%v", passRec, failRec, err)
		}
		if failRec.LogRef != r2.LogRef || passRec.LogRef != r3.LogRef {
			t.Fatalf("stored log_refs: fail=%q (want %q) pass=%q (want %q)", failRec.LogRef, r2.LogRef, passRec.LogRef, r3.LogRef)
		}
		for _, want := range []struct {
			path    string
			present string
			absent  []string
		}{
			{r1.LogRef, "fail-attempt-1\n", []string{"fail-attempt-2", "pass-attempt-3"}},
			{failRec.LogRef, "fail-attempt-2\n", []string{"fail-attempt-1", "pass-attempt-3"}},
			{passRec.LogRef, "pass-attempt-3\n", []string{"fail-attempt-1", "fail-attempt-2"}},
		} {
			got, err := os.ReadFile(want.path)
			if err != nil || !strings.Contains(string(got), want.present) {
				t.Fatalf("log %q = %q, missing %q (err=%v)", want.path, got, want.present, err)
			}
			for _, bad := range want.absent {
				if strings.Contains(string(got), bad) {
					t.Fatalf("log %q = %q, unexpectedly contains %q", want.path, got, bad)
				}
			}
		}
	})
}

func TestCoversPathExclusions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reads []string
		path  string
		want  bool
	}{
		{"included file", []string{"tools/tillr/", "!tools/tillr/**/*.md"}, "tools/tillr/main.go", true},
		{"excluded top md", []string{"tools/tillr/", "!tools/tillr/**/*.md"}, "tools/tillr/README.md", false},
		{"excluded nested md", []string{"tools/tillr/", "!tools/tillr/**/*.md"}, "tools/tillr/docs/guide.md", false},
		{"excluded single-level md", []string{"tools/tillr/", "!tools/tillr/*.md"}, "tools/tillr/README.md", false},
		{"included nested md when single-level excluded", []string{"tools/tillr/", "!tools/tillr/*.md"}, "tools/tillr/docs/guide.md", true},
		{"excluded exact file", []string{"tools/tillr/", "!tools/tillr/README.md"}, "tools/tillr/README.md", false},
		{"excluded subdir", []string{"tools/tillr/", "!tools/tillr/docs/"}, "tools/tillr/docs/guide.md", false},
		{"whole tree with exclusion", []string{".", "!docs/"}, "docs/guide.md", false},
		{"whole tree non-excluded", []string{".", "!docs/"}, "tools/tillr/main.go", true},
		{"exclusion before inclusion", []string{"!tools/tillr/**/*.md", "tools/tillr/"}, "tools/tillr/README.md", false},
		{"exclusion only matches nothing", []string{"!tools/tillr/README.md"}, "tools/tillr/main.go", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := coversPath(tc.reads, tc.path); got != tc.want {
				t.Fatalf("coversPath(%v, %q) = %t, want %t", tc.reads, tc.path, got, tc.want)
			}
		})
	}
}

func TestMemoryExclusionFingerprintAndReuse(t *testing.T) {
	root, tree1 := memoryRepo(t)
	memoryWrite(t, filepath.Join(root, "input/README.md"), "# One\n")
	memoryWrite(t, filepath.Join(root, "input/docs/guide.md"), "# Guide\n")
	memoryGit(t, root, "add", ".")
	memoryGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "with docs")
	tree1 = memoryGit(t, root, "rev-parse", "HEAD^{tree}")

	marker := filepath.Join(t.TempDir(), "marker")
	check := PlanCheck{
		CheckID: "tree-check",
		Scope:   "tree",
		Reads:   []string{"input/", "!input/**/*.md"},
		Command: []string{"sh", "-c", "echo ran >> '" + marker + "'"},
	}

	first, err := memoryRun(t, root, tree1, check)
	if err != nil || first.Result != ResultPass || first.Memory != "passed" {
		t.Fatalf("first: %+v %v", first, err)
	}

	// Changing only excluded Markdown files keeps the same fingerprint and reuses the pass.
	memoryWrite(t, filepath.Join(root, "input/README.md"), "# Two\n")
	memoryWrite(t, filepath.Join(root, "input/docs/guide.md"), "# Guide Two\n")
	memoryGit(t, root, "add", ".")
	tree2 := memoryGit(t, root, "write-tree")
	memoryGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "docs only")
	tree2 = memoryGit(t, root, "rev-parse", "HEAD^{tree}")

	// Even a dirty excluded file in the worktree does not make the check unbound.
	memoryWrite(t, filepath.Join(root, "input/README.md"), "# Dirty\n")

	second, err := memoryRun(t, root, tree2, check)
	if err != nil || second.Result != ResultPass || second.Memory != "remembered" || second.PassTime != first.PassTime {
		t.Fatalf("second: %+v %v", second, err)
	}
	raw, _ := os.ReadFile(marker)
	if string(raw) != "ran\n" {
		t.Fatalf("remembered check ran again: %q", raw)
	}
}
