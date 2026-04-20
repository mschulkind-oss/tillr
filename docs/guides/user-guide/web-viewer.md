# Web Viewer Guide

Start the web viewer:

```bash
tillr serve
# Tillr web viewer running at http://localhost:3847
# Watching tillr.db for changes…
```

| Flag | Description |
|------|-------------|
| `--port P` | Override the default port (3847) |

The web viewer is **read-only by design** — it renders the data that the CLI manages. All state changes happen through CLI commands (with the exception of QA approve/reject, which can be done from the web UI). The viewer updates in real time via WebSocket, so you can keep it open while agents work.

## Dashboard

The landing page shows project health at a glance. A kanban board groups features by tillr state — click any column header to filter the features list. Below the kanban you'll find milestone progress bars, a recent activity feed, roadmap highlights, a priority distribution chart, and a preview of active cycles.

## Feature Board

A tabular list of all features with status badges, priority indicators, and milestone assignments. Click any row to expand an inline detail panel showing the feature's description, milestone, priority, timestamps, and full history. Use the checkboxes to select multiple features, then use the floating action bar to batch-update status, milestone, or priority.

## Roadmap View

A presentation-quality roadmap grouped by priority (Critical → High → Medium → Low). Each item shows its status, category, and effort sizing badge. Click an item to expand its full description.

## Timeline View

A Gantt-style timeline page showing feature progress over time. Access it at `#timeline`. Features are displayed as horizontal bars spanning their active period, grouped by milestone. Useful for spotting bottlenecks and understanding parallel work.

## Cycle Progress

Displays both active and completed iteration cycles. Each cycle shows a step pipeline visualizing progress through the cycle's stages. Per-step scores are displayed alongside sparkline charts, with an average score summary. A cycle type reference grid at the bottom lists all available cycle definitions.

## Event History

A scrollable timeline of every project event, grouped by date. Category filter buttons (All / Cycle / Work / Feature / Roadmap / Milestone) and a feature dropdown let you narrow the view. Pagination uses a "load more" button to fetch older events.

## QA Review

A dedicated interface for reviewing features that have reached the `human-qa` stage. Features appear automatically when they enter `human-qa`, forming a review queue. Review the feature context and cycle results, then approve or reject with notes using the built-in textarea and action buttons.

## Decisions (ADRs)

Browse Architecture Decision Records at `#adrs`. Decisions are listed with their status (proposed, accepted, rejected, superseded, deprecated), linked features, and full context. Click any decision to view the complete record including context, decision text, and consequences.

## Keyboard Shortcuts

Press **`?`** on any page to see all available keyboard shortcuts. Shortcuts include navigation between pages, toggling dark mode, and jumping to specific features.

## Quick Feedback Button

A small **⊕** button floats in the bottom-right corner of every page. Click it to open a minimal text input — just type and press Enter to submit feedback, bug reports, or feature ideas. No forms, no dropdowns. Submissions appear in the idea queue (`tillr idea list`).

## Live Updates / WebSocket

The web viewer maintains a WebSocket connection to the server. When any data changes in the database, the server pushes an update and all open pages refresh automatically. If the connection drops, the viewer auto-reconnects with a 3-second backoff. No manual refresh needed — keep the dashboard open and watch agents work in real time.

---

« [User Guide](./README.md)
