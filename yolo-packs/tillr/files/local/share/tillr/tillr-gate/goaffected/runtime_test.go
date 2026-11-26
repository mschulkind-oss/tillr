package goaffected_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-gate/goaffected"
)

func TestConcurrentRunsDoNotShareScratchAndPreserveCache(t *testing.T) {
	tillrGateBin := buildTillrGateBinary(t, filepath.Join(t.TempDir(), "tillr-gate"))

	tmpPath := filepath.Join(scratchRoot(t), "ga-concurrent-"+freshNonce(t))
	repo := filepath.Join(tmpPath, "repo")
	for _, d := range []string{
		filepath.Join(repo, "tools", "tillr-conc"),
		filepath.Join(repo, "tools", "gate"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nonce := freshNonce(t)
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-conc", "go.mod"), []byte("module github.com/mschulkind-oss/tillr-conc\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	concTest := fmt.Sprintf(`package main
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)
func TestScratchIsolation(t *testing.T) {
	if t.Name() == %q { t.Fatal() }
	rec := map[string]string{
		"durable":   os.Getenv("YOLO_DURABLE_DIR"),
		"tmpdir":    os.Getenv("TMPDIR"),
		"gitconfig": os.Getenv("GIT_CONFIG_GLOBAL"),
	}
	if outPath := os.Getenv("CONC_OUT_FILE"); outPath != "" {
		raw, _ := json.Marshal(rec)
		if err := os.WriteFile(outPath, raw, 0o644); err != nil { t.Fatal(err) }
	}
	marker := filepath.Join(rec["durable"], "marker")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("scratch directory already had marker from another run")
	}
	if err := os.WriteFile(marker, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ready := os.Getenv("CONC_READY_FILE"); ready != "" {
		if err := os.WriteFile(ready, []byte("ok"), 0o644); err != nil { t.Fatal(err) }
	}
	if release := os.Getenv("CONC_RELEASE_FILE"); release != "" {
		for {
			if _, err := os.Stat(release); err == nil { break }
			time.Sleep(20 * time.Millisecond)
		}
	}
}
`, nonce)
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-conc", "conc_test.go"), []byte(concTest), 0o644); err != nil {
		t.Fatal(err)
	}

	sharedDurable := filepath.Join(tmpPath, "shared-durable")
	if err := os.MkdirAll(sharedDurable, 0o755); err != nil {
		t.Fatal(err)
	}
	gocache := sharedGocache(t)
	precompileGoTest(t, filepath.Join(repo, "tools", "tillr-conc"), gocache)

	out1 := filepath.Join(tmpPath, "out1.json")
	out2 := filepath.Join(tmpPath, "out2.json")
	ready1 := filepath.Join(tmpPath, "ready1")
	ready2 := filepath.Join(tmpPath, "ready2")
	release := filepath.Join(tmpPath, "release")

	base := withEnvOverrides(os.Environ(), map[string]string{
		"YOLO_DURABLE_DIR": sharedDurable,
		"GOCACHE":          gocache,
	})
	args := []string{"go-affected", "--root", repo, "--changed", "tools/tillr-conc/conc_test.go"}

	type procResult struct {
		out string
		err error
	}
	startProc := func(outFile, readyFile, releaseFile string) (*exec.Cmd, <-chan procResult) {
		c := exec.Command(tillrGateBin, args...)
		c.Env = withEnvOverrides(base, map[string]string{
			"CONC_OUT_FILE":     outFile,
			"CONC_READY_FILE":   readyFile,
			"CONC_RELEASE_FILE": releaseFile,
		})
		ch := make(chan procResult, 1)
		var buf strings.Builder
		c.Stdout = &buf
		c.Stderr = &buf
		if err := c.Start(); err != nil {
			t.Fatalf("start tillr-gate: %v", err)
		}
		go func() {
			err := c.Wait()
			ch <- procResult{out: buf.String(), err: err}
		}()
		return c, ch
	}

	p1, ch1 := startProc(out1, ready1, release)
	p2, ch2 := startProc(out2, ready2, release)
	defer func() {
		_ = os.WriteFile(release, []byte("cleanup\n"), 0o644)
		for _, p := range []*exec.Cmd{p1, p2} {
			if p.Process != nil {
				_ = p.Process.Kill()
			}
		}
	}()

	var res1, res2 procResult
	var done1, done2 bool
	for {
		_, err1 := os.Stat(ready1)
		_, err2 := os.Stat(ready2)
		if err1 == nil && err2 == nil {
			break
		}
		select {
		case res1 = <-ch1:
			done1 = true
			t.Fatalf("p1 exited before both concurrent runs reached barrier: err=%v\n%s", res1.err, res1.out)
		case res2 = <-ch2:
			done2 = true
			t.Fatalf("p2 exited before both concurrent runs reached barrier: err=%v\n%s", res2.err, res2.out)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !done1 {
		res1 = <-ch1
	}
	if !done2 {
		res2 = <-ch2
	}
	if res1.err != nil {
		t.Fatalf("p1 failed: %v\n%s", res1.err, res1.out)
	}
	if res2.err != nil {
		t.Fatalf("p2 failed: %v\n%s", res2.err, res2.out)
	}

	var r1, r2 map[string]string
	raw1, err := os.ReadFile(out1)
	if err != nil {
		t.Fatal(err)
	}
	raw2, err := os.ReadFile(out2)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw1, &r1); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw2, &r2); err != nil {
		t.Fatal(err)
	}
	if r1["durable"] == "" || r2["durable"] == "" || r1["durable"] == r2["durable"] {
		t.Fatalf("expected distinct durable dirs, got r1=%v r2=%v", r1, r2)
	}
	if r1["gitconfig"] == "" || r2["gitconfig"] == "" || r1["gitconfig"] == r2["gitconfig"] {
		t.Fatalf("expected distinct gitconfig paths, got r1=%v r2=%v", r1, r2)
	}

	// Sequential runs after both finish reuse slot-0 and hit Go's test result cache.
	evFile := filepath.Join(tmpPath, "seq-ev.json")
	seqArgs := append(append([]string{}, args...), "--evidence-out", evFile)
	seqCmd1 := exec.Command(tillrGateBin, seqArgs...)
	seqCmd1.Env = base
	if out, err := seqCmd1.CombinedOutput(); err != nil {
		t.Fatalf("seq1 failed: %v\n%s", err, out)
	}
	seqCmd2 := exec.Command(tillrGateBin, seqArgs...)
	seqCmd2.Env = base
	if out, err := seqCmd2.CombinedOutput(); err != nil {
		t.Fatalf("seq2 failed: %v\n%s", err, out)
	}
	evRaw, err := os.ReadFile(evFile)
	if err != nil {
		t.Fatal(err)
	}
	var ev2 goaffected.Evidence
	if err := json.Unmarshal(evRaw, &ev2); err != nil {
		t.Fatal(err)
	}
	if len(ev2.Packages) != 1 || !ev2.Packages[0].Cached {
		t.Fatalf("expected cached=true on seq2, got %+v", ev2)
	}
}

func TestStreamsProgressLinesBeforeRunEnds(t *testing.T) {
	pkg := "github.com/mschulkind-oss/tillr-stream"
	if line, ok := goaffected.FormatProgressEvent("not json\n"); ok || line != "" {
		t.Fatalf("unexpected progress for invalid json: %q %v", line, ok)
	}
	if line, ok := goaffected.FormatProgressEvent(fmt.Sprintf(`{"Action":"start","Package":%q}`, pkg)); ok {
		t.Fatalf("unexpected progress for start: %q", line)
	}
	if line, ok := goaffected.FormatProgressEvent(fmt.Sprintf(`{"Action":"run","Package":%q,"Test":"TestA"}`, pkg)); ok {
		t.Fatalf("unexpected progress for run: %q", line)
	}
	if line, ok := goaffected.FormatProgressEvent(fmt.Sprintf(`{"Action":"output","Package":%q,"Output":"hi\n"}`, pkg)); ok {
		t.Fatalf("unexpected progress for output: %q", line)
	}
	if line, ok := goaffected.FormatProgressEvent(fmt.Sprintf(`{"Action":"pass","Package":%q,"Test":"TestA","Elapsed":0.12}`, pkg)); !ok || line != fmt.Sprintf("go-affected: pass %s TestA (0.12s)\n", pkg) {
		t.Fatalf("unexpected pass test progress: %q %v", line, ok)
	}
	if line, ok := goaffected.FormatProgressEvent(fmt.Sprintf(`{"Action":"fail","Package":%q,"Test":"TestB","Elapsed":1.5}`, pkg)); !ok || line != fmt.Sprintf("go-affected: fail %s TestB (1.50s)\n", pkg) {
		t.Fatalf("unexpected fail test progress: %q %v", line, ok)
	}
	if line, ok := goaffected.FormatProgressEvent(fmt.Sprintf(`{"Action":"skip","Package":%q,"Test":"TestC","Elapsed":0.0}`, pkg)); !ok || line != fmt.Sprintf("go-affected: skip %s TestC (0.00s)\n", pkg) {
		t.Fatalf("unexpected skip test progress: %q %v", line, ok)
	}
	if line, ok := goaffected.FormatProgressEvent(fmt.Sprintf(`{"Action":"pass","Package":%q,"Elapsed":2.34}`, pkg)); !ok || line != fmt.Sprintf("go-affected: pass %s (2.34s)\n", pkg) {
		t.Fatalf("unexpected pass pkg progress: %q %v", line, ok)
	}

	tillrGateBin := buildTillrGateBinary(t, filepath.Join(t.TempDir(), "tillr-gate"))
	tmpPath := filepath.Join(scratchRoot(t), "ga-stream-"+freshNonce(t))
	repo := filepath.Join(tmpPath, "repo")
	for _, d := range []string{
		filepath.Join(repo, "tools", "tillr-stream"),
		filepath.Join(repo, "tools", "gate"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-stream", "go.mod"), []byte(fmt.Sprintf("module %s\n\ngo 1.22\n", pkg)), 0o644); err != nil {
		t.Fatal(err)
	}
	releaseFile := filepath.Join(tmpPath, "release")
	streamTest := `package main
import (
	"os"
	"testing"
	"time"
)
func TestFirst(t *testing.T) {}
func TestSecond(t *testing.T) {
	release := os.Getenv("STREAM_RELEASE_FILE")
	for {
		if _, err := os.Stat(release); err == nil { return }
		time.Sleep(20 * time.Millisecond)
	}
}
`
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-stream", "stream_test.go"), []byte(streamTest), 0o644); err != nil {
		t.Fatal(err)
	}
	durable := filepath.Join(tmpPath, "durable")
	if err := os.MkdirAll(durable, 0o755); err != nil {
		t.Fatal(err)
	}
	gocache := sharedGocache(t)
	precompileGoTest(t, filepath.Join(repo, "tools", "tillr-stream"), gocache)

	cmd := exec.Command(tillrGateBin, "go-affected", "--root", repo, "--changed", "tools/tillr-stream/stream_test.go")
	cmd.Env = withEnvOverrides(os.Environ(), map[string]string{
		"YOLO_DURABLE_DIR":    durable,
		"GOCACHE":             gocache,
		"STREAM_RELEASE_FILE": releaseFile,
	})
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := false
	defer func() {
		if _, err := os.Stat(releaseFile); err != nil {
			_ = os.WriteFile(releaseFile, []byte("cleanup\n"), 0o644)
		}
		if !exited && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	var seenLines []string
	sawFirstWhileRunning := false
	scanner := bufio.NewScanner(stdoutPipe)
	for scanner.Scan() {
		line := scanner.Text()
		seenLines = append(seenLines, line)
		if !sawFirstWhileRunning && strings.Contains(line, fmt.Sprintf("go-affected: pass %s TestFirst ", pkg)) {
			var ws syscall.WaitStatus
			wpid, werr := syscall.Wait4(cmd.Process.Pid, &ws, syscall.WNOHANG, nil)
			if werr == nil && wpid != 0 {
				t.Fatalf("process already exited before streaming TestFirst (status=%v): %v", ws, seenLines)
			}
			if _, err := os.Stat(releaseFile); err == nil {
				t.Fatal("TestSecond was already released")
			}
			sawFirstWhileRunning = true
			if err := os.WriteFile(releaseFile, []byte("go\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	if !sawFirstWhileRunning {
		t.Fatalf("did not receive TestFirst progress line while running: %v", seenLines)
	}
	waitErr := cmd.Wait()
	exited = true
	if waitErr != nil {
		t.Fatalf("tillr-gate failed: %v\n%s", waitErr, strings.Join(seenLines, "\n"))
	}
	var hasSecond, hasPkgPass, hasSummary bool
	for _, ln := range seenLines {
		if strings.Contains(ln, fmt.Sprintf("go-affected: pass %s TestSecond ", pkg)) {
			hasSecond = true
		}
		if strings.HasPrefix(ln, fmt.Sprintf("go-affected: pass %s (", pkg)) {
			hasPkgPass = true
		}
		if strings.HasPrefix(ln, "go-affected: 1 package(s)") {
			hasSummary = true
		}
	}
	if !hasSecond || !hasPkgPass || !hasSummary {
		t.Fatalf("missing expected progress/summary lines in %v", seenLines)
	}
}

func TestStalledRunPrintsNothingWithoutTestEvents(t *testing.T) {
	pkg := "github.com/mschulkind-oss/tillr-stall"
	procCPUTicks := func(pid int) (int64, bool) {
		rawBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return 0, false
		}
		raw := string(rawBytes)
		end := strings.LastIndexByte(raw, ')')
		if end < 0 {
			return 0, false
		}
		fields := strings.Fields(raw[end+1:])
		if len(fields) < 13 {
			return 0, false
		}
		utime, err1 := strconv.ParseInt(fields[11], 10, 64)
		stime, err2 := strconv.ParseInt(fields[12], 10, 64)
		if err1 != nil || err2 != nil {
			return 0, false
		}
		return utime + stime, true
	}

	tillrGateBin := buildTillrGateBinary(t, filepath.Join(t.TempDir(), "tillr-gate"))
	tmpPath := filepath.Join(scratchRoot(t), "ga-stall-"+freshNonce(t))
	repo := filepath.Join(tmpPath, "repo")
	for _, d := range []string{
		filepath.Join(repo, "tools", "tillr-stall"),
		filepath.Join(repo, "tools", "gate"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-stall", "go.mod"), []byte(fmt.Sprintf("module %s\n\ngo 1.22\n", pkg)), 0o644); err != nil {
		t.Fatal(err)
	}
	startedFile := filepath.Join(tmpPath, "started")
	releaseFile := filepath.Join(tmpPath, "release")
	stallTest := `package main
import (
	"os"
	"testing"
	"time"
)
func TestStalls(t *testing.T) {
	if err := os.WriteFile(os.Getenv("STALL_STARTED_FILE"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	release := os.Getenv("STALL_RELEASE_FILE")
	for {
		if _, err := os.Stat(release); err == nil { return }
		time.Sleep(20 * time.Millisecond)
	}
}
`
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-stall", "stall_test.go"), []byte(stallTest), 0o644); err != nil {
		t.Fatal(err)
	}
	durable := filepath.Join(tmpPath, "durable")
	if err := os.MkdirAll(durable, 0o755); err != nil {
		t.Fatal(err)
	}
	gocache := sharedGocache(t)
	precompileGoTest(t, filepath.Join(repo, "tools", "tillr-stall"), gocache)

	cmd := exec.Command(tillrGateBin, "go-affected", "--root", repo, "--changed", "tools/tillr-stall/stall_test.go")
	cmd.Env = withEnvOverrides(os.Environ(), map[string]string{
		"YOLO_DURABLE_DIR":   durable,
		"GOCACHE":            gocache,
		"STALL_STARTED_FILE": startedFile,
		"STALL_RELEASE_FILE": releaseFile,
	})
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	lineCh := make(chan string, 16)
	scanErrCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		scanErrCh <- scanner.Err()
		close(lineCh)
	}()
	defer func() {
		if _, err := os.Stat(releaseFile); err != nil {
			_ = os.WriteFile(releaseFile, []byte("cleanup\n"), 0o644)
		}
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	var preStallLines []string
	for {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		select {
		case ln, ok := <-lineCh:
			if !ok {
				<-scanErrCh
				err := cmd.Wait()
				t.Fatalf("tillr-gate exited prematurely: %v\n%s", err, strings.Join(preStallLines, "\n"))
			}
			preStallLines = append(preStallLines, ln)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if len(preStallLines) > 0 {
		t.Fatalf("unexpected output before stall window: %q", preStallLines[0])
	}
	// Allow tillr-gate to finish consuming start/run events and block on the pipe.
	time.Sleep(200 * time.Millisecond)

	// Drain any buffered lines emitted before blocking (there should be none for start/run).
	for {
		select {
		case ln, ok := <-lineCh:
			if !ok {
				<-scanErrCh
				t.Fatalf("stdout closed before stall window (wait: %v)", cmd.Wait())
			}
			t.Fatalf("unexpected output before stall window: %q", ln)
		default:
			goto drained
		}
	}
drained:
	cpuBefore, hasCPU := procCPUTicks(cmd.Process.Pid)
	select {
	case ln, ok := <-lineCh:
		if ok {
			t.Fatalf("stalled run unexpectedly produced output: %q", ln)
		}
		<-scanErrCh
		t.Fatalf("stdout closed during stall window (wait: %v)", cmd.Wait())
	case <-time.After(2 * time.Second):
	}
	if hasCPU {
		if cpuAfter, ok := procCPUTicks(cmd.Process.Pid); ok && cpuAfter != cpuBefore {
			t.Fatalf("wrapper consumed CPU while test stalled: before=%d after=%d", cpuBefore, cpuAfter)
		}
	}

	if err := os.WriteFile(releaseFile, []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var tail []string
	for ln := range lineCh {
		tail = append(tail, ln)
	}
	if err := <-scanErrCh; err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("tillr-gate failed: %v\n%s", err, strings.Join(tail, "\n"))
	}
	joined := strings.Join(tail, "\n")
	if !strings.Contains(joined, fmt.Sprintf("go-affected: pass %s TestStalls ", pkg)) {
		t.Fatalf("missing TestStalls pass line in %s", joined)
	}
	if !strings.Contains(joined, fmt.Sprintf("go-affected: pass %s (", pkg)) {
		t.Fatalf("missing package pass line in %s", joined)
	}
}

func TestScopedReadTestsExecutionCacheAndSharding(t *testing.T) {
	pkg := "github.com/mschulkind-oss/tillr-scoped"
	sibPkg := "github.com/mschulkind-oss/tillr-sib"
	tmpPath := filepath.Join(scratchRoot(t), "ga-scoped-"+freshNonce(t))
	repo := filepath.Join(tmpPath, "repo")
	for _, d := range []string{
		filepath.Join(repo, "tools", "tillr-sib"),
		filepath.Join(repo, "tools", "tillr-scoped"),
		filepath.Join(repo, "tools", "gate"),
		filepath.Join(repo, "config"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-sib", "go.mod"), []byte(fmt.Sprintf("module %s\n\ngo 1.22\n", sibPkg)), 0o644); err != nil {
		t.Fatal(err)
	}
	sibMain := filepath.Join(repo, "tools", "tillr-sib", "main.go")
	if err := os.WriteFile(sibMain, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plannerFile := filepath.Join(repo, "tools", "planner.py")
	if err := os.WriteFile(plannerFile, []byte("PLANNER_V = 'v1'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(repo, "config", "app.txt")
	if err := os.WriteFile(configFile, []byte("cfg-ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pidsDir := filepath.Join(tmpPath, "pids")
	if err := os.MkdirAll(pidsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	nonce := freshNonce(t)
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-scoped", "go.mod"), []byte(fmt.Sprintf("module %s\n\ngo 1.22\n", pkg)), 0o644); err != nil {
		t.Fatal(err)
	}
	scopedTest := fmt.Sprintf(`package main
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)
func TestEdgeInputs(t *testing.T) {
	if t.Name() == %q { t.Fatal() }
	if os.Getenv("TILLR_GATE_EDGE_INPUTS") == "" {
		t.Fatal("missing TILLR_GATE_EDGE_INPUTS")
	}
}
func recordPID(t *testing.T, name string) {
	if dir := os.Getenv("SCOPED_PIDS_DIR"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(fmt.Sprintf("%%d", os.Getpid())), 0o644)
	}
}
func TestReadsPlannerA(t *testing.T) {
	recordPID(t, "A")
	raw, err := os.ReadFile("../../tools/planner.py")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(raw), "PLANNER_V") { t.Fatalf("bad planner: %%q", raw) }
}
func TestReadsPlannerB(t *testing.T) {
	recordPID(t, "B")
	raw, err := os.ReadFile("../../tools/planner.py")
	if err != nil { t.Fatal(err) }
	if strings.Contains(string(raw), "BROKEN_B") { t.Fatalf("planner broke B: %%q", raw) }
}
func TestReadsConfig(t *testing.T) {
	recordPID(t, "CFG")
	raw, err := os.ReadFile("../../config/app.txt")
	if err != nil { t.Fatal(err) }
	if strings.TrimSpace(string(raw)) != "cfg-ok" { t.Fatalf("bad cfg: %%q", raw) }
}
func TestUnrelatedMustNotRunWhenNarrowed(t *testing.T) {
	recordPID(t, "UNREL")
	if os.Getenv("ALLOW_UNRELATED") != "1" {
		t.Fatal("unrelated test ran during narrowed reads execution")
	}
}
`, nonce)
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-scoped", "scoped_test.go"), []byte(scopedTest), 0o644); err != nil {
		t.Fatal(err)
	}

	edgesData := map[string]goaffected.EdgeSpec{
		pkg: {
			Builds: []string{sibPkg},
			Reads:  []string{"config/app.txt", "tools/planner.py"},
			ReadTests: map[string][]string{
				"tools/planner.py": {"TestReadsPlannerA", "TestReadsPlannerB"},
			},
		},
	}
	edgesRaw, _ := json.Marshal(edgesData)
	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), edgesRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	// Estimated total = 20.0 + 15.0 = 35.0s >= 25.0s across 2 reader tests -> 2 LPT shards.
	durationsRaw, _ := json.Marshal(map[string]map[string]float64{
		pkg: {
			"TestReadsPlannerA": 20.0,
			"TestReadsPlannerB": 15.0,
		},
	})
	if err := os.WriteFile(filepath.Join(repo, "tools", "go-test-durations.json"), durationsRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	evFile := filepath.Join(tmpPath, "ev.json")
	durable := filepath.Join(tmpPath, "durable")
	if err := os.MkdirAll(durable, 0o755); err != nil {
		t.Fatal(err)
	}
	gocache := sharedGocache(t)
	precompileGoTest(t, filepath.Join(repo, "tools", "tillr-scoped"), gocache)

	runScoped := func(changed []string, extra map[string]string) (int, string, *goaffected.Evidence) {
		t.Helper()
		t.Setenv("GOCACHE", gocache)
		t.Setenv("YOLO_DURABLE_DIR", durable)
		t.Setenv("SCOPED_PIDS_DIR", pidsDir)
		for k, v := range extra {
			t.Setenv(k, v)
		}
		sel, err := goaffected.SelectAffectedPackages(repo, changed, nil, nil, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackages: %v", err)
		}
		rc, out, ev, err := goaffected.RunSelectedTests(repo, sel, goaffected.RunOptions{
			EvidencePath:    evFile,
			DisableProgress: true,
		})
		if err != nil {
			t.Fatalf("RunSelectedTests: %v", err)
		}
		return rc, out, ev
	}

	// 1. Scoped change runs only TestReadsPlannerA + TestReadsPlannerB (+ TestEdgeInputs),
	// splits into 2 LPT shard processes, and records "tests" in evidence.
	rc, out, ev := runScoped([]string{"tools/planner.py", "tests/planner.test.py"}, nil)
	if rc != 0 {
		t.Fatalf("scoped run 1 failed rc=%d:\n%s", rc, out)
	}
	pidABytes, errA := os.ReadFile(filepath.Join(pidsDir, "A"))
	pidBBytes, errB := os.ReadFile(filepath.Join(pidsDir, "B"))
	if errA != nil || errB != nil {
		t.Fatalf("expected A and B pid files, got errA=%v errB=%v", errA, errB)
	}
	if _, err := os.Stat(filepath.Join(pidsDir, "CFG")); err == nil {
		t.Fatal("TestReadsConfig ran unexpectedly")
	}
	if _, err := os.Stat(filepath.Join(pidsDir, "UNREL")); err == nil {
		t.Fatal("TestUnrelatedMustNotRunWhenNarrowed ran unexpectedly")
	}
	if string(pidABytes) == string(pidBBytes) {
		t.Fatalf("expected 2 LPT shards for 35s reader subset, got same pid %s", string(pidABytes))
	}
	if len(ev.Packages) != 1 || ev.Packages[0].Cached || ev.Packages[0].Tests == nil || !reflect.DeepEqual(*ev.Packages[0].Tests, []string{"TestReadsPlannerA", "TestReadsPlannerB"}) {
		t.Fatalf("unexpected evidence 1: %+v", ev)
	}

	// 2. Second run with unchanged inputs hits Go's result cache across both shards
	// without re-executing either test binary.
	_ = os.Remove(filepath.Join(pidsDir, "A"))
	_ = os.Remove(filepath.Join(pidsDir, "B"))
	rc, out, ev = runScoped([]string{"tools/planner.py"}, nil)
	if rc != 0 {
		t.Fatalf("scoped run 2 failed rc=%d:\n%s", rc, out)
	}
	if len(ev.Packages) != 1 || !ev.Packages[0].Cached || ev.Packages[0].Tests == nil || !reflect.DeepEqual(*ev.Packages[0].Tests, []string{"TestReadsPlannerA", "TestReadsPlannerB"}) {
		t.Fatalf("unexpected evidence 2: %+v", ev)
	}
	if _, err := os.Stat(filepath.Join(pidsDir, "A")); err == nil {
		t.Fatal("A executed on cached run")
	}
	if _, err := os.Stat(filepath.Join(pidsDir, "B")); err == nil {
		t.Fatal("B executed on cached run")
	}

	// 3. Editing tools/planner.py invalidates the digest and catches TestReadsPlannerB's failure.
	if err := os.WriteFile(plannerFile, []byte("PLANNER_V = 'v2'\nBROKEN_B = True\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, ev = runScoped([]string{"tools/planner.py"}, nil)
	if rc == 0 || ev.Packages[0].Cached || !strings.Contains(out, "TestReadsPlannerB") {
		t.Fatalf("expected failure on TestReadsPlannerB, got rc=%d ev=%+v\n%s", rc, ev, out)
	}

	// 4. Per-invocation active_tests digest scoping: non-reader shard digests ignore
	// tools/planner.py edits while still tracking builds and unscoped reads.
	graph, err := goaffected.LoadTillrGraph(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	edgeEntry := edgesData[pkg]
	dUnrelBefore, err := goaffected.ComputeEdgeDigest(repo, edgeEntry, graph.PkgInfo, []string{"TestUnrelatedMustNotRunWhenNarrowed"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dReaderBefore, err := goaffected.ComputeEdgeDigest(repo, edgeEntry, graph.PkgInfo, []string{"TestReadsPlannerA"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plannerFile, []byte("PLANNER_V = 'v3'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dUnrelAfterFC, err := goaffected.ComputeEdgeDigest(repo, edgeEntry, graph.PkgInfo, []string{"TestUnrelatedMustNotRunWhenNarrowed"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dReaderAfterFC, err := goaffected.ComputeEdgeDigest(repo, edgeEntry, graph.PkgInfo, []string{"TestReadsPlannerA"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dUnrelAfterFC != dUnrelBefore {
		t.Fatal("non-reader shard digest changed on scoped read edit")
	}
	if dReaderAfterFC == dReaderBefore {
		t.Fatal("reader shard digest did not change on scoped read edit")
	}

	if err := os.WriteFile(configFile, []byte("cfg-ok-2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dUnrelAfterCfg, err := goaffected.ComputeEdgeDigest(repo, edgeEntry, graph.PkgInfo, []string{"TestUnrelatedMustNotRunWhenNarrowed"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dUnrelAfterCfg == dUnrelAfterFC {
		t.Fatal("non-reader shard digest missed unscoped read edit")
	}
	if err := os.WriteFile(configFile, []byte("cfg-ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(sibMain, []byte("package main\nfunc main() { println(\"v2\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dUnrelAfterSib, err := goaffected.ComputeEdgeDigest(repo, edgeEntry, graph.PkgInfo, []string{"TestUnrelatedMustNotRunWhenNarrowed"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dUnrelAfterSib == dUnrelAfterFC {
		t.Fatal("non-reader shard digest missed builds edit")
	}

	// 5. Pairing with an unscoped reads change widens back to whole-package (no Tests field in evidence).
	rc, out, ev = runScoped([]string{"tools/planner.py", "config/app.txt"}, map[string]string{
		"ALLOW_UNRELATED": "1",
	})
	if rc != 0 {
		t.Fatalf("widened run failed rc=%d:\n%s", rc, out)
	}
	if _, err := os.Stat(filepath.Join(pidsDir, "CFG")); err != nil {
		t.Fatal("CFG did not run on widened run")
	}
	if _, err := os.Stat(filepath.Join(pidsDir, "UNREL")); err != nil {
		t.Fatal("UNREL did not run on widened run")
	}
	if ev.Packages[0].Tests != nil {
		t.Fatalf("expected nil Tests on widened run, got %v", *ev.Packages[0].Tests)
	}
}

func TestScopedBuildTestsExecutionCacheAndSharding(t *testing.T) {
	const (
		pkg     = "github.com/mschulkind-oss/tillr-scoped"
		sibAPkg = "github.com/mschulkind-oss/tillr-siba"
		sibBPkg = "github.com/mschulkind-oss/tillr-sibb"
	)
	tmpPath := filepath.Join(scratchRoot(t), "ga-build-scoped-"+freshNonce(t))
	repo := filepath.Join(tmpPath, "repo")
	for _, d := range []string{
		filepath.Join(repo, "tools", "tillr-siba"),
		filepath.Join(repo, "tools", "tillr-sibb"),
		filepath.Join(repo, "tools", "tillr-scoped"),
		filepath.Join(repo, "tools", "gate"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-siba", "go.mod"), []byte("module "+sibAPkg+"\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sibAMain := filepath.Join(repo, "tools", "tillr-siba", "main.go")
	if err := os.WriteFile(sibAMain, []byte("package main\nfunc main() { println(\"sibA-v1\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-sibb", "go.mod"), []byte("module "+sibBPkg+"\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sibBMain := filepath.Join(repo, "tools", "tillr-sibb", "main.go")
	if err := os.WriteFile(sibBMain, []byte("package main\nfunc main() { println(\"sibB-v1\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pidsDir := filepath.Join(tmpPath, "pids")
	if err := os.MkdirAll(pidsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	nonce := freshNonce(t)
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-scoped", "go.mod"), []byte("module "+pkg+"\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scopedTest := fmt.Sprintf(`package main
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)
func TestEdgeInputs(t *testing.T) {
	if t.Name() == %q { t.Fatal() }
	if os.Getenv("TILLR_GATE_EDGE_INPUTS") == "" {
		t.Fatal("missing TILLR_GATE_EDGE_INPUTS")
	}
}
func recordPID(t *testing.T, name string) {
	if dir := os.Getenv("SCOPED_PIDS_DIR"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(fmt.Sprintf("%%d", os.Getpid())), 0o644)
	}
}
func TestBuildsSibA1(t *testing.T) {
	recordPID(t, "A1")
	raw, err := os.ReadFile("../tillr-siba/main.go")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(raw), "sibA-") { t.Fatalf("bad sibA: %%q", raw) }
}
func TestBuildsSibA2(t *testing.T) {
	recordPID(t, "A2")
	raw, err := os.ReadFile("../tillr-siba/main.go")
	if err != nil { t.Fatal(err) }
	if strings.Contains(string(raw), "BROKEN_A2") { t.Fatalf("sibA broke A2: %%q", raw) }
}
func TestBuildsSibB(t *testing.T) {
	recordPID(t, "B")
	raw, err := os.ReadFile("../tillr-sibb/main.go")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(raw), "sibB-") { t.Fatalf("bad sibB: %%q", raw) }
}
func TestUnrelatedMustNotRunWhenNarrowed(t *testing.T) {
	recordPID(t, "UNREL")
	if os.Getenv("ALLOW_UNRELATED") != "1" {
		t.Fatal("unrelated test ran during narrowed builds execution")
	}
}
`, nonce)
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-scoped", "scoped_test.go"), []byte(scopedTest), 0o644); err != nil {
		t.Fatal(err)
	}

	edgesData := map[string]goaffected.EdgeSpec{
		pkg: {
			Builds: []string{sibAPkg, sibBPkg},
			BuildTests: map[string][]string{
				sibAPkg: {"TestBuildsSibA1", "TestBuildsSibA2"},
			},
			Reads: []string{},
		},
	}
	edgesBytes, err := json.Marshal(edgesData)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), edgesBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	durationsBytes, err := json.Marshal(map[string]map[string]float64{
		pkg: {
			"TestBuildsSibA1": 20.0,
			"TestBuildsSibA2": 15.0,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "go-test-durations.json"), durationsBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	evFile := filepath.Join(tmpPath, "ev.json")
	durable := filepath.Join(tmpPath, "durable")
	if err := os.MkdirAll(durable, 0o755); err != nil {
		t.Fatal(err)
	}
	gocache := sharedGocache(t)
	precompileGoTest(t, filepath.Join(repo, "tools", "tillr-scoped"), gocache)

	runScoped := func(changed []string, extraEnv map[string]string) (int, string, *goaffected.Evidence) {
		t.Setenv("GOCACHE", gocache)
		t.Setenv("YOLO_DURABLE_DIR", durable)
		t.Setenv("SCOPED_PIDS_DIR", pidsDir)
		for k, v := range extraEnv {
			t.Setenv(k, v)
		}
		sel, serr := goaffected.SelectAffectedPackages(repo, changed, nil, nil, nil)
		if serr != nil {
			t.Fatalf("SelectAffectedPackages(%v): %v", changed, serr)
		}
		rc, out, ev, rerr := goaffected.RunSelectedTests(repo, sel, goaffected.RunOptions{EvidencePath: evFile})
		if rerr != nil {
			t.Fatalf("RunSelectedTests: %v", rerr)
		}
		return rc, out, ev
	}

	// 1. Scoped builds change runs only TestBuildsSibA1 + TestBuildsSibA2 (+ TestEdgeInputs),
	// splits into 2 LPT shard processes, and records "tests" in evidence.
	rc, out, ev := runScoped([]string{"tools/tillr-siba/main.go"}, nil)
	if rc != 0 {
		t.Fatalf("run 1 failed rc=%d:\n%s", rc, out)
	}
	pidA1, err := os.ReadFile(filepath.Join(pidsDir, "A1"))
	if err != nil {
		t.Fatalf("A1 did not run: %v", err)
	}
	pidA2, err := os.ReadFile(filepath.Join(pidsDir, "A2"))
	if err != nil {
		t.Fatalf("A2 did not run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pidsDir, "B")); err == nil {
		t.Fatal("B unexpectedly ran during narrowed sibA run")
	}
	if _, err := os.Stat(filepath.Join(pidsDir, "UNREL")); err == nil {
		t.Fatal("UNREL unexpectedly ran during narrowed sibA run")
	}
	if string(pidA1) == string(pidA2) {
		t.Fatalf("expected 2 LPT shards for 35s builder subset, got same pid %s", string(pidA1))
	}
	if len(ev.Packages) != 1 || ev.Packages[0].Cached {
		t.Fatalf("expected 1 uncached package in evidence, got %+v", ev.Packages)
	}
	if ev.Packages[0].Tests == nil || !reflect.DeepEqual(*ev.Packages[0].Tests, []string{"TestBuildsSibA1", "TestBuildsSibA2"}) {
		t.Fatalf("unexpected evidence tests: %+v", ev.Packages[0].Tests)
	}

	// 2. Second run with unchanged inputs hits Go's result cache across both shards.
	_ = os.Remove(filepath.Join(pidsDir, "A1"))
	_ = os.Remove(filepath.Join(pidsDir, "A2"))
	rc, out, ev = runScoped([]string{"tools/tillr-siba/main.go"}, nil)
	if rc != 0 {
		t.Fatalf("run 2 failed rc=%d:\n%s", rc, out)
	}
	if !ev.Packages[0].Cached {
		t.Fatalf("expected cached=true on second run, got %+v", ev.Packages[0])
	}
	if _, err := os.Stat(filepath.Join(pidsDir, "A1")); err == nil {
		t.Fatal("A1 ran on cached run")
	}
	if _, err := os.Stat(filepath.Join(pidsDir, "A2")); err == nil {
		t.Fatal("A2 ran on cached run")
	}

	// 3. Editing tillr-siba/main.go invalidates the digest and catches TestBuildsSibA2's failure.
	if err := os.WriteFile(sibAMain, []byte("package main\nfunc main() { println(\"sibA-v2 BROKEN_A2\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, ev = runScoped([]string{"tools/tillr-siba/main.go"}, nil)
	if rc == 0 {
		t.Fatalf("expected failure when sibA broke A2, got rc=0:\n%s", out)
	}
	if ev.Packages[0].Cached {
		t.Fatalf("expected cached=false on invalidated run, got %+v", ev.Packages[0])
	}
	if !strings.Contains(out, "TestBuildsSibA2") {
		t.Fatalf("expected TestBuildsSibA2 in failure output:\n%s", out)
	}
	if err := os.WriteFile(sibAMain, []byte("package main\nfunc main() { println(\"sibA-v3\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 4. Subset-scoped edge digest: non-builder shard ignores scoped sib_a edits while
	// tracking unscoped sib_b edits; builder shard tracks both sib_a and unscoped sib_b.
	graph, err := goaffected.LoadTillrGraph(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	edgeEntry := edgesData[pkg]
	dUnrelBefore, err := goaffected.ComputeEdgeDigest(repo, edgeEntry, graph.PkgInfo, []string{"TestUnrelatedMustNotRunWhenNarrowed"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dBuilderBefore, err := goaffected.ComputeEdgeDigest(repo, edgeEntry, graph.PkgInfo, []string{"TestBuildsSibA1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibAMain, []byte("package main\nfunc main() { println(\"sibA-v4\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dUnrelAfterA, err := goaffected.ComputeEdgeDigest(repo, edgeEntry, graph.PkgInfo, []string{"TestUnrelatedMustNotRunWhenNarrowed"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dBuilderAfterA, err := goaffected.ComputeEdgeDigest(repo, edgeEntry, graph.PkgInfo, []string{"TestBuildsSibA1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dUnrelAfterA != dUnrelBefore {
		t.Fatal("non-reader/non-builder shard digest changed on scoped builds edit")
	}
	if dBuilderAfterA == dBuilderBefore {
		t.Fatal("builder shard digest missed scoped builds edit")
	}

	if err := os.WriteFile(sibBMain, []byte("package main\nfunc main() { println(\"sibB-v2\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dUnrelAfterB, err := goaffected.ComputeEdgeDigest(repo, edgeEntry, graph.PkgInfo, []string{"TestUnrelatedMustNotRunWhenNarrowed"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dUnrelAfterB == dUnrelAfterA {
		t.Fatal("non-builder shard digest missed unscoped builds edit")
	}

	// 5. Pairing with an unscoped builds change widens back to whole-package (no Tests field in evidence).
	rc, out, ev = runScoped([]string{"tools/tillr-siba/main.go", "tools/tillr-sibb/main.go"}, map[string]string{
		"ALLOW_UNRELATED": "1",
	})
	if rc != 0 {
		t.Fatalf("widened run failed rc=%d:\n%s", rc, out)
	}
	if _, err := os.Stat(filepath.Join(pidsDir, "B")); err != nil {
		t.Fatal("B did not run on widened run")
	}
	if _, err := os.Stat(filepath.Join(pidsDir, "UNREL")); err != nil {
		t.Fatal("UNREL did not run on widened run")
	}
	if ev.Packages[0].Tests != nil {
		t.Fatalf("expected nil Tests on widened run, got %v", *ev.Packages[0].Tests)
	}
}

func TestCLIHelperFlagsMissingEdgesEdgeDigestAndScanTests(t *testing.T) {
	root := repoRoot(t)
	tmpPath := filepath.Join(scratchRoot(t), "ga-cli-helpers-"+freshNonce(t))
	if err := os.MkdirAll(tmpPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	bin := buildTillrGateBinary(t, filepath.Join(tmpPath, "tillr-gate"))
	env := goaffected.BuildEnv()

	runCLI := func(args ...string) (int, string, string) {
		cmd := exec.Command(bin, append([]string{"go-affected"}, args...)...)
		cmd.Env = env
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		rc := 0
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				rc = exitErr.ExitCode()
			} else {
				rc = 1
			}
		}
		return rc, stdout.String(), stderr.String()
	}

	// 1. --missing-edges passes on the real repository.
	if rc, out, errOut := runCLI("--root", root, "--missing-edges"); rc != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("--missing-edges on repo failed rc=%d out=%q err=%q", rc, out, errOut)
	}

	// 2. --scan-tests returns JSON list of top-level tests in tools/tillr-gate/goaffected.
	rc, out, errOut := runCLI("--root", root, "--scan-tests", "tools/tillr-gate/goaffected")
	if rc != 0 {
		t.Fatalf("--scan-tests failed rc=%d err=%q", rc, errOut)
	}
	var scanned []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &scanned); err != nil {
		t.Fatalf("unmarshal --scan-tests output %q: %v", out, err)
	}
	foundSelf := false
	for _, name := range scanned {
		if name == "TestCLIHelperFlagsMissingEdgesEdgeDigestAndScanTests" {
			foundSelf = true
			break
		}
	}
	if !foundSelf {
		t.Fatalf("--scan-tests output %v missing TestCLIHelperFlagsMissingEdgesEdgeDigestAndScanTests", scanned)
	}

	// 3. --edge-digest computes digests with and without --active-tests (CSV and JSON array).
	const drainPkg = "github.com/mschulkind-oss/tillr-drain/drain"
	rc, dFull, errOut := runCLI("--root", root, "--edge-digest", drainPkg)
	if rc != 0 || len(strings.TrimSpace(dFull)) != 64 {
		t.Fatalf("--edge-digest failed rc=%d out=%q err=%q", rc, dFull, errOut)
	}
	rc, dCSV, errOut := runCLI("--root", root, "--edge-digest", drainPkg, "--active-tests", "TestEdgeInputs,TestNonExistent")
	if rc != 0 || len(strings.TrimSpace(dCSV)) != 64 {
		t.Fatalf("--edge-digest CSV failed rc=%d out=%q err=%q", rc, dCSV, errOut)
	}
	rc, dJSON, errOut := runCLI("--root", root, "--edge-digest", drainPkg, "--active-tests", `["TestEdgeInputs","TestNonExistent"]`)
	if rc != 0 || strings.TrimSpace(dJSON) != strings.TrimSpace(dCSV) {
		t.Fatalf("--edge-digest JSON=%q != CSV=%q (rc=%d err=%q)", dJSON, dCSV, rc, errOut)
	}
	if strings.TrimSpace(dCSV) == strings.TrimSpace(dFull) {
		t.Fatalf("expected scoped --active-tests digest to differ from full digest %q", dFull)
	}

	// 4. --missing-edges fails on a repo with an undeclared Go module.
	fakeRepo := filepath.Join(tmpPath, "fake-repo")
	for _, d := range []string{
		filepath.Join(fakeRepo, "tools", "tillr"),
		filepath.Join(fakeRepo, "tools", "tillr-undeclared"),
		filepath.Join(fakeRepo, "tools", "gate"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(fakeRepo, "tools", "tillr", "go.mod"), []byte("module github.com/mschulkind-oss/tillr\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(fakeRepo, "tools", "tillr-undeclared", "go.mod"), []byte("module github.com/mschulkind-oss/tillr-undeclared\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(fakeRepo, "tools", "gate", "go-test-edges.json"), []byte(`{
  "github.com/mschulkind-oss/tillr/definition": {"builds": [], "reads": ["tools/tillr/"]},
  "github.com/mschulkind-oss/tillr/store": {"builds": [], "reads": []}
}`), 0o644)
	rc, out, _ = runCLI("--root", fakeRepo, "--missing-edges")
	if rc == 0 || !strings.Contains(out, "tools/tillr-undeclared/") {
		t.Fatalf("expected --missing-edges failure mentioning tools/tillr-undeclared/, got rc=%d out=%q", rc, out)
	}
}

func TestSuiteGoCacheAndRunnerHelpers(t *testing.T) {
	tmpPath := filepath.Join(scratchRoot(t), "ga-helper-"+freshNonce(t))
	if err := os.MkdirAll(tmpPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	writableCache := filepath.Join(tmpPath, "writable-gocache")
	if err := os.MkdirAll(writableCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := goaffected.SharedCacheDir(map[string]string{"GOCACHE": writableCache}); got != writableCache {
		t.Fatalf("expected writable GOCACHE reuse %q, got %q", writableCache, got)
	}
	if got := goaffected.SharedCacheDir(map[string]string{"GOCACHE": "off"}); got == "" || got == "off" {
		t.Fatalf("expected non-empty fallback for GOCACHE=off, got %q", got)
	}

	pkgDir := filepath.Join(tmpPath, "probe")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nonce := freshNonce(t)
	if err := os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module example.com/precompile-probe\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "probe_test.go"), []byte(fmt.Sprintf("package probe\nimport \"testing\"\nfunc TestProbe(t *testing.T) { if t.Name() == %q { t.Fatal() } }\n", nonce)), 0o644); err != nil {
		t.Fatal(err)
	}
	gocache := sharedGocache(t)
	precompileGoTest(t, pkgDir, gocache)

	checkCmd := exec.Command("go", "test", "-x", "-c", "-o", os.DevNull, ".")
	checkCmd.Dir = pkgDir
	checkCmd.Env = withEnvOverrides(goaffected.BuildEnv(), map[string]string{"GOCACHE": gocache})
	checkOut, err := checkCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test -x -c failed: %v\n%s", err, checkOut)
	}
	for _, ln := range strings.Split(string(checkOut), "\n") {
		if strings.Contains(ln, "/compile ") || strings.HasPrefix(ln, "compile ") {
			t.Fatalf("precompileGoTest missed BuildEnv() cache:\n%s", checkOut)
		}
	}

	// ScanPackageTests matches Test*, Example*, Fuzz* (excluding TestMain and lowercase suffixes).
	scanDir := filepath.Join(tmpPath, "scanpkg")
	if err := os.MkdirAll(scanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scanDir, "scan_test.go"), []byte(`package scanpkg
import "testing"
func TestOne(t *testing.T) {}
func Testhelper(t *testing.T) {}
func TestMain(m *testing.M) {}
func Example() {}
func ExampleFoo() {}
func FuzzBar(f *testing.F) {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	scanned, err := goaffected.ScanPackageTests(tmpPath, "scanpkg", []string{"scan_test.go"})
	if err != nil {
		t.Fatalf("ScanPackageTests: %v", err)
	}
	wantScanned := []string{"TestOne", "Example", "ExampleFoo", "FuzzBar"}
	if !reflect.DeepEqual(scanned, wantScanned) {
		t.Fatalf("ScanPackageTests = %v, want %v", scanned, wantScanned)
	}

	// Non-edge < 100-test packages use per-package scratch_key = import_path (not module:<mod>).
	fakeRoot := filepath.Join(tmpPath, "perpkg-root")
	for _, d := range []string{
		filepath.Join(fakeRoot, "tools", "tillr-x", "a"),
		filepath.Join(fakeRoot, "tools", "tillr-x", "b"),
		filepath.Join(fakeRoot, "tools", "gate"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(fakeRoot, "tools", "gate", "go-test-edges.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeRoot, "tools", "tillr-x", "go.mod"), []byte("module example.com/tx\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scratchLog := filepath.Join(tmpPath, "scratch-log.txt")
	for _, sub := range []string{"a", "b"} {
		body := fmt.Sprintf(`package %s
import (
	"os"
	"testing"
)
func Test%s(t *testing.T) {
	f, err := os.OpenFile(os.Getenv("SCRATCH_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil { t.Fatal(err) }
	_, _ = f.WriteString(os.Getenv("YOLO_DURABLE_DIR") + "\n")
	_ = f.Close()
}
`, sub, strings.ToUpper(sub))
		if err := os.WriteFile(filepath.Join(fakeRoot, "tools", "tillr-x", sub, sub+"_test.go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	durable := filepath.Join(tmpPath, "durable-perpkg")
	if err := os.MkdirAll(durable, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCACHE", gocache)
	t.Setenv("YOLO_DURABLE_DIR", durable)
	t.Setenv("SCRATCH_LOG", scratchLog)

	sel := &goaffected.Selection{
		SelectionMs: 1,
		Modules:     []string{"tools/tillr-x"},
		Selected: []goaffected.SelectedPackage{
			{
				ImportPath: "example.com/tx/a",
				Module:     "tools/tillr-x",
				DirRel:     "tools/tillr-x/a",
				TestFiles:  []string{"a_test.go"},
				Reason:     "changed",
			},
			{
				ImportPath: "example.com/tx/b",
				Module:     "tools/tillr-x",
				DirRel:     "tools/tillr-x/b",
				TestFiles:  []string{"b_test.go"},
				Reason:     "changed",
			},
		},
	}
	rcSel, outSel, _, err := goaffected.RunSelectedTests(fakeRoot, sel, goaffected.RunOptions{DisableProgress: true})
	if err != nil || rcSel != 0 {
		t.Fatalf("RunSelectedTests failed rc=%d err=%v\n%s", rcSel, err, outSel)
	}
	rawLog, err := os.ReadFile(scratchLog)
	if err != nil {
		t.Fatal(err)
	}
	slugOf := func(key string) string {
		sum := sha256.Sum256([]byte(key))
		return hex.EncodeToString(sum[:])[:12]
	}
	logStr := string(rawLog)
	if !strings.Contains(logStr, slugOf("example.com/tx/a")) || !strings.Contains(logStr, slugOf("example.com/tx/b")) {
		t.Fatalf("expected per-package scratch slugs in %q", logStr)
	}
}

func TestPrivateGitconfigDisablesAutoMaintenanceAndBypassesBlockGitShim(t *testing.T) {
	realGit, _ := goaffected.ResolveRealGit(os.Getenv("PATH"))
	if realGit == "" {
		t.Fatal("could not find real git binary on PATH")
	}

	tmpPath := filepath.Join(scratchRoot(t), "ga-gitshim-"+freshNonce(t))
	if err := os.MkdirAll(tmpPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	// 1. Direct WritePrivateGitconfig check: overrides inherited maintenance.auto / gc.auto while keeping owner keys.
	directScratch := filepath.Join(tmpPath, "direct-scratch")
	if err := os.MkdirAll(directScratch, 0o755); err != nil {
		t.Fatal(err)
	}
	directCfg, err := goaffected.WritePrivateGitconfig(directScratch, [][2]string{
		{"user.name", "Inherited Owner"},
		{"maintenance.auto", "true"},
		{"gc.auto", "6700"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gitCfgGetAll := func(key string) string {
		out, err := exec.Command(realGit, "config", "--file", directCfg, "--get-all", key).Output()
		if err != nil {
			t.Fatalf("git config --get-all %s: %v", key, err)
		}
		return strings.TrimSpace(string(out))
	}
	if got := gitCfgGetAll("maintenance.auto"); got != "false" {
		t.Fatalf("maintenance.auto = %q, want false", got)
	}
	if got := gitCfgGetAll("gc.auto"); got != "0" {
		t.Fatalf("gc.auto = %q, want 0", got)
	}
	if got := gitCfgGetAll("user.name"); got != "Inherited Owner" {
		t.Fatalf("user.name = %q, want Inherited Owner", got)
	}

	// 2. End-to-end isolated scratch test run with block/git and tillr-git-guard ahead on PATH.
	shimHits := filepath.Join(tmpPath, "shim-hits.log")
	fakeBlock := filepath.Join(tmpPath, ".yolo", "bin", "block")
	if err := os.MkdirAll(fakeBlock, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeBlockGit := filepath.Join(fakeBlock, "git")
	blockScript := fmt.Sprintf(`#!/bin/sh
# A guard: runs the real git unless an argument names this workspace's .yolo.
if [ -z "$YOLO_BYPASS_SHIMS" ]; then
  _yg_tool=git
  printf "block-hit\n" >> %q
  exit 97
fi
exec %q "$@"
`, shimHits, realGit)
	if err := os.WriteFile(fakeBlockGit, []byte(blockScript), 0o755); err != nil {
		t.Fatal(err)
	}

	fakeGuardDir := filepath.Join(tmpPath, "git-guard")
	if err := os.MkdirAll(fakeGuardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGuardGit := filepath.Join(fakeGuardDir, "git")
	guardScript := fmt.Sprintf(`#!/bin/sh
# tillr-git-guard
printf "guard-hit\n" >> %q
exit 98
`, shimHits)
	if err := os.WriteFile(fakeGuardGit, []byte(guardScript), 0o755); err != nil {
		t.Fatal(err)
	}

	fallbackEnv := goaffected.BypassGitShimEnv(
		map[string]string{"PATH": fakeGuardDir + string(os.PathListSeparator) + fakeBlock},
		"/proc/unwritable-git-bin",
	)
	if fallbackEnv["YOLO_BYPASS_SHIMS"] != "1" {
		t.Fatalf("expected YOLO_BYPASS_SHIMS=1, got %v", fallbackEnv)
	}
	for _, entry := range strings.Split(fallbackEnv["PATH"], string(os.PathListSeparator)) {
		if entry == fakeGuardDir {
			t.Fatalf("expected fakeGuardDir stripped from PATH: %v", fallbackEnv)
		}
	}

	inheritedCfg := filepath.Join(tmpPath, "inherited.gitconfig")
	inheritedText := "[user]\n\tname = Inherited Owner\n\temail = owner@example.invalid\n[maintenance]\n\tauto = true\n[gc]\n\tauto = 6700\n"
	if err := os.WriteFile(inheritedCfg, []byte(inheritedText), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := filepath.Join(tmpPath, "repo")
	mod := filepath.Join(repo, "tools", "tillr-gitshim")
	for _, d := range []string{mod, filepath.Join(repo, "tools", "gate")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nonce := freshNonce(t)
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module github.com/mschulkind-oss/tillr-gitshim\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitshimTest := fmt.Sprintf(`package main
import (
	"os/exec"
	"strings"
	"testing"
)
func TestGitShimBypassAndAutoMaintenanceOff(t *testing.T) {
	if t.Name() == %q { t.Fatal() }
	resolved, err := exec.LookPath("git")
	if err != nil { t.Fatal(err) }
	if resolved == %q || resolved == %q {
		t.Fatalf("git resolved to shell shim: %%s", resolved)
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %%v: %%s", err, out)
	}
	for _, check := range [][2]string{
		{"maintenance.auto", "false"},
		{"gc.auto", "0"},
		{"user.name", "Inherited Owner"},
	} {
		out, err := exec.Command("git", "-C", dir, "config", "--get-all", check[0]).CombinedOutput()
		if err != nil { t.Fatalf("git config %%s: %%v: %%s", check[0], err, out) }
		got := strings.TrimSpace(string(out))
		if got != check[1] { t.Fatalf("%%s = %%q, want %%q", check[0], got, check[1]) }
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-qm", "ok").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %%v: %%s", err, out)
	}
}
`, nonce, fakeBlockGit, fakeGuardGit)
	if err := os.WriteFile(filepath.Join(mod, "gitshim_test.go"), []byte(gitshimTest), 0o644); err != nil {
		t.Fatal(err)
	}

	durable := filepath.Join(tmpPath, "durable")
	if err := os.MkdirAll(durable, 0o755); err != nil {
		t.Fatal(err)
	}
	evFile := filepath.Join(tmpPath, "ev.json")
	gocache := sharedGocache(t)
	precompileGoTest(t, mod, gocache)

	tillrGateBin := buildTillrGateBinary(t, filepath.Join(t.TempDir(), "tillr-gate"))
	origPATH := os.Getenv("PATH")
	workerPath := strings.Join([]string{fakeGuardDir, fakeBlock, origPATH}, string(os.PathListSeparator))
	gatePath := strings.Join([]string{fakeBlock, origPATH}, string(os.PathListSeparator))

	env1 := withEnvOverrides(os.Environ(), map[string]string{
		"PATH":              workerPath,
		"GIT_CONFIG_GLOBAL": inheritedCfg,
		"YOLO_DURABLE_DIR":  durable,
		"GOCACHE":           gocache,
		"YOLO_BYPASS_SHIMS": "",
	})
	args := []string{"go-affected", "--root", repo, "--changed", "tools/tillr-gitshim/gitshim_test.go", "--evidence-out", evFile}

	cmd1 := exec.Command(tillrGateBin, args...)
	cmd1.Env = env1
	if out, err := cmd1.CombinedOutput(); err != nil {
		t.Fatalf("run 1 failed: %v\n%s", err, out)
	}
	if raw, err := os.ReadFile(shimHits); err == nil {
		t.Fatalf("shim was hit on run 1: %s", raw)
	}
	if raw, err := os.ReadFile(inheritedCfg); err != nil || string(raw) != inheritedText {
		t.Fatalf("inheritedCfg mutated: err=%v raw=%q", err, raw)
	}
	var ev1 goaffected.Evidence
	ev1Raw, _ := os.ReadFile(evFile)
	_ = json.Unmarshal(ev1Raw, &ev1)
	if len(ev1.Packages) != 1 || ev1.Packages[0].Cached {
		t.Fatalf("expected uncached run 1, got %+v", ev1)
	}

	env2 := withEnvOverrides(env1, map[string]string{"PATH": gatePath})
	cmd2 := exec.Command(tillrGateBin, args...)
	cmd2.Env = env2
	if out, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("run 2 failed: %v\n%s", err, out)
	}
	if raw, err := os.ReadFile(shimHits); err == nil {
		t.Fatalf("shim was hit on run 2: %s", raw)
	}
	var ev2 goaffected.Evidence
	ev2Raw, _ := os.ReadFile(evFile)
	_ = json.Unmarshal(ev2Raw, &ev2)
	if len(ev2.Packages) != 1 || !ev2.Packages[0].Cached {
		t.Fatalf("expected cached run 2, got %+v", ev2)
	}
}

func TestGlobalGitConfigAndWritePrivateGitconfigBypassParentPathShim(t *testing.T) {
	tmp := t.TempDir()
	shimDir := filepath.Join(tmp, "shim-bin")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shimHits := filepath.Join(tmp, "shim-hits.txt")
	shimScript := fmt.Sprintf("#!/bin/sh\n# tillr-git-guard\necho \"$0 $*\" >> %q\nexit 97\n", shimHits)
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shimScript), 0o755); err != nil {
		t.Fatal(err)
	}

	// Put the refusing shim first on the parent process PATH as well as the child env PATH.
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfgPath := filepath.Join(tmp, "global.gitconfig")
	if err := os.WriteFile(cfgPath, []byte("[user]\n\tname = Shim Test Owner\n\temail = shim@example.invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(tmp, "gocache")
	scratchDir := filepath.Join(tmp, "scratch")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		t.Fatal(err)
	}

	env := withEnvOverrides(os.Environ(), map[string]string{
		"GIT_CONFIG_GLOBAL": cfgPath,
		"GOCACHE":           cacheDir,
		"YOLO_BYPASS_SHIMS": "",
	})

	vals, err := goaffected.GlobalGitConfig(env)
	if err != nil {
		t.Fatalf("GlobalGitConfig failed with refusing shim on parent PATH: %v", err)
	}
	var sawUser bool
	for _, kv := range vals {
		if kv[0] == "user.name" && kv[1] == "Shim Test Owner" {
			sawUser = true
		}
	}
	if !sawUser {
		t.Fatalf("expected user.name in GlobalGitConfig output, got %+v", vals)
	}

	outCfg, err := goaffected.WritePrivateGitconfig(scratchDir, vals, env)
	if err != nil {
		t.Fatalf("WritePrivateGitconfig failed with refusing shim on parent PATH: %v", err)
	}
	rawCfg, err := os.ReadFile(outCfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawCfg), "Shim Test Owner") {
		t.Fatalf("expected private gitconfig to preserve user.name, got:\n%s", rawCfg)
	}
	if hits, err := os.ReadFile(shimHits); err == nil {
		t.Fatalf("refusing git shim was invoked: %s", hits)
	}
}

func TestGoTmpdirInsideSlotScratchAndCleanedOnSignal(t *testing.T) {
	tillrGateBin := buildTillrGateBinary(t, filepath.Join(t.TempDir(), "tillr-gate"))

	tmpPath := filepath.Join(scratchRoot(t), "ga-gotmpdir-"+freshNonce(t))
	repo := filepath.Join(tmpPath, "repo")
	pkgA := filepath.Join(repo, "tools", "tillr-gotmp", "pkga")
	pkgB := filepath.Join(repo, "tools", "tillr-gotmp", "pkgb")
	for _, d := range []string{filepath.Join(repo, "tools", "gate"), pkgA, pkgB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-gotmp", "go.mod"), []byte("module github.com/mschulkind-oss/tillr-gotmp\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	nonce := freshNonce(t)
	writePkgTest := func(dir, pkg, file string) {
		body := fmt.Sprintf(`package %s
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)
func TestSlotGoTmpDir(t *testing.T) {
	if t.Name() == %q { t.Fatal() }
	td := t.TempDir()
	exe, err := os.Executable()
	if err != nil { t.Fatal(err) }
	rec := map[string]any{
		"pkg":         %q,
		"gotmpdir":    os.Getenv("GOTMPDIR"),
		"durable":     os.Getenv("YOLO_DURABLE_DIR"),
		"tempdir":     td,
		"exe":         exe,
		"go_test_pid": os.Getppid(),
	}
	if outDir := os.Getenv("GOTMP_REC_DIR"); outDir != "" {
		raw, err := json.Marshal(rec)
		if err != nil { t.Fatal(err) }
		dest := filepath.Join(outDir, "%s.json")
		tmpFile := dest + ".tmp"
		if err := os.WriteFile(tmpFile, raw, 0o644); err != nil { t.Fatal(err) }
		if err := os.Rename(tmpFile, dest); err != nil { t.Fatal(err) }
	}
	if os.Getenv("GOTMP_BLOCK") == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}
}
`, pkg, nonce, pkg, pkg)
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writePkgTest(pkgA, "pkga", "a_test.go")
	writePkgTest(pkgB, "pkgb", "b_test.go")

	durable := filepath.Join(tmpPath, "durable")
	if err := os.MkdirAll(durable, 0o755); err != nil {
		t.Fatal(err)
	}
	gocache := sharedGocache(t)
	precompileGoTest(t, pkgA, gocache)
	precompileGoTest(t, pkgB, gocache)

	baseEnv := withEnvOverrides(os.Environ(), map[string]string{
		"TMPDIR":           "",
		"GOTMPDIR":         "",
		"YOLO_DURABLE_DIR": durable,
		"GOCACHE":          gocache,
	})

	cleanupLeakedTmp := func(goBuildDir string) {
		if goBuildDir != "" && filepath.Dir(goBuildDir) == "/tmp" {
			_ = os.RemoveAll(goBuildDir)
		}
	}
	isSubpath := func(child, parent string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".."
	}

	// 1. Multi-package invocation check.
	recDir := filepath.Join(tmpPath, "rec-multi")
	if err := os.MkdirAll(recDir, 0o755); err != nil {
		t.Fatal(err)
	}
	evFile := filepath.Join(tmpPath, "multi-ev.json")
	bothArgs := []string{
		"go-affected",
		"--root", repo,
		"--changed", "tools/tillr-gotmp/pkga/a_test.go", "tools/tillr-gotmp/pkgb/b_test.go",
		"--evidence-out", evFile,
	}
	cmd1 := exec.Command(tillrGateBin, bothArgs...)
	cmd1.Env = withEnvOverrides(baseEnv, map[string]string{
		"GOTMPDIR":      "/tmp",
		"GOTMP_REC_DIR": recDir,
	})
	if out, err := cmd1.CombinedOutput(); err != nil {
		t.Fatalf("multi run 1 failed: %v\n%s", err, out)
	}
	seenGoTmpDirs := make(map[string]bool)
	for _, pkgName := range []string{"pkga", "pkgb"} {
		raw, err := os.ReadFile(filepath.Join(recDir, pkgName+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var rec struct {
			Pkg       string `json:"pkg"`
			GoTmpDir  string `json:"gotmpdir"`
			Durable   string `json:"durable"`
			TempDir   string `json:"tempdir"`
			Exe       string `json:"exe"`
			GoTestPID int    `json:"go_test_pid"`
		}
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatal(err)
		}
		goBuildDir := filepath.Dir(filepath.Dir(rec.Exe))
		cleanupLeakedTmp(goBuildDir)
		if rec.GoTmpDir == "" || !isSubpath(rec.GoTmpDir, rec.Durable) {
			t.Fatalf("%s: GOTMPDIR %q is not strictly inside slot scratch %q", pkgName, rec.GoTmpDir, rec.Durable)
		}
		if !isSubpath(rec.TempDir, rec.GoTmpDir) {
			t.Fatalf("%s: tempdir %q not inside GOTMPDIR %q", pkgName, rec.TempDir, rec.GoTmpDir)
		}
		if !strings.HasPrefix(filepath.Base(goBuildDir), "go-build") || filepath.Dir(goBuildDir) != rec.GoTmpDir {
			t.Fatalf("%s: unexpected goBuildDir %q vs GOTMPDIR %q", pkgName, goBuildDir, rec.GoTmpDir)
		}
		if _, err := os.Stat(filepath.Join("/tmp", filepath.Base(goBuildDir))); err == nil {
			t.Fatalf("%s: go-build dir leaked into /tmp: %s", pkgName, goBuildDir)
		}
		if _, err := os.Stat(rec.GoTmpDir); err == nil {
			t.Fatalf("%s: slot scratch %s was not cleared when slot ended", pkgName, rec.GoTmpDir)
		}
		seenGoTmpDirs[rec.GoTmpDir] = true
	}
	if len(seenGoTmpDirs) != 2 {
		t.Fatalf("expected 2 distinct GOTMPDIRs, got %v", seenGoTmpDirs)
	}

	cmd2 := exec.Command(tillrGateBin, bothArgs...)
	cmd2.Env = withEnvOverrides(baseEnv, map[string]string{"GOTMP_REC_DIR": recDir})
	if out, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("multi run 2 failed: %v\n%s", err, out)
	}
	evRaw, _ := os.ReadFile(evFile)
	var ev2 goaffected.Evidence
	_ = json.Unmarshal(evRaw, &ev2)
	for _, p := range ev2.Packages {
		if !p.Cached {
			t.Fatalf("expected cached=true for all packages on run 2, got %+v", ev2)
		}
	}

	// 2. Signal checks: SIGTERM to runner, SIGKILL to go test, SIGKILL to runner.
	oneArgs := []string{"go-affected", "--root", repo, "--changed", "tools/tillr-gotmp/pkga/a_test.go"}
	type sigSlot struct {
		tag  string
		cmd  *exec.Cmd
		done chan error
		out  *strings.Builder
	}
	startBlocking := func(tag string) sigSlot {
		sRecDir := filepath.Join(tmpPath, "rec-"+tag)
		sDurable := filepath.Join(tmpPath, "durable-"+tag)
		for _, d := range []string{sRecDir, sDurable} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		c := exec.Command(tillrGateBin, oneArgs...)
		c.Env = withEnvOverrides(baseEnv, map[string]string{
			"YOLO_DURABLE_DIR": sDurable,
			"GOTMP_REC_DIR":    sRecDir,
			"GOTMP_BLOCK":      "1",
		})
		var buf strings.Builder
		c.Stdout = &buf
		c.Stderr = &buf
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- c.Wait() }()
		return sigSlot{tag: tag, cmd: c, done: done, out: &buf}
	}
	type slotRec struct {
		GoTmpDir  string `json:"gotmpdir"`
		Exe       string `json:"exe"`
		GoTestPID int    `json:"go_test_pid"`
	}
	waitReady := func(s sigSlot) (slotRec, string) {
		recFile := filepath.Join(tmpPath, "rec-"+s.tag, "pkga.json")
		for {
			if raw, err := os.ReadFile(recFile); err == nil {
				var r slotRec
				if json.Unmarshal(raw, &r) == nil && r.Exe != "" {
					return r, filepath.Dir(filepath.Dir(r.Exe))
				}
			}
			select {
			case err := <-s.done:
				t.Fatalf("tillr-gate (%s) exited early: %v\n%s", s.tag, err, s.out.String())
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	waitPIDGone := func(pid int) {
		for {
			if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	sTerm := startBlocking("sigterm-runner")
	sChildKill := startBlocking("sigkill-gotest")
	sKill := startBlocking("sigkill-runner")
	var bTerm, bChild, bKill string
	defer func() {
		for _, s := range []sigSlot{sTerm, sChildKill, sKill} {
			if s.cmd.Process != nil {
				_ = s.cmd.Process.Kill()
			}
		}
		cleanupLeakedTmp(bTerm)
		cleanupLeakedTmp(bChild)
		cleanupLeakedTmp(bKill)
	}()

	rTerm, bTerm := waitReady(sTerm)
	rChild, bChild := waitReady(sChildKill)
	rKill, bKill := waitReady(sKill)

	// 2a. SIGTERM to tillr-gate while go test is active.
	_ = sTerm.cmd.Process.Signal(syscall.SIGTERM)
	if err := <-sTerm.done; err == nil {
		t.Fatalf("expected non-zero exit after SIGTERM:\n%s", sTerm.out.String())
	}
	waitPIDGone(rTerm.GoTestPID)
	if !strings.HasPrefix(filepath.Base(bTerm), "go-build") || filepath.Dir(bTerm) == "/tmp" {
		t.Fatalf("unexpected bTerm: %s", bTerm)
	}
	if _, err := os.Stat(filepath.Join("/tmp", filepath.Base(bTerm))); err == nil {
		t.Fatalf("leaked to /tmp: %s", bTerm)
	}

	// 2b. SIGKILL directly to the slot's go test process.
	_ = syscall.Kill(rChild.GoTestPID, syscall.SIGKILL)
	if err := <-sChildKill.done; err == nil {
		t.Fatalf("expected non-zero exit after child SIGKILL:\n%s", sChildKill.out.String())
	}
	if !strings.HasPrefix(filepath.Base(bChild), "go-build") || filepath.Dir(bChild) == "/tmp" {
		t.Fatalf("unexpected bChild: %s", bChild)
	}
	if _, err := os.Stat(filepath.Join("/tmp", filepath.Base(bChild))); err == nil {
		t.Fatalf("leaked to /tmp: %s", bChild)
	}
	if _, err := os.Stat(bChild); err == nil {
		t.Fatalf("go-build dir survived slot end: %s", bChild)
	}
	if _, err := os.Stat(rChild.GoTmpDir); err == nil {
		t.Fatalf("GOTMPDIR survived slot end: %s", rChild.GoTmpDir)
	}

	// 2c. SIGKILL to tillr-gate (lifeline subreaper ends go test by SIGKILL).
	_ = sKill.cmd.Process.Kill()
	<-sKill.done
	waitPIDGone(rKill.GoTestPID)
	if !strings.HasPrefix(filepath.Base(bKill), "go-build") || filepath.Dir(bKill) == "/tmp" {
		t.Fatalf("unexpected bKill: %s", bKill)
	}
	if _, err := os.Stat(filepath.Join("/tmp", filepath.Base(bKill))); err == nil {
		t.Fatalf("leaked to /tmp: %s", bKill)
	}
}

func TestSingleTestShardSetsGOMAXPROCSAndP1(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	mod := filepath.Join(repo, "tools", "example")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/example\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "a_test.go"), []byte("package example\nimport \"testing\"\nfunc TestOne(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "tools", "gate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	captureFile := filepath.Join(tmp, "capture.txt")
	goWrapper := filepath.Join(binDir, "go")
	wrapperScript := fmt.Sprintf(`#!/bin/sh
printf 'GOMAXPROCS=%%s\n' "${GOMAXPROCS:-}" > %q
for arg in "$@"; do
  printf 'ARG=%%s\n' "$arg" >> %q
done
printf '{"Action":"pass","Package":"example.com/example","Test":"TestOne"}\n'
printf '{"Action":"pass","Package":"example.com/example"}\n'
`, captureFile, captureFile)
	if err := os.WriteFile(goWrapper, []byte(wrapperScript), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("YOLO_DURABLE_DIR", filepath.Join(tmp, "durable"))

	sel := &goaffected.Selection{
		Selected: []goaffected.SelectedPackage{
			{
				Module:        "tools/example",
				ImportPath:    "example.com/example",
				DirRel:        "tools/example",
				Reason:        "direct",
				SelectedTests: []string{"TestOne"},
			},
		},
	}

	rc, out, _, err := goaffected.RunSelectedTests(repo, sel, goaffected.RunOptions{DisableProgress: true})
	if err != nil {
		t.Fatalf("RunSelectedTests: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d, want 0:\n%s", rc, out)
	}

	capturedBytes, err := os.ReadFile(captureFile)
	if err != nil {
		t.Fatalf("read capture file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(capturedBytes)), "\n")
	if len(lines) == 0 || lines[0] != "GOMAXPROCS=1" {
		t.Fatalf("expected GOMAXPROCS=1, got %q", string(capturedBytes))
	}
	var args []string
	for _, line := range lines[1:] {
		args = append(args, strings.TrimPrefix(line, "ARG="))
	}
	hasFlagPair := func(flag, val string) bool {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == flag && args[i+1] == val {
				return true
			}
		}
		return false
	}
	if !hasFlagPair("-parallel", "1") {
		t.Fatalf("expected -parallel 1 in args %v", args)
	}
	if !hasFlagPair("-p", "1") {
		t.Fatalf("expected -p 1 in args %v", args)
	}
}

func TestSigquitExtractsBlockedTestsFirstInBoundedTriage(t *testing.T) {
	pkg := "github.com/mschulkind-oss/tillr-drain/drain"
	assertion := "blocked: the test binary was ended by SIGQUIT while this test was running"

	// Without SIGQUIT: quit, stack-like lines produce no SIGQUIT cases.
	noSigquit := fmt.Sprintf("%s.TestNotBlocked(0xc000123456)\n\t/workspace/tools/tillr-drain/drain/not_blocked_test.go:10 +0x10\n", pkg)
	if got := goaffected.ExtractSigquitCases(noSigquit, nil); len(got) != 0 {
		t.Fatalf("expected no SIGQUIT cases, got %+v", got)
	}

	// Reconstructed in event order from go test -json output chunks and plain lines,
	// excluding TestMain, closures, lowercase Test* helpers, methods, and `created by`.
	sigquitEvents := []map[string]any{
		{"Action": "run", "Package": pkg, "Test": "TestOrdinaryFail"},
		{"Action": "output", "Package": pkg, "Test": "TestOrdinaryFail", "Output": "    ordinary_test.go:22: boom\n"},
		{"Action": "output", "Package": pkg, "Test": "TestOrdinaryFail", "Output": "--- FAIL: TestOrdinaryFail (0.01s)\n"},
		{"Action": "fail", "Package": pkg, "Test": "TestOrdinaryFail", "Elapsed": 0.01},
		{"Action": "run", "Package": pkg, "Test": "TestWorkflowExitRecordRestart"},
		{"Action": "output", "Package": pkg, "Test": "TestWorkflowExitRecordRestart", "Output": "SIGQUIT: quit\n"},
		{"Action": "output", "Package": pkg, "Test": "TestWorkflowExitRecordRestart", "Output": "goroutine 42 [chan receive, 15 minutes]:\n"},
		{"Action": "output", "Package": pkg, "Test": "TestWorkflowExitRecordRestart", "Output": pkg + ".TestWorkflowExitRecordRestart(0xc000123456)\n"},
		{"Action": "output", "Package": pkg, "Test": "TestWorkflowExitRecordRestart", "Output": "\t/workspace/tools/tillr-drain/drain/workflow_exit_record_test.go:123 +0x45\n"},
		{"Action": "output", "Package": pkg, "Output": "goroutine 43 [chan receive]:\n"},
		{"Action": "output", "Package": pkg, "Output": pkg + ".TestWorkflowExitRecordRestart.func1(0xc000123456)\n"},
		{"Action": "output", "Package": pkg, "Output": "\t/workspace/tools/tillr-drain/drain/workflow_exit_record_test.go:99 +0x12\n"},
		{"Action": "output", "Package": pkg, "Output": pkg + ".TestWorkflowExitRecordRestart(0xc000123456)\n"},
		{"Action": "output", "Package": pkg, "Output": "\t/workspace/tools/tillr-drain/drain/duplicate_test.go:1 +0x1\n"},
		{"Action": "output", "Package": pkg, "Output": pkg + ".TestMain(0xc000123456)\n"},
		{"Action": "output", "Package": pkg, "Output": "\t/workspace/tools/tillr-drain/drain/main_test.go:15 +0x20\n"},
		{"Action": "output", "Package": pkg, "Output": pkg + ".Testhelper(0xc000123456)\n"},
		{"Action": "output", "Package": pkg, "Output": "\t/workspace/tools/tillr-drain/drain/helper_test.go:15 +0x20\n"},
		{"Action": "output", "Package": pkg, "Output": pkg + ".(*runner).TestMethod(0xc000123456)\n"},
		{"Action": "output", "Package": pkg, "Output": "\t/workspace/tools/tillr-drain/drain/method_test.go:15 +0x20\n"},
		{"Action": "output", "Package": pkg, "Output": "created by " + pkg + ".TestCreatedBy in goroutine 1\n"},
		{"Action": "output", "Package": pkg, "Output": "\t/workspace/tools/tillr-drain/drain/created_test.go:15 +0x20\n"},
		{"Action": "output", "Package": pkg, "Output": pkg + "_test.TestExternalBlocked(0xc000123456)\n"},
		{"Action": "output", "Package": pkg, "Output": "\t/workspace/tools/tillr-drain/drain/ext_test.go:55 +0x12\n"},
		{"Action": "fail", "Package": pkg, "Elapsed": 900.0},
	}
	var rawSigquitBuf strings.Builder
	for _, ev := range sigquitEvents {
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		rawSigquitBuf.Write(b)
		rawSigquitBuf.WriteByte('\n')
	}
	rawSigquit := rawSigquitBuf.String()

	cases := goaffected.ExtractSigquitCases(rawSigquit, nil)
	wantCases := []goaffected.TriageCase{
		{
			Package:   pkg,
			Name:      "TestWorkflowExitRecordRestart",
			Where:     "/workspace/tools/tillr-drain/drain/workflow_exit_record_test.go:123 +0x45",
			Assertion: assertion,
			Sigquit:   true,
		},
		{
			Package:   pkg,
			Name:      "TestExternalBlocked",
			Where:     "/workspace/tools/tillr-drain/drain/ext_test.go:55 +0x12",
			Assertion: assertion,
			Sigquit:   true,
		},
	}
	if !reflect.DeepEqual(cases, wantCases) {
		t.Fatalf("ExtractSigquitCases mismatch:\ngot:  %+v\nwant: %+v", cases, wantCases)
	}

	// Frame at EOF with no following line keeps empty where (rendered as "(no file named)").
	eofSigquit := fmt.Sprintf("SIGQUIT: quit\n%s.TestNoFollowingLine(0xc000123456)\n", pkg)
	eofCases := goaffected.ExtractSigquitCases(eofSigquit, nil)
	wantEofCases := []goaffected.TriageCase{
		{
			Package:   pkg,
			Name:      "TestNoFollowingLine",
			Where:     "",
			Assertion: assertion,
			Sigquit:   true,
		},
	}
	if !reflect.DeepEqual(eofCases, wantEofCases) {
		t.Fatalf("EOF ExtractSigquitCases mismatch:\ngot:  %+v\nwant: %+v", eofCases, wantEofCases)
	}
	eofTriage := goaffected.FormatTriageBlock(eofCases, nil)
	if !strings.Contains(eofTriage, "       case: TestNoFollowingLine -- (no file named)\n") {
		t.Fatalf("expected (no file named) in triage block, got:\n%s", eofTriage)
	}

	// ParseGoTestJSON puts SIGQUIT tests first in FailedTests.
	parsed, _ := goaffected.ParseGoTestJSON(rawSigquit)
	wantFailed := []string{
		"TestWorkflowExitRecordRestart",
		"TestExternalBlocked",
		"TestOrdinaryFail",
	}
	if !reflect.DeepEqual(parsed[pkg].FailedTests, wantFailed) {
		t.Fatalf("FailedTests mismatch:\ngot:  %v\nwant: %v", parsed[pkg].FailedTests, wantFailed)
	}

	// Bounded triage in RunSelectedTests: SIGQUIT cases come first ahead of 12 ordinary failures,
	// keeping 10 total and noting the dropped count, right before FormatFailureSummary.
	tmpPath := filepath.Join(scratchRoot(t), "ga-sigquit-triage-"+freshNonce(t))
	defer os.RemoveAll(tmpPath)
	repo := filepath.Join(tmpPath, "repo")
	modDir := filepath.Join(repo, "tools", "tillr-drain", "drain")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "tools", "gate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	sel := &goaffected.Selection{
		SelectionMs: 5,
		Selected: []goaffected.SelectedPackage{
			{
				Module:     "tools/tillr-drain",
				ImportPath: pkg,
				DirRel:     "tools/tillr-drain/drain",
				Reason:     "direct",
			},
		},
	}

	var manyEvents []map[string]any
	for idx := 1; idx < 12; idx++ {
		tname := fmt.Sprintf("TestOrdinary%02d", idx)
		manyEvents = append(manyEvents,
			map[string]any{"Action": "run", "Package": pkg, "Test": tname},
			map[string]any{"Action": "output", "Package": pkg, "Test": tname, "Output": fmt.Sprintf("    fail_test.go:%d: failed %d\n", idx, idx)},
			map[string]any{"Action": "output", "Package": pkg, "Test": tname, "Output": fmt.Sprintf("--- FAIL: %s (0.01s)\n", tname)},
			map[string]any{"Action": "fail", "Package": pkg, "Test": tname, "Elapsed": 0.01},
		)
	}
	manyEvents = append(manyEvents,
		map[string]any{"Action": "run", "Package": pkg, "Test": "TestSub/leaf"},
		map[string]any{"Action": "output", "Package": pkg, "Test": "TestSub/leaf", "Output": "        sub_test.go:77: leaf bad\n"},
		map[string]any{"Action": "output", "Package": pkg, "Test": "TestSub/leaf", "Output": "    --- FAIL: TestSub/leaf (0.01s)\n"},
		map[string]any{"Action": "fail", "Package": pkg, "Test": "TestSub/leaf", "Elapsed": 0.01},
		map[string]any{"Action": "output", "Package": pkg, "Test": "TestSub", "Output": "--- FAIL: TestSub (0.02s)\n"},
		map[string]any{"Action": "fail", "Package": pkg, "Test": "TestSub", "Elapsed": 0.02},
		map[string]any{"Action": "output", "Package": pkg, "Output": "SIGQUIT: quit\n"},
		map[string]any{"Action": "output", "Package": pkg, "Output": pkg + ".TestWorkflowExitRecordRestart(0xc000123456)\n"},
		map[string]any{"Action": "output", "Package": pkg, "Output": "\t/workspace/tools/tillr-drain/drain/workflow_exit_record_test.go:123 +0x45\n"},
		map[string]any{"Action": "output", "Package": pkg, "Output": pkg + "_test.TestExternalBlocked(0xc000123456)\n"},
		map[string]any{"Action": "output", "Package": pkg, "Output": "\t/workspace/tools/tillr-drain/drain/ext_test.go:55 +0x12\n"},
		map[string]any{"Action": "output", "Package": pkg, "Output": fmt.Sprintf("FAIL\t%s\t900.00s\n", pkg)},
		map[string]any{"Action": "fail", "Package": pkg, "Elapsed": 900.0},
	)
	var streamBuf strings.Builder
	for _, ev := range manyEvents {
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		streamBuf.Write(b)
		streamBuf.WriteByte('\n')
	}
	stream := streamBuf.String()

	t.Setenv("YOLO_DURABLE_DIR", filepath.Join(tmpPath, "durable"))
	rc, out, _, err := goaffected.RunSelectedTests(repo, sel, goaffected.RunOptions{
		DisableProgress: true,
		RunCommand: func(cmd []string, cwd string, env []string, onLine func(string)) (int, string, error) {
			return 1, stream, nil
		},
	})
	if err != nil {
		t.Fatalf("RunSelectedTests: %v", err)
	}
	if rc != 1 {
		t.Fatalf("rc=%d, want 1\n%s", rc, out)
	}

	triageHeader := "fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:\n"
	summaryHeader := "go-affected: failing packages and tests:\n"
	triageIdx := strings.Index(out, triageHeader)
	summaryIdx := strings.Index(out, summaryHeader)
	if triageIdx < 0 || summaryIdx < 0 || triageIdx >= summaryIdx {
		t.Fatalf("expected triage before summary (triageIdx=%d, summaryIdx=%d):\n%s", triageIdx, summaryIdx, out)
	}
	triageSection := out[triageIdx:summaryIdx]
	expectedPrefix := triageHeader +
		"  triage go-affected: Go test packages affected by the change pass\n" +
		"       case: TestWorkflowExitRecordRestart -- /workspace/tools/tillr-drain/drain/workflow_exit_record_test.go:123 +0x45\n" +
		fmt.Sprintf("       assertion: %s\n", assertion) +
		"       one case: go -C tools/tillr-drain test ./drain -run '^TestWorkflowExitRecordRestart$'\n" +
		"       case: TestExternalBlocked -- /workspace/tools/tillr-drain/drain/ext_test.go:55 +0x12\n" +
		fmt.Sprintf("       assertion: %s\n", assertion) +
		"       one case: go -C tools/tillr-drain test ./drain -run '^TestExternalBlocked$'\n" +
		fmt.Sprintf("       case: TestOrdinary01 -- %s (fail_test.go:1)\n", pkg) +
		"       assertion: failed 1\n" +
		"       one case: go -C tools/tillr-drain test ./drain -run '^TestOrdinary01$'\n"
	if !strings.HasPrefix(triageSection, expectedPrefix) {
		t.Fatalf("triageSection mismatch:\ngot:\n%s\nwant prefix:\n%s", triageSection, expectedPrefix)
	}
	var caseCount int
	for _, ln := range strings.Split(triageSection, "\n") {
		if strings.HasPrefix(ln, "       case: ") {
			caseCount++
		}
	}
	if caseCount != 10 {
		t.Fatalf("expected 10 case lines, got %d:\n%s", caseCount, triageSection)
	}
	if !strings.Contains(triageSection, "       (4 more failing case(s) not listed; the packet keeps 10)\n") {
		t.Fatalf("expected dropped count line in triageSection:\n%s", triageSection)
	}
	summarySection := out[summaryIdx:]
	wantSummarySubstr := fmt.Sprintf("  %s: TestWorkflowExitRecordRestart, TestExternalBlocked, TestOrdinary01", pkg)
	if !strings.Contains(summarySection, wantSummarySubstr) {
		t.Fatalf("expected %q in summarySection:\n%s", wantSummarySubstr, summarySection)
	}

	// Unnamed failure (e.g. build error) prints the no-case triage line.
	rc2, out2, _, err := goaffected.RunSelectedTests(repo, sel, goaffected.RunOptions{
		DisableProgress: true,
		RunCommand: func(cmd []string, cwd string, env []string, onLine func(string)) (int, string, error) {
			return 1, fmt.Sprintf("# %s\ndrain/broken.go:1:1: syntax error\nFAIL\t%s [build failed]\n", pkg, pkg), nil
		},
	})
	if err != nil {
		t.Fatalf("RunSelectedTests unnamed fail: %v", err)
	}
	if rc2 != 1 {
		t.Fatalf("rc2=%d, want 1\n%s", rc2, out2)
	}
	if !strings.Contains(out2, "       no case named in the output; reproduce with: python3 tools/gate/go_affected.py\n") {
		t.Fatalf("expected no-case triage line in out2:\n%s", out2)
	}
}

func TestWholePackageDurationBasedSharding(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	mod := filepath.Join(repo, "tools", "example")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/example\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "a_test.go"), []byte("package example\nimport \"testing\"\nfunc TestOne(t *testing.T) {}\nfunc TestTwo(t *testing.T) {}\nfunc TestThree(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "tools", "gate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	durationsJSON := `{"example.com/example":{"TestOne":20.0,"TestTwo":20.0,"TestThree":20.0}}`
	if err := os.WriteFile(filepath.Join(repo, "tools", "go-test-durations.json"), []byte(durationsJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("YOLO_DURABLE_DIR", filepath.Join(tmp, "durable"))

	sel := &goaffected.Selection{
		Selected: []goaffected.SelectedPackage{
			{
				Module:        "tools/example",
				ImportPath:    "example.com/example",
				DirRel:        "tools/example",
				TestFiles:     []string{"a_test.go"},
				Reason:        "import",
				SelectedTests: nil,
			},
		},
	}

	var mu sync.Mutex
	var patterns []string
	rc, out, _, err := goaffected.RunSelectedTests(repo, sel, goaffected.RunOptions{
		DisableProgress: true,
		RunCommand: func(cmd []string, cwd string, env []string, onLine func(string)) (int, string, error) {
			pat := ""
			for i := 0; i+1 < len(cmd); i++ {
				if cmd[i] == "-run" {
					pat = cmd[i+1]
					break
				}
			}
			mu.Lock()
			patterns = append(patterns, pat)
			mu.Unlock()
			return 0, fmt.Sprintf("{\"Action\":\"pass\",\"Package\":\"example.com/example\"}\n"), nil
		},
	})
	if err != nil {
		t.Fatalf("RunSelectedTests: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d, want 0:\n%s", rc, out)
	}
	if len(patterns) != 3 {
		t.Fatalf("expected 3 shards for 60s total across 3 tests, got %d: %v", len(patterns), patterns)
	}
	joined := strings.Join(patterns, " ")
	for _, name := range []string{"TestOne", "TestTwo", "TestThree"} {
		if !strings.Contains(joined, name) {
			t.Fatalf("expected %s across shard patterns %v", name, patterns)
		}
	}
}
