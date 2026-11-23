import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test } from "vitest";
import { AdminPage, Cell, TablePage } from "../src/components/AdminPage";
import { activeFilters, adminRoute, cellLink, serverQuery } from "../src/models/admin";
import type { AdminPage as Page } from "../src/models/admin";
import { envelope } from "./fixtures";

const columns = [
  { name: "seq", type: "INTEGER", not_null: false, pk: 1, indexed: true, sortable: true },
  { name: "kind", type: "TEXT", not_null: true, pk: 0, indexed: true, sortable: true },
  { name: "object_type", type: "TEXT", not_null: false, pk: 0, indexed: false, sortable: false },
  { name: "object_id", type: "TEXT", not_null: false, pk: 0, indexed: true, sortable: true },
  { name: "payload", type: "TEXT", not_null: true, pk: 0, indexed: false, sortable: false },
];
const eventsPage: Page = {
  table: "events", columns, indexes: [], rowids: [9, 8], sort: "seq", order: "desc", more: true, next: "CURSOR",
  rows: [
    [9, "run.child-session", "run", "run-77", '{"run_id":"run-77","n":1}'],
    [8, "item.moved", "item", "item-a", { text: "x".repeat(2000), length: 5000, truncated: true }],
  ],
};

afterEach(cleanup);

test("routes parse the index, a table with its query, and a row", () => {
  expect(adminRoute("/admin")).toEqual({ view: "index" });
  const table = adminRoute("/admin/events?f.kind=a&sort=kind");
  expect(table).toMatchObject({ view: "table", table: "events" });
  expect(adminRoute("/admin/events/12")).toEqual({ view: "row", table: "events", rowid: "12" });
  expect(adminRoute("/runs")).toBeNull();
});

test("the server query keeps only parameters the server knows, and reads the log newest first", () => {
  const params = new URLSearchParams("f.kind=a&min.seq=5&junk=1&after=C");
  expect(serverQuery("events", params)).toBe("f.kind=a&min.seq=5&after=C&order=desc");
  expect(serverQuery("items", new URLSearchParams("order=asc"))).toBe("order=asc");
  expect(activeFilters(new URLSearchParams("f.kind=a&max.seq=9&null.object_id=1")).map(f => `${f.column} ${f.op} ${f.value}`))
    .toEqual(["kind = a", "seq ≤ 9", "object_id is null "]);
});

test("ids other pages know link to them; an event's object links by its recorded type", () => {
  expect(cellLink("runs", "item_id", "item-a", {})).toBe("/item/item-a");
  expect(cellLink("runs", "run_id", "run-1", {})).toBe("/run/run-1?tab=transcript");
  expect(cellLink("events", "object_id", "run-77", { object_type: "run" })).toBe("/run/run-77?tab=transcript");
  expect(cellLink("events", "object_id", "x", { object_type: "trunk" })).toBeNull();
  expect(cellLink("items", "title", "hello", {})).toBeNull();
});

test("a table page links rows, sorts by indexed columns only, and pages by cursor", () => {
  const { container } = render(<TablePage page={eventsPage} params={new URLSearchParams("order=desc")} />);
  expect(screen.getByRole("link", { name: "Sort by kind" }).getAttribute("href")).toBe("/admin/events?order=asc&sort=kind");
  expect(screen.queryByRole("link", { name: "Sort by payload" })).toBeNull();
  const rows = container.querySelectorAll("tbody tr");
  expect(within(rows[0] as HTMLElement).getByRole("link", { name: "9" }).getAttribute("href")).toBe("/admin/events/9");
  expect(within(rows[0] as HTMLElement).getByRole("link", { name: "run-77" }).getAttribute("href")).toBe("/run/run-77?tab=transcript");
  expect(screen.getByRole("button", { name: "Next page" }).getAttribute("href")).toBe("/admin/events?order=desc&after=CURSOR");
});

test("JSON is pretty-printed behind an expand control, and a server-cut text says what was cut", () => {
  const { container } = render(<Cell value={'{"a":{"b":1}}'} />);
  expect(container.querySelector("pre")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Expand JSON" }));
  expect(container.querySelector("pre")!.textContent).toBe('{\n  "a": {\n    "b": 1\n  }\n}');
  cleanup();
  render(<Cell value={{ text: "y".repeat(2000), length: 5000, truncated: true }} />);
  fireEvent.click(screen.getByRole("button", { name: "Expand" }));
  expect(screen.getByText(/showing 2,000 of 5,000 bytes/)).toBeTruthy();
  cleanup();
  const { container: short } = render(<Cell value="short" />);
  expect(short.textContent).toBe("short");
  expect(short.querySelector("button")).toBeNull();
  cleanup();
  expect(render(<Cell value={null} />).container.textContent).toBe("NULL");
});

test("a scan that found nothing yet says so and offers to keep going", () => {
  render(<TablePage page={{ ...eventsPage, rows: [], rowids: [], scan: { through: 100000, max: 1380000 } }} params={new URLSearchParams()} />);
  expect(screen.getByRole("status").textContent).toContain("scanned through row id 100,000 of 1,380,000");
  expect(screen.getByRole("button", { name: "Keep scanning" })).toBeTruthy();
});

const originalFetch = globalThis.fetch;
let seen: string[] = [];
beforeEach(() => {
  seen = [];
  globalThis.fetch = (async input => {
    const path = String(input);
    seen.push(path);
    if (path === "/api/v1/admin/tables") return new Response(JSON.stringify(envelope({ tables: [
      { name: "events", rows: 1380000, estimated: true, columns: 10, indexes: 2, browsable: true },
      { name: "items", rows: 12, estimated: false, columns: 8, indexes: 0, browsable: true }] })));
    if (path.startsWith("/api/v1/admin/tables/events/rows/")) return new Response(JSON.stringify(envelope({
      table: "events", rowid: 9, columns, values: [9, "run.child-session", "run", "run-77", '{"run_id":"run-77"}'],
      payload_refs: [{ key: "run_id", id: "run-77" }],
      related: [{ table: "events", column: "object_id", value: "run-77", count: 1000, capped: true }] })));
    if (path.startsWith("/api/v1/admin/tables/events")) return new Response(JSON.stringify(envelope(eventsPage)));
    return new Response(JSON.stringify({ error: { code: "unknown-table", message: "the store has no table" } }), { status: 404 });
  }) as typeof fetch;
});
afterEach(() => { globalThis.fetch = originalFetch; });
const mount = (hash: string) => render(<QueryClientProvider client={new QueryClient()}><AdminPage route={hash} /></QueryClientProvider>);

test("the index lists tables with counts, marking an estimate", async () => {
  mount("/admin");
  const link = await screen.findByRole("link", { name: "events" });
  expect(link.getAttribute("href")).toBe("/admin/events");
  expect(link.closest("tr")!.textContent).toContain("~1,380,000");
  expect(screen.getByRole("link", { name: "items" }).closest("tr")!.textContent).toContain("12");
});

test("a table route asks the server for the filtered page and shows it", async () => {
  mount("/admin/events?f.kind=run.child-session&min.seq=5");
  await screen.findByRole("table", { name: "Rows of events" });
  expect(seen).toEqual(["/api/v1/admin/tables/events?f.kind=run.child-session&min.seq=5&order=desc"]);
  expect((screen.getByLabelText("Filter column") as HTMLSelectElement).options.length).toBe(5);
  expect(within(screen.getByRole("list", { name: "Active filters" })).getAllByRole("listitem")).toHaveLength(2);
});

test("a row shows every column, links its payload, and counts related rows with a cap", async () => {
  mount("/admin/events/9");
  await screen.findByRole("table", { name: "Row" });
  expect(within(screen.getByRole("table", { name: "Row" })).getAllByRole("row")).toHaveLength(5);
  const refs = screen.getByRole("region", { name: "Payload references" });
  expect(within(refs).getByRole("link", { name: "run-77" }).getAttribute("href")).toBe("/run/run-77?tab=transcript");
  const related = screen.getByRole("region", { name: "Related rows" });
  expect(within(related).getByRole("link", { name: "events" }).getAttribute("href")).toBe("/admin/events?f.object_id=run-77");
  expect(related.textContent).toContain("1000+");
});

test("an unknown table is the server's refusal, shown", async () => {
  mount("/admin/nope");
  await waitFor(() => expect(document.body.textContent).toContain("the store has no table"));
});
