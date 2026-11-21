import { defineConfig, devices } from "@playwright/test";

import { IMAGE_HOST_NAME } from "./tests/e2e/support/imagehost";

export default defineConfig({
  testDir: "tests/e2e",
  retries: 0,
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        // Only the test image host's name resolves here. Every other name
        // still goes to DNS, so leak.example.test fails to resolve.
        launchOptions: {
          args: [`--host-resolver-rules=MAP ${IMAGE_HOST_NAME} 127.0.0.1`],
        },
      },
    },
  ],
});
