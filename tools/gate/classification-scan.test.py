#!/usr/bin/env python3
"""Guard moved judgment sites against reintroducing raw result comparisons."""
import ast
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[2]
SITES = {
    "tools/tillr-gate/tree-check.py": ("main",),
    "tools/tillr-gate/change-gate.py": ("main", "warm_trimmed_go_cache"),
    "tools/gate/checks/change_common.py": ("stored_result", "judge_document_set"),
    "tools/gate/checks/go-build.py": ("main",),
}
UNMOVED_PYTHON = ()
GO_SITES = {
    "tools/tillr-gate/checks.go": ("checksFace",),
    "tools/tillr-gate/gate.go": ("gateResult", "runGateProgram", "RunGate"),
    "tools/tillr-gate/main.go": ("runFace", "writeRunSummary"),
    "tools/tillr-gate/memory.go": ("read", "publish", "annotateMemory", "run"),
    "tools/tillr-gate/run.go": ("runCheck", "RunWith", "runPlanChecks", "ApplyInheritedFailures", "budgetNotes"),
    "tools/tillr-gate/shadow.go": ("Shadow", "shadowResult"),
    "tools/tillr-gate/watchdog.go": ("readFastVerdict",),
    "tools/tillr-drain/drain/events.go": ("gateRefusalDetail",),
    "tools/tillr-drain/drain/fixer_gate.go": ("fixerCheck", "settleFixerGate"),
    "tools/tillr-drain/drain/fixer_resolve.go": ("resolveProvedFixers",),
    "tools/tillr-drain/fixer/fixer_gate.go": ("FixerCheckOverlaps", "PendingFixerVerdict"),
    "tools/tillr-drain/fixer/fixer_resolve.go": ("ReadGreenProof", "ReadGreenProofFile", "ReadCheckProofs", "ReadProgressCheckProof", "Passed"),
    "tools/tillr-drain/fixer/fixer_wait.go": ("FixerChecks",),
    "tools/tillr-drain/drain/fork_drift.go": ("settleGateFailure", "markForkGateUnanswered"),
    "tools/tillr-drain/remotesync/fork_check_triage.go": ("ForkCheckDeclID", "ReadFailingSpanMeta"),
    "tools/tillr-drain/remotesync/fork_drift.go": ("ReadForkConflicts",),
    "tools/tillr-drain/drain/gate_spans.go": ("readFastGateSpans", "readFailingSpans", "readSpansWithStatus", "readSpansWithWatches", "readRollingCoverage", "readGateSpans"),
    "tools/tillr-drain/drain/gate_stall.go": ("gateStallContext",),
    "tools/tillr-drain/drain/gate_timing.go": ("readGateTimingChecks",),
    "tools/tillr-drain/heavyrun/heavy.go": ("Run", "RunAttempt", "judgeTip", "OneLogged", "Replay", "SearchFirstRed", "attribute", "revert", "offerRevert", "resolveRevert"),
    "tools/tillr-drain/heavyrun/heavy_attribution.go": ("CompletedHeavyRed", "FindFirstRed", "RetainRedRepairs", "ForgetGreenAttributions"),
    "tools/tillr-drain/heavyrun/heavy_early.go": ("Offer", "recover"),
    "tools/tillr-drain/drain/heavy_go.go": ("judgeHeavyGo",),
    "tools/tillr-drain/drain/heavy_loop.go": ("startHeavy", "heavyStatusLine"),
    "tools/tillr-drain/heavyrun/heavy_progress.go": ("LoadHeavyProgress", "ReuseHeavyCheck", "SaveHeavyCheck", "ReadHeavyReplay"),
    "tools/tillr-drain/drain/heavy_revert.go": ("judgeRequired", "JudgeRequired", "revertLanding"),
    "tools/tillr-drain/drain/heavy_steps.go": ("JudgeHeavy", "decodeHeavyVerdict", "legacyKeptLog", "runHeavyLegacy"),
    "tools/tillr-drain/drain/loop.go": ("verify",),
    "tools/tillr-drain/drain/obstacle_hold.go": ("obstacleProofPassed", "releaseTrunkObstacleHolds"),
    "tools/tillr-drain/drain/obstacle_route.go": ("routeObstacle",),
    "tools/tillr-drain/drain/pass.go": ("foldPendingHistory",),
    "tools/tillr-drain/drain/plan.go": ("readRunHistory", "retryHoldReason"),
    "tools/tillr-drain/drain/remote_sync_landing.go": ("landRemoteSync",),
    "tools/tillr-drain/drain/returned.go": ("gateFailingChecks",),
    "tools/tillr-drain/drain/steps.go": ("Gate", "finishDetachedGate", "gateFailureContext", "Replay", "Rolling"),
    "tools/tillr-drain/drain/trunk.go": (
        "judgeTrunkChecks",
        "openTrunkRedNotice",
        "trunkAlreadyRed",
        "redOnTip",
        "settleTrunkAlreadyRed",
        "promoteVerdictPassInherited",
        "intersectingRedPaths",
        "acquitRedWithPaths",
        "culpritLanding",
        "openFixerFor",
        "routeTrunkFixer",
        "maybeRolling",
        "recordRollingRun",
        "repeatedRollingStalls",
        "rollingUnknownStreak",
    ),
    "tools/tillr-drain/drain/trunk_release.go": ("repairedTrunkWaiter", "releaseTrunkAcquittals"),
    "tools/tillr-drain/heavy/status.go": ("ReadStatus",),
    "tools/tillr-loop/loop/loop.go": ("execute",),
}
UNMOVED_GO = ()

GO_RESULT_WORDS = (
    "pass", "PASS", "ok", "CACHED", "remembered", "fail", "FAIL", "error", "ERR", "ERROR",
    "STALE", "stalled", "STALLED", "not-run", "over-budget", "unavailable", "skip",
    "skipped: no changed path is read", "QUARANTINED", "dropped", "absent", "disabled",
)
GO_RESULT_CONSTS = (
    "ResultPass", "ResultFail", "ResultError", "ResultStalled", "ResultUnavailable",
    "ResultSkipped", "ResultOverBudget", "ResultDisabled", "VerdictPass", "VerdictFail",
    "VerdictError", "ExitStalled", "gateExitVerified", "gateExitFailed",
)
GO_ALLOWED_SETTLEMENT_RHS = {
    '""', '"none"', '"inherited"', '"pass-inherited"',
    "ResultInherited", "ResultPassInherited", "VerdictPassInherited", "VerdictNone",
}


def strip_go_comments(source):
    """Blank Go line and block comments while keeping line numbers intact."""
    out = []
    i, n = 0, len(source)
    while i < n:
        ch = source[i]
        if ch == "/" and i + 1 < n and source[i + 1] == "/":
            end = source.find("\n", i)
            if end == -1:
                break
            i = end
            continue
        if ch == "/" and i + 1 < n and source[i + 1] == "*":
            end = source.find("*/", i + 2)
            segment = source[i:] if end == -1 else source[i:end + 2]
            out.append("\n" * segment.count("\n"))
            if end == -1:
                break
            i = end + 2
            continue
        if ch == '"':
            j = i + 1
            while j < n:
                if source[j] == "\\":
                    j += 2
                    continue
                if source[j] == '"':
                    j += 1
                    break
                j += 1
            out.append(source[i:j])
            i = j
            continue
        if ch == "`":
            end = source.find("`", i + 1)
            if end == -1:
                out.append(source[i:])
                break
            out.append(source[i:end + 1])
            i = end + 1
            continue
        out.append(ch)
        i += 1
    return "".join(out)


def raw_go_comparisons(source, *, full_runner_scan=True):
    cleaned = strip_go_comments(source)
    words_alt = "|".join(re.escape(f'"{w}"') for w in GO_RESULT_WORDS)
    consts_alt = "|".join(GO_RESULT_CONSTS)
    found = set()
    in_value_switch = False
    brace_depth = 0
    switch_depth = None
    for lineno, line in enumerate(cleaned.splitlines(), 1):
        stripped = line.strip()
        if re.match(r"^switch\s+[^{]+\{", stripped):
            in_value_switch = True
            switch_depth = brace_depth
            if full_runner_scan and re.search(r"^switch\s+(?:[\w.]+\.(?:Result|RawWord|Verdict|ExitCode\(\))|(?:exit|exitCode|exitVal|runExit|rc))\s*\{", stripped):
                found.add(lineno)
        brace_depth += line.count("{") - line.count("}")
        if in_value_switch and brace_depth <= switch_depth:
            in_value_switch = False
            switch_depth = None

        if full_runner_scan:
            if re.search(rf"(?:==|!=)\s*(?:{words_alt}|\b(?:{consts_alt})\b)|(?:{words_alt}|\b(?:{consts_alt})\b)\s*(?:==|!=)", line):
                found.add(lineno)
            if in_value_switch and re.search(rf"^\s*case\s+.*(?:{words_alt}|\b(?:{consts_alt})\b)", line):
                found.add(lineno)
            for match in re.finditer(r"([\w.()]+)\s*(==|!=|<=|>=|<|>)\s*(\"[^\"]*\"|[\w.()]+)", line):
                lhs, _, rhs = match.groups()
                for side, other in ((lhs, rhs), (rhs, lhs)):
                    if side.endswith((".Result", ".Verdict")):
                        if other not in GO_ALLOWED_SETTLEMENT_RHS:
                            found.add(lineno)
                    elif side.endswith((".RawWord", ".ExitCode()")) or side in {"exit", "exitCode", "exitVal", "runExit", "rc"}:
                        found.add(lineno)
        else:
            span_words = ("CACHED", "FAIL", "ERR", "STALE", "stalled", "STALLED", "not-run",
                          "over-budget", "unavailable", "skipped: no changed path is read",
                          "QUARANTINED", "disabled")
            span_alt = "|".join(re.escape(f'"{w}"') for w in span_words)
            if re.search(rf"(?:==|!=)\s*(?:{span_alt}|\b(?:{consts_alt})\b)|(?:{span_alt}|\b(?:{consts_alt})\b)\s*(?:==|!=)", line):
                found.add(lineno)
            if re.search(rf"\.(?:Result|RawWord)\s*(?:==|!=)\s*(?:{words_alt})", line):
                found.add(lineno)
    return sorted(found)


def _dict_key(node):
    if isinstance(node, ast.Subscript):
        return node.slice
    if (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
            and node.func.attr == "get" and node.args):
        return node.args[0]
    return None


def raw_word_operand(node):
    if isinstance(node, ast.Name):
        return node.id in {"result", "word", "raw_word"}
    if isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
        return node.func.id == "check_status"
    # Stored records and runner payloads expose their raw word as `result`.
    key = _dict_key(node)
    return isinstance(key, ast.Constant) and key.value == "result"


def raw_operand(node):
    if isinstance(node, (ast.Tuple, ast.List)):
        return any(raw_operand(element) for element in node.elts)
    if isinstance(node, ast.Name) and node.id in {"code", "exit_code", "rc"}:
        return True
    if isinstance(node, ast.Attribute) and node.attr in {
        "returncode", "rc", "failed", "conflict", "unanswered", "verdict",
    }:
        return True
    key = _dict_key(node)
    if isinstance(key, ast.Constant) and key.value in {"exit_code", "rc"}:
        return True
    return raw_word_operand(node)


def raw_truth_test(node):
    if isinstance(node, ast.BoolOp):
        return any(raw_truth_test(value) for value in node.values)
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.Not):
        return raw_truth_test(node.operand)
    return raw_operand(node)


def raw_exit_comparisons(nodes, *, sequencing=()):
    """Only judgments, not wire shape, forwarding, or rendered outcomes."""
    found = set()
    for node in nodes:
        for decision in ast.walk(node):
            if isinstance(decision, ast.Compare):
                # The tree runner uses a narrower word syntax than the general wire.
                if decision in sequencing or ast.unparse(decision) == "result not in ('pass', 'fail', 'stalled')":
                    continue
                if any(raw_operand(operand) for operand in [decision.left, *decision.comparators]):
                    found.add(decision.lineno)
            elif isinstance(decision, (ast.If, ast.IfExp, ast.While)) and raw_truth_test(decision.test):
                # The change adapter forwards the fast runner's exit unchanged;
                # it does not judge the runner's checks again.
                if ast.unparse(decision) != 'if checked.returncode:\n    return checked.returncode':
                    found.add(decision.test.lineno)
    return sorted(found)


def loop_gate_comparisons(execute):
    gate = next((n for n in execute.body if isinstance(n, ast.If)
                 and ast.unparse(n.test) == "kind == 'gate'"), None)
    # Scan the surrounding statements too: an earlier exit branch could bypass
    # the installed face. Only the gate's explicit else path is non-gate handling.
    nodes = [part for node in execute.body for part in (gate.body if node is gate else [node])]
    sequencing = set()
    for node in execute.body:
        if not isinstance(node, ast.For):
            continue
        for step in node.body:
            if not isinstance(step, ast.If) or step.orelse:
                continue
            # Preparation has no verdict, and the step runner stops at the first
            # nonzero exit. Neither check classifies a gate judgment.
            loop, test = ast.unparse(node.iter), ast.unparse(step.test)
            if (loop == "steps[:judged_from]" and test == "result.returncode != 0"
                    and ast.unparse(step.body[-1]) == "return EXIT_EXTERNAL"):
                sequencing.add(step.test)
            if (loop == "steps[judged_from:]" and test == "code != EXIT_PASS"
                    and len(step.body) == 1 and isinstance(step.body[0], ast.Break)):
                sequencing.add(step.test)
    return raw_exit_comparisons(nodes, sequencing=sequencing)


class ClassificationScanTest(unittest.TestCase):
    def test_judgment_sites_have_no_raw_exit_pairings(self):
        for path, names in SITES.items():
            tree = ast.parse((ROOT / path).read_text())
            functions = [n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name in names]
            self.assertEqual({n.name for n in functions}, set(names), path)
            self.assertEqual(raw_exit_comparisons(functions), [], path)
        self.assertEqual(UNMOVED_PYTHON, ())
        self.assertEqual([p for p in UNMOVED_GO if p.startswith("tools/tillr-gate/")], [])
        for path, names in GO_SITES.items():
            source = (ROOT / path).read_text()
            for fn in names:
                self.assertRegex(source, rf"(?m)^func\s+(?:\([^)]+\)\s+)?{re.escape(fn)}\s*\(", path)
        unmoved_go = set(UNMOVED_GO)
        for root_dir in ("tools/tillr-gate", "tools/tillr-drain", "tools/tillr-loop"):
            for go_file in sorted((ROOT / root_dir).rglob("*.go")):
                if go_file.name.endswith("_test.go"):
                    continue
                rel = go_file.relative_to(ROOT).as_posix()
                if rel in unmoved_go:
                    continue
                self.assertEqual(
                    raw_go_comparisons(go_file.read_text(), full_runner_scan=rel.startswith("tools/tillr-gate/")),
                    [],
                    rel,
                )

    def test_go_scan_positive_control(self):
        self.assertEqual(raw_go_comparisons('package p\nfunc f(c Check) bool {\n\treturn c.Result == ResultFail\n}\n'), [3])
        self.assertEqual(raw_go_comparisons('package p\nfunc f(w string) bool {\n\treturn w == "pass"\n}\n'), [3])
        self.assertEqual(raw_go_comparisons('package p\nfunc f(exit int) string {\n\tswitch exit {\n\tcase 0:\n\t\treturn "pass"\n\t}\n}\n'), [3])
        self.assertEqual(
            raw_go_comparisons('package p\nfunc f(c Check, r Res) bool {\n\tif c.Result == "" || c.Result == ResultInherited || c.Result == ResultPassInherited {\n\t\treturn false\n\t}\n\treturn r.Outcome == Pass && r.Detail == DetailStalled\n}\n'),
            [],
        )

    def test_scan_positive_control(self):
        source = ast.parse('def judge(code):\n    return "pass" if code == 0 else "fail"\n')
        self.assertEqual(raw_exit_comparisons(source.body), [2])
        source = ast.parse('def judge(word):\n    return word == "pass"\n')
        self.assertEqual(raw_exit_comparisons(source.body), [2])

    def test_tree_runner_exit_word_pairing_is_not_shape_validation(self):
        source = ast.parse('''\
def main():
    if (done.returncode, result) not in ((0, "pass"), (1, "fail"), (6, "stalled")):
        raise ValueError
''')
        self.assertEqual(raw_exit_comparisons(source.body), [2])

    def test_go_build_exit_truth_test_is_not_rendering(self):
        source = ast.parse('''\
def main():
    if code:
        failed = True
''')
        self.assertEqual(raw_exit_comparisons(source.body), [2])

    def test_early_loop_stall_branch_cannot_bypass_classification(self):
        source = '''package loop
func execute(kind string, code int) int {
	if kind == "gate" && code == ExitStalled {
		return ExitStalled
	}
	return 0
}
'''
        self.assertEqual(raw_go_comparisons(source), [3])

    def test_scan_allows_shape_checks_forwarding_and_rendering(self):
        source = ast.parse('''\
def main():
    if result not in ("pass", "fail", "stalled") or exit_kind != word_kind:
        raise ValueError
    if checked.returncode:
        return checked.returncode
    if reply["outcome"] == "fail":
        return 1
''')
        self.assertEqual(raw_exit_comparisons(source.body), [])

    def test_cached_word_comparisons_are_not_rendering(self):
        for expression in ('data.get("result") != verdict', 'record["result"] == "pass"'):
            with self.subTest(expression=expression):
                source = ast.parse(f'def stored_result():\n    return {expression}\n')
                self.assertEqual(raw_exit_comparisons(source.body), [2])
        source = ast.parse('def stored_result():\n    return answer["outcome"] == verdict\n')
        self.assertEqual(raw_exit_comparisons(source.body), [])

    def test_legacy_gate_and_replay_pairings_are_caught(self):
        for expression in (
            'check.rc == 0',
            'check.unanswered',
            'check.conflict',
            'check_status(check) == "FAIL"',
            'run["exit_code"] == 0',
        ):
            with self.subTest(expression=expression):
                source = ast.parse(f'def site(check, run):\n    if {expression}:\n        return 1\n')
                self.assertEqual(raw_exit_comparisons(source.body), [2])
        source = ast.parse('''\
def verdict_of(run):
    return run["detail"], run["exit_code"]
''')
        self.assertEqual(raw_exit_comparisons(source.body), [])


if __name__ == "__main__":
    unittest.main()
