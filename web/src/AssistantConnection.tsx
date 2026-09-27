import { useEffect, useState } from 'react'
import { api, APIError } from './api'

type Connection = { enabled: boolean; last_error: string }
export default function AssistantConnection({ onExpired }: { onExpired: () => void }) {
  const [state, setState] = useState<Connection | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [consent, setConsent] = useState(false)
  useEffect(() => {
    let active = true
    api<Connection>('assistant-connection').then(value => { if (active) setState(value) }).catch(e => {
      if (!active) return
      if (e instanceof APIError && e.status === 401) onExpired()
      setError(e instanceof Error ? e.message : '无法读取连接状态')
    })
    return () => { active = false }
  }, [onExpired])
  const enable = async () => {
    setBusy(true); setError('')
    try { setState(await api<Connection>('assistant-connection', 'POST', {})) }
    catch (e) {
      if (e instanceof APIError && e.status === 401) onExpired()
      setError(e instanceof Error ? e.message : '无法启用多账号复用')
    } finally { setBusy(false) }
  }
  return <section className="panel">
    <h2>多账号复用</h2>
    <p className="muted">设备完成一个账号的配对后，可在 App 开启「多账号复用」。同一 App 已登录、且本助手已验证的其他账号将自动连接，不必再次扫码。</p>
    <p className="caption">每个账号仍需分别配置 API Token 和 Cookie。新增账号在连接同步后可用，通常不超过三分钟；在 App 刷新状态即可关联。跨账号授权只保存在那台设备，不通过 iCloud 扩大到其他账号或设备。</p>
    {!state?.enabled && <label><input type="checkbox" checked={consent} onChange={e => setConsent(e.target.checked)} disabled={busy} />允许已授权的 App 自动连接本助手当前及以后添加的匹配账号</label>}
    <button type="button" disabled={busy || (!state?.enabled && !consent)} onClick={() => void enable()}>{busy ? '正在同步…' : state?.enabled ? '立即同步账号连接' : '启用多账号复用'}</button>
    {state?.enabled && <p role="status">已启用。请在已配对的 App 确认启用多账号复用；关闭单账号提醒或断开整个助手均在 App 操作。</p>}
    {(error || state?.last_error) && <p role="alert">{error || state?.last_error}</p>}
  </section>
}
