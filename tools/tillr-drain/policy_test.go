package main_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func TestPolicyFaceReadsAcceptedContentNotDisk(t *testing.T) {
	root := scratch(t, "policy-face-")
	content := `{
 "default_backend":"a", "seats":{"build":["a","b"],"design":"b"},
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
	for _, want := range []string{"build=a,b", "design=b", "backend a: models=cheap effort=medium", "backend b: models=strong,stronger effort=high", "override special=b", "policy on disk is not the policy in force: the policy names no backends"} {
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
