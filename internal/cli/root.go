package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mschulkind/lifecycle/internal/config"
	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/spf13/cobra"
)

var jsonOutput bool

var rootCmd = &cobra.Command{
	Use:   "lifecycle",
	Short: "Human-in-the-loop project management for agentic development",
	Long: `Lifecycle is a project management tool that bridges human product owners
and AI agents. It tracks, visualizes, and steers work as it flows through
defined iteration cycles — acting as the project manager for agentic development.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(nextCmd)
	rootCmd.AddCommand(doneCmd)
	rootCmd.AddCommand(failCmd)
	rootCmd.AddCommand(heartbeatCmd)
	rootCmd.AddCommand(featureCmd)
	rootCmd.AddCommand(milestoneCmd)
	rootCmd.AddCommand(roadmapCmd)
	rootCmd.AddCommand(cycleCmd)
	rootCmd.AddCommand(qaCmd)
	rootCmd.AddCommand(historyCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(logCmd)
	rootCmd.AddCommand(serveCmd)
}

func openDB() (*sql.DB, *config.Config, error) {
	root, err := config.FindProjectRoot()
	if err != nil {
		return nil, nil, fmt.Errorf("no lifecycle project found. Run 'lifecycle init <name>' first")
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, nil, fmt.Errorf("loading config: %w", err)
	}
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("opening database: %w", err)
	}
	return database, cfg, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
