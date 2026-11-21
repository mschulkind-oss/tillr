package main_test

import (
	"strings"
	"testing"
)

func TestWhyPrintsBackendAlarmBeforeDefinitionAndRows(t *testing.T) {
	path := fixture(t, itemRecord("ready", "in-queue"))
	ref := policyDoc(t, map[string]any{
		"default_backend": "fixture",
		"backends":        map[string]any{"fixture": map[string]any{"adapter": "true", "model": "fixture", "timeout_seconds": 60, "quota": []string{"quota-fixture"}}},
	})
	code, out := invoke(t, "why", "--store", path, "--policy", ref, "--lanes", "lane-1")
	if code != 0 || !strings.HasPrefix(string(out), "ALARM backend-window (fixture)") || !strings.Contains(string(out), "no quota reading recorded") {
		t.Fatalf("why: %d %s", code, out)
	}
	code, out = invoke(t, "why", "ready", "--store", path, "--policy", ref, "--json")
	if code != 0 || !strings.HasPrefix(string(out), "{\n  \"alarms\":") || !strings.Contains(string(out), `"item_id": "ready"`) {
		t.Fatalf("named why: %d %s", code, out)
	}
}
