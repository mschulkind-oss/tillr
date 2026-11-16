package main

import (
	"fmt"
	"strings"
)

// A ref need is spelled ref:<full local ref or single-* fetch pattern>. Bare paths retain the
// legacy registry's checkout/error contract; ref needs describe optional resources.
// Each missing ref is reported with the fetch that creates it in this clone.
type MissingNeed struct {
	Need    string `json:"need"`
	Command string `json:"command"`
}

type refInput struct {
	Ref    string
	Commit string
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func resolveRefNeeds(check PlanCheck, root string) ([]refInput, []MissingNeed, error) {
	refs := []refInput{}
	missing := []MissingNeed{}
	for _, need := range check.Needs {
		pattern, ok := strings.CutPrefix(need, "ref:")
		if !ok {
			continue
		}
		if !strings.HasPrefix(pattern, "refs/") {
			return nil, nil, fmt.Errorf("invalid ref need %q: use ref:refs/<name or single-* pattern>", need)
		}
		// Use Git's fetchable ref-pattern grammar, not arbitrary glob syntax:
		// the reported refspec must actually be able to provide this need.
		if _, err := memoryGitOutput(root, "check-ref-format", "--refspec-pattern", pattern); err != nil {
			return nil, nil, fmt.Errorf("invalid ref need %q: %w", need, err)
		}
		raw, err := memoryGitOutput(root, "for-each-ref", "--format=%(refname)", "--sort=refname", "--", pattern)
		if err != nil {
			return nil, nil, err
		}
		names := strings.Fields(string(raw))
		// for-each-ref treats an exact name as a directory prefix too. Only an
		// explicit wildcard may match other names.
		if !strings.Contains(pattern, "*") {
			names = nil
			for _, name := range strings.Fields(string(raw)) {
				if name == pattern {
					names = append(names, name)
				}
			}
		}
		if len(names) == 0 {
			missing = append(missing, MissingNeed{Need: need, Command: "git fetch origin " + shellQuote(pattern+":"+pattern)})
			continue
		}
		for _, name := range names {
			commit, err := memoryGitOutput(root, "rev-parse", "--verify", name+"^{commit}")
			if err != nil {
				return nil, nil, err
			}
			refs = append(refs, refInput{Ref: name, Commit: strings.TrimSpace(string(commit))})
		}
	}
	return refs, missing, nil
}
