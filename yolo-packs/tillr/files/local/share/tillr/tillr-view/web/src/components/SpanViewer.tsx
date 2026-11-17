import { useEffect, useMemo, useRef, useState } from "react";
import { analyzeSpans, barStyle, duration, windowOf, type TimeWindow, type ViewerSpan } from "../models/spans";
import { EntityLink, type EntityKind } from "./EntityLink";
import { EpistemicBadge } from "./EpistemicBadge";
import { useScreenHash } from "./ObservatoryShell";
import { latency, rate } from "./Usage";
import { URL_CHANGE_EVENT } from "../models/url";

const HEIGHT = 420, ROW = 36;
const time = (n: number | null) => n === null ? "not recorded" : `${n.toFixed(2)} ms`;
function namedEntities(attributes: Record<string, unknown>): { kind: EntityKind; id: string }[] {
  const found = new Map<string, { kind: EntityKind; id: string }>();
  const visit = (value: unknown) => {
    if (!value || typeof value !== "object") return;
    for (const [key, child] of Object.entries(value)) {
      const kind = key === "tick_id" ? "pass" : key.replace(/_id$/, "");
      if (["item", "run", "pass", "check", "landing"].includes(kind) && typeof child === "string" && child) found.set(`${kind}/${child}`, { kind: kind as EntityKind, id: child });
      else if (typeof child === "object") visit(child);
    }
  };
  visit(attributes); return [...found.values()];
}
export function SpanViewer({ spans, initialComplete = true, paused = false }: { spans: ViewerSpan[]; initialComplete?: boolean; paused?: boolean }) {
  const hash = useScreenHash();
  const selectedID = new URLSearchParams(hash.split("?")[1]).get("span");
  const selected = spans.find(s => s.id === selectedID || s.attributes.check_id === selectedID || (Array.isArray(s.attributes.aliases) && s.attributes.aliases.includes(selectedID)));
  const [clock, setClock] = useState(Date.now);
  const hasOpen = spans.some(s => s.open && s.start !== null);
  useEffect(() => {
    if (!hasOpen || paused) return;
    setClock(Date.now());
    const timer = window.setInterval(() => setClock(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [hasOpen, paused]);
  const displaySpans = useMemo(() => spans.map(s => s.open && s.start !== null ? { ...s, end: Math.max(s.start, clock) } : s), [spans, clock]);
  const analysis = useMemo(() => analyzeSpans(displaySpans), [displaySpans]);
  const bounds = useMemo(() => windowOf(displaySpans.map(s => ({ started_at_ms: s.start, finished_at_ms: s.open && s.start !== null ? Math.max(s.end!, s.start + 60000) : s.end }))), [displaySpans]);
  const [axis, setAxis] = useState<TimeWindow | null>(bounds);
  const fitted = useRef(false), touched = useRef(false);
  // Complete the initial fit after all timing reads settle. Later live updates must
  // not change the reader's scale; neither may a late response undo an explicit zoom.
  useEffect(() => {
    if (!touched.current && (!fitted.current || axis === null)) setAxis(bounds);
    if (initialComplete && bounds) fitted.current = true;
  }, [initialComplete, bounds]);
  const [collapsed, setCollapsed] = useState(new Set<string>());
  const query = new URLSearchParams(hash.split("?")[1]).get("q") ?? "";
  const setQuery = (value: string) => {
    const params = new URLSearchParams(window.location.hash.split("?")[1]);
    if (value) params.set("q", value); else params.delete("q");
    window.history.replaceState(null, "", `${window.location.pathname}${window.location.search}${window.location.hash.split("?")[0] || "#/run/unrecorded"}${params.size ? `?${params}` : ""}`);
    window.dispatchEvent(new Event(URL_CHANGE_EVENT));
  };
  const [scroll, setScroll] = useState(0);
  const viewport = useRef<HTMLDivElement>(null);
  const drag = useRef<{ x: number; axis: TimeWindow } | null>(null);
  const matches = useMemo(() => new Set(spans.filter(s => `${s.name} ${JSON.stringify(s.attributes)}`.toLowerCase().includes(query.toLowerCase())).map(s => s.id)), [spans, query]);
  const rows = useMemo(() => {
    const output: { span: ViewerSpan; depth: number }[] = [];
    const stack = (analysis.children.get(null) ?? []).slice().reverse().map(span => ({ span, depth: 0 }));
    while (stack.length) {
      const row = stack.pop()!;
      if (!query || matches.has(row.span.id)) output.push(row);
      if (query || !collapsed.has(row.span.id)) for (const span of [...(analysis.children.get(row.span.id) ?? [])].reverse()) stack.push({ span, depth: row.depth + 1 });
    }
    return output;
  }, [analysis, collapsed, query, matches]);
  useEffect(() => { setScroll(0); if (viewport.current) viewport.current.scrollTop = 0; }, [query]);
  const revealed = useRef<string | null>(null), pendingReveal = useRef<string | null>(null);
  useEffect(() => {
    if (!selected || revealed.current === selectedID) return;
    revealed.current = selectedID;
    pendingReveal.current = selected.id;
    const next = new Set(collapsed); let p = selected.parent;
    const visited = new Set<string>();
    while (p && !visited.has(p)) { visited.add(p); next.delete(p); p = spans.find(s => s.id === p)?.parent ?? null; }
    if (next.size !== collapsed.size) setCollapsed(next);
    // A shared selection is visible even when it was excluded by a local search.
    if (query && !matches.has(selected.id)) setQuery("");
  }, [selectedID, spans]);
  useEffect(() => {
    const index = rows.findIndex(row => row.span.id === pendingReveal.current);
    if (index >= 0 && viewport.current) { const top = Math.max(0, index * ROW - HEIGHT / 2); viewport.current.scrollTop = top; setScroll(top); pendingReveal.current = null; }
  }, [selectedID, rows]);
  const start = Math.min(Math.max(0, Math.floor(scroll / ROW) - 2), Math.max(0, rows.length - 1));
  const visible = rows.slice(start, start + Math.ceil(HEIGHT / ROW) + 4);
  const select = (id: string) => {
    const params = new URLSearchParams(window.location.hash.split("?")[1]); params.set("span", id);
    window.history.pushState(null, "", `${window.location.pathname}${window.location.search}${window.location.hash.split("?")[0] || "#/run/unrecorded"}?${params}`);
    window.dispatchEvent(new Event(URL_CHANGE_EVENT));
  };
  const zoom = (factor: number) => { touched.current = true; setAxis(current => {
    if (!current) return current;
    const center = (current.startMS + current.endMS) / 2, half = Math.max(0.5, (current.endMS - current.startMS) * factor / 2);
    return { startMS: center - half, endMS: center + half };
  }); };
  const pan = (fraction: number) => { touched.current = true; setAxis(current => current ? { startMS: current.startMS + (current.endMS - current.startMS) * fraction, endMS: current.endMS + (current.endMS - current.startMS) * fraction } : null); };
  const fit = () => { touched.current = true; setAxis(bounds); };
  const top = spans.filter(s => analysis.self.get(s.id) !== null && analysis.self.get(s.id) !== undefined).sort((a,b) => analysis.self.get(b.id)! - analysis.self.get(a.id)!).slice(0,10);
  return <section className="span-viewer" aria-label="Run spans">
    <div className="span-controls"><label>Find spans <input type="search" value={query} onChange={e => setQuery(e.target.value)} /></label>
      <output>{matches.size} matched span{matches.size === 1 ? "" : "s"}</output>
      <button onClick={() => zoom(0.5)}>Zoom in</button><button onClick={() => zoom(2)}>Zoom out</button>
      <button onClick={() => pan(-0.2)}>Pan left</button><button onClick={() => pan(0.2)}>Pan right</button>
      <button aria-label="Fit time axis" onClick={fit}>Fit</button></div>
    <p>End-time chain ★: the latest finishing root and descendants that reach its end. Earlier children have spare time before that end. Missing clocks limit the explanation.</p>
    <div role="region" aria-label="Span time axis" tabIndex={0} data-window={axis ? `${axis.startMS},${axis.endMS}` : "unrecorded"}
      onWheel={e => { if (e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY)) pan((e.deltaX || e.deltaY) / 1000); else zoom(e.deltaY > 0 ? 1.2 : 1 / 1.2); }}
      onKeyDown={e => { if (e.target !== e.currentTarget) return; if (["+", "=", "-", "ArrowLeft", "ArrowRight", "f"].includes(e.key)) e.preventDefault(); if (e.key === "+" || e.key === "=") zoom(0.5); if (e.key === "-") zoom(2); if (e.key === "ArrowLeft") pan(-0.2); if (e.key === "ArrowRight") pan(0.2); if (e.key === "f") fit(); }}
      onMouseDown={e => { if (axis && e.button === 0 && !(e.target as HTMLElement).closest("button,a,input")) drag.current = { x: e.clientX, axis }; }}
      onMouseMove={e => { const d = drag.current; if (!d) return; const width = e.currentTarget.getBoundingClientRect().width || 800; const delta = (d.x - e.clientX) / width * (d.axis.endMS - d.axis.startMS); touched.current = true; setAxis({ startMS: d.axis.startMS + delta, endMS: d.axis.endMS + delta }); }}
      onMouseUp={() => { drag.current = null; }} onMouseLeave={() => { drag.current = null; }}>
      <div className="span-axis">{axis ? Array.from({ length: 5 }, (_,i) => <time key={i}>{new Date(axis.startMS + (axis.endMS - axis.startMS) * i / 4).toISOString()}</time>) : "Time axis not recorded"}</div>
      <div className="span-viewport" ref={viewport} style={{ height: HEIGHT, overflowY: "auto" }} onScroll={e => setScroll(e.currentTarget.scrollTop)} role="tree" aria-label="Span rows">
        <div style={{ height: rows.length * ROW, position: "relative" }}>
          {visible.map(({ span: s, depth }, i) => {
            const rendered = s.open ? displaySpans.find(span => span.id === s.id)! : s;
            const bar = barStyle({ started_at_ms: rendered.start, finished_at_ms: rendered.end }, axis);
            const children = analysis.children.get(s.id)?.length;
            return <div key={s.id} data-span-id={s.id} role="treeitem" aria-level={depth + 1} aria-selected={selected?.id === s.id} aria-expanded={children ? !collapsed.has(s.id) : undefined}
              className={`span-row ${duration(rendered) === null ? "span-row--missing" : ""} ${analysis.critical.has(s.id) ? "span-row--critical" : ""}`} style={{ position: "absolute", top: (start + i) * ROW, height: ROW, width: "100%" }}>
              <div className="span-label" style={{ paddingLeft: depth * 16 }}>{children ? <button aria-label={`${collapsed.has(s.id) ? "Expand" : "Collapse"} ${s.name}`} onClick={() => setCollapsed(current => { const next = new Set(current); if (next.has(s.id)) next.delete(s.id); else next.add(s.id); return next; })}>{collapsed.has(s.id) ? "▸" : "▾"}</button> : null}
                <button aria-label={`Select ${s.name}`} onClick={() => select(s.id)}>{analysis.critical.has(s.id) ? "★ " : ""}{s.name}</button><EpistemicBadge state={s.state} />{s.attributes.category === "llm_turn" ? <small> First output {latency(s.attributes.first_output_ms)} · {rate(s.attributes.output_tokens_per_second)}</small> : null}</div>
              <div className="span-track" title={s.reason}>{duration(rendered) === null ? <span className="waterfall__gap-notice">{s.reason}</span> : bar ? <span data-clock={s.open ? "viewer" : "recorded"} className={`waterfall__bar waterfall__bar--${s.open ? "derived_clock" : s.state}`} style={bar} title={`${s.name}: ${time(duration(rendered))}${s.open ? " so far — viewer's clock; finish not recorded" : ""}`} /> : <span>outside time window</span>}</div>
            </div>;
          })}
        </div>
      </div>
    </div>
    <div className="span-panels"><section aria-label="Most uncovered time"><h2>Ten spans with the most time not covered by children</h2><ol>{top.map(s => <li key={s.id}><a href={`${hash.split("?")[0]}?span=${encodeURIComponent(s.id)}`} onClick={e => { e.preventDefault(); select(s.id); }}>{s.name}</a> {time(analysis.self.get(s.id) ?? null)}</li>)}</ol></section>
      {selected ? <section aria-label="Span details"><h2>{selected.name}</h2><dl><dt>Start</dt><dd>{selected.start === null ? "not recorded" : new Date(selected.start).toISOString()}</dd><dt>Duration</dt><dd>{time(duration(selected))}</dd><dt>Time not covered by children</dt><dd>{time(analysis.self.get(selected.id) ?? null)}</dd></dl>
        <h3>Recorded attributes</h3><dl>{Object.entries(selected.attributes).map(([key, value]) => <div key={key}><dt>{key}</dt><dd><code>{typeof value === "string" ? value : JSON.stringify(value)}</code></dd></div>)}</dl>
        <nav aria-label="Span entities">{namedEntities(selected.attributes).map(({kind,id}) => <EntityLink key={`${kind}/${id}`} kind={kind} id={id} query={kind === "check" ? { span: selected.id, ...(typeof selected.attributes.run_id === "string" ? { run: selected.attributes.run_id } : {}) } : undefined} />)}</nav>
        {["llm_turn", "tool_call"].includes(String(selected.attributes.category)) ? <p><a href={`${hash.split("?")[0]}?span=${encodeURIComponent(selected.id)}&tab=transcript`}>Open this run's transcript</a> — the timeline route does not record a transcript entry identifier for this span.</p> : null}</section> : selectedID ? <p role="alert">Span {selectedID} is not recorded in this run.</p> : <p>Select a span for its clocks, attributes and entity links.</p>}</div>
  </section>;
}
