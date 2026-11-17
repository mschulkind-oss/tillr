import type { ModelUsage, RunUsage, UsageSum } from "../models/types";

const number = (n: number | null | undefined) => n == null ? "unrecorded" : n.toLocaleString();
export const latency = (n: unknown) => typeof n === "number" ? `${n.toFixed(1)} ms` : "unrecorded";
export const rate = (n: unknown) => typeof n === "number" ? `${n.toFixed(1)} tokens/s` : "unrecorded";
function Kinds({ model }: { model: ModelUsage }) {
  return <li>{model.backend} · {model.model ?? "model unrecorded"}: {model.tokens != null ? `${number(model.tokens)} total · ` : ""}input {number(model.input)} · output {number(model.output)} · cache read {number(model.cache_read)} · cache write {number(model.cache_write)}</li>;
}
export function UsageSummary({ usage }: { usage?: UsageSum }) {
  return <div aria-label="Usage sum"><p>{usage?.lower_bound ? "≥ " : ""}{number(usage?.tokens)} tokens · {usage?.unrecorded_runs ?? "unknown"} unrecorded runs</p>
    <details><summary>Usage by model and backend</summary>{usage?.models.length ? <ul>{usage.models.map((model, i) => <Kinds key={i} model={model} />)}</ul> : <p>Model breakdown unrecorded.</p>}</details></div>;
}
export function RunUsageFigures({ usage }: { usage?: RunUsage }) {
  return <section aria-label="Run usage"><h2>Usage</h2><p>{usage?.backend ?? "backend unrecorded"} · {number(usage?.tokens)} tokens</p>
    <ul>{usage?.models.map((model, i) => <Kinds key={i} model={model} />)}</ul>
    {!usage?.models.length ? <p>Token kinds and model unrecorded.</p> : null}
    <p>Time to first output: {latency(usage?.first_output_ms)} · Output rate: {rate(usage?.output_tokens_per_second)}</p>
    <p>First output means the first streamed text or thinking delta. Output rate divides output tokens by the recorded time from that delta to completion; the run rate uses the sum of those intervals, excluding tools. Missing stream clocks are unrecorded.</p>
  </section>;
}
