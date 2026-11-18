package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// A trunk claim is judged against the current trunk tip, not against the lane's old base.
// The owner ruled that work is always built against the latest trunk: a check red only on a
// base the trunk has since moved past names a repair that already happened, so it files no
// fixer. Live on 2026-11-18, a run cut nine hours earlier from 1887afe2c filed
// fixer-main-1887afe2c948 for a check its tip had long since passed, and that open fixer
// held every ordinary item.

// staleLaneFixture claims item-a on base, cuts its lane there, and then moves the trunk one
// commit past it. It answers the base and the new tip.
func staleLaneFixture(t *testing.T, steps *obstacleSteps) (refusalLoopFixture, *Loop, *pendingWork, string, string) {
	t.Helper()
	f, l, work := obstacleFixture(t, steps, "item-a")
	base := work.BaseSHA
	lane := work.Decision.LaneID
	runGit(t, l.laneTarget(lane), "reset", "-q", "--hard", base)
	files := map[string]string{BaseSHAFile: base, ItemBaseSHAFile: base, SessionIDFile: "0b5d7a8e-6c1f-4f7e-9a51-3d2c1b0a9e8f"}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(l.laneDirPath(lane), name), []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.root, "repair.txt"), []byte("the trunk's repair\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "add", "repair.txt")
	runGit(t, f.root, "commit", "-qm", "repair the trunk")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	return f, l, work, base, tip
}

func fixerCount(t *testing.T, f refusalLoopFixture) int {
	t.Helper()
	var count int
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return tx.QueryRow(`SELECT count(*) FROM items WHERE item_id LIKE 'fixer-main-%'`).Scan(&count)
	})
	return count
}

const staleTrunkReport = "OBSTACLE: trunk\nCHECKS: go-build\nBASE: 0123\nRESULT: blocked\n"

// A trunk obstacle red on the lane's old base and green on the tip is answered in the same
// session: the trunk is repaired at the tip, the loop brings the lane onto it, and no fixer
// is filed.
func TestATrunkObstacleRedOnlyOnAnOldBaseIsAnsweredAtTheTip(t *testing.T) {
	steps := &obstacleSteps{known: true}
	f, l, work, base, tip := staleLaneFixture(t, steps)
	steps.byRev = map[string][]string{base: {"go-build"}}
	settleReport(t, f, l, work, staleTrunkReport)
	if n := fixerCount(t, f); n != 0 {
		t.Fatalf("a check red only on the old base filed %d fixer(s)", n)
	}
	if len(steps.states) != 0 {
		t.Fatalf("the item was held: %v", steps.states)
	}
	p := refusalOf(t, f, work.Decision.RunRequest.RunID)
	if p.Step != RefusalNoOp || !strings.Contains(p.Detail, "repaired at "+shortSHA(tip)) || !strings.Contains(p.Detail, "latest trunk") {
		t.Fatalf("refusal %+v", p)
	}
	lane := work.Decision.LaneID
	if got := l.laneBase(lane); got != tip {
		t.Fatalf("the lane's base is %s, want the tip %s", got, tip)
	}
	runGit(t, l.laneTarget(lane), "merge-base", "--is-ancestor", tip, "HEAD")
	if p.WorkerSession == nil || p.WorkerSession.BaseSHA != tip {
		t.Fatalf("the returned session keeps base %+v, want the tip %s", p.WorkerSession, tip)
	}
}

// A trunk obstacle red on the old base and on the tip is the trunk's: the fixer is filed for
// the tip, the trunk as it is now, and never for the lane's old base.
func TestATrunkObstacleRedOnTheTipFilesTheFixerAtTheTip(t *testing.T) {
	steps := &obstacleSteps{known: true}
	f, l, work, base, tip := staleLaneFixture(t, steps)
	steps.byRev = map[string][]string{base: {"go-build"}, tip: {"go-build"}}
	settleReport(t, f, l, work, staleTrunkReport)
	p := refusalOf(t, f, work.Decision.RunRequest.RunID)
	if p.Step != RefusalTrunkAlreadyRed || p.FixerItem != TrunkFixerID(tip) || p.RedSHA != tip {
		t.Fatalf("refusal %+v, want a fixer for the tip %s", p, shortSHA(tip))
	}
	if n := fixerCount(t, f); n != 1 {
		t.Fatalf("%d fixers filed, want 1", n)
	}
}

// The tip run's recorded pass of a check answers for the tip, so the loop replays only what
// that record does not cover.
func TestATipRunsRecordedPassAnswersForTheTipWithoutAReplay(t *testing.T) {
	steps := &obstacleSteps{known: true}
	f, l, work, base, tip := staleLaneFixture(t, steps)
	steps.byRev = map[string][]string{base: {"go-build"}, tip: {"go-build"}}
	dir, err := heavyDirectory(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(heavyProgress{TipSHA: tip, Checks: map[string]HeavyCheck{"go-build": {CheckID: "go-build", Result: "pass"}}, PassedAt: map[string]time.Time{"go-build": time.Now().UTC()}})
	if err := os.WriteFile(filepath.Join(dir, "heavy-progress.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	settleReport(t, f, l, work, staleTrunkReport)
	for _, rev := range steps.revs {
		if rev == tip {
			t.Fatalf("the tip was replayed for a check its run already passed: %v", steps.revs)
		}
	}
	if n := fixerCount(t, f); n != 0 {
		t.Fatalf("%d fixer(s) filed for a check the tip passed", n)
	}
	if p := refusalOf(t, f, work.Decision.RunRequest.RunID); p.Step != RefusalNoOp || !strings.Contains(p.Detail, "repaired at "+shortSHA(tip)) {
		t.Fatalf("refusal %+v", p)
	}
}
