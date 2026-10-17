import type { ReactNode } from 'react'
import Icon from './Icon'
import Brand from './components/app/Brand'
import BuildVersion from './components/app/BuildVersion'

export default function LoginLayout({ children }: { children: ReactNode }) {
  return <main className="login-shell">
    <section className="login-story" aria-label="关于通知助手">
      <a className="login-brand" href="https://github.com/wizcheu/v2echo-notifier" target="_blank" rel="noopener noreferrer"><Brand /></a>
      <div className="login-intro">
        <span className="eyebrow">SELF-HOSTED. ALWAYS YOURS.</span>
        <h2>社区的回应，<br />在这里连接。</h2>
        <p>在自己的服务器同步 V2EX 通知，<br />通过已配对的 V2Echo App 接收提醒。</p>
        <div className="connection-illustration" aria-label="通知连接流程示意">
          <div><span className="flow-node">V</span><strong>V2EX</strong><small>读取通知</small></div><span className="flow-line" aria-hidden="true" />
          <div><span className="flow-node"><Icon name="lock" /></span><strong>你的服务器</strong><small>同步与加密</small></div><span className="flow-line" aria-hidden="true" />
          <div><span className="flow-node"><Icon name="notification" /></span><strong>V2Echo</strong><small>接收提醒</small></div>
        </div>
      </div>
      <div className="login-story-footer"><Icon name="lock" /><span>凭据与完整通知历史，保存在你的服务器。</span></div>
    </section>
    <section className="login-access" aria-label="连接管理页">
      <div className="login-access-inner"><span className="login-edition">V2Echo / 通知助手</span>{children}</div>
      <footer className="login-footer"><div><span>非官方 V2EX 通知助手</span><BuildVersion /></div><a href="https://github.com/wizcheu/v2echo-notifier" target="_blank" rel="noopener noreferrer">部署与使用文档 ↗</a></footer>
    </section>
  </main>
}
