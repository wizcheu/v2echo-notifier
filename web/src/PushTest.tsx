import type { PushHistoryItem } from './PushHistory'
import { deliveryDetail } from './deliveryDetail'
import Icon from './Icon'
import { formatTime } from './formatTime'

export const pushServiceURL = 'https://app.v2echo.com/api/push'
export const pushTestBody = '这是一条 V2Echo 测试推送，收到此消息说明推送已到达设备。'
export type PushTestState = { next_allowed: number; latest: PushHistoryItem | null }
const statuses: Record<string, string> = {
  pending: '待处理',
  apns_accepted: 'APNs 已接收',
  rejected: '已拒绝',
  blocked: '需重新配对',
  expired: '已过期',
  skipped: '已跳过',
}
const at = formatTime

type Props = {
  username: string
  resting: boolean
  resumeText: string
  enabled: boolean
  verified: boolean
  paired: boolean
  blocked: boolean
  busy: boolean
  nextSync: number
  state: PushTestState
  onSend: () => void
  onSettings: () => void
  onHistory: () => void
}

export default function PushTest({
  username,
  resting,
  resumeText,
  enabled,
  verified,
  paired,
  blocked,
  busy,
  nextSync,
  state,
  onSend,
  onSettings,
  onHistory,
}: Props) {
  const latest = state.latest
  const waitingReceipt = latest?.status === 'pending' && latest.submitted
  const receiptNext = Math.max(nextSync, latest?.next_attempt ?? 0)
  const now = Date.now()
  const pending = latest?.status === 'pending' && new Date(latest.created_at).getTime() + 15 * 60_000 > now
  const cooling = state.next_allowed * 1000 > now
  const ready = verified && enabled && paired && !blocked && !resting
  const reason = !verified
    ? '等待当前账号验证通过。'
    : !paired
      ? '请先配对 App 中生成的接收设备。'
      : blocked
        ? '推送凭据已失效，请重新配对接收设备。'
        : !enabled
          ? '请先启用同步与上报。'
          : resting ? `休息时段，${resumeText}。` : ''
  if (latest && (pending || waitingReceipt)) return <div className="push-test waiting-test content-surface">
    <div className="notice" role="status"><Icon name="check_circle" /><div><strong>{waitingReceipt ? '已上报，等待回执' : '测试已创建，等待上报'}</strong><p>{!enabled ? '同步已暂停，启用后继续处理。' : resting ? `休息时段，${resumeText}；恢复后仍遵守冷却与有效期。` : blocked ? '请重新配对后恢复。' : `下次允许${waitingReceipt ? '查询回执' : '上报'}：${at(receiptNext)}。刷新页面不会提前查询或重新发送。`}</p></div></div>
    <ol className="process-timeline"><li><Icon name="check_circle" /><div><h2>测试已创建</h2><p>{formatTime(latest.created_at)}</p></div></li><li><Icon name={latest.submitted ? 'check_circle' : 'time'} /><div><h2>{latest.submitted ? '已完成首次上报' : '等待首次上报'}</h2><p>{at(latest.first_attempt_at)}</p></div></li><li><Icon name="time" /><div><h2>{waitingReceipt ? '等待下一轮回执查询' : '等待服务端确认'}</h2><p>{at(receiptNext)}</p></div></li></ol>
    <h3>手机可能已先收到通知，回执会在后续轮次更新。</h3><p className="caption">页面每 5 秒读取本地记录；测试有效期 15 分钟，上一条处理中不重复创建。</p>
    <div className="test-actions"><button disabled={busy || !ready || cooling || pending} onClick={onSend}>{resting ? '休息时段，暂不测试' : pending ? '等待测试结果' : '发送测试推送'}</button><button className="text-button" onClick={onHistory}>查看推送历史</button>{!ready && <button onClick={onSettings}>连接与设置</button>}</div>
  </div>
  return (
    <div className="settings push-test content-surface">
      <section className="panel">
        <div className="test-composer">
          <div className="push-preview">
            <span className="preview-app">
              本次测试消息
            </span>
            <h3>{username ? `@${username}` : '@用户名'}</h3>
            <p>{pushTestBody}</p>
          </div>
          <div className="test-prerequisites" aria-label="发送条件">
            <h2>发送条件</h2>
            <span className="prerequisite"><Icon name={verified ? 'check_circle' : 'time'} />
              {verified ? '账号已验证' : '账号待验证'}
            </span>
            <span className="prerequisite"><Icon name={paired && !blocked ? 'check_circle' : 'time'} />
              {blocked ? '需重新配对' : paired ? '设备已配对' : '设备未配对'}
            </span>
            <span className="prerequisite"><Icon name={enabled ? 'check_circle' : 'time'} />
              {enabled ? '同步与上报已启用' : '同步已暂停'}
            </span>
            <span className="prerequisite"><Icon name={resting ? 'moon' : 'check_circle'} />{resting ? '休息时段，已暂停' : '当前处于允许推送时段'}</span>
            <button className="text-button" onClick={onSettings}>连接设置</button>
          </div>
        </div>
        {!ready && (
          <div className="notice test-notice">
            <span>{reason}</span>
            <button type="button" onClick={onSettings}>
              连接与设置
            </button>
          </div>
        )}
        <div className="test-actions">
          <button className="primary" type="button" disabled={busy || !ready || cooling || pending} onClick={onSend}>
            {resting ? '休息时段，暂不测试' : busy ? '正在提交…' : pending ? '等待测试结果' : cooling ? '测试冷却中' : '发送测试推送'}
          </button>
          <p className="caption">每 3 分钟可创建一次；上一条未结束时需等待。</p>
        </div>

        {cooling && <p className="caption">下次可创建测试：{at(state.next_allowed)}</p>}
        {ready && nextSync * 1000 > now && (
          <p className="caption">
            下次允许与推送服务通信：{at(nextSync)}。提交后可能需要等待这一轮上报；回执也在后续轮次更新。
          </p>
        )}
      </section>
      <section className="panel" aria-label="最近一次推送测试">
        <div className="section-heading">
          <h2>最近一次测试</h2>
          {latest && (
            <span
              className={`status-pill ${['blocked', 'rejected', 'expired'].includes(latest.status) ? 'warning' : latest.status === 'apns_accepted' ? 'accepted' : 'neutral'}`}
            >
              {waitingReceipt ? '等待回执' : (statuses[latest.status] ?? latest.status)}
            </span>
          )}
        </div>
        {!latest ? (
          <p className="empty">还没有测试记录。配对完成后，发送一条测试推送确认连接。</p>
        ) : (
          <>
            <p role="status">{deliveryDetail(latest)}</p>
            {waitingReceipt && (
              <p className="caption">
                手机可能已先收到通知。页面每 5 秒读取本地记录，推送回执则至少间隔 3
                分钟查询；刷新页面不会提前查询或重新发送。
              </p>
            )}
            <dl className="details test-timeline">
              <div>
                <dt>创建时间</dt>
                <dd>{formatTime(latest.created_at)}</dd>
              </div>
              <div>
                <dt>首次上报</dt>
                <dd>{at(latest.first_attempt_at)}</dd>
              </div>
              {waitingReceipt && (
                <div>
                  <dt>下次允许查询回执</dt>
                  <dd>
                    {!enabled
                      ? '同步已暂停，启用后继续查询'
                      : blocked
                        ? '请重新配对后恢复'
                        : receiptNext * 1000 > now
                          ? at(receiptNext)
                          : '等待下一轮同步'}
                  </dd>
                </div>
              )}
              <div>
                <dt>取得回执</dt>
                <dd>{at(latest.finished_at)}</dd>
              </div>
              <div className="test-event">
                <dt>事件编号</dt>
                <dd>
                  <code>{latest.event_id}</code>
                </dd>
              </div>
            </dl><button className="text-button test-history-link" onClick={onHistory}>查看推送历史</button>
          </>
        )}
        <section className="test-help"><h3>没有看到通知？</h3><p className="caption">检查通知权限、专注模式和设备网络。App 停留在通知页时可切到其他页面或后台再试。</p><p className="caption">测试走正常上报队列，有效期 15 分钟；不会增加真实通知记录或修改网页未读数。</p></section>
      </section>
    </div>
  )
}
