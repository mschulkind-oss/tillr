---
title: "Test runner adapters: listing, selecting, and reading individual tests across tillr"
status: in-review
stage: DESIGN
updated: 2026-11-28
tags:
  - tillr
  - tillr-gate
  - tillr-drain
  - tillr-item
  - gate
  - adapters
  - testing
  - design
summary: "Defines the subprocess, exit-code, and versioned record contract (`gate.adapter.*`, `record_version: 1`) through which `tillr-gate` enumerates tests, runs a chosen subset or exclusion list, and reads per-test verdicts and failure slices for any test runner; specifies the first two built-in adapters (`go-test` and `vitest`, keeping adapter-less checks as opaque commands); maps every place tillr runs tests (`tillr-gate`, base replay, trunk proof, `verify`, split closing checks, trunk fixers, and test repair items); draws a non-overlapping decision boundary with `trunk-proof-per-test-flaky-judgment.md`; and sequences independent build slices alongside three open owner questions (`OQ-TRA1`–`OQ-TRA3`)."
next: "Owner review of open questions `OQ-TRA1`–`OQ-TRA3` and designer decisions `D-TRA1`–`D-TRA7`; Slices 1–3 (`tillr-gate` contract, `go-test` adapter, and `vitest` adapter library) do not depend on `OQ-TRA1`–`OQ-TRA3`."
depends-on:
  - landing-principles.md
  - trunk-proof-per-test-flaky-judgment.md
  - tillr-utility-contracts.md
  - tillr-utility-contracts/tillr-gate.md
  - gate-test-caching.md
  - fast-gate-same-tool-proof-misses.md
  - tillr-fixer-case-match-on-green-base.md
  - tillr-item-split.md
  - installing-tillr-outside-yolo.md
  - ../reference/gate.md
  - ../reference/gate-check-rules.md
  - ../tillr/decision-provenance-review.md
vantage:
  status-chip: true
---

# Test runner adapters: listing, selecting, and reading individual tests across tillr

> **Decision provenance ([`D-PR1`](../tillr/decision-provenance-review.md#decision-ledger), [`D-PR2`](../tillr/decision-provenance-review.md#decision-ledger), [`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21), [`OQ-PR22`](../tillr/decision-provenance-review.md#OQ-PR22)).**
>
> - **Needs the owner's ruling before dependent slices run:** **3 open questions** — [`OQ-TRA1`](#OQ-TRA1) (whether a structured test selection satisfies test-backed verification mechanically or still invokes an LLM `verify` turn), [`OQ-TRA2`](#OQ-TRA2) (whether front-end `vitest` and `tsc --noEmit` join the pre-merge change gate or stay in the after-merge heavy proof), and [`OQ-TRA3`](#OQ-TRA3) (which runner adapters ship built into `tillr-gate` beyond `go-test` and `vitest` versus living in consumer repositories via `.tillr.toml`).
> - **Designer's proposals the owner may overturn:** **7 decisions** (`D-TRA1`–`D-TRA7` in the [Decision Ledger](#decision-ledger)). None is presented as an owner ruling.
> - **Owner rulings quoted in this document:** Every verbatim quotation is cited to its dated ruling in [`tillr-utility-contracts.md`](./tillr-utility-contracts.md), [`gate-test-caching.md`](./gate-test-caching.md), [`docs/reference/gate.md`](../reference/gate.md), [`docs/reference/gate-check-rules.md`](../reference/gate-check-rules.md), [`tillr-item-split.md`](./tillr-item-split.md), or `R-TW-DR2`. The 2026-11-27 request prompt is recorded below as operator-filed direction (`D-PR1`), not as a verbatim transcript of the owner's speech.

---

## 1. Direction and what exists in the tree today

### 1.1 Operator-recorded owner direction (2026-11-27)

On 2026-11-27, alongside the companion trunk-proof design ([`trunk-proof-per-test-flaky-judgment.md`](./trunk-proof-per-test-flaky-judgment.md)), the operator filed a request recording the owner's architectural direction that tillr sits on top of test runners it did not write — `go test`, `pytest`, `vitest`, `cargo test`, JUnit-style suites — and needs one contract across them so that wherever tillr runs tests, it can list individual tests, run a selected set, and read each test's result and failure output in one structured record format rather than treating test checks as opaque shell commands or hard-coding Go-only log parsers.

Two existing owner rulings bound the shape of that contract from the outset:

- **Utility boundaries (`2026-11-21` owner ruling in [`tillr-utility-contracts.md` Section 1](./tillr-utility-contracts.md#1-the-ruling)):**
  > "Build tillr as a set of Unix utilities with clear, strong boundaries and contracts — CLI flags, exit codes, versioned records, table ownership. Not a monolith. Pin the contracts with contract examples so that when one side changes, tests fail on both sides before landing."
- **No fallbacks (`R-TW-DR2`):**
  > "Explicitly, we NEVER want fallbacks anywhere in this project"

### 1.2 What exists on `HEAD` (`11d7df6566ca`) and where Go-specific reach-ins sit

Measured on `11d7df6566ca` across [`tools/gate/checks/`](../../tools/gate/checks/) (`ls -1 tools/gate/checks/*.json | wc -l` = `45` check declarations):

1. **Check declarations have no adapter concept.** `parseCheckDeclaration` in [`tools/tillr-gate/checkfiles.go`](../../tools/tillr-gate/checkfiles.go) decodes a fixed 12-field struct (`id`, `label`, `command`, `scope`, `tier`, `budget_ms`, `timeout_ms`, `reads`, `tools`, `needs`, `remember`, `writes`, `exclusive`) with `decoder.DisallowUnknownFields()`; `rg 'Adapter' tools/tillr-gate/checkfiles.go` returns nothing. Every check is an opaque `command` argv.
2. **Test execution and failure reading are split across three Go-only paths and four log-scraping regexes:**
   - **Inside `tools/tillr-gate`:** [`tools/tillr-gate/shards/`](../../tools/tillr-gate/shards/) (`shards.Run`) runs `12` `go-test-*` module checks across duration-balanced `go test -json` shards, and [`tools/tillr-gate/goaffected/`](../../tools/tillr-gate/goaffected/) (`goaffected.Run`) selects affected Go packages for `go-affected.json` and prints human `fork-check: triage --` lines to standard output. Six other Go test checks (`go-test-tillr-cli.json`, `observatory-dev.json`, `format-test.json`, `commit-identity-test.json`, `gate-adapters-test.json`, `change-checks-test.json`, `tree-checks-test.json`, `pack-sync-test.json`, `gate-budget-test.json`, `go-shard-helpers-test.json`) bypass both packages and invoke raw `sh -c "... go test ..."` or `["go", "test", "-count=1", "tests/observatory_dev_test.go"]`.
   - **Second Go runner inside `tools/tillr-drain`:** `JudgeHeavyGo` and `GoSuiteFailures` in [`tools/tillr-drain/internal/gatespans/heavy_go.go`](../../tools/tillr-drain/internal/gatespans/heavy_go.go) parse `<run-dir>/gate/logs/<id>.log` (or re-invoke `go test -json ./...` directly) to synthesize `go-test:<module>:<pkg>:<TestName>` check IDs, and `ReplayGoCheck` / `RunNamedGoTestsInReplay` in [`tools/tillr-drain/internal/gatespans/replay_go_affected.go`](../../tools/tillr-drain/internal/gatespans/replay_go_affected.go) build a third `go test -run` invocation for base-SHA replay.
   - **Log-scraping regexes across `tools/tillr-drain`:** `readTriageBlock` in [`tools/tillr-drain/internal/runhistory/refusal_cases.go`](../../tools/tillr-drain/internal/runhistory/refusal_cases.go) scrapes `fork-check: triage --` text from `<check>.log`; `failingTestsFromLog` in [`tools/tillr-drain/drain/test_fix_hold.go`](../../tools/tillr-drain/drain/test_fix_hold.go) scrapes `--- FAIL:`, Python `unittestFailRe`, and `pytestFailRe` lines out of raw check logs; and `heavyCheckTestNames` in [`tools/tillr-drain/drain/heavy_link.go`](../../tools/tillr-drain/drain/heavy_link.go) calls `failingTestsFromLog` and searches open items' English `title` and `prose` (`findOpenCheckFixer`) because items carry no structured test selection (`rg 'test_selection' tools/tillr-drain/ tools/tillr-item/` returns nothing).
3. **Front-end tests (`observatory-web`) are completely opaque to tillr.** [`tools/gate/checks/observatory-web.json`](../../tools/gate/checks/observatory-web.json) runs `sh -c "cd tools/tillr-view/web && { [ -d node_modules ] || npm ci --no-audit --no-fund; } && npm run typecheck && npm run test"` as a single `scope: "tree", tier: "heavy"` command covering `tsc --noEmit` plus `70` Vitest test files (`fd '\.test\.tsx?$' tools/tillr-view/web/tests | wc -l` = `70`, `74` total files in `tests/` including fixtures) under `vitest@^2.1.8` ([`tools/tillr-view/web/package.json`](../../tools/tillr-view/web/package.json)); `rg 'vitest' tools/tillr-gate/` returns nothing. A single broken component assertion fails the entire `observatory-web` check, replays the entire `tsc` + 70-file suite on `base_sha` and during `FindFirstRed` culprit bisection, and files a whole-check `fixer-main-<sha>` item with no test selector.

---

## 2. The adapter contract (`D-TRA1`–`D-TRA3`)

### 2.1 Boundary: what an adapter owns vs. what `tillr-gate` owns (`D-TRA1`)

Under [`D-GR23`](../reference/gate.md#D-GR23) and [`D-GR28`](../reference/gate.md#D-GR28) (*"One function classifies every check result"*), an adapter is a **runner translator**, never a gate classifier or store writer:

- **The adapter owns:** discovering tests in its runner (`list`), computing runner-native change-affected suites/tests when asked (`select`), invoking the underlying test runner for all, included, or excluded tests (`run`), isolating runner temp state inside the provided `scratch_dir`, and writing one versioned `record.Envelope` (`record_version: 1`) containing structured per-test observations (`pass`, `fail`, `skipped`, `absent`), failure kinds, source locations, and sliced per-test failure output.
- **`tillr-gate` owns:** reading `<checks_dir>/*.json` and `.tillr.toml`, computing tree-scoped and per-test input fingerprints (`memory.go`), passing active disabled test selectors from `<store>/disabled/*.json` as `exclude_tests`, enforcing wall-clock `budget_ms` / `timeout_ms` and the 60-sample silent-hang watchdog (`watchdog.go`, [`D-GR16`](../reference/gate.md#D-GR16)), feeding each test observation through `checkresult.Classify` ([`tools/tillr/checkresult/classify.go`](../../tools/tillr/checkresult/classify.go)), writing contradiction markers (`disabled/*.json`), and emitting `gate.plan` and `gate.verdict` (plus a structured `gate.test-report` reference on each adapted `VerdictCheck`).
- **Standard error and raw logs are never contract ([`tillr-utility-contracts.md` Section 4](./tillr-utility-contracts.md#4-the-six-rules), P5).** Human-readable runner output streams to `<logs>/<check-id>.log` (`VerdictCheck.LogRef`) for humans and worker briefs; every tillr consumer (`tillr-gate`, `tillr-drain`, `tillr-loop`, `tillr-item`, `tillr-view`) reads only the versioned JSON envelopes.

### 2.2 Cross-run and cross-commit test identity (`D-TRA2`)

A test identifier must survive line insertions above the test, shard re-balancing, execution in different lane worktrees, and execution of the same test by both a change-scoped lane check (`go-affected`) and a tree-scoped proof check (`go-test-tillr-view`). At the same time, it must not assume Go syntax: Vitest, Jest, Mocha, and RSpec nest arbitrary `describe` blocks whose titles contain spaces, slashes (`"renders 0/5 shards"`), colons, and regex metacharacters.

Every test is identified across the wire by a structured `TestID` object (`D-TRA2`, **designer's decision; the owner may overturn this**):

```json
{
  "adapter": "vitest",
  "suite": "tools/tillr-view/web:tests/alerts.test.tsx",
  "case": ["AlertsPage", "renders 0/5 shards when linked item is open"]
}
```

| Field | Type | Invariant across runs and commits |
| :--- | :--- | :--- |
| `adapter` | `string` | Lowercase ASCII `[a-z0-9-]+` naming the adapter semantics (`"go-test"`, `"vitest"`, `"pytest"`, ...). |
| `suite` | `string` | Repository-root-relative runnable container, using forward slashes and never containing worktree absolute paths, shard numbers, or line numbers. For `go-test` module packages: `"<module-rel-dir>:<go-import-path>"` (for example `"tools/tillr-drain:github.com/mschulkind-oss/tillr-drain/drain"`); for `go-test` file checks: `"file:<repo-rel-file>"` (for example `"file:tests/observatory_dev_test.go"`, never Go's synthetic `"command-line-arguments"`); for `vitest`: `"<workdir-rel>:<test-file-rel>"` (for example `"tools/tillr-view/web:tests/alerts.test.tsx"`). |
| `case` | `[]string` | Non-empty ordered hierarchy from top-level test or `describe` block down to the leaf test case (for example `["TestWhyAfterRestartOverLiveEvaluator", "resumed_segment"]` in Go; `["AlertsPage", "renders open alert episodes"]` in Vitest). Hierarchy checks (`isAncestor`) compare slice prefixes, never splitting strings on `/` or `>`. |
| `variant` | `string` (optional, omitted when `""`) | Present only when a repository intentionally declares two checks that run the same `suite` and `case` under incompatible runtime matrices (for example `"browser"` vs. `"jsdom"`). Omitted by default so a change-scoped check (`go-affected`) and a tree-scoped check (`go-test-tillr-drain`) produce the identical `TestID`. |

**Canonical string key (`test_key`) and exact compatibility with [`D-TPF1`](./trunk-proof-per-test-flaky-judgment.md#decision-ledger):**

Where a single deterministic string key is required — `<store>/disabled/<sha256(test_key)>.json`, `checkresult.Classify`, CLI flags, and compact event fields — `FormatTestKey(id TestID)` produces:

- When `id.Adapter == "go-test"` and `id.Variant == ""` and no `case` segment contains `:` or `/`: `go-test:<suite>:< strings.Join(case, "/") >` — **byte-for-byte identical** to `go-test:<module>:<pkg>:<TestName>` in `JudgeHeavyGo` ([`tools/tillr-drain/internal/gatespans/heavy_go.go`](../../tools/tillr-drain/internal/gatespans/heavy_go.go)) and `D-TPF1` in [`trunk-proof-per-test-flaky-judgment.md` Section 2.1](./trunk-proof-per-test-flaky-judgment.md#21-the-per-test-check-selector-d-tpf1-subject-to-oq-tpf2).
- For every other adapter (or any Go subtest whose segment itself contains `/` or `:`): `"test:" + compactCanonicalJSON(id)` (RFC 8785 key order `["adapter","suite","case","variant"]`). No caller ever parses `test_key` with regexes when the structured `TestID` is on the record; `ParseTestKey` accepts both the `go-test:` form and the `"test:{...}"` form losslessly.

### 2.3 Processes, CLI flags, and exit codes (`D-TRA1`)

`tillr-gate` invokes any adapter (built-in or repository-declared) as a child process with `cmd.Dir` set to the checked-out repository root (`worktree`), `GIT_CEILING_DIRECTORIES` bounding Git discovery to the worktree, and a check-private `scratch_dir` (`TMPDIR` pointed inside it so sibling checks cannot collide, [Landing Principle 14](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)):

```text
<adapter-argv...> list   --request <req.json> --output <out.json>
<adapter-argv...> select --request <req.json> --output <out.json>
<adapter-argv...> run    --request <req.json> --output <out.json> [--events <events.jsonl>]
```

- `--request <path>` points to a `record.Envelope` (`record_version: 1`) written by `tillr-gate` before spawn. Passing selections in a file avoids OS `ARG_MAX` limits when `include` or `exclude_tests` carries hundreds of `TestID`s and avoids all shell/regex quoting across process boundaries.
- `--output <path>` is written atomically (`write temp + rename`) by the adapter before exit.
- `--events <path>` (optional, passed by `tillr-gate` on `run`) is an append-only JSONL stream of `gate.adapter.event` envelopes (`test-started`, `test-finished`, `progress`). Each flushed line advances the byte counter watched by `watchSources` in [`tools/tillr-gate/watchdog.go`](../../tools/tillr-gate/watchdog.go) and feeds live `completed / total` counts to `tillr-view`, while preserving already-completed per-test observations if a later test hangs (`ExitStalled = 6`) or a trunk proof cancels on a new tip.

**Exit-code contract** — aligned with [`tools/tillr/record/exit.go`](../../tools/tillr/record/exit.go) and [`D-GR15`](../reference/gate.md#D-GR15)/[`D-GR16`](../reference/gate.md#D-GR16):

| Exit code | Constant | Meaning | How `tillr-gate` classifies it |
| :--- | :--- | :--- | :--- |
| `0` | `record.ExitOK` | `list` or `select` succeeded; or `run` completed with `0` failed non-excluded tests, `0` `unattributed_errors`, and (when `require_present: true`) `0` `absent` / `skipped` targets. Valid result envelope at `--output`. | Feeds per-test observations to `checkresult.Classify`; if `--output` is missing or invalid, upgrades to `ResultError` (exit `4` `"could not judge"`), never `ResultPass`. |
| `1` | `record.ExitNegative` | `run` completed and observed at least one `outcome: "fail"`, non-empty `unattributed_errors` (compile or suite-setup break), or `absent`/`skipped` when `require_present: true`. Valid `gate.adapter.run-result` at `--output`. | Feeds per-test and suite-error observations to `checkresult.Classify` -> `fail` (or `disabled` on same-fingerprint contradiction per [`D-GR21`](../reference/gate.md#D-GR21)). |
| `2` | `record.ExitUsage` | Invalid CLI flags or malformed request JSON. | `ResultError` -> `tillr-gate` exits `4` (`unknown` / `"could not judge"`), never convicting the candidate ([Landing Principle 1](./landing-principles.md#1-a-refusal-must-be-a-verdict)). |
| `3` | `record.ExitIncompatible` | Unsupported `record_version` (`record.ErrUnsupportedVersion`) or unsupported operation (for example `select` on an adapter that implements only `list` and `run`). | `ResultError` -> `tillr-gate` exits `4` (`"could not judge"`). |
| `4` | `record.ExitExternal` | Missing runner dependency (`go`, `node`, `vitest`, or unbootstrapped `node_modules`), unreadable worktree, or environment fault outside the code under test. | `ResultError` -> `tillr-gate` exits `4` (`unavailable` / `unknown`, [Landing Principle 8](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)). |
| `5` | `record.ExitInternal` | Adapter internal panic or bug. | `ResultError` -> `tillr-gate` exits `4` (`"could not judge"`). |
| `6` | `ExitStalled` | Adapter's internal progress monitor (or `tillr-gate`'s outer `watchdog.go`) killed a hung test child after 60 silent samples. | `ResultStalled` -> `tillr-gate` exits `6`; completed `gate.adapter.event` lines before the stall remain readable for diagnostics, while stalled tests are never retried as flakes ([`D-GR16`](../reference/gate.md#D-GR16)). |

### 2.4 Versioned record schemas (`record_version: 1`, `D-TRA1`)

Every file at `--request`, `--output`, and `--events` uses [`record.Envelope`](../../tools/tillr/record/envelope.go) (`record_version: 1`, `producer.system: "tillr"`).

1. **`list` (`gate.adapter.list-request` -> `gate.adapter.list-result`):**
   - Request payload: `{ "check_id": string, "worktree": string, "workdir": string, "config": object, "suites"?: []string }`.
   - Result payload:
     ```json
     {
       "check_id": "observatory-web",
       "adapter": "vitest",
       "tests": [
         {
           "id": {
             "adapter": "vitest",
             "suite": "tools/tillr-view/web:tests/alerts.test.tsx",
             "case": ["AlertsPage", "renders open alert episodes"]
           },
           "file": "tools/tillr-view/web/tests/alerts.test.tsx"
         }
       ]
     }
     ```
     `tests` is sorted canonically by `(id.suite, id.case)` so contract examples under `tools/tillr-gate/contract/` are deterministic ([`tillr-utility-contracts.md` Section 6](./tillr-utility-contracts.md#6-contract-examples)).

2. **`select` (`gate.adapter.select-request` -> `gate.adapter.select-result`, optional — required only when a check sets `"scope": "change"`):**
   - Request payload: `{ "check_id": string, "worktree": string, "workdir": string, "config": object, "base_sha": string, "head_sha": string, "changed_paths": []string }`.
   - Result payload: `{ "check_id": string, "adapter": string, "selection_ms": int64, "none": bool, "selected_suites": []string, "selected_tests"?: []TestID, "deferred_suites"?: []string }`. When `none` is `true`, `tillr-gate` records `pass` (`0` affected tests) without spawning the runner.

3. **`run` (`gate.adapter.run-request` -> `gate.adapter.run-result`):**
   - Request payload:
     ```json
     {
       "check_id": "observatory-web",
       "worktree": "/abs/path/to/checkout",
       "workdir": "tools/tillr-view/web",
       "scratch_dir": "/abs/path/to/scratch/observatory-web",
       "config": { "config_file": "vitest.config.ts" },
       "mode": "include",
       "tests": [
         {
           "adapter": "vitest",
           "suite": "tools/tillr-view/web:tests/alerts.test.tsx",
           "case": ["AlertsPage", "renders open alert episodes"]
         }
       ],
       "exclude_tests": [],
       "prior_failed": [],
       "require_present": true,
       "cache": false,
       "race": false,
       "repeat": 1,
       "max_workers": 6
     }
     ```
     Closed `mode` Name Set: `"all"` (every test in the check, or in `suites` narrowed by a preceding `select`, minus `exclude_tests`) and `"include"` (only `tests`, minus `exclude_tests`).
   - Result payload — **one record format across every runner and every call site**:
     ```json
     {
       "check_id": "observatory-web",
       "adapter": "vitest",
       "status": "fail",
       "duration_ms": 612,
       "tests": [
         {
           "id": {
             "adapter": "vitest",
             "suite": "tools/tillr-view/web:tests/alerts.test.tsx",
             "case": ["AlertsPage", "renders open alert episodes"]
           },
           "outcome": "fail",
           "failure_kind": "assertion",
           "duration_ms": 39,
           "cached": false,
           "where": "tools/tillr-view/web/tests/alerts.test.tsx",
           "assertion": "Unable to find an element with the text: item-fix-1",
           "output": "FAIL tests/alerts.test.tsx > AlertsPage > renders open alert episodes\nTestingLibraryElementError: Unable to find an element with the text: item-fix-1\n"
         }
       ],
       "unattributed_errors": []
     }
     ```
   - Closed `outcome` Name Set on each entry in `tests[]`: `"pass"`, `"fail"`, `"skipped"` (skipped by test code such as `t.Skip()` or `test.skip()`), `"absent"` (requested in `mode: "include"` and not present in the suite).
   - Closed `failure_kind` Name Set when `outcome == "fail"`: `"assertion"`, `"panic"`, `"timeout"`, `"deadlock"`, `"sigquit"`, `"setup"`. Callers branch on `failure_kind`, never on Go strings like `"panic: test timed out after "` ([`D-GR31`](../reference/gate.md#D-GR31)).
   - `unattributed_errors[]` (`{ "suite": string, "kind": "compile" | "suite_setup" | "crash", "where"?: string, "summary": string, "output": string }`): populated when compilation, module import, `TestMain`, or `beforeAll` fails outside an individual test. Non-empty `unattributed_errors` makes `status: "fail"` (exit `1`), convicting a broken candidate in the lane gate while preventing trunk proof from misattributing a compile error as a per-test flake.
   - `VerdictCheck` on `gate.verdict` gains one optional field `test_report_ref` (`string`, relative path `<run-dir>/gate/reports/<check-id>.json` holding the validated `gate.adapter.run-result` envelope) plus `failing_tests` (`[]TestID`, leaf failures only). Old readers ignore unknown optional fields on `VerdictCheck`, while `tillr-drain` reads `test_report_ref` directly instead of scraping `<check-id>.log`.

### 2.5 How a repository declares an adapter — here and in external repositories (`D-TRA3`)

1. **In `<checks_dir>/<id>.json` ([`tools/tillr-gate/checkfiles.go`](../../tools/tillr-gate/checkfiles.go)):**
   `checkFile` gains an optional `"adapter"` object:
   ```json
   {
     "id": "observatory-dev",
     "label": "observatory recipe and supervisor harness tests pass",
     "scope": "tree",
     "tier": "heavy",
     "timeout_ms": 180000,
     "adapter": {
       "name": "go-test",
       "config": {
         "files": ["tests/observatory_dev_test.go"]
       }
     },
     "reads": [
       ".justfile",
       "tools/observatory/run.sh",
       "tools/tillr-view/Procfile.dev",
       "tools/tillr-view/.air.toml",
       "tests/observatory_dev_test.go"
     ],
     "tools": ["go", "git", "sh", "bash"]
   }
   ```
   - **Mutual exclusion (`R-TW-DR2`):** `"command"` and `"adapter"` are mutually exclusive in `checkFile.validate`. A check with `"command"` and no `"adapter"` is an **opaque command check** and runs identically to today (`c6`). A check with `"adapter"` and no `"command"` is an **adapted test check**. Declaring both, or declaring neither, fails `parseCheckDeclaration` with exit `4` (`"could not judge"`). There is never a fallback from a broken or missing adapter to a shell command.
2. **How `adapter.name` resolves (including in repositories outside this one):**
   Following [`installing-tillr-outside-yolo.md`](./installing-tillr-outside-yolo.md) (`D-HI1`, `D-HI4`) and [`tillr-utility-contracts.md` Section 8.8](./tillr-utility-contracts.md#88-the-check-declarations-directory-is-configured-in-tillrtoml) (`[gate] checks_dir` in `.tillr.toml`):
   - **Step 1 — Repository-declared adapters in `.tillr.toml`:** `.tillr.toml` (`tools/tillr/settings/settings.go`) accepts an optional `[gate.adapters.<name>]` table:
     ```toml
     [gate]
     checks_dir = "tools/gate/checks"

     [gate.adapters.pytest]
     command = ["python3", "tools/gate/adapters/pytest_adapter.py"]
     protocol_version = 1
     reads = ["tools/gate/adapters/pytest_adapter.py"]
     ```
     When `command[0]` contains `/`, `tillr-gate` resolves it relative to the checked-out `worktree` (never the trunk root, so a candidate modifying its repo-local adapter is gated against its own change) and automatically includes `.tillr.toml` and `[gate.adapters.<name>].reads` in `memory.go`'s input fingerprint and `declaration_digest`. When `command[0]` is a bare binary name (for example `"tillr-adapter-cargo"`), `tillr-gate` resolves it via `exec.LookPath` and records it alongside `check.tools`.
   - **Step 2 — Built-in adapters shipped inside `tillr-gate`:** When `.tillr.toml` does not define `[gate.adapters.<name>]`, `tillr-gate` checks its built-in registry (`tillr-gate adapter <name> ...`, invoked via `os.Executable()` using the same subprocess wire protocol so built-in and external adapters traverse the identical code path and contract checks!). An external consumer repository using Go or Vitest needs zero adapter scripts in its tree — installing `tillr` (`yolo-packs/tillr`, Homebrew, `install.sh`, or `just install`) ships `tillr-gate` with `go-test` and `vitest` ready to declare in `<checks_dir>/*.json`.
   - **Step 3 — Unknown adapter name:** If `<name>` is in neither `.tillr.toml` nor `tillr-gate`'s built-in registry, `tillr-gate` exits `4` (`record.ExitExternal`, `"could not judge: unknown test adapter <name>"`).

---

## 3. The first two adapters, and checks with no adapter (`D-TRA4`)

### 3.1 `go-test`: built from `shards`, `goaffected`, and file-mode Go checks

`tillr-gate adapter go-test` consolidates the logic already in [`tools/tillr-gate/shards/`](../../tools/tillr-gate/shards/) and [`tools/tillr-gate/goaffected/`](../../tools/tillr-gate/goaffected/) behind the `gate.adapter.*` contract:

- **Supported `adapter.config` shapes:**
  1. **Module suite** (`"module": "tools/tillr-drain"`, `"shards": 6`, `"cache": true`, `"parallel": 1` optional): covers all `12` `go-test-*.json` module checks, including [`tools/gate/checks/go-test-tillr-cli.json`](../../tools/gate/checks/go-test-tillr-cli.json) (which today runs raw `go test -parallel 1 -timeout 0 ./...` without `shards`).
  2. **Explicit packages or files** (`"module"?: "tools/gate"`, `"packages"?: ["./..."]`, `"files"?: ["tests/observatory_dev_test.go"]`, `"filter_regex"?: "TestDocuments|TestChange..."`): covers [`tools/gate/checks/observatory-dev.json`](../../tools/gate/checks/observatory-dev.json), [`tools/gate/checks/format-test.json`](../../tools/gate/checks/format-test.json), and targeted Go checks (`gate-adapters-test.json`, `change-checks-test.json`, `tree-checks-test.json`, `pack-sync-test.json`, `gate-budget-test.json`), replacing Go's synthetic `"command-line-arguments"` package label on file-mode runs with `"file:tests/observatory_dev_test.go"`.
  3. **Affected-package change check** (`"edges_file": "tools/gate/go-test-edges.json"`, `"deferred_packages": ["github.com/mschulkind-oss/tillr-drain/drain"]` on `scope: "change"`): implements `select` via `goaffected.SelectAffectedPackages` (`go list` reverse-import closure + `go-test-edges.json`), and `run` over those selected packages.
- **How each operation works:**
  - `list`: parses `*_test.go` ASTs (`go/parser`, the fast path already used in `goaffected`) to emit deterministic top-level `Test*`, `Example*`, and `Fuzz*` `TestID`s without compiling test binaries.
  - `run`: executes `go test -json` under `shards` duration-balanced worker pools with `PR_SET_CHILD_SUBREAPER` process-group cleanup ([`D-GR17`](../reference/gate.md#D-GR17)), private per-shard `TMPDIR` and `GIT_CONFIG_GLOBAL`, Go 1.27.3 unparenthesized `-skip` for `exclude_tests`, and `-count=1` whenever `cache == false` or `TILLR_RUN_ID != ""` ([`D-GR49`](../reference/gate.md#D-GR49)).
  - **Leaf attribution and `failure_kind`:** When `TestParent/sub_a` fails and `TestParent` also emits `"Action": "fail"`, prefix matching on `case` (`["TestParent"]` is a strict prefix of `["TestParent", "sub_a"]`) records `outcome: "fail"` on `["TestParent", "sub_a"]` rather than attributing a separate failure to `TestParent` unless `TestParent` emitted its own failure after subtests completed. Go timeout panics map to `failure_kind: "timeout"`, runtime deadlocks to `"deadlock"` ([`D-GR31`](../reference/gate.md#D-GR31)), `SIGQUIT` goroutine dumps to `"sigquit"` ([`D-GR52`](../reference/gate.md#D-GR52)), build/setup failures (`FAIL\t<pkg>\t[build failed]`) to `unattributed_errors` (`kind: "compile"` or `"suite_setup"`), and unmatched `mode: "include"` targets (`testing: warning: no tests to run` or parent ran with zero matching subtests) to `outcome: "absent"`.

### 3.2 `vitest`: front-end runner for `observatory-web` (proving the contract is not Go-shaped)

[`tools/gate/checks/observatory-web.json`](../../tools/gate/checks/observatory-web.json) tests [`tools/tillr-view/web/`](../../tools/tillr-view/web/) (`vitest@^2.1.8` in [`tools/tillr-view/web/package.json`](../../tools/tillr-view/web/package.json), configured by [`tools/tillr-view/web/vitest.config.ts`](../../tools/tillr-view/web/vitest.config.ts) with `environment: "jsdom"`, `maxWorkers: 6`, `include: ["tests/**/*.test.ts", "tests/**/*.test.tsx"]`). Every mechanic of `tillr-gate adapter vitest` differs from Go:

1. **Separating static typechecking from test execution:** `npm run typecheck` (`tsc --noEmit`) has no test cases to list or select; bundling it inside a test check means running one 40 ms component test would pay a full-project `tsc` pass and any type error would masquerade as a test run. Under `D-TRA4` (with pre-merge vs. post-merge placement left to the owner in [`OQ-TRA2`](#OQ-TRA2)), `tsc --noEmit` is separated from Vitest case execution cleanly (either as its own `observatory-web-typecheck` opaque check mirroring `go-build`, or per [`OQ-TRA2`](#OQ-TRA2) Option C as a `mode == "all"` preflight). Also, in clean lane worktrees where `tools/tillr-view/web/node_modules` is not checked in, if `node_modules` is absent and offline bootstrap fails, the adapter exits `4` (`record.ExitExternal`, `"could not judge"`), never `1`.
2. **Test identity without Go assumptions:** A Vitest test in [`tools/tillr-view/web/tests/alerts.test.tsx`](../../tools/tillr-view/web/tests/alerts.test.tsx) has no Go module, no import path, and free-form English `describe` / `test` strings:
   `{ "adapter": "vitest", "suite": "tools/tillr-view/web:tests/alerts.test.tsx", "case": ["Alerts view", "renders open alert episodes and linked items"] }`.
3. **`list`:** Invokes `./node_modules/.bin/vitest list --json --config vitest.config.ts [suite-files...]` in `workdir` (`tools/tillr-view/web`). Vitest 2.1 collects `describe`/`test` trees statically without executing component renders and emits JSON entries with file and title hierarchy, which the adapter normalizes to repo-relative `suite` and `case` arrays.
4. **`run` (`all`, `include`, and `exclude_tests`):**
   - Invokes `./node_modules/.bin/vitest run --config vitest.config.ts --reporter=json --outputFile=<scratch>/vitest-report.json --reporter=default` (passing `--maxWorkers=<req.max_workers>` and `--no-file-parallelism` when `max_workers == 1`).
   - **Targeted `mode: "include"`:** Groups requested `TestID`s by `suite` file, passes only those file paths on the Vitest CLI (so Vitest does not load the other 69 `.test.tsx` files), and passes `-t <anchored-pattern>` built by escaping every regex metacharacter of Vitest's space-joined `[*ancestorTitles, title].join(" ")` (`^\s*(?:<escaped_1>|<escaped_2>)$`).
   - **Handling Vitest's `-t` sibling `"skipped"` reporting (a runner-specific trap Go does not have):** When Vitest runs a file with `-t`, its `--reporter=json` output still lists every *non-matching* test in that `.test.tsx` file with `"status": "skipped"`. In `mode: "include"`, `tillr-gate adapter vitest` filters `assertionResults` against the requested `TestID` set: non-requested siblings skipped solely by `-t` are dropped; a requested `TestID` missing from `assertionResults` is reported as `outcome: "absent"`; and a requested `TestID` marked `"skipped"` / `"pending"` / `"todo"` in code (`test.skip(...)`) is reported as `outcome: "skipped"`.
   - **Handling `exclude_tests`:** Suites whose tests are all excluded pass `--exclude <rel-file>`. When one test inside a multi-test `.test.tsx` file is excluded (`DisabledMarker`), the adapter enumerates the remaining active tests in that file via `list` (or runs un-excluded files in one batch and partially-excluded files with an anchored inclusion pattern over the non-excluded tests in that file) so disabled Vitest cases never execute or block unrelated tests in the same file.
   - **Mapping Vitest JSON to `gate.adapter.run-result`:** Suite-level transform/import/`beforeAll` errors (`testResult.status == "failed"` with empty `assertionResults` or suite `message`) map to `unattributed_errors` (`kind: "compile"` on Vite/esbuild transform errors, `"suite_setup"` otherwise). Failed `assertionResults` map `failureMessages` to `output`, first failure line to `assertion`, `"Test timed out in "` to `failure_kind: "timeout"` (otherwise `"assertion"`), and `location` (`--includeTaskLocation`) to `where`.
5. **`select` (optional change-scoped Vitest):** Uses `vitest list --json --related <changed-files...>` (Vite's module import graph) so an edit to `tools/tillr-view/web/src/components/...` selects only `.test.ts`/`.test.tsx` files importing that module transitively.

### 3.3 Checks with no adapter keep working unchanged (`c6`)

All non-test checks in [`tools/gate/checks/`](../../tools/gate/checks/) — `go-build.json`, `go-vet.json`, `go-edges.json`, `go-module-inventory.json`, `format.json`, `documents.json`, `design-citations.json`, `citations.json`, `commit-identity.json`, `scratch.json`, `file-size.json`, `elapsed-time.json`, `gate-budget.json`, `hooks-install.json`, `no-python.json`, `pack-sync.json`, `tillr-backends.json`, `tillr-docs.json`, `tracked-tillr.json`, `workflows.json` — and any external repository check that sets `"command"` without `"adapter"` execute through `runPlanned` in [`tools/tillr-gate/run.go`](../../tools/tillr-gate/run.go) as opaque commands with zero change in flags, caching (`memory.go`), watchdog semantics, or `gate.verdict` records (`test_report_ref` is simply omitted).

---

## 4. Every place tillr runs tests, and what each does with the contract (`D-TRA5`, `D-TRA6`)

### 4.1 Code-site map across the repository

| Site | Exact code locations on `HEAD` (`11d7df6566ca`) | What it does today | What it does with the adapter contract |
| :--- | :--- | :--- | :--- |
| **1a. Lane candidate gate & local developer gate** | `Loop.verifyOnly` ([`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go)) -> `ProcessSteps.Gate` ([`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go)) -> `composeGate` ([`tools/tillr-loop/loop/loop.go`](../../tools/tillr-loop/loop/loop.go)) -> `changeGateFace` ([`tools/tillr-gate/change_gate.go`](../../tools/tillr-gate/change_gate.go)), `Plan` ([`tools/tillr-gate/plan.go`](../../tools/tillr-gate/plan.go)), `Run` ([`tools/tillr-gate/run.go`](../../tools/tillr-gate/run.go)), `cmdDone` ([`tools/gate/done.go`](../../tools/gate/done.go)); refusal parsing in `gateFailingChecks` ([`tools/tillr-drain/drain/returned.go`](../../tools/tillr-drain/drain/returned.go)) & `readTriageBlock` ([`tools/tillr-drain/internal/runhistory/refusal_cases.go`](../../tools/tillr-drain/internal/runhistory/refusal_cases.go)). | Runs `command` argv; `go-affected` prints `fork-check: triage --` text to stdout for `readTriageBlock` to regex-scrape; `gateFailingChecks` sees only check IDs and raw `.log` paths. | For adapted checks, `tillr-gate` invokes `select` (when `scope: "change"`) and `run` (passing active disabled `TestID`s in `exclude_tests`), classifies each test via `checkresult.Classify`, and writes `test_report_ref` + leaf `failing_tests` on `VerdictCheck`. `gateFailingChecks` and `refusal_cases.go` read structured `TestID`s and sliced `output` directly — deleting `fork-check: triage --` log scraping. |
| **1b. Base-SHA replay & open-repair acquittal** | `trunkAlreadyRed` / `settleTrunkAlreadyRed` ([`tools/tillr-drain/drain/trunk.go`](../../tools/tillr-drain/drain/trunk.go)), `ProcessSteps.Replay` ([`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go)), `ReplayGoCheck` / `RunNamedGoTestsInReplay` ([`tools/tillr-drain/internal/gatespans/replay_go_affected.go`](../../tools/tillr-drain/internal/gatespans/replay_go_affected.go)), `tillr-loop replay` ([`tools/tillr-loop/loop/loop.go`](../../tools/tillr-loop/loop/loop.go)), `holdForOpenTestFix` / `singleFailingTestNames` ([`tools/tillr-drain/drain/test_fix_hold.go`](../../tools/tillr-drain/drain/test_fix_hold.go)). | Narrowed per-test replay works **only** for Go checks (`isReplayGoCheck`), re-implementing `go test -run` in `replay_go_affected.go`; `observatory-web` or external checks replay the whole check. `holdForOpenTestFix` regex-scrapes `.log` files (`--- FAIL:`, `unittestFailRe`, `pytestFailRe`) and matches item English prose (`findOpenTestFixer`). | When an adapted check fails with leaf `failing_tests` (and empty `unattributed_errors`), `tillr-loop replay` calls `tillr-gate test-run --check <id> --tests <json>` (`mode: "include"`, `cache: false`) on `base_sha` for **any** adapted runner (a 1-test Vitest replay runs 1 `.test.tsx` file instead of `tsc` + 70 files). When `unattributed_errors` is non-empty (compile/setup break), replay runs the full check or suite. `fixer_case_match` ([`D-FCM1`–`D-FCM3`](./tillr-fixer-case-match-on-green-base.md#decision-ledger)) and `holdForOpenTestFix` compare exact canonical `TestID`s against open repair items' structured `test_selection`. |
| **2. Trunk proof, culprit bisection, & open-item linking** | `Loop.startHeavy` / `runTrunkProofStep` ([`tools/tillr-drain/drain/heavy_loop.go`](../../tools/tillr-drain/drain/heavy_loop.go)), `HeavyRunner.RunAttempt` ([`tools/tillr-drain/heavyrun/heavy.go`](../../tools/tillr-drain/heavyrun/heavy.go)), `JudgeHeavy` ([`tools/tillr-drain/internal/gatespans/heavy_steps.go`](../../tools/tillr-drain/internal/gatespans/heavy_steps.go)), `JudgeHeavyGo` / `GoSuiteFailures` ([`tools/tillr-drain/internal/gatespans/heavy_go.go`](../../tools/tillr-drain/internal/gatespans/heavy_go.go)), `HeavyRunner.FindFirstRed` ([`tools/tillr-drain/heavyrun/heavy_attribution.go`](../../tools/tillr-drain/heavyrun/heavy_attribution.go)), `linkMatchingOpenFixers` / `heavyCheckTestNames` ([`tools/tillr-drain/drain/heavy_link.go`](../../tools/tillr-drain/drain/heavy_link.go)). | Splits suite failures into per-test `HeavyCheck`s **only** for Go `shards` checks (`GoSuiteFailures`); `FindFirstRed` bisects per-test only for `go-test:...` selectors and re-runs full `observatory-web` on every midpoint commit; `heavy_link.go` scrapes `.log` files and English prose. | `JudgeHeavy` reads `test_report_ref` from `tillr-gate` for **every** adapted check (`go-test`, `vitest`, and consumer-repo adapters), emitting per-test `HeavyCheck`s carrying `FormatTestKey(id)` + structured `TestID` and per-test `output`. `FindFirstRed` bisects commit ranges via `tillr-gate test-run --mode include` (`1` test per bisection step in any language), and `linkMatchingOpenFixers` matches open items' structured `test_selection` first. Flake-probe and carryover rules remain governed by [`trunk-proof-per-test-flaky-judgment.md`](./trunk-proof-per-test-flaky-judgment.md) ([Section 5.1](#51-decision-boundary-with-trunk-proof-per-test-flaky-judgmentmd-zero-overlap)). |
| **3. Workflow `verify` step & item verification** | `Loop.verify` / `runCandidateExamination` ([`tools/tillr-drain/drain/settle_judge.go`](../../tools/tillr-drain/drain/settle_judge.go), [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go)), brief `VERIFICATION` placeholder ([`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go)), `verify` node in [`config/tillr/workflows/planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json), `CheckJudge` / `RecordJudge` ([`tools/tillr-item/item/judge.go`](../../tools/tillr-item/item/judge.go)). | `stage.go` populates `VERIFICATION` with `"None recorded..."`; an LLM `verify` agent inspects diffs or composes ad-hoc shell commands by hand to test criteria, then writes `JudgeOutput`. | When an item (or criterion) carries a structured `test_selection` (`D-TRA6`), `tillr-drain` runs `tillr-gate test-run` (`mode: "include"`, `require_present: true`, `cache: false`) **mechanically** in the candidate worktree without any agent composing a shell command, recording the signed `gate.adapter.run-result` artifact. Whether a passing mechanical run retires pure test-repair verification directly or also invokes an LLM `verify` turn is put to the owner in [`OQ-TRA1`](#OQ-TRA1). |
| **4. Split part gates & split closing checks** | Worded split closing: `splitclosing.Advance` / `runCheck` ([`tools/tillr-drain/internal/splitclosing/split_closing.go`](../../tools/tillr-drain/internal/splitclosing/split_closing.go)), `splitFace` ([`tools/tillr-drain/split.go`](../../tools/tillr-drain/split.go)), `FileSplit` ([`tools/tillr-item/item/split.go`](../../tools/tillr-item/item/split.go)); verifier-less / trunk-fixer splits (`D-SP18` in [`tillr-item-split.md` Section 7.7](./tillr-item-split.md#77-items-with-no-verifier-trunk-fixers-first)) and `S11` refusal case overlap (`D-SP17` in [`tillr-item-split.md` Section 6](./tillr-item-split.md#6-the-signals)). | Worded split closing runs a read-only `verify` agent over parent criteria. Verifier-less split parts (`D-SP18`, Slice 14 of [`tillr-item-split.md`](./tillr-item-split.md#14-the-build-in-slices-that-each-land-alone)) and `S11` gate-refusal case overlap (`D-SP17`, where 7 of 9 trunk-fixer refusal pairs collapsed to bare `go-test-tillr-drain`) lacked structured per-test selectors across checks. | `VerdictCheck.failing_tests` populates `S11` with exact `TestID` sets across every adapted check (solving the 7-of-9 bare check-ID collapse in [`tillr-item-split.md` Section 2](./tillr-item-split.md#2-the-case-measured)). In a verifier-less / trunk-fixer split (`D-SP18`), each cause part carries its assigned `TestID` subset in `test_selection`, proves those tests green via `tillr-gate test-run` (while checking unassigned tests add no new failures beyond sibling-owned base failures), and the parent's closing check runs the union of named `TestID`s on the trunk tip via `tillr-gate test-run` without prose prompts. |
| **5. Trunk fixers (`fixer-main-<sha>`)** | `routeTrunkFixer` ([`tools/tillr-drain/drain/trunk.go`](../../tools/tillr-drain/drain/trunk.go)), `prepareTrunkFixerFacts` ([`tools/tillr-drain/drain/fork_check_triage.go`](../../tools/tillr-drain/drain/fork_check_triage.go)), `fixHeavyChecks` ([`tools/tillr-drain/drain/heavy_loop.go`](../../tools/tillr-drain/drain/heavy_loop.go)), `Loop.settleFixerGate` / `ProcessSteps.fixerCheck` ([`tools/tillr-drain/drain/fixer_gate.go`](../../tools/tillr-drain/drain/fixer_gate.go)), [`tools/tillr-drain/fixer/fixer_gate.go`](../../tools/tillr-drain/fixer/fixer_gate.go), `tillr-loop fixer-gate` ([`tools/tillr-loop/loop/loop.go`](../../tools/tillr-loop/loop/loop.go)), `resolveProvedFixers` ([`tools/tillr-drain/drain/fixer_resolve.go`](../../tools/tillr-drain/drain/fixer_resolve.go)). | Per-test fixer checks (`goTestAbsent` / `goTestSkipped` -> `Unavailable`, targeted rerun via `JudgeHeavyGo`) work **only** for `go-test:...` selectors. A failure in `observatory-web`, `observatory-dev`, `go-test-tillr-cli`, or an external repo check records the whole check ID and re-runs the whole check in `settleFixerGate`. | `routeTrunkFixer` records structured `test_selection` (`check_id` + `TestID`s) for every adapted check failure, writes sliced per-test `output` and exact `tillr-gate test-run --check <id> --test <key>` reproduction commands into the fixer brief, and `settleFixerGate` -> `tillr-loop fixer-gate` invokes `tillr-gate test-run` (`mode: "include"`, `require_present: true`, `cache: false`), treating `outcome: "absent"` or `"skipped"` as `Unavailable` (refused as an incomplete repair, [`D-GR53`](../reference/gate.md#D-GR53)) across **every** adapted runner. |
| **6. Test repair items (`flaky-*` & filed test-fix requests)** | `sweepDisabledMarkers` / `reconcileDisabledMarker` / `FlakyRepairCriterionText` / `ProcessSteps.EnableCheck` ([`tools/tillr-drain/drain/flaky_repair.go`](../../tools/tillr-drain/drain/flaky_repair.go)), `tillr-gate enable` ([`tools/tillr-gate/enable.go`](../../tools/tillr-gate/enable.go)), `holdForOpenTestFix` ([`tools/tillr-drain/drain/test_fix_hold.go`](../../tools/tillr-drain/drain/test_fix_hold.go)). | `flaky-*` items embed the check ID in criterion text and marker JSON and bypass `settleFixerGate`; operator/watch-filed single-test repairs carry only English prose, forcing `itemFixesTest` regex/substring matching in `test_fix_hold.go` and `heavy_link.go`. | `tillr-item` stores an optional structured `test_selection` (`[]TestSelectionEntry{CheckID, Tests []TestID, ProveDisabled bool}`) on items (`D-TRA6`). Both `flaky-*` items and operator/watch-filed test repairs (`tillr-item create --test-selection ...`) populate it: briefs render exact `tillr-gate test-run` commands, `holdForOpenTestFix` and `linkMatchingOpenFixers` match on `TestID` equality with zero prose guessing, and pre-merge verification runs those exact `TestID`s through the adapter (applying [`trunk-proof-per-test-flaky-judgment.md`](./trunk-proof-per-test-flaky-judgment.md)'s stress/race bar when `prove_disabled: true`). |

### 4.2 What becomes possible, and which of it this design takes on (`D-TRA7`)

1. **Running only the failed tests again — TAKEN ON.** Base-SHA replay (`ProcessSteps.Replay`), culprit bisection (`FindFirstRed`), `fixer-main-<sha>` verification (`settleFixerGate`), test-repair verification, and (if approved in [`OQ-TPF1`](./trunk-proof-per-test-flaky-judgment.md#OQ-TPF1)) trunk-proof contradiction probes all call `tillr-gate test-run` (`mode: "include"`, `cache: false`) with the exact failed `TestID`s instead of re-running 70 Vitest files or whole Go checks.
2. **Judging a flaky test one test at a time across any adapted runner — MECHANISM TAKEN ON HERE; PROOF POLICY OWNED BY [`trunk-proof-per-test-flaky-judgment.md`](./trunk-proof-per-test-flaky-judgment.md).** Extending [Landing Principle 17](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking) from whole checks to individual tests requires (a) stable per-test identity, `exclude_tests` skipping, and per-test `input_digest` / `output` slicing across runners — **taken on in this design** — and (b) rules for when the trunk proof probes a failure, leaf-vs-parent attribution, stress-bar counts on `flaky-*`, and moving-tip carryover — **owned exclusively by [`trunk-proof-per-test-flaky-judgment.md`](./trunk-proof-per-test-flaky-judgment.md)**.
3. **Filing a repair item that carries the exact test selection — TAKEN ON (`D-TRA6`).** `tillr-item` persists `test_selection` (`check_id`, `tests[]`, `prove_disabled`) in the item store and exposes it on `item.show` and CLI flags (`--test-selection`), eliminating log-scraping and prose heuristics across `routeTrunkFixer`, `flaky_repair.go`, `test_fix_hold.go`, and `heavy_link.go`.
4. **A verify step that runs the named test without an agent composing a command — TAKEN ON (`D-TRA5`, policy boundary in [`OQ-TRA1`](#OQ-TRA1)).** `tillr-drain` executes `item.test_selection` mechanically via `tillr-gate test-run` before or in place of the LLM `verify` seat (per [`OQ-TRA1`](#OQ-TRA1)), never asking an agent to invent `go test -run` or `npx vitest -t` quoting.
5. **Choosing which tests a lane gate must run for a change — MECHANISM (`select`) TAKEN ON; GATE PROMOTION POLICY DEFERRED TO [`OQ-TRA2`](#OQ-TRA2) AND [`fast-gate-same-tool-proof-misses.md`](./fast-gate-same-tool-proof-misses.md).** The optional `select` operation (`gate.adapter.select-request` / `select-result`) gives `tillr-gate` one runner-agnostic interface for change-scoped test selection (`goaffected` for Go; `vitest related` / Vite module graph for Vitest). *Which* suites or packages a repository requires before merge vs. defers to the after-merge proof remains repository gate policy governed by [`D-GR56`](../reference/gate.md#D-GR56), [`OQ-FGM1`](./fast-gate-same-tool-proof-misses.md#OQ-FGM1) for Go, and [`OQ-TRA2`](#OQ-TRA2) for `observatory-web`.

---

## 5. Relation to companion designs and landing principles

### 5.1 Decision boundary with [`trunk-proof-per-test-flaky-judgment.md`](./trunk-proof-per-test-flaky-judgment.md) (zero overlap)

[`trunk-proof-per-test-flaky-judgment.md`](./trunk-proof-per-test-flaky-judgment.md) (`status: in-review`) was filed the same evening (2026-11-27) after commit `50e065bc` hit a transient failure in `TestWhyAfterRestartOverLiveEvaluator`. No decision is settled in both documents:

| Topic | Owned by [`trunk-proof-per-test-flaky-judgment.md`](./trunk-proof-per-test-flaky-judgment.md) | Owned by this document |
| :--- | :--- | :--- |
| **Test identity** | [`D-TPF1`](./trunk-proof-per-test-flaky-judgment.md#D-TPF1) / [`OQ-TPF2`](./trunk-proof-per-test-flaky-judgment.md#OQ-TPF2): leaf subtest vs. enclosing top-level function as the unit of contradiction/disabling in trunk proof, parent-fail suppression policy, and binding disabled selectors into `memory.go`. | [`D-TRA2`](#D-TRA2): structured wire tuple `TestID{adapter, suite, case []string, variant}` across Go and non-Go runners, and lossless `FormatTestKey` identity mapping to `go-test:<module>:<pkg>:<TestName>` so `D-TPF1` requires zero format migration. |
| **Focused rerun on trunk-proof failure** | [`OQ-TPF1`](./trunk-proof-per-test-flaky-judgment.md#OQ-TPF1) / [`D-TPF3`](./trunk-proof-per-test-flaky-judgment.md#D-TPF3): whether a first-seen trunk-proof failure triggers a focused same-tip contradiction probe (amending [`D-GR22`](../reference/gate.md#D-GR22)), which failure classes are excluded from probing, and `(fail, pass) -> disabled` vs. `(fail, fail) -> fail` classification. | [`D-TRA1`](#D-TRA1): the `tillr-gate` / adapter `run` (`mode: "include"`, `cache: false`) wire operation and closed `failure_kind` Name Set (`timeout`, `deadlock`, `sigquit`, `panic`, `assertion`, `setup`) that execute whatever [`OQ-TPF1`](./trunk-proof-per-test-flaky-judgment.md#OQ-TPF1) rules, across any runner. |
| **Automatic `flaky-*` filing & slug length** | [`D-TPF2`](./trunk-proof-per-test-flaky-judgment.md#D-TPF2): running `sweepDisabledMarkers` without requiring an active `refs/tillr/green` branch, and capping `FlakyRepairBaseID` slugs at 96 bytes + SHA-256 suffix. | [`D-TRA6`](#D-TRA6): structured `test_selection` table/field on items (`flaky-*`, `fixer-main-*`, and filed test repairs) so callers read `TestID` structs instead of parsing item IDs or English prose. |
| **`flaky-*` verification bar** | [`OQ-TPF3`](./trunk-proof-per-test-flaky-judgment.md#OQ-TPF3) / [`D-TPF4`](./trunk-proof-per-test-flaky-judgment.md#D-TPF4): repeat count, `-race`, contention load (`-k`), and `--prove-disabled` policy required before a `flaky-*` repair can land and re-enable its marker. | [`D-TRA1`](#D-TRA1) / [`D-TRA5`](#D-TRA5) / [`OQ-TRA1`](#OQ-TRA1): passing `repeat`, `race`, `max_workers`, `require_present`, and `prove_disabled` in `gate.adapter.run-request`, reporting `absent` / `skipped` uniformly across Go and non-Go runners, and mechanical `verify` execution for general test-backed items. |
| **Moving-tip (`T1 -> T2`) proof carryover** | [`OQ-TPF4`](./trunk-proof-per-test-flaky-judgment.md#OQ-TPF4) / [`D-TPF5`](./trunk-proof-per-test-flaky-judgment.md#D-TPF5): which `DisabledMarker`s and unchanged-input per-test `pass` observations carry across `T1 -> T2`, `--prior-failed` ordering policy, and pre-Landlock key scope. | [`D-TRA1`](#D-TRA1): carrying `prior_failed: []TestID` in `gate.adapter.run-request`, streaming `gate.adapter.event` JSONL before interruption, and returning optional per-test `input_digest` in `gate.adapter.run-result`. |
| **Non-proof test sites & external repos** | Explicitly out of scope ([Section 7 of `trunk-proof-per-test-flaky-judgment.md`](./trunk-proof-per-test-flaky-judgment.md#7-what-is-deliberately-not-done)). | [`D-TRA3`](#D-TRA3)–[`D-TRA6`](#D-TRA6), [`OQ-TRA1`](#OQ-TRA1)–[`OQ-TRA3`](#OQ-TRA3): `<checks_dir>/*.json` and `.tillr.toml` adapter declarations, external consumer repositories, `vitest` and file-mode `go-test` adapters, lane gate, base replay, `verify`, split closing checks, and `fixer-main-<sha>`. |

### 5.2 What [`gate-test-caching.md`](./gate-test-caching.md) and [`fast-gate-same-tool-proof-misses.md`](./fast-gate-same-tool-proof-misses.md) settle vs. leave open

- **[`gate-test-caching.md`](./gate-test-caching.md) (`status: in-review`):**
  - *Already settled by the owner (`2026-11-25` ruling quoted in [Section 1 of `gate-test-caching.md`](./gate-test-caching.md#1-owner-direction-and-framing)):*
    > "Skipping a test whose inputs did not change is right only when every input to the test has been accounted for — source, packages it imports, files it reads at runtime, binaries it shells out to, environment, flags, toolchain. A skip on a guess is an unverified merge."
    And [`D-GR49`](../reference/gate.md#D-GR49) settles that `TILLR_RUN_ID` disables test result caching inside agent runs while gates and trunk proofs run outside `TILLR_RUN_ID`.
  - *Still open in [`gate-test-caching.md`](./gate-test-caching.md) ([`OQ-GC1`](./gate-test-caching.md#OQ-GC1)–[`OQ-GC5`](./gate-test-caching.md#OQ-GC5)):* Landlock read-set verification vs. keeping `tools/gate/go-test-edges.json` ([`OQ-GC1`](./gate-test-caching.md#OQ-GC1)), `go test` content-addressed GOCACHE reuse vs. `tillr-gate`'s own test cache ([`OQ-GC2`](./gate-test-caching.md#OQ-GC2)), per-package proof resumption ([`OQ-GC3`](./gate-test-caching.md#OQ-GC3)), `shards` consolidation ([`OQ-GC4`](./gate-test-caching.md#OQ-GC4)), and `tillr-drain` sub-package carving ([`OQ-GC5`](./gate-test-caching.md#OQ-GC5)).
  - *How this design fits without pre-judging [`OQ-GC1`](./gate-test-caching.md#OQ-GC1)–[`OQ-GC5`](./gate-test-caching.md#OQ-GC5):* `gate.adapter.run-request` passes `cache: bool` (false whenever `TILLR_RUN_ID != ""` or during targeted replay/repair verification), and `gate.adapter.run-result` carries `cached: bool` plus optional `input_digest` per test. `vitest` defaults to `cached: false` (never guessing hidden filesystem inputs across JS files, obeying the `2026-11-25` soundness bar), while `go-test` delegates caching to the existing `shards` / `GOCACHE` path and upgrades automatically if [`OQ-GC1`](./gate-test-caching.md#OQ-GC1)/[`OQ-GC2`](./gate-test-caching.md#OQ-GC2) land Landlock-verified digests.
- **[`fast-gate-same-tool-proof-misses.md`](./fast-gate-same-tool-proof-misses.md) (`status: in-review`):**
  - *Already settled by owner rulings:* [`D-GR56`](../reference/gate.md#D-GR56) defers `github.com/mschulkind-oss/tillr-drain/drain` (`DeferredProofPackages`) from pre-merge `go-affected` to the rolling trunk proof until sub-package carving reduces its 50–80 s cost.
  - *Still open ([`OQ-FGM1`](./fast-gate-same-tool-proof-misses.md#OQ-FGM1)):* Whether to promote `tillr-drain/drain` back into `go-affected` immediately, keep [`D-GR56`](../reference/gate.md#D-GR56) until carved, or run only changed `*_test.go` test functions on `tillr-drain/drain`.
  - *How this design fits without pre-judging [`OQ-FGM1`](./fast-gate-same-tool-proof-misses.md#OQ-FGM1):* `gate.adapter.select-result` supports returning **both** `selected_suites` (package-level selection, preserving [`D-GR56`](../reference/gate.md#D-GR56) today) and `selected_tests` (function-level selection, directly supporting [`OQ-FGM1`](./fast-gate-same-tool-proof-misses.md#OQ-FGM1) Option C if the owner selects it).

### 5.3 Check against [`landing-principles.md`](./landing-principles.md)

| Principle | How this design satisfies it |
| :--- | :--- |
| **[Principle 1 (A refusal must be a verdict)](./landing-principles.md#1-a-refusal-must-be-a-verdict)** & **[Principle 5 (Only verdicts charge)](./landing-principles.md#5-only-verdicts-charge)** | Adapter exit codes `2`, `3`, `4`, `5`, `6` — and any exit `0`/`1` missing a valid `record_version: 1` `--output` envelope — map to `ResultError` / `ResultStalled` (`tillr-gate` exits `4` or `6`), never `ResultFail` and never `ResultPass`. Missing `node_modules` or a missing external adapter binary never charges a candidate refusal. |
| **[Principle 3 (Gate what will be the tip, once)](./landing-principles.md#3-gate-what-will-be-the-tip-once)** & **the trunk-activity scaling rule** | Adapted checks declare `reads` and optional `select` so candidates that do not touch `tools/tillr-view/web/` skip `vitest` in `0 ms`, and base-SHA replay / `FindFirstRed` bisection run in `O(failing tests)` rather than `O(whole suite * commits)`. |
| **[Principle 8 (A gate that cannot answer must say which)](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)** | Distinguishes `outcome: "fail"` / `unattributed_errors` (code under test is red, exit `1`) from `record.ExitExternal` (missing toolchain or dependency, exit `4`) and `ExitStalled` (60-sample hang, exit `6`). |
| **[Principle 11 (A failure is diagnosed, not waited out)](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out)** | Adapters perform zero blind retries (`repeat: 1` in gates and proofs; `repeat > 1` is permitted only when `prove_disabled: true` on a `flaky-*` repair item under [`OQ-TPF3`](./trunk-proof-per-test-flaky-judgment.md#OQ-TPF3)). |
| **[Principle 13 (A held state names a mover that can still act)](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)** | `holdForOpenTestFix` matches exact canonical `TestID`s against an open, non-finished item's `test_selection` (`item:<fixer-id>`), never holding on prose coincidences or closed items. |
| **[Principle 14 (Convict a run on what it did, not on what happened while it ran)](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)** | Every adapter invocation receives a check-private `scratch_dir` (`TMPDIR` isolated per check) and `GIT_CEILING_DIRECTORIES`, and base-SHA replay / `fixer_case_match` acquit candidates on pre-existing per-test trunk failures in any adapted runner. |
| **[Principle 15 (Land the smallest functional change)](./landing-principles.md#15-land-the-smallest-functional-change)** & **[`D-GR32`](../reference/gate.md#D-GR32)** | Sequenced in 7 self-contained slices ([Section 6](#6-build-slices-smallest-useful-first)) that land `tillr-gate` schema and adapter support before any `tools/gate/checks/*.json` file switches to `"adapter"`, respecting the installed `tillr-gate` binary on `refs/tillr/green`. |
| **[Principle 17 (Disable a flaky check, file its repair, and count it neither green nor blocking)](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking)** | `tillr-gate` (never the adapter) classifies per-test contradictions via `checkresult.Classify`, passes active disabled `TestID`s as `exclude_tests` so remaining tests in the Go package or Vitest file still guard the tree, and refuses `absent` or `skipped` outcomes on repair verification ([`D-GR53`](../reference/gate.md#D-GR53)). |

---

## 6. Build slices (smallest useful first)

Two repository constraints shape slice ordering:

1. **Installed `tillr-gate` rollout order ([`D-GR32`](../reference/gate.md#D-GR32)):** Live gates run the `tillr-gate` binary installed from `refs/tillr/green`, and `parseCheckDeclaration` in [`tools/tillr-gate/checkfiles.go`](../../tools/tillr-gate/checkfiles.go) calls `DisallowUnknownFields()`. Therefore, `tillr-gate` must accept `"adapter"` in `checkfiles.go` and advance `refs/tillr/green` **before** any tracked file in `tools/gate/checks/*.json` replaces `"command"` with `"adapter"`.
2. **Gate-code fences ([`OQ-GR3`](../reference/gate.md#OQ-GR3), [`OQ-GR6`](../reference/gate.md#OQ-GR6)) and per-slice question holds ([`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8)):** Each `tools/tillr-gate` slice lands with unit tests and pinned contract examples under `tools/tillr-gate/contract/` and zero `tools/tillr-drain` edits; slices that do not depend on [`OQ-TRA1`](#OQ-TRA1)–[`OQ-TRA3`](#OQ-TRA3) can be filed and built immediately while dependent slices wait on their named question.

| Slice | Scope | What becomes immediately useful when it lands | Question dependencies |
| :--- | :--- | :--- | :--- |
| **Slice 1: Adapter contract, `checkfiles.go` + `.tillr.toml` resolution, and `tillr-gate test-list` / `test-run` CLI** | `tools/tillr/settings/`, `tools/tillr-gate/` (`checkfiles.go`, adapter subprocess runner, `VerdictCheck.test_report_ref` + `failing_tests`, `tillr-gate test-list` and `tillr-gate test-run`), `tools/tillr-gate/contract/`. Zero edits to `tools/gate/checks/*.json` or `tools/tillr-drain`. | Pins `gate.adapter.*` (`record_version: 1`) with deterministic contract examples; lets external consumer repositories declare `[gate.adapters.<name>]` in `.tillr.toml`; keeps every adapter-less `command` check working unchanged; prepares `refs/tillr/green` for check-file migration ([`D-GR32`](../reference/gate.md#D-GR32)). | None (subject to owner overturn of [`D-TRA1`](#D-TRA1)–[`D-TRA3`](#D-TRA3)). |
| **Slice 2: Built-in `go-test` adapter in `tillr-gate` + contract examples** | `tools/tillr-gate/` (`tillr-gate adapter go-test` wrapping `shards`, `goaffected`, and file-mode Go checks with `TestID` leaf attribution and `failure_kind` mapping) + `tools/tillr-gate/contract/` Go examples. | `tillr-gate test-list` and `tillr-gate test-run` work end-to-end on module, package, file (`observatory_dev_test.go`), and `go-affected` shapes with structured `gate.adapter.run-result` output. | None (compatible with [`D-TPF1`](./trunk-proof-per-test-flaky-judgment.md#D-TPF1) regardless of how [`OQ-TPF2`](./trunk-proof-per-test-flaky-judgment.md#OQ-TPF2) settles). |
| **Slice 3: Built-in `vitest` adapter in `tillr-gate` + fixture tests and contract examples** | `tools/tillr-gate/` (`tillr-gate adapter vitest` implementing `list`, `run` with `-t` sibling-skip filtering and `exclude_tests`, and `select` via `vitest list --json --related`) + fixture tests and `tools/tillr-gate/contract/` Vitest examples. | `tillr-gate` can enumerate, select, exclude, and read per-test Vitest results and suite compile errors without Go-shaped assumptions, ready for `observatory-web` check wiring. | None for built-in `vitest` itself; additional built-in runners wait on [`OQ-TRA3`](#OQ-TRA3). |
| **Slice 4: Migrate `tools/gate/checks/` Go test checks onto `"adapter": {"name": "go-test"}` and `observatory-web` onto `"vitest"`** | `tools/gate/checks/go-test-*.json`, `go-affected.json`, `observatory-dev.json`, `format-test.json`, and (per [`OQ-TRA2`](#OQ-TRA2)) `observatory-web.json` / `observatory-web-typecheck.json`. Requires Slices 1–3 on `refs/tillr/green` ([`D-GR32`](../reference/gate.md#D-GR32)). | Every Go check (including `go-test-tillr-cli` and `observatory-dev`) and `observatory-web` emit structured `gate.adapter.run-result` reports on `gate.verdict`. | Go check migration has no `OQ-*` blocker; `observatory-web.json` scope/tier split depends on [`OQ-TRA2`](#OQ-TRA2). |
| **Slice 5: Switch `tillr-drain` and `tillr-loop` gate refusal reading, base replay, trunk proof, `FindFirstRed`, and `settleFixerGate` onto `test_report_ref` and `tillr-gate test-run`** | `tools/tillr-loop/loop/`, `tools/tillr-drain/` (`returned.go`, `refusal_cases.go`, `steps.go`, `replay_go_affected.go`, `heavy_steps.go`, `heavy_go.go`, `heavy_attribution.go`, `fixer_gate.go`, `trunk.go`), reading `tools/tillr-gate/contract/` examples in consumer tests. | Deletes `fork-check: triage --` scraping and `JudgeHeavyGo` / `RunNamedGoTestsInReplay` runner duplication; base replay, `FindFirstRed` culprit bisection, `S11` refusal overlap, and `fixer-main-<sha>` gates work per-test on `vitest`, file-mode Go, and external adapters alike. | None (trunk-proof flake probing and moving-tip carryover remain gated on [`OQ-TPF1`](./trunk-proof-per-test-flaky-judgment.md#OQ-TPF1)–[`OQ-TPF4`](./trunk-proof-per-test-flaky-judgment.md#OQ-TPF4)). |
| **Slice 6: Structured `test_selection` on items (`tillr-item` + `tillr-drain` repair filing and open-repair matching)** | `tools/tillr/store/` migration, `tools/tillr-item/` (`--test-selection`, `item.show`), `tools/tillr-drain/` (`trunk.go`, `flaky_repair.go`, `test_fix_hold.go`, `heavy_link.go`). | Filed test repairs (`flaky-*`, `fixer-main-*`, and operator/watch test-fix items) carry structured `TestID` selections; `holdForOpenTestFix` and `linkMatchingOpenFixers` match on exact `TestID`s instead of `.log` regexes and English prose. | None (`flaky-*` stress/race counts remain gated on [`OQ-TPF3`](./trunk-proof-per-test-flaky-judgment.md#OQ-TPF3)). |
| **Slice 7: Mechanical `test_selection` execution in item `verify` and verifier-less split closing checks** | `tools/tillr-drain/` (`settle_judge.go`, `stage.go`, `internal/splitclosing/`), `tools/tillr-item/item/split.go`. | Runs an item's or split part's named `TestID` selection mechanically via `tillr-gate test-run` without an agent composing shell commands, and closes verifier-less / trunk-fixer splits (`D-SP18`) across any adapted runner. | Depends on [`OQ-TRA1`](#OQ-TRA1) for how mechanical `test_selection` interacts with the LLM `verify` seat. |

---

## 7. Open questions

1. 💬 **OQ-TRA1: When an item carries a structured `test_selection` (such as a single-test repair item, a split part with assigned tests, or a request naming specific tests), should a passing mechanical adapter run satisfy test verification directly, or still invoke an LLM `verify` agent?**

   Today, generated `fixer-main-*` items use `single-worker@v1` (no `verify` node) plus `settleFixerGate`, while worded requests and `flaky-*` items use `planned-build@v1` and spawn an LLM `verify` agent that reads diffs or composes shell commands by hand. Once `tillr-drain` can run an item's structured `test_selection` mechanically via `tillr-gate test-run` (`mode: "include"`, `require_present: true`, `cache: false`):

   - **A — Settle pure test-repair items mechanically with zero LLM `verify` turn, and on multi-criterion worded requests (`planned-build@v1`) run attached `test_selection`s mechanically *before* `verify` — returning immediately to `build` if a required test fails, and passing the frozen `gate.adapter.run-result` record to `verify` when it passes.** Eliminates agent-composed test commands everywhere, saves wasted `verify` model spend when a required test is still red, and keeps the LLM `verify` judge on worded prose criteria (such as "fix at root cause without retries or sleeps").
   - **B — Allow any individual item criterion to bind a structured `test_selection` whose `done` / `not-done` verdict is recorded mechanically by `tillr-gate`, spawning the LLM `verify` agent only when at least one prose-only criterion remains.** Skips `verify` model turns completely whenever every criterion on an item is backed by a `TestID` selection, at the cost of having no LLM review for test-deletion/hardcoding shortcuts beyond `require_present: true` and the gate.
   - **C — Always run the LLM `verify` node on `planned-build@v1` items even when a mechanical `test_selection` fails, attaching `gate.adapter.run-result` purely as pre-collected context.** Preserves current node transitions unchanged, at the cost of paying an LLM `verify` turn to repeat a deterministic test failure.

   <!-- vantage: question id=OQ-TRA1 leaning="A — mechanical settlement on pure test-repair items, and pre-verify mechanical gate + frozen result packet on worded requests" -->

   _Leaning (designer's leaning, not an owner ruling):_ **A** — pure test repairs get deterministic mechanical verification with zero agent shell composition or extra LLM turns, while worded requests (`flaky-*` root-cause bar and multi-criterion features) fast-fail before `verify` on red tests and still check non-evasion prose criteria when green.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-TRA2: Once the `vitest` adapter lands, how should [`observatory-web`](../../tools/gate/checks/observatory-web.json) (`tsc --noEmit` + `70` Vitest test files in `tools/tillr-view/web/`) be placed between the pre-merge lane gate and the after-merge trunk proof?**

   Today [`tools/gate/checks/observatory-web.json`](../../tools/gate/checks/observatory-web.json) bundles `npm ci`, `tsc --noEmit`, and `vitest run` into a single `scope: "tree", tier: "heavy"` check, so pre-merge lane gates touching `tools/tillr-view/web/` run neither `tsc` nor `vitest` before landing — every front-end break lands on `main` first and is caught only by the trunk proof.

   - **A — Split out `observatory-web-typecheck` (`tsc --noEmit`, `reads: ["tools/tillr-view/web/"]`) and a change-scoped `observatory-web-affected` (`adapter: "vitest"`, `scope: "change"`, `tier: "heavy"`) in the pre-merge lane gate, while keeping full `observatory-web` (`adapter: "vitest"`, `scope: "tree"`, `tier: "heavy"`) in the after-merge trunk proof.** Mirrors Go (`go-build` + `go-affected` before merge, full `go-test-*` after merge): non-web lanes pay `0 ms` (`skipped: no changed path is read`), and front-end lanes catch type errors and affected component regressions before landing.
   - **B — Split `observatory-web-typecheck` (`tsc --noEmit`) from `observatory-web` (`adapter: "vitest"`), keeping both in the after-merge `scope: "tree", tier: "heavy"` trunk proof only.** Gives trunk proof, base replay, `FindFirstRed` culprit bisection, and `fixer-main-*` per-test Vitest granularity without adding `node` / `node_modules` execution to any pre-merge lane gate.
   - **C — Keep a single `observatory-web` check ID in the after-merge `heavy` tree proof, running `tsc --noEmit` as an adapter preflight step on `mode: "all"` only (skipped on `mode: "include"` single-test replays and fixers).** Preserves the single check ID `observatory-web` with no pre-merge front-end gate.

   <!-- vantage: question id=OQ-TRA2 leaning="A — pre-merge typecheck + vitest-affected on tools/tillr-view/web/ changes, full vitest suite in trunk proof" -->

   _Leaning (designer's leaning, not an owner ruling):_ **A** — because `reads: ["tools/tillr-view/web/"]` costs `0 ms` on every non-web lane (`Landing Principle 3`), while front-end changes stop landing broken TypeScript or component regressions onto `main`. (Note: in clean worktrees without pre-installed `node_modules`, Option A requires either a read-only/cached `node_modules` tree or `npm ci` bootstrap on web-touching lanes.)

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-TRA3: Beyond `go-test` and `vitest`, which test runner adapters should ship built into `tillr-gate` versus living in consumer repositories via `.tillr.toml` `[gate.adapters.<name>]`?**

   Every adapter built into `tillr-gate` travels automatically with `tillr` installations (`yolo-packs/tillr`, Homebrew, `install.sh`, `just install`) and is maintained and contract-tested in this repository, whereas `.tillr.toml` `[gate.adapters.<name>]` lets any external repository plug in an adapter executable speaking `gate.adapter.*` v1.

   - **A — Ship `go-test` and `vitest` built into `tillr-gate` now; external repositories on other stacks declare `[gate.adapters.<name>]` in `.tillr.toml` (or keep opaque `command` checks), and promote new adapters (`pytest`, `cargo-test`, etc.) into `tillr-gate` only when an operated repository actually exercises them.** Avoids shipping and maintaining unexercised adapters for toolchains (`pytest`, `cargo`, `mvn`) not installed in this repository's own gate.
   - **B — Ship `go-test`, `vitest`, and a generic `junit-xml` command-template adapter built into `tillr-gate`.** Lets external repositories using `pytest --junitxml`, `cargo nextest`, RSpec, or JUnit get per-test failure reading and template-driven test selection from `.tillr.toml` configuration alone without writing a wrapper script, at the cost of maintaining JUnit XML dialect normalization in `tillr-gate`.
   - **C — Ship `go-test`, `vitest`, `pytest`, and `cargo-test` built into `tillr-gate` from the start, tested against recorded runner fixtures.** Covers the four most common CLI ecosystems out of the box, at the cost of maintaining fixture-only adapters for Python and Rust without live runners in this repository's CI.

   <!-- vantage: question id=OQ-TRA3 leaning="A — ship go-test and vitest built in now; add built-in runners only when an operated repo exercises them" -->

   _Leaning (designer's leaning, not an owner ruling):_ **A** — `go-test` and `vitest` are live-tested on every trunk proof in this repository, while `[gate.adapters.<name>]` in `.tillr.toml` gives external repositories an immediate, documented extension point without shipping unexercised adapters.

   **Answer:**

   > _(empty — fill in when decided)_

---

## Decision Ledger

Every entry below is a **designer's proposal from an operator-filed request that the owner may overturn** ([`D-PR2`](../tillr/decision-provenance-review.md#decision-ledger), [`OQ-PR22`](../tillr/decision-provenance-review.md#OQ-PR22)), not an owner ruling.

| ID | Provenance & Status | Decision | Rationale |
| :--- | :--- | :--- | :--- |
| <a id="D-TRA1"></a>`D-TRA1` | **Designer's decision (from an operator request); the owner may overturn this.** | Define the test runner adapter boundary as a child-process CLI (`list`, `run`, and optional change-scoped `select`, accepting `--request <req.json>`, `--output <out.json>`, and optional `--events <events.jsonl>`), exit codes `0`–`6` matching [`tools/tillr/record/exit.go`](../../tools/tillr/record/exit.go) and [`D-GR16`](../reference/gate.md#D-GR16), and `record_version: 1` `record.Envelope` schemas (`gate.adapter.list-*`, `gate.adapter.select-*`, `gate.adapter.run-*`, `gate.adapter.event`), pinned by provider contract examples under `tools/tillr-gate/contract/`. Adapters report raw test observations (`pass`, `fail`, `skipped`, `absent`, `failure_kind`, `unattributed_errors`); `tillr-gate` alone runs `checkresult.Classify`. | Aligns with the `2026-11-21` utility-contracts ruling (`P1`–`P6`), [`D-GR23`](../reference/gate.md#D-GR23)/[`D-GR28`](../reference/gate.md#D-GR28) single-classifier rule, and [Landing Principles 1, 8, and 14](./landing-principles.md#1-a-refusal-must-be-a-verdict). |
| <a id="D-TRA2"></a>`D-TRA2` | **Designer's decision (from an operator request); the owner may overturn this.** | Identify every test across runs, worktrees, and commits by the structured tuple `TestID{adapter, suite, case []string, variant?}`, using repo-relative `suite` identifiers and ordered hierarchy slices `case` (never line numbers, shard indices, absolute paths, or delimiter-split strings). Define `FormatTestKey(id)` to emit `go-test:<module>:<pkg>:<TestName>` for standard Go package tests and `"test:" + compactCanonicalJSON(id)` otherwise. | Preserves spaces, slashes, colons, and nested `describe` blocks in Vitest/non-Go suites while remaining 100% compatible with [`D-TPF1`](./trunk-proof-per-test-flaky-judgment.md#D-TPF1) in [`trunk-proof-per-test-flaky-judgment.md`](./trunk-proof-per-test-flaky-judgment.md) and existing `go-test:...` selectors. |
| <a id="D-TRA3"></a>`D-TRA3` | **Designer's decision (from an operator request); the owner may overturn this.** (Built-in scope beyond Go/Vitest gated on [`OQ-TRA3`](#OQ-TRA3).) | Add an optional `"adapter"` object on `<checks_dir>/<id>.json` mutually exclusive with `"command"` (`R-TW-DR2`: zero fallback between adapted and opaque checks; checks without `"adapter"` keep running as opaque commands). Resolve `adapter.name` first from `[gate.adapters.<name>]` in `.tillr.toml` (supporting external repositories) and then from `tillr-gate`'s built-in adapter subcommands, failing with exit `4` (`"could not judge"`) on an unknown adapter. | Keeps all 20+ non-test checks and unmigrated consumer checks untouched (`c6`), supports external repositories without forking `tillr-gate`, and prevents silent fallback (`R-TW-DR2`). |
| <a id="D-TRA4"></a>`D-TRA4` | **Designer's decision (from an operator request); the owner may overturn this.** (`observatory-web` pre-merge vs. post-merge placement gated on [`OQ-TRA2`](#OQ-TRA2).) | Build the first two built-in adapters inside `tillr-gate`: (1) `go-test`, unifying `shards`, `goaffected`, unsharded module checks (`go-test-tillr-cli`), and file-mode Go checks (`observatory-dev`, `format-test` using `suite: "file:<rel-path>"`); and (2) `vitest` (`vitest list --json`, `vitest run --reporter=json`, `-t` sibling-skip filtering, `exclude_tests`, and `vitest list --json --related`), separating `tsc --noEmit` from Vitest test execution. | Eliminates three duplicate Go runner paths across `tillr-gate` and `tillr-drain` and proves the contract against a real non-Go front-end runner (`70` `.test.ts`/`.test.tsx` files in `tools/tillr-view/web/tests/`). |
| <a id="D-TRA5"></a>`D-TRA5` | **Designer's decision (from an operator request); the owner may overturn this.** (Verify-seat interaction gated on [`OQ-TRA1`](#OQ-TRA1).) | Attach `test_report_ref` and leaf `failing_tests` on `VerdictCheck` in `gate.verdict`, expose `tillr-gate test-list` and `tillr-gate test-run`, and switch `tillr-drain` / `tillr-loop` refusal extraction (`returned.go`, `refusal_cases.go`), base replay (`steps.go`), trunk proof & `FindFirstRed` (`heavy_steps.go`, `heavy_attribution.go`), `settleFixerGate` (`fixer_gate.go`), mechanical item verification (`settle_judge.go`), and verifier-less split closing checks (`splitclosing`) onto those structured records and CLI faces. | Replaces `fork-check: triage --` and `--- FAIL:` log scraping across all six test-running sites while keeping opaque command checks unchanged. |
| <a id="D-TRA6"></a>`D-TRA6` | **Designer's decision (from an operator request); the owner may overturn this.** | Persist an optional structured `test_selection` (`[]TestSelectionEntry{check_id, tests []TestID, prove_disabled}`) on items in `tillr-item` / `tillr-store`, populated by `routeTrunkFixer`, `sweepDisabledMarkers`, split part creation (`D-SP18`), and `tillr-item create --test-selection`, and matched directly by `holdForOpenTestFix` and `linkMatchingOpenFixers`. | Replaces English-prose substring heuristics in `test_fix_hold.go` and `heavy_link.go` with exact `TestID` matching (`R-TW-DR2`, [Landing Principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)). |
| <a id="D-TRA7"></a>`D-TRA7` | **Designer's decision (from an operator request); the owner may overturn this.** | Draw a strict non-overlapping ownership boundary with [`trunk-proof-per-test-flaky-judgment.md`](./trunk-proof-per-test-flaky-judgment.md), [`gate-test-caching.md`](./gate-test-caching.md), and [`fast-gate-same-tool-proof-misses.md`](./fast-gate-same-tool-proof-misses.md): this design owns the runner-agnostic wire contract, `TestID`, check/`.tillr.toml` adapter declaration, `go-test` and `vitest` adapters, structured `test_selection` on items, and non-proof call sites, while delegating trunk-proof flake-probe policy ([`OQ-TPF1`](./trunk-proof-per-test-flaky-judgment.md#OQ-TPF1)–[`OQ-TPF4`](./trunk-proof-per-test-flaky-judgment.md#OQ-TPF4)), test-cache soundness ([`OQ-GC1`](./gate-test-caching.md#OQ-GC1)–[`OQ-GC5`](./gate-test-caching.md#OQ-GC5)), and Go `go-affected` package deferral ([`OQ-FGM1`](./fast-gate-same-tool-proof-misses.md#OQ-FGM1)) to their respective documents. | Satisfies `c7` with zero double-settled decisions across concurrent design documents. |
