import { test, expect, openDashboard } from "./helpers"

// 02-enroll: the guided enrollment wizard (t13) — the fix for the
// CLI-vs-WebUI confusion. We drive it through token generation and the
// assembled enroll command; we don't run a real agent, so we stop at the
// "waiting for device" step (the approve path needs a live agent and is
// covered by the manual end-to-end check).
test("guided enrollment wizard assembles the enroll command", async ({ page }) => {
  await openDashboard(page)
  await page.getByRole("link", { name: "设备" }).click()
  await page.waitForURL("**/#/devices")

  await page.getByRole("button", { name: "引导式纳管" }).click()
  await expect(page.getByRole("heading", { name: "纳管一台新设备" })).toBeVisible()

  // Step 1 — generate a token.
  await expect(page.getByRole("heading", { name: "生成纳管 token" })).toBeVisible()
  await page.getByRole("button", { name: "生成 token" }).click()
  await expect(page.getByText(/token 已生成|有效期至/)).toBeVisible()

  // Step 2 — the assembled enroll command carries the origin + a token.
  await page.getByRole("button", { name: "下一步" }).click()
  await expect(page.getByText("在被管机器命令行执行")).toBeVisible()
  // The assembled command is the only mono block carrying the live origin
  // (the cards' background text uses <url> placeholders).
  const cmd = page.locator(".font-mono").filter({ hasText: "127.0.0.1:5173" })
  await expect(cmd).toBeVisible()
  await expect(cmd).toContainText("skillfleet-agent enroll")
  await expect(page.getByRole("button", { name: "复制命令" })).toBeVisible()

  // Step 3 — the approve step polls and shows its empty state (no real agent).
  await page.getByRole("button", { name: "下一步" }).click()
  await expect(page.getByRole("heading", { name: "等待设备出现并批准" })).toBeVisible()

  // Step 4 — roots guidance with the assembled command.
  await page.getByRole("button", { name: "下一步" }).click()
  await expect(page.getByRole("heading", { name: "注册 Skill 根目录" })).toBeVisible()
  await expect(
    page.locator(".font-mono").filter({ hasText: "skillfleet-agent roots add" }),
  ).toBeVisible()
  await expect(page.getByText("纳管完成")).toBeVisible()

  await page.getByRole("button", { name: "完成" }).click()
  await expect(page.getByRole("heading", { name: "纳管一台新设备" })).toBeHidden()
})
