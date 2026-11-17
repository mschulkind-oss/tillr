import { afterEach, expect, test } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ItemDetailPane } from "../src/components/ItemDetailPane";
import { blockedItemData } from "./fixtures";

afterEach(cleanup);

test("item page names the stored workflow and its source", () => {
  const item = { ...blockedItemData, item: { ...blockedItemData.item,
    selected_workflow: "build@v2", workflow_source: "default" } };
  render(<ItemDetailPane itemId={item.item.item_id} item={item}
    itemError={null} loading={false} onRetry={() => {}} latestRun={null}
    updatedAtMs={null} selectedRunId={null} onSelectRun={() => {}}
    doc={{ doc: undefined, error: null, pending: false, onRetry: () => {}, baseUrl: "" }} />);
  expect(screen.getByText("build@v2")).toBeTruthy();
  expect(screen.getByText("Source: default")).toBeTruthy();
});
