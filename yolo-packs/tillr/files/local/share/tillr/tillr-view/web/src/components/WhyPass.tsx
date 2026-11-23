import { EntityLink } from "./EntityLink";
import type { WaitingRow, WhySource } from "../models/types";

/** Says which pass the waiting rows were read from and how old it is, or that no recorded
 * pass could answer and the planner ran (and why). */
export function WhySourceLine({ source }: { source?: WhySource }) {
  if (!source) return null;
  return <p aria-label="Waiting source" className="why-source">{source.kind === "pass"
    ? `The loop's last recorded decisions: as of pass ${source.pass_id}, ${source.age_s} s ago.`
    : `Planned on request, not read from a recorded pass: ${source.fallback ?? "no recorded pass"}.`}</p>;
}

/** Design documents with no item: a short list of their own, never mixed into the rows. */
export function DesignsWaiting({ designs }: { designs?: WaitingRow[] }) {
  if (!designs?.length) return null;
  return <section aria-label="Designs waiting" data-testid="designs-waiting">
    <h2>Designs waiting for an item · {designs.length}</h2>
    <ul>{designs.map(row => <li key={row.document}><EntityLink kind="doc" id={row.document ?? ""} /> · {row.why}</li>)}</ul>
  </section>;
}
