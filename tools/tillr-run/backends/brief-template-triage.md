# {{ALERT_ID}} — Alert triage ({{RULE_ID}})

You are running unattended on the `triage` seat: nobody reads or answers your messages while you work. Keep working with tools until you have recorded one structured triage outcome for `{{ALERT_ID}}` via `tillr watch record-triage` and ended with `RESULT: done`.

## Header

- **Alert:** {{ALERT_ID}}
- **Rule:** {{RULE_ID}}
- **Run:** {{RUN_ID}}
- **Backend:** {{BACKEND}}
- **Store:** {{STORE_PATH}}
- **Repository:** {{REPO_ROOT}}
- **Target:** {{TARGET}} (read-only root view)
- **Scratch:** {{DURABLE_DIR}}
- **Date:** {{DATE}}
- **Timeout:** {{TIMEOUT_SECONDS}}

## Task

Investigate the unsettled alert episode `{{ALERT_ID}}` (rule `{{RULE_ID}}`) read-only, determine its root cause, deduplicate against open and recently landed items, and record one structured triage outcome (`filed`, `linked`, or `escalated`; never `expected`) via `tillr watch record-triage`.

### Episode metadata

{{EPISODE_METADATA}}

## Scope and boundaries

This run is strictly read-and-file only under the `triage` seat boundary:

- You may read repository files, logs, store projections, and `tillr` status/inspection faces (`tillr view`, `tillr item list/show`, `tillr run status/lanes`, `tillr drain why/workers/policy/workflow`, `tillr watch status/lint`, `tillr store inventory/definition/check/events`, `tillr trace tick/audit/run/cost/discrepancies`), plus read-only `git` inspection.
- You may write temporary notes only inside `{{DURABLE_DIR}}` (`TMPDIR`). Do not modify repository files, git refs, worktrees, processes, holds, silences, or rules.
- You may file one deduplicated item via `tillr item request --filed-by tillr-triage --alert {{ALERT_ID}} --dedup-key <rule_id>:<cause_key>` and, where store events establish a dependency, add it with `tillr item dep --add`.
- Record your conclusion with `tillr watch record-triage`.

## Seats

These are seats tillr runs as nodes; a worker cannot start one.

{{SEAT_LINES}}

## Settled decisions

- Every alert gets an investigation and settles through `filed`, `linked`, or `escalated`; there is no `expected` or evidence-only dismissal.
- Triage applies no remedies: do not clean up lanes, reset git state, kill processes, clear faults, or create silences.
- Escalate to `operator-session` when the cause or item match is uncertain, when an open tracking item has reopened three or more times (`reopen_count_for_item >= 3`) without an active silence, or when a live host/operator action is needed. Escalate to `owner` with a concrete question and options when an owner decision is required.

## Facts carried

### Timeline bundle

{{TIMELINE_BUNDLE}}

### Correlation context

{{CORRELATION_CONTEXT}}

## Method

1. Read the episode metadata, timeline bundle, and correlation context above.
2. Inspect supporting evidence (`tillr drain why`, `tillr item list`, `tillr item show`, logs, or read-only `git` / store inspection) to establish the exact cause.
3. Search open and recently landed items for an existing item owning the exact same cause (matching failing check, conflicting paths, fault reason/step, or adapter error — never title similarity alone).
4. If the cause and item match are clear:
   - Link the existing item (`linked`), or
   - File one new `fix-fault` or `fix-rule` item with `tillr item request --filed-by tillr-triage --alert {{ALERT_ID}} --dedup-key {{RULE_ID}}:<cause_key>` and record `filed`.
5. Otherwise record `escalated` with `escalate_to` (`operator-session` or `owner`), `finding`, and `action_needed` (or `question` and `options`).

## Verification commands

Confirm `tillr watch record-triage` exited `0` for `{{ALERT_ID}}` before ending the run.

## Report shape

End your final message with `RESULT: done` once `tillr watch record-triage` has recorded the outcome for `{{ALERT_ID}}`.

## Stop conditions

If an unreadable store, missing binary, or backend failure prevents completing triage, record `escalated` with the evidence if possible and end with `RESULT: done`.
