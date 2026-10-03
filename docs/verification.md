# 验证记录

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
