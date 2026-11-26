package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var goSites = map[string][]string{
	"tools/gate/tree_check.go":                            {"runTreeCheck"},
	"tools/gate/change_gate.go":                           {"cmdChangeGate", "warmTrimmedGoCache"},
	"tools/gate/change_common.go":                         {"readStoredResult", "judgeDocumentSet"},
	"tools/gate/go_build.go":                              {"runGoBuild"},
	"tools/tillr-gate/change_gate.go":                     {"changeGateFace", "warmTrimmedGoCache"},
	"tools/tillr-gate/checks.go":                          {"checksFace"},
	"tools/tillr-gate/gate.go":                            {"gateResult", "runGateProgram", "RunGate"},
	"tools/tillr-gate/main.go":                            {"runFace", "writeRunSummary"},
	"tools/tillr-gate/memory.go":                          {"read", "publish", "annotateMemory", "run"},
	"tools/tillr-gate/run.go":                             {"runCheck", "RunWith", "runPlanChecks", "ApplyInheritedFailures", "budgetNotes"},
	"tools/tillr-gate/shadow.go":                          {"Shadow", "shadowResult"},
	"tools/tillr-gate/tree_check.go":                      {"runTreeCheck"},
	"tools/tillr-gate/watchdog.go":                        {"readFastVerdict"},
	"tools/tillr-drain/drain/events.go":                   {"gateRefusalDetail"},
	"tools/tillr-drain/drain/fixer_gate.go":               {"fixerCheck", "settleFixerGate"},
	"tools/tillr-drain/drain/fixer_resolve.go":            {"resolveProvedFixers"},
	"tools/tillr-drain/fixer/fixer_gate.go":               {"FixerCheckOverlaps", "PendingFixerVerdict"},
	"tools/tillr-drain/fixer/fixer_resolve.go":            {"ReadGreenProof", "ReadGreenProofFile", "ReadCheckProofs", "ReadProgressCheckProof", "Passed"},
	"tools/tillr-drain/fixer/fixer_wait.go":               {"FixerChecks"},
	"tools/tillr-drain/drain/fork_drift.go":               {"settleGateFailure", "markForkGateUnanswered"},
	"tools/tillr-drain/remotesync/fork_check_triage.go":   {"ForkCheckDeclID", "ReadFailingSpanMeta"},
	"tools/tillr-drain/remotesync/fork_drift.go":          {"ReadForkConflicts"},
	"tools/tillr-drain/internal/gatespans/gate_spans.go":  {"ReadFastGateSpans", "ReadFailingSpans", "ReadSpansWithStatus", "ReadSpansWithWatches", "ReadRollingCoverage", "ReadGateSpans"},
	"tools/tillr-drain/internal/gatespans/gate_stall.go":  {"GateStallContext"},
	"tools/tillr-drain/internal/gatespans/gate_timing.go": {"ReadGateTimingChecks"},
	"tools/tillr-drain/heavyrun/heavy.go":                 {"Run", "RunAttempt", "judgeTip", "OneLogged", "Replay", "SearchFirstRed", "attribute", "revert", "offerRevert", "resolveRevert"},
	"tools/tillr-drain/heavyrun/heavy_attribution.go":     {"CompletedHeavyRed", "FindFirstRed", "RetainRedRepairs", "ForgetGreenAttributions"},
	"tools/tillr-drain/heavyrun/heavy_early.go":           {"Offer", "recover"},
	"tools/tillr-drain/internal/gatespans/heavy_go.go":    {"JudgeHeavyGo"},
	"tools/tillr-drain/drain/heavy_loop.go":               {"startHeavy", "heavyStatusLine"},
	"tools/tillr-drain/heavyrun/heavy_progress.go":        {"LoadHeavyProgress", "ReuseHeavyCheck", "SaveHeavyCheck", "ReadHeavyReplay"},
	"tools/tillr-drain/drain/heavy_revert.go":             {"judgeRequired", "JudgeRequired", "revertLanding"},
	"tools/tillr-drain/internal/gatespans/heavy_steps.go": {"JudgeHeavy", "DecodeHeavyVerdict", "LegacyKeptLog", "RunHeavyLegacy"},
	"tools/tillr-drain/drain/loop.go":                     {"verify"},
	"tools/tillr-drain/drain/obstacle_hold.go":            {"obstacleProofPassed", "releaseTrunkObstacleHolds"},
	"tools/tillr-drain/drain/obstacle_route.go":           {"routeObstacle"},
	"tools/tillr-drain/drain/pass.go":                     {"foldPendingHistory"},
	"tools/tillr-drain/drain/plan.go":                     {"readRunHistory", "retryHoldReason"},
	"tools/tillr-drain/drain/remote_sync_landing.go":      {"landRemoteSync"},
	"tools/tillr-drain/drain/returned.go":                 {"gateFailingChecks"},
	"tools/tillr-drain/drain/steps.go":                    {"Gate", "finishDetachedGate", "gateFailureContext", "Replay", "Rolling"},
	"tools/tillr-drain/drain/trunk.go": {
		"judgeTrunkChecks", "openTrunkRedNotice", "trunkAlreadyRed", "redOnTip",
		"settleTrunkAlreadyRed", "promoteVerdictPassInherited", "intersectingRedPaths",
		"acquitRedWithPaths", "culpritLanding", "openFixerFor", "routeTrunkFixer",
		"maybeRolling", "recordRollingRun", "repeatedRollingStalls", "rollingUnknownStreak",
	},
	"tools/tillr-drain/drain/trunk_release.go": {"repairedTrunkWaiter", "releaseTrunkAcquittals"},
	"tools/tillr-drain/heavy/status.go":        {"ReadStatus"},
	"tools/tillr-loop/loop/loop.go":            {"execute"},
}

var goResultWords = []string{
	"pass", "PASS", "ok", "CACHED", "remembered", "fail", "FAIL", "error", "ERR", "ERROR",
	"STALE", "stalled", "STALLED", "not-run", "over-budget", "unavailable", "skip",
	"skipped: no changed path is read", "QUARANTINED", "dropped", "absent", "disabled",
}

var goResultConsts = []string{
	"ResultPass", "ResultFail", "ResultError", "ResultStalled", "ResultUnavailable",
	"ResultSkipped", "ResultOverBudget", "ResultDisabled", "VerdictPass", "VerdictFail",
	"VerdictError", "ExitStalled", "gateExitVerified", "gateExitFailed",
}

var goAllowedSettlementRHS = map[string]bool{
	`""`: true, `"none"`: true, `"inherited"`: true, `"pass-inherited"`: true,
	"ResultInherited": true, "ResultPassInherited": true, "VerdictPassInherited": true, "VerdictNone": true,
}

func stripGoComments(source string) string {
	var out strings.Builder
	i, n := 0, len(source)
	for i < n {
		ch := source[i]
		if ch == '/' && i+1 < n && source[i+1] == '/' {
			end := strings.IndexByte(source[i:], '\n')
			if end == -1 {
				break
			}
			i += end
			continue
		}
		if ch == '/' && i+1 < n && source[i+1] == '*' {
			end := strings.Index(source[i+2:], "*/")
			var segment string
			if end == -1 {
				segment = source[i:]
			} else {
				segment = source[i : i+2+end+2]
			}
			out.WriteString(strings.Repeat("\n", strings.Count(segment, "\n")))
			if end == -1 {
				break
			}
			i += 2 + end + 2
			continue
		}
		if ch == '"' {
			j := i + 1
			for j < n {
				if source[j] == '\\' {
					j += 2
					continue
				}
				if source[j] == '"' {
					j++
					break
				}
				j++
			}
			out.WriteString(source[i:j])
			i = j
			continue
		}
		if ch == '`' {
			end := strings.IndexByte(source[i+1:], '`')
			if end == -1 {
				out.WriteString(source[i:])
				break
			}
			out.WriteString(source[i : i+1+end+1])
			i += 1 + end + 1
			continue
		}
		out.WriteByte(ch)
		i++
	}
	return out.String()
}

func rawGoComparisons(source string, fullRunnerScan bool) []int {
	cleaned := stripGoComments(source)
	quotedWords := make([]string, len(goResultWords))
	for i, w := range goResultWords {
		quotedWords[i] = regexp.QuoteMeta(`"` + w + `"`)
	}
	wordsAlt := strings.Join(quotedWords, "|")
	constsAlt := strings.Join(goResultConsts, "|")

	spanWords := []string{
		"CACHED", "FAIL", "ERR", "STALE", "stalled", "STALLED", "not-run",
		"over-budget", "unavailable", "skipped: no changed path is read",
		"QUARANTINED", "disabled",
	}
	quotedSpan := make([]string, len(spanWords))
	for i, w := range spanWords {
		quotedSpan[i] = regexp.QuoteMeta(`"` + w + `"`)
	}
	spanAlt := strings.Join(quotedSpan, "|")

	switchStartRe := regexp.MustCompile(`^switch\s+[^{]+\{`)
	switchRawRe := regexp.MustCompile(`^switch\s+(?:[\w.]+\.(?:Result|RawWord|Verdict|ExitCode\(\))|(?:exit|exitCode|exitVal|runExit|rc))\s*\{`)
	fullEqRe := regexp.MustCompile(`(?:==|!=)\s*(?:` + wordsAlt + `|\b(?:` + constsAlt + `)\b)|(?:` + wordsAlt + `|\b(?:` + constsAlt + `)\b)\s*(?:==|!=)`)
	caseWordRe := regexp.MustCompile(`^\s*case\s+.*(?:` + wordsAlt + `|\b(?:` + constsAlt + `)\b)`)
	cmpSideRe := regexp.MustCompile(`([\w.()]+)\s*(==|!=|<=|>=|<|>)\s*("[^"]*"|[\w.()]+)`)
	spanEqRe := regexp.MustCompile(`(?:==|!=)\s*(?:` + spanAlt + `|\b(?:` + constsAlt + `)\b)|(?:` + spanAlt + `|\b(?:` + constsAlt + `)\b)\s*(?:==|!=)`)
	spanFieldRe := regexp.MustCompile(`\.(?:Result|RawWord)\s*(?:==|!=)\s*(?:` + wordsAlt + `)`)

	found := make(map[int]bool)
	inValueSwitch := false
	braceDepth := 0
	switchDepth := 0
	for idx, line := range strings.Split(cleaned, "\n") {
		lineno := idx + 1
		stripped := strings.TrimSpace(line)
		if switchStartRe.MatchString(stripped) {
			inValueSwitch = true
			switchDepth = braceDepth
			if fullRunnerScan && switchRawRe.MatchString(stripped) {
				found[lineno] = true
			}
		}
		braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
		if inValueSwitch && braceDepth <= switchDepth {
			inValueSwitch = false
		}

		if fullRunnerScan {
			if fullEqRe.MatchString(line) {
				found[lineno] = true
			}
			if inValueSwitch && caseWordRe.MatchString(line) {
				found[lineno] = true
			}
			for _, m := range cmpSideRe.FindAllStringSubmatch(line, -1) {
				lhs, rhs := m[1], m[3]
				for _, pair := range [2][2]string{{lhs, rhs}, {rhs, lhs}} {
					side, other := pair[0], pair[1]
					if strings.HasSuffix(side, ".Result") || strings.HasSuffix(side, ".Verdict") {
						if !goAllowedSettlementRHS[other] {
							found[lineno] = true
						}
					} else if strings.HasSuffix(side, ".RawWord") || strings.HasSuffix(side, ".ExitCode()") ||
						side == "exit" || side == "exitCode" || side == "exitVal" || side == "runExit" || side == "rc" {
						found[lineno] = true
					}
				}
			}
		} else {
			if spanEqRe.MatchString(line) || spanFieldRe.MatchString(line) {
				found[lineno] = true
			}
		}
	}
	out := make([]int, 0, len(found))
	for k := range found {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func TestClassificationScanJudgmentSitesHaveNoRawComparisons(t *testing.T) {
	root, _ := loadTestContext(t)

	for relPath, fns := range goSites {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
		if err != nil {
			t.Fatalf("%s: %v", relPath, err)
		}
		for _, fn := range fns {
			re := regexp.MustCompile(`(?m)^func\s+(?:\([^)]+\)\s+)?` + regexp.QuoteMeta(fn) + `\s*\(`)
			if !re.Match(src) {
				t.Fatalf("%s: missing func %s", relPath, fn)
			}
		}
	}

	for _, rootDir := range []string{"tools/tillr-gate", "tools/tillr-drain", "tools/tillr-loop"} {
		absDir := filepath.Join(root, filepath.FromSlash(rootDir))
		err := filepath.WalkDir(absDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			relSlash := filepath.ToSlash(rel)
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			fullScan := strings.HasPrefix(relSlash, "tools/tillr-gate/")
			if hits := rawGoComparisons(string(data), fullScan); len(hits) > 0 {
				t.Errorf("%s: raw result comparisons at lines %v", relSlash, hits)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for relPath, fns := range goSites {
		if !strings.HasPrefix(relPath, "tools/gate/") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		for _, fn := range fns {
			body := extractGoFunc(src, fn)
			if body == "" {
				t.Fatalf("%s: could not extract func %s", relPath, fn)
			}
			if hits := rawGoComparisons(body, true); len(hits) > 0 {
				t.Errorf("%s (%s): raw result comparisons at relative lines %v", relPath, fn, hits)
			}
		}
	}
}

func extractGoFunc(src, name string) string {
	re := regexp.MustCompile(`(?m)^func\s+(?:\([^)]+\)\s+)?` + regexp.QuoteMeta(name) + `\s*\(`)
	loc := re.FindStringIndex(src)
	if loc == nil {
		return ""
	}
	rest := src[loc[0]:]
	brace := strings.IndexByte(rest, '{')
	if brace < 0 {
		return ""
	}
	depth := 0
	for i := brace; i < len(rest); i++ {
		if rest[i] == '{' {
			depth++
		} else if rest[i] == '}' {
			depth--
			if depth == 0 {
				return rest[:i+1]
			}
		}
	}
	return rest
}

func TestClassificationScanGoScanPositiveControl(t *testing.T) {
	if got := rawGoComparisons("package p\nfunc f(c Check) bool {\n\treturn c.Result == ResultFail\n}\n", true); len(got) != 1 || got[0] != 3 {
		t.Fatalf("expected [3], got %v", got)
	}
	if got := rawGoComparisons("package p\nfunc f(w string) bool {\n\treturn w == \"pass\"\n}\n", true); len(got) != 1 || got[0] != 3 {
		t.Fatalf("expected [3], got %v", got)
	}
	if got := rawGoComparisons("package p\nfunc f(exit int) string {\n\tswitch exit {\n\tcase 0:\n\t\treturn \"pass\"\n\t}\n}\n", true); len(got) != 1 || got[0] != 3 {
		t.Fatalf("expected [3], got %v", got)
	}
	clean := "package p\nfunc f(c Check, r Res) bool {\n\tif c.Result == \"\" || c.Result == ResultInherited || c.Result == ResultPassInherited {\n\t\treturn false\n\t}\n\treturn r.Outcome == Pass && r.Detail == DetailStalled\n}\n"
	if got := rawGoComparisons(clean, true); len(got) != 0 {
		t.Fatalf("expected [], got %v", got)
	}
	if got := rawGoComparisons("package p\nfunc f(c Check) bool {\n\treturn c.Result == \"FAIL\"\n}\n", false); len(got) != 1 || got[0] != 3 {
		t.Fatalf("span mode expected [3], got %v", got)
	}
}

func TestEarlyLoopStallBranchCannotBypassClassification(t *testing.T) {
	source := "package loop\nfunc execute(kind string, code int) int {\n\tif kind == \"gate\" && code == ExitStalled {\n\t\treturn ExitStalled\n\t}\n\treturn 0\n}\n"
	got := rawGoComparisons(source, false)
	if len(got) != 1 || got[0] != 3 {
		t.Fatalf("got %v, want [3]", got)
	}
}
