import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "../src/App";
import { envelope, snapshotData } from "./fixtures";
import { afterEach, expect, test, vi } from "vitest";
import { fetchQuestions } from "../src/models/api";
import { RefusalState, refusalCode } from "../src/components/RefusalState";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });

test("the complete Passes page renders one missing-route line, not a second shell alert", async () => {
  window.history.replaceState(null, "", "/#/passes?range=all");
  vi.stubGlobal("WebSocket", class { close() {} });
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    if (String(input) === "/api/v1/snapshot") return new Response(JSON.stringify(envelope(snapshotData)));
    if (String(input).startsWith("/api/v1/passes")) return new Response(JSON.stringify({ snapshot: { build: { revision: "old-server" } }, error: { code: "unknown-route", message: "old face routes" } }), { status: 404 });
    throw new Error(`Unexpected route: ${input}`);
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(<QueryClientProvider client={client}><App /></QueryClientProvider>);
  await waitFor(() => expect(screen.getAllByRole("alert")).toHaveLength(1));
  expect(container.textContent?.match(/This page needs a newer server/g)).toHaveLength(1);
  expect(screen.getByRole("alert").textContent).toContain("old-server");
  expect(screen.getByRole("alert").textContent).toContain("just observatory");
  expect(screen.getByRole("alert").textContent).not.toMatch(/face|routes/);
  client.clear();
});
test.each([true, false])("missing route produces one actionable line (JSON=%s)", async json => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(json ? JSON.stringify({ snapshot: { build: { revision: "old-build" } }, error: { code: "unknown-route", message: "this face serves the routes: a, b" } }) : "404 page not found", { status: 404 })));
  let error: Error | undefined;
  try { await fetchQuestions(); } catch (caught) { error = caught as Error; }
  expect(error).toBeDefined();
  const { container } = render(<RefusalState code={refusalCode(error!)} message={error!.message} />);
  expect(container.querySelectorAll("p")).toHaveLength(1);
  expect(container.textContent).toContain("This page needs a newer server");
  expect(container.textContent).toContain(json ? "old-build" : "not recorded");
  expect(container.textContent).toMatch(/page build: .+/);
  expect(container.textContent).toContain("just observatory");
  expect(container.textContent).not.toMatch(/face|routes:/);
});
