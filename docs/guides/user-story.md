# The Lifecycle Story: From Chaos to Clarity

*A story about Maya, a tech lead who stopped drowning in agent output and started shipping.*

---

## The Problem

Maya stares at her terminal, coffee going cold.

Her team is building **PawTrack** — a mobile app that helps pet owners track vet visits, medications, and feeding schedules. It's a good idea. The codebase is solid. They've got three AI agents churning through tasks around the clock.

And yet — things are falling apart.

The notification service has been "almost done" for two weeks. An agent rewrote the database schema last Tuesday and nobody noticed until staging broke on Thursday. There are four pull requests open for features that overlap in ways nobody can untangle. Her product manager keeps asking "when will search be ready?" and Maya honestly doesn't know.

She opens Slack. Seventeen unread threads. An agent finished the payment integration at 2 AM. Did anyone review it? She scrolls. No. It's been sitting there, unreviewed, for three days.

Maya knows the agents are productive. That's not the problem. The problem is that *nobody is steering the ship*. Work happens, but it happens in the dark. Features get built but never reviewed. Priorities shift but agents keep grinding on the old ones. There's no pipeline, no quality gate, no single place to look and understand: *where are we?*

She takes a sip of cold coffee and makes a face.

There has to be a better way.

---

## Day 1: Setting Up

Maya finds Lifecycle on a Friday afternoon. The pitch is simple: *structured iteration cycles for agentic development, with humans in the loop where it matters.* She decides to give it ten minutes.

```bash
cd ~/projects/pawtrack
lifecycle init pawtrack
```

```
✓ Created project "pawtrack"
✓ Database: .lifecycle.db
✓ Run 'lifecycle status' to see your project
```

Ten seconds. Okay, she's interested.

She starts by adding the features her team is actually working on. Not everything — just the stuff that matters right now:

```bash
lifecycle milestone add "v1.0 Launch" --description "Core features for App Store submission"

lifecycle feature add "Vet Visit Tracker" \
  --description "Log and view past vet visits with reminders for upcoming ones" \
  --spec "1. CRUD for vet visits with date, vet name, notes
2. Push notification reminders 7 days and 1 day before
3. Attach photos of documents/receipts
4. Calendar view of upcoming visits" \
  --milestone v1.0-launch \
  --priority high

lifecycle feature add "Medication Reminders" \
  --description "Daily medication tracking with smart reminders" \
  --spec "1. Add medications with dosage and schedule
2. Morning/evening reminder notifications
3. Completion tracking with streak counter
4. Refill reminders based on supply count" \
  --milestone v1.0-launch \
  --priority critical

lifecycle feature add "Pet Profile Pages" \
  --description "Rich profile for each pet with photo, breed, weight history" \
  --spec "1. Photo upload and cropping
2. Weight tracking with chart
3. Breed-specific health tips
4. Share profile via link" \
  --milestone v1.0-launch \
  --priority medium
```

Each feature has a spec — numbered acceptance criteria that tell agents (and humans) exactly what "done" means. No ambiguity. No "I thought you meant..."

She checks her work:

```bash
lifecycle status
```

```
Project: pawtrack
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Features by Status:
  draft          3  ███████████████████████████████████
  implementing   0
  agent-qa       0
  human-qa       0
  done           0

Milestones:
  v1.0 Launch    0/3 features complete  [··········] 0%

Active Agents:   0
```

Three features, all in draft, zero percent done. It's honest. Maya appreciates honest.

---

## The Dashboard

Monday morning. Maya starts the web viewer:

```bash
lifecycle serve
```

```
✓ Serving on http://localhost:3847
✓ Watching .lifecycle.db for changes
✓ WebSocket ready for live updates
```

She opens her browser and her eyebrows go up.

The **Dashboard** is a single page that answers every question her product manager has ever asked. At the top: feature cards grouped by status in a kanban-style layout — draft, implementing, agent-qa, human-qa, done. Right now everything is in the "draft" column, but she can already see how features will flow left to right as work progresses.

Below that, a **milestone progress bar** for "v1.0 Launch" — currently at 0%, a thin empty track waiting to fill. Next to it, a **priority breakdown** showing her three features color-coded: one critical (red), one high (amber), one medium (blue).

At the bottom, an **activity feed** — currently just three "feature created" entries. But Maya can imagine what this will look like in a week: a living stream of events, scores, approvals, agent completions. Everything that happened, in chronological order, no Slack archaeology required.

She bookmarks the page.

---

## Planning the Roadmap

Before unleashing agents, Maya wants a plan. She opens her terminal and builds a roadmap:

```bash
lifecycle roadmap add "Core Pet Management" \
  --description "Profiles, multi-pet support, photo management" \
  --priority critical --category core --effort m

lifecycle roadmap add "Health Tracking Suite" \
  --description "Vet visits, medications, vaccination records, weight trends" \
  --priority critical --category features --effort l

lifecycle roadmap add "Social & Sharing" \
  --description "Share pet profiles, community features, photo sharing" \
  --priority low --category growth --effort l

lifecycle roadmap add "Notification System" \
  --description "Push notifications, email digests, smart reminders" \
  --priority high --category infrastructure --effort m
```

She links features to roadmap items so there's a clear line from strategy to execution:

```bash
lifecycle feature edit vet-visit-tracker --roadmap-item health-tracking-suite
lifecycle feature edit medication-reminders --roadmap-item health-tracking-suite
lifecycle feature edit pet-profile-pages --roadmap-item core-pet-management
```

Back in the browser, she clicks over to the **Roadmap** page. It refreshed automatically — no reload needed, thanks to the WebSocket connection. The roadmap is grouped by priority: Critical items at top with red indicators, then High, Medium, Low. Each item shows its category tag, effort sizing (S/M/L/XL), and a count of linked features.

She runs `lifecycle roadmap show` in her terminal for a quick text view:

```
Priority   Title                   Category         Effort  Features
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
critical   Core Pet Management     core             M       1
critical   Health Tracking Suite   features         L       2
high       Notification System     infrastructure   M       0
low        Social & Sharing        growth           L       0
```

For the first time, Maya can see the shape of her project. Not in a Google Doc that's three weeks out of date — in a living system that updates as work happens.

---

## The Agent Workflow

Time to let the agents cook.

Maya kicks off a feature implementation cycle for the highest-priority feature:

```bash
lifecycle cycle start feature-impl medication-reminders
```

```
✓ Started cycle "feature-impl" for feature "Medication Reminders"
  Steps: research → implement → agent-qa → judge → human-qa
  Current step: research
```

An agent picks up the work. It calls `lifecycle next --json` and gets back a structured payload — the feature spec, the cycle state, what step it's on, everything it needs. No ambiguity, no "go figure it out."

```bash
# The agent runs this:
lifecycle next --json
```

```json
{
  "work_item": {
    "id": 1,
    "feature_id": "medication-reminders",
    "work_type": "research",
    "agent_prompt": "Research the requirements for Medication Reminders..."
  },
  "feature": {
    "name": "Medication Reminders",
    "spec": "1. Add medications with dosage and schedule\n2. Morning/evening reminder notifications\n3. Completion tracking with streak counter\n4. Refill reminders based on supply count"
  },
  "cycle": {
    "cycle_type": "feature-impl",
    "current_step": 0,
    "iteration": 1
  },
  "prior_results": []
}
```

The agent does its research — analyzes the codebase, checks notification APIs, reviews similar implementations — and reports back:

```bash
lifecycle done --result "Analyzed notification APIs. Recommend using local notifications for medication reminders with a background sync service for schedule updates. Existing Pet model needs a medications relationship. Estimated 3 new database tables."
```

The cycle advances automatically. Now the implement step is active. Another agent picks it up, writes the code, adds tests. When it finishes:

```bash
lifecycle done --result "Implemented medication CRUD with daily reminder scheduling. Added MedicationReminder model, NotificationService, and 47 unit tests. All tests passing."
```

Maya glances at the **Cycles** page in her browser. The pipeline visualization shows five steps as connected nodes: research (green check), implement (green check), agent-qa (pulsing blue — currently active), judge and human-qa (gray, waiting). Below it, a timeline of what each step produced.

The agent-qa step runs. The judge scores it:

```bash
lifecycle cycle score 8.5 --notes "Clean implementation. Good test coverage. Minor: streak counter resets on timezone change — edge case worth addressing."
```

Score threshold for feature-impl is 8.0. It passes. The feature advances to **human-qa**.

Maya's phone buzzes. She smiles.

---

## The QA Gate

This is where Lifecycle earns its name. The feature doesn't ship because an agent said it's good. It ships because *Maya* says it's good.

She checks the QA queue:

```bash
lifecycle qa pending
```

```
Features awaiting QA:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  medication-reminders   Medication Reminders   Score: 8.5   Cycle: feature-impl round 1
```

In the web viewer, the **QA** page shows the feature with full context: the spec, every cycle step's output, the judge's score and notes, the code changes. Maya doesn't have to go hunt for information — it's all right here.

She reviews the code. The judge was right — the streak counter has a timezone edge case. She rejects it with a note:

```bash
lifecycle qa reject medication-reminders \
  --notes "Streak counter breaks on timezone change. Add tests for users who travel. Also: the refill reminder calculation doesn't account for partial doses. Fix both, then resubmit."
```

The feature drops back to `implementing`. The cycle loops. An agent picks it up, sees Maya's feedback in the prior results, fixes both issues, and the cycle runs again: implement → agent-qa → judge → human-qa.

This time, the judge gives it a 9.2. Maya reviews. Everything looks clean. She approves:

```bash
lifecycle qa approve medication-reminders \
  --notes "Solid work. Timezone handling is thorough. Ship it."
```

```
✓ Feature "Medication Reminders" → done
  Completed in 2 iterations
```

Back on the Dashboard, the milestone progress bar for "v1.0 Launch" ticks up to 33%. The kanban board shows one card in the "done" column.

Maya takes a satisfied sip of (this time hot) coffee.

---

## Quick Feedback

Two weeks in. PawTrack has a beta group of fifty pet owners. Maya is at her desk when she notices something in the web viewer — Lifecycle has a small **⊕** button floating in the bottom-right corner of every page.

She clicks it. A minimal text input appears. She types:

```
Bug: medication reminder fires twice if app is force-closed and reopened
```

She hits Enter. Done. The feedback disappears into the system.

Later, she checks:

```bash
lifecycle idea list --type bug
```

```
ID    Type   Title                                                         Status
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
1     bug    medication reminder fires twice if app is force-closed...     submitted
```

She can also submit bugs through the CLI directly:

```bash
lifecycle bug report "Push notification sound doesn't respect phone silent mode" \
  --description "Users report that PawTrack reminders play sound even when phone is on silent. Affects iOS 17+."
```

When she's ready, she writes up a spec and promotes the bug into a full feature with its own cycle:

```bash
lifecycle idea spec 1 "1. Deduplicate pending notifications on app launch
2. Use notification ID to prevent duplicates at OS level
3. Add integration test for force-close scenario"

lifecycle idea approve 1
```

The idea becomes a feature, ready to enter a bug-triage cycle. The feedback loop — from user report to tracked, specced, assigned work — took about two minutes.

---

## Tracking Progress

It's the end of the sprint. Maya's product manager wants numbers. She opens the **Stats** page in the web viewer.

The **velocity chart** shows features completed per week — a line that's been climbing steadily. Week one: zero (setup). Week two: one feature. Week three: two features. The trend line points up and to the right.

A **cycle time distribution** chart breaks down how long each cycle phase takes on average. Research: 0.5 hours. Implement: 3.2 hours. Agent QA: 0.8 hours. Judge: instant. Human QA: 6.4 hours. Maya raises an eyebrow — the human QA step is the bottleneck. *She's* the bottleneck. She makes a mental note to review faster, or maybe delegate some QA to her co-lead.

She also checks from the terminal:

```bash
lifecycle roadmap stats
```

```
Roadmap Health
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Items: 4
  Critical:  2 (both in progress)
  High:      1 (not started)
  Low:       1 (not started)

Linked Features: 3/4 items have features
Coverage: 75%
```

The numbers tell a clear story: critical work is moving, lower-priority items are intentionally deferred, and most roadmap items are connected to actual features. No orphan plans. No forgotten ideas.

---

## Agents and Discussions

By week three, Maya has two agents working simultaneously. She checks the **Agents** page:

```bash
lifecycle agent list
```

The web viewer shows each agent session: what it's working on, when it last checked in, its completion rate. One agent is implementing the Vet Visit Tracker. The other is running a bug-triage cycle on the notification duplicate issue.

Then something interesting happens. The agent working on Vet Visit Tracker hits an architectural decision it can't make alone: should photo attachments be stored locally or uploaded to cloud storage? It opens a discussion:

```bash
lifecycle discuss new "RFC: Photo Storage Strategy for Vet Visit Attachments" \
  --feature vet-visit-tracker \
  --author agent-1
```

```bash
lifecycle discuss comment 1 \
  "Proposal A: Local storage with iCloud/Google Drive sync. Pros: works offline, no server costs. Cons: limited by device storage, no web access." \
  --author agent-1

lifecycle discuss comment 1 \
  "Proposal B: Cloud upload to S3 with local cache. Pros: accessible anywhere, unlimited storage. Cons: requires backend service, upload latency on poor connections." \
  --author agent-1
```

Maya sees the discussion in the web viewer's **Discussions** page. Two proposals, clearly laid out with trade-offs. She adds her own comment:

```bash
lifecycle discuss comment 1 \
  "Go with Proposal A for v1.0 — we don't have a backend yet and I don't want to add one for launch. We can migrate to cloud storage in v2.0." \
  --author maya
```

She resolves the discussion. The agent picks up the decision from the context and continues building — local storage it is.

No Slack thread. No meeting. No context lost. The decision is recorded permanently, linked to the feature, searchable forever.

---

## The Big Picture

It's been a month. Maya leans back and compares.

**Before Lifecycle:**
- Features lived in a Google Doc nobody updated
- Agent work was invisible until it showed up as a PR
- Nobody knew which features were in progress, blocked, or done
- QA was "whenever someone remembers to look at the PR"
- Bugs were reported in Slack and forgotten in Slack
- The product manager asked "where are we?" and got a shrug
- Agents did what they wanted; humans reacted after the fact

**After Lifecycle:**
- Every feature has a spec, a status, and a history
- The Dashboard shows project health in one glance — she checks it with morning coffee
- Agents work through structured cycles with defined steps and quality gates
- Nothing ships without human QA — the gate is real, not aspirational
- Bugs go from report to tracked feature in two minutes flat
- The roadmap is a living document that updates as work completes
- Discussions capture architectural decisions permanently
- The product manager opens the web viewer and gets his own answers

Maya's favorite part? She didn't have to change how her agents work. They still write code, run tests, do research. Lifecycle just gave them *structure* — a pipeline to move through, scores to hit, humans to answer to.

The agents are the engine. Lifecycle is the steering wheel.

She finishes her coffee (hot, this time) and runs one last command:

```bash
lifecycle status
```

```
Project: pawtrack
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Features by Status:
  draft          1  ████████
  implementing   1  ████████
  human-qa       1  ████████
  done           3  ████████████████████████

Milestones:
  v1.0 Launch    3/5 features complete  [██████····] 60%

Active Agents:   2
Recent Activity: 47 events this week
```

Sixty percent through v1.0, two agents actively working, forty-seven events captured and searchable. No surprises. No lost work. No cold coffee.

Well — maybe a little cold coffee. Some things never change.
