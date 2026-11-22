#!/usr/bin/env python3
"""Build the Go tools and compile their tests concurrently without running cases."""
from concurrent.futures import ThreadPoolExecutor
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from threading import Lock
import time
from change_common import git, tree_entries, tracked_changes, result_store, stored_result, store_result

GO_TOOL_DIRS = (
    "tools/pi-profiler", "tools/tillr", "tools/tillr-store", "tools/tillr-item",
    "tools/tillr-run", "tools/tillr-gate", "tools/tillr-view", "tools/tillr-land",
    "tools/tillr-drain", "tools/tillr-cli", "tools/tillr-watch",
)


def build_env():
    env = dict(os.environ, CGO_ENABLED="0")
    env.pop("LAND_BATCH_LIMIT", None)
    # -trimpath, for this check only. The check builds to prove the tree compiles and
    # throws the output away, so trimmed source paths change nothing that is kept -- and
    # without it Go keys compiled packages by their absolute source path, so every new
    # checkout (a new lane's worktree) compiles cold: 2.85 s alone against 0.84 s trimmed,
    # measured 2026-11-16, and past the 9 s ceiling beside nine other gates. It rides in
    # GOFLAGS, after the caller's own, so both builds use it. It is NOT a global flag:
    # recipes that build binaries people run
    # (`just tillr-build` and the like) keep their full paths. CGO is off above, so no C
    # object embeds a path, and no Go source here embeds one.
    env["GOFLAGS"] = " ".join(filter(None, [os.environ.get("GOFLAGS", ""), "-trimpath"]))
    # Leave GOCACHE and GOMODCACHE exactly as the caller selected them.
    return env


def test_cache_evidence(stdout):
    """Read positive evidence about Go's result cache, not its compilation cache."""
    cache = {}
    for line in stdout.splitlines():
        package_field = None
        if line.startswith("{"):
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if (not isinstance(event, dict) or event.get("Action") != "output" or event.get("Test")
                    or not isinstance(event.get("Package"), str) or not isinstance(event.get("Output"), str)):
                continue
            package_field, line = event["Package"], event["Output"].rstrip("\n")
        match = re.fullmatch(r"(ok|\?)\s+(\S+)\s+(\(cached\)|[\d.]+s|\[no test files\])(?:\s.*)?", line)
        if match:
            status, package, detail = match.groups()
            if package_field is not None and package != package_field:
                continue
            if status == "?" and detail == "[no test files]":
                cache[package] = "not-applicable"
            elif status == "ok" and detail != "[no test files]":
                cache[package] = "hit" if detail == "(cached)" else "miss"
        else:
            covered = re.fullmatch(r"(\S+)\t+coverage: (?:0(?:\.0+)?% of statements|\[no statements\])", line.strip())
            if covered and package_field in (None, covered[1]):
                cache[covered[1]] = "not-applicable"
    return cache


def build_module(module, scratch, evidence=None, publish=None):
    env = build_env()
    with tempfile.TemporaryDirectory(dir=scratch, prefix="gate-go-build-") as output:
        listed = subprocess.run(["go", "list", "-f", '{{if eq .Name "main"}}M{{end}}\t{{.ImportPath}}', "./..."],
                                cwd=module, env=env, text=True, capture_output=True)
        # On failure only newline-terminated records are known to be complete.
        complete = listed.stdout.rpartition("\n")[0] if listed.returncode else listed.stdout
        rows = [line.partition("\t") for line in complete.splitlines()]
        if evidence is not None:
            evidence.update(mode="compile-only", discovery="partial" if listed.returncode else "complete",
                            packages=[{"import_path": package, "test_cache": "not-started"}
                                      for _, _, package in rows if package])
            if publish:
                publish(evidence)
        if listed.returncode:
            return listed.returncode, listed.stdout + listed.stderr
        if any(marker == "M" for marker, _, _ in rows):
            built = subprocess.run(["go", "build", "-o", output, "./..."], cwd=module,
                                   env=env, text=True, capture_output=True)
            if built.returncode:
                return built.returncode, built.stdout + built.stderr
        # -run filters cases, not TestMain: several tools' TestMain builds another
        # binary even when no case runs. -exec compiles/links every test package but
        # replaces execution with true, so this check never runs test setup.
        if evidence is not None:
            for package in evidence["packages"]:
                package["test_cache"] = "unknown"
            if publish:
                publish(evidence)
        compiled = subprocess.run(["go", "test", "-exec", "true", "./..."], cwd=module,
                                  env=env, text=True, capture_output=True)
        if evidence is not None:
            cache = test_cache_evidence(compiled.stdout)
            for package in evidence["packages"]:
                package["test_cache"] = cache.get(package["import_path"], "unknown")
        return compiled.returncode, compiled.stdout + compiled.stderr


def write_evidence(path, modules):
    try:
        with tempfile.NamedTemporaryFile(mode="w", dir=Path(path).parent, encoding="utf-8", delete=False) as snapshot:
            try:
                snapshot.write(json.dumps(modules) + "\n")
                snapshot.close()
                os.replace(snapshot.name, path)
            finally:
                Path(snapshot.name).unlink(missing_ok=True)
    except OSError as error:
        # An observation failure is not a build verdict.
        print(f"go-build: could not record package evidence: {error}", file=sys.stderr)


def module_keys(root, modules):
    """Keys over module trees and local replacements, including embed assets and tests.

    Go's own parser reads replace blocks; no regex guesses the module grammar. A
    workspace includes every inventoried module conservatively. Inputs outside the
    Git tree, symlinks and dirty inputs are explicitly not remembered.
    """
    entries = tree_entries(root)
    code = {p: entries.get(p, ("", "", "absent"))[2] for p in
            ("tools/gate/checks/go-build.py", "tools/gate/checks/change_common.py",
             "tools/gate/checks/roadmap_sections.py")}
    env = build_env()
    asked = subprocess.run(["go", "env", "-json"], env=env, capture_output=True, text=True)
    version = subprocess.run(["go", "version"], env=env, capture_output=True, text=True)
    if asked.returncode or version.returncode:
        raise ValueError("could not read Go's effective environment/version: " + asked.stderr + version.stderr)
    settings = json.loads(asked.stdout)
    for name in ("GOMOD", "GOTMPDIR", "GOCACHE", "GOMODCACHE", "GOPATH", "GOENV", "GOROOT", "GOTOOLDIR", "GOGCCFLAGS"):
        settings.pop(name, None)
    workspace = settings.get("GOWORK")
    outside_workspace = False
    if workspace and workspace != "off":
        try:
            settings["GOWORK"] = Path(workspace).resolve().relative_to(root).as_posix()
        except ValueError:
            outside_workspace = True
    def inputs(module):
        # Auto-discovery starts at the build directory, not the repository root.
        # A nested go.work can introduce inputs outside the declared module trees.
        work = subprocess.run(["go", "env", "GOWORK"], cwd=module, env=env,
                              capture_output=True, text=True)
        if work.returncode or work.stdout.strip() not in ("", "off"):
            return module, "", False
        parsed = subprocess.run(["go", "mod", "edit", "-json"], cwd=module, env=env,
                                capture_output=True, text=True)
        if parsed.returncode:
            return module, "", False  # build_module reports invalid module syntax as a failed check
        replacements = json.loads(parsed.stdout).get("Replace") or []
        roots = [module.resolve()]
        roots.extend((module / replacement["New"]["Path"]).resolve() for replacement in replacements
                     if not replacement["New"].get("Version"))
        if workspace and workspace != "off":
            roots.extend(m.resolve() for m in modules)
        if any(not p.is_relative_to(root) for p in roots):
            return module, "", False
        prefixes = [p.relative_to(root).as_posix() for p in roots]
        selected = {p: e for p, e in entries.items()
                    if any(p == prefix or p.startswith(prefix + "/") for prefix in prefixes)
                    or p in code or p in ("go.work", "go.work.sum")}
        file_flags = any(flag in settings.get("GOFLAGS", "") for flag in
                         ("-overlay", "-modfile", "-toolexec", "-pkgdir", "-asmflags", "-gcflags",
                          "-ldflags", "-gccgoflags", "-compiler", "-C"))
        bound = not outside_workspace and not (workspace and workspace != "off") and not file_flags
        bound = bound and all(e[0] != "120000" for e in selected.values())
        bound = bound and all(prefix + "/go.mod" in entries for prefix in prefixes)
        bound = bound and not tracked_changes(root, set(selected))
        # Untracked source/assets can change a build too. Do not carry their pass.
        status = git(root, "status", "--porcelain", "--untracked-files=all", "--", *prefixes)
        bound = bound and not status
        identity = {"format": "go-build-module-1", "module": module.as_posix(),
                    "inputs": selected, "code": code, "go": version.stdout, "env": settings}
        key = hashlib.sha256(json.dumps(identity, sort_keys=True).encode()).hexdigest()
        return module, key, bound
    with ThreadPoolExecutor(max_workers=len(modules)) as pool:
        return {module: (key, bound) for module, key, bound in pool.map(inputs, modules)}


def main(memory_store=None):
    try:
        scratch = os.environ["YOLO_DURABLE_DIR"]
        modules = []
        for directory in GO_TOOL_DIRS:
            root = Path(directory)
            if not (root / "go.mod").is_file():
                raise ValueError(f"{directory}/go.mod is missing")
            modules.extend(sorted(path.parent for path in root.rglob("go.mod")))
        root = Path.cwd().resolve()
        # Tests may supply standalone modules outside this repository. They still
        # build, but there is no committed tree against which to bind a remembered pass.
        inside = all(module.resolve().is_relative_to(root) for module in modules)
        keys = module_keys(root, modules) if inside else {}
        store = Path(memory_store) if memory_store else result_store(root)
        tree = git(root, "rev-parse", "HEAD^{tree}").strip() if inside else ""
        evidence_path = os.environ.get("TILLR_GO_BUILD_EVIDENCE_OUT")
        snapshots = {}
        evidence_lock = Lock()

        def publish(observed):
            # Workers only mutate their local evidence. Serialize each snapshot and
            # its publication together so a later write cannot lose another module.
            with evidence_lock:
                snapshots[observed["module"]] = copy.deepcopy(observed)
                write_evidence(evidence_path, [snapshots[m.as_posix()] for m in modules
                                               if m.as_posix() in snapshots])

        def judge(module):
            key, bound = keys.get(module, ("", False))
            if bound and stored_result(store, key, "pass") and not stored_result(store, key, "fail"):
                if evidence_path:
                    publish({"module": module.as_posix(), "execution": "reused"})
                return 0, "", True, 0
            if not bound:
                print(f"go-build: {module}: inputs not bound to the committed tree; not remembered", file=sys.stderr)
            started = time.monotonic()
            if evidence_path:
                observed = {"module": module.as_posix(), "execution": "ran"}
                code, output = build_module(module, scratch, observed, publish)
                publish(observed)
            else:
                code, output = build_module(module, scratch)
            return code, output, False, int((time.monotonic() - started) * 1000)
        with ThreadPoolExecutor(max_workers=len(modules)) as pool:
            results = list(pool.map(judge, modules))
        # Publication binds the post-build inputs too. A build that read an edit
        # made during compilation must not certify the original committed sources.
        fresh = module_keys(root, modules) if inside and any(not r[2] for r in results) else keys
        failed = False
        for module, (code, output, remembered, duration) in zip(modules, results):
            key, bound = keys.get(module, ("", False))
            if not remembered and bound and fresh.get(module) == (key, True):
                store_result(store, key, "fail" if code else "pass", duration, tree,
                             "go-build-module-1", check_id="go-build")
            print(f"go-build: {module}: {'remembered pass' if remembered else 'FAILED' if code else 'built and test files compiled'}")
            if code:
                failed = True
                print(output, end="\n" if not output.endswith("\n") else "")
        return 1 if failed else 0
    except (KeyError, OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"go-build: could not judge: {error}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    sys.exit(main())
