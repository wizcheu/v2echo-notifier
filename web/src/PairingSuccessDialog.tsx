import { useEffect, useId, useRef } from 'react'

export default function PairingSuccessDialog({ username, onTest, onClose }: {
  username: string; onTest: () => void; onClose: () => void
}) {
  const dialog = useRef<HTMLDialogElement>(null)
  const title = useId()
  const description = useId()
  const refreshHint = useId()

  useEffect(() => {
    const element = dialog.current!
    element.showModal()
    return () => element.close()
  }, [])

  return <dialog ref={dialog} className="pairing-success-dialog" aria-labelledby={title} aria-describedby={`${description} ${refreshHint}`}
    onCancel={event => { event.preventDefault(); onClose() }}>
    <h2 id={title}>已成功绑定</h2>
    <p id={description}>@{username} 的接收设备已绑定成功。可以发送一条测试推送，确认 App 能收到通知。</p>
    <p id={refreshHint}>请在 App 的「远程推送」页面点击刷新按钮，更新配对状态。</p>
    <div className="dialog-actions">
      <button type="button" className="primary" autoFocus onClick={onTest}>去发送测试推送</button>
      <button type="button" onClick={onClose}>我知道了</button>
    </div>
  </dialog>
}
