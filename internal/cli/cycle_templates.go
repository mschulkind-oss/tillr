package cli

import (
	"fmt"
	"strings"

	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/models"
	"github.com/spf13/cobra"
)

var cycleTemplateCmd = &cobra.Command{
	Use:   "template",
	Short: "Manage custom cycle templates",
}

func init() {
	cycleCmd.AddCommand(cycleTemplateCmd)

	cycleTemplateCmd.AddCommand(cycleTemplateListCmd)
	cycleTemplateCmd.AddCommand(cycleTemplateShowCmd)
	cycleTemplateCmd.AddCommand(cycleTemplateAddCmd)
	cycleTemplateCmd.AddCommand(cycleTemplateRemoveCmd)

	cycleTemplateAddCmd.Flags().String("steps", "", "Comma-separated list of step names (required)")
	cycleTemplateAddCmd.Flags().String("description", "", "Template description")
	_ = cycleTemplateAddCmd.MarkFlagRequired("steps")
}

var cycleTemplateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all cycle templates (built-in + custom)",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Merge built-in types with custom templates from DB.
		var all []models.CycleTemplate
		for _, ct := range models.CycleTypes {
			all = append(all, models.CycleTemplate{
				Name:        ct.Name,
				Description: ct.Description,
				Steps:       ct.Steps,
				IsBuiltin:   true,
			})
		}

		custom, err := db.ListCycleTemplates(database)
		if err != nil {
			return fmt.Errorf("listing custom templates: %w", err)
		}
		for _, t := range custom {
			if !t.IsBuiltin {
				all = append(all, t)
			}
		}

		if jsonOutput {
			return printJSON(all)
		}

		for _, t := range all {
			tag := ""
			if t.IsBuiltin {
				tag = " [built-in]"
			}
			fmt.Printf("%-25s %s%s\n", t.Name, t.Description, tag)
			fmt.Printf("  Steps: %s\n\n", joinSteps(t.Steps))
		}
		return nil
	},
}

var cycleTemplateShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show template details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		// Check built-in types first.
		for _, ct := range models.CycleTypes {
			if ct.Name == name {
				t := models.CycleTemplate{
					Name:        ct.Name,
					Description: ct.Description,
					Steps:       ct.Steps,
					IsBuiltin:   true,
				}
				if jsonOutput {
					return printJSON(t)
				}
				printTemplate(t)
				return nil
			}
		}

		// Check custom templates in DB.
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		t, err := db.GetCycleTemplate(database, name)
		if err != nil {
			return fmt.Errorf("template %q not found", name)
		}

		if jsonOutput {
			return printJSON(t)
		}
		printTemplate(*t)
		return nil
	},
}

var cycleTemplateAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a custom cycle template",
	Args:  cobra.ExactArgs(1),
	Example: `  # Create a simple review cycle
  tillr cycle template add code-review --steps "review,revise,approve" --description "Code Review"

  # Create a testing cycle
  tillr cycle template add integration-test --steps "setup,test,teardown,report"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		// Reject if name matches a built-in type.
		for _, ct := range models.CycleTypes {
			if ct.Name == name {
				return fmt.Errorf("cannot add template %q: name conflicts with built-in cycle type", name)
			}
		}

		stepsStr, _ := cmd.Flags().GetString("steps")
		description, _ := cmd.Flags().GetString("description")

		steps := parseSteps(stepsStr)
		if len(steps) < 2 {
			return fmt.Errorf("a cycle template must have at least 2 steps")
		}

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		if err := db.InsertCycleTemplate(database, name, description, steps); err != nil {
			return fmt.Errorf("adding template %q: %w", name, err)
		}

		t := models.CycleTemplate{
			Name:        name,
			Description: description,
			Steps:       steps,
		}

		if jsonOutput {
			return printJSON(t)
		}
		fmt.Printf("✓ Added custom cycle template %q\n", name)
		fmt.Printf("  Steps: %s\n", joinSteps(steps))
		return nil
	},
}

var cycleTemplateRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a custom cycle template (cannot remove built-in)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		// Prevent removing built-in types.
		for _, ct := range models.CycleTypes {
			if ct.Name == name {
				return fmt.Errorf("cannot remove %q: it is a built-in cycle type", name)
			}
		}

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		if err := db.DeleteCycleTemplate(database, name); err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(map[string]string{"removed": name})
		}
		fmt.Printf("✓ Removed custom cycle template %q\n", name)
		return nil
	},
}

func parseSteps(s string) []models.CycleStep {
	var steps []models.CycleStep
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			steps = append(steps, models.CycleStep{Name: part})
		}
	}
	return steps
}

func printTemplate(t models.CycleTemplate) {
	tag := "custom"
	if t.IsBuiltin {
		tag = "built-in"
	}
	fmt.Printf("Name:        %s [%s]\n", t.Name, tag)
	if t.Description != "" {
		fmt.Printf("Description: %s\n", t.Description)
	}
	fmt.Printf("Steps:       %s\n", joinSteps(t.Steps))
	if t.CreatedAt != "" {
		fmt.Printf("Created:     %s\n", t.CreatedAt)
	}
}
