import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { LoopBand } from "../src/components/ObservatoryHeaderBar";
import { LoopSummary } from "../src/components/Home";
import { snapshotData } from "./fixtures";

afterEach(cleanup);
const now = Date.now();
const session = { session_id: "s", started_at_ms: now - 5000, refreshed_at_ms: now, next_pass_at_ms: now + 1000, ended_at_ms: null };

test.each(["queue-drained", "lanes-leased", "operator-stop", "host-boundary-violation", "gate-harness-error", "fault"])("header uses the recorded explanation for %s", reason => {
  const stop = { reason, at_ms: now - 1000, item_id: "item-a", step: "gate", sentence: `Recorded explanation for ${reason}.`, next: "Owner repairs the gate, then restarts with tillr-drain loop." };
  const drain = { ...snapshotData.drain_summary, session: { ...session, ended_at_ms: now - 1000 }, stop };
  const { container } = render(<><LoopBand drain={drain} now={now} /><LoopSummary drain={drain} now={now} /></>);
  for (const header of container.querySelectorAll(".loop-band, .loop-summary__state")) {
    expect(header.textContent).toContain("stopped since");
    for (const value of [new Date(stop.at_ms).toLocaleString(), stop.item_id, stop.step, stop.sentence, stop.next]) expect(header.textContent).toContain(value);
    expect(header.textContent).not.toMatch(/diagnose the recorded|inspect the recorded stop/);
  }
});

test("a startup stop without a session is still labeled stopped", () => {
  const stop = { reason: "fault", at_ms: now, item_id: "", step: "startup", sentence: "The loop stopped because its policy would not load.", next: "Owner repairs the policy." };
  const { container } = render(<LoopSummary drain={{ ...snapshotData.drain_summary, session: null, stop }} now={now} />);
  expect(container.querySelector(".class-state")?.textContent).toBe("stopped");
  expect(container.textContent).toContain("startup");
});

test.each([[session, true, "Loop alive"], [{ ...session, refreshed_at_ms: now - 4000 }, true, "Loop alive"], [{ ...session, refreshed_at_ms: now - 2001 }, false, "no loop is running"], [null, false, "no loop is running"]] as const)("header follows the server's heartbeat verdict", (heartbeat, active, label) => {
  const { container } = render(<LoopBand drain={{ ...snapshotData.drain_summary, active, session: heartbeat, heartbeat_interval_ms: 1000 }} now={now} />);
  expect(container.textContent).toContain(label);
});

test("a live heartbeat shows no stop facts, and a missing heartbeat does not claim a stop", () => {
  const live = { ...snapshotData.drain_summary, active: true, session: { ...session, refreshed_at_ms: now - 4000 } };
  const { container } = render(<><LoopBand drain={live} now={now} /><LoopSummary drain={live} now={now} /></>);
  expect(container.textContent).not.toMatch(/NOT KNOWN|no loop is running|Stop explanation not recorded|stop explanation not recorded/i);
  cleanup();
  const quiet = render(<><LoopBand drain={{ ...live, active: false }} now={now} /><LoopSummary drain={{ ...live, active: false }} now={now} /></>);
  expect(quiet.container.textContent).not.toMatch(/xplanation not recorded/);
});
