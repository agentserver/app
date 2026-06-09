# 本机多 slave 管理设计

## 目标

在星池指挥官控制台增加本机多 slave 管理能力。用户可以选择文件夹创建 slave，对每个 slave 执行启动/重启、暂停、删除。每个 slave 必须单独重新认证加入 agentserver 工作空间，不能复用 driver 的认证结果。

控制台和远端 agentserver 都必须能看出这些 slave 来自同一台电脑。控制台显示本机分组；agentserver 侧通过 slave 上报的 discovery card 字段体现来源。

## 用户规则

- 安装星池指挥官时确定一次电脑名。
- 电脑名创建后不可修改。
- 创建 slave 时选择一个文件夹。
- slave 名默认使用文件夹名。
- 用户可以在创建时把 slave 名改成任意名称。
- slave 名创建后不可修改。
- slave 名长度限制为不超过 20 个中文字长度。实现按 Unicode rune 计数，最多 20 个字符。
- agent display name 固定为 `电脑名-slave名`。

示例：电脑名为 `61414-PC`，文件夹为 `project-a`，默认 display name 是 `61414-PC-project-a`。如果创建时把 slave 名改为 `前端调试`，display name 是 `61414-PC-前端调试`。

## 现有系统边界

控制台当前由 `internal/ui/server.go` 暴露 `/api/console/*`，由 `internal/console.Controller` 聚合状态和执行动作。前端控制台是 `internal/ui/web/src/components/Dashboard.vue`。

loom driver 当前只在 onboarding/完成后写 `driver.yaml` 并配置 Codex MCP。安装包已包含 `slave-agent.exe`，但当前应用没有 slave 状态模型、配置生成、认证引导或进程生命周期管理。

loom slave 的 agent card 生效路径是向 agentserver 发布 discovery card。可依赖的上报字段包括 `discovery.display_name`、`discovery.description` 和 `resources.tags`。设计不使用 loom 不会上报的自定义字段。

## 架构

新增 `internal/slave` 包，作为本机 slave 管理器。它负责持久化 slave 注册表、生成 slave 配置、启动/停止进程、捕获认证 URL、维护运行状态。

### 安装期电脑名初始化

安装器负责初始化电脑名：

- Inno Setup GUI 安装时增加一个电脑名输入页，默认值为 Windows `COMPUTERNAME`。
- portable `install.ps1` 非静默安装时提示确认电脑名，默认值为 `$env:COMPUTERNAME`。
- 静默安装时不弹交互，直接使用 Windows `COMPUTERNAME`。
- 如果 `machine.json` 已存在，安装器不得覆盖其中的电脑名。
- 旧版本升级时如果没有 `machine.json`，安装器按上述规则创建；如果升级路径无法交互，则使用 Windows `COMPUTERNAME` 并锁定。

电脑名创建后不可修改。控制台只读取并展示电脑名，不提供编辑入口。

持久化文件：

- `~/.agentserver-vscode/machine.json`
  - `machine_id`：安装/首次初始化时生成的稳定本机 ID。
  - `computer_name`：用户安装时确认的电脑名，创建后不可修改。
- `~/.agentserver-vscode/slaves.json`
  - 多个 slave 的本地注册表。
  - 每项包含 `id`、`name`、`display_name`、`folder`、`config_path`、`status`、`pid`、`auth_url`、`last_error`、创建时间和更新时间。
- `~/.agentserver-vscode/slaves/<id>/config.yaml`
  - 单个 slave 的 loom 配置。
- `~/.agentserver-vscode/slaves/<id>/logs/slave.log`
  - slave stdout/stderr 日志。

状态模型中，`name`、`display_name`、`folder` 创建后不可修改。运行态字段如 `status`、`pid`、`auth_url`、`last_error` 可以更新。

## Slave 配置

创建 slave 时写入 loom `config.yaml`，使用 Codex 后端并以用户选择的文件夹作为工作目录。

核心字段：

```yaml
server:
  url: "https://agent.cs.ac.cn"
  name: "61414-PC-前端调试"

credentials:
  sandbox_id: ""
  tunnel_token: ""
  proxy_token: ""
  workspace_id: ""
  short_id: ""

agent:
  kind: "codex"

codex:
  bin: "codex"
  workdir: "C:\\Users\\61414\\project-a"
  extra_args: []

discovery:
  display_name: "61414-PC-前端调试"
  description: "来自同一台电脑：61414-PC；工作目录：C:\\Users\\61414\\project-a"
  skills: ["chat", "file", "permissions", "register_mcp", "unregister_mcp"]

resources:
  tags:
    - "agentserver-vscode-slave"
    - "local-machine:<machine_id>"
    - "host:61414-PC"

observer:
  enabled: true
  url: "https://loom.nj.cs.ac.cn:10062/"
```

`credentials` 初始为空，确保 slave 首次启动时触发 device-code 认证。认证完成后由 `slave-agent.exe` 回写凭据到同一 `config.yaml`。

`resources.tags` 是结构化来源标记，用于 agentserver 未来按本机聚合；`display_name` 和 `description` 是兼容展示字段，即使 agentserver 暂时不展示 tags，也能看出 slave 来自同一台电脑。

## 控制台 API

新增 API 均挂在现有控制台服务下：

- `GET /api/console/slaves`
  - 返回 `machine` 和 `slaves`。
- `POST /api/console/slaves`
  - 请求体：`{"folder":"...","name":"..."}`。
  - `name` 可省略，默认取文件夹名。
  - 创建记录、写配置、启动进程，返回 slave 状态。
- `POST /api/console/slaves/{id}/restart`
  - 停止旧进程后重新启动同一配置。
- `POST /api/console/slaves/{id}/pause`
  - 停止进程，保留配置和认证信息。
- `DELETE /api/console/slaves/{id}`
  - 停止进程，删除本地记录和目录。

返回的 slave 状态包含：

- `id`
- `name`
- `display_name`
- `folder`
- `status`: `stopped`、`starting`、`auth_required`、`running`、`paused`、`error`
- `pid`
- `auth_url`
- `last_error`
- `created_at`
- `updated_at`

## 认证流程

每个 slave 独立认证。创建或重启未认证的 slave 时，后端启动：

```powershell
slave-agent.exe <config.yaml>
```

slave 会在 stderr/stdout 输出 device-code 验证 URL。manager 捕获日志中的 URL，更新该 slave 状态为 `auth_required`，并把 `auth_url` 返回给前端。

认证完成后，slave 回写 `config.yaml` 中的凭据并开始上报 agent card。manager 通过进程存活和配置凭据是否已写入判断状态转为 `running`。

如果认证超时或进程退出，状态转为 `error`，保留 `last_error` 和日志路径。

## 前端设计

`Dashboard.vue` 增加“这台电脑上的 agent”区块：

- 标题：`这台电脑上的 agent`
- 副标题：`本机：<电脑名>`
- 创建入口：选择文件夹，填写 slave 名，显示最终 display name 预览。
- 列表项显示：
  - display name
  - 文件夹路径
  - 状态
  - 认证链接
  - 操作按钮：启动/重启、暂停、删除

创建表单规则：

- 文件夹必填。
- slave 名为空时使用文件夹名。
- slave 名最多 20 个字符。
- 创建后列表中名称不可编辑。

删除操作需要确认，文案说明会删除本地 slave 配置并停止本地进程；远端 agentserver card 的清理取决于 loom/agentserver 是否提供注销接口。本版本先保证本地删除，不声明远端删除。

## 错误处理

- 电脑名未初始化：控制台进入初始化提示，要求用户确认电脑名后再创建 slave。
- slave 名重复导致 display name 重复：创建失败，提示更换名称。
- 文件夹不存在：创建失败。
- 文件夹不可访问：创建失败。
- `slave-agent.exe` 缺失：创建/启动失败，并提示重新安装或更新安装包。
- 认证 URL 未捕获：状态保持 `starting`，日志继续可见；进程退出则转 `error`。
- 暂停失败：返回错误，不删除记录。
- 删除失败：如果进程停止成功但文件删除失败，记录保留并显示错误，避免本地状态和磁盘不一致。

## 测试计划

后端单元测试：

- 初始化电脑名后不可修改。
- Inno/portable 安装器会创建 `machine.json`，且升级时不覆盖已有电脑名。
- 创建 slave 时默认名使用文件夹名。
- 创建 slave 时自定义名不超过 20 个字符。
- 超长名、空文件夹、不存在文件夹、重复 display name 失败。
- 生成的 `config.yaml` 包含正确 `discovery.display_name`、`discovery.description` 和 `resources.tags`。
- 多个 slave 使用同一个 `machine_id` 和电脑名前缀，但各自有独立配置目录。
- pause/restart/delete 调用进程管理接口并更新状态。
- 捕获 device-code URL 后状态变为 `auth_required`。

HTTP API 测试：

- `GET /api/console/slaves` 返回 machine 和列表。
- `POST /api/console/slaves` 校验请求体并创建 slave。
- restart/pause/delete 只接受正确 HTTP 方法。
- 不存在的 slave 返回 404。

前端测试：

- Dashboard 显示 `这台电脑上的 agent` 和 `本机：<电脑名>`。
- 创建表单默认填入文件夹名并展示 display name 预览。
- 超长 slave 名禁用创建或显示错误。
- 多个 slave 列表显示各自状态和操作按钮。
- `auth_required` 状态显示认证链接。

打包/集成测试：

- Windows 安装包和 portable zip 均包含 `slave-agent.exe`。
- 完成安装后控制台能找到 `slave-agent.exe`。
- 在测试替身进程下验证创建、暂停、重启、删除全链路。
