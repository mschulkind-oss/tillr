package watch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

var errMissingRef = errors.New("ref not found")

type refPair struct{ leader, follower string }

type oidPair struct{ leader, follower string }

type fileStamp struct {
	exists  bool
	modTime time.Time
	size    int64
}

type greenProofFact struct {
	oid      string
	provedAt time.Time
	passed   []string
	recorded bool
}

type greenProofCacheEntry struct {
	path  string
	stamp fileStamp
	fact  greenProofFact
	err   error
}

type gitCache struct {
	root         string
	commonDir    string
	commonDirOK  bool
	peeled       map[string]string
	lags         map[oidPair]lagFact
	packedStat   fileStamp
	packedRefs   map[string]string
	proofs       map[string]greenProofCacheEntry
	shortCommits map[string]projectionFact[string]
	ancestors    map[oidPair]bool
}

func (c *gitCache) reset(root string) {
	c.root = root
	c.commonDir, c.commonDirOK = resolveCommonDir(root)
	c.peeled = map[string]string{}
	c.lags = map[oidPair]lagFact{}
	c.packedStat = fileStamp{}
	c.packedRefs = nil
	c.proofs = map[string]greenProofCacheEntry{}
	c.shortCommits = map[string]projectionFact[string]{}
	c.ancestors = map[oidPair]bool{}
}

func resolveCommonDir(root string) (string, bool) {
	if root == "" {
		return "", false
	}
	dotGit := filepath.Join(root, ".git")
	var gitDir string
	fi, err := os.Lstat(dotGit)
	switch {
	case err == nil && fi.IsDir():
		gitDir = dotGit
	case err == nil && fi.Mode().IsRegular():
		data, err := os.ReadFile(dotGit)
		if err != nil {
			return "", false
		}
		target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
		if !ok {
			return "", false
		}
		target = strings.TrimSpace(target)
		if !filepath.IsAbs(target) {
			target = filepath.Join(root, target)
		}
		gitDir = filepath.Clean(target)
	case os.IsNotExist(err):
		if headInfo, err := os.Lstat(filepath.Join(root, "HEAD")); err == nil && headInfo.Mode().IsRegular() {
			gitDir = root
		} else {
			return "", false
		}
	default:
		return "", false
	}
	if _, err := os.Lstat(filepath.Join(gitDir, "reftable")); err == nil {
		return "", false
	}
	commonDir := gitDir
	if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		target := strings.TrimSpace(string(data))
		if target != "" {
			if !filepath.IsAbs(target) {
				target = filepath.Join(gitDir, target)
			}
			commonDir = filepath.Clean(target)
		}
	}
	if _, err := os.Lstat(filepath.Join(commonDir, "reftable")); err == nil {
		return "", false
	}
	return commonDir, true
}

func isHexOID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func isSharedRef(ref string) bool {
	if !strings.HasPrefix(ref, "refs/") || strings.Contains(ref, "..") || strings.Contains(ref, "\\") {
		return false
	}
	for _, prefix := range []string{"refs/bisect/", "refs/worktree/", "refs/rewritten/"} {
		if strings.HasPrefix(ref, prefix) {
			return false
		}
	}
	return true
}

func parsePackedRefs(data []byte) map[string]string {
	refs := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		oid, ref, ok := strings.Cut(line, " ")
		if ok && isHexOID(oid) && ref != "" {
			refs[ref] = oid
		}
	}
	return refs
}

type refLookupStatus uint8

const (
	refLookupFallback refLookupStatus = iota
	refLookupFound
	refLookupMissing
)

func (c *gitCache) readRefOID(ref string) (string, refLookupStatus) {
	if !c.commonDirOK || !isSharedRef(ref) {
		return "", refLookupFallback
	}
	data, err := os.ReadFile(filepath.Join(c.commonDir, filepath.FromSlash(ref)))
	if err == nil {
		oid := strings.TrimSpace(string(data))
		if isHexOID(oid) {
			return oid, refLookupFound
		}
		return "", refLookupFallback
	}
	if !os.IsNotExist(err) {
		return "", refLookupFallback
	}
	packedPath := filepath.Join(c.commonDir, "packed-refs")
	st, err := os.Stat(packedPath)
	var stamp fileStamp
	if err == nil {
		stamp = fileStamp{exists: true, modTime: st.ModTime(), size: st.Size()}
	} else if !os.IsNotExist(err) {
		return "", refLookupFallback
	}
	if stamp != c.packedStat {
		c.packedStat = stamp
		if stamp.exists {
			raw, err := os.ReadFile(packedPath)
			if err != nil {
				c.packedStat = fileStamp{}
				c.packedRefs = nil
				return "", refLookupFallback
			}
			c.packedRefs = parsePackedRefs(raw)
		} else {
			c.packedRefs = nil
		}
	}
	oid, ok := c.packedRefs[ref]
	if ok {
		return oid, refLookupFound
	}
	return "", refLookupMissing
}

func (c *gitCache) commit(ctx context.Context, root, ref string, checkIO func(string) error, onExec func([]string)) (string, error) {
	if checkIO != nil {
		if err := checkIO("watch Git projection"); err != nil {
			return "", err
		}
	}
	switch rawOID, status := c.readRefOID(ref); status {
	case refLookupFound:
		if peeled, found := c.peeled[rawOID]; found {
			return peeled, nil
		}
		raw, err := gitOutputWith(ctx, root, checkIO, onExec, "rev-parse", "--verify", "--end-of-options", rawOID+"^{commit}")
		if err != nil {
			return "", fmt.Errorf("git ref %s: %w", ref, err)
		}
		peeled := strings.TrimSpace(string(raw))
		c.peeled[rawOID] = peeled
		return peeled, nil
	case refLookupMissing:
		return "", fmt.Errorf("git ref %s: %w", ref, errMissingRef)
	default:
		return gitCommitWith(ctx, root, ref, checkIO, onExec)
	}
}

func (c *gitCache) resolveCommit(ctx context.Context, root, rev string, checkIO func(string) error, onExec func([]string)) (string, error) {
	if checkIO != nil {
		if err := checkIO("watch Git projection"); err != nil {
			return "", err
		}
	}
	if c != nil {
		if fact, ok := c.shortCommits[rev]; ok {
			return fact.value, fact.err
		}
	}
	raw, err := gitOutputWith(ctx, root, checkIO, onExec, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	if err != nil {
		var exitErr *exec.ExitError
		if c != nil && errors.As(err, &exitErr) {
			c.shortCommits[rev] = projectionFact[string]{err: err}
		}
		return "", err
	}
	oid := strings.TrimSpace(string(raw))
	if c != nil {
		c.shortCommits[rev] = projectionFact[string]{value: oid}
	}
	return oid, nil
}

func (c *gitCache) isAncestor(ctx context.Context, root, ancestorOID, descendantOID string, checkIO func(string) error, onExec func([]string)) (bool, error) {
	if checkIO != nil {
		if err := checkIO("watch Git projection"); err != nil {
			return false, err
		}
	}
	if ancestorOID == descendantOID {
		return true, nil
	}
	key := oidPair{leader: descendantOID, follower: ancestorOID}
	if c != nil {
		if okVal, found := c.ancestors[key]; found {
			return okVal, nil
		}
	}
	_, err := gitOutputWith(ctx, root, checkIO, onExec, "merge-base", "--is-ancestor", ancestorOID, descendantOID)
	if err == nil {
		if c != nil {
			c.ancestors[key] = true
		}
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		if c != nil {
			c.ancestors[key] = false
		}
		return false, nil
	}
	return false, err
}

func (c *gitCache) fixerAncestorOfGreen(ctx context.Context, root, shortSHA, greenOID string, checkIO func(string) error, onExec func([]string)) (bool, error) {
	redOID, err := c.resolveCommit(ctx, root, shortSHA, checkIO, onExec)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return true, nil
		}
		return false, err
	}
	var mainOID string
	if c != nil {
		mainOID, err = c.commit(ctx, root, "refs/heads/main", checkIO, onExec)
	} else {
		mainOID, err = gitCommitWith(ctx, root, "refs/heads/main", checkIO, onExec)
	}
	if err != nil {
		if errors.Is(err, errMissingRef) {
			return true, nil
		}
		return false, err
	}
	onMain, err := c.isAncestor(ctx, root, redOID, mainOID, checkIO, onExec)
	if err != nil || !onMain {
		return !onMain, err
	}
	return c.isAncestor(ctx, root, redOID, greenOID, checkIO, onExec)
}

func (c *gitCache) readGreenProof(ctx context.Context, root, commonDir, oid string, checkIO func(string) error, onExec func([]string)) (greenProofFact, error) {
	if checkIO != nil {
		if err := checkIO("watch Git projection"); err != nil {
			return greenProofFact{}, err
		}
	}
	gateDir := filepath.Join(commonDir, "tillr-gate")
	primaryPath := filepath.Join(gateDir, "green-"+oid+".json")
	st, err := os.Stat(primaryPath)
	if err == nil {
		stamp := fileStamp{exists: true, modTime: st.ModTime(), size: st.Size()}
		if c != nil {
			if entry, ok := c.proofs[oid]; ok && entry.path == primaryPath && entry.stamp == stamp {
				return entry.fact, entry.err
			}
		}
		fact, parseErr := parseGreenProofFile(primaryPath, oid, true)
		if c != nil {
			c.proofs[oid] = greenProofCacheEntry{path: primaryPath, stamp: stamp, fact: fact, err: parseErr}
		}
		return fact, parseErr
	}
	if !os.IsNotExist(err) {
		return greenProofFact{}, unreadableProjection{"green_proof", err}
	}

	fallbackPath := filepath.Join(gateDir, "green.json")
	st, err = os.Stat(fallbackPath)
	if os.IsNotExist(err) {
		return greenProofFact{recorded: false}, nil
	}
	if err != nil {
		return greenProofFact{}, unreadableProjection{"green_proof", err}
	}
	stamp := fileStamp{exists: true, modTime: st.ModTime(), size: st.Size()}
	if c != nil {
		if entry, ok := c.proofs[oid]; ok && entry.path == fallbackPath && entry.stamp == stamp {
			return entry.fact, entry.err
		}
	}
	fact, parseErr := parseGreenProofFile(fallbackPath, oid, false)
	if c != nil {
		c.proofs[oid] = greenProofCacheEntry{path: fallbackPath, stamp: stamp, fact: fact, err: parseErr}
	}
	return fact, parseErr
}

func parseGreenProofFile(path, oid string, strictCommit bool) (greenProofFact, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return greenProofFact{}, unreadableProjection{"green_proof", err}
	}
	var payload struct {
		Commit   string   `json:"commit"`
		ProvedAt string   `json:"proved_at"`
		Passed   []string `json:"passed"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return greenProofFact{}, unreadableProjection{"green_proof", err}
	}
	if payload.Commit != oid {
		if strictCommit {
			return greenProofFact{}, unreadableProjection{"green_proof", fmt.Errorf("commit %q does not match %s", payload.Commit, oid)}
		}
		return greenProofFact{recorded: false}, nil
	}
	var provedAt time.Time
	if payload.ProvedAt != "" {
		var err error
		provedAt, err = time.Parse(time.RFC3339, payload.ProvedAt)
		if err != nil {
			return greenProofFact{}, unreadableProjection{"green_proof", fmt.Errorf("invalid proved_at %q: %w", payload.ProvedAt, err)}
		}
	}
	return greenProofFact{
		oid:      oid,
		provedAt: provedAt,
		passed:   payload.Passed,
		recorded: true,
	}, nil
}

func (c *gitCache) lag(ctx context.Context, root, leader, follower string, checkIO func(string) error, onExec func([]string)) (lagFact, error) {
	if leader == follower {
		return lagFact{}, nil
	}
	key := oidPair{leader, follower}
	if fact, ok := c.lags[key]; ok {
		return fact, nil
	}
	fact, err := gitLagWith(ctx, root, leader, follower, checkIO, onExec)
	if err == nil {
		c.lags[key] = fact
	}
	return fact, err
}

type lagFact struct {
	count  int64
	oldest time.Time
}

type gitSnapshot struct {
	refs        map[string]projectionFact[string]
	pairs       map[refPair]projectionFact[lagFact]
	greenProofs map[string]projectionFact[greenProofFact]
}

func (r Rule) gitRefs() []string {
	var refs []string
	bareGitRefs := false
	for _, entry := range r.Reads.Projections {
		if entry == "git_refs" {
			bareGitRefs = true
		} else if names, ok := strings.CutPrefix(entry, "git_refs:"); ok {
			for ref := range strings.SplitSeq(names, ",") {
				if !slices.Contains(refs, ref) {
					refs = append(refs, ref)
				}
			}
		}
	}
	if bareGitRefs {
		for _, expr := range r.expressions() {
			if expr == nil {
				continue
			}
			for _, op := range expr.projections {
				if op.name == "git.ref" && op.ref != "" && !slices.Contains(refs, op.ref) {
					refs = append(refs, op.ref)
				}
			}
		}
	}
	if r.When.Pattern == LagBehind {
		for _, ref := range []string{r.When.Leader, r.When.Follower} {
			if ref != "" && !slices.Contains(refs, ref) {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

func (r Rule) greenProofRefs() []string {
	var refs []string
	for _, entry := range r.Reads.Projections {
		if ref, ok := strings.CutPrefix(entry, "green_proof:"); ok && ref != "" && !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	return refs
}

func (r Rule) refs() []string {
	refs := r.gitRefs()
	for _, ref := range r.greenProofRefs() {
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	return refs
}

func gitRequirements(rules []Rule) (refs []string, pairs []refPair, proofRefs []string) {
	for _, r := range rules {
		if !r.Enabled || r.evaluatorReason() != "" {
			continue
		}
		refs = append(refs, r.refs()...)
		proofRefs = append(proofRefs, r.greenProofRefs()...)
		if r.When.Pattern == LagBehind {
			pairs = append(pairs, refPair{r.When.Leader, r.When.Follower})
		}
	}
	return refs, pairs, proofRefs
}

// covers checks presence, not readability: a failed Git read is still a fact
// for this pass and must not cause another evaluation attempt.
func (g gitSnapshot) covers(rules []Rule) bool {
	refs, pairs, proofRefs := gitRequirements(rules)
	for _, ref := range refs {
		if _, ok := g.refs[ref]; !ok {
			return false
		}
	}
	for _, pair := range pairs {
		if _, ok := g.pairs[pair]; !ok {
			return false
		}
	}
	for _, ref := range proofRefs {
		if _, ok := g.greenProofs[ref]; !ok {
			return false
		}
	}
	return true
}

func readGit(ctx context.Context, root string, rules []Rule, cache *gitCache, checkIO func(string) error, onExec func([]string)) (gitSnapshot, error) {
	g := gitSnapshot{
		refs:        map[string]projectionFact[string]{},
		pairs:       map[refPair]projectionFact[lagFact]{},
		greenProofs: map[string]projectionFact[greenProofFact]{},
	}
	refs, pairs, proofRefs := gitRequirements(rules)
	if cache == nil && (len(refs) > 0 || len(pairs) > 0 || len(proofRefs) > 0) {
		cache = &gitCache{}
	}
	if cache != nil && (len(refs) > 0 || len(pairs) > 0 || len(proofRefs) > 0) && (cache.root != root || cache.peeled == nil) {
		cache.reset(root)
	}
	for _, ref := range refs {
		if _, ok := g.refs[ref]; ok {
			continue
		}
		oid, err := cache.commit(ctx, root, ref, checkIO, onExec)
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
		lag, err := cache.lag(ctx, root, leader.value, follower.value, checkIO, onExec)
		if err != nil {
			err = fmt.Errorf("git lag %s behind %s: %w", pair.follower, pair.leader, err)
		}
		g.pairs[pair] = projectionFact[lagFact]{lag, err}
	}
	if len(proofRefs) > 0 {
		commonDir, commonErr := cache.commonDir, error(nil)
		if !cache.commonDirOK {
			raw, err := gitOutputWith(ctx, root, checkIO, onExec, "rev-parse", "--git-common-dir")
			if err != nil {
				commonErr = unreadableProjection{"green_proof", err}
			} else {
				commonDir = strings.TrimSpace(string(raw))
				if !filepath.IsAbs(commonDir) {
					commonDir = filepath.Join(root, commonDir)
				}
				commonDir = filepath.Clean(commonDir)
			}
		}
		for _, ref := range proofRefs {
			if _, ok := g.greenProofs[ref]; ok {
				continue
			}
			if commonErr != nil {
				g.greenProofs[ref] = projectionFact[greenProofFact]{err: commonErr}
				continue
			}
			refFact := g.refs[ref]
			if refFact.err != nil {
				if errors.Is(refFact.err, errMissingRef) {
					g.greenProofs[ref] = projectionFact[greenProofFact]{value: greenProofFact{recorded: false}}
				} else {
					g.greenProofs[ref] = projectionFact[greenProofFact]{err: unreadableProjection{"green_proof", refFact.err}}
				}
				continue
			}
			fact, err := cache.readGreenProof(ctx, root, commonDir, refFact.value, checkIO, onExec)
			g.greenProofs[ref] = projectionFact[greenProofFact]{value: fact, err: err}
		}
	}
	return g, ctx.Err()
}

func gitOutput(ctx context.Context, root string, args ...string) ([]byte, error) {
	return gitOutputWith(ctx, root, nil, nil, args...)
}

func gitOutputWith(ctx context.Context, root string, checkIO func(string) error, onExec func([]string), args ...string) ([]byte, error) {
	if checkIO != nil {
		if err := checkIO("watch Git projection"); err != nil {
			return nil, err
		}
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
	if onExec != nil {
		onExec(args)
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
	return gitCommitWith(ctx, root, ref, nil, nil)
}

func gitCommitWith(ctx context.Context, root, ref string, checkIO func(string) error, onExec func([]string)) (string, error) {
	// Resolve an exact ref, not Git's extended revision syntax. Peel by OID so
	// any concurrent move cannot change this fact part way through the read.
	raw, err := gitOutputWith(ctx, root, checkIO, onExec, "show-ref", "--hash", "--verify", "--", ref)
	if err == nil {
		raw, err = gitOutputWith(ctx, root, checkIO, onExec, "rev-parse", "--verify", "--end-of-options", strings.TrimSpace(string(raw))+"^{commit}")
	} else {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(exitErr.Stderr) == 0 {
			return "", fmt.Errorf("git ref %s: %w: %w", ref, errMissingRef, err)
		}
	}
	if err != nil {
		return "", fmt.Errorf("git ref %s: %w", ref, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func gitLagWith(ctx context.Context, root, leader, follower string, checkIO func(string) error, onExec func([]string)) (lagFact, error) {
	if leader == follower {
		return lagFact{}, nil
	}
	raw, err := gitOutputWith(ctx, root, checkIO, onExec, "rev-list", "--timestamp", follower+".."+leader, "--")
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
	reopenEvent            *observation
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
	for _, ref := range r.gitRefs() {
		if fact := g.refs[ref]; fact.err != nil {
			return fact.err
		}
	}
	for _, ref := range r.greenProofRefs() {
		if fact := g.greenProofs[ref]; fact.err != nil {
			return fact.err
		}
	}
	return nil
}
