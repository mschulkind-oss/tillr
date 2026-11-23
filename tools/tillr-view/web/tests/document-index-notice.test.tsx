import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "../src/App";
import { envelope, snapshotData } from "./fixtures";

class SilentSocket { close() {} }
afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });

function mount(documentIndex: unknown) {
  window.history.replaceState(null, "", "/#/");
  vi.stubGlobal("WebSocket", SilentSocket);
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    switch (new URL(String(input), "http://localhost").pathname) {
      case "/api/v1/lanes": return new Response(JSON.stringify(envelope([])));
      case "/api/v1/snapshot": return new Response(JSON.stringify(envelope(snapshotData)));
      case "/api/v1/passes": return new Response(JSON.stringify(envelope([])));
      case "/api/v1/why": return new Response(JSON.stringify(envelope({ rows: [], summary: { lanes_busy: 0, lanes_offered: 1, running: 0, could_run: 0, held: {} }, document_index: documentIndex })));
      default: throw new Error(`Unexpected route: ${input}`);
    }
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}><App /></QueryClientProvider>);
}

test("a stale document index is said plainly on Home, with its age", async () => {
  mount({ state: "refreshing", age_ms: 45000, message: "The document index is being refreshed; this answer uses one built 45s ago." });
  const text = (await screen.findByText(/being refreshed/)).textContent;
  expect(text).toContain("being refreshed");
  expect(text).toContain("45s ago");
});

test("a failed rebuild is shown, not hidden", async () => {
  mount({ state: "refreshing", age_ms: 90000, refresh_error: "uvx: not found", message: "The last document index rebuild failed (uvx: not found); this answer uses the index built 1m30s ago." });
  expect((await screen.findByText(/rebuild failed/)).textContent).toContain("uvx: not found");
});

test("a current index shows no notice", async () => {
  const { container } = mount({ state: "current", age_ms: 1000 });
  await screen.findByRole("navigation", { name: "Observatory" });
  await new Promise(resolve => setTimeout(resolve, 100));
  expect(container.querySelector(".document-index-notice")).toBeNull();
});
