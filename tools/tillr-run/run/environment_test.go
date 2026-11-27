package run_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestExecRequestEnvironment(t *testing.T) {
	// Decode the wire request so omission and explicit false remain distinct.
	for _, tc := range []struct {
		name       string
		node       string
		scratchEnv string
		inherited  bool
		preserve   bool
		group      string
	}{
		{"default", "node-build", "", true, false, ""},
		{"explicit-true", "node-build", `,"scratch_env":true`, true, false, ""},
		{"preserve", "node-build", `,"scratch_env":false`, true, true, ""},
		{"preserve-unset", "", `,"scratch_env":false`, false, true, ""},
		{"default-unset", "", "", false, false, ""},
		// A read-only node is told so: the pi adapter keeps a writer going until it has
		// committed, and a reader has no commit to make (backends/pi/keep-going.ts).
		{"read-only", "node-review", `,"read_only_group":"group-review"`, true, false, "group-review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := scratch(t)
			s, err := store.InitFixture(ctx, filepath.Join(dir, "fixture.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			callerScratch := filepath.Join(dir, "caller-scratch")
			t.Setenv("YOLO_DURABLE_DIR", callerScratch)
			if !tc.inherited {
				if err := os.Unsetenv("YOLO_DURABLE_DIR"); err != nil {
					t.Fatal(err)
				}
			}
			for _, key := range []string{"TILLR_RUN_ID", "TILLR_ITEM_ID", "TILLR_NODE_ID", "TILLR_LANE_ID", "TILLR_READ_ONLY_GROUP"} {
				t.Setenv(key, "stale-parent-value")
			}
			var req run.Request
			wire := fmt.Sprintf(`{"run_id":"run-env","item_id":"item-env","lane_id":"lane-env","backend":"pi","working_dir":"worktree","node_id":%q%s}`, tc.node, tc.scratchEnv)
			if err := json.Unmarshal([]byte(wire), &req); err != nil {
				t.Fatal(err)
			}
			// A stub adapter reports actual child assignments, not the runner's intent.
			req.Command = []string{"/bin/sh", "-c", "env"}
			laneRoot := filepath.Join(dir, "lanes")
			envelope, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: laneRoot, Now: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			logDir := filepath.Join(laneRoot, req.LaneID)
			if tc.group != "" {
				logDir = run.NodeDir(logDir, req.RunID)
			}
			body, err := os.ReadFile(filepath.Join(logDir, req.RunID+".log"))
			if err != nil {
				t.Fatal(err)
			}
			env := map[string]string{}
			for _, line := range strings.Split(string(body), "\n") {
				key, value, ok := strings.Cut(line, "=")
				if ok {
					if _, duplicate := env[key]; duplicate {
						t.Errorf("duplicate child assignment for %s", key)
					}
					env[key] = value
				}
			}
			for key, want := range map[string]string{"TILLR_RUN_ID": req.RunID, "TILLR_ITEM_ID": req.ItemID, "TILLR_LANE_ID": req.LaneID} {
				if env[key] != want {
					t.Errorf("%s = %q, want %q", key, env[key], want)
				}
			}
			if tc.node == "" {
				if value, exists := env["TILLR_NODE_ID"]; exists {
					t.Errorf("absent node must not inherit a parent node: %q", value)
				}
			} else if env["TILLR_NODE_ID"] != tc.node {
				t.Errorf("TILLR_NODE_ID = %q, want %q", env["TILLR_NODE_ID"], tc.node)
			}
			if tc.group == "" {
				if value, exists := env["TILLR_READ_ONLY_GROUP"]; exists {
					t.Errorf("a writing run must not inherit a parent's read-only group: %q", value)
				}
			} else if env["TILLR_READ_ONLY_GROUP"] != tc.group {
				t.Errorf("TILLR_READ_ONLY_GROUP = %q, want %q", env["TILLR_READ_ONLY_GROUP"], tc.group)
			}
			runScratch := run.DurableDir(filepath.Join(laneRoot, req.LaneID), req.RunID)
			wantScratch := runScratch
			if tc.preserve {
				wantScratch = callerScratch
				if !tc.inherited {
					wantScratch = ""
				}
			}
			if env["YOLO_DURABLE_DIR"] != wantScratch {
				t.Errorf("scratch environment = %q, want %q", env["YOLO_DURABLE_DIR"], wantScratch)
			}
			if tc.preserve && !tc.inherited {
				if _, exists := env["YOLO_DURABLE_DIR"]; exists {
					t.Error("scratch_env=false must preserve an unset variable")
				}
			}
			var rec run.Record
			if err := json.Unmarshal(envelope.Payload, &rec); err != nil {
				t.Fatal(err)
			}
			assertScratchArtifact(t, rec.ArtifactRefs, runScratch)
			info, err := os.Stat(runScratch)
			if err != nil || !info.IsDir() {
				t.Fatalf("run scratch directory was not created: %v", err)
			}
			var stored string
			if err := s.ReadTx(ctx, func(r *store.Reader) error {
				return r.QueryRow("SELECT artifact_refs FROM runs WHERE run_id = ?", req.RunID).Scan(&stored)
			}); err != nil {
				t.Fatal(err)
			}
			var refs []run.ArtifactRef
			if err := json.Unmarshal([]byte(stored), &refs); err != nil {
				t.Fatal(err)
			}
			assertScratchArtifact(t, refs, runScratch)
		})
	}
}

func assertScratchArtifact(t *testing.T, refs []run.ArtifactRef, want string) {
	t.Helper()
	for _, ref := range refs {
		if ref.Kind == "scratch" && ref.Ref == want {
			return
		}
	}
	t.Errorf("run artifacts %v do not name scratch directory %q", refs, want)
}

func TestExecAccessPropagation(t *testing.T) {
	tests := []struct {
		name         string
		stagedAccess string
		reqAccess    string
		group        string
		seat         string
		wantAccess   string
	}{
		{
			name:       "default is write",
			wantAccess: run.AccessWrite,
		},
		{
			name:         "staged read_only file propagates TILLR_ACCESS",
			stagedAccess: run.AccessReadOnly,
			seat:         "plan",
			wantAccess:   run.AccessReadOnly,
		},
		{
			name:         "staged write file propagates TILLR_ACCESS",
			stagedAccess: run.AccessWrite,
			seat:         "build",
			wantAccess:   run.AccessWrite,
		},
		{
			name:       "read_only_group implies read_only",
			group:      "group-scout",
			seat:       "scout",
			wantAccess: run.AccessReadOnly,
		},
		{
			name:       "explicit request access overrides default",
			reqAccess:  run.AccessReadOnly,
			seat:       "review",
			wantAccess: run.AccessReadOnly,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := scratch(t)
			s, err := store.InitFixture(ctx, filepath.Join(dir, "fixture.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()

			t.Setenv("TILLR_ACCESS", "stale-parent-access")
			t.Setenv("TILLR_SEAT", "stale-parent-seat")
			t.Setenv("TILLR_OUTPUTS_DIR", "/stale/outputs")

			laneRoot := filepath.Join(dir, "lanes")
			req := run.Request{
				RunID:         "run-access",
				ItemID:        "item-access",
				NodeID:        "node-access",
				LaneID:        "lane-access",
				Backend:       "native",
				WorkingDir:    "worktree",
				ReadOnlyGroup: tc.group,
				Seat:          tc.seat,
				Access:        tc.reqAccess,
				Command:       []string{"/bin/sh", "-c", "env"},
			}
			artifactDir := filepath.Join(laneRoot, req.LaneID)
			if tc.group != "" {
				artifactDir = run.NodeDir(artifactDir, req.RunID)
			}
			if tc.stagedAccess != "" {
				if err := os.MkdirAll(artifactDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(artifactDir, run.AccessFile), []byte(tc.stagedAccess+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: laneRoot, Now: time.Now()}); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join(artifactDir, req.RunID+".log"))
			if err != nil {
				t.Fatal(err)
			}
			env := map[string]string{}
			for _, line := range strings.Split(string(body), "\n") {
				if k, v, ok := strings.Cut(line, "="); ok {
					env[k] = v
				}
			}
			if env["TILLR_ACCESS"] != tc.wantAccess {
				t.Errorf("TILLR_ACCESS = %q, want %q", env["TILLR_ACCESS"], tc.wantAccess)
			}
			if tc.seat == "" {
				if v, exists := env["TILLR_SEAT"]; exists {
					t.Errorf("TILLR_SEAT leaked parent value %q", v)
				}
			} else if env["TILLR_SEAT"] != tc.seat {
				t.Errorf("TILLR_SEAT = %q, want %q", env["TILLR_SEAT"], tc.seat)
			}
			wantOutputs := filepath.Join(filepath.Dir(laneRoot), "loop", req.RunID, "artifacts")
			if env["TILLR_OUTPUTS_DIR"] != wantOutputs {
				t.Errorf("TILLR_OUTPUTS_DIR = %q, want %q", env["TILLR_OUTPUTS_DIR"], wantOutputs)
			}
		})
	}
}
