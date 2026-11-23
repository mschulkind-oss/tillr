import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { ItemGraph } from "../src/components/ItemGraph";
import type { ItemGraphAttempt, ItemGraphNode } from "../src/models/types";

afterEach(cleanup);
const node = (id: string, extra: Partial<ItemGraphNode> = {}): ItemGraphNode => ({ id, parent: "stage", kind: "agent", seat: "scout", command_ref: "", command: [], access: "read", state: "passed", level: 0, runs: [id + "-run"], children: {}, outputs: [], ...extra });
const attempts: ItemGraphAttempt[] = [
  { attempt: 2, nodes: [node("a", { runs: ["a-run", "retry"], children: {"a-run": {recorded: true, sessions: [{run_id:"a-run", session_id:"child", role:"reviewer", started_at_ms:null, ended_at_ms:null, turn_count:null, end_clock:"", transcript:""}]}, retry: {recorded:true, sessions:[]} }, outputs: [{name:"report", type:"text", run_id:"a-run", body:"hello"}] }), node("b", {children:{"b-run":{recorded:true,sessions:[]}}}), node("c", {level:1, parent:"", state:"running", runs:["c-run"]})] },
  { attempt: 1, nodes: [node("old")] },
];
test("draws ordered stages, all runs, outputs and collapsed earlier attempts", () => {
 render(<ItemGraph attempts={attempts} />);
 const a=screen.getByRole("article", {name:"Node a"});
 expect(within(a).getByRole("link", {name:"a-run"}).getAttribute("href")).toContain("/run/a-run");
 expect(within(a).getByRole("link", {name:"retry"})).toBeTruthy();
 expect(within(a).getByText("report · text")).toBeTruthy();
 expect(within(a).getByText("hello").tagName).toBe("PRE");
 expect(screen.getByText("Attempt 1").closest("details")?.open).toBe(false);
 const b=screen.getByRole("article", {name:"Node b"});
 expect(a.parentElement).toBe(b.parentElement);
 expect(screen.getByRole("article", {name:"Node c"}).parentElement).not.toBe(a.parentElement);
});
test("distinguishes observed, none and not recorded children within the parent", () => {
 render(<ItemGraph attempts={attempts} />);
 const a=screen.getByRole("article", {name:"Node a"});
 const child=within(a).getByRole("article", {name:"Child session child"});
 expect(child.textContent).toContain("observed");
 expect(within(a).getByText("child sessions: none")).toBeTruthy();
 expect(within(screen.getByRole("article", {name:"Node b"})).getByText("child sessions: none")).toBeTruthy();
 expect(within(screen.getByRole("article", {name:"Node c"})).getByText("child sessions: not recorded")).toBeTruthy();
 expect(screen.getByRole("region", {name:"Item graph"}).textContent).not.toContain("dispatched");
});
test("shows an interrupted run and its restart under one attempt heading", () => {
 const first = "run-item-seed-02-a2";
 const restart = "run-item-seed-02-a2-002-restart";
 const resumed = { attempt: 2, nodes: [node("worker", { parent: "", runs: [first, restart] })] };
 const laterID = "run-item-seed-02-a4";
 const later = { attempt: 4, nodes: [node("worker", { parent: "", runs: [laterID] })] };
 const { rerender } = render(<ItemGraph attempts={[resumed]} />);
 expect(screen.getByRole("heading", { name: "Nodes · attempt 2" })).toBeTruthy();
 const worker = within(screen.getByRole("article", { name: "Node worker" }));
 expect(worker.getAllByRole("link").map(link => link.getAttribute("href"))).toEqual([`/run/${first}`, `/run/${restart}`]);

 rerender(<ItemGraph attempts={[later, resumed]} />);
 expect(screen.getByRole("heading", { name: "Nodes · attempt 4" })).toBeTruthy();
 expect(screen.getByRole("link", { name: laterID })).toBeTruthy();
 expect(screen.getByText("Attempt 2").closest("details")?.open).toBe(false);
 expect(screen.queryByText("Attempt 3")).toBeNull();
});

test("single worker remains a visible node", () => {
 render(<ItemGraph attempts={[{attempt:1,nodes:[node("worker", {parent:"",runs:["only-run"]})]}]} />);
 expect(within(screen.getByRole("article",{name:"Node worker"})).getByRole("link",{name:"only-run"})).toBeTruthy();
});
