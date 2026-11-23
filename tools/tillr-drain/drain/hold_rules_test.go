package drain

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
)

var reasonSubpackageDirs = []struct {
	pkg string
	dir string
}{
	{pkg: "workflow", dir: "../workflow"},
	{pkg: "remotesync", dir: "../remotesync"},
	{pkg: "fixer", dir: "../fixer"},
	{pkg: "heavyrun", dir: "../heavyrun"},
}

// allExportedReasons returns every exported Reason* string constant across drain and
// its extracted domain subpackages. Subpackages allow only string literals; drain may
// also re-export an already-enumerated subpackage constant (pkg.ReasonX). Any other
// nonliteral value fails closed so coverage cannot be skipped.
func allExportedReasons(t *testing.T) map[string]string {
	t.Helper()
	subpkgs := map[string]map[string]string{}
	merged := map[string]string{}
	for _, entry := range reasonSubpackageDirs {
		pkgReasons := exportedReasonsFromDir(t, entry.dir, nil)
		subpkgs[entry.pkg] = pkgReasons
		for name, val := range pkgReasons {
			merged[entry.pkg+"."+name] = val
		}
	}
	for name, val := range exportedReasonsFromDir(t, ".", subpkgs) {
		merged[name] = val
	}
	return merged
}

func exportedReasonsFromDir(t *testing.T, dir string, subpkgs map[string]map[string]string) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				values := spec.(*ast.ValueSpec)
				for i, name := range values.Names {
					if !strings.HasPrefix(name.Name, "Reason") || !name.IsExported() {
						continue
					}
					if len(values.Values) <= i {
						t.Fatalf("cannot enumerate %s: constant value is implicit", name.Name)
					}
					val, err := resolveReasonConstExpr(values.Values[i], subpkgs)
					if err != nil {
						t.Fatalf("cannot enumerate %s: %v", name.Name, err)
					}
					found[name.Name] = val
				}
			}
		}
	}
	return found
}

func resolveReasonConstExpr(expr ast.Expr, subpkgs map[string]map[string]string) (string, error) {
	if literal, ok := expr.(*ast.BasicLit); ok && literal.Kind == token.STRING {
		return strconv.Unquote(literal.Value)
	}
	if sel, ok := expr.(*ast.SelectorExpr); ok && subpkgs != nil {
		if pkgIdent, ok := sel.X.(*ast.Ident); ok {
			if val, exists := subpkgs[pkgIdent.Name][sel.Sel.Name]; exists {
				return val, nil
			}
			return "", fmt.Errorf("unknown subpackage constant %s.%s", pkgIdent.Name, sel.Sel.Name)
		}
	}
	return "", fmt.Errorf("update the enumerator for its expression")
}

// TestEveryReasonConstantHasAHoldRule is the single place a missing display rule is
// caught: the view and the history reader read holdrules.Rules, so a Reason constant
// without a row there fails here, in the module its author runs.
func TestEveryReasonConstantHasAHoldRule(t *testing.T) {
	reasons := allExportedReasons(t)
	if len(reasons) == 0 {
		t.Fatal("no exported reasons were enumerated")
	}
	for _, problem := range holdRuleProblems(reasons, holdrules.Rules) {
		t.Error(problem)
	}
}

func holdRuleProblems(reasons map[string]string, rules map[string]holdrules.Rule) []string {
	var problems []string
	for name, reason := range reasons {
		rule, ok := rules[reason]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s = %q has no row in holdrules.Rules (tools/tillr-drain/holdrules/holdrules.go): add its class, where and mover", name, reason))
			continue
		}
		if !slices.Contains(holdrules.Classes, rule.Class) || rule.Where == "" || rule.Mover == "" {
			problems = append(problems, fmt.Sprintf("%s = %q has an incomplete rule %+v", name, reason, rule))
		}
	}
	slices.Sort(problems)
	return problems
}

// The check must itself fail for a reason with no row, or it guards nothing.
func TestHoldRuleCheckFailsForAnUnlistedReason(t *testing.T) {
	reasons := map[string]string{"ReasonDummyForTest": "dummy-for-test"}
	if got := holdRuleProblems(reasons, holdrules.Rules); len(got) != 1 {
		t.Fatalf("a reason with no row produced %d problems, want 1: %v", len(got), got)
	}
	incomplete := map[string]holdrules.Rule{"dummy-for-test": {Class: "bogus", Where: "queue", Mover: "x"}}
	if got := holdRuleProblems(reasons, incomplete); len(got) != 1 {
		t.Fatalf("a row with an invalid class produced %d problems, want 1: %v", len(got), got)
	}
	sel, err := parser.ParseExpr("fixer.ReasonTrunkRewritten")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := resolveReasonConstExpr(sel, map[string]map[string]string{"fixer": {"ReasonTrunkRewritten": "trunk-rewritten"}}); err != nil || got != "trunk-rewritten" {
		t.Fatalf("resolveReasonConstExpr(fixer.ReasonTrunkRewritten) = %q, %v", got, err)
	}
	if _, err := resolveReasonConstExpr(sel, map[string]map[string]string{}); err == nil {
		t.Fatal("resolveReasonConstExpr accepted an unknown subpackage constant")
	}
}
