# The Connectivity Web

> Every piece of data in Lifecycle is a node on a graph. Every node connects to every other node it's related to. No dead ends, no orphaned data, no "I can see it but can't click it."

## Concept

The Connectivity Web is a UX principle: **every data element displayed in the UI is a clickable link to its detail page, and every detail page shows all related nodes with links back**. If you can see a milestone name, you can click it. If a feature belongs to a cycle, the cycle is linked. If an agent is working on a feature, the agent is linked from the feature and the feature is linked from the agent.

This creates a fully navigable data graph where users can explore relationships in any direction.

## Entity Graph

### Nodes (15 entity types)

| Node | Detail Page | Summary Page |
|------|-------------|--------------|
| **Feature** | `/features/:id` | `/features` |
| **Milestone** | `/milestones/:id` | `/milestones` (via dashboard) |
| **RoadmapItem** | `/roadmap/:id` | `/roadmap` |
| **CycleInstance** | `/cycles/:id` | `/cycles` |
| **WorkItem** | (inline on feature/agent) | `/queue` (work queue) |
| **Event** | (inline on history) | `/history` |
| **QAResult** | (inline on feature) | `/qa` |
| **Discussion** | `/discussions/:id` | `/discussions` |
| **AgentSession** | `/agents/:id` | `/agents` |
| **Idea** | `/ideas/:id` | `/ideas` |
| **Decision** | `/decisions/:id` | `/decisions` |
| **ContextEntry** | (inline on feature) | `/context` |
| **StatusUpdate** | (inline on agent) | (via agent detail) |
| **CycleScore** | (inline on cycle) | (via cycle detail) |
| **Dependency** | (links on feature) | (via feature detail) |

### Edges (all relationships)

```
Feature ──── Milestone          (feature.milestone_id → milestone)
Feature ──── RoadmapItem        (feature.roadmap_item_id → roadmap_item)
Feature ──── Feature            (dependencies table: depends_on / blocks)
Feature ──── CycleInstance      (cycle.feature_id → feature)
Feature ──── WorkItem           (work_item.feature_id → feature)
Feature ──── Event              (event.feature_id → feature)
Feature ──── QAResult           (qa_result.feature_id → feature)
Feature ──── Discussion         (discussion.feature_id → feature)
Feature ──── AgentSession       (agent_session.feature_id → feature)
Feature ──── Idea               (idea.feature_id → feature, when processed)
Feature ──── ContextEntry       (context_entry.feature_id → feature)
Feature ──── Decision           (decision.feature_id → feature)
Feature ──── Heartbeat          (heartbeat.feature_id → feature)

CycleInstance ── CycleScore     (score.cycle_id → cycle)
Discussion ──── DiscussionComment (comment.discussion_id → discussion)
AgentSession ── StatusUpdate    (update.agent_session_id → session)
AgentSession ── Worktree        (worktree.agent_session_id → session)
AgentSession ── WorkItem        (work_item.assigned_agent → session)
Decision ────── Decision        (decision.superseded_by → decision)
```

### The Feature Hub

Feature is the most-connected node (13 edge types). Every other entity connects through it:

```
                    Milestone
                       │
          Idea ─── Feature ─── RoadmapItem
                  /  │  │  \
         Decision   │  │   Discussion
                   │  │
    AgentSession──WorkItem──CycleInstance──CycleScore
                   │
              QAResult, Event, ContextEntry, Heartbeat, Dependency
```

## Current State vs Target

### What's implemented ✅

| Link | From → To | Status |
|------|-----------|--------|
| Feature list → Feature detail | Features page → `/features/:id` | ✅ Works |
| Feature detail → Feature detail | Dependencies section | ✅ Works |
| Dashboard feature cards → Feature detail | Kanban board | ✅ Works |
| Sidebar → All top-level pages | Navigation | ✅ Works |

### What's missing ❌

#### Critical (blocks navigation)

| Link | From → To | Current State |
|------|-----------|---------------|
| QA feature name → Feature detail | QA page | ❌ Plain text |
| Roadmap item → Roadmap detail | Roadmap page | ❌ No detail page |
| Milestone name → Milestone detail | Everywhere | ❌ Plain text, no detail page |
| Event feature_id → Feature detail | Dashboard activity | ❌ Plain text |
| Cycle name → Cycle detail | Feature detail | ❌ Plain text, no detail page |

#### High (information gaps)

| Link | From → To | Current State |
|------|-----------|---------------|
| Feature → Roadmap item | Feature detail | ❌ Not shown |
| Feature → Cycle history | Feature detail | ❌ Not shown |
| Feature → Active agent | Feature detail | ❌ Not shown |
| Roadmap → Linked features | Roadmap detail | ❌ No detail page |
| Milestone → Features list | Milestone detail | ❌ No detail page |
| Agent → Current feature | Agent page | ❌ Placeholder page |
| Agent → Work items | Agent detail | ❌ Placeholder page |
| Idea → Created feature | Idea detail | ❌ Placeholder page |
| Discussion → Feature | Discussion detail | ❌ Placeholder page |
| Decision → Feature | Decision detail | ❌ Placeholder page |

#### Medium (enhanced cross-referencing)

| Link | From → To | Current State |
|------|-----------|---------------|
| Dashboard → Milestone detail | Milestone panel | ❌ Not linked |
| Feature → Discussions | Feature detail | ❌ Not shown |
| Feature → Decisions | Feature detail | ❌ Not shown |
| Feature → Context entries | Feature detail | ❌ Not shown |
| Roadmap → Milestones | Roadmap items | ❌ Not shown |
| Cycle → Work items | Cycle detail | ❌ No detail page |
| Agent → Status updates | Agent detail | ❌ Placeholder |

## Detail Pages Required

### 1. Milestone Detail (`/milestones/:id`)

Shows: name, description, status, progress bar, creation date.

**Connected nodes displayed:**
- Features in this milestone (table with status, priority, links)
- Active cycles for milestone features
- Recent events for milestone features
- QA pending count for milestone features
- Roadmap items linked via features

### 2. Roadmap Item Detail (`/roadmap/:id`)

Shows: title, description, priority, effort, category, status.

**Connected nodes displayed:**
- Features linked to this roadmap item (table with status)
- Discussions related to linked features
- Decisions related to linked features
- Active agents working on linked features

### 3. Cycle Detail (`/cycles/:id`)

Shows: cycle type, current step, iteration, status, step timeline.

**Connected nodes displayed:**
- Feature this cycle is for (link + summary)
- Score history (chart: score over iterations)
- Work items produced by this cycle
- Current step agent/assignee

### 4. Agent Detail (`/agents/:id`)

Shows: name, status, progress bar, current phase, ETA, duration.

**Connected nodes displayed:**
- Current feature being worked on (link + summary)
- Current work item (type, prompt, status)
- Status update feed (markdown timeline)
- Completed work items (history table)
- Worktree info (branch, path)

### 5. Idea Detail (`/ideas/:id`)

Shows: title, raw input, type, status, spec (markdown), submitted by.

**Connected nodes displayed:**
- Created feature (if processed → link)
- Source page context
- Assigned agent (if being processed)

### 6. Discussion Detail (`/discussions/:id`)

Shows: title, body (markdown), status, author, votes.

**Connected nodes displayed:**
- Linked feature (if any → link + summary)
- Comment thread (nested, with author + type badges)
- Vote summary (reactions)

### 7. Decision Detail (`/decisions/:id`)

Shows: title, status, context, decision, consequences (all markdown).

**Connected nodes displayed:**
- Linked feature (if any → link)
- Superseded by / supersedes chain (linked decisions)

## Implementation: Link Components

### `EntityLink` — Universal clickable reference

Every entity reference in the UI should use this pattern:

```tsx
// Renders a clickable link to any entity's detail page
<EntityLink type="feature" id="auth-login" name="Auth Login" />
<EntityLink type="milestone" id="v1.0" name="v1.0 Production" />
<EntityLink type="agent" id="agent-abc" name="claude-agent" status="active" />
```

Routes:
- `feature` → `/features/:id`
- `milestone` → `/milestones/:id`
- `roadmap` → `/roadmap/:id`
- `cycle` → `/cycles/:id`
- `agent` → `/agents/:id`
- `idea` → `/ideas/:id`
- `discussion` → `/discussions/:id`
- `decision` → `/decisions/:id`

### `RelatedEntities` — Cross-reference section

Every detail page gets a related entities section:

```tsx
<RelatedEntities
  featureId="auth-login"
  sections={['cycles', 'agents', 'discussions', 'decisions', 'qa']}
/>
```

## Page-by-Page Connectivity Fixes

### Dashboard
- [ ] Milestone names → `/milestones/:id`
- [ ] Event feature_id → `/features/:id`
- [ ] Roadmap items → `/roadmap/:id`
- [ ] Show active agents panel with → `/agents/:id`

### Features List
- [ ] Milestone name in rows → `/milestones/:id`
- [ ] Tags → filter by tag (same page)

### Feature Detail
- [ ] Milestone name → `/milestones/:id`
- [ ] Cycle name → `/cycles/:id`
- [ ] Add "Roadmap Item" link → `/roadmap/:id`
- [ ] Add "Active Agent" section → `/agents/:id`
- [ ] Add "Cycle History" section → `/cycles/:id` (each)
- [ ] Add "Discussions" section → `/discussions/:id` (each)
- [ ] Add "Decisions" section → `/decisions/:id` (each)

### QA Page
- [ ] Feature name → `/features/:id`
- [ ] Milestone name → `/milestones/:id`

### Roadmap Page
- [ ] Item titles → `/roadmap/:id`
- [ ] Show feature count per item → `/features` (filtered)

### Agents Page (NEW)
- [ ] Agent cards with status, progress, phase
- [ ] Current feature → `/features/:id`
- [ ] Current work item type shown
- [ ] Click to expand → `/agents/:id`

### Milestones Page (NEW — or via dashboard)
- [ ] Milestone cards with progress, feature count
- [ ] Click → `/milestones/:id`

## Priority Order

### Phase 1: Fix existing pages (add missing links)
1. Create `EntityLink` component
2. Fix QA page: feature names clickable
3. Fix Roadmap page: items clickable
4. Fix Dashboard: milestones, events, roadmap items clickable
5. Fix Features list: milestone names clickable
6. Fix Feature Detail: milestone, cycle clickable

### Phase 2: New detail pages
7. Milestone Detail page
8. Roadmap Item Detail page
9. Cycle Detail page
10. Agent Dashboard + Agent Detail page

### Phase 3: Remaining pages + cross-references
11. Idea page + Idea Detail
12. Discussion page + Discussion Detail
13. Decision page + Decision Detail
14. Feature Detail: add related entities sections (cycles, agents, discussions, decisions)
15. History page with entity links

### Phase 4: Enhanced connectivity
16. Search results → entity detail pages
17. Breadcrumb trails showing graph path
18. "Related items" sidebar on all detail pages
19. Active agent indicator on feature cards/rows
