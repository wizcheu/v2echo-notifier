import { useState } from 'react'
import { api, APIError } from './api'
import BrowserInstallGuide from './BrowserInstallGuide'

export type BrowserStatus = { available: boolean; enabled: boolean; required: boolean }

function showWindowStatus(tab: Window | null, title: string, message: string) {
  if (!tab || tab.closed) return
  try {
    tab.document.title = title
    const heading = tab.document.createElement('h1')
    heading.textContent = title
    const detail = tab.document.createElement('p')
    detail.textContent = message
    const hint = tab.document.createElement('p')
    hint.textContent = '请保留管理页。若准备失败，请返回管理页处理后重试。'
    tab.document.body.replaceChildren(heading, detail, hint)
  } catch {
    // The user may have navigated the reserved tab while preparation ran.
  }
}

export default function BrowserVerification({ status, accountPath, refresh, onExpired, onDisabled }: {
  status: BrowserStatus
  accountPath: (path: string) => string
  refresh: () => Promise<void>
  onExpired: () => void
  onDisabled: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [opened, setOpened] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  async function run(operation: 'start' | 'check' | 'disable') {
    // Reserve a tab during the click so async preparation is not popup-blocked.
    const tab = operation === 'start' ? window.open('about:blank', '_blank') : null
    if (tab) tab.opener = null
    if (operation === 'start') {
      setOpened(false)
      showWindowStatus(tab, '正在准备验证窗口', '正在连接服务器上的浏览器，请稍候…')
    }
    setBusy(true)
    setError('')
    setMessage('')
    try {
      const result = await api<{ desktop_url: string; unread_count: number }>(accountPath(`browser/${operation}`), 'POST', {})
      if (operation === 'start') {
        setOpened(true)
        if (tab && !tab.closed) tab.location.replace(result.desktop_url)
        else setMessage('验证窗口已准备好，请点击下方“进入验证窗口”。')
      } else if (operation === 'check') {
        setOpened(false)
        setMessage(`首页读取成功，当前有 ${result.unread_count} 条未读。后续检查将沿用浏览器会话。`)
      } else {
        setOpened(false)
        onDisabled()
      }
    } catch (e) {
      const detail = e instanceof Error ? e.message : '浏览器操作失败，请重试'
      showWindowStatus(tab, '验证窗口准备失败', detail)
      setError(detail)
      if (e instanceof APIError && e.status === 401) onExpired()
    } finally {
      try {
        await refresh()
      } catch (e) {
        setError(e instanceof Error ? e.message : '状态刷新失败，请重试')
        if (e instanceof APIError && e.status === 401) onExpired()
      } finally {
        setBusy(false)
      }
    }
  }

  return <section className="panel browser-verification" aria-label="浏览器验证">
    <h2>{status.enabled && !status.available ? '浏览器兼容模式暂不可用' : status.required ? '首页访问需要验证' : '浏览器兼容模式'}</h2>
    <p>{status.enabled && !status.available
      ? '此账号仍启用了浏览器兼容模式，但当前未配置浏览器服务，无法读取首页。如果当前代理可以正常访问，可关闭兼容模式，使用已保存的代理和 Cookie 进行检查。'
      : status.required
      ? '服务器已连接到 V2EX，但首页要求验证或拒绝了访问。可以打开验证窗口，完成后重新读取首页；直接封禁不一定能通过验证解除。'
      : status.enabled ? '首页通过服务器上的浏览器读取，API 与推送按原方式运行。' : '当代理触发访问验证时，可通过浏览器窗口手动完成。'}</p>
    {!status.available
      ? <>
        {status.enabled && <div className="browser-verification-actions">
          <button disabled={busy} onClick={() => void run('disable')}>{busy ? '正在处理…' : '关闭兼容模式，使用当前代理'}</button>
        </div>}
        <BrowserInstallGuide />
      </>
      : <>
        <p>窗口使用已保存的代理和账号 Cookie。请仅完成访问验证，不要切换账号。每次窗口有效期为 10 分钟，验证后回到此页重试。</p>
        {!window.isSecureContext && <p role="alert">验证窗口需要 HTTPS，请通过 HTTPS 打开管理页后使用。</p>}
        <div className="browser-verification-actions">
          <button disabled={busy || !window.isSecureContext} onClick={() => void run('start')}>{busy ? '正在处理…' : '打开验证窗口'}</button>
          {(opened || status.enabled) && <button disabled={busy} onClick={() => void run('check')}>验证完成，重试首页</button>}
          {status.enabled && <button disabled={busy} onClick={() => void run('disable')}>关闭兼容模式</button>}
          {opened && <a href="/browser-desktop/" target="_blank" rel="noreferrer">进入验证窗口</a>}
        </div>
      </>}
    {error && <p role="alert">{error}</p>}
    {message && <p role="status">{message}</p>}
  </section>
}
