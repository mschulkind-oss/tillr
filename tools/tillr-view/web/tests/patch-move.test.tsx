import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ItemsView } from "../src/components/ItemsPasses";
import type { WaitingRow } from "../src/models/types";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });

test("publication observations offer acknowledgment, not item links or controls", () => {
  window.history.replaceState(null, "", "/#/items");
  const fetch = vi.fn(); vi.stubGlobal("fetch", fetch);
  const command = "tillr drain acknowledge-patch-move --store /repo/.tillr/state.db --observation 123 --fork pi --by <owner>";
  const observation: WaitingRow = {
    item_id: "", class: "needs you", where: "publication",
    why: "patch/pi observed by run observing-run: old -> new; publication held",
    mover: command, since: "today", class_source: "store",
    ...{ observation: "patch/pi observation 123" },
  };
  render(<ItemsView waiting={[observation]} writesEnabled />);
  const row = screen.getByText(observation.why, { selector: "td" }).closest("tr")!;
  expect(within(row).getByText("patch/pi observation 123")).toBeTruthy();
  expect(within(row).getByText(command)).toBeTruthy();
  expect(within(row).queryAllByRole("button")).toHaveLength(0);
  expect(within(row).queryAllByRole("link")).toHaveLength(0);
  fireEvent.focus(row); fireEvent.keyDown(row, { key: "J" });
  fireEvent.keyDown(row, { key: "Enter" });
  expect(window.location.hash).toBe("#/items");
  expect(fetch).not.toHaveBeenCalled();
});
