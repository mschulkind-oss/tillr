package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeavyFailureEventsKeepMatchingCheckOutput(t *testing.T) {
	for _, autoRevert := range []bool{true, false} {
		kind := FixerLogsKind
		if autoRevert {
			kind = RevertKind
		}
		t.Run(kind, func(t *testing.T) {
			bodies := map[string]string{
				"unit/TestAlpha": "--- FAIL: TestAlpha\n    alpha_test.go:42: alpha assertion\n",
				"unit/TestBeta":  "--- FAIL: TestBeta\n    beta_test.go:91: beta assertion\n",
			}
			source := t.TempDir()
			h := newRevertHarness(t, func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
				var checks []HeavyCheck
				for _, id := range []string{"unit/TestAlpha", "unit/TestBeta"} {
					if only != "" && id != only {
						continue
					}
					check := HeavyCheck{CheckID: id, Result: "pass", Reads: []string{"a"}}
					if _, err := os.Stat(filepath.Join(dir, "a")); err == nil {
						check.Result = "fail"
						if strings.HasSuffix(id, "Alpha") {
							check.LogPath = filepath.Join(source, "alpha.log")
						} else {
							check.Detail = bodies[id]
						}
					}
					checks = append(checks, check)
				}
				return checks, nil
			})
			writeForkFile(t, filepath.Join(source, "alpha.log"), bodies["unit/TestAlpha"])
			h.loop.gatePolicy.AutoRevert = autoRevert
			h.land("item-broke", "a", "broken\n")
			h.proof()
			if err := os.RemoveAll(source); err != nil {
				t.Fatal(err)
			}
			payloads := h.query(`SELECT payload FROM events WHERE kind=?`, kind)
			if len(payloads) != 1 {
				t.Fatalf("wanted one %s event, got %v\n%s", kind, payloads, h.report.String())
			}
			var record struct {
				Checks    []string          `json:"checks"`
				CheckLogs map[string]string `json:"check_logs"`
				Result    string            `json:"result"`
			}
			if err := json.Unmarshal([]byte(payloads[0]), &record); err != nil {
				t.Fatal(err)
			}
			if autoRevert && (record.Result != "reverted" || strings.Join(record.Checks, ",") != "unit/TestAlpha,unit/TestBeta") {
				t.Fatalf("wrong revert receipt: %s", payloads[0])
			}
			if len(record.CheckLogs) != len(bodies) {
				t.Fatalf("wrong check logs: %s", payloads[0])
			}
			for id, want := range bodies {
				got, err := os.ReadFile(record.CheckLogs[id])
				if err != nil || string(got) != want {
					t.Fatalf("%s kept %q, want %q (%v)", id, got, want, err)
				}
			}
		})
	}
}
