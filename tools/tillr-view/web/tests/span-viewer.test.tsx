import { act, cleanup, fireEvent, render } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { SpanViewer } from "../src/components/SpanViewer";
import { assembleSpans, analyzeSpans, type ViewerSpan } from "../src/models/spans";
import type { GateData, GateSpan } from "../src/models/types";
import { runData, timelineData } from "./fixtures";

afterEach(() => { cleanup(); vi.useRealTimers(); window.history.replaceState(null, "", "/"); });
const span = (id: string, start: number | null, end: number | null, parent: string | null = null): ViewerSpan => ({ id, parent, name: id, start, end, state: "observed", attributes: { tool: id, item_id: "item-1", run_id: "run-1", pass_id: "pass-1", check_id: "check-1", landing_id: "landing-1" }, reason: "steps.jsonl missing" });
const spans = [span("run", 0, 100), span("turn", 10, 90, "run"), span("bash", 20, 80, "turn"), span("gap", null, null)];
test("uncovered time subtracts the union of clipped children, not their sum", () => {
 const analysis = analyzeSpans([span("p", 0, 100), span("a", -10, 60, "p"), span("b", 40, 110, "p")]);
 expect(analysis.self.get("p")).toBe(0);
 expect(analyzeSpans(spans).self.get("run")).toBe(20);
 expect(analyzeSpans(spans).critical).toEqual(new Set(["run"]));
 expect(analyzeSpans([span("run", 0, 100), span("turn", 10, 100, "run"), span("tool", 20, 100, "turn")]).critical).toEqual(new Set(["run", "turn", "tool"]));
});
test("missing steps keep explained dashed stages without invented bars", () => {
 const data = assembleSpans(runData, timelineData);
 const view = render(<SpanViewer spans={data} />);
 expect(view.container.querySelector('[data-span-id$="/stage"] .waterfall__bar')).toBeNull();
 expect(view.container.textContent).toContain("staging inputs are not preserved");
 expect(view.container.querySelector('[data-span-id$="/stage"]')?.className).toContain("span-row--missing");
 expect(view.container.querySelector('[data-span-id$="/run"] .waterfall__bar')).not.toBeNull();
});
test("hierarchy collapses, searching matches attributes, selection exposes every attribute and entity", () => {
 const view = render(<SpanViewer spans={spans} />);
 fireEvent.click(view.getByRole("button", { name: "Collapse run" }));
 expect(view.container.querySelector('[data-span-id="bash"]')).toBeNull();
 fireEvent.change(view.getByRole("searchbox"), { target: { value: "bash" } });
 expect(view.getByText("1 matched span")).toBeTruthy();
 fireEvent.click(view.getByRole("button", { name: "Select bash" }));
 expect(view.getByRole("region", { name: "Span details" }).textContent).toContain("Time not covered by children");
 for (const kind of ["item", "run", "pass", "check", "landing"]) expect(view.container.querySelector(`a[href^="#/${kind}/${kind}-1"]`)).not.toBeNull();
 expect(view.container.querySelector('a[href^="#/check/check-1"]')?.getAttribute("href")).toContain("span=bash");
 expect(window.location.hash).toContain("span=bash");
});
test("axis zooms and pans by mouse and keyboard and fit restores it", () => {
 const view = render(<SpanViewer spans={spans} />);
 const axis = view.getByRole("region", { name: "Span time axis" });
 const before = axis.getAttribute("data-window");
 fireEvent.wheel(axis, { deltaY: -100 });
 expect(axis.getAttribute("data-window")).not.toBe(before);
 const zoomed = axis.getAttribute("data-window");
 fireEvent.keyDown(axis, { key: "ArrowRight" });
 expect(axis.getAttribute("data-window")).not.toBe(zoomed);
 fireEvent.click(view.getByRole("button", { name: "Fit time axis" }));
 expect(axis.getAttribute("data-window")).toBe(before);
 fireEvent.keyDown(axis, { key: "+" });
 expect(axis.getAttribute("data-window")).not.toBe(before);
 fireEvent.mouseDown(axis, { clientX: 100 });
 fireEvent.mouseMove(axis, { clientX: 200 });
 fireEvent.mouseUp(axis);
 expect(axis.getAttribute("data-window")).not.toBe(before);
});
test("5000 spans render only the visible rows and a selected offscreen span scrolls into view", () => {
 const fixture = Array.from({ length: 5000 }, (_, i) => span(`span-${i}`, i, i + 1));
 window.history.replaceState(null, "", "/#/run/run-1?span=span-4999");
 const view = render(<SpanViewer spans={fixture} />);
 expect(view.container.querySelectorAll('[data-span-id]').length).toBeLessThan(30);
 expect(view.container.querySelector('[data-span-id="span-4999"]')).not.toBeNull();
 expect(view.getByRole("region", { name: "Span details" }).textContent).toContain("span-4999");
});

test("initial fit waits for the final timing face but does not override a reader zoom", () => {
 const first = [span("run", 1000, 2000)];
 const complete = [...first, span("gate", 2100, 3000)];
 const view = render(<SpanViewer spans={first} initialComplete={false} />);
 view.rerender(<SpanViewer spans={complete} initialComplete />);
 expect(view.getByRole("region", { name: "Span time axis" }).getAttribute("data-window")).toBe("1000,3000");
 view.unmount();
 const touched = render(<SpanViewer spans={first} initialComplete={false} />);
 fireEvent.click(touched.getByRole("button", { name: "Zoom in" }));
 const axis = touched.getByRole("region", { name: "Span time axis" }).getAttribute("data-window");
 touched.rerender(<SpanViewer spans={complete} initialComplete />);
 expect(touched.getByRole("region", { name: "Span time axis" }).getAttribute("data-window")).toBe(axis);
});
test("a refresh cannot reset scroll, erase search, or expand a deliberately collapsed subtree", () => {
 window.history.replaceState(null, "", "/#/run/run-1?span=turn");
 const fixture = [...spans, ...Array.from({length:100}, (_,i) => span(`s${i}`, i, i + 1))];
 const view = render(<SpanViewer spans={fixture} />);
 const viewport = view.getByRole("tree");
 fireEvent.scroll(viewport, { target: { scrollTop: 1800 } });
 view.rerender(<SpanViewer spans={[...fixture]} />);
 expect(viewport.scrollTop).toBe(1800);
 fireEvent.change(view.getByRole("searchbox"), { target: { value: "s50" } });
 view.rerender(<SpanViewer spans={[...fixture]} />);
 expect((view.getByRole("searchbox") as HTMLInputElement).value).toBe("s50");
 fireEvent.change(view.getByRole("searchbox"), { target: { value: "" } });
 fireEvent.click(view.getByRole("button", { name: "Collapse run" }));
 view.rerender(<SpanViewer spans={[...fixture]} />);
 expect(view.container.querySelector('[data-span-id="turn"]')).toBeNull();
});
test("an open run grows on the viewer clock at a fixed scale and freezes when paused", () => {
 vi.useFakeTimers(); vi.setSystemTime(2000);
 const open = { ...span("run",1000,null), open:true };
 const view = render(<SpanViewer spans={[open]} />);
 const bar = () => view.container.querySelector('[data-clock="viewer"]') as HTMLElement;
 const before = bar().style.width, axis = view.getByRole("region", {name:"Span time axis"}).getAttribute("data-window");
 act(() => vi.advanceTimersByTime(1000));
 expect(bar().style.width).not.toBe(before);
 expect(bar().title).toContain("so far");
 expect(view.getByRole("region", {name:"Span time axis"}).getAttribute("data-window")).toBe(axis);
 view.rerender(<SpanViewer spans={[open]} paused />);
 const frozen = bar().style.width;
 act(() => vi.advanceTimersByTime(10000));
 expect(bar().style.width).toBe(frozen);
});

test("the actual timeline plus gate response shape has one plan and correct uncovered time", () => {
 const plan: GateSpan = { span_id:"source-plan", parent_span_id:"", trace_id:"t", check_id:"plan", kind:"plan", label:"plan", status:"ok", start_unix_nano:"0", end_unix_nano:"0", started_at_ms:1000, finished_at_ms:2000, waited_ms_json:"{}", attributes_json:"{}", epistemic_state:"observed" };
 const check = { ...plan, span_id:"source-check", parent_span_id:plan.span_id, check_id:"test", kind:"check", label:"check" };
 const gate: GateData = { run:runData.run.run_id, verdict_id:"v", plan_span:plan, spans:[check], skipped_checks:[], sources:timelineData.sources };
 const timeline = { ...timelineData, spans: [...timelineData.spans, ...[plan,check].map(s => ({...timelineData.spans[0], span_id:s.span_id, parent_span_id:s.parent_span_id || null, category:"gate_check", label:s.label, started_at_ms:s.started_at_ms, finished_at_ms:s.finished_at_ms}))] };
 const combined = assembleSpans(runData,timeline,gate);
 expect(combined.filter(s => s.name === "plan")).toHaveLength(1);
 expect(combined.find(s => s.id === "source-plan")).toBeDefined();
 expect(analyzeSpans(combined).self.get("source-plan")).toBe(0);
 expect(combined.find(s => s.id === "source-check")?.parent).toBe("source-plan");
 window.history.replaceState(null,"",`/#/run/${runData.run.run_id}?span=${encodeURIComponent(runData.run.run_id + "/gate")}`);
 const view = render(<SpanViewer spans={combined} />);
 expect(view.getByRole("region",{name:"Span details"}).textContent).toContain("plan");
});
