// The refusal contract (design §5.2, ledger `D-OBS13`): one component renders every refusal
// the face can produce. A code the pane knows keeps the pane's own sentence; a code it does
// not know renders as *the face refused: `<code>`* with the server's message, never as an
// empty panel. The three panes — the rail, the item pane and the evidence pane — are driven
// with the same unknown code, because a shared component that two of three panes skip is two
// renderers and an empty pane.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test } from "vitest";

import { App } from "../src/App";
import { DocumentPane } from "../src/components/DocumentPane";
import { TranscriptView } from "../src/components/TranscriptView";
import { InspectPane } from "../src/components/InspectPane";
import { ItemDetailPane } from "../src/components/ItemDetailPane";
import { QueueRail } from "../src/components/QueueRail";
import { ApiError } from "../src/models/api";
import { EMPTY_FILTER } from "../src/models/board";
import { transcriptTailData } from "./fixtures";

const originalFetch = globalThis.fetch;
const originalWebSocket = globalThis.WebSocket;

/** A socket that never opens: the store is refusing, so nothing will be pushed anyway. */
class SilentSocket {
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  close() {}
}

beforeEach(() => {
  window.history.replaceState(null, "", "/item/item-untouched");
  globalThis.WebSocket = SilentSocket as unknown as typeof WebSocket;
});

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
  globalThis.WebSocket = originalWebSocket;
});

/** The code no pane in this tree has a sentence for, so it takes the unknown branch. */
const UNKNOWN = "a-code-no-pane-knows";
const MESSAGE = "the server said this, and the pane may not paraphrase it";

function unknown(): ApiError {
  return new ApiError(UNKNOWN, MESSAGE, 500);
}

function renderRail(code: ApiError) {
  return render(
    <QueueRail
      items={undefined}
      total={0}
      selectedItem={null}
      onSelectItem={() => {}}
      filter={EMPTY_FILTER}
      onFilterChange={() => {}}
      onClearFilter={() => {}}
      hidden={0}
      filterRef={{ current: null }}
      storeAbsent={false}
      refusal={{ code: code.code, message: code.message }}
      onRetry={() => {}}
    />,
  );
}

function renderItemPane(code: ApiError) {
  return render(
    <ItemDetailPane
      itemId="item-untouched"
      item={undefined}
      itemError={code}
      loading={false}
      onRetry={() => {}}
      latestRun={null}
      updatedAtMs={null}
      selectedRunId={null}
      onSelectRun={() => {}}
      doc={{ doc: undefined, error: null, pending: false, onRetry: () => {}, baseUrl: "" }}
    />,
  );
}

function renderEvidencePane(code: ApiError) {
  return render(
    <InspectPane
      itemId="item-untouched"
      item={undefined}
      latestRun={null}
      onSelectRun={() => {}}
      runId="run-untouched"
      run={undefined}
      runError={code}
      onRetryRun={() => {}}
      tab="transcript"
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
}

describe("an unknown code is a refusal on every pane", () => {
  const panes: Array<[string, (code: ApiError) => { container: HTMLElement }]> = [
    ["the rail", renderRail],
    ["the item pane", renderItemPane],
    ["the evidence pane", renderEvidencePane],
  ];

  for (const [name, pane] of panes) {
    test(`${name} names the code and the server's message`, () => {
      const { container } = pane(unknown());
      expect(container.textContent).toContain(`the face refused: ${UNKNOWN}`);
      expect(container.textContent).toContain(UNKNOWN);
      expect(container.textContent).toContain(MESSAGE);
      // Not an empty pane: something with the refusal's own role is on screen.
      const refusal = container.querySelector(`[data-refusal="${UNKNOWN}"]`);
      expect(refusal).not.toBeNull();
      expect(refusal?.textContent?.trim()).not.toBe("");
    });
  }
});

describe("the board's own read", () => {
  test("a refused store is a refusal in the rail's place, not an empty board", async () => {
    globalThis.fetch = (async () =>
      new Response(
        JSON.stringify({ error: { code: "store-unreadable", message: "the store is locked" } }),
        { status: 503, headers: { "Content-Type": "application/json" } },
      )) as typeof fetch;
    const client = new QueryClient();
    const { container } = render(
      <QueryClientProvider client={client}>
        <App />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(container.textContent).toContain("the store is locked"));
    expect(container.textContent).toContain("store-unreadable");
    expect(container.querySelector('[data-refusal="store-unreadable"]')).not.toBeNull();
  });
});

describe("a page the transcript route refused", () => {
  test("names the code of the page it could not read", async () => {
    globalThis.fetch = (async () =>
      new Response(
        JSON.stringify({ error: { code: "entry-not-found", message: "no such page" } }),
        { status: 404, headers: { "Content-Type": "application/json" } },
      )) as typeof fetch;
    const { container, getByRole } = render(
      <TranscriptView runId="run-item-seed-02" transcript={transcriptTailData} error={null} />,
    );
    fireEvent.click(getByRole("button", { name: /load earlier entries/i }));
    await waitFor(() => expect(container.textContent).toContain("entry-not-found"));
    expect(container.textContent).toContain("no such page");
    expect(container.querySelector('[data-refusal="entry-not-found"]')).not.toBeNull();
  });
});

describe("the document pane keeps its four sentences through the same component", () => {
  const cases: Array<[string, string]> = [
    ["no-design-doc", "This item names no design document"],
    ["design-doc-missing", "The design document this item names is not there"],
    ["design-doc-outside-root", "The design document is outside the root this face serves"],
    ["design-doc-unreadable", "The design document could not be read"],
  ];

  for (const [code, title] of cases) {
    test(`${code} keeps its own sentence and still shows the code`, () => {
      const { container } = render(
        <DocumentPane
          doc={undefined}
          error={new ApiError(code, "the file the item names", 500)}
          pending={false}
          onRetry={() => {}}
          baseUrl=""
        />,
      );
      expect(container.textContent).toContain(title);
      expect(container.textContent).toContain(code);
      expect(container.textContent).toContain("the file the item names");
      expect(container.textContent).not.toContain("the face refused");
    });
  }

  test("an unknown code degrades to the refusal, not to a generic document error", () => {
    const { container } = render(
      <DocumentPane doc={undefined} error={unknown()} pending={false} onRetry={() => {}} baseUrl="" />,
    );
    expect(container.textContent).toContain(`the face refused: ${UNKNOWN}`);
    expect(container.textContent).toContain(MESSAGE);
  });
});
