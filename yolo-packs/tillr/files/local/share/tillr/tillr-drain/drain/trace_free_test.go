package drain_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheLoopRunsNoTraceIndexer pins the owner's 2026-11-18 ruling: the trace index was
// derived data, so the loop no longer builds one. Readers answer from the run artifacts
// on demand, and nothing the loop runs writes an `index.db`.
//
// The planted program sits where the retired indexer's default search looked first, the
// repository's own `tools/tillr-trace/tillr-trace`, and records any call. A loop that still
// ran an index step would find it there. The retired `--trace-bin` and `--trace-root` flags
// are refused rather than accepted and ignored, so no caller is left with a dead switch.
func TestTheLoopRunsNoTraceIndexer(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", "exit 0"))
	marker := filepath.Join(scratch(t, "trace-marker"), "called")
	planted := filepath.Join(f.root, "tools", "tillr-trace", "tillr-trace")
	if err := os.MkdirAll(filepath.Dir(planted), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, planted, "#!/bin/sh\necho \"$@\" >> '"+marker+"'\n")
	if err := os.Chmod(planted, 0o755); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(scratch(t, "trace-free-policy"), "policy.json")
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policy, string(raw))
	args := []string{"loop", "--root", f.root, "--store", f.store, "--policy", policy, "--item-bin", sibling(t, "item"),
		"--lane-root", f.laneRoot, "--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true", "--rolling=", "--exit-when-drained"}
	if code, out := invoke(t, args...); code != 0 {
		t.Fatalf("loop exit=%d: %s", code, out)
	}
	if called, err := os.ReadFile(marker); err == nil {
		t.Fatalf("the loop ran the planted tillr-trace program: %s", called)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, retired := range []string{"--trace-bin=", "--trace-root=" + scratch(t, "retired-root")} {
		code, out := invoke(t, append(append([]string{}, args...), retired)...)
		if code == 0 || !strings.Contains(string(out), "flag provided but not defined") {
			t.Fatalf("%s was accepted: exit=%d: %s", retired, code, out)
		}
	}
}
