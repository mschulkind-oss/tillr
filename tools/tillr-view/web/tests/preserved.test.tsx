import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { PreservedWork } from "../src/components/PreservedWork";

afterEach(cleanup);

test("the owner sees the saved ref, summary and stopped reason", () => {
  render(<PreservedWork records={[{ ref: "refs/tillr/preserved/run-a", summary: "2 files, +3 -1 lines", reason: "reported RESULT: blocked" }]} />);
  expect(screen.getByText("refs/tillr/preserved/run-a").textContent).toBe("refs/tillr/preserved/run-a");
  expect(screen.getByRole("region", { name: "Earlier uncommitted work" }).textContent).toContain("2 files, +3 -1 lines");
  expect(screen.getByRole("region").textContent).toContain("reported RESULT: blocked");
  expect(screen.getByRole("region").textContent).toContain("not a candidate to gate or land");
});

test("different staged contents name their recoverable revision", () => {
  render(<PreservedWork records={[{ ref: "refs/tillr/preserved/run-a", summary: "staged index: 1 file", reason: "blocked", index_ref: "refs/tillr/preserved/run-a^2" }]} />);
  expect(screen.getByText("refs/tillr/preserved/run-a^2").textContent).toBe("refs/tillr/preserved/run-a^2");
});

test("a retained record does not claim a pruned ref still exists", () => {
  render(<PreservedWork records={[{ ref: "refs/tillr/preserved/run-a", summary: "1 file", reason: "orphaned", pruned: true }]} />);
  expect(screen.getByRole("region").textContent).toContain("ref was removed");
});
