import { test, expect } from '@playwright/test'
import manifest from '../package.json' with { type: 'json' }
import { fixture } from './fixture'

const buildVersion = process.env.VITE_APP_VERSION?.trim() || `${manifest.version}-dev`
const revision = process.env.VITE_APP_REVISION?.trim() || ''
const label = `版本 ${buildVersion}${revision ? ` · ${revision.slice(0, 7)}` : ''}`

for (const width of [320, 1440]) {
  test(`build identity and app icons are available before and after login at ${width}px`, async ({ page }, info) => {
    const { errors, writes } = await fixture(page, { authenticated: false })
    await page.setViewportSize({ width, height: 900 })
    await page.emulateMedia({ colorScheme: width === 320 ? 'dark' : 'light', reducedMotion: 'reduce' })
    await page.goto('/')
    const versionLabel = page.locator('.build-version')
    await expect(versionLabel).toHaveText(label)
    await expect(versionLabel).toBeVisible()
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: info.outputPath('login-version.png'), fullPage: true })

    const iconLinks = await page.locator('link[rel="icon"], link[rel="apple-touch-icon"]').evaluateAll(links => links.map(link => (link as HTMLLinkElement).href))
    expect(iconLinks).toHaveLength(3)
    for (const url of iconLinks) {
      const response = await page.request.get(url)
      expect(response.ok()).toBe(true)
      expect(response.headers()['content-type']).toMatch(/^image\//)
    }

    await page.getByLabel(/^管理密钥/).fill('synthetic-admin')
    await page.getByRole('button', { name: '进入管理页' }).click()
    await expect(page.locator('.metrics')).toBeVisible()
    await expect(page.locator('.workspace-footer')).toBeVisible()
    await expect(versionLabel).toHaveText(label)
    await versionLabel.scrollIntoViewIfNeeded()
    await expect(versionLabel).toBeInViewport()
    if (revision) await expect(versionLabel).toHaveAttribute('title', `构建提交：${revision}`)
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: info.outputPath('workspace-version.png'), fullPage: true })
    expect(errors).toEqual([])
    expect(writes).toEqual([])
  })
}
