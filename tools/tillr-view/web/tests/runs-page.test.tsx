import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test } from "vitest";
import { App } from "../src/App";
import { RunsPage, finishedRuns } from "../src/components/RunsPage";
import type { ItemView, LaneRow, RunSummary } from "../src/models/types";
import { envelope, twoItemSnapshotData } from "./fixtures";

const base = { state_since_ms: 1000, run_id: "", item_id: "", seat: "", backend: "pi", attempt: 1, outcome: "", last_step: "", last_step_at_ms: null, log_grew_at_ms: null,
  latest_action: { kind: "unrecorded" as const, text: "", at_ms: null, clock: "observed" } };
const lanes: LaneRow[] = [
  { ...base, lane_id: "lane-1", state: "idle" },
  { ...base, lane_id: "express-1", state: "running", run_id: "run-x", item_id: "item-x", seat: "fixer" },
  { ...base, lane_id: "lane-2", state: "running", run_id: "run-b", item_id: "item-b", seat: "builder", state_since_ms: 1000,
    nodes: [{ node_id: "judge", run_id: "run-b2", attempt: 1, seat: "judge", state: "running" }] },
];
const run = (id: string, finished: number): RunSummary => ({ run_id: id, lane_id: "lane-2", backend: "pi", seat: "builder", state: "exited", exit_code: 0, started_at_ms: 1, finished_at_ms: finished, cost_usd: null, usage_tokens: null });
const item = (id: string, latest_run: RunSummary | null) => ({ item_id: id, title: `Title of ${id}`, latest_run }) as unknown as ItemView;

afterEach(cleanup);

test("rows link lane, item by title, and run; busy lanes come before idle", () => {
  const { container } = render(<RunsPage lanes={lanes} items={[item("item-b", null), item("item-x", null)]} now={61000} />);
  const rows = container.querySelectorAll("section[aria-label='Current lanes'] tbody tr");
  expect([...rows].map(r => r.getAttribute("data-state"))).toEqual(["running", "running", "idle"]);
  const first = within(rows[0] as HTMLElement);
  expect(first.getByRole("link", { name: /express-1/ }).getAttribute("href")).toBe("/lane/express-1");
  expect(first.getByRole("link", { name: "Title of item-x" }).getAttribute("href")).toBe("/item/item-x");
  expect(first.getByRole("link", { name: /run-x/ }).getAttribute("href")).toBe("/run/run-x?tab=transcript");
  expect(rows[0].textContent).toContain("~1m");
  expect(rows[1].textContent).toContain("judge · judge");
  expect(rows[2].textContent).toContain("idle");
});

test("a failed read is a failure, not an empty list", () => {
  const { container } = render(<RunsPage lanes={undefined} lanesError={new Error("boom")} items={[]} now={0} />);
  expect(screen.getByRole("alert").textContent).toBe("could not read the store: boom");
  expect(container.textContent).not.toContain("No lanes recorded");
});

test("finished runs are the newest twenty, linked", () => {
  const items = Array.from({ length: 25 }, (_, i) => item(`i${i}`, run(`r${i}`, 1000 + i)));
  expect(finishedRuns(items).map(r => r.run.run_id)[0]).toBe("r24");
  expect(finishedRuns(items)).toHaveLength(20);
  const { container } = render(<RunsPage lanes={[]} items={items} now={0} />);
  const link = within(container.querySelector("section[aria-label='Recently finished runs']")!).getAllByRole("link", { name: /r24/ })[0];
  expect(link.getAttribute("href")).toBe("/run/r24?tab=transcript");
});

const originalFetch = globalThis.fetch, originalSocket = globalThis.WebSocket;
class SilentSocket { close() {} }
let lanesBody: () => Response;
beforeEach(() => {
  window.history.replaceState(null, "", "/runs");
  globalThis.WebSocket = SilentSocket as unknown as typeof WebSocket;
  lanesBody = () => new Response(JSON.stringify(envelope(lanes)));
  globalThis.fetch = (async input => {
    const path = String(input);
    if (path === "/api/v1/snapshot") return new Response(JSON.stringify(envelope(twoItemSnapshotData)));
    if (path.startsWith("/api/v1/lanes")) return lanesBody();
    return new Response(JSON.stringify(envelope([])));
  }) as typeof fetch;
});
afterEach(() => { globalThis.fetch = originalFetch; globalThis.WebSocket = originalSocket; });
const mount = () => render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><App /></QueryClientProvider>);

test("/runs routes to the page, not the unbuilt fallback", async () => {
  const view = mount();
  await waitFor(() => expect(view.container.querySelector("[aria-label='Runs page']")).not.toBeNull());
  await screen.findByRole("link", { name: /express-1/ });
  expect(view.container.textContent).not.toContain("not built yet");
});

test("a lanes read that fails shows the failure through the route", async () => {
  lanesBody = () => new Response("down", { status: 500 });
  mount();
  await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/^could not read the store: /));
});
