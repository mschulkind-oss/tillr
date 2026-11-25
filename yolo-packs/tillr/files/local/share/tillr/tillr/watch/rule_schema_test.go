package watch

import (
	"strings"
	"testing"
	"time"
)

func TestWatchLagSchema(t *testing.T) {
	register(t, "test.watch")
	lag := strings.NewReplacer(
		`events = ["test.watch"]`, `projections = ["git_refs:refs/heads/main,refs/tillr/green"]`,
		`group_by = "detail.item-id"`, `group_by = "global"`,
		"pattern = \"count_in_window\"\nthreshold = 1\nwindow = \"90s\"", "pattern = 'lag_behind'\nleader = 'refs/heads/main'\nfollower = 'refs/tillr/green'\nthreshold_commits = 3",
	).Replace(validRule)
	for _, tc := range []struct{ name, old, value, wantKey string }{
		{"commits-only", "", "", ""},
		{"duration-only", "threshold_commits = 3", "threshold_duration = '90s'", ""},
		{"both", "threshold_commits = 3", "threshold_commits = 3\nthreshold_duration = '90s'\nmode = 'all'", ""},
		{"empty-events", "[rule.reads]", "[rule.reads]\nevents = []", ""},
		{"unknown-kind", "[rule.reads]", "[rule.reads]\nevents = ['bogus']", "reads.events"},
		{"bad-ref-entry", "refs/tillr/green\"]", "refs/tillr/green,\"]", "reads.projections"},
		{"space-in-ref", "refs/tillr/green\"]", "refs/tillr/green blue\"]", "reads.projections"},
		{"revision-in-projection", "refs/tillr/green\"]", "refs/tillr/green~1\"]", "reads.projections"},
		{"revision-in-leader", "leader = 'refs/heads/main'", "leader = 'refs/heads/main~1'", "when.leader"},
		{"reflog-in-projection", "refs/tillr/green\"]", "refs/tillr/green@{1}\"]", "reads.projections"},
		{"lock-component", "refs/tillr/green\"]", "refs/tillr.lock/green\"]", "reads.projections"},
		{"empty-component", "refs/tillr/green\"]", "refs/tillr//green\"]", "reads.projections"},
		{"long-ref", "refs/tillr/green\"]", "refs/" + strings.Repeat("é", 126) + "\"]", "reads.projections"},
		{"unlisted", "follower = 'refs/tillr/green'", "follower = 'refs/heads/other'", "when.follower"},
		{"same", "follower = 'refs/tillr/green'", "follower = 'refs/heads/main'", "when.follower"},
		{"not-global", "group_by = \"global\"", "group_by = 'item_id'", "when.group_by"},
		{"no-constraints", "threshold_commits = 3", "", "when.threshold_commits"},
		{"zero-commits", "threshold_commits = 3", "threshold_commits = 0", "when.threshold_commits"},
		{"zero-duration", "threshold_commits = 3", "threshold_duration = '0s'", "when.threshold_duration"},
		{"mode-with-one", "threshold_commits = 3", "threshold_commits = 3\nmode = 'any'", "when.mode"},
		{"invalid-mode", "threshold_commits = 3", "threshold_commits = 3\nthreshold_duration = '90s'\nmode = 'or'", "when.mode"},
		{"no-event-expr", "threshold_commits = 3", "threshold_commits = 3\nclear_when = 'true'", "when.clear_when"},
		{"no-fingerprint", "threshold_commits = 3", "threshold_commits = 3\nfingerprint = ['step']", "when.fingerprint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules, err := LoadRuleDocument("test", []byte(strings.Replace(lag, tc.old, tc.value, 1)))
			if tc.wantKey != "" {
				if rules != nil || !hasError(err, "test:", tc.wantKey+":") {
					t.Fatalf("wanted %s refusal, got %v, %v", tc.wantKey, rules, err)
				}
				return
			}
			if err != nil || len(rules) != 1 {
				t.Fatalf("load: %v, %v", rules, err)
			}
			w := rules[0].When
			if w.Leader != "refs/heads/main" || w.Follower != "refs/tillr/green" || rules[0].evaluatorReason() != "" {
				t.Fatalf("unexpected lag rule: %+v", w)
			}
			if tc.name == "both" && (w.Mode != "all" || w.ThresholdDuration != 90*time.Second || w.ThresholdCommits != 3) || tc.name != "both" && w.Mode != "any" {
				t.Fatalf("unexpected threshold or mode: %+v", w)
			}
		})
	}
}

func TestWatchDurationAndProjectionSchema(t *testing.T) {
	register(t, "test.watch")
	base := strings.NewReplacer(
		`scope = "workflow:*"`, `scope = "core"`,
		"[rule.when]", "projections = ['items', 'charter', 'git_refs:refs/heads/main']\n[rule.when]",
		"pattern = \"count_in_window\"\nthreshold = 1", "pattern = 'duration_without_event'\nthreshold_duration = '20s'\nafter = 'age(event.ts) > 3 and not empty(item.seat)'\nmatch = 'item.state == charter.ready and git.ref(\"refs/heads/main\") == \"abc\"'\nclear_when = 'item.state in charter.finished'",
	).Replace(validRule)
	for _, tc := range []struct{ name, old, value, wantKey string }{
		{"valid", "", "", ""},
		{"missing-after", "after =", "unknown =", "when.unknown"},
		{"short-window", `window = "90s"`, `window = "20s"`, "when.window"},
		{"missing-duration", "threshold_duration = '20s'", "", "when.threshold_duration"},
		{"item-column", "item.seat", "item.title", "when.after"},
		{"charter-key", "charter.finished", "charter.hooks", "when.clear_when"},
		{"unlisted-ref", `git.ref("refs/heads/main")`, `git.ref("refs/heads/other")`, "when.match"},
		{"non-string-ref", `git.ref("refs/heads/main")`, `git.ref(event.detail.ref)`, "when.match"},
		{"ref-arity", `git.ref("refs/heads/main")`, `git.ref()`, "when.match"},
		{"age-arity", `age(event.ts)`, `age()`, "when.after"},
		{"missing-charter", "'charter', ", "", "when.match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules, err := LoadRuleDocument("test", []byte(strings.Replace(base, tc.old, tc.value, 1)))
			if tc.wantKey != "" {
				if rules != nil || !hasError(err, "test:", tc.wantKey+":") {
					t.Fatalf("wanted %s refusal, got %v, %v", tc.wantKey, rules, err)
				}
				return
			}
			if err != nil || len(rules) != 1 {
				t.Fatalf("load: %v, %v", rules, err)
			}
			w := rules[0].When
			if w.After == nil || w.Match == nil || w.ClearWhen == nil || w.ThresholdDuration != 20*time.Second || rules[0].evaluatorReason() != "" {
				t.Fatalf("unexpected duration rule: %+v", w)
			}
		})
	}
}

func TestWatchProjectionOnlyAndFixerProvedSchema(t *testing.T) {
	register(t, "test.watch")
	base := `[[rule]]
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
summary = "Fixer {{subject}} is still queued"
check = ["inspect {{subject}}"]
do = ["resolve {{subject}}"]
[rule.delivery]
receiver = "foreground"
`
	for _, tc := range []struct{ name, old, value, wantKey string }{
		{"valid", "", "", ""},
		{"empty-events", "[rule.reads]", "[rule.reads]\nevents = []", ""},
		{"missing-items", `"items", `, "", "reads.projections"},
		{"missing-fixer-checks", `"fixer_checks", `, "", "when.match"},
		{"missing-green-proof", `, "green_proof:refs/tillr/green"`, "", "when.match"},
		{"mismatched-green-proof", `green_proof:refs/tillr/green`, `green_proof:refs/heads/main`, "when.match"},
		{"bad-green-proof-ref", `green_proof:refs/tillr/green`, `green_proof:refs/tillr/green~1`, "reads.projections"},
		{"wrong-group-by", `group_by = "item_id"`, `group_by = "object_id"`, "when.group_by"},
		{"global-group-by", `group_by = "item_id"`, `group_by = "global"`, "when.group_by"},
		{"list-group-by", `group_by = "item_id"`, `group_by = ["item_id", "run_id"]`, "when.group_by"},
		{"missing-match", "match = 'item.id matches \"^fixer-main-\" and item.state == \"in-queue\" and item.fixer_proved(\"refs/tillr/green\")'\n", "", "when.match"},
		{"match-uses-event", `item.state == "in-queue"`, `event.kind == "test.watch"`, "when.match"},
		{"has-window", `pattern = "predicate_match"`, "pattern = \"predicate_match\"\nwindow = \"10m\"", "when.window"},
		{"has-clear-when", `pattern = "predicate_match"`, "pattern = \"predicate_match\"\nclear_when = \"item.state == 'closed'\"", "when.clear_when"},
		{"has-fingerprint", `pattern = "predicate_match"`, "pattern = \"predicate_match\"\nfingerprint = [\"item_id\"]", "when.fingerprint"},
		{"has-after", `pattern = "predicate_match"`, "pattern = \"predicate_match\"\nafter = \"true\"", "when.after"},
		{"has-reset", `pattern = "predicate_match"`, "pattern = \"predicate_match\"\nreset = \"true\"", "when.reset"},
		{"guidance-event-placeholder", `summary = "Fixer {{subject}} is still queued"`, `summary = "Fixer {{event.item_id}} is still queued"`, "guidance.summary"},
		{"item-id-without-items", `"items", `, "", "reads.projections"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules, err := LoadRuleDocument("test", []byte(strings.Replace(base, tc.old, tc.value, 1)))
			if tc.wantKey != "" {
				if rules != nil || !hasError(err, "test:", tc.wantKey+":") {
					t.Fatalf("wanted %s refusal, got %v, %v", tc.wantKey, rules, err)
				}
				return
			}
			if err != nil || len(rules) != 1 {
				t.Fatalf("load: %v, %v", rules, err)
			}
			if rules[0].evaluatorReason() != "" {
				t.Fatalf("unexpected evaluatorReason: %q", rules[0].evaluatorReason())
			}
			if refs := rules[0].greenProofRefs(); len(refs) != 1 || refs[0] != "refs/tillr/green" {
				t.Fatalf("greenProofRefs = %v", refs)
			}
		})
	}
}
