export const pages = {
  overview: {
    path: 'overview',
    title: '运行概览',
    description: '同步、推送和最近通知，一目了然。',
    icon: 'dashboard_4',
  },
  checkHistory: {
    path: 'check-history',
    title: '检查记录',
    description: '通知检查过程；每个账号仅保留最近 50 条。',
    icon: 'check_circle',
  },
  settings: {
    path: 'settings',
    title: '连接与设置',
    description: '按当前任务管理账号凭据、接收设备和同步偏好。',
    icon: 'settings_2',
  },
  credentials: {
    path: 'settings/credentials',
    title: '更新账号凭据',
    description: '查看、复制或替换当前账号的 API Token 与网页 Cookie。',
    icon: 'lock',
  },
  device: { path: 'settings/device', title: '连接与设置', description: '接收设备与配对信息。', icon: 'settings_2' },
  sync: {
    path: 'settings/sync',
    title: '连接与设置',
    description: '调整当前账号的检查与上报行为。',
    icon: 'settings_2',
  },
  proxy: {
    path: 'proxy',
    title: '网络代理',
    description: '为当前账号选择连接方式，测试后保存。',
    icon: 'globe',
  },
  pushTest: {
    path: 'push-test',
    title: '推送测试',
    description: '确认当前账号的推送能否在 App 中显示。',
    icon: 'send_plane',
  },
  pushHistory: {
    path: 'push-history',
    title: '推送历史',
    description: '先看处理结果，查看详情了解时间与上报过程。',
    icon: 'time',
  },
  history: {
    path: 'notifications',
    title: '通知记录',
    description: '已同步到此服务器的通知，显示最近 50 条。',
    icon: 'notification',
  },
  runtime: {
    path: 'overview/runtime',
    title: '同步与额度详情',
    description: '查看检查阶段、通信时间与共享额度。',
    icon: 'dashboard_4',
  },
} as const
export type Page = keyof typeof pages
export type Route =
  { kind: 'home' | 'accounts' | 'add' | 'missing' } | { kind: 'workspace'; accountID: string; page: Page }
export const mainPages: Page[] = ['overview', 'checkHistory', 'settings', 'proxy', 'pushTest', 'pushHistory', 'history']
export function activePage(page: Page): Page {
  return ['credentials', 'device', 'sync'].includes(page) ? 'settings' : page === 'runtime' ? 'overview' : page
}
export function accountHref(id: string, page: Page = 'overview') {
  return `#/accounts/${encodeURIComponent(id)}/${pages[page].path}`
}
export function parseRoute(hash: string): Route {
  const path = hash.replace(/^#/, '').split('?')[0].replace(/\/$/, '')
  if (!path) return { kind: 'home' }
  if (path === '/accounts') return { kind: 'accounts' }
  if (path === '/accounts/new') return { kind: 'add' }
  const match = /^\/accounts\/([^/]+)\/(.+)$/.exec(path)
  if (match) {
    const page = (Object.keys(pages) as Page[]).find((key) => pages[key].path === match[2])
    try {
      if (page) return { kind: 'workspace', accountID: decodeURIComponent(match[1]), page }
    } catch {
      /* Malformed links have a recoverable not-found page. */
    }
  }
  return { kind: 'missing' }
}
export function navigate(href: string, replace = false) {
  if (window.location.hash === href) return
  if (replace) {
    window.history.replaceState(null, '', href)
    window.dispatchEvent(new Event('hashchange'))
  } else window.location.hash = href
}
