package native

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxListEntries    = 500
	maxSearchMatches  = 200
	maxSearchLineLen  = 500
	binaryCheckBytes  = 8 * 1024
	maxSearchFileSize = 4 * 1024 * 1024
)

type listFilesArgs struct {
	Path string `json:"path"`
	Glob string `json:"glob"`
}

type searchFilesArgs struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path"`
	Glob            string `json:"glob"`
	Literal         bool   `json:"literal"`
	CaseInsensitive bool   `json:"case_insensitive"`
}

var errLimitReached = errors.New("limit reached")

func (t *Toolset) execListFiles(ctx context.Context, args listFilesArgs) (string, bool) {
	rawPath := strings.TrimSpace(args.Path)
	if rawPath == "" {
		rawPath = "."
	}
	resolved, err := t.policy.CheckReadTarget(rawPath)
	if err != nil {
		return err.Error(), true
	}

	info, err := os.Lstat(resolved)
	if err != nil {
		return fmt.Sprintf("list_files: %v", err), true
	}
	if !info.IsDir() {
		return filepath.Base(resolved), false
	}

	glob := strings.TrimSpace(args.Glob)
	if glob == "" {
		entries, err := os.ReadDir(resolved)
		if err != nil {
			return fmt.Sprintf("list_files: %v", err), true
		}
		var names []string
		truncated := false
		for _, entry := range entries {
			if ctx.Err() != nil {
				return fmt.Sprintf("list_files: %v", ctx.Err()), true
			}
			childPath := filepath.Join(resolved, entry.Name())
			if _, rerr := t.policy.CheckReadTarget(childPath); rerr != nil {
				continue
			}
			linfo, lerr := os.Lstat(childPath)
			if lerr != nil {
				continue
			}
			if len(names) >= maxListEntries {
				truncated = true
				break
			}
			name := entry.Name()
			if linfo.IsDir() {
				name += "/"
			}
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return "(empty directory)", false
		}
		out := strings.Join(names, "\n")
		if truncated {
			out += fmt.Sprintf("\n[truncated at %d entries]", maxListEntries)
		}
		return out, false
	}

	var matched []string
	truncated := false
	walkErr := filepath.WalkDir(resolved, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil
		}
		if path == resolved {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if _, rerr := t.policy.CheckReadTarget(path); rerr != nil {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(resolved, path)
		if rerr != nil {
			return nil
		}
		slashRel := filepath.ToSlash(rel)
		ok, merr := matchGlob(glob, slashRel)
		if merr != nil {
			return merr
		}
		if !ok {
			return nil
		}
		if len(matched) >= maxListEntries {
			truncated = true
			return errLimitReached
		}
		label := slashRel
		if linfo, lerr := os.Lstat(path); lerr == nil && linfo.IsDir() {
			label += "/"
		}
		matched = append(matched, label)
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errLimitReached) {
		return fmt.Sprintf("list_files: %v", walkErr), true
	}
	if len(matched) == 0 {
		return "(no matching entries)", false
	}
	out := strings.Join(matched, "\n")
	if truncated {
		out += fmt.Sprintf("\n[truncated at %d entries]", maxListEntries)
	}
	return out, false
}

func (t *Toolset) execSearchFiles(ctx context.Context, args searchFilesArgs) (string, bool) {
	if args.Pattern == "" {
		return "search_files: pattern is required", true
	}
	rawPath := strings.TrimSpace(args.Path)
	if rawPath == "" {
		rawPath = "."
	}
	resolved, err := t.policy.CheckReadTarget(rawPath)
	if err != nil {
		return err.Error(), true
	}

	expr := args.Pattern
	if args.Literal {
		expr = regexp.QuoteMeta(expr)
	}
	if args.CaseInsensitive {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return fmt.Sprintf("search_files: invalid pattern %q: %v", args.Pattern, err), true
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Sprintf("search_files: %v", err), true
	}

	glob := strings.TrimSpace(args.Glob)
	var results []string
	truncated := false

	searchOne := func(filePath, label string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, rerr := t.policy.CheckReadTarget(filePath); rerr != nil {
			return nil
		}
		fi, serr := os.Stat(filePath)
		if serr != nil || fi.IsDir() || fi.Size() > maxSearchFileSize {
			return nil
		}
		f, oerr := os.Open(filePath)
		if oerr != nil {
			return nil
		}
		defer f.Close()

		probe := make([]byte, binaryCheckBytes)
		n, rerr := f.Read(probe)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return nil
		}
		if isBinarySample(probe[:n]) {
			return nil
		}
		if _, serr := f.Seek(0, io.SeekStart); serr != nil {
			return nil
		}

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if !re.MatchString(line) {
				continue
			}
			if len(results) >= maxSearchMatches {
				truncated = true
				return errLimitReached
			}
			if len(line) > maxSearchLineLen {
				line = truncateUTF8(line, maxSearchLineLen) + "..."
			}
			results = append(results, fmt.Sprintf("%s:%d:%s", label, lineNo, line))
		}
		return nil
	}

	if !info.IsDir() {
		if err := searchOne(resolved, filepath.Base(resolved)); err != nil && !errors.Is(err, errLimitReached) {
			return fmt.Sprintf("search_files: %v", err), true
		}
	} else {
		walkErr := filepath.WalkDir(resolved, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				if path != resolved {
					if _, rerr := t.policy.CheckReadTarget(path); rerr != nil {
						return filepath.SkipDir
					}
				}
				return nil
			}
			// Skip symlinks during recursive directory search to avoid escaping or loops.
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			rel, rerr := filepath.Rel(resolved, path)
			if rerr != nil {
				return nil
			}
			slashRel := filepath.ToSlash(rel)
			if glob != "" {
				ok, merr := matchGlob(glob, slashRel)
				if merr != nil {
					return merr
				}
				if !ok {
					return nil
				}
			}
			return searchOne(path, slashRel)
		})
		if walkErr != nil && !errors.Is(walkErr, errLimitReached) {
			return fmt.Sprintf("search_files: %v", walkErr), true
		}
	}

	if len(results) == 0 {
		return "no matches", false
	}
	out := strings.Join(results, "\n")
	if truncated {
		out += fmt.Sprintf("\n[truncated at %d matches]", maxSearchMatches)
	}
	return out, false
}

// matchGlob matches pattern against a slash-separated relative path or its
// basename, supporting simple "**/" recursive prefixes.
func matchGlob(pattern, slashRel string) (bool, error) {
	pattern = filepath.ToSlash(pattern)
	if strings.HasPrefix(pattern, "**/") {
		sub := strings.TrimPrefix(pattern, "**/")
		if ok, err := filepath.Match(sub, slashRel); err != nil || ok {
			return ok, err
		}
		if ok, err := filepath.Match(sub, filepath.Base(slashRel)); err != nil || ok {
			return ok, err
		}
		// Try matching against every path suffix for patterns like "**/sub/*.go".
		parts := strings.Split(slashRel, "/")
		for i := 1; i < len(parts); i++ {
			candidate := strings.Join(parts[i:], "/")
			if ok, err := filepath.Match(sub, candidate); err != nil || ok {
				return ok, err
			}
		}
		return false, nil
	}
	if strings.Contains(pattern, "/") {
		return filepath.Match(pattern, slashRel)
	}
	return filepath.Match(pattern, filepath.Base(slashRel))
}

func isBinarySample(sample []byte) bool {
	if len(sample) == 0 {
		return false
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	// Trim up to 3 trailing bytes in case the fixed-size probe split a multi-byte UTF-8 rune.
	check := sample
	for i := 0; i < 3 && len(check) > 0 && !utf8.Valid(check); i++ {
		if utf8.RuneStart(check[len(check)-1]) {
			check = check[:len(check)-1]
			break
		}
		check = check[:len(check)-1]
	}
	return !utf8.Valid(check)
}

func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	n := maxBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
