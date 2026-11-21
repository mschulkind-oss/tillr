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
	Reads               Reads
	When                When
	Guidance            Guidance
	Delivery            Delivery
}

type Reads struct {
	Events, Projections []string
}

type When struct {
	GroupBy, Pattern, Field string
	Pairs                   [][2]string
	Threshold               int64
	Window                  time.Duration
	Match, Reset, ClearWhen *Expression
}

type Guidance struct {
	Summary   string
	Check, Do []string
}

type Delivery struct {
	Receiver   string
	AlsoNotify []string
}

// UnevaluatedReason reports a first-build capability gap, not an invalid
// rule. Callers separately honor Enabled, which is an operator choice.
func (r Rule) UnevaluatedReason() string {
	switch r.Scope {
	case "core", "workflow:*", "charter:*":
	default:
		return "named scope is not evaluated by this build"
	}
	switch r.When.Pattern {
	case LagBehind, DurationWithoutEvent:
		return "unsupported pattern " + r.When.Pattern
	}
	if len(r.Reads.Projections) != 0 {
		return "reads projections"
	}
	for _, e := range []*Expression{r.When.Match, r.When.Reset, r.When.ClearWhen} {
		if e != nil && !e.Supported() {
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
	kinds := make(map[string]bool)
	for _, kind := range event.Kinds() {
		kinds[kind.Name] = true
	}
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
		var doc map[string]any
		if err := toml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for _, key := range slices.Sorted(maps.Keys(doc)) {
			if key != "rule" {
				return fmt.Errorf("%s: %s: unknown top-level key", path, key)
			}
		}
		raw, present := doc["rule"]
		if !present {
			return nil
		}
		tables, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%s: rule: expected [[rule]] entries", path)
		}
		for i, raw := range tables {
			table, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: rule[%d]: expected a rule table", path, i)
			}
			id, _ := table["id"].(string)
			c := &ruleCheck{path: path, id: id, index: i}
			rule, err := c.load(table, kinds)
			if err != nil {
				return err
			}
			if previous, ok := seen[id]; ok {
				return c.refusal("id", "duplicate rule ID (first in %s)", previous)
			}
			seen[id] = path
			rules = append(rules, rule)
		}
		return nil
	})
	if err != nil {
		return nil, err
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
	// A quoted TOML value names JSON keys, not expression identifiers.
	for part := range strings.SplitSeq(s, ".") {
		if part == "" {
			k.reject(key, "expected an event column or a dotted JSON key path with no empty segments")
			break
		}
	}
	return s
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
	root.only("id", "enabled", "scope", "severity", "reads", "when", "guidance", "delivery")
	r := Rule{ID: root.text("id"), Enabled: true, Scope: root.text("scope"), Severity: root.text("severity")}
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
	r.Reads = Reads{Events: reads.strings("events", true), Projections: reads.strings("projections", false)}
	for _, kind := range r.Reads.Events {
		if !kinds[kind] {
			reads.reject("events", "unregistered event kind %q", kind)
		}
	}
	r.When = loadWhen(root.table("when"))
	g := root.table("guidance")
	g.only("summary", "check", "do")
	r.Guidance = Guidance{Summary: g.text("summary"), Check: g.strings("check", true), Do: g.strings("do", true)}
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

func loadWhen(k keys) When {
	w := When{GroupBy: k.eventPath("group_by"), Pattern: k.text("pattern"), ClearWhen: k.expression("clear_when", false)}
	allowed := []string{"group_by", "pattern", "clear_when", "window"}
	switch w.Pattern {
	case LagBehind, DurationWithoutEvent:
		// Other pattern keys have no contract yet. Do not interpret them.
		return w
	case PredicateMatch:
		k.only(append(allowed, "match")...)
		w.Threshold = 1
		w.Match = k.expression("match", false)
	case CountInWindow:
		k.only(append(allowed, "match", "threshold")...)
		w.Match = k.expression("match", false)
	case ConsecutiveCount:
		k.only(append(allowed, "match", "reset", "threshold")...)
		w.Match = k.expression("match", true)
		w.Reset = k.expression("reset", false)
	case AlternatingPair:
		k.only(append(allowed, "field", "pairs", "threshold")...)
		w.Field = k.eventPath("field")
		w.Pairs = loadPairs(k)
	default:
		k.reject("pattern", "unknown pattern %q", w.Pattern)
		return w
	}
	if w.Pattern != PredicateMatch {
		w.Threshold = requiredValue[int64](k, "threshold", "an integer of at least 1")
		if w.Threshold < 1 {
			k.reject("threshold", "expected an integer of at least 1")
		}
	}
	window := k.text("window")
	w.Window = parseWindow(window)
	if w.Window <= 0 {
		k.reject("window", "expected a positive whole-number duration in s, m or h")
	}
	return w
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
