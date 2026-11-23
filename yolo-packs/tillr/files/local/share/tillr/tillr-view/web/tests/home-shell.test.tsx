import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "../src/App";
import type { LaneRow } from "../src/models/types";
import { envelope, snapshotData } from "./fixtures";

class SilentSocket { close() {} }
afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });

function mount(refused = false, lanes: LaneRow[] = []) {
  window.history.replaceState(null, "", "/#/");
  vi.stubGlobal("WebSocket", SilentSocket);
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    // Route on the path: the lanes route also takes `?history=<lane>`.
    switch (new URL(String(input), "http://localhost").pathname) {
      case "/api/v1/lanes": return new Response(JSON.stringify(envelope(lanes)));
      case "/api/v1/snapshot": return new Response(JSON.stringify(envelope(snapshotData)));
      case "/api/v1/designs": return new Response(JSON.stringify(envelope({designs:[{path:"docs/design.md",last_designer:null}],arrivals:{within_lifetime:2,after_lifetime:1,no_session:1,unmeasured:0}})));
      case "/api/v1/questions": return new Response(JSON.stringify(envelope({ items: [], unknown: [], missing: [], counts: { items: 0, questions: 0, holds: 0 }, state: "questions-ok" })));
      case "/api/v1/passes": return new Response(JSON.stringify(envelope([{ dispatched_at_ms: 1794840000000, decisions: [{ item_id: "item-held", place: 1, action: "hold", reason: "question-open" }], stop: "queue-drained", detail: "all held: item-held waits on OQ-1" }])));
      case "/api/v1/why": return refused ? new Response(JSON.stringify({ error: { code: "store-unreadable", message: "waiting is locked" } }), { status: 503 }) : new Response(JSON.stringify(envelope({ rows: [{ place: 1, item_id: "item-held", class: "needs you", where: "queue", why: "question-open", mover: "OQ-1", since: "current plan", class_source: "planner" }], summary: { lanes_busy: 0, lanes_offered: 1, running: 0, could_run: 0, held: { "question-open": 1 } } })));
      default: throw new Error(`Unexpected route: ${input}`);
    }
  });
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><App /></QueryClientProvider>);
  return { ...view, fetch, client };
}

test("Home shell reads snapshot, passes and the planner's why rows; nav and pause are real controls", async () => {
  const { container, fetch } = mount();
  await screen.findAllByRole("link", { name: "item-held" });
  expect(new Set(fetch.mock.calls.map(([path]) => String(path)))).toEqual(new Set(["/api/v1/snapshot", "/api/v1/passes", "/api/v1/why", "/api/v1/lanes"]));
  expect(container.textContent).toContain("all held");
  for (const id of ["item-held", "OQ-1"]) {
    const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT);
    while (walker.nextNode()) if (walker.currentNode.textContent?.includes(id)) expect(walker.currentNode.parentElement?.closest(".entity-link")).not.toBeNull();
  }
  expect(window.location.hash).toBe("#/");
  expect(screen.getByRole("navigation", { name: "Observatory" }).querySelectorAll("a")).toHaveLength(9);
  expect(screen.getByRole("link", {name:"Designs"}).getAttribute("href")).toBe("#/designs");
  fireEvent.click(screen.getByRole("button", { name: "Pause updates" }));
  expect(screen.getByRole("button", { name: "Resume updates" })).toBeTruthy();
  expect(container.querySelector(".observatory-nav__freshness")?.textContent).toContain("paused");
  const reads = fetch.mock.calls.length;
  fireEvent.click(screen.getByRole("button", { name: "Resume updates" }));
  await waitFor(() => expect(fetch.mock.calls.length).toBeGreaterThan(reads));
});

test.each(["#/question/OQ-1", "#/doc/docs%2Fa.md", "#/unknown"])("an unavailable destination names its route instead of displaying the old board: %s", async hash => {
  const { container } = mount();
  await screen.findAllByRole("link", { name: "item-held" });
  window.location.hash = hash;
  fireEvent(window, new Event("hashchange"));
  await screen.findByRole("heading", { name: "Page unavailable" });
  expect(screen.getByRole("alert").textContent).toContain("This page is not built yet");
  expect(container.querySelector(".board")).toBeNull();
  fireEvent.keyDown(window, { key: "j" });
  expect(window.location.hash).toBe(hash);
});

test.each(["lane-1", "%broken"])("a missing lane names its id on the Lane page: %s", async id => {
  mount();
  await screen.findAllByRole("link", { name: "item-held" });
  window.location.hash = `#/lane/${id}`;
  fireEvent(window, new Event("hashchange"));
  await screen.findByRole("heading", { name: `Lane ${id}` });
  expect((await screen.findByRole("alert")).textContent).toContain(`Lane ${id} is not recorded.`);
});

test("a Lane page shows its usage sum and opens model kinds", async () => {
  const { container } = mount(false, [{lane_id:"lane-1",state:"idle",state_since_ms:null,run_id:"",item_id:"",seat:"",backend:"pi",attempt:0,outcome:"",last_step:"",last_step_at_ms:null,latest_action:{kind:"unrecorded",text:"",at_ms:null,clock:"no_clock"},log_grew_at_ms:null,usage:{tokens:100,models:[{backend:"pi",model:"m",input:10,output:20,cache_read:70,cache_write:0}],unrecorded_runs:1,lower_bound:true}}]);
  await screen.findAllByRole("link", { name: "item-held" });
  window.location.hash = "#/lane/lane-1"; fireEvent(window, new Event("hashchange"));
  await screen.findByRole("heading", { name: "Lane lane-1" });
  expect(screen.getByText("≥ 100 tokens · 1 unrecorded runs")).toBeTruthy();
  fireEvent.click(screen.getByText("Usage by model and backend"));
  expect(container.textContent).toContain("pi · m: input 10 · output 20 · cache read 70 · cache write 0");
});

test("Designs navigation opens the list and a document's designer page", async () => {
  mount();
  await screen.findAllByRole("link",{name:"item-held"});
  fireEvent.click(screen.getByRole("link",{name:"Designs"}));
  await screen.findByRole("heading",{name:"Designs",level:1});
  fireEvent.click(screen.getByRole("link",{name:"docs/design.md"}));
  await screen.findByRole("heading",{name:"docs/design.md",level:1});
  expect(screen.getByText("no last designer recorded")).toBeTruthy();
});

test("Questions Close and browser Back keep the shell route synchronized", async () => {
  mount();
  await screen.findAllByRole("link", { name: "item-held" });
  window.location.hash = "#/questions";
  fireEvent(window, new Event("hashchange"));
  await screen.findByRole("button", { name: "Back to the board" });
  fireEvent.click(screen.getByRole("button", { name: "Back to the board" }));
  await screen.findByRole("heading", { name: "Home", level: 1 });
  expect(window.location.hash).toBe("");
  window.history.back();
  await screen.findByRole("button", { name: "Back to the board" });
  expect(window.location.hash).toBe("#/questions");
});

test("a refused waiting route stays visible in the shell", async () => {
  const { container } = mount(true);
  await screen.findByText(/which items are waiting is unknown.*: waiting is locked/);
  expect(container.querySelector(".home table")).toBeNull();
  expect(container.textContent).toContain("all held");
});
