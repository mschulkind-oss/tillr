"""Record criterion judgments through the consumer CLI, then request completion."""
import argparse
import datetime
import hashlib
import json
import subprocess
import sys
from pathlib import Path

flags = ["--item", "--run", "--store", "--root", "--artifact-dir"]
if sys.argv[1:] == ["describe"]:
    print(json.dumps({"hooks": {"complete-item": flags}}))
    sys.exit(0)
parser = argparse.ArgumentParser()
parser.add_argument("hook", choices=["complete-item"])
for flag in flags:
    parser.add_argument(flag, required=True)
args = parser.parse_args()
root = Path(args.root)
answer = json.loads((root / f"{args.item}-assessment.json").read_text())
if answer.get("complete") is False:
    print(json.dumps({"complete": False, "reason": answer["reason"]}))
    sys.exit(0)
shown = json.loads(subprocess.check_output([
    "tillr", "item", "show", "--store", args.store, "--item", args.item,
]))["payload"]
now = datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z")
# Each criterion's current text is hashed, so an old judgment cannot finish new work.
for criterion in shown["criteria"]:
    verdict = {
        "record_version": 1,
        "kind": "item.verdict",
        "id": f"{args.run}-{criterion['criterion_id']}",
        "producer": {"system": "tillr", "utility": "recorded-verdict-example", "version": "0.1.0"},
        "created_at": now,
        "payload": {
            "item_id": args.item,
            "criterion_id": criterion["criterion_id"],
            "criterion_hash": hashlib.sha256(criterion["text"].encode()).hexdigest(),
            "verdict": answer["verdict"],
            "claim_class": criterion["claim_class"],
            "reason": answer["reason"],
            "evidence": [f"doc:{args.item}-assessment.json"],
            "actor_kind": "mechanical",
            "actor": "recorded-verdict-stub",
        },
    }
    path = root / f"{args.item}-{criterion['criterion_id']}-verdict.json"
    path.write_text(json.dumps(verdict) + "\n")
    subprocess.run([
        "tillr", "item", "verdict", "record", "--store", args.store,
        "--input", str(path), "--now", now,
    ], check=True, stdout=subprocess.DEVNULL)
print(json.dumps({
    "complete": True, "state": answer["state"], "reason": answer["reason"],
    "resolves_via": f"doc:{args.item}-assessment.json",
}))
