// The document link's one setting, end to end: the base URL rides on the response cursor
// (serve.go's `vantage_base_url`), the board reads it from the snapshot it already holds,
// and the pane builds the link from it. A test of the pane alone would not catch the
// setting being read from the wrong response — or not wired through at all — which is the
// failure this file exists for.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test } from "vitest";

import { App } from "../src/App";
import type { ApiEnvelope, ItemData } from "../src/models/types";
import {
  cursor,
  dispatchedItemData,
  docData,
  envelope,
  twoItemSnapshotData,
} from "./fixtures";

const originalFetch = globalThis.fetch;
const originalWebSocket = globalThis.WebSocket;

class SilentSocket {
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  close() {}
}

const items: Record<string, ItemData> = { "item-seed-02": dispatchedItemData };

/** stubFetch answers the board with a cursor carrying `baseUrl`, which is the only place
 * the setting is configured. */
function stubFetch(baseUrl: string | undefined) {
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const path = String(input);
    let body: ApiEnvelope<unknown>;
    if (path === "/api/v1/passes") {
      body = envelope([]);
    } else if (path === "/api/v1/snapshot") {
      body = {
        snapshot: baseUrl === undefined ? cursor : { ...cursor, vantage_base_url: baseUrl },
        data: twoItemSnapshotData,
      };
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

/** selectDispatched clicks the queue row an operator clicks: the document section then
 * reads the item's document from the server. */
async function selectDispatched(container: HTMLElement) {
  await waitFor(() => expect(container.textContent).toContain("item-seed-02"));
  await waitFor(() => expect(container.textContent).toContain("docs/design/a-design.md"));
}

beforeEach(() => {
  window.history.replaceState(null, "", "/#/item/item-seed-02");
  globalThis.WebSocket = SilentSocket as unknown as typeof WebSocket;
});

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
  globalThis.WebSocket = originalWebSocket;
});

describe("the document link reads its base URL from the response cursor", () => {
  test("a configured base answers base/#/path", async () => {
    stubFetch("http://vantage.test:1234");
    const { container } = renderApp();
    await selectDispatched(container);
    const link = container.querySelector("a.doc__link");
    expect(link?.getAttribute("href")).toBe(
      "http://vantage.test:1234/#/docs/design/a-design.md",
    );
    expect(container.textContent).not.toContain("goes nowhere");
  });

  test("no configured base draws the inert link and says so", async () => {
    stubFetch(undefined);
    const { container } = renderApp();
    await selectDispatched(container);
    expect(container.querySelector("a.doc__link")).toBeNull();
    expect(container.querySelector(".doc__link--inert")).not.toBeNull();
    expect(container.textContent).toContain(
      "no Vantage base URL is configured, so this link goes nowhere",
    );
  });
});
