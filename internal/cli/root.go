package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mschulkind-oss/tillr/internal/config"
	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/version"
	"github.com/spf13/cobra"
)

var jsonOutput bool

var rootCmd = &cobra.Command{
	Use:   "tillr",
	Short: "Human-in-the-loop project management for agentic development",
	Long: `Tillr is a project management tool that bridges human product owners
and AI agents. The post-reset surface is intentionally minimal —
features and comments only — and grows per docs/consulting-firm/roadmap.md.

QUICK START
  tillr init my-project          Create a project in the current directory
  tillr serve                    Start the web viewer at :3847
  tillr feature add "Title"      Create a feature
  tillr feature list             List features
  tillr feature show <id>        Show a feature with its comment thread
  tillr comment <feature> "..."  Add a comment to a feature

Use "tillr [command] --help" for detailed information about any command.
Use "tillr --json" on any command for structured output.`,
	Version: version.Version,
}

// Execute is the entry point invoked from cmd/tillr/main.go.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		if jsonOutput {
			out := map[string]string{"error": err.Error()}
			data, _ := json.MarshalIndent(out, "", "  ")
			fmt.Fprintln(os.Stderr, string(data))
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		}
		os.Exit(ExitUserError)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(featureCmd)
	rootCmd.AddCommand(commentCmd)
}

// openDB opens the project database from the discovered .tillr.json.
// Returned (db, cfg, nil) on success; (nil, nil, err) otherwise. Caller
// is responsible for closing the returned *sql.DB.
func openDB() (*sql.DB, *config.Config, error) {
	root, err := config.FindProjectRoot()
	if err != nil {
		return nil, nil, fmt.Errorf("no tillr project found (run 'tillr init <name>')")
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

// printJSON writes v as indented JSON to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
