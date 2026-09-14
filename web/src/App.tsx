import { useCallback, useEffect, useRef, useState } from 'react'
import type { FormEvent, ReactNode } from 'react'
import PushHistory from './PushHistory'
import type { PushHistoryPage } from './PushHistory'
import PushTest, { pushServiceURL } from './PushTest'
import type { PushTestState } from './PushTest'

type ProxyMode = 'environment' | 'direct' | 'custom'
type Config = { cookie_configured: boolean; proxy_mode: ProxyMode; proxy_url_configured: boolean; proxy_address: string; enabled: boolean; interval_seconds: number; relay_url: string; api_token_configured: boolean; relay_token_configured: boolean }
type State = {
  has_web_unread: boolean; web_unread_count: number; web_observed_at: string;
  initial_pairing_done: boolean; initial_unread_count: number; initial_unread_at: string
  username: string; phase: string; page: number; verified: boolean; auth_blocked: boolean
  last_error: string; next_api: string; next_check: string; last_success: string
  quota: { limit: number; remaining: number; reset: string; observed: boolean }
}
type Snapshot = {
  push_test: PushTestState; config: Config; state: State; relay_next_sync: number; relay_blocked: boolean; notification_count: number; pending_count: number
  notifications: { id: number; title: string; body: string; created: number }[]
  deliveries: { type: string; event_id: string; status: string; submitted: boolean; attempts: number; detail: string }[]
}
type ProxyDraft = { proxy_mode: ProxyMode; proxy_url: string }
type ProxyResult = { proxy_mode: ProxyMode; tested_at: string; checks: { name: string; url: string; connected: boolean; http_status: number; latency_ms: number; message: string }[] }
type Draft = { cookie: string; api_token: string; interval_seconds: number; enabled: boolean }
const initialDraft: Draft = { cookie: '', api_token: '', interval_seconds: 180, enabled: false }
const phases: Record<string, string> = { history: '导入历史通知', catchup: '补扫导入期间的新通知', live: '检查增量通知' }
const pages = {
  overview: { title: '运行概览', description: '查看通知同步、推送连接与可用额度。' },
  settings: { title: '连接与设置', description: '管理当前账号的凭据、接收设备和同步偏好。' },
  proxy: { title: '网络代理', description: '设置这台服务器访问通知与推送服务的连接方式。' },
  pushTest: { title: '推送测试', description: '发送一条测试消息，检查当前账号到 App 的推送连接。' },
  pushHistory: { title: '推送历史', description: '回看上报过的内容，追踪每条事件的处理结果。' },
  history: { title: '通知记录', description: '查看当前账号已同步到服务器的通知。' },
}

class APIError extends Error { constructor(message: string, public status: number) { super(message) } }
async function api<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
  const response = await fetch(`/api/${path}`, {
    method, credentials: 'same-origin', signal: AbortSignal.timeout(30_000),
    headers: { 'Content-Type': 'application/json', 'X-V2Echo-Request': '1' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const result = await response.json()
  if (!response.ok) throw new APIError(result.error ?? '请求失败', response.status)
  return result as T
}
function time(value: string | number) {
  if (!value || (typeof value === 'string' && value.startsWith('0001'))) return '尚无记录'
  return new Date(typeof value === 'number' ? value * 1000 : value).toLocaleString('zh-CN', { hour12: false })
}

type Account = { id: string; username: string; member_id: number; enabled: boolean; verified: boolean; blocked: boolean }

export default function App() {
  const [accounts, setAccounts] = useState<Account[]>([])
  const [selected, setSelected] = useState('')
  const [authenticated, setAuthenticated] = useState<boolean | null>(null)
  const [adding, setAdding] = useState(false)
  const [credential, setCredential] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const listGeneration = useRef(0)
  const sessionExpired = useCallback(() => {
    listGeneration.current++; setAuthenticated(false); setAccounts([]); setSelected(''); setCredential(''); setAdding(false)
  }, [])
  const refreshAccounts = useCallback(async () => {
    const version = ++listGeneration.current
    try {
      const result = await api<{ accounts: Account[] }>('accounts')
      if (version !== listGeneration.current) return
      setAccounts(result.accounts); setAuthenticated(true); setError('')
      setSelected(old => result.accounts.some(a => a.id === old) ? old : result.accounts[0]?.id ?? '')
    } catch (e) {
      if (version !== listGeneration.current) return
      if (e instanceof APIError && e.status === 401) sessionExpired()
      else { setError('无法读取账号列表，请检查连接。'); setAuthenticated(v => v ?? false) }
    }
  }, [sessionExpired])
  useEffect(() => { void refreshAccounts(); return () => { listGeneration.current++ } }, [refreshAccounts])
  useEffect(() => {
    if (!authenticated) return
    const timer = window.setInterval(() => void refreshAccounts(), 5000)
    return () => window.clearInterval(timer)
  }, [authenticated, refreshAccounts])
  async function submit(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError('')
    try {
      if (!authenticated) { await api('login', 'POST', { token: credential }); await refreshAccounts() }
      else { const result = await api<{ id: string }>('accounts', 'POST', { api_token: credential }); await refreshAccounts(); setSelected(result.id); setAdding(false) }
      setCredential('')
    } catch (e) { setError(e instanceof Error ? e.message : '保存失败') }
    finally { setBusy(false) }
  }
  async function logout() { await api('logout', 'POST', {}); sessionExpired() }
  async function removeAccount(id: string) {
    await api(`accounts/${encodeURIComponent(id)}`, 'DELETE', {})
    await refreshAccounts()
  }
  if (authenticated === null) return <main className="login-shell"><p role="status">正在连接通知助手…</p></main>
  if (!authenticated || adding || !accounts.length) return <main className="login-shell"><form className="login-panel" onSubmit={submit}>
    <div className="wordmark">V2Echo <span>通知助手</span></div>
    <h1>{authenticated ? '添加 V2EX 账号' : '连接你的通知助手'}</h1>
    <p className="muted">{authenticated ? '每个账号独立保存 Token、同步进度和通知记录。保存后会按共享额度验证身份，账号名由 V2EX 返回。' : '输入服务器数据目录 admin-token 文件中的管理密钥。'}</p>
    <label>{authenticated ? 'V2EX API Token' : '管理密钥'}<input type="text" autoComplete="off" required maxLength={4096} value={credential} onChange={e => setCredential(e.target.value)} /></label>
    {error && <p className="notice error" role="alert">{error}</p>}
    <button className="primary" disabled={busy}>{busy ? '处理中…' : authenticated ? '保存并验证账号' : '进入管理页'}</button>
    {authenticated && accounts.length > 0 && <button type="button" disabled={busy} onClick={() => { setAdding(false); setCredential(''); setError('') }}>返回账号</button>}
    {authenticated && <button type="button" disabled={busy} onClick={() => { setBusy(true); void logout().catch(() => setError('退出失败，请重试')).finally(() => setBusy(false)) }}>退出管理页</button>}
    <p className="caption">一个管理密钥可管理这里的全部账号，请只交给可信的管理员。</p>
  </form></main>
  return <AccountWorkspace key={selected} accountID={selected} onExpired={sessionExpired} onLogout={logout} onRemove={() => removeAccount(selected)} accountControls={<>
    <label>当前管理的账号<select value={selected} onChange={e => setSelected(e.target.value)}>{accounts.map(account => <option key={account.id} value={account.id}>{account.username ? `@${account.username}` : `待验证 · ${account.id.slice(0, 6)}`}{account.blocked ? ' · 需处理' : !account.enabled ? ' · 已暂停' : ''}</option>)}</select></label>
    <button className="text-button" type="button" onClick={() => { setCredential(''); setError(''); setAdding(true) }}>添加账号</button>
    {error && <p className="notice error" role="alert">{error}</p>}
  </>} />
}

function AccountWorkspace({ accountID, accountControls, onExpired, onLogout, onRemove }: {
  accountID: string; accountControls: ReactNode; onExpired: () => void; onLogout: () => Promise<void>; onRemove: () => Promise<void>
}) {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null)
  const [tab, setTab] = useState<'overview' | 'settings' | 'proxy' | 'history' | 'pushHistory' | 'pushTest'>('overview')
  const [draft, setDraft] = useState<Draft>(initialDraft)
  const [proxyDraft, setProxyDraft] = useState<ProxyDraft>({ proxy_mode: 'environment', proxy_url: '' })
  const [proxyResult, setProxyResult] = useState<ProxyResult | null>(null)
  const proxySeeded = useRef(false)
  const refreshVersion = useRef(0)
  const [pairCode, setPairCode] = useState('')
  const [pairCookie, setPairCookie] = useState('')
  const testRequestID = useRef('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [connectionError, setConnectionError] = useState('')
  const [busy, setBusy] = useState(false)
  const [confirmRemove, setConfirmRemove] = useState(false)
  const seeded = useRef(false)
  const active = useRef(true)
  const accountPath = useCallback((path: string) => `accounts/${encodeURIComponent(accountID)}/${path}`, [accountID])
  const loadDeliveries = useCallback(async (query: string) => {
    try { return await api<PushHistoryPage>(`${accountPath('deliveries')}?${query}`) }
    catch (e) { if (e instanceof APIError && e.status === 401) onExpired(); throw e }
  }, [accountPath, onExpired])
  const refreshing = useRef(false)
  const refresh = useCallback(async (force = false) => {
    if (refreshing.current && !force) return
    refreshing.current = true
    const version = ++refreshVersion.current
    try {
      const data = await api<Snapshot>(accountPath('status'))
      if (!active.current || version !== refreshVersion.current) return
      setSnapshot(data)
      setConnectionError('')
      if (!proxySeeded.current) { setProxyDraft({ proxy_mode: data.config.proxy_mode, proxy_url: '' }); proxySeeded.current = true }
      if (!seeded.current) {
        setDraft({ ...initialDraft, enabled: data.config.enabled, interval_seconds: data.config.interval_seconds })
        seeded.current = true
      }
    } catch (e) {
      if (!active.current || version !== refreshVersion.current) return
      if (e instanceof APIError && e.status === 401) {
        if (active.current) onExpired()
      } else { setConnectionError('无法连接通知助手，当前显示的可能是上次读取的状态。'); }
    } finally { if (version === refreshVersion.current) refreshing.current = false }
  }, [accountPath, onExpired])
  useEffect(() => { active.current = true; void refresh(); return () => { active.current = false } }, [refresh])
  useEffect(() => {
    const timer = window.setInterval(() => void refresh(), 5000)
    return () => window.clearInterval(timer)
  }, [refresh])

  async function action(fn: () => Promise<void>) {
    setBusy(true); setError(''); setMessage('')
    try { await fn() } catch (e) {
      setError(e instanceof Error ? e.message : '操作失败，请重试')
      if (active.current && e instanceof APIError && e.status === 401) onExpired()
    } finally { setBusy(false) }
  }
  function save(event: FormEvent) {
    event.preventDefault()
    void action(async () => {
      await api(accountPath('config'), 'PUT', draft)
      refreshVersion.current++
      seeded.current = false
      setDraft(d => ({ ...d, api_token: '', cookie: '' }))
      await refresh(true); setMessage('设置已保存。启用后将按可用额度执行检查。')
    })
  }

  if (!snapshot) return <main className="login-shell"><div className="login-panel">
    <div className="account-controls">{accountControls}</div>
    {connectionError ? <><p className="notice error" role="alert">{connectionError}</p><button onClick={() => void refresh()}>重新连接</button></> : <p role="status">正在读取状态…</p>}
  </div></main>

  const { config, state, notifications } = snapshot
  const configured = config.api_token_configured
  const relayReady = Boolean(config.relay_url === pushServiceURL && config.relay_token_configured)
  const phase = !configured ? '等待配置' : !config.enabled ? '已暂停' : state.auth_blocked ? '需要更新 Token' : !state.verified ? '验证账号' : phases[state.phase] ?? state.phase
  const next = new Date(state.next_api) > new Date(state.next_check) ? state.next_api : state.next_check
  return <div className="app-shell">
    <a className="skip-link" href="#main-content">跳到页面内容</a>
    <aside className="sidebar">
      <div className="wordmark">V2Echo<span>自托管通知助手</span></div>
      <div className="account-controls">{accountControls}</div>
      <nav aria-label="主导航">
        {([['overview', '运行概览'], ['settings', '连接与设置'], ['proxy', '网络代理'], ['pushTest', '推送测试'], ['pushHistory', '推送历史'], ['history', '通知记录']] as const).map(([key, label]) =>
          <button key={key} className={tab === key ? 'nav-item selected' : 'nav-item'} aria-current={tab === key ? 'page' : undefined} onClick={() => setTab(key)}>{label}</button>)}
      </nav>
      <div className="sidebar-footer"><span>凭据保存在这台服务器</span><button className="text-button" disabled={busy} onClick={() => void action(async () => { await onLogout() })}>退出管理页</button></div>
    </aside>
    <main className="workspace" id="main-content" tabIndex={-1}>
      <header className="page-header">
        <div><h1>{pages[tab].title}</h1><p className="page-description">{pages[tab].description}</p></div>
        <span className={`status-pill ${state.auth_blocked ? 'warning' : !config.enabled ? 'neutral' : ''}`}>{phase}</span>
      </header>
      {connectionError && <div className="notice error" role="alert"><span>{connectionError}</span><button onClick={() => void refresh()}>重新连接</button></div>}
      {error && <div className="notice error" role="alert">{error}<button className="text-button" onClick={() => setError('')}>关闭</button></div>}
      {message && <div className="notice" role="status">{message}</div>}
      {state.last_error && <div className="notice error" role="alert">{state.last_error}</div>}

      {tab === 'overview' && <>
        {!config.cookie_configured && <section className="setup-panel"><div><h2>让提醒显示网页未读总数</h2><p>保存登录 Cookie 后，由 API 首条消息 ID 的变化触发提醒、网页提供未读数量，每轮最多一条提醒。未配置时沿用逐条通知提醒。</p></div><button onClick={() => setTab('settings')}>配置 Cookie</button></section>}
        {!configured && <section className="setup-panel"><div><h2>先连接你的 V2EX 账号</h2><p>使用个人 API Token 导入历史通知，然后自动检测新增通知。</p></div><button className="primary" onClick={() => setTab('settings')}>配置连接</button></section>}
        <div className="metrics">
          <div><span>网页未读快照</span><strong>{state.has_web_unread ? state.web_unread_count : '—'}</strong><small>{state.has_web_unread ? time(state.web_observed_at) : '保存 Cookie 后读取'}</small></div>
          <div><span>待处理事件</span><strong>{snapshot.pending_count.toLocaleString()}</strong><small>{relayReady ? '等待上报或服务端回执' : '配置推送服务后开始上报'}</small></div>
          <div><span>所有账号共享额度</span><strong>{state.quota.limit ? state.quota.remaining : '—'}<em>{state.quota.limit ? ` / ${state.quota.limit}` : ''}</em></strong><small>{state.quota.observed ? '来自最近一次 API 响应' : '尚无响应额度，使用保守预算'}</small></div>
        </div>
        <div className="overview-grid"><section className="panel">
          <div className="section-heading"><h2>同步状态</h2><button disabled={busy || !config.enabled || state.auth_blocked} onClick={() => void action(async () => { await api(accountPath('check'), 'POST', {}); await refresh(); setMessage('已安排检查，仍会遵守额度限制和错误退避。') })}>立即检查</button></div>
          <dl className="details">
            <div><dt>当前阶段</dt><dd>{phase}{state.verified && state.phase !== 'live' ? ` · 第 ${state.page} 页` : ''}</dd></div>
            <div><dt>最近检查完成</dt><dd>{time(state.last_success)}</dd></div>
            <div><dt>下次允许检查</dt><dd>{config.enabled && !state.auth_blocked ? time(next) : '等待启用或更新凭据'}</dd></div>
            <div><dt>期望检查间隔</dt><dd>{config.interval_seconds} 秒 · 额度不足时自动延长</dd></div>
            <div><dt>额度窗口结束</dt><dd>{time(state.quota.reset)}</dd></div>
          </dl>
          <p className="caption">初次导入的历史通知不会推送。API 返回的是通知记录，本页数量不代表上游未读数。</p>
        </section>
        <section className="panel">
          <div className="section-heading"><h2>推送连接</h2><button className="text-button" onClick={() => setTab(relayReady ? 'pushHistory' : 'settings')}>{relayReady ? '查看推送历史' : '配置连接'}</button></div>
          <dl className="details">
            <div><dt>推送服务</dt><dd>{relayReady ? '已配置，回执可在推送历史中查看' : '未配置 · 可先完成本地同步'}</dd></div>
            <div><dt>下次允许上报</dt><dd>{!relayReady || !config.enabled ? '等待配置或启用' : snapshot.relay_blocked ? '凭据失效，请重新配对' : snapshot.relay_next_sync * 1000 > Date.now() ? time(snapshot.relay_next_sync) : '有待处理事件时上报'}</dd></div>
            <div><dt>上报间隔</dt><dd>至少 3 分钟一轮 · 无待处理事项时不通信</dd></div>
          </dl>
          <p className="caption">APNs 已接收不代表设备已送达；推送历史记录助手取得回执的时间。</p>
        </section></div>
        <section className="panel"><div className="section-heading"><h2>最近通知</h2><button className="text-button" onClick={() => setTab('history')}>查看记录</button></div><NotificationList items={notifications.slice(0, 5)} /></section>
      </>}

      {tab === 'settings' && <div className="settings">
        <section className="panel"><h2>配对接收设备</h2><p className="muted">在接收设备的通知设置中生成配对码。两端需要使用同一个 V2EX 账号。</p>
          <dl className="details"><div><dt>推送服务</dt><dd>{pushServiceURL}（固定）</dd></div></dl>
          <label>配对码<input autoComplete="off" spellCheck={false} maxLength={28} placeholder="V2E-…" value={pairCode} onChange={e => setPairCode(e.target.value)} /></label>
          {!state.initial_pairing_done && !config.cookie_configured && <label>同账号的 Cookie<input type="text" autoComplete="off" spellCheck={false} maxLength={16384} placeholder="A2=…; 其他 Cookie…" value={pairCookie} onChange={e => setPairCookie(e.target.value)} /></label>}
          <p className="caption">{state.initial_pairing_done ? `首次配对时有 ${state.initial_unread_count} 条未读。重新配对保留上次推送的 API 首条 ID，后续按网页未读数和首条 ID 判断提醒。` : '首次配对会用 Cookie 校验账号并读取首页未读数；后续检查确认 API 首条 ID 后，有未读时发送一条汇总提醒。Cookie 加密保存在这台服务器，后续定时读取首页未读数，不发送给推送服务。'} 更换接收设备会停止旧连接的待处理事件。</p>
          {(!state.verified || state.auth_blocked) && <p className="notice">请先在下方保存有效的 V2EX Token，账号验证通过后即可配对。</p>}
          <button type="button" className="primary section-action" disabled={busy || !pairCode || (!state.initial_pairing_done && !config.cookie_configured && !pairCookie) || !state.verified || state.auth_blocked} onClick={() => void action(async () => {
            try { await api(accountPath('pair'), 'POST', { code: pairCode, cookie: pairCookie }) } finally { setPairCookie('') }
            setPairCode(''); seeded.current = false; await refresh(true); setMessage('接收设备已配对。有初始未读时，汇总提醒的处理结果可在「推送历史」查看。')
          })}>{busy ? '处理中…' : '配对设备'}</button>
        </section>
        <form className="settings" onSubmit={save}>
        <section className="panel"><h2>V2EX 连接</h2><p className="muted">Token 只用于这台服务器访问官方 API。本配置仅属于当前选中的 V2EX 账号。</p>
          <label>个人 API Token<input type="text" autoComplete="off" maxLength={4096} placeholder={config.api_token_configured ? '已保存；留空保持不变' : '粘贴 Personal Access Token'} value={draft.api_token} onChange={e => setDraft({ ...draft, api_token: e.target.value })} /></label>
          <p className="caption">更新 Token 后会重新验证账号。其他账号请使用「添加账号」，不能用另一账号的 Token 覆盖当前配置。</p>
          <label>网页 Cookie<input type="text" autoComplete="off" spellCheck={false} maxLength={16384} placeholder={config.cookie_configured ? '已加密保存；留空保持不变' : 'A2=…; 其他 Cookie…'} value={draft.cookie} onChange={e => setDraft({ ...draft, cookie: e.target.value })} /></label>
          <p className="caption">网页未读大于 0 时，API 首条消息 ID 与上次推送不同才提醒一次；Cookie 用于读取 V2EX 首页的未读总数。Cookie 加密保存在此服务器，仅发往 V2EX 首页，不打开通知列表，不执行已读操作。网页读取失败时暂停本轮汇总，绝不把缺失计数当作 0。</p>
          <label className="short-field">期望检查间隔（秒）<input type="number" min={30} max={86400} required value={draft.interval_seconds} onChange={e => setDraft({ ...draft, interval_seconds: Number(e.target.value) })} /></label>
        </section>
        <div className="save-bar"><label className="checkbox"><input type="checkbox" checked={draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} />启用同步与上报</label><button className="primary" disabled={busy}>{busy ? '保存中…' : '保存设置'}</button></div>
      </form>
        <section className="panel danger-zone"><h2>移除当前账号</h2><p className="muted">删除此账号在助手中的凭据和本地通知记录，并停止同步与上报。其他账号不受影响。接收设备上的推送连接可在设备端关闭。</p>
          {confirmRemove ? <div className="section-heading"><span>确认移除 {state.username ? `@${state.username}` : '此待验证账号'}？</span><button className="danger-button" type="button" disabled={busy} onClick={() => void action(onRemove)}>确认移除</button><button type="button" disabled={busy} onClick={() => setConfirmRemove(false)}>取消</button></div> : <button className="danger-button" type="button" disabled={busy} onClick={() => setConfirmRemove(true)}>移除账号</button>}
        </section>
      </div>}

      {tab === 'proxy' && <form className="settings" onSubmit={event => {
        event.preventDefault()
        void action(async () => {
          await api(accountPath('proxy'), 'PUT', proxyDraft)
          refreshVersion.current++
          proxySeeded.current = false
          setProxyDraft(d => ({ ...d, proxy_url: '' }))
          await refresh(true); setMessage('代理设置已保存，下一次请求生效。')
        })
      }}>
        <section className="panel"><h2>网络代理</h2><p className="muted">用于当前账号访问 V2EX、定期首页未读检查，以及推送服务的配对、上报和回执查询。</p>
          <label>连接方式<select value={proxyDraft.proxy_mode} disabled={busy} onChange={e => { setProxyResult(null); setProxyDraft({ ...proxyDraft, proxy_mode: e.target.value as ProxyMode, proxy_url: '' }) }}>
            <option value="environment">使用环境变量代理</option><option value="direct">直接连接</option><option value="custom">自定义 HTTP / HTTPS 代理</option>
          </select></label>
          {proxyDraft.proxy_mode === 'custom' && <>
            <label>代理地址<input type="text" autoComplete="off" spellCheck={false} maxLength={4096} required={!config.proxy_url_configured} placeholder={config.proxy_url_configured ? '已保存；留空保持不变' : 'http://用户名:密码@代理主机:端口'} value={proxyDraft.proxy_url} disabled={busy} onChange={e => { setProxyResult(null); setProxyDraft({ ...proxyDraft, proxy_url: e.target.value }) }} /></label>
            {config.proxy_url_configured && <p className="caption">已保存地址：{config.proxy_address}（认证信息不回显）</p>}
            <p className="caption">支持 http:// 和 https://，无需认证时省略用户名和密码。密码中的 @、: 等特殊字符请使用 URL 百分号编码。代理不可用时不会自动改为直连。</p>
          </>}
          <p className="caption">环境变量模式读取运行进程的 HTTP_PROXY、HTTPS_PROXY 和 NO_PROXY。直连模式忽略这些变量。保存后下一次请求生效，上报冷却与额度等待仍然保留。</p>
        </section>
        <div className="save-bar">
          <button type="button" disabled={busy || (proxyDraft.proxy_mode === 'custom' && !proxyDraft.proxy_url.trim() && !config.proxy_url_configured)} onClick={() => void action(async () => {
            setProxyResult(null)
            const result = await api<ProxyResult>(accountPath('proxy/test'), 'POST', proxyDraft)
            if (active.current) setProxyResult(result)
          })}>{busy ? '处理中…' : '测试连接'}</button>
          <button type="submit" className="primary" disabled={busy}>保存代理设置</button>
        </div>
        <p className="caption">测试使用当前填写的连接方式，不会保存草稿。分别检查 V2EX 和 V2Echo 推送服务，不携带账号凭据；每个账号每分钟可测试一次。</p>
        {proxyResult && <section className="panel" aria-live="polite"><h2>连接测试结果</h2><p className="caption">{time(proxyResult.tested_at)} · {{environment:'环境变量代理',direct:'直接连接',custom:'自定义代理'}[proxyResult.proxy_mode]}</p>
          <ul className="delivery-list">{proxyResult.checks.map(check => <li key={check.url}>
            <div><strong>{check.name}</strong><span className={`status-pill ${!check.connected || check.http_status >= 400 ? 'warning' : ''}`}>{check.connected ? `${check.latency_ms} ms · HTTP ${check.http_status}` : '未连通'}</span></div>
            <p>{check.message}</p><small>{check.url}</small>
          </li>)}</ul>
        </section>}
      </form>}

      {tab === 'pushTest' && <PushTest username={state.username} enabled={config.enabled} verified={state.verified && !state.auth_blocked} paired={relayReady} blocked={snapshot.relay_blocked} busy={busy} nextSync={snapshot.relay_next_sync} state={snapshot.push_test} onSettings={() => setTab('settings')} onHistory={() => setTab('pushHistory')} onSend={() => void action(async () => {
        if (!testRequestID.current) testRequestID.current = Array.from(crypto.getRandomValues(new Uint8Array(32)), byte => byte.toString(16).padStart(2, '0')).join('')
        await api(accountPath('push-test'), 'POST', { event_id: testRequestID.current })
        testRequestID.current = ''
        await refresh(true)
        setMessage('测试请求已保存，请查看下方处理结果，并在 App 端确认是否收到消息。')
      })} />}

      {tab === 'pushHistory' && <PushHistory loadPage={loadDeliveries} username={state.username} />}

      {tab === 'history' && <>
        <section className="panel"><h2>本地通知</h2><p className="muted">显示最近 50 条，所有已同步记录保存在本机 SQLite。这里不判断消息已读状态，也不表示已推送；当前未读总数以 V2EX 网页为准。</p><NotificationList items={notifications} /></section>
      </>}
    </main>
  </div>
}

function NotificationList({ items }: { items: Snapshot['notifications'] }) {
  if (!items.length) return <p className="empty">尚无通知。配置并启用连接后，历史记录会分批出现在这里。</p>
  return <ul className="notification-list">{items.map(n => <li key={n.id}><div><h3>{n.title || 'V2EX 提醒'}</h3><time>{time(n.created)}</time></div>{n.body && <p>{n.body}</p>}<small>通知 #{n.id}</small></li>)}</ul>
}
