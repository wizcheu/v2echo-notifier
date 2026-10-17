import { useLayoutEffect, useId, useRef } from 'react'
import Loader from './components/kokonutui/loader'
import EmptyState from './components/app/EmptyState'
import type { PushHistoryItem } from './PushHistory'
import { deliveryDetail } from './deliveryDetail'
import { pushTestBody } from './PushTest'
import { formatTime } from './formatTime'
import Icon from './Icon'

export default function PushDetail({ item, loading, error, username, onClose, onRetry, onRestoreFocus }: {
  item?: PushHistoryItem; loading: boolean; error: string; username: string
  onClose: () => void; onRetry: () => void; onRestoreFocus: () => void
}) {
  const dialog = useRef<HTMLDialogElement>(null)
  const backdropPress = useRef(false)
  const headingID = useId()
  const summary = item && ['initial_unread', 'unread_summary'].includes(item.type)
  const test = item?.type === 'test'
  const title = item ? summary ? `${item.type === 'initial_unread' ? '首次配对时有' : '网页显示有'} ${item.unread_count} 条未读提醒` : test && !item.test_kind ? `@${username} · V2Echo 测试推送` : item.title || '新通知' : ''
  const labels: Record<string, string> = { apns_accepted: 'APNs 已接收', pending: item?.submitted ? '等待回执' : '待处理', rejected: '已拒绝', blocked: '需重新配对', expired: '已过期', skipped: '已跳过' }

  useLayoutEffect(() => {
    const element = dialog.current!
    const { overflow, paddingRight } = document.body.style
    const scrollbarWidth = window.innerWidth - document.documentElement.clientWidth
    const padding = Number.parseFloat(getComputedStyle(document.body).paddingRight)
    document.body.style.overflow = 'hidden'
    if (scrollbarWidth > 0) document.body.style.paddingRight = `${padding + scrollbarWidth}px`
    element.showModal()
    return () => {
      document.body.style.overflow = overflow
      document.body.style.paddingRight = paddingRight
      element.close()
      onRestoreFocus()
    }
  }, [onRestoreFocus])

  function outsideDialog(x: number, y: number) {
    const box = dialog.current!.getBoundingClientRect()
    return x < box.left || x > box.right || y < box.top || y > box.bottom
  }

  return <dialog ref={dialog} className="push-detail" aria-labelledby={headingID}
    onCancel={event => { event.preventDefault(); onClose() }}
    onPointerDown={event => { backdropPress.current = event.target === event.currentTarget && outsideDialog(event.clientX, event.clientY) }}
    onClick={event => {
      if (backdropPress.current && event.target === event.currentTarget && outsideDialog(event.clientX, event.clientY)) onClose()
      backdropPress.current = false
    }}>
    <header className="push-detail-header">
      <div><h2 id={headingID}>推送详情</h2><p className="caption">{item ? `${summary ? '网页未读汇总' : test ? '推送测试' : '通知'} · 事件编号 ${item.event_id}` : '查看事件处理过程与结果。'}</p></div>
      <button type="button" className="icon-button" autoFocus aria-label="关闭推送详情" onClick={onClose}><Icon name="close" /></button>
    </header>
    <div className="push-detail-body" tabIndex={0} role="region" aria-label="推送详情内容">
      {error && <div className="notice error" role="alert"><p>{error}</p><button type="button" onClick={onRetry}>重试</button></div>}
      {!item ? loading ? <Loader size="sm" title="正在读取推送详情…" subtitle="" /> : <EmptyState title={error ? '暂时无法读取这条记录' : '暂时找不到这条记录'} description={error ? '请检查连接后重试。' : '此记录不在当前列表中，请关闭详情后重新查找。'} /> : <>
        <section className="delivery-result">
          <span className={`status-pill ${item.status === 'apns_accepted' ? 'accepted' : ['pending', 'skipped'].includes(item.status) ? 'neutral' : 'warning'}`}>{labels[item.status] || item.status}</span>
          <h3>{title}</h3><p>{deliveryDetail(item)}</p>
        </section>
        <section className="delivery-process"><h3>处理过程</h3><ol className="process-timeline">
          <li><Icon name="check_circle" /><div><h4>{summary ? '观察未读并加入队列' : test ? '创建测试并加入队列' : '同步通知并加入队列'}</h4><p>{formatTime(item.created_at)} 产生 · {formatTime(item.queued_at)} 入队</p></div></li>
          <li><Icon name={item.submitted ? 'check_circle' : 'time'} /><div><h4>{item.submitted ? '首次上报，服务端确认' : '等待上报与服务端确认'}</h4><p>{formatTime(item.first_attempt_at)} 首次尝试 · {formatTime(item.submitted_at)} 首次确认</p></div></li>
          <li><Icon name={item.finished_at ? 'check_circle' : 'time'} /><div><h4>{item.finished_at ? '查询回执并记录结果' : '等待下一轮查询结果'}</h4><p>{formatTime(item.last_attempt_at)} 最近同步 · {formatTime(item.finished_at)} 记录结果</p>{item.status === 'pending' && <p>下次允许尝试 {formatTime(item.next_attempt)}</p>}</div></li>
        </ol></section>
        <section className="history-template"><h3>当前提醒模板参考</h3>{item.avatar_url && <img className="push-avatar" src={item.avatar_url} alt="发送者头像" referrerPolicy="no-referrer" />}<h4>{summary || (test && !item.test_kind) ? `@${username || '用户名'}` : item.title}</h4><p className="history-content">{test && !item.test_kind ? pushTestBody : summary ? `你有 ${item.unread_count} 条未读提醒，打开应用查看` : item.body || '有新通知，打开应用查看'}</p><p className="caption">{item.notification_id ? `${summary ? 'API 首条消息 ID' : '通知 ID'} ${item.notification_id} · ` : ''}同步 {item.attempts} 次（含回执查询）</p></section>
        <p className="caption">实际展示由推送服务和接收设备决定。上方内容为本地保存的上报摘要。</p>
      </>}
    </div>
  </dialog>
}
