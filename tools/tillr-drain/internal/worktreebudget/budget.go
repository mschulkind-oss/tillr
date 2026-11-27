package worktreebudget

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DirBytes records the measured disk usage of one scratch subdirectory.
type DirBytes struct {
	Path  string `json:"path"`
	RunID string `json:"run_id,omitempty"`
	Bytes int64  `json:"bytes"`
}

// ItemUsage holds measured worktree and scratch usage for one directory under LaneRoot.
type ItemUsage struct {
	LaneID        string
	HasWorktree   bool
	InUse         bool
	WorktreeBytes int64
	ScratchBytes  int64
	TotalBytes    int64
	ScratchDirs   []DirBytes
	ModTime       time.Time
}

// Snapshot summarizes worktree count, worktree+scratch bytes, and run log bytes.
type Snapshot struct {
	OnDisk          int                  `json:"on_disk"`
	InUse           int                  `json:"in_use"`
	Idle            int                  `json:"idle"`
	CountLimit      int                  `json:"count_limit"`
	WorktreeBytes   int64                `json:"worktree_bytes"`
	ScratchBytes    int64                `json:"scratch_bytes"`
	TotalBytes      int64                `json:"total_bytes"`
	DiskBudgetBytes int64                `json:"disk_budget_bytes"`
	RunLogBytes     int64                `json:"run_log_bytes"`
	Items           map[string]ItemUsage `json:"-"`
	LargestScratch  []DirBytes           `json:"largest_scratch,omitempty"`
}

// MeasureLaneRoot walks <laneRoot>/* and measures worktree and scratch+durable bytes.
// Missing directories or files that vanish mid-walk do not fail the measurement.
func MeasureLaneRoot(laneRoot string, inUse map[string]bool) (Snapshot, error) {
	snap := Snapshot{
		Items: map[string]ItemUsage{},
	}
	if laneRoot == "" {
		return snap, nil
	}
	entries, err := os.ReadDir(laneRoot)
	if os.IsNotExist(err) {
		return snap, nil
	}
	if err != nil {
		return snap, err
	}

	var allScratch []DirBytes
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		laneID := entry.Name()
		laneDir := filepath.Join(laneRoot, laneID)
		info, err := entry.Info()
		var modTime time.Time
		if err == nil {
			modTime = info.ModTime()
		}
		usage := ItemUsage{
			LaneID:  laneID,
			InUse:   inUse[laneID],
			ModTime: modTime,
		}
		wtPath := filepath.Join(laneDir, "worktree")
		if st, err := os.Stat(wtPath); err == nil && st.IsDir() {
			usage.HasWorktree = true
			usage.WorktreeBytes = dirBytes(wtPath)
			if st.ModTime().After(usage.ModTime) {
				usage.ModTime = st.ModTime()
			}
		}
		for _, sub := range []string{"scratch", "durable"} {
			subPath := filepath.Join(laneDir, sub)
			subEntries, err := os.ReadDir(subPath)
			if err != nil {
				continue
			}
			for _, se := range subEntries {
				p := filepath.Join(subPath, se.Name())
				var b int64
				if se.IsDir() {
					b = dirBytes(p)
					db := DirBytes{Path: p, RunID: se.Name(), Bytes: b}
					usage.ScratchDirs = append(usage.ScratchDirs, db)
					allScratch = append(allScratch, db)
				} else if fi, err := se.Info(); err == nil {
					b = fi.Size()
				}
				usage.ScratchBytes += b
			}
		}
		usage.TotalBytes = usage.WorktreeBytes + usage.ScratchBytes
		snap.WorktreeBytes += usage.WorktreeBytes
		snap.ScratchBytes += usage.ScratchBytes
		snap.TotalBytes += usage.TotalBytes
		if usage.HasWorktree {
			snap.OnDisk++
			if usage.InUse {
				snap.InUse++
			} else {
				snap.Idle++
			}
		}
		// Walk active per-node checkouts and scratch under <laneDir>/nodes/*.
		nodesDir := filepath.Join(laneDir, "nodes")
		if nodeEntries, err := os.ReadDir(nodesDir); err == nil {
			for _, ne := range nodeEntries {
				if !ne.IsDir() || strings.HasPrefix(ne.Name(), ".") {
					continue
				}
				nodeDir := filepath.Join(nodesDir, ne.Name())
				nodeWT := filepath.Join(nodeDir, "worktree")
				if st, err := os.Stat(nodeWT); err == nil && st.IsDir() {
					usage.HasWorktree = true
					b := dirBytes(nodeWT)
					usage.WorktreeBytes += b
					usage.TotalBytes += b
					snap.WorktreeBytes += b
					snap.TotalBytes += b
					if st.ModTime().After(usage.ModTime) {
						usage.ModTime = st.ModTime()
					}
					snap.OnDisk++
					if usage.InUse {
						snap.InUse++
					} else {
						snap.Idle++
					}
				}
				for _, sub := range []string{"scratch", "durable"} {
					subPath := filepath.Join(nodeDir, sub)
					subEntries, err := os.ReadDir(subPath)
					if err != nil {
						continue
					}
					for _, se := range subEntries {
						p := filepath.Join(subPath, se.Name())
						var b int64
						if se.IsDir() {
							b = dirBytes(p)
							db := DirBytes{Path: p, RunID: se.Name(), Bytes: b}
							usage.ScratchDirs = append(usage.ScratchDirs, db)
							allScratch = append(allScratch, db)
						} else if fi, err := se.Info(); err == nil {
							b = fi.Size()
						}
						usage.ScratchBytes += b
						usage.TotalBytes += b
						snap.ScratchBytes += b
						snap.TotalBytes += b
					}
				}
			}
		}
		snap.Items[laneID] = usage
	}

	sort.Slice(allScratch, func(i, j int) bool {
		if allScratch[i].Bytes != allScratch[j].Bytes {
			return allScratch[i].Bytes > allScratch[j].Bytes
		}
		return allScratch[i].Path < allScratch[j].Path
	})
	if len(allScratch) > 5 {
		allScratch = allScratch[:5]
	}
	snap.LargestScratch = allScratch
	return snap, nil
}

// MeasureRunLogs sums bytes under <root>/.tillr/loop.
func MeasureRunLogs(root string) int64 {
	if root == "" {
		return 0
	}
	return dirBytes(filepath.Join(root, ".tillr", "loop"))
}

// FormatBytes formats a byte count in human-readable binary units.
func FormatBytes(n int64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
		tib = 1024 * gib
	)
	switch {
	case n >= tib:
		return fmt.Sprintf("%.1f TiB", float64(n)/float64(tib))
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func dirBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}
