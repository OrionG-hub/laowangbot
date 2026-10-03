# laowangbot

[简体中文](README.md) · [English](README.en.md) · [繁體中文](README.zh-TW.md) · [日本語](README.ja.md)

**Go 核心 · 编译内置源码插件 · 可迁移的 Telegram UserBot**

保留原有内建命令，核心无需 Node.js；按需启动媒体工具，配置和命令状态存为本地 JSON。插件以 Go 源码分发，安装时编译进宿主；附加功能仍可能需要自行配置运行依赖。

项目发布于 `github.com/OrionG-hub/laowangbot`。官方 Release 提供各平台构件和校验文件；Docker 镜像暂未发布，容器请从源码本地构建。

## 快速开始

### 全新安装（需要登录）

首次安装和运行官方二进制无需 Go 或 Node.js；管理源码插件及源码升级需要 Go 和 Git。安装器下载最新官方 Release 并校验 SHA-256，交互登录后启动后台任务。命令要求 Release 已包含对应平台构件与 `checksums.txt`；尚未发布时请使用下面的源码方式。

```sh
# Linux
sudo bash -c 'bash <(curl -fsSL https://raw.githubusercontent.com/OrionG-hub/laowangbot/master/scripts/install.sh) --root /opt/laowangbot'

# macOS
bash <(curl -fsSL https://raw.githubusercontent.com/OrionG-hub/laowangbot/master/scripts/install.sh) --root "$HOME/laowangbot-data"
```

```powershell
# Windows PowerShell
& ([scriptblock]::Create((Invoke-RestMethod 'https://raw.githubusercontent.com/OrionG-hub/laowangbot/master/scripts/install.ps1'))) -Root "$env:LOCALAPPDATA\laowangbot"
```

Linux 使用 systemd，macOS 使用当前用户 launchd，Windows 使用当前用户登录计划任务。安装成功后在收藏夹执行 `.ping`、`.help` 验证连接；登录需要自己的 Telegram API ID、API hash、手机号和验证码。完整参数见 [安装指南](INSTALL.md)。

### 从旧人形一键迁移（无需复制配置）

已有 mibot-lite、MiBox 或 TeleBox？使用以下迁移命令，**不要使用上方的新安装命令**。选择旧人形和旧目录后，脚本自动搬迁配置、会话及已支持的数据；旧会话有效时无需重新输入 API ID 或登录。

```sh
# Linux
sudo bash -c 'bash <(curl -fsSL https://raw.githubusercontent.com/OrionG-hub/laowangbot/master/scripts/install.sh) --wizard --root /opt/laowangbot'

# macOS
bash <(curl -fsSL https://raw.githubusercontent.com/OrionG-hub/laowangbot/master/scripts/install.sh) --wizard --root "$HOME/laowangbot-data"
```

```powershell
# Windows PowerShell
& ([scriptblock]::Create((Invoke-RestMethod 'https://raw.githubusercontent.com/OrionG-hub/laowangbot/master/scripts/install.ps1'))) -Wizard -Root "$env:LOCALAPPDATA\laowangbot"
```

目标目录必须为空，旧目录保留。Linux 迁移会停止并禁用旧 systemd 服务自启动，失败恢复原状态；其他启动器须先自行停止并禁用自启动，避免同一会话同时运行。若已停在 `API ID:`，按 Ctrl+C 后改用以上命令；若提示目标非空，先检查内容，不要直接删除。未知旧插件只归档，代码仍需适配。详见[迁移指南](docs/migration.md)。

### 源码安装与迁移

源码安装需要 Go 1.26。在源码目录构建，然后按平台使用可信本地二进制：

```sh
bash scripts/build.sh ./laowangbot

# Linux
sudo bash scripts/install.sh --binary "$PWD/laowangbot" --root /opt/laowangbot

# macOS
bash scripts/install.sh --binary "$PWD/laowangbot" --root "$HOME/laowangbot-data"
```

```powershell
# Windows PowerShell
go build -trimpath -o .\laowangbot.exe .\cmd\laowangbot
.\scripts\install.ps1 -Binary "$PWD\laowangbot.exe" -Root "$env:LOCALAPPDATA\laowangbot"
```

```sh
# Migration
bash scripts/install.sh --binary "$PWD/laowangbot" \
  --migrate /path/to/old-bot --from auto \
  --root "$HOME/laowangbot-data"
```

迁移前停止旧实例，目标目录必须为空；Linux 迁移同样需要 `sudo`，或添加 `--no-service`。新装与迁移示例二选一。

默认前缀为 `.`、`。`、`$`、`，`；迁移保留自定义前缀。不要同时运行共享同一会话的旧、新实例。

| 阅读目标 | 文档 |
|---|---|
| Linux / macOS / Windows / Docker 部署、更新与备份 | [安装指南](INSTALL.md) |
| 账号、代理、环境变量与优先级 | [配置说明](docs/configuration.md) |
| mibot-lite / MiBox / TeleBox 迁移范围与报告 | [迁移指南](docs/migration.md) |
| 插件协议、本地安装及远程更新 | [插件开发](docs/plugins.md) |
| 代码结构、开发和验证 | [架构说明](docs/architecture.md) |

## 命令概览

使用 `.help 命令` 查询参数。内建命令的运行时提示主要为中文；四种 README 语言不代表完整运行时国际化。

| 类别 | 命令 |
|---|---|
| 运行维护 | `ping` `status` `memory` `sysinfo` `version` / `ver` `help` / `h` `update` `restart` `log` `bf` |
| 配置与插件 | `prefix` `alias` `privacy` `tpm` |
| 查询工具 | `calc` `rate` `tr` `gt` `whois` `ip` `bin` `ids` `dc` `speedtest` / `st` |
| AI | `ai` `sum` |
| 消息与媒体 | `yvlu` `eatgif` `eat` `eat2` `sticker` `t` `ts` `tk` `re` `save` `dme` `da` `dav` |
| 群管理 | `ban` `unban` `kick` `mute` `unmute` `sb` `unsb` `refresh` `aban` |
| 账号与授权 | `acn` / `autochangename` `sudo` `sure` |

`.sudo` / `.sure` 按内建授权白名单处理，别名不能绕过权限检查；设置类子命令通常仅限本人。外部插件管理仅限账号本人，插件不是安全沙箱。`ffmpeg` 用于部分媒体与语音命令，未安装不影响无关命令。AI 等服务需自行配置，密钥请在收藏夹中设置。

## 开发与验证

```sh
go build ./...
go vet ./...
bash scripts/build.sh
```

测试与测试数据仅在本地保留，不随仓库发布；单元测试、集成测试及覆盖率检查须使用本地测试副本。仓库 CI 执行多平台构建和静态检查；配置存在不等于已经在远程运行。Windows、Linux 服务及 Docker 的真实部署、Telegram 真账号联调仍需相应环境验收；交叉编译或离线检查不证明运行时成功。不沿用上游内存数字作为本项目实测结果。

## 来源与许可

独立开发起点为 [MiCat-S/mibot-lite](https://github.com/MiCat-S/mibot-lite) 的 `dbc404a2323061c4abd7f13088622e1d045153fa`，本地工作分支 `refactor/laowangbot`。感谢原作者与贡献者；保留 [LGPL-2.1 许可证](LICENSE) 和原有归属。迁移兼容性以实际转换范围为准。

状态卡片字体来自 Noto Sans SC，按 [SIL OFL 1.1](internal/statuscard/NotoSansSC-OFL.txt) 分发。

## 插件管理（TPM）

从 0.1.8 起，`monitor`、`qdsg` 随官方程序内置，直接使用 `.monitor`、`.qdsg`；安装和升级无需本地 Go 编译。配置继续保存在 `state/monitor`、`state/qdsg`。本地 OCR 仍按需使用 Python 依赖。

`bh` 保号管家也随程序内置：使用 `.bh` 查看 EmbyBoss/自定义保号检测、定时预警、通知和配置导入导出，配置保存在 `state/bh`。

`pmcaptcha` 私聊验证也已内置，使用 `.pmc` 查看帮助。新安装默认关闭；`.pmc on` 启用规则，`.pmc captcha on` 开启验证码，配置与用户记录保存在 `state/pmcaptcha`。

TPM 源码安装、更新、卸载、导入和替换暂时禁用。`.tpm ls -v` 查看内置及外部插件，`.tpm s 关键词` 搜索，`.tpm ul 名称` 导出已有外部源码。内置插件通过 `.update run` 随主程序更新；已有手动源码插件仍保留原版本及源码更新路径。见[完整说明](docs/plugins.md)。

WebDAV 文件归档已内置，回复媒体发送 `.dav`；使用 `.dav config` 在收藏夹配置、`.dav test` 检查连接。旧配置与上传记录继续使用，详见 [WebDAV 说明](docs/webdav.md)。
