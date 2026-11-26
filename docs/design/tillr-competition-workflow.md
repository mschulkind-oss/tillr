---
title: "Multi-agent competition workflows: parallel entries, independent 1–5 star judging, and round-by-round variation"
status: in-review
stage: DESIGN
updated: 2026-11-26
tags: [tillr, workflow, item-graphs, judge, observatory, design]
summary: "Formalizes multi-agent competitions as a first-class tillr workflow: parallel entrant nodes across one or more models, a pre-read rubric, a comparative judge that produced none of the entries and records 1–5 star ratings in SQLite, deterministic top-K selection with may_replace variation fan-out, pre-flight run-count visibility, Observatory live bundle previews, and five smallest-first build slices. Nothing here is built. Six questions await the owner (OQ-CW1 to OQ-CW6), and D-CW1 to D-CW10 are the designer's decisions."
next: "Rule OQ-CW1 to OQ-CW6; once ruled under OQ-PR21, file and build slices 1 to 5 of Section 8."
depends-on:
  - ../reference/tillr-item-graphs.md
  - ../reference/observatory-redesign.md
  - ../reference/observatory-actions.md
  - tillr-graph-is-the-path.md
  - tillr-judge-and-lifecycle.md
  - model-benchmarks-from-drain-records.md
  - tillr-ended-item-saves.md
vantage:
  status-chip: true
---

# Multi-agent competition workflows: parallel entries, independent 1–5 star judging, and round-by-round variation

**Status:** Not built. Running `rg -n 'compete|competition_entries|competition_ratings' tools/ config/` matches no competition workflow, command, or store table at `2527c9ca9a42`. Only the dated owner statements quoted in [§1.1](#11-verbatim-owner-request-and-settled-rulings) are owner rulings; the process summary and design points come from the 2026-11-26 command-line request, and `D-CW1` to `D-CW10` in the [Decision Ledger](#decision-ledger) are the designer's decisions, which the owner may overturn.

> **In short.** A competition runs as an acyclic item graph in which a pre-read `rubric` stage fixes evaluation criteria before any entry exists, `E` parallel entrant nodes each produce an isolated bundle (or per-node worktree commit), an independent judge that made none of the entries rates them `1` to `5` stars into SQLite, and a deterministic `compete-select` command picks the top `K` and expands Round `r+1`'s `K × V` variation nodes via `may_replace`. Only the final index or chosen outcome lands on the trunk; losing entries stay off `main`, and the Observatory renders structured ratings and sandboxed live previews directly from `.tillr/`.

**Why it matters.** Running a tournament inside one worker with harness subagents hides rounds and ratings from `tillr`, prevents rerunning one failed entrant, couples entrants to a single harness, leaves multi-file mockups with nowhere clean to live, and gives the filer no pre-start run count.

**The shape.** Map rounds onto finite unrolled stage groups and `may_replace` selection fan-out ([§2](#2-mapping-a-competition-onto-item-graphs-d-cw1)), store competing artifacts in bounded `"bundle"` directories or isolated entrant worktrees ([§3](#3-where-entries-live-while-competing-what-stays-for-losers-and-what-the-owner-opens-d-cw2-d-cw3)), separate the comparative `1–5 ★` judge from the terminal verdict judge ([§4](#4-the-competition-judge-store-ratings-and-the-verdict-judge-d-cw4-d-cw5-d-cw6)), define filer parameters, owner checkpoints, and pre-start run-count math ([§5](#5-filer-parameters-owner-involvement-and-presentation-d-cw7-d-cw8), [§6](#6-cost-visibility-before-starting-d-cw9)), and walk the Observatory redesign competition into five smallest-first build slices ([§7](#7-worked-example-the-observatory-redesign-competition), [§8](#8-build-slices-smallest-first-d-cw10)).

**Cost.** Expected agent runs equal `J_rubric + (E + 1) + (R - 1)(K × V + 1) + W_present + J_verdict`, printed by seat and resolved backend before the item is queued.

**Start at:** [§2](#2-mapping-a-competition-onto-item-graphs-d-cw1) and [§4](#4-the-competition-judge-store-ratings-and-the-verdict-judge-d-cw4-d-cw5-d-cw6), then [§7](#7-worked-example-the-observatory-redesign-competition).

**Needs your ruling:** [OQ-CW1](#OQ-CW1) (expressing rounds and parallel entrant width), [OQ-CW2](#OQ-CW2) (retention of losing entry bundles and refs after completion), [OQ-CW3](#OQ-CW3) (end-only vs. between-round owner picking), [OQ-CW4](#OQ-CW4) (per-entrant seats vs. policy backend names at filing time), [OQ-CW5](#OQ-CW5) (Observatory gallery, landed index, or winner promotion), and [OQ-CW6](#OQ-CW6) (filing confirmation threshold for high run counts).

**Awaits your review:** the designer's decisions `D-CW1` to `D-CW10` in the [Decision Ledger](#decision-ledger).

**Reads with:** [the item-graphs reference](../reference/tillr-item-graphs.md) (built workflow rules, `repeat`, `may_replace`, and `evaluator_slot`), [the graph design](./tillr-graph-is-the-path.md) (fog-of-war invariant and isolated parallel writers), [the judge and lifecycle design](./tillr-judge-and-lifecycle.md) (terminal criterion verdicts), [the Observatory reference](../reference/observatory-redesign.md) and [Observatory actions](../reference/observatory-actions.md) (read projections and `--write` child commands), [model benchmarks from drain records](./model-benchmarks-from-drain-records.md) (scratch packets vs. tracked opening pages), and [saved ended-item refs](./tillr-ended-item-saves.md) (pre-hash file bounds and ref cleanup).

**Words.** Alongside [the tillr item-graphs glossary](../reference/tillr-item-graphs.md#the-words), this document uses:

- **Entrant:** One agent node run producing an independent candidate entry for a round — either from the item brief in Round 1 or as a variation of an advancing parent entry in Round `r ≥ 2`.
- **Bundle:** A bounded regular-file output directory (`.tillr/loop/<run-id>/artifacts/<port>/`) carrying a self-contained entry such as an HTML/CSS mockup or design packet, hashed as a canonical file manifest in `node_artifacts`.
- **Competition judge:** A read-only agent node (`judge-<r>`) mapped to a model that produced none of the entries, scoring each round's entries `1` to `5` stars against a locked pre-read rubric.
- **Verdict judge:** The existing terminal workflow evaluator (`exit.evaluator_slot`, checked by `CheckJudge` in [`tools/tillr-item/item/judge.go`](../../tools/tillr-item/item/judge.go)) that decides `done`, `not-done`, `blocked`, or `defective` per stored item criterion before landing.

---

## 1. Governing rulings and why single-worker subagents fall short

### 1.1 Verbatim owner request and settled rulings

On 2026-11-26, after asking for an Observatory redesign competition, the owner asked for this design:

> "this won't be the last time I run a process like this, so also work on a design for how to formalize this."

Earlier owner rulings in other ledgers constrain how a competition fits into `tillr`:

1. **Finite, cycle-free tracked workflows bound to seats (`D-IG1`, `D-IG5`, `D-IG9`, `D-IG13`, confirmed in [`OQ-PR3`](../tillr/decision-provenance-review.md#OQ-PR3); `D-IG4`, 2026-11-17).** Workflows live in Git-tracked JSON under `config/tillr/workflows/`, forbid cycles and self-rewriting stages, bind agent steps to policy-declared seats rather than raw vendor model strings, and report graph size without refusing on depth or node count ([the item-graphs ledger](../reference/tillr-item-graphs.md#why-its-this-way)). Changing any of those recorded rules requires an owner ruling ([OQ-CW1](#OQ-CW1), [OQ-CW4](#OQ-CW4)).
2. **Bounded downstream graph replacement ([`OQ-IG2`](../reference/tillr-item-graphs.md#why-its-this-way), 2026-11-15) and one squashed trunk commit ([`OQ-IG1`](../reference/tillr-item-graphs.md#why-its-this-way), 2026-11-15).** A completed node with `may_replace` may splice a tracked workflow into strictly downstream, unstarted stages in one SQLite transaction (`graph_revisions`), and a landing item produces one squashed commit on `main`.
3. **When steps become graph nodes rather than harness subagents (`D-IG11`, `D-IG12`, 2026-11-15; [`OQ-GP5`](./tillr-graph-is-the-path.md#OQ-GP5), 2026-11-23).** `D-IG12` keeps work inside one node unless steps need *"distinct seats, narrowed write scopes, typed file handoffs, registered commands, or independent evaluation."* A multi-agent competition needs all five.
4. **Parallel writers (`R-GP1`, 2026-11-23, in [the graph design §1](./tillr-graph-is-the-path.md#1-the-direction-and-the-rules)).** Two writing nodes may run concurrently in isolated checkouts when their paths do not cross and join later; [§3.2](#32-isolated-per-node-worktrees-for-code-competitions-d-cw2) addresses the competition twist where rival code entrants touch the *same* paths and converge by selection rather than three-way merge.
5. **Judge authority ([`OQ-PR17`](../tillr/decision-provenance-review.md#OQ-PR17), 2026-11-20), spend visibility ([`OQ-GP3`](./tillr-graph-is-the-path.md#OQ-GP3), 2026-11-23), no dollar estimates ([`OQ-OR1`](../reference/observatory-redesign.md#why-its-this-way), 2026-11-15), and decision provenance ([`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21), [`OQ-PR22`](../tillr/decision-provenance-review.md#OQ-PR22), 2026-11-19).** Judges act as encoded steps of the flow ([provenance review](../tillr/decision-provenance-review.md#decision-ledger)); spend is recorded per node and never stops an active item ([`OQ-GP3`](./tillr-graph-is-the-path.md#OQ-GP3)); views show runs, tokens, models, backends, and latency with no dollar prices ([the Observatory ledger](../reference/observatory-redesign.md#why-its-this-way)); and choices touching repository history, models, spend, or owner-visible surfaces wait on owner rulings ([`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21)).

### 1.2 Why one worker with harness subagents is the wrong unit

Running a competition inside one `design` or `build` worker that spawns harness subagents fails on five concrete axes in the current tree:

- **One opaque run in the store:** `node_runs` records one row for the whole tournament, while subagents appear only as post-hoc `child_sessions` under that single parent ([the item-graphs reference](../reference/tillr-item-graphs.md#store-tables-and-observatory-surfaces)). Neither `tillr drain why` nor `/items/<id>` can show which round is active or how many entrants remain.
- **No single-entrant retry or reuse:** If one of five designers crashes or emits broken HTML, `tillr` cannot rerun that single entrant while reusing its four passed siblings (`node_reused` operates on `node_runs`, not `child_sessions`).
- **Single-harness lock-in:** Harness subagents inherit one adapter (`run-pi` with `PI_SUBAGENTS_TEMP_ROOT`), locking out adapters without subagent tooling (`agy`, `muse`) and preventing one competition from pitting `pi`, `claude`, `agy`, and `muse` directly against one another.
- **Unenforced rubric order and judge separation:** When one coordinating session spawns entrants and also summarizes or rates them, nothing in `tillr` guarantees that criteria were fixed before reading entries or that the rater used a disjoint model family.
- **No structured ratings in `.tillr/state.db`:** Prose star counts inside a worker report cannot be queried, sorted, or joined to runs in [`tools/tillr-view`](../../tools/tillr-view/item_nodes.go).

---

## 2. Mapping a competition onto item graphs (`D-CW1`)

### 2.1 Stage topology for `R` rounds

By the designer's decision (`D-CW1`, which the owner may overturn), a competition maps onto built item-graph primitives (`agent`, `command`, `repeat`, `copies_of` fan-in, and `may_replace` in [`tools/tillr-drain/workflow/workflow.go`](../../tools/tillr-drain/workflow/workflow.go) and [`tools/tillr-drain/drain/graph_proposal.go`](../../tools/tillr-drain/drain/graph_proposal.go)) as a finite pipeline of `R` rounds (`1 ≤ R ≤ 4`, default `R = 2`):

```
                    ┌──► round-1-e1 (agent, seat_1) ──┐
                    ├──► round-1-e2 (agent, seat_2) ──┤
brief ──► rubric ───┤              ...                ├──► judge-1 (agent, judge_seat)
         (read_only,└──► round-1-eE (agent, seat_E) ──┘          │ ratings (json)
          no entrant                                             ▼
          edges)                                          select-1 (command: compete-select)
                                                           │ records ratings + top K
                                                           │ emits proposal (graph.proposal)
                                                           ▼ replaces placeholder `round-2`
                                              ┌───────────────────────────────────────────┐
                                              │ round-2 (expanded competition-round@v1)   │
                                              │  ├── vary-e2-v1 .. vary-e2-vV             │
                                              │  ├── vary-e4-v1 .. vary-e4-vV             │
                                              │  └── judge (agent, judge_seat) ──► ratings│
                                              └─────────────────────┬─────────────────────┘
                                                                    ▼
                                                          select-2 (command: compete-select)
                                                                    │
                                                                    ▼
                                                           present / promote
                                                                    │
                                                                    ▼
                                                        verify (exit.evaluator_slot)
```

Each step uses a distinct capability of the built graph engine:

1. **`rubric` (`kind: "agent"`, `access: "read_only"` — or `kind: "command"` when the filer passes locked criteria):** Has **zero incoming edges from any entrant**. It reads the item brief and repository at `base_sha` and writes `outputs.rubric` (`"json"`). `rubric` and the `round-1` entrants have no dependency edge between them, so `claimParallelWithWorks` in [`tools/tillr-drain/drain/parallel.go`](../../tools/tillr-drain/drain/parallel.go) may even run `rubric` concurrently with Round 1 entrants while guaranteeing `rubric` never sees an entry.
2. **`round-1` (`kind: "agent"`, `repeat: "entrants"` over `"pieces"`):** Expands via `ParseWorkflowPiece` in [`tools/tillr-drain/workflow/workflow_arguments.go`](../../tools/tillr-drain/workflow/workflow_arguments.go) into `E` sibling stages (`round-1-<id>`) sharing `copies_of: "round-1"`, each carrying its own `seat`, framing `prompt`, and `entry` output port.
3. **`judge-1` (`kind: "agent"`, `seat: <judge_seat>`, `access: "read_only"`):** Consumes `rubric` plus all `E` `entry` artifacts via `copies_of` fan-in (`D-IG6`; `ExpandWorkflowWithWatch` in [`tools/tillr-drain/workflow/workflow_check.go`](../../tools/tillr-drain/workflow/workflow_check.go) attaches `RequiresPassed` barriers for every copy so `judge-1` waits until all `E` entrants pass). Writes `outputs.ratings` (`"json"`).
4. **`select-1` (`kind: "command"`, `command_ref: "compete-select"`, `access: "read_only"`, `may_replace: {"round-2": ["competition-round@v1"]}`):** Deterministically checks `ratings` against `rubric`, journals structured store rows ([§4.2](#42-ratings-recorded-as-store-data-d-cw5)), selects the top `K'` advancing entries (`1 ≤ K' ≤ K`), and writes `outputs.proposal` (`"graph.proposal"`) naming `target: "round-2"`, `workflow: "competition-round@v1"`, and `args.pieces` containing the `K' × V` variation pieces.
5. **Why `competition-round@v1` wraps both variations and `round-2/judge`:** In [`tools/tillr-drain/workflow/workflow_check.go`](../../tools/tillr-drain/workflow/workflow_check.go), `ExpandWorkflowWithWatch` refuses to export a repeated stage's port directly on `exit.outputs` (`workflow-output-promise: exit: <name> must name one non-repeated producer`). Placing `vary` (`repeat: "pieces"`), `judge` (non-repeated, fanning in `vary`), and a single manifest output inside `competition-round@v1` satisfies `replaceProposalGraph` in [`tools/tillr-drain/drain/graph_proposal.go`](../../tools/tillr-drain/drain/graph_proposal.go) without altering boundary-port rules.
6. **`present` and `verify`:** After the final round's `select-R` settles, `present` commits the deliverable index (or promotes a selected code winner; [§5.3](#53-presentation-observatory-gallery-landed-index-or-both-d-cw7)) and `verify` (`exit.evaluator_slot`) performs the standard terminal criterion check ([§4.3](#43-relation-to-the-terminal-verdict-judge-d-cw6)).

### 2.2 What built item-graph rules allow vs. refuse today

| Competition need | Built rule today | Reference / code | Status in this design |
| :--- | :--- | :--- | :--- |
| Loop back from `select-r` to `round-1` for another round | **Refused** (`workflow-cycle`, `workflow-expansion-cycle`) | `D-IG1`, `D-IG9` in [item graphs](../reference/tillr-item-graphs.md#why-its-this-way); `DecodeWorkflow` in [`workflow.go`](../../tools/tillr-drain/workflow/workflow.go) | Kept refused unless overturned in [OQ-CW1](#OQ-CW1); rounds unroll as finite stages (`D-CW1`). |
| Stage rewrites itself or an already-started stage | **Refused** (`workflow-proposal`, `proposal-target-order`, `proposal-target-started`) | `D-IG3`, [`OQ-IG2`](../reference/tillr-item-graphs.md#why-its-this-way); `settleProposal` in [`graph_proposal.go`](../../tools/tillr-drain/drain/graph_proposal.go) | Kept refused; `select-r` only replaces unstarted downstream `round-(r+1)`. |
| Command node emits `graph.proposal` via `may_replace` | **Allowed** | `DecodeWorkflow` in [`workflow.go`](../../tools/tillr-drain/workflow/workflow.go); `settleProposal` in [`graph_proposal.go`](../../tools/tillr-drain/drain/graph_proposal.go) | Used directly by `compete-select` (`D-CW1`) so selection uses zero LLM tokens. |
| Fan-in of `N` entrant outputs into one `judge` input | **Allowed** when producers share `copies_of` from one `repeat` stage | `D-IG6`; `ValidateOutputWiring` and `ExpandWorkflowWithWatch` in [`workflow_check.go`](../../tools/tillr-drain/workflow/workflow_check.go) | Used inside each round (`round-1` → `judge-1`, `round-r/vary` → `round-r/judge`). |
| Per-entrant distinct seat and prompt in one `repeat` | **Allowed** for `args` of type `"pieces"`, **refused** for `"strings"` (`workflow-repeat`) | `BindWorkflow` and `ParseWorkflowPiece` in [`workflow_arguments.go`](../../tools/tillr-drain/workflow/workflow_arguments.go) | Entrants and variations use `"pieces"` (`D-CW1`, `D-CW8`). |
| Multi-file HTML/CSS mockup directory as a node output | **Refused** (`node-output-missing: ... not a regular file`; closed types `markdown`, `json`, `patch`, `graph.proposal`) | `D-IG6`; `checkNodeOutputs` in [`node_outputs.go`](../../tools/tillr-drain/drain/node_outputs.go) | Designer's decision `D-CW2`: add bounded regular-file `"bundle"` directory output type. |
| Parallel entrant execution > `8` at once | **Refused** (`workflow-max-parallel`: `1..8`, default `4`; ordinary items also capped by free ordinary lanes) | `DecodeWorkflow` in [`workflow.go`](../../tools/tillr-drain/workflow/workflow.go); `claimParallelWithWorks` in [`parallel.go`](../../tools/tillr-drain/drain/parallel.go) | `E` or `K × V` may exceed `8` (up to `most: 12`), running in waves of `max_parallel`; raising `max_parallel` asked in [OQ-CW1](#OQ-CW1). |
| Parallel `write` entrants editing repository files | **Serialized** onto one lane worktree today (`s.Access != "read_only"`) | [`OQ-IG3`](../reference/tillr-item-graphs.md#why-its-this-way); `claimParallelWithWorks` in [`parallel.go`](../../tools/tillr-drain/drain/parallel.go); `R-GP1` / `D-GP16` in [the graph design §3.8](./tillr-graph-is-the-path.md#38-concurrent-writers-checkouts-and-the-join) | Artifact competitions run `read_only` in parallel now (Slice 1); code competitions use isolated worktrees in Slice 5 (`D-CW2`). |
| Filing a parameterized workflow via `tillr item request` | **Refused** (`workflow-needs-args`) | [`tools/tillr-item/item/request.go`](../../tools/tillr-item/item/request.go) | Dedicated `tillr item compete` intake writes `items.workflow` and `items.workflow_args`, matching `DocTriage` in [`doc_triage.go`](../../tools/tillr-item/item/doc_triage.go). |
| Adding a 9th node kind (`rate`) to the target graph model | **Amends closed table** (`work`, `judge`, `command`, `owner question`, `plan`, `classify`, `triage`) | [The graph design §3.1](./tillr-graph-is-the-path.md#31-node-kinds) | Avoided (`D-CW4`): `judge-<r>` is an ordinary read-only `agent` (`work`) node paired with `command` node `compete-select`. |

### 2.3 Fixed stage unrolling vs. a new round rule

Expressing `R` rounds does not need cycles or self-rewriting stages when `R` has a small practical upper bound (`1 ≤ R ≤ 4`). Tracked workflow templates `competition-1r@v1` through `competition-4r@v1` under `config/tillr/workflows/` keep every executed and proposed workflow Git-tracked (`D-IG5`), acyclic (`D-IG1`), and inspectable with `tillr drain workflow check` (`WorkflowSize` in [`tools/tillr-drain/workflow/workflow_check.go`](../../tools/tillr-drain/workflow/workflow_check.go)).

If `select-r` finds `0` viable entries (or `1` entry when variation is pointless), it omits `proposal` or proposes a minimal pass-through template so downstream rounds collapse cleanly without rewriting started nodes. Whether to keep this finite `1..4` stage unrolling or amend `D-IG1` / `D-IG9` to permit self-similar tail replacement (`competition-round@v1` proposing `competition-round@v1` until a rating threshold or round cap) is put to the owner in [OQ-CW1](#OQ-CW1).

---

## 3. Where entries live while competing, what stays for losers, and what the owner opens (`D-CW2`, `D-CW3`)

### 3.1 Per-node `"bundle"` directories for artifact competitions (`D-CW2`)

Mockup, architecture, and document competitions should not touch the shared Git checkout while competing: every entrant is `access: "read_only"` on the repository and writes only inside its own run-scoped artifacts directory `.tillr/loop/<run-id>/artifacts/entry/` (`nodeOutputsDir` in [`tools/tillr-drain/drain/node_outputs.go`](../../tools/tillr-drain/drain/node_outputs.go)).

By the designer's decision (`D-CW2`, which the owner may overturn), `OutputTypeValid` and `checkNodeOutputs` gain one directory port type, `"bundle"`:

1. **Self-contained directory layout:** The producer writes `.tillr/loop/<run-id>/artifacts/<name>/` containing a required manifest file (`entry.json` with `title`, `summary`, and relative `entrypoint`, such as `index.html` or `proposal.md`) plus supporting regular files (`.html`, `.css`, `.js`, `.svg`, `.png`, `.md`, `.json`).
2. **Safety and size bounds before hashing:** Matching `D-EW4` in [saved ended-item refs §5](./tillr-ended-item-saves.md#5-bounding-untracked-files-before-git-hashes-them), `checkNodeOutputs` walks the directory with `os.Lstat`, refusing symlinks, special files, `..` segments, missing `entrypoint`, or bounds over `256` files, `2 MiB` per file, or `16 MiB` total.
3. **Canonical `sha256` in `node_artifacts`:** `node_artifacts.location` stores the bundle directory path and `node_artifacts.sha256` stores the SHA-256 over sorted `<relpath>\0<file-sha256>\n` records, preserving tamper detection in [`tools/tillr-view/item_nodes.go`](../../tools/tillr-view/item_nodes.go).
4. **Variation handoff without shared-directory clobbering:** In Round `r ≥ 2`, `compete-select` passes each variation piece its assigned parent entry's immutable `.tillr/loop/<parent-run-id>/artifacts/entry/` path as a read-only input; the variation node copies or adapts files into its *own* `.tillr/loop/<child-run-id>/artifacts/entry/`, leaving the parent bundle untouched.

### 3.2 Isolated per-node worktrees for code competitions (`D-CW2`)

When entrants compete on live repository code (`--mode worktree` — e.g., three rival implementations of a layout engine that each must compile and run targeted tests), `read_only` bundles are insufficient and `R-GP1`'s three-way `join` ([the graph design §3.8](./tillr-graph-is-the-path.md#38-concurrent-writers-checkouts-and-the-join)) does not apply because rival entrants intentionally edit the **same** files in incompatible ways.

Under `D-CW2` (Slice 5, extending `D-GP16` for competitive selection; the owner may overturn this):

- Each Round 1 entrant runs in an isolated worktree `.tillr/items/<item-id>/nodes/<node-id>/worktree` detached at `base_sha`.
- On node completion, the drain commits/pins the entrant's tree at `refs/tillr/compete/<item-id>/<entry-id>`, exports `git diff base_sha..HEAD` plus targeted check output as a `"bundle"` in `.tillr/loop/<run-id>/artifacts/entry/` for `judge-<r>`, and removes or resets the temporary worktree.
- Round `r ≥ 2` variations branch their worktree from `refs/tillr/compete/<item-id>/<parent-entry-id>` instead of `base_sha`, inheriting the winning parent's working code.
- Competing refs **never** three-way merge; only when a single winning entry is promoted at the end does `git read-tree` / cherry-pick bring that winner onto the item's lane branch for `evaluator_slot`, the gate, and `Trunk.Squash` ([`OQ-IG1`](../reference/tillr-item-graphs.md#why-its-this-way)).

### 3.3 What is kept for losing entries and what lands (`D-CW3`)

Only the competition's final deliverable (the compact Markdown index, the promoted winning code change, or nothing on `no_commit`; see [OQ-CW5](#OQ-CW5)) ever reaches `main`. Losing entries — and raw mockup bundles — never land in Git history (`D-CW3`):

- **Why losing entries stay off `main`:** Committing `10`–`20` losing mockups bloats repository history, trips `vantage-check` or item-block sync on tracked Markdown ([`OQ-MB9`](./model-benchmarks-from-drain-records.md#OQ-MB9)), and mixes rejected drafts with authoritative docs. Meanwhile, the gate's `scratch` check ([`tools/gate/checks/scratch.json`](../../tools/gate/checks/scratch.json)) forbids citing scratch paths in tracked Markdown, so any landed index states scores, rationales, and run IDs as self-contained facts without linking scratch filesystem paths.
- **What stays permanently in `.tillr/state.db`:** Every entry's metadata (`entry_id`, `round`, `parent_entry_id`, `node_id`, `run_id`, `seat`, `backend`, `model`, `artifact_sha256`, `advanced`, `rank`) and every `1–5 ★` rating and rationale stay in SQLite permanently alongside `runs` and `node_runs`.
- **Disk bundles and `refs/tillr/compete/*` while active vs. after completion:** While the competition item is active or waiting on an owner pick, **all** entries (winners and losers across every round) remain intact on disk so the owner can open a Round 1 loser and overrule the judge. When heavy bundle directories and `refs/tillr/compete/<item-id>/*` refs are swept after item completion touches irreversible deletion of unlanded work ([authority, not approval](../reference/authority-not-approval.md#what-owner-only-is-for)) and awaits the owner's ruling in [OQ-CW2](#OQ-CW2).
- **What the owner opens:** The owner opens the Observatory competition view (`/items/<item-id>` or `/compete/<item-id>`), which links each entry's `entrypoint` under a read-only route `/compete/<item-id>/entries/<entry-id>/raw/<path>` served directly from `.tillr/loop/<run-id>/artifacts/entry/` after checking `node_artifacts.sha256` ([§5.3](#53-presentation-observatory-gallery-landed-index-or-both-d-cw7)).

---

## 4. The competition judge, store ratings, and the verdict judge (`D-CW4`, `D-CW5`, `D-CW6`)

### 4.1 Criteria written before reading and blind cross-model evaluation (`D-CW4`)

To prevent post-hoc criteria drift and model-brand favoritism (`D-CW4`, which the owner may overturn):

1. **Rubric locked before reading:** `rubric` produces a strict JSON document (`criteria[]` with `id`, `title`, `description`, `weight` summing to `1.0`, and optional `must_have` checks) hashed into `node_artifacts` before `judge-1` starts. Every `judge-<r>` stage receives that exact `rubric` file; `compete-select` verifies `ratings.rubric_sha256 == rubric.sha256` and refuses `ratings` if any criterion is missing, added, or reweighted.
2. **Judge made none of the entries:** The judge stage's resolved `(adapter, model)` must be disjoint from every entrant's resolved `(adapter, model)` across all rounds (`D-CW8`). At `tillr item compete` filing time (when `--policy` is supplied) and at drain claim time, an overlap is refused (`competition-judge-overlap`) rather than letting a model grade its own output.
3. **Blind slot presentation:** When staging `judge-<r>`'s brief, `nodeInputPaths` maps entrant bundles to neutral slot labels (`slot-1` … `slot-N` in deterministic hash order of `(item_id, round, node_id)`) and omits producer `seat`, `backend`, `model`, and session metadata from the judge's prompt. Model identity is joined back by `compete-select` *after* ratings are written.

### 4.2 Ratings recorded as store data (`D-CW5`)

Lane processes never open `.tillr/state.db` ([judge and lifecycle §2.1](./tillr-judge-and-lifecycle.md#21-topology-relative-to-gate-and-land-d-jl1)). Instead, `judge-<r>` writes `outputs.ratings` (`"json"`):

```json
{
  "rubric_sha256": "<64-hex>",
  "round": 1,
  "entries": [
    {
      "slot": "slot-2",
      "overall_stars": 5,
      "rank": 1,
      "criterion_stars": { "hierarchy": 5, "density": 4, "traceability": 5 },
      "strengths": "Single-axis waterfall and clear waiting-class grouping.",
      "weaknesses": "Mobile narrow-viewport table clips long run IDs.",
      "variation_guidance": "Test a compact run-ID chip and sticky pass header."
    }
  ]
}
```

During `compete-select` / node settlement, the drain validates `ratings` (`uniqueJSON` in [`tools/tillr-drain/drain/node_outputs.go`](../../tools/tillr-drain/drain/node_outputs.go), every slot rated once, integer `overall_stars` and `criterion_stars[*]` in `1..5`, strict permutation `rank` in `1..N`, computed `weighted_score` in `1.00..5.00`) and appends `compete.rubric.recorded`, `compete.entry.recorded`, `compete.rating.recorded`, and `compete.selection.recorded` envelopes projected into three SQLite tables registered via `store.RegisterDDL` ([`tools/tillr/store/registry.go`](../../tools/tillr/store/registry.go)) (`D-CW5`):

- **`competition_rounds`** `(item_id, attempt, round, rubric_sha256, rubric_json, entrant_count, advance_limit, variations_per_winner, state, settled_at)` — primary key `(item_id, attempt, round)`.
- **`competition_entries`** `(item_id, attempt, entry_id, round, parent_entry_id, slot_label, node_id, run_id, seat, backend, model, bundle_location, bundle_sha256, entrypoint, git_ref, rank, advanced)` — primary key `(item_id, attempt, entry_id)`.
- **`competition_ratings`** `(item_id, attempt, entry_id, round, judge_node_id, judge_run_id, judge_backend, judge_model, overall_stars, weighted_score, criterion_stars_json, strengths, weaknesses, variation_guidance, recorded_at)` — primary key `(item_id, attempt, entry_id)`, with `CHECK (overall_stars BETWEEN 1 AND 5)`.

Tie-breaking at the `K`-th advancement boundary is deterministic: higher `overall_stars` (`1..5`), then higher `weighted_score` under `rubric` weights, then lower explicit `rank` (`1..N`) from `judge-<r>`.

### 4.3 Relation to the terminal verdict judge (`D-CW6`)

The comparative competition judge (`judge-<r>`) and the existing terminal verdict judge (`exit.evaluator_slot` / `CheckJudge` in [`tools/tillr-item/item/judge.go`](../../tools/tillr-item/item/judge.go)) solve orthogonal problems and never replace each other (`D-CW6`):

| Aspect | Competition judge (`judge-<r>`) | Verdict judge (`exit.evaluator_slot` / `CheckJudge`) |
| :--- | :--- | :--- |
| **Purpose** | Rates rival entries `1`–`5` stars on craft and brief fit so the best `K` advance and the owner sees ranked options. | Verifies whether the item's final candidate at `(evaluated_head, evaluated_tree)` satisfies every stored `item_criteria` row. |
| **When & how often** | Once per round (`R` times per attempt), over unlanded per-node bundles before any commit is needed on the lane branch. | At most once per attempt (`workflow-evaluator-access`), as the root graph's final leaf immediately before gate and `Trunk.Squash` (`D-JL1`). |
| **Output & store table** | `ratings` JSON (`1..5` integer `overall_stars`, criterion scores, rank, notes) → `competition_ratings` as soon as the round settles. | `eval.output` (`outcome: pass\|fail`, per-criterion `done`, `not-done`, `blocked`, `defective`) → `verdicts` *after* landing (`D-JL1`). |
| **Meaning of a low rating** | A `1 ★` or `2 ★` entry is a **passed** run that is simply not selected in `compete-select`; it triggers no retry or item hold. | `not-done` refuses landing and returns findings for repair; `blocked` holds on the named mover; `defective` closes the item. |
| **Criterion authority** | **None.** Never retires `item_criteria` or authorizes landing. | Retires `verification-only` (and authorized) `item_criteria` upon landing ([judge and lifecycle §2.2](./tillr-judge-and-lifecycle.md#22-when-generic-judging-survives--and-closing-the-mechanical-stamp-hole-d-jl2)). |
| **How both compose** | Narrows `E` entries through `R` rounds into ranked finalists. | Checks at graph exit that all `R` rounds settled, store ratings exist, and any landed index or promoted winner passes repository rules (e.g., `vantage-check` and `scratch` checks). |

---

## 5. Filer parameters, owner involvement, and presentation (`D-CW7`, `D-CW8`)

### 5.1 Parameters on `tillr item compete`

Following `DocTriage` in [`tools/tillr-item/item/doc_triage.go`](../../tools/tillr-item/item/doc_triage.go), `tillr item compete` validates every parameter before writing to `.tillr/state.db` and records the normalized structure in `items.workflow_args` (`D-CW8`):

| Flag / `workflow_args` key | Type & bounds | Default | Meaning |
| :--- | :--- | :--- | :--- |
| `--title` / `title` | Non-empty string | *(required)* | Queue title and slug source. |
| `--brief` / `brief` | Non-empty text or `@file` | *(required)* | Shared problem statement given to `rubric` and every entrant. |
| `--entrants` / `entrants` (`E`) | Integer `2..12` | `4` | Independent Round 1 entrants. |
| `--advance` / `advance` (`K`) | Integer `1..E-1` (`1..E` when `R=1`) | `2` | Top-rated entries taken forward after each non-final round (`K × V ≤ 12`). |
| `--variations` / `variations` (`V`) | Integer `1..6` (`0` when `R=1`) | `2` | Independent variations spawned per advancing entry in Rounds `2..R`. |
| `--rounds` / `rounds` (`R`) | Integer `1..4` | `2` | Total rounds (`1` = single fan-out and rate; `2` = initial entries + one variation round). |
| `--entrant-seats` / `entrant_seats` | Comma list of policy seats | `design` (`build` in `worktree` mode) | Cycled across entrants (`e_i` gets `entrant_seats[i % len]`); in Round `r ≥ 2`, variations of one parent rotate across seats so each survivor is varied by distinct models (`D-CW8`). |
| `--judge-seat` / `judge_seat` | Single policy seat | `review` | Seat for `rubric` and `judge-1..judge-R`; resolved backend/model must not overlap any entrant ([§4.1](#41-criteria-written-before-reading-and-blind-cross-model-evaluation-d-cw4)). |
| `--show-top` / `show_top` (`S`) | Integer `1..12` | `max(K, 3)` | Number of top finalists highlighted at the top of the final gallery/index (all entries remain expandable). |
| `--criteria` / `criteria` | Optional strings or `@json` | *(agent `rubric` node)* | Pre-written rubric criteria; when complete JSON is supplied, `rubric` becomes a zero-LLM `command` lock step. |
| `--directions` / `directions` | Optional list (`≤ E`) | *(none)* | Optional per-entrant creative briefs (e.g., `"dense tabular"`, `"timeline-first"`) appended to individual entrant pieces. |
| `--mode` / `mode` | `bundle` \| `worktree` | `bundle` | Read-only artifact bundle competition ([§3.1](#31-per-node-bundle-directories-for-artifact-competitions-d-cw2)) vs. isolated code worktrees ([§3.2](#32-isolated-per-node-worktrees-for-code-competitions-d-cw2)). |
| `--between-rounds` / `between_rounds` | `judge` \| `owner` | `judge` | Automatic top-`K` advancement vs. pausing for an owner pick between rounds ([§5.2](#52-where-the-owner-comes-in-end-only-default-vs-between-round-picks), [OQ-CW3](#OQ-CW3)). |
| `--deliverable` / `deliverable` | `both` \| `observatory` \| `winner` | *(per [OQ-CW5](#OQ-CW5))* | Whether completion lands a Markdown index alongside the Observatory view, stays in Observatory only (`no_commit`), or pauses to promote a winning entry. |

**Different models per entrant (`D-CW8` and [OQ-CW4](#OQ-CW4)).** Under `D-IG13` / [`OQ-PR3`](../tillr/decision-provenance-review.md#OQ-PR3), stages name policy **seats** rather than model strings. Because `Policy` in [`tools/tillr-drain/internal/policy/policy.go`](../../tools/tillr-drain/internal/policy/policy.go) accepts an open set of lowercase-hyphenated seat names (`design-claude`, `design-agy`, `design-muse`, `design-pi`) as well as multi-backend `SeatLists` ([`tools/tillr-drain/internal/policy/seat_shares.go`](../../tools/tillr-drain/internal/policy/seat_shares.go)), two mechanisms stay strictly inside `D-IG13`:

1. Passing `--entrant-seats design-pi,design-claude,design-agy,design-muse` binds each entrant `WorkflowPiece` to its own policy seat.
2. When multiple entrants in the same round share one multi-backend `SeatList` seat, `claimParallelWithWorks` draws **without replacement** across that seat's open backends before repeating any backend (`D-CW8`), rather than relying on global historical `seat_dispatches` counts that could assign the same backend four times in one round.

Whether `tillr item compete` should *also* allow `--entrant-backends` / `--judge-backend` naming backend keys from `--policy`'s `backends` map directly in `items.workflow_args` — avoiding per-model seat boilerplate in the policy file while keeping tracked workflow JSON model-free, at the cost of relaxing `D-IG13` on `WorkflowPiece` — is an owner question ([OQ-CW4](#OQ-CW4)).

### 5.2 Where the owner comes in: end-only default vs. between-round picks

Under `D-CW7` and [OQ-CW3](#OQ-CW3):

- **Default (`--between-rounds judge`, owner at the end only):** All `R` rounds run unattended. The judge rates Round 1, `compete-select` advances the top `K` into Round 2 variations, the judge rates Round 2, and the owner reviews the finished top `S` entries once. This matches [`R-OD1`](./operator-decisions-inside-tillr.md#1-the-ruling) and avoids stalling multi-round runs on routine elimination cuts.
- **Opt-in (`--between-rounds owner`, owner picks between rounds):** After `judge-<r>` (`r < R`) records its `1–5 ★` ratings, `compete-select` places the item on a derived `owner` hold (`competition-pick`), **releasing the lane immediately** ([judge and lifecycle §4.6](./tillr-judge-and-lifecycle.md#46-a-held-item-never-occupies-a-lane-d-jl13)). The owner inspects the rated Round `r` entries in the Observatory and runs `tillr item compete-pick --item <id> --entries <e_a,e_b> [--guidance "..."]` (or submits the pick form in `tillr-view serve --write`, following `placeHandler` in [`tools/tillr-view/serveplace.go`](../../tools/tillr-view/serveplace.go)), which settles `round-(r+1)`'s `graph.proposal` and returns the item to `in-queue`.

### 5.3 Presentation: Observatory gallery, landed index, or both (`D-CW7`)

Each presentation surface solves a different half of the problem (`D-CW7`, governed by [OQ-CW5](#OQ-CW5)):

1. **Observatory competition gallery (`tools/tillr-view`):**
   - Renders on `/items/<item-id>` (and dedicated full-width `/compete/<item-id>`, honoring `D-OR1` and [`OQ-OR2`](../reference/observatory-redesign.md#why-its-this-way)):
     - **Top-`S` showcase** at the top, ordered by final rank, with `1–5 ★` chips, per-criterion bars, producer seat/backend/model, token and wall-time figures (never dollar prices, per [`OQ-OR1`](../reference/observatory-redesign.md#why-its-this-way)), and a **parent-regression badge** whenever every Round `r` variation of a survivor scored lower than its Round `r-1` parent (automatically surfacing that higher-starred parent alongside the finalists).
     - **Round-by-round lineage bracket** below, expanding eliminated entries so a `2 ★` dark-horse entry remains one click away.
     - **Sandboxed live bundle route** `GET /compete/<item-id>/entries/<entry-id>/raw/<path>`: verifies the file lies inside `competition_entries.bundle_location`, checks `bundle_sha256`, sets MIME type (`text/html`, `text/css`, `image/svg+xml`, etc.), and attaches `Content-Security-Policy: sandbox allow-scripts; default-src 'self' 'unsafe-inline' data: blob:; connect-src 'none'` plus `X-Frame-Options: SAMEORIGIN` so untrusted mockup scripts can animate interactive UI prototypes while remaining unable to call `/api/*` or `--write` endpoints (`D-CW7`).
2. **Landed Markdown index (`docs/competitions/<date>-<slug>.md` or `--report <path>`):**
   - Rendered deterministically by `compete-select --render-index` (zero extra LLM run): frontmatter, the brief, the locked rubric table, expected vs. actual run counts by seat/model, per-round score tables with judge strengths/weaknesses, and summaries of the top `S` entries.
   - Contains **zero** `.tillr/loop/` or scratch paths (`scratch` gate check stays green); links permanent entity IDs and states verified facts directly.

---

## 6. Cost visibility before starting (`D-CW9`)

Entrants, survivors, variations, and rounds multiply spend quickly, while [`OQ-GP3`](./tillr-graph-is-the-path.md#OQ-GP3) forbids mid-flight spend ceilings that kill active items and [`OQ-OR1`](../reference/observatory-redesign.md#why-its-this-way) forbids fabricated dollar estimates. Cost control therefore belongs **at filing time, in exact expected run counts** (`D-CW9`, which the owner may overturn).

For parameters `(E, K, V, R)` with `J_rubric ∈ {0, 1}` (`1` when an agent writes `rubric`, `0` when `--criteria @json` locks it), `W_present = 0` (deterministic `compete-select --render-index`), and `J_verdict ∈ {0, 1}` (`1` when `exit.evaluator_slot` runs on a committed deliverable, `0` on `no_commit`):

$$\text{AgentRuns}_{\text{expected}} = J_{\text{rubric}} + (E + 1) + (R - 1)(K \cdot V + 1) + J_{\text{verdict}}$$

$$\text{CommandRuns} = R + W_{\text{cmd}}, \qquad \text{CriticalPathAgentDepth} = J_{\text{rubric}}^{*} + 2R + J_{\text{verdict}}$$

*(where $J_{\text{rubric}}^{*} = 0$ when `rubric` runs in parallel with Round 1 entrants and `1` if serialized ahead of them, and peak concurrent width is $\max(E, K \cdot V)$, executed in waves of `min(max_parallel, free_lanes)`).*

Before creating an item (`tillr item compete --dry-run`) and in the filing receipt of `tillr item compete`, the CLI expands the workflow against `--policy` and prints the exact run budget:

```text
Competition: observatory-redesign (competition-2r@v1, mode=bundle)
Shape:       E=4 entrants -> K=2 advance x V=2 variations (R=2 rounds, show_top=3)
Agent runs:  12 expected (min 9 if 1 advances; critical-path agent depth: 5; peak width: 4)
  rubric       1 run   seat=review        backend=pi (example-vendor/model-large)
  round-1      4 runs  seats=design-pi:1, design-claude:1, design-agy:1, design-muse:1
  judge-1      1 run   seat=review        backend=pi (disjoint from all entrant models)
  round-2      4 runs  seats=design-pi:1, design-claude:1, design-agy:1, design-muse:1
  judge-2      1 run   seat=review        backend=pi
  verify       1 run   seat=verify        backend=pi
Commands:    2 deterministic runs (select-1, select-2/render-index; 0 model tokens)
```

Changing `--entrants 6 --advance 3 --variations 3 --rounds 3` immediately prints `29 expected agent runs` before a single run starts. Whether `tillr item compete` should require an explicit `--confirm-runs <N>` flag above a soft run threshold (while never halting an already-queued item, preserving [`OQ-GP3`](./tillr-graph-is-the-path.md#OQ-GP3)) is asked in [OQ-CW6](#OQ-CW6).

---

## 7. Worked example: the Observatory redesign competition

### 7.1 Trunk baseline and what was awkward to do by hand

**Repository baseline at `2527c9ca9a42`:** Running `fd -i 'compet' docs` returns nothing, and `fd -i 'observatory' docs` returns only [`docs/reference/observatory-actions.md`](../reference/observatory-actions.md), [`docs/reference/observatory-design.md`](../reference/observatory-design.md), and [`docs/reference/observatory-redesign.md`](../reference/observatory-redesign.md). **UNMEASURED:** the companion *"Observatory redesign competition"* item queued on 2026-11-26 had not yet landed a report on `main` as of `2527c9ca9a42`, so wall-clock timings and empirical scores from that companion run are not cited here and should be reconciled once it lands.

From the filed request's account of how that item had to be run today (*"one item whose single worker is told to do all of it with its own subagents"*) and from the current codebase, six steps are structurally awkward by hand:

| Hand-run friction in the Observatory competition | Why it happens in the tree today | How this workflow eliminates it |
| :--- | :--- | :--- |
| **1. Invisible progress across rounds** | One worker run occupies a lane across Round 1 generation, Round 1 judging, Round 2 variations, and Round 2 judging (`1` `node_runs` row). | `12` distinct `node_runs` rows across `rubric`, `round-1-*`, `judge-1`, `select-1`, `round-2/*`, `judge-2`, and `verify`, live-updated over SSE on `/items/<id>`. |
| **2. Brittle retries and single-harness subagents** | Subagents run inside one `run-pi` session (`child_sessions`); if Designer 3 fails or times out, the coordinator babysits or restarts by hand, and non-`pi` adapters (`agy`, `muse`) cannot participate as peers. | Each designer is an independent `tillr-run` node on its own seat/adapter; a failed entrant retries or escalates alone while passed siblings stay reused (`node_reused`). |
| **3. Post-hoc criteria and non-blind judging** | The coordinator can read mockups before writing its rubric, knows which subagent model made each mockup, and may share model family with an entrant. | `rubric` has no incoming edge from entrants (`rubric_sha256` locked first), `judge-<r>` sees shuffled `slot-1..N` bundles with model names stripped, and policy check enforces model disjointness (`D-CW4`, `D-CW8`). |
| **4. Unqueryable prose star ratings** | Ratings live only in Markdown text written by the coordinator; no SQL table holds stars or ranks. | `compete-select` validates `1..5` integer stars and writes `competition_rounds`, `competition_entries`, and `competition_ratings` in `.tillr/state.db` (`D-CW5`). |
| **5. Nowhere clean to store or open multi-file HTML mockups** | Each mockup redesigns `/`, `/items`, `/items/<id>`, `/lanes/<id>`, and `/runs/<id>` ([the Observatory reference](../reference/observatory-redesign.md#entities-and-urls)) with HTML + CSS. Committing `8`+ mockups bloats `docs/`, scratch paths are forbidden in tracked Markdown by [`tools/gate/checks/scratch.json`](../../tools/gate/checks/scratch.json), and [`tools/tillr-view/item_nodes.go`](../../tools/tillr-view/item_nodes.go) embeds single regular files as `text/plain`. | Entrants write `"bundle"` directories in `.tillr/loop/<run>/artifacts/entry/`; `/compete/<item-id>/entries/<entry-id>/raw/index.html` serves live sandboxed HTML mockups directly in the browser (`D-CW2`, `D-CW7`). |
| **6. Manual parent copying and variation wiring** | Between rounds, the coordinator must parse its own judge notes, copy the top `2` mockup directories into `4` variation directories without CSS cross-talk, and stitch parent-to-child lineage tables by hand. | `compete-select` deterministically picks the top `K`, passes each parent's immutable bundle path + `variation_guidance` into `round-2` `WorkflowPiece`s via `may_replace`, and records `parent_entry_id` automatically (`D-CW1`). |

### 7.2 End-to-end trace as a first-class workflow

1. **File with pre-flight run check:** The operator (or whale) runs `tillr item compete` with `--title "Observatory redesign competition"`, `--entrants 4`, `--advance 2`, `--variations 2`, `--rounds 2`, `--show-top 3`, `--entrant-seats design-pi,design-claude,design-agy,design-muse`, `--judge-seat review`, and `--brief @observatory-brief.md` referencing the five-page data contract in [`docs/reference/observatory-redesign.md`](../reference/observatory-redesign.md). The CLI confirms `review` uses a model disjoint from all four entrant seats and prints `12 expected agent runs, 2 command runs`.
2. **Round 1 (`rubric` + `4` parallel mockups → `judge-1` → `select-1`):** `rubric` locks five weighted criteria (`waiting-class clarity`, `queue-order legibility`, `graph-and-waterfall alignment`, `token-and-provenance honesty`, `keyboard navigation`). Concurrently, `round-1-e1..e4` each write a self-contained static mockup bundle (`entry.json` with `entrypoint: "index.html"`). `judge-1` inspects `slot-1..slot-4` blind against `rubric` and rates `e2` (`5 ★`), `e4` (`4 ★`), `e1` (`3 ★`), and `e3` (`2 ★`).
3. **Deterministic selection and `may_replace` expansion:** `select-1` (`compete-select`) validates `ratings`, writes `4` rows to `competition_entries` and `competition_ratings`, marks `e2` and `e4` `advanced = 1`, and writes `proposal` replacing `round-2` with `competition-round@v1` containing `4` variation pieces: `vary-e2-v1` (assigned `design-agy`), `vary-e2-v2` (assigned `design-muse`), `vary-e4-v1` (assigned `design-pi`), and `vary-e4-v2` (assigned `design-claude`) — cross-pollinating each winning Round 1 mockup onto models that did *not* author it.
4. **Round 2 (`4` parallel variations → `judge-2` → `select-2`):** Each variation reads its parent bundle read-only, applies `judge-1`'s `variation_guidance` in its own bundle directory, and is rated blind by `judge-2`. Suppose `vary-e2-v1` scores `5 ★` (`rank 1`), `vary-e4-v2` scores `4 ★` (`rank 2`), and both `e4` variations had scored `3 ★` if they regressed — `select-2` surfaces the top `3` entries (including parent `e2` or `e4` whenever a parent outscored its children).
5. **Owner review and landing:** `select-2 --render-index` writes `docs/competitions/2026-11-26-observatory-redesign.md` (with zero scratch citations), `verify` checks `uvx vantage-check` and store completeness, and `Trunk.Squash` lands the single index commit while the owner opens `/compete/<item-id>` in the Observatory to click through live side-by-side iframes of the top `3` mockups and Round 1's eliminated entries.

---

## 8. Build slices, smallest first (`D-CW10`)

Nothing in this document is built in this commit. Under [`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21), build slices wait on the owner's rulings on [OQ-CW1](#OQ-CW1) through [OQ-CW6](#OQ-CW6), and then land independently smallest-first (`D-CW10`):

| Slice | What lands | Packages touched & targeted verification | Gated on |
| ---: | :--- | :--- | :--- |
| **1. Single-round bundles, pre-read rubric, and store ratings (`R = 1`)** | Add `"bundle"` output validation and manifest `sha256` in `checkNodeOutputs` (`D-CW2`); register `competition_rounds`, `competition_entries`, and `competition_ratings` via `store.RegisterDDL` (`D-CW5`); add `compete-select` command (`config/tillr/commands.json`) with blind-slot remapping and `rubric_sha256` verification (`D-CW4`); ship `competition-1r@v1`, judge-disjointness check (`D-CW8`), without-replacement `SeatLists` draw, and `tillr item compete --dry-run` run-count preview (`D-CW9`). | `tools/tillr/store`, `tools/tillr-drain/workflow`, `tools/tillr-drain/drain`, `tools/tillr-item/item`, `config/tillr/`; targeted unit tests on bundle Lstat bounds, blind slot mapping, `1..5` star intake, and `--dry-run` run math (`just pack-sync`). | [OQ-CW1](#OQ-CW1), [OQ-CW4](#OQ-CW4), [OQ-CW5](#OQ-CW5), [OQ-CW6](#OQ-CW6) |
| **2. Multi-round `may_replace` variation fan-out (`R = 2..4`)** | Extend `compete-select` to emit `outputs.proposal` (`competition-round@v1`) with `K' × V` cross-seat variation `WorkflowPiece`s carrying parent bundle paths and `variation_guidance`; add `competition-2r@v1`..`competition-4r@v1`, `parent_entry_id` lineage, parent-regression flagging, and deterministic Markdown index rendering (`D-CW1`, `D-CW7`). | `tools/tillr-drain/drain`, `tools/tillr-item/item`, `config/tillr/workflows/`; targeted tests on `settleProposal` boundary wiring, tie-breaking, and `0`/`1`-survivor fallback (`just pack-sync`). | Slice 1, [OQ-CW1](#OQ-CW1), [OQ-CW5](#OQ-CW5) |
| **3. Observatory gallery and sandboxed live bundle route** | Add `/compete/<item-id>` and competition section on `/items/<item-id>` plus `GET /compete/<item-id>/entries/<entry-id>/raw/<path>` with `bundle_sha256` verification and sandbox `Content-Security-Policy` headers; add terminal table in `tillr view` honoring `D-OR5` and [`OQ-OR1`](../reference/observatory-redesign.md#why-its-this-way) (`D-CW7`). | `tools/tillr-view`, `tools/tillr-view/web`; Go handler tests for path-traversal refusal, hash mismatch, CSP headers, and Vitest UI tests (`just pack-sync`). | Slice 1, [OQ-CW5](#OQ-CW5) |
| **4. Opt-in owner picks between rounds and at finale** | Add `competition-pick` derived `owner` hold (releasing the lane), `tillr item compete-pick`, and Observatory `--write` pick form (`D-CW7`). | `tools/tillr-item`, `tools/tillr-drain/drain`, `tools/tillr-view`; targeted hold/resume and child-command write tests (`just pack-sync`). | Slice 2, Slice 3, [OQ-CW3](#OQ-CW3), [OQ-CW5](#OQ-CW5) |
| **5. Isolated per-node worktrees for code competitions and bundle/ref sweep** | Add `--mode worktree` isolated checkouts at `.tillr/items/<item-id>/nodes/<node-id>/worktree` pinned to `refs/tillr/compete/<item-id>/<entry-id>`, parent-ref branching in Round `r ≥ 2`, winner promotion onto the lane branch, and `SweepRefs` / bundle retention cleanup (`D-CW2`, `D-CW3`). | `tools/tillr-drain/drain`, `tools/tillr/store`; targeted tests on concurrent same-path code entrants, winner promotion, and `SweepRefs` predicates (`just pack-sync`). | Slice 2, `D-GP16` in [the graph design](./tillr-graph-is-the-path.md#38-concurrent-writers-checkouts-and-the-join), [OQ-CW2](#OQ-CW2) |

---

## 9. Failure modes, alternatives considered, and scope boundaries

| Failure / edge case | Behavior |
| :--- | :--- |
| One of `E` entrants fails, crashes, or omits `entry.json` | Standard item-graph retry/escalation reruns only that failed node while passed sibling entrants stay recorded (`node_reused`). If a single entrant remains blocked on a backend/seat fault after policy retries, the item holds on that node rather than silently dropping the entrant's model from the comparison. |
| Fewer than `K` entries pass basic viability (or filer set `K = 2` and only `1` entry is non-defective) | `compete-select` advances `K' = min(K, viable)` when `judge-<r>` marks an entry structurally unusable (`usable: false` with evidence), spawning `K' × V` variations instead of wasting runs varying a broken bundle. |
| Exact tie on `overall_stars` and `weighted_score` at cutoff `K` | Broken deterministically by `judge-<r>`'s required strict `rank` (`1..N`), which cannot tie (`compete-select` refuses duplicate ranks). |
| Every Round 2 variation scores lower than its Round 1 parent | `competition_entries` retains Round 1 and Round 2 rows; `compete-select` flags `parent_outscored_children = true` and includes the higher-rated Round 1 parent in the `show_top` showcase so variation never hides a stronger original. |
| HTML mockup bundle includes JavaScript attempting `fetch('/api/place', {method:'POST'})` | Blocked by `Content-Security-Policy: sandbox allow-scripts; ... connect-src 'none'` on `/compete/.../raw/*`, giving the iframe an opaque origin with network requests disabled (`D-CW7`). |
| Policy maps both `--entrant-seats` and `--judge-seat` to the same model | Refused before item creation by `tillr item compete --policy ...` (`competition-judge-overlap`) and refused at drain claim time if live policy reloaded into an overlap (`D-CW4`, `D-CW8`). |

| Alternative | Verdict |
| :--- | :--- |
| File each entrant as a separate top-level queue item connected by `item_edges` | Rejected (`D-CW1`): pollutes `/items` place order with `12`+ ephemeral sub-items, violates `D-IG2` (one item, one deliverable), and would try to land losing entries on `main`. |
| Add a 9th `competition-judge` node kind to the graph runtime | Rejected (`D-CW4`): needlessly amends the closed node-kind table in [the graph design §3.1](./tillr-graph-is-the-path.md#31-node-kinds) when a read-only `agent` node + deterministic `compete-select` command node expresses every invariant cleanly. |
| Let an LLM planner stage write `round-2`'s `graph.proposal` | Rejected (`D-CW1`): top-`K` selection and piece expansion from `ratings` JSON are mechanical bookkeeping; a registered Go command (`compete-select`) is deterministic, instant, and spends zero tokens. |
| Commit all competing mockup directories under `docs/competitions/<slug>/entries/` | Rejected (`D-CW3`): bloats Git history with losing drafts and exposes raw entrant Markdown to `vantage-check` and item sync ([`OQ-MB9`](./model-benchmarks-from-drain-records.md#OQ-MB9)). |

### What this does not do

- **It does not replace the terminal verdict judge or the landing gate.** Comparative `1–5 ★` ratings select among rival entries; `CheckJudge` and the gate still govern landing ([§4.3](#43-relation-to-the-terminal-verdict-judge-d-cw6)).
- **It does not introduce mid-flight spend ceilings or dollar estimates.** Expected run counts are shown before filing, honoring [`OQ-GP3`](./tillr-graph-is-the-path.md#OQ-GP3) and [`OQ-OR1`](../reference/observatory-redesign.md#why-its-this-way).
- **It does not build code, workflows, or schema migrations in this commit.**

---

## Open Questions

1. 💬 **OQ-CW1: Should competition rounds stay a finite unrolled stage sequence (`1..4` rounds, waves of `max_parallel ≤ 8`), or amend recorded graph rules?**

   Unrolling `1..4` round slots in tracked workflows with `may_replace` variation fan-out obeys `D-IG1` and `D-IG9` (no cycles, no self-rewriting stages). Supporting arbitrary round counts ("keep varying until an entry hits `5 ★`") or >`8` simultaneous entrants requires amending those rules.

   - **A — Keep finite unrolled stages (`R` in `1..4`) and existing `max_parallel` (`8`), running `9..12` entrants in two waves.** Zero changes to `D-IG1` or `D-IG9`; exact worst-case run count is known before starting.
   - **B — Keep finite unrolled stages (`R` in `1..4`), and raise `workflow-max-parallel` from `8` to `12` (still bounded by configured lanes).** Lets up to `12` read-only mockup entrants run in one wave when lane/express capacity permits, with no cycle rule change.
   - **C — Amend `D-IG1` / `D-IG9` to allow bounded self-similar tail replacement (`competition-round@v1` proposing `competition-round@v1` up to `max_rounds`).** Supports arbitrary `R` and early stopping on a target star score, at the cost of less predictable run counts and a new termination rule in `settleProposal`.

   <!-- vantage: question id=OQ-CW1 leaning="B: four rounds cover real design and code tournaments while keeping graphs cycle-free and pre-countable under D-IG1, and raising max_parallel to 12 matches pieces.most without bypassing lane budgets." -->

   _Leaning:_ B: four rounds cover real design and code tournaments while keeping graphs cycle-free and pre-countable under `D-IG1`, and raising `max_parallel` to `12` matches `pieces.most` without bypassing lane budgets.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-CW2: How long should losing (and unlanded finalist) entry bundles and `refs/tillr/compete/*` refs stay on disk after a competition finishes?**

   Compact metadata, `1–5 ★` scores, and judge notes stay in `.tillr/state.db` permanently. Full HTML/asset bundles under `.tillr/loop/<run>/artifacts/` and worktree commits under `refs/tillr/compete/<item-id>/*` stay off `main`, so deleting them removes the last copy of those unlanded files.

   - **A — Retain all round bundles and refs while the item is open or linked to an open follow-up item; sweep only after explicit disposition or follow-up landing.** Mirrors [saved ended-item refs](./tillr-ended-item-saves.md): once the competition item is terminal *and* either a follow-up winner item has landed or the operator/owner records `compete-dispose`, `SweepRefs` / artifact cleanup removes unheld bundles and refs.
   - **B — Retain top-`S` finalist bundles until explicit disposition, and sweep eliminated lower-ranked bundles as soon as the competition item lands or closes.** Shrinks disk footprint immediately while keeping SQLite ratings for losers, at the cost of losing raw HTML/code of eliminated entries once the item lands.
   - **C — Retain every entry bundle and ref indefinitely until manual operator cleanup.** Zero automatic deletion, at the cost of steady disk growth in `.tillr/loop/`.

   <!-- vantage: question id=OQ-CW2 leaning="A: inspecting a lower-rated Round 1 entry after the item lands is often how you catch a judge blind spot, while sweeping once a follow-up implementation lands or is explicitly disposed prevents unbounded accumulation." -->

   _Leaning:_ A: inspecting a lower-rated Round 1 entry after the item lands is often how you catch a judge blind spot, while sweeping once a follow-up implementation lands or is explicitly disposed prevents unbounded accumulation.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-CW3: Where should you enter a competition by default — only at the end, or also between rounds?**

   Running all `R` rounds unattended delivers refined variations in one pass, whereas a judge on aesthetic work can eliminate a bold Round 1 direction before Round 2 ever varies it.

   - **A — End-only by default (`--between-rounds judge`), with opt-in `--between-rounds owner` releasing the lane for your pick between rounds.** Ordinary competitions run end-to-end unattended (plus surface any parent that outscored its children); subjective visual briefs can opt into a lane-free pick hold after Round 1.
   - **B — End-only always.** Never pause between rounds; keep `show_top` and Round 1 parent-regression badges at the end, and if you prefer an eliminated Round 1 entry you file a new variation competition from that entry ID.
   - **C — Pause for your pick between rounds by default (`--between-rounds owner`), with `--between-rounds judge` as opt-in.** You approve or override every round's advancing set before variation spend runs.

   <!-- vantage: question id=OQ-CW3 leaning="A: unattended execution is why a comparative judge exists, parent-regression surfacing protects strong Round 1 entries automatically, and opt-in --between-rounds owner covers high-taste visual briefs without holding a lane." -->

   _Leaning:_ A: unattended execution is why a comparative judge exists, parent-regression surfacing protects strong Round 1 entries automatically, and opt-in `--between-rounds owner` covers high-taste visual briefs without holding a lane.

   **Answer:**

   > _(empty — fill in when decided)_

4. 💬 **OQ-CW4: Should per-entrant model diversity use policy-declared `seats` only, or also allow naming policy `backends` at filing time?**

   Under `D-IG13` and [`OQ-PR3`](../tillr/decision-provenance-review.md#OQ-PR3), workflow nodes bind to policy `seats`, never raw model IDs. Pitting four specific models against one another via seats alone requires declaring four distinct seats (`design-pi`, `design-claude`, `design-agy`, `design-muse`) in the external `--policy` file first.

   - **A — Policy `seats` only (keep `D-IG13` unchanged), plus without-replacement draws on multi-backend `SeatLists` seats.** Entrants bind to `--entrant-seats` or spread automatically across a shared `compete` seat's backend list without repeating a backend in the same round.
   - **B — Also allow `--entrant-backends` and `--judge-backend` in `items.workflow_args`, restricted to backend keys declared in `--policy`'s `backends` map.** Tracked workflow JSON files under `config/tillr/workflows/` remain completely model- and backend-free, while a filer can pit `pi-designer,claude,agy,muse` against one another without adding one seat per backend to the policy file (amends `D-IG13` for `WorkflowPiece` / item args only).

   <!-- vantage: question id=OQ-CW4 leaning="B: tracked workflow files stay model-free and credentials stay governed by --policy, while letting tillr item compete name declared policy backends directly avoids editing the policy file every time you want a specific model shootout." -->

   _Leaning:_ B: tracked workflow files stay model-free and credentials stay governed by `--policy`, while letting `tillr item compete` name declared policy backends directly avoids editing the policy file every time you want a specific model shootout.

   **Answer:**

   > _(empty — fill in when decided)_

5. 💬 **OQ-CW5: How should a finished competition present and land its outcome by default?**

   The Observatory can serve interactive HTML mockups and sortable `1–5 ★` tables from `.tillr/`, whereas a committed Markdown index under `docs/competitions/` leaves a durable, reviewable record in Git without committing raw entry bundles.

   - **A — Both by default (`--deliverable both`): Observatory gallery + a compact scratch-free Markdown index landed at `docs/competitions/<date>-<slug>.md`, with `--deliverable observatory` (`no_commit`) and `--deliverable winner` available as flags.** Gives every design tournament both live interactive mockup previews in the Observatory and a permanent Git summary record.
   - **B — Observatory gallery only (`no_commit` until you promote a winner).** Nothing lands in `docs/competitions/` automatically; results live in `.tillr/state.db` and the Observatory until you either close the item or pick a winning entry to land/follow up.
   - **C — Landed Markdown index only, with no new Observatory competition view.** Relies on static summaries in `docs/competitions/` and manual opening of `.tillr/loop/` files on disk.

   <!-- vantage: question id=OQ-CW5 leaning="A: HTML mockups need the Observatory's live sandboxed preview route to be experienced properly, while a compact landed index in docs/competitions/ preserves the rubric, scores, and rationale in Git after scratch bundles are swept." -->

   _Leaning:_ A: HTML mockups need the Observatory's live sandboxed preview route to be experienced properly, while a compact landed index in `docs/competitions/` preserves the rubric, scores, and rationale in Git after scratch bundles are swept.

   **Answer:**

   > _(empty — fill in when decided)_

6. 💬 **OQ-CW6: Should `tillr item compete` require an explicit `--confirm-runs` acknowledgement when expected agent runs exceed a soft threshold?**

   Under [`OQ-GP3`](./tillr-graph-is-the-path.md#OQ-GP3), spend never halts an active item once queued. At filing time, `tillr item compete` always prints the exact expected agent-run count; a single typo in `--rounds` or `--variations` can queue `30`–`50` frontier runs at once.

   - **A — Always print the per-seat run table, and refuse to queue >`20` expected agent runs unless `--confirm-runs <N>` is passed at filing time.** Never stops a running item ([`OQ-GP3`](./tillr-graph-is-the-path.md#OQ-GP3) intact), while catching accidental combinatorial blow-ups at the CLI gate before any run starts.
   - **B — Print the per-seat run table unconditionally and rely solely on static parameter caps (`E ≤ 12`, `K × V ≤ 12`, `R ≤ 4`), with no `--confirm-runs` flag.**

   <!-- vantage: question id=OQ-CW6 leaning="A: requiring --confirm-runs above 20 runs costs one flag when a large tournament is intentional and prevents an accidental --entrants 8 --advance 4 --variations 3 --rounds 3 command from quietly scheduling 35 runs." -->

   _Leaning:_ A: requiring `--confirm-runs` above `20` runs costs one flag when a large tournament is intentional and prevents an accidental `--entrants 8 --advance 4 --variations 3 --rounds 3` command from quietly scheduling `35` runs.

   **Answer:**

   > _(empty — fill in when decided)_

---

## Decision Ledger

Every row in this ledger is the designer's decision, made in response to the 2026-11-26 command-line request without new rulings from the owner beyond the 2026-11-26 prompt quoted in [§1.1](#11-verbatim-owner-request-and-settled-rulings), and the owner may overturn any of them ([`D-PR2`](../tillr/decision-provenance-review.md#6-how-this-stops-happening)). Governing owner rulings in earlier documents ([`OQ-PR3`](../tillr/decision-provenance-review.md#OQ-PR3), [`OQ-PR17`](../tillr/decision-provenance-review.md#OQ-PR17), [`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21), [`OQ-IG1`](../reference/tillr-item-graphs.md#why-its-this-way) to [`OQ-IG3`](../reference/tillr-item-graphs.md#why-its-this-way), `D-IG4`, `D-IG11` to `D-IG13`, `R-GP1`, [`OQ-GP3`](./tillr-graph-is-the-path.md#OQ-GP3), [`OQ-OR1`](../reference/observatory-redesign.md#why-its-this-way), `R-OD1`) remain owned by those documents' ledgers.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D-CW1 | **Designer's decision; the owner may overturn this.** Express a multi-round competition on built item graphs as `rubric` → repeated `"pieces"` entrants (`round-1`) → `judge-1` → deterministic `command` stage `compete-select` (`may_replace` splicing `competition-round@v1` with `K' × V` variation pieces into unstarted `round-(r+1)`) across `1..4` finite unrolled rounds, subject to [OQ-CW1](#OQ-CW1). **Rejected:** cycles, self-rewriting stages, LLM selection stages, and separate top-level queue items per entrant. | 2026-11-26, designer | [§2.1](#21-stage-topology-for-r-rounds)–[§2.3](#23-fixed-stage-unrolling-vs-a-new-round-rule) | No |
| D-CW2 | **Designer's decision; the owner may overturn this.** Add a bounded regular-file directory output port `"bundle"` (`entry.json` manifest + Lstat bounds of `256` files, `2 MiB` per file, `16 MiB` total + canonical manifest `sha256` in `node_artifacts`) so `read_only` artifact entrants run concurrently without touching the Git worktree; for `--mode worktree` code competitions (Slice 5), run entrants in isolated `.tillr/items/<item-id>/nodes/<node-id>/worktree` checkouts pinned at `refs/tillr/compete/<item-id>/<entry-id>`, branching Round `r ≥ 2` variations from their parent entry's ref and converging by winner promotion rather than three-way `join`. | 2026-11-26, designer | [§3.1](#31-per-node-bundle-directories-for-artifact-competitions-d-cw2), [§3.2](#32-isolated-per-node-worktrees-for-code-competitions-d-cw2) | No |
| D-CW3 | **Designer's decision; the owner may overturn this.** Never commit losing entry bundles or competing worktree drafts to `main`; persist every entry's metadata, `1–5 ★` scores, and judge notes permanently in `.tillr/state.db`, keep zero scratch paths in any landed Markdown index, and govern post-completion disk bundle and `refs/tillr/compete/*` deletion by [OQ-CW2](#OQ-CW2). | 2026-11-26, designer | [§3.3](#33-what-is-kept-for-losing-entries-and-what-lands-d-cw3) | No |
| D-CW4 | **Designer's decision; the owner may overturn this.** Keep the competition judge as a standard read-only `agent` (`work`) stage paired with `command` stage `compete-select` (adding no 9th node kind to [the graph design §3.1](./tillr-graph-is-the-path.md#31-node-kinds)); wire `rubric` with zero incoming edges from entrants, verify `rubric_sha256` on every round's ratings, present entries to `judge-<r>` under blind shuffled `slot-1..N` labels without model metadata, and refuse overlapping entrant/judge models (`competition-judge-overlap`). | 2026-11-26, designer | [§4.1](#41-criteria-written-before-reading-and-blind-cross-model-evaluation-d-cw4) | No |
| D-CW5 | **Designer's decision; the owner may overturn this.** Validate `ratings` JSON in `compete-select` (`overall_stars` and `criterion_stars[*]` integers in `1..5`, strict `rank` `1..N`, `weighted_score`) and project `compete.*` store events into `competition_rounds`, `competition_entries`, and `competition_ratings` via `store.RegisterDDL`, breaking ties at cutoff `K` by `overall_stars`, `weighted_score`, and `rank`. | 2026-11-26, designer | [§4.2](#42-ratings-recorded-as-store-data-d-cw5) | No |
| D-CW6 | **Designer's decision; the owner may overturn this.** Keep comparative round judging (`1–5 ★` in `competition_ratings`, where low stars eliminate an entry without failing its run or retiring item criteria) strictly separate from the terminal verdict judge (`exit.evaluator_slot` / `CheckJudge`, which evaluates the single final deliverable against `item_criteria` before landing). | 2026-11-26, designer | [§4.3](#43-relation-to-the-terminal-verdict-judge-d-cw6) | No |
| D-CW7 | **Designer's decision; the owner may overturn this.** Expose `/compete/<item-id>` (and an `/items/<item-id>` panel) in `tools/tillr-view` with top-`S` finalists, Round `1..R` lineage, parent-regression surfacing when all variations score below their parent, and `GET /compete/<item-id>/entries/<entry-id>/raw/<path>` served with `bundle_sha256` verification and `Content-Security-Policy: sandbox allow-scripts; ... connect-src 'none'`; render landed Markdown indexes deterministically via `compete-select --render-index`, subject to [OQ-CW3](#OQ-CW3) and [OQ-CW5](#OQ-CW5). | 2026-11-26, designer | [§5.2](#52-where-the-owner-comes-in-end-only-default-vs-between-round-picks), [§5.3](#53-presentation-observatory-gallery-landed-index-or-both-d-cw7) | No |
| D-CW8 | **Designer's decision; the owner may overturn this.** File competitions through `tillr item compete`, cycling `--entrant-seats` across entrants, rotating Round `r ≥ 2` variations of each survivor across distinct seats, and drawing without replacement when siblings share a multi-backend `SeatLists` seat, subject to [OQ-CW4](#OQ-CW4). | 2026-11-26, designer | [§5.1](#51-parameters-on-tillr-item-compete) | No |
| D-CW9 | **Designer's decision; the owner may overturn this.** Compute and print exact expected agent runs (`J_rubric + (E + 1) + (R - 1)(K × V + 1) + J_verdict`), command runs, critical-path agent depth, peak width, and per-seat/per-backend run counts in `tillr item compete --dry-run` and on filing, with no dollar estimates ([`OQ-OR1`](../reference/observatory-redesign.md#why-its-this-way)) and no mid-flight spend stop ([`OQ-GP3`](./tillr-graph-is-the-path.md#OQ-GP3)), subject to [OQ-CW6](#OQ-CW6). | 2026-11-26, designer | [§6](#6-cost-visibility-before-starting-d-cw9) | No |
| D-CW10 | **Designer's decision; the owner may overturn this.** Ship in the five smallest-first slices of [§8](#8-build-slices-smallest-first-d-cw10) once [OQ-CW1](#OQ-CW1) to [OQ-CW6](#OQ-CW6) are ruled under [`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21). | 2026-11-26, designer | [§8](#8-build-slices-smallest-first-d-cw10) | No |
