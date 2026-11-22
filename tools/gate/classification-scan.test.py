#!/usr/bin/env python3
"""Guard moved judgment sites against reintroducing raw result comparisons."""
import ast
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
SITES = {
    "tools/gate/tree-check.py": ("main",),
    "tools/gate/change-gate.py": ("main", "warm_trimmed_go_cache", "preserve_fast_timing"),
    "tools/gate/checks/change_common.py": ("stored_result", "judge_document_set"),
    "tools/gate/checks/go-build.py": ("main",),
}
UNMOVED_PYTHON = ("tools/fork-check.py",)


def raw_word_operand(node):
    if isinstance(node, ast.Name):
        return node.id in {"result", "word", "raw_word"}
    # Stored records and runner payloads expose their raw word as `result`.
    if isinstance(node, ast.Subscript):
        key = node.slice
    elif (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
          and node.func.attr == "get" and node.args):
        key = node.args[0]
    else:
        return False
    return isinstance(key, ast.Constant) and key.value == "result"


def raw_operand(node):
    if isinstance(node, (ast.Tuple, ast.List)):
        return any(raw_operand(element) for element in node.elts)
    if isinstance(node, ast.Name) and node.id in {"code", "exit_code", "rc"}:
        return True
    if isinstance(node, ast.Attribute) and node.attr == "returncode":
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
        loop = ast.parse((ROOT / "tools/tillr-loop/tillr-loop").read_text())
        execute = next(n for n in loop.body if isinstance(n, ast.FunctionDef) and n.name == "execute")
        self.assertEqual(loop_gate_comparisons(execute), [])
        self.assertEqual((ROOT / "tools/tillr-loop/tillr-loop").read_bytes(),
                         (ROOT / "tools/tillr-drain/drain/tillr-loop.py").read_bytes())
        self.assertEqual(UNMOVED_PYTHON, ("tools/fork-check.py",))

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
        loop = ast.parse((ROOT / "tools/tillr-loop/tillr-loop").read_text())
        execute = next(n for n in loop.body if isinstance(n, ast.FunctionDef) and n.name == "execute")
        gate_index = next(i for i, n in enumerate(execute.body) if isinstance(n, ast.If)
                          and ast.unparse(n.test) == "kind == 'gate'")
        old_branch, = ast.parse('''\
if kind == "gate" and code == EXIT_STALLED:
    return EXIT_STALLED
''').body
        execute.body.insert(gate_index, old_branch)
        self.assertEqual(loop_gate_comparisons(execute), [old_branch.lineno])

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


if __name__ == "__main__":
    unittest.main()
