package cli

import (
	"fmt"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

var decisionCmd = &cobra.Command{
	Use:     "decision",
	Aliases: []string{"adr"},
	Short:   "Architecture Decision Records (ADRs)",
}

func init() {
	decisionCmd.AddCommand(decisionAddCmd)
	decisionCmd.AddCommand(decisionListCmd)
	decisionCmd.AddCommand(decisionShowCmd)
	decisionCmd.AddCommand(decisionEditCmd)

	decisionAddCmd.Flags().String("context", "", "Why is this decision needed?")
	decisionAddCmd.Flags().String("decision", "", "What was decided?")
	decisionAddCmd.Flags().String("consequences", "", "What are the consequences?")
	decisionAddCmd.Flags().String("feature", "", "Link to feature ID")
	decisionAddCmd.Flags().String("status", "proposed", "Status (proposed, accepted, rejected, superseded, deprecated)")

	decisionListCmd.Flags().String("status", "", "Filter by status")

	decisionEditCmd.Flags().String("title", "", "New title")
	decisionEditCmd.Flags().String("status", "", "New status")
	decisionEditCmd.Flags().String("context", "", "New context")
	decisionEditCmd.Flags().String("decision", "", "New decision text")
	decisionEditCmd.Flags().String("consequences", "", "New consequences")
}

var decisionAddCmd = &cobra.Command{
	Use:   "add <title>",
	Short: "Record a new architecture decision",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		context, _ := cmd.Flags().GetString("context")
		decision, _ := cmd.Flags().GetString("decision")
		consequences, _ := cmd.Flags().GetString("consequences")
		featureID, _ := cmd.Flags().GetString("feature")
		status, _ := cmd.Flags().GetString("status")

		validStatuses := map[string]bool{
			"proposed": true, "accepted": true, "rejected": true,
			"superseded": true, "deprecated": true,
		}
		if !validStatuses[status] {
			return fmt.Errorf("invalid status %q: must be one of proposed, accepted, rejected, superseded, deprecated", status)
		}

		id := engine.Slug(args[0])
		d := &models.Decision{
			ID:           id,
			Title:        args[0],
			Status:       status,
			Context:      context,
			Decision:     decision,
			Consequences: consequences,
			FeatureID:    featureID,
		}
		if err := db.CreateDecision(database, d); err != nil {
			return fmt.Errorf("creating decision: %w", err)
		}

		p, _ := db.GetProject(database)
		if p != nil {
			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				FeatureID: featureID,
				EventType: "decision.created",
				Data:      fmt.Sprintf(`{"id":%q,"title":%q,"status":%q}`, id, args[0], status),
			})
		}

		if jsonOutput {
			created, _ := db.GetDecision(database, id)
			if created != nil {
				return printJSON(created)
			}
			return printJSON(d)
		}
		fmt.Printf("✓ Created decision: %s (%s)\n", args[0], id)
		return nil
	},
}

var decisionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List architecture decisions",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		status, _ := cmd.Flags().GetString("status")
		decisions, err := db.ListDecisions(database, status)
		if err != nil {
			return fmt.Errorf("listing decisions: %w", err)
		}
		if decisions == nil {
			decisions = []models.Decision{}
		}

		if jsonOutput {
			return printJSON(decisions)
		}

		if len(decisions) == 0 {
			fmt.Println("No decisions recorded yet.")
			return nil
		}

		statusIcons := map[string]string{
			"proposed":   "📝",
			"accepted":   "✅",
			"rejected":   "❌",
			"superseded": "🔄",
			"deprecated": "⚠️",
		}

		for _, d := range decisions {
			icon := statusIcons[d.Status]
			if icon == "" {
				icon = "📝"
			}
			fmt.Printf("  %s %-12s  %-40s  %s\n", icon, d.Status, d.Title, d.ID)
		}
		return nil
	},
}

var decisionShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show decision details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		d, err := db.GetDecision(database, args[0])
		if err != nil {
			return fmt.Errorf("decision %q not found", args[0])
		}

		if jsonOutput {
			return printJSON(d)
		}

		fmt.Printf("Decision: %s\n", d.Title)
		fmt.Printf("  ID:           %s\n", d.ID)
		fmt.Printf("  Status:       %s\n", d.Status)
		if d.FeatureID != "" {
			fmt.Printf("  Feature:      %s\n", d.FeatureID)
		}
		fmt.Printf("  Created:      %s\n", d.CreatedAt)
		fmt.Printf("  Updated:      %s\n", d.UpdatedAt)
		if d.Context != "" {
			fmt.Printf("\n  Context:\n    %s\n", strings.ReplaceAll(d.Context, "\n", "\n    "))
		}
		if d.Decision != "" {
			fmt.Printf("\n  Decision:\n    %s\n", strings.ReplaceAll(d.Decision, "\n", "\n    "))
		}
		if d.Consequences != "" {
			fmt.Printf("\n  Consequences:\n    %s\n", strings.ReplaceAll(d.Consequences, "\n", "\n    "))
		}
		return nil
	},
}

var decisionEditCmd = &cobra.Command{
	Use:   "edit <id>",
	Short: "Edit a decision",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Verify decision exists
		if _, err := db.GetDecision(database, args[0]); err != nil {
			return fmt.Errorf("decision %q not found", args[0])
		}

		updates := map[string]any{}

		if cmd.Flags().Changed("title") {
			v, _ := cmd.Flags().GetString("title")
			updates["title"] = v
		}
		if cmd.Flags().Changed("status") {
			v, _ := cmd.Flags().GetString("status")
			validStatuses := map[string]bool{
				"proposed": true, "accepted": true, "rejected": true,
				"superseded": true, "deprecated": true,
			}
			if !validStatuses[v] {
				return fmt.Errorf("invalid status %q: must be one of proposed, accepted, rejected, superseded, deprecated", v)
			}
			updates["status"] = v
		}
		if cmd.Flags().Changed("context") {
			v, _ := cmd.Flags().GetString("context")
			updates["context"] = v
		}
		if cmd.Flags().Changed("decision") {
			v, _ := cmd.Flags().GetString("decision")
			updates["decision"] = v
		}
		if cmd.Flags().Changed("consequences") {
			v, _ := cmd.Flags().GetString("consequences")
			updates["consequences"] = v
		}

		if len(updates) == 0 {
			return fmt.Errorf("no changes specified")
		}

		if err := db.UpdateDecision(database, args[0], updates); err != nil {
			return fmt.Errorf("updating decision: %w", err)
		}

		if jsonOutput {
			d, _ := db.GetDecision(database, args[0])
			return printJSON(d)
		}
		fmt.Printf("✓ Updated decision %s\n", args[0])
		return nil
	},
}
