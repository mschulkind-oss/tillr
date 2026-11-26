package observatory_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var recipeFiles = []string{
	".justfile",
	"tools/observatory/run.sh",
	"tools/tillr-view/Procfile.dev",
	"tools/tillr-view/.air.toml",
}

var baseTools = []string{"bash", "sh", "env", "mkdir", "mv", "rm", "sha256sum", "dirname", "sleep"}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(wd) == "tests" {
		return filepath.Dir(wd)
	}
	return wd
}

func realTool(t *testing.T, name string) string {
	t.Helper()
	for _, dir := range []string{"/usr/bin", "/bin"} {
		cand := filepath.Join(dir, name)
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
	}
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s is required to build minimal PATH: %v", name, err)
	}
	return p
}

func freePortPair(t *testing.T) int {
	t.Helper()
	for i := 0; i < 64; i++ {
		l1, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		port := l1.Addr().(*net.TCPAddr).Port
		l2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port+1))
		_ = l1.Close()
		if err == nil {
			_ = l2.Close()
			return port
		}
	}
	t.Fatal("could not find an adjacent free port pair")
	return 0
}

func airStub(t *testing.T) string {
	t.Helper()
	sed := realTool(t, "sed")
	return fmt.Sprintf(`#!/bin/sh
cfg=""
while [ $# -gt 0 ]; do
    case "$1" in -c) cfg="$2"; shift 2 ;; *) shift ;; esac
done
line=$(%s -n "s/^ *full_bin = '\(.*\)'$/\1/p" "$cfg")
[ -n "$line" ] || { echo "stub air: no full_bin in $cfg" >&2; exit 1; }
exec sh -c "$line"
`, sed)
}

func makeObsRoot(t *testing.T, base, name string, dist bool, modules string) string {
	t.Helper()
	repo := repoRoot(t)
	root := filepath.Join(base, name)
	for _, rel := range recipeFiles {
		src := filepath.Join(repo, filepath.FromSlash(rel))
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, raw, info.Mode()); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(root, "tools", "tillr-drain"), 0o755)
	web := filepath.Join(root, "tools", "tillr-view", "web")
	_ = os.MkdirAll(filepath.Join(web, "src"), 0o755)
	for _, rel := range []string{"index.html", "package.json", "package-lock.json", "vite.config.ts", "src/main.tsx"} {
		_ = os.WriteFile(filepath.Join(web, rel), []byte("x\n"), 0o644)
	}
	if modules != "" {
		_ = os.MkdirAll(filepath.Join(web, "node_modules"), 0o755)
		_ = os.WriteFile(filepath.Join(web, "node_modules", ".package-lock.json"), []byte("{}\n"), 0o644)
		_ = os.WriteFile(filepath.Join(web, "node_modules", ".tillr-platform"), []byte(modules+"\n"), 0o644)
	}
	if dist {
		_ = os.MkdirAll(filepath.Join(web, "dist"), 0o755)
		_ = os.WriteFile(filepath.Join(web, "dist", "index.html"), []byte("<html></html>\n"), 0o644)
		old := time.Now().Add(-time.Hour)
		for _, pattern := range []string{filepath.Join(web, "*.*"), filepath.Join(web, "src", "*"), filepath.Join(web, "node_modules", ".package-lock.json")} {
			matches, _ := filepath.Glob(pattern)
			for _, m := range matches {
				if info, err := os.Stat(m); err == nil && !info.IsDir() {
					_ = os.Chtimes(m, old, old)
				}
			}
		}
	}
	return root
}

func makeObsPath(t *testing.T, base, state string, goOK, nodeOK, gitOK, hivemindOK bool, platform string, npmFails, slowUI bool) string {
	t.Helper()
	bindir := filepath.Join(base, "bin")
	_ = os.MkdirAll(bindir, 0o755)
	for _, tool := range baseTools {
		_ = os.Symlink(realTool(t, tool), filepath.Join(bindir, tool))
	}
	if hivemindOK {
		hm, err := exec.LookPath("hivemind")
		if err != nil {
			t.Fatal(err)
		}
		_ = os.Symlink(hm, filepath.Join(bindir, "hivemind"))
	}
	stubs := map[string]string{
		"air": airStub(t),
	}
	cat := realTool(t, "cat")
	chmod := realTool(t, "chmod")
	basename := realTool(t, "basename")
	if goOK {
		stubs["go"] = fmt.Sprintf(`#!/bin/sh
echo "CGO_ENABLED=$CGO_ENABLED pwd=$(pwd) $*" >> %s/go.calls
out=""
while [ $# -gt 0 ]; do
    case "$1" in -o) out="$2"; shift 2 ;; *) shift ;; esac
done
%s > "$out" <<'PROG'
#!/bin/sh
printf '%%s\n' "$0" "$@" > %s/"$(%s "$0")".argv
printf '%%s\n' "${TILLR_VIEW_PERF_LOG-unset}" "${TILLR_VIEW_PERF_STARTUP-unset}" > %s/"$(%s "$0")".perfenv
n=0
while [ -n "$OBSERVATORY_TEST_WAIT_UI" ] && [ ! -e %s/ui.argv ] && [ $n -lt 300 ]; do
    sleep 0.1; n=$((n + 1))
done
exit 0
PROG
%s +x "$out"
`, state, cat, state, basename, state, basename, state, chmod)
	}
	if gitOK {
		stubs["git"] = "#!/bin/sh\nexit 0\n"
	}
	if nodeOK {
		stubs["node"] = fmt.Sprintf("#!/bin/sh\necho %s\n", platform)
		fail := ":"
		if npmFails {
			fail = "exit 1"
		}
		recordUI := `printf '%s\n' "$@"`
		if slowUI {
			recordUI = `{ printf '%s\n' "$1"; sleep 0.5; shift; printf '%s\n' "$@"; }`
		}
		stubs["npm"] = fmt.Sprintf(`#!/bin/sh
echo "$*" >> %s/npm.calls
case " $* " in
    *" ci "*) echo "npm error stub" >&2; %s ;;
    *" run dev "*) %s > %s/ui.argv.writing; mv %s/ui.argv.writing %s/ui.argv; while :; do sleep 0.2; done ;;
esac
exit 0
`, state, fail, recordUI, state, state, state)
	}
	for name, body := range stubs {
		_ = os.WriteFile(filepath.Join(bindir, name), []byte(body), 0o755)
	}
	return bindir
}

func hermeticRun(t *testing.T, root, recipe string, port int, bindir, durable, cwd string, extra map[string]string) (int, string, string) {
	t.Helper()
	justBin, err := exec.LookPath("just")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(justBin, "--justfile", filepath.Join(root, ".justfile"), recipe, strconv.Itoa(port))
	cmd.Dir = cwd
	env := []string{"PATH=" + bindir, "HOME=" + cwd}
	if durable != "" {
		env = append(env, "YOLO_DURABLE_DIR="+durable)
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("just %s: %v", recipe, err)
		}
	}
	return code, stdout.String(), stderr.String()
}

func argvOf(state, program string) []string {
	raw, err := os.ReadFile(filepath.Join(state, program+".argv"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

func flagVal(argv []string, name string) string {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == name {
			return argv[i+1]
		}
	}
	return ""
}

func TestHermeticObservatoryCases(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just required")
	}
	if _, err := exec.LookPath("hivemind"); err != nil {
		t.Skip("hivemind required")
	}
	base := t.TempDir()

	// 1. Serve names this checkout from anywhere
	var outs []string
	for _, name := range []string{"home/matt/code/tillr", "workspace"} {
		tag := strings.ReplaceAll(name, "/", "-")
		state := filepath.Join(base, "state-"+tag)
		_ = os.MkdirAll(state, 0o755)
		root := makeObsRoot(t, base, name, true, "linux-x64-gnu")
		bindir := makeObsPath(t, filepath.Join(base, "path-"+tag), state, true, false, true, false, "linux-x64-gnu", false, false)
		durable := filepath.Join(base, "durable")
		elsewhere := filepath.Join(base, "elsewhere")
		_ = os.MkdirAll(elsewhere, 0o755)
		port := freePortPair(t)
		code, _, stderr := hermeticRun(t, root, "observatory", port, bindir, durable, elsewhere, nil)
		argv := argvOf(state, "tillr-view")
		if code != 0 || len(argv) == 0 {
			t.Fatalf("[%s] serve failed: code=%d stderr=%s", name, code, stderr)
		}
		realRoot, _ := filepath.EvalSymlinks(root)
		out := filepath.Dir(argv[0])
		outs = append(outs, out)
		checkoutBytes, _ := os.ReadFile(filepath.Join(out, "checkout"))
		if filepath.Dir(out) != filepath.Join(durable, "tillr-observatory") || strings.TrimSpace(string(checkoutBytes)) != realRoot {
			t.Fatalf("[%s] bad out=%s checkout=%q", name, out, checkoutBytes)
		}
		if flagVal(argv, "--store") != filepath.Join(realRoot, ".tillr", "state.db") || flagVal(argv, "--root") != realRoot {
			t.Fatalf("[%s] bad store/root argv=%v", name, argv)
		}
		if flagVal(argv, "--tillr-drain") != filepath.Join(out, "tillr-drain") || flagVal(argv, "--addr") != fmt.Sprintf("127.0.0.1:%d", port) {
			t.Fatalf("[%s] bad drain/addr argv=%v", name, argv)
		}
		if _, err := os.Stat(filepath.Join(state, "npm.calls")); err == nil {
			t.Fatalf("[%s] npm should not be called", name)
		}
	}
	if len(outs) != 2 || outs[0] == outs[1] {
		t.Fatalf("shared build dir: %v", outs)
	}

	// 2. Missing toolchain refused before build
	for _, tc := range []struct{ missing, needle string }{{"go", "go is not on PATH"}, {"git", "git is not on PATH"}} {
		b := filepath.Join(base, "missing-"+tc.missing)
		state := filepath.Join(b, "state")
		_ = os.MkdirAll(state, 0o755)
		root := makeObsRoot(t, b, "root", true, "linux-x64-gnu")
		bindir := makeObsPath(t, filepath.Join(b, "path"), state, tc.missing != "go", true, tc.missing != "git", false, "linux-x64-gnu", false, false)
		code, _, stderr := hermeticRun(t, root, "observatory", freePortPair(t), bindir, filepath.Join(b, "durable"), b, nil)
		if code == 0 || !strings.Contains(stderr, tc.needle) {
			t.Fatalf("missing %s: code=%d stderr=%s", tc.missing, code, stderr)
		}
		if _, err := os.Stat(filepath.Join(state, "go.calls")); err == nil {
			t.Fatalf("missing %s: go was called", tc.missing)
		}
	}

	// 3. Stale front end without Node refused
	{
		b := filepath.Join(base, "stale")
		state := filepath.Join(b, "state")
		_ = os.MkdirAll(state, 0o755)
		root := makeObsRoot(t, b, "root", false, "linux-x64-gnu")
		bindir := makeObsPath(t, filepath.Join(b, "path"), state, true, false, true, false, "linux-x64-gnu", false, false)
		code, _, stderr := hermeticRun(t, root, "observatory", freePortPair(t), bindir, filepath.Join(b, "durable"), b, nil)
		if code == 0 || !strings.Contains(stderr, "node is not on PATH") || !strings.Contains(stderr, "install Node") {
			t.Fatalf("stale without node: code=%d stderr=%s", code, stderr)
		}
	}

	// 4. Modules from another platform reinstalled
	{
		b := filepath.Join(base, "platform")
		root := makeObsRoot(t, b, "root", true, "linux-arm64-musl")
		for attempt, wantCI := range []bool{true, false} {
			state := filepath.Join(b, fmt.Sprintf("state-%d", attempt))
			_ = os.MkdirAll(state, 0o755)
			bindir := makeObsPath(t, filepath.Join(b, fmt.Sprintf("path-%d", attempt)), state, true, true, true, false, "linux-x64-gnu", false, false)
			code, _, stderr := hermeticRun(t, root, "observatory", freePortPair(t), bindir, filepath.Join(b, "durable"), b, nil)
			raw, _ := os.ReadFile(filepath.Join(state, "npm.calls"))
			ranCI := strings.Contains(string(raw), " ci")
			if code != 0 || ranCI != wantCI {
				t.Fatalf("platform attempt %d: code=%d ranCI=%v want=%v stderr=%s", attempt, code, ranCI, wantCI, stderr)
			}
		}
	}

	// 5. Failed npm ci refused before build
	{
		b := filepath.Join(base, "npmfail")
		state := filepath.Join(b, "state")
		_ = os.MkdirAll(state, 0o755)
		root := makeObsRoot(t, b, "root", true, "")
		bindir := makeObsPath(t, filepath.Join(b, "path"), state, true, true, true, false, "linux-x64-gnu", true, false)
		code, _, stderr := hermeticRun(t, root, "observatory", freePortPair(t), bindir, filepath.Join(b, "durable"), b, nil)
		if code == 0 || !strings.Contains(stderr, "npm ci in ") || !strings.Contains(stderr, "NPM_CONFIG_CACHE") {
			t.Fatalf("npm fail: code=%d stderr=%s", code, stderr)
		}
	}

	// 6. Jail binds only published port
	{
		b := filepath.Join(base, "jail")
		port := freePortPair(t)
		root := makeObsRoot(t, b, "root", true, "linux-x64-gnu")
		for _, published := range []bool{false, true} {
			if published {
				_ = os.WriteFile(filepath.Join(root, "yolo-jail.toml"), []byte(fmt.Sprintf("mounts = []\n\n[machine]\nports = [{ host = %d, jail = %d }]\n", port, port)), 0o644)
			}
			state := filepath.Join(b, fmt.Sprintf("state-%v", published))
			_ = os.MkdirAll(state, 0o755)
			bindir := makeObsPath(t, filepath.Join(b, fmt.Sprintf("path-%v", published)), state, true, true, true, false, "linux-x64-gnu", false, false)
			code, _, stderr := hermeticRun(t, root, "observatory", port, bindir, filepath.Join(b, "durable"), b, map[string]string{
				"YOLO_JAIL":                  "1",
				"OBSERVATORY_JAIL_WORKSPACE": root,
				"YOLO_HOST_DIR":              "/home/someone/code/tillr",
			})
			argv := argvOf(state, "tillr-view")
			if code != 0 {
				t.Fatalf("jail published=%v: code=%d stderr=%s", published, code, stderr)
			}
			if published && flagVal(argv, "--addr") != fmt.Sprintf("0.0.0.0:%d", port) {
				t.Fatalf("published jail addr=%v", argv)
			}
			if !published && (flagVal(argv, "--addr") != fmt.Sprintf("127.0.0.1:%d", port) || !strings.Contains(stderr, "yolo config apply")) {
				t.Fatalf("unpublished jail argv=%v stderr=%s", argv, stderr)
			}
		}
	}

	// 7. Dev hands planner and strict port (both normal and slow UI)
	for _, slowUI := range []bool{false, true} {
		b := filepath.Join(base, fmt.Sprintf("dev-%v", slowUI))
		state := filepath.Join(b, "state")
		_ = os.MkdirAll(state, 0o755)
		root := makeObsRoot(t, b, "root", true, "linux-x64-gnu")
		bindir := makeObsPath(t, filepath.Join(b, "path"), state, true, true, true, true, "linux-x64-gnu", false, slowUI)
		port := freePortPair(t)
		code, _, stderr := hermeticRun(t, root, "observatory-dev", port, bindir, filepath.Join(b, "durable"), b, map[string]string{
			"OBSERVATORY_TEST_WAIT_UI": "1",
		})
		api := argvOf(state, "tillr-view-dev")
		ui := argvOf(state, "ui")
		if code != 0 || len(api) == 0 || flagVal(api, "--addr") != fmt.Sprintf("127.0.0.1:%d", port+1) {
			t.Fatalf("dev slow=%v failed: code=%d api=%v stderr=%s", slowUI, code, api, stderr)
		}
		if flagVal(ui, "--port") != strconv.Itoa(port) || flagVal(ui, "--host") != "127.0.0.1" {
			t.Fatalf("dev slow=%v ui=%v", slowUI, ui)
		}
	}
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	s := string(raw)
	if idx := strings.LastIndex(s, ") "); idx >= 0 && len(s) > idx+2 && s[idx+2] == 'Z' {
		return false
	}
	return true
}

func readPID(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return n
}

func killRecorded(state string) {
	for _, name := range []string{"api.pid", "ui.pid"} {
		if pid := readPID(filepath.Join(state, name)); pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func waitFor(timeout time.Duration, pred func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if pred() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestObservatoryDevHelper(t *testing.T) {
	mode := os.Getenv("OBSERVATORY_DEV_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "cleanup-on-eof":
		group, _ := strconv.Atoi(os.Getenv("OBS_HELPER_GROUP"))
		state := os.Getenv("OBS_HELPER_STATE")
		f := os.NewFile(3, "pipe")
		_, _ = io.ReadAll(f)
		_ = f.Close()
		if group > 0 {
			_ = syscall.Kill(-group, syscall.SIGTERM)
		}
		killRecorded(state)
		if group > 0 {
			_ = syscall.Kill(-group, syscall.SIGKILL)
		}
		os.Exit(0)
	case "recipe-proxy":
		state := os.Getenv("OBS_HELPER_STATE")
		port := os.Getenv("OBS_HELPER_PORT")
		exe, _ := os.Executable()
		watcher := exec.Command(exe, "-test.run=^TestObservatoryDevHelper$")
		watcher.Env = append(os.Environ(),
			"OBSERVATORY_DEV_HELPER=cleanup-on-eof",
			"OBS_HELPER_GROUP="+strconv.Itoa(syscall.Getpgrp()),
			"OBS_HELPER_STATE="+state,
		)
		pipeFile := os.NewFile(3, "pipe")
		watcher.ExtraFiles = []*os.File{pipeFile}
		watcher.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		_ = watcher.Start()
		_ = pipeFile.Close()
		recipe := exec.Command("just", "observatory-dev", port)
		recipe.Stdout = os.Stdout
		recipe.Stderr = os.Stderr
		err := recipe.Run()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				os.Exit(ee.ExitCode())
			}
			os.Exit(1)
		}
		os.Exit(0)
	}
}

func writeSupervisionStubs(t *testing.T, bindir, state, root string) {
	t.Helper()
	_ = os.MkdirAll(bindir, 0o755)
	_ = os.WriteFile(filepath.Join(bindir, "state"), []byte(state), 0o644)
	_ = os.WriteFile(filepath.Join(bindir, "root"), []byte(root), 0o644)
	apiPID := filepath.Join(state, "api.pid")
	apiDie := filepath.Join(state, "api.die")
	goCalled := filepath.Join(state, "go.called")
	goBody := fmt.Sprintf(`#!/bin/sh
echo called >> %s
out=""
while [ $# -gt 0 ]; do
    case "$1" in -o) out="$2"; shift 2 ;; *) shift ;; esac
done
mkdir -p "$(dirname "$out")"
cat > "$out" <<'API'
#!/bin/sh
echo $$ > %s
while [ ! -e %s ]; do sleep 0.2; done
exit 2
API
chmod +x "$out"
`, goCalled, apiPID, apiDie)
	uiPID := filepath.Join(state, "ui.pid")
	uiDie := filepath.Join(state, "ui.die")
	npmBody := fmt.Sprintf(`#!/bin/sh
prefix=""
install=""
dev=""
while [ $# -gt 0 ]; do
    case "$1" in
        --prefix) prefix="$2"; shift 2 ;;
        ci) install=1; shift ;;
        dev) dev=1; shift ;;
        *) shift ;;
    esac
done
if [ -n "$install" ]; then
    mkdir -p "$prefix/node_modules"
    printf '{}\n' > "$prefix/node_modules/.package-lock.json"
fi
[ -n "$dev" ] || exit 0
echo $$ > %s
while [ ! -e %s ]; do sleep 0.2; done
exit 0
`, uiPID, uiDie)
	_ = os.WriteFile(filepath.Join(bindir, "go"), []byte(goBody), 0o755)
	_ = os.WriteFile(filepath.Join(bindir, "npm"), []byte(npmBody), 0o755)
	_ = os.WriteFile(filepath.Join(bindir, "air"), []byte(airStub(t)), 0o755)
}

func startSupervisionRun(t *testing.T, port int, bindir, durable, root, state string) (*exec.Cmd, *os.File, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestObservatoryDevHelper$")
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.ExtraFiles = []*os.File{r}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TILLR_VIEW_API=") {
			continue
		}
		if strings.HasPrefix(e, "PATH=") {
			env = append(env, "PATH="+bindir+string(os.PathListSeparator)+strings.TrimPrefix(e, "PATH="))
			continue
		}
		env = append(env, e)
	}
	env = append(env,
		"YOLO_DURABLE_DIR="+durable,
		"OBSERVATORY_DEV_HELPER=recipe-proxy",
		"OBS_HELPER_STATE="+state,
		"OBS_HELPER_PORT="+strconv.Itoa(port),
	)
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		_ = r.Close()
		_ = w.Close()
		t.Fatal(err)
	}
	_ = r.Close()
	t.Cleanup(func() {
		_ = w.Close()
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = cmd.Wait()
		killRecorded(state)
	})
	return cmd, w, &stdout, &stderr
}

func TestObservatoryDevSupervision(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just required")
	}
	if _, err := exec.LookPath("hivemind"); err != nil {
		t.Skip("hivemind required")
	}
	base := t.TempDir()
	durable := filepath.Join(base, "durable")

	// 1. Ctrl-C clears both
	{
		state := filepath.Join(base, "case1")
		_ = os.MkdirAll(state, 0o755)
		bindir := filepath.Join(base, "bin1")
		root := makeObsRoot(t, base, "sup1", true, "linux-x64-gnu")
		writeSupervisionStubs(t, bindir, state, root)
		port := freePortPair(t)
		cmd, w, stdout, stderr := startSupervisionRun(t, port, bindir, durable, root, state)
		if !waitFor(90*time.Second, func() bool {
			return readPID(filepath.Join(state, "api.pid")) > 0 && readPID(filepath.Join(state, "ui.pid")) > 0
		}) {
			_ = w.Close()
			t.Fatalf("stubs never started: %s\n%s", stdout.String(), stderr.String())
		}
		api := readPID(filepath.Join(state, "api.pid"))
		ui := readPID(filepath.Join(state, "ui.pid"))
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
		if !waitFor(20*time.Second, func() bool { return !alive(api) && !alive(ui) }) {
			t.Fatalf("SIGINT left process alive: api=%v ui=%v", alive(api), alive(ui))
		}
		_ = w.Close()
		_ = cmd.Wait()
	}

	// 2. Dead API takes UI
	{
		state := filepath.Join(base, "case2")
		_ = os.MkdirAll(state, 0o755)
		bindir := filepath.Join(base, "bin2")
		root := makeObsRoot(t, base, "sup2", true, "")
		writeSupervisionStubs(t, bindir, state, root)
		port := freePortPair(t)
		cmd, w, _, _ := startSupervisionRun(t, port, bindir, durable, root, state)
		if !waitFor(90*time.Second, func() bool {
			return readPID(filepath.Join(state, "api.pid")) > 0 && readPID(filepath.Join(state, "ui.pid")) > 0
		}) {
			t.Fatal("stubs never started")
		}
		api := readPID(filepath.Join(state, "api.pid"))
		ui := readPID(filepath.Join(state, "ui.pid"))
		_ = os.WriteFile(filepath.Join(state, "api.die"), []byte("go\n"), 0o644)
		if !waitFor(20*time.Second, func() bool { return !alive(api) && !alive(ui) }) {
			t.Fatalf("dead API left UI alive: api=%v ui=%v", alive(api), alive(ui))
		}
		_ = w.Close()
		_ = cmd.Wait()
	}

	// 3. Dead UI takes API
	{
		state := filepath.Join(base, "case3")
		_ = os.MkdirAll(state, 0o755)
		bindir := filepath.Join(base, "bin3")
		root := makeObsRoot(t, base, "sup3", true, "linux-x64-gnu")
		writeSupervisionStubs(t, bindir, state, root)
		port := freePortPair(t)
		cmd, w, _, _ := startSupervisionRun(t, port, bindir, durable, root, state)
		if !waitFor(90*time.Second, func() bool {
			return readPID(filepath.Join(state, "api.pid")) > 0 && readPID(filepath.Join(state, "ui.pid")) > 0
		}) {
			t.Fatal("stubs never started")
		}
		api := readPID(filepath.Join(state, "api.pid"))
		ui := readPID(filepath.Join(state, "ui.pid"))
		_ = os.WriteFile(filepath.Join(state, "ui.die"), []byte("go\n"), 0o644)
		if !waitFor(20*time.Second, func() bool { return !alive(api) && !alive(ui) }) {
			t.Fatalf("dead UI left API alive: api=%v ui=%v", alive(api), alive(ui))
		}
		_ = w.Close()
		_ = cmd.Wait()
	}

	// 4. Held port refused before build
	{
		state := filepath.Join(base, "case4")
		_ = os.MkdirAll(state, 0o755)
		bindir := filepath.Join(base, "bin4")
		root := makeObsRoot(t, base, "sup4", true, "")
		writeSupervisionStubs(t, bindir, state, root)
		port := freePortPair(t)
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port+1))
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		cmd, w, _, stderr := startSupervisionRun(t, port, bindir, durable, root, state)
		err = cmd.Wait()
		_ = w.Close()
		if err == nil {
			t.Fatal("expected held port refusal")
		}
		if !strings.Contains(stderr.String(), fmt.Sprintf(":%d", port+1)) || !strings.Contains(stderr.String(), "already answering") {
			t.Fatalf("unexpected held port stderr: %s", stderr.String())
		}
		if _, err := os.Stat(filepath.Join(state, "go.called")); err == nil {
			t.Fatal("go was called on held port")
		}
	}
	_ = bufio.NewScanner(nil)
}
