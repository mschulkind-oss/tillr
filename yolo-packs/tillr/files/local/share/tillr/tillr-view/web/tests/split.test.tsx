import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { Home } from "../src/components/Home";
import { ItemPage } from "../src/components/ItemRunPages";
import { ItemsView } from "../src/components/ItemsPasses";
import { TrunkView } from "../src/components/TrunkView";
import type { ItemData, LandingRow, WaitingRow } from "../src/models/types";
import { snapshotData } from "./fixtures";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function renderWithQuery(node: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
}

function baseItemData(id: string, title: string, state: string): ItemData {
  return {
    item: {
      item_id: id,
      title,
      body_ref: "",
      design_doc: "",
      allowed_paths: [],
      projected_state: { state, raw_state: state, waiting_on: [] },
    },
    verdicts: [],
    closed_blockers: [],
    criteria: [],
    blocked_by: [],
    decisions: [],
    attempts: [],
    runs: [],
    owner_decisions: [],
    state_moves: [],
  };
}

test("ItemPage renders split parent section with owner words, parts, commits, and coverage", async () => {
  const payload = baseItemData("item-parent", "Parent Ask", "blocked");
  payload.split = {
    parent: {
      owner_words: "Build A and B.",
      total_parts: 2,
      required_parts: 2,
      required_landed: 1,
      progress_summary: "1 of 2 required parts landed",
      parts: [
        { label: "a", item_id: "item-part-a", title: "Part A", share: "Deliver A", required: true, state: "landed", landed_commit: "abc1234", landing_id: "land-a" },
        { label: "b", item_id: "item-part-b", title: "Part B", share: "Deliver B", follows: ["a"], required: true, state: "in-queue" },
      ],
      coverage: [
        { criterion_id: "c5", criterion_text: "A works; B works.", entries: [{ clause: "A works;", part_label: "a", part_item_id: "item-part-a" }, { clause: "B works.", part_label: "b", part_item_id: "item-part-b" }] },
        { criterion_id: "c6", criterion_text: "Together they work.", entries: [{ closing_check: true }] },
      ],
    },
  };

  vi.stubGlobal("fetch", vi.fn(async (url: string) => ({
    ok: true,
    headers: new Headers(),
    text: async () => JSON.stringify({
      snapshot: {},
      data: String(url).endsWith("/doc") ? { path: "", anchor: "", bytes: 0, text: "", questions: [], state: "absent" } : payload,
    }),
  }) as unknown as Response));

  renderWithQuery(<ItemPage id="item-parent" live={false} paused={true} baseURL="" />);
  const section = await screen.findByLabelText("Split parent");
  expect(section.textContent).toContain("1 of 2 required parts landed");
  expect(section.textContent).toContain("Build A and B.");
  expect(section.textContent).toContain("abc1234");
  expect(section.textContent).toContain("closing check");
});

test("ItemPage renders split part section with parent, share, leaves, and follows", async () => {
  const payload = baseItemData("item-part-b", "Part B", "in-queue");
  payload.split = {
    part: {
      parent_id: "item-parent",
      parent_title: "Parent Ask",
      label: "b",
      title: "Part B",
      share: "Deliver B",
      required: true,
      follows: [{ label: "a", item_id: "item-part-a", title: "Part A", state: "landed" }],
      leaves: [{ sibling_label: "a", sibling_item_id: "item-part-a", sibling_title: "Part A", sibling_state: "landed", work: "Deliver A" }],
    },
  };

  vi.stubGlobal("fetch", vi.fn(async (url: string) => ({
    ok: true,
    headers: new Headers(),
    text: async () => JSON.stringify({
      snapshot: {},
      data: String(url).endsWith("/doc") ? { path: "", anchor: "", bytes: 0, text: "", questions: [], state: "absent" } : payload,
    }),
  }) as unknown as Response));

  renderWithQuery(<ItemPage id="item-part-b" live={false} paused={true} baseURL="" />);
  const section = await screen.findByLabelText("Split part");
  expect(section.textContent).toContain("Parent Ask");
  expect(section.textContent).toContain("part b");
  expect(section.textContent).toContain("Deliver B");
  expect(section.textContent).toContain("Deliver A");
  expect(section.textContent).toContain("Part A");
});

test("Home and ItemsView render split parent counts and part parent/label", () => {
  const waiting: WaitingRow[] = [
    {
      place: 1,
      item_id: "item-part-b",
      class: "needs you",
      where: "lane",
      why: "blocked",
      mover: "owner",
      since: "1m",
      class_source: "store",
      split_part: { parent_id: "item-parent", parent_title: "Parent Ask", part_label: "b", required: true },
    },
    {
      place: 2,
      item_id: "item-parent",
      class: "waits on work",
      where: "queue",
      why: "split into 2 parts: 1 landed, 1 held; held: b (blocked; mover: owner)",
      mover: "b: owner",
      since: "2m",
      class_source: "store",
      split_parent: {
        total_parts: 2, required_parts: 2, required_landed: 1,
        landed: 1, working: 0, held: 1, queued: 0, closed: 0,
        progress_summary: "1 of 2 required parts landed",
      },
    },
  ];

  const homeView = render(<Home snapshot={snapshotData} waiting={waiting} />);
  expect(homeView.container.textContent).toContain("Parent Ask · part b");
  expect(homeView.container.textContent).toContain("split into 2 parts: 1 landed, 1 held");
  cleanup();

  const itemsSnap = {
    ...snapshotData,
    items: [
      { ...snapshotData.items[0], item_id: "item-parent", title: "Parent Ask", split_progress: "1 of 2 required parts landed" },
      { ...snapshotData.items[0], item_id: "item-part-b", title: "Part B", split_part: { parent_id: "item-parent", parent_title: "Parent Ask", part_label: "b", required: true } },
    ],
  };
  window.history.replaceState(null, "", "/items?state=unlanded");
  const itemsView = render(<ItemsView snapshot={itemsSnap} waiting={waiting} writesEnabled={false} />);
  expect(itemsView.container.textContent).toContain("Parent Ask · part b");
  expect(itemsView.container.textContent).toContain("1 of 2 required parts landed");
});

test("TrunkView reports part landing under parent title", async () => {
  const rows: LandingRow[] = [
    {
      kind: "landing",
      landing_id: "land-a",
      item_id: "item-part-a",
      parent_id: "item-parent",
      parent_title: "Parent Ask",
      part_label: "a",
      result: "landed",
      trunk_before: "111",
      trunk_after: "222",
      event_seq: 10,
      at_ms: 1700000000000,
      epistemic_state: "recorded",
    },
  ];

  vi.stubGlobal("fetch", vi.fn(async () => ({
    ok: true,
    headers: new Headers(),
    text: async () => JSON.stringify({ snapshot: {}, data: { rows } }),
  }) as unknown as Response));

  renderWithQuery(<TrunkView route="/trunk" paused={true} live={false} />);
  const parentLink = await screen.findByText(/Parent Parent Ask · part a/);
  expect(parentLink.getAttribute("href")).toBe("/item/item-parent");
});
