package drain

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandardStepsKeepTheirBuildAfterAnAdapterLanding(t *testing.T) {
	root := scratchUnderDurable(t)
	adapter := filepath.Join(root, "tools", "tillr-loop", "tillr-loop")
	_, source := repositoryFile(t, "tools/tillr-loop/tillr-loop")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(adapter), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, adapter, string(raw))
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "adapter at build")
	steps := &ProcessSteps{Root: root, Store: filepath.Join(root, "absent-store"),
		GateProgram:      "python3 tools/tillr-loop/tillr-loop gate",
		LandProgram:      "python3 " + adapter + " land",
		VerdictProgram:   "python3 " + adapter + " verdict",
		FixerGateProgram: DefaultFixerGateProgram}
	// Land a contract-breaking adapter into the runtime checkout after constructing the steps.
	writeForkFile(t, adapter, "import sys\nprint('adapter from newer landing', file=sys.stderr)\nsys.exit(99)\n")
	git("add", ".")
	git("-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "land newer adapter")
	var report bytes.Buffer
	if err := steps.CheckPrograms(context.Background(), &report); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gate", "fixer gate", "land", "verdict"} {
		if !strings.Contains(report.String(), "step "+name+": build's embedded adapter") {
			t.Errorf("missing selection for %s: %s", name, &report)
		}
	}
	log := adapterStubs(t)
	c := Candidate{ItemID: "item-a", LaneID: "lane-1", RunID: "run-1", Worktree: root, BaseSHA: strings.Repeat("1", 40), HeadSHA: runGit(t, root, "rev-parse", "HEAD")}
	writeRunRecord(t, filepath.Join(root, ".tillr", "loop", c.RunID), "{}\n")
	for _, program := range []string{steps.GateProgram, steps.LandProgram, steps.VerdictProgram, steps.FixerGateProgram} {
		var extra []string
		if strings.HasSuffix(program, " verdict") {
			extra = append(extra, steps.verdictArgs("pass")...)
		}
		if program == steps.FixerGateProgram {
			extra = append(extra, "--only", "fixture/check")
			writeForkFile(t, filepath.Join(root, "tools", "fork-check.py"), "print('named check ran')\n")
		}
		code, _, span, err := steps.program(context.Background(), "fixture", root, program, c, extra...)
		if err != nil || code != 0 {
			t.Errorf("next step used newer adapter: %q: code %d, err %v, stderr %s", program, code, err, span.StderrTail)
		}
	}
	for _, face := range []string{"tillr-gate plan", "tillr-land pass", "tillr-item verdict record"} {
		if !strings.Contains(strings.Join(stubCalls(t, log), "\n"), face) {
			t.Errorf("the built adapter did not execute %s", face)
		}
	}
	// Even deleting the runtime source cannot remove the running build's steps.
	if err := os.Remove(adapter); err != nil {
		t.Fatal(err)
	}
	code, _, _, err := steps.program(context.Background(), "gate", root, steps.GateProgram, c)
	if err != nil || code != 0 {
		t.Fatalf("deleted source changed build's step: %d %v", code, err)
	}
	// A different configured script remains the caller's, not a hidden replacement.
	custom := filepath.Join(root, "custom.py")
	writeForkFile(t, custom, "import os\nprint(os.environ['TILLR_LOOP_ADAPTER_BUILD'])\n")
	code, out, _, err := steps.program(context.Background(), "custom", root, "python3 "+custom, c)
	if err != nil || code != 0 || !strings.Contains(out, "digest") {
		t.Fatalf("custom program/build handoff: %d %q %v", code, out, err)
	}
	steps.GateProgram, steps.LandProgram, steps.VerdictProgram, steps.FixerGateProgram = "python3 "+custom, "python3 "+custom, "python3 "+custom, ""
	report.Reset()
	if err := steps.CheckPrograms(context.Background(), &report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "step gate: configured program") {
		t.Fatalf("custom selection not announced: %s", &report)
	}
}

func TestStandardAdapterSelectionOnlyReplacesTheScript(t *testing.T) {
	root := scratchUnderDurable(t)
	adapter := filepath.Join(root, "tools", "tillr-loop", "tillr-loop")
	if err := os.MkdirAll(filepath.Dir(adapter), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, adapter, "fixture")
	link := filepath.Join(root, "adapter-link")
	if err := os.Symlink(adapter, link); err != nil {
		t.Fatal(err)
	}
	steps := &ProcessSteps{Root: root}
	for _, tc := range []struct {
		command string
		want    int
	}{
		{"python3 tools/tillr-loop/tillr-loop gate", 1},
		{"python3 " + adapter + " land", 1},
		{"python3 -u -B " + adapter + " verdict", 3},
		{"python3 -W ignore -X utf8 -- " + adapter + " gate", 6},
		{adapter + " gate", 0},
		{"python3 " + link + " gate", 1},
		{"python3 custom.py " + adapter, -1},
		{"python3 -c code " + adapter, -1},
		{"python3 -m module " + adapter, -1},
		{"custom --data " + adapter, -1},
		{"python3 other/tools/tillr-loop/tillr-loop gate", -1},
	} {
		if got := steps.standardAdapterArg(strings.Fields(tc.command)); got != tc.want {
			t.Errorf("selection %q = %d, want %d", tc.command, got, tc.want)
		}
	}
}

func TestEmbeddedAdapterMatchesItsSource(t *testing.T) {
	_, source := repositoryFile(t, "tools/tillr-loop/tillr-loop")
	copy := filepath.Join(filepath.Dir(source), "..", "tillr-drain", "drain", "tillr-loop.py")
	want, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(copy)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("embedded adapter drift: run python3 tools/tillr-loop/sync-adapter.py")
	}
}
