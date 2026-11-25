package pihelper

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/settings"
	"golang.org/x/sys/unix"
)

const (
	buildMover     = "land a fix to yolo-packs/matt or patch/<fork> on trunk, or rerun tillr-run pi-build-agent-dir"
	buildCondition = "pi-extensions-build-failed"
)

var (
	baseCommitRe = regexp.MustCompile(`(?m)^base-commit:\s*([0-9a-fA-F]{40})\s*$`)
	findTestTsRe = regexp.MustCompile(`find\s+packages\s+-name\s+['"]?\*\.test\.ts['"]?\s+-type\s+f\s+-delete`)
	piBinRefRe   = regexp.MustCompile(`(/[^"'\s]+/node_modules(?:/\.bin/pi|/@earendil-works/pi-coding-agent))`)
)

// RunBuildAgentDir implements the pi-build-agent-dir face.
func RunBuildAgentDir(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tillr-run pi-build-agent-dir", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	keyOnly := fs.Bool("key", false, "print only the 12-char content key")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(stdout)
			fs.Usage()
			return 0
		}
		fmt.Fprintf(stderr, "%s: %v\n", buildCondition, err)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "%s: unexpected argument %q\n", buildCondition, fs.Arg(0))
		return 2
	}

	if err := runBuildAgentDir(*keyOnly, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "%s: %v\nmover: %s\n", buildCondition, err, buildMover)
		return 1
	}
	return 0
}

type deployedNonMattEntry struct {
	Name       string
	Kind       string
	LinkTarget string
	Path       string
}

type patchedPkg struct {
	Name              string
	Contribution      map[string]any
	SourceKind        string
	SourceRepo        string
	Branch            string
	SeriesRel         string
	PackageSeriesKey  string
	ManifestEntryHash string
}

type buildHeldError struct {
	agentDirsRoot string
	targetKey     string
	err           error
}

func (e *buildHeldError) Error() string { return e.err.Error() }

func runBuildAgentDir(keyOnly bool, stdout, stderr io.Writer) error {
	repoRoot, err := resolveRepoRoot()
	if err != nil {
		return err
	}
	trunkRef, err := resolveTrunkRef(repoRoot)
	if err != nil {
		return err
	}
	agentDirsRoot := resolveAgentDirsRoot(repoRoot)
	deployedDir := resolveDeployedDir()

	mattRaw, ok := gitShowText(repoRoot, trunkRef, "yolo-packs/matt/pack.json")
	if !ok {
		return errors.New("missing yolo-packs/matt/pack.json on trunk")
	}
	mattPack, err := parseJSONC(mattRaw)
	if err != nil {
		return err
	}

	mattExtTree := gitObjHash(repoRoot, trunkRef, "yolo-packs/matt/files/extensions")
	mattExtNamesList := gitLSTreeNames(repoRoot, trunkRef, "yolo-packs/matt/files/extensions")
	mattExtNames := make(map[string]bool, len(mattExtNamesList))
	for _, n := range mattExtNamesList {
		mattExtNames[n] = true
	}
	nonMattPackNames := nonMattPackExtensionNames(repoRoot, trunkRef)
	deployedNonMatt := collectDeployedNonMatt(deployedDir, mattExtNames, nonMattPackNames)
	piForksListHash := gitObjHash(repoRoot, trunkRef, "bin/pi-forks.list")

	patched, allDecls, err := resolvePatchedPackages(repoRoot, trunkRef, mattPack, deployedDir)
	if err != nil {
		return err
	}
	key, err := computeKey(mattExtTree, allDecls, piForksListHash, patched, deployedNonMatt)
	if err != nil {
		return err
	}

	if keyOnly {
		_, err := fmt.Fprintln(stdout, key)
		return err
	}

	if err := os.MkdirAll(agentDirsRoot, 0o755); err != nil {
		return err
	}
	lockPath := filepath.Join(agentDirsRoot, ".build.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX); err != nil {
		return err
	}

	keyDir := filepath.Join(agentDirsRoot, key)
	verifiedFile := filepath.Join(keyDir, ".verified")
	if raw, err := os.ReadFile(verifiedFile); err == nil && strings.TrimSpace(string(raw)) == key {
		if st, err := os.Stat(filepath.Join(keyDir, "agent")); err == nil && st.IsDir() {
			if err := writeOKState(agentDirsRoot, key, keyDir); err != nil {
				return err
			}
			_, err := fmt.Fprintln(stdout, filepath.Join(keyDir, "agent"))
			return err
		}
	}

	stage := filepath.Join(agentDirsRoot, fmt.Sprintf(".staging-%s-%d", key, os.Getpid()))
	_ = os.RemoveAll(stage)

	buildErr := func() error {
		stageAgent := filepath.Join(stage, "agent")
		if err := os.MkdirAll(stageAgent, 0o755); err != nil {
			return err
		}

		if st, err := os.Stat(deployedDir); err == nil && st.IsDir() {
			entries, err := os.ReadDir(deployedDir)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				name := entry.Name()
				if name == "extensions" || name == "yolo-patched" {
					continue
				}
				if err := os.Symlink(filepath.Join(deployedDir, name), filepath.Join(stageAgent, name)); err != nil {
					return err
				}
			}
		}

		stageExt := filepath.Join(stageAgent, "extensions")
		if err := os.MkdirAll(stageExt, 0o755); err != nil {
			return err
		}
		for _, entry := range deployedNonMatt {
			if err := os.Symlink(entry.Path, filepath.Join(stageExt, entry.Name)); err != nil {
				return err
			}
		}

		if mattExtTree != "" {
			if err := gitArchiveExtract(repoRoot, trunkRef+":yolo-packs/matt/files/extensions", stageExt); err != nil {
				return err
			}
		}

		stagePatched := filepath.Join(stageAgent, "yolo-patched")
		if err := os.MkdirAll(stagePatched, 0o755); err != nil {
			return err
		}
		for _, pkg := range patched {
			pkgCacheDir, err := materializeAndBuildPackage(pkg, repoRoot, trunkRef, agentDirsRoot)
			if err != nil {
				return err
			}
			if err := copyTreeHardlinkOrCopy(pkgCacheDir, filepath.Join(stagePatched, pkg.Name), map[string]bool{".built": true}); err != nil {
				return err
			}
		}

		if err := verifyExtensionsLoad(stageAgent, agentDirsRoot); err != nil {
			return err
		}

		if err := os.WriteFile(filepath.Join(stage, ".verified"), []byte(key+"\n"), 0o644); err != nil {
			return err
		}
		if _, err := os.Stat(keyDir); err == nil {
			if err := os.RemoveAll(keyDir); err != nil {
				return err
			}
		}
		if err := os.Rename(stage, keyDir); err != nil {
			return err
		}
		if err := writeOKState(agentDirsRoot, key, keyDir); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, filepath.Join(keyDir, "agent"))
		return err
	}()

	if buildErr != nil {
		_ = os.RemoveAll(stage)
		_ = writeHeldState(agentDirsRoot, key, buildErr.Error())
		return buildErr
	}
	return nil
}

func stripJSONC(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	n := len(text)
	inStr := false
	escape := false
	inLineComment := false
	inBlockComment := false
	for i := 0; i < n; i++ {
		ch := text[i]
		var nxt byte
		if i+1 < n {
			nxt = text[i+1]
		}
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				out.WriteByte(ch)
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && nxt == '/' {
				inBlockComment = false
				i++
			} else if ch == '\n' {
				out.WriteByte(ch)
			}
			continue
		}
		if inStr {
			out.WriteByte(ch)
			if escape {
				escape = false
			} else if ch == '\\' {
				escape = true
			} else if ch == '"' {
				inStr = false
			}
			continue
		}
		if ch == '"' {
			inStr = true
			out.WriteByte(ch)
			continue
		}
		if ch == '/' && nxt == '/' {
			inLineComment = true
			i++
			continue
		}
		if ch == '/' && nxt == '*' {
			inBlockComment = true
			i++
			continue
		}
		out.WriteByte(ch)
	}
	return out.String()
}

func parseJSONC(text string) (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(stripJSONC(text)), &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// canonicalJSON encodes v with sorted map keys, no extra whitespace, and
// SetEscapeHTML(false) so it matches Python's json.dumps(..., sort_keys=True, separators=(",", ":")).
func canonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func runGit(repo string, args ...string) (string, string, int, error) {
	cmdArgs := append([]string{"-C", repo}, args...)
	cmd := exec.Command("git", cmdArgs...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = 1
		}
	}
	return outBuf.String(), errBuf.String(), code, err
}

func mustRunGit(repo string, args ...string) (string, error) {
	stdout, stderr, code, err := runGit(repo, args...)
	if code != 0 || err != nil {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(stdout)
		}
		if detail == "" && err != nil {
			detail = err.Error()
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), detail)
	}
	return stdout, nil
}

func resolveRepoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", errors.New("not inside a git repository")
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return "", errors.New("not inside a git repository")
	}
	return filepath.Dir(common), nil
}

func resolveTrunkRef(repoRoot string) (string, error) {
	cfg, err := settings.Read(repoRoot)
	if err != nil {
		return "", fmt.Errorf("read repository settings: %w", err)
	}
	trunkRef := cfg.TrunkRef()
	if out, _, code, _ := runGit(repoRoot, "rev-parse", "--verify", "--quiet", trunkRef); code == 0 && strings.TrimSpace(out) != "" {
		return trunkRef, nil
	}
	if out, _, code, _ := runGit(repoRoot, "rev-parse", "--verify", "--quiet", "HEAD"); code == 0 && strings.TrimSpace(out) != "" {
		return "HEAD", nil
	}
	return "", fmt.Errorf("cannot resolve %s or HEAD", trunkRef)
}

func gitShowText(repoRoot, ref, relPath string) (string, bool) {
	out, _, code, _ := runGit(repoRoot, "show", fmt.Sprintf("%s:%s", ref, relPath))
	if code != 0 {
		return "", false
	}
	return out, true
}

func gitObjHash(repoRoot, ref, relPath string) string {
	out, _, code, _ := runGit(repoRoot, "rev-parse", "--verify", "--quiet", fmt.Sprintf("%s:%s", ref, relPath))
	if code != 0 {
		return ""
	}
	return strings.TrimSpace(out)
}

func gitLSTreeNames(repoRoot, ref, relPath string) []string {
	out, _, code, _ := runGit(repoRoot, "ls-tree", "--name-only", fmt.Sprintf("%s:%s", ref, relPath))
	if code != 0 {
		return nil
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names
}

func gitLSPackJSONs(repoRoot, ref string) []string {
	out, _, code, _ := runGit(repoRoot, "ls-tree", "-r", "--name-only", ref, "--", "yolo-packs")
	if code != 0 {
		return nil
	}
	var res []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "/")
		if len(parts) == 3 && parts[0] == "yolo-packs" && parts[2] == "pack.json" {
			res = append(res, line)
		}
	}
	sort.Strings(res)
	return res
}

func resolveAgentDirsRoot(repoRoot string) string {
	if override := strings.TrimSpace(os.Getenv("TILLR_PI_AGENT_DIRS_DIR")); override != "" {
		if abs, err := filepath.Abs(override); err == nil {
			return abs
		}
		return override
	}
	if durable := strings.TrimSpace(os.Getenv("YOLO_DURABLE_DIR")); durable != "" {
		if abs, err := filepath.Abs(durable); err == nil {
			return filepath.Join(abs, "pi-agent-dirs")
		}
		return filepath.Join(durable, "pi-agent-dirs")
	}
	return filepath.Join(repoRoot, ".yolo", "durable", "pi-agent-dirs")
}

func resolveDeployedDir() string {
	if override := strings.TrimSpace(os.Getenv("TILLR_DEPLOYED_PI_AGENT_DIR")); override != "" {
		if abs, err := filepath.Abs(override); err == nil {
			return abs
		}
		return override
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pi", "agent")
}

func atomicWriteJSON(path string, data map[string]any) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return err
	}
	tmp := filepath.Join(parent, fmt.Sprintf(".%s.tmp.%d", filepath.Base(path), os.Getpid()))
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func copyTreeHardlinkOrCopy(src, dst string, exclude map[string]bool) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if exclude[name] {
			continue
		}
		srcPath := filepath.Join(src, name)
		dstPath := filepath.Join(dst, name)
		typ := entry.Type()
		if typ&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(srcPath)
			if err != nil {
				return err
			}
			if err := os.Symlink(linkTarget, dstPath); err != nil {
				return err
			}
		} else if entry.IsDir() {
			if err := copyTreeHardlinkOrCopy(srcPath, dstPath, nil); err != nil {
				return err
			}
		} else {
			if err := exec.Command("cp", "-al", srcPath, dstPath).Run(); err != nil {
				if err := copyFileWithMode(srcPath, dstPath); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func copyFileWithMode(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func nonMattPackExtensionNames(repoRoot, trunkRef string) map[string]bool {
	names := make(map[string]bool)
	for _, packRel := range gitLSPackJSONs(repoRoot, trunkRef) {
		parts := strings.Split(packRel, "/")
		if len(parts) < 2 || parts[1] == "matt" {
			continue
		}
		raw, ok := gitShowText(repoRoot, trunkRef, packRel)
		if !ok || raw == "" {
			continue
		}
		doc, err := parseJSONC(raw)
		if err != nil {
			continue
		}
		contributes, _ := doc["contributes"].([]any)
		for _, item := range contributes {
			c, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if kind, _ := c["kind"].(string); kind != "files" {
				continue
			}
			into, _ := c["into"].(string)
			const prefix = ".pi/agent/extensions/"
			if strings.HasPrefix(into, prefix) {
				rel := strings.Trim(into[len(prefix):], "/")
				if rel != "" {
					first := strings.SplitN(rel, "/", 2)[0]
					names[first] = true
				}
			}
		}
	}
	return names
}

func collectDeployedNonMatt(deployedDir string, mattExtNames, nonMattPackNames map[string]bool) []deployedNonMattEntry {
	extDir := filepath.Join(deployedDir, "extensions")
	st, err := os.Stat(extDir)
	if err != nil || !st.IsDir() {
		return nil
	}
	dirEntries, err := os.ReadDir(extDir)
	if err != nil {
		return nil
	}
	var entries []deployedNonMattEntry
	for _, entry := range dirEntries {
		name := entry.Name()
		if mattExtNames[name] {
			continue
		}
		entryPath := filepath.Join(extDir, name)
		statInfo, statErr := os.Stat(entryPath)
		isDir := statErr == nil && statInfo.IsDir()
		if isDir || strings.HasPrefix(name, "yolo-") || nonMattPackNames[name] {
			linkTarget := ""
			if entry.Type()&os.ModeSymlink != 0 {
				if target, err := os.Readlink(entryPath); err == nil {
					linkTarget = target
				}
			}
			kind := "file"
			if isDir {
				kind = "dir"
			}
			entries = append(entries, deployedNonMattEntry{
				Name:       name,
				Kind:       kind,
				LinkTarget: linkTarget,
				Path:       entryPath,
			})
		}
	}
	return entries
}

func readDeployedSettingsPackages(deployedDir string) []string {
	settingsPath := filepath.Join(deployedDir, "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil
	}
	pkgs, _ := data["packages"].([]any)
	var out []string
	for _, p := range pkgs {
		if s, ok := p.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func parseBranchFromLocator(locator, defaultName string) string {
	if _, after, ok := strings.Cut(locator, "?ref="); ok {
		branch, _, _ := strings.Cut(after, "&")
		return branch
	}
	return "patch/" + defaultName
}

func resolvePatchedPackages(repoRoot, trunkRef string, mattPack map[string]any, deployedDir string) ([]patchedPkg, []any, error) {
	settingsPackages := readDeployedSettingsPackages(deployedDir)
	var patched []patchedPkg
	allDecls := []any{}

	const prefix = ".pi/agent/yolo-patched/"
	contributes, _ := mattPack["contributes"].([]any)
	for _, item := range contributes {
		c, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if kind, _ := c["kind"].(string); kind != "files" {
			continue
		}
		into, _ := c["into"].(string)
		if !strings.HasPrefix(into, prefix) {
			continue
		}
		name := strings.Trim(into[len(prefix):], "/")
		if name == "" {
			continue
		}
		allDecls = append(allDecls, c)

		inDeployed := false
		if st, err := os.Stat(filepath.Join(deployedDir, "yolo-patched", name)); err == nil && st.IsDir() {
			inDeployed = true
		}
		inSettings := false
		for _, p := range settingsPackages {
			if p == name || strings.HasSuffix(p, "/yolo-patched/"+name) {
				inSettings = true
				break
			}
		}
		if !inDeployed && !inSettings {
			continue
		}

		encoded, err := canonicalJSON(c)
		if err != nil {
			return nil, nil, err
		}
		manifestEntryHash := sha256Hex(encoded)

		if patchBranchVal, hasBranch := c["patch_branch"]; hasBranch {
			locator, _ := patchBranchVal.(string)
			branch := parseBranchFromLocator(locator, name)
			var sourceRepo, seriesKey string
			for _, cand := range []string{repoRoot, filepath.Join(repoRoot, ".forks", name)} {
				if _, err := os.Stat(cand); err != nil {
					continue
				}
				out, _, code, _ := runGit(cand, "rev-parse", "--verify", "--quiet", fmt.Sprintf("refs/heads/%s^{commit}", branch))
				if code == 0 && strings.TrimSpace(out) != "" {
					sourceRepo = cand
					seriesKey = strings.TrimSpace(out)
					break
				}
			}
			if sourceRepo == "" {
				return nil, nil, fmt.Errorf("cannot resolve patch branch refs/heads/%s for %s", branch, name)
			}
			patched = append(patched, patchedPkg{
				Name:              name,
				Contribution:      c,
				SourceKind:        "branch",
				SourceRepo:        sourceRepo,
				Branch:            branch,
				PackageSeriesKey:  seriesKey,
				ManifestEntryHash: manifestEntryHash,
			})
		} else if patchesVal, hasPatches := c["patches"]; hasPatches {
			patchesRel := strings.Trim(fmt.Sprint(patchesVal), "/")
			if !strings.HasPrefix(patchesRel, "yolo-packs/") {
				patchesRel = "yolo-packs/matt/" + patchesRel
			}
			seriesKey := gitObjHash(repoRoot, trunkRef, patchesRel)
			if seriesKey == "" {
				return nil, nil, fmt.Errorf("cannot resolve patch series %s for %s", patchesRel, name)
			}
			patched = append(patched, patchedPkg{
				Name:              name,
				Contribution:      c,
				SourceKind:        "series",
				SeriesRel:         patchesRel,
				PackageSeriesKey:  seriesKey,
				ManifestEntryHash: manifestEntryHash,
			})
		} else {
			return nil, nil, fmt.Errorf("yolo-patched/%s has neither patch_branch nor patches", name)
		}
	}
	return patched, allDecls, nil
}

func computeKey(mattExtTree string, allDecls []any, piForksListHash string, patched []patchedPkg, deployedNonMatt []deployedNonMattEntry) (string, error) {
	pkgsPayload := make([]any, 0, len(patched))
	for _, p := range patched {
		pkgsPayload = append(pkgsPayload, map[string]any{
			"name":                p.Name,
			"package_series_key":  p.PackageSeriesKey,
			"manifest_entry_hash": p.ManifestEntryHash,
		})
	}
	nonMattPayload := make([]any, 0, len(deployedNonMatt))
	for _, e := range deployedNonMatt {
		nonMattPayload = append(nonMattPayload, map[string]any{
			"name":        e.Name,
			"kind":        e.Kind,
			"link_target": e.LinkTarget,
		})
	}
	payload := map[string]any{
		"matt_extensions_tree": mattExtTree,
		"yolo_patched_decls":   allDecls,
		"pi_forks_list":        piForksListHash,
		"packages":             pkgsPayload,
		"deployed_non_matt":    nonMattPayload,
	}
	encoded, err := canonicalJSON(payload)
	if err != nil {
		return "", err
	}
	return sha256Hex(encoded)[:12], nil
}

func rewriteBuildCmd(cmd, cacheRoot string) string {
	pnpmXDG := filepath.Join(cacheRoot, "pnpm-xdg")
	cmd = strings.ReplaceAll(cmd, "/tmp/pnpm-xdg", pnpmXDG)
	nodeDel := `node -e "const fs=require('fs'),p=require('path');(function w(d){if(!fs.existsSync(d))return;for(const e of fs.readdirSync(d,{withFileTypes:true})){const f=p.join(d,e.name);if(e.isDirectory())w(f);else if(e.isFile()&&e.name.endsWith('.test.ts'))fs.unlinkSync(f)}})('packages')"`
	return findTestTsRe.ReplaceAllString(cmd, nodeDel)
}

func gitArchiveExtract(repoRoot, treeSpec, dst string) error {
	gitCmd := exec.Command("git", "-C", repoRoot, "archive", "--format=tar", treeSpec)
	tarCmd := exec.Command("tar", "-xf", "-", "-C", dst)
	pipe, err := gitCmd.StdoutPipe()
	if err != nil {
		return err
	}
	tarCmd.Stdin = pipe
	var gitErr, tarErr bytes.Buffer
	gitCmd.Stderr = &gitErr
	tarCmd.Stderr = &tarErr
	if err := gitCmd.Start(); err != nil {
		return err
	}
	if err := tarCmd.Start(); err != nil {
		_ = gitCmd.Wait()
		return err
	}
	gErr := gitCmd.Wait()
	tErr := tarCmd.Wait()
	if gErr != nil {
		return fmt.Errorf("git archive %s: %s (%v)", treeSpec, strings.TrimSpace(gitErr.String()), gErr)
	}
	if tErr != nil {
		return fmt.Errorf("tar extract %s: %s (%v)", treeSpec, strings.TrimSpace(tarErr.String()), tErr)
	}
	return nil
}

func materializeAndBuildPackage(pkg patchedPkg, repoRoot, trunkRef, agentDirsRoot string) (string, error) {
	name := pkg.Name
	pkgKeyBytes, err := canonicalJSON(map[string]any{
		"name":                name,
		"package_series_key":  pkg.PackageSeriesKey,
		"manifest_entry_hash": pkg.ManifestEntryHash,
	})
	if err != nil {
		return "", err
	}
	pkgKey := sha256Hex(pkgKeyBytes)[:16]

	pkgCacheRoot := filepath.Join(agentDirsRoot, ".pkg-cache")
	pkgCacheDir := filepath.Join(pkgCacheRoot, fmt.Sprintf("%s-%s", name, pkgKey))
	var produces []string
	if rawProd, ok := pkg.Contribution["produces"].([]any); ok {
		for _, p := range rawProd {
			if s, ok := p.(string); ok {
				produces = append(produces, s)
			}
		}
	}

	if st, err := os.Stat(filepath.Join(pkgCacheDir, ".built")); err == nil && !st.IsDir() {
		allExist := true
		for _, p := range produces {
			if _, err := os.Stat(filepath.Join(pkgCacheDir, p)); err != nil {
				allExist = false
				break
			}
		}
		if allExist {
			return pkgCacheDir, nil
		}
	}

	candDir := filepath.Join(agentDirsRoot, ".candidates", name)
	if err := os.MkdirAll(filepath.Dir(candDir), 0o755); err != nil {
		return "", err
	}
	cacheRoot := filepath.Join(agentDirsRoot, ".cache")
	for _, sub := range []string{"npm", "pnpm-xdg/data", "pnpm-xdg/state", "pnpm-xdg/cache", "pnpm-xdg/npm", "pnpm-xdg/store", "xdg-cache", "xdg-data", "xdg-state", "tmp"} {
		if err := os.MkdirAll(filepath.Join(cacheRoot, sub), 0o755); err != nil {
			return "", err
		}
	}

	if pkg.SourceKind == "branch" {
		if _, err := os.Stat(filepath.Join(candDir, ".git")); err != nil {
			_ = os.RemoveAll(candDir)
			if _, err := mustRunGit(filepath.Dir(candDir), "clone", "-q", "--shared", "--no-checkout", pkg.SourceRepo, candDir); err != nil {
				return "", err
			}
		}
		if _, err := mustRunGit(candDir, "fetch", "-q", pkg.SourceRepo, fmt.Sprintf("+refs/heads/%s:refs/heads/%s", pkg.Branch, pkg.Branch)); err != nil {
			return "", err
		}
		_, _, _, _ = runGit(candDir, "checkout", "-q", "-f", "--detach")
		if _, err := mustRunGit(candDir, "checkout", "-q", "-f", "-B", "main", fmt.Sprintf("refs/heads/%s", pkg.Branch)); err != nil {
			return "", err
		}
		if _, err := mustRunGit(candDir, "clean", "-qfd"); err != nil {
			return "", err
		}
	} else {
		forksCache := filepath.Join(repoRoot, ".forks", name)
		if _, err := os.Stat(forksCache); err != nil {
			return "", fmt.Errorf("missing shared fork checkout at %s for %s", forksCache, name)
		}
		patchTmp := filepath.Join(cacheRoot, fmt.Sprintf("patches-%s-%d", name, os.Getpid()))
		_ = os.RemoveAll(patchTmp)
		if err := os.MkdirAll(patchTmp, 0o755); err != nil {
			return "", err
		}
		defer os.RemoveAll(patchTmp)

		if err := gitArchiveExtract(repoRoot, fmt.Sprintf("%s:%s", trunkRef, pkg.SeriesRel), patchTmp); err != nil {
			return "", err
		}
		entries, err := os.ReadDir(patchTmp)
		if err != nil {
			return "", err
		}
		var patchFiles []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".patch") {
				patchFiles = append(patchFiles, filepath.Join(patchTmp, e.Name()))
			}
		}
		sort.Strings(patchFiles)
		if len(patchFiles) == 0 {
			return "", fmt.Errorf("no .patch files in %s", pkg.SeriesRel)
		}
		firstBytes, err := os.ReadFile(patchFiles[0])
		if err != nil {
			return "", err
		}
		m := baseCommitRe.FindSubmatch(firstBytes)
		if m == nil {
			return "", fmt.Errorf("missing base-commit in %s", filepath.Base(patchFiles[0]))
		}
		baseCommit := string(m[1])
		if _, err := os.Stat(filepath.Join(candDir, ".git")); err != nil {
			_ = os.RemoveAll(candDir)
			if _, err := mustRunGit(filepath.Dir(candDir), "clone", "-q", "--shared", "--no-checkout", forksCache, candDir); err != nil {
				return "", err
			}
		}
		_, _, _, _ = runGit(candDir, "fetch", "-q", forksCache)
		if _, err := mustRunGit(candDir, "checkout", "-q", "-f", "--detach", baseCommit); err != nil {
			return "", err
		}
		if _, err := mustRunGit(candDir, "clean", "-qfd"); err != nil {
			return "", err
		}
		amArgs := append([]string{"am", "-q"}, patchFiles...)
		if _, err := mustRunGit(candDir, amArgs...); err != nil {
			return "", err
		}
		if _, err := mustRunGit(candDir, "checkout", "-q", "-B", "main"); err != nil {
			return "", err
		}
	}

	buildCmdStr, _ := pkg.Contribution["build"].(string)
	buildCmdStr = strings.TrimSpace(buildCmdStr)
	if buildCmdStr != "" {
		rewritten := rewriteBuildCmd(buildCmdStr, cacheRoot)
		cmd := exec.Command("/bin/sh", "-c", rewritten)
		cmd.Dir = candDir
		env := append(os.Environ(),
			"npm_config_cache="+filepath.Join(cacheRoot, "npm"),
			"NPM_CONFIG_CACHE="+filepath.Join(cacheRoot, "npm"),
			"XDG_CACHE_HOME="+filepath.Join(cacheRoot, "xdg-cache"),
			"XDG_DATA_HOME="+filepath.Join(cacheRoot, "xdg-data"),
			"XDG_STATE_HOME="+filepath.Join(cacheRoot, "xdg-state"),
			"TMPDIR="+filepath.Join(cacheRoot, "tmp"),
		)
		cmd.Env = env
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err != nil {
			detail := strings.TrimSpace(errBuf.String())
			if detail == "" {
				detail = strings.TrimSpace(outBuf.String())
			}
			if detail == "" {
				detail = err.Error()
			}
			return "", fmt.Errorf("build failed for %s: %s", name, detail)
		}
	}

	for _, rel := range produces {
		if _, err := os.Stat(filepath.Join(candDir, rel)); err != nil {
			return "", fmt.Errorf("build for %s did not produce %s", name, rel)
		}
	}

	if err := os.MkdirAll(pkgCacheRoot, 0o755); err != nil {
		return "", err
	}
	pkgStage := filepath.Join(pkgCacheRoot, fmt.Sprintf(".staging-%s-%s-%d", name, pkgKey, os.Getpid()))
	_ = os.RemoveAll(pkgStage)
	if err := copyTreeHardlinkOrCopy(candDir, pkgStage, map[string]bool{".git": true}); err != nil {
		_ = os.RemoveAll(pkgStage)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(pkgStage, ".built"), []byte(pkgKey+"\n"), 0o644); err != nil {
		_ = os.RemoveAll(pkgStage)
		return "", err
	}
	if _, err := os.Stat(pkgCacheDir); err == nil {
		if err := os.RemoveAll(pkgCacheDir); err != nil {
			_ = os.RemoveAll(pkgStage)
			return "", err
		}
	}
	if err := os.Rename(pkgStage, pkgCacheDir); err != nil {
		_ = os.RemoveAll(pkgStage)
		return "", err
	}
	return pkgCacheDir, nil
}

func findPiCodingAgentPkg() string {
	var candidates []string
	whichPi := strings.TrimSpace(os.Getenv("PI_HARNESS_BIN"))
	if whichPi == "" {
		whichPi, _ = exec.LookPath("pi")
	}
	if whichPi != "" {
		if resolved, err := filepath.EvalSymlinks(whichPi); err == nil {
			whichPi = resolved
		}
		parent := filepath.Dir(whichPi)
		grandParent := filepath.Dir(parent)
		candidates = append(candidates,
			filepath.Join(parent, "index.js"),
			filepath.Join(grandParent, "index.js"),
			filepath.Join(parent, "node_modules/@earendil-works/pi-coding-agent/dist/index.js"),
			filepath.Join(grandParent, "lib/node_modules/@earendil-works/pi-coding-agent/dist/index.js"),
			filepath.Join(grandParent, "node_modules/@earendil-works/pi-coding-agent/dist/index.js"),
		)
		if info, err := os.Stat(whichPi); err == nil && !info.IsDir() && info.Size() < 65536 {
			if raw, err := os.ReadFile(whichPi); err == nil {
				for _, match := range piBinRefRe.FindAllString(string(raw), -1) {
					if filepath.Base(match) == "pi" {
						candidates = append(candidates, filepath.Join(filepath.Dir(filepath.Dir(match)), "@earendil-works/pi-coding-agent/dist/index.js"))
					} else {
						candidates = append(candidates, filepath.Join(match, "dist/index.js"))
					}
				}
			}
		}
	}
	home, _ := os.UserHomeDir()
	candidates = append(candidates, filepath.Join(home, ".local/share/pi-fork/node_modules/@earendil-works/pi-coding-agent/dist/index.js"))
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

func verifyExtensionsLoad(stageAgent, agentDirsRoot string) error {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		return nil
	}
	pkgIndex := findPiCodingAgentPkg()
	if pkgIndex == "" {
		return nil
	}
	const script = `
import { pathToFileURL } from "node:url";
import fs from "node:fs";
import path from "node:path";

const pkgIndex = process.argv[1];
const agentDir = process.argv[2];
const m = await import(pathToFileURL(pkgIndex).href);
const paths = [];
const extDir = path.join(agentDir, "extensions");
if (fs.existsSync(extDir)) {
  for (const e of fs.readdirSync(extDir, { withFileTypes: true })) {
    const p = path.join(extDir, e.name);
    if (e.name.endsWith(".ts") || e.name.endsWith(".js")) {
      paths.push(p);
    } else if (e.isDirectory() && (
      fs.existsSync(path.join(p, "index.ts")) ||
      fs.existsSync(path.join(p, "index.js")) ||
      fs.existsSync(path.join(p, "package.json"))
    )) {
      paths.push(p);
    }
  }
}
const patchedDir = path.join(agentDir, "yolo-patched");
if (fs.existsSync(patchedDir)) {
  for (const e of fs.readdirSync(patchedDir, { withFileTypes: true })) {
    if (e.isDirectory()) paths.push(path.join(patchedDir, e.name));
  }
}
const loader = new m.DefaultResourceLoader({
  cwd: agentDir,
  agentDir,
  noExtensions: true,
  noSkills: true,
  noPromptTemplates: true,
  noThemes: true,
  noContextFiles: true,
  additionalExtensionPaths: paths,
});
await loader.reload();
const res = loader.getExtensions();
if (res.errors && res.errors.length > 0) {
  console.error(JSON.stringify(res.errors));
  process.exit(1);
}
process.exit(0);
`
	env := append(os.Environ(), "PI_INSPECTOR=0")
	if os.Getenv("NODE_COMPILE_CACHE") == "" {
		compileCache := filepath.Join(agentDirsRoot, ".cache", "node-compile")
		if err := os.MkdirAll(compileCache, 0o755); err != nil {
			return err
		}
		env = append(env, "NODE_COMPILE_CACHE="+compileCache)
	}
	cmd := exec.Command(nodeBin, "--input-type=module", "-e", script, pkgIndex, stageAgent)
	cmd.Env = env
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(errBuf.String())
		if detail == "" {
			detail = strings.TrimSpace(outBuf.String())
		}
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("DefaultResourceLoader verification failed: %s", detail)
	}
	return nil
}

func readActiveKey(agentDirsRoot string) string {
	raw, err := os.ReadFile(filepath.Join(agentDirsRoot, "current.json"))
	if err != nil {
		return ""
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return ""
	}
	k, _ := data["key"].(string)
	return k
}

func writeOKState(agentDirsRoot, key, keyDir string) error {
	agentDir := filepath.Join(keyDir, "agent")
	if err := atomicWriteJSON(filepath.Join(agentDirsRoot, "current.json"), map[string]any{
		"key":       key,
		"agent_dir": agentDir,
		"dir":       keyDir,
	}); err != nil {
		return err
	}
	return atomicWriteJSON(filepath.Join(agentDirsRoot, "status.json"), map[string]any{
		"state":      "ok",
		"condition":  "",
		"active_key": key,
		"target_key": key,
		"mover":      "",
		"detail":     "",
	})
}

func writeHeldState(agentDirsRoot, targetKey, detail string) error {
	activeKey := readActiveKey(agentDirsRoot)
	return atomicWriteJSON(filepath.Join(agentDirsRoot, "status.json"), map[string]any{
		"state":      "held",
		"condition":  buildCondition,
		"active_key": activeKey,
		"target_key": targetKey,
		"mover":      buildMover,
		"detail":     detail,
	})
}
