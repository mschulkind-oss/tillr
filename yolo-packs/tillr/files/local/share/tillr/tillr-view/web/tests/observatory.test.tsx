// The observatory's honesty suite. Each case drives one component against a partial
// fixture and asserts that a missing value renders as a named gap, never as a plausible
// value: no zero clock, no green on an unverified claim, no empty payload for an absent
// record. The case list below is the suite's own registry -- a case that is not in it
// does not run.

import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, test } from "vitest";

import { ClaimAuditBanner } from "../src/components/ClaimAuditBanner";

import { InterToolExchangeView } from "../src/components/InterToolExchangeView";
import { ItemWithoutRun } from "../src/components/ItemWithoutRun";
import { ObservatoryHeaderBar } from "../src/components/ObservatoryHeaderBar";
import { PayloadInspectorDrawer } from "../src/components/PayloadInspectorDrawer";
import { SpanViewer } from "../src/components/SpanViewer";
import { assembleSpans, barStyle, windowOf } from "../src/models/spans";
import { TranscriptView } from "../src/components/TranscriptView";
import { EMPTY_FILTER } from "../src/models/board";
import {
  blockedItemData,
  dispatchedItemData,
  envelope,
  exchangesData,
  runData,
  snapshotData,
  timelineData,
  transcriptData,
  transcriptSeamData,
  transcriptTailData,
  transcriptTruncatedData,
} from "./fixtures";
import {
  routeFromSelection,
  searchFromSelection,
  selectionFromRoute,
  selectionFromSearch,
  DEFAULT_SELECTION,
} from "../src/models/url";
import type { ArtifactData } from "../src/models/types";

afterEach(cleanup);

/** The header's narrowing props in the state most cases want: nothing narrowed, so every
 * count is the whole board's and the one line that reports a narrowing is absent. */
const noFilter = {
  filter: EMPTY_FILTER,
  shown: snapshotData.items.length,
  onToggleState: () => {},
  onClearFilter: () => {},
};

interface Case {
  name: string;
  run: () => void | Promise<void>;
}

const cases: Case[] = [];

function addCase(name: string, run: () => void | Promise<void>) {
  cases.push({ name, run });
}

addCase("a span with no clock draws no bar and says so", () => {
  const window = windowOf(timelineData.spans);
  const gap = timelineData.spans[2];
  expect(gap.epistemic_state).toBe("unobserved_seam");
  expect(barStyle(gap, window)).toBeNull();

  const { container } = render(
    <SpanViewer spans={assembleSpans(runData, timelineData)} />,
  );
  const gapRow = container.querySelector('[data-span-id$="/gap"]');
  expect(gapRow).not.toBeNull();
  expect(gapRow?.textContent).toContain("no clock recorded — not drawn");
  expect(gapRow?.querySelector(".waterfall__bar")).toBeNull();
  expect(gapRow?.textContent).toContain("UNOBSERVED SEAM");
});

addCase("every timeline span renders its epistemic state", () => {
  const { container } = render(
    <SpanViewer spans={assembleSpans(runData, timelineData)} />,
  );
  const states = Array.from(container.querySelectorAll("[data-epistemic-state]")).map((node) =>
    node.getAttribute("data-epistemic-state"),
  );
  expect(states).toContain("observed");
  expect(states).toContain("derived_clock");
  expect(states).toContain("unobserved_seam");
  const derived = container.querySelector('[data-epistemic-state="derived_clock"]');
  expect(derived?.textContent).toBe("DERIVED CLOCK");
});

addCase("an unverified claim is amber with its reason, never verified", () => {
  const { container } = render(<ClaimAuditBanner run={runData} />);
  const unverified = container.querySelector(".claim--unverified");
  expect(unverified).not.toBeNull();
  expect(unverified?.textContent).toContain("tests_passed");
  expect(unverified?.textContent).toContain("UNVERIFIED");
  expect(unverified?.textContent).toContain("UNOBSERVED SEAM");
  expect(unverified?.textContent).toContain("proc-exec.jsonl missing");
  expect(unverified?.className).not.toContain("claim--verified");
});

addCase("a stage with no clock reads no clock, and a missing stage is absent", () => {
  const { container } = render(
    <SpanViewer spans={assembleSpans(runData)} />,
  );
  const cells = Array.from(container.querySelectorAll(".span-row"));
  expect(cells).toHaveLength(7);
  const runCell = cells.find((cell) => cell.getAttribute("data-span-id")?.endsWith("/run"));
  expect(runCell?.querySelector(".waterfall__bar")?.getAttribute("title")).toContain("24800.00 ms");
  for (const name of ["fence", "gate", "land", "verdict"]) {
    const cell = cells.find((node) => node.getAttribute("data-span-id")?.endsWith(`/${name}`));
    expect(cell?.textContent).toContain("no clock");
    expect(cell?.textContent).not.toContain("0.00s");
    expect(cell?.querySelector('[data-epistemic-state="absent"]')).not.toBeNull();
  }
});

addCase("an absent artifact names the paths searched instead of an empty body", () => {
  const artifact: ArtifactData = {
    run: "run-item-seed-02",
    artifact: {
      kind: "brief",
      present: false,
      epistemic_state: "absent",
      note: "no writer produced this file at this path",
      searched_paths: [
        ".tillr/loop/run-item-seed-02/brief.md",
        "lanes/lane-1/brief.md",
      ],
    },
  };
  const { container } = render(
    <PayloadInspectorDrawer
      exchange={null}
      artifact={artifact}
      artifactKind="brief"
      artifactError={null}
      onClose={() => {}}
    />,
  );
  expect(container.textContent).toContain("no writer produced this file at this path");
  expect(container.textContent).toContain(".tillr/loop/run-item-seed-02/brief.md");
  expect(container.querySelector(".inspector__body")).toBeNull();
});

// The owner's question on 2026-11-14, twice in one session: "am I looking at the current
// build?" A board that cannot answer it is a board you cannot trust a queue order to, and
// the case that prompted this drew 55 items in item-id order with nothing on screen saying
// the server predated the `rank` field.
addCase("the header names the build that answered, and an unstamped server says so", () => {
  const stamped = envelope(snapshotData);
  stamped.snapshot = {
    ...stamped.snapshot,
    build: {
      revision: "f0a53cd7ce6d9999",
      time: "2026-11-14T05:56:34Z",
      modified: false,
      front_end: "embedded",
      executable: "/workspace/.yolo/durable/tillr-view-observatory",
    },
  };
  const { container } = render(
    <ObservatoryHeaderBar
      snapshot={stamped}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live={true}
      {...noFilter}
    />,
  );
  expect(container.textContent).toContain("build f0a53cd7ce6d");
  expect(container.textContent).toContain("embedded");
  const line = container.querySelector(".head-build");
  expect(line).not.toBeNull();
  expect(line?.getAttribute("title")).toContain("/workspace/.yolo/durable/tillr-view-observatory");

  // A server too old to carry the field: the sentence must be usable, so it is not
  // ellipsised into the word "build" the way the first spelling of this line was.
  const unstamped = envelope(snapshotData);
  delete (unstamped.snapshot as { build?: unknown }).build;
  const old = render(
    <ObservatoryHeaderBar
      snapshot={unstamped}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live={true}
      {...noFilter}
    />,
  );
  expect(old.container.textContent).toContain("build unknown");
  expect(old.container.textContent).toContain("cannot vouch");
  expect(old.container.querySelector(".head-build--unknown")).not.toBeNull();
  old.unmount();
});

// The drain's build is a different question from the server's build: a `tillr-view`
// restarted this morning can be reading last night's loop result, so the header names both.
// A loop result written before the stamp existed carries no build, and that absence is
// drawn as a build that cannot be named rather than as an empty stamp a reader would take
// for current.
addCase("the header names the drain build, and an unstamped result says the drain cannot be named", () => {
  const stamped = envelope(snapshotData);
  const { container } = render(
    <ObservatoryHeaderBar
      snapshot={stamped}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live={true}
      {...noFilter}
    />,
  );
  expect(container.textContent).toContain("drain build 4a79a8e7f383");
  expect(container.textContent).toContain("digest 0123456789ab");

  const unstamped = envelope({
    ...snapshotData,
    drain_summary: {
      ...snapshotData.drain_summary,
      build: null,
      build_note:
        "the loop result names no build, so the board cannot vouch for the drain that wrote it",
    },
  });
  const old = render(
    <ObservatoryHeaderBar
      snapshot={unstamped}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live={true}
      {...noFilter}
    />,
  );
  expect(old.container.textContent).toContain("drain build unknown");
  expect(old.container.textContent).toContain("cannot vouch");
  old.unmount();
});

// The gate's own wall time: the header shows the last tick's number and the trend, so "the
// gate got slower" is a number with a date. It is the gate's number and not the tick's, and
// a tick that overran its budget is drawn as an overrun rather than as one more bar.
addCase("the header shows the gate's wall time and its trend, and an overrun is named", () => {
  const { container } = render(
    <ObservatoryHeaderBar
      snapshot={envelope(snapshotData)}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live={true}
      {...noFilter}
    />,
  );
  expect(container.textContent).toContain("Gate: 82.4s of a 330.0s budget");
  expect(container.textContent).toContain("2 ticks · worst 82.4s");
  expect(container.querySelectorAll(".head-gate__bar").length).toBe(2);

  // An overrunning tick is drawn as one, and the number says by how much.
  const over = envelope({
    ...snapshotData,
    drain_summary: {
      ...snapshotData.drain_summary,
      gate_ms: 400000,
      gate_trend: [
        { tick_id: "tick-3", at_ms: 1762979520000, gate_ms: 400000, budget_ms: 330000, over_budget: true },
      ],
    },
  });
  const drawn = render(
    <ObservatoryHeaderBar
      snapshot={over}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live={true}
      {...noFilter}
    />,
  );
  expect(drawn.container.textContent).toContain("over by 70.0s");
  expect(drawn.container.textContent).toContain("1 over budget");
  expect(drawn.container.querySelector(".head-gate__bar--over")).not.toBeNull();
  drawn.unmount();

  // A store with no measurement is a named absence, never a zero-length bar: zero would
  // read as a fast gate, which is the one thing this number exists to disprove.
  const unmeasured = envelope({
    ...snapshotData,
    drain_summary: {
      ...snapshotData.drain_summary,
      gate_ms: null,
      gate_budget_ms: null,
      gate_trend: [],
    },
  });
  const none = render(
    <ObservatoryHeaderBar
      snapshot={unmeasured}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live={true}
      {...noFilter}
    />,
  );
  expect(none.container.textContent).toContain("no tick has measured the gate yet");
  expect(none.container.querySelectorAll(".head-gate__bar").length).toBe(0);
});

addCase("a cost the store did not report is not rendered as zero", () => {
  const { container } = render(
    <ObservatoryHeaderBar
      snapshot={envelope(snapshotData)}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live={true}
      {...noFilter}
    />,
  );
  expect(container.textContent).not.toContain("Cost:");
  expect(container.textContent).not.toContain("$0.0000");
  expect(container.textContent).toContain("Refresh Snapshot");
  expect(container.textContent).toContain("seq #48");
});

// A screen the live channel is not moving has to say so, in the header, before the
// operator reads a stale number as a current one: the same store read twice looks
// identical whether or not anything is pushing it.
addCase("a screen with no live channel says it is polling", () => {
  const polling = render(
    <ObservatoryHeaderBar
      snapshot={envelope(snapshotData)}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live={false}
      {...noFilter}
    />,
  );
  expect(polling.container.textContent).toContain("live channel down");
  expect(polling.container.textContent).toContain("re-reading every 5s");
  expect(polling.container.textContent).not.toContain("pushed by the live channel");
  polling.unmount();

  const live = render(
    <ObservatoryHeaderBar
      snapshot={envelope(snapshotData)}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live={true}
      {...noFilter}
    />,
  );
  expect(live.container.textContent).toContain("pushed by the live channel");
  expect(live.container.textContent).not.toContain("live channel down");
});

// The refusal is the state a stale screen hides in, so the transport line is rendered
// there too rather than only with a snapshot to show.
addCase("a refused store still says which transport is moving it", () => {
  const { container } = render(
    <ObservatoryHeaderBar
      snapshot={undefined}
      error={new Error("the store is locked")}
      onRefresh={() => {}}
      refreshing={false}
      live={false}
      {...noFilter}
    />,
  );
  expect(container.textContent).toContain("The store could not be read.");
  expect(container.textContent).toContain("live channel down");
});

addCase("the seam row of the exchange table is a visible row, not an omission", () => {
  const { container } = render(
    <InterToolExchangeView
      runId="run-item-seed-02"
      exchanges={exchangesData}
      selectedExchange={null}
      onSelectExchange={() => {}}
    />,
  );
  const rows = Array.from(container.querySelectorAll(".exchange-row"));
  expect(rows).toHaveLength(2);
  const seam = rows.find((row) => row.textContent?.includes("bash -> subprocess"));
  expect(seam?.textContent).toContain("UNOBSERVED SEAM");
  expect(seam?.textContent).toContain("no clock");
});

// Every blocked item has no run, so the exchanges and timeline reads are disabled and
// never answer. A disabled read and a slow one are different facts, and the pane that
// means "nothing to read here" must not be the one that means "still reading".
addCase("an item with no run says it has no exchanges instead of reading forever", () => {
  const { container } = render(
    <InterToolExchangeView
      runId={null}
      exchanges={undefined}
      selectedExchange={null}
      onSelectExchange={() => {}}
    />,
  );
  expect(container.textContent).not.toContain("Reading the exchanges…");
  expect(container.textContent).toContain("No run is selected");
});



// The other half: a read that really is in flight still says so, so the no-run state is
// not bought by dropping the loading one.
addCase("a selected run whose exchanges are still being read says it is reading", () => {
  const { container } = render(
    <InterToolExchangeView
      runId="run-item-seed-02"
      exchanges={undefined}
      selectedExchange={null}
      onSelectExchange={() => {}}
    />,
  );
  expect(container.textContent).toContain("Reading the exchanges…");
});



// What the item does have, since it has no run: the state, what holds it and the criteria
// its verdict will be judged against.
addCase("an item with no run shows its state, what blocks it and what a run would bring", () => {
  const { container } = render(<ItemWithoutRun item={blockedItemData} hasRun={false} />);
  expect(container.textContent).toContain("No run for item-seed-06");
  expect(container.querySelector(".state-badge")?.textContent).toBe("blocked");
  expect(container.textContent).toContain("item:item-seed-02: finish the prerequisite");
  expect(container.textContent).toContain("waiting on item:item-seed-02");
  expect(container.textContent).toContain("raw: blocked");
  // A run that does not exist yet is explained, never drawn as a pipeline that is still
  // loading: the item says when a run will exist rather than leaving an empty box.
  expect(container.textContent).toContain("No lane has picked it up yet");
});

// An item the store does have a run for, with no run named in the URL, is a selection the
// operator has not made — not an item that has no run.
addCase("an item the store has a run for says the run is not selected", () => {
  const { container } = render(<ItemWithoutRun item={dispatchedItemData} hasRun={true} />);
  expect(container.textContent).toContain("No run selected for item-seed-02");
  expect(container.textContent).not.toContain("No run for item-seed-02");
});


addCase("a tool call renders the arguments it passed and what came back", () => {
  const { container } = render(
    <TranscriptView runId="run-item-seed-02" transcript={transcriptData} error={null} />,
  );
  const blocks = Array.from(container.querySelectorAll(".transcript-entry"));
  // The call and its result are one block: the stream records them as two messages, and the
  // pane draws the pair as the call it was.
  const call = blocks.find((block) => block.textContent?.includes("bash"));
  expect(call).toBeDefined();
  expect(call?.textContent).toContain('"ls tools/tillr-view"');
  expect(call?.textContent).toContain("serve.go transcript.go");
  // The shim's subprocess rows are reported as a source and not listed as entries: a shell
  // command is not a message, and the pane says where they went.
  expect(container.textContent).toContain("proc: present");
  expect(container.textContent).toContain("not listed");
  expect(container.querySelector(".transcript-proc")).toBeNull();
  // The reasoning is its own entry, and its own block, not folded into a message.
  const thinking = container.querySelector(".transcript-entry--thinking");
  expect(thinking?.textContent).toContain("one commit");
});

addCase("an entry with no clock says so, and an observed one shows its own", () => {
  const { container } = render(
    <TranscriptView runId="run-item-seed-02" transcript={transcriptTruncatedData} error={null} />,
  );
  expect(container.textContent).toContain("no clock");
  // A body the page truncated says how many bytes the record holds, and offers them.
  expect(container.textContent).toContain("400000 bytes");
  expect(container.querySelector(".btn--tiny")).not.toBeNull();
  // The derived clock is labelled rather than drawn as a recorded one.
  const derived = render(
    <TranscriptView runId="run-item-seed-02" transcript={transcriptData} error={null} />,
  );
  expect(derived.container.textContent).toContain("the agent finished its turn");
  expect(derived.container.textContent).toContain("DERIVED CLOCK");
});

addCase("a transcript that is a seam says what is missing instead of showing nothing", () => {
  const { container } = render(
    <TranscriptView runId="run-item-seed-02" transcript={transcriptSeamData} error={null} />,
  );
  expect(container.textContent).toContain("UNOBSERVED SEAM");
  expect(container.textContent).toContain("the agent's own stream is absent");
  expect(container.textContent).toContain("a seam, not an empty run");
  // The records that survive are still read: the seam is named, not substituted for the run.
  expect(container.querySelectorAll(".transcript-notice").length).toBeGreaterThan(0);
  expect(container.textContent).toContain("log: absent");
});

addCase("a tail page offers the server's cursor and no earlier page invents one", () => {
  const tail = render(
    <TranscriptView runId="run-item-seed-02" transcript={transcriptTailData} error={null} />,
  );
  expect(tail.container.textContent).toContain("load earlier entries");
  tail.unmount();
  const head = render(
    <TranscriptView runId="run-item-seed-02" transcript={transcriptData} error={null} />,
  );
  expect(head.container.textContent).not.toContain("load earlier entries");
});

addCase("a run with no selection or a refused read is a state, not an empty list", () => {
  const none = render(<TranscriptView runId={null} transcript={undefined} error={null} />);
  expect(none.container.textContent).toContain("No run is selected");
  none.unmount();
  const refused = render(
    <TranscriptView
      runId="run-item-seed-02"
      transcript={undefined}
      error={new Error("the store's schema is newer")}
    />,
  );
  expect(refused.container.textContent).toContain("the store's schema is newer");
  expect(refused.container.textContent).toContain("UNREADABLE");
});

addCase("the URL round-trips every selection and defaults the tab", () => {
  const selection = {
    item: "item-seed-02",
    run: "run-item-seed-02",
    tab: "timeline" as const,
    exchange: "run-item-seed-02/seam/proc",
    artifact: "gate.verdict",
  };
  const search = searchFromSelection(selection);
  expect(search).toBe(
    "?item=item-seed-02&run=run-item-seed-02&tab=timeline&exchange=run-item-seed-02%2Fseam%2Fproc&artifact=gate.verdict",
  );
  expect(selectionFromSearch(search)).toEqual(selection);
  expect(searchFromSelection(DEFAULT_SELECTION)).toBe("");
  expect(selectionFromSearch("")).toEqual(DEFAULT_SELECTION);
  // The item is the route, so a reload lands on the same item and everything else rides
  // behind it as parameters.
  expect(routeFromSelection(selection)).toBe(
    "/item/item-seed-02?run=run-item-seed-02&tab=timeline&exchange=run-item-seed-02%2Fseam%2Fproc&artifact=gate.verdict",
  );
  expect(selectionFromRoute(routeFromSelection(selection))).toEqual(selection);
  expect(selectionFromRoute("/item/item-seed-02")).toEqual({
    ...DEFAULT_SELECTION,
    item: "item-seed-02",
  });
  expect(selectionFromRoute("")).toEqual(DEFAULT_SELECTION);
  expect(routeFromSelection(DEFAULT_SELECTION)).toBe("/");
});

describe("the observatory renders gaps as gaps", () => {
  for (const testCase of cases) {
    test(testCase.name, async () => {
      await testCase.run();
    });
  }
});

// A server that outlived the tree it was built from says so: the comparison is the server's
// own (build.go checkoutFreshness), and "behind" is the one state that asks for a restart.
test("the build line says when the server is behind the checkout it serves", () => {
  const behind = envelope(snapshotData);
  behind.snapshot.build = {
    revision: "f0a53cd7ce6d0000000000000000000000000000", time: "", modified: false,
    front_end: "embedded", executable: "/x/tillr-view",
    checkout: { state: "behind", head: "abc", note: "the checkout's tillr sources moved since this server was built" },
  };
  const { container } = render(
    <ObservatoryHeaderBar snapshot={behind} error={null} onRefresh={() => {}} refreshing={false} live={true} {...noFilter} />,
  );
  const flag = container.querySelector(".head-build__behind");
  expect(flag?.textContent).toContain("behind the checkout");
  expect(flag?.getAttribute("title")).toContain("moved since this server was built");
  cleanup();
  const current = envelope(snapshotData);
  current.snapshot.build = { ...behind.snapshot.build, checkout: { state: "current", note: "" } };
  const now = render(
    <ObservatoryHeaderBar snapshot={current} error={null} onRefresh={() => {}} refreshing={false} live={true} {...noFilter} />,
  );
  expect(now.container.querySelector(".head-build__behind")).toBeNull();
});
