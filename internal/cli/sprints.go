package cli

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/spf13/cobra"
)

var sprintCmd = &cobra.Command{
	Use:   "sprint",
	Short: "Manage sprints",
}

func init() {
	sprintCmd.AddCommand(sprintCreateCmd)
	sprintCmd.AddCommand(sprintAddCmd)
	sprintCmd.AddCommand(sprintRemoveCmd)
	sprintCmd.AddCommand(sprintListCmd)
	sprintCmd.AddCommand(sprintShowCmd)
	sprintCmd.AddCommand(sprintActiveCmd)
	sprintCmd.AddCommand(sprintCloseCmd)

	sprintCreateCmd.Flags().String("start", "", "Start date (YYYY-MM-DD, default: today)")
	sprintCreateCmd.Flags().String("end", "", "End date (YYYY-MM-DD, default: 2 weeks from start)")
	sprintCreateCmd.Flags().String("goal", "", "Sprint goal")
}

var sprintCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new sprint",
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

		// Only one active sprint at a time
		hasActive, err := db.HasActiveSprint(database, p.ID)
		if err != nil {
			return fmt.Errorf("checking active sprints: %w", err)
		}
		if hasActive {
			return fmt.Errorf("an active sprint already exists. Close it first with 'tillr sprint close <id>'")
		}

		startStr, _ := cmd.Flags().GetString("start")
		endStr, _ := cmd.Flags().GetString("end")
		goal, _ := cmd.Flags().GetString("goal")

		now := time.Now()
		startDate := now
		if startStr != "" {
			startDate, err = time.Parse("2006-01-02", startStr)
			if err != nil {
				return fmt.Errorf("invalid start date %q (use YYYY-MM-DD): %w", startStr, err)
			}
		}

		endDate := startDate.AddDate(0, 0, 14) // default 2 weeks
		if endStr != "" {
			endDate, err = time.Parse("2006-01-02", endStr)
			if err != nil {
				return fmt.Errorf("invalid end date %q (use YYYY-MM-DD): %w", endStr, err)
			}
		}

		if !endDate.After(startDate) {
			return fmt.Errorf("end date must be after start date")
		}

		id := strings.ToLower(strings.ReplaceAll(args[0], " ", "-"))
		s := &models.Sprint{
			ID:        id,
			ProjectID: p.ID,
			Name:      args[0],
			Goal:      goal,
			StartDate: startDate.Format("2006-01-02"),
			EndDate:   endDate.Format("2006-01-02"),
			Status:    "active",
		}

		if err := db.CreateSprint(database, s); err != nil {
			return fmt.Errorf("creating sprint %q: %w", args[0], err)
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			EventType: "sprint.created",
			Data:      fmt.Sprintf(`{"id":%q,"name":%q}`, id, args[0]),
		})

		if jsonOutput {
			return printJSON(s)
		}
		fmt.Printf("✓ Created sprint %q (id: %s, %s → %s)\n", s.Name, s.ID, s.StartDate, s.EndDate)
		return nil
	},
}

var sprintAddCmd = &cobra.Command{
	Use:   "add <sprint-id> <feature-id> [feature-id...]",
	Short: "Add features to a sprint",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		sprintID := args[0]
		s, err := db.GetSprint(database, sprintID)
		if err != nil {
			return fmt.Errorf("sprint %q not found. Run 'tillr sprint list' to see available sprints", sprintID)
		}
		if s.Status == "closed" {
			return fmt.Errorf("sprint %q is closed. Cannot add features to a closed sprint", sprintID)
		}

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		var added []string
		for _, fid := range args[1:] {
			if _, err := db.GetFeature(database, fid); err != nil {
				return fmt.Errorf("feature %q not found", fid)
			}
			if err := db.AddFeatureToSprint(database, sprintID, fid); err != nil {
				if strings.Contains(err.Error(), "UNIQUE constraint") || strings.Contains(err.Error(), "PRIMARY KEY") {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "⚠ Feature %q already in sprint %q\n", fid, sprintID)
					continue
				}
				return fmt.Errorf("adding feature %q to sprint: %w", fid, err)
			}
			added = append(added, fid)
		}

		if len(added) > 0 {
			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				EventType: "sprint.features_added",
				Data:      fmt.Sprintf(`{"sprint_id":%q,"features":%q}`, sprintID, strings.Join(added, ",")),
			})
		}

		if jsonOutput {
			updated, _ := db.GetSprint(database, sprintID)
			return printJSON(updated)
		}
		fmt.Printf("✓ Added %d feature(s) to sprint %q\n", len(added), sprintID)
		return nil
	},
}

var sprintRemoveCmd = &cobra.Command{
	Use:   "remove <sprint-id> <feature-id>",
	Short: "Remove a feature from a sprint",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		sprintID, featureID := args[0], args[1]

		if err := db.RemoveFeatureFromSprint(database, sprintID, featureID); err != nil {
			return err
		}

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}
		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			EventType: "sprint.feature_removed",
			Data:      fmt.Sprintf(`{"sprint_id":%q,"feature_id":%q}`, sprintID, featureID),
		})

		if jsonOutput {
			updated, _ := db.GetSprint(database, sprintID)
			return printJSON(updated)
		}
		fmt.Printf("✓ Removed feature %q from sprint %q\n", featureID, sprintID)
		return nil
	},
}

var sprintListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all sprints",
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

		sprints, err := db.ListSprints(database, p.ID)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(sprints)
		}

		if len(sprints) == 0 {
			fmt.Println("No sprints found. Create one with 'tillr sprint create <name>'.")
			return nil
		}

		for _, s := range sprints {
			pct := 0
			if s.TotalFeatures > 0 {
				pct = (s.DoneFeatures * 100) / s.TotalFeatures
			}
			bar := progressBar(pct, 20)
			statusLabel := "○ closed"
			if s.Status == "active" {
				statusLabel = "● active"
			}
			fmt.Printf("%-20s %s %3d%% (%d/%d)  %s  %s → %s\n",
				s.ID, bar, pct, s.DoneFeatures, s.TotalFeatures, statusLabel, s.StartDate, s.EndDate)
		}
		return nil
	},
}

var sprintShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show sprint details with feature list and progress",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		s, err := db.GetSprint(database, args[0])
		if err != nil {
			return fmt.Errorf("sprint %q not found. Run 'tillr sprint list' to see available sprints", args[0])
		}

		features, err := db.ListSprintFeatures(database, args[0])
		if err != nil {
			return fmt.Errorf("listing sprint features: %w", err)
		}

		if jsonOutput {
			return printJSON(struct {
				*models.Sprint
				Features []models.Feature `json:"features"`
			}{s, features})
		}

		pct := 0
		if s.TotalFeatures > 0 {
			pct = (s.DoneFeatures * 100) / s.TotalFeatures
		}

		fmt.Printf("Sprint: %s\n", s.Name)
		fmt.Printf("  ID:     %s\n", s.ID)
		fmt.Printf("  Status: %s\n", s.Status)
		fmt.Printf("  Dates:  %s → %s\n", s.StartDate, s.EndDate)
		if s.Goal != "" {
			fmt.Printf("  Goal:   %s\n", s.Goal)
		}
		fmt.Printf("  Progress: %s %d%% (%d/%d)\n", progressBar(pct, 20), pct, s.DoneFeatures, s.TotalFeatures)

		if len(features) > 0 {
			fmt.Println("\n  Features:")
			for _, f := range features {
				icon := statusIcon(f.Status)
				fmt.Printf("    %s %-14s %-30s [%s]\n", icon, f.ID, f.Name, f.Status)
			}
		}
		return nil
	},
}

var sprintActiveCmd = &cobra.Command{
	Use:   "active",
	Short: "Show the current active sprint",
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

		s, err := db.GetActiveSprint(database, p.ID)
		if err != nil {
			if err == sql.ErrNoRows {
				if jsonOutput {
					return printJSON(nil)
				}
				fmt.Println("No active sprint. Create one with 'tillr sprint create <name>'.")
				return nil
			}
			return err
		}

		features, err := db.ListSprintFeatures(database, s.ID)
		if err != nil {
			return fmt.Errorf("listing sprint features: %w", err)
		}

		if jsonOutput {
			return printJSON(struct {
				*models.Sprint
				Features []models.Feature `json:"features"`
			}{s, features})
		}

		pct := 0
		if s.TotalFeatures > 0 {
			pct = (s.DoneFeatures * 100) / s.TotalFeatures
		}

		fmt.Printf("Active Sprint: %s\n", s.Name)
		fmt.Printf("  ID:     %s\n", s.ID)
		fmt.Printf("  Dates:  %s → %s\n", s.StartDate, s.EndDate)
		if s.Goal != "" {
			fmt.Printf("  Goal:   %s\n", s.Goal)
		}
		fmt.Printf("  Progress: %s %d%% (%d/%d)\n", progressBar(pct, 20), pct, s.DoneFeatures, s.TotalFeatures)

		if len(features) > 0 {
			fmt.Println("\n  Features:")
			for _, f := range features {
				icon := statusIcon(f.Status)
				fmt.Printf("    %s %-14s %-30s [%s]\n", icon, f.ID, f.Name, f.Status)
			}
		}
		return nil
	},
}

var sprintCloseCmd = &cobra.Command{
	Use:   "close <id>",
	Short: "Close a sprint and report results",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		s, err := db.GetSprint(database, args[0])
		if err != nil {
			return fmt.Errorf("sprint %q not found. Run 'tillr sprint list' to see available sprints", args[0])
		}
		if s.Status == "closed" {
			return fmt.Errorf("sprint %q is already closed", args[0])
		}

		features, err := db.ListSprintFeatures(database, args[0])
		if err != nil {
			return fmt.Errorf("listing sprint features: %w", err)
		}

		var completed, inProgress, notStarted []models.Feature
		for _, f := range features {
			switch f.Status {
			case "done":
				completed = append(completed, f)
			case "implementing", "agent-qa", "human-qa":
				inProgress = append(inProgress, f)
			default:
				notStarted = append(notStarted, f)
			}
		}

		if err := db.CloseSprint(database, args[0]); err != nil {
			return fmt.Errorf("closing sprint: %w", err)
		}

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}
		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			EventType: "sprint.closed",
			Data: fmt.Sprintf(`{"id":%q,"completed":%d,"in_progress":%d,"not_started":%d}`,
				args[0], len(completed), len(inProgress), len(notStarted)),
		})

		if jsonOutput {
			return printJSON(struct {
				Sprint     *models.Sprint   `json:"sprint"`
				Completed  []models.Feature `json:"completed"`
				InProgress []models.Feature `json:"in_progress"`
				NotStarted []models.Feature `json:"not_started"`
			}{s, completed, inProgress, notStarted})
		}

		fmt.Printf("✓ Closed sprint %q\n\n", s.Name)
		fmt.Printf("  Completed (%d):\n", len(completed))
		for _, f := range completed {
			fmt.Printf("    ✓ %s  %s\n", f.ID, f.Name)
		}
		if len(completed) == 0 {
			fmt.Println("    (none)")
		}

		fmt.Printf("\n  In Progress — carry over (%d):\n", len(inProgress))
		for _, f := range inProgress {
			fmt.Printf("    → %s  %s  [%s]\n", f.ID, f.Name, f.Status)
		}
		if len(inProgress) == 0 {
			fmt.Println("    (none)")
		}

		fmt.Printf("\n  Not Started — carry over (%d):\n", len(notStarted))
		for _, f := range notStarted {
			fmt.Printf("    · %s  %s  [%s]\n", f.ID, f.Name, f.Status)
		}
		if len(notStarted) == 0 {
			fmt.Println("    (none)")
		}
		return nil
	},
}

func statusIcon(status string) string {
	switch status {
	case "done":
		return "✓"
	case "implementing", "agent-qa", "human-qa":
		return "→"
	case "blocked":
		return "✗"
	default:
		return "·"
	}
}
