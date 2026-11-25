package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func shimEnv(t *testing.T, loopDir, runID string, extra ...string) []string {
	t.Helper()
	shimPath, err := filepath.Abs("bash-shim.sh")
	if err != nil {
		t.Fatalf("abs bash-shim.sh: %v", err)
	}
	base := make([]string, 0, len(os.Environ())+5+len(extra))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "BASH_ENV=") ||
			strings.HasPrefix(kv, "TILLR_TRACE_DIR=") ||
			strings.HasPrefix(kv, "TILLR_RUN_ID=") ||
			strings.HasPrefix(kv, "TILLR_LANE_ID=") {
			continue
		}
		base = append(base, kv)
	}
	base = append(base,
		"BASH_ENV="+shimPath,
		"TILLR_TRACE_DIR="+loopDir,
		"TILLR_RUN_ID="+runID,
		"TILLR_LANE_ID=lane-1",
	)
	base = append(base, extra...)
	return base
}

func shimRows(t *testing.T, loopDir string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(loopDir, "proc-exec.jsonl"))
	if err != nil {
		t.Fatalf("read proc-exec.jsonl: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("unmarshal proc-exec row %q: %v", line, err)
		}
		out = append(out, row)
	}
	return out
}

func runBash(t *testing.T, cwd string, env []string, args ...string) cliResult {
	t.Helper()
	cmd := exec.Command("bash", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if env != nil {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("bash %v: %v", args, err)
		}
	}
	return cliResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func TestBashShimRecordsACommandAndKeepsTheExitCode(t *testing.T) {
	work := t.TempDir()
	runID := "run-shim-1"
	store := filepath.Join(work, "shim-repo", ".tillr", "state.db")
	loopDir := filepath.Join(filepath.Dir(store), "loop", runID)
	if err := os.MkdirAll(loopDir, 0o755); err != nil {
		t.Fatalf("mkdir loopDir: %v", err)
	}
	makeStore(t, store, []storeRunSeed{{runID: runID, itemID: "574", laneID: "lane-1"}})

	env := shimEnv(t, loopDir, runID, "TILLR_REPO_ROOT="+testRepoRoot)
	res := runBash(t, testRepoRoot, env, "-c", "cd tools/tillr-store && false; echo after")
	if res.code != 0 || res.stdout != "after\n" || res.stderr != "" {
		t.Fatalf("unexpected bash result: %+v", res)
	}

	raw, err := os.ReadFile(filepath.Join(loopDir, "proc-exec.jsonl"))
	if err != nil {
		t.Fatalf("read proc-exec.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	for i, l := range lines {
		if len(l) >= 2048 {
			t.Fatalf("line %d len = %d >= 2048", i, len(l))
		}
	}
	rows := shimRows(t, loopDir)
	var cmds []string
	for _, r := range rows {
		cmds = append(cmds, r["cmd"].(string))
	}
	wantCmds := []string{"cd tools/tillr-store", "false", "echo after"}
	if !slices.Equal(cmds, wantCmds) {
		t.Fatalf("commands = %v, want %v", cmds, wantCmds)
	}
	failed := rows[1]
	if failed["exit_code"] != float64(1) {
		t.Fatalf("failed exit_code = %v, want 1", failed["exit_code"])
	}
	if failed["repo_rel_cwd"] != "tools/tillr-store" {
		t.Fatalf("repo_rel_cwd = %v, want tools/tillr-store", failed["repo_rel_cwd"])
	}
	if failed["cwd"] != filepath.Join(testRepoRoot, "tools/tillr-store") {
		t.Fatalf("cwd = %v, want %s", failed["cwd"], filepath.Join(testRepoRoot, "tools/tillr-store"))
	}
	if failed["run_id"] != runID || failed["lane_id"] != "lane-1" {
		t.Fatalf("run/lane = %v/%v", failed["run_id"], failed["lane_id"])
	}
	if !strings.HasPrefix(failed["span_id"].(string), "sh-") {
		t.Fatalf("span_id = %v", failed["span_id"])
	}

	notADir := filepath.Join(work, "not-a-directory")
	if err := os.WriteFile(notADir, []byte("a file, not a directory\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	brokenEnv := shimEnv(t, notADir, runID, "TILLR_REPO_ROOT="+testRepoRoot)
	broken := runBash(t, testRepoRoot, brokenEnv, "-c", "echo still-ok; exit 5")
	if broken.code != 5 || broken.stdout != "still-ok\n" || broken.stderr != "" {
		t.Fatalf("broken fail-open result = %+v", broken)
	}
}

func TestShimStampsUTCInAnyHostZone(t *testing.T) {
	work := t.TempDir()
	for _, zone := range []string{"America/New_York", "Asia/Tokyo"} {
		loopDir := filepath.Join(work, "tz-loop-"+strings.ReplaceAll(zone, "/", "-"))
		if err := os.MkdirAll(loopDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		before := float64(time.Now().UnixNano()) / 1e9
		res := runBash(t, "", shimEnv(t, loopDir, "run-shell-1", "TZ="+zone), "-c", "true")
		if res.code != 0 {
			t.Fatalf("TZ=%s failed (%d): %s", zone, res.code, res.stderr)
		}
		rows := shimRows(t, loopDir)
		row := rows[len(rows)-1]
		ts := row["ts"].(string)
		if !strings.HasSuffix(ts, "Z") {
			t.Fatalf("TZ=%s ts %q does not end with Z", zone, ts)
		}
		parsed, err := time.Parse("2006-01-02T15:04:05.999999999Z", ts)
		if err != nil {
			t.Fatalf("TZ=%s parse ts %q: %v", zone, ts, err)
		}
		skew := float64(parsed.UnixNano())/1e9 - before
		if math.Abs(skew) >= 5 {
			t.Fatalf("TZ=%s row ts %s is %.0fs from real UTC clock", zone, ts, skew)
		}
	}
}

func TestShimStampIsUTCAcrossTheDateBoundaries(t *testing.T) {
	work := t.TempDir()
	bodyBytes, err := os.ReadFile("bash-shim.sh")
	if err != nil {
		t.Fatalf("read bash-shim.sh: %v", err)
	}
	body := string(bodyBytes)
	start := strings.Index(body, "__tillr_proc_iso() {")
	if start < 0 {
		t.Fatalf("missing __tillr_proc_iso in bash-shim.sh")
	}
	endRel := strings.Index(body[start:], "\n}\n")
	if endRel < 0 {
		t.Fatalf("missing closing brace of __tillr_proc_iso")
	}
	fnBody := body[start : start+endRel+3]

	epochs := []int64{0, 59, 86399, 86400, 951782400, 1709164800, 1709168400, 4102444800, 1794550017}
	var script strings.Builder
	script.WriteString(fnBody)
	for _, ep := range epochs {
		fmt.Fprintf(&script, "__tillr_proc_iso \"%d.500000\"\nprintf \"%%s %%s\\n\" \"%d\" \"$__tillr_proc_ts\"\n", ep, ep)
	}
	scriptPath := filepath.Join(work, "iso-boundaries.sh")
	if err := os.WriteFile(scriptPath, []byte(script.String()), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "BASH_ENV=") {
			env = append(env, kv)
		}
	}
	res := runBash(t, "", env, scriptPath)
	if res.code != 0 {
		t.Fatalf("iso-boundaries.sh failed (%d): %s", res.code, res.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(res.stdout, "\n"), "\n")
	if len(lines) != len(epochs) {
		t.Fatalf("got %d lines, want %d: %v", len(lines), len(epochs), lines)
	}
	for _, line := range lines {
		parts := strings.Fields(line)
		ep, _ := strconv.ParseInt(parts[0], 10, 64)
		stamp := parts[1]
		want := time.Unix(ep, 0).UTC().Format("2006-01-02T15:04:05") + ".500Z"
		if stamp != want {
			t.Fatalf("epoch %d: got %q, want %q", ep, stamp, want)
		}
	}
}

func TestShimWritesOneRowPerCommandIncludingTheLast(t *testing.T) {
	work := t.TempDir()
	loopDir := filepath.Join(work, "exitrow-loop")
	if err := os.MkdirAll(loopDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	plain := filepath.Join(work, "exitrow-plain.sh")
	_ = os.WriteFile(plain, []byte("set -euo pipefail\necho one\nexit 0\n"), 0o644)
	seven := filepath.Join(work, "exitrow-seven.sh")
	_ = os.WriteFile(seven, []byte("echo out\necho err >&2\nexit 7\n"), 0o644)

	ok := runBash(t, "", shimEnv(t, loopDir, "run-shell-1"), plain)
	if ok.code != 0 || ok.stdout != "one\n" || ok.stderr != "" {
		t.Fatalf("plain result = %+v", ok)
	}

	failed := runBash(t, "", shimEnv(t, loopDir, "run-shell-1"), seven)
	if failed.code != 7 || failed.stdout != "out\n" || failed.stderr != "err\n" {
		t.Fatalf("seven result = %+v", failed)
	}

	rows := shimRows(t, loopDir)
	var cmds []string
	var codes []int
	for _, r := range rows {
		cmds = append(cmds, r["cmd"].(string))
		codes = append(codes, int(r["exit_code"].(float64)))
	}
	wantCmds := []string{"set -euo pipefail", "echo one", "exit 0", "echo out", "echo err 1>&2", "exit 7"}
	wantCodes := []int{0, 0, 0, 0, 0, 7}
	if !slices.Equal(cmds, wantCmds) {
		t.Fatalf("cmds = %v, want %v", cmds, wantCmds)
	}
	if !slices.Equal(codes, wantCodes) {
		t.Fatalf("codes = %v, want %v", codes, wantCodes)
	}
}

func TestShimAddsNoTraceLinesToAnXShell(t *testing.T) {
	work := t.TempDir()
	loopDir := filepath.Join(work, "xtrace-loop")
	if err := os.MkdirAll(loopDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	xtrace := func(script string) cliResult {
		res := runBash(t, "", shimEnv(t, loopDir, "run-shell-1"), "-x", "-c", script)
		if res.code != 0 {
			t.Fatalf("bash -x -c %q failed (%d): %s", script, res.code, res.stderr)
		}
		return res
	}

	one := xtrace("true")
	two := xtrace("true; true")
	oneLines := strings.Split(strings.TrimSuffix(one.stderr, "\n"), "\n")
	twoLines := strings.Split(strings.TrimSuffix(two.stderr, "\n"), "\n")
	if len(twoLines)-len(oneLines) != 1 {
		t.Fatalf("expected 1 extra line in xtrace, got %d vs %d\none:\n%s\ntwo:\n%s",
			len(oneLines), len(twoLines), one.stderr, two.stderr)
	}

	noisy := xtrace("echo keep-this >&2")
	noisyLines := strings.Split(strings.TrimSuffix(noisy.stderr, "\n"), "\n")
	if noisyLines[len(noisyLines)-1] != "keep-this" {
		t.Fatalf("last line of noisy stderr = %q, want keep-this", noisyLines[len(noisyLines)-1])
	}

	var cmds []string
	for _, r := range shimRows(t, loopDir) {
		cmds = append(cmds, r["cmd"].(string))
	}
	wantCmds := []string{"true", "true", "true", "echo keep-this 1>&2"}
	if !slices.Equal(cmds, wantCmds) {
		t.Fatalf("cmds = %v, want %v", cmds, wantCmds)
	}
}
