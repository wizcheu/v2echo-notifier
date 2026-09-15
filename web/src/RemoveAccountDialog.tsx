import { useEffect, useId, useRef } from 'react'

export default function RemoveAccountDialog({ username, busy, error, onCancel, onConfirm }: {
  username: string; busy: boolean; error?: string; onCancel: () => void; onConfirm: () => void
}) {
  const dialog = useRef<HTMLDialogElement>(null)
  const title = useId()
  useEffect(() => { const el = dialog.current!; el.showModal(); return () => el.close() }, [])
  return <dialog ref={dialog} className="remove-dialog" aria-labelledby={title} onCancel={event => {event.preventDefault(); if (!busy) onCancel()}}>
    <h2 id={title}>移除 {username ? `@${username}` : '此待验证账号'}？</h2>
    <p className="muted">此操作会删除当前服务器保存的该账号数据：</p>
    <p>凭据、通知历史与推送队列。<br />账号的同步与上报也会停止。</p>
    <p className="caption">不会删除 V2EX 网站账号，其他账号不受影响。</p>
    {error && <p className="notice error" role="alert">{error}</p>}
    <div className="dialog-actions"><button autoFocus disabled={busy} onClick={onCancel}>取消</button><button className="danger-button" disabled={busy} onClick={onConfirm}>{busy ? '正在移除…' : '确认移除'}</button></div>
  </dialog>
}
