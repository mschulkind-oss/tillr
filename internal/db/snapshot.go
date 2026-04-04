package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/internal/models"
)

// DefaultMaxSnapshots is the retention limit when no config override is set.
const DefaultMaxSnapshots = 10

// PreMutationSnapshot creates a backup of the database before a destructive
// batch/bulk operation. The snapshot is stored in .tillr-backups/ next to the
// database, logged to the events table, and old snapshots are pruned to stay
// within maxSnapshots. Pass maxSnapshots <= 0 to use DefaultMaxSnapshots.
func PreMutationSnapshot(database *sql.DB, dbPath, operationName string, maxSnapshots int) (string, error) {
	if maxSnapshots <= 0 {
		maxSnapshots = DefaultMaxSnapshots
	}

	backupDir := filepath.Join(filepath.Dir(dbPath), ".tillr-backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("creating backup directory: %w", err)
	}

	ts := time.Now().Format("2006-01-02T15-04-05")
	// Sanitise operation name for use in a filename.
	safeName := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, operationName)
	filename := fmt.Sprintf("pre-%s-%s.db", safeName, ts)
	destPath := filepath.Join(backupDir, filename)

	// Use VACUUM INTO for a consistent snapshot even under concurrent access.
	_, err := database.Exec(fmt.Sprintf(`VACUUM INTO '%s'`, strings.ReplaceAll(destPath, "'", "''")))
	if err != nil {
		return "", fmt.Errorf("creating pre-mutation snapshot: %w", err)
	}

	// Log the snapshot to the events table.
	p, pErr := GetProject(database)
	if pErr == nil {
		_ = InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			EventType: "db.pre_mutation_snapshot",
			Data:      fmt.Sprintf(`{"operation":%q,"path":%q}`, operationName, destPath),
		})
	}

	// Prune old automatic snapshots (files matching "pre-*.db").
	pruneSnapshots(backupDir, maxSnapshots)

	return destPath, nil
}

// pruneSnapshots removes the oldest pre-mutation snapshots when the count
// exceeds maxKeep. Only files matching "pre-*.db" are considered.
func pruneSnapshots(backupDir string, maxKeep int) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return
	}

	type snapshotEntry struct {
		path    string
		modTime time.Time
	}
	var snapshots []snapshotEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "pre-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		snapshots = append(snapshots, snapshotEntry{
			path:    filepath.Join(backupDir, e.Name()),
			modTime: info.ModTime(),
		})
	}

	if len(snapshots) <= maxKeep {
		return
	}

	// Sort newest first, then delete extras.
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].modTime.After(snapshots[j].modTime)
	})
	for _, s := range snapshots[maxKeep:] {
		_ = os.Remove(s.path)
	}
}
