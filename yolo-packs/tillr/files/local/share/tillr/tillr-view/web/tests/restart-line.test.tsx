import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { ObservatoryHeaderBar } from "../src/components/ObservatoryHeaderBar";
import { EMPTY_FILTER } from "../src/models/board";
import { envelope, liveSnapshotData } from "./fixtures";

afterEach(cleanup);

function header(restarts?: Record<string, unknown>) {
  const data = { ...liveSnapshotData, drain_summary: { ...liveSnapshotData.drain_summary, restarts } };
  return render(<ObservatoryHeaderBar snapshot={envelope(data as typeof liveSnapshotData)} error={null}
    onRefresh={() => {}} refreshing={false} live filter={EMPTY_FILTER} shown={1}
    onToggleState={() => {}} onClearFilter={() => {}} />);
}

test("the header names the latest restart answer of each role", () => {
  const { container } = header({
    drain: { request_id: "restart-drain-1", role: "drain", answer: "restarted", from_build: "a", to_build: "b", at: "2026-11-17T00:00:00Z" },
    view: { request_id: "restart-view-1", role: "view", answer: "refused", from_build: "a", to_build: "digest c", detail: "the store is unreadable", at: "2026-11-17T00:00:00Z" },
  });
  const text = container.querySelector(".head-restart")?.textContent ?? "";
  expect(text).toContain("drain restart: restarted");
  expect(text).toContain("view restart: refused — the store is unreadable");
});

test("a role nobody asked to restart says so", () => {
  const { container } = header(undefined);
  expect(container.querySelector(".head-restart")?.textContent).toContain("drain restart: none requested");
});
