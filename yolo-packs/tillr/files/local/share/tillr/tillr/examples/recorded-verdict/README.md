# Recorded verdict: finish findings without landing

This worked example uses a [definition](../../definition/README.md) (tillr's
placeholder name for the data that selects a store's lifecycle and completion rule).
It runs a command to reproduce a finding, then an agent stage to investigate it,
and finishes through a [hook program](../../definition/README.md#hook-program-contract),
not a Git commit. Here both stages are deterministic stand-ins: no model, network,
product installation, source change, gate, or landing is needed.

## Run from an empty directory

Install the tillr pack's current binaries on `PATH` and Python 3. Start in an empty
directory with no Git repository. The sequence copies the pack's example into that
directory. To use a source checkout instead, set `TILLR_EXAMPLE` to the absolute path
of this example before running the sequence. Use a fresh directory for each run;
it is not a restart or retry tutorial. Do not run it on your production store.

```sh
set -eu
example=${TILLR_EXAMPLE:-$HOME/.local/share/tillr/tillr/examples/recorded-verdict}
cp -R "$example/." .
chmod +x stub-adapter.sh extract-report
python3 prepare.py
tillr store init --store .tillr/state.db --definition definition.json
tillr item apply --store .tillr/state.db --input findings/finding-confirmed.json --now 2026-11-18T00:00:00Z
tillr item apply --store .tillr/state.db --input findings/finding-rejected.json --now 2026-11-18T00:00:00Z
tillr item apply --store .tillr/state.db --input findings/finding-inconclusive.json --now 2026-11-18T00:00:00Z
tillr drain loop --root "$PWD" --store "$PWD/.tillr/state.db" --lanes 1 --trace-bin= --exit-when-drained
tillr item list --store .tillr/state.db
for state in confirmed rejected inconclusive repeat-of tooling-fault; do
    tillr item list --store .tillr/state.db --state "$state"
done
for finding in finding-confirmed finding-rejected finding-inconclusive; do
    tillr item show --store .tillr/state.db --item "$finding"
done
```

The three findings finish respectively as `confirmed`, `rejected`, and
`inconclusive`; each state-filtered list contains one finding. `repeat-of` and
`tooling-fault` are declared finished states, but their lists are empty in this run.
Each shown item has a nonblank `lifecycle_reason`, `lifecycle_by` equal to
`definition:recorded-verdict`, an evidence pointer and an `event_seq`. Each of its
two criteria has a recorded verdict: `done`, `not-done`, or `blocked` respectively.
A finished finding means its investigation ended, not that every criterion passed.

## What the files do

- [definition.json](./definition.json) selects the lifecycle. Every key it uses is
  documented in the definition package's
  [keys, types, defaults, and readers](../../definition/README.md#keys-types-defaults-and-readers):
  `definition`, `version`, `states`, `ready`, `blocked`, `closed`, `finished`,
  `transitions` (including `from` and `to`), `prerequisites`, `completes`,
  `retention`, `brief_template`, and `hooks` (including `argv` and `names`).
  The empty prerequisites array means dispatch needs no named prerequisite.
- [The workflow](./config/tillr/workflows/recorded-verdict@v1.json) orders `reproduce`
  before `investigate`, one command stage followed by one agent stage.
- [prepare.py](./prepare.py) writes local command registration and backend policy
  with absolute program paths, so programs still work from the plain lane directory.
  It neither initializes a store nor invokes Git.
- [stage.py](./stage.py) writes reproduction evidence, then reads it to write the
  assessment. [stub-adapter.sh](./stub-adapter.sh) stands in for an agent backend;
  it does not call an AI provider. [outcomes.json](./outcomes.json) fixes the answers.
  [pause-kind](./pause-kind) declares that interruption stops the stub immediately;
  [extract-report](./extract-report) preserves its plain report without inventing model spend.
- [hook.py](./hook.py) advertises `complete-item`, reads the assessment and the
  current criterion text, records hash-bound verdicts through `tillr item verdict
  record`, and returns the finished state and reason. Its deterministic judge uses
  `actor_kind: mechanical` and `actor: recorded-verdict-stub` for these
  verification-only criteria; this is not evidence of real human approval. The core checks all criteria were
  judged and records the completion event before moving the item.
- [brief-template.md](./brief-template.md) supplies the brief the plain-directory
  staging path requires. The findings carry no prerequisite, dependency, or source fence.

## Consumer surface and limits

The [consumer contract](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-separable-parts.md#21-the-stable-surface)
promises the `tillr` dispatcher spelling and the `store init`, `item apply`,
`item show`, `item verdict`, and `drain loop` faces used here on your own store and
root. `item list` and its `--state` filter work in this example, but that older
contract's explicit face list does not yet include them. Definition selection with
`--definition` and hook completion are documented in the
[definition package](../../definition/README.md); they are the new integration
exercised here, not an expanded stability promise from this example. Python helpers
and example output files are example internals, not consumer contracts.

This example does not yet show real reproduction or model-backed investigation,
`repeat-of` evidence, a `tooling-fault` result, reopening, blocked-input handling,
retries after a hook failure, parallel execution, the detail hook, custom views,
or retention cleanup. Tracing is explicitly disabled so no external trace indexer
is needed. All store, run, lane and evidence files stay in the copied directory;
there is nothing to deploy or land from this run.
