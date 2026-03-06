package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind/lifecycle/internal/config"
	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init [project-name]",
	Short: "Initialize a new lifecycle project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
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
			DBPath:     filepath.Join(cwd, config.DefaultDBName),
			ServerPort: config.DefaultServerPort,
		}
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return fmt.Errorf("creating database: %w", err)
		}
		defer database.Close() //nolint:errcheck

		p, err := engine.InitProject(database, name)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(p)
		}
		fmt.Printf("✓ Initialized project %q in %s\n", p.Name, cwd)
		fmt.Printf("  Database: %s\n", cfg.DBPath)
		fmt.Printf("  Config:   %s\n", cfgPath)
		return nil
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show project status overview",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		overview, err := engine.GetStatusOverview(database)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(overview)
		}

		fmt.Printf("Project: %s\n\n", overview.Project.Name)

		total := 0
		for _, c := range overview.FeatureCounts {
			total += c
		}
		fmt.Printf("Features: %d total\n", total)
		for status, count := range overview.FeatureCounts {
			fmt.Printf("  %-14s %d\n", status, count)
		}

		fmt.Printf("\nMilestones: %d\n", overview.MilestoneCount)
		fmt.Printf("Active Cycles: %d\n", overview.ActiveCycles)

		if len(overview.RecentEvents) > 0 {
			fmt.Println("\nRecent Activity:")
			for _, e := range overview.RecentEvents {
				ts := e.CreatedAt
				fmt.Printf("  [%s] %s", ts, e.EventType)
				if e.FeatureID != "" {
					fmt.Printf(" (%s)", e.FeatureID)
				}
				fmt.Println()
			}
		}
		return nil
	},
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Validate environment and project setup",
	RunE: func(cmd *cobra.Command, args []string) error {
		type Check struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail,omitempty"`
		}
		var checks []Check

		// Check project
		root, err := config.FindProjectRoot()
		if err != nil {
			checks = append(checks, Check{"project", "fail", "No .lifecycle.json found. Run 'lifecycle init <name>'"})
		} else {
			checks = append(checks, Check{"project", "ok", root})
		}

		// Check DB
		if root != "" {
			cfg, err := config.Load(root)
			if err != nil {
				checks = append(checks, Check{"config", "fail", err.Error()})
			} else {
				checks = append(checks, Check{"config", "ok", cfg.DBPath})
				database, err := db.Open(cfg.DBPath)
				if err != nil {
					checks = append(checks, Check{"database", "fail", err.Error()})
				} else {
					_ = database.Close()
					checks = append(checks, Check{"database", "ok", cfg.DBPath})
				}
			}
		}

		if jsonOutput {
			return printJSON(checks)
		}

		allOK := true
		for _, c := range checks {
			icon := "✓"
			if c.Status != "ok" {
				icon = "✗"
				allOK = false
			}
			fmt.Printf("%s %-10s %s\n", icon, c.Name, c.Detail)
		}
		if allOK {
			fmt.Println("\nAll checks passed!")
		}
		return nil
	},
}

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Show event history",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		featureID, _ := cmd.Flags().GetString("feature")
		eventType, _ := cmd.Flags().GetString("type")
		since, _ := cmd.Flags().GetString("since")
		limit, _ := cmd.Flags().GetInt("limit")
		if limit == 0 {
			limit = 50
		}

		events, err := db.ListEvents(database, p.ID, featureID, eventType, since, limit)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(events)
		}

		if len(events) == 0 {
			fmt.Println("No events found.")
			return nil
		}
		for _, e := range events {
			ts := e.CreatedAt
			feat := ""
			if e.FeatureID != "" {
				feat = " [" + e.FeatureID + "]"
			}
			data := ""
			if e.Data != "" {
				var m map[string]any
				if json.Unmarshal([]byte(e.Data), &m) == nil {
					var parts []string
					for k, v := range m {
						parts = append(parts, fmt.Sprintf("%s=%v", k, v))
					}
					data = " " + fmt.Sprint(parts)
				}
			}
			fmt.Printf("%s  %-24s%s%s\n", ts, e.EventType, feat, data)
		}
		return nil
	},
}

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Full-text search across project data",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		events, err := db.SearchEvents(database, p.ID, args[0])
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(events)
		}

		if len(events) == 0 {
			fmt.Println("No results found.")
			return nil
		}
		for _, e := range events {
			ts := e.CreatedAt
			fmt.Printf("%s  %s  %s\n", ts, e.EventType, e.Data)
		}
		return nil
	},
}

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Compact activity log",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		events, err := db.ListEvents(database, p.ID, "", "", "", 30)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(events)
		}

		for _, e := range events {
			ts := e.CreatedAt
			icon := eventIcon(e.EventType)
			feat := ""
			if e.FeatureID != "" {
				feat = " " + e.FeatureID
			}
			fmt.Printf("%s %s %s%s\n", ts, icon, e.EventType, feat)
		}
		return nil
	},
}

func init() {
	historyCmd.Flags().String("feature", "", "Filter by feature ID")
	historyCmd.Flags().String("type", "", "Filter by event type")
	historyCmd.Flags().String("since", "", "Filter by date (ISO 8601)")
	historyCmd.Flags().Int("limit", 50, "Max events to show")
}

func eventIcon(eventType string) string {
	switch {
	case contains(eventType, "created"):
		return "+"
	case contains(eventType, "completed"), contains(eventType, "approved"):
		return "✓"
	case contains(eventType, "failed"), contains(eventType, "rejected"):
		return "✗"
	case contains(eventType, "started"):
		return "▶"
	case contains(eventType, "scored"):
		return "★"
	default:
		return "·"
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
