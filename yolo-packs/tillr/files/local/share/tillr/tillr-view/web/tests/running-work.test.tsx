import { cleanup, render, within } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { Home, LoopSummary } from "../src/components/Home";
import { ItemGraph } from "../src/components/ItemGraph";
import { RunningWork } from "../src/components/RunningWork";
import type { ItemGraphAttempt, RunningWorkPayload, SnapshotData } from "../src/models/types";
import { liveSnapshotData } from "./fixtures";

afterEach(cleanup);

const now = 1_800_000_000_000;

test("RunningWork renders summary line with R of L, worktree count/disk bounds, and one row per running node", () => {
  const payload: RunningWorkPayload = {
    summary: {
      ordinary_running: 1,
      express_running: 1,
      command_running: 0,
      agent_limit: 4,
      worktrees: {
        on_disk: 3,
        in_use: 2,
        idle: 1,
        count_limit: 8,
        worktree_bytes: 64 * 1024 * 1024,
        scratch_bytes: 512 * 1024 * 1024,
        total_bytes: 576 * 1024 * 1024,
        disk_budget_bytes: 16 * 1024 * 1024 * 1024,
        run_log_bytes: 1024 * 1024,
      },
    },
    rows: [
      {
        run_id: "run-1",
        item_id: "item-alpha",
        item_title: "Build alpha",
        node_id: "n2",
        node_kind: "agent",
        seat: "build",
        backend: "pi",
        express: false,
        started_at_ms: now - 45_000,
        state_since_ms: now - 45_000,
        log_grew_at_ms: now - 5_000,
        latest_action: { kind: "action", text: "bash: go test" },
        spend: { turns: 3, tokens: 1200, cost_usd: 0.15 },
      },
      {
        run_id: "run-2",
        item_id: "item-beta",
        item_title: "Urgent fix",
        node_id: "n1",
        node_kind: "agent",
        seat: "design",
        backend: "claude",
        express: true,
        started_at_ms: now - 12_000,
        state_since_ms: now - 12_000,
        log_grew_at_ms: now - 2_000,
        latest_action: { kind: "liveness", text: "log grew 2s ago" },
        spend: { turns: 1, tokens: 400, cost_usd: 0.05 },
      },
    ],
  };

  const { container } = render(<Home runningWork={payload} now={now} />);
  const section = within(container).getByRole("region", { name: "Running work" });
  expect(section.textContent).toContain("1 of 4 running, 1 owner-awaited");
  expect(section.textContent).toContain("3 of 8 worktrees on disk");
  expect(section.textContent).toContain("576.0 MiB of 16.0 GiB");
  const rows = section.querySelectorAll("tbody tr");
  expect(rows).toHaveLength(2);
  expect(within(rows[0] as HTMLElement).getByRole("link", { name: "item-alpha" }).getAttribute("href")).toBe("/item/item-alpha");
  expect(rows[0].textContent).toContain("n2");
  expect(rows[0].textContent).toContain("agent");
  expect(rows[0].textContent).toContain("build");
  expect(rows[0].textContent).toContain("pi");
  expect(rows[0].textContent).toContain("45s");
  expect(rows[0].textContent).toContain("log grew 5s ago");
  expect(rows[0].textContent).toContain("bash: go test");
  expect(rows[0].textContent).toContain("$0.15");
  expect(rows[1].textContent).toContain("owner-awaited");
});

test("RunningWork renders no rows when nothing is running, even with idle worktrees on disk", () => {
  const idlePayload: RunningWorkPayload = {
    summary: {
      ordinary_running: 0,
      express_running: 0,
      command_running: 0,
      agent_limit: 4,
      worktrees: {
        on_disk: 3,
        in_use: 0,
        idle: 3,
        count_limit: 8,
        worktree_bytes: 1024,
        scratch_bytes: 2048,
        total_bytes: 3072,
        disk_budget_bytes: 16 * 1024 * 1024 * 1024,
        run_log_bytes: 0,
      },
    },
    rows: [],
  };

  const { container } = render(<RunningWork data={idlePayload} now={now} />);
  expect(container.textContent).toContain("0 of 4 running · idle");
  expect(container.textContent).toContain("3 of 8 worktrees on disk");
  expect(container.querySelectorAll("tbody tr")).toHaveLength(0);
  expect(container.querySelector("table")).toBeNull();
});

test("LoopSummary displays running-work summary when snapshot.running_work is present", () => {
  const snap: SnapshotData = {
    ...liveSnapshotData,
    running_work: {
      ordinary_running: 2,
      express_running: 0,
      command_running: 0,
      agent_limit: 4,
      worktrees: {
        on_disk: 2,
        in_use: 2,
        idle: 0,
        count_limit: 8,
        worktree_bytes: 1024,
        scratch_bytes: 1024,
        total_bytes: 2048,
        disk_budget_bytes: 1048576,
        run_log_bytes: 0,
      },
    },
  };
  const { container } = render(<LoopSummary drain={snap.drain_summary} snapshot={snap} passes={[]} error={null} now={now} />);
  expect(container.textContent).toContain("running: 2 of 4 running");
});

test("ItemGraph displays node execution records (segments, logs, lease, outputs, ending, spend)", () => {
  const attempts: ItemGraphAttempt[] = [
    {
      attempt: 1,
      nodes: [
        {
          id: "n1",
          parent: "",
          kind: "agent",
          seat: "build",
          command_ref: "",
          command: null,
          access: "write",
          state: "running",
          level: 0,
          runs: ["run-exec-1"],
          executions: [
            {
              run_id: "run-exec-1",
              backend: "pi",
              seat: "build",
              segment_count: 2,
              segments: [{ ordinal: 1, started_at_ms: 1000 }, { ordinal: 2, started_at_ms: 2500 }],
              log: { path: "run-exec-1.log", present: true, bytes: 2048, transcript_url: "#/run/run-exec-1" },
              lease: { lane_id: "item-alpha", active: true, lease_no_expiry: true },
              ending: { state: "running", started_at_ms: 1000 },
              spend: { turns: 2, tokens: 1500, cost_usd: 0.12, live: true },
            },
          ],
          children: {},
          outputs: [{ name: "proposal", type: "json", run_id: "run-exec-1", body: "{\"ok\":true}" }],
        },
      ],
    },
  ];

  const { container } = render(<ItemGraph attempts={attempts} />);
  const node = within(container).getByRole("article", { name: "Node n1" });
  expect(node.textContent).toContain("segments: 2");
  expect(node.textContent).toContain("ending: running");
  expect(node.textContent).toContain("run-exec-1.log (2.0 KiB)");
  expect(within(node).getByRole("link", { name: "transcript" }).getAttribute("href")).toBe("#/run/run-exec-1");
  expect(node.textContent).toContain("lease: active (item-alpha, no expiry)");
  expect(node.textContent).toContain("spend: live 1,500 tok · $0.12");
  expect(node.textContent).toContain("proposal · json");
});
