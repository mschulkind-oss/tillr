package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	repoRootOnce sync.Once
	repoRootPath string
	identities   [2][2]string
)

func loadTestContext(t *testing.T) (string, [2][2]string) {
	t.Helper()
	repoRootOnce.Do(func() {
		wd, err := os.Getwd()
		if err != nil {
			panic(err)
		}
		repoRootPath = filepath.Clean(filepath.Join(wd, "..", ".."))
		raw, err := os.ReadFile(filepath.Join(repoRootPath, "tools", "gate", "allowed-commit-identities.json"))
		if err != nil {
			panic(err)
		}
		var pairs [][]string
		if err := json.Unmarshal(raw, &pairs); err != nil {
			panic(err)
		}
		identities[0] = [2]string{pairs[0][0], pairs[0][1]}
		identities[1] = [2]string{pairs[1][0], pairs[1][1]}
	})
	return repoRootPath, identities
}

type changeFixture struct {
	t         *testing.T
	repo      string
	base      string
	bin       string
	env       []string
	envMap    map[string]string
	gateBin   string
	renderLog string
}

func newChangeFixture(t *testing.T, withStubUVX bool) *changeFixture {
	t.Helper()
	root, id := loadTestContext(t)
	personal := id[0]

	repo := t.TempDir()
	bin := filepath.Join(repo, "stubs")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}

	gateBin := filepath.Join(t.TempDir(), "repo-gate")
	build := exec.Command("go", "build", "-o", gateBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}

	envMap := make(map[string]string)
	for _, e := range os.Environ() {
		k, v, ok := strings.Cut(e, "=")
		if ok {
			envMap[k] = v
		}
	}
	envMap["GIT_CONFIG_COUNT"] = "2"
	envMap["GIT_CONFIG_KEY_0"] = "user.name"
	envMap["GIT_CONFIG_VALUE_0"] = personal[0]
	envMap["GIT_CONFIG_KEY_1"] = "user.email"
	envMap["GIT_CONFIG_VALUE_1"] = personal[1]
	renderLog := filepath.Join(t.TempDir(), "render.log")
	envMap["RENDER_LOG"] = renderLog

	if withStubUVX {
		uvx := filepath.Join(bin, "uvx")
		uvxScript := `#!/usr/bin/env python3
import json, os, sys
env = os.environ.get
args = sys.argv[2:]
if args == ["version"]:
    print("vantage-check " + env("RENDER_VERSION", "0.0.0"))
    sys.exit(int(env("RENDER_VERSION_RC", "0")))
files = [arg for arg in args if arg not in ("check", "--format", "json")]
with open(env("RENDER_LOG"), "a") as log:
    log.writelines("vantage-check " + name + "\n" for name in files)
rc = int(env("RENDER_RC", "0"))
sys.stderr.write(env("RENDER_ERR", ""))
if rc == 127:
    sys.exit(rc)
failing = env("RENDER_FAIL", "").split(",") if env("RENDER_FAIL") else files
spell = env("RENDER_FINDING_FILE", "{}")
findings = [{"rule": "stub/finding", "severity": "error", "message": env("RENDER_FINDING"),
             "file": spell.format(name), "line": 1, "column": 1} for name in failing if env("RENDER_FINDING")]
failures = [{"file": name, "message": "stub"} for name in env("RENDER_UNCHECKED", "").split(",") if name]
print(json.dumps({"tool": "vantage-check", "version": env("RENDER_VERSION", "0.0.0"),
                  "findings": findings, "failures": failures}))
sys.exit(rc)
`
		if err := os.WriteFile(uvx, []byte(uvxScript), 0o755); err != nil {
			t.Fatal(err)
		}
		envMap["PATH"] = bin + string(os.PathListSeparator) + envMap["PATH"]
	}

	f := &changeFixture{
		t:         t,
		repo:      repo,
		bin:       bin,
		envMap:    envMap,
		gateBin:   gateBin,
		renderLog: renderLog,
	}
	f.rebuildEnv()

	f.git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# Fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	allowedDst := filepath.Join(repo, "tools", "gate", "allowed-commit-identities.json")
	if err := os.MkdirAll(filepath.Dir(allowedDst), 0o755); err != nil {
		t.Fatal(err)
	}
	allowedSrc, err := os.ReadFile(filepath.Join(root, "tools", "gate", "allowed-commit-identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(allowedDst, allowedSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	f.base = f.commit()
	return f
}

func (f *changeFixture) rebuildEnv() {
	f.env = make([]string, 0, len(f.envMap))
	for k, v := range f.envMap {
		f.env = append(f.env, k+"="+v)
	}
}

func (f *changeFixture) setEnv(k, v string) {
	f.envMap[k] = v
	f.rebuildEnv()
}

func (f *changeFixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = f.repo
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *changeFixture) commit() string {
	f.t.Helper()
	f.git("add", "-A")
	f.git("commit", "-qm", "chore: fixture")
	return f.git("rev-parse", "HEAD")
}

func (f *changeFixture) check(kind, base, head string) (int, string, string) {
	f.t.Helper()
	if base == "" {
		base = f.base
	}
	if head == "" {
		head = "HEAD"
	}
	cmd := exec.Command(f.gateBin, kind, base, head)
	cmd.Dir = f.repo
	cmd.Env = f.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			f.t.Fatalf("run %s: %v", kind, err)
		}
	}
	return code, stdout.String(), stderr.String()
}

func (f *changeFixture) roadmap(text string) string {
	f.t.Helper()
	p := filepath.Join(f.repo, "docs", "tillr", "roadmap.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return f.commit()
}

func (f *changeFixture) renders() []string {
	data, err := os.ReadFile(f.renderLog)
	if err != nil {
		return nil
	}
	return splitLines(string(data))
}

func TestDocumentsRoadmapRemembersUnchangedSectionsButNotChangedHeadings(t *testing.T) {
	f := newChangeFixture(t, true)
	text := "# Roadmap\n\n## First\n\nOne.\n\n## Second\n\nTwo.\n"
	f.roadmap(text)
	if code, out, errOut := f.check("documents", "", ""); code != 0 {
		t.Fatalf("first: code=%d out=%s err=%s", code, out, errOut)
	}
	if got := len(f.renders()); got != 3 {
		t.Fatalf("expected 3 renders, got %d", got)
	}
	f.roadmap(strings.ReplaceAll(text, "Two.", "Changed."))
	if code, out, errOut := f.check("documents", "", ""); code != 0 {
		t.Fatalf("second: code=%d out=%s err=%s", code, out, errOut)
	}
	if got := len(f.renders()); got != 4 {
		t.Fatalf("expected 4 renders, got %d", got)
	}
	f.roadmap(strings.ReplaceAll(text, "## Second", "## Renamed"))
	if code, out, errOut := f.check("documents", "", ""); code != 0 {
		t.Fatalf("third: code=%d out=%s err=%s", code, out, errOut)
	}
	if got := len(f.renders()); got != 7 {
		t.Fatalf("expected 7 renders, got %d", got)
	}
	matches, _ := filepath.Glob(filepath.Join(f.repo, "docs", "tillr", ".gate-roadmap-*"))
	if len(matches) != 0 {
		t.Fatalf("leftover temp files: %v", matches)
	}
}

func TestDocumentsRoadmapCrossSectionMarkupGetsFullRenderer(t *testing.T) {
	f := newChangeFixture(t, true)
	f.roadmap("# Roadmap\n\n## First\n\n[shared][ref]\n\n## Second\n\n[ref]: target.md\n")
	code, _, errOut := f.check("documents", "", "")
	if code != 0 {
		t.Fatalf("code=%d err=%s", code, errOut)
	}
	if !strings.Contains(errOut, "checking the full file") {
		t.Fatalf("expected 'checking the full file', got %q", errOut)
	}
	renders := f.renders()
	if len(renders) != 1 || renders[0] != "vantage-check docs/tillr/roadmap.md" {
		t.Fatalf("renders = %v", renders)
	}
}

func TestDocumentsRealRendererCatchesBrokenRoadmapLinkAndChangedTarget(t *testing.T) {
	f := newChangeFixture(t, false)
	target := filepath.Join(f.repo, "docs", "tillr", "target.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# Target\n\n## Live\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text := "# Roadmap\n\n## First\n\n[Target](target.md#live).\n\n## Second\n\nTwo.\n"
	f.roadmap(text)
	if code, out, errOut := f.check("documents", "", ""); code != 0 {
		t.Fatalf("first: code=%d out=%s err=%s", code, out, errOut)
	}
	f.roadmap(strings.ReplaceAll(text, "Two.", "[Broken](missing-roadmap-target.md)."))
	code, out, errOut := f.check("documents", "", "")
	if code != 1 || !strings.Contains(out, "link/missing-target") || !strings.Contains(out, "docs/tillr/roadmap.md:9:") {
		t.Fatalf("broken: code=%d out=%s err=%s", code, out, errOut)
	}
	f.roadmap(text)
	if err := os.WriteFile(target, []byte("# Target\n\n## Renamed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.roadmap(text)
	code, out, errOut = f.check("documents", "", "")
	if code != 1 || !strings.Contains(out, "link/dead-section-anchor") {
		t.Fatalf("renamed: code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestDocumentsRoadmapDoesNotRememberMutableSymlinkTarget(t *testing.T) {
	f := newChangeFixture(t, false)
	target := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(target, []byte("# Target\n\n## Live\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(f.repo, "docs", "tillr", "linked.md")
	if err := os.MkdirAll(filepath.Dir(linked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, linked); err != nil {
		t.Fatal(err)
	}
	base := f.commit()
	text := "# Roadmap\n\n## First\n\n[Target](linked.md#live).\n\n## Second\n\nTwo.\n"
	f.roadmap(text)
	if code, out, errOut := f.check("documents", base, ""); code != 0 {
		t.Fatalf("first: code=%d out=%s err=%s", code, out, errOut)
	}
	if err := os.WriteFile(target, []byte("# Target\n\n## Removed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.roadmap(strings.ReplaceAll(text, "Two.", "Changed."))
	code, out, errOut := f.check("documents", base, "")
	if code != 1 || !strings.Contains(out, "link/dead-section-anchor") {
		t.Fatalf("symlink changed: code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestDocumentsRealRendererCatchesHeaderOnlyInvalidFrontmatter(t *testing.T) {
	f := newChangeFixture(t, false)
	f.roadmap("---\ntitle: [\n---\n")
	code, out, errOut := f.check("documents", "", "")
	if code != 1 || !strings.Contains(out, "frontmatter/parse") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestDocumentsRealRendererInvalidatesIndentedAndTabbedHeadingLinks(t *testing.T) {
	f := newChangeFixture(t, false)
	for _, prefix := range []string{"  ### ", "###\t"} {
		text := "# Roadmap\n\n## First\n\n[Hidden](#hidden).\n\n## Second\n\n" + prefix + "Hidden\n"
		f.roadmap(text)
		if code, out, errOut := f.check("documents", "", ""); code != 0 {
			t.Fatalf("prefix %q first: code=%d out=%s err=%s", prefix, code, out, errOut)
		}
		f.roadmap(strings.ReplaceAll(text, prefix+"Hidden", prefix+"Renamed"))
		code, out, errOut := f.check("documents", "", "")
		if code != 1 || !strings.Contains(out, "link/dead-section-anchor") {
			t.Fatalf("prefix %q renamed: code=%d out=%s err=%s", prefix, code, out, errOut)
		}
	}
}

func TestDocumentsRealRendererDoesNotSplitCrossSectionMath(t *testing.T) {
	f := newChangeFixture(t, false)
	f.roadmap("# Roadmap\n\n## First\n\n$$\nx\n## Second\ny\n$$\n")
	code, out, errOut := f.check("documents", "", "")
	if code != 1 || !strings.Contains(out, "katex/parse") || !strings.Contains(errOut, "checking the full file") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestDocumentsRealRendererInvalidatesSelfLineLinksWhenPageLengthChanges(t *testing.T) {
	f := newChangeFixture(t, false)
	text := "# Roadmap\n\n## First\n\n[Tail](#L25).\n\n## Second\n\n" + strings.Repeat("Padding.\n\n", 15)
	f.roadmap(text)
	if code, out, errOut := f.check("documents", "", ""); code != 0 {
		t.Fatalf("first: code=%d out=%s err=%s", code, out, errOut)
	}
	f.roadmap(strings.ReplaceAll(text, strings.Repeat("Padding.\n\n", 15), "Short.\n"))
	code, out, errOut := f.check("documents", "", "")
	if code != 1 || !strings.Contains(out, "link/line-anchor-range") {
		t.Fatalf("short: code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestDocumentsRealRendererInvalidatesHeaderOnlyDependencyTargets(t *testing.T) {
	f := newChangeFixture(t, false)
	target := filepath.Join(f.repo, "docs", "design", "target.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# Target\n\n<!-- vantage: question id=OQ-X1 -->\n\n## Choice\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := f.commit()
	text := "---\nstatus: draft\nstage: DESIGN\ndepends-on: [../design/target.md#OQ-X1]\n---\n\n# Roadmap\n\n## First\n\nOne.\n"
	f.roadmap(text)
	if code, out, errOut := f.check("documents", base, ""); code != 0 {
		t.Fatalf("first: code=%d out=%s err=%s", code, out, errOut)
	}
	if err := os.WriteFile(target, []byte("# Target\n\n## Removed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.roadmap(text)
	code, out, errOut := f.check("documents", base, "")
	if code != 1 || !strings.Contains(out, "planning/depends-on-missing") {
		t.Fatalf("removed: code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestChangeRequiresBaseAndAncestor(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Future\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := f.commit()
	for _, kind := range []string{"commit-identity", "citations", "documents", "file-size"} {
		code, out, errOut := f.check(kind, future, f.base)
		if code != 4 || !strings.Contains(errOut, "ancestor") {
			t.Fatalf("%s future->base: code=%d out=%s err=%s", kind, code, out, errOut)
		}
		cmd := exec.Command(f.gateBin, kind)
		cmd.Dir = f.repo
		cmd.Env = f.env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 4 || !strings.Contains(stderr.String(), "base") {
			t.Fatalf("%s missing args: err=%v stderr=%s", kind, err, stderr.String())
		}
	}
}

func TestFileSizeAddedRefusalAndTreeDebt(t *testing.T) {
	f := newChangeFixture(t, true)
	giant := filepath.Join(f.repo, "tools", "giant.ts")
	if err := os.WriteFile(giant, []byte(strings.Repeat("line\n", 3001)), 0o644); err != nil {
		t.Fatal(err)
	}
	head := f.commit()
	code, out, _ := f.check("file-size", "", "")
	if code != 1 || !strings.Contains(out, "3,001 lines added") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Fixture\n\nEdited.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	if code, out, errOut := f.check("file-size", head, ""); code != 0 {
		t.Fatalf("after: code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestCitationsMarkersFencesAndUpstreamPins(t *testing.T) {
	f := newChangeFixture(t, true)
	f.git("checkout", "-qb", "lane-only")
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	side := f.commit()
	f.git("checkout", "-q", "-")
	for _, tc := range []struct {
		text string
		rc   int
	}{
		{"Evidence " + side + "\n", 1},
		{"[unlanded] Evidence " + side + "\n", 0},
		{"```\nEvidence " + side + "\n```\n", 0},
		{"Evidence " + f.base + "\n", 0},
	} {
		if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Fixture\n\n"+tc.text), 0o644); err != nil {
			t.Fatal(err)
		}
		f.commit()
		code, out, errOut := f.check("citations", "", "")
		if code != tc.rc {
			t.Fatalf("%q: code=%d want %d out=%s err=%s", tc.text, code, tc.rc, out, errOut)
		}
	}
	f.git("update-ref", "refs/remotes/upstream/main", side)
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Fixture\n\nEvidence "+side+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	if code, out, errOut := f.check("citations", "", ""); code != 0 {
		t.Fatalf("upstream pin: code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestCommitIdentityPolicyForEachRole(t *testing.T) {
	_, id := loadTestContext(t)
	personal, professional := id[0], id[1]
	unrelated := [2]string{"Fixture", "fixture@example.invalid"}
	wrongName := [2]string{unrelated[0], personal[1]}
	wrongEmail := [2]string{professional[0], unrelated[1]}

	f := newChangeFixture(t, true)
	f.setEnv("GIT_CONFIG_VALUE_0", unrelated[0])
	f.setEnv("GIT_CONFIG_VALUE_1", unrelated[1])

	commitWith := func(author, committer [2]string) string {
		cmd := exec.Command("git", "commit", "-q", "--allow-empty", "-m", "chore: identity fixture")
		cmd.Dir = f.repo
		cmd.Env = append(append([]string(nil), f.env...),
			"GIT_AUTHOR_NAME="+author[0], "GIT_AUTHOR_EMAIL="+author[1],
			"GIT_COMMITTER_NAME="+committer[0], "GIT_COMMITTER_EMAIL="+committer[1],
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("commit: %v: %s", err, out)
		}
		return f.git("rev-parse", "HEAD")
	}

	cases := []struct {
		author, committer [2]string
		rejectedRole      string
	}{
		{personal, personal, ""},
		{professional, professional, ""},
		{personal, professional, ""},
		{professional, personal, ""},
		{unrelated, professional, "author"},
		{personal, unrelated, "committer"},
		{wrongName, personal, "author"},
		{professional, wrongName, "committer"},
		{wrongEmail, personal, "author"},
		{professional, wrongEmail, "committer"},
	}
	for _, tc := range cases {
		base := f.git("rev-parse", "HEAD")
		sha := commitWith(tc.author, tc.committer)
		code, out, errOut := f.check("commit-identity", base, sha)
		want := 0
		if tc.rejectedRole != "" {
			want = 1
		}
		if code != want {
			t.Fatalf("author=%v committer=%v: code=%d want=%d out=%s err=%s", tc.author, tc.committer, code, want, out, errOut)
		}
		if tc.rejectedRole != "" {
			if !strings.Contains(out, sha+": "+tc.rejectedRole+" ") {
				t.Fatalf("expected %s rejection in %q", tc.rejectedRole, out)
			}
			other := "committer"
			if tc.rejectedRole == "committer" {
				other = "author"
			}
			if strings.Contains(out, sha+": "+other+" ") {
				t.Fatalf("unexpected %s rejection in %q", other, out)
			}
		}
	}
}

func TestCommitIdentityLocalAndWorktreeOverridesRefused(t *testing.T) {
	_, id := loadTestContext(t)
	personal := id[0]
	f := newChangeFixture(t, true)
	f.git("config", "extensions.worktreeConfig", "true")
	f.git("commit", "-q", "--allow-empty", "-m", "chore: empty")
	for _, scope := range []string{"local", "worktree"} {
		for _, kv := range [][2]string{
			{"user.name", "Wrong"},
			{"user.name", personal[0]},
			{"user.email", personal[1]},
			{"user.email", ""},
		} {
			f.git("config", "--"+scope, kv[0], kv[1])
			code, out, errOut := f.check("commit-identity", "", "")
			if code != 1 || !strings.Contains(out, scope+" "+kv[0]+" overrides") {
				t.Fatalf("scope=%s key=%s val=%q: code=%d out=%s err=%s", scope, kv[0], kv[1], code, out, errOut)
			}
			f.git("config", "--"+scope, "--unset-all", kv[0])
		}
	}
}

func TestCommitIdentityUnreadableIsCouldNotJudge(t *testing.T) {
	f := newChangeFixture(t, true)
	actualGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	stubGit := filepath.Join(f.bin, "git")
	script := "#!/bin/sh\nif [ \"$1\" = \"config\" ] || [ \"$3\" = \"config\" ]; then echo \"cannot read config\" >&2; exit 2; fi\nexec " + actualGit + " \"$@\"\n"
	if err := os.WriteFile(stubGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := f.check("commit-identity", "", "")
	if code != 4 || !strings.Contains(errOut, "could not judge") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestDocumentsRendererFindingsAndUnavailableToolDistinct(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	for _, tc := range []struct {
		rc, finding, stderr string
		expected            int
	}{
		{"1", "missing document target", "", 1},
		{"1", "", "resolver failed\n", 4},
		{"3", "diagnostic", "", 4},
		{"127", "", "uvx missing\n", 4},
	} {
		f.setEnv("RENDER_RC", tc.rc)
		f.setEnv("RENDER_FINDING", tc.finding)
		f.setEnv("RENDER_ERR", tc.stderr)
		code, out, errOut := f.check("documents", "", "")
		if code != tc.expected {
			t.Fatalf("rc=%s: code=%d want=%d out=%s err=%s", tc.rc, code, tc.expected, out, errOut)
		}
	}
}

func TestDocumentsUnchangedPassIsRemembered(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.WriteFile(filepath.Join(f.repo, "other.md"), []byte("# Other\n\n## Target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Fixture\n\nSee [other](other.md#target).\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	code, out, errOut := f.check("documents", "", "")
	if code != 0 {
		t.Fatalf("first: code=%d out=%s err=%s", code, out, errOut)
	}
	if len(f.renders()) != 2 {
		t.Fatalf("renders = %v", f.renders())
	}
	code, out, errOut = f.check("documents", "", "")
	if code != 0 {
		t.Fatalf("second: code=%d out=%s err=%s", code, out, errOut)
	}
	if len(f.renders()) != 2 {
		t.Fatalf("remembered pass was rendered again: %v", f.renders())
	}
	if !strings.Contains(errOut, "documents: README.md: remembered pass at ") || !strings.Contains(errOut, "2 document(s): 0 rendered, 2 remembered") {
		t.Fatalf("stderr = %q", errOut)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "unrelated.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	if code, _, _ := f.check("documents", "", ""); code != 0 || len(f.renders()) != 2 {
		t.Fatalf("unrelated commit forgot remembered pass")
	}
}

func TestDocumentsChangedOrChangedTargetRenderedAgain(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.WriteFile(filepath.Join(f.repo, "other.md"), []byte("# Other\n\n## Target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Fixture\n\nSee [other](other.md#target).\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	if code, _, _ := f.check("documents", "", ""); code != 0 || len(f.renders()) != 2 {
		t.Fatalf("initial")
	}
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Fixture\n\nSee [other](other.md#target), again.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	if code, _, _ := f.check("documents", "", ""); code != 0 || len(f.renders()) != 3 {
		t.Fatalf("changed README: renders=%v", f.renders())
	}
	if err := os.WriteFile(filepath.Join(f.repo, "other.md"), []byte("# Other\n\n## Renamed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	if code, _, _ := f.check("documents", "", ""); code != 0 || len(f.renders()) != 5 {
		t.Fatalf("changed target: renders=%v", f.renders())
	}
}

func TestDocumentsRememberedFailNeverReusedAsPass(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Fixture\n\nBroken.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	f.setEnv("RENDER_RC", "1")
	f.setEnv("RENDER_FINDING", "missing target")
	if code, _, _ := f.check("documents", "", ""); code != 1 {
		t.Fatalf("expected fail")
	}
	f.setEnv("RENDER_RC", "0")
	f.setEnv("RENDER_FINDING", "")
	for i := 0; i < 2; i++ {
		code, _, errOut := f.check("documents", "", "")
		if code != 0 || strings.Contains(errOut, "remembered") {
			t.Fatalf("attempt %d: code=%d err=%s", i, code, errOut)
		}
	}
	if len(f.renders()) != 3 {
		t.Fatalf("renders=%v", f.renders())
	}
}

func TestDocumentsPassFromAnotherRendererVersionJudgedAgain(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Fixture\n\nEdited.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	f.setEnv("RENDER_VERSION", "1.0.0")
	if code, _, _ := f.check("documents", "", ""); code != 0 {
		t.Fatalf("first")
	}
	if code, _, _ := f.check("documents", "", ""); code != 0 || len(f.renders()) != 1 {
		t.Fatalf("second: renders=%v", f.renders())
	}
	f.setEnv("RENDER_VERSION", "2.0.0")
	code, _, errOut := f.check("documents", "", "")
	if code != 0 || !strings.Contains(errOut, "not from renderer 2.0.0") || len(f.renders()) != 2 {
		t.Fatalf("version change: code=%d err=%s renders=%v", code, errOut, f.renders())
	}
	f.setEnv("RENDER_VERSION_RC", "3")
	if code, _, _ := f.check("documents", "", ""); code != 0 || len(f.renders()) != 3 {
		t.Fatalf("unknown version: renders=%v", f.renders())
	}
}

func TestDocumentsReportWithoutVersionRemembersNothing(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Fixture\n\nEdited.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	f.setEnv("RENDER_VERSION", "")
	for i := 0; i < 2; i++ {
		if code, _, _ := f.check("documents", "", ""); code != 0 {
			t.Fatalf("attempt %d", i)
		}
	}
	if len(f.renders()) != 2 {
		t.Fatalf("renders=%v", f.renders())
	}
}

func TestDocumentsFailingRunRemembersOnlyWhatItJudged(t *testing.T) {
	f := newChangeFixture(t, true)
	for name, body := range map[string]string{
		"other.md":  "# Other\n",
		"third.md":  "# Third\n",
		"README.md": "# Fixture\n\nEdited.\n",
	} {
		if err := os.WriteFile(filepath.Join(f.repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.commit()
	f.setEnv("RENDER_RC", "1")
	f.setEnv("RENDER_FINDING", "finding")
	f.setEnv("RENDER_FAIL", "README.md")
	f.setEnv("RENDER_UNCHECKED", "other.md")
	code, out, _ := f.check("documents", "", "")
	if code != 1 || !strings.Contains(out, "could not check") || len(f.renders()) != 3 {
		t.Fatalf("first: code=%d out=%s renders=%v", code, out, f.renders())
	}
	f.setEnv("RENDER_RC", "0")
	f.setEnv("RENDER_FINDING", "")
	f.setEnv("RENDER_FAIL", "")
	f.setEnv("RENDER_UNCHECKED", "")
	code, _, errOut := f.check("documents", "", "")
	if code != 0 || len(f.renders()) != 5 || !strings.Contains(errOut, "documents: third.md: remembered pass at ") {
		t.Fatalf("second: code=%d err=%s renders=%v", code, errOut, f.renders())
	}
}

func TestDocumentsFailingRunAttributesFindingsBeforeRemembering(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Fixture\n\nEdited.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	for _, spelling := range []string{"./{}", "elsewhere.md"} {
		_ = os.RemoveAll(filepath.Join(f.repo, ".git", "tillr-gate"))
		f.setEnv("RENDER_RC", "1")
		f.setEnv("RENDER_FINDING", "finding")
		f.setEnv("RENDER_FINDING_FILE", spelling)
		before := len(f.renders())
		for attempt := 1; attempt <= 2; attempt++ {
			code, _, errOut := f.check("documents", "", "")
			if code != 1 || strings.Contains(errOut, "remembered pass") {
				t.Fatalf("spelling %s attempt %d: code=%d err=%s", spelling, attempt, code, errOut)
			}
		}
		if len(f.renders())-before != 2 {
			t.Fatalf("spelling %s: expected 2 renders", spelling)
		}
	}
}

func TestDocumentsPassRestingOnWorkingTreeNotRemembered(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.WriteFile(filepath.Join(f.repo, ".gitignore"), []byte("scratch/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "docs", "b.md"), []byte("# B\n\n## Yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := f.commit()
	if err := os.WriteFile(filepath.Join(f.repo, "docs", "a.md"), []byte("# A\n\nSee [b](b.md#yes) and [n](../scratch/n.md).\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()

	cases := []struct {
		name string
		make func()
		undo func()
	}{
		{
			"ignored file on disk",
			func() {
				_ = os.MkdirAll(filepath.Join(f.repo, "scratch"), 0o755)
				_ = os.WriteFile(filepath.Join(f.repo, "scratch", "n.md"), []byte("# N\n"), 0o644)
			},
			func() { _ = os.RemoveAll(filepath.Join(f.repo, "scratch")) },
		},
		{
			"tracked file edited on disk",
			func() { _ = os.WriteFile(filepath.Join(f.repo, "docs", "b.md"), []byte("# B\n"), 0o644) },
			func() { f.git("checkout", "--", "docs/b.md") },
		},
		{
			"vantage config on disk",
			func() { _ = os.WriteFile(filepath.Join(f.repo, "docs", ".vantage.toml"), []byte("[rules]\n"), 0o644) },
			func() { _ = os.Remove(filepath.Join(f.repo, "docs", ".vantage.toml")) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.RemoveAll(filepath.Join(f.repo, ".git", "tillr-gate"))
			tc.make()
			before := len(f.renders())
			if code, _, _ := f.check("documents", base, ""); code != 0 {
				t.Fatalf("dirty run failed")
			}
			tc.undo()
			code, _, errOut := f.check("documents", base, "")
			if code != 0 || strings.Contains(errOut, "remembered pass") || len(f.renders())-before != 2 {
				t.Fatalf("clean run after dirty: code=%d err=%s renders=%v", code, errOut, f.renders())
			}
			if _, _, errOut := f.check("documents", base, ""); !strings.Contains(errOut, "remembered pass") {
				t.Fatalf("expected remembered pass on third run, got %q", errOut)
			}
		})
	}
}

func buildTillrGateForTest(t *testing.T, root string) string {
	t.Helper()
	runner := filepath.Join(t.TempDir(), "tillr-gate")
	cmd := exec.Command("go", "build", "-o", runner, ".")
	cmd.Dir = filepath.Join(root, "tools", "tillr-gate")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build tillr-gate: %v: %s", err, out)
	}
	return runner
}

func copyChecksAndStubTree(t *testing.T, root, repo, gateBin string) {
	t.Helper()
	srcChecks := filepath.Join(root, "tools", "gate", "checks")
	dstChecks := filepath.Join(repo, "tools", "gate", "checks")
	if err := os.MkdirAll(dstChecks, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(srcChecks)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(srcChecks, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var decl map[string]any
		if err := json.Unmarshal(raw, &decl); err != nil {
			t.Fatal(err)
		}
		if decl["scope"] == "tree" || decl["id"] == "go-affected" || decl["id"] == "go-module-inventory" ||
			decl["id"] == "gate-adapters-test" || decl["id"] == "change-checks-test" {
			decl["command"] = []string{"true"}
			decl["remember"] = false
		}
		if decl["scope"] == "change" {
			decl["tier"] = "heavy"
			decl["timeout_ms"] = 60000
			delete(decl, "budget_ms")
		}
		out, _ := json.Marshal(decl)
		if err := os.WriteFile(filepath.Join(dstChecks, e.Name()), out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Copy gateBin to tools/gate/repo-gate in repo so declarations resolve it.
	binBytes, err := os.ReadFile(gateBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "repo-gate"), binBytes, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestChangeMarkdownOnlyRemembersGoBuildWithoutStartingCompile(t *testing.T) {
	root, _ := loadTestContext(t)
	runner := buildTillrGateForTest(t, root)
	f := newChangeFixture(t, true)
	raw, err := os.ReadFile(filepath.Join(root, "tools", "gate", "checks", "go-build.json"))
	if err != nil {
		t.Fatal(err)
	}
	regDir := filepath.Join(f.repo, "build-check")
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(regDir, "go-build.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	binBytes, _ := os.ReadFile(f.gateBin)
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "gate", "repo-gate"), binBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range goToolDirs {
		mod := filepath.Join(f.repo, filepath.FromSlash(dir))
		if err := os.MkdirAll(mod, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fixture/"+filepath.Base(dir)+"\n\ngo 1.24\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mod, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	marker := filepath.Join(t.TempDir(), "compile.log")
	gorootOut, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	realGo := filepath.Join(strings.TrimSpace(string(gorootOut)), "bin", "go")
	goWrapper := filepath.Join(f.bin, "go")
	script := "#!/usr/bin/env python3\nimport os, sys\n" +
		"if len(sys.argv) > 1 and sys.argv[1] in ('list', 'build', 'test'):\n" +
		"    open(" + jsonQuote(marker) + ", 'a').write('compile\\n')\n" +
		"os.execv(" + jsonQuote(realGo) + ", [" + jsonQuote(realGo) + ", *sys.argv[1:]])\n"
	if err := os.WriteFile(goWrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	base := f.commit()
	logs := filepath.Join(t.TempDir(), "logs")
	runBuild := func() []map[string]any {
		cmd := exec.Command(runner, "checks", "--base", base, "--head", "HEAD", "--fast-tree", "--checks", regDir, "--logs", logs)
		cmd.Dir = f.repo
		cmd.Env = f.env
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("runner checks: %v: %s", err, stderr.String())
		}
		var env struct {
			Payload struct {
				Checks []map[string]any `json:"checks"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal %q: %v", stdout.String(), err)
		}
		return env.Payload.Checks
	}

	first := runBuild()
	if first[0]["memory"] != "passed" {
		t.Fatalf("expected memory=passed, got %v", first)
	}
	compilesBefore, _ := os.ReadFile(marker)
	if len(compilesBefore) == 0 {
		t.Fatalf("expected compile log")
	}

	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Markdown-only change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	second := runBuild()
	if len(second) != 1 || second[0]["memory"] != "remembered" {
		t.Fatalf("expected remembered, got %v", second)
	}
	compilesAfter, _ := os.ReadFile(marker)
	if string(compilesAfter) != string(compilesBefore) {
		t.Fatalf("remembered build started a compiler")
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestChangeWorktreeSelectsOwnDefaultDeclarations(t *testing.T) {
	root, _ := loadTestContext(t)
	runner := buildTillrGateForTest(t, root)
	f := newChangeFixture(t, true)
	copyChecksAndStubTree(t, root, f.repo, f.gateBin)
	base := f.commit()

	citPath := filepath.Join(f.repo, "tools", "gate", "checks", "citations.json")
	raw, _ := os.ReadFile(citPath)
	var decl map[string]any
	_ = json.Unmarshal(raw, &decl)
	decl["id"] = "fixture-citations"
	out, _ := json.Marshal(decl)
	_ = os.WriteFile(citPath, out, 0o644)

	cmd := exec.Command(runner, "checks", "--worktree", f.repo, "--base", base, "--head", "HEAD", "--logs", filepath.Join(f.repo, "logs"))
	cmd.Dir = root
	cmd.Env = f.env
	resOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("runner: %v: %s", err, resOut)
	}
	var env struct {
		Payload struct {
			Checks []struct {
				CheckID string `json:"check_id"`
			} `json:"checks"`
		} `json:"payload"`
	}
	_ = json.Unmarshal(resOut, &env)
	found := false
	for _, c := range env.Payload.Checks {
		if c.CheckID == "fixture-citations" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fixture-citations not selected: %+v", env.Payload.Checks)
	}
}

func TestChangeMarkdownRunsAllDeclaredChecksAgainstCallerBase(t *testing.T) {
	root, _ := loadTestContext(t)
	runner := buildTillrGateForTest(t, root)
	f := newChangeFixture(t, true)
	copyChecksAndStubTree(t, root, f.repo, f.gateBin)
	base := f.commit()

	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Fixture\n\nEdited.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := f.commit()

	logs := filepath.Join(f.repo, "logs")
	cmd := exec.Command(runner, "checks", "--base", base, "--head", head, "--logs", logs)
	cmd.Dir = f.repo
	cmd.Env = f.env
	resOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("runner: %v: %s", err, resOut)
	}
	var env struct {
		Payload struct {
			BaseSHA string           `json:"base_sha"`
			Checks  []map[string]any `json:"checks"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(resOut, &env); err != nil {
		t.Fatal(err)
	}
	if env.Payload.BaseSHA != base {
		t.Fatalf("base_sha=%q want=%q", env.Payload.BaseSHA, base)
	}
	expectedIDs := []string{"commit-identity", "citations", "documents", "file-size", "workflows", "elapsed-time"}
	judged := make(map[string]string)
	for _, c := range env.Payload.Checks {
		judged[c["check_id"].(string)] = c["result"].(string)
	}
	for _, id := range expectedIDs {
		if judged[id] != "pass" {
			t.Fatalf("check %s result=%q", id, judged[id])
		}
	}
	if renders := f.renders(); len(renders) != 1 || renders[0] != "vantage-check README.md" {
		t.Fatalf("renders=%v", renders)
	}
}

func TestChangeIdentityRangeMissingDeployedAndOutranks(t *testing.T) {
	root, id := loadTestContext(t)
	personal, professional := id[0], id[1]
	unrelated := [2]string{"Fixture", "fixture@example.invalid"}
	runner := buildTillrGateForTest(t, root)

	f := newChangeFixture(t, true)
	copyChecksAndStubTree(t, root, f.repo, f.gateBin)
	base := f.commit()

	commitWith := func(author, committer [2]string) string {
		cmd := exec.Command("git", "commit", "-q", "--allow-empty", "-m", "chore: identity fixture")
		cmd.Dir = f.repo
		cmd.Env = append(append([]string(nil), f.env...),
			"GIT_AUTHOR_NAME="+author[0], "GIT_AUTHOR_EMAIL="+author[1],
			"GIT_COMMITTER_NAME="+committer[0], "GIT_COMMITTER_EMAIL="+committer[1],
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("commit: %v: %s", err, out)
		}
		return f.git("rev-parse", "HEAD")
	}

	// Explicit range ignores commits outside base..head.
	explicitBase := commitWith(unrelated, unrelated)
	allowedHead := commitWith(personal, professional)
	commitWith(unrelated, unrelated)
	if code, out, errOut := f.check("commit-identity", explicitBase, allowedHead); code != 0 {
		t.Fatalf("explicit range: code=%d out=%s err=%s", code, out, errOut)
	}

	// Missing deployed identity is exit 4 / error, not rejection.
	f.setEnv("GIT_CONFIG_COUNT", "0")
	f.setEnv("GIT_CONFIG_GLOBAL", "/dev/null")
	f.setEnv("GIT_CONFIG_SYSTEM", "/dev/null")
	f.setEnv("GIT_CONFIG_NOSYSTEM", "1")
	cmd := exec.Command(runner, "checks", "--base", base, "--head", "HEAD", "--logs", filepath.Join(f.repo, "logs"))
	cmd.Dir = f.repo
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 4 {
		t.Fatalf("missing identity: err=%v out=%s", err, out)
	}

	// Missing judgment outranks an earlier rejection.
	f2 := newChangeFixture(t, true)
	copyChecksAndStubTree(t, root, f2.repo, f2.gateBin)
	base2 := f2.commit()
	f2.setEnv("GIT_AUTHOR_NAME", "Wrong")
	if err := os.WriteFile(filepath.Join(f2.repo, "README.md"), []byte("# Changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f2.commit()
	f2.setEnv("RENDER_RC", "3")
	cmd2 := exec.Command(runner, "checks", "--base", base2, "--head", "HEAD", "--logs", filepath.Join(f2.repo, "logs2"))
	cmd2.Dir = f2.repo
	cmd2.Env = f2.env
	out2, err2 := cmd2.CombinedOutput()
	if ee, ok := err2.(*exec.ExitError); !ok || ee.ExitCode() != 4 {
		t.Fatalf("outranks: err=%v out=%s", err2, out2)
	}
}

func TestDocumentsUnavailableRendererIsNotRejection(t *testing.T) {
	root, _ := loadTestContext(t)
	runner := buildTillrGateForTest(t, root)
	f := newChangeFixture(t, true)
	copyChecksAndStubTree(t, root, f.repo, f.gateBin)
	base := f.commit()

	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Fixture\n\nEdited.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit()
	f.setEnv("RENDER_RC", "3")
	cmd := exec.Command(runner, "checks", "--base", base, "--head", "HEAD", "--logs", filepath.Join(f.repo, "logs"))
	cmd.Dir = f.repo
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 4 {
		t.Fatalf("unavailable renderer: err=%v out=%s", err, out)
	}
	f.setEnv("RENDER_RC", "0")
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := f.check("documents", base, ""); code != 4 {
		t.Fatalf("dirty markdown: expected 4, got %d", code)
	}
}

func TestChangeAdapterRecordsCompleteFastInventoryIncludingSkips(t *testing.T) {
	root, _ := loadTestContext(t)
	runner := buildTillrGateForTest(t, root)
	f := newChangeFixture(t, true)
	copyChecksAndStubTree(t, root, f.repo, f.gateBin)
	base := f.commit()

	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := f.commit()

	cmd := exec.Command(f.gateBin, "change-gate", "--base", base, "--head", head, "--runner", runner, "--logs", filepath.Join(f.repo, "logs"))
	cmd.Dir = f.repo
	cmd.Env = f.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("change-gate: %v: %s", err, stderr.String())
	}
	var env struct {
		Payload struct {
			Checks []struct {
				CheckID string `json:"check_id"`
				Result  string `json:"result"`
			} `json:"checks"`
		} `json:"payload"`
	}
	firstLine := strings.Split(strings.TrimSpace(stdout.String()), "\n")[0]
	if err := json.Unmarshal([]byte(firstLine), &env); err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string)
	for _, c := range env.Payload.Checks {
		got[c.CheckID] = c.Result
	}
	skipped := map[string]bool{
		"change-checks-test": true, "format": true, "gate-adapters-test": true,
		"gate-budget": true, "go-affected": true, "go-build": true,
		"go-edges": true, "go-module-inventory": true, "workflows": true,
	}
	expected := []string{"commit-identity", "citations", "documents", "file-size", "workflows", "elapsed-time",
		"gate-budget", "scratch", "tracked-tillr", "design-citations", "pack-sync", "go-build", "format", "go-affected",
		"go-edges", "go-module-inventory", "gate-adapters-test", "change-checks-test"}
	if len(got) != len(expected) {
		t.Fatalf("got %d checks (%v), want %d", len(got), got, len(expected))
	}
	for _, id := range expected {
		want := "pass"
		if skipped[id] {
			want = "skipped: no changed path is read"
		}
		if got[id] != want {
			t.Fatalf("%s: got %q want %q", id, got[id], want)
		}
	}
	if !strings.Contains(stderr.String(), "skipped: no changed path is read:") {
		t.Fatalf("expected skip notice on stderr, got %q", stderr.String())
	}
}

func TestChangeRealFormatDeclarationsExecuteOnCleanTree(t *testing.T) {
	root, _ := loadTestContext(t)
	runner := buildTillrGateForTest(t, root)
	f := newChangeFixture(t, true)

	justData, err := os.ReadFile(filepath.Join(root, ".justfile"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, ".justfile"), justData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.repo, "tools", "example"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "example", "a.go"), []byte("package example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.repo, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "tests", "format.test.py"), []byte("print('format fixture ran')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := f.commit()

	decls := filepath.Join(f.repo, "format-checks")
	if err := os.MkdirAll(decls, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"format.json", "format-test.json"} {
		data, err := os.ReadFile(filepath.Join(root, "tools", "gate", "checks", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(decls, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, flag := range []string{"--fast-tree", "--heavy-tree"} {
		cmd := exec.Command(runner, "checks", flag, "--base", f.base, "--head", head, "--checks", decls, "--logs", filepath.Join(f.repo, "logs"))
		cmd.Dir = f.repo
		cmd.Env = f.env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", flag, err, out)
		}
	}
}

func TestChangeLoopProgramVerdictAndRendererUnavailable(t *testing.T) {
	root, _ := loadTestContext(t)
	runner := buildTillrGateForTest(t, root)
	f := newChangeFixture(t, true)
	copyChecksAndStubTree(t, root, f.repo, f.gateBin)
	base := f.commit()

	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Fixture\n\nEdited.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := f.commit()

	cmd := exec.Command(f.gateBin, "change-gate", "--base", base, "--head", head, "--runner", runner, "--logs", filepath.Join(f.repo, "logs"))
	cmd.Dir = f.repo
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("first: %v: %s", err, out)
	}
	f.setEnv("RENDER_RC", "3")
	f.setEnv("RENDER_VERSION_RC", "3")
	cmd = exec.Command(f.gateBin, "change-gate", "--base", base, "--head", head, "--runner", runner, "--logs", filepath.Join(f.repo, "logs"))
	cmd.Dir = f.repo
	cmd.Env = f.env
	_, err = cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 4 {
		t.Fatalf("expected exit 4 without renderer, got %v", err)
	}
}

func TestChangeLoopProgramKeepsVerdictOfSlowTreeCheck(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := f.commit()
	argsLog := filepath.Join(t.TempDir(), "runner-args.json")
	runner := filepath.Join(f.bin, "recording-runner")
	script := "#!/usr/bin/env python3\nimport json, sys\nopen(" + jsonQuote(argsLog) + ", 'w').write(json.dumps(sys.argv[1:]))\nsys.exit(1)\n"
	if err := os.WriteFile(runner, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(f.gateBin, "change-gate", "--base", f.base, "--head", head, "--runner", runner, "--logs", filepath.Join(f.repo, "logs"))
	cmd.Dir = f.repo
	cmd.Env = f.env
	_, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	raw, _ := os.ReadFile(argsLog)
	var args []string
	_ = json.Unmarshal(raw, &args)
	if len(args) < 2 || args[0] != "checks" || args[1] != "--fast-tree" {
		t.Fatalf("args=%v", args)
	}
	overrun := ""
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--tree-overrun" {
			overrun = args[i+1]
		}
	}
	if overrun != "keep" {
		t.Fatalf("tree-overrun=%q want keep", overrun)
	}
}

func TestChangeLoopProgramWarmsTrimmedGoCacheOnceOutsideCeilings(t *testing.T) {
	f := newChangeFixture(t, true)
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("# Changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := f.commit()
	warmLog := filepath.Join(t.TempDir(), "warm.log")
	warmRC := filepath.Join(t.TempDir(), "warm-rc")
	stubGate := filepath.Join(f.repo, "tools", "gate", "repo-gate")
	gateScript := "#!/usr/bin/env python3\nimport os, sys\n" +
		"open(" + jsonQuote(warmLog) + ", 'a').write('warm\\n')\n" +
		"sys.exit(int(open(" + jsonQuote(warmRC) + ").read().strip()))\n"
	if err := os.WriteFile(stubGate, []byte(gateScript), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(f.bin, "recording-runner")
	runnerScript := "#!/usr/bin/env python3\nimport sys\n" +
		"if '--help' in sys.argv:\n    print('  -fast-tree')\n    sys.exit(0)\n" +
		"open(" + jsonQuote(warmLog) + ", 'a').write('runner\\n')\nsys.exit(1)\n"
	if err := os.WriteFile(runner, []byte(runnerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rc := range []string{"4", "0", "0"} {
		_ = os.WriteFile(warmRC, []byte(rc), 0o644)
		cmd := exec.Command(f.gateBin, "change-gate", "--base", f.base, "--head", head, "--runner", runner, "--logs", filepath.Join(f.repo, "logs"))
		cmd.Dir = f.repo
		cmd.Env = f.env
		_, err := cmd.CombinedOutput()
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			t.Fatalf("rc=%s: expected exit 1, got %v", rc, err)
		}
	}
	logBytes, _ := os.ReadFile(warmLog)
	got := strings.Fields(string(logBytes))
	want := []string{"warm", "runner", "warm", "runner", "runner"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("warm log=%v want=%v", got, want)
	}
}

func TestChangeGoModuleAdditionAndRemovalSelectInventoryChecksInCandidateGate(t *testing.T) {
	f := newChangeFixture(t, true)
	root, _ := loadTestContext(t)
	runner := buildTillrGateForTest(t, root)
	checksDir := filepath.Join(f.repo, "inventory-checks")
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go-module-inventory.json", "go-edges.json"} {
		data, err := os.ReadFile(filepath.Join(root, "tools", "gate", "checks", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(checksDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{
		"tools/gate/checks/go-edges.py",
		"tools/gate/checks/go-module-inventory.py",
	} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(f.repo, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	markerLog := filepath.Join(f.repo, ".inventory-markers")
	if err := os.MkdirAll(filepath.Join(f.repo, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	scratchStub := "import os, sys\nfrom pathlib import Path\n" +
		"Path(" + jsonQuote(markerLog) + ").open('a').write('testmain-cleanup\\n')\n" +
		"if os.environ.get('FAIL_TESTMAIN') == '1':\n" +
		"    print('simulated TestMain failure')\n" +
		"    sys.exit(1)\n"
	if err := os.WriteFile(filepath.Join(f.repo, "tests", "tillr-scratch.test.py"), []byte(scratchStub), 0o644); err != nil {
		t.Fatal(err)
	}
	// Replace tools/gate with a minimal Go module stub for the two declared-suite tests.
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "gate", "go.mod"), []byte("module example.invalid/gate\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	treeTestStub := "package main\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n" +
		"func appendMarker(t *testing.T, line string) {\n" +
		"\tt.Helper()\n" +
		"\tf, err := os.OpenFile(" + jsonQuote(markerLog) + ", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)\n" +
		"\tif err != nil { t.Fatal(err) }\n" +
		"\tdefer f.Close()\n" +
		"\tif _, err := f.WriteString(line + \"\\n\"); err != nil { t.Fatal(err) }\n" +
		"}\n\n" +
		"func TestTreeEveryGoModuleIsBuiltAndHasDeclaredSuite(t *testing.T) { appendMarker(t, \"declared-suites\") }\n" +
		"func TestTreeDeclarationReadingGoModuleReadsInRepoDependencies(t *testing.T) { appendMarker(t, \"in-repo-deps\") }\n"
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "gate", "tree_checks_test.go"), []byte(treeTestStub), 0o644); err != nil {
		t.Fatal(err)
	}
	// Remove other .go files copied by newChangeFixture so `go test .` compiles only our stub.
	if entries, err := os.ReadDir(filepath.Join(f.repo, "tools", "gate")); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && e.Name() != "tree_checks_test.go" {
				_ = os.Remove(filepath.Join(f.repo, "tools", "gate", e.Name()))
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(f.repo, "tools", "tillr-gate", "goaffected"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "tillr-gate", "go.mod"), []byte("module github.com/mschulkind-oss/tillr-gate\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	affectedStub := "package goaffected_test\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n" +
		"func TestDiscoverModules(t *testing.T) {\n" +
		"\tf, err := os.OpenFile(" + jsonQuote(markerLog) + ", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)\n" +
		"\tif err != nil { t.Fatal(err) }\n" +
		"\tdefer f.Close()\n" +
		"\tif _, err := f.WriteString(\"go-affected-modules\\n\"); err != nil { t.Fatal(err) }\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "tillr-gate", "goaffected", "selection_test.go"), []byte(affectedStub), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(f.repo, "tools", "tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "tillr", "go.mod"), []byte("module github.com/mschulkind-oss/tillr\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.repo, "tools", "tillr-existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "tillr-existing", "go.mod"), []byte("module github.com/mschulkind-oss/tillr-existing\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "tillr-existing", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	defPkg := "github.com/mschulkind-oss/tillr/definition"
	defTest := "TestNameOnlyInOneConstantAndOnlyLifecycleConsumers"
	storePkg := "github.com/mschulkind-oss/tillr/store"
	storeTest := "TestEveryStoreProgramFlushesTheSQLiteTraceOnExit"
	edges := map[string]map[string]any{
		defPkg: {
			"builds": []string{},
			"reads":  []string{"tools/tillr-existing/", "tools/tillr-gate/", "tools/tillr/"},
			"read_tests": map[string][]string{
				"tools/tillr-existing/": {defTest},
				"tools/tillr-gate/":     {defTest},
				"tools/tillr/":          {defTest},
			},
		},
		storePkg: {
			"builds": []string{},
			"reads":  []string{"tools/tillr-existing/", "tools/tillr-gate/"},
			"read_tests": map[string][]string{
				"tools/tillr-existing/": {storeTest},
				"tools/tillr-gate/":     {storeTest},
			},
		},
	}
	writeEdges := func() {
		raw, err := json.MarshalIndent(edges, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.repo, "tools", "gate", "go-test-edges.json"), append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeEdges()
	base := f.commit()

	runGate := func(baseSHA, headSHA string, extraEnv ...string) (int, map[string]string, string) {
		cmd := exec.Command(runner, "checks", "--fast-tree", "--scope-to-change",
			"--base", baseSHA, "--head", headSHA,
			"--worktree", f.repo, "--checks", checksDir,
			"--logs", filepath.Join(t.TempDir(), "logs"))
		cmd.Dir = f.repo
		cmd.Env = append(append([]string{}, f.env...), extraEnv...)
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
		var env struct {
			Payload struct {
				Checks []struct {
					CheckID string `json:"check_id"`
					Result  string `json:"result"`
				} `json:"checks"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal stdout=%q stderr=%q: %v", stdout.String(), stderr.String(), err)
		}
		got := make(map[string]string, len(env.Payload.Checks))
		for _, c := range env.Payload.Checks {
			got[c.CheckID] = c.Result
		}
		return code, got, stdout.String() + stderr.String()
	}

	// 1. Ordinary tools/ edit (no go.mod added or removed): go-module-inventory exits 0
	// without invoking the heavy inventory checks.
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "tillr-existing", "main.go"), []byte("package main\nfunc main() { _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	headEdit := f.commit()
	code, checksEdit, out := runGate(base, headEdit)
	if code != 0 || checksEdit["go-edges"] != "pass" || checksEdit["go-module-inventory"] != "pass" {
		t.Fatalf("ordinary edit: code=%d checks=%v out=%s", code, checksEdit, out)
	}
	if _, err := os.Stat(markerLog); !os.IsNotExist(err) {
		t.Fatalf("ordinary tools/ edit should not invoke heavy inventory checks")
	}

	// 2. Candidate adds tools/tillr-new/go.mod without declaring it in go-test-edges.json:
	// both go-edges and go-module-inventory run and fail, and every inventory sub-check executes.
	if err := os.MkdirAll(filepath.Join(f.repo, "tools", "tillr-new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "tools", "tillr-new", "go.mod"), []byte("module github.com/mschulkind-oss/tillr-new\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	headAddUndeclared := f.commit()
	code, checksAddFail, out := runGate(headEdit, headAddUndeclared)
	if code != 1 || checksAddFail["go-edges"] != "fail" || checksAddFail["go-module-inventory"] != "fail" {
		t.Fatalf("undeclared add: code=%d checks=%v out=%s", code, checksAddFail, out)
	}
	wantMarkers := "testmain-cleanup,declared-suites,in-repo-deps,go-affected-modules"
	if gotBytes, err := os.ReadFile(markerLog); err != nil || strings.Join(strings.Fields(string(gotBytes)), ",") != wantMarkers {
		t.Fatalf("markers on add=%q want=%q err=%v", string(gotBytes), wantMarkers, err)
	}

	// 3. Declare tools/tillr-new/ in go-test-edges.json: when TestMain fails, the candidate gate
	// still fails; when all inventory checks pass, the candidate gate passes.
	edges[defPkg]["reads"] = []string{"tools/tillr-existing/", "tools/tillr-gate/", "tools/tillr/", "tools/tillr-new/"}
	edges[defPkg]["read_tests"].(map[string][]string)["tools/tillr-new/"] = []string{defTest}
	edges[storePkg]["reads"] = []string{"tools/tillr-existing/", "tools/tillr-gate/", "tools/tillr-new/"}
	edges[storePkg]["read_tests"].(map[string][]string)["tools/tillr-new/"] = []string{storeTest}
	writeEdges()
	headAddDeclared := f.commit()

	_ = os.Remove(markerLog)
	code, checksTMFail, out := runGate(headEdit, headAddDeclared, "FAIL_TESTMAIN=1")
	if code != 1 || checksTMFail["go-edges"] != "pass" || checksTMFail["go-module-inventory"] != "fail" {
		t.Fatalf("testmain fail: code=%d checks=%v out=%s", code, checksTMFail, out)
	}

	_ = os.Remove(markerLog)
	code, checksAddPass, out := runGate(headEdit, headAddDeclared)
	if code != 0 || checksAddPass["go-edges"] != "pass" || checksAddPass["go-module-inventory"] != "pass" {
		t.Fatalf("declared add pass: code=%d checks=%v out=%s", code, checksAddPass, out)
	}
	if gotBytes, err := os.ReadFile(markerLog); err != nil || strings.Join(strings.Fields(string(gotBytes)), ",") != wantMarkers {
		t.Fatalf("markers on pass=%q want=%q err=%v", string(gotBytes), wantMarkers, err)
	}

	// 4. Candidate removes tools/tillr-new/go.mod: inventory checks run again.
	_ = os.Remove(markerLog)
	f.git("rm", "-q", "tools/tillr-new/go.mod")
	edges[defPkg]["reads"] = []string{"tools/tillr-existing/", "tools/tillr-gate/", "tools/tillr/"}
	delete(edges[defPkg]["read_tests"].(map[string][]string), "tools/tillr-new/")
	edges[storePkg]["reads"] = []string{"tools/tillr-existing/", "tools/tillr-gate/"}
	delete(edges[storePkg]["read_tests"].(map[string][]string), "tools/tillr-new/")
	writeEdges()
	headRemove := f.commit()
	code, checksRemove, out := runGate(headAddDeclared, headRemove)
	if code != 0 || checksRemove["go-edges"] != "pass" || checksRemove["go-module-inventory"] != "pass" {
		t.Fatalf("remove: code=%d checks=%v out=%s", code, checksRemove, out)
	}
	if gotBytes, err := os.ReadFile(markerLog); err != nil || strings.Join(strings.Fields(string(gotBytes)), ",") != wantMarkers {
		t.Fatalf("markers on remove=%q want=%q err=%v", string(gotBytes), wantMarkers, err)
	}
}

func writeFixtureFile(t *testing.T, repo, relPath, content string) {
	t.Helper()
	full := filepath.Join(repo, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func validWatchRecordDoc(dependsOnYAML string) string {
	return "---\ntitle: \"Watch decision\"\nstatus: draft\nstage: DECIDED\ndepends-on:\n" + dependsOnYAML + "\n---\n\n# Watch decision\n"
}

func TestDocumentsWatchDecisionRecordClause1Anchors(t *testing.T) {
	f := newChangeFixture(t, true)
	mainPath := "docs/design/tillr-watch.md"

	// Initial commit with docs/design/tillr-watch.md containing D-TW1.
	writeFixtureFile(t, f.repo, mainPath, "# Watch\n\n<a id=\"D-TW1\"></a>D-TW1\n")
	base := f.commit()

	// Refusal: adding a brand-new docs/design/tillr-watch.md with a D-TW anchor (A status) fails clause 1.
	code, out, errOut := f.check("documents", f.base, base)
	if code != 1 {
		t.Fatalf("add main with D-TW1: code=%d out=%q err=%q", code, out, errOut)
	}
	lines := splitLines(out)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], mainPath+": ") || !strings.Contains(lines[0], "d-tw1") || !strings.Contains(lines[0], "clause 1") || !strings.Contains(lines[0], "docs/design/tillr-watch/<slug>.md") || !strings.Contains(lines[0], "docs/design/tillr-watch/decision-record-files.md") {
		t.Fatalf("unexpected clause 1 add finding: %q", out)
	}

	// Refusal: HTML id/name (double and single quoted), ATX heading slug, and Vantage question id
	// each produce one finding line in sorted anchor order.
	writeFixtureFile(t, f.repo, mainPath, strings.Join([]string{
		"# Watch",
		"",
		`<a id="D-TW1"></a>D-TW1`,
		`<a id="D-TW99"></a>D-TW99`,
		`<a name='D-TW100'></a>D-TW100`,
		`## D-TW101: New heading`,
		`<!-- vantage: question id=D-TW102 -->`,
		"",
	}, "\n"))
	f.commit()
	code, out, errOut = f.check("documents", base, "")
	if code != 1 {
		t.Fatalf("clause 1 multi-anchor: code=%d out=%q err=%q", code, out, errOut)
	}
	lines = splitLines(out)
	wantAnchors := []string{"d-tw100", "d-tw101-new-heading", "d-tw102", "d-tw99"}
	if len(lines) != len(wantAnchors) {
		t.Fatalf("clause 1 lines=%d want %d; out=%q", len(lines), len(wantAnchors), out)
	}
	for i, want := range wantAnchors {
		if !strings.HasPrefix(lines[i], mainPath+": ") || !strings.Contains(lines[i], want) || !strings.Contains(lines[i], "clause 1") || !strings.Contains(lines[i], "docs/design/tillr-watch/decision-record-files.md") {
			t.Fatalf("line[%d]=%q want anchor %q", i, lines[i], want)
		}
	}
	if len(f.renders()) != 0 {
		t.Fatalf("clause 1 refusal should not invoke vantage-check; renders=%v", f.renders())
	}

	// Pass: editing prose, deleting existing D-TW1, adding OQ-TW9, and writing D-TW inside fenced code blocks.
	writeFixtureFile(t, f.repo, mainPath, strings.Join([]string{
		"# Watch",
		"",
		"Updated prose without D-TW1.",
		`<a id="OQ-TW9"></a>OQ-TW9`,
		"```markdown",
		"## D-TW999: Inside backtick fence",
		`<a id="D-TW998"></a>`,
		"```",
		"~~~markdown",
		"<!-- vantage: question id=D-TW997 -->",
		"~~~",
		"",
	}, "\n"))
	f.commit()
	code, out, errOut = f.check("documents", base, "")
	if code != 0 {
		t.Fatalf("clause 1 pass cases: code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestDocumentsWatchDecisionRecordClause2MixedEdits(t *testing.T) {
	f := newChangeFixture(t, true)
	mainPath := "docs/design/tillr-watch.md"
	existingRec := "docs/design/tillr-watch/existing.md"
	writeFixtureFile(t, f.repo, mainPath, "# Watch\n")
	writeFixtureFile(t, f.repo, existingRec, validWatchRecordDoc("  - ../tillr-watch.md"))
	base := f.commit()

	// Refusal: add new record while modifying docs/design/tillr-watch.md.
	writeFixtureFile(t, f.repo, mainPath, "# Watch\n\nEdited.\n")
	writeFixtureFile(t, f.repo, "docs/design/tillr-watch/new-rule.md", validWatchRecordDoc("  - ../tillr-watch.md"))
	f.commit()
	code, out, errOut := f.check("documents", base, "")
	if code != 1 {
		t.Fatalf("mixed add + modify main: code=%d out=%q err=%q", code, out, errOut)
	}
	lines := splitLines(out)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], mainPath+": ") || !strings.Contains(lines[0], "docs/design/tillr-watch/new-rule.md") || !strings.Contains(lines[0], "clause 2") || !strings.Contains(lines[0], "docs/design/tillr-watch/decision-record-files.md") {
		t.Fatalf("unexpected clause 2 main-modify finding: %q", out)
	}

	// Refusal: add new record while deleting docs/design/tillr-watch.md and modifying existing record.
	f.git("reset", "--hard", "-q", base)
	f.git("rm", "-q", mainPath)
	writeFixtureFile(t, f.repo, existingRec, validWatchRecordDoc("  - ../tillr-watch.md")+"\nEdited.\n")
	writeFixtureFile(t, f.repo, "docs/design/tillr-watch/new-rule.md", validWatchRecordDoc("  - ../tillr-watch.md"))
	f.commit()
	code, out, errOut = f.check("documents", base, "")
	if code != 1 {
		t.Fatalf("mixed add + delete main + modify existing: code=%d out=%q err=%q", code, out, errOut)
	}
	lines = splitLines(out)
	if len(lines) != 2 || !strings.HasPrefix(lines[0], mainPath+": ") || !strings.HasPrefix(lines[1], existingRec+": ") {
		t.Fatalf("unexpected clause 2 multi-path findings: %q", out)
	}

	// Refusal: git mv of existing record inside docs/design/tillr-watch/ caught via --no-renames as A + D.
	f.git("reset", "--hard", "-q", base)
	f.git("mv", existingRec, "docs/design/tillr-watch/renamed.md")
	f.commit()
	code, out, errOut = f.check("documents", base, "")
	if code != 1 {
		t.Fatalf("git mv existing record: code=%d out=%q err=%q", code, out, errOut)
	}
	lines = splitLines(out)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], existingRec+": ") || !strings.Contains(lines[0], "docs/design/tillr-watch/renamed.md") || !strings.Contains(lines[0], "clause 2") {
		t.Fatalf("unexpected clause 2 git-mv finding: %q", out)
	}

	// Pass: adding a valid record alone + unrelated file outside docs/design/tillr-watch*.
	f.git("reset", "--hard", "-q", base)
	writeFixtureFile(t, f.repo, "docs/design/tillr-watch/solo.md", validWatchRecordDoc("  - ../tillr-watch.md"))
	writeFixtureFile(t, f.repo, "README.md", "# Fixture\n\nUnrelated.\n")
	f.commit()
	if code, out, errOut := f.check("documents", base, ""); code != 0 {
		t.Fatalf("solo add + unrelated: code=%d out=%q err=%q", code, out, errOut)
	}

	// Pass: modifying main and deleting existing record with zero added record files.
	f.git("reset", "--hard", "-q", base)
	writeFixtureFile(t, f.repo, mainPath, "# Watch\n\nCleanup.\n")
	f.git("rm", "-q", existingRec)
	f.commit()
	if code, out, errOut := f.check("documents", base, ""); code != 0 {
		t.Fatalf("modify/delete with zero added records: code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestDocumentsWatchDecisionRecordClause3DependsOn(t *testing.T) {
	f := newChangeFixture(t, true)
	writeFixtureFile(t, f.repo, "docs/design/tillr-watch.md", "# Watch\n")
	base := f.commit()

	// Refusal: no frontmatter, wrong depends-on, and wrong relative depth in nested subdirectory.
	writeFixtureFile(t, f.repo, "docs/design/tillr-watch/a-no-fm.md", "# No frontmatter\n")
	writeFixtureFile(t, f.repo, "docs/design/tillr-watch/b-wrong-dep.md", "---\ntitle: \"Wrong\"\ndepends-on:\n  - ../other.md\n---\n\n# Wrong\n")
	writeFixtureFile(t, f.repo, "docs/design/tillr-watch/sub/c-bad-rel.md", "---\ntitle: \"Nested\"\ndepends-on:\n  - ../tillr-watch.md\n---\n\n# Nested\n")
	f.commit()
	code, out, errOut := f.check("documents", base, "")
	if code != 1 {
		t.Fatalf("clause 3 refusals: code=%d out=%q err=%q", code, out, errOut)
	}
	lines := splitLines(out)
	wantPaths := []string{
		"docs/design/tillr-watch/a-no-fm.md",
		"docs/design/tillr-watch/b-wrong-dep.md",
		"docs/design/tillr-watch/sub/c-bad-rel.md",
	}
	if len(lines) != len(wantPaths) {
		t.Fatalf("clause 3 lines=%d want %d; out=%q", len(lines), len(wantPaths), out)
	}
	for i, wp := range wantPaths {
		if !strings.HasPrefix(lines[i], wp+": ") || !strings.Contains(lines[i], "clause 3") || !strings.Contains(lines[i], "depends-on") || !strings.Contains(lines[i], "docs/design/tillr-watch/decision-record-files.md") {
			t.Fatalf("line[%d]=%q want path %q", i, lines[i], wp)
		}
	}

	// Pass: scalar, inline-list (with fragment & comment), block-list (with comment), and nested ../../tillr-watch.md.
	f.git("reset", "--hard", "-q", base)
	writeFixtureFile(t, f.repo, "docs/design/tillr-watch/scalar.md", "---\ntitle: \"Scalar\"\ndepends-on: ../tillr-watch.md # main\n---\n\n# Scalar\n")
	writeFixtureFile(t, f.repo, "docs/design/tillr-watch/inline.md", "---\ntitle: \"Inline\"\ndepends-on: [\"../other.md\", '../tillr-watch.md#D-TW5'] # list\n---\n\n# Inline\n")
	writeFixtureFile(t, f.repo, "docs/design/tillr-watch/block.md", "---\ntitle: \"Block\"\ndepends-on:\n  - ../tillr-watch.md#section-1 # comment\n---\n\n# Block\n")
	writeFixtureFile(t, f.repo, "docs/design/tillr-watch/sub/nested.md", "---\ntitle: \"Nested\"\ndepends-on:\n  - ../../tillr-watch.md\n---\n\n# Nested\n")
	f.commit()
	if code, out, errOut := f.check("documents", base, ""); code != 0 {
		t.Fatalf("clause 3 valid forms: code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestDocumentsWatchDecisionRecordMultiViolationDegenerateAndCacheProof(t *testing.T) {
	f := newChangeFixture(t, true)
	mainPath := "docs/design/tillr-watch.md"
	existingRec := "docs/design/tillr-watch/existing.md"
	writeFixtureFile(t, f.repo, mainPath, "# Watch\n")
	writeFixtureFile(t, f.repo, existingRec, validWatchRecordDoc("  - ../tillr-watch.md"))
	base := f.commit()

	// Degenerate range: base == HEAD passes.
	if code, out, errOut := f.check("documents", base, base); code != 0 {
		t.Fatalf("base==HEAD: code=%d out=%q err=%q", code, out, errOut)
	}

	// Unrelated change passes without watch findings.
	writeFixtureFile(t, f.repo, "README.md", "# Fixture\n\nOther.\n")
	unrelated := f.commit()
	if code, out, errOut := f.check("documents", base, unrelated); code != 0 {
		t.Fatalf("unrelated: code=%d out=%q err=%q", code, out, errOut)
	}

	// Multi-violation without short-circuiting: Clause 1 + Clause 2 (2 files) + Clause 3 = 4 lines.
	writeFixtureFile(t, f.repo, mainPath, "# Watch\n\n<a id=\"D-TW88\"></a>D-TW88\n")
	writeFixtureFile(t, f.repo, existingRec, validWatchRecordDoc("  - ../tillr-watch.md")+"\nTouched.\n")
	writeFixtureFile(t, f.repo, "docs/design/tillr-watch/bad-new.md", "# Missing frontmatter\n")
	violatingHead := f.commit()
	_ = os.Remove(f.renderLog)
	code, out, errOut := f.check("documents", unrelated, violatingHead)
	if code != 1 {
		t.Fatalf("multi-violation: code=%d out=%q err=%q", code, out, errOut)
	}
	lines := splitLines(out)
	if len(lines) != 4 {
		t.Fatalf("expected 4 non-short-circuited findings, got %d: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "clause 1") || !strings.Contains(lines[1], "clause 2") || !strings.Contains(lines[2], "clause 2") || !strings.Contains(lines[3], "clause 3") {
		t.Fatalf("unexpected multi-violation ordering/clauses: %q", out)
	}
	if len(f.renders()) != 0 {
		t.Fatalf("multi-violation refusal must not call vantage-check; renders=%v", f.renders())
	}

	// Cache-proof refusal: warm remembered vantage-check passes for the exact violating blobs
	// by checking a range where those blobs are already at base (zero watch diff), then re-check
	// the violating range and confirm it still exits 1 without rendering.
	writeFixtureFile(t, f.repo, "README.md", "# Fixture\n\nWarm cache.\n")
	warmHead := f.commit()
	// Edit mainPath prose only (preserving D-TW88) and bad-new.md in a commit from violatingHead
	// so judgeDocuments runs and remembers passes for those blobs.
	writeFixtureFile(t, f.repo, mainPath, "# Watch\n\n<a id=\"D-TW88\"></a>D-TW88\n\nCached.\n")
	writeFixtureFile(t, f.repo, "docs/design/tillr-watch/bad-new.md", "# Missing frontmatter\n\nCached.\n")
	cachedHead := f.commit()
	if code, out, errOut := f.check("documents", warmHead, cachedHead); code != 0 {
		t.Fatalf("warm cache run: code=%d out=%q err=%q", code, out, errOut)
	}
	_ = os.Remove(f.renderLog)
	// Now check unrelated..cachedHead: both blobs have remembered passes, yet all 3 clauses still fail.
	code, out, errOut = f.check("documents", unrelated, cachedHead)
	if code != 1 {
		t.Fatalf("cache-proof refusal: code=%d out=%q err=%q", code, out, errOut)
	}
	if len(splitLines(out)) != 4 {
		t.Fatalf("cache-proof findings=%q", out)
	}
	if len(f.renders()) != 0 {
		t.Fatalf("cache-proof refusal must not call vantage-check; renders=%v", f.renders())
	}
}

func TestChangeGateSuitesRunPreMergeOnGateEditsOutsideFastBudget(t *testing.T) {
	root, _ := loadTestContext(t)
	runner := buildTillrGateForTest(t, root)
	checksDir := filepath.Join(root, "tools", "gate", "checks")

	for _, id := range []string{"gate-adapters-test", "change-checks-test"} {
		raw, err := os.ReadFile(filepath.Join(checksDir, id+".json"))
		if err != nil {
			t.Fatalf("read %s.json: %v", id, err)
		}
		var decl map[string]any
		if err := json.Unmarshal(raw, &decl); err != nil {
			t.Fatalf("unmarshal %s.json: %v", id, err)
		}
		if decl["scope"] != "change" || decl["tier"] != "heavy" {
			t.Fatalf("%s.json: scope=%v tier=%v, want change/heavy", id, decl["scope"], decl["tier"])
		}
		if _, hasBudget := decl["budget_ms"]; hasBudget {
			t.Fatalf("%s.json must not declare budget_ms (must stay outside the fast tier's time sum)", id)
		}
		if timeout, ok := decl["timeout_ms"].(float64); !ok || timeout <= 0 {
			t.Fatalf("%s.json: timeout_ms=%v, want positive number", id, decl["timeout_ms"])
		}
		if remember, ok := decl["remember"].(bool); !ok || remember {
			t.Fatalf("%s.json: remember=%v, want false", id, decl["remember"])
		}
	}

	// Verify gate-budget still passes with the real checks directory (suites are outside the 9000 ms fast sum).
	fBudget := newChangeFixture(t, true)
	if out, err := exec.Command(fBudget.gateBin, "gate-budget", checksDir).CombinedOutput(); err != nil {
		t.Fatalf("gate-budget failed: %v: %s", err, out)
	}

	cases := []struct {
		name      string
		changed   string
		wantToRun bool
	}{
		{"tillr-gate", "tools/tillr-gate/sample.go", true},
		{"tools-gate", "tools/gate/sample.go", true},
		{"pack-mirror-tillr-gate", "yolo-packs/tillr/files/local/share/tillr/tillr-gate/sample.go", true},
		{"pack-program-tillr-gate", "yolo-packs/tillr/programs/tillr-gate/run", true},
		{"elsewhere-readme", "README.md", false},
		{"elsewhere-drain", "tools/tillr-drain/drain/sample.go", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newChangeFixture(t, true)
			markerFile := filepath.Join(f.repo, ".suite-markers")
			dstChecks := filepath.Join(f.repo, "tools", "gate", "checks")
			if err := os.MkdirAll(dstChecks, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"gate-adapters-test", "change-checks-test"} {
				raw, err := os.ReadFile(filepath.Join(checksDir, id+".json"))
				if err != nil {
					t.Fatal(err)
				}
				var decl map[string]any
				if err := json.Unmarshal(raw, &decl); err != nil {
					t.Fatal(err)
				}
				decl["command"] = []string{"sh", "-c", "printf '%s\\n' " + id + " >> " + jsonQuote(markerFile), id}
				out, _ := json.Marshal(decl)
				if err := os.WriteFile(filepath.Join(dstChecks, id+".json"), out, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			base := f.commit()

			writeFixtureFile(t, f.repo, tc.changed, "changed\n")
			head := f.commit()

			cmd := exec.Command(f.gateBin, "change-gate", "--base", base, "--head", head, "--runner", runner, "--logs", filepath.Join(f.repo, "logs"))
			cmd.Dir = f.repo
			cmd.Env = f.env
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("change-gate (%s): %v: stdout=%s stderr=%s", tc.changed, err, stdout.String(), stderr.String())
			}

			var env struct {
				Payload struct {
					Checks []struct {
						CheckID string `json:"check_id"`
						Result  string `json:"result"`
					} `json:"checks"`
				} `json:"payload"`
			}
			firstLine := strings.Split(strings.TrimSpace(stdout.String()), "\n")[0]
			if err := json.Unmarshal([]byte(firstLine), &env); err != nil {
				t.Fatalf("parse verdict: %v: %s", err, stdout.String())
			}
			results := make(map[string]string)
			for _, c := range env.Payload.Checks {
				results[c.CheckID] = c.Result
			}
			markersRaw, _ := os.ReadFile(markerFile)
			markers := string(markersRaw)
			for _, id := range []string{"gate-adapters-test", "change-checks-test"} {
				if tc.wantToRun {
					if results[id] != "pass" {
						t.Fatalf("%s for %s: result=%q want pass (all=%v)", id, tc.changed, results[id], results)
					}
					if !strings.Contains(markers, id+"\n") {
						t.Fatalf("%s for %s did not execute (markers=%q)", id, tc.changed, markers)
					}
				} else {
					if results[id] != "skipped: no changed path is read" {
						t.Fatalf("%s for %s: result=%q want skipped (all=%v)", id, tc.changed, results[id], results)
					}
					if strings.Contains(markers, id+"\n") {
						t.Fatalf("%s for %s unexpectedly executed (markers=%q)", id, tc.changed, markers)
					}
				}
			}
		})
	}
}

func TestChangeLandingGateRefusesBrokenGateAdapterBeforeMerge(t *testing.T) {
	root, _ := loadTestContext(t)
	runner := buildTillrGateForTest(t, root)
	f := newChangeFixture(t, true)

	// Install the real, unmodified gate-adapters-test.json declaration.
	dstChecks := filepath.Join(f.repo, "tools", "gate", "checks")
	if err := os.MkdirAll(dstChecks, 0o755); err != nil {
		t.Fatal(err)
	}
	rawDecl, err := os.ReadFile(filepath.Join(root, "tools", "gate", "checks", "gate-adapters-test.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstChecks, "gate-adapters-test.json"), rawDecl, 0o644); err != nil {
		t.Fatal(err)
	}

	// Copy the real Python gate adapter scripts and suite into tools/tillr-gate/.
	for _, name := range []string{"change-gate.py", "tree-check.py", "classify.py", "change-gate.test.py"} {
		data, err := os.ReadFile(filepath.Join(root, "tools", "tillr-gate", name))
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, f.repo, filepath.ToSlash(filepath.Join("tools", "tillr-gate", name)), string(data))
	}
	// Minimal Go module stub under tools/gate/ so the Go half of gate-adapters-test compiles cleanly.
	if entries, err := os.ReadDir(filepath.Join(f.repo, "tools", "gate")); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				_ = os.Remove(filepath.Join(f.repo, "tools", "gate", e.Name()))
			}
		}
	}
	writeFixtureFile(t, f.repo, "tools/gate/go.mod", "module example.invalid/gate\n\ngo 1.24\n")
	writeFixtureFile(t, f.repo, "tools/gate/change_gate_test.go", "package main\n\nimport \"testing\"\n\nfunc TestChangeGateStub(t *testing.T) {}\n")
	base := f.commit()
	f.git("branch", "-f", "main", base)

	// Cut a candidate branch and commit a broken gate adapter.
	f.git("checkout", "-qb", "lane-broken-adapter", base)
	writeFixtureFile(t, f.repo, "tools/tillr-gate/change-gate.py", "#!/usr/bin/env python3\nraise RuntimeError('broken gate adapter')\n")
	head := f.commit()

	// Run the landing gate before merging the candidate into main.
	cmd := exec.Command(f.gateBin, "change-gate", "--base", base, "--head", head, "--runner", runner, "--logs", filepath.Join(f.repo, "logs"))
	cmd.Dir = f.repo
	cmd.Env = f.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("landing gate must refuse broken gate adapter with exit 1, got err=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}

	var env struct {
		Payload struct {
			Checks []struct {
				CheckID string `json:"check_id"`
				Result  string `json:"result"`
			} `json:"checks"`
		} `json:"payload"`
	}
	firstLine := strings.Split(strings.TrimSpace(stdout.String()), "\n")[0]
	if err := json.Unmarshal([]byte(firstLine), &env); err != nil {
		t.Fatalf("parse verdict: %v: %s", err, stdout.String())
	}
	foundFail := false
	for _, c := range env.Payload.Checks {
		if c.CheckID == "gate-adapters-test" && c.Result == "fail" {
			foundFail = true
		}
	}
	if !foundFail {
		t.Fatalf("expected gate-adapters-test to fail in landing gate verdict, got %+v", env.Payload.Checks)
	}
	// Because the landing gate refused the candidate, main was not merged and remains at base.
	if gotMain := f.git("rev-parse", "main"); gotMain != base {
		t.Fatalf("main moved to %s despite landing gate refusal (base=%s)", gotMain, base)
	}
}
