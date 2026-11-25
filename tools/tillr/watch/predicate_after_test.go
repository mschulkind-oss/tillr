package watch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func init() {
	for _, kind := range []string{"watch-test.after.start", "watch-test.after.gate", "watch-test.after.run", "watch-test.after.clear"} {
		if err := event.Register(kind, "watch-test"); err != nil {
			panic(err)
		}
	}
}

func TestPredicateMatchAfterAndResetSchemaAndLint(t *testing.T) {
	writeRuleFile := func(t *testing.T, body string) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "rule.toml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	lintRoot := t.TempDir()
	validTOML := `
[[rule]]
id = "ordered-match"
scope = "core"
severity = "warning"

[rule.reads]
events = ["watch-test.after.start", "watch-test.after.gate", "watch-test.after.run"]

[rule.when]
group_by = "item_id"
pattern = "predicate_match"
after = 'event.kind == "watch-test.after.start"'
reset = 'event.kind == "watch-test.after.gate"'
match = 'event.kind == "watch-test.after.run"'
window = "24h"

[rule.guidance]
summary = "Item {{subject}} ran {{event.run_id}} after hold."
check = ["inspect {{subject}}"]
do = ["fix {{subject}}"]

[rule.delivery]
receiver = "foreground"
`
	dir := writeRuleFile(t, validTOML)
	rules, err := LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules(valid): %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if !ruleCacheable(rules[0]) {
		t.Fatal("expected predicate_match with after/reset to remain cacheable")
	}
	findings, err := Lint(context.Background(), LintOptions{
		Root:      lintRoot,
		RulesPath: dir,
	})
	if err != nil {
		t.Fatalf("Lint(valid): %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 lint findings on valid rule, got %+v", findings)
	}

	// definitionHash changes when after or reset changes.
	dirDiffAfter := writeRuleFile(t, strings.Replace(validTOML, `event.kind == "watch-test.after.start"`, `event.kind == "watch-test.after.start" and event.kept == true`, 1))
	rulesDiffAfter, err := LoadRules(dirDiffAfter)
	if err != nil {
		t.Fatalf("LoadRules(diffAfter): %v", err)
	}
	if definitionHash(rules[0]) == definitionHash(rulesDiffAfter[0]) {
		t.Fatal("expected definitionHash to change when after changes")
	}
	dirDiffReset := writeRuleFile(t, strings.Replace(validTOML, `event.kind == "watch-test.after.gate"`, `event.kind in ["watch-test.after.gate"]`, 1))
	rulesDiffReset, err := LoadRules(dirDiffReset)
	if err != nil {
		t.Fatalf("LoadRules(diffReset): %v", err)
	}
	if definitionHash(rules[0]) == definitionHash(rulesDiffReset[0]) {
		t.Fatal("expected definitionHash to change when reset changes")
	}

	cases := []struct {
		name      string
		toml      string
		wantSub   string
		wantCodes []string
	}{
		{
			name: "reset without after",
			toml: `
[[rule]]
id = "reset-no-after"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.after.gate", "watch-test.after.run"]
[rule.when]
group_by = "item_id"
pattern = "predicate_match"
reset = 'event.kind == "watch-test.after.gate"'
match = 'event.kind == "watch-test.after.run"'
window = "1h"
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
			wantSub:   "when.reset: requires after on predicate_match",
			wantCodes: []string{CodeWatchLintRuleSchema},
		},
		{
			name: "after without match",
			toml: `
[[rule]]
id = "after-no-match"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.after.start"]
[rule.when]
group_by = "item_id"
pattern = "predicate_match"
after = 'event.kind == "watch-test.after.start"'
window = "1h"
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
			wantSub:   "when.match: required when after is set on predicate_match",
			wantCodes: []string{CodeWatchLintRuleSchema},
		},
		{
			name: "after on count_in_window still refused",
			toml: `
[[rule]]
id = "count-after"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.after.start", "watch-test.after.run"]
[rule.when]
group_by = "item_id"
pattern = "count_in_window"
after = 'event.kind == "watch-test.after.start"'
match = 'event.kind == "watch-test.after.run"'
threshold = 2
window = "1h"
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
			wantSub:   "when.after: unknown key",
			wantCodes: []string{CodeWatchLintRuleSchema},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := writeRuleFile(t, tc.toml)
			if _, err := LoadRules(d); err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("LoadRules(%s): got err=%v, want substring %q", tc.name, err, tc.wantSub)
			}
			got, err := Lint(context.Background(), LintOptions{
				Root:      lintRoot,
				RulesPath: d,
			})
			if err != nil {
				t.Fatalf("Lint(%s): %v", tc.name, err)
			}
			found := false
			for _, f := range got {
				if slices.Contains(tc.wantCodes, f.Code) && strings.Contains(f.Detail, tc.wantSub) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("Lint(%s): expected code %v with %q, got %+v", tc.name, tc.wantCodes, tc.wantSub, got)
			}
		})
	}
}

type sliceObservationSource struct {
	obs []Observation
}

func (s *sliceObservationSource) ReadWindow(_ context.Context, sinceTS int64, kinds []string) ([]Observation, SourceCursor, SkippedLines, error) {
	var out []Observation
	var maxSeq int64
	for _, o := range s.obs {
		if o.Seq > maxSeq {
			maxSeq = o.Seq
		}
		if o.TS >= sinceTS && slices.Contains(kinds, o.Kind) {
			out = append(out, o)
		}
	}
	return out, SourceCursor{Seq: maxSeq}, 0, nil
}

func (s *sliceObservationSource) Project(_ context.Context, _ []string) (map[string]any, error) {
	return nil, nil
}

func (s *sliceObservationSource) WatchTargets() ([]string, time.Duration) {
	return nil, 0
}

func TestPredicateMatchAfterAndResetEvalCacheAndSourceParity(t *testing.T) {
	dir := t.TempDir()
	ruleBody := `
[[rule]]
id = "ordered-rule"
scope = "core"
severity = "warning"

[rule.reads]
events = ["watch-test.after.start", "watch-test.after.gate", "watch-test.after.run", "watch-test.after.clear"]

[rule.when]
group_by = "item_id"
pattern = "predicate_match"
after = 'event.kind == "watch-test.after.start"'
reset = 'event.kind == "watch-test.after.gate"'
match = 'event.kind == "watch-test.after.run"'
clear_when = 'event.kind == "watch-test.after.clear"'
window = "24h"

[rule.guidance]
summary = "Item {{subject}} ran {{event.run_id}}"
check = ["inspect {{subject}}"]
do = ["fix {{subject}}"]

[rule.delivery]
receiver = "foreground"
`
	if err := os.WriteFile(filepath.Join(dir, "ordered.toml"), []byte(ruleBody), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, err := LoadRules(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	evalStore := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	src := &sliceObservationSource{}
	memRules := slices.Clone(rules)
	memRules[0].Scope = "*"
	evalMem := Evaluator{Source: src, Rules: memRules, Clock: func() time.Time { return now }}

	// Establish initial baseline at seq 0.
	if _, err := evalStore.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := evalMem.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	var seq int64
	emit := func(kind, itemID, runID string) {
		t.Helper()
		now = now.Add(time.Minute)
		seq++
		fields := map[string]any{"item_id": itemID}
		if runID != "" {
			fields["run_id"] = runID
		}
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       kind,
				ObjectType: "item",
				ObjectID:   &itemID,
				At:         now,
				Payload:    raw,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		src.obs = append(src.obs, Observation{
			Seq:    seq,
			TS:     now.UnixMilli(),
			Kind:   kind,
			Fields: fields,
		})
	}

	assertParity := func(wantSubjects []string) []Notice {
		t.Helper()
		resStore, err := evalStore.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		resMem, err := evalMem.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(resStore) != 1 || resStore[0].Diagnostic() != "" || resStore[0].Skipped != 0 {
			t.Fatalf("unexpected store results: %+v", resStore)
		}
		if len(resMem) != 1 || resMem[0].Diagnostic() != "" || resMem[0].Skipped != 0 {
			t.Fatalf("unexpected source results: %+v", resMem)
		}
		nStore, err := evalStore.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		nMem, err := evalMem.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		gotStore := make([]string, len(nStore))
		for i, n := range nStore {
			gotStore[i] = n.Bundle.Subject
		}
		gotMem := make([]string, len(nMem))
		for i, n := range nMem {
			gotMem[i] = n.Bundle.Subject
		}
		slices.Sort(gotStore)
		slices.Sort(gotMem)
		if !slices.Equal(gotStore, wantSubjects) || !slices.Equal(gotMem, wantSubjects) {
			t.Fatalf("subjects mismatch: store=%v source=%v want=%v", gotStore, gotMem, wantSubjects)
		}
		return nStore
	}

	// 1. match without prior after does not fire.
	emit("watch-test.after.run", "item-unarmed", "run-1")
	assertParity(nil)

	// 2. after -> reset -> match does not fire.
	emit("watch-test.after.start", "item-reset", "")
	emit("watch-test.after.gate", "item-reset", "")
	emit("watch-test.after.run", "item-reset", "run-2")
	assertParity(nil)

	// 3. after -> match fires and populates {{event.run_id}}.
	emit("watch-test.after.start", "item-fired", "")
	emit("watch-test.after.run", "item-fired", "run-3")
	opened := assertParity([]string{"item-fired"})
	if !strings.Contains(opened[0].Bundle.Guidance.Summary, "run-3") {
		t.Fatalf("expected summary to include run-3, got %q", opened[0].Bundle.Guidance.Summary)
	}

	// 4. Subsequent reset does NOT clear the already-opened alert.
	emit("watch-test.after.gate", "item-fired", "")
	assertParity([]string{"item-fired"})

	// 5. clear_when clears the opened alert and disarms.
	emit("watch-test.after.clear", "item-fired", "")
	assertParity(nil)
}
