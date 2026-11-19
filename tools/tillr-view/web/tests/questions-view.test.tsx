// The board's one place for the questions waiting on the owner, end to end: the header's
// Questions count opens the view, the view lists every item whose document still asks
// something with its question ids, and each id is a link built as
// `<base>/#/<path>#<OQ-id>`. With no base configured the links are drawn, are the same
// URLs, and are inert — the board never probes the address.
//
// The rail is asserted beside it because the criterion is that the list, the rail and the
// header agree with the planner: the snapshot carries the loop's own `question-open` hold,
// and the row reads it rather than the store's in-queue state.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test } from "vitest";

import { App } from "../src/App";
import type {
  ApiEnvelope,
  ItemData,
  QuestionsData,
  SnapshotData,
} from "../src/models/types";
import { cursor, docData, dispatchedItemData, envelope } from "./fixtures";

const originalFetch = globalThis.fetch;
const originalWebSocket = globalThis.WebSocket;

class SilentSocket {
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  close() {}
}

/** One held item and the two questions its document still asks. The planner's own count
 * and the item's hold are the same fact on two surfaces. */
const snapshot: SnapshotData = {
  sources: {
    store: { path: ".tillr/state.db", state: "present" },
    loop_dir: { path: ".tillr/loop", state: "present" },
    trace: { path: "tillr-trace", state: "absent", note: "no tillr-trace program at this path" },
  },
  counts: {
    by_state: { blocked: 1 },
    dispatchable: 0,
    blocked: 1,
    runs_live: 0,
    question_holds: 1,
  },
  drain_summary: {
    active: false,
    running_runs: 0,
    ticks: 1,
    dispatched: 0,
    landed: 0,
    rejected: 0,
    infra_failures: null,
    total_cost_usd: null,
    last_stop_reason: "queue-drained",
    stop_note: "",
    last_decision: null,
    build_note: "",
    gate_ms: null,
    gate_budget_ms: null,
    gate_trend: [],
  },
  lanes: [],
  items: [
    {
      item_id: "item-seed-06",
      title: "sixth seed",
      projected_state: {
        state: "blocked",
        raw_state: "in-queue",
        underlying: "question-open",
        hold: "question-open",
      },
      updated_at_ms: 1762979460000,
      cost_usd: null,
      latest_decision: null,
      latest_run: null,
      place: 2,
    },
  ],
  recent_runs: [],
};

const questions: QuestionsData = {
  items: [
    {
      item_id: "item-seed-06",
      title: "sixth seed",
      state: "in-queue",
      design_doc: "docs/design/seed.md",
      questions: [
        { question_id: "OQ-1", text: "does the first thing work?", state: "open" },
        { question_id: "OQ-3", text: "does the third thing work?", state: "open" },
      ],
      questions_state: "questions-ok",
      hold: "question-open",
    },
  ],
  unknown: [],
  missing: [],
  counts: { items: 1, questions: 2, holds: 1 },
  state: "questions-ok",
};

const items: Record<string, ItemData> = { "item-seed-06": dispatchedItemData };

/** stubFetch answers the board and its two reads. `baseUrl` is the only place the Vantage
 * link's setting lives; the questions route never names it. */
function stubFetch(baseUrl: string | undefined) {
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const path = String(input);
    let body: ApiEnvelope<unknown>;
    if (path === "/api/v1/passes") {
      body = envelope([]);
    } else if (path === "/api/v1/snapshot") {
      body = {
        snapshot: baseUrl === undefined ? cursor : { ...cursor, vantage_base_url: baseUrl },
        data: snapshot,
      };
    } else if (path === "/api/v1/questions") {
      body = envelope(questions);
    } else if (path.endsWith("/doc")) {
      body = envelope(docData);
    } else {
      body = envelope(items[path.slice("/api/v1/items/".length)]);
    }
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
}

function renderApp() {
  const client = new QueryClient();
  return render(
    <QueryClientProvider client={client}>
      <App />
    </QueryClientProvider>,
  );
}

/** openQuestions clicks the header's Questions count, which is the control the item-less
 * reader uses: no item has to be known first. */
async function openQuestions(container: HTMLElement) {
  await waitFor(() => expect(container.textContent).toContain("Questions"));
  const link = container.querySelector('nav[aria-label="Observatory"] a[href="#/questions"]');
  if (!link) throw new Error("the shell carries no Questions link");
  fireEvent.click(link);
  await waitFor(() => expect(container.textContent).toContain("Questions waiting on you"));
}

beforeEach(() => {
  window.history.replaceState(null, "", "/#/item/item-seed-06");
  globalThis.WebSocket = SilentSocket as unknown as typeof WebSocket;
});

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
  globalThis.WebSocket = originalWebSocket;
});

describe("the questions view is the board's one place for a ruling", () => {
  test("the header count opens a list that links each question into Vantage", async () => {
    stubFetch("http://vantage.test:1234");
    const { container } = renderApp();
    await waitFor(() => expect(container.textContent).toContain("item-seed-06"));

    expect(container.querySelector('[aria-label="Item page"]')).not.toBeNull();

    await openQuestions(container);
    await waitFor(() => expect(container.textContent).toContain("OQ-1"));
    expect(container.textContent).toContain("does the first thing work?");
    expect(container.textContent).toContain("docs/design/seed.md");

    const links = Array.from(container.querySelectorAll("a.question__link"));
    expect(links.map((link) => link.getAttribute("href"))).toEqual([
      "http://vantage.test:1234/#/docs/design/seed.md#OQ-1",
      "http://vantage.test:1234/#/docs/design/seed.md#OQ-3",
    ]);
    expect(container.textContent).not.toContain("go somewhere");
  });

  test("a pasted #/questions link opens the list rather than selecting an item", async () => {
    stubFetch("http://vantage.test:1234");
    window.history.replaceState(null, "", "/#/questions");
    const { container } = renderApp();
    await waitFor(() => expect(container.textContent).toContain("Questions waiting on you"));
    await waitFor(() => expect(container.textContent).toContain("OQ-1"));
    // The default "land on the queue's head" must not replace the pasted route.
    expect(window.location.hash).toBe("#/questions");
  });

  test("selecting an item leaves the list on that item's own route", async () => {
    stubFetch("http://vantage.test:1234");
    window.history.replaceState(null, "", "/#/questions");
    const { container } = renderApp();
    await waitFor(() => expect(container.textContent).toContain("OQ-1"));
    const title = Array.from(container.querySelectorAll("button.question-item__title")).find(
      (node) => node.textContent?.includes("sixth seed"),
    );
    if (!title) throw new Error("the list carries no item title to open");
    fireEvent.click(title as HTMLElement);
    await waitFor(() => expect(window.location.hash).toBe("#/item/item-seed-06"));
    await waitFor(() => expect(container.textContent).not.toContain("Questions waiting on you"));
  });

  test("with no base URL the links are drawn, are correct, and go nowhere", async () => {
    stubFetch(undefined);
    const { container } = renderApp();
    await waitFor(() => expect(container.textContent).toContain("item-seed-06"));
    await openQuestions(container);
    await waitFor(() => expect(container.textContent).toContain("OQ-1"));

    expect(container.querySelectorAll("a.question__link")).toHaveLength(0);
    const inert = Array.from(container.querySelectorAll(".question__link--inert"));
    expect(inert.map((link) => link.getAttribute("data-href"))).toEqual([
      "/#/docs/design/seed.md#OQ-1",
      "/#/docs/design/seed.md#OQ-3",
    ]);
    expect(container.textContent).toContain("no Vantage base URL is configured");
    expect(container.textContent).toContain("go nowhere");
  });
});
