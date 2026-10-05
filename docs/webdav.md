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

根地址必须为 HTTPS，不带账号、查询参数或片段。服务器须支持 PROPFIND、MKCOL、PUT、HEAD 和 MOVE；`.dav test` 只读检查根目录，实际上传还需要创建、写入和移动权限。

回复单条图片、视频、语音或文件消息发送 `.dav`，归档到 `会话名 (会话ID)/年/月/日/`。文件名包含消息 ID 和随机标识，避免覆盖。会话文件夹按 ID 固定，之后改名不改变已有映射。不自动上传整组相册。成功提示保留三秒后删除命令消息。

仅账号本人可用，不能通过 sudo/sure 借用；配置仅限收藏夹，密码和 API Token 不回显并隐藏配置命令。凭据仍经 Telegram 云聊天传输，其他客户端缓存不保证被清除。

默认单文件上限为 1024 MiB，`.dav config limit 0` 取消大小上限（可设 0–4096 MiB）。文件先流式下载到部署目录 `temp/webdav-*`，再流式上传，不把整个大文件放进内存。下载前及下载期间检查磁盘，至少预留 256 MiB；Linux 临时目录不能位于 tmpfs。一次只允许一个上传，总时限一小时，完成或取消后清理本地临时文件。

`.dav cancel` 请求取消。远端先写 `.partial-` 临时文件、核验大小、MOVE 到最终路径，再次核验大小后才写成功记录。失败或取消可能留下远端文件；不会自动删除云端内容。大小核验不等于远端 SHA256 校验，记录中的 SHA256 为本地下载文件摘要。

HTTP 413 表示 WebDAV 服务端或中间代理拒绝上传大小；`.dav config limit 0` 无法解除服务端限制。拒绝可能发生在文件到达 CloudDrive 前，不保证远端生成了 `.partial-` 文件。使用 CloudDrive 时可尝试以下 API 分片模式。

## CloudDrive 经 CDN 分片上传

CloudDrive 的 WebDAV PUT 没有通用分片协议；启用可选 `clouddrive` 模式后，插件改用同一域名的官方 gRPC-Web API：CreateFile → 按偏移 WriteToFile → CloseFile，随后仍通过 WebDAV HEAD → MOVE → HEAD 核验归档。WebDAV 地址和原账号密码保持不变。普通 WebDAV 默认模式不受影响。

在 CloudDrive 管理页面创建 API Token，授予列目录、创建文件、写入和读取权限，根目录限定为 WebDAV 对应目录。不要用 WebDAV 密码替代 API Token。在 Telegram 收藏夹配置：

```text
.dav config cdtoken 你的API令牌
.dav config cdroot /
.dav config mode clouddrive
.dav test
```

`cdroot` 是 API Token 视角下与 WebDAV 根目录对应的绝对路径：若 Token 已将 `/123云盘/WebDAV/Telegram` 映射为根目录，用 `/`；若 API 仍使用全局路径，用 `/123云盘/WebDAV/Telegram`。两者必须指向同一位置；`.dav test` 只验证各自可访问，不能证明目录映射一致。映射错误会使上传后的 WebDAV 大小核验失败，不写成功记录。

每片最大 **50 MB**（50,000,000 字节，协议额外开销不足 100 字节），126 MB 文件分三次写入：50 MB + 50 MB + 26 MB。串行从磁盘流式发送，不分配整片内存、不并发上传。进度显示当前片数和服务端已确认字节数。每次写入必须收到完整成功响应且确认字节数一致；网络、写入或关闭失败不自动重试，以免对不确定状态重复操作。失败尝试关闭句柄，保留 `.partial-` 文件。

CDN 必须允许同域名 `/clouddrive.CloudDriveFileSrv/` 的 POST 请求并透传 gRPC-Web，机器人访问不能被人机验证拦截；CloudDrive 自身也必须接受 50 MB 加协议开销的 RPC 消息。减小分片无法绕过更小的 CDN 或服务端消息上限。HTTP 413 或 gRPC 8 等错误仍需要检查服务端限制。关闭文件及 WebDAV 核验仅表示 CloudDrive 可访问该文件，不能保证它已同步到网盘后端。

接口依据 [CloudDrive 官方 API 指南](https://www.clouddrive2.com/api/CloudDrive2_gRPC_API_Guide.html)，未使用 1.1.1 新增的 `uploadImmediately` 字段。CloudDrive 1.1.1 经 Peekabo CDN 的 50 MB 分片实际上传尚未实机验证；本地 TLS 模拟测试不能替代线上验收。可用 `.dav config mode webdav` 切回普通上传。

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
