package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/persona"
	"github.com/spf13/cobra"
)

var personaCmd = &cobra.Command{
	Use:     "persona",
	Aliases: []string{"p"},
	Short:   "Manage agent personas (Stage 0 / MVP)",
}

var personaListCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered personas",
	RunE: func(_ *cobra.Command, _ []string) error {
		_, cfg, err := openDB()
		if err != nil {
			return err
		}
		personas, err := persona.List(cfg.ProjectDir)
		if err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(personas)
		}
		if len(personas) == 0 {
			fmt.Println("No personas. Add one at .claude/agents/<name>.md.")
			return nil
		}
		for _, p := range personas {
			updated := p.UpdatedAt
			if updated == "" {
				updated = "(empty)"
			}
			fmt.Printf("%-15s  %6d words  %s\n", p.Name, p.ContextWords, updated)
		}
		return nil
	},
}

var personaShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show persona metadata",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		_, cfg, err := openDB()
		if err != nil {
			return err
		}
		p, err := persona.Get(cfg.ProjectDir, args[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(p)
		}
		fmt.Printf("Name:        %s\n", p.Name)
		fmt.Printf("Definition:  %s\n", p.DefinitionPath)
		fmt.Printf("Context:     %s (%d words, %d bytes)\n",
			p.ContextPath, p.ContextWords, p.ContextBytes)
		if p.UpdatedAt != "" {
			fmt.Printf("Updated:     %s\n", p.UpdatedAt)
		} else {
			fmt.Println("Updated:     (no context yet)")
		}
		threshold := persona.CompactThreshold()
		if p.ContextWords >= threshold {
			fmt.Printf("⚠️  Context exceeds compact threshold (%d words). Run: tillr persona compact %s\n",
				threshold, p.Name)
		}
		return nil
	},
}

var personaContextCmd = &cobra.Command{
	Use:   "context <name>",
	Short: "Print a persona's context file",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		_, cfg, err := openDB()
		if err != nil {
			return err
		}
		body, err := persona.ContextRead(cfg.ProjectDir, args[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(map[string]string{"name": args[0], "body": body})
		}
		if body == "" {
			fmt.Println("(no context yet)")
			return nil
		}
		fmt.Print(body)
		return nil
	},
}

var personaAppendCmd = &cobra.Command{
	Use:   "append <name> [body]",
	Short: "Append a timestamped block to a persona's context file",
	Long: `Append a timestamped block to swarf/agents/<name>/context.md.

If body is omitted on the command line, reads body from stdin.
The --summary flag adds a one-line summary to the timestamp header.

Examples:
  tillr persona append implementer "Implemented OAuth using go-oidc"
  echo "Long context body..." | tillr persona append researcher --summary "OAuth library research"`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, err := openDB()
		if err != nil {
			return err
		}
		summary, _ := cmd.Flags().GetString("summary")
		var body string
		if len(args) == 2 {
			body = args[1]
		} else {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			body = string(data)
		}
		if strings.TrimSpace(body) == "" && strings.TrimSpace(summary) == "" {
			return fmt.Errorf("body or --summary is required")
		}
		if err := persona.Append(cfg.ProjectDir, args[0], summary, body); err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(map[string]string{"persona": args[0], "status": "appended"})
		}
		fmt.Printf("Appended to %s persona context.\n", args[0])
		return nil
	},
}

var personaCompactCmd = &cobra.Command{
	Use:   "compact <name>",
	Short: "Archive older context blocks to a backup, keep recent N verbatim",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, err := openDB()
		if err != nil {
			return err
		}
		keep, _ := cmd.Flags().GetInt("keep")
		result, err := persona.Compact(cfg.ProjectDir, args[0], keep)
		if err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(result)
		}
		if result.BlocksMoved == 0 {
			fmt.Printf("Nothing to compact (%d block(s); kept all).\n", result.BlocksKept)
			return nil
		}
		fmt.Printf("Compacted %s persona context.\n", args[0])
		fmt.Printf("  Words:   %d → %d\n", result.WordsBefore, result.WordsAfter)
		fmt.Printf("  Blocks:  %d archived, %d kept\n", result.BlocksMoved, result.BlocksKept)
		fmt.Printf("  Backup:  %s\n", result.BackupPath)
		return nil
	},
}

var personaClaimCmd = &cobra.Command{
	Use:   "claim <name>",
	Short: "Claim the next pending feature targeted at this persona",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		project, err := db.GetProject(database)
		if err != nil {
			return fmt.Errorf("loading project: %w", err)
		}

		feature, err := db.ClaimNextFeature(database, project.ID, args[0])
		if err != nil {
			return err
		}
		if feature == nil {
			if jsonOutput {
				return printJSON(map[string]any{"persona": args[0], "claimed": nil})
			}
			fmt.Printf("No pending features for %s.\n", args[0])
			return nil
		}
		if jsonOutput {
			return printJSON(feature)
		}
		fmt.Printf("Claimed #%d  %s  → %s\n", feature.ID, feature.Title, feature.TargetPersona)
		fmt.Printf("Status: %s\n", feature.Status)
		if feature.Description != "" {
			fmt.Printf("\n%s\n", feature.Description)
		}
		return nil
	},
}

func init() {
	personaAppendCmd.Flags().StringP("summary", "s", "",
		"One-line summary added to the timestamp header")

	personaCompactCmd.Flags().IntP("keep", "k", 20,
		"Number of recent blocks to keep verbatim")

	personaCmd.AddCommand(personaListCmd)
	personaCmd.AddCommand(personaShowCmd)
	personaCmd.AddCommand(personaContextCmd)
	personaCmd.AddCommand(personaAppendCmd)
	personaCmd.AddCommand(personaCompactCmd)
	personaCmd.AddCommand(personaClaimCmd)
}
