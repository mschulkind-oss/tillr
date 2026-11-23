---
title: "Evidence Registry and Whole-Suite Lane Guardrail — Plan Seed"
status: draft
stage: SKETCH
updated: 2026-11-20
next: "Wait for the owner's ruling on OQ-ER1; split into fenced build slices once tillr-evidence-registry-and-suite-guardrail.md is accepted"
depends-on:
  - ./tillr-evidence-registry-and-suite-guardrail.md#OQ-ER1
related:
  - ./tillr-evidence-registry-and-suite-guardrail.md
  - ./tillr-lane-shims.md
  - ./gate-rework.md
  - ./session-recording.md
---

# Evidence Registry and Whole-Suite Lane Guardrail — Plan Seed

**Status:** 2026-11-20 — seeded during design; incomplete until [tillr-evidence-registry-and-suite-guardrail.md](./tillr-evidence-registry-and-suite-guardrail.md) is ruled and accepted.

Seed for the implementation plan of [tillr-evidence-registry-and-suite-guardrail.md](./tillr-evidence-registry-and-suite-guardrail.md). On any conflict about behavior, the design wins. Do not build from this sketch while [OQ-ER1](./tillr-evidence-registry-and-suite-guardrail.md#OQ-ER1), the design's only remaining question, is open.

---

## Proposed build phases (as surfaced during design)

The phases below separate `tools/tillr-gate/`, `tools/go-test-shards.py` + `tools/tillr-run/`, `tools/tillr-drain/`, and `tools/tillr-view/` so independent module slices can run in parallel once Phase 1 defines the shared on-disk evidence schema.

| Phase | Scope & target files | Gated on | Done when |
| :--- | :--- | :--- | :--- |
| **1. Canonical `Format: 3` Evidence Registry in `tillr-gate` and `fork-check.py` bridge** | [`tools/tillr-gate/memory.go`](../../tools/tillr-gate/memory.go), [`tools/tillr-gate/main.go`](../../tools/tillr-gate/main.go) (`forget` face), [`tools/fork-check.py`](../../tools/fork-check.py) (`--evidence-out` structured triage & `by-key` bridge for remaining Go/Python suites). | [OQ-ER1](./tillr-evidence-registry-and-suite-guardrail.md#OQ-ER1), ledger review (`D-ER1`–`D-ER3`) | `<git-common-dir>/tillr-gate/evidence/by-key/<fingerprint>.{pass,fail}.json` and `by-commit/<sha>.json` write atomically with embedded `FailingCase` entries and `log_sha256`; a `fingerprint` with both `.pass.json` and `.fail.json` marks `memory: "contradicted"` and stops short-circuiting as `remembered`. |
| **2. Cross-stage reuse in `HeavyRunner`, `fixer-gate`, and `base-replay`** | [`tools/tillr-drain/heavyrun/heavy.go`](../../tools/tillr-drain/heavyrun/heavy.go), [`tools/tillr-drain/heavyrun/heavy_progress.go`](../../tools/tillr-drain/heavyrun/heavy_progress.go), [`tools/tillr-drain/drain/heavy_loop.go`](../../tools/tillr-drain/drain/heavy_loop.go), [`tools/tillr-drain/drain/trunk.go`](../../tools/tillr-drain/drain/trunk.go), [`tools/tillr-loop/tillr-loop`](../../tools/tillr-loop/tillr-loop). | Phase 1 | `HeavyRunner` and `base-replay` read and publish `by-key` and `by-commit` entries so suites proved at `tillr-gate` or on an ancestor tip skip execution across `fixer-gate`, `tillr-gate`, and `HeavyRunner`; contradicted fingerprints follow the owner's [OQ-ER3](./tillr-evidence-registry-and-suite-guardrail.md#OQ-ER3) ruling. |
| **3. Worker `$TILLR_EVIDENCE_FILE` snapshot and `## Facts carried` trust contract** | [`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go), [`tools/tillr-run/backends/brief-template.md`](../../tools/tillr-run/backends/brief-template.md), [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go), [`tools/tillr-drain/drain/plain_stage.go`](../../tools/tillr-drain/drain/plain_stage.go), [`tools/tillr-drain/drain/refusal_cases.go`](../../tools/tillr-drain/drain/refusal_cases.go), [`tools/tillr-drain/drain/landing_call.go`](../../tools/tillr-drain/drain/landing_call.go). | Phase 1 and ledger review (`D-ER4`, `D-ER7`) | Initial runs (`fixer-main-*`, reverted items, `trunk` returns, and ordinary starts) and resumed gate/review returns stage `<run-scratch>/evidence.json` + copied logs under `<run-scratch>/evidence-logs/`, render the bounded digest + trust wording in `## Facts carried`, and normalize legacy `go test ./...` / `go-test-shards.py` lines in `{{VERIFICATION}}`. |
| **4. Two-seam Whole-Suite Guardrail (`go` shim + `go-test-shards.py` + `fork-check.py`)** | [`tools/go-test-shards.py`](../../tools/go-test-shards.py), [`tools/fork-check.py`](../../tools/fork-check.py), [`tests/go-test-shards.test.py`](../../tests/go-test-shards.test.py), and `tools/tillr-run/shims/go` (extending the `<run-scratch>/shims/bin` + `config.env` layout in [`tillr-lane-shims.md`](./tillr-lane-shims.md)). | [OQ-LS1](./tillr-lane-shims.md#OQ-LS1), [OQ-LS2](./tillr-lane-shims.md#OQ-LS2), ledger review (`D-ER5`, `D-ER6`, `D-ER8`) | Under `TILLR_RUN_ID`, in-repo whole-module `go-test-shards.py` runs, `--slot-class proof`, `fork-check.py` gate runs, unnarrowed `go test ./...`, and unnarrowed `> 25`-test packages exit `1` with the diff-derived `-run` command and `-exec true` compile command while `$TMPDIR` test fixtures stay green and refusals append `kind: "shim.refusal"` (`rule: "whole-suite"`) to `$TILLR_TRACE_DIR/shim-refusals.jsonl`. |
| **5. Refusal trace and reuse visibility on operator surfaces** | [`tools/tillr-trace/`](../../tools/tillr-trace/) (`refusals` and `run` views per [`tillr-lane-shims.md`](./tillr-lane-shims.md)), [`tools/tillr-view/`](../../tools/tillr-view/) (Observatory Run Detail + gate/proof `reused`/`executed` check counts). | Phase 4, [OQ-LS4](./tillr-lane-shims.md#OQ-LS4), and ledger review (`D-ER8`) | `tillr-trace refusals`, `tillr-trace run <id>`, and Observatory Run Detail display whole-suite refusals (plus any dashboard summary settled in [OQ-LS4](./tillr-lane-shims.md#OQ-LS4)) alongside gate and trunk-proof `reused`/`executed` check counts without derived `.tillr/state.db` trace writes. |

---

## Checks for the planner when turning this sketch into build slices

- **Share one shim directory and trace stream with [`tillr-lane-shims.md`](./tillr-lane-shims.md):** `tools/tillr-run/shims/go` lives in the same `tools/tillr-run/shims/` directory, reads the same read-only `config.env` (`D-LS1`), strips `$TILLR_SHIM_BIN` before chaining down `PATH` (`D-LS2`), and appends `kind: "shim.refusal"` to `$TILLR_TRACE_DIR/shim-refusals.jsonl` (`D-LS10`). Do not create a second shim directory or second refusal JSONL file.
- **Single-writer overlap in `tools/tillr-drain/drain/`:** Phases 2 and 3 touch `tools/tillr-drain/drain/`; schedule them sequentially or keep their file sets disjoint (`heavy*.go`/`trunk.go` vs `stage.go`/`refusal_cases.go`/`landing_call.go`). Phases 4 and 5 (outside `drain/`) can build in parallel with Phases 1–2.
- **Unit-test fixture invariance:** verify in Phase 4 that running `python3 tests/go-test-shards.test.py` and `python3 tests/fork-check.test.py` *with `TILLR_RUN_ID=test-run` set in the environment* passes completely because their synthetic modules live under `$TMPDIR` outside `$TILLR_REPO_ROOT`.
- **Documentation contract test:** any new `tillr gate <face>` documented in `tools/tillr-gate/README.md` or the `tillr-operating` skill must ship with `--help` support in the same commit so [`tests/tillr-docs.test.py`](../../tests/tillr-docs.test.py) stays green, and [`docs/tillr/design-status.md`](../tillr/design-status.md) keeps both new documents marked `not shipped` until shipped pack docs are intentionally refreshed via `just pack-docs`.
