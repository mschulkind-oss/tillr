import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { FinishedBand } from "../src/components/FinishedBand";
import { InspectPane } from "../src/components/InspectPane";
import { ItemDetailPane } from "../src/components/ItemDetailPane";
import { ItemTimeline, RunPage } from "../src/components/ItemRunPages";
import { ItemsView, PassesView } from "../src/components/ItemsPasses";
import { LaneDetail } from "../src/components/LanePage";
import { NowBand } from "../src/components/NowBand";
import { liveRuns } from "../src/models/now";
import type { ItemData, LaneRow, PassRow, RunData, SnapshotData } from "../src/models/types";
import { dispatchedItemData, envelope, liveSnapshotData, runData, snapshotData } from "./fixtures";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function mockRunFetch(run: RunData) {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    const url = typeof input === "string" ? input : input instanceof Request ? input.url : String(input);
    if (url.includes("/timeline")) {
      return new Response(JSON.stringify(envelope({ run: run.run.run_id, spans: [], sources: snapshotData.sources })), { status: 200 });
    }
    if (url.includes("/gate")) {
      return new Response(JSON.stringify(envelope({ run: run.run.run_id, verdict_id: "", plan_span: null, spans: [], skipped_checks: [], sources: snapshotData.sources })), { status: 200 });
    }
    return new Response(JSON.stringify(envelope(run)), { status: 200 });
  });
}

describe("run seat display and pre-change compatibility", () => {
  test("RunPage displays seat, workflow, and node when present and handles pre-change runs", async () => {
    const recordedRun: RunData = {
      ...runData,
      run: { ...runData.run, seat: "edit", workflow: "standard@v1", node: "work/piece-view" },
    };
    mockRunFetch(recordedRun);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { unmount } = render(
      <QueryClientProvider client={client}>
        <RunPage id={recordedRun.run.run_id} route={`/run/${recordedRun.run.run_id}`} live={false} paused={false} />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(screen.getByText(/seat edit/)).toBeTruthy());
    expect(screen.getByText(/workflow standard@v1/)).toBeTruthy();
    expect(screen.getByText(/node work\/piece-view/)).toBeTruthy();
    unmount();

    mockRunFetch(runData);
    const preChangeClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={preChangeClient}>
        <RunPage id={runData.run.run_id} route={`/run/${runData.run.run_id}`} live={false} paused={false} />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(screen.getByText(/exit not recorded/)).toBeTruthy());
    expect(screen.queryByText(/seat /)).toBeNull();
  });

  test("InspectPane shows run seat when present and omits it cleanly when absent", () => {
    const withSeat: RunData = { ...runData, run: { ...runData.run, seat: "build" } };
    const { container, rerender } = render(
      <InspectPane
        itemId={dispatchedItemData.item.item_id}
        item={dispatchedItemData}
        latestRun={snapshotData.items[0].latest_run}
        onSelectRun={() => {}}
        runId={withSeat.run.run_id}
        run={withSeat}
        tab="timeline"
        onSelectTab={() => {}}
        exchanges={undefined}
        timeline={undefined}
        transcript={undefined}
        transcriptError={null}
        exchangeCount={0}
        spanCount={0}
        selectedExchange={null}
        onSelectExchange={() => {}}
        onOpenArtifact={() => {}}
        exchange={null}
        artifact={undefined}
        artifactKind={null}
        artifactError={null}
        onCloseInspector={() => {}}
      />,
    );
    expect(container.querySelector(".run-summary")?.textContent).toContain("seat build");

    rerender(
      <InspectPane
        itemId={dispatchedItemData.item.item_id}
        item={dispatchedItemData}
        latestRun={snapshotData.items[0].latest_run}
        onSelectRun={() => {}}
        runId={runData.run.run_id}
        run={runData}
        tab="timeline"
        onSelectTab={() => {}}
        exchanges={undefined}
        timeline={undefined}
        transcript={undefined}
        transcriptError={null}
        exchangeCount={0}
        spanCount={0}
        selectedExchange={null}
        onSelectExchange={() => {}}
        onOpenArtifact={() => {}}
        exchange={null}
        artifact={undefined}
        artifactKind={null}
        artifactError={null}
        onCloseInspector={() => {}}
      />,
    );
    expect(container.querySelector(".run-summary")?.textContent).not.toContain("seat ");
  });

  test("ItemDetailPane and ItemTimeline display run seat when recorded and handle pre-change runs", () => {
    const itemWithSeatRuns: ItemData = {
      ...dispatchedItemData,
      runs: [
        { run_id: "run-new", lane_id: "lane-1", backend: "pi", seat: "design", workflow: "standard@v1", node: "design", state: "finished", exit_code: 0, started_at_ms: 2000, finished_at_ms: 2500, end_cause: "normal-exit", usage_tokens: 100, cost_usd: 0.1 },
        { run_id: "run-old", lane_id: "lane-1", backend: "pi", state: "finished", exit_code: 0, started_at_ms: 1000, finished_at_ms: 1500, end_cause: "normal-exit", usage_tokens: 50, cost_usd: 0.05 },
      ],
    };
    const { container } = render(<ItemTimeline item={itemWithSeatRuns} />);
    const history = within(container).getByRole("region", { name: "Item history" });
    const items = within(history).getAllByRole("listitem");
    expect(items[0].textContent).not.toContain("seat ");
    expect(items[1].textContent).toContain("seat design");

    const latestWithSeat = { ...snapshotData.items[0].latest_run!, seat: "verify" };
    const detail = render(
      <ItemDetailPane
        itemId={itemWithSeatRuns.item.item_id}
        item={itemWithSeatRuns}
        itemError={null}
        loading={false}
        onRetry={() => {}}
        latestRun={latestWithSeat}
        updatedAtMs={1762979400000}
        selectedRunId={null}
        onSelectRun={() => {}}
        doc={{ doc: undefined, error: null, pending: false, onRetry: () => {}, baseUrl: "" }}
      />,
    );
    expect(detail.container.textContent).toContain("seat verify");
  });

  test("LaneDetail shows Seat column in Lane history table and seat on lane nodes", () => {
    const laneWithHistoryAndNodes: LaneRow = {
      lane_id: "lane-1",
      state: "running",
      run_id: "run-a",
      item_id: dispatchedItemData.item.item_id,
      backend: "pi",
      seat: "build",
      seat_source: "run",
      attempt: 1,
      outcome: "",
      state_since_ms: 1000,
      last_step: "",
      last_step_at_ms: null,
      log_grew_at_ms: 1000,
      latest_action: { kind: "action", text: "running", at_ms: 1000, clock: "observed" },
      nodes: [
        { node_id: "work/piece-a", run_id: "run-a", attempt: 1, seat: "edit", state: "running" },
        { node_id: "work/piece-legacy", run_id: "run-legacy", attempt: 1, state: "passed" },
      ],
      history: [
        { run_id: "run-a", item_id: "item-1", lane_id: "lane-1", seat: "edit", state: "running", started_at_ms: 2000, finished_at_ms: 0 },
        { run_id: "run-old", item_id: "item-0", lane_id: "lane-1", state: "exited", started_at_ms: 1000, finished_at_ms: 1500 },
      ],
    };
    const { container } = render(<LaneDetail lane={laneWithHistoryAndNodes} item={dispatchedItemData} now={9000} />);
    const graphList = container.querySelector(".lane-graph");
    expect(graphList?.textContent).toContain("work/piece-a · edit · running");
    expect(graphList?.textContent).toContain("work/piece-legacy · passed");
    expect(container.textContent).toContain("seat edit");

    const table = within(container).getByRole("table");
    const headers = within(table).getAllByRole("columnheader").map((h) => h.textContent);
    expect(headers).toContain("Seat");
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows[0].textContent).toContain("edit");
    expect(rows[1].textContent).toContain("—");
  });

  test("NowBand and FinishedBand show run seat when present and handle pre-change snapshots", () => {
    const liveWithSeat: SnapshotData = {
      ...liveSnapshotData,
      items: liveSnapshotData.items.map((item) =>
        item.latest_run ? { ...item, latest_run: { ...item.latest_run, seat: "tricky" } } : item,
      ),
    };
    expect(liveRuns(liveWithSeat)[0].seat).toBe("tricky");
    const { container, rerender } = render(<NowBand snapshot={liveWithSeat} now={1762979465000} />);
    expect(container.querySelector(".now-band__seat")?.textContent).toBe("seat tricky");

    rerender(<NowBand snapshot={liveSnapshotData} now={1762979465000} />);
    expect(container.querySelector(".now-band__seat")).toBeNull();

    const finishedWithSeat = [
      {
        ...snapshotData.items[0],
        projected_state: { state: "landed", raw_state: "landed" },
        latest_run: { ...snapshotData.items[0].latest_run!, seat: "review" },
      },
      {
        ...snapshotData.items[0],
        item_id: "item-legacy",
        title: "legacy finished",
        projected_state: { state: "landed", raw_state: "landed" },
      },
    ];
    const finished = render(
      <FinishedBand items={finishedWithSeat} now={1762979465000} onSelectItem={() => {}} onOpenArchive={() => {}} />,
    );
    expect(finished.container.querySelector(".finished-band__seat")?.textContent).toBe("seat review");
    expect(finished.container.textContent).toContain("legacy finished");
  });

  test("ItemsView and PassesView show seat when present and handle pre-change entries", () => {
    const snapWithSeat: SnapshotData = {
      ...snapshotData,
      items: [
        {
          ...snapshotData.items[0],
          projected_state: { state: "working", raw_state: "in-progress" },
          latest_run: { ...snapshotData.items[0].latest_run!, seat: "build" },
        },
      ],
    };
    const itemsView = render(
      <ItemsView
        snapshot={snapWithSeat}
        waiting={[
          { item_id: "item-seed-02", place: 1, class: "needs you", where: "lane-1", why: "waiting", mover: "owner", since: "1m", class_source: "state" },
        ]}
      />,
    );
    expect(itemsView.container.textContent).toContain("run-item-seed-02 · build");

    const passWithSeat: PassRow = {
      dispatched_at_ms: Date.now() - 1000,
      decisions: [
        { item_id: "item-a", place: 1, action: "dispatch", reason: "ready", lane_id: "lane-1", run_id: "run-1", seat: "design" },
        { item_id: "item-b", place: 2, action: "hold", reason: "blocked" },
      ],
    };
    const passesView = render(<PassesView passes={[passWithSeat]} />);
    expect(passesView.container.textContent).toContain("run-1 · design");
  });
});
