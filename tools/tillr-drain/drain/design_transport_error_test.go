package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestParsePiTransportError(t *testing.T) {
	const errText = "terminated: Body Timeout Error (UND_ERR_BODY_TIMEOUT)"
	cases := []struct {
		name     string
		log      string
		wantOK   bool
		wantText string
	}{
		{
			name: "real pi retry exhaustion order",
			log: strings.Join([]string{
				`{"type":"auto_retry_start","attempt":3,"maxAttempts":3,"delayMs":8000,"errorMessage":"` + errText + `"}`,
				`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"` + errText + `"}}`,
				`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"` + errText + `","content":[]}]}`,
				`{"type":"auto_retry_end","success":false,"attempt":3,"finalError":"` + errText + `"}`,
				`{"type":"entry_appended","entry":{}}`,
				`{"type":"agent_settled","aborted":false}`,
			}, "\n"),
			wantOK:   true,
			wantText: errText,
		},
		{
			name: "auto_retry_end before agent_end",
			log: strings.Join([]string{
				`{"type":"auto_retry_end","success":false,"attempt":3,"finalError":"` + errText + `"}`,
				`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"` + errText + `","content":[]}]}`,
			}, "\n"),
			wantOK:   true,
			wantText: errText,
		},
		{
			name: "fallback to assistant errorMessage when finalError empty",
			log: strings.Join([]string{
				`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"` + errText + `","content":[]}]}`,
				`{"type":"auto_retry_end","success":false,"attempt":3}`,
			}, "\n"),
			wantOK:   true,
			wantText: errText,
		},
		{
			name: "retry succeeded and ended normally",
			log: strings.Join([]string{
				`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"` + errText + `","content":[]}]}`,
				`{"type":"auto_retry_start","attempt":1,"maxAttempts":3,"delayMs":2000,"errorMessage":"` + errText + `"}`,
				`{"type":"auto_retry_end","success":true,"attempt":1}`,
				`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"done"}]}]}`,
			}, "\n"),
			wantOK: false,
		},
		{
			name: "normal ending with no transport error",
			log: strings.Join([]string{
				`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"no commit"}]}]}`,
				`{"type":"agent_settled","aborted":false}`,
			}, "\n"),
			wantOK: false,
		},
		{
			name: "failed retry superseded by later normal agent_end",
			log: strings.Join([]string{
				`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"` + errText + `","content":[]}]}`,
				`{"type":"auto_retry_end","success":false,"attempt":3,"finalError":"` + errText + `"}`,
				`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"recovered"}]}]}`,
			}, "\n"),
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parsePiTransportError(tc.log)
			if ok != tc.wantOK || got != tc.wantText {
				t.Fatalf("parsePiTransportError() = (%q, %v), want (%q, %v)", got, ok, tc.wantText, tc.wantOK)
			}
		})
	}
}

func TestDesignRunTransportErrorRecordsBackendFailureWithoutRefusalOrConflict(t *testing.T) {
	const errText = "terminated: Body Timeout Error (UND_ERR_BODY_TIMEOUT)"
	for _, tc := range []struct {
		name  string
		piLog string
	}{
		{
			name: "agent_end then auto_retry_end",
			piLog: strings.Join([]string{
				`{"type":"auto_retry_start","attempt":3,"maxAttempts":3,"delayMs":8000,"errorMessage":"` + errText + `"}`,
				`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"` + errText + `","content":[]}]}`,
				`{"type":"auto_retry_end","success":false,"attempt":3,"finalError":"` + errText + `"}`,
				`{"type":"agent_settled","aborted":false}`,
			}, "\n") + "\n",
		},
		{
			name: "auto_retry_end then agent_end",
			piLog: strings.Join([]string{
				`{"type":"auto_retry_end","success":false,"attempt":3,"finalError":"` + errText + `"}`,
				`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"` + errText + `","content":[]}]}`,
			}, "\n") + "\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l := designConflictFixture(t)
			ctx := context.Background()
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			runGit(t, l.laneTarget("lane-1"), "reset", "--hard", base)

			p := decisionFor(t, l, "item-a")
			if p.decision.Action != ActionDispatch || p.decision.BlockConflict == nil {
				t.Fatalf("initial design dispatch missing: %+v", p.decision)
			}
			ok, work, err := l.applyPlan(ctx, p, base, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("applyPlan: ok=%v err=%v", ok, err)
			}
			d := work.Decision
			conflictsBefore := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id='item-a'`)
			raisedBefore := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id='item-a' AND json_extract(payload,'$.status')='raised'`)

			logPath := filepath.Join(t.TempDir(), d.RunRequest.RunID+".log")
			if err := os.WriteFile(logPath, []byte(tc.piLog), 0o600); err != nil {
				t.Fatal(err)
			}
			reportPath := filepath.Join(t.TempDir(), "report.md")
			if err := os.WriteFile(reportPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			rec := run.Record{
				RunID:      d.RunRequest.RunID,
				ItemID:     d.ItemID,
				LaneID:     d.LaneID,
				Backend:    d.RunRequest.Backend,
				State:      run.StateExited,
				ExitCode:   countPtr(0),
				StartedAt:  l.cfg.Now().Format(time.RFC3339),
				FinishedAt: l.cfg.Now().Format(time.RFC3339),
				EndCause:   run.EndNormalExit,
				ArtifactRefs: []run.ArtifactRef{
					{Kind: "report", Ref: reportPath},
					{Kind: "log", Ref: logPath},
				},
			}
			fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })

			var res LoopResult
			out := l.settle(ctx, d, rec, logTail(rec.ArtifactRefs), base, "tick", l.cfg.Now(), &res, &PassTiming{}, nil, false)
			if out.err != nil {
				t.Fatalf("settle: %+v", out)
			}
			if err := l.markExamined(ctx, d); err != nil {
				t.Fatal(err)
			}
			settleDesign(t, l)

			// No refusal recorded.
			if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind=? AND object_id=?`, RefusalKind, rec.RunID); n != 0 {
				t.Fatalf("refusal events = %d, want 0", n)
			}
			// No new item.conflict raised or recorded by settlement.
			if got := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id='item-a'`); got != conflictsBefore {
				t.Fatalf("item.conflict events = %d, want %d (no conflict recorded for transport error)", got, conflictsBefore)
			}
			if got := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id='item-a' AND json_extract(payload,'$.status')='raised'`); got != raisedBefore {
				t.Fatalf("raised item.conflict events = %d, want %d", got, raisedBefore)
			}
			// Backend outcome recorded as infrastructure naming the error text.
			var outcome struct {
				Class AttemptClass `json:"class"`
				Cause string       `json:"cause"`
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var raw []byte
				if err := r.QueryRow(`SELECT o.payload FROM events o JOIN events f ON f.seq = json_extract(o.payload,'$.terminal_seq')
 WHERE o.kind=? AND o.object_id=? AND f.kind=? AND f.object_id=? ORDER BY o.seq DESC LIMIT 1`,
					backendOutcomeKind, rec.Backend, run.FinishKind, rec.RunID).Scan(&raw); err != nil {
					return err
				}
				return json.Unmarshal(raw, &outcome)
			})
			if outcome.Class != AttemptInfrastructure || !strings.Contains(outcome.Cause, errText) {
				t.Fatalf("backend outcome = %+v, want infrastructure naming %q", outcome, errText)
			}
			// Infrastructure failure event recorded naming the error text.
			var failure infrastructureFailure
			fixtureRead(t, f, func(r *store.Reader) error {
				var err error
				failure, err = readRecordedInfrastructureFailure(r, "item-a", "$.run_id", rec.RunID)
				return err
			})
			if failure.RunID != rec.RunID || !strings.Contains(failure.Detail, errText) || !strings.Contains(failure.Words, errText) {
				t.Fatalf("infrastructure failure = %+v, want detail/words naming %q", failure, errText)
			}
			// Once the backend is open, the item goes back to the design seat without raising a conflict.
			if err := ProbeBackend(ctx, f.storePath, f.root, rec.Backend, []string{"/bin/true"}); err != nil {
				t.Fatal(err)
			}
			next := decisionFor(t, l, "item-a").decision
			if next.Action != ActionDispatch || next.Seat != "design" || next.BlockConflict == nil || next.RunRequest == nil {
				t.Fatalf("next decision = %+v, want design dispatch", next)
			}
		})
	}
}

func TestDesignRunNoCommitWithoutTransportErrorStillRecordsNoOpRefusal(t *testing.T) {
	f, l := designConflictFixture(t)
	ctx := context.Background()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, l.laneTarget("lane-1"), "reset", "--hard", base)

	p := decisionFor(t, l, "item-a")
	if p.decision.Action != ActionDispatch || p.decision.BlockConflict == nil {
		t.Fatalf("initial design dispatch missing: %+v", p.decision)
	}
	ok, work, err := l.applyPlan(ctx, p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("applyPlan: ok=%v err=%v", ok, err)
	}
	d := work.Decision
	raisedBefore := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id='item-a' AND json_extract(payload,'$.status')='raised'`)

	logPath := filepath.Join(t.TempDir(), d.RunRequest.RunID+".log")
	piLog := strings.Join([]string{
		`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"I looked around."}]}]}`,
		`{"type":"agent_settled","aborted":false}`,
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(piLog), 0o600); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(reportPath, []byte("I looked around.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := run.Record{
		RunID:      d.RunRequest.RunID,
		ItemID:     d.ItemID,
		LaneID:     d.LaneID,
		Backend:    d.RunRequest.Backend,
		State:      run.StateExited,
		ExitCode:   countPtr(0),
		StartedAt:  l.cfg.Now().Format(time.RFC3339),
		FinishedAt: l.cfg.Now().Format(time.RFC3339),
		EndCause:   run.EndNormalExit,
		ArtifactRefs: []run.ArtifactRef{
			{Kind: "report", Ref: reportPath},
			{Kind: "log", Ref: logPath},
		},
	}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })

	var res LoopResult
	out := l.settle(ctx, d, rec, logTail(rec.ArtifactRefs), base, "tick", l.cfg.Now(), &res, &PassTiming{}, nil, false)
	if out.err != nil {
		t.Fatalf("settle: %+v", out)
	}
	if err := l.markExamined(ctx, d); err != nil {
		t.Fatal(err)
	}
	settleDesign(t, l)

	// No-op refusal is still recorded.
	if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind=? AND object_id=? AND json_extract(payload,'$.step')=?`, RefusalKind, rec.RunID, RefusalNoOp); n != 1 {
		t.Fatalf("no-op refusal events = %d, want 1", n)
	}
	// No infrastructure failure recorded.
	if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind=? AND object_id='item-a'`, infrastructureFailureKind); n != 0 {
		t.Fatalf("infrastructure failure events = %d, want 0", n)
	}
	// Conflict raised again for design seat as before.
	if got := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id='item-a' AND json_extract(payload,'$.status')='raised'`); got != raisedBefore+1 {
		t.Fatalf("raised item.conflict events = %d, want %d", got, raisedBefore+1)
	}
	raisedForDesign(t, f, l, rec.RunID, DesignNextActorField)
}
