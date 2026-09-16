# V2Echo 通知助手

V2Echo 的自托管通知助手，在自己的服务器或 NAS 上检查 V2EX 未读消息，并向已配对的 App 推送提醒。提供 Web 管理界面，基于 React、Go 和 SQLite，所有通知检查与上报均主动向外发起，无需公网入站地址。

## 功能

- 最多管理 20 个账号，凭据加密保存在自己的服务器。
- 结合网页未读数与 API 首条消息 ID 判断提醒，不执行已读操作。
- 自定义推送时段；默认 UTC+8 的 08:00–24:00 运行，休息期间暂停检查与推送。
- 提供网络代理、推送测试、最近 50 条检查记录与推送历史。
- 默认只安装 notifier；实际首页需要 CF 验证时，按引导加装独立 browser 容器，详见 [NAS 浏览器验证](docs/deployment.md#nas-浏览器验证)。

## 快速开始

使用已公开发布的 GHCR 镜像，支持 amd64 和 arm64。无需下载源码，在部署目录创建 `compose.yaml`：

```yaml
services:
  notifier:
    image: ghcr.io/wizcheu/v2echo-notifier:latest
    restart: unless-stopped
    ports:
      - "8282:8282"
    volumes:
      - notifier-data:/data
    read_only: true
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL

volumes:
  notifier-data:
```

```sh
docker compose pull
docker compose up -d
docker compose exec notifier /notifier -data /data -print-admin-token
```

打开 `http://<宿主机地址>:8282`，用命令输出的管理密钥登录，然后：

1. 添加账号，填写同一个 V2EX 账号的 API Token 和网页 Cookie（需包含 A2）。
2. 在 V2Echo App 中生成配对码，到「连接与设置 → 接收设备」完成配对。
3. 在「同步偏好」确认推送时段，再到「推送测试」检查接收情况。

`latest` 用于正式版本，`edge` 用于测试。固定版本、更新迁移、代理和数据卷说明见[部署与更新](docs/deployment.md)。

管理界面请在可信网络或 HTTPS 反向代理后使用。备份需保留整个数据卷；“APNs 已接收”表示推送服务取得接受回执，不代表设备已经显示通知。

## 按需安装 browser

默认只安装 notifier。若实际首页检查遇到 Cloudflare 人机验证，管理页会提示加装可选 browser 容器；NAS 无需安装浏览器或桌面系统。API 身份已确认的新账号可先保存为暂停同步的待验证账号，安装后继续处理。

两个组件使用独立镜像，按需拉取即可：

| 组件 | 镜像 | 用途 |
| --- | --- | --- |
| notifier | `ghcr.io/wizcheu/v2echo-notifier` | 管理页、通知检查与推送，默认安装 |
| browser | `ghcr.io/wizcheu/v2echo-notifier-browser` | 人工验证与后续浏览器首页读取，按需安装 |

无需下载源码或额外的 Compose 文件。将原 `compose.yaml` 按下面的完整示例合并，添加 browser 服务以及 notifier 的连接配置；若已自定义端口、卷映射或项目名称，沿用原设置。两个镜像使用配套的已发布版本。

```yaml
services:
  notifier:
    image: ghcr.io/wizcheu/v2echo-notifier:latest
    restart: unless-stopped
    ports:
    - 8282:8282
    volumes:
    - notifier-data:/data
    read_only: true
    security_opt:
    - no-new-privileges:true
    cap_drop:
    - ALL
    environment:
      ECHO_BROWSER_CONTROL_URL: http://browser:8090
      ECHO_BROWSER_DESKTOP_URL: http://browser:3000
      ECHO_BROWSER_TOKEN: ${ECHO_BROWSER_TOKEN:?Set a random shared browser token
        of at least 32 characters in .env}
    depends_on:
    - browser
  browser:
    image: ${ECHO_BROWSER_IMAGE:-ghcr.io/wizcheu/v2echo-notifier-browser:latest}
    restart: unless-stopped
    shm_size: 1gb
    environment:
      PUID: '1000'
      PGID: '1000'
      TZ: Asia/Shanghai
      ECHO_BROWSER_TOKEN: ${ECHO_BROWSER_TOKEN:?Set ECHO_BROWSER_TOKEN in .env}
      SUBFOLDER: /browser-desktop/
      SELKIES_AUDIO_ENABLED: false|locked
      SELKIES_MICROPHONE_ENABLED: false|locked
      SELKIES_GAMEPAD_ENABLED: false|locked
      SELKIES_CLIPBOARD_ENABLED: false|locked
      SELKIES_COMMAND_ENABLED: false|locked
      SELKIES_FILE_TRANSFERS: none|locked
      SELKIES_ENABLE_SHARING: false|locked
      SELKIES_UI_SIDEBAR_SHOW_APPS: false|locked
      SELKIES_UI_SIDEBAR_SHOW_FILES: false|locked
      SELKIES_UI_SIDEBAR_SHOW_SHARING: false|locked
    tmpfs:
    - /config:mode=0700,uid=1000,gid=1000
    - /tmp
volumes:
  notifier-data: null
```

在原部署目录执行 `openssl rand -hex 32`，将生成的随机值写入同目录 `.env` 中的 `ECHO_BROWSER_TOKEN=随机值`（至少 32 字符）。两个容器共用此值。browser 默认使用 `:latest`；需要固定版本时，在 `.env` 中设置 `ECHO_BROWSER_IMAGE=ghcr.io/wizcheu/v2echo-notifier-browser:已发布标签`。

然后直接拉取镜像并启动：

```sh
docker compose pull
docker compose up -d
```

Compose 会从 GHCR 拉取 browser，启动它，并更新 notifier 的连接配置。保持原部署目录、Compose 项目名称和数据卷，账号与历史会保留。刷新管理页后点击“打开验证窗口”，完成人工验证，再点击“验证完成，重试首页”。管理页须通过 HTTPS 或本机 localhost 访问。

browser 启用后需持续运行，后续首页检查会沿用浏览器会话。代理连接测试不复用该会话，因此验证后测试仍可能返回 403，应以实际检查记录为准。403 也可能是站点直接拒绝，不保证都能通过人机验证解除。完整配置见 [NAS 浏览器验证](docs/deployment.md#nas-浏览器验证)。

## 文档

| 文档 | 内容 |
| --- | --- |
| [部署与更新](docs/deployment.md) | 镜像安装、版本更新、代理、管理密钥、数据保存 |
| [使用指南](docs/user-guide.md) | 账号与凭据、设备配对、网络代理、推送时段、测试与记录 |
| [本地开发](docs/development.md) | 开发环境、编译运行、测试、GitHub Actions 镜像发布 |
| [同步契约](docs/synchronization.md) | 未读判断、去重、额度、调度与本地管理接口 |
| [配对接口](docs/pairing.md) | 接收设备配对与发送凭据兑换 |
| [S2S 协议](docs/s2s-protocol.md) | 事件上报、回执、有效期与预算 |
