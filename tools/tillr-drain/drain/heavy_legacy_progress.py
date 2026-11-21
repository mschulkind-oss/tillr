"""Observe the configured legacy gate without replacing its planner or scheduler."""
import fnmatch
import importlib.util
import json
import os
from pathlib import Path
import subprocess
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
state = {"tip_sha": tip, "checks": {}, "reused": [], "go_failures": {}}


def changed_between(old, new):
    """Paths that differ between two commits, or None when old is not an ancestor of new."""
    try:
        subprocess.run(["git", "merge-base", "--is-ancestor", old, new], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        out = subprocess.run(["git", "diff", "--name-only", "--no-renames", "-z", old, new, "--"],
                             check=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL).stdout
    except (subprocess.CalledProcessError, OSError):
        return None
    return [name for name in out.decode(errors="replace").split("\0") if name]


def watch_touched(watches, changed):
    for watch in watches:
        base = watch.rstrip("/")
        for name in changed:
            if name == base or name.startswith(base + "/") or fnmatch.fnmatch(name, watch):
                return True
    return False


if path.exists():
    saved = json.loads(path.read_text())
    if saved["tip_sha"] == tip:
        state["checks"] = saved["checks"]
    elif saved.get("tip_sha"):
        # A passed check carries to a descendant tip only when nothing it can read changed:
        # no changed file at all, or explicit watches that no changed file touches.
        changed = changed_between(saved["tip_sha"], tip)
        if changed is not None:
            for key, check in saved["checks"].items():
                watches = check.get("watches") or []
                if not changed or (watches and check.get("reusable", True) and not watch_touched(watches, changed)):
                    state["checks"][key] = check
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
    verdict = status(check)
    if check.kind == "go-tool" and verdict == "FAIL":
        # Reports keep only the tail of a failed check. Preserve every shard
        # header before that truncation, including any failure without a case.
        headers = [line for line in check.output.splitlines() if line.startswith("go-test-shards: ")]
        with lock:
            state["go_failures"][key] = headers
            publish()
    if verdict in ("PASS", "ok", "CACHED"):
        with lock:
            state["checks"][key] = {"seconds": check.seconds, "proved_at": check.ended,
                                    "watches": list(getattr(check, "watches", ()) or ()),
                                    "reusable": bool(getattr(check, "reusable", True))}
            publish()


with lock:
    publish()
gate.execute_check = observe
sys.exit(gate.main(sys.argv[5:]))
