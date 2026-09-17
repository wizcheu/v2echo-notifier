import { useEffect, useRef, useState } from 'react'
import QRCode from 'react-qr-code'
import { api, APIError } from './api'

type Invitation = { code: string; payload: string; expires_at: string }

export default function PairingQR({ accountID, username, cookie, disabled, onPaired, onExpired }: {
  accountID: string; username: string; cookie: string; disabled: boolean
  onPaired: () => Promise<void>; onExpired: () => void
}) {
  const [invitation, setInvitation] = useState<Invitation | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [now, setNow] = useState(Date.now())
  const [copied, setCopied] = useState(false)
  const active = useRef(true)
  useEffect(() => { active.current = true; return () => { active.current = false } }, [])
  const remaining = invitation ? Math.max(0, Math.ceil((Date.parse(invitation.expires_at) - now) / 1000)) : 0

  useEffect(() => {
    if (!invitation) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [invitation])

  const complete = async () => {
    if (busy || !invitation) return
    setBusy(true); setError('')
    try {
      const result = await api<{ paired: boolean }>(`accounts/${encodeURIComponent(accountID)}/pair/qr/complete`, 'POST', { code: invitation.code })
      if (!active.current) return
      if (result.paired) {
        setInvitation(null)
        await onPaired()
      } else {
        setError('尚未收到 App 的扫码请求，请在 App 提交后，再点击完成绑定。')
      }
    } catch (e) {
      if (!active.current) return
      if (e instanceof APIError && e.status === 401) onExpired()
      setError(e instanceof Error ? e.message : '暂时无法确认配对结果，请重试。')
    } finally { if (active.current) setBusy(false) }
  }

  const generate = async () => {
    if (busy) return
    setBusy(true); setError(''); setCopied(false)
    try {
      const result = await api<Invitation>(`accounts/${encodeURIComponent(accountID)}/pair/qr`, 'POST', { cookie })
      if (active.current) { setInvitation(result); setNow(Date.now()) }
    } catch (e) {
      if (!active.current) return
      if (e instanceof APIError && e.status === 401) onExpired()
      setError(e instanceof Error ? e.message : '无法生成二维码，请重试。')
    } finally { if (active.current) setBusy(false) }
  }

  return <div className="pairing-qr">
    <h3>使用 App 扫码绑定</h3>
    <p className="muted">在 V2Echo 的「远程推送」中开启本机推送，再点击「扫码绑定通知助手」。两端均需登录 @{username}。</p>
    {invitation && remaining > 0 ? <>
      <div className="pairing-qr-image"><QRCode value={invitation.payload} size={224} level="M" title="通知助手配对二维码" /></div>
      <p role="status">等待 App 扫码 · 剩余 {Math.floor(remaining / 60)} 分 {remaining % 60} 秒</p>
      <button type="button" className="primary pairing-qr-action" disabled={disabled || busy} onClick={() => void complete()}>{busy ? '正在确认…' : '已扫码，完成绑定'}</button>
      <button type="button" onClick={() => void navigator.clipboard.writeText(invitation.payload).then(() => setCopied(true)).catch(() => setError('复制失败，请手动选择下方配对内容。'))}>{copied ? '已复制' : '复制配对内容'}</button>
      <details><summary>无法扫码？</summary><p className="caption">可复制到 Mac 或当前手机 App 的「粘贴助手配对内容」输入框。</p><code className="pairing-qr-payload">{invitation.payload}</code></details>
      <p className="caption">App 提交后点击「已扫码，完成绑定」，不会自动查询配对结果。二维码仅供自己的接收设备使用；成功后将替换原有连接。</p>
    </> : <>
      {invitation && <p role="status">二维码已过期，请重新生成。</p>}
      <button type="button" className="primary pairing-qr-action" disabled={disabled || busy} onClick={() => void generate()}>{busy ? '正在生成…' : invitation ? '重新生成配对二维码' : '生成配对二维码'}</button>
    </>}
    {error && <p className="notice" role="alert">{error}</p>}
  </div>
}
