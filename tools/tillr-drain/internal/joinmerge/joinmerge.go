package joinmerge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/internal/boundary"
	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
)

// GitFunc runs a git command in dir and returns stdout.
type GitFunc func(ctx context.Context, dir string, args ...string) (string, error)

// SiblingCommit describes one changed parallel writer commit to fold at join.
type SiblingCommit struct {
	NodeID     string
	PinRef     string
	HeadSHA    string
	TreeSHA    string
	Subject    string
	Message    string
	AuthorDate string
}

// MergeResult is the outcome of folding changed sibling commits onto baseSHA.
type MergeResult struct {
	Clean            bool
	TreeSHA          string
	MirrorOnly       bool
	MirrorModules    []string
	ConflictingPaths []string
	ConflictReports  []string
	ConflictKey      string
}

// FoldSiblingCommits folds changed sibling commits onto baseSHA in deterministic order
// using git merge-tree --write-tree --merge-base=<baseSHA>.
func FoldSiblingCommits(ctx context.Context, repo, baseSHA string, siblings []SiblingCommit) (MergeResult, error) {
	if len(siblings) == 0 {
		return MergeResult{Clean: true}, nil
	}
	if len(siblings) == 1 {
		return MergeResult{Clean: true, TreeSHA: siblings[0].TreeSHA}, nil
	}

	accumulated := baseSHA
	mirrorOnly := false
	seenModule := map[string]bool{}
	var mirrorModules []string

	for _, sib := range siblings {
		out, exitCode, err := runGitAllowConflict(ctx, repo, "merge-tree", "--write-tree", "--merge-base="+baseSHA, accumulated, sib.HeadSHA)
		if err != nil {
			return MergeResult{}, err
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
			return MergeResult{}, fmt.Errorf("git merge-tree returned empty output for %s", sib.NodeID)
		}
		treeSHA := strings.TrimSpace(lines[0])
		if exitCode == 0 {
			accumulated = treeSHA
			continue
		}

		var reports []string
		for _, line := range lines[1:] {
			if strings.HasPrefix(line, "CONFLICT ") {
				reports = append(reports, line)
			}
		}
		nonMirror, mods := boundary.FilterMirrorConflictReports(reports, runhistory.ConflictPath)
		for _, m := range mods {
			if !seenModule[m] {
				seenModule[m] = true
				mirrorModules = append(mirrorModules, m)
			}
		}
		if len(nonMirror) == 0 && len(reports) > 0 {
			mirrorOnly = true
			resolvedTree, err := restoreMirrorPathsInTree(ctx, repo, treeSHA, baseSHA)
			if err != nil {
				return MergeResult{}, fmt.Errorf("restoring pack mirror paths after %s: %w", sib.NodeID, err)
			}
			accumulated = resolvedTree
			continue
		}

		pathSet := map[string]bool{}
		var paths []string
		for _, r := range nonMirror {
			p := runhistory.ConflictPath(r)
			if p != "" && !pathSet[p] {
				pathSet[p] = true
				paths = append(paths, p)
			}
		}
		sort.Strings(paths)
		sort.Strings(mirrorModules)
		return MergeResult{
			Clean:            false,
			MirrorModules:    mirrorModules,
			ConflictingPaths: paths,
			ConflictReports:  nonMirror,
			ConflictKey:      ComputeConflictKey(baseSHA, siblings, paths),
		}, nil
	}

	sort.Strings(mirrorModules)
	return MergeResult{
		Clean:         true,
		TreeSHA:       accumulated,
		MirrorOnly:    mirrorOnly,
		MirrorModules: mirrorModules,
	}, nil
}

// ComputeConflictKey computes a deterministic key over baseSHA, ordered sibling
// (node_id, tree_sha) pairs, and sorted conflicting paths.
func ComputeConflictKey(baseSHA string, siblings []SiblingCommit, paths []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "base:%s\n", baseSHA)
	for _, s := range siblings {
		fmt.Fprintf(h, "sib:%s:%s\n", s.NodeID, s.TreeSHA)
	}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	for _, p := range sorted {
		fmt.Fprintf(h, "path:%s\n", p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// CombinedCommitMessage formats the joined commit message matching Trunk.Squash:
// the first changed sibling's message, plus "- <subject>" bullets for remaining siblings.
func CombinedCommitMessage(siblings []SiblingCommit) string {
	if len(siblings) == 0 {
		return ""
	}
	msg := strings.TrimRight(siblings[0].Message, "\n")
	if len(siblings) == 1 {
		return msg + "\n"
	}
	var b strings.Builder
	b.WriteString(msg)
	b.WriteString("\n")
	for _, s := range siblings[1:] {
		subj := strings.TrimSpace(s.Subject)
		if subj == "" {
			subj = s.NodeID
		}
		b.WriteString("\n- ")
		b.WriteString(subj)
	}
	b.WriteString("\n")
	return b.String()
}

// JoinConflictRecord is the payload recorded on drain.join-conflict events.
type JoinConflictRecord struct {
	ItemID           string   `json:"item_id"`
	Attempt          int      `json:"attempt"`
	JoinNodeID       string   `json:"join_node_id"`
	ConflictKey      string   `json:"conflict_key"`
	BaseSHA          string   `json:"base_sha"`
	ConflictingPaths []string `json:"conflicting_paths"`
}

// CommitTreeWithRepoIdentity creates a single-parent commit from treeSHA using the
// repository's configured user.name and user.email and optional GIT_AUTHOR_DATE.
func CommitTreeWithRepoIdentity(ctx context.Context, dir, treeSHA, parentSHA, authorDate, message string, git GitFunc) (string, error) {
	name, err := git(ctx, dir, "config", "user.name")
	if err != nil || strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("join commit-tree: git user.name is not configured")
	}
	email, err := git(ctx, dir, "config", "user.email")
	if err != nil || strings.TrimSpace(email) == "" {
		return "", fmt.Errorf("join commit-tree: git user.email is not configured")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "commit-tree", treeSHA, "-p", parentSHA)
	env := os.Environ()
	env = append(env,
		"GIT_AUTHOR_NAME="+strings.TrimSpace(name),
		"GIT_AUTHOR_EMAIL="+strings.TrimSpace(email),
		"GIT_COMMITTER_NAME="+strings.TrimSpace(name),
		"GIT_COMMITTER_EMAIL="+strings.TrimSpace(email),
	)
	if strings.TrimSpace(authorDate) != "" {
		env = append(env, "GIT_AUTHOR_DATE="+strings.TrimSpace(authorDate))
	}
	cmd.Env = env
	cmd.Stdin = strings.NewReader(message)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git commit-tree: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// FormatReconcilePrompt builds the brief prompt for an intra-item <join-id>-reconcile writer stage.
func FormatReconcilePrompt(ctx context.Context, root, base string, changed []SiblingCommit, conflictingPaths []string, git GitFunc) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Reconcile overlapping changes from parallel writers onto base %s.\n\n", base)
	fmt.Fprintf(&prompt, "Conflicting non-mirror paths:\n")
	for _, p := range conflictingPaths {
		fmt.Fprintf(&prompt, "- %s\n", p)
	}
	fmt.Fprintf(&prompt, "\nSibling writer commits (pinned, do not discard):\n")
	for _, c := range changed {
		diffStat, _ := git(ctx, root, "diff", "--stat", base+".."+c.HeadSHA)
		fmt.Fprintf(&prompt, "- node %s: ref=%s sha=%s\n%s\n", c.NodeID, c.PinRef, c.HeadSHA, strings.TrimSpace(diffStat))
	}
	fmt.Fprintf(&prompt, "\nCommit the reconciled tree onto the primary item worktree from %s, or end blocked with OBSTACLE: definition and EVIDENCE: plan-wrong: <reason>.\n", base)
	return prompt.String()
}

func runGitAllowConflict(ctx context.Context, dir string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return stdout.String(), 1, nil
	}
	return "", -1, fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
}

func restoreMirrorPathsInTree(ctx context.Context, repo, conflictedTree, baseSHA string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "tillr-join-index-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)
	indexPath := filepath.Join(tmpDir, "index")

	runIdx := func(stdin string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return stdout.String(), nil
	}

	if _, err := runIdx("", "read-tree", conflictedTree); err != nil {
		return "", err
	}

	rawPaths, err := runIdx("", "ls-tree", "-r", "--name-only", "-z", conflictedTree)
	if err != nil {
		return "", err
	}
	var mirrorPaths []string
	for _, p := range strings.Split(rawPaths, "\x00") {
		if p == "" {
			continue
		}
		if _, ok := boundary.PackMirrorModule(p); ok {
			mirrorPaths = append(mirrorPaths, p)
		}
	}
	if len(mirrorPaths) == 0 {
		return conflictedTree, nil
	}

	// Collect baseSHA's entries for those mirror paths.
	lsArgs := append([]string{"ls-tree", "-r", "-z", baseSHA, "--"}, mirrorPaths...)
	baseEntriesRaw, err := runIdx("", lsArgs...)
	if err != nil {
		return "", err
	}
	baseEntries := map[string]string{}
	for _, entry := range strings.Split(baseEntriesRaw, "\x00") {
		if entry == "" {
			continue
		}
		meta, pathPart, ok := strings.Cut(entry, "\t")
		if ok {
			baseEntries[pathPart] = meta + "\t" + pathPart
		}
	}

	var updateInfo strings.Builder
	var removePaths []string
	for _, p := range mirrorPaths {
		if line, ok := baseEntries[p]; ok {
			updateInfo.WriteString(line)
			updateInfo.WriteByte('\n')
		} else {
			removePaths = append(removePaths, p)
		}
	}
	if updateInfo.Len() > 0 {
		if _, err := runIdx(updateInfo.String(), "update-index", "--index-info"); err != nil {
			return "", err
		}
	}
	if len(removePaths) > 0 {
		rmArgs := append([]string{"update-index", "--force-remove", "--"}, removePaths...)
		if _, err := runIdx("", rmArgs...); err != nil {
			return "", err
		}
	}

	newTree, err := runIdx("", "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(newTree), nil
}
