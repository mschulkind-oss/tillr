package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWaitingNamesOwnerRetryAfterAutomaticReadmission(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`DELETE FROM dispatches`)
	h.exec(`INSERT INTO dispatches (decision_id,item_id,action,reason,dispatched_at) VALUES ('held','item-seed-02','hold','readmission-exhausted',1000)`)
	status, _, raw := h.get("/api/v1/waiting")
	if status != 200 || !strings.Contains(string(raw), "automatic re-admission failed") || !strings.Contains(string(raw), "tillr drain retry") {
		t.Fatalf("waiting API: %d %s", status, raw)
	}
	bin := filepath.Join(t.TempDir(), "tillr-view")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	cmd := exec.Command(bin, "waiting", "--history", "--store", h.store)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "automatic re-admission failed") || !strings.Contains(string(out), "tillr drain retry") {
		t.Fatalf("waiting CLI: %v %s", err, out)
	}
	rule := waitingRuleFor("inputs-changed: item-block, backend-policy")
	if rule.class != "ready" || rule.mover != "the next pass" {
		t.Fatalf("changed input: %+v", rule)
	}
}
