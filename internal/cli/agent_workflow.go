package cli

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/internal/config"
	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/spf13/cobra"
)

func init() {
	agentCmd.AddCommand(agentCheckoutCmd)
	agentCmd.AddCommand(agentReleaseCmd)
	agentCmd.AddCommand(agentClaimCmd)
	agentCmd.AddCommand(agentUpdateActivityCmd)
	agentCmd.AddCommand(agentSubmitCmd)
	agentCmd.AddCommand(agentPingCmd)
	agentCmd.AddCommand(agentStatusWorkflowCmd)
	agentCmd.AddCommand(agentGCCmd)

	queueCmd.AddCommand(queueScopeCmd)
	queueCmd.AddCommand(queueShowCmd)

	agentCheckoutCmd.Flags().String("name", "", "Worktree label (required)")
	_ = agentCheckoutCmd.MarkFlagRequired("name")

	agentReleaseCmd.Flags().String("name", "", "Worktree label (required)")
	_ = agentReleaseCmd.MarkFlagRequired("name")

	agentUpdateActivityCmd.Flags().String("message", "", "Activity message (required)")
	_ = agentUpdateActivityCmd.MarkFlagRequired("message")

	agentSubmitCmd.Flags().String("message", "", "Commit message")
	agentSubmitCmd.Flags().String("fail", "", "Mark submission as failed with reason")

	agentCmd.AddCommand(agentNextCmd)

	queueScopeCmd.Flags().String("workstream", "", "Filter queue to workstream")
	queueScopeCmd.Flags().Int("min-priority", -1, "Minimum feature priority")
	queueScopeCmd.Flags().String("status", "", "Filter to specific feature status (draft or planning)")
	queueScopeCmd.Flags().Bool("clear", false, "Clear all scope filters")
}

// getAgentWorktreeID returns a stable agent ID for worktree operations.
func getAgentWorktreeID() string {
	if id := os.Getenv("TILLR_AGENT_ID"); id != "" {
		return id
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("agent-%s-%d", host, os.Getpid())
}

// findGitRoot returns the git repository root directory.
func findGitRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// --- tillr agent checkout ---

var agentCheckoutCmd = &cobra.Command{
	Use:   "checkout",
	Short: "Create a git worktree for agent work",
	Long: `Create a new git worktree, register it in the database, and return the path.

The worktree is created as a sibling directory of the main repository
using the naming convention .claude/worktrees/<name>.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Check if worktree already exists in DB
		existing, _ := db.GetAgentWorktreeByName(database, name)
		if existing != nil {
			if jsonOutput {
				return printJSON(map[string]any{
					"status":    "exists",
					"name":      existing.Name,
					"path":      existing.Path,
					"wt_status": existing.Status,
				})
			}
			fmt.Printf("Worktree %q already exists at %s (status: %s)\n", name, existing.Path, existing.Status)
			return nil
		}

		gitRoot, err := findGitRoot()
		if err != nil {
			return err
		}

		// Create worktree path as .claude/worktrees/<name> relative to git root
		wtPath := filepath.Join(gitRoot, ".claude", "worktrees", name)
		branch := fmt.Sprintf("agent/%s", name)

		// Create git worktree
		cmdExec := exec.Command("git", "worktree", "add", wtPath, "-b", branch)
		cmdExec.Dir = gitRoot
		cmdExec.Stderr = os.Stderr
		if err := cmdExec.Run(); err != nil {
			return fmt.Errorf("creating git worktree: %w", err)
		}

		// Register in DB
		agentID := getAgentWorktreeID()
		w := &models.AgentWorktree{
			Name:    name,
			Path:    wtPath,
			AgentID: agentID,
			Status:  "in-use",
		}
		if err := db.CreateAgentWorktree(database, w); err != nil {
			return fmt.Errorf("registering worktree: %w", err)
		}

		_ = db.InsertAgentActivity(database, agentID, "", fmt.Sprintf("checked out worktree %q at %s", name, wtPath))

		if jsonOutput {
			return printJSON(map[string]any{
				"status": "created",
				"name":   name,
				"path":   wtPath,
				"branch": branch,
			})
		}
		fmt.Printf("Created worktree %q at %s (branch: %s)\n", name, wtPath, branch)
		return nil
	},
}

// --- tillr agent release ---

var agentReleaseCmd = &cobra.Command{
	Use:   "release",
	Short: "Mark a worktree as available",
	Long: `Mark a worktree as available for reuse. Warns if the working directory
has uncommitted changes.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		wt, err := db.GetAgentWorktreeByName(database, name)
		if err != nil {
			return fmt.Errorf("worktree %q not found: %w", name, err)
		}

		// Check for dirty working directory
		cmdExec := exec.Command("git", "status", "--porcelain")
		cmdExec.Dir = wt.Path
		out, _ := cmdExec.Output()
		if len(strings.TrimSpace(string(out))) > 0 {
			if !jsonOutput {
				fmt.Fprintf(os.Stderr, "Warning: worktree %q has uncommitted changes\n", name)
			}
		}

		// Release any active claim for this worktree
		agentID := getAgentWorktreeID()
		claim, _ := db.GetActiveClaimForAgent(database, agentID)
		if claim != nil && claim.WorktreeName == name {
			_ = db.UpdateAgentClaimStatus(database, claim.ID, "released")
		}

		if err := db.UpdateAgentWorktreeStatus(database, name, "available", ""); err != nil {
			return fmt.Errorf("releasing worktree: %w", err)
		}

		_ = db.InsertAgentActivity(database, agentID, "", fmt.Sprintf("released worktree %q", name))

		if jsonOutput {
			return printJSON(map[string]string{"status": "released", "name": name})
		}
		fmt.Printf("Released worktree %q\n", name)
		return nil
	},
}

// --- tillr agent claim ---

var agentClaimCmd = &cobra.Command{
	Use:   "claim",
	Short: "Claim the next available feature from the queue",
	Long: `Find the next claimable feature (topologically sorted by deps, then by priority),
lock it to this agent, transition it to 'implementing', and print feature details.

With --json, outputs everything the agent needs: feature ID, name, spec,
description, dependencies, and linked context.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := resolveProject(database, cfg)
		if err != nil {
			return err
		}

		agentID := getAgentWorktreeID()

		// Check if agent already has an active claim
		existingClaim, _ := db.GetActiveClaimForAgent(database, agentID)
		if existingClaim != nil {
			f, _ := db.GetFeature(database, existingClaim.FeatureID)
			if jsonOutput {
				return printJSON(map[string]any{
					"status":  "already_claimed",
					"claim":   existingClaim,
					"feature": f,
				})
			}
			fmt.Printf("Already claiming feature %s\n", existingClaim.FeatureID)
			return nil
		}

		// Get scope filters
		scope, err := db.GetQueueScopeMap(database)
		if err != nil {
			scope = make(map[string]string)
		}

		// Get claimable features
		features, err := db.GetClaimableFeatures(database, p.ID, scope)
		if err != nil {
			return fmt.Errorf("querying claimable features: %w", err)
		}

		if len(features) == 0 {
			if jsonOutput {
				return printJSON(map[string]string{"status": "no_work"})
			}
			fmt.Println("No claimable features in queue.")
			return nil
		}

		// Claim the first feature
		feature := features[0]

		// Determine worktree name (if agent has one)
		var wtName string
		worktrees, _ := db.ListAgentWorktrees(database)
		for _, wt := range worktrees {
			if wt.AgentID == agentID && wt.Status == "in-use" {
				wtName = wt.Name
				break
			}
		}

		claim := &models.AgentClaim{
			AgentID:      agentID,
			FeatureID:    feature.ID,
			WorktreeName: wtName,
			Status:       "active",
		}
		if err := db.CreateAgentClaim(database, claim); err != nil {
			return fmt.Errorf("creating claim: %w", err)
		}

		// Transition feature to 'implementing'
		_ = db.UpdateFeature(database, feature.ID, map[string]any{
			"status":          "implementing",
			"previous_status": feature.Status,
		})

		_ = db.InsertAgentActivity(database, agentID, feature.ID, fmt.Sprintf("claimed feature %q", feature.Name))

		// Load context entries for the feature
		contexts, _ := db.ListContext(database, p.ID, feature.ID)

		// Load rejection notes from QA history
		qaResults, _ := db.ListQAResults(database, feature.ID)
		var rejectionNotes []map[string]any
		for _, r := range qaResults {
			if !r.Passed && r.Notes != "" {
				rejectionNotes = append(rejectionNotes, map[string]any{
					"notes":      r.Notes,
					"created_at": r.CreatedAt,
					"qa_type":    r.QAType,
				})
			}
		}

		if jsonOutput {
			result := map[string]any{
				"status":   "claimed",
				"claim":    claim,
				"feature":  feature,
				"contexts": contexts,
			}
			if len(rejectionNotes) > 0 {
				result["rejection_notes"] = rejectionNotes
			}
			return printJSON(result)
		}

		fmt.Printf("Claimed feature: %s\n", feature.ID)
		fmt.Printf("  Name:     %s\n", feature.Name)
		fmt.Printf("  Priority: %d\n", feature.Priority)
		if feature.Description != "" {
			fmt.Printf("  Desc:     %s\n", feature.Description)
		}
		if feature.Spec != "" {
			fmt.Printf("  Spec:     %s\n", truncate(feature.Spec, 200))
		}
		if len(feature.DependsOn) > 0 {
			fmt.Printf("  Deps:     %s\n", strings.Join(feature.DependsOn, ", "))
		}
		return nil
	},
}

// --- tillr agent update (activity log) ---

var agentUpdateActivityCmd = &cobra.Command{
	Use:   "log-activity",
	Short: "Log an activity message",
	Long:  `Record an activity message in the agent activity log.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		message, _ := cmd.Flags().GetString("message")

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		agentID := getAgentWorktreeID()

		// Get current claim's feature ID if any
		var featureID string
		claim, _ := db.GetActiveClaimForAgent(database, agentID)
		if claim != nil {
			featureID = claim.FeatureID
		}

		if err := db.InsertAgentActivity(database, agentID, featureID, message); err != nil {
			return fmt.Errorf("logging activity: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]string{"status": "logged", "agent_id": agentID, "message": message})
		}
		fmt.Printf("Logged: %s\n", message)
		return nil
	},
}

// --- tillr agent submit ---

// runValidationChecks runs compile checks (go build, tsc) in the given directory.
// Returns nil if all checks pass, or an error describing the failure.
func runValidationChecks(dir string) error {
	// go build ./...
	goBuild := exec.Command("go", "build", "./...")
	goBuild.Dir = dir
	goBuild.Stdout = os.Stdout
	goBuild.Stderr = os.Stderr
	if err := goBuild.Run(); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}

	// Check if web/ directory exists, and if so run tsc
	webDir := filepath.Join(dir, "web")
	if info, err := os.Stat(webDir); err == nil && info.IsDir() {
		tsc := exec.Command("npx", "tsc", "--noEmit")
		tsc.Dir = webDir
		tsc.Stdout = os.Stdout
		tsc.Stderr = os.Stderr
		if err := tsc.Run(); err != nil {
			return fmt.Errorf("tsc --noEmit failed: %w", err)
		}
	}

	return nil
}

var agentSubmitCmd = &cobra.Command{
	Use:   "submit",
	Short: "Commit, push, create PR, and advance feature to human-qa",
	Long: `Submit the current work: run validation checks, commit all changes,
push the branch, create a pull request via 'gh pr create', link the PR
to the feature, and transition the feature to 'human-qa' status.

Use --fail to mark the submission as failed and return the feature to
implementing status without committing or pushing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		agentID := getAgentWorktreeID()
		claim, err := db.GetActiveClaimForAgent(database, agentID)
		if err != nil || claim == nil {
			return fmt.Errorf("no active claim for this agent")
		}

		feature, err := db.GetFeature(database, claim.FeatureID)
		if err != nil {
			return fmt.Errorf("feature not found: %w", err)
		}

		// Handle --fail flag
		failReason, _ := cmd.Flags().GetString("fail")
		if failReason != "" {
			// Transition back to implementing
			_ = db.UpdateFeature(database, feature.ID, map[string]any{
				"status":          "implementing",
				"previous_status": feature.Status,
			})

			// Record failed QA result
			_ = db.CreateQAResult(database, &models.QAResult{
				FeatureID: feature.ID,
				QAType:    "agent",
				Passed:    false,
				Notes:     failReason,
			})

			_ = db.InsertAgentActivity(database, agentID, feature.ID, fmt.Sprintf("submit failed for %q: %s", feature.Name, failReason))

			if jsonOutput {
				return printJSON(map[string]any{
					"status":     "failed",
					"feature_id": feature.ID,
					"reason":     failReason,
				})
			}
			fmt.Printf("Marked feature %s as failed: %s\n", feature.ID, failReason)
			return nil
		}

		// Find the worktree path
		var workDir string
		if claim.WorktreeName != "" {
			wt, wtErr := db.GetAgentWorktreeByName(database, claim.WorktreeName)
			if wtErr == nil {
				workDir = wt.Path
			}
		}
		if workDir == "" {
			workDir, _ = os.Getwd()
		}

		// Determine the project root for validation checks
		checkDir := workDir
		if gitRoot, err := findGitRoot(); err == nil {
			checkDir = gitRoot
		}

		// Run validation checks before git operations
		if err := runValidationChecks(checkDir); err != nil {
			return fmt.Errorf("validation checks failed: %w", err)
		}

		commitMsg, _ := cmd.Flags().GetString("message")
		if commitMsg == "" {
			commitMsg = fmt.Sprintf("feat: %s", feature.Name)
		}

		// Git add + commit
		addCmd := exec.Command("git", "add", "-A")
		addCmd.Dir = workDir
		addCmd.Stderr = os.Stderr
		if err := addCmd.Run(); err != nil {
			return fmt.Errorf("git add: %w", err)
		}

		commitCmd := exec.Command("git", "commit", "-m", commitMsg)
		commitCmd.Dir = workDir
		commitCmd.Stderr = os.Stderr
		_ = commitCmd.Run() // May fail if nothing to commit — that's OK

		// Get current branch
		branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
		branchCmd.Dir = workDir
		branchOut, err := branchCmd.Output()
		if err != nil {
			return fmt.Errorf("getting branch: %w", err)
		}
		branch := strings.TrimSpace(string(branchOut))

		// Push
		pushCmd := exec.Command("git", "push", "-u", "origin", branch)
		pushCmd.Dir = workDir
		pushCmd.Stderr = os.Stderr
		if err := pushCmd.Run(); err != nil {
			return fmt.Errorf("git push: %w", err)
		}

		// Create PR via gh
		prTitle := fmt.Sprintf("[%s] %s", claim.FeatureID, feature.Name)
		prBody := fmt.Sprintf("## Feature\n\n**ID:** %s\n**Name:** %s\n\n%s",
			feature.ID, feature.Name, feature.Description)

		prCmd := exec.Command("gh", "pr", "create", "--title", prTitle, "--body", prBody)
		prCmd.Dir = workDir
		prCmd.Stderr = os.Stderr
		prOut, prErr := prCmd.Output()
		var prURL string
		if prErr == nil {
			prURL = strings.TrimSpace(string(prOut))
		}

		// Link PR to feature if we got a URL
		if prURL != "" {
			_ = db.LinkFeaturePR(database, &models.FeaturePR{
				FeatureID: feature.ID,
				PRURL:     prURL,
				Status:    "open",
			})
		}

		// Record passing agent QA result
		_ = db.CreateQAResult(database, &models.QAResult{
			FeatureID: feature.ID,
			QAType:    "agent",
			Passed:    true,
		})

		// Transition feature to human-qa (compile checks ARE the agent QA)
		_ = db.UpdateFeature(database, feature.ID, map[string]any{
			"status":          "human-qa",
			"previous_status": feature.Status,
		})

		// Complete the claim
		_ = db.UpdateAgentClaimStatus(database, claim.ID, "completed")

		_ = db.InsertAgentActivity(database, agentID, feature.ID, fmt.Sprintf("submitted feature %q (PR: %s)", feature.Name, prURL))

		if jsonOutput {
			return printJSON(map[string]any{
				"status":     "submitted",
				"feature_id": feature.ID,
				"branch":     branch,
				"pr_url":     prURL,
			})
		}
		fmt.Printf("Submitted feature %s\n", feature.ID)
		fmt.Printf("  Branch: %s\n", branch)
		if prURL != "" {
			fmt.Printf("  PR:     %s\n", prURL)
		}
		fmt.Printf("  Status: implementing -> human-qa\n")
		return nil
	},
}

// --- tillr agent next ---

var agentNextCmd = &cobra.Command{
	Use:   "next",
	Short: "Submit current claim (if any) then claim next feature",
	Long: `Convenience command that runs the agent loop body:
submit the current claim (if any), then claim the next feature.

If there is an active claim, the submit logic runs first (including
validation checks). Then the claim logic runs to pick up the next
feature from the queue.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		agentID := getAgentWorktreeID()

		// If there's an active claim, submit it first
		existingClaim, _ := db.GetActiveClaimForAgent(database, agentID)
		if existingClaim != nil {
			feature, fErr := db.GetFeature(database, existingClaim.FeatureID)
			if fErr != nil {
				return fmt.Errorf("feature not found for active claim: %w", fErr)
			}

			// Find the worktree path
			var workDir string
			if existingClaim.WorktreeName != "" {
				wt, wtErr := db.GetAgentWorktreeByName(database, existingClaim.WorktreeName)
				if wtErr == nil {
					workDir = wt.Path
				}
			}
			if workDir == "" {
				workDir, _ = os.Getwd()
			}

			// Determine the project root for validation checks
			checkDir := workDir
			if gitRoot, grErr := findGitRoot(); grErr == nil {
				checkDir = gitRoot
			}

			// Run validation checks
			if err := runValidationChecks(checkDir); err != nil {
				return fmt.Errorf("validation checks failed (submit aborted): %w", err)
			}

			commitMsg := fmt.Sprintf("feat: %s", feature.Name)

			// Git add + commit
			addCmd := exec.Command("git", "add", "-A")
			addCmd.Dir = workDir
			addCmd.Stderr = os.Stderr
			if err := addCmd.Run(); err != nil {
				return fmt.Errorf("git add: %w", err)
			}

			commitExec := exec.Command("git", "commit", "-m", commitMsg)
			commitExec.Dir = workDir
			commitExec.Stderr = os.Stderr
			_ = commitExec.Run()

			// Get current branch
			branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
			branchCmd.Dir = workDir
			branchOut, bErr := branchCmd.Output()
			if bErr != nil {
				return fmt.Errorf("getting branch: %w", bErr)
			}
			branch := strings.TrimSpace(string(branchOut))

			// Push
			pushCmd := exec.Command("git", "push", "-u", "origin", branch)
			pushCmd.Dir = workDir
			pushCmd.Stderr = os.Stderr
			if err := pushCmd.Run(); err != nil {
				return fmt.Errorf("git push: %w", err)
			}

			// Create PR
			prTitle := fmt.Sprintf("[%s] %s", existingClaim.FeatureID, feature.Name)
			prBody := fmt.Sprintf("## Feature\n\n**ID:** %s\n**Name:** %s\n\n%s",
				feature.ID, feature.Name, feature.Description)
			prCmd := exec.Command("gh", "pr", "create", "--title", prTitle, "--body", prBody)
			prCmd.Dir = workDir
			prCmd.Stderr = os.Stderr
			prOut, prErr := prCmd.Output()
			var prURL string
			if prErr == nil {
				prURL = strings.TrimSpace(string(prOut))
			}

			if prURL != "" {
				_ = db.LinkFeaturePR(database, &models.FeaturePR{
					FeatureID: feature.ID,
					PRURL:     prURL,
					Status:    "open",
				})
			}

			// Record passing agent QA
			_ = db.CreateQAResult(database, &models.QAResult{
				FeatureID: feature.ID,
				QAType:    "agent",
				Passed:    true,
			})

			_ = db.UpdateFeature(database, feature.ID, map[string]any{
				"status":          "human-qa",
				"previous_status": feature.Status,
			})
			_ = db.UpdateAgentClaimStatus(database, existingClaim.ID, "completed")
			_ = db.InsertAgentActivity(database, agentID, feature.ID, fmt.Sprintf("submitted feature %q via next (PR: %s)", feature.Name, prURL))

			if !jsonOutput {
				fmt.Printf("Submitted feature %s -> human-qa\n", feature.ID)
			}
		}

		// Now claim next feature
		p, err := resolveProject(database, cfg)
		if err != nil {
			return err
		}

		scope, err := db.GetQueueScopeMap(database)
		if err != nil {
			scope = make(map[string]string)
		}

		features, err := db.GetClaimableFeatures(database, p.ID, scope)
		if err != nil {
			return fmt.Errorf("querying claimable features: %w", err)
		}

		if len(features) == 0 {
			if jsonOutput {
				return printJSON(map[string]string{"status": "no_work"})
			}
			fmt.Println("No claimable features in queue.")
			return nil
		}

		feature := features[0]

		var wtName string
		worktrees, _ := db.ListAgentWorktrees(database)
		for _, wt := range worktrees {
			if wt.AgentID == agentID && wt.Status == "in-use" {
				wtName = wt.Name
				break
			}
		}

		claim := &models.AgentClaim{
			AgentID:      agentID,
			FeatureID:    feature.ID,
			WorktreeName: wtName,
			Status:       "active",
		}
		if err := db.CreateAgentClaim(database, claim); err != nil {
			return fmt.Errorf("creating claim: %w", err)
		}

		_ = db.UpdateFeature(database, feature.ID, map[string]any{
			"status":          "implementing",
			"previous_status": feature.Status,
		})
		_ = db.InsertAgentActivity(database, agentID, feature.ID, fmt.Sprintf("claimed feature %q", feature.Name))

		contexts, _ := db.ListContext(database, p.ID, feature.ID)

		qaResults, _ := db.ListQAResults(database, feature.ID)
		var rejectionNotes []map[string]any
		for _, r := range qaResults {
			if !r.Passed && r.Notes != "" {
				rejectionNotes = append(rejectionNotes, map[string]any{
					"notes":      r.Notes,
					"created_at": r.CreatedAt,
					"qa_type":    r.QAType,
				})
			}
		}

		if jsonOutput {
			result := map[string]any{
				"status":   "claimed",
				"claim":    claim,
				"feature":  feature,
				"contexts": contexts,
			}
			if len(rejectionNotes) > 0 {
				result["rejection_notes"] = rejectionNotes
			}
			return printJSON(result)
		}

		fmt.Printf("Claimed feature: %s\n", feature.ID)
		fmt.Printf("  Name:     %s\n", feature.Name)
		fmt.Printf("  Priority: %d\n", feature.Priority)
		if feature.Description != "" {
			fmt.Printf("  Desc:     %s\n", feature.Description)
		}
		return nil
	},
}

// --- tillr agent ping ---

var agentPingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Update heartbeat timestamp",
	Long:  `Update the heartbeat timestamp for this agent's worktree.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		agentID := getAgentWorktreeID()

		// Update heartbeat on all worktrees owned by this agent
		worktrees, _ := db.ListAgentWorktrees(database)
		updated := 0
		for _, wt := range worktrees {
			if wt.AgentID == agentID && wt.Status == "in-use" {
				_ = db.UpdateAgentWorktreeHeartbeat(database, wt.Name)
				updated++
			}
		}

		if jsonOutput {
			return printJSON(map[string]any{
				"status":            "ok",
				"agent_id":          agentID,
				"worktrees_updated": updated,
				"timestamp":         time.Now().UTC().Format(time.RFC3339),
			})
		}
		fmt.Printf("Heartbeat updated (%d worktree(s))\n", updated)
		return nil
	},
}

// --- tillr agent status (workflow) ---

var agentStatusWorkflowCmd = &cobra.Command{
	Use:   "info",
	Short: "Show current claim, worktree, and time active",
	Long: `Display the current agent's workflow status: active claim,
worktree assignment, and elapsed time.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		agentID := getAgentWorktreeID()

		claim, _ := db.GetActiveClaimForAgent(database, agentID)
		worktrees, _ := db.ListAgentWorktrees(database)

		var agentWorktrees []models.AgentWorktree
		for _, wt := range worktrees {
			if wt.AgentID == agentID {
				agentWorktrees = append(agentWorktrees, wt)
			}
		}

		activity, _ := db.ListAgentActivity(database, agentID, 10)

		if jsonOutput {
			result := map[string]any{
				"agent_id":  agentID,
				"worktrees": agentWorktrees,
				"activity":  activity,
			}
			if claim != nil {
				result["claim"] = claim
				f, _ := db.GetFeature(database, claim.FeatureID)
				if f != nil {
					result["feature"] = f
				}
			}
			return printJSON(result)
		}

		fmt.Printf("Agent: %s\n\n", agentID)

		if claim != nil {
			fmt.Printf("Active Claim:\n")
			fmt.Printf("  Feature: %s\n", claim.FeatureID)
			fmt.Printf("  Since:   %s\n", claim.ClaimedAt)
			f, _ := db.GetFeature(database, claim.FeatureID)
			if f != nil {
				fmt.Printf("  Name:    %s\n", f.Name)
				fmt.Printf("  Status:  %s\n", f.Status)
			}
		} else {
			fmt.Println("No active claim.")
		}

		if len(agentWorktrees) > 0 {
			fmt.Printf("\nWorktrees:\n")
			for _, wt := range agentWorktrees {
				fmt.Printf("  %-20s %-10s %s\n", wt.Name, wt.Status, wt.Path)
			}
		}

		if len(activity) > 0 {
			fmt.Printf("\nRecent Activity:\n")
			for _, a := range activity {
				fmt.Printf("  %s  %s\n", a.CreatedAt, a.Message)
			}
		}

		return nil
	},
}

// --- tillr agent gc ---

var agentGCCmd = &cobra.Command{
	Use:   "gc",
	Short: "Reclaim worktrees with stale heartbeats (>10 min)",
	Long: `Mark worktrees as stale when their heartbeat is older than 10 minutes.
Stale worktrees are released and their claims are freed.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		reclaimed, err := db.GCStaleAgentWorktrees(database, 10)
		if err != nil {
			return fmt.Errorf("gc: %w", err)
		}

		// Also release claims for stale worktrees
		claims, _ := db.ListActiveAgentClaims(database)
		releasedClaims := 0
		for _, c := range claims {
			if c.WorktreeName != "" {
				wt, wtErr := db.GetAgentWorktreeByName(database, c.WorktreeName)
				if wtErr == nil && wt.Status == "stale" {
					_ = db.UpdateAgentClaimStatus(database, c.ID, "released")
					// Revert feature status
					_ = db.UpdateFeature(database, c.FeatureID, map[string]any{
						"status": "draft",
					})
					releasedClaims++
				}
			}
		}

		if jsonOutput {
			return printJSON(map[string]any{
				"stale_worktrees": reclaimed,
				"released_claims": releasedClaims,
			})
		}

		if reclaimed == 0 && releasedClaims == 0 {
			fmt.Println("No stale worktrees or claims to reclaim.")
		} else {
			fmt.Printf("Reclaimed %d stale worktree(s), released %d claim(s)\n", reclaimed, releasedClaims)
		}
		return nil
	},
}

// --- tillr queue scope ---

var queueScopeCmd = &cobra.Command{
	Use:   "scope",
	Short: "Set queue scope filters",
	Long: `Configure which features are eligible for agent claiming.
Filters include workstream, minimum priority, and feature status.

With no flags, displays the current scope configuration.
Use --clear to remove all scope filters.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		clearAll, _ := cmd.Flags().GetBool("clear")
		if clearAll {
			for _, k := range []string{"workstream", "min-priority", "status"} {
				_ = db.DeleteQueueScope(database, k)
			}
			if jsonOutput {
				return printJSON(map[string]string{"status": "cleared"})
			}
			fmt.Println("Queue scope cleared.")
			return nil
		}

		// Check if any flag was set
		anySet := false

		if ws, _ := cmd.Flags().GetString("workstream"); ws != "" {
			_ = db.SetQueueScope(database, "workstream", ws)
			anySet = true
		}
		if minPri, _ := cmd.Flags().GetInt("min-priority"); minPri >= 0 {
			_ = db.SetQueueScope(database, "min-priority", fmt.Sprintf("%d", minPri))
			anySet = true
		}
		if st, _ := cmd.Flags().GetString("status"); st != "" {
			_ = db.SetQueueScope(database, "status", st)
			anySet = true
		}

		if anySet {
			if jsonOutput {
				scope, _ := db.GetQueueScopeMap(database)
				return printJSON(map[string]any{"status": "updated", "scope": scope})
			}
			fmt.Println("Queue scope updated.")
		}

		// Display current scope
		entries, err := db.ListQueueScope(database)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(entries)
		}

		if len(entries) == 0 {
			fmt.Println("No scope filters set (all eligible features will be queued).")
			return nil
		}

		fmt.Printf("Queue Scope:\n")
		for _, e := range entries {
			fmt.Printf("  %-15s = %s\n", e.Key, e.Value)
		}
		return nil
	},
}

// --- tillr queue show ---

var queueShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display current scope and ordered queue contents",
	Long: `Show the current queue scope configuration and the ordered list of
features available for claiming. Features are sorted by dependency
satisfaction, then by priority (descending).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := resolveProject(database, cfg)
		if err != nil {
			return err
		}

		scope, _ := db.GetQueueScopeMap(database)
		features, err := db.GetClaimableFeatures(database, p.ID, scope)
		if err != nil {
			return fmt.Errorf("loading queue: %w", err)
		}

		activeClaims, _ := db.ListActiveAgentClaims(database)

		if jsonOutput {
			return printJSON(map[string]any{
				"scope":         scope,
				"queue":         features,
				"active_claims": activeClaims,
			})
		}

		// Print scope
		fmt.Printf("Queue Scope:\n")
		if len(scope) == 0 {
			fmt.Printf("  (no filters)\n")
		} else {
			for k, v := range scope {
				fmt.Printf("  %-15s = %s\n", k, v)
			}
		}

		// Print active claims
		if len(activeClaims) > 0 {
			fmt.Printf("\nActive Claims:\n")
			fmt.Printf("  %-20s %-25s %s\n", "AGENT", "FEATURE", "SINCE")
			fmt.Println("  " + strings.Repeat("-", 70))
			for _, c := range activeClaims {
				fmt.Printf("  %-20s %-25s %s\n", truncate(c.AgentID, 18), c.FeatureID, c.ClaimedAt)
			}
		}

		// Print queue
		fmt.Printf("\nClaimable Features (%d):\n", len(features))
		if len(features) == 0 {
			fmt.Printf("  (empty)\n")
			return nil
		}

		fmt.Printf("  %-4s %-30s %-10s %-8s %s\n", "#", "ID", "STATUS", "PRIO", "NAME")
		fmt.Println("  " + strings.Repeat("-", 80))
		for i, f := range features {
			name := f.Name
			if len(name) > 35 {
				name = name[:32] + "..."
			}
			fmt.Printf("  %-4d %-30s %-10s %-8d %s\n", i+1, f.ID, f.Status, f.Priority, name)
		}
		return nil
	},
}

// resolveProject returns the project for this database (one project per DB).
func resolveProject(database *sql.DB, _ *config.Config) (*models.Project, error) {
	return db.GetProject(database)
}
