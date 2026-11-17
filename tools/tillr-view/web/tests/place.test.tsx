import { createServer, type Server } from "node:http";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "../src/App";
import { envelope, snapshotData } from "./fixtures";

class SilentSocket { close() {} }
let server: Server | undefined;
afterEach(async () => { cleanup(); vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); if (server) await new Promise<void>(resolve => server!.close(() => resolve())); server = undefined; });

test.each([false, true, "refused"])("Items move-to-top with writes=%s against a fixture HTTP server", async mode => {
  const writes = mode !== false;
  const requests: { path: string; body: string; type: string | undefined }[] = [];
  let moved = false;
  const rows = () => [{ item_id: "first", rank: "a", class: "ready", where: "queue", why: "ready", mover: "loop", since: "today", class_source: "rule" }, { item_id: "second", rank: moved ? "0" : "b", class: "ready", where: "queue", why: "ready", mover: "loop", since: "today", class_source: "rule" }];
  server = createServer(async (req, res) => {
    let body = ""; for await (const chunk of req) body += chunk;
    res.setHeader("Content-Type", "application/json");
    if (req.method === "POST") {
      requests.push({ path: req.url!, body, type: req.headers["content-type"] });
      if (mode === "refused") { res.statusCode = 409; res.end(JSON.stringify({ error: { code: "not-in-order", message: "item is parked" } })); return; }
      moved = true;
      res.end(JSON.stringify(envelope({ item_id: "second", place: 1, count: 2, event_seq: 49 }))); return;
    }
    const data = req.url === "/api/v1/why" ? { rows: rows(), summary: {} } : req.url === "/api/v1/snapshot" ? snapshotData : [];
    res.end(JSON.stringify({ ...envelope(data), snapshot: { ...envelope(data).snapshot, writes_enabled: writes } }));
  });
  await new Promise<void>(resolve => server!.listen(0, "127.0.0.1", resolve));
  const address = server.address(); if (!address || typeof address === "string") throw new Error("no fixture address");
  const nativeFetch = globalThis.fetch;
  vi.stubGlobal("fetch", (path: RequestInfo | URL, init?: RequestInit) => nativeFetch(`http://127.0.0.1:${address.port}${String(path)}`, { ...init, signal: undefined }));
  vi.stubGlobal("WebSocket", SilentSocket);
  window.history.replaceState(null, "", "/#/items?state=unlanded");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><App /></QueryClientProvider>);
  await screen.findByRole("link", { name: "second" });
  const order = () => [...view.container.querySelectorAll("tbody tr")].map(row => row.getAttribute("data-row-id"));
  expect(order()).toEqual(["first", "second"]);
  if (!writes) { expect(screen.queryByRole("button", { name: /move .* to top/i })).toBeNull(); expect(requests).toEqual([]); return; }
  expect(screen.getAllByRole("button", { name: /move .* to top/i })).toHaveLength(2);
  fireEvent.focus(screen.getByRole("button", { name: "Move second to top" }));
  fireEvent.click(screen.getByRole("button", { name: "Move second to top" }));
  if (mode === "refused") {
    await screen.findByRole("alert", { name: "" });
    expect(screen.getByText("Move refused: second: item is parked")).toBeTruthy();
    expect(order()).toEqual(["first", "second"]);
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "Move second to top" }).disabled).toBe(false);
  } else await waitFor(() => expect(order()).toEqual(["second", "first"]));
  expect(requests).toEqual([{ path: "/api/v1/items/second/place", body: '{"to":"first"}', type: "application/json" }]);
});
