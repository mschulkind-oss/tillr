import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { VerifyRefusals } from "../src/components/ItemRunPages";
import type { VerifyRefusalView } from "../src/models/types";

afterEach(cleanup);

test("refusals display findings on their own rows and leave legacy refusals unclassified", () => {
  const base = { event_seq: 1, at_ms: 1000, run_id: "verify-1", attempt: 1, detail: "original reason", replan: false,
    next_run_id: null, next_attempt: null, next_step: null, next_seat: null, next_event_seq: null, next_at_ms: null };
  const refusals: VerifyRefusalView[] = [{ ...base, findings: [
    { criterion: "c1", kind: "gap", case: "Missing case.", paths: ["file:a.go:4", "file:b.go"] },
    { criterion: "c1", kind: "unasked-change", case: "Extra case.", paths: ["file:d.go"] },
  ] }, { ...base, event_seq: 2, run_id: "compat-1", detail: "compatibility refusal", findings: [
    { criterion: "c3", kind: "regression", case: "Compatibility case.", paths: ["file:g.go:5"] },
  ] }, { ...base, event_seq: 3, run_id: "blocked-1", detail: "blocked for planning", findings: [
    { criterion: "c4", kind: "regression", case: "Blocked review case.", paths: ["file:h.go:6"] },
  ] }, { ...base, event_seq: 4, run_id: "verify-old", detail: "old reason" }];
  render(<VerifyRefusals refusals={refusals} />);
  const list = within(screen.getByRole("region", { name: "Verify refusals" })).getAllByRole("list")[0];
  expect(Array.from(list.children, row => row.querySelector("pre")?.textContent)).toEqual([
    "original reason", "compatibility refusal", "blocked for planning", "old reason",
  ]);
  expect(Array.from(list.children, row => {
    const findings = within(row as HTMLElement).queryByLabelText("Findings");
    return findings && Array.from(findings.children, finding => finding.textContent);
  })).toEqual([
    ["c1 · Gap: Missing case. · file:a.go:4, file:b.go", "c1 · Unasked change: Extra case. · file:d.go"],
    ["c3 · Regression: Compatibility case. · file:g.go:5"],
    ["c4 · Regression: Blocked review case. · file:h.go:6"],
    null,
  ]);
});

test("refusals display earlier finding fates, examined paths, and comparison deltas", () => {
  const base = { event_seq: 1, at_ms: 1000, run_id: "verify-1", attempt: 1, detail: "reason", replan: false,
    next_run_id: null, next_attempt: null, next_step: null, next_seat: null, next_event_seq: null, next_at_ms: null };
  const refusals: VerifyRefusalView[] = [
    {
      ...base,
      examined: { whole: true },
    },
    {
      ...base,
      event_seq: 2,
      run_id: "verify-2",
      examined: { whole: false, unexamined_paths: ["file:slow.go", "file:docs/guide.md"] },
      earlier_finding_fates: [
        { criterion_id: "c1", finding: "Missing case.", paths: ["file:a.go:4"], fate: "fixed" },
        { finding: "Retry drops request.", fate: "not-fixed" },
      ],
      verdict_comparison: {
        previous_run_id: "verify-1",
        all_earlier_fixed: false,
        new_finding_at_new_place: true,
        new_finding_after_fix: false,
        files_delta: 3,
        added_lines_delta: -10,
        repair_growing: false,
        has_unexamined_paths: true,
      },
    },
  ];
  render(<VerifyRefusals refusals={refusals} />);
  expect(screen.getByText("Examined: whole candidate")).toBeTruthy();
  expect(screen.getByText(/Unexamined paths:/)).toBeTruthy();
  expect(screen.getByText("file:slow.go")).toBeTruthy();
  expect(screen.getByText("file:docs/guide.md")).toBeTruthy();
  expect(screen.getByLabelText("Verdict comparison").textContent).toContain("+3 files, -10 lines");
  expect(screen.getByLabelText("Verdict comparison").textContent).toContain("unexamined paths (S4 advisory)");
  const fatesList = screen.getByLabelText("Earlier finding fates");
  expect(Array.from(fatesList.children, li => li.textContent)).toEqual([
    "c1 · Fixed: Missing case. · file:a.go:4",
    "Not fixed: Retry drops request.",
  ]);
});
