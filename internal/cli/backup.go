package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Backup the project database",
	Long: `Create a timestamped backup of the lifecycle database.

Backups are stored in .lifecycle-backups/ by default. Use --output to specify
a custom path. The backup uses SQLite's VACUUM INTO for a consistent snapshot
that is safe even when the server is running.`,
	Args: cobra.NoArgs,
	RunE: runBackup,
}

var backupListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available backups",
	Args:  cobra.NoArgs,
	RunE:  runBackupList,
}

var restoreCmd = &cobra.Command{
	Use:   "restore <backup-file>",
	Short: "Restore database from a backup",
	Long: `Restore the lifecycle database from a backup file.

This is a destructive operation — the current database will be overwritten.
A pre-restore backup is created automatically before restoring.

Requires --confirm flag to proceed.`,
	Args: cobra.ExactArgs(1),
	RunE: runRestore,
}

func init() {
	backupCmd.AddCommand(backupListCmd)

	backupCmd.Flags().StringP("output", "o", "", "Custom output path for backup file")

	restoreCmd.Flags().Bool("confirm", false, "Confirm the destructive restore operation")
}

func runBackup(cmd *cobra.Command, _ []string) error {
	database, cfg, err := openDB()
	if err != nil {
		return err
	}
	defer database.Close() //nolint:errcheck

	output, _ := cmd.Flags().GetString("output")

	var destPath string
	if output != "" {
		destPath = output
		if !filepath.IsAbs(destPath) {
			destPath = filepath.Join(cfg.ProjectDir, destPath)
		}
		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("creating output directory: %w", err)
		}
	} else {
		backupDir := filepath.Join(cfg.ProjectDir, ".lifecycle-backups")
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			return fmt.Errorf("creating backup directory: %w", err)
		}
		ts := time.Now().Format("20060102-150405")
		destPath = filepath.Join(backupDir, fmt.Sprintf("lifecycle-%s.db", ts))
	}

	// Use VACUUM INTO for a consistent, safe backup even with concurrent access
	_, err = database.Exec(fmt.Sprintf(`VACUUM INTO '%s'`, strings.ReplaceAll(destPath, "'", "''")))
	if err != nil {
		return fmt.Errorf("creating backup: %w", err)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		return fmt.Errorf("reading backup file: %w", err)
	}

	result := BackupResult{
		Path: destPath,
		Size: info.Size(),
		Time: time.Now().UTC().Format(time.RFC3339),
	}

	if jsonOutput {
		return printJSON(result)
	}

	fmt.Printf("✓ Backup created: %s (%s)\n", destPath, formatSize(info.Size()))
	return nil
}

func runBackupList(_ *cobra.Command, _ []string) error {
	_, cfg, err := openDB()
	if err != nil {
		return err
	}

	backupDir := filepath.Join(cfg.ProjectDir, ".lifecycle-backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			if jsonOutput {
				return printJSON([]BackupInfo{})
			}
			fmt.Println("No backups found.")
			return nil
		}
		return fmt.Errorf("reading backup directory: %w", err)
	}

	var backups []BackupInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		backups = append(backups, BackupInfo{
			Name:    e.Name(),
			Path:    filepath.Join(backupDir, e.Name()),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}

	// Sort newest first
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].ModTime > backups[j].ModTime
	})

	if jsonOutput {
		return printJSON(backups)
	}

	if len(backups) == 0 {
		fmt.Println("No backups found.")
		return nil
	}

	fmt.Printf("%-40s  %10s  %s\n", "NAME", "SIZE", "MODIFIED")
	for _, b := range backups {
		fmt.Printf("%-40s  %10s  %s\n", b.Name, formatSize(b.Size), b.ModTime)
	}
	return nil
}

func runRestore(cmd *cobra.Command, args []string) error {
	confirm, _ := cmd.Flags().GetBool("confirm")
	if !confirm {
		return fmt.Errorf("restore is a destructive operation. Use --confirm to proceed")
	}

	backupFile := args[0]

	// Resolve the backup file path
	if !filepath.IsAbs(backupFile) {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		backupFile = filepath.Join(cwd, backupFile)
	}

	// Verify backup file exists and is readable
	backupInfo, err := os.Stat(backupFile)
	if err != nil {
		return fmt.Errorf("backup file not found: %w", err)
	}
	if backupInfo.IsDir() {
		return fmt.Errorf("backup path is a directory, not a file")
	}

	database, cfg, err := openDB()
	if err != nil {
		return err
	}

	// Get current DB info for the "what will be overwritten" message
	currentInfo, err := os.Stat(cfg.DBPath)
	if err != nil {
		database.Close() //nolint:errcheck
		return fmt.Errorf("reading current database: %w", err)
	}

	// Create pre-restore backup using VACUUM INTO
	backupDir := filepath.Join(cfg.ProjectDir, ".lifecycle-backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		database.Close() //nolint:errcheck
		return fmt.Errorf("creating backup directory: %w", err)
	}
	ts := time.Now().Format("20060102-150405")
	preRestorePath := filepath.Join(backupDir, fmt.Sprintf("lifecycle-pre-restore-%s.db", ts))

	_, err = database.Exec(fmt.Sprintf(`VACUUM INTO '%s'`, strings.ReplaceAll(preRestorePath, "'", "''")))
	if err != nil {
		database.Close() //nolint:errcheck
		return fmt.Errorf("creating pre-restore backup: %w", err)
	}

	// Close DB before overwriting
	database.Close() //nolint:errcheck

	// Copy backup file over the current database
	backupData, err := os.ReadFile(backupFile)
	if err != nil {
		return fmt.Errorf("reading backup file: %w", err)
	}
	if err := os.WriteFile(cfg.DBPath, backupData, 0o644); err != nil {
		return fmt.Errorf("writing database: %w", err)
	}

	// Remove any WAL/SHM files from the old database
	os.Remove(cfg.DBPath + "-wal") //nolint:errcheck
	os.Remove(cfg.DBPath + "-shm") //nolint:errcheck

	result := RestoreResult{
		RestoredFrom:    backupFile,
		PreRestorePath:  preRestorePath,
		OverwrittenSize: currentInfo.Size(),
		RestoredSize:    backupInfo.Size(),
		Time:            time.Now().UTC().Format(time.RFC3339),
	}

	if jsonOutput {
		return printJSON(result)
	}

	fmt.Printf("✓ Restored from: %s (%s)\n", backupFile, formatSize(backupInfo.Size()))
	fmt.Printf("  Overwritten: %s (%s)\n", cfg.DBPath, formatSize(currentInfo.Size()))
	fmt.Printf("  Pre-restore backup: %s\n", preRestorePath)
	return nil
}

// BackupResult is the JSON output for the backup command.
type BackupResult struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Time string `json:"time"`
}

// BackupInfo is the JSON output for a single backup in the list command.
type BackupInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// RestoreResult is the JSON output for the restore command.
type RestoreResult struct {
	RestoredFrom    string `json:"restored_from"`
	PreRestorePath  string `json:"pre_restore_path"`
	OverwrittenSize int64  `json:"overwritten_size"`
	RestoredSize    int64  `json:"restored_size"`
	Time            string `json:"time"`
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
