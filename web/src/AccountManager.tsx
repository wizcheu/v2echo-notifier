import { useState } from 'react'
import RemoveAccountDialog from './RemoveAccountDialog'

export type Account = {
  browser_required?: boolean
  id: string
  username: string
  member_id: number
  enabled: boolean
  verified: boolean
  blocked: boolean
  cookie_configured: boolean
  cookie_verified: boolean
  token_issue: string
  cookie_issue: string
  last_error: string
}

export function TokenHelp() {
  return (
    <p className="caption">
      前往{' '}
      <a href="https://v2ex.com/settings/tokens" target="_blank" rel="noopener noreferrer">
        V2EX 生成 API Token ↗
      </a>
      ，Scope 选择 <strong>Everything</strong>，有效期建议选长一些，例如 <strong>180 天</strong>。Token 与 Cookie
      必须属于同一个账号。
    </p>
  )
}

const needsAttention = (a: Account) => Boolean(a.browser_required || a.blocked || a.token_issue || a.cookie_issue || !a.cookie_configured)

export default function AccountManager({
  accounts,
  error,
  onAdd,
  onOpen,
  onRemove,
}: {
  accounts: Account[]
  error: string
  onAdd: () => void
  onOpen: (id: string) => void
  onRemove: (id: string) => Promise<void>
}) {
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState('')
  async function remove(id: string) {
    setBusy(true)
    setFailure('')
    try {
      await onRemove(id)
      setConfirm('')
    } catch (e) {
      setFailure(e instanceof Error ? e.message : '移除失败，请重试。')
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="account-manager">
      <header className="page-header">
        <div>
          <h1>账号管理</h1>
          <p className="page-description">已添加 {accounts.length} / 20 个账号。查看凭据状态，更新连接或移除旧账号。</p>
        </div>
        <div className="account-actions">
          <button className="primary" disabled={busy || accounts.length >= 20} onClick={onAdd}>
            添加账号
          </button>
        </div>
      </header>
      {(failure || error) && (
        <p className="notice error" role="alert">
          {failure || error}
        </p>
      )}
      <div className="content-surface">
      {!accounts.length && (
        <section className="panel">
          <h2>还没有账号</h2>
          <p className="muted">准备同账号的 API Token 和网页 Cookie，即可添加并开始验证。</p>
          <TokenHelp />
        </section>
      )}
      {[
        { title: '需要处理', items: accounts.filter(needsAttention) },
        { title: '其他账号', items: accounts.filter((a) => !needsAttention(a)) },
      ]
        .filter((g) => g.items.length)
        .map((group) => (
          <section className="account-group" key={group.title}>
            <h2>
              {group.title === '其他账号' && !accounts.some(needsAttention) ? '全部账号' : group.title}
              <small> {group.items.length}</small>
            </h2>
            {group.title === '其他账号' && <div className="account-columns" aria-hidden="true"><span>账号</span><span>凭据状态</span><span>同步</span><span className="account-operation-label">操作</span></div>}
            <div className="account-list">
              {group.items.map((account) => (
                <section className={`account-row ${needsAttention(account) ? 'needs-attention' : ''}`} key={account.id}>
                  <div className="section-heading">
                    <h2>{account.username ? `@${account.username}` : `待验证账号 · ${account.id.slice(0, 6)}`}</h2>
                    <span
                      className={`status-pill ${needsAttention(account) ? 'warning' : !account.enabled ? 'neutral' : 'accepted'}`}
                    >
                      {needsAttention(account) ? '需要处理' : account.enabled ? '已启用' : '已暂停'}
                    </span>
                  </div>
                  {account.verified && account.cookie_verified && !needsAttention(account) ? <p className="account-credential-summary">Token、Cookie 均已验证</p> : <dl className="details">
                    <div>
                      <dt>API Token</dt>
                      <dd>{account.token_issue ? '需要更新' : account.verified ? '已验证' : '等待验证'}</dd>
                    </div>
                    <div>
                      <dt>网页 Cookie</dt>
                      <dd>
                        {!account.cookie_configured
                          ? '需要补充'
                          : account.cookie_issue
                            ? '需要更新'
                            : account.cookie_verified
                              ? '已验证'
                              : '等待验证'}
                      </dd>
                    </div>
                  </dl>}
                  {account.token_issue && <p className="notice error">{account.token_issue}</p>}
                  {account.cookie_issue && <p className="notice error">{account.cookie_issue}</p>}
                  {account.last_error &&
                    account.last_error !== account.token_issue &&
                    account.last_error !== account.cookie_issue && <p className="caption">{account.last_error}</p>}
                  <div className="account-actions">
                    <button className={needsAttention(account) ? '' : 'text-button'} disabled={busy} onClick={() => onOpen(account.id)}>{account.browser_required ? '处理访问验证' : needsAttention(account) ? '更新凭据' : '连接设置'}</button>
                    <button className="danger-button" disabled={busy} onClick={() => {setFailure('');setConfirm(account.id)}}>移除账号</button>
                  </div>
                </section>
              ))}
            </div>
          </section>
        ))}
      </div>
      {confirm && <RemoveAccountDialog username={accounts.find(a => a.id === confirm)?.username || ''} busy={busy} error={failure} onCancel={() => setConfirm('')} onConfirm={() => void remove(confirm)} />}
      <p className="caption">
        凭据状态在后台检查后更新。暂停同步时也会暂停凭据检查；网络故障或访问挑战不会直接判定为过期。
      </p>
    </div>
  )
}
