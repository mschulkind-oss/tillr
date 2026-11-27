//go:build linux

package run

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTriageMountOpsBindsActiveLaneRootReadOnly(t *testing.T) {
	t.Run("items-root", func(t *testing.T) {
		root := t.TempDir()
		itemsRoot := filepath.Join(root, ".tillr", "items")
		laneDir := filepath.Join(itemsRoot, "triage-1")
		workDir := filepath.Join(laneDir, "worktree")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			t.Fatal(err)
		}

		mounts := triageMountOps(root, itemsRoot, laneDir, workDir, "", "", filepath.Join(root, ".tillr"))
		assertMount := func(dst string, wantRO bool) int {
			t.Helper()
			idx := -1
			for i, m := range mounts {
				if m.Dst == dst && m.ReadOnly == wantRO {
					if idx != -1 {
						t.Fatalf("duplicate mount for %s (ro=%v): %+v", dst, wantRO, mounts)
					}
					idx = i
				}
			}
			if idx == -1 {
				t.Fatalf("missing mount for %s (ro=%v) in %+v", dst, wantRO, mounts)
			}
			return idx
		}

		roIdx := assertMount(itemsRoot, true)
		rwIdx := assertMount(laneDir, false)
		if roIdx >= rwIdx {
			t.Fatalf("lane root read-only mount (%d) must precede writable laneDir mount (%d)", roIdx, rwIdx)
		}
	})

	t.Run("lanes-and-custom-root", func(t *testing.T) {
		root := t.TempDir()
		lanesRoot := filepath.Join(root, ".tillr", "lanes")
		itemsRoot := filepath.Join(root, ".tillr", "items")
		customRoot := filepath.Join(t.TempDir(), "custom-lanes")
		laneDir := filepath.Join(customRoot, "triage-2")
		for _, d := range []string{lanesRoot, itemsRoot, laneDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}

		mounts := triageMountOps(root, customRoot, laneDir, filepath.Join(laneDir, "worktree"), "", "", "")
		wantRO := map[string]bool{customRoot: false, lanesRoot: false, itemsRoot: false}
		var laneDirIdx, customIdx int
		for i, m := range mounts {
			if _, ok := wantRO[m.Dst]; ok && m.ReadOnly {
				if wantRO[m.Dst] {
					t.Fatalf("duplicate read-only mount for %s: %+v", m.Dst, mounts)
				}
				wantRO[m.Dst] = true
				if m.Dst == customRoot {
					customIdx = i
				}
			}
			if m.Dst == laneDir && !m.ReadOnly {
				laneDirIdx = i
			}
		}
		for dir, seen := range wantRO {
			if !seen {
				t.Fatalf("missing read-only mount for %s in %+v", dir, mounts)
			}
		}
		if customIdx >= laneDirIdx {
			t.Fatalf("custom lane root read-only mount (%d) must precede writable laneDir mount (%d)", customIdx, laneDirIdx)
		}
	})
}
