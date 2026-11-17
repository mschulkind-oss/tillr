import { render, screen, cleanup } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { PassSpend, foldPasses } from "../src/components/ItemsPasses";
import { DesignerCard } from "../src/components/Designers";
import type { PassRow, SpendTotals, DesignerView } from "../src/models/types";

afterEach(cleanup);
const sum: SpendTotals = {cost_usd:.03,tokens:1500,runs:2,unreported:1,tokens_unreported:1,cost_unreported:1};
it("prints inherited spend separately and discloses unreported runs", () => {
 render(<PassSpend pass={{dispatched_at_ms:1,decisions:[],spend_started_in_loop:{...sum,cost_usd:0,tokens:0,runs:0,unreported:0,tokens_unreported:0,cost_unreported:0},spend_carried_over:sum}} />);
 expect(screen.getByText(/Spend started in this loop: 0 tokens/)).toBeTruthy();
 expect(screen.getByText(/Spend carried over from earlier loops: ≥ 1500 tokens · 1 unrecorded runs/)).toBeTruthy();
});
it("does not fold a pass with different spend into the same answer", () => {
 const pass: PassRow = {dispatched_at_ms:2,decisions:[],spend_carried_over:sum};
 expect(foldPasses([pass,{...pass,dispatched_at_ms:1,spend_carried_over:{...sum,cost_usd:.06}}])).toHaveLength(2);
});
it("never shows a designer price even when the fixture carries one", () => {
 const designer: DesignerView = {session_id:"s",state:"expired",warm_until:null,last_request:"",context_tokens:null,cache_lifetime_seconds:null,compacted:false,batches:[],cost_usd:.03,missing_costs:1};
 render(<DesignerCard designer={designer} />);
 expect(screen.queryByText(/\$/)).toBeNull();
 expect(screen.queryByText(/cost-unreported/)).toBeNull();
});
