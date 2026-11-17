import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { ItemTimeline } from "../src/components/ItemRunPages";
import { dispatchedItemData as itemData } from "./fixtures";

afterEach(cleanup);

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
