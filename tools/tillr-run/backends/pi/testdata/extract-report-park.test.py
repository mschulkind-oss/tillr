#!/usr/bin/env python3
"""A stale footer cannot make the runner's live probe end a parked call."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

EXTRACT = Path(__file__).resolve().parent.parent / "extract-report"


class ParkedProbeTest(unittest.TestCase):
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
