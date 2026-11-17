package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestUsageRoutesNeverExposePrices(t *testing.T) {
	h := newServeHarness(t)
	// Enumerate the registrations, so adding a route cannot silently weaken this test.
	source, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatal(err)
	}
	registrations := regexp.MustCompile(`mux.Handle(?:Func)?\("GET ([^"]+)"`).FindAllStringSubmatch(string(source), -1)
	if len(registrations) < 20 {
		t.Fatal("route enumeration lost its positive control")
	}
	for _, registration := range registrations {
		path := registration[1]
		path = strings.ReplaceAll(path, "{seq}", "0")
		path = strings.ReplaceAll(path, "{kind}", "turn")
		id := "run-1"
		if strings.Contains(path, "/items/") {
			id = "item-seed-02"
		}
		if strings.Contains(path, "/landings/") {
			id = "attempt-1"
		}
		if strings.Contains(path, "/checks/") {
			id = "check-1"
		}
		path = strings.ReplaceAll(path, "{id}", id)
		status, body := h.getRaw(path)
		if status == 500 {
			t.Errorf("%s failed internally: %s", path, body)
		}
		if regexp.MustCompile(`(?i)"[^"\n]*cost[^"\n]*"\s*:`).Match(body) || strings.ContainsAny(string(body), "$€£¥") {
			t.Errorf("%s exposes pricing: %s", path, body)
		}
	}
	// This is the only non-GET route: browser timing intake, not a store write.
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, clientTimingPath, strings.NewReader(`{"page":"usage","timings":[]}`))
	h.srv.routes().ServeHTTP(response, request)
	if strings.Contains(strings.ToLower(response.Body.String()), "cost") || strings.ContainsAny(response.Body.String(), "$€£¥") {
		t.Fatalf("POST %s exposes pricing: %s", clientTimingPath, response.Body.String())
	}
}

func TestUsageMeasurementsOnRunTurnsAndSummaries(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `INSERT INTO events(seq,ts,kind,object_type,object_id,payload) VALUES(1,2000,'run.finish','run','run-1','{"usage":{"details":{"models":[{"model":"wire","input":10,"output":20,"cache_read":0}],"turns":[{"id":"t1","model":"wire","started_at_ms":1000,"finished_at_ms":2200,"first_output_ms":200,"output_tokens_per_second":20}],"first_output_ms":200,"output_tokens_per_second":20}}}')`)
	_, payload, _ := h.get("/api/v1/runs/run-1")
	usage := payload["data"].(map[string]any)["usage"].(map[string]any)
	if usage["first_output_ms"] != float64(200) || usage["output_tokens_per_second"] != float64(20) {
		t.Fatal(usage)
	}
	model := usage["models"].([]any)[0].(map[string]any)
	if model["backend"] != "pi" || model["model"] != "wire" || model["cache_read"] != float64(0) {
		t.Fatal(model)
	}
	if _, present := model["cache_write"]; present {
		t.Fatal("missing cache write became a measurement")
	}
	_, timeline, _ := h.get("/api/v1/runs/run-1/timeline")
	found := false
	for _, raw := range timeline["data"].(map[string]any)["spans"].([]any) {
		sp := raw.(map[string]any)
		if sp["category"] == "llm_turn" {
			found = true
			if sp["first_output_ms"] != float64(200) || sp["output_tokens_per_second"] != float64(20) {
				t.Fatal(sp)
			}
		}
	}
	if !found {
		t.Fatal("missing turn span")
	}
	for _, path := range []string{"/api/v1/items/item-seed-02", "/api/v1/lanes", "/api/v1/perf/items"} {
		_, _, raw := h.get(path)
		if !strings.Contains(string(raw), `"model": "wire"`) || !strings.Contains(string(raw), `"output": 20`) {
			t.Fatalf("%s missing breakdown: %s", path, raw)
		}
	}
}

func TestUsageMixedModelTotalsAreReportedNotRecomputed(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `UPDATE runs SET usage_tokens=20 WHERE run_id='run-1'`)
	perfExec(t, h, `INSERT INTO events(seq,ts,kind,object_type,object_id,payload) VALUES(1,2000,'run.finish','run','run-1','{"usage":{"details":{"models":[{"model":"a","tokens":10,"input":2,"output":3,"cache_read":0,"cache_write":0},{"model":"b","tokens":10,"input":2,"output":3,"cache_read":0,"cache_write":0}],"turns":[]}}}')`)
	_, payload, _ := h.get("/api/v1/items/item-seed-02")
	usage := payload["data"].(map[string]any)["usage"].(map[string]any)
	if usage["tokens"] != float64(20) {
		t.Fatal(usage)
	}
	for _, raw := range usage["models"].([]any) {
		model := raw.(map[string]any)
		if model["tokens"] != float64(10) {
			t.Fatal("model total lost other reported token kinds", model)
		}
	}
}

func TestUsageIncludesAllocatedUnfinishedRuns(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(2300,'run.allocate','run','live-usage','{"run_id":"live-usage","item_id":"item-seed-02","lane_id":"lane-a","backend":"claude","started_at":"2026-11-17T00:00:00Z"}')`)
	for _, path := range []string{"/api/v1/items/item-seed-02", "/api/v1/lanes", "/api/v1/perf/items"} {
		_, _, raw := h.get(path)
		if !strings.Contains(string(raw), `"lower_bound": true`) {
			t.Fatalf("%s omitted allocated run: %s", path, raw)
		}
	}
}

func TestPriceGuardPreservesMultipleJSONLines(t *testing.T) {
	text := `{"usage":{"cost":{"total":0.5}},"text":"one"}` + "\n" + `{"text":"two"}`
	clean, err := withoutPrices(map[string]any{"body": text, "text": "Price $0.50"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(clean)
	if !strings.Contains(string(raw), "two") || strings.Contains(string(raw), "$0.50") || strings.Contains(string(raw), "cost") {
		t.Fatal(string(raw))
	}
}

func TestPriceRoutesRemoveOTLPAndPlainUSDAmounts(t *testing.T) {
	h := newServeHarness(t)
	if err := os.WriteFile(filepath.Join(h.loopDir, "run-1", "brief.md"), []byte("cost usd: 0.750000\nUSD 0.75\n0.75 USD\n$0.75\nunchanged prose"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/runs/run-1/trace?format=otlp-json", "/api/v1/runs/run-1/trace?format=chrome-trace", "/api/v1/runs/run-1/artifacts/brief"} {
		status, raw := h.getRaw(path)
		if status != 200 {
			t.Fatalf("%s: %d %s", path, status, raw)
		}
		if strings.Contains(string(raw), "cost_usd") || strings.Contains(string(raw), "0.75") || strings.ContainsAny(string(raw), "$€£¥") {
			t.Fatalf("%s returned prices: %s", path, raw)
		}
		if strings.Contains(path, "brief") && !strings.Contains(string(raw), "unchanged prose") {
			t.Fatal("redaction lost unrelated prose")
		}
	}
}

func TestUsageItemLowerBound(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES('no-usage','item-seed-02','lane-a','pi','finished',2100,2200,'[]',0)`)
	status, payload, _ := h.get("/api/v1/items/item-seed-02")
	if status != 200 {
		t.Fatal(payload)
	}
	usage, ok := payload["data"].(map[string]any)["usage"].(map[string]any)
	if !ok {
		t.Fatal("missing usage summary")
	}
	if usage["tokens"] != float64(1234) || usage["unrecorded_runs"] != float64(1) || usage["lower_bound"] != true {
		t.Fatal(usage)
	}
	models := usage["models"].([]any)
	if len(models) != 1 {
		t.Fatal(models)
	}
	raw, _ := json.Marshal(models[0])
	if !strings.Contains(string(raw), `"backend":"pi"`) {
		t.Fatal(string(raw))
	}
}
