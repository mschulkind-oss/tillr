// @vitest-environment node
// The dev proxy's suite, and the whole of it. It runs in the node environment because
// importing the config loads Vite itself, which esbuild refuses to do under jsdom.
//
// Nothing else in the gate loads
// `vite.config.ts`, so a proxy entry that cannot carry an upgrade ships silently — the
// operator gets a screen that never moves and no error to explain it. The config is
// imported rather than read as text, so this asserts the option Vite actually receives.

import { describe, expect, test, vi } from "vitest";

import config from "../vite.config";

interface ProxyEntry {
  target?: string;
  ws?: boolean;
}

describe("the dev server's proxy reaches the same server the reads do", () => {
  test("the /api entry carries a websocket upgrade", () => {
    const proxy = config.server?.proxy as Record<string, ProxyEntry> | undefined;
    expect(proxy).toBeDefined();
    const entry = proxy?.["/api"];
    expect(entry?.target).toBe("http://127.0.0.1:4318");
    // Without `ws: true` Vite answers the live channel's upgrade as an ordinary request:
    // HTTP reads keep working, so the screen looks broken rather than disconnected.
    expect(entry?.ws).toBe(true);
  });

  test("the target follows TILLR_VIEW_API, so a moved port is not a dead proxy", async () => {
    // `just observatory-dev 5000` exports this. Without it the proxy would answer a moved
    // server's requests by dialling 4318, which is the same silent freeze as a missing
    // `ws: true` -- the operator sees a screen that never moves.
    vi.stubEnv("TILLR_VIEW_API", "http://127.0.0.1:4399");
    vi.resetModules();
    try {
      const moved = (await import("../vite.config")).default;
      const proxy = moved.server?.proxy as Record<string, ProxyEntry> | undefined;
      expect(proxy?.["/api"]?.target).toBe("http://127.0.0.1:4399");
      expect(proxy?.["/api"]?.ws).toBe(true);
    } finally {
      vi.unstubAllEnvs();
      vi.resetModules();
    }
  });

  test("the dev server's own port is not the API's", () => {
    // A proxy to itself would loop; the port is what keeps the two apart.
    expect(config.server?.port).toBe(5173);
  });
});
