package run_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUsageDetailsSurviveExecutionAndStatus(t *testing.T) {
	backends, adapter := backendWithExtractor(t, "# run report\ntokens: 30\ncost usd: unknown\n---\n", "0")
	extractor := filepath.Join(filepath.Dir(adapter), "extract-report")
	body, err := os.ReadFile(extractor)
	if err != nil {
		t.Fatal(err)
	}
	details := `{"models":[{"model":"wire-model","input":10,"output":20,"cache_read":0}],"turns":[{"id":"t1","model":"wire-model","output":20,"first_output_ms":200,"output_tokens_per_second":20}],"first_output_ms":200,"output_tokens_per_second":20}`
	script := strings.Replace(string(body), "exit 0\n", "printf '%s' '"+details+"' > \"$out.usage.json\"\nexit 0\n", 1)
	if err := os.WriteFile(extractor, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	rec, s := execFixture(t, backends, adapter, "pi")
	if rec.Usage == nil || rec.Usage.Details == nil {
		t.Fatalf("details missing: %+v", rec)
	}
	stored, found := storedRun(t, s, "run_001")
	if !found || stored.Usage == nil || stored.Usage.Details == nil {
		t.Fatal("status lost usage details")
	}
	a, _ := json.Marshal(rec.Usage.Details)
	b, _ := json.Marshal(stored.Usage.Details)
	if string(a) != string(b) {
		t.Fatalf("status mismatch: %s / %s", a, b)
	}
	model := stored.Usage.Details.Models[0]
	if model.CacheRead == nil || *model.CacheRead != 0 || model.CacheWrite != nil {
		t.Fatal("missing kind became zero")
	}
}
