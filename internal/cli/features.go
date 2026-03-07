package cli

import (
	"fmt"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/spf13/cobra"
)

var featureCmd = &cobra.Command{
	Use:   "feature",
	Short: "Manage features",
}

func init() {
	featureCmd.AddCommand(featureAddCmd)
	featureCmd.AddCommand(featureListCmd)
	featureCmd.AddCommand(featureShowCmd)
	featureCmd.AddCommand(featureEditCmd)
	featureCmd.AddCommand(featureRemoveCmd)
	featureCmd.AddCommand(featureDepsCmd)
	featureCmd.AddCommand(featureBatchCmd)

	featureAddCmd.Flags().String("milestone", "", "Assign to milestone")
	featureAddCmd.Flags().Int("priority", 0, "Priority (higher = more important)")
	featureAddCmd.Flags().StringSlice("depends-on", nil, "Feature dependencies")
	featureAddCmd.Flags().String("description", "", "Feature description")
	featureAddCmd.Flags().String("spec", "", "Feature spec / acceptance criteria (detailed requirements)")
	featureAddCmd.Flags().String("roadmap-item", "", "Link to originating roadmap item ID")
	featureAddCmd.Flags().String("status", "draft", "Initial status (draft, planning, implementing, agent-qa, human-qa, done, blocked)")

	featureListCmd.Flags().String("status", "", "Filter by status")
	featureListCmd.Flags().String("milestone", "", "Filter by milestone")

	featureEditCmd.Flags().String("name", "", "New name")
	featureEditCmd.Flags().String("description", "", "New description")
	featureEditCmd.Flags().String("spec", "", "New spec / acceptance criteria")
	featureEditCmd.Flags().String("status", "", "New status")
	featureEditCmd.Flags().String("milestone", "", "New milestone")
	featureEditCmd.Flags().String("roadmap-item", "", "Link to roadmap item ID")
	featureEditCmd.Flags().Int("priority", -1, "New priority")

	featureBatchCmd.Flags().StringSlice("ids", nil, "Feature IDs to update (comma-separated)")
	featureBatchCmd.Flags().String("status", "", "Set status for all features")
	featureBatchCmd.Flags().String("milestone", "", "Set milestone for all features")
	featureBatchCmd.Flags().Int("priority", -1, "Set priority for all features")
}

var featureAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a new feature",
	Args:  cobra.ExactArgs(1),
	Example: `  # Add a new feature
  lifecycle feature add "User Auth" --description "JWT-based authentication" --priority 8

  # Add with full spec for agents
  lifecycle feature add "Search" --spec "1. Full-text search via FTS5\n2. Results ranked by relevance" --milestone v1.0

  # Onboarding: add already-completed feature
  lifecycle feature add "Database Layer" --status done --spec "..." --priority 10`,
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

		milestone, _ := cmd.Flags().GetString("milestone")
		priority, _ := cmd.Flags().GetInt("priority")
		deps, _ := cmd.Flags().GetStringSlice("depends-on")
		desc, _ := cmd.Flags().GetString("description")
		spec, _ := cmd.Flags().GetString("spec")
		roadmapItem, _ := cmd.Flags().GetString("roadmap-item")
		status, _ := cmd.Flags().GetString("status")

		f, err := engine.AddFeature(database, p.ID, args[0], desc, spec, milestone, priority, deps, roadmapItem)
		if err != nil {
			return err
		}

		// If status is not the default "draft", set it directly
		if status != "" && status != "draft" {
			validStatuses := map[string]bool{
				"planning": true, "implementing": true, "agent-qa": true,
				"human-qa": true, "done": true, "blocked": true,
			}
			if !validStatuses[status] {
				return fmt.Errorf("invalid status %q: must be one of draft, planning, implementing, agent-qa, human-qa, done, blocked", status)
			}
			if err := db.SetFeatureStatus(database, f.ID, status); err != nil {
				return fmt.Errorf("setting feature status: %w", err)
			}
			f.Status = status
		}

		if jsonOutput {
			return printJSON(f)
		}
		fmt.Printf("✓ Added feature %q (id: %s)\n", f.Name, f.ID)
		return nil
	},
}

var featureListCmd = &cobra.Command{
	Use:   "list",
	Short: "List features",
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
		milestone, _ := cmd.Flags().GetString("milestone")

		features, err := db.ListFeatures(database, p.ID, status, milestone)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(features)
		}

		if len(features) == 0 {
			fmt.Println("No features found.")
			return nil
		}

		fmt.Printf("%-20s %-14s %-4s %s\n", "ID", "STATUS", "PRI", "NAME")
		fmt.Println(strings.Repeat("─", 60))
		for _, f := range features {
			fmt.Printf("%-20s %-14s %-4d %s\n", f.ID, f.Status, f.Priority, f.Name)
		}
		return nil
	},
}

var featureShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show feature details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		f, err := db.GetFeature(database, args[0])
		if err != nil {
			return fmt.Errorf("feature not found: %s", args[0])
		}

		if jsonOutput {
			return printJSON(f)
		}

		fmt.Printf("Feature: %s\n", f.Name)
		fmt.Printf("  ID:        %s\n", f.ID)
		fmt.Printf("  Status:    %s\n", f.Status)
		fmt.Printf("  Priority:  %d\n", f.Priority)
		if f.MilestoneID != "" {
			fmt.Printf("  Milestone: %s (%s)\n", f.MilestoneName, f.MilestoneID)
		}
		if f.AssignedCycle != "" {
			fmt.Printf("  Cycle:     %s\n", f.AssignedCycle)
		}
		if len(f.DependsOn) > 0 {
			fmt.Printf("  Depends:   %s\n", strings.Join(f.DependsOn, ", "))
		}
		if f.Description != "" {
			fmt.Printf("  Desc:      %s\n", f.Description)
		}
		if f.Spec != "" {
			fmt.Printf("  Spec:      %s\n", f.Spec)
		}
		if f.RoadmapItemID != "" {
			fmt.Printf("  Roadmap:   %s\n", f.RoadmapItemID)
		}
		fmt.Printf("  Created:   %s\n", f.CreatedAt)
		return nil
	},
}

var featureEditCmd = &cobra.Command{
	Use:   "edit <id>",
	Short: "Edit feature properties",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		updates := make(map[string]any)
		if v, _ := cmd.Flags().GetString("name"); v != "" {
			updates["name"] = v
		}
		if v, _ := cmd.Flags().GetString("description"); v != "" {
			updates["description"] = v
		}
		if v, _ := cmd.Flags().GetString("spec"); v != "" {
			updates["spec"] = v
		}
		newStatus, _ := cmd.Flags().GetString("status")
		if v, _ := cmd.Flags().GetString("milestone"); v != "" {
			updates["milestone_id"] = v
		}
		if v, _ := cmd.Flags().GetString("roadmap-item"); v != "" {
			updates["roadmap_item_id"] = v
		}
		if v, _ := cmd.Flags().GetInt("priority"); v >= 0 && cmd.Flags().Changed("priority") {
			updates["priority"] = v
		}

		if len(updates) == 0 && newStatus == "" {
			return fmt.Errorf("no changes specified")
		}

		if len(updates) > 0 {
			if err := db.UpdateFeature(database, args[0], updates); err != nil {
				return err
			}
		}

		// Handle status transition through the engine to enforce QA gate
		if newStatus != "" {
			p, err := db.GetProject(database)
			if err != nil {
				return fmt.Errorf("getting project: %w", err)
			}
			if err := engine.TransitionFeature(database, p.ID, args[0], newStatus); err != nil {
				return err
			}
		}

		if jsonOutput {
			f, _ := db.GetFeature(database, args[0])
			return printJSON(f)
		}
		fmt.Printf("✓ Updated feature %s\n", args[0])
		return nil
	},
}

var featureRemoveCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove a feature",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		if err := db.DeleteFeature(database, args[0]); err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(map[string]string{"deleted": args[0]})
		}
		fmt.Printf("✓ Removed feature %s\n", args[0])
		return nil
	},
}

var featureDepsCmd = &cobra.Command{
	Use:   "deps <id>",
	Short: "Show dependency tree for a feature",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		featureID := args[0]
		f, err := db.GetFeature(database, featureID)
		if err != nil {
			return fmt.Errorf("feature not found: %s", featureID)
		}

		dependents, _ := db.GetFeatureDependents(database, featureID)

		if jsonOutput {
			tree, _ := db.GetFeatureDependencyTree(database, featureID)
			blocked, _ := db.GetBlockedFeatures(database)
			blockedSet := map[string]bool{}
			for _, b := range blocked {
				blockedSet[b.ID] = true
			}
			return printJSON(map[string]any{
				"feature":    f,
				"tree":       tree,
				"dependents": dependents,
				"is_blocked": blockedSet[f.ID],
			})
		}

		// Print tree header
		statusMark := statusSymbol(f.Status)
		fmt.Printf("%s (%s) %s\n", f.Name, f.Status, statusMark)

		// Print dependencies
		for i, depID := range f.DependsOn {
			dep, depErr := db.GetFeature(database, depID)
			isLast := i == len(f.DependsOn)-1
			prefix := "├── "
			if isLast && len(dependents) == 0 {
				prefix = "└── "
			}
			if depErr != nil {
				fmt.Printf("%s%s (unknown)\n", prefix, depID)
				continue
			}
			mark := statusSymbol(dep.Status)
			blocking := ""
			if dep.Status != "done" {
				blocking = " BLOCKING"
			}
			fmt.Printf("%s%s (%s) %s%s\n", prefix, dep.Name, dep.Status, mark, blocking)
			// Print transitive deps (one level)
			for j, subDepID := range dep.DependsOn {
				subDep, subErr := db.GetFeature(database, subDepID)
				subPrefix := "│   "
				if isLast && len(dependents) == 0 {
					subPrefix = "    "
				}
				connector := "├── "
				if j == len(dep.DependsOn)-1 {
					connector = "└── "
				}
				if subErr != nil {
					fmt.Printf("%s%s%s (unknown)\n", subPrefix, connector, subDepID)
					continue
				}
				subMark := statusSymbol(subDep.Status)
				fmt.Printf("%s%s%s (%s) %s\n", subPrefix, connector, subDep.Name, subDep.Status, subMark)
			}
		}

		// Print dependents (required by)
		if len(dependents) > 0 {
			fmt.Println("Required by:")
			for i, dep := range dependents {
				prefix := "├── "
				if i == len(dependents)-1 {
					prefix = "└── "
				}
				fmt.Printf("%s%s (%s)\n", prefix, dep.Name, dep.Status)
			}
		}

		return nil
	},
}

var featureBatchCmd = &cobra.Command{
	Use:   "batch",
	Short: "Batch update multiple features",
	Example: `  # Set status for multiple features
  lifecycle feature batch --ids f1,f2,f3 --status implementing

  # Set milestone for multiple features
  lifecycle feature batch --ids f1,f2 --milestone v1.0-mvp

  # Set priority for multiple features
  lifecycle feature batch --ids f1,f2,f3 --priority 8`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		ids, _ := cmd.Flags().GetStringSlice("ids")
		if len(ids) == 0 {
			return fmt.Errorf("--ids is required")
		}

		status, _ := cmd.Flags().GetString("status")
		milestone, _ := cmd.Flags().GetString("milestone")
		priority, _ := cmd.Flags().GetInt("priority")
		priorityChanged := cmd.Flags().Changed("priority")

		var field, value string
		switch {
		case status != "":
			validStatuses := map[string]bool{
				"draft": true, "planning": true, "implementing": true,
				"agent-qa": true, "human-qa": true, "done": true, "blocked": true,
			}
			if !validStatuses[status] {
				return fmt.Errorf("invalid status %q", status)
			}
			field, value = "status", status
		case milestone != "":
			field, value = "milestone_id", milestone
		case priorityChanged && priority >= 0:
			field, value = "priority", fmt.Sprintf("%d", priority)
		default:
			return fmt.Errorf("specify one of --status, --milestone, or --priority")
		}

		updated, err := db.BatchUpdateFeatures(database, ids, field, value)
		if err != nil {
			return fmt.Errorf("batch update: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{"updated": updated, "field": field, "value": value, "ids": ids})
		}
		fmt.Printf("✓ Updated %d feature(s): %s = %s\n", updated, field, value)
		return nil
	},
}

func statusSymbol(status string) string {
	switch status {
	case "done":
		return "✓"
	case "blocked":
		return "✗"
	default:
		return "○"
	}
}
