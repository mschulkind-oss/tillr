package drain

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

type drainSiteRef struct {
	File   string
	Symbol string
}

// Slice 1c moved sites (D1-D16, D34, and readGateTimingChecks), scanned strictly
// at function/method granularity across both exclusive and shared files.
var slice1cDrainSites = []drainSiteRef{
	// D1
	{"drain/steps.go", "Gate"},
	{"drain/steps.go", "finishDetachedGate"},
	// D2
	{"drain/steps.go", "gateFailureContext"},
	{"drain/returned.go", "gateFailingChecks"},
	{"internal/gatespans/gate_spans.go", "ReadFastGateSpans"},
	{"internal/gatespans/gate_spans.go", "ReadFailingSpans"},
	{"internal/gatespans/gate_spans.go", "ReadSpansWithStatus"},
	{"internal/gatespans/gate_spans.go", "ReadSpansWithWatches"},
	{"internal/gatespans/gate_spans.go", "ReadMatchingSpans"},
	// D3
	{"drain/loop.go", "verify"},
	// D4
	{"internal/gatespans/gate_stall.go", "GateStallContext"},
	{"drain/pass.go", "foldPendingHistory"},
	// D5
	{"remotesync/fork_drift.go", "ReadForkConflicts"},
	// D6
	{"drain/fork_drift.go", "settleGateFailure"},
	{"drain/fork_drift.go", "markForkGateUnanswered"},
	// D7
	{"drain/heavy_revert.go", "judgeRequired"},
	{"drain/heavy_revert.go", "JudgeRequired"},
	// D8
	{"drain/fixer_gate.go", "fixerCheck"},
	{"drain/fixer_gate.go", "settleFixerGate"},
	{"fixer/fixer_gate.go", "PendingFixerVerdict"},
	// D9
	{"drain/events.go", "gateRefusalDetail"},
	{"drain/plan.go", "readRunHistory"},
	{"drain/plan.go", "retryHoldReason"},
	{"drain/remote_sync_landing.go", "landRemoteSync"},
	// D10
	{"drain/trunk.go", "trunkAlreadyRed"},
	{"drain/trunk.go", "redOnTip"},
	{"drain/obstacle_route.go", "routeObstacle"},
	// D11
	{"drain/trunk.go", "judgeTrunkChecks"},
	// D12
	{"drain/steps.go", "Replay"},
	// D13
	{"drain/trunk.go", "settleTrunkAlreadyRed"},
	{"drain/trunk.go", "intersectingRedPaths"},
	{"drain/trunk.go", "acquitRedWithPaths"},
	// D14
	{"drain/trunk.go", "promoteVerdictPassInherited"},
	// D15
	{"drain/trunk.go", "culpritLanding"},
	// D16
	{"drain/trunk.go", "openFixerFor"},
	{"drain/trunk.go", "routeTrunkFixer"},
	{"fixer/fixer_wait.go", "FixerChecks"},
	{"fixer/fixer_gate.go", "FixerCheckOverlaps"},
	{"remotesync/fork_check_triage.go", "ForkCheckDeclID"},
	// D34
	{"drain/trunk.go", "openTrunkRedNotice"},
	// Fast-gate timing check reader
	{"internal/gatespans/gate_timing.go", "ReadGateTimingChecks"},
}

var scannedDrainSites = append([]drainSiteRef(nil), append(slice1cDrainSites, slice1dDrainSites...)...)

// Slice 1d moved sites (D17-D33).
var slice1dDrainSites = []drainSiteRef{
	// D17
	{"drain/steps.go", "Rolling"},
	// D18
	{"drain/trunk.go", "maybeRolling"},
	{"drain/trunk.go", "recordRollingRun"},
	{"drain/trunk.go", "repeatedRollingStalls"},
	{"drain/trunk.go", "rollingUnknownStreak"},
	// D19
	{"internal/gatespans/gate_spans.go", "ReadRollingCoverage"},
	{"internal/gatespans/gate_spans.go", "ReadGateSpans"},
	{"remotesync/fork_check_triage.go", "ReadFailingSpanMeta"},
	// D20
	{"internal/gatespans/heavy_steps.go", "JudgeHeavy"},
	{"internal/gatespans/heavy_steps.go", "DecodeHeavyVerdict"},
	{"internal/gatespans/heavy_steps.go", "LegacyKeptLog"},
	// D21
	{"internal/gatespans/heavy_steps.go", "RunHeavyLegacy"},
	// D22
	{"internal/gatespans/heavy_go.go", "JudgeHeavyGo"},
	// D23
	{"heavyrun/heavy.go", "Run"},
	{"heavyrun/heavy.go", "RunAttempt"},
	{"heavyrun/heavy.go", "judgeTip"},
	// D24
	{"heavyrun/heavy_progress.go", "LoadHeavyProgress"},
	{"heavyrun/heavy_progress.go", "ReuseHeavyCheck"},
	{"heavyrun/heavy_progress.go", "SaveHeavyCheck"},
	// D25
	{"heavyrun/heavy_attribution.go", "CompletedHeavyRed"},
	// D26
	{"heavyrun/heavy.go", "OneLogged"},
	{"heavyrun/heavy.go", "Replay"},
	{"heavyrun/heavy_progress.go", "ReadHeavyReplay"},
	// D27
	{"heavyrun/heavy.go", "SearchFirstRed"},
	{"heavyrun/heavy.go", "attribute"},
	{"heavyrun/heavy_attribution.go", "FindFirstRed"},
	{"heavyrun/heavy_early.go", "Offer"},
	{"heavyrun/heavy_early.go", "recover"},
	// D28
	{"heavyrun/heavy_attribution.go", "RetainRedRepairs"},
	{"heavyrun/heavy_attribution.go", "ForgetGreenAttributions"},
	// D29
	{"heavyrun/heavy.go", "revert"},
	{"heavyrun/heavy.go", "offerRevert"},
	{"heavyrun/heavy.go", "resolveRevert"},
	{"drain/heavy_revert.go", "revertLanding"},
	// D30
	{"drain/heavy_loop.go", "startHeavy"},
	{"drain/heavy_loop.go", "heavyStatusLine"},
	{"heavy/status.go", "ReadStatus"},
	// D31
	{"fixer/fixer_resolve.go", "ReadGreenProof"},
	{"fixer/fixer_resolve.go", "ReadGreenProofFile"},
	{"fixer/fixer_resolve.go", "ReadCheckProofs"},
	{"fixer/fixer_resolve.go", "ReadProgressCheckProof"},
	{"fixer/fixer_resolve.go", "Passed"},
	// D32
	{"drain/fixer_resolve.go", "resolveProvedFixers"},
	// D33
	{"drain/obstacle_hold.go", "obstacleProofPassed"},
	{"drain/obstacle_hold.go", "releaseTrunkObstacleHolds"},
	{"drain/trunk_release.go", "repairedTrunkWaiter"},
	{"drain/trunk_release.go", "releaseTrunkAcquittals"},
}

// No drain site remains unmoved after slice 1c and 1d.
var unmovedDrainSites = []drainSiteRef{}

var drainForbiddenResultWords = map[string]bool{
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

var drainForbiddenResultConsts = map[string]bool{
	"ResultPass":           true,
	"ResultFail":           true,
	"ResultError":          true,
	"ResultStalled":        true,
	"ResultUnavailable":    true,
	"ResultSkipped":        true,
	"ResultOverBudget":     true,
	"ResultDisabled":       true,
	"VerdictPass":          true,
	"VerdictFail":          true,
	"VerdictError":         true,
	"GateStallExit":        true,
	"fixerUpstreamDropped": true,
}

var drainUnambiguousSpanLiterals = map[string]bool{
	"CACHED":                           true,
	"remembered":                       true,
	"FAIL":                             true,
	"PASS":                             true,
	"ERR":                              true,
	"STALE":                            true,
	"STALLED":                          true,
	"not-run":                          true,
	"over-budget":                      true,
	"unavailable":                      true,
	"skipped: no changed path is read": true,
	"QUARANTINED":                      true,
}

var drainAllowedSettlementConsts = map[string]bool{
	"GatePassed":            true,
	"GatePassedInherited":   true,
	"GateFailed":            true,
	"GateStalled":           true,
	"GateErrored":           true,
	"VerdictPassInherited":  true,
	"VerdictNone":           true,
	"LandLanded":            true,
	"rollingOutcomeStalled": true,
}

func drainStringLit(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}

func isDrainOutcomeField(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		return e.Sel.Name == "Outcome"
	case *ast.Ident:
		return e.Name == "outcome"
	}
	return false
}

func isDrainResultOrStatusField(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		return e.Sel.Name == "Result" || e.Sel.Name == "Status" || e.Sel.Name == "Memory"
	case *ast.IndexExpr:
		if sel, ok := e.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "gateResults" {
			return true
		}
		if s, ok := drainStringLit(e.Index); ok && s == "result" {
			return true
		}
	}
	return false
}

func isDrainSettlementExpr(expr ast.Expr) bool {
	if s, ok := drainStringLit(expr); ok {
		return s == "" || s == "none" || s == "inherited" || s == "pass-inherited"
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return drainAllowedSettlementConsts[e.Name]
	case *ast.SelectorExpr:
		return drainAllowedSettlementConsts[e.Sel.Name]
	}
	return false
}

func isDrainForbiddenWordOrConst(expr ast.Expr, strictSite bool) bool {
	if s, ok := drainStringLit(expr); ok {
		if strictSite {
			return drainForbiddenResultWords[s]
		}
		return drainUnambiguousSpanLiterals[s]
	}
	switch e := expr.(type) {
	case *ast.Ident:
		if drainForbiddenResultConsts[e.Name] {
			return true
		}
		if strictSite && (e.Name == "GateFailed" || e.Name == "GateStalled" || e.Name == "GatePassed" || e.Name == "GateErrored") {
			return true
		}
	case *ast.SelectorExpr:
		return drainForbiddenResultConsts[e.Sel.Name]
	}
	return false
}

func isDrainExitSubject(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == "code" || e.Name == "exit" || e.Name == "rc" || e.Name == "exitCode"
	case *ast.SelectorExpr:
		return e.Sel.Name == "ExitCode"
	case *ast.StarExpr:
		return isDrainExitSubject(e.X)
	case *ast.CallExpr:
		// Method call `cmd.ProcessState.ExitCode()` / `exitErr.ExitCode()` has 0 args;
		// tillr error helper `ExitCode(err)` / `item.ExitCode(err)` has 1 arg.
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ExitCode" && len(e.Args) == 0 {
			return true
		}
	}
	return false
}

func isAllowedUsageExitComparison(left, right ast.Expr) bool {
	isUsage := func(expr ast.Expr) bool {
		sel, ok := expr.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "ExitUsage"
	}
	return (isDrainExitSubject(left) && isUsage(right)) || (isDrainExitSubject(right) && isUsage(left))
}

func isDrainRunResultSelector(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Result" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "run"
}

func isDrainRunSummaryWord(expr ast.Expr) bool {
	s, ok := drainStringLit(expr)
	if !ok {
		return false
	}
	switch s {
	case "green", "red", "red-unfiled", "unknown":
		return true
	}
	return false
}

func isDrainGoTestEventAction(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Action" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "event"
}

func isDrainGoTestActionWord(expr ast.Expr) bool {
	s, ok := drainStringLit(expr)
	if !ok {
		return false
	}
	switch s {
	case "pass", "fail", "skip":
		return true
	}
	return false
}

func isForbiddenDrainComparison(left, right ast.Expr, strictSite bool) bool {
	if (isDrainOutcomeField(left) && isDrainSettlementExpr(right)) || (isDrainOutcomeField(right) && isDrainSettlementExpr(left)) {
		return false
	}
	if ((isDrainOutcomeField(left) || isDrainRunResultSelector(left)) && isDrainRunSummaryWord(right)) ||
		((isDrainOutcomeField(right) || isDrainRunResultSelector(right)) && isDrainRunSummaryWord(left)) {
		return false
	}
	if (isDrainGoTestEventAction(left) && isDrainGoTestActionWord(right)) ||
		(isDrainGoTestEventAction(right) && isDrainGoTestActionWord(left)) {
		return false
	}
	if strictSite {
		if isAllowedUsageExitComparison(left, right) {
			return false
		}
		if isDrainExitSubject(left) || isDrainExitSubject(right) {
			return true
		}
		if isDrainResultOrStatusField(left) && !isDrainSettlementExpr(right) {
			return true
		}
		if isDrainResultOrStatusField(right) && !isDrainSettlementExpr(left) {
			return true
		}
	}
	return isDrainForbiddenWordOrConst(left, strictSite) || isDrainForbiddenWordOrConst(right, strictSite)
}

func scanDrainFunc(fset *token.FileSet, fn *ast.FuncDecl, strictSite bool) []int {
	if fn.Body == nil {
		return nil
	}
	flagged := map[int]bool{}
	flag := func(pos token.Pos) {
		if pos.IsValid() {
			flagged[fset.Position(pos).Line] = true
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.BinaryExpr:
			if stmt.Op != token.EQL && stmt.Op != token.NEQ {
				return true
			}
			if isForbiddenDrainComparison(stmt.X, stmt.Y, strictSite) {
				flag(stmt.OpPos)
			}
		case *ast.SwitchStmt:
			tag := stmt.Tag
			if strictSite && tag != nil && (isDrainExitSubject(tag) || isDrainResultOrStatusField(tag)) {
				flag(stmt.Switch)
			}
			for _, bodyStmt := range stmt.Body.List {
				clause, ok := bodyStmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expr := range clause.List {
					if tag != nil {
						if isForbiddenDrainComparison(tag, expr, strictSite) {
							flag(clause.Case)
						}
					} else if isDrainForbiddenWordOrConst(expr, strictSite) {
						flag(clause.Case)
					}
				}
			}
		}
		return true
	})
	lines := make([]int, 0, len(flagged))
	for line := range flagged {
		lines = append(lines, line)
	}
	slices.Sort(lines)
	return lines
}

func TestClassificationScanDrainSites(t *testing.T) {
	moduleRoot := filepath.Clean("..")
	scanned := map[drainSiteRef]bool{}
	for _, ref := range scannedDrainSites {
		scanned[ref] = true
	}
	unmoved := map[drainSiteRef]bool{}
	for _, ref := range unmovedDrainSites {
		if scanned[ref] {
			t.Fatalf("site %v appears in both scannedDrainSites and unmovedDrainSites", ref)
		}
		unmoved[ref] = true
	}
	if len(unmovedDrainSites) != 0 {
		t.Fatalf("unmovedDrainSites must be empty, got %v", unmovedDrainSites)
	}
	for _, ref := range slice1cDrainSites {
		if unmoved[ref] {
			t.Fatalf("slice 1c site %v must not remain in unmovedDrainSites", ref)
		}
	}
	for _, ref := range slice1dDrainSites {
		if unmoved[ref] {
			t.Fatalf("slice 1d site %v must not remain in unmovedDrainSites", ref)
		}
	}

	seen := map[drainSiteRef]bool{}
	for _, pkgDir := range []string{"drain", "internal", "heavy", "workflow", "remotesync", "fixer", "heavyrun"} {
		rootDir := filepath.Join(moduleRoot, pkgDir)
		err := filepath.WalkDir(rootDir, func(fullPath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(moduleRoot, fullPath)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, fullPath, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", rel, err)
			}
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				ref := drainSiteRef{File: rel, Symbol: fn.Name.Name}
				seen[ref] = true
				if unmoved[ref] {
					continue
				}
				if lines := scanDrainFunc(fset, fn, scanned[ref]); len(lines) != 0 {
					t.Errorf("%s (%s): raw check result or exit comparisons at lines %v", rel, fn.Name.Name, lines)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", pkgDir, err)
		}
	}

	for _, ref := range scannedDrainSites {
		if !seen[ref] {
			t.Errorf("scannedDrainSites entry %v matches no function in %s", ref, ref.File)
		}
	}
	for _, ref := range unmovedDrainSites {
		if !seen[ref] {
			t.Errorf("unmovedDrainSites entry %v matches no function in %s", ref, ref.File)
		}
	}
}

func TestClassificationScanDrainPositiveControls(t *testing.T) {
	cases := []struct {
		name   string
		strict bool
		src    string
		want   []int
	}{
		{
			name:   "per-check result word comparison in moved site",
			strict: true,
			src:    "package drain\nfunc f(check item) bool {\n\treturn check.Result == \"fail\"\n}\n",
			want:   []int{3},
		},
		{
			name:   "check span status comparison in moved site",
			strict: true,
			src:    "package drain\nfunc f(span gateSpan) bool {\n\treturn span.Status == \"FAIL\"\n}\n",
			want:   []int{3},
		},
		{
			name:   "local answer word comparison in moved site",
			strict: true,
			src:    "package drain\nfunc f(answer string) bool {\n\treturn answer == \"dropped\"\n}\n",
			want:   []int{3},
		},
		{
			name:   "process exit code switch in moved site",
			strict: true,
			src:    "package drain\nfunc f(cmd proc) bool {\n\tswitch cmd.ProcessState.ExitCode() {\n\tcase 0:\n\t\treturn true\n\t}\n\treturn false\n}\n",
			want:   []int{3, 4},
		},
		{
			name:   "unambiguous span literal outside site list",
			strict: false,
			src:    "package drain\nfunc helper(s string) bool {\n\treturn s == \"QUARANTINED\"\n}\n",
			want:   []int{3},
		},
		{
			name:   "settlements GateOutcome and classified outcomes allowed",
			strict: true,
			src: "package drain\nfunc f(g GateOutcome, v verdict, r Classified) bool {\n" +
				"\tif v.Result == \"\" || v.Result == GatePassedInherited || v.Result == \"inherited\" {\n\t\treturn true\n\t}\n" +
				"\treturn g.Outcome == GateFailed && r.Outcome == Pass && r.Detail == DetailStalled\n}\n",
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
			fn := parsed.Decls[0].(*ast.FuncDecl)
			got := scanDrainFunc(fset, fn, tc.strict)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got lines %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDrainClassificationScan(t *testing.T) {
	t.Run("sites", TestClassificationScanDrainSites)
	t.Run("positive_controls", TestClassificationScanDrainPositiveControls)
}
