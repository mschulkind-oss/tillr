package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr/store"
	"golang.org/x/sys/unix"
)

func TestInitProcessPriorityFakeCgroupDelegatedParent(t *testing.T) {
	cgMount := t.TempDir()
	initCG := filepath.Join(cgMount, "init")
	if err := os.MkdirAll(initCG, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cgMount, "cgroup.subtree_control"), []byte("cpu memory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(initCG, "cgroup.controllers"), []byte("cpuset cpu io memory pids\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	procCgroup := filepath.Join(t.TempDir(), "proc_cgroup")
	if err := os.WriteFile(procCgroup, []byte("0::/init\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	storePath := filepath.Join(t.TempDir(), "state.db")
	prio := InitProcessPriority(storePath, cgMount, procCgroup)
	defer prio.Close()

	if prio.Mechanism() != "cgroup" {
		t.Fatalf("mechanism = %q, want cgroup (log: %s)", prio.Mechanism(), prio.LogLine())
	}
	cgRoot := filepath.Join(cgMount, "tillr-"+storePriorityHash(storePath))
	if prio.Summary().CgroupRoot != cgRoot {
		t.Fatalf("cgroup_root = %q, want %q", prio.Summary().CgroupRoot, cgRoot)
	}
	subtree, err := os.ReadFile(filepath.Join(cgRoot, "cgroup.subtree_control"))
	if err != nil || string(subtree) != "+cpu" {
		t.Fatalf("cg_root subtree_control = %q, err=%v; want +cpu", string(subtree), err)
	}
	for class, wantWeight := range map[ProcessClass]int{
		ProcessClassRepair: 400,
		ProcessClassGate:   200,
		ProcessClassProof:  100,
		ProcessClassWorker: 50,
	} {
		w, err := os.ReadFile(filepath.Join(cgRoot, string(class), "cpu.weight"))
		if err != nil || string(w) != strconv.Itoa(wantWeight) {
			t.Fatalf("%s cpu.weight = %q, err=%v; want %d", class, string(w), err, wantWeight)
		}
		fd, err := prio.CgroupFD(class)
		if err != nil || fd < 0 {
			t.Fatalf("CgroupFD(%s) = (%d, %v), want valid fd", class, fd, err)
		}
	}
	wantLog := "loop: process priority: cgroup v2 (" + cgRoot + "; cpu.weight repair=400 gate=200 proof=100 worker=50)"
	if prio.LogLine() != wantLog {
		t.Fatalf("LogLine() = %q, want %q", prio.LogLine(), wantLog)
	}
}

func TestInitProcessPriorityFakeCgroupSelfDelegated(t *testing.T) {
	cgMount := t.TempDir()
	if err := os.WriteFile(filepath.Join(cgMount, "cgroup.controllers"), []byte("cpu io\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cgMount, "cgroup.procs"), []byte("101\n202\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	procCgroup := filepath.Join(t.TempDir(), "proc_cgroup")
	if err := os.WriteFile(procCgroup, []byte("0::/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	storePath := filepath.Join(t.TempDir(), "state.db")
	prio := InitProcessPriority(storePath, cgMount, procCgroup)
	defer prio.Close()

	if prio.Mechanism() != "cgroup" {
		t.Fatalf("mechanism = %q, want cgroup (log: %s)", prio.Mechanism(), prio.LogLine())
	}
	supProcs, err := os.ReadFile(filepath.Join(cgMount, "supervisor", "cgroup.procs"))
	if err != nil || string(supProcs) != "101\n202\n" {
		t.Fatalf("supervisor/cgroup.procs = %q, err=%v; want 101\\n202\\n", string(supProcs), err)
	}
	ownSubtree, err := os.ReadFile(filepath.Join(cgMount, "cgroup.subtree_control"))
	if err != nil || string(ownSubtree) != "+cpu" {
		t.Fatalf("own cgroup.subtree_control = %q, err=%v; want +cpu", string(ownSubtree), err)
	}
	cgRoot := filepath.Join(cgMount, "tillr-"+storePriorityHash(storePath))
	if prio.Summary().CgroupRoot != cgRoot {
		t.Fatalf("cgroup_root = %q, want %q", prio.Summary().CgroupRoot, cgRoot)
	}
}

func TestInitProcessPriorityFallsBackToNiceOnUnavailableCgroup(t *testing.T) {
	base, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
	if err != nil {
		t.Fatal(err)
	}
	missingMount := filepath.Join(t.TempDir(), "missing-cgroup")
	procCgroup := filepath.Join(t.TempDir(), "proc_cgroup")
	if err := os.WriteFile(procCgroup, []byte("0::/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prio := InitProcessPriority(filepath.Join(t.TempDir(), "state.db"), missingMount, procCgroup)
	defer prio.Close()

	if prio.Mechanism() != "nice" {
		t.Fatalf("mechanism = %q, want nice", prio.Mechanism())
	}
	for class, offset := range processClassNiceOffsets {
		want := min(19, base+offset)
		got, ok := prio.NiceTarget(class)
		if !ok || got != want {
			t.Fatalf("NiceTarget(%s) = (%d, %v), want (%d, true)", class, got, ok, want)
		}
	}
	wantPrefix := "loop: process priority: nice (repair=" + strconv.Itoa(min(19, base)) +
		" gate=" + strconv.Itoa(min(19, base+5)) +
		" proof=" + strconv.Itoa(min(19, base+10)) +
		" worker=" + strconv.Itoa(min(19, base+15)) +
		"; cgroup unavailable: "
	if !strings.HasPrefix(prio.LogLine(), wantPrefix) {
		t.Fatalf("LogLine() = %q, want prefix %q", prio.LogLine(), wantPrefix)
	}
}

func TestLoopRunLogsPriorityOncePersistsSessionAndWhyReportsIt(t *testing.T) {
	cgMount := t.TempDir()
	if err := os.WriteFile(filepath.Join(cgMount, "cgroup.controllers"), []byte("cpu\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cgMount, "cgroup.procs"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	procCgroup := filepath.Join(t.TempDir(), "proc_cgroup")
	if err := os.WriteFile(procCgroup, []byte("0::/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, l0 := passFixture(t, "item-prio-why")
	// Mark the fixture item landed so ExitWhenDrained exits immediately after one pass.
	stInit, err := store.Open(context.Background(), f.storePath, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := stInit.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = 'landed' WHERE item_id = 'item-prio-why'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_ = stInit.Close()

	var report bytes.Buffer
	cfg := l0.cfg
	cfg.NoNotify = true
	cfg.ExitWhenDrained = true
	cfg.Report = &report
	cfg.CgroupMountRoot = cgMount
	cfg.ProcSelfCgroupPath = procCgroup
	l, err := NewLoop(cfg)
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res, runErr := l.Run(ctx)
	if runErr != nil {
		t.Fatalf("Loop.Run: %v (result=%+v)", runErr, res)
	}

	cgRoot := filepath.Join(cgMount, "tillr-"+storePriorityHash(f.storePath))
	wantLine := "loop: process priority: cgroup v2 (" + cgRoot + "; cpu.weight repair=400 gate=200 proof=100 worker=50)"
	if count := strings.Count(report.String(), wantLine); count != 1 {
		t.Fatalf("startup log count = %d, want 1\nreport:\n%s", count, report.String())
	}

	// Verify session persistence and LiveLoopProcessPriority while a session is live,
	// and omission after ended_at is set.
	st2, err := store.Open(ctx, f.storePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()

	owner, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	ownerJSON, _ := json.Marshal(owner)
	summary := InitProcessPriority(f.storePath, cgMount, procCgroup)
	defer summary.Close()
	prioJSON, _ := json.Marshal(summary.Summary())

	now := l.cfg.Now()
	if err := st2.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, loop_owner, lanes, process_priority) VALUES ('live-prio', ?, ?, ?, ?, '["lane-1"]', ?)`,
			now.UnixMilli(), now.UnixMilli(), now.Add(time.Minute).UnixMilli(), string(ownerJSON), string(prioJSON),
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	r, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.ReadTx(ctx, func(rd *store.Reader) error {
		got, ok, err := LiveLoopProcessPriority(rd, f.storePath, now)
		if err != nil || !ok {
			t.Fatalf("LiveLoopProcessPriority = (%+v, %v, %v), want ok", got, ok, err)
		}
		if got.Mechanism != "cgroup" || got.CgroupRoot != cgRoot || got.Classes["repair"] != 400 {
			t.Fatalf("LiveLoopProcessPriority = %+v, want cgroup root %s", got, cgRoot)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Mark session ended -> omitted.
	if err := st2.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE drain_sessions SET ended_at = ? WHERE session_id = 'live-prio'`, now.UnixMilli())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.ReadTx(ctx, func(rd *store.Reader) error {
		got, ok, err := LiveLoopProcessPriority(rd, f.storePath, now)
		if err != nil || ok || got != nil {
			t.Fatalf("after end: LiveLoopProcessPriority = (%+v, %v, %v), want nil,false,nil", got, ok, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProcessPlacementCgroupMechanism(t *testing.T) {
	cgMount := t.TempDir()
	if err := os.WriteFile(filepath.Join(cgMount, "cgroup.controllers"), []byte("cpu\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cgMount, "cgroup.procs"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	procCgroup := filepath.Join(t.TempDir(), "proc_cgroup")
	if err := os.WriteFile(procCgroup, []byte("0::/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prio := InitProcessPriority(filepath.Join(t.TempDir(), "state.db"), cgMount, procCgroup)
	defer prio.Close()
	if prio.Mechanism() != "cgroup" {
		t.Fatalf("mechanism = %q, want cgroup", prio.Mechanism())
	}

	steps := &ProcessSteps{priority: prio}
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		class ProcessClass
	}{
		{name: "proof", ctx: context.Background(), class: ProcessClassProof},
		{name: "fixer-repair", ctx: context.WithValue(context.Background(), heavyRepairKey{}, true), class: ProcessClassRepair},
	} {
		cmd := exec.Command("true")
		if err := steps.applyHeavyPriority(tc.ctx, cmd); err != nil {
			t.Fatalf("%s applyHeavyPriority: %v", tc.name, err)
		}
		wantFD, err := prio.CgroupFD(tc.class)
		if err != nil {
			t.Fatal(err)
		}
		if cmd.SysProcAttr == nil || !cmd.SysProcAttr.UseCgroupFD || cmd.SysProcAttr.CgroupFD != wantFD {
			t.Fatalf("%s SysProcAttr = %+v, want UseCgroupFD=true CgroupFD=%d", tc.name, cmd.SysProcAttr, wantFD)
		}
	}

	for _, class := range []ProcessClass{ProcessClassRepair, ProcessClassGate, ProcessClassWorker} {
		cmd := exec.Command("true")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := prio.ApplyToCmd(cmd, class, ""); err != nil {
			t.Fatalf("ApplyToCmd(%s): %v", class, err)
		}
		wantFD, err := prio.CgroupFD(class)
		if err != nil {
			t.Fatal(err)
		}
		if !cmd.SysProcAttr.Setpgid || !cmd.SysProcAttr.UseCgroupFD || cmd.SysProcAttr.CgroupFD != wantFD {
			t.Fatalf("ApplyToCmd(%s) SysProcAttr = %+v, want Setpgid=true UseCgroupFD=true CgroupFD=%d", class, cmd.SysProcAttr, wantFD)
		}
	}

	// Runtime missing descriptor fails loudly naming <cg_root>/<class>.
	delete(prio.cgroupFDs, ProcessClassGate)
	err := prio.ApplyToCmd(exec.Command("true"), ProcessClassGate, "")
	if err == nil || !strings.Contains(err.Error(), filepath.Join(prio.Summary().CgroupRoot, "gate")) {
		t.Fatalf("missing fd error = %v, want path naming %s", err, filepath.Join(prio.Summary().CgroupRoot, "gate"))
	}
}

func TestProcessPlacementNiceMechanismAndChildInheritance(t *testing.T) {
	prio := newNicePriority("test")
	defer prio.Close()

	// Verify StartRun / worker & repair child inheritance using --tillr-exec-nice.
	for _, tc := range []struct {
		name  string
		class ProcessClass
	}{
		{name: "repair", class: ProcessClassRepair},
		{name: "gate", class: ProcessClassGate},
		{name: "proof", class: ProcessClassProof},
		{name: "worker", class: ProcessClassWorker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "nice.txt")
			cmd := exec.Command("/bin/sh", "-c", `sh -c nice > "$1"`, "nice-child", out)
			if err := prio.ApplyToCmd(cmd, tc.class, ""); err != nil {
				t.Fatalf("ApplyToCmd(%s): %v", tc.class, err)
			}
			if err := cmd.Run(); err != nil {
				t.Fatalf("cmd.Run(%s): %v", tc.class, err)
			}
			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := prio.NiceTarget(tc.class)
			if strings.TrimSpace(string(raw)) != strconv.Itoa(want) {
				t.Fatalf("class %s child nice = %q, want %d", tc.class, strings.TrimSpace(string(raw)), want)
			}
		})
	}

	// Verify heavy supervisor path (--tillr-heavy-supervisor) applies proof / repair niceness.
	steps := &ProcessSteps{priority: prio}
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		class ProcessClass
	}{
		{name: "heavy-proof", ctx: context.WithValue(context.Background(), heavyrun.HeavyUnownedKey{}, true), class: ProcessClassProof},
		{name: "heavy-fixer-repair", ctx: context.WithValue(context.WithValue(context.Background(), heavyrun.HeavyUnownedKey{}, true), heavyRepairKey{}, true), class: ProcessClassRepair},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "heavy-nice.txt")
			cmd := exec.CommandContext(tc.ctx, "/bin/sh", "-c", `sh -c nice > "$1"`, "heavy-child", out)
			if err := steps.applyHeavyPriority(tc.ctx, cmd); err != nil {
				t.Fatal(err)
			}
			if err := heavyrun.RunOwnedHeavy(tc.ctx, cmd, "legacy"); err != nil {
				t.Fatalf("RunOwnedHeavy: %v", err)
			}
			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := prio.NiceTarget(tc.class)
			if strings.TrimSpace(string(raw)) != strconv.Itoa(want) {
				t.Fatalf("%s child nice = %q, want %d", tc.name, strings.TrimSpace(string(raw)), want)
			}
		})
	}
}
