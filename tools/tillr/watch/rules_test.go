package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
)

const validRule = `[[rule]]
id = "case"
scope = "workflow:*"
severity = "warning"
[rule.reads]
events = ["test.watch"]
[rule.when]
group_by = "detail.item-id"
pattern = "count_in_window"
threshold = 1
window = "90s"
[rule.guidance]
summary = "Found {{count}}"
check = ["look"]
do = ["consider"]
[rule.delivery]
receiver = "foreground"
`

func put(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func register(t *testing.T, kinds ...string) {
	t.Helper()
	for _, kind := range kinds {
		if err := event.Register(kind, "watch-test"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWatchLoadRecursive(t *testing.T) {
	register(t, "test.watch")
	dir := t.TempDir()
	put(t, dir, "ignore.txt", "not TOML")
	put(t, dir, "a.toml", strings.Replace(validRule, "id = \"case\"", "id = \"off\"\nenabled = false", 1))
	path := put(t, dir, "nested/deep/b.toml", validRule)
	rules, err := LoadRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[0].Enabled || rules[0].UnevaluatedReason() != "" || !rules[1].Enabled || rules[1].ID != "case" || rules[1].When.Window != 90*time.Second || rules[1].When.Threshold != 1 || rules[1].UnevaluatedReason() != "" {
		t.Fatalf("unexpected rules: %+v", rules)
	}
	first := put(t, dir, "a.toml", validRule)
	if rules, err := LoadRules(dir); rules != nil || !hasError(err, path, `"case"`, "id", "duplicate rule ID (first in "+first+")") {
		t.Fatalf("duplicate: rules=%+v err=%v", rules, err)
	}
}

func TestWatchLoadDuplicateInOneFile(t *testing.T) {
	register(t, "test.watch")
	dir := t.TempDir()
	// The later invalid table must not mask the earlier duplicate.
	path := put(t, dir, "duplicate.toml", validRule+validRule+"[[rule]]\nid = 'invalid'\n")
	if rules, err := LoadRules(dir); rules != nil || !hasError(err, path, `"case"`, "id", "duplicate rule ID (first in "+path+")") {
		t.Fatalf("duplicate: rules=%+v err=%v", rules, err)
	}
}

func TestWatchLoadSymlinkRoot(t *testing.T) {
	register(t, "test.watch")
	dir := t.TempDir()
	path := put(t, dir, "rules/nested/rule.toml", validRule)
	link := filepath.Join(dir, "linked-rules")
	if err := os.Symlink("rules", link); err != nil {
		t.Fatal(err)
	}
	if rules, err := LoadRules(link); err != nil || len(rules) != 1 || rules[0].ID != "case" {
		t.Errorf("directory symlink: rules=%+v err=%v", rules, err)
	}
	fileLink := filepath.Join(dir, "linked-file")
	if err := os.Symlink(path, fileLink); err != nil {
		t.Fatal(err)
	}
	if rules, err := LoadRules(fileLink); rules != nil || !hasError(err, fileLink, "rules path must be a directory") {
		t.Errorf("file symlink: rules=%+v err=%v", rules, err)
	}
}

func TestWatchRuleIDByteLimit(t *testing.T) {
	register(t, "test.watch")
	for _, tc := range []struct {
		name, id string
		valid    bool
	}{
		{"ascii-128", strings.Repeat("x", 128), true},
		{"ascii-129", strings.Repeat("x", 129), false},
		{"utf8-128", strings.Repeat("é", 64), true},
		{"utf8-129", strings.Repeat("é", 64) + "x", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := put(t, dir, "id.toml", strings.Replace(validRule, `id = "case"`, `id = "`+tc.id+`"`, 1))
			rules, err := LoadRules(dir)
			if tc.valid {
				if err != nil || len(rules) != 1 || rules[0].ID != tc.id {
					t.Fatalf("valid ID: rules=%+v err=%v", rules, err)
				}
			} else if rules != nil || !hasError(err, path, `rule "`+tc.id+`"`, "id: ", "128 bytes") {
				t.Fatalf("oversized ID: rules=%+v err=%v", rules, err)
			}
		})
	}
}

func TestWatchDocumentedRules(t *testing.T) {
	// In a standalone core-module test, provide the registrations that the
	// linked owning utilities provide to the watch executable.
	register(t, "drain.node-advanced", "drain.refusal", "boundary.check", "land.landing", "drain.revert", "drain.fixer-proved")
	// The fixtures are the worked examples from Section 5.1. Keep them next
	// to the tests because the generated pack does not carry the design doc.
	dir := t.TempDir()
	for _, name := range []string{"step-oscillation", "unproved-trunk-lag"} {
		data, err := os.ReadFile("testdata/" + name + ".toml")
		if err != nil {
			t.Fatal(err)
		}
		put(t, dir, name+".toml", string(data))
	}
	rules, err := LoadRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[0].ID != "step-oscillation" || rules[0].Reads.Events[0] != "drain.node-advanced" || rules[0].When.Threshold != 4 || rules[1].ID != "unproved-trunk-lag" {
		t.Fatalf("unexpected examples: %+v", rules)
	}
	if rules[0].UnevaluatedReason() == "" || rules[1].UnevaluatedReason() != "named scope is not evaluated by this build" {
		t.Fatalf("example eligibility: %q, %q", rules[0].UnevaluatedReason(), rules[1].UnevaluatedReason())
	}
}

func TestWatchRuleRefusals(t *testing.T) {
	register(t, "test.watch")
	for _, tt := range []struct{ name, old, replacement, key string }{
		{"top-level", "[[rule]]", "other = 1\n[[rule]]", "other"},
		{"missing-id", `id = "case"`, "", "id"},
		{"missing-events", `events = ["test.watch"]`, "", "reads.events"},
		{"missing-receiver", `receiver = "foreground"`, "", "delivery.receiver"},
		{"wrong-projections-type", "[rule.when]", "projections = 'items'\n[rule.when]", "reads.projections"},
		{"unknown-when", "threshold = 1", "threshold = 1\nunknown = true", "when.unknown"},
		{"rule", "severity", "severty", "severty"},
		{"reads", "events", "eventz", "reads.eventz"},
		{"guidance", "summary", "sumary", "guidance.sumary"},
		{"delivery", "receiver", "reciever", "delivery.reciever"},
		{"wrong-pattern-key", "threshold = 1", "threshold = 1\nreset = 'true'", "when.reset"},
		{"remedy", "[[rule]]", "[[rule]]\nremedy = 'no'", "remedy"},
		{"nested-remedy", `pattern = "count_in_window"`, "pattern = 'lag_behind'\nopaque = [{nested={remedy=false}}]", "when.opaque[0].nested.remedy"},
		{"drain-receiver", `receiver = "foreground"`, `receiver = "drain:design"`, "delivery.receiver"},
		{"drain-notify", `receiver = "foreground"`, "receiver = 'foreground'\nalso_notify = ['subscribers', 'drain:build']", "delivery.also_notify"},
		{"other-receiver", `receiver = "foreground"`, `receiver = "build"`, "delivery.receiver"},
		{"wrong-notify-type", `receiver = "foreground"`, "receiver = 'foreground'\nalso_notify = [1]", "delivery.also_notify"},
		{"record-not-event", `"test.watch"`, `"drain.decision"`, "reads.events"},
		{"no-events", `["test.watch"]`, `[]`, "reads.events"},
		{"missing-scope", `scope = "workflow:*"`, "", "scope"},
		{"empty-scope-name", `scope = "workflow:*"`, `scope = "workflow:"`, "scope"},
		{"bad-severity", `severity = "warning"`, `severity = "critical"`, "severity"},
		{"bad-enabled", "[[rule]]", "[[rule]]\nenabled = 'true'", "enabled"},
		{"bad-group", `"detail.item-id"`, `"detail..item-id"`, "when.group_by"},
		{"bad-pattern", `"count_in_window"`, `"typo"`, "when.pattern"},
		{"missing-threshold", "threshold = 1", "", "when.threshold"},
		{"zero-threshold", "threshold = 1", "threshold = 0", "when.threshold"},
		{"float-threshold", "threshold = 1", "threshold = 1.0", "when.threshold"},
		{"missing-window", `window = "90s"`, "", "when.window"},
		{"zero-window", `"90s"`, `"0s"`, "when.window"},
		{"fraction-window", `"90s"`, `"0.5s"`, "when.window"},
		{"compound-window", `"90s"`, `"1h30m"`, "when.window"},
		{"overflow-window", `"90s"`, `"1000000000000h"`, "when.window"},
		{"no-match", `pattern = "count_in_window"`, `pattern = "consecutive_count"`, "when.match"},
		{"bad-match", `window = "90s"`, "window = '90s'\nmatch = 'event.kind =='", "when.match"},
		{"bad-unsupported-clear", `pattern = "count_in_window"`, "pattern = 'duration_without_event'\nclear_when = 'event.'", "when.clear_when"},
		{"missing-field", `pattern = "count_in_window"`, "pattern = 'alternating_pair'\npairs = [['a', 'b']]", "when.field"},
		{"missing-pairs", `pattern = "count_in_window"`, "pattern = 'alternating_pair'\nfield = 'seat'", "when.pairs"},
		{"undeclared-item", `window = "90s"`, "window = '90s'\nmatch = 'item.state == \"ready\"'", "when.match"},
		{"pair-identical", `pattern = "count_in_window"`, "pattern = 'alternating_pair'\nfield = 'seat'\npairs = [['a', 'a']]", "when.pairs"},
		{"empty-check", `["look"]`, `[]`, "guidance.check"},
		{"empty-do-member", `["consider"]`, `[""]`, "guidance.do"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := put(t, dir, "rule.toml", strings.Replace(validRule, tt.old, tt.replacement, 1))
			rules, err := LoadRules(dir)
			identity := `rule "case"`
			if tt.name == "missing-id" {
				identity = "rule[0]"
			} else if tt.name == "top-level" {
				identity = ""
			}
			if rules != nil || !hasError(err, path, identity, ": "+tt.key+":") {
				t.Fatalf("want refusal naming %s: rules=%+v err=%v", tt.key, rules, err)
			}
		})
	}
}

func TestWatchRuleEligibility(t *testing.T) {
	register(t, "test.watch")
	for _, tt := range []struct{ when, projections, reason string }{
		{"pattern = 'duration_without_event'\nafter = 'event.pass == false'\nthreshold_duration = '1m'\nwindow = '2m'", "", ""},
		{"pattern = 'predicate_match'\nwindow = '1m'", "[]", ""},
		{"pattern = 'predicate_match'\nwindow = '1m'", "['heavy_lock_state']", "unsupported projection \"heavy_lock_state\""},
		{"pattern = 'predicate_match'\nwindow = '1m'\nmatch = 'age(event.ts) > 1'", "", ""},
		{"pattern = 'predicate_match'\nwindow = '1m'\nclear_when = 'event.seat not in [\"a\"]'", "", ""},
		{"pattern = 'consecutive_count'\nthreshold = 2\nwindow = '1s'\nmatch = 'event.pass == false'\nreset = 'event.pass == true'", "", ""},
		{"pattern = 'alternating_pair'\nfield = 'seat'\npairs = [['a','b']]\nthreshold = 1\nwindow = '1h'", "", ""},
	} {
		dir := t.TempDir()
		text := strings.Replace(validRule, "pattern = \"count_in_window\"\nthreshold = 1\nwindow = \"90s\"", tt.when, 1)
		if tt.projections != "" {
			text = strings.Replace(text, "[rule.when]", "projections = "+tt.projections+"\n[rule.when]", 1)
		}
		put(t, dir, "rule.toml", text)
		rules, err := LoadRules(dir)
		if err != nil || len(rules) != 1 {
			t.Fatalf("%s: %v", tt.when, err)
		}
		reason := rules[0].evaluatorReason()
		if tt.reason == "" && reason != "" || !strings.Contains(reason, tt.reason) {
			t.Errorf("%s: reason %q, want %q", tt.when, reason, tt.reason)
		}
	}
}

func hasError(err error, parts ...string) bool {
	if err == nil {
		return false
	}
	for _, part := range parts {
		if !strings.Contains(err.Error(), part) {
			return false
		}
	}
	return true
}
