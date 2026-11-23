import { useQuery } from "@tanstack/react-query";
import { navigate } from "../models/route";
import { useState, type FormEvent } from "react";
import { fetchAdminRow, fetchAdminTable, fetchAdminTables } from "../models/api";
import {
  activeFilters, adminRoute, cellLink, entityHref, isBlob, isCut, prettyJSON, rowHref, serverQuery, tableHref,
  type AdminColumn, type AdminPage as Page, type AdminRoute, type AdminValue,
} from "../models/admin";
import { RefusalState, refusalCode } from "./RefusalState";

const PREVIEW = 120;
const read = { retry: false, refetchOnWindowFocus: false, staleTime: 30_000 } as const;

/** The admin browser: every table of the store, read only (`/admin`). */
export function AdminPage({ route: location }: { route: string }) {
  const route = adminRoute(location);
  return <main className="entity-page admin-page" aria-label="Admin page">
    <h1><a href="/admin">Admin</a>{route && route.view !== "index" ? <> / <a href={tableHref(route.table)}>{route.table}</a></> : null}{route?.view === "row" ? <> / row {route.rowid}</> : null}</h1>
    <p className="admin-note">A read-only view of the store's tables. Nothing here writes, and no SQL is sent.</p>
    {route?.view === "table" ? <TableView key={location} route={route} /> : route?.view === "row" ? <RowView key={location} table={route.table} rowid={route.rowid} /> : <IndexView />}
  </main>;
}

function Failure({ error }: { error: Error }) {
  return <RefusalState code={refusalCode(error)} message={error.message} />;
}

function IndexView() {
  const tables = useQuery({ queryKey: ["admin", "tables"], queryFn: ({ signal }) => fetchAdminTables(signal), ...read });
  if (tables.error) return <Failure error={tables.error} />;
  if (!tables.data) return <p role="status">Reading tables…</p>;
  return <table aria-label="Tables"><thead><tr><th>Table</th><th>Rows</th><th>Columns</th><th>Indexes</th></tr></thead>
    <tbody>{tables.data.data.tables.map(table => <tr key={table.name}>
      <td>{table.browsable ? <a href={tableHref(table.name)}>{table.name}</a> : <>{table.name} <small>(not browsable)</small></>}</td>
      <td title={table.estimated ? "An estimate: the span of the table's row ids" : "Counted"}>{table.browsable ? <>{table.estimated ? "~" : ""}{table.rows.toLocaleString("en-US")}</> : "—"}</td>
      <td>{table.columns}</td><td>{table.indexes}</td>
    </tr>)}</tbody></table>;
}

/** Text that may be long or JSON: a short preview, with the whole value behind a control. */
export function Cell({ value }: { value: AdminValue }) {
  const [open, setOpen] = useState(false);
  if (value === null) return <span className="admin-null">NULL</span>;
  if (isBlob(value)) return <span className="admin-null">{value.blob_bytes} bytes (blob)</span>;
  if (typeof value !== "string" && !isCut(value)) return <>{String(value)}</>;
  const text = isCut(value) ? value.text : value;
  const length = isCut(value) ? value.length : value.length;
  const pretty = isCut(value) ? null : prettyJSON(text);
  if (length <= PREVIEW && !pretty) return <>{text}</>;
  const cut = isCut(value);
  return <span className="admin-long">
    {open ? <pre>{pretty ?? text}</pre> : <span>{text.slice(0, PREVIEW)}…</span>}
    {" "}<button type="button" aria-expanded={open} onClick={() => setOpen(!open)}>{open ? "Collapse" : pretty ? "Expand JSON" : "Expand"}</button>
    {open && cut ? <small> showing {text.length.toLocaleString("en-US")} of {length.toLocaleString("en-US")} bytes; the row page has all of it</small> : null}
  </span>;
}

function FilterForm({ table, columns, params }: { table: string; columns: AdminColumn[]; params: URLSearchParams }) {
  const filters = activeFilters(params);
  const go = (next: URLSearchParams) => { next.delete("after"); navigate(tableHref(table, next)); };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const column = String(form.get("column") ?? ""), op = String(form.get("op") ?? "f"), value = String(form.get("value") ?? "");
    if (!column || (op !== "null" && value === "")) return;
    const next = new URLSearchParams(params);
    next.set(`${op}.${column}`, op === "null" ? "1" : value);
    go(next);
  };
  const quick = table === "events" ? <EventsFilter params={params} onApply={go} /> : null;
  return <section aria-label="Filters" className="admin-filters">
    {quick}
    <form onSubmit={submit}>
      <select name="column" aria-label="Filter column">{columns.map(c => <option key={c.name} value={c.name}>{c.name}{c.indexed ? "" : " (scan)"}</option>)}</select>
      <select name="op" aria-label="Filter operator"><option value="f">=</option><option value="min">≥</option><option value="max">≤</option><option value="null">is null</option></select>
      <input name="value" aria-label="Filter value" /><button type="submit">Add filter</button>
    </form>
    {filters.length ? <ul aria-label="Active filters">{filters.map(f => <li key={f.key}>{f.column} {f.op} {f.value}{" "}
      <button type="button" aria-label={`Remove filter ${f.key}`} onClick={() => { const next = new URLSearchParams(params); next.delete(f.key); go(next); }}>×</button></li>)}</ul> : null}
  </section>;
}

/** The log's own filters: kind and object are index lookups, and seq is its row id. */
function EventsFilter({ params, onApply }: { params: URLSearchParams; onApply: (next: URLSearchParams) => void }) {
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget), next = new URLSearchParams(params);
    for (const [name, key] of [["kind", "f.kind"], ["object", "f.object_id"], ["from", "min.seq"], ["to", "max.seq"]] as const) {
      const value = String(form.get(name) ?? "").trim();
      if (value) next.set(key, value); else next.delete(key);
    }
    onApply(next);
  };
  return <form aria-label="Event filters" onSubmit={submit}>
    <label>Kind <input name="kind" defaultValue={params.get("f.kind") ?? ""} /></label>
    <label>Object <input name="object" defaultValue={params.get("f.object_id") ?? ""} /></label>
    <label>Seq from <input name="from" inputMode="numeric" defaultValue={params.get("min.seq") ?? ""} /></label>
    <label>to <input name="to" inputMode="numeric" defaultValue={params.get("max.seq") ?? ""} /></label>
    <button type="submit">Filter events</button>
  </form>;
}

function TableView({ route }: { route: Extract<AdminRoute, { view: "table" }> }) {
  const { table, params } = route;
  const query = serverQuery(table, params);
  const page = useQuery({ queryKey: ["admin", "table", table, query], queryFn: ({ signal }) => fetchAdminTable(table, query, signal), ...read });
  if (page.error) return <Failure error={page.error} />;
  if (!page.data) return <p role="status">Reading {table}…</p>;
  return <TablePage page={page.data.data} params={params} />;
}

export function TablePage({ page, params }: { page: Page; params: URLSearchParams }) {
  const table = page.table;
  const sortBy = (column: AdminColumn) => {
    const next = new URLSearchParams(params);
    next.delete("after");
    const current = page.sort === column.name;
    next.set("sort", column.name);
    next.set("order", current && page.order === "asc" ? "desc" : "asc");
    return tableHref(table, next);
  };
  const nextHref = () => { const next = new URLSearchParams(params); next.set("after", page.next ?? ""); return tableHref(table, next); };
  return <>
    <FilterForm table={table} columns={page.columns} params={params} />
    <p role="status">{page.rows.length} row{page.rows.length === 1 ? "" : "s"} shown, ordered by {page.sort} {page.order}
      {page.scan ? <> · scanned through row id {page.scan.through.toLocaleString("en-US")} of {page.scan.max.toLocaleString("en-US")} (no index serves this filter)</> : null}</p>
    <div className="admin-scroll"><table aria-label={`Rows of ${table}`}>
      <thead><tr><th>#</th>{page.columns.map(c => <th key={c.name} title={`${c.type || "no type"}${c.pk ? " · primary key" : ""}${c.indexed ? " · indexed" : ""}`}>
        {c.sortable ? <a href={sortBy(c)} aria-label={`Sort by ${c.name}`}>{c.name}{page.sort === c.name ? (page.order === "asc" ? " ▲" : " ▼") : ""}</a> : c.name}</th>)}</tr></thead>
      <tbody>{page.rows.map((cells, r) => {
        const byName: Record<string, AdminValue> = {};
        page.columns.forEach((c, i) => { byName[c.name] = cells[i]; });
        return <tr key={page.rowids[r]}>
          <td><a href={rowHref(table, page.rowids[r])}>{page.rowids[r]}</a></td>
          {page.columns.map((c, i) => {
            const link = cellLink(table, c.name, cells[i], byName);
            const filter = typeof cells[i] === "string" || typeof cells[i] === "number";
            return <td key={c.name}>{link ? <a href={link}>{String(cells[i])}</a> : <Cell value={cells[i]} />}
              {filter && c.indexed && !(typeof cells[i] === "string" && (cells[i] as string).length > PREVIEW) ? <a className="admin-narrow" href={tableHref(table, withFilter(params, c.name, String(cells[i])))} title={`Only rows where ${c.name} is this`} aria-label={`Filter ${c.name} = ${String(cells[i])}`}> ⌖</a> : null}</td>;
          })}
        </tr>;
      })}</tbody>
    </table></div>
    {page.rows.length === 0 && !page.more ? <p>No rows match.</p> : null}
    {page.more && page.next ? <p><a href={nextHref()} role="button">{page.rows.length === 0 ? "Keep scanning" : "Next page"}</a></p> : null}
  </>;
}

const withFilter = (params: URLSearchParams, column: string, value: string) => {
  const next = new URLSearchParams(params);
  next.delete("after"); next.set(`f.${column}`, value);
  return next;
};

function RowView({ table, rowid }: { table: string; rowid: string }) {
  const row = useQuery({ queryKey: ["admin", "row", table, rowid], queryFn: ({ signal }) => fetchAdminRow(table, rowid, signal), ...read });
  if (row.error) return <Failure error={row.error} />;
  if (!row.data) return <p role="status">Reading row…</p>;
  const data = row.data.data;
  const byName: Record<string, AdminValue> = {};
  data.columns.forEach((c, i) => { byName[c.name] = data.values[i]; });
  return <>
    <table aria-label="Row"><tbody>{data.columns.map((c, i) => {
      const value = data.values[i], link = cellLink(table, c.name, value, byName);
      return <tr key={c.name}><th scope="row">{c.name}</th><td>{link ? <a href={link}>{String(value)}</a> : <FullValue value={value} />}</td></tr>;
    })}</tbody></table>
    <div className="admin-neighbors"><a href={rowHref(table, data.rowid - 1)}>Previous row id</a> · <a href={rowHref(table, data.rowid + 1)}>Next row id</a></div>
    {data.payload_refs.length ? <section aria-label="Payload references"><h2>Named in the payload</h2><ul>
      {data.payload_refs.map(ref => { const kind = ref.key.replace(/_id$/, ""), href = entityHref(kind, ref.id);
        return <li key={ref.key}>{ref.key}: {href ? <a href={href}>{ref.id}</a> : ref.id}</li>; })}</ul></section> : null}
    <section aria-label="Related rows"><h2>Related rows</h2>{data.related.length === 0 ? <p>None found through an index.</p> : <ul>
      {data.related.map(rel => <li key={`${rel.table}.${rel.column}.${rel.value}`}>
        <a href={tableHref(rel.table, { [`f.${rel.column}`]: rel.value })}>{rel.table}</a> where {rel.column} = {rel.value.length > 60 ? `${rel.value.slice(0, 60)}…` : rel.value}: {rel.count}{rel.capped ? "+" : ""}</li>)}</ul>}</section>
  </>;
}

function FullValue({ value }: { value: AdminValue }) {
  if (typeof value === "string") { const pretty = prettyJSON(value); return pretty ? <pre>{pretty}</pre> : value.length > PREVIEW ? <pre>{value}</pre> : <>{value}</>; }
  if (isCut(value)) return <><pre>{value.text}</pre><small>cut at {value.text.length.toLocaleString("en-US")} of {value.length.toLocaleString("en-US")} bytes</small></>;
  return <Cell value={value} />;
}
