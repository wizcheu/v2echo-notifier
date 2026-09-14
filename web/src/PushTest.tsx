import type { PushHistoryItem } from './PushHistory'
import { deliveryDetail } from './deliveryDetail'

export const pushServiceURL = 'https://app.v2echo.com/api/push'
export const pushTestBody = '这是一条 V2Echo 测试推送，收到此消息说明推送已到达设备。'
export type PushTestState = { next_allowed: number; latest: PushHistoryItem | null }
const statuses: Record<string, string> = { pending: '待处理', apns_accepted: 'APNs 已接收', rejected: '已拒绝', blocked: '需重新配对', expired: '已过期', skipped: '已跳过' }
function at(value: number) { return value ? new Date(value * 1000).toLocaleString('zh-CN', { hour12: false }) : '尚未记录' }

type Props = {
  username: string; enabled: boolean; verified: boolean; paired: boolean; blocked: boolean
  busy: boolean; nextSync: number; state: PushTestState
  onSend: () => void; onSettings: () => void; onHistory: () => void
}

export default function PushTest({ username, enabled, verified, paired, blocked, busy, nextSync, state, onSend, onSettings, onHistory }: Props) {
  const latest = state.latest
  const waitingReceipt = latest?.status === 'pending' && latest.submitted
  const receiptNext = Math.max(nextSync, latest?.next_attempt ?? 0)
  const now = Date.now()
  const pending = latest?.status === 'pending' && new Date(latest.created_at).getTime() + 15 * 60_000 > now
  const cooling = state.next_allowed * 1000 > now
  const ready = verified && enabled && paired && !blocked
  const reason = !verified ? '等待当前账号验证通过。' : !paired ? '请先配对 App 中生成的接收设备。' : blocked ? '推送凭据已失效，请重新配对接收设备。' : !enabled ? '请先启用同步与上报。' : ''
  return <div className="settings push-test">
    <section className="panel">
      <h2>向当前接收设备发送测试</h2>
      <p className="muted">使用当前账号的推送连接发送一条测试消息，不需要等待新的 V2EX 通知。</p>
      <dl className="details">
        <div><dt>接收账号</dt><dd>{username ? `@${username}` : '尚未验证'}</dd></div>
        <div><dt>推送服务</dt><dd>{pushServiceURL}</dd></div>
        <div><dt>测试标题</dt><dd>{username ? `@${username}` : '@用户名'}</dd></div>
        <div><dt>测试正文</dt><dd>{pushTestBody}</dd></div>
      </dl>
      {!ready && <div className="notice test-notice"><span>{reason}</span><button type="button" onClick={onSettings}>连接与设置</button></div>}
      <div className="test-actions"><button className="primary" type="button" disabled={busy || !ready || cooling || pending} onClick={onSend}>{busy ? '正在提交…' : pending ? '等待测试结果' : cooling ? '测试冷却中' : '发送测试推送'}</button><button className="text-button" type="button" onClick={onHistory}>查看推送历史</button></div>
      <p className="caption">每三分钟可创建一次测试，上一条仍在处理时请等待。测试走正常上报队列，保留冷却、额度限制和错误退避，有效期为 15 分钟。</p>
      {cooling && <p className="caption">下次可创建测试：{at(state.next_allowed)}</p>}
      {ready && nextSync * 1000 > now && <p className="caption">下次允许与推送服务通信：{at(nextSync)}。提交后可能需要等待这一轮上报；回执也在后续轮次更新。</p>}
    </section>
    <section className="panel" aria-label="最近一次推送测试">
      <div className="section-heading"><h2>最近一次测试</h2>{latest && <span className={`status-pill ${['blocked', 'rejected', 'expired'].includes(latest.status) ? 'warning' : latest.status === 'apns_accepted' ? 'accepted' : 'neutral'}`}>{waitingReceipt ? '等待回执' : statuses[latest.status] ?? latest.status}</span>}</div>
      {!latest ? <p className="empty">还没有测试记录。配对完成后，发送一条测试推送确认连接。</p> : <>
        <p role="status">{deliveryDetail(latest)}</p>
        {waitingReceipt && <p className="caption">手机可能已先收到通知。页面每 5 秒读取本地记录，推送回执则至少间隔 3 分钟查询；刷新页面不会提前查询或重新发送。</p>}
        <dl className="details">
          <div><dt>创建时间</dt><dd>{new Date(latest.created_at).toLocaleString('zh-CN', { hour12: false })}</dd></div>
          <div><dt>首次上报</dt><dd>{at(latest.first_attempt_at)}</dd></div>
          {waitingReceipt && <div><dt>下次允许查询回执</dt><dd>{!enabled ? '同步已暂停，启用后继续查询' : blocked ? '请重新配对后恢复' : receiptNext * 1000 > now ? at(receiptNext) : '等待下一轮同步'}</dd></div>}
          <div><dt>结果记录</dt><dd>{at(latest.finished_at)}</dd></div>
          <div><dt>事件编号</dt><dd><code>{latest.event_id}</code></dd></div>
        </dl>
      </>}
      <p className="caption">APNs 已接收表示苹果推送服务接受了消息，不代表设备已送达。请在 App 端确认是否看到上述测试文案；通知权限、专注模式和设备网络都可能影响展示。</p>
      <p className="caption">测试消息点击后进入 App 通知入口，不会增加真实通知记录或修改未读数。App 前台停留在通知页时可能不展示横幅，可切到其他页面或后台测试。</p>
    </section>
  </div>
}
