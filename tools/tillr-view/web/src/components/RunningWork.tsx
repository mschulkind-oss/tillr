import { durationText } from "../models/now";
import type { RunningWorkPayload, RunningWorkRow, RunningWorkSummary } from "../models/types";
import { EntityLink } from "./EntityLink";

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let value = bytes;
  let idx = 0;
  while (value >= 1024 && idx < units.length - 1) {
    value /= 1024;
    idx++;
  }
  return idx === 0 ? `${Math.round(value)} B` : `${value.toFixed(1)} ${units[idx]}`;
}

export function formatRunningWorkSummaryLine(summary: RunningWorkSummary): string {
  const r = summary.ordinary_running;
  const l = summary.agent_limit;
  const express = summary.express_running;
  const idle = r === 0 && express === 0 && summary.command_running === 0;
  const runningPart = l === null
    ? (r === 0 && express === 0 ? "0 running · idle" : `${r} running${express > 0 ? `, ${express} owner-awaited` : ""}`)
    : `${r} of ${l} running${express > 0 ? `, ${express} owner-awaited` : ""}${idle ? " · idle" : ""}`;

  const wt = summary.worktrees;
  const wtPart = wt.count_limit > 0
    ? `${wt.on_disk} of ${wt.count_limit} worktrees on disk`
    : `${wt.on_disk} worktrees on disk`;
  const diskPart = wt.disk_budget_bytes > 0
    ? `${formatBytes(wt.total_bytes)} of ${formatBytes(wt.disk_budget_bytes)} (${formatBytes(wt.worktree_bytes)} worktrees, ${formatBytes(wt.scratch_bytes)} scratch)`
    : `${formatBytes(wt.total_bytes)} (${formatBytes(wt.worktree_bytes)} worktrees, ${formatBytes(wt.scratch_bytes)} scratch)`;

  return `${runningPart} · ${wtPart} · ${diskPart}`;
}

function formatSpend(row: RunningWorkRow): string {
  const parts: string[] = [];
  if (row.spend.tokens !== null && row.spend.tokens !== undefined) {
    parts.push(`${row.spend.tokens.toLocaleString()} tok`);
  }
  if (row.spend.cost_usd !== null && row.spend.cost_usd !== undefined) {
    parts.push(`$${row.spend.cost_usd.toFixed(2)}`);
  }
  if (parts.length > 0) return parts.join(" · ");
  return row.spend.note || "—";
}

function livenessText(row: RunningWorkRow, now: number): string {
  if (row.log_grew_at_ms !== null && row.log_grew_at_ms !== undefined) {
    const age = Math.max(0, now - row.log_grew_at_ms);
    if (age >= 60_000) {
      return `quiet ${durationText(age)}`;
    }
    return `log grew ${durationText(age)} ago`;
  }
  return "no log yet";
}

export function RunningWork({
  data,
  summaryFallback,
  error,
  now = Date.now(),
}: {
  data?: RunningWorkPayload;
  summaryFallback?: RunningWorkSummary;
  error?: Error | null;
  now?: number;
}) {
  const summary = data?.summary ?? summaryFallback;
  const rows = data?.rows ?? [];

  return (
    <section className="lane-strip" aria-label="Running work">
      <h2>Running work</h2>
      {error ? (
        <p role="alert">Running work unavailable: {error.message}</p>
      ) : data === undefined && summary === undefined ? (
        <p role="status">Reading running work…</p>
      ) : (
        <>
          {summary ? <p className="lane-strip__summary">{formatRunningWorkSummaryLine(summary)}</p> : null}
          {rows.length > 0 ? (
            <table className="lanes-table">
              <thead>
                <tr>
                  <th>Item</th>
                  <th>Node</th>
                  <th>Seat</th>
                  <th>Backend</th>
                  <th>Elapsed</th>
                  <th>Liveness</th>
                  <th>Latest action</th>
                  <th>Spend</th>
                </tr>
              </thead>
              <tbody>
                {rows.map(row => (
                  <tr key={row.run_id}>
                    <td>
                      <EntityLink kind="item" id={row.item_id} />
                      {row.item_title ? <span className="muted"> · {row.item_title}</span> : null}
                      {row.express ? <span className="pill">owner-awaited</span> : null}
                    </td>
                    <td>
                      <code>{row.node_id || row.run_id}</code>
                      <span className="muted"> · {row.node_kind}</span>
                    </td>
                    <td>{row.seat || "—"}</td>
                    <td>{row.backend || "—"}</td>
                    <td>{row.started_at_ms > 0 ? durationText(Math.max(0, now - row.started_at_ms)) : "—"}</td>
                    <td>{livenessText(row, now)}</td>
                    <td>{row.latest_action?.text || "—"}</td>
                    <td>{formatSpend(row)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : null}
        </>
      )}
    </section>
  );
}
