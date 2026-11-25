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
	table map[string]any
}

type Reads struct {
	Events, Projections []string
}

type When struct {
	Fingerprint                                []string
	GroupBy                                    string
	GroupByList                                []string
	Pattern, Field, DistinctBy                 string
	Pairs                                      [][2]string
	Threshold, ThresholdCommits                int64
	Window, ThresholdDuration                  time.Duration
	Leader, Follower, Mode                     string
	Match, Reset, ClearWhen, After, ReopenWhen *Expression
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
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	kinds := registeredRuleKinds()
	var rules []Rule
	seen := make(map[string]string)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root && !entry.IsDir() {
			return fmt.Errorf("%s: rules path must be a directory", dir)
		}
		if entry.IsDir() || filepath.Ext(path) != ".toml" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		loaded, err := loadRuleDocument(path, data, kinds, seen)
		if err != nil {
			return err
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
		rules = append(rules, rule)
	}
	return rules, nil
}

type ruleCheck struct {
	path, id string
	index    int
	err      error
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
			reads.reject("events", "unregistered event kind %q", kind)
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
		if r.When.GroupBy != "item_id" || len(r.When.GroupByList) != 0 {
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
		if value != "foreground" && value != "subscribers" {
			d.reject(key, "receiver %q is not foreground or subscribers", value)
		}
	}
	checkReceiver("receiver", r.Delivery.Receiver)
	for _, value := range r.Delivery.AlsoNotify {
		checkReceiver("also_notify", value)
	}
	return r, c.err
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

func loadGroupBy(k keys) (string, []string) {
	raw, present := k.values["group_by"]
	if !present {
		k.reject("group_by", "expected a non-empty string or a non-empty list of strings")
		return "", nil
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			k.reject("group_by", "expected a non-empty string")
			return "", nil
		}
		if !validEventPath(v) {
			k.reject("group_by", "expected an event column or a dotted JSON key path with no empty segments")
			return "", nil
		}
		return v, nil
	case []any:
		if len(v) == 0 {
			k.reject("group_by", "expected a non-empty list of strings")
			return "", nil
		}
		list := make([]string, 0, len(v))
		seen := make(map[string]bool, len(v))
		for i, elem := range v {
			s, ok := elem.(string)
			if !ok || s == "" {
				k.reject("group_by", "invalid string at index %d", i)
				return "", nil
			}
			if s == "global" {
				k.reject("group_by", "global is not allowed in a group_by list")
				return "", nil
			}
			if !validEventPath(s) {
				k.reject("group_by", "expected an event column or a dotted JSON key path with no empty segments")
				return "", nil
			}
			if seen[s] {
				k.reject("group_by", "duplicate group_by name %q", s)
				return "", nil
			}
			seen[s] = true
			list = append(list, s)
		}
		if len(list) == 1 {
			return list[0], nil
		}
		return strings.Join(list, ", "), list
	default:
		k.reject("group_by", "expected a non-empty string or a non-empty list of strings")
		return "", nil
	}
}

func loadWhen(k keys, pattern string, refs map[string]bool, projectionOnly bool) When {
	w := When{Pattern: pattern}
	w.GroupBy, w.GroupByList = loadGroupBy(k)
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
	allowed := []string{"group_by", "pattern", "clear_when", "window", "fingerprint"}
	switch w.Pattern {
	case DurationWithoutEvent:
		k.only(append(allowed, "after", "match", "threshold_duration")...)
		w.After = k.expression("after", true)
		w.Match = k.expression("match", false)
		w.ThresholdDuration = k.duration("threshold_duration")
	case PredicateMatch:
		k.only(append(allowed, "match")...)
		w.Threshold = 1
		w.Match = k.expression("match", projectionOnly)
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
