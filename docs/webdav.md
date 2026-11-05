# WebDAV 文件归档

WebDAV 随官方程序内置，更新后直接使用 `.dav`，无需 TPM 安装或本地编译。以下示例使用默认前缀 `.`。

## 配置与上传

在 Telegram 收藏夹依次配置：

```text
.dav config url https://example.com/dav
.dav config user 用户名
.dav config pass 密码
.dav test
```

根地址必须为 HTTPS，不带账号、查询参数或片段。服务器须支持 PROPFIND（含 Depth: 1）、MKCOL、PUT、GET、HEAD 和 MOVE；`.dav test` 只读检查根目录，实际上传还需要创建、写入和移动权限。

回复单条图片、视频、语音或文件消息发送 `.dav`，归档到 `会话名 (会话ID)/年/月/日/`。保留原文件名（仍会清理非法字符并限制长度），不添加消息 ID 或随机前缀。同一归档目录内，对原名及其序号文件发起远端内容请求，响应大小明确不同时提前结束读取，大小相同或未知时完整读取并比较 SHA256：同名同摘要拒绝上传，同名不同摘要使用最小空闲序号，例如 `123pan-3.2.5-arm64 (1).dmg`。检查包含已有记录识别的旧版前缀文件，但不会重命名旧文件。目录或内容无法读取时停止上传；最终移动始终禁止覆盖。群组/频道按 ID 识别；群名变化后，下次在该群使用 `.dav` 时重命名整个远端目录，并同步当前 WebDAV 目标的历史路径与会话名。新目录已存在时停止，不覆盖或合并；未确认的移动会保留状态，下一次重试先恢复。私聊目录不自动随名称变更。不自动上传整组相册。成功提示保留三秒后删除命令消息。

回复纯文本消息发送 `.dav`，会将正文及消息信息归档为 `message_消息ID.json`，使用相同的会话与日期目录。带文字说明的图片、视频、语音或文件消息，同时归档媒体和独立 JSON；没有文字的媒体保持只归档文件。链接预览按文本处理，联系人、投票等其他消息类型不在支持范围内。

JSON 包含 `authorId`、`id`、`mediaTypes`、`peerId`、`text`、`timestamp`、`datetime`，有话题或回复关系时额外包含 `threadId`、`replyToId`、`replyToPeerId`。`timestamp` 是原消息的 Unix 秒级发送时间，`datetime` 是同一时刻按北京时间（UTC+8）格式化的 `YYYY-MM-DD HH:mm:ss`，不受部署机器时区影响。例如 `1793846103` 对应 `2026-11-05 10:35:03`。新字段随新生成的 JSON 写入，已有远端 JSON 不自动补写；会话/发送者 ID 沿用程序标记格式（用户为正数，普通群以 `-` 开头，超级群/频道以 `-100` 开头）；没有话题时省略 `threadId`，不伪造第三方客户端的 `namespace`。纯文本的 `mediaTypes` 为 `[]`，媒体类型为 `photo`、`video`、`audio` 或 `document`。

媒体与 JSON 各有一条成功记录，可分别通过 `.dav list`、`.dav info` 查询。若媒体成功但 JSON 失败，保留媒体记录并提示错误，回复同一消息重试 `.dav` 时只补传 JSON。若 JSON 已完成远端归档但确认或本地记录保存失败，重试时会读取同名远端文件，确认大小和 SHA256 一致后恢复记录。旧版本已经归档的媒体附带文字时，也可用同一命令补存 JSON。重复归档按消息和归档类型分别跳过，不会因消息后来编辑而刷新已有 JSON。

仅账号本人可用，不能通过 sudo/sure 借用；配置仅限收藏夹，密码和 API Token 不回显并隐藏配置命令。凭据仍经 Telegram 云聊天传输，其他客户端缓存不保证被清除。

默认单文件上限为 1024 MiB，`.dav config limit 0` 取消大小上限（可设 0–4096 MiB）。文件先流式下载到部署目录 `temp/webdav-*`，再流式上传，不把整个大文件放进内存。下载前及下载期间检查磁盘，至少预留 256 MiB；Linux 临时目录不能位于 tmpfs。一次只允许一个上传，总时限一小时，完成或取消后清理本地临时文件。

`.dav cancel` 请求取消。远端先写 `.partial-` 临时文件、核验大小、MOVE 到最终路径，再次核验大小后才写成功记录。失败或取消可能留下远端文件；不会自动删除云端内容。大小核验不等于远端 SHA256 校验，记录中的 SHA256 为本地下载文件摘要。

HTTP 413 表示 WebDAV 服务端或中间代理拒绝上传大小；`.dav config limit 0` 无法解除服务端限制。拒绝可能发生在文件到达 CloudDrive 前，不保证远端生成了 `.partial-` 文件。使用 CloudDrive 时可尝试以下 HTTP 分片模式。

## CloudDrive 经 CDN 分片上传

启用 `clouddrive` 模式后，插件使用 WebDAV 的 `sabredav-partialupdate` 扩展：首片 PUT 创建唯一临时文件，后续 PATCH 按 `X-Update-Range` 指定偏移续写。仅使用现有 WebDAV 地址、用户名和密码，不调用 gRPC，也不需要 API Token。旧 `cdtoken`、`cdroot` 配置保留但不参与上传，普通 `webdav` 模式保持整文件 PUT。

在 Telegram 收藏夹配置：

```text
.dav config mode clouddrive
.dav test
```

每片最大 **80 MB**（80,000,000 字节），126 MB 文件分两次写入：80 MB + 46 MB。串行从磁盘流式发送，不分配整片内存。首片带 `If-None-Match: *` 防止覆盖，不自动重试不确定的写入。失败保留 `.partial-` 文件。

上传前 OPTIONS 必须声明 `DAV: sabredav-partialupdate`。完成分片后流式 GET 全部临时文件，对比本地 SHA256 和大小；通过后执行 HEAD → MOVE（禁止覆盖）→ HEAD，再保存成功记录。远端校验会额外下载一次完整文件，计入一小时上传总时限；只能确认 CloudDrive 返回的内容，不能保证已同步到网盘后端。

CDN 必须放行 WebDAV 路径的 OPTIONS、PROPFIND、MKCOL、PUT、PATCH、GET、HEAD 和 MOVE，透传 `X-Update-Range` 与条件请求头，且不能将机器人请求转到人机验证页面。80 MB 分片只能避开更大的单请求体限制，不能绕过 WAF 或服务端不支持续写的限制。

已在 CloudDrive 1.1.1 本机直连验证 80 MB PUT + 46 MB PATCH、SHA256、MOVE 和延时读取。Peekabo 域名实测返回 307 跳转到验证码页面，因此经 CDN 的实际上传尚未完成验收。可用 `.dav config mode webdav` 切回普通上传。

## 查询

```text
.dav list
.dav list 2026-10-04
.dav next
.dav prev
.dav page 3
.dav info 记录ID
```

按北京时间上传日期筛选，每页十条；每个聊天独立分页，列表固定五分钟有效，翻页不续期。同一目标、同一消息已有成功记录时跳过重复上传；这不检查云端文件是否后来被手动删除。

## 旧数据与备份

沿用 `data/webdav-config.json` 和 `data/webdav-records.json`，保留记录 ID、完整路径、文件摘要和会话目录映射。旧 mibot-lite 的 `data/` 随迁移复制，无需重新配置。`.bf` 会备份这两个顶层 JSON 文件；临时文件和云端文件不在备份中。

旧 MiBox/TeleBox 的 WebDAV SQLite 或其他格式未提供自动转换；迁移保留在 `legacy/`，不能将保留原文件视为已恢复记录。
