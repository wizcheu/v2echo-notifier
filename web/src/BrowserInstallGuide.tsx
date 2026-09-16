const deploymentURL = 'https://github.com/wizcheu/v2echo-notifier/blob/main/docs/deployment.md#nas-浏览器验证'

export default function BrowserInstallGuide() {
  return <div>
    <p>此服务器尚未安装可选浏览器组件。如果实际首页检查被 Cloudflare 验证拦截，需要先安装该组件，再打开窗口完成人工验证。NAS 无需安装桌面系统。</p>
    <details>
      <summary>查看安装步骤</summary>
      <ol>
        <li>按照<a href={deploymentURL} target="_blank" rel="noreferrer">部署文档</a>中的完整配置，在原 <code>compose.yaml</code> 中加入 browser 服务与 notifier 的连接配置，使用配套的已发布镜像，无需下载额外配置文件。</li>
        <li>在同目录的 <code>.env</code> 中设置 <code>ECHO_BROWSER_TOKEN</code>，使用至少 32 字符的随机值。两个容器通过此密钥连接。</li>
        <li>在原部署目录执行以下命令，保留原 Compose 项目名称与数据卷：</li>
      </ol>
      <pre><code>{'docker compose pull\ndocker compose up -d'}</code></pre>
      <p>启动后刷新管理页，保存要使用的代理，再点击“打开验证窗口”。验证完成后点击“验证完成，重试首页”；待验证新账号还需完成配对并开启同步。</p>
      <p>浏览器组件需要持续运行，以便后续检查沿用已验证会话。管理页需通过 HTTPS 或本机 localhost 访问。站点直接拒绝的 403 不保证能通过人机验证解除。</p>
    </details>
  </div>
}
