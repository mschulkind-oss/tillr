package cli

import (
	"fmt"

	"github.com/mschulkind/lifecycle/internal/config"
	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/server"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the web viewer with live reload",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := config.FindProjectRoot()
		if err != nil {
			return fmt.Errorf("no lifecycle project found. Run 'lifecycle init <name>' first")
		}
		cfg, err := config.Load(root)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		port, _ := cmd.Flags().GetInt("port")
		if port != 0 {
			cfg.ServerPort = port
		}

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return fmt.Errorf("opening database: %w", err)
		}
		defer database.Close() //nolint:errcheck

		fmt.Printf("Starting web viewer at http://localhost:%d\n", cfg.ServerPort)
		fmt.Printf("Database: %s\n", cfg.DBPath)
		fmt.Println("Press Ctrl+C to stop.")

		return server.StartWithDBPath(database, cfg.ServerPort, cfg.DBPath)
	},
}

func init() {
	serveCmd.Flags().Int("port", 0, "Server port (default: 3847)")
}
