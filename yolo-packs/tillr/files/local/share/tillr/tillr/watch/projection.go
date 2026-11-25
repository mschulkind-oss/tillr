package watch

import (
	"fmt"
	"regexp"
	"strings"
)

// CharterFacts are the store charter values exposed to watch rules.
type CharterFacts struct {
	Name, Ready, Blocked, Closed string
	States, Finished             []string
}

// A read projection fact can be unavailable; that error is itself cached.
type projectionFact[T any] struct {
	value T
	err   error
}

type unreadableProjection struct {
	name string
	err  error
}

func (e unreadableProjection) Error() string { return fmt.Sprintf("%s: %v", e.name, e.err) }

func namedScope(scope, prefix string) string {
	name, ok := strings.CutPrefix(scope, prefix)
	if !ok || name == "*" {
		return ""
	}
	return name
}

func textList(v []string) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = s
	}
	return out
}

type gitRuleFacts struct {
	git   gitSnapshot
	extra map[string]any
}

func (p gitRuleFacts) forSubject(_ string) (lookup func(projectionOperand) any, inScope bool, err error) {
	return func(op projectionOperand) any {
		if op.name == "git.ref" {
			return p.git.refs[op.ref].value
		}
		if p.extra != nil {
			return p.extra[op.name]
		}
		return nil
	}, true, nil
}

const goTestPrefix = "go-test:"

var goToolModule = regexp.MustCompile(`^tools/[a-z0-9-]+$`)

func fixerChecksFromText(text string) []string {
	_, rest, ok := strings.Cut(text, "(red: ")
	if !ok {
		return nil
	}
	list, _, ok := strings.Cut(rest, ")")
	if !ok {
		return nil
	}
	var checks []string
	for _, id := range strings.Split(list, ",") {
		if id = strings.TrimSpace(id); id != "" {
			checks = append(checks, id)
		}
	}
	return checks
}

func provableChecks(checks []string) []string {
	out := []string{}
	for _, check := range checks {
		labelled := false
		if !strings.Contains(check, "/") {
			for _, other := range checks {
				if strings.HasPrefix(other, check+"/") {
					labelled = true
					break
				}
			}
		}
		if !labelled {
			out = append(out, check)
		}
	}
	return out
}

func goSuiteProofID(id string) string {
	test, ok := strings.CutPrefix(id, goTestPrefix)
	if !ok {
		return ""
	}
	module, _, ok := strings.Cut(test, ":")
	if !ok || !goToolModule.MatchString(module) {
		return ""
	}
	slug := strings.Join(strings.FieldsFunc(module, func(r rune) bool { return r == '/' || r == '-' }), "-")
	return "go-tool/" + slug + "-s-whole-suite-passes"
}

func proofCoversChecks(passed, checks []string) bool {
	if len(checks) == 0 || len(passed) == 0 {
		return false
	}
	passedSet := make(map[string]bool, len(passed))
	for _, id := range passed {
		passedSet[id] = true
	}
	for _, check := range checks {
		if passedSet[check] {
			continue
		}
		if suite := goSuiteProofID(check); suite != "" && passedSet[suite] {
			continue
		}
		if !strings.Contains(check, "/") {
			matched := false
			prefix := check + "/"
			for _, p := range passed {
				if strings.HasPrefix(p, prefix) {
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}
		return false
	}
	return true
}
