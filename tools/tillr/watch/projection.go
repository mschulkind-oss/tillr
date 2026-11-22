package watch

import (
	"fmt"
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
