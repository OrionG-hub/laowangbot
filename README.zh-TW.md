# laowangbot

[简体中文](README.md) · [English](README.en.md) · [繁體中文](README.zh-TW.md) · [日本語](README.ja.md)

**Go 核心 · 編譯內建原始碼外掛 · 可遷移的 Telegram UserBot**

laowangbot 保留既有內建指令；核心不需要 Node.js。外掛以 Go 原始碼分發，安裝時編譯進主程式；部分媒體功能按需使用 ffmpeg。

## 目前狀態

專案發布於 `github.com/OrionG-hub/laowangbot`。官方 Release 提供各平台執行檔與校驗檔；尚未發布 Docker 映像，容器請從原始碼本機建置。

## 快速開始

### 全新安裝（需要登入）

首次安裝及執行官方二進位檔不需 Go 或 Node.js；管理原始碼外掛及原始碼升級需要 Go 和 Git。安裝器下載最新官方 Release、校驗 SHA-256，互動登入後啟動背景工作。Release 必須包含對應平台執行檔與 `checksums.txt`；尚未發布時請使用下方原始碼方式。

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

Linux 使用 systemd，macOS 使用目前使用者的 launchd 工作，Windows 使用使用者登入排程工作。安裝後在「已儲存訊息」傳送 `.ping`、`.help` 驗證連線；登入需要 Telegram API ID、API hash、手機號碼與驗證碼。完整參數請參閱 [安裝指南](INSTALL.md)。

### 從舊人形一鍵遷移（不需複製設定）

已有 mibot-lite、MiBox 或 TeleBox？請使用以下遷移命令，不要使用上方全新安裝命令。選擇舊人形與目錄後，自動搬移設定、會話及支援的資料；舊會話有效時不需重新輸入 API ID 或登入。

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

目標目錄必須為空，原目錄保留。Linux 遷移會停止並停用舊 systemd 服務的自動啟動，失敗恢復原狀；其他啟動器須先自行停止並停用自動啟動。若停在 `API ID:`，按 Ctrl+C 後改用以上命令。目標非空時先檢查內容，不要直接刪除。未知外掛只封存，程式碼仍需適配。詳見[遷移指南](docs/migration.md)。

### 原始碼安裝與遷移

原始碼建置需要 Go 1.26。在原始碼目錄建置，再依平台安裝可信本機執行檔：

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

遷移前停止舊實例，目標目錄必須為空；Linux 遷移同樣需要 `sudo`，或加入 `--no-service`。全新安裝與遷移範例擇一執行。

## 指令概覽

預設前綴為 `.`、`。`、`$`、`，`，遷移保留自訂前綴。使用 `.help 指令` 查詢詳情。執行時訊息主要為中文，四種 README 語言不代表完整執行時國際化。

| 類別 | 指令 |
|---|---|
| 執行維護 | `ping` `status` `memory` `sysinfo` `version` / `ver` `help` / `h` `update` `restart` `log` `bf` |
| 設定與外掛 | `prefix` `alias` `privacy` `tpm` |
| 查詢工具 | `calc` `rate` `tr` `gt` `whois` `ip` `bin` `ids` `dc` `speedtest` / `st` |
| AI | `ai` `sum` |
| 訊息與媒體 | `yvlu` `eatgif` `eat` `eat2` `sticker` `t` `ts` `tk` `re` `save` `dme` `da` `dav` |
| 群組管理 | `ban` `unban` `kick` `mute` `unmute` `sb` `unsb` `refresh` `aban` |
| 帳號與授權 | `acn` / `autochangename` `sudo` `sure` |

## 文件導覽

以下詳細文件目前以簡體中文提供。

- [安裝與部署](INSTALL.md): Linux / macOS / Windows / Docker
- [設定](docs/configuration.md): `LAOWANGBOT_*`, legacy `MIBOT_*`
- [遷移](docs/migration.md): mibot-lite / MiBox / TeleBox
- [外掛協定](docs/plugins.md): `.tpm`, local / remote
- [架構與開發](docs/architecture.md): Go, JSON, processes

遷移匯入已知設定並封存舊資源及外掛；未知 TypeScript 外掛需手動適配，不能直接執行。不要讓新舊實例同時使用相同會話。

## 驗證與來源

```sh
go build ./...
go vet ./...
```

測試與測試資料僅保留於本機，不隨儲存庫發布；單元測試、整合測試及覆蓋率檢查須使用本機測試副本。CI 執行建置與靜態檢查。CI 設定與交叉編譯不代表目標平台已成功執行；Linux、Windows、Docker 真實部署與 Telegram 真實帳號仍待驗收，不將上游記憶體數字當成本專案實測。

源自 [MiCat-S/mibot-lite](https://github.com/MiCat-S/mibot-lite) 的 `dbc404a2323061c4abd7f13088622e1d045153fa`，本機分支為 `refactor/laowangbot`。保留原作者歸屬與 [LGPL-2.1](LICENSE)。Noto Sans SC 字型子集依 [SIL OFL 1.1](internal/statuscard/NotoSansSC-OFL.txt) 發布。

## 外掛管理（TPM）

自 0.1.8 起，官方程式內建 `monitor`、`qdsg`，直接使用 `.monitor`、`.qdsg`，無需本機 Go 編譯。設定仍保留於 `state/monitor`、`state/qdsg`；本機 OCR 仍需選用的 Python 相依套件。

另內建 `bh` 保號管家，支援 EmbyBoss／自訂檢測、排程預警、Bot 通知與設定匯入匯出。使用 `.bh` 查看說明，設定儲存於 `state/bh`。

`pmcaptcha` 私聊驗證亦已內建，新安裝預設關閉。使用 `.pmc` 查看說明，`.pmc on` 啟用規則，`.pmc captcha on` 開啟驗證，設定與記錄儲存於 `state/pmcaptcha`。

TPM 原始碼安裝、更新、移除、匯入和替換暫時停用。`.tpm ls -v` 查看外掛，`.tpm s 關鍵詞` 搜尋，`.tpm ul 名稱` 匯出既有外部原始碼。內建外掛隨 `.update run` 更新主程式；既有自訂原始碼外掛保留原實作與原始碼更新方式。詳見[說明](docs/plugins.md)。

WebDAV 檔案歸檔已內建：回覆媒體傳送 `.dav`，在收藏夾用 `.dav config` 設定，`.dav test` 檢查連線。保留舊設定與上傳記錄，詳見 [WebDAV 說明](docs/webdav.md)。
