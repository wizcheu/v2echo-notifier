import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { LayoutGroup, motion, useReducedMotion } from 'motion/react'
import type { Account } from './AccountManager'
import { accountHref, activePage, pages } from './routes'
import type { Page, Route } from './routes'
import Icon from './Icon'
import AccountSwitcher from './AccountSwitcher'
import Brand from './components/app/Brand'
import BuildVersion from './components/app/BuildVersion'

const navigation: Page[] = ['overview', 'history', 'checkHistory', 'pushHistory', 'pushTest', 'settings', 'proxy']

export default function Shell({ accounts, accountID, route, children, onLogout }: {
  accounts: Account[]; accountID: string; route: Route; children: ReactNode; onLogout: () => Promise<void>
}) {
  const [menuOpen, setMenuOpen] = useState(false)
  const [loggingOut, setLoggingOut] = useState(false)
  const [error, setError] = useState('')
  const header = useRef<HTMLElement>(null)
  const menuTrigger = useRef<HTMLButtonElement>(null)
  const reduced = useReducedMotion()
  const selected = route.kind === 'workspace' ? activePage(route.page) : 'accounts'
  const routeKey = route.kind === 'workspace' ? `${route.accountID}/${route.page}` : route.kind
  const title = route.kind === 'workspace' ? pages[route.page].title : route.kind === 'add' ? '添加账号' : '账号管理'

  useEffect(() => {
    setMenuOpen(false)
    document.getElementById('main-content')?.focus({ preventScroll: true })
    window.scrollTo(0, 0)
    document.title = `${title} · V2Echo`
  }, [routeKey, title])
  useEffect(() => {
    if (!menuOpen) return
    const dismiss = (event: PointerEvent) => {
      if (!header.current?.contains(event.target as Node)) setMenuOpen(false)
    }
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { setMenuOpen(false); menuTrigger.current?.focus() }
    }
    document.addEventListener('pointerdown', dismiss)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('pointerdown', dismiss); document.removeEventListener('keydown', escape) }
  }, [menuOpen])

  const selection = <motion.span className="nav-active" layoutId="navigation" aria-hidden="true"
    transition={reduced ? { duration: 0 } : { type: 'spring', stiffness: 430, damping: 36 }} />
  return <div className="app-shell">
    <button className="skip-link" onClick={() => document.getElementById('main-content')?.focus()}>跳到主要内容</button>
    <header className={`app-header ${menuOpen ? 'menu-open' : ''}`} ref={header}>
      <div className="app-header-inner">
        <a className="brand-link" href={accountID ? accountHref(accountID) : '#/accounts'} aria-label="V2Echo 通知助手"><Brand /></a>
        <span className="workspace-label">通知工作台</span>
        <div className="header-tools">
          <a className="guide-link" href="https://github.com/wizcheu/v2echo-notifier/blob/main/docs/user-guide.md" target="_blank" rel="noopener noreferrer">使用指南 <span aria-hidden="true">↗</span></a>
          <button className="icon-button logout-button" aria-label="退出管理页" title="退出管理页" disabled={loggingOut} onClick={() => {
            setLoggingOut(true); setError('')
            void onLogout().catch(() => setError('退出失败，请重试。')).finally(() => setLoggingOut(false))
          }}><Icon name="exit_line" /></button>
          <button className="menu-toggle icon-button" ref={menuTrigger} aria-expanded={menuOpen} aria-controls="main-navigation"
            aria-label={menuOpen ? '收起导航' : '打开导航'} onClick={() => setMenuOpen(!menuOpen)}><Icon name={menuOpen ? 'close' : 'menu'} /></button>
        </div>
        <div className="header-account"><AccountSwitcher onLogout={onLogout} accounts={accounts} accountID={accountID} page={route.kind === 'workspace' ? route.page : 'overview'} /></div>
      </div>
      <div className="navigation-container">
        <LayoutGroup id="main-navigation-selection">
          <nav id="main-navigation" aria-label="主导航">
            {accountID && navigation.map(key => <a key={key} href={accountHref(accountID, key)} className={`nav-item ${selected === key ? 'selected' : ''}`} aria-current={selected === key ? 'page' : undefined}>
              {selected === key && selection}<Icon name={pages[key].icon} /><span>{pages[key].title}</span>
            </a>)}
            <a className={`nav-item nav-accounts ${selected === 'accounts' ? 'selected' : ''}`} href="#/accounts" aria-current={selected === 'accounts' ? 'page' : undefined}>
              {selected === 'accounts' && selection}<Icon name="user_3" /><span>账号管理</span><span className="nav-count" aria-hidden="true">{accounts.length}</span>
            </a>
          </nav>
        </LayoutGroup>
      </div>
    </header>
    <main className="workspace" data-page={route.kind === 'workspace' ? route.page : route.kind} id="main-content" tabIndex={-1}>
      <div className="workspace-content">{error && <p className="notice error" role="alert">{error}</p>}{children}</div>
      <footer className="workspace-footer"><span><Icon name="lock" />凭据加密保存 · 数据留在你的服务器</span><BuildVersion /></footer>
    </main>
  </div>
}
