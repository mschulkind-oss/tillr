package forkgate_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/forkgate"
)

var (
	binOnce     sync.Once
	binDir      string
	drainBin    string
	gateBin     string
	binBuildErr error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
	os.Exit(code)
}

func scratchDir(t *testing.T) string {
	t.Helper()
	if durable := os.Getenv("YOLO_DURABLE_DIR"); durable != "" {
		base := filepath.Join(durable, "fork-gate-test")
		if err := os.MkdirAll(base, 0o755); err != nil {
			t.Fatal(err)
		}
		dir, err := os.MkdirTemp(base, "case-*")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		return dir
	}
	return t.TempDir()
}

func ensureBinaries(t *testing.T) (string, string) {
	t.Helper()
	binOnce.Do(func() {
		base := os.Getenv("YOLO_DURABLE_DIR")
		if base != "" {
			if err := os.MkdirAll(base, 0o755); err != nil {
				binBuildErr = err
				return
			}
		}
		dir, err := os.MkdirTemp(base, "forkgate-bins-*")
		if err != nil {
			binBuildErr = err
			return
		}
		binDir = dir
		drainBin = filepath.Join(dir, "tillr-drain")
		buildDrain := exec.Command("go", "build", "-o", drainBin, "..")
		if out, err := buildDrain.CombinedOutput(); err != nil {
			binBuildErr = fmt.Errorf("building tillr-drain: %v\n%s", err, out)
			return
		}
		if envGate := os.Getenv("TILLR_GATE_BIN"); envGate != "" {
			gateBin = envGate
			return
		}
		gateBin = filepath.Join(dir, "tillr-gate")
		buildGate := exec.Command("go", "build", "-o", gateBin, ".")
		buildGate.Dir = filepath.Join("..", "..", "tillr-gate")
		if out, err := buildGate.CombinedOutput(); err != nil {
			binBuildErr = fmt.Errorf("building tillr-gate: %v\n%s", err, out)
			return
		}
	})
	if binBuildErr != nil {
		t.Fatal(binBuildErr)
	}
	return drainBin, gateBin
}

func writePack(t *testing.T, root, name string, contribution map[string]any) string {
	t.Helper()
	path := filepath.Join(root, "yolo-packs", name, "pack.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"contributes": []any{contribution}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- ForkBuildContracts (6 cases) ---

func TestJSONCPreservesQuotedCommentsAndRemovesTrailingCommas(t *testing.T) {
	root := scratchDir(t)
	path := filepath.Join(root, "pack.json")
	content := `{
  // comment
  "url": "https://example.invalid/", /* another comment */
  "literal": "/*not a comment*/,}",
  "array": [1, 2,],
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var got struct {
		URL     string `json:"url"`
		Literal string `json:"literal"`
		Array   []int  `json:"array"`
	}
	if err := forkgate.JSONC(path, &got); err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://example.invalid/" || got.Literal != "/*not a comment*/,}" || len(got.Array) != 2 || got.Array[0] != 1 || got.Array[1] != 2 {
		t.Fatalf("unexpected JSONC parse: %+v", got)
	}
}

func TestDeclaredCommandIsNotReplacedByAnNpmGuess(t *testing.T) {
	root := scratchDir(t)
	source := "git+https://example.invalid/repo?ref=main"
	path := writePack(t, root, "fork", map[string]any{
		"patch_branch": "git+https://example.invalid/repo?ref=patch%2Fpi",
		"source":       source,
		"build":        "node scripts/fork-build.mjs",
	})
	gotPath, gotCmd, gotSource, err := forkgate.BuildCommand(root, "pi", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != path || gotCmd != "node scripts/fork-build.mjs" || gotSource != source {
		t.Fatalf("got (%q, %q, %q), want (%q, %q, %q)", gotPath, gotCmd, gotSource, path, "node scripts/fork-build.mjs", source)
	}
}

func TestSourceOnlyDeliveryHasAnExplicitCheck(t *testing.T) {
	root := scratchDir(t)
	source := "git+https://example.invalid/repo?ref=main"
	path := writePack(t, root, "source", map[string]any{
		"patch_branch": "git+https://example.invalid/repo?ref=patch/pi",
		"source":       source,
	})
	found, command, foundSource, err := forkgate.BuildCommand(root, "pi", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if found != path {
		t.Fatalf("found = %q, want %q", found, path)
	}
	if command != "git diff --exit-code HEAD && git diff --cached --exit-code" {
		t.Fatalf("command = %q", command)
	}
	if foundSource != source {
		t.Fatalf("source = %q, want %q", foundSource, source)
	}
}

func TestNoDeliveryIsAnError(t *testing.T) {
	root := scratchDir(t)
	_, _, _, err := forkgate.BuildCommand(root, "pi", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "found 0") {
		t.Fatalf("err = %v, want 'found 0'", err)
	}
}

func TestAmbiguousDeliveryIsAnError(t *testing.T) {
	root := scratchDir(t)
	for _, name := range []string{"one", "two"} {
		writePack(t, root, name, map[string]any{
			"patch_branch": "git+https://example.invalid/repo?ref=patch/pi",
			"source":       "git+https://example.invalid/repo?ref=main",
		})
	}
	_, _, _, err := forkgate.BuildCommand(root, "pi", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "found 2") {
		t.Fatalf("err = %v, want 'found 2'", err)
	}
}

func TestExistingForkQualityScriptsPreserved(t *testing.T) {
	root := scratchDir(t)
	gotSubagents, err := forkgate.QualityCommand("pi-subagents", root, "HEAD", "HEAD", "/bin/tillr-drain", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	wantSubagents := "npm ci --include=dev --legacy-peer-deps --ignore-scripts --no-audit --no-fund && npm run typecheck && npm run test:unit"
	if gotSubagents != wantSubagents {
		t.Fatalf("pi-subagents = %q, want %q", gotSubagents, wantSubagents)
	}

	gotSearch, err := forkgate.QualityCommand("pi-session-search", root, "HEAD", "HEAD", "/bin/tillr-drain", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	wantSearch := "npm ci --include=dev --legacy-peer-deps --ignore-scripts --no-audit --no-fund && npm run check && npm run test"
	if gotSearch != wantSearch {
		t.Fatalf("pi-session-search = %q, want %q", gotSearch, wantSearch)
	}
}

// --- ForkQualityGateTests (18 cases) ---

type gateFixture struct {
	t        *testing.T
	tmp      string
	root     string
	fork     string
	base     string
	binDir   string
	npxLog   string
	drainBin string
	gateBin  string
}

func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	drainPath, gatePath := ensureBinaries(t)
	tmp := scratchDir(t)
	root := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &gateFixture{
		t:        t,
		tmp:      tmp,
		root:     root,
		fork:     filepath.Join(tmp, "fork"),
		binDir:   filepath.Join(tmp, "bin"),
		npxLog:   filepath.Join(tmp, "npx.log"),
		drainBin: drainPath,
		gateBin:  gatePath,
	}
	f.git(root, "init", "-b", "main")
	f.git(root, "config", "user.name", "Fixture User")
	f.git(root, "config", "user.email", "fixture@example.invalid")
	f.writeFile(filepath.Join(root, ".gitignore"), ".tillr/\n")
	f.git(root, "add", ".gitignore")
	f.git(root, "commit", "-m", "init")

	if err := os.MkdirAll(f.fork, 0o755); err != nil {
		t.Fatal(err)
	}
	f.git(f.fork, "init", "-b", "main")
	f.git(f.fork, "config", "user.name", "Fixture User")
	f.git(f.fork, "config", "user.email", "fixture@example.invalid")
	f.writeFile(filepath.Join(f.fork, "README.md"), "base\n")
	f.writeFile(filepath.Join(f.fork, "packages", "pkg-a", "index.ts"), "export const a = 1;\n")
	f.writeFile(filepath.Join(f.fork, "packages", "pkg-b", "index.ts"), "export const b = 1;\n")
	for name, script := range map[string]string{
		"pkg-a": "npx vitest --run --reporter=dot",
		"pkg-b": "npx vitest --run --untouched",
	} {
		pkgJSON, _ := json.Marshal(map[string]any{
			"name": name, "version": "1.0.0", "scripts": map[string]string{"test": script},
		})
		f.writeFile(filepath.Join(f.fork, "packages", name, "package.json"), string(pkgJSON))
	}
	f.git(f.fork, "add", ".")
	f.git(f.fork, "commit", "-m", "fork base")
	f.base = f.git(f.fork, "rev-parse", "HEAD")
	f.git(f.fork, "tag", "v1.0.0")
	f.git(f.fork, "update-ref", "refs/remotes/upstream/main", f.base)

	if err := os.MkdirAll(f.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *gateFixture) git(cwd string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *gateFixture) writeFile(path, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *gateFixture) writePack(forkName, buildCmd string) {
	f.t.Helper()
	writePack(f.t, f.root, forkName, map[string]any{
		"patch_branch": fmt.Sprintf("git+https://example.invalid/repo?ref=patch/%s", forkName),
		"source":       "git+https://example.invalid/repo?ref=main",
		"build":        buildCmd,
	})
}

func (f *gateFixture) writeNpxStub(script string) {
	f.t.Helper()
	npx := filepath.Join(f.binDir, "npx")
	body := fmt.Sprintf("#!/bin/sh\nset -eu\nNPX_LOG=%s\n%s", shellQuoteForTest(f.npxLog), script)
	if err := os.WriteFile(npx, []byte(body), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func shellQuoteForTest(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func (f *gateFixture) runForkGate(forkName, runID, head string, extraEnv map[string]string) (int, string, string) {
	f.t.Helper()
	cmd := exec.Command(f.drainBin,
		"fork-gate",
		"--item", "item-1", "--lane", "lane-1", "--run", runID,
		"--worktree", f.fork, "--base", f.base, "--head", head,
		"--root", f.root, "--fork", forkName,
		"--tillr-gate", f.gateBin,
	)
	cmd.Dir = f.root
	env := envWithOverrides(extraEnv, map[string]string{
		"PATH": f.binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
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
			f.t.Fatalf("running tillr-drain fork-gate: %v", err)
		}
	}
	return code, stdout.String(), stderr.String()
}

func envWithOverrides(extra map[string]string, baseOverrides map[string]string) []string {
	merged := make(map[string]string)
	for _, entry := range os.Environ() {
		if k, v, ok := strings.Cut(entry, "="); ok {
			merged[k] = v
		}
	}
	for k, v := range baseOverrides {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

func (f *gateFixture) commitOfflineDevcheckPackage(scripts map[string]string) string {
	f.t.Helper()
	vendor := filepath.Join(f.fork, "vendor")
	if err := os.MkdirAll(vendor, 0o755); err != nil {
		f.t.Fatal(err)
	}
	tgzPath := filepath.Join(vendor, "devcheck-1.0.0.tgz")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	pkgJSON, _ := json.Marshal(map[string]any{
		"name":    "devcheck",
		"version": "1.0.0",
		"bin":     map[string]string{"devcheck": "bin.js"},
	})
	if err := tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(pkgJSON))}); err != nil {
		f.t.Fatal(err)
	}
	if _, err := tw.Write(pkgJSON); err != nil {
		f.t.Fatal(err)
	}

	binJS := []byte("#!/usr/bin/env node\nconsole.log(\"devcheck:\" + process.argv.slice(2).join(\" \"));\nprocess.exit(process.argv.includes(\"--fail\") ? 1 : 0);\n")
	if err := tw.WriteHeader(&tar.Header{Name: "package/bin.js", Mode: 0o755, Size: int64(len(binJS))}); err != nil {
		f.t.Fatal(err)
	}
	if _, err := tw.Write(binJS); err != nil {
		f.t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		f.t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		f.t.Fatal(err)
	}

	tgzBytes := buf.Bytes()
	if err := os.WriteFile(tgzPath, tgzBytes, 0o644); err != nil {
		f.t.Fatal(err)
	}
	sum := sha512.Sum512(tgzBytes)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sum[:])

	rootPkg, _ := json.MarshalIndent(map[string]any{
		"name":            "fixture-fork",
		"version":         "1.0.0",
		"scripts":         scripts,
		"devDependencies": map[string]string{"devcheck": "file:vendor/devcheck-1.0.0.tgz"},
	}, "", "  ")
	f.writeFile(filepath.Join(f.fork, "package.json"), string(rootPkg))

	lockPkg, _ := json.MarshalIndent(map[string]any{
		"name":            "fixture-fork",
		"version":         "1.0.0",
		"lockfileVersion": 3,
		"requires":        true,
		"packages": map[string]any{
			"": map[string]any{
				"name":            "fixture-fork",
				"version":         "1.0.0",
				"devDependencies": map[string]string{"devcheck": "file:vendor/devcheck-1.0.0.tgz"},
			},
			"node_modules/devcheck": map[string]any{
				"version":   "1.0.0",
				"resolved":  "vendor/devcheck-1.0.0.tgz",
				"integrity": integrity,
				"dev":       true,
				"bin":       map[string]string{"devcheck": "bin.js"},
			},
		},
	}, "", "  ")
	f.writeFile(filepath.Join(f.fork, "package-lock.json"), string(lockPkg))

	f.git(f.fork, "add", "vendor/devcheck-1.0.0.tgz", "package.json", "package-lock.json")
	f.git(f.fork, "commit", "-m", "add offline devcheck package")
	return f.git(f.fork, "rev-parse", "HEAD")
}

func (f *gateFixture) addTuiTest(assertion string, script *string) {
	f.t.Helper()
	tui := filepath.Join(f.fork, "packages", "tui")
	scripts := map[string]string{}
	if script != nil {
		scripts["test"] = *script
	}
	pkgJSON, _ := json.Marshal(map[string]any{
		"name":    "tui",
		"version": "1.0.0",
		"scripts": scripts,
	})
	f.writeFile(filepath.Join(tui, "package.json"), string(pkgJSON))
	f.writeFile(filepath.Join(tui, "test", "fixture.test.ts"),
		"import { test } from \"node:test\";\n"+
			"import assert from \"node:assert/strict\";\n"+
			fmt.Sprintf("test(\"tui fixture ran in package\", () => { %s });\n", assertion))
}

func (f *gateFixture) qualityLog(runDir string) string {
	f.t.Helper()
	logs, err := filepath.Glob(filepath.Join(runDir, "gate.logs", "*-fork-quality.log"))
	if err != nil {
		f.t.Fatal(err)
	}
	if len(logs) != 1 {
		f.t.Fatalf("expected 1 quality log, got %v", logs)
	}
	raw, err := os.ReadFile(logs[0])
	if err != nil {
		f.t.Fatal(err)
	}
	return string(raw)
}

type verdictPayload struct {
	Result string `json:"result"`
	Checks []struct {
		CheckID string `json:"check_id"`
		Result  string `json:"result"`
	} `json:"checks"`
}

func (f *gateFixture) readVerdict(runDir string) verdictPayload {
	f.t.Helper()
	raw, err := os.ReadFile(filepath.Join(runDir, "gate.verdict.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var env struct {
		Payload verdictPayload `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		f.t.Fatal(err)
	}
	return env.Payload
}

func (f *gateFixture) checkMap(v verdictPayload) map[string]string {
	m := make(map[string]string, len(v.Checks))
	for _, c := range v.Checks {
		m[c.CheckID] = c.Result
	}
	return m
}

func (f *gateFixture) readNpxLines() []string {
	f.t.Helper()
	raw, err := os.ReadFile(f.npxLog)
	if err != nil {
		f.t.Fatal(err)
	}
	trimmed := strings.TrimRight(string(raw), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func TestPassingBuildAndFailingQualityFailsWithoutCacheSource(t *testing.T) {
	f := newGateFixture(t)
	f.writePack("pi", "touch build.marker")
	f.writeNpxStub("echo \"$PWD:$*\" >> \"$NPX_LOG\"\nexit 1\n")
	f.writeFile(filepath.Join(f.fork, "packages", "pkg-a", "index.ts"), "export const a = 2;\n")
	f.git(f.fork, "commit", "-qam", "touch pkg-a")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("pi", "run-quality-fail", head, nil)
	if code != 1 {
		t.Fatalf("code = %d, want 1\n%s%s", code, stdout, stderr)
	}
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-quality-fail")
	verdict := f.readVerdict(runDir)
	if verdict.Result != "fail" {
		t.Fatalf("result = %q, want fail", verdict.Result)
	}
	checks := f.checkMap(verdict)
	if checks["fork-build"] != "pass" || checks["fork-quality"] != "fail" {
		t.Fatalf("checks = %+v", checks)
	}
	if _, err := os.Stat(filepath.Join(runDir, "fork-cache-source")); !os.IsNotExist(err) {
		t.Fatal("fork-cache-source should not exist")
	}
}

func TestPassingBuildAndPassingQualityWritesCacheSource(t *testing.T) {
	f := newGateFixture(t)
	f.writePack("pi", "touch build.marker")
	f.writeNpxStub("test -f \"$(git rev-parse --show-toplevel)/build.marker\"\necho \"$(pwd):$*\" >> \"$NPX_LOG\"\n")
	f.writeFile(filepath.Join(f.fork, "packages", "pkg-a", "index.ts"), "export const a = 3;\n")
	f.git(f.fork, "commit", "-qam", "touch pkg-a")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("pi", "run-quality-pass", head, nil)
	if code != 0 {
		t.Fatalf("code = %d, want 0\n%s%s", code, stdout, stderr)
	}
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-quality-pass")
	verdict := f.readVerdict(runDir)
	if verdict.Result != "pass" {
		t.Fatalf("result = %q, want pass", verdict.Result)
	}
	checks := f.checkMap(verdict)
	if checks["fork-build"] != "pass" || checks["fork-quality"] != "pass" {
		t.Fatalf("checks = %+v", checks)
	}
	if _, err := os.Stat(filepath.Join(runDir, "fork-cache-source")); err != nil {
		t.Fatalf("fork-cache-source missing: %v", err)
	}
	lines := f.readNpxLines()
	if len(lines) != 2 || !strings.HasSuffix(lines[0], ":tsc --noEmit") || !strings.HasSuffix(lines[1], "/packages/pkg-a:vitest --run --reporter=dot") {
		t.Fatalf("unexpected npx lines: %v", lines)
	}
}

func TestQualityChecksDoNotSeeCallerHomeOrCredentials(t *testing.T) {
	f := newGateFixture(t)
	callerHome := filepath.Join(f.tmp, "caller home")
	authFile := filepath.Join(callerHome, ".pi", "agent", "auth.json")
	f.writeFile(authFile, `{"openai":{"type":"api_key","key":"fixture-only"}}`)
	f.writePack("pi", `test -s "$HOME/.pi/agent/auth.json"`)
	f.writeNpxStub(
		"test -d \"$HOME\" && test -w \"$HOME\"\n" +
			"test ! -e \"$HOME/.pi/agent/auth.json\" || { echo \"caller auth file visible\" >&2; exit 1; }\n" +
			"echo \"root ran with isolated home\"\n",
	)
	script := "node --test test/*.test.ts"
	f.addTuiTest(
		`assert.equal(existsSync(join(homedir(), ".pi/agent/auth.json")), false);`+
			`for (const name of ["OPENAI_API_KEY", "GITHUB_TOKEN", "AWS_SHARED_CREDENTIALS_FILE", `+
			`"XDG_CONFIG_HOME", "PI_CODING_AGENT_DIR"]) `+
			`assert.equal(Object.hasOwn(process.env, name), false, name);`,
		&script,
	)
	testPath := filepath.Join(f.fork, "packages", "tui", "test", "fixture.test.ts")
	existing, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatal(err)
	}
	f.writeFile(testPath,
		"import { existsSync } from \"node:fs\";\n"+
			"import { homedir } from \"node:os\";\n"+
			"import { join } from \"node:path\";\n"+string(existing))
	f.git(f.fork, "add", ".")
	f.git(f.fork, "commit", "-m", "test quality environment")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("pi", "run-isolated-quality", head, map[string]string{
		"HOME":                        callerHome,
		"OPENAI_API_KEY":              "fixture-only",
		"GITHUB_TOKEN":                "fixture-only",
		"AWS_SHARED_CREDENTIALS_FILE": authFile,
		"XDG_CONFIG_HOME":             filepath.Join(callerHome, ".config"),
		"PI_CODING_AGENT_DIR":         filepath.Dir(authFile),
	})
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-isolated-quality")
	log := f.qualityLog(runDir)
	if code != 0 {
		t.Fatalf("code = %d, want 0\n%s%s%s", code, stdout, stderr, log)
	}
	if !strings.Contains(log, "root ran with isolated home") || !strings.Contains(log, "tui fixture ran in package") {
		t.Fatalf("missing expected log lines in:\n%s", log)
	}
}

func TestFailingBuildSkipsQualityExecution(t *testing.T) {
	f := newGateFixture(t)
	f.writePack("pi", "exit 1")
	f.writeNpxStub("echo \"$(pwd):$*\" >> \"$NPX_LOG\"\n")
	f.writeFile(filepath.Join(f.fork, "packages", "pkg-a", "index.ts"), "export const a = 4;\n")
	f.git(f.fork, "commit", "-qam", "touch pkg-a")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("pi", "run-build-fail", head, nil)
	if code != 1 {
		t.Fatalf("code = %d, want 1\n%s%s", code, stdout, stderr)
	}
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-build-fail")
	verdict := f.readVerdict(runDir)
	if verdict.Result != "fail" {
		t.Fatalf("result = %q, want fail", verdict.Result)
	}
	checks := f.checkMap(verdict)
	if checks["fork-build"] != "fail" || checks["fork-quality"] != "fail" {
		t.Fatalf("checks = %+v", checks)
	}
	if _, err := os.Stat(filepath.Join(runDir, "fork-cache-source")); !os.IsNotExist(err) {
		t.Fatal("fork-cache-source should not exist")
	}
	if _, err := os.Stat(f.npxLog); !os.IsNotExist(err) {
		t.Fatal("fork-quality should not run npx when fork-build fails")
	}
}

func TestDiffTouchingNoPackageSkipsPackageTests(t *testing.T) {
	f := newGateFixture(t)
	f.writePack("pi", "true")
	f.writeNpxStub("echo \"$(pwd):$*\" >> \"$NPX_LOG\"\n")
	f.writeFile(filepath.Join(f.fork, "README.md"), "updated\n")
	f.git(f.fork, "commit", "-qam", "touch readme only")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("pi", "run-no-pkg", head, nil)
	if code != 0 {
		t.Fatalf("code = %d, want 0\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "skipping package tests") {
		t.Fatalf("expected 'skipping package tests' in stdout:\n%s", stdout)
	}
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-no-pkg")
	verdict := f.readVerdict(runDir)
	if verdict.Result != "pass" {
		t.Fatalf("result = %q, want pass", verdict.Result)
	}
	lines := f.readNpxLines()
	if len(lines) != 1 || !strings.HasSuffix(lines[0], ":tsc --noEmit") {
		t.Fatalf("unexpected npx lines: %v", lines)
	}
}

func TestTouchedPackagesRunTheirDeclaredTestsNotUntouchedPackages(t *testing.T) {
	f := newGateFixture(t)
	f.writePack("pi", "touch build.marker")
	f.writeNpxStub(
		"test -f \"$(git rev-parse --show-toplevel)/build.marker\"\n" +
			"echo \"$PWD:$*\" >> \"$NPX_LOG\"\n" +
			"case \"$*\" in \"tsc --noEmit\"|\"vitest --run --reporter=dot\") ;; *) exit 1;; esac\n",
	)
	script := "node --test test/*.test.ts && echo tui-compound-step-ran"
	f.addTuiTest("assert.equal(process.cwd().endsWith(\"/packages/tui\"), true);", &script)
	f.writeFile(filepath.Join(f.fork, "packages", "pkg-a", "index.ts"), "export const a = 5;\n")
	f.git(f.fork, "add", ".")
	f.git(f.fork, "commit", "-m", "touch tui and pkg-a")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("pi", "run-package-scripts", head, nil)
	if code != 0 {
		t.Fatalf("code = %d, want 0\n%s%s", code, stdout, stderr)
	}
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-package-scripts")
	verdict := f.readVerdict(runDir)
	if verdict.Result != "pass" {
		t.Fatalf("result = %q, want pass", verdict.Result)
	}
	checks := f.checkMap(verdict)
	if checks["fork-build"] != "pass" || checks["fork-quality"] != "pass" {
		t.Fatalf("checks = %+v", checks)
	}
	if _, err := os.Stat(filepath.Join(runDir, "fork-cache-source")); err != nil {
		t.Fatalf("fork-cache-source missing: %v", err)
	}
	log := f.qualityLog(runDir)
	if !strings.Contains(log, "tui fixture ran in package") || !strings.Contains(log, "\ntui-compound-step-ran\n") {
		t.Fatalf("missing expected tui output in:\n%s", log)
	}
	if !strings.Contains(stdout+stderr+log, "node --test test/*.test.ts") {
		t.Fatalf("missing script in output")
	}
	lines := f.readNpxLines()
	if len(lines) != 2 || !strings.HasSuffix(lines[0], ":tsc --noEmit") || !strings.HasSuffix(lines[1], "/packages/pkg-a:vitest --run --reporter=dot") {
		t.Fatalf("unexpected npx lines: %v", lines)
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "/packages/tui:vitest") || strings.Contains(joined, "/packages/pkg-b:") {
		t.Fatalf("unexpected package ran: %s", joined)
	}
}

func TestRebasedCandidateTestsOnlyPatchPackagesNotUpstreamChanges(t *testing.T) {
	f := newGateFixture(t)
	f.writePack("pi", "touch build.marker")
	f.writeNpxStub("test -f \"$(git rev-parse --show-toplevel)/build.marker\"\necho \"$PWD:$*\" >> \"$NPX_LOG\"\n")

	oldUpstream := f.base
	f.writeFile(filepath.Join(f.fork, "packages", "pkg-a", "index.ts"), "export const a = 2;\n")
	f.git(f.fork, "commit", "-qam", "old delivered pkg-a patch")
	oldDelivered := f.git(f.fork, "rev-parse", "HEAD")
	f.git(f.fork, "update-ref", "refs/heads/patch/pi", oldDelivered)

	f.git(f.fork, "checkout", "--detach", oldUpstream)
	f.writeFile(filepath.Join(f.fork, "packages", "pkg-b", "index.ts"), "export const b = 2;\n")
	f.git(f.fork, "commit", "-qam", "upstream-only pkg-b change")
	newUpstream := f.git(f.fork, "rev-parse", "HEAD")
	f.git(f.fork, "tag", "v2.0.0")
	f.writeFile(filepath.Join(f.fork, "packages", "pkg-b", "index.ts"), "export const b = 3;\n")
	f.git(f.fork, "commit", "-qam", "upstream moves past release")
	f.git(f.fork, "update-ref", "refs/remotes/upstream/main", "HEAD")
	f.git(f.fork, "checkout", "--detach", "v2.0.0")
	f.git(f.fork, "cherry-pick", oldDelivered)
	head := f.git(f.fork, "rev-parse", "HEAD")
	f.base = old_delivered_or(oldDelivered)

	ancestry := exec.Command("git", "merge-base", "--is-ancestor", f.base, head)
	ancestry.Dir = f.fork
	if err := ancestry.Run(); err == nil {
		t.Fatal("fixture must have divergent fork tips")
	}

	pkgsBase, err := forkgate.TouchedPackages(f.fork, f.base, head)
	if err != nil || len(pkgsBase) != 1 || pkgsBase[0] != "packages/pkg-b" {
		t.Fatalf("touched(base, head) = %v, %v", pkgsBase, err)
	}
	pkgsUp, err := forkgate.TouchedPackages(f.fork, newUpstream, head)
	if err != nil || len(pkgsUp) != 1 || pkgsUp[0] != "packages/pkg-a" {
		t.Fatalf("touched(newUpstream, head) = %v, %v", pkgsUp, err)
	}

	code, stdout, stderr := f.runForkGate("pi", "run-rebased-candidate", head, nil)
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-rebased-candidate")
	if code != 0 {
		t.Fatalf("code = %d, want 0\n%s%s%s", code, stdout, stderr, f.qualityLog(runDir))
	}
	verdict := f.readVerdict(runDir)
	if verdict.Result != "pass" {
		t.Fatalf("result = %q, want pass", verdict.Result)
	}
	checks := f.checkMap(verdict)
	if checks["fork-build"] != "pass" || checks["fork-quality"] != "pass" {
		t.Fatalf("checks = %+v", checks)
	}
	lines := f.readNpxLines()
	if len(lines) != 2 || !strings.HasSuffix(lines[0], ":tsc --noEmit") || !strings.HasSuffix(lines[1], "/packages/pkg-a:vitest --run --reporter=dot") {
		t.Fatalf("unexpected npx lines: %v", lines)
	}
	if strings.Contains(strings.Join(lines, "\n"), "/packages/pkg-b:") {
		t.Fatalf("pkg-b should not have run: %v", lines)
	}
	if _, err := os.Stat(filepath.Join(runDir, "fork-cache-source")); err != nil {
		t.Fatalf("fork-cache-source missing: %v", err)
	}
}

func old_delivered_or(s string) string { return s }

func TestIncrementalCandidateTestsOnlyPackagesTouchedByCandidate(t *testing.T) {
	f := newGateFixture(t)
	f.writePack("pi", "touch build.marker")
	f.writeNpxStub(
		"test -f \"$(git rev-parse --show-toplevel)/build.marker\"\n" +
			"echo \"$PWD:$*\" >> \"$NPX_LOG\"\n" +
			"case \"$PWD\" in */packages/pkg-b) echo \"untouched pkg-b timed out\" >&2; exit 1;; esac\n",
	)
	f.writeFile(filepath.Join(f.fork, "packages", "pkg-b", "index.ts"), "export const b = 2;\n")
	f.git(f.fork, "commit", "-qam", "earlier delivered fork patch in pkg-b")
	delivered := f.git(f.fork, "rev-parse", "HEAD")
	f.git(f.fork, "update-ref", "refs/heads/patch/pi", delivered)
	f.base = delivered

	f.writeFile(filepath.Join(f.fork, "packages", "pkg-a", "index.ts"), "export const a = 6;\n")
	f.git(f.fork, "commit", "-qam", "incremental candidate change in pkg-a")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("pi", "run-incremental-candidate", head, nil)
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-incremental-candidate")
	if code != 0 {
		t.Fatalf("code = %d, want 0\n%s%s%s", code, stdout, stderr, f.qualityLog(runDir))
	}
	verdict := f.readVerdict(runDir)
	if verdict.Result != "pass" {
		t.Fatalf("result = %q, want pass", verdict.Result)
	}
	checks := f.checkMap(verdict)
	if checks["fork-build"] != "pass" || checks["fork-quality"] != "pass" {
		t.Fatalf("checks = %+v", checks)
	}
	lines := f.readNpxLines()
	if len(lines) != 2 || !strings.HasSuffix(lines[0], ":tsc --noEmit") || !strings.HasSuffix(lines[1], "/packages/pkg-a:vitest --run --reporter=dot") {
		t.Fatalf("unexpected npx lines: %v", lines)
	}
	if strings.Contains(strings.Join(lines, "\n"), "/packages/pkg-b:") {
		t.Fatalf("pkg-b should not have run: %v", lines)
	}
	if _, err := os.Stat(filepath.Join(runDir, "fork-cache-source")); err != nil {
		t.Fatalf("fork-cache-source missing: %v", err)
	}
}

func TestQualityBoundsVitestAndGoWorkerParallelism(t *testing.T) {
	f := newGateFixture(t)
	expected := forkgate.QualityWorkerLimit()
	n, err := strconv.Atoi(expected)
	if err != nil || n < 1 || n > forkgate.DefaultQualityWorkers {
		t.Fatalf("unexpected worker limit: %q", expected)
	}
	f.writePack("pi", "touch build.marker")
	f.writeNpxStub(fmt.Sprintf(
		"test \"${VITEST_MAX_WORKERS:-}\" = \"%s\" || { echo \"VITEST_MAX_WORKERS=${VITEST_MAX_WORKERS:-}\" >&2; exit 1; }\n"+
			"test \"${GOMAXPROCS:-}\" = \"%s\" || { echo \"GOMAXPROCS=${GOMAXPROCS:-}\" >&2; exit 1; }\n"+
			"echo \"$PWD:$*\" >> \"$NPX_LOG\"\n",
		expected, expected,
	))
	f.writeFile(filepath.Join(f.fork, "packages", "pkg-a", "index.ts"), "export const a = 7;\n")
	f.git(f.fork, "commit", "-qam", "verify bounded quality worker env")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("pi", "run-bounded-workers", head, map[string]string{
		"VITEST_MAX_WORKERS": "99",
		"GOMAXPROCS":         "99",
	})
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-bounded-workers")
	if code != 0 {
		t.Fatalf("code = %d, want 0\n%s%s%s", code, stdout, stderr, f.qualityLog(runDir))
	}
	verdict := f.readVerdict(runDir)
	if verdict.Result != "pass" {
		t.Fatalf("result = %q, want pass", verdict.Result)
	}
	lines := f.readNpxLines()
	if len(lines) != 2 {
		t.Fatalf("unexpected npx lines: %v", lines)
	}
}

func (f *gateFixture) writeMiseCargoStub() map[string]string {
	f.t.Helper()
	callerHome := filepath.Join(f.tmp, "caller home")
	authFile := filepath.Join(callerHome, ".pi", "agent", "auth.json")
	f.writeFile(authFile, "fixture-only\n")
	cargo := filepath.Join(f.binDir, "cargo")
	body := "#!/bin/sh\nset -eu\n" +
		"test -d \"$HOME\" && test -w \"$HOME\"\n" +
		"test ! -e \"$HOME/.pi/agent/auth.json\"\n" +
		"test \"${OPENAI_API_KEY+x}\" != x\n" +
		"test \"${GITHUB_TOKEN+x}\" != x\n" +
		"test \"${AWS_SHARED_CREDENTIALS_FILE+x}\" != x\n" +
		"test \"${XDG_CONFIG_HOME+x}\" != x\n" +
		"test \"${PI_CODING_AGENT_DIR+x}\" != x\n" +
		"echo \"cargo shim ran with isolated home and environment\"\n" +
		"echo \"mise ERROR cargo is not a valid shim. This likely means you uninstalled a tool\" >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(cargo, []byte(body), 0o755); err != nil {
		f.t.Fatal(err)
	}
	return map[string]string{
		"HOME":                        callerHome,
		"OPENAI_API_KEY":              "fixture-only",
		"GITHUB_TOKEN":                "fixture-only",
		"AWS_SHARED_CREDENTIALS_FILE": authFile,
		"XDG_CONFIG_HOME":             filepath.Join(callerHome, ".config"),
		"PI_CODING_AGENT_DIR":         filepath.Dir(authFile),
	}
}

func TestMiseInvalidCargoShimInNestedNpmScriptIsAnError(t *testing.T) {
	f := newGateFixture(t)
	callerEnv := f.writeMiseCargoStub()
	f.writePack("pi", "touch build.marker")
	f.writeNpxStub("echo \"$PWD:$*\" >> \"$NPX_LOG\"\n")
	pkgJSON, _ := json.Marshal(map[string]any{
		"name":    "pkg-a",
		"version": "1.0.0",
		"scripts": map[string]string{"test": "npm run selected", "selected": "cargo test"},
	})
	f.writeFile(filepath.Join(f.fork, "packages", "pkg-a", "package.json"), string(pkgJSON))
	f.git(f.fork, "commit", "-qam", "declare nested cargo test")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("pi", "run-mise-cargo-nested", head, callerEnv)
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-mise-cargo-nested")
	log := f.qualityLog(runDir)
	for _, want := range []string{
		"cargo shim ran with isolated home and environment",
		"mise ERROR cargo is not a valid shim",
		"cargo test",
		"packages/pkg-a: unable to run scripts.test 'npm run selected'",
		"mise could not resolve cargo in the isolated environment",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("missing %q in log:\n%s", want, log)
		}
	}
	if code != 4 {
		t.Fatalf("code = %d, want 4\n%s%s%s", code, stdout, stderr, log)
	}
	verdict := f.readVerdict(runDir)
	if verdict.Result != "error" {
		t.Fatalf("result = %q, want error", verdict.Result)
	}
	checks := f.checkMap(verdict)
	if checks["fork-build"] != "pass" || checks["fork-quality"] != "error" {
		t.Fatalf("checks = %+v", checks)
	}
	if _, err := os.Stat(filepath.Join(runDir, "fork-cache-source")); !os.IsNotExist(err) {
		t.Fatal("fork-cache-source should not exist")
	}
	authContent, err := os.ReadFile(filepath.Join(callerEnv["HOME"], ".pi/agent/auth.json"))
	if err != nil || string(authContent) != "fixture-only\n" {
		t.Fatalf("caller auth file modified: %q, %v", string(authContent), err)
	}
}

func TestMiseInvalidCargoShimInDirectRunQualityIsAnError(t *testing.T) {
	f := newGateFixture(t)
	callerEnv := f.writeMiseCargoStub()
	cmd := exec.Command(f.drainBin, "fork-gate", "--run-quality", f.tmp, "cargo test")
	cmd.Dir = f.fork
	cmd.Env = envWithOverrides(callerEnv, map[string]string{
		"PATH": f.binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	if !strings.Contains(stdout.String(), "cargo shim ran with isolated home and environment") {
		t.Fatalf("missing stdout in:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "mise ERROR cargo is not a valid shim") ||
		!strings.Contains(stderr.String(), "fork-quality:") ||
		!strings.Contains(stderr.String(), "mise could not resolve cargo in the isolated environment") {
		t.Fatalf("missing expected stderr in:\n%s", stderr.String())
	}
	if code != 4 {
		t.Fatalf("code = %d, want 4\n%s%s", code, stdout.String(), stderr.String())
	}
	authContent, err := os.ReadFile(filepath.Join(callerEnv["HOME"], ".pi/agent/auth.json"))
	if err != nil || string(authContent) != "fixture-only\n" {
		t.Fatalf("caller auth file modified: %q, %v", string(authContent), err)
	}
}

func TestUnavailablePackageRunnerIsAnErrorNotATestFailure(t *testing.T) {
	f := newGateFixture(t)
	f.writePack("pi", "touch build.marker")
	f.writeNpxStub("echo \"$PWD:$*\" >> \"$NPX_LOG\"\n")
	pkgJSON, _ := json.Marshal(map[string]any{
		"name":    "pkg-a",
		"version": "1.0.0",
		"scripts": map[string]string{"test": "tillr-nonexistent-test-runner --run"},
	})
	f.writeFile(filepath.Join(f.fork, "packages", "pkg-a", "package.json"), string(pkgJSON))
	f.git(f.fork, "commit", "-qam", "declare unavailable runner")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("pi", "run-missing-runner", head, nil)
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-missing-runner")
	if code != 4 {
		t.Fatalf("code = %d, want 4\n%s%s", code, stdout, stderr)
	}
	verdict := f.readVerdict(runDir)
	if verdict.Result != "error" {
		t.Fatalf("result = %q, want error", verdict.Result)
	}
	checks := f.checkMap(verdict)
	if checks["fork-build"] != "pass" || checks["fork-quality"] != "error" {
		t.Fatalf("checks = %+v", checks)
	}
	diag := stdout + stderr + f.qualityLog(runDir)
	if !strings.Contains(diag, "pkg-a") || !strings.Contains(diag, "tillr-nonexistent-test-runner") {
		t.Fatalf("missing diagnostics in:\n%s", diag)
	}
	if _, err := os.Stat(filepath.Join(runDir, "fork-cache-source")); !os.IsNotExist(err) {
		t.Fatal("fork-cache-source should not exist")
	}
}

func TestOfflineNpmExecCannotResolveDeclaredRunner(t *testing.T) {
	f := newGateFixture(t)
	f.writePack("pi", "touch build.marker")
	f.writeNpxStub("echo \"$PWD:$*\" >> \"$NPX_LOG\"\n")
	runner := "tillr-nonexistent-test-runner"
	script := fmt.Sprintf("npm exec --offline --yes --cache .empty-npm-cache -- %s --run", runner)
	f.addTuiTest("assert.equal(process.cwd().endsWith(\"/packages/tui\"), true);", &script)
	f.git(f.fork, "add", ".")
	f.git(f.fork, "commit", "-m", "declare unresolved npm exec runner")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("pi", "run-unresolved-npm-exec", head, nil)
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-unresolved-npm-exec")
	log := f.qualityLog(runDir)
	if !strings.Contains(log, "ENOTCACHED") {
		t.Fatalf("missing ENOTCACHED in:\n%s", log)
	}
	if code != 4 {
		t.Fatalf("code = %d, want 4\n%s%s%s", code, stdout, stderr, log)
	}
	verdict := f.readVerdict(runDir)
	if verdict.Result != "error" {
		t.Fatalf("result = %q, want error", verdict.Result)
	}
	checks := f.checkMap(verdict)
	if checks["fork-build"] != "pass" || checks["fork-quality"] != "error" {
		t.Fatalf("checks = %+v", checks)
	}
	wantDiag := fmt.Sprintf("packages/tui: unable to run scripts.test '%s'", script)
	if !strings.Contains(log, wantDiag) {
		t.Fatalf("missing %q in:\n%s", wantDiag, log)
	}
	if _, err := os.Stat(filepath.Join(runDir, "fork-cache-source")); !os.IsNotExist(err) {
		t.Fatal("fork-cache-source should not exist")
	}
}

func TestMissingPackageTestDeclarationIsAnError(t *testing.T) {
	f := newGateFixture(t)
	f.writePack("pi", "touch build.marker")
	f.writeNpxStub("echo \"$PWD:$*\" >> \"$NPX_LOG\"\n")
	f.addTuiTest("assert.equal(process.cwd().endsWith(\"/packages/tui\"), true);", nil)
	f.git(f.fork, "add", ".")
	f.git(f.fork, "commit", "-m", "add tui without a test declaration")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("pi", "run-missing-declaration", head, nil)
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-missing-declaration")
	if code != 4 {
		t.Fatalf("code = %d, want 4\n%s%s", code, stdout, stderr)
	}
	verdict := f.readVerdict(runDir)
	if verdict.Result != "error" {
		t.Fatalf("result = %q, want error", verdict.Result)
	}
	checks := f.checkMap(verdict)
	if checks["fork-build"] != "pass" || checks["fork-quality"] != "error" {
		t.Fatalf("checks = %+v", checks)
	}
	log := f.qualityLog(runDir)
	if !strings.Contains(log, "packages/tui") || !strings.Contains(log, "no scripts.test") {
		t.Fatalf("missing expected diagnostic in:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(runDir, "fork-cache-source")); !os.IsNotExist(err) {
		t.Fatal("fork-cache-source should not exist")
	}
}

func TestAGenuineFailingDeclaredPackageTestIsAFailure(t *testing.T) {
	f := newGateFixture(t)
	f.writePack("pi", "touch build.marker")
	f.writeNpxStub("echo \"$PWD:$*\" >> \"$NPX_LOG\"\n")
	script := "node --test test/*.test.ts"
	f.addTuiTest("assert.equal(1, 2);", &script)
	f.git(f.fork, "add", ".")
	f.git(f.fork, "commit", "-m", "add failing tui test")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("pi", "run-failing-tui", head, nil)
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-failing-tui")
	if code != 1 {
		t.Fatalf("code = %d, want 1\n%s%s", code, stdout, stderr)
	}
	verdict := f.readVerdict(runDir)
	if verdict.Result != "fail" {
		t.Fatalf("result = %q, want fail", verdict.Result)
	}
	checks := f.checkMap(verdict)
	if checks["fork-build"] != "pass" || checks["fork-quality"] != "fail" {
		t.Fatalf("checks = %+v", checks)
	}
	if !strings.Contains(f.qualityLog(runDir), "tui fixture ran in package") {
		t.Fatal("missing tui output in log")
	}
	if _, err := os.Stat(filepath.Join(runDir, "fork-cache-source")); !os.IsNotExist(err) {
		t.Fatal("fork-cache-source should not exist")
	}
}

func TestUnlistedForkKeepsBuildOnlyVerdictAndReportsIt(t *testing.T) {
	f := newGateFixture(t)
	f.writePack("custom-fork", "true")
	f.writeFile(filepath.Join(f.fork, "README.md"), "custom\n")
	f.git(f.fork, "commit", "-qam", "custom change")
	head := f.git(f.fork, "rev-parse", "HEAD")

	code, stdout, stderr := f.runForkGate("custom-fork", "run-unlisted", head, nil)
	if code != 0 {
		t.Fatalf("code = %d, want 0\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "build-only verdict") {
		t.Fatalf("missing 'build-only verdict' in stdout:\n%s", stdout)
	}
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-unlisted")
	verdict := f.readVerdict(runDir)
	if verdict.Result != "pass" {
		t.Fatalf("result = %q, want pass", verdict.Result)
	}
	if len(verdict.Checks) != 1 || verdict.Checks[0].CheckID != "fork-build" {
		t.Fatalf("unexpected checks: %+v", verdict.Checks)
	}
}

func TestPiSubagentsOfflineEndToEnd(t *testing.T) {
	f := newGateFixture(t)
	f.writePack("pi-subagents", "npm ci --omit=dev --legacy-peer-deps --ignore-scripts --no-audit --no-fund")
	headPass := f.commitOfflineDevcheckPackage(map[string]string{
		"typecheck": "devcheck typecheck",
		"test:unit": "devcheck test:unit",
	})
	code, stdout, stderr := f.runForkGate("pi-subagents", "run-subagents-pass", headPass, nil)
	if code != 0 {
		t.Fatalf("code = %d, want 0\n%s%s", code, stdout, stderr)
	}
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-subagents-pass")
	verdict := f.readVerdict(runDir)
	if verdict.Result != "pass" {
		t.Fatalf("result = %q, want pass", verdict.Result)
	}
	checks := f.checkMap(verdict)
	if checks["fork-build"] != "pass" || checks["fork-quality"] != "pass" {
		t.Fatalf("checks = %+v", checks)
	}
	if _, err := os.Stat(filepath.Join(runDir, "fork-cache-source")); err != nil {
		t.Fatalf("fork-cache-source missing: %v", err)
	}

	headFail := f.commitOfflineDevcheckPackage(map[string]string{
		"typecheck": "devcheck typecheck",
		"test:unit": "devcheck test:unit --fail",
	})
	codeFail, stdoutFail, stderrFail := f.runForkGate("pi-subagents", "run-subagents-fail", headFail, nil)
	if codeFail != 1 {
		t.Fatalf("code = %d, want 1\n%s%s", codeFail, stdoutFail, stderrFail)
	}
	runDirFail := filepath.Join(f.root, ".tillr", "loop", "run-subagents-fail")
	verdictFail := f.readVerdict(runDirFail)
	if verdictFail.Result != "fail" {
		t.Fatalf("result = %q, want fail", verdictFail.Result)
	}
	checksFail := f.checkMap(verdictFail)
	if checksFail["fork-build"] != "pass" || checksFail["fork-quality"] != "fail" {
		t.Fatalf("checks = %+v", checksFail)
	}
	if _, err := os.Stat(filepath.Join(runDirFail, "fork-cache-source")); !os.IsNotExist(err) {
		t.Fatal("fork-cache-source should not exist")
	}
}

func TestPiSessionSearchOfflineEndToEnd(t *testing.T) {
	f := newGateFixture(t)
	f.writePack(
		"pi-session-search",
		"npm ci --legacy-peer-deps --ignore-scripts --no-audit --no-fund && npm run build && npm prune --omit=dev --legacy-peer-deps --ignore-scripts --no-audit --no-fund",
	)
	headPass := f.commitOfflineDevcheckPackage(map[string]string{
		"build": "devcheck build",
		"check": "devcheck check",
		"test":  "devcheck test",
	})
	code, stdout, stderr := f.runForkGate("pi-session-search", "run-search-pass", headPass, nil)
	if code != 0 {
		t.Fatalf("code = %d, want 0\n%s%s", code, stdout, stderr)
	}
	runDir := filepath.Join(f.root, ".tillr", "loop", "run-search-pass")
	verdict := f.readVerdict(runDir)
	if verdict.Result != "pass" {
		t.Fatalf("result = %q, want pass", verdict.Result)
	}
	checks := f.checkMap(verdict)
	if checks["fork-build"] != "pass" || checks["fork-quality"] != "pass" {
		t.Fatalf("checks = %+v", checks)
	}
	if _, err := os.Stat(filepath.Join(runDir, "fork-cache-source")); err != nil {
		t.Fatalf("fork-cache-source missing: %v", err)
	}

	headFail := f.commitOfflineDevcheckPackage(map[string]string{
		"build": "devcheck build",
		"check": "devcheck check",
		"test":  "devcheck test --fail",
	})
	codeFail, stdoutFail, stderrFail := f.runForkGate("pi-session-search", "run-search-fail", headFail, nil)
	if codeFail != 1 {
		t.Fatalf("code = %d, want 1\n%s%s", codeFail, stdoutFail, stderrFail)
	}
	runDirFail := filepath.Join(f.root, ".tillr", "loop", "run-search-fail")
	verdictFail := f.readVerdict(runDirFail)
	if verdictFail.Result != "fail" {
		t.Fatalf("result = %q, want fail", verdictFail.Result)
	}
	checksFail := f.checkMap(verdictFail)
	if checksFail["fork-build"] != "pass" || checksFail["fork-quality"] != "fail" {
		t.Fatalf("checks = %+v", checksFail)
	}
	if _, err := os.Stat(filepath.Join(runDirFail, "fork-cache-source")); !os.IsNotExist(err) {
		t.Fatal("fork-cache-source should not exist")
	}
}

func TestHelpWritesUsageToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := forkgate.Run([]string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "tillr-drain fork-gate") {
		t.Fatalf("stdout = %q, want usage naming tillr-drain fork-gate", out)
	}
}

func TestMissingRequiredFlagExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := forkgate.Run([]string{"--item", "item-1"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "missing required flag --lane") {
		t.Fatalf("stderr = %q, want missing required flag message", stderr.String())
	}
}
