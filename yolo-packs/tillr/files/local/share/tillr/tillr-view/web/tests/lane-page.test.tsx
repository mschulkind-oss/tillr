import { cleanup, fireEvent, render, within } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { LaneDetail, PipelineRow, ItemGraph } from "../src/components/LanePage";
import { TranscriptView } from "../src/components/TranscriptView";
import { transcriptData } from "./fixtures";
import type { ItemData, LaneRow, PipelineStep } from "../src/models/types";

afterEach(cleanup);
const itemData: ItemData = { item: {item_id:"item-a", title:"Build it", body_ref:"", design_doc:"", allowed_paths:[], projected_state:{state:"working", raw_state:"in-progress"}}, criteria:[{criterion_id:"c1", text:"works", state:"open"}], blocked_by:["item-before"], unblocks:["item-after"], closed_blockers:[], verdicts:[], decisions:[], attempts:[], runs:[], owner_decisions:[], state_moves:[] };
const lane: LaneRow = { lane_id: "lane-1", state: "running", run_id: "run-a", item_id: itemData.item.item_id, backend: "pi", seat: "builder", attempt: 2, outcome: "", state_since_ms: 1000, last_step: "", last_step_at_ms: null, log_grew_at_ms: 1000, latest_action: { kind: "action", text: "bash: go test", at_ms: 1000, clock: "observed" } };

test.each(["stage", "graph", "gate", "land", "complete"])("pipeline position %s is recorded, not inferred", position => {
 const names = ["stage", "graph", "gate", "land"];
 const index = position === "complete" ? 4 : names.indexOf(position);
 const steps: PipelineStep[] = names.slice(0, index + 1).map((step, i) => ({ step, started_at_ms: 1000 + i * 1000, finished_at_ms: i < index ? 2000 + i * 1000 : null }));
 const { container } = render(<PipelineRow steps={steps} now={9000} />);
 const current = container.querySelectorAll('[aria-current="step"]');
 expect(current).toHaveLength(position === "complete" ? 0 : 1);
 if (position !== "complete") expect(current[0].textContent).toContain(position);
 if (index > 0) expect(container.textContent).toContain("1.0s");
});

test("lane detail has honest criteria, linked dependencies, attempts and lane history", () => {
 const { container } = render(<LaneDetail lane={{...lane, history:[{run_id:"old-run", item_id:"old-item", lane_id:"lane-1", state:"exited", started_at_ms:100, finished_at_ms:500}]}} item={itemData} now={9000} />);
 for (const heading of ["Pipeline", "Graph", "Criteria", "Waits on", "Unblocks", "Attempts on this item", "Lane history"]) expect(within(container).getByRole("heading", {name:heading})).toBeTruthy();
 expect(container.textContent).toContain("worker progress is not recorded");
 expect(within(container).getByRole("link", {name:"old-run"}).getAttribute("href")).toBe("#/run/old-run");
 expect(within(container).getByRole("link", {name:"old-item"})).toBeTruthy();
});

test("graph renderer accepts nodes and edges", () => {
 const {container} = render(<ItemGraph nodes={[{id:"a", label:"build", runId:"run-a", state:"running"}, {id:"b", label:"judge", runId:"run-b", state:"waiting"}]} edges={[{from:"a",to:"b"}]} />);
 expect(within(container).getByRole("link", {name:/build/})).toBeTruthy();
 expect(container.textContent).toContain("a → b");
});

test("scrolling up freezes follow, new entries resume it; tools are collapsed", () => {
 const {container, rerender} = render(<TranscriptView runId="r" transcript={transcriptData} error={null} />);
 const pane = container.querySelector('.transcript__entries')!;
 Object.defineProperties(pane, {scrollHeight:{value:1000, configurable:true}, clientHeight:{value:200, configurable:true}, scrollTop:{value:100, writable:true, configurable:true}});
 fireEvent.scroll(pane);
 const next = {...transcriptData, entries:[...transcriptData.entries, {seq:999, source:"log", kind:"message", role:"assistant", at_ms:1000, text:"**fresh** entry", bytes:15, truncated:false, epistemic_state:"observed" as const}]};
 rerender(<TranscriptView runId="r" transcript={next} error={null} />);
 expect(container.textContent).not.toContain("fresh");
 fireEvent.click(within(container).getByRole("button", {name:/new entries/}));
 expect(container.querySelector("strong")?.textContent || container.textContent).toBeTruthy();
 expect(container.querySelector('.transcript__markdown strong')?.textContent).toBe("fresh");
 expect(container.querySelector('details.transcript-entry')?.hasAttribute("open")).toBe(false);
});
