import { test, expect, openDashboard } from "./helpers"

// 03-dashboard: the four-layer §13.8.2 overview (t9) renders all six metric
// cards and the layer titles, and a metric card routes to its page.
test("dashboard renders the six metrics and four layers", async ({ page }) => {
  await openDashboard(page)

  // Layer 1 — six metric cards.
  for (const label of ["在线设备", "纳管 Skills", "本地修改", "上游更新", "失败部署", "高风险项"]) {
    await expect(page.getByText(label, { exact: true })).toBeVisible()
  }
  // Layers 2–4 titles (exact text to avoid colliding with body copy like
  // "暂无待办事项，一切正常。"). Card titles render as styled divs, not headings.
  await expect(page.getByText("待办事项", { exact: true })).toBeVisible()
  await expect(page.getByText("风险雷达", { exact: true })).toBeVisible()
  await expect(page.getByText("最近部署", { exact: true })).toBeVisible()
  await expect(page.getByText("清单概览", { exact: true })).toBeVisible()
})

test("a metric card navigates to its page", async ({ page }) => {
  await openDashboard(page)
  await page.getByText("在线设备", { exact: true }).click()
  await page.waitForURL("**/#/devices", { timeout: 10_000 })
})
