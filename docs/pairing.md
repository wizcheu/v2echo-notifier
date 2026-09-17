# 配对接口

## 助手生成二维码

管理接口 `POST /api/accounts/{id}/pair/qr` 接收可选 `cookie`，要求当前账号已通过 API 验证，首次配对还会验证首页账号和未读数。返回 `code`、`payload`、`expires_at`；二维码在管理页本地生成，内容仅为 `v2echo://push-pair?code=V2N-…`，不包含助手地址、PAT、Cookie 或发送 Token。邀请码为随机 18 字节的 base64url 无填充字符串，有效期 10 分钟，生成失败重试或刷新页面后重新点击生成会复用尚未完成的有效请求。

助手先加密持久化邀请码、独立随机 32 字节发送 Token 和首次首页验证快照，再以发送 Token 作为 Bearer 调用固定推送服务的 `POST /v1/invitations`，提交 `code`、`source_account_id`、`source_username`，接收 `expires_at`。发送 Token 在正式绑定前只用于该邀请码的确认。

App 在注册后扫描二维码（也可粘贴配对内容），向固定推送服务提交设备配对请求。用户随后在助手点击「已扫码，完成绑定」，管理页调用 `POST /api/accounts/{id}/pair/qr/complete`，请求体为 `{"code":"V2N-…"}`。助手凭本地发送 Token 调用 `POST /v1/invitations/exchange`，同样仅提交 `code`；尚未扫码返回 `{"paired":false}`，成功返回 `paired`、`binding_id`、`username`。只有成功回执通过账号校验后才提交连接、Cookie、初始快照及队列变更，规则与手动配对一致；已完成的本地确认重试不再请求服务。

扫码流程不自动轮询：倒计时仅更新本地界面，确认和重试均由用户点击触发；App 在助手完成后通过「刷新状态」查看结果。所有网络请求均主动向外发起，助手无需公网 IP、端口映射或与手机处于同一网络。过期、账号不符、设备撤销或 App 重新生成配对码后，旧扫码请求不能完成绑定。

## App 生成配对码（保留）

管理接口 `POST /api/accounts/{id}/pair` 接收 `code` 和首次配对需要的 `cookie`（可复用设置中已保存的值）。Cookie 为单行 Cookie 请求头内容，必须含 A2，加密保存于当前账号配置，仅用于请求 `https://www.v2ex.com/`；不跟随跳转、不读取网页 `/notifications`，不以明文落盘或传给推送服务。首页顶栏账号须与 API 已验证账号匹配，同时须解析到有效未读总数；游客、访问挑战、账号不符或计数缺失均中止配对，缺失不当成 0。

发送凭据通过接收设备配对取得。服务基础地址固定为 `https://app.v2echo.com/api/push`，接口为 `POST /v1/pairings/exchange`，JSON 请求：

```json
{
  "code": "V2E-abcdefghijklmnopqrstuvwx",
  "sender_token": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
  "source_account_id": 7,
  "source_username": "tester"
}
```

以上均为合成示例。配对码由接收设备提供，格式为 `V2E-` 加 24 位 base64url 字符，有效期 10 分钟。助手生成随机 32 字节、base64url 无填充发送 Token，并在发起请求前加密持久化。请求不携带 V2EX PAT、V2EX Cookie 或管理会话 Cookie。

成功返回 HTTP 200：

```json
{"paired":true,"binding_id":"service-generated-binding-id","username":"tester"}
```

服务须校验配对码所对应的账号与 `source_username` 一致，将 `source_account_id` 固定为此发送凭据的来源账号；用户名匹配用于防误配，不构成外部账号身份认证。助手只使用管理页当前选择、经 V2EX API 验证的本地账号发起配对，并核对回执账号；配对请求、待确认凭据和结果只属于该账号配置。

同一码和同一发送 Token 在有效期内重试应返回同一成功结果，不创建第二次绑定，也不改变首次成功配对时间。其他发送 Token 不能兑换已消费的码。HTTP 403 表示码失效、已使用或账号不匹配；其他失败保持旧配置。

首次成功配对时，连接、加密 Cookie、首页快照和初始化状态在同一个 SQLite 事务保存；不立即生成汇总。后续定时检查将正数网页未读和 API 首条 ID 一起判断，首次或首条 ID 变化时创建 `unread_summary`。重新配对不清除已有比较基准；Cookie 留存用于定期首页检查，失效后需在设置中更新。

成功后自动配置事件上报连接。若替换已有连接，旧连接的 pending / blocked 事件在同一配置事务内结束，避免已提交或结果未知的旧通知转发给新接收者。重复确认同一配对不会终止新连接队列；其他账号的配置和队列不受影响。

接收端退出登录后撤销该连接，重新登录同一账号也需要新配对。普通逐条通知的产生时间须严格晚于本次配对，其余返回 `rejected` / `reason: before_binding`。网页汇总表示本轮观察到的未读总数，不适用普通通知的产生时间过滤；它可包含配对前仍未阅读的消息，但仍受已有首条 ID 基准约束。
