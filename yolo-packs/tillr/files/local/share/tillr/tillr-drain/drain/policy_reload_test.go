package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type policyRunSteps struct {
	recordingSteps
	requests chan run.Request
	release  chan struct{}
}

func (s *policyRunSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.requests <- req
	select {
	case <-s.release:
		return run.Record{}, "", errors.New("test worker released")
	case <-ctx.Done():
		return run.Record{}, "", ctx.Err()
	}
}

func TestPolicyReloadAtPassBoundaryKeepsLiveRequest(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	p := twoBackends()
	p.Seats = map[string]string{"build": "first"}
	ref := filepath.Join(f.root, "policy.json")
	save := func(p Policy) {
		t.Helper()
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, ref, string(raw))
	}
	save(p)
	loaded, err := LoadPolicy(ref)
	if err != nil {
		t.Fatal(err)
	}
	l.cfg.Policy, l.cfg.PolicyRef = loaded, ref
	var report bytes.Buffer
	l.cfg.Report = &report
	steps := &policyRunSteps{requests: make(chan run.Request, 2), release: make(chan struct{})}
	l.cfg.Steps = steps
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat = 'build', state = CASE WHEN item_id = 'item-b' THEN 'blocked' ELSE state END`)
		return err
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		// Keep the context live until all workers report: sendCompletion can
		// drop notifications after cancellation.
		close(steps.release)
		for len(l.active) > 0 {
			c := <-l.completions
			delete(l.active, c.id)
		}
	}()
	var result LoopResult
	pass := func() {
		t.Helper()
		if _, err := l.pass(ctx, &result); err != nil {
			t.Fatal(err)
		}
	}
	receive := func() run.Request {
		t.Helper()
		select {
		case req := <-steps.requests:
			return req
		case <-time.After(hangGuard):
			t.Fatal("dispatch did not start")
			return run.Request{}
		}
	}
	pass()
	original := receive()
	if original.Backend != "first" {
		t.Fatalf("first dispatch: %+v", original)
	}
	p.Seats["build"] = "second"
	save(p)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = 'in-queue' WHERE item_id = 'item-b'`)
		return err
	})
	pass()
	next := receive()
	if next.Backend != "second" || original.Backend != "first" || !l.active[original.RunID] {
		t.Fatalf("reload changed live request or missed next dispatch: %s / %s", original.Backend, next.Backend)
	}
	if !strings.Contains(report.String(), "build=second") {
		t.Fatalf("no mapping in report: %s", report.String())
	}
	before := report.Len()
	pass()
	if strings.Contains(report.String()[before:], "policy change") {
		t.Fatal("unchanged content recorded again")
	}
}

func TestPolicyStartupPinsCheckedContentBeforeFirstPass(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ref := filepath.Join(f.root, "policy.json")
	p := twoBackends()
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, ref, string(raw))
	cfg := l.cfg
	cfg.PolicyRef = ref
	l, err = NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, ref, "broken before first pass")
	if err := l.refreshPolicy(); err != nil {
		t.Fatal(err)
	}
	state, err := ReadPolicyState(f.root)
	if err != nil || state.Content != string(raw) || state.Refusal == "" {
		t.Fatalf("startup content lost: %+v / %v", state, err)
	}
	if _, err := NewLoop(cfg); err == nil {
		t.Fatal("invalid policy accepted at startup")
	}
}

func TestRepairPolicyReadDuringReload(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := twoBackends()
	ref := filepath.Join(f.root, "policy.json")
	l.cfg.PolicyRef = ref
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		close(started)
		for i := 0; i < 80; i++ {
			policy := l.currentPolicy()
			basis, err := l.repairBasis(context.Background(), Candidate{ItemID: "item-a"}, LandOutcome{}, policy)
			if err != nil {
				finished <- err
				return
			}
			want, err := inputHash([]any{policy, policy.SeatLists})
			if err != nil || basis.Inputs.BackendPolicy != want {
				finished <- fmt.Errorf("repair inputs do not match captured policy: %v", err)
				return
			}
		}
		finished <- nil
	}()
	<-started
	for i := 0; i < 80; i++ {
		if i%2 == 0 {
			p.DefaultBackend = "first"
		} else {
			p.DefaultBackend = "second"
		}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, ref, string(raw))
		if err := l.refreshPolicy(); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestPolicyReloadRefusesMalformedAndMissingFiles(t *testing.T) {
	for _, mode := range []string{"malformed", "missing"} {
		t.Run(mode, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			p := twoBackends()
			raw, _ := json.MarshalIndent(p, "", "  ")
			ref := filepath.Join(f.root, "policy.json")
			writeForkFile(t, ref, string(raw))
			l.cfg.PolicyRef = ref
			if err := l.refreshPolicy(); err != nil {
				t.Fatal(err)
			}
			if mode == "missing" {
				if err := os.Remove(ref); err != nil {
					t.Fatal(err)
				}
			} else {
				writeForkFile(t, ref, "{bad JSON")
			}
			_, startupErr := LoadPolicy(ref)
			if err := l.refreshPolicy(); err != nil {
				t.Fatal(err)
			}
			state, err := ReadPolicyState(f.root)
			if err != nil || state.Refusal != startupErr.Error() || state.Content != string(raw) {
				t.Fatalf("refused state: %+v / %v", state, err)
			}
			if mode == "missing" && state.DiskHash != "" {
				t.Fatal("missing file has a content hash")
			}
			effective, err := EffectivePolicy(f.root, ref)
			if err != nil || effective == nil || effective.DefaultBackend != "first" {
				t.Fatalf("last accepted policy lost: %+v / %v", effective, err)
			}
		})
	}
}

func TestInvalidPolicyReloadRetainsPolicyAndStartupRefusal(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := twoBackends()
	ref := filepath.Join(f.root, "policy.json")
	raw, _ := json.Marshal(p)
	writeForkFile(t, ref, string(raw))
	l.cfg.Policy, l.cfg.PolicyRef = &p, ref
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE items SET state = 'blocked'`); return err })
	var result LoopResult
	if _, err := l.pass(context.Background(), &result); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, ref, `{"default_backend":"missing"}`)
	_, startupErr := LoadPolicy(ref)
	var report bytes.Buffer
	l.cfg.Report = &report
	if _, err := l.pass(context.Background(), &result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), startupErr.Error()) {
		t.Fatalf("reload did not print startup refusal %v: %s", startupErr, report.String())
	}
	if l.cfg.Policy.DefaultBackend != "first" {
		t.Fatal("invalid content replaced policy")
	}
	// Restoration is a change too, even when its content equals the last accepted policy.
	state, err := ReadPolicyState(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if state.Refusal != startupErr.Error() || state.Hash != contentHash(raw) || state.DiskHash == state.Hash {
		t.Fatalf("refusal state: %+v", state)
	}
	effective, err := EffectivePolicy(f.root, ref)
	if err != nil || effective.DefaultBackend != "first" {
		t.Fatalf("effective policy: %+v / %v", effective, err)
	}
	writeForkFile(t, ref, string(raw))
	if _, err := l.pass(context.Background(), &result); err != nil {
		t.Fatal(err)
	}
	state, err = ReadPolicyState(f.root)
	if err != nil || state.Refusal != "" || state.Hash != state.DiskHash {
		t.Fatalf("restored state: %+v / %v", state, err)
	}
	history, err := os.ReadFile(filepath.Join(LoopDir(f.root), "backend-policy-changes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(history)), "\n")
	if len(lines) != 3 {
		t.Fatalf("change history: %s", history)
	}
	var rejected, restored policyChange
	if err := json.Unmarshal([]byte(lines[1]), &rejected); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[2]), &restored); err != nil {
		t.Fatal(err)
	}
	if rejected.OldHash != contentHash(raw) || rejected.NewHash != contentHash([]byte(`{"default_backend":"missing"}`)) || rejected.InForceHash != contentHash(raw) || restored.OldHash != rejected.NewHash || restored.NewHash != contentHash(raw) {
		t.Fatalf("hash history: %+v / %+v", rejected, restored)
	}
}
