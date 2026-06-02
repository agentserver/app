# agentserver-vscode 安装包设计 (v1)

- **版本范围**: v1 — Windows x64,无 loom
- **日期**: 2026-06-02
- **作者**: brainstorming via Claude
- **状态**: Draft → 待 user review

---

## 0. 背景

项目目标:为非专业用户提供一个 **Windows x64** 安装包 (v1 范围),装完后
桌面有一个名为 `agentserver-vscode` 的快捷方式,双击即获得已配好 codex +
modelserver 的 VS Code 中文开发环境。文件夹右键有 "用 agentserver-vscode
打开" 菜单。

macOS / Linux 包推迟到后续版本;目录结构和接口为多平台留扩展点
(`shortcut/{darwin,linux}.go` 等是 stub),但 v1 不实现也不测试它们。

后续 v2 会加入 [loom](https://github.com/agentserver/loom) 的本地 driver/slave
agent + skills,把本地编辑器与远程 agentserver workspace 拉通做多机协同。

**v1 故意不做** loom 相关任何东西,先把"装好 VS Code+codex,登录 modelserver
和 agentserver,写好 API key"这个核心闭环跑通。

### 与项目描述 `项目描述.md` 的差异

项目描述写"微信扫码登录/注册"。源码层面 (`code.cs.ac.cn`
即 modelserver、`agent.cs.ac.cn` 即 agentserver) 都**没有 WeChat 登录入口**,
两个平台都走 [RFC 8628 OAuth 2.0 Device Authorization Grant](https://datatracker.ietf.org/doc/html/rfc8628)
(Hydra 后端)。用户看到的登录页是否有"微信"按钮取决于平台运维,
**与安装包无关**。安装包只负责发起 device-code 流程并打开浏览器。

---

## 1. 总体架构 & 仓库结构

```
agentserver-pkg/
├── cmd/
│   ├── launcher/                                  # 双击桌面快捷方式触发的入口
│   │   main.go                                    #   - 首启: 拉起 onboarding-server + 开浏览器
│   │                                              #   - 后续: 直接 exec VS Code
│   ├── onboarding-server/                         # 引导 web 服务 (可独立调试)
│   │   main.go
│   ├── agentctl/                                  # 维护 CLI: doctor / reconfigure / uninstall / logs
│   │   main.go
│   └── open-folder/                               # 右键菜单 handler
│       main.go                                    #   - 解析路径 → exec VS Code
├── internal/
│   ├── oauth/devicecode.go                        # 通用 RFC 8628 device-code 客户端
│   ├── modelserver/client.go                      # 调 https://code.cs.ac.cn API
│   ├── agentserver/client.go                      # 调 https://agent.cs.ac.cn API
│   ├── vscode/
│   │   ├── detect.go                              #   - which/where; `code --version` 解析
│   │   ├── install.go                             #   - 下载 + 静默安装 (Windows user-x64)
│   │   ├── settings.go                            #   - 写 user settings.json (merge,不覆盖)
│   │   └── extensions.go                          #   - 装中文语言包 + 我们的 .vsix
│   ├── codex/config.go                            # 写/merge ~/.codex/config.toml + setx OPENAI_API_KEY
│   ├── download/resumable.go                      # 断点续传下载 (Range + ETag + sha256)
│   ├── shortcut/                                  # 桌面快捷方式 + 右键菜单
│   │   ├── windows.go                             #   - .lnk + 注册表 HKCU\Software\Classes\Directory\shell
│   │   ├── darwin.go                              #   - v2 stub
│   │   └── linux.go                               #   - v2 stub
│   ├── env/persist.go                             # setx + 广播 WM_SETTINGCHANGE
│   ├── state/store.go                             # ~/.agentserver-vscode/state.json 原子读写
│   ├── secrets/keyring.go                         # zalando/go-keyring 封装
│   └── ui/                                        # 引导 web UI
│       ├── server.go                              #   - HTTP + JSON-RPC
│       ├── assets/                                #   - embed.FS, 中文单文件 SPA
│       │   ├── index.html
│       │   ├── app.js
│       │   └── styles.css
│       └── progress.go                            #   - SSE 推送进度
├── extensions/agentserver-vscode/                 # VS Code 扩展 (独立 npm 项目)
│   ├── package.json
│   ├── src/extension.ts                           #   - 启动时选文件夹 + 终端守护 + 面板裁剪
│   ├── tsconfig.json
│   └── README.md
├── packaging/windows/
│   ├── installer.iss                              # Inno Setup 6 脚本
│   ├── shell-integration.reg.tmpl                 # 右键菜单注册表模板
│   └── icon.ico
├── test/
│   ├── unit/                                      # Go 各包 + npm
│   ├── integration/                               # Linux 容器内跑 fake server + stub `code`
│   │   ├── fakeserver/                            #   - 模拟 modelserver + agentserver
│   │   ├── flows/                                 #   - 全流程 / 重启恢复 / 重试 / 幂等
│   │   └── vscode_stub/                           #   - 假 code CLI 二进制
│   └── e2e/windows/                               # 真实远程 Windows E2E
│       ├── main_test.go
│       ├── harness/                               #   - ssh / pwsh / webdriver
│       └── fixtures/
├── scripts/
│   ├── build.sh                                   # 交叉编译 Windows 二进制
│   └── package.sh                                 # 调 Inno Setup
├── docs/
│   ├── superpowers/specs/2026-06-02-agentserver-vscode-installer-design.md
│   ├── user-guide.zh.md
│   └── v2-loom-roadmap.md
├── .github/workflows/
│   ├── ci.yml                                     # push/PR: 单元 + 集成 + Windows 包冒烟
│   └── e2e.yml                                    # workflow_dispatch + tag: Windows E2E
├── .goreleaser.yml
├── go.mod
└── 项目描述.md
```

### 分包原则

- **每个 `internal/` 子包一件事一组接口**,3–5 个公开函数,可单测
- **平台差异关在 `shortcut/`、`vscode/install.go`、`env/persist.go` 几个文件**
  里,业务流程层 (`internal/ui/server.go` 调编排) 看不到平台分支
- **引导 web UI 是一次性的** (首启或 `agentctl reconfigure` 时启动),不常驻
- **VS Code 扩展是独立 npm 项目**,CI 用 vsce 打成 `.vsix`,embed 到 Go 二进制
- **v2 扩展点保留**:`shortcut/{darwin,linux}.go`、`internal/service/`、
  `internal/loom/` 的位置在结构里留好,内容是 stub

---

## 2. 组件清单与职责边界

| 组件 | 输入 | 输出 | 依赖 | 一句话职责 |
|---|---|---|---|---|
| `cmd/launcher` | argv | exit code | `state`, `ui.Server`, `vscode.Detect` | 双击入口:首启拉引导,后续直接调 VS Code |
| `cmd/onboarding-server` | 端口 env | HTTP 服务 | `ui.Server` | 可独立运行的引导 server (CI/调试用) |
| `cmd/agentctl` | 子命令 | stdout + exit code | 全部 internal/* | 安装后运维入口:`doctor` / `reconfigure` / `uninstall` / `logs` |
| `cmd/open-folder` | 路径 argv | exec VS Code | `vscode`, `state` | 右键菜单的实际 handler |
| `internal/oauth` | endpoint, clientID, scope | AccessToken, RefreshToken | net/http | 通用 RFC 8628 device-code 客户端 |
| `internal/modelserver` | JWT | APIKey, ProjectID | `oauth` | 包装 modelserver API: list/create project → create key |
| `internal/agentserver` | JWT | WorkspaceID, WorkspaceAPIKey | `oauth` | 包装 agentserver API: get-or-create workspace + ws api key |
| `internal/vscode/detect` | — | Installed, Path, Version | OS exec | 检测 VS Code |
| `internal/vscode/install` | 平台 | 已装 VS Code | `download` | Windows 静默下载 + Inno Setup `/VERYSILENT` 安装 |
| `internal/vscode/settings` | 配置项 | settings.json | encoding/json | merge 不覆盖用户已有键 |
| `internal/vscode/extensions` | .vsix 路径 | 已装扩展 | OS exec | 装中文语言包 + 我们的 .vsix |
| `internal/codex/config` | APIKey, baseURL | ~/.codex/config.toml + env | pelletier/go-toml | merge 已有 config.toml,保留用户其他 provider |
| `internal/download` | URL, dst | 文件 | net/http | 断点续传 (Range + ETag + sha256) |
| `internal/shortcut` | App 名, icon, 目标命令 | .lnk + 右键菜单 | golang.org/x/sys/windows + registry | 桌面快捷方式 + 文件夹右键 |
| `internal/env` | key, value | 已生效环境变量 | x/sys/windows + WM_SETTINGCHANGE | setx 持久化 + 通知正在跑的进程 |
| `internal/state` | path | 读写 state.json | encoding/json + flock | 单一可信状态源,带原子写 |
| `internal/secrets` | name, key | get/set/delete | zalando/go-keyring | 系统 keyring 封装 |
| `internal/ui` | Orchestrator | HTTP + SSE + embed.FS | net/http | 引导 web UI: 中文 SPA + JSON-RPC + 进度推送 |
| `extensions/agentserver-vscode` | VS Code 启动 | 守护终端 + 选文件夹 + 裁剪 panel | VS Code Extension API | 见 §5 |

### 顶层编排器

```go
// 住在 internal/ui/server.go
type Orchestrator interface {
    LoginModelserver(ctx context.Context) (DeviceCodeChallenge, error)
    PollModelserverLogin(ctx context.Context) (modelserver.APIKey, error)
    LoginAgentserver(ctx context.Context) (DeviceCodeChallenge, error)
    PollAgentserverLogin(ctx context.Context) (agentserver.WorkspaceCreds, error)
    EnsureVSCode(ctx context.Context) (progress <-chan Event, err error)
    ConfigureVSCode(ctx context.Context) error
    Finalize(ctx context.Context) error  // 桌面快捷方式 + 右键菜单 + state.json complete
}
```

每个步骤都是**幂等**的 — 失败重启引导,从 `state.json` 恢复进度。

### 边界检查

- `oauth` 只懂 device-code 协议,**不知道** modelserver/agentserver 存在 → fake server 单测覆盖
- `modelserver`/`agentserver` client 只知道 HTTP API,**不知道**引导 UI 存在 → httptest 桩单测
- `vscode` 三个文件互不依赖 — `detect` 不调 `install`,`install` 不写 `settings`
- `shortcut`/`env` 走 build tag (`_windows.go`),编译期分离;非 Windows 是 stub
- VS Code 扩展是独立 npm 项目,不与 Go 共享类型,合约靠 settings.json 的几个键名

---

## 3. 数据流与状态机

### 关键状态: `~/.agentserver-vscode/state.json`

```jsonc
{
  "schema_version": 1,
  "install_id": "uuid-v4",
  "created_at": "2026-06-02T10:00:00+08:00",
  "onboarding": {
    "status": "complete",
    // pending | in_progress | complete | failed
    "completed_steps": ["modelserver_login", "agentserver_login",
                        "vscode_installed", "vscode_configured",
                        "shortcuts_created"],
    "last_error": null
  },
  "modelserver": {
    "base_url": "https://code.cs.ac.cn",
    "user_id": "...",
    "project_id": "...",
    "api_key_suffix": "abcd",
    "api_key_created_at": "..."
  },
  "agentserver": {
    "base_url": "https://agent.cs.ac.cn",
    "user_id": "...",
    "workspace_id": "ws-...",
    "workspace_api_key_suffix": "wxyz"
    // 持久化但 v1 不消费;v2 加 loom 时直接用
  },
  "vscode": {
    "path": "C:\\Users\\xxx\\AppData\\Local\\Programs\\Microsoft VS Code\\Code.exe",
    "version": "1.96.0",
    "installed_by_us": true,
    "user_data_dir": "C:\\Users\\xxx\\.agentserver-vscode\\vscode-data",
    "extensions_dir": "C:\\Users\\xxx\\.agentserver-vscode\\vscode-extensions",
    "extension_version": "0.1.0"
  },
  "shortcuts": {
    "desktop_created": true,
    "context_menu_installed": true
  }
}
```

**敏感信息** (modelserver API key、agentserver workspace API key、refresh tokens)
**不进 state.json**,走 Windows Credential Manager (`zalando/go-keyring`)。
keyring service name 统一为 `agentserver-vscode`。

### 首次启动数据流

```
1. 双击桌面快捷方式 (launcher.exe, 无参)
2. launcher 读 state.json,onboarding.status != complete
3. launcher 在 127.0.0.1:RANDPORT 启动 onboarding-server
4. launcher 调 `rundll32 url.dll,FileProtocolHandler http://127.0.0.1:PORT/`
   打开默认浏览器
5. 用户在浏览器看引导页 (中文 SPA,左侧 stepper 显示 5 步)
6. STEP 1 — modelserver 登录:
   - 用户点"开始登录"
   - 后端调 POST https://code.cs.ac.cn/api/oauth2/device/auth
     → 拿到 user_code + verification_uri_complete
   - 后端用 os/exec 新开浏览器标签页指向 verification_uri_complete
   - 用户在该标签页内完成登录 (账号 / GitHub / 平台运维配的其他方式)
   - 后端轮询 POST .../api/oauth2/token until 200 (interval 5s, 默认 expires_in 600s)
   - 拿到 access_token → 调 GET /api/v1/projects 找 default project,
     没有就 POST /api/v1/projects {"name":"default"}
   - 调 POST /api/v1/projects/{pid}/keys
     {"name":"agentserver-vscode"} → 返回 "ms-xxx" 形 API key
   - 存:
     - keyring "agentserver-vscode" / "modelserver_api_key" = "ms-xxx"
     - setx OPENAI_API_KEY=ms-xxx (+ WM_SETTINGCHANGE 广播)
     - merge 写 ~/.codex/config.toml
     - state.json: modelserver.{user_id, project_id, api_key_suffix}
     - completed_steps += "modelserver_login"
7. STEP 2 — agentserver 登录:
   - 复用 oauth 包,只换 endpoint 到 agent.cs.ac.cn
   - 拿到 token 后 GET /api/workspaces; 没有"default"就 POST /api/workspaces
   - POST /api/workspaces/{wid}/api-keys → workspace api key
   - 存 keyring / state.json
   - completed_steps += "agentserver_login"
8. STEP 3 — VS Code (检测 → 装):
   - Detect: `where code.cmd` + `code --version`,有 ≥1.85 就跳到 STEP 4
   - 否则:
     - download.DownloadResumable https://update.code.visualstudio.com/latest/win32-x64-user/stable
     - SHA256 校验
     - exec installer /VERYSILENT /MERGETASKS=!runcode,addtopath
     - 再 Detect 一次写 state
   - completed_steps += "vscode_installed"
9. STEP 4 — VS Code 配置:
   - 写 vscode user-data-dir/User/settings.json (见 §4.4)
   - 写 ~/.codex/config.toml (见 §4.1)
   - 下载 codex.exe 到 %LOCALAPPDATA%\agentserver-vscode\bin\
   - exec `code --user-data-dir=... --extensions-dir=... --install-extension MS-CEINTL.vscode-language-pack-zh-hans`
   - exec `code --user-data-dir=... --extensions-dir=... --install-extension <embedded.vsix>`
   - completed_steps += "vscode_configured"
10. STEP 5 — 快捷方式 + 右键菜单:
    - 写 %USERPROFILE%\Desktop\agentserver-vscode.lnk → launcher.exe
    - 写注册表:
      HKCU\Software\Classes\Directory\shell\AgentserverVscode\command
        默认值 = "<install>\open-folder.exe" "%V"
      HKCU\Software\Classes\Directory\Background\shell\AgentserverVscode\command
        默认值 = "<install>\open-folder.exe" "%V"
    - completed_steps += "shortcuts_created"
11. 引导页显示"全部完成,点这里启动" → 用户点 → onboarding-server 退出
12. launcher 进入"已配置"分支:exec code.exe --user-data-dir=... (空 workspace)
13. VS Code 启动后 agentserver-vscode 扩展接管:
    - 没 workspace → showOpenDialog 让用户选文件夹
    - 打开后注入 codex terminal profile
```

### Wire 协议: 引导页 ↔ onboarding-server

JSON-RPC + SSE,所有路径前缀 `/api/`:

```
POST  /api/start                              # 触发下一未完成步骤
POST  /api/step/modelserver_login             # 启动 device-code
  ← { user_code, verification_uri_complete, expires_in, interval }
GET   /api/step/modelserver_login/status      # 轮询
  ← { state: "waiting"|"success"|"failed", error? }
POST  /api/step/agentserver_login             # 同上
GET   /api/step/agentserver_login/status
POST  /api/step/vscode_install                # 启动下载, 返回 stream_id
GET   /api/events?stream=<id>                 # SSE 推送进度: {stage, downloaded, total, speed, msg}
POST  /api/step/vscode_configure
POST  /api/finalize                           # 桌面快捷方式 + 右键菜单
GET   /api/state                              # sanitized state.json (不含 secrets)
POST  /api/abort                              # 用户点"稍后再说"
```

SPA 单页 (原生 JS,无框架),侧边 stepper 5 步,主区显示当前步骤的提示 +
按钮 + 进度条。

### 后续启动数据流

**双击桌面图标:**
```
launcher.exe (无参)
 → 读 state.json, status == complete
 → exec code.exe --user-data-dir=<our-dir> (空 workspace)
 → 扩展弹"选文件夹"对话框
```

**右键文件夹 → 用 agentserver-vscode 打开:**
```
shell → open-folder.exe "C:\path\to\folder"
 → exec code.exe --user-data-dir=<our-dir> "C:\path\to\folder"
```

### 错误处理

| 失败点 | 表现 | 处理 |
|---|---|---|
| device-code 超时 (10 分钟未操作) | UI 显示"超时" + "重试" | 不持久化,重新生成 challenge |
| modelserver/agentserver API 5xx 或网络故障 | UI 显示错误 + 重试 | 不写 state |
| VS Code 下载断开 | SSE 推送 error,UI 显示重试 | `internal/download` 走 Range 续传 (见 §4.3) |
| VS Code 静默安装失败 | UI 给出"打开下载页面手动安装"按钮 | 用户装完后点"已装好",回到 detect |
| state.json 损坏 | launcher 启动时 schema 校验失败 | rename `state.json.corrupt-<ts>`,从头来 |
| keyring 不可用 | get/set 报错 | 文件回退 `secrets.json` chmod 600,UI 提示"未存入凭据管理器" |
| 端口 RANDPORT 被占 | bind 失败 | 重试 5 次换端口 |
| 浏览器没装/没默认 | rundll32 失败 | UI 提示 + 显示 URL 让用户手动复制 |
| codex 写 config.toml 与用户旧有冲突 | merge 时备份 | 备份原文件 `config.toml.bak.<ts>`,只追加 `[model_providers.modelserver]` |

**幂等性保证:** 每个步骤先检查目标状态再做事:`vscode/install` 先 `Detect`、
`agentserver.CreateWorkspace` 先 list 看有没有同名的、`modelserver.CreateAPIKey`
先 list 看 keyring 中的 suffix 是否仍 active。重跑 launcher 不会重复
创建 workspace / key。

---

## 4. 各步骤细节

### 4.1 Step 1 — modelserver 登录 + API key

```go
ch, _ := oauth.RequestDeviceCode(ctx, oauth.Config{
    Endpoint:   "https://code.cs.ac.cn",
    AuthPath:   "/api/oauth2/device/auth",
    TokenPath:  "/api/oauth2/token",
    ClientID:   "agentserver-vscode",
    Scope:      "openid profile offline_access",
})
// SSE 推 ch.VerificationURIComplete; 后端立刻打开浏览器新标签页
token, _ := oauth.PollToken(ctx, ch)   // 用 ch.Interval, 默认 5s; ctx 带 expires_in 超时

projects, _ := ms.ListProjects(ctx, token)
proj := pickOrCreateProject(projects, "default")
key, _ := ms.CreateAPIKey(ctx, token, proj.ID, "agentserver-vscode")
// key.Secret = "ms-xxxxx..." (服务端只返回一次)

secrets.Set("modelserver_api_key", key.Secret)
codex.UpdateConfig(codex.Settings{
    Provider: "modelserver",
    Model:    "gpt-5.5",
    BaseURL:  "https://code.ai.cs.ac.cn/v1",
    EnvKey:   "OPENAI_API_KEY",
    WireAPI:  "responses",
})
env.PersistUserEnv("OPENAI_API_KEY", key.Secret)
// Windows: setx OPENAI_API_KEY ... → 写 HKCU\Environment + 广播 WM_SETTINGCHANGE

state.Update(func(s *State) {
    s.Modelserver.ProjectID = proj.ID
    s.Modelserver.APIKeySuffix = key.Secret[len(key.Secret)-4:]
    s.Onboarding.AddCompleted("modelserver_login")
})
```

#### `~/.codex/config.toml` 目标内容

```toml
model_provider = "modelserver"
model = "gpt-5.5"

[model_providers.modelserver]
name = "modelserver"
base_url = "https://code.ai.cs.ac.cn/v1"
env_key = "OPENAI_API_KEY"
wire_api = "responses"
```

`codex.UpdateConfig` 必须 **merge 而非覆盖**:用 `pelletier/go-toml/v2` 反序列化已有 config,
追加/覆写 `model_provider`、`model`、`[model_providers.modelserver]` 三处,
保留用户其他 provider 和顶级 setting。merge 前备份到 `config.toml.bak.<ts>`。

### 4.2 Step 2 — agentserver 登录 + workspace

复用 `oauth`,换 endpoint:

```go
ch, _ := oauth.RequestDeviceCode(ctx, oauth.Config{
    Endpoint:  "https://agent.cs.ac.cn",
    AuthPath:  "/api/oauth2/device/auth",
    TokenPath: "/api/oauth2/token",
    ClientID:  "agentserver-vscode",
    Scope:     "openid profile agent:register",
})
token, _ := oauth.PollToken(ctx, ch)

ws, _ := as.GetOrCreateDefaultWorkspace(ctx, token, "agentserver-vscode")
wsKey, _ := as.CreateWorkspaceAPIKey(ctx, token, ws.ID, "agentserver-vscode")
secrets.Set("agentserver_ws_api_key", wsKey.Secret)
state.Update(...)
```

**v1 只持久化 workspace 凭据,不消费。** v2 加 loom 时直接用,
用户无需再登录一次。

### 4.3 内部:`internal/download` 断点续传

VS Code 安装包 ~100MB,国内网络断一次重下很难受。所有大文件 (VS Code、
codex.exe、未来的 loom 二进制) 都走 `download.DownloadResumable`。

#### 文件布局

```
%LOCALAPPDATA%\agentserver-vscode\cache\
  vscode-1.96.0-win32-x64-user.exe.part   # 下载中
  vscode-1.96.0-win32-x64-user.exe.meta   # {url, etag, total_size, sha256, downloaded_at}
  vscode-1.96.0-win32-x64-user.exe        # 完成且 sha256 通过后 rename
```

#### 算法

```go
func DownloadResumable(ctx context.Context, url, dst, sha256Expected string,
                       progress chan<- Event) error {
    // 1. 读 .meta,若 url+etag 仍匹配 → 取 .part 文件大小作为 offset
    // 2. HEAD url → 拿 ETag/Last-Modified/Content-Length, 验证 server 支持 Range
    //    (Accept-Ranges: bytes 或 Content-Range)
    // 3. GET with Range: bytes=<offset>- → 追加写 .part
    //    - server 返回 200 (而非 206) → 不支持 Range,truncate 重下
    //    - ETag 变了 → 文件已更新,删 .part 重下
    // 4. 每 256KB 推 progress event {downloaded, total, speed}
    // 5. 完成后 sha256 校验,通过则 rename(.part → dst)
    //    失败则删 .part + .meta,return err
}
```

#### 重试策略

网络错误 (timeout / connection reset / 5xx) 自动重试 3 次,指数退避
(2s/4s/8s),仍失败才推 SSE error 让用户点重试。用户点重试时也走续传路径。

#### SHA256 来源

- VS Code: 硬编码当前 v1 锁定版本对应的 SHA256 (从 `https://code.visualstudio.com/sha?build=stable` 获取)。
  升级 VS Code 锁定版本需要改源码常量。
- codex.exe: 同样硬编码版本 + SHA256
- 未来 loom 二进制 (v2): 取 GitHub Release 附带的 `sha256sums` 文件

### 4.4 Step 3 — VS Code 检测/下载/安装

```go
det := vscode.Detect()
if det.Installed && det.Version.GTE("1.85.0") {
    state.VSCode.InstalledByUs = false
    state.VSCode.Path = det.Path
    return nil
}

plan := vscode.PlanInstall()
// plan.URL = "https://update.code.visualstudio.com/latest/win32-x64-user/stable"
// plan.SHA256 = "<硬编码版本对应值>"
// plan.SilentArgs = []string{"/VERYSILENT", "/MERGETASKS=!runcode,addtopath"}

file, _ := download.DownloadResumable(ctx, plan.URL, cachePath(plan), plan.SHA256, progressCh)
err := vscode.SilentInstall(file, plan)   // exec.Command(file, plan.SilentArgs...)
```

#### Windows 安装方式锁定 user-x64

| 候选 | 选 / 不选 | 理由 |
|---|---|---|
| User Installer (`win32-x64-user`) | **选** | 装到 `%LOCALAPPDATA%\Programs\Microsoft VS Code\`,不需要管理员 |
| System Installer (`win32-x64`) | 不选 | 需要管理员/UAC |
| Zip | 不选 | 不会注册 `code.cmd` 到 PATH,后续命令调不到 |

`/MERGETASKS=!runcode,addtopath` 解释:
- `!runcode` — 安装完不自动启动 VS Code (我们自己控制)
- `addtopath` — 让 VS Code 自己把 `Code\bin\` 加到 PATH (用户后续在普通命令行
  里也能用 `code` 命令)。**注意区分**:这里加的是 VS Code 的路径;§4.5 说的
  "不动用户 PATH" 指我们不把 codex 路径加进 PATH (codex 走绝对路径在 VS
  Code terminal profile 里调用)。

安装后 `Detect()` 再调一次确认 `code --version` 可用 (绝对路径,不依赖 PATH 已刷新)。

### 4.5 Step 4 — VS Code 配置 + 扩展安装

#### user settings.json 目标内容

写到 `%USERPROFILE%\.agentserver-vscode\vscode-data\User\settings.json`
(我们的独立 user-data-dir,**不污染用户已有 VS Code 配置**):

```jsonc
{
  "locale": "zh-cn",
  "telemetry.telemetryLevel": "off",
  "workbench.startupEditor": "none",
  "workbench.activityBar.location": "hidden",
  "workbench.statusBar.visible": true,
  "workbench.panel.defaultLocation": "bottom",
  "workbench.panel.opensMaximized": "always",

  // 扩展契约
  "agentserverVscode.panel.allowed": ["terminal", "output"],
  "agentserverVscode.startup.openFolderIfEmpty": true,
  "agentserverVscode.terminal.respawnOnClose": true,
  "agentserverVscode.terminal.profileName": "codex",

  "terminal.integrated.defaultProfile.windows": "codex",
  "terminal.integrated.profiles.windows": {
    "codex": {
      "path": "C:\\Windows\\System32\\cmd.exe",
      "args": ["/k", "<CODEX_ABS_PATH>"]
    }
  }
}
```

**重要:** JSON 不展开环境变量。`<CODEX_ABS_PATH>` 在 `vscode/settings.go`
写盘前由 Go 代码替换为绝对路径,例如
`C:\\Users\\61414\\AppData\\Local\\agentserver-vscode\\bin\\codex.exe`
(JSON 内反斜杠需转义)。

`vscode.settings.WriteMerge` 走 JSON 反序列化 → 改 → 序列化,保留用户已有未涉及的键。

#### 装中文语言包 + 我们的扩展

```go
codeExe := state.VSCode.Path
userDataDir := state.VSCode.UserDataDir
extDir := state.VSCode.ExtensionsDir

exec(codeExe, "--user-data-dir", userDataDir, "--extensions-dir", extDir,
     "--install-extension", "MS-CEINTL.vscode-language-pack-zh-hans")
exec(codeExe, ..., "--install-extension", embeddedVsixPath)
// embeddedVsixPath: embed.FS 中的 .vsix 临时落地到磁盘后传入
```

#### codex CLI 安装

- 下载 `codex.exe` 到 `%LOCALAPPDATA%\agentserver-vscode\bin\codex.exe`
- 走 `internal/download` (断点续传 + sha256)
- 来源 URL 待定 (TODO §7):优先 OpenAI 官方 release 二进制,次选 `npm i -g @openai/codex` (要求用户已装 node,不可控)
- **不动用户 PATH** — VS Code terminal profile 用绝对路径调

### 4.6 Step 5 — 快捷方式 + 右键菜单 + 收尾

#### 桌面快捷方式

```
%USERPROFILE%\Desktop\agentserver-vscode.lnk
  → target: <install>\launcher.exe
  → args:    (无)
  → icon:    <install>\icon.ico
  → workdir: %USERPROFILE%
```

用 `golang.org/x/sys/windows` + IShellLink COM 接口或 `mitchellh/go-ps`
等库写 .lnk;v1 用直接调用 COM 实现 (单文件 `shortcut/windows.go`)。

#### 文件夹右键 "用 agentserver-vscode 打开"

```
HKCU\Software\Classes\Directory\shell\AgentserverVscode
  (default)            = "用 agentserver-vscode 打开"
  Icon                 = "<install>\icon.ico"
HKCU\Software\Classes\Directory\shell\AgentserverVscode\command
  (default)            = "<install>\open-folder.exe" "%V"

HKCU\Software\Classes\Directory\Background\shell\AgentserverVscode
  (default)            = "用 agentserver-vscode 打开"
  Icon                 = "<install>\icon.ico"
HKCU\Software\Classes\Directory\Background\shell\AgentserverVscode\command
  (default)            = "<install>\open-folder.exe" "%V"
```

写 `HKCU\` 不要 `HKLM\`,避免提权。`Background\shell` 是为了让"在文件夹空白处右键"也能用。

#### 收尾

- state.json: `onboarding.status = complete`
- 引导页显示"全部完成,点这里启动" → 用户点 → onboarding-server 退出
- launcher 进入"已配置"分支 exec VS Code

#### `agentctl uninstall`

逆向清理:
- 删桌面快捷方式 .lnk
- 删注册表 `HKCU\Software\Classes\Directory\shell\AgentserverVscode` 整棵
- 删注册表 `HKCU\Software\Classes\Directory\Background\shell\AgentserverVscode`
- `setx OPENAI_API_KEY ""` (清空,无法真删但置空)
- keyring delete `modelserver_api_key`, `agentserver_ws_api_key`, refresh tokens
- 删 `%USERPROFILE%\.agentserver-vscode\` 整个目录 (state, cache, vscode-data, vscode-extensions, bin)
- 删 `%LOCALAPPDATA%\agentserver-vscode\` 整个目录 (codex.exe)
- `~/.codex/config.toml` 还原备份 (取最新 `.bak.<ts>` 重命名回去),没有备份则保留不动
- 不卸 VS Code (即使是我们装的,默认保留;用户 `agentctl uninstall --vscode` 才卸)

---

## 5. VS Code 扩展 `agentserver-vscode`

独立 npm 项目,放在 `extensions/agentserver-vscode/`,用 `vsce` 打成 `.vsix`
embed 到 Go 二进制。

### 职责 (尽量薄)

1. **启动选文件夹** — 没有 workspace 时弹 `showOpenDialog`
2. **终端守护** — 第一个终端必须是 codex profile,关闭后自动重开
3. **面板裁剪** — Problems / Output / Terminal 之外的 panel tabs (Debug Console / Ports / Comments / Test Results 等) 隐藏
4. **诊断命令** — `agentserver-vscode.doctor` 命令打印 codex/keyring 状态

### `package.json` 关键片段

```jsonc
{
  "name": "agentserver-vscode",
  "displayName": "agentserver-vscode",
  "publisher": "agentserver",
  "version": "0.1.0",
  "engines": { "vscode": "^1.85.0" },
  "activationEvents": ["onStartupFinished"],
  "main": "./out/extension.js",
  "contributes": {
    "configuration": {
      "title": "agentserver-vscode",
      "properties": {
        "agentserverVscode.startup.openFolderIfEmpty":
          { "type": "boolean", "default": true },
        "agentserverVscode.terminal.respawnOnClose":
          { "type": "boolean", "default": true },
        "agentserverVscode.terminal.profileName":
          { "type": "string", "default": "codex" },
        "agentserverVscode.panel.hideViews":
          { "type": "array", "default":
            ["workbench.panel.repl", "workbench.debug.console",
             "workbench.panel.comments", "ports",
             "workbench.panel.testResults"] }
      }
    },
    "commands": [
      { "command": "agentserverVscode.doctor",
        "title": "agentserver-vscode: 诊断" },
      { "command": "agentserverVscode.reopenCodexTerminal",
        "title": "agentserver-vscode: 重开 codex 终端" }
    ]
  }
}
```

### `src/extension.ts` 行为伪代码

```ts
export async function activate(ctx: vscode.ExtensionContext) {
  // 1. 选文件夹
  const folders = vscode.workspace.workspaceFolders;
  if (!folders?.length &&
      config().get<boolean>("startup.openFolderIfEmpty")) {
    const picked = await vscode.window.showOpenDialog({
      canSelectFolders: true, canSelectMany: false,
      openLabel: "打开", title: "选择要打开的项目文件夹"
    });
    if (picked?.[0]) {
      await vscode.commands.executeCommand("vscode.openFolder", picked[0], false);
      return;  // 重载后会再次 activate
    }
  }

  // 2. 面板裁剪 (三层兜底,见"已知踩坑")
  await hidePanelViews();

  // 3. 启动后必须有 codex 终端
  if (vscode.window.terminals.length === 0) {
    await openCodexTerminal();
  }

  // 4. 终端关闭重开
  ctx.subscriptions.push(
    vscode.window.onDidCloseTerminal(async (t) => {
      if (!config().get<boolean>("terminal.respawnOnClose")) return;
      if (t.name !== config().get<string>("terminal.profileName")) return;
      if (closingAll) return;  // 50ms 防抖窗口
      await openCodexTerminal();
    })
  );

  // 5. 命令
  ctx.subscriptions.push(
    vscode.commands.registerCommand("agentserverVscode.reopenCodexTerminal",
      openCodexTerminal),
    vscode.commands.registerCommand("agentserverVscode.doctor", runDoctor)
  );
}

async function openCodexTerminal() {
  const name = config().get<string>("terminal.profileName") ?? "codex";
  const term = vscode.window.createTerminal({ name });
  term.show(/*preserveFocus*/ false);
}
```

### 已知踩坑 (spec 中固化)

1. **`vscode.openFolder` 会重启 extension host** — activate 会被再调一次,所以选完文件夹要 `return`,不要继续往下做终端初始化(下一次 activate 会做)。
2. **面板裁剪 VS Code 没有官方 API 移除内置 view**。三层兜底:
   - (a) 用 `setContext` 把内置 view 的 when 表达式置 false — VS Code 1.85+ 部分 view 支持
   - (b) 接管 panel 焦点:`onDidChangeActivePanelView` 每当用户切到我们不想显示的 view,立刻调 `workbench.action.focusPanel` 回到 terminal
   - (c) 文档兜底:settings.json `"workbench.view.alwaysShowHeaderActions": false`,文档告诉用户"如果还是看得到 X view,右键它选 Hide"
3. **codex profile 用绝对路径** — Windows 上 user-level install 之后 PATH 在新 session 才生效,绝对路径避免这个坑
4. **关全部终端 vs 关单个终端** — `onDidCloseTerminal` 加 50ms 防抖 + `onDidChangeWindowState` 检测窗口是否在关闭,只在"用户点了某个 terminal 的 X" 时重开

### 扩展测试 (`@vscode/test-electron`)

```
extensions/agentserver-vscode/test/
  runTest.ts
  suite/
    startup.test.ts     // 启动后断言有 1 个 codex 终端
    respawn.test.ts     // 关闭终端 → 200ms 后断言又有一个
    folderPick.test.ts  // mock showOpenDialog → 验证调用 openFolder
    panel.test.ts       // 切到 debug console → 验证被切回 terminal
```

CI 在 Windows runner 上跑 `npm test`。

---

## 6. 测试策略 (Windows-only, 三层)

```
            ┌────────────────────────┐
            │ E2E (远程 Windows,    │  1 套完整流程, ~12 min
            │ workflow_dispatch +    │
            │ tag release)           │
            └────────────────────────┘
       ┌──────────────────────────────────┐
       │ 集成 (本地 Linux + Docker)        │  ~20 用例, ~5 min
       └──────────────────────────────────┘
  ┌────────────────────────────────────────────┐
  │ 单元 (Go + npm)                            │  全部 package + 扩展, <30s
  └────────────────────────────────────────────┘
```

### 单元测试

| 包 | 测什么 | 怎么测 |
|---|---|---|
| `oauth` | device-code 请求/轮询/超时/refresh | `httptest.NewServer` 起 fake Hydra |
| `modelserver` | list/create project, create/list keys | httptest 桩,断言请求体/header |
| `agentserver` | get-or-create workspace, create ws api key | 同上 |
| `download` | 续传 / Range 不支持回退 / ETag 变化 / sha256 失败 | 自定义 handler 故意中断、改 ETag、截短 body |
| `codex/config` | merge 已有 toml 保留其他 provider | 表驱动 |
| `vscode/settings` | merge 保留用户键 | 同 |
| `state` | atomic write、并发读写、损坏迁移 | 故意写损坏 JSON |
| `shortcut/windows` | .lnk 写入 / 注册表写入 / 删除 | 用 `t.TempDir()` 隔离 + windows-only build tag |
| `env/windows` | setx + WM_SETTINGCHANGE | 同上 |

Go 包覆盖率目标 **≥ 80%**,`go test -race -cover ./...` 在 CI 默认跑。

VS Code 扩展用 `@vscode/test-electron`,见 §5。

### 集成测试 (本地 Linux + Docker)

跑在开发机和 GitHub Actions ubuntu runner 上,**不依赖真 modelserver/agentserver**:

```
test/integration/
├── fakeserver/                    // 实现两个平台的 OAuth + API
│   ├── modelserver.go             //   /api/oauth2/device/auth, /api/oauth2/token,
│   │                              //   /api/v1/projects, /api/v1/projects/{}/keys
│   └── agentserver.go             //   同上 + workspaces, ws api keys
├── flows/
│   ├── full_onboarding_test.go    // 拉起 onboarding-server,httpclient 模拟引导页交互
│   ├── resume_test.go             // 中途杀 launcher → 重启 → 状态恢复
│   ├── retry_test.go              // device-code 超时、API 5xx → 重试 → 成功
│   └── idempotent_test.go         // 跑两遍,断言不重复创建 workspace/key
└── vscode_stub/                   // 假 `code` CLI 二进制,记录调用参数
```

VS Code 用 stub:写一个 Go 小程序伪装 `code`,把 `--install-extension`、
`--user-data-dir` 等参数记到文件供断言。**不需要装真 VS Code**,跑得快。

### E2E 测试 (远程 Windows)

测试目标:`ssh -p2222 61414@10.128.185.173`

```
test/e2e/windows/
├── main_test.go                  // TestMain: 编译 → scp → ssh 跑 → 收集日志
├── harness/
│   ├── ssh.go                    // 封装 golang.org/x/crypto/ssh
│   ├── pwsh.go                   // powershell -NoProfile -Command "..." → stdout+exit
│   ├── webdriver.go              // 远程 chromedriver 接管浏览器走 OAuth
│   └── probe.go                  // VS Code 截屏 + OCR 校验中文 / 面板状态
├── fixtures/account.env          // TEST_MS_USER, TEST_MS_PASS, TEST_AS_USER, TEST_AS_PASS
│                                 //   (CI secret 注入, gitignored)
└── e2e_test.go
```

#### E2E 流程

```
1.  scp 最新 agentserver-vscode-setup.exe → C:\Users\61414\Downloads\
2.  PowerShell 静默卸载残留: agentctl uninstall --silent (容忍失败)
3.  PowerShell 静默安装: Start-Process .\agentserver-vscode-setup.exe /VERYSILENT -Wait
4.  断言: 桌面有 agentserver-vscode.lnk; 注册表有 HKCU\Software\Classes\Directory\shell\AgentserverVscode
5.  ssh 触发 launcher (模拟双击): Start-Process .\agentserver-vscode\launcher.exe
6.  等 onboarding-server 启动 (轮询 127.0.0.1:<port>/api/state, port 从 state.json 拿到)
7.  webdriver 接管已开浏览器:
    - 切到 OAuth tab → 填测试账号 → 提交 → 等待 close
    - 同样处理 agentserver login
    (微信扫码不能脚本化,见下方说明)
8.  等 onboarding 完成 (轮询 /api/state.onboarding.status == "complete", 超时 5 min)
9.  断言:
    - %USERPROFILE%\.codex\config.toml 含 model_provider = "modelserver"
    - state.json 中 5 步全 completed
    - VS Code 可启动, code --version 返回 ≥ 1.85
10. 启动 VS Code: 调 code --user-data-dir=...,等 3 秒,截屏 → OCR 校验:
    - 语言是中文 (识别 "文件"、"编辑" 菜单)
    - 默认 panel = Terminal,内含 "codex" 字样
11. 模拟右键打开文件夹: mkdir C:\tmp\test → 调 open-folder.exe "C:\tmp\test"
    → 截屏验证 VS Code workspace 切到该文件夹
12. 清理: agentctl uninstall --silent + rm -r C:\tmp\test
```

E2E 跑一次约 **12 分钟**。SSH 凭据从环境变量取 (`E2E_SSH_PASSWORD` 或
`E2E_SSH_KEY_PATH`),不入仓。

#### 微信扫码登录处理

E2E 假设测试账号已绑定**用户名/密码**或 **GitHub OAuth** (由我们在 Hydra
后台预配),webdriver 用账号密码走完整流程,不真扫微信。
如果将来要测真微信扫码,需要人工手动跑。

### CI 触发矩阵

| 触发 | 跑什么 | 耗时 | 阻塞合并 |
|---|---|---|---|
| push / PR | `go test -race ./...` + `npm test` + lint | 2 min | ✅ |
| PR | + 集成测 (Linux 容器, fake server + stub code) | 5 min | ✅ |
| PR | + Windows 二进制构建 + Inno Setup 包冒烟 (能起 launcher) | 10 min | ✅ |
| `workflow_dispatch` / tag release | + Windows E2E (`ssh -p2222 10.128.185.173`) | 12 min | ❌ (release 阻塞) |

`workflow_dispatch` 让维护者手动触发 Windows E2E,日常 PR 不卡。

### 测试不覆盖 (v1 明确放过)

- 真微信扫码 — 需要人工
- macOS / Linux 任何路径 — v2
- 升级路径 — v1 先 uninstall + reinstall

---

## 7. 风险与未决项

### 已知风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| 平台 OAuth 实际响应字段与源码不一致 | device-code 崩 | `fakeserver` 兜底;首次 E2E 时录真实响应作 fixture |
| modelserver 创建 API key 需先有 project | `CreateAPIKey` 报 404 | `pickOrCreateProject` 先 list,空则 `POST /api/v1/projects {name:"default"}` |
| VS Code 面板裁剪无官方 API | Debug Console / Ports 仍可见 | (a)+(b)+(c) 三层兜底 (见 §5);最坏情况"主要是 Terminal,其他存在但不显眼" |
| Windows SmartScreen 弹"未知发布者" | 用户被吓退 | v1 文档化 click-through;v1.1 起买 Authenticode 证书 |
| 杀软拦截 launcher 写注册表 | 安装失败 | 文档化 + Inno Setup 自带"添加排除"提示 |
| Hydra 登录页里实际没有微信选项 | 项目描述里"微信扫码登录"落空 | 平台侧问题,不在安装包范围;告知用户找平台运维 |
| codex CLI 版本与 modelserver `wire_api = "responses"` 不兼容 | codex 启动报错 | 锁定 codex 版本;集成测覆盖 toml 写入是否被 codex 接受 |
| 测试机 10.128.185.173 网络不可达 GitHub Actions | E2E 跑不了 | self-hosted runner;或 SSH 反向打洞;或 E2E 仅作 warning |
| 用户已有 `~/.codex/config.toml` 含冲突 provider | merge 时覆盖用户设置 | 只新增 `[model_providers.modelserver]` + 备份原文件到 `config.toml.bak.<ts>` |

### 未决项 (v1 首次 E2E 时确认)

1. **modelserver / agentserver 的 Hydra OAuth client_id 是否需要预注册** — 若是,需要平台运维给固定 client_id `agentserver-vscode`,否则用 PKCE + public client
2. **codex 二进制官方下载地址 + Windows 安装方式** — 是 `npm i -g @openai/codex` 还是有 release 二进制?优先后者(避免对 npm/node 的依赖)
3. **VS Code 面板裁剪 (a) `setContext` 方案在 1.85+ 实际覆盖范围** — 要在 E2E 截屏验证
4. **测试账号** — 由谁创建 `TEST_MS_USER / TEST_MS_PASS / TEST_AS_USER / TEST_AS_PASS` 并放进 CI secrets
5. **OpenAI 兼容 API 的 `base_url` 取值** — 项目描述写 `https://code.ai.cs.ac.cn/v1`,
   modelserver 源码里看到的是 `https://code.cs.ac.cn/api/v1`。**v1 实现以项目
   描述为准** (写入 `~/.codex/config.toml` 的就是 `https://code.ai.cs.ac.cn/v1`),
   首次 E2E 跑通后回头确认;若不通,改 `internal/codex/config.go` 中的常量。
   注意:`/api/v1/projects/...` 等管理 API 仍走 `https://code.cs.ac.cn`,
   只有 OpenAI 兼容推理 API 走 `code.ai.cs.ac.cn`。

### v1 不做 / 推迟到 v2

- **loom driver / slave / observer / skills 整套** (见下方"v2 路线图")
- macOS / Linux 安装包
- 代码签名
- in-place 升级 (用 uninstall + reinstall 替代)
- 多用户 / 多 workspace 切换
- 离线安装包 (不下载 VS Code,bundle)
- 自动更新

### v2 路线图

`docs/v2-loom-roadmap.md` 维护,要点:

- 下载 loom driver/slave Windows 二进制 (从 GitHub Releases)
- 写 `loom config.yaml`,使用 v1 已持久化的 `workspace_id` + `workspace_api_key` + `LOOM_OBSERVER_URL` (待确认)
- 用 `kardianos/service` 注册成 Windows service;无管理员则降级到任务计划程序 (`schtasks /sc onlogon /rl HIGHEST`)
- driver/slave enroll 到 agent.cs.ac.cn workspace (复用 device-code 流程)
- 安装 loom 自带 skills (driver、chat、bash、file 等核心)
- VS Code 扩展不变 (codex 通过 loom MCP 调到 driver)
- 引导页加 STEP 6 "启动协同 agent"
- state.json 增加 `loom` 字段
- E2E 加 loom service 状态校验 + driver MCP 可达性校验
