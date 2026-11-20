// The document pane (design §8, ledger `OQ-OBD2`): the item's design document as a
// frontmatter card, a question count, a path and a link into Vantage — and never as prose.
// Two states decide whether the pane is useful or a lie: a base URL is configured, so the
// link is `base/#/<path>` (with the item's section when it names one), or none is, so the
// link is still drawn, still says what it would do, and goes nowhere. The board never
// requests that URL: it does not know whether Vantage is running.

import { cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test } from "vitest";

import { DocumentPane } from "../src/components/DocumentPane";
import { vantageLink } from "../src/models/document";
import type { DocData } from "../src/models/types";

const originalFetch = globalThis.fetch;

beforeEach(() => {
  globalThis.fetch = (() => {
    throw new Error("the document pane must not request anything");
  }) as unknown as typeof fetch;
});

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
});

const doc: DocData = {
  item_id: "item-obs-doc-link",
  path: "docs/reference/observatory-design.md",
  frontmatter: {
    title: "Observatory Design: The Board an Operator Reads",
    status: "accepted",
    stage: "DECIDED",
    next: "land the metadata-and-link pane",
    updated: "2026-11-13",
  },
  anchor: "item-detail-and-design-document-metadata",
  open_questions: 3,
  questions_state: "questions-ok",
  bytes: 31204,
};

function renderPane(overrides: Partial<{ doc: DocData; baseUrl: string }> = {}) {
  return render(
    <DocumentPane
      doc={overrides.doc ?? doc}
      error={null}
      pending={false}
      onRetry={() => {}}
      baseUrl={overrides.baseUrl ?? ""}
    />,
  );
}

describe("the document pane links into Vantage", () => {
  test("a configured base answers base/#/path#anchor", () => {
    const { container } = renderPane({ baseUrl: "http://vantage.test:1234" });
    const link = container.querySelector("a.doc__link");
    expect(link).not.toBeNull();
    expect(link?.getAttribute("href")).toBe(
      "http://vantage.test:1234/#/docs/reference/observatory-design.md#item-detail-and-design-document-metadata",
    );
    // The link is drawn and the pane does not say it is inert.
    expect(container.textContent).not.toContain("goes nowhere");
  });

  test("an item that names no section answers a link with no fragment", () => {
    const { container } = renderPane({
      doc: { ...doc, anchor: "" },
      baseUrl: "http://vantage.test:1234",
    });
    const href = container.querySelector("a.doc__link")?.getAttribute("href");
    expect(href).toBe("http://vantage.test:1234/#/docs/reference/observatory-design.md");
  });

  test("no configured base draws the link inert and says so in one line", () => {
    const { container } = renderPane({ baseUrl: "" });
    // Drawn: the words are on screen. Inert: it is not an anchor and carries no href.
    expect(container.textContent).toContain("Open in Vantage");
    expect(container.querySelector("a.doc__link")).toBeNull();
    const inert = container.querySelector(".doc__link--inert");
    expect(inert).not.toBeNull();
    expect(inert?.getAttribute("href")).toBeNull();
    expect(inert?.getAttribute("aria-disabled")).toBe("true");
    expect(container.textContent).toContain(
      "no Vantage base URL is configured, so this link goes nowhere",
    );
  });

  test("a base with a trailing slash and a path with a leading slash answer one slash", () => {
    expect(vantageLink("http://vantage.test:1234/", "/docs/a.md", "")).toBe(
      "http://vantage.test:1234/#/docs/a.md",
    );
    expect(vantageLink("http://vantage.test:1234", "docs/a.md", "the-section")).toBe(
      "http://vantage.test:1234/#/docs/a.md#the-section",
    );
  });
});

describe("the document pane shows metadata and no prose", () => {
  test("the frontmatter card, the count, the path and the size", () => {
    const { container } = renderPane({ baseUrl: "http://vantage.test:1234" });
    const text = container.textContent ?? "";
    for (const value of [
      "Observatory Design: The Board an Operator Reads",
      "accepted",
      "DECIDED",
      "land the metadata-and-link pane",
      "2026-11-13",
      "3 open questions",
      "docs/reference/observatory-design.md",
      "31,204 bytes",
    ]) {
      expect(text).toContain(value);
    }
  });

  test("a key the document does not declare is named, not drawn as a blank", () => {
    const { container } = renderPane({
      doc: { ...doc, frontmatter: { title: "A design", stage: "DESIGN" } },
    });
    const absent = Array.from(container.querySelectorAll(".doc__fact-value--absent")).map(
      (element) => element.textContent,
    );
    expect(absent).toEqual(["not recorded", "not recorded", "not recorded"]);
  });

  test("a document with no frontmatter block says so instead of drawing five blanks", () => {
    const { container } = renderPane({ doc: { ...doc, frontmatter: null } });
    expect(container.textContent).toContain(
      "this document opens with no frontmatter, so there is no metadata card to show",
    );
    expect(container.querySelector(".doc__card")).toBeNull();
  });

  test("one open question is singular", () => {
    const { container } = renderPane({ doc: { ...doc, open_questions: 1 } });
    expect(container.textContent).toContain("1 open question");
  });

  test("an index that could not answer says so instead of drawing zero questions", () => {
    const { container } = renderPane({
      doc: { ...doc, open_questions: null, questions_state: "questions-unknown" },
    });
    expect(container.textContent).toContain("question count unknown: the index could not be read");
    expect(container.textContent).not.toContain("0 open questions");
  });

  test("a missing document is named, not counted as zero", () => {
    const { container } = renderPane({
      doc: { ...doc, open_questions: null, questions_state: "design-doc-missing" },
    });
    expect(container.textContent).toContain("the design document is not there");
    expect(container.textContent).not.toContain("0 open questions");
  });
});
