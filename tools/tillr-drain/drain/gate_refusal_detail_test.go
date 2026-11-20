package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGateRefusalRecordsFailingCheckDetail(t *testing.T) {
	for _, key := range []string{"", "existing-evidence"} {
		t.Run("evidence="+key, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			id := "run-gate-detail"
			dir := RunDir(l.cfg.Root, id)
			if err := os.MkdirAll(filepath.Join(dir, "logs"), 0755); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, "logs", "00-gate.log")
			output := "fork-check: triage -- failing checks\n  triage tillr-backends: backend tests\n    no case named in the output; reproduce with: python3 tests/tillr-backends.test.py\n"
			if err := os.WriteFile(log, []byte(output), 0600); err != nil {
				t.Fatal(err)
			}
			verdict := mustJSON(map[string]any{"payload": map[string]any{"result": "fail", "checks": []map[string]any{{"check_id": "gate:python3", "result": "fail", "log_ref": log}}}})
			if err := os.WriteFile(filepath.Join(dir, GateVerdictRecord), []byte(verdict), 0600); err != nil {
				t.Fatal(err)
			}
			c := Candidate{ItemID: "item-a", RunID: id, LaneID: "lane-1", RefusalDetail: "existing diagnostic", EvidenceKey: key}
			if err := l.recordRefusal(context.Background(), c, RefusalGate); err != nil {
				t.Fatal(err)
			}
			p := refusalOf(t, f, id)
			for _, want := range []string{"existing diagnostic", "tillr-backends", "python3 tests/tillr-backends.test.py"} {
				if !strings.Contains(p.Detail, want) {
					t.Errorf("detail missing %q: %q", want, p.Detail)
				}
			}
			if len(p.Checks) != 1 || p.Checks[0].CheckID != "gate:python3" || len(p.Checks[0].Failing) != 1 || p.Checks[0].Failing[0].ID != "tillr-backends" {
				t.Fatalf("checks lost: %+v", p.Checks)
			}
			if key != "" && p.Evidence != key {
				t.Errorf("evidence changed: %q", p.Evidence)
			}
		})
	}
}

func TestGateRefusalWithoutTriageNamesWrapperCheck(t *testing.T) {
	f, l := passFixture(t, "item-a")
	id := "run-wrapper-check"
	dir := RunDir(l.cfg.Root, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "check.log")
	verdict := mustJSON(map[string]any{"payload": map[string]any{"result": "fail", "checks": []map[string]any{{"check_id": "gate:python3", "result": "fail", "log_ref": log}}}})
	if err := os.WriteFile(filepath.Join(dir, GateVerdictRecord), []byte(verdict), 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.recordRefusal(context.Background(), Candidate{ItemID: "item-a", RunID: id, LaneID: "lane-1"}, RefusalGate); err != nil {
		t.Fatal(err)
	}
	p := refusalOf(t, f, id)
	for _, want := range []string{"gate:python3", log} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("detail missing %q: %q", want, p.Detail)
		}
	}
}

func TestGateRefusalVerdictWithoutFailingCheck(t *testing.T) {
	f, l := passFixture(t, "item-a")
	id := "run-empty-gate-verdict"
	dir := RunDir(l.cfg.Root, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, GateVerdictRecord), []byte(`{"payload":{"result":"fail","checks":[]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.recordRefusal(context.Background(), Candidate{ItemID: "item-a", RunID: id, LaneID: "lane-1"}, RefusalGate); err != nil {
		t.Fatal(err)
	}
	p := refusalOf(t, f, id)
	if !strings.Contains(p.Detail, "gate produced no usable failure verdict") || !strings.Contains(p.Detail, filepath.Join(dir, StepsFile)) {
		t.Fatalf("incorrect detail: %q", p.Detail)
	}
}

func TestGateRefusalWithoutVerdictNamesLog(t *testing.T) {
	f, l := passFixture(t, "item-a")
	id := "run-no-gate-verdict"
	c := Candidate{ItemID: "item-a", RunID: id, LaneID: "lane-1"}
	if err := l.recordRefusal(context.Background(), c, RefusalGate); err != nil {
		t.Fatal(err)
	}
	p := refusalOf(t, f, id)
	for _, want := range []string{"gate produced no verdict", filepath.Join(RunDir(l.cfg.Root, id), StepsFile)} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("detail missing %q: %q", want, p.Detail)
		}
	}
}
