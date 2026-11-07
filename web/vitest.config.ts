import { defineConfig } from "vitest/config";
import { fileURLToPath } from "node:url";

// Only the parity guard runs here; component rendering is covered by the
// typecheck and build. The @ alias matches tsconfig.json so the test imports
// the same modules the app does, rather than a copy.
export default defineConfig({
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./", import.meta.url)),
    },
  },
  test: {
    include: ["tests/**/*.test.ts"],
    environment: "node",
  },
});
