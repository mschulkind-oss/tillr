// The normalized read cache. Every key is derived from an entity id, so the manual
// refresh and OBS-5's future socket invalidation target the same keys and no component
// changes when the transport does (design §5.2).

import { useQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";

import {
  fetchItem,
  fetchDesigns,
  fetchItemDoc,
  fetchQuestions,
  fetchRun,
  fetchRunArtifact,
  fetchRunExchanges,
  fetchRunGate,
  fetchRunTimeline,
  fetchRunTranscript,
  fetchSnapshot,
  fetchLanes,
  fetchPasses,
  fetchPassHistory,
  fetchWaiting,
} from "./api";
import type {
  ApiEnvelope,
  ArtifactData,
  DocData,
  DesignsData,
  ExchangesData,
  GateData,
  ItemData,
  QuestionsData,
  RunData,
  TimelineData,
  TranscriptData,
} from "./types";

/** How often a screen with no live channel re-reads. Slow on purpose: it is the fallback
 * under a socket that went away, not the transport, and the point is that a reader can tell
 * it apart from a live screen by the header rather than by watching a clock. */
export const FALLBACK_POLL_MS = 5_000;

/** Every cache key the observatory reads. One spelling per entity, so a refresh and a
 * future invalidation frame name the same entry.
 *
 * `run` is a leaf and the three below it are its children: a run's own payload, and the
 * subtree a run-wide invalidation still reaches because the key is a prefix — which is
 * what `runSubtree` names on its own. The split is what lets a log that grew
 * (`runlog:<id>`, the payload only) cost one read instead of four. */
export const queryKeys = {
  lanes: (): readonly unknown[] => ["lanes"],
  snapshot: (): readonly unknown[] => ["snapshot"],
  passes: (): readonly unknown[] => ["passes"],
  waiting: (): readonly unknown[] => ["waiting"],
  landings: (): readonly unknown[] => ["landings"],
  landing: (id: string): readonly unknown[] => ["landings", id],
  /** The board's question list, read on its own route: the index behind it walks the tree,
   * so it is not part of a snapshot that the live channel refreshes per event. */
  questions: (): readonly unknown[] => ["questions"],
  item: (itemId: string): readonly unknown[] => ["items", itemId],
  run: (runId: string): readonly unknown[] => ["runs", runId, "run"],
  /** The transcript is a child of the run's payload key, not a sibling: a log that grew
   * invalidates `runlog:<id>`, which is exactly this key, so the live channel refreshes the
   * transcript without naming a second resource — and without reaching the exchanges,
   * timeline or artifacts a log line did not change. */
  runTranscript: (runId: string): readonly unknown[] => ["runs", runId, "run", "transcript"],
  runSubtree: (runId: string): readonly unknown[] => ["runs", runId],
  runExchanges: (runId: string): readonly unknown[] => ["runs", runId, "exchanges"],
  runTimeline: (runId: string): readonly unknown[] => ["runs", runId, "timeline"],
  runGate: (runId: string): readonly unknown[] => ["runs", runId, "gate"],
  runArtifact: (runId: string, kind: string): readonly unknown[] => [
    "runs",
    runId,
    "artifacts",
    kind,
  ],
  itemDoc: (itemId: string): readonly unknown[] => ["items", itemId, "doc"],
};

/** A query that reads one API response. The envelope is kept, so every panel can show the
 * cursor it was read at rather than looking freshly correct.
 *
 * `live` is the socket's answer: while it is down nothing pushes a change, so the screen
 * re-reads on a slow timer instead of waiting for a frame that is not coming. A screen
 * that looks live and is not is the one failure an observatory may not have.
 *
 * `paused` is the operator's answer, and it outranks both: the pause control stops every
 * automatic update, so a paused screen does not poll either — the clock it shows is the age
 * of the read it is showing, and a timer quietly refreshing behind that sentence would make
 * the sentence a lie. An explicit refresh still reads; that is the operator asking, not a
 * tick (design §3.3). */
function useRead<T>(
  key: readonly unknown[],
  read: (signal?: AbortSignal) => Promise<ApiEnvelope<T>>,
  enabled: boolean,
  live: boolean,
  paused = false,
  keepPrevious = false,
): UseQueryResult<ApiEnvelope<T>, Error> {
  return useQuery<ApiEnvelope<T>, Error>({
    queryKey: key,
    placeholderData: keepPrevious ? previous => previous : undefined,
    queryFn: ({ signal }) => read(signal),
    enabled,
    staleTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: paused || live ? false : FALLBACK_POLL_MS,
  });
}

// Poll even with a live socket: finished step files and quiet ages are not store events.
export const useLanesQuery = (enabled: boolean, paused = false, laneOpen = false, historyLane?: string) =>
  useQuery({queryKey: historyLane ? [...queryKeys.lanes(), historyLane] : queryKeys.lanes(), queryFn: ({signal}) => fetchLanes(signal, historyLane), enabled,
    retry: false, refetchOnWindowFocus: false, refetchInterval: paused ? false : laneOpen ? 1000 : FALLBACK_POLL_MS});

export const usePassesQuery = (enabled: boolean, paused = false) =>
  useRead(queryKeys.passes(), fetchPasses, enabled, false, paused);
export const usePassHistoryQuery = (range: string, pages: number, enabled: boolean, paused = false) =>
  useRead([...queryKeys.passes(), "history", range, pages], signal => {
    const duration = { "24h": 86400000, "7d": 604800000, "30d": 2592000000 }[range] ?? 86400000;
    return fetchPassHistory(range === "all" ? null : Math.max(0, Date.now() - duration), pages, signal);
  }, enabled, false, paused, true);
export const useWaitingQuery = (enabled: boolean, paused = false) =>
  useRead(queryKeys.waiting(), fetchWaiting, enabled, false, paused);

export const useSnapshotQuery = (live: boolean, paused = false) =>
  useRead(queryKeys.snapshot(), fetchSnapshot, true, live, paused);

/** How often an open question list re-reads. Slower than the board on purpose: the index
 * walks every planning document, and the list is read when the operator opens it. */
export const QUESTIONS_POLL_MS = 60_000;

/** The question list. `enabled` is the view being open: a board whose reader is looking at
 * the queue does not pay for a tree walk they did not ask for. `paused` is the same
 * operator's answer every other read below takes: a paused screen does not poll, whichever
 * view it is on. */
export const useQuestionsQuery = (enabled: boolean, paused = false) =>
  useQuery<ApiEnvelope<QuestionsData>, Error>({
    queryKey: queryKeys.questions(),
    queryFn: ({ signal }) => fetchQuestions(signal),
    enabled,
    staleTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: enabled && !paused ? QUESTIONS_POLL_MS : false,
  });

// Designer expiry is a clock change, so this read polls even with a live socket.
export const useDesignsQuery = (enabled: boolean, paused = false) => useRead<DesignsData>(["designs"], signal => fetchDesigns(signal), enabled, false, paused);

export const useItemQuery = (itemId: string | null, live: boolean, paused = false) =>
  useRead<ItemData>(
    queryKeys.item(itemId ?? ""),
    (signal) => fetchItem(itemId as string, signal),
    itemId !== null,
    live,
    paused,
  );

/** The item's design document, read from the server as Markdown. It is needed once per
 * selection and never polled: the file changes when a person edits it, not on a tick. */
export const useItemDocQuery = (
  itemId: string | null,
  live: boolean,
  paused = false,
) =>
  useRead<DocData>(
    queryKeys.itemDoc(itemId ?? ""),
    (signal) => fetchItemDoc(itemId as string, signal),
    itemId !== null,
    live,
    paused,
  );

export const useRunQuery = (runId: string | null, live: boolean, paused = false) =>
  useRead<RunData>(
    queryKeys.run(runId ?? ""),
    (signal) => fetchRun(runId as string, signal),
    runId !== null,
    live,
    paused,
  );

export const useRunExchangesQuery = (
  runId: string | null,
  live: boolean,
  enabled = true,
  paused = false,
) =>
  useRead<ExchangesData>(
    queryKeys.runExchanges(runId ?? ""),
    (signal) => fetchRunExchanges(runId as string, signal),
    runId !== null && enabled,
    live,
    paused,
  );

/** The tail of a run's transcript. It carries a page, not the run's log, and its `before`
 * cursor is what pages back through the rest. */
export const useRunTranscriptQuery = (
  runId: string | null,
  _live: boolean,
  enabled = true,
  paused = false,
) => {
  const client = useQueryClient();
  return useQuery<ApiEnvelope<TranscriptData>, Error>({
    queryKey: queryKeys.runTranscript(runId ?? ""),
    queryFn: async ({ signal }) => {
      const previous = client.getQueryData<ApiEnvelope<TranscriptData>>(queryKeys.runTranscript(runId ?? ""));
      let page = await fetchRunTranscript(runId as string, null, signal, previous?.data.cursor);
      const entries = new Map((previous?.data.entries ?? []).map(entry => [entry.id ?? `${entry.source}:${entry.seq}`, entry]));
      for (const entry of page.data.entries) entries.set(entry.id ?? `${entry.source}:${entry.seq}`, entry);
      while (page.data.has_more && previous) {
        const cursor = page.data.cursor;
        page = await fetchRunTranscript(runId as string, null, signal, cursor);
        if (page.data.has_more && page.data.cursor === cursor) throw new Error("Transcript cursor did not advance.");
        for (const entry of page.data.entries) entries.set(entry.id ?? `${entry.source}:${entry.seq}`, entry);
      }
      return {...page, data: {...page.data, before: previous ? previous.data.before : page.data.before, entries: [...entries.values()]}};
    },
    enabled: runId !== null && enabled,
    staleTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
    // File growth needs no store event. Poll only an open transcript, including
    // when the socket is unavailable; Pause still stops every automatic read.
    refetchInterval: paused ? false : 1000,
  });
};

export const useRunTimelineQuery = (
  runId: string | null,
  live: boolean,
  enabled = true,
  paused = false,
) =>
  useRead<TimelineData>(
    queryKeys.runTimeline(runId ?? ""),
    (signal) => fetchRunTimeline(runId as string, signal),
    runId !== null && enabled,
    live,
    paused,
  );

/** The gate's per-check spans, enabled by the run page: the store's own
 * `gate_spans` rows plus the run's `gate.spans.json`. */
export const useRunGateQuery = (
  runId: string | null,
  live: boolean,
  enabled = true,
  paused = false,
) =>
  useRead<GateData>(
    queryKeys.runGate(runId ?? ""),
    (signal) => fetchRunGate(runId as string, signal),
    runId !== null && enabled,
    live,
    paused,
  );

/** One artifact of one run. `kind` is the server's closed set: brief, fence, base_sha,
 * request, gate.change, gate.plan, gate.verdict, land.candidate, land.attempt, turn,
 * tool, item.verdict, gate.log, diff. */
export const useRunArtifactQuery = (
  runId: string | null,
  kind: string | null,
  live: boolean,
  paused = false,
) =>
  useRead<ArtifactData>(
    queryKeys.runArtifact(runId ?? "", kind ?? ""),
    (signal) => fetchRunArtifact(runId as string, kind as string, signal),
    runId !== null && kind !== null,
    live,
    paused,
  );

/** `[Refresh Snapshot]`: re-read every cached entry from the server. v1 refreshes by
 * hand; OBS-5's socket calls exactly this invalidation instead of a new code path. */
export function useRefreshAll(): { refresh: () => void; refreshing: boolean } {
  const client = useQueryClient();
  return {
    refresh: () => {
      void client.invalidateQueries();
    },
    refreshing: client.isFetching() > 0,
  };
}
