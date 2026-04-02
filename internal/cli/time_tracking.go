package cli

import (
	"fmt"
	"math"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/spf13/cobra"
)

var timeCmd = &cobra.Command{
	Use:   "time",
	Short: "Analyze time tracking data",
}

func init() {
	timeCmd.AddCommand(timeShowCmd)
	timeCmd.AddCommand(timeSummaryCmd)
}

// formatDuration converts seconds to a human-readable string like "2h 15m" or "3d 5h".
func formatDuration(totalSec float64) string {
	if totalSec < 0 {
		return "0m"
	}
	sec := int(math.Round(totalSec))
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	days := sec / 86400
	sec %= 86400
	hours := sec / 3600
	sec %= 3600
	minutes := sec / 60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	return strings.Join(parts, " ")
}

var timeShowCmd = &cobra.Command{
	Use:   "show <feature-id>",
	Short: "Show time spent on each work item for a feature",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		featureID := args[0]

		// Verify feature exists
		f, err := db.GetFeature(database, featureID)
		if err != nil {
			return fmt.Errorf("feature %q not found: %w", featureID, err)
		}

		items, err := db.GetWorkItemsWithTime(database, featureID)
		if err != nil {
			return fmt.Errorf("querying work items: %w", err)
		}

		// Compute durations and total
		var totalSec float64
		for i := range items {
			items[i].Duration = formatDuration(items[i].DurationSec)
			totalSec += items[i].DurationSec
		}

		report := struct {
			FeatureID   string `json:"feature_id"`
			FeatureName string `json:"feature_name"`
			Items       []struct {
				ID          int     `json:"id"`
				WorkType    string  `json:"work_type"`
				Status      string  `json:"status"`
				StartedAt   string  `json:"started_at"`
				CompletedAt string  `json:"completed_at"`
				DurationSec float64 `json:"duration_seconds"`
				Duration    string  `json:"duration"`
			} `json:"items"`
			TotalSec      float64 `json:"total_seconds"`
			TotalDuration string  `json:"total_duration"`
		}{
			FeatureID:     f.ID,
			FeatureName:   f.Name,
			TotalSec:      totalSec,
			TotalDuration: formatDuration(totalSec),
		}
		for _, it := range items {
			report.Items = append(report.Items, struct {
				ID          int     `json:"id"`
				WorkType    string  `json:"work_type"`
				Status      string  `json:"status"`
				StartedAt   string  `json:"started_at"`
				CompletedAt string  `json:"completed_at"`
				DurationSec float64 `json:"duration_seconds"`
				Duration    string  `json:"duration"`
			}{
				ID:          it.ID,
				WorkType:    it.WorkType,
				Status:      it.Status,
				StartedAt:   it.StartedAt,
				CompletedAt: it.CompletedAt,
				DurationSec: it.DurationSec,
				Duration:    it.Duration,
			})
		}

		if jsonOutput {
			return printJSON(report)
		}

		fmt.Printf("Time Report: %s (%s)\n", f.Name, f.ID)
		fmt.Println(strings.Repeat("─", 80))

		if len(items) == 0 {
			fmt.Println("No completed work items with time data.")
			return nil
		}

		fmt.Printf("%-6s %-16s %-8s %-22s %-22s %s\n",
			"ID", "WORK TYPE", "STATUS", "STARTED", "COMPLETED", "DURATION")
		for _, it := range items {
			fmt.Printf("%-6d %-16s %-8s %-22s %-22s %s\n",
				it.ID, it.WorkType, it.Status, it.StartedAt, it.CompletedAt, it.Duration)
		}

		fmt.Println(strings.Repeat("─", 80))
		fmt.Printf("Total: %s\n", formatDuration(totalSec))
		return nil
	},
}

var timeSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Project-wide time tracking summary",
	RunE: func(_ *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		summary, err := db.GetProjectTimeSummary(database)
		if err != nil {
			return fmt.Errorf("querying time summary: %w", err)
		}

		// Fill in human-readable durations
		summary.TotalDuration = formatDuration(summary.TotalSec)
		for i := range summary.ByWorkType {
			summary.ByWorkType[i].AvgDuration = formatDuration(summary.ByWorkType[i].AvgSec)
		}
		for i := range summary.TopFeatures {
			summary.TopFeatures[i].Duration = formatDuration(summary.TopFeatures[i].TotalSec)
		}
		for i := range summary.ByStatus {
			summary.ByStatus[i].Duration = formatDuration(summary.ByStatus[i].TotalSec)
		}

		if jsonOutput {
			return printJSON(summary)
		}

		fmt.Printf("Project Time Summary\n")
		fmt.Println(strings.Repeat("─", 60))
		fmt.Printf("Total time tracked: %s\n\n", summary.TotalDuration)

		if len(summary.ByWorkType) > 0 {
			fmt.Println("Average Time by Work Type:")
			fmt.Printf("  %-16s %6s %10s\n", "WORK TYPE", "COUNT", "AVG TIME")
			for _, wt := range summary.ByWorkType {
				fmt.Printf("  %-16s %6d %10s\n", wt.WorkType, wt.Count, wt.AvgDuration)
			}
			fmt.Println()
		}

		if len(summary.TopFeatures) > 0 {
			fmt.Println("Top Features by Time Spent:")
			fmt.Printf("  %-30s %s\n", "FEATURE", "TIME")
			for _, ft := range summary.TopFeatures {
				label := ft.Name
				if len(label) > 28 {
					label = label[:28] + ".."
				}
				fmt.Printf("  %-30s %s\n", label, ft.Duration)
			}
			fmt.Println()
		}

		if len(summary.ByStatus) > 0 {
			fmt.Println("Time by Feature Status:")
			fmt.Printf("  %-16s %s\n", "STATUS", "TIME")
			for _, st := range summary.ByStatus {
				fmt.Printf("  %-16s %s\n", st.Status, st.Duration)
			}
		}

		return nil
	},
}
