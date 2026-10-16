# V2Echo 通知助手 S2S v1

本文定义通知助手的 HTTPS 批量上报、回执和限额契约。所有连接均由助手主动建立，无需公网入站地址或 Webhook。

## 鉴权与目标

基础地址固定为 `https://app.v2echo.com/api/push`，发送 Token 由配对取得。请求用 `Authorization: Bearer <发送 Token>` 鉴权，只能操作该凭据绑定的接收连接与账号。接收连接可以是单台设备，也可以是客户端通过 iCloud 加入的最多 10 台独立设备。`source_account_id` 是配对时固定的来源信息，不用于选择收件人或证明账号所有权。请求不携带 V2EX PAT、密码或 Cookie。

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
    "notification_kind": "reply",
    "details_version": 3,
    "avatar_url": "https://cdn.v2ex.com/avatar/example_normal.png",
    "unread_count": 3,
    "encrypted_bodies": [],
    "created_at": "2026-09-13T08:00:00Z",
    "expires_at": "2026-09-14T08:00:00Z"
  }],
  "receipts": ["已经收到 pending 的另一事件ID，64位小写十六进制"]
}
```

两个数组必须存在，合计至少一项；每轮最多 20 个上报事件、50 个待查回执，整个 JSON 请求不超过 64 KiB。两个数组中的 ID 不可重复。旧明文事件标题不超过 220 个 Unicode 字符。新版线上事件禁止 `title` 和 `body` 明文字段；上例空密文数组表示接收设备尚未登记密钥，只投递通用标题与正文。每设备信封格式见下文。批量规则或事件格式错误会拒绝整批，客户端应只发送自身生成、符合契约的事件。

助手持久化每条事件的稳定 ID 和原始内容；重试不改变事件。服务按设备、绑定与事件 ID 去重，相同 ID 不同内容返回 409。重复上报计入同步轮数，但已存在事件不重复扣除新事件额度。预算先于事件存储预留，存储失败后的重试可能保守地多占一份预算；预留额度不退款，以免失败请求绕过限制。

## 回执与重试

HTTP 200 返回这一轮的全部回执，不等待 APNs 完成：

```json
{
  "retry_after_seconds": 30,
  "receipts": [
    {"event_id":"本轮上报ID","status":"pending","retry_after_seconds":30},
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

共享连接的回执可增加 `deliveries` 数组，逐项包含设备随机 ID、处理状态及原因。只有全部接收设备都被 APNs 接受时，顶层状态才为 `apns_accepted`；存在处理中设备为 `pending`，全部过期为 `expired`，其他终态为 `rejected`。部分失败原因是 `one_or_more_deliveries_rejected`，无可投递设备是 `no_active_devices`。当前助手按顶层状态处理，忽略新增数组；单设备 ACK 仍由接收端上报，不将聚合成功视作实际显示。

顶层 `retry_after_seconds` 是整轮最短间隔；单条 pending 的间隔还会考虑服务端下一次投递与租约时间。某个回执退避一小时不会阻塞其他新事件在下一轮提交。

助手 HTTP 总超时为 17 秒。网络错误、5xx、无效回执按整条连接持久化指数退避（至少 30 秒，最长 1 小时，加 0～15 秒随机延迟）；HTTP `Retry-After` 可以要求更长等待，最多接受 48 小时。429 暂停该连接的全部上报和查询，而非仅暂停本批事件。401 / 403 停止该连接自动上报，直到更新凭据或重新配对。其他 4xx 将本批记录为拒绝。

原 `/v1/events` 与单条 GET 回执接口不提供兼容入口。

## 频率与预算

没有新事件或到期回执时不发请求，无心跳。空闲后第一轮立即发送；随后至少间隔 30 秒，同期产生的通知在本地积累，下轮批量发送。V2EX 检查周期与此 S2S 间隔独立，手动检查和调低检查周期均不缩短 S2S 冷却。

每个本地账号配置独立调度，对应一条配对接收连接；同账号的多个接收设备可由客户端加入该连接，助手仍只上报一次。多账号实例有多条独立连接；服务端不信任客户端自报的 notifier ID，而以认证设备为配额主体，所有连接另受服务总预算约束。

| 默认限制 | 数值 |
| --- | --- |
| 同一设备的同步间隔 | 至少 30 秒 |
| 每台设备每日同步轮数 | 1,000 轮 |
| 每台设备每日新事件 | 500 个，含详细通知、旧汇总与测试 |
| 全服务每日同步轮数 | 20,000 轮 |
| 全服务每日新事件 | 10,000 个 |
| 全服务每日投递处理尝试 | 100,000 次，含共享连接逐设备投递和失败重试 |

每日额度按 UTC 零点重置。服务端返回 HTTP 429 和 `Retry-After`：`sync_cooldown` 表示冷却；`device_daily_budget` / `service_daily_budget` 表示每日预算不足。整批超出剩余额度时整体等待，不部分接受。触及投递尝试总预算时，未完成事件留在队列，恢复后只投递仍未过期的事件。

服务器持久化设备冷却与预算；更换发送 Token 或重新配对不会重置同一安装身份的额度。助手在网络请求前原子保存整轮等待时间和事件尝试状态，重启、重新配对或修改配置不清除等待。新安装身份有自己的设备额度，仍受全服务预算约束。

回执读取不更新事件状态、不触发 APNs；每个获准同步轮次仍有服务与设备两份预算记账。被冷却或预算拒绝的请求不更新这些计数。入口另有轻量限流，不能将其当作全局精确账本；进入 Worker 的被拒绝请求仍消耗请求额度。上述预算只保护本推送服务的 S2S 工作量，不是 Cloudflare 整个账户绝不会超额的保证。

## 详细通知与升级兼容

新版 `notification` 使用 `details_version: 3`，携带 `encrypted_bodies` 和可选 `avatar_url` / `unread_count`，禁止 `title`、`body` 明文。服务仍接受旧助手的 `details_version: 1` 明文格式和 `details_version: 2` 仅正文加密格式，保留旧规范化与哈希；这两者不具备新版标题与正文共同加密的保护。头像限 1024 字符的 HTTPS URL，仅接受 V2EX / www / cdn 及 cdn 子域下的 `/avatar/` 或 `/gravatar/`，禁止认证信息、端口与 fragment。计数范围 1～1,000,000，是网页绝对快照，直接设置 badge，不逐条累加。正文对应回复或提及内容、被感谢回复，感谢主题与收藏对应主题标题；具体清洗见同步契约。

详细事件 `created_at` 为首次入队时间，原始通知时间留在助手本地；因此首次启用可以提醒当前未读中的旧消息。服务端仍按事件创建时间过滤配对前事件。标题和正文显示分别最多取前 100 / 180 个 Unicode 字符并加省略号，为 APNs 的 4 KiB 载荷留出空间；系统控制可见行数。iOS 扩展解密后从标题识别发送者和操作，匿名下载头像并捐赠 incoming `INSendMessageIntent`，转换为突出头像的系统通讯通知；发送者名称只在本机从解密内容提取，不新增明文字段。通讯发送者显示名设为「用户名＋动作」，供系统生成主标题，正文仅保留具体内容；用户 handle 仍按原用户名生成。系统把整段文字视为显示名称，最终排版需真机核对。按接收账号与发送者建立会话标识，不虚构主题会话；头像下载或转换失败保留文字，独立 ACK 不依赖头像或转换成功。

普通 `notification` 可额外携带 `notification_kind`，允许 `reply`、`mention`、`thanks_topic`、`thanks_reply`、`favorite`、`other`，类型与 `notification_id` 是明文路由元数据；标题与正文仍独立加密。助手从通知结构中的动作确定类型，排除主题链接及正文中的词，不解析系统通知的展示文案。Worker 原样透传类型和真实 ID；不携带该字段的旧事件维持原规范化及哈希，已经持久化的旧密文不补字段。其他事件禁止调用方提交该字段；分类测试由 Worker 从 `test_kind` 生成 APNs 的同名字段，不生成通知 ID。新版助手需同时确认 `notification_routing: 1`，旧服务上继续保留事件等待升级。

匿名 `GET /v1/health` 返回 `notification_body_encryption: 2`、`notification_routing: 1`、`notification_details: 1`、`notification_test_samples: 1`、`max_events: 20`、`max_receipts: 50`、`max_sync_bytes: 65536` 和 `sync_interval: 30`。新助手发送所有逐条通知 / 分类测试前检查加密能力；缺项、失败或旧服务时保留队列并提示升级，三分钟后重查。滚动升级中收到 400 / 404 / 413 也保留新批次重试，有效期不会延长。仅汇总、连通性测试与回执的批次仍按 4 条 / 16 条 / 16 KiB 发送，并遵守服务返回的更长冷却。

建议先更新服务，再更新助手及 iOS。新服务兼容原 `notification`、`unread_summary`、`initial_unread` 与未指定 `test_kind` 的 `test`，旧事件规范化与摘要哈希不变。旧 iOS 只显示通用标题、通用正文和角标；新版 iOS 登记设备公钥后在扩展解密标题和正文。新 iOS 接收旧载荷保持原行为。通常无需重新配对或清空数据，首次打开新版 App 完成设备注册即可；本机私钥丢失或密钥冲突时须关闭再重新开启个人提醒以建立新安装身份。首次升级会对当前未读范围逐条补发一次，可能与此前汇总重复提醒。

## 标题与正文端到端加密 v2

新版助手只在用户服务器保留原文及本地预览，上传前为每个已登记的 iOS 接收设备分别加密标题和正文。服务与 APNs 不持有解密私钥，只转发密文；账号、编号、时间、未读数和头像地址仍为明文。设备无密钥、扩展未执行、未解锁过设备、解密失败或本机配对已撤销时，仅显示标题“V2Echo 通知”和正文“有新通知，打开应用查看”，不回退上传明文。macOS 尚未接入解密。

- 接收端通过设备凭据 `PUT /v1/devices/{id}/body-key` 登记 `{ "public_key": "32字节X25519公钥的无填充base64url" }`。同一设备公钥不可替换，重复登记同值幂等；不同值返回 409。私钥仅在设备与通知扩展共用的设备专属 Keychain 中，使用首次解锁后可访问保护，不同步 iCloud、不纳入应用备份，系统恢复不跨设备迁移。
- 助手通过发送凭据 `GET /v1/recipient-keys` 获取 `{source_account_id, recipients:[{device_id,binding_id,key_id,public_key}]}`，最多 10 台，仅含当前连接、有效且有公钥的 iOS 设备。`key_id` 为公钥原始 32 字节的 SHA-256 小写十六进制摘要。空数组为有效旧设备兼容状态；请求失败、账号不匹配或格式异常须等待，不当作空数组。
- 助手在本地按 `device_id` 固定首次取得的公钥；已知设备密钥变化会暂停该批内容发送。首次公钥和新设备加入仍信任配对服务与 HTTPS，未提供带外指纹核验，不承诺抵抗恶意配对服务首次替换公钥，也不提供接收端私钥泄露后的历史前向保密。
- 每个事件、每个接收者生成独立的临时 X25519 密钥与随机 12 字节 nonce。ECDH 输出经 HKDF-SHA256 派生 32 字节 AES 密钥；salt 是接收公钥的 SHA-256 原始 32 字节，info 与 GCM AAD 均为下述 UTF-8 字符串（无末尾换行）。AES-256-GCM 的 `ciphertext` 编码包含密文与末尾 16 字节 tag。所有二进制字段采用无填充 base64url。

```text
V2EchoPushContent/v2\n{event_id}\n{source_account_id十进制}\n{device_id}\n{binding_id}\n{key_id}
```

`encrypted_bodies` 中每项仅含 `device_id`、`binding_id`、`key_id`、`ephemeral_key`、`nonce`、`ciphertext`。前两者为小写 UUID，`ephemeral_key` 是临时公钥；每设备至多一项，数组至多 10 项；密文解码后 16～1144 字节。标题和正文先分别按 Unicode 截取 100 / 180 个字符并按需追加省略号，空标题 / 正文分别使用“V2Echo 通知”/“打开应用查看详情”。加密前的二进制明文为：2 字节大端无符号标题 UTF-8 字节长度、标题 UTF-8 字节、正文 UTF-8 字节，最多 1128 字节（标题最多 403、正文最多 723 字节）。这样控制字符也不会扩大外层 JSON。服务仅选择与目标设备和当前绑定完全匹配的一项，放入 APNs 自定义字段 `encrypted_body`，并附 `body_encryption: 2`、`source_account_id` 及现有路由字段；`aps.alert.title` 始终为“V2Echo 通知”，`aps.alert.body` 始终为“有新通知，打开应用查看”，设置 `mutable-content: 1`。设备还检查本机当前或未过期待确认绑定与账号授权，之后才解密修改显示内容。ACK 仍只说明扩展处理了载荷，不证明解密、显示或阅读成功。

助手在 `encrypted_notification_wire` 保存首次生成的线上密文，之后才预留发送尝试。重启、超时和重试复用字节完全一致的密文，不重复生成 nonce 或改写事件摘要；后加入设备不追补已准备事件的内容。`notification_key_pins` 保存首次公钥。升级前未尝试的旧事件和已缓存的仅正文加密事件转为新版密文；已经尝试且未获回执的旧明文或仅正文加密事件停止重传并标记跳过，已提交事件继续查询回执。升级无法撤回此前已上传或在途的明文。

兼容读取：`body_encryption: 1` 仍采用 `V2EchoPushBody/v1` 上下文和 UTF-8 正文明文（密文最多 740 字节），保留原明文标题。新版生成一律使用 v2；两种上下文互不兼容，修改版本不能使同一密文通过验证。分类测试使用 `body_encryption: 2` 与相同的标题、正文二进制明文。旧扩展不认识 v2 时显示服务端通用占位文字。

## 兼容旧版周期网页未读汇总

`type: "unread_summary"` 的线上 DTO 仅含 `event_id`（随机 64 位小写十六进制）、`type`、`source_account_id`、`unread_count`、`created_at` 和 `expires_at`。计数为 1～1,000,000 的整数，时间为本轮网页观察时间，有效期不超过 900 秒；禁止 `notification_id`、`binding_id`、`title`、`body`。用于比较的 API 首条 ID 仅保存在助手本地。

来源账号必须与发送凭据一致，接收者由绑定确定。服务不读取 V2EX，也不独立验证计数。每次首条 ID 变化生成一个事件 ID；重试保持同一 ID 与内容，服务按现有绑定和事件 ID 去重，沿用冷却、预算与回执。APNs 标题为 `@用户名`，正文为“你有 N 条未读提醒，打开应用查看”，不附带具体通知 ID；badge 使用网页快照的未读总数（绝对值，不累加）。先升级服务再升级助手，旧服务会拒绝未知事件类型。

## 旧版首次未读汇总

为兼容旧助手保留以下 `initial_unread` 协议；当前配置 Cookie 的助手创建详细通知。

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

旧版助手只在账号配置第一次配对时创建汇总并持久化初始化完成状态；重启、换设备或重新配对不重复补发。汇总优先提交，仍遵守连接冷却、预算与有效期。APNs 显示“你有 N 条未读提醒，打开应用查看”，badge 使用该快照的未读总数。角标需要接收设备允许系统标记；延迟投递或其他设备已读可能使快照暂时过时，由 App 的当前账号未读状态校准，助手不发送已读清零事件。

## 主动测试

用户可通过助手管理页触发 `type: "test"` 事件。线上 DTO 包含 `event_id`（64 位小写十六进制）、`type`、`source_account_id`、`created_at`、`expires_at` 和可选 `test_kind`，有效期最多 900 秒；禁止 `title`、`body`、`notification_id`、`binding_id` 和 `unread_count`。连通性测试固定文案由服务端生成。分类测试另带 `body_encryption: 2` 和 `encrypted_bodies`，由助手加密固定示例标题与正文，头像仍由服务端选择。

测试必须使用已配对发送凭据，来源账号必须匹配，立即配对同秒也可测试。它与其他事件共用幂等回执、上报冷却、每日新事件与投递尝试预算，不提供绕过限额的接口。APNs 标题为接收账号 `@用户名`，正文为“这是一条 V2Echo 测试推送，收到此消息说明推送已到达设备。”；不携带通知 ID，不设置 badge，沿用原有账号、安装和绑定校验及通知入口。

`test_kind` 为 `reply`、`mention`、`thanks_topic`、`thanks_reply` 或 `favorite` 时使用对应的虚构「测试用户」固定标题与正文，并使用指定公开测试头像 `https://cdn.v2ex.com/avatar/17f5/9a32/136734_xlarge.png?m=1784141154`。测试账号与文案仍为虚构，头像来自公开 V2EX CDN；旧 `GET /v1/test-avatar.png` 合成图片接口仅为已发送消息保留。省略该字段保留上述连通性测试。分类测试同样不设置 badge、不带通知 ID。头像地址由服务端选择；新版标题与正文均由助手生成并与真实通知走同一加密流程，旧设备显示通用标题与正文。服务只校验密文结构，无法审查其解密内容；助手管理接口仅接受固定 `test_kind`，不接受任意正文。助手先检查加密能力，旧服务上保留等待升级。

## 有效期与送达边界

详细事件在首次入队后 24 小时过期，旧格式普通事件在原通知产生后 24 小时过期，首次汇总在观察后 5 分钟过期，周期网页汇总和主动测试在创建后 15 分钟过期。助手超过期限停止请求，但保留本地通知记录；本地 expired 不证明 APNs 之前从未接受过。服务端清除终态正文并保留去重记录至事件过期 48 小时后。

网络中断可使结果未知，幂等重试不等于设备端严格只显示一次。APNs 已接收的通知不能撤回，每次 APNs 系统排队时间最多 5 分钟。

## 官方依据

- [APNs 响应状态](https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns)
- [发送 APNs 请求](https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns)
- [Cloudflare 限流绑定的局部性与一致性](https://developers.cloudflare.com/workers/runtime-apis/bindings/rate-limit/)
