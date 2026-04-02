package cli

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/daemon"
	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Start multi-project server daemon",
	Long: `Start the Tillr daemon, serving multiple projects from a single HTTP server.

Projects are configured in ~/.config/tillr/daemon.json:
  {
    "projects": [
      {"path": "/home/user/code/project-a"},
      {"path": "/home/user/code/project-b", "slug": "b"}
    ],
    "port": 3847
  }

Each project's API is available under /api/p/{slug}/...
The project list is at /api/projects.

Use 'tillr daemon init' to create the config file.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, _ := cmd.Flags().GetString("config")
		if configPath == "" {
			configPath = daemon.DefaultConfigPath()
		}

		cfg, err := daemon.LoadConfig(configPath)
		if err != nil {
			return fmt.Errorf("loading daemon config: %w\n\nRun 'tillr daemon init <project-dir>...' to create one", err)
		}

		port, _ := cmd.Flags().GetInt("port")
		if port != 0 {
			cfg.Port = port
		}

		logFile, _ := cmd.Flags().GetString("log-file")
		noLog, _ := cmd.Flags().GetBool("no-log")

		// Default: log to ~/.config/tillr/daemon.log
		if logFile == "" && !noLog {
			logFile = filepath.Join(filepath.Dir(configPath), "daemon.log")
		}

		if logFile != "" {
			_ = os.MkdirAll(filepath.Dir(logFile), 0755)
			f, ferr := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if ferr != nil {
				return fmt.Errorf("opening log file %s: %w", logFile, ferr)
			}
			defer f.Close() //nolint:errcheck
			multi := io.MultiWriter(os.Stdout, f)
			log.SetOutput(multi)
			fmt.Fprintf(os.Stderr, "Logging to %s\n", logFile)
		}

		fmt.Printf("Starting Tillr daemon on http://localhost:%d\n", cfg.Port)
		fmt.Printf("Config: %s\n", configPath)
		fmt.Printf("Projects: %s\n", projectSummary(cfg))
		fmt.Println("Press Ctrl+C to stop.")

		return daemon.StartDaemon(cfg)
	},
}

var daemonInitCmd = &cobra.Command{
	Use:   "init [project-dir...]",
	Short: "Create daemon config file",
	Long: `Create the daemon config file at ~/.config/tillr/daemon.json.

Pass one or more project directories as arguments. Each must contain
a .tillr.json config file and tillr.db database.

Example:
  tillr daemon init ~/code/project-a ~/code/project-b`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, _ := cmd.Flags().GetString("config")
		if configPath == "" {
			configPath = daemon.DefaultConfigPath()
		}

		// Check if config already exists
		if _, err := os.Stat(configPath); err == nil {
			return fmt.Errorf("config already exists at %s — edit it directly or remove it first", configPath)
		}

		if err := daemon.InitConfig(configPath, args); err != nil {
			return err
		}

		fmt.Printf("Created daemon config at %s\n", configPath)
		fmt.Println("Start the daemon with: tillr daemon")
		return nil
	},
}

func projectSummary(cfg *daemon.DaemonConfig) string {
	slugs := make([]string, len(cfg.Projects))
	for i, p := range cfg.Projects {
		slugs[i] = p.Slug
	}
	return strings.Join(slugs, ", ")
}

func init() {
	daemonCmd.Flags().String("config", "", "Path to daemon config (default ~/.config/tillr/daemon.json)")
	daemonCmd.Flags().Int("port", 0, "Override server port")
	daemonCmd.Flags().String("log-file", "", "Log file path (default: ~/.config/tillr/daemon.log)")
	daemonCmd.Flags().Bool("no-log", false, "Disable log file")

	daemonInitCmd.Flags().String("config", "", "Path to daemon config")
	daemonCmd.AddCommand(daemonInitCmd)
}
