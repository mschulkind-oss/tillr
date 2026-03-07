// Package vcs provides VCS (git/jj) integration utilities for reading
// commit history, branch information, and detecting the active VCS.
package vcs

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CommitInfo represents a parsed VCS commit.
type CommitInfo struct {
	Hash    string `json:"hash"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Branch  string `json:"branch,omitempty"`
}

// BranchInfo represents a parsed VCS branch.
type BranchInfo struct {
	Name      string `json:"name"`
	IsCurrent bool   `json:"is_current"`
	Commit    string `json:"commit,omitempty"`
	FeatureID string `json:"feature_id,omitempty"`
}

// DetectVCS checks for .jj or .git directories and returns the VCS type.
func DetectVCS() string {
	if _, err := os.Stat(".jj"); err == nil {
		return "jj"
	}
	if _, err := os.Stat(".git"); err == nil {
		return "git"
	}
	return ""
}

// GetLog returns recent commits from the detected VCS.
func GetLog(limit int) (vcsType string, commits []CommitInfo, err error) {
	vcsType = DetectVCS()
	if vcsType == "" {
		return "", nil, nil
	}
	switch vcsType {
	case "jj":
		commits, err = getJJLog(limit)
	default:
		commits, err = getGitLog(limit)
	}
	return vcsType, commits, err
}

// GetBranches returns branches/bookmarks from the detected VCS.
func GetBranches() (vcsType string, branches []BranchInfo, err error) {
	vcsType = DetectVCS()
	if vcsType == "" {
		return "", nil, nil
	}
	switch vcsType {
	case "jj":
		branches, err = getJJBranches()
	default:
		branches, err = getGitBranches()
	}
	return vcsType, branches, err
}

func getGitLog(limit int) ([]CommitInfo, error) {
	out, err := exec.Command("git", "--no-pager", "log", "--oneline",
		fmt.Sprintf("-%d", limit),
		"--format=%H|%s|%an|%ai").Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	var commits []CommitInfo
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) == 4 {
			commits = append(commits, CommitInfo{
				Hash:    parts[0],
				Message: parts[1],
				Author:  parts[2],
				Date:    parts[3],
			})
		}
	}
	return commits, nil
}

func getJJLog(limit int) ([]CommitInfo, error) {
	tmpl := `change_id.short() ++ "|" ++ description.first_line() ++ "|" ++ author.name() ++ "|" ++ committer.timestamp().local().format("%Y-%m-%d %H:%M") ++ "\n"`
	out, err := exec.Command("jj", "log", "--no-pager", "-n", fmt.Sprintf("%d", limit),
		"-T", tmpl).Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	var commits []CommitInfo
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		// jj log prefixes lines with graph markers like ◆ or ○ — strip them
		for len(line) > 0 && line[0] != '|' && !isAlphaNum(line[0]) {
			_, size := firstRune(line)
			line = line[size:]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 2 {
			continue
		}
		c := CommitInfo{
			Hash:    strings.TrimSpace(parts[0]),
			Message: strings.TrimSpace(parts[1]),
		}
		if len(parts) > 2 {
			c.Author = strings.TrimSpace(parts[2])
		}
		if len(parts) > 3 {
			c.Date = strings.TrimSpace(parts[3])
		}
		if c.Hash != "" {
			commits = append(commits, c)
		}
	}
	return commits, nil
}

func getGitBranches() ([]BranchInfo, error) {
	out, err := exec.Command("git", "--no-pager", "branch", "-v", "--no-color").Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	var branches []BranchInfo
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			continue
		}
		isCurrent := strings.HasPrefix(line, "* ")
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimPrefix(line, "  ")
		line = strings.TrimSpace(line)

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		branches = append(branches, BranchInfo{
			Name:      fields[0],
			IsCurrent: isCurrent,
			Commit:    fields[1],
		})
	}
	return branches, nil
}

func getJJBranches() ([]BranchInfo, error) {
	out, err := exec.Command("jj", "bookmark", "list", "--all").Output()
	if err != nil {
		// Older jj versions use "branch" instead of "bookmark"
		out, err = exec.Command("jj", "branch", "list", "--all").Output()
		if err != nil {
			return nil, err
		}
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	var branches []BranchInfo
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// jj bookmark list format: "name: commit_id description"
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			branches = append(branches, BranchInfo{Name: line})
			continue
		}
		name := strings.TrimSpace(line[:colonIdx])
		rest := strings.TrimSpace(line[colonIdx+1:])
		commit := ""
		if fields := strings.Fields(rest); len(fields) > 0 {
			commit = fields[0]
		}
		branches = append(branches, BranchInfo{
			Name:   name,
			Commit: commit,
		})
	}
	return branches, nil
}

func isAlphaNum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func firstRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}
