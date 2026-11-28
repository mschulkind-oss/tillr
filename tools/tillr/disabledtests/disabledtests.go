// Package disabledtests reads and validates the tracked list of disabled tests.
//
// The list is one JSON file whose path is set by `.tillr.toml` `[gate] disabled_tests`.
// An entry names either a whole gate check by its id or one Go test (or subtest) by
// the selector `go-test:<module>:<package-import-path>:<TestName>[/<subtest>...]`.
package disabledtests

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
)

const (
	// Version is the only schema version the list is read at.
	Version = 1
	// SourceTrunkProof marks an entry the trunk proof added.
	SourceTrunkProof = "trunk-proof"
	// SourceHand marks an entry a person added.
	SourceHand = "hand"

	goSelectorPrefix = "go-test:"
)

var (
	checkIDRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	moduleRe       = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)
	importPathRe   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./~+-]*$`)
	topLevelTestRe = regexp.MustCompile(`^(Test|Example|Fuzz)\w*$`)
)

// Entry is one disabled test or whole check.
type Entry struct {
	Selector  string `json:"selector"`
	Source    string `json:"source"`
	FailedOn  string `json:"failed_on,omitempty"`
	FirstLine string `json:"first_line,omitempty"`
}

// List is the parsed disabled-test file.
type List struct {
	Version  int     `json:"version"`
	Disabled []Entry `json:"disabled"`
}

// GoSelector is the parsed form of a `go-test:` selector.
type GoSelector struct {
	Module  string
	Package string
	// Test is the top-level test name, optionally followed by /-separated subtest names.
	Test string
}

// Top returns the top-level test name.
func (g GoSelector) Top() string {
	top, _, _ := strings.Cut(g.Test, "/")
	return top
}

// IsSubtest reports whether the selector names a subtest.
func (g GoSelector) IsSubtest() bool { return strings.Contains(g.Test, "/") }

// ParseGoSelector splits a go-test selector. ok is false when sel is not one.
func ParseGoSelector(sel string) (GoSelector, bool) {
	rest, found := strings.CutPrefix(sel, goSelectorPrefix)
	if !found {
		return GoSelector{}, false
	}
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) != 3 {
		return GoSelector{}, false
	}
	g := GoSelector{Module: parts[0], Package: parts[1], Test: parts[2]}
	if !moduleRe.MatchString(g.Module) || !importPathRe.MatchString(g.Package) {
		return GoSelector{}, false
	}
	segments := strings.Split(g.Test, "/")
	if !topLevelTestRe.MatchString(segments[0]) {
		return GoSelector{}, false
	}
	for _, seg := range segments[1:] {
		if seg == "" || strings.ContainsAny(seg, " \t\r\n:") {
			return GoSelector{}, false
		}
	}
	return g, true
}

// ValidSelector reports whether sel is a go-test selector or a gate check id.
func ValidSelector(sel string) bool {
	if strings.HasPrefix(sel, goSelectorPrefix) {
		_, ok := ParseGoSelector(sel)
		return ok
	}
	return checkIDRe.MatchString(sel)
}

// Parse decodes and validates a list. Unknown fields, a wrong version, an invalid
// selector, an unknown source and duplicate selectors are all refused.
func Parse(data []byte) (List, error) {
	var list List
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&list); err != nil {
		return List{}, fmt.Errorf("not a disabled-test list: %w", err)
	}
	if dec.More() {
		return List{}, errors.New("not a disabled-test list: trailing data after the JSON object")
	}
	if err := list.Validate(); err != nil {
		return List{}, err
	}
	return list, nil
}

// Validate checks the schema rules.
func (l List) Validate() error {
	if l.Version != Version {
		return fmt.Errorf("version must be %d, got %d", Version, l.Version)
	}
	seen := make(map[string]struct{}, len(l.Disabled))
	for i, e := range l.Disabled {
		if !ValidSelector(e.Selector) {
			return fmt.Errorf("disabled[%d]: selector %q is neither a go-test:<module>:<package>:<Test>[/<sub>] selector nor a check id", i, e.Selector)
		}
		if _, dup := seen[e.Selector]; dup {
			return fmt.Errorf("disabled[%d]: selector %q is listed twice", i, e.Selector)
		}
		seen[e.Selector] = struct{}{}
		if e.Source != SourceTrunkProof && e.Source != SourceHand {
			return fmt.Errorf("disabled[%d]: source %q must be %q or %q", i, e.Source, SourceTrunkProof, SourceHand)
		}
	}
	return nil
}

// Load reads the list at path. A missing file is an empty list: no test is disabled.
func Load(path string) (List, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return List{Version: Version}, nil
	}
	if err != nil {
		return List{}, err
	}
	list, err := Parse(data)
	if err != nil {
		return List{}, fmt.Errorf("%s: %w", path, err)
	}
	return list, nil
}

// GoSelectors returns the parsed go-test selectors of one module, sorted by package then test.
func (l List) GoSelectors(module string) []GoSelector {
	var out []GoSelector
	for _, e := range l.Disabled {
		if g, ok := ParseGoSelector(e.Selector); ok && g.Module == module {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		return out[i].Test < out[j].Test
	})
	return out
}

// Checks returns the whole-check ids on the list.
func (l List) Checks() []string {
	var out []string
	for _, e := range l.Disabled {
		if !strings.HasPrefix(e.Selector, goSelectorPrefix) {
			out = append(out, e.Selector)
		}
	}
	return out
}
