package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type gateRecoveryTestSteps struct {
	recordingSteps
	storePath        string
	gateFn           func(ctx context.Context, c Candidate) (GateOutcome, error)
	gateWithBinaryFn func(ctx context.Context, c Candidate, gateBin string) (GateOutcome, error)
	gateCalls        int
	recoveryCalls    int
	lastRecoveryBin  string
}

func (s *gateRecoveryTestSteps) ItemState(ctx context.Context, id, runID, state string, waiting ...item.WaitingOn) error {
	if s.storePath == "" {
		return nil
	}
	st, err := store.Open(ctx, s.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.SetState(ctx, tx, id, state, time.Now().UTC(), waiting...)
		return err
	})
}

func (s *gateRecoveryTestSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.gateCalls++
	if s.gateFn != nil {
		return s.gateFn(ctx, c)
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *gateRecoveryTestSteps) GateWithBinary(ctx context.Context, c Candidate, gateBin string) (GateOutcome, error) {
	s.recoveryCalls++
	s.lastRecoveryBin = gateBin
	if s.gateWithBinaryFn != nil {
		return s.gateWithBinaryFn(ctx, c, gateBin)
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

func installFakeTillrGateOnPath(t *testing.T, body string) (string, string) {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "tillr-gate")
	if err := os.WriteFile(binPath, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return binPath, fileDigest(binPath)
}

func writeMinimalGateModule(t *testing.T, dir string, tag string) {
	t.Helper()
	writeForkFile(t, filepath.Join(dir, "tools", "tillr-gate", "go.mod"), "module example.com/test-tillr-gate\n\ngo 1.22\n")
	writeForkFile(t, filepath.Join(dir, "tools", "tillr-gate", "main.go"), "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println("+`"`+tag+`"`+") }\n")
}

func writePassingDirectChecks(t *testing.T, dir string) {
	t.Helper()
	writeForkFile(t, filepath.Join(dir, "tools", "gate", "checks", "fast-ok.json"),
		`{"id":"fast-ok","label":"fast ok","scope":"tree","tier":"fast","budget_ms":100,"reads":["."],"command":["true"]}`)
	writeForkFile(t, filepath.Join(dir, "tools", "gate", "checks", "change-ok.json"),
		`{"id":"change-ok","label":"change ok","scope":"change","tier":"fast","budget_ms":100,"reads":["."],"command":["sh","-c","test -n \"$1\" && test -n \"$2\"","change-ok"]}`)
}

func writeRecoveryTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stageCandidateForRecoveryTest(t *testing.T, f refusalLoopFixture, l *Loop, itemID, laneID string, files map[string]string, fence []string) Candidate {
	t.Helper()
	if len(fence) > 0 {
		rawFence, err := json.Marshal(fence)
		if err != nil {
			t.Fatal(err)
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET allowed_paths=? WHERE item_id=?`, string(rawFence), itemID)
			return err
		})
	} else {
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET allowed_paths='[]' WHERE item_id=?`, itemID)
			return err
		})
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	var planned *plannedApplication
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == itemID {
			cp := p
			planned = &cp
			break
		}
	}
	if planned == nil {
		t.Fatalf("no dispatch plan for %s", itemID)
	}
	if laneID != "" {
		planned.decision.LaneID = laneID
		if planned.decision.RunRequest != nil {
			planned.decision.RunRequest.LaneID = laneID
		}
	}
	if ok, _, err := l.applyPlan(context.Background(), *planned, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("applyPlan %s: ok=%v err=%v", itemID, ok, err)
	}
	lane := planned.decision.LaneID
	lanePath := l.lanePath(lane)
	runGit(t, lanePath, "reset", "--hard", base)
	runGit(t, lanePath, "clean", "-fd")
	for rel, content := range files {
		writeRecoveryTestFile(t, filepath.Join(lanePath, rel), content)
	}
	runGit(t, lanePath, "add", "-A")
	runGit(t, lanePath, "commit", "-qm", "candidate for "+itemID)
	head := runGit(t, lanePath, "rev-parse", "HEAD")

	c := Candidate{
		ItemID:   itemID,
		LaneID:   lane,
		RunID:    planned.decision.RunRequest.RunID,
		Worktree: lanePath,
		BaseSHA:  base,
		HeadSHA:  head,
		Request:  planned.decision.RunRequest,
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		rec := run.Record{
			RunID:        c.RunID,
			ItemID:       c.ItemID,
			LaneID:       c.LaneID,
			Backend:      "fixture",
			State:        run.StateExited,
			ExitCode:     countPtr(0),
			StartedAt:    l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"),
			FinishedAt:   l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"),
			ArtifactRefs: []run.ArtifactRef{},
		}
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		return appendEffect(tx, candidateReadyKind, c.RunID, c, l.cfg.Now())
	})
	return c
}

func runGitAllowError(dir string, args ...string) (string, error) {
	return runGitOutput(dir, args...)
}

func runGitOutput(dir string, args ...string) (string, error) {
	ctx := context.Background()
	l := &Loop{}
	return l.git(ctx, dir, args...)
}

func readGateRecoveryEvents(t *testing.T, storePath string) []gateRecoveryEvent {
	t.Helper()
	st, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var out []gateRecoveryEvent
	if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
		evs, err := r.EventsOfKind(0, GateRecoveryKind)
		if err != nil {
			return err
		}
		for _, e := range evs {
			var ev gateRecoveryEvent
			if err := json.Unmarshal(e.Payload, &ev); err != nil {
				return err
			}
			out = append(out, ev)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDirectGateChecksRespectsChecksDirScopeAndReads(t *testing.T) {
	dir := initDrainChecksDirRepo(t, "custom/checks", true)
	base := runGit(t, dir, "rev-parse", "HEAD")

	// Fast check that reads src/, change check that requires base/head args,
	// and an unrelated fast check that would fail if executed when its reads don't match.
	writeForkFile(t, filepath.Join(dir, "custom", "checks", "fast-src.json"),
		`{"id":"fast-src","label":"fast src","scope":"tree","tier":"fast","budget_ms":100,"reads":["src/"],"command":["true"]}`)
	writeForkFile(t, filepath.Join(dir, "custom", "checks", "change-args.json"),
		`{"id":"change-args","label":"change args","scope":"change","tier":"heavy","timeout_ms":1000,"reads":["src/"],"command":["sh","-c","test \"$1\" = \""+`+base+`+"\" && test -n \"$2\"","change-args"]}`)
	// Use a clean shell check verifying $1 and $2 are non-empty and distinct.
	writeForkFile(t, filepath.Join(dir, "custom", "checks", "change-args.json"),
		`{"id":"change-args","label":"change args","scope":"change","tier":"heavy","timeout_ms":1000,"reads":["src/"],"command":["sh","-c","test -n \"$1\" && test -n \"$2\" && test \"$1\" != \"$2\"","change-args"]}`)
	writeForkFile(t, filepath.Join(dir, "custom", "checks", "unrelated-fail.json"),
		`{"id":"unrelated-fail","label":"unrelated","scope":"tree","tier":"fast","budget_ms":100,"reads":["other/"],"command":["false"]}`)
	// Heavy tree check must NOT be executed by direct fast/landing verifier.
	writeForkFile(t, filepath.Join(dir, "custom", "checks", "heavy-tree.json"),
		`{"id":"heavy-tree","label":"heavy tree","scope":"tree","tier":"heavy","timeout_ms":1000,"reads":["."],"command":["false"]}`)
	writeForkFile(t, filepath.Join(dir, "src", "app.txt"), "changed\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "update src")
	head := runGit(t, dir, "rev-parse", "HEAD")

	ok, detail, err := runDirectGateChecks(context.Background(), dir, base, head, "")
	if err != nil || !ok {
		t.Fatalf("runDirectGateChecks = (%v, %q, %v), want (true, \"\", nil)", ok, detail, err)
	}

	// Now touch other/ so unrelated-fail is selected and fails.
	writeRecoveryTestFile(t, filepath.Join(dir, "other", "f.txt"), "boom\n")
	runGit(t, dir, "add", "other/f.txt")
	runGit(t, dir, "commit", "-qm", "touch other")
	head2 := runGit(t, dir, "rev-parse", "HEAD")

	ok, detail, err = runDirectGateChecks(context.Background(), dir, base, head2, "")
	if err != nil || ok || !strings.Contains(detail, "unrelated-fail") {
		t.Fatalf("runDirectGateChecks with failing check = (%v, %q, %v), want false naming unrelated-fail", ok, detail, err)
	}
}

func TestGateHealthyPathUsesInstalledBinaryWithoutRecovery(t *testing.T) {
	_, _ = installFakeTillrGateOnPath(t, "#!/bin/sh\nexit 0\n")
	f, l := passFixture(t, "item-a")
	steps := &gateRecoveryTestSteps{}
	l.cfg.Steps = steps

	c := stageCandidateForRecoveryTest(t, f, l, "item-a", "lane-1", map[string]string{
		"tools/tillr-gate/go.mod":             "module example.com/test-tillr-gate\n\ngo 1.22\n",
		"tools/tillr-gate/main.go":            "package main\nfunc main() {}\n",
		"tools/gate/checks/fast-ok.json":      `{"id":"fast-ok","label":"ok","scope":"tree","tier":"fast","budget_ms":100,"reads":["."],"command":["true"]}`,
		"tools/gate/checks/change-ok.json":    `{"id":"change-ok","label":"ok","scope":"change","tier":"fast","budget_ms":100,"reads":["."],"command":["true"]}`,
		"tools/tillr-gate/healthy_marker.txt": "ok\n",
	}, []string{"tools/tillr-gate/"})

	_, landed, err := l.verify(context.Background(), &c, &LoopResult{}, nil)
	if err != nil || !landed {
		t.Fatalf("healthy verify = (landed=%v, err=%v), want landed=true", landed, err)
	}
	if steps.gateCalls != 1 || steps.recoveryCalls != 0 {
		t.Fatalf("calls: gate=%d recovery=%d, want 1 and 0", steps.gateCalls, steps.recoveryCalls)
	}
	if evs := readGateRecoveryEvents(t, f.storePath); len(evs) != 0 {
		t.Fatalf("healthy path emitted recovery events: %+v", evs)
	}
}

func TestGateRecoveryPathBindsCandidateBuildAndEmitsBothDigests(t *testing.T) {
	installedPath, installedDigest := installFakeTillrGateOnPath(t, "#!/bin/sh\n# installed-v1\nexit 6\n")
	f, l := passFixture(t, "item-gate-fix")
	steps := &gateRecoveryTestSteps{
		gateFn: func(ctx context.Context, c Candidate) (GateOutcome, error) {
			return GateOutcome{
				Outcome:       GateStalled,
				StalledChecks: []StallObservation{{CheckID: "documents", CPUTimeMS: 1, SilentSamples: 60}},
			}, nil
		},
	}
	l.cfg.Steps = steps

	c := stageCandidateForRecoveryTest(t, f, l, "item-gate-fix", "lane-1", map[string]string{
		"tools/tillr-gate/go.mod":          "module example.com/test-tillr-gate\n\ngo 1.22\n",
		"tools/tillr-gate/main.go":         "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"candidate-v2\") }\n",
		"tools/gate/checks/fast-ok.json":   `{"id":"fast-ok","label":"ok","scope":"tree","tier":"fast","budget_ms":100,"reads":["."],"command":["true"]}`,
		"tools/gate/checks/change-ok.json": `{"id":"change-ok","label":"ok","scope":"change","tier":"fast","budget_ms":100,"reads":["."],"command":["sh","-c","test -n \"$1\" && test -n \"$2\"","change-ok"]}`,
	}, []string{"tools/tillr-gate/"})

	_, landed, err := l.verify(context.Background(), &c, &LoopResult{}, nil)
	if err != nil || !landed {
		t.Fatalf("recovery verify = (landed=%v, err=%v), want landed=true", landed, err)
	}
	if steps.recoveryCalls != 1 || steps.lastRecoveryBin == "" {
		t.Fatalf("expected 1 recovery gate call with candidate binary, got calls=%d bin=%q", steps.recoveryCalls, steps.lastRecoveryBin)
	}
	// Installed binary on PATH must remain untouched.
	if got := fileDigest(installedPath); got != installedDigest {
		t.Fatalf("installed binary digest changed: got %s, want %s", got, installedDigest)
	}
	wantCandDigest := fileDigest(steps.lastRecoveryBin)
	if wantCandDigest == "" || wantCandDigest == installedDigest {
		t.Fatalf("candidate binary digest invalid or equal to installed: %q", wantCandDigest)
	}
	evs := readGateRecoveryEvents(t, f.storePath)
	if len(evs) != 1 {
		t.Fatalf("gate recovery events = %d, want 1", len(evs))
	}
	ev := evs[0]
	if ev.ItemID != c.ItemID || ev.RunID != c.RunID || ev.LaneID != c.LaneID || ev.HeadSHA != c.HeadSHA ||
		ev.Trigger != GateStalled || ev.InstalledDigest != installedDigest || ev.CandidateDigest != wantCandDigest {
		t.Fatalf("unexpected recovery event: %+v (want installed=%s candidate=%s)", ev, installedDigest, wantCandDigest)
	}
}

func TestGateRecoveryRefusesWhenDirectChecksOrCandidateGateFails(t *testing.T) {
	t.Run("direct check fails", func(t *testing.T) {
		_, _ = installFakeTillrGateOnPath(t, "#!/bin/sh\nexit 4\n")
		f, l := passFixture(t, "item-direct-fail")
		steps := &gateRecoveryTestSteps{
			gateFn: func(ctx context.Context, c Candidate) (GateOutcome, error) {
				return GateOutcome{Outcome: GateErrored, ErrorDetail: "flag provided but not defined: -bad"}, nil
			},
		}
		l.cfg.Steps = steps
		c := stageCandidateForRecoveryTest(t, f, l, "item-direct-fail", "lane-1", map[string]string{
			"tools/tillr-gate/go.mod":          "module example.com/test-tillr-gate\n\ngo 1.22\n",
			"tools/tillr-gate/main.go":         "package main\nfunc main() {}\n",
			"tools/gate/checks/fast-fail.json": `{"id":"fast-fail","label":"fail","scope":"tree","tier":"fast","budget_ms":100,"reads":["."],"command":["false"]}`,
		}, []string{"tools/tillr-gate/"})

		_, landed, err := l.verify(context.Background(), &c, &LoopResult{}, nil)
		if landed || err == nil {
			t.Fatalf("expected failure when direct check fails, got landed=%v err=%v", landed, err)
		}
		if steps.recoveryCalls != 0 {
			t.Fatalf("candidate gate should not run when direct checks fail, got %d calls", steps.recoveryCalls)
		}
		if evs := readGateRecoveryEvents(t, f.storePath); len(evs) != 0 {
			t.Fatalf("no recovery event should be emitted when direct checks fail: %+v", evs)
		}
	})

	t.Run("candidate gate fails", func(t *testing.T) {
		_, _ = installFakeTillrGateOnPath(t, "#!/bin/sh\nexit 6\n")
		f, l := passFixture(t, "item-cand-fail")
		steps := &gateRecoveryTestSteps{
			gateFn: func(ctx context.Context, c Candidate) (GateOutcome, error) {
				return GateOutcome{
					Outcome:       GateStalled,
					StalledChecks: []StallObservation{{CheckID: "documents", CPUTimeMS: 1, SilentSamples: 60}},
				}, nil
			},
			gateWithBinaryFn: func(ctx context.Context, c Candidate, gateBin string) (GateOutcome, error) {
				return GateOutcome{Outcome: GateFailed}, nil
			},
		}
		l.cfg.Steps = steps
		c := stageCandidateForRecoveryTest(t, f, l, "item-cand-fail", "lane-1", map[string]string{
			"tools/tillr-gate/go.mod":        "module example.com/test-tillr-gate\n\ngo 1.22\n",
			"tools/tillr-gate/main.go":       "package main\nfunc main() {}\n",
			"tools/gate/checks/fast-ok.json": `{"id":"fast-ok","label":"ok","scope":"tree","tier":"fast","budget_ms":100,"reads":["."],"command":["true"]}`,
		}, []string{"tools/tillr-gate/"})

		_, landed, err := l.verify(context.Background(), &c, &LoopResult{}, nil)
		if landed || err == nil {
			t.Fatalf("expected hold when candidate gate fails, got landed=%v err=%v", landed, err)
		}
		if steps.recoveryCalls != 1 {
			t.Fatalf("expected 1 candidate gate attempt, got %d", steps.recoveryCalls)
		}
		if evs := readGateRecoveryEvents(t, f.storePath); len(evs) != 0 {
			t.Fatalf("no recovery event should be emitted when candidate gate fails: %+v", evs)
		}
	})
}

func TestGateNonGateAcquittalDeduplicatesDefectAndReleasesOnLanding(t *testing.T) {
	_, _ = installFakeTillrGateOnPath(t, "#!/bin/sh\n# broken-installed-v1\nexit 6\n")
	f, l := passFixture(t, "item-a", "item-b")
	steps := &gateRecoveryTestSteps{
		storePath: f.storePath,
		gateFn: func(ctx context.Context, c Candidate) (GateOutcome, error) {
			if c.ItemID == "item-a" {
				return GateOutcome{
					Outcome:       GateStalled,
					StalledChecks: []StallObservation{{CheckID: "documents", CPUTimeMS: 1, SilentSamples: 60}},
				}, nil
			}
			return GateOutcome{Outcome: GateErrored, ErrorDetail: "flag provided but not defined: -bad"}, nil
		},
	}
	l.cfg.Steps = steps

	// Non-gate item-a hits two stalls; direct checks pass -> acquitted behind defect item.
	ca := stageCandidateForRecoveryTest(t, f, l, "item-a", "lane-1", map[string]string{
		"docs/a.md":                        "hello a\n",
		"tools/gate/checks/fast-ok.json":   `{"id":"fast-ok","label":"ok","scope":"tree","tier":"fast","budget_ms":100,"reads":["."],"command":["true"]}`,
		"tools/gate/checks/change-ok.json": `{"id":"change-ok","label":"ok","scope":"change","tier":"fast","budget_ms":100,"reads":["."],"command":["sh","-c","test -n \"$1\" && test -n \"$2\"","change-ok"]}`,
	}, []string{"docs/"})

	// First stall -> held for rerun (ReasonGateStallRerun).
	_, landed, err := l.verify(context.Background(), &ca, &LoopResult{}, nil)
	if landed || err != errGateStallHeld {
		t.Fatalf("first stall = (landed=%v, err=%v), want errGateStallHeld", landed, err)
	}
	// Second stall -> direct execution passes -> acquitted behind gate defect item.
	_, landed, err = l.verify(context.Background(), &ca, &LoopResult{}, nil)
	if landed || err != nil {
		t.Fatalf("second stall acquittal = (landed=%v, err=%v), want (false, nil)", landed, err)
	}

	// Non-gate item-b hits deterministic gate error; direct checks pass -> attaches to SAME defect item.
	cb := stageCandidateForRecoveryTest(t, f, l, "item-b", "lane-2", map[string]string{
		"docs/b.md":                        "hello b\n",
		"tools/gate/checks/fast-ok.json":   `{"id":"fast-ok","label":"ok","scope":"tree","tier":"fast","budget_ms":100,"reads":["."],"command":["true"]}`,
		"tools/gate/checks/change-ok.json": `{"id":"change-ok","label":"ok","scope":"change","tier":"fast","budget_ms":100,"reads":["."],"command":["sh","-c","test -n \"$1\" && test -n \"$2\"","change-ok"]}`,
	}, []string{"docs/"})
	_, landed, err = l.verify(context.Background(), &cb, &LoopResult{}, nil)
	if landed || err != nil {
		t.Fatalf("item-b deterministic error acquittal = (landed=%v, err=%v), want (false, nil)", landed, err)
	}

	// Verify both items are blocked waiting on ONE deduplicated defect item fenced to tillr-gate.
	defectID := l.gateBinaryDefectID()
	if !IsDefectItem(defectID) {
		t.Fatalf("defectID %q is not IsDefectItem", defectID)
	}
	st, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	var defectFenceRaw string
	if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
		for _, id := range []string{"item-a", "item-b"} {
			var state string
			if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state); err != nil {
				return err
			}
			if state != StateBlocked {
				t.Fatalf("%s state = %q, want %q", id, state, StateBlocked)
			}
			wait, _, err := item.ReadWaitingOn(r, id)
			if err != nil || wait == nil || wait.Waiter != "item:"+defectID {
				t.Fatalf("%s waiting_on = %+v (err=%v), want item:%s", id, wait, err, defectID)
			}
		}
		var defectCount int
		if err := r.QueryRow(`SELECT COUNT(*) FROM items WHERE item_id LIKE 'defect-%'`).Scan(&defectCount); err != nil {
			return err
		}
		if defectCount != 1 {
			t.Fatalf("defect item count = %d, want 1", defectCount)
		}
		return r.QueryRow(`SELECT COALESCE(allowed_paths, '[]') FROM items WHERE item_id=?`, defectID).Scan(&defectFenceRaw)
	}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	var defectFence []string
	if err := json.Unmarshal([]byte(defectFenceRaw), &defectFence); err != nil {
		t.Fatal(err)
	}
	if !fencePermitsGateSource(defectFence) || !insideFence("yolo-packs/tillr/files/local/share/tillr/tillr-gate/main.go", defectFence) {
		t.Fatalf("defect fence %v does not cover tools/tillr-gate/ and pack mirror", defectFence)
	}

	// Mark the defect item landed (and proved) and verify releaseTrunkAcquittals releases item-a and item-b.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, StateLanded, defectID)
		return err
	})
	if err := l.releaseTrunkAcquittals(context.Background()); err != nil {
		t.Fatalf("releaseTrunkAcquittals: %v", err)
	}
	st, err = store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
		for _, id := range []string{"item-a", "item-b"} {
			var state string
			if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state); err != nil {
				return err
			}
			if state != StateInQueue {
				t.Fatalf("after defect landed, %s state = %q, want %q", id, state, StateInQueue)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGateDigestAdvanceClearsStallAndHarnessErrorHolds(t *testing.T) {
	binPath, digestV1 := installFakeTillrGateOnPath(t, "#!/bin/sh\n# v1\nexit 0\n")
	f, l := passFixture(t, "item-stalled", "item-errored", "item-other")

	cStalled := Candidate{ItemID: "item-stalled", LaneID: "lane-1", RunID: "run-1", HeadSHA: "aaa"}
	cErrored := Candidate{ItemID: "item-errored", LaneID: "lane-2", RunID: "run-2", HeadSHA: "bbb"}
	cOther := Candidate{ItemID: "item-other", LaneID: "lane-3", RunID: "run-3", HeadSHA: "ccc"}

	if err := l.holdItemFault(context.Background(), cStalled, ReasonGateStalled, "stalled twice"); err != nil {
		t.Fatal(err)
	}
	if err := l.holdItemFault(context.Background(), cErrored, StopGateHarnessError, "usage error"); err != nil {
		t.Fatal(err)
	}
	if err := l.holdItemFault(context.Background(), cOther, ReasonLandHarnessError, "land error"); err != nil {
		t.Fatal(err)
	}

	// Verify GateDigest is recorded on ReasonGateStalled and StopGateHarnessError only.
	readFaults := func() map[string]ItemFault {
		st, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		var out map[string]ItemFault
		if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
			var err error
			out, err = ReadItemFaults(r)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	faults := readFaults()
	if faults["item-stalled"].GateDigest != digestV1 {
		t.Fatalf("stalled GateDigest = %q, want %q", faults["item-stalled"].GateDigest, digestV1)
	}
	if faults["item-errored"].GateDigest != digestV1 {
		t.Fatalf("errored GateDigest = %q, want %q", faults["item-errored"].GateDigest, digestV1)
	}
	if faults["item-other"].GateDigest != "" {
		t.Fatalf("other fault GateDigest = %q, want empty", faults["item-other"].GateDigest)
	}

	// Same installed digest: holds must remain active.
	if err := l.releaseAdvancedGateDigestFaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	faults = readFaults()
	if _, ok := faults["item-stalled"]; !ok {
		t.Fatalf("item-stalled cleared while digest unchanged")
	}
	if _, ok := faults["item-errored"]; !ok {
		t.Fatalf("item-errored cleared while digest unchanged")
	}

	// Advance installed tillr-gate binary on PATH -> both gate holds clear, unrelated hold stays.
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n# v2\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseAdvancedGateDigestFaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	faults = readFaults()
	if _, ok := faults["item-stalled"]; ok {
		t.Fatalf("item-stalled still held after installed tillr-gate digest advanced")
	}
	if _, ok := faults["item-errored"]; ok {
		t.Fatalf("item-errored still held after installed tillr-gate digest advanced")
	}
	if _, ok := faults["item-other"]; !ok {
		t.Fatalf("unrelated land-harness-error hold was unexpectedly cleared")
	}
}

func TestGateRecoveryUnrestrictedFenceRequiresGateTouch(t *testing.T) {
	_, _ = installFakeTillrGateOnPath(t, "#!/bin/sh\nexit 6\n")
	f, l := passFixture(t, "item-touches-gate", "item-no-gate")
	steps := &gateRecoveryTestSteps{
		gateFn: func(ctx context.Context, c Candidate) (GateOutcome, error) {
			return GateOutcome{
				Outcome:       GateStalled,
				StalledChecks: []StallObservation{{CheckID: "documents", CPUTimeMS: 1, SilentSamples: 60}},
			}, nil
		},
	}
	l.cfg.Steps = steps

	cTouch := stageCandidateForRecoveryTest(t, f, l, "item-touches-gate", "lane-1", map[string]string{
		"tools/tillr-gate/go.mod":        "module example.com/test-tillr-gate\n\ngo 1.22\n",
		"tools/tillr-gate/main.go":       "package main\nfunc main() {}\n",
		"tools/gate/checks/fast-ok.json": `{"id":"fast-ok","label":"ok","scope":"tree","tier":"fast","budget_ms":100,"reads":["."],"command":["true"]}`,
	}, nil)
	_, landed, err := l.verify(context.Background(), &cTouch, &LoopResult{}, nil)
	if err != nil || !landed {
		t.Fatalf("unrestricted candidate touching tools/tillr-gate/ should recover, got landed=%v err=%v", landed, err)
	}
	if steps.recoveryCalls != 1 {
		t.Fatalf("expected 1 recovery call, got %d", steps.recoveryCalls)
	}

	cNoTouch := stageCandidateForRecoveryTest(t, f, l, "item-no-gate", "lane-2", map[string]string{
		"docs/note.md":                   "unrelated\n",
		"tools/gate/checks/fast-ok.json": `{"id":"fast-ok","label":"ok","scope":"tree","tier":"fast","budget_ms":100,"reads":["."],"command":["true"]}`,
	}, nil)
	_, landed, err = l.verify(context.Background(), &cNoTouch, &LoopResult{}, nil)
	if landed || err != errGateStallHeld {
		t.Fatalf("unrestricted candidate not touching tools/tillr-gate/ should not recover, got landed=%v err=%v", landed, err)
	}
	if steps.recoveryCalls != 1 {
		t.Fatalf("recovery calls should still be 1, got %d", steps.recoveryCalls)
	}
}

func TestProcessStepsGateWithBinaryPrependsPath(t *testing.T) {
	f, l := passFixture(t, "item-a")
	candDir := t.TempDir()
	candBin := filepath.Join(candDir, "tillr-gate")
	if err := os.WriteFile(candBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gotPathFile := filepath.Join(t.TempDir(), "path.txt")
	prog := executable(t, `printf '%s' "$PATH" > `+gotPathFile+`; exit 0`)
	ps := &ProcessSteps{
		Root:        f.root,
		LaneRoot:    f.laneRoot,
		Store:       f.storePath,
		GateProgram: prog,
		Now:         l.cfg.Now,
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	c := Candidate{
		ItemID:   "item-a",
		LaneID:   "lane-1",
		RunID:    "run-a",
		Worktree: f.root,
		BaseSHA:  base,
		HeadSHA:  base,
	}
	out, err := ps.GateWithBinary(context.Background(), c, candBin)
	if err != nil || out.Outcome != GatePassed {
		t.Fatalf("GateWithBinary = (%+v, %v), want GatePassed", out, err)
	}
	rawPath, err := os.ReadFile(gotPathFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(rawPath), candDir+string(os.PathListSeparator)) {
		t.Fatalf("PATH %q does not start with candidate bin dir %q", string(rawPath), candDir)
	}
}

func TestGateNonGateDirectCheckFailureHoldsInsteadOfAcquitting(t *testing.T) {
	_, _ = installFakeTillrGateOnPath(t, "#!/bin/sh\n# broken-installed-v1\nexit 6\n")

	t.Run("second stall", func(t *testing.T) {
		f, l := passFixture(t, "item-a")
		steps := &gateRecoveryTestSteps{
			storePath: f.storePath,
			gateFn: func(ctx context.Context, c Candidate) (GateOutcome, error) {
				return GateOutcome{
					Outcome:       GateStalled,
					StalledChecks: []StallObservation{{CheckID: "documents", CPUTimeMS: 1, SilentSamples: 60}},
				}, nil
			},
		}
		l.cfg.Steps = steps

		ca := stageCandidateForRecoveryTest(t, f, l, "item-a", "lane-1", map[string]string{
			"docs/a.md":                        "hello a\n",
			"tools/gate/checks/fast-fail.json": `{"id":"fast-fail","label":"fail","scope":"tree","tier":"fast","budget_ms":100,"reads":["."],"command":["false"]}`,
		}, []string{"docs/"})

		// First stall -> held for rerun.
		_, landed, err := l.verify(context.Background(), &ca, &LoopResult{}, nil)
		if landed || err != errGateStallHeld {
			t.Fatalf("first stall = (landed=%v, err=%v), want errGateStallHeld", landed, err)
		}
		// Second stall with failing direct check -> held normally, NOT acquitted.
		_, landed, err = l.verify(context.Background(), &ca, &LoopResult{}, nil)
		if landed || err != errItemFaultHeld {
			t.Fatalf("second stall with direct failure = (landed=%v, err=%v), want errItemFaultHeld", landed, err)
		}
		if steps.recoveryCalls != 0 {
			t.Fatalf("non-gate item should not trigger candidate recovery, got %d calls", steps.recoveryCalls)
		}

		st, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
			var defectCount int
			if err := r.QueryRow(`SELECT COUNT(*) FROM items WHERE item_id LIKE 'defect-%'`).Scan(&defectCount); err != nil {
				return err
			}
			if defectCount != 0 {
				t.Fatalf("defect item count = %d, want 0", defectCount)
			}
			faults, err := ReadItemFaults(r)
			if err != nil {
				return err
			}
			flt, ok := faults["item-a"]
			if !ok || flt.Reason != ReasonGateStalled {
				t.Fatalf("item-a fault = %+v (ok=%v), want %q", flt, ok, ReasonGateStalled)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("deterministic error", func(t *testing.T) {
		f, l := passFixture(t, "item-b")
		steps := &gateRecoveryTestSteps{
			storePath: f.storePath,
			gateFn: func(ctx context.Context, c Candidate) (GateOutcome, error) {
				return GateOutcome{Outcome: GateErrored, ErrorDetail: "flag provided but not defined: -bad"}, nil
			},
		}
		l.cfg.Steps = steps

		cb := stageCandidateForRecoveryTest(t, f, l, "item-b", "lane-1", map[string]string{
			"docs/b.md":                        "hello b\n",
			"tools/gate/checks/fast-fail.json": `{"id":"fast-fail","label":"fail","scope":"tree","tier":"fast","budget_ms":100,"reads":["."],"command":["false"]}`,
		}, []string{"docs/"})

		_, landed, err := l.verify(context.Background(), &cb, &LoopResult{}, nil)
		if landed || err == nil {
			t.Fatalf("deterministic error with direct failure = (landed=%v, err=%v), want landed=false and non-nil err", landed, err)
		}
		if steps.recoveryCalls != 0 {
			t.Fatalf("non-gate item should not trigger candidate recovery, got %d calls", steps.recoveryCalls)
		}

		st, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
			var defectCount int
			if err := r.QueryRow(`SELECT COUNT(*) FROM items WHERE item_id LIKE 'defect-%'`).Scan(&defectCount); err != nil {
				return err
			}
			if defectCount != 0 {
				t.Fatalf("defect item count = %d, want 0", defectCount)
			}
			faults, err := ReadItemFaults(r)
			if err != nil {
				return err
			}
			flt, ok := faults["item-b"]
			if !ok || flt.Reason != StopGateHarnessError {
				t.Fatalf("item-b fault = %+v (ok=%v), want %q", flt, ok, StopGateHarnessError)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}
