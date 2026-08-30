import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: 'html',
  use: {
    baseURL: 'http://localhost:4173',
    trace: 'on-first-retry',
  },
  webServer: {
    command: 'npm run build && npm run preview -- --port 4173',
    url: 'http://localhost:4173',
    // Foot-gun specific to this repo: multiple git worktrees of this project share one
    // node_modules (see the SDD ledger / prior branches' setup). If a `vite preview` from a
    // DIFFERENT worktree's checkout is still running on :4173, this flag makes Playwright
    // silently reuse it locally, serving that other worktree's code instead of this one's —
    // the suite can then pass (or fail) against code you aren't actually testing. CI is
    // unaffected (reuseExistingServer is false there), so this only bites local runs.
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
