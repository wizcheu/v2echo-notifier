import Icon from './Icon'
import NotificationList from './NotificationList'
import ParticleButton from './components/kokonutui/particle-button'
import { accountHref } from './routes'
import { formatTime as time } from './formatTime'
import { scheduleDescription } from './PushSchedule'
import type { PushSchedule } from './PushSchedule'

type Props = {
  accountID: string
  state: {
    username: string; verified: boolean; auth_blocked: boolean; cookie_issue: string
    has_web_unread: boolean; web_unread_count: number; web_observed_at: string; last_success: string
    quota: { limit: number; remaining: number; reset: string; observed: boolean }
  }
  config: { enabled: boolean; cookie_configured: boolean }
  notifications: { id: number; title: string; body: string; created: number }[]
  pendingCount: number
  configured: boolean
  relayReady: boolean
  pairingBlocked: boolean
  resting: boolean
  busy: boolean
  phase: string
  nextCheck: string
  nextSync: number
  schedule: PushSchedule
  resumeText: string
  onCheck: () => void
}

export default function Dashboard({ accountID, state, config, notifications, pendingCount, configured, relayReady, pairingBlocked, resting, busy, phase, nextCheck, nextSync, schedule, resumeText, onCheck }: Props) {
  const href = (page: Parameters<typeof accountHref>[1]) => accountHref(accountID, page)
  const healthy = !pairingBlocked && !resting && config.enabled && state.verified && relayReady
  return <>
    {(!configured || !state.verified || !relayReady || !config.enabled || !config.cookie_configured) && <section className="setup-steps" aria-label="完成连接">
      {([
        ['credentials', '验证账号凭据', state.verified && config.cookie_configured && !state.cookie_issue],
        ['device', '配对接收设备', relayReady && !pairingBlocked],
        ['sync', '开启同步与上报', config.enabled],
      ] as const).map(([page, label, done], index) => <a href={href(page)} key={page}><span className="step-number">{done ? <Icon name="check_circle" /> : `0${index + 1}`}</span><strong>{label}</strong><Icon name="arrow_right" /></a>)}
    </section>}
    <div className="dashboard">
      <section className="snapshot-card">
        <div className="snapshot-header"><span className="section-kicker">网页未读快照</span><span className="snapshot-source"><Icon name="globe" />V2EX</span></div>
        <div className="snapshot-readout"><strong>{state.has_web_unread ? state.web_unread_count : '—'}</strong><span>{state.has_web_unread ? '条未读提醒' : '等待网页快照'}</span></div>
        <p className="snapshot-description">{state.has_web_unread ? `观察于 ${time(state.web_observed_at)}` : '保存有效 Cookie 后，读取首页未读数。'}<br />来自网页首页，实际未读状态以 V2EX 为准。</p>
        <div className="dashboard-stats metrics">
          <div><span><Icon name="time" />待处理事件</span><strong>{pendingCount.toLocaleString()}<small>条</small></strong><a className="metric-link" href={href('pushHistory') + '?status=pending'}>查看处理进度 <span aria-hidden="true">↗</span></a></div>
          <div><span><Icon name="dashboard_4" />所有账号共享额度</span><strong>{state.quota.limit ? state.quota.remaining : '—'}{Boolean(state.quota.limit) && <small>/ {state.quota.limit}</small>}</strong><small>{state.quota.observed ? `窗口截至 ${time(state.quota.reset)}` : '尚无响应额度，使用保守预算'}</small></div>
        </div>
      </section>
      <section className={`health-summary ${healthy ? 'is-healthy' : ''} ${resting ? 'resting-summary' : ''}`}>
        <div className="health-top"><span className="section-kicker">运行状态</span><Icon name={pairingBlocked ? 'lock' : resting ? 'moon' : healthy ? 'check_circle' : 'time'} /></div>
        <h2>{pairingBlocked ? phase : resting ? '休息时段，暂不打扰' : phase}</h2>
        {pairingBlocked ? <p>自动检查与推送已暂停，旧连接无法继续接收新提醒。重新配对后按同步开关与时段恢复。</p> : resting ? <><p>自动检查已暂停 · 推送已暂停</p><p>{resumeText}<br />{scheduleDescription(schedule)}</p></> : <dl><div><dt>最近检查完成</dt><dd>{time(state.last_success)}</dd></div><div><dt>下次允许检查</dt><dd>{config.enabled && !state.auth_blocked && !state.cookie_issue ? time(nextCheck) : '等待启用或更新凭据'}</dd></div></dl>}
        {pairingBlocked ? <a className="button primary" href={href('device')}>重新配对设备<Icon name="arrow_right" /></a> : resting ? <a className="button" href={href('sync')}>调整推送时段<Icon name="arrow_right" /></a> : <ParticleButton variant="outline" fullWidth status={busy ? 'pending' : 'idle'} pendingLabel="正在安排…" disabled={busy || !config.enabled || state.auth_blocked || Boolean(state.cookie_issue)} onClick={onCheck}>立即检查<Icon name="arrow_right" /></ParticleButton>}
        <p className="health-footnote">检查仍遵守额度、冷却和错误退避。</p>
      </section>
      <section className="recent-notifications">
        <div className="section-heading"><div><span className="section-kicker">通知收件箱</span><h2>最近同步</h2></div><a className="text-link" href={href('history')}>全部通知记录 <Icon name="arrow_right" /></a></div>
        <NotificationList items={notifications.slice(0, 5)} />
      </section>
      <section className="connection-summary">
        <span className="section-kicker">推送连接</span>
        <div className="connection-identity"><span className="connection-icon"><Icon name="send_plane" /></span><div><h2>{state.username ? `@${state.username}` : '待验证账号'}</h2><span className={`status-pill ${pairingBlocked ? 'warning' : relayReady ? 'accepted' : 'neutral'}`}>{pairingBlocked ? '需重新配对' : relayReady ? '已配对' : '尚未配对'}</span></div></div>
        <p>{pairingBlocked ? '自动检查与推送已暂停，请重新配对接收设备。' : resting ? `${resumeText}，恢复后先检查最新未读。` : !relayReady || !config.enabled ? '完成配对并启用后开始上报。' : nextSync * 1000 > Date.now() ? `下次允许通信 ${time(nextSync)}` : '有待处理事件时上报，至少 30 秒一轮。'}</p>
        <div className="connection-actions"><a className="button" href={href('pushTest')}>{resting ? '查看推送测试记录' : '发送测试推送'}</a><a className="text-link" href={href('device')}>连接设置<Icon name="arrow_right" /></a></div>
      </section>
    </div>
    <footer className="overview-footer"><a className="text-link" href={href('runtime')}>同步与额度详情 <Icon name="arrow_right" /></a><p className="caption">初次导入的历史通知不会推送；APNs 已接收不代表设备已送达。</p></footer>
  </>
}
