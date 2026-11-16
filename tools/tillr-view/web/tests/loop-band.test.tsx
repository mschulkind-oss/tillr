import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { ObservatoryHeaderBar } from "../src/components/ObservatoryHeaderBar";
import { EMPTY_FILTER } from "../src/models/board";
import { envelope, liveSnapshotData } from "./fixtures";

afterEach(cleanup);

test("live runs do not claim a live loop without a recorded session", () => {
  const { container } = render(<ObservatoryHeaderBar snapshot={envelope(liveSnapshotData)} error={null}
    onRefresh={() => {}} refreshing={false} live filter={EMPTY_FILTER} shown={1}
    onToggleState={() => {}} onClearFilter={() => {}} />);
  expect(container.querySelector(".loop-band")?.textContent).toContain("Loop unobserved");
  expect(container.querySelector(".loop-band")?.textContent).not.toContain("alive");
});

test.each([false, true])("loop liveness comes from the session (ended=%s)", (ended) => {
  const now = Date.now();
  const data = { ...liveSnapshotData, drain_summary: { ...liveSnapshotData.drain_summary,
    active: true, session_grace_ms: 5000, session_note: "recorded",
    session: { session_id: "s", started_at_ms: now, refreshed_at_ms: now,
      next_pass_at_ms: now + 60000, ended_at_ms: ended ? now : null } } };
  const { container } = render(<ObservatoryHeaderBar snapshot={envelope(data)} error={null}
    onRefresh={() => {}} refreshing={false} live filter={EMPTY_FILTER} shown={1}
    onToggleState={() => {}} onClearFilter={() => {}} />);
  expect(container.querySelector(".loop-band")?.textContent).toContain(ended ? "Loop stopped" : "Loop alive");
});
