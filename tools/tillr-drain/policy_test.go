package main_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func TestPolicyFaceAcceptsEveryShippedPolicy(t *testing.T) {
	paths, err := filepath.Glob("../../config/tillr/backend-policy*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("expected at least one shipped policy: %v, %v", paths, err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			p, err := drain.LoadPolicy(path)
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			root := scratch(t, "shipped-policy-")
			state := drain.PolicyState{Ref: path, Hash: fmt.Sprintf("%x", sha256.Sum256(content)), Content: string(content)}
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(drain.LoopDir(root), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, drain.PolicyStateFile(root), string(raw))
			code, out := invoke(t, "policy", "--root", root)
			if code != 0 || !strings.Contains(string(out), p.Description()) {
				t.Fatalf("policy: %d %s", code, out)
			}
		})
	}
}

func TestPolicyFaceReadsAcceptedContentNotDisk(t *testing.T) {
	root := scratch(t, "policy-face-")
	content := `{
 "default_backend":"a", "seats":{"build":[{"backend":"a","share":1},{"backend":"b","share":1}],"design":"b"},
 "backends":{"a":{"adapter":"true","model":"cheap","effort":"medium"},"b":{"adapter":"true","models":["strong","stronger"],"effort":"high"}},
 "overrides":{"special":"b"}
 }`
	ref := filepath.Join(root, "policy.json")
	if err := os.WriteFile(ref, []byte(`{"bad":"disk content"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state := drain.PolicyState{Ref: ref, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(content))), Content: content, Refusal: "the policy names no backends"}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(drain.LoopDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(drain.PolicyStateFile(root), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := invoke(t, "policy", "--root", root)
	if code != 0 {
		t.Fatalf("policy: %d %s", code, out)
	}
	for _, want := range []string{"build=a:1,b:1", "design=b", "backend a: models=cheap effort=medium", "backend b: models=strong,stronger effort=high", "override special=b", "policy on disk is not the policy in force: the policy names no backends"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	code, out = invoke(t, "policy", "--root", root, "--json")
	var got drain.PolicyState
	if code != 0 || json.Unmarshal(out, &got) != nil || got.Content != content || got.Hash != state.Hash {
		t.Fatalf("policy JSON: %d %s", code, out)
	}
	code, out = invoke(t, "policy", "--root", scratch(t, "no-policy-"))
	if code == 0 || !strings.Contains(string(out), "reading the policy in force") {
		t.Fatalf("missing snapshot should refuse: %d %s", code, out)
	}
}

func TestPolicyFaceShowsProfileListReasonAndSkippedProfiles(t *testing.T) {
	root := scratch(t, "policy-profiles-")
	content := `{
  "default_backend":"fallback",
  "seats":{"build":"fallback","design":"fallback"},
  "backends":{"fallback":{"adapter":"true","model":"m","effort":"low"}}
}`
	ref := filepath.Join(root, "backend-policy.copilot-gemini.json")
	if err := os.WriteFile(ref, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	switchedAt := time.Date(2026, 11, 27, 18, 8, 12, 0, time.UTC)
	resetAt := time.Date(2026, 11, 27, 20, 30, 0, 0, time.UTC)
	state := drain.PolicyState{
		Ref:          ref,
		Source:       "toml",
		Profile:      "copilot-gemini",
		Profiles:     []string{"claude-sub", "copilot-gemini"},
		ProfileIndex: 1,
		Why:          "switched from claude-sub at 2026-11-27T18:08:12Z: claude-sonnet five_hour used 0.82 >= 0.80",
		SwitchedFrom: "claude-sub",
		SwitchedAt:   &switchedAt,
		SkippedProfiles: []drain.SkippedProfileQuota{
			{
				Profile:         "claude-sub",
				Backend:         "claude-sonnet",
				Windows:         []drain.QuotaWindow{{Kind: "five_hour", Fraction: 0.82, ResetAt: &resetAt}},
				Reserve:         0.2,
				ReopenThreshold: 0.7,
				ResetAt:         &resetAt,
			},
		},
		Hash:    fmt.Sprintf("%x", sha256.Sum256([]byte(content))),
		Content: content,
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(drain.LoopDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(drain.PolicyStateFile(root), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := invoke(t, "policy", "--root", root)
	if code != 0 {
		t.Fatalf("policy: %d %s", code, out)
	}
	text := string(out)
	for _, want := range []string{
		"source=toml",
		"profile=copilot-gemini",
		"profiles=claude-sub,copilot-gemini (2/2)",
		"policy reason: switched from claude-sub",
		"switched from: claude-sub at 2026-11-27T18:08:12Z",
		"skipped profiles:",
		"claude-sub (backend=claude-sonnet windows=[five_hour=0.82] reserve=0.2 reopen<=0.7 resets=2026-11-27T20:30:00Z)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("policy text missing %q:\n%s", want, text)
		}
	}

	code, jsonOut := invoke(t, "policy", "--root", root, "--json")
	if code != 0 {
		t.Fatalf("policy --json: %d %s", code, jsonOut)
	}
	var got drain.PolicyState
	if err := json.Unmarshal(jsonOut, &got); err != nil {
		t.Fatalf("unmarshal policy --json: %v\n%s", err, jsonOut)
	}
	if got.Profile != "copilot-gemini" || got.ProfileIndex != 1 || got.Source != "toml" ||
		len(got.Profiles) != 2 || got.Profiles[0] != "claude-sub" || got.Profiles[1] != "copilot-gemini" ||
		got.Why != state.Why || got.SwitchedFrom != "claude-sub" || len(got.SkippedProfiles) != 1 ||
		got.SkippedProfiles[0].Profile != "claude-sub" || got.SkippedProfiles[0].ReopenThreshold != 0.7 {
		t.Fatalf("policy --json mismatch: %+v", got)
	}
}
