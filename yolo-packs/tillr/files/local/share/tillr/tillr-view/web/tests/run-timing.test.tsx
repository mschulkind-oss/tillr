import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { RunPage } from "../src/components/ItemRunPages";
import { DrainScheduleLedger, TimeOrderStream, WallTimeCard, mergeStream, metricText } from "../src/components/RunTiming";
import type { RunData, RunTiming, ScheduleEntry, TranscriptData, TranscriptEntry } from "../src/models/types";
import { envelope, runData, snapshotData, transcriptData } from "./fixtures";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const timing: RunTiming = {
  wall_ms: { ms: 2100000, epistemic_state: "observed" },
  model_ms: { ms: 93000, epistemic_state: "observed" },
  tool_ms: { ms: 2000000, epistemic_state: "observed" },
  slot_wait_ms: { ms: 1980000, epistemic_state: "lower_bound", note: "at least this long" },
  tool_active_ms: { ms: null, epistemic_state: "absent", note: "slot wait is not recorded" },
  uncovered_ms: { ms: 7000, epistemic_state: "derived", note: "wall time outside recorded model and tool intervals" },
};

const schedule = (
  at_ms: number | null,
  kind: string,
  category: ScheduleEntry["category"] = "gate",
  links?: Record<string, string>,
): ScheduleEntry => ({
  at_ms,
  category,
  kind,
  summary: kind,
  source: "events",
  links,
  epistemic_state: at_ms === null ? "no_clock" : "observed",
});

const agent = (
  at_ms: number | null,
  text: string,
  source: TranscriptEntry["source"] = "log",
  id = `${source}:${at_ms ?? "none"}:${text}`,
): TranscriptEntry => ({
  id,
  seq: 0,
  source,
  kind: "message",
  at_ms,
  text,
  bytes: text.length,
  truncated: false,
  epistemic_state: "observed",
});

describe("wall-time card", () => {
  test("labels lower bounds, derived figures, and not-recorded metrics without showing zero", () => {
    expect(metricText(timing.slot_wait_ms)).toBe("≥ 33m 0s");
    expect(metricText(timing.tool_active_ms)).toBe("not recorded");
    expect(metricText(timing.uncovered_ms!)).toBe("~7s");
    render(<WallTimeCard timing={timing} />);
    expect(screen.getByText(/slot wait is not recorded/)).toBeTruthy();
    expect(screen.getByText("LOWER BOUND")).toBeTruthy();
    expect(screen.getByText("DERIVED")).toBeTruthy();
    expect(screen.queryByText(/^0s/)).toBeNull();
  });
});

describe("drain schedule ledger and time-order stream", () => {
  test("orders drain decisions with agent entries, deduplicates steps.jsonl rows, and puts clockless entries last", () => {
    const rows = mergeStream(
      [schedule(3000, "gate.verdict"), schedule(1000, "run.allocate", "allocation"), schedule(null, "landing.unclocked", "landing")],
      [agent(null, "no clock"), agent(2000, "mid"), agent(1000, "step gate started", "steps"), agent(500, "first")],
    );
    expect(rows.map(r => (r.type === "schedule" ? r.entry.kind : r.entry.text))).toEqual([
      "first",
      "run.allocate",
      "mid",
      "gate.verdict",
      "landing.unclocked",
      "no clock",
    ]);
  });

  test("filters between Everything, Scheduling only, and Agent only, and renders EntityLinks", () => {
    render(
      <TimeOrderStream
        schedule={[schedule(1000, "retry", "retry", { item_id: "item-1", next_run_id: "run-2" })]}
        entries={[agent(2000, "hello")]}
        entriesPending={false}
      />,
    );
    expect(screen.getByText(/hello/)).toBeTruthy();
    expect(screen.getByText("run-2")).toBeTruthy();
    fireEvent.click(screen.getByText("Scheduling only"));
    expect(screen.queryByText(/hello/)).toBeNull();
    expect(screen.getByText("run-2")).toBeTruthy();
    fireEvent.click(screen.getByText("Agent only"));
    expect(screen.getByText(/hello/)).toBeTruthy();
    expect(screen.queryByText("run-2")).toBeNull();
  });

  test("announces truncated tail transcripts and loads earlier pages without omitting mid-run entries", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = typeof input === "string" ? input : input instanceof Request ? input.url : String(input);
      if (url.includes("before=cursor-2")) {
        const page2: TranscriptData = {
          ...transcriptData,
          run: "run-big",
          entries: [agent(1500, "middle-agent-entry", "log", "log:2:0")],
          before: "cursor-1",
        };
        return new Response(JSON.stringify(envelope(page2)), { status: 200 });
      }
      const page1: TranscriptData = {
        ...transcriptData,
        run: "run-big",
        entries: [agent(1100, "earliest-agent-entry", "log", "log:1:0")],
        before: null,
      };
      return new Response(JSON.stringify(envelope(page1)), { status: 200 });
    });

    const initialTail: TranscriptData = {
      ...transcriptData,
      run: "run-big",
      entries: [agent(2500, "tail-agent-entry", "log", "log:3:0")],
      before: "cursor-2",
    };

    render(
      <TimeOrderStream
        runId="run-big"
        schedule={[schedule(1000, "run.allocate", "allocation"), schedule(3000, "gate.verdict", "gate")]}
        transcript={initialTail}
      />,
    );

    expect(screen.getByRole("status").textContent).toContain("Showing the newest 1 agent entries; earlier agent entries precede this page.");
    fireEvent.click(screen.getByRole("button", { name: "Load all earlier entries" }));

    await waitFor(() => expect(screen.getByText(/earliest-agent-entry/)).toBeTruthy());
    expect(screen.getByText(/middle-agent-entry/)).toBeTruthy();
    expect(screen.getByText(/tail-agent-entry/)).toBeTruthy();
    expect(screen.queryByRole("status")).toBeNull();
  });

  test("DrainScheduleLedger renders chronological decisions on the default timeline view", () => {
    render(<DrainScheduleLedger schedule={[schedule(1000, "run.allocate", "allocation", { lane_id: "lane-a" })]} />);
    expect(screen.getByText(/run.allocate/)).toBeTruthy();
    expect(screen.getByText("lane-a")).toBeTruthy();
  });

  test("RunPage renders wall-time split, drain schedule ledger, and time-order tab", async () => {
    const timedRun: RunData = {
      ...runData,
      timing,
      schedule: [
        schedule(1762979400500, "run.allocate", "allocation", { lane_id: "lane-1" }),
        schedule(1762979405000, "gate.verdict", "gate", { landing_id: "landing-1" }),
      ],
    };
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = typeof input === "string" ? input : input instanceof Request ? input.url : String(input);
      if (url.includes("/timeline")) {
        return new Response(
          JSON.stringify(
            envelope({
              run: timedRun.run.run_id,
              spans: [
                {
                  span_id: `${timedRun.run.run_id}/slot.wait/0`,
                  parent_span_id: `${timedRun.run.run_id}/run`,
                  kind: "slot_wait",
                  label: "test slot wait: slot-0",
                  started_ms: 1762979401000,
                  finished_ms: 1762979403000,
                  duration_ms: 2000,
                  source: "slot-waits.jsonl",
                  epistemic_state: "observed",
                },
              ],
              sources: snapshotData.sources,
            }),
          ),
          { status: 200 },
        );
      }
      if (url.includes("/gate")) {
        return new Response(
          JSON.stringify(envelope({ run: timedRun.run.run_id, verdict_id: "", plan_span: null, spans: [], skipped_checks: [], sources: snapshotData.sources })),
          { status: 200 },
        );
      }
      if (url.includes("/transcript")) {
        return new Response(JSON.stringify(envelope(transcriptData)), { status: 200 });
      }
      return new Response(JSON.stringify(envelope(timedRun)), { status: 200 });
    });

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <RunPage id={timedRun.run.run_id} route={`/run/${timedRun.run.run_id}`} live={false} paused={false} />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(screen.getByRole("region", { name: "Where wall time went" })).toBeTruthy());
    expect(screen.getByRole("region", { name: "Drain scheduling decisions" })).toBeTruthy();

    rerender(
      <QueryClientProvider client={client}>
        <RunPage id={timedRun.run.run_id} route={`/run/${timedRun.run.run_id}?tab=time-order`} live={false} paused={false} />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(screen.getByRole("region", { name: "Time order" })).toBeTruthy());
    await waitFor(() => expect(screen.getByText(/read the design and land the change/)).toBeTruthy());
    expect(screen.getByText("landing-1")).toBeTruthy();
  });
});
