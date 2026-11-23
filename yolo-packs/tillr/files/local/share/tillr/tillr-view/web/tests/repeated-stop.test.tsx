import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { LoopBand } from "../src/components/ObservatoryHeaderBar";
import { LoopSummary } from "../src/components/Home";
import { snapshotData } from "./fixtures";

afterEach(cleanup);

const now = Date.parse("2026-11-19T21:25:00.000Z");
const session = {
  session_id: "sess-restarted",
  started_at_ms: now - 5000,
  refreshed_at_ms: now,
  next_pass_at_ms: now + 60000,
  ended_at_ms: null,
};

const repeatedStop = {
  count: 5,
  reason: "fault",
  detail: "cannot read landing offer for run-1 while its processes survive",
  item_id: "item-1",
  step: "land",
  first_at_ms: Date.parse("2026-11-19T20:15:00.000Z"),
  last_at_ms: Date.parse("2026-11-19T21:20:00.000Z"),
  sentence: "The loop stopped at land on item-1 because cannot read landing offer for run-1 while its processes survive.",
  next: "Owner resolves the failure described above, then restarts with tillr-drain loop.",
};

test("LoopBand and LoopSummary render stuck-drain notice when stopped after repeated exits", () => {
  const drain = {
    ...snapshotData.drain_summary,
    active: false,
    session: { ...session, ended_at_ms: repeatedStop.last_at_ms },
    repeated_stop: repeatedStop,
  };
  const { container } = render(<><LoopBand drain={drain} now={now} /><LoopSummary drain={drain} now={now} /></>);
  const band = container.querySelector(".loop-band")?.textContent ?? "";
  const summary = container.querySelector(".loop-summary__repeated")?.textContent ?? "";

  for (const text of [band, summary]) {
    expect(text).toContain("exited 5 times in a row");
    expect(text).toContain("fault: cannot read landing offer for run-1 while its processes survive");
    expect(text).toContain(`${new Date(repeatedStop.first_at_ms).toLocaleString()}..${new Date(repeatedStop.last_at_ms).toLocaleString()}`);
    expect(text).toContain(repeatedStop.next);
  }
  expect(band).toContain("stuck drain");
  expect(summary).toContain("Stuck drain:");
});

test("LoopBand and LoopSummary preserve repeated-exit notice while restarted session is alive", () => {
  const drain = {
    ...snapshotData.drain_summary,
    active: true,
    session,
    heartbeat_interval_ms: 1000,
    repeated_stop: repeatedStop,
  };
  const { container } = render(<><LoopBand drain={drain} now={now} /><LoopSummary drain={drain} now={now} /></>);
  const band = container.querySelector(".loop-band")?.textContent ?? "";
  const summary = container.querySelector(".loop-summary__repeated")?.textContent ?? "";

  expect(band).toContain("Loop alive");
  expect(band).toContain("stuck drain (restarted after repeated exits): exited 5 times in a row");
  expect(summary).toContain("Stuck drain (restarted after repeated exits):");
  expect(summary).toContain("exited 5 times in a row");
});

test("LoopBand and LoopSummary omit repeated-exit notice when repeated_stop is absent", () => {
  const drain = {
    ...snapshotData.drain_summary,
    active: true,
    session,
    heartbeat_interval_ms: 1000,
    repeated_stop: null,
  };
  const { container } = render(<><LoopBand drain={drain} now={now} /><LoopSummary drain={drain} now={now} /></>);
  expect(container.querySelector(".loop-band__repeated")).toBeNull();
  expect(container.querySelector(".loop-summary__repeated")).toBeNull();
});
