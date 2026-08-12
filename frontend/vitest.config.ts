import { defineConfig } from "vitest/config";

// Only pure helpers under lib/ are unit-tested; components are covered by tsc and manual browser checks.
export default defineConfig({
  test: {
    include: ["lib/**/*.test.ts"],
    environment: "node",
  },
});
