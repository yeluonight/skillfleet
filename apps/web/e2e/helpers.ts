import { test as base, expect, type Page } from "@playwright/test"

// Shared credentials for the E2E run. The admin is created by the setup spec
// (or already exists when re-running against a live server).
export const ADMIN = { username: "admin", password: "correcthorsebatterystaple" }

// login drives the login form and waits for the dashboard. Assumes setup is
// already done (the 00-setup spec runs first in file order).
export async function login(page: Page) {
  await page.goto("/")
  await page.waitForLoadState("networkidle")
  // If already authenticated (reused server within a context), we may land on
  // the dashboard directly.
  if (page.url().includes("#/dashboard")) return
  if (page.url().includes("#/setup")) {
    throw new Error("login(): server still needs setup; run setup first")
  }
  await page.getByLabel(/用户名|Username/).fill(ADMIN.username)
  await page.getByLabel(/密码|Password/).fill(ADMIN.password)
  await page.getByRole("button", { name: /^(登录|Sign in)$/ }).click()
  await page.waitForURL(/#\/dashboard/, { timeout: 15_000 })
}

// setLanguage flips the persisted language and reloads so the whole app
// re-renders in that language.
export async function setLanguage(page: Page, lang: "zh-CN" | "en") {
  await page.evaluate((l) => {
    localStorage.setItem("skillfleet.lang", l)
    location.reload()
  }, lang)
  await page.waitForTimeout(800)
}

export const test = base
export { expect }

// openDashboard navigates to the app root and waits for the dashboard. Used by
// specs that run with the shared signed-in storage state (no login needed).
export async function openDashboard(page: Page) {
  await page.goto("/")
  await page.waitForURL(/#\/dashboard/, { timeout: 15_000 })
}
