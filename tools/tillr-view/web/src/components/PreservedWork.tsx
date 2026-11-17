import type { SavedWork } from "../models/types";

/** Saved edits are recovery evidence, never a candidate to gate or land. */
export function PreservedWork({ records }: { records: SavedWork[] }) {
  if (!records.length) return null;
  return <section aria-label="Earlier uncommitted work"><h2>Earlier uncommitted work</h2>
    {records.map(record => <p key={record.ref}>{record.pruned ? "Earlier work was saved at " : "Earlier work exists at "}<code>{record.ref}</code>: {record.summary}. Stopped: {record.reason}.
      {record.index_ref ? <> Different staged contents: <code>{record.index_ref}</code>.</> : null}
      {record.pruned ? " The ref was removed after the item became terminal; this is its historical record." : " Inspect this ref before starting over. It is recovery evidence, not a candidate to gate or land."}
    </p>)}
  </section>;
}
