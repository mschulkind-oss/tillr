#!/usr/bin/env python3
import json
import subprocess
import tempfile
import unittest
from pathlib import Path

PROGRAM = Path(__file__).resolve().parent.parent / 'landing-call-held'


def entry(id, parent, message):
    return {'type': 'message', 'id': id, 'parentId': parent, 'message': message}


class LandingCallHeld(unittest.TestCase):
    def test_transcripts(self):
        call = entry('a', None, {'role': 'assistant', 'content': [{'type': 'toolCall', 'id': 'call'}]})
        result = entry('b', 'a', {'role': 'toolResult', 'toolCallId': 'call'})
        other = entry('c', 'b', {'role': 'toolResult', 'toolCallId': 'other'})
        only_other = entry('c', 'a', {'role': 'toolResult', 'toolCallId': 'other'})
        compaction = {'type': 'compaction', 'id': 'c', 'parentId': 'a'}
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'session.jsonl'
            cases = [([call], 0), ([call, result], 0), ([call, result, other], 1),
                     ([call, only_other], 1), ([call, compaction], 1), ('not json', 1), (None, 1)]
            for transcript, expected in cases:
                with self.subTest(transcript=transcript):
                    path.unlink(missing_ok=True)
                    if isinstance(transcript, list):
                        path.write_text('\n'.join(map(json.dumps, [{'type': 'session', 'id': 's'}, *transcript])) + '\n')
                    elif transcript is not None:
                        path.write_text(transcript)
                    self.assertEqual(subprocess.run([PROGRAM, path, 'call']).returncode, expected)

    def test_branch_and_invalid_input_fail_closed(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'session.jsonl'
            call = entry('a', None, {'role': 'assistant', 'content': [{'type': 'toolCall', 'id': 'call'}]})
            other = entry('b', None, {'role': 'assistant', 'content': []})
            for records in ([call, other], [call, {'type': 'message', 'id': 'b', 'parentId': 'missing'}]):
                path.write_text('\n'.join(map(json.dumps, [{'type': 'session', 'id': 's'}, *records])) + '\n')
                self.assertEqual(subprocess.run([PROGRAM, path, 'call']).returncode, 1)
            path.write_text(json.dumps({'type': 'session', 'id': 's'}) + '\n' + json.dumps(call) + '\n{broken\n')
            self.assertEqual(subprocess.run([PROGRAM, path, 'call']).returncode, 1)


if __name__ == '__main__':
    unittest.main()
