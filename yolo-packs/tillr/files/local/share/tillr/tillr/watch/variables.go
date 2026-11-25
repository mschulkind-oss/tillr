package watch

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
)

func validVarIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i == 0 {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_') {
				return false
			}
			continue
		}
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func validVariableEventPath(s string) bool {
	if s == "" || !validEventPath(s) || strings.HasPrefix(s, "event.") {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

type ruleVar struct {
	name         string
	path         string
	groupByIndex int
}

func lastDotSegment(path string) string {
	if idx := strings.LastIndexByte(path, '.'); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func groupByVariablePaths(w When) []string {
	if len(w.GroupByList) >= 2 {
		return w.GroupByList
	}
	if len(w.GroupByAny) > 0 {
		return []string{w.GroupByAny[0]}
	}
	if w.GroupBy != "" && w.GroupBy != "global" {
		return []string{w.GroupBy}
	}
	return nil
}

func resolveVariables(r Rule) ([]ruleVar, error) {
	groupByPaths := groupByVariablePaths(r.When)
	groupByIndexByPath := make(map[string]int, len(groupByPaths))
	for i, p := range groupByPaths {
		groupByIndexByPath[p] = i
	}

	aliasForGroupBy := make(map[int]string)
	var extraVars []ruleVar
	for _, name := range slices.Sorted(maps.Keys(r.Variables)) {
		path := r.Variables[name]
		if idx, ok := groupByIndexByPath[path]; ok {
			if prev, exists := aliasForGroupBy[idx]; exists {
				return nil, fmt.Errorf("duplicate alias for group_by path %q (%q and %q)", path, prev, name)
			}
			aliasForGroupBy[idx] = name
		} else {
			extraVars = append(extraVars, ruleVar{name: name, path: path, groupByIndex: -1})
		}
	}

	seenName := make(map[string]string, len(groupByPaths)+len(extraVars))
	vars := make([]ruleVar, 0, len(groupByPaths)+len(extraVars))
	for i, p := range groupByPaths {
		name := lastDotSegment(p)
		if alias, ok := aliasForGroupBy[i]; ok {
			name = alias
		}
		if prevPath, exists := seenName[name]; exists {
			return nil, fmt.Errorf("variable name %q collides between %q and %q", name, prevPath, p)
		}
		seenName[name] = p
		vars = append(vars, ruleVar{name: name, path: p, groupByIndex: i})
	}
	for _, ev := range extraVars {
		if prevPath, exists := seenName[ev.name]; exists {
			return nil, fmt.Errorf("variable name %q collides between %q and %q", ev.name, prevPath, ev.path)
		}
		seenName[ev.name] = ev.path
		vars = append(vars, ev)
	}
	return vars, nil
}

// VariableNames returns the rule's ordered variable names derived from group_by
// and optional [rule.variables].
func (r Rule) VariableNames() []string {
	vars, err := resolveVariables(r)
	if err != nil || len(vars) == 0 {
		return nil
	}
	names := make([]string, len(vars))
	for i, v := range vars {
		names[i] = v.name
	}
	return names
}

func extractVariables(r Rule, subjectKey string, c *condition) map[string]string {
	out := make(map[string]string)
	vars, err := resolveVariables(r)
	if err != nil || len(vars) == 0 {
		return out
	}

	var composite []string
	var compositeOK bool
	if len(r.When.GroupByList) >= 2 {
		composite, compositeOK = decodeCompositeSubject(subjectKey, len(r.When.GroupByList))
	}

	var newestLookup func(string) any
	if c != nil && len(c.events) > 0 {
		newestLookup = c.events[len(c.events)-1].lookup
	}

	for _, v := range vars {
		if v.groupByIndex >= 0 {
			if len(r.When.GroupByList) >= 2 {
				if compositeOK && v.groupByIndex < len(composite) {
					out[v.name] = composite[v.groupByIndex]
				}
			} else {
				out[v.name] = subjectKey
			}
			continue
		}
		if newestLookup != nil {
			if s, ok := newestLookup(v.path).(string); ok && s != "" {
				out[v.name] = s
			}
		}
	}
	return out
}

func encodeVariablesJSON(vars map[string]string) string {
	if len(vars) == 0 {
		return "{}"
	}
	data, err := json.Marshal(vars)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func decodeVariablesJSON(raw string) map[string]string {
	out := make(map[string]string)
	if raw == "" || raw == "{}" || raw == "null" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		return map[string]string{}
	}
	return out
}
