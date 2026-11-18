import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ItemsView } from "../src/components/ItemsPasses";

afterEach(() => { cleanup(); vi.useRealTimers(); });
it("reads the pending hand move's age and recorded status from the waiting receipt", () => {
  vi.useFakeTimers(); vi.setSystemTime(1_000_000);
  const pending = { recorded: false, hand_event_seq: 7, recorded_event_seq: 0, changed_at_ms: 160_000 };
  const view = render(<ItemsView waiting={[]} orderRecording={pending} />);
  expect(screen.getByText("order changed 14m ago, not yet recorded")).toBeTruthy();
  view.rerender(<ItemsView waiting={[]} orderRecording={{ ...pending, recorded: true, recorded_event_seq: 7 }} />);
  expect(screen.getByText("order recorded")).toBeTruthy();
  view.rerender(<ItemsView waiting={[]} />);
  expect(screen.queryByText("order recorded")).toBeNull();
});
