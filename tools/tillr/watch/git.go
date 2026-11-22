package watch

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

type refPair struct{ leader, follower string }

type lagFact struct {
	count  int64
	oldest time.Time
}

type gitSnapshot struct {
	refs  map[string]projectionFact[string]
	pairs map[refPair]projectionFact[lagFact]
}

func (r Rule) refs() []string {
	var refs []string
	for _, entry := range r.Reads.Projections {
		if names, ok := strings.CutPrefix(entry, "git_refs:"); ok {
			refs = append(refs, strings.Split(names, ",")...)
		}
	}
	return refs
}

func gitRequirements(rules []Rule) (refs []string, pairs []refPair) {
	for _, r := range rules {
		if !r.Enabled || r.evaluatorReason() != "" {
			continue
		}
		refs = append(refs, r.refs()...)
		if r.When.Pattern == LagBehind {
			pairs = append(pairs, refPair{r.When.Leader, r.When.Follower})
		}
	}
	return refs, pairs
}

func readGit(ctx context.Context, root string, rules []Rule) (gitSnapshot, error) {
	g := gitSnapshot{refs: map[string]projectionFact[string]{}, pairs: map[refPair]projectionFact[lagFact]{}}
	refs, pairs := gitRequirements(rules)
	for _, ref := range refs {
		if _, ok := g.refs[ref]; ok {
			continue
		}
		oid, err := gitCommit(ctx, root, ref)
		g.refs[ref] = projectionFact[string]{oid, err}
	}
	for _, pair := range pairs {
		if _, ok := g.pairs[pair]; ok {
			continue
		}
		leader, follower := g.refs[pair.leader], g.refs[pair.follower]
		if leader.err != nil {
			g.pairs[pair] = projectionFact[lagFact]{err: leader.err}
			continue
		}
		if follower.err != nil {
			g.pairs[pair] = projectionFact[lagFact]{err: follower.err}
			continue
		}
		lag, err := gitLag(ctx, root, leader.value, follower.value)
		if err != nil {
			err = fmt.Errorf("git lag %s behind %s: %w", pair.follower, pair.leader, err)
		}
		g.pairs[pair] = projectionFact[lagFact]{lag, err}
	}
	return g, ctx.Err()
}

func gitOutput(ctx context.Context, root string, args ...string) ([]byte, error) {
	if err := store.CheckIO("watch Git projection"); err != nil {
		return nil, err
	}
	if root == "" {
		return nil, fmt.Errorf("repository root is required")
	}
	// Pin the Git directory, not just the working directory: -C alone
	// would discover an enclosing repository for an ordinary child path.
	// Git handles both a .git directory and a linked worktree's .git file.
	gitDir := ".git"
	if _, err := os.Lstat(filepath.Join(root, gitDir)); os.IsNotExist(err) {
		gitDir = "." // A bare repository stores its metadata at the root.
	} else if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root, "--git-dir=" + gitDir}, args...)...)
	cmd.Env = gitProjectionEnv(cmd.Environ())
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok && len(exit.Stderr) != 0 {
		// Diagnostics are one line even when Git suggests follow-up commands.
		detail := strings.Join(strings.Fields(string(exit.Stderr)), " ")
		err = fmt.Errorf("%w: %s", err, detail)
	}
	return out, err
}

// An explicit Git directory does not isolate all repository-local settings
// (git rev-parse --local-env-vars). Ignore the caller's Git context.
func gitProjectionEnv(env []string) []string {
	out := env[:0]
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_NAMESPACE",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_INDEX_FILE", "GIT_GRAFT_FILE",
			"GIT_REPLACE_REF_BASE", "GIT_NO_REPLACE_OBJECTS", "GIT_SHALLOW_FILE", "GIT_PREFIX",
			"GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
			"GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM":
			continue
		}
		out = append(out, entry)
	}
	return out
}

func gitCommit(ctx context.Context, root, ref string) (string, error) {
	// Resolve an exact ref, not Git's extended revision syntax. Peel by OID so
	// any concurrent move cannot change this fact part way through the read.
	raw, err := gitOutput(ctx, root, "show-ref", "--hash", "--verify", "--", ref)
	if err == nil {
		raw, err = gitOutput(ctx, root, "rev-parse", "--verify", "--end-of-options", strings.TrimSpace(string(raw))+"^{commit}")
	}
	if err != nil {
		return "", fmt.Errorf("git ref %s: %w", ref, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func gitLag(ctx context.Context, root, leader, follower string) (lagFact, error) {
	if leader == follower {
		return lagFact{}, nil
	}
	raw, err := gitOutput(ctx, root, "rev-list", "--timestamp", follower+".."+leader, "--")
	if err != nil {
		return lagFact{}, err
	}
	var lag lagFact
	// rev-list emits one timestamp and object name per line, never a commit body.
	lines := bufio.NewScanner(bytes.NewReader(raw))
	for lines.Scan() {
		timestamp, _, _ := strings.Cut(lines.Text(), " ")
		secs, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			return lagFact{}, err
		}
		at := time.Unix(secs, 0)
		if lag.count == 0 || at.Before(lag.oldest) {
			lag.oldest = at
		}
		lag.count++
	}
	return lag, lines.Err()
}

type lagCondition struct {
	count                  int64
	elapsed                time.Duration
	leaderOID, followerOID string
}

func deriveLag(r Rule, g gitSnapshot, now time.Time) (*condition, error) {
	w := r.When
	fact := g.pairs[refPair{w.Leader, w.Follower}]
	if fact.err != nil {
		return nil, fact.err
	}
	lag := &lagCondition{count: fact.value.count, leaderOID: g.refs[w.Leader].value, followerOID: g.refs[w.Follower].value}
	if lag.count != 0 {
		lag.elapsed = max(0, now.Sub(fact.value.oldest))
	}
	return &condition{lag: lag}, nil
}

func (l lagCondition) fires(w When) bool {
	if l.count == 0 {
		return false
	}
	commits := w.ThresholdCommits != 0 && l.count >= w.ThresholdCommits
	duration := w.ThresholdDuration != 0 && l.elapsed >= w.ThresholdDuration
	if w.Mode == "all" {
		return commits && duration
	}
	return commits || duration
}

func (g gitSnapshot) ruleError(r Rule) error {
	for _, ref := range r.refs() {
		if fact := g.refs[ref]; fact.err != nil {
			return fact.err
		}
	}
	return nil
}
