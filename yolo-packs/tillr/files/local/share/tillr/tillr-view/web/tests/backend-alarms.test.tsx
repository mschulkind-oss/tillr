import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { ObservatoryShell } from "../src/components/ObservatoryShell";
import { waitingActors, waitingOrder } from "../src/models/waiting";
import type { WaitingRow } from "../src/models/types";
import { envelope, snapshotData } from "./fixtures";

afterEach(cleanup);

test("the shared shell leads with the measured alarm before navigation and counts", () => {
  const alarm = { kind: "backend-closed", backend: "pi", since: "2026-11-21 03:00:00 EST", cause: "infrastructure failure: run down", items: ["item-a"], lanes: ["lane-1"], clears: "successful probe", command: "tillr drain probe-backend" };
  const shell = (alarms = [alarm]) => <ObservatoryShell hash="#/items" snapshot={envelope({ ...snapshotData, alarms })} snapshotError={null} live paused={false} onPause={() => {}} onRefresh={() => {}}><main>queue rows</main></ObservatoryShell>;
  const { container, rerender } = render(shell());
  const banner = screen.getByRole("alert", { name: "Backend alarms" });
  expect(container.querySelector(".observatory-shell__sticky")?.firstElementChild).toBe(banner);
  for (const word of ["ALARM backend-closed (pi)", alarm.since, alarm.cause, "1 item(s) [item-a]", "1 lane(s) [lane-1]", alarm.clears, alarm.command]) {
    expect(banner.textContent).toContain(word);
  }
  rerender(shell([]));
  expect(screen.queryByRole("alert", { name: "Backend alarms" })).toBeNull();
});

test("alarm rows sort before place and count only the held item, not a synthetic block", () => {
  const regular: WaitingRow = { item_id: "regular", place: 1, class: "ready", why: "", mover: "drain", where: "queue", since: "", class_source: "planner" };
  const held: WaitingRow = { ...regular, item_id: "held", place: 9, class: "alarm" };
  expect([regular, held].sort(waitingOrder)).toEqual([held, regular]);
  const synthetic: WaitingRow = { ...held, alarm: { kind: "backend-closed", backend: "pi", since: "", cause: "", items: ["held"], lanes: [], clears: "", command: "" } };
  expect(waitingActors([regular, held, synthetic])).toEqual({ owner: 0, drain: 0, design: 0, operator: 1 });
});
