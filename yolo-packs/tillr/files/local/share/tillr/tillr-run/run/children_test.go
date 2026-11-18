package run_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestNormalExitEndsWorkerBackgroundChild(t *testing.T) {
	dir := scratch(t)
	pidFile := filepath.Join(dir, "child.pid")
	st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	req := run.Request{RunID: "children", Backend: "command", Command: []string{"sh", "-c", `sleep 300 >/dev/null 2>&1 & echo $! > "$1"`, "worker", pidFile}, WorkingDir: run.WorktreeDir}
	env, err := run.Exec(context.Background(), st, req, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	var rec run.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.EndCause != run.EndNormalExit {
		t.Fatalf("end cause: %s", rec.EndCause)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	// A killed orphan can remain a zombie until its new parent reaps it. Zombies
	// cannot write in the lane, and are not live processes for lane rotation.
	for deadline := time.Now().Add(hangGuard); time.Now().Before(deadline); {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(stat)[strings.LastIndexByte(string(stat), ')')+1:])
		if len(fields) > 0 && fields[0] == "Z" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("normal exit left worker child %d alive", pid)
}
