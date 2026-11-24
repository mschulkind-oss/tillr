package drain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
)

type scratchParentKey struct {
	t    *testing.T
	root string
}

type scratchParentState struct {
	mu     sync.Mutex
	parent string
	seq    int
}

var scratchParents sync.Map

// scratchUnderDurable creates a numbered directory under a per-test parent inside the
// runner's per-slot scratch directory ($GOTMPDIR, .go-affected-scratch in
// $YOLO_DURABLE_DIR, or non-system $TMPDIR), falling back to $YOLO_DURABLE_DIR and never
// /tmp.
func scratchUnderDurable(t *testing.T) string {
	t.Helper()
	cleanRoot := filepath.Clean(scratchParentRoot())
	key := scratchParentKey{t: t, root: cleanRoot}
	val, _ := scratchParents.LoadOrStore(key, &scratchParentState{})
	state := val.(*scratchParentState)

	state.mu.Lock()
	defer state.mu.Unlock()

	if state.parent != "" {
		if _, err := os.Stat(state.parent); os.IsNotExist(err) {
			state.parent = ""
		} else if err != nil {
			t.Fatalf("cannot stat the scratch parent: %v", err)
		}
	}
	if state.parent == "" {
		if err := os.MkdirAll(cleanRoot, 0o755); err != nil {
			t.Fatalf("cannot create the scratch root: %v", err)
		}
		parent, err := os.MkdirTemp(cleanRoot, sanitizeScratchPattern(t.Name())+"-")
		if err != nil {
			t.Fatalf("cannot create the scratch parent: %v", err)
		}
		state.parent = parent
		t.Cleanup(func() {
			scratchParents.Delete(key)
			_ = os.RemoveAll(parent)
		})
	}
	state.seq++
	dir := filepath.Join(state.parent, fmt.Sprintf("%03d", state.seq))
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("cannot create the scratch directory: %v", err)
	}
	return dir
}

func scratchParentRoot() string {
	if dir := nonSystemTempRoot(os.Getenv("GOTMPDIR")); dir != "" {
		return dir
	}
	durable := strings.TrimSpace(os.Getenv("YOLO_DURABLE_DIR"))
	if isRunnerSlotScratch(durable) {
		return durable
	}
	if dir := nonSystemTempRoot(os.Getenv("TMPDIR")); dir != "" {
		return dir
	}
	if durable != "" {
		return durable
	}
	return filepath.Join("..", "..", "..", ".yolo", "durable")
}

func isRunnerSlotScratch(dir string) bool {
	if dir == "" {
		return false
	}
	clean := filepath.Clean(dir)
	if !strings.HasPrefix(filepath.Base(clean), "slot-") {
		return false
	}
	return filepath.Base(filepath.Dir(filepath.Dir(clean))) == ".go-affected-scratch"
}

func nonSystemTempRoot(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	clean := filepath.Clean(dir)
	switch clean {
	case "/tmp", "/var/tmp", "/private/tmp", "/private/var/tmp":
		return ""
	}
	if (strings.HasPrefix(clean, "/var/folders/") || strings.HasPrefix(clean, "/private/var/folders/")) && filepath.Base(clean) == "T" {
		return ""
	}
	return dir
}

func sanitizeScratchPattern(name string) string {
	var b strings.Builder
	for i := 0; i < len(name) && b.Len() < 32; i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			b.WriteByte(c)
		} else if c == '/' {
			b.WriteByte('_')
		}
	}
	s := strings.Trim(b.String(), "._-")
	if s == "" {
		return "drain-timing"
	}
	return s
}

func TestScratchUnderDurableUsesRunnerSlotScratch(t *testing.T) {
	if os.Getenv("TILLR_TEST_SCRATCH_SIGNAL_HELPER") == "1" {
		first := scratchUnderDurable(t)
		second := scratchUnderDurable(t)
		if err := os.WriteFile(os.Getenv("TILLR_TEST_SCRATCH_RECORD"), []byte(first+"\n"+second+"\n"), 0o600); err != nil {
			t.Fatalf("recording helper scratch paths: %v", err)
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		t.Fatal("process survived SIGKILL")
	}

	outerBase := os.Getenv("YOLO_DURABLE_DIR")
	if outerBase == "" {
		outerBase = filepath.Join("..", "..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(outerBase, 0o755); err != nil {
		t.Fatalf("creating outer test base: %v", err)
	}
	fixtureRoot, err := os.MkdirTemp(outerBase, "timing-test-fixture-")
	if err != nil {
		t.Fatalf("creating fixture root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(fixtureRoot) })

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("finding test executable: %v", err)
	}

	runKilledChild := func(t *testing.T, extraEnv ...string) (string, string) {
		t.Helper()
		recordFile := filepath.Join(fixtureRoot, strings.ReplaceAll(t.Name(), "/", "_")+"-record.txt")
		cmd := exec.Command(exe, "-test.run=^TestScratchUnderDurableUsesRunnerSlotScratch$")
		env := []string{}
		for _, kv := range os.Environ() {
			if strings.HasPrefix(kv, "GOTMPDIR=") || strings.HasPrefix(kv, "TMPDIR=") || strings.HasPrefix(kv, "YOLO_DURABLE_DIR=") {
				continue
			}
			env = append(env, kv)
		}
		env = append(env,
			"TILLR_TEST_SCRATCH_SIGNAL_HELPER=1",
			"TILLR_TEST_SCRATCH_RECORD="+recordFile,
		)
		cmd.Env = append(env, extraEnv...)
		out, runErr := cmd.CombinedOutput()
		if runErr == nil {
			t.Fatalf("killed helper exited 0:\n%s", out)
		}
		raw, readErr := os.ReadFile(recordFile)
		if readErr != nil {
			t.Fatalf("reading helper record after %v: %v\n%s", runErr, readErr, out)
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if len(lines) != 2 {
			t.Fatalf("helper recorded %q, want 2 paths", string(raw))
		}
		return lines[0], lines[1]
	}

	dirEntryNames := func(t *testing.T, dir string) []string {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		slices.Sort(names)
		return names
	}

	t.Run("runner-tmpdir-slot", func(t *testing.T) {
		durableTop := filepath.Join(fixtureRoot, "tmpdir-durable")
		slotDir := filepath.Join(fixtureRoot, "tmpdir-slot-0")
		existing := filepath.Join(durableTop, "drain-timing-existing")
		if err := os.MkdirAll(existing, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOTMPDIR", "")
		t.Setenv("TMPDIR", slotDir)
		t.Setenv("YOLO_DURABLE_DIR", durableTop)

		var parent string
		t.Run("in-process", func(t *testing.T) {
			first := scratchUnderDurable(t)
			second := scratchUnderDurable(t)
			if first == second {
				t.Fatalf("scratchUnderDurable returned duplicate path %s", first)
			}
			if !strings.HasPrefix(first, slotDir+string(filepath.Separator)) || !strings.HasPrefix(second, slotDir+string(filepath.Separator)) {
				t.Fatalf("paths %q and %q must live under runner slot %q", first, second, slotDir)
			}
			if filepath.Dir(first) != filepath.Dir(second) || filepath.Dir(filepath.Dir(first)) != filepath.Clean(slotDir) {
				t.Fatalf("calls did not share one per-test parent under slot: %q vs %q (slot %q)", first, second, slotDir)
			}
			parent = filepath.Dir(first)
			if got := dirEntryNames(t, durableTop); !slices.Equal(got, []string{"drain-timing-existing"}) {
				t.Fatalf("durable top-level entries = %v, want [drain-timing-existing]", got)
			}
		})
		if _, err := os.Stat(parent); !os.IsNotExist(err) {
			t.Fatalf("per-test parent %s remained after test cleanup: %v", parent, err)
		}

		killedFirst, killedSecond := runKilledChild(t,
			"GOTMPDIR=",
			"TMPDIR="+slotDir,
			"YOLO_DURABLE_DIR="+durableTop,
		)
		if filepath.Dir(killedFirst) != filepath.Dir(killedSecond) || filepath.Dir(filepath.Dir(killedFirst)) != filepath.Clean(slotDir) {
			t.Fatalf("killed helper calls did not share one per-test parent under slot: %q vs %q (slot %q)", killedFirst, killedSecond, slotDir)
		}
		if !strings.HasPrefix(killedFirst, slotDir+string(filepath.Separator)) {
			t.Fatalf("killed helper path %q not under slot %q", killedFirst, slotDir)
		}
		if _, err := os.Stat(killedFirst); err != nil {
			t.Fatalf("expected killed helper scratch %s to remain inside slot before runner cleanup: %v", killedFirst, err)
		}
		if got := dirEntryNames(t, durableTop); !slices.Equal(got, []string{"drain-timing-existing"}) {
			t.Fatalf("killed helper left entries at durable top level: %v", got)
		}
	})

	t.Run("runner-gotmpdir-slot", func(t *testing.T) {
		durableTop := filepath.Join(fixtureRoot, "gotmpdir-durable")
		slotDir := filepath.Join(fixtureRoot, "gotmpdir-slot-0")
		if err := os.MkdirAll(durableTop, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOTMPDIR", slotDir)
		t.Setenv("TMPDIR", "/tmp")
		t.Setenv("YOLO_DURABLE_DIR", durableTop)

		got := scratchUnderDurable(t)
		if !strings.HasPrefix(got, slotDir+string(filepath.Separator)) {
			t.Fatalf("scratchUnderDurable = %q, want under GOTMPDIR %q", got, slotDir)
		}
		if entries := dirEntryNames(t, durableTop); len(entries) != 0 {
			t.Fatalf("durable top-level entries = %v, want empty", entries)
		}
	})

	t.Run("go-affected-slot-in-durable", func(t *testing.T) {
		durableTop := filepath.Join(fixtureRoot, "affected-durable")
		slotDir := filepath.Join(durableTop, ".go-affected-scratch", "abc123def456", "slot-0")
		outerTmp := filepath.Join(fixtureRoot, "outer-shared-tmp")
		existing := filepath.Join(durableTop, "drain-timing-existing")
		if err := os.MkdirAll(existing, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(outerTmp, 0o755); err != nil {
			t.Fatal(err)
		}

		killedFirst, killedSecond := runKilledChild(t,
			"GOTMPDIR=",
			"TMPDIR="+outerTmp,
			"YOLO_DURABLE_DIR="+slotDir,
		)
		if filepath.Dir(killedFirst) != filepath.Dir(killedSecond) || filepath.Dir(filepath.Dir(killedFirst)) != filepath.Clean(slotDir) {
			t.Fatalf("killed helper calls did not share one per-test parent under go-affected slot: %q vs %q (slot %q)", killedFirst, killedSecond, slotDir)
		}
		if !strings.HasPrefix(killedFirst, slotDir+string(filepath.Separator)) {
			t.Fatalf("killed helper path %q not under go-affected slot %q", killedFirst, slotDir)
		}
		if got := dirEntryNames(t, outerTmp); len(got) != 0 {
			t.Fatalf("outer shared TMPDIR gained entries %v instead of using go-affected slot", got)
		}
		if got := dirEntryNames(t, durableTop); !slices.Equal(got, []string{".go-affected-scratch", "drain-timing-existing"}) {
			t.Fatalf("durable top-level entries = %v, want [.go-affected-scratch drain-timing-existing]", got)
		}
	})

	t.Run("fallback-durable-never-tmp", func(t *testing.T) {
		durableOnly := filepath.Join(fixtureRoot, "fallback-durable")
		t.Setenv("GOTMPDIR", "")
		t.Setenv("TMPDIR", "/tmp")
		t.Setenv("YOLO_DURABLE_DIR", durableOnly)

		var parent string
		t.Run("sub/with spaces", func(t *testing.T) {
			first := scratchUnderDurable(t)
			second := scratchUnderDurable(t)
			if !strings.HasPrefix(first, durableOnly+string(filepath.Separator)) {
				t.Fatalf("fallback path %q not under %q", first, durableOnly)
			}
			if filepath.Dir(first) != filepath.Dir(second) {
				t.Fatalf("subtest calls did not share one parent: %q vs %q", first, second)
			}
			parent = filepath.Dir(first)
			if got := dirEntryNames(t, durableOnly); len(got) != 1 {
				t.Fatalf("durable fallback entries = %v, want 1 per-test parent", got)
			}
		})
		if _, err := os.Stat(parent); !os.IsNotExist(err) {
			t.Fatalf("fallback per-test parent %s remained after cleanup: %v", parent, err)
		}
	})
}
