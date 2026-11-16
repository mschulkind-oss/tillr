import { afterEach, expect, test } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import { ItemDetailPane } from "../src/components/ItemDetailPane";
import { blockedItemData } from "./fixtures";

afterEach(cleanup);

function pane(closedBlockers: string[] = [], verdicts = [] as {
  criterion_id: string; verdict: string; actor: string; actor_kind: string;
  claim_class: string; recorded_at: string;
}[]) {
  const item = { ...blockedItemData, closed_blockers: closedBlockers, verdicts };
  return render(<ItemDetailPane itemId={item.item.item_id} item={item}
    itemError={null} loading={false} onRetry={() => {}} latestRun={null}
    updatedAtMs={null} selectedRunId={null} onSelectRun={() => {}}
    doc={{ doc: undefined, error: null, pending: false, onRetry: () => {}, baseUrl: "" }} />);
}

test("each verdict names its criterion, author, authority, claim and exact time", () => {
  const verdicts = ["mechanical", "agent", "human"].map((kind, i) => ({
    criterion_id: `c${i}`, verdict: i === 0 ? "done" : "not-done",
    actor: `${kind}-author`, actor_kind: kind,
    claim_class: i === 2 ? "owner-only" : "verification-only",
    recorded_at: `2026-11-16T12:00:0${i}Z`,
  }));
  pane([], verdicts);
  const rows = within(screen.getByRole("table", { name: "Verdicts" })).getAllByRole("row");
  expect(rows).toHaveLength(4);
  for (const [i, v] of verdicts.entries()) {
    for (const value of Object.values(v)) {
      expect(within(rows[i + 1]).getAllByText(value).length).toBeGreaterThan(0);
    }
  }
});

test("legacy verdicts explicitly name an unrecorded author", () => {
  pane([], [{ criterion_id: "c1", verdict: "done", actor: "", actor_kind: "mechanical",
    claim_class: "verification-only", recorded_at: "2026-11-16T12:00:00Z" }]);
  expect(screen.getByRole("cell", { name: "not recorded" })).toBeTruthy();
});

test("one alarm line names closed blockers", () => {
  pane(["item-closed", "another-closed"]);
  const alerts = screen.getAllByRole("alert");
  expect(alerts).toHaveLength(1);
  expect(alerts[0].textContent).toContain("item-closed");
  expect(alerts[0].textContent).toContain("another-closed");
});

test("a landed blocker does not produce a closed-item alarm", () => {
  pane([]);
  expect(screen.queryByRole("alert")).toBeNull();
});
