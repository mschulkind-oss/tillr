package build_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var buildModules = []string{
	"tillr-cli", "tillr-drain", "tillr-gate", "tillr-item",
	"tillr-land", "tillr-run", "tillr-store", "tillr-view",
}

type greenBuildFixture struct {
	t        *testing.T
	repoRoot string
	scratch  string
	root     string
	out      string
	env      []string
	green    string
}

func newGreenBuildFixture(t *testing.T) *greenBuildFixture {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := wd
	if filepath.Base(repoRoot) == "tests" {
		repoRoot = filepath.Dir(repoRoot)
	}
	parent := os.Getenv("YOLO_DURABLE_DIR")
	if parent == "" {
		parent = t.TempDir()
	} else if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	scratch, err := os.MkdirTemp(parent, "green-build-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(scratch) })

	root := filepath.Join(scratch, "repo")
	out := filepath.Join(scratch, "output")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	var env []string
	goflags := ""
	for _, e := range os.Environ() {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "LAND_BATCH_LIMIT" || k == "YOLO_DURABLE_DIR" || k == "CGO_ENABLED" || k == "GOFLAGS" {
			if k == "GOFLAGS" {
				goflags = v
			}
			continue
		}
		env = append(env, e)
	}
	if goflags != "" {
		goflags += " -trimpath"
	} else {
		goflags = "-trimpath"
	}
	env = append(env, "YOLO_DURABLE_DIR="+out, "CGO_ENABLED=0", "GOFLAGS="+goflags)

	justBytes, err := os.ReadFile(filepath.Join(repoRoot, ".justfile"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".justfile"), justBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	f := &greenBuildFixture{
		t:        t,
		repoRoot: repoRoot,
		scratch:  scratch,
		root:     root,
		out:      out,
		env:      env,
	}
	f.git("init", "-q", "-b", "main")
	buildGoBytes, err := os.ReadFile(filepath.Join(repoRoot, "tools", "tillr-drain", "drain", "build.go"))
	if err != nil {
		t.Fatal(err)
	}
	tillrDir := filepath.Join(root, "tools", "tillr")
	buildstampDir := filepath.Join(tillrDir, "buildstamp")
	if err := os.MkdirAll(buildstampDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tillrDir, "go.mod"), []byte("module github.com/mschulkind-oss/tillr\n\ngo 1.23\n"), 0o644)
	for _, name := range []string{"build.go", "support-revision"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, "tools", "tillr", "buildstamp", name))
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(buildstampDir, name), data, 0o644)
	}
	for _, mod := range buildModules {
		modDir := filepath.Join(root, "tools", mod)
		if err := os.MkdirAll(modDir, 0o755); err != nil {
			t.Fatal(err)
		}
		goMod := "module github.com/mschulkind-oss/" + mod + "\n\ngo 1.23\n"
		if mod == "tillr-drain" {
			goMod += "\nrequire github.com/mschulkind-oss/tillr v0.0.0\n\nreplace github.com/mschulkind-oss/tillr => ../tillr\n"
		}
		_ = os.WriteFile(filepath.Join(modDir, "go.mod"), []byte(goMod), 0o644)
		extra := ""
		stamp := `""`
		if mod == "tillr-drain" {
			drainDir := filepath.Join(modDir, "drain")
			_ = os.MkdirAll(drainDir, 0o755)
			_ = os.WriteFile(filepath.Join(drainDir, "build.go"), buildGoBytes, 0o644)
			extra = `; "github.com/mschulkind-oss/tillr-drain/drain"`
			stamp = "drain.ReadBuildStamp().String()"
		}
		mainSrc := "package main\nimport (\"fmt\"; \"os\"" + extra + ")\n" +
			"func main() { if len(os.Args)>1 && os.Args[1]==\"loop\" { " +
			"fmt.Println(\"loop: build\", " + stamp + "); return }; fmt.Println(\"older\") }\n"
		_ = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(mainSrc), 0o644)
	}
	f.git("add", ".")
	f.git("commit", "-qm", "older programs")
	f.green = f.git("rev-parse", "HEAD")
	return f
}

func (f *greenBuildFixture) git(args ...string) string {
	f.t.Helper()
	all := append([]string{"-C", f.root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid"}, args...)
	cmd := exec.Command("git", all...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *greenBuildFixture) build(extraEnv ...string) (int, string) {
	f.t.Helper()
	cmd := exec.Command("just", "tillr-build")
	cmd.Dir = f.root
	cmd.Env = append(append([]string{}, f.env...), extraEnv...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			f.t.Fatalf("just tillr-build: %v", err)
		}
	}
	return code, string(out)
}

func TestBuildUsesGreenNotTipOrDirtySources(t *testing.T) {
	t.Parallel()
	f := newGreenBuildFixture(t)
	f.git("update-ref", "refs/tillr/green", f.green)
	for _, mod := range buildModules {
		p := filepath.Join(f.root, "tools", mod, "main.go")
		raw, _ := os.ReadFile(p)
		_ = os.WriteFile(p, bytes.ReplaceAll(raw, []byte("older"), []byte("newer")), 0o644)
	}
	f.git("add", ".")
	f.git("commit", "-qm", "newer programs")
	for _, mod := range buildModules {
		p := filepath.Join(f.root, "tools", mod, "main.go")
		raw, _ := os.ReadFile(p)
		_ = os.WriteFile(p, bytes.ReplaceAll(raw, []byte("newer"), []byte("dirty")), 0o644)
	}
	before := f.git("status", "--porcelain")
	tip := f.git("rev-parse", "HEAD")
	code, out := f.build()
	if code != 0 {
		t.Fatalf("build failed (%d): %s", code, out)
	}
	if f.git("status", "--porcelain") != before || f.git("rev-parse", "HEAD") != tip || f.git("rev-parse", "refs/tillr/green") != f.green {
		t.Fatal("repo state changed")
	}
	for _, mod := range buildModules {
		name := mod
		if mod == "tillr-cli" {
			name = "tillr"
		}
		got, err := exec.Command(filepath.Join(f.out, "bin", name)).Output()
		if err != nil || strings.TrimSpace(string(got)) != "older" {
			t.Fatalf("%s: got %q err=%v", name, got, err)
		}
	}
	drainOut, err := exec.Command(filepath.Join(f.out, "bin", "tillr-drain"), "loop").Output()
	if err != nil {
		t.Fatal(err)
	}
	first := strings.Split(string(drainOut), "\n")[0]
	if !strings.HasPrefix(first, "loop: build ") || !strings.Contains(first, "green "+f.green) {
		t.Fatalf("unexpected stamp: %q", first)
	}
	if matches, _ := filepath.Glob(filepath.Join(f.out, "tillr-build-*")); len(matches) != 0 {
		t.Fatalf("uncleaned tillr-build dirs: %v", matches)
	}
}

func TestMissingPointerBuildsNothingAndNamesHeavyRun(t *testing.T) {
	t.Parallel()
	f := newGreenBuildFixture(t)
	code, out := f.build()
	if code == 0 {
		t.Fatalf("expected failure, got:\n%s", out)
	}
	for _, want := range []string{"refs/tillr/green", "heavy run", "tillr drain loop", "TILLR_BUILD_UNPROVED=<commit>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if _, err := os.Stat(f.out); !os.IsNotExist(err) {
		t.Fatalf("output dir should not exist: %v", err)
	}
}

func TestUnprovedBootstrapBuildsNamedCommitAndSaysSo(t *testing.T) {
	t.Parallel()
	f := newGreenBuildFixture(t)
	code, out := f.build("TILLR_BUILD_UNPROVED=" + f.green)
	if code != 0 {
		t.Fatalf("unproved build failed (%d): %s", code, out)
	}
	if !strings.Contains(out, "UNPROVED build of "+f.green) || !strings.Contains(out, "refs/tillr/green is absent") || strings.Contains(out, "tillr-build: green ") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if err := exec.Command("git", "-C", f.root, "rev-parse", "--verify", "--quiet", "refs/tillr/green").Run(); err == nil {
		t.Fatal("bootstrap created refs/tillr/green")
	}
	drainOut, err := exec.Command(filepath.Join(f.out, "bin", "tillr-drain"), "loop").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Split(string(drainOut), "\n")[0], "green unknown") {
		t.Fatalf("unexpected stamp: %s", drainOut)
	}
	info, err := exec.Command("go", "version", "-m", filepath.Join(f.out, "bin", "tillr-drain")).Output()
	if err != nil || !strings.Contains(string(info), "vcs.revision="+f.green) {
		t.Fatalf("go version -m: %v\n%s", err, info)
	}
	badCode, badOut := f.build("TILLR_BUILD_UNPROVED=no-such-commit")
	if badCode == 0 || !strings.Contains(badOut, "TILLR_BUILD_UNPROVED=no-such-commit names no commit") {
		t.Fatalf("expected refusal, got code=%d out=%s", badCode, badOut)
	}
}

func TestFailedBuildPreservesExistingPrograms(t *testing.T) {
	t.Parallel()
	f := newGreenBuildFixture(t)
	_ = os.WriteFile(filepath.Join(f.root, "tools", "tillr-view", "main.go"), []byte("not valid Go\n"), 0o644)
	f.git("add", ".")
	f.git("commit", "-qm", "broken view")
	f.git("update-ref", "refs/tillr/green", f.git("rev-parse", "HEAD"))
	binDir := filepath.Join(f.out, "bin")
	_ = os.MkdirAll(binDir, 0o755)
	for _, mod := range buildModules {
		name := mod
		if mod == "tillr-cli" {
			name = "tillr"
		}
		_ = os.WriteFile(filepath.Join(binDir, name), []byte("previous install"), 0o644)
	}
	code, out := f.build()
	if code == 0 || !strings.Contains(out, "expected 'package'") {
		t.Fatalf("expected compile error, got code=%d out=%s", code, out)
	}
	entries, _ := os.ReadDir(binDir)
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(binDir, e.Name()))
		if string(raw) != "previous install" {
			t.Fatalf("%s overwritten", e.Name())
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(f.out, "tillr-build-*")); len(matches) != 0 {
		t.Fatalf("uncleaned tillr-build dirs: %v", matches)
	}
}

func TestLegacyGreenPreservesVCSAndPlannerGuard(t *testing.T) {
	t.Parallel()
	f := newGreenBuildFixture(t)
	_ = os.RemoveAll(filepath.Join(f.root, "tools"))
	archiveCmd := exec.Command("git", "-C", f.repoRoot, "archive", "HEAD", "tools")
	archive, err := archiveCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	untar := exec.Command("tar", "-x", "-C", f.root)
	untar.Stdin = bytes.NewReader(archive)
	if out, err := untar.CombinedOutput(); err != nil {
		t.Fatalf("untar: %v\n%s", err, out)
	}
	legacyPath := filepath.Join(f.root, "tools", "tillr-drain", "drain", "build.go")
	legacyRaw, _ := os.ReadFile(legacyPath)
	updated := strings.ReplaceAll(string(legacyRaw), "var greenRevision string", "")
	updated = strings.ReplaceAll(updated, "greenRevision", `""`)
	_ = os.WriteFile(legacyPath, []byte(updated), 0o644)
	f.git("add", ".")
	f.git("commit", "-qm", "legacy programs")
	green := f.git("rev-parse", "HEAD")
	f.git("update-ref", "refs/tillr/green", green)

	viewPath := filepath.Join(f.root, "tools", "tillr-view", "main.go")
	viewRaw, _ := os.ReadFile(viewPath)
	_ = os.WriteFile(viewPath, bytes.ReplaceAll(viewRaw, []byte(`const version = "0.1.0"`), []byte(`const version = "999.0.0"`)), 0o644)
	f.git("add", ".")
	f.git("commit", "-qm", "newer view version")

	code, out := f.build()
	if code != 0 {
		t.Fatalf("legacy build failed (%d): %s", code, out)
	}
	for _, mod := range buildModules {
		name := mod
		if mod == "tillr-cli" {
			name = "tillr"
		}
		info, err := exec.Command("go", "version", "-m", filepath.Join(f.out, "bin", name)).Output()
		if err != nil || !strings.Contains(string(info), "vcs.revision="+green) || !strings.Contains(string(info), "vcs.modified=false") {
			t.Fatalf("%s version -m: %v\n%s", name, err, info)
		}
	}
	storePath := filepath.Join(f.scratch, "legacy-store")
	initCmd := exec.Command(filepath.Join(f.out, "bin", "tillr-store"), "init", "--store", storePath)
	initCmd.Env = f.env
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	whyCmd := exec.Command(filepath.Join(f.out, "bin", "tillr-drain"), "why", "--store", storePath, "--root", f.root, "--json")
	whyCmd.Env = f.env
	whyRaw, err := whyCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var whyParsed struct {
		Build struct {
			Revision string `json:"revision"`
		} `json:"build"`
	}
	_ = json.Unmarshal(whyRaw, &whyParsed)
	if whyParsed.Build.Revision != green {
		t.Fatalf("why revision = %q, want %q", whyParsed.Build.Revision, green)
	}

	planner := filepath.Join(f.scratch, "wrong-planner")
	ans, _ := json.Marshal(map[string]any{
		"rows":    []any{},
		"summary": map[string]any{},
		"build":   map[string]any{"revision": f.git("rev-parse", "HEAD"), "modified": false, "digest": "wrong"},
	})
	_ = os.WriteFile(planner, []byte("#!/bin/sh\nprintf '%s\\n' '"+string(ans)+"'\n"), 0o755)
	waitingCmd := exec.Command(filepath.Join(f.out, "bin", "tillr-view"), "waiting", "--store", storePath, "--root", f.root, "--tillr-drain", planner)
	waitingCmd.Env = f.env
	waitingOut, err := waitingCmd.CombinedOutput()
	if err == nil || !strings.Contains(string(waitingOut), "another build's rules") {
		t.Fatalf("expected planner guard refusal, got err=%v out=%s", err, waitingOut)
	}

	statusCmd := exec.Command(filepath.Join(f.out, "bin", "tillr-view"), "status", "--store", storePath, "--root", f.root)
	statusCmd.Env = f.env
	statusRaw, err := statusCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Producer struct {
			Version string `json:"version"`
		} `json:"producer"`
		Payload struct {
			Heavy struct {
				AtGreen bool `json:"at_green"`
			} `json:"heavy"`
		} `json:"payload"`
	}
	_ = json.Unmarshal(statusRaw, &st)
	if st.Producer.Version != "0.1.0" || st.Payload.Heavy.AtGreen {
		t.Fatalf("unexpected status: %s", statusRaw)
	}
}

func TestStatusReportsWhetherCheckoutIsGreen(t *testing.T) {
	t.Parallel()
	f := newGreenBuildFixture(t)
	f.git("update-ref", "refs/tillr/green", f.green)
	view := filepath.Join(f.scratch, "tillr-view")
	buildView := exec.Command("go", "build", "-o", view, ".")
	buildView.Dir = filepath.Join(f.repoRoot, "tools", "tillr-view")
	buildView.Env = f.env
	if out, err := buildView.CombinedOutput(); err != nil {
		t.Fatalf("build tillr-view: %v\n%s", err, out)
	}
	storePath := filepath.Join(f.scratch, "store")
	initCmd := exec.Command("go", "run", ".", "init", "--store", storePath)
	initCmd.Dir = filepath.Join(f.repoRoot, "tools", "tillr-store")
	initCmd.Env = f.env
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("init store: %v\n%s", err, out)
	}
	readStatus := func() (bool, string) {
		cmd := exec.Command(view, "status", "--store", storePath, "--root", f.root)
		cmd.Env = f.env
		raw, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		var st struct {
			Payload struct {
				Heavy struct {
					AtGreen     bool   `json:"at_green"`
					GreenCommit string `json:"green_commit"`
				} `json:"heavy"`
			} `json:"payload"`
		}
		_ = json.Unmarshal(raw, &st)
		return st.Payload.Heavy.AtGreen, st.Payload.Heavy.GreenCommit
	}
	if atGreen, _ := readStatus(); !atGreen {
		t.Fatal("expected at_green=true")
	}
	_ = os.WriteFile(filepath.Join(f.root, "new-tip"), []byte("unproved"), 0o644)
	f.git("add", "new-tip")
	f.git("commit", "-qm", "unproved tip")
	if atGreen, gc := readStatus(); atGreen || gc != f.green {
		t.Fatalf("expected at_green=false green_commit=%q, got %v %q", f.green, atGreen, gc)
	}
}
