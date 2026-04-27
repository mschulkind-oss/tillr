# 30. Rui — The Conductor Pattern

**Context:** Rui is a solo developer who used Claude Code for six months
on a side project before the context bloat caught up with him. Threads
got long. Decisions got buried. Clearing the chat meant losing
everything Claude had learned about the project. Trying to do
everything in one foreground agent — research, design, implementation,
review — meant every task polluted every other.

He'd already built a workflow on a previous project: a *conductor*
agent that orchestrates, plus *persona* sub-agents that own their
context files. He's bringing that pattern to tillr.

This story is the post-reset architectural foundation for everything
that follows. Subsequent stages compose on top.

**What happens today (without the pattern):**

Rui's typical Claude Code session:

- Starts fresh. Asks Claude to do five things — research a library,
  fix a bug, write a feature, review another agent's PR, refactor a
  module.
- Claude does the work but mixes contexts. The library-research
  prompt is still in scope when implementation starts; Claude
  over-applies the research to unrelated code.
- After 4–5 hours, Claude is forgetting things he told it earlier.
  The session has become unusable.
- He clears the chat to escape the bloat — and now every project
  decision Claude knew is gone. He starts re-explaining the codebase.

The flat single-agent model doesn't survive contact with a multi-week
project.

**What happens with the conductor pattern:**

1. **Personas are defined once.** Rui (or tillr's defaults) creates
   `.claude/agents/<name>.md` files for each persona role. MVP
   personas: `implementer`, `researcher`, `reviewer`. More can be
   added later.

2. **Each persona owns a context file** in swarf:

   ```
   swarf/
     conductor.md
     agents/
       implementer/context.md
       researcher/context.md
       reviewer/context.md
     retros/
       2026-04-27T15-30.md
   ```

   Format: append-only markdown, sectioned by timestamp. Easy to grep,
   easy to compact, easy for humans to read.

3. **The conductor is the foregrounded Claude session.** Rui talks to
   it. The conductor's job is project management — *not* product
   management. Product decisions stay with Rui. The conductor:
   - Reads task state (`tillr feature list --persona implementer`)
   - Dispatches sub-agents via Claude's Task tool
   - Files new tasks (`tillr feature add --persona researcher "..."`)
   - Writes its own state to `swarf/conductor.md` after every
     significant action

4. **Dispatch is via Task tool, not CLI-spawned Claude.** When the
   conductor decides to research something, it invokes the
   `researcher` sub-agent with:
   - The researcher's *current* context (read from
     `swarf/agents/researcher/context.md`)
   - The specific task
   - Tool restrictions (researcher gets read-only + web access; no
     Edit/Write to project code)

   The sub-agent is a child of the conductor's session. No separate
   Claude process. No CLI command to start it.

5. **Persona lifecycle: load → work → persist → die.**

   ```
   [conductor]
     → Task(subagent_type='researcher', prompt='research X')
       → researcher reads its context file (loaded into prompt)
       → researcher does the work (web fetch, analyze, decide)
       → researcher calls `tillr persona append researcher "..."`
       → researcher returns summary to conductor
       → researcher process ends
   [conductor receives summary, decides next step]
   ```

   The persona has zero in-memory state between invocations. Its
   "memory" is entirely the context file. Each invocation is a fresh
   spawn. This is the load-do-persist-die pattern.

6. **Compaction.** When `swarf/agents/<persona>/context.md` exceeds
   ~20k words (configurable), tillr queues a compact task. Either the
   persona itself runs the compact, or a dedicated `compactor` persona
   does it. Compact = summarize entries older than N days into
   bullets; keep recent entries verbatim. The file shrinks back to
   reasonable size, important learnings preserved.

7. **Conductor reload.** When the foreground chat gets long or
   confusing, Rui runs `/clear` then a `conductor` skill that:
   - Reads `swarf/conductor.md` (current state, recent actions)
   - Reads `tillr persona list` (active personas + queue depths)
   - Reads `tillr feature list --status active`
   - Synthesizes: "I'm the conductor. Currently 3 features queued for
     implementer, 1 for researcher (waiting on Rui's input on
     approach). Last session ended at X working on Y. Ready."

   Total reload: <30 seconds. Conductor's curated state survives the
   chat clear because it's been written to disk continuously.

8. **Max parallelism.** `tillr config max-parallelism 3` caps
   concurrent background agents. The conductor reads this and only
   dispatches up to N simultaneous Task invocations per message
   batch. Above the cap, work waits in the queue.

9. **Retro.** After a session, Rui runs `tillr retro`. Tillr:
   - Finds the recent Claude session transcript (lives in
     `~/.claude/projects/<repo>/transcripts/`)
   - Analyzes for friction signals (retries, reverted approaches,
     conductor confusion, persona errors)
   - Writes findings to `swarf/retros/<timestamp>.md`
   - Optionally appends lessons learned to relevant persona contexts
     (e.g., "researcher: when API docs say X, the actual behavior is
     Y" — now in researcher's context for next time)

**Concrete walkthrough:**

```
[Rui]    Hey conductor, I want to add OAuth to our auth system.
         Can you research what we should use?

[Conductor]
         I'll dispatch the researcher.
         [calls Task(subagent_type='researcher',
                     prompt='Evaluate OAuth library options for
                             our Go HTTP server. Output: top 2 with
                             trade-offs.')]

[researcher (sub-agent)]
         [reads swarf/agents/researcher/context.md — has prior notes
          on the project's auth patterns from last week]
         [web fetches, evaluates 4 libraries, picks 2]
         [calls `tillr persona append researcher` with findings]
         [returns: "Recommend goth or coreos/go-oidc. Goth is more
                    abstracted but heavier; go-oidc is closer to the
                    metal. Trade-off doc appended to context."]

[Conductor]
         The researcher recommends goth or coreos/go-oidc. Want me
         to file an implementation feature for one, or talk through
         the trade-off first?

[Rui]    Talk through it.

[Conductor]
         [reads researcher's trade-off summary]
         goth wraps multiple providers; if we only need Google,
         go-oidc is simpler. We don't have multi-provider plans.
         Recommend go-oidc.

[Rui]    Go with that.

[Conductor]
         Filing feature.
         [calls `tillr feature add --persona implementer
                                 "Implement Google OAuth using
                                  coreos/go-oidc"`]
         Feature #4 queued for implementer. Anything else?
```

Implementer claims later (tomorrow, an hour from now, doesn't matter):

```
[Rui]    Conductor, run the implementer queue.

[Conductor]
         [calls `tillr persona claim implementer` → returns Feature #4]
         [calls Task(subagent_type='implementer',
                     prompt='Feature #4: Implement Google OAuth ...')]

[implementer (sub-agent)]
         [reads swarf/agents/implementer/context.md — has prior notes
          on the project's HTTP middleware patterns]
         [implements, tests, commits]
         [calls `tillr persona append implementer` with what was built]
         [calls `tillr comment 4 "Implemented per spec. PR ready."`]
         [returns to conductor with summary]
```

Both personas independently grew their context files. Neither saw
the other's. The conductor mediated, but didn't accumulate.

**Gap:** Persona context files can drift wrong, and once they're in
the file, every future invocation of that persona inherits the bad
data. Compaction can compress noise into something that looks
authoritative. Mitigation: retros surface "the implementer kept hitting
this issue" as a signal to manually edit the context file. Tillr should
make context files *easy to edit* — `tillr persona context implementer
--edit` opens in `$EDITOR`.

**Gap:** The "conductor reload" depends on the conductor having
written enough to its file before the clear. If Rui clears mid-task
without giving the conductor a chance to write, the conductor wakes
up confused. Default: the conductor skill prompts the conductor to
*write before responding* on every action — modest token overhead,
high reliability gain.

**Gap:** Multi-developer swarf sharing isn't designed. Two developers
on the same project means two copies of `swarf/`. The swarf tool
syncs each developer's machines, but the *team's* context files
diverge. Either we accept per-dev divergence (each developer has
their own conductor + persona memory) or we add a team-shared layer.
MVP defers; revisit after solo workflow is proven.

**Gap:** Compaction is lossy. Whatever heuristic we use will
sometimes drop something important. Mitigation: compaction writes a
backup to `swarf/agents/<persona>/context.md.compact-<date>` before
overwriting; manual recovery is possible. Need to validate the
heuristic over real usage.

**What would trip Rui up:**

- **Conductor context file drift.** If the conductor writes too
  little, reload is unreliable. If too much, the file becomes
  expensive to load. Calibration: write after every significant
  action (dispatch, file creation, decision), not every message.

- **Persona over-eagerness.** A persona that writes too aggressively
  to its context file fills up fast. Per-persona "what to record"
  guidance in the agent prompt matters.

- **Cross-persona pollution.** If Rui asks the implementer to
  "research a library," it'll do the research but in the wrong
  context (implementer's). Conductor should detect and dispatch to
  the right persona. Mitigation: persona prompts are specific about
  scope ("if you need to research, ask the conductor to dispatch the
  researcher").

- **Retro fatigue.** If retros file too many recommendations, Rui
  ignores them. Same dynamic as story 19. Significance threshold +
  user-actionable items only.

**What makes this work:**

- **The substrate is files, not in-memory state.** Everything that
  matters lives on disk in human-readable markdown. Survives session
  clears, machine moves, sync via swarf.

- **Each agent owns its memory.** No shared global state, no central
  registry of facts. Each persona's context is *its* expertise.

- **The conductor stays light.** It dispatches, it doesn't do. Its
  context file records intent and progress, not detail. When it's
  too cluttered, clear and reload.

- **The lifecycle is explicit.** Load, work, persist, die. No
  hidden state, no leaks between invocations.

- **Tillr is bookkeeping.** It stores files, tracks the queue,
  triggers compaction, runs retros. It does *not* dispatch — the
  conductor does that via Task tool.

**Position in roadmap:**

**Stage 0 (foundational).** This is the architecture every other
stage builds on. Stage 0 expands from "platform adapter" (the
original framing) to "conductor + persona infrastructure":

- swarf layout
- Persona context CLI surface
- `.claude/agents/<persona>/` files for the starter set
- Conductor skill (`~/.claude/skills/conductor/`)
- Compaction trigger
- Retro command
- Max parallelism config

See [mvp.md](../mvp.md) for the concrete shipping plan.

This story supersedes [story 29 (Anders — platform adapter)](./29-anders-platform-adapter.md)
for MVP scope. Multi-platform abstraction (Anders's original concern)
is deferred to a future stage when Copilot/Cursor parity becomes
necessary.

---

« [All stories](./README.md) · [MVP](../mvp.md) · [Consulting-firm overview](../README.md)
