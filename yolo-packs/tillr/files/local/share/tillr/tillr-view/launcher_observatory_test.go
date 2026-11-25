package main_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	sharedNoObsOnce sync.Once
	sharedNoObsBin  string
	sharedNoObsErr  error
)

// seedNoObsLauncherCache builds the unmodified pack tree once per test binary
// through the shipped launcher with node/npm omitted, and links the resulting
// digest-named executable into home's launcher cache. Both the no-node launcher
// test and the initial launch of the restart test share that build instead of
// compiling the same unmodified source tree twice.
func seedNoObsLauncherCache(t *testing.T, home string) {
	t.Helper()
	sharedNoObsOnce.Do(func() {
		dir, err := os.MkdirTemp(filepath.Dir(viewBin), "launcher-no-obs-")
		if err != nil {
			sharedNoObsErr = err
			return
		}
		buildHome := filepath.Join(dir, "home")
		share := filepath.Join(buildHome, ".local", "share")
		if err := os.MkdirAll(share, 0o755); err != nil {
			sharedNoObsErr = err
			return
		}
		source, err := filepath.Abs(filepath.Join("..", "..", "yolo-packs", "tillr", "files", "local", "share", "tillr"))
		if err != nil {
			sharedNoObsErr = err
			return
		}
		if _, err := exec.Command("cp", "-r", source, filepath.Join(share, "tillr")).CombinedOutput(); err != nil {
			sharedNoObsErr = err
			return
		}
		launcher, err := filepath.Abs(filepath.Join("..", "..", "yolo-packs", "tillr", "programs", "tillr-view", "run"))
		if err != nil {
			sharedNoObsErr = err
			return
		}
		env := append(noNodeEnv(t, dir, buildHome), "TILLR_LAUNCHER_RESOLVE=1")
		cmd := exec.Command(launcher)
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			sharedNoObsErr = err
			return
		}
		sharedNoObsBin = strings.TrimSpace(string(out))
	})
	if sharedNoObsErr != nil {
		t.Fatalf("building shared no-observatory launcher binary: %v", sharedNoObsErr)
	}
	if sharedNoObsBin == "" {
		t.Fatal("shared no-observatory launcher binary path was empty")
	}
	dstDir := filepath.Join(home, ".cache", "tillr", "bin")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dstDir, filepath.Base(sharedNoObsBin))
	if err := os.Link(sharedNoObsBin, dst); err != nil {
		raw, rerr := os.ReadFile(sharedNoObsBin)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if werr := os.WriteFile(dst, raw, 0o755); werr != nil {
			t.Fatal(werr)
		}
	}
}

func stubPlanner(t *testing.T, dir string) string {
	t.Helper()
	planner := filepath.Join(dir, "tillr-drain")
	if err := os.WriteFile(planner, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("writing stub planner: %v", err)
	}
	return planner
}

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
	planner = stubPlanner(t, dir)
	return dir, home, launcher, planner
}

func launcherBaseEnv(t *testing.T, home string) []string {
	t.Helper()
	return []string{
		"HOME=" + home,
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"GOCACHE=" + goEnvVal(t, "GOCACHE"),
		"GOMODCACHE=" + goEnvVal(t, "GOMODCACHE"),
		"GOFLAGS=-trimpath -mod=mod",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
	}
}

// noNodeEnv builds an environment with a minimal PATH that omits node and npm,
// so the shipped launcher takes its no-toolchain path without running npm.
func noNodeEnv(t *testing.T, dir, home string) []string {
	t.Helper()
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
	env = append(env, "PATH="+noNodeBin)
	return append(env, launcherBaseEnv(t, home)...)
}

// linkCachedNodeModules links webDir/node_modules to a shared content-addressed
// cache keyed by package-lock.json and the launcher's node platform string.
// That avoids unpacking thousands of files via `npm ci` on every test run while
// leaving `npm run build` (`tsc --noEmit && vite build`) and `go build -tags observatory`
// to run through the shipped launcher as usual. If the cache cannot be prepared,
// it returns quietly and lets the launcher run `npm ci` itself.
func linkCachedNodeModules(t *testing.T, webDir string) {
	t.Helper()
	lockPath := filepath.Join(webDir, "package-lock.json")
	lockBytes, err := os.ReadFile(lockPath)
	if err != nil {
		return
	}
	platOut, err := exec.Command("node", "-p",
		`process.platform + "-" + process.arch + (process.platform === "linux" ? (process.report.getReport().header.glibcVersionRuntime ? "-gnu" : "-musl") : "")`,
	).Output()
	if err != nil {
		return
	}
	platform := strings.TrimSpace(string(platOut))
	if platform == "" {
		return
	}
	sum := sha256.Sum256(lockBytes)
	key := hex.EncodeToString(sum[:8]) + "-" + platform

	cacheRoot, err := os.UserCacheDir()
	if err != nil || cacheRoot == "" {
		cacheRoot = os.Getenv("YOLO_DURABLE_DIR")
	}
	if cacheRoot == "" {
		return
	}
	cacheParent := filepath.Join(cacheRoot, "tillr", "test-web-node-modules")
	if err := os.MkdirAll(cacheParent, 0o755); err != nil {
		return
	}
	cachedEntry := filepath.Join(cacheParent, key)
	cached := filepath.Join(cachedEntry, "node_modules")
	stamp := filepath.Join(cached, ".tillr-platform")
	cachedLock := filepath.Join(cached, ".package-lock.json")

	ready := func() bool {
		raw, err := os.ReadFile(stamp)
		if err != nil || strings.TrimSpace(string(raw)) != platform {
			return false
		}
		_, err = os.Stat(cachedLock)
		return err == nil
	}

	if !ready() {
		tmp, err := os.MkdirTemp(cacheParent, "nm-build-*")
		if err != nil {
			return
		}
		defer os.RemoveAll(tmp)
		pkgBytes, err := os.ReadFile(filepath.Join(webDir, "package.json"))
		if err != nil {
			return
		}
		if err := os.WriteFile(filepath.Join(tmp, "package.json"), pkgBytes, 0o644); err != nil {
			return
		}
		if err := os.WriteFile(filepath.Join(tmp, "package-lock.json"), lockBytes, 0o644); err != nil {
			return
		}
		ci := exec.Command("npm", "--prefix", tmp, "ci", "--no-audit", "--no-fund")
		if err := ci.Run(); err != nil {
			return
		}
		tmpNM := filepath.Join(tmp, "node_modules")
		if err := os.WriteFile(filepath.Join(tmpNM, ".tillr-platform"), []byte(platform+"\n"), 0o644); err != nil {
			return
		}
		if err := os.Rename(tmp, cachedEntry); err != nil && !ready() {
			return
		}
	}

	info, err := os.Stat(cachedLock)
	if err != nil {
		return
	}
	// Ensure package-lock.json is not newer than node_modules/.package-lock.json
	// (cp -r resets mtime to now), so the launcher skips `npm ci` and runs `npm run build`.
	older := info.ModTime().Add(-time.Second)
	if err := os.Chtimes(lockPath, older, older); err != nil {
		return
	}
	_ = os.Symlink(cached, filepath.Join(webDir, "node_modules"))
}

func goEnvVal(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

const hangGuard = 5 * time.Minute

func waitForFrontEnd(t *testing.T, base, outPath string) string {
	t.Helper()
	deadline := time.Now().Add(hangGuard)
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
	t.Parallel()

	dir, home, launcher, planner := renderPackTree(t)
	linkCachedNodeModules(t, filepath.Join(home, ".local", "share", "tillr", "tillr-view", "web"))
	env := append(os.Environ(), launcherBaseEnv(t, home)...)

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
	t.Parallel()
	dir, home, launcher, planner := renderPackTree(t)
	seedNoObsLauncherCache(t, home)
	env := noNodeEnv(t, dir, home)

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
