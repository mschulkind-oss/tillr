"""No model process may be needed to read subscription quota."""
import io
import json
import os
from pathlib import Path
import runpy
import tempfile
import unittest
from unittest.mock import patch, Mock


class QuotaTests(unittest.TestCase):
    def setUp(self):
        self.module = runpy.run_path(str(Path(__file__).with_name("quota")))

    def test_direct_request_normalizes_both_windows_without_model_call(self):
        payload = {"five_hour": {"utilization": 80, "resets_at": "2026-11-18T01:00:00Z"},
                   "seven_day": {"utilization": 33.5, "resets_at": "2026-11-24T01:00:00Z"}}
        response = io.BytesIO(json.dumps(payload).encode())
        with patch.dict(os.environ, {"CLAUDE_CODE_OAUTH_TOKEN": "fixture-secret"}, clear=True), \
             patch("urllib.request.build_opener", return_value=Mock(open=Mock(return_value=response))) as build:
            result = self.module["read_quota"]()
        request = build.return_value.open.call_args.args[0]
        self.assertEqual(request.get_method(), "GET")
        self.assertEqual(request.full_url, "https://api.anthropic.com/api/oauth/usage")
        self.assertEqual(request.get_header("Authorization"), "Bearer fixture-secret")
        self.assertEqual(request.get_header("Anthropic-beta"), "oauth-2025-04-20")
        self.assertEqual(result["windows"], [
            {"kind": "five_hour", "fraction": .8, "reset_at": "2026-11-18T01:00:00Z"},
            {"kind": "seven_day", "fraction": .335, "reset_at": "2026-11-24T01:00:00Z"}])

    def test_reads_existing_secure_storage_without_refreshing_it(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / ".credentials.json"
            contents = json.dumps({"claudeAiOauth": {"accessToken": "fixture-token", "refreshToken": "never-spend"}})
            path.write_text(contents)
            with patch.dict(os.environ, {"CLAUDE_SECURESTORAGE_CONFIG_DIR": root}, clear=True):
                self.assertEqual(self.module["access_token"](), "fixture-token")
            self.assertEqual(path.read_text(), contents)

    def test_missing_auth_is_an_error_not_a_model_probe(self):
        with tempfile.TemporaryDirectory() as root, patch.dict(os.environ, {"HOME": root}, clear=True):
            with self.assertRaisesRegex(ValueError, "subscription access token"):
                self.module["access_token"]()

    def test_bad_and_unknown_windows_are_refused(self):
        normalize = self.module["normalize"]
        for payload in [{}, {"five_hour": None, "seven_day": None},
                        {"five_hour": {"utilization": 0, "resets_at": None}},
                        {"five_hour": {"utilization": -1, "resets_at": "2026-11-18T01:00:00Z"}},
                        {"five_hour": {"utilization": True, "resets_at": "2026-11-18T01:00:00Z"}},
                        {"five_hour": {"utilization": 80, "resets_at": "bad"}},
                        {"five_hour": {"utilization": 80}},
                        {"five_hour": {"utilization": float("nan"), "resets_at": "2026-11-18T01:00:00Z"}}]:
            with self.subTest(payload=payload), self.assertRaises(ValueError):
                normalize(payload)
        # Exercise window validation separately from missing-key validation.
        for used, reset in [(True, "2026-11-18T01:00:00Z"), (-1, None), (float("nan"), "2026-11-18T01:00:00Z"), (80, "bad"), (80, None)]:
            with self.subTest(used=used, reset=reset), self.assertRaises(ValueError):
                normalize({"five_hour": {"utilization": used, "resets_at": reset}, "seven_day": None})

    def test_null_window_is_omitted_not_invented(self):
        result = self.module["normalize"]({"five_hour": None, "seven_day": {"utilization": 10, "resets_at": "2026-11-24T01:00:00Z"}})
        self.assertEqual(len(result["windows"]), 1)
        self.assertEqual(result["windows"][0]["kind"], "seven_day")

    def test_idle_window_has_no_invented_reset(self):
        result = self.module["normalize"]({"five_hour": {"utilization": 0, "resets_at": None}, "seven_day": None})
        self.assertEqual(result["windows"], [{"kind": "five_hour", "fraction": 0, "reset_at": None}])

    def test_does_not_redirect_the_bearer_token(self):
        handler = self.module["NoRedirect"]()
        with self.assertRaisesRegex(ValueError, "redirect"):
            handler.redirect_request(None, None, 302, "moved", {}, "https://other.invalid")


if __name__ == "__main__":
    unittest.main()
