import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { DesignerCard, CommentThreads, DesignsPage } from "../src/components/Designers";
import type { DesignerView, CommentItemView } from "../src/models/types";

const designer: DesignerView = { session_id: "session-123", state: "warm", warm_until: "2026-11-17T05:00:00Z", last_request: "2026-11-17T04:00:00Z", context_tokens: 4200, cache_lifetime_seconds: 3600, compacted: false, batches: [{ item_id: "batch-one", run_id: "run-one", reason: "resumed-warm", cost_usd: 0.42 }], cost_usd: 0.42, missing_costs: 0 };
describe("designer observatory", () => {
 it("shows the session, warmth, context, batches and total cost", () => {
  render(<DesignerCard designer={designer} />);
  for (const text of ["session-123", "warm until 2026-11-17T05:00:00Z", "4,200 tokens", "$0.4200"]) expect(screen.getAllByText(text).length).toBeGreaterThan(0);
  expect(screen.getByRole("link", {name:"batch-one"}).getAttribute("href")).toContain("item/batch-one");
 });
 it("does not invent measurements", () => {
  render(<DesignerCard designer={{...designer,state:"compacted",warm_until:null,context_tokens:null,cost_usd:null,missing_costs:1}} />);
  expect(screen.getByText("compacted")).toBeTruthy();
  expect(screen.getByText("context size not recorded")).toBeTruthy();
  expect(screen.getByText(/summed cost not recorded/)).toBeTruthy();
 });
 it("shows comments, replies, reasons and lack of confirmation", () => {
  const comments: CommentItemView = { document:"docs/design.md", runs:designer.batches, confirmation_note:"replies are not being confirmed because no Vantage server is set", comments:[{id:"c1",comment:"Why this shape?",reactions:[{actor:"reviewer",summary:"Please explain"}],replies:[{id:"c1",run_id:"run-one",round:1,summary:"See Shape",state:"written"}]}] };
  render(<CommentThreads data={comments} />);
  for (const text of ["Why this shape?","See Shape","written","resumed-warm",comments.confirmation_note]) expect(screen.getByText(text)).toBeTruthy();
 });
 it("shows refresh measurements and links to each design", () => {
  render(<DesignsPage data={{designs:[{path:"docs/design.md",last_designer:designer}],arrivals:{within_lifetime:2,after_lifetime:1,no_session:1,unmeasured:0}}} error={null} pending={false} onRetry={() => {}} />);
  expect(screen.getByText(/within the cache lifetime: 2/)).toBeTruthy();
  expect(screen.getByText(/after it: 1/)).toBeTruthy();
  expect(screen.getByText(/no session to resume: 1/)).toBeTruthy();
  expect(screen.getByRole("link",{name:"docs/design.md"}).getAttribute("href")).toContain("design/");
 });
});
