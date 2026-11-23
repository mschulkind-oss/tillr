package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readHeavyRecord(t *testing.T, root string) HeavyRun {
	t.Helper()
	dir, err := heavyDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "heavy.json"))
	if err != nil {
		t.Fatal(err)
	}
	var run HeavyRun
	if err := json.Unmarshal(raw, &run); err != nil {
		t.Fatal(err)
	}
	return run
}

func assertNoRevertReason(t *testing.T, root, log, checkID, wantFirstRed, wantReason, wantRefused string) {
	t.Helper()
	run := readHeavyRecord(t, root)
	var found *HeavyCheck
	for i := range run.Checks {
		if run.Checks[i].CheckID == checkID {
			found = &run.Checks[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("heavy.json missing check %q: %+v", checkID, run.Checks)
	}
	if found.FirstRedLanding != wantFirstRed {
		t.Fatalf("heavy.json %s first_red_landing=%q, want %q", checkID, found.FirstRedLanding, wantFirstRed)
	}
	if found.RevertedBy != "" {
		t.Fatalf("heavy.json %s reverted_by=%q, want empty", checkID, found.RevertedBy)
	}
	if found.RevertRefused != wantRefused {
		t.Fatalf("heavy.json %s revert_refused=%q, want %q", checkID, found.RevertRefused, wantRefused)
	}
	if !strings.Contains(found.Detail, wantReason) {
		t.Fatalf("heavy.json %s detail=%q does not contain %q", checkID, found.Detail, wantReason)
	}
	if strings.Contains(found.Detail, "\n") {
		t.Fatalf("heavy.json %s detail is not one line: %q", checkID, found.Detail)
	}
	matched := false
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, checkID) && strings.Contains(line, shortSHA(wantFirstRed)) && strings.Contains(line, wantReason) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("loop log has no single line with %q, %s and %q:\n%s", checkID, shortSHA(wantFirstRed), wantReason, log)
	}
}

func TestHeavyNoRevertReasonRecordedForEveryFixerPath(t *testing.T) {
	ctx := context.Background()

	t.Run("auto_revert_disabled", func(t *testing.T) {
		h := newRevertHarness(t, fileRed("a"))
		h.loop.gatePolicy.AutoRevert = false
		culprit := h.land("item-broke", "a", "broken\n")
		h.proof()
		if fixers := h.fixers(); len(fixers) != 1 || fixers[0] != TrunkFixerID(culprit) {
			t.Fatalf("fixers=%v, want [%s]", fixers, TrunkFixerID(culprit))
		}
		assertNoRevertReason(t, h.root, h.report.String(), "unit", culprit, "automatic revert is disabled", "")
	})

	t.Run("first_red_already_set", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		culprit := heavyCommit(t, root, "bad")
		var log strings.Builder
		var fixes []string
		runner := heavyFixture(t, root, func(_ context.Context, _ string, _ string) ([]HeavyCheck, error) {
			return []HeavyCheck{{CheckID: "unit", Result: "fail", FirstRedLanding: culprit}}, nil
		})
		runner.Progress = func(line string) { log.WriteString(line) }
		runner.Revert = func(context.Context, string, string, []HeavyCheck, HeavyReplay) (RevertOutcome, error) {
			t.Fatal("Revert should not be called when FirstRedLanding was already set")
			return RevertOutcome{}, nil
		}
		runner.Fix = func(_ context.Context, sha string, _ []HeavyCheck) error {
			fixes = append(fixes, sha)
			return nil
		}
		if _, err := runner.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if len(fixes) != 1 || fixes[0] != culprit {
			t.Fatalf("fixes=%v, want [%s]", fixes, culprit)
		}
		assertNoRevertReason(t, root, log.String(), "unit", culprit, "first red landing was already set before attribution", "")
	})

	t.Run("multiple_go_culprits", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		commitFile := func(name, content string) string {
			if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			heavyGit(t, root, "add", name)
			heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", name)
			return heavyGit(t, root, "rev-parse", "HEAD")
		}
		first := commitFile("a", "bad1\n")
		second := commitFile("b", "bad2\n")
		const id = goTestPrefix + "tools/tillr-drain:example/drain:TestBoth"
		var log strings.Builder
		var fixes []string
		runner := heavyFixture(t, root, func(_ context.Context, dir, _ string) ([]HeavyCheck, error) {
			_, errA := os.Stat(filepath.Join(dir, "a"))
			_, errB := os.Stat(filepath.Join(dir, "b"))
			res := "pass"
			if errA == nil || errB == nil {
				res = "fail"
			}
			return []HeavyCheck{{CheckID: id, Result: res}}, nil
		})
		runner.Progress = func(line string) { log.WriteString(line) }
		runner.Revert = func(context.Context, string, string, []HeavyCheck, HeavyReplay) (RevertOutcome, error) {
			t.Fatal("Revert should not be called when multiple Go culprits exist")
			return RevertOutcome{}, nil
		}
		runner.Fix = func(_ context.Context, sha string, _ []HeavyCheck) error {
			fixes = append(fixes, sha)
			return nil
		}
		if _, err := runner.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if len(fixes) != 1 || fixes[0] != second {
			t.Fatalf("fixes=%v, want [%s]", fixes, second)
		}
		assertNoRevertReason(t, root, log.String(), id, first, "multiple first-red commits for this test: "+first+", "+second, "")
	})

	t.Run("additional_go_culprit_no_verdict", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		commitFile := func(name, content string) string {
			if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			heavyGit(t, root, "add", name)
			heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", name)
			return heavyGit(t, root, "rev-parse", "HEAD")
		}
		first := commitFile("a", "bad1\n")
		tip := commitFile("b", "ok\n")
		const id = goTestPrefix + "tools/tillr-drain:example/drain:TestErr"
		var log strings.Builder
		var fixes []string
		runner := heavyFixture(t, root, func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
			if ctx.Value(heavyInverseKey{}) != nil {
				return nil, errors.New("inverse replay failed\nwith details")
			}
			_, errA := os.Stat(filepath.Join(dir, "a"))
			res := "pass"
			if errA == nil {
				res = "fail"
			}
			return []HeavyCheck{{CheckID: id, Result: res}}, nil
		})
		runner.Progress = func(line string) { log.WriteString(line) }
		runner.Revert = func(context.Context, string, string, []HeavyCheck, HeavyReplay) (RevertOutcome, error) {
			t.Fatal("Revert should not be called when additional attribution gave no verdict")
			return RevertOutcome{}, nil
		}
		runner.Fix = func(_ context.Context, sha string, _ []HeavyCheck) error {
			fixes = append(fixes, sha)
			return nil
		}
		if _, err := runner.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if len(fixes) != 1 || fixes[0] != tip {
			t.Fatalf("fixes=%v, want [%s]", fixes, tip)
		}
		assertNoRevertReason(t, root, log.String(), id, first, "additional attribution gave no verdict: inverse replay failed with details", "")
	})

	t.Run("no_landing_record", func(t *testing.T) {
		h := newRevertHarness(t, fileRed("a"))
		if err := os.WriteFile(filepath.Join(h.root, "a"), []byte("unrecorded\n"), 0644); err != nil {
			t.Fatal(err)
		}
		heavyGit(t, h.root, "add", "a")
		heavyGit(t, h.root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "unrecorded")
		unrecorded := heavyGit(t, h.root, "rev-parse", "HEAD")
		h.proof()
		if fixers := h.fixers(); len(fixers) != 1 || fixers[0] != TrunkFixerID(unrecorded) {
			t.Fatalf("fixers=%v, want [%s]", fixers, TrunkFixerID(unrecorded))
		}
		assertNoRevertReason(t, h.root, h.report.String(), "unit", unrecorded, "has no landing record", "")
	})

	t.Run("rebased_landing", func(t *testing.T) {
		h := newRevertHarness(t, fileRed("a"))
		pre := h.land("item-rebased", "a", "broken\n")
		// Keep pre reachable as an orphan ref so rebasedLanding can inspect it, then
		// rewrite main with a new parent so the landed commit gets a different sha.
		heavyGit(t, h.root, "update-ref", "refs/tillr/pre-rebase-test", pre)
		heavyGit(t, h.root, "reset", "--hard", h.base)
		if err := os.WriteFile(filepath.Join(h.root, "other"), []byte("upstream\n"), 0644); err != nil {
			t.Fatal(err)
		}
		heavyGit(t, h.root, "add", "other")
		heavyGit(t, h.root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "upstream")
		heavyGit(t, h.root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "cherry-pick", pre)
		rebased := heavyGit(t, h.root, "rev-parse", "HEAD")
		h.proof()
		if fixers := h.fixers(); len(fixers) != 1 || fixers[0] != TrunkFixerID(rebased) {
			t.Fatalf("fixers=%v, want [%s]", fixers, TrunkFixerID(rebased))
		}
		assertNoRevertReason(t, h.root, h.report.String(), "unit", rebased, "a rebase of main changed the landed commit's sha", "")
	})

	t.Run("trunk_advanced_beyond_proved_commit", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		if err := os.WriteFile(filepath.Join(root, "a"), []byte("bad\n"), 0644); err != nil {
			t.Fatal(err)
		}
		heavyGit(t, root, "add", "a")
		heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "bad")
		culprit := heavyGit(t, root, "rev-parse", "HEAD")
		advanced := false
		var log strings.Builder
		var fixes []string
		var firstRun HeavyRun
		runner := heavyFixture(t, root, func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
			if only == "" && !advanced {
				advanced = true
				if err := os.WriteFile(filepath.Join(root, "unrelated"), []byte("later\n"), 0644); err != nil {
					t.Fatal(err)
				}
				heavyGit(t, root, "add", "unrelated")
				heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "unrelated")
			}
			_, errA := os.Stat(filepath.Join(dir, "a"))
			res := "pass"
			if errA == nil {
				res = "fail"
			}
			return []HeavyCheck{{CheckID: "unit", Result: res, Reads: []string{"a"}}}, nil
		})
		runner.Progress = func(line string) { log.WriteString(line) }
		runner.Revert = func(_ context.Context, tip, _ string, _ []HeavyCheck, _ HeavyReplay) (RevertOutcome, error) {
			if tip == culprit {
				t.Fatal("Revert should not be called once trunk moved beyond proved tip")
			}
			return RevertOutcome{Refused: "second run refusal"}, nil
		}
		runner.Fix = func(_ context.Context, sha string, _ []HeavyCheck) error {
			fixes = append(fixes, sha)
			return nil
		}
		runner.Report = func(run HeavyRun) {
			if run.TipSHA == culprit {
				firstRun = readHeavyRecord(t, root)
			}
		}
		if _, err := runner.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if len(fixes) == 0 || fixes[0] != culprit {
			t.Fatalf("fixes=%v, want first fixer against %s", fixes, culprit)
		}
		if len(firstRun.Checks) != 1 || firstRun.Checks[0].FirstRedLanding != culprit ||
			firstRun.Checks[0].RevertRefused != "trunk advanced beyond the proved commit" ||
			!strings.Contains(firstRun.Checks[0].Detail, "automatic revert not possible: trunk advanced beyond the proved commit") {
			t.Fatalf("first run heavy.json=%+v", firstRun.Checks)
		}
		if !strings.Contains(log.String(), "loop: heavy unit red from "+shortSHA(culprit)+": automatic revert not possible: trunk advanced beyond the proved commit; filing a fixer") {
			t.Fatalf("loop log missing tip-moved refusal:\n%s", log.String())
		}
	})

	t.Run("revert_callback_error", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		culprit := heavyCommit(t, root, "bad")
		var log strings.Builder
		var fixes []string
		runner := heavyFixture(t, root, func(_ context.Context, dir, _ string) ([]HeavyCheck, error) {
			raw, _ := os.ReadFile(filepath.Join(dir, "value"))
			res := "pass"
			if string(raw) == "bad" {
				res = "fail"
			}
			return []HeavyCheck{{CheckID: "unit", Result: res}}, nil
		})
		runner.Progress = func(line string) { log.WriteString(line) }
		runner.Revert = func(context.Context, string, string, []HeavyCheck, HeavyReplay) (RevertOutcome, error) {
			return RevertOutcome{}, errors.New("lock busy\nline two")
		}
		runner.Fix = func(_ context.Context, sha string, _ []HeavyCheck) error {
			fixes = append(fixes, sha)
			return nil
		}
		if _, err := runner.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if len(fixes) != 1 || fixes[0] != culprit {
			t.Fatalf("fixes=%v, want [%s]", fixes, culprit)
		}
		assertNoRevertReason(t, root, log.String(), "unit", culprit, "automatic revert not possible: the revert could not be attempted: lock busy line two", "the revert could not be attempted: lock busy\nline two")
	})

	t.Run("revert_empty_outcome", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		culprit := heavyCommit(t, root, "bad")
		var log strings.Builder
		var fixes []string
		runner := heavyFixture(t, root, func(_ context.Context, dir, _ string) ([]HeavyCheck, error) {
			raw, _ := os.ReadFile(filepath.Join(dir, "value"))
			res := "pass"
			if string(raw) == "bad" {
				res = "fail"
			}
			return []HeavyCheck{{CheckID: "unit", Result: res}}, nil
		})
		runner.Progress = func(line string) { log.WriteString(line) }
		runner.Revert = func(context.Context, string, string, []HeavyCheck, HeavyReplay) (RevertOutcome, error) {
			return RevertOutcome{}, nil
		}
		runner.Fix = func(_ context.Context, sha string, _ []HeavyCheck) error {
			fixes = append(fixes, sha)
			return nil
		}
		if _, err := runner.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if len(fixes) != 1 || fixes[0] != culprit {
			t.Fatalf("fixes=%v, want [%s]", fixes, culprit)
		}
		assertNoRevertReason(t, root, log.String(), "unit", culprit, "automatic revert not possible: the revert answered neither a landing nor a reason", "the revert answered neither a landing nor a reason")
	})

	for _, early := range []bool{false, true} {
		name := "revert_refused_batch"
		if early {
			name = "revert_refused_early"
		}
		t.Run(name, func(t *testing.T) {
			root, base := heavyRepo(t)
			heavyGit(t, root, "update-ref", GreenRef, base)
			culprit := heavyCommit(t, root, "bad")
			var log strings.Builder
			var fixes []string
			runner := heavyFixture(t, root, func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
				raw, _ := os.ReadFile(filepath.Join(dir, "value"))
				res := "pass"
				if string(raw) == "bad" {
					res = "fail"
				}
				check := HeavyCheck{CheckID: "unit", Result: res}
				if early && only == "" {
					heavyCheckEnded(ctx, check)
				}
				return []HeavyCheck{check}, nil
			})
			runner.Progress = func(line string) { log.WriteString(line) }
			runner.Revert = func(context.Context, string, string, []HeavyCheck, HeavyReplay) (RevertOutcome, error) {
				return RevertOutcome{Refused: "revert conflict on value"}, nil
			}
			runner.Fix = func(_ context.Context, sha string, _ []HeavyCheck) error {
				fixes = append(fixes, sha)
				return nil
			}
			if _, err := runner.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if len(fixes) != 1 || fixes[0] != culprit {
				t.Fatalf("fixes=%v, want [%s]", fixes, culprit)
			}
			assertNoRevertReason(t, root, log.String(), "unit", culprit, "automatic revert not possible: revert conflict on value", "revert conflict on value")
		})
	}

	t.Run("still_red_other_check", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		culprit := heavyCommit(t, root, "bad")
		var log strings.Builder
		fixed := map[string]string{}
		runner := heavyFixture(t, root, func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
			raw, _ := os.ReadFile(filepath.Join(dir, "value"))
			res := "pass"
			if string(raw) == "bad" {
				res = "fail"
			}
			if only != "" {
				return []HeavyCheck{{CheckID: only, Result: res}}, nil
			}
			return []HeavyCheck{
				{CheckID: "check-a", Result: res},
				{CheckID: "check-b", Result: res},
			}, nil
		})
		runner.Progress = func(line string) { log.WriteString(line) }
		runner.Revert = func(context.Context, string, string, []HeavyCheck, HeavyReplay) (RevertOutcome, error) {
			return RevertOutcome{Refused: "check-b still failed after revert", StillRed: true, stillRedCheck: "check-b"}, nil
		}
		runner.Fix = func(_ context.Context, sha string, checks []HeavyCheck) error {
			for _, c := range checks {
				fixed[c.CheckID] = sha
			}
			return nil
		}
		if _, err := runner.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if fixed["check-a"] != culprit {
			t.Fatalf("fixed=%v, want check-a against %s", fixed, culprit)
		}
		assertNoRevertReason(t, root, log.String(), "check-a", culprit, "automatic revert not possible: check-b still failed after revert", "check-b still failed after revert")
	})

	t.Run("still_red_retry_revised_refused", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		if err := os.WriteFile(filepath.Join(root, "a"), []byte("bad\n"), 0644); err != nil {
			t.Fatal(err)
		}
		heavyGit(t, root, "add", "a")
		heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "cause")
		actualCulprit := heavyGit(t, root, "rev-parse", "HEAD")
		if err := os.WriteFile(filepath.Join(root, "b"), []byte("later\n"), 0644); err != nil {
			t.Fatal(err)
		}
		heavyGit(t, root, "add", "b")
		heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "later")
		tip := heavyGit(t, root, "rev-parse", "HEAD")
		dir, err := heavyDirectory(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		// Seed a stale replay so initial attribution blames tip, then StillRed triggers fresh attribution to actualCulprit.
		if err := saveHeavyReplay(dir, actualCulprit, "unit", false); err != nil {
			t.Fatal(err)
		}
		var log strings.Builder
		var fixes []string
		runner := heavyFixture(t, root, fileRed("a"))
		runner.Progress = func(line string) { log.WriteString(line) }
		runner.Revert = func(_ context.Context, _, landing string, _ []HeavyCheck, _ HeavyReplay) (RevertOutcome, error) {
			if landing == tip {
				return RevertOutcome{Refused: "unit still failed after reverting " + shortSHA(tip), StillRed: true, stillRedCheck: "unit"}, nil
			}
			return RevertOutcome{Refused: "revised revert conflict"}, nil
		}
		runner.Fix = func(_ context.Context, sha string, _ []HeavyCheck) error {
			fixes = append(fixes, sha)
			return nil
		}
		if _, err := runner.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if len(fixes) != 1 || fixes[0] != actualCulprit {
			t.Fatalf("fixes=%v, want [%s]", fixes, actualCulprit)
		}
		assertNoRevertReason(t, root, log.String(), "unit", actualCulprit, "automatic revert not possible: revised revert conflict", "revised revert conflict")
	})
}
