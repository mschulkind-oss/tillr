// The admin browser's shapes and its pure helpers: route parsing, the query the server
// receives, and which values link to a page the Observatory already has.

export interface AdminColumn { name: string; type: string; not_null: boolean; pk: number; indexed: boolean; sortable: boolean }
export interface AdminIndex { name: string; unique: boolean; partial: boolean; columns: string[] }
export interface AdminTableSummary { name: string; rows: number; estimated: boolean; columns: number; indexes: number; browsable: boolean }
export interface AdminTablesData { tables: AdminTableSummary[] }
/** A text value the server cut: the head, and the length it was cut from. */
export interface AdminCut { text: string; length: number; truncated: true }
export type AdminValue = string | number | boolean | null | AdminCut | { blob_bytes: number };
export interface AdminPage {
  table: string; columns: AdminColumn[]; indexes: AdminIndex[]; rowids: number[]; rows: AdminValue[][];
  next?: string; more: boolean; sort: string; order: "asc" | "desc"; scan?: { through: number; max: number };
}
export interface AdminRelated { table: string; column: string; value: string; count: number; capped: boolean }
export interface AdminRow {
  table: string; rowid: number; columns: AdminColumn[]; values: AdminValue[]; related: AdminRelated[];
  payload_refs: { key: string; id: string }[];
}

export type AdminRoute =
  | { view: "index" }
  | { view: "table"; table: string; params: URLSearchParams }
  | { view: "row"; table: string; rowid: string };

/** adminRoute reads `#/admin`, `#/admin/<table>?...` and `#/admin/<table>/<rowid>`. */
export function adminRoute(hash: string): AdminRoute | null {
  const [path, query] = hash.split("?");
  if (path !== "#/admin" && !path.startsWith("#/admin/")) return null;
  const parts = path.split("/").slice(2).filter(Boolean).map(part => { try { return decodeURIComponent(part); } catch { return part; } });
  if (parts.length === 0) return { view: "index" };
  if (parts.length === 1) return { view: "table", table: parts[0], params: new URLSearchParams(query) };
  return { view: "row", table: parts[0], rowid: parts[1] };
}

export const tableHref = (table: string, params?: URLSearchParams | Record<string, string>): string => {
  const query = params ? new URLSearchParams(params).toString() : "";
  return `#/admin/${encodeURIComponent(table)}${query ? `?${query}` : ""}`;
};
export const rowHref = (table: string, rowid: number | string): string => `#/admin/${encodeURIComponent(table)}/${encodeURIComponent(String(rowid))}`;

/** The parameters the server understands, in the order a URL keeps them. The log is read
 * newest first unless the reader chose otherwise. */
export function serverQuery(table: string, params: URLSearchParams): string {
  const out = new URLSearchParams();
  for (const [key, value] of params) {
    if (key === "limit" || key === "order" || key === "sort" || key === "after" || /^(f|min|max|null)\./.test(key)) out.set(key, value);
  }
  if (table === "events" && !out.has("order")) out.set("order", "desc");
  return out.toString();
}

/** The filters in a table URL, for the chips above the table. */
export function activeFilters(params: URLSearchParams): { key: string; column: string; op: string; value: string }[] {
  const ops: Record<string, string> = { f: "=", min: "≥", max: "≤", null: "is null" };
  const out: { key: string; column: string; op: string; value: string }[] = [];
  for (const [key, value] of params) {
    const match = /^(f|min|max|null)\.(.+)$/.exec(key);
    if (match) out.push({ key, column: match[2], op: ops[match[1]], value: match[1] === "null" ? "" : value });
  }
  return out;
}

const ENTITY_COLUMNS: Record<string, string> = { item_id: "item", run_id: "run", lane_id: "lane" };
export const entityHref = (kind: string, id: string): string | null =>
  kind === "item" ? `#/item/${encodeURIComponent(id)}` :
  kind === "run" ? `#/run/${encodeURIComponent(id)}?tab=transcript` :
  kind === "lane" ? `#/lane/${encodeURIComponent(id)}` : null;

/** Where a cell's value leads, if it is an id another page knows: an item, run or lane id
 * links to that page, and an event's object does by its recorded type. */
export function cellLink(table: string, column: string, value: AdminValue, row: Record<string, AdminValue>): string | null {
  if (typeof value !== "string" || value === "") return null;
  if (ENTITY_COLUMNS[column]) return entityHref(ENTITY_COLUMNS[column], value);
  if (table === "events" && column === "object_id" && typeof row.object_type === "string") return entityHref(row.object_type, value);
  return null;
}

export const isCut = (value: AdminValue): value is AdminCut => typeof value === "object" && value !== null && "truncated" in value;
export const isBlob = (value: AdminValue): value is { blob_bytes: number } => typeof value === "object" && value !== null && "blob_bytes" in value;

/** parseJSON answers the pretty form of a text that is a whole JSON object or array. */
export function prettyJSON(text: string): string | null {
  const head = text.trimStart()[0];
  if (head !== "{" && head !== "[") return null;
  try { return JSON.stringify(JSON.parse(text), null, 2); } catch { return null; }
}
