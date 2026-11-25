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
from change_common import git, tree_entries, result_store, stored_result, store_result
from classify import classify

GO_TOOL_DIRS = (
    "tools/tillr", "tools/tillr-store", "tools/tillr-item",
    "tools/tillr-run", "tools/tillr-gate", "tools/tillr-view", "tools/tillr-land",
    "tools/tillr-drain", "tools/tillr-cli", "tools/tillr-watch", "tools/tillr-trace",
    "tools/tillr-roll",
)

_GO_ENV_CACHE = {}
_GOMOD_CACHE = {}
_MODULE_DEPS = {}
_CACHE_LOCK = Lock()


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
        # -run filters cases, not TestMain: several tools' TestMain builds another
        # binary even when no case runs. -exec compiles/links every test package but
        # replaces execution with true, so this check never runs test setup.
        # Run `go test -exec true ./...` before `go build` so non-test packages and
        # merged internal-test packages compile concurrently in one `go` graph; linking
        # main packages afterward reuses those GOCACHE entries while still catching a
        # `package main` missing `func main`.
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
        if compiled.returncode:
            return compiled.returncode, compiled.stdout + compiled.stderr
        if any(marker == "M" for marker, _, _ in rows):
            mains = [package for marker, _, package in rows if marker == "M" and package] or ["./..."]
            built = subprocess.run(["go", "build", "-o", output, *mains], cwd=module,
                                   env=env, text=True, capture_output=True)
            if built.returncode:
                return built.returncode, built.stdout + built.stderr
        return 0, compiled.stdout + compiled.stderr


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


def _go_metadata(root, env):
    work_chain = tuple((d / "go.work").is_file() for d in (root, *root.parents))
    cache_key = (str(root), tuple(sorted(env.items())), work_chain)
    with _CACHE_LOCK:
        cached = _GO_ENV_CACHE.get(cache_key)
    if cached is not None:
        return copy.deepcopy(cached[0]), cached[1]
    asked = subprocess.run(["go", "env", "-json"], cwd=root, env=env, capture_output=True, text=True)
    version = subprocess.run(["go", "version"], cwd=root, env=env, capture_output=True, text=True)
    if asked.returncode or version.returncode:
        raise ValueError("could not read Go's effective environment/version: " + asked.stderr + version.stderr)
    settings = json.loads(asked.stdout)
    for name in ("GOMOD", "GOTMPDIR", "GOCACHE", "GOMODCACHE", "GOPATH", "GOENV", "GOROOT", "GOTOOLDIR", "GOGCCFLAGS"):
        settings.pop(name, None)
    with _CACHE_LOCK:
        _GO_ENV_CACHE[cache_key] = (copy.deepcopy(settings), version.stdout)
    return settings, version.stdout


def _local_replaces(module, env):
    try:
        raw = (module / "go.mod").read_bytes()
    except OSError:
        return None
    with _CACHE_LOCK:
        cached = _GOMOD_CACHE.get(raw)
    if cached is not None:
        return cached
    parsed = subprocess.run(["go", "mod", "edit", "-json"], cwd=module, env=env,
                            capture_output=True, text=True)
    if parsed.returncode:
        return None  # build_module reports invalid module syntax as a failed check
    replacements = json.loads(parsed.stdout).get("Replace") or []
    rel_paths = tuple(r["New"]["Path"] for r in replacements if not r["New"].get("Version"))
    with _CACHE_LOCK:
        _GOMOD_CACHE[raw] = rel_paths
    return rel_paths


def _dirty_paths(root, watch_paths):
    if not watch_paths:
        return set()
    status = git(root, "status", "--porcelain", "-z", "--no-renames", "--untracked-files=all",
                 "--", *sorted(watch_paths))
    return {entry[3:] for entry in status.split("\0") if len(entry) > 3}


def dependency_waves(modules):
    """Group modules into topological waves by local replace edges (cycles share a wave)."""
    if len(modules) <= 1:
        return [list(modules)] if modules else []
    by_resolved = {m.resolve(): m for m in modules}
    with _CACHE_LOCK:
        deps = {m: [by_resolved[target] for target in _MODULE_DEPS.get(m.resolve(), ())
                    if target in by_resolved and target != m.resolve()]
                for m in modules}
    index = {}
    lowlink = {}
    on_stack = set()
    stack = []
    sccs = []
    counter = 0

    def strongconnect(node):
        nonlocal counter
        index[node] = lowlink[node] = counter
        counter += 1
        stack.append(node)
        on_stack.add(node)
        for dep in deps[node]:
            if dep not in index:
                strongconnect(dep)
                lowlink[node] = min(lowlink[node], lowlink[dep])
            elif dep in on_stack:
                lowlink[node] = min(lowlink[node], index[dep])
        if lowlink[node] == index[node]:
            comp = []
            while True:
                member = stack.pop()
                on_stack.remove(member)
                comp.append(member)
                if member == node:
                    break
            sccs.append(comp)

    for module in modules:
        if module not in index:
            strongconnect(module)
    scc_of = {member: idx for idx, comp in enumerate(sccs) for member in comp}
    scc_level = {}
    for idx, comp in enumerate(sccs):
        scc_level[idx] = max(
            (scc_level[scc_of[dep]] + 1 for member in comp for dep in deps[member] if scc_of[dep] != idx),
            default=0,
        )
    level_of = {m: scc_level[scc_of[m]] for m in modules}
    max_level = max(level_of.values(), default=0)
    return [[m for m in modules if level_of[m] == level] for level in range(max_level + 1)]


def module_keys(root, modules):
    """Keys over module trees and local replacements, including embed assets and tests.

    Go's own parser reads replace blocks; no regex guesses the module grammar. A
    workspace includes every inventoried module conservatively. Inputs outside the
    Git tree, symlinks and dirty inputs are explicitly not remembered.
    """
    if not modules:
        return {}
    entries = tree_entries(root)
    code_paths = ("tools/gate/checks/go-build.py", "tools/gate/checks/change_common.py",
                  "tools/gate/checks/roadmap_sections.py", "tools/tillr-gate/classify.py")
    code = {p: entries.get(p, ("", "", "absent"))[2] for p in code_paths}
    env = build_env()
    settings, version_stdout = _go_metadata(root, env)
    workspace = settings.get("GOWORK")
    outside_workspace = False
    if workspace and workspace != "off":
        try:
            settings["GOWORK"] = Path(workspace).resolve().relative_to(root).as_posix()
        except ValueError:
            outside_workspace = True
    file_flags = any(flag in settings.get("GOFLAGS", "") for flag in
                     ("-overlay", "-modfile", "-toolexec", "-pkgdir", "-asmflags", "-gcflags",
                      "-ldflags", "-gccgoflags", "-compiler", "-C"))

    def parse_one(module):
        resolved = module.resolve()
        rel_paths = _local_replaces(module, env)
        if rel_paths is None:
            return module, None
        dep_roots = tuple((module / rel).resolve() for rel in rel_paths)
        with _CACHE_LOCK:
            _MODULE_DEPS[resolved] = dep_roots
        # Auto-discovery starts at the build directory, not the repository root.
        # When GOWORK is empty at root, a nested go.work inside root activates workspace mode.
        if not workspace and any((d / "go.work").is_file() for d in (resolved, *resolved.parents)
                                 if d != root and d.is_relative_to(root)):
            return module, None
        roots = [resolved, *dep_roots]
        if workspace and workspace != "off":
            roots.extend(m.resolve() for m in modules)
        if any(not p.is_relative_to(root) for p in roots):
            return module, None
        own_prefix = resolved.relative_to(root).as_posix()
        dep_prefixes = sorted({p.relative_to(root).as_posix() for p in roots[1:]} - {own_prefix})
        return module, (own_prefix, dep_prefixes)

    with ThreadPoolExecutor(max_workers=len(modules)) as pool:
        parsed_modules = dict(pool.map(parse_one, modules))

    watch_paths = set(code_paths) | {"go.work", "go.work.sum"}
    for info in parsed_modules.values():
        if info is not None:
            own_prefix, dep_prefixes = info
            watch_paths.add(own_prefix)
            watch_paths.update(dep_prefixes)
    dirty = _dirty_paths(root, [p for p in watch_paths if p in entries or (root / p).exists()])

    def matches_dirty(path, own_prefix, dep_prefixes):
        if path in code or path in ("go.work", "go.work.sum"):
            return True
        if path == own_prefix or path.startswith(own_prefix + "/"):
            return True
        if workspace and workspace != "off":
            return any(path == prefix or path.startswith(prefix + "/") for prefix in dep_prefixes)
        return any(
            path == prefix or (
                path.startswith(prefix + "/")
                and (not path.endswith("_test.go") or os.path.islink(root / path))
            )
            for prefix in dep_prefixes
        )

    def matches_entry(path, entry, own_prefix, dep_prefixes):
        if path in code or path in ("go.work", "go.work.sum"):
            return True
        if path == own_prefix or path.startswith(own_prefix + "/"):
            return True
        if workspace and workspace != "off":
            return any(path == prefix or path.startswith(prefix + "/") for prefix in dep_prefixes)
        # Exclude directory tree objects (whose git tree sha changes on *_test.go edits)
        # and non-symlink *_test.go blobs in replaced dependencies; keep every non-test
        # blob and every symlink so downstream modules rebuild iff an importable input moved.
        if entry[1] == "tree" or (entry[0] != "120000" and path.endswith("_test.go")):
            return False
        return any(path.startswith(prefix + "/") for prefix in dep_prefixes)

    out = {}
    for module in modules:
        info = parsed_modules[module]
        if info is None:
            out[module] = ("", False)
            continue
        own_prefix, dep_prefixes = info
        all_prefixes = [own_prefix, *dep_prefixes]
        selected = {p: e for p, e in entries.items() if matches_entry(p, e, own_prefix, dep_prefixes)}
        bound = not outside_workspace and not (workspace and workspace != "off") and not file_flags
        bound = bound and all(e[0] != "120000" for e in selected.values())
        bound = bound and all(prefix + "/go.mod" in entries for prefix in all_prefixes)
        bound = bound and not any(matches_dirty(p, own_prefix, dep_prefixes) for p in dirty)
        identity = {"format": "go-build-module-2", "module": module.as_posix(),
                    "inputs": selected, "code": code, "go": version_stdout, "env": settings}
        key = hashlib.sha256(json.dumps(identity, sort_keys=True).encode()).hexdigest()
        out[module] = (key, bound)
    return out


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
                return None, "", True, 0
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
        results_by_module = {}
        with ThreadPoolExecutor(max_workers=max(1, len(modules))) as pool:
            for wave in dependency_waves(modules):
                for module, module_result in zip(wave, pool.map(judge, wave)):
                    results_by_module[module] = module_result
        results = [results_by_module[module] for module in modules]
        observations = [{"check": "go-build", "source": "go-module-build", "exit_code": code}
                        for code, _, remembered, _ in results if not remembered]
        outcomes = [answer["outcome"] for answer in classify(observations)] if observations else []
        # Publication binds the post-build inputs too. A build that read an edit
        # made during compilation must not certify the original committed sources.
        fresh = module_keys(root, modules) if inside and observations else keys
        classified = iter(outcomes)
        unjudged = []
        failed = False
        for module, (_, output, remembered, duration) in zip(modules, results):
            outcome = "pass" if remembered else next(classified)
            if outcome not in ("pass", "fail"):
                unjudged.append(str(module))
                print(output, end="\n" if not output.endswith("\n") else "")
                continue
            key, bound = keys.get(module, ("", False))
            if not remembered and bound and fresh.get(module) == (key, True):
                store_result(store, key, outcome, duration, tree, "go-build-module-2", check_id="go-build")
            print(f"go-build: {module}: {'remembered pass' if remembered else 'FAILED' if outcome == 'fail' else 'built and test files compiled'}")
            if outcome == "fail":
                failed = True
                print(output, end="\n" if not output.endswith("\n") else "")
        if unjudged:
            raise ValueError(f"no Go build verdict for {', '.join(unjudged)}")
        return 1 if failed else 0
    except (KeyError, OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"go-build: could not judge: {error}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    sys.exit(main())
