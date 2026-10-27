# 安装与运维

[返回首页](README.md) · [配置](docs/configuration.md) · [迁移](docs/migration.md) · [插件](docs/plugins.md)

项目已发布到 GitHub，官方 Release 提供各平台构件和 `checksums.txt`；Docker 镜像暂未发布，容器请从源码本地构建。下载式安装只适用于已上传对应 Release 的版本；核心运行无需 Go 或 Node.js，只有编译需要 Go 1.26。

## 官方一键安装

Release 已发布时，按平台执行一行命令：

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

命令要求对应 Release 构件和 `checksums.txt` 已上传；尚未发布或需要本地构建时，使用下方的 `--binary` / `-Binary` 安装方式。多账号部署时，为每个账号指定不同的 `--root` 和 `--service-name`（Windows 使用 `-Root` 和 `-ServiceName`）。

在终端运行时，安装器先询问“全新安装”或“从旧部署迁移”。全新安装检测到目标目录的 `config.json` 或 `gotd-session.json` 后提供两种选择：

- **新目录多开**：输入空的新目录，默认给出原目录旁未占用的 `account-2`，已存在则递增；服务名默认由新目录名生成，可自行修改。原账号继续运行，新账号使用独立配置和插件数据。
- **覆盖登录**：停止所选服务，使用目标目录内当前程序重新登录，保留 `data/`、`state/`、`plugins/` 和编译记录。仅账号会话被替换，旧账号的 `updates.json` 更新缓存会清除；登录、检查或恢复服务失败时回滚旧会话。覆盖登录不升级程序。

登录或迁移后可输入机器人显示名称，例如 `sakurabot`，与 `.rename` 共用 `data/brand.json`，最长 32 个字符，超长截取。回车保留当前设置，新安装默认 `laowangbot`。这是 `.status`、`.sysinfo` 等处的品牌名；Telegram 账号昵称、`app_name` 设备名称、可执行文件名和服务名各自独立。

只需升级程序并保留账号时，Unix 安装命令增加 `--non-interactive`，Windows 增加 `-NonInteractive`，跳过安装方式、换号和品牌名询问；必须已有可用配置。原 `--wizard` / `-Wizard` 仍直接进入迁移向导。

## 1. 构建与首次登录

在源码目录执行：

```sh
bash scripts/build.sh ./laowangbot
mkdir -p -m 700 "$HOME/laowangbot-data"
./laowangbot --login --root "$HOME/laowangbot-data"
./laowangbot --check --root "$HOME/laowangbot-data"
./laowangbot --supervise --root "$HOME/laowangbot-data"
```

API 凭据从 [my.telegram.org](https://my.telegram.org) 申请，登录交互输入手机号、验证码与可选两步验证密码。`--check` 不联网，只验证本地配置与会话。看到 `runtime.ready` 后，在收藏夹执行 `.ping`、`.help` 确认真实连接。

`--serve` 直接运行；`--supervise` 启动可移植监督进程，以支持重启和更新后的重新加载。部署目录与源码目录建议分开。

`.eat`、`.eat2` 和 `.eatgif` 生成动画需要系统安装 `ffmpeg`。Linux 可执行 `sudo apt-get install -y ffmpeg`，macOS 可执行 `brew install ffmpeg`；安装后重启 laowangbot 服务。Docker 默认镜像已包含 ffmpeg。

## 2. Linux（systemd）

在目标架构编译，或在构建机交叉编译：

```sh
GOOS=linux GOARCH=amd64 bash scripts/build.sh ./laowangbot-linux-amd64
sudo bash scripts/install.sh --binary "$PWD/laowangbot-linux-amd64" --root /opt/laowangbot
sudo systemctl status laowangbot
sudo journalctl -u laowangbot -f
```

ARM64 使用 `GOARCH=arm64`。系统服务安装需要 root 和 systemd；脚本交互登录后默认创建 `laowangbot.service`。多账号时指定不同的服务名：

```sh
sudo bash scripts/install.sh --binary "$PWD/laowangbot-linux-amd64" --root /opt/laowangbot/account-a --service-name laowangbot-a
sudo bash scripts/install.sh --binary "$PWD/laowangbot-linux-amd64" --root /opt/laowangbot/account-b --service-name laowangbot-b
sudo systemctl status laowangbot-a laowangbot-b
```

普通用户可加 `--no-service`，再手动运行 `--supervise`。服务名必须以字母、数字或 `_` 开头，只允许字母、数字、`.`、`_`、`-`，且不带 `.service` 后缀。

## 3. macOS（launchd）

```sh
bash scripts/build.sh ./laowangbot
bash scripts/install.sh --binary "$PWD/laowangbot" --root "$HOME/laowangbot-data"
launchctl print "gui/$(id -u)/io.github.laowangbot"
tail -f "$HOME/laowangbot-data/service.err.log"
```

不要用 sudo 安装用户 LaunchAgent。任务属于当前登录用户，默认写入 `~/Library/LaunchAgents/io.github.laowangbot.plist`；多账号时加 `--service-name account-a`，脚本会写入独立的 LaunchAgent。这是用户登录后的后台任务，并非无人登录时的系统服务。

## 4. Windows（PowerShell）

在源码目录用 Go 1.26 构建，再安装：

```powershell
go build -trimpath -o .\laowangbot.exe .\cmd\laowangbot
.\scripts\install.ps1 -Binary "$PWD\laowangbot.exe" -Root "$env:LOCALAPPDATA\laowangbot"
Get-ScheduledTask -TaskName laowangbot
```

直接 `go build` 的版本显示为 `dev`；正式构件用 `scripts/release.sh` 注入版本。安装器创建当前用户登录时运行的计划任务，任务运行 `--supervise`；多账号时为每个账号指定不同的 `-ServiceName`。这不是 Windows 系统服务；`-NoService` 只安装，随后手动启动。脚本执行权限遵循本机策略。

更新时在上述安装命令增加 `-NonInteractive`，安装器停止旧任务并确认可执行文件不再运行后替换。回滚使用：

```powershell
.\scripts\install.ps1 -Root "$env:LOCALAPPDATA\laowangbot" -ServiceName laowangbot -Rollback
```

多账号回滚时必须继续使用安装时的同一个 `-ServiceName`。

Windows 不支持 Telegram 内直接替换正在运行的程序；`.update run` 会拒绝，必须通过安装器停止、替换和启动。若手工启动过进程，先停止它。

## 5. 从现有部署安装

推荐使用迁移向导：Linux 执行 `sudo bash scripts/install.sh --wizard --root /opt/laowangbot`；macOS 执行 `bash scripts/install.sh --wizard --root "$HOME/laowangbot-data"`；Windows 使用 `-Wizard`。选择旧人形和目录后自动搬迁，无需复制配置。详见[同机迁移向导](docs/migration.md#同机一键迁移向导)。


停止旧实例，并选择一个空目标目录：

```sh
bash scripts/install.sh --binary "$PWD/laowangbot"   --migrate /path/to/old-bot --from telebox --root "$HOME/laowangbot-data"
```

`--from` 支持 `auto`、`mibot-lite`、`mibox`、`telebox`；Windows 对应 `-Migrate`、`-From`。迁移和恢复不能同时使用，详见[迁移指南](docs/migration.md)。

## 6. Docker（本地构建）

```sh
docker build -t laowangbot:local .
docker volume create laowangbot-data
docker run --rm -it -v laowangbot-data:/data laowangbot:local --login --root /data
docker run --rm -v laowangbot-data:/data laowangbot:local --check --root /data
docker run -d --name laowangbot --restart unless-stopped   -v laowangbot-data:/data laowangbot:local
```

`/data` 持久化账号与状态；默认镜像包含 ffmpeg，可用 `--build-arg WITH_FFMPEG=false` 省略。源码 Docker 构建当前显示 `dev` 版本。容器升级应重新构建镜像、停止并删除旧容器，再用同一数据卷启动；不要在容器内用 `.update run` 替代镜像管理。

## 7. 更新与回滚

本地更新可使用 `--binary PATH --non-interactive` / `-Binary PATH -NonInteractive`，保留账号配置并留存上一二进制。Linux/macOS 在有有效 Release 后可使用 `.update check`、`.update run`、`.update rollback`，需要校验文件和可重启运行方式。未发布时查询或下载可能失败，不代表已经存在可升级版本。

维护者执行 `bash scripts/release.sh vX.Y.Z` 会构建 Linux/macOS/Windows 的 amd64、arm64 构件及 `checksums.txt`，不会主动发布。发布工作流会在推送版本标签后构建并上传 Release 构件；下载式安装脚本校验 SHA-256。`--binary` 是用户明确选择的可信本地文件。Docker 目前没有公共镜像，使用源码构建。

安装成功仅表示本地配置检查及相应启动操作完成，仍需 `.ping` 确认 Telegram 可用。

## 8. 备份、恢复与联调

```sh
./laowangbot --backup /safe/backup.tar.gz --root /path/to/deployment
./laowangbot --restore /safe/backup.tar.gz --root /path/to/empty-destination
```

`.bf` 把命令和插件配置发到收藏夹。新备份标识为 `laowangbot-backup.json`，恢复兼容旧 `mibot-lite-backup.json`。备份仅含 `data/*.json`、`state/<插件>/*.json` 和 `state/pmcaptcha/legacy/*.json`（待合并的旧配置与记录），不含登录会话、`.env`、插件源码或缓存。在收藏夹回复 `.bf` 发出的备份文件发送 `.hf`，只恢复这些配置并自动重启服务，当前登录状态保持不变。

真实账号验证可在停止服务后执行 `./laowangbot --verify --root /path/to/deployment`。它会在收藏夹发送、读取并清理测试消息，依赖 Telegram 和第三方服务，不能代替离线测试。当前尚未完成 Linux/Windows/Docker 真实部署与 Telegram 凭据联调。

### 可选测速工具

Linux 可以按已有校验值下载 Ookla CLI；macOS/Windows 请先手动安装对应平台的 `speedtest` 并加入 PATH。部分系统资源指标仍只在 Linux 可用，其他系统显示未知或零值。

## yvlu 本地渲染

文字语录优先使用内嵌字体的 Go 本地渲染器，无需 Node.js。静态贴纸转 WebP 需要 ffmpeg（Linux：`sudo apt-get install -y ffmpeg`；macOS：`brew install ffmpeg`）；图片与故事卡直接生成 PNG。Unicode 表情图片按需从 Twemoji CDN 获取。

含媒体、剧透/引用格式、无法解码的自定义表情或本地转换失败时回退远端服务；此时标签仍可能不显示。TGS/视频表情状态优先使用静态缩略图，本地不输出动态状态。

## yvlu 动态贴纸

WebM 视频贴纸可直接嵌入语录，无需转码。TGS 动画需要 **ffmpeg、Python 3、rlottie-python 和 Pillow**，仅处理 TGS 时按需启动，不增加常驻 Node.js 运行时。

Linux 示例（依赖安装好后重启 laowangbot）：

```sh
sudo apt-get install -y ffmpeg python3 python3-venv
sudo python3 -m venv /opt/laowangbot-tgs
sudo /opt/laowangbot-tgs/bin/pip install rlottie-python==1.3.8 Pillow==12.3.0
sudo mkdir -p /etc/systemd/system/laowangbot.service.d
printf '[Service]\nEnvironment=LAOWANGBOT_PYTHON=/opt/laowangbot-tgs/bin/python\n' | sudo tee /etc/systemd/system/laowangbot.service.d/tgs.conf
sudo systemctl daemon-reload
sudo systemctl restart laowangbot
```

macOS/Windows 也可在独立 Python 虚拟环境中安装这两个包，并为运行 laowangbot 的进程设置 `LAOWANGBOT_PYTHON` 为对应 Python 可执行文件的绝对路径；ffmpeg 需在系统 PATH 中。此变量读取进程环境，单写入 laowangbot 的 `.env` 不生效。

Docker 按需启用：`docker build --build-arg WITH_TGS=true -t laowangbot:local .`。

TGS 经 PNG 帧保留透明度，再转为动态 WebM。缺依赖、下载失败或转换失败会明确报错，不会生成空白贴纸或静默替换为静态图。贴纸输入限制 8 MiB；TGS 压缩及解压 JSON 各最多 1 MiB，画布最大 512×512、时长最多 3 秒，输出采样最高 30 fps，不支持依赖外部图片资源的 Lottie。
