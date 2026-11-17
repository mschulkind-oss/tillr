#!/usr/bin/env python3
"""`just observatory` and `just observatory-dev` serve THIS checkout's board, and cannot leave
a process behind.

Part one (the hermetic cases, 2026-11-16) runs both recipes from a copy of the recipe files at
a fresh absolute path, from another working directory, with PATH holding only a few coreutils
and stub toolchain programs. It asserts what "this checkout" means: the server is handed the
store, root and planner as absolute paths under the root it was started from; both programs
are built static, into a directory of that checkout's own; a missing toolchain program or a
front end that cannot be built is refused in one sentence naming the fix, before any build;
node_modules installed for another platform is reinstalled; and a yolo jail binds the
container side only for a port the workspace publishes, and otherwise prints the line to add.

Part two, `just observatory-dev`'s supervision:

The recipe runs two processes: the Go API and Vite. Before 2026-11-15 it backgrounded the
API and then `exec`'d Vite, which discarded the `EXIT` trap that was supposed to kill the
API — so an interrupted run could leave the API holding its port and answering the next
run's browser with the startup verdict it had cached (measured: a restarted
`just observatory-dev 8250` printed `bind: address already in use` while Vite proxied to
the previous process). The fix hands both to hivemind, declared in
`tools/tillr-view/Procfile.dev`.

`go` and `npm` are stubbed on PATH, so this needs no toolchain, no node_modules and no
network: the stubs record their pids and sleep. `just` and `hivemind` are real, which is
the point — the supervision is the behaviour under test.

Asserts:
  1. both processes start, from the ports the recipe prints;
  2. SIGINT to the recipe's process group (a terminal's Ctrl-C) leaves neither alive;
  3. one process dying takes the other with it, in both directions: a dead API must not
     leave Vite proxying to nothing, and a dead UI must not leave the API holding the port
     (that second door is the one the old recipe left open);
  4. an API port that is already answering is refused before any build, with the port named
     and nothing started;
  5. gate cancellation (SIGKILL of the suite's group) also cleans up the detached recipe
     and its stubs, using a pipe watcher that survives the suite rather than a finally block.

Run: python3 tests/observatory-dev.test.py
"""

import os
import signal
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from shutil import which

REPO = Path(__file__).resolve().parent.parent
RECIPE = "observatory-dev"
RECIPE_FILES = (".justfile", "tools/observatory/run.sh", "tools/tillr-view/Procfile.dev")

failures: list[str] = []


def check(name: str, ok: bool, detail: str = "") -> None:
    if ok:
        print(f"ok   {name}")
    else:
        print(f"FAIL {name}" + (f": {detail}" if detail else ""), file=sys.stderr)
        failures.append(name)


# --- part one: hermetic roots -----------------------------------------------------------------

def real_tool(name: str) -> str:
    """A coreutils program by its real path, not a jail's guard wrapper of the same name."""
    for directory in ("/usr/bin", "/bin"):
        candidate = Path(directory, name)
        if candidate.exists():
            return str(candidate)
    found = which(name)
    if not found:
        raise RuntimeError(f"{name} is required to build the minimal PATH")
    return found


BASE_TOOLS = ("bash", "sh", "env", "mkdir", "mv", "rm", "sha256sum", "dirname", "sleep")


def make_root(base: Path, name: str, *, dist: bool = True, modules: str | None = "linux-x64-gnu") -> Path:
    """A checkout with only the recipe files and the web tree's shape, at its own absolute path."""
    root = base / name
    for rel in RECIPE_FILES:
        dest = root / rel
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_bytes((REPO / rel).read_bytes())
        dest.chmod((REPO / rel).stat().st_mode)
    (root / "tools/tillr-drain").mkdir(parents=True)
    web = root / "tools/tillr-view/web"
    (web / "src").mkdir(parents=True)
    for rel in ("index.html", "package.json", "package-lock.json", "vite.config.ts", "src/main.tsx"):
        (web / rel).write_text("x\n")
    if modules is not None:
        (web / "node_modules").mkdir()
        (web / "node_modules/.package-lock.json").write_text("{}\n")
        (web / "node_modules/.tillr-platform").write_text(modules + "\n")
    if dist:
        (web / "dist").mkdir()
        (web / "dist/index.html").write_text("<html></html>\n")
        # Newer than every source, without sleeping: stamp the sources into the past.
        old = time.time() - 3600
        for f in [*web.glob("*.*"), *web.glob("src/*"), web / "node_modules/.package-lock.json"]:
            if f.is_file():
                os.utime(f, (old, old))
    return root


def make_path(base: Path, state: Path, *, go: bool = True, node: bool = True, git: bool = True,
              hivemind: bool = False, platform: str = "linux-x64-gnu", npm_fails: bool = False) -> Path:
    """A PATH of a few real coreutils and stub toolchain programs, each recording its call."""
    bindir = base / "bin"
    bindir.mkdir(parents=True)
    for tool in BASE_TOOLS:
        (bindir / tool).symlink_to(real_tool(tool))
    if hivemind:
        (bindir / "hivemind").symlink_to(which("hivemind"))
    stubs = {}
    # The stubs name their own helpers by absolute path: the PATH under test is the recipe's.
    cat, chmod, basename = real_tool("cat"), real_tool("chmod"), real_tool("basename")
    if go:
        # `go build [-tags t] -o X .` writes a program to X that records how it was run. The
        # served program exits at once (serve) or waits for the UI to start (dev).
        stubs["go"] = f"""#!/bin/sh
echo "CGO_ENABLED=$CGO_ENABLED pwd=$(pwd) $*" >> {state}/go.calls
out=""
while [ $# -gt 0 ]; do
    case "$1" in -o) out="$2"; shift 2 ;; *) shift ;; esac
done
{cat} > "$out" <<'PROG'
#!/bin/sh
printf '%s\\n' "$0" "$@" > {state}/"$({basename} "$0")".argv
n=0
while [ -n "$OBSERVATORY_TEST_WAIT_UI" ] && [ ! -e {state}/ui.argv ] && [ $n -lt 300 ]; do
    sleep 0.1; n=$((n + 1))
done
exit 0
PROG
{chmod} +x "$out"
"""
    if git:
        stubs["git"] = "#!/bin/sh\nexit 0\n"
    if node:
        stubs["node"] = f"#!/bin/sh\necho {platform}\n"
        fail = "exit 1" if npm_fails else ":"
        stubs["npm"] = f"""#!/bin/sh
echo "$*" >> {state}/npm.calls
case " $* " in
    *" ci "*) echo "npm error stub" >&2; {fail} ;;
    *" run dev "*) printf '%s\\n' "$@" > {state}/ui.argv; while :; do sleep 0.2; done ;;
esac
exit 0
"""
    for name, body in stubs.items():
        path = bindir / name
        path.write_text(body)
        path.chmod(0o755)
    return bindir


def hermetic_run(root: Path, recipe: str, port: int, bindir: Path, durable: Path,
                 cwd: Path, extra_env: dict[str, str] | None = None,
                 timeout: float = 60) -> subprocess.CompletedProcess:
    env = {"PATH": str(bindir), "HOME": str(cwd), "YOLO_DURABLE_DIR": str(durable)}
    env.update(extra_env or {})
    return subprocess.run([which("just"), "--justfile", str(root / ".justfile"), recipe, str(port)],
                          cwd=cwd, env=env, stdin=subprocess.DEVNULL, capture_output=True,
                          text=True, timeout=timeout)


def argv_of(state: Path, program: str) -> list[str]:
    path = state / f"{program}.argv"
    return path.read_text().splitlines() if path.exists() else []


def flag(argv: list[str], name: str) -> str | None:
    for index, arg in enumerate(argv):
        if arg == name and index + 1 < len(argv):
            return argv[index + 1]
    return None


def case_serve_names_this_checkout_from_anywhere(base: Path) -> None:
    """Two checkouts at two absolute paths, started from a third directory, minimal PATH, no Node."""
    outs = []
    for name in ("home/matt/code/tillr", "workspace"):
        state = base / ("state-" + name.replace("/", "-"))
        state.mkdir(parents=True)
        root = make_root(base, name)
        bindir = make_path(base / ("path-" + name.replace("/", "-")), state, node=False)
        durable = base / "durable"
        elsewhere = base / "elsewhere"
        elsewhere.mkdir(exist_ok=True)
        port = free_port_pair()
        done = hermetic_run(root, "observatory", port, bindir, durable, elsewhere)
        argv = argv_of(state, "tillr-view")
        check(f"[{name}] serve runs without Node when the front end is built",
              done.returncode == 0 and bool(argv), f"exit={done.returncode}\n{done.stderr}")
        if not argv:
            continue
        real = root.resolve()
        out = Path(argv[0]).parent
        outs.append(out)
        check(f"[{name}] the server is this checkout's own build",
              out.parent == durable / "tillr-observatory" and (out / "checkout").read_text().strip() == str(real),
              argv[0])
        check(f"[{name}] the store and root are absolute and this checkout's",
              flag(argv, "--store") == f"{real}/.tillr/state.db" and flag(argv, "--root") == str(real),
              str(argv))
        check(f"[{name}] the planner is the one built beside the server",
              flag(argv, "--tillr-drain") == str(out / "tillr-drain") and (out / "tillr-drain").exists(),
              str(argv))
        check(f"[{name}] outside a jail the server binds loopback",
              flag(argv, "--addr") == f"127.0.0.1:{port}", str(argv))
        calls = (state / "go.calls").read_text().splitlines()
        check(f"[{name}] both programs are built static, the view with its front end",
              len(calls) == 2 and all(c.startswith("CGO_ENABLED=0 ") for c in calls)
              and "-tags observatory" in calls[0] and calls[0].endswith(".")
              and f"pwd={real}/tools/tillr-view " in calls[0]
              and f"pwd={real}/tools/tillr-drain " in calls[1], "\n".join(calls))
        check(f"[{name}] npm is never asked for", not (state / "npm.calls").exists())
    check("two checkouts never share a build directory", len(outs) == 2 and outs[0] != outs[1], str(outs))


def case_missing_toolchain_is_refused_before_any_build(base: Path) -> None:
    for missing, needle in (("go", "go is not on PATH"), ("git", "git is not on PATH")):
        state = base / f"state-{missing}"
        state.mkdir()
        root = make_root(base, f"root-{missing}")
        bindir = make_path(base / f"path-{missing}", state, go=missing != "go", git=missing != "git")
        done = hermetic_run(root, "observatory", free_port_pair(), bindir, base / "durable", base)
        lines = [line for line in done.stderr.splitlines() if line.strip()]
        check(f"no {missing}: refused in one sentence naming it",
              done.returncode != 0 and len(lines) == 1 and needle in lines[0], done.stderr)
        check(f"no {missing}: nothing is built", not (state / "go.calls").exists())


def case_a_stale_front_end_without_node_is_refused(base: Path) -> None:
    state = base / "state-stale"
    state.mkdir()
    root = make_root(base, "root-stale", dist=False)
    bindir = make_path(base / "path-stale", state, node=False)
    done = hermetic_run(root, "observatory", free_port_pair(), bindir, base / "durable", base)
    check("no Node and no built front end: refused naming Node",
          done.returncode != 0 and "node is not on PATH" in done.stderr and "install Node" in done.stderr,
          done.stderr)
    check("no Node and no built front end: nothing is built", not (state / "go.calls").exists())


def case_modules_from_another_platform_are_reinstalled(base: Path) -> None:
    root = make_root(base, "root-platform", modules="linux-arm64-musl")
    stamp = root / "tools/tillr-view/web/node_modules/.tillr-platform"
    for attempt, want_ci in ((1, True), (2, False)):
        state = base / f"state-platform-{attempt}"
        state.mkdir()
        bindir = make_path(base / f"path-platform-{attempt}", state, platform="linux-x64-gnu")
        done = hermetic_run(root, "observatory", free_port_pair(), bindir, base / "durable", base)
        calls = (state / "npm.calls").read_text().splitlines() if (state / "npm.calls").exists() else []
        ran_ci = any(c.split()[2:3] == ["ci"] for c in calls)
        check(f"run {attempt}: npm ci {'runs' if want_ci else 'does not run'} for "
              f"{'another' if want_ci else 'this'} platform's modules",
              done.returncode == 0 and ran_ci == want_ci, f"{calls}\n{done.stderr}")
    check("the modules are stamped with the platform that installed them",
          stamp.read_text().strip() == "linux-x64-gnu", stamp.read_text())


def case_a_failed_install_is_refused_before_any_build(base: Path) -> None:
    state = base / "state-npm"
    state.mkdir()
    root = make_root(base, "root-npm", modules=None)
    bindir = make_path(base / "path-npm", state, npm_fails=True)
    done = hermetic_run(root, "observatory", free_port_pair(), bindir, base / "durable", base)
    check("a failed npm ci is refused naming it", done.returncode != 0
          and "npm ci in " in done.stderr and "NPM_CONFIG_CACHE" in done.stderr, done.stderr)
    check("a failed npm ci builds nothing", not (state / "go.calls").exists())


def case_a_jail_binds_only_a_published_port(base: Path) -> None:
    port = free_port_pair()
    root = make_root(base, "root-jail")
    for published in (False, True):
        if published:
            (root / "yolo-jail.toml").write_text(
                f'mounts = []\n\n[machine]\nports = [{{ host = {port}, jail = {port} }}]\n')
        state = base / f"state-jail-{published}"
        state.mkdir()
        bindir = make_path(base / f"path-jail-{published}", state)
        done = hermetic_run(root, "observatory", port, bindir, base / "durable", base,
                            extra_env={"YOLO_JAIL": "1", "OBSERVATORY_JAIL_WORKSPACE": str(root),
                                       "YOLO_HOST_DIR": "/home/someone/code/tillr"})
        argv = argv_of(state, "tillr-view")
        if published:
            check("a published jail port binds the container side",
                  flag(argv, "--addr") == f"0.0.0.0:{port}", f"{argv}\n{done.stderr}")
            check("and names the host URL", f"http://localhost:{port}" in done.stderr, done.stderr)
        else:
            check("an unpublished jail port binds loopback", flag(argv, "--addr") == f"127.0.0.1:{port}",
                  f"{argv}\n{done.stderr}")
            check("and prints the exact line that publishes it",
                  f"ports = [{{ host = {port}, jail = {port} }}]" in done.stderr
                  and "[machine]" in done.stderr and "yolo config apply" in done.stderr
                  and "/home/someone/code/tillr/yolo-jail.toml on the host" in done.stderr
                  and "yolo stop" in done.stderr, done.stderr)


def case_dev_hands_the_planner_and_a_strict_port_to_its_children(base: Path) -> None:
    state = base / "state-dev"
    state.mkdir()
    root = make_root(base, "root-dev")
    bindir = make_path(base / "path-dev", state, hivemind=True)
    port = free_port_pair()
    try:
        done = hermetic_run(root, "observatory-dev", port, bindir, base / "durable", base,
                            extra_env={"OBSERVATORY_TEST_WAIT_UI": "1"}, timeout=90)
        code, err = done.returncode, done.stderr
    except subprocess.TimeoutExpired as expired:
        code, err = None, str(expired.stderr)
    api, ui = argv_of(state, "tillr-view-dev"), argv_of(state, "ui")
    real = root.resolve()
    out = Path(api[0]).parent if api else None
    check("dev: the recipe ends when its API does", code is not None, err)
    check("dev: the API is handed this checkout's store, root and planner",
          bool(api) and flag(api, "--store") == f"{real}/.tillr/state.db" and flag(api, "--root") == str(real)
          and out is not None and flag(api, "--tillr-drain") == str(out / "tillr-drain")
          and flag(api, "--addr") == f"127.0.0.1:{port + 1}", f"{api}\n{err}")
    check("dev: Vite may not wander to another port", "--strictPort" in ui
          and flag(ui, "--port") == str(port) and flag(ui, "--host") == "127.0.0.1", f"{ui}\n{err}")
    calls = (state / "go.calls").read_text() if (state / "go.calls").exists() else ""
    check("dev: the API is built without the embedded front end", "-tags" not in calls, calls)


def hermetic_cases() -> None:
    with tempfile.TemporaryDirectory(prefix="observatory.") as tmp:
        base = Path(tmp)
        for index, case in enumerate((
            case_serve_names_this_checkout_from_anywhere,
            case_missing_toolchain_is_refused_before_any_build,
            case_a_stale_front_end_without_node_is_refused,
            case_modules_from_another_platform_are_reinstalled,
            case_a_failed_install_is_refused_before_any_build,
            case_a_jail_binds_only_a_published_port,
            case_dev_hands_the_planner_and_a_strict_port_to_its_children,
        ), start=1):
            directory = base / f"h{index}"
            directory.mkdir()
            case(directory)


# --- part two: observatory-dev supervision ------------------------------------------------------

def free_port_pair() -> int:
    """A UI port whose successor is free too: the recipe derives the API's port from it."""
    for _ in range(200):
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        if port >= 65535:
            continue
        try:
            with socket.socket() as nxt:
                nxt.bind(("127.0.0.1", port + 1))
        except OSError:
            continue
        return port
    raise RuntimeError("no free port pair")


def read_pid(path: Path) -> int | None:
    try:
        return int(path.read_text().strip())
    except (OSError, ValueError):
        return None


def alive(pid: int | None) -> bool:
    if not pid:
        return False
    try:
        os.kill(pid, 0)
    except OSError:
        return False
    return True


def kill_recorded(state: Path | None = None) -> None:
    """Kill whatever the stubs recorded, so a failed case cannot leak the thing it hunts.

    A failing case is exactly the one where the supervisor did not clean up, so the suite's
    own teardown may not rely on the supervision it is testing.
    """
    if state is None:
        return
    for name in ("api.pid", "ui.pid"):
        pid = read_pid(state / name)
        if pid:
            try:
                os.kill(pid, signal.SIGKILL)
            except OSError:
                pass


def wait_for(predicate, timeout: float, step: float = 0.1) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return True
        time.sleep(step)
    return predicate()


def write_stubs(bindir: Path, state: Path) -> None:
    """`go build -o X .` writes the API stub to X; `npm … run dev` becomes the UI stub.

    Both stubs are real processes that stay alive, because the assertion is about who
    supervises them. Absolute paths are baked in: hivemind runs the Procfile's command in
    the repository root, so nothing may depend on this process's cwd or environment.
    """
    bindir.mkdir(parents=True, exist_ok=True)
    (bindir / "state").write_text(str(state))

    api_pid = state / "api.pid"
    api_die = state / "api.die"
    go_called = state / "go.called"
    api_body = f"""#!/bin/sh
echo $$ > {api_pid}
while [ ! -e {api_die} ]; do
    sleep 0.2
done
exit 2
"""
    go = f"""#!/bin/sh
# stub go: only `build -o <path> .` matters; it writes a long-lived API there.
echo called >> {go_called}
out=""
while [ $# -gt 0 ]; do
    case "$1" in
        -o) out="$2"; shift 2 ;;
        *) shift ;;
    esac
done
[ -n "$out" ] || {{ echo "stub go: no -o" >&2; exit 1; }}
mkdir -p "$(dirname "$out")"
cat > "$out" <<'API'
{api_body}API
chmod +x "$out"
"""

    ui_pid = state / "ui.pid"
    ui_die = state / "ui.die"
    npm = f"""#!/bin/sh
# stub npm: `ci` is a no-op, `run dev` is the UI process.
for a in "$@"; do
    case "$a" in
        dev) dev=1 ;;
    esac
done
[ -n "${{dev:-}}" ] || exit 0
echo $$ > {ui_pid}
while [ ! -e {ui_die} ]; do
    sleep 0.2
done
exit 0
"""

    for name, body in (("go", go), ("npm", npm)):
        path = bindir / name
        path.write_text(body)
        path.chmod(0o755)


def recipe_env(bindir: Path, durable: Path) -> dict[str, str]:
    env = dict(os.environ)
    env["PATH"] = f"{bindir}{os.pathsep}{env.get('PATH', '')}"
    env["YOLO_DURABLE_DIR"] = str(durable)
    # A dev run must never reach the real store: the stubs ignore the flag, and this keeps
    # the intent explicit if one of them ever learns to read it.
    env.pop("TILLR_VIEW_API", None)
    return env


def cleanup_on_eof(fd: int, group: int, state: Path) -> int:
    """A separate session watches a pipe held only by the suite, even through SIGKILL.

    EOF means the suite closed the run or died. Stop the recipe before killing its
    recorded stubs, so a cancelled suite does not rely on Python finally blocks.
    """
    with os.fdopen(fd, "rb") as control:
        control.read()
    try:
        os.killpg(group, signal.SIGTERM)
    except ProcessLookupError:
        pass
    kill_recorded(state)
    try:
        os.killpg(group, signal.SIGKILL)
    except ProcessLookupError:
        pass
    return 0


def recipe_proxy(fd: int, state: Path, port: str) -> int:
    """Start cleanup before the recipe, and keep the real recipe's exit and output.

    The proxy catches group SIGINT without ignoring it in exec'd children. The recipe
    joins this session's group; a pipe watcher in another session can clean it up even
    if the suite dies between starting the proxy and starting the recipe.
    """
    signal.signal(signal.SIGINT, lambda *_: None)
    subprocess.Popen(
        [sys.executable, str(Path(__file__).resolve()), "--cleanup-on-eof",
         str(fd), str(os.getpgrp()), str(state)],
        pass_fds=(fd,), stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL, start_new_session=True,
    )
    os.close(fd)
    recipe = subprocess.Popen(["just", RECIPE, port], stdin=subprocess.DEVNULL)
    code = recipe.wait()
    return code if code >= 0 else 128 - code


class Run:
    """One `just observatory-dev` start, cleaned up even if the gate kills the suite."""

    def __init__(self, port: int, env: dict[str, str]):
        self.port = port
        self.api_port = port + 1
        state = Path((Path(env["PATH"].split(os.pathsep)[0]) / "state").read_text())
        reader, self.cleanup_writer = os.pipe()
        try:
            self.proc = subprocess.Popen(
                [sys.executable, str(Path(__file__).resolve()), "--recipe-proxy",
                 str(reader), str(state), str(port)],
                cwd=REPO, env=env, pass_fds=(reader,),
                stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                text=True, start_new_session=True,
            )
        except BaseException:
            os.close(self.cleanup_writer)
            self.cleanup_writer = None
            raise
        finally:
            os.close(reader)

    def interrupt(self) -> None:
        """A terminal's Ctrl-C: SIGINT to the whole foreground process group.

        start_new_session makes this process the session leader, so its pid IS the group to
        signal — asking the kernel for the group after the leader is dead answers nothing,
        and a teardown that answers nothing leaves exactly the processes this suite hunts.
        """
        try:
            os.killpg(self.proc.pid, signal.SIGINT)
        except ProcessLookupError:
            pass

    def finish(self, timeout: float = 30.0) -> tuple[int | None, str, str]:
        try:
            out, err = self.proc.communicate(timeout=timeout)
        except subprocess.TimeoutExpired:
            # A recipe that will not let go is the failure this suite exists for, so it is a
            # verdict (None), not a crash: kill the group and read whatever was written.
            self.kill()
            out, err = self.proc.communicate(timeout=10)
            return None, out, err
        return self.proc.returncode, out, err

    def kill(self) -> None:
        if self.cleanup_writer is not None:
            os.close(self.cleanup_writer)
            self.cleanup_writer = None
        try:
            os.killpg(self.proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        try:
            self.proc.communicate(timeout=10)
        except subprocess.TimeoutExpired:
            pass


def wait_for_both(api_pid: Path, ui_pid: Path, timeout: float = 90.0) -> bool:
    return wait_for(lambda: api_pid.exists() and ui_pid.exists(), timeout)


def case_both_come_up_and_ctrl_c_clears_them(bindir: Path, state: Path, durable: Path) -> None:
    port = free_port_pair()
    run = Run(port, recipe_env(bindir, durable))
    api_pid, ui_pid = state / "api.pid", state / "ui.pid"
    try:
        check("both processes start", wait_for_both(api_pid, ui_pid),
              "the recipe never started both stubs; stdout/stderr above")
        api, ui = read_pid(api_pid), read_pid(ui_pid)
        check("both are alive while the recipe runs", alive(api) and alive(ui),
              f"api={api} alive={alive(api)}, ui={ui} alive={alive(ui)}")
        run.interrupt()
        gone = wait_for(lambda: not alive(api) and not alive(ui), 20.0)
        check("SIGINT to the group leaves neither process alive", gone,
              f"api={api} alive={alive(api)}, ui={ui} alive={alive(ui)}")
        code, out, err = run.finish()
        check("the recipe reports the two ports it is serving", f":{port}" in out and f":{port + 1}" in out,
              out.strip() + err.strip())
        check("the recipe exits after the interrupt", code is not None, f"exit={code}")
    finally:
        run.kill()
        kill_recorded(state)


def case_a_dead_api_takes_the_ui(bindir: Path, state: Path, durable: Path) -> None:
    port = free_port_pair()
    run = Run(port, recipe_env(bindir, durable))
    api_pid, ui_pid = state / "api.pid", state / "ui.pid"
    try:
        up = wait_for_both(api_pid, ui_pid)
        check("both processes start (api dies on its own)", up, "the recipe never started both stubs")
        if not up:
            return
        api, ui = read_pid(api_pid), read_pid(ui_pid)
        (state / "api.die").write_text("go\n")
        check("an API that dies first stops the UI too",
              wait_for(lambda: not alive(api) and not alive(ui), 20.0),
              f"api={api} alive={alive(api)}, ui={ui} alive={alive(ui)}")
        code, _, _ = run.finish()
        check("the recipe exits once a process is gone", code is not None, f"exit={code}")
    finally:
        run.kill()
        kill_recorded(state)


def case_a_dead_ui_takes_the_api(bindir: Path, state: Path, durable: Path) -> None:
    """The orphan the old recipe left: Vite stops and the backgrounded API stays behind."""
    port = free_port_pair()
    run = Run(port, recipe_env(bindir, durable))
    api_pid, ui_pid = state / "api.pid", state / "ui.pid"
    try:
        up = wait_for_both(api_pid, ui_pid)
        check("both processes start (ui dies on its own)", up, "the recipe never started both stubs")
        if not up:
            return
        api, ui = read_pid(api_pid), read_pid(ui_pid)
        (state / "ui.die").write_text("go\n")
        check("a UI that dies first stops the API too",
              wait_for(lambda: not alive(api) and not alive(ui), 20.0),
              f"api={api} alive={alive(api)}, ui={ui} alive={alive(ui)}")
        code, _, _ = run.finish()
        check("the recipe exits once the UI is gone", code is not None, f"exit={code}")
    finally:
        run.kill()
        kill_recorded(state)


def case_a_held_port_is_refused_before_the_build(bindir: Path, state: Path, durable: Path) -> None:
    port = free_port_pair()
    holder = socket.socket()
    holder.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    holder.bind(("127.0.0.1", port + 1))
    holder.listen(1)
    run = Run(port, recipe_env(bindir, durable))
    try:
        code, out, err = run.finish(timeout=90)
        check("a port that is already answering is refused", code not in (0, None),
              f"exit={code}\n{out}\n{err}")
        check("the refusal names the port", f":{port + 1}" in err, err.strip())
        check("the refusal says what is holding it", "already answering" in err, err.strip())
        check("nothing is built and nothing is started",
              not (state / "go.called").exists()
              and not (state / "api.pid").exists()
              and not (state / "ui.pid").exists(),
              f"go.called={(state / 'go.called').exists()}, "
              f"api.pid={(state / 'api.pid').exists()}, ui.pid={(state / 'ui.pid').exists()}")
    finally:
        run.kill()
        kill_recorded(state)
        holder.close()


def case_gate_cancellation_cleans_detached_recipe(bindir: Path, state: Path, durable: Path) -> None:
    """The gate SIGKILLs the suite's group; its detached recipe still needs cleanup."""
    probe = subprocess.Popen(
        [sys.executable, str(Path(__file__).resolve()), "--cancellation-probe",
         str(free_port_pair()), str(state)],
        env=recipe_env(bindir, durable), stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    recipe_pid = None
    try:
        check("the cancellation probe stays in the suite's process group",
              os.getpgid(probe.pid) == os.getpgrp())
        up = wait_for_both(state / "api.pid", state / "ui.pid", timeout=10)
        check("the cancellation probe starts both processes", up)
        recipe_pid = read_pid(state / "recipe.pid")
        api, ui = read_pid(state / "api.pid"), read_pid(state / "ui.pid")
        os.kill(probe.pid, signal.SIGKILL)
        probe.wait(timeout=10)
        check("gate cancellation leaves neither recipe nor stub alive",
              up and wait_for(lambda: not any(alive(pid) for pid in (recipe_pid, api, ui)), 3),
              f"recipe={recipe_pid}, api={api}, ui={ui}")
    finally:
        try:
            os.kill(probe.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        probe.wait(timeout=10)
        if recipe_pid:
            try:
                os.killpg(recipe_pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        kill_recorded(state)


def main() -> int:
    if not all(which(tool) for tool in ("just", "hivemind")):
        print("SKIP: just and hivemind are both required; this suite tests their wiring",
              file=sys.stderr)
        return 2
    hermetic_cases()
    with tempfile.TemporaryDirectory(prefix="observatory-dev.") as tmp:
        base = Path(tmp)
        bindir, durable = base / "bin", base / "durable"
        for index, case in enumerate((
            case_both_come_up_and_ctrl_c_clears_them,
            case_a_dead_api_takes_the_ui,
            case_a_dead_ui_takes_the_api,
            case_a_held_port_is_refused_before_the_build,
            case_gate_cancellation_cleans_detached_recipe,
        ), start=1):
            # Each case gets its own process state: a pid file left by the previous case is
            # exactly the staleness this suite is about.
            state = base / f"case{index}"
            state.mkdir()
            write_stubs(bindir, state)
            case(bindir, state, durable)
    if failures:
        print(f"\n{len(failures)} case(s) failed: {', '.join(failures)}", file=sys.stderr)
        return 1
    print("\nall cases passed")
    return 0


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--cleanup-on-eof":
        sys.exit(cleanup_on_eof(int(sys.argv[2]), int(sys.argv[3]), Path(sys.argv[4])))
    elif len(sys.argv) > 1 and sys.argv[1] == "--recipe-proxy":
        sys.exit(recipe_proxy(int(sys.argv[2]), Path(sys.argv[3]), sys.argv[4]))
    elif len(sys.argv) > 1 and sys.argv[1] == "--cancellation-probe":
        run = Run(int(sys.argv[2]), dict(os.environ))
        Path(sys.argv[3], "recipe.pid").write_text(str(run.proc.pid))
        signal.pause()
    else:
        sys.exit(main())
