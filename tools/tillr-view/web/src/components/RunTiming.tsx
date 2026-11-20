// Where one run's wall time went, and what the drain decided about it, on one page.
//
// The split is the server's (`timing` on the run): model time, tool time, the part of tool
// time spent waiting for a test slot, active tool time, and remaining uncovered harness time.
// A figure the run's records do not carry renders as "not recorded" with the reason — never
// as 0s — and a figure derived or only a lower bound says so.
//
// The chronological stream merges the drain's scheduling decisions (allocation, gate start
// and end, refusal, landing, retry) with the agent's transcript entries by recorded clock,
// deduplicating supervisor `steps.jsonl` rows already present in `schedule`, and paging
// backward through `before` cursors when a run has more than one transcript page.

import { useEffect, useMemo, useRef, useState } from "react";

import { EntityLink } from "./EntityLink";
import { EpistemicBadge } from "./EpistemicBadge";
import { RefusalState, refusalCode } from "./RefusalState";
import { fetchRunTranscript } from "../models/api";
import type { RunTiming, ScheduleEntry, TimingMetric, TranscriptData, TranscriptEntry } from "../models/types";

export function spokenDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

/** metricText is a figure as the card spells it: `≥` for a lower bound, `~` for a derived
 * one, and "not recorded" when the record is absent. */
export function metricText(metric: TimingMetric): string {
  if (metric.ms === null) return "not recorded";
  const prefix =
    metric.epistemic_state === "lower_bound"
      ? "≥ "
      : metric.epistemic_state === "derived" || metric.epistemic_state === "derived_clock"
        ? "~"
        : "";
  return prefix + spokenDuration(metric.ms);
}

const ROWS: { key: keyof RunTiming; label: string }[] = [
  { key: "wall_ms", label: "Wall time" },
  { key: "model_ms", label: "Model time" },
  { key: "tool_ms", label: "Tool time" },
  { key: "slot_wait_ms", label: "of which waiting for a test slot" },
  { key: "tool_active_ms", label: "Tool time not waiting" },
  { key: "uncovered_ms", label: "Uncovered / harness time" },
];

export function WallTimeCard({ timing }: { timing: RunTiming | undefined }) {
  if (!timing) {
    return (
      <section aria-label="Where wall time went">
        <h2>Where wall time went</h2>
        <p>This server did not send a wall-time split for the run: it predates the field.</p>
      </section>
    );
  }
  const wall = timing.wall_ms.ms;
  return (
    <section aria-label="Where wall time went">
      <h2>Where wall time went</h2>
      <dl>
        {ROWS.map(({ key, label }) => {
          const metric = timing[key];
          if (!metric) return null;
          const share = wall && metric.ms !== null && key !== "wall_ms" ? ` (${Math.round((100 * metric.ms) / wall)}% of wall)` : "";
          return (
            <div key={key} data-timing={key}>
              <dt>{label}</dt>
              <dd>
                {metricText(metric)}
                {share} <EpistemicBadge state={metric.epistemic_state} />
                {metric.note ? <small> {metric.note}</small> : null}
              </dd>
            </div>
          );
        })}
      </dl>
    </section>
  );
}

function ScheduleLinks({ links }: { links: Record<string, string> | undefined }) {
  if (!links) return null;
  return (
    <>
      {Object.entries(links).map(([k, v]) => {
        if (!v) return null;
        if (k === "item_id") return <span key={k}><EntityLink kind="item" id={v} /> </span>;
        if (k === "lane_id") return <span key={k}><EntityLink kind="lane" id={v} /> </span>;
        if (k === "pass_id") return <span key={k}><EntityLink kind="pass" id={v} /> </span>;
        if (k === "landing_id") return <span key={k}><EntityLink kind="landing" id={v} /> </span>;
        if (k.endsWith("run_id")) return <span key={k}><EntityLink kind="run" id={v} /> </span>;
        return null;
      })}
    </>
  );
}

export function DrainScheduleLedger({
  schedule,
  onOpenTimeOrder,
}: {
  schedule: ScheduleEntry[] | undefined;
  onOpenTimeOrder?: () => void;
}) {
  if (schedule === undefined) return null;
  return (
    <section aria-label="Drain scheduling decisions">
      <h2>Drain scheduling decisions</h2>
      {onOpenTimeOrder ? (
        <p>
          <button type="button" onClick={onOpenTimeOrder}>Interleave with agent entries</button>
        </p>
      ) : null}
      {schedule.length === 0 ? (
        <p>No drain scheduling decisions are recorded for this run.</p>
      ) : (
        <ol>
          {schedule.map((entry, i) => (
            <li key={i} data-stream="schedule" data-category={entry.category}>
              <time>{entry.at_ms === null ? "no clock" : new Date(entry.at_ms).toISOString()}</time>{" "}
              <strong>drain · {entry.category}</strong> {entry.summary}{" "}
              <ScheduleLinks links={entry.links} />
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

export type StreamRow =
  | { at: number | null; order: number; type: "schedule"; entry: ScheduleEntry }
  | { at: number | null; order: number; type: "agent"; entry: TranscriptEntry };

/** mergeStream orders the drain's decisions and the agent's entries on one clock.
 * Supervisor `steps.jsonl` rows (`source === "steps"`) are dropped from `entries` when
 * `schedule` is supplied because `schedule` already projects `steps.jsonl` start and end
 * records. Ties keep the drain's decision first; entries with no clock come last. */
export function mergeStream(schedule: ScheduleEntry[], entries: TranscriptEntry[]): StreamRow[] {
  const agentEntries = entries.filter(e => e.source !== "steps");
  const rows: StreamRow[] = [
    ...schedule.map((entry, i): StreamRow => ({ at: entry.at_ms, order: i, type: "schedule", entry })),
    ...agentEntries.map((entry, i): StreamRow => ({ at: entry.at_ms, order: schedule.length + i, type: "agent", entry })),
  ];
  return rows.sort((a, b) => {
    if (a.at === null || b.at === null) return a.at === b.at ? a.order - b.order : a.at === null ? 1 : -1;
    return a.at - b.at || (a.type === b.type ? a.order - b.order : a.type === "schedule" ? -1 : 1);
  });
}

function mergeEntries(earlier: TranscriptEntry[], later: TranscriptEntry[]): TranscriptEntry[] {
  const seen = new Map<string, TranscriptEntry>();
  for (const entry of earlier) seen.set(entry.id ?? `${entry.source}:${entry.seq}`, entry);
  for (const entry of later) seen.set(entry.id ?? `${entry.source}:${entry.seq}`, entry);
  return [...seen.values()];
}

type Filter = "all" | "schedule" | "agent";

export function TimeOrderStream({
  runId,
  schedule,
  transcript,
  entries,
  entriesPending = false,
  transcriptError = null,
}: {
  runId?: string;
  schedule: ScheduleEntry[] | undefined;
  transcript?: TranscriptData;
  entries?: TranscriptEntry[];
  entriesPending?: boolean;
  transcriptError?: Error | null;
}) {
  const [filter, setFilter] = useState<Filter>("all");
  const [earlier, setEarlier] = useState<TranscriptEntry[]>([]);
  const [cursor, setCursor] = useState<string | null | undefined>(undefined);
  const [loadingEarlier, setLoadingEarlier] = useState(false);
  const [pageError, setPageError] = useState<Error | null>(null);
  const activeRunRef = useRef(runId);
  activeRunRef.current = runId;

  useEffect(() => {
    setEarlier([]);
    setCursor(undefined);
    setLoadingEarlier(false);
    setPageError(null);
  }, [runId]);

  const baseEntries = transcript?.entries ?? entries ?? [];
  const mergedEntries = useMemo(() => mergeEntries(earlier, baseEntries), [earlier, baseEntries]);
  const agentEntryCount = useMemo(() => mergedEntries.filter(e => e.source !== "steps").length, [mergedEntries]);
  const effectiveBefore = cursor === undefined ? (transcript?.before ?? null) : cursor;

  const loadOneEarlierPage = async () => {
    if (!runId || !effectiveBefore || loadingEarlier) return;
    const targetRun = runId;
    setLoadingEarlier(true);
    setPageError(null);
    try {
      const page = await fetchRunTranscript(targetRun, effectiveBefore);
      if (activeRunRef.current !== targetRun) return;
      setEarlier(prev => mergeEntries(page.data.entries, prev));
      setCursor(page.data.before);
    } catch (err) {
      if (activeRunRef.current !== targetRun) return;
      setPageError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      if (activeRunRef.current === targetRun) setLoadingEarlier(false);
    }
  };

  const loadAllEarlierPages = async () => {
    if (!runId || !effectiveBefore || loadingEarlier) return;
    const targetRun = runId;
    setLoadingEarlier(true);
    setPageError(null);
    let nextCursor: string | null = effectiveBefore;
    let collected: TranscriptEntry[] = [];
    try {
      while (nextCursor) {
        const page: Awaited<ReturnType<typeof fetchRunTranscript>> = await fetchRunTranscript(targetRun, nextCursor);
        if (activeRunRef.current !== targetRun) return;
        if (page.data.before === nextCursor) break;
        collected = mergeEntries(page.data.entries, collected);
        nextCursor = page.data.before;
      }
      setEarlier(prev => mergeEntries(collected, prev));
      setCursor(null);
    } catch (err) {
      if (activeRunRef.current !== targetRun) return;
      if (collected.length > 0) setEarlier(prev => mergeEntries(collected, prev));
      setCursor(nextCursor);
      setPageError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      if (activeRunRef.current === targetRun) setLoadingEarlier(false);
    }
  };

  const rows = useMemo(
    () => mergeStream(schedule ?? [], mergedEntries).filter(r => filter === "all" || r.type === filter),
    [schedule, mergedEntries, filter],
  );

  return (
    <section aria-label="Time order">
      <h2>Scheduling and agent, in time order</h2>
      <nav aria-label="Time order filter">
        {(["all", "schedule", "agent"] as Filter[]).map(f => (
          <button key={f} type="button" aria-pressed={filter === f} onClick={() => setFilter(f)}>
            {f === "all" ? "Everything" : f === "schedule" ? "Scheduling only" : "Agent only"}
          </button>
        ))}
      </nav>
      {schedule === undefined ? <p>This server did not send the drain&apos;s decisions for the run.</p> : null}
      {transcriptError ? <RefusalState code={refusalCode(transcriptError)} message={transcriptError.message} /> : null}
      {pageError ? <RefusalState code={refusalCode(pageError)} message={pageError.message} /> : null}
      {effectiveBefore !== null ? (
        <div role="status" className="time-order-pagination">
          <span>
            Showing the newest {agentEntryCount} agent entries; earlier agent entries precede this page.
          </span>{" "}
          {runId ? (
            <>
              <button type="button" disabled={loadingEarlier} onClick={() => void loadOneEarlierPage()}>
                {loadingEarlier ? "Loading earlier entries…" : "Load earlier entries"}
              </button>{" "}
              <button type="button" disabled={loadingEarlier} onClick={() => void loadAllEarlierPages()}>
                Load all earlier entries
              </button>
            </>
          ) : null}
        </div>
      ) : null}
      {entriesPending ? <p>Reading the agent&apos;s transcript…</p> : null}
      {rows.length === 0 && !entriesPending ? <p>Nothing is recorded for this run.</p> : null}
      <ol>
        {rows.map((row, i) =>
          row.type === "schedule" ? (
            <li key={i} data-stream="schedule" data-category={row.entry.category}>
              <time>{row.at === null ? "no clock" : new Date(row.at).toISOString()}</time>{" "}
              <strong>drain · {row.entry.category}</strong> {row.entry.summary}{" "}
              <ScheduleLinks links={row.entry.links} />
            </li>
          ) : (
            <li key={row.entry.id ?? `agent-${i}`} data-stream="agent" data-kind={row.entry.kind}>
              <time>{row.at === null ? "no clock" : new Date(row.at).toISOString()}</time>{" "}
              <strong>
                agent · {row.entry.kind}
                {row.entry.role ? ` (${row.entry.role})` : ""}
              </strong>{" "}
              {row.entry.tool ? `${row.entry.tool.name} ` : ""}
              {(row.entry.text ?? row.entry.tool?.arguments ?? "").slice(0, 240)}
            </li>
          ),
        )}
      </ol>
    </section>
  );
}
