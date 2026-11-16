import type { EpistemicState, GateData, RunData, TimelineData } from "./types";

export interface ViewerSpan {
  id: string; parent: string | null; name: string;
  start: number | null; end: number | null; state: EpistemicState;
  attributes: Record<string, unknown>; reason: string; open?: boolean;
}
export interface TimeWindow { startMS: number; endMS: number }
export function windowOf(spans: { started_at_ms: number | null; finished_at_ms: number | null }[]): TimeWindow | null {
  const clocked = spans.filter(s => s.started_at_ms !== null && s.finished_at_ms !== null && s.finished_at_ms >= s.started_at_ms);
  if (!clocked.length) return null;
  let startMS = Infinity, endMS = -Infinity;
  for (const s of clocked) { startMS = Math.min(startMS, s.started_at_ms!); endMS = Math.max(endMS, s.finished_at_ms!); }
  return { startMS, endMS: Math.max(endMS, startMS + 1) };
}
export function barStyle(span: { started_at_ms: number | null; finished_at_ms: number | null }, window: TimeWindow | null) {
  if (!window || span.started_at_ms === null || span.finished_at_ms === null || span.finished_at_ms < span.started_at_ms) return null;
  const width = window.endMS - window.startMS;
  if (span.finished_at_ms < window.startMS || span.started_at_ms > window.endMS) return null;
  const start = Math.max(window.startMS, span.started_at_ms), end = Math.min(window.endMS, span.finished_at_ms);
  return { left: `${100 * (start - window.startMS) / width}%`, width: `${100 * (end - start) / width}%`, minWidth: "2px" };
}
export function duration(s: ViewerSpan): number | null {
  return s.start !== null && s.end !== null && Number.isFinite(s.start) && Number.isFinite(s.end) && s.end >= s.start ? s.end - s.start : null;
}
function objectJSON(raw: string): Record<string, unknown> {
  try { const value: unknown = JSON.parse(raw); return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : { recorded_json: value }; }
  catch { return { unreadable_json: raw }; }
}
export function waitedTotal(span: { waited_ms_json: string }): number | null {
  const values = Object.values(objectJSON(span.waited_ms_json)).filter((v): v is number => typeof v === "number" && Number.isFinite(v) && v >= 0);
  return values.length ? values.reduce((a, b) => a + b, 0) : null;
}

/** Combine existing read faces without assigning a clock to an unrecorded stage. IDs
 * issued by the timeline and gate remain stable so execution links can reopen them. */
export function assembleSpans(run: RunData, timeline?: TimelineData, gate?: GateData): ViewerSpan[] {
  const runID = run.run.run_id, common = { run_id: runID, item_id: run.run.item_id };
  const spans = new Map<string, ViewerSpan>();
  const stages = ["stage", "run", "boundary", "fence", "gate", "land", "verdict"];
  for (const name of stages) {
    const s = run.stages.find(row => row.stage === name);
    spans.set(`${runID}/${name}`, { id: `${runID}/${name}`, parent: null, name,
      start: s?.started_at_ms ?? null, end: name === "run" && run.run.state === "running" && s?.finished_at_ms === 0 ? null : s?.finished_at_ms ?? null,
      state: s?.epistemic_state ?? "absent", open: name === "run" && run.run.state === "running" && (s?.finished_at_ms === 0 || s?.finished_at_ms == null), attributes: { ...common, ...s },
      reason: `${s?.summary ?? "the server answered no row for this stage"} — no clock recorded — not drawn` });
  }
  for (const s of timeline?.spans ?? []) spans.set(s.span_id, {
    id: s.span_id, parent: s.parent_span_id, name: s.label,
    start: s.started_at_ms, end: s.finished_at_ms === 0 && run.run.state === "running" ? null : s.finished_at_ms,
    state: s.epistemic_state, open: s.span_id === `${runID}/run` && run.run.state === "running" && (s.finished_at_ms === 0 || s.finished_at_ms === null), attributes: { ...common, ...s }, reason: `${s.label} — no clock recorded — not drawn`,
  });
  const gateID = gate?.plan_span?.span_id || `${runID}/gate`;
  if (gate?.plan_span) {
    const p = gate.plan_span;
    // /timeline also serves the stored plan. Keep its recorded identity, replacing
    // the coarse file-clock gate stage rather than adding another timed root.
    spans.delete(`${runID}/gate`);
    spans.set(gateID, { id: gateID, parent: null, name: p.label, start: p.started_at_ms, end: p.finished_at_ms,
      state: p.epistemic_state, attributes: { ...common, ...p, ...objectJSON(p.attributes_json), stage_record: run.stages.find(s => s.stage === "gate") ?? null, timeline_record: timeline?.spans.find(s => s.span_id === p.span_id) ?? null, aliases: [`${runID}/gate`] }, reason: "gate plan — no clock recorded — not drawn" });
  }
  const gateIDs = new Set(gate?.spans.map(s => s.span_id) ?? []);
  for (const s of gate?.spans ?? []) spans.set(s.span_id, {
    id: s.span_id, parent: gateIDs.has(s.parent_span_id) ? s.parent_span_id : gateID,
    name: s.label, start: s.started_at_ms, end: s.finished_at_ms, state: s.epistemic_state,
    attributes: { ...common, ...s, ...objectJSON(s.attributes_json) }, reason: `${s.label} — no clock recorded — not drawn`,
  });
  for (const s of gate?.skipped_checks ?? []) spans.set(`${gateID}/skip/${s.check_id}`, {
    id: `${gateID}/skip/${s.check_id}`, parent: gateID, name: s.label, start: null, end: null,
    state: "absent", attributes: { ...common, ...s }, reason: s.reason,
  });
  return [...spans.values()];
}

/** Uncovered time is duration minus the union of direct children's clock intervals,
 * clipped to the parent. The end-time chain starts at the latest finishing root. A
 * child belongs only if it reaches its parent's finish: an earlier child has spare
 * time and cannot determine the end based on recorded clocks alone. */
export function analyzeSpans(spans: ViewerSpan[]) {
  const byID = new Map(spans.map(s => [s.id, s]));
  const children = new Map<string | null, ViewerSpan[]>();
  for (const span of spans) {
    // Break malformed cycles deterministically so traversal always terminates.
    let parent = span.parent && byID.has(span.parent) ? span.parent : null;
    const seen = new Set([span.id]); let cursor = parent;
    while (cursor) { if (seen.has(cursor)) { parent = null; break; } seen.add(cursor); cursor = byID.get(cursor)?.parent ?? null; }
    const group = children.get(parent) ?? []; group.push(span); children.set(parent, group);
  }
  const self = new Map<string, number | null>();
  for (const span of spans) {
    const total = duration(span);
    if (total === null) { self.set(span.id, null); continue; }
    const intervals = (children.get(span.id) ?? []).filter(c => duration(c) !== null)
      .map(c => [Math.max(span.start!, c.start!), Math.min(span.end!, c.end!)]).filter(([a,b]) => b > a).sort((a,b) => a[0] - b[0]);
    let covered = 0, end = span.start!;
    for (const [a,b] of intervals) { covered += Math.max(0, b - Math.max(a, end)); end = Math.max(end, b); }
    self.set(span.id, total - covered);
  }
  const critical = new Set<string>();
  let candidates = children.get(null) ?? [];
  while (candidates.length) {
    const latest = candidates.filter(c => duration(c) !== null).sort((a,b) => b.end! - a.end!)[0];
    if (!latest || critical.has(latest.id)) break;
    critical.add(latest.id); candidates = (children.get(latest.id) ?? []).filter(child => child.end === latest.end);
  }
  return { children, self, critical };
}
