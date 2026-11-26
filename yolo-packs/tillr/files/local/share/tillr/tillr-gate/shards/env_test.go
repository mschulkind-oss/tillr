package shards

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAbsentGlobalConfigIsValid(t *testing.T) {
	root := requireTempDir(t)
	binary := writeFakeScript(t, root, "fake-ok", "#!/usr/bin/env python3",
		"print('=== RUN   TestWorks'); print('--- PASS: TestWorks (0.00s)'); print('PASS')\n")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "absent"))
	events, err := RunShard("fixture", root, binary, []string{"TestWorks"}, 0, filepath.Join(root, "scratch"), "3600s", nil, RunOptions{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Test == "TestWorks" && ev.Action == "pass" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected TestWorks pass event, got %+v", events)
	}
}

func TestMalformedGlobalConfigRemainsAnError(t *testing.T) {
	root := requireTempDir(t)
	cfg := filepath.Join(root, "invalid")
	if err := os.WriteFile(cfg, []byte("[unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	if _, err := GlobalGitConfig(nil); err == nil {
		t.Fatal("expected error for malformed global git config")
	}
}

func TestUnknownTestAndIsolation(t *testing.T) {
	root := requireTempDir(t)
	mod := filepath.Join(root, "module")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fixture\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package fixture
import ("testing"; "os"; "time"; "strings")
func check(t *testing.T) {
	if os.Getenv("YOLO_DURABLE_DIR") != os.Getenv("TMPDIR") { t.Fatal("durable scratch is not isolated") }
	if e := os.Chdir(os.Getenv("TMPDIR")); e != nil { t.Fatal(e) }
	p := "same-name"
	if _, e := os.Stat(p); !os.IsNotExist(e) { t.Fatal("another shard's file", e) }
	if e := os.WriteFile(p, []byte(t.Name()), 0600); e != nil { t.Fatal(e) }
	g := os.Getenv("GIT_CONFIG_GLOBAL")
	if b, e := os.ReadFile(g); e != nil || !strings.Contains(string(b), "untouched") || strings.Contains(string(b), "[include]") { t.Fatal("inherited config lost or shared", e) }
	if e := os.WriteFile(g, []byte(t.Name()), 0600); e != nil { t.Fatal(e) }
	time.Sleep(50*time.Millisecond)
}
func TestKnown(t *testing.T) { check(t) }
func TestNew(t *testing.T) { check(t) }
`
	if err := os.WriteFile(filepath.Join(mod, "fixture_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	durations := filepath.Join(root, "durations.json")
	if err := os.WriteFile(durations, []byte(`{"fixture": {"TestKnown": 1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(root, "events.jsonl")
	globalConfig := filepath.Join(root, "inherited.gitconfig")
	included := filepath.Join(root, "included.gitconfig")
	if err := os.WriteFile(included, []byte("[fixture]\nseed = untouched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inherited := "[include]\npath = included.gitconfig\n"
	if err := os.WriteFile(globalConfig, []byte(inherited), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLIInSubprocess(t, []string{
		mod, "-k", "2", "--durations", durations, "--events", report,
	}, map[string]string{"GIT_CONFIG_GLOBAL": globalConfig}, "")
	if got, err := os.ReadFile(globalConfig); err != nil || string(got) != inherited {
		t.Fatalf("inherited gitconfig modified: %q (err=%v)", string(got), err)
	}
	if got, err := os.ReadFile(included); err != nil || string(got) != "[fixture]\nseed = untouched\n" {
		t.Fatalf("included gitconfig modified: %q (err=%v)", string(got), err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	passed := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var ev TestEvent
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Action == "pass" && ev.Test != "" {
			passed[ev.Test] = true
		}
	}
	want := map[string]bool{"TestKnown": true, "TestNew": true}
	if !reflect.DeepEqual(passed, want) {
		t.Fatalf("passed=%v, want %v", passed, want)
	}
}

func TestDiscoveryUsesPrivateScratchAndGitConfig(t *testing.T) {
	root := requireTempDir(t)
	mod := filepath.Join(root, "module")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fixture\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package fixture
import ("testing"; "os"; "strings")
func TestWorks(t *testing.T) {}
func TestMain(m *testing.M) {
	scratch := os.Getenv("TMPDIR")
	config := os.Getenv("GIT_CONFIG_GLOBAL")
	if !strings.Contains(scratch, "go-shards-") || !strings.HasPrefix(config, scratch+"/") {
		os.Stderr.WriteString("TestMain needs private discovery scratch and config\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
}
`
	if err := os.WriteFile(filepath.Join(mod, "fixture_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	durations := filepath.Join(root, "durations.json")
	if err := os.WriteFile(durations, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLIInSubprocess(t, []string{mod, "--durations", durations}, nil, "")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
}

func TestIsolatedRunsBypassBlockGitShimAndDisableAutoMaintenance(t *testing.T) {
	root := requireTempDir(t)
	var realGit string
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == "" {
			continue
		}
		cand := filepath.Join(entry, "git")
		if !isExecutableFile(cand) {
			continue
		}
		dir := filepath.Dir(cand)
		if filepath.Base(dir) == "block" &&
			filepath.Base(filepath.Dir(dir)) == "bin" &&
			filepath.Base(filepath.Dir(filepath.Dir(dir))) == ".yolo" {
			continue
		}
		f, err := os.Open(cand)
		if err != nil {
			continue
		}
		buf := make([]byte, 256)
		n, _ := f.Read(buf)
		_ = f.Close()
		head := buf[:n]
		if bytes.HasPrefix(head, []byte("#!")) &&
			(bytes.Contains(head, []byte("_yg_tool=git")) || bytes.Contains(head, []byte("# tillr-git-guard"))) {
			continue
		}
		realGit = cand
		break
	}
	if realGit == "" {
		t.Fatal("could not locate real git on PATH")
	}

	shimHits := filepath.Join(root, "shim-hits.log")
	fakeBlock := filepath.Join(root, ".yolo", "bin", "block")
	if err := os.MkdirAll(fakeBlock, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeBlockGit := writeFakeScript(t, fakeBlock, "git", "#!/bin/sh", fmt.Sprintf(`# A guard: runs the real git unless an argument names this workspace's .yolo.
if [ -z "$YOLO_BYPASS_SHIMS" ]; then
  _yg_tool=git
  printf "block-hit\n" >> %q
  exit 97
fi
exec %q "$@"`, shimHits, realGit))

	fakeGuardDir := filepath.Join(root, "git-guard")
	if err := os.MkdirAll(fakeGuardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGuardGit := writeFakeScript(t, fakeGuardDir, "git", "#!/bin/sh", fmt.Sprintf(`# tillr-git-guard
printf "guard-hit\n" >> %q
exit 98`, shimHits))

	fallbackEnv := BypassGitShimEnv(
		map[string]string{"PATH": strings.Join([]string{fakeGuardDir, fakeBlock}, string(os.PathListSeparator))},
		"/proc/unwritable-git-bin",
	)
	if fallbackEnv["YOLO_BYPASS_SHIMS"] != "1" {
		t.Fatalf("expected YOLO_BYPASS_SHIMS=1, got %q", fallbackEnv["YOLO_BYPASS_SHIMS"])
	}
	for _, entry := range filepath.SplitList(fallbackEnv["PATH"]) {
		if entry == fakeGuardDir {
			t.Fatalf("expected fakeGuardDir removed from fallback PATH: %s", fallbackEnv["PATH"])
		}
	}

	inheritedCfg := filepath.Join(root, "inherited.gitconfig")
	inheritedText := "[user]\n\tname = Shard Owner\n\temail = shard@example.invalid\n[maintenance]\n\tauto = true\n[gc]\n\tauto = 6700\n"
	if err := os.WriteFile(inheritedCfg, []byte(inheritedText), 0o644); err != nil {
		t.Fatal(err)
	}

	mod := filepath.Join(root, "gitshim-mod")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fixture\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`package fixture
import (
	"os"
	"os/exec"
	"strings"
	"testing"
)
func verifyGitEnv() string {
	resolved, err := exec.LookPath("git")
	if err != nil { return err.Error() }
	if resolved == %q || resolved == %q {
		return "git resolved to shell shim: " + resolved
	}
	for _, check := range [][2]string{
		{"maintenance.auto", "false"},
		{"gc.auto", "0"},
		{"user.name", "Shard Owner"},
	} {
		out, err := exec.Command("git", "config", "--global", "--get-all", check[0]).CombinedOutput()
		if err != nil { return check[0] + ": " + err.Error() + ": " + string(out) }
		if got := strings.TrimSpace(string(out)); got != check[1] {
			return check[0] + "=" + got + " want " + check[1]
		}
	}
	return ""
}
func TestMain(m *testing.M) {
	if problem := verifyGitEnv(); problem != "" {
		os.Stderr.WriteString("TestMain: " + problem + "\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
}
func checkRepo(t *testing.T) {
	if problem := verifyGitEnv(); problem != "" { t.Fatal(problem) }
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %%v: %%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-qm", "ok").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %%v: %%s", err, out)
	}
}
func TestAlpha(t *testing.T) { checkRepo(t) }
func TestBeta(t *testing.T) { checkRepo(t) }
`, fakeBlockGit, fakeGuardGit)
	if err := os.WriteFile(filepath.Join(mod, "fixture_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	durations := filepath.Join(root, "durations.json")
	if err := os.WriteFile(durations, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	events := filepath.Join(root, "events.jsonl")
	shimmedPath := strings.Join([]string{fakeGuardDir, fakeBlock, os.Getenv("PATH")}, string(os.PathListSeparator))

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "shards", mod, "-k", "2", "--durations", durations, "--events", events)
	envMap := EnvSliceToMap(os.Environ())
	envMap["PATH"] = shimmedPath
	envMap["GIT_CONFIG_GLOBAL"] = inheritedCfg
	delete(envMap, "YOLO_BYPASS_SHIMS")
	cmd.Env = EnvMapToSlice(envMap)
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("CLI failed: %v\n%s", err, outBuf.String())
	}
	if hits, herr := os.ReadFile(shimHits); herr == nil {
		t.Fatalf("shim was hit: %s", string(hits))
	}
	if got, err := os.ReadFile(inheritedCfg); err != nil || string(got) != inheritedText {
		t.Fatalf("inherited gitconfig modified: %q (err=%v)", string(got), err)
	}
	raw, err := os.ReadFile(events)
	if err != nil {
		t.Fatal(err)
	}
	passed := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var ev TestEvent
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Action == "pass" && ev.Test != "" {
			passed[ev.Test] = true
		}
	}
	want := map[string]bool{"TestAlpha": true, "TestBeta": true}
	if !reflect.DeepEqual(passed, want) {
		t.Fatalf("passed=%v, want %v", passed, want)
	}
}

func TestPrivateEnvironmentSetsGitCeilingDirectories(t *testing.T) {
	scratch := t.TempDir()
	existing := filepath.Join(t.TempDir(), "existing-ceiling")
	t.Setenv("GIT_CEILING_DIRECTORIES", existing)
	env, err := PrivateEnvironment("test", scratch, [][2]string{{"user.name", "Fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	got := filepath.SplitList(env["GIT_CEILING_DIRECTORIES"])
	if len(got) != 2 || got[0] != existing || got[1] != scratch {
		t.Fatalf("GIT_CEILING_DIRECTORIES = %q, want [%q, %q]", env["GIT_CEILING_DIRECTORIES"], existing, scratch)
	}
}
