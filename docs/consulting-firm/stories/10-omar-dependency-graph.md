# 10. Omar — PM Planning a Milestone, Needs the Full Picture

**Context:** Omar is planning the next milestone for his project. He has
20 remaining features to prioritize and wants to understand the
dependency graph before setting the order. He's been bitten before by
prioritizing features out of order and causing cascading blocks.

**What happens today:**

Omar reads each feature's spec and manually maps dependencies. He
misses implicit dependencies (features that share a database table,
features where one agent's comment on another feature flagged a
coordination need). He prioritizes #80 before #78, not knowing that
#80 depends on #78's API.

**What happens with the context graph:**

1. Omar opens the tillr dashboard and selects the "Milestone: MVP
   Launch" view:

   ```
   MVP Launch — Dependency Graph
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━

   #38 user-model ✓ done
    └── #42 login-page ✓ done
    └── #67 permissions ✓ done
         └── #72 admin-panel ✓ done
    └── #61 user-settings ✓ done
         └── #78 notification-preferences → in review
              └── #80 activity-feed → implementing
                   (depends: needs #78's API, see comment thread)

   #55 event-system ✓ done
    └── #78 notification-preferences → in review
              (uses event bus from #55)

   #51 api-error-handling ✓ done
    └── (all new API features inherit error middleware)

   Standalone:
   #83 export-csv → in review
   #85 reporting-data-model → human-qa
   ```

2. The dependency between #78 and #80 was never declared explicitly.
   It was discovered from a cross-feature comment:

   ```
   Source: Implementer on #80 commented on #78:
   "I'll need GET /api/users/:id/notification-preferences
   before I can filter the activity feed."
   ```

   Tillr detected the cross-reference and added the edge to the graph.

3. Omar sees that #80 is blocked on #78. He reviews #78, approves it,
   and #80 unblocks automatically. Without the graph, he might have
   reviewed #83 first (it's simpler) and left #80 stuck.

   **Gap:** The dependency graph shows explicit links (declared deps) and
   discovered links (from comments). But there's a third kind: *implicit*
   dependencies from shared code. If two features both modify
   `internal/db/models.go`, they have a merge conflict risk that doesn't
   appear in comments or declarations. The graph needs a "shared files"
   edge type, detected from git history or file-level analysis.

4. Omar also sees a cluster he didn't expect: #38 → #42 → #67 → #72 is
   a clear chain, but #55 → #78 is a cross-cutting dependency that
   only exists because the implementer on #78 reused #55's event bus.
   This tells Omar that the event system is becoming foundational — it
   might need hardening before more features depend on it.

**What would trip him up:**
- Dependency graph with 50+ features could be visually overwhelming.
  Omar needs filtering: show only the subgraph relevant to a specific
  feature, or only blocked chains, or only the critical path.
- "Discovered" dependencies from comments might be wrong — an agent
  mentions #42 in a comment but it's not actually a dependency, just
  a reference. The system needs to distinguish "depends on" from
  "related to."

**What makes this work:**
- Dependencies are discovered, not just declared. The graph builds from
  code relationships, cross-feature comments, and shared decisions.
- The PM sees structure that was invisible. Not because it didn't
  exist, but because no one had connected the dots across 20 tickets.
- Planning decisions are informed by the actual shape of the work,
  not by a flat priority list.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
