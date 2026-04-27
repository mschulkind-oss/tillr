# 31. Yael — TUI as the Primary Interface

**Context:** Yael lives in the terminal. She runs tmux with several
panes — one for the conductor (Claude Code chat), one for the editor,
one for tests, one for shell. Browser tabs are friction. She wants to
inspect features, persona contexts, retros, and the queue from the
terminal.

This story is the post-reset answer to "do we even need a web UI."
Tillr ships a TUI as a first-class surface. The web UI continues to
exist (already there from the reset; minimal incremental cost) but
isn't the primary interface.

**What happens today (with web only):**

Yael's existing flow:

- Browser tab open with `localhost:3847`. She's lazy about refreshing.
- To check the persona queue depth, she alt-tabs to the browser, waits
  for the SPA to load, navigates to a page that doesn't exist yet.
- She'd rather just stay in the terminal and use `tillr feature list`
  + `tillr persona context implementer | less` — but inspecting the
  full state means stitching together five CLI calls.

The CLI is great for one-shot writes; for browsing, it's friction.

**What happens with the TUI:**

```
$ tillr tui
```

Opens a fullscreen TUI in the current terminal. Three panes:

```
╭─ Tillr ────────────────────────────────────────────────╮
│ Features (5)        │ Feature #4: Implement Google     │
│   #1 Add login form │ OAuth                            │
│   #2 Fix the date   │                                  │
│   #3 Migration plan │ persona: implementer  status: claimed │
│ ▶ #4 Implement OAuth│ description: Use coreos/go-oidc...│
│   #5 Style fixes    │                                  │
│                     │ ── Comments (3) ─────────────    │
│ Personas (3)        │ [implementer 14:21]              │
│   implementer  3    │ Started. Reading existing auth   │
│   researcher   1    │ middleware...                    │
│   reviewer     0    │                                  │
│                     │ [Rui 14:25]                      │
│ Retros (2)          │ make sure to use our standard    │
│                                                        │
│ [n] new  [c] comment  [/] search  [q] quit             │
╰────────────────────────────────────────────────────────╯
```

Bindings (vim-flavored):

- `j`/`k` — navigate items
- `Enter` — open
- `n` — new feature in current pane (form)
- `c` — comment on current item
- `e` — edit (current persona context, current feature)
- `/` — search across features and contexts
- `r` — refresh (data is WebSocket-driven; manual refresh is rare)
- `q` — quit
- `?` — help overlay

**Persona context view:**

Selecting a persona in the sidebar opens its context file with
size/last-update metadata at the top:

```
implementer/context.md   8,432 words / 22kb   updated 14:21

## 2026-04-27 14:21 — Feature #4 (OAuth)
Started implementing Google OAuth using coreos/go-oidc.
Existing middleware pattern: ...

## 2026-04-25 11:03 — Feature #2 (Date bug)
The bug was in timezone handling. Fixed by ...

[scroll for more]
```

`e` opens the file in `$EDITOR`. Mutations go through the same
backend as web/CLI; no separate write path.

**Retro view:**

```
2026-04-27T15-30.md       3 recommendations

# Retrospective — session 14:00–15:30

## What worked
- Conductor dispatched researcher for the OAuth question; clean
  hand-off; no context bleed.

## What didn't
- Implementer was given Feature #2 (date bug) without enough context
  in the prompt; spent 4 tool calls re-deriving project conventions.
- Compaction of researcher's context lost a note on which library
  versions are deprecated.

## Recommendations
1. Implementer prompt: include "read swarf/agents/implementer/context.md
   first" preamble.
2. Compaction: preserve all entries with "deprecated" / "broken" /
   "blocker" keywords verbatim.
3. Add researcher persona note: cite sources by URL, not name only.
```

Rui can press `a` (apply) to take action on a recommendation —
appends the prompt change to the persona's instructions, or appends
the rule to compaction config.

**Gap:** Concurrent edits between TUI, web, and CLI. If Yael edits a
feature in TUI and Rui edits the same feature on the web, last write
wins. MVP accepts this; multi-user is deferred. Single-developer use
rarely produces concurrent edits.

**Gap:** Performance with large context files. A 50k-word context
file would lag the TUI render. Pagination + lazy load required for
files >10k words.

**Gap:** WebSocket reactivity. The TUI subscribes to the same `/ws`
endpoint as the web UI. If WebSocket isn't pushed events yet (Stage 1
hasn't shipped event production), the TUI is functionally polling.
Live updates need server-side event publish — same prerequisite as
the web UI's live refresh.

**Gap:** TUI library choice. Bubble Tea is the obvious Go choice
(strong ecosystem, well-maintained, MVU pattern). Open question:
should we use it or roll something simpler? Decided in Q47 below.

**What would trip Yael up:**

- **TUI feels primary, but mutation flows go through the conductor.**
  Yael might try to use the TUI to *drive* work — instead of asking
  the conductor. The TUI is for inspection + light edits, not for
  task creation. The conductor is the task interface.

- **Editor integration assumptions.** TUI shells out to `$EDITOR` for
  context file edits. Yael's $EDITOR is `nvim`; that should just
  work. Other users with no $EDITOR set need a fallback.

- **Color and Unicode in different terminals.** Bubble Tea has good
  defaults, but tmux + iTerm + zellij + alacritty all behave slightly
  differently. Test on at least 2.

**What makes this work:**

- **The TUI is just another client of the same backend API.** No new
  data path. CRUD through the existing HTTP API.

- **Keyboard-first, no mouse needed.** Matches Yael's flow.

- **Bubble Tea is well-trodden.** Plenty of reference TUIs to crib
  from (gh dash, k9s, etc.).

- **Mutations stay light.** TUI is for inspection. Heavy task
  creation goes through the conductor.

- **WebSocket is shared infrastructure.** Stage 1's event production
  benefits both web and TUI for free.

**Position in roadmap:**

**MVP Stage 0d** — ships in the initial milestone alongside swarf
layout, persona CLI, and conductor skill. Read-only first (features
list + persona contexts + retros), with basic mutation (`n`, `c`)
following.

The web UI's role narrows: prioritization view, sometimes-used inbox.
The TUI is the primary "tillr inspector" surface.

If the TUI proves to be enough alone, the web UI's scope shrinks
further — possibly down to "the embedded React app is gone, replaced
by a static dashboard or removed entirely." Open until we use the TUI
in real conditions.

---

« [All stories](./README.md) · [MVP](../mvp.md) · [Consulting-firm overview](../README.md)
