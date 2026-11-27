package native

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxSymlinkHops = 40
	denyListRel    = "config/tillr/deny-list.json"
	doItNowFile    = "do-it-now"
)

// FencePolicy configures pre-write target gating (CheckWriteTarget) and
// secret-path read refusals (CheckReadTarget) for in-process native tools.
type FencePolicy struct {
	WorkDir       string
	ArtifactDir   string
	FencePath     string
	FencePrefixes []string
	ArtifactsDir  string
	DurableDir    string
	ProgressFile  string
	ReadOnly      bool
	PlanQuickWay  bool
	DenyListPath  string
	HomeDir       string
}

type denyListDoc struct {
	Entries []denyListEntry `json:"entries"`
}

type denyListEntry struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

// ParseFenceEntries returns the non-empty trimmed path prefixes from fence.txt.
func ParseFenceEntries(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if entry := strings.TrimSpace(line); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

// InsideFence reports whether a slash-separated repo-relative path matches at
// least one prefix in fence. Matches drain/workflow.InsideFence semantics.
func InsideFence(rel string, fence []string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	for _, entry := range fence {
		e := filepath.ToSlash(strings.TrimSpace(entry))
		if e == "" {
			continue
		}
		trimmed := strings.TrimSuffix(e, "/")
		if rel == trimmed || strings.HasPrefix(rel, trimmed+"/") {
			return true
		}
	}
	return false
}

// effectiveHome returns the home directory used for secret-read denylist rules.
func (p *FencePolicy) effectiveHome() string {
	if strings.TrimSpace(p.HomeDir) != "" {
		return p.HomeDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// loadFencePrefixes returns the active lane fence prefixes, loading fence.txt
// when FencePrefixes was not supplied directly.
func (p *FencePolicy) loadFencePrefixes() ([]string, error) {
	if p.FencePrefixes != nil {
		return p.FencePrefixes, nil
	}
	fencePath := p.FencePath
	if fencePath == "" && p.ArtifactDir != "" {
		fencePath = filepath.Join(p.ArtifactDir, "fence.txt")
	}
	if fencePath == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(fencePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading lane fence %s: %w", fencePath, err)
	}
	return ParseFenceEntries(string(raw)), nil
}

// loadDenyList returns path entries from config/tillr/deny-list.json if present.
func (p *FencePolicy) loadDenyList() ([]denyListEntry, error) {
	path := p.DenyListPath
	if path == "" && p.WorkDir != "" {
		path = filepath.Join(p.WorkDir, filepath.FromSlash(denyListRel))
	}
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading deny-list %s: %w", path, err)
	}
	var doc denyListDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing deny-list %s: %w", path, err)
	}
	return doc.Entries, nil
}

// planQuickWayUnlocked reports whether a quick-way plan node has already
// written <ArtifactsDir>/do-it-now to unlock worktree writes.
func (p *FencePolicy) planQuickWayUnlocked() bool {
	if !p.PlanQuickWay || p.ArtifactsDir == "" {
		return false
	}
	marker := filepath.Join(p.ArtifactsDir, doItNowFile)
	info, err := os.Stat(marker)
	if err != nil || info.IsDir() {
		return false
	}
	physMarker, err := resolvePhysicalPath(marker, "")
	if err != nil {
		return false
	}
	physArtifacts, err := resolvePhysicalPath(p.ArtifactsDir, "")
	if err != nil {
		return false
	}
	rel, ok := withinDir(physMarker, physArtifacts)
	return ok && rel != "."
}

// CheckWriteTarget validates rawPath before any file is opened or created on disk.
// It resolves symlinks on the deepest existing ancestor (and dangling leaf symlinks)
// and enforces read_only access, plan do-it-now unlock, .git protection,
// config/tillr/deny-list.json, and fence.txt prefixes.
func (p *FencePolicy) CheckWriteTarget(rawPath string) (string, error) {
	if strings.TrimSpace(rawPath) == "" {
		return "", errors.New("write refused: path is empty")
	}
	phys, err := resolvePhysicalPath(rawPath, p.WorkDir)
	if err != nil {
		return "", fmt.Errorf("write refused: resolving %q: %w", rawPath, err)
	}

	// 1. $TILLR_PROGRESS_FILE is always writable.
	if p.ProgressFile != "" {
		physProg, err := resolvePhysicalPath(p.ProgressFile, "")
		if err == nil && phys == physProg {
			return phys, nil
		}
	}

	// 2. Declared workflow node outputs directory (<root>/.tillr/loop/<run-id>/artifacts/).
	if p.ArtifactsDir != "" {
		physArt, err := resolvePhysicalPath(p.ArtifactsDir, "")
		if err == nil {
			if rel, ok := withinDir(phys, physArt); ok && rel != "." {
				return phys, nil
			}
		}
	}

	// 3. The run's scratch directory (DurableDir).
	if p.DurableDir != "" {
		physDur, err := resolvePhysicalPath(p.DurableDir, "")
		if err == nil {
			if rel, ok := withinDir(phys, physDur); ok && rel != "." {
				return phys, nil
			}
		}
	}

	// 4. Target working directory (<workDir>), subject to read_only, .git, deny-list, and fence.txt.
	if p.WorkDir != "" {
		physWork, err := resolvePhysicalPath(p.WorkDir, "")
		if err != nil {
			return "", fmt.Errorf("write refused: resolving workDir: %w", err)
		}
		if rel, ok := withinDir(phys, physWork); ok {
			if rel == "." {
				return "", fmt.Errorf("write refused: %q is the worktree root directory", rawPath)
			}
			if p.ReadOnly && !p.planQuickWayUnlocked() {
				if p.PlanQuickWay {
					return "", fmt.Errorf("write refused: run is read_only; write %s first to unlock worktree writes on plan seat", filepath.Join(p.ArtifactsDir, doItNowFile))
				}
				return "", fmt.Errorf("write refused: run is read_only; writes to %s inside worktree are not permitted (only artifacts and scratch are writable)", filepath.ToSlash(rel))
			}

			slashRel := filepath.ToSlash(rel)
			if slashRel == ".git" || strings.HasPrefix(slashRel, ".git/") {
				return "", fmt.Errorf("write refused: %s is inside .git internals", slashRel)
			}

			denyEntries, err := p.loadDenyList()
			if err != nil {
				return "", fmt.Errorf("write refused: %w", err)
			}
			for _, entry := range denyEntries {
				if entry.Path == "" {
					continue
				}
				ep := filepath.ToSlash(strings.TrimSuffix(strings.TrimSpace(entry.Path), "/"))
				if slashRel == ep || strings.HasPrefix(slashRel, ep+"/") {
					return "", fmt.Errorf("write refused: %s matches deny-list entry %q (%s)", slashRel, entry.Path, entry.Reason)
				}
			}

			prefixes, err := p.loadFencePrefixes()
			if err != nil {
				return "", fmt.Errorf("write refused: %w", err)
			}
			if len(prefixes) > 0 && !InsideFence(slashRel, prefixes) {
				return "", fmt.Errorf("write refused: %s is outside lane fence (permitted prefixes: %s)", slashRel, strings.Join(prefixes, ", "))
			}
			return phys, nil
		}
	}

	prefixes, _ := p.loadFencePrefixes()
	if len(prefixes) > 0 {
		return "", fmt.Errorf("write refused: %q resolves to %s outside permitted roots (worktree fence prefixes: %s)", rawPath, phys, strings.Join(prefixes, ", "))
	}
	return "", fmt.Errorf("write refused: %q resolves to %s outside permitted worktree, artifacts, and scratch roots", rawPath, phys)
}

// CheckReadTarget validates rawPath for read_file, list_files, and search_files,
// refusing credential and process-environment locations.
func (p *FencePolicy) CheckReadTarget(rawPath string) (string, error) {
	if strings.TrimSpace(rawPath) == "" {
		return "", errors.New("read refused: path is empty")
	}
	absRaw, err := absPath(rawPath, p.WorkDir)
	if err != nil {
		return "", fmt.Errorf("read refused: resolving %q: %w", rawPath, err)
	}
	if reason := p.secretPathReason(absRaw); reason != "" {
		return "", fmt.Errorf("read refused: %q targets protected credential or environment path (%s)", rawPath, reason)
	}
	phys, err := resolvePhysicalPath(rawPath, p.WorkDir)
	if err != nil {
		return "", fmt.Errorf("read refused: resolving %q: %w", rawPath, err)
	}
	if reason := p.secretPathReason(phys); reason != "" {
		return "", fmt.Errorf("read refused: %q resolves to protected credential or environment path (%s)", rawPath, reason)
	}
	return phys, nil
}

func (p *FencePolicy) secretPathReason(candidate string) string {
	clean := filepath.ToSlash(filepath.Clean(candidate))
	// /proc/*/environ (including /proc/self/environ and /proc/<pid>/task/<tid>/environ).
	if strings.HasPrefix(clean, "/proc/") && strings.HasSuffix(clean, "/environ") {
		return "/proc/*/environ"
	}

	home := p.effectiveHome()
	if home == "" {
		return ""
	}
	// Check against both raw home and physically resolved home (for symlinked temp homes).
	homes := []string{filepath.Clean(home)}
	if physHome, err := resolvePhysicalPath(home, ""); err == nil && physHome != homes[0] {
		homes = append(homes, physHome)
	}

	for _, h := range homes {
		hSlash := filepath.ToSlash(h)
		for _, denied := range []string{
			".config/yolo-agent-env",
			".config/tillr",
			".aws",
			".ssh",
			".claude",
		} {
			prefix := hSlash + "/" + denied
			if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
				return "~/" + denied
			}
		}
		piAgentPrefix := hSlash + "/.pi/agent/"
		if strings.HasPrefix(clean, piAgentPrefix) {
			rest := strings.TrimPrefix(clean, piAgentPrefix)
			seg, _, _ := strings.Cut(rest, "/")
			if strings.HasPrefix(seg, "auth") {
				return "~/.pi/agent/auth*"
			}
		}
	}
	return ""
}

func absPath(rawPath, workDir string) (string, error) {
	if filepath.IsAbs(rawPath) {
		return filepath.Clean(rawPath), nil
	}
	if workDir != "" {
		return filepath.Abs(filepath.Join(workDir, rawPath))
	}
	return filepath.Abs(rawPath)
}

// resolvePhysicalPath resolves symlinks along rawPath (relative to workDir),
// handling non-existent descendants and dangling leaf symlinks safely.
func resolvePhysicalPath(rawPath, workDir string) (string, error) {
	abs, err := absPath(rawPath, workDir)
	if err != nil {
		return "", err
	}
	return resolvePhysicalHops(abs, 0)
}

func resolvePhysicalHops(abs string, hops int) (string, error) {
	if hops > maxSymlinkHops {
		return "", errors.New("too many symlink levels")
	}
	abs = filepath.Clean(abs)
	// Fast path: path exists and resolves cleanly.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	}

	// Walk up until we find the deepest existing entry (via Lstat so dangling
	// symlinks are detected rather than skipped as non-existent).
	var tail []string
	cur := abs
	for {
		info, err := os.Lstat(cur)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				// Existing symlink whose target may be dangling.
				linkTarget, rerr := os.Readlink(cur)
				if rerr != nil {
					return "", rerr
				}
				var next string
				if filepath.IsAbs(linkTarget) {
					next = linkTarget
				} else {
					parentPhys, perr := resolvePhysicalHops(filepath.Dir(cur), hops+1)
					if perr != nil {
						return "", perr
					}
					next = filepath.Join(parentPhys, linkTarget)
				}
				resolvedTarget, rerr := resolvePhysicalHops(next, hops+1)
				if rerr != nil {
					return "", rerr
				}
				return joinTail(resolvedTarget, tail), nil
			}
			resolved, rerr := filepath.EvalSymlinks(cur)
			if rerr != nil {
				return "", rerr
			}
			return joinTail(filepath.Clean(resolved), tail), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return joinTail(cur, tail), nil
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

func joinTail(base string, revTail []string) string {
	out := base
	for i := len(revTail) - 1; i >= 0; i-- {
		out = filepath.Join(out, revTail[i])
	}
	return filepath.Clean(out)
}

func withinDir(path, dir string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}
