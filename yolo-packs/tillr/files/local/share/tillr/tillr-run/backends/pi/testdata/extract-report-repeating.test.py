#!/usr/bin/env python3
"""A session pi ended for a repeating call is reported as that, not as a missing footer.

The streams are cut from the two runs of 2026-11-19 that repeated one read hundreds of
times, followed by the `loop_detected` event pi emits when it ends such a session.
"""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
EXTRACT = HERE.parent / "extract-report"
STREAMS = {
    "repeating-call-request-b13084fe3e.jsonl": "tools/tillr-drain/drain/pass.go",
    "repeating-call-landing-call-delivery-tests.jsonl": "tools/tillr-drain/drain/landing_call_test.go",
}


def extract(stream):
    return subprocess.run([str(EXTRACT), "--backend", "pi", "--exit-code", "0", str(stream)], text=True, capture_output=True)


class RepeatingCallReportTest(unittest.TestCase):
    def test_the_report_names_the_ending_and_exits_six(self):
        for name, path in STREAMS.items():
            with self.subTest(name):
                result = extract(HERE / name)
                self.assertEqual(result.returncode, 6, result.stderr)
                self.assertIn("ENDED BY THE HARNESS", result.stderr)
                self.assertNotIn("INCOMPLETE", result.stderr)
                self.assertIn("\nresult: ended by the harness: repeating-call\n", result.stdout)
                self.assertIn("complete: no\n", result.stdout)
                last = result.stdout.rstrip().splitlines()[-1]
                self.assertIn("repeated one identical call 6 times in a row: read", last)
                self.assertIn(path, last)

    def test_without_the_event_the_same_calls_are_an_incomplete_report(self):
        name = next(iter(STREAMS))
        lines = [line for line in (HERE / name).read_text().splitlines() if json.loads(line)["type"] != "loop_detected"]
        with tempfile.TemporaryDirectory() as directory:
            stream = Path(directory) / "stream"
            stream.write_text("\n".join(lines) + "\n")
            result = extract(stream)
        self.assertEqual(result.returncode, 4, result.stderr)
        self.assertIn("\nresult: missing\n", result.stdout)

    def test_the_ending_is_the_harness_even_after_an_earlier_footer(self):
        # pi's event is the last word on how the session ended: a footer the model wrote
        # in an earlier turn is not this session's answer.
        name = next(iter(STREAMS))
        events = [json.loads(line) for line in (HERE / name).read_text().splitlines()]
        events.insert(1, {"type": "agent_end", "messages": [{"role": "assistant", "content": [{"type": "text", "text": "RESULT: done"}]}]})
        with tempfile.TemporaryDirectory() as directory:
            stream = Path(directory) / "stream"
            stream.write_text("".join(json.dumps(event) + "\n" for event in events))
            result = extract(stream)
        self.assertEqual(result.returncode, 6, result.stderr)
        self.assertIn("\nresult: ended by the harness: repeating-call\n", result.stdout)


if __name__ == "__main__":
    unittest.main()
