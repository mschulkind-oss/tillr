import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { LanePage } from "../src/components/LanePage";
import { useRunTranscriptQuery } from "../src/models/queries";
import { TranscriptView } from "../src/components/TranscriptView";
import { envelope, transcriptData } from "./fixtures";
import type { LaneRow, TranscriptEntry } from "../src/models/types";

afterEach(()=>{cleanup();vi.useRealTimers();vi.unstubAllGlobals();});
const entry=(i:number):TranscriptEntry=>({id:`log:${i}:0`,seq:i,source:"log",kind:"message",role:"assistant",at_ms:i+1000,text:`message-${i}`,bytes:12,truncated:false,epistemic_state:"observed"});
const lane:LaneRow={lane_id:"lane-1",state:"running",run_id:"run-first",item_id:"",backend:"pi",seat:"builder",attempt:1,outcome:"",state_since_ms:1000,last_step:"",last_step_at_ms:null,log_grew_at_ms:null,latest_action:{kind:"unrecorded",text:"",at_ms:null,clock:"no_clock"}};

test("lane transcript moves to the running node without replacing the page",async()=>{
 vi.useFakeTimers();
 const requests:string[]=[];
 vi.stubGlobal("fetch",async(url:string)=>{requests.push(url);return new Response(JSON.stringify(envelope({...transcriptData,entries:[{...entry(0),text:url.includes("run-second")?"second node text":"first node text"}],has_more:false})));});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const view=render(<QueryClientProvider client={client}><LanePage id="lane-1" lanes={[{...lane,nodes:[{node_id:"first",run_id:"run-first",attempt:1,state:"running"}]}]} error={null} live={false} paused={false}/></QueryClientProvider>);
 await act(async()=>{await vi.advanceTimersByTimeAsync(10)});
 expect(view.container.textContent).toContain("first node text");
 view.rerender(<QueryClientProvider client={client}><LanePage id="lane-1" lanes={[{...lane,nodes:[{node_id:"first",run_id:"run-first",attempt:1,state:"passed"},{node_id:"second",run_id:"run-second",attempt:1,state:"running"}]}]} error={null} live={false} paused={false}/></QueryClientProvider>);
 await act(async()=>{await vi.advanceTimersByTimeAsync(10)});
 expect(view.container.textContent).toContain("second node text");
 expect(view.container.textContent).not.toContain("first node text");
 expect(requests.some(url=>url.includes("runs/run-second/transcript"))).toBe(true);
 client.clear();
});

test.each([100,200])("forward polling exhausts %i appended entries while paused and resume retains every loaded entry",async(appendCount)=>{
 vi.useFakeTimers(); let appended=false;
 vi.stubGlobal("fetch",async(url:string)=>{
  const params=new URL(url,"http://fixture").searchParams;
  const after=params.get("after"); const before=params.get("before");
  const entries=before?Array.from({length:100},(_,i)=>entry(i)):!after?Array.from({length:100},(_,i)=>entry(i+100)):!appended?[]:after==="200"?Array.from({length:100},(_,i)=>entry(i+200)):after==="300" && appendCount===200?Array.from({length:100},(_,i)=>entry(i+300)):[];
  const cursor=entries.length?String(entries.at(-1)!.seq+1):after ?? "200";
  return new Response(JSON.stringify(envelope({...transcriptData,entries,cursor,before:before?null:"100",// The server reports a full page as possibly having more, even at EOF.
   has_more:appended && entries.length===100})));
 });
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 function Probe(){const q=useRunTranscriptQuery("r",false);return <TranscriptView runId="r" transcript={q.data?.data} error={q.error}/>;}
 const {container}=render(<QueryClientProvider client={client}><Probe/></QueryClientProvider>);
 await act(async()=>{await vi.advanceTimersByTimeAsync(10)});
 await act(async()=>{fireEvent.click(within(container).getByRole("button",{name:/earlier/}));await vi.advanceTimersByTimeAsync(10)});
 appended=true;
 await act(async()=>{await vi.advanceTimersByTimeAsync(1100)});
 expect(container.textContent).not.toContain(`message-${199+appendCount}`);
 fireEvent.click(within(container).getByRole("button",{name:/new entries/}));
 const texts=[...container.querySelectorAll(".transcript__markdown")].map(node=>node.textContent);
 expect(texts).toEqual(Array.from({length:200+appendCount},(_,i)=>`message-${i}`));
 client.clear();
});
