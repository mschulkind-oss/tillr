import { cleanup, render, within } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { Home } from "../src/components/Home";
import { LaneStrip, LANE_QUIET_MS } from "../src/components/LaneStrip";
import type { LaneRow } from "../src/models/types";

const now = 600000;
const lanes: LaneRow[] = Array.from({ length: 10 }, (_, i) => ({
  lane_id: `lane-${i + 1}`, state: i < 4 ? "running" : i < 7 ? "settling" : "idle",
  state_since_ms: 2000, run_id: i === 9 ? "" : `run-${i}`, item_id: i === 9 ? "" : `item-${i}`,
  seat: "builder", backend: i % 2 ? "claude" : "pi", attempt: 2, outcome: i === 8 ? "landed abc123" : "exited 0",
  last_step: i === 4 ? "gate" : "", last_step_at_ms: i === 4 ? now - 40000 : null,
  latest_action: { kind: i === 1 ? "liveness" : "action", text: "bash: go test ./...", at_ms: now - 4000, clock: "observed" },
  log_grew_at_ms: i === 0 || i >= 4 ? now - 300000 : now - 4000,
}));
afterEach(cleanup);

test("Home renders every lane and counts exactly the same ten rows", () => {
  const { container } = render(<Home lanes={lanes} now={now} />);
  const strip = container.querySelector(".lane-strip")!;
  expect(strip.textContent).toContain("4 of 10 running · 3 settling · 3 idle");
  const rows = strip.querySelectorAll("li");
  expect(rows).toHaveLength(10);
  lanes.forEach((lane, i) => {
    expect(within(rows[i]).getByRole("link", { name: lane.lane_id }).getAttribute("href")).toBe(`#/lane/${lane.lane_id}`);
    expect(rows[i].textContent).not.toMatch(/gating|landing|paused/);
  });
  expect(rows[4].textContent).toContain("after gate ~40s");
  expect(rows[5].textContent).toContain("step —");
  expect(rows[8].textContent).toContain("last:");
  expect(rows[8].textContent).toContain("landed abc123");
  expect(rows[9].textContent).toContain("last run —");
});

test("quiet is running-only and its five-minute boundary is inclusive", () => {
  expect(LANE_QUIET_MS).toBe(300000);
  const { container } = render(<LaneStrip lanes={lanes} now={now} />);
  expect(container.querySelectorAll(".lane-strip__quiet")).toHaveLength(1);
  expect(container.textContent).toContain("quiet 5m");
  expect(container.textContent).toContain("log grew 4s ago · liveness signal");
  const { container: before } = render(<LaneStrip lanes={[lanes[0]]} now={now - 1} />);
  expect(before.textContent).not.toContain("quiet");
});

test("missing clocks and missing action records never become zero ages", () => {
  const { container } = render(<LaneStrip lanes={[{ ...lanes[0], state_since_ms: null, log_grew_at_ms: null, latest_action: { kind: "unrecorded", text: "", at_ms: null, clock: "no_clock" } }]} now={now} />);
  expect(container.textContent).toContain("action unrecorded");
  expect(container.textContent).not.toMatch(/quiet|0s/);
});

test("lane refusal is explicit, not an idle count", () => {
  const { container } = render(<Home lanesError={new Error("store unreadable")} />);
  expect(within(container).getByRole("alert").textContent).toContain("Lanes refused: store unreadable");
  expect(container.textContent).not.toContain("0 of 0");
});
