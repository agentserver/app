# 星池指挥官自动更新设计

**Date**: 2026-06-12
**Status**: Design approved; written spec pending user review
**Scope**: Completed-console mode adds manual update checks, automatic update checks, user-confirmed installer download and launch, preserved login/state data, and automatic restart of local slaves after an app update.

## Motivation

星池指挥官目前只能通过用户手动获取新版安装包并重新运行来覆盖安装。这个流程对非专业用户不够明显，也无法在客户端里提醒新版本。

新增自动更新能力后，用户应能在常驻控制台里手动检查更新；应用也应在后台定期检查更新。有新版时必须先让用户确认，不能静默安装。安装包从 `assets.agent.cs.ac.cn` 拉取，下载后必须校验哈希，再启动安装器完成覆盖安装。

更新过程不应删除用户登录信息，包括 modelserver API key / refresh token、agentserver workspace key / tunnel token、本地 state、machine identity、slave registry、slave config 和 Codex 配置。更新完成后，之前正在运行或等待认证的本地 slave 应自动启动或重启。

## Goals

- 支持手动检查更新。
- 支持自动检查更新。
- 检查到新版后必须由用户确认才下载安装。
- 从固定 HTTPS manifest 拉取更新信息：

  ```text
  https://assets.agent.cs.ac.cn/agentserver-app/windows/latest.json
  ```

- 从 manifest 指定的安装包 URL 下载 Windows 安装包。
- 下载完成后校验 manifest 中的 SHA-256，校验失败拒绝运行安装包。
- 启动现有 Windows 安装器进行覆盖安装。
- 覆盖安装不得调用卸载流程，不得删除 `~/.agentserver-app`、keyring/secrets、modelserver 登录信息、agentserver 登录信息、slave registry 或 slave config。
- 更新前记录需要恢复的 local slaves；更新后自动启动/重启这些 slaves。
- 更新状态和错误在 Dashboard 可见。
- Linux/macOS 开发环境下保持构建和测试可运行，Windows 特有安装启动能力以平台文件隔离。

## Non-Goals

- 不做完全静默安装。即使自动检查发现新版，也不自动下载安装。
- 不实现二进制差分更新。
- 不新增长期运行的独立 Windows Service。
- 不支持降级安装。
- 不改变 Codex runtime 的 pinned manifest 机制；应用更新和 Codex runtime 更新是两个独立流程。
- 不让远端 manifest 覆盖客户端的安全策略。

## Update Manifest

Asset storage and publishing rules are defined in `docs/superpowers/specs/2026-06-13-update-assets-protocol.md`.

客户端只请求一个固定 manifest：

```text
https://assets.agent.cs.ac.cn/agentserver-app/windows/latest.json
```

manifest JSON:

```json
{
  "version": "0.1.2",
  "url": "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
  "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "size": 123456789,
  "notes": "修复若干安装和启动问题。"
}
```

Fields:

- `version`: SemVer 版本号，必须大于当前应用版本才认为有更新。允许可选 `v` 前缀，比较时忽略前缀。
- `url`: 安装包 URL。必须是 HTTPS，host 必须是 `assets.agent.cs.ac.cn`。
- `sha256`: 安装包 SHA-256 hex，必须为 64 个十六进制字符。
- `size`: 可选。大于 0 时用于进度展示和下载完成后的长度校验。
- `notes`: 可选。显示在 Dashboard 的更新提示里。

安全约束：

- manifest URL 固定在客户端，不从用户输入或远端配置读取。
- 安装包 URL 不允许跳出 `assets.agent.cs.ac.cn`。
- 缺少 `sha256` 或 `sha256` 格式错误时，更新检查失败。
- 下载后的文件哈希不匹配时删除临时文件，并显示错误。
- 当前版本大于等于 manifest 版本时显示“已是最新版本”。

## Version Source

新增 Go 侧版本常量，例如 `internal/appversion.Version = "0.1.1"`。Updater、Dashboard 状态和测试都从这个常量读取当前版本。

Windows 打包脚本中的版本值仍需在发版时同步更新：

- `packaging/windows/installer.iss`
- `packaging/windows/install.ps1`
- `scripts/package-windows-zip.sh`
- `extensions/agentserver-app/package.json`
- `test/e2e/windows/e2e_test.go`

本功能不尝试自动重写这些文件，但会把 Go runtime 当前版本集中化，避免 updater 再硬编码版本。

## Product Behavior

### Manual Check

Dashboard 顶部操作区增加“检查更新”按钮。

点击后：

1. 调用本地 API 检查 manifest。
2. 显示当前版本、最新版本、检查时间和说明。
3. 如果没有新版，显示“已是最新版本”。
4. 如果有新版，显示“下载并安装”按钮。
5. 用户点击“下载并安装”时弹出确认：

   ```text
   将下载并启动星池指挥官 0.1.2 安装器。安装过程中星池指挥官会短暂关闭，登录信息和已创建的本地智能体会保留。是否继续？
   ```

6. 用户确认后开始下载、校验、启动安装器。

### Automatic Check

completed console 启动后自动检查一次，延迟 30 秒执行，避免影响控制台启动。

之后每 24 小时最多检查一次。检查节流状态保存到 `~/.agentserver-app/update-state.json`，避免每次点击桌面图标都打远端。

自动检查只更新本地 update state 和 Dashboard 提示，不自动下载，也不自动弹系统模态框。用户打开 Dashboard 时可以看到“发现新版本”，再手动确认安装。

自动检查失败只记录错误，不打断启动，不影响托盘、前端启动或 slave 管理。

### Install Flow

用户确认安装后：

1. Updater 记录本次更新前需要恢复的 slave IDs。
2. 下载安装包到：

   ```text
   ~/.agentserver-app/cache/updates/agentserver-app-<version>-setup.exe
   ```

3. 先写 `.part` 文件，下载完成并校验 SHA-256 后 rename 为最终路径。
4. 启动安装包进程。
5. 返回 API 响应：`{"state":"started"}`。
6. 安装器会停止旧的 app 进程并覆盖 `%LOCALAPPDATA%\Programs\agentserver-app` 中的文件。
7. 安装器完成后由用户或安装器 postinstall 启动新版 `launcher.exe`。

启动安装器时不传 `--uninstall`，不调用 `uninstall.exe`，不删除用户状态目录。

### State Preservation

现有覆盖安装脚本应继续只覆盖安装目录文件：

```text
%LOCALAPPDATA%\Programs\agentserver-app
```

不得删除：

```text
~/.agentserver-app/state.json
~/.agentserver-app/secrets.json
~/.agentserver-app/machine.json
~/.agentserver-app/slaves.json
~/.agentserver-app/slaves/**
~/.codex/config.toml
~/.codex/.codex-global-state.json
~/.config/multi-agent/driver.yaml
```

不得删除 keyring 中的：

- `modelserver_api_key`
- `modelserver_refresh_token`
- `modelserver_access_token_expires_at`
- `agentserver_ws_api_key`
- `agentserver_tunnel_token`

这些 key 只允许 `logout` 或 `uninstall` 明确删除。

## Slave Restore

更新安装器会关闭旧的 `slave-agent.exe`。为保证用户已有远程能力恢复，安装前记录需要恢复的 slave IDs。

记录文件：

```text
~/.agentserver-app/pending-slave-restarts.json
```

格式：

```json
{
  "reason": "app_update",
  "version": "0.1.2",
  "created_at": "2026-06-12T12:00:00Z",
  "slave_ids": ["sl-1", "sl-2"]
}
```

记录规则：

- `running`: 记录。
- `starting`: 记录。
- `auth_required`: 记录。
- `paused`: 不记录，尊重用户暂停意图。
- `stopped`: 不记录。
- `error`: 不记录，避免更新后自动启动已有错误的配置。

新版 completed console 启动时：

1. 读取 `pending-slave-restarts.json`。
2. 对每个 ID 调用 existing `slave.Manager.Restart(ctx, id)`。
3. 成功或 `os.ErrNotExist` 都继续处理其他 slave。
4. 处理完成后删除 pending 文件。
5. 失败的 slave 保留在 registry 中的 `error` 状态，并在 Dashboard local slave 列表展示错误。

如果安装后用户没有立即启动 launcher，pending 文件保留到下一次启动。

## Architecture

### `internal/updater`

新增包负责更新核心逻辑：

- `Manifest`: 解析远端 JSON。
- `Client.Check(ctx)`: 拉取 manifest，校验字段，比较版本，返回 update availability。
- `Client.Download(ctx, manifest, progress)`: 下载 `.part` 文件，校验 size 和 SHA-256，rename 到 cache。
- `Client.StartInstaller(ctx, path)`: 启动安装包。Windows 使用 `exec.Command(path)` 并隐藏窗口；非 Windows 返回清晰错误。
- `StateStore`: 读写 `update-state.json`，保存 last check、available update、install status、last error。
- `VersionCompare`: 只支持 SemVer 数字段比较，忽略 `v` 前缀，支持 `0.1.2` 与 `0.1.10` 正确排序。

`internal/updater` 不依赖 UI、console 或 slave。它只处理网络、文件、版本和安装器启动。

### Console Controller

`console.Controller` 增加 update 方法，协调 updater 与 slave restore：

- `UpdateState(ctx)`: 返回当前本地 update state。
- `CheckUpdate(ctx, automatic bool)`: 拉 manifest，更新 state。
- `InstallUpdate(ctx)`: 确认已有可安装 update，记录 pending slave restarts，下载并启动安装器。

`console.Deps` 增加：

- `Updates *updater.Service`
- `Slaves *slave.Manager` 已存在，用于记录需要恢复的 slaves。

### HTTP API

新增 console API：

- `GET /api/console/update`
  - 返回当前版本、last check、available update、状态和错误。
- `POST /api/console/update/check`
  - 手动检查更新。
- `POST /api/console/update/install`
  - 下载、校验并启动安装包。

所有 POST mutation 沿用 `requireTrustedConsoleMutation`。

响应示例：

```json
{
  "current_version": "0.1.1",
  "last_checked_at": "2026-06-12T12:00:00Z",
  "status": "available",
  "update": {
    "version": "0.1.2",
    "notes": "修复若干安装和启动问题。"
  },
  "last_error": ""
}
```

状态枚举：

- `idle`
- `checking`
- `latest`
- `available`
- `downloading`
- `ready`
- `installer_started`
- `error`

### Completed Launcher

completed console 创建时：

1. 构造 updater service：
   - manifest URL 固定为 `https://assets.agent.cs.ac.cn/agentserver-app/windows/latest.json`
   - cache dir 为 `~/.agentserver-app/cache/updates`
   - state path 为 `~/.agentserver-app/update-state.json`
   - current version 为 `internal/appversion.Version`
2. 启动后延迟 30 秒执行自动检查。
3. 启动后调用 slave restore helper 处理 `pending-slave-restarts.json`。
4. 自动检查和 slave restore 的错误只写日志和 state，不阻塞控制台启动。

### Dashboard

Dashboard 增加一个更新区域，放在顶部 action 附近或连接信息区之前：

- 当前版本。
- 最近检查时间。
- 新版本可用时显示版本和 release notes。
- “检查更新”按钮。
- “下载并安装”按钮，仅在有新版时显示。
- 下载/安装错误以现有 `el-alert` 风格展示。

UI 不新增复杂向导，不使用营销式页面。用户安装确认使用浏览器 `window.confirm`，保持与现有 logout/delete 确认一致。

### Tray

托盘不弹安装确认。自动检查发现新版后，可以在 tooltip 或菜单中显示“有新版本”，但安装入口仍以 Dashboard 为主。

首版实现可只更新 Dashboard，不要求托盘菜单增加更新项。

## Error Handling

- manifest 请求失败：记录 `last_error`，状态 `error`；保留上一次可用 update 信息，但标记本次检查失败时间。
- manifest JSON 无效：状态 `error`。
- version 不合法：状态 `error`。
- URL host 不允许：状态 `error`。
- SHA-256 缺失或格式错误：状态 `error`。
- 下载中断：删除 `.part`，状态 `error`。
- size 不匹配：删除 `.part`，状态 `error`。
- SHA-256 不匹配：删除下载文件，状态 `error`。
- 启动安装器失败：状态 `error`。
- 记录 pending slave restarts 失败：拒绝启动安装，状态 `error`，避免更新后无法恢复用户正在运行的 slaves。

## Testing Strategy

### Go Unit Tests

`internal/updater`:

- Parses valid manifest.
- Rejects missing sha256.
- Rejects non-HTTPS URL.
- Rejects URL host outside `assets.agent.cs.ac.cn`.
- Compares `0.1.10` greater than `0.1.2`.
- Reports latest when manifest version equals current.
- Reports available when manifest version is greater.
- Downloads through a test HTTP server, verifies SHA-256, renames final file.
- Deletes `.part` on failed hash.

`internal/console`:

- Manual check calls updater and persists available update.
- Install records only running/starting/auth_required slave IDs.
- Install does not record paused/stopped/error slaves.
- Install refuses to start if pending slave restart record cannot be written.

`internal/ui`:

- `GET /api/console/update` returns update state.
- `POST /api/console/update/check` requires trusted mutation and calls controller.
- `POST /api/console/update/install` requires trusted mutation and calls controller.

`cmd/launcher`:

- completed console wires updater with fixed manifest URL.
- startup triggers auto-check asynchronously.
- startup restores pending slave restarts without blocking server startup.

### Frontend Tests

`Dashboard.spec.ts`:

- Renders current version and “检查更新”.
- Manual check calls API and displays latest state.
- Available update displays “下载并安装”.
- Install asks `window.confirm` before calling API.
- Cancelled confirm does not call install API.
- Install errors are displayed.

### Verification Commands

Minimum local verification:

```bash
go test ./...
cd internal/ui/web && npm test
```

Windows smoke verification when available:

1. Install version `0.1.1`.
2. Complete onboarding or seed completed state and secrets.
3. Create a local slave and wait for running/auth_required.
4. Serve a fake `latest.json` pointing to a signed/test setup exe or use staging assets.
5. Click Dashboard “检查更新”.
6. Confirm install.
7. Verify installed version updates in Apps & Features.
8. Verify modelserver and agentserver remain connected.
9. Verify previous running/auth_required slave is started again or shows an actionable error.

## Release Asset Contract

Release automation must publish:

```text
https://assets.agent.cs.ac.cn/agentserver-app/windows/latest.json
https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-<version>-setup.exe
```

`latest.json` must be updated only after the setup exe is fully uploaded and its SHA-256 is known.

The setup exe should be the existing Inno output:

```text
agentserver-app-<version>-setup.exe
```

The portable zip is not used by the automatic updater in this design.

## Operational Assumptions

- `assets.agent.cs.ac.cn` serves `latest.json` and setup exe with HTTPS.
- The setup exe is trusted by Windows SmartScreen policy used for production releases.
- Existing Inno install behavior remains an in-place upgrade and does not invoke uninstall cleanup.
- Postinstall launch behavior remains enabled for interactive installs, so a user who confirms the installer can return to the Dashboard after install completes.
