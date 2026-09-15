import type { ReactNode } from 'react'
import Icon from './Icon'

export default function LoginLayout({ children }: { children: ReactNode }) {
  return (
    <main className="login-shell">
      <header className="login-brand wordmark">
        V2Echo<span>自托管通知助手</span>
      </header>
      <div className="login-body">
        <section className="login-intro" aria-label="关于通知助手">
          <h2>
            让社区的回应，
            <br />
            及时回到你身边。
          </h2>
          <p>
            在自己的服务器上同步 V2EX 通知，
            <br />
            通过已配对的 App 接收提醒。
          </p>
          <div className="login-privacy">
            <Icon name="lock" />
            <span>凭据与通知历史保存在你的服务器</span>
          </div>
        </section>
        {children}
      </div>
      <footer className="login-footer">V2Echo · 非官方 V2EX 通知助手</footer>
    </main>
  )
}
