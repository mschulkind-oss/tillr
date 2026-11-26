package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type goTestEdgeEntry struct {
	Builds     []string            `json:"builds"`
	Reads      []string            `json:"reads"`
	BuildTests map[string][]string `json:"build_tests"`
	ReadTests  map[string][]string `json:"read_tests"`
}

func discoverTillrModules(root string) ([]string, error) {
	var modules []string
	if info, err := os.Stat(filepath.Join(root, "tools", "tillr", "go.mod")); err == nil && !info.IsDir() {
		modules = append(modules, "tools/tillr")
	}
	matches, err := filepath.Glob(filepath.Join(root, "tools", "tillr-*", "go.mod"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	for _, m := range matches {
		modules = append(modules, "tools/"+filepath.Base(filepath.Dir(m)))
	}
	return modules, nil
}

func loadGoTestEdges(root string) (map[string]goTestEdgeEntry, error) {
	path := filepath.Join(root, "tools", "gate", "go-test-edges.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]goTestEdgeEntry{}, nil
		}
		return nil, err
	}
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		return nil, fmt.Errorf("%s: expected JSON object: %w", path, err)
	}
	out := make(map[string]goTestEdgeEntry, len(rawMap))
	for pkg, itemRaw := range rawMap {
		var entry goTestEdgeEntry
		if err := json.Unmarshal(itemRaw, &entry); err != nil {
			return nil, fmt.Errorf("%s: invalid entry for %q: %w", path, pkg, err)
		}
		for mapName, m := range map[string]struct {
			tests   map[string][]string
			allowed []string
			listKey string
		}{
			"build_tests": {entry.BuildTests, entry.Builds, "builds"},
			"read_tests":  {entry.ReadTests, entry.Reads, "reads"},
		} {
			allowed := make(map[string]bool, len(m.allowed))
			for _, k := range m.allowed {
				allowed[k] = true
			}
			for rule, names := range m.tests {
				if !allowed[rule] {
					return nil, fmt.Errorf("%s: %s.%s key %q must appear in %s.%s", path, pkg, mapName, rule, pkg, m.listKey)
				}
				if len(names) == 0 {
					return nil, fmt.Errorf("%s: %s.%s[%q] must be a non-empty list of test names", path, pkg, mapName, rule)
				}
				for _, name := range names {
					if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) {
						return nil, fmt.Errorf("%s: %s.%s[%q] must be a non-empty list of test names", path, pkg, mapName, rule)
					}
				}
			}
		}
		out[pkg] = entry
	}
	return out, nil
}

type sourceScanningEdgeSpec struct {
	pkg               string
	testName          string
	includeRootModule bool
}

var sourceScanningEdges = []sourceScanningEdgeSpec{
	{
		pkg:               "github.com/mschulkind-oss/tillr/definition",
		testName:          "TestNameOnlyInOneConstantAndOnlyLifecycleConsumers",
		includeRootModule: true,
	},
	{
		pkg:               "github.com/mschulkind-oss/tillr/store",
		testName:          "TestEveryStoreProgramFlushesTheSQLiteTraceOnExit",
		includeRootModule: false,
	},
}

func missingSourceScanningEdges(root string) ([]string, error) {
	edges, err := loadGoTestEdges(root)
	if err != nil {
		return nil, err
	}
	modules, err := discoverTillrModules(root)
	if err != nil {
		return nil, err
	}
	var findings []string
	for _, spec := range sourceScanningEdges {
		entry, ok := edges[spec.pkg]
		if !ok {
			findings = append(findings, fmt.Sprintf("%s: missing entry in tools/gate/go-test-edges.json", spec.pkg))
			continue
		}
		reads := make(map[string]bool, len(entry.Reads))
		for _, r := range entry.Reads {
			reads[r] = true
		}
		for _, mod := range modules {
			if !spec.includeRootModule && mod == "tools/tillr" {
				continue
			}
			rule := mod + "/"
			if !reads[rule] {
				findings = append(findings, fmt.Sprintf("%s: missing %q in tools/gate/go-test-edges.json reads", spec.pkg, rule))
				continue
			}
			tests := entry.ReadTests[rule]
			found := false
			for _, tn := range tests {
				if tn == spec.testName {
					found = true
					break
				}
			}
			if !found {
				findings = append(findings, fmt.Sprintf("%s: missing %q in tools/gate/go-test-edges.json read_tests[%q]", spec.pkg, spec.testName, rule))
			}
		}
	}
	return findings, nil
}

func cmdGoEdges(args []string) int {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-edges: could not judge: %v\n", err)
		return 4
	}
	findings, err := missingSourceScanningEdges(root)
	if err != nil {
		if os.IsNotExist(err) || os.IsPermission(err) {
			fmt.Fprintf(os.Stderr, "go-edges: could not judge: %v\n", err)
			return 4
		}
		fmt.Printf("go-edges: %v\n", err)
		return 1
	}
	for _, f := range findings {
		fmt.Printf("go-edges: %s\n", f)
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}
