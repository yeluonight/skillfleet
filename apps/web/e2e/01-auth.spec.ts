import { test, expect, login, ADMIN } from "./helpers"

// 01-auth exercises the login form itself, so it runs signed-OUT (opting out
// of the suite's shared storage state).
test.use({ storageState: { cookies: [], origins: [] } })

// the login flow and the bad-credentials path, both localised (t7).
test("rejects bad credentials with a Chinese error", async ({ page }) => {
  await page.goto("/")
  await page.waitForURL(/#\/login/, { timeout: 10_000 })
  await page.getByLabel(/用户名|Username/).fill(ADMIN.username)
  await page.getByLabel(/密码|Password/).fill("wrongwrongwrong")
  await page.getByRole("button", { name: /登录|Sign in/ }).click()
  await expect(page.getByText("用户名或密码错误。")).toBeVisible()
})

test("signs in and lands on the dashboard with the sidebar", async ({ page }) => {
  await login(page)
  // Sidebar nav is present with the localised items (t5 + t6).
  await expect(page.getByRole("link", { name: "仪表盘" })).toBeVisible()
  await expect(page.getByRole("link", { name: "设备" })).toBeVisible()
  await expect(page.getByRole("link", { name: "审计" })).toBeVisible()
})
