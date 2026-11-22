package drain_test

import (
	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
	"testing"
)

func TestProbeBackendFaceRecordsSuccessAndFailure(t *testing.T) {
	path := fixture(t, itemRecord("item_042", "in-queue"), runRecord("broken", "item_042", "lane-9", "first", run.StateExited, exitCode(2), ""))
	tickPath := tick(t, "2026-11-18T00:00:00Z", []string{"lane-1"}, 1, policyDoc(t, map[string]any{"default_backend": "first", "preference": []string{"first"}, "backends": map[string]any{"first": map[string]any{"adapter": "/bin/true", "model": "route"}}}))
	if got := plan(t, path, tickPath)[0].Payload; got.Reason != drain.ReasonBackendClosed {
		t.Fatalf("before probe: %+v", got)
	}
	if code, out := invoke(t, "probe-backend", "--store", path, "--backend", "first", "--", "/bin/false"); code == 0 {
		t.Fatalf("failed probe passed: %s", out)
	}
	if got := plan(t, path, tickPath)[0].Payload; got.Action != drain.ActionHold {
		t.Fatalf("failed probe reopened: %+v", got)
	}
	if code, out := invoke(t, "probe-backend", "--store", path, "--backend", "first", "--", "/bin/true"); code != 0 {
		t.Fatalf("successful probe refused: %d %s", code, out)
	}
	if got := plan(t, path, tickPath)[0].Payload; got.Action != drain.ActionDispatch {
		t.Fatalf("successful probe did not reopen: %+v", got)
	}
	if code, out := invoke(t, "probe-backend", "--help"); code != 0 {
		t.Fatalf("probe help: %d %s", code, out)
	}
}
