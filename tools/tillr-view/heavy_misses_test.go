package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestHeavyMissesReadsBoundLandingAndCheckEvidence(t *testing.T) {
	const historical = " reverted, so that landing does not explain the red; the fixer starts from the tip"
	for _, tc := range []struct {
		name, beforeSHA, afterSHA string
		stillRed, singleton       bool
	}{
		{"current", "the fast gate did not pass with the revert: fail; disproved still fails on the tip with ", " reverted, so the attribution did not explain the red", true, false},
		{"historical", "disproved still fails on the tip with ", historical, true, false},
		{"unknown check", "unrelated still fails on the tip with ", historical, false, false},
		{"unrelated check suffix", "the fast gate did not pass with the revert: fail; unrelated/disproved still fails on the tip with ", historical, false, false},
		{"singleton without a reason", "", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkBoundLandingAndCheckEvidence(t, tc.beforeSHA, tc.afterSHA, tc.stillRed, tc.singleton)
		})
	}
}

func checkBoundLandingAndCheckEvidence(t *testing.T, beforeSHA, afterSHA string, wantStillRed, singleton bool) {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "commit.gpgSign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(path string) {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(path), 0644); err != nil {
			t.Fatal(err)
		}
	}
	const module = "tools/tillr-drain"
	const mirror = "yolo-packs/tillr/files/local/share/tillr/tillr-drain"
	git("init", "-q")
	write(module + "/go.mod")
	write(mirror + "/go.mod")
	git("add", ".")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	write(module + "/with space\nand newline.go")
	write(mirror + "/with space\nand newline.go")
	write("notes.txt")
	git("add", ".")
	git("commit", "-qm", "landing")
	commit, tree := git("rev-parse", "HEAD"), git("rev-parse", "HEAD^{tree}")

	path := fixture(t)
	ctx := context.Background()
	db, err := store.Open(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	from := time.Date(2026, 11, 19, 15, 55, 0, 0, time.FixedZone("EST", -5*3600))
	to := from.Add(time.Hour)
	at := from.Add(time.Minute)
	var seq int64
	emit := func(kind, typ, id string, payload map[string]any, when time.Time) int64 {
		t.Helper()
		if err := event.Register(kind, "view-test"); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.WriteTx(ctx, func(tx *store.Tx) error {
			seq, err = tx.Append(event.Event{Kind: kind, ObjectType: typ, ObjectID: &id, Payload: raw, At: when})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return seq
	}
	verdict := func(id, run, baseSHA, treeSHA, result string, when time.Time) int64 {
		return emit("gate.verdict", "run", run, map[string]any{"verdict_id": id, "run_id": run, "base_sha": baseSHA, "tree_sha": treeSHA, "result": result}, when)
	}
	land := func(id, sha, result, skip string, when time.Time) int64 {
		return emit("land.landing", "item", "item-"+id, map[string]any{"landing_id": id, "item_id": "item-" + id, "run_id": "run-" + id, "verdict_id": "gate-" + id, "base_sha": base, "gated_tree": tree, "landed_tree": base, "trunk_before": base, "trunk_after": sha, "result": result, "gate_skipped_reason": skip}, when)
	}
	attach := func(sha string, when time.Time, checks ...string) int64 {
		return emit("drain.fixer-attached", "run", "fixer-main-reused", map[string]any{"red_sha": sha, "fixer_item": "fixer-main-reused", "failing_checks": checks, "allowed_paths": []string{"not-a-diff"}}, when)
	}
	prove := func(when time.Time, checks ...string) int64 {
		return emit("drain.fixer-proved", "run", "fixer-main-reused", map[string]any{"fixer_item": "fixer-main-reused", "green": commit, "checks": checks}, when)
	}
	undo := func(check, result string, stillRed bool) {
		emit("drain.revert", "landing", commit, map[string]any{"landing": commit, "checks": []string{check, check}, "revert": base, "result": result, "still_red": stillRed}, at)
	}

	gateSeq := verdict("gate-good", "run-good", base, tree, "pass-inherited", from.Add(-time.Minute))
	attach(commit, at.Add(time.Minute), "before-by-seq")
	landingSeq := land("good", commit, "landed", "", at)
	emit("land.landing", "item", "fixer-main-reused", map[string]any{
		"landing_id": "older-fixer", "item_id": "fixer-main-reused", "result": "landed", "trunk_after": strings.Repeat("0", 40),
	}, at)
	// Wall clocks can go backward. The observation at the lower bound is later by seq.
	logSeq := emit("drain.fixer-logs", "item", "fixer-main-reused", map[string]any{"red_sha": commit, "fixer_item": "fixer-main-reused", "check_logs": map[string]string{"fixed": "logs/heavy.log", "reverted": "logs/other.log"}}, from)
	attach(commit, at, "fixed", "pending", "reverted", "fixed")
	prove(at, "pending")
	attach(commit, at, "pending") // A new association is not covered by its predecessor's proof.
	proofSeq := prove(at, "fixed")
	prove(to, "pending") // At the exclusive upper bound.
	undo("reverted", "reverted", false)
	disprovedChecks, reason := []string{"offered", "disproved"}, beforeSHA+commit[:12]+afterSHA
	if singleton {
		disprovedChecks, reason = []string{"disproved"}, ""
	}
	disprovedSeq := emit("drain.revert", "landing", commit, map[string]any{
		"landing": commit, "checks": disprovedChecks, "result": "not-possible", "still_red": true, "reason": reason,
	}, at)
	offeredSeq := disprovedSeq
	if singleton {
		undo("offered", "not-possible", false)
		offeredSeq = seq
	}
	emit("drain.fixer-logs", "item", "item-repair", map[string]any{
		"red_sha": commit, "fixer_item": "item-repair", "check_logs": map[string]string{"repaired": "logs/repair.log"},
	}, at)
	land("repair", strings.Repeat("e", 40), "refused", "", at)
	repairCommit := strings.Repeat("f", 40)
	repairSeq := land("repair", repairCommit, "landed", "", at)
	emit("drain.refusal", "run", "run-good", map[string]any{"step": "trunk-already-red", "red_sha": commit, "failing_checks": []string{"acquitted"}}, at)
	attach(commit, to, "after-window")

	missing := strings.Repeat("a", 40)
	verdict("gate-missing-git", "run-missing-git", base, tree, "pass", at)
	land("missing-git", missing, "landed", "", at)
	attach(missing, at, "fixed") // Same fixer and check, but proof predates this association.
	for i, id := range []string{"wrong-run", "wrong-verdict", "wrong-base", "wrong-tree", "failed-gate", "unbound", "skipped", "late-gate", "outside-window", "refused", "no-landing"} {
		sha := strings.Repeat("123456789bc"[i:i+1], 40)
		switch id {
		case "wrong-run":
			verdict("gate-"+id, "run-other", base, tree, "pass", at)
		case "wrong-verdict":
			verdict("gate-other", "run-"+id, base, tree, "pass", at)
		case "failed-gate":
			verdict("gate-"+id, "run-"+id, base, tree, "fail", at)
		case "wrong-base":
			verdict("gate-"+id, "run-"+id, commit, tree, "pass", at)
		case "wrong-tree":
			verdict("gate-"+id, "run-"+id, base, base, "pass", at)
		case "unbound":
			verdict("gate-"+id, "run-"+id, "", "", "pass", at)
		case "late-gate", "no-landing":
		default:
			verdict("gate-"+id, "run-"+id, base, tree, "pass", at)
		}
		if id != "no-landing" {
			when, outcome, skip := from, "landed", ""
			if id == "outside-window" {
				when = from.Add(-time.Minute)
			}
			if id == "refused" {
				outcome = "refused"
			}
			if id == "skipped" {
				skip = "gate-skip: fixture"
			}
			land(id, sha, outcome, skip, when)
		}
		if id == "late-gate" {
			verdict("gate-"+id, "run-"+id, base, tree, "pass", at)
		}
		attach(sha, at, id)
	}
	attach(commit[:12], at, "short-name")

	code, out := runView(t, "heavy-misses", "--store", path, "--root", root, "--from", from.Format(time.RFC3339), "--to", to.Format(time.RFC3339))
	if code != 0 {
		t.Fatalf("heavy-misses exit = %d: %s", code, out)
	}
	env, err := record.Decode(out)
	if err != nil || env.Kind != "view.heavy-misses" {
		t.Fatalf("not a view.heavy-misses envelope: %s (%v)", out, err)
	}
	var report struct {
		Window struct {
			From time.Time `json:"from_inclusive"`
			To   time.Time `json:"to_exclusive"`
		} `json:"window"`
		Snapshot struct {
			Head int64 `json:"event_head_seq"`
		} `json:"snapshot"`
		Landings []struct {
			LandingID    string   `json:"landing_id"`
			TrunkAfter   string   `json:"trunk_after"`
			EventSeq     int64    `json:"event_seq"`
			GateEventSeq int64    `json:"gate_event_seq"`
			Paths        []string `json:"changed_paths"`
			PathsError   string   `json:"changed_paths_error"`
			Checks       []struct {
				Check       string `json:"check_id"`
				Attribution string `json:"attribution"`
				RevertedBy  string `json:"reverted_by"`
				Evidence    []struct {
					Seq      int64  `json:"seq"`
					Kind     string `json:"kind"`
					LogRef   string `json:"log_ref"`
					Revert   string `json:"revert_sha"`
					Landed   string `json:"landed_sha"`
					StillRed bool   `json:"still_red"`
					Green    string `json:"green_sha"`
				} `json:"evidence"`
				Fixers []struct {
					Status     string `json:"status"`
					LandingSeq int64  `json:"landing_seq"`
					LandedSHA  string `json:"landed_sha"`
					ProofSeq   int64  `json:"proof_seq"`
				} `json:"fixers"`
			} `json:"checks"`
		} `json:"landings"`
		ByCheck []struct {
			Key   string `json:"key"`
			Count int    `json:"landings"`
		} `json:"by_check"`
		ByModule []struct {
			Key   string `json:"key"`
			Count int    `json:"landings"`
		} `json:"by_module"`
		Coverage struct {
			Unmatched []struct {
				Reason string `json:"reason"`
			} `json:"unmatched"`
			Pending int `json:"pending_fixer_associations"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal(env.Payload, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Window.From.Equal(from) || !report.Window.To.Equal(to) || report.Snapshot.Head != seq {
		t.Fatalf("wrong observation window or snapshot: %+v %+v", report.Window, report.Snapshot)
	}
	if len(report.Landings) != 2 {
		t.Fatalf("want two landings: %s", out)
	}
	good, unavailable := report.Landings[0], report.Landings[1]
	if good.LandingID != "good" || good.TrunkAfter != commit || good.EventSeq != landingSeq || good.GateEventSeq != gateSeq || good.PathsError != "" || !reflect.DeepEqual(good.Paths, []string{"notes.txt", module + "/with space\nand newline.go", mirror + "/with space\nand newline.go"}) {
		t.Fatalf("landing and Git evidence: %+v", good)
	}
	if unavailable.Paths != nil || unavailable.PathsError == "" || len(unavailable.Checks) != 1 || len(unavailable.Checks[0].Fixers) != 1 || unavailable.Checks[0].Fixers[0].Status != "pending" {
		t.Fatalf("missing Git and proof evidence: %+v", unavailable)
	}
	disprovedAttribution := "offered"
	if wantStillRed {
		disprovedAttribution = "disproved"
	}
	want := []string{"disproved:" + disprovedAttribution, "fixed:named", "offered:offered", "pending:named", "repaired:named", "reverted:confirmed"}
	var got []string
	for _, c := range good.Checks {
		got = append(got, c.Check+":"+c.Attribution)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("checks = %v, want %v", got, want)
		if len(good.Checks) != len(want) {
			return
		}
	}
	disproved, fixed, offered, pending, repaired, reverted := good.Checks[0], good.Checks[1], good.Checks[2], good.Checks[3], good.Checks[4], good.Checks[5]
	if len(disproved.Evidence) != 1 || disproved.Evidence[0].Seq != disprovedSeq || disproved.Evidence[0].StillRed != wantStillRed || len(offered.Evidence) != 1 || offered.Evidence[0].Seq != offeredSeq || offered.Evidence[0].StillRed {
		t.Errorf("inverse evidence (want still_red=%t for disproved and false for offered): %+v %+v", wantStillRed, disproved, offered)
	}
	if len(fixed.Fixers) != 1 || fixed.Fixers[0].Status != "proved-green" || fixed.Fixers[0].LandedSHA != "" || fixed.Fixers[0].ProofSeq != proofSeq || len(fixed.Evidence) != 3 || fixed.Evidence[0].Seq != logSeq || fixed.Evidence[0].LogRef != "logs/heavy.log" || fixed.Evidence[1].Kind != "drain.fixer-attached" || fixed.Evidence[2].Seq != proofSeq || fixed.Evidence[2].Green != commit {
		t.Errorf("green rerun without a fixer landing: %+v", fixed)
	}
	if len(repaired.Fixers) != 1 || repaired.Fixers[0].Status != "repaired" || repaired.Fixers[0].LandingSeq != repairSeq || repaired.Fixers[0].LandedSHA != repairCommit || repaired.Fixers[0].ProofSeq != 0 || len(repaired.Evidence) != 2 || repaired.Evidence[1].Seq != repairSeq || repaired.Evidence[1].Kind != "land.landing" || repaired.Evidence[1].Landed != repairCommit {
		t.Errorf("landed fixer evidence: %+v", repaired)
	}
	if len(pending.Fixers) != 1 || pending.Fixers[0].Status != "pending" || len(pending.Evidence) != 3 || len(reverted.Fixers) != 1 || reverted.Fixers[0].Status != "pending" || len(reverted.Evidence) != 3 || reverted.Evidence[2].Revert != base || reverted.RevertedBy != base || good.Checks[0].RevertedBy != "" {
		t.Fatalf("pending and revert evidence: %+v %+v", pending, reverted)
	}
	if len(report.ByCheck) != 6 || report.ByCheck[1].Key != "fixed" || report.ByCheck[1].Count != 2 || report.ByCheck[5].Key != "reverted" || report.ByCheck[5].Count != 1 {
		t.Fatalf("by check: %+v", report.ByCheck)
	}
	if len(report.ByModule) != 3 || report.ByModule[0].Key != "(non-module)" || report.ByModule[1].Key != "(unknown)" || report.ByModule[2].Key != module || report.ByModule[2].Count != 1 {
		t.Fatalf("by module: %+v", report.ByModule)
	}
	want = []string{
		"no-prior-landing", // Association predates the landing by sequence.
		"no-bound-pass", "no-bound-pass", "no-bound-pass", "no-bound-pass", "no-bound-pass", "no-bound-pass",
		"gate-skipped", "no-bound-pass", "landing-outside-window", "no-prior-landing", "no-prior-landing", "not-full-sha",
	}
	got = nil
	for _, c := range report.Coverage.Unmatched {
		got = append(got, c.Reason)
	}
	if report.Coverage.Pending != 3 || !reflect.DeepEqual(got, want) {
		t.Fatalf("coverage = %+v, want reasons %v", report.Coverage, want)
	}
}

func TestHeavyMissesFixerCreationChecks(t *testing.T) {
	const (
		prefix    = "123456789abc"
		fixer     = "fixer-main-" + prefix
		nextHeavy = "), and the next heavy run advances refs/tillr/green"
	)
	sha := prefix + strings.Repeat("d", 28)
	collision := prefix + strings.Repeat("e", 28)
	other := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name, subjectType, subject, criterion, text                string
		logs, secondBefore, firstAfter, laterCollision, laterOther bool
		wantCreation                                               bool
		unmatched                                                  string
	}{
		{name: "partial logs", logs: true, wantCreation: true},
		{name: "no logs", wantCreation: true},
		{name: "wrong subject type", subjectType: "run"},
		{name: "wrong subject ID", subject: "fixer-main-aaaabbbbcccc"},
		{name: "wrong criterion ID", criterion: "different"},
		{name: "wrong criterion shape", text: "user wrote (red: fixed, no-kept-log)"},
		{name: "wrong prefix", text: "the full gate is green at aaaabbbbcccc (red: fixed, no-kept-log" + nextHeavy},
		{name: "ambiguous without corroboration", secondBefore: true, unmatched: "ambiguous-creation-prefix"},
		{name: "exact creation log disambiguates", secondBefore: true, logs: true, wantCreation: true},
		{name: "later attachment log does not disambiguate", secondBefore: true, laterCollision: true, unmatched: "ambiguous-creation-prefix"},
		{name: "later different SHA has only its own checks", laterOther: true, wantCreation: true},
		{name: "future landing with backdated clock", firstAfter: true, logs: true, unmatched: "no-prior-landing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			at := time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)
			path := fixture(t)
			db, err := store.Open(ctx, path, false)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			emit := func(kind, typ, id string, payload map[string]any, when time.Time) int64 {
				t.Helper()
				if err := event.Register(kind, "view-test"); err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				var seq int64
				if err := db.WriteTx(ctx, func(tx *store.Tx) error {
					seq, err = tx.Append(event.Event{Kind: kind, ObjectType: typ, ObjectID: &id, Payload: raw, At: when})
					return err
				}); err != nil {
					t.Fatal(err)
				}
				return seq
			}
			land := func(commit string) {
				base, tree := strings.Repeat("b", 40), strings.Repeat("c", 40)
				emit("gate.verdict", "run", commit, map[string]any{"verdict_id": commit, "run_id": commit, "base_sha": base, "tree_sha": tree, "result": "pass"}, at)
				emit("land.landing", "item", commit, map[string]any{"landing_id": commit, "item_id": commit, "run_id": commit, "verdict_id": commit, "base_sha": base, "gated_tree": tree, "trunk_before": base, "trunk_after": commit, "result": "landed"}, at.Add(-time.Second))
			}
			if !tc.firstAfter {
				land(sha)
			}
			if tc.secondBefore {
				land(collision)
			}
			if tc.laterOther {
				land(other)
			}
			typ, subject, criterion, text := tc.subjectType, tc.subject, tc.criterion, tc.text
			if typ == "" {
				typ = "item"
			}
			if subject == "" {
				subject = fixer
			}
			if criterion == "" {
				criterion = "trunk-green"
			}
			if text == "" {
				text = "the full gate is green at " + prefix + " (red: fixed, no-kept-log" + nextHeavy + ". Optional producer note."
			}
			created := emit("item.add", typ, subject, map[string]any{
				"item_id": fixer, "title": "fix the broken trunk at " + prefix, "seat": "build", "rank": "000", "state": "in-queue",
				"criteria": []map[string]string{{"criterion_id": criterion, "text": text, "state": "not-done"}},
			}, at)
			log := func(commit, check string) int64 {
				return emit("drain.fixer-logs", "item", fixer, map[string]any{"red_sha": commit, "fixer_item": fixer, "check_logs": map[string]string{check: "logs/kept.log"}}, at.Add(-time.Second))
			}
			var logSeq int64
			if tc.logs {
				logSeq = log(sha, "fixed")
			}
			if tc.firstAfter {
				land(sha)
			}
			attach := func(commit string) {
				check := "later"
				if commit == collision {
					check = "fixed"
				} // A matching log key alone does not corroborate creation.
				log(commit, check)
				emit("drain.fixer-attached", "run", fixer, map[string]any{"red_sha": commit, "fixer_item": fixer, "failing_checks": []string{check}}, at)
			}
			if tc.laterCollision {
				attach(collision)
			}
			if tc.laterOther {
				attach(other)
			}
			proof := emit("drain.fixer-proved", "run", fixer, map[string]any{"fixer_item": fixer, "green": strings.Repeat("0", 40), "checks": []string{"fixed", "no-kept-log"}}, at.Add(-time.Second))
			from, to := at.Add(-time.Minute).Format(time.RFC3339), at.Add(time.Minute).Format(time.RFC3339)
			code, out := runView(t, "heavy-misses", "--store", path, "--root", t.TempDir(), "--from", from, "--to", to)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, out)
			}
			env, err := record.Decode(out)
			if err != nil {
				t.Fatal(err)
			}
			type evidence struct {
				Seq    int64
				Kind   string
				LogRef string `json:"log_ref"`
			}
			var report struct {
				Landings []struct {
					SHA    string `json:"trunk_after"`
					Checks []struct {
						ID       string `json:"check_id"`
						Evidence []evidence
						Fixers   []struct {
							Status         string
							AssociationSeq int64 `json:"association_seq"`
						}
					}
				}
				ByCheck []struct {
					Key   string
					Count int `json:"landings"`
				} `json:"by_check"`
				ByModule []struct {
					Key   string
					Count int `json:"landings"`
				} `json:"by_module"`
				Coverage struct {
					Unmatched []struct {
						Evidence evidence
						Reason   string
						Checks   []string
						NamedSHA string `json:"named_sha"`
					}
				}
			}
			if err := json.Unmarshal(env.Payload, &report); err != nil {
				t.Fatal(err)
			}
			got, counts := map[string][]string{}, map[string]int{}
			for _, row := range report.Landings {
				for _, check := range row.Checks {
					got[row.SHA] = append(got[row.SHA], check.ID)
					if row.SHA != sha || !tc.wantCreation {
						continue
					}
					want := []evidence{{created, "item.add", ""}}
					if tc.logs && check.ID == "fixed" {
						want = append(want, evidence{logSeq, "drain.fixer-logs", "logs/kept.log"})
					}
					want = append(want, evidence{proof, "drain.fixer-proved", ""})
					if !reflect.DeepEqual(check.Evidence, want) || len(check.Fixers) != 1 || check.Fixers[0].Status != "proved-green" || check.Fixers[0].AssociationSeq != created {
						t.Errorf("creation, optional log and later proof: %+v want evidence %+v", check, want)
					}
				}
			}
			want := map[string][]string{}
			if tc.wantCreation {
				want[sha] = []string{"fixed", "no-kept-log"}
			}
			if tc.laterCollision {
				want[collision] = []string{"fixed"}
			}
			if tc.laterOther {
				want[other] = []string{"later"}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("checks by SHA = %v, want %v", got, want)
			}
			for _, c := range report.ByCheck {
				counts[c.Key] = c.Count
			}
			wantCounts := map[string]int{}
			for _, checks := range want {
				for _, check := range checks {
					wantCounts[check]++
				}
			}
			if !reflect.DeepEqual(counts, wantCounts) {
				t.Errorf("check summary = %v, want %v", counts, wantCounts)
			}
			if len(want) == 0 && len(report.ByModule) != 0 || len(want) != 0 && (len(report.ByModule) != 1 || report.ByModule[0].Key != "(unknown)" || report.ByModule[0].Count != len(want)) {
				t.Errorf("module summary = %v, want %d distinct landings", report.ByModule, len(want))
			}
			var unmatched string
			for _, miss := range report.Coverage.Unmatched {
				if miss.Evidence.Seq != created {
					continue
				}
				unmatched = miss.Reason
				if miss.NamedSHA != prefix || !reflect.DeepEqual(miss.Checks, []string{"fixed", "no-kept-log"}) {
					t.Errorf("unmatched creation lost its evidence: %+v", miss)
				}
			}
			if unmatched != tc.unmatched {
				t.Errorf("unmatched creation = %q, want %q", unmatched, tc.unmatched)
			}
		})
	}
}

func TestHeavyMissesRequiresAnOrderedOffsetWindowAndStore(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--store", "unused"},
		{"--store", "unused", "--from", "2026-11-19T15:55:00", "--to", "2026-11-19T22:00:00Z"},
		{"--store", "unused", "--from", "2026-11-19T15:55:00-05:00", "--to", "2026-11-19T20:55:00Z"},
		{"--store", "unused", "--from", "2026-11-19T15:55:00+24:00", "--to", "2026-11-19T22:00:00Z"},
	} {
		if code, _ := runView(t, append([]string{"heavy-misses"}, args...)...); code != record.ExitUsage {
			t.Errorf("run %v = %d, want usage error", args, code)
		}
	}
	if code, out := runView(t, "heavy-misses", "--help"); code != 0 || !strings.Contains(string(out), "RFC3339") {
		t.Fatalf("face help: exit %d, %s", code, out)
	}
}
