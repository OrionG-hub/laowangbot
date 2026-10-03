# laowangbot

[简体中文](README.md) · [English](README.en.md) · [繁體中文](README.zh-TW.md) · [日本語](README.ja.md)

**Go core · Compiled-in Go source plugins · Migratable Telegram UserBot**

laowangbot keeps the existing built-in commands without requiring Node.js in the core. Plugins ship as Go source and compile into the host during installation; media features use ffmpeg when needed.

## Status

The project is published at `github.com/OrionG-hub/laowangbot`. Official Releases provide platform binaries and checksums; no Docker image is published, so build the container locally from source.

## Quick start

### Fresh installation (login required)

Installing and running the official binary requires neither Go nor Node.js. Source plugin management and source upgrades require Go and Git. The installer downloads the latest official Release, verifies SHA-256, prompts for login and starts a background task. The Release must contain the platform binary and `checksums.txt`; use the source workflow below until these assets are published.

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

Linux uses systemd, macOS a per-user launchd job, and Windows a user logon scheduled task. Send `.ping` and `.help` in Saved Messages after installation to verify connectivity. Login requires your Telegram API ID, API hash, phone number and verification code. See [INSTALL.md](INSTALL.md) for all options.

### One-command migration from an existing bot

Already running mibot-lite, MiBox or TeleBox? Use this migration command instead of the fresh installer above. Select the old bot and directory; configuration, session and supported data are transferred automatically. A valid existing session requires no new API ID entry or login.

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

Use an empty destination. Linux migration stops and disables the old systemd service, restoring its original state on failure. For other launchers, stop the old instance and disable automatic startup first. The source directory is preserved. If you are at `API ID:`, press Ctrl+C and use the commands above. Inspect a nonempty destination instead of deleting it. Unknown plugins are archived and still require code adaptation. See the [migration guide](docs/migration.md).

### Source installation and migration

Source builds require Go 1.26. From the source directory, build and install a trusted local binary:

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

Stop the old bot before migration and use an empty destination. On Linux, prefix migration with `sudo` or add `--no-service`. Choose either a fresh install or migration.

## Commands

Default prefixes are `.`, `。`, `$`, and `，`; migration preserves custom prefixes. Use `.help command` for details. Runtime messages are mostly Chinese; four README translations do not imply complete runtime localization.

| Category | Commands |
|---|---|
| Operations | `ping` `status` `memory` `sysinfo` `version` / `ver` `help` / `h` `update` `restart` `log` `bf` |
| Configuration / plugins | `prefix` `alias` `privacy` `tpm` |
| Utilities | `calc` `rate` `tr` `gt` `whois` `ip` `bin` `ids` `dc` `speedtest` / `st` |
| AI | `ai` `sum` |
| Messages / media | `yvlu` `eatgif` `eat` `eat2` `sticker` `t` `ts` `tk` `re` `save` `dme` `da` `dav` |
| Moderation | `ban` `unban` `kick` `mute` `unmute` `sb` `unsb` `refresh` `aban` |
| Account / delegation | `acn` / `autochangename` `sudo` `sure` |

## Documentation

Detailed references below are currently in Simplified Chinese.

- [Deployment](INSTALL.md): Linux / macOS / Windows / Docker
- [Configuration](docs/configuration.md): `LAOWANGBOT_*`, legacy `MIBOT_*`
- [Migration](docs/migration.md): mibot-lite / MiBox / TeleBox
- [Plugin protocol](docs/plugins.md): `.tpm`, local / remote
- [Architecture and development](docs/architecture.md): Go, JSON, processes

Migration imports known configuration and archives old assets/plugins. Unknown TypeScript plugins require manual adaptation; they are not automatically executable. Do not run old and new instances with the same session at once.

## Validation and provenance

```sh
go build ./...
go vet ./...
```

Tests and fixtures are kept locally and are not distributed in this repository. Unit tests, integration tests and coverage checks require a local test copy; CI runs builds and static checks. CI definitions and cross-compilation do not prove target-platform runtime success. Live Linux/Windows/Docker deployment and real Telegram account checks still need validation; no upstream memory figures are claimed as current measurements.

Derived from [MiCat-S/mibot-lite](https://github.com/MiCat-S/mibot-lite), commit `dbc404a2323061c4abd7f13088622e1d045153fa`, on local branch `refactor/laowangbot`. Original attribution and [LGPL-2.1](LICENSE) are retained. The Noto Sans SC subset uses [SIL OFL 1.1](internal/statuscard/NotoSansSC-OFL.txt).

## Plugin manager (TPM)

Since 0.1.8, official binaries include `monitor` and `qdsg`. Use `.monitor` and `.qdsg` directly, with no local Go compilation. State remains in `state/monitor` and `state/qdsg`; local OCR still needs its optional Python dependencies.

The built-in `bh` account-expiry monitor supports EmbyBoss/custom checks, scheduled alerts, Bot notifications, and configuration import/export. Use `.bh` for help; state is stored in `state/bh`.

The built-in `pmcaptcha` private-chat verifier is disabled by default on new installations. Use `.pmc` for help, `.pmc on` to enable rules, and `.pmc captcha on` to enable challenges. State is stored in `state/pmcaptcha`.

TPM source installation, updates, removal, import and replacement are temporarily disabled. Use `.tpm ls -v` to inspect plugins, `.tpm s keyword` to search, and `.tpm ul name` to export existing external source. Bundled plugins update with `.update run`; existing custom source plugins retain their implementation and source-update path. See the [guide](docs/plugins.md).

WebDAV archiving is built in: reply to media with `.dav`, configure it in Saved Messages using `.dav config`, and check access with `.dav test`. Existing settings and records are preserved. See [WebDAV](docs/webdav.md) (Chinese).
