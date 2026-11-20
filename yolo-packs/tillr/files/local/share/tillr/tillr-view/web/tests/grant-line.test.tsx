import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { GrantLine, ObservatoryHeaderBar } from "../src/components/ObservatoryHeaderBar";
import { EMPTY_FILTER } from "../src/models/board";
import type { GrantView } from "../src/models/types";
import { envelope, liveSnapshotData } from "./fixtures";

afterEach(cleanup);

const expiresAtMs = Date.parse("2026-11-19T23:08:00Z");
const sampleGrant: GrantView = {
  grant_id: "grant-verif-24h",
  claim_classes: ["verification-only"],
  grantor: "owner",
  issued_at_ms: expiresAtMs - 24 * 60 * 60 * 1000,
  expires_at_ms: expiresAtMs,
  issued_at: "2026-11-18T23:08:00Z",
  expires_at: "2026-11-19T23:08:00Z",
  state: "active",
  warning: false,
  summary: "grant: grant-verif-24h (verification-only) expires at 2026-11-19T23:08:00Z",
  renew_command: "tillr item grant issue --store /tmp/board.db --input <record> --now <RFC3339>",
};

test("shows grant expiry calmly more than an hour before it lapses", () => {
  const twoHoursBefore = expiresAtMs - 2 * 60 * 60 * 1000;
  const { container } = render(<GrantLine grants={[sampleGrant]} now={twoHoursBefore} />);
  const node = container.querySelector(".head-grant");
  expect(node).not.toBeNull();
  expect(node?.classList.contains("head-grant--warning")).toBe(false);
  expect(node?.textContent).toContain("grant grant-verif-24h (verification-only) expires at 2026-11-19T23:08:00Z (in 2h00m)");
  expect(node?.textContent).not.toContain("warning:");
});

test("warns from one hour before expiry and names the renewal command", () => {
  for (const offsetMs of [60 * 60 * 1000, 45 * 60 * 1000]) {
    const { container, unmount } = render(<GrantLine grants={[sampleGrant]} now={expiresAtMs - offsetMs} />);
    const node = container.querySelector(".head-grant--warning");
    expect(node).not.toBeNull();
    const text = node?.textContent ?? "";
    expect(text).toContain("warning: grant grant-verif-24h (verification-only) expires at 2026-11-19T23:08:00Z");
    expect(text).toContain("renew with tillr item grant issue --store /tmp/board.db --input <record> --now <RFC3339>");
    unmount();
  }
});

test("says plainly when a grant has expired in the header bar", () => {
  const expiredGrant: GrantView = { ...sampleGrant, state: "expired", warning: true };
  const data = { ...liveSnapshotData, grants: [expiredGrant] };
  const { container } = render(
    <ObservatoryHeaderBar
      snapshot={envelope(data)}
      error={null}
      onRefresh={() => {}}
      refreshing={false}
      live
      filter={EMPTY_FILTER}
      shown={1}
      onToggleState={() => {}}
      onClearFilter={() => {}}
    />,
  );
  // liveSnapshotData's timestamp is well after or before depending on wall clock; test GrantLine directly at +5m too.
  expect(container.querySelector(".head-grant")).not.toBeNull();

  const after = render(<GrantLine grants={[expiredGrant]} now={expiresAtMs + 5 * 60 * 1000} />);
  const text = after.container.querySelector(".head-grant--warning")?.textContent ?? "";
  expect(text).toContain("warning: grant grant-verif-24h (verification-only) expired at 2026-11-19T23:08:00Z");
  expect(text).toContain("issue a new grant with tillr item grant issue --store /tmp/board.db --input <record> --now <RFC3339>");
});
