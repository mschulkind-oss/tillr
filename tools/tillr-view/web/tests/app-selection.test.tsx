import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test } from "vitest";
import type { GateData, GateSpan } from "../src/models/types";
import { App } from "../src/App";
import { foldedDecisions } from "../src/components/ItemRunPages";
import { blockedItemData, docData, dispatchedItemData, envelope, exchangesData, runData, timelineData, transcriptData, twoItemSnapshotData } from "./fixtures";

const start = runData.run.started_at_ms, finish = runData.run.finished_at_ms;
const check: GateSpan = { span_id: "check-span", trace_id: "trace", parent_span_id: "plan-span", check_id: "web-test", kind: "check", label: "web test", status: "ok", start_unix_nano: "0", end_unix_nano: "0", started_at_ms: finish + 100, finished_at_ms: finish + 900, waited_ms_json: '{"pool":10}', attributes_json: '{"pass_id":"pass-1"}', epistemic_state: "observed" };
const fullGate: GateData = { run: runData.run.run_id, verdict_id: "verdict-1", plan_span: { ...check, span_id: "plan-span", check_id: "plan", label: "gate", started_at_ms: finish, finished_at_ms: finish + 1000 }, spans: [check], skipped_checks: [], sources: timelineData.sources };
const fullTimeline = { ...timelineData, spans: [...timelineData.spans, ...["llm_turn", "tool_call"].map((category,i) => ({ ...timelineData.spans[0], span_id: category, parent_span_id: i === 1 ? "llm_turn" : `${runData.run.run_id}/run`, label: category, category, started_at_ms: start + 100 + i * 100, finished_at_ms: start + 500 - i * 100 }))] };
const fullRun = { ...runData, stages: runData.stages.map(s => s.stage === "stage" ? { ...s, started_at_ms: start - 100, finished_at_ms: start, epistemic_state: "observed" as const } : s) };
const originalFetch = globalThis.fetch, originalSocket = globalThis.WebSocket;
class SilentSocket { close() {} }
beforeEach(() => {
 window.history.replaceState(null, "", "/item/item-seed-06");
 globalThis.WebSocket = SilentSocket as unknown as typeof WebSocket;
 globalThis.fetch = (async input => {
  const path = String(input);
  const data = path === "/api/v1/snapshot" ? twoItemSnapshotData : path.startsWith("/api/v1/passes") ? [] : path.endsWith("/gate") ? fullGate : path.endsWith("/timeline") ? fullTimeline : path.endsWith("/exchanges") ? exchangesData : path.endsWith("/transcript") ? transcriptData : path.endsWith("/doc") ? docData : path === "/api/v1/items/item-seed-06" ? blockedItemData : path === "/api/v1/items/item-seed-02" ? dispatchedItemData : fullRun;
  return new Response(JSON.stringify(envelope(data)));
 }) as typeof fetch;
});
afterEach(() => { cleanup(); globalThis.fetch = originalFetch; globalThis.WebSocket = originalSocket; });
const mount = () => render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><App /></QueryClientProvider>);
test("a cold item URL opens a full page without an inspector reading forever", async () => {
 const view = mount(); await screen.findByText("sixth seed");
 expect(view.container.querySelector('[aria-label="Item page"]')).not.toBeNull();
 expect(view.container.querySelector('.board')).toBeNull();
 expect(view.container.textContent).not.toContain("Reading the timeline");
 expect(view.container.textContent).toContain("ac-1 — it is checked (pending)");
 expect(view.container.textContent).toContain("item-seed-02");
});
test("consecutive same-answer passes fold, but a changed answer starts a new row", () => {
 const decisions = ["hold", "hold", "dispatch", "hold"].map((action,i) => ({ decision_id: `d${i}`, item_id: "item-1", lane_id: "", run_id: "", action, reason: "ready", dispatched_at_ms: 100 - i }));
 expect(foldedDecisions(decisions).map(r => r.count)).toEqual([2,1,1]);
 expect(foldedDecisions(decisions)[0].since).toBe(99);
});
test("a run URL uses the existing timing routes and shows turn and tool spans", async () => {
 window.history.replaceState(null, "", "/run/run-item-seed-02");
 const calls: string[] = [], fetch = globalThis.fetch;
 globalThis.fetch = (async input => { calls.push(String(input)); return fetch(input); }) as typeof fetch;
 const view = mount();
 await waitFor(() => expect(view.container.querySelector('[data-span-id$="/run"] .waterfall__bar')).not.toBeNull());
 expect(calls).toContain("/api/v1/runs/run-item-seed-02/timeline");
 expect(calls).toContain("/api/v1/runs/run-item-seed-02/gate");
 expect(view.container.textContent).toContain("no clock recorded");
 await waitFor(() => expect(view.container.querySelector('[data-span-id="check-span"] .waterfall__bar')).not.toBeNull());
 const axis = finish + 1000 - (start - 100);
 for (const [id, offset, duration] of [[`${runData.run.run_id}/stage`,0,100], [`${runData.run.run_id}/run`,100,finish-start], ["llm_turn",200,400], ["tool_call",300,200], ["check-span",finish-start+200,800]] as const) {
  const bar = view.container.querySelector(`[data-span-id="${id}"] .waterfall__bar`) as HTMLElement;
  expect(parseFloat(bar.style.left)).toBeCloseTo(offset / axis * 100);
  expect(parseFloat(bar.style.width)).toBeCloseTo(duration / axis * 100);
 }
});

test("a cold exchange URL loads its payload even without the exchanges tab", async () => {
 window.history.replaceState(null, "", "/run/run-item-seed-02?exchange=run-item-seed-02%2Fgate.verdict");
 const view = mount();
 await waitFor(() => expect(view.container.textContent).toContain('"outcome":"pass"'));
 expect(view.container.textContent).not.toContain("Select an exchange");
});
test("an unknown exchange selection names the missing record", async () => {
 window.history.replaceState(null, "", "/run/run-item-seed-02?exchange=missing");
 const view = mount();
 await waitFor(() => expect(view.container.textContent).toContain("Exchange missing is not recorded"));
});
