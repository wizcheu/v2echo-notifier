import { useCallback, useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import PushScheduleFields, { defaultPushSchedule, scheduleError, scheduleDescription, beijingTime } from './PushSchedule'
import type { PushSchedule, PushScheduleStatus } from './PushSchedule'
import RemoveAccountDialog from './RemoveAccountDialog'
import { formatTime } from './formatTime'
import { useRoute } from './useRoute'
import { readHistoryLocation } from './historyLocation'
import PushHistory from './PushHistory'
import CheckHistory from './CheckHistory'
import SecretField from './SecretField'
import { TokenHelp } from './AccountManager'
import { api, APIError } from './api'
import { accountHref, activePage, navigate, pages } from './routes'
import type { Page } from './routes'
import Icon from './Icon'
import type { PushHistoryPage } from './PushHistory'
import PushTest, { pushServiceURL } from './PushTest'
import type { PushTestState } from './PushTest'

type ProxyMode = 'environment' | 'direct' | 'custom'
type Config = {
  push_schedule: PushSchedule
  cookie_configured: boolean
  proxy_mode: ProxyMode
  proxy_url_configured: boolean
  proxy_address: string
  enabled: boolean
  interval_seconds: number
  relay_url: string
  api_token_configured: boolean
  relay_token_configured: boolean
}
type State = {
  token_issue: string
  cookie_issue: string
  token_checked_at: string
  cookie_checked_at: string
  next_token_check: string
  has_web_unread: boolean
  web_unread_count: number
  web_observed_at: string
  initial_pairing_done: boolean
  initial_unread_count: number
  initial_unread_at: string
  username: string
  phase: string
  page: number
  verified: boolean
  auth_blocked: boolean
  last_error: string
  next_api: string
  next_check: string
  last_success: string
  quota: { limit: number; remaining: number; reset: string; observed: boolean }
}
type Snapshot = {
  push_schedule_status: PushScheduleStatus
  push_test: PushTestState
  config: Config
  state: State
  relay_next_sync: number
  relay_blocked: boolean
  notification_count: number
  pending_count: number
  notifications: { id: number; title: string; body: string; created: number }[]
  deliveries: { type: string; event_id: string; status: string; submitted: boolean; attempts: number; detail: string }[]
}
type ProxyDraft = { proxy_mode: ProxyMode; proxy_url: string }
type ProxyResult = {
  proxy_mode: ProxyMode
  tested_at: string
  checks: { name: string; url: string; connected: boolean; http_status: number; latency_ms: number; message: string }[]
}
type Draft = { push_schedule: PushSchedule; cookie: string; api_token: string; interval_seconds: number; enabled: boolean }
type SavedConfig = Draft & ProxyDraft & { relay_url: string; relay_token: string }
const initialDraft: Draft = { push_schedule: defaultPushSchedule, cookie: '', api_token: '', interval_seconds: 180, enabled: false }
const phases: Record<string, string> = {
  history: '导入历史通知',
  catchup: '补扫导入期间的新通知',
  live: '自动检查已开启',
}
const time = formatTime

export default function AccountWorkspace({
  accountID,
  onExpired,
  onRemove,
  tab,
}: {
  accountID: string
  tab: Page
  onExpired: () => void
  onRemove: () => Promise<void>
}) {
  const { hash } = useRoute()
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null)
  const href = (page: Page) => accountHref(accountID, page)
  const setTab = (page: Page) => navigate(href(page))
  const needsConfig = ['credentials', 'device', 'sync', 'proxy'].includes(tab)
  const [draft, setDraft] = useState<Draft>(initialDraft)
  const [proxyDraft, setProxyDraft] = useState<ProxyDraft>({ proxy_mode: 'environment', proxy_url: '' })
  const [proxyResult, setProxyResult] = useState<ProxyResult | null>(null)
  const proxySeeded = useRef(false)
  const [configLoaded, setConfigLoaded] = useState(false)
  const [configLoading, setConfigLoading] = useState(false)
  const [configError, setConfigError] = useState('')
  const [relayToken, setRelayToken] = useState('')
  const configVersion = useRef(0)
  const refreshVersion = useRef(0)
  const [pairCode, setPairCode] = useState('')
  const [pairCookie, setPairCookie] = useState('')
  const testRequestID = useRef('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  useEffect(() => { setMessage(''); setError('') }, [tab])
  const [connectionError, setConnectionError] = useState('')
  const [busy, setBusy] = useState(false)
  const [confirmRemove, setConfirmRemove] = useState(false)
  const seeded = useRef(false)
  const active = useRef(true)
  const accountPath = useCallback((path: string) => `accounts/${encodeURIComponent(accountID)}/${path}`, [accountID])
  const loadDeliveries = useCallback(
    async (query: string) => {
      try {
        return await api<PushHistoryPage>(`${accountPath('deliveries')}?${query}`)
      } catch (e) {
        if (e instanceof APIError && e.status === 401) onExpired()
        throw e
      }
    },
    [accountPath, onExpired],
  )
  const loadSavedConfig = useCallback(
    async (scope: 'all' | 'credentials' | 'sync' | 'proxy' | 'pairing' = 'all') => {
      const version = ++configVersion.current
      setConfigLoading(true)
      setConfigError('')
      try {
        const saved = await api<SavedConfig>(accountPath('config'))
        if (!active.current || version !== configVersion.current) return
        if (scope === 'all') {
          seeded.current = true
          setDraft({
            api_token: saved.api_token,
            push_schedule: saved.push_schedule ?? defaultPushSchedule,
            cookie: saved.cookie ?? '',
            interval_seconds: saved.interval_seconds,
            enabled: saved.enabled,
          })
        }
        if (scope === 'all' || scope === 'proxy') {
          proxySeeded.current = true
          setProxyDraft({ proxy_mode: saved.proxy_mode, proxy_url: saved.proxy_url })
        }
        if (scope === 'credentials') setDraft((d) => ({ ...d, api_token: saved.api_token, cookie: saved.cookie ?? '' }))
        if (scope === 'sync')
          setDraft((d) => ({ ...d, interval_seconds: saved.interval_seconds, enabled: saved.enabled, push_schedule: saved.push_schedule ?? defaultPushSchedule }))
        if (scope === 'pairing') setDraft((d) => ({ ...d, cookie: d.cookie || saved.cookie || '' }))
        setRelayToken(saved.relay_token)
        setConfigLoaded(true)
      } catch (e) {
        if (!active.current || version !== configVersion.current) return
        setConfigError('无法读取已保存设置，请重试。')
        if (e instanceof APIError && e.status === 401) onExpired()
        throw e
      } finally {
        if (active.current && version === configVersion.current) setConfigLoading(false)
      }
    },
    [accountPath, onExpired],
  )
  useEffect(() => {
    if (needsConfig && !configLoaded) void loadSavedConfig().catch(() => {})
  }, [needsConfig, configLoaded, loadSavedConfig])
  const refreshing = useRef(false)
  const refresh = useCallback(
    async (force = false) => {
      if (refreshing.current && !force) return
      refreshing.current = true
      const version = ++refreshVersion.current
      try {
        const data = await api<Snapshot>(accountPath('status'))
        if (!active.current || version !== refreshVersion.current) return
        setSnapshot(data)
        setConnectionError('')
        if (!proxySeeded.current) {
          setProxyDraft({ proxy_mode: data.config.proxy_mode, proxy_url: '' })
          proxySeeded.current = true
        }
        if (!seeded.current) {
          setDraft({ ...initialDraft, enabled: data.config.enabled, interval_seconds: data.config.interval_seconds, push_schedule: data.config.push_schedule ?? defaultPushSchedule })
          seeded.current = true
        }
      } catch (e) {
        if (!active.current || version !== refreshVersion.current) return
        if (e instanceof APIError && e.status === 401) {
          if (active.current) onExpired()
        } else {
          setConnectionError('无法连接通知助手，当前显示的可能是上次读取的状态。')
        }
      } finally {
        if (version === refreshVersion.current) refreshing.current = false
      }
    },
    [accountPath, onExpired],
  )
  useEffect(() => {
    active.current = true
    void refresh()
    return () => {
      active.current = false
    }
  }, [refresh])
  useEffect(() => {
    const timer = window.setInterval(() => void refresh(), 5000)
    return () => window.clearInterval(timer)
  }, [refresh])

  async function action(fn: () => Promise<void>) {
    setBusy(true)
    setError('')
    setMessage('')
    try {
      await fn()
    } catch (e) {
      setError(e instanceof Error ? e.message : '操作失败，请重试')
      if (active.current && e instanceof APIError && e.status === 401) onExpired()
    } finally {
      setBusy(false)
    }
  }
  function save(event: FormEvent) {
    event.preventDefault()
    void action(async () => {
      if (tab === 'sync' && draft.enabled && scheduleError(draft.push_schedule)) throw new Error(scheduleError(draft.push_schedule))
      const saved = await api<SavedConfig>(accountPath('config'))
      const scheduleToSave = !draft.enabled && scheduleError(draft.push_schedule) ? saved.push_schedule ?? defaultPushSchedule : draft.push_schedule
      await api(
        accountPath('config'),
        'PUT',
        tab === 'credentials'
          ? {
              api_token: draft.api_token,
              cookie: draft.cookie,
              interval_seconds: saved.interval_seconds,
              enabled: saved.enabled,
            }
          : { api_token: '', cookie: '', interval_seconds: draft.interval_seconds, enabled: draft.enabled, push_schedule: scheduleToSave },
      )
      refreshVersion.current++
      await loadSavedConfig(tab === 'credentials' ? 'credentials' : 'sync')
      await refresh(true)
      setMessage(
        tab === 'credentials' ? '凭据已保存，将按额度与冷却重新验证。' : `同步偏好已保存：${scheduleDescription(scheduleToSave)}（UTC+8）。${draft.enabled ? '时段外暂停检查与推送。' : '同步仍处于暂停状态。'}`,
      )
    })
  }

  if (!snapshot)
    return (
      <section className="panel">
        {connectionError ? (
          <>
            <p className="notice error" role="alert">
              {connectionError}
            </p>
            <button onClick={() => void refresh()}>重新连接</button>
          </>
        ) : (
          <p role="status">正在读取状态…</p>
        )}
      </section>
    )

  const { config, state, notifications } = snapshot
  const schedule = config.push_schedule ?? defaultPushSchedule
  const resting = snapshot.push_schedule_status?.resting ?? false
  const resumeAt = beijingTime(snapshot.push_schedule_status?.next_start ?? '')
  const resumeText = `${resumeAt}（UTC+8）恢复检查与推送`
  const configured = config.api_token_configured
  const relayReady = Boolean(config.relay_url === pushServiceURL && config.relay_token_configured)
  const pairingBlocked = snapshot.relay_blocked
  const phase = pairingBlocked ? '需重新配对，检查已暂停' : state.token_issue
    ? '需要更新 API Token'
    : state.cookie_issue
      ? '需要更新 Cookie'
      : !configured
        ? '等待配置'
        : !config.enabled
          ? '已暂停'
          : state.auth_blocked
            ? '需要更新 Token'
            : !state.verified
              ? '验证账号'
              : resting ? '休息时段' : (phases[state.phase] ?? state.phase)
  const next = new Date(state.next_api) > new Date(state.next_check) ? state.next_api : state.next_check
  const onboarding = tab === 'overview' && !state.initial_pairing_done && state.verified && !state.auth_blocked && !state.cookie_issue
  const credentialIssue = Boolean(state.token_issue || state.cookie_issue || state.auth_blocked)
  const recovery = tab === 'overview' && credentialIssue
  const cookieVerified = config.cookie_configured && !state.cookie_issue && Boolean(state.cookie_checked_at && !state.cookie_checked_at.startsWith('0001'))
  const detailOpen = tab === 'pushHistory' && Boolean(readHistoryLocation(hash).event)
  const pair = () => void action(async () => {
    try { await api(accountPath('pair'), 'POST', { code: pairCode, cookie: pairCookie }) } finally { setPairCookie('') }
    setPairCode(''); await loadSavedConfig(configLoaded ? 'pairing' : 'all'); await refresh(true)
    setMessage('接收设备已配对，将按同步开关、推送时段和现有额度继续检查与上报。')
  })
  const settingsTabs = (
    <nav className="settings-tabs" aria-label="设置分类">
      {(
        [
          ['settings', '账号凭据'],
          ['device', '接收设备'],
          ['sync', '同步偏好'],
        ] as const
      ).map(([key, label]) => (
        <a key={key} href={href(key)} aria-current={tab === key ? 'page' : undefined}>
          {label}
        </a>
      ))}
    </nav>
  )
  return (
    <>
      {!detailOpen && <header className="page-header">
        <div>
          <h1><span className="desktop-title">{onboarding ? '完成通知连接' : recovery ? `需要更新${state.cookie_issue ? '网页 Cookie' : ' API Token'}` : pages[tab].title}</span><span className="mobile-title">{onboarding ? '完成通知连接' : recovery ? '更新账号凭据' : tab === 'device' ? '接收设备' : tab === 'sync' ? '同步偏好' : tab === 'credentials' ? '更新凭据' : pages[tab].title}</span></h1>
          <p className="page-description"><span className="desktop-title">{onboarding ? `@${state.username} 已验证，继续配对设备并开启同步。` : recovery ? state.cookie_issue ? '网页未读检查已暂停，请更新 Cookie。' : '当前账号的 API 请求已暂停，请更新凭据。' : tab === 'overview' ? `@${state.username} 的同步与推送状态。` : pages[tab].description}</span><span className="mobile-title">{onboarding ? `@${state.username} 已验证，继续配对设备并开启同步。` : recovery ? state.cookie_issue ? '网页未读检查已暂停，请更新 Cookie。' : '当前账号的 API 请求已暂停，请更新凭据。' : tab === 'settings' ? `@${state.username} 的凭据、设备与同步偏好` : ['overview','device','sync','credentials'].includes(tab) ? `当前账号 @${state.username}` : pages[tab].description}</span></p>
        </div>
        {(['credentials', 'runtime', 'device', 'sync'].includes(tab)) && (
          <a className={`back-link ${tab === 'device' || tab === 'sync' ? 'mobile-only' : ''}`} href={href(tab === 'runtime' ? 'overview' : 'settings')}>
            {tab === 'runtime' ? '返回运行概览' : '返回设置'}
          </a>
        )}
      </header>}
      {pairingBlocked && tab !== 'overview' && (
        <div className="notice" role="status">
          <span>接收设备需重新配对，自动检查与推送已暂停。</span>
          <a className="text-link" href={href('device')}>重新配对设备</a>
        </div>
      )}
      {connectionError && (
        <div className="notice error" role="alert">
          <span>{connectionError}</span>
          <button onClick={() => void refresh()}>重新连接</button>
        </div>
      )}
      {error && (
        <div className="notice error" role="alert">
          {error}
          <button className="text-button" onClick={() => setError('')}>
            关闭
          </button>
        </div>
      )}
      {message && (
        <div className="notice" role="status">
          {message}
        </div>
      )}
      {state.last_error && state.last_error !== state.token_issue && state.last_error !== state.cookie_issue && (
        <div className="notice error" role="alert">
          {state.last_error}
        </div>
      )}

      {state.token_issue && !recovery && (
        <section className="notice error" role="alert">
          <div>
            <strong>请更新 API Token</strong>
            <p>{state.token_issue}</p>
            <TokenHelp />
          </div>
          {tab !== 'credentials' && <button onClick={() => setTab('credentials')}>更新 Token</button>}
        </section>
      )}
      {state.cookie_issue && !recovery && (
        <section className="notice error" role="alert">
          <div>
            <strong>请更新 Cookie</strong>
            <p>{state.cookie_issue}</p>
          </div>
          {tab !== 'credentials' && <button onClick={() => setTab('credentials')}>更新 Cookie</button>}
        </section>
      )}
      {onboarding && <div className="onboarding content-surface">
        <ol className="connection-steps"><li><Icon name="check_circle" /><div><strong>账号验证</strong><small>已完成</small></div></li><li><Icon name="time" /><div><strong>配对设备</strong><small>当前步骤</small></div></li><li><Icon name={config.enabled ? "check_circle" : "time"} /><div><strong>启用同步</strong><small>{config.enabled ? '已启用' : '下一步'}</small></div></li></ol>
        <section><h2>在 App 中生成配对码</h2><p className="muted">打开接收设备的通知设置，确认使用 @{state.username} 账号，然后复制配对码。</p>
          <label>配对码<input value={pairCode} onChange={e => setPairCode(e.target.value)} disabled={busy} maxLength={28} autoComplete="off" placeholder="输入 V2E-…" /></label>
          {!config.cookie_configured && <SecretField label="同账号的 Cookie" value={pairCookie} onChange={setPairCookie} maxLength={16384} disabled={busy} />}
          <div className="onboarding-actions"><button className="primary" disabled={busy || !pairCode || (!config.cookie_configured && !pairCookie)} onClick={pair}>{busy ? '正在配对…' : '配对设备'}</button><a className="text-link" href={href('settings')}>稍后配对</a></div>
        </section><section className="onboarding-help"><h2>首次配对</h2><p>使用已保存的 Cookie 校验账号并读取首页未读数，Cookie 不发送给推送服务。</p><p>有初始未读时，确认 API 首条 ID 后发送一条汇总提醒，结果可在推送历史查看。</p><p>启用同步后，历史通知会分批导入；初次导入的历史通知不会逐条推送。</p></section>
      </div>}
      {recovery && <div className="recovery-page content-surface">
        <div className="notice error" role="alert"><Icon name="lock" /><div><strong>{state.cookie_issue ? 'Cookie 已失效' : 'API Token 需要更新'}</strong><p>{state.cookie_issue || state.token_issue || '请重新验证当前账号。'}</p></div></div>
        <div className="account-actions"><button className="primary" onClick={() => setTab('credentials')}>更新{state.cookie_issue ? ' Cookie' : ' Token'}</button><a className="text-link" href="#/accounts">查看账号状态</a></div>
        <section><span className="muted">{state.has_web_unread ? '上次网页未读快照' : '当前网页未读快照'}</span><h2>{state.has_web_unread ? `${state.web_unread_count} 条` : '暂时无法获取'}</h2><p className="muted">{state.has_web_unread ? `观察于 ${time(state.web_observed_at)}，更新凭据后重新检查。` : '缺失计数不会当作 0。本轮汇总已暂停，历史记录仍可查看。'}</p></section>
        <dl className="details"><div><dt>最近成功检查</dt><dd>{time(state.last_success)}</dd></div><div><dt>下一步</dt><dd>更新凭据 → 等待验证 → 恢复检查</dd></div></dl>
        <a className="text-link" href={href('history')}>查看历史记录</a><p className="caption">网络故障或访问挑战会显示连接错误与重试入口，不会直接判定凭据过期。</p>
      </div>}
      {tab === 'overview' && !onboarding && !recovery && (
        <>
          {(!configured || !state.verified || !relayReady || !config.enabled || !config.cookie_configured) && (
            <section className="setup-steps" aria-label="完成连接">
              <a href={href('credentials')}>
                <span className="step-number">1</span>
                <div>
                  <strong>验证账号凭据</strong>
                  <small>
                    {state.verified && config.cookie_configured && !state.cookie_issue
                      ? '凭据已配置'
                      : '补充或更新 Token 与 Cookie'}
                  </small>
                </div>
                <Icon name="arrow_right" />
              </a>
              <a href={href('device')}>
                <span className="step-number">2</span>
                <div>
                  <strong>配对接收设备</strong>
                  <small>{relayReady ? '已配对' : '输入 App 中生成的配对码'}</small>
                </div>
                <Icon name="arrow_right" />
              </a>
              <a href={href('sync')}>
                <span className="step-number">3</span>
                <div>
                  <strong>开启同步与上报</strong>
                  <small>{config.enabled ? '已启用' : '设置检查间隔并启用'}</small>
                </div>
                <Icon name="arrow_right" />
              </a>
            </section>
          )}
          <div className="overview-content">
          <section className={`health-summary ${resting ? 'resting-summary' : ''}`}>
            <Icon name={pairingBlocked ? 'lock' : resting ? 'moon' : state.auth_blocked || state.cookie_issue ? 'lock' : 'check_circle'} />
            <div>
              <h2>{pairingBlocked ? phase : resting ? '休息时段，暂不打扰' : phase}</h2>
              {pairingBlocked ? <><p>自动检查与推送已暂停，旧连接无法继续接收新提醒。</p><p className="caption">在 App 中生成新的配对码，重新配对后按同步开关与推送时段恢复。</p></> : resting ? <><p>自动检查已暂停 · 推送已暂停</p><p className="caption">{resumeText} · {scheduleDescription(schedule)}</p></> : <><p><span className="desktop-title">最近检查完成于 {time(state.last_success)} · 下次允许检查 {config.enabled && !state.auth_blocked && !state.cookie_issue ? time(next) : '等待启用或更新凭据'}</span><span className="mobile-title">最近完成 {formatTime(state.last_success, false).replace('今天 ', '')} · 下次允许 {formatTime(next, false).replace('今天 ', '')}</span></p>
              <p className="caption">
                {config.enabled ? '同步已启用' : '同步已暂停'}，
                {snapshot.relay_blocked ? '接收设备需要重新配对' : relayReady ? '接收设备已配对' : '接收设备尚未配对'}。
              </p></>}
            </div>
            {pairingBlocked ? <a className="button" href={href('device')}>重新配对设备</a> : resting ? <a className="button" href={href('sync')}>调整推送时段</a> : <button
              disabled={busy || pairingBlocked || resting || !config.enabled || state.auth_blocked || Boolean(state.cookie_issue)}
              onClick={() =>
                void action(async () => {
                  await api(accountPath('check'), 'POST', {})
                  await refresh()
                  setMessage('已安排检查，仍会遵守额度限制和错误退避。')
                })
              }
            >
              立即检查
            </button>}
          </section>
          <div className="metrics">
            <div>
              <span>网页未读快照</span>
              <strong>{state.has_web_unread ? `${state.web_unread_count} 条` : '—'}</strong>
              <small>{state.has_web_unread ? time(state.web_observed_at) : '保存 Cookie 后读取'}</small>
            </div>
            <div>
              <span>待处理事件</span>
              <strong>{snapshot.pending_count.toLocaleString()} 条</strong>
              <a className="metric-link" href={href('pushHistory') + '?status=pending'}>
                查看处理进度 <span aria-hidden="true">›</span>
              </a>
            </div>
            <div className="quota-metric">
              <span>所有账号共享额度</span>
              <strong>{state.quota.limit ? `${state.quota.remaining} / ${state.quota.limit}` : '—'}</strong>
              <small>
                {state.quota.observed ? `最近额度窗口截至 ${time(state.quota.reset)}` : '尚无响应额度，使用保守预算'}
              </small>
            </div>
          </div>
          <section className="connection-summary">
            <div>
              <h2><span className="desktop-title">推送连接</span><span className="mobile-title">接收设备</span></h2>
              <h3>
                {state.username ? `@${state.username}` : '待验证账号'} ·{' '}
                {snapshot.relay_blocked ? '需重新配对' : relayReady ? '已配对' : '尚未配对'}
              </h3>
              <p className="caption">
                {pairingBlocked ? '自动检查与推送已暂停，请重新配对接收设备。' : resting ? `${resumeText}，恢复后先检查最新未读。` : !relayReady || !config.enabled
                  ? '完成配对并启用后开始上报。'
                  : snapshot.relay_next_sync * 1000 > Date.now()
                      ? `下次允许通信 ${time(snapshot.relay_next_sync)}`
                      : '有待处理事件时上报，至少 3 分钟一轮。'}
              </p>
            </div>
            <div className="account-actions">
              <a className="button" href={href('pushTest')}>
                {resting ? '查看推送测试记录' : '发送测试推送'}
              </a>
              <a className="text-link" href={href('device')}>
                连接设置
              </a>
            </div>
          </section>
          <section className="panel">
            <div className="section-heading">
              <h2>最近通知</h2>
              <a className="text-link" href={href('history')}>
                全部通知记录
              </a>
            </div>
            <NotificationList items={notifications.slice(0, 5)} />
          </section>
          </div><footer className="overview-footer">
            <a className="text-link" href={href('runtime')}>
              同步与额度详情 <span aria-hidden="true">›</span>
            </a>
            <a className="text-link mobile-only" href={href('history')}>查看通知记录</a><p className="caption">网页未读来自首页快照；初次导入的历史通知不会推送。</p>
          </footer>
        </>
      )}
      {tab === 'runtime' && (
        <div className="overview-grid runtime-details content-surface">
          <section className="panel">
            <div className="section-heading">
              <h2>当前账号的检查</h2>
              <button
                disabled={busy || pairingBlocked || resting || !config.enabled || state.auth_blocked || Boolean(state.cookie_issue)}
                onClick={() =>
                  void action(async () => {
                    await api(accountPath('check'), 'POST', {})
                    await refresh()
                    setMessage('已安排检查，仍会遵守额度限制和错误退避。')
                  })
                }
              >
                立即检查
              </button>
            </div>
            <dl className="details">
              <div>
                <dt>当前阶段</dt>
                <dd>
                  {phase}
                  {state.verified && state.phase !== 'live' ? ` · 第 ${state.page} 页` : ''}
                </dd>
              </div>
              <div>
                <dt>最近检查完成</dt>
                <dd>{time(state.last_success)}</dd>
              </div>
              <div>
                <dt>下次允许检查</dt>
                <dd>
                  {pairingBlocked ? '重新配对后恢复' : resting ? resumeText : config.enabled && !state.auth_blocked && !state.cookie_issue ? time(next) : '等待启用或更新凭据'}
                </dd>
              </div>
              <div>
                <dt>期望检查间隔</dt>
                <dd>{config.interval_seconds} 秒 · 额度不足时自动延长</dd>
              </div>
            </dl>

          </section>
          <section className="panel">
            <h2>所有账号共享额度</h2>
            <dl className="details">
              <div>
                <dt>剩余额度</dt>
                <dd>{state.quota.limit ? `${state.quota.remaining} / ${state.quota.limit}` : '—'}</dd>
              </div>
              <div>
                <dt>最近额度窗口截至</dt>
                <dd>{time(state.quota.reset)}</dd>
              </div>
            </dl>
            <p className="caption">
              {state.quota.observed ? '来自最近一次 API 响应。' : '尚无响应额度，使用保守预算。'}
            </p>
          </section>
          <section className="panel">
            <div className="section-heading">
              <h2>推送服务通信</h2>
              <button className="text-button" onClick={() => setTab(relayReady ? 'pushHistory' : 'device')}>
                {relayReady ? '查看推送历史' : '配置连接'}
              </button>
            </div>
            <dl className="details">

              <div>
                <dt>下次允许上报</dt>
                <dd>
                  {!relayReady || !config.enabled
                    ? '等待配置或启用'
                    : snapshot.relay_blocked
                      ? '凭据失效，请重新配对'
                      : snapshot.relay_next_sync * 1000 > Date.now()
                        ? time(snapshot.relay_next_sync)
                        : '有待处理事件时上报'}
                </dd>
              </div>
              <div>
                <dt>上报间隔</dt>
                <dd>至少 3 分钟一轮 · 无待处理事项时不通信</dd>
              </div>
            </dl>
            <p className="caption">{relayReady ? '推送服务已配置。' : '推送服务尚未配置。'}APNs 已接收不代表设备已送达；初次导入的历史通知不会推送。</p>
          </section>
        </div>
      )}
      {needsConfig && !configLoaded && (
        <section className="panel" aria-busy={configLoading}>
          <p role="status">{configLoading ? '正在读取已保存设置…' : '已保存设置暂时不可用。'}</p>
          {!configLoading && (
            <button type="button" onClick={() => void loadSavedConfig().catch(() => {})}>
              重新读取
            </button>
          )}
        </section>
      )}
      {needsConfig && configError && (
        <p className="notice error" role="alert">
          {configError}
        </p>
      )}
      {tab === 'settings' && (
        <div className="settings credentials-overview">
          <section className="panel credential-group settings-card">
          {settingsTabs}
            <h2>账号凭据</h2>
            <p className="muted">两项凭据应属于同一个 V2EX 账号，仅加密保存在此服务器。</p>
            <div className="setting-rows">
              <a className="setting-row" href={href('credentials')}>
                <div>
                  <strong>个人 API Token</strong>
                  <small>
                    {state.token_issue
                      ? '需要更新'
                      : state.verified
                        ? `已验证 · 最近验证 ${time(state.token_checked_at)}`
                        : configured
                          ? '等待验证'
                          : '尚未配置'}
                  </small>
                </div>
                <span className={`status-pill ${state.token_issue ? 'warning' : state.verified ? 'accepted' : 'neutral'}`}>{state.token_issue ? '需要更新' : state.verified ? '已验证' : '等待验证'}</span>
                <span className="row-action">查看 / 编辑</span>
              </a>
              <a className="setting-row" href={href('credentials')}>
                <div>
                  <strong>网页 Cookie</strong>
                  <small>
                    {state.cookie_issue
                      ? '需要更新'
                      : config.cookie_configured
                        ? `已配置 · 最近验证 ${time(state.cookie_checked_at)}`
                        : '需要补充'}
                  </small>
                </div>
                <span className={`status-pill ${state.cookie_issue || !config.cookie_configured ? 'warning' : cookieVerified ? 'accepted' : 'neutral'}`}>{state.cookie_issue ? '需要更新' : cookieVerified ? '已验证' : config.cookie_configured ? '等待验证' : '需要补充'}</span>
                <span className="row-action">查看 / 编辑</span>
              </a>
            </div>
            <div className="credential-update-link"><p className="muted">显示、复制或替换凭据；留空保存保留原值。</p><a className="button primary" href={href('credentials')}><span className="desktop-title">更新凭据</span><span className="mobile-title">查看或更新凭据</span></a></div>
          </section>
          <section className="mobile-only mobile-setting-section"><h2>接收设备</h2><h3>@{state.username} · {pairingBlocked ? '需重新配对' : relayReady ? '已配对' : '尚未配对'}</h3><p className="muted">重新配对、查看发送 Token 或发送测试。</p><a className="button" href={href('device')}>管理接收设备</a></section>
          <section className="mobile-only mobile-setting-section"><h2>同步偏好</h2><h3>{config.enabled ? '已启用' : '已暂停'} · 期望每 {config.interval_seconds} 秒检查</h3><p className="muted">{scheduleDescription(schedule)}（UTC+8）；休息期间暂停检查与推送。</p><a className="button" href={href('sync')}>调整同步与网络</a></section>
          <section className="panel credential-explainer">
            <h2>凭据如何使用</h2>
            <p className="muted">
              Token 用于访问官方 API。Cookie
              用于读取首页未读数，不执行已读操作，也不发送给推送服务。网页读取失败时暂停本轮汇总。
            </p>
            <p className="caption">
              凭据失效会在概览与账号管理中提示。网络故障或访问挑战不会直接判定为过期。未配置 Cookie
              的已有账号仍使用逐条通知提醒。
            </p>
          </section>
          <section className="panel danger-zone">
            <h2>移除当前账号</h2>
            <p className="muted">
              删除此账号在助手中的凭据和本地通知记录，并停止同步与上报。其他账号不受影响。接收设备上的推送连接可在设备端关闭。
            </p>
            <button className="danger-button" type="button" disabled={busy} onClick={() => setConfirmRemove(true)}>移除账号</button>
            {confirmRemove && <RemoveAccountDialog username={state.username} busy={busy} error={error} onCancel={() => setConfirmRemove(false)} onConfirm={() => void action(onRemove)} />}
          </section>
        </div>
      )}
      {tab === 'device' && configLoaded && (
        <div className="settings device-settings content-surface settings-card">
          {settingsTabs}
          <section className="device-summary">
            <div>
              <h2><span className="desktop-title">当前接收账号</span><span className="mobile-title">配对信息</span></h2>
              <div className="device-identity">
                <h3>{state.username ? `@${state.username}` : '待验证账号'}</h3>
                <span
                  className={`status-pill ${snapshot.relay_blocked ? 'warning' : relayReady ? 'accepted' : 'neutral'}`}
                >
                  {snapshot.relay_blocked ? '需重新配对' : relayReady ? '已配对' : '尚未配对'}
                </span>
              </div>
            </div>
            <p className="caption"><span className="desktop-title">首次配对时有 {state.initial_unread_count} 条未读。重新配对会保留上次推送的 API 首条消息 ID。</span><span className="mobile-title">首次配对时有 {state.initial_unread_count} 条未读，汇总提醒的结果可在推送历史查看。</span></p>
            <a className="button primary" href={href('pushTest')}>
              发送测试推送
            </a>
          </section>
          <section className="panel">
            <div className="section-heading">
              <h2>{pairingBlocked ? '重新配对接收设备' : relayReady ? '更换接收设备' : '配对接收设备'}</h2>
            </div>
            <p className="muted">在接收设备的通知设置中生成配对码。两端需要使用同一个 V2EX 账号。</p>
            <div className="pairing-fields">
              <label>
                {relayReady ? '新配对码' : '配对码'}
                <input
                  autoComplete="off"
                  spellCheck={false}
                  maxLength={28}
                  disabled={busy}
                  placeholder="V2E-…"
                  value={pairCode}
                  onChange={(e) => setPairCode(e.target.value)}
                />
              </label>
              <button
                type="button"
                className="section-action"
                disabled={
                  busy ||
                  !pairCode ||
                  (!state.initial_pairing_done && !config.cookie_configured && !pairCookie) ||
                  !state.verified ||
                  state.auth_blocked
                }
                onClick={pair}
              >
                {busy ? '处理中…' : relayReady ? '重新配对' : '配对设备'}
              </button>
            </div>
            {!state.initial_pairing_done && !config.cookie_configured && (
              <SecretField
                label="同账号的 Cookie"
                maxLength={16384}
                placeholder="A2=…; 其他 Cookie…"
                value={pairCookie}
                onChange={setPairCookie}
                disabled={busy}
              />
            )}
            <p className="caption">
              {state.initial_pairing_done ? '重新配对将停止旧连接的待处理事件。' : '首次配对会校验同账号的 Cookie 并读取首页未读数；有未读时，确认 API 首条 ID 后发送一条汇总提醒。Cookie 加密保存在此服务器，不发送给推送服务。'}
            </p>
            {(!state.verified || state.auth_blocked) && (
              <p className="notice">请先在「账号凭据」中保存有效的 V2EX Token，账号验证通过后即可配对。</p>
            )}
            <div className="device-token-row"><SecretField label="推送发送 Token（配对后自动生成）" value={relayToken} readOnly disabled={busy || configLoading} placeholder="配对后自动生成" /><div className="service-address"><span>固定推送服务</span><p>{pushServiceURL}</p></div></div>
          </section>
        </div>
      )}
      {tab === 'credentials' && configLoaded && (
        <form className="settings credential-editor content-surface" onSubmit={save}>
          <section className="credential-fields"><SecretField label="个人 API Token" value={draft.api_token} disabled={busy || configLoading} placeholder="粘贴 Personal Access Token" onChange={value => setDraft({...draft, api_token:value})} /><p className="caption">最近验证 {time(state.token_checked_at)} · {state.token_issue ? '需要更新' : state.verified ? '已验证' : '等待验证'}</p>
          <SecretField label="网页 Cookie" value={draft.cookie} maxLength={16384} disabled={busy || configLoading} placeholder="A2=…; 其他 Cookie…" onChange={value => setDraft({...draft,cookie:value})} /><p className="caption">最近验证 {time(state.cookie_checked_at)} · {state.cookie_issue ? '需要更新' : config.cookie_configured ? '已配置' : '尚未配置'}</p></section>
          <div className="notice credential-notice"><Icon name="check_circle" /><div><div className="desktop-title"><strong>使用同一个 V2EX 账号</strong><p>Token 与 Cookie 的用户名必须完全一致。不能用其他账号的 Token 覆盖当前配置。</p></div><span className="mobile-title">两项必须属于同一个账号。<br />留空保留原值，更新后重新验证。</span></div></div>
          <div className="save-bar"><p className="caption">留空保留原值；更新后重新验证。</p><a className="button" href={href('settings')}>取消</a><button className="primary" disabled={busy || configLoading}>{busy ? '正在保存…' : <><span className="desktop-title">保存账号凭据</span><span className="mobile-title">保存凭据</span></>}</button></div>
          <section className="credential-help"><h2>获取凭据</h2><div className="desktop-title"><TokenHelp /><p className="caption">Cookie：复制已登录浏览器的完整 Cookie 请求头，需包含 A2。</p></div><div className="mobile-title"><p className="caption">Token 选择 Everything 权限，建议有效期 180 天。Cookie 需包含 A2。</p><a className="text-link credential-help-link" href="https://v2ex.com/settings/tokens" target="_blank" rel="noopener noreferrer">前往 V2EX 生成 Token ↗</a></div><details className="technical-details"><summary>凭据检查与隐私说明</summary><p className="caption">Token 用于访问官方 API；Cookie 仅用于读取首页未读数，不执行已读操作，也不发送给推送服务。读取失败时暂停本轮汇总，缺失计数不会当作 0。没有通知 API 请求时，每 24 小时额外检查一次 Token。</p></details></section>
        </form>
      )}
      {tab === 'sync' && configLoaded && (
        <form className="settings sync-settings content-surface settings-card" onSubmit={save}>
          {settingsTabs}
          <section className="panel">
            <h2>同步与上报</h2>
            <label className="switch-row">
              <div>
                <strong>启用同步与上报</strong>
                <small>关闭后暂停全部自动任务。启用后按下方时段运行。</small>
              </div>
              <input
                type="checkbox"
                role="switch"
                disabled={busy || configLoading}
                checked={draft.enabled}
                onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
              />
            </label>
          </section>
          <PushScheduleFields value={draft.push_schedule} disabled={busy || configLoading || !draft.enabled} paused={!draft.enabled} onChange={push_schedule => setDraft(current => ({ ...current, push_schedule }))} />
          <section className="panel sync-interval">
            <label className="short-field">
              期望检查间隔（秒）
              <input
                type="number"
                min={30}
                max={86400}
                required
                disabled={busy || configLoading}
                value={draft.interval_seconds}
                onChange={(e) => setDraft({ ...draft, interval_seconds: Number(e.target.value) })}
                onBlur={(e) => setDraft(current => ({ ...current, interval_seconds: Math.max(30, Number(e.target.value) || 30) }))}
              />
            </label>
            <p className="caption"><span className="desktop-title">仅在允许推送时段内生效。范围 30–86400 秒；额度不足时自动延长，冷却与错误退避仍然生效。</span><span className="mobile-title">范围 30–86400 秒，额度不足时自动延长。</span></p>
          </section>
          <section className="panel sync-proxy">
            <a className="setting-row" href={href('proxy')}>
              <div>
                <strong><span className="desktop-title">当前使用的连接方式</span><span className="mobile-title">网络代理</span></strong>
                <small>
                  {
                    { environment: '环境变量代理', direct: '直接连接', custom: '自定义 HTTP / HTTPS 代理' }[
                      config.proxy_mode
                    ]
                  }
                </small>
              </div>
              <span className="row-action">管理网络代理</span>
            </a>
          </section>
          <div className="save-bar">
            <p className="caption">仅对当前账号生效；保存后按 UTC+8 执行。</p>
            <button className="primary" disabled={busy || configLoading}>
              {busy ? '正在保存…' : '保存同步偏好'}
            </button>
          </div><div className="sync-footer"><p className="caption">当前共享额度 {state.quota.remaining} / {state.quota.limit}；窗口结束 {time(state.quota.reset)}。</p><a className="text-link" href={href('runtime')}>查看运行详情</a></div>
        </form>
      )}

      {tab === 'proxy' && configLoaded && (
        <form
          className="settings proxy-settings content-surface"
          onSubmit={(event) => {
            event.preventDefault()
            void action(async () => {
              await api(accountPath('proxy'), 'PUT', proxyDraft)
              refreshVersion.current++
              await loadSavedConfig('proxy')
              await refresh(true)
              setMessage('代理设置已保存，下一次请求生效。')
            })
          }}
        >
          <section className="panel">
            <h2>连接方式</h2>
            <p className="muted">用于当前账号访问 V2EX、定期首页未读检查，以及推送服务的配对、上报和回执查询。</p>
            <fieldset className="mode-picker" disabled={busy}>
              <legend>连接方式</legend>
              {(
                [
                  ['environment', '环境变量代理'],
                  ['direct', '直接连接'],
                  ['custom', '自定义代理'],
                ] as const
              ).map(([mode, label]) => (
                <label key={mode}>
                  <input
                    type="radio"
                    name="proxy-mode"
                    value={mode}
                    checked={proxyDraft.proxy_mode === mode}
                    onChange={() => {
                      setProxyResult(null)
                      setProxyDraft({ ...proxyDraft, proxy_mode: mode })
                    }}
                  />
                  <span>{label}</span>
                </label>
              ))}
            </fieldset>
            {proxyDraft.proxy_mode === 'custom' && (
              <>
                <SecretField
                  label="代理地址"
                  value={proxyDraft.proxy_url}
                  required={!config.proxy_url_configured}
                  placeholder="http://用户名:密码@代理主机:端口"
                  disabled={busy || configLoading}
                  onChange={(value) => {
                    setProxyResult(null)
                    setProxyDraft({ ...proxyDraft, proxy_url: value })
                  }}
                />
                {config.proxy_url_configured && (
                  <p className="caption">已保存地址：{config.proxy_address}（上方可查看含认证信息的完整地址）</p>
                )}
                <p className="caption">
                  支持 http:// 和 https://，无需认证时省略用户名和密码。密码中的 @、: 等特殊字符请使用 URL
                  百分号编码。代理不可用时不会自动改为直连。
                </p>
              </>
            )}

          </section>
          <div className="proxy-test-actions">
            <button
              type="button"
              disabled={
                busy ||
                (proxyDraft.proxy_mode === 'custom' && !proxyDraft.proxy_url.trim() && !config.proxy_url_configured)
              }
              onClick={() =>
                void action(async () => {
                  setProxyResult(null)
                  const result = await api<ProxyResult>(accountPath('proxy/test'), 'POST', proxyDraft)
                  if (active.current) setProxyResult(result)
                })
              }
            >
              {busy ? '处理中…' : '测试连接'}
            </button>
            <p className="caption">测试不保存设置，也不携带账号凭据；测试完成后可立即重试。</p>
          </div>

          {proxyResult && (
            <section className="panel proxy-result" aria-live="polite">
              <h2>{proxyResult.checks.length === 2 && proxyResult.checks.every(c => c.connected && c.http_status < 400) ? '两项服务均可连接' : '连接测试结果'}</h2>
              <p className="caption">
                {time(proxyResult.tested_at)} ·{' '}
                {{ environment: '环境变量代理', direct: '直接连接', custom: '自定义代理' }[proxyResult.proxy_mode]}
              </p>
              <ul className="delivery-list">
                {proxyResult.checks.map((check) => (
                  <li key={check.url}><div className="proxy-check"><Icon name={check.connected && check.http_status < 400 ? 'check_circle' : 'time'} /><strong>{check.name}</strong><span>{check.connected ? `${check.latency_ms} ms · HTTP ${check.http_status}` : '未连通'}</span></div>{(!check.connected || check.http_status >= 400) && <p>{check.message}</p>}</li>
                ))}
              </ul>
            </section>
          )}
          <div className="save-bar"><p className="caption">保存后下一次请求生效，已有冷却和额度等待继续保留。</p><button type="submit" className="primary" disabled={busy}>保存代理设置</button></div>
          <section className="proxy-help"><h2>其他连接方式</h2><p className="caption">环境变量模式读取 HTTP_PROXY、HTTPS_PROXY、NO_PROXY；直连模式忽略这些设置。当前连接方式用于 V2EX 与推送服务。</p></section>
        </form>
      )}

      {tab === 'pushTest' && (
        <PushTest
          username={state.username}
          resting={resting}
          resumeText={resumeText}
          enabled={config.enabled}
          verified={state.verified && !state.auth_blocked}
          paired={relayReady}
          blocked={snapshot.relay_blocked}
          busy={busy}
          nextSync={snapshot.relay_next_sync}
          state={snapshot.push_test}
          onSettings={() =>
            setTab(
              !state.verified || state.auth_blocked
                ? 'credentials'
                : !relayReady || snapshot.relay_blocked
                  ? 'device'
                  : 'sync',
            )
          }
          onHistory={() => setTab('pushHistory')}
          onSend={() =>
            void action(async () => {
              if (!testRequestID.current)
                testRequestID.current = Array.from(crypto.getRandomValues(new Uint8Array(32)), (byte) =>
                  byte.toString(16).padStart(2, '0'),
                ).join('')
              await api(accountPath('push-test'), 'POST', { event_id: testRequestID.current })
              testRequestID.current = ''
              await refresh(true)
              setMessage('测试请求已保存，请查看下方处理结果，并在 App 端确认是否收到消息。')
            })
          }
        />
      )}

      {tab === 'pushHistory' && <PushHistory loadPage={loadDeliveries} username={state.username} />}
      {tab === 'checkHistory' && <CheckHistory accountID={accountID} onExpired={onExpired} />}

      {tab === 'history' && (
        <>
          <section className="panel content-surface">
            <p className="muted">本地记录不标记已读，也不表示已经推送；未读总数以 V2EX 网页为准。</p>
            <NotificationList items={notifications} grouped />
          </section>
        </>
      )}
    </>
  )
}

function NotificationList({ items, grouped = false }: { items: Snapshot['notifications']; grouped?: boolean }) {
  if (!items.length) return <p className="empty">目前还没有同步到通知。启用同步后，助手会按检查计划自动更新。</p>
  const byDate: Record<string, Snapshot['notifications']> = {}
  for (const item of items) {
    const day = grouped
      ? formatTime(item.created, false).split(' ')[0]
      : ''
    ;(byDate[day] ??= []).push(item)
  }
  return (
    <>
      {Object.entries(byDate).map(([day, records]) => (
        <div key={day} className="notification-group">
          {day && <h2 className="date-heading">{day}</h2>}
          <ul className={`notification-list ${grouped ? 'with-icons' : ''}`}>
            {records.map((n) => (
              <li key={n.id}>
                {grouped && <Icon name="notification" />}
                <div>
                  <h3>{n.title || 'V2EX 提醒'}</h3>
                  <time>{grouped ? formatTime(n.created, false).split(' ').slice(1).join(' ') : formatTime(n.created, false)}</time>
                </div>
                {n.body && <p>{n.body}</p>}
                {grouped && <small>通知 #{n.id}</small>}
              </li>
            ))}
          </ul>
        </div>
      ))}
    </>
  )
}
