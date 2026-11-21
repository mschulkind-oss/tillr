package drain

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSuccessfulBackendProbeReopensQuotaWindow(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Policy.Backends["fixture"] = Backend{Adapter: "/bin/true"}
	at := time.Now().UTC()
	l.cfg.Now = func() time.Time { return at }
	reset := at.Add(time.Hour).Truncate(time.Second)
	report := filepath.Join(f.root, "quota.report")
	writeForkFile(t, report, fmt.Sprintf("# run report\nrate limit kind: weekly\nfraction: 1\nstatus: rejected\nreset: %d\n---\n", reset.Unix()))
	refuse := func(runID, backend string) {
		t.Helper()
		rec := run.Record{RunID: runID, ItemID: "item-b", LaneID: "lane-1", Backend: backend, State: run.StateExited,
			ExitCode: code(1), StartedAt: at.Format(time.RFC3339), FinishedAt: at.Format(time.RFC3339),
			ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}}}
		fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		if err := l.recordBackendWindow(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	probe := func(backend, command string) error {
		return ProbeBackend(ctx, f.storePath, f.root, backend, []string{command})
	}
	success := func(backend string) {
		t.Helper()
		if err := probe(backend, "/bin/true"); err != nil {
			t.Fatal(err)
		}
	}
	held := func() {
		t.Helper()
		if d := mustPlan(t, l)[0].decision; d.Action != ActionHold || d.Reason != ReasonBackendWindow {
			t.Fatalf("expected quota hold, got %+v", d)
		}
	}

	success("fixture") // An older success cannot clear a later refusal.
	refuse("refused", "fixture")
	refuse("other-refused", "other")
	success("unrelated")
	held()
	if err := probe("fixture", "/bin/false"); err == nil {
		t.Fatal("failed probe returned success")
	}
	held()
	success("fixture")
	if d := mustPlan(t, l)[0].decision; d.Action != ActionDispatch || d.RunRequest.Backend != "fixture" {
		t.Fatalf("expected dispatch to reopened backend, got %+v", d)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM backend_windows WHERE backend='fixture'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("reopened backend still has a window: count=%d err=%v", n, err)
		}
		if err := r.QueryRow(`SELECT count(*) FROM backend_windows WHERE backend='other'`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("unrelated window changed: count=%d err=%v", n, err)
		}
		if err := r.QueryRow(`SELECT count(*) FROM events e JOIN events p ON p.seq=json_extract(e.payload,'$.probe_seq')
			JOIN events f ON f.object_id=json_extract(e.payload,'$.run_id') AND f.kind=?
			WHERE e.kind='drain.backend-quota-reopened' AND e.object_id='fixture'
			AND p.kind=? AND p.object_id=e.object_id AND json_extract(p.payload,'$.success')=1
			AND f.object_id='refused' AND f.seq<p.seq AND p.seq<e.seq`, run.FinishKind, backendProbeKind).Scan(&n); err != nil || n != 1 {
			t.Fatalf("reopening did not name its successful probe and refusal: count=%d err=%v", n, err)
		}
		return nil
	})

	refuse("refused-again", "fixture")
	held()
	success("fixture")
	var result LoopResult
	if stop, err := l.pass(ctx, &result); err != nil || stop != "" || result.Dispatched != 1 {
		t.Fatalf("next pass did not dispatch held work: stop=%q err=%v result=%+v", stop, err, result)
	}
	select {
	case c := <-l.completions:
		if c.itemID != "item-a" || c.outcome.err != nil {
			t.Fatalf("unexpected run completion: item=%s err=%v", c.itemID, c.outcome.err)
		}
	case <-time.After(hangGuard):
		t.Fatal("dispatched run did not finish")
	}
}
