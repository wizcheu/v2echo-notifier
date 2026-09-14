import { useEffect, useState } from 'react'
import { pushTestBody } from './PushTest'
import { deliveryDetail } from './deliveryDetail'

export type PushHistoryItem = {
  sequence: number; event_id: string; type: string; status: string; submitted: boolean
  attempts: number; next_attempt: number; detail: string; notification_id: number
  title: string; body: string; unread_count: number; created_at: string
  queued_at: number; first_attempt_at: number; last_attempt_at: number; submitted_at: number; finished_at: number
}
export type PushHistoryPage = { items: PushHistoryItem[]; next_cursor: number }
const statuses: Record<string, string> = { pending: '待处理', apns_accepted: 'APNs 已接收', rejected: '已拒绝', blocked: '需更新凭据', expired: '已过期', skipped: '已跳过' }
function at(value: string | number) {
  if (!value || (typeof value === 'string' && value.startsWith('0001'))) return '未记录'
  return new Date(typeof value === 'number' ? value * 1000 : value).toLocaleString('zh-CN', { hour12: false })
}

export default function PushHistory({ loadPage, username }: { loadPage: (query: string) => Promise<PushHistoryPage>; username: string }) {
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [status, setStatus] = useState('')
  const [cursors, setCursors] = useState([0])
  const [revision, setRevision] = useState(0)
  const [page, setPage] = useState<PushHistoryPage | null>(null)
  const [loading, setLoading] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')
  const before = cursors[cursors.length - 1]!
  useEffect(() => {
    let cancelled = false, pending = false, hasPage = false
    setPage(null)
    setError('')
    async function refresh() {
      if (pending) return
      pending = true; setRefreshing(true)
      if (!hasPage) setLoading(true)
      try {
        const params = new URLSearchParams({ before: String(before), limit: '20', status, q: query })
        const result = await loadPage(params.toString())
        if (!cancelled) { hasPage = true; setPage(result); setError('') }
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : '无法读取推送历史')
      } finally {
        pending = false
        if (!cancelled) { setLoading(false); setRefreshing(false) }
      }
    }
    void refresh()
    const timer = window.setInterval(() => void refresh(), 5000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [before, query, status, revision, loadPage])

  return <section className="panel push-history">
    <div className="section-heading"><div><h2>上报记录</h2><p className="caption">每 5 秒自动更新 · 按入队时间倒序</p></div><button type="button" disabled={refreshing} onClick={() => { setCursors([0]); setRevision(r => r + 1) }}>刷新最新记录</button></div>
    <p className="muted">查看当前账号的通知摘要、上报尝试和处理结果。APNs 已接收不代表设备已送达；结果时间是助手取得回执的时间。</p>
    <form className="history-filters" onSubmit={event => { event.preventDefault(); setCursors([0]); setQuery(search.trim()); setRevision(r => r + 1) }}>
      <label>搜索内容<input type="text" maxLength={200} value={search} onChange={e => setSearch(e.target.value)} placeholder="通知标题、摘要或编号" /></label>
      <label>处理状态<select value={status} onChange={e => { setCursors([0]); setStatus(e.target.value) }}><option value="">全部状态</option>{Object.entries(statuses).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</select></label>
      <button type="submit" className="primary" disabled={loading}>搜索</button>
    </form>
    {(query || status) && <div className="filter-summary"><span>{query ? `搜索「${query}」` : '所有内容'} · {statuses[status] || '全部状态'}</span><button className="text-button" type="button" onClick={() => { setSearch(''); setQuery(''); setStatus(''); setCursors([0]) }}>清除筛选</button></div>}
    {error && <p className="notice error" role="alert">{error}。当前记录可能尚未更新，请刷新重试。</p>}
    <div aria-busy={loading}>
      {!page ? <p className="empty" role="status">{error ? '暂时无法显示推送历史。' : '正在读取推送历史…'}</p> : page.items.length === 0 ? <p className="empty">{query || status ? '没有符合条件的推送记录。' : '还没有推送记录。通知或网页未读汇总进入上报队列后会显示在这里。'}</p> :
        <ul className="push-history-list">{page.items.map(item => {
          const initial = item.type === 'initial_unread'
          const summary = initial || item.type === 'unread_summary'
          const test = item.type === 'test'
          const label = item.status === 'pending' && item.submitted ? '等待回执' : statuses[item.status] ?? item.status
          const tone = ['rejected', 'blocked', 'expired'].includes(item.status) ? 'warning' : item.status === 'apns_accepted' ? 'accepted' : 'neutral'
          return <li key={item.event_id}>
            <div className="history-timestamp"><span>首次上报尝试</span><strong>{item.first_attempt_at ? at(item.first_attempt_at) : item.attempts ? '旧记录未保存时间' : '尚未尝试'}</strong><small>{summary ? (initial ? '首次未读汇总' : '网页未读汇总') : test ? '测试推送' : `通知 #${item.notification_id}`}</small></div>
            <div className="history-message">
            <div className="history-item-heading"><span className={`status-pill ${tone}`}>{label}</span>{item.finished_at > 0 && <small>结果记录：{at(item.finished_at)}</small>}</div>
            <h3>{summary ? `${initial ? '首次配对时有' : '网页显示有'} ${item.unread_count} 条未读提醒` : item.title || '新通知'}</h3>
            {!summary && <p className="history-content">{item.body || '此通知没有正文摘要。'}</p>}
            <p className="history-result">{deliveryDetail(item)} · 同步 {item.attempts} 次（含回执查询）</p>
            <details><summary>时间与推送详情</summary>
              <dl className="details">
                <div><dt>{summary ? '未读数观察时间' : test ? '测试创建时间' : '通知产生时间'}</dt><dd>{at(item.created_at)}</dd></div>
                <div><dt>加入队列</dt><dd>{at(item.queued_at)}</dd></div>
                <div><dt>最近同步尝试</dt><dd>{at(item.last_attempt_at)}</dd></div>
                {item.type === 'unread_summary' && <div><dt>API 首条消息 ID</dt><dd>{item.notification_id}</dd></div>}
                <div><dt>服务端首次确认</dt><dd>{at(item.submitted_at)}</dd></div>
                <div><dt>结果记录</dt><dd>{at(item.finished_at)}</dd></div>
                <div><dt>事件编号</dt><dd><code>{item.event_id}</code></dd></div>
              </dl>
              <p className="caption">当前提醒模板参考：{username ? `@${username}` : '@用户名'} / {test ? pushTestBody : summary ? `你有 ${item.unread_count} 条未读提醒，打开应用查看` : '有新通知，打开应用查看'}。实际展示由推送服务和接收设备决定，上方内容为本地保存的上报摘要。</p>
            </details>
            </div>
          </li>
        })}</ul>}
    </div>
    <div className="history-pagination"><button type="button" disabled={loading || cursors.length === 1} onClick={() => setCursors(c => c.slice(0, -1))}>上一页</button><span>第 {cursors.length} 页 · 每页 20 条</span><button type="button" disabled={loading || !page?.next_cursor} onClick={() => { if (page?.next_cursor) setCursors(c => [...c, page.next_cursor]) }}>下一页</button></div>
    <p className="caption">历史按进入队列的先后倒序排列，保存在当前服务器。升级前未记录的时间显示为“未记录”；首次导入且未安排推送的通知仅在“通知记录”中展示。</p>
  </section>
}
