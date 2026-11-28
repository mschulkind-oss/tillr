package drain

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/settings"
)

// ResolveConfigDir returns the per-user tillr configuration directory:
// $TILLR_CONFIG_DIR (must be absolute), else $XDG_CONFIG_HOME/tillr (when absolute),
// else $HOME/.config/tillr.
func ResolveConfigDir() (string, error) {
	if dir := os.Getenv("TILLR_CONFIG_DIR"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", Usagef("TILLR_CONFIG_DIR=%q must be an absolute path", dir)
		}
		return filepath.Clean(dir), nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "tillr"), nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		return "", Usagef("cannot resolve tillr configuration directory: HOME is not set")
	}
	return filepath.Join(home, ".config", "tillr"), nil
}

// ListProfileNames returns the sorted profile names from backend-policy.<name>.json
// files in configDir, ignoring malformed filenames and directories. A missing
// configDir returns no names and a nil error.
func ListProfileNames(configDir string) ([]string, error) {
	entries, err := os.ReadDir(configDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "backend-policy.") || !strings.HasSuffix(name, ".json") || len(name) <= len("backend-policy.")+len(".json") {
			continue
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(name, "backend-policy."), ".json")
		if !settings.ValidProfileName(mid) {
			continue
		}
		info, err := os.Stat(filepath.Join(configDir, name))
		if err != nil || info.IsDir() {
			continue
		}
		names = append(names, mid)
	}
	sort.Strings(names)
	return names, nil
}

// ResolveProfilePath resolves a named seat profile to <config-dir>/backend-policy.<name>.json.
// When the file does not exist, it refuses with exit 2 listing the profile names that exist.
func ResolveProfilePath(name string) (string, error) {
	if !settings.ValidProfileName(name) {
		return "", Usagef("profile %q: invalid profile name (must match [a-z0-9][a-z0-9_-]*)", name)
	}
	configDir, err := ResolveConfigDir()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(configDir, "backend-policy."+name+".json")
	info, err := os.Stat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			existing, listErr := ListProfileNames(configDir)
			if listErr != nil {
				return "", Usagef("profile %q not found in %s (%v)", name, configDir, listErr)
			}
			if len(existing) == 0 {
				return "", Usagef("profile %q not found in %s (no profiles exist)", name, configDir)
			}
			return "", Usagef("profile %q not found in %s (available profiles: %s)", name, configDir, strings.Join(existing, ", "))
		}
		return "", Usagef("reading profile %q (%s): %v", name, candidate, err)
	}
	if info.IsDir() {
		return "", Usagef("profile %q (%s) is a directory, not a file", name, candidate)
	}
	return candidate, nil
}

// ParseProfileList parses a comma-separated, whitespace-trimmed list of distinct
// valid profile names. Empty input, empty entries, invalid names, and duplicates
// are refused with exit 2.
func ParseProfileList(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, Usagef("profile list must not be empty")
	}
	parts := strings.Split(raw, ",")
	names := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for i, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, Usagef("profile list %q has an empty entry at index %d", raw, i)
		}
		if !settings.ValidProfileName(name) {
			return nil, Usagef("profile list %q entry %d (%q): invalid profile name (must match [a-z0-9][a-z0-9_-]*)", raw, i, name)
		}
		if seen[name] {
			return nil, Usagef("profile list %q contains duplicate profile %q at index %d", raw, name, i)
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, nil
}

// ResolveProfilePaths resolves every profile name in an ordered list to
// <config-dir>/backend-policy.<name>.json. If any entry is missing or invalid,
// it refuses with exit 2 naming the offending profile, its index, <config-dir>,
// and the available profiles.
func ResolveProfilePaths(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, Usagef("profile list must not be empty")
	}
	seen := make(map[string]bool, len(names))
	paths := make([]string, len(names))
	for i, name := range names {
		if seen[name] {
			return nil, Usagef("profile %q (index %d of %d): duplicate profile name", name, i, len(names))
		}
		seen[name] = true
		p, err := ResolveProfilePath(name)
		if err != nil {
			if len(names) > 1 {
				return nil, Usagef("profile %q (index %d of %d): %v", name, i, len(names), err)
			}
			return nil, err
		}
		paths[i] = p
	}
	return paths, nil
}
