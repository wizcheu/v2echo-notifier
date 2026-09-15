import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import type { Account } from './AccountManager'
import { accountHref, activePage, mainPages, pages } from './routes'
import type { Route } from './routes'
import Icon from './Icon'
import AccountSwitcher from './AccountSwitcher'

export default function Shell({
  accounts,
  accountID,
  route,
  children,
  onLogout,
}: {
  accounts: Account[]
  accountID: string
  route: Route
  children: ReactNode
  onLogout: () => Promise<void>
}) {
  const [menuOpen, setMenuOpen] = useState(false)
  const [loggingOut, setLoggingOut] = useState(false)
  const [error, setError] = useState('')
  const current = accounts.find((a) => a.id === accountID)
  const selected = route.kind === 'workspace' ? activePage(route.page) : 'accounts'
  const routeKey = route.kind === 'workspace' ? `${route.accountID}/${route.page}` : route.kind
  useEffect(() => {
    setMenuOpen(false)
    document.getElementById('main-content')?.focus({ preventScroll: true })
    window.scrollTo(0, 0)
    document.title = `${route.kind === 'workspace' ? pages[route.page].title : route.kind === 'add' ? '添加账号' : '账号管理'} · V2Echo`
  }, [routeKey])
  return (
    <div className="app-shell">
      <button className="skip-link" onClick={() => document.getElementById('main-content')?.focus()}>
        跳到主要内容
      </button>
      <aside className={`sidebar ${menuOpen ? 'menu-open' : ''}`}>
        <div className="sidebar-brand">
          <a className="wordmark" href={accountID ? accountHref(accountID) : '#/accounts'}>
            V2Echo<span>自托管通知助手</span>
          </a>
          <button
            className="menu-toggle"
            aria-expanded={menuOpen}
            aria-controls="main-navigation"
            onClick={() => setMenuOpen(!menuOpen)}
          >
            <Icon name={menuOpen ? 'close' : 'menu'} />
            {menuOpen ? '收起' : '导航'}
          </button>
        </div>
        <nav id="main-navigation" aria-label="主导航">
          {accountID &&
            mainPages.map((key) => (
              <a
                key={key}
                href={accountHref(accountID, key)}
                className={`nav-item ${selected === key ? 'selected' : ''}`}
                aria-current={selected === key ? 'page' : undefined}
              >
                <Icon name={pages[key].icon} />
                <span>{pages[key].title}</span>
              </a>
            ))}
          <a
            className={`nav-item accounts-nav ${selected === 'accounts' ? 'selected' : ''}`}
            href="#/accounts"
            aria-current={selected === 'accounts' ? 'page' : undefined}
          >
            <Icon name="user_3" />
            <span>账号管理</span>
          </a>
        </nav>
        <div className="sidebar-bottom">
          <AccountSwitcher
            onLogout={onLogout}
            accounts={accounts}
            accountID={accountID}
            page={route.kind === 'workspace' ? route.page : 'overview'}
          />
          <p className="privacy-caption">凭据加密保存在此服务器</p>
          <button
            className="nav-item logout"
            disabled={loggingOut}
            onClick={() => {
              setLoggingOut(true)
              setError('')
              void onLogout()
                .catch(() => setError('退出失败，请重试。'))
                .finally(() => setLoggingOut(false))
            }}
          >
            <Icon name="exit" />
            {loggingOut ? '正在退出…' : '退出管理页'}
          </button>
          {error && (
            <p className="notice error" role="alert">
              {error}
            </p>
          )}
        </div>
      </aside>
      {accountID && <nav className="mobile-tabs" aria-label="移动主导航">
        {accountID && (["overview", "checkHistory", "settings", "pushHistory"] as const).map((key) => <a key={key} href={accountHref(accountID, key)} aria-current={(selected === "proxy" ? "settings" : selected === "pushTest" ? "pushHistory" : selected) === key ? "page" : undefined}><Icon name={pages[key].icon} /><span>{{overview:"概览", checkHistory:"检查记录", settings:"设置", pushHistory:"推送"}[key]}</span></a>)}
      </nav>}
      <main className="workspace" data-page={route.kind === 'workspace' ? route.page : route.kind} id="main-content" tabIndex={-1}>
        <div className="breadcrumb">
          工作台 <span>/</span>{' '}
          {current && route.kind === 'workspace' ? `@${current.username || '待验证账号'}` : '账号管理'}
        </div>
        {children}
      </main>
    </div>
  )
}
