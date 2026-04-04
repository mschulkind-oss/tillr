# How Should User Stories Live in Tillr?

Four options, explored through user stories. Each story shows someone
actually using the approach — what works, what breaks, and the technical
cost. Then a hybrid option that steals the best parts.

---

## Option A: Stories as First-Class Entities

Stories get their own table, CLI commands, and UI page. Features link
to stories. The story is the unit of human intent; features are the
unit of agent work.

### Story: Val writes a story, then derives features

**Context:** Val wants to add OAuth login. Today she'd jump straight to
`tillr feature add "oauth-login"`. With Option A, she starts with intent.

1. Val writes the story:
   ```
   tillr story create "oauth-login" \
     --persona "Sam (solo dev)" \
     --goal "Log in with Google so I don't need another password" \
     --spec-file docs/stories/oauth-login.md
   ```

   The spec file is rich markdown — the same format as the user stories
   document, with context, exact steps, what to check, what would trip
   them up.

2. She derives features from the story:
   ```
   tillr feature add "google-oauth-provider" --story oauth-login \
     --spec "Configure Google OAuth app, add callback endpoint"
   tillr feature add "oauth-login-button" --story oauth-login \
     --spec "Add 'Sign in with Google' button to login page"
   tillr feature add "oauth-account-linking" --story oauth-login \
     --spec "Link OAuth identity to existing email account"
   ```

3. In the UI, the story page shows:
   - The full narrative (persona, context, steps, what to check)
   - All linked features and their statuses
   - A progress bar: 0/3 features done
   - The human QA checklist derived from the story's "what to check"
     section

4. When an agent claims `google-oauth-provider`, the claim response
   includes the parent story context — not just the feature spec, but
   WHY this feature exists and how it fits into the user's journey.

5. When all 3 features reach human-qa, Val's inbox shows:
   ```
   Story: OAuth Login (Sam — solo dev)
     3 features ready for review as a group

     What to check:
     - Click "Sign in with Google" on the login page
     - Complete the Google OAuth flow
     - Verify you land on the dashboard, logged in
     - Try with an existing account that has the same email
     - Log out and log back in with Google — session persists

     [Approve Story] [Reject Story] [Review Individual Features]
   ```

   The QA items come from the story narrative, not the feature specs.
   Val is testing the USER EXPERIENCE, not the implementation details.

**What works well:**
- Stories carry intent that features alone can't. "Add OAuth callback
  endpoint" is meaningless to Lisa (non-technical PM). "Log in with
  Google so I don't need another password" is clear.
- QA checklists write themselves from the story narrative — they're
  already in human language because stories are human-focused.
- Agents get better context — they know WHY they're building something,
  not just WHAT.
- Grouping features under a story means QA can happen at the story
  level — "does this whole flow work?" not "does this individual
  endpoint return 200?"

**What breaks:**
- Not everything maps to a user story. Refactoring, performance work,
  infrastructure changes, bug fixes — these are technical work with no
  user-facing persona. Forcing them into stories creates fake stories
  ("As a developer, I want the build to be faster") that nobody reads.
- Two-level hierarchy (story → features) adds overhead. For simple
  features ("fix the 404 on the settings page"), creating a story
  first is busywork.
- The story spec file is a markdown document that lives outside the DB.
  Now you have two sources of truth — the spec in the file and the
  metadata in the DB. They can diverge.

**Technical cost:**
- New `stories` table: id, persona, goal, narrative (or path to spec
  file), status, created_at
- New `story_id` FK on features table (nullable — not all features
  have stories)
- New CLI commands: `tillr story create/list/show/edit`
- New UI page: story list, story detail with linked features
- Story-level QA flow in the inbox
- Migration: ~200 lines of Go, ~400 lines of React

---

## Option B: Stories as Workstream Context

No new entities. Workstreams ARE the stories. The workstream description
becomes the narrative. Features within a workstream serve that narrative.
The story document lives as a markdown file linked to the workstream.

### Story: Kenji organizes work around user journeys

**Context:** Kenji is planning the next quarter. He has 4 user journeys
he wants to deliver: first-time setup, daily QA review, agent onboarding,
multi-project management.

1. He creates workstreams that ARE the stories:
   ```
   tillr workstream create "First-Time Setup Experience" \
     --description "Sam (solo dev) discovers tillr and gets it running..."
   ```

   He writes the full narrative in a markdown doc and attaches it:
   ```
   tillr attach add workstream first-time-setup-experience \
     --file docs/stories/first-time-setup.md
   ```

2. He adds features that serve this story:
   ```
   tillr feature add "empty-state-guidance" \
     --workstream first-time-setup-experience \
     --spec "When dashboard is empty, show guided setup steps..."
   tillr feature add "connect-agent-wizard" \
     --workstream first-time-setup-experience \
     --spec "Step-by-step flow to connect Claude Code..."
   ```

3. The workstream page shows the narrative at the top, then features
   below. The human inbox on the workstream page pulls QA items from
   the narrative's "what to check" section.

4. When reviewing, Val sees the workstream inbox:
   ```
   Workstream: First-Time Setup Experience
   Persona: Sam — solo dev, side project

   Features ready for review (2):
     empty-state-guidance     Priority 8
     connect-agent-wizard     Priority 7

   Story-level checks:
     - Run tillr init in a fresh directory. Is the output clear?
     - Open the UI with an empty DB. Do you know what to do next?
     - Follow the "connect agent" wizard. Does it actually work?
   ```

**What works well:**
- No new entities — uses the existing workstream model. Zero migration
  cost. Zero new CLI commands.
- Workstreams already have descriptions, attachments, feature lists,
  and inbox. The story just makes the description richer.
- Natural grouping — a workstream IS a coherent unit of user value.
  "Authentication" is a workstream, and "Sam wants to log in" is the
  story that drives it.

**What breaks:**
- Workstreams are too coarse. "Authentication" might have 3 user
  stories: login, signup, password reset. Cramming them all into one
  workstream muddles the intent. Splitting into 3 workstreams creates
  too many small workstreams.
- Workstreams have operational meaning (scoping, agent assignment,
  progress tracking). Conflating them with stories means the story
  structure dictates the operational structure, which may not align.
  You might want agents scoped to "backend" workstreams but stories
  organized as "user journeys."
- Infrastructure and refactoring work doesn't fit. You'd need an
  "Internal" workstream that has no story, which breaks the pattern.
- The story document is an attachment, not a first-class field. It
  might be missed. The connection between the story and the QA
  checklist is manual — someone has to extract the checks from the
  narrative.

**Technical cost:**
- Near zero. Maybe add a `narrative` or `story_file` field to
  workstreams, or rely on existing attachments.
- The real cost is in the inbox UI — extracting QA items from the
  story document and surfacing them inline. That's ~200 lines of
  React, doable.

---

## Option C: Stories as Spec Templates

Stories don't live in tillr at all. They live as markdown docs in the
repo. But tillr enforces that every feature has a spec, and the spec
format includes story context: who is this for, why do they need it,
and what should a human check.

### Story: Agent claims a feature and gets everything it needs

**Context:** An agent runs `tillr agent next`. Today it gets a feature ID,
name, description, and spec (if one exists). Often the spec is thin or
missing. The agent guesses.

With Option C, every claimable feature has a structured spec:

```markdown
## Who is this for?
Val — developer who's lost track of what needs attention.

## What's the problem?
32 features are in human-qa and she doesn't know where to start.
No global inbox, no test plans, no priority ordering.

## What does done look like?
A global inbox page that shows all items needing human action across
all workstreams, ordered by priority, with inline approve/reject and
a "what to check" summary per item.

## Agent implementation notes
- New React page at /inbox
- API endpoint: GET /api/inbox aggregates across workstreams
- Pull QA checklist from feature's test_plan field
- Inline approve/reject reuses existing QA mutation

## Human QA checklist
- [ ] Open /inbox with features in human-qa across 2+ workstreams
- [ ] Items are ordered by priority (highest first)
- [ ] Each item shows a plain-English summary of what to check
- [ ] Approve a feature — it moves to done, disappears from inbox
- [ ] Reject a feature — prompted for notes, feature goes back
- [ ] Inbox is empty when nothing needs attention
```

1. Val enforces this format:
   ```
   tillr config set require-spec true
   tillr config set spec-template structured
   ```

   Now `tillr feature add` without `--spec` creates the feature in draft
   and warns: "Feature has no spec — agents can't claim it until a spec
   is added."

   The queue page shows these features grayed out with "Needs spec."

2. Val writes specs using the template. She can do this from the CLI:
   ```
   tillr feature edit global-inbox --spec-file docs/specs/global-inbox.md
   ```
   Or from the UI (if we add a spec editor).

3. When the agent claims, the full spec is included in the claim response.
   The agent knows who it's building for, what the problem is, what done
   looks like, and what the human will check.

4. When the feature reaches human-qa, Val's inbox extracts the "Human QA
   checklist" section from the spec and displays it inline:
   ```
   global-inbox                    Priority 9
     - Open /inbox with features in human-qa across 2+ workstreams
     - Items are ordered by priority (highest first)
     - Each item shows a plain-English summary of what to check
     [Approve] [Reject]
   ```

**What works well:**
- No new entities, no new tables, no migration. Just a structured spec
  format and validation.
- The spec IS the source of truth. No divergence between a story doc
  and a feature. The story context lives in the spec.
- Agents get everything from one place — `tillr feature show --json`.
- Human QA checklists are part of the spec, written by the human at
  spec time, not generated after the fact. The human decides what to
  check BEFORE the agent builds, not after.
- Works for all feature types — user-facing features get the full
  "who/what/why" template. Infrastructure work gets a simpler template
  ("what does done look like" + "QA checklist" only).

**What breaks:**
- No grouping. Each feature is standalone. You can't see "here are 3
  features that together deliver the OAuth login experience." That
  grouping only exists if the features share a workstream, which is
  Option B.
- Spec-writing becomes a bottleneck. If Val has 40 features that need
  specs before agents can claim them, she's spending her time writing
  specs instead of reviewing output. The cure is worse than the disease.
- The "who is this for?" context is duplicated across every feature in
  the same area. The OAuth provider, the login button, and the account
  linking feature all say "Val wants to log in with Google." That
  repetition is a sign that the context belongs at a higher level (story
  or workstream), not on each feature.
- Template enforcement is config, not schema. A typo in the template
  section headers means the QA extraction fails silently.

**Technical cost:**
- `require_spec` config field, spec validation on claim
- Spec template with defined sections (could be structured YAML
  frontmatter in the markdown, or just convention with header parsing)
- QA checklist extraction from spec markdown (~100 lines of Go for
  parsing, ~150 lines of React for display)

---

## Option D: Stories Drive Workstreams, Specs Drive Features

A hybrid. Two levels of narrative:

1. **Workstream-level stories** define the user journey and the human QA
   criteria. These are the "why" and "how to verify."
2. **Feature-level specs** define the implementation and the agent QA
   criteria. These are the "what" and "how to build."

The inbox pulls QA items from the workstream story (human-level checks)
and the feature spec (technical-level checks), presenting them separately.

### Story: Val sets up a workstream with a story, then specs features

1. Val creates a workstream with a story:
   ```
   tillr workstream create "Human QA Experience" \
     --story-file docs/stories/human-qa-experience.md
   ```

   The story file:
   ```markdown
   # Human QA Experience

   ## Persona
   Val — developer managing 4 workstreams, 60 features, agents running
   periodically. She opens tillr once a day for 15 minutes.

   ## Current pain
   32 features in human-qa. No idea where to start. No test plans.
   No guided flow. She clicks into each workstream, scans features,
   tries to figure out what changed and whether it's right.

   ## Desired experience
   Val opens tillr. First thing she sees is her inbox: everything
   across all workstreams that needs a human decision, highest priority
   first. Each item tells her what to check in plain English. She
   approves or rejects inline. 15 minutes, done.

   ## How to verify (human QA)
   - Open tillr with features in human-qa across multiple workstreams
   - The inbox is the first thing you see (or one click away)
   - Items are ordered by priority
   - Each item has a plain-English "what to check" summary
   - Approve: feature moves to done, disappears from inbox
   - Reject: prompted for notes, feature returns to agent
   - Blocked features show what's blocking and if action is needed
   - When inbox is empty, it says so clearly
   - The whole flow takes under 5 minutes for 5 items

   ## Out of scope
   - Notifications (separate story)
   - Cross-project inbox (separate story, depends on daemon work)
   ```

2. She adds features with specs:
   ```
   tillr feature add "global-inbox-page" \
     --workstream human-qa-experience \
     --spec "React page at /inbox. GET /api/inbox returns items
   across workstreams. Items grouped by type (QA, blocked, rejected,
   needs-spec). Each item shows feature name, workstream, priority,
   age, and summary. Inline approve/reject buttons."

   tillr feature add "human-qa-checklist-field" \
     --workstream human-qa-experience \
     --spec "Add qa_checklist text field to features table. CLI:
   tillr feature edit <id> --qa-checklist 'item1|item2|item3'.
   API returns it in feature JSON. UI renders as checkable list
   on feature detail and in inbox."

   tillr feature add "spec-required-for-queue" \
     --workstream human-qa-experience \
     --spec "Features without specs can't be claimed. Queue shows
   them grayed out with 'Needs spec' label. tillr agent claim
   returns error if spec is empty."
   ```

3. Agents claim features and get the feature spec (implementation
   details) plus the workstream story (user context). The agent knows
   both WHAT to build and WHO it's for.

4. When all features reach human-qa, Val's inbox groups them by
   workstream/story:

   ```
   Human QA Experience (Val — daily QA review)        3 items

     Story-level checks:
     - [ ] Inbox is the first thing you see
     - [ ] Items ordered by priority
     - [ ] Approve moves to done, disappears
     - [ ] Reject prompts for notes
     - [ ] Whole flow under 5 minutes for 5 items

     Features:
       global-inbox-page            ready    [Approve] [Reject]
       human-qa-checklist-field     ready    [Approve] [Reject]
       spec-required-for-queue      ready    [Approve] [Reject]

     [Approve All] [Review Individually]
   ```

   The story-level checks are human QA — does the overall experience
   work? The per-feature checks (from specs) are for drilling in when
   something's wrong.

5. Val checks the story-level items. The inbox loads, items are ordered
   right, approve/reject works. She clicks [Approve All]. Three features
   move to done. The workstream shows 100% complete.

### Story: Marcus encounters infrastructure work that has no user story

**Context:** Marcus needs to add SQLite WAL mode and a busy timeout to
prevent concurrent agent lockouts. There's no user persona here — this
is plumbing.

1. He creates a feature directly (no workstream story needed):
   ```
   tillr feature add "sqlite-wal-mode" \
     --workstream agent-infra \
     --spec "Set WAL mode and 5s busy timeout on db.Open(). Add
   integration test with concurrent writers."
   ```

   The `agent-infra` workstream doesn't have a user story — it has a
   description ("Infrastructure for reliable agent operation") but no
   persona or journey. That's fine. Not everything is a user story.

2. When this reaches human-qa, the inbox shows it with the feature spec,
   not a story-level checklist:
   ```
   Agent Infrastructure                              1 item

     sqlite-wal-mode              Priority 7
       Spec: "Set WAL mode and 5s busy timeout..."
       No story-level checks — review the spec.
       [Approve] [Reject]
   ```

   Marcus reviews the code, runs a concurrent test, approves.

**What works well:**
- Two clear levels: stories for user intent (workstream), specs for
  implementation (feature). Each serves a different audience — stories
  for humans, specs for agents.
- Not everything needs a story. Infrastructure workstreams skip the
  story and work fine with just specs.
- QA has clear layers: story-level checks are human experience checks
  ("does this flow feel right?"), feature-level checks are technical
  ("does the endpoint return 200?").
- The inbox can group by workstream/story, making QA coherent — "review
  the whole OAuth flow" not "review 3 disconnected features."
- Agents get both levels of context in the claim response.

**What breaks:**
- The `--story-file` on workstreams is a new concept. Today workstreams
  have `description` (short) and `tags`. Adding a story file means
  either a new DB column or relying on attachments.
- Writing good stories takes effort. If no one writes them, workstreams
  degrade to Option C (specs only, no grouping narrative). The tool
  can't force good stories — it can only make them easy to write and
  visible when missing.
- Story-level QA checks need to be extractable from the markdown. This
  means the story format needs some structure — a `## How to verify`
  section that the tool can parse. Convention-based, but fragile.
- Workstream stories might be too coarse for QA. "First-time setup"
  might have 15 features. QAing all 15 against one story checklist
  might mean re-running the whole flow for each batch, which is
  wasteful if you approved 10 features last week and 5 more came in.

**Technical cost:**
- Add `story_file` or `narrative` text column to workstreams table
  (migration: ~20 lines)
- Parse `## How to verify` section from story markdown for QA items
  (~100 lines of Go)
- Global inbox page aggregating across workstreams (~300 lines of React)
- Claim response includes workstream story context (~30 lines of Go)
- Feature spec validation on claim (~20 lines of Go)
- Total: ~500 lines, most of it in the inbox UI

---

## Comparison

| Dimension | A: Story Entity | B: Workstream = Story | C: Spec Template | D: Hybrid |
|-----------|----------------|----------------------|------------------|-----------|
| New DB tables | Yes (stories) | No | No | No (column on workstreams) |
| New CLI commands | Yes (story CRUD) | No | No | No (flag on workstream create) |
| Migration cost | High (~600 lines) | Near zero | Low (~150 lines) | Low (~500 lines) |
| Works for infra/refactoring | Forced/awkward | Forced/awkward | Yes (simpler template) | Yes (stories optional) |
| QA grouping | Stories group features | Workstreams group features | No grouping | Workstreams group features |
| Story → QA traceability | Strong (explicit link) | Medium (convention) | Weak (per-feature only) | Strong (parsed from story) |
| Spec bottleneck risk | Low (stories are optional) | Low | High (every feature needs spec) | Medium (specs needed, stories optional) |
| Agent context richness | Best (story + spec) | Good (workstream desc + spec) | Good (structured spec) | Best (story + spec) |
| Human QA experience | Best | Good | Adequate | Best |
| Conceptual overhead | High (new concept) | Low | Low | Medium |

---

## Recommendation Factors

**If you value simplicity:** Option B (workstream = story) or C (spec
templates). Both work within existing structures. C gives you spec-driven
QA without new concepts; B gives you story grouping without new tables.

**If you value QA quality:** Option A or D. Both give you story-level
human QA checks that are separate from technical specs. D is cheaper
than A because it reuses workstreams instead of adding a new entity.

**If you value agent context:** Option A or D. Both provide rich context
in the claim response — who the feature is for, why it matters, how
it fits into a journey. C provides this per-feature but it's repetitive
across related features.

**If you want to start today with minimal changes:** Option C. Add spec
validation and a structured spec template. You get spec-driven QA and
better agent context with ~150 lines of code. Graduate to D later when
the story-level grouping becomes important.

**If you want the end state now:** Option D. Stories drive workstreams,
specs drive features, QA has two clear layers, agents get full context,
and infrastructure work doesn't have to pretend it has a persona.

The realistic path is probably **C now, D later** — enforce specs
immediately (biggest bang for buck), then add workstream-level stories
when the QA grouping problem gets painful enough. Or just go straight
to D if the investment feels right — it's ~500 lines total.
