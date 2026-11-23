package drain_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the real replay program, not just the replay outcome: only a pass
// for the exact commit and check should prevent a process from starting.
func TestGateRefusalUsesExactTrunkProof(t *testing.T) {
	for _, tc := range []struct {
		name, record, check, result string
		moveTip                     bool
		wantReplay                  string
		wantSource                  string
	}{
		{name: "base is tip", record: "base", check: "scratch", result: "pass", wantSource: "base proof record"},
		{name: "archived base", record: "base", check: "scratch", result: "pass", moveTip: true, wantSource: "base proof record"},
		{name: "repaired tip", record: "tip", check: "scratch", result: "pass", moveTip: true, wantReplay: "scratch", wantSource: "base replay; tip proof record"},
		{name: "different commit", record: "other", check: "scratch", result: "pass", wantReplay: "scratch", wantSource: "base replay"},
		{name: "different check", record: "base", check: "unrelated", result: "pass", wantReplay: "scratch", wantSource: "base replay"},
		{name: "red record", record: "base", check: "scratch", result: "fail", wantReplay: "scratch", wantSource: "base replay"},
		{name: "partial proof", record: "base", check: "scratch", result: "pass", wantReplay: "other-check", wantSource: "base proof record and replay"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
			fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
			older := git(t, f.root, "rev-parse", "main")
			git(t, f.root, "commit", "--allow-empty", "-qm", "candidate base")
			base := git(t, f.root, "rev-parse", "main")
			tip := base
			gateBody := failingTreeGateBody
			if tc.name == "partial proof" {
				gateBody = strings.Replace(gateBody, `"check_id":"scratch","result":"fail"}`, `"check_id":"scratch","result":"fail"},{"check_id":"other-check","result":"fail"}`, 1)
			}
			if tc.moveTip {
				git(t, f.root, "commit", "--allow-empty", "-qm", "advance trunk")
				tip = git(t, f.root, "rev-parse", "main")
				git(t, f.root, "reset", "--hard", base)
				gateBody = fmt.Sprintf("git -C %q update-ref refs/heads/main %s\ngit -C %q update-ref refs/tillr/green %s\n", f.root, tip, f.root, tip) + gateBody
			} else {
				git(t, f.root, "update-ref", "refs/tillr/green", base)
			}
			dir := filepath.Join(f.root, ".git", "tillr-gate")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			sha := map[string]string{"base": base, "tip": tip, "other": older}[tc.record]
			if tc.result == "pass" && tc.record == "base" {
				for _, proved := range []string{sha, tip} {
					writeFile(t, filepath.Join(dir, "green-"+proved+".json"), fmt.Sprintf(`{"commit":%q,"proved_at":"2026-11-19T15:45:00Z","passed":[%q]}`, proved, tc.check))
				}
			} else {
				writeFile(t, filepath.Join(dir, "heavy-progress.json"), fmt.Sprintf(`{"tip_sha":%q,"checks":{%q:{"check_id":%q,"result":%q}},"passed_at":{%q:"2026-11-19T15:45:00Z"}}`, sha, tc.check, tc.check, tc.result, tc.check))
			}
			steps := f.steps(t, stub(t, "gate", gateBody), always(t, "land", 0), always(t, "verdict", 0))
			replayLog := filepath.Join(dir, "replay.log")
			replayBody := fmt.Sprintf("rev=$(git rev-parse HEAD)\nprintf '%%s %%s\\n' \"$rev\" \"$*\" >> %q\n", replayLog)
			if tc.record == "tip" {
				replayBody += fmt.Sprintf("[ \"$rev\" != %s ]\n", base)
			}
			steps.ReplayProgram = stub(t, "replay", replayBody) + " {only}"
			var report strings.Builder
			result, err := f.newLoopWith(t, 1, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, &report).Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(replayLog)
			if tc.wantReplay != "" {
				for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
					if err != nil || line != base+" "+tc.wantReplay {
						t.Fatalf("wrong replay invocations: %q (%v)", raw, err)
					}
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("the proved check started a replay: %q (%v)", raw, err)
			}
			if result.TrunkAcquitted != 0 || refusalOf(t, f.store, "gate") == nil {
				t.Fatalf("expected ordinary gate refusal: %+v\n%s", result, &report)
			}
			if !strings.Contains(report.String(), "recording a refusal, not a verdict (trunk evidence: "+tc.wantSource+")") {
				t.Fatalf("refusal did not name %q:\n%s", tc.wantSource, &report)
			}
		})
	}
}

func TestGoAffectedCoveredByGreenTrunkProof(t *testing.T) {
	const goAffectedCheck = "go-affected/affected-tillr-go-test-packages-pass"
	allModuleChecks := []string{
		"go-substrate/tools-tillr-s-whole-suite-passes",
		"gate-runner/tools-tillr-gate-s-whole-suite-passes",
		"go-tool/tools-tillr-drain-s-whole-suite-passes",
		"go-tool/tools-tillr-view-s-whole-suite-passes",
	}
	withoutView := []string{
		"go-substrate/tools-tillr-s-whole-suite-passes",
		"gate-runner/tools-tillr-gate-s-whole-suite-passes",
		"go-tool/tools-tillr-drain-s-whole-suite-passes",
	}

	for _, tc := range []struct {
		name           string
		record         string
		passed         []string
		failingSummary string
		progressOnly   bool
		moveTip        bool
		wantReplay     string
		wantSource     string
	}{
		{
			name:       "all tillr module checks green on base",
			record:     "base",
			passed:     allModuleChecks,
			wantSource: "base proof record",
		},
		{
			name:       "all tillr module checks green on archived base",
			record:     "base",
			passed:     allModuleChecks,
			moveTip:    true,
			wantSource: "base proof record",
		},
		{
			name:       "repaired tip green proof covers go-affected",
			record:     "tip",
			passed:     allModuleChecks,
			moveTip:    true,
			wantReplay: goAffectedCheck,
			wantSource: "base replay; tip proof record",
		},
		{
			name:       "missing tillr module check without failing-package summary replays",
			record:     "base",
			passed:     withoutView,
			wantReplay: goAffectedCheck,
			wantSource: "base replay",
		},
		{
			name:           "failing package owned by green module is covered",
			record:         "base",
			passed:         withoutView,
			failingSummary: "       | go-affected: failing packages and tests:\n       |   github.com/mschulkind-oss/tillr-drain/drain: TestBroken\n  triage go-affected/affected-tillr-go-test-packages-pass: affected tillr Go test packages pass\n",
			wantSource:     "base proof record",
		},
		{
			name:           "failing package owned by unproved module replays",
			record:         "base",
			passed:         withoutView,
			failingSummary: "go-affected: failing packages and tests:\n  github.com/mschulkind-oss/tillr-view/view: TestBroken\n",
			wantReplay:     goAffectedCheck,
			wantSource:     "base replay",
		},
		{
			name:         "progress proof without green proof file replays",
			record:       "base",
			passed:       allModuleChecks,
			progressOnly: true,
			wantReplay:   goAffectedCheck,
			wantSource:   "base replay",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
			for modDir, modPath := range map[string]string{
				"tools/tillr":       "github.com/mschulkind-oss/tillr",
				"tools/tillr-gate":  "github.com/mschulkind-oss/tillr-gate",
				"tools/tillr-drain": "github.com/mschulkind-oss/tillr-drain",
				"tools/tillr-view":  "github.com/mschulkind-oss/tillr-view",
				"tools/pi-profiler": "github.com/mschulkind-oss/pi-profiler",
			} {
				if err := os.MkdirAll(filepath.Join(f.root, modDir), 0o755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(f.root, modDir, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26.0\n", modPath))
			}
			git(t, f.root, "add", "tools")
			git(t, f.root, "commit", "-qm", "seed tillr go modules")
			fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))

			base := git(t, f.root, "rev-parse", "main")
			tip := base
			gateBody := strings.Replace(failingTreeGateBody, `"check_id":"scratch"`, fmt.Sprintf(`"check_id":%q`, goAffectedCheck), 1)
			if tc.failingSummary != "" {
				gateBody = "cat <<'EOF_SUMMARY'\n" + tc.failingSummary + "EOF_SUMMARY\n" + gateBody
			}
			if tc.moveTip {
				git(t, f.root, "commit", "--allow-empty", "-qm", "advance trunk")
				tip = git(t, f.root, "rev-parse", "main")
				git(t, f.root, "reset", "--hard", base)
				gateBody = fmt.Sprintf("git -C %q update-ref refs/heads/main %s\ngit -C %q update-ref refs/tillr/green %s\n", f.root, tip, f.root, tip) + gateBody
			} else {
				git(t, f.root, "update-ref", "refs/tillr/green", base)
			}

			dir := filepath.Join(f.root, ".git", "tillr-gate")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			passedJSON, err := json.Marshal(tc.passed)
			if err != nil {
				t.Fatal(err)
			}
			if tc.progressOnly {
				checksMap := map[string]map[string]string{}
				passedAt := map[string]string{}
				for _, id := range tc.passed {
					checksMap[id] = map[string]string{"check_id": id, "result": "pass"}
					passedAt[id] = "2026-11-22T15:45:00Z"
				}
				progressRaw, err := json.Marshal(map[string]any{
					"tip_sha":   base,
					"checks":    checksMap,
					"passed_at": passedAt,
				})
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(dir, "heavy-progress.json"), string(progressRaw))
			} else {
				var provedCommits []string
				switch tc.record {
				case "base":
					provedCommits = []string{base, tip}
				case "tip":
					provedCommits = []string{tip}
				}
				for _, proved := range provedCommits {
					writeFile(t, filepath.Join(dir, "green-"+proved+".json"), fmt.Sprintf(`{"commit":%q,"proved_at":"2026-11-22T15:45:00Z","passed":%s}`, proved, passedJSON))
				}
			}

			steps := f.steps(t, stub(t, "gate", gateBody), always(t, "land", 0), always(t, "verdict", 0))
			replayLog := filepath.Join(dir, "replay.log")
			replayBody := fmt.Sprintf("rev=$(git rev-parse HEAD)\nprintf '%%s %%s\\n' \"$rev\" \"$*\" >> %q\n", replayLog)
			if tc.record == "tip" {
				replayBody += fmt.Sprintf("[ \"$rev\" != %s ]\n", base)
			}
			steps.ReplayProgram = stub(t, "replay", replayBody) + " {only}"
			var report strings.Builder
			result, err := f.newLoopWith(t, 1, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, &report).Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(replayLog)
			if tc.wantReplay != "" {
				for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
					if err != nil || line != base+" "+tc.wantReplay {
						t.Fatalf("wrong replay invocations: %q (%v)", raw, err)
					}
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("the proved check started a replay: %q (%v)", raw, err)
			}
			if result.TrunkAcquitted != 0 || refusalOf(t, f.store, "gate") == nil {
				t.Fatalf("expected ordinary gate refusal: %+v\n%s", result, &report)
			}
			if !strings.Contains(report.String(), "recording a refusal, not a verdict (trunk evidence: "+tc.wantSource+")") {
				t.Fatalf("refusal did not name %q:\n%s", tc.wantSource, &report)
			}
		})
	}
}
