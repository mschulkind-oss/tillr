// Package gate is the gate's own binary: it plans a change into the checks the gate
// would run, and runs them.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Registry is the check registry as data: the check *declarations* the gate can plan, and
// the digest of the declarations a verdict binds.
//
// It is a set of declarations rather than one plan instance because a plan instance is a
// function of a change: the `documents` check's argv is the changed Markdown paths, and a
// storybook check's reads are the paths the change matched. A registry that carried the
// instance the exporter happened to build would run the document checker over every
// tracked document whatever the change, and would select nothing for a file added after
// the export. A declaration carries the patterns that select the check and the command and
// reads it computes from the selected paths, so a planner given a change computes its own
// instance -- which is what `Plan` does.
//
// InProcess names the checks the gate runs as its own code rather than as a command. They
// are not in Declarations because a registry of commands cannot spell them, and they are
// not dropped because a planner that silently lost them would attest less than the gate it
// replaces. They carry the same `selects` and `watches` a command declaration does, so a
// planner can say which of them a change owes.
type Registry struct {
	Digest       string           `json:"digest"`
	Declarations []Declaration    `json:"declarations"`
	InProcess    []InProcessCheck `json:"in_process"`
}

// Pattern is one way a declaration names paths: a repo-relative path (a file, or a
// directory when it ends in `/`), or a regex with the refinements the document predicate
// adds. `Paths` is not a pattern at all: it is the placeholder a declaration's `watches`
// uses for the paths its own `selects` matched.
type Pattern struct {
	Path        string   `json:"path,omitempty"`
	Regex       string   `json:"regex,omitempty"`
	ExcludeDirs []string `json:"exclude_dirs,omitempty"`
	MustExist   bool     `json:"must_exist,omitempty"`
	Paths       bool     `json:"paths,omitempty"`

	compiled *regexp.Regexp
}

// Matches reports whether one changed path is selected by this pattern. A `Paths`
// placeholder selects nothing: it stands for the selected set, not for a rule.
func (p Pattern) Matches(path, root string) bool {
	if p.Paths {
		return false
	}
	if p.compiled != nil {
		if !p.compiled.MatchString(path) {
			return false
		}
		for _, part := range strings.Split(path, "/") {
			for _, excluded := range p.ExcludeDirs {
				if part == excluded {
					return false
				}
			}
		}
		if p.MustExist {
			info, err := os.Stat(filepath.Join(root, path))
			if err != nil || !info.Mode().IsRegular() {
				return false
			}
		}
		return true
	}
	return path == p.Path || (strings.HasSuffix(p.Path, "/") && strings.HasPrefix(path, p.Path))
}

// Declaration is one check as the gate declares it: what selects it, and the command and
// reads it computes from the selected paths. `{paths}` in `Command` is replaced by the
// selected paths; a `Paths` entry in `Watches` is replaced by them too.
type Declaration struct {
	DeclarationDigest string `json:"declaration_digest,omitempty"`
	undeclaredReads   bool   // Check files default to whole-tree fingerprinting even when reads is omitted.

	Scope           string            `json:"scope,omitempty"`
	BudgetMS        int               `json:"budget_ms,omitempty"`
	Tools           []string          `json:"tools,omitempty"`
	Remember        *bool             `json:"remember,omitempty"`
	ID              string            `json:"id"`
	Kind            string            `json:"kind"`
	Label           string            `json:"label"`
	Selects         []Pattern         `json:"selects"`
	Command         []string          `json:"command"`
	Watches         []Pattern         `json:"watches"`
	Writes          []string          `json:"writes"`
	After           []string          `json:"after"`
	TimeoutMS       int               `json:"timeout_ms"`
	Forced          string            `json:"forced"`
	Env             map[string]string `json:"env"`
	Drop            []string          `json:"drop"`
	Needs           []string          `json:"needs"`
	Tier            string            `json:"tier"`
	Exclusive       bool              `json:"exclusive"`
	ExclusiveReason string            `json:"exclusive_reason"`
}

// InProcessCheck is one check the gate runs in its own process. Kind is the stable
// identifier; Label is the sentence the report prints.
//
// Tier is the design's landing/rolling membership, in the same fail-safe direction the
// command declarations use: a check that names neither is the landing tier. The generated
// registry may omit it, and an unnamed in-process check is read as landing rather than as
// unclassified, because the gate's own-code checks are the integrity set.
type InProcessCheck struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Label   string    `json:"label"`
	Selects []Pattern `json:"selects"`
	Watches []Pattern `json:"watches"`
	Tier    string    `json:"tier,omitempty"`
}

// Check is one planned step, computed from a declaration for one change. `Reads` are the
// paths that select it, `Writes` the paths it must not share with a concurrent step,
// `DependsOn` the checks that must finish first, and `Forced` the flag that plans it when
// no changed path does.
type Check struct {
	DeclarationDigest string `json:"declaration_digest,omitempty"`

	Scope           string            `json:"scope,omitempty"`
	BudgetMS        int               `json:"budget_ms,omitempty"`
	Tools           []string          `json:"tools,omitempty"`
	Remember        *bool             `json:"remember,omitempty"`
	ID              string            `json:"id"`
	Kind            string            `json:"kind"`
	Label           string            `json:"label"`
	Command         []string          `json:"command"`
	TimeoutMS       int               `json:"timeout_ms"`
	Reads           []string          `json:"reads"`
	Writes          []string          `json:"writes"`
	DependsOn       []string          `json:"depends_on"`
	Forced          string            `json:"forced"`
	Env             map[string]string `json:"env"`
	Drop            []string          `json:"drop"`
	Needs           []string          `json:"needs"`
	Tier            string            `json:"tier"`
	Exclusive       bool              `json:"exclusive"`
	ExclusiveReason string            `json:"exclusive_reason"`
}

// Plan computes the checks a change selects: the plan instance the declarations describe.
//
// A declaration is selected when a changed path matches one of its `selects` patterns, or
// when it declares a `forced` flag -- the integrity set is planned every run rather than
// selected by a path. The selected paths are sorted, so two planners given the same change
// compute the same argv. `root` is the worktree the change is against, which a pattern that
// requires a file to still exist reads.
func (r *Registry) Plan(changed []string, root string) []Check {
	checks := make([]Check, 0, len(r.Declarations))
	for _, decl := range r.Declarations {
		selected := selectedPaths(decl.Selects, changed, root)
		if len(selected) == 0 && decl.Forced == "" {
			continue
		}
		checks = append(checks, Check{
			DeclarationDigest: decl.DeclarationDigest,

			Scope:           decl.Scope,
			BudgetMS:        decl.BudgetMS,
			Tools:           decl.Tools,
			Remember:        decl.Remember,
			ID:              decl.ID,
			Kind:            decl.Kind,
			Label:           decl.Label,
			Command:         expand(decl.Command, selected),
			TimeoutMS:       decl.TimeoutMS,
			Reads:           expandWatches(decl.Watches, selected),
			Writes:          decl.Writes,
			DependsOn:       decl.After,
			Forced:          decl.Forced,
			Env:             decl.Env,
			Drop:            decl.Drop,
			Needs:           decl.Needs,
			Tier:            decl.Tier,
			Exclusive:       decl.Exclusive,
			ExclusiveReason: decl.ExclusiveReason,
		})
	}
	return checks
}

// selectedPaths is the changed paths a declaration's patterns select, sorted.
func selectedPaths(selects []Pattern, changed []string, root string) []string {
	var selected []string
	for _, path := range changed {
		for _, pattern := range selects {
			if pattern.Matches(path, root) {
				selected = append(selected, path)
				break
			}
		}
	}
	sort.Strings(selected)
	return selected
}

// expand substitutes the selected paths for a `{paths}` element of a command.
func expand(command, selected []string) []string {
	out := make([]string, 0, len(command))
	for _, part := range command {
		if part == "{paths}" {
			out = append(out, selected...)
			continue
		}
		out = append(out, part)
	}
	return out
}

// expandWatches substitutes the selected paths for a `Paths` placeholder, and spells every
// other pattern as the path or regex source it declares.
func expandWatches(watches []Pattern, selected []string) []string {
	out := make([]string, 0, len(watches))
	for _, watch := range watches {
		switch {
		case watch.Paths:
			out = append(out, selected...)
		case watch.Regex != "":
			out = append(out, watch.Regex)
		default:
			out = append(out, watch.Path)
		}
	}
	return out
}

// LoadRegistry reads the generated registry and refuses one whose digest does not match
// its declarations. The digest is what a verdict binds, so a file edited without
// regenerating it would bind a verdict to a registry that never produced the plan.
//
// The digest is over the `declarations` and `in_process` values' exact bytes, in the
// canonical form the generator writes: JSON with no spaces, keys sorted, and non-ASCII
// escaped. A semantically identical file in another form is refused, so a second producer
// must reproduce that form rather than merely the same declarations.
//
// Both values are hashed because both are what a verdict owes: the in-process checks have
// no command to spell, so they are named separately, and a digest over the command
// declarations alone would let an edit to one of their selections change what a verdict
// owes without moving the digest.
func LoadRegistry(path string) (*Registry, error) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return loadCheckDirectory(path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the check registry: %w", err)
	}
	var file struct {
		Digest       string          `json:"digest"`
		Declarations json.RawMessage `json:"declarations"`
		InProcess    json.RawMessage `json:"in_process"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("decoding the check registry %s: %w", path, err)
	}
	hashed := make([]byte, 0, len(file.Declarations)+len(file.InProcess))
	hashed = append(append(hashed, file.Declarations...), file.InProcess...)
	sum := sha256.Sum256(hashed)
	if digest := hex.EncodeToString(sum[:]); digest != file.Digest {
		return nil, fmt.Errorf("the check registry %s carries digest %q but its declarations and "+
			"in-process checks hash to %q; regenerate it with `python3 tools/fork-check.py --registry-json`",
			path, file.Digest, digest)
	}
	reg := &Registry{Digest: file.Digest}
	if err := json.Unmarshal(file.Declarations, &reg.Declarations); err != nil {
		return nil, fmt.Errorf("decoding the check registry's declarations: %w", err)
	}
	if err := json.Unmarshal(file.InProcess, &reg.InProcess); err != nil {
		return nil, fmt.Errorf("decoding the check registry's in-process checks: %w", err)
	}
	seen := map[string]bool{}
	for index := range reg.Declarations {
		decl := &reg.Declarations[index]
		if decl.ID == "" || decl.Kind == "" || decl.Label == "" {
			return nil, fmt.Errorf("the check registry's declaration %d is missing an id, a kind, or a label", index)
		}
		if seen[decl.ID] {
			return nil, fmt.Errorf("the check registry declares %q twice; an id names one check", decl.ID)
		}
		seen[decl.ID] = true
		compile(decl.Selects)
		compile(decl.Watches)
	}
	for index := range reg.InProcess {
		compile(reg.InProcess[index].Selects)
		compile(reg.InProcess[index].Watches)
	}
	return reg, nil
}

// compile resolves each pattern's regex once, so selection does not recompile it per path.
func compile(patterns []Pattern) {
	for index := range patterns {
		if patterns[index].Regex == "" {
			continue
		}
		patterns[index].compiled = regexp.MustCompile(patterns[index].Regex)
	}
}
