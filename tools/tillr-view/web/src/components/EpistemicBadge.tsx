// Architecture and invariants: docs/reference/observatory-design.md
//
// The provenance marker across seven states. Every span, exchange, claim and stage
// renders through this component, so a partial record can never look like a clean pass:
// an absent clock is labelled, never drawn as a zero-width bar.

import type { EpistemicState } from "../models/types";

const LABELS: Record<string, string> = {
  observed: "OBSERVED",
  derived_clock: "DERIVED CLOCK",
  unobserved_seam: "UNOBSERVED SEAM",
  unreadable: "UNREADABLE",
  absent: "ABSENT",
  lane_copy: "LANE COPY",
  no_clock: "NO CLOCK",
  derived: "DERIVED",
  lower_bound: "LOWER BOUND",
};

/** The one sentence each state carries when it needs explaining. `observed` needs none. */
const EXPLANATIONS: Record<string, string> = {
  derived_clock:
    "this clock was inferred — a file's mtime or a parent span's — not recorded by the program itself",
  unobserved_seam: "no emitter recorded this, so the work inside it is unknown rather than empty",
  unreadable: "the record exists and could not be read",
  absent: "no writer produced this record",
  lane_copy: "this is the lane's copy, which the next tick overwrites, not a preserved one",
  no_clock: "the record this came from stamped no clock at all, so none is shown rather than 1970",
  derived: "computed from the run's recorded intervals rather than written as one figure",
  lower_bound: "at least this much was recorded; the true figure may be higher",
};

export function EpistemicBadge({ state }: { state: EpistemicState | string }) {
  const label = LABELS[state] ?? String(state).toUpperCase();
  const explanation = EXPLANATIONS[state];
  return (
    <span
      className={`epistemic-badge epistemic-badge--${state}`}
      data-epistemic-state={state}
      title={explanation}
    >
      {label}
    </span>
  );
}

/** isObserved answers whether a state is recorded fact rather than a gap. Only
 * `observed` is: everything else renders with its marker and its explanation. */
export function isObserved(state: EpistemicState | string): boolean {
  return state === "observed";
}
