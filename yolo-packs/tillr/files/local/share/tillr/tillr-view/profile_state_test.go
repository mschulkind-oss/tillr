package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

func writeTestLoopPolicyState(t *testing.T, root string, state map[string]any) {
	t.Helper()
	loopDir := filepath.Join(root, ".tillr", "loop")
	if err := os.MkdirAll(loopDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loopDir, "backend-policy.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProfileStatusAndSnapshotSummary(t *testing.T) {
	h := newServeHarness(t)
	switchedAt := time.Date(2026, 11, 27, 18, 8, 12, 0, time.UTC)
	resetAt := time.Date(2026, 11, 27, 20, 30, 0, 0, time.UTC)
	policyContent := `{"default_backend":"copilot","seats":{"build":"copilot"},"backends":{"copilot":{"adapter":"true","model":"m","effort":"low"}}}`

	writeTestLoopPolicyState(t, h.dir, map[string]any{
		"ref":           filepath.Join(h.dir, "backend-policy.copilot-gemini.json"),
		"source":        "toml",
		"profile":       "copilot-gemini",
		"profiles":      []string{"claude-sub", "copilot-gemini"},
		"profile_index": 1,
		"why":           "switched from claude-sub at 2026-11-27T18:08:12Z",
		"switched_from": "claude-sub",
		"switched_at":   switchedAt.Format(time.RFC3339),
		"skipped_profiles": []map[string]any{
			{
				"profile":          "claude-sub",
				"backend":          "claude-sonnet",
				"windows":          []map[string]any{{"kind": "five_hour", "fraction": 0.82, "reset_at": resetAt.Format(time.RFC3339)}},
				"reserve":          0.2,
				"reopen_threshold": 0.7,
				"reset_at":         resetAt.Format(time.RFC3339),
			},
		},
		"hash":    fmt.Sprintf("%x", sha256.Sum256([]byte(policyContent))),
		"content": policyContent,
	})

	// 1. statusFaceFormat JSON
	env, code := statusFaceFormat([]string{"--store", h.store, "--root", h.dir, "--json"}, nil)
	if code != record.ExitOK {
		t.Fatalf("statusFaceFormat JSON exited %d", code)
	}
	var payload statusPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Profile == nil || payload.Profile.Profile != "copilot-gemini" || payload.Profile.ProfileIndex != 1 ||
		payload.Profile.Source != "toml" || len(payload.Profile.Profiles) != 2 ||
		len(payload.Profile.SkippedProfiles) != 1 || payload.Profile.SkippedProfiles[0].Profile != "claude-sub" {
		t.Fatalf("statusPayload.Profile = %+v", payload.Profile)
	}

	// 2. statusText
	text := statusText(payload)
	for _, want := range []string{
		"profile: copilot-gemini (2/2 of [claude-sub, copilot-gemini]) source=toml switched from claude-sub",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("statusText missing %q:\n%s", want, text)
		}
	}

	// 3. /api/v1/snapshot drain_summary.profile
	status, snapBody, snapRaw := h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("snapshot status %d: %s", status, snapRaw)
	}
	drainSum := snapBody["data"].(map[string]any)["drain_summary"].(map[string]any)
	prof, ok := drainSum["profile"].(map[string]any)
	if !ok || prof["profile"] != "copilot-gemini" || prof["source"] != "toml" || prof["profile_index"] != float64(1) {
		t.Fatalf("snapshot drain_summary.profile = %v", drainSum["profile"])
	}
}
