import { useEffect, useState } from 'react'
import { useRoute } from './useRoute'
import { navigate } from './routes'
import { readHistoryLocation, historyHref } from './historyLocation'
import PushDetail from './PushDetail'
import { formatTime } from './formatTime'

export type PushHistoryItem = {
  sequence: number
  event_id: string
  type: string
  status: string
  submitted: boolean
  attempts: number
  next_attempt: number
  detail: string
  notification_id: number
  title: string
  body: string
  unread_count: number
  created_at: string
  queued_at: number
  first_attempt_at: number
  last_attempt_at: number
  submitted_at: number
  finished_at: number
}
export type PushHistoryPage = { items: PushHistoryItem[]; next_cursor: number }
const statuses: Record<string, string> = {
  pending: '待处理',
  apns_accepted: 'APNs 已接收',
  rejected: '已拒绝',
  blocked: '需更新凭据',
  expired: '已过期',
  skipped: '已跳过',
}
const at = formatTime

export default function PushHistory({
  loadPage,
  username,
}: {
  loadPage: (query: string) => Promise<PushHistoryPage>
  username: string
}) {
  const { hash } = useRoute()
  const location = readHistoryLocation(hash)
  const { query, status, cursors } = location
  const [search, setSearch] = useState(query)
  useEffect(() => setSearch(query), [query])
  function update(changes: Partial<typeof location>) {
    navigate(historyHref(hash, { ...location, event: '', ...changes }))
  }
  const [revision, setRevision] = useState(0)
  const [page, setPage] = useState<PushHistoryPage | null>(null)
  const [loading, setLoading] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')
  const before = cursors[cursors.length - 1]!
  useEffect(() => {
    let cancelled = false,
      pending = false,
      hasPage = false
    setPage(null)
    setError('')
    async function refresh() {
      if (pending) return
      pending = true
      setRefreshing(true)
      if (!hasPage) setLoading(true)
      try {
        const params = new URLSearchParams({ before: String(before), limit: '20', status, q: query })
        const result = await loadPage(params.toString())
        if (!cancelled) {
          hasPage = true
          setPage(result)
          setError('')
        }
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : '无法读取推送历史')
      } finally {
        pending = false
        if (!cancelled) {
          setLoading(false)
          setRefreshing(false)
        }
      }
    }
    void refresh()
    const timer = window.setInterval(() => void refresh(), 5000)
      return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [before, query, status, revision, loadPage])

useEffect(() => {window.scrollTo(0, 0); document.title = `${location.event ? '推送详情' : '推送历史'} · V2Echo`}, [location.event])
  if (location.event) return <PushDetail item={page?.items.find(item => item.event_id === location.event)} loading={!page} error={error} username={username} onBack={() => update({event:''})} />

  return (
    <section className="panel push-history content-surface">
      <form
        className="history-filters"
        onSubmit={(event) => {
          event.preventDefault()
          update({ cursors: [0], query: search.trim() })
          setRevision((r) => r + 1)
        }}
      >
        <label>
          搜索内容
          <input
            type="text"
            maxLength={200}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="通知标题、摘要或编号"
          />
        </label>
        <label>
          处理状态
          <select
            value={status}
            onChange={(e) => {
              update({ cursors: [0], status: e.target.value })
            }}
          >
            <option value="">全部状态</option>
            {Object.entries(statuses).map(([key, label]) => (
              <option key={key} value={key}>
                {label}
              </option>
            ))}
          </select>
        </label>
        <button type="submit" className="primary" disabled={loading}>
          搜索
        </button>
      </form>
      <div className="section-heading history-toolbar">
        <p className="caption">每 5 秒自动更新 · 按入队时间倒序 · APNs 已接收不代表设备已送达</p>
        <button
          className="text-button"
          type="button"
          disabled={refreshing}
          onClick={() => {
            update({ cursors: [0] })
            setRevision((r) => r + 1)
          }}
        >
          刷新最新记录
        </button>
      </div>
      {(query || status) && (
        <div className="filter-summary">
          <span>
            {query ? `搜索「${query}」` : '所有内容'} · {statuses[status] || '全部状态'}
          </span>
          <button
            className="text-button"
            type="button"
            onClick={() => {
              setSearch('')
              update({ query: '', status: '', cursors: [0] })
            }}
          >
            清除筛选
          </button>
        </div>
      )}
      {error && (
        <p className="notice error" role="alert">
          {error}。当前记录可能尚未更新，请刷新重试。
        </p>
      )}
      <div aria-busy={loading}>
        {!!page?.items.length && (
          <div className="history-columns" aria-hidden="true">
            <span>事件与摘要</span>
            <span>处理结果</span>
            <span>首次上报</span>
            <span />
          </div>
        )}
        {!page ? (
          <p className="empty" role="status">
            {error ? '暂时无法显示推送历史。' : '正在读取推送历史…'}
          </p>
        ) : page.items.length === 0 ? (
          <p className="empty">
            {query || status
              ? '没有符合条件的推送记录。'
              : '还没有推送记录。通知或网页未读汇总进入上报队列后会显示在这里。'}
          </p>
        ) : (
          <ul className="push-history-list" aria-label="推送事件">
            {page.items.map((item) => {
              const initial = item.type === 'initial_unread'
              const summary = initial || item.type === 'unread_summary'
              const test = item.type === 'test'
              const label =
                item.status === 'pending' && item.submitted ? '等待回执' : (statuses[item.status] ?? item.status)
              const tone = ['rejected', 'blocked', 'expired'].includes(item.status)
                ? 'warning'
                : item.status === 'apns_accepted'
                  ? 'accepted'
                  : 'neutral'
              return (
                <li key={item.event_id}>
                  <details open={location.event === item.event_id}>
                    <summary
                      className="history-row"
                      onClick={(e) => {
                        e.preventDefault()
                        update({ event: location.event === item.event_id ? '' : item.event_id })
                      }}
                    >
                      <span className="history-message">
                        <strong>
                          {summary
                            ? `${initial ? '首次配对时有' : '网页显示有'} ${item.unread_count} 条未读提醒`
                            : test
                              ? `${username ? `@${username} · ` : ''}V2Echo 测试推送`
                              : item.title || '新通知'}
                        </strong>
                        <small>
                          {summary
                            ? initial
                              ? '首次未读汇总'
                              : '网页未读汇总'
                            : test
                              ? '测试推送'
                              : `通知 #${item.notification_id}`}{' '}
                          · 同步 {item.attempts} 次（含回执查询）
                        </small>
                      </span>
                      <span className={`status-pill ${tone}`}>{label}</span>
                      <span className="history-time">
                        {item.first_attempt_at
                          ? at(item.first_attempt_at, false)
                          : item.attempts
                            ? '旧记录未保存时间'
                            : '尚未尝试'}
                      </span>
                      <span className="detail-label">
                        {location.event === item.event_id ? '收起' : '详情'} <span aria-hidden="true">›</span>
                      </span>
                    </summary>

                  </details>
                </li>
              )
            })}
          </ul>
        )}
      </div>
      <div className="history-pagination">
        <button
          type="button"
          disabled={loading || cursors.length === 1}
          onClick={() => update({ cursors: cursors.slice(0, -1) })}
        >
          上一页
        </button>
        <span>第 {cursors.length} 页 · 每页 20 条</span>
        <button
          type="button"
          disabled={loading || !page?.next_cursor}
          onClick={() => {
            if (page?.next_cursor) update({ cursors: [...cursors, page.next_cursor] })
          }}
        >
          下一页
        </button>
      </div>
      <p className="caption">
        历史按进入队列的先后倒序排列，保存在当前服务器。升级前未记录的时间显示为“未记录”；首次导入且未安排推送的通知仅在“通知记录”中展示。
      </p>
    </section>
  )
}
