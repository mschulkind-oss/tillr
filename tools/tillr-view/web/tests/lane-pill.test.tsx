// The header's lane pill (design §4.4, ledger `D-OBS12`): a stopped board says its lanes
// are idle instead of wearing a `working` glyph over no live run. The pill is always
// drawn, so the answer to "is a lane working" is present at zero as well as at two — and
// the NowBand stays absent at zero, because a band with nothing in it is the
// absence-drawn-as-a-value the truthfulness table forbids.

import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, test } from "vitest";

import { ObservatoryHeaderBar } from "../src/components/ObservatoryHeaderBar";
import { NowBand } from "../src/components/NowBand";
import { EMPTY_FILTER } from "../src/models/board";
import { envelope, liveSnapshotData, snapshotData } from "./fixtures";
import type { SnapshotData } from "../src/models/types";

afterEach(cleanup);

function header(data: SnapshotData) {
  return render(
    <ObservatoryHeaderBar
      snapshot={envelope(data)}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live
      filter={EMPTY_FILTER}
      shown={data.items.length}
      onToggleState={() => {}}
      onClearFilter={() => {}}
    />,
  );
}

describe("the lane pill is drawn at zero as well as at work", () => {
  test("express and desk leases never inflate the ordinary cap", () => {
    const data: SnapshotData = { ...liveSnapshotData,
      counts: { by_state: {}, dispatchable: 0, blocked: 0, runs_live: 9 },
      lanes: [
        ...Array.from({ length: 10 }, (_, i) => ({ lane_id: `lane-${i+1}`, state: i < 7 ? "leased" : "idle", work_state: i < 7 ? "running" as const : "idle" as const, run_id: "", leased_until_ms: null })),
        { lane_id: "express-1", state: "leased", work_state: "running", run_id: "", leased_until_ms: null },
        { lane_id: "express-2", state: "idle", work_state: "idle", run_id: "", leased_until_ms: null },
        { lane_id: "desk-doc", state: "leased", work_state: "running", run_id: "", leased_until_ms: null },
      ] };
    expect(header(data).container.querySelector(".lane-pill")?.textContent).toContain("7 of 10 running, 1 of 2 express");
  });
  test("settling leases are not counted as running in either pool", () => {
    const data: SnapshotData = { ...liveSnapshotData, lanes: [
      { lane_id: "lane-1", state: "leased", work_state: "settling", run_id: "finished-1", leased_until_ms: null },
      { lane_id: "express-1", state: "leased", work_state: "settling", run_id: "finished-2", leased_until_ms: null },
      { lane_id: "express-2", state: "leased", work_state: "running", run_id: "live-1", leased_until_ms: null },
    ] };
    expect(header(data).container.querySelector(".lane-pill")?.textContent).toContain("0 of 1 running, 1 of 2 express");
  });
  test("desk-only work does not claim the whole drain is idle", () => {
    const data: SnapshotData = { ...liveSnapshotData, lanes: [
      { lane_id: "lane-1", state: "idle", work_state: "idle", run_id: "", leased_until_ms: null },
      { lane_id: "desk-doc", state: "leased", work_state: "running", run_id: "desk-live", leased_until_ms: null },
    ] };
    const pill = header(data).container.querySelector(".lane-pill");
    expect(pill?.textContent).toContain("0 of 1 running · idle");
    expect(pill?.getAttribute("title")).toContain("ordinary and express pools are idle");
    expect(pill?.getAttribute("title")).not.toContain("the drain is idle");
  });
  test("an older server's leases do not invent running counts", () => {
    const data: SnapshotData = { ...liveSnapshotData, lanes: [
      { lane_id: "lane-1", state: "leased", run_id: "old-run", leased_until_ms: null },
    ] };
    expect(header(data).container.querySelector(".lane-pill")?.textContent).toContain("work state unrecorded");
  });
  test("a stopped board says 0 of 1 running · idle", () => {
    const stopped: SnapshotData = {
      ...snapshotData,
      counts: { by_state: {}, dispatchable: 0, blocked: 0, runs_live: 0 },
      lanes: [{ lane_id: "lane-1", state: "idle", run_id: "", leased_until_ms: null }],
    };
    const { container } = header(stopped);
    const pill = container.querySelector(".lane-pill");
    expect(pill).not.toBeNull();
    expect(pill?.textContent).toContain("0 of 1 running · idle");
    expect(pill?.className).toContain("lane-pill--idle");

    // The band is still absent: the pill is the answer to "is anything running", and the
    // band would be an empty copy of a run that does not exist.
    const { container: bandContainer } = render(<NowBand snapshot={stopped} now={1} />);
    expect(bandContainer.querySelector(".now-band")).toBeNull();
  });

  test("two working lanes say 2 of 2 running", () => {
    const busy: SnapshotData = {
      ...liveSnapshotData,
      counts: { by_state: {}, dispatchable: 0, blocked: 0, runs_live: 2 },
      lanes: [
        { lane_id: "lane-1", state: "leased", work_state: "running", run_id: "run-live-1", leased_until_ms: 1762979580000 },
        { lane_id: "lane-2", state: "leased", work_state: "running", run_id: "run-live-2", leased_until_ms: 1762979580000 },
      ],
    };
    const { container } = header(busy);
    const pill = container.querySelector(".lane-pill");
    expect(pill?.textContent).toContain("2 of 2 running");
    expect(pill?.textContent).not.toContain("idle");
    expect(pill?.className).toContain("lane-pill--running");
  });

  test("the pill is present before the first read answers", () => {
    const { container } = render(
      <ObservatoryHeaderBar
        snapshot={undefined}
        error={null}
        onRefresh={() => {}}
        refreshing={false}
        live={false}
        filter={EMPTY_FILTER}
        shown={0}
        onToggleState={() => {}}
        onClearFilter={() => {}}
      />,
    );
    const pill = container.querySelector(".lane-pill");
    expect(pill).not.toBeNull();
    // An unknown count is `—`, never a zero that would read as an idle board.
    expect(pill?.textContent).toContain("— of — running");
  });

  test("running_work summary shows R of L running, owner-awaited count, and never counts idle worktrees as slots", () => {
    const idleWithWorktrees: SnapshotData = {
      ...snapshotData,
      running_work: {
        ordinary_running: 0,
        express_running: 0,
        command_running: 0,
        agent_limit: 4,
        worktrees: { on_disk: 5, in_use: 0, idle: 5, count_limit: 8, worktree_bytes: 1000, scratch_bytes: 2000, total_bytes: 3000, disk_budget_bytes: 10000, run_log_bytes: 100 },
      },
    };
    expect(header(idleWithWorktrees).container.querySelector(".lane-pill")?.textContent).toContain("0 of 4 running · idle");

    const activeWithExpress: SnapshotData = {
      ...liveSnapshotData,
      running_work: {
        ordinary_running: 2,
        express_running: 1,
        command_running: 1,
        agent_limit: 4,
        worktrees: { on_disk: 5, in_use: 3, idle: 2, count_limit: 8, worktree_bytes: 1000, scratch_bytes: 2000, total_bytes: 3000, disk_budget_bytes: 10000, run_log_bytes: 100 },
      },
    };
    const pill = header(activeWithExpress).container.querySelector(".lane-pill");
    expect(pill?.textContent).toContain("2 of 4 running, 1 owner-awaited");
    expect(pill?.textContent).not.toContain("idle");
  });
});
