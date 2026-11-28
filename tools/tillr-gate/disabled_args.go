package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-gate/shards"
	"github.com/mschulkind-oss/tillr/record"
)

// disabledArgsFace is `tillr-gate disabled-args`: it scopes Go's `-skip` to one package.
//
// `-skip` has no package qualifier, and sibling packages carry same-named tests, so a
// module-wide `go test ./... -skip` would skip the wrong tests. `--packages` names the
// packages of a module that have listed tests; `--package` prints the `-skip` arguments
// for one. Empty stdout means nothing is listed.
func disabledArgsFace(args []string) int {
	flags := flag.NewFlagSet("disabled-args", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	module := flags.String("module", "", "the Go module directory, slash-separated and relative to --root (required)")
	root := flags.String("root", "", "the repository root (default: located from --module or '.', else '.')")
	listPackages := flags.Bool("packages", false, "print the import paths in the module with active disabled tests")
	pkg := flags.String("package", "", "print the -skip arguments for this package import path")
	var prove stringList
	flags.Var(&prove, "prove-disabled", "a listed selector to run anyway (repeatable)")
	if code, answered := parseFlags(flags, args); answered {
		return code
	}
	if *module == "" {
		fmt.Fprintln(os.Stderr, "tillr-gate disabled-args: --module is required")
		return record.ExitUsage
	}
	if *listPackages == (*pkg != "") {
		fmt.Fprintln(os.Stderr, "tillr-gate disabled-args: name exactly one of --packages and --package <import-path>")
		return record.ExitUsage
	}
	repo, mod := *root, filepath.ToSlash(filepath.Clean(*module))
	if repo == "" {
		repo = shards.LocateRepoRoot(*module, false, true)
		if repo == "" {
			repo = shards.LocateRepoRoot(".", false, true)
		}
		if repo == "" {
			repo = "."
		}
		abs, err := filepath.Abs(*module)
		if err == nil {
			if rel, rerr := filepath.Rel(repo, abs); rerr == nil && !strings.HasPrefix(rel, "..") {
				mod = filepath.ToSlash(rel)
			}
		}
	}
	byPackage, err := shards.LoadModuleSelectors(repo, mod, prove)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate disabled-args: %v\n", err)
		return record.ExitNegative
	}
	if *listPackages {
		var pkgs []string
		for p, sels := range byPackage {
			if len(sels) > 0 {
				pkgs = append(pkgs, p)
			}
		}
		sort.Strings(pkgs)
		for _, p := range pkgs {
			fmt.Println(p)
		}
		return record.ExitOK
	}
	if pattern := shards.FullSkipPattern(byPackage[*pkg]); pattern != "" {
		fmt.Println("-skip")
		fmt.Println(pattern)
	}
	return record.ExitOK
}
