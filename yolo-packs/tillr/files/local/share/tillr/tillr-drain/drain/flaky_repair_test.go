package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/store"
)

func writeDisabledMarkerFixture(t *testing.T, resultsStore string, marker checkresult.DisabledMarker) string {
	t.Helper()
	disabledDir := filepath.Join(resultsStore, "disabled")
	if err := os.MkdirAll(disabledDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if marker.Episode == "" {
		marker.Episode = checkresult.FormatEpisode(marker.Fingerprint, marker.PassAt, marker.FailAt)
	}
	raw, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(disabledDir, checkMarkerHash(marker.CheckID)+".json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFiledFlakyItem(t *testing.T, storePath, itemID string) (title, state, criterion string) {
	t.Helper()
	s, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		if err := r.QueryRow(`SELECT title, state FROM items WHERE item_id = ?`, itemID).Scan(&title, &state); err != nil {
			return err
		}
		return r.QueryRow(`SELECT text FROM item_criteria WHERE item_id = ? AND criterion_id = ?`, itemID, FlakyRepairCriterion).Scan(&criterion)
	})
	if err != nil {
		t.Fatalf("read %s: %v", itemID, err)
	}
	return title, state, criterion
}

func TestDisabledMarkerSweepFilesReEnableItemTitleAndWords(t *testing.T) {
	f, l := passFixture(t, "item-unrelated")
	l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
	resultsStore := filepath.Join(f.root, ".git", "tillr-gate", "results")
	marker := checkresult.DisabledMarker{
		CheckID:     "fast-sample",
		Fingerprint: "fp-contradicted-1",
		PassAt:      "2026-11-21T02:00:00Z",
		PassLogRef:  "/store/disabled/ep1.pass.log",
		FailAt:      "2026-11-21T02:05:00Z",
		FailLogRef:  "",
	}
	marker.Episode = checkresult.FormatEpisode(marker.Fingerprint, marker.PassAt, marker.FailAt)
	markerPath := writeDisabledMarkerFixture(t, resultsStore, marker)

	plans := mustPlan(t, l)
	foundFlaky := false
	for _, p := range plans {
		if p.decision.ItemID == "flaky-fast-sample" {
			foundFlaky = true
			if p.decision.Action == ActionHold && p.decision.Reason == ReasonTriageNoBlock {
				t.Fatalf("flaky repair item held on document triage: %+v", p.decision)
			}
		}
	}
	if !foundFlaky {
		t.Fatalf("expected flaky-fast-sample in planned items, got %d plans", len(plans))
	}

	title, state, criterion := readFiledFlakyItem(t, f.storePath, "flaky-fast-sample")
	if wantTitle := "re-enable check `fast-sample` correctly"; title != wantTitle {
		t.Fatalf("got title %q, want %q", title, wantTitle)
	}
	if state != StateInQueue {
		t.Fatalf("got state %q, want %q", state, StateInQueue)
	}
	wantCriterion := "Fix check `fast-sample` at its cause and re-enable `fast-sample` correctly (do not add retries, sleeps, or quarantine skips); its work is done when `fast-sample` is back in the gate program and no longer both passes and fails on the same inputs. Disabling episode `fp-contradicted-1|2026-11-21T02:00:00Z|2026-11-21T02:05:00Z` (fingerprint `fp-contradicted-1`): passed at 2026-11-21T02:00:00Z (/store/disabled/ep1.pass.log) and failed at 2026-11-21T02:05:00Z (output location not recorded)."
	if criterion != wantCriterion {
		t.Fatalf("got criterion:\n%s\nwant:\n%s", criterion, wantCriterion)
	}

	raw, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var stamped checkresult.DisabledMarker
	if err := json.Unmarshal(raw, &stamped); err != nil {
		t.Fatal(err)
	}
	if stamped.RepairItem != "flaky-fast-sample" {
		t.Fatalf("expected marker repair_item stamped as flaky-fast-sample, got %+v", stamped)
	}
}

func TestDisabledMarkerSweepDeduplicatesWhileOpenAndEnablesAfterLand(t *testing.T) {
	f, l := passFixture(t)
	work := scratchUnderDurable(t)
	gateBin := filepath.Join(work, "tillr-gate")
	buildCmd := exec.Command("go", "build", "-o", gateBin, ".")
	buildCmd.Dir = filepath.Join("..", "..", "tillr-gate")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build tillr-gate: %v\n%s", err, out)
	}
	l.cfg.Steps = &ProcessSteps{Root: f.root, LaneRoot: l.cfg.LaneRoot, HeavyGateBin: gateBin}

	resultsStore := filepath.Join(f.root, ".git", "tillr-gate", "results")
	ep1 := checkresult.FormatEpisode("fp-ep1", "2026-11-21T04:00:00Z", "2026-11-21T04:02:00Z")
	markerPath := writeDisabledMarkerFixture(t, resultsStore, checkresult.DisabledMarker{
		CheckID:     "fast-sample",
		Episode:     ep1,
		Fingerprint: "fp-ep1",
		PassAt:      "2026-11-21T04:00:00Z",
		PassLogRef:  "/store/disabled/ep1.pass.log",
		FailAt:      "2026-11-21T04:02:00Z",
		FailLogRef:  "/store/disabled/ep1.fail.log",
	})

	l.sweepDisabledMarkers(context.Background())
	l.sweepDisabledMarkers(context.Background())

	var count int
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM items WHERE item_id LIKE 'flaky-fast-sample%'`).Scan(&count)
	})
	if count != 1 {
		t.Fatalf("expected 1 open flaky item across repeated sweeps, got %d", count)
	}

	// While flaky-fast-sample is unfinished, a newer contradiction on the same check reuses the open item.
	epRotated := checkresult.FormatEpisode("fp-ep1b", "2026-11-21T04:10:00Z", "2026-11-21T04:11:00Z")
	writeDisabledMarkerFixture(t, resultsStore, checkresult.DisabledMarker{
		CheckID:     "fast-sample",
		Episode:     epRotated,
		Fingerprint: "fp-ep1b",
		PassAt:      "2026-11-21T04:10:00Z",
		PassLogRef:  "/store/disabled/ep1b.pass.log",
		FailAt:      "2026-11-21T04:11:00Z",
		FailLogRef:  "/store/disabled/ep1b.fail.log",
	})
	l.sweepDisabledMarkers(context.Background())
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM items WHERE item_id LIKE 'flaky-fast-sample%'`).Scan(&count)
	})
	if count != 1 {
		t.Fatalf("expected open item to cover rotated episode without filing a duplicate, got %d", count)
	}

	// Mark flaky-fast-sample landed: the next sweep runs `tillr-gate enable` and clears the marker.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = 'flaky-fast-sample'`, StateLanded)
		return err
	})

	l.sweepDisabledMarkers(context.Background())
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("expected landed repair sweep to remove disabled marker %s, stat err=%v", markerPath, err)
	}
	enabledRaw, err := os.ReadFile(filepath.Join(resultsStore, "enabled", checkMarkerHash("fast-sample")+".json"))
	if err != nil {
		t.Fatalf("expected enabled record written by tillr-gate enable: %v", err)
	}
	if !strings.Contains(string(enabledRaw), epRotated) {
		t.Fatalf("expected enabled record to list cleared episode %q, got:\n%s", epRotated, enabledRaw)
	}

	// A new episode after the first repair landed files flaky-fast-sample-2.
	ep2 := checkresult.FormatEpisode("fp-ep2", "2026-11-21T05:00:00Z", "2026-11-21T05:03:00Z")
	writeDisabledMarkerFixture(t, resultsStore, checkresult.DisabledMarker{
		CheckID:     "fast-sample",
		Episode:     ep2,
		Fingerprint: "fp-ep2",
		PassAt:      "2026-11-21T05:00:00Z",
		PassLogRef:  "/store/disabled/ep2.pass.log",
		FailAt:      "2026-11-21T05:03:00Z",
		FailLogRef:  "/store/disabled/ep2.fail.log",
	})
	l.sweepDisabledMarkers(context.Background())

	title2, state2, criterion2 := readFiledFlakyItem(t, f.storePath, "flaky-fast-sample-2")
	if title2 != "re-enable check `fast-sample` correctly" || state2 != StateInQueue {
		t.Fatalf("unexpected second item: title=%q state=%q", title2, state2)
	}
	if !strings.Contains(criterion2, ep2) {
		t.Fatalf("second item criterion missing episode %q:\n%s", ep2, criterion2)
	}
}

type markerWritingReplaySteps struct {
	recordingSteps
	resultsStore string
	cancelFn     context.CancelFunc
	replayErr    error
}

func (s *markerWritingReplaySteps) Replay(_ context.Context, _ Candidate, _ string, checks []string) (ReplayOutcome, error) {
	for _, id := range checks {
		ep := checkresult.FormatEpisode("fp-"+id, "2026-11-21T06:00:00Z", "2026-11-21T06:01:00Z")
		disabledDir := filepath.Join(s.resultsStore, "disabled")
		_ = os.MkdirAll(disabledDir, 0o755)
		raw, _ := json.Marshal(checkresult.DisabledMarker{
			CheckID:     id,
			Episode:     ep,
			Fingerprint: "fp-" + id,
			PassAt:      "2026-11-21T06:00:00Z",
			FailAt:      "2026-11-21T06:01:00Z",
		})
		_ = os.WriteFile(filepath.Join(disabledDir, checkMarkerHash(id)+".json"), append(raw, '\n'), 0o644)
	}
	if s.cancelFn != nil {
		s.cancelFn()
	}
	return ReplayOutcome{}, s.replayErr
}

func TestDisabledMarkerSweepFilesOnCancelledAndErroredSteps(t *testing.T) {
	f, l := passFixture(t)
	resultsStore := filepath.Join(f.root, ".git", "tillr-gate", "results")

	// 1. Replay step returns an error after writing a marker.
	errSteps := &markerWritingReplaySteps{
		resultsStore: resultsStore,
		replayErr:    errors.New("simulated replay error"),
	}
	l.cfg.Steps = errSteps
	_, _, _ = l.judgeTrunkChecks(context.Background(), errSteps, Candidate{ItemID: "item-x"}, "deadbeef", []string{"err-check"}, false)

	title1, state1, _ := readFiledFlakyItem(t, f.storePath, "flaky-err-check")
	if title1 != "re-enable check `err-check` correctly" || state1 != StateInQueue {
		t.Fatalf("errored replay did not file repair item: title=%q state=%q", title1, state1)
	}

	// 2. Replay step cancels its context and returns context.Canceled after writing a marker.
	ctx, cancel := context.WithCancel(context.Background())
	cancelSteps := &markerWritingReplaySteps{
		resultsStore: resultsStore,
		cancelFn:     cancel,
		replayErr:    context.Canceled,
	}
	l.cfg.Steps = cancelSteps
	_, _, _ = l.judgeTrunkChecks(ctx, cancelSteps, Candidate{ItemID: "item-y"}, "deadbeef", []string{"cancel-check"}, false)

	title2, state2, _ := readFiledFlakyItem(t, f.storePath, "flaky-cancel-check")
	if title2 != "re-enable check `cancel-check` correctly" || state2 != StateInQueue {
		t.Fatalf("cancelled replay did not file repair item: title=%q state=%q", title2, state2)
	}
}
