import { useEffect, useId, useRef, useState } from 'react'
import type { KeyboardEvent } from 'react'
import type { Account } from './AccountManager'
import { accountHref } from './routes'
import type { Page } from './routes'
import Icon from './Icon'

function accountStatus(account: Account) {
  if (account.blocked || account.token_issue || account.cookie_issue || !account.cookie_configured)
    return '凭据需要处理'
  if (!account.verified) return '等待验证'
  return account.enabled ? '同步已启用' : '同步已暂停'
}

export default function AccountSwitcher({
  accounts,
  accountID,
  page,
  onLogout,
}: {
  accounts: Account[]
  accountID: string
  page: Page
  onLogout: () => Promise<void>
}) {
  const [logoutError, setLogoutError] = useState('')
  const [loggingOut, setLoggingOut] = useState(false)
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const menu = useRef<HTMLDivElement>(null)
  const menuID = useId()
  const current = accounts.find((account) => account.id === accountID)
  useEffect(() => setOpen(false), [accountID, page])
  useEffect(() => {
    if (!open) return
    menu.current?.querySelector<HTMLElement>('[aria-checked="true"]')?.focus({ preventScroll: true })
    const dismiss = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('pointerdown', dismiss)
    return () => document.removeEventListener('pointerdown', dismiss)
  }, [open])
  function close(restoreFocus = false) {
    setOpen(false)
    if (restoreFocus) trigger.current?.focus({ preventScroll: true })
  }
  function keyboard(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      close(true)
      return
    }
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
    const items = Array.from(
      menu.current?.querySelectorAll<HTMLElement>('[role^="menuitem"]:not([aria-disabled="true"])') ?? [],
    ).filter(item => item.getClientRects().length > 0)
    if (!items.length) return
    event.preventDefault()
    const index = items.indexOf(document.activeElement as HTMLElement)
    const next =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? items.length - 1
          : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length
    items[next].focus({ preventScroll: true })
  }
  if (!current) return null
  return (
    <div
      className="account-switcher"
      ref={root}
      onKeyDown={keyboard}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setOpen(false)
      }}
    >
      <button
        ref={trigger}
        className="account-trigger"
        aria-label={`切换当前账号，当前 @${current.username || '待验证账号'}`}
        aria-haspopup="menu"
        aria-controls={menuID}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        onKeyDown={(event) => {
          if (!open && ['ArrowDown', 'ArrowUp'].includes(event.key)) {
            event.preventDefault()
            event.stopPropagation()
            setOpen(true)
          }
        }}
      >
        <span className="avatar" aria-hidden="true">
          {current.username?.slice(0, 1).toUpperCase() || 'V'}
        </span>
        <span className="account-trigger-copy">
          <strong>{current.username ? `@${current.username}` : '待验证账号'}</strong>
          <small>当前账号</small>
        </span>
        <svg className="account-chevron" viewBox="0 0 20 20" aria-hidden="true">
          <path
            d="m6 8 4-4 4 4M6 12l4 4 4-4"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </button>
      {open && (
        <div id={menuID} className="account-menu" role="menu" aria-label="切换账号" ref={menu}>
          <div className="account-menu-heading" role="presentation">
            <span>切换账号</span>
            <small>{accounts.length} / 20</small>
          </div>
          <div className="account-menu-list" role="group" aria-label="已添加的账号">
            {accounts.map((account) => (
              <a
                key={account.id}
                role="menuitemradio"
                aria-checked={account.id === accountID}
                className="account-option"
                href={accountHref(account.id, page)}
                onClick={() => close(account.id === accountID)}
              >
                <span className="avatar" aria-hidden="true">
                  {account.username?.slice(0, 1).toUpperCase() || 'V'}
                </span>
                <span className="account-option-copy">
                  <strong>{account.username ? `@${account.username}` : '待验证账号'}</strong>
                  <small>{accountStatus(account)}</small>
                </span>
                {account.id === accountID && <Icon name="check_circle" />}
              </a>
            ))}
          </div>
          <div className="account-menu-actions" role="group" aria-label="账号操作">
            {accounts.length < 20 ? (
              <a role="menuitem" href="#/accounts/new" onClick={() => close()}>
                <span className="account-add-icon" aria-hidden="true">
                  +
                </span>
                添加账号
              </a>
            ) : (
              <span role="menuitem" aria-disabled="true">
                已达 20 个账号上限
              </span>
            )}
            <a role="menuitem" href="#/accounts" onClick={() => close()}>
              <Icon name="user_3" />
              管理账号
            </a>
            <button className="mobile-only logout" role="menuitem" disabled={loggingOut} onClick={() => {setLoggingOut(true); void onLogout().catch(() => setLogoutError('退出失败，请重试。')).finally(() => setLoggingOut(false))}}><Icon name="exit" />{loggingOut ? '正在退出…' : '退出管理页'}</button>
            {logoutError && <p role="alert">{logoutError}</p>}
          </div>
        </div>
      )}
    </div>
  )
}
