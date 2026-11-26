package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
)

const (
	CodeWatchLintInput            = "watch-lint-input"
	CodeWatchLintCharterSchema    = "watch-lint-charter-schema"
	CodeWatchLintHookContract     = "watch-lint-hook-contract"
	CodeWatchLintWorkflowSchema   = "watch-lint-workflow-schema"
	CodeWatchLintCharterCandidate = "watch-lint-charter-candidate"
	CodeWatchLintSeatUnmapped     = "watch-lint-seat-unmapped"
	CodeWatchLintUnboundedReturn  = "watch-lint-unbounded-return"
	CodeWatchLintUnknownSignal    = "watch-lint-unknown-signal"
	CodeWatchLintUnknownStage     = "watch-lint-unknown-stage"
	CodeWatchLintEmptyGuidance    = "watch-lint-empty-guidance"
	CodeWatchLintForbiddenRemedy  = "watch-lint-forbidden-remedy"
	CodeWatchLintLivenessReceiver = "watch-lint-liveness-receiver"
	CodeWatchLintRuleSchema       = "watch-lint-rule-schema"
)

// LintOptions configures a read-only, store-free static lint pass over a
// (charter, workflows, backend policies, watch rules) configuration tuple.
type LintOptions struct {
	Root           string
	CharterPath    string
	WorkflowsPath  string
	PolicyPaths    []string
	RulesPath      string
	ShippedFS      fs.FS
	NoShippedRules bool
	HookTimeout    time.Duration
}

// LintFinding is one deterministic refusal emitted by Lint.
type LintFinding struct {
	Source string
	Code   string
	Detail string
}

// String formats the finding as `<source>: <code>: <detail>`.
func (f LintFinding) String() string {
	return fmt.Sprintf("%s: %s: %s", f.Source, f.Code, f.Detail)
}

type findingCollector struct {
	absRoot  string
	findings []LintFinding
	seen     map[LintFinding]bool
}

func newFindingCollector(absRoot string) *findingCollector {
	return &findingCollector{
		absRoot: absRoot,
		seen:    make(map[LintFinding]bool),
	}
}

func (c *findingCollector) add(source, code, detail string) {
	f := LintFinding{
		Source: c.formatSource(source),
		Code:   code,
		Detail: strings.TrimSpace(detail),
	}
	if !c.seen[f] {
		c.seen[f] = true
		c.findings = append(c.findings, f)
	}
}

func (c *findingCollector) formatSource(path string) string {
	if path == "" {
		return "."
	}
	if strings.HasPrefix(path, "shipped:") || path == "builtin:"+definition.GitLandingName {
		return path
	}
	absPath := path
	if !filepath.IsAbs(absPath) && c.absRoot != "" {
		if resolved, err := filepath.Abs(path); err == nil {
			absPath = resolved
		}
	}
	if c.absRoot != "" {
		if rel, err := filepath.Rel(c.absRoot, absPath); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}

func (c *findingCollector) sorted() []LintFinding {
	out := make([]LintFinding, len(c.findings))
	copy(out, c.findings)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Detail < out[j].Detail
	})
	return out
}

// LintedRuleSource records one effective rule ID and its formatted source path.
type LintedRuleSource struct {
	ID     string
	Source string
}

// LintReport contains the sorted findings and the effective rules evaluated by LintWithReport.
type LintReport struct {
	Findings []LintFinding
	Rules    []LintedRuleSource
}

// Lint validates the four-part configuration tuple (charter, workflows,
// backend policies, watch rules) without opening .tillr/state.sqlite or
// mutating any file or Git state.
func Lint(ctx context.Context, opts LintOptions) ([]LintFinding, error) {
	report, err := LintWithReport(ctx, opts)
	return report.Findings, err
}

// LintWithReport behaves like Lint and also returns the effective rules with
// their attributed sources ("shipped:..." or repository file path).
func LintWithReport(ctx context.Context, opts LintOptions) (LintReport, error) {
	root := opts.Root
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return LintReport{}, fmt.Errorf("getwd: %w", err)
		}
		root = cwd
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	fc := newFindingCollector(absRoot)

	rootInfo, err := os.Stat(absRoot)
	if err != nil || !rootInfo.IsDir() {
		detail := fmt.Sprintf("root %q must be an existing directory", root)
		if err != nil {
			detail = fmt.Sprintf("root %q: %v", root, err)
		}
		fc.add(root, CodeWatchLintInput, detail)
		return LintReport{Findings: fc.sorted()}, nil
	}

	kinds := registeredRuleKinds()

	charter := loadAndLintCharter(absRoot, opts.CharterPath, fc)
	rulesDir, rulesDirExists := resolveRulesDir(absRoot, opts.RulesPath)
	if charter.valid {
		lintCharterObservability(charter, rulesDir, rulesDirExists, kinds, fc)
		lintCharterHooks(ctx, absRoot, charter, opts.HookTimeout, fc)
	}

	workflows, wfDir := discoverAndLoadWorkflows(absRoot, opts.WorkflowsPath, fc)
	policies, missingDefaultPolicy := loadAndLintPolicies(absRoot, opts.PolicyPaths, fc)
	rules := discoverAndLoadRuleLayers(absRoot, opts.RulesPath, opts.ShippedFS, opts.NoShippedRules, kinds, fc)

	if charter.valid {
		lintWorkflowsWithCharter(workflows, charter, fc)
	}
	lintWorkflowsWithPolicies(absRoot, workflows, policies, missingDefaultPolicy, fc)
	lintWorkflowsWatchRefs(absRoot, workflows, wfDir, fc)
	lintWorkflowsWithRules(workflows, rules, fc)
	lintRulesWithContext(rules, charter, workflows, fc)

	sources := make([]LintedRuleSource, 0, len(rules))
	for _, lr := range rules {
		if lr.rule.ID == "" {
			continue
		}
		sources = append(sources, LintedRuleSource{
			ID:     lr.rule.ID,
			Source: fc.formatSource(lr.source),
		})
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].ID != sources[j].ID {
			return sources[i].ID < sources[j].ID
		}
		return sources[i].Source < sources[j].Source
	})

	return LintReport{
		Findings: fc.sorted(),
		Rules:    sources,
	}, nil
}

// checkDuplicateJSONKeys verifies that raw JSON contains no duplicate object
// keys at any nesting level.
func checkDuplicateJSONKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := checkJSONValueDuplicateKeys(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON content")
		}
		return err
	}
	return nil
}

func checkJSONValueDuplicateKeys(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for dec.More() {
			kTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := kTok.(string)
			if !ok {
				return fmt.Errorf("expected string key")
			}
			if seen[key] {
				return fmt.Errorf("duplicate key %q", key)
			}
			seen[key] = true
			if err := checkJSONValueDuplicateKeys(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return fmt.Errorf("expected '}'")
		}
	case '[':
		for dec.More() {
			if err := checkJSONValueDuplicateKeys(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return fmt.Errorf("expected ']'")
		}
	}
	return nil
}
