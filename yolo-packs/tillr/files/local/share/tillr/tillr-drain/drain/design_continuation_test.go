package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type designRepairSteps struct {
	*returnedLandingSteps
	failures int
	sessions []string
	requests []run.Request
}

func (s *designRepairSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	session, err := ReadSessionID(filepath.Join(s.laneRoot, req.LaneID))
	if err != nil {
		return run.Record{}, "", err
	}
	s.sessions = append(s.sessions, session)
	s.requests = append(s.requests, req)
	return s.continuationSteps.Run(ctx, req)
}

func (s *designRepairSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	if s.gates >= s.failures {
		return s.continuationSteps.Gate(ctx, c)
	}
	s.gates++
	dir := RunDir(s.root, c.RunID)
	log := filepath.Join(dir, "documents.log")
	if err := os.WriteFile(log, []byte("document lint: invalid heading\n"), 0600); err != nil {
		return GateOutcome{}, err
	}
	verdict := fmt.Sprintf(`{"payload":{"checks":[{"check_id":"documents","result":"fail","log_ref":%q}]}}`, log)
	if err := os.WriteFile(filepath.Join(dir, GateFastVerdictFile), []byte(verdict), 0600); err != nil {
		return GateOutcome{}, err
	}
	db, err := store.Open(ctx, s.store, true)
	if err != nil {
		return GateOutcome{}, err
	}
	defer db.Close()
	err = db.WriteTx(ctx, func(tx *store.Tx) error {
		return appendEffect(tx, GateVerdictKind, c.RunID, map[string]string{"result": "fail"}, s.at)
	})
	return GateOutcome{Outcome: GateFailed}, err
}

func TestDesignGateRefusalContinuesRecordedSessionAndLands(t *testing.T) {
	for _, failures := range []int{1, 3} {
		t.Run(fmt.Sprint(failures), func(t *testing.T) {
			f, l := designConflictFixture(t)
			doc := filepath.Join(f.root, "briefs", "item-a.md")
			writeForkFile(t, doc, "# Doc\n\n### Build item a\n\n<!-- tillr: item id=\"item-a\" paths=\"tools/item-a/\" -->\n- **c1:** the first criterion of item-a\n")
			runGit(t, f.root, "add", "briefs/item-a.md")
			runGit(t, f.root, "commit", "-qm", "item definition")
			l.cfg.Lanes = []string{"lane-1"}
			declareWorkerResumeFixture(t, l)
			adapter := l.cfg.Policy.Backends["fixture"].Adapter
			writeForkFile(t, adapter, `#!/bin/sh
set -eu
file=briefs/item-a.md
if [ "$TILLR_SESSION_MODE" = resume ]; then
  grep -q 'Failing check documents' "$TILLR_RESUME_PROMPT_FILE"
  grep -q 'document lint: invalid heading' "$TILLR_RESUME_PROMPT_FILE"
  grep -q "design turn" "$file"
fi
printf 'design turn\n' >> "$file"
git add "$file"
git commit -qm 'design answer'
`)
			if err := os.Chmod(adapter, 0755); err != nil {
				t.Fatal(err)
			}
			steps := &designRepairSteps{returnedLandingSteps: &returnedLandingSteps{continuationSteps: &continuationSteps{t: t, store: f.storePath, laneRoot: f.laneRoot, at: l.cfg.Now()}, root: f.root}, failures: failures}
			l.cfg.Steps = steps
			l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
			// The rolling pass dispatches, settles and examines independently. Replace the
			// loop after the first refusal, before it has planned the continuation.
			for i := 0; i < 3; i++ {
				onePassNoStop(t, l)
			}
			if steps.gates != 1 || steps.lands != 0 {
				t.Fatalf("before restart: gates=%d lands=%d", steps.gates, steps.lands)
			}
			var err error
			l, err = NewLoop(l.cfg)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3*failures; i++ {
				onePassNoStop(t, l)
			}
			if steps.gates != failures+1 || steps.lands != 1 || len(steps.requests) != failures+1 {
				t.Fatalf("gates=%d lands=%d requests=%+v", steps.gates, steps.lands, steps.requests)
			}
			for i, req := range steps.requests {
				if steps.sessions[i] == "" || steps.sessions[i] != steps.sessions[0] || req.LaneID != steps.requests[0].LaneID || req.SessionTurn != (i > 0) || req.Resume {
					t.Fatalf("wrong session execution at %d: %+v %v", i, req, steps.sessions)
				}
			}
			if c, _ := conflictOf(t, f, "item-a"); c.Status != "answered" || c.WaitingOn != "build" || c.AnswerHead == "" {
				t.Fatalf("not answered: %+v", c)
			}
			if got := runGit(t, f.root, "show", TrunkRef+":briefs/item-a.md"); strings.Count(got, "design turn") != failures+1 {
				t.Fatalf("wrong landed answer: %s", got)
			}
		})
	}
}

func TestFailedDesignContinuationRequiresMissingSessionDiagnostic(t *testing.T) {
	const session = "11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		line string
		gone bool
	}{
		{"No session found matching '" + session + "'", true},
		{"No conversation found with session ID: " + session, true},
		{"provider returned HTTP 503", false},
	} {
		t.Run(tc.line, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "run.log")
			writeForkFile(t, log, tc.line+"\n")
			d := Decision{DesignContinuationFrom: "refused", DesignerSessionID: session, RunRequest: &run.Request{SessionTurn: true}}
			rec := run.Record{State: run.StateExited, ExitCode: countPtr(1), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{{Kind: "log", Ref: log}}}
			got, err := failedDesignerResume(d, rec)
			if err != nil || got != tc.gone {
				t.Fatalf("missing session = %v (%v), want %v", got, err, tc.gone)
			}
		})
	}
}

func TestDesignContinuationRejectsStaleClaimForItsLane(t *testing.T) {
	f, l := designConflictFixture(t)
	l.cfg.Lanes = []string{"lane-1"}
	endDesignRun(t, f, l, RefusalGate)
	db, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	seedRefusalItem(t, db, "item-b")
	db.Close()
	stale := decisionFor(t, l, "item-b")
	if stale.decision.Action != ActionDispatch || stale.decision.LaneID != "lane-1" {
		t.Fatalf("fixture plan: %+v", stale.decision)
	}
	settleDesign(t, l)
	ok, _, err := l.applyPlan(context.Background(), stale, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || ok {
		t.Fatalf("stale plan took the design lane: %v %v", ok, err)
	}
}
