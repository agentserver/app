# VS Code 极简模式 v1.5

**Date**: 2026-06-05
**Status**: Design approved; written spec pending user review
**Scope**: 保留现有 Windows 安装包、VS Code 安装/配置、codex 终端和
`agentserver-vscode` 扩展；增量改造为面向大众用户的极简交互模式。

## Motivation

当前 v1 目标是让非专业用户安装后获得一个已经配置好的 VS Code + codex 环境。
这个方案复用了成熟编辑器、文件树、终端、扩展机制和 Windows 文件夹右键菜单，
落地速度快。但 VS Code 是专业开发工具，即使经过配置，用户仍会看到大量与任务
无关的概念：终端、调试、输出、端口、配置、扩展、命令面板、状态栏等。

大众用户的目标不是学习 VS Code，而是：

- 打开一个文件夹。
- 单击文件后能在中间看到内容。
- 对简单文本文件可以直接编辑。
- 右键文件可以用系统默认应用打开。
- 通过一个对话框让 agent 修改文件、生成文件、创建并运行代码。
- 运行、命令、终端、环境变量、profile 等技术细节都不需要出现在主界面。

因此 v1.5 不推翻现有 VS Code 基座，而是把 VS Code 压成一个受控外壳：
默认界面只保留文件夹、文件内容和会话入口；高级开发工具能力作为隐藏逃生口保留。

## Product Direction

### Recommended path

先做 **VS Code 极简模式**。它改动集中在 `internal/vscode/settings.go` 和
`extensions/agentserver-vscode/`，不需要新建桌面壳，也不改 onboarding 的核心
登录/安装流程。这样可以快速验证用户是否仍然觉得复杂。

后续如果用户仍觉得 VS Code 痕迹太重，再做独立轻量 App。独立 App 可借鉴：

- [filebrowser/filebrowser](https://github.com/filebrowser/filebrowser):
  文件夹、预览、编辑、右键操作的 Go + Web 单二进制形态。
- [OpenHands](https://github.com/OpenHands/openhands):
  AI agent 会话、任务状态、文件修改和命令执行的产品组织方式。

### Non-goals

- 不做新的 Tauri/Wails/Electron 桌面 App。
- 不 fork VS Code 或 code-server。
- 不承诺真正删除 VS Code 内置功能。扩展 API 无法完全移除所有 built-in views，
  只能通过 settings、commands、profiles 和扩展行为尽量隐藏。
- 不在 v1.5 做完整图形化代码运行面板。代码执行仍由 codex/terminal 后台完成，
  但主界面不直接暴露终端。

## User Experience

### First meaningful screen after onboarding

完成配置后点击启动，或后续双击桌面快捷方式：

1. 如果没有打开文件夹，显示通俗的文件夹选择窗口。
2. 如果已有文件夹，直接进入极简工作界面。
3. 左侧是文件列表。
4. 中间是文件内容。简单文本文件可编辑；非文本文件交给系统默认应用或只读预览。
5. 主入口是一个清晰动作：`创建新的会话`。

界面不默认显示终端、输出、调试控制台、端口、问题面板、扩展入口、状态栏、布局
控制按钮或命令中心。

### Language

可见文案使用大众语言：

| Current / technical | New user-facing language |
|---|---|
| 重开 codex 终端 | 创建新的会话 |
| terminal / profile | 会话 |
| workspace | 文件夹 |
| output / debug console / ports | 默认不可见 |
| doctor | 诊断工具 |
| VS Code 配置 | 准备工作区 |

`VS Code` 可以在安装和诊断场景出现，但不应成为主界面的概念中心。产品名仍使用
`星池指挥官`。

### File operations

v1.5 只承诺 VS Code 能稳定支持的文件操作：

- 单击文件在中间打开。
- 简单文本文件可编辑并保存。
- Explorer 右键菜单里增加/保留 `用系统应用打开`。
- 右键文件夹或空白区域仍支持 `用星池指挥官打开`。

如果 VS Code 内置右键菜单中仍有过多高级项，v1.5 只通过扩展新增更通俗的主动作，
不尝试重写全部上下文菜单。

### Agent session

`创建新的会话` 做两件事：

1. 确保后台 codex 会话存在。
2. 将用户焦点带到对话入口，而不是裸终端。

v1.5 可以先用 VS Code terminal 作为隐藏执行载体，但不把它作为默认可见面板。
如果必须显示，应显示为 `会话`，并避免出现 shell prompt 作为第一视觉焦点。

## Architecture

### Existing components reused

```
cmd/launcher
  - 保持启动入口不变。
  - onboarding complete 后继续 exec VS Code，但传入我们的 user-data-dir 和 extensions-dir。

internal/vscode/settings.go
  - 写入更严格的极简 UI settings。
  - 保留独立 user-data-dir，不污染用户自己的 VS Code。

extensions/agentserver-vscode
  - 负责启动时选文件夹、打开/维护后台 codex 会话、注册通俗命令、best-effort 隐藏高级面板。

cmd/open-folder
  - 保持文件夹右键入口，后续显示名改为 "用星池指挥官打开"。
```

### Settings changes

`internal/vscode/settings.go` 继续 merge settings，但增加极简 UI defaults：

```jsonc
{
  "locale": "zh-cn",
  "telemetry.telemetryLevel": "off",
  "workbench.startupEditor": "none",
  "workbench.activityBar.location": "hidden",
  "workbench.statusBar.visible": false,
  "workbench.panel.defaultLocation": "bottom",
  "workbench.panel.opensMaximized": "never",
  "window.menuBarVisibility": "hidden",
  "window.commandCenter": false,
  "workbench.layoutControl.enabled": false,
  "breadcrumbs.enabled": false,
  "editor.minimap.enabled": false,
  "editor.stickyScroll.enabled": false,
  "workbench.editor.showTabs": "single",
  "workbench.editor.empty.hint": "hidden",
  "workbench.tips.enabled": false,
  "update.showReleaseNotes": false,
  "extensions.ignoreRecommendations": true
}
```

The exact availability of some keys depends on VS Code version. Implementation
must keep unknown keys harmless and cover them with settings output tests.

### Extension changes

`extensions/agentserver-vscode` changes:

- Rename visible command title:
  - from `星池指挥官: 重开 codex 终端`
  - to `星池指挥官: 创建新的会话`
- Keep command id stable (`agentserverVscode.reopenCodexTerminal`) to avoid
  breaking tests and existing installs; only the user-facing title changes.
- Register file-context command `用系统应用打开`, implemented with
  `vscode.env.openExternal(fileUri)` for the selected file. If the OS refuses
  the file URI, show a Chinese error message instead of falling back to terminal.
- On activation:
  - prompt for folder if empty;
  - hide technical panels best-effort;
  - do not show terminal as the first screen unless required for current codex integration;
  - if terminal must be opened, immediately refocus Explorer/editor after spawning it.
- Keep `agentserverVscode.doctor` available but do not put it in primary UI.

### Best-effort panel hiding

The current `panel.ts` already notes that VS Code lacks an official API to remove
built-in panel views. v1.5 should make this limitation explicit in tests and docs:

- Use settings to stop panels from opening by default.
- Use command/context best-effort to focus away from hidden views.
- Do not claim debug/output/ports are impossible to access.
- Provide a hidden advanced escape command: `星池指挥官: 显示高级界面`.

### Launcher and shortcut naming

Product-facing names should move from `agentserver-vscode` toward `星池指挥官`:

- Desktop shortcut: `星池指挥官`.
- Folder context menu: `用星池指挥官打开`.
- Internal executable names may remain unchanged for compatibility.

If renaming shortcuts is risky for existing installs, do it idempotently:
create the new shortcut and remove the old shortcut only when it points to our launcher.

## Testing

### Go tests

- `internal/vscode/settings_test.go`:
  - writes all minimal settings;
  - preserves unrelated existing settings;
  - overwrites only managed keys;
  - keeps codex terminal profile intact.
- `internal/shortcut` Windows tests:
  - expected display names use `星池指挥官`;
  - old shortcut cleanup is safe and idempotent if implemented.

### Extension tests

- `npm run compile` remains green.
- Command registration test verifies user-facing titles in `package.json`.
- Activation test verifies a codex terminal/session exists but the command title is
  `创建新的会话`.
- If terminal focus behavior changes, test that activation does not leave the
  terminal as the only visible user task unless no editor/folder is available.

### E2E checks

On the Windows test machine:

- Fresh install opens onboarding and completes existing login/config flow.
- After launch, UI language is Chinese.
- No default terminal/debug/output/ports panel is visible on first screen.
- User can choose/open a folder.
- User can click a text file and see it in the editor area.
- Command palette exposes `星池指挥官: 创建新的会话`.
- Context menu shows `用星池指挥官打开` for folders.

## Risks

- VS Code UI hiding is incomplete by design. Users can still discover advanced
  UI through keyboard shortcuts or menus if they know VS Code.
- Some settings may change across VS Code versions. Pinning minimum supported
  VS Code and testing generated settings reduces this risk.
- Hiding terminal while using terminal-backed codex may make failure states harder
  to debug. Keep `诊断工具` and `显示高级界面` as escape hatches.
- Renaming product surfaces can break existing shortcuts if cleanup is careless.
  Treat shortcut changes as additive/idempotent.

## Decisions

- `创建新的会话` in v1.5 renames and controls the existing terminal-backed
  session. A custom Webview chat is out of scope for v1.5 and belongs to the v2
  UI.
- New installs use `星池指挥官` for the desktop shortcut. Upgrades add the new
  shortcut and safely remove the old one only when it points to our launcher.

## Acceptance Criteria

- A non-technical user can start from the desktop shortcut, choose a folder, click
  a file, and start a new agent session without seeing the words terminal, debug,
  output, profile, or ports in the primary workflow.
- Existing onboarding, VS Code installation, codex config, token refresh, and
  folder right-click behavior continue to work.
- `make test`, `make ui-test`, and extension compile pass after implementation.
