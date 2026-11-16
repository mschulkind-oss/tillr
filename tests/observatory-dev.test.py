#!/usr/bin/env python3
"""`just observatory-dev` cannot leave a process behind, and cannot serve a stale face.

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

failures: list[str] = []


def check(name: str, ok: bool, detail: str = "") -> None:
    if ok:
        print(f"ok   {name}")
    else:
        print(f"FAIL {name}" + (f": {detail}" if detail else ""), file=sys.stderr)
        failures.append(name)


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
