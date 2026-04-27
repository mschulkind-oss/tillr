package cli

import (
	"fmt"

	"github.com/mschulkind-oss/tillr/internal/server"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the web viewer",
	RunE: func(cmd *cobra.Command, _ []string) error {
		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		if port, _ := cmd.Flags().GetInt("port"); port != 0 {
			cfg.ServerPort = port
		}

		fmt.Printf("Starting web viewer at http://localhost:%d\n", cfg.ServerPort)
		fmt.Printf("Database: %s\n", cfg.DBPath)
		fmt.Println("Press Ctrl+C to stop.")

		return server.Start(database, server.Config{
			Port:   cfg.ServerPort,
			ApiKey: cfg.ApiKey,
		})
	},
}

func init() {
	serveCmd.Flags().Int("port", 0, "Server port (default: 3847)")
}
