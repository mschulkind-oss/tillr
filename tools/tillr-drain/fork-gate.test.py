#!/usr/bin/env python3
"""Parser and declaration contracts; invoked by the drain's Go suite."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('fork_gate', Path(__file__).with_name('fork-gate.py'))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class ForkBuildContracts(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(dir=os.environ['FORK_GATE_TEST_SCRATCH'])
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)

    def pack(self, name, contribution):
        path = self.root / 'yolo-packs' / name / 'pack.json'
        path.parent.mkdir(parents=True)
        path.write_text(json.dumps({'contributes': [contribution]}))
        return path

    def test_jsonc_preserves_quoted_comments_and_removes_trailing_commas(self):
        path = self.root / 'pack.json'
        path.write_text('''{
          // comment
          "url": "https://example.invalid/", /* another comment */
          "literal": "/*not a comment*/,}",
          "array": [1, 2,],
        }''')
        self.assertEqual(gate.jsonc(path), {
            'url': 'https://example.invalid/', 'literal': '/*not a comment*/,}', 'array': [1, 2]})

    def test_declared_command_is_not_replaced_by_an_npm_guess(self):
        path = self.pack('fork', {'patch_branch': 'git+https://example.invalid/repo?ref=patch%2Fpi',
                                 'build': 'node scripts/fork-build.mjs'})
        self.assertEqual(gate.build_command(self.root, 'pi'), (path, 'node scripts/fork-build.mjs'))

    def test_source_only_delivery_has_an_explicit_check(self):
        path = self.pack('source', {'patch_branch': 'git+https://example.invalid/repo?ref=patch/pi'})
        found, command = gate.build_command(self.root, 'pi')
        self.assertEqual(found, path)
        self.assertEqual(command, 'git diff --exit-code HEAD && git diff --cached --exit-code')

    def test_no_delivery_is_an_error(self):
        with self.assertRaisesRegex(ValueError, 'found 0'):
            gate.build_command(self.root, 'pi')

    def test_ambiguous_delivery_is_an_error(self):
        for name in ('one', 'two'):
            self.pack(name, {'patch_branch': 'git+https://example.invalid/repo?ref=patch/pi'})
        with self.assertRaisesRegex(ValueError, 'found 2'):
            gate.build_command(self.root, 'pi')


if __name__ == '__main__':
    unittest.main()
