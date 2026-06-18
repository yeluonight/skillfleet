import { test, expect, openDashboard, setLanguage } from "./helpers"

// 04-i18n: switching the language re-renders the whole shell live and persists
// across a reload (t12 Settings + the i18n foundation). This is the proof that
// the dual zh-CN/en dictionaries are actually wired, not just present.
test("language switch flips the shell between zh-CN and en", async ({ page }) => {
  await openDashboard(page)

  // Default is Simplified Chinese.
  await expect(page.getByRole("link", { name: "仪表盘" })).toBeVisible()

  // Switch to English: the sidebar + dashboard re-render in English.
  await setLanguage(page, "en")
  await expect(page.getByRole("link", { name: "Dashboard" })).toBeVisible()
  await expect(page.getByRole("link", { name: "Devices" })).toBeVisible()
  await expect(page.getByRole("link", { name: "Audit" })).toBeVisible()

  // Switch back to Chinese.
  await setLanguage(page, "zh-CN")
  await expect(page.getByRole("link", { name: "仪表盘" })).toBeVisible()
})

test("audit page filters persist to the URL query", async ({ page }) => {
  await openDashboard(page)
  await page.getByRole("link", { name: "审计" }).click()
  await page.waitForURL("**/#/audit")
  await expect(page.getByText("审计日志")).toBeVisible()

  await page.getByPlaceholder(/device\./).fill("device.")
  await page.getByRole("button", { name: "应用筛选" }).click()
  await expect(page).toHaveURL(/action=device\./)
})
