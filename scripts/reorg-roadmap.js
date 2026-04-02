#!/usr/bin/env node
// Reorganize roadmap items to reflect current feature state.
// Usage: node scripts/reorg-roadmap.js
//
// Steps:
//   1. Sync roadmap item statuses to match linked features
//   2. Create missing roadmap items for features without them
//   3. Create v2.0-scale milestone if missing
//   4. Recategorize roadmap items into: core, web-ui, integrations, analytics, workflow, dx

const BASE = process.env.LIFECYCLE_URL || "http://localhost:9879";

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function api(path, opts = {}) {
  const url = `${BASE}${path}`;
  const res = await fetch(url, {
    headers: { "Content-Type": "application/json" },
    ...opts,
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`${opts.method || "GET"} ${path} => ${res.status}: ${text}`);
  }
  return res.json();
}

// Map feature status to roadmap status
function featureStatusToRoadmap(status) {
  switch (status) {
    case "done":
      return "done";
    case "human-qa":
    case "agent-qa":
    case "implementing":
      return "in-progress";
    case "planning":
    case "draft":
      return "proposed";
    case "blocked":
      return "deferred";
    default:
      return "proposed";
  }
}

// Map feature priority (int) to roadmap priority (string)
function featurePriorityToRoadmap(priority) {
  if (priority >= 8) return "critical";
  if (priority >= 6) return "high";
  if (priority >= 4) return "medium";
  if (priority >= 2) return "low";
  return "nice-to-have";
}

// Slug an ID from a title
function slugify(title) {
  return title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "")
    .substring(0, 80);
}

// Categorize a roadmap item by text analysis, with manual overrides for edge cases
const CATEGORY_OVERRIDES = {
  "websocket-live-updates": "core",
  "config-file-defaults": "dx",
  "fts5-search-enhancement": "core",
  "batch-feature-ops": "dx",
  "agent-discussion-system": "workflow",
  "feature-dependencies": "workflow",
  "decision-log": "workflow",
  "notification-system": "integrations",
  "dependency-graph-visualization": "analytics",
  "cycle-scoring-ux": "analytics",
  "stats-dashboard-page": "analytics",
  "fix-score-history-chart-aspect-ratio": "analytics",
  "agent-heartbeat-dashboard": "analytics",
  "timeline-view": "analytics",
  "perf-monitoring-dashboard": "analytics",
  "activity-heat-maps": "analytics",
  "comprehensive-roadmap": "core",
  "self-hosting-bootstrap": "core",
  "interactive-tui-mode": "dx",
  "architecture-diagrams": "dx",
  "real-time-updates": "core",
  "project-templates": "dx",
  "bulk-operations": "dx",
};

function categorize(id, title, description) {
  if (CATEGORY_OVERRIDES[id]) return CATEGORY_OVERRIDES[id];

  const text = `${title} ${description || ""}`.toLowerCase();

  // Analytics - charts, stats, metrics, visualization
  if (
    text.includes("chart") ||
    text.includes("heatmap") ||
    text.includes("heat map") ||
    text.includes("burndown") ||
    text.includes("stats") ||
    text.includes("metric") ||
    text.includes("analytics") ||
    text.includes("velocity") ||
    text.includes("throughput") ||
    text.includes("score trend") ||
    text.includes("cycle-time") ||
    text.includes("cycle time")
  )
    return "analytics";

  // Integrations - external systems, notifications, import/export
  if (
    text.includes("github") ||
    text.includes("jira") ||
    text.includes("mcp") ||
    text.includes("webhook") ||
    text.includes("ci/cd") ||
    text.includes("ci-cd") ||
    text.includes("import") ||
    text.includes("export") ||
    text.includes("notification") ||
    text.includes("pr integration")
  )
    return "integrations";

  // Web UI - frontend pages, modals, layout, styling
  if (
    text.includes("dashboard") ||
    text.includes("sidebar") ||
    text.includes("modal") ||
    text.includes("viewer") ||
    text.includes("display") ||
    text.includes("responsive") ||
    text.includes("mobile") ||
    text.includes("theme") ||
    text.includes("dark mode") ||
    text.includes("dark/light") ||
    text.includes("lightbox") ||
    text.includes("breadcrumb") ||
    text.includes("layout") ||
    text.includes("kanban") ||
    text.includes("inline edit") ||
    text.includes("toast") ||
    text.includes("feedback modal") ||
    text.includes("web ui") ||
    text.includes("frontend") ||
    text.includes("react") ||
    text.includes("a11y") ||
    text.includes("accessibility") ||
    text.includes("wcag") ||
    text.includes("keyboard shortcut")
  )
    return "web-ui";

  // Workflow - agents, cycles, QA, queues, discussions, ideas
  if (
    text.includes("agent") ||
    text.includes("cycle") ||
    text.includes("qa ") ||
    text.includes(" qa") ||
    text.includes("workflow") ||
    text.includes("queue") ||
    text.includes("discussion") ||
    text.includes("idea") ||
    text.includes("approval") ||
    text.includes("vote") ||
    text.includes("voting") ||
    text.includes("scoring") ||
    text.includes("intake") ||
    text.includes("pipeline") ||
    text.includes("plugin") ||
    text.includes("multi-project") ||
    text.includes("coordination") ||
    text.includes("blocking") ||
    text.includes("depend")
  )
    return "workflow";

  // DX - developer experience, CLI, shell, config, docs
  if (
    text.includes("cli") ||
    text.includes("shell") ||
    text.includes("completion") ||
    text.includes("developer") ||
    text.includes("onboard") ||
    text.includes("template") ||
    text.includes("config") ||
    text.includes("hot reload") ||
    text.includes("error handling") ||
    text.includes("log") ||
    text.includes("release note") ||
    text.includes("changelog") ||
    text.includes("documentation") ||
    text.includes("api doc") ||
    text.includes("tui") ||
    text.includes("fuzzy search") ||
    text.includes("rate limit") ||
    text.includes("adr") ||
    text.includes("decision record") ||
    text.includes("batch") ||
    text.includes("bulk")
  )
    return "dx";

  // Core - engine, DB, API, architecture, search, auth, backup
  return "core";
}

async function main() {
  console.log("=== Roadmap Reorganization ===\n");

  // Fetch all data
  const [features, roadmapItems, milestones] = await Promise.all([
    api("/api/features"),
    api("/api/roadmap"),
    api("/api/milestones"),
  ]);

  console.log(
    `Fetched: ${features.length} features, ${roadmapItems.length} roadmap items, ${milestones.length} milestones\n`
  );

  // Build lookup maps
  const roadmapById = new Map(roadmapItems.map((r) => [r.id, r]));
  const featuresByRoadmapId = new Map();
  for (const f of features) {
    if (f.roadmap_item_id) {
      featuresByRoadmapId.set(f.roadmap_item_id, f);
    }
  }
  const featureById = new Map(features.map((f) => [f.id, f]));

  // Helper to find the linked feature for a roadmap item
  function findLinkedFeature(ri) {
    let f = featuresByRoadmapId.get(ri.id) || featureById.get(ri.id);
    if (!f) {
      for (const feat of features) {
        if (slugify(feat.name) === ri.id) {
          f = feat;
          break;
        }
      }
    }
    return f;
  }

  // ========================================
  // Step 1: Sync roadmap statuses to features
  // ========================================
  console.log("--- Step 1: Sync roadmap item statuses ---");
  let statusUpdated = 0;
  let statusSkipped = 0;

  for (const ri of roadmapItems) {
    const linkedFeature = findLinkedFeature(ri);
    if (!linkedFeature) {
      statusSkipped++;
      continue;
    }

    const newStatus = featureStatusToRoadmap(linkedFeature.status);
    if (ri.status === newStatus) continue;

    try {
      await api(`/api/roadmap/${ri.id}/status`, {
        method: "PATCH",
        body: JSON.stringify({ status: newStatus }),
      });
      statusUpdated++;
      console.log(`  ${ri.id}: ${ri.status} -> ${newStatus} (feature: ${linkedFeature.status})`);
    } catch (e) {
      console.error(`  ERROR ${ri.id}: ${e.message}`);
    }
    await sleep(50);
  }
  console.log(`  Synced: ${statusUpdated} updated, ${statusSkipped} no linked feature\n`);

  // ========================================
  // Step 2: Create v2.0-scale milestone
  // ========================================
  console.log("--- Step 2: Create v2.0-scale milestone ---");
  if (!milestones.some((m) => m.id === "v2.0-scale")) {
    try {
      await api("/api/milestones", {
        method: "POST",
        body: JSON.stringify({
          id: "v2.0-scale",
          name: "v2.0 Scale",
          description: "Scaling features for multi-project and team usage",
          sort_order: 5,
        }),
      });
      console.log("  Created v2.0-scale milestone\n");
    } catch (e) {
      console.error(`  ERROR: ${e.message}\n`);
    }
    await sleep(50);
  } else {
    console.log("  Already exists\n");
  }

  // ========================================
  // Step 3: Create missing roadmap items
  // ========================================
  console.log("--- Step 3: Create roadmap items for unlinked features ---");
  const freshRoadmap = await api("/api/roadmap");
  const freshRoadmapById = new Map(freshRoadmap.map((r) => [r.id, r]));

  let created = 0;
  for (const f of features) {
    if (f.roadmap_item_id && freshRoadmapById.has(f.roadmap_item_id)) continue;
    if (freshRoadmapById.has(f.id)) continue;
    const slug = slugify(f.name);
    if (freshRoadmapById.has(slug)) continue;

    const id = slug || f.id;
    const cat = categorize(id, f.name, f.description);
    const priority = featurePriorityToRoadmap(f.priority);
    const status = featureStatusToRoadmap(f.status);

    try {
      await api("/api/roadmap", {
        method: "POST",
        body: JSON.stringify({
          id,
          title: f.name,
          description: f.description || "",
          category: cat,
          priority,
          status,
        }),
      });
      created++;
      freshRoadmapById.set(id, { id }); // prevent duplicates
      console.log(`  Created: ${id} (${cat}, ${priority}, ${status})`);
    } catch (e) {
      console.error(`  ERROR ${id}: ${e.message}`);
    }
    await sleep(50);
  }
  console.log(`  Created ${created} new roadmap items\n`);

  // ========================================
  // Step 4: Recategorize all roadmap items
  // ========================================
  console.log("--- Step 4: Recategorize roadmap items ---");
  const allRoadmap = await api("/api/roadmap");
  let recategorized = 0;

  for (const ri of allRoadmap) {
    const newCat = categorize(ri.id, ri.title, ri.description);
    if (ri.category !== newCat) {
      try {
        await api(`/api/roadmap/${ri.id}`, {
          method: "PATCH",
          body: JSON.stringify({ category: newCat }),
        });
        recategorized++;
        console.log(`  ${ri.id}: ${ri.category || "(none)"} -> ${newCat}`);
      } catch (e) {
        console.error(`  ERROR ${ri.id}: ${e.message}`);
      }
      await sleep(50);
    }
  }
  console.log(`  Recategorized ${recategorized} items\n`);

  // Also fix statuses for any newly-created items that might have defaulted
  console.log("--- Fixing statuses for new items ---");
  const postCreateRoadmap = await api("/api/roadmap");
  let statusFixed = 0;
  for (const ri of postCreateRoadmap) {
    const linkedFeature = findLinkedFeature(ri);
    if (!linkedFeature) continue;
    const correctStatus = featureStatusToRoadmap(linkedFeature.status);
    if (ri.status !== correctStatus) {
      try {
        await api(`/api/roadmap/${ri.id}/status`, {
          method: "PATCH",
          body: JSON.stringify({ status: correctStatus }),
        });
        statusFixed++;
        console.log(`  ${ri.id}: ${ri.status} -> ${correctStatus}`);
      } catch (e) {
        console.error(`  ERROR ${ri.id}: ${e.message}`);
      }
      await sleep(50);
    }
  }
  console.log(`  Fixed ${statusFixed} statuses\n`);

  // ========================================
  // Summary
  // ========================================
  const finalRoadmap = await api("/api/roadmap");
  const finalMilestones = await api("/api/milestones");

  const statusCounts = {};
  const categoryCounts = {};
  for (const ri of finalRoadmap) {
    statusCounts[ri.status] = (statusCounts[ri.status] || 0) + 1;
    categoryCounts[ri.category] = (categoryCounts[ri.category] || 0) + 1;
  }

  console.log("=== Final State ===");
  console.log(`Total roadmap items: ${finalRoadmap.length}`);
  console.log(`Milestones: ${finalMilestones.map((m) => m.id).join(", ")}`);
  console.log("\nBy status:");
  for (const [s, c] of Object.entries(statusCounts).sort()) {
    console.log(`  ${s}: ${c}`);
  }
  console.log("\nBy category:");
  for (const [s, c] of Object.entries(categoryCounts).sort()) {
    console.log(`  ${s}: ${c}`);
  }
}

main().catch((e) => {
  console.error("FATAL:", e);
  process.exit(1);
});
