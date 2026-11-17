import { afterEach, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { UsageSummary, RunUsageFigures } from "../src/components/Usage";
import { SpanViewer } from "../src/components/SpanViewer";

afterEach(cleanup);
it("draws one recorded and one unrecorded run as a lower bound and opens the model breakdown", () => {
 const view = render(<UsageSummary usage={{tokens:100,unrecorded_runs:1,lower_bound:true,models:[{backend:"pi",model:"model-a",input:30,output:20,cache_read:50,cache_write:0}]}} />);
 expect(screen.getByText("≥ 100 tokens · 1 unrecorded runs")).toBeTruthy();
 const summary = screen.getByText("Usage by model and backend");
 fireEvent.click(summary);
 expect(view.container.textContent).toContain("pi · model-a: input 30 · output 20 · cache read 50 · cache write 0");
 expect(view.container.textContent).not.toMatch(/[$€£¥]/);
});
it("shows measured zero separately from absent kinds and clocks", () => {
 const view = render(<RunUsageFigures usage={{backend:"claude",tokens:0,models:[{backend:"claude",model:"m",input:0,output:0}],turns:[]}} />);
 expect(view.container.textContent).toContain("input 0 · output 0 · cache read unrecorded · cache write unrecorded");
 expect(view.container.textContent).toContain("Time to first output: unrecorded · Output rate: unrecorded");
});
it("puts first output and throughput on each turn row", () => {
 const view = render(<SpanViewer spans={[{id:"t",parent:null,name:"turn t",start:1000,end:2200,state:"observed",reason:"",attributes:{category:"llm_turn",first_output_ms:200,output_tokens_per_second:20}}]} />);
 expect(view.container.textContent).toContain("First output 200.0 ms · 20.0 tokens/s");
});
