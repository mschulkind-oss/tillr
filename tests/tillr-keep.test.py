#!/usr/bin/env python3
"""Keeper tests: no real store, loop, gate, or model is invoked."""
import importlib.machinery
import importlib.util
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
            script = ("import ctypes,time; from pathlib import Path; "
                      "ctypes.CDLL(None).prctl(15,b'tillr-drain',0,0,0); "
                      "exec('while not Path(\"exit\").exists(): time.sleep(0.01)')")
            adopted = subprocess.Popen([sys.executable, "-c", script], cwd=root)
            launched = []
            waits = []
            stop = threading.Event()
            try:
                deadline = time.monotonic() + 5
                while keeper.process(adopted.pid)[0] != "tillr-drain":
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(0.01)

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

    def test_cap_and_healthy_reset(self):
        delay = 60
        waits = []
        for _ in range(8):
            wait, delay = keeper.restart_delay(delay, 299)
            waits.append(wait)
        self.assertEqual(waits, [60, 120, 240, 480, 960, 1800, 1800, 1800])
        self.assertEqual(keeper.restart_delay(delay, 300), (60, 60))

    def test_command_only_composes_loop(self):
        command = keeper.loop_command(ROOT, ["--lanes", "3"])
        self.assertEqual(command[:4], ["tillr-drain", "loop", "--root", str(ROOT)])
        for face in ("gate", "land", "verdict"):
            self.assertIn("--" + face, command)
        self.assertNotIn("retry", command)
        self.assertNotIn("--no-after-merge-check", command)
        self.assertEqual(command[-2:], ["--lanes", "3"])

    def test_process_name_not_command_line_and_repo_scope(self):
        with tempfile.TemporaryDirectory(dir=SCRATCH) as directory:
            root = Path(directory)
            # Both commands mention tillr-drain, only one process names itself that.
            impostor = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)",
                                         "tillr-drain"], cwd=root)
            real = subprocess.Popen([sys.executable, "-c",
                                     "import ctypes,time; ctypes.CDLL(None).prctl(15,b'tillr-drain',0,0,0); time.sleep(30)"], cwd=root)
            try:
                deadline = time.monotonic() + 5
                while keeper.process(real.pid)[0] != "tillr-drain":
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(0.01)
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
            loop = subprocess.Popen([sys.executable, "-c",
                                     "import ctypes,time; ctypes.CDLL(None).prctl(15,b'tillr-drain',0,0,0); time.sleep(30)"], cwd=root)
            child = None
            try:
                deadline = time.monotonic() + 5
                while keeper.process(loop.pid)[0] != "tillr-drain":
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(0.01)
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
            children = [subprocess.Popen([sys.executable, "-c",
                        "import ctypes,time; ctypes.CDLL(None).prctl(15,b'tillr-drain',0,0,0); time.sleep(30)"],
                        cwd=root) for _ in range(2)]
            try:
                deadline = time.monotonic() + 5
                while any(keeper.process(child.pid)[0] != "tillr-drain" for child in children):
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(0.01)
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
