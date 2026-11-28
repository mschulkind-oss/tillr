// Package packsync checks the tillr pack's module mirror and documentation snapshot.
package packsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/packdocs"
	"github.com/mschulkind-oss/tillr/packmirror"
)

// Modules lists every module that ships, in the order the pack's `files` tree carries them.
var Modules = []string{
	"tillr",
	"tillr-cli",
	"tillr-drain",
	"tillr-gate",
	"tillr-item",
	"tillr-land",
	"tillr-run",
	"tillr-store",
	"tillr-view",
	"tillr-watch",
	"tillr-trace",
	"tillr-roll",
}

// SourceOnlyModules lists modules that ship without a PATH launcher.
var SourceOnlyModules = []string{"tillr", "tillr-cli", "tillr-roll"}

// ProgramModules maps PATH binary name to the module that builds it.
var ProgramModules = buildProgramModules()

func buildProgramModules() map[string]string {
	m := map[string]string{
		"tillr":      "tillr-cli",
		"tillr-loop": "tillr-loop",
	}
	sourceOnly := make(map[string]bool, len(SourceOnlyModules))
	for _, s := range SourceOnlyModules {
		sourceOnly[s] = true
	}
	for _, mod := range Modules {
		if !sourceOnly[mod] {
			m[mod] = mod
		}
	}
	return m
}

// BuildOutputRe matches Python bytecode caches that are build outputs rather than source.
var BuildOutputRe = regexp.MustCompile(`(?:^|/)__pycache__/|\.pyc$`)

var (
	launcherHeaderRe  = regexp.MustCompile(`^# \S+ -- the launcher the tillr pack`)
	launcherAssignRe  = regexp.MustCompile(`^(bin|mod)=`)
	declaredProgramRe = regexp.MustCompile(`"kind":\s*"program",\s*"bin":\s*"([^"]+)"`)
)

func packDir(root string) string {
	return filepath.Join(root, "yolo-packs", "tillr")
}

func copyDir(root string) string {
	return filepath.Join(packDir(root), "files", "local", "share", "tillr")
}

func programsDir(root string) string {
	return filepath.Join(packDir(root), "programs")
}

func ok(stdout io.Writer, msg string) {
	if stdout != nil {
		fmt.Fprintf(stdout, "ok   %s\n", msg)
	}
}

func committable(root, module string) (map[string]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "tools/"+module)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	found := make(map[string]string)
	prefix := "tools/" + module + "/"
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		rel := strings.TrimPrefix(string(entry), prefix)
		if BuildOutputRe.MatchString(rel) {
			continue
		}
		found[rel] = filepath.Join(root, "tools", module, filepath.FromSlash(rel))
	}
	return found, nil
}

func files(root string) (map[string]string, error) {
	found := make(map[string]string)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if BuildOutputRe.MatchString(relSlash) {
			return nil
		}
		found[relSlash] = p
		return nil
	})
	return found, err
}

func executable(p string) (bool, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return false, err
	}
	return fi.Mode()&0o111 != 0, nil
}

func isSymlink(p string) (bool, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return false, err
	}
	return fi.Mode()&os.ModeSymlink != 0, nil
}

func mirroredBytes(relative string, data []byte) []byte {
	if !strings.HasSuffix(relative, "README.md") {
		return data
	}
	return []byte(packmirror.Rewrite(string(data), pathpkg.Dir(relative)))
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func formatCommas(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		b.WriteByte(',')
	}
	for i := rem; i < len(s); i += 3 {
		if i > rem {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// CaseTreeMatchesSource checks each module's copy against its source: present, complete, and nothing extra.
func CaseTreeMatchesSource(root string, candidate bool, stdout io.Writer) error {
	if candidate {
		if stdout != nil {
			fmt.Fprintln(stdout, "n/a  the tillr pack's tree: a candidate does not carry the generated tree; the landing runs this case strictly")
		}
		return nil
	}
	copyRoot := copyDir(root)
	if fi, err := os.Stat(copyRoot); err != nil || !fi.IsDir() {
		return fmt.Errorf("missing the pack's copy: %s\nrun `just pack-sync` to generate it", copyRoot)
	}
	checked := 0
	for _, module := range Modules {
		source := filepath.Join(root, "tools", module)
		copyMod := filepath.Join(copyRoot, module)
		if fi, err := os.Stat(source); err != nil || !fi.IsDir() {
			return fmt.Errorf("missing the canonical source tree: %s", source)
		}
		if fi, err := os.Stat(copyMod); err != nil || !fi.IsDir() {
			return fmt.Errorf("missing the pack's copy of %s: %s\nrun `just pack-sync`", module, copyMod)
		}
		sourceFiles, err := committable(root, module)
		if err != nil {
			return err
		}
		copyFiles, err := files(copyMod)
		if err != nil {
			return err
		}
		var missing, extra []string
		for k := range sourceFiles {
			if _, ok := copyFiles[k]; !ok {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			limit := len(missing)
			ellipsis := ""
			if limit > 5 {
				limit = 5
				ellipsis = " ..."
			}
			return fmt.Errorf("the pack's copy of %s is missing %d file(s): %s%s\nrun `just pack-sync`",
				module, len(missing), strings.Join(missing[:limit], ", "), ellipsis)
		}
		for k := range copyFiles {
			if module == "tillr-view" && packmirror.IsGeneratedWebDistPath(k) {
				continue
			}
			if _, ok := sourceFiles[k]; !ok {
				extra = append(extra, k)
			}
		}
		sort.Strings(extra)
		if len(extra) > 0 {
			limit := len(extra)
			ellipsis := ""
			if limit > 5 {
				limit = 5
				ellipsis = " ..."
			}
			return fmt.Errorf("the pack's copy of %s carries %d file(s) the source does not have: %s%s\nrun `just pack-sync` (it mirrors the tree, so a stray file is a stale copy)",
				module, len(extra), strings.Join(extra[:limit], ", "), ellipsis)
		}
		checked += len(sourceFiles)
	}
	ok(stdout, fmt.Sprintf("every module's copy matches its source's file set (%d modules, %d files)", len(Modules), checked))
	return nil
}

// CaseBytes checks every copied file against its source byte for byte, with the README rewrite.
func CaseBytes(root string, candidate bool, stdout io.Writer) error {
	if candidate {
		if stdout != nil {
			fmt.Fprintln(stdout, "n/a  the tillr pack's bytes: a candidate does not carry the generated tree")
		}
		return nil
	}
	copyRoot := copyDir(root)
	for _, module := range Modules {
		copyMod := filepath.Join(copyRoot, module)
		sourceFiles, err := committable(root, module)
		if err != nil {
			return err
		}
		for _, relative := range sortedKeys(sourceFiles) {
			sourceBytes, err := os.ReadFile(sourceFiles[relative])
			if err != nil {
				return err
			}
			expected := mirroredBytes(relative, sourceBytes)
			actual, err := os.ReadFile(filepath.Join(copyMod, filepath.FromSlash(relative)))
			if err != nil {
				return err
			}
			if !bytes.Equal(expected, actual) {
				return fmt.Errorf("the pack's copy of %s/%s diverges from its source: run `just pack-sync`", module, relative)
			}
		}
	}
	ok(stdout, "every copied file is byte-identical to its source, with the README rewrite")
	return nil
}

// CaseModes checks that exec bits survive the mirror and no copied file is a symlink.
func CaseModes(root string, candidate bool, stdout io.Writer) error {
	if candidate {
		if stdout != nil {
			fmt.Fprintln(stdout, "n/a  the tillr pack's modes: a candidate does not carry the generated tree")
		}
		return nil
	}
	copyRoot := copyDir(root)
	for _, module := range Modules {
		copyMod := filepath.Join(copyRoot, module)
		sourceFiles, err := committable(root, module)
		if err != nil {
			return err
		}
		for _, relative := range sortedKeys(sourceFiles) {
			copyPath := filepath.Join(copyMod, filepath.FromSlash(relative))
			sym, err := isSymlink(copyPath)
			if err != nil {
				return err
			}
			if sym {
				return fmt.Errorf("the pack's copy of %s/%s is a symlink; the committed tree must be self-contained", module, relative)
			}
			srcExec, err := executable(sourceFiles[relative])
			if err != nil {
				return err
			}
			dstExec, err := executable(copyPath)
			if err != nil {
				return err
			}
			if srcExec != dstExec {
				return fmt.Errorf("the pack's copy of %s/%s has a different exec bit from the source: run `just pack-sync`", module, relative)
			}
		}
	}
	ok(stdout, "every copied file keeps its source's exec bit and is a regular file")
	return nil
}

// CaseLoopAdapter checks that the pack carries the tillr-loop Go module source (except README.md) and no Python script.
func CaseLoopAdapter(root string, candidate bool, stdout io.Writer) error {
	if candidate {
		if stdout != nil {
			fmt.Fprintln(stdout, "n/a  the loop adapter: a candidate does not carry the generated tree")
		}
		return nil
	}
	oldScript := filepath.Join(copyDir(root), "tillr-loop", "tillr-loop")
	if _, err := os.Lstat(oldScript); err == nil {
		return fmt.Errorf("the pack still carries the removed Python script at %s: run `just pack-sync`", oldScript)
	}
	allWant, err := committable(root, "tillr-loop")
	if err != nil {
		return err
	}
	want := make(map[string]bool, len(allWant))
	for rel := range allWant {
		if rel != "README.md" {
			want[rel] = true
		}
	}
	got := make(map[string]bool)
	loopCopy := filepath.Join(copyDir(root), "tillr-loop")
	if info, err := os.Stat(loopCopy); err == nil && info.IsDir() {
		if err := filepath.WalkDir(loopCopy, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(loopCopy, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if rel != "README.md" {
				got[rel] = true
			}
			return nil
		}); err != nil {
			return err
		}
	}
	var missing, extra []string
	for rel := range want {
		if !got[rel] {
			missing = append(missing, rel)
		}
	}
	for rel := range got {
		if !want[rel] {
			extra = append(extra, rel)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		return fmt.Errorf("the pack's tillr-loop module diverges from tools/tillr-loop/: missing=%v, extra=%v: run `just pack-sync`", missing, extra)
	}
	sortedWant := make([]string, 0, len(want))
	for rel := range want {
		sortedWant = append(sortedWant, rel)
	}
	sort.Strings(sortedWant)
	for _, rel := range sortedWant {
		src := filepath.Join(root, "tools", "tillr-loop", filepath.FromSlash(rel))
		dst := filepath.Join(loopCopy, filepath.FromSlash(rel))
		sym, symErr := isSymlink(dst)
		fi, statErr := os.Stat(dst)
		if statErr != nil || !fi.Mode().IsRegular() || symErr != nil || sym {
			return fmt.Errorf("%s must be a regular file", dst)
		}
		srcBytes, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		dstBytes, err := os.ReadFile(dst)
		if err != nil {
			return err
		}
		if !bytes.Equal(dstBytes, srcBytes) {
			return fmt.Errorf("the pack's copy of tillr-loop/%s diverges from its source: run `just pack-sync`", rel)
		}
	}
	ok(stdout, "the pack delivers the tillr-loop Go module source without the old Python script")
	return nil
}

// CaseWorkflows checks that the pack's workflows directory matches config/tillr/workflows byte for byte.
func CaseWorkflows(root string, candidate bool, stdout io.Writer) error {
	if candidate {
		if stdout != nil {
			fmt.Fprintln(stdout, "n/a  the tillr pack's workflows: a candidate does not carry the generated tree")
		}
		return nil
	}
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", packmirror.WorkflowsDir)
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	want := make(map[string]string)
	prefix := packmirror.WorkflowsDir + "/"
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		p := string(entry)
		rel := strings.TrimPrefix(p, prefix)
		src := filepath.Join(root, filepath.FromSlash(p))
		if _, err := os.Stat(src); err == nil {
			want[rel] = src
		}
	}
	wfCopy := filepath.Join(copyDir(root), packmirror.WorkflowsMount)
	got := make(map[string]string)
	if info, err := os.Stat(wfCopy); err == nil && info.IsDir() {
		var err error
		got, err = files(wfCopy)
		if err != nil {
			return err
		}
	}
	var missing, extra []string
	for rel := range want {
		if _, ok := got[rel]; !ok {
			missing = append(missing, rel)
		}
	}
	for rel := range got {
		if _, ok := want[rel]; !ok {
			extra = append(extra, rel)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		return fmt.Errorf("the pack's workflows diverge from %s/: missing=%v, extra=%v: run `just pack-sync`", packmirror.WorkflowsDir, missing, extra)
	}
	for _, rel := range sortedKeys(want) {
		dst := got[rel]
		sym, symErr := isSymlink(dst)
		fi, statErr := os.Stat(dst)
		if statErr != nil || !fi.Mode().IsRegular() || symErr != nil || sym {
			return fmt.Errorf("%s must be a regular file", dst)
		}
		srcBytes, err := os.ReadFile(want[rel])
		if err != nil {
			return err
		}
		dstBytes, err := os.ReadFile(dst)
		if err != nil {
			return err
		}
		if !bytes.Equal(dstBytes, srcBytes) {
			return fmt.Errorf("the pack's copy of workflows/%s diverges from %s/%s: run `just pack-sync`", rel, packmirror.WorkflowsDir, rel)
		}
	}
	ok(stdout, fmt.Sprintf("the pack delivers %d workflow(s) matching %s/", len(want), packmirror.WorkflowsDir))
	return nil
}

// CaseSupportRevision verifies that tools/tillr/buildstamp/support-revision matches
// the live SupportRevision of the checkout and matches the pack mirror byte-for-byte.
func CaseSupportRevision(root string, candidate bool, stdout io.Writer) error {
	if candidate {
		if stdout != nil {
			fmt.Fprintln(stdout, "n/a  the tillr pack's support-revision: a candidate does not carry the generated tree")
		}
		return nil
	}
	want, err := packmirror.ComputeSupportRevision(root)
	if err != nil {
		return err
	}
	srcPath := filepath.Join(root, filepath.FromSlash(packmirror.SupportRevisionRelPath))
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("missing %s: run `just pack-sync`: %w", packmirror.SupportRevisionRelPath, err)
	}
	if strings.TrimSpace(string(srcBytes)) != want {
		return fmt.Errorf("%s is stale (%q != %q): run `just pack-sync`", packmirror.SupportRevisionRelPath, strings.TrimSpace(string(srcBytes)), want)
	}
	copyPath := filepath.Join(copyDir(root), filepath.FromSlash(packmirror.SupportRevisionBuildstampRelPath))
	copyBytes, err := os.ReadFile(copyPath)
	if err != nil {
		return fmt.Errorf("missing pack copy %s: run `just pack-sync`: %w", packmirror.SupportRevisionBuildstampRelPath, err)
	}
	if !bytes.Equal(srcBytes, copyBytes) {
		return fmt.Errorf("the pack's copy of %s diverges from its source: run `just pack-sync`", packmirror.SupportRevisionBuildstampRelPath)
	}
	ok(stdout, fmt.Sprintf("support-revision matches live support files and pack mirror (%s)", want))
	return nil
}

// CaseBuildOutputs checks that a Python bytecode cache is treated as a build output, and source files are not.
func CaseBuildOutputs(stdout io.Writer) error {
	for _, rel := range []string{"__pycache__/tillr_trace.cpython-314.pyc", "tillr_trace.pyc"} {
		if !BuildOutputRe.MatchString(rel) {
			return fmt.Errorf("%s is a build output and the source set must leave it out", rel)
		}
	}
	for _, rel := range []string{"tillr_trace.py", "README.md", "testdata/canary-run.log", "pycache-notes.md"} {
		if BuildOutputRe.MatchString(rel) {
			return fmt.Errorf("%s is source, and the source set must keep it", rel)
		}
	}
	ok(stdout, "a Python bytecode cache is a build output, and a source file is not")
	return nil
}

// CaseCopyBuildOutputs checks that a bytecode cache on the pack side is ignored while a stray source file is not.
func CaseCopyBuildOutputs(stdout io.Writer) error {
	dir, err := os.MkdirTemp("", "packsync-copy-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	if err := os.MkdirAll(filepath.Join(dir, "__pycache__"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "__pycache__", "x.cpython-314.pyc"), []byte{0}, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "kept.py"), []byte("x = 1\n"), 0o644); err != nil {
		return err
	}
	got, err := files(dir)
	if err != nil {
		return err
	}
	if len(got) != 1 || got["kept.py"] == "" {
		return fmt.Errorf("a __pycache__ file in the pack's copy must not be listed as a copied file")
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.py"), []byte("y = 2\n"), 0o644); err != nil {
		return err
	}
	got2, err := files(dir)
	if err != nil {
		return err
	}
	if len(got2) != 2 || got2["stray.py"] == "" {
		return fmt.Errorf("a stray source file in the pack's copy must still be listed, so it reads as drift")
	}
	ok(stdout, "a bytecode cache on the pack side is ignored, and a stray source file is not")
	return nil
}

// LauncherBody returns a launcher's text with the three lines that legitimately differ removed.
func LauncherBody(text string) string {
	var kept []string
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if launcherHeaderRe.MatchString(line) || launcherAssignRe.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// DeclaredPrograms returns the `bin` of every `program` contribution in the pack manifest.
func DeclaredPrograms(root string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(packDir(root), "pack.json"))
	if err != nil {
		return nil, err
	}
	matches := declaredProgramRe.FindAllStringSubmatch(string(raw), -1)
	bins := make([]string, 0, len(matches))
	for _, m := range matches {
		bins = append(bins, m[1])
	}
	return bins, nil
}

// CaseLaunchers checks that the launchers are one body, each naming the module that builds it.
func CaseLaunchers(root string, stdout io.Writer) error {
	bodies := make(map[string]string, len(ProgramModules))
	binNames := sortedKeys(ProgramModules)
	for _, binName := range binNames {
		module := ProgramModules[binName]
		p := filepath.Join(programsDir(root), binName, "run")
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() {
			return fmt.Errorf("missing launcher: %s", p)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		text := string(raw)
		if !strings.Contains(text, "\nbin="+binName+"\n") {
			return fmt.Errorf("%s does not set `bin=%s`; the delivered file is named for the binary it must run", p, binName)
		}
		if !strings.Contains(text, "\nmod="+module+"\n") {
			return fmt.Errorf("%s does not set `mod=%s`: %s is built from module %s", p, module, binName, module)
		}
		execBit, err := executable(p)
		if err != nil || !execBit {
			return fmt.Errorf("%s is not executable; yolo delivers the exec bit", p)
		}
		bodies[binName] = LauncherBody(text)
	}
	canonical := bodies["tillr"]
	for _, binName := range binNames {
		if bodies[binName] != canonical {
			return fmt.Errorf("the %s launcher's body has drifted from the tillr launcher's; the launchers are one script and only the header line and the two assignments differ", binName)
		}
	}
	declared, err := DeclaredPrograms(root)
	if err != nil {
		return err
	}
	sort.Strings(declared)
	if len(declared) != len(binNames) {
		return fmt.Errorf("the manifest's `program` entries and the launchers do not match: declared %v, launchers %v", declared, binNames)
	}
	for i := range declared {
		if declared[i] != binNames[i] {
			return fmt.Errorf("the manifest's `program` entries and the launchers do not match: declared %v, launchers %v", declared, binNames)
		}
	}
	ok(stdout, fmt.Sprintf("the launchers are one body, each naming its module, one per declared program (%d entries)", len(declared)))
	return nil
}

// CaseDocsTransform exercises the documentation transform on a specimen covering every rule.
func CaseDocsTransform(stdout io.Writer) error {
	source := "docs/design/a.md"
	delivered := map[string]bool{
		"docs/design/a.md":            true,
		"docs/design/b.md":            true,
		"docs/tillr/design-status.md": true,
	}
	text := strings.Join([]string{
		"---",
		"depends-on: [b.md]",
		"---",
		"",
		"# A",
		"",
		"See [b](./b.md#2-the-part), [index](../tillr/design-status.md), [gone](./c.md#x),",
		"[stage](../../tools/tillr-drain/drain/stage.go), [loop](../../tools/tillr-loop/README.md),",
		"[here](#local), [web](https://example.com/x.md) and [dir](../../tools/tillr-gate/).",
		"[ref]: ../roadmap.md",
		`<!-- tillr: item id="item-x" paths="docs/" blocked_by="" -->`,
		`   <!-- vantage: question id=OQ-A1 leaning="A" -->`,
		"```text",
		`<!-- tillr: item id="item-specimen" paths="docs/" blocked_by="" -->`,
		"[kept](./c.md)",
		"```",
		"",
	}, "\n")

	out := packdocs.Transform(text, source, delivered, "0123456789abcdef", "2026-11-16")
	lines := strings.Split(out, "\n")
	if lines[0] != "---" || lines[1] != "depends-on: [b.md]" || lines[2] != "---" {
		return fmt.Errorf("frontmatter must be untouched")
	}
	if lines[5] != "" || !strings.HasPrefix(lines[6], "> **Delivered copy.** This is `docs/design/a.md`") {
		return fmt.Errorf("the banner follows the first heading")
	}
	if strings.Count(out, "> **Delivered copy.**") != 1 {
		return fmt.Errorf("expected exactly one banner")
	}
	repo := packmirror.Repository
	for _, expected := range []string{
		"[b](b.md#2-the-part)",
		"[index](../tillr/design-status.md)",
		"[gone](" + repo + "docs/design/c.md#x)",
		"[stage](../../tillr-drain/drain/stage.go)",
		"[loop](" + repo + "tools/tillr-loop/README.md)",
		"[here](#local)",
		"[web](https://example.com/x.md)",
		"[dir](../../tillr-gate/)",
		"[ref]: " + repo + "docs/roadmap.md",
		`<!-- inert copy of a tillr directive: item id="item-x"`,
		`<!-- tillr: item id="item-specimen"`,
		"[kept](./c.md)",
		`   <!-- vantage: question id=OQ-A1 leaning="A" -->`,
	} {
		if !strings.Contains(out, expected) {
			return fmt.Errorf("the transform should have written %q:\n%s", expected, out)
		}
	}
	if live := packdocs.LiveDirectives(out); len(live) != 0 {
		return fmt.Errorf("expected no live directives in output, got %v", live)
	}
	if live := packdocs.LiveDirectives(text); len(live) == 0 {
		return fmt.Errorf("the specimen's own directive must be live")
	}
	deliveredWithLoop := map[string]bool{
		"docs/design/a.md":            true,
		"docs/design/b.md":            true,
		"docs/tillr/design-status.md": true,
		"tools/tillr-loop/README.md":  true,
	}
	back := packdocs.Transform("# L\n[a](../../docs/design/a.md)\n", "tools/tillr-loop/README.md",
		deliveredWithLoop, strings.Repeat("0", 40), "2026-11-16")
	if !strings.Contains(back, "[a](../docs/design/a.md)") {
		return fmt.Errorf("expected back-link [a](../docs/design/a.md), got:\n%s", back)
	}
	ok(stdout, "the documentation transform re-resolves links, makes item directives inert outside fences, leaves fences, frontmatter and questions alone, and adds one banner")
	return nil
}

// CaseDocsDeclaration checks the index declaration, MirroredModules parity, and tree_common.py DELIVERED_PREFIX parity.
func CaseDocsDeclaration(root string, stdout io.Writer) error {
	index := strings.Join([]string{
		"| Document | Status | Pack |",
		"| :--- | :--- | :--- |",
		"| [Item graphs](../reference/tillr-item-graphs.md) | built | shipped |",
		"| [Seams](../design/autoqa-tillr-seams.md#top) | superseded | not shipped |",
		"| [Gate](../../tools/tillr-gate/README.md) | built | shipped |",
		"| [Unsure](../design/unsure.md) | ? | maybe |",
		"| not a link | built | shipped |",
		"```",
		"| [Specimen](../design/specimen.md) | x | shipped |",
		"```",
	}, "\n")

	decl := packdocs.Declared(index, packdocs.Index)
	if len(decl) != 2 || decl[0] != "docs/reference/tillr-item-graphs.md" || decl[1] != "tools/tillr-gate/README.md" {
		return fmt.Errorf("unexpected Declared result: %v", decl)
	}
	if undecided := packdocs.UndecidedRows(index); len(undecided) != 1 {
		return fmt.Errorf("expected 1 undecided row, got %v", undecided)
	}
	if len(packdocs.MirroredModules) != len(Modules) {
		return fmt.Errorf("packdocs.MirroredModules and Modules disagree")
	}
	for i := range Modules {
		if packdocs.MirroredModules[i] != Modules[i] {
			return fmt.Errorf("packdocs.MirroredModules and Modules disagree at index %d", i)
		}
	}

	// The copy-to-source rule every path-keyed allowance follows: it inverts Destination,
	// and the gate's restatement of it (`tools/gate/repo-gate excused-as`) agrees on every shape.
	gateExcusedAs := func(path string) (string, error) {
		cmd := exec.Command(filepath.Join(root, "tools", "gate", "repo-gate"), "excused-as", path)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("repo-gate excused-as %q: %w", path, err)
		}
		return strings.TrimRight(string(out), "\n"), nil
	}
	relCopy, err := filepath.Rel(root, copyDir(root))
	if err != nil {
		return err
	}
	if packdocs.DeliveredPrefix != filepath.ToSlash(relCopy)+"/" {
		return fmt.Errorf("DeliveredPrefix %q != %q", packdocs.DeliveredPrefix, filepath.ToSlash(relCopy)+"/")
	}
	for _, src := range []string{
		"docs/design/a.md",
		"docs/tillr/design-status.md",
		"tools/tillr-loop/README.md",
		"tools/tillr-view/README.md",
		"tools/tillr-run/backends/pi/run-pi",
	} {
		c := packdocs.DeliveredPrefix + packdocs.Destination(src)
		if got := packdocs.SourceOf(c); got != src {
			return fmt.Errorf("SourceOf(%q) = %q, want %q", c, got, src)
		}
		gotGate, err := gateExcusedAs(c)
		if err != nil {
			return err
		}
		if gotGate != src {
			return fmt.Errorf("repo-gate excused-as %q = %q, want %q", c, gotGate, src)
		}
	}
	for _, outside := range []string{"docs/design/a.md", "tools/tillr-view/README.md", "yolo-packs/matt/README.md"} {
		if got := packdocs.SourceOf(outside); got != outside {
			return fmt.Errorf("SourceOf(%q) = %q, want %q", outside, got, outside)
		}
		gotGate, err := gateExcusedAs(outside)
		if err != nil {
			return err
		}
		if gotGate != outside {
			return fmt.Errorf("repo-gate excused-as %q = %q, want %q", outside, gotGate, outside)
		}
	}
	ok(stdout, "the index declares exactly its `shipped` rows, names an undecided row, and agrees with this suite about which modules are mirrored; a delivered copy maps back to its source by one rule, which the gate's checks restate identically")
	return nil
}

func readBlobs(root string, blobs []string) (map[string][]byte, error) {
	found := make(map[string][]byte, len(blobs))
	if len(blobs) == 0 {
		return found, nil
	}
	var input strings.Builder
	for _, b := range blobs {
		input.WriteString(b)
		input.WriteByte('\n')
	}
	cmd := exec.Command("git", "-C", root, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(input.String())
	batch, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	at := 0
	for _, blob := range blobs {
		nl := bytes.IndexByte(batch[at:], '\n')
		if nl < 0 {
			return nil, fmt.Errorf("unexpected EOF in git cat-file --batch output")
		}
		header := strings.Fields(string(batch[at : at+nl]))
		if len(header) != 3 || header[1] != "blob" {
			return nil, fmt.Errorf("the snapshot names %s, which this repository does not hold as a blob: run `just pack-docs`", blob)
		}
		size, err := strconv.Atoi(header[2])
		if err != nil {
			return nil, err
		}
		start := at + nl + 1
		found[blob] = batch[start : start+size]
		at = start + size + 1
	}
	return found, nil
}

func loadSnapshot(root string) (packdocs.SnapshotData, error) {
	p := filepath.Join(copyDir(root), filepath.FromSlash(packdocs.Snapshot))
	raw, err := os.ReadFile(p)
	if err != nil {
		rel, _ := filepath.Rel(root, p)
		return packdocs.SnapshotData{}, fmt.Errorf("missing the delivered documentation's snapshot: %s\nrun `just pack-docs`", filepath.ToSlash(rel))
	}
	var snap packdocs.SnapshotData
	if err := json.Unmarshal(raw, &snap); err != nil {
		return packdocs.SnapshotData{}, err
	}
	return snap, nil
}

func headBlobs(root string, paths []string) (map[string]string, error) {
	found := make(map[string]string)
	if len(paths) == 0 {
		return found, nil
	}
	args := append([]string{"-C", root, "ls-tree", "-z", "HEAD", "--"}, paths...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, err
	}
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		meta, p, ok := strings.Cut(string(entry), "\t")
		if ok {
			fields := strings.Fields(meta)
			if len(fields) >= 3 {
				found[p] = fields[2]
			}
		}
	}
	return found, nil
}

// StaleDocuments returns the delivered sources whose blob at HEAD is not the one the snapshot copied.
func StaleDocuments(entries []packdocs.SnapshotEntry, atHead map[string]string) []string {
	var stale []string
	for _, e := range entries {
		if atHead[e.Source] != e.Blob {
			stale = append(stale, e.Source)
		}
	}
	return stale
}

// CaseDocsStaleRule checks the freshness rule on synthetic data.
func CaseDocsStaleRule(stdout io.Writer) error {
	entries := []packdocs.SnapshotEntry{
		{Source: "docs/a.md", Blob: strings.Repeat("1", 40), Delivered: "docs/a.md"},
		{Source: "docs/b.md", Blob: strings.Repeat("2", 40), Delivered: "docs/b.md"},
		{Source: "docs/c.md", Blob: strings.Repeat("3", 40), Delivered: "docs/c.md"},
	}
	atHead := map[string]string{
		"docs/a.md": strings.Repeat("1", 40),
		"docs/b.md": strings.Repeat("9", 40),
	}
	stale := StaleDocuments(entries, atHead)
	if len(stale) != 2 || stale[0] != "docs/b.md" || stale[1] != "docs/c.md" {
		return fmt.Errorf("unexpected stale list: %v", stale)
	}
	if len(StaleDocuments(entries[:1], atHead)) != 0 {
		return fmt.Errorf("unchanged document should not be stale")
	}
	ok(stdout, "a delivered source changed or deleted at HEAD reads as stale, and an unchanged one does not")
	return nil
}

// CaseDocsDelivered checks every delivered document against the blob its snapshot names.
func CaseDocsDelivered(root string, candidate bool, stdout io.Writer) error {
	if candidate {
		if stdout != nil {
			fmt.Fprintln(stdout, "n/a  the delivered documentation: a candidate does not carry the generated tree")
		}
		return nil
	}
	snap, err := loadSnapshot(root)
	if err != nil {
		return err
	}
	entries := packdocs.SnapshotEntries(snap)
	delivered := packdocs.DeliveredSet(snap)
	blobIDs := make([]string, len(entries))
	for i, e := range entries {
		blobIDs[i] = e.Blob
	}
	blobs, err := readBlobs(root, blobIDs)
	if err != nil {
		return err
	}
	copyRoot := copyDir(root)
	size := 0
	copiedSet := make(map[string]bool, len(entries))
	expectedFiles := map[string]bool{packdocs.Snapshot: true}
	sources := make([]string, 0, len(entries))
	for _, e := range entries {
		sources = append(sources, e.Source)
		copiedSet[e.Delivered] = true
		if strings.HasPrefix(e.Delivered, "docs/") {
			expectedFiles[e.Delivered] = true
		}
		copyPath := filepath.Join(copyRoot, filepath.FromSlash(e.Delivered))
		fi, err := os.Stat(copyPath)
		sym, symErr := isSymlink(copyPath)
		if err != nil || !fi.Mode().IsRegular() || symErr != nil || sym {
			return fmt.Errorf("the delivered copy of %s is missing or a symlink: %s; run `just pack-docs`", e.Source, e.Delivered)
		}
		expected := []byte(packdocs.Transform(string(blobs[e.Blob]), e.Source, delivered, snap.Commit, snap.Date))
		actual, err := os.ReadFile(copyPath)
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, expected) {
			shortBlob := e.Blob
			if len(shortBlob) > 10 {
				shortBlob = shortBlob[:10]
			}
			return fmt.Errorf("the delivered copy of %s is not the transform of the blob its snapshot names (%s); it was edited by hand or the transform changed: run `just pack-docs`", e.Source, shortBlob)
		}
		if live := packdocs.LiveDirectives(string(actual)); len(live) > 0 {
			return fmt.Errorf("the delivered copy of %s carries a live item directive: %s", e.Source, live[0])
		}
		size += len(actual)
	}

	var stray []string
	err = filepath.WalkDir(filepath.Join(copyRoot, "docs"), func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(copyRoot, p)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if !expectedFiles[relSlash] {
			stray = append(stray, relSlash)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(stray)
	if len(stray) > 0 {
		limit := len(stray)
		if limit > 5 {
			limit = 5
		}
		return fmt.Errorf("the delivered docs tree carries %d file(s) its snapshot does not name: %s; run `just pack-docs`", len(stray), strings.Join(stray[:limit], ", "))
	}

	indexDest := packdocs.Destination(snap.Index)
	indexPath := filepath.Join(copyRoot, filepath.FromSlash(indexDest))
	if !delivered[snap.Index] {
		return fmt.Errorf("the status index is not delivered")
	}
	indexRaw, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("the status index is not delivered")
	}
	declared := packdocs.Declared(string(indexRaw), snap.Index)
	mirrorDests := make(map[string]bool, len(snap.InModuleMirror))
	for _, p := range snap.InModuleMirror {
		mirrorDests[packdocs.Destination(p)] = true
	}
	var undelivered []string
	declaredSet := make(map[string]bool, len(declared))
	for _, p := range declared {
		declaredSet[p] = true
		if !copiedSet[p] {
			if mirrorDests[p] {
				if fi, err := os.Stat(filepath.Join(copyRoot, filepath.FromSlash(p))); err == nil && fi.Mode().IsRegular() {
					continue
				}
			}
			undelivered = append(undelivered, p)
		}
	}
	if len(undelivered) > 0 {
		limit := len(undelivered)
		if limit > 5 {
			limit = 5
		}
		return fmt.Errorf("the delivered index lists %d shipped document(s) the pack does not deliver: %s; run `just pack-docs`", len(undelivered), strings.Join(undelivered[:limit], ", "))
	}
	var unlisted []string
	for p := range copiedSet {
		if !declaredSet[p] && p != indexDest {
			unlisted = append(unlisted, p)
		}
	}
	sort.Strings(unlisted)
	if len(unlisted) > 0 {
		limit := len(unlisted)
		if limit > 5 {
			limit = 5
		}
		return fmt.Errorf("the pack delivers %d document(s) its index does not list as shipped: %s; run `just pack-docs`", len(unlisted), strings.Join(unlisted[:limit], ", "))
	}

	atHead, err := headBlobs(root, sources)
	if err != nil {
		return err
	}
	lagging := StaleDocuments(entries, atHead)
	shortCommit := snap.Commit
	if len(shortCommit) > 10 {
		shortCommit = shortCommit[:10]
	}
	ok(stdout, fmt.Sprintf("the delivered documentation is the snapshot of %s (%s): %d document(s), %s bytes, plus %d delivered by the module mirror; %d changed at HEAD since (`--fresh` judges that)",
		shortCommit, snap.Date, len(entries), formatCommas(size), len(snap.InModuleMirror), len(lagging)))
	return nil
}

// CaseDocsFresh checks that the snapshot matches HEAD (--fresh).
func CaseDocsFresh(root string, stdout io.Writer) error {
	snap, err := loadSnapshot(root)
	if err != nil {
		return err
	}
	entries := packdocs.SnapshotEntries(snap)
	sources := make([]string, len(entries))
	for i, e := range entries {
		sources[i] = e.Source
	}
	atHead, err := headBlobs(root, sources)
	if err != nil {
		return err
	}
	stale := StaleDocuments(entries, atHead)
	if len(stale) > 0 {
		short := snap.Commit
		if len(short) > 10 {
			short = short[:10]
		}
		limit := len(stale)
		ellipsis := ""
		if limit > 8 {
			limit = 8
			ellipsis = " ..."
		}
		return fmt.Errorf("%d delivered document(s) changed at HEAD since the snapshot of %s (%s): %s%s\nrun `just pack-docs` and commit the result",
			len(stale), short, snap.Date, strings.Join(stale[:limit], ", "), ellipsis)
	}
	indexOut, err := exec.Command("git", "-C", root, "show", "HEAD:"+packdocs.Index).Output()
	if err != nil {
		return err
	}
	declared := make(map[string]bool)
	for _, p := range packdocs.Declared(string(indexOut), packdocs.Index) {
		declared[p] = true
	}
	shipped := packdocs.DeliveredSet(snap)
	delete(shipped, packdocs.Index)
	for _, p := range snap.InModuleMirror {
		shipped[p] = true
	}
	var added, removed []string
	for p := range declared {
		if !shipped[p] {
			added = append(added, p)
		}
	}
	for p := range shipped {
		if !declared[p] {
			removed = append(removed, p)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	if len(added) > 0 || len(removed) > 0 {
		if len(added) > 5 {
			added = added[:5]
		}
		if len(removed) > 5 {
			removed = removed[:5]
		}
		return fmt.Errorf("the index at HEAD declares a different set of shipped documents from the snapshot's: added %v, removed %v\nrun `just pack-docs` and commit the result", added, removed)
	}
	ok(stdout, fmt.Sprintf("the delivered documentation is current: %d document(s) match HEAD", len(entries)))
	return nil
}

func findRepoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	out, err := exec.Command("git", "-C", wd, "rev-parse", "--show-toplevel").Output()
	if err == nil {
		if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
			return trimmed
		}
	}
	return wd
}

// CaseWebDist checks that the pack's shipped observatory front end exists, contains no
// symlinks, and matches the current digest of its sources under tools/tillr-view/web/.
func CaseWebDist(root string, candidate bool, stdout io.Writer) error {
	if candidate {
		if stdout != nil {
			fmt.Fprintln(stdout, "n/a  the tillr pack's observatory front end: a candidate does not carry the generated tree")
		}
		return nil
	}
	pkgPath := filepath.Join(root, filepath.FromSlash(packmirror.WebSourceDir), "package.json")
	if fi, err := os.Stat(pkgPath); err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	wantDigest, err := packmirror.ComputeWebSourceDigest(root)
	if err != nil {
		return err
	}
	viewCopy := filepath.Join(copyDir(root), "tillr-view")
	distDir := filepath.Join(viewCopy, filepath.FromSlash(packmirror.WebDistRel))
	indexPath := filepath.Join(distDir, "index.html")
	if sym, err := isSymlink(indexPath); err != nil || sym {
		return fmt.Errorf("missing or symlinked pack observatory front end (%s): run `just pack-sync`", indexPath)
	}
	if fi, err := os.Stat(indexPath); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("missing pack observatory front end (%s): run `just pack-sync`", indexPath)
	}
	fileCount := 0
	if err := filepath.WalkDir(distDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("the pack's observatory front end contains a symlink (%s); the committed tree must be self-contained", p)
		}
		if !d.IsDir() {
			fileCount++
		}
		return nil
	}); err != nil {
		return err
	}
	digestPath := filepath.Join(viewCopy, filepath.FromSlash(packmirror.WebDistDigestRel))
	if sym, err := isSymlink(digestPath); err != nil || sym {
		return fmt.Errorf("missing or symlinked pack observatory front-end digest (%s): run `just pack-sync`", digestPath)
	}
	rawDigest, err := os.ReadFile(digestPath)
	if err != nil {
		return fmt.Errorf("missing pack observatory front-end digest (%s): run `just pack-sync`: %w", digestPath, err)
	}
	gotDigest := strings.TrimSpace(string(rawDigest))
	if gotDigest != wantDigest {
		return fmt.Errorf("the pack's shipped observatory front end is older than its sources (%s != %s): run `just pack-sync`", gotDigest, wantDigest)
	}
	short := wantDigest
	if len(short) > 19 {
		short = short[:19]
	}
	ok(stdout, fmt.Sprintf("the pack's observatory front end matches its sources (%s, %d files)", short, fileCount))
	return nil
}

// Main is the CLI entrypoint for pack-sync-check. args should be os.Args[1:].
func Main(args []string, stdout, stderr io.Writer) int {
	root := findRepoRoot()
	var fresh, candidate bool
	for _, a := range args {
		switch a {
		case "--fresh":
			fresh = true
		case "--candidate":
			candidate = true
		}
	}
	if fresh {
		if err := CaseDocsFresh(root, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	checks := []func() error{
		func() error { return CaseTreeMatchesSource(root, candidate, stdout) },
		func() error { return CaseBytes(root, candidate, stdout) },
		func() error { return CaseModes(root, candidate, stdout) },
		func() error { return CaseWebDist(root, candidate, stdout) },
		func() error { return CaseLoopAdapter(root, candidate, stdout) },
		func() error { return CaseWorkflows(root, candidate, stdout) },
		func() error { return CaseSupportRevision(root, candidate, stdout) },
		func() error { return CaseBuildOutputs(stdout) },
		func() error { return CaseCopyBuildOutputs(stdout) },
		func() error { return CaseLaunchers(root, stdout) },
		func() error { return CaseDocsTransform(stdout) },
		func() error { return CaseDocsDeclaration(root, stdout) },
		func() error { return CaseDocsStaleRule(stdout) },
		func() error { return CaseDocsDelivered(root, candidate, stdout) },
	}
	for _, fn := range checks {
		if err := fn(); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	return 0
}
