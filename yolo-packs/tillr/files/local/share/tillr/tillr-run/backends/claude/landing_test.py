#!/usr/bin/env python3
"""The Claude delivery call: hook decisions, the MCP replay result, the transcript check."""
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

LANDING = str(Path(__file__).resolve().parent / "landing")


def sh(cwd, *args):
    subprocess.run(args, cwd=cwd, check=True, capture_output=True,
                   env=dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t", GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t"))


class Fixture(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        root = Path(self._tmp.name)
        self.repo, self.durable = root / "repo", root / "durable"
        self.repo.mkdir()
        self.durable.mkdir()
        self.transcript = root / "session.jsonl"
        self.transcript.write_text("")
        sh(self.repo, "git", "init", "-q")
        (self.repo / "a").write_text("1")
        sh(self.repo, "git", "add", "a")
        sh(self.repo, "git", "commit", "-qm", "base")
        self.base = self.head()

    def head(self):
        return subprocess.run(["git", "rev-parse", "HEAD"], cwd=self.repo, capture_output=True, text=True, check=True).stdout.strip()

    def commit(self, text="2"):
        (self.repo / "a").write_text(text)
        sh(self.repo, "git", "commit", "-qam", text)
        return self.head()

    def call(self, command, event=None, *extra, env=None):
        return subprocess.run([LANDING, command, "--durable", str(self.durable), "--base", self.base, *extra],
                              input=json.dumps(event) if event is not None else None, capture_output=True, text=True,
                              env=dict(os.environ, **(env or {})))

    def pre(self, tool="mcp__tillr__land", call="toolu_1", tool_input=None):
        event = {"tool_name": tool, "tool_use_id": call, "tool_input": {} if tool_input is None else tool_input,
                 "cwd": str(self.repo), "transcript_path": str(self.transcript)}
        out = self.call("pre-tool-use", event)
        self.assertEqual(out.returncode, 0, out.stderr)
        if not out.stdout.strip():
            return None, None
        body = json.loads(out.stdout)["hookSpecificOutput"]
        return body["permissionDecision"], body.get("permissionDecisionReason")

    def offer(self):
        return json.loads((self.durable / "landing-offer.json").read_text())


class PreToolUse(Fixture):
    def test_a_committed_clean_offer_is_deferred_and_recorded_as_pi_records_it(self):
        head = self.commit()
        self.assertEqual(self.pre(), ("defer", None))
        self.assertEqual(self.offer(), {"call_id": "toolu_1", "commit": head, "base": self.base, "transcript": str(self.transcript)})

    def test_extractor_uses_only_the_commit_offer_as_the_success_ending(self):
        self.commit()
        self.assertEqual(self.pre(), ("defer", None))
        path = self.durable / "landing-offer.json"
        extractor = Path(LANDING).with_name("extract-report")
        for legacy in (False, True):
            with self.subTest(legacy=legacy):
                if legacy:
                    path.write_text(json.dumps({**self.offer(), "summary": "claimed test results"}))
                out = subprocess.run([extractor, "--backend", "claude", "--landing-offer", path],
                                     input="", text=True, capture_output=True, check=True)
                self.assertIn("complete: yes\nresult: done\n", out.stdout)
                self.assertNotIn("claimed test results", out.stdout)

    def test_checks_a_worker_can_fix_return_at_once_and_write_no_offer(self):
        decision, reason = self.pre()
        self.assertEqual(decision, "deny")
        self.assertIn("HEAD is still the staged base", reason)
        self.commit()
        (self.repo / "dirty").write_text("x")
        decision, reason = self.pre()
        self.assertEqual(decision, "deny")
        self.assertIn("dirty", reason)
        (self.repo / "dirty").unlink()
        sh(self.repo, "git", "mv", "a", "renamed_a")
        decision, reason = self.pre()
        self.assertEqual(decision, "deny")
        self.assertIn("renamed_a", reason)
        self.assertIn("a", reason)
        sh(self.repo, "git", "reset", "-q", "--hard", "HEAD")
        self.assertFalse((self.durable / "landing-offer.json").exists())

    def test_a_commit_already_refused_is_denied_with_the_stored_answer(self):
        head = self.commit()
        (self.durable / "landing-state.json").write_text(json.dumps({"refused": [{"commit": head, "answer": "ANSWER: gate said no"}]}))
        decision, reason = self.pre()
        self.assertEqual(decision, "deny")
        self.assertIn("A new commit is required.\nANSWER: gate said no", reason)

    def test_land_ignores_old_report_only_refusals_but_honors_content_refusals(self):
        commit = self.commit()
        self.assertEqual(self.pre()[0], "defer")
        first = self.offer()
        (self.durable / "landing-offer.json").unlink()
        state = self.durable / "landing-state.json"
        report = {"commit": commit, "summary": "old summary", "answer": "Criterion c2 (report): missing results", "report_only": True}
        content = {"commit": commit, "answer": "Criterion c1 (content): missing test"}
        state.write_text(json.dumps({"refused": [report, content]}))
        decision, reason = self.pre(call="next")
        self.assertEqual(decision, "deny")
        self.assertIn("Criterion c1 (content)", reason)
        state.write_text(json.dumps({"refused": [report]}))
        self.assertEqual(self.pre(call="next"), ("defer", None))
        self.assertEqual(self.offer(), {**first, "call_id": "next"})

    def test_a_sibling_call_is_denied_while_an_offer_is_open_and_allowed_once_it_has_its_result(self):
        self.commit()
        self.assertEqual(self.pre()[0], "defer")
        self.assertEqual(self.pre(tool="Bash", call="toolu_2")[0], "deny")
        self.assertEqual(self.pre(call="toolu_3")[0], "deny")
        self.assertEqual(self.pre(call="toolu_1")[0], "defer")  # a replay of the parked call
        self.transcript.write_text(json.dumps({"type": "user", "message": {"content": [{"type": "tool_result", "tool_use_id": "toolu_1", "content": "x"}]}}) + "\n")
        self.assertEqual(self.pre(tool="Bash", call="toolu_2"), (None, None))
        self.assertFalse((self.durable / "landing-offer.json").exists())

    def test_the_staged_answer_for_this_call_is_allowed_through_to_the_server(self):
        self.commit()
        (self.durable / "landing-answer.json").write_text(json.dumps({"call_id": "toolu_1", "text": "ANSWER"}))
        self.assertEqual(self.pre()[0], "allow")

    def test_obstacle_is_validated_and_deferred_in_the_pi_offer_shape(self):
        decision, reason = self.pre(tool="mcp__tillr__obstacle", tool_input={"kind": "fence", "evidence": "e"})
        self.assertEqual(decision, "deny")
        self.assertIn('Invalid obstacle kind "fence"', reason)
        decision, reason = self.pre(tool="mcp__tillr__obstacle", tool_input={"kind": "tooling", "command": "check", "paths": "---, ```", "evidence": "e"})
        self.assertEqual(decision, "deny")
        self.assertIn("missing required field(s): paths", reason)
        decision, reason = self.pre(tool="mcp__tillr__obstacle", tool_input={"kind": "seat", "tried": "t", "missing": "m"})
        self.assertEqual(decision, "deny")  # no attempt yet
        self.assertEqual(self.pre(tool="mcp__tillr__obstacle", tool_input={"kind": "owner", "question": "Which?"})[0], "defer")
        self.assertEqual(self.offer()["obstacle"], {"kind": "owner", "fields": {"QUESTION": "Which?"}})
        self.assertEqual(self.offer()["tool"], "obstacle")


class Mcp(Fixture):
    def serve(self, *messages, **kwargs):
        out = subprocess.run([LANDING, "mcp", "--durable", str(self.durable)], input="".join(json.dumps(m) + "\n" for m in messages), capture_output=True, text=True, timeout=10)
        return [json.loads(line) for line in out.stdout.splitlines()]

    def call_msg(self, call="toolu_1"):
        return {"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": {"name": "land", "arguments": {}, "_meta": {"claudecode/toolUseId": call}}}

    def test_it_lists_both_tools_and_carries_the_naming_instruction(self):
        init, tools = self.serve({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-03-26"}},
                                 {"jsonrpc": "2.0", "method": "notifications/initialized"},
                                 {"jsonrpc": "2.0", "id": 3, "method": "tools/list"})
        self.assertIn("mcp__tillr__land", init["result"]["instructions"])
        self.assertEqual([t["name"] for t in tools["result"]["tools"]], ["land", "obstacle"])

    def test_the_answer_is_the_calls_own_error_result(self):
        (self.durable / "landing-answer.json").write_text(json.dumps({"call_id": "toolu_1", "text": "ANSWER: refused\ncheck go-test"}))
        (reply,) = self.serve(self.call_msg())
        self.assertEqual(reply["result"], {"content": [{"type": "text", "text": "ANSWER: refused\ncheck go-test"}], "isError": True})

    def test_a_call_that_was_not_parked_reports_so_and_clears_its_offer(self):
        (self.durable / "landing-offer.json").write_text(json.dumps({"call_id": "toolu_1"}))
        (reply,) = self.serve(self.call_msg())
        self.assertTrue(reply["result"]["isError"])
        self.assertIn("by itself", reply["result"]["content"][0]["text"])
        self.assertFalse((self.durable / "landing-offer.json").exists())


class Transcript(Fixture):
    def held(self, rows, call="toolu_1"):
        self.transcript.write_text("".join(json.dumps(r) + "\n" for r in rows))
        return subprocess.run([LANDING, "held", str(self.transcript), call], capture_output=True).returncode

    DEFERRED = {"type": "attachment", "attachment": {"type": "hook_deferred_tool", "toolUseID": "toolu_1", "toolName": "mcp__tillr__land"}}
    RESULT = {"type": "user", "message": {"content": [{"type": "tool_result", "tool_use_id": "toolu_1", "content": "a"}]}}

    def test_an_unresolved_deferred_call_is_held(self):
        self.assertEqual(self.held([self.DEFERRED]), 0)

    def test_a_call_with_its_result_saved_is_reported_resolved(self):
        self.assertEqual(self.held([self.DEFERRED, self.RESULT]), 3)

    def test_no_marker_or_another_call_is_not_held(self):
        self.assertEqual(self.held([{"type": "user"}]), 1)
        self.assertEqual(self.held([self.DEFERRED], call="toolu_9"), 1)

    def test_a_marker_beyond_the_tail_window_is_not_held(self):
        filler = {"type": "assistant", "message": {"content": [{"type": "text", "text": "x" * 4096}]}}
        self.assertEqual(self.held([self.DEFERRED] + [filler] * 400), 1)


class Stop(Fixture):
    def stop(self, **env):
        out = self.call("stop", {"cwd": str(self.repo), "transcript_path": str(self.transcript)}, env=env)
        self.assertEqual(out.returncode, 0, out.stderr)
        return json.loads(out.stdout) if out.stdout.strip() else None

    def test_a_plain_stop_is_nudged_once_per_state(self):
        first = self.stop()
        self.assertEqual(first["decision"], "block")
        self.assertIn("mcp__tillr__land", first["reason"])
        self.assertIsNone(self.stop())  # same head, tree and message
        self.commit()
        self.assertEqual(self.stop()["decision"], "block")  # the state moved

    def test_only_a_clean_unchanged_piece_can_finish_with_a_done_report(self):
        brief = self.transcript.with_name("brief.md")
        section = ("## Workflow node\n\nRun only node work/piece-mirror on the build seat; access: write.\n"
                   "An unchanged piece may finish without a commit.\n")
        env = {"BOARD_DRAIN_BRIEF": str(brief)}

        def report(text):
            self.transcript.write_text(json.dumps({"type": "assistant", "message": {
                "content": [{"type": "text", "text": text}]}}) + "\n")

        brief.write_text(section)
        report("Verified: pack sync left no diff.\n\nRESULT: done")
        self.assertIsNone(self.stop(**env))
        brief.write_text("Brief\n## Saved request words (verbatim; 123 bytes)\n" + section)
        self.assertEqual(self.stop(**env)["decision"], "block")
        brief.write_text(section)
        (self.repo / "scratch").touch()
        self.assertEqual(self.stop(**env)["decision"], "block")
        (self.repo / "scratch").unlink()
        report("Still working")
        self.assertEqual(self.stop(**env)["decision"], "block")
        self.commit()
        report("Changed and verified.\n\nRESULT: done")
        self.assertEqual(self.stop(**env)["decision"], "block")

    def test_a_requested_pause_is_never_nudged(self):
        pause = Path(self._tmp.name) / "pause"
        pause.write_text("")
        self.assertIsNone(self.stop(TILLR_PAUSE_REQUEST=str(pause)))


class Prepare(Fixture):
    def test_it_writes_a_stdio_server_and_the_two_hooks_merged_over_given_settings(self):
        extra_json = Path(self._tmp.name) / "policy-settings.json"
        extra_json.write_text('{"env":{"B":"1"},"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"true"}]}]}}')
        extra_list = Path(self._tmp.name) / "extra.bin"
        extra_list.write_bytes(str(extra_json).encode() + b"\0" + b'{"permissions":{"allow":["Read"]}}\0')
        out = self.call("prepare", None, "--settings-json", '{"env":{"A":""}}', "--extra-settings-file", str(extra_list))
        self.assertEqual(out.returncode, 0, out.stderr)
        mcp = json.loads((self.durable / "claude-mcp.json").read_text())["mcpServers"]["tillr"]
        self.assertEqual(mcp["type"], "stdio")
        self.assertEqual(mcp["args"][1:], ["mcp", "--durable", str(self.durable)])
        settings = json.loads((self.durable / "claude-settings.json").read_text())
        self.assertEqual(settings["env"], {"A": "", "B": "1"})
        self.assertEqual(settings["permissions"], {"allow": ["Read"]})
        self.assertEqual(len(settings["hooks"]["PreToolUse"]), 2)
        self.assertIn("pre-tool-use", settings["hooks"]["PreToolUse"][1]["hooks"][0]["command"])
        self.assertIn(" stop ", settings["hooks"]["Stop"][0]["hooks"][0]["command"])


if __name__ == "__main__":
    unittest.main()
