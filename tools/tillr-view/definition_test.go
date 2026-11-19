package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWaitingDefinitionHeader(t *testing.T) {
	h := newServeHarness(t)
	bin := filepath.Join(t.TempDir(), "tillr-view")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	for _, custom := range []bool{false, true} {
		name := definition.GitLandingName
		if custom {
			s, err := store.Open(context.Background(), h.store, true)
			if err != nil {
				t.Fatal(err)
			}
			d, err := definition.GitLanding()
			if err != nil {
				t.Fatal(err)
			}
			d.Name = "findings"
			name = d.Name
			raw, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := definition.Set(context.Background(), s, raw); err != nil {
				t.Fatal(err)
			}
			s.Close()
		}
		out, err := exec.Command(bin, "waiting", "--history", "--store", h.store).Output()
		if err != nil || !strings.HasPrefix(string(out), "definition: "+name+"\n") {
			t.Fatalf("header: %v %s", err, out)
		}
	}
}
