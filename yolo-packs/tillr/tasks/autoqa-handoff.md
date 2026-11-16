# AutoQA review handoff — `tillr` component seams

> **This is a task brief, not a briefing.** Hand it to the agent dispatched to review these
> pieces, and to nothing else. It shipped as a pack briefing once, which meant every agent in
> every workspace selecting this pack was told to deliver design comments instead of doing its
> job; a briefing is a house rule, and this is one lane's assignment.


You are reviewing `tillr` from the AutoQA perspective. **Your deliverable is design comments only — do not edit source, do not write tests, and do not commit.**

## Current State (Not Finished)

A live unattended tick has landed a real candidate through these eight binaries, but the toolset is intentionally shipped early with three known gaps:

1. **Queue item-state moves just landed** and have minimal operational mileage.
2. **The seed holds only two dispatchable items.**
3. **The recording layer's emitters (`tools/tillr`) are unbuilt** — the library types exist, but the binaries do not yet emit the full event stream.

## How to Inspect the Pieces

All eight binaries are on your `PATH`, and their source + per-module `README` files are installed at `~/.local/share/tillr/`:

- Umbrella dispatcher: `tillr` (nouns `store`, `item`, `land`, `drain`, `run`, `gate`, `view`)
- Gate execution & comparison: `tillr-gate` (faces `dualrun`, `plan`, `run`) and `~/.local/share/tillr/tillr-gate/`
- Lane execution & lifecycle: `tillr-run` (faces `exec`, `lanes`, `status`, `release`) and `~/.local/share/tillr/tillr-run/`
- Item verdicts & grants: `tillr-item` (faces `apply`, `seed`, `show`, `state`, `decision`, `mail`, `grant`, `verdict`) and `~/.local/share/tillr/tillr-item/`
- Store & read views: `tillr-store` (faces `init`, `seed`, `check`, `events`) and `tillr-view` (faces `status`, `events`, `serve`)
- Loop orchestration reference: read the `tillr-operating` skill (`SKILL.md`).

Run any binary with no arguments to print its faces; a face invoked without its required flags names them in its refusal.

## What to Return

Inspect the binaries and the source tree above, then return structured design comments answering:

1. **Standalone invocation:** Can AutoQA drive `tillr-gate` (`plan`, `run`, `dualrun`) and `tillr-run` (`exec`, `status`, `release`) independently without running `tillr-drain`? Which required flags or state preconditions couple them to the drain loop today?
2. **Verdict & decision payloads:** What input/output schema does AutoQA need from `tillr-item verdict` and `tillr-item decision` to post a QA pass/fail or request human review?
3. **Missing event emitters:** Before the `tools/tillr` recording emitters are built, which exact events, fields, and correlation IDs must `tillr-gate`, `tillr-run`, and `tillr-land` emit so AutoQA can reconstruct a run from `tillr-view events`?
4. **Refusal & exit contracts:** Where do the current binaries lack machine-readable output or distinct exit codes that an unattended QA harness requires?
