import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  reporter: "list",
  timeout: 30_000,
  use: {
    browserName: "chromium",
    headless: true,
  },
});
