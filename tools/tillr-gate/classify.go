package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
)

var (
	classifyCheck   = checkresult.Classify
	aggregateChecks = checkresult.Aggregate

	legacyPidPattern     = regexp.MustCompile(`run-\d+`)
	legacyNonSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)
)

type classifyRunOutput struct {
	Outcome  checkresult.Outcome `json:"outcome"`
	Detail   string              `json:"detail"`
	ExitCode int                 `json:"exit_code"`
}

type classifyAggregateOutput struct {
	Checks []checkresult.Result `json:"checks"`
	Run    classifyRunOutput    `json:"run"`
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("value cannot be empty")
	}
	*f = append(*f, value)
	return nil
}

func parseAggregateOrder(raw string) (checkresult.AggregateOrder, bool) {
	switch checkresult.AggregateOrder(raw) {
	case checkresult.RunnerOrder, checkresult.ShadowOrder, checkresult.LegacyGateOrder, checkresult.LegacyReplayOrder:
		return checkresult.AggregateOrder(raw), true
	default:
		return "", false
	}
}

// verdictWord maps a classified check outcome back to the runner's verdict vocabulary.
func verdictWord(res checkresult.Result) string {
	switch res.Outcome {
	case checkresult.Pass:
		return ResultPass
	case checkresult.Fail:
		return ResultFail
	case checkresult.Unavailable:
		if res.Detail == checkresult.DetailSkipped {
			return ResultSkipped
		}
		return ResultUnavailable
	case checkresult.Disabled:
		return ResultDisabled
	default:
		if res.Detail == checkresult.DetailStalled {
			return ResultStalled
		}
		if res.Detail == checkresult.DetailOverBudget {
			return ResultOverBudget
		}
		return ResultError
	}
}

func legacyFastSpanID(id, label, root string) string {
	text := label
	if root != "" {
		text = strings.ReplaceAll(text, root, "{root}")
		if clean := filepath.Clean(root); clean != root {
			text = strings.ReplaceAll(text, clean, "{root}")
		}
	}
	text = legacyPidPattern.ReplaceAllString(text, "run-<pid>")
	slug := strings.Trim(legacyNonSlugPattern.ReplaceAllString(strings.ToLower(text), "-"), "-")
	if slug == "" {
		return id
	}
	return id + "/" + slug
}

func resolveCheckDeclaration(decls []Declaration, selector, root string) (*Declaration, error) {
	if selector == "" {
		return nil, nil
	}
	var matches []Declaration
	for _, decl := range decls {
		matched := selector == decl.ID ||
			(decl.Label != "" && selector == decl.ID+":"+decl.Label) ||
			(decl.Tier == "fast" && decl.Label != "" && selector == legacyFastSpanID(decl.ID, decl.Label, root))
		if matched {
			matches = append(matches, decl)
		}
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous check selector %q matches %d declarations", selector, len(matches))
	}
	if len(matches) == 0 {
		return nil, nil
	}
	resolved := matches[0]
	return &resolved, nil
}

func planDeclarationForStore(decl Declaration) PlanCheck {
	if decl.Scope == "tree" {
		switch {
		case decl.Tier == "heavy":
			decl.Forced = "heavy-tree"
		case decl.Forced == "":
			decl.Forced = "fast-tree"
		}
	}
	planned := (&Registry{Declarations: []Declaration{decl}}).Plan(nil, "")
	if len(planned) == 0 {
		return PlanCheck{CheckID: decl.ID}
	}
	c := planned[0]
	return PlanCheck{
		DeclarationDigest: c.DeclarationDigest,
		Scope:             c.Scope,
		BudgetMS:          c.BudgetMS,
		Tools:             c.Tools,
		Remember:          c.Remember,
		CheckID:           c.ID,
		Command:           c.Command,
		TimeoutMS:         c.TimeoutMS,
		Reads:             c.Reads,
		Writes:            c.Writes,
		DependsOn:         c.DependsOn,
		Kind:              c.Kind,
		Label:             c.Label,
		Env:               c.Env,
		Drop:              c.Drop,
		Needs:             c.Needs,
		Tier:              c.Tier,
		Exclusive:         c.Exclusive,
		ExclusiveReason:   c.ExclusiveReason,
		Forced:            c.Forced,
	}
}

type loadedCheckSet struct {
	root         string
	treeSHA      string
	inputs       []treeInput
	declarations []Declaration
	fingerprints map[string]string
}

type classifyRepoLoader struct {
	repo     string
	byCommit map[string]*loadedCheckSet
	byWT     map[string]*loadedCheckSet
}

func newClassifyRepoLoader(repo string) (*classifyRepoLoader, error) {
	absRepo, err := filepath.Abs(repo)
	if err != nil {
		return nil, err
	}
	return &classifyRepoLoader{
		repo:     absRepo,
		byCommit: map[string]*loadedCheckSet{},
		byWT:     map[string]*loadedCheckSet{},
	}, nil
}

func (loader *classifyRepoLoader) commitSet(rev string) (*loadedCheckSet, error) {
	if rev == "" {
		rev = "HEAD"
	}
	if cached, ok := loader.byCommit[rev]; ok {
		return cached, nil
	}
	treeRaw, err := memoryGitOutput(loader.repo, "rev-parse", rev+"^{tree}")
	if err != nil {
		return nil, err
	}
	treeSHA := strings.TrimSpace(string(treeRaw))
	if cached, ok := loader.byCommit["tree:"+treeSHA]; ok {
		loader.byCommit[rev] = cached
		return cached, nil
	}
	inputs, err := readTreeInputs(loader.repo, treeSHA)
	if err != nil {
		return nil, err
	}
	checksDir := settings.DefaultChecksDir
	for _, entry := range inputs {
		if filepath.ToSlash(entry.Path) == ".tillr.toml" {
			raw, err := memoryGitOutput(loader.repo, "cat-file", "blob", entry.Object)
			if err != nil {
				return nil, err
			}
			tmpDir, err := os.MkdirTemp("", "tillr-gate-settings-*")
			if err != nil {
				return nil, err
			}
			err = func() error {
				defer os.RemoveAll(tmpDir)
				if writeErr := os.WriteFile(filepath.Join(tmpDir, ".tillr.toml"), raw, 0600); writeErr != nil {
					return writeErr
				}
				s, readErr := settings.Read(tmpDir)
				if readErr != nil {
					return readErr
				}
				checksDir = s.ChecksDir()
				return nil
			}()
			if err != nil {
				return nil, err
			}
			break
		}
	}
	var declEntries []treeInput
	for _, entry := range inputs {
		slashPath := filepath.ToSlash(entry.Path)
		if filepath.ToSlash(filepath.Dir(slashPath)) == checksDir && strings.HasSuffix(slashPath, ".json") {
			declEntries = append(declEntries, entry)
		}
	}
	sort.Slice(declEntries, func(i, j int) bool {
		return declEntries[i].Path < declEntries[j].Path
	})
	decls := make([]Declaration, 0, len(declEntries))
	for _, entry := range declEntries {
		raw, err := memoryGitOutput(loader.repo, "cat-file", "blob", entry.Object)
		if err != nil {
			return nil, err
		}
		decl, err := parseCheckDeclaration(rev+":"+entry.Path, raw)
		if err != nil {
			return nil, err
		}
		decls = append(decls, decl)
	}
	set := &loadedCheckSet{
		root:         loader.repo,
		treeSHA:      treeSHA,
		inputs:       inputs,
		declarations: decls,
		fingerprints: map[string]string{},
	}
	loader.byCommit[rev] = set
	loader.byCommit["tree:"+treeSHA] = set
	return set, nil
}

func (loader *classifyRepoLoader) worktreeSet(rawWT, rev string) (*loadedCheckSet, error) {
	wtDir := loader.repo
	if rawWT != "" && rawWT != "true" && rawWT != "." {
		if filepath.IsAbs(rawWT) {
			wtDir = filepath.Clean(rawWT)
		} else {
			wtDir = filepath.Clean(filepath.Join(loader.repo, rawWT))
		}
	}
	if rev == "" {
		rev = "HEAD"
	}
	cacheKey := wtDir + "\x00" + rev
	if cached, ok := loader.byWT[cacheKey]; ok {
		return cached, nil
	}
	s, err := settings.Read(wtDir)
	if err != nil {
		return nil, err
	}
	checkDir := filepath.Join(wtDir, filepath.FromSlash(s.ChecksDir()))
	entries, err := os.ReadDir(checkDir)
	var decls []Declaration
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", checkDir, err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		fullPath := filepath.Join(checkDir, entry.Name())
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("check declaration %s: expected a regular file", fullPath)
		}
		raw, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			return nil, fmt.Errorf("reading %s: %w", fullPath, readErr)
		}
		decl, parseErr := parseCheckDeclaration(fullPath, raw)
		if parseErr != nil {
			return nil, parseErr
		}
		decls = append(decls, decl)
	}
	set := &loadedCheckSet{
		root:         wtDir,
		declarations: decls,
		fingerprints: map[string]string{},
	}
	if len(decls) > 0 {
		treeRaw, treeErr := memoryGitOutput(wtDir, "rev-parse", rev+"^{tree}")
		if treeErr != nil {
			return nil, treeErr
		}
		set.treeSHA = strings.TrimSpace(string(treeRaw))
		inputs, inputsErr := readTreeInputs(wtDir, set.treeSHA)
		if inputsErr != nil {
			return nil, inputsErr
		}
		set.inputs = inputs
	}
	loader.byWT[cacheKey] = set
	return set, nil
}

func (set *loadedCheckSet) fingerprintFor(decl Declaration) (string, error) {
	if decl.Scope != "tree" || (decl.Remember != nil && !*decl.Remember) {
		return "", nil
	}
	if fp, ok := set.fingerprints[decl.ID]; ok {
		return fp, nil
	}
	planned := planDeclarationForStore(decl)
	_, missing, err := resolveRefNeeds(planned, set.root)
	if err != nil {
		return "", err
	}
	if len(missing) > 0 {
		set.fingerprints[decl.ID] = ""
		return "", nil
	}
	if planned.CheckID == "go-build" {
		_, reason, idErr := goBuildIdentity(planned, set.root, set.inputs)
		if idErr != nil {
			return "", idErr
		}
		if reason != "" {
			set.fingerprints[decl.ID] = ""
			return "", nil
		}
	}
	fp, err := treeFingerprint(planned, set.inputs, set.root)
	if err != nil {
		return "", err
	}
	set.fingerprints[decl.ID] = fp
	return fp, nil
}

func classifyStoreObservation(
	obs checkresult.Observation,
	store resultStore,
	loader *classifyRepoLoader,
	proveSet map[string]bool,
	batchNow string,
) (checkresult.Result, error) {
	selector := obs.Check
	if selector == "" {
		selector = obs.CheckID
	}
	var canonicalID string
	if loader != nil {
		var (
			set *loadedCheckSet
			err error
		)
		if obs.WorktreeEnabled() {
			set, err = loader.worktreeSet(obs.Worktree, obs.Commit)
		} else {
			set, err = loader.commitSet(obs.Commit)
		}
		if err != nil {
			return checkresult.Result{}, err
		}
		decl, err := resolveCheckDeclaration(set.declarations, selector, set.root)
		if err != nil {
			return checkresult.Result{}, err
		}
		if decl == nil {
			if proveSet[selector] {
				obs.Proving = true
			}
			return classifyCheck(obs), nil
		}
		canonicalID = decl.ID
		obs.CheckID = canonicalID
		if obs.Fingerprint == "" {
			fp, fpErr := set.fingerprintFor(*decl)
			if fpErr != nil {
				return checkresult.Result{}, fpErr
			}
			obs.Fingerprint = fp
		}
	} else {
		canonicalID = obs.CheckID
		if canonicalID == "" {
			canonicalID = obs.Check
		}
		if canonicalID == "" {
			return classifyCheck(obs), nil
		}
		obs.CheckID = canonicalID
	}

	if proveSet[canonicalID] || proveSet[selector] {
		obs.Proving = true
	}
	if obs.ObservedAt != "" {
		canonical, err := canonicalUTCTime(obs.ObservedAt)
		if err != nil {
			return checkresult.Result{}, usagef("invalid observed_at %q: %v", obs.ObservedAt, err)
		}
		obs.ObservedAt = canonical
	}

	lock, err := store.lockCheck(canonicalID)
	if err != nil {
		return checkresult.Result{}, err
	}
	defer lock.Close()

	marker, err := store.readDisabled(canonicalID)
	if err != nil {
		return checkresult.Result{}, err
	}
	hadMarker := obs.Marker != nil || marker != nil
	if obs.Marker == nil {
		obs.Marker = marker
	}

	enabled, err := store.readEnabled(canonicalID)
	if err != nil {
		return checkresult.Result{}, err
	}
	if enabled != nil {
		for _, ep := range enabled.Episodes {
			if !slices.Contains(obs.ResolvedEpisodes, ep) {
				obs.ResolvedEpisodes = append(obs.ResolvedEpisodes, ep)
			}
		}
		if obs.EnabledAt == "" {
			obs.EnabledAt = enabled.EnabledAt
		}
	}
	if obs.ObservedAt == "" && !(obs.Source == checkresult.CachedRecord && hasResolvedFingerprint(obs.ResolvedEpisodes, obs.Fingerprint)) {
		obs.ObservedAt = batchNow
	}

	if obs.Fingerprint != "" {
		pass, fail, readErr := store.read(obs.Fingerprint)
		if readErr != nil {
			return checkresult.Result{}, readErr
		}
		if obs.StoredPass == nil && pass != nil {
			obs.StoredPass = &checkresult.StoredRecord{At: pass.Time, LogRef: pass.LogRef}
		}
		if obs.StoredFail == nil && fail != nil {
			obs.StoredFail = &checkresult.StoredRecord{At: fail.Time, LogRef: fail.LogRef}
		}
	}

	res := classifyCheck(obs)
	if !obs.Proving && !hadMarker && res.Outcome == checkresult.Disabled && res.Episode != "" {
		res, err = store.writeRule3Marker(canonicalID, res)
		if err != nil {
			return checkresult.Result{}, err
		}
	}
	return res, nil
}

// classifyFace reports check classifications, pure by default and store-backed
// when `--repo` or `--store` is supplied.
func classifyFace(args []string) int {
	flags := flag.NewFlagSet("classify", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	aggregate := flags.String("aggregate", "", "aggregate classified checks under `order`: runner, shadow, legacy-gate, or legacy-replay")
	repoDir := flags.String("repo", "", "repository or worktree whose committed declarations and result store back classification")
	storeDir := flags.String("store", "", "override the result store directory")
	var proveDisabled stringListFlag
	flags.Var(&proveDisabled, "prove-disabled", "check id being proved on its repair candidate (repeatable)")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: tillr-gate classify [--aggregate <order>] [--repo <dir>] [--store <dir>] [--prove-disabled <check-id>]\nRead a JSON array of check observations from stdin and write their classifications to stdout, in order.")
		flags.PrintDefaults()
	}
	if code, answered := parseFlags(flags, args); answered {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return record.ExitUsage
	}
	aggregateSet := false
	repoSet := false
	storeSet := false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "aggregate":
			aggregateSet = true
		case "repo":
			repoSet = true
		case "store":
			storeSet = true
		}
	})
	if (repoSet && strings.TrimSpace(*repoDir) == "") || (storeSet && strings.TrimSpace(*storeDir) == "") {
		fmt.Fprintln(os.Stderr, "tillr-gate classify: --repo and --store cannot be empty when specified")
		return record.ExitUsage
	}
	var order checkresult.AggregateOrder
	if aggregateSet {
		var ok bool
		order, ok = parseAggregateOrder(*aggregate)
		if !ok {
			fmt.Fprintf(os.Stderr, "tillr-gate classify: %q is not an aggregate order; name one of: runner, shadow, legacy-gate, legacy-replay\n", *aggregate)
			return record.ExitUsage
		}
	}
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	var batch []*checkresult.Observation
	if err := decoder.Decode(&batch); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate classify: reading observations: %v\n", err)
		return record.ExitUsage
	}
	if batch == nil || decoder.Decode(new(any)) != io.EOF {
		fmt.Fprintln(os.Stderr, "tillr-gate classify: expected exactly one JSON array")
		return record.ExitUsage
	}

	proveSet := make(map[string]bool, len(proveDisabled))
	for _, id := range proveDisabled {
		proveSet[id] = true
	}

	storeBacked := repoSet || storeSet
	var (
		store  resultStore
		loader *classifyRepoLoader
	)
	if storeBacked {
		if repoSet {
			var err error
			loader, err = newClassifyRepoLoader(*repoDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tillr-gate classify: %v\n", externalf("resolving --repo: %v", err))
				return record.ExitExternal
			}
		}
		if storeSet {
			absStore, err := filepath.Abs(*storeDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tillr-gate classify: %v\n", externalf("resolving --store: %v", err))
				return record.ExitExternal
			}
			store = resultStore{dir: absStore}
		} else {
			var err error
			store, err = openResultStore(loader.repo)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tillr-gate classify: %v\n", externalf("opening the result store: %v", err))
				return record.ExitExternal
			}
		}
	}

	batchNow := time.Now().UTC().Format(time.RFC3339Nano)
	answers := make([]checkresult.Result, len(batch))
	for i, observation := range batch {
		if observation == nil {
			fmt.Fprintf(os.Stderr, "tillr-gate classify: null observation at index %d\n", i)
			return record.ExitUsage
		}
		obs := *observation
		if !storeBacked {
			if proveSet[obs.Check] || proveSet[obs.CheckID] {
				obs.Proving = true
			}
			answers[i] = classifyCheck(obs)
			continue
		}
		res, err := classifyStoreObservation(obs, store, loader, proveSet, batchNow)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tillr-gate classify: %v\n", err)
			return exitCode(externalOrOwn(err))
		}
		answers[i] = res
	}
	var output any = answers
	if aggregateSet {
		runResult := aggregateChecks(order, answers)
		output = classifyAggregateOutput{
			Checks: answers,
			Run: classifyRunOutput{
				Outcome:  runResult.Outcome,
				Detail:   runResult.Detail,
				ExitCode: runResult.ExitCode,
			},
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate classify: writing classifications: %v\n", err)
		return record.ExitInternal
	}
	return record.ExitOK
}

func externalOrOwn(err error) error {
	if exitCode(err) != record.ExitInternal {
		return err
	}
	return externalf("%v", err)
}

func hasResolvedFingerprint(episodes []string, fp string) bool {
	if fp == "" {
		return false
	}
	prefix := fp + "|"
	for _, ep := range episodes {
		if strings.HasPrefix(ep, prefix) {
			return true
		}
	}
	return false
}
