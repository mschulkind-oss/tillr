// The header's lane pill (design §4.4, ledger `D-OBS12`): a stopped board says its lanes
// are idle instead of wearing a `working` glyph over no live run. The pill is always
// drawn, so the answer to "is a lane working" is present at zero as well as at two — and
// the NowBand stays absent at zero, because a band with nothing in it is the
// absence-drawn-as-a-value the truthfulness table forbids.

import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, test } from "vitest";

import { ObservatoryHeaderBar } from "../src/components/ObservatoryHeaderBar";
import { NowBand } from "../src/components/NowBand";
import { EMPTY_FILTER } from "../src/models/board";
import { envelope, liveSnapshotData, snapshotData } from "./fixtures";
import type { SnapshotData } from "../src/models/types";

afterEach(cleanup);

function header(data: SnapshotData) {
  return render(
    <ObservatoryHeaderBar
      snapshot={envelope(data)}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live
      filter={EMPTY_FILTER}
      shown={data.items.length}
      onToggleState={() => {}}
      onClearFilter={() => {}}
    />,
  );
}

describe("the lane pill is drawn at zero as well as at work", () => {
  test("a stopped board says 0/1 lanes running · idle", () => {
    const stopped: SnapshotData = {
      ...snapshotData,
      counts: { by_state: {}, dispatchable: 0, blocked: 0, runs_live: 0 },
      lanes: [{ lane_id: "lane-1", state: "idle", run_id: "", leased_until_ms: null }],
    };
    const { container } = header(stopped);
    const pill = container.querySelector(".lane-pill");
    expect(pill).not.toBeNull();
    expect(pill?.textContent).toContain("0/1 lanes running · idle");
    expect(pill?.className).toContain("lane-pill--idle");

    // The band is still absent: the pill is the answer to "is anything running", and the
    // band would be an empty copy of a run that does not exist.
    const { container: bandContainer } = render(<NowBand snapshot={stopped} now={1} />);
    expect(bandContainer.querySelector(".now-band")).toBeNull();
  });

  test("two working lanes say 2/2 lanes running", () => {
    const busy: SnapshotData = {
      ...liveSnapshotData,
      counts: { by_state: {}, dispatchable: 0, blocked: 0, runs_live: 2 },
      lanes: [
        { lane_id: "lane-1", state: "leased", run_id: "run-live-1", leased_until_ms: 1762979580000 },
        { lane_id: "lane-2", state: "leased", run_id: "run-live-2", leased_until_ms: 1762979580000 },
      ],
    };
    const { container } = header(busy);
    const pill = container.querySelector(".lane-pill");
    expect(pill?.textContent).toContain("2/2 lanes running");
    expect(pill?.textContent).not.toContain("idle");
    expect(pill?.className).toContain("lane-pill--running");
  });

  test("the pill is present before the first read answers", () => {
    const { container } = render(
      <ObservatoryHeaderBar
        snapshot={undefined}
        error={null}
        onRefresh={() => {}}
        refreshing={false}
        live={false}
        filter={EMPTY_FILTER}
        shown={0}
        onToggleState={() => {}}
        onClearFilter={() => {}}
      />,
    );
    const pill = container.querySelector(".lane-pill");
    expect(pill).not.toBeNull();
    // An unknown count is `—`, never a zero that would read as an idle board.
    expect(pill?.textContent).toContain("—/— lanes running");
  });
});
