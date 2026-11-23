package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

const driftID = "patch-branch-inv2:pi"

func driftFixture(t *testing.T, delivered bool) (refusalLoopFixture, *Loop, forkObservation, string) {
	t.Helper()
	f, l := oqgp7Fixture(t, "item-doc")
	original := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "upstream v2")
	release := runGit(t, f.root, "rev-parse", "HEAD")
	patch := original
	if delivered {
		patch = release
	}
	runGit(t, f.root, "branch", "patch/pi", patch)
	o := forkObservation{CheckID: driftID, Fork: "pi", Kind: "release", Ref: "refs/tags/v2.0.0", Commit: release, Delivery: "patch/pi"}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(f.root, o.Pin())), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, o.Pin()), original+"\nremote\n"+original+"\n")
	runGit(t, f.root, "add", o.Pin())
	runGit(t, f.root, "commit", "-qm", "stale fork pin")
	return f, l, o, runGit(t, f.root, "rev-parse", "HEAD")
}

func TestForkDriftDisposition(t *testing.T) {
	for _, tt := range []struct {
		name, kind, delivery, path, replay, want string
		requests                                 int
	}{
		{"release-disjoint", "release", "patch/pi", "yolo-packs/pi-fork/pack.json", "red", GatePassedInherited, 1},
		{"release-touching", "release", "patch/pi", "yolo-packs/fork-pins/pi", "red", "hold", 1},
		{"head-disjoint", "head", "patch/pi", "yolo-packs/fork-pins/other", "red", GatePassedInherited, 0},
		{"head-touching", "head", "patch/pi", "yolo-packs/fork-pins/pi", "red", "", 0},
		{"series-disjoint", "release", "yolo-packs/matt/pi", "yolo-packs/matt/pack.json", "red", GatePassedInherited, 0},
		{"series-touching", "release", "yolo-packs/matt/pi", "yolo-packs/matt/pi/0001.patch", "red", "", 0},
		{"fixed-pin", "pin", "patch/pi", "yolo-packs/matt/pack.json", "red", "", 0},
		{"fixed-base", "base", "patch/pi", "yolo-packs/matt/pack.json", "red", "", 0},
		{"candidate-only", "release", "patch/pi", "yolo-packs/matt/pack.json", "pass", "", 0},
		{"base-unknown", "release", "patch/pi", "yolo-packs/matt/pack.json", "unknown", GateErrored, 0},
		{"base-no-conflict", "release", "patch/pi", "yolo-packs/matt/pack.json", "missing-observation", GateErrored, 0},
		{"target-moved", "release", "patch/pi", "yolo-packs/matt/pack.json", "different", GateErrored, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			f, l, o, base := driftFixture(t, false)
			o.Kind, o.Delivery = tt.kind, tt.delivery
			prior := o
			if tt.replay == "different" {
				prior.Ref = "refs/tags/v1.0.0"
			}
			replay := ReplayOutcome{Known: tt.replay != "unknown", Rev: base}
			if tt.replay != "pass" {
				replay.Red = []string{driftID}
			}
			if tt.replay != "missing-observation" {
				replay.forkConflicts = map[string]forkObservation{driftID: prior}
			}
			l.cfg.Steps = &oqgp7ReplaySteps{recordingSteps: &recordingSteps{}, storePath: f.storePath, replays: map[string]ReplayOutcome{base: replay}}
			gate := GateOutcome{Outcome: GateFailed, FailingChecks: []string{driftID}, forkConflicts: map[string]forkObservation{driftID: o}, CandidateChangedPaths: []string{tt.path}}
			c := Candidate{ItemID: "item-doc", BaseSHA: base}
			out, held, err := l.settleGateFailure(ctx, &c, gate, &LoopResult{})
			if held {
				out = "hold"
			}
			if err != nil || out != tt.want {
				t.Fatalf("settlement = %q, %v; want %q", out, err, tt.want)
			}
			if (len(c.InheritedDrift) == 1) != (out == GatePassedInherited) {
				t.Fatalf("inherited = %+v", c.InheritedDrift)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var requests, fixers int
				if err := tx.QueryRow(`SELECT count(*) FROM items WHERE item_id LIKE 'rebase-fork-%'`).Scan(&requests); err != nil {
					return err
				}
				if err := tx.QueryRow(`SELECT count(*) FROM items WHERE item_id LIKE 'fixer-main-%'`).Scan(&fixers); err != nil {
					return err
				}
				if requests != tt.requests || fixers != 0 {
					t.Fatalf("requests=%d, fixers=%d", requests, fixers)
				}
				if held {
					waiter, _, err := item.ReadWaitingOn(tx.Reader(), c.ItemID)
					if err != nil || waiter == nil || !strings.HasPrefix(waiter.Waiter, "item:"+forkRebasePrefix) {
						t.Fatalf("waiter=%+v, %v", waiter, err)
					}
				}
				return nil
			})
		})
	}
}

func TestForkDriftRequestDeduplicatesAndContinuesInOrder(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		ctx := context.Background()
		f, l, o, _ := driftFixture(t, delivered)
		id, err := l.fileForkRebase(ctx, o)
		if err != nil || id == "" {
			t.Fatalf("file: %q %v", id, err)
		}
		again, err := l.fileForkRebase(ctx, o)
		if again != id || err != nil {
			t.Fatalf("duplicate: %q %v", again, err)
		}
		d, err := l.recordedDelivery(ctx, id)
		if err != nil || (d.activeFork() == "") != delivered {
			t.Fatalf("delivery: %+v %v", d, err)
		}
		if !delivered {
			partial, err := l.settlePartialDelivery(ctx, Candidate{ItemID: id, BaseRef: o.PatchRef(), RunID: "run-rebase"}, o.Commit)
			if err != nil || !partial {
				t.Fatalf("partial = %v, %v", partial, err)
			}
			d, err = l.recordedDelivery(ctx, id)
			if err != nil || d.activeFork() != "" || d.ForkLanded != o.Commit {
				t.Fatalf("after delivery = %+v %v", d, err)
			}
		}
		o.Ref = "refs/tags/v3.0.0"
		o.Commit = runGit(t, f.root, "rev-parse", "HEAD")
		if newer, err := l.fileForkRebase(ctx, o); newer != id || err != nil {
			t.Fatalf("updated request: %q %v", newer, err)
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			var count int
			if err := tx.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, forkRebaseKind, id).Scan(&count); err != nil {
				return err
			}
			if count != 2 {
				t.Fatalf("request target records=%d, want two", count)
			}
			var text string
			if err := tx.QueryRow(`SELECT text FROM item_criteria WHERE item_id=?`, id).Scan(&text); err != nil {
				return err
			}
			if !strings.Contains(text, o.Ref) || !strings.Contains(text, o.Commit) {
				t.Fatalf("updated criterion: %s", text)
			}
			return nil
		})
	}
}

func TestForkDriftUnansweredSpanIsNotRed(t *testing.T) {
	path := filepath.Join(t.TempDir(), GateSpansFile)
	writeForkFile(t, path, `{"check_spans":[{"check_id":"patch-branch-inv2/pi","kind":"patch-branch-inv2","status":"ERR"},{"check_id":"forks-check/pi","kind":"forks-check","status":"FAIL"}]}`)
	observations, unfinished, unansweredFork := readForkConflicts(path)
	if len(observations) != 0 || !unfinished || !unansweredFork {
		t.Fatalf("unanswered span: %v, unfinished=%t, unanswered=%t", observations, unfinished, unansweredFork)
	}
	out, held, err := (&Loop{}).settleGateFailure(context.Background(), &Candidate{}, GateOutcome{
		Outcome: GateFailed, FailingChecks: []string{"forks-check/pi"}, unfinished: unfinished, unansweredFork: unansweredFork,
	}, &LoopResult{})
	if out != GateErrored || held || err != nil {
		t.Fatalf("settlement=%s held=%t err=%v", out, held, err)
	}
}

func TestForkDriftNoticesOnceWithoutRequest(t *testing.T) {
	ctx := context.Background()
	_, l, o, _ := driftFixture(t, false)
	var report bytes.Buffer
	l.cfg.Report = &report
	for _, kind := range []string{"head", "release"} {
		o.Kind = kind
		o.Delivery = "yolo-packs/pi-fork/patches"
		c := Candidate{ItemID: "item-doc", InheritedDrift: []forkObservation{o}}
		l.reportInheritedDrift(ctx, c)
		l.reportInheritedDrift(ctx, c)
	}
	if lines := strings.Count(report.String(), " inherited "); lines != 2 {
		t.Fatalf("want one line per target, got %q", report.String())
	}
}

func TestForkDriftRequestCannotWaitForItself(t *testing.T) {
	ctx := context.Background()
	f, l, o, base := driftFixture(t, true)
	id, err := l.fileForkRebase(ctx, o)
	if err != nil || id == "" {
		t.Fatalf("could not file request: %q %v", id, err)
	}
	l.cfg.Steps = &oqgp7ReplaySteps{recordingSteps: &recordingSteps{}, storePath: f.storePath,
		replays: map[string]ReplayOutcome{base: {Known: true, Red: []string{driftID}, forkConflicts: map[string]forkObservation{driftID: o}}},
	}
	c := Candidate{ItemID: id, BaseSHA: base}
	gate := GateOutcome{Outcome: GateFailed, FailingChecks: []string{driftID}, forkConflicts: map[string]forkObservation{driftID: o}, CandidateChangedPaths: []string{o.Pin()}}
	if result, held, err := l.settleGateFailure(ctx, &c, gate, &LoopResult{}); result != "" || held || err != nil {
		t.Fatalf("the request's own red must refuse, not inherit or hold: %q %t %v", result, held, err)
	}
}

func TestForkDriftRejectsAnOutdatedPinPiece(t *testing.T) {
	ctx := context.Background()
	_, l, o, base := driftFixture(t, true)
	id, err := l.fileForkRebase(ctx, o)
	if err != nil || id == "" {
		t.Fatalf("could not file request: %q %v", id, err)
	}
	lane := l.lanePath("lane-1")
	runGit(t, lane, "reset", "--hard", base)
	c := Candidate{ItemID: id, Worktree: lane, BaseSHA: base, HeadSHA: base}
	if detail, err := l.checkForkRebasePiece(ctx, c); detail == "" || err != nil {
		t.Fatalf("stale check = %q %v", detail, err)
	}
	writeForkFile(t, filepath.Join(lane, o.Pin()), o.Commit+"\nremote\n"+o.Commit+"\n")
	runGit(t, lane, "commit", "-qam", "record delivered release")
	c.HeadSHA = runGit(t, lane, "rev-parse", "HEAD")
	if detail, err := l.checkForkRebasePiece(ctx, c); detail != "" || err != nil {
		t.Fatalf("current check = %q %v", detail, err)
	}
}

func TestForkDriftLandsWithInheritedVerdict(t *testing.T) {
	ctx := context.Background()
	f, l, o, base := driftFixture(t, false)
	lane := l.lanePath("lane-1")
	runGit(t, lane, "reset", "--hard", base)
	path := "docs/guide.md"
	if err := os.MkdirAll(filepath.Join(lane, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane, path), "# Guide\n")
	runGit(t, lane, "add", path)
	runGit(t, lane, "commit", "-qm", "update guide")
	c := Candidate{ItemID: "item-doc", RunID: "run-doc-1", LaneID: "lane-1", Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD")}
	for k, v := range map[string]string{FenceFile: "docs/", ItemFenceFile: "docs/", BaseSHAFile: base} {
		writeForkFile(t, filepath.Join(l.laneDirPath(c.LaneID), k), v+"\n")
	}
	verdict := filepath.Join(RunDir(f.root, c.RunID), GateVerdictRecord)
	if err := os.MkdirAll(filepath.Dir(verdict), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, verdict, `{"record_version":1,"kind":"gate.verdict","id":"v1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-21T00:00:00Z","payload":{"result":"fail","checks":[{"check_id":"`+driftID+`","result":"fail"}]}}`)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_criteria SET state='done' WHERE item_id='item-doc'`)
		return err
	})
	steps := &oqgp7ReplaySteps{recordingSteps: &recordingSteps{}, storePath: f.storePath,
		replays: map[string]ReplayOutcome{base: {Known: true, Rev: base, Red: []string{driftID}, forkConflicts: map[string]forkObservation{driftID: o}}},
		gateFn: func(Candidate) (GateOutcome, error) {
			return GateOutcome{Outcome: GateFailed, FailingChecks: []string{driftID}, forkConflicts: map[string]forkObservation{driftID: o}, CandidateChangedPaths: []string{path}}, nil
		},
	}
	l.cfg.Steps = steps
	if _, landed, err := l.verify(ctx, &c, &LoopResult{}, nil); err != nil || !landed {
		t.Fatalf("landed=%t err=%v", landed, err)
	}
	raw, _ := readRecordPayload(verdict)
	var saved struct {
		Result    string
		Inherited []InheritedFailure `json:"inherited_failures"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil || saved.Result != GatePassedInherited || len(saved.Inherited) != 1 || !strings.Contains(saved.Inherited[0].Output, o.Ref) {
		t.Fatalf("verdict: %s", raw)
	}
	c.ItemID, c.RunID = "item-no-verdict", "run-no-verdict"
	// Unknown is routed to the existing retry hold rather than a failing candidate.
	steps.replays = map[string]ReplayOutcome{base: {Rev: base, Known: false}}
	if out, _, err := l.settleGateFailure(ctx, &c, GateOutcome{Outcome: GateFailed, FailingChecks: []string{driftID}, forkConflicts: map[string]forkObservation{driftID: o}}, &LoopResult{}); err != nil || out != GateErrored {
		t.Fatalf("unknown classified %q %v", out, err)
	}
}
