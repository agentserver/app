# Codex Desktop 默认 winget 安装模式

**Date**: 2026-06-07
**Status**: Design approved; written spec pending user review
**Scope**: Windows x64 安装包新增 Codex Desktop winget 安装与向导模式切换。默认安装
Codex Desktop；用户选择 `极简风` 时才安装并配置简化 VS Code。

## Motivation

当前安装包默认安装 VS Code，并通过 `agentserver-vscode` 扩展把 VS Code 裁剪成
面向大众用户的极简界面。这个方向可以复用成熟编辑器，但 VS Code 仍然会暴露大量
专业开发工具概念。用户已经确认新的默认体验应改为 Codex Desktop，只有明确选择
`极简风` 时才走 VS Code 简化界面。

新默认路径要满足：

- 默认通过 `winget install Codex -s msstore` 安装 Codex Desktop。
- 安装时默认选择 Codex Desktop。
- 安装器仍保留 `极简风` 选择，选择后安装简化 VS Code。
- 桌面快捷方式仍打开 `星池指挥官` 的配置向导。
- 配置向导根据安装模式切换为 Codex Desktop 或 VS Code 相关步骤。
- Codex Desktop 模式不显示 `配置 VS Code 与 codex` 这类文案，也不安装 VS Code 扩展。

官方 Codex 文档给出的 Windows 安装命令是 `winget install Codex -s msstore`，并确认
Codex app、CLI、IDE extension 共用配置层，用户级配置位于 `~/.codex/config.toml`，
Codex Desktop 也注册 `codex://` URL scheme。因此默认模式可以复用现有
`internal/codex/config.go` 写入 modelserver provider 配置，并通过深链打开
Codex Desktop。

## Product Decision

采用一个安装包、两种前端模式：

| 模式 | 默认 | 安装内容 | 向导语义 | 启动结果 |
|---|---:|---|---|---|
| Codex Desktop | 是 | winget 安装的 Codex Desktop、launcher、onboarding、codex 配置工具 | 安装和配置 Codex Desktop | 打开 Codex Desktop 新会话，优先携带文件夹路径 |
| 极简风 VS Code | 否 | VS Code 离线包、VSIX、launcher、onboarding、codex.exe | 安装和配置极简 VS Code | 打开简化 VS Code 工作区 |

不拆成两个安装包。一个安装包更符合用户选择逻辑，也避免后续测试机验证时需要判断拿
哪个包。`极简风` 是安装器里的显式选项，默认不勾选。

## Installer UX

### Inno Setup

`packaging/windows/installer.iss` 新增一个任务选项：

- 用户可见名：`极简风界面（安装简化 VS Code）`
- 默认状态：不选中
- 选中后：运行 VS Code 安装脚本并写入 `minimal_vscode` 模式
- 未选中：运行 Codex Desktop winget 安装脚本并写入 `codex_desktop` 模式

安装器仍创建 `星池指挥官` 桌面快捷方式，快捷方式目标保持 `{app}\launcher.exe`。
启动后由 launcher/onboarding 根据持久化模式决定显示哪套向导。

### Bundled Payloads

安装包内置：

- `vscode-installer.exe`：保留给 `极简风` 模式。
- `agentserver-vscode.vsix`：只在 `极简风` 模式使用。
- `codex.exe`：继续保留给 VS Code 模式和必要的辅助命令；Codex Desktop 模式不依赖
  它作为主要前端。

安装包不再内置 Codex Desktop 离线包。Codex Desktop 默认安装依赖 Windows 上的
`winget`、Microsoft Store source 和网络访问。安装失败时给出明确错误，不默默回退到
VS Code。

### Install Mode Persistence

安装器和 portable 安装脚本写入同一个安装模式值：

```json
{
  "frontend_mode": "codex_desktop"
}
```

落地位置固定为两处：

- 安装目录：`{app}\install-mode.json`，安装器和 portable 脚本负责写入。
- 用户状态：`~/.agentserver-vscode/state.json`，launcher 首次启动时从
  `install-mode.json` 复制到 state，后续 `/api/state` 从 state 返回。

如果重装后 `install-mode.json` 与 state 中的模式不一致，launcher 使用
`install-mode.json` 覆盖 state。这样重装可以切换模式，同时保留 state 作为向导 API
的单一读视图。

合法值只有：

- `codex_desktop`
- `minimal_vscode`

缺失或非法值按 `codex_desktop` 处理，因为这是新的默认路径。

## Onboarding UX

### Codex Desktop Mode

默认模式的步骤为：

```ts
[
  { id: 'modelserver_login',       label: '登录 modelserver',       kind: 'oauth',    autoStart: false },
  { id: 'agentserver_login',       label: '登录 agentserver',       kind: 'oauth',    autoStart: false },
  { id: 'codex_desktop_install',   label: '安装 Codex Desktop',     kind: 'progress', autoStart: true  },
  { id: 'codex_desktop_configure', label: '配置 Codex Desktop',     kind: 'action',   autoStart: true  },
  { id: 'finalize',                label: '完成配置',              kind: 'action',   autoStart: false },
]
```

文案重点是 Codex Desktop 和共享 Codex 配置，不出现 `VS Code`、`扩展`、`终端` 等默认
模式不需要的概念。

完成页按钮改为 `打开 Codex Desktop`，点击后调用统一启动 API。启动时：

1. 优先打开 `codex://threads/new` 深链。
2. 如果 launcher 有当前文件夹路径，优先把路径作为深链参数传入。
3. 如果当前 Windows 版 Codex Desktop 不支持路径参数，退化为只打开新会话。
4. 打开成功后关闭 onboarding HTTP server。

### Minimal VS Code Mode

`极简风` 模式保留现有步骤，但名称继续面向普通用户：

```ts
[
  { id: 'modelserver_login',  label: '登录 modelserver',       kind: 'oauth',    autoStart: false },
  { id: 'agentserver_login',  label: '登录 agentserver',       kind: 'oauth',    autoStart: false },
  { id: 'vscode_install',     label: '安装极简界面',           kind: 'progress', autoStart: true  },
  { id: 'vscode_configure',   label: '准备极简界面',           kind: 'action',   autoStart: true  },
  { id: 'finalize',           label: '完成配置',              kind: 'action',   autoStart: false },
]
```

后端仍走 `EnsureVSCode`、`ConfigureVSCode`、扩展安装和 VS Code 启动逻辑。

### Frontend State

`/api/state` 增加：

```json
{
  "frontend_mode": "codex_desktop",
  "frontend_name": "Codex Desktop"
}
```

前端不再导出一份固定 `STEPS`。它根据 `frontend_mode` 选择 Codex Desktop 或
Minimal VS Code 的步骤表，并把 completed token 映射到对应 step id。

Completed token 映射：

| Server token | Codex Desktop step | Minimal VS Code step |
|---|---|---|
| `modelserver_login` | `modelserver_login` | `modelserver_login` |
| `agentserver_login` | `agentserver_login` | `agentserver_login` |
| `codex_desktop_installed` | `codex_desktop_install` | none |
| `codex_desktop_configured` | `codex_desktop_configure` | none |
| `vscode_installed` | none | `vscode_install` |
| `vscode_configured` | none | `vscode_configure` |
| `shortcuts_created` | `finalize` | `finalize` |

## Backend Architecture

### Orchestrator Interface

现有接口写死了 VS Code：

```go
EnsureVSCode(ctx, progress)
ConfigureVSCode(ctx)
LaunchAndShutdown(ctx)
```

新增模式感知方法，保留旧方法给 VS Code 实现复用：

```go
FrontendMode(ctx context.Context) (FrontendMode, error)
EnsureFrontend(ctx context.Context, progress chan<- ProgressEvent) error
ConfigureFrontend(ctx context.Context) error
LaunchAndShutdown(ctx context.Context) error
```

`EnsureFrontend` 根据 mode 分发：

- `codex_desktop` -> `EnsureCodexDesktop`
- `minimal_vscode` -> `EnsureVSCode`

`ConfigureFrontend` 根据 mode 分发：

- `codex_desktop` -> `ConfigureCodexDesktop`
- `minimal_vscode` -> `ConfigureVSCode`

服务器 API 改成通用路径：

- `POST /api/step/frontend_install`
- `POST /api/step/frontend_configure`
- `POST /api/launch`

旧路径 `/api/step/vscode_install`、`/api/step/vscode_configure`、`/api/launch-vscode`
只作为兼容 wrapper 保留，内部调用通用方法。新前端只调用通用路径。

### Codex Desktop Package

新增 `internal/codexdesktop` 包，职责边界类似 `internal/vscode`：

- `detect.go`：检测 Codex Desktop 是否已安装。
- `install.go`：通过 winget 安装 Codex Desktop。
- `launch.go`：打开 `codex://threads/new` 深链。
- `winget.go`：封装 `winget install Codex -s msstore` 命令、非交互参数和错误解析。

Windows 检测顺序：

1. 检查 Windows App execution alias 或已知安装入口。
2. 检查 `codex://` URL scheme 是否注册。
3. 必要时使用 PowerShell 查询 AppX package。

检测成功后写入 state：

```json
{
  "codex_desktop": {
    "installed": true,
    "version": "detected-version-if-available",
    "installed_by_us": true
  }
}
```

如果版本号无法稳定获取，允许为空，但 `installed` 必须准确。

安装命令固定为：

```powershell
winget install Codex -s msstore --accept-source-agreements --accept-package-agreements
```

如果 `winget` 当前版本支持 `--silent` 且 Codex package 接受静默安装，脚本可以附加
`--silent`。如果 `--silent` 导致 package 不兼容错误，回退到不带 `--silent` 的同一
winget 命令。脚本不能启动 Microsoft Store 图形界面等待用户手动点击安装。

### Codex Desktop Configure

`ConfigureCodexDesktop` 做这些事：

1. 写入或合并 `~/.codex/config.toml`，复用 `codex.ModelserverSettings()`。
2. 从 secrets 中读取 `modelserver_api_key`，持久化 `OPENAI_API_KEY`，并设置当前进程环境。
3. 启动 `token-refresher.exe`。
4. 不写 VS Code `settings.json`。
5. 不安装 VS Code 语言包。
6. 不安装 `agentserver-vscode.vsix`。
7. 标记 `codex_desktop_configured` 完成。

这保证默认模式的 “配置 Codex Desktop” 不会卡在 VS Code 扩展安装或 VS Code 检测上。

### Launch Behavior

`LaunchAndShutdown` 按 mode 分发：

- Codex Desktop：打开 deep link，成功后触发 `Shutdown`。
- Minimal VS Code：保持当前 VS Code launch args 和环境变量注入逻辑。

Codex Desktop deep link 构造：

```text
codex://threads/new
codex://threads/new?path=<url-encoded-folder>
```

Windows 对路径参数支持如果不稳定，失败时只打开 `codex://threads/new`。这属于产品级退化，
不是安装失败。

## Packaging Scripts

构建脚本不下载 Codex Desktop payload，也不生成 Codex Desktop manifest。Inno Setup
只需要打入 `ensure-codex-desktop.ps1`，该脚本负责检测 Codex Desktop 并在需要时运行
winget。

Portable 安装脚本 `packaging/windows/install.ps1` 也要支持相同模式：

- 默认运行 `ensure-codex-desktop.ps1`。
- 指定 `-MinimalVSCode` 时运行 `ensure-vscode.ps1`。
- 写入同样的 install mode 文件。

## Error Handling

### Installer

- `winget` 不存在：安装失败并提示用户安装或更新 Windows App Installer / Windows
  Package Manager。
- Microsoft Store source 不可用：安装失败并提示检查 Store 源、网络或企业策略。
- Codex package 安装退出非零：显示 winget 错误，不切换到 VS Code。
- 用户选择 `极简风` 时 VS Code 安装失败：沿用 VS Code 错误路径。

### Onboarding

- mode 缺失或非法：按 `codex_desktop` 继续。
- Codex Desktop 已安装：`安装 Codex Desktop` 步骤快速成功，并显示“已检测到 Codex Desktop”。
- Codex Desktop 未安装：`安装 Codex Desktop` 步骤运行 winget，并显示检测、安装、验证
  三个阶段的进度文案。
- 配置失败：显示具体错误，允许重试。
- 打开 deep link 失败：显示“打开 Codex Desktop 失败”，不清空已完成配置。

## Tests

### Go Unit Tests

- state 默认 mode 缺失时返回 `codex_desktop`。
- `/api/state` 包含 `frontend_mode`。
- `EnsureFrontend` 在 `codex_desktop` 下调用 winget installer，不调用 VS Code。
- winget installer 构造 `winget install Codex -s msstore` 命令，并添加 agreement 参数。
- winget installer 在 `winget` 缺失时返回用户可读错误。
- `ConfigureCodexDesktop` 写 `~/.codex/config.toml`、持久化 `OPENAI_API_KEY`、标记
  `codex_desktop_configured`。
- `ConfigureCodexDesktop` 不调用 VS Code extension installer。
- `LaunchAndShutdown` 在 `codex_desktop` 下打开 `codex://threads/new`。
- `minimal_vscode` 仍映射到当前 VS Code install/configure/launch。

### Frontend Tests

- `codex_desktop` state 渲染 Codex Desktop 步骤。
- `minimal_vscode` state 渲染极简 VS Code 步骤。
- completed token 在两种模式下映射正确。
- 完成页按钮在 Codex Desktop 模式显示 `打开 Codex Desktop`。
- 完成页按钮在 Minimal VS Code 模式显示极简界面相关文案。

### Packaging Tests

- `installer.iss` 包含 `ensure-codex-desktop.ps1`。
- `installer.iss` 默认运行 `ensure-codex-desktop.ps1`。
- `installer.iss` 只有选择 `极简风` 时运行 `ensure-vscode.ps1`。
- `install.ps1` 默认写入 `codex_desktop`。
- `install.ps1 -MinimalVSCode` 写入 `minimal_vscode`。
- 构建脚本不下载 Codex Desktop payload。

### Windows E2E

默认模式：

1. 卸载测试机相关内容。
2. 安装新 EXE，不选择 `极简风`。
3. 确认安装器通过 `winget install Codex -s msstore` 安装 Codex Desktop。
4. 双击 `星池指挥官`。
5. 向导显示 `安装 Codex Desktop` 和 `配置 Codex Desktop`。
6. 完成后点击 `打开 Codex Desktop`。
7. 确认没有进入 VS Code 安装/配置路径。

极简风模式：

1. 卸载测试机相关内容。
2. 安装新 EXE，选择 `极简风界面`。
3. 确认安装器安装 VS Code。
4. 双击 `星池指挥官`。
5. 向导显示极简界面步骤。
6. 完成后打开简化 VS Code。

## Non-goals

- 不在本次实现中重做独立文件浏览器。
- 不把 Codex Desktop UI 本身改造成文件浏览器。
- 不删除 VS Code 模式；它作为 `极简风` 继续存在。
- 不改变 modelserver 和 agentserver 的 OAuth 登录流程。
- 不改变 `~/.codex/config.toml` 中现有 modelserver provider 语义。
- 不承诺 Windows 版 Codex Desktop 一定能直接用 deep link 打开指定路径；路径参数支持按实际
  版本检测，失败时退化到新会话。
- 不再提供 Codex Desktop 离线安装包；默认模式依赖 winget 和 Microsoft Store source。

## Implementation Discovery Requirement

实现前需要在 Windows 测试机确认 winget 行为：

- `winget install Codex -s msstore --accept-source-agreements --accept-package-agreements`
  是否能在普通用户权限下安装。
- 是否需要 `--silent`，以及加上 `--silent` 后 package 是否接受。
- 安装完成后 `codex://` URL scheme 是否立即可用。
- winget 缺失、Store source 被禁用、网络不可达时的错误文本，用于脚本错误分类。

默认用户体验仍是一个安装包；Codex Desktop 由安装包调用 winget 安装，不要求用户手动
打开 Microsoft Store。
