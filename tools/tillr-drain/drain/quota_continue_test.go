package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type quotaContinuationSteps struct {
	continuationSteps
	backends string
	requests []run.Request
}

func (s *quotaContinuationSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.requests = append(s.requests, req)
	db, err := store.Open(ctx, s.store, true)
	if err != nil {
		return run.Record{}, "", err
	}
	defer db.Close()
	env, err := run.Exec(ctx, db, req, run.ExecOptions{LaneRoot: s.laneRoot, BackendsDir: s.backends, Now: s.at})
	if err != nil {
		return run.Record{}, "", err
	}
	var rec run.Record
	err = json.Unmarshal(env.Payload, &rec)
	return rec, logTail(rec.ArtifactRefs), err
}

func quotaContinuationFixture(t *testing.T, work bool) (refusalLoopFixture, *Loop, *quotaContinuationSteps, Decision, run.Record) {
	t.Helper()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	backends := t.TempDir()
	steps := &quotaContinuationSteps{continuationSteps: continuationSteps{t: t, store: f.storePath, laneRoot: f.laneRoot, at: l.cfg.Now()}, backends: backends}
	l.cfg.Steps = steps
	l.cfg.Now = func() time.Time { return steps.at }
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(backends, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\n"
		if name == "alpha" {
			script += "if [ \"${TILLR_SESSION_MODE:-new}\" = new ]; then\n"
			if work {
				script += "echo one > one.txt; echo two > two.txt\n"
			}
			script += "echo refused; exit 1\nfi\n"
		}
		script += "test -f one.txt && test -f two.txt || exit 9\ncat \"$TILLR_RESUME_PROMPT_FILE\"\ngit add one.txt two.txt; git commit -qm candidate\necho 'RESULT: done'\n"
		writeForkFile(t, filepath.Join(dir, "exec"), script)
		if err := os.Chmod(filepath.Join(dir, "exec"), 0755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(dir, "pause-kind"), run.PauseWhereItStands+"\n")
		extract := fmt.Sprintf("#!/bin/sh\nprev=; out=; code=\nfor arg in \"$@\"; do [ \"$prev\" != --out ] || out=$arg; [ \"$prev\" != --exit-code ] || code=$arg; prev=$arg; done\nif [ \"$code\" = 1 ]; then printf '# run report\\nrate limit kind: five_hour\\nfraction: 1\\nstatus: rejected\\nreset: %d\\n---\\n' > \"$out\"; else printf '# run report\\n---\\nRESULT: done\\n' > \"$out\"; fi\n", steps.at.Add(time.Hour).Unix())
		writeForkFile(t, filepath.Join(dir, "extract-report"), extract)
		if err := os.Chmod(filepath.Join(dir, "extract-report"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	l.cfg.Policy.Backends = map[string]Backend{"alpha": {Adapter: filepath.Join(backends, "alpha", "exec"), Model: "stub"}, "beta": {Adapter: filepath.Join(backends, "beta", "exec"), Model: "stub"}}
	l.cfg.Policy.DefaultBackend = "alpha"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["one.txt","two.txt"]'`)
		return err
	})
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	dir := l.laneDirPath("lane-1")
	for name, body := range map[string]string{BaseSHAFile: base + "\n", FenceFile: "one.txt\ntwo.txt\n", BriefFile: "finish this attempt\n", SessionIDFile: "33b5f2d2-0cd4-4a49-b293-c58f5635561f\n"} {
		writeForkFile(t, filepath.Join(dir, name), body)
	}
	plan := mustPlan(t, l)[0]
	ok, _, err := l.applyPlan(context.Background(), plan, base, steps.at)
	if err != nil || !ok {
		t.Fatalf("dispatch: %v %v", ok, err)
	}
	rec, _, err := steps.Run(context.Background(), *plan.decision.RunRequest)
	if err != nil {
		t.Fatal(err)
	}
	return f, l, steps, plan.decision, rec
}

func TestQuotaContinuationKeepsFilesAttemptAndJudgesOnce(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint(pending), func(t *testing.T) {
			f, l, steps, d, rec := quotaContinuationFixture(t, true)
			var result LoopResult
			if pending {
				settleContinuation(t, l, &result)
			} else {
				out := l.settle(context.Background(), d, rec, logTail(rec.ArtifactRefs), l.laneBase(d.LaneID), "tick", steps.at, &result, &PassTiming{}, nil, false)
				if out.err != nil || out.stop != "" {
					t.Fatalf("settle: %+v", out)
				}
			}
			if len(steps.requests) != 1 {
				t.Fatalf("closed backend routed work elsewhere: %+v", steps.requests)
			}
			steps.at = steps.at.Add(2 * time.Hour)
			settleContinuation(t, l, &result)
			if len(steps.requests) != 2 {
				t.Fatalf("requests=%+v", steps.requests)
			}
			next := steps.requests[1]
			if next.RunID != d.RunRequest.RunID || next.LaneID != d.LaneID || next.Backend != "alpha" || !next.Resume || next.FreshSession {
				t.Fatalf("lost attempt or session: %+v", next)
			}
			for i := 0; i < 3; i++ {
				settleContinuation(t, l, &result)
			}
			if steps.gates != 1 || steps.lands != 1 || result.InfraFailures != 0 {
				t.Fatalf("gates=%d lands=%d result=%+v", steps.gates, steps.lands, result)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var n int
				if err := r.QueryRow(`SELECT count(*) FROM runs`).Scan(&n); err != nil {
					return err
				}
				if n != 1 {
					t.Errorf("runs=%d", n)
				}
				if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind = ?`, RetryAckKind).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					t.Errorf("quota spent retries=%d", n)
				}
				return nil
			})
		})
	}
}

func TestQuotaContinuationWaitsAndReopensOriginalBackend(t *testing.T) {
	f, l, steps, d, _ := quotaContinuationFixture(t, true)
	var result LoopResult
	settleContinuation(t, l, &result)
	if w := readContinuationWork(t, f); w.Done || w.LaneReleased {
		t.Fatalf("pause ended work: %+v", w)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var backend, state string
		err := r.QueryRow(`SELECT backend,state FROM quota_pauses WHERE run_id=?`, d.RunRequest.RunID).Scan(&backend, &state)
		if err == nil && (backend != "alpha" || state != "waiting") {
			t.Errorf("pause=%s/%s", backend, state)
		}
		return err
	})
	plan := mustPlan(t, l)[0]
	if plan.decision.Action != ActionHold || plan.decision.Reason != ReasonBackendWindow {
		t.Fatalf("waiting: %+v", plan.decision)
	}
	if stop, err := l.pass(context.Background(), &result); err != nil || stop != "" {
		t.Fatalf("loop exited with quota-paused work: %s / %v", stop, err)
	}
	// A new loop sees the persisted hold; the reopening does not need the old process.
	replacement := f.loop(t, steps, &itemContentStager{})
	replacement.cfg.Lanes = l.cfg.Lanes
	replacement.cfg.Policy = l.cfg.Policy
	replacement.cfg.Now = func() time.Time { return steps.at.Add(2 * time.Hour) }
	steps.at = steps.at.Add(2 * time.Hour)
	settleContinuation(t, replacement, &result)
	for i := 0; i < 3; i++ {
		settleContinuation(t, replacement, &result)
	}
	if len(steps.requests) != 2 || steps.requests[1].Backend != "alpha" || !steps.requests[1].Resume {
		t.Fatalf("requests=%+v", steps.requests)
	}
	raw, _ := json.Marshal(steps.requests[1])
	if strings.Contains(string(raw), `"fresh_session":true`) {
		t.Fatalf("same backend started fresh: %s", raw)
	}
	if steps.gates != 1 || steps.lands != 1 {
		t.Fatalf("judgments %d %d", steps.gates, steps.lands)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		err := r.QueryRow(`SELECT count(*) FROM quota_pauses`).Scan(&n)
		if n != 0 {
			t.Errorf("pause left after claim: %d", n)
		}
		return err
	})
}
