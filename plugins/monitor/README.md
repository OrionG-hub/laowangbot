# Monitor 1.0.0

官方内置插件（自 laowangbot 0.1.8 起，无需本地安装或编译），源自用户提供的 `monitor.ts`（AyuGram Monitor V3.21，1395 行）。不需要 Node、Python 或原 TypeScript 运行时。入口 `Open`，使用宿主 pluginapi 接口；`cmd/plugin-monitor` 仅作兼容开发入口。

## 功能核对

- 全局开关；当前群 on/off；全部监控/监听列表；排除列表；配置和状态展示。
- 全局/群独立关键词的添加、删除、清空；普通关键词忽略大小写；`re:表达式` / `re:/表达式/flags`；支持回溯、反向引用、前后断言，100ms 正则执行上限。
- 指定人和指定频道发送者；显式群/用户 ID 或公开/私有消息链接；身份过滤 owner/admin/user/bot；指定人绕过身份过滤。保持源代码实际行为：指定人是额外匹配条件，不排斥同群其他符合关键词和身份条件的人。
- 目标列表；Bot 通知或用户账号通知；原消息转发；受保护消息降级为转义 HTML 文本；按钮文字参与匹配。
- 抽奖口令提取、60 Unicode 字符上限；start/startapp 按钮提取；候选群链接、实体核验和不可发言频道跳过；公开/私有群原消息链接、私聊/Bot 会话链接。
- Bot getUpdates 回调；机主鉴权；一次性按钮与保留 URL 按钮；回调回答；冲突退避；持久化 offset。Token 不内置，不显示在配置输出和网络错误中。
- `.monitor send`；`.monitor sync`；可信 Leader；机主按钮广播与本机执行；2 分钟本机回声抑制和按聊天/消息 ID 防止 event+command 重复执行；1.5–6 秒随机延迟；不可发言目标降级至来源群；成功/失败反馈和自动删除。
- 抽奖 ID、深链、转发来源、奖品/开奖字段、SHA256 文本指纹去重；24 小时有效期；旧 lottery 键归一化；每 10 分钟清理；诊断样本轮换；clean。
- 保留 `monitor.json` 的 `monitor_settings`、`monitor_pending_actions`（二元数组）、`monitor_dedup` 格式。额外持久化延迟任务、同步抑制和 offset；权限 0600，临时文件原子替换；迁移由宿主负责。

## 命令

`.monitor` 与 `.monitor help` 显示完整帮助和当前群状态；参数错误会明确报错，不保存配置。配置过长时按段发送。

前缀 `.monitor`：`list`、`list_groups`、`on`、`off`、`clean`、`global on/off`、`send <群ID> <文本>`、`sync <目标> <备用群> <文本>`。

`set` 后支持：

- `leader <ID>/del`、`bot_token <Token>/del`、`bot_id <ID>/del`
- `monitor_all_groups on/off`、`dedup on/off`
- `monitor_admins_messages on/off`、`monitor_users_messages on/off`
- `ignore_bot_messages on/off`：与原插件一致，on 表示监控机器人，off 表示忽略。
- `monitor_group add <ID/链接>` / `del <序号>`；`exclude_group` 同格式。
- `keyword add <关键词>` / `del <序号>`。
- `group_keyword add <群> <关键词>` / `del <群> <关键词/序号>` / `clear <群>`。
- `group_user add <消息链接>` 或 `add <群> <用户ID>`；`del <群> <用户ID/序号>`；`clear <群>`。
- `target add <ID> [备注]` / `del <序号>`。

## 与原版的明确差异和边界

1. 仅存在 10 分钟内有效的“一键参加”按钮时，每秒短轮询 getUpdates（timeout=0、HTTP 最长 5 秒）；空闲或按钮全部过期时停止轮询。超过 10 分钟的按钮不执行代发。网络请求仍在插件串行调用中，最长可能占用 5 秒。
2. 延迟发送/删除通过宿主 tick 驱动并落盘；重启可恢复；删除失败最多重试 3 次。磁盘写入比原来的每分钟去重快照更及时。
3. 正则用 Go regexp2 ECMAScript 模式。`i/m/s`、单次测试的 `g/d`、`y` 起点约束受支持；`u` 使用 Go Unicode 字符语义，并非 JS UTF-16 的逐位等价；`v` Unicode 集合语法明确拒绝。依赖 JavaScript 专属 Unicode 转义/集合语义的正则需要改写，不宣称与 JS RegExp 全部语法完全等价。
4. 配置列表使用转义 JSON 展示完整字段和已有目标备注，不额外逐项查询群名称；Token 只显示是否设置。通知及受保护消息的文本降级均控制 Telegram 消息长度。
5. Bot 通知 HTTP 网络失败最多重试 3 次，通过持久化 tick 任务执行（至少 500ms，实际受 tick 间隔影响）；用户账号原消息转发仍执行。无 Bot Token 的通知与原版一样没有交互按钮。
6. 通知、同步反馈及定时任务发送/删除失败会返回宿主错误；保存失败时回滚设置。更换 Bot Token 会清零轮询 offset。与原版一样，去重记录在通知前保存，不能保证网络故障下所有目标均收到；崩溃发生在远端发送成功与本地落盘之间仍可能重发。禁止把本地测试等同真实 Telegram 联调通过。
7. 本插件不会导入原文件中的凭证；仅使用运行时设置的 Token。同步只信任用户类型发送者，采用有命名空间的发送者 ID；频道同号不能冒充 Leader/机主。回调在无法确认机主时拒绝执行（原版此处允许 owner 未初始化时继续）。
8. 到达时距消息发出不超过 15 分钟的推送仍可提醒，插件队列等待时间不计入这个上限；启动前消息仍由宿主过滤。启用去重时沿用现有抽奖/深链/来源/文本规则，短文本等无法生成去重键的内容不保证被去重。

宿主每分钟记录 `plugin.stats`：收到事件数、过滤数、到达即晚于 60 秒数、最大到达延迟、tick 次数、最慢 tick/调用耗时和队列长度。计数无消息文本或凭证；完全没有事件和 tick 时不输出。

## 验证

`go test -race -coverprofile=coverage.out ./plugins/monitor ./cmd/plugin-monitor`

测试包括 fake Host 的命令/过滤/去重/持久化/定时执行，以及 httptest Bot API 的回调鉴权、按钮执行、同步回声抑制、offset 和 409 退避。所有外发均为模拟，不连接真实 Telegram。
