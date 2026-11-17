// The one place the browser talks to the server. It reads `/api/v1/*` and nothing else:
// no client in this tree writes, and no route outside that prefix exists (design §6.4).

import type {
  ApiEnvelope,
  ApiFailure,
  ArtifactData,
  ArtifactPayload,
  DocData,
  DesignsData,
  ExchangesData,
  GateData,
  ItemData,
  LaneRow,
  LandingRow,
  LandingsData,
  PassRow,
  PerformanceCheck,
  PerformanceItem,
  CheckExecution,
  WaitingRow,
  QuestionsData,
  RunData,
  SnapshotData,
  TimelineData,
  TranscriptData,
  TranscriptEntryData,
} from "./types";
import { perf, resourceTiming } from "./perf";

/** An answer the server refused, carrying the cursor it refused at. */
export class ApiError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(code: string, message: string, status: number) {
    super(message);
    this.code = code;
    this.status = status;
  }
}

export const fetchDesigns = (signal?: AbortSignal) => read<DesignsData>("/api/v1/designs", signal);

async function read<T>(path: string, signal?: AbortSignal): Promise<ApiEnvelope<T>> {
  // Every read is timed as the browser waited for it, and reported with the server's request
  // id, so the performance log can put the two side by side (models/perf.ts).
  const started = performance.now();
  const response = await fetch(path, { signal, headers: { Accept: "application/json" } });
  const text = await response.text();
  perf.fetched({
    path, request_id: response.headers.get("X-Request-Id") ?? "", ms: Math.round((performance.now() - started) * 10) / 10,
    bytes: text.length, status: response.status, ...resourceTiming(path),
  });
  const body: unknown = JSON.parse(text);
  if (!response.ok) {
    const failure = body as ApiFailure;
    throw new ApiError(
      failure.error?.code ?? "http_error",
      failure.error?.message ?? `${response.status} ${response.statusText}`,
      response.status,
    );
  }
  return body as ApiEnvelope<T>;
}

export const fetchLanes = (signal?: AbortSignal) => read<LaneRow[]>("/api/v1/lanes", signal);

export const fetchLandings = (signal?: AbortSignal) => read<LandingsData>("/api/v1/landings", signal);
export const fetchLanding = (id: string, signal?: AbortSignal) => read<LandingRow>(`/api/v1/landings/${encodeURIComponent(id)}`, signal);

export const fetchWaiting = async (signal?: AbortSignal) => {
  const answer = await read<{ rows: WaitingRow[] }>("/api/v1/why", signal);
  return { ...answer, data: answer.data.rows };
};
export const fetchPasses = (signal?: AbortSignal) => read<PassRow[]>("/api/v1/passes", signal);

/** The API returns at most 50 rows, with an exclusive `before` cursor. Cover a bounded
 * range completely; for all history, expose older pages on the reader's request. */
export async function fetchPassHistory(since: number | null, pages: number, signal?: AbortSignal): Promise<ApiEnvelope<{ rows: PassRow[]; more: boolean }>> {
  const rows: PassRow[] = [];
  let before: number | null = null;
  for (let index = 0; ; index++) {
    const params = new URLSearchParams();
    if (since !== null) params.set("since", String(since));
    if (before !== null) params.set("before", String(before));
    const response = await read<PassRow[]>(`/api/v1/passes${params.size ? `?${params}` : ""}`, signal);
    rows.push(...response.data);
    const more = response.data.length === 50;
    if (!more || (since === null && index + 1 >= pages)) return { ...response, data: { rows, more } };
    const oldest = Math.min(...response.data.map(row => row.dispatched_at_ms));
    if (before !== null && oldest >= before) throw new Error("Pass history cursor did not advance");
    if (since !== null && oldest <= since) return { ...response, data: { rows, more: false } };
    before = oldest;
  }
}


export const fetchPerformanceChecks = (bounds: string, signal?: AbortSignal) =>
  read<{ checks: PerformanceCheck[] }>(`/api/v1/perf/checks?${bounds}`, signal);
export const fetchPerformanceItems = (bounds: string, signal?: AbortSignal) =>
  read<{ items: PerformanceItem[] }>(`/api/v1/perf/items?${bounds}`, signal);
export const fetchCheck = (id: string, signal?: AbortSignal) =>
  read<{ check_id: string; executions: CheckExecution[] }>(`/api/v1/checks/${encodeURIComponent(id)}`, signal);

/** Exhaust the timestamp cursor; even a short page is not proof the range is exhausted. */
export async function fetchPassRange(since: number | null, end: number, signal?: AbortSignal): Promise<PassRow[]> {
  const rows: PassRow[] = [];
  let before = end;
  while (since === null || before > since) {
    const params = new URLSearchParams({ before: String(before) });
    if (since !== null) params.set("since", String(since));
    const page = await read<PassRow[]>(`/api/v1/passes?${params}`, signal);
    if (page.data.length === 0) break;
    const next = Math.min(...page.data.map(row => row.dispatched_at_ms));
    if (next >= before) throw new Error("Pass cursor did not advance; the range could not be read completely.");
    rows.push(...page.data.filter(row => row.dispatched_at_ms < end && (since === null || row.dispatched_at_ms >= since)));
    before = next;
  }
  return rows;
}

export const fetchSnapshot = (signal?: AbortSignal) =>
  read<SnapshotData>("/api/v1/snapshot", signal);

/** The board's one place for the questions waiting on the owner. The server needs Vantage's
 * index for it (cached while the tree's Markdown is unchanged, but a walk of the whole tree
 * when it is not), so it is fetched when the view is opened rather than on every snapshot:
 * the snapshot must stay cheap. */
export const fetchQuestions = (signal?: AbortSignal) =>
  read<QuestionsData>("/api/v1/questions", signal);

export const fetchItem = (itemId: string, signal?: AbortSignal) =>
  read<ItemData>(`/api/v1/items/${encodeURIComponent(itemId)}`, signal);

/** The item's design document. Its refusals are distinct codes — `no-design-doc`,
 * `design-doc-missing`, `design-doc-outside-root`, `design-doc-unreadable` — so each state
 * the screen draws is the server's own answer rather than a guess from an empty body. The
 * server's whole vocabulary is `apierror.go`'s `apiCodes` table; a client that spells one
 * differently loses that state to its default branch without any error. */
export const fetchItemDoc = (itemId: string, signal?: AbortSignal) =>
  read<DocData>(`/api/v1/items/${encodeURIComponent(itemId)}/doc`, signal);

export const fetchRun = (runId: string, signal?: AbortSignal) =>
  read<RunData>(`/api/v1/runs/${encodeURIComponent(runId)}`, signal);

export const fetchRunExchanges = (runId: string, signal?: AbortSignal) =>
  read<ExchangesData>(`/api/v1/runs/${encodeURIComponent(runId)}/exchanges`, signal);

export const fetchRunTimeline = (runId: string, signal?: AbortSignal) =>
  read<TimelineData>(`/api/v1/runs/${encodeURIComponent(runId)}/timeline`, signal);

/** The gate's per-check spans, composed into the run page's unified axis. The executed checks come
 * from the store's `gate_spans` table and the skipped steps from the run's own
 * `gate.spans.json`. */
export const fetchRunGate = (runId: string, signal?: AbortSignal) =>
  read<GateData>(`/api/v1/runs/${encodeURIComponent(runId)}/gate`, signal);

/** One page of a run's transcript. `before` null asks for the tail, which is the page a
 * reader opens a live run at; the answer's `before` and `cursor` walk back and forward. */
export const fetchRunTranscript = (
  runId: string,
  before: string | null,
  signal?: AbortSignal,
) =>
  read<TranscriptData>(
    `/api/v1/runs/${encodeURIComponent(runId)}/transcript${
      before === null ? "" : `?before=${encodeURIComponent(before)}`
    }`,
    signal,
  );

/** One entry of a run's transcript, answered whole: what a truncated body points at. */
export const fetchRunTranscriptEntry = (runId: string, seq: number, signal?: AbortSignal) =>
  read<TranscriptEntryData>(
    `/api/v1/runs/${encodeURIComponent(runId)}/transcript/${seq}`,
    signal,
  );

export const fetchRunArtifact = (runId: string, kind: string, signal?: AbortSignal) =>
  read<ArtifactData>(
    `/api/v1/runs/${encodeURIComponent(runId)}/artifacts/${encodeURIComponent(kind)}`,
    signal,
  );

export type { ArtifactPayload };
