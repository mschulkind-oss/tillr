// Package watch loads tillr's declarative alert rules. Loading is read-only:
// rules are validated data, with no commands or alert actions to execute.
package watch

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/pelletier/go-toml/v2"
)

const (
	PredicateMatch       = "predicate_match"
	CountInWindow        = "count_in_window"
	ConsecutiveCount     = "consecutive_count"
	AlternatingPair      = "alternating_pair"
	LagBehind            = "lag_behind"
	DurationWithoutEvent = "duration_without_event"

	RuleSourceShipped = "shipped"
	RuleSourceFile    = "file"
	RuleSourceStore   = "store"
)

type Rule struct {
	ID, Scope, Severity string
	Enabled             bool
	Variables           map[string]string
	Reads               Reads
	When                When
	Guidance            Guidance
	Delivery            Delivery

	// Keep the validated table, including omitted vs. explicit optional keys,
	// for whole-rule encoding and tuning without reconstituting TOML from defaults.
	table            map[string]any
	origin           string
	unregisteredKind string
}

// Origin returns the rule's load source ("shipped", "file", or "store").
func (r Rule) Origin() string {
	return r.origin
}

type Reads struct {
	Events, Projections []string
}

type When struct {
	Fingerprint                                []string
	GroupBy                                    string
	GroupByList                                []string
	GroupByAny                                 []string
	Pattern, Field, DistinctBy, ForEach        string
	BroadcastEvents                            []string
	Pairs                                      [][2]string
	Threshold, ThresholdCommits                int64
	Window, ThresholdDuration                  time.Duration
	Leader, Follower, Mode                     string
	Match, Reset, ClearWhen, After, ReopenWhen *Expression
	KeepOnTerminalItem                         bool
}

type Guidance struct {
	Summary, ReopenSummary string
	Check, Do              []string
}

type Delivery struct {
	Receiver   string
	AlsoNotify []string
}

// UnevaluatedReason reports eligibility for the legacy command-facing
// Evaluate and Skipped methods. Enabled is an independent operator choice.
func (r Rule) UnevaluatedReason() string {
	if r.unregisteredKind != "" {
		return fmt.Sprintf("unregistered event kind %q", r.unregisteredKind)
	}
	if reason := r.scopeReason(); reason != "" {
		return reason
	}
	switch r.When.Pattern {
	case LagBehind, DurationWithoutEvent:
		return "unsupported pattern " + r.When.Pattern
	}
	if len(r.Reads.Projections) != 0 {
		return "reads projections"
	}
	for _, e := range r.expressions() {
		if e != nil && (!e.Supported() || e.usesNotIn) {
			return "uses an unsupported expression form"
		}
	}
	return ""
}

func (r Rule) scopeReason() string {
	switch r.Scope {
	case "", "*", "core", "workflow:*", "charter:*":
		return ""
	default:
		return "named scope is not evaluated by this build"
	}
}

func (r Rule) expressions() [5]*Expression {
	return [5]*Expression{r.When.Match, r.When.Reset, r.When.ClearWhen, r.When.After, r.When.ReopenWhen}
}

func (r Rule) evaluatorReason() string {
	if r.unregisteredKind != "" {
		return fmt.Sprintf("unregistered event kind %q", r.unregisteredKind)
	}
	for _, projection := range r.Reads.Projections {
		if projection != "items" && projection != "charter" && projection != "git_refs" && projection != "fixer_checks" &&
			!strings.HasPrefix(projection, "git_refs:") && !strings.HasPrefix(projection, "green_proof:") {
			return fmt.Sprintf("unsupported projection %q", projection)
		}
	}
	for _, e := range r.expressions() {
		if e != nil && e.unsupported {
			return "uses an unsupported expression form"
		}
	}
	return ""
}

// LoadRules validates every .toml file beneath dir, in path and file order.
// A refusal returns no rules, including rules loaded before the refusal.
// Callers must link the owners of the event kinds they recognize before
// loading; the loader uses the process's event registry, not a second list.
func LoadRules(dir string) ([]Rule, error) {
	if dir == "" {
		return nil, fmt.Errorf("rules path must be a directory")
	}
	return LoadRuleLayers(nil, dir)
}

// LoadRulesFS validates every .toml file beneath fsys in sorted path order,
// tagging each rule with RuleSourceShipped and refusing [[override]] blocks.
func LoadRulesFS(fsys fs.FS) ([]Rule, error) {
	if fsys == nil {
		return nil, nil
	}
	kinds := registeredRuleKinds()
	return loadRulesFS(fsys, kinds)
}

// RuleLoadOptions configures how LoadRuleLayersWithOptions validates rule layers.
type RuleLoadOptions struct {
	AllowUnregisteredKinds bool
}

// LoadRuleLayers loads shipped rules from shippedFS (if non-nil) and layers
// repository rules and [[override]] entries from repoDir (if non-empty) on top.
func LoadRuleLayers(shippedFS fs.FS, repoDir string) ([]Rule, error) {
	return LoadRuleLayersWithOptions(shippedFS, repoDir, RuleLoadOptions{})
}

// LoadRuleLayersWithOptions loads shipped rules from shippedFS (if non-nil) and
// layers repository rules and [[override]] entries from repoDir (if non-empty)
// on top according to opts.
func LoadRuleLayersWithOptions(shippedFS fs.FS, repoDir string, opts RuleLoadOptions) ([]Rule, error) {
	kinds := registeredRuleKinds()
	var rules []Rule
	shippedByID := make(map[string]Rule)
	indexByID := make(map[string]int)

	if shippedFS != nil {
		shipped, err := loadRulesFS(shippedFS, kinds)
		if err != nil {
			return nil, err
		}
		for _, r := range shipped {
			indexByID[r.ID] = len(rules)
			shippedByID[r.ID] = r
			rules = append(rules, r)
		}
	}

	if repoDir == "" {
		return rules, nil
	}

	root, err := filepath.EvalSymlinks(repoDir)
	if err != nil {
		return nil, err
	}
	repoSeen := make(map[string]string)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root && !entry.IsDir() {
			return fmt.Errorf("%s: rules path must be a directory", repoDir)
		}
		if entry.IsDir() || filepath.Ext(path) != ".toml" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		loaded, err := loadRepoRuleDocument(path, data, kinds, repoSeen, shippedByID, opts.AllowUnregisteredKinds)
		if err != nil {
			return err
		}
		for _, r := range loaded {
			if idx, ok := indexByID[r.ID]; ok {
				rules[idx] = r
			} else {
				indexByID[r.ID] = len(rules)
				rules = append(rules, r)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rules, nil
}

func loadRulesFS(fsys fs.FS, kinds map[string]bool) ([]Rule, error) {
	var rules []Rule
	seen := make(map[string]string)
	err := fs.WalkDir(fsys, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".toml" {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		loaded, err := loadRuleDocument("shipped:"+path, data, kinds, seen)
		if err != nil {
			return err
		}
		for i := range loaded {
			loaded[i].origin = RuleSourceShipped
		}
		rules = append(rules, loaded...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rules, nil
}

// LoadRuleDocument validates a complete document, using source in diagnostics.
func LoadRuleDocument(source string, data []byte) ([]Rule, error) {
	return loadRuleDocument(source, data, registeredRuleKinds(), make(map[string]string))
}

// EncodeRules writes the validated tables of loaded rules in the supplied order.
func EncodeRules(rules []Rule) ([]byte, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	tables := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		if rule.table == nil {
			return nil, fmt.Errorf("rule %q was not loaded from TOML", rule.ID)
		}
		tables = append(tables, rule.table)
	}
	return toml.Marshal(map[string]any{"rule": tables})
}

func registeredRuleKinds() map[string]bool {
	kinds := make(map[string]bool)
	for _, kind := range event.Kinds() {
		kinds[kind.Name] = true
	}
	return kinds
}

func loadRuleDocument(source string, data []byte, kinds map[string]bool, seen map[string]string) ([]Rule, error) {
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	for _, key := range slices.Sorted(maps.Keys(doc)) {
		if key != "rule" {
			return nil, fmt.Errorf("%s: %s: unknown top-level key", source, key)
		}
	}
	raw, present := doc["rule"]
	if !present {
		return nil, nil
	}
	tables, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: rule: expected [[rule]] entries", source)
	}
	var rules []Rule
	for i, raw := range tables {
		table, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: rule[%d]: expected a rule table", source, i)
		}
		id, _ := table["id"].(string)
		c := &ruleCheck{path: source, id: id, index: i}
		rule, err := c.load(table, kinds)
		if err != nil {
			return nil, err
		}
		if previous, ok := seen[id]; ok {
			return nil, c.refusal("id", "duplicate rule ID (first in %s)", previous)
		}
		seen[id] = source
		rule.origin = RuleSourceFile
		rules = append(rules, rule)
	}
	return rules, nil
}

func loadRepoRuleDocument(source string, data []byte, kinds map[string]bool, seen map[string]string, shippedByID map[string]Rule, allowUnregisteredKinds bool) ([]Rule, error) {
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	for _, key := range slices.Sorted(maps.Keys(doc)) {
		if key != "rule" && key != "override" {
			return nil, fmt.Errorf("%s: %s: unknown top-level key", source, key)
		}
	}
	var rules []Rule
	if raw, present := doc["rule"]; present {
		tables, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("%s: rule: expected [[rule]] entries", source)
		}
		for i, raw := range tables {
			table, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s: rule[%d]: expected a rule table", source, i)
			}
			id, _ := table["id"].(string)
			c := &ruleCheck{path: source, id: id, index: i, allowUnregisteredKinds: allowUnregisteredKinds}
			rule, err := c.load(table, kinds)
			if err != nil {
				return nil, err
			}
			if previous, ok := seen[id]; ok {
				return nil, c.refusal("id", "duplicate rule ID (first in %s)", previous)
			}
			seen[id] = source
			rule.origin = RuleSourceFile
			rules = append(rules, rule)
		}
	}
	if raw, present := doc["override"]; present {
		tables, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("%s: override: expected [[override]] entries", source)
		}
		for i, raw := range tables {
			table, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s: override[%d]: expected an override table", source, i)
			}
			rule, err := loadOverrideTable(source, i, table, kinds, seen, shippedByID, allowUnregisteredKinds)
			if err != nil {
				return nil, err
			}
			rule.origin = RuleSourceFile
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

func loadOverrideTable(source string, index int, table map[string]any, kinds map[string]bool, seen map[string]string, shippedByID map[string]Rule, allowUnregisteredKinds bool) (Rule, error) {
	id, _ := table["id"].(string)
	c := &ruleCheck{path: source, id: id, index: index, allowUnregisteredKinds: allowUnregisteredKinds}
	c.noRemedy(table, "")
	if c.err != nil {
		return Rule{}, c.err
	}
	if id == "" {
		return Rule{}, fmt.Errorf("%s: override[%d]: id: expected a non-empty string", source, index)
	}
	if len(table) == 1 {
		return Rule{}, fmt.Errorf("%s: override %q: must specify at least one key besides id", source, id)
	}
	for _, key := range slices.Sorted(maps.Keys(table)) {
		switch key {
		case "id", "enabled", "scope", "severity", "reads", "when", "guidance", "delivery":
		default:
			return Rule{}, fmt.Errorf("%s: override %q: %s: unknown key", source, id, key)
		}
	}
	if previous, ok := seen[id]; ok {
		return Rule{}, fmt.Errorf("%s: override %q: id: duplicate rule ID (first in %s)", source, id, previous)
	}
	shipped, ok := shippedByID[id]
	if !ok {
		return Rule{}, fmt.Errorf("%s: override %q: no shipped rule with id %q", source, id, id)
	}
	seen[id] = source
	merged, err := applyOverrideTable(source, id, shipped.table, table)
	if err != nil {
		return Rule{}, err
	}
	return c.load(merged, kinds)
}

func applyOverrideTable(source, id string, base, patch map[string]any) (map[string]any, error) {
	merged := cloneTable(base)
	for _, key := range slices.Sorted(maps.Keys(patch)) {
		if key == "id" {
			continue
		}
		val := patch[key]
		switch key {
		case "enabled", "scope", "severity":
			merged[key] = cloneTOMLValue(val)
		case "reads", "when", "guidance", "delivery":
			subPatch, ok := val.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s: override %q: %s: expected a table", source, id, key)
			}
			var subMerged map[string]any
			if existing, ok := merged[key].(map[string]any); ok {
				subMerged = cloneTable(existing)
			} else {
				subMerged = make(map[string]any)
			}
			for _, subKey := range slices.Sorted(maps.Keys(subPatch)) {
				subMerged[subKey] = cloneTOMLValue(subPatch[subKey])
			}
			merged[key] = subMerged
		}
	}
	return merged, nil
}

type ruleCheck struct {
	path, id               string
	index                  int
	allowUnregisteredKinds bool
	err                    error
}

func (c *ruleCheck) refusal(key, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if c.id == "" {
		return fmt.Errorf("%s: rule[%d]: %s: %s", c.path, c.index, key, msg)
	}
	return fmt.Errorf("%s: rule %q: %s: %s", c.path, c.id, key, msg)
}

func (c *ruleCheck) refuse(key, format string, args ...any) {
	if c.err == nil {
		c.err = c.refusal(key, format, args...)
	}
}

// Check recursively before interpreting any table, even unread pattern keys.
func (c *ruleCheck) noRemedy(raw any, path string) {
	switch node := raw.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(node)) {
			key := path + k
			if k == "remedy" {
				c.refuse(key, "remedies are not allowed")
				return
			}
			c.noRemedy(node[k], key+".")
		}
	case []any:
		for i, elem := range node {
			c.noRemedy(elem, fmt.Sprintf("%s[%d].", strings.TrimSuffix(path, "."), i))
		}
	}
}

type keys struct {
	*ruleCheck
	prefix string
	values map[string]any
}

func (k keys) reject(key, format string, args ...any) {
	k.refuse(k.prefix+key, format, args...)
}

func (k keys) only(allowed ...string) {
	for _, key := range slices.Sorted(maps.Keys(k.values)) {
		if !slices.Contains(allowed, key) {
			k.reject(key, "unknown key")
		}
	}
}

func (k keys) table(key string) keys {
	var table map[string]any
	if raw, present := k.values[key]; present {
		var ok bool
		table, ok = raw.(map[string]any)
		if !ok {
			k.reject(key, "expected a table")
		}
	}
	return keys{k.ruleCheck, k.prefix + key + ".", table}
}

func requiredValue[T any](k keys, key, expected string) T {
	v, ok := k.values[key].(T)
	if !ok {
		k.reject(key, "expected %s", expected)
	}
	return v
}

func (k keys) text(key string) string {
	s := requiredValue[string](k, key, "a non-empty string")
	if s == "" {
		k.reject(key, "expected a non-empty string")
	}
	return s
}

func (k keys) eventPath(key string) string {
	s := k.text(key)
	if !validEventPath(s) {
		k.reject(key, "expected an event column or a dotted JSON key path with no empty segments")
	}
	return s
}

// Quoted TOML values name JSON keys, not expression identifiers.
func validEventPath(s string) bool {
	for part := range strings.SplitSeq(s, ".") {
		if part == "" {
			return false
		}
	}
	return true
}

func (k keys) strings(key string, required bool) []string {
	if _, present := k.values[key]; !present && !required {
		return nil
	}
	raw := requiredValue[[]any](k, key, "a list of strings")
	if required && len(raw) == 0 {
		k.reject(key, "expected a non-empty list of strings")
	}
	out := make([]string, len(raw))
	for i, value := range raw {
		s, ok := value.(string)
		if !ok || required && s == "" {
			k.reject(key, "invalid string at index %d", i)
		}
		out[i] = s
	}
	return out
}

func (k keys) expression(key string, required bool) *Expression {
	if _, present := k.values[key]; !present && !required {
		return nil
	}
	e, err := ParseExpression(k.text(key))
	if err != nil {
		k.reject(key, "%v", err)
	}
	return e
}

func (c *ruleCheck) load(table map[string]any, kinds map[string]bool) (Rule, error) {
	c.noRemedy(table, "")
	root := keys{c, "", table}
	root.only("id", "enabled", "scope", "severity", "variables", "reads", "when", "guidance", "delivery")
	r := Rule{ID: root.text("id"), Enabled: true, Scope: root.text("scope"), Severity: root.text("severity"), table: cloneTable(table)}
	if len(r.ID) > 128 {
		root.reject("id", "must be at most 128 bytes")
	}
	if _, present := table["enabled"]; present {
		r.Enabled = requiredValue[bool](root, "enabled", "a boolean")
	}
	prefix, name, _ := strings.Cut(r.Scope, ":")
	if r.Scope != "core" && !((prefix == "charter" || prefix == "workflow") && name != "") {
		root.reject("scope", "expected core, charter:<name> or workflow:<name>")
	}
	if !slices.Contains([]string{"info", "warning", "high"}, r.Severity) {
		root.reject("severity", "expected info, warning or high")
	}
	reads := root.table("reads")
	reads.only("events", "projections")
	when := root.table("when")
	pattern := when.text("pattern")
	r.Reads = Reads{Events: reads.strings("events", pattern != LagBehind && pattern != PredicateMatch), Projections: reads.strings("projections", false)}
	for _, kind := range r.Reads.Events {
		if !kinds[kind] {
			if c.allowUnregisteredKinds {
				if r.unregisteredKind == "" {
					r.unregisteredKind = kind
				}
			} else {
				reads.reject("events", "unregistered event kind %q", kind)
			}
		}
	}
	refs := make(map[string]bool)
	proofRefs := make(map[string]bool)
	for _, projection := range r.Reads.Projections {
		if list, ok := strings.CutPrefix(projection, "git_refs:"); ok {
			for ref := range strings.SplitSeq(list, ",") {
				if !validRef(ref) {
					reads.reject("projections", "invalid Git ref %q in %q", ref, projection)
				}
				refs[ref] = true
			}
		} else if ref, ok := strings.CutPrefix(projection, "green_proof:"); ok {
			if !validRef(ref) {
				reads.reject("projections", "invalid Git ref %q in %q", ref, projection)
			}
			proofRefs[ref] = true
		}
	}
	projectionOnly := pattern == PredicateMatch && len(r.Reads.Events) == 0
	if projectionOnly && !slices.Contains(r.Reads.Projections, "items") {
		reads.reject("projections", "projection-only predicate_match requires \"items\"")
	}
	r.When = loadWhen(when, pattern, refs, projectionOnly)
	if projectionOnly {
		if r.When.GroupBy != "item_id" || len(r.When.GroupByList) != 0 || len(r.When.GroupByAny) != 0 {
			when.reject("group_by", "projection-only predicate_match requires group_by = \"item_id\"")
		}
		if r.When.Match != nil && r.When.Match.usesEvent {
			when.reject("match", "projection-only predicate_match cannot reference event.*")
		}
		if _, has := when.values["clear_when"]; has {
			when.reject("clear_when", "projection-only predicate_match must omit clear_when")
		}
		if _, has := when.values["fingerprint"]; has {
			when.reject("fingerprint", "projection-only predicate_match must omit fingerprint")
		}
	}
	if rawVars, present := table["variables"]; present {
		r.Variables = loadVariables(root, rawVars, r.When.Pattern)
	}
	if _, err := resolveVariables(r); err != nil {
		if _, present := table["variables"]; present {
			root.reject("variables", "%v", err)
		} else {
			when.reject("group_by", "%v", err)
		}
	}
	if r.When.ForEach != "" && len(r.Reads.Events) == 0 {
		when.reject("for_each", "requires reads.events")
	}
	validateBroadcastEvents(when, r)
	if r.When.ReopenWhen != nil && len(r.Reads.Events) == 0 {
		when.reject("reopen_when", "requires reads.events")
	}
	if namedScope(r.Scope, "workflow:") != "" {
		if r.When.GroupBy == "global" {
			when.reject("group_by", "named workflow scope requires a subject")
		} else if len(r.When.GroupByList) >= 2 {
			when.reject("group_by", "named workflow scope requires a single subject")
		}
	}

	checkProjectionOperands(when, r, refs, proofRefs)
	g := root.table("guidance")
	g.only("summary", "reopen_summary", "check", "do")
	var reopenSummary string
	if _, present := g.values["reopen_summary"]; present {
		if r.When.Pattern != LagBehind || r.When.ReopenWhen == nil {
			g.reject("reopen_summary", "requires lag_behind with when.reopen_when")
		} else {
			reopenSummary = g.text("reopen_summary")
			if strings.TrimSpace(reopenSummary) == "" {
				g.reject("reopen_summary", "expected a non-empty non-whitespace string")
			}
		}
	}
	r.Guidance = Guidance{Summary: g.text("summary"), ReopenSummary: reopenSummary, Check: g.strings("check", true), Do: g.strings("do", true)}
	if projectionOnly {
		checkNoEventPlaceholder := func(key, text string) {
			tokens, err := extractGuidancePlaceholders(text)
			if err != nil {
				return
			}
			for _, tok := range tokens {
				if strings.HasPrefix(tok, "event.") {
					g.reject(key, "projection-only predicate_match cannot use {{%s}}", tok)
					return
				}
			}
		}
		checkNoEventPlaceholder("summary", r.Guidance.Summary)
		for i, line := range r.Guidance.Check {
			checkNoEventPlaceholder(fmt.Sprintf("check[%d]", i), line)
		}
		for i, line := range r.Guidance.Do {
			checkNoEventPlaceholder(fmt.Sprintf("do[%d]", i), line)
		}
	}
	d := root.table("delivery")
	d.only("receiver", "also_notify")
	r.Delivery = Delivery{Receiver: d.text("receiver"), AlsoNotify: d.strings("also_notify", false)}
	checkReceiver := func(key, value string) {
		if !isValidReceiver(value) {
			d.reject(key, "receiver %q is not foreground, subscribers, triage or operator-session", value)
		}
	}
	checkReceiver("receiver", r.Delivery.Receiver)
	for _, value := range r.Delivery.AlsoNotify {
		checkReceiver("also_notify", value)
	}
	return r, c.err
}

func isValidReceiver(value string) bool {
	switch value {
	case "foreground", "subscribers", "triage", "operator-session":
		return true
	default:
		return false
	}
}

func loadVariables(root keys, raw any, pattern string) map[string]string {
	tbl, ok := raw.(map[string]any)
	if !ok {
		root.reject("variables", "expected a table")
		return nil
	}
	if pattern == LagBehind {
		root.reject("variables", "not allowed on lag_behind rules")
		return nil
	}
	if len(tbl) == 0 {
		return nil
	}
	vars := make(map[string]string, len(tbl))
	for _, key := range slices.Sorted(maps.Keys(tbl)) {
		if !validVarIdent(key) {
			root.reject("variables."+key, "expected an ASCII identifier ([A-Za-z_][A-Za-z0-9_]*)")
			continue
		}
		val, ok := tbl[key].(string)
		if !ok || !validVariableEventPath(val) {
			root.reject("variables."+key, "expected a non-empty event path without event. prefix or whitespace")
			continue
		}
		vars[key] = val
	}
	return vars
}

func loadGroupBy(k keys) (string, []string, []string) {
	raw, present := k.values["group_by"]
	if !present {
		k.reject("group_by", "expected a non-empty string, a non-empty list of strings, or { any = [...] }")
		return "", nil, nil
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			k.reject("group_by", "expected a non-empty string")
			return "", nil, nil
		}
		if !validEventPath(v) {
			k.reject("group_by", "expected an event column or a dotted JSON key path with no empty segments")
			return "", nil, nil
		}
		return v, nil, nil
	case []any:
		if len(v) == 0 {
			k.reject("group_by", "expected a non-empty list of strings")
			return "", nil, nil
		}
		list := make([]string, 0, len(v))
		seen := make(map[string]bool, len(v))
		for i, elem := range v {
			s, ok := elem.(string)
			if !ok || s == "" {
				k.reject("group_by", "invalid string at index %d", i)
				return "", nil, nil
			}
			if s == "global" {
				k.reject("group_by", "global is not allowed in a group_by list")
				return "", nil, nil
			}
			if !validEventPath(s) {
				k.reject("group_by", "expected an event column or a dotted JSON key path with no empty segments")
				return "", nil, nil
			}
			if seen[s] {
				k.reject("group_by", "duplicate group_by name %q", s)
				return "", nil, nil
			}
			seen[s] = true
			list = append(list, s)
		}
		if len(list) == 1 {
			return list[0], nil, nil
		}
		return strings.Join(list, ", "), list, nil
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(v)) {
			if key != "any" {
				k.reject("group_by", "unknown key %q in group_by table", key)
				return "", nil, nil
			}
		}
		rawAny, ok := v["any"].([]any)
		if !ok || len(rawAny) == 0 {
			k.reject("group_by", "expected any to be a non-empty list of strings")
			return "", nil, nil
		}
		list := make([]string, 0, len(rawAny))
		seen := make(map[string]bool, len(rawAny))
		for i, elem := range rawAny {
			s, ok := elem.(string)
			if !ok || s == "" {
				k.reject("group_by", "invalid string at index %d", i)
				return "", nil, nil
			}
			if s == "global" {
				k.reject("group_by", "global is not allowed in group_by.any")
				return "", nil, nil
			}
			if !validEventPath(s) {
				k.reject("group_by", "expected an event column or a dotted JSON key path with no empty segments")
				return "", nil, nil
			}
			if isForbiddenGroupByColumn(s) {
				k.reject("group_by", "integer column %q cannot be used in group_by.any", s)
				return "", nil, nil
			}
			if seen[s] {
				k.reject("group_by", "duplicate group_by name %q", s)
				return "", nil, nil
			}
			seen[s] = true
			list = append(list, s)
		}
		if len(list) == 1 {
			return list[0], nil, list
		}
		return strings.Join(list, " | "), nil, list
	default:
		k.reject("group_by", "expected a non-empty string, a non-empty list of strings, or { any = [...] }")
		return "", nil, nil
	}
}

func loadWhen(k keys, pattern string, refs map[string]bool, projectionOnly bool) When {
	w := When{Pattern: pattern}
	w.GroupBy, w.GroupByList, w.GroupByAny = loadGroupBy(k)
	if pattern == LagBehind {
		return loadLag(k, w, refs)
	}
	w.ClearWhen = k.expression("clear_when", false)
	if _, present := k.values["fingerprint"]; present {
		w.Fingerprint = k.strings("fingerprint", true)
		for _, name := range w.Fingerprint {
			if !validEventPath(name) || strings.HasPrefix(name, "event.") {
				k.reject("fingerprint", "expected an event name without the event. prefix, got %q", name)
			}
		}
	}
	if _, present := k.values["keep_on_terminal_item"]; present {
		w.KeepOnTerminalItem = requiredValue[bool](k, "keep_on_terminal_item", "a boolean")
	}
	allowed := []string{"group_by", "pattern", "clear_when", "window", "fingerprint", "keep_on_terminal_item", "for_each", "broadcast_events"}
	w.ForEach = loadForEach(k)
	w.BroadcastEvents = loadBroadcastEvents(k)
	switch w.Pattern {
	case DurationWithoutEvent:
		k.only(append(allowed, "after", "match", "threshold_duration")...)
		w.After = k.expression("after", true)
		w.Match = k.expression("match", false)
		w.ThresholdDuration = k.duration("threshold_duration")
	case PredicateMatch:
		k.only(append(allowed, "after", "match", "reset")...)
		w.Threshold = 1
		if _, hasAfter := k.values["after"]; hasAfter {
			if projectionOnly {
				k.reject("after", "projection-only predicate_match must omit after")
			}
			w.After = k.expression("after", true)
			if _, hasMatch := k.values["match"]; !hasMatch {
				k.reject("match", "required when after is set on predicate_match")
			} else {
				w.Match = k.expression("match", true)
			}
			w.Reset = k.expression("reset", false)
		} else {
			w.Match = k.expression("match", projectionOnly)
			if _, hasReset := k.values["reset"]; hasReset {
				k.reject("reset", "requires after on predicate_match")
			}
		}
	case CountInWindow:
		k.only(append(allowed, "match", "distinct_by", "threshold")...)
		w.Match = k.expression("match", false)
		w.DistinctBy = loadDistinctBy(k)
	case ConsecutiveCount:
		k.only(append(allowed, "match", "reset", "distinct_by", "threshold")...)
		w.Match = k.expression("match", true)
		w.Reset = k.expression("reset", false)
		w.DistinctBy = loadDistinctBy(k)
	case AlternatingPair:
		k.only(append(allowed, "field", "pairs", "threshold")...)
		w.Field = k.eventPath("field")
		w.Pairs = loadPairs(k)
	default:
		k.reject("pattern", "unknown pattern %q", w.Pattern)
		return w
	}
	if w.Pattern != PredicateMatch && w.Pattern != DurationWithoutEvent {
		w.Threshold = k.positiveInteger("threshold")
	}
	_, hasWindow := k.values["window"]
	if w.Pattern == PredicateMatch && projectionOnly {
		if hasWindow {
			k.reject("window", "projection-only predicate_match must omit window")
		}
		w.Window = 0
	} else if w.Pattern == PredicateMatch && w.ClearWhen != nil && !hasWindow {
		w.Window = 0
	} else {
		w.Window = k.duration("window")
	}
	if w.Pattern == DurationWithoutEvent && w.Window <= w.ThresholdDuration {
		k.reject("window", "must be longer than threshold_duration")
	}
	return w
}

func loadDistinctBy(k keys) string {
	if _, present := k.values["distinct_by"]; !present {
		return ""
	}
	name := k.text("distinct_by")
	if name == "" {
		return ""
	}
	if name == "global" || strings.HasPrefix(name, "event.") || !validEventPath(name) {
		k.reject("distinct_by", "expected an event name without the event. prefix or global, got %q", name)
		return ""
	}
	if isForbiddenGroupByColumn(name) {
		k.reject("distinct_by", "integer column %q cannot be used as distinct_by", name)
		return ""
	}
	return name
}

func isForbiddenForEachPath(col string) bool {
	switch col {
	case "global", "kind", "seat", "object_type", "object_id":
		return true
	default:
		return false
	}
}

func loadForEach(k keys) string {
	if _, present := k.values["for_each"]; !present {
		return ""
	}
	name := k.text("for_each")
	if name == "" {
		return ""
	}
	if isForbiddenGroupByColumn(name) {
		k.reject("for_each", "integer column %q cannot be used as for_each", name)
		return ""
	}
	if isForbiddenForEachPath(name) || strings.HasPrefix(name, "event.") || !validEventPath(name) {
		k.reject("for_each", "expected a payload path without the event. prefix or reserved column, got %q", name)
		return ""
	}
	return name
}

func loadBroadcastEvents(k keys) []string {
	if _, present := k.values["broadcast_events"]; !present {
		return nil
	}
	events := k.strings("broadcast_events", true)
	seen := make(map[string]bool, len(events))
	for _, kind := range events {
		if kind == "" {
			continue
		}
		if seen[kind] {
			k.reject("broadcast_events", "duplicate event kind %q", kind)
		}
		seen[kind] = true
	}
	return events
}

func validateBroadcastEvents(when keys, r Rule) {
	if len(r.When.BroadcastEvents) == 0 {
		return
	}
	if len(r.Reads.Events) == 0 {
		when.reject("broadcast_events", "requires reads.events")
	}
	if r.When.GroupBy == "global" {
		when.reject("broadcast_events", "cannot be used with group_by = \"global\"")
	}
	if r.When.Reset == nil && r.When.ClearWhen == nil {
		when.reject("broadcast_events", "requires reset or clear_when")
	}
	for _, kind := range r.When.BroadcastEvents {
		if kind != "" && !slices.Contains(r.Reads.Events, kind) {
			when.reject("broadcast_events", "event kind %q must appear in reads.events", kind)
		}
	}
	if len(r.Reads.Events) > 0 {
		hasNonBroadcast := false
		for _, kind := range r.Reads.Events {
			if !slices.Contains(r.When.BroadcastEvents, kind) {
				hasNonBroadcast = true
				break
			}
		}
		if !hasNonBroadcast {
			when.reject("broadcast_events", "reads.events must retain at least one non-broadcast event kind")
		}
	}
}

func (k keys) positiveInteger(key string) int64 {
	n := requiredValue[int64](k, key, "an integer of at least 1")
	if n < 1 {
		k.reject(key, "expected an integer of at least 1")
	}
	return n
}

func (k keys) duration(key string) time.Duration {
	d := parseWindow(k.text(key))
	if d <= 0 {
		k.reject(key, "expected a positive whole-number duration in s, m or h")
	}
	return d
}

// ParseWindow parses a positive whole-number duration in s, m, or h.
func ParseWindow(s string) time.Duration {
	return parseWindow(s)
}

func parseWindow(s string) time.Duration {
	if len(s) < 2 || !strings.ContainsRune("smh", rune(s[len(s)-1])) {
		return 0
	}
	for i := 0; i < len(s)-1; i++ {
		if !digit(s[i]) {
			return 0
		}
	}
	d, _ := time.ParseDuration(s)
	return d
}

func loadPairs(k keys) [][2]string {
	raw := requiredValue[[]any](k, "pairs", "a list of pairs")
	if len(raw) == 0 {
		k.reject("pairs", "expected at least one pair")
	}
	pairs := make([][2]string, 0, len(raw))
	for i, value := range raw {
		pair, ok := value.([]any)
		if !ok || len(pair) != 2 {
			k.reject("pairs", "entry %d must be a list of two different strings", i)
			continue
		}
		a, aOK := pair[0].(string)
		b, bOK := pair[1].(string)
		if !aOK || !bOK || a == b {
			k.reject("pairs", "entry %d must be a list of two different strings", i)
		}
		pairs = append(pairs, [2]string{a, b})
	}
	return pairs
}
