import { afterEach, expect, test, vi } from "vitest";
import { fetchPassHistory } from "../src/models/api";
import { envelope } from "./fixtures";
import type { PassRow } from "../src/models/types";
afterEach(() => vi.unstubAllGlobals());
const page: PassRow[] = Array.from({ length: 50 }, (_, index) => ({ dispatched_at_ms: 2000 - index, decisions: [] }));
test("bounded history follows the exclusive cursor past 50 rows to cover the range", async () => {
  const fetch = vi.fn(async (url: string) => new Response(JSON.stringify(envelope(url.includes("before=") ? [{ dispatched_at_ms: 1900, decisions: [{ item_id: "old-match", rank: "a", action: "hold", reason: "blocked" }] }] : page))));
  vi.stubGlobal("fetch", fetch);
  const result = await fetchPassHistory(1000, 1);
  expect(result.data.rows).toHaveLength(51);
  expect(fetch.mock.calls[1][0]).toBe("/api/v1/passes?since=1000&before=1951");
  expect(result.data.more).toBe(false);
});
test("all history exposes older pages rather than claiming the first 50 are complete", async () => {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => new Response(JSON.stringify(envelope(url.includes("before=") ? [] : page)))));
  expect((await fetchPassHistory(null, 1)).data.more).toBe(true);
  expect((await fetchPassHistory(null, 2)).data.more).toBe(false);
});
