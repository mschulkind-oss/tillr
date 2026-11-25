package main_test

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func renderPackTree(t *testing.T) (dir, home, launcher, planner string) {
	t.Helper()
	dir = viewScratch(t)
	home = filepath.Join(dir, "home")
	share := filepath.Join(home, ".local", "share")
	if err := os.MkdirAll(share, 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := filepath.Abs(filepath.Join("..", "..", "yolo-packs", "tillr", "files", "local", "share", "tillr"))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cp", "-r", source, filepath.Join(share, "tillr")).CombinedOutput(); err != nil {
		t.Fatalf("rendering the pack tree: %v: %s", err, out)
	}
	launcher, err = filepath.Abs(filepath.Join("..", "..", "yolo-packs", "tillr", "programs", "tillr-view", "run"))
	if err != nil {
		t.Fatal(err)
	}
	planner = filepath.Join(dir, "tillr-drain")
	if out, err := exec.Command("go", "-C", "../tillr-drain", "build", "-o", planner, ".").CombinedOutput(); err != nil {
		t.Fatalf("building the planner: %v: %s", err, out)
	}
	return dir, home, launcher, planner
}

func goEnvVal(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func waitForFrontEnd(t *testing.T, base, outPath string) string {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		resp, err := http.Get(base + "/api/v1/snapshot")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var parsed struct {
				Snapshot struct {
					Build struct {
						FrontEnd string `json:"front_end"`
					} `json:"build"`
				} `json:"snapshot"`
			}
			if json.Unmarshal(body, &parsed) == nil && parsed.Snapshot.Build.FrontEnd != "" {
				return parsed.Snapshot.Build.FrontEnd
			}
		}
		if time.Now().After(deadline) {
			raw, _ := os.ReadFile(outPath)
			t.Fatalf("the launcher-built server never answered:\n%s", raw)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestShippedLauncherBuildsObservatoryWhenToolchainPresent(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not on PATH")
	}

	dir, home, launcher, planner := renderPackTree(t)
	env := append(os.Environ(),
		"HOME="+home,
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"GOCACHE="+goEnvVal(t, "GOCACHE"),
		"GOMODCACHE="+goEnvVal(t, "GOMODCACHE"),
		"GOFLAGS=-mod=mod",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
	)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()

	store := fixture(t)
	outPath := filepath.Join(dir, "serve.out")
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	cmd := exec.Command(launcher, "serve", "--store", store, "--addr", addr, "--perf-log", "off", "--tillr-drain", planner)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	base := "http://" + addr
	frontEnd := waitForFrontEnd(t, base, outPath)
	if frontEnd != "embedded" {
		raw, _ := os.ReadFile(outPath)
		t.Fatalf("snapshot.build.front_end = %q, want %q\n%s", frontEnd, "embedded", raw)
	}

	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	page, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200:\n%s", resp.StatusCode, page)
	}
	if !strings.Contains(strings.ToLower(string(page)), "<!doctype html>") {
		t.Fatalf("GET / did not return HTML:\n%s", page)
	}
}

func TestShippedLauncherWithoutNodeWarnsAtBuildTimeAndKeepsNoFrontEndNote(t *testing.T) {
	dir, home, launcher, planner := renderPackTree(t)

	// Construct a minimal PATH without node or npm.
	noNodeBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(noNodeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"sh", "go", "cat", "sort", "xargs", "sha256sum", "shasum", "cut", "mkdir", "mv", "rm", "ln"} {
		if p, err := exec.LookPath(tool); err == nil {
			_ = os.Symlink(p, filepath.Join(noNodeBin, tool))
		}
	}

	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PATH=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"PATH="+noNodeBin,
		"HOME="+home,
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"GOCACHE="+goEnvVal(t, "GOCACHE"),
		"GOMODCACHE="+goEnvVal(t, "GOMODCACHE"),
		"GOFLAGS=-mod=mod",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
	)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()

	store := fixture(t)
	outPath := filepath.Join(dir, "serve.out")
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	cmd := exec.Command(launcher, "serve", "--store", store, "--addr", addr, "--perf-log", "off", "--tillr-drain", planner)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	base := "http://" + addr
	frontEnd := waitForFrontEnd(t, base, outPath)
	if frontEnd != "none" {
		raw, _ := os.ReadFile(outPath)
		t.Fatalf("snapshot.build.front_end = %q, want %q\n%s", frontEnd, "none", raw)
	}

	rawOut, _ := os.ReadFile(outPath)
	if !strings.Contains(string(rawOut), "node and npm not found; building without observatory web UI") {
		t.Fatalf("build output did not explain how to get the observatory UI:\n%s", rawOut)
	}

	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	page, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET / status = %d, want 404:\n%s", resp.StatusCode, page)
	}
	for _, want := range []string{
		"carries no built front end",
		"the pack-delivered `tillr-view` on PATH never carries a front end",
	} {
		if !strings.Contains(string(page), want) {
			t.Errorf("GET / 404 body missing %q:\n%s", want, page)
		}
	}
}
