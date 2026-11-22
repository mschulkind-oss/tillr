package drain_test

import (
	"context"
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
