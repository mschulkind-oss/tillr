// Package packdocs delivers tillr's design documentation through the tillr pack as a dated snapshot.
package packdocs

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	Index           = "docs/tillr/design-status.md"
	JailRoot        = "~/.local/share/tillr"
	Snapshot        = "docs/snapshot.json"
	Repository      = "https://github.com/mschulkind-oss/tillr/blob/main/"
	DeliveredPrefix = "yolo-packs/tillr/files/local/share/tillr/"
)

// MirroredModules lists the modules that `just pack-sync` mirrors.
var MirroredModules = []string{
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

var (
	inlineLinkRe    = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	referenceLinkRe = regexp.MustCompile(`^( {0,3}\[[^\]\n]+\]:\s*)(\S+)`)
	absoluteRe      = regexp.MustCompile(`^(?:[a-z][a-z0-9+.-]*:|/|#)`)
	directiveRe     = regexp.MustCompile(`<!--(\s*)tillr:(\s*)`)
	tableLinkRe     = regexp.MustCompile(`\[[^\]]*\]\(([^)\s#]+\.md)(?:#[^)\s]*)?\)`)
	headingRe       = regexp.MustCompile(`^# \S`)
)

const usageText = `Deliver tillr's design documentation through the tillr pack, as a dated snapshot.

Usage:
  pack-docs.py sync [--commit <rev>]   rewrite the delivered tree from <rev> (default HEAD)
  pack-docs.py list                    print the documents the index at HEAD declares
`

// SnapshotDoc represents one document entry in snapshot.json.
type SnapshotDoc struct {
	Source    string `json:"source"`
	Blob      string `json:"blob"`
	Delivered string `json:"delivered"`
}

// SnapshotData represents the snapshot.json schema (`tillr-pack-docs-1`).
type SnapshotData struct {
	Format         string        `json:"format"`
	Commit         string        `json:"commit"`
	Date           string        `json:"date"`
	Index          string        `json:"index"`
	JailRoot       string        `json:"jail_root"`
	Documents      []SnapshotDoc `json:"documents"`
	InModuleMirror []string      `json:"in_module_mirror"`
}

// SnapshotEntry is (source, blob, delivered) for one snapshot document.
type SnapshotEntry struct {
	Source    string
	Blob      string
	Delivered string
}

// DeliveredRoot returns the delivered root directory under repoRoot.
func DeliveredRoot(repoRoot string) string {
	return filepath.Join(repoRoot, "yolo-packs", "tillr", "files", "local", "share", "tillr")
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func isFence(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func parseTableCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.Trim(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// Declared returns the repository paths the index marks `shipped`, in the order listed.
func Declared(indexText, indexPath string) []string {
	if indexPath == "" {
		indexPath = Index
	}
	here := pathpkg.Dir(indexPath)
	var found []string
	seen := make(map[string]bool)
	fenced := false
	for _, line := range splitLines(indexText) {
		if isFence(line) {
			fenced = !fenced
			continue
		}
		if fenced || !strings.HasPrefix(strings.TrimLeft(line, " \t"), "|") {
			continue
		}
		cells := parseTableCells(line)
		if len(cells) < 2 || cells[len(cells)-1] != "shipped" {
			continue
		}
		m := tableLinkRe.FindStringSubmatch(cells[0])
		if m == nil {
			continue
		}
		p := pathpkg.Clean(pathpkg.Join(here, m[1]))
		if !seen[p] && p != indexPath {
			seen[p] = true
			found = append(found, p)
		}
	}
	return found
}

// UndecidedRows returns table rows that link a document in their first cell but say neither `shipped` nor `not shipped` last.
func UndecidedRows(indexText string) []string {
	var rows []string
	fenced := false
	for i, line := range splitLines(indexText) {
		number := i + 1
		if isFence(line) {
			fenced = !fenced
			continue
		}
		if fenced || !strings.HasPrefix(strings.TrimLeft(line, " \t"), "|") {
			continue
		}
		cells := parseTableCells(line)
		if len(cells) < 2 || !tableLinkRe.MatchString(cells[0]) {
			continue
		}
		last := cells[len(cells)-1]
		if last != "shipped" && last != "not shipped" {
			snippet := strings.TrimSpace(line)
			if len(snippet) > 120 {
				snippet = snippet[:120]
			}
			rows = append(rows, fmt.Sprintf("%d: %s", number, snippet))
		}
	}
	return rows
}

// Mirrored reports whether a repository path lies inside a module `just pack-sync` mirrors.
func Mirrored(p string) bool {
	parts := strings.Split(p, "/")
	if len(parts) < 2 || parts[0] != "tools" {
		return false
	}
	for _, m := range MirroredModules {
		if parts[1] == m {
			return true
		}
	}
	return false
}

// Destination returns where a repository path lands under the delivered root.
func Destination(p string) string {
	return strings.TrimPrefix(p, "tools/")
}

// SourceOf inverts Destination for paths under DeliveredPrefix.
func SourceOf(p string) string {
	if !strings.HasPrefix(p, DeliveredPrefix) {
		return p
	}
	rest := strings.TrimPrefix(p, DeliveredPrefix)
	if strings.HasPrefix(rest, "docs/") {
		return rest
	}
	return "tools/" + rest
}

// Banner returns the one-line delivered copy banner placed after the first heading.
func Banner(source, commit, date string) string {
	short := commit
	if len(short) > 10 {
		short = short[:10]
	}
	return fmt.Sprintf(
		"> **Delivered copy.** This is `%s` from this repository at commit `%s` (%s), copied into the tillr pack by `just pack-docs`. The [source](%s%s) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.",
		source, short, date, Repository, source,
	)
}

// RewriteTarget re-resolves one relative link target for the delivered tree.
func RewriteTarget(target, source string, delivered map[string]bool) string {
	if absoluteRe.MatchString(target) {
		return target
	}
	p, anchor, hasHash := strings.Cut(target, "#")
	if p == "" {
		return target
	}
	trailing := ""
	if strings.HasSuffix(p, "/") {
		trailing = "/"
	}
	resolved := pathpkg.Clean(pathpkg.Join(pathpkg.Dir(source), p))
	if strings.HasPrefix(resolved, "..") || resolved == "." {
		return target
	}
	suffix := ""
	if hasHash {
		suffix = "#" + anchor
	}
	if delivered[resolved] || Mirrored(resolved) {
		baseDir := pathpkg.Dir(Destination(source))
		if baseDir == "" {
			baseDir = "."
		}
		rel, err := filepath.Rel(filepath.FromSlash(baseDir), filepath.FromSlash(Destination(resolved)))
		if err != nil {
			return Repository + resolved + trailing + suffix
		}
		return filepath.ToSlash(rel) + trailing + suffix
	}
	return Repository + resolved + trailing + suffix
}

// Inert makes tillr directives in a line inert while keeping the comment readable.
func Inert(line string) string {
	return directiveRe.ReplaceAllString(line, "<!--${1}inert copy of a tillr directive:${2}")
}

// Transform computes the delivered bytes of one source document.
func Transform(text, source string, delivered map[string]bool, commit, date string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines)+2)
	fenced := false
	frontmatter := len(lines) > 0 && strings.TrimSpace(lines[0]) == "---"
	bannerDone := false
	for number, line := range lines {
		if frontmatter {
			out = append(out, line)
			if number > 0 && strings.TrimSpace(line) == "---" {
				frontmatter = false
			}
			continue
		}
		if isFence(line) {
			fenced = !fenced
			out = append(out, line)
			continue
		}
		if fenced {
			out = append(out, line)
			continue
		}
		line = inlineLinkRe.ReplaceAllStringFunc(line, func(match string) string {
			target := match[2 : len(match)-1]
			return "](" + RewriteTarget(target, source, delivered) + ")"
		})
		line = referenceLinkRe.ReplaceAllStringFunc(line, func(match string) string {
			sub := referenceLinkRe.FindStringSubmatch(match)
			return sub[1] + RewriteTarget(sub[2], source, delivered)
		})
		line = Inert(line)
		out = append(out, line)
		if !bannerDone && headingRe.MatchString(line) {
			out = append(out, "", Banner(source, commit, date))
			bannerDone = true
		}
	}
	return strings.Join(out, "\n")
}

// LiveDirectives returns `line: text` for every directive outside a fenced block that a tool would read.
func LiveDirectives(text string) []string {
	var found []string
	fenced := false
	for i, line := range splitLines(text) {
		number := i + 1
		if isFence(line) {
			fenced = !fenced
			continue
		}
		if !fenced && directiveRe.MatchString(line) {
			snippet := strings.TrimSpace(line)
			if len(snippet) > 100 {
				snippet = snippet[:100]
			}
			found = append(found, fmt.Sprintf("%d: %s", number, snippet))
		}
	}
	return found
}

// SnapshotEntries returns (source, blob, delivered) for every document in snapshot.
func SnapshotEntries(snapshot SnapshotData) []SnapshotEntry {
	entries := make([]SnapshotEntry, 0, len(snapshot.Documents))
	for _, doc := range snapshot.Documents {
		entries = append(entries, SnapshotEntry{
			Source:    doc.Source,
			Blob:      doc.Blob,
			Delivered: doc.Delivered,
		})
	}
	return entries
}

// DeliveredSet returns the set of source paths in snapshot.
func DeliveredSet(snapshot SnapshotData) map[string]bool {
	set := make(map[string]bool, len(snapshot.Documents))
	for _, doc := range snapshot.Documents {
		set[doc.Source] = true
	}
	return set
}

func runGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func formatCommas(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		if len(s) > rem {
			b.WriteByte(',')
		}
	}
	for i := rem; i < len(s); i += 3 {
		if i > rem {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// Sync rewrites the delivered documentation tree from commit (default HEAD).
func Sync(repoRoot, commit string, stdout, stderr io.Writer) int {
	if commit == "" {
		commit = "HEAD"
	}
	shaOut, err := runGit(repoRoot, "rev-parse", "--verify", commit+"^{commit}")
	if err != nil {
		fmt.Fprintf(stderr, "pack-docs: %v\n", err)
		return 1
	}
	sha := strings.TrimSpace(shaOut)

	dateOut, err := runGit(repoRoot, "show", "-s", "--format=%cd", "--date=short", sha)
	if err != nil {
		fmt.Fprintf(stderr, "pack-docs: %v\n", err)
		return 1
	}
	date := strings.TrimSpace(dateOut)

	indexText, err := runGit(repoRoot, "show", sha+":"+Index)
	if err != nil {
		fmt.Fprintf(stderr, "pack-docs: %v\n", err)
		return 1
	}

	decl := Declared(indexText, Index)
	sources := []string{Index}
	inMirror := []string{}
	for _, p := range decl {
		if Mirrored(p) {
			inMirror = append(inMirror, p)
		} else {
			sources = append(sources, p)
		}
	}

	var missing []string
	for _, p := range append(append([]string(nil), sources...), inMirror...) {
		out, err := runGit(repoRoot, "ls-tree", "--name-only", sha, "--", p)
		if err != nil || strings.TrimSpace(out) == "" {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		short := sha
		if len(short) > 10 {
			short = short[:10]
		}
		fmt.Fprintf(stderr, "pack-docs: the index at %s declares documents that commit does not hold: %s\n",
			short, strings.Join(missing, ", "))
		return 1
	}

	if undecided := UndecidedRows(indexText); len(undecided) > 0 {
		fmt.Fprintf(stderr, "pack-docs: index rows that say neither `shipped` nor `not shipped`:\n  %s\n",
			strings.Join(undecided, "\n  "))
		return 1
	}

	deliveredRoot := DeliveredRoot(repoRoot)
	previous := filepath.Join(deliveredRoot, filepath.FromSlash(Snapshot))
	if raw, err := os.ReadFile(previous); err == nil {
		var prevSnap SnapshotData
		if jsonErr := json.Unmarshal(raw, &prevSnap); jsonErr == nil {
			for _, entry := range SnapshotEntries(prevSnap) {
				_ = os.Remove(filepath.Join(deliveredRoot, filepath.FromSlash(entry.Delivered)))
			}
		}
	}
	docsRoot := filepath.Join(deliveredRoot, "docs")
	if fi, err := os.Stat(docsRoot); err == nil && fi.IsDir() {
		if err := os.RemoveAll(docsRoot); err != nil {
			fmt.Fprintf(stderr, "pack-docs: %v\n", err)
			return 1
		}
	}

	delivered := make(map[string]bool, len(sources))
	for _, s := range sources {
		delivered[s] = true
	}

	entries := make([]SnapshotDoc, 0, len(sources))
	var totalSize int64
	for _, source := range sources {
		blobOut, err := runGit(repoRoot, "rev-parse", sha+":"+source)
		if err != nil {
			fmt.Fprintf(stderr, "pack-docs: %v\n", err)
			return 1
		}
		blob := strings.TrimSpace(blobOut)
		text, err := runGit(repoRoot, "cat-file", "blob", blob)
		if err != nil {
			fmt.Fprintf(stderr, "pack-docs: %v\n", err)
			return 1
		}
		dest := Destination(source)
		target := filepath.Join(deliveredRoot, filepath.FromSlash(dest))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			fmt.Fprintf(stderr, "pack-docs: %v\n", err)
			return 1
		}
		transformed := Transform(text, source, delivered, sha, date)
		if err := os.WriteFile(target, []byte(transformed), 0o644); err != nil {
			fmt.Fprintf(stderr, "pack-docs: %v\n", err)
			return 1
		}
		totalSize += int64(len(transformed))
		entries = append(entries, SnapshotDoc{
			Source:    source,
			Blob:      blob,
			Delivered: dest,
		})
	}

	snap := SnapshotData{
		Format:         "tillr-pack-docs-1",
		Commit:         sha,
		Date:           date,
		Index:          Index,
		JailRoot:       JailRoot,
		Documents:      entries,
		InModuleMirror: inMirror,
	}
	if err := os.MkdirAll(filepath.Dir(previous), 0o755); err != nil {
		fmt.Fprintf(stderr, "pack-docs: %v\n", err)
		return 1
	}
	encoded, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "pack-docs: %v\n", err)
		return 1
	}
	if err := os.WriteFile(previous, append(encoded, '\n'), 0o644); err != nil {
		fmt.Fprintf(stderr, "pack-docs: %v\n", err)
		return 1
	}

	short := sha
	if len(short) > 10 {
		short = short[:10]
	}
	fmt.Fprintf(stdout, "pack-docs: delivered %d document(s), %s bytes, from %s (%s); %d more are delivered by the module mirror\n",
		len(entries), formatCommas(totalSize), short, date, len(inMirror))
	return 0
}

func findRepoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	out, err := runGit(wd, "rev-parse", "--show-toplevel")
	if err == nil {
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			return trimmed
		}
	}
	return wd
}

// Main is the CLI entrypoint for pack-docs. args should be os.Args[1:].
func Main(args []string, stdout, stderr io.Writer) int {
	repoRoot := findRepoRoot()
	if len(args) >= 1 && args[0] == "sync" {
		commit := "HEAD"
		if len(args) == 3 && args[1] == "--commit" {
			commit = args[2]
		} else if len(args) != 1 {
			fmt.Fprint(stderr, usageText)
			return 2
		}
		return Sync(repoRoot, commit, stdout, stderr)
	}
	if len(args) == 1 && args[0] == "list" {
		indexText, err := runGit(repoRoot, "show", "HEAD:"+Index)
		if err != nil {
			fmt.Fprintf(stderr, "pack-docs: %v\n", err)
			return 1
		}
		for _, p := range Declared(indexText, Index) {
			fmt.Fprintln(stdout, p)
		}
		return 0
	}
	fmt.Fprint(stderr, usageText)
	return 2
}
