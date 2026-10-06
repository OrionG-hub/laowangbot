# 验证记录

## 0.1.20 CloudDrive HTTP 分片验证（2026-10-06）

测试仅保存在仓库外本地验证副本，生产源码与当前工作区同步。

- 全仓库单元测试及 race、integration 标签集成测试和覆盖率检查通过；全仓库语句覆盖率 54.1%。修正分片进度及成功提示后重跑 WebDAV race、集成与覆盖率检查通过，WebDAV 覆盖率 83.2%。生产代码构建及 vet 通过。
- TLS 与模拟 Telegram RPC 验证 126,000,000 字节文件以 PUT 80,000,000 字节、PATCH 46,000,000 字节上传，偏移、请求长度、SHA256、HEAD → MOVE → HEAD 及模式切换后的去重通过；所有请求体小于 100 MB。
- 首片和第二片写入失败、内容损坏、长度不足或超出、GET 拒绝、HEAD 不符、MOVE 拒绝、能力缺失及取消均不写成功记录；分片失败不重试、不删除远端临时文件。OPTIONS、PUT、PATCH、GET 重定向不会发送凭据到跳转目标。
- 无 API Token 的 CloudDrive 配置及只读 OPTIONS 能力检测通过，旧凭据配置保留兼容且不回显。
- 在用户服务器上直连 CloudDrive 1.1.1 验证 64 KiB + 64 KiB 和 80 MB + 46 MB；下载内容与原始 SHA256 一致，126 MB 文件 MOVE、HEAD 和 15 秒后再次读取通过。独立测试对象已删除。

从服务器访问 Peekabo 域名返回 307 跳转到验证码页面，经 CDN 的实际分片上传尚未验收；需放行 WebDAV 方法及请求头。直连读取仅确认 CloudDrive 返回内容，未验证网盘后端同步。未部署或重启线上机器人。

## 0.1.19 CloudDrive 50 MB 分片及本地阻塞修复（2026-10-05）

- CloudDrive 每片上限改为 50,000,000 字节；TLS 和模拟 Telegram RPC 验证 126 MB 文件按 50 MB + 50 MB + 26 MB 写入，偏移、摘要、关闭及归档核验通过。模拟代理拒绝超过 60 MB 的请求，确认新分片可完成上传。
- 删除重复的 `EventFilter` 声明；Monitor 入队过滤复用执行阶段的 15 分钟判断，5 秒、90 秒、900 秒允许，901 秒拒绝，队列等待不影响判断。修正本地 BH 测试缺失的批次参数。
- 当前工作区的单元测试及 race、integration 标签测试、覆盖率检查、构建和 vet 通过。同步全部当前源码及本地测试到仓库外验证副本后，全套单元/race、集成和覆盖率检查通过；全仓库语句覆盖率 54.1%，WebDAV 单元覆盖率 79.5%，Monitor 84.4%，BH 81.3%。

CloudDrive 1.1.1 经 Peekabo CDN 的实际上传未验证；50 MB 分片加协议开销仍可能超过更小的代理或服务端上限。尚未部署或重启线上服务，本地测试不能证明线上 HTTP 413 已消除。

## 0.1.18 CloudDrive 分片验证

测试仍仅保存在仓库外的本地验证副本；同步本次生产代码并增加 gRPC-Web、TLS 和 Telegram RPC 回归，未提交任何测试文件。

- 全仓库单元测试及 race、integration 标签全套集成测试、覆盖率检查通过；总语句覆盖率 44.2%，WebDAV 83.6%。生产代码构建及 vet 通过。
- 实际在磁盘生成并通过 TLS 发送 126 MiB 文件，验证 80 MiB + 46 MiB 两个 POST 的 protobuf 字段、偏移、请求长度及完整文件摘要；注册命令经模拟 Telegram RPC 下载后完成同样流程。
- 验证完整写入确认、CloseFile → HEAD → MOVE → HEAD、模式切换后的消息去重；第二片失败、确认字节数不符、取消、关闭失败及根目录映射不一致均不写成功记录、不移动归档，保留未完成文件并清理本地临时文件。
- 验证同域名 API 路由、禁止重定向、防止 Token/原始错误体回显、HTML 人机验证、HTTP 413、gRPC 拒绝、损坏帧、缺少完成状态、重复响应与超大响应拒绝；取消后等待句柄关闭再释放任务。
- 配置检查覆盖 Token 隐藏、控制字符、非法目录和模式，以及只读连接检测；保留既有 WebDAV、Monitor、迁移及备份恢复回归。

未使用真实 CloudDrive API Token 或真实 Telegram 账号联调；访问目标域名被 Peekabo 人机验证拦截。CloudDrive 1.0.20 是否接受 80 MiB WriteToFile、CDN gRPC-Web 透传、Token 根目录映射以及网盘后端同步均未线上验证。本地流式测试不是 VPS 内存峰值测量；HTTP 413 / gRPC 8 可能仍来自 CDN 或服务端消息大小限制。

## 0.1.17 PR #3 合并验证

在仓库外同步 0.1.17 生产源码，并新增本地专项回归；测试文件未入库。

- 全仓库单元测试及 race、integration 标签集成测试、覆盖率检查通过；总体语句覆盖率 43.7%，Monitor 74.6%，extensions 64.7%。构建和 vet 通过。
- 对照验证到达年龄 5 秒、90 秒、15 分钟允许提醒，15 分钟加一秒拒绝；入队过滤与执行判定一致，队列积压不缩短有效期，已有抽奖 ID 可去重。
- httptest Bot API 验证空闲及仅过期按钮时零轮询，有有效按钮时轮询；一分钟按钮执行代发，二十分钟按钮拒绝；配置面板状态和建议命令确实互相切换。
- 验证无日期事件的统计、并发计数与重置，以及实际串行插件调用中低于一秒的 tick 仍被计入。
- 继承 WebDAV、PR #4 缓存以及真实二进制迁移/备份恢复回归。

未用真实 Telegram 流量联调 PR #3；每分钟统计输出尚未做线上日志验收。现有去重规则不保证短文本等无法生成键的消息不重复；按钮有效期由原先最长 24 小时收紧为 10 分钟。

## 0.1.16 WebDAV 与头像缓存验证

测试仅保留在仓库外本地副本；副本同步本次生产源码，并修正旧版 Telegram fixture 的媒体 flags 和已增加内置插件后的数量断言。

- 全仓库单元测试及 race 通过；integration 标签全套集成测试通过。
- WebDAV 经 TLS 服务与 Telegram RPC 模拟验证下载、PUT → HEAD → MOVE → HEAD、去重、失败不写成功记录、取消等待下载结束、磁盘余量及大小限制、分页有效期和密码脱敏。126 MiB 文件按块写入磁盘并上传成功。
- 真实构建二进制的迁移与备份恢复验证保留旧 WebDAV 配置、记录 ID、路径和固定会话目录，源文件保持原样。
- PR #4 缓存回归验证头像号变化、未知头像、动画定义变化、损坏元数据、大小上限、容量与过期淘汰、孤儿清理及队列上限。
- 全仓库覆盖率 43.5%，WebDAV 包语句覆盖率 80.5%；`go build ./...` 和 `go vet ./...` 通过。

真实 Telegram 账号与用户 WebDAV 服务未联调；模拟 RPC/TLS 和大文件测试不代表线上网络验收，也不是服务器内存峰值实测。Windows 磁盘 API 未在本机原生运行，发布 CI 将构建六个平台。PR #4 已知边界：本账号头像变更须重连后识别，删除头像可能继续沿用实体缓存中的旧头像号。

以下为历史验证记录。测试文件及测试数据现已移出仓库，相关命令需要本地测试副本；当前 CI 执行构建和静态检查，不再生成测试覆盖率。

本地工具链为 Go 1.27.1（项目最低版本 Go 1.26）。在 macOS arm64 工作站运行：

- `go test -count=1 -race ./...`：通过。
- `go test -count=1 -coverprofile=/tmp/laowangbot-coverage-final.out ./...`：通过；总体语句覆盖率 38.3%。覆盖率是全仓库结果，不能单独代表 Telegram 线上行为。
- `go test -count=1 -tags=integration ./tests/integration`：通过；真实构建的二进制完成 mibot-lite、MiBox、TeleBox fixture 迁移，离线检查、备份、恢复和插件安装/替换。
- `go vet ./...`：通过。
- `bash tests/deployment/install_test.sh`：通过；迁移目标保护、更新备份、失败保留和 launchd 回滚模拟通过。
- `bash -n scripts/build.sh scripts/release.sh scripts/install.sh scripts/install-service.sh`：通过。
- `bash scripts/release.sh v0.1.0-dev`：生成 Linux amd64/arm64、macOS amd64/arm64、Windows amd64/arm64 六个构件及 SHA-256 清单。

Windows PowerShell 脚本和 Dockerfile 已进入 CI；本机没有 PowerShell、Docker，因此未在本机执行。CI 矩阵覆盖目标平台的测试入口，但目标系统服务、Docker 容器启动、真实 Telegram 登录、代理、ffmpeg、外部 AI/语音服务和 `--verify` 均未在本次本地验证。远程下载式安装依赖 GitHub Release 的六平台构件和 SHA-256 清单；发布状态以 GitHub 为准。

`.bf` 备份收录账号配置、`.env`、顶层 `data/*.json`、`state/<name>/*.json` 和 `state/pmcaptcha/legacy/*.json`；插件代码、其他嵌套状态及缓存需要随部署目录单独备份。迁移会将旧插件归档到 `legacy/`，不会把这些目录伪装成已兼容状态。

## 发布前复核

补充并通过回归测试：启动锁检查先于会话写入；备份和恢复拒绝 `data` 符号链接；运行实例固定插件代码及清单，更新后仅在重启时生效，插件状态继续写入原部署。Windows 测试不将 POSIX 权限位当作 ACL 验证。README 提供 `master` 分支的远程单条安装命令。

## TPM 功能补齐验证

新增搜索、详细列表、批量安装/更新/卸载、ZIP 导入导出及远程插件本地修改保护。不增加自定义源；手动插件不参与强制远程更新；运行实例保留启动快照，修改重启后生效。

- `go test -count=1 -race ./...`：通过。
- `go test -count=1 -tags=integration ./...`：通过，包含真实文件的 TPM 导出、卸载、保留状态、重新导入、跳过手动插件和注册命令流程。
- `go test -count=1 -coverprofile=/tmp/tpm-final.cover ./...`：通过，全仓库覆盖率 **39.6%**。
- `go vet ./...`：通过。
- 插件管理包测试覆盖远程下载校验、修改/新增/删除文件保护、强制更新、并发修改复检、损坏清单恢复、ZIP 越界/重复路径/链接/大小与数量限制。

真实 Telegram 回复文件下载和导出消息发送未联调；此次本地文件集成测试不代表真实账号网络验收。Windows/Linux 的服务部署和新 TPM 真机运行仍需目标环境验证。

## 同机迁移向导验证

`install.sh --wizard` / `install.ps1 -Wizard` 提供 mibot-lite、MiBox、TeleBox 选择，自动调用现有迁移和安装流程。无需手动复制配置、会话或数据。

- `bash tests/deployment/migrate_wizard_test.sh`：通过，覆盖三种来源、空输入/取消、非空目标、源目录内目标、服务工作目录不符、启动中旧服务停止、迁移失败及新服务启动前失败恢复。
- `bash tests/deployment/install_test.sh`：通过，原安装/更新/回滚行为回归通过。
- `go test -count=1 -tags=integration ./...`：通过，真实二进制经向导搬迁并检查账号配置、别名数据和迁移报告。
- 单元测试（含 race）、覆盖率检查和 `go vet ./...`：通过；Go 总体覆盖率 39.6%，不包含 Shell/PowerShell 行覆盖率。
- Windows 向导测试已接入 CI；本机缺少 PowerShell，未执行。systemd 停止/恢复使用 mock，未操作真实 Linux 服务；真实 Telegram 登录连接未验证。

## yvlu 动态贴纸空白修复

根因是贴纸属性分支直接返回，未将媒体放入语录请求。已改为静态/WebM 贴纸直接嵌入，TGS 按需通过 Python rlottie/Pillow 渲染 RGBA PNG 帧，再用 ffmpeg 编码 VP9 WebM；转换错误明确返回，不静默降级或提交空媒体。

- 单元测试、race、全套集成测试、覆盖率和 vet 通过；总体覆盖率 **39.8%**。
- 在临时 Python 3.13 环境实际运行 rlottie-python 1.3.8、Pillow 12.3.0 和 ffmpeg 7.1。合成的移动图形 TGS 经转换后解码验证：尺寸、时长、多帧变化、透明背景、半透明颜色及取消操作均通过。
- 通过模拟 Telegram 下载 RPC，验证 WebM/静态贴纸不被丢弃，以及实际 TGS 转换结果进入语录请求。
- Windows amd64 交叉构建通过。Docker TGS 可选构建和 Windows/Linux 原生转换未在本机运行。
- 未使用真实 Telegram 会话调用远端 quote 服务，最终线上语录回传仍未验证；本地合成 fixture 不代表对用户原贴纸的实际抓取或线上验收。

## 0.1.1 提交前验证

版本按最后一位递增，本次为 0.1.1。插件架构切换为 Go 源码协议 v2，TPM 编译成功后提交源码及二进制，失败保留旧部署。monitor、qdsg 首版均为 1.0.0。全套单元/race、integration 标签集成、覆盖率和 vet 已执行；Windows 的运行中源码编译替换明确不支持，相关测试跳过，不能据交叉编译宣称已支持。真实 Telegram、OCR 模型、CF 外援未联调。
