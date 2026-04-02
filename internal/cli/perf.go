package cli

import (
	"fmt"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/spf13/cobra"
)

var perfCmd = &cobra.Command{
	Use:   "perf",
	Short: "Performance metrics and monitoring",
	Long:  "View CLI command execution times, DB query counts, and other performance data.",
}

func init() {
	perfCmd.AddCommand(perfShowCmd)
	perfShowCmd.Flags().Int("limit", 10, "Number of recent slow commands to show")
}

var perfShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show performance metrics",
	Long: `Display aggregated performance metrics including:
  - Overall command counts and average durations
  - P95 latency
  - Per-command breakdown
  - Recent slow commands

Examples:
  tillr perf show              # Show perf summary
  tillr perf show --json       # JSON output
  tillr perf show --limit 20   # Show top 20 slow commands`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		limit, _ := cmd.Flags().GetInt("limit")
		summary, err := db.GetPerfSummary(database, limit)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(summary)
		}

		// Human-readable output
		fmt.Println("Performance Metrics")
		fmt.Println("===================")
		fmt.Println()

		if summary.TotalCommands == 0 {
			fmt.Println("No command metrics recorded yet.")
			fmt.Println("Metrics are collected automatically as you use tillr commands.")
			return nil
		}

		fmt.Printf("  Total Commands:   %d\n", summary.TotalCommands)
		fmt.Printf("  Avg Duration:     %.1f ms\n", summary.AvgDurationMs)
		fmt.Printf("  P95 Duration:     %.1f ms\n", summary.P95DurationMs)
		fmt.Printf("  Success Rate:     %.1f%%\n", summary.SuccessRate)
		fmt.Println()

		if len(summary.ByCommand) > 0 {
			fmt.Println("Per-Command Breakdown")
			fmt.Println("---------------------")
			fmt.Printf("  %-25s %6s %10s %10s %8s\n", "COMMAND", "COUNT", "AVG (ms)", "MAX (ms)", "SUCCESS")
			for _, s := range summary.ByCommand {
				fmt.Printf("  %-25s %6d %10.1f %10.1f %7.0f%%\n",
					s.Command, s.Count, s.AvgDurationMs, s.MaxDurationMs, s.SuccessRate)
			}
			fmt.Println()
		}

		if len(summary.RecentSlow) > 0 {
			fmt.Println("Slowest Commands")
			fmt.Println("----------------")
			for _, m := range summary.RecentSlow {
				status := "✓"
				if !m.Success {
					status = "✗"
				}
				fmt.Printf("  %s %-25s %8.1f ms  %s\n", status, m.Command, m.DurationMs, m.CreatedAt)
			}
		}

		return nil
	},
}
