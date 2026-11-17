// What the owner's browser experienced, reported to the server's performance log
// (tools/tillr-view perflog.go, `POST /api/v1/client-timing`). It measures four things and
// nothing else: each page view's time to its first data and to settled, every API fetch as the
// browser waited for it, long main-thread tasks where the browser reports them, and slow
// renders of a screen. Nothing leaves the machine: the report goes to the board's own origin.
//
// The clock is injectable (`PerfRecorder`'s `now`), so a test drives a page view with
// recorded times instead of waiting.

export const CLIENT_TIMING_PATH = "/api/v1/client-timing";

/** How often a non-empty batch is sent while the page is open. */
export const FLUSH_EVERY_MS = 10_000;
/** A render slower than this is reported. One frame is ~16 ms; 50 ms is a stutter. */
export const SLOW_RENDER_MS = 50;
/** The most fetches one batch carries; past it they are counted, not kept. */
export const MAX_FETCHES = 500;

export interface PageView {
  page: string;
  nav: "load" | "hash";
  started_ms: number;
  first_data_ms: number | null;
  settled_ms: number | null;
  requests: number;
}

export interface FetchTiming {
  path: string;
  request_id: string;
  ms: number;
  ttfb_ms: number | null;
  bytes: number;
  transfer_bytes: number | null;
  status: number;
  page: string;
}

export interface Duration {
  id: string;
  ms: number;
  page: string;
}

export interface ClientBatch {
  ts: string;
  session: string;
  reason: string;
  page_views: PageView[];
  fetches: FetchTiming[];
  long_tasks: Duration[];
  renders: Duration[];
  dropped_fetches: number;
}

/** The route a hash names, with ids folded so one run is not its own page. */
export function pageOf(hash: string): string {
  const path = hash.split("?")[0] || "#/";
  const parts = path.split("/");
  if (parts.length > 2 && ["item", "run", "design", "check", "landing"].includes(parts[1])) return `#/${parts[1]}/{id}`;
  return path === "#" ? "#/" : path;
}

export interface PerfRecorderOptions {
  now?: () => number;
  send?: (batch: ClientBatch, beacon: boolean) => void;
  wallClock?: () => string;
}

export class PerfRecorder {
  readonly session: string;
  private readonly now: () => number;
  private readonly send: (batch: ClientBatch, beacon: boolean) => void;
  private readonly wallClock: () => string;
  private current: PageView | null = null;
  private views: PageView[] = [];
  private fetches: FetchTiming[] = [];
  private longTasks: Duration[] = [];
  private renders: Duration[] = [];
  private droppedFetches = 0;

  constructor(options: PerfRecorderOptions = {}) {
    this.now = options.now ?? (() => performance.now());
    this.send = options.send ?? sendBatch;
    this.wallClock = options.wallClock ?? (() => new Date().toISOString());
    this.session = Math.random().toString(36).slice(2, 10);
  }

  /** A page view begins: the document's load (at navigation start, 0) or a hash change. */
  beginPage(hash: string, nav: "load" | "hash"): void {
    this.finishPage();
    this.current = { page: pageOf(hash), nav, started_ms: nav === "load" ? 0 : this.now(), first_data_ms: null, settled_ms: null, requests: 0 };
  }

  /** The current view's first data reached the screen. */
  firstData(): void {
    const view = this.current;
    if (view && view.first_data_ms === null) view.first_data_ms = round(this.now() - view.started_ms);
  }

  /** Every read the current view started has answered. */
  settled(): void {
    const view = this.current;
    if (view && view.first_data_ms !== null && view.settled_ms === null) {
      view.settled_ms = round(this.now() - view.started_ms);
      this.finishPage();
    }
  }

  private finishPage(): void {
    if (this.current) this.views.push(this.current);
    this.current = null;
  }

  get page(): string {
    return this.current?.page ?? "";
  }

  fetched(timing: Omit<FetchTiming, "page">): void {
    if (this.current) this.current.requests += 1;
    if (this.fetches.length >= MAX_FETCHES) {
      this.droppedFetches += 1;
      return;
    }
    this.fetches.push({ ...timing, page: this.page });
  }

  longTask(ms: number): void {
    this.longTasks.push({ id: "longtask", ms: round(ms), page: this.page });
  }

  rendered(id: string, ms: number): void {
    if (ms >= SLOW_RENDER_MS) this.renders.push({ id, ms: round(ms), page: this.page });
  }

  /** Send what has accumulated. A page view still waiting for its data stays open. */
  flush(reason: string, beacon = false): void {
    if (beacon) this.finishPage();
    if (!this.views.length && !this.fetches.length && !this.longTasks.length && !this.renders.length) return;
    const batch: ClientBatch = {
      ts: this.wallClock(), session: this.session, reason,
      page_views: this.views, fetches: this.fetches, long_tasks: this.longTasks, renders: this.renders,
      dropped_fetches: this.droppedFetches,
    };
    this.views = []; this.fetches = []; this.longTasks = []; this.renders = []; this.droppedFetches = 0;
    this.send(batch, beacon);
  }
}

function round(ms: number): number {
  return Math.round(ms * 10) / 10;
}

function sendBatch(batch: ClientBatch, beacon: boolean): void {
  const body = JSON.stringify(batch);
  if (beacon && typeof navigator !== "undefined" && typeof navigator.sendBeacon === "function") {
    navigator.sendBeacon(CLIENT_TIMING_PATH, new Blob([body], { type: "application/json" }));
    return;
  }
  if (typeof fetch === "undefined") return;
  void fetch(CLIENT_TIMING_PATH, { method: "POST", body, keepalive: true, headers: { "Content-Type": "application/json" } }).catch(() => undefined);
}

/** The one recorder for this page. Tests make their own. */
export const perf = new PerfRecorder();

/** The browser's Resource Timing for one URL, read after its body: time to first byte and the
 * bytes that crossed the wire, which a compressed answer makes smaller than the body. */
export function resourceTiming(url: string): { ttfb_ms: number | null; transfer_bytes: number | null } {
  if (typeof performance === "undefined" || typeof performance.getEntriesByName !== "function") return { ttfb_ms: null, transfer_bytes: null };
  const absolute = typeof location === "undefined" ? url : new URL(url, location.href).href;
  const entries = performance.getEntriesByName(absolute, "resource") as PerformanceResourceTiming[];
  const entry = entries[entries.length - 1];
  if (!entry) return { ttfb_ms: null, transfer_bytes: null };
  // Resource Timing keeps a bounded buffer; the observatory reads one entry per fetch, so
  // clearing keeps the buffer from filling and silently ending the measurements.
  if (entries.length > 50 && typeof performance.clearResourceTimings === "function") performance.clearResourceTimings();
  return {
    ttfb_ms: entry.responseStart > 0 ? round(entry.responseStart - entry.startTime) : null,
    transfer_bytes: entry.transferSize > 0 ? entry.transferSize : null,
  };
}

/** Starts the recorder's timers: a flush every FLUSH_EVERY_MS, a beacon when the page is
 * hidden or left, and the long-task observer where the browser has one. */
export function startPerf(recorder: PerfRecorder = perf): () => void {
  if (typeof window === "undefined") return () => undefined;
  const timer = window.setInterval(() => recorder.flush("interval"), FLUSH_EVERY_MS);
  const hidden = () => { if (document.visibilityState === "hidden") recorder.flush("hidden", true); };
  const leave = () => recorder.flush("pagehide", true);
  document.addEventListener("visibilitychange", hidden);
  window.addEventListener("pagehide", leave);
  let observer: PerformanceObserver | null = null;
  try {
    if (typeof PerformanceObserver !== "undefined" && PerformanceObserver.supportedEntryTypes?.includes("longtask")) {
      observer = new PerformanceObserver(list => { for (const entry of list.getEntries()) recorder.longTask(entry.duration); });
      observer.observe({ type: "longtask", buffered: true });
    }
  } catch {
    observer = null;
  }
  return () => {
    window.clearInterval(timer);
    document.removeEventListener("visibilitychange", hidden);
    window.removeEventListener("pagehide", leave);
    observer?.disconnect();
  };
}
