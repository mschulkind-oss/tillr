// Package skip builds Go `-skip` patterns from the tracked disabled-test list. It sits
// below both shards and goaffected, which cannot import each other.
package skip

import (
	"regexp"
	"sort"
	"strings"
)

// Subtests builds the Go -skip pattern for the subtest selectors in listed.
//
// Execution against Go's testing/match.go fixes two rules: slash-separated patterns are
// never wrapped in parentheses, because the matcher splits on "/" only at parenthesis
// depth zero, so the selectors are joined by an unparenthesized top-level "|" with each
// segment anchored; and a selector under a listed ancestor is pruned, because the
// matcher returns the first matching alternative even when it matches only in part.
// Top-level selectors are left out: a caller that runs through Assign removes them from
// the run list instead.
func Subtests(listed []string) string { return build(listed, false) }

// Full builds the Go -skip pattern for a direct `go test` call over one package: every
// listed top-level test (^TestName$) and every listed subtest (^TestParent$/^sub$), under
// the same two rules as Subtests. A direct call has no Assign step that could remove
// top-level tests.
func Full(listed []string) string { return build(listed, true) }

func build(listed []string, topLevel bool) string {
	set := make(map[string]struct{}, len(listed))
	for _, sel := range listed {
		set[sel] = struct{}{}
	}
	var patterns []string
	for sel := range set {
		if (!topLevel && !strings.Contains(sel, "/")) || hasListedAncestor(sel, set) {
			continue
		}
		segs := strings.Split(sel, "/")
		for i, s := range segs {
			segs[i] = "^" + regexp.QuoteMeta(s) + "$"
		}
		patterns = append(patterns, strings.Join(segs, "/"))
	}
	sort.Strings(patterns)
	return strings.Join(patterns, "|")
}

func hasListedAncestor(sel string, set map[string]struct{}) bool {
	for i := 0; i < len(sel); i++ {
		if sel[i] == '/' {
			if _, ok := set[sel[:i]]; ok {
				return true
			}
		}
	}
	return false
}
