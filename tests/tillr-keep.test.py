#!/usr/bin/env python3
"""Keeper tests: no real store, loop, gate, or model is invoked."""
import importlib.machinery
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
import unittest

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[1]
SCRATCH = os.environ.get("YOLO_DURABLE_DIR", str(ROOT / "tests"))
KEEPER = ROOT / "tools/tillr-roll/tillr-keep"
loader = importlib.machinery.SourceFileLoader("keeper", str(KEEPER))
spec = importlib.util.spec_from_loader(loader.name, loader)
keeper = importlib.util.module_from_spec(spec)
loader.exec_module(keeper)


def drain_stub(root, *argv, name=b"tillr-drain"):
    """Start a stand-in tillr-drain in root whose argv after the program is argv.

    The script is named tillr-drain, so its command line reads like the real binary's
    (interpreter, then .../tillr-drain, then the face), and it renames its own process
    to the drain's kernel name, which is what the keeper reads first.
    """
    script = Path(root) / ".stub" / "tillr-drain"
    script.parent.mkdir(exist_ok=True)
    if not script.exists():
        # It waits for an `exit` file in its working directory, or thirty seconds.
        script.write_text("import ctypes,sys,time\nfrom pathlib import Path\n"
                          "ctypes.CDLL(None).prctl(15,sys.argv[-1].encode(),0,0,0)\n"
                          "deadline = time.monotonic() + 30\n"
                          "while not Path('exit').exists() and time.monotonic() < deadline:\n"
                          "    time.sleep(0.01)\n")
    child = subprocess.Popen([sys.executable, str(script), *argv, name.decode()], cwd=root)
    deadline = time.monotonic() + 5
    while (keeper.process(child.pid) or ("",))[0] != name.decode()[:15]:
        if time.monotonic() > deadline:
            raise AssertionError("the stub never renamed itself")
        time.sleep(0.01)
    return child


class KeeperTests(unittest.TestCase):
    def test_three_stub_exits_wait_60_120_240(self):
        stop = threading.Event()
        waits = []
        launches = []

        def launch():
            child = subprocess.Popen([sys.executable, "-c", "raise SystemExit(4)"])
            launches.append(child)
            return child

        def observe(child):
            exited = keeper.observe_loop(stop, child)
            self.assertEqual(child.returncode, 4)
            return exited

        def wait(delay):
            waits.append(delay)
            if len(waits) == 3:
                stop.set()
            return stop.is_set()

        keeper.keep(stop, lambda: None, launch, observe, wait, clock=lambda: 0)
        self.assertEqual(len(launches), 3)
        self.assertEqual(waits, [60, 120, 240])

    def test_adopted_exit_restarts_with_production_discovery_and_observation(self):
        with tempfile.TemporaryDirectory(dir=SCRATCH) as directory:
            root = Path(directory)
            # Hold the adopted stub until observation begins; no timing race at discovery.
            marker = root / "exit"
            adopted = drain_stub(root, "loop")
            launched = []
            waits = []
            stop = threading.Event()
            try:

                def launch():
                    child = subprocess.Popen([sys.executable, "-c", "raise SystemExit(4)"], cwd=root)
                    launched.append(child)
                    return child

                def observe(child):
                    marker.touch()
                    return keeper.observe_loop(stop, child)

                def wait(seconds):
                    adopted.wait(timeout=5)
                    waits.append(seconds)
                    if len(waits) == 3:
                        stop.set()
                    return stop.is_set()

                keeper.keep(stop, lambda: keeper.find_loop(root), launch, observe, wait)
                self.assertEqual(len(launched), 2)
                self.assertEqual(waits, [60, 120, 240])
                self.assertTrue(all(child.returncode == 4 for child in launched))
            finally:
                for child in [adopted, *launched]:
                    if child.poll() is None:
                        child.terminate()
                    child.wait(timeout=5)

    def test_loop_root_overrides_are_refused(self):
        with tempfile.TemporaryDirectory(dir=SCRATCH) as directory:
            for option in ("--root", "--root=elsewhere", "-root", "-root=elsewhere"):
                result = subprocess.run([sys.executable, str(KEEPER), "--root", directory,
                                         "--", option, "elsewhere"],
                                        capture_output=True, text=True, timeout=5,
                                        env={**os.environ, "PATH": directory})
                self.assertEqual(result.returncode, 2, (option, result.stderr))
                self.assertIn("not a loop override", result.stderr)
            self.assertFalse((Path(directory) / ".tillr").exists())

    def test_only_the_loop_face_for_this_root_is_the_loop(self):
        # On 2026-11-17 the keeper adopted a short-lived `tillr-drain why` in the same
        # checkout as the loop, waited for it to exit, and only then started the loop. A
        # read-only face (why, plan, once, stop, policy) is never the loop, and neither is
        # a loop for another root.
        with tempfile.TemporaryDirectory(dir=SCRATCH) as directory:
            root = Path(directory)
            others = [drain_stub(root, face) for face in ("why", "plan", "once", "stop", "policy")]
            others.append(drain_stub(root, "loop", "--root", str(root / "elsewhere")))
            try:
                self.assertIsNone(keeper.find_loop(root))
                loop = drain_stub(root, "loop", "--root", str(root), "--lanes", "5")
                try:
                    self.assertEqual(keeper.find_loop(root), loop.pid)
                finally:
                    loop.terminate()
                    loop.wait(timeout=5)
            finally:
                for child in others:
                    child.terminate()
                    child.wait(timeout=5)

    def test_just_recipe_keeps_an_empty_loop_argument(self):
        # `just tillr-keep -- --rolling '' --lanes 5` rendered `--rolling  --lanes 5`, so
        # --lanes became the rolling program. The recipe passes every argument through
        # intact; a stand-in python3 on PATH prints the argv the recipe gave it.
        with tempfile.TemporaryDirectory(dir=SCRATCH) as directory:
            stub = Path(directory) / "python3"
            stub.write_text(f"#!{sys.executable}\nimport json,sys\nprint(json.dumps(sys.argv[1:]))\n")
            stub.chmod(0o755)
            environment = {**os.environ, "PATH": directory + os.pathsep + os.environ["PATH"]}
            result = subprocess.run(["just", "--justfile", str(ROOT / ".justfile"), "tillr-keep",
                                     "--root", directory, "--", "--rolling", "", "--lanes", "5", "a b"],
                                    capture_output=True, text=True, timeout=5, env=environment)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads(result.stdout),
                             ["tools/tillr-roll/tillr-keep", "--root", directory, "--",
                              "--rolling", "", "--lanes", "5", "a b"])

    def test_cap_and_healthy_reset(self):
        delay = 60
        waits = []
        for _ in range(8):
            wait, delay = keeper.restart_delay(delay, 299)
            waits.append(wait)
        self.assertEqual(waits, [60, 120, 240, 480, 960, 1800, 1800, 1800])
        self.assertEqual(keeper.restart_delay(delay, 300), (60, 60))

    def test_command_only_composes_loop(self):
        with tempfile.TemporaryDirectory(prefix="consumer repo ", dir=SCRATCH) as directory:
            root = Path(directory)
            command = keeper.loop_command(root, ["--lanes", "3"])
        self.assertEqual(command[:4], ["tillr-drain", "loop", "--root", str(root)])
        for face in ("gate", "land", "verdict"):
            self.assertEqual(command[command.index("--" + face) + 1], f"builtin:tillr-loop {face}")
        self.assertNotIn("retry", command)
        self.assertNotIn("--no-after-merge-check", command)
        self.assertEqual(command[-2:], ["--lanes", "3"])

    def test_process_name_not_command_line_and_repo_scope(self):
        with tempfile.TemporaryDirectory(dir=SCRATCH) as directory:
            root = Path(directory)
            # Both commands mention tillr-drain, only one process names itself that.
            impostor = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)",
                                         "tillr-drain"], cwd=root)
            real = drain_stub(root, "loop")
            try:
                self.assertEqual(keeper.find_loop(root), real.pid)
                self.assertIsNone(keeper.find_loop(root / "other"))
            finally:
                for child in (impostor, real):
                    child.terminate()
                    child.wait(timeout=5)

    def test_stop_leaves_launched_loop_running(self):
        with tempfile.TemporaryDirectory(dir=SCRATCH) as directory:
            root = Path(directory)
            stub = root / "tillr-drain"
            stub.write_text("#!/usr/bin/env python3\nimport ctypes,time\n"
                            "ctypes.CDLL(None).prctl(15,b'tillr-drain-abc123',0,0,0)\n"
                            "time.sleep(30)\n")
            stub.chmod(0o755)
            environment = {**os.environ, "PATH": str(root) + os.pathsep + os.environ["PATH"]}
            child = subprocess.Popen([sys.executable, str(KEEPER), "--root", str(root)],
                                     stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                     text=True, env=environment)
            loop_pid = None
            try:
                line = child.stdout.readline()
                self.assertIn("started loop", line)
                loop_pid = int(line.split()[-1])
                self.assertIn("watching", child.stdout.readline())
                deadline = time.monotonic() + 5
                while keeper.process(loop_pid)[0] != "tillr-drain-abc":
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(0.01)
                self.assertEqual(keeper.find_loop(root), loop_pid)
                self.assertNotEqual(os.getpgid(loop_pid), os.getpgid(child.pid))
                # Run the actual just recipe for stop, scoped to this scratch checkout.
                result = subprocess.run(["just", "--justfile", str(ROOT / ".justfile"),
                                         "tillr-keep", "--root", str(root), "--stop"],
                                        capture_output=True, text=True, timeout=5)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(child.wait(timeout=5), 0)
                self.assertIsNotNone(keeper.process(loop_pid))
                self.assertNotEqual(keeper.process(loop_pid)[2], "Z")
                self.assertEqual(keeper.find_loop(root), loop_pid)
            finally:
                if child.poll() is None:
                    child.terminate()
                child.wait(timeout=5)
                child.stdout.close()
                if loop_pid is not None:
                    os.kill(loop_pid, 15)

    def test_stop_leaves_adopted_loop_and_prevents_second_keeper(self):
        with tempfile.TemporaryDirectory(dir=SCRATCH) as directory:
            root = Path(directory)
            loop = drain_stub(root, "loop")
            child = None
            try:
                child = subprocess.Popen([sys.executable, str(KEEPER), "--root", str(root)],
                                         stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
                self.assertIn("watching", child.stdout.readline())
                second = subprocess.run([sys.executable, str(KEEPER), "--root", str(root)],
                                        capture_output=True, text=True, timeout=5)
                self.assertEqual(second.returncode, 2, second.stderr)
                self.assertIn("already running", second.stderr)
                stopped = subprocess.run([sys.executable, str(KEEPER), "--root", str(root), "--stop"],
                                         capture_output=True, text=True, timeout=5)
                self.assertEqual(stopped.returncode, 0, stopped.stderr)
                self.assertEqual(child.wait(timeout=5), 0)
                self.assertIsNone(loop.poll())
            finally:
                if child is not None:
                    if child.poll() is None:
                        child.terminate()
                    child.wait(timeout=5)
                    child.stdout.close()
                loop.terminate()
                loop.wait(timeout=5)

    def test_ambiguous_process_names_refuse(self):
        with tempfile.TemporaryDirectory(dir=SCRATCH) as directory:
            root = Path(directory)
            children = [drain_stub(root, "loop") for _ in range(2)]
            try:
                with self.assertRaisesRegex(RuntimeError, "multiple tillr-drain"):
                    keeper.find_loop(root)
            finally:
                for child in children:
                    child.terminate()
                    child.wait(timeout=5)

    def test_stop_during_wait_prevents_restart(self):
        stop = threading.Event()
        launched = []

        def wait(delay):
            stop.set()
            return True

        keeper.keep(stop, lambda: None, lambda: launched.append(1),
                    lambda child: True, wait, clock=lambda: 0)
        self.assertEqual(launched, [1])

    def test_saved_scale_target_survives_crash_relaunch_and_unflagged_restart(self):
        import sqlite3
        from datetime import datetime, timedelta, timezone

        self.assertEqual(
            keeper.strip_lanes(["--policy", "p.json", "--lanes", "1", "--rolling", "", "--lanes=2", "-lanes", "3"]),
            ["--policy", "p.json", "--rolling", ""],
        )
        with tempfile.TemporaryDirectory(dir=SCRATCH) as directory:
            root = Path(directory)
            db_path = root / ".tillr/state.db"
            db_path.parent.mkdir(parents=True, exist_ok=True)
            session_start = datetime(2026, 11, 23, 4, 0, 0, tzinfo=timezone.utc)
            before = (session_start - timedelta(minutes=5)).isoformat().replace("+00:00", "Z")
            after = (session_start + timedelta(minutes=5)).isoformat().replace("+00:00", "Z")
            with sqlite3.connect(db_path) as conn:
                conn.execute(
                    "CREATE TABLE drain_runtime_config ("
                    "key TEXT PRIMARY KEY, value_json TEXT NOT NULL, source TEXT NOT NULL, "
                    "updated_at TEXT NOT NULL, updated_by TEXT NOT NULL)"
                )
                conn.execute(
                    "INSERT INTO drain_runtime_config VALUES ('ordinary_lanes', '6', 'operator', ?, 'matt')",
                    (before,),
                )
            extra = ["--policy", "config/custom.json", "--lanes", "1"]
            # First launch and any retry before drain_runtime_config is seeded in this session keep --lanes 1.
            self.assertEqual(keeper.launch_extra_for_attempt(root, extra, session_start, adopted=False, launches=0), extra)
            self.assertEqual(keeper.launch_extra_for_attempt(root, extra, session_start, adopted=False, launches=1), extra)
            # Once seeded and later live-scaled to 4 during this session, crash relaunch omits --lanes.
            with sqlite3.connect(db_path) as conn:
                conn.execute(
                    "UPDATE drain_runtime_config SET value_json='4', updated_at=? WHERE key='ordinary_lanes'",
                    (after,),
                )
            self.assertEqual(
                keeper.launch_extra_for_attempt(root, extra, session_start, adopted=False, launches=1),
                ["--policy", "config/custom.json"],
            )
            self.assertEqual(
                keeper.launch_extra_for_attempt(root, extra, session_start, adopted=True, launches=0),
                ["--policy", "config/custom.json"],
            )
            # Unflagged --restart preserves saved 4 instead of re-passing boot-time --lanes 1.
            keeper.write_recorded_launch(root, "config/custom.json", "1")
            args, policy, lanes = keeper.resolve_restart_args(root, [], None)
            self.assertEqual(policy, "config/custom.json")
            self.assertEqual(lanes, "4")
            self.assertEqual(args, ["--policy", "config/custom.json"])
            # Explicit --lanes 2 on --restart still wins.
            args2, policy2, lanes2 = keeper.resolve_restart_args(root, ["--lanes", "2"], None)
            self.assertEqual(policy2, "config/custom.json")
            self.assertEqual(lanes2, "2")
            self.assertIn("--lanes", args2)

    def test_stop_absent_is_noop_and_help_does_not_start(self):
        with tempfile.TemporaryDirectory(dir=SCRATCH) as directory:
            result = subprocess.run([sys.executable, str(KEEPER), "--root", directory, "--stop"],
                                    capture_output=True, text=True, timeout=5)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertFalse((Path(directory) / ".tillr").exists())
        result = subprocess.run(["just", "--justfile", str(ROOT / ".justfile"), "tillr-keep", "--help"],
                                capture_output=True, text=True, timeout=5)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--stop", result.stdout)


if __name__ == "__main__":
    unittest.main(verbosity=2)
