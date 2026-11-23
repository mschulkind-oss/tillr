import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { LoopSummary } from "../src/components/Home";

afterEach(cleanup);

// 2026-11-22: a snapshot read the browser had cancelled left the header saying "NOT KNOWN ·
// no loop is running · No session recorded" while a loop was running. A failed read is a
// failure, not a fact about the loop.
test("a failed snapshot read is drawn as a failure, never as no loop or no session", () => {
  const { container } = render(<LoopSummary drain={undefined} readError={new Error("the store could not be read: database is locked")} />);
  const text = container.textContent ?? "";
  expect(text).toContain("could not read the store: the store could not be read: database is locked");
  expect(text).not.toContain("no loop is running");
  expect(text).not.toContain("No session recorded");
  expect(text).not.toContain("not recorded");
});

test("before the first read answers, the summary says it is reading, not that no loop runs", () => {
  const { container } = render(<LoopSummary drain={undefined} />);
  const text = container.textContent ?? "";
  expect(text).toContain("Reading the store");
  expect(text).not.toContain("no loop is running");
});
