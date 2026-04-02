package cli

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/spf13/cobra"
)

var milestoneCmd = &cobra.Command{
	Use:   "milestone",
	Short: "Manage milestones",
}

func init() {
	milestoneCmd.AddCommand(milestoneAddCmd)
	milestoneCmd.AddCommand(milestoneListCmd)
	milestoneCmd.AddCommand(milestoneShowCmd)

	milestoneAddCmd.Flags().String("description", "", "Milestone description")
	milestoneAddCmd.Flags().Int("order", 0, "Sort order")
}

var milestoneAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a new milestone",
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

		desc, _ := cmd.Flags().GetString("description")
		order, _ := cmd.Flags().GetInt("order")

		id := strings.ToLower(strings.ReplaceAll(args[0], " ", "-"))
		m := &models.Milestone{
			ID:          id,
			ProjectID:   p.ID,
			Name:        args[0],
			Description: desc,
			SortOrder:   order,
		}

		if err := db.CreateMilestone(database, m); err != nil {
			return fmt.Errorf("creating milestone %q: %w", args[0], err)
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			EventType: "milestone.created",
			Data:      fmt.Sprintf(`{"name":%q}`, args[0]),
		})

		if jsonOutput {
			return printJSON(m)
		}
		fmt.Printf("✓ Added milestone %q (id: %s)\n", m.Name, m.ID)
		return nil
	},
}

var milestoneListCmd = &cobra.Command{
	Use:   "list",
	Short: "List milestones",
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

		milestones, err := db.ListMilestones(database, p.ID)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(milestones)
		}

		if len(milestones) == 0 {
			fmt.Println("No milestones found.")
			return nil
		}

		for _, m := range milestones {
			pct := 0
			if m.TotalFeatures > 0 {
				pct = (m.DoneFeatures * 100) / m.TotalFeatures
			}
			bar := progressBar(pct, 20)
			fmt.Printf("%-20s %s %3d%% (%d/%d)  [%s]\n",
				m.ID, bar, pct, m.DoneFeatures, m.TotalFeatures, m.Status)
		}
		return nil
	},
}

var milestoneShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show milestone details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		m, err := db.GetMilestone(database, args[0])
		if err != nil {
			return fmt.Errorf("milestone %q not found. Run 'tillr milestone list' to see available milestones", args[0])
		}

		if jsonOutput {
			return printJSON(m)
		}

		fmt.Printf("Milestone: %s\n", m.Name)
		fmt.Printf("  ID:     %s\n", m.ID)
		fmt.Printf("  Status: %s\n", m.Status)
		if m.Description != "" {
			fmt.Printf("  Desc:   %s\n", m.Description)
		}
		fmt.Printf("  Created: %s\n", m.CreatedAt)
		return nil
	},
}

func progressBar(pct, width int) string {
	filled := (pct * width) / 100
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}
