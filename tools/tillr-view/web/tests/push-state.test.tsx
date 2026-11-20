// Design Section 7: a parked push is a row with a named mover and a header pill, and a
// drained queue with unpublished proven commits does not look finished.

import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, test } from "vitest";

import { PushPill } from "../src/components/ObservatoryHeaderBar";
import { waitingActors } from "../src/models/waiting";
import type { PushSummary, WaitingRow } from "../src/models/types";

afterEach(cleanup);

const row = (cls: string, mover: string): WaitingRow => ({
  item_id: "", observation: "post-landing:push", class: cls, where: "push", why: "w", mover, since: "t", class_source: "store",
});

describe("push rows reach the right actor", () => {
  test("needs-you holds go to owner or operator, active repair to the drain", () => {
    const counts = waitingActors([
      row("needs you", "owner; resolve gate-code conflict onto origin/main (abc)"),
      row("needs you", "operator; rebase main onto origin/main (abc)"),
      row("needs you", "operator; run tillr drain acknowledge-patch-move after checking the fork rewrite"),
      row("waits on work", "sync-remote-abc; resolve rebase conflict onto origin/main (abc)"),
      row("waits on work", "the heavy runner; prove rebased tip x before pushing"),
    ]);
    expect(counts).toEqual({ owner: 1, drain: 2, design: 0, operator: 2 });
  });
});

describe("the push pill", () => {
  const base: PushSummary = { what: "green", remote: "origin", ref: "main", unpublished_green_count: 0, unpublished_main_count: 0 };
  test("is absent with no push recorded or nothing parked", () => {
    expect(render(<PushPill push={{ what: "none" }} />).container.textContent).toBe("");
    expect(render(<PushPill push={base} />).container.textContent).toBe("");
    expect(render(<PushPill />).container.textContent).toBe("");
  });
  test("counts proven commits in the push's own mode", () => {
    const green = render(<PushPill push={{ ...base, unpublished_green_count: 3, unpublished_main_count: 9 }} />);
    expect(green.container.textContent).toBe("3 unpublished");
    cleanup();
    const tip = render(<PushPill push={{ ...base, what: "tip", unpublished_green_count: 3, unpublished_main_count: 9 }} />);
    expect(tip.container.textContent).toBe("9 unpublished");
  });
  test("names a held state even when no proven commit is ahead", () => {
    const v = render(<PushPill push={{ ...base, state_kind: "push-remote-declined", mover: "operator; x" }} />);
    expect(v.container.textContent).toBe("0 unpublished · push-remote-declined");
    expect(v.container.querySelector(".push-pill--held")?.getAttribute("title")).toContain("waits on: operator; x");
  });
});
