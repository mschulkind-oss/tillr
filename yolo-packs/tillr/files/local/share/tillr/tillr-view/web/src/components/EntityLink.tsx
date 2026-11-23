import type { ReactNode } from "react";
import { StateGlyph } from "./StateGlyph";

export const ENTITY_KINDS = ["item", "run", "pass", "lane", "check", "landing", "doc", "question"] as const;
export type EntityKind = (typeof ENTITY_KINDS)[number];

/** The shared entity URL and visual treatment, including references not yet in the store. */
export function EntityLink({ kind, id, title, state = "not known", children, overBudget, query }: {
  kind: EntityKind; id: string; title?: string; state?: string; children?: ReactNode; overBudget?: boolean; query?: Record<string, string>;
}) {
  return <a className="entity-link" data-entity-kind={kind} data-entity-id={id} data-over-budget={overBudget}
    href={`/${kind}/${encodeURIComponent(id)}${query ? `?${new URLSearchParams(query)}` : ""}`}>
    <StateGlyph state={state} /><code>{id}</code>{title ? <span>{title}</span> : null}{children}
  </a>;
}

export function ClassState({ value }: { value: string }) {
  const known = ["alarm", "moving", "ready", "needs you", "waits on work", "needs a cause", "landed", "pass", "closed", "skipped", "stopped", "not known", "waits on the loop"].includes(value);
  return <span className="class-state" data-class={known ? value : "not known"}>
    <StateGlyph state={known ? value : "not known"} />{value}
  </span>;
}
