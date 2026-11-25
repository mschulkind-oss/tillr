// Package forkgate builds the exact fork head with its pack command, through tillr-gate's verdict face.
//
// The ordinary repository gate reads repository paths, not a fork's paths. This gate
// instead declares fork-build in a per-run registry, and never reads a moving patch
// branch for its build input. Build output is retained under the run directory.
package forkgate

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// DefaultQualityWorkers bounds per-gate worker pools so vitest and Go-based bundlers
// (esbuild) do not spawn full-machine parallelism beside concurrent gates.
const DefaultQualityWorkers = 4

// QualityEntry describes the quality checks for a fork. Either Root is set (for pi,
// which runs tsc at root and npm test per touched package) or Scripts is set (for
// forks whose devDependencies are omitted or pruned by pack builds).
type QualityEntry struct {
	Root    string
	Scripts []string
}

// ForkQuality mirrors tools/fork-check.py's FORK_QUALITY, extended with pi
// (tsc at root, npm test per touched package).
var ForkQuality = map[string]QualityEntry{
	"pi":                {Root: "npx tsc --noEmit"},
	"pi-session-search": {Scripts: []string{"check", "test"}},
	"pi-subagents":      {Scripts: []string{"typecheck", "test:unit"}},
}

// QualityToolEnv lists environment variables kept for quality runs: tool lookup,
// locale, scratch, certificate trust and explicit npm cache settings. Provider
// variables and config paths (including XDG and PI_CODING_AGENT_DIR) are not inherited.
var QualityToolEnv = []string{
	"PATH", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
	"NPM_CONFIG_CACHE", "npm_config_cache", "NPM_CONFIG_OFFLINE", "npm_config_offline",
}

var (
	miseShimRE    = regexp.MustCompile(`^mise ERROR ([\w.+-]+) is not a valid shim(?:\.|$)`)
	hexSHA1Or256  = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	shellSafeChar = regexp.MustCompile(`^[a-zA-Z0-9_@%+=:,./-]+$`)
)

// Run executes the fork-gate CLI with the given arguments and streams.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 3 && args[0] == "--run-quality" {
		return runQuality(args[1], args[2], stdout, stderr)
	}
	if len(args) == 2 && args[0] == "--package-test" {
		return packageTest(args[1], stdout, stderr)
	}
	code, err := runMain(args, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "fork-build: %v\n", err)
		return 4
	}
	return code
}

func runMain(args []string, stdout, stderr io.Writer) (int, error) {
	fset := flag.NewFlagSet("tillr-drain fork-gate", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	item := fset.String("item", "", "item id (required)")
	lane := fset.String("lane", "", "lane id (required)")
	runID := fset.String("run", "", "run id (required)")
	worktree := fset.String("worktree", "", "fork worktree path (required)")
	base := fset.String("base", "", "base commit sha (required)")
	head := fset.String("head", "", "head commit sha (required)")
	rootFlag := fset.String("root", "", "repository root path (required)")
	fork := fset.String("fork", "", "fork name (required)")
	tillrGate := fset.String("tillr-gate", "tillr-gate", "tillr-gate binary")
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fset.SetOutput(stdout)
			fset.Usage()
			return 0, nil
		}
		fmt.Fprintf(stderr, "fork-build: %v\n", err)
		return 2, nil
	}
	for _, req := range []struct {
		name string
		val  string
	}{
		{"--item", *item},
		{"--lane", *lane},
		{"--run", *runID},
		{"--worktree", *worktree},
		{"--base", *base},
		{"--head", *head},
		{"--root", *rootFlag},
		{"--fork", *fork},
	} {
		if req.val == "" {
			fmt.Fprintf(stderr, "fork-build: missing required flag %s\n", req.name)
			return 2, nil
		}
	}

	root, err := filepath.Abs(*rootFlag)
	if err != nil {
		return 4, err
	}
	directory := filepath.Join(root, ".tillr", "loop", *runID)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return 4, err
	}
	for _, stale := range []string{"gate.verdict.json", "fork-cache-source", "fork-build-passed"} {
		if err := os.Remove(filepath.Join(directory, stale)); err != nil && !os.IsNotExist(err) {
			return 4, err
		}
	}

	pack, command, source, err := BuildCommand(root, *fork, stdout)
	if err != nil {
		return 4, err
	}

	// Builds can delete tracked tests or generate files. Never run them in the writer's tree.
	candidate, err := os.MkdirTemp(directory, "fork-build-*")
	if err != nil {
		return 4, err
	}
	if err := call([]string{"git", "clone", "--quiet", "--shared", "--no-checkout", *worktree, candidate}, root, stdout, stderr); err != nil {
		return 4, err
	}
	if err := call([]string{"git", "checkout", "--quiet", "--detach", *head}, candidate, stdout, stderr); err != nil {
		return 4, err
	}
	// A fresh shared cache must retain the upstream refs that pi-forks check reads.
	if err := call([]string{"git", "fetch", "--quiet", "--no-tags", *worktree,
		"+refs/remotes/upstream/*:refs/remotes/upstream/*", "+refs/tags/*:refs/tags/*"}, candidate, stdout, stderr); err != nil {
		return 4, err
	}
	upstreamCmd := exec.Command("git", "remote", "get-url", "upstream")
	upstreamCmd.Dir = *worktree
	if upstreamOut, err := upstreamCmd.Output(); err == nil {
		if err := call([]string{"git", "remote", "add", "upstream", strings.TrimSpace(string(upstreamOut))}, candidate, stdout, stderr); err != nil {
			return 4, err
		}
	}

	buildPassed := filepath.Join(directory, "fork-build-passed")
	buildCmd := fmt.Sprintf("(%s) && : > %s", command, shellQuote(buildPassed))
	qualityBase := *base
	if *fork == "pi" {
		qualityBase, err = piQualityBase(*worktree, source, *base, *head)
		if err != nil {
			return 4, err
		}
	}
	exePath, err := os.Executable()
	if err != nil {
		return 4, err
	}
	quality, err := QualityCommand(*fork, candidate, qualityBase, *head, exePath, stdout)
	if err != nil {
		return 4, err
	}

	relPack, err := filepath.Rel(root, pack)
	if err != nil {
		relPack = pack
	}
	fmt.Fprintf(stdout, "fork-build: %s with %s: %s\n", *head, relPack, command)

	type declaration struct {
		ID        string              `json:"id"`
		Kind      string              `json:"kind"`
		Label     string              `json:"label"`
		Selects   []map[string]string `json:"selects"`
		Watches   []map[string]bool   `json:"watches"`
		After     []string            `json:"after,omitempty"`
		Command   []string            `json:"command"`
		TimeoutMs int                 `json:"timeout_ms"`
	}
	declarationList := []declaration{
		{
			ID:        "fork-build",
			Kind:      "fork-build",
			Label:     *fork + ": pack build",
			Selects:   []map[string]string{{"regex": "."}},
			Watches:   []map[string]bool{{"paths": true}},
			Command:   []string{"sh", "-c", buildCmd},
			TimeoutMs: 0,
		},
	}
	if quality != "" {
		fmt.Fprintf(stdout, "fork-quality: %s: %s\n", *head, quality)
		isolatedQuality := shellJoin(exePath, "fork-gate", "--run-quality", directory, quality)
		guardedQuality := fmt.Sprintf(
			`if [ ! -f %s ]; then echo "fork-quality: skipped because fork-build did not pass" >&2; exit 1; fi && exec %s`,
			shellQuote(buildPassed), isolatedQuality,
		)
		declarationList = append(declarationList, declaration{
			ID:        "fork-quality",
			Kind:      "fork-quality",
			Label:     *fork + ": quality",
			Selects:   []map[string]string{{"regex": "."}},
			Watches:   []map[string]bool{{"paths": true}},
			After:     []string{"fork-build"},
			Command:   []string{"sh", "-c", guardedQuality},
			TimeoutMs: 0,
		})
	} else {
		fmt.Fprintf(stdout, "fork-quality: no quality entry for %s; keeping build-only verdict\n", *fork)
	}

	declBytes, err := marshalCompact(declarationList)
	if err != nil {
		return 4, err
	}
	declarations := string(declBytes)
	inProcess := "[]"
	sum := sha256.Sum256([]byte(declarations + inProcess))
	digest := hex.EncodeToString(sum[:])
	digestBytes, err := marshalCompact(digest)
	if err != nil {
		return 4, err
	}
	registry := filepath.Join(directory, "fork.registry.json")
	registryBody := fmt.Sprintf(`{"digest":%s,"declarations":%s,"in_process":%s}`+"\n", string(digestBytes), declarations, inProcess)
	if err := os.WriteFile(registry, []byte(registryBody), 0o644); err != nil {
		return 4, err
	}

	change := filepath.Join(directory, "gate.change.json")
	plan := filepath.Join(directory, "gate.plan.json")
	verdict := filepath.Join(directory, "gate.verdict.json")
	if err := call([]string{*tillrGate, "change", "--item", *item, "--lane", *lane, "--run", *runID,
		"--worktree", candidate, "--base", *base, "--head", *head, "--output", change}, candidate, stdout, stderr); err != nil {
		return 4, err
	}
	if err := call([]string{*tillrGate, "plan", "--registry", registry, "--input", change,
		"--root", candidate, "--output", plan}, candidate, stdout, stderr); err != nil {
		return 4, err
	}

	gateRunCmd := exec.Command(*tillrGate, "run", "--input", plan, "--worktree", candidate,
		"--output", verdict, "--logs", filepath.Join(directory, "gate.logs"))
	gateRunCmd.Dir = candidate
	gateRunCmd.Stdout = stdout
	gateRunCmd.Stderr = stderr
	gateRunErr := gateRunCmd.Run()

	verdictBytes, err := os.ReadFile(verdict)
	if err != nil {
		code := exitCodeFromErr(gateRunErr)
		return 4, fmt.Errorf("fork build gate produced no verdict (exit %d)", code)
	}
	var parsed struct {
		Payload struct {
			Result string `json:"result"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(verdictBytes, &parsed); err != nil {
		return 4, err
	}
	outcome := parsed.Payload.Result
	if outcome == "pass" {
		// Import the built objects, not a ref: only the lander moves patch/<fork>.
		if err := call([]string{"git", "fetch", "--quiet", "--no-tags", candidate, *head}, root, stdout, stderr); err != nil {
			return 4, err
		}
		if err := os.WriteFile(filepath.Join(directory, "fork-cache-source"), []byte(candidate+"\n"), 0o644); err != nil {
			return 4, err
		}
	}
	switch outcome {
	case "pass":
		return 0, nil
	case "fail":
		return 1, nil
	case "error":
		return 4, nil
	default:
		return 4, fmt.Errorf("unknown gate verdict result %q", outcome)
	}
}

// JSONC parses a JSON-with-comments-and-trailing-commas file, keeping quoted strings intact.
func JSONC(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cleaned := stripJSONC(string(raw))
	return json.Unmarshal([]byte(cleaned), v)
}

func stripJSONC(s string) string {
	// Pass 1: strip // and /* ... */ comments outside quoted strings.
	var noComments strings.Builder
	noComments.Grow(len(s))
	for i := 0; i < len(s); {
		switch s[i] {
		case '"':
			end := scanQuotedString(s, i)
			noComments.WriteString(s[i:end])
			i = end
		case '/':
			if i+1 < len(s) && s[i+1] == '/' {
				i += 2
				for i < len(s) && s[i] != '\n' {
					i++
				}
				continue
			}
			if i+1 < len(s) && s[i+1] == '*' {
				i += 2
				for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
					i++
				}
				if i+1 < len(s) {
					i += 2
				} else {
					i = len(s)
				}
				continue
			}
			noComments.WriteByte(s[i])
			i++
		default:
			noComments.WriteByte(s[i])
			i++
		}
	}

	// Pass 2: strip trailing commas before } or ] outside quoted strings.
	intermediate := noComments.String()
	var out strings.Builder
	out.Grow(len(intermediate))
	for i := 0; i < len(intermediate); {
		switch intermediate[i] {
		case '"':
			end := scanQuotedString(intermediate, i)
			out.WriteString(intermediate[i:end])
			i = end
		case ',':
			j := i + 1
			for j < len(intermediate) && isJSONWhitespace(intermediate[j]) {
				j++
			}
			if j < len(intermediate) && (intermediate[j] == '}' || intermediate[j] == ']') {
				i++
				continue
			}
			out.WriteByte(',')
			i++
		default:
			out.WriteByte(intermediate[i])
			i++
		}
	}
	return out.String()
}

func scanQuotedString(s string, start int) int {
	i := start + 1
	for i < len(s) {
		if s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] == '"' {
			return i + 1
		}
		i++
	}
	return len(s)
}

func isJSONWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// BuildCommand locates the single pack.json delivery for fork under <root>/yolo-packs.
func BuildCommand(root, fork string, stdout io.Writer) (string, string, string, error) {
	packsRoot := filepath.Join(root, "yolo-packs")
	var packPaths []string
	if _, err := os.Stat(packsRoot); err == nil {
		if err := filepath.WalkDir(packsRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && d.Name() == "pack.json" {
				packPaths = append(packPaths, path)
			}
			return nil
		}); err != nil {
			return "", "", "", err
		}
	}
	sort.Strings(packPaths)

	type match struct {
		path    string
		command string
		source  string
	}
	var matches []match
	for _, path := range packPaths {
		var parsed struct {
			Contributes []struct {
				PatchBranch string `json:"patch_branch"`
				Build       string `json:"build"`
				Source      string `json:"source"`
			} `json:"contributes"`
		}
		if err := JSONC(path, &parsed); err != nil {
			return "", "", "", err
		}
		for _, c := range parsed.Contributes {
			u, err := url.Parse(c.PatchBranch)
			if err != nil {
				continue
			}
			refs := u.Query()["ref"]
			if len(refs) == 1 && refs[0] == "patch/"+fork {
				matches = append(matches, match{path: path, command: c.Build, source: c.Source})
			}
		}
	}
	if len(matches) != 1 {
		return "", "", "", fmt.Errorf("fork %s needs exactly one pack delivery, found %d", fork, len(matches))
	}
	m := matches[0]
	command := m.command
	if command == "" {
		// A pack with no build command delivers source only. Still run a real check.
		if stdout != nil {
			fmt.Fprintf(stdout, "fork-build: %s declares no build command; checking source-only delivery\n", m.path)
		}
		command = "git diff --exit-code HEAD && git diff --cached --exit-code"
	}
	return m.path, command, m.source, nil
}

// QualityWorkerLimit returns the bounded worker count as a decimal string.
func QualityWorkerLimit() string {
	cpus := runtime.NumCPU()
	if cpus < 1 {
		cpus = 1
	}
	if cpus > DefaultQualityWorkers {
		cpus = DefaultQualityWorkers
	}
	return strconv.Itoa(cpus)
}

func checkedQualityProcess(argv []string, dir string, env []string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = env
	}
	cmd.Stdout = stdout
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}

	var unavailable string
	reader := bufio.NewReader(stderrPipe)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			_, _ = stderr.Write(line)
			diagnostic := strings.TrimSpace(string(line))
			if diagnostic == "npm error code ENOTCACHED" || diagnostic == "npm ERR! code ENOTCACHED" {
				unavailable = "npm could not resolve the runner offline (ENOTCACHED)"
			} else if m := miseShimRE.FindStringSubmatch(diagnostic); m != nil {
				unavailable = fmt.Sprintf("mise could not resolve %s in the isolated environment", m[1])
			}
		}
		if readErr != nil {
			break
		}
	}

	waitErr := cmd.Wait()
	status := exitCodeFromErr(waitErr)
	if status != 0 && unavailable != "" {
		return status, errors.New(unavailable)
	}
	if status == 126 || status == 127 || status < 0 {
		return status, fmt.Errorf("%s exited %d; see tool diagnostics above", argv[0], status)
	}
	return status, nil
}

func runQuality(directory, command string, stdout, stderr io.Writer) int {
	home, err := os.MkdirTemp(directory, "fork-quality-home-*")
	if err != nil {
		fmt.Fprintf(stderr, "fork-quality: unable to run %s: %v\n", pyRepr(command), err)
		return 4
	}
	defer os.RemoveAll(home)

	var env []string
	for _, name := range QualityToolEnv {
		if val, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+val)
		}
	}
	workers := QualityWorkerLimit()
	env = append(env,
		"HOME="+home,
		"NPM_CONFIG_GLOBALCONFIG="+os.DevNull,
		"VITEST_MAX_WORKERS="+workers,
		"GOMAXPROCS="+workers,
	)

	status, err := checkedQualityProcess([]string{"sh", "-c", command}, "", env, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "fork-quality: unable to run %s: %v\n", pyRepr(command), err)
		return 4
	}
	return status
}

func isAncestor(worktree, ancestor, descendant string) (bool, error) {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", "--end-of-options", ancestor, descendant)
	cmd.Dir = worktree
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	code := exitCodeFromErr(err)
	if code == 0 || code == 1 {
		return code == 0, nil
	}
	return false, fmt.Errorf("fork-quality: cannot check ancestry of %s and %s: %s", ancestor, descendant, strings.TrimSpace(stderrBuf.String()))
}

func piQualityBase(worktree, source, base, head string) (string, error) {
	// Incremental fork candidates build on the previous delivery tip: test only
	// the packages this candidate changed, not unrelated packages touched by
	// earlier fork commits or a stale upstream ref. On a rebase (divergent
	// base), diff from the upstream merge base so upstream-only changes stay out.
	ok, err := isAncestor(worktree, base, head)
	if err != nil {
		return "", err
	}
	if ok {
		return base, nil
	}
	return upstreamBase(worktree, source, head)
}

func upstreamBase(worktree, source, head string) (string, error) {
	git := func(args ...string) (string, int) {
		cmd := exec.Command("git", args...)
		cmd.Dir = worktree
		out, err := cmd.Output()
		return string(out), exitCodeFromErr(err)
	}

	var ref string
	if u, err := url.Parse(source); err == nil {
		ref = u.Query().Get("ref")
	}
	var refs []string
	if ref == "" {
		out, code := git("symbolic-ref", "--quiet", "refs/remotes/upstream/HEAD")
		if code == 0 {
			refs = []string{strings.TrimSpace(out)}
		} else {
			refs = []string{"refs/remotes/upstream/main"}
		}
	} else if hexSHA1Or256.MatchString(ref) {
		refs = []string{ref}
	} else {
		refs = []string{"refs/remotes/upstream/" + ref, "refs/tags/" + ref}
	}

	for _, candidateRef := range refs {
		upstreamOut, code := git("rev-parse", "--verify", "--end-of-options", candidateRef+"^{commit}")
		if code == 0 {
			baseOut, baseCode := git("merge-base", "--all", strings.TrimSpace(upstreamOut), head)
			var bases []string
			for _, line := range strings.Split(strings.TrimSpace(baseOut), "\n") {
				if line != "" {
					bases = append(bases, line)
				}
			}
			if baseCode == 0 && len(bases) == 1 {
				return bases[0], nil
			}
			return "", fmt.Errorf("fork-quality: cannot determine a unique upstream base for %s on %s", head, candidateRef)
		}
	}
	return "", fmt.Errorf("fork-quality: cannot resolve upstream ref (%s) for %s", strings.Join(refs, ", "), head)
}

// TouchedPackages returns the sorted list of packages/<name> directories modified between base and head.
func TouchedPackages(candidate, base, head string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", "-z", base, head)
	cmd.Dir = candidate
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff exited %d", exitCodeFromErr(err))
	}
	pkgSet := make(map[string]struct{})
	for _, relBytes := range bytes.Split(out, []byte{0}) {
		rel := string(relBytes)
		if rel == "" {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) >= 2 && parts[0] == "packages" {
			info, statErr := os.Stat(filepath.Join(candidate, "packages", parts[1]))
			if statErr == nil && info.IsDir() {
				pkgSet["packages/"+parts[1]] = struct{}{}
			}
		}
	}
	packages := make([]string, 0, len(pkgSet))
	for pkg := range pkgSet {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)
	return packages, nil
}

func packageTest(pkg string, stdout, stderr io.Writer) int {
	raw, err := os.ReadFile(filepath.Join(pkg, "package.json"))
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(stderr, "fork-quality: %s: no scripts.test declared (missing package.json)\n", pkg)
		} else {
			fmt.Fprintf(stderr, "fork-quality: %s: cannot read scripts.test: %v\n", pkg, err)
		}
		return 4
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		fmt.Fprintf(stderr, "fork-quality: %s: cannot read scripts.test: %v\n", pkg, err)
		return 4
	}
	scripts, ok := manifest["scripts"].(map[string]any)
	if !ok {
		fmt.Fprintf(stderr, "fork-quality: %s: cannot read scripts.test: no scripts.test declared\n", pkg)
		return 4
	}
	script, ok := scripts["test"].(string)
	if !ok || strings.TrimSpace(script) == "" {
		fmt.Fprintf(stderr, "fork-quality: %s: cannot read scripts.test: no scripts.test declared\n", pkg)
		return 4
	}

	status, err := checkedQualityProcess([]string{"npm", "test"}, pkg, nil, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "fork-quality: %s: unable to run scripts.test %s: %v\n", pkg, pyRepr(script), err)
		return 4
	}
	if status != 0 {
		return 1
	}
	return 0
}

// QualityCommand builds the quality command string for fork, or "" if unlisted.
func QualityCommand(fork, candidate, base, head, exePath string, stdout io.Writer) (string, error) {
	entry, ok := ForkQuality[fork]
	if !ok {
		return "", nil
	}
	if entry.Root != "" {
		commands := []string{entry.Root}
		packages, err := TouchedPackages(candidate, base, head)
		if err != nil {
			return "", err
		}
		if len(packages) == 0 {
			if stdout != nil {
				fmt.Fprintf(stdout, "fork-quality: %s touches no packages/ workspace; skipping package tests\n", fork)
			}
		} else {
			for _, pkg := range packages {
				commands = append(commands, shellJoin(exePath, "fork-gate", "--package-test", pkg))
			}
		}
		return strings.Join(commands, " && "), nil
	}
	// Tuple entries (pi-subagents, pi-session-search) have devDependencies omitted or pruned by pack builds.
	scripts := make([]string, 0, len(entry.Scripts))
	for _, script := range entry.Scripts {
		scripts = append(scripts, "npm run "+shellQuote(script))
	}
	return "npm ci --include=dev --legacy-peer-deps --ignore-scripts --no-audit --no-fund && " + strings.Join(scripts, " && "), nil
}

func call(argv []string, cwd string, stdout, stderr io.Writer) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		code := exitCodeFromErr(err)
		return fmt.Errorf("%s %s exited %d", argv[0], argv[1], code)
	}
	return nil
}

func exitCodeFromErr(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return -int(ws.Signal())
		}
		return exitErr.ExitCode()
	}
	return 1
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if shellSafeChar.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func shellJoin(parts ...string) string {
	quoted := make([]string, len(parts))
	for i, p := range parts {
		quoted[i] = shellQuote(p)
	}
	return strings.Join(quoted, " ")
}

func pyRepr(s string) string {
	if !strings.ContainsRune(s, '\'') {
		return "'" + s + "'"
	}
	if !strings.ContainsRune(s, '"') {
		return `"` + s + `"`
	}
	return fmt.Sprintf("%q", s)
}

func marshalCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
