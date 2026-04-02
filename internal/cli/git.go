package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/models"
	"github.com/mschulkind/tillr/internal/vcs"
	"github.com/spf13/cobra"
)

var gitCmd = &cobra.Command{
	Use:   "git",
	Short: "Git/VCS integration",
	Long: `View commit history, branch status, and link commits to features.

Automatically detects whether the project uses jj or git and runs
the appropriate VCS commands.`,
}

var gitLogLimit int

func init() {
	gitCmd.AddCommand(gitLogCmd)
	gitCmd.AddCommand(gitLinkCmd)
	gitCmd.AddCommand(gitBranchesCmd)

	gitLogCmd.Flags().IntVarP(&gitLogLimit, "limit", "n", 20, "Number of commits to show")
}

var gitLogCmd = &cobra.Command{
	Use:   "log",
	Short: "Show recent commits",
	RunE: func(cmd *cobra.Command, args []string) error {
		vcsType, commits, err := vcs.GetLog(gitLogLimit)
		if err != nil {
			return fmt.Errorf("reading %s log: %w", vcsType, err)
		}
		if vcsType == "" {
			if jsonOutput {
				return printJSON(map[string]any{"vcs": "", "commits": []any{}})
			}
			fmt.Println("No VCS detected (no .jj or .git directory found).")
			return nil
		}

		if jsonOutput {
			return printJSON(map[string]any{"vcs": vcsType, "commits": commits})
		}

		fmt.Printf("VCS: %s\n\n", vcsType)
		if len(commits) == 0 {
			fmt.Println("No commits found.")
			return nil
		}
		for _, c := range commits {
			date := c.Date
			if len(date) > 16 {
				date = date[:16]
			}
			hash := c.Hash
			if len(hash) > 8 {
				hash = hash[:8]
			}
			fmt.Printf("%-8s  %-16s  %s  %s\n", hash, date, c.Author, c.Message)
		}
		return nil
	},
}

var gitLinkCmd = &cobra.Command{
	Use:   "link <feature-id> <commit-hash>",
	Short: "Link a commit to a feature",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		featureID := args[0]
		commitHash := args[1]

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Verify feature exists and get project ID
		feat, err := db.GetFeature(database, featureID)
		if err != nil {
			return fmt.Errorf("feature %q not found. Run 'tillr feature list' to see available features", featureID)
		}

		// Store as an event
		data, _ := json.Marshal(map[string]string{
			"commit_hash": commitHash,
			"vcs":         vcs.DetectVCS(),
		})
		event := &models.Event{
			ProjectID: feat.ProjectID,
			FeatureID: featureID,
			EventType: "git-link",
			Data:      string(data),
		}

		if err := db.InsertEvent(database, event); err != nil {
			return fmt.Errorf("recording git link: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]string{
				"status":      "linked",
				"feature_id":  featureID,
				"commit_hash": commitHash,
			})
		}
		fmt.Printf("✓ Linked commit %s to feature %s\n", commitHash, featureID)
		return nil
	},
}

var gitBranchesCmd = &cobra.Command{
	Use:   "branches",
	Short: "Show branches and their linked features",
	RunE: func(cmd *cobra.Command, args []string) error {
		vcsType, branches, err := vcs.GetBranches()
		if err != nil {
			return fmt.Errorf("reading %s branches: %w", vcsType, err)
		}
		if vcsType == "" {
			if jsonOutput {
				return printJSON(map[string]any{"vcs": "", "branches": []any{}})
			}
			fmt.Println("No VCS detected (no .jj or .git directory found).")
			return nil
		}

		// Try to match branches to features via worktrees
		database, _, dbErr := openDB()
		if dbErr == nil {
			defer database.Close() //nolint:errcheck
			worktrees, _ := db.ListWorktrees(database)
			for i := range branches {
				for _, wt := range worktrees {
					if wt.Branch == branches[i].Name || wt.Name == branches[i].Name {
						branches[i].FeatureID = wt.AgentSessionID
						break
					}
				}
			}
		}

		if jsonOutput {
			return printJSON(map[string]any{"vcs": vcsType, "branches": branches})
		}

		fmt.Printf("VCS: %s\n\n", vcsType)
		if len(branches) == 0 {
			fmt.Println("No branches found.")
			return nil
		}
		fmt.Printf("%-4s %-30s %-12s %s\n", "", "BRANCH", "COMMIT", "LINKED")
		fmt.Println(strings.Repeat("─", 70))
		for _, b := range branches {
			marker := "  "
			if b.IsCurrent {
				marker = "* "
			}
			commit := b.Commit
			if len(commit) > 10 {
				commit = commit[:10]
			}
			linked := b.FeatureID
			if linked == "" {
				linked = "-"
			}
			fmt.Printf("%s  %-30s %-12s %s\n", marker, b.Name, commit, linked)
		}
		return nil
	},
}
