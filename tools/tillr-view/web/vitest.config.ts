import { defineConfig } from "vitest/config";

// The unit suite runs in jsdom over the components, with no server: every fixture is a
// partial drain, so a component that invents a value fails here rather than on screen.
export default defineConfig({
  test: {
    environment: "jsdom",
    minWorkers: 1,
    maxWorkers: 6,
    css: { include: /tokens\.css/ },
    include: ["tests/**/*.test.ts", "tests/**/*.test.tsx"],
  },
});
