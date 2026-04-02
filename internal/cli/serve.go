package cli

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr/internal/config"
	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/server"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the web viewer with live reload",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := config.FindProjectRoot()
		if err != nil {
			return fmt.Errorf("no tillr project found. Run 'tillr init <name>' first")
		}
		cfg, err := config.Load(root)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		port, _ := cmd.Flags().GetInt("port")
		if port != 0 {
			cfg.ServerPort = port
		}

		rateLimit, _ := cmd.Flags().GetFloat64("rate-limit")
		rateBurst, _ := cmd.Flags().GetInt("rate-burst")
		// Use config values as defaults if CLI flags were not explicitly set
		if !cmd.Flags().Changed("rate-limit") && cfg.RateLimit > 0 {
			rateLimit = cfg.RateLimit
		}
		if !cmd.Flags().Changed("rate-burst") && cfg.RateBurst > 0 {
			rateBurst = cfg.RateBurst
		}
		noAuth, _ := cmd.Flags().GetBool("no-auth")
		logFile, _ := cmd.Flags().GetString("log-file")
		noLog, _ := cmd.Flags().GetBool("no-log")

		// Default: auto-log to context/tillr-{port}.log (unique per port so
		// jail and host instances don't collide on the shared filesystem).
		if logFile == "" && !noLog {
			logDir := filepath.Join(root, "context")
			_ = os.MkdirAll(logDir, 0755)
			logFile = filepath.Join(logDir, fmt.Sprintf("tillr-%d.log", cfg.ServerPort))
		}

		if logFile != "" {
			f, ferr := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if ferr != nil {
				return fmt.Errorf("opening log file %s: %w", logFile, ferr)
			}
			defer f.Close() //nolint:errcheck
			multi := io.MultiWriter(os.Stdout, f)
			log.SetOutput(multi)
			fmt.Fprintf(os.Stderr, "Logging to %s\n", logFile)
		}

		apiKey := cfg.ApiKey
		if noAuth {
			apiKey = ""
		}

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return fmt.Errorf("opening database: %w", err)
		}
		defer database.Close() //nolint:errcheck

		fmt.Printf("Starting web viewer at http://localhost:%d\n", cfg.ServerPort)
		fmt.Printf("Database: %s\n", cfg.DBPath)
		if apiKey != "" {
			fmt.Println("API key authentication: enabled")
		}
		fmt.Println("Press Ctrl+C to stop.")

		return server.StartWithConfig(database, server.ServerConfig{
			Port:       cfg.ServerPort,
			DBPath:     cfg.DBPath,
			RateLimit:  rateLimit,
			RateBurst:  rateBurst,
			ApiKey:     apiKey,
			VantageURL: cfg.VantageURL,
		})
	},
}

func init() {
	serveCmd.Flags().Int("port", 0, "Server port (default: 3847)")
	serveCmd.Flags().Float64("rate-limit", 100, "API rate limit in requests per second (0 to disable)")
	serveCmd.Flags().Int("rate-burst", 200, "API rate limit burst capacity")
	serveCmd.Flags().Bool("no-auth", false, "Disable API key authentication even when configured")
	serveCmd.Flags().String("log-file", "", "Write server logs to file (default: context/tillr-{port}.log)")
	serveCmd.Flags().Bool("no-log", false, "Disable automatic log file creation")
}
