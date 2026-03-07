# QA — Screen Specification

## Overview

The QA page is the human gatekeeper for feature completion in Lifecycle. It presents features that have reached the `human-qa` status and provides reviewers with the tools to approve or reject them. Approval transitions a feature to `done`; rejection sends it back to `implementing` for rework.

This page enforces the **QA gate** — a hard constraint in the state machine that requires every feature to pass through `human-qa` before it can reach `done`. No shortcut exists; the valid transitions are:

```
implementing → agent-qa → human-qa → done
implementing → human-qa → done
```

The page is rendered by `App.renderQA()` in `app2.js` (line 814) and backed by the `/api/qa/` endpoint group in `server.go` (line 615).

---

## User Roles & Personas

| Role | Description | Primary Actions |
|---|---|---|
| **Human Reviewer** | Product owner, tech lead, or designated QA person who makes the approve/reject decision. | Review pending features, write notes, approve or reject. |
| **Agent Observer** | An AI agent checking QA status to know when work is unblocked. | Read pending count, check if a feature was approved or rejected (via API). |
| **Project Manager** | Tracks throughput and review velocity across features. | Monitor summary stats (pending, approved, rejected counts), scan recently-reviewed list. |

---

## User Stories

### US-1: View pending QA queue

**As a** human reviewer,
**I want to** see all features awaiting my review in one place,
**So that** I know exactly what needs my attention and can prioritize.

**Given** three features are in `human-qa` status,
**When** I navigate to the QA page,
**Then** I see a summary bar showing "3 Pending", and three review cards in the left column titled "Pending Review".

---

### US-2: Read feature context before reviewing

**As a** human reviewer,
**I want to** see the feature name, description, priority, milestone, time-in-QA, and prior review rounds on the card,
**So that** I have enough context to make a decision without leaving the page.

**Given** a feature "Search API" (P1, milestone "v1.0 MVP") entered QA 2 hours ago with 1 prior rejection,
**When** I view its QA card,
**Then** I see:
- Title: "Search API"
- Badge: "awaiting QA" (yellow)
- Priority badge: "P1 High" (high-priority style)
- Description text
- Metadata row: 🏷️ feature ID, 📌 v1.0 MVP, 🕐 2h ago, 🔄 1 prior review

---

### US-3: Approve a feature

**As a** human reviewer,
**I want to** approve a feature with optional notes,
**So that** it transitions to `done` and unblocks downstream work.

**Given** I am viewing a pending QA card,
**When** I optionally type notes in the textarea and click "✓ Approve",
**Then** a confirmation modal appears showing:
- Title: "Approve Feature"
- Description: "This will mark the feature as done and complete the QA cycle."
- Feature ID
- My notes (or default "Approved via web" if I left notes blank)

**When** I click the "✓ Approve" button in the modal,
**Then** a `POST /api/qa/{id}/approve` request fires with `{ notes }`, a success toast "✓ Feature approved" appears, a `QAResult` record is created with `passed: true`, the feature transitions to `done`, and the page re-renders showing one fewer pending card.

---

### US-4: Reject a feature (notes required)

**As a** human reviewer,
**I want to** reject a feature with a mandatory explanation,
**So that** the implementing agent knows exactly what to fix.

**Given** I am viewing a pending QA card and the notes textarea is empty,
**When** I click "✗ Reject",
**Then** the textarea gains a `qa-notes-required` highlight (red border), the placeholder changes to "Please provide a reason for rejection…", the textarea receives focus, and an error toast "Please add rejection notes" appears. **The rejection does not proceed.**

**Given** I have typed rejection notes,
**When** I click "✗ Reject",
**Then** a confirmation modal appears showing:
- Title: "Reject Feature"
- Description: "This will send the feature back to development for further work."
- Feature ID
- My notes

**When** I click the "✗ Reject" button in the modal,
**Then** a `POST /api/qa/{id}/reject` request fires with `{ notes }`, an error-styled toast "✗ Feature rejected" appears, a `QAResult` record is created with `passed: false`, the feature transitions to `implementing`, and the page re-renders.

---

### US-5: Cancel a review action

**As a** human reviewer,
**I want to** cancel an approve or reject after clicking the button,
**So that** I don't accidentally change feature status.

**Given** the confirmation modal is open,
**When** I click "Cancel", press Escape, or click the overlay backdrop,
**Then** the modal fades out (200ms transition) and is removed from the DOM. No API call is made. The feature remains in `human-qa`.

---

### US-6: View recently reviewed features

**As a** a project manager,
**I want to** see a timeline of recent approvals and rejections,
**So that** I can track review velocity and catch patterns.

**Given** features have been approved and rejected in the past,
**When** I view the QA page,
**Then** the right column "Recently Reviewed" shows a list of events, each with:
- Icon: ✓ (green circle) for approved, ✗ (red circle) for rejected
- Feature ID
- Verdict: "Approved" or "Rejected"
- Relative timestamp (e.g., "2h ago")
- Reviewer notes (if provided), extracted from the event's JSON `data` field

---

### US-7: View summary statistics

**As a** project manager,
**I want to** see aggregate counts of pending, approved, and rejected features at a glance,
**So that** I can gauge project health and QA throughput.

**Given** 2 features are pending, 5 have been approved, and 1 has been rejected,
**When** I view the QA page,
**Then** the summary bar shows: **2** Pending (yellow), **5** Approved (green), **1** Rejected (red).

---

### US-8: Empty state — no pending reviews

**As a** human reviewer,
**I want to** see a clear indication when there's nothing to review,
**So that** I know the queue is clear.

**Given** no features are in `human-qa` status,
**When** I view the QA page,
**Then** the summary bar shows "0 Pending", and the left column renders no cards (empty). The right column may still show historical reviews.

---

### US-9: Empty state — no review history

**As a** human reviewer visiting QA for the first time,
**I want to** see a friendly empty state in the reviewed column,
**So that** I'm not confused by a blank section.

**Given** no `qa.approved` or `qa.rejected` events exist,
**When** I view the QA page,
**Then** the right column shows an empty state with 📋 icon and text "No reviews yet".

---

### US-10: QA gate enforcement

**As a** system,
**I want to** enforce that features cannot reach `done` without passing through `human-qa`,
**So that** every shipped feature has explicit human sign-off.

**Given** a feature is in `implementing` status,
**When** any actor attempts to transition it directly to `done`,
**Then** the transition is rejected because `ValidTransitions["implementing"]` does not include `"done"`. The only path is `implementing → human-qa → done` (or through `agent-qa` first).

**Given** a feature is in `agent-qa` status,
**When** any actor attempts to transition it directly to `done`,
**Then** the transition is rejected. The only path forward is `agent-qa → human-qa → done`.

---

### US-11: Rejected feature re-enters QA

**As a** human reviewer,
**I want** rejected features to reappear in my queue after rework,
**So that** I can verify the fixes.

**Given** I rejected feature "Search API" with notes "Missing pagination",
**When** the implementing agent fixes the issue and transitions the feature back to `human-qa`,
**Then** the feature reappears in my pending queue with "🔄 1 prior review" in its metadata, and the previous rejection appears in the recently reviewed list.

---

### US-12: Handle API errors gracefully

**As a** human reviewer,
**I want** the page to handle network failures without breaking,
**So that** I can still see what's available or retry.

**Given** the `/api/features?status=human-qa` or `/api/history` call fails,
**When** the page loads,
**Then** `features` and `history` fall back to empty arrays. The page renders with 0 Pending and no reviewed items — it does not crash or show a raw error.

**Given** the `POST /api/qa/{id}/approve` call fails,
**When** the server returns an error,
**Then** an error toast "Error: could not approve feature" appears and the page re-renders (via `App.navigate('qa')`).

---

### US-13: Backend rejects invalid QA actions

**As a** system,
**I want** the approve/reject endpoints to enforce status preconditions,
**So that** only features actually in `human-qa` can be acted upon.

**Given** a feature is in `implementing` status (not `human-qa`),
**When** a POST to `/api/qa/{id}/approve` is made,
**Then** the engine returns an error: `cannot approve feature in "implementing" status: must be in human-qa`. The feature status does not change.

---

## Screen Layout

### Structure (top to bottom)

```
┌─────────────────────────────────────────────────────────┐
│  Page Header                                            │
│  ┌───────────────────────────────────────────────────┐  │
│  │  h2: "Quality Assurance"                          │  │
│  │  subtitle: "Review and approve features"          │  │
│  └───────────────────────────────────────────────────┘  │
│                                                         │
│  Summary Bar                                            │
│  ┌──────────┬───┬──────────┬───┬──────────┐            │
│  │  2       │ │ │  5       │ │ │  1       │            │
│  │  Pending │   │  Approved│   │  Rejected│            │
│  │  (yellow)│   │  (green) │   │  (red)   │            │
│  └──────────┴───┴──────────┴───┴──────────┘            │
│                                                         │
│  Two-Column Layout (grid: 3fr 2fr)                      │
│  ┌──────────────────────┬──────────────────┐            │
│  │ ● Pending Review     │ ● Recently       │            │
│  │                      │   Reviewed       │            │
│  │ ┌──────────────────┐ │ ┌──────────────┐ │            │
│  │ │ QA Review Card   │ │ │ ✓ feature-1  │ │            │
│  │ │ (see below)      │ │ │   Approved   │ │            │
│  │ └──────────────────┘ │ │   2h ago     │ │            │
│  │ ┌──────────────────┐ │ ├──────────────┤ │            │
│  │ │ QA Review Card   │ │ │ ✗ feature-2  │ │            │
│  │ │                  │ │ │   Rejected   │ │            │
│  │ └──────────────────┘ │ │   1d ago     │ │            │
│  │                      │ │   "notes..." │ │            │
│  │                      │ └──────────────┘ │            │
│  └──────────────────────┴──────────────────┘            │
└─────────────────────────────────────────────────────────┘
```

### QA Review Card (detail)

```
┌─ border-left: 3px solid var(--warning) ─────────────────┐
│  Feature Name            [awaiting QA] [P1 High]         │
│                                                          │
│  Description text goes here...                           │
│                                                          │
│  🏷️ feature-id  📌 v1.0 MVP  🕐 2h ago  🔄 1 prior     │
│                                                          │
│  ┌─────────────────────────────────────────────────┐     │
│  │ Add review notes or feedback…                   │     │
│  │                                                 │     │
│  └─────────────────────────────────────────────────┘     │
│  [✓ Approve]  [✗ Reject]                                │
└──────────────────────────────────────────────────────────┘
```

### Confirmation Modal

```
┌─────── backdrop: rgba(0,0,0,0.5) + blur(4px) ──────────┐
│                                                          │
│          ┌──────────────────────────────┐                │
│          │  ✓  Approve Feature          │                │
│          │                              │                │
│          │  Feature: **search-api**     │                │
│          │                              │                │
│          │  This will mark the feature  │                │
│          │  as done and complete the    │                │
│          │  QA cycle.                   │                │
│          │                              │                │
│          │  Notes:                      │                │
│          │  "Looks good, ship it"       │                │
│          │                              │                │
│          │  [Cancel]    [✓ Approve]     │                │
│          └──────────────────────────────┘                │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

### Responsive Behavior

| Breakpoint | Change |
|---|---|
| **≤ 768px** (tablet) | Two-column layout collapses to single column. Approve/Reject buttons get larger touch targets (`min-height: 44px`). |
| **≤ 480px** (mobile) | Summary bar stacks vertically and centers text. Card header stacks (name above badges). Approve/Reject buttons go full-width and stack vertically. |

---

## Data Requirements

### API Calls on Page Load

| Call | Endpoint | Purpose |
|---|---|---|
| Features | `GET /api/features?status=human-qa` | Fetch features pending human review |
| History | `GET /api/history` | Fetch all events (filtered client-side for `qa.approved` / `qa.rejected`) |

Both calls are made in parallel via `Promise.all`. On failure, each falls back to `[]`.

### Feature Object (from `/api/features`)

| Field | Type | Used For |
|---|---|---|
| `id` | string | Card ID, API calls, metadata display |
| `name` | string | Card title |
| `description` | string | Card body (fallback: "No description provided") |
| `priority` | int | Priority badge (≤1 = High, ≤3 = Medium, else Low) |
| `milestone_name` | string? | Metadata row (shown only if present) |
| `updated_at` | ISO 8601 | "Entered QA" relative time |
| `created_at` | ISO 8601 | Fallback for `updated_at` |
| `status` | string | Always `"human-qa"` for this page |

### History Event Object (from `/api/history`)

| Field | Type | Used For |
|---|---|---|
| `event_type` | string | Filter for `qa.approved` / `qa.rejected` |
| `feature_id` | string | Link to feature, review round counter |
| `created_at` | ISO 8601 | Relative timestamp in reviewed list |
| `data` | JSON string | Contains `{ notes }` or `{ reason }` for display |

### Derived Data (computed client-side)

| Variable | Derivation |
|---|---|
| `qaEvents` | `history.filter(e => e.event_type === 'qa.approved' \|\| 'qa.rejected')` |
| `reviewCounts` | Count of QA events per `feature_id` — drives "🔄 N prior reviews" |
| `approvedCount` | `qaEvents.filter(e.event_type === 'qa.approved').length` |
| `rejectedCount` | `qaEvents.filter(e.event_type === 'qa.rejected').length` |

### QAResult Record (written on approve/reject)

| Field | Type | Value |
|---|---|---|
| `feature_id` | string | The reviewed feature |
| `qa_type` | string | Always `"human"` |
| `passed` | bool | `true` (approve) or `false` (reject) |
| `notes` | string | Reviewer's notes |

---

## Interactions

### Approve Flow

```
User clicks "✓ Approve"
  → _qaConfirmAndAct(featureId, 'approve')
    → Read notes from textarea (or default "Approved via web")
    → _showQAConfirmModal(featureId, 'approve', notes, onConfirm)
      → Modal renders with green icon, "Approve Feature" title
      → User clicks "✓ Approve" in modal
        → _executeQAAction(featureId, 'approve', notes)
          → POST /api/qa/{featureId}/approve  { notes }
          → Backend: engine.ApproveFeatureQA()
            → Validates status === "human-qa"
            → Creates QAResult (passed: true)
            → TransitionFeature → "done"
          → Toast: "✓ Feature approved" (success)
          → App.navigate('qa')  // re-renders page
```

### Reject Flow

```
User clicks "✗ Reject"
  → _qaConfirmAndAct(featureId, 'reject')
    → IF notes are empty:
      → textarea.focus()
      → placeholder = "Please provide a reason for rejection…"
      → textarea.classList.add('qa-notes-required')
      → Toast: "Please add rejection notes" (error)
      → RETURN (no modal, no API call)
    → IF notes are present:
      → _showQAConfirmModal(featureId, 'reject', notes, onConfirm)
        → Modal renders with red icon, "Reject Feature" title
        → User clicks "✗ Reject" in modal
          → _executeQAAction(featureId, 'reject', notes)
            → POST /api/qa/{featureId}/reject  { notes }
            → Backend: engine.RejectFeatureQA()
              → Validates status === "human-qa"
              → Creates QAResult (passed: false)
              → TransitionFeature → "implementing"
            → Toast: "✗ Feature rejected" (error style)
            → App.navigate('qa')  // re-renders page
```

### Modal Dismiss

| Trigger | Behavior |
|---|---|
| Click "Cancel" button | Modal fades out (200ms), removed from DOM |
| Press Escape key | Same as Cancel; ESC handler is cleaned up after |
| Click overlay backdrop | Same as Cancel (checks `e.target === modal`) |

### Textarea Input

- On any `input` event, the `qa-notes-required` CSS class is removed from the textarea, clearing the red validation highlight.

### Page Re-render

After every approve or reject action (success or failure), `App.navigate('qa')` is called, which re-fetches all data and re-renders the entire page.

---

## State Handling

### Feature Status State Machine (relevant transitions)

```
                   ┌──────────┐
                   │  draft   │
                   └────┬─────┘
                        │
                   ┌────▼─────┐
                   │ planning │
                   └────┬─────┘
                        │
                   ┌────▼────────┐
               ┌───│implementing │◄────────────────┐
               │   └──┬───────┬──┘                  │
               │      │       │                     │
          ┌────▼───┐  │  ┌────▼────┐                │
          │agent-qa│  │  │human-qa │──── reject ────┘
          └────┬───┘  │  └────┬────┘
               │      │       │ approve
               │      │  ┌────▼────┐
               └──────┘  │  done   │
                          └────────┘

    ★ The ONLY path to "done" passes through "human-qa"
    ★ "blocked" can be entered from any active status
```

### Valid Transitions Map (from `engine.go`)

```go
"draft"        → ["planning", "implementing", "blocked"]
"planning"     → ["implementing", "blocked"]
"implementing" → ["agent-qa", "human-qa", "blocked"]
"agent-qa"     → ["human-qa", "implementing", "blocked"]
"human-qa"     → ["done", "implementing", "blocked"]      // ← THE GATE
"blocked"      → ["draft", "planning", "implementing"]
"done"         → ["implementing"]                          // reopen
```

### Backend Precondition Enforcement

Both `ApproveFeatureQA()` and `RejectFeatureQA()` check `f.Status != "human-qa"` and return an error if violated. This is a defense-in-depth layer — the UI only shows features already in `human-qa`, but the backend independently validates.

### Error States

| Scenario | Behavior |
|---|---|
| API fetch fails on page load | `features` and `history` default to `[]`; page renders empty |
| POST approve/reject fails | Error toast displayed; page re-renders via `App.navigate('qa')` |
| Feature not in `human-qa` | Backend rejects with error message including current status |
| Invalid API path format | Backend returns 400 with `{ "error": "invalid path" }` |
| Unknown action verb | Backend returns 400 with `{ "error": "unknown action: xyz" }` |
| Non-POST to approve/reject | Backend returns 405 with `{ "error": "POST required" }` |

### Toast Notifications

| Event | Message | Style |
|---|---|---|
| Approve success | "✓ Feature approved" | `success` (green) |
| Reject success | "✗ Feature rejected" | `error` (red) |
| Reject without notes | "Please add rejection notes" | `error` (red) |
| API error on approve | "Error: could not approve feature" | `error` (red) |
| API error on reject | "Error: could not reject feature" | `error` (red) |

---

## Accessibility Notes

### Semantic HTML & ARIA

- The notes textarea has `aria-label="Review notes for {Feature Name}"` for screen reader context.
- Approve button has `aria-label="Approve {Feature Name}"`.
- Reject button has `aria-label="Reject {Feature Name}"`.
- Metadata icons (🏷️, 📌, 🕐, 🔄) use `title` attributes for tooltip/screen reader fallback.

### Keyboard Navigation

- **Tab order**: Textarea → Approve button → Reject button (per card, top to bottom).
- **Escape**: Closes the confirmation modal from anywhere on the page.
- **Enter/Space**: Activates focused buttons (native browser behavior).
- Textarea supports standard keyboard editing; `qa-notes-required` class is removed on any `input` event.

### Focus Management

- When rejection is attempted without notes, `textarea.focus()` is called to direct the user to the required field.
- The confirmation modal does not explicitly trap focus (potential improvement area).

### Color & Contrast

- Status information is conveyed through both color AND text/icon:
  - Approve: green color + "✓" icon + "Approved" text
  - Reject: red color + "✗" icon + "Rejected" text
  - Pending: yellow color + "awaiting QA" badge text
- Priority uses both color class AND label text ("P1 High", "P2 Medium", "P5 Low").
- Dark mode and light mode both supported via CSS custom properties and `[data-theme="light"]` selectors.

### Touch Targets

- At ≤768px, approve/reject buttons expand to `min-height: 44px` (WCAG 2.5.5 minimum).
- At ≤480px, buttons go full-width for easier mobile tapping.

### Motion

- All animations use CSS transitions (200–300ms) and `fadeIn` keyframe. There is no `prefers-reduced-motion` media query override (potential improvement area).
