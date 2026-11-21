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

func TestPolicyFaceAcceptsEveryShippedPolicy(t *testing.T) {
	paths, err := filepath.Glob("../../config/tillr/backend-policy*.json")
	if err != nil || len(paths) < 6 {
		t.Fatalf("expected at least six shipped policies: %v, %v", paths, err)
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
