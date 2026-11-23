import { cleanup, fireEvent, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { Home } from "../src/components/Home";
import { RunsPage } from "../src/components/RunsPage";
import { collapseIdleLanes, IDLE_VISIBLE_MS } from "../src/models/lanes";
import type { ItemView, LaneRow, RunSummary } from "../src/models/types";

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

test("home lane strip folds the same lanes", () => {
  const { container } = render(<Home lanes={lanes} now={now} snapshot={{ drain_summary: { session: { lanes: ["lane-1"] } } } as never} />);
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
