# =============================================================================
# TILLR BUILD SYSTEM
# =============================================================================
# Recipes for building, testing and operating tillr; see README.md

# Default command - show help
default:
    @just --list

# The standard recipe set. `check` is what to run while working; `check-ci` is what CI
# runs and never rewrites a file. `done` (below) is the full local gate.
check: format lint test

check-ci: lint-ci test

# Fetch every Go module's dependencies and the observatory front end's packages.
setup:
    #!/usr/bin/env bash
    set -euo pipefail
    for mod in tools/*/go.mod; do (cd "$(dirname "$mod")" && go mod download); done
    (cd tools/tillr-view/web && npm ci)

# Vet every Go module.
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    for mod in tools/*/go.mod; do (cd "$(dirname "$mod")" && go vet ./...); done

# The same, after refusing unformatted Go instead of rewriting it.
lint-ci:
    @just format --check
    @just lint

# Run every Go module's tests; arguments go to `go test`. With no arguments the drain
# runs in parallel shards, because its whole suite exceeds `go test`'s ten-minute limit.
[positional-arguments]
test *args:
    #!/usr/bin/env bash
    set -euo pipefail
    for mod in tools/*/go.mod; do
        dir="$(dirname "$mod")"
        if [ "$#" -eq 0 ] && [ "$dir" = "tools/tillr-drain" ]; then
            python3 tools/go-test-shards.py "$dir" -k 6
        else
            (cd "$dir" && go test ./... "$@")
        fi
    done

# Python is excluded: adopting its formatter would rewrite 84 files at once.
# shell, TypeScript, and Lua are excluded because they have no pinned formatter.
# Adding a language means pinning its formatter in mise.toml and landing the reformat
# as its own commit. Only tracked source is ours; vendor trees are not.
# Format Go with gofmt; --check lists changes without writing and exits 1 on drift.
[positional-arguments]
format *args:
    #!/usr/bin/env python3
    import hashlib
    import os
    from pathlib import Path
    import re
    import stat
    import subprocess
    import sys
    import tempfile

    if sys.argv[1:] not in ([], ["--check"]):
        print("usage: just format [--check]", file=sys.stderr)
        sys.exit(2)

    def resolve_gofmt():
        candidates = [
            cand for entry in os.environ.get("PATH", "").split(os.pathsep) if entry
            for cand in (os.path.join(entry, "gofmt"),)
            if os.path.isfile(cand) and os.access(cand, os.X_OK)
        ]
        if not candidates:
            return "gofmt", None
        chosen = candidates[0]
        if os.path.basename(os.path.realpath(chosen)) == "mise" and not os.environ.get("MISE_GO_VERSION"):
            direct = next((c for c in candidates if os.path.basename(os.path.realpath(c)) != "mise"), None)
            if direct:
                pattern = re.compile(r'(?m)^\s*"?(?:go|golang)"?\s*[=\s]')
                configs = []
                cur = os.getcwd()
                while True:
                    for name in ("mise.toml", "mise.local.toml", ".mise.toml", ".mise.local.toml", ".tool-versions"):
                        configs.append(os.path.join(cur, name))
                    parent = os.path.dirname(cur)
                    if parent == cur:
                        break
                    cur = parent
                xdg = os.environ.get("XDG_CONFIG_HOME")
                home = os.environ.get("HOME")
                if xdg and os.path.isabs(xdg):
                    configs.append(os.path.join(xdg, "mise", "config.toml"))
                elif home and os.path.isabs(home):
                    configs.append(os.path.join(home, ".config", "mise", "config.toml"))
                if not any(
                    pattern.search(Path(cfg).read_text(encoding="utf-8", errors="ignore"))
                    for cfg in configs if os.path.isfile(cfg)
                ):
                    chosen = direct
        chosen_real = os.path.realpath(chosen)
        if os.path.basename(chosen_real) == "mise":
            return chosen, None
        try:
            with open(chosen_real, "rb") as handle:
                return chosen, hashlib.file_digest(handle, "sha256").hexdigest()
        except OSError:
            return chosen, None

    def resolve_cache_file(tool_key):
        if not tool_key:
            return None
        cache_root = os.environ.get("JUST_FORMAT_CACHE_DIR")
        if not cache_root:
            xdg = os.environ.get("XDG_CACHE_HOME")
            home = os.environ.get("HOME")
            if xdg and os.path.isabs(xdg):
                cache_root = os.path.join(xdg, "just-format")
            elif home and os.path.isabs(home):
                cache_root = os.path.join(home, ".cache", "just-format")
        return Path(cache_root) / f"{tool_key}.bin" if cache_root else None

    def load_clean(cache_file):
        if cache_file is None:
            return set()
        try:
            raw = cache_file.read_bytes()
        except OSError:
            return set()
        usable = len(raw) - (len(raw) % 32)
        return {raw[i:i + 32] for i in range(0, usable, 32)}

    def save_clean(cache_file, clean_digests, live_clean):
        if cache_file is None:
            return
        merged = set(live_clean)
        if len(merged) < 65536:
            for item in clean_digests:
                merged.add(item)
                if len(merged) >= 65536:
                    break
        payload = b"".join(sorted(merged))
        tmp_path = None
        try:
            cache_file.parent.mkdir(parents=True, exist_ok=True)
            fd, tmp_path = tempfile.mkstemp(dir=cache_file.parent, prefix=".gofmt-")
            with os.fdopen(fd, "wb") as handle:
                handle.write(payload)
            os.replace(tmp_path, cache_file)
            tmp_path = None
        except OSError:
            pass
        finally:
            if tmp_path:
                try:
                    os.unlink(tmp_path)
                except OSError:
                    pass

    listing = subprocess.check_output([
        "git", "ls-files", "-z", "--", "tools/", "yolo-packs/tillr/files/local/share/tillr/",
    ])
    dir_ok = {"": True, ".": not os.path.islink(".")}

    def clean_ancestors(directory):
        ok = dir_ok.get(directory)
        if ok is None:
            ok = clean_ancestors(os.path.dirname(directory)) and not os.path.islink(directory)
            dir_ok[directory] = ok
        return ok

    files = []
    groups = {}
    file_digest = {}
    for entry in listing.split(b"\0"):
        if not entry or not entry.endswith(b".go"):
            continue
        path = os.fsdecode(entry)
        if os.path.basename(path) == ".go":
            continue
        parts = path.split("/")
        if "vendor" in parts or ".forks" in parts or not clean_ancestors(os.path.dirname(path)):
            continue
        try:
            st = os.lstat(path)
        except OSError:
            continue
        if not stat.S_ISREG(st.st_mode):
            continue
        with open(path, "rb") as handle:
            digest = hashlib.sha256(handle.read()).digest()
        groups.setdefault(digest, []).append(path)
        files.append(path)
        file_digest[path] = digest

    if files:
        gofmt, tool_key = resolve_gofmt()
        cache_file = resolve_cache_file(tool_key)
        clean = load_clean(cache_file)
        env = {**os.environ, "GOGC": "off"}
        env.setdefault("GOMEMLIMIT", "512MiB")
        env.setdefault("GOMAXPROCS", str(min(os.cpu_count() or 4, 12)))
        pending = [bucket[0] for digest, bucket in groups.items() if digest not in clean]
        rep_digest = {bucket[0]: digest for digest, bucket in groups.items()}
        dirty_digests = set()
        if pending:
            result = subprocess.run([gofmt, "-l", *pending], env=env, capture_output=True)
            flagged = {os.fsdecode(line) for line in result.stdout.splitlines() if line}
            dirty_digests = {rep_digest[rep] for rep in flagged if rep in rep_digest}
            if result.returncode == 0 and not result.stderr:
                newly_clean = {rep_digest[rep] for rep in pending if rep not in flagged}
                if newly_clean:
                    clean |= newly_clean
                    save_clean(cache_file, clean, groups.keys() & clean)
        else:
            result = subprocess.CompletedProcess([gofmt, "-l"], 0, b"", b"")
        dirty = [path for path in files if file_digest[path] in dirty_digests]

        def write_stderr():
            if not result.stderr:
                return
            sys.stderr.buffer.write(result.stderr)
            dups = [
                dup for bucket in groups.values() if len(bucket) > 1
                if os.fsencode(bucket[0]) + b":" in result.stderr
                for dup in bucket[1:]
            ]
            if dups:
                extra = subprocess.run([gofmt, "-l", *dups], env=env, capture_output=True)
                sys.stderr.buffer.write(extra.stderr)

        if sys.argv[1:]:
            if dirty:
                sys.stdout.buffer.write(b"".join(os.fsencode(f"{path}\n") for path in dirty))
            write_stderr()
            sys.exit(result.returncode or int(bool(dirty)))
        if result.returncode:
            write_stderr()
            sys.exit(result.returncode)
        if dirty:
            subprocess.run([gofmt, "-w", *dirty], env=env, check=True)
            formatted_clean = set()
            for path in dirty:
                with open(path, "rb") as handle:
                    formatted_clean.add(hashlib.sha256(handle.read()).digest())
            clean |= formatted_clean
            save_clean(cache_file, clean, (groups.keys() - dirty_digests) | formatted_clean)

# Install this repository's git hooks (hooks/*) into the shared hooks directory, so they
# run from the main checkout and every linked worktree.
# A copy git already runs is replaced when it carries this repository's marker, and a
# hook without the marker is left alone -- kept as <name>.local, which our hook runs
# first -- so every run reports installed, replaced, left alone or drifted (see
# hooks/README.md and bin/hooks-install).
hooks-install *args:
    @bin/hooks-install {{args}}

# Mirror the tillr pack's copy of every `tools/tillr*` module into
# yolo-packs/tillr/files/local/share/tillr/<module>. The copy is committed because the
# pack -- not this checkout -- is what puts `tillr` on PATH in a workspace that has no
# tools/. `tests/pack-sync.test.py` fails when the copy diverges from its source.
# With module arguments, render only those modules. The drain embeds this same
# renderer so it can work on candidates with earlier versions of this recipe.
[positional-arguments]
pack-sync *modules:
    #!/usr/bin/env bash
    set -euo pipefail
    root="$(cd "$(dirname "{{justfile()}}")" && pwd)"
    exec python3 "$root/tools/pack-mirror-links.py" --sync "$root" "$@"

# Build the eight tillr programs from refs/tillr/green (the last trunk commit proved by
# the heavy runner), never from the newer tip or working tree. The pack's rendered copy
# is a separate owner-run deployment and is not changed here.
#
# The output directory must be prepended to PATH for the loop AND its children, not just used by
# name: `tillr` resolves `tillr-<noun>` through PATH and `ProcessSteps` resolves `tillr-run` and
# `tillr-item` through PATH, so a directory holding only `tillr` hands the loop the launched
# siblings. Build all eight together and run:
#
#   PATH="$YOLO_DURABLE_DIR/bin:$PATH" tillr drain loop ...
#
# Run it against a SCRATCH store, never the launched binaries' store: a newer binary migrates a
# store, and the launched binaries then refuse it. docs/reference/tillr-distribution.md,
# "Running a tree the pack has not rendered", states the rule.
#
# Bootstrap, the one exception: when the deployed loop itself cannot prove the trunk (it
# crashes, or predates the fix the heavy run needs), the pointer can never reach the fix
# that would move it. An operator then names the commit explicitly:
#
#   TILLR_BUILD_UNPROVED=<commit> just tillr-build
#
# That build says UNPROVED on every run, never moves or reads the pointer as its proof, and
# stamps no green commit, so the loop's `loop: build` line reads `green unknown` until the
# next ordinary build from refs/tillr/green replaces it.
tillr-build:
    #!/usr/bin/env bash
    set -euo pipefail
    proof="-X github.com/mschulkind-oss/tillr-drain/drain.greenRevision="
    if [ -n "${TILLR_BUILD_UNPROVED:-}" ]; then
        if ! green=$(git rev-parse --verify --quiet "${TILLR_BUILD_UNPROVED}^{commit}"); then
            printf 'tillr-build: TILLR_BUILD_UNPROVED=%s names no commit\n' "$TILLR_BUILD_UNPROVED" >&2
            exit 1
        fi
        proved=$(git rev-parse --verify --quiet 'refs/tillr/green^{commit}' || printf 'absent')
        printf 'tillr-build: UNPROVED build of %s (TILLR_BUILD_UNPROVED); refs/tillr/green is %s and stays unchanged\n' "$green" "$proved" >&2
        proof=""
    elif ! green=$(git rev-parse --verify 'refs/tillr/green^{commit}'); then
        printf 'tillr-build: no valid refs/tillr/green; the heavy run at `tillr drain loop` startup (with after-merge checks enabled) creates it after proving the trunk; TILLR_BUILD_UNPROVED=<commit> is the documented bootstrap\n' >&2
        exit 1
    else
        proof="-X github.com/mschulkind-oss/tillr-drain/drain.greenRevision=$green -X github.com/mschulkind-oss/tillr/buildstamp.greenRevision=$green"
    fi
    durable="${YOLO_DURABLE_DIR:?tillr-build needs YOLO_DURABLE_DIR for build output and scratch}"
    mkdir -p "$durable"
    durable=$(cd "$durable" && pwd)
    out="$durable/bin"
    work=$(mktemp -d "$durable/tillr-build-XXXXXXXX")
    trap 'rm -rf "$work"' EXIT
    # This independent, sparse clone borrows read-only objects, not the source's refs
    # or index. Its detached HEAD pins one green commit even if the proof ref moves.
    # Keeping Git metadata preserves native build stamps in older source trees and
    # in every sibling program; the view uses that revision to reject stale planners.
    git clone --shared --no-checkout --quiet . "$work/source"
    git -C "$work/source" sparse-checkout set --cone tools
    git -C "$work/source" checkout --detach --quiet "$green"
    mkdir -p "$work/bin"
    if [ -n "$proof" ]; then
        printf 'tillr-build: green %s\n' "$green"
    else
        printf 'tillr-build: UNPROVED %s\n' "$green"
    fi
    # `tillr-cli` builds the dispatcher named `tillr`; the shared library module of that name is
    # not a program, so it is built through its dependents rather than emitted here.
    for module in tillr-cli tillr-drain tillr-gate tillr-item tillr-land tillr-run tillr-store tillr-trace tillr-view; do
        [ -f "$work/source/tools/$module/go.mod" ] || continue
        name="${module}"
        [ "$module" = "tillr-cli" ] && name="tillr"
        tags=""
        if [ "$module" = "tillr-view" ]; then
            # A view built without the tag serves the API and answers 404 for the UI with
            # "this binary carries no built front end". Embed the front end when there is one
            # to embed, and say which line to run when there is not, so the message arrives
            # here rather than from a browser a day later.
            if [ -f "$work/source/tools/tillr-view/web/dist/index.html" ]; then
                tags="-tags observatory"
            else
                printf 'tillr-build: %s carries no front end in green %s; pass --web-dist to its serve face for external assets\n' "$name" "$green"
            fi
        fi
        ( cd "$work/source/tools/$module" && CGO_ENABLED=0 go build -buildvcs=true $tags -ldflags "$proof" -o "$work/bin/$name" . )
    done
    # Keep the existing install untouched until all eight builds succeed.
    mkdir -p "$out"
    for program in "$work/bin/"*; do
        python3 -c 'import os, sys; os.replace(*sys.argv[1:])' "$program" "$out/${program##*/}"
        printf 'tillr-build: %s/%s\n' "$out" "${program##*/}"
    done
    printf 'tillr-build: prepend %s to PATH for the loop and its children: PATH=%s:$PATH\n' "$out" "$out"

# Test, build and smoke the committed checkout before updating the live bin directory.
[positional-arguments]
tillr-roll *args:
    @python3 tools/tillr-roll/tillr-roll.py "$@"

# Keep this checkout's loop alive; --stop stops only the keeper. Pass loop options after --.
# Positional arguments keep each one intact, including an empty `--rolling ''`.
[positional-arguments]
tillr-keep *args:
    @tillr-drain keep "$@"

# Stand the keeper down, stop the loop, wait for exit, build from HEAD, and restart the keeper
# with the running loop's (or recorded launch's) policy and lane count. Safe when no loop runs.
[positional-arguments]
tillr-restart *args:
    @tillr-drain keep --restart "$@"

# Check the one generated pack copy: the tillr pack's tree against tools/tillr*,
# module by module, byte for byte, with the README rewrite, the exec bits, and the
# launchers' shared body.
pack-sync-test:
    @python3 tests/pack-sync.test.py

# Refresh the tillr pack's dated snapshot of tillr's design documentation: the status
# index docs/tillr/design-status.md and every document its tables mark `shipped`, copied
# from HEAD into yolo-packs/tillr/files/local/share/tillr/docs/ (tools/pack-docs.py), then
# judged current against HEAD. Commit the result. Deliberately NOT part of `pack-sync`:
# lanes run pack-sync, the docs tree is outside a lane's fence, and the drain edits design
# documents on every landing, so the snapshot is refreshed by hand, never per landing.
pack-docs:
    @python3 tools/pack-docs.py sync
    @python3 tests/pack-sync.test.py --fresh

# Serve the drain observatory for THIS checkout on http://127.0.0.1:<port> (`just observatory
# 5000`; the default is 4318), from one process with the front end embedded. It builds
# tillr-view and the tillr-drain it asks for Waiting from this tree (static, so a host and a
# container can run each other's builds) into one directory per checkout, passes the store,
# root and planner as absolute paths, and runs with any working directory, mount path or
# PATH that holds the toolchain. `--store`, `--root`, `--addr` and the rest pass through to
# `tillr-view serve` and beat the defaults. No Node is needed once the front end is built.
# A port held by this same build serving this store answers with its URL; one held by an
# older build or anything else is refused with the command that frees it. In a yolo jail the
# recipe binds 0.0.0.0 only for a port published in yolo-jail.toml, and otherwise prints the
# line to add. tools/tillr-view/README.md ("Starting the observatory") states every refusal.
[doc("Serve this checkout's observatory, built from this checkout, on 127.0.0.1:<port> (default 4318)")]
[no-exit-message]
observatory port="4318" *args:
    @exec bash "{{justfile_directory()}}/tools/observatory/run.sh" serve "{{port}}" {{args}}

# Summarize the observatory's performance log: per-route and per-page p50/p95/max, the slowest
# requests with their phase breakdown, the browser-versus-server gap, the live channel's
# traffic and each start's phases. `just observatory` and `observatory-dev` write that log
# under this checkout's durable scratch directory (tools/tillr-view/README.md, "The
# performance log"), which the host and its jails share, so this reads what the HOST's server recorded
# too. Arguments pass through to `tillr-view perf-log` (`--since 1h`, `--top 20`, `--json`, or
# a log file or directory instead of every server's).
[doc("Summarize the observatory's performance log (what the host's or this jail's server recorded)")]
[no-exit-message]
observatory-perf *args:
    @cd "{{justfile_directory()}}/tools/tillr-view" && go run . perf-log --logs "{{justfile_directory()}}/.yolo/durable/tillr-observatory/logs" --from "{{invocation_directory()}}" {{args}}

# Develop against the observatory with hot reload. The port you name is the one you OPEN --
# Vite serves the UI there and proxies `/api` to a Go server one port above it (so two dev
# runs do not collide); a caller-named `--addr` moves the API and the proxy follows it.
# Live reload is for code, not data (design Section 5.3). The same guarantees as
# `observatory`: both programs built from this tree, every path absolute, nothing found on
# PATH but the toolchain. No `npm run build` and no `observatory` tag: Vite serves the UI
# from source, so embedded assets would be a stale snapshot of it.
#
# WHAT RELOADS: the UI through Vite, and the API through `air` (pinned in `mise.toml`, configured
# in `tools/tillr-view/.air.toml`): a change to a Go source tillr-view is built from, including
# commits landing on this checkout and the local modules it replaces, is rebuilt once per burst
# and replaces the API on its port. A failed build prints its error here and leaves the old API
# answering.
#
# ONE SUPERVISED TREE, NOT TWO BACKGROUND JOBS. The processes are declared in
# `tools/tillr-view/Procfile.dev` and run under hivemind (pinned in `mise.toml`): a child that
# dies takes the other with it, and SIGINT or SIGTERM tears down both process groups. A port
# that is already answering is refused before any build, and Vite runs with `--strictPort`,
# because the last way to serve the wrong board is to serve an old process's.
[doc("Hot-reload observatory for this checkout: Vite on <port>, the API on <port>+1")]
[no-exit-message]
observatory-dev port="4318" *args:
    @exec bash "{{justfile_directory()}}/tools/observatory/run.sh" dev "{{port}}" {{args}}

# End of task: judge the committed tree with every declared check
# (tools/gate/checks/, one JSON file each), using tillr-gate built from this checkout.
# First the change checks and the fast whole-tree checks over BASE..HEAD, then the
# heavy whole-tree checks (the Go suites and the script suites) on HEAD. BASE is
# refs/tillr/green, else the upstream branch, else HEAD's first parent. Commit first:
# the checks judge HEAD. --since <base> names the range; --fast runs the first pass
# only; --only <id> runs one heavy check. It writes no verdict record and moves no ref.
done *args:
    @python3 tools/gate/done.py {{args}}

# Whole-module execution; prefer targeted `go test -run` while editing.
go-test-shards module="tools/tillr-drain" *args:
    @python3 tools/go-test-shards.py {{module}} {{args}}
