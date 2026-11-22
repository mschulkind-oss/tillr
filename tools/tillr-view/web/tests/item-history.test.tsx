import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { ItemTimeline, VerifyRefusals } from "../src/components/ItemRunPages";
import { dispatchedItemData as itemData } from "./fixtures";

afterEach(cleanup);

test("item history shows graph changes and refused proposals with the proposing run", () => {
  const item = { ...itemData, graph_revisions: [
    { revision_id: 1, proposing_run: "plan-run", proposing_node: "open", target: "write", workflow: "replacement@v1", rule: "", detail: "", before_nodes: 4, after_nodes: 5, recorded_at_ms: 1000 },
    { revision_id: 2, proposing_run: "second-run", proposing_node: "plan-again", target: "write", workflow: "replacement@v1", rule: "proposal-target-started", detail: "write has a run", before_nodes: 5, after_nodes: 5, recorded_at_ms: 2000 },
  ] };
  render(<ItemTimeline item={item} />);
  const history = screen.getByRole("region", { name: "Item history" });
  expect(history.textContent).toContain("Graph changed by open: 4 → 5 nodes");
  expect(history.textContent).toContain("Graph proposal refused by proposal-target-started: write has a run");
  expect(within(history).getByRole("link", { name: "plan-run" }).getAttribute("href")).toContain("plan-run");
  expect(within(history).getByRole("link", { name: "second-run" })).toBeTruthy();
});

test("same-millisecond state moves follow their numeric event sequence", () => {
  const item = { ...itemData, state_moves: [
    { event_seq: 10, kind: "item.update", at_ms: 1000, from: "closed", to: "in-queue", action: "reopen", reason: "wanted again", actor: "owner" },
    { event_seq: 9, kind: "item.update", at_ms: 1000, from: "in-queue", to: "closed", action: "close", reason: "not wanted", actor: "owner" },
  ] };
  render(<ItemTimeline item={item} />);
  const rows = within(screen.getByRole("region", { name: "Item history" })).getAllByRole("listitem");
  expect(rows.map(row => row.textContent)).toEqual([expect.stringContaining("not wanted"), expect.stringContaining("wanted again")]);
});

test("item history interleaves every run, verdict, owner ruling and state move oldest first", () => {
  const item = { ...itemData, runs: [
    { run_id: "run-b", lane_id: "lane-a", backend: "claude", state: "finished", exit_code: 1, started_at_ms: 2000, finished_at_ms: 2500, end_cause: "failed-to-start", usage_tokens: null, cost_usd: null },
    { run_id: "run-a", lane_id: "lane-a", backend: "pi", state: "finished", exit_code: 0, started_at_ms: 1000, finished_at_ms: 1500, end_cause: "normal-exit", usage_tokens: 42, cost_usd: 0.5 },
  ], verdicts: [{ criterion_id: "ac-1", verdict: "not-done", actor: "reviewer", actor_kind: "human", claim_class: "verification-only", recorded_at: "1970-01-01T00:00:03Z", reason: "no candidate", evidence: ["run:run-b"] }],
    owner_decisions: [{ decision_id: "ruling", text: "try again", decided_by: "owner", decided_at_ms: 3500, scope_kind: "items", scope_ref: itemData.item.item_id, status: "active", supersedes: "", session_id: "session" }],
    state_moves: [
      { event_seq: 6, kind: "item.update", at_ms: 5000, from: "closed", to: "in-queue", action: "reopen", reason: "wanted again", actor: "owner" },
      { event_seq: 5, kind: "item.update", at_ms: 4000, from: "in-queue", to: "closed", action: "close", reason: "not wanted", actor: "owner" },
    ],
  };
  render(<ItemTimeline item={item} />);
  const history = screen.getByRole("region", { name: "Item history" });
  const entries = within(history).getAllByRole("listitem");
  expect(entries.map(row => row.textContent)).toEqual([
    expect.stringContaining("run-a"), expect.stringContaining("run-b"), expect.stringContaining("no candidate"),
    expect.stringContaining("try again"), expect.stringContaining("not wanted"), expect.stringContaining("wanted again"),
  ]);
  expect(entries[0].textContent).toContain("pi · normal-exit");
  expect(entries[0].textContent).toContain("42 tokens");
  expect(history.textContent).not.toMatch(/\$|cost_usd|spend not recorded/);
  expect(entries[0].textContent).toContain("1970-01-01T00:00:01.500Z");
  expect(entries[1].textContent).toContain("tokens not recorded");
  expect(entries[2].textContent).toContain("reviewer");
  expect(entries[2].textContent).toContain("run:run-b");
  expect(entries[4].textContent).toContain("in-queue → closed");
  expect(entries[5].textContent).toContain("reopen");
  expect(within(history).getByRole("link", { name: "run-a" }).getAttribute("href")).toContain("run-a");
});

const none = { next_run_id: null, next_attempt: null, next_step: null, next_seat: null, next_event_seq: null, next_at_ms: null };
const refusals = [
  { event_seq: 2, at_ms: 2000, run_id: "verify-1", attempt: 1, detail: "c1: missing test\nc2: wrong file", replan: false, next_run_id: "work-2", next_attempt: 1, next_step: "work", next_seat: "build", next_event_seq: 3, next_at_ms: 3000 },
  { event_seq: 5, at_ms: 5000, run_id: "verify-2", attempt: 2, detail: "the plan was wrong", replan: true, next_run_id: "plan-3", next_attempt: 3, next_step: "plan", next_seat: "plan", next_event_seq: 6, next_at_ms: 6000 },
  { event_seq: 7, at_ms: 7000, run_id: "verify-3", attempt: 3, detail: "c1: still red", replan: false, ...none },
];

test("the item page shows each verify refusal, its findings and which step ran next", () => {
  render(<VerifyRefusals refusals={refusals} />);
  const section = screen.getByRole("region", { name: "Verify refusals" });
  const rows = within(section).getAllByRole("listitem");
  expect(rows).toHaveLength(3);
  expect(rows[0].textContent).toContain("Next step: work");
  expect(rows[0].textContent).toContain("c1: missing test\nc2: wrong file");
  expect(rows[1].textContent).toContain("Next step: plan");
  expect(rows[1].textContent).toContain("replan");
  expect(rows[0].textContent).not.toContain("replan");
  expect(rows[0].textContent).toContain("attempt 1");
  expect(rows[2].textContent).toContain("No next step started yet");
  expect(within(rows[0]).getByRole("link", { name: "verify-1" })).toBeTruthy();
  expect(within(rows[0]).getByRole("link", { name: "work-2" })).toBeTruthy();
  expect(within(rows[1]).getByRole("link", { name: "plan-3" })).toBeTruthy();
});

test("verify refusals show recorded counts beside their limits, but not for older refusals", () => {
  render(<VerifyRefusals refusals={[
    { ...refusals[0], candidate_size: { files: 3, added_lines: 71, file_limit: 8, added_line_limit: 100 } },
    { ...refusals[1], candidate_size: { files: 9, added_lines: 80, file_limit: 9, added_line_limit: 80 } },
    { ...refusals[2], candidate_size: { files: 0, added_lines: 0, file_limit: 27, added_line_limit: 1200 } },
    { ...refusals[2], event_seq: 8, detail: "older findings" },
  ]} />);
  const rows = within(screen.getByRole("region", { name: "Verify refusals" })).getAllByRole("listitem");
  expect(rows[0].textContent).toContain("3 files (limit 8) · 71 added lines (limit 100)");
  expect(rows[1].textContent).toContain("9 files (limit 9) · 80 added lines (limit 80)");
  expect(rows[2].textContent).toContain("0 files (limit 27) · 0 added lines (limit 1200)");
  expect(rows[3].textContent).toContain("older findings");
  expect(rows[3].textContent).not.toMatch(/Candidate size|limit|added lines/);
});

test("no verify refusals renders no section", () => {
  render(<VerifyRefusals refusals={[]} />);
  expect(screen.queryByRole("region", { name: "Verify refusals" })).toBeNull();
});

test("item history lists each verify refusal in time order with the next step", () => {
  const item = { ...itemData, runs: [], verdicts: [], owner_decisions: [], state_moves: [], verify_refusals: [refusals[1], refusals[0]] };
  render(<ItemTimeline item={item} />);
  const rows = within(screen.getByRole("region", { name: "Item history" })).getAllByRole("listitem");
  expect(rows).toHaveLength(2);
  expect(rows[0].textContent).toContain("c1: missing test");
  expect(rows[0].textContent).toContain("Next step: work");
  expect(rows[1].textContent).toContain("Next step: plan");
});
