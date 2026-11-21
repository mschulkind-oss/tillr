package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinalReportFromCommitOffer(t *testing.T) {
	for _, tc := range []struct{ name, offer, want string }{
		{"commit", `{"call_id":"call","base":"base","commit":"head"}`, "complete: yes\nresult: done\n\n---\nRESULT: done\n"},
		{"legacy commit", `{"call_id":"call","base":"base","commit":"head","summary":"Changed behavior.\n\nVerification: targeted test passed."}`, "complete: yes\nresult: done\n\n---\nRESULT: done\n"},
		{"no offer", "", "complete: no\nresult: missing\n"},
		{"obstacle", `{"call_id":"call","base":"base","tool":"obstacle","obstacle":{"kind":"owner"}}`, "complete: no\nresult: missing\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			stream := filepath.Join(dir, "stream.jsonl")
			if err := os.WriteFile(stream, []byte(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"No footer."}]}}`+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.offer != "" {
				if err := os.WriteFile(filepath.Join(dir, "landing-offer.json"), []byte(tc.offer), 0600); err != nil {
					t.Fatal(err)
				}
			}
			backends, err := filepath.Abs("../backends")
			if err != nil {
				t.Fatal(err)
			}
			_, note := readSpend(context.Background(), backends, "pi/run-pi", "pi", dir, stream, nil, true)
			report, err := os.ReadFile(filepath.Join(dir, "report.md"))
			if err != nil || !strings.Contains(string(report), tc.want) {
				t.Fatalf("report = %s\nerror = %v; extraction note: %s", report, err, note)
			}
			if strings.Contains(string(report), "Changed behavior.") || strings.Contains(string(report), "Verification: targeted test passed.") {
				t.Fatalf("receipt included the legacy worker summary: %s", report)
			}
		})
	}
}
