package drain

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	deadline := time.After(hangGuard)
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
	for _, kind := range []string{"base-red", "no-touching-commits", "multiple-last-green"} {
		t.Run(kind, func(t *testing.T) {
			root, base := heavyRepo(t)
			heavyGit(t, root, "update-ref", GreenRef, base)
			landing := heavyCommit(t, root, "landing")
			last := landing
			if kind == "multiple-last-green" {
				last = heavyCommit(t, root, "last-green")
			}
			var tip string
			if kind == "no-touching-commits" {
				if err := os.WriteFile(filepath.Join(root, "unrelated"), []byte("unrelated"), 0644); err != nil {
					t.Fatal(err)
				}
				heavyGit(t, root, "add", ".")
				heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "unrelated")
				tip = heavyGit(t, root, "rev-parse", "HEAD")
			} else {
				tip = heavyCommit(t, root, "red")
			}
			s, err := store.InitFixture(context.Background(), filepath.Join(scratchUnderDurable(t), "store.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if kind != "no-touching-commits" {
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
			reads := []string{"value"}
			if kind == "no-touching-commits" {
				reads = []string{"other"}
			}
			steps := heavyTestSteps{judge: func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
				sha := heavyGit(t, dir, "rev-parse", "HEAD")
				if only != "" {
					replayed = append(replayed, sha)
				}
				result := "pass"
				if sha == tip || kind == "base-red" || (kind == "multiple-last-green" && sha == landing) {
					result = "fail"
				}
				if only != "" && kind == "multiple-last-green" && sha == tip {
					result = "pass"
				}
				return []HeavyCheck{{CheckID: "unit", Result: result, Reads: reads}}, nil
			}}
			var log bytes.Buffer
			l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: &log}}
			if err := l.startHeavy(context.Background(), tip); err != nil {
				t.Fatal(err)
			}
			l.heavyWG.Wait()
			if kind == "multiple-last-green" && (len(replayed) != 2 || replayed[0] != base || replayed[1] != tip) {
				t.Fatalf("last candidate was not checked before narrowing: %v", replayed)
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

// Each start of the loop has time for one check before it is replaced, and unrelated
// commits land between starts. Passes whose reads did not change carry forward, so the
// green pointer still reaches the newest tip instead of staying unproved.
func TestHeavyRepeatedRestartsCarryPassesForwardAndAdvanceGreen(t *testing.T) {
	root, base := heavyRepo(t)
	work := scratchUnderDurable(t)
	checks := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checks, 0755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		writeForkFile(t, filepath.Join(checks, id+".json"), `{"id":"`+id+`","scope":"tree","tier":"heavy","reads":["read-`+id+`/"]}`)
	}
	heavyGit(t, root, "add", ".")
	heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "declarations")
	heavyGit(t, root, "update-ref", GreenRef, base)
	calls := filepath.Join(work, "calls")
	// A check passes only when the test has allowed it; any other check hangs until the
	// loop is cancelled, which is "the loop was replaced mid-run".
	gate := executable(t, `id=all
while [ "$#" -gt 0 ]; do
 if [ "$1" = --only ]; then shift; id=$1; fi
 shift
done
echo "$id" >> `+calls+`
if [ ! -e `+work+`/allow-$id ]; then touch `+work+`/entered-$id; sleep 1000; fi
printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"%s","result":"pass"}]}}\n' "$id"`)
	s, err := store.InitFixture(context.Background(), filepath.Join(work, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := func(ctx context.Context, tip string) *Loop {
		steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: gate}
		l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: work, Steps: heavyTestSteps{judge: steps.JudgeHeavy}, Now: time.Now, Report: io.Discard}}
		if err := l.maybeRolling(ctx, tip, false, &LoopResult{}); err != nil {
			t.Fatal(err)
		}
		return l
	}
	allow := func(id string) {
		if err := os.WriteFile(filepath.Join(work, "allow-"+id), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	interrupted := func(tip, hung string) {
		ctx, cancel := context.WithCancel(context.Background())
		l := start(ctx, tip)
		deadline := time.After(hangGuard)
		for {
			if _, err := os.Stat(filepath.Join(work, "entered-"+hung)); err == nil {
				break
			}
			select {
			case <-deadline:
				cancel()
				l.heavyWG.Wait()
				t.Fatalf("check %s did not start", hung)
			case <-time.After(10 * time.Millisecond):
			}
		}
		cancel()
		l.heavyWG.Wait()
		if got := heavyGit(t, root, "rev-parse", GreenRef); got != base {
			t.Fatalf("green moved to %s before every check passed", got)
		}
	}
	allow("a")
	interrupted(heavyCommit(t, root, "one"), "b")
	allow("b")
	interrupted(heavyCommit(t, root, "two"), "c")
	allow("c")
	tip := heavyCommit(t, root, "three")
	l := start(context.Background(), tip)
	l.heavyWG.Wait()
	raw, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "a\nb\nb\nc\nc\n" {
		t.Fatalf("checks ran %q", raw)
	}
	if got := heavyGit(t, root, "rev-parse", GreenRef); got != tip {
		t.Fatalf("green=%s want %s", got, tip)
	}
}

// A restart that cuts the culprit search short leaves its finished replays on disk; the
// next start does not run them again and still files the fixer.
func TestHeavyRestartMidBisectionReusesFinishedReplays(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	c1 := heavyCommit(t, root, "one")
	c2 := heavyCommit(t, root, "two")
	c3 := heavyCommit(t, root, "three")
	tip := heavyCommit(t, root, "four")
	red := map[string]bool{c3: true, tip: true}
	s, err := store.InitFixture(context.Background(), filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var mu sync.Mutex
	replayed := []string{}
	blockAt := c2
	entered := make(chan struct{})
	steps := heavyTestSteps{judge: func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		sha := heavyGit(t, dir, "rev-parse", "HEAD")
		if only != "" {
			mu.Lock()
			replayed = append(replayed, sha)
			hold := sha == blockAt
			mu.Unlock()
			if hold {
				close(entered)
				<-ctx.Done()
				return nil, ctx.Err()
			}
		}
		result := "pass"
		if red[sha] {
			result = "fail"
		}
		return []HeavyCheck{{CheckID: "unit", Result: result, Reads: []string{"value"}}}, nil
	}}
	var log strings.Builder
	newLoop := func() *Loop {
		return &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: &log}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := newLoop()
	if err := l.startHeavy(ctx, tip); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(hangGuard):
		cancel()
		l.heavyWG.Wait()
		t.Fatal("bisection did not reach its third replay")
	}
	cancel()
	l.heavyWG.Wait()
	mu.Lock()
	blockAt = ""
	mu.Unlock()
	l = newLoop()
	if err := l.startHeavy(context.Background(), tip); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	count := map[string]int{}
	for _, sha := range replayed {
		count[sha]++
	}
	if count[base] != 1 || count[tip] != 1 || count[c2] != 2 || count[c3] != 1 || count[c1] != 0 {
		t.Fatalf("replays ran %v; log=%s", count, log.String())
	}
	if !strings.Contains(log.String(), "reused replay") {
		t.Fatalf("log=%s", log.String())
	}
	var text string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error { return r.QueryRow(`SELECT text FROM item_criteria`).Scan(&text) }); err != nil || !strings.Contains(text, shortSHA(c3)) {
		t.Fatalf("no fixer against %s: %v; text=%s log=%s", c3, err, text, log.String())
	}
}
