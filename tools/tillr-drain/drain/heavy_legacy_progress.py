"""Observe the configured legacy gate without replacing its planner or scheduler."""
import fnmatch
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile
import threading
import time

source, destination, tip, retry_proof, logs_dir = sys.argv[1:6]
path, logs = Path(destination), Path(logs_dir)
spec = importlib.util.spec_from_file_location("tillr_heavy_legacy_gate", source)
gate = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = gate
spec.loader.exec_module(gate)
# These are required observation surfaces, not permission for a silent uncached run.
original = gate.execute_check
identity = gate.declaration_id
status = gate.check_status
state = {"tip_sha": tip, "checks": {}, "reused": [], "failures": {}}


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


def keep_output(output):
    if not output.strip():
        return ""
    logs.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", prefix="observed-",
                                     suffix=".log", dir=logs, delete=False) as log:
        log.write(output)
        return log.name


def execute_with_events(check, root, diff_base, *args, **kwargs):
    # Instrument only the known Go suite commands, leaving the legacy gate in
    # charge of selection, execution and status. Older gates without this command
    # surface still contribute the observed whole check output.
    module = check.label.removesuffix("'s whole suite passes")
    command = getattr(gate, "go_tool_command", None)
    if (check.kind != "go-tool" or module == check.label or module not in getattr(gate, "GO_TOOL_DIRS", ())
            or command is None or getattr(check, "argv", None) != ["sh", "-c", command(module)]):
        original(check, root, diff_base, *args, **kwargs)
        return None
    argv = check.argv
    logs.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=logs, prefix=".heavy-events-") as temporary:
        events = Path(temporary) / "go-events.jsonl"
        sharded = module in gate.GO_SHARD_DIRS
        if sharded:
            script = argv[2] + " --events " + shlex.quote(str(events.resolve()))
        else:
            script = argv[2].replace(" go test ", " go test -json ", 1)
        check.argv = argv[:2] + [script]
        try:
            original(check, root, diff_base, *args, **kwargs)
        finally:
            check.argv = argv
        if status(check) != "FAIL":
            return None
        if sharded:
            # The captured verdict can still name a timeout and carry its stacks
            # when the runner could not write its optional events file.
            lines = events.read_text(encoding="utf-8").splitlines() if events.exists() else []
        else:
            lines = check.output.splitlines()
        return go_failure_logs(module, lines, check.output, sharded)


def record_timeouts(timeouts, package, output):
    # The testing package names running tests before the stacks; a hung test
    # need not emit a case-level fail event at all.
    panic = re.search(
        r"^(panic: test timed out after \S+)\nrunning tests:\n((?:\t[^\n]+\n)+)",
        output, re.MULTILINE,
    )
    if not panic:
        return False
    names = re.findall(r"^\t((?:Test|Example|Fuzz)\S*) \(\S+\)$", panic[2], re.MULTILINE)
    for name in names:
        timeouts[package, name] = (panic[1], output[panic.start():])
    return bool(names)


def go_failure_logs(module, lines, output, sharded):
    messages, package_output = {}, {}
    failed, package_failures = set(), set()
    for line in lines:
        try:
            event = json.loads(line)
        except ValueError:
            continue  # Native Go may also report non-JSON build diagnostics.
        action, package, name = event.get("Action"), event.get("Package"), event.get("Test")
        if not sharded and package and action == "output":
            package_output.setdefault(package, []).append(event["Output"])
        if package and name:
            key = (package, name)
            if action == "output":
                messages.setdefault(key, []).append(event["Output"])
            elif action == "fail":
                failed.add(key)
        elif action in ("fail", "build-fail"):
            package_failures.add(package)

    # A failing subtest also marks each enclosing test failed. Name the leaf,
    # and only keep output attributed by test2json to that same package and test.
    parents = set()
    for package, name in failed:
        while "/" in name:
            name = name.rsplit("/", 1)[0]
            parents.add((package, name))
    timeouts = {}
    unscoped = False
    if sharded:
        # Classify each shard separately so a timeout does not hide a crash in
        # another shard (even one in the same package). Ordinary case names and
        # assertion output still come exclusively from Go events. Strip slot
        # notices from the retained evidence too, not just the classification.
        output = re.sub(
            r"^go-test-shards: \S+ shard \d+ waiting for a test slot[^\n]*; "
            r"not a failure; no timeout applies while waiting\)(?:\n|$)",
            "", output, flags=re.MULTILINE,
        )
        shards = re.split(r"^go-test-shards: ", output, flags=re.MULTILINE)[1:]
        for shard in shards:
            header, _, body = shard.partition("\n")
            match = re.fullmatch(r"(\S+) shard \d+ failed \(exit (\d+)\)(?:: (.+))?", header)
            if match and record_timeouts(timeouts, match[1], body):
                continue
            if not (lines and match and match[2] == "1" and match[3]):
                unscoped = True
    else:
        for package in package_failures:
            record_timeouts(timeouts, package, "".join(package_output.get(package, ())))

    cases = []
    named = (failed - parents) | timeouts.keys()
    for package, name in sorted(named):
        panic, text = timeouts.get((package, name), ("", ""))
        text = text or "".join(messages.get((package, name), ()))
        # In a shard the runtime can detect a deadlock and end the test
        # before the testing package's timeout fires. A focused run may hang
        # instead. Only use the exact runtime line in this case's own output.
        deadlock = "fatal error: all goroutines are asleep - deadlock!"
        if not panic and deadlock in text.splitlines():
            panic = deadlock
        body = f"go-test:{module}:{package}:{name}\n" + text
        # Keep the existing progress-file field for both kinds of hang.
        cases.append({"package": package, "name": name, "log": keep_output(body), "timeout_panic": panic})
    unscoped |= bool(package_failures - {package for package, _ in named})
    return {"cases": cases, "log": keep_output(output) if unscoped or not cases else ""}


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
    failure = execute_with_events(check, root, diff_base, *args, **kwargs)
    verdict = status(check)
    if verdict == "FAIL":
        if failure is None:
            failure = {"log": keep_output(check.output)}
        with lock:
            state["failures"][key] = failure
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
sys.exit(gate.main(sys.argv[6:]))
