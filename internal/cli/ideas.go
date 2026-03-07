package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

var ideaCmd = &cobra.Command{
	Use:   "idea",
	Short: "Manage the idea queue",
}

var bugCmd = &cobra.Command{
	Use:   "bug",
	Short: "Bug reporting shortcuts",
}

func init() {
	ideaCmd.AddCommand(ideaSubmitCmd)
	ideaCmd.AddCommand(ideaListCmd)
	ideaCmd.AddCommand(ideaShowCmd)
	ideaCmd.AddCommand(ideaSpecCmd)
	ideaCmd.AddCommand(ideaApproveCmd)
	ideaCmd.AddCommand(ideaRejectCmd)

	ideaSubmitCmd.Flags().String("description", "", "Markdown description (required)")
	ideaSubmitCmd.Flags().String("type", "feature", "Idea type (feature or bug)")
	ideaSubmitCmd.Flags().Bool("auto-implement", false, "Automatically start implementation after approval")
	ideaSubmitCmd.Flags().String("submitted-by", "human", "Who submitted the idea")

	ideaListCmd.Flags().String("status", "", "Filter by status (pending, spec-ready, approved, rejected)")
	ideaListCmd.Flags().String("type", "", "Filter by type (feature, bug)")

	ideaSpecCmd.Flags().String("spec", "", "Markdown spec (required)")

	ideaApproveCmd.Flags().String("notes", "", "Approval notes")

	ideaRejectCmd.Flags().String("notes", "", "Rejection notes")

	bugCmd.AddCommand(bugReportCmd)
	bugCmd.AddCommand(bugListCmd)

	bugReportCmd.Flags().String("description", "", "Markdown description (required)")
}

var ideaSubmitCmd = &cobra.Command{
	Use:   "submit <title>",
	Short: "Submit a new idea",
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
		if desc == "" {
			return fmt.Errorf("--description is required")
		}
		ideaType, _ := cmd.Flags().GetString("type")
		autoImpl, _ := cmd.Flags().GetBool("auto-implement")
		submittedBy, _ := cmd.Flags().GetString("submitted-by")

		idea := &models.IdeaQueueItem{
			ProjectID:     p.ID,
			Title:         args[0],
			RawInput:      desc,
			IdeaType:      ideaType,
			Status:        "pending",
			AutoImplement: autoImpl,
			SubmittedBy:   submittedBy,
		}

		if err := db.InsertIdea(database, idea); err != nil {
			return fmt.Errorf("submitting idea: %w", err)
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			EventType: "idea.submitted",
			Data:      fmt.Sprintf(`{"idea_id":%d,"title":%q,"type":%q}`, idea.ID, args[0], ideaType),
		})

		if jsonOutput {
			return printJSON(idea)
		}
		fmt.Printf("✓ Submitted idea #%d: %s\n", idea.ID, args[0])
		return nil
	},
}

var ideaListCmd = &cobra.Command{
	Use:   "list",
	Short: "List ideas",
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

		status, _ := cmd.Flags().GetString("status")
		ideaType, _ := cmd.Flags().GetString("type")

		ideas, err := db.ListIdeas(database, p.ID, status, ideaType)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(ideas)
		}

		if len(ideas) == 0 {
			fmt.Println("No ideas found.")
			return nil
		}

		fmt.Printf("%-6s %-10s %-12s %-5s %s\n", "ID", "TYPE", "STATUS", "AUTO", "TITLE")
		fmt.Println(strings.Repeat("─", 60))
		for _, i := range ideas {
			auto := ""
			if i.AutoImplement {
				auto = "yes"
			}
			fmt.Printf("%-6d %-10s %-12s %-5s %s\n", i.ID, i.IdeaType, i.Status, auto, i.Title)
		}
		return nil
	},
}

var ideaShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show idea details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid idea ID: %s", args[0])
		}

		idea, err := db.GetIdea(database, id)
		if err != nil {
			return fmt.Errorf("idea not found: %s", args[0])
		}

		if jsonOutput {
			return printJSON(idea)
		}

		fmt.Printf("Idea #%d: %s\n", idea.ID, idea.Title)
		fmt.Printf("  Type:      %s\n", idea.IdeaType)
		fmt.Printf("  Status:    %s\n", idea.Status)
		fmt.Printf("  Submitted: %s by %s\n", idea.CreatedAt, idea.SubmittedBy)
		if idea.AutoImplement {
			fmt.Printf("  Auto:      yes\n")
		}
		if idea.RawInput != "" {
			fmt.Printf("  Input:     %s\n", idea.RawInput)
		}
		if idea.SpecMD != "" {
			fmt.Printf("  Spec:      %s\n", idea.SpecMD)
		}
		if idea.FeatureID != "" {
			fmt.Printf("  Feature:   %s\n", idea.FeatureID)
		}
		return nil
	},
}

var ideaSpecCmd = &cobra.Command{
	Use:   "spec <id>",
	Short: "Set specification for an idea",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		spec, _ := cmd.Flags().GetString("spec")
		if spec == "" {
			return fmt.Errorf("--spec is required")
		}

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid idea ID: %s", args[0])
		}

		if err := db.SetIdeaSpec(database, id, spec); err != nil {
			return fmt.Errorf("setting spec: %w", err)
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			EventType: "idea.spec_generated",
			Data:      fmt.Sprintf(`{"idea_id":%d}`, id),
		})

		if jsonOutput {
			idea, _ := db.GetIdea(database, id)
			return printJSON(idea)
		}
		fmt.Printf("✓ Spec set for idea #%d (status → spec-ready)\n", id)
		return nil
	},
}

var ideaApproveCmd = &cobra.Command{
	Use:   "approve <id>",
	Short: "Approve an idea",
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

		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid idea ID: %s", args[0])
		}

		idea, err := db.GetIdea(database, id)
		if err != nil {
			return fmt.Errorf("idea not found: %s", args[0])
		}

		notes, _ := cmd.Flags().GetString("notes")

		// Create a feature from the idea if it has a spec
		featureID := ""
		if idea.SpecMD != "" {
			f, fErr := engine.AddFeature(database, p.ID, idea.Title, idea.RawInput, idea.SpecMD, "", 0, nil, "")
			if fErr != nil {
				return fmt.Errorf("creating feature from idea: %w", fErr)
			}
			featureID = f.ID

			// If auto-implement, start a feature-implementation cycle
			if idea.AutoImplement {
				_, _ = engine.StartCycle(database, p.ID, featureID, "feature-implementation")
			}
		}

		if err := db.ApproveIdea(database, id, featureID); err != nil {
			return fmt.Errorf("approving idea: %w", err)
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			EventType: "idea.approved",
			Data:      fmt.Sprintf(`{"idea_id":%d,"feature_id":%q,"notes":%q}`, id, featureID, notes),
		})

		if jsonOutput {
			result := map[string]any{
				"status":  "approved",
				"idea_id": id,
			}
			if featureID != "" {
				result["feature_id"] = featureID
			}
			return printJSON(result)
		}
		if featureID != "" {
			fmt.Printf("✓ Approved idea #%d → feature %s\n", id, featureID)
		} else {
			fmt.Printf("✓ Approved idea #%d\n", id)
		}
		return nil
	},
}

var ideaRejectCmd = &cobra.Command{
	Use:   "reject <id>",
	Short: "Reject an idea",
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

		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid idea ID: %s", args[0])
		}

		notes, _ := cmd.Flags().GetString("notes")

		if err := db.UpdateIdeaStatus(database, id, "rejected"); err != nil {
			return fmt.Errorf("rejecting idea: %w", err)
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			EventType: "idea.rejected",
			Data:      fmt.Sprintf(`{"idea_id":%d,"notes":%q}`, id, notes),
		})

		if jsonOutput {
			return printJSON(map[string]string{"status": "rejected", "idea_id": args[0]})
		}
		fmt.Printf("✗ Rejected idea #%d\n", id)
		return nil
	},
}

// Bug shortcuts

var bugReportCmd = &cobra.Command{
	Use:   "report <title>",
	Short: "Report a bug (shortcut for idea submit --type bug)",
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
		if desc == "" {
			return fmt.Errorf("--description is required")
		}

		idea := &models.IdeaQueueItem{
			ProjectID:   p.ID,
			Title:       args[0],
			RawInput:    desc,
			IdeaType:    "bug",
			Status:      "pending",
			SubmittedBy: "human",
		}

		if err := db.InsertIdea(database, idea); err != nil {
			return fmt.Errorf("reporting bug: %w", err)
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			EventType: "idea.submitted",
			Data:      fmt.Sprintf(`{"idea_id":%d,"title":%q,"type":"bug"}`, idea.ID, args[0]),
		})

		if jsonOutput {
			return printJSON(idea)
		}
		fmt.Printf("✓ Reported bug #%d: %s\n", idea.ID, args[0])
		return nil
	},
}

var bugListCmd = &cobra.Command{
	Use:   "list",
	Short: "List bugs (shortcut for idea list --type bug)",
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

		bugs, err := db.ListIdeas(database, p.ID, "", "bug")
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(bugs)
		}

		if len(bugs) == 0 {
			fmt.Println("No bugs found.")
			return nil
		}

		fmt.Printf("%-6s %-12s %s\n", "ID", "STATUS", "TITLE")
		fmt.Println(strings.Repeat("─", 50))
		for _, b := range bugs {
			fmt.Printf("%-6d %-12s %s\n", b.ID, b.Status, b.Title)
		}
		return nil
	},
}
