import type { Page } from '@playwright/test'

// All endpoints are intercepted. These accounts, credentials and events are synthetic.
export async function fixture(page: Page, options: { authenticated?: boolean; empty?: boolean; blocked?: boolean; resting?: boolean; pairingBlocked?: boolean; samples?: boolean; onboarding?: boolean } = {}) {
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
    if (path === '/api/assistant-connection') return respond({ enabled: false, last_error: '' })
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
          relay_token_configured: !options.onboarding,
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
          initial_pairing_done: !options.onboarding,
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
        push_test: { next_allowed: 0, latest: options.samples ? null : entry(), samples: options.samples ? [
          {kind:'reply',label:'回复',title:'测试用户 回复了你的主题',body:'具体回复内容'},
          {kind:'mention',label:'提及',title:'测试用户 提到了你',body:'去掉本人提及后的内容'},
          {kind:'thanks_topic',label:'感谢主题',title:'测试用户 感谢了你的主题',body:'原主题标题'},
          {kind:'thanks_reply',label:'感谢回复',title:'测试用户 感谢了你的回复',body:'被感谢的回复内容'},
          {kind:'favorite',label:'收藏主题',title:'测试用户 收藏了你的主题',body:'原主题标题'},
        ].map(sample=>({...sample,avatar_url:'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aUioAAAAASUVORK5CYII='})) : [] },
      })
    return respond({ error: 'Unexpected fixture request' }, 404)
  })
  return { writes, config, errors, checks }
}
