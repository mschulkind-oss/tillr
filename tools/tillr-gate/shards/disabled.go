package shards

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mschulkind-oss/tillr-gate/internal/skip"
	"github.com/mschulkind-oss/tillr/disabledtests"
	"github.com/mschulkind-oss/tillr/settings"
)

// DisabledSet holds the tests the tracked disabled list excludes from one module's run.
// Selectors are keyed by package import path; each is a top-level test name, optionally
// followed by /-separated subtest names. Plan records which selectors applied, so the
// summary can count them. A nil *DisabledSet disables nothing.
type DisabledSet struct {
	byPackage map[string][]string

	mu      sync.Mutex
	applied map[string]struct{}
}

// NewDisabledSet builds a set from package import path to listed test selectors.
func NewDisabledSet(byPackage map[string][]string) *DisabledSet {
	return &DisabledSet{byPackage: byPackage, applied: map[string]struct{}{}}
}

// Applied returns how many listed selectors matched a discovered test, sorted as
// "<package>:<test>" strings.
func (d *DisabledSet) Applied() []string {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.applied))
	for k := range d.applied {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Plan applies the set to one package's discovered top-level test names, before they
// are assigned to shards, and returns names without the listed top-level tests. A selector
// whose top-level test is not among names is stale: it is neither applied nor counted.
func (d *DisabledSet) Plan(pkg string, names []string) []string {
	if d == nil || len(d.byPackage[pkg]) == 0 {
		return names
	}
	present := make(map[string]struct{}, len(names))
	for _, n := range names {
		present[n] = struct{}{}
	}
	listed := make(map[string]struct{})
	var live []string
	for _, sel := range d.byPackage[pkg] {
		top, _, _ := strings.Cut(sel, "/")
		if _, ok := present[top]; !ok {
			continue
		}
		listed[sel] = struct{}{}
		live = append(live, sel)
	}
	d.mu.Lock()
	for _, sel := range live {
		d.applied[pkg+":"+sel] = struct{}{}
	}
	d.mu.Unlock()
	var kept []string
	for _, n := range names {
		if _, off := listed[n]; !off {
			kept = append(kept, n)
		}
	}
	return kept
}

// SkipFor returns the Go -skip pattern for the listed subtests of pkg, or "" when none.
func (d *DisabledSet) SkipFor(pkg string) string {
	if d == nil {
		return ""
	}
	return SkipPattern(d.byPackage[pkg])
}

// SkipPattern builds the Go -skip pattern for the subtest selectors in listed; see
// skip.Subtests. Top-level selectors are left out: they are removed from the run list instead.
func SkipPattern(listed []string) string { return skip.Subtests(listed) }

// FailedTest is one test that failed in a run, with only its own output.
type FailedTest struct {
	Package string
	// Test is the leaf name, such as "TestWhy/resumed_segment".
	Test   string
	Output string
}

// FailedTests attributes failures to leaf tests. A test that emitted "fail" is named only
// when no descendant ("<Test>/...") of it also failed in the same package, so a failing
// subtest is named without its parent or its passing siblings, and a parent that failed
// outside every subtest is named itself. Each result carries only its own output: the
// test's own lines, and its ancestors' lines written before the ancestor's first subtest
// started (the preamble).
func FailedTests(events []TestEvent) []FailedTest {
	type key struct{ pkg, test string }
	failed := make(map[key]struct{})
	var order []key
	for _, ev := range events {
		if ev.Action == goTestActionFail && ev.Test != "" {
			k := key{ev.Package, ev.Test}
			if _, ok := failed[k]; !ok {
				failed[k] = struct{}{}
				order = append(order, k)
			}
		}
	}
	var leaves []key
	for _, k := range order {
		leaf := true
		for other := range failed {
			if other.pkg == k.pkg && strings.HasPrefix(other.test, k.test+"/") {
				leaf = false
				break
			}
		}
		if leaf {
			leaves = append(leaves, k)
		}
	}
	// firstChild[k] is the event index at which k's first subtest started.
	firstChild := make(map[key]int)
	for i, ev := range events {
		if ev.Test == "" || !strings.Contains(ev.Test, "/") {
			continue
		}
		parts := strings.Split(ev.Test, "/")
		for n := 1; n < len(parts); n++ {
			k := key{ev.Package, strings.Join(parts[:n], "/")}
			if _, ok := firstChild[k]; !ok {
				firstChild[k] = i
			}
		}
	}
	out := make([]FailedTest, 0, len(leaves))
	for _, leaf := range leaves {
		var b strings.Builder
		for i, ev := range events {
			if ev.Output == "" || ev.Package != leaf.pkg || ev.Test == "" {
				continue
			}
			if ev.Test == leaf.test {
				b.WriteString(ev.Output)
				continue
			}
			if strings.HasPrefix(leaf.test, ev.Test+"/") {
				if first, ok := firstChild[key{ev.Package, ev.Test}]; ok && i < first {
					b.WriteString(ev.Output)
				}
			}
		}
		out = append(out, FailedTest{Package: leaf.pkg, Test: leaf.test, Output: b.String()})
	}
	return out
}

// FullSkipPattern builds the Go -skip pattern for a direct `go test` call over one package:
// listed top-level tests and listed subtests together; see skip.Full.
func FullSkipPattern(listed []string) string { return skip.Full(listed) }

// LoadModuleSelectors reads the tracked disabled list of the repository at root and returns
// the listed go-test selectors of module (slash-separated, relative to root) keyed by package
// import path, less any selector named in prove. The list path comes from `.tillr.toml`
// `[gate] disabled_tests`; a missing list disables nothing, and a malformed one is an error.
func LoadModuleSelectors(root, module string, prove []string) (map[string][]string, error) {
	cfg, err := settings.Read(root)
	if err != nil {
		return nil, err
	}
	list, err := disabledtests.Load(filepath.Join(root, filepath.FromSlash(cfg.DisabledTestsPath())))
	if err != nil {
		return nil, err
	}
	return SelectorsByPackage(list, module, prove), nil
}

// SelectorsByPackage groups the go-test selectors of list that belong to module by package
// import path, less any selector named in prove.
func SelectorsByPackage(list disabledtests.List, module string, prove []string) map[string][]string {
	proved := make(map[string]struct{}, len(prove))
	for _, sel := range prove {
		proved[sel] = struct{}{}
	}
	byPackage := map[string][]string{}
	for _, e := range list.Disabled {
		if _, ok := proved[e.Selector]; ok {
			continue
		}
		g, ok := disabledtests.ParseGoSelector(e.Selector)
		if !ok || g.Module != module {
			continue
		}
		byPackage[g.Package] = append(byPackage[g.Package], g.Test)
	}
	return byPackage
}

// loadDisabledSet reads the tracked disabled list of the repository holding module and
// returns the module's tests, less any selector named in prove. The list path comes from
// `.tillr.toml` `[gate] disabled_tests`; a missing list disables nothing. A module outside
// a repository disables nothing.
func loadDisabledSet(module string, prove []string) (*DisabledSet, error) {
	root := LocateRepoRoot(module, false, true)
	if root == "" {
		return nil, nil
	}
	rel, err := filepath.Rel(root, module)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, nil
	}
	rel = filepath.ToSlash(rel)
	byPackage, err := LoadModuleSelectors(root, rel, prove)
	if err != nil {
		return nil, err
	}
	return NewDisabledSet(byPackage), nil
}
