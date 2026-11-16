package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// treeInput is a Git entry at the judged tree, not the worktree's current bytes.
// Mode participates too: making a program executable can change a check's verdict.
type treeInput struct {
	Path   string
	Object string
	Mode   string
}

func memoryGitOutput(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git %v: %s", args, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("git %v: %w", args, err)
	}
	return out, nil
}

func readTreeInputs(root, tree string) ([]treeInput, error) {
	raw, err := memoryGitOutput(root, "ls-tree", "-r", "-z", tree)
	if err != nil {
		return nil, err
	}
	var inputs []treeInput
	for _, entry := range strings.Split(string(raw), "\x00") {
		if entry == "" {
			continue
		}
		metadata, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(metadata)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("malformed ls-tree entry %q", entry)
		}
		inputs = append(inputs, treeInput{Path: path, Object: fields[2], Mode: fields[0]})
	}
	return inputs, nil
}

func coversPath(reads []string, path string) bool {
	if len(reads) == 0 {
		return true
	}
	for _, read := range reads {
		read = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(read)), "./")
		if read == "." || path == read || strings.HasPrefix(path, read+"/") {
			return true
		}
	}
	return false
}

// The declaration digest carries the original declaration's exact bytes through a
// plan. The effective step is hashed as well, so older or hand-written plans cannot
// reuse a result for a different command or execution environment.
func treeFingerprint(check PlanCheck, inputs []treeInput, root string) (string, error) {
	selected := []treeInput{}
	for _, input := range inputs {
		if coversPath(check.Reads, input.Path) {
			selected = append(selected, input)
		}
	}
	versions := []string{}
	for _, tool := range check.Tools {
		args := []string{"--version"}
		// Go's documented version command is a subcommand, not a flag.
		// Select it explicitly; a failed identity command is never retried
		// with guesses or treated as a tool-free fingerprint.
		if filepath.Base(tool) == "go" {
			args = []string{"version"}
		}
		cmd := exec.Command(tool, args...)
		cmd.Dir = root
		cmd.Env = checkEnv(check)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("version of tool %q: %w: %s", tool, err, strings.TrimSpace(string(raw)))
		}
		versions = append(versions, tool, string(raw))
	}
	refs, missing, err := resolveRefNeeds(check, root)
	if err != nil {
		return "", err
	}
	if len(missing) != 0 {
		return "", fmt.Errorf("ref needs became unavailable while fingerprinting: %v", missing)
	}
	raw, err := json.Marshal(struct {
		Format   int
		Check    PlanCheck
		Inputs   []treeInput
		Versions []string
		Refs     []refInput
	}{2, check, selected, versions, refs})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// bindingState is read before lookup, and again after execution before publication.
// A rename contributes both paths. Git-ignored inputs are deliberately not covered.
type bindingState struct {
	DifferentHEAD bool
	Paths         []string
}

func readBinding(root, tree string) (bindingState, error) {
	head, err := memoryGitOutput(root, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return bindingState{}, err
	}
	state := bindingState{DifferentHEAD: strings.TrimSpace(string(head)) != tree}
	raw, err := memoryGitOutput(root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return bindingState{}, err
	}
	entries := strings.Split(string(raw), "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if entry == "" {
			continue
		}
		if len(entry) < 4 {
			return bindingState{}, fmt.Errorf("malformed git status entry %q", entry)
		}
		state.Paths = append(state.Paths, entry[3:])
		if strings.ContainsAny(entry[:2], "RC") {
			i++
			if i >= len(entries) || entries[i] == "" {
				return bindingState{}, fmt.Errorf("missing rename source in git status")
			}
			state.Paths = append(state.Paths, entries[i])
		}
	}
	sort.Strings(state.Paths)
	return state, nil
}
func (state bindingState) unboundPath(reads []string) string {
	if state.DifferentHEAD {
		return "HEAD"
	}
	for _, path := range state.Paths {
		if coversPath(reads, path) {
			return path
		}
	}
	return ""
}

// Each verdict has its own filename. Publishing a pass never reads, writes, or
// removes a fail; opposite writers cannot erase each other's evidence.
type storedResult struct {
	CheckID    string `json:"check_id"`
	Result     string `json:"result"`
	DurationMS int64  `json:"duration_ms"`
	Time       string `json:"time"`
	Tree       string `json:"tree"`
}
type resultStore struct{ dir string }

func openResultStore(root string) (resultStore, error) {
	common, err := memoryGitOutput(root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return resultStore{}, err
	}
	return resultStore{dir: filepath.Join(strings.TrimSpace(string(common)), "tillr-gate", "results")}, nil
}
func (store resultStore) read(key string) (*storedResult, *storedResult, error) {
	read := func(verdict string) (*storedResult, error) {
		path := filepath.Join(store.dir, key+"."+verdict+".json")
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var result storedResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		if result.Result != verdict || result.CheckID == "" || result.Tree == "" || result.Time == "" {
			return nil, fmt.Errorf("invalid result file %s", path)
		}
		if _, err := time.Parse(time.RFC3339Nano, result.Time); err != nil {
			return nil, fmt.Errorf("invalid time in %s: %w", path, err)
		}
		return &result, nil
	}
	pass, err := read(ResultPass)
	if err != nil {
		return nil, nil, err
	}
	fail, err := read(ResultFail)
	return pass, fail, err
}
func (store resultStore) publish(key string, result storedResult) error {
	if result.Result != ResultPass && result.Result != ResultFail {
		return fmt.Errorf("cannot store result %q", result.Result)
	}
	if err := os.MkdirAll(store.dir, 0755); err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(store.dir, ".result-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(store.dir, key+"."+result.Result+".json"))
}

type treeMemory struct {
	tree    string
	inputs  []treeInput
	binding bindingState
	store   resultStore
}

func prepareTreeMemory(plan PlanPayload, root string) (*treeMemory, error) {
	needed := false
	for _, check := range plan.Checks {
		if check.Scope == "tree" && (check.Remember == nil || *check.Remember) {
			needed = true
			break
		}
	}
	if !needed {
		return nil, nil
	}
	// Resolve commit spellings too: the comparison is of trees, never commit ids.
	treeRaw, err := memoryGitOutput(root, "rev-parse", plan.TreeSHA+"^{tree}")
	if err != nil {
		return nil, err
	}
	tree := strings.TrimSpace(string(treeRaw))
	inputs, err := readTreeInputs(root, tree)
	if err != nil {
		return nil, err
	}
	binding, err := readBinding(root, tree)
	if err != nil {
		return nil, err
	}
	store, err := openResultStore(root)
	if err != nil {
		return nil, err
	}
	return &treeMemory{tree: tree, inputs: inputs, binding: binding, store: store}, nil
}

func annotateMemory(outcome *VerdictCheck, pass, fail *storedResult, remembered bool) {
	if pass != nil {
		outcome.PassTime = pass.Time
	}
	if fail != nil {
		outcome.FailTime = fail.Time
	}
	switch {
	case outcome.Result == ResultPass && pass != nil && fail != nil:
		outcome.Memory = "flaky"
	case remembered:
		outcome.Memory = "remembered"
	case outcome.Result == ResultPass:
		outcome.Memory = "passed"
	case outcome.Result == ResultFail:
		outcome.Memory = "failed"
	}
}
func (memory *treeMemory) run(check PlanCheck, root, logs string, index int) (VerdictCheck, error) {
	_, missing, err := resolveRefNeeds(check, root)
	if err != nil {
		return VerdictCheck{CheckID: check.CheckID, Result: ResultError}, err
	}
	if len(missing) > 0 {
		return VerdictCheck{CheckID: check.CheckID, Result: ResultUnavailable, MissingNeeds: missing}, nil
	}
	if check.Scope != "tree" || check.Remember != nil && !*check.Remember {
		outcome := runCheck(check, root, logs, index)
		if memory != nil {
			state, err := readBinding(root, memory.tree)
			if err != nil {
				return outcome, err
			}
			memory.binding = state
		}
		return outcome, nil
	}
	outcome := VerdictCheck{CheckID: check.CheckID, Result: ResultError}
	if path := memory.binding.unboundPath(check.Reads); path != "" {
		outcome = runCheck(check, root, logs, index)
		outcome.Memory = "not remembered"
		outcome.UnboundPath = path
		// Subsequent checks see changes made by this command too.
		state, err := readBinding(root, memory.tree)
		if err != nil {
			return outcome, err
		}
		memory.binding = state
		return outcome, nil
	}
	key, err := treeFingerprint(check, memory.inputs, root)
	if err != nil {
		return outcome, err
	}
	outcome.Fingerprint = key
	pass, fail, err := memory.store.read(key)
	if err != nil {
		return outcome, err
	}
	if pass != nil {
		outcome.Result = ResultPass
		annotateMemory(&outcome, pass, fail, true)
		return outcome, nil
	}
	outcome = runCheck(check, root, logs, index)
	outcome.Fingerprint = key
	state, err := readBinding(root, memory.tree)
	if err != nil {
		return outcome, err
	}
	memory.binding = state
	if path := state.unboundPath(check.Reads); path != "" {
		outcome.Memory = "not remembered"
		outcome.UnboundPath = path
		return outcome, nil
	}
	if outcome.Result != ResultPass && outcome.Result != ResultFail {
		return outcome, nil
	}
	// A ref moving during execution means the command's inputs are no longer
	// bound to its initial key. Do not publish that result under either tip.
	currentKey, err := treeFingerprint(check, memory.inputs, root)
	if err != nil {
		return outcome, err
	}
	if currentKey != key {
		outcome.Memory = "not remembered"
		outcome.UnboundPath = "needed ref or tool identity"
		return outcome, nil
	}
	result := storedResult{CheckID: check.CheckID, Result: outcome.Result, DurationMS: outcome.DurationMS, Time: time.Now().UTC().Format(time.RFC3339Nano), Tree: memory.tree}
	if err := memory.store.publish(key, result); err != nil {
		return outcome, err
	}
	// This read is for reporting, not merging. The separate files are what preserve
	// an opposite publication between the earlier read and this writer's rename.
	pass, fail, err = memory.store.read(key)
	if err != nil {
		return outcome, err
	}
	annotateMemory(&outcome, pass, fail, false)
	return outcome, nil
}
