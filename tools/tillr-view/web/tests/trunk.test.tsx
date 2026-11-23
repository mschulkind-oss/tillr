import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "../src/App";
import { invalidationKey } from "../src/models/socket";
import { queryKeys } from "../src/models/queries";
import { envelope, snapshotData } from "./fixtures";

const rows = [
  { kind: "landing", landing_id: "landing-a", item_id: "item-a", run_id: "run-a", pass_id: "pass-a", result: "squash-conflict", fix_run_id: "run-fix", trunk_before: "aaa", trunk_after: "aaa", at_ms: 1234, event_seq: 1, epistemic_state: "observed" },
  { kind: "acquittal", item_id: "item-b", run_id: "run-b", result: "trunk-already-red", fixer_item: "fix-red", red_sha: "red-commit", at_ms: 1500, event_seq: 2, epistemic_state: "observed" },
  { kind: "gap", trunk_before: "aaa", trunk_after: "ccc", at_ms: null, event_seq: 0, epistemic_state: "unobserved_seam" },
];
function setup(hash: string, fail = false) {
 window.history.replaceState(null, "", hash);
 vi.stubGlobal("WebSocket", undefined);
 const fetch = vi.fn(async (path: string) => {
  if (path === "/api/v1/passes") return new Response(JSON.stringify(envelope([])));
  if (path === "/api/v1/snapshot") return new Response(JSON.stringify(envelope(snapshotData)));
  if (path.startsWith("/api/v1/landings")) {
   if (fail) return new Response(JSON.stringify({error:{code:"table-absent",message:"landings table is missing"}}), {status:503});
   return new Response(JSON.stringify(envelope(path === "/api/v1/landings" ? {rows} : rows[0])));
  }
  throw new Error(`unexpected read: ${path}`);
 });
 vi.stubGlobal("fetch", fetch);
 const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
 const view = render(<QueryClientProvider client={client}><App /></QueryClientProvider>);
 return { ...view, fetch, client };
}
afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.history.replaceState(null,"","/"); });

test("landing invalidation reaches both the ledger and detail queries", () => {
 expect(invalidationKey("landings")).toEqual(queryKeys.landings());
 expect(queryKeys.landing("landing-a").slice(0,1)).toEqual(queryKeys.landings());
});

test("Trunk opens from its URL with repair links and an explicit untimed gap", async () => {
 const view = setup("/trunk");
 await view.findByRole("heading", {name:"Trunk"});
 await view.findByRole("link", {name:"landing-a"});
 expect(view.getByRole("link", {name:"Fix run run-fix"}).getAttribute("href")).toBe("/run/run-fix");
 expect(view.getByRole("link", {name:"Fixer item fix-red"}).getAttribute("href")).toBe("/item/fix-red");
 expect(view.getByRole("link", {name:"Red commit red-commit"}).getAttribute("href")).toContain("red-commit");
 expect(view.getByText(/Unrecorded trunk move/)).toBeTruthy();
 fireEvent.keyDown(window, {key:"j"});
 expect((window.location.pathname + window.location.search)).toBe("/trunk");
 expect(view.fetch.mock.calls.every(([path]) => !String(path).includes("/items/"))).toBe(true);
 fireEvent.click(view.getByRole("link", {name:"landing-a"}));
 await waitFor(() => expect((window.location.pathname + window.location.search)).toBe("/landing/landing-a"));
 await view.findByRole("heading", {name:"Landing landing-a"});
 view.client.clear();
});

test("a red commit link opens its recorded relationships", async () => {
 const view = setup("/trunk?sha=red-commit");
 await view.findByRole("link", {name:"Fixer item fix-red"});
 expect(view.getByRole("heading", {name:"Trunk commit red-commit"})).toBeTruthy();
 expect(view.queryByRole("link", {name:"landing-a"})).toBeNull();
 expect(view.getByText("git show red-commit")).toBeTruthy();
 view.client.clear();
});

test("a bookmarked landing links its item, run and pass without a default queue selection", async () => {
 const view=setup("/landing/landing-a");
 await view.findByRole("heading", {name:"Landing landing-a"});
 expect((await view.findByRole("link", {name:"Item item-a"})).getAttribute("href")).toBe("/item/item-a");
 expect(view.getByRole("link", {name:"Run run-a"}).getAttribute("href")).toBe("/run/run-a");
 expect(view.getByRole("link", {name:"Pass pass-a"}).getAttribute("href")).toBe("/pass/pass-a");
 view.client.clear();
});

test("an unread ledger is not an empty trunk", async () => {
 const view=setup("/trunk",true);
 await view.findByText(/landings table is missing/);
 expect(view.queryByText("No landings recorded.")).toBeNull();
 expect(view.getByRole("button",{name:"Read it again"})).toBeTruthy();
 view.client.clear();
});
