package tillrdocs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	BuildTimeout = 300 * time.Second
	HelpTimeout  = 60 * time.Second
)

var (
	nounSet = func() map[string]bool {
		m := make(map[string]bool, len(Nouns))
		for _, n := range Nouns {
			m[n] = true
		}
		return m
	}()
	binaryValues = func() map[string]bool {
		m := make(map[string]bool, len(Binary))
		for _, v := range Binary {
			m[v] = true
		}
		return m
	}()
)

// InlineSpans returns every backtick span in text, allowing a single newline inside one and nothing else.
func InlineSpans(text string) []string {
	var spans []string
	index := 0
	for {
		startRel := strings.IndexByte(text[index:], '`')
		if startRel < 0 {
			return spans
		}
		start := index + startRel
		endRel := strings.IndexByte(text[start+1:], '`')
		if endRel < 0 {
			return spans
		}
		end := start + 1 + endRel
		body := text[start+1 : end]
		if !strings.Contains(body, "\n\n") {
			spans = append(spans, body)
		}
		index = end + 1
	}
}

// CodeBlobs returns every fenced block's body and every inline span outside fences.
func CodeBlobs(text string) []string {
	matches := fenceRe.FindAllStringSubmatch(text, -1)
	blobs := make([]string, 0, len(matches))
	for _, m := range matches {
		blobs = append(blobs, m[1])
	}
	stripped := fenceRe.ReplaceAllString(text, "")
	blobs = append(blobs, InlineSpans(stripped)...)
	return blobs
}

// WordsOf returns one token list per non-empty line of a blob, with punctuation trimmed.
func WordsOf(blob string) [][]string {
	var out [][]string
	for _, line := range splitLines(blob) {
		raw := strings.Fields(line)
		var words []string
		for _, tok := range raw {
			w := strings.Trim(tok, "`*.,;:()[]")
			if w != "" {
				words = append(words, w)
			}
		}
		if len(words) > 0 {
			out = append(out, words)
		}
	}
	return out
}

// Documented returns the invocation a token list names, or nil when it names no face.
func Documented(words []string) []string {
	if len(words) == 0 {
		return nil
	}
	head := words[0]
	var prefix, rest []string
	if head == "tillr" {
		if len(words) < 3 || !nounSet[words[1]] {
			return nil
		}
		prefix = []string{head, words[1]}
		rest = words[2:]
	} else if binaryValues[head] {
		if len(words) < 2 {
			return nil
		}
		prefix = []string{head}
		rest = words[1:]
	} else {
		return nil
	}
	var kept []string
	for _, word := range rest {
		if strings.HasPrefix(word, "-") || strings.HasPrefix(word, "<") || strings.HasPrefix(word, "$") || strings.HasPrefix(word, "{") {
			break
		}
		kept = append(kept, word)
	}
	if len(kept) == 0 {
		return nil
	}
	argv := make([]string, 0, len(prefix)+len(kept))
	argv = append(argv, prefix...)
	argv = append(argv, kept...)
	return argv
}

// Invocations returns every documented invocation in one document, in appearance order.
func Invocations(root, path string) ([][]string, error) {
	txt, err := textOf(root, path)
	if err != nil {
		return nil, err
	}
	var found [][]string
	for _, blob := range CodeBlobs(txt) {
		for _, words := range WordsOf(blob) {
			if argv := Documented(words); argv != nil {
				found = append(found, argv)
			}
		}
	}
	return found, nil
}

// GoBuildEnv returns the environment matching the fast gate's go-build flags.
func GoBuildEnv() []string {
	var env []string
	var goflags string
	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if k == "LAND_BATCH_LIMIT" || k == "CGO_ENABLED" {
			continue
		}
		if k == "GOFLAGS" {
			goflags = v
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "CGO_ENABLED=0")
	if goflags != "" {
		env = append(env, "GOFLAGS="+goflags+" -trimpath")
	} else {
		env = append(env, "GOFLAGS=-trimpath")
	}
	return env
}

// BuildModule builds one Go module into directory/binary.
func BuildModule(module, binary, directory, root string) error {
	ctx, cancel := context.WithTimeout(context.Background(), BuildTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(directory, binary), ".")
	cmd.Dir = filepath.Join(root, "tools", module)
	cmd.Env = GoBuildEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building %s from tools/%s failed:\n%s", binary, module, stderr.String())
	}
	return nil
}

// BuildBinariesError builds the eight binaries concurrently into directory.
func BuildBinariesError(directory, root string) error {
	type item struct {
		module string
		binary string
	}
	items := make([]item, 0, len(Binary))
	for mod, bin := range Binary {
		items = append(items, item{mod, bin})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].binary < items[j].binary
	})

	errs := make([]error, len(items))
	var wg sync.WaitGroup
	for i, it := range items {
		wg.Add(1)
		go func(idx int, mod, bin string) {
			defer wg.Done()
			errs[idx] = BuildModule(mod, bin, directory, root)
		}(i, it.module, it.binary)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func scrubHelpEnv(directory string) []string {
	var env []string
	var pathVal string
	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch k {
		case "PATH":
			pathVal = v
		case "BOARD_DB", "XDG_STATE_HOME":
			continue
		default:
			env = append(env, entry)
		}
	}
	if pathVal != "" {
		env = append(env, "PATH="+directory+string(os.PathListSeparator)+pathVal)
	} else {
		env = append(env, "PATH="+directory)
	}
	env = append(env, "BOARD_DB=")
	env = append(env, "XDG_STATE_HOME="+filepath.Join(directory, "state"))
	return env
}

// FacesHelpError runs `--help` concurrently across every documented invocation in document order.
func FacesHelpError(directory string, documents []string, root string) (error, int) {
	env := scrubHelpEnv(directory)

	type entry struct {
		relative string
		argv     []string
	}
	var ordered []entry
	seen := make(map[string]bool)
	for _, p := range documents {
		rel := relPath(root, p)
		invs, err := Invocations(root, p)
		if err != nil {
			return err, 0
		}
		for _, argv := range invs {
			key := strings.Join(argv, "\x00")
			if seen[key] {
				continue
			}
			seen[key] = true
			ordered = append(ordered, entry{relative: rel, argv: argv})
		}
	}
	if len(ordered) == 0 {
		return fmt.Errorf("no documented invocation was found in any module README or in the tillr-operating skill, so this suite asserted nothing"), 0
	}

	type result struct {
		exitCode int
		stdout   string
		stderr   string
		runErr   error
	}
	results := make([]result, len(ordered))
	sem := make(chan struct{}, min(16, len(ordered)))
	var wg sync.WaitGroup
	for i, ent := range ordered {
		wg.Add(1)
		go func(idx int, e entry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			args := make([]string, 0, len(e.argv))
			args = append(args, e.argv[1:]...)
			args = append(args, "--help")
			ctx, cancel := context.WithTimeout(context.Background(), HelpTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, filepath.Join(directory, e.argv[0]), args...)
			cmd.Dir = directory
			cmd.Env = env
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			code := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					code = exitErr.ExitCode()
				} else {
					code = -1
				}
			}
			results[idx] = result{
				exitCode: code,
				stdout:   stdout.String(),
				stderr:   stderr.String(),
				runErr:   err,
			}
		}(i, ent)
	}
	wg.Wait()

	for i, res := range results {
		ent := ordered[i]
		face := ent.argv[len(ent.argv)-1]
		if res.exitCode != 0 {
			return fmt.Errorf("%s documents `%s`, which does not answer its `--help`: exit %d\nstderr: %s\nA face a document names is a face the binary must have.",
				ent.relative, strings.Join(ent.argv, " "), res.exitCode, strings.TrimSpace(res.stderr)), len(ordered)
		}
		if !strings.Contains(res.stdout, face) {
			return fmt.Errorf("%s documents `%s`, whose `--help` does not name the face:\n%s",
				ent.relative, strings.Join(ent.argv, " "), strings.TrimSpace(res.stdout)), len(ordered)
		}
	}
	return nil, len(ordered)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// FacesHelpFixtureError verifies FacesHelpError on a tree with one face's `--help` broken and on silent help.
func FacesHelpFixtureError(scratch, root string) error {
	brokenTree := filepath.Join(scratch, "broken-tree")
	if err := os.MkdirAll(filepath.Join(brokenTree, "tools"), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(filepath.Join(root, "tools", "tillr"), filepath.Join(brokenTree, "tools", "tillr")); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(root, "tools", "tillr-gate"), filepath.Join(brokenTree, "tools", "tillr-gate")); err != nil {
		return err
	}
	gateMain := filepath.Join(brokenTree, "tools", "tillr-gate", "main.go")
	srcBytes, err := os.ReadFile(gateMain)
	if err != nil {
		return err
	}
	source := string(srcBytes)
	target := "\t\"shadow\":      shadowFace,\n"
	if !strings.Contains(source, target) {
		return fmt.Errorf("tillr-gate main.go no longer registers the shadow face expected by the fixture")
	}
	if err := os.WriteFile(gateMain, []byte(strings.Replace(source, target, "", 1)), 0o644); err != nil {
		return err
	}

	brokenBin := filepath.Join(scratch, "broken-bin")
	if err := os.MkdirAll(brokenBin, 0o755); err != nil {
		return err
	}
	for _, binary := range Binary {
		if binary != "tillr-gate" {
			if err := os.Symlink(filepath.Join(scratch, binary), filepath.Join(brokenBin, binary)); err != nil {
				return err
			}
		}
	}
	if err := BuildModule("tillr-gate", "tillr-gate", brokenBin, brokenTree); err != nil {
		return fmt.Errorf("building broken tillr-gate fixture failed: %w", err)
	}
	docs := Documents(root)
	err, _ = FacesHelpError(brokenBin, docs, root)
	expectedPrefix := "tools/tillr-gate/README.md documents `tillr-gate shadow`, which does not answer its `--help`: exit 2\nstderr: tillr-gate: \"shadow\" is not a face;"
	expectedSuffix := "\nA face a document names is a face the binary must have."
	if err == nil || !strings.HasPrefix(err.Error(), expectedPrefix) || !strings.HasSuffix(err.Error(), expectedSuffix) {
		return fmt.Errorf("broken-face fixture reported unexpected finding: %v", err)
	}

	silentBin := filepath.Join(scratch, "silent-bin")
	if err := os.MkdirAll(silentBin, 0o755); err != nil {
		return err
	}
	for _, binary := range Binary {
		if binary != "tillr-land" && binary != "tillr-view" {
			if err := os.Symlink(filepath.Join(scratch, binary), filepath.Join(silentBin, binary)); err != nil {
				return err
			}
		}
	}
	landShim := fmt.Sprintf("#!/bin/sh\nif [ \"${1:-}\" = \"one\" ]; then printf \"Usage: tillr-land <face>\\n\"; exit 0; fi\nexec %q \"$@\"\n", filepath.Join(scratch, "tillr-land"))
	if err := os.WriteFile(filepath.Join(silentBin, "tillr-land"), []byte(landShim), 0o755); err != nil {
		return err
	}
	viewShim := fmt.Sprintf("#!/bin/sh\nif [ \"${1:-}\" = \"perf-log\" ]; then printf \"broken perf-log\\n\" >&2; exit 2; fi\nexec %q \"$@\"\n", filepath.Join(scratch, "tillr-view"))
	if err := os.WriteFile(filepath.Join(silentBin, "tillr-view"), []byte(viewShim), 0o755); err != nil {
		return err
	}
	silentErr, _ := FacesHelpError(silentBin, docs, root)
	expectedSilent := "tools/tillr-land/README.md documents `tillr-land one`, whose `--help` does not name the face:\nUsage: tillr-land <face>"
	if silentErr == nil || silentErr.Error() != expectedSilent {
		return fmt.Errorf("silent-help fixture reported %v, want %q", silentErr, expectedSilent)
	}
	return nil
}

// RunFacesLane builds the eight binaries in a temporary scratch directory, checks all documented
// faces with `--help`, runs the broken/silent fixtures, and always removes the scratch directory.
func RunFacesLane(root string) (buildErr, helpErr error, checked int, fixtureErr error) {
	scratch, err := os.MkdirTemp(os.Getenv("YOLO_DURABLE_DIR"), "tillr-docs-*")
	if err != nil {
		return err, nil, 0, nil
	}
	defer os.RemoveAll(scratch)

	if err := BuildBinariesError(scratch, root); err != nil {
		return err, nil, 0, nil
	}
	docs := Documents(root)
	helpErr, checked = FacesHelpError(scratch, docs, root)
	if helpErr != nil {
		return nil, helpErr, checked, nil
	}
	fixtureErr = FacesHelpFixtureError(scratch, root)
	return nil, nil, checked, fixtureErr
}
