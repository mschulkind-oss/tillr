package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

const providerRefusal = "github-copilot API error (400): invalid_request_body"

func providerErrorRecord(runID, itemID, laneID, backend string, refs []run.ArtifactRef) run.Record {
	return run.Record{
		RunID: runID, ItemID: itemID, LaneID: laneID, Backend: backend,
		State: run.StateExited, EndCause: run.EndProviderError, ProviderError: providerRefusal, ExitCode: code(1),
		StartedAt: "2026-11-28T08:22:00Z", FinishedAt: "2026-11-28T08:22:05Z", ArtifactRefs: refs,
	}
}

// A run whose provider refused the last model request is the backend's failure on every
// seat and backend, and its own words are the cause.
func TestProviderErrorRecordIsInfrastructureOnEverySeat(t *testing.T) {
	for _, seat := range []string{"test-run", "build", "scout", "design"} {
		rec := providerErrorRecord("run-"+seat, "item", "lane", "pi-scout", nil)
		rec.Seat = seat
		if got := ClassifyAttempt(rec, ""); got != AttemptInfrastructure {
			t.Errorf("%s: ClassifyAttempt = %s, want infrastructure", seat, got)
		}
		if got := classifyDecisionAttempt(Decision{Seat: seat}, rec, ""); got != AttemptInfrastructure {
			t.Errorf("%s: classifyDecisionAttempt = %s, want infrastructure", seat, got)
		}
		if words := infrastructureWords(rec, ""); words != providerRefusal {
			t.Errorf("%s: infrastructureWords = %q, want the provider's message", seat, words)
		}
	}
}

// Pre-fix logs on disk: pi exited 0 with a final assistant error and no auto_retry_end
// (an HTTP 400 is never retried), on a seat that is not design.
func TestPiTransportErrorReadsAFinalErrorWithoutRetryOnAnySeat(t *testing.T) {
	log := filepath.Join(t.TempDir(), "run.log")
	writeForkFile(t, log, `{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"`+providerRefusal+`","content":[]}}`+"\n"+
		`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"`+providerRefusal+`","content":[]}]}`+"\n")
	rec := run.Record{RunID: "r", ItemID: "i", LaneID: "l", Backend: "pi-scout", Seat: "test-run", State: run.StateExited, EndCause: run.EndNormalExit, ExitCode: code(0),
		ArtifactRefs: []run.ArtifactRef{{Kind: "log", Ref: log}}}
	if words, ok := piTransportError(rec); !ok || words != providerRefusal {
		t.Fatalf("piTransportError = (%q, %v), want the provider's message", words, ok)
	}
	if got := classifyDecisionAttempt(Decision{Seat: "test-run"}, rec, ""); got != AttemptInfrastructure {
		t.Fatalf("classifyDecisionAttempt = %s, want infrastructure", got)
	}
}

// A workflow node whose provider refused its request is an infrastructure failure naming
// the provider's message; it is not restarted as an unreadable report.
func TestProviderRefusedWorkflowNodeIsInfrastructureNotAnUnreadReport(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	ctx := context.Background()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	p := mustPlan(t, l)[0]
	if ok, _, err := l.applyPlan(ctx, p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	d := p.decision
	d.Seat = "test-run"
	runGit(t, l.laneTarget(d.LaneID), "reset", "--hard", base)
	rec := providerErrorRecord(d.RunRequest.RunID, d.ItemID, d.LaneID, "fixture", nil)
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })

	result := &LoopResult{}
	handled, err := l.settleNode(ctx, d, rec, "[Pi's last assistant turn had no text or tool call.]", base, result, false)
	if err != nil || !handled {
		t.Fatalf("settleNode = (%v, %v), want (true, nil)", handled, err)
	}
	if result.InfraFailures != 1 {
		t.Fatalf("InfraFailures = %d, want 1", result.InfraFailures)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		failure, err := readRecordedInfrastructureFailure(r, d.ItemID, "$.run_id", rec.RunID)
		if err != nil {
			return err
		}
		if !strings.Contains(failure.Words, "invalid_request_body") || !strings.Contains(failure.Detail, "invalid_request_body") {
			t.Errorf("infrastructure failure = %+v, want the provider's message as its cause", failure)
		}
		return nil
	})
	restarted := pendingForTest(t, f, d.ItemID)
	if restarted.Decision.UnreadFooter != "" {
		t.Fatalf("restarted as an unread report: %+v", restarted.Decision)
	}
}
