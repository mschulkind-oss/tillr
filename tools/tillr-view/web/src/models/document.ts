// The document pane's link into Vantage (design §8.2, ledger `OQ-OBD2`). One user-scope
// base URL — the server reads it, and every surface that links into Vantage reads the same
// field — plus the document's own path and, when the item names one, a section.
//
// The board builds this URL and never requests it. It does not know whether Vantage is
// running, and a board that waited on a second service would have a second way to be wrong;
// the reader gets a link that does not go anywhere rather than a pane that does not render.

/** vantageLink answers `<base>/#/<path>`, and `<base>/#/<path>#<anchor>` when the item's
 * `body_ref` names a section of the document rather than only the document. A trailing
 * slash on the base and a leading slash on the path are the same slash, so either spelling
 * of the configured base answers one URL rather than a link with `//` in it. */
export function vantageLink(base: string, path: string, anchor: string): string {
  const root = base.trim().replace(/\/+$/, "");
  const document = path.replace(/^\/+/, "");
  const section = anchor === "" ? "" : `#${anchor}`;
  return `${root}/#/${document}${section}`;
}
