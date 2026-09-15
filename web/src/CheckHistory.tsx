import { useEffect, useRef, useState } from 'react'
import { api, APIError } from './api'
import { accountHref, navigate } from './routes'
import { useRoute } from './useRoute'
import { checkHistoryHref, readCheckLocation } from './checkHistoryLocation'
import { formatTime } from './formatTime'
import Icon from './Icon'

type Stage = { status: string; http_status?: number; duration_ms: number; detail: string }
export type CheckRecord = {
  id: number
  started_at: string
  finished_at: string
  duration_ms: number
  trigger: string
  mode: string
  status: 'completed' | 'partial' | 'failed'
  unread_count: number | null
  first_id: number | null
  previous_first_id: number | null
  web: Stage
  api: Stage
  summary: string
  outcome: string
  event_id?: string
}
type CheckPage = { items: CheckRecord[]; limit: number }
const results = {
  completed: { label: '检查完成', tone: 'accepted' },
  partial: { label: '部分完成', tone: 'partial' },
  failed: { label: '检查失败', tone: 'warning' },
}
const duration = (ms: number) => `${(ms / 1000).toFixed(1)} 秒`

export default function CheckHistory({ accountID, onExpired }: { accountID: string; onExpired: () => void }) {
  const { hash } = useRoute()
  const location = readCheckLocation(hash)
  const [records, setRecords] = useState<CheckRecord[] | null>(null)
  const [pendingRecords, setPendingRecords] = useState<CheckRecord[] | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')
  const refreshRef = useRef<(force?: boolean) => Promise<void>>(async () => {})
  const expandedRef = useRef(location.record)
  expandedRef.current = location.record
  const expiredRef = useRef(onExpired)
  expiredRef.current = onExpired

  useEffect(() => {
    let cancelled = false, loading = false, loaded = false
    let current: CheckRecord[] = []
    setRecords(null)
    setPendingRecords(null)
    setError('')
    async function refresh(force = false) {
      if (loading) return
      loading = true
      setRefreshing(true)
      try {
        const page = await api<CheckPage>(`accounts/${encodeURIComponent(accountID)}/check-history`)
        if (cancelled) return
        // Completed records are immutable. Hold new rows while someone reads details.
        if (loaded && expandedRef.current && !force && page.items[0]?.id !== current[0]?.id) {
          setPendingRecords(page.items)
        } else if (!loaded || force || !expandedRef.current) {
          current = page.items
          setRecords(page.items)
          setPendingRecords(null)
        }
        loaded = true
        setError('')
      } catch (e) {
        if (cancelled) return
        if (e instanceof APIError && e.status === 401) expiredRef.current()
        setError(e instanceof Error ? e.message : '无法读取检查记录，请重试')
      } finally {
        loading = false
        if (!cancelled) setRefreshing(false)
      }
    }
    refreshRef.current = refresh
    void refresh()
    const timer = window.setInterval(() => void refresh(), 5000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [accountID])

  useEffect(() => {
    if (!location.record) void refreshRef.current()
  }, [location.record])

  function update(changes: Partial<typeof location>) {
    navigate(checkHistoryHref(accountHref(accountID, 'checkHistory'), { ...location, ...changes }))
  }
  const attention = records?.filter(item => item.status !== 'completed').length ?? 0
  const visible = records?.filter(item => !location.attention || item.status !== 'completed') ?? []
  const missing = records && location.record && !records.some(item => item.id === location.record)
  return <>
    <section className="check-history content-surface" aria-labelledby="check-history-title">
      <div className="section-heading check-toolbar">
        <div><h2 id="check-history-title">最近检查</h2><p className="caption">{records ? `已记录 ${records.length} 条 · 最新在前` : '每个账号保留最近 50 条'}</p></div>
        <button type="button" disabled={refreshing} onClick={() => void refreshRef.current(true)}>{refreshing ? '正在刷新…' : '刷新记录'}</button>
      </div>
      <nav className="check-filters" aria-label="检查记录筛选">
        <a href={checkHistoryHref(accountHref(accountID, 'checkHistory'), { attention: false, record: null })} aria-current={!location.attention ? 'true' : undefined}>全部 <span>{records?.length ?? '—'}</span></a>
        <a href={checkHistoryHref(accountHref(accountID, 'checkHistory'), { attention: true, record: null })} aria-current={location.attention ? 'true' : undefined}>需要关注 <span>{records ? attention : '—'}</span></a>
      </nav>
      {error && <div className="notice error" role="alert"><span>{error}{records ? '；当前显示上次读取的记录。' : ''}</span><button onClick={() => void refreshRef.current(true)} disabled={refreshing}>重试</button></div>}
      {pendingRecords && <div className="notice check-new-records" role="status"><span>有新的检查记录，当前阅读位置已保留。</span><button className="text-button" onClick={() => void refreshRef.current(true)} disabled={refreshing}>显示最新记录</button></div>}
      {missing && <div className="notice" role="status"><span>这条记录已不在最近 50 条中。</span><button className="text-button" onClick={() => update({ record: null })}>关闭详情</button></div>}
      {!records && !error && <p className="empty" role="status">正在读取检查记录…</p>}
      {records && !visible.length && <div className="empty"><Icon name="check_circle" /><h3>{records.length ? '没有需要关注的检查' : '还没有检查记录'}</h3><p>{records.length ? '最近保留的检查均已完成。' : '启用同步后，实际发起的检查会记录在这里。升级前的检查不会补录。'}</p>{!records.length && <a className="text-link" href={accountHref(accountID, 'sync')}>查看同步设置</a>}</div>}
      {!!visible.length && <>
        <div className="check-columns" aria-hidden="true"><span>检查时间</span><span>结果</span><span>未读数</span><span>耗时</span><span>检查摘要</span><span /></div>
        <ol className="check-list">
          {visible.map(item => {
            const open = location.record === item.id
            const result = results[item.status] ?? results.failed
            return <li key={item.id} className={open ? 'expanded' : undefined}>
              <div className="check-row" onClick={event => { if (!(event.target as HTMLElement).closest('button,a')) update({ record: open ? null : item.id }) }}>
                <div className="check-time"><time dateTime={item.started_at}>{formatTime(item.started_at)}</time><small>{item.trigger === 'manual' ? '手动检查' : '自动检查'}{item.mode === 'api' ? ' · API 同步' : ''}</small></div>
                <span className={`status-pill ${result.tone}`}>{result.label}</span>
                <div className="check-unread"><span className="check-mobile-label">未读 </span><strong>{item.unread_count === null ? '—' : `${item.unread_count} 条`}</strong>{item.unread_count === null && <small>{item.mode === 'api' ? '未读取网页' : '未获取'}</small>}</div>
                <div className="check-duration"><span className="check-mobile-label">{item.trigger === 'manual' ? '手动检查' : '自动检查'} · </span>{duration(item.duration_ms)}</div>
                <div className="check-summary"><p>{item.summary}</p><small>{item.outcome}</small></div>
                <button className="text-button check-expand" aria-expanded={open} aria-controls={`check-detail-${item.id}`} aria-label={`${open ? '收起' : '查看'} ${formatTime(item.started_at)} 的检查详情`} onClick={() => update({ record: open ? null : item.id })}>{open ? '收起' : '详情'}</button>
              </div>
              {open && <div className="check-detail" id={`check-detail-${item.id}`}>
                <h3>本次检查过程</h3>
                <CheckStep title="网页首页" stage={item.web} extra={item.unread_count !== null ? `返回 ${item.unread_count} 条未读` : undefined} />
                <CheckStep title={item.mode === 'api' ? 'API 通知同步' : 'API 首条消息确认'} stage={item.api} extra={`本次首条 ID：${item.first_id ?? '—'}${item.mode === 'web' ? ` · 上次推送基准：${item.previous_first_id ?? '尚未建立'}` : ''}`} />
                <div className="check-detail-footer"><div><p>{item.outcome}</p><small>完成于 {formatTime(item.finished_at)} · 总耗时 {duration(item.duration_ms)}</small></div>{item.event_id && <a className="text-link" href={`${accountHref(accountID, 'pushHistory')}?q=${encodeURIComponent(item.event_id)}`}>查看推送历史</a>}</div>
              </div>}
            </li>
          })}
        </ol>
        <p className="caption check-list-note">仅记录实际发起的检查；“—” 表示本次未获取网页未读数。</p>
      </>}
    </section>
    <p className="caption check-retention">检查记录保存在此服务器。超过 50 条自动移除最早记录。</p>
  </>
}

function CheckStep({ title, stage, extra }: { title: string; stage: Stage; extra?: string }) {
  return <div className="check-step"><span className={`status-pill ${stage.status === 'completed' ? 'accepted' : stage.status === 'failed' ? 'warning' : 'neutral'}`}>{stage.status === 'completed' ? '已完成' : stage.status === 'failed' ? '失败' : '未发起'}</span><div><h4>{title}{stage.http_status ? ` · HTTP ${stage.http_status}` : ''}</h4><p>{stage.detail}</p>{extra && <p>{extra}</p>}{stage.status !== 'skipped' && <small>耗时 {duration(stage.duration_ms)}</small>}</div></div>
}
