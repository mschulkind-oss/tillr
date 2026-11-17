import importlib.machinery
import importlib.util
import json
import pathlib
import unittest

ROOT = pathlib.Path(__file__).parent


def dialect(name):
    loader = importlib.machinery.SourceFileLoader(name, str(ROOT / name / 'extract-report'))
    module = importlib.util.module_from_spec(importlib.util.spec_from_loader(name, loader))
    loader.exec_module(module)
    return module.dialect


class UsageTest(unittest.TestCase):
    def test_pi_kinds_and_clock(self):
        records = [
            {'type': 'turn_start', 'timestamp': 1000},
            {'type': 'message_update', 'timestamp': 1200, 'assistantMessageEvent': {'type': 'text_delta', 'delta': 'hi'}},
            {'type': 'message_end', 'timestamp': 2200, 'message': {'role': 'assistant'}},
            {'type': 'turn_end', 'timestamp': 2300, 'message': {'role': 'assistant', 'model': 'm', 'usage': {'input': 10, 'output': 20, 'cacheRead': 0}}},
        ]
        detail = dialect('pi')('\n'.join(map(json.dumps, records)))['usage_details']
        model = detail['models'][0]
        self.assertEqual((model['input'], model['output'], model['cache_read']), (10, 20, 0))
        self.assertNotIn('cache_write', model)
        self.assertEqual(detail['first_output_ms'], 200)
        self.assertEqual(detail['output_tokens_per_second'], 20)
        self.assertEqual(detail['turns'][0]['first_output_ms'], 200)

    def test_claude_deduplicates_and_result_kinds(self):
        records = [
            {'type': 'system', 'subtype': 'init', 'model': 'm', 'timestamp': '2026-11-17T00:00:00Z'},
            {'type': 'assistant', 'timestamp': '2026-11-17T00:00:01Z', 'message': {'id': 'a', 'model': 'm', 'usage': {'input_tokens': 2, 'output_tokens': 4}, 'content': [{'type': 'text', 'text': 'hi'}]}},
            {'type': 'assistant', 'timestamp': '2026-11-17T00:00:02Z', 'message': {'id': 'a', 'model': 'm', 'usage': {'input_tokens': 2, 'output_tokens': 4}, 'content': []}},
            {'type': 'result', 'usage': {'input_tokens': 2, 'output_tokens': 4}},
        ]
        detail = dialect('claude')('\n'.join(map(json.dumps, records)))['usage_details']
        self.assertEqual(detail['models'][0]['input'], 2)
        self.assertEqual(len(detail['turns']), 1)
        self.assertNotIn('cache_read', detail['models'][0])
        # An assistant snapshot is not a first streamed token. No invented latency/rate.
        self.assertNotIn('first_output_ms', detail)
        self.assertNotIn('output_tokens_per_second', detail)

    def test_claude_stream_clocks(self):
        records = [
            {'type': 'system', 'subtype': 'init', 'model': 'm', 'timestamp': '2026-11-17T00:00:00Z'},
            {'type': 'stream_event', 'timestamp': '2026-11-17T00:00:00.100Z', 'event': {'type': 'message_start'}},
            {'type': 'stream_event', 'timestamp': '2026-11-17T00:00:00.300Z', 'event': {'type': 'content_block_delta', 'delta': {'type': 'text_delta', 'text': 'hi'}}},
            {'type': 'assistant', 'timestamp': '2026-11-17T00:00:01.300Z', 'message': {'id': 'a', 'model': 'm', 'usage': {'input_tokens': 2, 'output_tokens': 4}}},
            {'type': 'stream_event', 'timestamp': '2026-11-17T00:00:01.300Z', 'event': {'type': 'message_stop'}},
        ]
        detail = dialect('claude')('\n'.join(map(json.dumps, records)))['usage_details']
        self.assertEqual(detail['first_output_ms'], 300)
        self.assertEqual(detail['turns'][0]['first_output_ms'], 200)
        self.assertEqual(detail['output_tokens_per_second'], 4)

    def test_result_only_and_multiple_models(self):
        result = dialect('claude')(json.dumps({'type': 'result', 'usage': {'output_tokens': 0}}))['usage_details']
        self.assertEqual(result['models'][0]['output'], 0)
        self.assertEqual(result['turns'], [])
        records = [{'type': 'turn_end', 'message': {'model': name, 'usage': {'input': 2, 'output': 3, 'cacheRead': 0, 'cacheWrite': 0, 'reasoning': 5, 'totalTokens': 10}}} for name in ['a', 'b']]
        detail = dialect('pi')('\n'.join(map(json.dumps, records)))['usage_details']
        self.assertEqual([m['model'] for m in detail['models']], ['a', 'b'])
        self.assertEqual([m['tokens'] for m in detail['models']], [10, 10])
        self.assertNotIn('first_output_ms', detail)

    def test_missing_first_delta_clock_stays_absent(self):
        records = [
            {'type': 'turn_start', 'timestamp': 1000},
            {'type': 'message_update', 'assistantMessageEvent': {'type': 'text_delta', 'delta': 'first'}},
            {'type': 'message_update', 'timestamp': 1200, 'assistantMessageEvent': {'type': 'text_delta', 'delta': 'later'}},
            {'type': 'message_end', 'timestamp': 2200, 'message': {'role': 'assistant'}},
            {'type': 'turn_end', 'timestamp': 2300, 'message': {'model': 'm', 'usage': {'output': 20}}},
            {'type': 'turn_start', 'timestamp': 3000},
            {'type': 'message_update', 'timestamp': 3200, 'assistantMessageEvent': {'type': 'text_delta', 'delta': 'second turn'}},
            {'type': 'message_end', 'timestamp': 4200, 'message': {'role': 'assistant'}},
            {'type': 'turn_end', 'timestamp': 4300, 'message': {'model': 'm', 'usage': {'output': 20}}},
        ]
        detail = dialect('pi')('\n'.join(map(json.dumps, records)))['usage_details']
        self.assertNotIn('first_output_ms', detail)
        self.assertNotIn('first_output_ms', detail['turns'][0])
        self.assertNotIn('output_tokens_per_second', detail['turns'][0])

    def test_pi_completion_excludes_tools(self):
        records = [
            {'type': 'turn_start', 'timestamp': 1000},
            {'type': 'message_update', 'timestamp': 1200, 'assistantMessageEvent': {'type': 'text_delta', 'delta': 'hi'}},
            {'type': 'message_end', 'timestamp': 2200, 'message': {'role': 'assistant', 'id': 'a'}},
            {'type': 'turn_end', 'timestamp': 62300, 'message': {'role': 'assistant', 'id': 'a', 'model': 'm', 'usage': {'output': 20}}},
        ]
        detail = dialect('pi')('\n'.join(map(json.dumps, records)))['usage_details']
        self.assertEqual(detail['output_tokens_per_second'], 20)
        self.assertEqual(detail['turns'][0]['finished_at_ms'], 2200)

    def test_claude_completion_settles_after_all_repeated_blocks(self):
        records = [
            {'type': 'stream_event', 'timestamp': 1000, 'event': {'type': 'message_start', 'message': {'id': 'a'}}},
            {'type': 'stream_event', 'timestamp': 1200, 'event': {'type': 'content_block_delta', 'delta': {'text': 'hi'}}},
            {'type': 'assistant', 'timestamp': 1500, 'message': {'id': 'a', 'model': 'm', 'usage': {'output_tokens': 20}}},
            {'type': 'assistant', 'timestamp': 2200, 'message': {'id': 'a', 'model': 'm', 'usage': {'output_tokens': 20}}},
            {'type': 'stream_event', 'timestamp': 2400, 'event': {'type': 'message_stop'}},
        ]
        detail = dialect('claude')('\n'.join(map(json.dumps, records)))['usage_details']
        self.assertAlmostEqual(detail['output_tokens_per_second'], 20 / 1.2)
        self.assertEqual(detail['turns'][0]['finished_at_ms'], 2400)
        # A truncated stream is not a completed generation, even with assistant blocks.
        partial = dialect('claude')('\n'.join(map(json.dumps, records[:-1])))['usage_details']
        self.assertNotIn('output_tokens_per_second', partial)
        self.assertNotIn('finished_at_ms', partial['turns'][0])

    def test_missing_usage_is_absent(self):
        for name in ['pi', 'claude']:
            self.assertIsNone(dialect(name)('{}')['usage_details'])


if __name__ == '__main__':
    unittest.main()
