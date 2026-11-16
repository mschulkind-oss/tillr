package drain

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func continuationRunBin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(scratchUnderDurable(t), "tillr-run")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join("..", "..", "tillr-run")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build tillr-run: %v: %s", err, out)
	}
	return bin
}

func useContinuationProcess(steps *continuationSteps, bin string) {
	steps.process = &ProcessSteps{RunBin: bin, Store: steps.store, LaneRoot: steps.laneRoot, Now: func() time.Time { return steps.at }}
}

func TestResumeDistinctSameSecondSegments(t *testing.T) {
	bin := continuationRunBin(t)
	for _, mode := range []struct {
		name    string
		step    time.Duration
		process bool
	}{
		{"process-100ms", 100 * time.Millisecond, true},
		{"process-nanosecond", time.Nanosecond, true},
		{"exec-nanosecond", time.Nanosecond, false},
	} {
		t.Run(mode.name, func(t *testing.T) {
			f, l, steps, d := continuationFixture(t, "kill -TERM $$")
			steps.at = steps.at.Truncate(time.Second)
			if mode.process {
				useContinuationProcess(steps, bin)
			}
			var result LoopResult
			settleContinuation(t, l, &result)
			cfg := ResumeConfig{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Steps: steps, Now: func() time.Time { return steps.at }}
			for i := 0; i < 2; i++ {
				steps.at = steps.at.Add(mode.step)
				if _, err := Resume(context.Background(), cfg, ResumeSpec{RunID: d.RunRequest.RunID, Request: d.RunRequest}); err != nil {
					t.Fatal(err)
				}
				if w := readContinuationWork(t, f); w.Done || w.Orphaned {
					t.Fatalf("segment %d did not reopen: %+v", i, w)
				}
				fixtureWrite(t, f, func(tx *store.Tx) error {
					var payload []byte
					if err := tx.QueryRow(`SELECT payload FROM events WHERE kind = 'run.resume' ORDER BY seq DESC LIMIT 1`).Scan(&payload); err != nil {
						return err
					}
					var segment struct {
						ResumedAt string `json:"resumed_at"`
					}
					if err := json.Unmarshal(payload, &segment); err != nil {
						return err
					}
					if segment.ResumedAt != steps.at.UTC().Format(time.RFC3339Nano) {
						t.Fatalf("resume clock lost precision: %s, want %s", segment.ResumedAt, steps.at.UTC().Format(time.RFC3339Nano))
					}
					return nil
				})
				settleContinuation(t, l, &result)
				replayContinuation(t, f)
				if w := readContinuationWork(t, f); !w.Done {
					t.Fatalf("replay reopened segment %d", i)
				}
			}
			// A third distinct segment in the same second must settle normally.
			req := *d.RunRequest
			req.Command = []string{"sh", "-c", "echo candidate > candidate.txt; git add candidate.txt; git commit -qm candidate"}
			steps.at = steps.at.Add(mode.step)
			if _, err := Resume(context.Background(), cfg, ResumeSpec{RunID: req.RunID, Request: &req}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				settleContinuation(t, l, &result)
			}
			replayContinuation(t, f)
			if w := readContinuationWork(t, f); !w.Done {
				t.Fatal("judged replay reopened")
			}
			if steps.gates != 1 || steps.lands != 1 || result.Dispatched != 0 {
				t.Fatalf("settlement: gates=%d lands=%d %+v", steps.gates, steps.lands, result)
			}
			assertContinuationAttempts(t, f, 1)
		})
	}
}

func assertContinuationAttempts(t *testing.T, f refusalLoopFixture, want int) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, query := range []string{"SELECT count(*) FROM runs", "SELECT count(*) FROM events WHERE kind = 'drain.started'"} {
			var count int
			if err := tx.QueryRow(query).Scan(&count); err != nil {
				return err
			}
			if count != want {
				t.Fatalf("attempt count %d, want %d: %s", count, want, query)
			}
		}
		var retries int
		if err := tx.QueryRow(`SELECT count(*) FROM events WHERE kind = ?`, RetryAckKind).Scan(&retries); err != nil {
			return err
		}
		if retries != 0 {
			t.Fatalf("same-ID continuation authorized retries: %d", retries)
		}
		return nil
	})
}

func TestResumeSecondFailureHonorsContinuedHistory(t *testing.T) {
	bin := continuationRunBin(t)
	for _, mode := range []string{"current", "current-and-prior", "owner", "prior-still-orphaned"} {
		t.Run(mode, func(t *testing.T) {
			priorOrphaned := mode == "current-and-prior" || mode == "prior-still-orphaned"
			f, l, steps, first := continuationDispatchFixture(t, "exit 0", priorOrphaned)
			useContinuationProcess(steps, bin)
			l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "fixture"}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if mode == "owner" {
					_, err := tx.Exec(`UPDATE items SET body_ref = NULL WHERE item_id = 'item-a'`)
					return err
				}
				_, err := tx.Exec(`UPDATE items SET body_ref = 'docs/work.md#work' WHERE item_id = 'item-a'`)
				return err
			})
			var result LoopResult
			settleContinuation(t, l, &result)
			cfg := ResumeConfig{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Steps: steps, Now: func() time.Time { return steps.at }}
			if mode == "current-and-prior" {
				steps.at = steps.at.Add(100 * time.Millisecond)
				if _, err := Resume(context.Background(), cfg, ResumeSpec{RunID: first.RunRequest.RunID, Request: first.RunRequest}); err != nil {
					t.Fatal(err)
				}
				settleContinuation(t, l, &result)
				replayContinuation(t, f)
			}
			l.build.Digest = "changed"
			second := mustPlan(t, l)[0]
			if !second.decision.Readmitted || second.decision.Attempt != first.Attempt+1 {
				t.Fatalf("not second attempt: %+v", second.decision)
			}
			second.decision.RunRequest.Command = []string{"sh", "-c", "kill -TERM $$"}
			steps.at = steps.at.Add(100 * time.Millisecond)
			ok, _, err := l.applyPlan(context.Background(), second, readContinuationWork(t, f).BaseSHA, steps.at)
			if err != nil || !ok {
				t.Fatalf("dispatch second: %v / %v", ok, err)
			}
			if _, _, err := steps.Run(context.Background(), *second.decision.RunRequest); err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				return appendEffect(tx, loopOrphanedKind, second.decision.RunRequest.RunID, struct{}{}, steps.at)
			})
			settleContinuation(t, l, &result)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				cs, err := item.ReadConflicts(tx.Reader())
				if cs["item-a"].ConflictID != "" {
					t.Fatal("interrupted attempt raised second-failure conflict")
				}
				return err
			})
			req := *second.decision.RunRequest
			req.Command = []string{"sh", "-c", "exit 0"}
			steps.at = steps.at.Add(100 * time.Millisecond)
			if _, err := Resume(context.Background(), cfg, ResumeSpec{RunID: req.RunID, Request: &req}); err != nil {
				t.Fatal(err)
			}
			settleContinuation(t, l, &result)
			replayContinuation(t, f)
			if w := readContinuationWork(t, f); !w.Done || w.Orphaned || w.Decision.Attempt != second.decision.Attempt || w.Decision.RunRequest.RunID != req.RunID {
				t.Fatalf("continued second attempt not examined: %+v", w)
			}
			if err := l.markExamined(context.Background(), second.decision); err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				cs, err := item.ReadConflicts(tx.Reader())
				c := cs["item-a"]
				if mode == "prior-still-orphaned" {
					if c.ConflictID != "" {
						t.Fatalf("orphaned prior charged: %+v", c)
					}
					return err
				}
				seat, status := "design", "raised"
				if mode == "owner" {
					seat, status = "owner", "owner"
				}
				if c.WaitingOn != seat || c.Status != status || c.RunID != req.RunID || !strings.Contains(c.Text, first.RunRequest.RunID) || !strings.Contains(c.Text, "Second failed attempt:") {
					t.Fatalf("missing second-failure disposition: %+v", c)
				}
				var count int
				if err := tx.QueryRow(`SELECT count(*) FROM events WHERE kind = 'item.conflict'`).Scan(&count); err != nil {
					return err
				}
				if count != 1 {
					t.Fatalf("duplicate conflict: %d", count)
				}
				return err
			})
			assertContinuationAttempts(t, f, 2)
			if steps.gates != 0 || steps.lands != 0 || result.Dispatched != 0 {
				t.Fatalf("no-op judged or redispatched: %+v", result)
			}
			if mode == "owner" {
				if d := mustPlan(t, l)[0].decision; d.Action != ActionHold || d.Reason != ReasonConflictOwner {
					t.Fatalf("owner hold bypassed: %+v", d)
				}
			} else if mode != "prior-still-orphaned" {
				if d := mustPlan(t, l)[0].decision; d.Seat != "design" || d.BlockConflict == nil {
					t.Fatalf("design route bypassed: %+v", d)
				}
			}
		})
	}
}
