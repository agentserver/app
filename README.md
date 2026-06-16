# 星池指挥官

星池指挥官是个人算力网的本地入口。它把用户自己的办公室电脑、家里电脑、
实验室机器和无桌面 Linux 服务器接入同一个工作台，让用户围绕目标工作，而
不是围绕设备、文件位置和工具配置工作。

从用户视角看，它不是“多装几个 AI 工具”，而是一个连续工作网络：

- 一个入口：桌面快捷方式、文件夹右键菜单或 Linux `agentserver` 命令都进入同一个工作体系。
- 多台机器：每台电脑可以作为可调度的工作现场，承载本地文件、长任务或专用环境。
- 自动拆任务：Loom driver/slave 把复杂目标拆成可推进的小任务，并协调多个助手执行。
- 上下文不断：工作区、设备状态、模型登录和本地任务状态由 AgentServer 与本机状态共同维护。
- 用户可控：安装、登录、模型访问、更新、在线状态和本机 slave 都在星池指挥官里可见、可恢复。

## 当前仓库负责什么

本仓库提供把个人设备接入个人算力网的客户端和安装包：

- Windows 安装包 `agentserver-app`
  - 默认安装并配置 Codex Desktop。
  - 可选择极简 VS Code + Codex CLI 模式。
  - 提供桌面快捷方式“星池指挥官”和资源管理器右键“用星池指挥官打开”。
  - 常驻本地控制台和托盘，用于查看登录、额度、更新、本机 slave 和工作区状态。
- Linux headless 版本 `agentserver`
  - 在无桌面服务器上直接运行当前目录作为 slave。
  - `agentserver install-driver` 可把 driver MCP 挂入当前用户的 Codex CLI。
  - 通过 device code 和二维码完成 `code.cs.ac.cn` / `agent.cs.ac.cn` 登录。
- 共享模型访问路径
  - 本地代理负责把 Codex / Codex Desktop 请求转发到 `https://code.ai.cs.ac.cn/v1`。
  - 后台 refresher 维护短期 access token，避免长进程因为 token 刷新而失效。

## 背后系统分工

- 星池指挥官 app：安装、登录、更新、前端选择、本地控制台、设备接入。
- AgentServer：工作区、设备状态、远程身份和跨入口连续体验。
- ModelServer：模型账号、模型调用、使用边界和用量记录。
- Loom：driver/slave 协作、任务拆分、多助手执行和多机器调度。

## 典型使用

1. 用户安装 Windows 安装包、把 macOS DMG 里的 `星池指挥官.app` 拖到 `/Applications`，或在 Linux 服务器上解压 `agentserver`。
2. 首次启动时完成 ModelServer 和 AgentServer 登录。
3. 星池指挥官配置 Codex/Codex Desktop，让模型请求走本地代理。
4. 用户可以从桌面、右键菜单或 Linux 命令进入同一个工作区。
5. 本机或远端 slave 加入后，用户只需要说明目标，系统负责拆分、分派和持续推进。

## 开发构建

```bash
make build          # 构建本机/目标二进制到 dist/
make test           # 前端构建 + go test -race -count=1 ./...
make cross-windows  # 交叉构建 Windows 二进制
make package        # 完整 Windows 打包，需要 Wine + Inno Setup
```

## macOS

- **支持平台：** macOS 11.0+，universal binary（Apple Silicon arm64 + Intel amd64）。
- **构建（仅 Mac，需 CGo）：**
  ```bash
  make package-macos   # 产出 dist/macos/星池指挥官-<version>-universal.dmg + .sha256
  ```
  目标 `cross-darwin`（universal 二进制）、`macos-icon`（`image/icon.png` → `icon.icns`）由 `package-macos` 依赖。CGo（`fyne.io/systray` 菜单栏）无法在 Linux 上交叉编译，故 macOS 构建必须在 Mac 上原生运行。
- **安装：** 挂载 DMG → 拖 `星池指挥官.app` 到 `/Applications`（标准账户通常无需密码；非管理员可拖到 `~/Applications`）→ 双击首启 onboarding。
- **Gatekeeper：** v1 未签名/未公证，首启需「右键 → 打开」或 `xattr -dr com.apple.quarantine /Applications/星池指挥官.app`。拿到 Apple Developer ID 后按 [`packaging/macos/SIGNING.md`](packaging/macos/SIGNING.md) 启用签名 + 公证。
- **数据位置：** `~/.agentserver-app/`（与 Windows/Linux 一致）。
- **已知限制：**
  - GUI 应用不继承 rc 环境变量；env 持久化走 `launchctl setenv` + rc 受管块（只对当前会话生效，对 launcher 显式 spawn 的子进程足够）。
  - 无 Mac App Store 沙箱版（需写 `~/.codex`、`launchctl setenv`、spawn codex/VS Code，与沙箱不兼容）。
  - 不强制开机自启（与 Windows 行为对齐；login item 作为后续可选增强）。

更多设计文档见：

- `docs/superpowers/specs/2026-06-07-tray-dashboard-design.md`
- `docs/superpowers/specs/2026-06-09-local-token-proxy-design.md`
- `docs/superpowers/specs/2026-06-09-local-slave-management-design.md`
- `docs/superpowers/specs/2026-06-13-linux-headless-server-design.md`
- `docs/superpowers/specs/2026-06-15-macos-commander-design.md`（macOS 版设计）
- `docs/superpowers/plans/2026-06-15-macos-commander-implementation.md`（macOS 版实现计划）
