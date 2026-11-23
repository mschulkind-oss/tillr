import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "../src/App";
import { envelope, snapshotData } from "./fixtures";

class SilentSocket { close() {} }
afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });

const row = { item_id: "item-held", class: "needs you", where: "queue", why: "question-open", mover: "owner", since: "today", class_source: "pass", place: 1 };
const designs = [{ document: "docs/tillr/design.md", class: "needs you", where: "queue", why: "design-without-item: docs/tillr/roadmap.md:7", mover: "owner", since: "today", class_source: "rule" }];

function mount(hash: string, source: unknown) {
  window.history.replaceState(null, "", hash);
  vi.stubGlobal("WebSocket", SilentSocket);
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    switch (new URL(String(input), "http://localhost").pathname) {
      case "/api/v1/lanes": return new Response(JSON.stringify(envelope([])));
      case "/api/v1/snapshot": return new Response(JSON.stringify(envelope(snapshotData)));
      case "/api/v1/passes": return new Response(JSON.stringify(envelope([])));
      case "/api/v1/why": return new Response(JSON.stringify(envelope({ rows: [row], designs_waiting: designs, source, summary: {} })));
      default: throw new Error(`Unexpected route: ${input}`);
    }
  }));
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><App /></QueryClientProvider>);
}

const pass = { kind: "pass", pass_id: "gen-11739", age_s: 12, text: "as of pass gen-11739, 12 s ago" };

test("Home says which pass its rows come from and lists designs apart from the rows", async () => {
  mount("/", pass);
  expect((await screen.findByLabelText("Waiting source")).textContent).toContain("as of pass gen-11739, 12 s ago");
  const section = await screen.findByTestId("designs-waiting");
  expect(within(section).getByText("docs/tillr/design.md")).toBeTruthy();
  expect(document.querySelector('[data-class] a[href*="design.md"]')).toBeNull();
});

test("Items shows the pass age and the designs list", async () => {
  mount("/items", pass);
  expect((await screen.findByLabelText("Waiting source")).textContent).toContain("12 s ago");
  expect(await screen.findByTestId("designs-waiting")).toBeTruthy();
});

test("a planned answer says it was not read from a pass, and why", async () => {
  mount("/", { kind: "planner", age_s: 0, fallback: "no loop is running (the loop session has ended)", text: "" });
  expect((await screen.findByLabelText("Waiting source")).textContent).toContain("no loop is running");
});
