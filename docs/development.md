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

Vite 把 `/api` 与 `/browser-desktop/`（含 WebSocket）代理到本地 Go 服务。React 构建产物通过 `go:embed` 打入正式可执行文件；需要让可执行文件包含最新前端时重新执行 `make build`，日常修改的验证范围按[验证](#验证)一节选择。

## UI 组件

管理页已配置 Tailwind CSS 4、shadcn/ui 与 Kokonut UI；注册表和路径别名定义在 `web/components.json`。在 `web/` 目录安装组件：

```sh
npx shadcn@latest add @kokonutui/<name>
```

组件清单见 [Kokonut UI](https://kokonutui.com/llms.txt)。组件安装到 `web/src/components/kokonutui/`，共用类名合并函数为 `web/src/lib/utils.ts`；安装后的源码可直接修改。

管理页以黑白中性色、细边框和轻量动效建立统一层级。全局使用顶部导航；手机通过同一导航的展开菜单进入全部功能，不再维护一套底部导航。概览以未读快照、通知收件箱为主，运行与连接状态置于侧列；凭据、设备、同步偏好共用独立设置导航。文案和状态始终来自真实数据，不使用示例统计或把 APNs 已接收描述成设备已送达。

样式入口为 `web/src/index.css`，不加载 Tailwind 全局重置。`style.css` 定义语义颜色、字体与基础控件；`styles/shell.css` 管理导航和登录，`workspace.css` 管理表单与账号，`records.css` 管理列表与详情，`dashboard.css` 管理概览，`responsive.css` 管理响应布局。样式放在 `components` 层，显式工具类可覆盖默认值。`dark:` 跟随系统外观；`--muted` 表示文字色、`--accent` 表示主要操作色，与 shadcn 的同名表面角色不同，使用已映射的 `text-muted-foreground`、`bg-muted`、`bg-accent` 等工具类。

通过 CLI 安装的 Kokonut `particle-button` 与 `loader` 已适配语义令牌、中文和系统减少动态效果设置。提交按钮使用受控 `status`，跟随请求状态，成功与失败由现有业务消息展示；不把普通点击当作成功。粒子默认关闭。设置与筛选共用 `SegmentedNavigation`，选中指示器由 Motion 过渡；减少动态效果时立即切换。

`Dashboard.tsx` 负责概览呈现，`SettingsLayout.tsx` 负责设置区结构，`NotificationList.tsx` 共用通知列表。账号请求、独立草稿、配对、队列和业务门控仍集中在原工作区；工作区按需加载，登录页不会提前载入配对二维码等模块。

Codex 可通过 `npx shadcn@latest mcp init --client codex` 配置 shadcn MCP，并按命令提示更新本机配置后重启。MCP 项目查询使用 `web/` 的绝对路径。

## 验证

按 [AGENTS.md](../AGENTS.md) 的风险分级执行，以下是按需命令，不是每次修改的必跑清单。纯文档、普通注释 / 格式、展示文案和既有样式参数默认只检查差异；无行为影响的声明整理与已确认无引用 / 副作用的死代码删除同样可跳过编译。业务逻辑运行相关测试，Go 按受影响包执行 `go vet`；前端类型 / 组件结构 / 依赖 / 打包配置变化再运行 build。跨包或集成变化才扩大验证范围，已有有效结果不重复执行。

后端完整测试入口：

```sh
make test
```

测试使用合成响应和内存 HTTP transport，覆盖历史导入、分页重叠、增量翻页恢复、空历史、Token 失效、账号不匹配、额度退避、上报超时重放、pending 查询、配对重试、连接替换、多账号隔离、共享额度公平调度和配对前通知跳过、Cookie 身份检查、网页汇总原子提交、首条 ID 变化去重、删除与零未读、Cookie 加密及失败恢复。自动化测试不调用真实网络接口；实际账号与推送连接需要单独验证。

前端验证入口（Node.js 22.18+，按变更选择相关测试、构建或浏览器验收，不整段执行）：

```sh
cd web
npm test
npm run build
npm run test:browser
```

浏览器测试默认使用本机 Chrome，所有 API 均由 `web/tests/fixture.ts` 的合成数据替代，不访问真实账号或发送推送。`layout.spec.ts` 覆盖 320、390、820、1440px 的浅深色页面、菜单、提交中与失败、登录、空态、首次连接和移除确认；390px / 1440px 的页面截图输出到 `web/test-results/`，便于逐页视觉复核。截图是合成数据的本地浏览器验收，不代表真实设备或推送服务联调。

## 发布容器镜像

工作流为 [Publish Docker image](../.github/workflows/publish-image.yml)，notifier 发布到 `ghcr.io/<仓库所有者>/<仓库名>`，browser 发布到独立的 `ghcr.io/<仓库所有者>/<仓库名>-browser`，构建 `linux/amd64` 和 `linux/arm64`。使用 GitHub 自动提供的 `GITHUB_TOKEN`，无需填写 Docker Hub 账号、密码或额外的发布密钥。

将工作流提交并推送到默认分支后，可在 GitHub 的 **Actions → Publish Docker image → Run workflow** 选择构建分支及参数：

| 参数 | 用途 |
| --- | --- |
| 构建组件 `component` | 默认 `notifier`；选择 `browser` 只构建浏览器镜像，选择 `all` 构建两者 |
| 镜像版本号 `image_tag` | 默认 `edge`；可填 `0.1.0` 等版本号，也可直接填 `latest`；同时作为镜像标签和网页显示版本 |
| 同时更新 latest `publish_latest` | 默认关闭；勾选后会在填写的标签之外发布 `latest` |

例如：填写 `0.1.0` 并勾选同时更新 latest，会发布 `:0.1.0`、`:latest` 和用于追溯提交的 `:sha-*`；沿用默认参数只发布 `:edge` 和 `:sha-*`。手动填写的镜像版本号不会自动创建 Git 标签或 GitHub Release。准备正式提供给用户的构建才更新 `latest`。

登录页与登录后的页面底部均显示构建版本和短提交号，例如 `版本 0.1.0 · abc1234`。版本号在构建时写入网页资源及镜像的 `org.opencontainers.image.version` 标签；即使通过 `latest` 拉取，网页仍显示本次填写的 `0.1.0`。Git 标签触发时取去掉开头 `v` 的版本号；例如 `v0.2.0-rc.1` 显示为 `0.2.0-rc.1`。`edge` / `latest` 构建可用提交号区分，工作流摘要也列出完整提交号。

本地 `npm run dev` / `npm run build` 默认显示 `web/package.json` 的版本加 `-dev`，不自动推断已发布版本。可通过 `VITE_APP_VERSION=0.1.0 VITE_APP_REVISION=<提交号> npm run build` 显式指定；这些值属于公开构建信息。Docker 对应参数为 `APP_VERSION` / `APP_REVISION`，详见[源码构建](deployment.md#从源码构建)。修改容器的运行时环境变量不会改变已编译的网页版本。

只发布 browser 时，选择 `component=browser`、`image_tag=edge`，保持 `publish_latest` 关闭。本仓库会生成 `ghcr.io/wizcheu/v2echo-notifier-browser:edge`，不会构建或更新 notifier；部署时通过 `ECHO_BROWSER_IMAGE` 选择该标签。首次配套发布可选择 `component=all`，同时构建两个镜像。Fork 仓库时，镜像地址随仓库所有者和仓库名变化。

推送正式语义版本 Git 标签（例如 `v0.1.0`）会同时构建 notifier 和 browser，并各自自动发布 `:0.1.0` 和 `:latest`；预发布标签（例如 `v0.2.0-rc.1`）不会自动更新 `latest`。成功后的工作流摘要会列出实际发布的标签、拉取命令及镜像 digest。

两个镜像分别拥有自己的 GitHub Package。首次发布的 GitHub Package 默认是 Private。在包的 **Package settings → Change visibility** 中改为 **Public**，用户才能匿名拉取；仓库公开不代表镜像包自动公开。

遇到 `manifest unknown` 时，先核对工作流摘要中的标签。例如只做过默认手动构建时，应使用 `docker pull ghcr.io/wizcheu/v2echo-notifier:edge`；需要 `latest` 则按上述参数重新发布。重跑旧工作流不会增加新的输入参数，需先推送更新后的工作流，再点击 Run workflow。

## Web 路由与草稿

工作台采用 Hash 路径，直接刷新或复制链接不需要反向代理配置页面回退。账号也包含在路径中，例如 `/#/accounts/<账号 ID>/overview`。

- 主要页面：`overview`、`check-history`、`settings`、`proxy`、`push-test`、`push-history`、`notifications`。
- 设置子页：`settings/credentials`、`settings/device`、`settings/sync`；运行详情：`overview/runtime`。
- 账号管理：`/#/accounts`；添加账号：`/#/accounts/new`。

浏览器前进、后退和刷新会恢复账号与页面。推送历史的搜索、状态、分页及当前详情保存在 Hash 查询参数中；详情以模态弹窗展示，可刷新或复制链接继续查看，关闭后保留列表位置与筛选。登录后返回原链接；已移除账号的链接显示提示，不会自动打开另一账号的配置。

凭据与同步偏好分别保存，同一账号的工作台页面间切换会保留尚未保存的草稿；刷新、切换账号或进入账号管理会丢弃草稿。管理密钥、Token 和 Cookie 不写入 URL 或浏览器本地存储。

移动端使用顶部账号切换与底部「概览、检查记录、设置、推送」导航；凭据、接收设备和同步偏好从设置页进入。界面使用系统无衬线字体，不加载额外字体文件或外部字体服务。

## 接口与源码入口

管理接口的账号隔离、鉴权、配置读取和调度规则见 [同步契约](synchronization.md)，接收设备兑换流程见 [配对接口](pairing.md)，事件上传和回执格式见 [S2S 协议](s2s-protocol.md)。

## 浏览器兼容模式测试

```sh
python3 -m unittest discover -s browser -p 'test_*.py'
```

Go 测试覆盖验证恢复、账号与管理会话隔离、失败不回退、代理变更清理、凭据加密、待验证新账号及推送基准不变；Python 测试覆盖首页导航边界、Cookie 域、认证 HTTPS 代理拒绝时不直连，以及避免读取上一次页面。容器启动、Selkies 画面与实际 Cloudflare 人工验证需在有 Docker 的环境另行验收，不能由这些合成测试推断。
