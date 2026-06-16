# 星池指挥官 macOS 版本设计 (Design Spec)

**日期:** 2026-06-15
**分支:** `feature/macos-implementation`
**状态:** 设计待评审
**参照:** Windows 版（`cmd/launcher`、`cmd/open-folder`、`cmd/token-refresher`、`cmd/agentctl`、`cmd/uninstall` + `packaging/windows`）

## 1. 目标与非目标

### 目标
把 Windows 版「星池指挥官」的桌面体验完整移植到 macOS，作为个人算力网的本地入口。用户安装后：

- 桌面/Dock 有「星池指挥官」入口，双击进入同一个工作体系。
- 首次启动引导完成 ModelServer + AgentServer 登录（OAuth）。
- 自动写入 Codex 配置，模型请求走本地代理 `http://127.0.0.1:53452/v1`，后台 refresher 维护 token。
- 常驻菜单栏图标 + 本地控制台（浏览器 Web 仪表盘），查看登录、额度、更新、本机 slave、工作区状态。
- Finder 右键文件夹「用星池指挥官打开」进入对应前端。
- 支持两种前端模式（与 Windows 一致）：Codex Desktop 模式（默认）与极简 VS Code 模式。
- 支持 universal binary（Apple Silicon arm64 + Intel amd64）。
- 分发为 DMG（拖拽到 `/Applications`）。

### 非目标（v1）
- Mac App Store 上架（沙箱与现有架构不兼容：需写 `~/.codex`、`launchctl setenv`、spawn codex/VS Code）。
- 强制开机自启（与 Windows 行为对齐：点击入口后才常驻；login item 作为后续可选增强）。
- Apple Developer ID 签名 + 公证（用户暂无证书；打包流程预留插入点，拿到证书后启用，详见 §9）。
- Sparkle 式增量更新（仅整包 DMG/zip 替换）。

### 已确认的关键决策
| 项 | 决策 |
|---|---|
| 前端模式 | 双模式全量（Codex Desktop + 极简 VS Code） |
| 架构 | universal（arm64 + amd64） |
| 构建/验证 | 用户 Mac 上编译运行；纯 Go 逻辑在 Linux 交叉编译检查 + 单测 |
| 菜单栏 | 第三方 systray 库（推荐 `fyne.io/systray`，计划阶段确认最新维护 fork），库内部即 Cocoa/CGo |
| 原生集成 | 其余原生能力尽量用 macOS 自带命令行（`osascript`/`ps`/`defaults`/`codesign`/`spctl`/`hdiutil`），避免脆弱的手写 CGo |
| 分发格式 | DMG |
| Codex Desktop | Mac 版存在，双模式可做 |
| 证书 | 先 ad-hoc/不签名构建测试，预留签名+公证开关 |

## 2. 架构总览

**核心洞察：Windows 的运行时架构本身就是跨平台的，几乎不动。** 控制台是 `127.0.0.1` 上的本地 HTTP Web 仪表盘（Vue 前端，`go:embed`），不是原生窗口；IPC 全部基于 loopback HTTP + `console-port.json` + 文件锁。移植工作集中在：

1. **darwin 平台层**：把目前落到 `_other.go`（`//go:build !windows`）桩里的平台函数补上真正的 darwin 实现。
2. **兄弟二进制命名**：`launcher.exe` → `launcher`（去掉 `.exe`）。
3. **打包**：`.app` bundle + DMG + Finder Quick Action + universal 构建。

### 2.1 多二进制模型（忠实移植，推荐）

与 Windows 一一对应，6 个二进制放进 `.app/Contents/MacOS/`：

| 二进制 | 角色 | Windows 对应 |
|---|---|---|
| `launcher` | GUI 入口；首次运行托管 onboarding 向导；常驻托管控制台 + 菜单栏 | `launcher.exe` |
| `open-folder` | Finder 右键处理器；argv[1]=路径；确保控制台后台运行后打开前端 | `open-folder.exe` |
| `token-refresher` | 后台守护：OAuth token 刷新 + 本地模型代理（`model-proxy-daemon` 子命令） | `token-refresher.exe` |
| `agentctl` | 维护 CLI：`doctor`/`install-codex`/`uninstall` 等 | `agentctl.exe` |
| `uninstall` | 卸载器 | `uninstall.exe` |
| `driver-agent` / `slave-agent` | Loom v0.0.5 darwin 二进制（随包附带） | `driver-agent.exe`/`slave-agent.exe` |

`onboarding-server` 仅为开发调试用，不进 Mac 产品包。

**备选方案（不采用）：** 合并为单二进制（像 Linux 的 `agentserver`）。会重构 `cmd/*`、发散大，违背「细节参照 Windows」。

### 2.2 .app bundle 布局

```
星池指挥官.app/
  Contents/
    Info.plist                         # CFBundleName=星池指挥官, CFBundleExecutable=launcher,
                                       #   CFBundleIconFile=icon.icns, LSMinimumSystemVersion=11.0,
                                       #   CFBundleIdentifier=cn.cs.agentserver.app, 用 AppleEvents 的 usage description
    MacOS/
      launcher                         # universal (CGo)，CFBundleExecutable
      open-folder
      token-refresher
      agentctl
      uninstall
      driver-agent                     # universal 或随 arch
      slave-agent
    Resources/
      icon.icns                        # bundle/Dock 图标（image/icon.png → iconutil）
      icon.png / icon-template.png     # 菜单栏图标（template image：黑+透明）
      LICENSE.zh.txt
      agentserver-app.vsix             # 极简 VS Code 模式用（复用 Windows 包）
      driver-skills.tar.gz             # 跨平台，复用
      driver-superpower-skills.tar.gz
      driver-codex-prompts.tar.gz
      codex-manifest-darwin-arm64.json # 新增
      codex-manifest-darwin-amd64.json # 新增
      用星池指挥官打开.workflow          # Quick Action 模板（见 §6.3）
    _CodeSignature/                    # codesign 产物
```

**可写数据位置（不变）：** 全部仍放 `~/.agentserver-app/`（`state.json`、`secrets.json`、`machine.json`、`console-port.json`、`slaves.json`、`bin-root/bin/codex`、`proxy-token`、`token-refresher.lock`、`logs/`、`vscode-data/`、`vscode-extensions/`），与 Windows/Linux 保持一致，便于跨平台共用。`.app` bundle 在签名后只读，所有可写状态都在 bundle 外。

### 2.3 macOS 上没有独立「安装器」

与 Windows（Inno Setup + 一堆 `ensure-*.ps1`）不同，Mac 走 DMG 拖拽到 `/Applications` 的惯例，**没有单独的安装脚本运行**。所有「安装期」工作（装 codex/VS Code/Codex Desktop、装 Quick Action、写 codex 配置、OAuth）都由 **`.app` 首次启动时的 onboarding 向导**完成——这正好复用 launcher 已有的 onboarding 架构（`serveOnboarding`）。流程：

1. 挂载 DMG → 拖 `星池指挥官.app` 到 `/Applications`（标准账户通常无需密码；非管理员可拖到 `~/Applications`）。
2. 双击 `.app` → `launcher` 运行 onboarding 向导（浏览器）。
3. 向导完成：OAuth 登录、按选定模式引导安装 codex/前端、装 Quick Action、写 codex 配置、启动 token-refresher 守护。
4. 转入常驻控制台 + 菜单栏。

## 3. 已就绪、无需改动的子系统

经完整源码审阅，以下核心子系统在 macOS 上**原样可用**（平台逻辑已用 `//go:build !windows` 覆盖 darwin，或本就无 OS 分支）：

- **本地模型代理** `internal/modelproxy`：`127.0.0.1:53452`，loopback，纯 `net/http`。
- **token 刷新 + 守护编排** `internal/modelaccess`（`daemon_process_unix.go` 用 `Setsid` 在 darwin 有效）、`internal/tokenrefresh`（`flock` 在 darwin 有效）。
- **OAuth** `internal/oauth`、`internal/modelserver`（PKCE loopback 回调 + device flow，全可移植）。
- **codex 配置** `internal/codex`（TOML 生成无 OS 分支；见 §4.13 的小清理）。
- **Loom / slave 注册表 / machine** `internal/loom`、`internal/slave`（除进程存活检查，见 §4.4）、`internal/state`、文件锁。
- **secrets** `internal/secrets`（`keyring_darwin.go` 已实现，走 `/usr/bin/security`）。
- **paths** `internal/paths`（`default` 非 windows 分支用 `~/.agentserver-app/bin-root/bin/codex`，darwin 正确；保持不变）。
- **download / appversion / branding / browser**（`open_other.go` 已尝试 `open` 命令，darwin 可用）。
- **IPC 架构**：loopback HTTP + `console-port.json` + 文件锁——完全跨平台。

## 4. 需要新增 darwin 实现的平台层（清单）

> 实现约定：对每个需要 darwin 专属行为的函数，新增 `*_darwin.go`（`//go:build darwin`），并把现有 `*_other.go`（`//go:build !windows`）的构建标签收窄为 `//go:build !windows && !darwin`，使其仅作为 linux/其余平台的回退。凡平台函数内部含 `runtime.GOOS == "linux"` 分支的，拆成 linux 专属 + darwin 专属 + 通用回退三件套。

下面逐项给出 darwin 行为要求。

### 4.1 菜单栏 `internal/tray`
- **现状：** `tray_other.go`（`!windows`）是 `noopApp`，无图标/菜单/通知。
- **接口（共享 `tray.go`，不变）：** `App{Run(ctx, Actions) error; Update(State); Notify(title,msg) error}`，`New(iconPath) App`，`State{Tooltip,FiveHour,SevenDay}`，`Actions{OpenDashboard,OpenFrontend,OpenSubscription,Quit}`。
- **darwin 实现 `tray_darwin.go`：** 用 `fyne.io/systray`（或计划阶段确认的维护中 fork）。
  - `Run`：在主线程跑 `systray.Run(onReady, onExit)`。`onReady` 设置模板图标 + 构建菜单：`打开控制台` / `启动 Codex Desktop`（或 `启动极简界面`，按 install mode）/ `打开订阅页` / 灰色额度行（FiveHour、SevenDay）/ `退出星池指挥官`，绑定到 `Actions`。tooltip 用 `systray.SetTooltip`。点击图标双击 → `OpenDashboard`。
  - `Update`：刷新 tooltip 与灰色菜单项文本。
  - `Notify`：用 `osascript -e 'display notification "<msg>" with title "星池指挥官" sound name "Glass"'`（systray 库的通知支持参差，osascript 稳定、对应 Windows 气球通知）。
- **关键架构差异（darwin 专属）：** macOS 的 Cocoa 要求事件循环跑在**主线程**（main goroutine）。Windows 版把 Win32 消息循环放在一个 `runtime.LockOSThread` 的 goroutine 里，但 macOS 不行——`launcher` 的 `main()` 在 darwin 上必须**让主线程跑 tray，HTTP 控制台跑在 goroutine**（与 Windows 相反）。需在 `cmd/launcher` 加 darwin 专属的主循环装配（见 §5.1）。

### 4.2 文件夹选择器 `internal/folderpicker`
- **现状：** `folderpicker_other.go` 返回硬错误。
- **darwin 实现 `folderpicker_darwin.go`：** `osascript -e 'choose folder with prompt "选择允许被远程控制的文件夹"'`，解析返回的 POSIX 路径（AppleScript 返回 `alias …` 形式，需转 POSIX）。无手写 CGo。取消返回 `("", nil)`。

### 4.3 控制台实例归属判断 `internal/console`
- **现状：** `instance_process_unix.go`（`!windows`）在 linux 读 `/proc/<pid>/status` 的 `Uid:`，否则用 `syscall.Kill(pid,0)`（只判活，不判归属）。
- **darwin 要求：** 用 `ps -o uid= -p <pid>` 比对 `os.Getuid()`，确认端口文件属于当前用户。
- **文件拆分：** `instance_process_linux.go`（`/proc`）/ `instance_process_darwin.go`（`ps` uid）/ 通用回退（`syscall.Kill`）。

### 4.4 slave 进程存活 + 可执行文件核验 `internal/slave`
- **现状：** `process_liveness_unix.go`（`!windows`）的 exe 核验分支是 linux 专属（读 `/proc/<pid>/exe`）；非 linux 直接 `return processMatch`，即 darwin 上「信任任何活 PID」，存在 PID 复用误杀风险。
- **darwin 要求：** 用 `ps -o comm= -p <pid>`（或 `proc_pidpath`）取真实可执行路径，与期望 exe 比对（`os.SameFile`）。
- **文件拆分：** `process_liveness_linux.go` / `process_liveness_darwin.go`（`ps`）/ 通用回退。`process_liveness_darwin_test.go` 当前断言的是「信任活 PID」的弱行为，需更新为强核验断言。

### 4.5 环境变量持久化 `internal/env`
- **现状：** `persist_other.go`（`!windows`）`persistUserEnv`/`deleteUserEnv` 是空 no-op。`tokenrefresh` 把它用作 `PersistEnv`（直连 API key 模式用）。
- **darwin 实现 `persist_darwin.go`：**
  - 写：`launchctl setenv <key> <val>`（当前 GUI 会话生效）+ 在 `~/.zshrc`（与 `~/.bash_profile`）写入受管块（带 `# agentserver-managed:start/end` 标记），保证新开终端/重启后持久（镜像仓库已有的「受管块」模式）。
  - 删：`launchctl unsetenv` + 移除受管块。
  - **已知局限（在文档中说明）：** macOS GUI 应用不继承 rc 文件环境；`launchctl setenv` 只对当前会话有效。这对「把 key 暴露给 slave 子进程」够用（slave 由 launcher 直接 spawn，可显式传 env）。
  - 这是与 Windows（HKCU\Environment，跨重启持久）契合度最弱的一处，明确标记。

### 4.6 桌面快捷方式 + 右键菜单 `internal/shortcut`
- **现状：** `shortcut_other.go` 返回硬错误。
- **darwin 实现 `shortcut_darwin.go`：**
  - **桌面快捷方式：** `osascript -e 'tell application "Finder" to make alias file to (POSIX file "<.app>") at (POSIX file "<Desktop>")'`，文件名 `星池指挥官`。备选 symlink（但 symlink 丢失 bundle 图标）。
  - **右键菜单（Finder Quick Action）：** 把预制的 `用星池指挥官打开.workflow`（Automator「快速操作」，接收「文件或文件夹」，运行 shell）拷贝到 `~/Library/Services/`。workflow 的 shell 动作对每个选中项调用 `<.app>/Contents/MacOS/open-folder "<path>"`。.workflow 模板随包附带在 `Resources/`，安装时拷贝（路径在打包期注入，或运行期用 `os.Executable()` 推算）。对应 Windows 的 3 个右键动词（文件/文件夹/背景）合一为单个接收文件或文件夹的 Service。
  - **uninstallAll：** 删桌面别名 + 删 `~/Library/Services/用星池指挥官打开.workflow`。
- **备选（不采用）：** Finder Sync 扩展——更强大但需 `.appex`、签名场景复杂，过度。

### 4.7 VS Code 探测/安装/配置 `internal/vscode`
多处 Windows-only，需补 darwin：
- **探测 `detect_darwin.go`：** 候选路径补 `/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code` 与 `~/Applications/...`；用 `defaults read /Applications/...app/Contents/Info CFBundleShortVersionString` 取版本。`detect_other.go` 收窄为 `!windows && !darwin`。
- **安装计划（共享 `install.go` 的 `planInstallFor`，当前非 windows/amd64 会 **panic**）：** 加 `case "darwin"` 返回 `InstallPlan`：下载 `https://update.code.visualstudio.com/latest/{darwin-arm64|darwin-universal}/stable` 的 zip，`FileExt=".zip"`，附 SHA256。
- **静默安装 `install_darwin.go`：** `unzip -o <zip> -d /Applications`（产出 `Visual Studio Code.app`），`codesign --verify --deep --strict` + `spctl --assess`，`xattr -dr com.apple.quarantine`。`install_other.go` 收窄。
- **签名校验 `install_authenticode_darwin.go`：** `codesign --verify` + `spctl`（替换 no-op）。`install_authenticode_other.go` 收窄。
- **终端配置（共享 `settings.go`，当前只写 Windows `cmd.exe` profile）：** 补 `osx` profile（如 `/bin/zsh -l`），并在 `terminal.integrated.defaultProfile.osx` 指定。
- **bootstrapper 校验（共享 `validateBootstrapperFile`，要求 `"MZ"` PE 魔数）：** darwin zip 走 zip 魔数校验分支。

### 4.8 Codex Desktop 探测/安装 `internal/codexdesktop`
- **探测 `detect_darwin.go`：** 检查 `/Applications/Codex.app/Contents/Info.plist` 存在 + `codex://` scheme 注册（`/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister -dump | grep codex` 或简化为 `defaults`/存在性判断）。`detect_other.go` 收窄。
- **安装 `install_darwin.go`：** 下载 Codex Desktop 的 darwin 包（dmg/zip）→ 挂载/解压到 `/Applications` → `codesign`/`spctl` 校验 → `xattr` 清隔离。现 `winget.go`/`install.go` 是 winget 专属，需把 `EnsureInstalled` 的安装机制改成平台分派。
- **启动（共享 `launch.go`）：** `codex://threads/new[?path=]` 经 `browser.Open`（`open` 命令）触发 Launch Services——已可用。

### 4.9 Codex 运行时 manifest `internal/codexruntime` + 装配
- `codexruntime` 的 extract/integrity/manifest 逻辑可移植。**需新增 manifest 文件：**
  - `packaging/macos/codex-manifest-darwin-arm64.json`：pin `@openai/codex` darwin-arm64，`strip_prefix: "vendor/aarch64-apple-darwin/"`，`codex_exe: "bin/codex"`，`required_files: ["bin/codex","codex-path/rg", ...]`，双 sha（integrity + shasum），URL 用现有国内 npm 镜像。
  - `packaging/macos/codex-manifest-darwin-amd64.json`：同上，`strip_prefix: "vendor/x86_64-apple-darwin/"`。
- **manifest 选择装配：** 桌面端通过 `agentctl install-codex --manifest <path>` 触发 `codexruntime.Ensure`。需在桌面流程（onboarding/launcher）按 `runtime.GOARCH` 选择对应 darwin manifest（manifest 文件随包在 `Resources/`，运行期由 `os.Executable()` 推算路径）。
- **隔离处理：** 下载的 `codex` 二进制会被 Gatekeeper 隔离，安装后 `xattr -dr com.apple.quarantine` 清除（与 Windows「校验后运行」一致）。

### 4.10 自更新 `internal/updater`
- **manifest URL（共享 `service.go`，当前硬编码 `windows/`）：** 平台化，darwin 用 `https://assets.agent.cs.ac.cn/agentserver-app/macos/latest.json`（新增服务端清单）。
- **缓存文件名（共享 `installerCachePath`，当前强制 `.exe`）：** 平台化扩展名（dmg/zip）。
- **启动安装器 `installer_darwin.go`：** zip → 解压替换；dmg → `hdiutil attach` 挂载 + 拷贝 + `hdiutil detach`。需 detach/`Release` 以免阻塞（对齐 Windows `cmd.Start()+Release()`）。`installer_other.go` 收窄。
- **替换运行中的 bundle `replace_darwin.go`：** 运行中的 Mach-O 文件不能原地删，但可重命名其 `.app` 目录：`旧.app → 旧.app.old` → 新 `.app` 入位 → 重启 → 删 `.old`。`replace_other.go`（裸 `os.Rename`）收窄。
- **控制台更新流程 `internal/console/update.go`：** 「下载 → 启动安装器 → 退出」模型在 darwin 同样适用，由上述平台文件承接。

### 4.11 卸载进程清理 `internal/uninstall`
- **`process_stop_darwin.go`：** 枚举可执行路径在 `.app/Contents/MacOS/` 下的进程（`ps -eo pid,comm` 或 `pgrep -f`）→ `SIGKILL` → 轮询等待退出（对齐 Windows PowerShell `Stop-Process`+`Wait-Process`）。`process_stop_other.go` 收窄。
- **`registry_other.go`（`removeUninstallRegistry` no-op）：** macOS 无「添加/删除程序」注册表。DMG 安装时保持 no-op；若日后用 `.pkg` 分发，可 `pkgutil --forget <id>`。当前可接受 no-op。

### 4.12 兄弟二进制命名（跨切面改动）
`cmd/launcher` 与 `cmd/open-folder` 大量 `joinExe(installDir, "launcher.exe")` 等硬编码 `.exe`。新增小 helper（如 `internal/process/exename.go` 或 cmd 内私有 `exeName(name)`）：windows 返回 `name+".exe"`，其余返回 `name`。替换所有 `joinExe(dir, "X.exe")` 为 `joinExe(dir, exeName("X"))`。
- `agentserver-app.vsix`：跨平台同名，不变。
- 图标：`.ico` → darwin 用 `.icns`（bundle）+ `.png`/template（菜单栏）。`preferredIconPath`（当前 glob `icon-*.ico`）需平台化。
- **兄弟发现本身仍有效**：`os.Executable()` 在 darwin 返回 `…/Contents/MacOS/launcher`，同目录即兄弟二进制所在（只读可读）。

### 4.13 install-mode 路径可写性 `internal/installmode`
- **问题：** `PathForExecutable` 返回 `<exe 目录>/install-mode.json`；签名后的 `.app` 里 `Contents/MacOS/` 只读 → 写失败。
- **方案：** darwin 上把 install-mode 文件迁到可写位置 `paths.InstallRoot`（`~/.agentserver-app/install-mode.json`）。`installmode` 加一个接受 `paths`/显式可写路径的入口，`cmd/launcher`、`cmd/open-folder`、`cmd/agentctl` 在 darwin 上改用该路径（Windows/Linux 行为不变）。

### 4.14 codex 配置的 sandbox 小清理（低风险）
`internal/codex/config.go` 无条件写 `[windows] sandbox = "unelevated"`。改为平台条件化：仅 windows 写 `[windows]`；darwin 按需写 mac 沙箱键（或省略，待确认 macOS codex 的沙箱约定）。非阻塞清理。

## 5. 关键 darwin 专属实现要点

### 5.1 launcher 主循环重构（tray 必须在主线程）
macOS Cocoa 强制事件循环在主线程。当前 `cmd/launcher` 在主 goroutine 跑 `srv.Serve(ln)`、在子 goroutine 跑 tray（Windows 模式）。darwin 需要**反过来**：
- 平台钩子 `runApp(mainLoop, trayLoop)`：darwin 把 `trayLoop` 跑在主线程、`mainLoop`（HTTP server + 控制台 + slave manager + updater）跑在 goroutine；Windows 维持现状。
- 用 `//go:build darwin` 的 `cmd/launcher` 装配文件 + `!darwin` 的现有装配文件分派。
- 这是 darwin 端最大的结构性改动，需仔细设计以不破坏 Windows 流程（Windows 流程必须有回归测试保护）。

### 5.2 依赖在 Mac 上的引导（onboarding 内完成）
全部复用现有 onboarding 编排，补 darwin 安装机制：
- **Codex 运行时：** `agentctl install-codex` + darwin manifest（§4.9）。
- **VS Code（极简模式）：** `vscode` 包的 darwin 安装路径（§4.7）。
- **Codex Desktop（默认模式）：** `codexdesktop` 包的 darwin 安装路径（§4.8）。
- **driver skills/prompts：** `loom.InstallDriverSupport`（Go 实现，跨平台，解压受管 tarball 到 `~/.agents/skills`、`~/.codex/skills`，合并 `~/.codex/AGENTS.md` 受管块）——原样可用。
- **Quick Action / 桌面别名：** `shortcut` 包的 darwin 实现（§4.6）。

### 5.3 Finder Quick Action（`.workflow`）构造
预制的 `用星池指挥官打开.workflow` 是一个 Automator document bundle（`document.wflow` + `Contents/Info.plist`）。打包期生成：用 `osacompile` 或预制模板 + 注入 `.app` 路径占位符。最稳妥：在 `packaging/macos/` 维护一份模板，`package-macos.sh` 用打包时的 `.app` 内 `open-folder` 绝对路径替换占位符；onboarding 时拷到 `~/Library/Services/`。

## 6. 打包与分发

### 6.1 Makefile 新增目标
```
cross-darwin     # 在 Mac 上原生构建 universal 二进制（CGO_ENABLED=1，arm64+amd64 → lipo）
macos-icon       # image/icon.png → icon.icns（iconutil/sips）
package-macos    # cross-darwin + macos-icon + 组装 .app + （可选）签名 + 生成 DMG
```
- darwin 构建必须 `CGO_ENABLED=1`（systray 库依赖）。在用户 Mac 上原生 `go build` 即可；universal 用两次 `GOARCH` 构建 + `lipo -create`。
- Linux 上无法构建 darwin CGo 二进制；`cross-darwin`/`package-macos` 仅在 Mac 上运行。

### 6.2 `scripts/package-macos.sh`
职责（对齐 `scripts/package-windows.sh` / `package-linux.sh` 的分阶段风格）：
1. 构建所有 darwin universal 二进制到 `dist/macos/bin/`。
2. 准备 `driver-agent`/`slave-agent`（darwin arm64+amd64 lipo 或随 arch；SHA 校验，镜像 `linux-package-common.sh`）。
3. 拷贝 resources（vsix、skills tarball、codex manifests、LICENSE、icon.icns、Quick Action 模板）。
4. 组装 `星池指挥官.app`（按 §2.2 布局）。
5. 写 `Contents/MacOS/install-mode.json`（初始 `{"frontend_mode":"codex_desktop"}`，首次运行可改）。
6. **签名（可选开关）：** 有 `MACOS_SIGN_IDENTITY` 环境变量 → `codesign --deep --force --options runtime --sign "$MACOS_SIGN_IDENTITY"` + `xcrun notarytool submit` + `xcrun stapler staple`；否则 `codesign -s -`（ad-hoc）。开关化，默认 ad-hoc。
7. 生成 DMG：`hdiutil create`（含 `/Applications` 软链的拖拽布局），按需签名/公证 DMG 本身。

### 6.3 分发与首启
- 分发物：`星池指挥官-<version>-universal.dmg` + `.sha256` sidecar。
- 用户拖拽安装到 `/Applications`（或 `~/Applications`）。首次双击触发 onboarding（§5.2）。
- **Gatekeeper 提示：** 未签名/未公证时，非技术用户会遇到「无法验证开发者」。v1 接受此限制（用户右键→打开 或 `xattr -dr`）；拿到证书后启用 §6.2 步骤 6 即消除。

## 7. 测试策略

### 7.1 在 Linux（本机）可验证的部分
- **纯 Go 逻辑单测：** 所有平台无关包继续 `go test`。
- **darwin 平台逻辑的可测性设计：** darwin 平台文件（`ps`/`osascript`/`codesign` 解析等）不会在 linux 编译运行。为最大化 linux 上覆盖，把**解析/判定逻辑**抽到共享（无构建标签）文件、把**OS 调用薄壳**放进 `_darwin.go`，并注入「命令执行器」接口，使共享逻辑的 `*_test.go` 在 linux 上可跑。
- **交叉编译检查：** `GOOS=darwin GOARCH=arm64 go vet/build ./...`（非 CGo 部分可在 linux 验证编译；CGo 部分 Mac 上验证）。
- **回归保护：** Windows/Linux 流程不动，确保 `go test ./...` 全绿（当前基线：27 包全过）。

### 7.2 仅在 Mac 上验证的部分
- 构建 universal `.app` 与 DMG。
- 端到端：首启 onboarding（OAuth）、菜单栏、Quick Action 右键、两种前端模式、本机 slave 增删停、token refresher 守护、自更新替换。
- 可选：macOS CI runner 自动构建（v1 不阻塞）。

### 7.3 测试矩阵（对齐 README 的 Windows 测试覆盖）
macOS 验收覆盖：首次启动登录流程 / Codex Desktop 默认模式 / 极简 VS Code 模式 / Finder 右键「用星池指挥官打开」/ 本地控制台 + 菜单栏 + 更新检查 + model proxy/refresher / 本机 slave 增启停删 / 卸载。

## 8. 实现顺序（高层，供 writing-plans 细化）
1. **脚手架与构建：** Makefile `cross-darwin`/`macos-icon`/`package-macos`、`packaging/macos/` 目录、Info.plist、icon.icns、`scripts/package-macos.sh`。先能产出可运行的 `.app`（哪怕平台层是桩）。
2. **兄弟命名 helper + paths/installmode 可写路径**（§4.12、§4.13）——解锁其余改动。
3. **进程归属/存活 darwin**（§4.3、§4.4）——安全相关，先稳。
4. **平台层补齐（无 CGo 部分）：** env（§4.5）、shortcut（§4.6）、vscode（§4.7）、codexdesktop（§4.8）、codex manifest（§4.9）、updater（§4.10）、uninstall 进程清理（§4.11）、folderpicker（§4.2）、codex 配置清理（§4.14）。
5. **菜单栏（CGo/库）：** tray_darwin（§4.1）+ launcher 主循环重构（§5.1）。
6. **端到端联调（Mac）：** onboarding 全流程 + 两种前端 + Quick Action + 自更新 + 卸载。
7. **文档：** README/项目描述补 macOS 章节；打包/签名说明。

## 9. 签名/公证（预留，拿到证书后启用）
- 需要：Apple Developer Program（$99/年）→ Developer ID Application 证书。
- 流程：每个 `Contents/MacOS/` 二进制 + bundle 用 `codesign --deep --force --options runtime` 签名；`driver-agent`/`slave-agent`/`codex` 各自签名；`xcrun notarytool submit` + `stapler staple`；DMG 本身也签+公证。
- 打包脚本已开关化（`MACOS_SIGN_IDENTITY`），届时无需改架构。

## 10. 风险与待确认
- **systray 库选择：** 计划阶段确认 `fyne.io/systray` 是否为最新维护 fork、是否支持所需菜单结构；通知一律走 osascript 兜底。
- **launcher 主线程重构：** darwin 把 tray 放主线程、server 放 goroutine，需保证不破坏 Windows（靠 `//go:build` 分派 + Windows 回归测试）。
- **Codex Desktop Mac 安装源：** 需确认其 darwin 分发包的下载地址与格式（dmg/zip），用于 `codexdesktop` darwin 安装。
- **安装位置与自更新可写性：** 默认拖拽到 `/Applications`（`/Applications` 通常属 `admin` 组可写，标准管理员账户拖拽与自更新替换 bundle 均无需密码）；非管理员账户用 `~/Applications` 回退（此时自更新亦无提权问题）。默认走 `/Applications`，文档说明 `~/Applications` 备选。
- **env 持久化契合度：** macOS 无 HKCU\Environment 等价物（§4.5 已说明局限）。
- **codex macOS 沙箱键：** 需确认 darwin codex 的沙箱配置约定（§4.14）。

## 11. 与现有 Windows/Linux 的关系
- Windows 流程**零改动**（靠构建标签分派 + 回归测试保护）。
- Linux headless（`cmd/agentserver`）**零改动**。
- 新增的 darwin 平台文件与 `packaging/macos/` 是叠加式增量；共享包仅在必要处（codex 配置 sandbox、vscode planInstall、updater URL、兄弟命名 helper、installmode 路径）做平台分派，且以「不影响 Windows/Linux」为约束。
