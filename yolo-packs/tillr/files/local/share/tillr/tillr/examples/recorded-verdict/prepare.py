"""Resolve program paths in the copied example; never open a store or invoke Git."""
import json
import sys
from pathlib import Path

root = Path(__file__).resolve().parent
config = root / "config/tillr"
config.mkdir(parents=True, exist_ok=True)
(config / "backend-policy.json").write_text(json.dumps({
    "default_backend": "stub",
    "seats": {"build": "stub"},
    "backends": {"stub": {
        "adapter": str(root / "stub-adapter.sh"),
        "model": "deterministic-example",
    }},
}, indent=2) + "\n")
(config / "commands.json").write_text(json.dumps({
    "reproduce": {"argv": [sys.executable, str(root / "stage.py"), "reproduce"], "flags": {}},
}, indent=2) + "\n")
