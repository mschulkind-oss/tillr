import { cleanup, render, within } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { DependencyGraph } from "../src/components/DependencyGraph";
import { DependencyLinks } from "../src/components/DependencyLinks";
import { dependencyLayout, relatedItems, selfLoopPath } from "../src/models/dependencyGraph";
import type { ItemView, WaitingRow } from "../src/models/types";

import { graphItem as item, sixtyItems } from "./dependency-fixture";
const rows = (items: ItemView[]): WaitingRow[] => items.map(i => ({item_id: i.item_id, class: i.item_id === "a" ? "moving" : "ready", rank: "", where: "", why: "", mover: "", since: "", class_source: "store"}));
afterEach(cleanup);

test("60 items and 80 edges have nonoverlapping full labels at 1440px", () => {
  const items = sixtyItems;
  const layout = dependencyLayout(items);
  expect(layout.nodes).toHaveLength(60);
  expect(layout.edges).toHaveLength(80);
  expect(layout.width).toBeLessThanOrEqual(1440 - 64);
  for (const a of layout.nodes) for (const b of layout.nodes) if (a !== b) expect(a.x + a.width <= b.x || b.x + b.width <= a.x || a.y + a.height <= b.y || b.y + b.height <= a.y).toBe(true);
  for (const node of layout.nodes) {
    expect(node.lines.join("").replace(/\s/g, "")).toBe(node.item.title.replace(/\s/g, ""));
    expect(node.lines.every(line => Array.from(line).length <= 28)).toBe(true);
  }
});

test("landed blockers are edge check marks, roots are leftmost and running nodes show their lane", () => {
  const items = [item("done", [], "landed"), item("a", ["done"], "working"), item("b", ["a"]), item("other")];
  items[1].latest_run = {run_id:"r", lane_id:"lane-6", backend:"pi", state:"running", exit_code:null, started_at_ms:1, finished_at_ms:0, cost_usd:null, usage_tokens:null};
  const layout = dependencyLayout(items);
  expect(layout.nodes.map(n => n.item.item_id)).not.toContain("done");
  expect(layout.nodes.find(n => n.item.item_id === "a")!.column).toBe(0);
  expect(layout.nodes.find(n => n.item.item_id === "other")!.column).toBe(0);
  expect(layout.nodes.find(n => n.item.item_id === "b")!.column).toBe(1);
  const {container} = render(<DependencyGraph items={items} waiting={rows(items)} selected="a" />);
  expect(container.querySelector('[data-landed="done"]')?.textContent).toContain("✓");
  expect(container.textContent).toContain("lane-6");
  expect(container.querySelector('[data-item="a"] [data-glyph="triangle"]')).toBeTruthy();
  expect(container.querySelector('[data-item="other"]')?.getAttribute("data-dimmed")).toBe("true");
  expect(within(container).getByRole("link", {name:/Select Build b/}).getAttribute("href")).toBe("#/graph?item=b");
});

test("selection follows ancestors and descendants, but not siblings", () => {
  const items = [item("a"), item("b", ["a"]), item("c", ["b"]), item("sibling", ["a"])];
  expect([...relatedItems(items, "b")].sort()).toEqual(["a", "b", "c"]);
});

test("cycle nodes and edges are flagged without flagging downstream work", () => {
  const items = [item("a", ["b"]), item("b", ["a"]), item("after", ["b"]), item("self", ["self"])];
  const layout = dependencyLayout(items);
  expect(layout.nodes.filter(n => n.cycle).map(n => n.item.item_id).sort()).toEqual(["a", "b", "self"]);
  expect(layout.edges.filter(e => e.cycle)).toHaveLength(3);
  const {container} = render(<DependencyGraph items={items} waiting={rows(items)} />);
  expect(container.querySelectorAll('[data-cycle="true"].dependency-node')).toHaveLength(3);
  expect(container.textContent).toContain("cycle");
});

test("self-dependencies travel above the opaque node instead of underneath its label", () => {
  const items = [item("self", ["self"])];
  const node = dependencyLayout(items).nodes[0];
  const path = selfLoopPath(node);
  expect(path).toContain(`L${node.x + 24},${node.y - 16}`);
  expect(path).toContain(`M${node.x + node.width},${node.y + 10}`);
  expect(path.endsWith(`${node.x},${node.y + 10}`)).toBe(true);
  const {container} = render(<DependencyGraph items={items} waiting={rows(items)} />);
  expect(container.querySelector('[data-self="true"] path')?.getAttribute("d")).toBe(path);
});

test("both dependency headings and every item link carry a graph selection", () => {
  const {container} = render(<DependencyLinks id="current" blockedBy={["before/id"]} unblocks={["after"]} />);
  expect(within(container).getByRole("link", {name:"Waits on"}).getAttribute("href")).toBe("#/graph?item=current");
  expect(within(container).getByRole("link", {name:"Unblocks"}).getAttribute("href")).toBe("#/graph?item=current");
  expect(within(container).getByRole("link", {name:"before/id"}).getAttribute("href")).toBe("#/graph?item=before%2Fid");
});

test("missing edges, classes and stale selections are explicit", () => {
  const old = item("old"); delete old.blocked_by;
  const {container, rerender} = render(<DependencyGraph items={[old]} waiting={rows([old])} />);
  expect(container.textContent).toContain("does not supply dependency edges");
  rerender(<DependencyGraph items={[item("new")]} waiting={[]} selected="gone" />);
  expect(container.textContent).toContain("not known");
  expect(container.textContent).toContain("gone is not in this snapshot");
});
