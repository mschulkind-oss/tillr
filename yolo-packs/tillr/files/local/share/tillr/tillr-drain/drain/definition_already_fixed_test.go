package drain

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestDefinitionAlreadyFixedParsing(t *testing.T) {
	claimCases := []struct {
		name     string
		evidence string
		want     bool
	}{
		{
			name:     "incident wording",
			evidence: "The reported failure is already fixed and deterministically tested on the base commit (022b2aaafc).",
			want:     true,
		},
		{
			name:     "present tense already fixes",
			evidence: "Commit 3ed0291d91 already fixes the reported failure and TestOperatorStopBackoff passes on base.",
			want:     true,
		},
		{
			name:     "fix already on base",
			evidence: "The requested fix is already on the base (3ed0291d91).",
			want:     true,
		},
		{
			name:     "already resolved",
			evidence: "Already resolved by 3ed0291d91.",
			want:     true,
		},
		{
			name:     "contradiction naming a commit",
			evidence: "Commit 3ed0291d91 removed the --legacy flag, so the request contradicts the tree.",
			want:     false,
		},
		{
			name:     "plan-wrong prefix excluded",
			evidence: "Plan-Wrong: already fixed in 3ed0291d91",
			want:     false,
		},
		{
			name:     "empty",
			evidence: "",
			want:     false,
		},
	}
	for _, tc := range claimCases {
		if got := evidenceClaimsAlreadyFixed(tc.evidence); got != tc.want {
			t.Errorf("evidenceClaimsAlreadyFixed(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}

	commitCases := []struct {
		name     string
		evidence string
		want     []string
	}{
		{
			name:     "parens and prose skipping item id",
			evidence: "item-request-3a0ae4fd31 is already fixed on base commit (022b2aaafc) by `3ed0291d91` and commit:3ED0291D91.",
			want:     []string{"022b2aaafc", "3ed0291d91"},
		},
		{
			name:     "comma separated",
			evidence: "abc1234,def5678",
			want:     []string{"abc1234", "def5678"},
		},
		{
			name:     "ignores underscored and too-short or too-long",
			evidence: "foo_abc1234 123456 " + strings.Repeat("a", 41),
			want:     nil,
		},
	}
	for _, tc := range commitCases {
		got := evidenceCommits(tc.evidence)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("evidenceCommits(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDefinitionObstacleAlreadyFixedResolvesElsewhere(t *testing.T) {
	ctx := context.Background()

	type testCase struct {
		name            string
		evidence        func(fixSHA, baseSHA, otherSHA string) string
		wantState       string
		wantResolvesVia func(fixSHA, baseSHA string) string
		wantReasonSub   func(fixSHA, baseSHA string) string
		wantDesignWait  bool
	}

	cases := []testCase{
		{
			name: "ancestor-fix-and-base",
			evidence: func(fixSHA, baseSHA, _ string) string {
				return "The reported failure is already fixed and deterministically tested on the base commit (" + shortSHA(baseSHA) + "); commit " + shortSHA(fixSHA) + " already fixes it and TestOperatorStopBackoff passes on the base."
			},
			wantState:       StateResolvedElsewhere,
			wantResolvesVia: func(fixSHA, _ string) string { return "commit:" + fixSHA },
			wantReasonSub:   func(fixSHA, _ string) string { return shortSHA(fixSHA) },
		},
		{
			name: "base-commit-only",
			evidence: func(_, baseSHA, _ string) string {
				return "The reported failure is already fixed and deterministically tested on the base commit (" + shortSHA(baseSHA) + ")."
			},
			wantState:       StateResolvedElsewhere,
			wantResolvesVia: func(_, baseSHA string) string { return "commit:" + baseSHA },
			wantReasonSub:   func(_, baseSHA string) string { return shortSHA(baseSHA) },
		},
		{
			name: "no-commit-named",
			evidence: func(_, _, _ string) string {
				return "The reported failure is already fixed on the base and TestOperatorStopBackoff passes."
			},
			wantState:      StateBlocked,
			wantDesignWait: true,
		},
		{
			name: "non-ancestor-commit",
			evidence: func(_, _, otherSHA string) string {
				return "Commit " + shortSHA(otherSHA) + " already fixes the reported failure and TestOperatorStopBackoff passes."
			},
			wantState:      StateBlocked,
			wantDesignWait: true,
		},
		{
			name: "ancestor-commit-not-already-fixed",
			evidence: func(fixSHA, _, _ string) string {
				return "Commit " + shortSHA(fixSHA) + " removed the --legacy flag, contradicting the request."
			},
			wantState:      StateBlocked,
			wantDesignWait: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, l, _ := workflowLoopFixture(t, "")
			l.cfg.Policy.Seats["design"] = "fixture"
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state='closed'`)
				return err
			})

			// Create a fixing commit on trunk, then a later base commit on trunk so
			// fixSHA is a strict ancestor of baseSHA. Also create a commit on a side
			// branch that is NOT an ancestor of baseSHA.
			if err := os.WriteFile(filepath.Join(f.root, "fix.txt"), []byte("fixed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, f.root, "add", "fix.txt")
			runGit(t, f.root, "commit", "-qm", "fix: resolve reported failure")
			fixSHA := runGit(t, f.root, "rev-parse", "HEAD")

			runGit(t, f.root, "checkout", "-qb", "side-branch", "HEAD~1")
			if err := os.WriteFile(filepath.Join(f.root, "side.txt"), []byte("unrelated\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, f.root, "add", "side.txt")
			runGit(t, f.root, "commit", "-qm", "side: not an ancestor of base")
			otherSHA := runGit(t, f.root, "rev-parse", "HEAD")

			runGit(t, f.root, "checkout", "-q", TrunkRef)
			if err := os.WriteFile(filepath.Join(f.root, "later.txt"), []byte("later\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, f.root, "add", "later.txt")
			runGit(t, f.root, "commit", "-qm", "chore: advance base past fix")
			baseSHA := runGit(t, f.root, "rev-parse", TrunkRef)

			id := fileFixtureRequestWith(t, f, "Fix TestOperatorStopBackoff failure seen in unrelated gate\n", "build", item.SingleWorkerWorkflow, requestFence, false)
			reportBody := "OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: " + tc.evidence(fixSHA, baseSHA, otherSHA) + "\nRESULT: blocked\n"
			steps := &obstacleRequestSteps{
				t: t, f: f,
				report: func(req run.Request) (string, *landingOffer) {
					return wrapExtractedReport(req.RunID, reportBody), nil
				},
			}
			l.cfg.Steps = steps

			result := &LoopResult{}
			for i := 0; i < 4 && len(steps.runs) == 0; i++ {
				if _, err := l.pass(ctx, result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			if len(steps.runs) != 1 {
				t.Fatalf("dispatched runs = %v, want 1 worker run", steps.runs)
			}

			var state, resolvesVia, reason, by string
			r, err := store.OpenRead(ctx, f.storePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.ReadTx(ctx, func(rd *store.Reader) error {
				return rd.QueryRow(`SELECT state, COALESCE(resolves_via,''), COALESCE(lifecycle_reason,''), COALESCE(lifecycle_by,'') FROM items WHERE item_id = ?`, id).Scan(&state, &resolvesVia, &reason, &by)
			}); err != nil {
				r.Close()
				t.Fatal(err)
			}
			r.Close()

			if state != tc.wantState {
				t.Fatalf("item state = %q, want %q (resolves_via=%q reason=%q)", state, tc.wantState, resolvesVia, reason)
			}
			if tc.wantDesignWait {
				if got := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id=? AND json_extract(payload,'$.waiting_on')='design'`, id); got != 1 {
					t.Fatalf("design-waiting item.conflict count = %d, want 1", got)
				}
				return
			}
			if got := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id=?`, id); got != 0 {
				t.Fatalf("item.conflict count = %d, want 0 when resolved elsewhere", got)
			}
			wantVia := tc.wantResolvesVia(fixSHA, baseSHA)
			if resolvesVia != wantVia {
				t.Fatalf("resolves_via = %q, want %q", resolvesVia, wantVia)
			}
			wantSub := tc.wantReasonSub(fixSHA, baseSHA)
			if !strings.Contains(reason, wantSub) || !strings.Contains(reason, "already fixed") {
				t.Fatalf("lifecycle_reason = %q, want it to mention %q and 'already fixed'", reason, wantSub)
			}
			if by != Utility {
				t.Fatalf("lifecycle_by = %q, want %q", by, Utility)
			}
		})
	}
}
