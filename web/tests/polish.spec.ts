import { test, expect } from '@playwright/test'
import { fixture } from './fixture'

for (const width of [390, 1440]) {
  test(`login button keeps its full width from the first frame at ${width}px`, async ({ page }) => {
    const { errors } = await fixture(page, { authenticated: false })
    await page.setViewportSize({ width, height: 900 })
    await page.emulateMedia({ reducedMotion: 'no-preference' })
    await page.addInitScript(() => {
      const samples: number[] = []
      ;(window as any).loginWidthSamples = samples
      function sample() {
        const button = [...document.querySelectorAll('button')].find(button => button.textContent?.trim() === '进入管理页')
        if (button) {
          const field = button.closest('form')!.querySelector('input')!.closest('.secret-field')!
          samples.push(button.getBoundingClientRect().width / field.getBoundingClientRect().width)
        }
        if (samples.length < 40) requestAnimationFrame(sample)
      }
      requestAnimationFrame(sample)
    })
    for (let attempt = 0; attempt < 2; attempt++) {
      if (attempt === 0) await page.goto('/')
      else await page.reload()
      await expect.poll(() => page.evaluate(() => (window as any).loginWidthSamples.length)).toBe(40)
      const widths = await page.evaluate(() => (window as any).loginWidthSamples as number[])
      expect(Math.min(...widths)).toBeGreaterThan(.99)
      expect(Math.max(...widths)).toBeLessThan(1.01)
    }
    expect(errors).toEqual([])
  })

  test(`push detail modal preserves the list, focus and scroll at ${width}px`, async ({ page }, info) => {
    const { errors, writes } = await fixture(page)
    await page.setViewportSize({ width, height: 844 })
    await page.emulateMedia({ reducedMotion: 'reduce', colorScheme: width === 390 ? 'dark' : 'light' })
    const firstPage = page.waitForResponse(response => response.url().includes('/deliveries?'))
    await page.goto('/#/accounts/a/push-history')
    const sample = (await (await firstPage).json()).items[0]
    const items = Array.from({ length: 20 }, (_, i) => ({ ...sample, event_id: `synthetic-modal-${i}`, type: 'notification', title: `合成提醒 ${i}`, body: '这是一条仅用于弹窗验收的合成通知。' }))
    await page.route('**/api/accounts/a/deliveries?*', route => route.fulfill({ json: { items, next_cursor: 0 } }))
    await page.reload()
    const row = page.locator('.history-row').nth(12)
    await row.scrollIntoViewIfNeeded()
    const position = await page.evaluate(() => window.scrollY)
    await row.click()
    const dialog = page.getByRole('dialog', { name: '推送详情' })
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText('synthetic-modal-12')
    await expect(page.getByRole('button', { name: '关闭推送详情' })).toBeFocused()
    await page.screenshot({ path: info.outputPath('push-detail.png'), animations: 'disabled' })
    await page.keyboard.press('Shift+Tab')
    expect(await dialog.evaluate(element => element.contains(document.activeElement))).toBe(true)
    await page.mouse.move(2, 2)
    await page.mouse.wheel(0, 400)
    expect(await page.evaluate(() => window.scrollY)).toBe(position)
    const box = (await dialog.boundingBox())!
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(width)
    expect(box.y + box.height).toBeLessThanOrEqual(844)
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
    await expect(row).toBeFocused()
    expect(await page.evaluate(() => window.scrollY)).toBe(position)
    await expect(page).not.toHaveURL(/event=/)
    await row.click()
    await expect(dialog).toBeVisible()
    await dialog.getByRole('heading', { name: '处理过程' }).click()
    await expect(dialog).toBeVisible()
    await page.mouse.click(2, 2)
    await expect(dialog).toHaveCount(0)
    await expect(row).toBeFocused()
    expect(await page.evaluate(() => document.body.style.overflow)).not.toBe('hidden')
    expect(errors).toEqual([])
    expect(writes).toEqual([])
  })
}

test('detail links recover from loading failures and retain an open record during polling', async ({ page }) => {
  const { errors, writes } = await fixture(page)
  await page.clock.install()
  await page.route('**/api/accounts/a/deliveries?*', route => route.fulfill({ status: 503, json: { error: '合成详情读取失败' } }))
  await page.goto('/#/accounts/a/push-history?q=synthetic&event=synthetic-event-40')
  const dialog = page.getByRole('dialog', { name: '推送详情' })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole('alert')).toContainText('合成详情读取失败')
  await expect(dialog).not.toContainText('正在读取推送详情')
  await page.unroute('**/api/accounts/a/deliveries?*')
  await dialog.getByRole('button', { name: '重试', exact: true }).click()
  await expect(dialog).toContainText('synthetic-event-40')
  await page.route('**/api/accounts/a/deliveries?*', route => route.fulfill({ json: { items: [], next_cursor: 0 } }))
  await page.clock.fastForward(5100)
  await expect(page.locator('.history-row')).toHaveCount(0)
  await expect(dialog).toContainText('synthetic-event-40')
  await dialog.getByRole('button', { name: '关闭推送详情' }).click()
  await expect(dialog).toHaveCount(0)
  await expect(page.getByLabel('搜索内容')).toHaveValue('synthetic')
  await expect(page).not.toHaveURL(/event=/)
  expect(errors).toEqual([])
  expect(writes).toEqual([])
})
