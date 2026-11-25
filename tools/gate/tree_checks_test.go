package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func initTempRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return root
}

func trackFile(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "add", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add %s: %v: %s", name, err, out)
	}
}

func TestScratchProseFencesAndFileSpecificExceptions(t *testing.T) {
	root := initTempRepo(t)
	trackFile(t, root, "guide.md", "# Guide\nEvidence `.yolo/durable/report.md`.\n")
	findings, err := scratchCitations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 || !strings.Contains(findings[0], "guide.md:2") {
		t.Fatalf("expected guide.md:2 finding, got %v", findings)
	}

	trackFile(t, root, "guide.md", "```sh\ncat .yolo/durable/report.md\n```\n")
	findings, err = scratchCitations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings inside fence, got %v", findings)
	}

	location := "build into `$YOLO_DURABLE_DIR/bin` and prepend it to PATH.\n"
	trackFile(t, root, "docs/reference/tillr-distribution.md", location)
	findings, err = scratchCitations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected exception for tillr-distribution.md, got %v", findings)
	}

	trackFile(t, root, "guide.md", location)
	findings, err = scratchCitations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		t.Fatalf("expected finding for guide.md, got none")
	}
}

func TestScratchExceptionFollowsDeliveredCopyToItsSource(t *testing.T) {
	root := initTempRepo(t)
	line := "build into `$YOLO_DURABLE_DIR/bin` and prepend it to PATH.\n"
	copyPath := "yolo-packs/tillr/files/local/share/tillr/docs/reference/tillr-distribution.md"
	trackFile(t, root, copyPath, line)
	findings, err := scratchCitations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected delivered copy exception, got %v", findings)
	}

	other := "yolo-packs/tillr/files/local/share/tillr/docs/design/other.md"
	trackFile(t, root, other, line)
	findings, err = scratchCitations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 || !strings.Contains(findings[0], other+":1") {
		t.Fatalf("expected %s:1 finding, got %v", other, findings)
	}
	if err := os.Remove(filepath.Join(root, other)); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "rm", "-q", "--cached", other).CombinedOutput(); err != nil {
		t.Fatalf("git rm: %v: %s", err, out)
	}

	mirror := "yolo-packs/tillr/files/local/share/tillr/tillr-view/README.md"
	trackFile(t, root, mirror, "under `tillr-observatory/<key>/`, where `<key>` names it. $YOLO_DURABLE_DIR\n")
	findings, err = scratchCitations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected mirror exception, got %v", findings)
	}
	if got := excusedAs(mirror); got != "tools/tillr-view/README.md" {
		t.Fatalf("excusedAs(%q) = %q, want tools/tillr-view/README.md", mirror, got)
	}
}

func TestDesignCitationsDocumentAndAnchorResolution(t *testing.T) {
	root := initTempRepo(t)
	trackFile(t, root, "docs/design/target.md", "# Target\n## 3. The section\n<a id=\"html\"></a>\n<!-- vantage: question id=Q1 -->\n")
	for _, citation := range []string{"target.md", "target.md#3-the-section", "target.md#html", "target.md#q1"} {
		trackFile(t, root, "docs/design/citing.md", "---\ndepends-on: "+citation+"\nsupersedes: a framing\n---\n")
		findings, err := designCitationFindings(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != 0 {
			t.Fatalf("citation %q: unexpected findings %v", citation, findings)
		}
	}
	for _, tc := range []struct {
		citation string
		needle   string
	}{
		{"target.md#gone", "no such section"},
		{"gone.md", "names no tracked document"},
	} {
		trackFile(t, root, "docs/design/citing.md", "---\ndepends-on:\n - "+tc.citation+"\n---\n")
		findings, err := designCitationFindings(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) == 0 || !strings.Contains(findings[0], tc.needle) {
			t.Fatalf("citation %q: expected %q, got %v", tc.citation, tc.needle, findings)
		}
	}
	trackFile(t, root, "docs/design/citing.md", "---\ndepends-on: [target.md#html, 'target.md#q1']\n---\n")
	findings, err := designCitationFindings(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("inline list: unexpected findings %v", findings)
	}
}

func TestDesignCitationsSkipDeliveredPackDocsSnapshot(t *testing.T) {
	root := initTempRepo(t)
	snapshotCopy := "yolo-packs/tillr/files/local/share/tillr/docs/design/shipped.md"
	trackFile(t, root, snapshotCopy, "---\ndepends-on:\n  - graduated-design.md\n---\n# Copy\n")
	findings, err := designCitationFindings(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected snapshot copy skipped, got %v", findings)
	}

	trackFile(t, root, "docs/design/live.md", "---\ndepends-on:\n  - graduated-design.md\n---\n")
	findings, err = designCitationFindings(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0], "docs/design/live.md:3") || !strings.Contains(findings[0], "names no tracked document") {
		t.Fatalf("expected 1 finding for live.md:3, got %v", findings)
	}
}

func TestTreeMissingGitIsCouldNotJudge(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "repo-gate")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}
	root := initTempRepo(t)
	for _, name := range []string{"scratch", "design-citations"} {
		cmd := exec.Command(bin, name)
		cmd.Dir = root
		cmd.Env = []string{"PATH=/not-a-tool-directory"}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			t.Fatalf("%s: expected exit 4, got 0", name)
		}
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 4 {
			t.Fatalf("%s: expected exit 4, got %v (stderr: %s)", name, err, stderr.String())
		}
		if !strings.Contains(stderr.String(), "could not judge") {
			t.Fatalf("%s: expected 'could not judge' in stderr, got %q", name, stderr.String())
		}
	}
}

func TestTreeTenFastChecksFitAndExtraDeclarationFails(t *testing.T) {
	root, _ := loadTestContext(t)
	checksDir := filepath.Join(root, "tools", "gate", "checks")
	entries, err := os.ReadDir(checksDir)
	if err != nil {
		t.Fatal(err)
	}
	fastCount := 0
	total := 0
	tmpDir := t.TempDir()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(checksDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var decl struct {
			ID       string `json:"id"`
			Tier     string `json:"tier"`
			BudgetMS int    `json:"budget_ms"`
		}
		if err := json.Unmarshal(raw, &decl); err != nil {
			t.Fatal(err)
		}
		if decl.Tier == "fast" {
			total += decl.BudgetMS
			if decl.ID != "gate-budget" {
				fastCount++
			}
		}
		if err := os.WriteFile(filepath.Join(tmpDir, decl.ID+".json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if fastCount != 11 {
		t.Fatalf("expected 11 fast checks excluding gate-budget, got %d", fastCount)
	}
	bin := buildRepoGateForTest(t)
	if out, err := exec.Command(bin, "gate-budget", checksDir).CombinedOutput(); err != nil {
		t.Fatalf("gate-budget failed: %v: %s", err, out)
	}
	extra, _ := json.Marshal(map[string]any{"tier": "fast", "budget_ms": 9001 - total})
	if err := os.WriteFile(filepath.Join(tmpDir, "extra.json"), extra, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "gate-budget", tmpDir)
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 || !strings.Contains(string(out), "9001") {
		t.Fatalf("expected exit 1 with 9001, got err=%v out=%s", err, out)
	}
}

func TestTreeEveryGoModuleIsBuiltAndHasDeclaredSuite(t *testing.T) {
	root, _ := loadTestContext(t)
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", "tools").Output()
	if err != nil {
		t.Fatal(err)
	}
	modules := make(map[string]bool)
	for _, p := range strings.Split(string(out), "\x00") {
		if strings.HasSuffix(p, "/go.mod") {
			modules[path.Dir(p)] = true
		}
	}
	// tools/gate/go.mod may be untracked until committed, so check filesystem too.
	if _, err := os.Stat(filepath.Join(root, "tools", "gate", "go.mod")); err == nil {
		modules["tools/gate"] = true
	}
	expected := make(map[string]bool, len(goToolDirs))
	for _, d := range goToolDirs {
		expected[d] = true
	}
	if len(modules) != len(expected) {
		t.Fatalf("modules=%v want=%v", modules, expected)
	}
	for m := range modules {
		if !expected[m] {
			t.Fatalf("module %s missing from goToolDirs", m)
		}
	}

	checksDir := filepath.Join(root, "tools", "gate", "checks")
	buildRaw, err := os.ReadFile(filepath.Join(checksDir, "go-build.json"))
	if err != nil {
		t.Fatal(err)
	}
	var buildDecl struct {
		Tier  string   `json:"tier"`
		Reads []string `json:"reads"`
	}
	if err := json.Unmarshal(buildRaw, &buildDecl); err != nil {
		t.Fatal(err)
	}
	if buildDecl.Tier != "fast" {
		t.Fatalf("go-build tier=%q", buildDecl.Tier)
	}
	readDirs := make(map[string]bool)
	for _, r := range buildDecl.Reads {
		if strings.HasSuffix(r, "/") {
			readDirs[r] = true
		}
	}
	for m := range modules {
		if !readDirs[m+"/"] {
			t.Fatalf("go-build.json reads missing %s/", m)
		}
		suiteName := "go-test-" + path.Base(m)
		suiteRaw, err := os.ReadFile(filepath.Join(checksDir, suiteName+".json"))
		if err != nil {
			t.Fatalf("missing %s.json: %v", suiteName, err)
		}
		var suite struct {
			ID      string   `json:"id"`
			Scope   string   `json:"scope"`
			Tier    string   `json:"tier"`
			Reads   []string `json:"reads"`
			Command []string `json:"command"`
		}
		if err := json.Unmarshal(suiteRaw, &suite); err != nil {
			t.Fatal(err)
		}
		if suite.ID != suiteName || suite.Scope != "tree" || suite.Tier != "heavy" {
			t.Fatalf("%s: unexpected metadata %+v", suiteName, suite)
		}
		hasRead := false
		for _, r := range suite.Reads {
			if r == m+"/" {
				hasRead = true
				break
			}
		}
		if !hasRead || !strings.Contains(strings.Join(suite.Command, " "), m) {
			t.Fatalf("%s: missing module read or command", suiteName)
		}
	}
}

func missingGoReads(reads []string, root string) map[string][]string {
	covered := func(p string) bool {
		for _, r := range reads {
			if p == r || (strings.HasSuffix(r, "/") && strings.HasPrefix(p, r)) {
				return true
			}
		}
		return false
	}
	missing := make(map[string][]string)
	for _, r := range reads {
		if !strings.HasSuffix(r, "/") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(r), "go.mod"))
		if err != nil {
			continue
		}
		for _, line := range splitLines(string(raw)) {
			code, _, _ := strings.Cut(line, "//")
			if !strings.Contains(code, "=>") {
				continue
			}
			_, rhs, _ := strings.Cut(code, "=>")
			fields := strings.Fields(rhs)
			if len(fields) == 0 || (!strings.HasPrefix(fields[0], "./") && !strings.HasPrefix(fields[0], "../")) {
				continue
			}
			dep := path.Clean(path.Join(r, fields[0])) + "/"
			if !strings.HasPrefix(dep, "..") && !covered(dep) {
				missing[r] = append(missing[r], dep)
			}
		}
	}
	return missing
}

func TestTreeDeclarationReadingGoModuleReadsInRepoDependencies(t *testing.T) {
	fixture := t.TempDir()
	for _, d := range []string{"tools/app", "tools/lib"} {
		if err := os.MkdirAll(filepath.Join(fixture, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(fixture, "tools", "app", "go.mod"), []byte("module x/app\n\nreplace x/lib => ../lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := missingGoReads([]string{"tools/app/"}, fixture)
	if len(m["tools/app/"]) != 1 || m["tools/app/"][0] != "tools/lib/" {
		t.Fatalf("missingGoReads = %v", m)
	}
	if m2 := missingGoReads([]string{"tools/app/", "tools/lib/"}, fixture); len(m2) != 0 {
		t.Fatalf("expected empty, got %v", m2)
	}

	root, _ := loadTestContext(t)
	checksDir := filepath.Join(root, "tools", "gate", "checks")
	entries, err := os.ReadDir(checksDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(checksDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var decl struct {
			Reads []string `json:"reads"`
		}
		if err := json.Unmarshal(raw, &decl); err != nil {
			t.Fatal(err)
		}
		if got := missingGoReads(decl.Reads, root); len(got) != 0 {
			t.Fatalf("%s: missing Go dependencies in reads: %v", e.Name(), got)
		}
	}
}

func loadShardRunnerFlags(t *testing.T, root string) map[string]bool {
	t.Helper()
	cmd := exec.Command("go", "run", "-C", filepath.Join(root, "tools", "tillr-gate"), ".", "shards", "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tillr-gate shards --help: %v: %s", err, out)
	}
	flags := make(map[string]bool)
	inOptions := false
	for _, line := range splitLines(string(out)) {
		if strings.TrimSpace(line) == "options:" {
			inOptions = true
			continue
		}
		if !inOptions || !strings.HasPrefix(line, "  -") {
			continue
		}
		head := strings.TrimSpace(line)
		if idx := strings.Index(head, "  "); idx >= 0 {
			head = head[:idx]
		}
		for _, part := range strings.Split(head, ",") {
			tok := strings.Fields(strings.TrimSpace(part))
			if len(tok) > 0 && strings.HasPrefix(tok[0], "-") {
				flags[tok[0]] = true
			}
		}
	}
	if len(flags) == 0 {
		t.Fatalf("parsed no flags from tillr-gate shards --help:\n%s", out)
	}
	return flags
}

func checkShardRunnerCommandFlags(command []string, accepted map[string]bool) error {
	var tokens []string
	if len(command) >= 3 && command[0] == "sh" && command[1] == "-c" {
		tokens = strings.Fields(command[2])
	} else {
		tokens = command
	}
	for i := 0; i < len(tokens); i++ {
		tok := strings.Trim(tokens[i], "\"'()")
		isShards := tok == "shards" && i > 0 && (strings.Trim(tokens[i-1], "\"'()") == "." || strings.HasSuffix(strings.Trim(tokens[i-1], "\"'()"), "tillr-gate"))
		if !isShards {
			continue
		}
		for j := i + 1; j < len(tokens); j++ {
			raw := tokens[j]
			if raw == "&&" || raw == "||" || raw == ";" || raw == "|" {
				break
			}
			arg := strings.Trim(raw, "\"'()")
			if strings.HasPrefix(arg, "-") {
				flag := arg
				if eq := strings.IndexByte(flag, '='); eq >= 0 {
					flag = flag[:eq]
				}
				if !accepted[flag] {
					return fmt.Errorf("unrecognized tillr-gate shards flag %q", flag)
				}
			}
			if strings.Contains(raw, ")") {
				break
			}
		}
	}
	return nil
}

func TestTreeGoTestShardsDeclarationsUseAcceptedFlags(t *testing.T) {
	root, _ := loadTestContext(t)
	accepted := loadShardRunnerFlags(t, root)

	if err := checkShardRunnerCommandFlags([]string{
		"sh", "-c", "CGO_ENABLED=0 go run -C tools/tillr-gate . shards tools/gate -k 6 --cache --timeout 0",
	}, accepted); err != nil {
		t.Fatalf("expected valid shard command to pass, got: %v", err)
	}
	if err := checkShardRunnerCommandFlags([]string{
		"sh", "-c", "CGO_ENABLED=0 go run -C tools/tillr-gate . shards tools/gate -k 6 --cache --slot-class proof --timeout 0",
	}, accepted); err == nil {
		t.Fatal("expected --slot-class to fail validation, got nil")
	}

	checksDir := filepath.Join(root, "tools", "gate", "checks")
	entries, err := os.ReadDir(checksDir)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(checksDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var decl struct {
			Command []string `json:"command"`
		}
		if err := json.Unmarshal(raw, &decl); err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(decl.Command, " ")
		if strings.Contains(joined, "tillr-gate") && strings.Contains(joined, " shards ") {
			checked++
			if err := checkShardRunnerCommandFlags(decl.Command, accepted); err != nil {
				t.Fatalf("%s: %v", e.Name(), err)
			}
		}
	}
	if checked == 0 {
		t.Fatal("expected at least one check declaration invoking tillr-gate shards")
	}
}
