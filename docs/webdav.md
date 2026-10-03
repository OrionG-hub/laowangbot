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

仅账号本人可用，不能通过 sudo/sure 借用；配置仅限收藏夹，密码不回显并隐藏配置命令。密码仍经 Telegram 云聊天传输，其他客户端缓存不保证被清除。

默认单文件上限为 1024 MiB，`.dav config limit 0` 取消大小上限（可设 0–4096 MiB）。文件先流式下载到部署目录 `temp/webdav-*`，再流式上传，不把整个大文件放进内存。下载前及下载期间检查磁盘，至少预留 256 MiB；Linux 临时目录不能位于 tmpfs。一次只允许一个上传，总时限一小时，完成或取消后清理本地临时文件。

`.dav cancel` 请求取消。远端先写 `.partial-` 临时文件、核验大小、MOVE 到最终路径，再次核验大小后才写成功记录。失败或取消可能留下远端文件；不会自动删除云端内容。大小核验不等于远端 SHA256 校验，记录中的 SHA256 为本地下载文件摘要。

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
