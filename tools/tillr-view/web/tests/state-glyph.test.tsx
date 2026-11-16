// The state encoding contract (design §7.2, ledger `D-OBS8`): an item state is colour, word
// and glyph on every surface, and the glyph is the channel that survives a greyscale print,
// a colour-blind reader and a low-contrast projector.
//
// The table below is the *written* table. The component is compared against it rather than a
// screenshot being looked at, because a capture nobody reads is not a check: the table names
// the design's five hand-named shapes (`dot`, `triangle`, `bar`, `check`, `slash`), the two
// it leaves unnamed, and the fallback a state the projection grows later gets. A shape
// chosen by eye fails here first, at 12 px, before it fails a printed page.

import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, test } from "vitest";

import { ItemWithoutRun } from "../src/components/ItemWithoutRun";
import { ItemDetailPane } from "../src/components/ItemDetailPane";
import { ObservatoryHeaderBar } from "../src/components/ObservatoryHeaderBar";
import { QueueRail } from "../src/components/QueueRail";
import {
  PROJECTED_STATES,
  STATE_GLYPHS,
  StateGlyph,
  glyphFor,
} from "../src/components/StateGlyph";
import { EMPTY_FILTER, queueOrder, stateLabel } from "../src/models/board";
import type { ItemView } from "../src/models/types";
import { blockedItemData, envelope, filterSnapshotData } from "./fixtures";

afterEach(cleanup);

/** The written table: one shape per projected state, plus `unknown`. */
const WRITTEN: Record<string, string> = {
  not_started: "ring",
  in_queue: "dot",
  working: "triangle",
  blocked: "bar",
  landed: "check",
  resolved_elsewhere: "arrow",
  closed: "slash",
  unknown: "square",
};

/** The design names these five by hand in §7.2, so they are not this build's to rename. */
const NAMED_BY_DESIGN: Record<string, string> = {
  in_queue: "dot",
  working: "triangle",
  blocked: "bar",
  landed: "check",
  closed: "slash",
};

/** A colour literal anywhere inside a glyph is the colour channel leaking into the shape:
 * the shape has to be drawable in one ink, which is how it survives greyscale. */
const COLOR_LITERAL = /#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(|style=/i;

/** The board, optionally with its colour forced to one value (the greyscale capture). */
function rail(items: ItemView[] = filterSnapshotData.items, color?: string) {
  return render(
    <div style={color === undefined ? undefined : { color }}>
      <QueueRail
        items={queueOrder(items)}
        total={items.length}
        selectedItem={null}
        onSelectItem={() => {}}
        filter={EMPTY_FILTER}
        onFilterChange={() => {}}
        onClearFilter={() => {}}
        hidden={0}
        filterRef={{ current: null }}
        storeAbsent={false}
      />
    </div>,
  );
}

function header() {
  return render(
    <ObservatoryHeaderBar
      snapshot={envelope(filterSnapshotData)}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live={false}
      filter={EMPTY_FILTER}
      shown={filterSnapshotData.items.length}
      onToggleState={() => {}}
      onClearFilter={() => {}}
    />,
  );
}

function itemPane() {
  return render(
    <ItemDetailPane
      itemId={blockedItemData.item.item_id}
      item={blockedItemData}
      itemError={null}
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

function glyphNode(container: HTMLElement, selector: string): SVGSVGElement | null {
  return container.querySelector<SVGSVGElement>(selector);
}

describe("the written table", () => {
  test("is the design's five shapes plus one per projected state, all distinct", () => {
    expect(STATE_GLYPHS).toEqual(WRITTEN);
    // The seven states the projection is closed over each get a shape of their own.
    expect([...PROJECTED_STATES]).toHaveLength(7);
    const shapes = PROJECTED_STATES.map((state) => STATE_GLYPHS[state]);
    expect(shapes).toEqual(PROJECTED_STATES.map((state) => WRITTEN[state]));
    expect(new Set(shapes).size).toBe(7);
    // A glyph table missing a state is the review finding §7.2 describes, so the table
    // carries an `unknown` for the state the projection has not grown yet.
    expect(new Set(Object.values(STATE_GLYPHS)).size).toBe(Object.keys(STATE_GLYPHS).length);
    expect(STATE_GLYPHS.unknown).toBe(WRITTEN.unknown);
  });

  test("keeps the shapes the design names rather than re-drawing them", () => {
    for (const [state, glyph] of Object.entries(NAMED_BY_DESIGN)) {
      expect(glyphFor(state)).toBe(glyph);
    }
  });

  test("falls back to the unknown shape, never to no shape", () => {
    // A state the table does not name gets the table's `unknown` shape, not a default that
    // happens to look like some state's.
    expect(glyphFor("waiting")).toBe(STATE_GLYPHS.unknown);
    expect(glyphFor("a-state-no-build-has-seen")).toBe(STATE_GLYPHS.unknown);
    const { container } = render(<StateGlyph state="a-state-no-build-has-seen" />);
    expect(glyphNode(container, "svg")?.getAttribute("data-glyph")).toBe(STATE_GLYPHS.unknown);
    // The word is still the board's word: the unknown glyph accompanies it.
    expect(stateLabel("a_state_no_build_has_seen")).toBe("a state no build has seen");
  });
});

describe("the component", () => {
  test("renders a distinct shape per projected state, painted with no colour of its own", () => {
    const geometries = new Map<string, string>();
    for (const state of PROJECTED_STATES) {
      const { container } = render(<StateGlyph state={state} />);
      const glyph = glyphNode(container, "svg");
      expect(glyph, state).not.toBeNull();
      expect(glyph?.getAttribute("data-glyph")).toBe(WRITTEN[state]);
      expect(glyph?.getAttribute("data-state")).toBe(state);
      // One ink: the geometry names `currentColor` and no literal.
      const markup = glyph?.innerHTML ?? "";
      expect(markup).toContain("currentColor");
      expect(markup).not.toMatch(COLOR_LITERAL);
      // No two states share a silhouette.
      expect(geometries.has(markup), state).toBe(false);
      geometries.set(markup, state);
      cleanup();
    }
    expect(geometries.size).toBe(PROJECTED_STATES.length);
  });

  test("is decorative: the word is the accessible channel", () => {
    const { container } = render(<StateGlyph state="working" />);
    const glyph = glyphNode(container, "svg");
    expect(glyph?.getAttribute("aria-hidden")).toBe("true");
    expect(glyph?.getAttribute("focusable")).toBe("false");
    expect(glyph?.textContent).toBe("");
  });
});

describe("the capture-time check: greyscale", () => {
  // The board is captured with its colour forced to one value, which is what a greyscale
  // print does to it. Every glyph paints its shape with `currentColor`, so both rows resolve
  // to the same ink; whatever still tells a blocked row from an in-queue row is the shape.
  test("a blocked row and an in-queue row stay distinguishable with one ink", () => {
    const { container } = rail(filterSnapshotData.items, "#000000");
    const rows = Array.from(container.querySelectorAll(".queue-row"));
    const rowFor = (itemId: string) => {
      const row = rows.find((candidate) => candidate.textContent?.includes(itemId));
      expect(row, itemId).toBeDefined();
      return {
        row,
        glyph: row?.querySelector<SVGSVGElement>(".queue-row__glyph svg") ?? null,
      };
    };

    const blocked = rowFor("item-blocked");
    const queued = rowFor("item-queue");
    expect(blocked.glyph?.getAttribute("data-glyph")).toBe("bar");
    expect(queued.glyph?.getAttribute("data-glyph")).toBe("dot");

    // With the colour flattened, the silhouettes are the whole difference and they differ.
    expect(blocked.glyph?.innerHTML).not.toBe(queued.glyph?.innerHTML);
    for (const { glyph } of [blocked, queued]) {
      const markup = glyph?.innerHTML ?? "";
      expect(markup).toContain("currentColor");
      expect(markup).not.toMatch(COLOR_LITERAL);
    }

    // And the row is not leaning on shape alone either: the word is beside the glyph.
    expect(blocked.row?.textContent).toContain("Blocked");
    expect(queued.row?.textContent).toContain("In queue");
  });
});

describe("every surface draws the one component", () => {
  test("the rail's row, the item pane's chip, the header's count and the no-run badge", () => {
    const one = render(<StateGlyph state="blocked" />).container.innerHTML;
    cleanup();

    const railGlyph = glyphNode(
      rail().container,
      '.queue-row[data-state="blocked"] .queue-row__glyph svg',
    );
    expect(railGlyph?.outerHTML).toBe(one);
    cleanup();

    const chipGlyph = glyphNode(itemPane().container, ".state-chip svg");
    expect(chipGlyph?.outerHTML).toBe(one);
    cleanup();

    const badgeGlyph = glyphNode(
      render(<ItemWithoutRun item={blockedItemData} hasRun={false} />).container,
      ".state-badge svg",
    );
    expect(badgeGlyph?.outerHTML).toBe(one);

    const { container: head } = header();
    const blockedStat = Array.from(head.querySelectorAll(".stat")).find((stat) =>
      stat.querySelector(".stat__label")?.textContent?.startsWith("Blocked"),
    );
    expect(blockedStat).toBeDefined();
    expect(blockedStat?.querySelector("svg")?.outerHTML).toBe(one);
  });

  test("a count that is not a state draws no state's shape", () => {
    const { container } = header();
    const stat = (label: string) =>
      Array.from(container.querySelectorAll(".stat")).find((node) =>
        node.querySelector(".stat__label")?.textContent?.startsWith(label),
      );
    // The item total is an inventory, not a state, so it borrows nobody's glyph.
    expect(stat("Items")?.querySelector("svg")).toBeNull();
    // Every state count has one.
    for (const label of ["In queue", "Working", "Blocked", "Landed", "Closed"]) {
      expect(stat(label)?.querySelector("svg"), label).not.toBeNull();
    }
  });
});
