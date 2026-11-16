import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "../src/App";
import { envelope, snapshotData } from "./fixtures";

class SilentSocket { close() {} }
afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });
test.each(["#/items", "#/passes?range=all"])("%s renders a ledger rather than the legacy board or placeholder", async hash => {
  window.history.replaceState(null, "", `/${hash}`);
  vi.stubGlobal("WebSocket", SilentSocket);
  let arrived = false;
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    const data: unknown = path === "/api/v1/snapshot" ? snapshotData : path === "/api/v1/waiting" ? [{ rank: "a", item_id: "item-held", class: "needs you", where: "queue", why: "question-open", mover: "OQ-1", since: "today", class_source: "rule" }] : path === "/api/v1/passes" ? [{ dispatched_at_ms: 1000, decisions: [{ item_id: "item-held", rank: "a", action: "hold", reason: "question-open" }] }] : null;
    if (data === null) throw new Error(`Unexpected route: ${path}`);
    if (arrived && Array.isArray(data)) {
      if (path === "/api/v1/waiting") data.unshift({ ...data[0], item_id: "item-new", rank: "0" });
      if (path === "/api/v1/passes") data.unshift({ dispatched_at_ms: 2000, decisions: [{ item_id: "item-new", rank: "0", action: "hold", reason: "blocked" }] });
    }
    return new Response(JSON.stringify(envelope(data)));
  });
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><App /></QueryClientProvider>);
  await screen.findAllByRole("link", { name: "item-held" });
  expect(screen.getByRole("heading", { level: 1 }).textContent).toBe(hash.startsWith("#/items") ? "Items" : "Passes");
  expect(view.container.querySelector(".board")).toBeNull();
  expect(window.location.hash).toBe(hash);
  const shell = view.container.querySelector(".observatory-shell")!;
  fireEvent.scroll(shell, { target: { scrollTop: 200 } });
  arrived = true;
  await client.invalidateQueries();
  await screen.findByRole("button", { name: "1 newer ↑" });
  expect(screen.queryByRole("link", { name: "item-new" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "1 newer ↑" }));
  expect(shell.scrollTop).toBe(0);
  await screen.findAllByRole("link", { name: "item-new" });
  fireEvent.keyDown(window, { key: "j" });
  expect(window.location.hash).toBe(hash);
  expect(view.container.querySelector('[data-focused="true"]')).toBeTruthy();
  expect(fetch.mock.calls.every(([path]) => ["/api/v1/snapshot", "/api/v1/waiting", "/api/v1/passes"].includes(String(path)))).toBe(true);
});
