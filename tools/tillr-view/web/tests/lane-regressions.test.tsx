import { act, cleanup, fireEvent, render, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { LaneDetail } from "../src/components/LanePage";
import { TranscriptView } from "../src/components/TranscriptView";
import { envelope, dispatchedItemData as itemData, transcriptData } from "./fixtures";
import type { LaneRow, TranscriptEntry } from "../src/models/types";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
const lane: LaneRow = {lane_id:"lane-1", state:"running", run_id:"run-b", item_id:itemData.item.item_id, backend:"pi", seat:"builder", attempt:1, outcome:"", state_since_ms:1000, last_step:"", last_step_at_ms:null, log_grew_at_ms:null, latest_action:{kind:"unrecorded",text:"",at_ms:null,clock:"no_clock"}};
const entry = (id: string, seq: number, text=id): TranscriptEntry => ({id,seq,source:"log",kind:"message",role:"assistant",at_ms:1000+seq,text,bytes:text.length,truncated:false,epistemic_state:"observed"});

test("two workflow nodes are one attempt, ordered and linked", () => {
 const {container} = render(<LaneDetail lane={{...lane,workflow_ref:"build@v1",nodes:[{node_id:"scout",run_id:"run-a",attempt:1,state:"passed"},{node_id:"build",run_id:"run-b",attempt:1,state:"running"}]}} item={{...itemData,runs:[{...itemData.runs[0],run_id:"run-a"},{...itemData.runs[0],run_id:"run-b"}]}} now={9000} />);
 expect(container.textContent).toContain("attempt 1 of 1");
 expect(container.textContent).toContain("2 nodes · build@v1");
 expect(container.textContent).toContain("run order as recorded");
 expect(container.textContent).not.toContain("graphs are not built");
 const attempts = within(container).getByRole("heading",{name:"Attempts on this item"}).parentElement!;
 expect(attempts.querySelectorAll("ol > li")).toHaveLength(1);
 expect(container.querySelectorAll(".lane-graph li")[0].getAttribute("data-state")).toBe("passed");
 expect(container.querySelectorAll(".lane-graph li")[1].getAttribute("data-state")).toBe("running");
});

test("resume keeps older entries, the frozen tail and all newly loaded entries", async () => {
 vi.stubGlobal("fetch", async () => new Response(JSON.stringify(envelope({...transcriptData,entries:[entry("older",0)],before:null}))));
 const {container,rerender}=render(<TranscriptView runId="r" transcript={{...transcriptData,entries:[entry("middle",1)],before:"older-page"}} error={null} />);
 await act(async()=> {fireEvent.click(within(container).getByRole("button",{name:/earlier/}));});
 rerender(<TranscriptView runId="r" transcript={{...transcriptData,entries:[entry("new",2)],before:"later-page"}} error={null} />);
 fireEvent.click(within(container).getByRole("button",{name:/new entries/}));
 for(const text of ["older","middle","new"]) expect(container.textContent).toContain(text);
});

test("expanded body stays attached to its identity when merged records shift sequence", async () => {
 const long=entry("log:10:0",0,"preview"); long.truncated=true; long.bytes=10000;
 vi.stubGlobal("fetch",async()=>new Response(JSON.stringify(envelope({...transcriptData,entries:[{...long,text:"whole original message",truncated:false}]}))));
 const {container,rerender}=render(<TranscriptView runId="r" transcript={{...transcriptData,entries:[long]}} error={null} />);
 await act(async()=>{fireEvent.click(within(container).getByRole("button",{name:/read all/}));});
 rerender(<TranscriptView runId="r" transcript={{...transcriptData,entries:[entry("steps:0:0",0,"inserted step"),{...long,seq:1}]}} error={null} />);
 expect(container.textContent).toContain("inserted step");
 expect(container.textContent).toContain("whole original message");
 const articles=container.querySelectorAll("article");
 expect(articles[0].textContent).not.toContain("whole original message");
});
