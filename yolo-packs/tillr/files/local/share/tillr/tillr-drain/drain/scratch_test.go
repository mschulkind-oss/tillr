package drain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
