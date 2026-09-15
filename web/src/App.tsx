import { useCallback, useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import AccountWorkspace from './AccountWorkspace'
import AccountManager from './AccountManager'
import type { Account } from './AccountManager'
import SecretField from './SecretField'
import Shell from './Shell'
import LoginLayout from './LoginLayout'
import { api, APIError } from './api'
import { accountHref, navigate } from './routes'
import { useRoute } from './useRoute'

type ProxyMode = 'environment' | 'direct' | 'custom'
const emptyProxy = { proxy_mode: 'environment' as ProxyMode, proxy_url: '' }
export default function App() {
  const { route } = useRoute()
  const [accounts, setAccounts] = useState<Account[]>([])
  const [authenticated, setAuthenticated] = useState<boolean | null>(null)
  const [newCookie, setNewCookie] = useState('')
  const [newProxy, setNewProxy] = useState(emptyProxy)
  const [credential, setCredential] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [listError, setListError] = useState('')
  const listGeneration = useRef(0)
  const selected = route.kind === 'workspace' ? route.accountID : (accounts[0]?.id ?? '')
  const adding = route.kind === 'add'
  const sessionExpired = useCallback(() => {
    listGeneration.current++
    setAuthenticated(false)
    setAccounts([])
    setCredential('')
    setNewCookie('')
    setNewProxy(emptyProxy)
  }, [])
  const refreshAccounts = useCallback(async () => {
    const version = ++listGeneration.current
    try {
      const result = await api<{ accounts: Account[] }>('accounts')
      if (version !== listGeneration.current) return
      setAccounts(result.accounts)
      setAuthenticated(true)
      setListError('')
    } catch (e) {
      if (version !== listGeneration.current) return
      if (e instanceof APIError && e.status === 401) sessionExpired()
      else setListError('无法读取账号列表，请检查连接。')
    }
  }, [sessionExpired])
  useEffect(() => {
    void refreshAccounts()
    return () => {
      listGeneration.current++
    }
  }, [refreshAccounts])
  useEffect(() => {
    if (!authenticated) return
    const timer = window.setInterval(() => void refreshAccounts(), 5000)
    return () => window.clearInterval(timer)
  }, [authenticated, refreshAccounts])
  useEffect(() => {
    if (authenticated && route.kind === 'home')
      navigate(accounts.length ? accountHref(accounts[0].id) : '#/accounts/new', true)
  }, [authenticated, accounts, route.kind])
  useEffect(() => {
    setCredential('')
    setNewCookie('')
    setNewProxy(emptyProxy)
    setError('')
  }, [adding])
  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      if (!authenticated) {
        await api('login', 'POST', { token: credential })
        await refreshAccounts()
      } else {
        const result = await api<{ id: string }>('accounts', 'POST', {
          api_token: credential,
          cookie: newCookie,
          ...newProxy,
        })
        await refreshAccounts()
        navigate(accountHref(result.id))
        setNewCookie('')
        setNewProxy(emptyProxy)
      }
      setCredential('')
    } catch (e) {
      setError(e instanceof Error ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }
  async function logout() {
    await api('logout', 'POST', {})
    sessionExpired()
  }
  async function removeAccount(id: string) {
    try {
      await api(`accounts/${encodeURIComponent(id)}`, 'DELETE', {})
      await refreshAccounts()
    } catch (e) {
      if (e instanceof APIError && e.status === 401) sessionExpired()
      throw e
    }
  }
  if (authenticated === null)
    return (
      <LoginLayout>
        <div className="login-panel">
          {listError ? (
            <>
              <p className="notice error" role="alert">
                {listError}
              </p>
              <button onClick={() => void refreshAccounts()}>重新连接</button>
            </>
          ) : (
            <p role="status">正在连接通知助手…</p>
          )}
        </div>
      </LoginLayout>
    )
  const connectionForm = (
    <form className={authenticated ? 'add-account' : 'login-panel'} onSubmit={submit}>
      <header className={authenticated ? 'page-header' : undefined}>
        <div>
          <h1>{authenticated ? '添加 V2EX 账号' : '连接你的通知助手'}</h1>
          <p className={authenticated ? 'page-description' : 'muted'}>
            {authenticated
              ? '验证同账号的 API Token 与网页 Cookie，验证通过后保存。'
              : '输入服务器数据目录 admin-token 文件中的管理密钥。'}
          </p>
        </div>
        {authenticated && accounts.length > 0 && (
          <button
            className="text-button"
            type="button"
            disabled={busy}
            onClick={() => {
              navigate('#/accounts')
              setCredential('')
              setNewCookie('')
              setNewProxy({ proxy_mode: 'environment', proxy_url: '' })
              setError('')
            }}
          >
            返回账号管理
          </button>
        )}
      </header>
      {authenticated ? (
        <>
          <h2 className="add-connection-title">服务器连接</h2>
          <label>
            验证与同步的连接方式
            <select
              value={newProxy.proxy_mode}
              disabled={busy}
              onChange={(e) => setNewProxy({ proxy_mode: e.target.value as ProxyMode, proxy_url: '' })}
            >
              <option value="environment">使用环境变量代理</option>
              <option value="custom">自定义 HTTP / HTTPS 代理</option>
              <option value="direct">直接连接</option>
            </select>
          </label>
          {newProxy.proxy_mode === 'custom' && (
            <>
              <SecretField
                label="代理地址"
                value={newProxy.proxy_url}
                onChange={(value) => setNewProxy({ ...newProxy, proxy_url: value })}
                required
                disabled={busy}
                maxLength={4096}
                placeholder="http://代理主机:端口"
              />
              <p className="caption">
                填写 notifier 服务器可访问的代理地址；需要认证时使用
                http://用户名:密码@主机:端口，密码中的特殊字符需进行 URL 编码。
              </p>
            </>
          )}
          <p className="caption">
            可选环境变量、直连、自定义 HTTP / HTTPS 代理。自定义时显示代理地址输入框。
          </p>
          <SecretField
            label="V2EX API Token"
            value={credential}
            onChange={setCredential}
            required
            disabled={busy}
            placeholder="粘贴 Personal Access Token"
          />

          <SecretField
            label="同账号的网页 Cookie"
            value={newCookie}
            onChange={setNewCookie}
            required
            disabled={busy}
            maxLength={16384}
            placeholder="粘贴完整 Cookie 请求头，需包含 A2"
          />
          <p className="caption">
            两项均为必填，用户名需完全一致（区分大小写）。验证通过后，凭据加密保存在此服务器。
          </p>
        </>
      ) : (
        <SecretField
          label="管理密钥"
          value={credential}
          onChange={setCredential}
          required
          disabled={busy}
          placeholder="输入服务器管理密钥"
        />
      )}

      {(error || listError) && (
        <p className="notice error" role="alert">
          {error || listError}
        </p>
      )}
      <button className="primary" disabled={busy}>
        {busy ? (authenticated ? '正在验证账号…' : '处理中…') : authenticated ? '验证并保存账号' : '进入管理页'}
      </button>
      {authenticated ? <div className="add-footnotes"><p className="caption">验证连接立即生效；成功后与账号一起保存，之后可在网络代理中修改。</p><p className="caption">验证失败会显示具体原因，并保留已填内容。</p></div> : <p className="caption">一个管理密钥可管理这里的全部账号，<br />请只交给可信的管理员。</p>}
      {authenticated && (
        <aside className="add-help">
          <h2>准备凭据</h2>
          <h3>API Token</h3>
          <p className="caption">Scope 选择 Everything；建议有效期 180 天。</p><a className="text-link token-help-link" href="https://v2ex.com/settings/tokens" target="_blank" rel="noopener noreferrer">前往 V2EX 生成 ↗</a>
          <h3>网页 Cookie</h3>
          <p className="caption">
            从已登录 V2EX 的浏览器复制完整 Cookie 请求头，需包含 A2。两项凭据必须属于同一个账号。
          </p>
        </aside>
      )}
    </form>
  )
  if (!authenticated) return <LoginLayout>{connectionForm}</LoginLayout>
  const exists = accounts.some((a) => a.id === selected)
  return (
    <Shell accounts={accounts} accountID={exists ? selected : (accounts[0]?.id ?? '')} route={route} onLogout={logout}>
      {listError && (
        <p className="notice error" role="alert">
          {listError}
          <button onClick={() => void refreshAccounts()}>重试</button>
        </p>
      )}
      {adding ? (
        connectionForm
      ) : route.kind === 'accounts' ? (
        <AccountManager
          accounts={accounts}
          error=""
          onAdd={() => navigate('#/accounts/new')}
          onOpen={(id) =>
            navigate(
              accountHref(
                id,
                accounts.find((a) => a.id === id && (a.token_issue || a.cookie_issue || !a.cookie_configured))
                  ? 'credentials'
                  : 'settings',
              ),
            )
          }
          onRemove={removeAccount}
        />
      ) : route.kind === 'workspace' && exists ? (
        <AccountWorkspace
          key={selected}
          accountID={selected}
          tab={route.page}
          onExpired={sessionExpired}
          onRemove={async () => {
            await removeAccount(selected)
            navigate('#/accounts', true)
          }}
        />
      ) : route.kind === 'home' ? (
        <p role="status">正在打开工作台…</p>
      ) : (
        <section className="empty">
          <h1>{route.kind === 'workspace' ? '账号不存在或已移除' : '找不到这个页面'}</h1>
          <p>请从账号管理重新打开工作台。</p>
          <a className="button" href="#/accounts">
            前往账号管理
          </a>
        </section>
      )}
    </Shell>
  )
}
