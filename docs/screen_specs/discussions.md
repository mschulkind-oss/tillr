# Discussions — Screen Specification

## Overview

The Discussions page provides a threaded conversation space for project stakeholders — humans and agents alike — to deliberate on design decisions, proposals, and open questions tied to specific features or the project at large. It is accessible from the sidebar via the 💬 icon and operates as two sub-views within a single route: a **list view** showing all discussions with status summary cards, and a **detail view** showing a single discussion with its full comment thread and reply form.

### Navigation

| Mechanism | Value |
|-----------|-------|
| Sidebar label | Discussions |
| Sidebar icon | 💬 |
| URL hash (list) | `#discussions` |
| URL hash (detail) | `#discussions` with `navContext.id` set |

### Key Characteristics

- **No pagination** — all discussions are fetched and rendered in a single request.
- **Inline form** — new discussions are created via an inline toggle form, not a modal.
- **Read-only status** — the web UI displays discussion status but does not provide controls to change it; status transitions happen via the CLI.
- **Comment types are API-only** — the reply form always creates `comment`-type replies; other types (`proposal`, `approval`, `objection`, `revision`, `decision`) are set via the CLI or direct API calls.
- **Markdown support** — bold, inline code, fenced code blocks, and links are rendered in both discussion bodies and replies.

---

## User Roles & Personas

| Persona | Description | Typical Actions |
|---------|-------------|-----------------|
| **Human Product Owner** | Drives decisions, reviews proposals, resolves discussions | Browse discussions, read threads, post replies, review decisions |
| **AI Agent** | Creates discussions during research/planning cycles, posts proposals | Create discussions (via CLI → appears in UI), add typed comments (proposal, decision) |
| **Developer** | Participates in technical design discussions | Read threads, reply with implementation context, follow linked features |
| **Reviewer** | Reviews proposals and objections before a decision is made | Scan open discussions, read comment threads, post approval/objection via CLI |

---

## User Stories

### US-1: View Discussion List

> **As a** product owner  
> **I want to** see all project discussions at a glance  
> **So that** I can quickly identify which conversations need my attention.

**Given** the user navigates to the Discussions page  
**When** the page loads  
**Then** the system fetches `GET /api/discussions` and renders:
1. A page header with the title "Discussions" and a "＋ New Discussion" button.
2. Four stat cards in a grid showing counts by status: Open (🟢), Resolved (🔵), Merged (🟣), Closed (⚪).
3. A vertical list of discussion items ordered by most recently updated first (`updated_at DESC`).

**Each list item displays:**
- A colored avatar circle with the author's initials (deterministic color from name hash).
- The discussion title alongside a status badge (`open` | `resolved` | `merged` | `closed`).
- A body preview truncated to 100 characters with ellipsis.
- Metadata row: author name, reply count badge (💬 N), feature tag (if linked), and relative timestamp ("2h ago").

---

### US-2: Filter Discussions

> **As a** developer  
> **I want to** filter discussions by status or linked feature  
> **So that** I can focus on relevant conversations.

**Given** the discussion list is displayed  
**When** the API is called with query parameters `?status={status}` and/or `?feature={featureId}`  
**Then** only discussions matching the filter criteria are returned and rendered.

*Note: The current UI does not expose filter controls directly; filtering is available via the API and via feature-linked discussion lists on the Features detail page.*

---

### US-3: Create a New Discussion

> **As a** product owner  
> **I want to** start a new discussion thread  
> **So that** I can raise a topic for the team to deliberate on.

**Given** the user is on the discussion list view  
**When** the user clicks the "＋ New Discussion" button  
**Then** an inline form appears below the stat cards (the button toggles visibility) and the Title input receives focus.

**Form fields:**

| Field | Element | Required | Default | Notes |
|-------|---------|----------|---------|-------|
| Title | `<input type="text">` | Yes | — | Placeholder: "Discussion title…" |
| Body | `<textarea rows="4">` | No | — | Placeholder: "Describe what you want to discuss…"; hint text: "Supports \*\*bold\*\*, \`code\`, and \[links\](url)" |
| Link to Feature | `<select>` | No | "None" | Populated with all project features |
| Author | `<input type="text">` | No | `"human"` | Identifies who created the discussion |

**Actions:**
- **Create Discussion** — `POST /api/discussions` with `{ title, body, feature_id, author }`. On success (201), navigates back to the refreshed list view.
- **Cancel** — hides the form without submitting.

**Given** the user submits a form with an empty title  
**When** the POST is sent  
**Then** the server returns an error (title is required) and the discussion is not created.

---

### US-4: View Discussion Detail

> **As a** stakeholder  
> **I want to** read the full discussion and all replies  
> **So that** I can understand the conversation history and context.

**Given** the user clicks a discussion item in the list  
**When** `App.navigateTo('discussions', discussionId)` is called  
**Then** the detail view renders with data from `GET /api/discussions/{id}`:

1. **Back button** — returns to the list view.
2. **Header section:**
   - Discussion ID number and title with status badge.
   - Author avatar (colored initials), author name, and creation timestamp.
   - Feature tag linking to the associated feature (if any; click navigates to that feature).
   - Total reply count.
3. **Body section** — the discussion body rendered as markdown in a card container (`.disc-detail-body`).
4. **Thread section** (`.disc-thread`) — titled "Thread · N replies":
   - Each reply displays: avatar, author name, comment type badge, relative timestamp, and markdown-rendered content.
   - If no replies exist, an empty state message is shown.
5. **Reply form** — at the bottom (see US-5).

---

### US-5: Reply to a Discussion

> **As a** developer  
> **I want to** post a reply to an existing discussion  
> **So that** I can contribute my perspective to the conversation.

**Given** the user is viewing a discussion's detail page  
**When** the user fills in the reply form and clicks "Post Reply"  
**Then** the system sends `POST /api/discussions/{id}/replies` with `{ body, author }`.

**Reply form fields:**

| Field | Element | Required | Default | Notes |
|-------|---------|----------|---------|-------|
| Reply body | `<textarea rows="3">` | Yes | — | Placeholder: "Write a reply…"; markdown hint shown |
| Author | `<input type="text">` | No | `"human"` | Pre-filled |

**On success (201):**
- The detail view re-renders with fresh data (re-fetched from API).
- Event handlers are re-bound.
- The page scrolls the reply form into view.

**On submit:**
- The submit button is disabled during the POST to prevent duplicate submissions.

*Note: The comment type is hardcoded to `"comment"` by the web UI. To create typed comments (proposal, approval, objection, revision, decision), use the CLI.*

---

### US-6: Navigate from Discussion to Linked Feature

> **As a** developer  
> **I want to** click a feature tag on a discussion  
> **So that** I can quickly view the feature that the discussion relates to.

**Given** a discussion (in list or detail view) has a linked feature  
**When** the user clicks the feature tag pill  
**Then** the app navigates to the Features page for that feature (`App.navigateTo('features', featureId)`).

**Given** the user clicks the feature tag  
**When** the click event fires  
**Then** event propagation is stopped so the list item click handler does not also fire.

---

### US-7: View Discussions from Feature Detail

> **As a** product owner  
> **I want to** see all discussions linked to a specific feature  
> **So that** I can review the conversation history for that feature.

**Given** the user is viewing a feature's detail page  
**When** the feature has linked discussions  
**Then** a "Discussions" section (`.feature-discussions-section`) renders with a list of linked discussion items, each showing title, status badge, and reply count.

---

### US-8: View Comment Type Badges

> **As a** reviewer  
> **I want to** see what type each comment is (proposal, approval, objection, etc.)  
> **So that** I can quickly identify the nature of each contribution.

**Given** a discussion thread contains comments with different types  
**When** the detail view renders  
**Then** each comment displays a colored type badge:

| Type | Badge Color | Semantic Meaning |
|------|-------------|------------------|
| `comment` | Gray (`var(--bg-tertiary)`) | General discussion |
| `proposal` | Blue (`var(--accent)`) | A concrete suggestion |
| `approval` | Green (`var(--success)`) | Agreement / sign-off |
| `objection` | Red (`var(--danger)`) | Disagreement / concern |
| `revision` | Yellow (`var(--warning)`) | Suggested modification |
| `decision` | Purple (`var(--purple)`) | Final resolution |

---

### US-9: View Discussion Status Badges

> **As a** product owner  
> **I want to** see each discussion's status at a glance  
> **So that** I know which discussions are still active vs. resolved.

**Given** discussions exist in different statuses  
**When** the list or detail view renders  
**Then** each discussion displays a colored status badge:

| Status | Badge Style | Stat Card Icon |
|--------|-------------|----------------|
| `open` | Green bg `#0d2818`, text `var(--success)` | 🟢 |
| `resolved` | Blue bg `#0c2d6b`, text `var(--accent)` | 🔵 |
| `merged` | Purple bg `#1f1d45`, text `var(--purple)` | 🟣 |
| `closed` | Gray bg `var(--bg-tertiary)`, text `var(--text-muted)` | ⚪ |

---

### US-10: Markdown Rendering in Discussions

> **As a** participant  
> **I want to** use basic markdown formatting in discussions and replies  
> **So that** I can clearly communicate code snippets, links, and emphasis.

**Given** a discussion body or reply contains markdown syntax  
**When** it is rendered on screen  
**Then** the following transformations are applied:

| Markdown Syntax | Rendered HTML | Example |
|-----------------|---------------|---------|
| `**text**` | `<strong>text</strong>` | **bold text** |
| `` `code` `` | `<code>code</code>` | inline code |
| `[text](https://url)` | `<a href="https://url" target="_blank" rel="noopener">text</a>` | clickable link (HTTP/HTTPS only) |
| ` ``` ` fenced block | `<pre><code>...</code></pre>` | code block |
| Blank line | `<p>&nbsp;</p>` | paragraph break |

Text is HTML-escaped before markdown processing to prevent XSS. Links open in a new tab with `rel="noopener"`.

---

### US-11: Author Avatars

> **As a** reader  
> **I want to** visually distinguish different authors  
> **So that** I can follow who said what in a conversation.

**Given** a discussion or reply has an author name  
**When** the item renders  
**Then** an avatar circle is displayed with:
- The author's initials (first letter of first and last name, e.g., "JS" for "John Smith"; single letter for single-word names).
- A deterministic background color derived from a hash of the author name (one of 10 predefined colors, consistent across renders).
- Size: 36px for list items, 32px for reply avatars.

---

### US-12: Relative Timestamps

> **As a** user  
> **I want to** see how recently discussions and replies were posted  
> **So that** I can gauge the recency and activity level of conversations.

**Given** a discussion or reply has a created/updated timestamp  
**When** the item renders  
**Then** the timestamp is displayed in relative format:
- "just now" (< 1 minute)
- "Nm ago" (minutes)
- "Nh ago" (hours)
- "Nd ago" (days)
- "Nw ago" (weeks)

---

## Screen Layout

### List View

```
┌──────────────────────────────────────────────────────────┐
│  Discussions                          [＋ New Discussion] │
├──────────────────────────────────────────────────────────┤
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐   │
│  │ 🟢  3    │ │ 🔵  1    │ │ 🟣  0    │ │ ⚪  2    │   │
│  │ Open     │ │ Resolved │ │ Merged   │ │ Closed   │   │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘   │
│                                                          │
│  ┌─ Inline New Discussion Form (hidden by default) ────┐ │
│  │ Title:    [________________________]                 │ │
│  │ Body:     [________________________]                 │ │
│  │           [________________________]                 │ │
│  │           Supports **bold**, `code`, and [links]     │ │
│  │ Feature:  [ None               ▾ ]                   │ │
│  │ Author:   [ human_________________]                  │ │
│  │           [Create Discussion] [Cancel]               │ │
│  └──────────────────────────────────────────────────────┘ │
│                                                          │
│  ┌──────────────────────────────────────────────────────┐ │
│  │ (JS) RFC: Caching Strategy            [open]        │ │
│  │      Propose Redis for session cache, local LR…     │ │
│  │      John Smith · 💬 4 · ▪ cache-layer · 2h ago     │ │
│  ├──────────────────────────────────────────────────────┤ │
│  │ (OA) Database migration approach      [resolved]    │ │
│  │      Should we use goose or atlas for migrat…       │ │
│  │      onboarding-agent · 💬 7 · ▪ db-layer · 1d ago  │ │
│  └──────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────┘
```

### Detail View

```
┌──────────────────────────────────────────────────────────┐
│  [← Back]                                                │
│                                                          │
│  #3 RFC: Caching Strategy                   [open]       │
│  (JS) John Smith · Created 2h ago                        │
│  ▪ cache-layer · 💬 4 replies                             │
│                                                          │
│  ┌── Body ──────────────────────────────────────────────┐ │
│  │ Propose Redis for session cache, local LRU for       │ │
│  │ hot paths. Need to decide on eviction policy.        │ │
│  │                                                      │ │
│  │ See **RFC-042** for background.                      │ │
│  └──────────────────────────────────────────────────────┘ │
│                                                          │
│  Thread · 4 replies                                      │
│  ────────────────────────────────────────────────────     │
│  (OA) onboarding-agent  [proposal]  1h ago               │
│       I suggest we use a TTL-based eviction with a       │
│       max of 10k entries per shard.                      │
│                                                          │
│  (DK) dev-kim            [comment]   45m ago             │
│       That works. We should also consider write-through  │
│       for consistency.                                   │
│                                                          │
│  (JS) John Smith         [approval]  20m ago             │
│       Approved. Let's go with TTL + write-through.       │
│                                                          │
│  (OA) onboarding-agent  [decision]  5m ago               │
│       Decision: Redis with TTL eviction, write-through   │
│       caching, max 10k entries per shard.                │
│                                                          │
│  ┌── Reply ─────────────────────────────────────────────┐ │
│  │ [Write a reply…                                     ]│ │
│  │ Supports **bold**, `code`, and [links](url)          │ │
│  │ Author: [human_____]                                 │ │
│  │ [Post Reply]                                         │ │
│  └──────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────┘
```

---

## Data Requirements

### API Endpoints

| Method | Path | Purpose | Query Params |
|--------|------|---------|--------------|
| `GET` | `/api/discussions` | List discussions | `?feature={id}`, `?status={status}` |
| `POST` | `/api/discussions` | Create discussion | — |
| `GET` | `/api/discussions/{id}` | Get discussion + comments | — |
| `POST` | `/api/discussions/{id}/replies` | Add reply | — |

### GET /api/discussions — Response

```json
[
  {
    "id": 3,
    "project_id": "my-project",
    "feature_id": "cache-layer",
    "title": "RFC: Caching Strategy",
    "body": "Propose Redis for session cache...",
    "status": "open",
    "author": "John Smith",
    "created_at": "2025-01-15T10:30:00Z",
    "updated_at": "2025-01-15T12:30:00Z",
    "comment_count": 4
  }
]
```

### POST /api/discussions — Request

```json
{
  "title": "RFC: Caching Strategy",
  "body": "Propose Redis for session cache...",
  "feature_id": "cache-layer",
  "author": "human"
}
```

- `title` is required. `body`, `feature_id`, and `author` are optional.
- `author` defaults to `"human"` if omitted.
- Returns `201 Created` with the full Discussion object.

### GET /api/discussions/{id} — Response

```json
{
  "id": 3,
  "project_id": "my-project",
  "feature_id": "cache-layer",
  "title": "RFC: Caching Strategy",
  "body": "Propose Redis for session cache...",
  "status": "open",
  "author": "John Smith",
  "created_at": "2025-01-15T10:30:00Z",
  "updated_at": "2025-01-15T12:30:00Z",
  "comment_count": 4,
  "comments": [
    {
      "id": 7,
      "discussion_id": 3,
      "author": "onboarding-agent",
      "content": "I suggest we use a TTL-based eviction...",
      "parent_id": 0,
      "type": "proposal",
      "created_at": "2025-01-15T11:30:00Z"
    }
  ]
}
```

### POST /api/discussions/{id}/replies — Request

```json
{
  "body": "That works. We should also consider write-through.",
  "author": "dev-kim"
}
```

- `body` is required. `author` defaults to `"human"`.
- Comment type is hardcoded to `"comment"` by the server.
- Returns `201 Created` with the DiscussionComment object.
- The parent discussion's `updated_at` is bumped on every new reply.

### Data Models

**Discussion:**

| Field | Type | Notes |
|-------|------|-------|
| `id` | int | Auto-increment primary key |
| `project_id` | string | Parent project identifier |
| `feature_id` | string | Optional linked feature (omitted if empty) |
| `title` | string | Required |
| `body` | string | Optional; supports markdown |
| `status` | string | `open` · `resolved` · `merged` · `closed` |
| `author` | string | Creator name |
| `created_at` | string | ISO 8601 |
| `updated_at` | string | ISO 8601; bumped on new replies |
| `comment_count` | int | Computed via subquery |
| `comments` | []Comment | Populated on detail fetch only |

**DiscussionComment:**

| Field | Type | Notes |
|-------|------|-------|
| `id` | int | Auto-increment primary key |
| `discussion_id` | int | Parent discussion |
| `author` | string | Comment author |
| `content` | string | Supports markdown |
| `parent_id` | int | For nested replies (0 = top-level) |
| `type` | string | `comment` · `proposal` · `approval` · `objection` · `revision` · `decision` |
| `created_at` | string | ISO 8601 |

---

## Interactions

### List View Interactions

| Element | Event | Behavior |
|---------|-------|----------|
| "＋ New Discussion" button | Click | Toggles inline form visibility; focuses title input when shown |
| Discussion list item | Click | Navigates to detail view (`App.navigateTo('discussions', id)`) |
| Feature tag in list item | Click | Navigates to feature detail (`App.navigateTo('features', featureId)`); stops event propagation |
| "Create Discussion" button | Click | Validates title is non-empty; POSTs to `/api/discussions`; on 201, refreshes list |
| "Cancel" button | Click | Hides the inline form without submitting |
| Stat cards | Hover | Subtle lift animation (`translateY(-2px)`) with colored box-shadow |

### Detail View Interactions

| Element | Event | Behavior |
|---------|-------|----------|
| "← Back" button | Click | Navigates back to list view (`App.navigateTo('discussions')`) |
| Feature tag in header | Click | Navigates to feature detail |
| "Post Reply" button | Click | POSTs reply to `/api/discussions/{id}/replies`; button is disabled during request; on success, re-renders detail view and scrolls reply form into view |

### Keyboard Interactions

- Standard form navigation (Tab, Shift+Tab between fields).
- Enter in the title field does not submit (it's a single-line input within a multi-field form).

---

## State Handling

### Loading States

| State | Behavior |
|-------|----------|
| Initial page load | Fetches discussions and features concurrently; renders list on completion |
| Detail view load | Fetches single discussion by ID; renders header/body/thread on completion |
| Form submission | Submit button is disabled during POST to prevent double-submission |

### Empty States

| Context | Display |
|---------|---------|
| No discussions exist | Stat cards show all zeros; list area is empty |
| Discussion has no replies | Thread section shows an empty state message |
| Discussion has no body | Body section renders as empty (no card shown if body is falsy) |
| Discussion has no linked feature | Feature tag is omitted from metadata row |

### Error States

| Condition | Behavior |
|-----------|----------|
| POST fails (network error) | Console error logged; UI does not navigate away |
| Discussion ID not found | API returns 404; detail view may show error or empty |
| Missing required `title` on create | Server rejects; 400 response |

### Data Freshness

- Discussion list is fetched fresh on every navigation to the Discussions page.
- Detail view re-fetches after posting a reply, ensuring the thread is up-to-date.
- WebSocket (`/ws`) pushes live updates when the underlying SQLite database changes; the Discussions page re-renders on relevant events.

### View Routing

The `renderDiscussions()` entry point checks `App._navContext.id`:
- **If set** → renders detail view for that discussion ID.
- **If not set** → renders the discussion list.

Navigation between list and detail is handled entirely client-side via `App.navigateTo()` which sets `_navContext` and triggers a re-render.

---

## Accessibility Notes

### Semantic Structure

- Stat card icons use `aria-hidden="true"` since they are decorative emoji.
- Form fields have associated `<label>` elements with `for` attributes matching input IDs.
- The page uses heading hierarchy for section titles (discussion title, "Thread · N replies").

### Color & Contrast

- Status badges and comment type badges rely on both color and text to convey meaning (not color alone).
- Dark theme is the default; light theme is fully supported via `[data-theme="light"]` overrides on all status/type badge colors and stat card gradients.
- Avatar colors are chosen from a palette of 10 predefined colors designed for contrast against white text initials.

### Interactive Elements

- All clickable items (list items, buttons, feature tags) have `cursor: pointer` and visible hover/focus states (border color change, box-shadow).
- The new discussion form auto-focuses the title input when toggled open.
- The reply form scrolls into view after a successful post.

### Current Gaps

- Discussion list items are `<div>` elements with click handlers rather than `<a>` or `<button>` — they are not keyboard-navigable without Tab/Enter support being added.
- No ARIA live regions for announcing new replies or form submission results.
- No skip-link or landmark roles specific to the Discussions page.
- Stat cards do not have `role="status"` or ARIA labels describing their values.

---

## Appendix: CSS Class Reference

### Layout Classes

| Class | Purpose |
|-------|---------|
| `.disc-list` | Flex column container for list items (gap: 8px) |
| `.disc-list-item` | Individual discussion card in list (flex row, hover effects) |
| `.disc-detail-header` | Detail view header container |
| `.disc-detail-title` | Detail title row (flex, 1.3rem, bold) |
| `.disc-detail-meta` | Detail metadata row (flex, small font) |
| `.disc-detail-body` | Markdown body card container |
| `.disc-thread` | Thread section wrapper |
| `.disc-thread-title` | Thread heading with reply count |
| `.disc-reply` | Individual reply container (flex row, gap 12px) |

### Component Classes

| Class | Purpose |
|-------|---------|
| `.disc-avatar` | 36px colored circle with initials |
| `.disc-reply-avatar` | 32px colored circle (smaller, for replies) |
| `.disc-reply-badge` | Pill showing reply count (💬 N) |
| `.disc-feature-tag` | Clickable pill linking to associated feature |
| `.disc-list-preview` | Truncated body preview with ellipsis |
| `.disc-list-meta` | Metadata row in list items |

### Status Badge Classes

| Class | Appearance |
|-------|------------|
| `.disc-status-open` | Green background, green text |
| `.disc-status-resolved` | Blue background, blue text |
| `.disc-status-merged` | Purple background, purple text |
| `.disc-status-closed` | Gray background, muted text |

### Comment Type Badge Classes

| Class | Appearance |
|-------|------------|
| `.disc-type-comment` | Gray background, muted text |
| `.disc-type-proposal` | Blue background, accent text |
| `.disc-type-approval` | Green background, success text |
| `.disc-type-objection` | Red background, danger text |
| `.disc-type-revision` | Yellow background, warning text |
| `.disc-type-decision` | Purple background, purple text |

### Form Classes

| Class | Purpose |
|-------|---------|
| `.disc-form` | Card-styled form container (16px 20px padding) |
| `.disc-form-title` | Form heading (0.9rem, bold) |
| `.disc-form-group` | Field group with label (margin-bottom 12px) |
| `.disc-form-hint` | Hint text below textarea (0.7rem, muted) |
| `.disc-form-actions` | Button row (flex, gap 8px) |
| `.disc-form-submit` | Primary action button (accent bg, disabled state) |
| `.disc-form-cancel` | Secondary action button (tertiary bg, border) |
| `.disc-new-btn` | "＋ New Discussion" button (accent bg) |
| `.disc-back-btn` | "← Back" button (tertiary bg, margin-bottom 12px) |

### Feature-Linked Section Classes

| Class | Purpose |
|-------|---------|
| `.feature-discussions-section` | Container on feature detail page |
| `.feature-discussions-header` | Section heading (uppercase, muted) |
| `.feature-discussions-list` | Flex column list of linked discussions |
