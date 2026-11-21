import { describe, expect, test } from "vitest";

import { waitingActors } from "../src/models/waiting";
import type { WaitingRow } from "../src/models/types";

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
