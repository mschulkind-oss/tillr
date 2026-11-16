// The Gate Check Waterfall (design §5.3's provenance rule, item `item-gate-check-spans`):
// the verdict records one opaque `gate:python3` check, and the per-check spans are what
// turn it into bars a reader can act on. Three contracts are pinned here:
//
//   1. the plan span is the axis, and a check's bar is positioned inside it;
//   2. a span with no recorded clock draws no bar and says so — `TimelineWaterfall`'s
//      null-clock refusal, inherited rather than reimplemented;
//   3. the steps the gate skipped are named with their reasons and never drawn as bars.

import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, test } from "vitest";

import { SpanViewer } from "../src/components/SpanViewer";
import { assembleSpans, windowOf, waitedTotal } from "../src/models/spans";
import { runData } from "./fixtures";
import type { GateData, GateSpan } from "../src/models/types";

afterEach(cleanup);

const sources: GateData["sources"] = {
  store: { path: ".tillr/state.db", state: "present" },
  loop_dir: { path: ".tillr/loop", state: "present" },
  trace_index: { path: "index.db", state: "absent" },
};

function span(overrides: Partial<GateSpan> & Pick<GateSpan, "span_id" | "check_id" | "label">): GateSpan {
  return {
    trace_id: "trace-1",
    parent_span_id: "parent-1",
    kind: "gate",
    status: "ok",
    start_unix_nano: "1700000000000000000",
    end_unix_nano: "1700000000175820000",
    started_at_ms: 1_700_000_000_000,
    finished_at_ms: 1_700_000_175_820,
    waited_ms_json: '{"pool": 0, "cheap": 0, "exclusive": 0, "heavy": 0, "writes": 0, "after": 0}',
    attributes_json: "{}",
    epistemic_state: "observed",
    ...overrides,
  };
}

function gate(): GateData {
  return {
    run: "run-1",
    verdict_id: "verdict-1",
    plan_span: span({
      span_id: "span-plan",
      check_id: "plan",
      kind: "plan",
      label: "gate plan",
      status: "verified",
      started_at_ms: 1_700_000_000_000,
      finished_at_ms: 1_700_000_178_057,
    }),
    spans: [
      span({
        span_id: "span-gate",
        check_id: "gate/python3",
        label: "gate:python3",
        status: "ok",
        started_at_ms: 1_700_000_001_000,
        finished_at_ms: 1_700_000_176_820,
        waited_ms_json: '{"pool": 12.5, "cheap": 0, "exclusive": 0, "heavy": 0, "writes": 0, "after": 0}',
      }),
      span({
        span_id: "span-guard",
        check_id: "scratch/no-tracked-markdown-cites-a-scratch-path",
        kind: "scratch",
        label: "no tracked Markdown cites a scratch path as evidence",
        started_at_ms: null,
        finished_at_ms: null,
        waited_ms_json: "{}",
      }),
    ],
    skipped_checks: [
      {
        check_id: "documents/uvx-vantage-check",
        kind: "documents",
        label: "documents (uvx vantage-check)",
        reason: "no tracked Markdown changed in the selected range",
        planned: false,
        owed: false,
      },
    ],
    summary: { checks: 3, executed: 2, skipped: 1, outcome: "verified" },
    sources,
  };
}

describe("GateWaterfall", () => {
  test("the plan span is the axis and a check's bar sits inside it", () => {
    const data = gate();
    const window = windowOf([data.plan_span!, ...data.spans]);
    expect(window).not.toBeNull();
    expect(window?.startMS).toBe(1_700_000_000_000);
    expect(window?.endMS).toBe(1_700_000_178_057);
    const { container } = render(<SpanViewer spans={assembleSpans(runData, undefined, data)} />);
    const rows = [...container.querySelectorAll('[data-span-id="span-gate"], [data-span-id="span-guard"]')];
    expect(rows).toHaveLength(2);
    // The measured check is drawn; the guard with no clock is refused.
    expect(rows[0].querySelector(".waterfall__bar")).not.toBeNull();
    expect(rows[1].querySelector(".waterfall__bar")).toBeNull();
    expect(rows[1].textContent).toContain("no clock recorded");
  });

  test("a skipped step is named with its reason and never drawn", () => {
    const { container } = render(<SpanViewer spans={assembleSpans(runData, undefined, gate())} />);
    expect(container.textContent).toContain("no tracked Markdown changed in the selected range");
    // The skip is not a bar: two rows are the executed checks, and the skip list is a
    // separate details block.
    expect(container.querySelector('[data-span-id*="/skip/"] .waterfall__bar')).toBeNull();
  });

  test("an absent store span set says so rather than drawing an empty table", () => {
    const empty: GateData = {
      ...gate(),
      plan_span: null,
      spans: [],
      skipped_checks: [],
    };
    const { container } = render(<SpanViewer spans={assembleSpans(runData, undefined, empty)} />);
    expect(container.textContent).toContain("no row for this stage");
    expect(container.querySelector('[data-span-id$="/gate"] .waterfall__bar')).toBeNull();
  });


});

describe("waitedTotal", () => {
  test("sums the scheduler's disjoint reasons and answers null for no measurement", () => {
    expect(waitedTotal(span({ span_id: "a", check_id: "a", label: "a", waited_ms_json: '{"pool": 1.5, "cheap": 2.5}' }))).toBe(4);
    expect(waitedTotal(span({ span_id: "b", check_id: "b", label: "b", waited_ms_json: "{}" }))).toBeNull();
  });
});
