import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { App } from "../src/App";
import type { AlertDetailData, AlertsData, AlertEpisodeView } from "../src/models/types";
import { blockedItemData, envelope, snapshotData } from "./fixtures";

class SilentSocket {
  close() {}
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

const ep1: AlertEpisodeView = {
  alert_id: "alert-ep1",
  rule_id: "fast-gate-run-slow",
  subject_key: "go-build",
  episode_num: 2,
  state: "open",
  settlement_state: "uninvestigated",
  severity: "high",
  condition_fingerprint: "fp-1",
  opened_seq: 10,
  opened_at: Date.parse("2026-11-26T01:00:00Z"),
  last_observed_seq: 14,
  last_observed_at: Date.parse("2026-11-26T01:05:00Z"),
  observation_count: 3,
  unsettled_for_ms: 840_000,
  variables: { slowest_check: "go-build" },
  variables_json: '{"slowest_check":"go-build"}',
  bundle_json: '{"duration_ms":42000}',
  linked_items: [
    {
      alert_id: "alert-ep1",
      item_id: "item-fix-1",
      item_title: "Speed up go-build",
      item_state: "working",
      relation: "fix-fault",
      linked_at: Date.parse("2026-11-26T01:06:00Z"),
      linked_by: "operator-session",
      event_seq: 15,
      active_silence: false,
    },
  ],
  latest_transition: {
    seq: 15,
    timestamp: Date.parse("2026-11-26T01:06:00Z"),
    state_change: "item-linked",
    kind: "watch.alert.item-linked",
    actor: "operator-session",
    detail: "item-fix-1",
  },
};

const ep2: AlertEpisodeView = {
  alert_id: "alert-ep2",
  rule_id: "package-frequent-red",
  subject_key: "tools/flaky",
  episode_num: 1,
  state: "resolved",
  settlement_state: "settled",
  severity: "warning",
  condition_fingerprint: "fp-2",
  opened_seq: 5,
  opened_at: Date.parse("2026-11-25T20:00:00Z"),
  last_observed_seq: 6,
  last_observed_at: Date.parse("2026-11-25T20:05:00Z"),
  observation_count: 1,
  resolved_seq: 7,
  resolved_at: Date.parse("2026-11-25T20:10:00Z"),
  resolved_reason: "condition-cleared",
  variables: { package: "tools/flaky" },
  variables_json: '{"package":"tools/flaky"}',
  outcome: "fix-rule",
  linked_items: [],
};

function makeAlertsData(episodes: AlertEpisodeView[] = [ep1, ep2], absent = false): AlertsData {
  return {
    absent,
    counts: {
      total: episodes.length,
      active: episodes.filter((e) => e.state !== "resolved").length,
      open: episodes.filter((e) => e.state === "open").length,
      acknowledged: episodes.filter((e) => e.state === "acknowledged").length,
      silenced: episodes.filter((e) => e.state === "silenced").length,
      resolved: episodes.filter((e) => e.state === "resolved").length,
      unsettled: episodes.filter((e) => e.settlement_state !== "settled").length,
      uninvestigated: episodes.filter((e) => e.settlement_state === "uninvestigated").length,
      investigating: episodes.filter((e) => e.settlement_state === "investigating").length,
      investigated: episodes.filter((e) => e.settlement_state === "investigated").length,
      settled: episodes.filter((e) => e.settlement_state === "settled").length,
      active_silences: absent ? 0 : 1,
    },
    episodes,
    active_silences: absent
      ? []
      : [
          {
            silence_id: "sil-1",
            rule_id: "package-frequent-red",
            vars: { package: "tools/flaky" },
            vars_json: '{"package":"tools/flaky"}',
            tracking_item_id: "item-fix-1",
            tracking_item_title: "Speed up go-build",
            tracking_item_state: "working",
            created_by: "operator-session",
            created_at: Date.parse("2026-11-26T00:30:00Z"),
            reason: "known flake",
            investigation_cause: "race in test",
            investigation_evidence: "run-123",
            source_alert_id: "alert-ep2",
            silence_until_ms: Date.parse("2026-11-26T06:00:00Z"),
          },
        ],
  };
}

function makeAlertDetail(ep: AlertEpisodeView = ep1, withTransitions = true): AlertDetailData {
  return {
    absent: false,
    episode: ep,
    transitions: withTransitions
      ? [
          {
            seq: 10,
            timestamp: Date.parse("2026-11-26T01:00:00Z"),
            state_change: "fired",
            kind: "watch.alert.opened",
            actor: "watch-evaluator",
            detail: "threshold exceeded",
          },
          {
            seq: 12,
            timestamp: Date.parse("2026-11-26T01:02:00Z"),
            state_change: "tracked",
            kind: "watch.alert.tracked",
            actor: "not recorded",
          },
        ]
      : [],
    linked_items: ep.linked_items,
    related_episodes: [
      {
        ...ep,
        alert_id: "alert-ep0",
        episode_num: 1,
        state: "resolved",
        settlement_state: "settled",
      },
    ],
  };
}

function mountRoute(
  initialRoute: string,
  options: {
    alertsData?: AlertsData;
    alertDetail?: AlertDetailData;
    linkedAlertsOnItem?: boolean;
  } = {},
) {
  window.history.replaceState(null, "", initialRoute);
  vi.stubGlobal("WebSocket", SilentSocket);
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    switch (url.pathname) {
      case "/api/v1/snapshot":
        return new Response(JSON.stringify(envelope(snapshotData)));
      case "/api/v1/passes":
        return new Response(JSON.stringify(envelope([])));
      case "/api/v1/why":
        return new Response(
          JSON.stringify(
            envelope({
              rows: [],
              summary: { lanes_busy: 0, lanes_offered: 0, running: 0, could_run: 0, held: {} },
            }),
          ),
        );
      case "/api/v1/lanes":
        return new Response(JSON.stringify(envelope([])));
      case "/api/v1/alerts": {
        const base = options.alertsData ?? makeAlertsData();
        const state = url.searchParams.get("state") ?? "";
        const rule = url.searchParams.get("rule") ?? "";
        const severity = url.searchParams.get("severity") ?? "";
        const settlement = url.searchParams.get("settlement") ?? "";
        const filtered = base.episodes.filter(
          (e) =>
            (!state || e.state === state) &&
            (!rule || e.rule_id === rule) &&
            (!severity || e.severity === severity) &&
            (!settlement || e.settlement_state === settlement),
        );
        return new Response(
          JSON.stringify(envelope({ ...base, episodes: filtered })),
        );
      }
      case "/api/v1/alerts/alert-ep1":
        return new Response(
          JSON.stringify(envelope(options.alertDetail ?? makeAlertDetail())),
        );
      case "/api/v1/items/item-fix-1":
      case "/api/v1/items/item-1":
        return new Response(
          JSON.stringify(
            envelope({
              ...blockedItemData,
              item: { ...blockedItemData.item, item_id: url.pathname.split("/").pop()! },
              linked_alerts:
                options.linkedAlertsOnItem === false
                  ? []
                  : [
                      {
                        alert_id: "alert-ep1",
                        rule_id: "fast-gate-run-slow",
                        subject_key: "go-build",
                        state: "open",
                        settlement_state: "uninvestigated",
                        severity: "high",
                        relation: "fix-fault",
                        linked_at: Date.parse("2026-11-26T01:06:00Z"),
                        linked_by: "operator-session",
                        event_seq: 15,
                      },
                    ],
            }),
          ),
        );
      case "/api/v1/items/item-fix-1/doc":
      case "/api/v1/items/item-1/doc":
        return new Response(
          JSON.stringify(
            envelope({
              item_id: url.pathname.split("/")[4],
              path: "docs/design/test.md",
              frontmatter: null,
              anchor: "",
              open_questions: 0,
              questions_state: "questions-ok",
              bytes: 10,
            }),
          ),
        );
      default:
        throw new Error(`Unexpected route: ${input}`);
    }
  });
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={client}>
      <App />
    </QueryClientProvider>,
  );
  return { ...view, fetch, client };
}

describe("Observatory alerts UI (Slice 8)", () => {
  test("Alerts ledger renders episodes, silences, and filters via URL", async () => {
    const { fetch } = mountRoute("/alerts");
    const episodesRegion = await screen.findByRole("region", { name: "Alert episodes" });
    expect(within(episodesRegion).getByText("alert-ep1")).toBeTruthy();
    expect(within(episodesRegion).getByText("alert-ep2")).toBeTruthy();
    expect(within(episodesRegion).getByText("slowest_check=go-build")).toBeTruthy();
    expect(within(episodesRegion).getByText("uninvestigated · unsettled 14m")).toBeTruthy();
    expect(screen.getByText("sil-1")).toBeTruthy();

    fireEvent.change(screen.getByLabelText("State filter"), { target: { value: "open" } });
    await waitFor(() => expect(window.location.search).toBe("?state=open"));
    await waitFor(() => expect(within(episodesRegion).queryByText("alert-ep2")).toBeNull());
    expect(within(episodesRegion).getByText("alert-ep1")).toBeTruthy();
    expect(
      fetch.mock.calls.some(([u]) => String(u) === "/api/v1/alerts?state=open"),
    ).toBe(true);

    fireEvent.change(screen.getByLabelText("State filter"), { target: { value: "" } });
    fireEvent.change(screen.getByLabelText("Severity filter"), { target: { value: "warning" } });
    await waitFor(() => expect(window.location.search).toBe("?severity=warning"));
    await waitFor(() => expect(within(episodesRegion).queryByText("alert-ep1")).toBeNull());
    expect(within(episodesRegion).getByText("alert-ep2")).toBeTruthy();
    expect(
      fetch.mock.calls.some(([u]) => String(u) === "/api/v1/alerts?severity=warning"),
    ).toBe(true);
  });

  test("Alerts ledger handles absent alert table without error", async () => {
    mountRoute("/alerts", { alertsData: makeAlertsData([], true) });
    await screen.findByText("No watch alert table in this store yet.");
  });

  test("AlertPage shows transition history with not recorded actor, bundle, related episodes, and navigates to ItemPage", async () => {
    mountRoute("/alert/alert-ep1");
    await screen.findByText("threshold exceeded");
    expect(screen.getByText("watch-evaluator")).toBeTruthy();
    expect(screen.getByText("not recorded")).toBeTruthy();
    expect(screen.getByText(/"duration_ms": 42000/)).toBeTruthy();
    expect(screen.getByText("alert-ep0")).toBeTruthy();

    // Navigate from AlertPage linked item to ItemPage
    const itemLink = screen.getByRole("link", { name: /item-fix-1/ });
    fireEvent.click(itemLink);
    await screen.findByRole("region", { name: "Linked alerts" });
    expect(window.location.pathname).toBe("/item/item-fix-1");
    expect(screen.getByRole("link", { name: "alert-ep1" })).toBeTruthy();

    // Navigate back from ItemPage linked alert to AlertPage
    fireEvent.click(screen.getByRole("link", { name: "alert-ep1" }));
    await waitFor(() => expect(window.location.pathname).toBe("/alert/alert-ep1"));
  });

  test("AlertPage shows explicit message when no transition events are recorded", async () => {
    mountRoute("/alert/alert-ep1", { alertDetail: makeAlertDetail(ep1, false) });
    await screen.findByText(/No transition events are recorded for this episode/);
  });

  test("Home promotes Watch alerts card when a high-severity or unsettled alert is active", async () => {
    const { container } = mountRoute("/");
    await screen.findByText("alert-ep1");
    const region = screen.getByRole("region", { name: "Watch alerts" });
    expect(region.getAttribute("data-promoted")).toBe("true");
    expect(container.querySelector(".home__columns")?.previousElementSibling).toBe(region);
  });

  test("Home places Watch alerts card in sidebar when no high/unsettled/owner-escalated alert is active", async () => {
    mountRoute("/", { alertsData: makeAlertsData([ep2]) });
    await screen.findByText("No active or unsettled watch alerts.");
    const region = screen.getByRole("region", { name: "Watch alerts" });
    expect(region.getAttribute("data-promoted")).toBe("false");
    expect(region.closest("aside")).not.toBeNull();
  });

  test("ItemPage shows explicit empty state when no alerts are linked", async () => {
    mountRoute("/item/item-1", { linkedAlertsOnItem: false });
    await screen.findByText("No alerts linked to this item.");
  });
});
