package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// The real adapter invokes a wrapper stand-in with the same record contract as
// tillr-gate run --gate. No watchdog sleeps or elapsed-time assertions enter it.
func TestProcessStepsRealAdapterPreservesStallAndStartError(t *testing.T) {
	_, adapter := repositoryFile(t, "tools/tillr-loop/tillr-loop")
	f, l, _, c := stalledFixture(t, false)
	dir := scratchUnderDurable(t)
	stub := filepath.Join(dir, "tillr-gate")
	script := `#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
args=sys.argv[1:]
face=args[0]
if face=='change': sys.exit(0)
out=Path(args[args.index('--output')+1])
out.parent.mkdir(parents=True,exist_ok=True)
result=os.environ['STUB_GATE_RESULT']
check={'check_id':'documents','result':result,'duration_ms':60000,'budget_ms':2000,'over_budget_ms':58000,'last_output_line':'heartbeat','cpu_time_ms':12,'silent_samples':60,'start_unix_nano':1000000000,'end_unix_nano':61000000000}
kind='gate.plan' if face=='plan' else 'gate.verdict'
rec={'record_version':1,'kind':kind,'id':'same-id','producer':{'system':'tillr','utility':'gate','version':'0.1.0'},'created_at':'2026-11-17T07:00:00Z','payload':{'plan_id':'same-plan','verdict_id':'same-verdict','result':result,'checks':[check]}}
out.write_text(json.dumps(rec))
if face=='run':
 Path(os.environ['TILLR_FAST_VERDICT_OUT']).write_text(json.dumps(rec))
 if os.environ.get('SKIP_REMAINING')!='1':
  Path(os.environ['TILLR_GATE_SPANS_OUT']).write_text(json.dumps({'check_spans':[{'check_id':'remaining-suite','status':'OK','start_ns':100,'end_ns':200}]}))
 sys.exit({'stalled':6,'pass':0,'error':4}[result])
`
	if err := writeForkSafe(stub, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TILLR_LOOP_REGISTRY", filepath.Join(dir, "registry.json"))
	t.Setenv("STUB_GATE_RESULT", "stalled")
	steps := &ProcessSteps{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Now: time.Now, GateProgram: "python3 " + adapter + " gate"}
	gate, err := steps.Gate(context.Background(), c)
	if err != nil || gate.Outcome != GateStalled || len(gate.StalledChecks) != 1 || gate.StalledChecks[0].CheckID != "documents" || gate.StalledChecks[0].CPUTimeMS != 12 {
		t.Fatalf("stall: %+v %v", gate, err)
	}
	// A crash immediately after Gate returns must leave the first-stall wait,
	// observations and concurrency count in the same committed judgment.
	fixtureRead(t, f, func(r *store.Reader) error {
		works, err := readPending(r)
		if err == nil && (works[c.ItemID].GateStall == nil || works[c.ItemID].GateStall.GatesInFlight != 1) {
			t.Errorf("first judgment did not retain its stall atomically: %+v", works[c.ItemID])
		}
		return err
	})
	if err := l.holdGateStall(context.Background(), c, gate, 1); err != errGateStallHeld {
		t.Fatal(err)
	}
	t.Setenv("STUB_GATE_RESULT", "pass")
	if gate, err := steps.Gate(context.Background(), c); err != nil || gate.Outcome != GatePassed {
		t.Fatalf("pass: %+v %v", gate, err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=?`, GateVerdictKind, c.RunID).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			t.Errorf("stall rerun discarded verdict: %d events", n)
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM gate_spans WHERE run_id=?`, c.RunID).Scan(&n); err != nil {
			return err
		}
		if n != 4 {
			t.Errorf("deterministic verdict overwrote spans: %d rows", n)
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM gate_spans WHERE run_id=? AND status='stalled'`, c.RunID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("stalled check evidence lost: %d", n)
		}
		return nil
	})
	t.Setenv("STUB_GATE_RESULT", "error")
	c.RunID += "-error"
	gate, err = steps.Gate(context.Background(), c)
	if err != nil || gate.Outcome != GateErrored || len(gate.StalledChecks) != 0 {
		t.Fatalf("start error: %+v %v", gate, err)
	}
	if err := l.gateFault(context.Background(), c, "check could not start"); err != errItemFaultHeld {
		t.Fatal(err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err == nil && faults[c.ItemID].Reason != StopGateHarnessError {
			t.Errorf("error classified as stall: %+v", faults)
		}
		return err
	})
	fixtureWrite(t, f, func(tx *store.Tx) error {
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		return ClearItemFault(tx, c.ItemID, faults[c.ItemID].Seq, l.cfg.Now())
	})
	t.Setenv("STUB_GATE_RESULT", "stalled")
	t.Setenv("SKIP_REMAINING", "1")
	gate, err = steps.Gate(context.Background(), c)
	if err != nil || gate.Outcome != GateStalled {
		t.Fatalf("stall after clearance: %+v %v", gate, err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var count int
		err := r.QueryRow(`SELECT COUNT(*) FROM gate_spans WHERE run_id=? AND check_id='remaining-suite'`, c.RunID).Scan(&count)
		if err == nil && count != 1 {
			t.Errorf("rerun attributed stale remaining spans: %d", count)
		}
		return err
	})
}
