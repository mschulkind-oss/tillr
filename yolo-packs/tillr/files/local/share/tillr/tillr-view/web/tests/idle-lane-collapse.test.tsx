import { cleanup, fireEvent, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { within } from "@testing-library/react";
import { LoopSummary } from "../src/components/Home";
import { LaneStrip } from "../src/components/LaneStrip";
import { LaneDetail } from "../src/components/LanePage";
import { ObservatoryHeaderBar } from "../src/components/ObservatoryHeaderBar";
import { RunsPage } from "../src/components/RunsPage";
import { EMPTY_FILTER } from "../src/models/board";
import { collapseIdleLanes, IDLE_VISIBLE_MS } from "../src/models/lanes";
import type { ItemView, LaneRow, LaneView, RunSummary, SnapshotData } from "../src/models/types";
import { envelope, liveSnapshotData } from "./fixtures";

const now = 1_800_000_000_000;
const row = (lane_id: string, state: LaneRow["state"], since: number): LaneRow => ({
  lane_id, state, state_since_ms: since, run_id: "", item_id: "", seat: "", backend: "pi", attempt: 1, outcome: "", last_step: "", last_step_at_ms: null,
  log_grew_at_ms: null, latest_action: { kind: "unrecorded", text: "", at_ms: null, clock: "observed" },
} as unknown as LaneRow);
const old = now - 60 * 3600_000;
const lanes = [row("lane-1", "idle", now - 1000), row("lane-2", "running", old), ...Array.from({ length: 5 }, (_, i) => row(`lane-${i + 3}`, "idle", old)), row("express-1", "idle", old), row("desk-trunk-fixer", "idle", old)];

afterEach(cleanup);

test("configured lanes stay, busy lanes always stay, other idle lanes collapse", () => {
  const { shown, hidden } = collapseIdleLanes(lanes, ["lane-1", "express-1"], now);
  expect(shown.map(l => l.lane_id)).toEqual(["lane-1", "lane-2", "express-1"]);
  expect(hidden).toHaveLength(6);
});

test("a busy lane outside the configured count is never hidden", () => {
  const { shown } = collapseIdleLanes(lanes, ["lane-1"], now);
  expect(shown.map(l => l.lane_id)).toContain("lane-2");
});

test("without a configured list, idle age decides", () => {
  const { shown } = collapseIdleLanes(lanes, undefined, now);
  expect(shown.map(l => l.lane_id)).toEqual(["lane-1", "lane-2"]);
  expect(collapseIdleLanes([row("lane-9", "idle", now - IDLE_VISIBLE_MS + 5)], [], now).shown).toHaveLength(1);
});

test("runs page folds idle lanes into one line that expands", () => {
  const { container, getByRole } = render(<RunsPage lanes={lanes} items={[]} now={now} configured={["lane-1", "express-1"]} />);
  const rows = () => container.querySelectorAll("section[aria-label='Current lanes'] tbody tr");
  expect(rows()).toHaveLength(3);
  expect(container.textContent).toContain("6 more idle lanes (show)");
  fireEvent.click(getByRole("button", { name: "(show)" }));
  expect(rows()).toHaveLength(9);
});

test("lane strip folds idle lanes", () => {
  const { container } = render(<LaneStrip lanes={lanes} now={now} configured={["lane-1"]} />);
  expect(container.querySelectorAll(".lane-strip li")).toHaveLength(2);
  expect(container.textContent).toContain("7 more idle lanes (show)");
});

test("finished time is local, not raw UTC", () => {
  const finished = now - 3 * 3600_000;
  const run = { run_id: "r1", lane_id: "lane-1", backend: "pi", seat: "b", state: "exited", exit_code: 0, started_at_ms: 1, finished_at_ms: finished } as RunSummary;
  const item = { item_id: "i", title: "T", latest_run: run } as unknown as ItemView;
  const { container } = render(<RunsPage lanes={[]} items={[item]} now={now} />);
  expect(container.textContent).not.toMatch(/\d{4}-\d\d-\d\dT\d\d:/);
  expect(container.textContent).toContain("3h ago");
  expect(container.querySelector("td[title]")?.getAttribute("title")).toBe(new Date(finished).toLocaleString());
});

test("1 configured lane, 2 draining lanes and 20 idle historical lanes render as three labeled groups with configured-vs-busy headers", () => {
  const configuredRow: LaneRow = {
    ...row("lane-1", "running", now - 15_000),
    group: "configured",
    run_id: "run-conf-1",
    item_id: "item-conf-1",
    seat: "build",
  };
  const drainingRunning: LaneRow = {
    ...row("lane-2", "running", old),
    group: "draining",
    group_note: "finishing old work, gets no new work",
    run_id: "run-drain-2",
    item_id: "item-drain-2",
    seat: "build",
  };
  const drainingHeld: LaneRow = {
    ...row("lane-3", "idle", old),
    group: "draining",
    group_note: "finishing old work, gets no new work",
    held_item_id: "item-drain-3",
    held_run_id: "run-drain-3",
    held_reason: "candidate pin cannot be read back",
    run_id: "run-drain-3",
    item_id: "item-drain-3",
    outcome: "exited 0",
  };
  const historicalIdle: LaneRow[] = Array.from({ length: 20 }, (_, i) => ({
    ...row(`lane-${i + 4}`, "idle", old),
    group: "idle",
  }));
  const fixtureLanes: LaneRow[] = [configuredRow, drainingRunning, drainingHeld, ...historicalIdle];

  const snapshotLanes: LaneView[] = [
    { lane_id: "lane-1", state: "leased", work_state: "running", group: "configured", run_id: "run-conf-1", leased_until_ms: null },
    { lane_id: "lane-2", state: "leased", work_state: "running", group: "draining", group_note: "finishing old work, gets no new work", run_id: "run-drain-2", leased_until_ms: null },
    { lane_id: "lane-3", state: "idle", work_state: "idle", group: "draining", group_note: "finishing old work, gets no new work", held_item_id: "item-drain-3", run_id: "", leased_until_ms: null },
    ...Array.from({ length: 20 }, (_, i) => ({
      lane_id: `lane-${i + 4}`, state: "idle", work_state: "idle" as const, group: "idle" as const, run_id: "", leased_until_ms: null,
    })),
  ];
  const snap: SnapshotData = {
    ...liveSnapshotData,
    lanes: snapshotLanes,
    drain_summary: {
      ...liveSnapshotData.drain_summary,
      active: true,
      session_grace_ms: 5000,
      session: {
        session_id: "sess-1",
        started_at_ms: now - 60_000,
        refreshed_at_ms: now,
        next_pass_at_ms: now + 60_000,
        ended_at_ms: null,
        lanes: ["lane-1"],
      },
    },
  };
  const expectedHeader = "1 configured, 3 busy (1 of 1 configured + 2 draining old work) · 20 idle holding nothing";

  // LaneStrip: header + three labeled groups + folded 20 idle lanes.
  const home = render(<LaneStrip lanes={fixtureLanes} now={now} configured={snap.drain_summary.session?.lanes} />);
  const strip = home.container.querySelector(".lane-strip")!;
  expect(strip.textContent).toContain(expectedHeader);
  const confGroup = strip.querySelector('[data-lane-group="configured"]') as HTMLElement;
  const drainGroup = strip.querySelector('[data-lane-group="draining"]') as HTMLElement;
  const idleGroup = strip.querySelector('[data-lane-group="idle"]') as HTMLElement;
  expect(confGroup).not.toBeNull();
  expect(drainGroup).not.toBeNull();
  expect(idleGroup).not.toBeNull();
  expect(confGroup.textContent).toContain("Configured lanes (1)");
  expect(within(confGroup).getByRole("link", { name: "lane-1" })).toBeTruthy();
  expect(drainGroup.textContent).toContain("Draining lanes (2)");
  expect(drainGroup.textContent).toContain("finishing old work, gets no new work");
  expect(within(drainGroup).getByRole("link", { name: "lane-2" })).toBeTruthy();
  expect(within(drainGroup).getByRole("link", { name: "lane-3" })).toBeTruthy();
  expect(drainGroup.querySelectorAll("li")[0].textContent).toContain("finishing old work, gets no new work");
  expect(drainGroup.querySelectorAll("li")[1].textContent).toContain("finishing old work, gets no new work");
  expect(drainGroup.querySelectorAll("li")[1].textContent).toContain("item-drain-3");
  expect(idleGroup.textContent).toContain("Idle lanes holding nothing (20)");
  expect(idleGroup.textContent).toContain("20 more idle lanes (show)");
  expect(strip.querySelectorAll("li")).toHaveLength(3);
  fireEvent.click(within(idleGroup).getByRole("button", { name: "(show)" }));
  expect(strip.querySelectorAll("li")).toHaveLength(23);
  cleanup();

  // RunsPage: same header + three labeled groups + folded 20 idle lanes.
  const runs = render(<RunsPage lanes={fixtureLanes} items={[]} now={now} configured={["lane-1"]} />);
  const current = runs.container.querySelector("section[aria-label='Current lanes']")!;
  expect(current.textContent).toContain(expectedHeader);
  expect(current.querySelector('[data-lane-group="configured"]')?.textContent).toContain("Configured lanes (1)");
  expect(current.querySelector('[data-lane-group="draining"]')?.textContent).toContain("Draining lanes (2)");
  expect(current.querySelector('[data-lane-group="draining"]')?.textContent).toContain("finishing old work, gets no new work");
  expect(current.querySelector('[data-lane-group="idle"]')?.textContent).toContain("Idle lanes holding nothing (20)");
  expect(current.querySelectorAll("tbody tr")).toHaveLength(3);
  fireEvent.click(within(current as HTMLElement).getByRole("button", { name: "(show)" }));
  expect(current.querySelectorAll("tbody tr")).toHaveLength(23);
  cleanup();

  // Shell LoopSummary & ObservatoryHeaderBar LanePill both state configured vs busy in the same terms.
  const summary = render(<LoopSummary drain={snap.drain_summary} snapshot={snap} now={now} />);
  expect(summary.container.textContent).toContain(expectedHeader);
  cleanup();

  const bar = render(
    <ObservatoryHeaderBar
      snapshot={envelope(snap)}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live
      filter={EMPTY_FILTER}
      shown={snap.items.length}
      onToggleState={() => {}}
      onClearFilter={() => {}}
    />,
  );
  expect(bar.container.querySelector(".lane-pill")?.textContent).toContain(expectedHeader);
  cleanup();

  // Lane detail page marks a draining lane with the exact phrase.
  const detail = render(<LaneDetail lane={drainingHeld} now={now} />);
  expect(detail.container.textContent).toContain("finishing old work, gets no new work");
});
