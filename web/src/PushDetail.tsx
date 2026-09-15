import type { PushHistoryItem } from './PushHistory'
import { deliveryDetail } from './deliveryDetail'
import { pushTestBody } from './PushTest'
import { formatTime } from './formatTime'
import Icon from './Icon'

export default function PushDetail({item, loading, error, username, onBack}: {item?: PushHistoryItem;loading:boolean;error:string;username:string;onBack:()=>void}) {
  const summary = item && ['initial_unread','unread_summary'].includes(item.type)
  const test = item?.type === 'test'
  const title = item ? summary ? `${item.type === 'initial_unread' ? '首次配对时有' : '网页显示有'} ${item.unread_count} 条未读提醒` : test ? `@${username} · V2Echo 测试推送` : item.title || '新通知' : ''
  const labels: Record<string,string> = {apns_accepted:'APNs 已接收',pending:item?.submitted?'等待回执':'待处理',rejected:'已拒绝',blocked:'需重新配对',expired:'已过期',skipped:'已跳过'}
  return <div className="push-detail">
    <header className="page-header"><div><h1>推送详情</h1><p className="page-description">{item ? `${summary ? '网页未读汇总' : test ? '推送测试' : '通知'} · 事件编号 ${item.event_id}` : '查看事件处理过程与结果。'}</p></div><button className="text-button" onClick={onBack}>返回推送历史</button></header>
    {error && <p className="notice error" role="alert">{error}</p>}
    {!item ? <p className="empty">{loading ? '正在读取推送详情…' : '此记录不在当前列表中，请返回推送历史重新查找。'}</p> : <div className="content-surface">
      <section className="delivery-result"><span className={`status-pill ${item.status==='apns_accepted'?'accepted':['pending','skipped'].includes(item.status)?'neutral':'warning'}`}>{labels[item.status] || item.status}</span><h2>{title}</h2><p>{deliveryDetail(item)}</p></section>
      <section className="delivery-process"><h2>处理过程</h2><ol className="process-timeline">
        <li><Icon name="check_circle" /><div><h3>{summary ? '观察未读并加入队列' : test ? '创建测试并加入队列' : '同步通知并加入队列'}</h3><p>{formatTime(item.created_at)} 产生 · {formatTime(item.queued_at)} 入队</p></div></li>
        <li><Icon name={item.submitted?'check_circle':'time'} /><div><h3>{item.submitted?'首次上报，服务端确认':'等待上报与服务端确认'}</h3><p>{formatTime(item.first_attempt_at)} 首次尝试 · {formatTime(item.submitted_at)} 首次确认</p></div></li>
        <li><Icon name={item.finished_at?'check_circle':'time'} /><div><h3>{item.finished_at?'查询回执并记录结果':'等待下一轮查询结果'}</h3><p>{formatTime(item.last_attempt_at)} 最近同步 · {formatTime(item.finished_at)} 记录结果</p>{item.status==='pending' && <p>下次允许尝试 {formatTime(item.next_attempt)}</p>}</div></li>
      </ol></section>
      <section className="history-template"><h2>当前提醒模板参考</h2><h3>@{username || '用户名'}</h3><p className="history-content">{test ? pushTestBody : summary ? `你有 ${item.unread_count} 条未读提醒，打开应用查看` : item.body || '有新通知，打开应用查看'}</p><p className="caption">{item.notification_id ? `${summary ? 'API 首条消息 ID' : '通知 ID'} ${item.notification_id} · ` : ''}同步 {item.attempts} 次（含回执查询）</p></section>
      <p className="caption">实际展示由推送服务和接收设备决定。上方内容为本地保存的上报摘要。</p>
    </div>}
  </div>
}
