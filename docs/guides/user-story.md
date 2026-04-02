# The Tillr Story: From Chaos to Clarity

*A story about Maya, a tech lead who stopped drowning in agent output and started shipping.*

---

## The Problem

Maya stares at her terminal, coffee going cold.

Her team is building **PawTrack** — a mobile app that helps pet owners track vet visits, medications, and feeding schedules. They've got three AI agents churning through tasks around the clock. And yet — things are falling apart.

The notification service has been "almost done" for two weeks. An agent rewrote the database schema last Tuesday and nobody noticed until staging broke on Thursday. Her product manager keeps asking "when will search be ready?" and Maya honestly doesn't know.

She opens Slack. Seventeen unread threads. An agent finished the payment integration at 2 AM. Did anyone review it? No. It's been sitting there for three days.

The agents are productive. That's not the problem. The problem is that *nobody is steering the ship*. Work happens in the dark. Features get built but never reviewed. Priorities shift but agents keep grinding on old ones. There's no pipeline, no quality gate, no single place to understand: *where are we?*

There has to be a better way.

---

## Day 1: Setting Up

Maya finds Tillr on a Friday afternoon. The pitch: *structured iteration cycles for agentic development, with humans in the loop where it matters.* She gives it ten minutes.

```bash
cd ~/projects/pawtrack
tillr init pawtrack
```

```
✓ Created project "pawtrack"
✓ Database: .tillr.db
✓ Run 'tillr status' to see your project
```

Ten seconds. She's interested.

She adds the features her team is working on — not everything, just what matters now:

```bash
tillr milestone add "v1.0 Launch" --description "Core features for App Store submission"

tillr feature add "Vet Visit Tracker" \
  --description "Log and view past vet visits with reminders for upcoming ones" \
  --spec "1. CRUD for vet visits with date, vet name, notes
2. Push notification reminders 7 days and 1 day before
3. Attach photos of documents/receipts
4. Calendar view of upcoming visits" \
  --milestone v1.0-launch \
  --priority 8

tillr feature add "Medication Reminders" \
  --description "Daily medication tracking with smart reminders" \
  --spec "1. Add medications with dosage and schedule
2. Morning/evening reminder notifications
3. Completion tracking with streak counter
4. Refill reminders based on supply count" \
  --milestone v1.0-launch \
  --priority 9

tillr feature add "Pet Profile Pages" \
  --description "Rich profile for each pet with photo, breed, weight history" \
  --spec "1. Photo upload and cropping
2. Weight tracking with chart
3. Breed-specific health tips
4. Share profile via link" \
  --milestone v1.0-launch \
  --priority 5
```

Each feature has a spec — numbered acceptance criteria that tell agents (and humans) exactly what "done" means.

She checks her work:

```bash
tillr status
```

```
Project: pawtrack

Features: 3 total
  draft          3

Milestones: 1
Active Cycles: 0
```

Three features, all in draft, zero cycles running. Honest. Maya appreciates honest.

---

## The Dashboard

Monday morning. Maya starts the web viewer:

```bash
tillr serve
```

She opens her browser and her eyebrows go up.

The **Dashboard** answers every question her product manager has ever asked. Feature cards grouped by status in a kanban layout — draft, implementing, agent-qa, human-qa, done. Below that, **milestone progress bars** and a **priority breakdown**. At the bottom, an **activity feed** — currently just three "feature created" entries, but Maya can already imagine what this looks like in a week: a living stream of events, scores, approvals.

She presses **`?`** and a keyboard shortcuts overlay appears — quick keys for navigation, dark mode, jumping to features. She bookmarks the page.

---

## Planning the Roadmap

Before unleashing agents, Maya wants a plan. She opens her terminal and builds a roadmap:

```bash
tillr roadmap add "Core Pet Management" \
  --description "Profiles, multi-pet support, photo management" \
  --priority critical --category core --effort m

tillr roadmap add "Health Tracking Suite" \
  --description "Vet visits, medications, vaccination records, weight trends" \
  --priority critical --category features --effort l

tillr roadmap add "Social & Sharing" \
  --description "Share pet profiles, community features, photo sharing" \
  --priority low --category growth --effort l

tillr roadmap add "Notification System" \
  --description "Push notifications, email digests, smart reminders" \
  --priority high --category infrastructure --effort m
```

She links features to roadmap items so there's a clear line from strategy to execution:

```bash
tillr feature edit vet-visit-tracker --roadmap-item health-tracking-suite
tillr feature edit medication-reminders --roadmap-item health-tracking-suite
tillr feature edit pet-profile-pages --roadmap-item core-pet-management
```

Back in the browser, the **Roadmap** page has already refreshed — WebSocket, no reload needed. Items grouped by priority, each showing its category tag, effort sizing, and linked feature count.

She runs `tillr roadmap show` in her terminal for a quick text view:

```
Priority   Title                   Category         Effort  Features
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
critical   Core Pet Management     core             M       1
critical   Health Tracking Suite   features         L       2
high       Notification System     infrastructure   M       0
low        Social & Sharing        growth           L       0
```

For the first time, Maya can see the shape of her project — not in a Google Doc three weeks out of date, but in a living system that updates as work happens.

---

## The Agent Workflow

Time to let the agents cook.

Maya kicks off a feature implementation cycle for the highest-priority feature:

```bash
tillr cycle start feature-implementation medication-reminders
```

```
✓ Started cycle "feature-implementation" for feature "Medication Reminders"
  Steps: research → implement → agent-qa → judge → human-qa
  Current step: research
```

An agent picks up the work via `tillr next --json` — a structured payload with the feature spec, cycle state, and everything it needs:

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
    "cycle_type": "feature-implementation",
    "current_step": 0,
    "iteration": 1
  },
  "prior_results": []
}
```

The agent does its research and reports back:

```bash
tillr done --result "Analyzed notification APIs. Recommend using local notifications for medication reminders with a background sync service for schedule updates. Existing Pet model needs a medications relationship. Estimated 3 new database tables."
```

The cycle advances automatically. The implement step activates, another agent writes code and tests:

```bash
tillr done --result "Implemented medication CRUD with daily reminder scheduling. Added MedicationReminder model, NotificationService, and 47 unit tests. All tests passing."
```

Maya glances at the **Cycles** page. The pipeline shows five steps as connected nodes: research (green ✓), implement (green ✓), agent-qa (pulsing blue), judge and human-qa (gray, waiting).

The agent-qa step runs. The judge scores it:

```bash
tillr cycle score 8.5 --notes "Clean implementation. Good test coverage. Minor: streak counter resets on timezone change — edge case worth addressing."
```

Score threshold is 8.0. It passes. The feature advances to **human-qa**.

---

## The QA Gate

This is where Tillr earns its name. The feature doesn't ship because an agent said it's good. It ships because *Maya* says it's good.

She checks the QA queue:

```bash
tillr qa pending
```

```
Features awaiting QA:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  medication-reminders   Medication Reminders   Score: 8.5   Cycle: feature-implementation round 1
```

In the web viewer, the **QA** page shows a review queue — each feature with the spec, every cycle step's output, the judge's score and notes. Maya doesn't have to hunt for information.

She reviews the code. The judge was right about the timezone edge case. She rejects:

```bash
tillr qa reject medication-reminders \
  --notes "Streak counter breaks on timezone change. Add tests for users who travel. Also: the refill reminder calculation doesn't account for partial doses. Fix both, then resubmit."
```

The feature drops back to `implementing`. The cycle loops — implement → agent-qa → judge → human-qa. This time, score: 9.2. Maya reviews, approves:

```bash
tillr qa approve medication-reminders \
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

Two weeks in. PawTrack has a beta group of fifty users. Maya notices a small **⊕** button floating in the bottom-right corner of every page in the web viewer.

She clicks it. A minimal text input appears — just a box and Enter. She types:

```
Bug: medication reminder fires twice if app is force-closed and reopened
```

Hit Enter. Done. Two seconds.

Later, she checks:

```bash
tillr idea list --type bug
```

```
ID    Type   Title                                                         Status
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
1     bug    medication reminder fires twice if app is force-closed...     submitted
```

She can also submit bugs via CLI:

```bash
tillr bug report "Push notification sound doesn't respect phone silent mode" \
  --description "Users report that PawTrack reminders play sound even when phone is on silent. Affects iOS 17+."
```

When ready, she writes a spec and promotes the bug into a feature:

```bash
tillr idea spec 1 "1. Deduplicate pending notifications on app launch
2. Use notification ID to prevent duplicates at OS level
3. Add integration test for force-close scenario"

tillr idea approve 1
```

The idea becomes a feature, ready for a bug-triage cycle. From user report to tracked, specced, assigned work — about two minutes.

---

## Tracking Progress

It's the end of the sprint. Maya's product manager wants numbers. She opens the **Stats** page.

The **velocity chart** shows features completed per week — climbing steadily. The **success rate** shows what percentage of cycle iterations pass their quality gate, counting only completed cycles so the number stays honest.

A **cycle time distribution** chart breaks down average phase duration. Research: 0.5 hours. Implement: 3.2 hours. Agent QA: 0.8 hours. Human QA: 6.4 hours. Maya raises an eyebrow — *she's* the bottleneck. She makes a mental note to delegate some QA.

She also checks from the terminal:

```bash
tillr roadmap stats
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

The numbers tell a clear story: critical work is moving, lower-priority items are deferred, and most roadmap items are connected to actual features.

---

## Agents and Discussions

By week three, Maya has two agents working simultaneously:

```bash
tillr agent list
```

The web viewer shows each agent session: what it's working on, when it last checked in via heartbeat, its completion rate. One agent is implementing the Vet Visit Tracker; the other is running a bug-triage cycle on the notification issue. Long-running agents send `tillr heartbeat` calls so Maya can tell if something's stuck.

Then the Vet Visit Tracker agent hits an architectural decision it can't make alone: should photo attachments be stored locally or uploaded to cloud storage?

The agent opens a discussion:

```bash
tillr discuss new "RFC: Photo Storage Strategy" \
  --feature vet-visit-tracker --author agent-1
```

It posts two proposals as comments. Maya sees them in the **Discussions** page, adds her own take, and resolves it:

```bash
tillr discuss resolve 1

tillr decision add "Local photo storage for v1.0" \
  --context "Need to store vet visit photo attachments" \
  --decision "Use local device storage with iCloud/Google Drive sync" \
  --consequences "No web access to photos until v2.0 cloud migration" \
  --feature vet-visit-tracker
```

The decision is recorded permanently as an ADR, linked to the feature, and visible on the **Decisions** page. No Slack thread. No meeting. No context lost.

---

## The Big Picture

A month in. Maya leans back and compares.

**Before Tillr:**
- Features lived in a Google Doc nobody updated
- Agent work was invisible until it showed up as a PR
- QA was "whenever someone remembers to look at the PR"
- Bugs were reported in Slack and forgotten in Slack
- The product manager asked "where are we?" and got a shrug

**After Tillr:**
- Every feature has a spec, a status, and a history
- The Dashboard shows project health in one glance
- Agents work through structured cycles with quality gates
- Nothing ships without human QA
- Bugs go from report to tracked feature in two minutes
- Discussions and ADRs capture architectural decisions permanently
- The **Timeline** page shows Gantt-style feature progress over time
- `tillr export features --format md` generates stakeholder reports
- The product manager opens the web viewer and answers his own questions

Maya's favorite part? She didn't change how her agents work. They still write code, run tests, do research. Tillr just gave them *structure* — a pipeline to move through, scores to hit, humans to answer to.

The agents are the engine. Tillr is the steering wheel.

She finishes her coffee (hot, this time) and runs one last command:

```bash
tillr status
```

```
Project: pawtrack

Features: 6 total
  draft          1
  implementing   1
  human-qa       1
  done           3

Milestones: 1
Active Cycles: 2

Recent Activity:
  [2025-02-14 09:12:03] qa.approved (medication-reminders)
  [2025-02-14 08:45:17] cycle.score (vet-visit-tracker)
  [2025-02-14 08:30:01] work_item.completed (vet-visit-tracker)
```

Sixty percent through v1.0, two active cycles, every event captured and searchable. No surprises. No lost work. No cold coffee.

Well — maybe a little cold coffee. Some things never change.
