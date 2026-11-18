import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { NewIdea } from "../src/components/NewIdea";
import { snapshotData, envelope, dispatchedItemData } from "./fixtures";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ItemPage } from "../src/components/ItemRunPages";
import { ItemsView } from "../src/components/ItemsPasses";
import { ObservatoryShell } from "../src/components/ObservatoryShell";

const items = snapshotData.items;

test.each(["#/", "#/items", "#/item/one", "#/lane/one", "#/questions", "#/trunk"])("every page's shared header exposes New idea: %s", hash => {
  const snapshot = { ...envelope(snapshotData), snapshot: { ...envelope(snapshotData).snapshot, writes_enabled: true } };
  render(<ObservatoryShell hash={hash} snapshot={snapshot} snapshotError={null} live={false} paused={false} onPause={() => {}} onRefresh={() => {}}>Page</ObservatoryShell>);
  expect(screen.getByRole("button", { name: "New idea" })).toBeTruthy();
});

test("idea row carries a chip and Item page renders full verbatim words and the gap", async () => {
  window.history.replaceState(null, "", "/#/items?state=unlanded");
  const id = items[0].item_id;
  const list = render(<ItemsView snapshot={{ ...snapshotData, items: [{ ...items[0], kind: "idea", title: "First words" }] }} waiting={[{ item_id: id, place: 1, class: "ready", where: "queue", why: "ready", mover: "loop", since: "today", class_source: "rule" }]} />);
  expect(screen.getByText("idea")).toBeTruthy();
  expect(screen.getByText("First words")).toBeTruthy();
  list.unmount();
  const words = "Exact words\n  whitespace and <not markup> 雪\n";
  vi.stubGlobal("fetch", vi.fn(async (path: string) => new Response(JSON.stringify(path.endsWith("/doc") ? { error: { code: "no-design-doc", message: "No design document" } } : envelope({ ...dispatchedItemData, idea: { words, words_path: "docs/ideas/one.txt", in_tree: false } })), { status: path.endsWith("/doc") ? 404 : 200 })));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><ItemPage id={id} live={false} paused baseURL="" /></QueryClientProvider>);
  const region = await screen.findByRole("region", { name: "Idea words" });
  expect(region.querySelector("pre")?.textContent).toBe(words);
  expect(region.textContent).toContain("not yet in the tree");
  client.clear();
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); localStorage.clear(); window.history.replaceState(null, "", "/"); });

test("idea draft survives reload and a refusal, and is cleared only by confirmed save", async () => {
  const fetch = vi.fn().mockResolvedValueOnce({ ok: false, status: 409, json: async () => ({ error: { code: "intake-not-configured", message: "Set intake paths" } }) })
    .mockResolvedValueOnce({ ok: true, json: async () => ({ data: { item_id: "item-idea-abc", place: 1 } }) });
  vi.stubGlobal("fetch", fetch);
  const saved = vi.fn();
  const first = render(<NewIdea enabled items={items} store="fixture" onSaved={saved} />);
  fireEvent.keyDown(window, { key: "n" });
  fireEvent.change(screen.getByRole("textbox", { name: "The words" }), { target: { value: "Exact words\n 雪" } });
  fireEvent.click(screen.getByRole("radio", { name: "Bottom" }));
  first.unmount();
  const reload = render(<NewIdea enabled items={items} store="fixture" onSaved={saved} />);
  fireEvent.click(screen.getByRole("button", { name: "New idea" }));
  expect((screen.getByRole("textbox", { name: "The words" }) as HTMLTextAreaElement).value).toBe("Exact words\n 雪");
  expect((screen.getByRole("radio", { name: "Bottom" }) as HTMLInputElement).checked).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await screen.findByText(/Set intake paths/);
  expect(screen.getByRole("dialog", { name: "New idea" })).toBeTruthy();
  expect(localStorage.getItem("tillr.idea:fixture")).toContain("Exact words");
  fireEvent.keyDown(screen.getByRole("textbox", { name: "The words" }), { key: "Enter", ctrlKey: true });
  await waitFor(() => expect(saved).toHaveBeenCalledWith("item-idea-abc"));
  expect(localStorage.getItem("tillr.idea:fixture")).toBeNull();
  expect(fetch.mock.calls[0][0]).toBe("/api/v1/ideas");
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ words: "Exact words\n 雪", place: { to: "last" } });
  reload.unmount();
  render(<NewIdea enabled items={items} store="fixture" onSaved={saved} />);
  fireEvent.keyDown(window, { key: "n" });
  expect((screen.getByRole("textbox", { name: "The words" }) as HTMLTextAreaElement).value).toBe("");
  expect((screen.getByRole("radio", { name: "Top" }) as HTMLInputElement).checked).toBe(true);
});

test("one tab's confirmed save cannot delete another tab's newer draft", async () => {
  let finish!: (result: unknown) => void;
  vi.stubGlobal("fetch", vi.fn(() => new Promise(resolve => { finish = resolve; })));
  const view = render(<NewIdea enabled items={items} store="fixture" onSaved={() => {}} />);
  fireEvent.keyDown(window, { key: "n" });
  fireEvent.change(screen.getByRole("textbox", { name: "The words" }), { target: { value: "Tab A words" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  const newer = { words: "Tab B's newer unsaved words", place: "bottom", after: "" };
  localStorage.setItem("tillr.idea:fixture", JSON.stringify(newer));
  finish({ ok: true, json: async () => ({ data: { item_id: "saved-a", place: 1 } }) });
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(JSON.parse(localStorage.getItem("tillr.idea:fixture")!)).toEqual(newer);
  view.unmount();
  render(<NewIdea enabled items={items} store="fixture" onSaved={() => {}} />);
  fireEvent.keyDown(window, { key: "n" });
  expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "The words" }).value).toBe(newer.words);
});

test("pending saving keeps modal focus inside the dialog", async () => {
  let finish!: (result: unknown) => void;
  vi.stubGlobal("fetch", vi.fn(() => new Promise(resolve => { finish = resolve; })));
  render(<NewIdea enabled items={items} store="fixture" onSaved={() => {}} />);
  fireEvent.keyDown(window, { key: "n" });
  const words = screen.getByRole("textbox", { name: "The words" });
  fireEvent.change(words, { target: { value: "Pending words" } });
  fireEvent.keyDown(words, { key: "Enter", ctrlKey: true });
  const dialog = screen.getByRole("dialog");
  expect(document.activeElement).toBe(dialog);
  expect(fireEvent.keyDown(dialog, { key: "Tab" })).toBe(false);
  expect(fireEvent.keyDown(dialog, { key: "Tab", shiftKey: true })).toBe(false);
  finish({ ok: false, json: async () => ({ error: { code: "store-busy", message: "try again" } }) });
  await screen.findByText(/try again/);
  await waitFor(() => expect(document.activeElement).toBe(words));
});

test("pending saving keeps the draft and suppresses repeated keyboard saves", async () => {
  let finish!: (result: unknown) => void;
  const fetch = vi.fn(() => new Promise(resolve => { finish = resolve; }));
  vi.stubGlobal("fetch", fetch);
  render(<NewIdea enabled items={items} store="fixture" onSaved={() => {}} />);
  fireEvent.keyDown(window, { key: "n" });
  const words = screen.getByRole("textbox", { name: "The words" });
  fireEvent.change(words, { target: { value: "Pending words" } });
  fireEvent.keyDown(words, { key: "Enter", ctrlKey: true });
  fireEvent.keyDown(words, { key: "Enter", ctrlKey: true });
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(localStorage.getItem("tillr.idea:fixture")).toContain("Pending words");
  finish({ ok: true, json: async () => ({ data: { item_id: "confirmed", place: 1 } }) });
  await waitFor(() => expect(localStorage.getItem("tillr.idea:fixture")).toBeNull());
});

test("read-only has no control or n shortcut, and n does not interrupt typing", () => {
  const view = render(<NewIdea enabled={false} items={items} store="fixture" onSaved={() => {}} />);
  fireEvent.keyDown(window, { key: "n" });
  expect(screen.queryByRole("button", { name: "New idea" })).toBeNull();
  expect(screen.queryByRole("dialog")).toBeNull();
  view.rerender(<><input aria-label="Other input" /><NewIdea enabled items={items} store="fixture" onSaved={() => {}} /></>);
  fireEvent.keyDown(screen.getByRole("textbox", { name: "Other input" }), { key: "n" });
  expect(screen.queryByRole("dialog")).toBeNull();
});

test("After saves the named item; malformed success cannot clear the draft", async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: {} }) });
  vi.stubGlobal("fetch", fetch);
  const ordered = [{ ...items[0], item_id: "second", place: 2 }, { ...items[0], item_id: "parked", place: undefined }, { ...items[0], item_id: "first", place: 1 }];
  render(<NewIdea enabled items={ordered} store="fixture" onSaved={() => {}} />);
  fireEvent.keyDown(window, { key: "n" });
  fireEvent.change(screen.getByRole("textbox", { name: "The words" }), { target: { value: "An idea" } });
  fireEvent.click(screen.getByRole("radio", { name: "After…" }));
  const option = screen.getByRole<HTMLSelectElement>("combobox", { name: "After item" });
  expect(Array.from(option.options, entry => entry.value)).toEqual(["", "first", "second"]);
  fireEvent.change(option, { target: { value: "first" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await screen.findByRole("alert");
  expect(JSON.parse(fetch.mock.calls[0][1].body).place).toEqual({ after: option.options[1].value });
  expect(localStorage.getItem("tillr.idea:fixture")).toContain("An idea");
});
