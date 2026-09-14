# V2Echo 通知助手 S2S v1

本文定义通知助手的 HTTPS 批量上报、回执和限额契约。所有连接均由助手主动建立，无需公网入站地址或 Webhook。

## 鉴权与目标

基础地址固定为 `https://app.v2echo.com/api/push`，发送 Token 由配对取得。请求用 `Authorization: Bearer <发送 Token>` 鉴权，只能操作该凭据绑定的设备与账号。`source_account_id` 是配对时固定的来源信息，不用于选择收件人或证明账号所有权。请求不携带 V2EX PAT、密码或 Cookie。

## 批量同步

```http
POST /v1/sync
Authorization: Bearer <发送 Token>
Content-Type: application/json
```

```json
{
  "events": [{
    "event_id": "稳定的账号与通知ID摘要，64位小写十六进制",
    "type": "notification",
    "source_account_id": 7,
    "notification_id": 123,
    "title": "example 在某主题里回复了你",
    "body": "回复摘要",
    "created_at": "2026-09-13T08:00:00Z",
    "expires_at": "2026-09-14T08:00:00Z"
  }],
  "receipts": ["已经收到 pending 的另一事件ID，64位小写十六进制"]
}
```

两个数组必须存在，合计至少一项；每轮最多 4 个上报事件、16 个待查回执，整个 JSON 请求不超过 16 KiB。两个数组中的 ID 不可重复。事件标题不超过 220 个 Unicode 字符，正文不超过 500 个。批量规则或事件格式错误会拒绝整批，客户端应只发送自身生成、符合契约的事件。

助手持久化每条事件的稳定 ID 和原始内容；重试不改变事件。服务按设备、绑定与事件 ID 去重，相同 ID 不同内容返回 409。重复上报计入同步轮数，但已存在事件不重复扣除新事件额度。预算先于事件存储预留，存储失败后的重试可能保守地多占一份预算；预留额度不退款，以免失败请求绕过限制。

## 回执与重试

HTTP 200 返回这一轮的全部回执，不等待 APNs 完成：

```json
{
  "retry_after_seconds": 180,
  "receipts": [
    {"event_id":"本轮上报ID","status":"pending","retry_after_seconds":180},
    {"event_id":"本轮查询ID","status":"apns_accepted","apns_id":"APNs请求标识"}
  ]
}
```

`pending` 表示事件已持久化；后续把该 ID 放进 `receipts`，不再放进 `events`。回执顺序与请求 ID 顺序一致，客户端仍按 ID 校验；缺少、重复、未知状态或不匹配的回执使整轮结果视为未知，不误记成功。

| 状态 | 含义与助手行为 |
| --- | --- |
| `pending` | 已持久化，按该回执的退避时间继续查询 |
| `apns_accepted` | APNs 已接受请求，停止重试；不证明设备送达、弹出或用户阅读 |
| `rejected` | 永久拒绝，保留失败记录，停止重试 |
| `expired` | 事件过期，停止重试 |
| `not_found` | 当前绑定下没有该回执；已经提交的事件继续查询，不重新生成或发送 |

`rejected` 且 `reason: before_binding` 表示普通通知产生于配对之前或同一秒，助手显示“已跳过”。普通事件的产生时间须严格晚于当前配对时间，阻止退出期间的通知在重登后补推。

顶层 `retry_after_seconds` 是整轮最短间隔；单条 pending 的间隔还会考虑服务端下一次投递与租约时间。某个回执退避一小时不会阻塞其他新事件在下一轮提交。

助手 HTTP 总超时为 17 秒。网络错误、5xx、无效回执按整条连接持久化指数退避（至少 3 分钟，最长 1 小时，加 0～15 秒随机延迟）；HTTP `Retry-After` 可以要求更长等待，最多接受 48 小时。429 暂停该连接的全部上报和查询，而非仅暂停本批事件。401 / 403 停止该连接自动上报，直到更新凭据或重新配对。其他 4xx 将本批记录为拒绝。

原 `/v1/events` 与单条 GET 回执接口不提供兼容入口。

## 频率与预算

没有新事件或到期回执时不发请求，无心跳。空闲后第一轮立即发送；随后至少间隔 180 秒，同期产生的通知在本地积累，下轮批量发送。V2EX 检查周期与此 S2S 间隔独立，手动检查和调低检查周期均不缩短 S2S 冷却。

每个本地账号配置独立调度，对应一台配对设备。多账号实例有多条独立连接；服务端不信任客户端自报的 notifier ID，而以认证设备为配额主体，所有连接另受服务总预算约束。

| 默认限制 | 数值 |
| --- | --- |
| 同一设备的同步间隔 | 至少 180 秒 |
| 每台设备每日同步轮数 | 100 轮 |
| 每台设备每日新事件 | 50 个，含网页汇总、旧版首次汇总与测试 |
| 全服务每日同步轮数 | 10,000 轮 |
| 全服务每日新事件 | 3,000 个 |
| 全服务每日投递处理尝试 | 4,000 次，含失败重试 |

每日额度按 UTC 零点重置。服务端返回 HTTP 429 和 `Retry-After`：`sync_cooldown` 表示冷却；`device_daily_budget` / `service_daily_budget` 表示每日预算不足。整批超出剩余额度时整体等待，不部分接受。触及投递尝试总预算时，未完成事件留在队列，恢复后只投递仍未过期的事件。

服务器持久化设备冷却与预算；更换发送 Token 或重新配对不会重置同一安装身份的额度。助手在网络请求前原子保存整轮等待时间和事件尝试状态，重启、重新配对或修改配置不清除等待。新安装身份有自己的设备额度，仍受全服务预算约束。

回执读取不更新事件状态、不触发 APNs；每个获准同步轮次仍有服务与设备两份预算记账。被冷却或预算拒绝的请求不更新这些计数。入口另有轻量限流，不能将其当作全局精确账本；进入 Worker 的被拒绝请求仍消耗请求额度。上述预算只保护本推送服务的 S2S 工作量，不是 Cloudflare 整个账户绝不会超额的保证。

## 周期网页未读汇总

`type: "unread_summary"` 的线上 DTO 仅含 `event_id`（随机 64 位小写十六进制）、`type`、`source_account_id`、`unread_count`、`created_at` 和 `expires_at`。计数为 1～1,000,000 的整数，时间为本轮网页观察时间，有效期不超过 900 秒；禁止 `notification_id`、`binding_id`、`title`、`body`。用于比较的 API 首条 ID 仅保存在助手本地。

来源账号必须与发送凭据一致，接收者由绑定确定。服务不读取 V2EX，也不独立验证计数。每次首条 ID 变化生成一个事件 ID；重试保持同一 ID 与内容，服务按现有绑定和事件 ID 去重，沿用冷却、预算与回执。APNs 标题为 `@用户名`，正文为“你有 N 条未读提醒，打开应用查看”，不附带具体通知 ID，也不设置 badge。先升级服务再升级助手，旧服务会拒绝未知事件类型。

## 旧版首次未读汇总

为兼容旧助手保留以下 `initial_unread` 协议；当前配置 Cookie 的助手使用上面的周期汇总。

`events` 中可包含一条 `initial_unread` 事件：

```json
{
  "event_id": "SHA256(initial-unread:绑定ID)的小写十六进制",
  "type": "initial_unread",
  "source_account_id": 7,
  "binding_id": "配对回执中的绑定ID",
  "unread_count": 3,
  "created_at": "2026-09-13T08:00:00Z",
  "expires_at": "2026-09-13T08:05:00Z"
}
```

计数为 1～1,000,000 的整数；绑定与发送凭据必须匹配，每个绑定只有这一固定事件 ID。事件有效期最多 5 分钟，观察时间须在首次成功配对时间前后 5 分钟内，可早于配对几秒，不适用普通通知的配对前过滤。不包含 Cookie、通知正文或具体未读 ID；DTO 可带空字符串 `title` / `body`，服务端规范化时忽略。

旧版助手只在账号配置第一次配对时创建汇总并持久化初始化完成状态；重启、换设备或重新配对不重复补发。汇总优先提交，仍遵守连接冷却、预算与有效期。APNs 显示“你有 N 条未读提醒，打开应用查看”，不设置绝对 badge 数。

## 主动测试

用户可通过助手管理页触发 `type: "test"` 事件。线上 DTO 仅包含 `event_id`（64 位小写十六进制）、`type`、`source_account_id`、`created_at` 和 `expires_at`，有效期最多 900 秒；禁止 `title`、`body`、`notification_id`、`binding_id` 和 `unread_count`。固定文案由服务端生成，助手本地保存的预览文案不上传。

测试必须使用已配对发送凭据，来源账号必须匹配，立即配对同秒也可测试。它与其他事件共用幂等回执、上报冷却、每日新事件与投递尝试预算，不提供绕过限额的接口。APNs 标题为接收账号 `@用户名`，正文为“这是一条 V2Echo 测试推送，收到此消息说明推送已到达设备。”；不携带通知 ID，不设置 badge，沿用原有账号、安装和绑定校验及通知入口。

部署应先升级支持 `test` 的服务端，再升级助手；旧服务会拒绝包含未知事件类型的整批请求。

## 有效期与送达边界

普通事件在产生后 24 小时过期，首次汇总在观察后 5 分钟过期，周期网页汇总和主动测试在创建后 15 分钟过期。助手超过期限停止请求，但保留本地通知记录；本地 expired 不证明 APNs 之前从未接受过。服务端清除终态正文并保留去重记录至事件过期 48 小时后。

网络中断可使结果未知，幂等重试不等于设备端严格只显示一次。APNs 已接收的通知不能撤回，每次 APNs 系统排队时间最多 5 分钟。

## 官方依据

- [APNs 响应状态](https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns)
- [发送 APNs 请求](https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns)
- [Cloudflare 限流绑定的局部性与一致性](https://developers.cloudflare.com/workers/runtime-apis/bindings/rate-limit/)
