import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "tests/e2e",
  retries: 0,
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
