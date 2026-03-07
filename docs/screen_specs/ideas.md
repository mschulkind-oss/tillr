# Ideas / Intake — Screen Specification

## Overview

The **Ideas** page is the primary intake funnel for the Lifecycle app. It allows humans to submit raw ideas (features or bugs), agents to generate structured specifications from those ideas, and humans to approve or reject the resulting specs — converting approved ideas into tracked features.

**Route:** `#ideas` (SPA hash route)
**Page title:** 💡 Idea Queue
**Primary audience:** Human product owners, development leads, and AI agents

### Workflow Summary

```
Human submits idea (pending)
       ↓
Agent picks up idea, generates spec (processing → spec-ready)
       ↓
Human reviews spec (approve → feature created | reject → archived)
```

### Key Metrics (subtitle bar)

The page subtitle displays a live summary: `{total} idea(s) · {pending} pending · {spec-ready} ready for review`

---

## User Roles & Personas

| Role | Description | Key Actions |
|------|-------------|-------------|
| **Product Owner** | Human stakeholder who submits ideas and makes approval decisions | Submit ideas, review specs, approve/reject |
| **Developer** | Human contributor who submits bug reports or feature ideas | Submit ideas, view spec details |
| **Spec Agent** | AI agent that picks up pending ideas and generates structured specifications | `GET /api/ideas` (poll for pending), `POST /api/ideas/{id}/spec` |
| **Orchestrator Agent** | AI agent that dispatches spec work and monitors the queue | `GET /api/ideas?status=pending`, triggers spec generation |
| **Viewer** | Any stakeholder browsing the idea queue for awareness | Read-only browsing of all idea statuses |

---

## User Stories

### US-1: Submit a New Idea

**As a** product owner or developer,
**I want to** submit a new idea with a title, description, type, and auto-implement preference,
**So that** it enters the intake queue for specification and review.

#### Acceptance Criteria

**Scenario 1: Successful submission**
- **Given** I am on the Ideas page
- **When** I click the "+ Submit Idea" button
- **Then** a modal overlay appears centered on the viewport with a semi-transparent backdrop

- **Given** the submit modal is open
- **When** I fill in the title "Add dark mode toggle", set type to "Feature", write a markdown description, and click "Submit"
- **Then** the modal closes, a new idea card appears in the "Pending" group, and the subtitle counters update

**Scenario 2: Title is required**
- **Given** the submit modal is open
- **When** I leave the title field empty and click "Submit"
- **Then** a browser alert displays "Title is required" and the modal remains open

**Scenario 3: Dismiss modal without submitting**
- **Given** the submit modal is open
- **When** I click "Cancel" or click the backdrop outside the modal card
- **Then** the modal closes and no idea is created

**Scenario 4: Default values**
- **Given** the submit modal is open
- **When** I only fill in the title and click "Submit"
- **Then** the idea is created with type "feature", auto-implement unchecked, description empty, and submitted_by "human"

---

### US-2: Browse Ideas by Status Group

**As a** product owner,
**I want to** see all ideas organized by their current status,
**So that** I can quickly find items that need my attention.

#### Acceptance Criteria

**Scenario 1: Status groups displayed in order**
- **Given** ideas exist in multiple statuses
- **When** I view the Ideas page
- **Then** ideas are grouped under section headers in this order: ⏳ Pending → ⚙️ Processing → ✅ Spec Ready → 👍 Approved → ❌ Rejected

**Scenario 2: Approved and Rejected sections are collapsed**
- **Given** approved or rejected ideas exist
- **When** I view the Ideas page
- **Then** the Approved and Rejected sections are rendered inside `<details>` elements (collapsed by default) while Pending, Processing, and Spec Ready are expanded

**Scenario 3: Empty state**
- **Given** no ideas have been submitted
- **When** I view the Ideas page
- **Then** I see an empty state with the 💡 icon, "No ideas yet" text, and the hint "Submit your first idea using the button above."

**Scenario 4: Ideas sorted within groups**
- **Given** multiple ideas exist in the same status group
- **When** I view that group
- **Then** ideas are sorted by creation date, newest first (`created_at DESC`)

---

### US-3: View Idea Details

**As a** product owner,
**I want to** see the full details of each idea at a glance,
**So that** I can understand its content without navigating away.

#### Acceptance Criteria

**Scenario 1: Idea card content**
- **Given** an idea exists with title "Fix login timeout", type "bug", submitted by "alice"
- **When** I view its card
- **Then** I see: the 🐛 emoji (bug type), bold title "Fix login timeout", status badge, relative timestamp (e.g. "2h ago"), and "by alice"

**Scenario 2: Feature type indicator**
- **Given** an idea has type "feature"
- **When** I view its card
- **Then** the ✨ emoji is displayed before the title

**Scenario 3: Auto-implement badge**
- **Given** an idea has `auto_implement: true`
- **When** I view its card
- **Then** a "🤖 auto" label appears next to the title

**Scenario 4: Description truncation**
- **Given** an idea has a `raw_input` longer than 200 characters
- **When** I view its card
- **Then** the description is truncated to 200 characters with "…" appended

**Scenario 5: Generated spec is viewable**
- **Given** an idea has a `spec_md` field populated
- **When** I view its card
- **Then** a collapsible `<details>` section labeled "View Spec" is shown, containing the rendered markdown

**Scenario 6: Linked feature is navigable**
- **Given** an idea has been approved and linked to feature "f-123"
- **When** I view its card
- **Then** I see "→ Feature: f-123" with a clickable link that navigates to the feature detail view

---

### US-4: Approve an Idea

**As a** product owner,
**I want to** approve a spec-ready idea,
**So that** it is converted into a tracked feature for development.

#### Acceptance Criteria

**Scenario 1: Approve button visibility**
- **Given** an idea has status "spec-ready"
- **When** I view its card
- **Then** "✅ Approve" and "❌ Reject" buttons are visible at the bottom of the card

**Scenario 2: Approve action**
- **Given** I am viewing a spec-ready idea
- **When** I click "✅ Approve"
- **Then** a `POST /api/ideas/{id}/approve` request is sent, the idea moves to the "Approved" group, and the page refreshes

**Scenario 3: Buttons not shown for other statuses**
- **Given** an idea has status "pending", "processing", "approved", or "rejected"
- **When** I view its card
- **Then** no approve/reject buttons are displayed

---

### US-5: Reject an Idea

**As a** product owner,
**I want to** reject an idea that doesn't meet our needs,
**So that** it is archived and no longer clogs the review queue.

#### Acceptance Criteria

**Scenario 1: Reject action**
- **Given** I am viewing a spec-ready idea
- **When** I click "❌ Reject"
- **Then** a `POST /api/ideas/{id}/reject` request is sent, the idea moves to the "Rejected" group (collapsed), and the page refreshes

---

### US-6: Agent Generates a Specification

**As a** spec agent,
**I want to** pick up a pending idea and submit a structured specification,
**So that** the idea is ready for human review.

#### Acceptance Criteria

**Scenario 1: Pick up next pending idea**
- **Given** pending ideas exist in the queue
- **When** the agent calls `GET /api/ideas?status=pending`
- **Then** a list of pending ideas is returned, ordered oldest first (the agent picks the oldest via `GetNextIdeaForSpec`)

**Scenario 2: Submit generated spec**
- **Given** the agent has generated a markdown spec for idea #5
- **When** the agent calls `POST /api/ideas/5/spec` with `{"spec_md": "## Spec\n..."}`
- **Then** the idea's status transitions from "pending" (or "processing") to "spec-ready", the spec is stored, and the updated idea is returned

**Scenario 3: Spec appears on card**
- **Given** a spec has been submitted for an idea
- **When** a human views the Ideas page
- **Then** the idea card shows a "View Spec" collapsible section and the idea appears under the "Spec Ready" group with approve/reject buttons

---

### US-7: Auto-implement Flag

**As a** product owner,
**I want to** mark an idea for automatic implementation,
**So that** once approved, agents proceed without waiting for manual dispatch.

#### Acceptance Criteria

**Scenario 1: Toggle in submit form**
- **Given** the submit modal is open
- **When** I check the "Auto-implement" checkbox and submit
- **Then** the created idea has `auto_implement: true`

**Scenario 2: Visual indicator on card**
- **Given** an idea has `auto_implement: true`
- **When** I view the idea card
- **Then** a "🤖 auto" label appears inline with the title

**Scenario 3: Default is off**
- **Given** the submit modal is open
- **When** I do not interact with the auto-implement checkbox
- **Then** the created idea has `auto_implement: false`

---

### US-8: Real-time Updates via WebSocket

**As a** product owner viewing the Ideas page,
**I want to** see changes in real time when agents submit specs or other users submit ideas,
**So that** I always see the current state without manual refresh.

#### Acceptance Criteria

**Scenario 1: Live update on new idea**
- **Given** I am viewing the Ideas page and another user submits an idea
- **When** the WebSocket broadcasts a change event
- **Then** the Ideas page re-renders with the new idea card visible

**Scenario 2: Live update on status change**
- **Given** I am viewing the Ideas page and an agent submits a spec (status → spec-ready)
- **When** the WebSocket broadcasts the update
- **Then** the idea card moves from the Pending group to the Spec Ready group with approve/reject buttons

---

## Screen Layout

### Page Structure

```
┌─────────────────────────────────────────────────────┐
│  💡 Idea Queue                                      │
│  12 idea(s) · 3 pending · 2 ready for review       │
├─────────────────────────────────────────────────────┤
│  [+ Submit Idea]                                    │
├─────────────────────────────────────────────────────┤
│                                                     │
│  ⏳ Pending (3)                                     │
│  ┌─────────────────────────────────────────────┐    │
│  │ ✨ Add dark mode toggle         🤖 auto     │    │
│  │ [planning] · 2h ago                         │    │
│  │ Users want a dark mode toggle in settings…  │    │
│  │ by alice                                    │    │
│  └─────────────────────────────────────────────┘    │
│  ┌─────────────────────────────────────────────┐    │
│  │ 🐛 Login timeout on slow networks           │    │
│  │ [planning] · 5h ago                         │    │
│  │ When the network is slow, the login page…   │    │
│  │ by bob                                      │    │
│  └─────────────────────────────────────────────┘    │
│                                                     │
│  ⚙️ Processing (1)                                  │
│  ┌─────────────────────────────────────────────┐    │
│  │ ✨ API rate limiting             [planning]  │    │
│  │ 10m ago                                     │    │
│  │ We need rate limiting on all public endpo…  │    │
│  │ by carol                                    │    │
│  └─────────────────────────────────────────────┘    │
│                                                     │
│  ✅ Spec Ready (2)                                  │
│  ┌─────────────────────────────────────────────┐    │
│  │ ✨ Search functionality          [human-qa]  │    │
│  │ 1d ago                                      │    │
│  │ Full-text search across all entities…       │    │
│  │ by dave                                     │    │
│  │ ▸ View Spec                                 │    │
│  │ [✅ Approve]  [❌ Reject]                    │    │
│  └─────────────────────────────────────────────┘    │
│                                                     │
│  ▸ 👍 Approved (4)       ← collapsed by default    │
│  ▸ ❌ Rejected (2)       ← collapsed by default    │
│                                                     │
└─────────────────────────────────────────────────────┘
```

### Submit Modal

```
┌─────────── Modal Overlay (rgba(0,0,0,0.6)) ───────────┐
│                                                         │
│     ┌─────────────────────────────────────────┐         │
│     │  Submit New Idea                        │         │
│     │                                         │         │
│     │  Title *                                │         │
│     │  ┌───────────────────────────────────┐  │         │
│     │  │ Idea title                        │  │         │
│     │  └───────────────────────────────────┘  │         │
│     │                                         │         │
│     │  Description                            │         │
│     │  ┌───────────────────────────────────┐  │         │
│     │  │ Describe the idea                 │  │         │
│     │  │ (markdown supported)              │  │         │
│     │  │                                   │  │         │
│     │  │                                   │  │         │
│     │  │                                   │  │         │
│     │  └───────────────────────────────────┘  │         │
│     │                                         │         │
│     │  Type              Auto-implement       │         │
│     │  ┌─────────────┐   ☐ Auto-implement     │         │
│     │  │ Feature   ▾ │                        │         │
│     │  └─────────────┘                        │         │
│     │                                         │         │
│     │               [Cancel]  [Submit]        │         │
│     └─────────────────────────────────────────┘         │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

### Idea Card Anatomy

```
┌────────────────────────────────────────────────────────┐
│  {type emoji} {title}  {🤖 auto if flagged}           │
│                                    {status badge} {age}│
│                                                        │
│  {raw_input, truncated to 200 chars}                   │
│  by {submitted_by}                                     │
│                                                        │
│  → Feature: {feature_id}        ← only if linked       │
│                                                        │
│  ▸ View Spec                    ← only if spec_md set  │
│                                                        │
│  [✅ Approve]  [❌ Reject]      ← only if spec-ready   │
└────────────────────────────────────────────────────────┘
```

---

## Data Requirements

### IdeaQueueItem Model

| Field | Type | JSON Key | Required | Default | Description |
|-------|------|----------|----------|---------|-------------|
| `ID` | `int` | `id` | Auto | Auto-increment | Unique database identifier |
| `ProjectID` | `string` | `project_id` | Yes | From context | Associated project |
| `Title` | `string` | `title` | **Yes** | — | Idea title (validated on submit) |
| `RawInput` | `string` | `raw_input` | No | `""` | Markdown description body |
| `IdeaType` | `string` | `idea_type` | No | `"feature"` | `"feature"` or `"bug"` |
| `Status` | `string` | `status` | No | `"pending"` | See status lifecycle below |
| `SpecMD` | `string` | `spec_md` | No | `""` | Agent-generated markdown spec (omitted from JSON if empty) |
| `AutoImplement` | `bool` | `auto_implement` | No | `false` | Whether to auto-implement on approval |
| `SubmittedBy` | `string` | `submitted_by` | No | `"human"` | Submitter identifier |
| `AssignedAgent` | `string` | `assigned_agent` | No | `""` | Agent currently processing (omitted from JSON if empty) |
| `FeatureID` | `string` | `feature_id` | No | `""` | Linked feature after approval (omitted from JSON if empty) |
| `CreatedAt` | `string` | `created_at` | Auto | `CURRENT_TIMESTAMP` | ISO 8601 creation time |
| `UpdatedAt` | `string` | `updated_at` | Auto | `CURRENT_TIMESTAMP` | ISO 8601 last-modified time |

### Status Lifecycle

```
pending ──→ processing ──→ spec-ready ──→ approved
                                    └──→ rejected
```

| Status | Display | Badge Class | Meaning |
|--------|---------|-------------|---------|
| `pending` | ⏳ Pending | `status-planning` (purple) | Awaiting agent spec generation |
| `processing` | ⚙️ Processing | `status-planning` (purple) | Agent is actively generating a spec |
| `spec-ready` | ✅ Spec Ready | `status-human-qa` (orange) | Spec generated; awaiting human review |
| `approved` | 👍 Approved | `status-done` (green) | Approved by human; may be linked to a feature |
| `rejected` | ❌ Rejected | `status-blocked` (red) | Rejected by human; archived |

### Idea Types

| Type | Emoji | Description |
|------|-------|-------------|
| `feature` | ✨ | New feature request (default) |
| `bug` | 🐛 | Bug report |

### API Endpoints

| Method | Path | Purpose | Request Body | Response |
|--------|------|---------|-------------|----------|
| `GET` | `/api/ideas` | List ideas | — | `IdeaQueueItem[]` |
| `GET` | `/api/ideas?status={s}` | Filter by status | — | `IdeaQueueItem[]` |
| `GET` | `/api/ideas?type={t}` | Filter by type | — | `IdeaQueueItem[]` |
| `POST` | `/api/ideas` | Submit new idea | `{title, raw_input?, idea_type?, auto_implement?, submitted_by?}` | `IdeaQueueItem` (201) |
| `GET` | `/api/ideas/{id}` | Get single idea | — | `IdeaQueueItem` |
| `POST` | `/api/ideas/{id}/spec` | Submit spec | `{spec_md}` | `IdeaQueueItem` |
| `POST` | `/api/ideas/{id}/approve` | Approve idea | `{notes?, feature_id?}` | `IdeaQueueItem` |
| `POST` | `/api/ideas/{id}/reject` | Reject idea | `{}` | `IdeaQueueItem` |

### Error Responses

| Condition | HTTP Status | Body |
|-----------|-------------|------|
| Missing title on POST | 400 | `{"error": "title is required"}` |
| Invalid JSON body | 400 | `{"error": "invalid request body"}` |
| Invalid idea ID | 400 | `{"error": "invalid idea ID"}` |
| Idea not found | 404 | `{"error": "idea not found"}` |
| Database error | 500 | Error message string |

---

## Interactions

### Submit Idea Flow

1. User clicks **"+ Submit Idea"** button
2. Modal overlay fades in (`display: none` → `display: flex`), centered on viewport
3. User fills in form fields:
   - **Title** (text input, required) — placeholder: "Idea title"
   - **Description** (textarea, 5 rows, optional) — placeholder: "Describe the idea (markdown supported)"
   - **Type** (select dropdown) — options: Feature (default), Bug
   - **Auto-implement** (checkbox) — unchecked by default
4. User clicks **"Submit"**:
   - Client validates title is non-empty (alert if blank)
   - `POST /api/ideas` with `{title, raw_input, idea_type, auto_implement}`
   - On success: modal closes, page re-renders via `App.navigate('ideas')`
5. Dismiss without submitting:
   - Click **"Cancel"** button, OR
   - Click the backdrop area outside the modal card

### Approve / Reject Flow

1. User scrolls to an idea with status `spec-ready`
2. (Optional) User expands "View Spec" details to review the generated specification
3. User clicks **"✅ Approve"**:
   - `POST /api/ideas/{id}/approve` with `{}`
   - Status transitions to `approved`
   - Page re-renders; card moves to collapsed "Approved" group
4. OR user clicks **"❌ Reject"**:
   - `POST /api/ideas/{id}/reject` with `{}`
   - Status transitions to `rejected`
   - Page re-renders; card moves to collapsed "Rejected" group

### Feature Navigation

- If an idea has a linked `feature_id`, the card shows "→ Feature: {id}"
- The feature ID is rendered as a `.clickable-feature` span
- Clicking navigates to the feature detail view

### Spec Viewing

- Ideas with a populated `spec_md` field show a `<details>` element
- Clicking the "View Spec" summary expands the collapsible to show rendered markdown
- The spec content is rendered inside a `.md-content` container

---

## State Handling

### Page Load

1. `renderIdeas()` is called when navigating to `#ideas`
2. `GET /api/ideas` fetches all ideas for the current project
3. Ideas are grouped into status buckets: `pending`, `processing`, `spec-ready`, `approved`, `rejected`
4. Counters are computed for the subtitle line
5. Cards are rendered per group with section headers
6. Event handlers are bound via `App._bindIdeasEvents()`

### Real-Time Updates

- The app maintains a WebSocket connection to `/ws`
- On any database change, the server pushes a notification
- The Ideas page re-renders by re-calling `renderIdeas()` with fresh data
- No partial DOM updates — full re-render on each change

### Modal State

| State | `#ideaModal` display | Trigger |
|-------|---------------------|---------|
| Closed (default) | `none` | Page load, Cancel click, backdrop click, successful submit |
| Open | `flex` | "+ Submit Idea" click |

- Modal state is purely DOM-driven (no JavaScript state variable)
- Form fields are not cleared between opens (browser-native behavior)
- Modal uses `z-index: 1000` to overlay all page content

### Loading & Error States

- **No explicit loading spinner** — the page renders once data is available
- **API errors** — caught in the submit handler; errors are logged to console
- **Empty state** — rendered when the ideas array is empty (💡 icon + message + hint)

### Status Badge Mapping

The renderer maps idea statuses to reusable badge CSS classes:

```javascript
const statusMap = {
  'pending':    'planning',     // purple
  'processing': 'planning',    // purple
  'spec-ready': 'human-qa',    // orange/warning
  'approved':   'done',        // green
  'rejected':   'blocked'      // red
};
```

---

## Accessibility Notes

### Current Implementation

- **Semantic HTML:** Uses `<button>`, `<label>`, `<select>`, `<input>`, `<details>`/`<summary>` elements appropriately
- **Label association:** Form labels are present for all inputs (Title, Description, Type) though not all use `for` attribute binding
- **Keyboard dismissal:** Modal can be closed via Cancel button (keyboard-accessible)
- **Collapsible sections:** `<details>` elements provide native keyboard expand/collapse
- **Color + text:** Status is conveyed through both color badges AND text labels (not color alone)
- **Emoji indicators:** Type (✨/🐛) and auto-implement (🤖) use emoji which are read by screen readers

### Recommendations for Improvement

- Add `aria-modal="true"` and `role="dialog"` to the modal overlay
- Trap focus within the modal when open (currently focus can escape to background)
- Add `aria-label` or `aria-labelledby` to the modal referencing the "Submit New Idea" heading
- Add `Escape` key handler to close the modal
- Ensure backdrop click target has `role="presentation"` or equivalent
- Add `aria-required="true"` to the title input
- Add `aria-live="polite"` to the subtitle counter region for screen reader announcements on updates
- Consider adding a confirmation dialog before approve/reject actions to prevent accidental clicks

### Color Contrast (Dark Theme)

| Element | Foreground | Background | Notes |
|---------|-----------|-----------|-------|
| Badge (planning) | `#bc8cff` | `#1f1d45` | Purple on dark purple — verify 4.5:1 ratio |
| Badge (human-qa) | `#d29922` | `#2a1f0c` | Orange on dark brown — verify contrast |
| Badge (done) | `#3fb950` | `#0d2818` | Green on dark green |
| Badge (blocked) | `#f85149` | `#2d1215` | Red on dark red |
| Card text | `#e6edf3` | `#1c2128` | Primary text on card background |
| Muted text (age, submitter) | `opacity: 0.5` | — | May fall below 4.5:1 — verify |

### Animations

- Card entrance: `fadeInUp 0.4s` — respects no preference currently; should honor `prefers-reduced-motion`
- Badge entrance: `scaleIn 0.3s` — same consideration
- Card hover: `translateY(-1px)` — subtle, unlikely to cause issues
