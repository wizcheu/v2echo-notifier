# 本地开发

[返回 README](../README.md) · [同步契约](synchronization.md)

## 构建与运行

需要 Go 1.26、Node.js 22.12+ 和 npm。

```sh
git clone https://github.com/wizcheu/v2echo-notifier.git
cd v2echo-notifier
make build
./bin/notifier
```

默认仅监听 `127.0.0.1:8282`。启动后读取本地管理密钥，再打开 `http://127.0.0.1:8282` 登录：

```sh
./bin/notifier -print-admin-token
```

开发 React 时，在另一个终端执行：

```sh
cd web
npm run dev
```

Vite 把 `/api` 代理到本地 Go 服务。React 构建产物通过 `go:embed` 打入正式可执行文件；修改前端后重新执行 `make build`。

## 验证

```sh
make test
```

测试使用合成响应和内存 HTTP transport，覆盖历史导入、分页重叠、增量翻页恢复、空历史、Token 失效、账号不匹配、额度退避、上报超时重放、pending 查询、配对重试、连接替换、多账号隔离、共享额度公平调度和配对前通知跳过、Cookie 身份检查、网页汇总原子提交、首条 ID 变化去重、删除与零未读、Cookie 加密及失败恢复。自动化测试不调用真实网络接口；实际账号与推送连接需要单独验证。

前端验证（Node.js 22.18+）：

```sh
cd web
npm test
npm run build
npm run test:browser
```

浏览器测试默认使用本机 Chrome，所有 API 均由合成数据替代，不访问真实账号或发送推送。

## 发布容器镜像

工作流为 [Publish Docker image](../.github/workflows/publish-image.yml)，自动发布到 `ghcr.io/<仓库所有者>/<仓库名>`，构建 `linux/amd64` 和 `linux/arm64`。使用 GitHub 自动提供的 `GITHUB_TOKEN`，无需填写 Docker Hub 账号、密码或额外的发布密钥。

将工作流提交并推送到默认分支后，可在 GitHub 的 **Actions → Publish Docker image → Run workflow** 选择构建分支及参数：

| 参数 | 用途 |
| --- | --- |
| 镜像标签 `image_tag` | 默认 `edge`；可填 `0.1.0` 等版本号，也可直接填 `latest` |
| 同时更新 latest `publish_latest` | 默认关闭；勾选后会在填写的标签之外发布 `latest` |

例如：填写 `0.1.0` 并勾选同时更新 latest，会发布 `:0.1.0`、`:latest` 和用于追溯提交的 `:sha-*`；沿用默认参数只发布 `:edge` 和 `:sha-*`。手动填写的镜像版本号不会自动创建 Git 标签或 GitHub Release。准备正式提供给用户的构建才更新 `latest`。

推送正式语义版本 Git 标签（例如 `v0.1.0`）也会自动发布 `:0.1.0` 和 `:latest`；预发布标签（例如 `v0.2.0-rc.1`）不会自动更新 `latest`。成功后的工作流摘要会列出实际发布的标签、拉取命令及镜像 digest。

首次发布的 GitHub Package 默认是 Private。在包的 **Package settings → Change visibility** 中改为 **Public**，用户才能匿名拉取；仓库公开不代表镜像包自动公开。

遇到 `manifest unknown` 时，先核对工作流摘要中的标签。例如只做过默认手动构建时，应使用 `docker pull ghcr.io/wizcheu/v2echo-notifier:edge`；需要 `latest` 则按上述参数重新发布。重跑旧工作流不会增加新的输入参数，需先推送更新后的工作流，再点击 Run workflow。

## Web 路由与草稿

工作台采用 Hash 路径，直接刷新或复制链接不需要反向代理配置页面回退。账号也包含在路径中，例如 `/#/accounts/<账号 ID>/overview`。

- 主要页面：`overview`、`check-history`、`settings`、`proxy`、`push-test`、`push-history`、`notifications`。
- 设置子页：`settings/credentials`、`settings/device`、`settings/sync`；运行详情：`overview/runtime`。
- 账号管理：`/#/accounts`；添加账号：`/#/accounts/new`。

浏览器前进、后退和刷新会恢复账号与页面。推送历史的搜索、状态、分页及详情页也保存在 Hash 查询参数中。登录后返回原链接；已移除账号的链接显示提示，不会自动打开另一账号的配置。

凭据与同步偏好分别保存，同一账号的工作台页面间切换会保留尚未保存的草稿；刷新、切换账号或进入账号管理会丢弃草稿。管理密钥、Token 和 Cookie 不写入 URL 或浏览器本地存储。

移动端使用顶部账号切换与底部「概览、检查记录、设置、推送」导航；凭据、接收设备和同步偏好从设置页进入。界面使用系统无衬线字体，不加载额外字体文件或外部字体服务。

## 接口与源码入口

管理接口的账号隔离、鉴权、配置读取和调度规则见 [同步契约](synchronization.md)，接收设备兑换流程见 [配对接口](pairing.md)，事件上传和回执格式见 [S2S 协议](s2s-protocol.md)。
