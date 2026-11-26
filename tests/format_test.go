package format_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const packPrefix = "yolo-packs/tillr/files/local/share/tillr"

var badGo = []byte("package fixture\nfunc example(){println(1)}\n")

type formatFixture struct {
	t        *testing.T
	repoRoot string
	root     string
	cache    string
	repoGate string
}

func newFormatFixture(t *testing.T) *formatFixture {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := wd
	if filepath.Base(repoRoot) == "tests" {
		repoRoot = filepath.Dir(repoRoot)
	}
	durable := os.Getenv("YOLO_DURABLE_DIR")
	if durable == "" {
		durable = t.TempDir()
	} else if err := os.MkdirAll(durable, 0o755); err != nil {
		t.Fatal(err)
	}
	tmp, err := os.MkdirTemp(durable, "format-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	f := &formatFixture{
		t:        t,
		repoRoot: repoRoot,
		root:     tmp,
		cache:    filepath.Join(tmp, ".git", "gofmt-cache"),
		repoGate: filepath.Join(tmp, "tools", "gate", "repo-gate"),
	}
	f.runCmd(0, nil, "git", "init", "-q")
	justBytes, err := os.ReadFile(filepath.Join(repoRoot, ".justfile"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".justfile"), justBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(f.repoGate), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-C", filepath.Join(repoRoot, "tools", "gate"), "-o", f.repoGate, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build repo-gate: %v\n%s", err, out)
	}
	f.runCmd(0, nil, "git", "add", ".justfile")
	return f
}

func (f *formatFixture) runCmd(wantCode int, extraEnv map[string]string, args ...string) (string, string) {
	f.t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = f.root
	env := append(os.Environ(),
		"TMPDIR="+f.root,
		"JUST_FORMAT_CACHE_DIR="+f.cache,
		"XDG_CONFIG_HOME="+filepath.Join(f.root, ".git", "xdg-config"),
	)
	for k, v := range extraEnv {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			f.t.Fatalf("exec %v: %v", args, err)
		}
	}
	if code != wantCode {
		f.t.Fatalf("cmd %v exit=%d want=%d\nstdout:\n%s\nstderr:\n%s", args, code, wantCode, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

func (f *formatFixture) plant(rel string, data []byte, tracked bool) string {
	f.t.Helper()
	p := filepath.Join(f.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		f.t.Fatal(err)
	}
	if tracked {
		f.runCmd(0, nil, "git", "add", "-f", "--", rel)
	}
	return p
}

func (f *formatFixture) snapshot() map[string]string {
	f.t.Helper()
	out := make(map[string]string)
	err := filepath.WalkDir(f.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(f.root, p)
		out[filepath.ToSlash(rel)] = string(raw) + "|" + info.ModTime().String()
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func assertMapsEqual(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("snapshot len=%d want=%d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("snapshot mismatch for %s", k)
		}
	}
}

func realGofmt(t *testing.T) string {
	t.Helper()
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == "" {
			continue
		}
		cand := filepath.Join(entry, "gofmt")
		info, err := os.Stat(cand)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			real, err := filepath.EvalSymlinks(cand)
			if err == nil && filepath.Base(real) != "mise" {
				return cand
			}
		}
	}
	p, err := exec.LookPath("gofmt")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *formatFixture) installCountingGofmt(tag string) (string, map[string]string) {
	f.t.Helper()
	binDir := filepath.Join(f.root, ".git", "test-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	logPath := filepath.Join(f.root, ".git", "gofmt-calls.log")
	real := realGofmt(f.t)
	wrapper := filepath.Join(binDir, "gofmt")
	script := "#!/bin/sh\n# " + tag + "\nprintf '%s\\n' \"$*\" >> " + logPath + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		f.t.Fatal(err)
	}
	return logPath, map[string]string{"PATH": binDir + string(os.PathListSeparator) + os.Getenv("PATH")}
}

func TestFormatCheckReportsWithoutWritingAndIsIdempotent(t *testing.T) {
	f := newFormatFixture(t)
	names := []string{"tools/example/a.go", "tools/example/space name.go", packPrefix + "/tillr/a.go"}
	for _, name := range names {
		f.plant(name, badGo, true)
	}
	before := f.snapshot()
	out, _ := f.runCmd(1, nil, "just", "format", "--check")
	gotLines := strings.Split(strings.TrimSpace(out), "\n")
	sort.Strings(gotLines)
	wantLines := append([]string(nil), names...)
	sort.Strings(wantLines)
	if strings.Join(gotLines, "\n") != strings.Join(wantLines, "\n") {
		t.Fatalf("check stdout = %v, want %v", gotLines, wantLines)
	}
	assertMapsEqual(t, f.snapshot(), before)

	f.runCmd(0, nil, "just", "format")
	gofmtCmd := exec.Command("gofmt")
	gofmtCmd.Stdin = bytes.NewReader(badGo)
	expected, err := gofmtCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		got, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, expected) {
			t.Fatalf("%s not formatted", name)
		}
	}
	formatted := f.snapshot()
	f.runCmd(0, nil, "just", "format")
	assertMapsEqual(t, f.snapshot(), formatted)
	out, _ = f.runCmd(0, nil, "just", "format", "--check")
	if out != "" {
		t.Fatalf("expected empty check output, got %q", out)
	}
}

func TestFormatExcludedTrackedFilesAndUntrackedUntouched(t *testing.T) {
	f := newFormatFixture(t)
	names := []string{
		"tools/vendor/a.go", "tools/example/vendor/nested/a.go",
		packPrefix + "/tillr/vendor/a.go", ".forks/example/a.go",
		"tools/.forks/example/a.go", "vendor/a.go",
		"yolo-packs/matt/briefing/prose.md", "yolo-packs/matt/skills/example/a.go",
		"tools/example/change.patch", "yolo-packs/other/a.go", "elsewhere/a.go",
	}
	for _, name := range names {
		f.plant(name, badGo, true)
	}
	f.plant("tools/example/untracked.go", badGo, false)
	link := filepath.Join(f.root, "tools", "example", "link.go")
	if err := os.Symlink("../../vendor/a.go", link); err != nil {
		t.Fatal(err)
	}
	f.runCmd(0, nil, "git", "add", "tools/example/link.go")
	before := f.snapshot()
	f.runCmd(0, nil, "just", "format")
	f.runCmd(0, nil, "just", "format", "--check")
	assertMapsEqual(t, f.snapshot(), before)
}

func TestFormatSymlinkAncestorCannotReachVendor(t *testing.T) {
	f := newFormatFixture(t)
	source := f.plant("tools/example/a.go", badGo, true)
	protected := f.plant("vendor/a.go", badGo, true)
	_ = os.Remove(source)
	_ = os.Remove(filepath.Dir(source))
	if err := os.Symlink("../vendor", filepath.Dir(source)); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot()
	out, _ := f.runCmd(0, nil, "just", "format", "--check")
	if out != "" {
		t.Fatalf("expected empty output, got %q", out)
	}
	f.runCmd(0, nil, "just", "format")
	got, _ := os.ReadFile(protected)
	if !bytes.Equal(got, badGo) {
		t.Fatal("vendor/a.go was modified through symlink")
	}
	assertMapsEqual(t, f.snapshot(), before)
}

func TestFormatEmptySourceSetAndUnknownArg(t *testing.T) {
	f := newFormatFixture(t)
	f.runCmd(0, nil, "just", "format")
	f.runCmd(0, nil, "just", "format", "--check")
	f.runCmd(2, nil, "just", "format", "--unknown")
}

func TestFormatGateUsesRecipeAndIsRedUntilFormatted(t *testing.T) {
	f := newFormatFixture(t)
	declRaw, err := os.ReadFile(filepath.Join(f.repoRoot, "tools", "gate", "checks", "format.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decl struct {
		Command  []string `json:"command"`
		Scope    string   `json:"scope"`
		Tier     string   `json:"tier"`
		Remember *bool    `json:"remember"`
		BudgetMS int      `json:"budget_ms"`
		Tools    []string `json:"tools"`
		Reads    []string `json:"reads"`
	}
	if err := json.Unmarshal(declRaw, &decl); err != nil {
		t.Fatal(err)
	}
	if strings.Join(decl.Command, " ") != "just format --check" || decl.Scope != "tree" || decl.Tier != "fast" {
		t.Fatalf("unexpected format.json: %+v", decl)
	}
	if decl.Remember == nil || *decl.Remember {
		t.Fatalf("format.json remember must be false")
	}
	if decl.BudgetMS <= 0 {
		t.Fatalf("budget_ms=%d", decl.BudgetMS)
	}
	f.plant("tools/example/a.go", badGo, true)
	before := f.snapshot()
	f.runCmd(1, nil, decl.Command...)
	assertMapsEqual(t, f.snapshot(), before)
	f.runCmd(0, nil, "just", "format")
	f.runCmd(0, nil, decl.Command...)
}

func TestFormatThenPackSyncKeepsSourceAndCopyIdentical(t *testing.T) {
	f := newFormatFixture(t)
	source := f.plant("tools/tillr/example.go", badGo, true)
	goMod, err := os.ReadFile(filepath.Join(f.repoRoot, "tools", "tillr", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	f.plant("tools/tillr/go.mod", goMod, true)
	for _, relDir := range []string{"tools/tillr/packmirror", "tools/tillr/cmd/pack-mirror"} {
		absDir := filepath.Join(f.repoRoot, filepath.FromSlash(relDir))
		err := filepath.WalkDir(absDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(f.repoRoot, p)
			raw, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			f.plant(filepath.ToSlash(rel), raw, true)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.runCmd(0, nil, "just", "format")
	f.runCmd(0, nil, "just", "pack-sync")
	srcBytes, _ := os.ReadFile(source)
	copyBytes, _ := os.ReadFile(filepath.Join(f.root, packPrefix, "tillr", "example.go"))
	if !bytes.Equal(srcBytes, copyBytes) {
		t.Fatal("pack copy differs from source")
	}
}

func TestFormatListAndComments(t *testing.T) {
	f := newFormatFixture(t)
	out, _ := f.runCmd(0, nil, "just", "--list")
	if !regexp.MustCompile(`format[^\n]*Go`).MatchString(out) {
		t.Fatalf("just --list missing format Go description:\n%s", out)
	}
	recipe, _ := os.ReadFile(filepath.Join(f.root, ".justfile"))
	for _, phrase := range []string{"Python", "shell", "TypeScript", "Lua", "84", "mise.toml", "own commit"} {
		if !strings.Contains(string(recipe), phrase) {
			t.Fatalf(".justfile missing %q", phrase)
		}
	}
}

func TestFormatDuplicateDigestsAndCache(t *testing.T) {
	f := newFormatFixture(t)
	cleanSrc := []byte("package fixture\n")
	f.plant("tools/tillr/a.go", badGo, true)
	f.plant(packPrefix+"/tillr/a.go", badGo, true)
	f.plant("tools/tillr/clean.go", cleanSrc, true)
	f.plant(packPrefix+"/tillr/clean.go", cleanSrc, true)

	logPath, env := f.installCountingGofmt("v1")
	readCalls := func() [][]string {
		raw, err := os.ReadFile(logPath)
		if err != nil {
			return nil
		}
		_ = os.Remove(logPath)
		var out [][]string
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line != "" {
				out = append(out, strings.Fields(line))
			}
		}
		return out
	}

	first, _ := f.runCmd(1, env, "just", "format", "--check")
	if len(strings.Split(strings.TrimSpace(first), "\n")) != 2 {
		t.Fatalf("unexpected first check output: %q", first)
	}
	calls := readCalls()
	if len(calls) != 1 || calls[0][0] != "-l" || len(calls[0][1:]) != 2 {
		t.Fatalf("unexpected first calls: %v", calls)
	}

	f.runCmd(1, env, "just", "format", "--check")
	calls = readCalls()
	if len(calls) != 1 || len(calls[0][1:]) != 1 {
		t.Fatalf("unexpected second calls: %v", calls)
	}

	f.runCmd(0, env, "just", "format")
	calls = readCalls()
	if len(calls) != 2 || calls[0][0] != "-l" || calls[1][0] != "-w" || len(calls[1][1:]) != 2 {
		t.Fatalf("unexpected format calls: %v", calls)
	}

	out, _ := f.runCmd(0, env, "just", "format", "--check")
	if out != "" {
		t.Fatalf("expected clean check, got %q", out)
	}
	f.runCmd(0, env, "just", "format")
	if calls := readCalls(); len(calls) != 0 {
		t.Fatalf("expected cache hit, got %v", calls)
	}

	_ = os.WriteFile(filepath.Join(f.root, "tools", "tillr", "clean.go"), badGo, 0o644)
	unstaged, _ := f.runCmd(1, env, "just", "format", "--check")
	if strings.TrimSpace(unstaged) != "tools/tillr/clean.go" {
		t.Fatalf("unexpected unstaged output: %q", unstaged)
	}
	f.runCmd(0, env, "just", "format")
	_ = readCalls()

	logPath, envV2 := f.installCountingGofmt("v2")
	out, _ = f.runCmd(0, envV2, "just", "format", "--check")
	if out != "" {
		t.Fatalf("expected empty v2 check, got %q", out)
	}
	callsV2 := readCalls()
	if len(callsV2) != 1 || callsV2[0][0] != "-l" {
		t.Fatalf("expected v2 re-check, got %v", callsV2)
	}
}

func TestFormatDuplicateSyntaxErrorsAndCorruptCache(t *testing.T) {
	f := newFormatFixture(t)
	broken := []byte("package fixture\nfunc (\n")
	f.plant("tools/tillr/broken.go", broken, true)
	f.plant(packPrefix+"/tillr/broken.go", broken, true)
	for _, args := range [][]string{{"just", "format", "--check"}, {"just", "format"}} {
		_, stderr := f.runCmd(2, nil, args...)
		if !strings.Contains(stderr, "tools/tillr/broken.go:") || !strings.Contains(stderr, packPrefix+"/tillr/broken.go:") {
			t.Fatalf("%v missing duplicate syntax error: %s", args, stderr)
		}
	}

	f.plant("tools/tillr/broken.go", badGo, true)
	f.plant(packPrefix+"/tillr/broken.go", badGo, true)
	_ = os.MkdirAll(f.cache, 0o755)
	bins, _ := filepath.Glob(filepath.Join(f.cache, "*.bin"))
	for _, b := range bins {
		_ = os.WriteFile(b, []byte("truncated"), 0o644)
	}
	blocked := filepath.Join(f.root, ".git", "not-a-directory")
	_ = os.WriteFile(blocked, []byte("file blocking mkdir\n"), 0o644)
	for _, extra := range []map[string]string{nil, {"JUST_FORMAT_CACHE_DIR": blocked}} {
		out, _ := f.runCmd(1, extra, "just", "format", "--check")
		if len(strings.Split(strings.TrimSpace(out), "\n")) != 2 {
			t.Fatalf("unexpected check output: %q", out)
		}
	}
	f.runCmd(0, map[string]string{"JUST_FORMAT_CACHE_DIR": blocked}, "just", "format")
	out, _ := f.runCmd(0, map[string]string{"JUST_FORMAT_CACHE_DIR": blocked}, "just", "format", "--check")
	if out != "" {
		t.Fatalf("expected clean output, got %q", out)
	}
}

func TestFormatInactiveMiseShimBypassedUntilConfigured(t *testing.T) {
	f := newFormatFixture(t)
	f.plant("tools/example/a.go", []byte("package fixture\n"), true)
	shimDir := filepath.Join(f.root, ".git", "shim-bin")
	realDir := filepath.Join(f.root, ".git", "real-bin")
	_ = os.MkdirAll(shimDir, 0o755)
	_ = os.MkdirAll(realDir, 0o755)
	fakeMise := filepath.Join(shimDir, "mise")
	_ = os.WriteFile(fakeMise, []byte("#!/bin/sh\necho 'mise shim invoked' >&2\nexit 99\n"), 0o755)
	_ = os.Symlink(fakeMise, filepath.Join(shimDir, "gofmt"))
	_ = os.Symlink(realGofmt(t), filepath.Join(realDir, "gofmt"))
	env := map[string]string{"PATH": shimDir + string(os.PathListSeparator) + realDir + string(os.PathListSeparator) + os.Getenv("PATH")}

	_ = os.WriteFile(filepath.Join(f.root, "mise.toml"), []byte("[tools]\njust = \"1.58.0\"\n\"cargo:viu\" = \"latest\"\n"), 0o644)
	f.runCmd(0, env, "just", "format", "--check")

	_ = os.WriteFile(filepath.Join(f.root, "mise.toml"), []byte("[tools]\ngo = \"1.27.3\"\n"), 0o644)
	_, stderr := f.runCmd(99, env, "just", "format", "--check")
	if !strings.Contains(stderr, "mise shim invoked") {
		t.Fatalf("expected mise shim stderr, got %q", stderr)
	}
}
