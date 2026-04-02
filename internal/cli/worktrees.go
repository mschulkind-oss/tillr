package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/spf13/cobra"
)

var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Manage code worktrees/workspaces",
	Long: `Manage git/jj worktrees and link them to agent sessions.

Automatically detects whether the project uses jj or git and runs
the appropriate VCS commands.`,
}

func init() {
	worktreeCmd.AddCommand(worktreeListCmd)
	worktreeCmd.AddCommand(worktreeAddCmd)
	worktreeCmd.AddCommand(worktreeRemoveCmd)
	worktreeCmd.AddCommand(worktreeLinkCmd)

	worktreeAddCmd.Flags().String("path", "", "Filesystem path for the new worktree")
}

func detectVCS() string {
	if _, err := os.Stat(".jj"); err == nil {
		return "jj"
	}
	if _, err := os.Stat(".git"); err == nil {
		return "git"
	}
	return ""
}

type vcsWorktree struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	IsMain bool   `json:"is_main"`
}

func parseGitWorktrees() ([]vcsWorktree, error) {
	out, err := exec.Command("git", "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("running git worktree list: %w", err)
	}

	var worktrees []vcsWorktree
	var current vcsWorktree
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	isFirst := true
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if current.Path != "" {
				if current.Name == "" {
					parts := strings.Split(current.Path, string(os.PathSeparator))
					current.Name = parts[len(parts)-1]
				}
				current.IsMain = isFirst
				worktrees = append(worktrees, current)
				isFirst = false
			}
			current = vcsWorktree{}
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			current.Path = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "HEAD ") {
			current.Commit = strings.TrimPrefix(line, "HEAD ")
		} else if strings.HasPrefix(line, "branch ") {
			branch := strings.TrimPrefix(line, "branch ")
			// Strip refs/heads/ prefix
			branch = strings.TrimPrefix(branch, "refs/heads/")
			current.Branch = branch
			if current.Name == "" {
				current.Name = branch
			}
		}
	}
	// Handle last entry (no trailing blank line)
	if current.Path != "" {
		if current.Name == "" {
			parts := strings.Split(current.Path, string(os.PathSeparator))
			current.Name = parts[len(parts)-1]
		}
		current.IsMain = isFirst
		worktrees = append(worktrees, current)
	}
	return worktrees, nil
}

func parseJJWorkspaces() ([]vcsWorktree, error) {
	out, err := exec.Command("jj", "workspace", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("running jj workspace list: %w", err)
	}

	var worktrees []vcsWorktree
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	isFirst := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// jj workspace list format: "name: path"
		parts := strings.SplitN(line, ":", 2)
		name := strings.TrimSpace(parts[0])
		path := ""
		if len(parts) > 1 {
			path = strings.TrimSpace(parts[1])
		}
		worktrees = append(worktrees, vcsWorktree{
			Name:   name,
			Path:   path,
			IsMain: isFirst,
		})
		isFirst = false
	}
	return worktrees, nil
}

var worktreeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List worktrees/workspaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		vcs := detectVCS()
		if vcs == "" {
			if jsonOutput {
				return printJSON(map[string]any{"vcs": "", "worktrees": []any{}})
			}
			fmt.Println("No VCS detected (no .jj or .git directory found).")
			return nil
		}

		var worktrees []vcsWorktree
		var err error
		switch vcs {
		case "jj":
			worktrees, err = parseJJWorkspaces()
		case "git":
			worktrees, err = parseGitWorktrees()
		}
		if err != nil {
			return err
		}

		// Merge with DB records for agent links
		database, _, dbErr := openDB()
		var dbWorktrees []models.Worktree
		if dbErr == nil {
			defer database.Close() //nolint:errcheck
			dbWorktrees, _ = db.ListWorktrees(database)
		}

		type worktreeOutput struct {
			vcsWorktree
			AgentID string `json:"agent_id,omitempty"`
			VCS     string `json:"vcs"`
		}

		var result []worktreeOutput
		for _, wt := range worktrees {
			o := worktreeOutput{vcsWorktree: wt, VCS: vcs}
			for _, dw := range dbWorktrees {
				if dw.Name == wt.Name {
					o.AgentID = dw.AgentSessionID
					break
				}
			}
			result = append(result, o)
		}

		if jsonOutput {
			return printJSON(map[string]any{"vcs": vcs, "worktrees": result})
		}

		fmt.Printf("VCS: %s\n\n", vcs)
		fmt.Printf("%-20s %-40s %-20s %s\n", "NAME", "PATH", "BRANCH", "AGENT")
		fmt.Println(strings.Repeat("─", 90))
		for _, wt := range result {
			main := ""
			if wt.IsMain {
				main = " (main)"
			}
			branch := wt.Branch
			if branch == "" {
				branch = "-"
			}
			agent := wt.AgentID
			if agent == "" {
				agent = "-"
			}
			fmt.Printf("%-20s %-40s %-20s %s%s\n", wt.Name, wt.Path, branch, agent, main)
		}
		return nil
	},
}

var worktreeAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Create a new worktree/workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		vcs := detectVCS()
		if vcs == "" {
			return fmt.Errorf("no VCS detected (no .jj or .git directory found)")
		}

		path, _ := cmd.Flags().GetString("path")
		if path == "" {
			path = "../" + name
		}

		var cmdExec *exec.Cmd
		switch vcs {
		case "jj":
			cmdExec = exec.Command("jj", "workspace", "add", "--name", name, path)
		case "git":
			cmdExec = exec.Command("git", "worktree", "add", path, "-b", name)
		}
		cmdExec.Stdout = os.Stdout
		cmdExec.Stderr = os.Stderr
		if err := cmdExec.Run(); err != nil {
			return fmt.Errorf("creating worktree: %w", err)
		}

		// Record in DB
		database, _, dbErr := openDB()
		if dbErr == nil {
			defer database.Close() //nolint:errcheck
			w := &models.Worktree{
				ID:   fmt.Sprintf("wt-%x", time.Now().UnixNano()&0xFFFFFF),
				Name: name,
				Path: path,
			}
			_ = db.CreateWorktree(database, w)
		}

		if jsonOutput {
			return printJSON(map[string]string{"status": "created", "name": name, "path": path, "vcs": vcs})
		}
		fmt.Printf("✓ Created %s worktree %q at %s\n", vcs, name, path)
		return nil
	},
}

var worktreeRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a worktree/workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		vcs := detectVCS()
		if vcs == "" {
			return fmt.Errorf("no VCS detected (no .jj or .git directory found)")
		}

		// Find path from VCS listing
		var worktrees []vcsWorktree
		var err error
		switch vcs {
		case "jj":
			worktrees, err = parseJJWorkspaces()
		case "git":
			worktrees, err = parseGitWorktrees()
		}
		if err != nil {
			return err
		}

		var target *vcsWorktree
		for i := range worktrees {
			if worktrees[i].Name == name {
				target = &worktrees[i]
				break
			}
		}
		if target == nil {
			return fmt.Errorf("worktree %q not found", name)
		}
		if target.IsMain {
			return fmt.Errorf("cannot remove the main worktree")
		}

		var cmdExec *exec.Cmd
		switch vcs {
		case "jj":
			cmdExec = exec.Command("jj", "workspace", "forget", name)
		case "git":
			cmdExec = exec.Command("git", "worktree", "remove", target.Path)
		}
		cmdExec.Stdout = os.Stdout
		cmdExec.Stderr = os.Stderr
		if err := cmdExec.Run(); err != nil {
			return fmt.Errorf("removing worktree: %w", err)
		}

		// Remove from DB
		database, _, dbErr := openDB()
		if dbErr == nil {
			defer database.Close() //nolint:errcheck
			if dw, getErr := db.GetWorktreeByName(database, name); getErr == nil {
				_ = db.DeleteWorktree(database, dw.ID)
			}
		}

		if jsonOutput {
			return printJSON(map[string]string{"status": "removed", "name": name, "vcs": vcs})
		}
		fmt.Printf("✓ Removed %s worktree %q\n", vcs, name)
		return nil
	},
}

var worktreeLinkCmd = &cobra.Command{
	Use:   "link <worktree-name> <agent-id>",
	Short: "Link a worktree to an agent session",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		wtName := args[0]
		agentID := args[1]

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Verify agent exists
		if _, err := db.GetAgentSession(database, agentID); err != nil {
			return fmt.Errorf("agent session not found: %s", agentID)
		}

		// Find or create worktree DB record
		wt, err := db.GetWorktreeByName(database, wtName)
		if err != nil {
			// Auto-create a DB record from VCS state
			vcs := detectVCS()
			var worktrees []vcsWorktree
			switch vcs {
			case "jj":
				worktrees, _ = parseJJWorkspaces()
			case "git":
				worktrees, _ = parseGitWorktrees()
			}
			var found *vcsWorktree
			for i := range worktrees {
				if worktrees[i].Name == wtName {
					found = &worktrees[i]
					break
				}
			}
			if found == nil {
				return fmt.Errorf("worktree %q not found in VCS or database", wtName)
			}
			wt = &models.Worktree{
				ID:     fmt.Sprintf("wt-%x", time.Now().UnixNano()&0xFFFFFF),
				Name:   found.Name,
				Path:   found.Path,
				Branch: found.Branch,
			}
			if err := db.CreateWorktree(database, wt); err != nil {
				return fmt.Errorf("recording worktree: %w", err)
			}
		}

		if err := db.LinkWorktreeToAgent(database, wt.ID, agentID); err != nil {
			return fmt.Errorf("linking worktree: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]string{"status": "linked", "worktree": wtName, "agent_id": agentID})
		}
		fmt.Printf("✓ Linked worktree %q to agent %s\n", wtName, agentID)
		return nil
	},
}
