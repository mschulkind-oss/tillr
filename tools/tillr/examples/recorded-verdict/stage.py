"""Deterministic command and agent stand-ins, not a real product reproduction."""
import json
import os
import sys
from pathlib import Path

root = Path(__file__).resolve().parent
node = sys.argv[1]
if node not in ("reproduce", "investigate") or os.environ["TILLR_NODE_ID"] != node:
    raise ValueError("unexpected workflow node")
item = os.environ["TILLR_ITEM_ID"]
outcome = json.loads((root / "outcomes.json").read_text())[item]
reproduction = root / f"{item}-reproduction.json"
assessment = root / f"{item}-assessment.json"
if node == "reproduce":
    reproduction.write_text(json.dumps(outcome) + "\n")
else:
    # The second stage consumes the first stage's evidence before judging.
    observed = json.loads(reproduction.read_text())
    if observed != outcome:
        raise ValueError("reproduction disagrees with expected demonstration outcome")
    assessment.write_text(json.dumps(observed) + "\n")
with (root / "stage-calls.jsonl").open("a") as calls:
    calls.write(json.dumps({"item": item, "node": node, "cwd": os.getcwd()}) + "\n")
print(f"{node}: {item}: {outcome['reason']}")
print("RESULT: done")
