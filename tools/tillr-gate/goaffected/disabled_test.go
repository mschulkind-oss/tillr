package goaffected_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-gate/goaffected"
)

const disabledPkg = "example.com/tillr-m/a"

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for path, body := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func sel(tests ...string) string {
	var out []string
	for _, tn := range tests {
		out = append(out, `{"selector":"go-test:tools/tillr-m:`+disabledPkg+`:`+tn+`","source":"hand"}`)
	}
	return `{"version":1,"disabled":[` + strings.Join(out, ",") + `]}`
}

// disabledRepo is a git repository whose main branch lists trunkList and whose worktree
// lists worktreeList.
func disabledRepo(t *testing.T, trunkList, worktreeList string) string {
	t.Helper()
	repo := t.TempDir()
	base := map[string]string{
		".tillr.toml":                   "[gate]\ndisabled_tests = \"gate/disabled.json\"\n",
		"tools/gate/go-test-edges.json": "{}",
		"tools/tillr-m/go.mod":          "module example.com/tillr-m\n\ngo 1.23\n",
		"tools/tillr-m/a/a_test.go":     "package a\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {}\nfunc TestTwo(t *testing.T) {}\nfunc TestThree(t *testing.T) {}\n",
	}
	writeFiles(t, repo, base)
	writeFiles(t, repo, map[string]string{"gate/disabled.json": trunkList})
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "base")
	writeFiles(t, repo, map[string]string{"gate/disabled.json": worktreeList})
	return repo
}

func TestLoadDisabledTestsUnionsWorktreeAndTrunkTipMinusProved(t *testing.T) {
	repo := disabledRepo(t, sel("TestTrunk", "TestBoth/sub"), sel("TestWorktree", "TestBoth/sub"))
	d, err := goaffected.LoadDisabledTests(repo, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(d.For("tools/tillr-m", disabledPkg), ","); got != "TestBoth/sub,TestTrunk,TestWorktree" {
		t.Fatalf("union = %q", got)
	}
	d, err = goaffected.LoadDisabledTests(repo, []string{"go-test:tools/tillr-m:" + disabledPkg + ":TestTrunk"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(d.For("tools/tillr-m", disabledPkg), ","); got != "TestBoth/sub,TestWorktree" {
		t.Fatalf("after --prove-disabled = %q", got)
	}
	if top, subs := d.Split("tools/tillr-m", disabledPkg); strings.Join(top, ",") != "TestWorktree" || strings.Join(subs, ",") != "TestBoth/sub" {
		t.Fatalf("Split = %v %v", top, subs)
	}
}

func TestLoadDisabledTestsBrokenTrunkBlobContributesNothingButBrokenWorktreeFails(t *testing.T) {
	repo := disabledRepo(t, `{"version":1,"disabled":[{"selector":"bad selector","source":"hand"}]}`, sel("TestWorktree"))
	var warn bytes.Buffer
	d, err := goaffected.LoadDisabledTests(repo, nil, &warn)
	if err != nil {
		t.Fatalf("a broken trunk blob must not fail the lane: %v", err)
	}
	if got := strings.Join(d.For("tools/tillr-m", disabledPkg), ","); got != "TestWorktree" || !strings.Contains(warn.String(), "trunk tip") {
		t.Fatalf("got %q, warn %q", got, warn.String())
	}
	writeFiles(t, repo, map[string]string{"gate/disabled.json": "not json"})
	if _, err := goaffected.LoadDisabledTests(repo, nil, nil); err == nil {
		t.Fatal("a malformed worktree list must fail the run")
	}
	// Outside a git repository the trunk tip contributes nothing.
	plain := t.TempDir()
	writeFiles(t, plain, map[string]string{"tillr-disabled-tests.json": sel("TestOne")})
	d, err = goaffected.LoadDisabledTests(plain, nil, nil)
	if err != nil || strings.Join(d.For("tools/tillr-m", disabledPkg), ",") != "TestOne" {
		t.Fatalf("plain dir: %v %v", d, err)
	}
}

func runDisabled(t *testing.T, repo string, item goaffected.SelectedPackage, disabled *goaffected.DisabledTests) [][]string {
	t.Helper()
	t.Setenv("YOLO_DURABLE_DIR", filepath.Join(repo, ".durable"))
	var cmds [][]string
	_, _, _, err := goaffected.RunSelectedTests(repo, &goaffected.Selection{Selected: []goaffected.SelectedPackage{item}}, goaffected.RunOptions{
		DisableProgress: true,
		Disabled:        disabled,
		RunCommand: func(cmd []string, cwd string, env []string, onLine func(string)) (int, string, error) {
			cmds = append(cmds, cmd)
			return 0, "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return cmds
}

func TestRunSelectedTestsExcludesListedTests(t *testing.T) {
	repo := disabledRepo(t, sel("TestOne", "TestTwo/sub"), sel())
	d, err := goaffected.LoadDisabledTests(repo, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	item := goaffected.SelectedPackage{Module: "tools/tillr-m", ImportPath: disabledPkg, DirRel: "tools/tillr-m/a", Reason: "direct"}

	// Unsharded whole package: no -run, a -skip joining the top-level test and the subtest.
	cmds := runDisabled(t, repo, item, d)
	if len(cmds) != 1 || strings.Join(cmds[0], " ") != "go test -json -timeout 0 -p 1 -parallel 1 -skip ^TestOne$|^TestTwo$/^sub$ "+disabledPkg {
		t.Fatalf("unsharded: %v", cmds)
	}
	// Without the list nothing is skipped.
	if cmds := runDisabled(t, repo, item, nil); strings.Contains(strings.Join(cmds[0], " "), "-skip") {
		t.Fatalf("an empty set must add no -skip: %v", cmds)
	}

	// Narrowed: the listed top-level test leaves the -run pattern; the subtest is passed to -skip.
	item.SelectedTests = []string{"TestOne", "TestTwo", "TestThree"}
	cmds = runDisabled(t, repo, item, d)
	if len(cmds) != 1 || !strings.Contains(strings.Join(cmds[0], " "), `-run ^(TestTwo|TestThree)$ -skip ^TestTwo$/^sub$`) {
		t.Fatalf("narrowed: %v", cmds)
	}
	// Narrowed to listed tests only: nothing runs.
	item.SelectedTests = []string{"TestOne"}
	if cmds := runDisabled(t, repo, item, d); len(cmds) != 0 {
		t.Fatalf("a package whose selected tests are all listed must not run: %v", cmds)
	}
}

func TestRunSelectedTestsExcludesListedTestsFromShardedRuns(t *testing.T) {
	repo := disabledRepo(t, sel("TestOne"), sel())
	var body strings.Builder
	body.WriteString("package a\n\nimport \"testing\"\n\n")
	for i := 0; i < 130; i++ {
		body.WriteString("func Test" + strings.Repeat("X", 1) + string(rune('A'+i/26)) + string(rune('a'+i%26)) + "(t *testing.T) {}\n")
	}
	body.WriteString("func TestOne(t *testing.T) {}\n")
	writeFiles(t, repo, map[string]string{"tools/tillr-m/a/many_test.go": body.String()})
	d, err := goaffected.LoadDisabledTests(repo, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	item := goaffected.SelectedPackage{Module: "tools/tillr-m", ImportPath: disabledPkg, DirRel: "tools/tillr-m/a", Reason: "direct"}
	cmds := runDisabled(t, repo, item, d)
	if len(cmds) < 2 {
		t.Fatalf("expected a sharded run, got %v", cmds)
	}
	for _, c := range cmds {
		if strings.Contains(strings.Join(c, " "), "TestOne") {
			t.Fatalf("a listed test must not be assigned to a shard: %v", c)
		}
	}
	if cmds := runDisabled(t, repo, item, nil); !strings.Contains(strings.Join(joinAll(cmds), " "), "TestOne") {
		t.Fatalf("without the list TestOne runs: %v", cmds)
	}
}

func joinAll(cmds [][]string) []string {
	var out []string
	for _, c := range cmds {
		out = append(out, c...)
	}
	return out
}

func TestVerdictDigestChangesWhenTheDisabledSetChanges(t *testing.T) {
	repo := disabledRepo(t, sel(), sel())
	if out, err := exec.Command("go", "version").CombinedOutput(); err != nil {
		t.Skipf("go unavailable: %s", out)
	}
	graph, err := goaffected.LoadTillrGraph(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	edges, err := goaffected.LoadEdges(repo)
	if err != nil {
		t.Fatal(err)
	}
	digest := func(listed string) string {
		writeFiles(t, repo, map[string]string{"gate/disabled.json": listed})
		d, err := goaffected.LoadDisabledTests(repo, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		cache := goaffected.NewVerdictDigestCache()
		cache.Disabled = d
		return goaffected.ComputePackageVerdictDigest(repo, disabledPkg, graph, edges, nil, cache)
	}
	none, listed := digest(sel()), digest(sel("TestOne"))
	if none == "" || listed == "" || none == listed {
		t.Fatalf("a disabled test must change the digest: %q vs %q", none, listed)
	}
	if again := digest(sel()); again != none {
		t.Fatal("re-enabling must restore the digest")
	}
}
