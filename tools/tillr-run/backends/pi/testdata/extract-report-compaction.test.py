#!/usr/bin/env python3
"""A threshold compaction followed by an unprompted memory-context turn must not hide the first ending."""
import json
from pathlib import Path
import subprocess
import unittest

EXTRACT = Path(__file__).resolve().parent.parent / "extract-report"


def run_extract(events):
    stream = "".join(json.dumps(event) + "\n" for event in events)
    return subprocess.run(
        [str(EXTRACT), "--backend", "pi", "--exit-code", "0"],
        input=stream,
        text=True,
        capture_output=True,
    )


class CompactionFollowupReportTest(unittest.TestCase):
    def test_threshold_compaction_memory_context_turn_keeps_first_result(self):
        first_text = (
            "Nine gaps in the design.\n\n"
            "OBSTACLE: definition\n"
            "CRITERIA: c1\n"
            "EVIDENCE: missing specification\n"
            "RESULT: blocked\n"
        )
        first_assistant = {
            "id": "m1",
            "role": "assistant",
            "content": [{"type": "text", "text": first_text}],
        }
        memory_msg = {
            "id": "m-mem",
            "role": "custom",
            "customType": "memory-context",
            "content": "<memory>persistent long-term memory context</memory>",
            "display": False,
        }
        second_text = "waiting for the next instruction"
        second_assistant = {
            "id": "m2",
            "role": "assistant",
            "content": [{"type": "text", "text": second_text}],
        }
        events = [
            {"type": "session", "version": 3, "id": "11111111-1111-7111-8111-111111111111"},
            {"type": "agent_start"},
            {"type": "turn_start"},
            {"type": "message_start", "message": {"role": "user", "content": "plan the task"}},
            {"type": "message_end", "message": {"role": "user", "content": "plan the task"}},
            {"type": "message_start", "message": {"id": "m1", "role": "assistant", "content": []}},
            {
                "type": "message_update",
                "message": first_assistant,
                "assistantMessageEvent": {"type": "text_delta", "delta": first_text},
            },
            {"type": "message_end", "message": first_assistant},
            {
                "type": "turn_end",
                "message": {
                    **first_assistant,
                    "usage": {"totalTokens": 5051392, "cost": {"total": 1.32}},
                },
            },
            {
                "type": "agent_end",
                "messages": [{"role": "user", "content": "plan the task"}, first_assistant],
            },
            {"type": "compaction_start", "reason": "threshold"},
            {
                "type": "compaction_end",
                "reason": "threshold",
                "aborted": False,
                "willRetry": False,
                "result": {
                    "summary": (
                        "No specific task has been requested yet. "
                        "The session only loaded the user's persistent long-term memory context"
                    )
                },
            },
            {"type": "agent_start"},
            {"type": "turn_start"},
            {"type": "message_start", "message": memory_msg},
            {"type": "message_end", "message": memory_msg},
            {"type": "message_start", "message": {"id": "m2", "role": "assistant", "content": []}},
            {
                "type": "message_update",
                "message": second_assistant,
                "assistantMessageEvent": {"type": "text_delta", "delta": second_text},
            },
            {"type": "message_end", "message": second_assistant},
            {
                "type": "turn_end",
                "message": {
                    **second_assistant,
                    "usage": {"totalTokens": 500, "cost": {"total": 0.001}},
                },
            },
            {"type": "agent_end", "messages": [memory_msg, second_assistant]},
            {"type": "agent_settled"},
        ]

        result = run_extract(events)
        self.assertEqual(result.returncode, 3, result.stderr + result.stdout)
        self.assertIn("\ncomplete: yes\n", result.stdout)
        self.assertIn("\nresult: blocked\n", result.stdout)
        self.assertIn("\ntokens: 5051892\n", result.stdout)
        self.assertIn("\ncost usd: 1.321000\n", result.stdout)
        body = result.stdout.split("\n---\n", 1)[1]
        self.assertIn("Nine gaps in the design.", body)
        self.assertEqual(body.rstrip().splitlines()[-1], "RESULT: blocked")
        self.assertNotIn("waiting for the next instruction", body)

    def test_settled_only_stream_keeps_first_result(self):
        first_assistant = {"role": "assistant", "content": [{"type": "text", "text": "RESULT: done\n"}]}
        memory_msg = {
            "role": "custom",
            "customType": "memory-context",
            "content": "<memory>context</memory>",
            "display": False,
        }
        second_assistant = {
            "role": "assistant",
            "content": [{"type": "text", "text": "waiting for the next instruction"}],
        }
        events = [
            {"type": "agent_end", "messages": [first_assistant]},
            {"type": "compaction_start", "reason": "threshold"},
            {"type": "compaction_end", "reason": "threshold", "aborted": False, "willRetry": False},
            {"type": "agent_start"},
            {"type": "agent_end", "messages": [memory_msg, second_assistant]},
        ]
        result = run_extract(events)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertIn("\nresult: done\n", result.stdout)
        body = result.stdout.split("\n---\n", 1)[1]
        self.assertEqual(body.strip(), "RESULT: done")

    def test_reasoning_only_post_compaction_memory_turn_keeps_first_result(self):
        first_assistant = {"role": "assistant", "content": [{"type": "text", "text": "RESULT: done\n"}]}
        memory_msg = {
            "role": "custom",
            "customType": "memory-context",
            "content": "<memory>context</memory>",
            "display": False,
        }
        second_assistant = {"role": "assistant", "content": [{"type": "thinking", "thinking": "idle"}]}
        events = [
            {"type": "agent_end", "messages": [first_assistant]},
            {"type": "compaction_start", "reason": "threshold"},
            {"type": "compaction_end", "reason": "threshold", "aborted": False, "willRetry": False},
            {"type": "agent_start"},
            {"type": "message_end", "message": memory_msg},
            {"type": "message_end", "message": second_assistant},
            {"type": "agent_end", "messages": [memory_msg, second_assistant]},
        ]
        result = run_extract(events)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertIn("\nresult: done\n", result.stdout)
        self.assertNotIn("last assistant turn had no text or tool call", result.stdout)

    def test_post_compaction_turn_with_prompt_supersedes_first_result(self):
        for prompt_msg in (
            {"role": "user", "content": "actually check one more thing"},
            {"role": "custom", "customType": "tillr-goal", "content": "The run is not finished", "display": True},
        ):
            with self.subTest(prompt_msg=prompt_msg):
                first_assistant = {"role": "assistant", "content": [{"type": "text", "text": "RESULT: done\n"}]}
                second_assistant = {"role": "assistant", "content": [{"type": "text", "text": "RESULT: blocked\n"}]}
                events = [
                    {"type": "agent_end", "messages": [first_assistant]},
                    {"type": "compaction_start", "reason": "threshold"},
                    {"type": "compaction_end", "reason": "threshold", "aborted": False, "willRetry": False},
                    {"type": "agent_start"},
                    {"type": "message_end", "message": prompt_msg},
                    {"type": "message_end", "message": second_assistant},
                    {"type": "agent_end", "messages": [prompt_msg, second_assistant]},
                ]
                result = run_extract(events)
                self.assertEqual(result.returncode, 3, result.stderr + result.stdout)
                self.assertIn("\nresult: blocked\n", result.stdout)

    def test_overflow_compaction_with_retry_reports_retried_turn(self):
        first_assistant = {"role": "assistant", "content": [{"type": "text", "text": "RESULT: done\n"}]}
        second_assistant = {"role": "assistant", "content": [{"type": "text", "text": "RESULT: blocked\n"}]}
        events = [
            {"type": "agent_end", "messages": [first_assistant]},
            {"type": "compaction_start", "reason": "overflow"},
            {"type": "compaction_end", "reason": "overflow", "aborted": False, "willRetry": True},
            {"type": "agent_start"},
            {"type": "agent_end", "messages": [second_assistant]},
        ]
        result = run_extract(events)
        self.assertEqual(result.returncode, 3, result.stderr + result.stdout)
        self.assertIn("\nresult: blocked\n", result.stdout)


if __name__ == "__main__":
    unittest.main()
