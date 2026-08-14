import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// The screens are tested in a real DOM against a real server. Nothing is added
// to the server for it: the test signs in the way the browser does.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: false,
    include: ["src/**/*.test.tsx"],
    testTimeout: 20000,
  },
});
