import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "../src/App";
import { envelope, snapshotData } from "./fixtures";

class SilentSocket { close() {} }
afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });

function mount(refused = false) {
  window.history.replaceState(null, "", "/#/");
  vi.stubGlobal("WebSocket", SilentSocket);
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    switch (String(input)) {
      case "/api/v1/lanes": return new Response(JSON.stringify(envelope([])));
      case "/api/v1/snapshot": return new Response(JSON.stringify(envelope(snapshotData)));
      case "/api/v1/questions": return new Response(JSON.stringify(envelope({ items: [], unknown: [], missing: [], counts: { items: 0, questions: 0, holds: 0 }, state: "questions-ok" })));
      case "/api/v1/passes": return new Response(JSON.stringify(envelope([{ dispatched_at_ms: 1794840000000, decisions: [{ item_id: "item-held", rank: "a", action: "hold", reason: "question-open" }], stop: "queue-drained", detail: "all held: item-held waits on OQ-1" }])));
      case "/api/v1/why": return refused ? new Response(JSON.stringify({ error: { code: "store-unreadable", message: "waiting is locked" } }), { status: 503 }) : new Response(JSON.stringify(envelope({ rows: [{ rank: "a", item_id: "item-held", class: "needs you", where: "queue", why: "question-open", mover: "OQ-1", since: "current plan", class_source: "planner" }], summary: { lanes_busy: 0, lanes_offered: 1, running: 0, could_run: 0, held: { "question-open": 1 } } })));
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
  expect(screen.getByRole("navigation", { name: "Observatory" }).querySelectorAll("a")).toHaveLength(7);
  fireEvent.click(screen.getByRole("button", { name: "Pause updates" }));
  expect(screen.getByRole("button", { name: "Resume updates" })).toBeTruthy();
  expect(container.querySelector(".observatory-nav__freshness")?.textContent).toContain("paused");
  const reads = fetch.mock.calls.length;
  fireEvent.click(screen.getByRole("button", { name: "Resume updates" }));
  await waitFor(() => expect(fetch.mock.calls.length).toBeGreaterThan(reads));
});

test.each(["#/lane/lane-1", "#/question/OQ-1", "#/doc/docs%2Fa.md", "#/unknown", "#/lane/%broken"])("an unavailable destination names its route instead of displaying the old board: %s", async hash => {
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
  await screen.findByText("Waiting refused: waiting is locked");
  expect(container.querySelector(".home table")).toBeNull();
  expect(container.textContent).toContain("all held");
});
