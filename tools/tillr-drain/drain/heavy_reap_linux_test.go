//go:build linux

package drain

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The heavy supervisor is a child subreaper (prepareHeavyTree), so every orphan its
// command leaves is adopted by it. It reaped them only after the command ended
// (finishHeavyTree): on 2026-11-18 one supervisor of a heavy gate run, alive since
// 10:08, held 6,408 zombies, nearly all git. The supervisor reaps what it adopts as it
// exits, and never the direct child, whose exit status is the command's verdict.
//
// The subreaper is a helper process, so the test binary itself never adopts anything.
// Every wait below is for a state change that must come; there is no wall-clock bound.
func TestASubreaperReapsTheOrphansItAdoptsAndLeavesItsOwnChild(t *testing.T) {
	if dir := os.Getenv("TILLR_TEST_REAP_DIR"); dir != "" {
		subreaperHelper(t, dir)
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestASubreaperReapsTheOrphansItAdoptsAndLeavesItsOwnChild$", "-test.v")
	cmd.Env = append(os.Environ(), "TILLR_TEST_REAP_DIR="+scratchUnderDurable(t))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("subreaper helper: %v\n%s", err, out.String())
	}
}

func subreaperHelper(t *testing.T, dir string) {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	pidFile, release := filepath.Join(dir, "orphan.pid"), filepath.Join(dir, "release")
	if err := unix.Mkfifo(release, 0o600); err != nil {
		t.Fatal(err)
	}
	// The command's subshell starts the orphan in the background and exits at once, so
	// the orphan is adopted while it waits on the fifo. The command waits on stdin.
	command := exec.Command("sh", "-c", `( sh -c 'echo $$ > "$1.part" && mv "$1.part" "$1"; read x < "$2"' sh "$1" "$2" & ); read x; exit 7`, "sh", pidFile, release)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	direct := command.Process.Pid
	var orphan int
	for {
		raw, err := os.ReadFile(pidFile)
		if err == nil {
			if orphan, err = strconv.Atoi(strings.TrimSpace(string(raw))); err != nil {
				t.Fatal(err)
			}
			break
		}
		// The command blocks on stdin until the test closes it, so a command that has
		// exited (a zombie, since nothing has waited on it) can never publish the file.
		if p, perr := readHeavyProcess(direct); perr != nil || p.state == "Z" {
			t.Fatalf("the command ended before it published the orphan's pid: state %q, err %v", p.state, perr)
		}
		time.Sleep(time.Millisecond)
	}
	awaitProcess(t, orphan, func(p heavyProcess) bool { return p.parent == os.Getpid() })
	fifo, err := os.OpenFile(release, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fifo.WriteString("go\n"); err != nil {
		t.Fatal(err)
	}
	fifo.Close()
	awaitProcess(t, orphan, func(p heavyProcess) bool { return p.state == "Z" })

	if err := reapAdoptedOrphans(direct); err != nil {
		t.Fatal(err)
	}
	if p, err := readHeavyProcess(orphan); !heavyProcessDisappeared(err) {
		t.Fatalf("the adopted orphan %d was not reaped: state %q, err %v", orphan, p.state, err)
	}

	// The direct child, exited and unreaped, is the command's own: its status stays
	// for the command's Wait.
	stdin.Close()
	awaitProcess(t, direct, func(p heavyProcess) bool { return p.state == "Z" })
	if err := reapAdoptedOrphans(direct); err != nil {
		t.Fatal(err)
	}
	err = command.Wait()
	if command.ProcessState == nil || command.ProcessState.ExitCode() != 7 {
		t.Fatalf("the direct child's exit status was taken: %v", err)
	}
}

// awaitProcess waits, with no deadline, until pid's /proc state satisfies ok.
func awaitProcess(t *testing.T, pid int, ok func(heavyProcess) bool) {
	t.Helper()
	for {
		p, err := readHeavyProcess(pid)
		if err != nil {
			t.Fatalf("process %d: %v", pid, err)
		}
		if ok(p) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
