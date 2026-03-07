package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/export"
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
	decisionCmd.AddCommand(decisionSupersedeCmd)
	decisionCmd.AddCommand(decisionExportCmd)

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

	decisionExportCmd.Flags().String("format", "adr", "Output format (adr, json, md, csv)")
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
		if d.SupersededBy != "" {
			fmt.Printf("  Superseded by: %s\n", d.SupersededBy)
		}
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

var decisionSupersedeCmd = &cobra.Command{
	Use:   "supersede <old-id> <new-id>",
	Short: "Mark a decision as superseded by another",
	Long:  "Sets the old decision's status to 'superseded' and links it to the new decision.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		oldID, newID := args[0], args[1]

		// Verify both decisions exist
		oldD, err := db.GetDecision(database, oldID)
		if err != nil {
			return fmt.Errorf("decision %q not found", oldID)
		}
		newD, err := db.GetDecision(database, newID)
		if err != nil {
			return fmt.Errorf("decision %q not found", newID)
		}

		if err := db.SupersedeDecision(database, oldID, newID); err != nil {
			return fmt.Errorf("superseding decision: %w", err)
		}

		p, _ := db.GetProject(database)
		if p != nil {
			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				EventType: "decision.superseded",
				Data:      fmt.Sprintf(`{"old_id":%q,"old_title":%q,"new_id":%q,"new_title":%q}`, oldID, oldD.Title, newID, newD.Title),
			})
		}

		if jsonOutput {
			updated, _ := db.GetDecision(database, oldID)
			return printJSON(updated)
		}
		fmt.Printf("✓ Decision %q superseded by %q\n", oldD.Title, newD.Title)
		return nil
	},
}

var decisionExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export decisions as ADR documents",
	Long:  "Export architecture decisions in standard ADR format, JSON, Markdown, or CSV.",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		decisions, err := db.ListDecisions(database, "")
		if err != nil {
			return fmt.Errorf("listing decisions: %w", err)
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "adr" {
			return export.DecisionsADR(decisions, os.Stdout)
		}
		return export.Decisions(decisions, os.Stdout, format)
	},
}
