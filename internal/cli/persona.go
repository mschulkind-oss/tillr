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
	Long: `Personas are the agent roles that do scoped work for tillr. Each
is defined by a .claude/agents/<name>.md sub-agent file, and each
owns an append-style markdown context file at swarf/agents/<name>/
context.md. The orchestrator daemon dispatches features tagged for a
persona; per Principle Zero, the orchestrator (not the agent itself)
appends to the context file on completion.

Conceptually:
  .claude/agents/<name>.md    — what the persona is FOR (prompt + tools)
  swarf/agents/<name>/...     — the persona's accumulated memory`,
}

var personaListCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered personas with context size and last update",
	Long: `Discovers personas from .claude/agents/*.md and reports each one's
context-file size (words) and last-update timestamp. Add a new
persona by creating .claude/agents/<name>.md.`,
	Example: `  tillr persona list
  tillr --json persona list`,
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
			fmt.Println(Dim("No personas. Add one at .claude/agents/<name>.md."))
			return nil
		}
		fmt.Printf("%s  %s  %s\n",
			Header(fmt.Sprintf("%-15s", "PERSONA")),
			Header(fmt.Sprintf("%6s", "WORDS")),
			Header("UPDATED"))
		for _, p := range personas {
			updated := p.UpdatedAt
			if updated == "" {
				updated = Dim("(empty)")
			}
			words := fmt.Sprintf("%6d", p.ContextWords)
			if p.ContextWords >= persona.CompactThreshold() {
				words = Warning(words)
			}
			fmt.Printf("%-15s  %s  %s\n", Persona(p.Name), words, updated)
		}
		return nil
	},
}

var personaShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show persona metadata + paths + size",
	Example: `  tillr persona show implementer
  tillr --json persona show researcher`,
	Args: cobra.ExactArgs(1),
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
		fmt.Printf("%-13s%s\n", Header("Name:"), Persona(p.Name))
		fmt.Printf("%-13s%s\n", Header("Definition:"), Code(p.DefinitionPath))
		fmt.Printf("%-13s%s %s\n", Header("Context:"), Code(p.ContextPath),
			Dim(fmt.Sprintf("(%d words, %d bytes)", p.ContextWords, p.ContextBytes)))
		if p.UpdatedAt != "" {
			fmt.Printf("%-13s%s\n", Header("Updated:"), p.UpdatedAt)
		} else {
			fmt.Printf("%-13s%s\n", Header("Updated:"), Dim("(no context yet)"))
		}
		threshold := persona.CompactThreshold()
		if p.ContextWords >= threshold {
			fmt.Printf("%s Context exceeds compact threshold (%d words). Run: %s\n",
				Warning("⚠"), threshold,
				Code(fmt.Sprintf("tillr persona compact %s", p.Name)))
		}
		return nil
	},
}

var personaContextCmd = &cobra.Command{
	Use:   "context <name>",
	Short: "Print a persona's context file (the agent's accumulated memory)",
	Long: `Prints the raw markdown context file for a persona. This is the
agent's accumulated memory — what's loaded as the system prompt every
time the orchestrator dispatches the persona.`,
	Example: `  tillr persona context implementer
  tillr persona context researcher | less
  tillr --json persona context reviewer`,
	Args: cobra.ExactArgs(1),
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
			fmt.Println(Dim("(no context yet)"))
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

Note: under normal operation the orchestrator daemon performs this
append automatically per Principle Zero. Manual invocation is for
testing, ad-hoc curation, and migration.`,
	Example: `  tillr persona append implementer "Implemented OAuth using go-oidc"
  echo "Long context body..." | tillr persona append researcher --summary "OAuth library research"
  tillr persona append reviewer --summary "Pattern: never use raw json.Marshal" "Use internal/json instead..."`,
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
		fmt.Printf("%s Appended to %s persona context.\n", Success("✓"), Persona(args[0]))
		return nil
	},
}

var personaCompactCmd = &cobra.Command{
	Use:   "compact <name>",
	Short: "Archive older context blocks to a backup, keep recent N verbatim",
	Long: `Compaction archives older blocks of a persona's context file to a
timestamped backup file (swarf/agents/<name>/context.md.pre-compact-*)
and keeps the most recent N blocks verbatim. Useful when the context
file grows past the compact threshold (default 20,000 words).

The orchestrator triggers this automatically post-run when context
crosses the threshold (see open question Q41); manual invocation
remains supported.`,
	Example: `  tillr persona compact implementer
  tillr persona compact researcher --keep 30
  tillr --json persona compact reviewer`,
	Args: cobra.ExactArgs(1),
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
			fmt.Printf("%s Nothing to compact (%d block(s); kept all).\n",
				Dim("·"), result.BlocksKept)
			return nil
		}
		fmt.Printf("%s Compacted %s persona context.\n",
			Success("✓"), Persona(args[0]))
		fmt.Printf("  %s   %s %s %s\n",
			Header("Words:"),
			Dim(fmt.Sprintf("%d", result.WordsBefore)),
			Dim("→"),
			fmt.Sprintf("%d", result.WordsAfter))
		fmt.Printf("  %s  %s archived, %s kept\n",
			Header("Blocks:"),
			Warning(fmt.Sprintf("%d", result.BlocksMoved)),
			Success(fmt.Sprintf("%d", result.BlocksKept)))
		fmt.Printf("  %s  %s\n", Header("Backup:"), Code(result.BackupPath))
		return nil
	},
}

var personaClaimCmd = &cobra.Command{
	Use:   "claim <name>",
	Short: "Claim the next pending feature targeted at this persona",
	Long: `Returns the next feature whose target_persona matches <name> with
status 'draft' or 'queued', and transitions it to 'claimed'. Used by
the orchestrator when dispatching; can be called directly for manual
claiming or testing.`,
	Example: `  tillr persona claim implementer
  tillr --json persona claim implementer  # for agent consumption`,
	Args: cobra.ExactArgs(1),
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
			fmt.Printf("%s No pending features for %s.\n", Dim("·"), Persona(args[0]))
			return nil
		}
		if jsonOutput {
			return printJSON(feature)
		}
		fmt.Printf("%s %s  %s  %s%s\n",
			Success("✓ Claimed"),
			Code(fmt.Sprintf("#%d", feature.ID)),
			feature.Title,
			Dim("→"), Persona(feature.TargetPersona))
		fmt.Printf("%s %s\n", Header("Status:"), Status(feature.Status))
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
