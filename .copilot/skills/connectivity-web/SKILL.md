---
name: connectivity-web
description: Apply the Connectivity Web UX principle to any graph-like visualization app. Ensures every data element is a navigable node with bidirectional links to all related entities. Use when building or auditing dashboards, admin panels, or data-centric UIs.
---

# Connectivity Web — Graph Navigation UX Skill

> Every piece of data is a node. Every relationship is an edge. Every edge is a clickable link. No dead ends.

## What This Skill Does

This skill guides you through applying the **Connectivity Web** principle to any application that displays interconnected data. It transforms flat, siloed pages into a fully navigable data graph where users can explore relationships in any direction.

**Use this skill when:**
- Building a new dashboard, admin panel, or data visualization app
- Auditing an existing UI for navigational gaps
- A user says "I can see it but can't click it" or "how do I get from X to Y?"
- Migrating from a flat page structure to interconnected views

## The Principle

```
If you can SEE a piece of data, you can CLICK it.
If you can CLICK it, you land on a page that shows ALL its relationships.
If it HAS relationships, every related entity links BACK.
```

This creates a **bidirectional graph** where users never hit dead ends. Every summary page links to detail pages. Every detail page links to related entities. Every related entity links back.

## Methodology

### Phase 1: Map the Data Graph

Before writing any code, enumerate every entity type and relationship in the application.

#### Step 1: Identify All Nodes

List every distinct data type the application manages. Be thorough — include both primary entities and supporting/junction types.

Example from a project management tool:
```
Primary:   Feature, Milestone, RoadmapItem, Agent, Cycle
Secondary: WorkItem, Event, QAResult, Discussion, Decision, Idea
Supporting: Comment, Score, StatusUpdate, Dependency, Tag
```

For each node, determine:
- **Summary page**: Does it have a list/index page? (e.g., `/features`)
- **Detail page**: Does it have a detail page? (e.g., `/features/:id`)
- **Inline display**: Is it shown inline on other pages? (e.g., QAResult on Feature detail)

#### Step 2: Map All Edges

For every pair of related entities, document the relationship:

```
Feature ──── Milestone        (feature.milestone_id → milestone)
Feature ──── RoadmapItem      (feature.roadmap_item_id → roadmap_item)
Feature ──── Feature          (dependencies: many-to-many)
Feature ──── CycleInstance    (cycle.feature_id → feature)
Feature ──── WorkItem         (work_item.feature_id → feature)
Feature ──── Event            (event.feature_id → feature)
Discussion ── Feature         (discussion.feature_id → feature)
Decision ──── Feature         (decision.feature_id → feature)
Decision ──── Decision        (decision.superseded_by → decision)
Agent ──────── WorkItem       (work_item.assigned_agent → agent)
```

Capture:
- **Direction**: Which entity holds the foreign key?
- **Cardinality**: 1:1, 1:many, many:many
- **Navigability needed**: Both directions? (Usually yes.)

#### Step 3: Find the Hub Nodes

Identify which entities are the most connected — these are your **hub nodes**. In most apps, 1-3 entities connect to nearly everything else. These hubs become the backbone of navigation.

Example: In a project management tool, `Feature` connects to 13 other entity types. It's the hub. Users constantly navigate through features to reach everything else.

Hub nodes deserve:
- Rich detail pages with sections for every relationship
- Prominent placement in navigation
- Quick-access links from every other page

#### Step 4: Produce the Graph Document

Create a design document (e.g., `docs/design/connectivity-web.md`) with:

1. **Node inventory table**: entity → summary page → detail page → inline display
2. **Edge list**: every relationship with direction and cardinality
3. **Hub analysis**: which entities are most connected
4. **Gap analysis**: which links are missing in the current UI
5. **Implementation priority**: critical → high → medium gaps

### Phase 2: Build the Infrastructure

#### The EntityLink Component

Create a universal component for rendering clickable entity references. This is the atomic building block of the connectivity web.

```tsx
// Every entity reference in the UI uses this component
<EntityLink type="feature" id={f.id} name={f.name} />
<EntityLink type="milestone" id={m.id} name={m.name} />
<EntityLink type="agent" id={a.id} name={a.name} showIcon />
```

**Requirements:**
- Maps entity type → route path (e.g., `feature` → `/features/:id`)
- Renders as a styled `<Link>` with hover state
- Optional icon prefix per entity type
- Optional status indicator
- Compact variant (chip/pill) for inline use in tables

**Implementation pattern:**

```tsx
const routeMap: Record<EntityType, string> = {
  feature: '/features',
  milestone: '/milestones',
  agent: '/agents',
  // ... one entry per entity type
}

function EntityLink({ type, id, name, showIcon }) {
  return <Link to={`${routeMap[type]}/${id}`}>{name || id}</Link>
}
```

**Key insight:** By centralizing all entity routing in one component, you can:
- Ensure consistent link styling across the entire app
- Add new entity types in one place
- Refactor routes without hunting through every file

#### SPA Routing

If the app is a single-page application with a backend serving assets, ensure the server correctly handles client-side routes:

**Don't use dot-detection** for file vs route differentiation (IDs may contain dots like `v0.2`). Instead, use filesystem stat:

```go
// Check if the path is a real file in the embedded assets
if _, err := fs.Stat(assetsFS, cleanPath); err != nil {
    // Not a file — serve index.html for SPA routing
    r.URL.Path = "/"
}
```

### Phase 3: Audit and Fix Existing Pages

For each existing page, systematically check every rendered data element:

#### Audit Checklist

For every page in the app, answer these questions:

1. **What data is shown?** List every entity type that appears.
2. **What's currently linked?** Which items have working navigation.
3. **What's plain text but should link?** These are your gaps.
4. **What related data is NOT shown?** These are missing cross-references.

Common gaps to look for:
- **Names as plain text**: Milestone names, agent names, category labels shown as text but not linked
- **IDs without context**: Feature IDs in event logs without links
- **Missing reverse links**: Feature shows its milestone, but milestone doesn't show its features
- **Orphaned inline data**: QA results shown but can't navigate to the reviewed feature

#### Fix Pattern

For each gap, the fix is almost always the same:

```tsx
// BEFORE: plain text
<span>{feature.milestone_name}</span>

// AFTER: linked
<EntityLink type="milestone" id={feature.milestone_id} name={feature.milestone_name} />
```

### Phase 4: Build Detail Pages

Every entity type with meaningful data needs a detail page. Detail pages follow a consistent pattern:

#### Detail Page Template

```
┌─────────────────────────────────────────────┐
│ Breadcrumb: EntityType > EntityName          │
├─────────────────────────────────────────────┤
│ Header: Name + Status Badge                  │
│ Description / Summary                        │
├─────────────────────────────────────────────┤
│ Metadata Grid:                               │
│   Created: date    Priority: high            │
│   Updated: date    Category: infrastructure  │
├─────────────────────────────────────────────┤
│ Primary Content:                             │
│   (Spec, body text, raw input, etc.)         │
├─────────────────────────────────────────────┤
│ Related Entities:                            │
│   ┌ Features (3) ──────────────────────┐     │
│   │ Feature A  ● implementing  → link  │     │
│   │ Feature B  ● done          → link  │     │
│   └────────────────────────────────────┘     │
│   ┌ Discussions (1) ──────────────────┐     │
│   │ RFC: Caching Strategy   → link    │     │
│   └────────────────────────────────────┘     │
│   ┌ Decisions (0) ────────────────────┐     │
│   │ No decisions linked yet.          │     │
│   └────────────────────────────────────┘     │
└─────────────────────────────────────────────┘
```

**Key rules:**
1. **Breadcrumb** at top — always navigable back to the list page
2. **Header** — entity name with status badge
3. **Metadata grid** — all scalar fields in a compact grid
4. **Related entities sections** — one section per relationship, each item linked via EntityLink
5. **Empty states** — "No X linked yet" rather than hiding empty sections (teaches users what connections are possible)

### Phase 5: Cross-Reference Sections

The most powerful connectivity feature: **related entity sections** on detail pages.

For each hub entity (e.g., Feature), add sections for every connected entity type:

```
Feature Detail:
├── Metadata (milestone link, cycle link, roadmap link)
├── Spec / Description
├── Dependencies (feature → feature links)
├── QA History (inline results)
├── Cycle History (cycle links with scores)
├── Active Agent (agent link with status)
├── Discussions (discussion links)
├── Decisions (decision links)
└── Context Entries (inline)
```

**Implementation tip:** Fetch related entities via filtered API calls, not by embedding everything in the primary response:

```tsx
// Fetch cycles related to this feature
const { data: cycles } = useQuery({
  queryKey: ['cycles'],
  select: (data) => data.filter(c => c.feature_id === featureId)
})
```

This keeps the API simple while the frontend assembles the graph view.

## Anti-Patterns to Avoid

### 1. Dot-Based Dead Ends
Never show a count or statistic without making it drillable:
```
❌ "3 features blocked"        → can't see which ones
✅ "3 features blocked" → link → filtered feature list showing blocked items
```

### 2. One-Way Links
If Feature links to Milestone, Milestone must link back to Feature:
```
❌ Feature → Milestone (but Milestone page doesn't show features)
✅ Feature → Milestone → Feature (bidirectional)
```

### 3. Modal Traps
Don't show entity details in modals that can't be bookmarked or opened in new tabs. Use real routes:
```
❌ onClick={() => setModal(feature)}   → no URL, can't share
✅ <Link to={`/features/${f.id}`}>     → real URL, shareable
```

### 4. Hover-Only Information
Don't put navigational content in hover tooltips. Hovers disappear, can't be clicked on mobile, and are invisible to assistive tech:
```
❌ <span title="Milestone: v1.0">Feature Name</span>
✅ <EntityLink type="milestone" id={m.id} name={m.name} />
```

### 5. Status Without Context
Don't show a status badge without linking to what's in that status:
```
❌ StatusBadge showing "implementing" with no way to see what's implementing
✅ StatusBadge that links to filtered list: /features?status=implementing
```

## Quality Checklist

Before considering the connectivity web complete, verify:

- [ ] Every entity type has a summary (list) page
- [ ] Every entity type with meaningful data has a detail page
- [ ] Every entity reference on every page is a clickable EntityLink
- [ ] Every detail page shows all related entities with links
- [ ] Breadcrumbs work on every detail page
- [ ] All routes work when opened directly (SPA routing)
- [ ] All routes work when opened in a new tab
- [ ] Back/forward browser navigation works
- [ ] No data is shown as plain text that could be a link
- [ ] Empty related-entity sections show helpful empty states
- [ ] Hub nodes have comprehensive cross-reference sections

## Adapting to Different Tech Stacks

The connectivity web is a UX principle, not a tech choice. Here's how to adapt:

| Stack | EntityLink equivalent | Routing | Data fetching |
|-------|----------------------|---------|--------------|
| React + React Router | `<Link>` component | React Router v7 | TanStack Query |
| Next.js | `<Link>` (next/link) | File-based routing | SWR or TanStack Query |
| Vue + Vue Router | `<RouterLink>` | Vue Router | Pinia + fetch |
| Svelte + SvelteKit | `<a href>` | SvelteKit routing | load functions |
| HTMX | `<a hx-get>` | Server-side | Server renders |
| Vanilla JS | `<a href>` | History API | fetch |

The pattern is always the same: a reusable component that maps `(entityType, id, name) → clickable link to detail page`.

## Example: Applying to a New Project

Suppose you're building a **monitoring dashboard** with these entities:
- Service, Endpoint, Alert, Incident, Team, Deployment, Metric

### Step 1: Map the graph
```
Service ─── Endpoint      (1:many)
Service ─── Alert         (1:many)
Service ─── Team          (many:many)
Service ─── Deployment    (1:many)
Alert ───── Incident      (many:1)
Incident ── Team          (many:1)
Deployment ─ Metric       (1:many)
```

### Step 2: Identify hub → Service (6 connections)

### Step 3: Create EntityLink with types: service, endpoint, alert, incident, team, deployment, metric

### Step 4: Audit every page — anywhere a service name appears, wrap it in `<EntityLink type="service">`. Same for teams, alerts, etc.

### Step 5: Build detail pages for Service (showing all endpoints, alerts, deployments, team), Incident (showing alerts, team, service), etc.

The result: users can start anywhere and navigate to anything related, in any direction, without dead ends.
