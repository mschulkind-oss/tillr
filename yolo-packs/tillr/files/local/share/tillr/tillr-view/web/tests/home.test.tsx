import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { Home, LoopSummary } from "../src/components/Home";
import { ENTITY_KINDS, EntityLink } from "../src/components/EntityLink";
import { snapshotData } from "./fixtures";

const now = Date.now();
const drain = { ...snapshotData.drain_summary, session_grace_ms: 5000,
  session: { session_id: "session-a", started_at_ms: now - 60000, refreshed_at_ms: now,
    next_pass_at_ms: now + 10000, ended_at_ms: now }, last_stop_reason: "queue-drained" };
const passes = [
  { dispatched_at_ms: now - 10000, decisions: [{ item_id: "item-cause", rank: "a", action: "hold", reason: "gate-failed" }], stop: "queue-drained", detail: "nothing dispatchable" },
  { dispatched_at_ms: now - 5000, decisions: [{ item_id: "item-owner", rank: "b", action: "dispatch", reason: "ready" }], stop: "moves", detail: "one dispatched" },
];
const waiting = [
  { rank: "b", item_id: "item-owner-b", class: "needs you", where: "queue", why: "question-open", mover: "OQ-1", since: "2h", class_source: "rule" },
  { rank: "a", item_id: "item-cause", class: "needs a cause", where: "gate", why: "gate-failed", mover: "cause not recorded", since: "1h", class_source: "rule" },
  { rank: "c", item_id: "item-work", class: "waits on work", where: "queue", why: "blocked", mover: "item-cause", since: "1h", class_source: "rule" },
  { rank: "a", item_id: "item-owner-a", class: "needs you", where: "queue", why: "question-open", mover: "OQ-2", since: "2h", class_source: "rule" },
];
afterEach(cleanup);

test("one entity component links all eight kinds, including missing references", () => {
  expect(ENTITY_KINDS).toEqual(["item", "run", "pass", "lane", "check", "landing", "doc", "question"]);
  const { container } = render(<>{ENTITY_KINDS.map(kind => <EntityLink key={kind} kind={kind} id="missing / id" title="Title" />)}</>);
  for (const kind of ENTITY_KINDS) expect(container.querySelector(`[data-entity-kind="${kind}"]`)?.getAttribute("href")).toBe(`#/${kind}/missing%20%2F%20id`);
  expect(container.querySelectorAll("svg")).toHaveLength(8);
});

test("Home groups by mover, ranks within groups and keeps every entity id in EntityLink", () => {
  const { container } = render(<Home snapshot={snapshotData} waiting={waiting} />);
  const groups = Array.from(container.querySelectorAll("section[data-class]"));
  expect(groups.map(g => g.getAttribute("data-class"))).toEqual(["needs you", "needs a cause", "waits on work"]);
  expect(Array.from(groups[0].querySelectorAll('[data-entity-kind="item"]')).map(n => n.textContent)).toEqual(["item-owner-a", "item-owner-b"]);
  for (const id of [...waiting.map(row => row.item_id), "OQ-1", "OQ-2", "lane-1"]) {
    const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT);
    while (walker.nextNode()) if (walker.currentNode.textContent?.includes(id)) expect(walker.currentNode.parentElement?.closest(".entity-link")).not.toBeNull();
  }
});

test("Home shows dirty landing paths and elapsed waiting time beside since", () => {
  const { container } = render(<Home waiting={[{ ...waiting[2], where: "land", why: "trunk-path-dirty: a.txt, b.txt", mover: "commit or restore the path", since: "2026-11-16T12:00:00Z", waited: "2m0s" }]} />);
  expect(container.textContent).toContain("trunk-path-dirty: a.txt, b.txt");
  expect(container.textContent).toContain("2026-11-16T12:00:00Z · waited 2m0s");
});

test("Home caps each group at eight and names remaining rows", () => {
  const rows = Array.from({ length: 11 }, (_, i) => ({ ...waiting[0], item_id: `item-${i}`, rank: String(i).padStart(2, "0") }));
  const { container } = render(<Home snapshot={snapshotData} waiting={rows} />);
  expect(container.querySelectorAll('tbody [data-entity-kind="item"]')).toHaveLength(8);
  expect(screen.getByText("3 more needs you", { exact: false })).toBeTruthy();
});

test("waiting refusal is words, never an empty table", () => {
  const { container } = render(<Home snapshot={snapshotData} waitingError={new Error("waiting could not read the store")} />);
  expect(screen.getByRole("alert").textContent).toContain("waiting could not read the store");
  expect(container.querySelector("table")).toBeNull();
});

test("loop summary uses snapshot session and newest pass, not active runs or old stop detail", () => {
  const { container } = render(<LoopSummary drain={drain} passes={passes} now={now} />);
  expect(container.textContent).toContain("stopped");
  expect(container.textContent).toContain("one dispatched");
  expect(container.textContent).not.toContain("nothing dispatchable");
  expect(container.textContent).toContain("1 dispatched · 0 held");
  expect(screen.getByRole("link", { name: "session-a" }).getAttribute("href")).toBe("#/passes?session=session-a");
});

test.each([
  [null, "not known"],
  [{ ...drain.session, ended_at_ms: null }, "moving"],
  [{ ...drain.session, ended_at_ms: null, next_pass_at_ms: now - 6000 }, "not known"],
])("loop state follows recorded heartbeat and grace", (session, word) => {
  const { container } = render(<LoopSummary drain={{ ...drain, active: true, session }} now={now} />);
  expect(container.querySelector(".loop-summary__state")?.textContent).toContain(word);
});

test("quoted stop detail links item identities instead of leaking plain-text IDs", () => {
  const row = { ...passes[0], detail: "nothing dispatchable: item_042 held behind item-missing", decisions: [{ item_id: "item_042", rank: "a", action: "hold", reason: "blocked" }] };
  const { container } = render(<LoopSummary drain={drain} passes={[row]} now={now} />);
  for (const id of ["item_042", "item-missing"]) {
    const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT);
    while (walker.nextNode()) if (walker.currentNode.textContent?.includes(id)) expect(walker.currentNode.parentElement?.closest(".entity-link")).not.toBeNull();
  }
  expect(container.querySelector("q")?.textContent).toBe(row.detail);
});

test.each([
  ["queue-drained", "after a hold moves"],
  ["lanes-leased", "tillr run release"],
  ["operator-stop", "operator wants"],
  ["host-boundary-violation", "do not retry"],
  ["gate-harness-error", "repair the gate harness"],
  ["fault", "diagnose the recorded fault"],
  ["future-stop", "inspect the recorded stop"],
])("restart guidance is specific to %s", (reason, guidance) => {
  const { container } = render(<LoopSummary drain={{ ...drain, last_stop_reason: reason }} passes={passes} now={now} />);
  expect(container.querySelector(".loop-summary__state")?.textContent).toContain(guidance);
});

test("each class remains a distinct shape and word when color is forced", () => {
  const rows = ["moving", "ready", "needs you", "waits on work", "needs a cause", "unclassified"].map((value, i) => ({ ...waiting[0], class: value, item_id: `item-${i}` }));
  const { container } = render(<div style={{ color: "black" }}><Home snapshot={snapshotData} waiting={rows} /></div>);
  const badges = Array.from(container.querySelectorAll("tbody .class-state"));
  expect(new Set(badges.map(b => b.querySelector("svg")?.getAttribute("data-glyph"))).size).toBe(6);
  expect(new Set(badges.map(b => b.textContent)).size).toBe(6);
});
