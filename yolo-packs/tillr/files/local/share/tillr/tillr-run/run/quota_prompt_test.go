package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuotaContinuationForcesFreshSession(t *testing.T) {
	for _, kind := range []string{PauseAtGap, PauseWhereItStands, PauseCannotResume} {
		t.Run(kind, func(t *testing.T) {
			lane := t.TempDir()
			durable := filepath.Join(lane, "durable")
			if err := os.MkdirAll(durable, 0700); err != nil {
				t.Fatal(err)
			}
			old := "33b5f2d2-0cd4-4a49-b293-c58f5635561f"
			for path, body := range map[string]string{filepath.Join(lane, SessionIDFile): old + "\n", filepath.Join(lane, "brief.md"): "the original brief", filepath.Join(durable, "report.md"): "quota report"} {
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var req Request
			if err := json.Unmarshal([]byte(`{"run_id":"run","resume":true,"fresh_session":true,"resume_prompt":"The earlier session ran out of quota on alpha. Uncommitted paths: one.txt, two.txt."}`), &req); err != nil {
				t.Fatal(err)
			}
			mode, path, err := prepareContinuation(req, kind, lane, filepath.Join(lane, "worktree"), durable)
			if err != nil {
				t.Fatal(err)
			}
			session, _ := os.ReadFile(filepath.Join(lane, SessionIDFile))
			if mode != "new" {
				t.Fatalf("not a fresh session: %s", mode)
			}
			if strings.TrimSpace(string(session)) != old {
				t.Fatalf("unclaimed handoff replaced original session: %s", session)
			}
			body, _ := os.ReadFile(path)
			for _, want := range []string{"ran out of quota on alpha", "one.txt, two.txt", "Transcript: " + filepath.Join(lane, "run.log"), "Report: " + filepath.Join(durable, "report.md"), "Worktree:", "the original brief"} {
				if !strings.Contains(string(body), want) {
					t.Errorf("missing %q: %s", want, body)
				}
			}
		})
	}
}
