# 配对接口

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
