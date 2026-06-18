import { test as setup, expect } from "@playwright/test"

const STORAGE_STATE = ".playwright/state.json"
const ADMIN = { username: "admin", password: "correcthorsebatterystaple" }

// auth.setup.ts runs once before the suite (the "setup" project). It consumes
// the boot-time setup code to create the admin if the server still needs it,
// then signs in and saves the storage state so the main project's specs reuse
// the session instead of each re-logging-in (which would trip the per-user
// login rate limit).
setup("authenticate", async ({ page, request }) => {
  const status = await request.get("/api/status").then((r) => r.json())

  if (status.setup_required) {
    const code = process.env.SF_SETUP_CODE
    expect(code, "SF_SETUP_CODE must be set when the server needs setup").toBeTruthy()
    await page.goto("/")
    await page.waitForURL("**/#/setup", { timeout: 10_000 })
    await expect(page.getByText("初始化设置")).toBeVisible()
    await page.getByLabel("初始化代码").fill(code!)
    await page.getByLabel(/用户名|Username/).fill(ADMIN.username)
    await page.getByLabel(/密码|Password/).fill(ADMIN.password)
    await page.getByRole("button", { name: "创建管理员" }).click()
    await page.waitForURL("**/#/login", { timeout: 10_000 })
  }

  // Sign in and persist the session.
  await page.goto("/")
  await page.waitForLoadState("networkidle")
  if (!page.url().includes("#/dashboard")) {
    await page.getByLabel(/用户名|Username/).fill(ADMIN.username)
    await page.getByLabel(/密码|Password/).fill(ADMIN.password)
    await page.getByRole("button", { name: /^(登录|Sign in)$/ }).click()
    await page.waitForURL(/#\/dashboard/, { timeout: 15_000 })
  }
  await page.context().storageState({ path: STORAGE_STATE })
})
