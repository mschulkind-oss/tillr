---
title: Agent Context Delivery — How Instructions Should Reach an Agent
date: 2026-10-26
status: draft
stage: SKETCH
next: "Owner review of the decision ledger, whose D-AC rows are a designer's recommendations. OQ-AC3 is routed from the pi roadmap's Icebox."
board: "224"
tags: [agents, context-delivery, briefing, pi-fork, board-drain, subagents]
summary: Rules should travel at the granularity of their invariance — fleet rules in the broadcast briefing, role rules in agent definitions, run rules in the dispatcher's brief, hard limits in code and config, never in prose. The week's three leaks were one missing concept (no per-run prose channel, now fixed via the brief), one unguarded ambient extension, and one global config ceiling. Neither mode-scoping the briefing nor rebuilding on Pi's split libraries fixes the class; the briefing is materialized per home, and all context machinery lives in pi-coding-agent, not the lower libraries.
---

> **2026-11-18 (updated 2026-11-19): the body below originated as a 2026-10-26 design for the board drain; board-drain-era paths and symbols are historical.** Since then the board's drain and `tools/board-cli` were deleted and tillr replaced them, `design-lane.ts` was deleted on 2026-11-11, and the retired autonomous-landing design was superseded by [`landing-principles.md`](landing-principles.md). What still holds is the ruling the body argues for: a rule is placed by how long it stays true, the brief is the one prose channel scoped to a single run, and hard limits live in code and config. Today the brief is [`brief-template.md`](../../tillr-run/backends/brief-template.md), whose ten required sections [`preflight.sh`](../../tillr-run/backends/preflight.sh) enforces, and a lane's process is identified by `TILLR_RUN_ID` and `TILLR_LANE_ID`; in [`run-pi`](../../tillr-run/backends/pi/run-pi), `BOARD_DRAIN_BRIEF`, `BOARD_DRAIN_RUN`, and `BOARD_DRAIN_SESSION` survive only as adapter compatibility names, and on continuation `TILLR_RESUME_PROMPT_FILE` replaces the first prompt while the staged brief remains available by path. The per-agent thinking ceiling of `D-AC7` is not built (`yolo-packs/matt/pack.json` keeps `subagents.maxThinking` fleet-wide). The owner has not reviewed the ledger: its `D-AC` rows are a designer's recommendations.

## In short

The interface gap is real but narrower than it feels. This fleet already owns every conditional mechanism it needs — per-lane environment identity (`BOARD_DRAIN_*`), per-lane process argv (`--model`, `--thinking`, `--tools`, `--extension`), per-agent definitions with prompt/tool/model/thinking fields, and an extension hook (`before_agent_start`) that can rewrite the system prompt per process. What was missing was exactly one concept: a prose channel whose scope is *one run*. Today's fix — lane rules composed into the lane brief — is that channel, and this design rules it the governing mechanism rather than a stopgap.

The two larger moves on the table do not survive inspection. Mode-scoping the briefing (a pack declaring "lanes only") cannot work at the briefing's granularity: briefing contributions are rendered at launch into files in the agent home (`~/.pi/agent/AGENTS.md` for pi), so their scope is a *home*, not a process or a task. Rebuilding on Pi's split libraries buys nothing for context delivery: extension loading, skill loading, `AGENTS.md` discovery, and system-prompt assembly all live in `pi-coding-agent`, not in `pi-ai` or `pi-agent-core`, so depending on the split packages means reimplementing precisely the machinery under discussion — plus the tools, session handling, and TUI — while the drain is broken.

And the prior-art question has a plain answer: the "Evaluating pi-forks purpose and necessity" session did not decide Pi-versus-libraries at all. It decided the scope of the `bin/pi-forks` tooling. Section 2.5 gives the detail; the dependency question is decided here for the first time, on the evidence in Section 1.

## 1. The gap, named precisely

### 1.1 Channels and their true granularity

Four channels are usually named. There are really seven delivery surfaces, and they differ in *when* scope is resolved — that resolution point, not the file format, is what a rule's scope can be.

| Channel | Scope resolved | Granularity | Conditional on a run? | Evidence |
|---|---|---|---|---|
| Pack briefing (`briefing/*.md`) | At launch, by the pack materializer | Per agent home | No | Manifest keys are only `agents`, `from`, `into` (`agents`/`into` mutually exclusive; neither means broadcast); contributions render into the home (pi: `~/.pi/agent/AGENTS.md`) and every pi process in that home reads the same file |
| Pack `files` → extensions | At install into the home; at runtime, by the extension's own code | Per home at load; per process if the code self-gates | Only as hand-written code | Historically, `design-lane.ts` was contributed unconditionally (`yolo-packs/matt/pack.json`, `files` from `files/extensions/design-lane.ts` into `.pi/agent/extensions/`) and gated in `registerDesignLane` on `BOARD_DRAIN_ARTIFACT` (deleted on 2026-11-11) |
| Skill | At invocation, by the model | Per agent that can see it; per-agent definition can set `skills`/`inheritSkills` | Triggered, never guaranteed | Agent-definition fields in `.forks/pi-subagents/src/agents/agents.ts` |
| `AGENTS.md` / `CLAUDE.md` | At session start, by working directory | Per repository | No | `loadProjectContextFiles` in `packages/coding-agent/src/core/resource-loader.ts` |
| Agent definition (pi-subagents) | At dispatch, by role name | Per role | Per role, not per task | Fields include `thinking`, `model`, `tools`, `excludeTools`, `systemPromptMode`, `inheritProjectContext`, `inheritGlobalContext`, `inheritSkills`, `extensions`, `subagentOnlyExtensions` (`agents.ts`) |
| The brief (lane brief / subagent task) | At dispatch, by whoever composes it | Per run | Yes — the only prose channel that is (on Pi continuation, `TILLR_RESUME_PROMPT_FILE` replaces the first prompt while the staged brief remains at its path) | Today: [`brief-template.md`](../../tillr-run/backends/brief-template.md) staged per run and checked by [`preflight.sh`](../../tillr-run/backends/preflight.sh), with [`run-pi`](../../tillr-run/backends/pi/run-pi) passing the brief (or `TILLR_RESUME_PROMPT_FILE` on continuation). Historically (2026-10-26): the deleted `tools/board-cli/internal/board/lane_backend.go` / `resume.go` / `drain.go` / `design.go` (`laneRules`, `childBrief`, `designChildBrief`) |
| Process argv + env (drain only) | At spawn, by the drain | Per lane | Yes, for hard knobs, not prose | Today: [`tools/tillr-run/run/exec.go`](../../tillr-run/run/exec.go) exports `TILLR_RUN_ID` / `TILLR_LANE_ID`, and [`run-pi`](../../tillr-run/backends/pi/run-pi) keeps `BOARD_DRAIN_BRIEF`, `BOARD_DRAIN_RUN`, `BOARD_DRAIN_SESSION` as compatibility names. Historically (2026-10-26): `drainChildEnv` in the deleted board drain exported `BOARD_DRAIN_*` and `BOARD_TOPIC` / `BOARD_SEAT` |

Two under-appreciated facts fall out of the table. First, **identity was never missing**: a lane process can be told from an ordinary session with certainty, from `TILLR_RUN_ID` / `TILLR_LANE_ID` today (historically from `drainChildEnv`'s `BOARD_DRAIN_*` exports) — an in-process consumer just has to look. Second, **the briefing and the brief resolve at opposite ends of a run's life**: the materializer that renders the briefing runs before any mode exists and writes to a home shared by every session launched from it, while the dispatcher composing a brief knows the item, the role, and the mode exactly. Any design that wants per-run prose must compose where the mode is known. That is the whole argument of this document in one sentence.

### 1.2 What "mode scoping" would mean, per channel

"Mode" here means the run's operating context — drained lane (implementing or designing), lead-orchestrated, or plain interactive session — as opposed to *who* the agent is or *which repository* it is in.

| Channel | What mode scoping would concretely require | Can it express "lanes only"? |
|---|---|---|
| Briefing | The launch-time materializer would need to know the mode of a process that does not exist yet, and the rendered destination would need to be partitioned by mode, since one home otherwise serves lane and interactive processes the same `AGENTS.md` | Not at current granularity; only if homes became per-mode or per-process |
| Extension | Nothing new: read lane identity env (`TILLR_RUN_ID` / `TILLR_LANE_ID` today, historically `BOARD_DRAIN_*`) from `process.env` before registering hooks — the idiom the deleted `registerDesignLane` used and `steering-timer.ts` uses today. A declared form (a manifest `activation` key checked by the loader) does not exist (and was rejected in [`OQ-AC1`](#OQ-AC1)) | Yes, today, as code — opt-in per extension, invisible to review at the manifest level |
| Skill | A mode-visible skill set: partly expressible today per role (`skills`, `inheritSkills` in an agent definition), not per run | Per role only; and a skill only fires if the model invokes it, so it can carry know-how, never a rule that must hold |
| `AGENTS.md` | Nothing sensible — directory nesting is the only sub-scope | No |
| Agent definition | Mode *is* role selection at dispatch — but only for pi-subagents children. A drained lane is a bare `pi` process; nothing in it reads an agent definition | For subagent workers, yes; for drain lanes, not without new plumbing (Section 2.3) |
| Brief | Already mode-scoped by construction: the drain composes lane briefs, a lead composes worker briefs | Yes — in production since today's `laneRules` fix |
| Config ceilings (`subagents.maxThinking`) | A per-agent key in the override surface. The data model already has a per-agent `maxThinking` field; it is populated only by the global apply, never by per-agent override config (`agents.ts`) | Yes, with a small patch — this is a config-surface gap, not a concept gap |

### 1.3 The three leaks, classified

| Leak | Missing concept, or bug in a mechanism? | Precise fault | State |
|---|---|---|---|
| 1. `subagents.maxThinking` refused `xhigh` for the design seat | **Bug in a mechanism** — an incompletely surfaced field | Per-agent `thinking` and a per-agent `maxThinking` slot both exist in `agents.ts`, but only the global apply writes `maxThinking`; no per-agent override is expressible, so raising the ceiling raised it fleet-wide | Open; fixed by D-AC7, a small pi-subagents patch, unrelated to prose delivery |
| 2. `design-lane.ts` fenced tools in ordinary sessions | **Bug in the use of a mechanism, on a channel whose conditionality is undeclared** | Historically, the pack `files` contribution was unconditional, so the extension installed into every home; the runtime self-gate on `BOARD_DRAIN_ARTIFACT` was absent at first, and nothing in the manifest declared that it should exist | Historical repair: `registerDesignLane` gated on `BOARD_DRAIN_ARTIFACT` (`c2e9159dc…`), and `design-lane.ts` was later deleted on 2026-11-11. On enforcement without a manifest key, see [`OQ-AC1`](#OQ-AC1) |
| 3. Lane workflow rules in `briefing/work-rules.md` read as everyone's rules | **The one missing concept** | At the time there was no prose channel between "every process in this home" and "this repository". The `[lane]` prose tags tried to simulate scope inside a broadcast and were rightly rejected: a rule explaining why it does not apply still costs the reading and still reads as an instruction | Fixed on 2026-10-26 via `laneRules` in the deleted board drain; today tillr stages briefs from [`brief-template.md`](../../tillr-run/backends/brief-template.md) |

So of three leaks that felt like one design flaw, only the third was. The other two are instances of one recurring shape — *a hard constraint delivered on a too-broad surface* — and each has its own narrow repair. A single new "mode" mechanism bolted onto the briefing would have fixed, at best, one leak of three, and that one only if homes were re-partitioned.

## 2. Ruling on the mechanism

### 2.1 Alternative (a) — Rules travel in the brief, composed by the dispatcher

Historically (2026-10-26), this was implemented by `laneRules` as a Go string constant in the deleted `drain.go` (`childBrief` and `designChildBrief`), which made rule changes code changes and risked divergence. Today tillr stages the brief from [`brief-template.md`](../../tillr-run/backends/brief-template.md) and checks its ten required headings in [`preflight.sh`](../../tillr-run/backends/preflight.sh) (though the template is generic and does not carry the unbuilt canonical lane-rule extraction of D-AC10). **Cost of getting it wrong:** a dispatcher that forgets a rule produces a lane without it; a rule in a brief is a task-level instruction, not a system-level one, and stays advisory to a model that skims. Both failures are *local* — one lane, visibly, never a silent fleet-wide broadcast. **Cannot express:** hard constraints (a brief cannot fence a tool or cap thinking); rules for sessions with no dispatcher (plain interactive work); any guarantee that two dispatchers compose the same rules.

### 2.2 Alternative (b) — The briefing gains a mode/task scope

**Build cost:** medium in code (a manifest key, materializer support), high in consequence: because contributions render per home, a mode scope is only honest if homes are partitioned per mode or per process, and if the drain — which spawns bare `pi`, not the pack launcher — re-renders or selects the right home per lane. That is a change to the launch topology, not to a schema. **Cost of getting it wrong:** the exact leak class that prompted this design, except silent and at broadcast strength; every session in a mis-scoped home is contaminated at once. It also creates a global mode taxonomy that every pack must agree on forever. **Cannot express:** per-item variation inside a mode, rules for an already-launched home, or anything the dispatcher knows that the launch did not. **Verdict: rejected** as the mechanism for run-scoped rules. The existing `agents:` audience key stays the briefing's only scope, and the briefing keeps only what is true for every process that will ever share the home.

### 2.3 Alternative (c) — A lane is an agent definition

Give lane kinds their own agent definitions (prompt, tools, model, thinking, inheritance) so a lane *is* a role. The definitions are rich enough today — the obstacle is the consumer. A drained lane is a separate `pi` process by settled design (session resume via `BOARD_DRAIN_SESSION`, worktree isolation); nothing in that process reads a definition, and `BOARD_DRAIN_AGENT` is exported but unconsumed. Three ways to close that: make the drain a pi-subagents parent (reopens the process-isolation decisions the drain is built on — rejected); patch `pi` itself with an `--agent <name>` profile that applies a definition at startup (a real fork patch, and it couples the definition format across the pi and pi-subagents forks); or treat the definition's *prompt file* as the canonical rule source and have each composer — drain or lead — include it in the brief. **Build cost:** high for the first two, low for the third — because the third is alternative (a) with a single source of truth. **Cost of getting it wrong:** for the true (c), a second definition-loading path that can drift from pi-subagents', and lane behavior that changes when an unrelated pack edits a role. **Cannot express:** per-item rules (a definition is per role by definition); it still needs the brief for everything run-specific. **Verdict: deferred in its true form; adopted in its cheap form** — role-invariant rules belong in agent definitions where a definition is already the delivery vehicle (subagent workers), and lane rules get one canonical source file that composers embed (D-AC10).

A variant worth naming, because it is available today without any fork patch: **(c′) env-gated prompt injection by an extension.** The extension API's `before_agent_start` event (`.forks/pi/packages/coding-agent/src/core/extensions/types.ts`) lets a handler replace the system prompt or inject a message per session. An extension that reads `BOARD_DRAIN_ITEM` and, only then, injects the lane rules would deliver run-scoped prose at system precedence, per process, from one file. Its failure class is leak 2's — ambient installation, undeclared gating — so it is an escalation path if brief-level delivery proves too weak, not a starting point (D-AC11).

### 2.4 Alternative (d) — Move control into the runtime: depend on the split packages

The owner's instinct — own the layer instead of tolerating an interface gap — deserves a direct answer: **there is no owned layer underneath the gap.** In the fork's own monorepo (`.forks/pi/packages/`, 15 packages), everything this design is about lives in `pi-coding-agent`: `loadExtensions` (`core/extensions/loader.ts`), `loadSkills`/`discoverSkills` (`core/skills.ts`), `loadProjectContextFiles` (`core/resource-loader.ts`), `buildSystemPrompt` (`core/system-prompt.ts`), plus the built-in tools and session management. The split libraries below it — `pi-ai` (model API), `pi-agent-core` (agent loop, transport, state), `pi-tui` — contain none of it. So (d) does not mean "keep the machinery, gain control of delivery"; it means *reimplementing* delivery, tools, sessions, and UI, discarding pi-subagents with it (an extension on the very package being dropped), and carrying alone a delta the fork currently carries at roughly 170 patches, `coding-agent`-dominant. The mode concept would still have to be designed from scratch on top — the libraries do not have it either. **Cost of getting it wrong:** the platform rebuilt under a broken drain, with every existing pack, extension, and agent definition invalidated at once. **Verdict: rejected** for this problem. The control the owner wants already exists at the layers we patch: pi-subagents definitions, drain argv/env, extension self-gating, and `before_agent_start`.

### 2.5 What the earlier session actually decided

A scout read the session the owner remembered — `2026-10-25T18-39-42-454Z_01a1745c-…` under `~/.pi/agent/sessions/--workspace--/`, titled "Evaluating pi-forks purpose and necessity", necessity exchange at 2026-10-26T01:54–01:56Z. **It concluded nothing about depending on Pi's split libraries versus Pi itself, because that was not its question.** Its subject was the `bin/pi-forks` maintenance tooling: the verdict was "keep it — but roughly half of it is now dead weight", keeping the branch-centric half (setup, merge-check, lag, fetch, published, build/run) and dropping the series-shaped half (export, check, verify) after the cutover to a published patch branch. The owner approved on the spot ("yes do it all right now. your 1-4."). So Section 2.4 is a first ruling on the dependency question, not a reopening — and it lands on the same side as that session's spirit: the patched branch *is* the dependency strategy; the work is keeping its surface honest, not replacing it.

### 2.6 Recommendation

** Adopt (a) as the governing mechanism, hardened: placement by invariance.** Every rule goes to the channel whose granularity matches how long the rule is true, and no rule is written one level broader and "contextualized" in prose:

| If the rule is true for… | It lives in… | Never in… |
|---|---|---|
| Every process in any home (fleet) | Pack briefing, `agents:`-scoped where narrower | — |
| A role, whoever dispatches it | Its agent definition (subagent path); the canonical lane-rule source (process path) | The briefing |
| One repository | `AGENTS.md` | Briefs copied by hand |
| One run: a lane, an item, a dispatch | The brief, composed by the dispatcher from canonical sources | The briefing, with or without tags |
| A hard limit (tools, thinking, model, extension activation) | Code and config: `laneCommand` argv, agent-definition fields, env-gated extensions, per-agent ceilings | Prose anywhere |

This is mostly a ratification of today's fix, plus the discipline that makes it stay fixed: one canonical source per rule set (D-AC10), hard knobs out of prose (D-AC6), and the two narrow mechanism repairs for leaks 1 and 2 (D-AC7, D-AC8).

**What would overturn it.**

- (i) Measured lane-rule violations *despite* the brief carrying the rule — brief precedence too weak → escalate to (c′) env-gated system-prompt injection, not to (b).
- (ii) A third and fourth brief composer appear and their rule copies observably diverge → the single-source discipline has failed socially; move to true (c), a definition the lane process loads itself.
- (iii) Per-process or per-mode agent homes become the fleet's launch topology for other reasons → (b) becomes cheap and honest, revisit D-AC3.
- (iv) Upstream ships a native agent-profile or conditional-context mechanism in `pi-coding-agent` → adopt it as the carrier for role rules.
- (v) Fork maintenance pain concentrates in `coding-agent` merge surface — repeated upstream-merge failures attributable to it → re-evaluate (d) on maintenance grounds; context delivery alone never justifies it.

## 3. The subagent question

The two lane kinds are not symmetric, and the brief fix does not cover them the same way — because, on inspection, the subagent path never had leak 3.

A fresh pi-subagents child receives: the parent's brief as its task, its agent definition's prompt (for the worker, `systemPromptMode: replace`, displacing Pi's base prompt), and the repository's `AGENTS.md` (`inheritProjectContext` defaults true and the worker keeps it). It does **not** receive the broadcast briefing: pack briefing composes into the parent's `~/.pi/agent/AGENTS.md`, which is *global* context, and `inheritGlobalContext` defaults false with no role enabling it. The one exception is fork-context children, which inherit the parent's whole prompt — briefing included — and should be treated as the parent for rule-scope purposes. On the tillr lane path, note also that on a resumable Pi continuation [`run-pi`](../../tillr-run/backends/pi/run-pi) selects `TILLR_RESUME_PROMPT_FILE` as the prompt, replacing the first prompt while keeping the staged brief available by path.

Three consequences. First, removing lane rules from `briefing/work-rules.md` changes nothing directly for a lead's workers; they were never reading it. What it changes is the *lead's* context when the lead is a top-level session in that home — and a lead that stops internalizing lane caps as its own is exactly the repair wanted. Second, the risk on the subagent path is the opposite of a leak: *under*-delivery. There is no `laneRules` equivalent a lead automatically composes; lane-like discipline for workers exists only if each lead writes it into each brief. Third — and this is the placement rule that resolves it — most "lane rules" govern a *loop* (repair-cycle caps, re-plan after failures), and on the subagent path the loop's owner is the lead, not the worker. Those rules belong in the lead's agent definition, where they bind the seat that counts the cycles; a worker's brief should carry the item, its bounds, and its acceptance shape, not loop policy it cannot act on. Where a lead genuinely dispatches a worker to run its own bounded loop, the lead composes from the same canonical rule source the drain embeds (D-AC10) — one text, two composers, no third copy.

So: the brief-injection fix fully covers the process lane; the subagent path needs no new channel, only the discipline of D-AC9 — loop rules in the loop owner's definition, `inheritGlobalContext` left false, and shared rule text sourced, not paraphrased.

## 4. Migration

> **Historical note (2026-11-19):** The migration table below was written on 2026-10-26 against the then-broken board drain and the retired autonomous-landing design (since deleted in favour of [`landing-principles.md`](landing-principles.md)). The "2 remedy / 3 round caps" named below and in Section 5 are stale: the owner ruled on 2026-11-17 ([`D-JL10`](tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)) that a run's ending is never counted (also noted in [`OQ-AC5`](#OQ-AC5)). Today tillr uses [`brief-template.md`](../../tillr-run/backends/brief-template.md) and [`preflight.sh`](../../tillr-run/backends/preflight.sh) (which checks the ten required headings and placeholders, not semantic rule content); D-AC10 (one canonical Markdown lane-rule source with a golden test) and D-AC7 (per-agent `maxThinking`) remain unbuilt.

At the time of writing (2026-10-26), the board drain was broken: seven candidates were unlanded, the gate had been red, and the retired autonomous-landing design owned that repair. Nothing in this design was urgent enough to touch the drain's working surface before that landed; the `laneRules` fix was already the recommendation's core. Settled decisions cited at that time (`publish.mode` off, one trunk-grant landing workspace, the carried-gate rule, 2 remedy / 3 round caps with owner-only abandon, `mq` as its own program, lane rules in the brief) were board-era rules.

| Phase | Work | Why then |
|---|---|---|
| Now (2026-10-26, board drain broken) | Ratify the Decision Ledger. Verify the shipped pack carries the `design-lane.ts` env guard (present in tree, commit `c2e9159dc…`; `design-lane.ts` was later deleted on 2026-11-11). No code moves | The only leak-class exposure not already fixed was leak 1, which is a pi-subagents change — also deferred, since agent dispatch was the drain's surface |
| Must not be migrated yet (2026-10-26) | The briefing materializer; `laneCommand`/spawn path and `BOARD_DRAIN_*` env shape; designer/worker agent definitions; any pi-fork patch touching system-prompt assembly | Every one was load-bearing for the repair the retired autonomous-landing design was making; changing delivery under it confounded its gate |
| First, once the gate is green (unbuilt) | D-AC10: extract lane rules from hardcoded constants to one canonical Markdown source embedded by composers, with a golden test pinning brief bytes so the move is provably behavior-neutral. (Tillr's [`brief-template.md`](../../tillr-run/backends/brief-template.md) and [`preflight.sh`](../../tillr-run/backends/preflight.sh) are generic and check headings/placeholders, so D-AC10 is not built by them.) | Removes the divergence risk in the mechanism we depend on, at near-zero risk |
| Second (D-AC7 unbuilt) | D-AC7: per-agent `maxThinking` in the pi-subagents override surface, with a test that the design seat may use `xhigh` under a lower fleet ceiling. D-AC9: loop rules into the lead's definition; confirm no role sets `inheritGlobalContext` | Closes leak 1's mechanism properly; aligns the subagent path with Section 3 |
| Third (see [`OQ-AC1`](#OQ-AC1)) | D-AC8: the fencing-extension discipline — every extension registering a `tool_call` fence (or equivalent refusal) must self-gate on lane env, enforced by whatever check [`OQ-AC1`](#OQ-AC1) settles on | Converts leak 2 from a remembered mistake into an impossible one |
| Only if triggered (Section 2.6) | (c′) env-gated `before_agent_start` injection; true (c) lane-loaded definitions; (b) if homes are re-partitioned; (d) only on fork-maintenance evidence | Each is a real cost against a hypothetical; none is owed on today's evidence |

## 5. What this does not solve

Plainly: **the chosen mechanism would not have prevented leaks 1 or 2.** Neither was a prose problem. Leak 1 was a number in a config schema — a fleet ceiling with no per-agent key; perfectly scoped briefs leave the design seat still refused `xhigh` before any model call. Only D-AC7 fixes it. Leak 2 was installed code refusing tool calls in sessions it never should have touched; a rule written in any prose channel, however well scoped, does not stop an extension from registering a hook. Only the env self-gate (historical; `design-lane.ts` was deleted) and the D-AC8 discipline fix it. The brief mechanism addresses leak 3's class — prose true for one run broadcast to all — and, by giving run-scoped rules a home, removes the pressure that pushed them into the briefing in the first place.

It also does not make any prose rule *binding*. A brief is an instruction to a model, not an enforcement; the caps and fences that must hold regardless of the model's reading (such as abandon rules, tool fences, and — in the 2026-10-26 board drain — the since-removed 2 remedy / 3 round caps) have to live where they already should — in drain code, argv, config, and gated extensions — with prose merely explaining them to the agent they bind. Nor does it give plain interactive sessions a mode: a session with no dispatcher has no brief, by definition, and rules that should bind *it* must go through briefing, `AGENTS.md`, or not exist.

## 6. Risks

| Risk | Shape | Mitigant |
|---|---|---|
| Brief-rule dilution | As briefs grow (item, context, rules, acceptance), a lane skims the rules the way it skimmed `[lane]` tags | Rules stay short and canonical (D-AC10); overturn trigger (i) watches for measured violations and escalates to (c′) |
| Composer divergence | Drain, design drain, and leads each "improve" their copy of the rules until lanes behave differently by parentage | One embedded source, golden-tested; leads source rather than paraphrase (D-AC9/D-AC10) |
| Env-gate spoofing or drift | A session with stray `BOARD_DRAIN_*` in its environment gets lane fences; or the drain renames a key and a fence goes inert in a real lane — failing *open* | Fences should require the item-scoped keys (`BOARD_DRAIN_ARTIFACT`, `_ITEM`), not generic ones; D-AC8's check pins the key set against `drainChildEnv` |
| Ratifying the status quo too early | (a) is ruled load-bearing on one day of operation | The overturn triggers in Section 2.6 are the exit; (c′) is pre-designed so escalation is a step, not a project |
| Home-topology assumption | Section 2.2 rests on briefing rendering per home shared by concurrent processes (scout-evidenced for the materializer); if some lanes already run in isolated homes, (b)'s cost is lower than stated for lanes specifically | Unverified for the drain's spawn environment; [`OQ-AC4`](#OQ-AC4) must be answered before any future (b) work — and it does not change today's ruling, since the brief already covers lanes |
| Prior-art misreading | Section 2.5 rests on a scout's reading of one session file; a different session may hold a genuine Pi-vs-libraries discussion the owner remembers | If such a session with a contrary, acted-on decision surfaces, D-AC4 must be re-argued against it; the located session is the one named in the brief |

## Decision Ledger

**Who decided each row.** `D-AC1` to `D-AC11` are a designer's recommendations of 2026-10-26, which the owner has not reviewed. The four `OQ-AC` rows record how each question was closed; none of them is an owner's ruling, and he may overturn any.

| ID | Decision | Basis |
|---|---|---|
| D-AC1 | Rules are placed by invariance: fleet → briefing, role → agent definition or canonical lane-rule source, repository → `AGENTS.md`, run → brief, hard limit → code/config. No rule is broadcast broader and qualified in prose | Section 2.6; the `[lane]`-tag rejection generalized |
| D-AC2 | Lane (mode/run) prose rules travel in the brief composed by the dispatcher — retained as the governing mechanism, building on the settled `laneRules` fix | Section 2.1; only per-run prose channel, already shipped |
| D-AC3 | The briefing channel gains no mode/task scope; `agents:` remains its only audience narrowing | Section 2.2; per-home materialization makes mode inexpressible honestly |
| D-AC4 | The fleet does not rebase onto `pi-ai`/`pi-agent-core` or another agent library; the patched `pi-coding-agent` branch remains the runtime | Section 2.4; all context machinery is in `pi-coding-agent`; Section 2.5 confirms no prior contrary decision exists |
| D-AC5 | A drained lane remains a separate `pi` process spawned by `laneCommand`; it is not converted into a pi-subagents child | Process isolation and session resume are the drain's settled design; (c) true form deferred per Section 2.3 |
| D-AC6 | Hard limits never travel as prose: tools, model, thinking via `laneCommand` argv and definition fields; fences via env-gated extensions; caps via drain code | Leaks 1 and 2; Section 5 |
| D-AC7 | pi-subagents gains a per-agent `maxThinking` in its override surface, so a seat may exceed the fleet ceiling by declaration | Leak 1; the field exists in `agents.ts` and is only unwired |
| D-AC8 | Any extension that can refuse a tool call must self-gate on lane env (`BOARD_DRAIN_ARTIFACT`/`BOARD_DRAIN_ITEM`-class keys) before registering its fence; enforcement mechanism per [`OQ-AC1`](#OQ-AC1) | Leak 2; generalizes the `registerDesignLane` repair |
| D-AC9 | On the subagent path, loop rules live in the loop owner's (lead's) agent definition; worker briefs carry item, bounds, acceptance. `inheritGlobalContext` stays false for all roles; fork-context children are treated as their parent for rule scope | Section 3 |
| D-AC10 | Lane rules get one canonical Markdown source, embedded by composers and sourced by any lead composing a lane-like dispatch — replacing hardcoded string constants, byte-stable under a golden test. (**Unbuilt:** tillr's [`brief-template.md`](../../tillr-run/backends/brief-template.md) and [`preflight.sh`](../../tillr-run/backends/preflight.sh) are generic and check headings/placeholders, not semantic rule content) | Section 4; hardens D-AC2 against divergence |
| D-AC11 | If brief-level delivery measurably fails, escalate to env-gated `before_agent_start` system-prompt injection from the same canonical source — not to briefing mode scope | Section 2.3 (c′); Section 2.6 trigger (i) |
| OQ-AC1 | **Designer's decision, 2026-11-18; not ruled by the owner; the owner may overturn it.** An extension that behaves differently inside a lane is held to that by a test in the pack's own suite, not by a key in the pack manifest. **Rejected:** a manifest `activation` key, which is loader work in yolo for a class that has no member today; and review convention alone | No extension registers a tool-call fence any more: `rg -c 'tool_call' yolo-packs/matt/files/extensions/*.ts` on 2026-11-18 matches nothing. The one extension that keys on lane identity, `steering-timer.ts`, ran inside a lane on 2026-11-14 because the identity keys it read were empty; it now reads the current keys and `yolo-packs/matt/tests/steering-timer.test.mjs` pins that. `extension-sync-guard.test.mjs` is the precedent for a test that walks every extension |
| OQ-AC2 | **Closed as moot on 2026-11-16; not an owner's ruling.** It asked where in the board command the canonical lane rules would be embedded. That command is deleted; tillr hands the brief to the harness from one template file | The brief template named in the note at the top of this document |
| OQ-AC4 | **Closed on 2026-11-16, answered from the tree; not an owner's ruling.** A lane runs its harness with no home override, so lanes and interactive sessions share one agent home | Section 2.2's cost for a mode-scoped briefing stands as written |
| OQ-AC5 | **Closed as stale by the designer, 2026-11-18; not ruled by the owner; the owner may overturn it.** It asked how adherence to the brief's rules would be measured. The rules it named, the repair and round caps, were removed by the owner's ruling of 2026-11-17 that a run's ending is never counted ([`D-JL10`](tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)). The one rule a brief still carries that the drain can check is its report shape, and the drain checks it on every run: a run that ends with neither a commit nor a named obstacle is recorded as a missing output and returned to its session | Measured on 2026-11-18: 19 of 61 drain runs on one model ended that way ([`D-JL11`](tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11)). That rate is the measure trigger (i) of Section 2.6 asked for |

## 8. Open Questions

- 💬 **OQ-AC3: Should `pi` gain a `--agent <name>` startup profile (true alternative (c))?**

   This would let any process — lane or interactive — wear a definition, unifying the role concept across spawn paths. It is a cross-fork format commitment, so it should wait for a trigger from Section 2.6 rather than being built speculatively.

   <!-- vantage: question id=OQ-AC3 leaning="No, until a Section 2.6 trigger fires." -->

   _Leaning:_ No, until a Section 2.6 trigger fires.

## 9. Defined Terms

| Term | Meaning in this document |
|---|---|
| **Broadcast** | A channel whose every recipient receives the same content with no per-recipient condition — the pack briefing within an agent home. |
| **Brief** | The per-run prose composed by a dispatcher for one run: the staged brief from [`brief-template.md`](../../tillr-run/backends/brief-template.md) (historically `BOARD_DRAIN_BRIEF`, kept in [`run-pi`](../../tillr-run/backends/pi/run-pi) as an adapter compatibility path; replaced on Pi continuation by `TILLR_RESUME_PROMPT_FILE` for the first prompt while the brief remains at its path), or the task message for a pi-subagents child. |
| **Lane** | One bounded unit of autonomous work: a separate `pi` process spawned by tillr (historically by the deleted board drain) for one item, or, by analogy in Section 3, a lead's dispatched worker. |
| **Mode** | A run's operating context — drained lane, lead-orchestrated, plain interactive — independent of the agent's role and the repository. |
| **Materialization** | The launch-time rendering of pack contributions (briefing, `files`) into files inside an agent home, where standing processes then read them. |
| **Ambient extension** | A pack-contributed extension installed into every agent home and auto-loaded by every session of that agent, regardless of mode. |
| **Self-gate** | An ambient extension's runtime check of its own activation condition (here, `BOARD_DRAIN_*` env) before registering behavior; reaching for it is currently each extension's own responsibility. |
| **Agent definition** | A pi-subagents role declaration (prompt, `tools`, `model`, `thinking`, inheritance flags) applied to a child at dispatch. |
| **Hard knob / hard limit** | A constraint enforced by code or config (argv flags, tool fences, thinking ceilings, drain caps), as opposed to a prose rule, which only instructs. |
| **Loop owner** | The seat that runs a retry/repair loop and counts its cycles — the lane itself on the process path, the lead on the subagent path. Loop rules bind the loop owner. |