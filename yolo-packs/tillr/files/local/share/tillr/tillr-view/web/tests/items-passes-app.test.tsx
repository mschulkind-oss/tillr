import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "../src/App";
import { envelope, snapshotData } from "./fixtures";

class SilentSocket { close() {} }
class OrderSocket {
  static current: OrderSocket;
  onmessage: ((event: { data: string }) => void) | null = null;
  constructor() { OrderSocket.current = this; }
  close() {}
}
afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });

test.each(["focus", "scroll"])("socket order changes wait behind the Items control while %s holds the table", async heldBy => {
  window.history.replaceState(null, "", "/items?state=unlanded");
  vi.stubGlobal("WebSocket", OrderSocket);
  let changed = false;
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    const rows = ["a", "b"].map(id => ({ item_id: id, place: changed ? (id === "b" ? 1 : 2) : (id === "a" ? 1 : 2), class: "ready", where: "queue", why: "ready", mover: "loop", since: "today", class_source: "rule" }));
    return new Response(JSON.stringify(envelope(path === "/api/v1/why" ? { rows } : path === "/api/v1/snapshot" ? snapshotData : [])));
  });
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><App /></QueryClientProvider>);
  await screen.findByRole("link", { name: "b" });
  const order = () => [...view.container.querySelectorAll("tbody tr")].map(row => row.getAttribute("data-row-id"));
  if (heldBy === "focus") fireEvent.focus(view.container.querySelector('[data-row-id="b"]')!);
  else fireEvent.scroll(screen.getByTestId("stable-list"), { target: { scrollTop: 100 } });
  changed = true;
  act(() => OrderSocket.current.onmessage?.({ data: JSON.stringify({ type: "snapshot.invalidated", event_head_seq: 50, changed: ["snapshot"] }) }));
  await screen.findByRole("button", { name: "order changed ↑" });
  expect(order()).toEqual(["a", "b"]);
  fireEvent.click(screen.getByRole("button", { name: "order changed ↑" }));
  expect(order()).toEqual(["b", "a"]);
  expect(fetch.mock.calls.filter(([path]) => path === "/api/v1/why").length).toBeGreaterThan(1);
  view.unmount(); client.clear();
});
test.each(["/items", "/passes?range=all"])("%s renders a ledger rather than the legacy board or placeholder", async hash => {
  window.history.replaceState(null, "", hash);
  vi.stubGlobal("WebSocket", SilentSocket);
  let arrived = false;
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    const data: unknown = path === "/api/v1/snapshot" ? snapshotData : path === "/api/v1/why" ? [{ place: 1, item_id: "item-held", class: "needs you", where: "queue", why: "question-open", mover: "OQ-1", since: "today", class_source: "rule" }] : path === "/api/v1/passes" ? [{ dispatched_at_ms: 1000, decisions: [{ item_id: "item-held", place: 1, action: "hold", reason: "question-open" }] }] : null;
    if (data === null) throw new Error(`Unexpected route: ${path}`);
    if (arrived && Array.isArray(data)) {
      if (path === "/api/v1/why") data.unshift({ ...data[0], item_id: "item-new", place: 1 });
      if (path === "/api/v1/passes") data.unshift({ dispatched_at_ms: 2000, decisions: [{ item_id: "item-new", place: 1, action: "hold", reason: "blocked" }] });
    }
    return new Response(JSON.stringify(envelope(path === "/api/v1/why" ? { rows: data, summary: {} } : data)));
  });
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><App /></QueryClientProvider>);
  await screen.findAllByRole("link", { name: "item-held" });
  expect(screen.getByRole("heading", { level: 1 }).textContent).toBe(hash.startsWith("/items") ? "Items" : "Passes");
  expect(view.container.querySelector(".board")).toBeNull();
  expect((window.location.pathname + window.location.search)).toBe(hash);
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
  expect((window.location.pathname + window.location.search)).toBe(hash);
  expect(view.container.querySelector('[data-focused="true"]')).toBeTruthy();
  expect(fetch.mock.calls.every(([path]) => ["/api/v1/snapshot", "/api/v1/why", "/api/v1/passes"].includes(String(path)))).toBe(true);
});
