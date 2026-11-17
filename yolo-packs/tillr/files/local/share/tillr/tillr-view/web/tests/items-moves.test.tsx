import { act, cleanup, createEvent, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ItemsView } from "../src/components/ItemsPasses";
import type { WaitingRow } from "../src/models/types";

const rows: WaitingRow[] = ["a", "hidden", "b", "c"].map((id, index) => ({
  item_id: id, rank: String(index + 1), class: id === "hidden" ? "ready" : "needs you",
  where: "queue", why: "question", mover: "owner", since: "today", class_source: "rule",
}));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });
function route(filtered = false) { window.history.replaceState(null, "", `/#/items?state=unlanded${filtered ? "&class=needs+you" : ""}`); }
const order = () => [...document.querySelectorAll("tbody tr")].map(row => row.getAttribute("data-row-id"));
const row = (id: string) => document.querySelector<HTMLElement>(`[data-row-id="${id}"]`)!;
function deferred() {
  let resolve!: (response: Response) => void;
  const fetch = vi.fn((_path: string, _init?: RequestInit) => new Promise<Response>(done => { resolve = done; }));
  vi.stubGlobal("fetch", fetch);
  return { fetch, resolve: (response: Response) => resolve(response) };
}
function drop(id: string, target: string, below = false) {
  const dataTransfer = { setData: vi.fn(), getData: vi.fn(), effectAllowed: "", dropEffect: "" };
  fireEvent.dragStart(screen.getByRole("button", { name: `Drag ${id}` }), { dataTransfer });
  vi.spyOn(row(target), "getBoundingClientRect").mockReturnValue({ top: 0, height: 20 } as DOMRect);
  fireEvent.dragOver(row(target), { dataTransfer });
  const event = createEvent.drop(row(target), { dataTransfer });
  Object.defineProperty(event, "clientY", { value: below ? 19 : 1 });
  fireEvent(row(target), event);
}

test.each([false, true])("drops use visible before/after anchors with filtered=%s and roll back a 409", async filtered => {
  route(filtered);
  const request = deferred();
  render(<ItemsView waiting={rows} writesEnabled />);
  const previous = order();
  drop("c", "a");
  expect(JSON.parse(request.fetch.mock.calls[0][1]!.body as string)).toEqual({ before: "a" });
  expect(order()).toEqual(["c", ...previous.filter(id => id !== "c")]);
  await act(async () => request.resolve(new Response(JSON.stringify({ error: { code: "not-in-order", message: "anchor is parked" } }), { status: 409 })));
  expect(order()).toEqual(previous);
  expect(screen.getByRole("alert").textContent).toContain("anchor is parked");
  drop("a", "c", true);
  expect(JSON.parse(request.fetch.mock.calls[1][1]!.body as string)).toEqual({ after: "c" });
  expect(order()).toEqual([...previous.filter(id => id !== "a"), "a"]);
  await act(async () => request.resolve(new Response("{}")));
});

test("handles and uppercase moves require writes and place sorting", () => {
  route();
  const fetch = vi.fn(async () => new Response("{}")); vi.stubGlobal("fetch", fetch);
  const view = render(<ItemsView waiting={rows} />);
  expect(screen.queryByRole("button", { name: "Drag a" })).toBeNull();
  fireEvent.focus(row("a")); fireEvent.keyDown(row("a"), { key: "J" });
  expect(fetch).not.toHaveBeenCalled();
  view.rerender(<ItemsView waiting={rows} writesEnabled />);
  expect(screen.getByRole("button", { name: "Drag a" }).getAttribute("draggable")).toBe("true");
  fireEvent.change(screen.getByLabelText("Sort"), { target: { value: "item" } });
  expect(screen.queryByRole("button", { name: "Drag a" })).toBeNull();
  fireEvent.focus(row("a")); fireEvent.keyDown(row("a"), { key: "J" });
  expect(fetch).not.toHaveBeenCalled();
});

test("J and K move one visible row, keep focus, and ignore typing and boundaries", async () => {
  route(true);
  const fetch = vi.fn(async (_path: string, _init?: RequestInit) => new Response("{}")); vi.stubGlobal("fetch", fetch);
  render(<ItemsView waiting={rows} writesEnabled />);
  expect(screen.getByText(/J.*move down/)).toBeTruthy();
  expect(screen.getByText(/K.*move up/)).toBeTruthy();
  fireEvent.focus(row("a")); fireEvent.keyDown(row("a"), { key: "K" });
  expect(fetch).not.toHaveBeenCalled();
  fireEvent.keyDown(row("a"), { key: "J" });
  expect(order()).toEqual(["b", "a", "c"]);
  expect(JSON.parse(fetch.mock.calls[0][1]!.body as string)).toEqual({ after: "b" });
  await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "Drag a" }).disabled).toBe(false));
  expect(document.activeElement).toBe(row("a"));
  fireEvent.keyDown(row("a"), { key: "K" });
  expect(order()).toEqual(["a", "b", "c"]);
  expect(JSON.parse(fetch.mock.calls[1][1]!.body as string)).toEqual({ before: "b" });
  await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "Drag a" }).disabled).toBe(false));
  fireEvent.keyDown(screen.getByLabelText("Class"), { key: "J" });
  expect(fetch).toHaveBeenCalledTimes(2);
});

test("a fresh unchanged order after a concurrent restoration still offers acceptance", async () => {
  route(true);
  const request = deferred();
  let refreshed!: () => void;
  const onMoved = vi.fn(() => new Promise<void>(resolve => { refreshed = resolve; }));
  const view = render(<ItemsView waiting={rows} writesEnabled onMoved={onMoved} />);
  fireEvent.focus(row("a")); fireEvent.keyDown(row("a"), { key: "J" });
  expect(order()).toEqual(["b", "a", "c"]);
  await act(async () => request.resolve(new Response("{}")));
  expect(onMoved).toHaveBeenCalledTimes(1);
  // Another tab has undone the move before our first authoritative refresh.
  view.rerender(<ItemsView waiting={rows.map(item => ({ ...item }))} writesEnabled onMoved={onMoved} />);
  await act(async () => refreshed());
  await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "Drag a" }).disabled).toBe(false));
  expect(order()).toEqual(["b", "a", "c"]);
  fireEvent.click(screen.getByRole("button", { name: "order changed ↑" }));
  expect(order()).toEqual(["a", "b", "c"]);
});

test("keyboard moves do not jump over an ineligible adjacent visible row", () => {
  route(true);
  const fetch = vi.fn(async () => new Response("{}")); vi.stubGlobal("fetch", fetch);
  const waiting = [rows[0], { ...rows[2], item_id: "parked", rank: "2" }, rows[2]];
  const snapshot = { items: [{ item_id: "parked", projected_state: { raw_state: "not-started", state: "not-started" } }] } as import("../src/models/types").SnapshotData;
  render(<ItemsView waiting={waiting} snapshot={snapshot} writesEnabled />);
  fireEvent.focus(row("a")); fireEvent.keyDown(row("a"), { key: "J" });
  expect(fetch).not.toHaveBeenCalled();
  expect(order()).toEqual(["a", "parked", "b"]);
  fireEvent.focus(row("b")); fireEvent.keyDown(row("b"), { key: "K" });
  expect(fetch).not.toHaveBeenCalled();
});

test("a failed refresh after a saved move does not roll it back or call it refused", async () => {
  route(true);
  vi.stubGlobal("fetch", vi.fn(async () => new Response("{}")));
  render(<ItemsView waiting={rows} writesEnabled onMoved={async () => { throw new Error("network unavailable"); }} />);
  fireEvent.focus(row("b")); fireEvent.keyDown(row("b"), { key: "K" });
  await screen.findByRole("alert");
  expect(order()).toEqual(["b", "a", "c"]);
  expect(screen.getByRole("alert").textContent).toBe("Order saved, but refresh failed: network unavailable");
});

test("pending moves reject repeated keys and the below-table drop names the last visible row", async () => {
  route(true);
  const request = deferred();
  render(<ItemsView waiting={rows} writesEnabled />);
  fireEvent.focus(row("a")); fireEvent.keyDown(row("a"), { key: "J" });
  fireEvent.keyDown(row("a"), { key: "J" });
  expect(request.fetch).toHaveBeenCalledTimes(1);
  await act(async () => request.resolve(new Response("{}")));
  const dataTransfer = { setData: vi.fn(), effectAllowed: "", dropEffect: "" };
  fireEvent.dragStart(screen.getByRole("button", { name: "Drag b" }), { dataTransfer });
  fireEvent.dragOver(screen.getByText("Drop here to move below the last row"), { dataTransfer });
  fireEvent.drop(screen.getByText("Drop here to move below the last row"), { dataTransfer });
  expect(JSON.parse(request.fetch.mock.calls[1][1]!.body as string)).toEqual({ after: "c" });
  expect(order()).toEqual(["a", "c", "b"]);
  await act(async () => request.resolve(new Response("{}")));
});

test("a confirmed local move does not hide a later return to the original server order", async () => {
  route(true);
  vi.stubGlobal("fetch", vi.fn(async () => new Response("{}")));
  const view = render(<ItemsView waiting={rows} writesEnabled />);
  fireEvent.focus(row("b")); fireEvent.keyDown(row("b"), { key: "K" });
  await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "Drag b" }).disabled).toBe(false));
  expect(order()).toEqual(["b", "a", "c"]);
  expect(screen.queryByRole("button", { name: "order changed ↑" })).toBeNull();
  view.rerender(<ItemsView waiting={rows.map(item => item.item_id === "b" ? { ...item, rank: "0" } : item)} writesEnabled />);
  expect(screen.queryByRole("button", { name: "order changed ↑" })).toBeNull();
  view.rerender(<ItemsView waiting={rows} writesEnabled />);
  expect(order()).toEqual(["b", "a", "c"]);
  fireEvent.click(screen.getByRole("button", { name: "order changed ↑" }));
  expect(order()).toEqual(["a", "b", "c"]);
});

test.each(["focus", "scroll"])("incoming order waits while held by %s; facts still update", heldBy => {
  route();
  const view = render(<ItemsView waiting={rows} />);
  if (heldBy === "focus") fireEvent.focus(row("b"));
  else fireEvent.scroll(screen.getByTestId("stable-list"), { target: { scrollTop: 100 } });
  view.rerender(<ItemsView waiting={rows.map(item => ({ ...item, rank: item.item_id === "c" ? "0" : item.rank, why: "updated fact" }))} />);
  expect(order()).toEqual(["a", "hidden", "b", "c"]);
  expect(screen.getAllByRole("cell", { name: "updated fact" })).toHaveLength(4);
  fireEvent.click(screen.getByRole("button", { name: "order changed ↑" }));
  expect(order()).toEqual(["c", "a", "hidden", "b"]);
  expect(screen.queryByRole("button", { name: "order changed ↑" })).toBeNull();
});
