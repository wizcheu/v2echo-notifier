import { test, expect } from '@playwright/test'
import type { Page, TestInfo } from '@playwright/test'
import { fixture } from './fixture'

const scenes = [
  ['overview', '/accounts/a/overview', '.metrics'],
  ['checks', '/accounts/a/check-history', '.check-list'],
  ['settings', '/accounts/a/settings', '.settings-card'],
  ['credentials', '/accounts/a/settings/credentials', '.credential-fields'],
  ['device', '/accounts/a/settings/device', '.pairing-qr'],
  ['sync', '/accounts/a/settings/sync', '.push-schedule'],
  ['proxy', '/accounts/a/proxy', '.mode-picker'],
  ['test', '/accounts/a/push-test', '.test-composer'],
  ['history', '/accounts/a/push-history', '.history-row'],
  ['notifications', '/accounts/a/notifications', '.notification-list'],
  ['runtime', '/accounts/a/overview/runtime', '.runtime-details'],
  ['accounts', '/accounts', '.account-list'],
  ['add', '/accounts/new', '.add-fields'],
] as const

async function capture(page: Page, info: TestInfo, name: string) {
  await page.evaluate(() => document.fonts.ready)
  await page.screenshot({ path: info.outputPath(`${name}.png`), fullPage: true, animations: 'disabled' })
}

for (const colorScheme of ['light', 'dark'] as const) {
  for (const width of [320, 390, 820, 1440]) {
    test(`all pages fit ${width}px in ${colorScheme} mode`, async ({ page }, info) => {
      test.setTimeout(90_000)
      const { errors, writes } = await fixture(page, { samples: true })
      await page.setViewportSize({ width, height: width < 760 ? 844 : 1024 })
      await page.emulateMedia({ colorScheme, reducedMotion: 'reduce' })
      for (const [name, path, ready] of scenes) {
        await page.goto(`/#${path}`)
        await expect(page.locator(ready).first()).toBeVisible()
        await expect(page.locator('main h1')).toHaveCount(1)
        await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), { message: `${name} at ${width}px` }).toBe(true)
        const outside = await page.locator('main input, main select, main .save-bar, main .check-row, main .history-row').evaluateAll(elements => elements.filter(element => {
          const box = element.getBoundingClientRect()
          return box.width > 0 && (box.x < -1 || box.right > innerWidth + 1)
        }).map(element => element.tagName + '.' + element.className))
        expect(outside, `${name} controls stay reachable`).toEqual([])
        if (width === 390 || width === 1440) await capture(page, info, name)
      }
      expect(errors).toEqual([])
      expect(writes).toEqual([])
    })
  }
}

test('mobile navigation opens all routes and dismisses with keyboard and outside click', async ({ page }, info) => {
  await fixture(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.goto('/#/accounts/a/overview')
  const trigger = page.getByRole('button', { name: '打开导航' })
  const navigation = page.getByRole('navigation', { name: '主导航', exact: true })
  await trigger.click()
  await expect(navigation).toBeVisible()
  await capture(page, info, 'mobile-navigation')
  await page.keyboard.press('Escape')
  await expect(trigger).toBeFocused()
  await expect(navigation).toBeHidden()
  await trigger.click()
  await page.getByRole('heading', { name: '运行概览', exact: true }).click()
  await expect(navigation).toBeHidden()
  await trigger.click()
  await navigation.getByRole('link', { name: '网络代理', exact: true }).click()
  await expect(page).toHaveURL(/accounts\/a\/proxy$/)
  await expect(navigation).toBeHidden()
  await expect(page.locator('main')).toBeFocused()
  await page.getByRole('button', { name: /切换当前账号/ }).click()
  await capture(page, info, 'mobile-accounts')
})

test('pending form feedback blocks duplicate submits and failures retain the credential draft', async ({ page }, info) => {
  const { errors } = await fixture(page)
  await page.emulateMedia({ reducedMotion: 'reduce' })
  let release: () => void = () => {}
  let submissions = 0
  await page.route('**/api/accounts/a/config', async route => {
    if (route.request().method() === 'GET') return route.fallback()
    submissions++
    await new Promise<void>(resolve => { release = resolve })
    await route.fulfill({ status: 503, json: { error: '合成连接失败，请重试。' } })
  })
  await page.goto('/#/accounts/a/settings/credentials')
  await page.getByLabel('个人 API Token', { exact: true }).fill('synthetic-retained-draft')
  await page.getByRole('button', { name: '保存账号凭据' }).click()
  const saving = page.locator('button[data-status="pending"]')
  await expect(saving).toBeDisabled()
  await expect(saving).toHaveAttribute('aria-busy', 'true')
  await capture(page, info, 'saving')
  release()
  await expect(page.getByRole('alert')).toContainText('合成连接失败')
  await expect(page.getByRole('button', { name: '保存账号凭据' })).toBeEnabled()
  await expect(page.getByLabel('个人 API Token', { exact: true })).toHaveValue('synthetic-retained-draft')
  expect(submissions).toBe(1)
  expect(errors).toEqual([])
  await capture(page, info, 'save-error')
})

test('login, recovery, onboarding, empty states and confirmation remain readable', async ({ page }, info) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await fixture(page, { authenticated: false })
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '连接你的通知助手' })).toBeVisible()
  await capture(page, info, 'login-desktop')
  await page.setViewportSize({ width: 390, height: 844 })
  await capture(page, info, 'login-mobile')
  await page.emulateMedia({ colorScheme: 'dark', reducedMotion: 'reduce' })
  await capture(page, info, 'login-dark-mobile')
  await page.unroute('**/api/**')
  await fixture(page, { onboarding: true })
  await page.goto('/#/accounts/a/overview')
  await page.reload()
  await expect(page.locator('.onboarding')).toBeVisible()
  await capture(page, info, 'onboarding-mobile')
  await page.unroute('**/api/**')
  await fixture(page, { blocked: true })
  await page.goto('/#/accounts/a/overview')
  await page.reload()
  await expect(page.locator('.recovery-page')).toBeVisible()
  await capture(page, info, 'recovery-mobile')
  await page.setViewportSize({ width: 1440, height: 1024 })
  await page.goto('/#/accounts')
  await page.locator('.account-row').first().getByRole('button', { name: '移除账号' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await expect(page.getByRole('button', { name: '取消', exact: true })).toBeFocused()
  await capture(page, info, 'confirmation-dark')
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.route('**/api/accounts/a/check-history', route => route.fulfill({ json: { items: [], limit: 50 } }))
  await page.goto('/#/accounts/a/check-history')
  await expect(page.getByRole('heading', { name: '还没有检查记录' })).toBeVisible()
  await capture(page, info, 'empty-dark')
})
