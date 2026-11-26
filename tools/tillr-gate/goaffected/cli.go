package goaffected

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Run is the CLI entry point for go-affected, matching tools/gate/go_affected.py
// plus --edge-digest for tools/go-test-shards.py.
func Run(args []string) int {
	if len(args) > 0 && args[0] == InvocationGuardFlag {
		if len(args) < 4 || args[2] != "--" {
			fmt.Fprintln(os.Stderr, "go-affected: invalid --invocation-guard arguments")
			return 2
		}
		fd, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-affected: invalid lifeline fd %q: %v\n", args[1], err)
			return 2
		}
		return InvocationGuardMain(fd, args[3:])
	}

	InstallSignalHandlers()

	rootArg := "."
	base := ""
	head := "HEAD"
	hasChanged := false
	var changed []string
	dropModules := make(map[string]bool)
	selectOnly := false
	evidenceOut := ""
	verdictsInFlag := ""
	verdictsOutFlag := ""
	edgeDigestPkg := ""
	hasActiveTests := false
	activeTestsRaw := ""
	missingEdges := false
	scanTestsDir := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-h" || arg == "--help" {
			printCLIUsage()
			return 0
		}
		if arg == "--select-only" {
			selectOnly = true
			continue
		}
		if arg == "--missing-edges" {
			missingEdges = true
			continue
		}
		if arg == "--changed" {
			hasChanged = true
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				changed = append(changed, args[i])
			}
			continue
		}
		if strings.HasPrefix(arg, "--changed=") {
			hasChanged = true
			val := strings.TrimPrefix(arg, "--changed=")
			if val != "" {
				changed = append(changed, val)
			}
			continue
		}

		name, val, hasEq := strings.Cut(arg, "=")
		nextVal := func() (string, bool) {
			if hasEq {
				return val, true
			}
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}

		switch name {
		case "--root":
			v, ok := nextVal()
			if !ok {
				fmt.Fprintln(os.Stderr, "go-affected: --root requires a value")
				return 2
			}
			rootArg = v
		case "--base":
			v, ok := nextVal()
			if !ok {
				fmt.Fprintln(os.Stderr, "go-affected: --base requires a value")
				return 2
			}
			base = v
		case "--head":
			v, ok := nextVal()
			if !ok {
				fmt.Fprintln(os.Stderr, "go-affected: --head requires a value")
				return 2
			}
			head = v
		case "--drop-module":
			v, ok := nextVal()
			if !ok {
				fmt.Fprintln(os.Stderr, "go-affected: --drop-module requires a value")
				return 2
			}
			dropModules[v] = true
		case "--evidence-out":
			v, ok := nextVal()
			if !ok {
				fmt.Fprintln(os.Stderr, "go-affected: --evidence-out requires a value")
				return 2
			}
			evidenceOut = v
		case "--verdicts-in":
			v, ok := nextVal()
			if !ok {
				fmt.Fprintln(os.Stderr, "go-affected: --verdicts-in requires a value")
				return 2
			}
			verdictsInFlag = v
		case "--verdicts-out":
			v, ok := nextVal()
			if !ok {
				fmt.Fprintln(os.Stderr, "go-affected: --verdicts-out requires a value")
				return 2
			}
			verdictsOutFlag = v
		case "--edge-digest":
			v, ok := nextVal()
			if !ok {
				fmt.Fprintln(os.Stderr, "go-affected: --edge-digest requires a value")
				return 2
			}
			edgeDigestPkg = v
		case "--active-tests":
			v, ok := nextVal()
			if !ok {
				fmt.Fprintln(os.Stderr, "go-affected: --active-tests requires a value")
				return 2
			}
			hasActiveTests = true
			activeTestsRaw = v
		case "--scan-tests":
			v, ok := nextVal()
			if !ok {
				fmt.Fprintln(os.Stderr, "go-affected: --scan-tests requires a value")
				return 2
			}
			scanTestsDir = v
		default:
			fmt.Fprintf(os.Stderr, "go-affected: unknown argument %q\n", arg)
			return 2
		}
	}

	root := resolvePath(rootArg)

	if missingEdges {
		return runMissingEdges(root)
	}
	if scanTestsDir != "" {
		return runScanTests(root, scanTestsDir)
	}
	if edgeDigestPkg != "" {
		return runEdgeDigest(root, edgeDigestPkg, hasActiveTests, activeTestsRaw)
	}

	var changedPaths []string
	if hasChanged {
		changedPaths = changed
	} else if base != "" {
		var err error
		changedPaths, err = GitChangedPaths(root, base, head)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-affected: git diff failed: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintln(os.Stderr, "go-affected: either --base or --changed is required")
		return 2
	}

	verdictsIn := verdictsInFlag
	if verdictsIn == "" {
		verdictsIn = os.Getenv(GoVerdictsInEnv)
	}
	verdictsOut := verdictsOutFlag
	if verdictsOut == "" {
		verdictsOut = os.Getenv(GoVerdictsOutEnv)
	}

	var graph *Graph
	if (verdictsIn != "" || verdictsOut != "") && !selectOnly {
		var err error
		graph, err = LoadTillrGraph(root, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-affected: failed to load graph: %v\n", err)
			return 1
		}
	}

	selection, err := SelectAffectedPackages(root, changedPaths, dropModules, graph, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-affected: selection failed: %v\n", err)
		return 1
	}

	if selectOnly {
		out, err := json.MarshalIndent(selection, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-affected: marshal selection failed: %v\n", err)
			return 1
		}
		fmt.Println(string(out))
		return 0
	}

	evPath := evidenceOut
	if evPath == "" {
		evPath = os.Getenv("TILLR_GO_AFFECTED_EVIDENCE_OUT")
	}
	rc, output, _, err := RunSelectedTests(root, selection, RunOptions{
		EvidencePath: evPath,
		Graph:        graph,
		VerdictsIn:   verdictsIn,
		VerdictsOut:  verdictsOut,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-affected: %v\n", err)
		return 1
	}
	_, _ = os.Stdout.WriteString(output)
	return rc
}

func runEdgeDigest(root, importPath string, hasActiveTests bool, activeTestsRaw string) int {
	edges, err := LoadEdges(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-affected: failed to load edges: %v\n", err)
		return 1
	}
	edge, ok := edges[importPath]
	if !ok {
		fmt.Fprintf(os.Stderr, "go-affected: no edge declared for %q\n", importPath)
		return 1
	}

	var activeTests []string
	if hasActiveTests {
		trimmed := strings.TrimSpace(activeTestsRaw)
		if strings.HasPrefix(trimmed, "[") {
			if err := json.Unmarshal([]byte(trimmed), &activeTests); err != nil {
				fmt.Fprintf(os.Stderr, "go-affected: invalid --active-tests JSON: %v\n", err)
				return 2
			}
			if activeTests == nil {
				activeTests = []string{}
			}
		} else if trimmed == "" {
			activeTests = []string{}
		} else {
			parts := strings.Split(trimmed, ",")
			activeTests = make([]string, 0, len(parts))
			for _, p := range parts {
				if s := strings.TrimSpace(p); s != "" {
					activeTests = append(activeTests, s)
				}
			}
		}
	}

	var pkgInfo map[string]PkgInfo
	if len(edge.Builds) > 0 {
		graph, err := LoadTillrGraph(root, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-affected: failed to load graph: %v\n", err)
			return 1
		}
		pkgInfo = graph.PkgInfo
	}

	digest, err := ComputeEdgeDigest(root, edge, pkgInfo, activeTests, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-affected: compute edge digest failed: %v\n", err)
		return 1
	}
	fmt.Println(digest)
	return 0
}

func runMissingEdges(root string) int {
	findings, err := MissingSourceScanningEdges(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-affected: missing-edges failed: %v\n", err)
		return 1
	}
	for _, f := range findings {
		fmt.Fprintln(os.Stdout, f)
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}

func runScanTests(root, dir string) int {
	baseRoot := root
	dirRel := dir
	if filepath.IsAbs(dir) {
		baseRoot = dir
		dirRel = "."
	} else {
		dirRel = filepath.ToSlash(dir)
	}
	names, err := ScanPackageTests(baseRoot, dirRel, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-affected: scan-tests failed: %v\n", err)
		return 1
	}
	if names == nil {
		names = []string{}
	}
	out, err := json.Marshal(names)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-affected: marshal scan-tests failed: %v\n", err)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

func printCLIUsage() {
	fmt.Print(`Usage: go-affected [options]

Options:
  --root PATH               Repository root (default: .)
  --base REV                Base commit
  --head REV                Head commit (default: HEAD)
  --changed [PATH ...]      Explicit changed paths (relative to root)
  --drop-module MOD         Module to drop from selection (repeatable)
  --select-only             Print selection JSON and exit 0
  --evidence-out PATH       Write evidence JSON to this path
  --verdicts-in PATH        Read prior passing package verdicts JSON from this path
  --verdicts-out PATH       Write passing package verdicts JSON to this path
  --edge-digest PKG         Print TILLR_GATE_EDGE_INPUTS digest for PKG and exit 0
  --active-tests LIST       Comma-separated or JSON array of active tests for --edge-digest
  --missing-edges           Check cross-module source-scanning edges and exit 0/1
  --scan-tests DIR          Print JSON list of top-level Go tests in DIR and exit 0
`)
}
