package main_test

import (
	"context"
	"encoding/json"
	"github.com/mschulkind-oss/tillr/store"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sqlStrings(t *testing.T, db, query string) []string {
	t.Helper()
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var values []string
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var value *string
			if err := rows.Scan(&value); err != nil {
				return err
			}
			if value != nil {
				values = append(values, *value)
			}
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func judgeGit(t *testing.T, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", ref).CombinedOutput()
	if err != nil {
		t.Fatalf("git: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func judgeFixture(t *testing.T) (string, map[string]any) {
	t.Helper()
	db := filepath.Join(scratch(t, "judge"), "state.db")
	p := base("item_042", "judge intake")
	p.Criteria = []criterion{{CriterionID: "c1", Text: frozenText}, {CriterionID: "c2", Text: "second criterion"}}
	if code, out := apply(t, db, envelope(t, "item.record", "judge-item", p)); code != 0 {
		t.Fatalf("apply: %d %s", code, out)
	}
	if code, out := issueGrant(t, db, defaultGrant()); code != 0 {
		t.Fatalf("grant: %d %s", code, out)
	}
	entries := []any{}
	for _, c := range p.Criteria {
		entries = append(entries, map[string]any{"criterion_id": c.CriterionID, "criterion_hash": hashOf(c.Text), "claim_class": "verification-only", "verdict": "done", "reason": "tests pass", "evidence": []string{"commit:" + judgeGit(t, "HEAD")}})
	}
	return db, map[string]any{"evaluated_head": judgeGit(t, "HEAD"), "evaluated_tree": judgeGit(t, "HEAD^{tree}"), "outcome": "pass", "verdicts": entries}
}

func judgeRun(t *testing.T, db string, file map[string]any, extra ...string) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"judge", "--store", db, "--item", "item_042", "--input", input(t, string(raw)), "--head", judgeGit(t, "HEAD"), "--actor", "review:run-judge:session-judge:route", "--judged-actor", "write:run-writer:session-writer:route", "--grant", "grant_1", "--now", issuedAt}
	return run(t, append(args, extra...)...)
}

func TestJudgeIntakeRefusalsAreAtomic(t *testing.T) {
	cases := []struct {
		name, rule string
		mutate     func(map[string]any)
		extra      []string
	}{
		{"head", "evaluated-head", func(f map[string]any) { f["evaluated_head"] = "other" }, nil},
		{"tree", "evaluated-tree", func(f map[string]any) { f["evaluated_tree"] = "other" }, nil},
		{"missing", "criterion-missing", func(f map[string]any) { f["verdicts"] = f["verdicts"].([]any)[:1] }, nil},
		{"unknown", "criterion-unknown", func(f map[string]any) { f["verdicts"].([]any)[1].(map[string]any)["criterion_id"] = "cx" }, nil},
		{"duplicate", "criterion-duplicate", func(f map[string]any) { f["verdicts"].([]any)[1].(map[string]any)["criterion_id"] = "c1" }, nil},
		{"hash", "criterion-hash", func(f map[string]any) { f["verdicts"].([]any)[1].(map[string]any)["criterion_hash"] = "wrong" }, nil},
		{"reason", "verdict-reason", func(f map[string]any) { f["verdicts"].([]any)[1].(map[string]any)["reason"] = "  " }, nil},
		{"evidence", "verdict-evidence", func(f map[string]any) { f["verdicts"].([]any)[1].(map[string]any)["evidence"] = []string{} }, nil},
		{"blank-evidence", "verdict-evidence", func(f map[string]any) { f["verdicts"].([]any)[1].(map[string]any)["evidence"] = []string{" "} }, nil},
		{"class", "claim-class-mismatch", func(f map[string]any) { f["verdicts"].([]any)[1].(map[string]any)["claim_class"] = "direction" }, nil},
		{"false-pass", "outcome-pass", func(f map[string]any) { f["verdicts"].([]any)[1].(map[string]any)["verdict"] = "not-done" }, nil},
		{"actor", "actor-in-output", func(f map[string]any) { f["actor"] = nil }, nil},
		{"judged-actor", "actor-in-output", func(f map[string]any) { f["judged_actor"] = "" }, nil},
		{"entry-actor", "actor-in-output", func(f map[string]any) { f["verdicts"].([]any)[1].(map[string]any)["actor"] = "writer" }, nil},
		{"same-run", "self-judging", nil, []string{"--actor", "review:run-writer:session-other:route"}},
		{"same-session", "self-judging", nil, []string{"--actor", "review:run-other:session-writer:route"}},
		{"grant", "grant-absent", nil, []string{"--grant", "absent"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, file := judgeFixture(t)
			if tc.mutate != nil {
				tc.mutate(file)
			}
			code, out := judgeRun(t, db, file, tc.extra...)
			assertRefused(t, code, out, tc.rule)
			dryCode, dryOut := judgeRun(t, db, file, append(tc.extra, "--dry-run")...)
			assertRefused(t, dryCode, dryOut, tc.rule)
			if n := eventsOfKind(t, db, "item.verdict"); n != 0 {
				t.Fatalf("refusal wrote %d verdicts", n)
			}
			for _, c := range showItem(t, db, "item_042").Payload.Criteria {
				if c.State != "" {
					t.Fatalf("refusal changed %s to %s", c.CriterionID, c.State)
				}
			}
		})
	}
}

func TestJudgeIntakeRecordsAllAndLandedEvidence(t *testing.T) {
	db, file := judgeFixture(t)
	code, out := judgeRun(t, db, file, "--landed", judgeGit(t, "HEAD"))
	if code != 0 || strings.TrimSpace(string(out)) != "pass" {
		t.Fatalf("judge: %d %s", code, out)
	}
	if n := eventsOfKind(t, db, "item.verdict"); n != 2 {
		t.Fatalf("got %d verdicts", n)
	}
	for _, c := range showItem(t, db, "item_042").Payload.Criteria {
		if c.State != "done" {
			t.Fatalf("not retired: %+v", c)
		}
	}
	// Read the stored event payloads, not the command's outcome, for provenance.
	bodies := sqlStrings(t, db, `SELECT payload FROM events WHERE kind='item.verdict' ORDER BY seq`)
	for _, body := range bodies {
		var v verdictInput
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatal(err)
		}
		if v.Actor != "review:run-judge:session-judge:route" || v.JudgedActor != "write:run-writer:session-writer:route" || v.EvaluatedHead != judgeGit(t, "HEAD") || len(v.Landed) != 1 || v.Landed[0] != judgeGit(t, "HEAD") || len(v.Evidence) != 2 || !strings.Contains(strings.Join(v.Evidence, " "), "commit:"+judgeGit(t, "HEAD")) {
			t.Fatalf("bad provenance: %+v", v)
		}
	}
}

func judgeExec(t *testing.T, db, query string) {
	t.Helper()
	s, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error { _, err := tx.Exec(query); return err }); err != nil {
		t.Fatal(err)
	}
}

func TestJudgeWriteFailureRollsBackTheWholeFile(t *testing.T) {
	db, file := judgeFixture(t)
	judgeExec(t, db, `CREATE TRIGGER reject_second_verdict BEFORE INSERT ON events WHEN NEW.kind='item.verdict' AND json_extract(NEW.payload,'$.criterion_id')='c2' BEGIN SELECT RAISE(ABORT,'second verdict write failed'); END`)
	code, out := judgeRun(t, db, file)
	if code != 5 {
		t.Fatalf("got %d %s, want internal failure", code, out)
	}
	if n := eventsOfKind(t, db, "item.verdict"); n != 0 {
		t.Fatalf("partial batch: %d", n)
	}
	for _, c := range showItem(t, db, "item_042").Payload.Criteria {
		if c.State != "" {
			t.Fatalf("partial criterion: %+v", c)
		}
	}
}

func TestJudgeDryRunDoesNotMigrateLegacyStore(t *testing.T) {
	db, file := judgeFixture(t)
	judgeExec(t, db, `ALTER TABLE items DROP COLUMN judge_mode`)
	code, out := judgeRun(t, db, file, "--dry-run")
	if code != 0 || strings.TrimSpace(string(out)) != "pass" {
		t.Fatalf("legacy dry-run: %d %s", code, out)
	}
	if got := sqlStrings(t, db, `SELECT name FROM pragma_table_info('items') WHERE name='judge_mode'`); len(got) != 0 {
		t.Fatal("dry-run migrated store")
	}
}

func TestJudgeJSONAndOpaqueIndependence(t *testing.T) {
	for _, tc := range []struct{ actor, writer, rule string }{
		{"run=opaque-1;session=opaque-2", "run=opaque-1;session=opaque-3", "self-judging"},
		{"run=opaque-1;session=opaque-2", "run=opaque-3;session=opaque-2", "self-judging"},
		{"review:12345678-abcd-1234-abcd-123456789abc:route", "write:12345678-abcd-1234-abcd-123456789abc:route", "self-judging"},
		{"run=run-shared", "write:run-shared:route", "self-judging"},
		{"run=review;session=s1;route=one", "run=write;session=s2;route=one", ""},
		{"review:writer1:run-model", "write:writer2:run-model", ""},
		{"review:writer1:session-model", "write:writer2:session-model", ""},
		{"review:writer1:ambiguous:route", "write:writer2:route", "actor-provenance"},
	} {
		t.Run(tc.actor+tc.writer, func(t *testing.T) {
			db, file := judgeFixture(t)
			code, out := judgeRun(t, db, file, "--actor", tc.actor, "--judged-actor", tc.writer)
			if tc.rule != "" {
				assertRefused(t, code, out, tc.rule)
			} else if code != 0 {
				t.Fatalf("route equality refused: %d %s", code, out)
			}
		})
	}
	db, file := judgeFixture(t)
	for _, mutate := range []func(map[string]any){
		func(f map[string]any) { f["unexpected"] = true },
		func(f map[string]any) { f["outcome"] = "unknown" },
		func(f map[string]any) { f["verdicts"].([]any)[0].(map[string]any)["verdict"] = "unknown" },
	} {
		raw, _ := json.Marshal(file)
		var f map[string]any
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		mutate(f)
		code, out := judgeRun(t, db, f)
		if code != 1 {
			t.Fatalf("malformed output accepted: %d %s", code, out)
		}
	}
}

func TestJudgeModeSurvivesRerank(t *testing.T) {
	db := filepath.Join(scratch(t, "judge-rank"), "state.db")
	p := base("item-judge", "judge requested")
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var initial map[string]any
	if err := json.Unmarshal(raw, &initial); err != nil {
		t.Fatal(err)
	}
	initial["judge_mode"] = "agent"
	if code, out := apply(t, db, envelope(t, "item.record", "judge-request", initial)); code != 0 {
		t.Fatalf("apply: %d %s", code, out)
	}
	if code, out := rank(t, db, p.ItemID, "a"); code != 0 {
		t.Fatalf("rank: %d %s", code, out)
	}
	if got := sqlStrings(t, db, `SELECT judge_mode FROM items`); len(got) != 1 || got[0] != "agent" {
		t.Fatalf("rerank cleared judge: %v", got)
	}
}

func TestJudgeRefusesOpaqueStampedRuns(t *testing.T) {
	for _, tc := range []struct{ actor, writer string }{
		{"review:run_001:route-a", "write:run_001:route-b"},
		{"run=run_001;session=review", "write:run_001:route-b"},
		{"review:opaque:route-a", "opaque:route-b"},
	} {
		db, file := judgeFixture(t)
		code, out := judgeRun(t, db, file, "--actor", tc.actor, "--judged-actor", tc.writer)
		assertRefused(t, code, out, "self-judging")
	}
}

func TestJudgeRefusesMalformedJSON(t *testing.T) {
	db, file := judgeFixture(t)
	for _, tc := range []struct{ raw, rule string }{
		{`{"outcome":"pass","outcome":"fail"}`, "judge-output"},
		{`{"actor":null}`, "actor-in-output"},
		{`[]`, "judge-output"},
		{`{"evaluated_head":`, "judge-output"},
		{`{} {}`, "judge-output"},
	} {
		code, out := judgeRun(t, db, file, "--input", input(t, tc.raw))
		assertRefused(t, code, out, tc.rule)
	}
	if n := eventsOfKind(t, db, "item.verdict"); n != 0 {
		t.Fatal("malformed JSON wrote verdicts")
	}
}

func TestJudgeDryRunOutcomes(t *testing.T) {
	for _, tc := range []struct {
		states  []string
		outcome string
		code    int
	}{
		{[]string{"done", "done"}, "pass", 0}, {[]string{"blocked", "defective"}, "defective", 10}, {[]string{"not-done", "blocked"}, "blocked", 11}, {[]string{"done", "not-done"}, "not-done", 12},
	} {
		t.Run(tc.outcome, func(t *testing.T) {
			db, file := judgeFixture(t)
			if tc.outcome != "pass" {
				file["outcome"] = "fail"
			}
			for i, state := range tc.states {
				file["verdicts"].([]any)[i].(map[string]any)["verdict"] = state
			}
			before := sqlStrings(t, db, `SELECT sql FROM sqlite_master ORDER BY name`)
			code, out := judgeRun(t, db, file, "--dry-run")
			if code != tc.code || strings.TrimSpace(string(out)) != tc.outcome {
				t.Fatalf("got %d %s", code, out)
			}
			if n := eventsOfKind(t, db, "item.verdict"); n != 0 {
				t.Fatalf("dry-run wrote %d", n)
			}
			after := sqlStrings(t, db, `SELECT sql FROM sqlite_master ORDER BY name`)
			if strings.Join(before, "\n") != strings.Join(after, "\n") {
				t.Fatal("dry-run changed schema")
			}
		})
	}
	code, out := run(t, "judge", "--help")
	if code != 0 {
		t.Fatal(code)
	}
	for _, phrase := range []string{"10 defective", "11 blocked", "12 not-done", "0 pass"} {
		if !strings.Contains(string(out), phrase) {
			t.Fatalf("missing %q in help: %s", phrase, out)
		}
	}
}
