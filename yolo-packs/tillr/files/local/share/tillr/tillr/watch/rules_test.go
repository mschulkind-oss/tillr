package watch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
		{"pattern = 'predicate_match'\nwindow = '1m'", "['fixer_checks', 'green_proof:refs/tillr/green']", ""},
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

func TestWatchFixerCheckHelpers(t *testing.T) {
	text := "Fix trunk (red: unit-a, go-test:tools/tillr-drain:./drain:TestFoo, go-test, go-test/legacy)"
	got := fixerChecksFromText(text)
	want := []string{"unit-a", "go-test:tools/tillr-drain:./drain:TestFoo", "go-test", "go-test/legacy"}
	if !slices.Equal(got, want) {
		t.Fatalf("fixerChecksFromText = %v, want %v", got, want)
	}
	if empty := fixerChecksFromText("no list here"); len(empty) != 0 {
		t.Fatalf("expected empty checks, got %v", empty)
	}

	provable := provableChecks(got)
	wantProvable := []string{"unit-a", "go-test:tools/tillr-drain:./drain:TestFoo", "go-test/legacy"}
	if !slices.Equal(provable, wantProvable) {
		t.Fatalf("provableChecks = %v, want %v", provable, wantProvable)
	}

	if suite := goSuiteProofID("go-test:tools/tillr-drain:./drain:TestFoo"); suite != "go-tool/tools-tillr-drain-s-whole-suite-passes" {
		t.Fatalf("goSuiteProofID = %q", suite)
	}
	if suite := goSuiteProofID("unit-a"); suite != "" {
		t.Fatalf("expected empty suite for non-go-test, got %q", suite)
	}

	if !proofCoversChecks([]string{"unit-a", "go-tool/tools-tillr-drain-s-whole-suite-passes", "go-test/legacy"}, provable) {
		t.Fatal("expected exact + goSuiteProofID to cover")
	}
	if !proofCoversChecks([]string{"lint/markdown"}, []string{"lint"}) {
		t.Fatal("expected bare <kind>/ prefix match")
	}
	if proofCoversChecks([]string{"unit-a"}, nil) {
		t.Fatal("expected empty checks not to be covered")
	}
	if proofCoversChecks(nil, []string{"unit-a"}) {
		t.Fatal("expected empty passed not to cover")
	}
}

func TestWatchGreenProofAndFixerAncestryGit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Watch Test", "GIT_AUTHOR_EMAIL=watch@example.invalid",
			"GIT_COMMITTER_NAME=Watch Test", "GIT_COMMITTER_EMAIL=watch@example.invalid",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "--quiet", "-b", "main")
	runGit("commit", "--quiet", "--allow-empty", "-m", "c1")
	c1 := runGit("rev-parse", "HEAD")
	runGit("commit", "--quiet", "--allow-empty", "-m", "c2")
	c2 := runGit("rev-parse", "HEAD")
	runGit("commit", "--quiet", "--allow-empty", "-m", "c3")
	c3 := runGit("rev-parse", "HEAD")

	ruleText := `[[rule]]
id = "proved-queued"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["items", "fixer_checks", "green_proof:refs/tillr/green"]
[rule.when]
group_by = "item_id"
pattern = "predicate_match"
match = 'item.id matches "^fixer-main-" and item.state == "in-queue" and item.fixer_proved("refs/tillr/green")'
[rule.guidance]
summary = "Fixer {{subject}}"
check = ["inspect"]
do = ["resolve"]
[rule.delivery]
receiver = "foreground"
`
	rules, err := LoadRuleDocument("test", []byte(ruleText))
	if err != nil {
		t.Fatal(err)
	}
	var execCount int
	onExec := func([]string) { execCount++ }
	var cache gitCache

	// 1. Missing ref in valid git repo -> no recorded proof, Diagnostic() == "", zero git spawns.
	snap, err := readGit(ctx, root, rules, &cache, nil, onExec)
	if err != nil || snap.ruleError(rules[0]) != nil || snap.greenProofs["refs/tillr/green"].value.recorded {
		t.Fatalf("missing ref: snap=%+v ruleErr=%v err=%v", snap, snap.ruleError(rules[0]), err)
	}
	if execCount != 0 {
		t.Fatalf("expected 0 git spawns for missing ref, got %d", execCount)
	}

	// 2. Non-Git root -> unreadableProjection{"green_proof", ...}.
	nonGit := t.TempDir()
	var nonGitCache gitCache
	badSnap, err := readGit(ctx, nonGit, rules, &nonGitCache, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rErr := badSnap.ruleError(rules[0]); rErr == nil || !strings.Contains(rErr.Error(), "green_proof:") {
		t.Fatalf("expected green_proof unreadableProjection on non-Git root, got %v", rErr)
	}

	// 3. Ref present, proof file missing -> no recorded proof.
	runGit("update-ref", "refs/tillr/green", c2)
	snap, err = readGit(ctx, root, rules, &cache, nil, onExec)
	if err != nil || snap.ruleError(rules[0]) != nil || snap.greenProofs["refs/tillr/green"].value.recorded {
		t.Fatalf("missing proof file: snap=%+v ruleErr=%v err=%v", snap, snap.ruleError(rules[0]), err)
	}
	firstExec := execCount
	if firstExec == 0 {
		t.Fatal("expected initial peel of refs/tillr/green")
	}
	// Cached idle pass -> 0 spawns.
	snap, err = readGit(ctx, root, rules, &cache, nil, onExec)
	if err != nil || execCount != firstExec {
		t.Fatalf("idle pass spawned git: %d -> %d, err=%v", firstExec, execCount, err)
	}

	// 4. Fallback green.json with mismatched commit -> no recorded proof.
	gateDir := filepath.Join(root, ".git", "tillr-gate")
	put(t, gateDir, "green.json", `{"commit":"`+c1+`","proved_at":"2026-11-23T09:25:00Z","passed":["chk"]}`)
	snap, err = readGit(ctx, root, rules, &cache, nil, onExec)
	if err != nil || snap.ruleError(rules[0]) != nil || snap.greenProofs["refs/tillr/green"].value.recorded {
		t.Fatalf("mismatched green.json: snap=%+v ruleErr=%v err=%v", snap, snap.ruleError(rules[0]), err)
	}

	// 5. Fallback green.json with matching commit -> recorded proof.
	put(t, gateDir, "green.json", `{"commit":"`+c2+`","proved_at":"2026-11-23T09:25:00Z","passed":["chk"]}`)
	snap, err = readGit(ctx, root, rules, &cache, nil, onExec)
	if err != nil || snap.ruleError(rules[0]) != nil || !snap.greenProofs["refs/tillr/green"].value.recorded {
		t.Fatalf("matching green.json: snap=%+v ruleErr=%v err=%v", snap, snap.ruleError(rules[0]), err)
	}

	// 6. Primary green-<oid>.json overrides green.json; corrupt/mismatched fails with unreadableProjection.
	put(t, gateDir, "green-"+c2+".json", `{"commit":"`+c1+`","proved_at":"2026-11-23T09:25:00Z","passed":["chk"]}`)
	snap, err = readGit(ctx, root, rules, &cache, nil, onExec)
	if err != nil || snap.ruleError(rules[0]) == nil || !strings.Contains(snap.ruleError(rules[0]).Error(), "green_proof:") {
		t.Fatalf("expected unreadableProjection for mismatched green-<oid>.json, got %v", snap.ruleError(rules[0]))
	}
	put(t, gateDir, "green-"+c2+".json", `{"commit":"`+c2+`","proved_at":"not-rfc3339","passed":["chk"]}`)
	snap, err = readGit(ctx, root, rules, &cache, nil, onExec)
	if err != nil || snap.ruleError(rules[0]) == nil || !strings.Contains(snap.ruleError(rules[0]).Error(), "green_proof:") {
		t.Fatalf("expected unreadableProjection for bad proved_at, got %v", snap.ruleError(rules[0]))
	}
	put(t, gateDir, "green-"+c2+".json", `{"commit":"`+c2+`","proved_at":"2026-11-23T09:25:00Z","passed":["chk"]}`)
	snap, err = readGit(ctx, root, rules, &cache, nil, onExec)
	if err != nil || snap.ruleError(rules[0]) != nil || !snap.greenProofs["refs/tillr/green"].value.recorded {
		t.Fatalf("valid green-<oid>.json: %+v, ruleErr=%v, err=%v", snap, snap.ruleError(rules[0]), err)
	}

	// 7. Ancestry checks cached in gitCache: c1 <= c2 (true), c3 <= c2 (false), cached on repeat (0 spawns).
	if ok, err := cache.fixerAncestorOfGreen(ctx, root, c1[:12], c2, nil, onExec); err != nil || !ok {
		t.Fatalf("c1 ancestor of c2: ok=%v err=%v", ok, err)
	}
	if ok, err := cache.fixerAncestorOfGreen(ctx, root, c3[:12], c2, nil, onExec); err != nil || ok {
		t.Fatalf("c3 should not be ancestor of c2: ok=%v err=%v", ok, err)
	}
	afterAncestry := execCount
	if ok, err := cache.fixerAncestorOfGreen(ctx, root, c1[:12], c2, nil, onExec); err != nil || !ok {
		t.Fatalf("cached c1: ok=%v err=%v", ok, err)
	}
	if ok, err := cache.fixerAncestorOfGreen(ctx, root, c3[:12], c2, nil, onExec); err != nil || ok {
		t.Fatalf("cached c3: ok=%v err=%v", ok, err)
	}
	if execCount != afterAncestry {
		t.Fatalf("expected 0 git spawns on cached ancestry checks, got %d -> %d", afterAncestry, execCount)
	}
}
