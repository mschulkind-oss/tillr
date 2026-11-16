import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchCheck, fetchPassRange, fetchPerformanceChecks, fetchPerformanceItems } from "../models/api";
import { FALLBACK_POLL_MS } from "../models/queries";
import type { ApiEnvelope, PassRow, SnapshotData } from "../models/types";
import { EntityLink } from "./EntityLink";
import { RefusalState, refusalCode } from "./RefusalState";

const ranges = ["24h", "7d", "30d", "all"] as const;
type Range = typeof ranges[number];
const durations = { "24h": 86400000, "7d": 7 * 86400000, "30d": 30 * 86400000 };
const ms = (value: number | null) => value === null ? "unrecorded" : `${value.toLocaleString()} ms`;
const hasPhases = (row: PassRow) => row.phases != null && Object.values(row.phases).some(value => value !== null);
const passStamp = (at: number) => new Date(at).toISOString().replace(/[-:]/g, "").replace(/\.\d{3}Z$/, "Z");

function EmptyRange({ available, error, loading }: { available?: boolean; error: Error | null; loading: boolean }) {
  if (error) return <p role="alert">Wider history could not be read: {error.message}</p>;
  if (loading) return <p role="status">No data in this range. Reading the widest range…</p>;
  return <p>{available ? <>No data in this range. Widest range with data: <a href="#/performance?range=all">all</a>.</> : "No recorded data in any range."}</p>;
}

function Section({ name, error, pending, retry, children }: { name: string; error: Error | null; pending: boolean; retry: () => void; children: ReactNode }) {
  return <section className="performance__section" aria-label={name}><h2>{name}</h2>
    {error ? <RefusalState code={refusalCode(error)} message={error.message} sentences={{}} onRetry={retry} /> : pending ? <p role="status">Reading {name.toLowerCase()}…</p> : children}
  </section>;
}

export function PerformanceView({ route, snapshot, snapshotError = null, paused }: {
  route: string; snapshot?: ApiEnvelope<SnapshotData>; snapshotError?: Error | null; paused: boolean;
}) {
  const selected = new URLSearchParams(route.split("?")[1] ?? "").get("range") ?? "24h";
  const range: Range = ranges.includes(selected as Range) ? selected as Range : "24h";
  // Every section shares the server snapshot clock, never the browser's wall clock.
  const end = snapshot ? Date.parse(snapshot.snapshot.generated_at) : NaN;
  const since = range === "all" ? null : Math.max(0, end - durations[range]);
  const bounds = new URLSearchParams({ to_ms: String(end) });
  if (since !== null) bounds.set("from_ms", String(since));
  const options = { enabled: Number.isFinite(end), retry: false, refetchOnWindowFocus: false, refetchInterval: paused ? false : FALLBACK_POLL_MS } as const;
  const checks = useQuery({ ...options, queryKey: ["performance", "checks", since, end], queryFn: ({signal}) => fetchPerformanceChecks(String(bounds), signal) });
  const items = useQuery({ ...options, queryKey: ["performance", "items", since, end], queryFn: ({signal}) => fetchPerformanceItems(String(bounds), signal) });
  const passes = useQuery({ ...options, queryKey: ["passes", "performance", since, end], queryFn: ({signal}) => fetchPassRange(since, end, signal) });
  const checkRows = [...(checks.data?.data.checks ?? [])].sort((a,b) => (b.p95_ms ?? -1) - (a.p95_ms ?? -1));
  const itemRows = [...(items.data?.data.items ?? [])].sort((a,b) => (b.usage_tokens ?? -1) - (a.usage_tokens ?? -1));
  const phaseRows = (passes.data ?? []).filter(hasPhases);
  const trend = (snapshot?.data.drain_summary.gate_trend ?? []).filter(row => row.at_ms < end && (since === null || row.at_ms >= since));
  const trendClocks = new Set(trend.map(row => row.at_ms));
  const missingBudgets = phaseRows.filter(row => row.phases?.gate_ms != null && !trendClocks.has(row.dispatched_at_ms)).length;
  const wideOptions = { ...options, enabled: options.enabled && range !== "all" };
  const wideChecks = useQuery({ ...wideOptions, enabled: wideOptions.enabled && checks.isSuccess && checkRows.length === 0, queryKey: ["performance", "checks", null, end], queryFn: ({signal}) => fetchPerformanceChecks(`to_ms=${end}`, signal) });
  const wideItems = useQuery({ ...wideOptions, enabled: wideOptions.enabled && items.isSuccess && itemRows.length === 0, queryKey: ["performance", "items", null, end], queryFn: ({signal}) => fetchPerformanceItems(`to_ms=${end}`, signal) });
  const widePasses = useQuery({ ...wideOptions, enabled: wideOptions.enabled && passes.isSuccess && phaseRows.length === 0, queryKey: ["passes", "performance", null, end], queryFn: ({signal}) => fetchPassRange(null, end, signal) });
  const emptyPasses = <EmptyRange available={(widePasses.data ?? []).some(hasPhases)} error={range === "all" ? null : widePasses.error} loading={range !== "all" && widePasses.isPending} />;
  const maxCheck = Math.max(1, ...checkRows.map(row => row.p95_ms ?? 0));
  const maxGate = Math.max(1, ...trend.flatMap(row => [row.gate_ms, row.budget_ms]));
  const maxPhase = Math.max(1, ...phaseRows.map(row => Object.values(row.phases!).reduce<number>((sum, value) => sum + (value ?? 0), 0)));
  return <main className="performance">
    <h1>Performance</h1>
    <nav aria-label="Performance range">{ranges.map(value => <a key={value} href={`#/performance?range=${value}`} aria-current={range === value ? "page" : undefined}>{value}</a>)}</nav>
    {!ranges.includes(selected as Range) ? <p role="alert">Unknown range {selected}; showing 24h.</p> : null}
    {Number.isFinite(end) ? <p>{since === null ? "All recorded history" : new Date(since).toISOString()} → {new Date(end).toISOString()} (snapshot time; end exclusive)</p> : <p role={snapshotError ? "alert" : "status"}>{snapshotError ? snapshotError.message : "Reading the snapshot range clock…"}</p>}
    <Section name="Gate checks" error={checks.error} pending={checks.isPending} retry={() => void checks.refetch()}>
      {checkRows.length === 0 ? <EmptyRange available={(wideChecks.data?.data.checks.length ?? 0) > 0} error={range === "all" ? null : wideChecks.error} loading={range !== "all" && wideChecks.isPending} /> : <table><thead><tr><th>Check</th><th>n timed / executions</th><th>p50</th><th>p95</th><th>Max</th><th>Waited</th><th>Failed</th></tr></thead><tbody>{checkRows.map(row => <tr key={row.check_id}>
        <td><EntityLink kind="check" id={row.check_id} />{row.p95_ms !== null ? <span className="performance__check-bar" style={{width: `${100 * row.p95_ms / maxCheck}%`}} /> : null}</td><td>{row.sample_count} / {row.count}</td><td>{ms(row.median_ms)}</td><td>{row.p95_ms === null ? "p95 unavailable (fewer than 5 timed samples)" : ms(row.p95_ms)}</td><td>{ms(row.max_ms)}</td><td>{ms(row.waited_ms)}</td><td>{row.failures}</td>
      </tr>)}</tbody></table>}
    </Section>
    <Section name="Gate wall per pass" error={passes.error} pending={passes.isPending} retry={() => void passes.refetch()}>
      {phaseRows.length === 0 ? emptyPasses : <>
        <p>Bars show served budgets only. ▲ means recorded over budget.</p>
        <div className="performance__gate-bars">{trend.map(row => <EntityLink key={row.tick_id} kind="pass" id={row.tick_id} overBudget={row.over_budget}>
          <span className="performance__gate-plot"><span className="performance__gate-fill" style={{height: `${100 * row.gate_ms / maxGate}%`}} /><span className="performance__budget-line" style={{bottom: `${100 * row.budget_ms / maxGate}%`}} /></span>
          <span>{row.over_budget ? "▲ Over budget · " : ""}{ms(row.gate_ms)} · Budget {row.budget_ms} ms</span>
        </EntityLink>)}</div>
        {missingBudgets > 0 ? <p>{missingBudgets} gated passes in this range have no budget served; no gate-wall bar is drawn for them.</p> : null}
        {trend.length === 0 ? <p>No gate budgets served in this range.</p> : null}
      </>}
    </Section>
    <Section name="Phases per pass" error={passes.error} pending={passes.isPending} retry={() => void passes.refetch()}>
      {phaseRows.length === 0 ? emptyPasses : <ul className="performance__phases">{phaseRows.map(row => <li key={row.dispatched_at_ms}><EntityLink kind="pass" id={passStamp(row.dispatched_at_ms)}>
        <span className="performance__phase-plot">{Object.entries(row.phases!).map(([phase,value]) => value === null ? null : <span key={phase} className={`performance__phase performance__phase--${phase}`} style={{width: `${100 * value / maxPhase}%`}} />)}</span>
        <span>{Object.entries(row.phases!).map(([phase,value]) => `${phase.replace("_ms", "")}: ${ms(value)}`).join(" · ")}</span>
      </EntityLink></li>)}</ul>}
    </Section>
    <Section name="Items" error={items.error} pending={items.isPending} retry={() => void items.refetch()}>
      {itemRows.length === 0 ? <EmptyRange available={(wideItems.data?.data.items.length ?? 0) > 0} error={range === "all" ? null : wideItems.error} loading={range !== "all" && wideItems.isPending} /> : <table><thead><tr><th>Item</th><th>Runs</th><th>Tokens</th><th>Cost</th><th>Unrecorded runs</th></tr></thead><tbody>{itemRows.map(row => <tr key={row.item_id}><td><EntityLink kind="item" id={row.item_id} /></td><td>{row.run_count}</td><td>{row.usage_tokens === null ? "unrecorded" : `${row.lower_bound || row.tokens_unrecorded_runs > 0 ? "≥ " : ""}${row.usage_tokens.toLocaleString()}`}</td><td>{row.cost_usd === null ? "unrecorded" : `${row.lower_bound || row.cost_unrecorded_runs > 0 ? "≥ " : ""}$${row.cost_usd.toFixed(2)}`}</td><td>{row.unrecorded_runs} wholly · {row.tokens_unrecorded_runs} tokens · {row.cost_unrecorded_runs} cost</td></tr>)}</tbody></table>}
    </Section>
  </main>;
}

export function CheckView({ route, paused }: { route: string; paused: boolean }) {
  let id = route.slice("#/check/".length).split("?")[0];
  try { id = decodeURIComponent(id); } catch { /* Show the literal malformed identity. */ }
  const params = new URLSearchParams(route.split("?")[1]);
  const execution = params.get("span"), executionRun = params.get("run");
  const query = useQuery({ queryKey: ["checks", id], queryFn: ({signal}) => fetchCheck(id, signal), retry: false, refetchOnWindowFocus: false, refetchInterval: paused ? false : FALLBACK_POLL_MS });
  return <main className="performance"><h1>Check <EntityLink kind="check" id={id} /></h1><a href="#/performance">Performance</a>
    <Section name="Executions" error={query.error} pending={query.isPending} retry={() => void query.refetch()}>
      <table><thead><tr><th>Run</th><th>Item</th><th>Pass</th><th>Result</th><th>Duration</th><th>Wait measurements</th></tr></thead><tbody>{query.data?.data.executions.map(row => <tr key={row.span_id} aria-selected={execution === row.span_id && (!executionRun || executionRun === row.run_id)}>
        <td><EntityLink kind="run" id={row.run_id} /><a href={`#/run/${encodeURIComponent(row.run_id)}?span=${encodeURIComponent(row.span_id)}`}>Execution {row.span_id}</a></td><td>{row.item_id ? <EntityLink kind="item" id={row.item_id} /> : "Item unrecorded"}</td><td>{row.tick_id ? <EntityLink kind="pass" id={row.tick_id} /> : "Pass unrecorded"}</td><td>{row.status}</td><td>{ms(row.duration_ms)}</td><td><code>{row.waited_ms_json}</code></td>
      </tr>)}</tbody></table>
    </Section>
  </main>;
}
