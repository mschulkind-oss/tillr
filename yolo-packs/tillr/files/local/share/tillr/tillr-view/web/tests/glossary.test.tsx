import { cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import glossary from "../src/models/glossary.json";
import { Glossary, attachDefinitions, glossaryTerms } from "../src/components/Glossary";

afterEach(cleanup);
const required = ["pass", "hold", "hold-only pass", "action", "lane", "run", "candidate", "gate", "landing", "heartbeat", "session"];
test("one glossary defines every required term and attaches definitions to page usage", async () => {
  for (const term of required) expect(glossary[term as keyof typeof glossary]).toMatch(/\.$/);
  const { container } = render(<Glossary><main>{required.map(term => <p key={term}>{term}</p>)}</main></Glossary>);
  await waitFor(() => {
    for (const node of container.querySelectorAll("main p")) {
      for (const term of glossaryTerms(node.textContent ?? "")) expect(node.getAttribute("title")).toContain(glossary[term]);
    }
  });
});
test("new page text, plural labels and updated text receive current definitions", async () => {
  const view = render(<Glossary><main><p>Passes and runs</p></main></Glossary>);
  expect(view.container.querySelector("main p")?.getAttribute("title")).toContain(glossary.pass);
  view.rerender(<Glossary><main><p>Candidate landing</p><p>Heartbeat</p></main></Glossary>);
  await waitFor(() => expect(view.container.querySelector("main p")?.getAttribute("title")).toContain(glossary.candidate));
  expect(view.container.querySelector("main p")?.getAttribute("title")).not.toContain(glossary.pass);
});
test("missing glossary definitions are errors, not silent unannotated jargon", () => {
  const node = document.createElement("main"); node.innerHTML = "<p>gate</p>";
  expect(() => attachDefinitions(node, { ...glossary, gate: "" })).toThrow("gate");
});
