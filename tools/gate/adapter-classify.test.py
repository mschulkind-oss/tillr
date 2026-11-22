#!/usr/bin/env python3
"""Installed-face handoff for the legacy gate and standalone loop."""
from contextlib import redirect_stderr
import io
import json
import os
from pathlib import Path
import runpy
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]


class AdapterClassificationTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.log = self.root / "observations"
        self.argv_log = self.root / "argv"
        installed = shutil.which("tillr-gate")
        self.assertIsNotNone(installed)
        standin = self.root / "tillr-gate"
        standin.write_text(f'''#!{sys.executable}
import json, subprocess, sys
raw = sys.stdin.buffer.read()
with open({str(self.log)!r}, "ab") as out: out.write(raw + b"\\n")
with open({str(self.argv_log)!r}, "a", encoding="utf-8") as out: out.write(json.dumps(sys.argv[1:]) + "\\n")
raise SystemExit(subprocess.run([{installed!r}, *sys.argv[1:]], input=raw).returncode)
''')
        standin.chmod(0o755)
        self.env = patch.dict(os.environ, PATH=str(self.root) + os.pathsep + os.environ["PATH"])
        self.env.start()
        self.addCleanup(self.env.stop)

    def observations(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()]

    def invocations(self):
        return [json.loads(line) for line in self.argv_log.read_text().splitlines()]

    def test_legacy_raw_facts_and_outcome(self):
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        check = gate["Check"]("example", "one", ["true"], "", 1)
        check.rc = 4
        self.assertEqual(gate["check_status"](check), "FAIL")
        obs = self.observations()[-1][0]
        self.assertEqual(obs["source"], "legacy-check")
        self.assertEqual(obs["exit_code"], 4)
        self.assertEqual(obs["legacy"]["group"], "verdict")
        check.rc = 124
        self.assertEqual(gate["check_status"](check), "ERR")
        self.assertEqual(self.observations()[-1][0]["exit_code"], 124)

    def test_legacy_precedence_and_changed_evidence_are_sent(self):
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        check = gate["Check"]("waybar", "one", [], "", 1)
        check.rc = 77
        status = gate["check_status"]
        self.assertEqual(status(check), "skip")
        self.assertTrue(self.observations()[-1][0]["legacy"]["sentinel_skip"])
        count = len(self.observations())
        self.assertEqual(status(check), "skip")
        self.assertEqual(len(self.observations()), count)
        check.cached = True
        self.assertEqual(status(check), "CACHED")
        check.not_run_reason = "cancelled"
        self.assertEqual(status(check), "not-run")
        check.not_run_reason = None
        check.cached = False
        check.rc = gate["CHECK_STALLED_RC"]
        self.assertEqual(status(check), "STALLED")
        check.rc = 0
        self.assertEqual(status(check), "ok")
        check.kind = "forks-check"
        check.rc = 4
        self.assertEqual(status(check), "ERR")
        self.assertTrue(self.observations()[-1][0]["legacy"]["reserves_exit_4"])

    def test_gate_removes_only_classifier_accepted_skips(self):
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        transport = gate["_classify_transport"]
        actual = transport.classify
        target = self.root / "case.ts"
        target.write_text("fixture\n")
        declaration = gate["Quarantine"]("quality", target.name, {"timing"}, "fixture/item",
                                         gate["file_blob_id"](target), "declared timing case")
        def make_plan():
            waybar = gate["Check"]("waybar", "waybar", ["check-waybar"], "", 1)
            waybar.rc = 77
            quality = gate["Check"]("fork-quality", "quality", ["check-quality"], "", 1)
            quality.rc = 1
            quality.fingerprint_dir = str(self.root)
            quality.output = "ℹ fail 1\n✖ failing tests:\n\ntest at case.ts:1:1\n✖ timing (2ms)\n"
            plan = gate["Plan"]()
            # Settlement reports Waybar first, not in the plan's check order.
            plan.checks = [quality, waybar]
            return plan, quality, waybar

        settle = gate["settle_check_skips"]
        with patch.dict(settle.__globals__, {"QUARANTINED_FORK_QUALITY": [declaration]}):
            plan, quality, waybar = make_plan()
            self.assertEqual(gate["check_status"](waybar), "skip")
            self.assertEqual(gate["check_status"](quality), "QUARANTINED")
            seen = self.observations()
            self.assertEqual([batch[0]["exit_code"] for batch in seen], [77, 1])
            self.assertTrue(seen[0][0]["legacy"]["sentinel_skip"])
            self.assertTrue(seen[1][0]["legacy"]["quarantined"])
            settle(plan)
            evidence = gate["evidence_records"]
            with patch.dict(evidence.__globals__, {"repo_ident": lambda _: "fixture"}):
                records = evidence(self.root, {}, [quality, waybar])[:2]
            self.assertEqual(self.observations(), seen)  # Reuse progress's answers.
            self.assertEqual(plan.checks, [])
            self.assertEqual([skip.kind for skip in plan.skips], ["waybar", "fork-quality"])
            self.assertIs(plan.skips[1].quarantine, declaration)
            self.assertEqual(plan.skips[1].why, declaration.reason)
            self.assertEqual([entry["outcome"] for entry in records], ["skipped", "skipped"])
            self.assertEqual(records[0]["detail"]["quarantine"]["blob"], declaration.blob)

            def keep(raw):
                answer = actual(raw)
                if answer[0]["outcome"] == "unavailable":
                    return [{"outcome": "fail", "detail": "failed"}]
                return answer
            plan, quality, waybar = make_plan()
            with patch.object(transport, "classify", side_effect=keep), \
                 patch.dict(evidence.__globals__, {"repo_ident": lambda _: "fixture"}):
                settle(plan)
                records = evidence(self.root, {}, plan.checks)[:2]
            self.assertEqual(plan.checks, [quality, waybar])
            self.assertEqual(plan.skips, [])
            self.assertEqual([entry["outcome"] for entry in records], ["failed", "failed"])
            self.assertNotIn("quarantine", records[0]["detail"])

            # A bookkeeping status has priority in progress but did not mask a
            # sentinel in the separate pre-aggregation removal pass.
            plan, quality, waybar = make_plan()
            plan.measure_landing = True
            waybar.wall_ms, quality.wall_ms = 5, 8
            waybar.not_run_reason = "cancelled"
            quality.cached = True
            self.assertEqual(gate["check_status"](waybar), "not-run")
            self.assertEqual(gate["check_status"](quality), "CACHED")
            settle(plan)
            self.assertEqual(plan.checks, [])
            self.assertEqual([skip.executed_check for skip in plan.skips], [waybar, quality])

    def test_fast_tree_execution_classifies_raw_exit_before_rendering(self):
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        check = gate["Check"]("fast", "one", ["runner"], "", 1)
        check.fast_tree = True
        attempt = gate["execute_attempt"]
        with patch.dict(attempt.__globals__, {"run_command": lambda *a, **k: (6, "stalled", 0)}):
            attempt(self.root, check, "base")
        self.assertEqual(check.rc, gate["CHECK_STALLED_RC"])
        observations = self.observations()[-1]
        self.assertEqual(observations, [
            {"check": "one", "source": "judging-program", "exit_code": 6},
            {"check": "one", "source": "legacy-check", "exit_code": 6, "legacy": {"group": "verdict"}},
        ])
        transport = gate["_classify_transport"]
        actual = transport.classify
        def changed(raw):
            replies = actual(raw)
            self.assertEqual(replies[0]["detail"], "stalled")
            replies[0] = {"outcome": "unknown", "detail": "error"}
            return replies
        other = gate["Check"]("fast", "two", ["runner"], "", 1)
        other.fast_tree = True
        with patch.dict(attempt.__globals__, {"run_command": lambda *a, **k: (6, "stalled", 0)}), \
             patch.object(transport, "classify", side_effect=changed):
            attempt(self.root, other, "base")
        self.assertTrue(other.preflight_refused)
        self.assertEqual(other.rc, 6)

    def test_legacy_kind_and_output_facts_reach_installed_face(self):
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        status = gate["check_status"]
        check = gate["Check"]("fork-quality", "one", ["/repo/bin/pi-forks", "candidate", "--deps"], "", 1)
        check.rc = 1
        check.output = "pi-forks: refusing to run from a linked worktree: /repo/lane\n"
        self.assertEqual(status(check), "ERR")
        facts = self.observations()[-1][0]["legacy"]
        self.assertEqual(facts["group"], "unable")
        self.assertTrue(facts["linked_worktree_refusal"])
        check.output = "STALE: candidate moved"
        check.rc = 2
        self.assertEqual(status(check), "STALE")
        self.assertTrue(self.observations()[-1][0]["legacy"]["stale"])
        check.rc = 1
        self.assertEqual(status(check), "FAIL")
        self.assertFalse(self.observations()[-1][0]["legacy"]["stale"])
        # The raw marker alone would broaden the old stale rule: on exit 1 the
        # installed face treats the marker fact as stale instead of a red.
        answer = subprocess.run(["tillr-gate", "classify"], input=json.dumps([
            {"source": "legacy-check", "exit_code": 1, "legacy": {"group": "unable", "stale": True}}
        ]), text=True, capture_output=True, check=True)
        self.assertEqual(json.loads(answer.stdout)[0]["detail"], "stale")
        check.kind, check.argv = "series", []
        check.output = "upstream v9 does not take the patch series"
        self.assertEqual(status(check), "FAIL")
        facts = self.observations()[-1][0]["legacy"]
        self.assertEqual(facts["group"], "invariant")
        self.assertTrue(facts["named_invariant"])
        check.kind, check.output = "example", ""
        check.fast_tree, check.preflight_refused = True, True
        self.assertEqual(status(check), "ERR")
        facts = self.observations()[-1][0]["legacy"]
        self.assertTrue(facts["fast_tree"] and facts["preflight_refused"])
        check.fast_tree, check.preflight_refused = False, False
        with patch.dict(status.__globals__, {"applied_quarantine": lambda _: object(),
                                             "sentinel_skip": lambda _: None}):
            self.assertEqual(status(check), "QUARANTINED")
        self.assertTrue(self.observations()[-1][0]["legacy"]["quarantined"])

    def test_legacy_uses_answer_and_transport_failure_is_unanswered(self):
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        check = gate["Check"]("example", "one", [], "", 1)
        check.rc = 4
        transport = gate["_classify_transport"]
        actual = transport.classify
        def changed(observations):
            self.assertEqual(actual(observations)[0]["outcome"], "fail")
            return [{"outcome": "unknown", "detail": "error"}]
        with patch.object(transport, "classify", side_effect=changed):
            self.assertEqual(gate["check_status"](check), "ERR")
        check.rc = 3
        with patch.object(transport, "classify", side_effect=transport.ClassificationError("offline")):
            with self.assertRaises(transport.ClassificationError):
                gate["check_status"](check)
            with patch.dict(gate["main"].__globals__, {"_main": lambda _: gate["check_status"](check)}):
                self.assertEqual(gate["main"]([]), 2)

    def test_evidence_consumes_the_underlying_classification(self):
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        check = gate["Check"]("example", "one", [], "", 1)
        check.rc = 1
        transport = gate["_classify_transport"]
        actual = transport.classify
        def changed(observations):
            answer = actual(observations)
            if "not_run" not in observations[0]["legacy"]:
                self.assertEqual(answer[0]["outcome"], "fail")
                return [{"outcome": "pass", "detail": "passed"}]
            return answer
        evidence = gate["evidence_records"]
        with patch.object(transport, "classify", side_effect=changed), \
             patch.dict(evidence.__globals__, {"repo_ident": lambda _: "fixture"}):
            record = evidence(self.root, {"commit": "abc", "at": "now"}, [check])[0]
        self.assertEqual(record["outcome"], "ok")

    def test_cached_verdict_is_classified_when_read(self):
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        cache = gate["VerdictCache"](self.root)
        cache.data["verdicts"]["key"] = {"rc": 0}
        transport = gate["_classify_transport"]
        actual = transport.classify
        def changed(observations):
            self.assertEqual(observations[0]["source"], "legacy-check")
            self.assertEqual(observations[0]["exit_code"], 0)
            self.assertEqual(actual(observations)[0]["outcome"], "pass")
            return [{"outcome": "unknown", "detail": "error"}]
        with patch.object(transport, "classify", side_effect=changed):
            self.assertIsNone(cache.lookup("key"))
        # The old JSON reader accepts these as equal to integer zero, whereas
        # the installed integer wire cannot carry either exact raw value.
        for value in (False, 0.0):
            cache.data["verdicts"]["key"] = {"rc": value}
            self.assertIsNotNone(cache.lookup("key"))

    def test_out_of_wire_cached_exit_is_a_miss_and_runs_the_check(self):
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        runtime = gate["RuntimeFingerprint"]("node", "npm")
        marker = self.root / "ran"
        command = [sys.executable, "-c", f"from pathlib import Path; Path({str(marker)!r}).touch()"]
        path = self.root / "verdicts.json"
        attempt = gate["execute_attempt"]
        for rc in (2**63, -2**63 - 1):
            with self.subTest(rc=rc):
                check = gate["Check"]("fork-quality", "quality", command, "", 1)
                check.fingerprint_dir = str(self.root)
                check.cache_quality = tuple(command)
                key = gate["verdict_key"]("fixture-tree", check.cache_quality, check.cache_install, runtime.entry())
                path.write_text(json.dumps({"schema": 1, "verdicts": {key: {"rc": rc}}}))
                cache = gate["VerdictCache"](path)
                self.assertIsNone(cache.lookup(key))
                marker.unlink(missing_ok=True)
                with patch.dict(attempt.__globals__, {"materialized_fingerprint": lambda _: "fixture-tree"}):
                    attempt(self.root, check, "base", cache=cache, runtime=runtime)
                self.assertTrue(marker.is_file())
                self.assertFalse(check.cached)
                self.assertEqual(check.rc, 0)
        # Only the fresh green verdicts reached the face, not the stored integers.
        self.assertEqual([batch[0]["exit_code"] for batch in self.observations()], [0, 0])

    def test_series_declaration_only_pass_is_not_expressible_on_installed_wire(self):
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        transcript = f"series: {gate['SERIES_UNRESOLVED']}\nother takes the series\n"
        observations = []
        for extra, settled_rc in (("", 0), ("unrecognised failure\n", 1)):
            check = gate["Check"]("series", "pack", [], "", 1)
            check.rc, check.output = 1, transcript + extra
            facts = gate["legacy_facts"](check, settled=True)
            observations.append({"source": "legacy-check", "exit_code": check.rc, "legacy": facts})
            gate["read_series_transcript"](check)
            self.assertEqual(check.rc, settled_rc)
            self.assertEqual(len(check.declared), 1)
        # Same raw exit and wire facts; there is no field for declared series
        # or leftover, unrecognised output. Neither observation passes.
        self.assertEqual(observations[0], observations[1])
        answer = gate["_classify_transport"].classify(observations)
        self.assertEqual([(item["outcome"], item["detail"]) for item in answer],
                         [("unknown", "error")] * 2)

    def test_zero_exit_named_conflict_gap(self):
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        check = gate["Check"]("self-suite", "suite", [sys.executable, "tests/fork-check.test.py"], "", 1)
        check.output = ('Traceback (most recent call last):\n'
                        '  File "tests/fork-check.test.py", line 7, in <module>\n'
                        '    assert False\nAssertionError\n')
        self.assertTrue(check.named_case)
        self.assertTrue(check.conflict)
        observation = {"source": "legacy-check", "exit_code": 0,
                       "legacy": {"group": "named", "named_case": True}}
        result = subprocess.run(["tillr-gate", "classify"], input=json.dumps([observation]),
                                text=True, capture_output=True, check=True)
        self.assertEqual(json.loads(result.stdout)[0]["outcome"], "pass")

    def test_stall_evidence_uses_the_face(self):
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        check = gate["Check"]("example", "one", [], "", 1)
        check.rc = gate["CHECK_STALLED_RC"]
        check.output = 'stall observations: {"silent_samples": 60}'
        plan = gate["Plan"]()
        plan.checks = [check]
        transport = gate["_classify_transport"]
        actual = transport.classify
        def changed(observations):
            answer = actual(observations)
            if observations[0]["legacy"] == {"group": "verdict"}:
                self.assertEqual(answer[0]["detail"], "stalled")
                return [{"outcome": "unknown", "detail": "error"}]
            return answer
        path = self.root / "fast.json"
        with patch.dict(os.environ, TILLR_FAST_VERDICT_OUT=str(path)), \
             patch.object(transport, "classify", side_effect=changed):
            gate["write_gate_spans"]("", plan, self.root, 1, "stalled", "head", "tree", [], 1, 1, 0)
        self.assertEqual(json.loads(path.read_text())["payload"]["checks"], [])

    def test_preflight_nonzero_gap_in_installed_legacy_wire(self):
        # The manual preflight treats every nonzero as fail, including timeouts;
        # the installed legacy protocol treats 124 as unknown, so it cannot replace it.
        result = subprocess.run(["tillr-gate", "classify"], input=json.dumps([
            {"source": "legacy-check", "exit_code": 124, "legacy": {"group": "verdict"}}
        ]), text=True, capture_output=True, check=True)
        self.assertEqual(json.loads(result.stdout)[0]["outcome"], "unknown")
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        import inspect
        self.assertIn('if run.rc == 0:', inspect.getsource(gate["run_preflight"]))

    def test_loop_judges_gate_exit_in_replay_shaped_directory(self):
        loop = runpy.run_path(str(ROOT / "tools/tillr-loop/tillr-loop"))
        script = self.root / "gate"
        script.write_text("#!/bin/sh\nexit 1\n")
        script.chmod(0o755)
        step = loop["Step"]([str(script), "run"], cwd=str(self.root))
        self.assertEqual(loop["execute"]("gate", [step]), 1)
        obs = self.observations()[-1][0]
        self.assertEqual(obs["source"], "judging-program")
        self.assertEqual(obs["exit_code"], 1)
        self.assertFalse((self.root / "tools/gate/classify.py").exists())

    def test_loop_reports_command_and_raw_exit_when_face_cannot_judge(self):
        loop = runpy.run_path(str(ROOT / "tools/tillr-loop/tillr-loop"))
        execute = loop["execute"]
        step = loop["Step"](["tillr-gate", "run"], cwd=str(self.root))
        stderr = io.StringIO()
        with patch.dict(execute.__globals__, {"invoke": lambda _: subprocess.CompletedProcess(step.argv, 3)}), \
             redirect_stderr(stderr):
            self.assertEqual(execute("gate", [step]), 4)
        self.assertEqual(self.observations(), [[{"source": "judging-program", "exit_code": 3}]])
        self.assertEqual(stderr.getvalue(), "tillr-loop gate: tillr-gate run answered exit 3: no judgment was made\n")

    def test_loop_uses_face_and_fails_closed(self):
        loop = runpy.run_path(str(ROOT / "tools/tillr-loop/tillr-loop"))
        step = loop["Step"](["gate", "run"], cwd=str(self.root))
        execute = loop["execute"]
        actual = subprocess.run
        def altered(*args, **kwargs):
            response = actual(*args, **kwargs)
            self.assertEqual(json.loads(response.stdout)[0]["outcome"], "pass")
            response.stdout = '[{"outcome":"fail","detail":"failed"}]'
            return response
        with patch.dict(execute.__globals__, {"invoke": lambda _: subprocess.CompletedProcess([], 0)}):
            with patch.object(subprocess, "run", side_effect=altered):
                self.assertEqual(execute("gate", [step]), 1)
            with patch.object(subprocess, "run", side_effect=OSError("offline")):
                self.assertEqual(execute("gate", [step]), 4)

    def test_verdict_of_uses_legacy_gate_aggregate(self):
        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        verdict_of = gate["verdict_of"]
        transport = verdict_of.__globals__["_classify_transport"]

        def make_check(kind="rank-order", rc=0, *, not_run=None, cached=False):
            check = gate["Check"](kind, f"{kind}-{rc}", ["true"], "why", 1)
            check.rc = rc
            check.not_run_reason = not_run
            check.cached = cached
            return check

        self.assertEqual(verdict_of([]), ("verified", 0))
        self.assertEqual(self.invocations()[-1], ["classify", "--aggregate", "legacy-gate"])
        self.assertEqual(self.observations()[-1], [])

        green = make_check(rc=0)
        red = make_check(rc=1)
        unanswered = make_check(rc=124)
        stalled = make_check(rc=gate["CHECK_STALLED_RC"])
        skipped_not_run = make_check(rc=0, not_run="cancelled")
        cached = make_check("fork-quality", rc=0, cached=True)

        self.assertEqual(verdict_of([green, skipped_not_run, cached]), ("verified", 0))
        self.assertEqual(self.invocations()[-1], ["classify", "--aggregate", "legacy-gate"])
        self.assertEqual(len(self.observations()[-1]), 3)
        self.assertEqual(verdict_of([red, skipped_not_run]), ("failed", 1))
        self.assertEqual(verdict_of([unanswered]), ("unproven", 2))
        self.assertEqual(verdict_of([stalled, red, unanswered]), ("stalled", 6))

        actual = transport.classify_aggregate

        def substituted(order, observations):
            self.assertEqual(order, "legacy-gate")
            real = actual(order, observations)
            self.assertEqual(real["run"], {"outcome": "pass", "detail": "verified", "exit_code": 0})
            return {
                "checks": real["checks"],
                "run": {"outcome": "unknown", "detail": "stalled", "exit_code": 6},
            }

        with patch.object(transport, "classify_aggregate", side_effect=substituted):
            self.assertEqual(verdict_of([green]), ("stalled", 6))

        with patch.dict(os.environ, PATH=str(self.root / "missing-bin")):
            with self.assertRaisesRegex(transport.ClassificationError, "tillr-gate classify"):
                verdict_of([green])

    def test_run_replay_uses_legacy_replay_aggregate(self):
        from contextlib import redirect_stdout
        import types

        gate = runpy.run_path(str(ROOT / "tools/fork-check.py"))
        run_replay = gate["run_replay"]
        main = gate["main"]
        transport = run_replay.__globals__["_classify_transport"]

        def run_case(kind="rank-order", *, effect, extra_args=(), call_main=False):
            planned = gate["Check"](kind, "one", ["true"], "why", 1)
            fake_gate = types.SimpleNamespace(
                plan=types.SimpleNamespace(checks=[planned], skips=[]),
                missing=[],
                diff_base=None,
                head="4ca1a67aabaf3488d6535d3f451a67113cdeafcc",
            )
            stdout, stderr = io.StringIO(), io.StringIO()
            argv = ["--replay", "--only", kind, *extra_args]
            with patch.dict(run_replay.__globals__, {
                "plan_gate": lambda root, options: fake_gate,
                "execute_attempt": lambda root, check, diff_base, cancel=None: effect(check),
                "capture_fork_conflict": lambda root, check, cancel: None,
            }), redirect_stdout(stdout), redirect_stderr(stderr):
                code = main(argv) if call_main else run_replay(argv)
            return code, stdout.getvalue(), stderr.getvalue()

        code, out, err = run_case(effect=lambda check: setattr(check, "rc", 0))
        self.assertEqual(code, 0, err)
        self.assertEqual(self.invocations()[-1], ["classify", "--aggregate", "legacy-replay"])
        self.assertIn("PASS rank-order: one (exit 0)", out)
        self.assertIn("all 1 named check(s) green on 4ca1a67aabaf", out)

        code, out, err = run_case(effect=lambda check: (setattr(check, "rc", 1), setattr(check, "output", "boom\n")))
        self.assertEqual(code, 1)
        self.assertIn("FAIL rank-order: one (exit 1)", out)
        self.assertIn("boom", out)
        self.assertIn("1 of 1 check(s) red on 4ca1a67aabaf: rank-order: one", err)

        for kind, mutator in (
            ("rank-order", lambda check: (setattr(check, "rc", 124), setattr(check, "output", "timed out"))),
            ("waybar", lambda check: setattr(check, "rc", 77)),
            ("rank-order", lambda check: (setattr(check, "rc", 0), setattr(check, "was_cancelled", True))),
            ("rank-order", lambda check: setattr(check, "rc", gate["CHECK_STALLED_RC"])),
        ):
            with self.subTest(kind=kind):
                code, out, err = run_case(kind=kind, effect=mutator)
                self.assertEqual(code, 2, out + err)
                self.assertIn("could not judge", err)

        actual = transport.classify_aggregate

        def as_fail(order, observations):
            self.assertEqual(order, "legacy-replay")
            real = actual(order, observations)
            self.assertEqual(real["run"], {"outcome": "pass", "detail": "pass", "exit_code": 0})
            return {
                "checks": [{"outcome": "fail", "detail": "failed"}],
                "run": {"outcome": "fail", "detail": "fail", "exit_code": 1},
            }

        def as_unknown(order, observations):
            self.assertEqual(order, "legacy-replay")
            real = actual(order, observations)
            self.assertEqual(real["run"], {"outcome": "pass", "detail": "pass", "exit_code": 0})
            return {
                "checks": [{"outcome": "unknown", "detail": "error"}],
                "run": {"outcome": "unknown", "detail": "error", "exit_code": 2},
            }

        with patch.object(transport, "classify_aggregate", side_effect=as_fail):
            code, out, err = run_case(effect=lambda check: setattr(check, "rc", 0))
            self.assertEqual(code, 1)
            self.assertIn("FAIL rank-order: one (exit 0)", out)
            self.assertIn("1 of 1 check(s) red", err)

        with patch.object(transport, "classify_aggregate", side_effect=as_unknown):
            code, out, err = run_case(effect=lambda check: setattr(check, "rc", 0))
            self.assertEqual(code, 2)
            self.assertIn("could not judge (exit 0)", err)

        with patch.dict(os.environ, PATH=str(self.root / "missing-bin")):
            code, _, err = run_case(effect=lambda check: setattr(check, "rc", 0), call_main=True)
            self.assertEqual(code, 2)
            self.assertIn("fork-check: could not judge: tillr-gate classify", err)
            code, _, err = run_case(effect=lambda check: setattr(check, "rc", 0), extra_args=("--fixer",), call_main=True)
            self.assertEqual(code, 2)
            self.assertIn("fork-check replay: could not judge: tillr-gate classify", err)

    def test_classify_aggregate_validates_wire_shape(self):
        import importlib.util

        spec = importlib.util.spec_from_file_location("gate_classify_test", ROOT / "tools/gate/classify.py")
        classify = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(classify)

        observations = [{"source": "result-word", "word": "pass"}]
        answered = classify.classify_aggregate("legacy-gate", observations)
        self.assertEqual(answered["run"], {"outcome": "pass", "detail": "verified", "exit_code": 0})
        self.assertEqual(answered["checks"][0]["outcome"], "pass")

        with self.assertRaisesRegex(classify.ClassificationError, "aggregate order"):
            classify.classify_aggregate("", observations)

        for code, stdout in (
            (2, "{}"),
            (0, "not json"),
            (0, "[]"),
            (0, '{"checks": []}'),
            (0, '{"checks": [], "run": {"outcome": "pass", "detail": "verified", "exit_code": 0}}'),
            (0, '{"checks": [{"outcome": "pass", "detail": "passed", "raw_word": "pass"}], "run": {}}'),
            (0, '{"checks": [{"outcome": "pass", "detail": "passed", "raw_word": "pass"}],'
                ' "run": {"outcome": "pass", "detail": "verified", "exit_code": true}}'),
            (0, '{"checks": [{"outcome": "pass", "detail": "passed", "raw_word": "fail"}],'
                ' "run": {"outcome": "pass", "detail": "verified", "exit_code": 0}}'),
        ):
            with self.subTest(code=code, stdout=stdout), patch.object(
                classify, "run_command", return_value=subprocess.CompletedProcess([], code, stdout=stdout, stderr="")
            ):
                with self.assertRaisesRegex(classify.ClassificationError, "tillr-gate classify"):
                    classify.classify_aggregate("legacy-gate", observations)


if __name__ == "__main__":
    unittest.main()
