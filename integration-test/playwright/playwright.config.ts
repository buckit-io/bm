import { defineConfig } from "@playwright/test";

const baseURL = process.env.PLAYWRIGHT_BASE_URL ?? "http://127.0.0.1:9443";
const recordVideo = process.env.BM_E2E_RECORD_VIDEO === "1";
const viewport = { width: 1280, height: 1024 };

export default defineConfig({
  testDir: "./tests",
  timeout: 90_000,
  expect: {
    timeout: 20_000,
  },
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [["html", { open: "never" }], ["line"]] : "list",
  use: {
    baseURL,
    viewport,
    trace: "on-first-retry",
    screenshot: "only-on-failure",
    video: recordVideo
      ? { mode: "on", size: viewport }
      : "retain-on-failure",
  },
});
