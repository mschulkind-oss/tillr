package cli

import (
	"fmt"

	"github.com/mschulkind/lifecycle/internal/config"
	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage multiple projects within a single database",
	Long: `Manage multiple projects stored in the same lifecycle database.

Use 'project list' to see all projects, 'project switch <id>' to change
the active project, and 'project create <name>' to add a new one.`,
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		projects, err := db.ListProjects(database)
		if err != nil {
			return fmt.Errorf("listing projects: %w", err)
		}

		if jsonOutput {
			return printJSON(projects)
		}

		if len(projects) == 0 {
			fmt.Println("No projects found.")
			return nil
		}

		for _, p := range projects {
			marker := "  "
			if p.ID == cfg.ActiveProject {
				marker = "* "
			}
			desc := ""
			if p.Description != "" {
				desc = " — " + p.Description
			}
			fmt.Printf("%s%-20s %s%s\n", marker, p.ID, p.Name, desc)
		}
		return nil
	},
}

var projectSwitchCmd = &cobra.Command{
	Use:   "switch <project-id>",
	Short: "Switch the active project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		targetID := args[0]
		p, err := db.GetProjectByID(database, targetID)
		if err != nil {
			return fmt.Errorf("project %q not found", targetID)
		}

		cfg.ActiveProject = p.ID
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{
				"active_project": p,
			})
		}
		fmt.Printf("✓ Switched to project %q (%s)\n", p.Name, p.ID)
		return nil
	},
}

var projectCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new project in the current database",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		name := args[0]
		description, _ := cmd.Flags().GetString("description")

		p, err := engine.InitProject(database, name)
		if err != nil {
			return fmt.Errorf("creating project: %w", err)
		}
		if description != "" {
			if err := db.UpdateProject(database, p.ID, description); err != nil {
				return fmt.Errorf("updating description: %w", err)
			}
			p.Description = description
		}

		switchTo, _ := cmd.Flags().GetBool("switch")
		if switchTo {
			cfg.ActiveProject = p.ID
			if err := config.Save(cfg); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
		}

		if jsonOutput {
			return printJSON(p)
		}
		fmt.Printf("✓ Created project %q (%s)\n", p.Name, p.ID)
		if switchTo {
			fmt.Printf("  Switched active project to %s\n", p.ID)
		}
		return nil
	},
}

var projectCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the current active project",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		var p *models.Project
		if cfg.ActiveProject != "" {
			p, err = db.GetProjectByID(database, cfg.ActiveProject)
			if err != nil {
				return fmt.Errorf("active project %q not found in database", cfg.ActiveProject)
			}
		} else {
			p, err = db.GetProject(database)
			if err != nil {
				return fmt.Errorf("no projects found")
			}
		}

		if jsonOutput {
			return printJSON(p)
		}
		fmt.Printf("%s — %s\n", p.ID, p.Name)
		return nil
	},
}

func init() {
	projectCmd.AddCommand(projectListCmd)
	projectCmd.AddCommand(projectSwitchCmd)
	projectCmd.AddCommand(projectCreateCmd)
	projectCmd.AddCommand(projectCurrentCmd)

	projectCreateCmd.Flags().String("description", "", "Project description")
	projectCreateCmd.Flags().Bool("switch", false, "Switch to the new project after creation")
}
