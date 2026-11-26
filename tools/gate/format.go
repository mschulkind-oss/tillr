package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

var miseConfigGoRe = regexp.MustCompile(`(?m)^\s*"?(?:go|golang)"?\s*[=\s]`)

func resolveGofmt() (string, string) {
	pathEnv := os.Getenv("PATH")
	var candidates []string
	for _, entry := range strings.Split(pathEnv, string(os.PathListSeparator)) {
		if entry == "" {
			continue
		}
		cand := filepath.Join(entry, "gofmt")
		info, err := os.Stat(cand)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			candidates = append(candidates, cand)
		}
	}
	if len(candidates) == 0 {
		return "gofmt", ""
	}
	chosen := candidates[0]
	chosenReal, err := filepath.EvalSymlinks(chosen)
	if err != nil {
		chosenReal = chosen
	}
	if filepath.Base(chosenReal) == "mise" && os.Getenv("MISE_GO_VERSION") == "" {
		var direct string
		for _, c := range candidates {
			r, err := filepath.EvalSymlinks(c)
			if err != nil {
				r = c
			}
			if filepath.Base(r) != "mise" {
				direct = c
				break
			}
		}
		if direct != "" {
			var configs []string
			cur, _ := os.Getwd()
			for cur != "" {
				for _, name := range []string{"mise.toml", "mise.local.toml", ".mise.toml", ".mise.local.toml", ".tool-versions"} {
					configs = append(configs, filepath.Join(cur, name))
				}
				parent := filepath.Dir(cur)
				if parent == cur {
					break
				}
				cur = parent
			}
			if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" && filepath.IsAbs(xdg) {
				configs = append(configs, filepath.Join(xdg, "mise", "config.toml"))
			} else if home := os.Getenv("HOME"); home != "" && filepath.IsAbs(home) {
				configs = append(configs, filepath.Join(home, ".config", "mise", "config.toml"))
			}
			configured := false
			for _, cfg := range configs {
				if data, err := os.ReadFile(cfg); err == nil {
					if miseConfigGoRe.Match(data) {
						configured = true
						break
					}
				}
			}
			if !configured {
				chosen = direct
				chosenReal, err = filepath.EvalSymlinks(chosen)
				if err != nil {
					chosenReal = chosen
				}
			}
		}
	}
	if filepath.Base(chosenReal) == "mise" {
		return chosen, ""
	}
	f, err := os.Open(chosenReal)
	if err != nil {
		return chosen, ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return chosen, ""
	}
	return chosen, hex.EncodeToString(h.Sum(nil))
}

func resolveFormatCacheFile(toolKey string) string {
	if toolKey == "" {
		return ""
	}
	cacheRoot := os.Getenv("JUST_FORMAT_CACHE_DIR")
	if cacheRoot == "" {
		if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" && filepath.IsAbs(xdg) {
			cacheRoot = filepath.Join(xdg, "just-format")
		} else if home := os.Getenv("HOME"); home != "" && filepath.IsAbs(home) {
			cacheRoot = filepath.Join(home, ".cache", "just-format")
		}
	}
	if cacheRoot == "" {
		return ""
	}
	return filepath.Join(cacheRoot, toolKey+".bin")
}

func loadCleanDigests(cacheFile string) map[[32]byte]bool {
	out := make(map[[32]byte]bool)
	if cacheFile == "" {
		return out
	}
	raw, err := os.ReadFile(cacheFile)
	if err != nil {
		return out
	}
	usable := len(raw) - (len(raw) % 32)
	for i := 0; i < usable; i += 32 {
		var d [32]byte
		copy(d[:], raw[i:i+32])
		out[d] = true
	}
	return out
}

func saveCleanDigests(cacheFile string, clean, liveClean map[[32]byte]bool) {
	if cacheFile == "" {
		return
	}
	merged := make(map[[32]byte]bool, len(liveClean))
	for d := range liveClean {
		merged[d] = true
	}
	if len(merged) < 65536 {
		for d := range clean {
			merged[d] = true
			if len(merged) >= 65536 {
				break
			}
		}
	}
	sorted := make([][32]byte, 0, len(merged))
	for d := range merged {
		sorted = append(sorted, d)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare(sorted[i][:], sorted[j][:]) < 0
	})
	var buf bytes.Buffer
	buf.Grow(len(sorted) * 32)
	for _, d := range sorted {
		buf.Write(d[:])
	}
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(cacheFile), ".gofmt-*")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Rename(tmpPath, cacheFile); err != nil {
		return
	}
	cleanup = false
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return true
	}
	return info.Mode()&os.ModeSymlink != 0
}

func gofmtEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+3)
	hasMem := false
	hasProcs := false
	for _, e := range env {
		k, _, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if k == "GOGC" {
			continue
		}
		if k == "GOMEMLIMIT" {
			hasMem = true
		}
		if k == "GOMAXPROCS" {
			hasProcs = true
		}
		out = append(out, e)
	}
	out = append(out, "GOGC=off")
	if !hasMem {
		out = append(out, "GOMEMLIMIT=512MiB")
	}
	if !hasProcs {
		n := runtime.NumCPU()
		if n <= 0 {
			n = 4
		}
		if n > 12 {
			n = 12
		}
		out = append(out, "GOMAXPROCS="+strconv.Itoa(n))
	}
	return out
}

func cmdFormat(args []string) int {
	checkOnly := false
	switch len(args) {
	case 0:
	case 1:
		if args[0] != "--check" {
			fmt.Fprintln(os.Stderr, "usage: just format [--check]")
			return 2
		}
		checkOnly = true
	default:
		fmt.Fprintln(os.Stderr, "usage: just format [--check]")
		return 2
	}

	listing, err := exec.Command("git", "ls-files", "-z", "--", "tools/", "yolo-packs/tillr/files/local/share/tillr/").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "format: git ls-files: %v\n", err)
		return 1
	}

	dirOK := map[string]bool{
		"":  true,
		".": !isSymlink("."),
	}
	var cleanAncestors func(string) bool
	cleanAncestors = func(dir string) bool {
		if ok, seen := dirOK[dir]; seen {
			return ok
		}
		parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(dir)))
		ok := cleanAncestors(parent) && !isSymlink(filepath.FromSlash(dir))
		dirOK[dir] = ok
		return ok
	}

	var files []string
	groups := make(map[[32]byte][]string)
	fileDigest := make(map[string][32]byte)
	for _, rawEntry := range bytes.Split(listing, []byte{0}) {
		if len(rawEntry) == 0 {
			continue
		}
		path := string(rawEntry)
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		if filepath.Base(path) == ".go" {
			continue
		}
		parts := strings.Split(path, "/")
		excluded := false
		for _, p := range parts {
			if p == "vendor" || p == ".forks" {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
		if dir == "." {
			dir = ""
		}
		if !cleanAncestors(dir) {
			continue
		}
		st, err := os.Lstat(filepath.FromSlash(path))
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.FromSlash(path))
		if err != nil {
			continue
		}
		digest := sha256.Sum256(data)
		groups[digest] = append(groups[digest], path)
		files = append(files, path)
		fileDigest[path] = digest
	}

	if len(files) == 0 {
		return 0
	}

	gofmtBin, toolKey := resolveGofmt()
	cacheFile := resolveFormatCacheFile(toolKey)
	clean := loadCleanDigests(cacheFile)
	env := gofmtEnv()

	var pending []string
	repDigest := make(map[string][32]byte)
	for d, bucket := range groups {
		if !clean[d] {
			pending = append(pending, bucket[0])
			repDigest[bucket[0]] = d
		}
	}
	sort.Strings(pending)

	dirtyDigests := make(map[[32]byte]bool)
	var gofmtCode int
	var gofmtStderr []byte
	if len(pending) > 0 {
		cmd := exec.Command(gofmtBin, append([]string{"-l"}, pending...)...)
		cmd.Env = env
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		err := cmd.Run()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				gofmtCode = ee.ExitCode()
			} else {
				gofmtCode = 1
			}
		}
		gofmtStderr = errBuf.Bytes()
		flagged := make(map[string]bool)
		for _, line := range strings.Split(outBuf.String(), "\n") {
			line = strings.TrimRight(line, "\r")
			if line != "" {
				flagged[line] = true
			}
		}
		for rep := range flagged {
			if d, ok := repDigest[rep]; ok {
				dirtyDigests[d] = true
			}
		}
		if gofmtCode == 0 && len(gofmtStderr) == 0 {
			newlyClean := false
			for _, rep := range pending {
				if !flagged[rep] {
					d := repDigest[rep]
					clean[d] = true
					newlyClean = true
				}
			}
			if newlyClean {
				liveClean := make(map[[32]byte]bool)
				for d := range groups {
					if clean[d] {
						liveClean[d] = true
					}
				}
				saveCleanDigests(cacheFile, clean, liveClean)
			}
		}
	}

	var dirty []string
	for _, p := range files {
		if dirtyDigests[fileDigest[p]] {
			dirty = append(dirty, p)
		}
	}

	writeStderr := func() {
		if len(gofmtStderr) == 0 {
			return
		}
		_, _ = os.Stderr.Write(gofmtStderr)
		var dups []string
		for _, bucket := range groups {
			if len(bucket) > 1 && bytes.Contains(gofmtStderr, []byte(bucket[0]+":")) {
				dups = append(dups, bucket[1:]...)
			}
		}
		if len(dups) > 0 {
			extra := exec.Command(gofmtBin, append([]string{"-l"}, dups...)...)
			extra.Env = env
			var errBuf bytes.Buffer
			extra.Stderr = &errBuf
			_ = extra.Run()
			_, _ = os.Stderr.Write(errBuf.Bytes())
		}
	}

	if checkOnly {
		for _, p := range dirty {
			fmt.Println(p)
		}
		writeStderr()
		if gofmtCode != 0 {
			return gofmtCode
		}
		if len(dirty) > 0 {
			return 1
		}
		return 0
	}

	if gofmtCode != 0 {
		writeStderr()
		return gofmtCode
	}
	if len(dirty) > 0 {
		cmd := exec.Command(gofmtBin, append([]string{"-w"}, dirty...)...)
		cmd.Env = env
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return ee.ExitCode()
			}
			return 1
		}
		formattedClean := make(map[[32]byte]bool)
		for _, p := range dirty {
			if data, err := os.ReadFile(filepath.FromSlash(p)); err == nil {
				d := sha256.Sum256(data)
				formattedClean[d] = true
				clean[d] = true
			}
		}
		liveClean := make(map[[32]byte]bool, len(formattedClean))
		for d := range groups {
			if !dirtyDigests[d] && clean[d] {
				liveClean[d] = true
			}
		}
		for d := range formattedClean {
			liveClean[d] = true
		}
		saveCleanDigests(cacheFile, clean, liveClean)
	}
	return 0
}
