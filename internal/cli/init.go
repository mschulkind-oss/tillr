package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr/internal/config"
	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init <project-name>",
	Short: "Initialize a new tillr project in the current directory",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		name := args[0]

		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		cfgPath := filepath.Join(cwd, config.ConfigFileName)
		if _, err := os.Stat(cfgPath); err == nil {
			return fmt.Errorf("project already initialized in %s", cwd)
		}

		cfg := &config.Config{
			ProjectDir: cwd,
			DBPath:     config.DefaultDBName,
			ServerPort: config.DefaultServerPort,
		}
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

		database, err := db.Open(filepath.Join(cwd, cfg.DBPath))
		if err != nil {
			return fmt.Errorf("opening database: %w", err)
		}
		defer database.Close() //nolint:errcheck

		project, err := db.CreateProject(database, name)
		if err != nil {
			return fmt.Errorf("creating project: %w", err)
		}

		if jsonOutput {
			return printJSON(project)
		}
		fmt.Printf("Initialized project %q in %s\n", project.Name, cwd)
		fmt.Printf("  Database: %s\n", cfg.DBPath)
		fmt.Printf("  Config:   %s\n", cfgPath)
		return nil
	},
}
