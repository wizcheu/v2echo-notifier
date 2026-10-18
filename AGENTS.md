# 开发约定

- 本仓库面向公开发布。文档、界面文案、注释与配置应自包含，不引用其他项目的目录、架构、开发进度或内部实现。
- 依赖网络服务时，只记录使用本程序所需的公开接口、配置项与行为契约。
- 后端使用 Go + SQLite，管理页使用 React；正式构建将前端资源嵌入可执行文件。
- 用户凭据、通知历史和本地运行数据不提交到版本控制；示例与测试使用合成数据。
- 按实际风险选择最小验证。纯文档、普通注释 / 格式、无占位符变化的展示文案、既有样式数值、版本号，以及可确认无副作用 / 无动态引用的死代码删除或不影响执行顺序的声明整理，默认只检查差异与相关引用，不运行编译、`go vet`、前端 build 或整套测试，也无需为跳过编译再询问。`go:build` / `go:embed`、业务阈值、协议字段、持久化键、类型 / 依赖 / 结构变化不属于豁免项；按语义判断，不按改动行数。
- Go 逻辑变更运行相关测试，并按受影响包执行 `go vet`；跨包契约、依赖或构建配置变化时再扩大范围。前端逻辑变更优先相关测试，类型、组件结构、依赖或打包配置变化运行 `npm run build`；单纯文案 / 样式参数不触发 build。需要交付嵌入前端的可执行文件或镜像时，先完成前端构建，再执行 Go 构建。日常验证不自动构建 Docker 镜像或发布。
- 同一批修改完成后集中验证，复用与当前相关代码、配置和环境匹配的有效结果；通过后仅补文档、注释或低风险展示值不重跑。交付时简述实际检查与未编译原因，不把静态检查表述为编译 / 运行通过。

- 可选浏览器容器仅用于首页与 CF 人工验证；API 和推送保持 Go 通道。人工验证复用同一浏览器及账号代理，会话按账号加密保存；禁止通过通知列表验证或触发已读。浏览器服务失败不静默回退。浏览器桥接逻辑变更另运行 `python3 -m unittest discover -s browser -p 'test_*.py'`；仅文档、普通注释等低风险修改仍按上述豁免。

## Kokonut UI

The React frontend in `web/` installs components from Kokonut UI, a shadcn/ui registry.
The registry is set in `web/components.json` as `@kokonutui`. Run shadcn commands from `web/` and use that directory for MCP project queries.

- Before building an animated or interactive component from scratch, check
  https://kokonutui.com/llms.txt for every component and its one-line description.
- Install with the shadcn CLI, never by pasting code:
  `npx shadcn@latest add @kokonutui/<name>`.
  It writes `src/components/kokonutui/<name>.tsx` and installs its dependencies.
- Installed components are project code. Edit them in place rather than wrapping them.
- Any docs page is available as Markdown, with the full source, by adding `.md`
  to its URL, e.g. https://kokonutui.com/docs/buttons/particle-button.md.
- Use the shared neutral semantic palette and system color-scheme behavior when adding components;
  `web/src/index.css` maps Tailwind/shadcn roles to `web/src/style.css`.
  The workspace uses top navigation; do not reintroduce the old global sidebar or purple palette.
