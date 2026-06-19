import { defineConfig, devices } from "@playwright/test"

// Playwright config for the Phase 12 E2E suite (t14). Covers the critical
// flow: setup → login → guided enrollment → dashboard metrics → language
// switch.
//
// Auth is done ONCE in a setup project (e2e/auth.setup.ts) which consumes the
// boot-time setup code (SF_SETUP_CODE) if needed and saves the signed-in
// storage state to .playwright/state.json. The main chromium project depends
// on it and reuses that state, so the specs don't each re-login — which would
// trip the server's per-user login rate limit.
//
// The suite drives the Vite dev server (proxies /api + /health to the Go
// server on :47890), started here via `webServer`. The Go server itself is NOT
// started by Playwright — CI builds + launches it; locally run one on :47890.
const STORAGE_STATE = ".playwright/state.json"

export default defineConfig({
  testDir: "./e2e",
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["github"], ["list"]] : "list",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: "http://127.0.0.1:5173",
    trace: "on-first-retry",
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "setup", testMatch: /auth\.setup\.ts/ },
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"], storageState: STORAGE_STATE },
      dependencies: ["setup"],
      // The login spec needs the signed-out state to exercise the form, so it
      // opts out of the shared storage state itself (see 01-auth.spec.ts).
      testIgnore: /auth\.setup\.ts/,
    },
  ],
  webServer: {
    command: "npm run dev",
    url: "http://127.0.0.1:5173",
    reuseExistingServer: !process.env.CI,
    // CI cold start (fresh runner, optimizeDeps prebuild on first vite run)
    // can take well over 60s; give generous headroom. Pipe vite's output so
    // a future slow/failed start is diagnosable in the CI log.
    timeout: 180_000,
    stdout: "pipe",
    stderr: "pipe",
  },
})
