"""Observe the configured legacy gate without replacing its planner or scheduler."""
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import time

source, destination, tip, retry_proof = sys.argv[1:5]
path = Path(destination)
spec = importlib.util.spec_from_file_location("tillr_heavy_legacy_gate", source)
gate = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = gate
spec.loader.exec_module(gate)
# These are required observation surfaces, not permission for a silent uncached run.
original = gate.execute_check
identity = gate.declaration_id
status = gate.check_status
state = {"tip_sha": tip, "checks": {}, "reused": []}
if path.exists():
    saved = json.loads(path.read_text())
    if saved["tip_sha"] == tip:
        state["checks"] = saved["checks"]
# Named stall retries run outside this adapter. The runner saves their passes
# before starting the next retry; import those passes into the scheduler's cache.
retry_path = Path(retry_proof)
if retry_path.exists():
    retries = json.loads(retry_path.read_text())
    if retries["tip_sha"] == tip:
        for key, check in retries["checks"].items():
            if check["result"] == "pass":
                state["checks"][key] = {
                    "seconds": check["duration_ms"] / 1000,
                    "proved_at": gate.now_iso(),
                }
lock = threading.Lock()


def publish():
    # Called only while holding lock. Rename publishes a complete snapshot, even
    # when the gate's worker threads finish checks at the same time.
    fd, temporary = tempfile.mkstemp(prefix=".heavy-legacy-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as out:
            json.dump(state, out)
            out.write("\n")
            out.flush()
            os.fsync(out.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def observe(check, root, diff_base, *args, **kwargs):
    key = identity(check.kind, check.label, root)
    with lock:
        saved = state["checks"].get(key)
        if saved:
            check.rc = 0
            check.cached = True
            check.cache_key = "heavy-tip:" + tip
            check.cache_hit = {"at": saved["proved_at"]}
            check.seconds = saved["seconds"]
            check.start_ns = check.end_ns = time.time_ns()
            check.ended = gate.now_iso()
            state["reused"].append(key)
            publish()
            return
    original(check, root, diff_base, *args, **kwargs)
    if status(check) in ("PASS", "ok", "CACHED"):
        with lock:
            state["checks"][key] = {"seconds": check.seconds, "proved_at": check.ended}
            publish()


with lock:
    publish()
gate.execute_check = observe
sys.exit(gate.main(sys.argv[5:]))
