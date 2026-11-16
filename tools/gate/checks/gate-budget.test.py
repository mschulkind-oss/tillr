#!/usr/bin/env python3
"""Fixture tests for arithmetic admission of fast check declarations."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

PROGRAM = Path(__file__).with_name("gate-budget.py")


class GateBudgetTest(unittest.TestCase):
    def invoke(self, budgets):
        # Tests run beside the program, so this check reads only tools/gate/checks/.
        # Its fixtures live in the lane's designated scratch, not in the repository.
        scratch = os.environ.get("YOLO_DURABLE_DIR")
        if not scratch:
            self.fail("YOLO_DURABLE_DIR is required for fixture storage")
        with tempfile.TemporaryDirectory(dir=scratch, prefix="gate-budget-") as tmp:
            directory = Path(tmp)
            for name, tier, cost in budgets:
                declaration = {"id": name, "label": name, "command": ["true"], "scope": "tree", "tier": tier}
                declaration["budget_ms" if tier == "fast" else "timeout_ms"] = cost
                (directory / f"{name}.json").write_text(json.dumps(declaration))
            # Neither a neighboring program nor a heavy timeout belongs in the sum.
            (directory / "program.py").write_text("not JSON")
            before = {p.name: p.read_bytes() for p in directory.iterdir()}
            result = subprocess.run(["python3", str(PROGRAM), str(directory)], capture_output=True, text=True)
            self.assertEqual(before, {p.name: p.read_bytes() for p in directory.iterdir()})
            return result

    def test_passes_at_9000(self):
        result = self.invoke([("one", "fast", 4000), ("two", "fast", 5000), ("slow", "heavy", 90001)])
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("9000", result.stdout)

    def test_fails_when_added_check_exceeds_9000(self):
        result = self.invoke([("one", "fast", 4000), ("two", "fast", 5000), ("added", "fast", 1)])
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("9001", result.stdout)
        self.assertIn("9000", result.stdout)

    def test_does_not_read_a_clock(self):
        # Execute the program with common clock APIs replaced by functions that fail.
        # Arithmetic must work even where no wall or monotonic clock is readable.
        scratch = os.environ.get("YOLO_DURABLE_DIR")
        if not scratch:
            self.fail("YOLO_DURABLE_DIR is required for fixture storage")
        with tempfile.TemporaryDirectory(dir=scratch, prefix="gate-budget-clock-") as tmp:
            (Path(tmp) / "one.json").write_text(json.dumps({"id": "one", "tier": "fast", "budget_ms": 9000}))
            harness = """
import runpy, sys, time

def forbidden(*args, **kwargs):
    raise AssertionError('budget admission read a clock')

for name in ('time', 'monotonic', 'perf_counter', 'process_time', 'time_ns', 'monotonic_ns', 'perf_counter_ns', 'process_time_ns'):
    setattr(time, name, forbidden)
sys.argv = [sys.argv[1], sys.argv[2]]
runpy.run_path(sys.argv[0], run_name='__main__')
"""
            result = subprocess.run(["python3", "-c", harness, str(PROGRAM), tmp], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_invalid_cost_is_not_a_zero_budget(self):
        scratch = os.environ.get("YOLO_DURABLE_DIR")
        if not scratch:
            self.fail("YOLO_DURABLE_DIR is required for fixture storage")
        for value in [None, 0, -1, True, 1.5, "100"]:
            with self.subTest(value=value), tempfile.TemporaryDirectory(dir=scratch, prefix="gate-budget-invalid-") as tmp:
                (Path(tmp) / "invalid.json").write_text(json.dumps({"id": "bad", "tier": "fast", "budget_ms": value}))
                result = subprocess.run(["python3", str(PROGRAM), tmp], capture_output=True, text=True)
                self.assertEqual(result.returncode, 4, result.stdout + result.stderr)
                self.assertIn("invalid.json", result.stderr)
                self.assertIn("budget_ms", result.stderr)


if __name__ == "__main__":
    unittest.main()
