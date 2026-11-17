import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { Home } from "../src/components/Home";
import { ItemsView } from "../src/components/ItemsPasses";
import type { WaitingRow } from "../src/models/types";
import { snapshotData } from "./fixtures";

afterEach(() => { cleanup(); window.history.replaceState(null, "", "/"); });
test("a cleared but retained document row never navigates to a fabricated item", () => {
 window.history.replaceState(null, "", "/#/items");
 const warning = { rank: "", item_id: "", document: "docs/design/a.md", class: "needs you", where: "queue", why: "design-without-item", mover: "owner", since: "today", class_source: "rule" };
 const view = render(<ItemsView waiting={[warning]} snapshot={snapshotData} />);
 fireEvent.focus(screen.getAllByRole("row")[1]);
 view.rerender(<ItemsView waiting={[]} snapshot={snapshotData} />);
 expect(screen.getByRole("cell", { name: "docs/design/a.md" })).toBeTruthy();
 fireEvent.keyDown(window, { key: "Enter" });
 expect(window.location.hash).toBe("#/items");
});

const rows = [
 { rank: "", item_id: "", document: "docs/design/z.md", class: "needs you", where: "queue", why: "design-without-item: docs/roadmap.md:3", mover: "owner: file the document's item block", since: "today", class_source: "rule" },
 { rank: "", item_id: "item-z", class: "needs you", where: "queue", why: "not-ready", mover: "owner", since: "today", class_source: "rule" },
 { rank: "", item_id: "", document: "docs/design/a.md", class: "needs you", where: "queue", why: "design-without-item: docs/roadmap.md:2", mover: "owner: file the document's item block", since: "today", class_source: "rule" },
];
for (const face of ["home", "items"]) {
 test(`${face} shows distinct design paths after unranked items without fake item links`, () => {
  window.history.replaceState(null, "", "/#/items");
  const waiting = rows as WaitingRow[];
  render(face === "items" ? <ItemsView waiting={waiting} snapshot={snapshotData} /> : <Home waiting={waiting} snapshot={snapshotData} now={0} />);
  const displayed = screen.getAllByRole("row").slice(1);
  expect(displayed.map(row => row.textContent)).toEqual([
   expect.stringContaining("item-z"), expect.stringContaining("docs/design/a.md"), expect.stringContaining("docs/design/z.md"),
  ]);
  expect(screen.getByRole("link", { name: "item-z" })).toBeTruthy();
  expect(document.querySelector('a[href="#/item/"]')).toBeNull();
  expect(document.querySelectorAll("tbody tr")).toHaveLength(3);
 });
}
