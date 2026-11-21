# Request for comments — `tillr` for AutoQA and Product QA

> **This is a task brief, not a briefing.** Hand it to the agent dispatched to review these
> surfaces and write comments, and to nothing else. It shipped as a pack briefing once, which
> meant every agent in every workspace selecting this pack was told to deliver design comments
> instead of doing its job; a briefing is a house rule, and this is one review assignment.

You are reviewing `tillr` from the **Product QA** and **AutoQA** side. **Your deliverable is
design comments and concrete integration requirements only — do not edit tillr source, do not
write tillr tests, and do not commit to tillr.** (If you are running inside your own project's
repository, record your reply in a tracked design document there so the owner can relay it, as
[`tillr-designs`](../skills/tillr-designs/SKILL.md) describes.)

The two QA efforts in tillr's documents are distinct, and this request asks what either side
needs from tillr now:

- **Product QA** is a product bug-finding pipeline in its own repository, tillr's first
  external consumer. Its first two rounds of requests and tillr's answers live in
  [`docs/design/tillr-separable-parts.md`](../../../docs/design/tillr-separable-parts.md)
  (`~/.local/share/tillr/docs/design/tillr-separable-parts.md`).
- **AutoQA** ([`docs/design/tillr-autoqa.md`](../../../docs/design/tillr-autoqa.md),
  `~/.local/share/tillr/docs/design/tillr-autoqa.md`) is tillr's own exploratory self-testing
  design — currently iceboxed on the roadmap, while sharing the same standalone store, runner,
  verdict, and inspection seams.

## What is settled and built today

Checked against the tree on 2026-11-19 (`303ad3fcf`), the early prototype gaps (two-item static
seed, unbuilt event emitters, single-purpose Git landing) have been overtaken:

1. **Core and charter split (`definition.json`).** All eighteen items of
   [`docs/design/tillr-definitions-plan.md`](../../../docs/design/tillr-definitions-plan.md)
   have landed. `tillr store init` creates every module's tables up front and binds the store's
   charter (`git-landing` by default, or a custom `definition.json` declaring states,
   transitions, `completes: "hook"`, and a `complete-item` hook program; documented in
   [`tools/tillr/definition/README.md`](../../../tools/tillr/definition/README.md)). A worked
   non-Git example in
   [`tools/tillr/examples/recorded-verdict/README.md`](../../../tools/tillr/examples/recorded-verdict/README.md)
   (`~/.local/share/tillr/tillr/examples/recorded-verdict/`) runs reproduction and investigation
   stages from an empty directory and finishes findings through `tillr item verdict` with no Git
   repository, gate, or landing.
2. **Process-and-record contract.** External callers compose the eight binaries on `PATH`
   (`tillr` and `tillr-store`, `tillr-item`, `tillr-run`, `tillr-drain`, `tillr-gate`,
   `tillr-land`, `tillr-view`) with explicit `--store`, `--root`, and `--lane-root` paths, exit
   codes `0..5` (plus `6` `stalled` on `tillr-gate`), and `record_version: 1` JSON envelopes
   (`producer.system: "tillr"`). The promised consumer surface is
   [Section 2.1 of `tillr-separable-parts.md`](../../../docs/design/tillr-separable-parts.md#21-the-stable-surface);
   Go packages, SQLite schemas, planner hold-reason strings, and `.tillr/loop` layout remain
   [internal in Section 2.2](../../../docs/design/tillr-separable-parts.md#22-what-is-internal-and-may-change-daily).
3. **Store events, read views, standalone gate checks, and tracing.** The binaries emit
   structured events (`item.*`, `run.*`, `drain.*`, `call.*`, `land.*`) readable by cursor via
   `tillr store events` and `tillr view events`. `tillr view` provides `status`, `waiting`,
   `passes`, `lanes`, `running`, `trace`, `perf-log`, and the embedded web observatory
   (`tillr view serve`). `tillr-gate` provides no-store `change`, `plan`, and `shadow` faces
   alongside `checks`, `run`, and `dualrun`. `tillr-trace` records the commands a run executes.
4. **Intake, workflows, worker endings, backends, and trunk proof.** Store-declared requests
   (`tillr item request`) and reserved `express` lanes run alongside `tillr item sync` and
   `tillr item apply`. Build work runs `planned-build@v1` (`plan`, `work`, and `verify`, whose
   persisted rulebook allows `verification-only`) or single-step `single-worker@v1`. `pi` workers use `land` and
   `obstacle` suspended tool calls when `pi --features` advertises `parked-tool-calls`
   (reopening promptlessly on refusal, with fallback to `RESULT:` footers), recover from
   `repeating-call` loops on a fresh session and fallback backend, route seats across Copilot,
   Bedrock, and headless Claude policies, and prove heavy checks after merge (`refs/tillr/green`)
   with `auto_revert` and path-intersection landing (`pass-inherited` vs `trunk-already-red`)
   under a red check.
5. **Still moving or unbuilt.** Four small consumer items remain unfiled (`P2` explicit-store
   recipe, `P3` stock plain-command adapter, `P4` contract change feed, `P6` standalone dead-run
   probe), raw `tillr item state` terminal-evidence guards are queued, and two items from the
   2026-11-19 latency report remain open (`deferJudge` two-pass settlement and `SQLITE_BUSY`
   lock-timeout retry/fault handling). Companion `<topic>-plan.md` files, mandatory code diff
   review, the compliance officer, whole-life item graphs, and log-per-item storage are proposed
   only — do not assume them.

## How to inspect and try the pieces

All eight binaries are on `PATH`, and their Go source, worked examples, module `README.md`
files, and design snapshot sit under `~/.local/share/tillr/` (summarized in
[`yolo-packs/tillr/README.md`](../README.md), [`tillr-designs`](../skills/tillr-designs/SKILL.md),
and [`tillr-operating`](../skills/tillr-operating/SKILL.md)):

- Run any binary with `--help` to list its faces, and `<binary> <face> --help` to inspect its
  flags (flags live on `--help` rather than in prose).
- Read `~/.local/share/tillr/docs/tillr/design-status.md`,
  `~/.local/share/tillr/docs/design/tillr-separable-parts.md`, and
  `~/.local/share/tillr/tillr/definition/README.md`.
- Run the non-Git charter walkthrough in
  `~/.local/share/tillr/tillr/examples/recorded-verdict/README.md` in a fresh temporary
  directory (never against a live store).

## Questions to answer

Inspect the binaries, module references, and worked example above, then return concrete,
numbered comments on the five areas that matter now:

1. **Store, events, and read views for your own gates and verdicts.** To drive your own
   reproduction commands, gates, `complete-item` hooks, or `tillr item verdict`, `judge`, and
   `decision` flows, what exact records, event kinds, correlation IDs, cursor guarantees, or
   `tillr view` / `tillr store` read outputs do you still need — or find missing, underspecified,
   or awkward when you run them? For agent verdicts, test against a selected workflow whose
   evaluator lists the stored criterion class in `judge_classes`. The verdict is bound to
   that step and the exact candidate, not to a renewable approval.
2. **Filing work and reading its outcome from another repository.** What do you need to file
   items, findings, or requests into a tillr store (including across repository boundaries) and
   read back progress, artifacts, criterion verdicts, and terminal outcomes without polling
   private files or querying SQLite directly? Where do `tillr item apply`, `tillr item request`,
   `tillr item list`, `tillr item show`, `tillr item mail`, or `definition.json` hooks fall
   short?
3. **Which parts you would depend on first.** In what order would you adopt tillr's pieces — for
   example, standalone `tillr-store` + `tillr-item` + `tillr-run`, `tillr-drain loop` under a
   custom non-landing charter like `recorded-verdict`, no-store `tillr-gate` checks, or
   `tillr-view` — and which pieces can stay out of your first integration?
4. **What would break you if it changed.** Which exact CLI flags, exit codes, record fields,
   event payloads, `TILLR_*` environment variables, directory paths, or state transitions would
   break your pipeline if tillr changed them without a version bump — including anything
   currently marked internal in `tillr-separable-parts.md` Section 2.2 that you find yourself
   forced or tempted to touch?
5. **Running and debugging a drain in a repository without direct access.** The owner highlighted
   *"the pain of fixing things that break in other repositories when we don't have direct
   access"*. When a drain loop, backend adapter, hook program, gate check, or SQLite store
   stalls, faults, or misbehaves inside a repository tillr's maintainers cannot log into, what is
   missing today? Consider preflight/doctor checks, dry-run validation of charters/workflows/step
   programs, self-contained and redacted diagnostic bundles (from `tillr store check`,
   `inventory`, `events`, `tillr view waiting`, `passes`, `trace`, and run logs), and operator
   recovery faces that avoid hand-editing state.
