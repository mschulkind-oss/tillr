// The unranked block, and the payload that has no ranks at all.
//
// Two failures are what this suite exists to catch. The first is a store's silence drawn as
// a priority: an item the store never ranked sorts after every ranked one by id, and if it
// simply continues the list then `item-seed-01` (blocked) sitting below rank `z` is
// indistinguishable from an item the owner ranked last. The second is a *server's* silence
// drawn as the store's: on 2026-11-14 a `tillr-view` built before the rank field made every
// row unranked, and the board drew 55 rows in item-id order with nothing on screen saying
// why. Both are the same screen, and they are not the same fact — the store's has a remedy
// through `tillr item rank`, the server's has a remedy in the build.
//
// The order itself is asserted here as well, because it is one rule the server, this client
// and the drain planner share (design §4.1, ledger `D-OBS1`): ranked ascending as a plain
// string, unranked last, `item_id` as the tiebreak. The server's suite reads the same store
// shape (`the_board_is_ordered_by_the_queue_rank`); this is the client half.

import { render } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { QueueRail } from "../src/components/QueueRail";
import {
  EMPTY_FILTER,
  queueOrder,
  rankProvenance,
  splitByRank,
  unrankedCount,
} from "../src/models/board";
import type { BuildStamp, ItemView } from "../src/models/types";

/** One row of the board, with the only fields these cases read. `rank` is passed as
 * `undefined` to model a server that predates the field, which is a different payload from
 * one that carries `"rank": ""`. */
function row(itemId: string, rank: string | null | undefined, state = "in_queue"): ItemView {
  return {
    item_id: itemId,
    title: itemId,
    projected_state: { state, raw_state: state },
    updated_at_ms: 1762979400000,
    cost_usd: null,
    latest_decision: null,
    latest_run: null,
    ...(rank === undefined ? {} : { rank }),
  };
}

const BUILD: BuildStamp = {
  revision: "f0a53cd7ce6d9999aaaa",
  time: "2026-11-14T05:56:34Z",
  modified: false,
  front_end: "embedded",
  executable: "/workspace/.yolo/durable/tillr-view-observatory",
};

function rail(
  items: ItemView[],
  options: { build?: BuildStamp; unrankedTotal?: number } = {},
) {
  return render(
    <QueueRail
      items={queueOrder(items)}
      total={items.length}
      unrankedTotal={options.unrankedTotal}
      selectedItem={null}
      onSelectItem={() => {}}
      filter={EMPTY_FILTER}
      onFilterChange={() => {}}
      onClearFilter={() => {}}
      hidden={0}
      filterRef={{ current: null }}
      storeAbsent={false}
      build={options.build}
    />,
  );
}

/** The queue's titles in the order the DOM draws them, which is the order an operator
 * reads — not the order a prop was passed in. */
function drawnTitles(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll(".queue .queue-row__title")).map(
    (node) => node.textContent ?? "",
  );
}

/** A store holding both kinds, out of rank order on purpose. It is the same fixture the
 * server's suite seeds: `b`, `a`, `am` and two unranked rows, so an order that survived
 * insertion order would fail here. */
function storeHoldingBothKinds(): ItemView[] {
  return [
    row("item-seed-06", ""),
    row("item-seed-02", "b"),
    row("item-seed-04", "a"),
    row("item-seed-08", "am"),
    row("item-seed-01", ""),
  ];
}

describe("one order, cut into two blocks", () => {
  test("the split concatenates back to queueOrder's answer", () => {
    const store = storeHoldingBothKinds();
    const ordered = queueOrder(store);
    const { ranked, unranked } = splitByRank(ordered);
    expect([...ranked, ...unranked].map((item) => item.item_id)).toEqual(
      ordered.map((item) => item.item_id),
    );
  });

  test("ranked ascending, unranked last by id — the server's rule and the planner's", () => {
    // The server's `rankBefore` (assemble.go) and the drain's planner decide this same
    // list: rank `a` < `am` < `b` as plain strings, then the two unranked rows by id.
    expect(queueOrder(storeHoldingBothKinds()).map((item) => item.item_id)).toEqual([
      "item-seed-04",
      "item-seed-08",
      "item-seed-02",
      "item-seed-01",
      "item-seed-06",
    ]);
  });

  test("the rail draws the ranked block, then the heading, then the unranked block", () => {
    const { container } = rail(storeHoldingBothKinds());
    expect(drawnTitles(container)).toEqual([
      "item-seed-04",
      "item-seed-08",
      "item-seed-02",
      "item-seed-01",
      "item-seed-06",
    ]);
    const blocks = Array.from(container.querySelector(".queue-list")?.children ?? []);
    expect(blocks.map((node) => node.className)).toEqual(["queue", "queue-unranked"]);
    // The heading heads its own list rather than sitting inside it: the ranked list is
    // unreservedly priority, and the unranked one is unreservedly not.
    expect(blocks[1]?.querySelector(".queue-unranked__head")).not.toBeNull();
    expect(blocks[1]?.querySelector(".queue")).not.toBeNull();
    expect(blocks[0]?.querySelector(".queue-unranked__head")).toBeNull();
  });
});

describe("the unranked heading", () => {
  test("names the block, its count, and the command that corrects it", () => {
    const { container } = rail(storeHoldingBothKinds());
    const title = container.querySelector(".queue-unranked__title")?.textContent ?? "";
    expect(title).toContain("No priority recorded");
    expect(title).toContain("2 items");
    const note = container.querySelector(".queue-unranked__note")?.textContent ?? "";
    // How to rank them: the field and the one write face that owns it. "Correctable from
    // the board" is the criterion — an operator who has to discover the write command has a
    // state they cannot act on.
    expect(note).toContain("rank");
    expect(note).toContain("tillr item rank");
    expect(note).toContain("--item <id> --rank <token>");
  });

  test("counts the whole board, not just the rows the filter leaves visible", () => {
    // The header's own count says "shown / total" for the same reason: a number that
    // shrinks when a narrowing is applied is a number lying about the store.
    const { container } = rail(storeHoldingBothKinds().slice(0, 3), { unrankedTotal: 2 });
    expect(container.querySelector(".queue-unranked__title")?.textContent).toContain("2 items");

    const hidden = rail(storeHoldingBothKinds().slice(0, 4), { unrankedTotal: 9 });
    expect(hidden.container.querySelector(".queue-unranked__title")?.textContent).toContain(
      "1 of 9 items",
    );
  });

  test("says nothing at all when every row is ranked", () => {
    const { container } = rail([row("item-a", "a"), row("item-b", "b")]);
    expect(container.querySelector(".queue-unranked")).toBeNull();
    expect(container.textContent).not.toContain("No priority");
  });
});

describe("a payload with no rank on any item", () => {
  test("is a server older than the field, and it is named as one", () => {
    // The measured failure: a `tillr-view` built before the rank field. Every row is
    // drawn, but the heading must say the order is item id and name the build, or the
    // board is presenting an order it cannot vouch for as priority.
    const items = [row("item-a", undefined), row("item-b", undefined)];
    expect(rankProvenance(items)).toBe("server-unranked");
    const { container } = rail(items, { build: BUILD });
    const head = container.querySelector(".queue-unranked__head");
    expect(head?.getAttribute("data-provenance")).toBe("server-unranked");
    expect(container.querySelector(".queue-unranked__title")?.textContent).toContain(
      "No priority in this payload",
    );
    expect(container.textContent).not.toContain("No priority recorded");
    expect(container.textContent).toContain("sends no");
    expect(container.textContent).toContain("f0a53cd7ce6d");
  });

  test("is drawn as unverified when the server cannot name its own build", () => {
    const { container } = rail([row("item-a", undefined), row("item-b", undefined)]);
    expect(container.textContent).toContain("build unverified");
  });
});

describe("a store that has never been ranked", () => {
  test("is the store's silence, not the server's: the field is there and empty", () => {
    const items = [row("item-a", ""), row("item-b", null)];
    expect(rankProvenance(items)).toBe("store-unranked");
    const { container } = rail(items, { build: BUILD });
    const head = container.querySelector(".queue-unranked__head");
    expect(head?.getAttribute("data-provenance")).toBe("store-unranked");
    expect(container.querySelector(".queue-unranked__title")?.textContent).toContain(
      "No priority recorded",
    );
    expect(container.textContent).toContain("never ranked an item");
    expect(container.textContent).toContain("tillr item rank");
    // The build is not the diagnosis here, and naming it would blur the two.
    expect(container.textContent).not.toContain("f0a53cd7ce6d");
  });
});

describe("the provenance read from the payload's shape", () => {
  test("one ranked row makes the whole board a ranked board", () => {
    expect(rankProvenance([row("item-a", "a"), row("item-b", "")])).toBe("recorded");
  });

  test("an empty board is not a server older than the field", () => {
    expect(rankProvenance([])).toBe("store-unranked");
    expect(unrankedCount([])).toBe(0);
  });

  test("counts the unranked rows and only those", () => {
    expect(unrankedCount(storeHoldingBothKinds())).toBe(2);
  });
});
