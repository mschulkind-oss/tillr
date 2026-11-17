#!/usr/bin/env python3
"""Admit declarations by summing fast budgets, without measuring elapsed time."""

import argparse
import json
from pathlib import Path
import sys

# The fast tier's cap. `tools/tillr-gate/run.go` declares the same number as
# FastTierCapMS, the most a fast check's ceiling can be, and its
# TestTheTierCapIsTheAdmissionTestsLimit fails when the two disagree.
LIMIT_MS = 9000


def total_budget(directory, refusals=None):
    """The sum of every fast `budget_ms`. A fast `timeout_ms` outside [budget_ms,
    LIMIT_MS] is appended to `refusals`: the runner clamps a fast check's ceiling to that
    range, so such a declaration states a ceiling the runner will not keep."""
    total = 0
    for path in sorted(directory.glob("*.json")):
        try:
            declaration = json.loads(path.read_text())
            if not isinstance(declaration, dict):
                raise ValueError("expected one declaration object")
            tier = declaration.get("tier")
            if tier not in ("fast", "heavy"):
                raise ValueError("tier must be fast or heavy")
            if tier == "fast":
                budget = declaration.get("budget_ms")
                # bool is a Python int subclass, but is not a declared cost.
                if type(budget) is not int or budget <= 0:
                    raise ValueError("budget_ms must be a positive integer for tier fast")
                total += budget
                if "timeout_ms" in declaration:
                    ceiling = declaration["timeout_ms"]
                    if type(ceiling) is not int or ceiling <= 0:
                        raise ValueError("timeout_ms must be a positive integer when declared")
                    if not budget <= ceiling <= LIMIT_MS and refusals is not None:
                        refusals.append(f"{path}: timeout_ms {ceiling} is a fast check's ceiling and must lie "
                                        f"between its budget_ms {budget} and the tier cap {LIMIT_MS} ms")
        except (OSError, ValueError) as error:
            raise ValueError(f"{path}: {error}") from error
    return total


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", nargs="?", type=Path, default=Path(__file__).parent)
    # The runner appends these arguments to every change check. Admission is a
    # property of the declarations in the current tree, not of the revision range.
    parser.add_argument("base", nargs="?")
    parser.add_argument("head", nargs="?")
    args = parser.parse_args()
    if not args.directory.is_dir():
        print(f"gate-budget: {args.directory}: declaration directory is missing", file=sys.stderr)
        return 4
    refusals = []
    try:
        total = total_budget(args.directory, refusals)
    except ValueError as error:
        print(f"gate-budget: {error}", file=sys.stderr)
        return 4
    status = "exceeds" if total > LIMIT_MS else "within"
    print(f"gate-budget: fast budget sum {total} ms {status} {LIMIT_MS} ms")
    for refusal in refusals:
        print(f"gate-budget: {refusal}")
    return int(total > LIMIT_MS or bool(refusals))


if __name__ == "__main__":
    sys.exit(main())
