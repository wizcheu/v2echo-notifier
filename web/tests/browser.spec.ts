import { test, expect } from '@playwright/test'
import type { Page } from '@playwright/test'

// All endpoints are intercepted. These accounts, credentials and events are synthetic.
async function fixture(page: Page, options: { authenticated?: boolean; empty?: boolean; blocked?: boolean; resting?: boolean; pairingBlocked?: boolean } = {}) {
  let authenticated = options.authenticated ?? true
  const writes: { path: string; body: any }[] = []
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  const config = {
    api_token: 'synthetic-token',
    cookie: 'A2=synthetic-cookie',
    enabled: true,
    interval_seconds: 180,
    push_schedule: { mode: 'window', start: '08:00', end: '24:00' },
    proxy_mode: 'environment',
    proxy_url: '',
    relay_url: 'https://app.v2echo.com/api/push',
    relay_token: 'synthetic-relay',
  }
  let accounts = options.empty
    ? []
    : ['a', 'b'].map((id) => ({
        id,
        username: id === 'a' ? 'sample' : 'second',
        member_id: 1,
        enabled: true,
        verified: true,
        blocked: false,
        cookie_configured: true,
        cookie_verified: true,
        token_issue: '',
        cookie_issue: '',
        last_error: '',
      }))
  const entry = (sequence = 40) => ({
    sequence,
    event_id: `synthetic-event-${sequence}`,
    type: 'unread_summary',
    status: 'pending',
    submitted: true,
    attempts: 2,
    next_attempt: 1900000000,
    detail: '',
    notification_id: 123,
    title: '',
    body: '',
    unread_count: 8,
    created_at: '2026-09-14T01:40:00Z',
    queued_at: 1789350000,
    first_attempt_at: 1789350004,
    last_attempt_at: 1789350004,
    submitted_at: 1789350004,
    finished_at: 0,
  })
  const checks = ['completed', 'completed', 'partial', 'failed', 'completed', 'failed'].map((status, i) => ({
    id: 6 - i, status, trigger: i === 2 ? 'manual' : 'automatic', mode: 'web',
    started_at: `2026-09-15T01:${String(41-i*3).padStart(2,'0')}:00Z`,
    finished_at: `2026-09-15T01:${String(41-i*3).padStart(2,'0')}:01Z`,
    duration_ms: 800, unread_count: status === 'failed' ? null : i === 4 ? 0 : 8,
    first_id: status === 'completed' && i !== 4 ? 123 : null, previous_first_id: 122,
    web: { status: status === 'failed' ? 'failed' : 'completed', http_status: status === 'failed' ? 403 : 200, duration_ms: 400, detail: status === 'failed' ? '无法读取网页未读数' : '已读取本次网页未读数' },
    api: { status: status === 'completed' && i !== 4 ? 'completed' : 'skipped', duration_ms: 400, detail: status === 'partial' ? '共享额度不足，未发起 API 首条消息确认' : '本轮通知 API 检查' },
    summary: ['发现新提醒，已加入上报队列', 'API 首条 ID 未变化', '网页读取成功，API 等待共享额度', '首页请求失败', '没有未读消息', '网页 Cookie 已失效'][i],
    outcome: i === 0 ? '后续上报结果见推送历史' : '本轮未创建上报事件', event_id: i === 0 ? 'synthetic-check-event' : '',
  }))
  await page.route('**/api/**', async (route) => {
    const req = route.request(),
      url = new URL(req.url()),
      path = url.pathname,
      method = req.method()
    const respond = (body: unknown, status = 200) => route.fulfill({ status, json: body })
    if (path === '/api/login') {
      authenticated = true
      return respond({ ok: true })
    }
    if (!authenticated) return respond({ error: '请先登录管理页' }, 401)
    if (path === '/api/logout') {
      authenticated = false
      return respond({ ok: true })
    }
    if (method !== 'GET') {
      const body = req.postDataJSON()
      writes.push({ path, body })
      if (method === 'DELETE') {
        accounts = accounts.filter((a) => !path.endsWith('/' + a.id))
        return respond({ ok: true })
      }
      if (path.endsWith('/config'))
        Object.assign(config, {
          ...body,
          api_token: body.api_token || config.api_token,
          cookie: body.cookie || config.cookie,
        })
      if (path.endsWith('/proxy/test'))
        return respond({
          proxy_mode: body.proxy_mode,
          tested_at: '2026-09-14T01:40:00Z',
          checks: [
            {
              name: 'V2EX',
              url: 'https://v2ex.com',
              connected: true,
              http_status: 200,
              latency_ms: 35,
              message: '连接成功',
            },
          ],
        })
      return respond({ ok: true, id: 'a' })
    }
    if (path === '/api/accounts') return respond({ accounts })
    if (path.endsWith('/config')) return respond(config)
    if (path.endsWith('/check-history')) return respond({ items: checks, limit: 50 })
    if (path.endsWith('/deliveries'))
      return respond({
        items: [entry(url.searchParams.get('before') === '20' ? 19 : 40)],
        next_cursor: url.searchParams.get('before') === '20' ? 0 : 20,
      })
    if (path.endsWith('/status'))
      return respond({
        config: {
          ...config,
          api_token_configured: true,
          cookie_configured: true,
          proxy_url_configured: false,
          proxy_address: '',
          relay_token_configured: true,
        },
        state: {
          token_issue: options.blocked ? '测试凭据需要更新' : '',
          cookie_issue: '',
          token_checked_at: '2026-09-14T01:40:00Z',
          cookie_checked_at: '2026-09-14T01:41:00Z',
          next_token_check: '',
          has_web_unread: true,
          web_unread_count: 8,
          web_observed_at: '2026-09-14T01:41:00Z',
          initial_pairing_done: true,
          initial_unread_count: 8,
          initial_unread_at: '',
          username: path.includes('/b/') ? 'second' : 'sample',
          phase: 'live',
          page: 1,
          verified: true,
          auth_blocked: Boolean(options.blocked),
          last_error: '',
          next_api: '2026-09-14T01:44:00Z',
          next_check: '2026-09-14T01:44:00Z',
          last_success: '2026-09-14T01:41:00Z',
          quota: { limit: 120, remaining: 86, reset: '2026-09-14T02:00:00Z', observed: true },
        },
        push_schedule_status: { timezone: 'UTC+8', server_time: '2026-09-14T18:00:00Z', resting: Boolean(options.resting && config.enabled), next_start: '2026-09-15T00:00:00Z', window_end: '' },
        relay_next_sync: 1900000000,
        relay_blocked: Boolean(options.pairingBlocked),
        pending_count: 2,
        notification_count: 2,
        notifications: [
          { id: 1, title: '有人回复了你的主题', body: '这是一条用于验证布局的合成通知。', created: 1789350000 },
          { id: 2, title: '另一条通知', body: '保留完整的内容与时间。', created: 1789263600 },
        ],
        deliveries: [],
        push_test: { next_allowed: 0, latest: entry() },
      })
    return respond({ error: 'Unexpected fixture request' }, 404)
  })
  return { writes, config, errors, checks }
}

test('every tab and settings subpage survives navigation and refresh; account stays in URL', async ({ page }) => {
  const { errors } = await fixture(page)
  await page.goto('/#/accounts/b/settings/device')
  await expect(page.getByRole('heading', { name: '当前接收账号' })).toBeVisible()
  await expect(page.getByRole('button', { name: /切换当前账号/ })).toHaveAccessibleName('切换当前账号，当前 @second')
  await page.getByRole('link', { name: '同步偏好', exact: true }).click()
  await expect(page).toHaveURL(/b\/settings\/sync$/)
  await page.goBack()
  await expect(page).toHaveURL(/b\/settings\/device$/)
  await page.goForward()
  await page.reload()
  await expect(page.getByRole('spinbutton', { name: '期望检查间隔（秒）' })).toHaveValue('180')
  for (const [label, path] of [
    ['运行概览', 'overview'],
    ['检查记录', 'check-history'],
    ['网络代理', 'proxy'],
    ['推送测试', 'push-test'],
    ['推送历史', 'push-history'],
    ['通知记录', 'notifications'],
    ['连接与设置', 'settings'],
  ]) {
    await page.getByRole('navigation', { name: '主导航' }).getByRole('link', { name: label, exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`/b/${path}$`))
    await page.reload()
    await expect(page.getByRole('heading', { name: label, exact: true }).first()).toBeVisible()
  }
  await expect(page.getByRole('button', { name: /切换当前账号/ })).toHaveAccessibleName('切换当前账号，当前 @second')
  expect(errors).toEqual([])
})

test('credentials and sync save independently without submitting the other page draft', async ({ page }) => {
  const { writes, config } = await fixture(page)
  await page.goto('/#/accounts/a/settings/credentials')
  await page.getByLabel('个人 API Token', { exact: true }).fill('synthetic-updated')
  await page.getByRole('link', { name: '返回设置', exact: true }).click()
  await page.getByRole('link', { name: '同步偏好', exact: true }).click()
  await page.getByRole('spinbutton').fill('240')
  await page.getByRole('switch').uncheck()
  await page.getByRole('button', { name: '保存同步偏好' }).click()
  await expect(page.getByRole('status')).toContainText('同步偏好已保存')
  expect(writes[0].body).toEqual({ api_token: '', cookie: '', enabled: false, interval_seconds: 240, push_schedule: { mode: 'window', start: '08:00', end: '24:00' } })
  await page.getByRole('link', { name: '账号凭据', exact: true }).click()
  await page.getByRole('link', { name: /个人 API Token/ }).click()
  await expect(page.getByLabel('个人 API Token', { exact: true })).toHaveValue('synthetic-updated')
  await page.getByRole('button', { name: '保存账号凭据' }).click()
  await expect(page.getByRole('status')).toContainText('凭据已保存')
  expect(writes[1].body.interval_seconds).toBe(240)
  expect(writes[1].body.enabled).toBe(false)
  expect(config.api_token).toBe('synthetic-updated')
})

test('history filters, pagination and event detail restore with back, forward and reload', async ({ page }) => {
  await fixture(page)
  await page.goto('/#/accounts/a/overview')
  await page.getByRole('link', { name: /查看处理进度/ }).click()
  await expect(page.getByLabel('处理状态')).toHaveValue('pending')
  await page.getByLabel('搜索内容').fill('合成 & 测试')
  await page.getByRole('button', { name: '搜索', exact: true }).click()
  await page.getByRole('button', { name: '下一页' }).click()
  await page.locator('summary.history-row').click()
  await expect(page.locator('.push-detail')).toContainText('synthetic-event-19')
  await page.reload()
  await expect(page).toHaveURL(/q=/)
  await expect(page.locator('.push-detail')).toContainText('synthetic-event-19')
  await page.goBack()
  await expect(page.getByLabel('搜索内容')).toHaveValue('合成 & 测试')
  await page.goForward()
  await expect(page.getByRole('heading', { name: '推送详情', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '返回推送历史' }).click()
  await page.getByRole('button', { name: '上一页' }).click()
  await expect(page.getByText('第 1 页 · 每页 20 条')).toBeVisible()
})

test('login preserves a deep link, missing accounts do not open another account', async ({ page }) => {
  await fixture(page, { authenticated: false })
  await page.goto('/#/accounts/b/settings/sync')
  await expect(page.getByLabel(/^管理密钥/)).toHaveAttribute('type', 'password')
  await page.getByLabel(/^管理密钥/).fill('synthetic-admin')
  await page.getByRole('button', { name: '进入管理页' }).click()
  await expect(page.getByRole('spinbutton')).toBeVisible()
  await expect(page).toHaveURL(/b\/settings\/sync$/)
  await page.goto('/#/accounts/removed/settings')
  await expect(page.getByRole('heading', { name: '账号不存在或已移除' })).toBeVisible()
  await expect(page.getByRole('button', { name: '移除账号', exact: true })).toHaveCount(0)
})

test('proxy test uses the draft and leaves saving explicit; account removal requires confirmation', async ({
  page,
}) => {
  const { writes } = await fixture(page)
  await page.goto('/#/accounts/a/proxy')
  await page.getByRole('radio', { name: '自定义代理' }).check()
  await page.getByLabel(/^代理地址/).fill('http://synthetic:8080')
  await page.getByRole('button', { name: '测试连接', exact: true }).click()
  await expect(page.getByRole('heading', { name: '连接测试结果' })).toBeVisible()
  expect(writes).toHaveLength(1)
  expect(writes[0]).toEqual({
    path: '/api/accounts/a/proxy/test',
    body: { proxy_mode: 'custom', proxy_url: 'http://synthetic:8080' },
  })
  await page.getByRole('link', { name: '账号管理', exact: true }).click()
  const account = page
    .locator('.account-row')
    .filter({ has: page.getByRole('heading', { name: '@second', exact: true }) })
  await account.getByRole('button', { name: '移除账号' }).click()
  expect(writes).toHaveLength(1)
  await page.getByRole('dialog').getByRole('button', { name: '取消' }).click()
  await expect(account.getByRole('button', { name: '移除账号' })).toBeVisible()
  await page.mouse.move(0, 0)
  await page.screenshot({ path: '/tmp/v2echo-account-actions-desktop.png', fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: '/tmp/v2echo-account-actions-mobile.png', fullPage: true })
})

test('mobile and dark layouts fit the viewport, and navigation stays keyboard accessible', async ({ page }) => {
  const { errors } = await fixture(page)
  await page.setViewportSize({ width: 390, height: 844 })
  for (const route of [
    'overview',
    'settings',
    'settings/credentials',
    'settings/device',
    'settings/sync',
    'proxy',
    'push-test',
    'push-history',
    'notifications',
  ]) {
    await page.goto(`/#/accounts/a/${route}`)
    await expect(page.locator('.page-header')).toBeVisible()
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  }
  await page.getByRole('navigation', { name: '移动主导航' }).getByRole('link', { name: '概览' }).click()
  await expect(page).toHaveURL(/accounts\/a\/overview$/)
  await page.screenshot({ path: 'test-results/overview-mobile.png', fullPage: true })
  await page.setViewportSize({ width: 1440, height: 1024 })
  await page.screenshot({ path: 'test-results/overview-desktop.png', fullPage: true })
  await page.emulateMedia({ colorScheme: 'dark' })
  await page.goto('/#/accounts/a/settings')
  await page.screenshot({ path: 'test-results/settings-dark.png', fullPage: true })
  expect(errors).toEqual([])
})

test('expired credentials disable checking and testing; an empty server can add its first account', async ({
  page,
}) => {
  const { writes } = await fixture(page, { blocked: true })
  await page.goto('/#/accounts/a/overview')
  await expect(page.getByRole('button', { name: '更新 Token' })).toBeVisible()
  await expect(page.getByRole('button', { name: '立即检查' })).toHaveCount(0)
  await page.getByRole('navigation', { name: '主导航' }).getByRole('link', { name: '推送测试', exact: true }).click()
  await expect(page.getByRole('button', { name: '发送测试推送', exact: true })).toBeDisabled()
  expect(writes).toEqual([])
  await page.unroute('**/api/**')
  await fixture(page, { empty: true })
  await page.goto('/')
  await expect(page).toHaveURL(/accounts\/new$/)
  await expect(page.getByRole('heading', { name: '添加 V2EX 账号' })).toBeVisible()
})

test('account menu supports selection, keyboard dismissal and management shortcuts', async ({ page }) => {
  await fixture(page)
  await page.goto('/#/accounts/a/settings/sync')
  const trigger = page.getByRole('button', { name: /切换当前账号/ })
  await trigger.click()
  await expect(page.getByRole('menuitemradio', { name: /@sample/ })).toHaveAttribute('aria-checked', 'true')
  await expect(page.getByRole('menuitemradio', { name: /@sample/ })).toBeFocused()
  await page.keyboard.press('ArrowDown')
  await expect(page.getByRole('menuitemradio', { name: /@second/ })).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/accounts\/b\/settings\/sync$/)
  await expect(trigger).toHaveAccessibleName('切换当前账号，当前 @second')
  await expect(page.getByRole('menu', { name: '切换账号' })).toHaveCount(0)
  await page.goBack()
  await expect(trigger).toHaveAccessibleName('切换当前账号，当前 @sample')
  await trigger.press('ArrowDown')
  await page.keyboard.press('Escape')
  await expect(trigger).toBeFocused()
  await expect(trigger).toHaveAttribute('aria-expanded', 'false')
  await trigger.click()
  await page.getByRole('heading', { name: '同步与上报' }).click()
  await expect(trigger).toHaveAttribute('aria-expanded', 'false')
  await trigger.click()
  await page.getByRole('menuitem', { name: '添加账号', exact: true }).click()
  await expect(page).toHaveURL(/accounts\/new$/)
  await page.goBack()
  await trigger.click()
  await page.getByRole('menuitem', { name: '管理账号', exact: true }).click()
  await expect(page).toHaveURL(/#\/accounts$/)
})

test('mobile account menu stays within the screen and closes when tabbing away', async ({ page }) => {
  await fixture(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/#/accounts/a/overview')
  await page.getByRole('button', { name: /切换当前账号/ }).click()
  const menu = page.getByRole('menu', { name: '切换账号' })
  const box = await menu.boundingBox()
  expect(box!.x).toBeGreaterThanOrEqual(0)
  expect(box!.x + box!.width).toBeLessThanOrEqual(390)
  await page.keyboard.press('End')
  await expect(page.getByRole('menuitem', { name: '退出管理页' })).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(menu).toHaveCount(0)
})


test('settings segments keep card bounds; check interval cannot save below 30 seconds', async ({ page }) => {
  const { writes } = await fixture(page)
  await page.goto('/#/accounts/a/settings')
  const card = page.locator('.settings-card')
  await expect(card).toBeVisible()
  const initial = await card.boundingBox()
  expect(initial).not.toBeNull()
  for (const label of ['接收设备', '同步偏好', '账号凭据']) {
    await page.getByRole('navigation', { name: '设置分类' }).getByRole('link', { name: label, exact: true }).click()
    await expect(card).toBeVisible()
    const bounds = await card.boundingBox()
    expect(bounds).not.toBeNull()
    expect(bounds!.x).toBeCloseTo(initial!.x, 1)
    expect(bounds!.width).toBeCloseTo(initial!.width, 1)
  }
  await page.getByRole('link', { name: '同步偏好', exact: true }).click()
  const interval = page.getByRole('spinbutton', { name: '期望检查间隔（秒）' })
  await interval.fill('1')
  await interval.press('Tab')
  await expect(interval).toHaveValue('30')
  await interval.focus()
  await interval.press('ArrowDown')
  await expect(interval).toHaveValue('30')
  await page.getByRole('button', { name: '保存同步偏好', exact: true }).click()
  await expect(page.getByRole('status')).toContainText('同步偏好已保存')
  expect(writes[0].body.interval_seconds).toBe(30)
  await interval.fill('180')
  await interval.press('Tab')
  await expect(interval).toHaveValue('180')
})


test('check history has an independent tab, durable filters and expandable observations', async ({ page }) => {
  const { writes, errors } = await fixture(page)
  await page.goto('/#/accounts/a/check-history')
  await expect(page.locator('#main-navigation a[aria-current="page"]')).toHaveText('检查记录')
  await expect(page.locator('.check-list>li')).toHaveCount(6)
  await expect(page.locator('.check-unread').nth(3)).toContainText('—')
  await expect(page.locator('.check-unread').nth(4)).toContainText('0 条')
  await page.locator('.check-expand').nth(2).click()
  await expect(page).toHaveURL(/record=4/)
  await expect(page.locator('.check-detail')).toContainText('共享额度不足')
  await page.reload()
  await expect(page.locator('.check-detail')).toBeVisible()
  await page.getByRole('link', { name: '需要关注 3' }).click()
  await expect(page.locator('.check-list>li')).toHaveCount(3)
  await page.goBack()
  await expect(page.locator('.check-detail')).toBeVisible()
  await page.goForward()
  await expect(page.locator('.check-list>li')).toHaveCount(3)
  await page.getByRole('button', { name: '刷新记录', exact: true }).click()
  await expect(page.getByRole('button', { name: '刷新记录', exact: true })).toBeEnabled()
  expect(writes).toEqual([])
  expect(errors).toEqual([])
})

test('check history preserves expanded rows during polling and recovers empty and error states', async ({ page }) => {
  const { checks } = await fixture(page)
  await page.clock.install()
  await page.goto('/#/accounts/a/check-history?record=4')
  await expect(page.locator('.check-detail')).toBeVisible()
  checks.unshift({ ...checks[0], id: 7 })
  await page.clock.fastForward(6000)
  await expect(page.getByText('有新的检查记录，当前阅读位置已保留。')).toBeVisible()
  await expect(page.locator('.check-list>li')).toHaveCount(6)
  await page.getByRole('button', { name: '显示最新记录' }).click()
  await expect(page.locator('.check-list>li')).toHaveCount(7)
  await expect(page.locator('.check-detail')).toBeVisible()
  checks.splice(0)
  await page.getByRole('button', { name: '刷新记录', exact: true }).click()
  await expect(page.getByText('这条记录已不在最近 50 条中。')).toBeVisible()
  await expect(page.getByRole('heading', { name: '还没有检查记录' })).toBeVisible()
  await page.route('**/api/accounts/a/check-history', route => route.fulfill({ status: 500, json: { error: '合成读取错误' } }))
  await page.getByRole('button', { name: '刷新记录', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('合成读取错误')
  await page.unroute('**/api/accounts/a/check-history')
  await page.getByRole('button', { name: '重试', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveCount(0)
})

test('check history desktop and mobile layouts keep four bottom tabs and aligned content', async ({ page }) => {
  await fixture(page)
  await page.goto('/#/accounts/a/check-history')
  await expect(page.locator('.check-list>li')).toHaveCount(6)
  await page.screenshot({ path: '/tmp/v2echo-check-history-desktop.png', fullPage: true })
  await page.locator('.check-expand').first().click()
  await page.locator('.check-expand').first().hover()
  await page.screenshot({ path: '/tmp/v2echo-check-history-expanded-hover.png', fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.locator('.mobile-tabs a')).toHaveCount(4)
  await expect(page.locator('.mobile-tabs a[aria-current="page"]')).toHaveText('检查记录')
  await page.locator('.check-expand').nth(2).click()
  await expect(page.locator('.check-detail')).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy()
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.mouse.move(0, 0)
  await page.screenshot({ path: '/tmp/v2echo-check-history-mobile.png', fullPage: true })
  for (const width of [320, 820]) {
    await page.setViewportSize({ width, height: 844 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy()
  }
})


test('push schedule saves overnight times, preserves drafts and rejects an empty window', async ({ page }) => {
  const { writes, errors } = await fixture(page)
  await page.goto('/#/accounts/a/settings/sync')
  const start = page.getByLabel('允许推送 · 开始'), end = page.getByLabel(/允许推送 · 结束/)
  await expect(start).toHaveValue('08:00')
  await expect(end).toHaveValue('24:00')
  await expect(page.locator('.schedule-summary')).toContainText('休息 00:00–08:00')
  await page.screenshot({ path: '/tmp/v2echo-schedule-desktop.png', fullPage: true })
  const width = (await page.locator('form.settings-card').boundingBox())?.width
  await start.fill('22:00'); await end.fill('08:30')
  await expect(page.locator('.schedule-summary')).toContainText('休息 08:30–22:00')
  await page.getByRole('radio', { name: '全天运行', exact: true }).check()
  await expect(start).toHaveCount(0)
  expect((await page.locator('form.settings-card').boundingBox())?.width).toEqual(width)
  await page.getByRole('radio', { name: '指定时段', exact: true }).check()
  await expect(start).toHaveValue('22:00')
  await expect(end).toHaveValue('08:30')
  await page.getByRole('button', { name: '保存同步偏好' }).click()
  await expect(page.getByRole('status')).toContainText('UTC+8')
  expect(writes.at(-1)?.body.push_schedule).toEqual({ mode: 'window', start: '22:00', end: '08:30' })
  await page.reload()
  await expect(start).toHaveValue('22:00')
  await end.fill('22:00')
  await expect(page.locator('#schedule-error')).toContainText('开始与结束不能相同')
  const saved = writes.length
  await page.getByRole('button', { name: '保存同步偏好' }).click()
  expect(writes.length).toEqual(saved)
  await end.fill('08:30')
  await page.getByRole('radio', { name: '全天运行', exact: true }).check()
  await page.getByRole('button', { name: '保存同步偏好' }).click()
  await expect(page.getByRole('status')).toContainText('全天运行')
  await page.reload()
  await expect(page.getByRole('radio', { name: '全天运行', exact: true })).toBeChecked()
  expect(errors).toEqual([])
})

test('resting overview and push test explain pause; schedule fits mobile widths', async ({ page }) => {
  const { writes, errors } = await fixture(page, { resting: true })
  await page.goto('/#/accounts/a/overview')
  await expect(page.getByRole('heading', { name: '休息时段，暂不打扰' })).toBeVisible()
  await expect(page.locator('.health-summary')).toContainText('自动检查已暂停 · 推送已暂停')
  await page.screenshot({ path: '/tmp/v2echo-rest-desktop.png', fullPage: true })
  await page.getByRole('link', { name: '调整推送时段', exact: true }).click()
  await expect(page.getByLabel('允许推送 · 开始')).toHaveValue('08:00')
  for (const width of [390, 320, 820]) {
    await page.setViewportSize({ width, height: 844 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy()
    if (width === 390) await page.screenshot({ path: '/tmp/v2echo-schedule-mobile.png', fullPage: true })
  }
  await page.goto('/#/accounts/a/push-test')
  await expect(page.getByRole('button', { name: '休息时段，暂不测试' })).toBeDisabled()
  await expect(page.locator('.push-test')).toContainText('UTC+8')
  expect(writes).toEqual([])
  expect(errors).toEqual([])
})


test('pausing sync preserves the saved schedule even with an unfinished time edit', async ({ page }) => {
  const { writes } = await fixture(page)
  await page.goto('/#/accounts/a/settings/sync')
  await page.getByLabel('允许推送 · 开始').fill('1')
  await page.getByRole('switch').uncheck()
  await expect(page.getByLabel('允许推送 · 开始')).toBeDisabled()
  await page.getByRole('button', { name: '保存同步偏好' }).click()
  await expect(page.getByRole('status')).toContainText('同步仍处于暂停状态')
  expect(writes.at(-1)?.body).toMatchObject({ enabled: false, push_schedule: { mode: 'window', start: '08:00', end: '24:00' } })
  await expect(page.getByLabel('允许推送 · 开始')).toHaveValue('08:00')
})


test('invalid pairing pauses checks and links to device recovery', async ({ page }) => {
  const { writes } = await fixture(page, { pairingBlocked: true })
  await page.goto('/#/accounts/a/overview')
  await expect(page.getByRole('heading', { name: '需重新配对，检查已暂停' })).toBeVisible()
  await expect(page.getByRole('button', { name: '立即检查', exact: true })).toHaveCount(0)
  await page.getByRole('link', { name: '重新配对设备', exact: true }).click()
  await expect(page).toHaveURL(/settings\/device/)
  await expect(page.getByText('接收设备需重新配对，自动检查与推送已暂停。', { exact: true })).toBeVisible()
  await page.goto('/#/accounts/a/overview/runtime')
  await expect(page.getByRole('button', { name: '立即检查', exact: true })).toBeDisabled()
  expect(writes.filter(write => write.path.endsWith('/check'))).toHaveLength(0)
})
