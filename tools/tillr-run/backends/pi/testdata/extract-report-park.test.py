#!/usr/bin/env python3
"""Earlier footers cannot stand in for a parked or empty final turn."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

EXTRACT = Path(__file__).resolve().parent.parent / "extract-report"


class ParkedProbeTest(unittest.TestCase):
    def test_commit_offer_supplies_the_footer_without_a_summary(self):
        for legacy in ({}, {"summary": "I ran every check (unverified claim)."}):
            with self.subTest(legacy=legacy), tempfile.TemporaryDirectory() as directory:
                offer = Path(directory) / "landing-offer.json"
                offer.write_text(json.dumps({"call_id": "call-1", "base": "base", "commit": "head", **legacy}))
                report = subprocess.run(
                    [str(EXTRACT), "--exit-code", "0", "--landing-offer", str(offer)],
                    input="", text=True, capture_output=True,
                )
                self.assertEqual(report.returncode, 0, report.stderr)
                self.assertEqual(report.stdout.split("\n---\n", 1)[1].strip(), "RESULT: done")

    def test_reasoning_only_last_turn_is_not_a_verdict(self):
        earlier = {"role": "assistant", "content": [{"type": "text", "text": "RESULT: done"}]}
        final = {"role": "assistant", "content": [{"type": "thinking", "thinking": "private"}]}
        for ending in (
            [{"type": "agent_end", "messages": [earlier, final]}],
            [{"type": "message_end", "message": final}],
            [{"type": "message", "message": final}],
        ):
            with self.subTest(ending=ending):
                events = [
                    {"type": "message_update", "message": earlier, "assistantMessageEvent": {"type": "text_delta", "delta": "RESULT: done"}},
                    *ending,
                ]
                stream = "".join(json.dumps(event) + "\n" for event in events)
                report = subprocess.run([str(EXTRACT), "--exit-code", "0"], input=stream, text=True, capture_output=True)
                self.assertEqual(report.returncode, 4, report.stderr)
                self.assertIn("result: missing", report.stdout)
                self.assertIn("last assistant turn had no text or tool call", report.stdout)
                self.assertNotIn("RESULT: done", report.stdout)
                self.assertNotIn("private", report.stdout)

    def test_stale_footer_is_not_a_live_verdict(self):
        events = [
            {"type": "agent_settled"},
            {"type": "agent_end", "messages": [{"role": "assistant", "content": [{"type": "text", "text": "RESULT: done"}]}]},
            {"type": "parked", "toolCallId": "call-1"},
            {"type": "agent_end", "messages": []},
        ]
        with tempfile.TemporaryDirectory() as directory:
            stream = Path(directory) / "stream"
            stream.write_text("".join(json.dumps(event) + "\n" for event in events))
            live = subprocess.run([str(EXTRACT), "--exit-code", "unknown", str(stream)], text=True, capture_output=True)
            final = subprocess.run([str(EXTRACT), "--exit-code", "0", str(stream)], text=True, capture_output=True)
        self.assertEqual(live.returncode, 4, live.stderr)
        self.assertNotEqual(live.stdout.rstrip().splitlines()[-1], "RESULT: done")
        self.assertEqual(final.returncode, 0, final.stderr)
        self.assertEqual(final.stdout.rstrip().splitlines()[-1], "RESULT: done")


if __name__ == "__main__":
    unittest.main()
