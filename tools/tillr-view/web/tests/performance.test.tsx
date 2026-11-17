import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, within, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "../src/App";
import { fetchPassRange } from "../src/models/api";
import { PerformanceView, CheckView } from "../src/components/PerformanceView";
import { envelope, cursor, snapshotData } from "./fixtures";

const end = Date.parse(cursor.generated_at);
const passes = Array.from({length: 25}, (_, i) => ({dispatched_at_ms: end - (i + 1) * 1000, decisions: [], phases: {stage_ms: 20, dispatch_ms: null, gate_ms: 100, land_ms: 30}}));
const trend = passes.slice(0, 20).map((p, i) => ({tick_id: `tick-${i}`, at_ms: p.dispatched_at_ms, gate_ms: 100, budget_ms: i % 2 ? 200 : 50, over_budget: i % 3 === 0}));
const check = {check_id: "go-test:tillr", count: 4, sample_count: 4, median_ms: 10, p95_ms: null, max_ms: 20, waited_ms: 2, failures: 1};
const item = {item_id: "item-a", run_count: 2, usage_tokens: 100, cost_usd: .1, lower_bound: true, unrecorded_runs: 1, tokens_unrecorded_runs: 1, cost_unrecorded_runs: 1};
function setup(route = "#/performance?range=24h", empty = false, noData = false, fail = false, zeroMissing = false) {
 const fetch = vi.fn(async (path: string) => {
  const url = new URL(path, "http://localhost");
  const ranged = url.searchParams.has("from_ms") || url.searchParams.has("since");
  const hasData = !noData && !(empty && ranged);
  if (fail) return new Response(JSON.stringify({error:{code:"table-absent",message:"measurements missing"}}),{status:503});
  if (url.pathname === "/api/v1/snapshot") return new Response(JSON.stringify(envelope({...snapshotData, drain_summary:{...snapshotData.drain_summary, gate_trend:trend}})));
  if (url.pathname === "/api/v1/perf/checks") return new Response(JSON.stringify(envelope({checks: hasData ? [check] : []})));
  if (url.pathname === "/api/v1/perf/items") return new Response(JSON.stringify(envelope({items: hasData ? [zeroMissing ? {...item,unrecorded_runs:0,tokens_unrecorded_runs:0,cost_unrecorded_runs:0} : item] : []})));
  if (url.pathname === "/api/v1/passes") {
   const before = url.searchParams.has("before") ? Number(url.searchParams.get("before")) : end;
   // Deliberately short pages: the client must exhaust the cursor, not assume 50 rows.
   const page = hasData ? passes.filter(p => p.dispatched_at_ms < before).slice(0, 10) : [];
   return new Response(JSON.stringify(envelope(page)));
  }
  if (url.pathname.startsWith("/api/v1/checks/")) return new Response(JSON.stringify(envelope({check_id: check.check_id, executions: [{span_id:"span-a", run_id:"run-a", item_id:"item-a", tick_id:"tick-a", duration_ms:10, status:"FAIL", waited_ms_json:"{}"}]})));
  throw new Error(`unexpected ${path}`);
 });
 vi.stubGlobal("fetch", fetch);
 const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
 const view = render(<QueryClientProvider client={client}>{route.startsWith("#/check/") ? <CheckView route={route} paused={false} /> : <PerformanceView route={route} snapshot={envelope({...snapshotData, drain_summary:{...snapshotData.drain_summary, gate_trend: empty ? [] : trend}})} paused={false} />}</QueryClientProvider>);
 return {...view, fetch, client};
}
afterEach(() => {cleanup(); vi.unstubAllGlobals(); window.history.replaceState(null,"","/");});

test("25 gated passes use only the 20 served budgets and recorded shapes, with linked phase bars", async () => {
 const view = setup();
 await view.findByText("5 gated passes in this range have no budget served; no gate-wall bar is drawn for them.");
 const gate = within(view.getByRole("region",{name:"Gate wall per pass"}));
 const bars = gate.getAllByRole("link");
 expect(bars).toHaveLength(20);
 expect(bars.filter(b => b.textContent?.includes("▲"))).toHaveLength(7);
 for (const [i, bar] of bars.entries()) {
  expect(bar.getAttribute("href")).toBe(`#/pass/tick-${i}`);
  expect(bar.getAttribute("data-over-budget")).toBe(String(trend[i].over_budget));
  expect(bar.textContent?.includes("▲")).toBe(trend[i].over_budget);
  expect(bar.textContent).toContain(`Budget ${trend[i].budget_ms} ms`);
 }
 expect(within(view.getByRole("region",{name:"Phases per pass"})).getAllByRole("link")).toHaveLength(25);
 expect(view.getByRole("link",{name:/go-test:tillr/}).getAttribute("href")).toBe("#/check/go-test%3Atillr");
 expect(view.getByRole("link",{name:/item-a/}).getAttribute("href")).toBe("#/item/item-a");
 expect(view.getByText("≥ 100 tokens · 1 unrecorded runs")).toBeTruthy();
 expect(view.getByText(/p95 unavailable/)).toBeTruthy();
 const requests = view.fetch.mock.calls.map(([p]) => new URL(p,"http://localhost"));
 expect(requests.every(u => !u.pathname.includes("perf/passes"))).toBe(true);
 expect(requests.filter(u => u.pathname.includes("perf/")).every(u => u.searchParams.get("to_ms") === String(end) && u.searchParams.get("from_ms") === String(end-86400000))).toBe(true);
 expect(requests.filter(u => u.pathname.endsWith("passes")).every(u => u.searchParams.get("since") === String(end-86400000))).toBe(true);
 fireEvent.click(view.getByRole("link",{name:"7d"}));
 await waitFor(() => expect(window.location.hash).toBe("#/performance?range=7d"));
 view.client.clear();
});

test("complete item usage is not marked as a lower bound", async () => {
 const view=setup("#/performance?range=24h",false,false,false,true);
 const section=within(view.getByRole("region",{name:"Items"}));
 await section.findByText("100 tokens · 0 unrecorded runs");
 expect(section.queryByText(/≥/)).toBeNull();
 view.client.clear();
});

test("empty sections name all as the widest range with data, including phases beyond the trend", async () => {
 const view=setup("#/performance?range=24h",true);
 await waitFor(() => expect(view.getAllByText(/Widest range with data:/)).toHaveLength(4));
 expect(view.queryAllByText(/No recorded data in any range/)).toHaveLength(0);
 view.client.clear();
});

test("passes without recorded phases do not make either pass section nonempty", async () => {
 const view=setup("#/performance?range=24h",true);
 const original=view.fetch.getMockImplementation()!;
 view.fetch.mockImplementation(async path => {
  const url=new URL(path,"http://localhost");
  if (url.pathname === "/api/v1/passes" && url.searchParams.has("since")) {
   const before=Number(url.searchParams.get("before"));
   return new Response(JSON.stringify(envelope(passes.filter(row => row.dispatched_at_ms < before).slice(0,10).map(row => ({...row,phases:null})))));
  }
  return original(path);
 });
 await waitFor(() => expect(within(view.getByRole("region",{name:"Phases per pass"})).getByText(/Widest range with data:/)).toBeTruthy());
 expect(within(view.getByRole("region",{name:"Gate wall per pass"})).getByText(/Widest range with data:/)).toBeTruthy();
 expect(view.queryByText(/No gate budgets served/)).toBeNull();
 view.client.clear();
});

test("an entirely empty history does not invent a wider range", async () => {
 const view=setup("#/performance?range=24h",true,true);
 await waitFor(() => expect(view.getAllByText(/No recorded data in any range/)).toHaveLength(4));
 view.client.clear();
});

test("read failures are visible rather than empty ranges", async () => {
 const view=setup("#/performance?range=24h",false,false,true);
 await view.findAllByText(/measurements missing/);
 expect(view.queryByText(/No recorded data/)).toBeNull();
 view.client.clear();
});

test("all presets bound every section and survive navigation and reload in the shell", async () => {
 window.history.replaceState(null,"","/#/performance?range=24h");
 vi.stubGlobal("WebSocket",undefined);
 const view = setup();
 view.rerender(<QueryClientProvider client={view.client}><App /></QueryClientProvider>);
 await view.findByText(/5 gated passes/);
 for (const range of ["7d", "30d", "all"] as const) {
  fireEvent.click(view.getByRole("link",{name:range}));
  await waitFor(() => expect(window.location.hash).toBe(`#/performance?range=${range}`));
  await waitFor(() => expect(view.fetch.mock.calls.some(([p]) => {
   const url = new URL(p,"http://localhost");
   return url.pathname === "/api/v1/perf/items" && (range === "all" ? !url.searchParams.has("from_ms") : url.searchParams.get("from_ms") === String(end-(range === "7d" ? 7 : 30)*86400000));
  })).toBe(true));
 }
 window.history.replaceState(null,"","/#/performance?range=7d");
 fireEvent(window,new Event("popstate"));
 await waitFor(() => expect(view.getByRole("link",{name:"7d"}).getAttribute("aria-current")).toBe("page"));
 view.rerender(<QueryClientProvider client={view.client}><App key="reload" /></QueryClientProvider>);
 await view.findByText(/5 gated passes/);
 expect(view.getByRole("link",{name:"7d"}).getAttribute("aria-current")).toBe("page");
 fireEvent.click(view.getByRole("link",{name:/go-test:tillr/}));
 await view.findByText("FAIL");
 expect(window.location.hash).toBe("#/check/go-test%3Atillr");
 fireEvent.keyDown(window,{key:"j"});
 expect(window.location.hash).toBe("#/check/go-test%3Atillr");
 view.client.clear();
});

test("range paging rejects a stuck cursor rather than returning partial data", async () => {
 vi.stubGlobal("fetch",vi.fn(async () => new Response(JSON.stringify(envelope([passes[0]])))));
 await expect(fetchPassRange(end-86400000,end)).rejects.toThrow("Pass cursor did not advance");
});

test("Check bookmarks show executions and links to runs, items, passes, and checks", async () => {
 const view=setup("#/check/go-test%3Atillr");
 await view.findByText("FAIL");
 for (const [name, href] of [["run-a","#/run/run-a"],["item-a","#/item/item-a"],["tick-a","#/pass/tick-a"],["go-test:tillr","#/check/go-test%3Atillr"]]) {
  expect(view.getByRole("link",{name:new RegExp(name)}).getAttribute("href")).toBe(href);
 }
 expect(view.fetch.mock.calls[0][0]).toBe("/api/v1/checks/go-test%3Atillr");
 view.client.clear();
});

test("a check execution link highlights precisely that recorded run/span", async () => {
 const view = setup("#/check/go-test%3Atillr?run=run-a&span=span-a");
 await view.findByText("Execution span-a");
 expect(view.container.querySelector('tr[aria-selected="true"]')?.textContent).toContain("run-a");
 expect(view.getByText("Execution span-a").getAttribute("href")).toBe("#/run/run-a?span=span-a");
 view.client.clear();
});
