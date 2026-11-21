import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { ObservatoryHeaderBar } from "../src/components/ObservatoryHeaderBar";
import { EMPTY_FILTER } from "../src/models/board";
import { envelope, liveSnapshotData } from "./fixtures";

afterEach(cleanup);

test("ignores a legacy snapshot's grant renewal data without hiding operator status", () => {
  const legacySnapshot = {
    ...liveSnapshotData,
    grants: [{
      grant_id: "legacy-verification-grant",
      claim_classes: ["verification-only"],
      expires_at_ms: 1,
      expires_at: "1970-01-01T00:00:00Z",
      renew_command: "tillr item grant issue --input <record>",
    }],
  };
  const { container } = render(
    <ObservatoryHeaderBar
      snapshot={envelope(legacySnapshot)}
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
  expect(screen.getByText(/drain restart:/)).toBeTruthy();
  expect(container.textContent).not.toContain("legacy-verification-grant");
  expect(container.textContent).not.toContain("tillr item grant issue");
});
