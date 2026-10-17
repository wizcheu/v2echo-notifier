# 开发约定

- 本仓库面向公开发布。文档、界面文案、注释与配置应自包含，不引用其他项目的目录、架构、开发进度或内部实现。
- 依赖网络服务时，只记录使用本程序所需的公开接口、配置项与行为契约。
- 后端使用 Go + SQLite，管理页使用 React；正式构建将前端资源嵌入可执行文件。
- 用户凭据、通知历史和本地运行数据不提交到版本控制；示例与测试使用合成数据。
- Go 逻辑变更运行相关测试与 `go vet ./...`；前端变更运行 `npm run build`。先完成前端构建，再执行依赖嵌入资源的 Go 构建。

- 可选浏览器容器仅用于首页与 CF 人工验证；API 和推送保持 Go 通道。人工验证复用同一浏览器及账号代理，会话按账号加密保存；禁止通过通知列表验证或触发已读。浏览器服务失败不静默回退。浏览器桥接变更另运行 `python3 -m unittest discover -s browser -p 'test_*.py'`。

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
