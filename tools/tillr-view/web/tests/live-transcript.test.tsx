import { appendFileSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { LanePage } from "../src/components/LanePage";
import { useRunTranscriptQuery } from "../src/models/queries";
import { TranscriptView } from "../src/components/TranscriptView";
import { envelope, transcriptData } from "./fixtures";
import type { LaneRow } from "../src/models/types";

afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });

// A fixture API reads an append-only stream on every request. The browser keeps
// the same mounted page, query client and follow component while the writer appends.
test.each(["lane", "run"])("%s transcript sees an appended fixture stream in under two seconds", async page => {
 vi.useFakeTimers();
 const dir = mkdtempSync(join(process.env.TMPDIR ?? join(process.cwd(), "tests"), "live-transcript-")), stream = join(dir, "run.log");
 writeFileSync(stream, "");
 const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
 vi.stubGlobal("fetch", async () => {
  const entries = readFileSync(stream, "utf8").split("\n").filter(Boolean).map(line => JSON.parse(line));
  return new Response(JSON.stringify(envelope({...transcriptData, entries})));
 });
 const lane: LaneRow = {lane_id:"lane-1", state:"running", run_id:"run-live", item_id:"", backend:"pi", seat:"builder", attempt:1, outcome:"", state_since_ms:1000, last_step:"", last_step_at_ms:null, log_grew_at_ms:1000, latest_action:{kind:"unrecorded", text:"", at_ms:null, clock:"no_clock"}};
 function RunTranscript() { const query = useRunTranscriptQuery("run-live", false); return <TranscriptView runId="run-live" transcript={query.data?.data} error={query.error} />; }
 try {
  const {container} = render(<QueryClientProvider client={client}>{page === "lane" ? <LanePage id="lane-1" lanes={[lane]} error={null} live={false} paused={false} /> : <RunTranscript />}</QueryClientProvider>);
  await act(async () => { await vi.advanceTimersByTimeAsync(10); });
  expect(container.textContent).not.toContain("stream grew");
  appendFileSync(stream, JSON.stringify({seq:0, source:"log", kind:"message", role:"assistant", at_ms:1000, text:"**stream grew**", bytes:14, truncated:false, epistemic_state:"observed"})+"\n");
  await act(async () => { await vi.advanceTimersByTimeAsync(1100); });
  expect(container.querySelector('.transcript__markdown strong')?.textContent).toBe("stream grew");
 } finally { client.clear(); rmSync(dir, {recursive:true}); }
});

test("Pause stops the transcript polling clock", async () => {
 vi.useFakeTimers();
 const fetch = vi.fn(async () => new Response(JSON.stringify(envelope(transcriptData))));
 vi.stubGlobal("fetch", fetch);
 const client = new QueryClient();
 function Probe({paused}:{paused:boolean}) { useRunTranscriptQuery("r", false, true, paused); return null; }
 const view = render(<QueryClientProvider client={client}><Probe paused={false} /></QueryClientProvider>);
 await act(async () => { await vi.advanceTimersByTimeAsync(10); });
 view.rerender(<QueryClientProvider client={client}><Probe paused /></QueryClientProvider>);
 const count = fetch.mock.calls.length;
 await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
 expect(fetch.mock.calls.length).toBe(count);
 client.clear();
});
