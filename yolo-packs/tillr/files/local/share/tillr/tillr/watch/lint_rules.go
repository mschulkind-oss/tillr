package watch

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2"
)

type lintedRule struct {
	source string
	rule   Rule
}

func resolveRulesDir(absRoot, rulesPath string) (string, bool) {
	if rulesPath != "" {
		info, err := os.Stat(rulesPath)
		if err == nil && info.IsDir() {
			return rulesPath, true
		}
		return "", false
	}
	defaultDir := filepath.Join(absRoot, "config", "tillr", "watch")
	info, err := os.Stat(defaultDir)
	if err == nil && info.IsDir() {
		return defaultDir, true
	}
	return "", false
}

func discoverAndLoadRuleLayers(absRoot, rulesPath string, shippedFS fs.FS, noShipped bool, kinds map[string]bool, fc *findingCollector) []lintedRule {
	var shippedRules []lintedRule
	shippedTables := make(map[string]map[string]any)
	if !noShipped && shippedFS != nil {
		shippedSeenIDs := make(map[string]string)
		_ = fs.WalkDir(shippedFS, ".", func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				fc.add("shipped:"+filepath.ToSlash(path), CodeWatchLintInput, err.Error())
				return nil
			}
			if entry.IsDir() || filepath.Ext(path) != ".toml" {
				return nil
			}
			source := "shipped:" + filepath.ToSlash(path)
			data, err := fs.ReadFile(shippedFS, path)
			if err != nil {
				fc.add(source, CodeWatchLintInput, fmt.Sprintf("reading %q: %v", source, err))
				return nil
			}
			loaded, tables := lintShippedRuleData(source, data, kinds, shippedSeenIDs, fc)
			shippedRules = append(shippedRules, loaded...)
			for id, tbl := range tables {
				shippedTables[id] = tbl
			}
			return nil
		})
	}

	var paths []string
	if rulesPath != "" {
		linfo, err := os.Lstat(rulesPath)
		if err != nil {
			fc.add(rulesPath, CodeWatchLintInput, fmt.Sprintf("rules %q: %v", rulesPath, err))
		} else if linfo.Mode()&fs.ModeSymlink != 0 {
			fc.add(rulesPath, CodeWatchLintRuleSchema, "rule file must not be a symlink")
		} else if linfo.IsDir() {
			paths = walkRuleFiles(rulesPath, fc)
		} else {
			paths = []string{rulesPath}
		}
	} else {
		defaultDir := filepath.Join(absRoot, "config", "tillr", "watch")
		linfo, err := os.Lstat(defaultDir)
		if err != nil {
			if !os.IsNotExist(err) {
				fc.add(defaultDir, CodeWatchLintInput, fmt.Sprintf("rules %q: %v", defaultDir, err))
			}
		} else if !linfo.IsDir() {
			fc.add(defaultDir, CodeWatchLintInput, fmt.Sprintf("rules %q must be a directory", defaultDir))
		} else {
			paths = walkRuleFiles(defaultDir, fc)
		}
	}

	repoSeenIDs := make(map[string]string)
	var repoRules []lintedRule
	for _, p := range paths {
		repoRules = append(repoRules, lintRepoRuleFile(p, kinds, repoSeenIDs, shippedTables, noShipped || shippedFS == nil, fc)...)
	}

	repoByID := make(map[string]lintedRule, len(repoRules))
	for _, rr := range repoRules {
		if rr.rule.ID != "" {
			repoByID[rr.rule.ID] = rr
		}
	}
	merged := make([]lintedRule, 0, len(shippedRules)+len(repoRules))
	for _, sr := range shippedRules {
		if rr, ok := repoByID[sr.rule.ID]; ok {
			merged = append(merged, rr)
		} else {
			merged = append(merged, sr)
		}
	}
	for _, rr := range repoRules {
		if _, isShipped := shippedTables[rr.rule.ID]; !isShipped {
			merged = append(merged, rr)
		}
	}
	return merged
}

func lintShippedRuleData(source string, data []byte, kinds map[string]bool, seenIDs map[string]string, fc *findingCollector) ([]lintedRule, map[string]map[string]any) {
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		fc.add(source, CodeWatchLintRuleSchema, fmt.Sprintf("TOML syntax: %v", err))
		return nil, nil
	}
	for _, k := range slices.Sorted(maps.Keys(doc)) {
		if k != "rule" {
			fc.add(source, CodeWatchLintRuleSchema, fmt.Sprintf("unknown top-level key %q", k))
		}
	}
	raw, present := doc["rule"]
	if !present {
		return nil, nil
	}
	tables, ok := raw.([]any)
	if !ok {
		fc.add(source, CodeWatchLintRuleSchema, "rule: expected [[rule]] entries")
		return nil, nil
	}
	var out []lintedRule
	byID := make(map[string]map[string]any, len(tables))
	for i, elem := range tables {
		table, ok := elem.(map[string]any)
		if !ok {
			fc.add(source, CodeWatchLintRuleSchema, fmt.Sprintf("rule[%d]: expected a rule table", i))
			continue
		}
		rule := lintOneRuleTable(source, i, table, kinds, seenIDs, fc)
		if rule.ID != "" {
			if _, exists := byID[rule.ID]; !exists {
				byID[rule.ID] = table
			}
		}
		out = append(out, lintedRule{source: source, rule: rule})
	}
	return out, byID
}

func lintRepoRuleFile(path string, kinds map[string]bool, seenIDs map[string]string, shippedTables map[string]map[string]any, noShipped bool, fc *findingCollector) []lintedRule {
	linfo, err := os.Lstat(path)
	if err != nil {
		fc.add(path, CodeWatchLintInput, fmt.Sprintf("rules %q: %v", path, err))
		return nil
	}
	if linfo.Mode()&fs.ModeSymlink != 0 {
		fc.add(path, CodeWatchLintRuleSchema, "rule file must not be a symlink")
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fc.add(path, CodeWatchLintInput, fmt.Sprintf("reading %q: %v", path, err))
		return nil
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		fc.add(path, CodeWatchLintRuleSchema, fmt.Sprintf("TOML syntax: %v", err))
		return nil
	}
	for _, k := range slices.Sorted(maps.Keys(doc)) {
		if k != "rule" && k != "override" {
			fc.add(path, CodeWatchLintRuleSchema, fmt.Sprintf("unknown top-level key %q", k))
		}
	}
	var out []lintedRule
	if raw, present := doc["rule"]; present {
		tables, ok := raw.([]any)
		if !ok {
			fc.add(path, CodeWatchLintRuleSchema, "rule: expected [[rule]] entries")
		} else {
			for i, elem := range tables {
				table, ok := elem.(map[string]any)
				if !ok {
					fc.add(path, CodeWatchLintRuleSchema, fmt.Sprintf("rule[%d]: expected a rule table", i))
					continue
				}
				rule := lintOneRuleTable(path, i, table, kinds, seenIDs, fc)
				out = append(out, lintedRule{source: path, rule: rule})
			}
		}
	}
	if raw, present := doc["override"]; present {
		tables, ok := raw.([]any)
		if !ok {
			fc.add(path, CodeWatchLintRuleSchema, "override: expected [[override]] entries")
		} else {
			for i, elem := range tables {
				table, ok := elem.(map[string]any)
				if !ok {
					fc.add(path, CodeWatchLintRuleSchema, fmt.Sprintf("override[%d]: expected an override table", i))
					continue
				}
				if rule, ok := lintOneOverrideTable(path, i, table, kinds, seenIDs, shippedTables, noShipped, fc); ok {
					out = append(out, lintedRule{source: path, rule: rule})
				}
			}
		}
	}
	return out
}

func lintOneOverrideTable(path string, index int, table map[string]any, kinds map[string]bool, seenIDs map[string]string, shippedTables map[string]map[string]any, noShipped bool, fc *findingCollector) (Rule, bool) {
	id, _ := table["id"].(string)
	label := fmt.Sprintf("override[%d]", index)
	if id != "" {
		label = fmt.Sprintf("override %q", id)
	}
	emit := func(code, msg string) {
		fc.add(path, code, fmt.Sprintf("%s: %s", label, msg))
	}
	checkNoRemedy(table, "", func(keyPath string) {
		emit(CodeWatchLintForbiddenRemedy, fmt.Sprintf("%s: remedies are not allowed", keyPath))
	})
	if id == "" {
		emit(CodeWatchLintRuleSchema, "id: expected a non-empty string")
		return Rule{}, false
	}
	if len(table) == 1 {
		emit(CodeWatchLintRuleSchema, "must specify at least one key besides id")
		return Rule{}, false
	}
	badKey := false
	for _, k := range slices.Sorted(maps.Keys(table)) {
		switch k {
		case "id", "enabled", "scope", "severity", "reads", "when", "guidance", "delivery", "remedy":
		default:
			emit(CodeWatchLintRuleSchema, fmt.Sprintf("%s: unknown key", k))
			badKey = true
		}
	}
	if prev, dup := seenIDs[id]; dup {
		emit(CodeWatchLintRuleSchema, fmt.Sprintf("id: duplicate rule ID (first in %s)", fc.formatSource(prev)))
		return Rule{}, false
	}
	seenIDs[id] = path
	if noShipped {
		emit(CodeWatchLintRuleSchema, fmt.Sprintf("no shipped rule with id %q (shipped rules disabled)", id))
		return Rule{}, false
	}
	shippedTable, ok := shippedTables[id]
	if !ok {
		emit(CodeWatchLintRuleSchema, fmt.Sprintf("no shipped rule with id %q", id))
		return Rule{}, false
	}
	if badKey {
		return Rule{}, false
	}
	merged, err := applyOverrideTable(path, id, shippedTable, table)
	if err != nil {
		emit(CodeWatchLintRuleSchema, err.Error())
		return Rule{}, false
	}
	return lintOneRuleTable(path, index, merged, kinds, nil, fc), true
}

func walkRuleFiles(dir string, fc *findingCollector) []string {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			fc.add(path, CodeWatchLintInput, err.Error())
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if filepath.Ext(path) == ".toml" || entry.IsDir() {
				fc.add(path, CodeWatchLintRuleSchema, "rule file must not be a symlink")
			}
			return nil
		}
		if entry.IsDir() || filepath.Ext(path) != ".toml" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	sort.Strings(files)
	return files
}

func lintRuleFile(path string, kinds map[string]bool, seenIDs map[string]string, fc *findingCollector) []lintedRule {
	linfo, err := os.Lstat(path)
	if err != nil {
		fc.add(path, CodeWatchLintInput, fmt.Sprintf("rules %q: %v", path, err))
		return nil
	}
	if linfo.Mode()&fs.ModeSymlink != 0 {
		fc.add(path, CodeWatchLintRuleSchema, "rule file must not be a symlink")
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fc.add(path, CodeWatchLintInput, fmt.Sprintf("reading %q: %v", path, err))
		return nil
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		fc.add(path, CodeWatchLintRuleSchema, fmt.Sprintf("TOML syntax: %v", err))
		return nil
	}
	for _, k := range slices.Sorted(maps.Keys(doc)) {
		if k != "rule" {
			fc.add(path, CodeWatchLintRuleSchema, fmt.Sprintf("unknown top-level key %q", k))
		}
	}
	raw, present := doc["rule"]
	if !present {
		return nil
	}
	tables, ok := raw.([]any)
	if !ok {
		fc.add(path, CodeWatchLintRuleSchema, "rule: expected [[rule]] entries")
		return nil
	}
	var out []lintedRule
	for i, elem := range tables {
		table, ok := elem.(map[string]any)
		if !ok {
			fc.add(path, CodeWatchLintRuleSchema, fmt.Sprintf("rule[%d]: expected a rule table", i))
			continue
		}
		rule := lintOneRuleTable(path, i, table, kinds, seenIDs, fc)
		out = append(out, lintedRule{source: path, rule: rule})
	}
	return out
}

func lintOneRuleTable(path string, index int, table map[string]any, kinds map[string]bool, seenIDs map[string]string, fc *findingCollector) Rule {
	id, _ := table["id"].(string)
	label := fmt.Sprintf("rule[%d]", index)
	if id != "" {
		label = fmt.Sprintf("rule %q", id)
	}
	emit := func(code, msg string) {
		fc.add(path, code, fmt.Sprintf("%s: %s", label, msg))
	}

	// Check for any "remedy" key recursively first.
	checkNoRemedy(table, "", func(keyPath string) {
		emit(CodeWatchLintForbiddenRemedy, fmt.Sprintf("%s: remedies are not allowed", keyPath))
	})

	for _, k := range slices.Sorted(maps.Keys(table)) {
		switch k {
		case "id", "enabled", "scope", "severity", "variables", "reads", "when", "guidance", "delivery", "remedy":
		default:
			emit(CodeWatchLintRuleSchema, fmt.Sprintf("%s: unknown key", k))
		}
	}

	var r Rule
	r.Enabled = true
	if rawID, ok := table["id"].(string); !ok || rawID == "" {
		emit(CodeWatchLintRuleSchema, "id: expected a non-empty string")
	} else {
		r.ID = rawID
		if len(rawID) > 128 {
			emit(CodeWatchLintRuleSchema, "id: must be at most 128 bytes")
		}
		if seenIDs != nil {
			if prev, dup := seenIDs[rawID]; dup {
				emit(CodeWatchLintRuleSchema, fmt.Sprintf("id: duplicate rule ID (first in %s)", fc.formatSource(prev)))
			} else {
				seenIDs[rawID] = path
			}
		}
	}

	if rawEnabled, present := table["enabled"]; present {
		b, ok := rawEnabled.(bool)
		if !ok {
			emit(CodeWatchLintRuleSchema, "enabled: expected a boolean")
		} else {
			r.Enabled = b
		}
	}

	if scope, ok := table["scope"].(string); !ok || scope == "" {
		emit(CodeWatchLintUnknownStage, "scope: expected core, charter:<name> or workflow:<name>")
	} else {
		r.Scope = scope
		prefix, name, _ := strings.Cut(scope, ":")
		if scope != "core" && !((prefix == "charter" || prefix == "workflow") && name != "") {
			emit(CodeWatchLintUnknownStage, fmt.Sprintf("scope: invalid scope %q (expected core, charter:<name> or workflow:<name>)", scope))
		}
	}

	if sev, ok := table["severity"].(string); !ok || !slices.Contains([]string{"info", "warning", "high"}, sev) {
		emit(CodeWatchLintRuleSchema, "severity: expected info, warning or high")
	} else {
		r.Severity = sev
	}

	// Determine pattern early for reads validation.
	whenMap, whenOK := table["when"].(map[string]any)
	pattern, _ := "", false
	if whenOK {
		pattern, _ = whenMap["pattern"].(string)
	}

	// [rule.reads]
	refs := make(map[string]bool)
	proofRefs := make(map[string]bool)
	readsMap, readsOK := table["reads"].(map[string]any)
	if !readsOK {
		emit(CodeWatchLintUnknownSignal, "reads: expected a table")
	} else {
		for _, k := range slices.Sorted(maps.Keys(readsMap)) {
			if k != "events" && k != "projections" {
				emit(CodeWatchLintUnknownSignal, fmt.Sprintf("reads.%s: unknown key", k))
			}
		}
		if rawEv, present := readsMap["events"]; present {
			evList, ok := tomlStringList(rawEv)
			if !ok {
				emit(CodeWatchLintUnknownSignal, "reads.events: expected a list of strings")
			} else {
				r.Reads.Events = evList
				for _, ev := range evList {
					if ev == "" {
						emit(CodeWatchLintUnknownSignal, "reads.events: empty event kind")
					} else if !kinds[ev] {
						emit(CodeWatchLintUnknownSignal, fmt.Sprintf("reads.events: unregistered event kind %q", ev))
					}
				}
			}
		}
		if rawProj, present := readsMap["projections"]; present {
			projList, ok := tomlStringList(rawProj)
			if !ok {
				emit(CodeWatchLintUnknownSignal, "reads.projections: expected a list of strings")
			} else {
				r.Reads.Projections = projList
				for _, proj := range projList {
					switch {
					case proj == "items" || proj == "charter" || proj == "fixer_checks":
					case strings.HasPrefix(proj, "git_refs:"):
						list := strings.TrimPrefix(proj, "git_refs:")
						if list == "" {
							emit(CodeWatchLintUnknownSignal, fmt.Sprintf("reads.projections: empty git_refs list in %q", proj))
						} else {
							for ref := range strings.SplitSeq(list, ",") {
								if !validRef(ref) {
									emit(CodeWatchLintUnknownSignal, fmt.Sprintf("reads.projections: invalid Git ref %q in %q", ref, proj))
								} else {
									refs[ref] = true
								}
							}
						}
					case strings.HasPrefix(proj, "green_proof:"):
						ref := strings.TrimPrefix(proj, "green_proof:")
						if !validRef(ref) {
							emit(CodeWatchLintUnknownSignal, fmt.Sprintf("reads.projections: invalid Git ref %q in %q", ref, proj))
						} else {
							proofRefs[ref] = true
						}
					default:
						emit(CodeWatchLintUnknownSignal, fmt.Sprintf("reads.projections: unknown projection %q", proj))
					}
				}
			}
		}
		if pattern != LagBehind && pattern != PredicateMatch && len(r.Reads.Events) == 0 {
			emit(CodeWatchLintUnknownSignal, "reads.events: expected a non-empty list of event kinds")
		}
		if pattern == PredicateMatch && len(r.Reads.Events) == 0 && !slices.Contains(r.Reads.Projections, "items") {
			emit(CodeWatchLintUnknownSignal, "reads.projections: projection-only predicate_match requires \"items\"")
		}
		if pattern == LagBehind && len(r.Reads.Events) == 0 && len(r.Reads.Projections) == 0 {
			emit(CodeWatchLintUnknownSignal, "reads: lag_behind requires events or projections")
		}
	}

	// [rule.when]
	if !whenOK {
		emit(CodeWatchLintRuleSchema, "when: expected a table")
	} else {
		r.When = lintRuleWhen(whenMap, r.Scope, r.Reads, refs, proofRefs, kinds, emit)
	}

	// [rule.variables]
	if rawVars, present := table["variables"]; present {
		r.Variables = lintRuleVariables(rawVars, r.When.Pattern, emit)
	}
	if _, err := resolveVariables(r); err != nil {
		if _, present := table["variables"]; present {
			emit(CodeWatchLintRuleSchema, fmt.Sprintf("variables: %v", err))
		} else {
			emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.group_by: %v", err))
		}
	}

	// [rule.guidance]
	guidanceMap, gOK := table["guidance"].(map[string]any)
	if !gOK {
		emit(CodeWatchLintEmptyGuidance, "guidance: expected a table")
	} else {
		r.Guidance = lintRuleGuidance(guidanceMap, r.When, len(r.Reads.Events) > 0, r.VariableNames(), emit)
	}

	// [rule.delivery]
	deliveryMap, dOK := table["delivery"].(map[string]any)
	if !dOK {
		emit(CodeWatchLintForbiddenRemedy, "delivery: expected a table")
	} else {
		r.Delivery = lintRuleDelivery(deliveryMap, emit)
	}
	if r.Delivery.Receiver == "triage" && isLivenessRule(r) {
		emit(CodeWatchLintLivenessReceiver, "delivery.receiver: loop-liveness rule must not route to triage; use operator-session")
	}

	return r
}

func checkNoRemedy(raw any, path string, onRemedy func(string)) {
	switch node := raw.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(node)) {
			key := path + k
			if k == "remedy" {
				onRemedy(key)
				continue
			}
			checkNoRemedy(node[k], key+".", onRemedy)
		}
	case []any:
		for i, elem := range node {
			checkNoRemedy(elem, fmt.Sprintf("%s[%d].", strings.TrimSuffix(path, "."), i), onRemedy)
		}
	}
}

func tomlStringList(raw any) ([]string, bool) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, len(arr))
	for i, v := range arr {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

func lintRuleWhen(whenMap map[string]any, scope string, reads Reads, refs, proofRefs map[string]bool, kinds map[string]bool, emit func(string, string)) When {
	var w When
	pattern, ok := whenMap["pattern"].(string)
	if !ok || pattern == "" {
		emit(CodeWatchLintRuleSchema, "when.pattern: expected a non-empty string")
		return w
	}
	w.Pattern = pattern

	// group_by
	if rawGB, present := whenMap["group_by"]; !present {
		emit(CodeWatchLintRuleSchema, "when.group_by: expected a non-empty string or list of strings")
	} else {
		switch gb := rawGB.(type) {
		case string:
			if gb == "" || !validEventPath(gb) {
				emit(CodeWatchLintRuleSchema, "when.group_by: expected a non-empty valid event path")
			} else if isForbiddenGroupByColumn(gb) {
				emit(CodeWatchLintForbiddenRemedy, fmt.Sprintf("when.group_by: integer column %q cannot be used as group_by", gb))
			} else {
				w.GroupBy = gb
			}
		case []any:
			list, ok := tomlStringList(gb)
			if !ok || len(list) == 0 {
				emit(CodeWatchLintRuleSchema, "when.group_by: expected a non-empty list of strings")
			} else {
				seen := make(map[string]bool)
				for _, col := range list {
					if col == "" || col == "global" || !validEventPath(col) {
						emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.group_by: invalid entry %q", col))
					} else if isForbiddenGroupByColumn(col) {
						emit(CodeWatchLintForbiddenRemedy, fmt.Sprintf("when.group_by: integer column %q cannot be used as group_by", col))
					}
					if seen[col] {
						emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.group_by: duplicate entry %q", col))
					}
					seen[col] = true
				}
				if len(list) == 1 {
					w.GroupBy = list[0]
				} else {
					w.GroupBy = strings.Join(list, ", ")
					w.GroupByList = list
				}
			}
		case map[string]any:
			for _, key := range slices.Sorted(maps.Keys(gb)) {
				if key != "any" {
					emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.group_by: unknown key %q in group_by table", key))
				}
			}
			rawAny, hasAny := gb["any"]
			list, ok := tomlStringList(rawAny)
			if !hasAny || !ok || len(list) == 0 {
				emit(CodeWatchLintRuleSchema, "when.group_by: expected any to be a non-empty list of strings")
			} else {
				seen := make(map[string]bool)
				for _, col := range list {
					if col == "" || col == "global" || !validEventPath(col) {
						emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.group_by: invalid entry %q", col))
					} else if isForbiddenGroupByColumn(col) {
						emit(CodeWatchLintForbiddenRemedy, fmt.Sprintf("when.group_by: integer column %q cannot be used as group_by", col))
					}
					if seen[col] {
						emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.group_by: duplicate entry %q", col))
					}
					seen[col] = true
				}
				if len(list) == 1 {
					w.GroupBy = list[0]
				} else {
					w.GroupBy = strings.Join(list, " | ")
				}
				w.GroupByAny = list
			}
		default:
			emit(CodeWatchLintRuleSchema, "when.group_by: expected a non-empty string, list of strings, or { any = [...] }")
		}
	}

	if namedScope(scope, "workflow:") != "" {
		if w.GroupBy == "global" || len(w.GroupByList) >= 2 {
			emit(CodeWatchLintRuleSchema, "when.group_by: named workflow scope requires a single non-global subject")
		}
	}

	if pattern == LagBehind {
		allowed := []string{"group_by", "pattern", "leader", "follower", "threshold_commits", "threshold_duration", "mode", "reopen_when"}
		for _, k := range slices.Sorted(maps.Keys(whenMap)) {
			if k != "remedy" && !slices.Contains(allowed, k) {
				emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.%s: unknown key", k))
			}
		}
		if len(w.GroupByList) >= 2 || len(w.GroupByAny) > 0 || (w.GroupBy != "" && w.GroupBy != "global") {
			emit(CodeWatchLintRuleSchema, "when.group_by: lag_behind requires global")
		}
		leader, _ := whenMap["leader"].(string)
		follower, _ := whenMap["follower"].(string)
		if !validRef(leader) || !refs[leader] {
			emit(CodeWatchLintRuleSchema, "when.leader: expected a full ref name listed in reads.projections")
		}
		if !validRef(follower) || !refs[follower] {
			emit(CodeWatchLintRuleSchema, "when.follower: expected a full ref name listed in reads.projections")
		}
		if leader != "" && leader == follower {
			emit(CodeWatchLintRuleSchema, "when.follower: must differ from leader")
		}
		w.Leader, w.Follower = leader, follower

		_, hasCommits := whenMap["threshold_commits"]
		_, hasDur := whenMap["threshold_duration"]
		if !hasCommits && !hasDur {
			emit(CodeWatchLintRuleSchema, "when.threshold_commits: threshold_commits or threshold_duration is required")
		}
		if hasCommits {
			n, ok := whenMap["threshold_commits"].(int64)
			if !ok || n < 1 {
				emit(CodeWatchLintForbiddenRemedy, "when.threshold_commits: expected a positive integer >= 1")
			} else {
				w.ThresholdCommits = n
			}
		}
		if hasDur {
			s, ok := whenMap["threshold_duration"].(string)
			d := parseWindow(s)
			if !ok || d <= 0 {
				emit(CodeWatchLintForbiddenRemedy, "when.threshold_duration: expected a positive whole-number duration in s, m or h")
			} else {
				w.ThresholdDuration = d
			}
		}
		if rawMode, present := whenMap["mode"]; present {
			m, ok := rawMode.(string)
			if !hasCommits || !hasDur {
				emit(CodeWatchLintRuleSchema, "when.mode: requires both threshold_commits and threshold_duration")
			}
			if !ok || (m != "any" && m != "all") {
				emit(CodeWatchLintRuleSchema, "when.mode: expected any or all")
			}
			w.Mode = m
		}
		w.ReopenWhen = lintRuleExpr(whenMap, "reopen_when", false, w.GroupBy, reads, refs, proofRefs, kinds, emit)
		if w.ReopenWhen != nil && len(reads.Events) == 0 {
			emit(CodeWatchLintUnknownSignal, "when.reopen_when: requires reads.events")
		}
		return w
	}

	projectionOnly := pattern == PredicateMatch && len(reads.Events) == 0
	if projectionOnly && (w.GroupBy != "item_id" || len(w.GroupByList) != 0 || len(w.GroupByAny) != 0) {
		emit(CodeWatchLintRuleSchema, "when.group_by: projection-only predicate_match requires group_by = \"item_id\"")
	}

	if rawFP, present := whenMap["fingerprint"]; present {
		if projectionOnly {
			emit(CodeWatchLintRuleSchema, "when.fingerprint: projection-only predicate_match must omit fingerprint")
		} else {
			fp, ok := tomlStringList(rawFP)
			if !ok || len(fp) == 0 {
				emit(CodeWatchLintRuleSchema, "when.fingerprint: expected a non-empty list of strings")
			} else {
				for _, name := range fp {
					if name == "" || !validEventPath(name) || strings.HasPrefix(name, "event.") {
						emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.fingerprint: invalid event path %q", name))
					}
				}
				w.Fingerprint = fp
			}
		}
	}

	if rawKeep, present := whenMap["keep_on_terminal_item"]; present {
		b, ok := rawKeep.(bool)
		if !ok {
			emit(CodeWatchLintRuleSchema, "when.keep_on_terminal_item: expected a boolean")
		} else {
			w.KeepOnTerminalItem = b
		}
	}

	allowed := []string{"group_by", "pattern", "clear_when", "window", "fingerprint", "keep_on_terminal_item", "for_each", "broadcast_events"}
	w.ForEach = lintForEach(whenMap, reads, emit)
	switch pattern {
	case DurationWithoutEvent:
		allowed = append(allowed, "after", "match", "threshold_duration")
		w.After = lintRuleExpr(whenMap, "after", true, w.GroupBy, reads, refs, proofRefs, kinds, emit)
		w.Match = lintRuleExpr(whenMap, "match", false, w.GroupBy, reads, refs, proofRefs, kinds, emit)
		s, ok := whenMap["threshold_duration"].(string)
		d := parseWindow(s)
		if !ok || d <= 0 {
			emit(CodeWatchLintForbiddenRemedy, "when.threshold_duration: expected a positive whole-number duration in s, m or h")
		} else {
			w.ThresholdDuration = d
		}
	case PredicateMatch:
		allowed = append(allowed, "after", "match", "reset")
		w.Threshold = 1
		if _, hasAfter := whenMap["after"]; hasAfter {
			if projectionOnly {
				emit(CodeWatchLintRuleSchema, "when.after: projection-only predicate_match must omit after")
			}
			w.After = lintRuleExpr(whenMap, "after", true, w.GroupBy, reads, refs, proofRefs, kinds, emit)
			if _, hasMatch := whenMap["match"]; !hasMatch {
				emit(CodeWatchLintRuleSchema, "when.match: required when after is set on predicate_match")
			} else {
				w.Match = lintRuleExpr(whenMap, "match", true, w.GroupBy, reads, refs, proofRefs, kinds, emit)
			}
			w.Reset = lintRuleExpr(whenMap, "reset", false, w.GroupBy, reads, refs, proofRefs, kinds, emit)
		} else {
			w.Match = lintRuleExpr(whenMap, "match", projectionOnly, w.GroupBy, reads, refs, proofRefs, kinds, emit)
			if projectionOnly && w.Match != nil && w.Match.usesEvent {
				emit(CodeWatchLintUnknownSignal, "when.match: projection-only predicate_match cannot reference event.*")
			}
			if _, hasReset := whenMap["reset"]; hasReset {
				emit(CodeWatchLintRuleSchema, "when.reset: requires after on predicate_match")
			}
		}
	case CountInWindow:
		allowed = append(allowed, "match", "distinct_by", "threshold")
		w.Match = lintRuleExpr(whenMap, "match", false, w.GroupBy, reads, refs, proofRefs, kinds, emit)
		w.DistinctBy = lintDistinctBy(whenMap, emit)
	case ConsecutiveCount:
		allowed = append(allowed, "match", "reset", "distinct_by", "threshold")
		w.Match = lintRuleExpr(whenMap, "match", true, w.GroupBy, reads, refs, proofRefs, kinds, emit)
		w.Reset = lintRuleExpr(whenMap, "reset", false, w.GroupBy, reads, refs, proofRefs, kinds, emit)
		w.DistinctBy = lintDistinctBy(whenMap, emit)
	case AlternatingPair:
		allowed = append(allowed, "field", "pairs", "threshold")
		field, ok := whenMap["field"].(string)
		if !ok || field == "" || !validEventPath(field) {
			emit(CodeWatchLintRuleSchema, "when.field: expected a non-empty event path")
		} else {
			w.Field = field
		}
		if rawPairs, present := whenMap["pairs"]; !present {
			emit(CodeWatchLintRuleSchema, "when.pairs: expected at least one pair")
		} else if arr, ok := rawPairs.([]any); !ok || len(arr) == 0 {
			emit(CodeWatchLintRuleSchema, "when.pairs: expected at least one pair")
		} else {
			for idx, elem := range arr {
				pair, ok := elem.([]any)
				if !ok || len(pair) != 2 {
					emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.pairs[%d]: must be a list of two different strings", idx))
					continue
				}
				a, aOK := pair[0].(string)
				b, bOK := pair[1].(string)
				if !aOK || !bOK || a == "" || b == "" || a == b {
					emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.pairs[%d]: must be a list of two different non-empty strings", idx))
					continue
				}
				w.Pairs = append(w.Pairs, [2]string{a, b})
			}
		}
	default:
		emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.pattern: unknown pattern %q", pattern))
		return w
	}

	if projectionOnly {
		if _, hasClear := whenMap["clear_when"]; hasClear {
			emit(CodeWatchLintRuleSchema, "when.clear_when: projection-only predicate_match must omit clear_when")
		}
	} else {
		w.ClearWhen = lintRuleExpr(whenMap, "clear_when", false, w.GroupBy, reads, refs, proofRefs, kinds, emit)
	}
	w.BroadcastEvents = lintBroadcastEvents(whenMap, w, reads, emit)

	for _, k := range slices.Sorted(maps.Keys(whenMap)) {
		if k != "remedy" && !slices.Contains(allowed, k) {
			emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.%s: unknown key", k))
		}
	}

	if pattern != PredicateMatch && pattern != DurationWithoutEvent {
		n, ok := whenMap["threshold"].(int64)
		if !ok || n < 1 {
			emit(CodeWatchLintForbiddenRemedy, "when.threshold: expected a positive integer >= 1")
		} else {
			w.Threshold = n
		}
	}

	_, hasWindow := whenMap["window"]
	if projectionOnly {
		if hasWindow {
			emit(CodeWatchLintRuleSchema, "when.window: projection-only predicate_match must omit window")
		}
		w.Window = 0
	} else if pattern == PredicateMatch && w.ClearWhen != nil && !hasWindow {
		w.Window = 0
	} else {
		s, ok := whenMap["window"].(string)
		d := parseWindow(s)
		if !ok || d <= 0 {
			emit(CodeWatchLintForbiddenRemedy, "when.window: expected a positive whole-number duration in s, m or h")
		} else {
			w.Window = d
		}
	}
	if pattern == DurationWithoutEvent && w.Window > 0 && w.ThresholdDuration > 0 && w.Window <= w.ThresholdDuration {
		emit(CodeWatchLintForbiddenRemedy, "when.window: must be longer than threshold_duration")
	}

	return w
}

func lintDistinctBy(whenMap map[string]any, emit func(string, string)) string {
	raw, present := whenMap["distinct_by"]
	if !present {
		return ""
	}
	db, ok := raw.(string)
	if !ok || db == "" || db == "global" || strings.HasPrefix(db, "event.") || !validEventPath(db) {
		emit(CodeWatchLintRuleSchema, "when.distinct_by: expected a non-empty valid event path without the event. prefix or global")
		return ""
	}
	if isForbiddenGroupByColumn(db) {
		emit(CodeWatchLintForbiddenRemedy, fmt.Sprintf("when.distinct_by: integer column %q cannot be used as distinct_by", db))
	}
	return db
}

func lintForEach(whenMap map[string]any, reads Reads, emit func(string, string)) string {
	raw, present := whenMap["for_each"]
	if !present {
		return ""
	}
	if len(reads.Events) == 0 {
		emit(CodeWatchLintRuleSchema, "when.for_each: requires reads.events")
	}
	fe, ok := raw.(string)
	if !ok || fe == "" {
		emit(CodeWatchLintRuleSchema, "when.for_each: expected a non-empty payload path without the event. prefix or reserved column")
		return ""
	}
	if isForbiddenGroupByColumn(fe) {
		emit(CodeWatchLintForbiddenRemedy, fmt.Sprintf("when.for_each: integer column %q cannot be used as for_each", fe))
		return fe
	}
	if isForbiddenForEachPath(fe) || strings.HasPrefix(fe, "event.") || !validEventPath(fe) {
		emit(CodeWatchLintRuleSchema, "when.for_each: expected a non-empty payload path without the event. prefix or reserved column")
		return ""
	}
	return fe
}

func lintBroadcastEvents(whenMap map[string]any, w When, reads Reads, emit func(string, string)) []string {
	raw, present := whenMap["broadcast_events"]
	if !present {
		return nil
	}
	events, ok := tomlStringList(raw)
	if !ok || len(events) == 0 {
		emit(CodeWatchLintRuleSchema, "when.broadcast_events: expected a non-empty list of strings")
		return nil
	}
	seen := make(map[string]bool, len(events))
	for _, kind := range events {
		if kind == "" {
			emit(CodeWatchLintRuleSchema, "when.broadcast_events: expected non-empty event kinds")
			continue
		}
		if seen[kind] {
			emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.broadcast_events: duplicate event kind %q", kind))
		}
		seen[kind] = true
	}
	if len(reads.Events) == 0 {
		emit(CodeWatchLintRuleSchema, "when.broadcast_events: requires reads.events")
	}
	if w.GroupBy == "global" {
		emit(CodeWatchLintRuleSchema, "when.broadcast_events: cannot be used with group_by = \"global\"")
	}
	if w.Reset == nil && w.ClearWhen == nil {
		emit(CodeWatchLintRuleSchema, "when.broadcast_events: requires reset or clear_when")
	}
	for _, kind := range events {
		if kind != "" && !slices.Contains(reads.Events, kind) {
			emit(CodeWatchLintRuleSchema, fmt.Sprintf("when.broadcast_events: event kind %q must appear in reads.events", kind))
		}
	}
	if len(reads.Events) > 0 {
		hasNonBroadcast := false
		for _, kind := range reads.Events {
			if !slices.Contains(events, kind) {
				hasNonBroadcast = true
				break
			}
		}
		if !hasNonBroadcast {
			emit(CodeWatchLintRuleSchema, "when.broadcast_events: reads.events must retain at least one non-broadcast event kind")
		}
	}
	return events
}

func isForbiddenGroupByColumn(col string) bool {
	return col == "seq" || col == "ts" || col == "revision"
}

func lintRuleExpr(whenMap map[string]any, key string, required bool, groupBy string, reads Reads, refs, proofRefs map[string]bool, kinds map[string]bool, emit func(string, string)) *Expression {
	raw, present := whenMap[key]
	if !present {
		if required {
			emit(CodeWatchLintUnknownSignal, fmt.Sprintf("when.%s: expected a non-empty expression string", key))
		}
		return nil
	}
	src, ok := raw.(string)
	if !ok || strings.TrimSpace(src) == "" {
		emit(CodeWatchLintUnknownSignal, fmt.Sprintf("when.%s: expected a non-empty expression string", key))
		return nil
	}
	exp, err := ParseExpression(src)
	if err != nil {
		emit(CodeWatchLintUnknownSignal, fmt.Sprintf("when.%s: %v", key, err))
		return nil
	}
	if exp.unsupported {
		emit(CodeWatchLintUnknownSignal, fmt.Sprintf("when.%s: uses an unsupported expression operand or call", key))
	}
	for _, op := range exp.projections {
		switch {
		case op.name == "git.ref":
			if !refs[op.ref] {
				emit(CodeWatchLintUnknownSignal, fmt.Sprintf("when.%s: git.ref(%q) requires a listed ref in reads.projections", key, op.ref))
			}
		case op.name == "item.fixer_proved":
			if groupBy == "global" || !slices.Contains(reads.Projections, "fixer_checks") || !proofRefs[op.ref] {
				emit(CodeWatchLintUnknownSignal, fmt.Sprintf("when.%s: item.fixer_proved(%q) requires a non-global subject, fixer_checks, and green_proof:%s in reads.projections", key, op.ref, op.ref))
			}
		case strings.HasPrefix(op.name, "item."):
			switch op.name {
			case "item.id", "item.state", "item.seat", "item.waiting_on":
				if groupBy == "global" || !slices.Contains(reads.Projections, "items") {
					emit(CodeWatchLintUnknownSignal, fmt.Sprintf("when.%s: %s requires an items projection and a non-global subject", key, op.name))
				}
			default:
				emit(CodeWatchLintUnknownSignal, fmt.Sprintf("when.%s: unknown items operand %s", key, op.name))
			}
		case strings.HasPrefix(op.name, "charter."):
			switch op.name {
			case "charter.name", "charter.ready", "charter.blocked", "charter.closed", "charter.states", "charter.finished":
				if !slices.Contains(reads.Projections, "charter") {
					emit(CodeWatchLintUnknownSignal, fmt.Sprintf("when.%s: %s requires a charter projection", key, op.name))
				}
			default:
				emit(CodeWatchLintUnknownSignal, fmt.Sprintf("when.%s: unknown charter operand %s", key, op.name))
			}
		}
	}
	for _, cmp := range exp.eventComparisons {
		if cmp.field == "kind" && (cmp.op == "==" || cmp.op == "in") {
			for _, lit := range cmp.values {
				if !kinds[lit] {
					emit(CodeWatchLintUnknownSignal, fmt.Sprintf("when.%s: unregistered event.kind %q", key, lit))
				} else if len(reads.Events) > 0 && !slices.Contains(reads.Events, lit) {
					emit(CodeWatchLintUnknownSignal, fmt.Sprintf("when.%s: event.kind %q is not listed in reads.events", key, lit))
				}
			}
		}
	}
	return exp
}

func lintRuleVariables(raw any, pattern string, emit func(string, string)) map[string]string {
	tbl, ok := raw.(map[string]any)
	if !ok {
		emit(CodeWatchLintRuleSchema, "variables: expected a table")
		return nil
	}
	if pattern == LagBehind {
		emit(CodeWatchLintRuleSchema, "variables: not allowed on lag_behind rules")
		return nil
	}
	if len(tbl) == 0 {
		return nil
	}
	vars := make(map[string]string, len(tbl))
	for _, key := range slices.Sorted(maps.Keys(tbl)) {
		if !validVarIdent(key) {
			emit(CodeWatchLintRuleSchema, fmt.Sprintf("variables.%s: expected an ASCII identifier ([A-Za-z_][A-Za-z0-9_]*)", key))
			continue
		}
		val, ok := tbl[key].(string)
		if !ok || !validVariableEventPath(val) {
			emit(CodeWatchLintRuleSchema, fmt.Sprintf("variables.%s: expected a non-empty event path without event. prefix or whitespace", key))
			continue
		}
		vars[key] = val
	}
	return vars
}

func lintRuleGuidance(gMap map[string]any, when When, hasEvents bool, varNames []string, emit func(string, string)) Guidance {
	for _, k := range slices.Sorted(maps.Keys(gMap)) {
		if k != "summary" && k != "reopen_summary" && k != "check" && k != "do" {
			emit(CodeWatchLintEmptyGuidance, fmt.Sprintf("guidance.%s: unknown key", k))
		}
	}
	allowEvent := hasEvents && when.Pattern != LagBehind
	var g Guidance
	if summary, ok := gMap["summary"].(string); !ok || strings.TrimSpace(summary) == "" {
		emit(CodeWatchLintEmptyGuidance, "guidance.summary: expected a non-blank string")
	} else {
		g.Summary = summary
		checkGuidanceText("guidance.summary", summary, when, varNames, allowEvent, emit)
	}

	if rawReopen, present := gMap["reopen_summary"]; present {
		if when.Pattern != LagBehind || when.ReopenWhen == nil {
			emit(CodeWatchLintEmptyGuidance, "guidance.reopen_summary: requires lag_behind with when.reopen_when")
		} else if rs, ok := rawReopen.(string); !ok || strings.TrimSpace(rs) == "" {
			emit(CodeWatchLintEmptyGuidance, "guidance.reopen_summary: expected a non-blank string")
		} else {
			g.ReopenSummary = rs
			checkGuidanceText("guidance.reopen_summary", rs, when, varNames, true, emit)
		}
	}

	for _, field := range []struct {
		name string
		dst  *[]string
	}{{"check", &g.Check}, {"do", &g.Do}} {
		list, ok := tomlStringList(gMap[field.name])
		if !ok || len(list) == 0 {
			emit(CodeWatchLintEmptyGuidance, fmt.Sprintf("guidance.%s: expected a non-empty list of non-blank strings", field.name))
			continue
		}
		*field.dst = list
		for i, line := range list {
			if strings.TrimSpace(line) == "" {
				emit(CodeWatchLintEmptyGuidance, fmt.Sprintf("guidance.%s[%d]: expected a non-blank string", field.name, i))
				continue
			}
			checkGuidanceText(fmt.Sprintf("guidance.%s[%d]", field.name, i), line, when, varNames, allowEvent, emit)
		}
	}
	return g
}

func checkGuidanceText(field, text string, when When, varNames []string, allowEvent bool, emit func(string, string)) {
	tokens, err := extractGuidancePlaceholders(text)
	if err != nil {
		emit(CodeWatchLintEmptyGuidance, fmt.Sprintf("%s: %v", field, err))
		return
	}
	for _, tok := range tokens {
		if !validGuidancePlaceholder(tok, when, varNames, allowEvent) {
			emit(CodeWatchLintEmptyGuidance, fmt.Sprintf("%s: invalid or unsupported placeholder {{%s}} for pattern %q", field, tok, when.Pattern))
		}
	}
}

func extractGuidancePlaceholders(line string) ([]string, error) {
	var tokens []string
	pos := 0
	for pos < len(line) {
		openIdx := strings.Index(line[pos:], "{{")
		closeIdx := strings.Index(line[pos:], "}}")
		if closeIdx >= 0 && (openIdx < 0 || closeIdx < openIdx) {
			return nil, fmt.Errorf("unmatched closing delimiter }}")
		}
		if openIdx < 0 {
			break
		}
		start := pos + openIdx + 2
		endRel := strings.Index(line[start:], "}}")
		if endRel < 0 {
			return nil, fmt.Errorf("unclosed placeholder {{")
		}
		tok := line[start : start+endRel]
		if strings.ContainsAny(tok, "{}") {
			return nil, fmt.Errorf("nested brace inside placeholder {{%s}}", tok)
		}
		tokens = append(tokens, tok)
		pos = start + endRel + 2
	}
	return tokens, nil
}

func validGuidancePlaceholder(tok string, when When, varNames []string, allowEvent bool) bool {
	switch tok {
	case "subject", "rule_id", "alert_id", "count", "elapsed":
		return true
	case "leader", "follower", "leader_short", "follower_short":
		return when.Pattern == LagBehind
	case "pair_a", "pair_b":
		return when.Pattern == AlternatingPair
	case "distinct_values":
		return (when.Pattern == CountInWindow || when.Pattern == ConsecutiveCount) && when.DistinctBy != ""
	}
	if name, ok := strings.CutPrefix(tok, "var."); ok {
		return name != "" && slices.Contains(varNames, name)
	}
	if name, ok := strings.CutPrefix(tok, "event."); ok {
		if !allowEvent {
			return false
		}
		if name == "" || strings.HasPrefix(name, "event.") || strings.IndexFunc(name, unicode.IsSpace) >= 0 {
			return false
		}
		return validEventPath(name)
	}
	return false
}

func lintRuleDelivery(dMap map[string]any, emit func(string, string)) Delivery {
	for _, k := range slices.Sorted(maps.Keys(dMap)) {
		if k != "receiver" && k != "also_notify" {
			emit(CodeWatchLintForbiddenRemedy, fmt.Sprintf("delivery.%s: unknown key", k))
		}
	}
	var d Delivery
	if rec, ok := dMap["receiver"].(string); !ok || !isValidReceiver(rec) {
		emit(CodeWatchLintForbiddenRemedy, fmt.Sprintf("delivery.receiver: expected foreground, subscribers, triage or operator-session, got %v", dMap["receiver"]))
	} else {
		d.Receiver = rec
	}
	if rawAlso, present := dMap["also_notify"]; present {
		list, ok := tomlStringList(rawAlso)
		if !ok {
			emit(CodeWatchLintForbiddenRemedy, "delivery.also_notify: expected a list of strings")
		} else {
			d.AlsoNotify = list
			for _, v := range list {
				if !isValidReceiver(v) {
					emit(CodeWatchLintForbiddenRemedy, fmt.Sprintf("delivery.also_notify: receiver %q is not foreground, subscribers, triage or operator-session", v))
				}
			}
		}
	}
	return d
}

// isLivenessRule reports whether r watches the absence of drain.loop-pass or
// watches drain.loop-ended as a trigger (rather than only as a clear/reset).
func isLivenessRule(r Rule) bool {
	if r.When.Pattern == DurationWithoutEvent && slices.Contains(r.Reads.Events, "drain.loop-pass") {
		if (r.When.After != nil && exprAllowsKind(r.When.After, "drain.loop-pass")) ||
			(r.When.Match != nil && exprAllowsKind(r.When.Match, "drain.loop-pass")) {
			return true
		}
	}
	if slices.Contains(r.Reads.Events, "drain.loop-ended") {
		switch r.When.Pattern {
		case DurationWithoutEvent:
			if (r.When.After != nil && exprAllowsKind(r.When.After, "drain.loop-ended")) ||
				(r.When.Match != nil && exprAllowsKind(r.When.Match, "drain.loop-ended")) {
				return true
			}
		case LagBehind:
			if r.When.ReopenWhen != nil && exprAllowsKind(r.When.ReopenWhen, "drain.loop-ended") {
				return true
			}
		default:
			if exprAllowsKind(r.When.Match, "drain.loop-ended") {
				return true
			}
		}
	}
	return false
}

// exprAllowsKind reports whether exp can match an event of the given kind.
// If exp is nil or imposes no event.kind == / in constraint, any read event kind is allowed.
func exprAllowsKind(exp *Expression, kind string) bool {
	if exp == nil {
		return true
	}
	constrained := false
	for _, cmp := range exp.eventComparisons {
		if cmp.field == "kind" && (cmp.op == "==" || cmp.op == "in") {
			constrained = true
			if slices.Contains(cmp.values, kind) {
				return true
			}
		}
	}
	return !constrained
}

func lintRulesWithContext(rules []lintedRule, charter charterData, workflows []loadedWorkflow, fc *findingCollector) {
	for _, lr := range rules {
		r := lr.rule
		if charter.valid && charter.def.Completes != "landing" {
			if r.Scope == "core" || r.Scope == "charter:"+charter.def.Name {
				for _, proj := range r.Reads.Projections {
					if strings.HasPrefix(proj, "git_refs:") || proj == "fixer_checks" || strings.HasPrefix(proj, "green_proof:") {
						fc.add(lr.source, CodeWatchLintUnknownSignal, fmt.Sprintf("rule %q: projection %q is incompatible with charter %q (completes=%q)", r.ID, proj, charter.def.Name, charter.def.Completes))
					}
				}
			}
		}

		wfRef, isWF := strings.CutPrefix(r.Scope, "workflow:")
		if !isWF || wfRef == "*" {
			continue
		}
		var matched []loadedWorkflow
		for _, lw := range workflows {
			if strings.Contains(wfRef, "@v") {
				if lw.ref == wfRef {
					matched = append(matched, lw)
				}
			} else if lw.wf.Name == wfRef {
				matched = append(matched, lw)
			}
		}
		if len(matched) == 0 {
			fc.add(lr.source, CodeWatchLintUnknownStage, fmt.Sprintf("rule %q: workflow scope %q matches no loaded workflow", r.ID, r.Scope))
			continue
		}

		allStages := make(map[string]bool)
		allSeats := map[string]bool{"design": true}
		for _, lw := range matched {
			for id := range lw.stageIDs {
				allStages[id] = true
			}
			for seat := range lw.seats {
				allSeats[seat] = true
			}
		}

		if r.When.Pattern == AlternatingPair {
			switch r.When.Field {
			case "seat":
				for _, p := range r.When.Pairs {
					if !allSeats[p[0]] {
						fc.add(lr.source, CodeWatchLintUnknownStage, fmt.Sprintf("rule %q: alternating_pair seat %q not found in %s", r.ID, p[0], r.Scope))
					}
					if !allSeats[p[1]] {
						fc.add(lr.source, CodeWatchLintUnknownStage, fmt.Sprintf("rule %q: alternating_pair seat %q not found in %s", r.ID, p[1], r.Scope))
					}
				}
			case "stage", "stage_id", "node", "node_id":
				for _, p := range r.When.Pairs {
					if !allStages[p[0]] {
						fc.add(lr.source, CodeWatchLintUnknownStage, fmt.Sprintf("rule %q: alternating_pair stage %q not found in %s", r.ID, p[0], r.Scope))
					}
					if !allStages[p[1]] {
						fc.add(lr.source, CodeWatchLintUnknownStage, fmt.Sprintf("rule %q: alternating_pair stage %q not found in %s", r.ID, p[1], r.Scope))
					}
				}
			}
		}

		for _, exp := range r.expressions() {
			if exp == nil {
				continue
			}
			for _, cmp := range exp.eventComparisons {
				if cmp.op != "==" && cmp.op != "!=" && cmp.op != "in" {
					continue
				}
				switch cmp.field {
				case "seat":
					for _, val := range cmp.values {
						if !allSeats[val] {
							fc.add(lr.source, CodeWatchLintUnknownStage, fmt.Sprintf("rule %q: event.seat %q not found in %s", r.ID, val, r.Scope))
						}
					}
				case "stage", "stage_id", "node", "node_id":
					for _, val := range cmp.values {
						if !allStages[val] {
							fc.add(lr.source, CodeWatchLintUnknownStage, fmt.Sprintf("rule %q: event.%s %q not found in %s", r.ID, cmp.field, val, r.Scope))
						}
					}
				}
			}
		}
	}
}
