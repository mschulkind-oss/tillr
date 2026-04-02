package cli

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/spf13/cobra"
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Audit log operations",
	Long: `Query and export the project event/audit log.

  tillr audit tail                    Show recent events
  tillr audit stats                   Event counts by type
  tillr audit export [--format json]  Export audit log`,
}

var auditTailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Show recent audit events",
	RunE: func(cmd *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		limit, _ := cmd.Flags().GetInt("limit")
		if limit <= 0 {
			limit = 20
		}

		events, err := db.ListEvents(database, p.ID, "", "", "", limit)
		if err != nil {
			return fmt.Errorf("listing events: %w", err)
		}

		if jsonOutput {
			if events == nil {
				events = []models.Event{}
			}
			return printJSON(events)
		}

		if len(events) == 0 {
			fmt.Println("No events found.")
			return nil
		}

		for _, e := range events {
			featureStr := ""
			if e.FeatureID != "" {
				featureStr = fmt.Sprintf(" [%s]", e.FeatureID)
			}
			fmt.Printf("%s  %-30s%s  %s\n", e.CreatedAt, e.EventType, featureStr, auditTruncate(e.Data, 80))
		}
		return nil
	},
}

var auditStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show event counts by type",
	RunE: func(_ *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		stats, err := db.GetEventStats(database, p.ID)
		if err != nil {
			return fmt.Errorf("getting event stats: %w", err)
		}

		if jsonOutput {
			return printJSON(stats)
		}

		fmt.Printf("%-40s %s\n", "EVENT TYPE", "COUNT")
		fmt.Printf("%-40s %s\n", strings.Repeat("-", 40), strings.Repeat("-", 8))
		total := 0
		for _, s := range stats {
			fmt.Printf("%-40s %d\n", s.EventType, s.Count)
			total += s.Count
		}
		fmt.Printf("\n%-40s %d\n", "TOTAL", total)
		return nil
	},
}

var auditExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export audit log",
	Long: `Export the audit/event log in various formats.

Supported formats: json (default), csv, jsonl`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		since, _ := cmd.Flags().GetString("since")
		until, _ := cmd.Flags().GetString("until")
		eventType, _ := cmd.Flags().GetString("type")
		featureID, _ := cmd.Flags().GetString("feature")
		format, _ := cmd.Flags().GetString("format")
		output, _ := cmd.Flags().GetString("output")

		events, err := db.ListEventsFiltered(database, p.ID, featureID, eventType, since, until, 0)
		if err != nil {
			return fmt.Errorf("listing events: %w", err)
		}
		if events == nil {
			events = []models.Event{}
		}

		var writer *os.File
		if output != "" {
			writer, err = os.Create(output)
			if err != nil {
				return fmt.Errorf("creating output file: %w", err)
			}
			defer writer.Close() //nolint:errcheck
		} else {
			writer = os.Stdout
		}

		switch format {
		case "csv":
			w := csv.NewWriter(writer)
			_ = w.Write([]string{"id", "project_id", "feature_id", "event_type", "data", "created_at"})
			for _, e := range events {
				_ = w.Write([]string{
					fmt.Sprintf("%d", e.ID),
					e.ProjectID,
					e.FeatureID,
					e.EventType,
					e.Data,
					e.CreatedAt,
				})
			}
			w.Flush()
		case "jsonl":
			enc := json.NewEncoder(writer)
			for _, e := range events {
				_ = enc.Encode(e)
			}
		default:
			enc := json.NewEncoder(writer)
			enc.SetIndent("", "  ")
			_ = enc.Encode(events)
		}

		if output != "" {
			fmt.Fprintf(os.Stderr, "Exported %d events to %s (%s format)\n", len(events), output, format)
		}
		return nil
	},
}

func auditTruncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

var analyticsCmd = &cobra.Command{
	Use:   "analytics",
	Short: "Analytics and insights",
}

var analyticsHeatmapCmd = &cobra.Command{
	Use:   "heatmap",
	Short: "Show activity heatmap by hour-of-day and day-of-week",
	RunE: func(_ *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		grid, err := db.GetHeatmapGrid(database, p.ID)
		if err != nil {
			return fmt.Errorf("getting heatmap data: %w", err)
		}

		if jsonOutput {
			return printJSON(grid)
		}

		dayNames := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
		fmt.Printf("Activity Heatmap (events by hour-of-day and day-of-week)\n\n")
		fmt.Printf("%-4s", "")
		for h := 0; h < 24; h++ {
			fmt.Printf("%3d", h)
		}
		fmt.Println()

		for d := 0; d < 7; d++ {
			fmt.Printf("%-4s", dayNames[d])
			for h := 0; h < 24; h++ {
				count := grid.Cells[d*24+h]
				if count == 0 {
					fmt.Print("  .")
				} else {
					fmt.Printf("%3d", count)
				}
			}
			fmt.Println()
		}
		return nil
	},
}

func init() {
	auditTailCmd.Flags().Int("limit", 20, "Number of events to show")

	auditExportCmd.Flags().String("since", "", "Start date (YYYY-MM-DD)")
	auditExportCmd.Flags().String("until", "", "End date (YYYY-MM-DD)")
	auditExportCmd.Flags().String("type", "", "Filter by event type")
	auditExportCmd.Flags().String("feature", "", "Filter by feature ID")
	auditExportCmd.Flags().String("format", "json", "Output format: json, csv, jsonl")
	auditExportCmd.Flags().StringP("output", "o", "", "Output file (default: stdout)")

	auditCmd.AddCommand(auditTailCmd)
	auditCmd.AddCommand(auditStatsCmd)
	auditCmd.AddCommand(auditExportCmd)

	analyticsCmd.AddCommand(analyticsHeatmapCmd)
}
