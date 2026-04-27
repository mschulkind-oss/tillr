# MVP — The Conductor + Persona Workflow, Working

The smallest set of capabilities that lets a real user (Rui — see
[story 30](./stories/30-rui-conductor-pattern.md)) start using tillr
for actual project management work. Target build time: 1–2 weeks of
focused work, after which the workflow is dogfoodable.

**This MVP supersedes the original Stage 0/1 plan in
[roadmap.md](./roadmap.md).** The conductor + persona architecture
(established 2026-04-27 from prior project learnings) reshapes what
"foundational" means: it's no longer just comments + adapter, it's
the lifecycle, context files, retro, and TUI.

## Goal

Within 1–2 weeks of build, Rui can:

1. Open Claude Code in the tillr-managed worktree.
2. Talk to the **conductor** (foreground Claude session) as a project
   manager — not product manager. Rui owns product decisions.
3. The conductor **dispatches typed tasks to persona sub-agents** via
   Claude's Task tool: implementer, researcher, reviewer (starter
   three).
4. Each persona maintains its own context file in `swarf/agents/<name>/`,
   appending after each invocation.
5. Rui can **clear the chat anytime**, run a `conductor` skill, and
   the conductor re-hydrates from `swarf/conductor.md` in <30s.
6. Rui can run **`tillr retro`** after a session to harvest lessons
   from the Claude session transcript.
7. Rui can inspect everything — features, comments, persona contexts,
   retros — through CLI, **TUI**, or web (the existing minimal web
   stays).
8. Auto-compaction triggers when persona context files cross ~20k
   words, keeping context lean.

## What gets built (in order)

### Phase 1 — Core mechanics (3–5 days)

**Backend:**
- `internal/persona` package — read/append/compact context files
  under `swarf/agents/<name>/`.
- `internal/cli/persona.go`:
  - `tillr persona list` — names, sizes, last-update timestamps
  - `tillr persona show <name>` — definition + context summary
  - `tillr persona context <name>` — print full context file
  - `tillr persona append <name> "..."` — append to context (used by
    sub-agents at end of run)
  - `tillr persona claim <name>` — return next typed task as JSON
- `internal/cli/feature.go` extension:
  - `tillr feature add --persona <name> "Title"` — type-tagged task
  - `tillr feature list --persona <name>` — filter by persona
- `internal/cli/config.go` (new):
  - `tillr config max-parallelism <N>` — store cap
  - `tillr config show` — print current config

**Repo files:**
- `.claude/agents/implementer.md`, `.claude/agents/researcher.md`,
  `.claude/agents/reviewer.md` — persona sub-agent definitions with
  prompts that reference `tillr persona append`
- `~/.claude/skills/conductor/SKILL.md` — the rehydrate skill (lives
  outside repo since it's per-user; provide a doc on how to install)

**swarf seed:**
- `swarf/conductor.md` — conductor's own context, starts empty
- `swarf/agents/{implementer,researcher,reviewer}/context.md` — empty

**End-to-end demo at end of Phase 1:**
- Conductor receives "research X." Calls `Task(subagent_type='researcher', ...)`.
- Researcher reads its context (empty first run), does work, calls
  `tillr persona append researcher "..."`.
- Returns to conductor.
- Conductor files an implementation feature: `tillr feature add --persona implementer "..."`.
- Implementer claims later, dispatched the same way, appends to its context.

### Phase 2 — Lifecycle + retro (2–3 days)

**Compaction:**
- Manual: `tillr persona compact <name>` — summarize entries older
  than 7 days into bullets, keep recent verbatim, write backup to
  `swarf/agents/<name>/context.md.pre-compact-<date>`.
- Auto trigger: when a persona's context file exceeds threshold
  (default 20k words, configurable), tillr emits a "compact needed"
  marker that the conductor picks up on next dispatch and queues as
  a task.

**Retro:**
- `tillr retro` — basic version:
  - Find the most recent Claude session transcript (configurable
    location; default `~/.claude/projects/<repo>/transcripts/`)
  - Parse for friction signals (tool retries, reverted edits,
    long deliberation messages, persona errors)
  - Write findings to `swarf/retros/<timestamp>.md`
  - Optionally append targeted lessons to specific persona contexts
    (with `--apply` flag)

**End-to-end demo at end of Phase 2:**
- After 30+ persona invocations producing >20k words in implementer's
  file, run `tillr persona compact implementer`. File shrinks; backup
  preserved.
- After a 1-hour conductor session, run `tillr retro`. Get a markdown
  file with 2–4 actionable recommendations.

### Phase 3 — Inspection surfaces (3–5 days)

**TUI** (Bubble Tea):
- `tillr tui` — fullscreen three-pane TUI.
  - Sidebar: Features / Personas / Retros tabs.
  - Main view: detail of selected item.
  - Command bar: keyboard hints.
- Read-only first: list/show/navigate.
- Light mutations: `n` new feature, `c` comment, `e` edit (in $EDITOR).
- WebSocket-driven live updates (subscribes to `/ws`; events get
  pushed when Stage 1 ships event production — until then, manual
  refresh `r` works).

**Web UI** (existing, minimal additions):
- Add a persona index page at `/personas` (list, click to view context).
- Add a retros page at `/retros`.
- Keep features as primary surface.
- No major redesign.

**End-to-end demo at end of Phase 3:**
- Open `tillr tui` in one terminal pane, conductor chat in another.
- Inspect a persona's context, see live updates as the conductor
  dispatches work to it.
- Run `tillr retro` — recommendations appear in TUI's Retros tab
  immediately.

### Total MVP effort: 8–13 days of focused work.

## Out of scope (NOT in MVP)

These are explicit deferrals. They build *on* the MVP infrastructure;
none are blocked, just pulled out of MVP for time:

| Capability | Story / Layer | Why deferred |
|------------|---------------|--------------|
| Cycle templates with multi-step routing | story 13, Layer 9 | Persona-typed queue is enough for MVP. Cycle templates add structure later. |
| Style guide enforcer + style-rule PRs | story 25, Layer 9b | Reviewer persona exists; rule-based enforcement is a Phase 4+. |
| Async reviewer↔implementer cycle states | story 27, Layer 4b | Sequential dispatch via conductor handles MVP. |
| Knowledge synthesis from review history | story 8, Layer 7 | Persona context files *are* the curated knowledge for MVP. |
| Context graph assembly (cross-feature edges) | story 7, Layer 6 | Per-persona context replaces this for MVP. |
| Hierarchy / org-level director | story 26, Layer 11 | Single-project, single-conductor MVP. |
| Multi-platform agent adapter (Copilot, etc.) | story 29 | Claude only for MVP. |
| Estimation by analogy | story 17, Layer 10 | Need data first. |
| Decisions / philosophies first-class tracking | Layer 5/8 | Persona contexts hold these implicitly for MVP. |
| Tunable oversight via questionnaires | story 24, Layer 2b | Cycle templates are deferred; questionnaires depend on them. |

## How to know we shipped

The MVP is "done" when Rui can demonstrate this loop without
hand-holding the system:

1. Open Claude Code, talk to the conductor about a real task.
2. Conductor dispatches a researcher; researcher returns useful info
   that's persisted.
3. Conductor files an implementation feature.
4. (Later session) implementer claims and completes the feature;
   commits.
5. Rui clears the chat, runs `conductor` skill, conductor reports
   accurate state in <30s.
6. After 2 weeks of use, persona context files have nontrivial
   useful content (>5k words each) and have been compacted at least
   once.
7. `tillr retro` produces actionable recommendations that Rui acts
   on.
8. TUI shows everything navigable from the terminal.

## What the MVP enables next

Once MVP is dogfooded for 2-3 weeks and tuned, the rest of the
[roadmap](./roadmap.md) unblocks naturally:

- **Stage 2 (async dialogue):** Becomes "two personas talking via
  cross-feature comments" — substrate is already in place.
- **Stage 3 (style enforcer):** Just adds a `style-enforcer` persona
  + style-rule artifacts. The dispatch model is identical.
- **Stage 4 (memory & search):** Persona context files are searchable
  by definition; add `tillr search` over them + `philosophies` table.
- **Stage 5 (context graph):** Becomes optional — per-persona context
  may be enough; revisit if cross-persona discovery matters.

The MVP is the *foundation* the post-reset architecture grows from.
Everything else compose on top.

## Things to validate during MVP build

These are calibration questions only real usage answers:

- Is the 20k-word compaction threshold right? (might be too high or
  too low)
- Are 3 personas (implementer, researcher, reviewer) enough, or do we
  need a 4th from day 1?
- Does `tillr retro` produce useful output, or is it noise?
- Is the conductor skill reload reliable, or does it lose context
  often?
- Is the TUI worth the build cost, or do CLI + web cover everything?

Answer these by *using* the system; revise from there.

## Related docs

- [roadmap.md](./roadmap.md) — the full long-term plan; MVP is its
  Stage 0 expansion
- [story 30](./stories/30-rui-conductor-pattern.md) — narrative for
  the conductor + persona pattern
- [story 31](./stories/31-yael-tui-primary-interface.md) — narrative
  for the TUI as primary interface
- [docs/reset.md](../reset.md) — what's preserved at archive/pre-reset
- [open-questions.md](./open-questions.md) — calibration questions for
  MVP build

---

« [Consulting-firm overview](./README.md) · [Roadmap](./roadmap.md) · [All stories](./stories/README.md)
