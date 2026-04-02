# From Idea to Done in 47 Minutes

## A Narrative Walkthrough of the Tillr App

---

## Introduction

Imagine a world where you could casually mention an idea over coffee, and 47 minutes later it's been researched, specified, implemented, tested, and shipped — with a full audit trail documenting every step. That's the world Tillr creates.

**Tillr** is a project management tool built for the age of agentic development. It sits between human product owners and AI agents, providing the structure, guardrails, and visibility needed to let autonomous agents do great work while keeping humans firmly in control. Think of it as the operating system for human-AI collaboration: humans make decisions, agents do the work, and Tillr makes sure nothing falls through the cracks.

This isn't a tool for micro-managing agents. It's a tool for *trusting* them — because you can see exactly what they're doing, exactly where every feature stands, and exactly when your attention is needed. The QA gate ensures no feature reaches "done" without a human saying "yes." Everything else? The agents handle it.

Let's follow a feature through its entire tillr, from a spark of inspiration to a shipped product improvement. Along the way, you'll meet every page in the app and see how they work together.

---

## Cast of Characters

**Sarah** — Product Manager at a small SaaS startup. She manages priorities, reviews specs, and approves features for release. She's the human in the loop.

**DevBot** — An AI agent that picks up work items, writes code, runs tests, and reports progress. It doesn't get tired, it doesn't get distracted, and it follows the spec.

**The Tillr App** — The web dashboard Sarah keeps open in a browser tab. It shows her everything she needs to know and pushes updates to her in real time via WebSocket — no refresh needed.

---

## Chapter 1: The Spark

### 💡 The Ideas Page

It's Tuesday morning. Sarah is reading through customer feedback over coffee when she notices a pattern: three separate users have asked for a dark mode toggle. "We should just do this," she thinks.

She switches to her browser tab where Tillr is already open. In the sidebar, she clicks **💡 Ideas** to open the Idea Queue. The page header reads:

> **💡 Idea Queue**
> *8 idea(s) · 1 pending · 1 ready for review*

Below the header, there's a bright **"+ Submit Idea"** button. Sarah clicks it.

A modal slides into view — a clean overlay on a semi-transparent backdrop. The form is simple:

- **Title**: She types *"Dark mode toggle"*
- **Description**: She writes a quick note in the markdown-supported textarea: *"Multiple users requesting dark mode. Should include a toggle in settings and respect system preference (prefers-color-scheme)."*
- **Type**: She leaves it as "Feature" (the default)
- **Auto-implement**: She checks this box — she's confident this is a good idea and wants to skip straight to implementation after spec review

She clicks **Submit**. The modal closes. A new card appears at the top of the **⏳ Pending** section:

```
✨ Dark mode toggle                        🤖 auto
[planning] · just now
Multiple users requesting dark mode. Should include a toggle in
settings and respect system preference (prefers-color-scheme).
by human
```

The ✨ emoji marks it as a feature idea. The 🤖 auto badge means it's flagged for automatic implementation after approval. Total time: about 10 seconds.

Sarah takes another sip of coffee. Her work here is done — for now.

---

## Chapter 2: The Queue

### 💡 The Ideas Page (continued)

Behind the scenes, an AI spec agent is watching the idea queue. It polls `GET /api/ideas?status=pending` and finds Sarah's new idea at the top of the list.

The idea card briefly flickers as the page updates via WebSocket. The card moves from **⏳ Pending** to **⚙️ Processing** — the spec agent has claimed it. Sarah glances at her screen and sees:

```
⚙️ Processing (1)
┌──────────────────────────────────────────────┐
│ ✨ Dark mode toggle               🤖 auto    │
│ [planning] · 3m ago                          │
│ Multiple users requesting dark mode...       │
│ by human                                     │
└──────────────────────────────────────────────┘
```

A few minutes later, the agent submits its work. The card moves again, this time to **✅ Spec Ready**. The page subtitle updates: *"9 idea(s) · 0 pending · 1 ready for review"*

Now the card looks different. It has a collapsible spec section and action buttons:

```
✅ Spec Ready (1)
┌──────────────────────────────────────────────┐
│ ✨ Dark mode toggle               🤖 auto    │
│ [human-qa] · 8m ago                          │
│ Multiple users requesting dark mode...       │
│ by human                                     │
│                                              │
│ ▸ View Spec                                  │
│                                              │
│ [✅ Approve]  [❌ Reject]                     │
└──────────────────────────────────────────────┘
```

Sarah clicks **▸ View Spec** to expand the generated specification. The details section unfolds, revealing a beautifully structured markdown document:

> **## Dark Mode Toggle**
>
> **Acceptance Criteria:**
> 1. A toggle switch in the Settings page allows users to switch between light and dark mode
> 2. The toggle state persists across sessions (stored in localStorage)
> 3. CSS custom properties (`--bg`, `--text`, `--border`, etc.) drive all colors
> 4. The app respects `prefers-color-scheme` media query on first visit
> 5. Transition between modes is smooth (0.3s ease on background-color and color)
> 6. All status badges maintain WCAG AA contrast in both themes
> 7. Charts and canvas elements adapt to the active color scheme

Sarah reads through it. The spec agent did a thorough job — it even thought about accessibility contrast ratios. But Sarah wants one more thing. She makes a mental note: she'll add it after approving.

She clicks **✅ Approve**.

The idea moves to the **👍 Approved** section (collapsed by default — out of sight, out of mind). Because the idea was flagged with 🤖 auto-implement, a new feature is automatically created in the system with the generated spec. The page refreshes, and right there on the card she now sees:

```
→ Feature: dark-mode-toggle
```

A clickable link that takes her straight to the feature. A feature that didn't exist 10 minutes ago.

---

## Chapter 3: The Plan

### 📋 The Roadmap Page

Sarah clicks **📋 Roadmap** in the sidebar to check on the bigger picture. The Roadmap page displays all strategic items as a prioritized list — each one showing a title, priority indicator, status, effort estimate, and category.

Her new dark mode feature is linked to the roadmap item **"UI Polish"**, which she'd created last week. She can see it on the page:

```
 2. UI Polish                           ◐ accepted    [M]
    Improve visual design, add dark mode, refine spacing
    Category: design · Priority: high
```

The numbered index is colored orange — indicating high priority. The ◐ icon means "accepted" (in progress). The `[M]` badge is the effort estimate: medium.

Sarah wants to bump this up. Multiple users have asked for dark mode, and the "UI Polish" item was sitting at position 2. She grabs the drag handle and pulls it up to the top of the list. The item smoothly repositions, and a `POST /api/roadmap/reorder` request fires in the background. Done — "UI Polish" is now the team's top priority.

At the bottom of the item, she can see linked features. Dark mode toggle is one of three features under this roadmap item, alongside "Refined spacing system" and "Consistent button styles." The milestone tag reads **"v2.0 Polish"** with a progress indicator.

---

## Chapter 4: The Build

### 🤖 The Agents Dashboard

Sarah clicks **🤖 Agents** in the sidebar. This is her mission control — the real-time view of what AI agents are doing right now.

At the top, the stats bar gives her the pulse:

```
┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐
│ Total: 7 │ │ Active: 1│ │ Done: 5  │ │ Rate: 83%│
│ Sessions │ │          │ │          │ │ Success  │
└──────────┘ └──────────┘ └──────────┘ └──────────┘
```

One agent is active. Sarah looks at the active agent card:

```
┌──────────────────────────────────────────────────────┐
│  DevBot                                              │
│  Feature: dark-mode-toggle                           │
│  Implementing dark mode with CSS custom properties   │
│                                                      │
│  ████████████████████░░░░░░░░░░░░  60%               │
│  Phase: [css-variables]    ETA: 2:15 PM              │
│                                                      │
│  Recent Updates                                      │
│  ┌────────────────────────────────────────────┐      │
│  │ [css-variables] · 2 min ago                │      │
│  │ Defined 24 CSS custom properties for both  │      │
│  │ light and dark themes. All status badges   │      │
│  │ verified for WCAG AA contrast.             │      │
│  ├────────────────────────────────────────────┤      │
│  │ [research] · 12 min ago                    │      │
│  │ Analyzed existing color usage across all   │      │
│  │ 4 CSS files. Found 47 hardcoded colors    │      │
│  │ to convert to custom properties.           │      │
│  ├────────────────────────────────────────────┤      │
│  │ [scaffolding] · 18 min ago                 │      │
│  │ Created toggle component in settings.      │      │
│  │ localStorage persistence working.          │      │
│  └────────────────────────────────────────────┘      │
│                                                      │
└──────────────────────────────────────────────────────┘
```

The progress bar is a smooth gradient — it animated from 45% to 60% when the latest status update arrived (a 0.8-second cubic-bezier transition, barely noticeable but satisfying). The phase badge `[css-variables]` is blue. The **Feature: dark-mode-toggle** text is a clickable link — Sarah could click it to jump straight to the feature detail page.

Sarah watches for a moment. The status updates are streaming in every few minutes as DevBot posts heartbeats via the API. Each update includes rendered markdown, so she can read structured findings, code snippets, and progress notes right in the timeline.

She doesn't need to do anything. The agent is making steady progress. She switches to another tab.

Twenty minutes later, she gets a notification. The agent has completed its work. The WebSocket pushes an update, and the Agents page re-renders automatically. DevBot's card has vanished from the active section and slid into the collapsible **"Completed & Failed Sessions"** section at the bottom:

```
▸ Completed & Failed Sessions (6)
```

She expands it and sees DevBot's session with a green ✅ icon, showing "completed" with the timestamp "just now."

---

## Chapter 5: The Features Page

### 📦 Features

Before heading to QA, Sarah clicks **📦 Features** in the sidebar to check on the feature's journey through the state machine. The Features page shows an interactive table with status-colored rows.

The page header reads:

> **Features**
> *14 features tracked*
> *5 done · 3 implementing · 1 blocked*

She spots the dark mode feature immediately — its row has a yellow-orange left border, indicating it's in `human-qa` status. The state machine transitioned it automatically when DevBot completed the work. That's the engine at work: `implementing → human-qa` happens without anyone lifting a finger.

She clicks the row to expand the detail panel. It unfolds below, spanning all six table columns:

```
ID:          dark-mode-toggle
Status:      [human-qa ▾]
Priority:    P2 High
Milestone:   v2.0 Polish
Description: Toggle between light and dark mode with system preference detection
Roadmap:     UI Polish (clickable link)
Created:     2025-07-15T09:23:41Z

── Spec ──────────────────────────────────────
1. A toggle switch in Settings page
2. Toggle state persists (localStorage)
3. CSS custom properties drive all colors
4. Respects prefers-color-scheme on first visit
5. Smooth 0.3s transition between modes
6. WCAG AA contrast maintained in both themes
7. Charts and canvas elements adapt

── Work Items & Cycles ───────────────────────
✓ research     — done    — "Analyzed 47 hardcoded colors..."
✓ develop      — done    — "Implemented dark mode toggle..."
✓ agent-qa     — done    — "All tests passing, contrast verified"
○ human-qa     — pending — awaiting review

── History ───────────────────────────────────
⊕ Feature created                           9:23 AM
▸ Cycle started: feature-implementation      9:24 AM
✔ Work completed: research                   9:31 AM
✔ Work completed: develop                    9:52 AM
✔ Work completed: agent-qa                   9:58 AM
→ Status: implementing → human-qa            9:58 AM
```

The full audit trail is right there. Every state transition, every completed work item, every timestamp. Sarah can see exactly what happened and when.

She notices the status dropdown — a styled `<select>` element with a ▾ arrow. She could change the status right here if she wanted. But she won't — she's going to use the proper QA workflow.

---

## Chapter 6: The Review

### ✅ The QA Page

Sarah clicks **✅ QA** in the sidebar. This is the gatekeeper — the page that enforces Tillr's most important rule: *no feature reaches "done" without explicit human approval.*

The page is divided into two columns. On the left: **Pending Review**. On the right: **Recently Reviewed**.

The summary bar at the top reads:

```
1 Pending    7 Approved    1 Rejected
```

There's one card waiting for her:

```
┌──────────────────────────────────────────────────────┐
│  Dark mode toggle                    [awaiting QA]   │
│                                                      │
│  P2 High                                             │
│  Toggle between light and dark mode with system      │
│  preference detection                                │
│                                                      │
│  🏷️ dark-mode-toggle  📌 v2.0 Polish                │
│  🕐 12m ago  🔄 0 prior reviews                      │
│                                                      │
│  ┌──────────────────────────────────────────────┐    │
│  │ Notes (optional for approval)                │    │
│  │                                              │    │
│  │                                              │    │
│  └──────────────────────────────────────────────┘    │
│                                                      │
│  [✓ Approve]                    [✗ Reject]           │
│                                                      │
└──────────────────────────────────────────────────────┘
```

Sarah opens the app in another browser tab to actually test the feature. She finds the dark mode toggle in Settings, clicks it — the entire interface smoothly transitions to dark mode. She checks the contrast on status badges. She opens the browser's dev tools and toggles `prefers-color-scheme: dark` — the app picks it up correctly on first visit. She switches back to light mode. The transition is buttery smooth, exactly 0.3 seconds.

Everything works. She switches back to the QA tab.

In the notes textarea, she types: *"Beautiful! Smooth transitions, excellent contrast. System preference detection works perfectly. Ship it."*

She clicks **✓ Approve**.

A confirmation modal appears:

```
┌─────────────────────────────────────────────┐
│  Approve Feature                            │
│                                             │
│  This will mark the feature as done and     │
│  complete the QA cycle.                     │
│                                             │
│  Feature: dark-mode-toggle                  │
│  Notes: Beautiful! Smooth transitions,      │
│  excellent contrast. System preference      │
│  detection works perfectly. Ship it.        │
│                                             │
│              [Cancel]  [✓ Approve]          │
└─────────────────────────────────────────────┘
```

She clicks **✓ Approve** in the modal.

A green toast notification slides in: **✓ Feature approved**

Behind the scenes, three things happen simultaneously:
1. A `QAResult` record is created with `passed: true` and her notes
2. The feature transitions from `human-qa` to `done` (the only valid path through the state machine)
3. An event is logged: `feature.status_changed` from `human-qa` to `done`

The pending card vanishes. The "Recently Reviewed" column on the right updates:

```
Recently Reviewed
┌────────────────────────────────────────┐
│  ✓ dark-mode-toggle                   │
│    Approved · just now                 │
│    "Beautiful! Smooth transitions..."  │
└────────────────────────────────────────┘
```

The summary bar now reads: **0 Pending · 8 Approved · 1 Rejected**

---

## Chapter 7: The Ship

### 📊 Dashboard

Sarah clicks **📊 Dashboard** in the sidebar — the default landing page, and the most satisfying place to be right now.

The stats grid at the top reflects the change immediately:

```
📦 Total Features    ✅ Completed    🔨 In Progress    🔍 Awaiting QA    🔄 Active Cycles
      14                  6               3                  0                  0
```

Completed just ticked up from 5 to 6. Awaiting QA dropped from 1 to 0.

Below the stats, the **status bar** — a horizontal stacked bar showing feature distribution — has shifted. The green "Done" segment grew wider. The orange "Human QA" segment has disappeared entirely. If Sarah hovers over the green segment, a tooltip reads: *"Done: 6"*.

On the **Feature Board** — a kanban-style view — the "Dark mode toggle" card has moved from the "HUMAN QA" column to the "DONE" column. Done features appear with a subtle strikethrough on their name and reduced opacity — they're finished, archived in place. The "DONE" column header shows a count badge: **6**.

The **Milestones** section tells the real story:

```
v2.0 Polish                    [active]
████████████████░░░░░░░░░░░░░  56%
5/9 features
```

That progress bar just ticked up. Two of the three "UI Polish" features are now complete. One more to go before the milestone hits its next mark.

In the **Recent Activity** feed, the latest entries read:

```
✔ Feature approved: dark-mode-toggle           just now
→ Status changed: human-qa → done              just now
✔ Work completed: agent-qa                     35m ago
✔ Work completed: develop                      41m ago
```

Each event has an icon — ✔ for approvals and completions, → for status transitions. The feature ID badge next to each entry is clickable — Sarah could drill back into the feature detail at any point.

Sarah smiles. The feature she casually mentioned over coffee is now live, tested, and documented. Total elapsed time: 47 minutes.

---

## The State Machine: Why This Works

At the heart of Tillr is a state machine that governs how every feature moves through its journey. It's simple, strict, and deliberately designed to keep humans in control:

```
draft → planning → implementing → agent-qa → human-qa → done
                        ↕              ↕          ↕
                     blocked        blocked     blocked
```

The key insight is the **QA gate**. Look at the valid transitions:

| From | Can go to |
|------|-----------|
| `draft` | planning, implementing, blocked |
| `planning` | implementing, blocked |
| `implementing` | agent-qa, human-qa, blocked |
| `agent-qa` | human-qa, implementing, blocked |
| `human-qa` | **done**, implementing, blocked |
| `blocked` | draft, planning, implementing |
| `done` | implementing (reopen if needed) |

Notice what's *not* there: you cannot go from `implementing` to `done`. You cannot go from `agent-qa` to `done`. The *only* path to "done" passes through `human-qa`. Every single time. No shortcuts, no overrides.

This is what makes Tillr trustworthy for agentic development. Agents can research, build, and test with full autonomy. But the final "ship it" decision always belongs to a human.

---

## The Feedback Loop

What Sarah experienced is the core loop of Tillr, and it works the same whether the idea takes 47 minutes or 47 days:

```
    ┌─────────┐     ┌─────────┐     ┌─────────┐
    │  Ideas  │ ──→ │ Feature │ ──→ │  Agent  │
    │  Queue  │     │ Created │     │  Builds │
    └─────────┘     └─────────┘     └─────────┘
         ↑                               │
         │                               ↓
    ┌─────────┐     ┌─────────┐     ┌─────────┐
    │Rejection│ ←── │ Human   │ ←── │ Agent   │
    │(rework) │     │   QA    │     │   QA    │
    └─────────┘     └─────────┘     └─────────┘
                         │
                         ↓
                    ┌─────────┐
                    │  Done   │
                    └─────────┘
```

**Ideas flow in.** Humans submit raw thoughts — a sentence or two, nothing more. The barrier to entry is intentionally low.

**Specs flow out.** AI agents transform raw ideas into structured specifications with numbered acceptance criteria. The spec is the contract.

**Work gets done.** Agents pick up work items, execute through iteration cycles (research → develop → agent-qa → judge → human-qa), and report progress via status updates.

**Humans decide.** The QA page is where human judgment enters the loop. Approve, and the feature ships. Reject with notes, and it goes back for rework. Rejection notes become the agent's instructions for the next iteration — the 🔄 badge tracks how many rounds a feature has been through.

**Everything is visible.** The Dashboard shows the health of the whole project. The Features page shows where every feature stands. The Agents page shows what's happening right now. The History page shows what happened and when. Nothing is hidden, nothing is lost.

---

## Why This Matters

Traditional project management tools were built for humans managing humans. They assume someone will update a Jira ticket, someone will remember to write a test, someone will notice that a pull request has been sitting for three days.

Tillr is built for humans managing agents. The agents don't forget to update status — the state machine does it for them. The agents don't skip QA — the transition rules make it impossible. The agents don't go silent — heartbeats and status updates stream in real time.

And for the humans? The cognitive load drops dramatically. Sarah didn't write a spec. She didn't create a ticket. She didn't assign a developer. She didn't check in on progress. She didn't merge a PR. She typed one sentence, reviewed one spec, and approved one feature. Three decisions. Everything else was handled.

That's the promise of Tillr: **you make the decisions that matter, and the machines handle everything else.** The tool makes sure nothing falls through the cracks, nothing ships without approval, and everything leaves a trail.

Sarah finishes her coffee. The dark mode toggle is live. She opens the Idea Queue again — there are two more ideas waiting. She smiles and clicks **"+ Submit Idea."**

The loop continues.
