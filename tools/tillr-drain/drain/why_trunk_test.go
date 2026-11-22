package drain_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

// trunkRepo is a repository on main with two commits whose green pointer is the first,
// proved at 2026-11-19T15:45:00Z. It answers the root, green and tip.
func trunkRepo(t *testing.T) (root, green, tip string) {
	t.Helper()
	root = scratch(t, "trunk-repo")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	for _, name := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", name)
		git("commit", "-qm", name)
		if green == "" {
			green = git("rev-parse", "HEAD")
		}
	}
	tip = git("rev-parse", "HEAD")
	git("update-ref", "refs/tillr/green", green)
	dir := filepath.Join(root, ".git", "tillr-gate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	proof, _ := json.Marshal(map[string]any{"commit": green, "proved_at": "2026-11-19T15:45:00Z"})
	if err := os.WriteFile(filepath.Join(dir, "green-"+green+".json"), proof, 0o644); err != nil {
		t.Fatal(err)
	}
	return root, green, tip
}

func writeHeavyReport(t *testing.T, root string, report map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(report)
	if err := os.WriteFile(filepath.Join(root, ".git", "tillr-gate", "heavy.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func trunkLines(out []byte) []string {
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "trunk:") {
			lines = append(lines, l)
		}
	}
	return lines
}

const trunkNow = "2026-11-20T00:00:00Z" // 8h15m after the proof

func TestWhyOneLineNamesUnprovedTrunkAndFailingCheck(t *testing.T) {
	root, green, tip := trunkRepo(t)
	writeHeavyReport(t, root, map[string]any{"result": "fail", "tip_sha": tip, "base_sha": green, "checks": []map[string]any{{"check_id": "go-tool/tools-tillr-run-s-whole-suite-passes", "result": "fail"}}})
	path := fixture(t)
	code, out := invoke(t, "why", "--store", path, "--root", root, "--now", trunkNow)
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	lines := trunkLines(out)
	if len(lines) != 1 {
		t.Fatalf("trunk lines=%q in\n%s", lines, out)
	}
	for _, want := range []string{"unproved for 8h15m0s", "green " + green[:12], "tip " + tip[:12], "failing: go-tool/tools-tillr-run-s-whole-suite-passes"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("line %q lacks %q", lines[0], want)
		}
	}
	code, out = invoke(t, "why", "--store", path, "--root", root, "--now", trunkNow, "--json")
	var answer drain.WhyAnswer
	if code != 0 || json.Unmarshal(out, &answer) != nil || answer.TrunkLine != lines[0] || answer.Trunk == nil || len(answer.Trunk.FailingChecks) != 1 {
		t.Fatalf("json: %d %+v\n%s", code, answer, out)
	}
}

func TestWhyOneLineReportsUnprovedTrunkWithNoVerdict(t *testing.T) {
	root, _, _ := trunkRepo(t)
	writeHeavyReport(t, root, map[string]any{"result": "unknown", "detail": "heavy runner\nfaulted:\n  socket closed", "checks": []map[string]any{}})
	code, out := invoke(t, "why", "--store", fixture(t), "--root", root, "--now", trunkNow)
	lines := trunkLines(out)
	if code != 0 || len(lines) != 1 {
		t.Fatalf("why: %d %q\n%s", code, lines, out)
	}
	for _, want := range []string{"unproved for 8h15m0s", "failing: none (heavy=unknown: heavy runner faulted: socket closed)"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("line %q lacks %q", lines[0], want)
		}
	}
}

func TestWhyPrintsNoTrunkLineWhenTrunkIsProved(t *testing.T) {
	root, _, tip := trunkRepo(t)
	if out, err := exec.Command("git", "-C", root, "update-ref", "refs/tillr/green", tip).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	writeHeavyReport(t, root, map[string]any{"result": "pass", "checks": []map[string]any{}})
	code, out := invoke(t, "why", "--store", fixture(t), "--root", root, "--now", trunkNow)
	if code != 0 || len(trunkLines(out)) != 0 {
		t.Fatalf("proved trunk printed a line: %d\n%s", code, out)
	}
}
