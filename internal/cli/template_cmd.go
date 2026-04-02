package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Manage project templates",
}

func init() {
	templateCmd.AddCommand(templateListCmd)
	templateCmd.AddCommand(templateShowCmd)
}

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available project templates",
	RunE: func(cmd *cobra.Command, args []string) error {
		type templateInfo struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Milestones  int    `json:"milestones"`
			Features    int    `json:"features"`
		}

		var infos []templateInfo
		for _, name := range templateNames() {
			t := projectTemplates[name]
			infos = append(infos, templateInfo{
				Name:        name,
				Description: t.Description,
				Milestones:  len(t.Milestones),
				Features:    len(t.Features),
			})
		}

		if jsonOutput {
			return printJSON(infos)
		}

		fmt.Printf("%-15s %-5s %-5s %s\n", "NAME", "MILES", "FEATS", "DESCRIPTION")
		fmt.Println(strings.Repeat("-", 70))
		for _, t := range infos {
			fmt.Printf("%-15s %-5d %-5d %s\n", t.Name, t.Milestones, t.Features, t.Description)
		}
		fmt.Println()
		fmt.Println("Usage: tillr init <name> --template <template>")
		return nil
	},
}

var templateShowCmd = &cobra.Command{
	Use:   "show <template-name>",
	Short: "Show details of a project template",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		tmpl, ok := projectTemplates[name]
		if !ok {
			return fmt.Errorf("unknown template %q. Available: %s", name, strings.Join(templateNames(), ", "))
		}

		if jsonOutput {
			data := map[string]any{
				"name":         tmpl.Name,
				"description":  tmpl.Description,
				"milestones":   tmpl.Milestones,
				"features":     tmpl.Features,
				"roadmap_items": tmpl.RoadmapItems,
				"discussions":  tmpl.Discussions,
			}
			b, _ := json.MarshalIndent(data, "", "  ")
			fmt.Println(string(b))
			return nil
		}

		fmt.Printf("Template: %s\n", tmpl.Name)
		fmt.Printf("Description: %s\n\n", tmpl.Description)

		fmt.Println("Milestones:")
		for _, m := range tmpl.Milestones {
			fmt.Printf("  - %s: %s\n", m.Name, m.Description)
		}

		fmt.Println("\nFeatures:")
		for _, f := range tmpl.Features {
			deps := ""
			if len(f.DependsOn) > 0 {
				deps = fmt.Sprintf(" (depends: %s)", strings.Join(f.DependsOn, ", "))
			}
			milestone := ""
			if f.Milestone != "" {
				milestone = fmt.Sprintf(" [%s]", f.Milestone)
			}
			fmt.Printf("  - %s (priority %d)%s%s\n", f.Name, f.Priority, milestone, deps)
			if f.Description != "" {
				fmt.Printf("    %s\n", f.Description)
			}
		}

		if len(tmpl.RoadmapItems) > 0 {
			fmt.Println("\nRoadmap Items:")
			for _, r := range tmpl.RoadmapItems {
				fmt.Printf("  - %s [%s, %s]\n", r.Title, r.Priority, r.Effort)
			}
		}

		if len(tmpl.Discussions) > 0 {
			fmt.Println("\nDiscussions:")
			for _, d := range tmpl.Discussions {
				fmt.Printf("  - %s\n", d.Title)
			}
		}

		return nil
	},
}
