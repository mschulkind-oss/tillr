package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind/lifecycle/internal/config"
	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/export"
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
			DBPath:     config.DefaultDBName,
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
	Long: `Doctor validates the lifecycle environment and project health.

It checks for required tools, project configuration, database integrity,
and provides actionable suggestions for any issues found.

Checks performed:
  project     .lifecycle.json found
  config      Configuration file valid
  database    SQLite database opens and has expected tables
  schema      Schema version matches expected migration count
  orphans     No orphaned work items or cycle references
  git         Git repository detected
  go          Go toolchain available (required version)
  gh          GitHub CLI available (optional, enables GitHub integration)
  skills      Agent configuration files (AGENTS.md, copilot-instructions.md)

Each check reports: ✓ ok, ! warn, or ✗ fail with fix suggestions.`,
	Example: `  lifecycle doctor          # Human-readable health report
  lifecycle doctor --json   # Structured output for automation`,
	RunE: func(cmd *cobra.Command, args []string) error {
		type Check struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail,omitempty"`
			Fix    string `json:"fix,omitempty"`
		}
		type HealthSummary struct {
			FeatureTotal      int            `json:"feature_total"`
			FeatureCounts     map[string]int `json:"feature_counts"`
			FeaturesWithSpecs int            `json:"features_with_specs"`
			Milestones        int            `json:"milestones"`
			RoadmapItems      int            `json:"roadmap_items"`
			Discussions       int            `json:"discussions"`
		}
		type DoctorResult struct {
			Checks []Check        `json:"checks"`
			Health *HealthSummary `json:"health,omitempty"`
		}
		var checks []Check
		var health *HealthSummary

		// Check project
		root, err := config.FindProjectRoot()
		if err != nil {
			checks = append(checks, Check{
				Name:   "project",
				Status: "fail",
				Detail: "No .lifecycle.json found",
				Fix:    "Run 'lifecycle init <name>' or 'lifecycle onboard' to initialize",
			})
		} else {
			checks = append(checks, Check{Name: "project", Status: "ok", Detail: root})
		}

		// Check config and DB
		var database *sql.DB
		var projectID string
		if root != "" {
			cfg, err := config.Load(root)
			if err != nil {
				checks = append(checks, Check{
					Name:   "config",
					Status: "fail",
					Detail: err.Error(),
					Fix:    "Check .lifecycle.json is valid JSON",
				})
			} else {
				checks = append(checks, Check{Name: "config", Status: "ok", Detail: cfg.DBPath})

				// Database health check
				d, err := db.Open(cfg.DBPath)
				if err != nil {
					checks = append(checks, Check{
						Name:   "database",
						Status: "fail",
						Detail: err.Error(),
						Fix:    "Check database file permissions or re-initialize with 'lifecycle init'",
					})
				} else {
					database = d
					// Verify database has expected tables
					dbHealthDetail := cfg.DBPath
					var tableCount int
					row := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
					if err := row.Scan(&tableCount); err == nil {
						dbHealthDetail = fmt.Sprintf("%s (%d tables)", cfg.DBPath, tableCount)
					}
					if p, err := db.GetProject(database); err == nil {
						projectID = p.ID
						checks = append(checks, Check{Name: "database", Status: "ok", Detail: dbHealthDetail})
					} else {
						checks = append(checks, Check{
							Name:   "database",
							Status: "warn",
							Detail: "Database exists but no project found",
							Fix:    "Run 'lifecycle init <name>' to create a project",
						})
					}
				}
			}
		}

		// Check git
		gitCmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
		if out, err := gitCmd.Output(); err == nil && strings.TrimSpace(string(out)) == "true" {
			checks = append(checks, Check{Name: "git", Status: "ok", Detail: "git repository detected"})
		} else {
			checks = append(checks, Check{
				Name:   "git",
				Status: "warn",
				Detail: "Not a git repository",
				Fix:    "Run 'git init' to initialize version control",
			})
		}

		// Check go version
		goCmd := exec.Command("go", "version")
		if out, err := goCmd.Output(); err == nil {
			version := strings.TrimSpace(string(out))
			parts := strings.Fields(version)
			if len(parts) >= 3 {
				version = parts[2]
			}
			checks = append(checks, Check{Name: "go", Status: "ok", Detail: version})
		} else {
			checks = append(checks, Check{
				Name:   "go",
				Status: "warn",
				Detail: "Go not found",
				Fix:    "Install Go 1.24+ from https://go.dev/dl/ or via 'mise install go'",
			})
		}

		// Check GitHub CLI
		if _, err := exec.LookPath("gh"); err == nil {
			ghCmd := exec.Command("gh", "auth", "status")
			if err := ghCmd.Run(); err == nil {
				checks = append(checks, Check{Name: "gh", Status: "ok", Detail: "GitHub CLI authenticated"})
			} else {
				checks = append(checks, Check{
					Name:   "gh",
					Status: "warn",
					Detail: "GitHub CLI found but not authenticated",
					Fix:    "Run 'gh auth login' to enable GitHub integration",
				})
			}
		} else {
			checks = append(checks, Check{
				Name:   "gh",
				Status: "warn",
				Detail: "GitHub CLI not found (optional)",
				Fix:    "Install from https://cli.github.com/ for GitHub issue/PR integration",
			})
		}

		// Check skills/agent configuration
		cwd, _ := os.Getwd()
		var skillsFound []string
		skillFiles := []struct {
			path string
			name string
		}{
			{"AGENTS.md", "AGENTS.md"},
			{".github/copilot-instructions.md", "Copilot Instructions"},
			{".cursorrules", "Cursor Rules"},
			{".clinerules", "Cline Rules"},
		}
		for _, sf := range skillFiles {
			if _, err := os.Stat(filepath.Join(cwd, sf.path)); err == nil {
				skillsFound = append(skillsFound, sf.name)
			}
		}
		if len(skillsFound) > 0 {
			checks = append(checks, Check{
				Name:   "skills",
				Status: "ok",
				Detail: fmt.Sprintf("Agent configs: %s", strings.Join(skillsFound, ", ")),
			})
		} else {
			checks = append(checks, Check{
				Name:   "skills",
				Status: "warn",
				Detail: "No agent configuration files found",
				Fix:    "Create AGENTS.md or .github/copilot-instructions.md for agent guidance",
			})
		}

		// Build health summary if DB is available
		if database != nil && projectID != "" {
			defer database.Close() //nolint:errcheck

			// Check schema version
			var schemaVersion int
			row := database.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version")
			if err := row.Scan(&schemaVersion); err == nil {
				expected := db.ExpectedMigrationCount()
				if schemaVersion == expected {
					checks = append(checks, Check{
						Name:   "schema",
						Status: "ok",
						Detail: fmt.Sprintf("schema version %d/%d", schemaVersion, expected),
					})
				} else {
					checks = append(checks, Check{
						Name:   "schema",
						Status: "warn",
						Detail: fmt.Sprintf("schema version %d, expected %d", schemaVersion, expected),
						Fix:    "Re-open the database to apply pending migrations, or re-initialize with 'lifecycle init'",
					})
				}
			}

			// Check for orphaned work items (referencing non-existent features)
			var orphanedWorkItems int
			row = database.QueryRow(`
				SELECT COUNT(*) FROM work_items
				WHERE feature_id NOT IN (SELECT id FROM features)`)
			if err := row.Scan(&orphanedWorkItems); err == nil && orphanedWorkItems > 0 {
				checks = append(checks, Check{
					Name:   "orphans",
					Status: "warn",
					Detail: fmt.Sprintf("%d work item(s) reference non-existent features", orphanedWorkItems),
					Fix:    "These may be left over from deleted features; consider cleaning up the database",
				})
			}

			// Check for orphaned cycle instances (referencing non-existent features)
			var orphanedCycles int
			row = database.QueryRow(`
				SELECT COUNT(*) FROM cycle_instances
				WHERE feature_id NOT IN (SELECT id FROM features)`)
			if err := row.Scan(&orphanedCycles); err == nil && orphanedCycles > 0 {
				checks = append(checks, Check{
					Name:   "orphans",
					Status: "warn",
					Detail: fmt.Sprintf("%d cycle instance(s) reference non-existent features", orphanedCycles),
					Fix:    "These may be left over from deleted features; consider cleaning up the database",
				})
			}

			if orphanedWorkItems == 0 && orphanedCycles == 0 {
				checks = append(checks, Check{
					Name:   "orphans",
					Status: "ok",
					Detail: "no orphaned work items or cycle references",
				})
			}

			health = &HealthSummary{}

			if counts, err := db.FeatureCounts(database, projectID); err == nil {
				health.FeatureCounts = counts
				for _, c := range counts {
					health.FeatureTotal += c
				}
			}
			if total, withSpecs, err := db.CountFeaturesWithoutSpecs(database, projectID); err == nil {
				_ = total
				health.FeaturesWithSpecs = withSpecs
			}
			if count, err := db.CountMilestones(database, projectID); err == nil {
				health.Milestones = count
			}
			if stats, err := db.GetRoadmapStats(database, projectID); err == nil {
				health.RoadmapItems = stats.Total
			}
			if count, err := db.CountDiscussions(database, projectID); err == nil {
				health.Discussions = count
			}
		} else if database != nil {
			_ = database.Close()
		}

		if jsonOutput {
			return printJSON(DoctorResult{Checks: checks, Health: health})
		}

		allOK := true
		for _, c := range checks {
			icon := "✓"
			switch c.Status {
			case "fail":
				icon = "✗"
				allOK = false
			case "warn":
				icon = "!"
			}
			detail := c.Detail
			if c.Fix != "" && c.Status != "ok" {
				detail = fmt.Sprintf("%s\n    → %s", c.Detail, c.Fix)
			}
			fmt.Printf("%s %-10s %s\n", icon, c.Name, detail)
		}

		if health != nil {
			fmt.Println("\nProject Health:")
			// Features line
			if health.FeatureTotal > 0 {
				var parts []string
				for status, count := range health.FeatureCounts {
					parts = append(parts, fmt.Sprintf("%d %s", count, status))
				}
				fmt.Printf("  Features:     %d (%s)\n", health.FeatureTotal, strings.Join(parts, ", "))
			} else {
				fmt.Printf("  Features:     0\n")
			}
			// Specs
			specIcon := "✓"
			if health.FeaturesWithSpecs < health.FeatureTotal {
				specIcon = "!"
			}
			fmt.Printf("  With specs:   %d/%d %s\n", health.FeaturesWithSpecs, health.FeatureTotal, specIcon)
			fmt.Printf("  Milestones:   %d\n", health.Milestones)
			fmt.Printf("  Roadmap:      %d items\n", health.RoadmapItems)
			fmt.Printf("  Discussions:  %d\n", health.Discussions)
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

var historyExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export event history for auditing",
	Long:  "Export lifecycle events in JSON, CSV, or Markdown format for compliance and auditing.",
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
		until, _ := cmd.Flags().GetString("until")
		format, _ := cmd.Flags().GetString("format")
		output, _ := cmd.Flags().GetString("output")

		events, err := db.ListEventsFiltered(database, p.ID, featureID, eventType, since, until, 0)
		if err != nil {
			return err
		}

		var w *os.File
		if output != "" {
			w, err = os.Create(output)
			if err != nil {
				return fmt.Errorf("creating output file: %w", err)
			}
			defer w.Close() //nolint:errcheck
		} else {
			w = os.Stdout
		}

		if err := export.Events(events, w, format); err != nil {
			return fmt.Errorf("exporting events: %w", err)
		}

		if output != "" {
			fmt.Fprintf(os.Stderr, "Exported %d events to %s\n", len(events), output)
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

	historyExportCmd.Flags().String("format", "json", "Output format (json, csv, markdown)")
	historyExportCmd.Flags().String("since", "", "Include events from this date (ISO 8601)")
	historyExportCmd.Flags().String("until", "", "Include events until this date (ISO 8601)")
	historyExportCmd.Flags().String("type", "", "Filter by event type")
	historyExportCmd.Flags().String("feature", "", "Filter by feature ID")
	historyExportCmd.Flags().StringP("output", "o", "", "Write to file instead of stdout")

	historyCmd.AddCommand(historyExportCmd)
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
