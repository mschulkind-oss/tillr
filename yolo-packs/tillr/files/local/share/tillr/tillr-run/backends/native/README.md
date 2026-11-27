# The native agent backend — `native`

The first-party Go backend compiled into `tillr-run`. It preserves the same
process-and-record boundary as the external CLI adapters while running the model
turn loop, session transcript, and turn-gap pauses directly in Go (`tillr-run
native-agent`). Reference:
[`docs/design/tillr-native-agent-backend.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-native-agent-backend.md);
the shared adapter contract is
[`docs/reference/tillr-muse-backend.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-muse-backend.md).

| File | What it is |
| :--- | :--- |
| `run-native` | The adapter. Validates shared flags and `preflight_brief`, then `exec`s `tillr-run native-agent`. |
| `extract-report` | The dialect: calls `tillr-run extract-report --dialect native` to write the 19-key `report.md` and `report.md.usage.json`. |
| [`pause-kind`](./pause-kind) | Static pause declaration (`stops-at-a-gap`). |
| [`ending-capability`](./ending-capability) | Static ending declaration (`call-suspends`), requiring no startup `--features` subprocess probe. |

## Adapter contract

Pause kind: `stops-at-a-gap`, declared in [`pause-kind`](./pause-kind).

Ending capability: `call-suspends`, declared in
[`ending-capability`](./ending-capability).

```text
run-native --backend native --model ID [--effort LEVEL]
           [--account SLUG] [--brief PATH] [--target PATH]
           [--timeout SECONDS] [--session-id UUID]
```

- **Harness record:** writes `tillr-native/<revision>` to
  `$TILLR_HARNESS_RECORD_FILE` and leaves `extension-set` absent because no
  external extensions are loaded.
- **Session transcript:** stores provider-neutral JSONL at
  `<artifactDir>/sessions/<session-id>.jsonl`, outside the worktree. On
  `TILLR_SESSION_MODE=resume`, loads the existing transcript, repairs any
  interrupted trailing `tool_use` blocks with synthetic error `tool_result`
  blocks, and appends `$TILLR_RESUME_PROMPT_FILE` when present.
- **Turn-gap pauses:** checks `$TILLR_PAUSE_REQUEST` before opening each model
  turn and after flushing tool results, exiting `0` without emitting
  `agent_settled` so `tillr-run exec` records `StatePaused`.
- **Quota and timeouts:** emits `rate_limit_event` on terminal quota exhaustion
  (read by `extract-report` into `report.md` headers for `RefusedWindow`) and
  assistant error `message_end` events on request timeouts (recorded as
  `run.model-timeout`).
- **Offline testing (slice 1):** live HTTPS provider wire adapters land in
  slice 5. Until then, `native-agent` refuses when `TILLR_NATIVE_MOCK_URL` is
  unset, and accepts only a loopback `http://127.0.0.1` / `http://[::1]` /
  `http://localhost` mock endpoint for offline tests.
