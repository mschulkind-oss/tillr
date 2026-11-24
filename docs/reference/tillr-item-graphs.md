---
title: "Item graphs — tracked workflows, node runs, and bounded refinement"
status: accepted
stage: CURRENT
verified: 2026-11-20
verified_commit: 412259f25ae143948671832e310e7cd34ee03f5c
covers:
  - config/tillr/workflows/
  - config/tillr/commands.json
  - tools/tillr/settings/settings.go
  - tools/tillr-item/item/workflow.go
  - tools/tillr-item/item/workflow_selection.go
  - tools/tillr-item/workflowmeta/read.go
  - tools/tillr-drain/drain/workflow.go
  - tools/tillr-drain/workflow/workflow.go
  - tools/tillr-drain/workflow/workflow_check.go
  - tools/tillr-drain/workflow/workflow_arguments.go
  - tools/tillr-drain/drain/workflow_defaults.go
  - tools/tillr-drain/drain/workflow_exit.go
  - tools/tillr-drain/drain/workflow_exit_record.go
  - tools/tillr-drain/drain/workflow_evaluator.go
  - tools/tillr-drain/drain/workflow_failover.go
  - tools/tillr-drain/drain/workflow_order.go
  - tools/tillr-drain/drain/workflow_brief.go
  - tools/tillr-drain/drain/graph_proposal.go
  - tools/tillr-drain/drain/node_outputs.go
  - tools/tillr-drain/drain/command.go
  - tools/tillr-drain/drain/parallel.go
  - tools/tillr-drain/drain/build_readers.go
  - tools/tillr-drain/drain/worker_session.go
  - tools/tillr-drain/drain/dispatches.go
  - tools/tillr-run/run/schema.go
  - tools/tillr-land/land/trunk.go
  - tools/tillr-view/item_nodes.go
  - tools/tillr-view/lane_nodes.go
  - tools/tillr-view/graph_revisions.go
  - tools/tillr-view/web/src/components/ItemGraph.tsx
  - tools/gate/checks/workflows.py
tags: [tillr, graphs, workflows, drain, reference]
summary: "How tillr expands versioned JSON workflows into finite, cycle-free item graphs of agent and command nodes, runs readers in parallel and writers serially on one lane, applies bounded mid-attempt graph proposals, evaluates or settles graph exits, and renders node runs and revisions in the observatory."
---

# Item graphs — tracked workflows, node runs, and bounded refinement

**Status:** Verified 2026-11-20 against `412259f25ae143948671832e310e7cd34ee03f5c`.

**Change to the shipped behavior described below.** On 2026-11-19 the owner asked of the item fence, "didn't we already rule that this is basically impossible and wasteful?" and answered the operator's proposal, "yes, file that." Under the operator request (which the owner may overturn), expansion and dispatch need no list for the item or its writing pieces. Descriptions below of subset validation, staged `fence.txt` files and `fenceViolation` describe the old implementation. Read-only access and serial writers remain; the [common deny-list](../design/tillr-vocabulary.md#51-the-work-and-its-statement) applies to every worker.

An item in tillr represents one deliverable with one set of frozen criteria, and — for Git-landing definitions — one gate run and one squashed trunk commit. Inside an attempt, that item may execute as a finite, cycle-free **item graph** expanded from versioned JSON workflow files under [`config/tillr/workflows/`](../../config/tillr/workflows/). Each executable node is one `tillr run exec` invocation — either an agent on a policy-mapped seat or a registered command — sharing the item's claimed lane and worktree. Ready read-only nodes run together up to `max_parallel`; writing nodes chain commits one at a time in stage order; and a designated planning stage may propose replacing a later, unstarted stage with another tracked workflow inside one atomic store transaction.

In this repository, [`.tillr.toml`](../../.tillr.toml) currently sets no `[workflows.default]` table, so document-declared items without an explicit `workflow` attribute run as the implicit `single-worker@v1` worker, whereas `tillr item request` (`planned-build@v1`, `scouted-design@v1`), `tillr item doc-triage` (`doc-triage@v1`), and explicit `workflow` blocks run multi-node graphs.

| Component | Lives in |
| :--- | :--- |
| Tracked workflow definitions, command registry, and planner guidance | [`config/tillr/workflows/`](../../config/tillr/workflows/), [`config/tillr/commands.json`](../../config/tillr/commands.json), [`config/tillr/plan-guidance.md`](../../config/tillr/plan-guidance.md) |
| Repository `[workflows.default]` settings reader | [`tools/tillr/settings/settings.go`](../../tools/tillr/settings/settings.go) (`WorkflowDefaults`) |
| Workflow reference grammar, request defaults, and stored selection reader | [`tools/tillr-item/item/workflow.go`](../../tools/tillr-item/item/workflow.go), [`request.go`](../../tools/tillr-item/item/request.go), [`workflow_selection.go`](../../tools/tillr-item/item/workflow_selection.go), [`workflowmeta/read.go`](../../tools/tillr-item/workflowmeta/read.go) |
| Loader, static expansion, argument binding, `workflow check` CLI, and gate check | [`tools/tillr-drain/drain/workflow.go`](../../tools/tillr-drain/drain/workflow.go), [`workflow_check.go`](../../tools/tillr-drain/workflow/workflow_check.go), [`workflow_arguments.go`](../../tools/tillr-drain/workflow/workflow_arguments.go), [`tools/gate/checks/workflows.py`](../../tools/gate/checks/workflows.py) |
| Selection, node advancement, quick-way bypass, proposals, outputs, commands, and parallel readers | [`workflow_defaults.go`](../../tools/tillr-drain/drain/workflow_defaults.go), [`workflow_order.go`](../../tools/tillr-drain/drain/workflow_order.go), [`graph_proposal.go`](../../tools/tillr-drain/drain/graph_proposal.go), [`node_outputs.go`](../../tools/tillr-drain/drain/node_outputs.go), [`command.go`](../../tools/tillr-drain/drain/command.go), [`parallel.go`](../../tools/tillr-drain/drain/parallel.go), [`build_readers.go`](../../tools/tillr-drain/drain/build_readers.go) |
| Exit checks, `no_commit` receipts, `evaluator_slot` routing, failover, and reader reuse | [`workflow_exit.go`](../../tools/tillr-drain/drain/workflow_exit.go), [`workflow_exit_record.go`](../../tools/tillr-drain/drain/workflow_exit_record.go), [`workflow_evaluator.go`](../../tools/tillr-drain/drain/workflow_evaluator.go), [`workflow_failover.go`](../../tools/tillr-drain/drain/workflow_failover.go), [`worker_session.go`](../../tools/tillr-drain/drain/worker_session.go) |
| Multi-commit squash message selection | [`tools/tillr-land/land/trunk.go`](../../tools/tillr-land/land/trunk.go) (`Squash`, `MessageCommit`) |
| Observatory item-graph API, lane node strip, and React views | [`tools/tillr-view/item_nodes.go`](../../tools/tillr-view/item_nodes.go), [`lane_nodes.go`](../../tools/tillr-view/lane_nodes.go), [`graph_revisions.go`](../../tools/tillr-view/graph_revisions.go), [`ItemGraph.tsx`](../../tools/tillr-view/web/src/components/ItemGraph.tsx) |

**Reads with:** [`tools/tillr-drain/README.md`](../../tools/tillr-drain/README.md) (module-level reference for [write scopes](../../tools/tillr-drain/README.md#node-write-scopes), [composed stages](../../tools/tillr-drain/README.md#stages-that-name-workflows), [parallel readers](../../tools/tillr-drain/README.md#parallel-read-only-nodes), and [command nodes](../../tools/tillr-drain/README.md#registered-command-nodes)), [`tools/tillr-item/README.md`](../../tools/tillr-item/README.md) ([store-declared items](../../tools/tillr-item/README.md#workflow-arguments-and-store-declared-items) and [filed requests](../../tools/tillr-item/README.md#filing-a-request)), [the drain successor reference](./tillr-drain-successor.md), [tillr item states](./tillr-item-states.md), [build-commit continuation](./tillr-build-commit.md), [judge and lifecycle](../design/tillr-judge-and-lifecycle.md) (`item.RecordJudge` and evaluator binding), and [the graph-is-the-path design](../design/tillr-graph-is-the-path.md) (the unbuilt proposal to move gate, landing, objections, and hoisted sub-agents into one whole-life graph; this document describes the built per-attempt workflow machinery).

---

## The words

| Term | Meaning |
| :--- | :--- |
| **Workflow** | A tracked, versioned JSON file in `config/tillr/workflows/<name>@v<N>.json` naming stages, edges, arguments, and exit rules (`Workflow` in `workflow.go`). Replaces "graph template". |
| **Stage** | One entry in a workflow's `stages` list (`kind` of `"agent"`, `"command"`, or `"workflow"`). A `"workflow"` stage is expanded away before dispatch and never has a run of its own. |
| **Graph** | The concrete, cycle-free list of expanded executable nodes, edges, scopes, and source SHA-256 digests persisted for an item (`workflow_selections.graph` and `graph_revisions.after_graph`). |
| **Node** | One executable step in an expanded graph (`WorkflowStage` after flattening). Replaces "leaf". |
| **Node run** | One execution of a node inside an item attempt, recorded in `node_runs` and backed by one `runs` row (`tillr run exec`). |
| **In fog** ("not started") | Any node in an attempt's graph that has no row in `node_runs` yet. Only a downstream subtree whose nodes are all not started may be replaced by a proposal (`proposal-target-started`). |
| **Child session** | A sub-agent launched inside one agent run by the harness itself (such as Pi's `subagent` tool), recorded in `child_sessions`. Observed and displayed inside its parent run, never scheduled as a workflow node. |
| **Evaluator slot** | The optional read-only node named by `exit.evaluator_slot` that evaluates the joined candidate through `judgeCandidate` and `item.RecordJudge` before the gate. |

**Intra-item graph vs inter-item lineage.** Steps that converge on one deliverable inside one path fence — scouting, planning, piece-wise edits, command checks, and verification — belong in one item's graph (`D-IG2`). Work that produces separate deliverables with independent fences or landings uses separate items linked by `blocked_by` or `from` (`tillr-item`).

**Workflow nodes vs child sessions.** Use separate workflow nodes when steps need distinct seats, narrowed `write_scope` fences, producer-bound input files, registered commands, or independent evaluator review. When one worker only needs helper read-only searches inside its own context, harness child sessions avoid graph ceremony while still recording tokens and transcripts under the parent run (`D-IG11`, `D-IG12`).

---

## Workflows, selection, and expansion

### Selection and defaults

A workflow reference must match `^[a-z][a-z0-9-]*@v[1-9][0-9]*$` (`item.WorkflowRefValid` in `tools/tillr-item/item/workflow.go`), except that the built-in opt-out `single-worker@v1` (`SingleWorkerWorkflow`) resolves to a `nil` graph with no JSON file on disk and runs the drain's single-worker path.

Workflows reach an item from three places:

1. **Item declaration:** A Markdown item directive may set `workflow="<ref>"` (`sync.go` rejects workflows that declare top-level `args` with `workflow-needs-args`, because directives carry no argument map). Store-declared items filed by `tillr item doc-triage` set `items.workflow = "doc-triage@v1"` with JSON `items.workflow_args`, and `tillr item request` defaults `build` requests to `planned-build@v1` and `design` requests to `scouted-design@v1` (`RequestDefaultWorkflow` in `request.go`, rejecting parameterized workflows with `workflow-needs-args`).
2. **Seat defaults in `.tillr.toml`:** `[workflows.default]` maps seat names to workflow references (`settings.Read` in `tools/tillr/settings/settings.go`). At loop startup, `WorkflowDefaultDiagnostics` (`workflow_defaults.go`) checks every configured seat default (`git ls-files --error-unmatch` plus full expansion) and logs diagnostics without aborting unrelated seats; items on a broken default hold as `workflow-invalid`.
3. **Selection precedence (`selectWorkflow` in `workflow_defaults.go`):**
   - Generated repair items (`IsGeneratedRepair`) always take `single-worker@v1` with `source = "repair"`.
   - If `workflow_selections` already holds a row for `item_id` (`item.ReadWorkflowSelection`), that stored `workflow`, `source`, and expanded `graph` snapshot are reused so edits to tracked workflow files do not alter an already-selected item.
   - Otherwise `items.workflow` wins (`source = "block"`, binding `items.workflow_args` via `loadWorkflowWith`), followed by `WorkflowDefaults[item.Seat]` (`source = "default"`), and finally `single-worker@v1` (`source = "implicit"`).

On the item's first claim, `recordWorkflowSelection` inserts `(item_id, workflow, source, graph)` into `workflow_selections` with `ON CONFLICT(item_id) DO NOTHING`.

### Schema, stage kinds, and static expansion

Each tracked workflow is a regular `.json` file (symlinks and non-`.json` paths fail `workflow-file`) decoded with unknown-field rejection (`workflowFields` and `decodeWorkflow` in `workflow_check.go` and `workflow.go`):

- **Top-level fields:** `name` and integer `version` (must match `<name>@v<version>`), nonempty `stages`, `edges` (list, may be empty), and optional `exit`, `args`, `inputs`, and `max_parallel` (`1` through `8`, defaulting to `4` when omitted).
- **Stage fields and `kind`:** Every stage has a unique `id` containing no `/` or control whitespace (`workflow-stage-id`, `workflow-duplicate-id`) and one of three `kind` values:
  - `"agent"`: requires `seat` (`item.ValidSeatName` and mapped in `policy.Seats`; `access: "write"` on any seat in `policy.ReadOnlySeats` fails `workflow-read-only-seat-writes`) and `access` (`"read_only"` or `"write"`). Accepts optional `prompt`, `write_scope`, `inputs`, `outputs`, `repeat`, `may_replace`, and nonnegative `timeout_ms`. Cannot set `command_ref`, `flags`, `workflow`, or `args`.
  - `"command"`: requires `command_ref` naming an entry in [`config/tillr/commands.json`](../../config/tillr/commands.json), `access` (`"read_only"` or `"write"`), and an empty `seat`. Each registry entry defines a nonempty `argv` (direct shell executables `sh`, `bash`, `zsh`, `dash`, and `fish` are refused by `command-registry`) and a `flags` map from `--`-prefixed flag names to `"string"`, `"number"`, or `"path"`. Supplied `flags` are validated (`path` values must be clean, repository-relative, and cannot escape the repository root through symlinks), sorted by flag name, and appended as `--flag=value` into internal `resolved_argv` (`command.go`; tracked files may never set `resolved_argv` directly).
  - `"workflow"`: requires `workflow` naming another tracked workflow file and specifies no direct run fields (`seat`, `access`, `command_ref`, `flags`, `timeout_ms`). Accepts optional `args`, `inputs`, `outputs`, `write_scope`, and `may_replace`.
- **Arguments and `repeat` (`workflow_arguments.go`):** Top-level `args` maps parameter names to `{type, most}`. Supported `type` values are `"paths"` (nonempty list of repository-relative paths), `"strings"` (requires `most > 0`), `"command"` (registered command name), and `"pieces"` (requires `most > 0`). Whole string values spelled `${args.<name>}` substitute the typed value before decoding. A stage setting `repeat: "<arg>"` expands over:
  - a `"strings"` list on a `"read_only"` stage, producing `<id>-1` through `<id>-N` with `copies_of: "<id>"` and replacing `${item}` in `prompt`; or
  - a `"pieces"` list on an `"agent"` stage, producing one stage per piece in list order with the piece's `id`, `seat`, `access`, `prompt`, optional `outputs`, and (required for `"write"`, forbidden for `"read_only"`) nonempty `write_scope` (writing pieces on `verify`, `review`, or `plan` fail `workflow-read-only-seat-writes`).
  An empty `"strings"` or `"pieces"` list removes the repeated stage cleanly while bridging its incoming order edges to its outgoing order edges (`removeEmptyRepeats`), provided at least one executable stage remains.
- **Static expansion (`expandWorkflow` in `workflow_check.go`):** Before any run is dispatched, nested `"workflow"` stages are recursively expanded (refusing cycles in `stack` as `workflow-expansion-cycle`). Inner node IDs become `<stage>/<inner_id>` with `parent` recording the containing stage path and `groups` retaining the outer stage's interface for later proposals. Incoming edges to the `"workflow"` stage wire to inner entry nodes and matching unfed `inputs`; outgoing edges wire from `exit.outputs` ports and carry `requires_passed` barriers for every node in the inner `exit.requires_nodes`. Enclosing `write_scope` prefixes propagate inward via `inheritWorkflowScope` (refusing inner scopes that leave their container). Stages are topologically sorted (`decodeWorkflow`), refusing edge cycles (`workflow-cycle`).
- **Validation (`tillr drain workflow check` and gate):** Both the standalone CLI (`tillr drain workflow check <file-or-dir>`) and the gate check ([`tools/gate/checks/workflows.py`](../../tools/gate/checks/workflows.py)) validate every tracked file under `config/tillr/workflows/` against the active backend policy. Parameterized workflows checked standalone bind synthetic arguments (`workflowCheckArguments`), keeping `"paths"` symbolic so enclosing scope containment is verified without inventing concrete paths, and exercising every `"strings"` parameter with `most >= 2` at two values so fan-out output collisions are caught before merge. Graph size (`nodes`, `depth`, `edges`, `expansion_depth`) is printed and never refused.

### Shipped workflows and command registry

At `412259f25ae143948671832e310e7cd34ee03f5c`, six workflows and one command ship in `config/tillr/`:

| File | Stages and behavior |
| :--- | :--- |
| [`scout-then-build@v1.json`](../../config/tillr/workflows/scout-then-build@v1.json) | `scout` (`scout`, `read_only`) -> `work` (`build`, `write`). |
| [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json) | `open` (`design`, `read_only`, `may_replace: {"write": ["design-from-scouts@v1"]}`) -> `write` (`design`, `write`); default for `design` requests. |
| [`design-from-scouts@v1.json`](../../config/tillr/workflows/design-from-scouts@v1.json) | Parameterized (`prompts`: `strings`, `most: 8`); repeats `scout` (`findings: markdown`) into `join` (`design`, `write`). |
| [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json) | `plan` (`plan`, `read_only`, `may_replace: {"work": ["build-pieces@v1"]}`) -> `work` (`build`, `write`) -> `verify` (`verify`, `read_only`, `evaluator_slot`); default for `build` requests. Injects [`config/tillr/plan-guidance.md`](../../config/tillr/plan-guidance.md) into `plan`'s brief when present. |
| [`build-pieces@v1.json`](../../config/tillr/workflows/build-pieces@v1.json) | Parameterized (`pieces`: `pieces`, `most: 12`); repeats `piece` in list order. |
| [`doc-triage@v1.json`](../../config/tillr/workflows/doc-triage@v1.json) | Parameterized (`docs`: `strings`, `most: 48`); repeats `read` (`scout`, `finding: markdown`) into `report` (`design`, `write`). Filed by `tillr item doc-triage`. |
| [`commands.json`](../../config/tillr/commands.json) | Registers `"pack-sync": {"argv": ["just", "pack-sync"], "flags": {}}`. |

---

## Bounded graph proposals and the planner quick way

### Proposals and graph revisions

A stage that declares `may_replace: {"<target>": ["<workflow-ref>", ...]}` and at most one output of type `"graph.proposal"` (`workflow-proposal`) may ask the drain to replace a downstream stage subtree at settlement (`graph_proposal.go`). Omitting the proposal file is valid and leaves the current graph unchanged.

When the proposal file is present under `<run-dir>/artifacts/<name>`, `settleProposal` validates it inside the same SQLite write transaction that marks the proposing node `'passed'`:

1. **Ownership and JSON shape:** The proposing run must still own the item's pending workflow (`proposal-owner`), and the file must parse as a single JSON object `{target, workflow, args}` with no duplicate keys or unknown fields (`proposal-json`).
2. **Fog-of-war target rules:** `target` must exist and lie strictly downstream of the proposing node (`proposalLater`; `proposal-target-order`), and neither `target` nor any `<target>/...` descendant may have a row in `node_runs` for the current attempt (`proposal-target-started`). No node can replace itself or an already-started node.
3. **Allowlist, expansion, and fence:** `workflow` must appear in `stage.MayReplace[target]` (`proposal-workflow-not-allowed`), be tracked in Git (`proposal-workflow-untracked`), expand cleanly with `args` under the active policy (`proposal-argument-bounds`, `proposal-seat-unmapped`, `proposal-read-only-seat-writes`, `proposal-workflow-invalid`), and keep every writing stage inside the item's fence (`proposal-scope-outside-fence`).
4. **Boundary splice (`replaceProposalGraph`):** The replacement workflow must accept every boundary input of `target` (`proposal-input-promise`), export every boundary output via `exit.outputs` (`proposal-output-promise`), introduce no second `evaluator_slot` (`proposal-exit`), and keep the spliced graph cycle-free (`proposal-cycle`).

Every proposal — accepted or refused — inserts one immutable row into `graph_revisions` (keyed by `proposing_run UNIQUE` and guarded by `BEFORE UPDATE` and `BEFORE DELETE` abort triggers). An **accepted** proposal records `rule = ""`, increments `Workflow.Revision`, updates `Sources` digests, appends a `drain.graph-changed` event with the updated `pendingWork`, and suffixes subsequent node run IDs in that attempt with `-g<revision>-<sha256(stage_id)[:8]>` (`workflowNodeRunID`). A **refused** proposal records the refusal `rule` and `detail` with `before_graph == after_graph`, emits no `drain.graph-changed` event, does not fail the proposing run, and leaves the unreplaced target stage intact to execute next.

### The planner quick way

A read-only `"agent"` stage on the `"plan"` seat (`planAllowsQuickWay` in `workflow.go`, such as `plan` in `planned-build@v1`) may take a one-run shortcut when a request is small and self-contained:

- The planner writes a regular file named `do-it-now` in `<run-dir>/artifacts/` (`plan-quick-invalid` if non-regular), makes and commits the change inside the item's fence on the lane worktree, writes no `graph.proposal` (`plan-quick-conflict` if both are present), and ends with `RESULT: done`.
- `settleNode` switches `effectiveAccess` to `"write"` for that run's fence check, sets `node_runs.quick_way = 1` and `outcome = 'passed'`, marks all unstarted stages in the attempt `'skipped'`, appends `drain.plan-quick`, and sends the committed candidate directly to gate and landing without running downstream `work` or `verify` stages.

---

## Execution, fences, artifacts, and parallelism

### Lane lifecycle, write scopes, and chained commits

An item claims one lane and worktree (or fork checkout `.forks/lanes/<lane>/<fork>`) at node `0` and retains that lane across node transitions (`advanceNode` in `workflow.go`). Base node run IDs take the form `run-<item>-a<attempt>-n<stage_index>` (1-based; attempt ordinals count item attempts rather than node runs).

- **Write scopes (`write_scope`):** A writing stage sets `write_scope` to `"inherit"` (also the default when omitted) or a nonempty list of clean, single-line, repository-relative path prefixes (`workflow-write-scope`, `workflow-write-scope-empty`). Setting `write_scope` on a `"read_only"` stage is refused (`workflow-write-scope-read-only`). Both the item's `allowed_paths` and any explicit `write_scope` receive pack-mirror and doc-companion expansion (`fencePaths`) and fork-lane path remapping (`scopeForLane`). Before node `0` starts, `checkScopes` verifies every writing node lies inside the expanded item fence, holding violations as `node-scope-outside-fence`.
- **Serial writers and per-node fence checks:** Writing nodes always claim the lane alone. When a node finishes and `advanceNode` schedules a successor, it records the lane's current `HEAD` in `Decision.NodeBaseSHA` without resetting the worktree, and stages the successor's own resolved scope to `<lane>/fence.txt`. At each writing node's exit, `fenceViolationAt` checks `nodeBase..head` (plus any uncommitted paths on a readable answer) against `<lane>/fence.txt`, holding escapes as `boundary-scope-escape` (`outcome = 'refused'`).
- **Read-only and command worktree guards:** A `"read_only"` node that advances `HEAD` past `nodeBase` or leaves uncommitted files, or a `"command"` node that exits leaving uncommitted files, is marked `'refused'` and holds the item as `boundary-scope-escape`.
- **Command exit codes:** Command nodes run via `tillr run exec` with backend `"command"`, consuming no model tokens and bypassing backend quota windows. Exit `0` passes; exit `1` fails and follows any ready `when_outcome: "fail"` edge (updating `node_runs.outcome` to `'handled-fail'`), or holds the item as `node-failed: <node>` when no failure edge applies; any other exit code, launch failure, or `timeout_ms` wall expiry marks `'fault'` and holds as `command-fault`.
- **Single gate and squashed commit:** Passing individual node scopes does not waive the final item-level check over `itemBase..head`. At graph exit, one gate run and one `tillr-land pass --message-from <WorkerHeadSHA>` call squash every writer and command commit in `itemBase..head` into a single trunk commit whose message comes from `WorkerHeadSHA` (`Trunk.Squash` in `tools/tillr-land/land/trunk.go`, avoiding post-run housekeeping commits).
- **Mid-workflow invalidation (`workflow-invalidated`):** Editing a workflow file, item block, or fence never invalidates an in-flight attempt (`plan.go`). Only if an item leaves its ready state, gains an unmet `blocked_by` edge, opens an owner question, or owes an owner-only criterion between nodes does the planner settle completed work and release the lane with `workflow-invalidated`.

### Parallel read-only nodes

When the next ready node is `"read_only"` and not `evaluator_slot`, `claimParallel` (`parallel.go`) claims up to `max_parallel` (default `4`, max `8`) ready `"read_only"` stages in one store transaction:

- All claimed readers share the item's single lane worktree under an indefinite group lease (`leased_until = NULL`) tracked in `read_only_runs(run_id, group_id, item_id, lane_id, settled)` (`tools/tillr-run/run/schema.go`). Each reader stages its own `brief.md`, `fence.txt`, `session-id`, and durable artifacts under `<lane-dir>/nodes/<run-id>/` (`run.NodeDir`) so sibling sessions never clobber each other.
- The loop waits for every reader in the batch to reach a terminal record before checking the shared worktree. If `HEAD` moved or the worktree is dirty, every member is marked `'refused'` and the item holds as `boundary-scope-escape` naming all batch members.
- A failing or blocked reader does not cancel running siblings: finished siblings keep their `node_runs` outcomes, `node_artifacts`, and recorded spend.
- Resuming one held reader with `tillr-drain resume --run <id>` switches its working directory to `NodeDir` and re-runs only that reader while keeping completed siblings. During a `tillr` binary roll, `claimBuildReaders` (`build_readers.go`) mints `-restart` replacements only for unfinished readers whose recorded `tillr` build hash differs from the new loop.

### Producer-bound artifacts

Stages pass structured files across edges without committing intermediate scratch to Git (`node_outputs.go`):

- **Allowed types:** Both `inputs` and `outputs` map port names (`[A-Za-z0-9_.-]+`, excluding `.` and `..`) to one of four closed types: `"markdown"`, `"json"`, `"patch"`, or `"graph.proposal"` (`outputTypeValid`).
- **Producer check (`checkNodeOutputs`):** On a passing exit, every declared output (except an omitted optional `"graph.proposal"`) must exist as a regular, non-symlink file at `.tillr/loop/<run-id>/artifacts/<name>`. Files of type `"json"` are token-walked by `uniqueJSON` to reject trailing tokens and duplicate object keys at every nesting depth. Missing or invalid files mark the node `'failed'` and emit `RefusalNodeOutput` (`node-output-missing`). Accepted artifacts are written to `node_artifacts` with their SHA-256 hex digest in the same transaction that marks the `node_runs` row `'passed'`.
- **Consumer wiring (`validateOutputWiring`, `nodeInputPaths`):** Every consumer `inputs` port must be fed by an edge naming both `from_output` and `to_input` with identical types. Two distinct stages may never feed the same consumer input (`workflow-input`). The one fan-in exception is a `repeat` stage: all expanded copies share `copies_of` and wire into the consumer's single input in copy order, and `workflowNodeBrief` lists every accepted producer ID and path (while refusing `node-input-missing` if any copy lacks a passed artifact). A nested workflow's `exit.outputs` cannot export a multi-copy repeated stage output as a single port (`workflow-output-promise`).

### Retries, failover, and reader reuse

**Piece-fence retry (superseded).** A path missing from an old list needs neither a wider scope nor a new run. A report naming `fence` is a missing output under [the worker ending rule](../design/tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11).

- **Unanswered, repeating-call, or interrupted nodes (`workflow_failover.go`):** An agent node whose harness faults, is interrupted by a signal, loops on a repeating tool call (`end_cause: repeating-call`), or exits without a readable `RESULT:` footer (`done`, `failed`, `blocked`, parsed past Markdown emphasis) preserves uncommitted edits at `refs/tillr/preserved/<run-id>`, resets `HEAD` to `nodeStartSHA` so partial commits from the aborted run do not leak forward, and dispatches a replacement run (`-r1`, `-r2`, or `-restart`) on the same node (`retryWorkflowNode`, `restartInterruptedNode`, `restartRepeatingNode`). If backend failover exhausts or an exit leaves an unreadable answer with nowhere to fail over, the item holds (`node-answer-unread: <node>` or `repeating-call`).
- **Returned candidate refusals (`worker_session.go`, `plan.go`):** When a build candidate is refused at the fence, gate, or landing and its producing writer left a resumable `WorkerSession` (`workerSessionReason == "resumed-worker"`), the next attempt keeps that session's `Workflow` (including any accepted proposals) and resumes directly at `s.NodeIndex` (`retainedGraph = true`).
- **Reader reuse across operator retries (`retainedReaders`, `node_reused`):** When `tillr drain retry` starts a new attempt on an item whose previous attempt ran **no** writing node, every passed `"read_only"` prefix node is recorded in `node_reused(item_id, attempt, node_id, source_attempt)`. Outcome queries (`effectiveNodeRuns`), artifact lookups (`nodeInputPaths`), and evaluator metadata (`nodeSourceAttempt`) resolve through `source_attempt` so passed scout or triage readers neither re-run nor re-spend; if a writing node ran in the failed attempt, nothing is reused.

---

## Graph exit and evaluator routing

### Exit requirements and no-commit completion

`WorkflowExit` (`workflow_exit.go`) controls when a graph is complete and what Git state it must leave:

- **`requires_nodes`:** Nonempty list of stage IDs whose `node_runs` outcome must be `'passed'` (`workflowDone`). Omitting `exit` requires every stage in the graph to pass. At load time, `validateWorkflowExit` rejects any required node whose only incoming paths require another required node to fail (`workflow-exit`). When all required nodes pass, unrun optional branches are recorded in `node_runs` as `'skipped'`; if the graph has no ready successor left and any required node has not passed, the terminal node is marked `'failed'` and the item holds (`node-failed: <node>`).
- **`requires_candidate`:** Defaults to `"commit_required"`, and also accepts `"commit_optional"` or `"no_commit"`:
  - `"commit_required"`: `head == base` enters build-commit continuation (if eligible) or refuses as `candidate-no-op`; `head != base` proceeds to gate and landing.
  - `"commit_optional"`: `head != base` proceeds to gate and landing; `head == base` settles without landing via `settleGraphExit`.
  - `"no_commit"`: refuses as `boundary-scope-escape` ("workflow exit forbids a commit") if `head != base`.
- **No-commit settlement (`completeGraphWithoutCommit` in `workflow_exit_record.go`):** When `head == base` under `"no_commit"` or `"commit_optional"`, `finishWorkflow` first verifies `git status --porcelain --untracked-files=all` is empty (refusing dirty trees as `boundary-scope-escape`), then atomically writes and renames `<lane>/durable/<run-id>/graph-exit.json` (`reason: "no commit by design"`, `landed: false`, binding `workflow_sha256`, `base_sha`, and every node's `run_id` and `outcome`), records mechanical `done` verdicts (`actor: "drain/graph-exit"`) for `verification-only` criteria, appends `drain.graph-completed`, and moves the item to `landed` without invoking `tillr-gate` or `tillr-land`.

### Workflow evaluator

Setting `exit.evaluator_slot` routes a workflow's review node through the drain's single judge pipeline (`workflow_evaluator.go`, `judge.go`):

- **Static invariants (`validateEvaluator`):** The named stage must exist, have `access: "read_only"` (`workflow-evaluator-access`), appear in `requires_nodes` (`workflow-evaluator-required`), and stand topologically after every `"write"` stage (`workflow-evaluator-order`). At most one `evaluator_slot` may exist across composed or proposed workflows, and the store definition must complete on `"landing"` (`workflow-evaluator-requires-landing` in `plan.go`).
- **Rulebook and execution:** The evaluator stage declares a nonempty `judge_classes` list drawn from `verification-only`, `red-gate-triage`, and `direction`. `owner-only` is never eligible. The shipped `verify` and compatibility `review` steps list only `verification-only`. Intake checks each criterion's stored class against the evaluator in the selected expanded graph and binds the verdict to that graph, node, and exact candidate head and tree. No clock or separate approval changes the list. The evaluator never joins a parallel reader group; it runs alone via `judgeCandidate`, receiving `candidate.diff` (`itemBase..head`), its stage `prompt`, accepted `EvaluatorInputs` (fan-in paths joined by `"\n"`), and each preceding passed or `handled-fail` producer's `node_id`, `seat`, `access`, `prompt`, and `artifacts` directory. It writes `<durable>/eval.output` checked by `item.RecordJudge`.
- **Multi-writer independence:** `JudgeReceipt.Options.JudgedActor` lists all writing runs and sessions (`run=<r1,r2>;session=<s1,s2>`, falling back to read-only producers when no writer ran), so `checkIndependence` refuses any shared run ID or session handle between the evaluator and any producer.
- **Dispositions:**
  - All `done`: marks the evaluator `'passed'` and attaches the checked `JudgeReceipt` (`attachWorkflowJudge`) to the final candidate so landing records the agent verdicts in one transaction without launching a second review run (even if the item also carries `judge="agent"`).
  - Unreadable file or dirty evaluator worktree: saves `worktree-change.patch` if dirty, restores the committed candidate, marks `'evaluated-fail'`, and holds as `judge-unreadable`.
  - `defective` or `blocked` (`defective` > `blocked` > `not-done`): records verdicts and closes the item (`judge-defective`) or moves the item to `blocked` with `waiting_on` (`judge-blocked`).
  - `not-done`: marks `'evaluated-fail'` and emits `drain.refusal` at step `verify-not-done` (`RefusalVerifyNotDone`, keyed by criterion IDs and candidate+reason digests in `verifyNotDoneEvidence`), returning the uncovered criteria to the workflow's first node on the next attempt unless an identical repeat escalates it.

---

## Store tables and observatory surfaces

### SQLite tables

| Table | Primary key and notable columns | Writer and purpose |
| :--- | :--- | :--- |
| `items` | `item_id` (`workflow TEXT NOT NULL DEFAULT ''`, `workflow_args TEXT NOT NULL DEFAULT ''`) | `tillr-item` (`schema.go`): authored workflow ref and JSON argument map for store-declared items. |
| `workflow_selections` | `item_id` (`workflow`, `source`, `graph`) | `tillr-drain` (`dispatches.go`, `workflow_defaults.go`): immutable initial selection and flattened graph JSON per item. |
| `node_runs` | `(item_id, attempt, node_id)` (`run_id UNIQUE`, `outcome`, `seat`, `model`, `quick_way DEFAULT 0`) | `tillr-drain` (`dispatches.go`, `workflow.go`): latest run and outcome (`running`, `passed`, `failed`, `handled-fail`, `evaluated-fail`, `blocked`, `refused`, `fault`, `skipped`, `repeating-call`) per node per attempt. |
| `node_artifacts` | `(item_id, attempt, node_id, name)` (`run_id`, `type`, `sha256`, `location`) | `tillr-drain` (`dispatches.go`, `node_outputs.go`): accepted producer outputs bound to run ID and SHA-256 digest. |
| `graph_revisions` | `revision_id AUTOINCREMENT` (`item_id`, `attempt`, `proposing_run UNIQUE`, `proposing_node`, `target`, `workflow`, `rule`, `detail`, `before_graph`, `after_graph`, `before_nodes`, `after_nodes`, `recorded_at`) | `tillr-drain` (`graph_proposal.go`): append-only record (update and delete triggers abort) of accepted (`rule = ''`) and refused mid-attempt proposals. |
| `node_reused` | `(item_id, attempt, node_id)` (`source_attempt`) | `tillr-drain` (`worker_session.go`): maps reused read-only prefix nodes on a retried attempt back to the attempt that ran them. |
| `read_only_runs` | `run_id` (`group_id`, `item_id`, `lane_id`, `settled DEFAULT 0`) | `tillr-run` and `tillr-drain` (`run/schema.go`, `parallel.go`): tracks active parallel read-only group memberships sharing one lane lease. |

### Observatory

`tillr-view` projects item graphs from read-only SQLite snapshots and artifact files without executing workflow logic of its own:

- **Item page (`item_nodes.go`, `graph_revisions.go`, `ItemGraph.tsx`, `ItemDetailPane.tsx`, `ItemRunPages.tsx`):** `/api/v1/items/<id>` exposes `selected_workflow`, `workflow_source`, `graph_revisions` (merged into `ItemTimeline` too), and `graph_attempts` (using each attempt's latest accepted `after_graph` from `graph_revisions`, falling back to `workflow_selections.graph`, or synthesizing a single `"worker"` node when an attempt has runs without a stored graph). `ItemGraph.tsx` groups nodes by topological `level` and `parent` container, links every node's runs (including `node_reused` runs and `drain.started` fallbacks), nests observed `child_sessions` inside their parent run rather than as sibling nodes, and verifies `node_artifacts` on disk against stored SHA-256 digests before serving expandable inline previews (truncating previews above `32 KiB` and marking missing or mismatched files explicitly).
- **Lane page and waiting view (`lane_nodes.go`, `waiting.go`, `LanePage.tsx`):** `/api/v1/lanes` enriches active workflow lanes with the current attempt's node list (`enrichLaneNodes`, normalizing `'handled-fail'` to `'passed'` when a failure edge handled it) and `child_sessions`, while waiting and Home lane summaries report `"<N> nodes running"` across active `read_only_runs` groups rather than counting sibling readers as separate lanes.

---

## What a workflow node may never be

The loader (`workflow.go`, `workflow_check.go`), command registry (`command.go`), and proposal settler (`graph_proposal.go`) enforce these boundaries before any run starts:

- **No cycles:** Directed cycles in `edges`, recursive `"workflow"` file cycles, and proposal splices that introduce cycles through `requires_passed` barriers are refused (`workflow-cycle`, `workflow-expansion-cycle`, `proposal-cycle`).
- **No expression language on edges:** `when_outcome` accepts only `"pass"`, `"fail"`, or omission (`workflow-outcome`), and parameter substitution replaces only whole `${args.<name>}` values and `${item}` prompts without evaluating expressions.
- **No model-authored argv or shell commands:** Command nodes resolve only tracked `config/tillr/commands.json` entries with typed `--flag=value` pairs; direct shell interpreters (`sh`, `bash`, `zsh`, `dash`, `fish`) and workflow-supplied `resolved_argv` arrays are refused (`command-registry`, `workflow-command`).
- **No intra-graph landing, gate, or criterion retirement:** Nodes execute inside one lane worktree; `tillr-gate`, `tillr-land`, and `item.RecordVerdict` run at most once after root graph exit (`D-IG2`, `D-IG8`, `D-IG9`).
- **No self-replacement or started-node mutation:** A proposal can target only a strictly downstream stage with zero `node_runs` rows in the current attempt (`proposal-target-order`, `proposal-target-started`).
- **No timers, undo callbacks, or in-process Go steps:** Every executable node is a finite `tillr run exec` child process; scheduling clocks and compensations stay outside the graph (`D-IG9`).
- **No fixed node-count or expansion-depth cap:** Termination follows from cycle refusal and per-argument `most` bounds; `tillr drain workflow check` reports graph size and refuses none for size (`D-IG4`).

---

## Verification anchors

| Mechanism | Regression tests |
| :--- | :--- |
| Multi-node execution, commit chaining, scopes, and single squashed landing | [`workflow_loop_test.go`](../../tools/tillr-drain/drain/workflow_loop_test.go) (`TestWorkflowRunsTwoNodesAndLandsOnce`, `TestWorkflowNextAttemptCountsItemsNotNodes`), [`workflow_test.go`](../../tools/tillr-drain/workflow/workflow_test.go), [`workflow_snapshot_test.go`](../../tools/tillr-drain/drain/workflow_snapshot_test.go) |
| Schema rules, unknown fields, symbolic path checks, and gate check | [`workflow_check_test.go`](../../tools/tillr-drain/drain/workflow_check_test.go), [`tools/gate/checks/workflows.test.py`](../../tools/gate/checks/workflows.test.py) |
| Static `"workflow"` composition, `args`, `repeat`, and uncapped depth | [`workflow_expansion_test.go`](../../tools/tillr-drain/drain/workflow_expansion_test.go) (`TestWorkflowExpansionDeepAndWide`), [`workflow_expansion_contract_test.go`](../../tools/tillr-drain/drain/workflow_expansion_contract_test.go), [`workflow_expansion_loop_test.go`](../../tools/tillr-drain/drain/workflow_expansion_loop_test.go) |
| Seat workflow defaults and startup diagnostics | [`workflow_defaults_test.go`](../../tools/tillr-drain/drain/workflow_defaults_test.go), [`tools/tillr/settings/workflows_test.go`](../../tools/tillr/settings/workflows_test.go) |
| `may_replace` proposals, `graph_revisions` triggers, `planned-build@v1`, and `scouted-design@v1` | [`graph_proposal_test.go`](../../tools/tillr-drain/drain/graph_proposal_test.go), [`planned_build_test.go`](../../tools/tillr-drain/drain/planned_build_test.go), [`scouted_design_test.go`](../../tools/tillr-drain/drain/scouted_design_test.go) |
| Worker session retention and reader reuse across workflow retries | [`worker_session_workflow_test.go`](../../tools/tillr-drain/drain/worker_session_workflow_test.go), [`workflow_restart_test.go`](../../tools/tillr-drain/drain/workflow_restart_test.go) |
| Registered `command` nodes, flag validation, and `when_outcome: "fail"` | [`command_test.go`](../../tools/tillr-drain/drain/command_test.go), [`command_loop_test.go`](../../tools/tillr-drain/drain/command_loop_test.go) |
| Parallel read-only groups, sibling isolation, resume, and build-roll restarts | [`parallel_test.go`](../../tools/tillr-drain/drain/parallel_test.go), [`parallel_continuation_test.go`](../../tools/tillr-drain/drain/parallel_continuation_test.go), [`workflow_build_recovery_test.go`](../../tools/tillr-drain/drain/workflow_build_recovery_test.go) |
| `WorkflowExit`, `no_commit` `graph-exit.json`, and `evaluator_slot` / `verify-not-done` | [`workflow_exit_test.go`](../../tools/tillr-drain/drain/workflow_exit_test.go), [`workflow_exit_record_test.go`](../../tools/tillr-drain/drain/workflow_exit_record_test.go), [`workflow_evaluator_test.go`](../../tools/tillr-drain/drain/workflow_evaluator_test.go), [`planned_verify_route_test.go`](../../tools/tillr-drain/drain/planned_verify_route_test.go) |
| Observatory `graph_attempts`, SHA-256 artifact checks, and lane node strips | [`tools/tillr-view/item_nodes_test.go`](../../tools/tillr-view/item_nodes_test.go), [`item_nodes_history_test.go`](../../tools/tillr-view/item_nodes_history_test.go), [`parallel_test.go`](../../tools/tillr-view/parallel_test.go) |

---

## Current values

Verified at `412259f25ae143948671832e310e7cd34ee03f5c`. The prose above explains how these identifiers and bounds behave; this table is the single place their literal values are stated.

| Value | Setting / literal | Defined in |
| :--- | :--- | :--- |
| Workflow reference regex and built-in opt-out | `^[a-z][a-z0-9-]*@v[1-9][0-9]*$`; `"single-worker@v1"` | `workflowRefPattern`, `SingleWorkerWorkflow` in [`tools/tillr-item/item/workflow.go`](../../tools/tillr-item/item/workflow.go), [`request.go`](../../tools/tillr-item/item/request.go) |
| Tracked workflow and command paths | `config/tillr/workflows/<ref>.json`, `config/tillr/commands.json`, `config/tillr/plan-guidance.md` | `loadWorkflowWith` (`workflow.go`), `loadCommandRegistry` (`command.go`), `workflow_brief.go` |
| Filed-request default workflows | `build = "planned-build@v1"`, `design = "scouted-design@v1"` | `RequestDefaultWorkflow` in [`tools/tillr-item/item/request.go`](../../tools/tillr-item/item/request.go) |
| Shipped `most` argument bounds | `design-from-scouts@v1`: `8`; `build-pieces@v1`: `12`; `doc-triage@v1`: `48` | [`config/tillr/workflows/`](../../config/tillr/workflows/) |
| Parallel reader concurrency (`max_parallel`) | Default `4` when omitted, valid range `1`–`8` | `decodeWorkflow` (`workflow.go`), `workflowFields` (`workflow_check.go`) |
| Allowed producer and consumer artifact types | `"markdown"`, `"json"`, `"patch"`, `"graph.proposal"` | `outputTypeValid` in [`node_outputs.go`](../../tools/tillr-drain/drain/node_outputs.go) |
| Node run ID and revised-graph suffix | `run-<item>-a<attempt>-n<index>` (`+ "-g<rev>-<sha256[:8]>"` when `Revision > 0`) | `workflowNodeRunID` in [`graph_proposal.go`](../../tools/tillr-drain/drain/graph_proposal.go) |
| Artifact, quick-way, exit, and parallel-reader paths | `.tillr/loop/<run>/artifacts/<name>`, `do-it-now`, `<lane>/durable/<run>/graph-exit.json`, `<lane>/nodes/<run>/` | `nodeOutputsDir` (`node_outputs.go`), `quickWayFile` (`workflow.go`), `graphExitFile` (`workflow_exit_record.go`), `NodeDir` (`tools/tillr-run/run/exec.go`) |
| Workflow event kinds | `drain.node-advanced`, `drain.graph-changed`, `drain.plan-quick`, `drain.graph-completed` | `workflow.go`, `graph_proposal.go`, `workflow_exit_record.go` |
| Observatory inline artifact preview cap | `32768` bytes (`32 KiB`) | `maxArtifactPreviewBytes` in [`tools/tillr-view/item_nodes.go`](../../tools/tillr-view/item_nodes.go) |

---

<a id="decision-ledger"></a>

## Why it's this way

Each row preserves an owner ruling ([`OQ-IG1`](#why-its-this-way)–[`OQ-IG3`](#why-its-this-way), `D-IG10`, `D-IG11`, and the 2026-11-20 confirmation of `D-IG1`, `D-IG5`, `D-IG9`, `D-IG13` in [`OQ-PR3`](../tillr/decision-provenance-review.md#OQ-PR3)) or design decision (`D-IG2`–`D-IG4`, `D-IG6`–`D-IG8`, `D-IG12`) that shaped the built workflow machinery, together with any unbuilt amendments proposed in [`tillr-graph-is-the-path.md`](../design/tillr-graph-is-the-path.md#121-the-item-graph-ledger).

| ID | Decision | Why it stays |
| :--- | :--- | :--- |
| D-IG1 | Adopt finite, cycle-free node graphs expanded into ordinary `tillr run exec` nodes, rather than importing Guild's runtime, CUE evaluator, or a general workflow engine *(confirmed by owner 2026-11-20 in [`OQ-PR3`](../tillr/decision-provenance-review.md#OQ-PR3))* | Keeps `tillr-run` as the sole subprocess face and SQLite as the sole store without introducing a second execution runtime or expression language. |
| D-IG2 | Keep the execution graph internal to an item attempt while the queue, gate, and landing see one item and one deliverable *(designer's decision, 2026-11-14; proposed whole-life extension in [`tillr-graph-is-the-path.md`](../design/tillr-graph-is-the-path.md))* | Prevents sub-steps of one deliverable from polluting queue ordering or landing half-built intermediate commits on `main`. |
| D-IG3 | Fix a node's definition once its run starts and restrict runtime graph changes to `may_replace` proposals over strictly downstream, unstarted stages settled in one transaction (`graph_revisions`) *(designer's decision, 2026-11-15)*. **Designer’s amendment from the operator request, 2026-11-20; the owner may overturn it:** record a wider effective scope only on a new run of the same piece for missing item-allowed paths, not on the graph or the reporting run. | Preserves causal replay: nothing running or finished is rewritten underneath an active worker, and every mid-attempt refinement has an immutable before and after audit row. |
| D-IG4 | Flatten `"workflow"` stages statically before dispatch using `<stage>/<inner_id>` path IDs and `parent` metadata, reporting graph size without fixed depth or node-count caps *(caps of depth 8 and 100 nodes dropped 2026-11-17 per owner direction in [`tillr-core-and-git-landing.md`](../design/tillr-core-and-git-landing.md#11-the-owners-words-as-reported-by-product-qa))* | Static flattening lets the runtime loop advance a flat stage slice without nested state machines, while cycle refusal and per-argument `most` bounds guarantee termination without arbitrary size ceilings. |
| D-IG5 | Require every executed or proposed workflow and command to come from Git-tracked files (`config/tillr/workflows/`, `config/tillr/commands.json`) with typed, shell-free arguments *(confirmed by owner 2026-11-20 in [`OQ-PR3`](../tillr/decision-provenance-review.md#OQ-PR3))* | Prevents prompt-injected or model-invented graph wiring and shell command lines while letting planners pass bounded task lists and scopes as data. |
| D-IG6 | Bind every stage input to its named producer's recorded `node_artifacts` row (allowing multi-file fan-in only across copies of one `repeat` stage) and settle `no_commit` graphs through `graph-exit.json` *(designer's decision, 2026-11-15)* | Eliminates ambient scratch-file guessing across retries or restarts and lets read-only verification workflows complete cleanly without dummy Git commits. |
| D-IG7 | **Superseded by operator request (2026-11-19; the owner may overturn it).** Writing nodes need no predicted path list; do not validate their scope against the item's `allowed_paths`. | The common protections apply before the gate. The judge decides whether the diff and changed paths follow from the request. |
| D-IG8 | Run all nodes of an attempt on the item's single claimed lane and worktree without resetting between nodes, gating and landing once at graph exit *(designer's decision, 2026-11-15)* | Avoids multi-lane deadlocks when capacity is `1` and preserves chained commits across sequential writers in one worktree. |
| D-IG9 | Refuse cycles, edge expression languages, timers, undo hooks, in-process nodes, intra-graph landings, and self-rewriting stages *(confirmed by owner 2026-11-20 in [`OQ-PR3`](../tillr/decision-provenance-review.md#OQ-PR3); unbuilt amendments in [`tillr-graph-is-the-path.md`](../design/tillr-graph-is-the-path.md#45-relation-to-the-item-graph-rulings))* | Bounds workflow expressiveness to finite dependency graphs over subprocess runs so workflow files cannot grow into an untestable programming language. |
| OQ-IG1 | Land one squashed commit per item via `Trunk.Squash` (`--message-from <WorkerHeadSHA>`), even when multiple writing or command nodes committed along the lane *(owner ruling, 2026-11-15)* | Keeps `main` bisect-clean with one atomic commit per item and takes the commit message from the worker's tip rather than any post-run housekeeping commit. |
| OQ-IG2 | Ship both static `"workflow"` composition and bounded runtime `may_replace` refinement together rather than deferring mid-attempt planning *(owner ruling, 2026-11-15)* | Both `scouted-design@v1` (conditional scout fan-out) and `planned-build@v1` (conditional piece decomposition) depend on a read-only opening stage deciding whether fan-out is warranted after reading the repository. |
| OQ-IG3 | Run ready `"read_only"` nodes in parallel up to `max_parallel` on the item's single worktree while keeping `"write"` nodes strictly serial *(owner ruling, 2026-11-15)* | Captures parallel wall-clock speedup on read-only scouting and document triage without creating multiple worktrees per item or concurrent Git index races. |
| D-IG10 | Run owner-facing express work on reserved lanes outside normal build concurrency without preempting in-flight build runs *(owner ruling, 2026-11-15; built via `[lanes] express` in `.tillr.toml`)* | Prevents long build queues from starving interactive owner requests and ideas while avoiding wasted spend from killing active runs. |
| D-IG11 | Separate attempt-scoped workflow nodes (`node_runs`) from harness-internal child sessions (`child_sessions`), recording child sessions under their parent run rather than promoting them to graph nodes *(owner ruling, 2026-11-15; partially overturned for future hoisting in [`OQ-GP5`](../design/tillr-graph-is-the-path.md#OQ-GP5), unbuilt)* | Keeps item attempt counts, fences, and producer-bound contracts tied to drain-scheduled runs while making sub-agent transcripts and token spend visible in the observatory. |
| D-IG12 | Prefer a single node (using harness child sessions as needed) unless steps require distinct seats, narrowed write scopes, typed file handoffs, registered commands, or independent evaluation *(owner ruling, 2026-11-15; superseded in the unbuilt [`tillr-graph-is-the-path.md`](../design/tillr-graph-is-the-path.md#9-subagent-hoisting) design)* | Avoids cold-context startup overhead and template proliferation when one worker can complete the work on one seat and fence. |
| D-IG13 | Store workflows in `config/tillr/workflows/<name>@v<N>.json` and bind `"agent"` stages to policy-mapped `seat` names rather than model IDs *(confirmed by owner 2026-11-20 in [`OQ-PR3`](../tillr/decision-provenance-review.md#OQ-PR3); Markdown attribute half governed by [`OQ-QM1`](../design/tillr-queue-mirrors-designs.md#OQ-QM1))* | Decouples workflow topology from model vendor churn so changing a seat's backend or model in `backend-policy.json` updates every workflow at once. |
