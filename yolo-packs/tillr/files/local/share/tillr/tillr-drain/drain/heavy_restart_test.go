package drain

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestHeavyLoopRestartReusesCompletedCheckOnFirstTick(t *testing.T) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	checks := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checks, 0755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		writeForkFile(t, filepath.Join(checks, id+".json"), `{"id":"`+id+`","scope":"tree","tier":"heavy"}`)
	}
	tip := heavyCommit(t, root, "ready")
	calls := filepath.Join(work, "calls")
	entered := filepath.Join(work, "entered-b")
	gate := executable(t, `id=all
while [ "$#" -gt 0 ]; do
 if [ "$1" = --only ]; then shift; id=$1; fi
 shift
done
echo "$id" >> `+calls+`
if [ "$id" = b ] && [ ! -e `+entered+` ]; then touch `+entered+`; sleep 1000; fi
printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"%s","result":"pass"}]}}\n' "$id"`)
	s, err := store.InitFixture(context.Background(), filepath.Join(work, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var log bytes.Buffer
	makeLoop := func() *Loop {
		steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: gate}
		judge := heavyTestSteps{judge: steps.JudgeHeavy}
		return &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: work, Steps: judge, Now: time.Now, Report: &log}}
	}
	l := makeLoop()
	if err := l.maybeRolling(ctx, tip, false, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(20 * time.Second)
waiting:
	for {
		if _, err := os.Stat(entered); err == nil {
			break waiting
		}
		select {
		case <-deadline:
			cancel()
			l.heavyWG.Wait()
			t.Fatal("second check did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	l.heavyWG.Wait()
	raw, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "a\nb\n" {
		t.Fatalf("first loop ran %q", raw)
	}
	// A new Loop has no in-memory trigger state and no new landing. Its very first tick owes proof.
	l = makeLoop()
	if err := l.maybeRolling(context.Background(), tip, false, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	raw, err = os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "a\nb\nb\n" || !strings.Contains(log.String(), "reused checks=a") {
		t.Fatalf("calls=%q log=%s", raw, log.String())
	}
	status, err := ReadHeavyStatus(context.Background(), root, s.Path())
	if err != nil || status.GreenCommit != tip || status.GreenProvedAt.IsZero() {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if line := heavyStatusLine(status); !strings.Contains(line, "green="+shortSHA(tip)) || !strings.Contains(line, "proved_age=") {
		t.Fatal(line)
	}
}

func TestHeavyUnattributableRangeFilesFixerWithBothEndpoints(t *testing.T) {
	for _, kind := range []string{"base-red", "last-landing-green", "no-landings", "multiple-last-green"} {
		t.Run(kind, func(t *testing.T) {
			root, base := heavyRepo(t)
			heavyGit(t, root, "update-ref", GreenRef, base)
			landing := heavyCommit(t, root, "landing")
			last := landing
			if kind == "multiple-last-green" {
				last = heavyCommit(t, root, "last-green")
			}
			tip := heavyCommit(t, root, "red")
			s, err := store.InitFixture(context.Background(), filepath.Join(scratchUnderDurable(t), "store.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if kind != "no-landings" {
				if err := s.WriteTx(context.Background(), func(tx *store.Tx) error { return recordFixtureLanding(tx, "fixture", "fixture-run", landing, 0) }); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "multiple-last-green" {
				if err := s.WriteTx(context.Background(), func(tx *store.Tx) error { return recordFixtureLanding(tx, "fixture-last", "fixture-run-last", last, 1) }); err != nil {
					t.Fatal(err)
				}
			}
			replayed := []string{}
			steps := heavyTestSteps{judge: func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
				sha := heavyGit(t, dir, "rev-parse", "HEAD")
				if only != "" {
					replayed = append(replayed, sha)
				}
				result := "pass"
				if sha == tip || kind == "base-red" || (kind == "multiple-last-green" && sha == landing) {
					result = "fail"
				}
				return []HeavyCheck{{CheckID: "unit", Result: result, Reads: []string{"value"}}}, nil
			}}
			var log bytes.Buffer
			l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: &log}}
			if err := l.startHeavy(context.Background(), tip); err != nil {
				t.Fatal(err)
			}
			l.heavyWG.Wait()
			if kind == "multiple-last-green" && (len(replayed) != 2 || replayed[0] != base || replayed[1] != last) {
				t.Fatalf("last landing was not checked before narrowing: %v", replayed)
			}
			var text string
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error { return r.QueryRow(`SELECT text FROM item_criteria`).Scan(&text) }); err != nil {
				t.Fatalf("no fixer: %v; %s", err, log.String())
			}
			if !strings.Contains(text, base) || !strings.Contains(text, tip) || !strings.Contains(text, "attribution unavailable") || strings.Contains(log.String(), "runner fault") {
				t.Fatalf("text=%s log=%s", text, log.String())
			}
		})
	}
}
