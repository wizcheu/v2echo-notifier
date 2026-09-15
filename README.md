# V2Echo 通知助手

V2Echo 的自托管通知助手，在自己的服务器或 NAS 上检查 V2EX 未读消息，并向已配对的 App 推送提醒。提供 Web 管理界面，基于 React、Go 和 SQLite，所有通知检查与上报均主动向外发起，无需公网入站地址。

## 功能

- 最多管理 20 个账号，凭据加密保存在自己的服务器。
- 结合网页未读数与 API 首条消息 ID 判断提醒，不执行已读操作。
- 自定义推送时段；默认 UTC+8 的 08:00–24:00 运行，休息期间暂停检查与推送。
- 提供网络代理、推送测试、最近 50 条检查记录与推送历史。

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

首次部署需已有公开镜像；如果尚不可拉取，可使用[源码构建](docs/deployment.md#从源码构建)。`latest` 用于正式版本，`edge` 用于测试；固定版本、更新迁移、代理和数据卷说明见[部署与更新](docs/deployment.md)。

管理界面请在可信网络或 HTTPS 反向代理后使用。备份需保留整个数据卷；“APNs 已接收”表示推送服务取得接受回执，不代表设备已经显示通知。

## 文档

| 文档 | 内容 |
| --- | --- |
| [部署与更新](docs/deployment.md) | 镜像与源码部署、版本更新、构建代理、管理密钥、数据保存 |
| [使用指南](docs/user-guide.md) | 账号与凭据、设备配对、网络代理、推送时段、测试与记录 |
| [本地开发](docs/development.md) | 开发环境、编译运行、测试、Web 路由 |
| [同步契约](docs/synchronization.md) | 未读判断、去重、额度、调度与本地管理接口 |
| [配对接口](docs/pairing.md) | 接收设备配对与发送凭据兑换 |
| [S2S 协议](docs/s2s-protocol.md) | 事件上报、回执、有效期与预算 |
