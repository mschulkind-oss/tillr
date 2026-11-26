package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var movedRunnerSites = map[string][]string{
	"tools/tillr-gate/change_gate.go":                   {"changeGateFace", "warmTrimmedGoCache"},
	"tools/tillr-gate/checks.go":                        {"checksFace"},
	"tools/tillr-gate/gate.go":                          {"gateResult", "runGateProgram", "RunGate"},
	"tools/tillr-gate/main.go":                          {"runFace", "writeRunSummary"},
	"tools/tillr-gate/memory.go":                        {"read", "publish", "annotateMemory", "run"},
	"tools/tillr-gate/run.go":                           {"runCheck", "RunWith", "runPlanChecks", "ApplyInheritedFailures", "budgetNotes"},
	"tools/tillr-gate/shadow.go":                        {"Shadow", "shadowResult"},
	"tools/tillr-gate/tree_check.go":                    {"runTreeCheck"},
	"tools/tillr-gate/watchdog.go":                      {"readFastVerdict"},
	"tools/tillr-drain/drain/events.go":                 {"gateRefusalDetail"},
	"tools/tillr-drain/drain/fixer_gate.go":             {"fixerCheck", "settleFixerGate"},
	"tools/tillr-drain/drain/fixer_resolve.go":          {"resolveProvedFixers"},
	"tools/tillr-drain/fixer/fixer_gate.go":             {"FixerCheckOverlaps", "PendingFixerVerdict"},
	"tools/tillr-drain/fixer/fixer_resolve.go":          {"ReadGreenProof", "ReadGreenProofFile", "ReadCheckProofs", "ReadProgressCheckProof", "Passed"},
	"tools/tillr-drain/fixer/fixer_wait.go":             {"FixerChecks"},
	"tools/tillr-drain/drain/fork_drift.go":             {"settleGateFailure", "markForkGateUnanswered"},
	"tools/tillr-drain/remotesync/fork_check_triage.go": {"ForkCheckDeclID", "ReadFailingSpanMeta"},
	"tools/tillr-drain/remotesync/fork_drift.go":        {"ReadForkConflicts"},
	"tools/tillr-drain/drain/gate_spans.go":             {"readFastGateSpans", "readFailingSpans", "readSpansWithStatus", "readSpansWithWatches", "readRollingCoverage", "readGateSpans"},
	"tools/tillr-drain/drain/gate_stall.go":             {"gateStallContext"},
	"tools/tillr-drain/drain/gate_timing.go":            {"readGateTimingChecks"},
	"tools/tillr-drain/heavyrun/heavy.go":               {"Run", "RunAttempt", "judgeTip", "OneLogged", "Replay", "SearchFirstRed", "attribute", "revert", "offerRevert", "resolveRevert"},
	"tools/tillr-drain/heavyrun/heavy_attribution.go":   {"CompletedHeavyRed", "FindFirstRed", "RetainRedRepairs", "ForgetGreenAttributions"},
	"tools/tillr-drain/heavyrun/heavy_early.go":         {"Offer", "recover"},
	"tools/tillr-drain/drain/heavy_go.go":               {"judgeHeavyGo"},
	"tools/tillr-drain/drain/heavy_loop.go":             {"startHeavy", "heavyStatusLine"},
	"tools/tillr-drain/heavyrun/heavy_progress.go":      {"LoadHeavyProgress", "ReuseHeavyCheck", "SaveHeavyCheck", "ReadHeavyReplay"},
	"tools/tillr-drain/drain/heavy_revert.go":           {"judgeRequired", "JudgeRequired", "revertLanding"},
	"tools/tillr-drain/drain/heavy_steps.go":            {"JudgeHeavy", "decodeHeavyVerdict", "legacyKeptLog", "runHeavyLegacy"},
	"tools/tillr-drain/drain/loop.go":                   {"verify"},
	"tools/tillr-drain/drain/obstacle_hold.go":          {"obstacleProofPassed", "releaseTrunkObstacleHolds"},
	"tools/tillr-drain/drain/obstacle_route.go":         {"routeObstacle"},
	"tools/tillr-drain/drain/pass.go":                   {"foldPendingHistory"},
	"tools/tillr-drain/drain/plan.go":                   {"readRunHistory", "retryHoldReason"},
	"tools/tillr-drain/drain/remote_sync_landing.go":    {"landRemoteSync"},
	"tools/tillr-drain/drain/returned.go":               {"gateFailingChecks"},
	"tools/tillr-drain/drain/steps.go":                  {"Gate", "finishDetachedGate", "gateFailureContext", "Replay", "Rolling"},
	"tools/tillr-drain/drain/trunk.go":                  {"judgeTrunkChecks", "openTrunkRedNotice", "trunkAlreadyRed", "redOnTip", "settleTrunkAlreadyRed", "promoteVerdictPassInherited", "intersectingRedPaths", "acquitRedWithPaths", "culpritLanding", "openFixerFor", "routeTrunkFixer", "maybeRolling", "recordRollingRun", "repeatedRollingStalls", "rollingUnknownStreak"},
	"tools/tillr-drain/drain/trunk_release.go":          {"repairedTrunkWaiter", "releaseTrunkAcquittals"},
	"tools/tillr-drain/heavy/status.go":                 {"ReadStatus"},
}

// All drain sites are moved (slice 1c and slice 1d).
// No file under tools/tillr-gate/ may appear here.
var unmovedGoFiles = []string{}

var forbiddenResultWords = map[string]bool{
	"pass":                             true,
	"PASS":                             true,
	"ok":                               true,
	"CACHED":                           true,
	"remembered":                       true,
	"fail":                             true,
	"FAIL":                             true,
	"error":                            true,
	"ERR":                              true,
	"ERROR":                            true,
	"STALE":                            true,
	"stalled":                          true,
	"STALLED":                          true,
	"not-run":                          true,
	"over-budget":                      true,
	"unavailable":                      true,
	"skip":                             true,
	"skipped: no changed path is read": true,
	"QUARANTINED":                      true,
	"dropped":                          true,
	"absent":                           true,
	"disabled":                         true,
}

var forbiddenResultConsts = map[string]bool{
	"ResultPass":        true,
	"ResultFail":        true,
	"ResultError":       true,
	"ResultStalled":     true,
	"ResultUnavailable": true,
	"ResultSkipped":     true,
	"ResultOverBudget":  true,
	"ResultDisabled":    true,
	"VerdictPass":       true,
	"VerdictFail":       true,
	"VerdictError":      true,
	"ExitStalled":       true,
	"gateExitVerified":  true,
	"gateExitFailed":    true,
}

var rawExitIdentNames = map[string]bool{
	"exit":     true,
	"exitCode": true,
	"exitVal":  true,
	"runExit":  true,
	"rc":       true,
}

func isSettlementOrEmpty(expr ast.Expr) bool {
	expr = unwrapParens(expr)
	switch v := expr.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			s, err := strconv.Unquote(v.Value)
			return err == nil && (s == "" || s == "none" || s == "inherited" || s == "pass-inherited")
		}
	case *ast.Ident:
		switch v.Name {
		case "ResultInherited", "ResultPassInherited", "VerdictPassInherited", "VerdictNone":
			return true
		}
	}
	return false
}

func unwrapParens(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.X
	}
}

func isForbiddenResultLiteralOrConst(expr ast.Expr) bool {
	expr = unwrapParens(expr)
	switch v := expr.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			s, err := strconv.Unquote(v.Value)
			return err == nil && forbiddenResultWords[s]
		}
	case *ast.Ident:
		return forbiddenResultConsts[v.Name]
	}
	return false
}

func isRawResultSelector(expr ast.Expr) (string, bool) {
	expr = unwrapParens(expr)
	switch v := expr.(type) {
	case *ast.SelectorExpr:
		switch v.Sel.Name {
		case "Result", "RawWord", "Verdict":
			return v.Sel.Name, true
		}
	case *ast.CallExpr:
		if sel, ok := unwrapParens(v.Fun).(*ast.SelectorExpr); ok && sel.Sel.Name == "ExitCode" && len(v.Args) == 0 {
			return "ExitCode()", true
		}
	case *ast.Ident:
		if rawExitIdentNames[v.Name] {
			return v.Name, true
		}
	}
	return "", false
}

var unambiguousSpanOrResultLiterals = map[string]bool{
	"CACHED":                           true,
	"FAIL":                             true,
	"ERR":                              true,
	"STALE":                            true,
	"stalled":                          true,
	"STALLED":                          true,
	"not-run":                          true,
	"over-budget":                      true,
	"unavailable":                      true,
	"skipped: no changed path is read": true,
	"QUARANTINED":                      true,
	"disabled":                         true,
}

func isUnambiguousCheckComparison(left, right ast.Expr) bool {
	isSpanOrConst := func(expr ast.Expr) bool {
		expr = unwrapParens(expr)
		switch v := expr.(type) {
		case *ast.BasicLit:
			if v.Kind == token.STRING {
				s, err := strconv.Unquote(v.Value)
				return err == nil && unambiguousSpanOrResultLiterals[s]
			}
		case *ast.Ident:
			return forbiddenResultConsts[v.Name]
		}
		return false
	}
	isResultField := func(expr ast.Expr) bool {
		sel, ok := unwrapParens(expr).(*ast.SelectorExpr)
		return ok && (sel.Sel.Name == "Result" || sel.Sel.Name == "RawWord")
	}
	if isSpanOrConst(left) || isSpanOrConst(right) {
		return true
	}
	if isResultField(left) && isForbiddenResultLiteralOrConst(right) {
		return true
	}
	if isResultField(right) && isForbiddenResultLiteralOrConst(left) {
		return true
	}
	return false
}

func scanGoClassificationAST(fset *token.FileSet, file *ast.File) []int {
	return scanGoFile(fset, file, true)
}

func scanGoFile(fset *token.FileSet, file *ast.File, fullSiteScan bool) []int {
	var lines []int
	recordLine := func(pos token.Pos) {
		line := fset.Position(pos).Line
		if !slices.Contains(lines, line) {
			lines = append(lines, line)
		}
	}
	checkPair := func(pos token.Pos, left, right ast.Expr) {
		if !fullSiteScan {
			if isUnambiguousCheckComparison(left, right) {
				recordLine(pos)
			}
			return
		}
		if isForbiddenResultLiteralOrConst(left) || isForbiddenResultLiteralOrConst(right) {
			recordLine(pos)
			return
		}
		if name, ok := isRawResultSelector(left); ok {
			if (name == "Result" || name == "Verdict") && isSettlementOrEmpty(right) {
				return
			}
			recordLine(pos)
			return
		}
		if name, ok := isRawResultSelector(right); ok {
			if (name == "Result" || name == "Verdict") && isSettlementOrEmpty(left) {
				return
			}
			recordLine(pos)
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.BinaryExpr:
			switch node.Op {
			case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
				checkPair(node.OpPos, node.X, node.Y)
			}
		case *ast.SwitchStmt:
			if node.Tag != nil {
				if fullSiteScan {
					if _, ok := isRawResultSelector(node.Tag); ok {
						recordLine(node.Switch)
					}
				}
				for _, stmt := range node.Body.List {
					clause, ok := stmt.(*ast.CaseClause)
					if !ok {
						continue
					}
					for _, expr := range clause.List {
						checkPair(clause.Case, node.Tag, expr)
					}
				}
			}
		}
		return true
	})
	slices.Sort(lines)
	return lines
}

func TestClassificationScanGoSites(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, path := range unmovedGoFiles {
		if strings.HasPrefix(path, "tools/tillr-gate/") {
			t.Fatalf("runner file %s must not remain on unmovedGoFiles", path)
		}
	}
	unmoved := make(map[string]bool, len(unmovedGoFiles))
	for _, path := range unmovedGoFiles {
		unmoved[path] = true
	}

	for relPath, wantFuncs := range movedRunnerSites {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, filepath.Join(repoRoot, relPath), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", relPath, err)
		}
		seen := map[string]bool{}
		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				seen[fn.Name.Name] = true
			}
		}
		for _, want := range wantFuncs {
			if !seen[want] {
				t.Errorf("%s: missing moved site function %s", relPath, want)
			}
		}
	}

	for _, dir := range []string{"tools/tillr-gate", "tools/tillr-drain"} {
		rootDir := filepath.Join(repoRoot, dir)
		err := filepath.WalkDir(rootDir, func(fullPath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(repoRoot, fullPath)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if unmoved[rel] {
				return nil
			}
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, fullPath, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", rel, err)
			}
			fullRunnerScan := strings.HasPrefix(rel, "tools/tillr-gate/")
			if lines := scanGoFile(fset, parsed, fullRunnerScan); len(lines) != 0 {
				t.Errorf("%s: raw result or exit comparisons at lines %v", rel, lines)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

func TestClassificationScanGoPositiveControls(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []int
	}{
		{
			name: "result constant comparison",
			src:  "package p\nfunc f(c Check) bool {\n\treturn c.Result == ResultFail\n}\n",
			want: []int{3},
		},
		{
			name: "result literal comparison",
			src:  "package p\nfunc f(w string) bool {\n\treturn w == \"pass\"\n}\n",
			want: []int{3},
		},
		{
			name: "switch on exit or result",
			src:  "package p\nfunc f(exit int) string {\n\tswitch exit {\n\tcase 0:\n\t\treturn \"pass\"\n\t}\n\treturn \"error\"\n}\n",
			want: []int{3, 4},
		},
		{
			name: "exitErr ExitCode comparison",
			src:  "package p\nfunc f(e errType) bool {\n\treturn e.ExitCode() == 4\n}\n",
			want: []int{3},
		},
		{
			name: "settlements empty and classified outcome allowed",
			src: "package p\nfunc f(c Check, r Classified) bool {\n" +
				"\tif c.Result == \"\" || c.Result == ResultInherited || c.Result == ResultPassInherited {\n\t\treturn false\n\t}\n" +
				"\treturn r.Outcome == Pass && r.Detail == DetailStalled\n}\n",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, "case.go", tc.src, 0)
			if err != nil {
				t.Fatal(err)
			}
			got := scanGoClassificationAST(fset, parsed)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got lines %v, want %v", got, tc.want)
			}
		})
	}
}
