import { cleanup, fireEvent, render } from "@testing-library/react";
import { afterEach, describe, expect, test } from "vitest";

import { ObservatoryHeaderBar, ProfilePill } from "../src/components/ObservatoryHeaderBar";
import { EMPTY_FILTER } from "../src/models/board";
import { envelope, snapshotData } from "./fixtures";

afterEach(cleanup);

describe("ProfilePill", () => {
  test("renders nothing when drain_summary has no profile", () => {
    const { container } = render(
      <ObservatoryHeaderBar
        snapshot={envelope(snapshotData)}
        error={null}
        onRefresh={() => {}}
        refreshing={false}
        live={true}
        filter={EMPTY_FILTER}
        shown={0}
        onToggleState={() => {}}
        onClearFilter={() => {}}
      />,
    );
    expect(container.querySelector(".profile-pill")).toBeNull();
  });

  test("preferred profile renders active badge and popover details", () => {
    const { getByRole, queryByRole } = render(
      <ProfilePill
        profile={{
          profile: "claude-sub",
          profiles: ["claude-sub", "copilot-gemini"],
          profile_index: 0,
          source: "toml",
          why: "preferred profile (1/2); all quota windows open",
        }}
      />,
    );
    const btn = getByRole("button", { name: "claude-sub" });
    expect(queryByRole("dialog")).toBeNull();
    fireEvent.click(btn);
    const dialog = getByRole("dialog", { name: "Seat profile details" });
    expect(dialog.textContent).toContain("claude-sub (in force)");
    expect(dialog.textContent).toContain("copilot-gemini");
    expect(dialog.textContent).toContain("Source: toml");
    expect(dialog.textContent).toContain("Reason: preferred profile (1/2); all quota windows open");
  });

  test("fallback profile renders fallback badge until reset and skipped quota details", () => {
    const { getByRole } = render(
      <ProfilePill
        profile={{
          profile: "copilot-gemini",
          profiles: ["claude-sub", "copilot-gemini"],
          profile_index: 1,
          source: "toml",
          why: "switched from claude-sub at 2026-11-27T18:08:12Z",
          switched_from: "claude-sub",
          switched_at: "2026-11-27T18:08:12Z",
          skipped_profiles: [
            {
              profile: "claude-sub",
              backend: "claude-sonnet",
              windows: [{ kind: "five_hour", fraction: 0.82, reset_at: "2026-11-27T20:30:00Z" }],
              reserve: 0.2,
              reopen_threshold: 0.7,
              reset_at: "2026-11-27T20:30:00Z",
            },
          ],
        }}
      />,
    );
    const btn = getByRole("button", { name: "copilot-gemini · fallback from claude-sub until 20:30Z" });
    fireEvent.click(btn);
    const dialog = getByRole("dialog", { name: "Seat profile details" });
    expect(dialog.textContent).toContain("copilot-gemini (in force)");
    expect(dialog.textContent).toContain("Switched at: 2026-11-27T18:08:12Z");
    expect(dialog.textContent).toContain("claude-sonnet");
    expect(dialog.textContent).toContain("five_hour 0.82");
    expect(dialog.textContent).toContain("reserve 0.2");
    expect(dialog.textContent).toContain("reopen <= 0.7");
  });
});
