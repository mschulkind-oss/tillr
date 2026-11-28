// Package packmirror renders the tillr pack mirror and rewrites README links that escape it.
package packmirror

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	Repository                       = "https://github.com/mschulkind-oss/tillr/blob/main/"
	MirrorRoot                       = "yolo-packs/tillr/files/local/share/tillr"
	LoopModule                       = "tillr-loop"
	WorkflowsDir                     = "config/tillr/workflows"
	WorkflowsMount                   = "workflows"
	SupportRevisionRelPath           = "tools/tillr/buildstamp/support-revision"
	SupportRevisionBuildstampRelPath = "tillr/buildstamp/support-revision"
	WebSourceDir                     = "tools/tillr-view/web"
	WebDistRel                       = "web/dist"
	WebDistDigestRel                 = "web/dist-source-digest"
)

// BuildOutputRe matches Python bytecode caches that are build outputs rather than source.
var BuildOutputRe = regexp.MustCompile(`(?:^|/)__pycache__/|\.pyc$`)

var (
	// LoopAdapter is the relative path to the standalone loop adapter executable.
	LoopAdapter = filepath.Join(LoopModule, LoopModule)

	// Shipped lists all shipped modules, including shared libraries and source-only modules.
	Shipped = []string{
		"tillr", "tillr-cli", "tillr-drain", "tillr-gate", "tillr-item", "tillr-land",
		"tillr-run", "tillr-store", "tillr-view", "tillr-watch", "tillr-trace", "tillr-roll",
	}

	linkRe     = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	absoluteRe = regexp.MustCompile(`^(?:[a-z][a-z0-9+.-]*:|/|#)`)
)

const usageText = `Render the tillr pack mirror and rewrite README links that escape it.

Usage: pack-mirror.py --sync <repo-root> [<module> ...]
       pack-mirror-links.py <module-root>
`

// Rewrite rewrites relative links in one README's text that escape the module root.
func Rewrite(text, directory string) string {
	dirSlash := filepath.ToSlash(directory)
	return linkRe.ReplaceAllStringFunc(text, func(match string) string {
		// match is "](target)"
		target := match[2 : len(match)-1]
		if absoluteRe.MatchString(target) {
			return match
		}
		p, anchor, hasHash := strings.Cut(target, "#")
		resolved := pathpkg.Clean(pathpkg.Join(dirSlash, p))
		if !strings.HasPrefix(resolved, "..") {
			return match
		}
		for strings.HasPrefix(resolved, "../") {
			resolved = resolved[3:]
		}
		suffix := ""
		if hasHash && anchor != "" {
			suffix = "#" + anchor
		}
		return "](" + Repository + resolved + suffix + ")"
	})
}

// RewriteReadmes walks root and rewrites escaping links in every README.md.
func RewriteReadmes(root string, stdout io.Writer) error {
	rewritten := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Name() != "README.md" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		before := string(raw)
		relDir, err := filepath.Rel(root, filepath.Dir(p))
		if err != nil {
			return err
		}
		after := Rewrite(before, relDir)
		if after != before {
			if err := os.WriteFile(p, []byte(after), 0o644); err != nil {
				return err
			}
			rewritten++
		}
		return nil
	})
	if err != nil {
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "pack-mirror-links: rewrote %d README(s) under %s\n", rewritten, root)
	}
	return nil
}

// CheckDestination refuses any symlinked ancestor between dest and root.
func CheckDestination(root, dest string) error {
	for parent := filepath.Dir(dest); parent != root; parent = filepath.Dir(parent) {
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		fi, err := os.Lstat(parent)
		if err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlinked destination ancestor: %s", parent)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return err
	}
	_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	return nil
}

// MirrorLoopAdapter mirrors the tools/tillr-loop module (except README.md, owned by pack-docs) into the pack.
func MirrorLoopAdapter(root string, stdout io.Writer) error {
	sourceRel := "tools/" + LoopModule
	dest := filepath.Join(root, filepath.FromSlash(MirrorRoot), LoopModule)
	if err := CheckDestination(root, dest); err != nil {
		return err
	}

	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", sourceRel)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return err
	}

	type liveEntry struct {
		rel string
		src string
	}
	readmePath := sourceRel + "/README.md"
	var live []liveEntry
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		p := string(raw)
		if p == readmePath {
			continue
		}
		src := filepath.Join(root, filepath.FromSlash(p))
		if _, err := os.Stat(src); err == nil {
			live = append(live, liveEntry{rel: p, src: src})
		}
	}

	if fi, err := os.Lstat(dest); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			if err := os.Remove(dest); err != nil {
				return err
			}
		} else {
			entries, err := os.ReadDir(dest)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.Name() == "README.md" {
					continue
				}
				entryPath := filepath.Join(dest, entry.Name())
				efi, err := os.Lstat(entryPath)
				if err != nil {
					return err
				}
				if efi.Mode()&os.ModeSymlink != 0 || !efi.IsDir() {
					if err := os.Remove(entryPath); err != nil {
						return err
					}
				} else {
					if err := os.RemoveAll(entryPath); err != nil {
						return err
					}
				}
			}
		}
	}

	if len(live) == 0 {
		return nil
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	prefix := sourceRel + "/"
	for _, entry := range live {
		relFromSource := strings.TrimPrefix(entry.rel, prefix)
		dst := filepath.Join(dest, filepath.FromSlash(relFromSource))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(entry.src, dst); err != nil {
			return err
		}
	}
	if stdout != nil {
		relDst, _ := filepath.Rel(root, dest)
		fmt.Fprintf(stdout, "pack-sync: mirrored %s -> %s\n", sourceRel, filepath.ToSlash(relDst))
	}
	return nil
}

// MirrorModule mirrors one tools/<module> directory into the pack.
func MirrorModule(root, module string, stdout io.Writer) error {
	sourceRel := "tools/" + module
	dest := filepath.Join(root, filepath.FromSlash(MirrorRoot), module)
	if err := CheckDestination(root, dest); err != nil {
		return err
	}

	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", sourceRel)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return err
	}

	type liveEntry struct {
		rel string
		src string
	}
	var live []liveEntry
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		p := string(raw)
		src := filepath.Join(root, filepath.FromSlash(p))
		if _, err := os.Stat(src); err == nil {
			live = append(live, liveEntry{rel: p, src: src})
		}
	}

	var preservedDistDir string
	var wantWebDigest string
	if module == "tillr-view" && hasWebSource(root) {
		var derr error
		wantWebDigest, derr = ComputeWebSourceDigest(root)
		if derr != nil {
			return derr
		}
		if existingDistMatches(dest, wantWebDigest) {
			tmpDir, terr := os.MkdirTemp("", "tillr-packmirror-web-*")
			if terr != nil {
				return terr
			}
			defer os.RemoveAll(tmpDir)
			if cerr := copyTree(filepath.Join(dest, filepath.FromSlash(WebDistRel)), filepath.Join(tmpDir, "dist")); cerr != nil {
				return cerr
			}
			preservedDistDir = filepath.Join(tmpDir, "dist")
		}
	}

	if fi, err := os.Lstat(dest); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			if err := os.Remove(dest); err != nil {
				return err
			}
		} else {
			if err := os.RemoveAll(dest); err != nil {
				return err
			}
		}
	}

	if len(live) == 0 {
		return nil
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	prefix := sourceRel + "/"
	for _, entry := range live {
		relFromSource := strings.TrimPrefix(entry.rel, prefix)
		dst := filepath.Join(dest, filepath.FromSlash(relFromSource))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(entry.src, dst); err != nil {
			return err
		}
	}
	if module == "tillr-view" && wantWebDigest != "" {
		if err := syncWebDist(root, dest, wantWebDigest, preservedDistDir, stdout); err != nil {
			return err
		}
	}
	if err := RewriteReadmes(dest, stdout); err != nil {
		return err
	}
	if stdout != nil {
		relDst, _ := filepath.Rel(root, dest)
		fmt.Fprintf(stdout, "pack-sync: mirrored %s -> %s\n", sourceRel, filepath.ToSlash(relDst))
	}
	return nil
}

// IsGeneratedWebDistPath reports whether a path relative to tillr-view/ belongs to the
// generated observatory front-end bundle or its source-digest stamp.
func IsGeneratedWebDistPath(rel string) bool {
	p := strings.TrimPrefix(filepath.ToSlash(rel), "./")
	return p == WebDistDigestRel || p == WebDistRel || strings.HasPrefix(p, WebDistRel+"/")
}

// IsWebBuildInput reports whether a path relative to tools/tillr-view/web/ contributes to
// the built observatory front end.
func IsWebBuildInput(rel string) bool {
	p := strings.TrimPrefix(filepath.ToSlash(rel), "./")
	switch p {
	case "index.html", "package.json", "package-lock.json", "tsconfig.json", "vite.config.ts":
		return true
	}
	return strings.HasPrefix(p, "src/")
}

func hasWebSource(root string) bool {
	fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(WebSourceDir), "package.json"))
	return err == nil && fi.Mode().IsRegular()
}

// ComputeWebSourceDigest computes the deterministic SHA-256 digest ("sha256:<64-hex>")
// over sorted (relative_path, sha256(file_bytes)) entries of the observatory front-end
// build inputs under tools/tillr-view/web/.
func ComputeWebSourceDigest(root string) (string, error) {
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", WebSourceDir)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("list observatory web sources under %s: %w", root, err)
	}
	type entry struct {
		rel    string
		digest string
	}
	prefix := WebSourceDir + "/"
	var entries []entry
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		repoRel := string(raw)
		rel := strings.TrimPrefix(repoRel, prefix)
		if !IsWebBuildInput(rel) {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(repoRel))
		info, err := os.Stat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return "", fmt.Errorf("read observatory web source %s: %w", repoRel, err)
		}
		sum := sha256.Sum256(data)
		entries = append(entries, entry{
			rel:    rel,
			digest: hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].rel < entries[j].rel
	})
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%s\n", e.rel, e.digest)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func existingDistMatches(dest, wantDigest string) bool {
	indexPath := filepath.Join(dest, filepath.FromSlash(WebDistRel), "index.html")
	if fi, err := os.Stat(indexPath); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(WebDistDigestRel)))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(raw)) == wantDigest
}

func copyTree(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return copyFile(p, dst)
	})
}

func ensureWebModules(webDir string, stdout io.Writer) error {
	if _, err := exec.LookPath("node"); err != nil {
		return fmt.Errorf("building observatory front end requires node on PATH: %w", err)
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return fmt.Errorf("building observatory front end requires npm on PATH: %w", err)
	}
	platOut, _ := exec.Command("node", "-p",
		`process.platform + "-" + process.arch + (process.platform === "linux" ? (process.report.getReport().header.glibcVersionRuntime ? "-gnu" : "-musl") : "")`,
	).Output()
	platform := strings.TrimSpace(string(platOut))

	nmDir := filepath.Join(webDir, "node_modules")
	lockSrc := filepath.Join(webDir, "package-lock.json")
	lockInstalled := filepath.Join(nmDir, ".package-lock.json")
	stamp := filepath.Join(nmDir, ".tillr-platform")

	needCI := false
	if fi, err := os.Stat(nmDir); err != nil || !fi.IsDir() {
		needCI = true
	} else if srcInfo, err := os.Stat(lockSrc); err == nil {
		if dstInfo, derr := os.Stat(lockInstalled); derr != nil || srcInfo.ModTime().After(dstInfo.ModTime()) {
			needCI = true
		}
	}
	if !needCI && platform != "" {
		raw, err := os.ReadFile(stamp)
		if err != nil || strings.TrimSpace(string(raw)) != platform {
			needCI = true
		}
	}
	if !needCI {
		return nil
	}
	cmd := exec.Command("npm", "--prefix", webDir, "ci", "--no-audit", "--no-fund")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("npm ci in %s failed: %w\n%s", webDir, err, strings.TrimSpace(string(out)))
	}
	if platform != "" {
		_ = os.WriteFile(stamp, []byte(platform+"\n"), 0o644)
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "pack-sync: installed observatory web dependencies in %s\n", webDir)
	}
	return nil
}

func syncWebDist(root, dest, wantDigest, preservedDistDir string, stdout io.Writer) error {
	dstDist := filepath.Join(dest, filepath.FromSlash(WebDistRel))
	dstDigest := filepath.Join(dest, filepath.FromSlash(WebDistDigestRel))

	if preservedDistDir != "" {
		if err := copyTree(preservedDistDir, dstDist); err != nil {
			return err
		}
		return os.WriteFile(dstDigest, []byte(wantDigest+"\n"), 0o644)
	}

	webDir := filepath.Join(root, filepath.FromSlash(WebSourceDir))
	if err := ensureWebModules(webDir, stdout); err != nil {
		return err
	}
	cmd := exec.Command("npm", "--prefix", webDir, "run", "build")
	cmd.Env = append(os.Environ(), "TILLR_VIEW_PAGE_BUILD="+wantDigest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("npm run build in %s failed: %w\n%s", webDir, err, strings.TrimSpace(string(out)))
	}
	srcDist := filepath.Join(webDir, "dist")
	if fi, err := os.Stat(filepath.Join(srcDist, "index.html")); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("observatory front-end build produced no %s", filepath.Join(srcDist, "index.html"))
	}
	if err := copyTree(srcDist, dstDist); err != nil {
		return err
	}
	if err := os.WriteFile(dstDigest, []byte(wantDigest+"\n"), 0o644); err != nil {
		return err
	}
	if stdout != nil {
		relDst, _ := filepath.Rel(root, dstDist)
		fmt.Fprintf(stdout, "pack-sync: built observatory front end -> %s\n", filepath.ToSlash(relDst))
	}
	return nil
}

// MirrorWorkflows mirrors config/tillr/workflows into the pack's workflows directory.
func MirrorWorkflows(root string, stdout io.Writer) error {
	dest := filepath.Join(root, filepath.FromSlash(MirrorRoot), WorkflowsMount)
	if err := CheckDestination(root, dest); err != nil {
		return err
	}

	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", WorkflowsDir)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return err
	}

	type liveEntry struct {
		rel string
		src string
	}
	var live []liveEntry
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		p := string(raw)
		src := filepath.Join(root, filepath.FromSlash(p))
		if _, err := os.Stat(src); err == nil {
			live = append(live, liveEntry{rel: p, src: src})
		}
	}

	if fi, err := os.Lstat(dest); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			if err := os.Remove(dest); err != nil {
				return err
			}
		} else {
			if err := os.RemoveAll(dest); err != nil {
				return err
			}
		}
	}

	if len(live) == 0 {
		return nil
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	prefix := WorkflowsDir + "/"
	for _, entry := range live {
		relFromSource := strings.TrimPrefix(entry.rel, prefix)
		dst := filepath.Join(dest, filepath.FromSlash(relFromSource))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(entry.src, dst); err != nil {
			return err
		}
	}
	if stdout != nil {
		relDst, _ := filepath.Rel(root, dest)
		fmt.Fprintf(stdout, "pack-sync: mirrored %s -> %s\n", WorkflowsDir, filepath.ToSlash(relDst))
	}
	return nil
}

func isShipped(module string) bool {
	if module == LoopModule || module == WorkflowsMount {
		return true
	}
	for _, m := range Shipped {
		if m == module {
			return true
		}
	}
	return false
}

// SupportFileLogicalPath maps a repo-relative path to its install-root-relative
// logical support-file path, returning ("", false) when the path is not a
// runtime support file.
func SupportFileLogicalPath(repoRel string) (string, bool) {
	p := strings.TrimPrefix(filepath.ToSlash(repoRel), "./")
	if p == "" || BuildOutputRe.MatchString(p) {
		return "", false
	}
	if rest, ok := strings.CutPrefix(p, "tools/tillr-run/backends/"); ok {
		if rest == "" || rest == "README.md" || strings.HasSuffix(rest, "/README.md") {
			return "", false
		}
		if strings.HasPrefix(rest, "testdata/") || strings.Contains(rest, "/testdata/") {
			return "", false
		}
		return "tillr-run/backends/" + rest, true
	}
	if p == "tools/tillr-trace/bash-shim.sh" {
		return "tillr-trace/bash-shim.sh", true
	}
	if rest, ok := strings.CutPrefix(p, "tools/tillr-gate/"); ok {
		if rest != "" && !strings.Contains(rest, "/") && strings.HasSuffix(rest, ".py") && !strings.HasSuffix(rest, ".test.py") {
			return "tillr-gate/" + rest, true
		}
		return "", false
	}
	if rest, ok := strings.CutPrefix(p, WorkflowsDir+"/"); ok {
		if rest != "" && !strings.Contains(rest, "/") && strings.HasSuffix(rest, ".json") {
			return WorkflowsMount + "/" + rest, true
		}
		return "", false
	}
	return "", false
}

// IsSupportFile reports whether a repo-relative path is a runtime support file
// included in SupportRevision.
func IsSupportFile(repoRel string) bool {
	_, ok := SupportFileLogicalPath(repoRel)
	return ok
}

// ComputeSupportRevision computes the deterministic SHA-256 digest
// ("sha256:<64-hex>") over sorted (logical_path, sha256(file_bytes)) entries of
// tillr's runtime support files under root.
func ComputeSupportRevision(root string) (string, error) {
	cmd := exec.Command(
		"git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--",
		"tools/tillr-run/backends",
		"tools/tillr-trace/bash-shim.sh",
		"tools/tillr-gate",
		WorkflowsDir,
	)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("list support files under %s: %w", root, err)
	}

	type entry struct {
		logical string
		digest  string
	}
	var entries []entry
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		repoRel := string(raw)
		logical, ok := SupportFileLogicalPath(repoRel)
		if !ok {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(repoRel))
		info, err := os.Stat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return "", fmt.Errorf("read support file %s: %w", repoRel, err)
		}
		sum := sha256.Sum256(data)
		entries = append(entries, entry{
			logical: logical,
			digest:  hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].logical < entries[j].logical
	})

	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%s\n", e.logical, e.digest)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// WriteSupportRevision computes SupportRevision for root and writes it to
// tools/tillr/buildstamp/support-revision.
func WriteSupportRevision(root string) (string, error) {
	rev, err := ComputeSupportRevision(root)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(root, filepath.FromSlash(SupportRevisionRelPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	want := rev + "\n"
	if existing, err := os.ReadFile(dst); err == nil && string(existing) == want {
		return rev, nil
	}
	if err := os.WriteFile(dst, []byte(want), 0o644); err != nil {
		return "", err
	}
	return rev, nil
}

// Sync mirrors the requested modules (or all shipped modules if none specified) under root.
func Sync(root string, modules []string, stdout, stderr io.Writer) int {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(stderr, "pack-sync: %v\n", err)
		return 1
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}

	if len(modules) == 0 {
		modules = append(append([]string(nil), Shipped...), LoopModule, WorkflowsMount)
	}
	for _, m := range modules {
		if !isShipped(m) {
			fmt.Fprintf(stderr, "pack-sync: unknown module: %s\n", m)
			return 2
		}
	}
	for _, m := range modules {
		var err error
		switch m {
		case LoopModule:
			err = MirrorLoopAdapter(absRoot, stdout)
		case WorkflowsMount:
			err = MirrorWorkflows(absRoot, stdout)
		default:
			if m == "tillr" {
				if _, revErr := WriteSupportRevision(absRoot); revErr != nil {
					fmt.Fprintf(stderr, "pack-sync: %v\n", revErr)
					return 1
				}
			}
			err = MirrorModule(absRoot, m, stdout)
		}
		if err != nil {
			fmt.Fprintf(stderr, "pack-sync: %v\n", err)
			return 1
		}
	}
	return 0
}

// Main is the CLI entrypoint for pack-mirror and pack-mirror-links.
// args should be os.Args[1:].
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) >= 2 && args[0] == "--sync" {
		return Sync(args[1], args[2:], stdout, stderr)
	}
	if len(args) != 1 || args[0] == "--sync" {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	if err := RewriteReadmes(args[0], stdout); err != nil {
		fmt.Fprintf(stderr, "pack-mirror-links: %v\n", err)
		return 1
	}
	return 0
}
