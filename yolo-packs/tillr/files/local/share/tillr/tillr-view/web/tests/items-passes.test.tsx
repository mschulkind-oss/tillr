import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { foldPasses, ItemsView, PassesView } from "../src/components/ItemsPasses";
import { Home } from "../src/components/Home";
import type { PassRow, WaitingRow } from "../src/models/types";
import { snapshotData } from "./fixtures";

afterEach(() => { cleanup(); window.history.replaceState(null, "", "/"); });
const rows: WaitingRow[] = [
  { place: 2, item_id: "item-b", class: "ready", where: "queue", why: "ready", mover: "next pass", since: "today", class_source: "rule" },
  { place: 1, item_id: "item-a", class: "needs you", where: "queue", why: "question-open", mover: "OQ-1", since: "yesterday", class_source: "rule" },
];
const pass = (ms: number, reason = "blocked"): PassRow => ({ dispatched_at_ms: ms, decisions: [{ item_id: "item-a", place: 1, action: "hold", reason }] });
function route(hash: string) { window.history.replaceState(null, "", `/${hash}`); }

test.each(["items", "home"])("%s waiting table reports who changed the graph and node counts", (face) => {
  route("#/items?state=unlanded");
  const waiting = [{ ...rows[0], graph_changes: ["graph changed by open (plan-run): 4 → 5 nodes"] }];
  render(face === "items" ? <ItemsView waiting={waiting} snapshot={snapshotData} /> : <Home waiting={waiting} snapshot={snapshotData} />);
  expect(screen.getByText("graph changed by open (plan-run): 4 → 5 nodes")).toBeTruthy();
});

test("Pass filters say what they do and offer only recorded actions", () => {
  route("#/passes?range=all");
  render(<PassesView passes={[pass(1000), { dispatched_at_ms: 2000, decisions: [{ item_id: "item-b", place: 2, action: "dispatch", reason: "ready" }] }]} />);
  const actions = screen.getByLabelText("Action");
  expect(actions.tagName).toBe("SELECT");
  expect([...actions.querySelectorAll("option")].map(option => option.value)).toEqual(["", "dispatch", "hold"]);
  expect(screen.getByLabelText("Item").getAttribute("title")).toContain("touched");
  fireEvent.change(screen.getByLabelText("Item"), { target: { value: "item-b" } });
  expect(screen.getAllByTestId("pass-group")).toHaveLength(1);
  expect(screen.getByLabelText("show passes where nothing moved")).toBeTruthy();
});

test("Items presets, class and reason persist in the URL on cold mount", () => {
  route("#/items");
  const view = render(<ItemsView waiting={rows} snapshot={snapshotData} />);
  expect(screen.getAllByRole("row")).toHaveLength(2);
  fireEvent.click(screen.getByRole("button", { name: "all unlanded" }));
  fireEvent.change(screen.getByLabelText("Class"), { target: { value: "needs you" } });
  fireEvent.change(screen.getByLabelText("Reason"), { target: { value: "question-open" } });
  expect(new URLSearchParams(window.location.hash.split("?")[1]).get("state")).toBe("unlanded");
  view.unmount();
  render(<ItemsView waiting={rows} snapshot={snapshotData} />);
  expect((screen.getByLabelText("Class") as HTMLSelectElement).value).toBe("needs you");
  expect((screen.getByLabelText("Reason") as HTMLSelectElement).value).toBe("question-open");
  expect(screen.getAllByRole("row")).toHaveLength(2);
  fireEvent.click(screen.getByRole("button", { name: "landed" }));
  expect(window.location.hash).toContain("state=landed");
});

test("Items offers waits on the loop, not an unclassified hold", () => {
  route("#/items?state=unlanded");
  const held = { ...rows[0], class: "waits on the loop", why: "not yet planned", mover: "the next pass" };
  render(<ItemsView waiting={[held]} snapshot={snapshotData} />);
  const classes = screen.getByLabelText("Class");
  expect(within(classes).queryByRole("option", { name: "unclassified" })).toBeNull();
  expect(within(classes).getByRole("option", { name: "waits on the loop" })).toBeTruthy();
  fireEvent.change(classes, { target: { value: "waits on the loop" } });
  expect(screen.getAllByRole("row")).toHaveLength(2);
  expect(screen.getByRole("cell", { name: "not yet planned" })).toBeTruthy();
});

test("Passes fold consecutive identical answers independent of decision order, not separated matches", () => {
  route("#/passes?range=all");
  const a = pass(4000), b = pass(3000), c = pass(2000, "ready"), d = pass(1000);
  render(<PassesView passes={[d, c, b, a]} />);
  const groups = screen.getAllByTestId("pass-group");
  expect(groups).toHaveLength(3);
  expect(groups[0].textContent).toContain("×2 passes");
  expect(groups[0].querySelector("details")?.open).toBe(true);
  expect(groups[1].querySelector("details")?.open).toBe(true);
  expect(groups[2].querySelector("details")?.open).toBe(false);
  expect(groups[1].textContent).toContain("changed:");
});

test.each(["items", "passes"])("%s keeps arrivals behind newer while scrolled and supports row keys", kind => {
  route(`#/${kind}${kind === "passes" ? "?range=all" : "?state=unlanded"}`);
  const view = render(kind === "items" ? <ItemsView waiting={rows} snapshot={snapshotData} /> : <PassesView passes={[pass(2000), pass(1000, "ready")]} />);
  const list = screen.getByTestId("stable-list");
  fireEvent.scroll(list, { target: { scrollTop: 200 } });
  view.rerender(kind === "items" ? <ItemsView waiting={[...rows, { ...rows[0], item_id: "item-new", place: 1, class: "needs you" }]} snapshot={snapshotData} /> : <PassesView passes={[pass(3000, "new"), pass(2000), pass(1000, "ready")]} />);
  expect(screen.getByRole("button", { name: "1 newer ↑" })).toBeTruthy();
  expect(list.textContent).not.toContain(kind === "items" ? "item-new" : "19700101T000003Z");
  fireEvent.keyDown(window, { key: "j" });
  expect(list.querySelector('[data-focused="true"]')).toBeTruthy();
  fireEvent.keyDown(window, { key: "j" });
  fireEvent.keyDown(window, { key: "k" });
  const focused = list.querySelector('[data-focused="true"]')!;
  const href = within(focused as HTMLElement).getAllByRole("link")[0].getAttribute("href");
  fireEvent.keyDown(window, { key: "Enter" });
  expect(window.location.hash).toBe(href);
  route(`#/${kind}`);
  fireEvent.keyDown(window, { key: "]" });
  expect(window.location.hash).not.toBe(href);
  fireEvent.keyDown(window, { key: "[" });
  expect(window.location.hash).toBe(href);
  fireEvent.keyDown(window, { key: "u" });
  expect(window.location.hash).toBe("#/");
  route(`#/${kind}`);
  fireEvent.click(screen.getByRole("button", { name: "1 newer ↑" }));
  expect(screen.queryByRole("button", { name: "1 newer ↑" })).toBeNull();
});

test("focused rows also hold arrivals, and typing does not navigate", () => {
  route("#/items?state=unlanded");
  const view = render(<ItemsView waiting={rows} snapshot={snapshotData} />);
  fireEvent.keyDown(window, { key: "j" });
  view.rerender(<ItemsView waiting={[{ ...rows[0], item_id: "item-new", place: 1 }, ...rows]} snapshot={snapshotData} />);
  expect(screen.getByRole("button", { name: "1 newer ↑" })).toBeTruthy();
  fireEvent.keyDown(screen.getByLabelText("Class"), { key: "u" });
  expect(window.location.hash).toContain("#/items");
});

test("folding compares full answers, ignores decision order and keeps session boundaries", () => {
  const a = pass(4000);
  a.decisions.push({ item_id: "item-b", place: 2, action: "dispatch", reason: "ready", lane_id: "lane-1", run_id: "run-1" });
  const b = { ...a, dispatched_at_ms: 3000, decisions: [...a.decisions].reverse() };
  const c = { ...b, dispatched_at_ms: 2000, session_id: "other-session" };
  expect(foldPasses([c, a, b]).map(group => group.count)).toEqual([2, 1]);
});

test("arrivals at the top appear immediately and landed uses the snapshot", () => {
  route("#/items?state=unlanded");
  const view = render(<ItemsView waiting={rows} snapshot={snapshotData} />);
  view.rerender(<ItemsView waiting={[...rows, { ...rows[0], item_id: "item-new" }]} snapshot={snapshotData} />);
  expect(screen.getByRole("link", { name: "item-new" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: /newer/ })).toBeNull();
  cleanup();
  route("#/items?state=landed");
  render(<ItemsView waiting={rows} snapshot={{ ...snapshotData, items: [{ ...snapshotData.items[0], item_id: "item-landed", projected_state: { state: "landed", raw_state: "landed" } }] }} />);
  expect(screen.getByRole("link", { name: "item-landed" })).toBeTruthy();
  expect(screen.queryByRole("link", { name: "item-a" })).toBeNull();
});

test("the actual shell scrollport holds arrivals and acceptance resets it", () => {
  route("#/items?state=unlanded");
  const view = render(<div className="observatory-shell"><ItemsView waiting={rows} snapshot={snapshotData} /></div>);
  const shell = view.container.firstElementChild!;
  fireEvent.scroll(shell, { target: { scrollTop: 200 } });
  view.rerender(<div className="observatory-shell"><ItemsView waiting={[...rows, { ...rows[0], item_id: "item-new" }]} snapshot={snapshotData} /></div>);
  expect(screen.queryByRole("link", { name: "item-new" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "1 newer ↑" }));
  expect(shell.scrollTop).toBe(0);
});

test("removal-only updates offer acceptance rather than retaining stale rows forever", () => {
  route("#/items");
  const view = render(<ItemsView waiting={[rows[1]]} snapshot={snapshotData} />);
  fireEvent.keyDown(window, { key: "j" });
  expect(document.activeElement?.getAttribute("data-focused")).toBe("true");
  view.rerender(<ItemsView waiting={[]} snapshot={snapshotData} />);
  fireEvent.click(screen.getByRole("button", { name: "1 updates ↑" }));
  expect(screen.queryByRole("link", { name: "item-a" })).toBeNull();
});

test("all history explicitly offers older pages even when the loaded filters match nothing", () => {
  route("#/passes?range=all&item=older-item");
  let requested = false;
  render(<PassesView passes={[pass(1000)]} more onOlder={() => { requested = true; }} />);
  expect(screen.getByText(/filters search the loaded history/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Load older passes" }));
  expect(requested).toBe(true);
});

test("read refusals are not empty lists", () => {
  render(<ItemsView error={new Error("waiting locked")} />);
  expect(screen.getByRole("alert").textContent).toContain("waiting locked");
  cleanup();
  render(<PassesView error={new Error("passes locked")} />);
  expect(screen.getByRole("alert").textContent).toContain("passes locked");
});


test("filtered Items rows keep their true places with gaps", () => {
 route("#/items?state=unlanded");
 const waiting = [
  { ...rows[0], item_id: "item-first", place: 1, class: "ready" },
  { ...rows[0], item_id: "item-hidden", place: 2, class: "needs you" },
  { ...rows[0], item_id: "item-third", place: 3, class: "ready" },
 ];
 const { container } = render(<ItemsView waiting={waiting} snapshot={snapshotData} />);
 fireEvent.change(screen.getByLabelText("Class"), { target: { value: "ready" } });
 expect(Array.from(container.querySelectorAll("tbody tr")).map(row => row.querySelector("td")?.textContent)).toEqual(["1", "3"]);
 expect(screen.getByRole("columnheader", { name: "#" })).toBeTruthy();
 expect(container.querySelector(".queue-unranked")).toBeNull();
});
