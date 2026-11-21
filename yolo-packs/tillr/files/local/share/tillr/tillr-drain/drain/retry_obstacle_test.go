package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr/store"
)

func TestOperatorRetryRestoresObstacleCandidate(t *testing.T) {
	for _, state := range []string{"recorded head", "legacy pin", "missing", "no candidate"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			f, l := passFixture(t, "item-a")
			d := stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")
			base := l.laneBase(d.LaneID)
			target := l.laneTarget(d.LaneID)
			head := runGit(t, target, "rev-parse", "HEAD")
			// Immutable run metadata, not today's lane base, owns this retry boundary.
			if err := os.MkdirAll(RunDir(f.root, d.RunRequest.RunID), 0755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(RunDir(f.root, d.RunRequest.RunID), ItemBaseSHAFile), base+"\n")
			obs := parseObstacle("RESULT: blocked\nOBSTACLE: owner\nQUESTION: Which regression evidence should I provide?\n")
			if err := l.recordObstacleHold(ctx, d, obs); err != nil {
				t.Fatal(err)
			}
			if err := l.pin(ctx, d.RunRequest.RunID, head); err != nil {
				t.Fatal(err)
			}
			if state != "recorded head" {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					var raw []byte
					if err := tx.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, obstacles.HeldKind, d.ItemID).Scan(&raw); err != nil {
						return err
					}
					var h obstacles.Hold
					if err := json.Unmarshal(raw, &h); err != nil {
						return err
					}
					switch state {
					case "legacy pin":
						h.HeadSHA = ""
					case "missing":
						h.HeadSHA = strings.Repeat("b", 40)
					case "no candidate":
						h.HeadSHA = base
					}
					return appendEffectOn(tx, obstacles.HeldKind, "item", d.ItemID, h, l.cfg.Now())
				})
			}
			if err := l.markExamined(ctx, d); err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := AuthorizeRetry(ctx, tx, RetryRequest{ItemID: d.ItemID, RunID: d.RunRequest.RunID, By: "owner", Why: "Use the targeted test evidence", At: l.cfg.Now()})
				return err
			})
			tip := conflictTestCommit(t, f.root, "later.txt", "later\n", "advance trunk")
			runGit(t, target, "reset", "--hard", tip)
			l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
			d = mustPlan(t, l)[0].decision
			available := state == "recorded head" || state == "legacy pin"
			if d.Action != ActionRetry || d.RetryCandidate == nil || d.RetryCandidate.Available != available {
				t.Fatalf("retry: %+v candidate=%+v", d, d.RetryCandidate)
			}
			staged, err := l.stageDispatches(ctx, []Decision{d}, tip, &LoopResult{})
			if err != nil || len(staged) != 1 {
				t.Fatalf("stage: %v %+v", err, staged)
			}
			wantHead, wantBase := head, base
			if !available {
				wantHead, wantBase = tip, tip
			}
			if got := runGit(t, l.laneTarget(d.LaneID), "rev-parse", "HEAD"); got != wantHead {
				t.Fatalf("head=%s want %s", got, head)
			}
			if got := l.settleBase(d.LaneID, tip); got != wantBase {
				t.Fatalf("base=%s want %s", got, base)
			}
			if state == "no candidate" && strings.Contains(retryCandidateBrief(d.RetryCandidate), "unavailable") {
				t.Fatal("no-candidate exit called missing work")
			}
			if !strings.Contains(retryCandidateBrief(d.RetryCandidate), "Which regression evidence") {
				t.Fatal("lost obstacle question")
			}
		})
	}
}
