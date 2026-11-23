import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "../src/App";
import { envelope, snapshotData } from "./fixtures";
import type { ItemData, ItemView } from "../src/models/types";

class SilentSocket { close() {} }
afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });
const item = (id: string, blocked_by: string[]): ItemView => ({...snapshotData.items[0], item_id:id, title:id, latest_run:null, blocked_by});

test("graph route restores URL selection and reacts to navigation and live data", async () => {
  window.history.replaceState(null, "", "/graph?item=b");
  vi.stubGlobal("WebSocket", SilentSocket);
  let items = [item("a", []), item("b", ["a"]), item("c", ["b"]), item("unrelated", [])];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path === "/api/v1/snapshot") return new Response(JSON.stringify(envelope({...snapshotData, items})));
    if (path === "/api/v1/why") return new Response(JSON.stringify(envelope({rows: items.map(i => ({item_id:i.item_id, class:"ready"})), summary:{}})));
    if (path === "/api/v1/passes") return new Response(JSON.stringify(envelope([])));
    throw new Error(`Unexpected route ${path}`);
  }));
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  const {container} = render(<QueryClientProvider client={client}><App /></QueryClientProvider>);
  await screen.findByRole("link", {name:"Select b"});
  expect(screen.getByRole("link", {name:"Graph"}).getAttribute("aria-current")).toBe("page");
  expect(container.querySelector('[data-item="b"]')?.getAttribute("data-selected")).toBe("true");
  expect(container.querySelector('[data-item="unrelated"]')?.getAttribute("data-dimmed")).toBe("true");
  window.history.replaceState(null, "", "/graph?item=a");
  window.dispatchEvent(new Event("popstate"));
  await waitFor(() => expect(container.querySelector('[data-item="a"]')?.getAttribute("data-selected")).toBe("true"));
  items = [...items, item("new", ["c"])];
  await client.invalidateQueries();
  await screen.findByRole("link", {name:"Select new"});
  expect(container.querySelector('[data-item="new"]')?.getAttribute("data-dimmed")).toBe("false");
  client.clear();
});

test("Item page waits-on and unblocks link into selected graph nodes", async () => {
  window.history.replaceState(null, "", "/item/current");
  vi.stubGlobal("WebSocket", SilentSocket);
  const data: ItemData = {item:{item_id:"current",title:"Current",body_ref:"",design_doc:"",allowed_paths:[],projected_state:{state:"blocked",raw_state:"blocked"}}, blocked_by:["before"],unblocks:["after"],criteria:[],closed_blockers:[],verdicts:[],decisions:[],attempts:[],runs:[],owner_decisions:[],state_moves:[]};
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path === "/api/v1/snapshot") return new Response(JSON.stringify(envelope(snapshotData)));
    if (path === "/api/v1/passes") return new Response(JSON.stringify(envelope([])));
    if (path === "/api/v1/items/current") return new Response(JSON.stringify(envelope(data)));
    return new Response(JSON.stringify({error:{code:"not-found",message:"No document"}}), {status:404});
  }));
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  render(<QueryClientProvider client={client}><App /></QueryClientProvider>);
  expect((await screen.findByRole("link", {name:"Waits on"})).getAttribute("href")).toBe("/graph?item=current");
  expect(screen.getByRole("link", {name:"before"}).getAttribute("href")).toBe("/graph?item=before");
  expect(screen.getByRole("link", {name:"after"}).getAttribute("href")).toBe("/graph?item=after");
  client.clear();
});
