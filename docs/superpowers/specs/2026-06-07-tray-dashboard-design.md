# 星池指挥官常驻控制台与托盘设计

## 背景

当前桌面快捷方式启动 `launcher.exe`。首次运行时它启动本地 onboarding Web UI；完成配置后，再次启动会直接打开 Codex Desktop 或极简 VS Code，然后进程退出。在 Windows 上这会暴露命令行窗口，体验不适合大众用户，也无法持续显示额度状态或提醒。

目标是把“星池指挥官”变成一个常驻的本地控制台：双击桌面图标后既打开控制台页面，也直接启动用户当前选择的前端；关闭页面后后台和托盘继续运行。右键“用星池指挥官打开”仍可打开文件夹，并在本地控制台未启动时自动拉起后台。

## 目标

- 双击桌面“星池指挥官”不再显示命令行窗口。
- 双击后启动本地控制台后台和托盘图标。
- 双击后打开控制台页面。
- 双击后直接启动当前前端：
  - 默认 `codex_desktop`：启动 Codex Desktop。
  - `minimal_vscode`：启动定制 VS Code。
- 右键“用星池指挥官打开”继续可用。
- 右键入口启动时，如果本地控制台没有运行，先后台拉起控制台和托盘。
- 控制台页面显示 modelserver 项目、agentserver 工作空间、5小时和7天额度、订阅入口。
- 托盘显示额度摘要，并在已用额度达到 50% 和 80% 时弹 Windows 通知。
- OAuth 登录成功后必须写入 `Modelserver.ProjectID` 和 `Agentserver.WorkspaceID`，为后续额度和工作空间查询提供稳定主键。

## 非目标

- 不做独立 WebView 桌面窗口；控制台继续使用本地浏览器页面。
- 不在托盘里做复杂交互面板。Windows 托盘只负责图标、tooltip、菜单和通知。
- 不改变 onboarding 的用户可见登录步骤；登录成功后增加 project/workspace 解析和 state 写入。
- 不改变 Codex Desktop 或 VS Code 的核心启动参数，除非是为了复用当前已有 launch/prep 逻辑。

## 用户体验

### 桌面双击

用户双击“星池指挥官”：

1. `launcher.exe` 检查本地控制台是否已经运行。
2. 如果未运行，启动本地 HTTP 服务和 Windows 托盘。
3. 打开本地控制台页面，例如 `http://127.0.0.1:<port>/`。
4. 启动当前安装模式对应的前端：
   - Codex Desktop：打开 `codex://threads/new`。
   - 极简 VS Code：打开定制 VS Code。
5. 如果控制台已经运行，则复用现有后台：重新打开控制台页面，并再次执行“启动前端”动作。

### 右键打开文件夹

用户在 Explorer 右键文件夹选择“用星池指挥官打开”：

1. `open-folder.exe` 检查本地控制台是否已经运行。
2. 如果未运行，后台启动 `launcher.exe --background`。
3. 不主动打开控制台页面，避免打断用户。
4. 按当前模式打开该文件夹：
   - Codex Desktop：打开 `codex://threads/new?path=<folder>`。
   - 极简 VS Code：用定制 VS Code 打开该文件夹。

### 托盘

托盘图标常驻。关闭浏览器页面不会退出后台。

托盘 tooltip：

```text
星池指挥官
5小时 剩余约 42%
7天 剩余约 78%
```

托盘菜单：

- 打开控制台
- 启动 Codex Desktop / 启动极简界面
- 打开订阅页
- 5小时额度：已用 58%，剩余约 42%
- 7天额度：已用 22%，剩余约 78%
- 退出星池指挥官

额度不可用时，tooltip 和菜单显示“额度暂不可用”。

### 额度提醒

提醒按“已用额度”触发：

- 5小时额度已用达到 50%
- 5小时额度已用达到 80%
- 7天额度已用达到 50%
- 7天额度已用达到 80%

每个窗口周期内，每个阈值最多提醒一次。窗口重置后允许再次提醒。判断重置周期优先使用 `resets_at`；如果接口没有返回 `resets_at`，以本地观测到的使用百分比从高值回落到低值作为新周期信号。

## 数据来源

### 本地 state

本项目的 `state.json` 是本地控制台的基础数据源：

- `FrontendMode`：决定启动 Codex Desktop 还是极简 VS Code。
- `Modelserver.ProjectID`：用于调用 modelserver 订阅和额度接口。
- `Agentserver.WorkspaceID`：用于展示 agentserver 工作空间。
- `VSCode.Path`：极简 VS Code 启动路径。
- `CodexDesktop`：显示 Codex Desktop 安装状态。

OAuth 流程是 `Modelserver.ProjectID` 和 `Agentserver.WorkspaceID` 的主写入点。对应 ID 没有解析成功时，登录步骤不能标记为完成，避免后续控制台只能看到空 ID。

- modelserver：`PollModelserverLogin` 完成 PKCE token exchange 并保存 `modelserver_api_key` 后，立即调用 `GET https://codeapi.cs.ac.cn/api/v1/projects`。如果本地已有 `ProjectID`，优先匹配它；否则优先选择 active project；如果没有项目，则创建或选择一个默认项目。选中后写回 `State.Modelserver.ProjectID`，再标记 `modelserver_login` 完成。
- agentserver：`PollAgentserverLogin` 完成 device token 保存后，必须解析用户在 device consent 中选择的 workspace。优先从 token claims/introspection 可见数据取得 `workspace_id`；如果客户端只能调用 API，则调用 `GET https://agent.cs.ac.cn/api/workspaces` 并选择用户当前可用 workspace。选中后写回 `State.Agentserver.WorkspaceID`，再标记 `agentserver_login` 完成。

控制台刷新仍可校验和修复 state：如果本地 ID 指向的 project/workspace 已不存在，可重新拉取列表并提示用户重新选择或重新登录。它不是首次写入 ID 的主路径。

如果远端 API 因 token 类型或权限返回 401/403，onboarding 显示“需要重新登录”或“无法读取项目/工作空间”，并且对应登录步骤保持未完成。

### modelserver API

参考 `modelserver/modelserver`：

- 项目列表：`GET https://codeapi.cs.ac.cn/api/v1/projects`
- 订阅页：`https://code.cs.ac.cn/projects/{projectId}/subscription`
- 额度接口：`GET https://codeapi.cs.ac.cn/api/v1/projects/{projectId}/subscription/usage`

额度接口返回 `data` 数组，元素包含：

```json
{
  "window": "5h",
  "percentage": 58.2,
  "resets_at": "2026-06-07T12:34:56Z"
}
```

页面和托盘展示：

- 已用：`percentage`
- 剩余约：`max(0, 100 - percentage)`
- 重置时间：`resets_at`，如果存在

### agentserver API

参考 `agentserver/agentserver`：

- 工作空间列表：`GET https://agent.cs.ac.cn/api/workspaces`

工作空间展示名称优先使用远端返回的 `name`，没有名称时使用 ID 的短格式。

## 架构

### 单实例控制台

新增一个本地控制台运行模式，复用 `launcher.exe`：

- 正常启动：打开控制台页面并启动前端。
- `--background`：只启动后台和托盘，不打开控制台页面，不启动前端。
- 已有实例运行时：新进程通过端口文件或本地 HTTP 控制接口请求现有实例执行动作，然后退出。

控制台实例写入本地运行时文件：

- `console-port.json`：保存端口、进程 ID、启动时间。
- `console-notifications.json`：保存额度阈值提醒状态，避免重复提醒。

端口文件只作为发现入口。发现后必须调用健康检查接口确认实例真实可用；不可用时清理旧文件并重新启动。

### HTTP API

本地控制台服务在现有 `internal/ui` 服务基础上增加控制台 API：

- `GET /api/console/state`：返回项目、工作空间、额度、前端模式、订阅 URL、错误状态。
- `POST /api/console/refresh`：立即刷新远端状态和额度。
- `POST /api/console/open-frontend`：启动 Codex Desktop 或极简 VS Code。
- `POST /api/console/open-folder`：内部调用，用当前前端打开给定文件夹。
- `POST /api/console/open-subscription`：打开 modelserver 订阅页。
- `POST /api/console/quit`：退出托盘后台。

现有 onboarding API 保留。页面根据 `onboarding_status` 决定显示配置向导还是常驻控制台：

- `pending` / `in_progress` / `failed`：显示配置向导。
- `complete`：显示常驻控制台。

### 托盘实现

Windows 托盘需要新增平台层。优先选择成熟 Go 库实现托盘图标、菜单和通知；如果库无法满足通知能力，则托盘菜单用 Go 库，通知用 PowerShell/Windows toast 辅助命令。

非 Windows 平台使用 no-op 实现，保证 Go 测试和 Linux 构建可运行。

### GUI 子系统

Windows 构建时，`launcher.exe` 和 `open-folder.exe` 使用 Windows GUI 子系统，避免弹出命令行窗口。

如果遇到 fatal error，不能依赖命令行输出。需要写入 `launcher.log`，必要时弹出简短错误提示。

## 页面设计

常驻控制台页面保持极简、工具化，不做营销页。

主区域：

- 顶部：`星池指挥官`
- 状态行：当前前端、后台状态、最后刷新时间
- 额度区：
  - 5小时额度，进度条，已用百分比，剩余约百分比，重置时间
  - 7天额度，进度条，已用百分比，剩余约百分比，重置时间
- 连接区：
  - modelserver 项目名称/ID
  - agentserver 工作空间名称/ID
- 操作区：
  - 打开 Codex Desktop / 打开极简界面
  - 刷新状态
  - 打开订阅页
  - 重新配置

错误状态：

- 项目未找到：显示“未找到 modelserver 项目”，提供打开 modelserver 和重新刷新。
- 工作空间未找到：显示“未找到 agentserver 工作空间”，提供打开 agentserver 和重新刷新。
- 额度不可用：显示具体 HTTP 状态或简短错误，仍保留订阅入口。
- token 失效：显示“需要重新登录”，提供重新配置入口。

## 测试策略

### Go 单元测试

- 单实例发现：
  - 端口文件存在且健康检查成功时复用实例。
  - 端口文件存在但健康检查失败时清理并重启。
- 控制台状态聚合：
  - 能从 state 和 fake modelserver/agentserver API 聚合项目、工作空间、额度。
  - 本地 ID 指向的 project/workspace 不存在时能提示重新登录或重新选择。
  - 额度接口失败时返回可展示错误，不阻断控制台页面。
- OAuth ID 写入：
  - modelserver 登录成功后会写入 `State.Modelserver.ProjectID`，再标记 `modelserver_login` 完成。
  - modelserver 项目解析失败时不标记 `modelserver_login` 完成。
  - agentserver 登录成功后会写入 `State.Agentserver.WorkspaceID`，再标记 `agentserver_login` 完成。
  - agentserver 工作空间解析失败时不标记 `agentserver_login` 完成。
- 额度提醒：
  - 49% 不提醒。
  - 50% 提醒一次。
  - 80% 再提醒一次。
  - 同一周期重复刷新不重复提醒。
  - `resets_at` 变化后允许再次提醒。
- 启动动作：
  - 双击路径会请求打开页面并启动前端。
  - `--background` 不打开页面、不启动前端。
  - `open-folder.exe` 在控制台未运行时会启动后台，再打开文件夹。

### 前端测试

- 完成 onboarding 后显示控制台页面。
- 额度卡片正确展示 5h/7d。
- 额度不可用时显示错误和订阅入口。
- 点击“打开前端”“刷新状态”“打开订阅页”会调用正确 API。

### Windows 验证

- 安装后双击桌面图标没有命令行窗口。
- 双击后浏览器控制台打开，Codex Desktop/极简 VS Code 也打开。
- 关闭浏览器后托盘仍在。
- 托盘菜单能打开控制台、启动前端、打开订阅页、退出。
- 右键文件夹能打开对应前端；如果托盘后台未运行，会自动拉起。
- 模拟额度 50%/80% 时 Windows 通知出现且不重复。

## 风险与降级

- modelserver OAuth token 可能不能访问项目列表或订阅 API：onboarding 不标记 modelserver 登录完成，页面显示需要重新登录或权限不足；不影响已完成配置用户继续启动 Codex/VS Code。
- agentserver device token 可能不能访问 workspace 解析路径：onboarding 不标记 agentserver 登录完成，页面显示工作空间不可用；不影响已完成配置用户右键打开当前前端。
- 托盘库在交叉编译或 Wine 打包环境中可能引入限制：先把托盘封装在独立包，保留 no-op 实现，必要时 Windows 实现单独迭代。
- 浏览器页面不能真正最小化到托盘：产品语义定义为“关闭页面后后台和托盘仍运行，可从托盘重新打开页面”。
