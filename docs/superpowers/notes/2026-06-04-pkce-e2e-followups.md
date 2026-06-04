# PKCE E2E follow-ups (Windows 测试机, 2026-06-04)

PKCE login PR 在 Windows 测试机 (`ssh -p2222 61414@10.128.185.173`) 上跑通
了完整 onboarding 链路。过程中发现了 3 个**值得后续单独处理**的问题，本
文记下症状 + 当前 workaround + 应做的修复，方便起新的 P-task。

测试结果：5 个 onboarding step 全部完成；codex 配置写入正确；OPENAI_API_KEY
的值是 `ory_at_xNOB8XEC...`（Hydra access_token）；桌面快捷方式 + 右键菜单
都建好。

---

## 1. 前端 UX bugs（高优先级 — 用户感知最强烈）

`internal/ui/assets/app.js` 当前实现把所有非 happy path 信息都吃掉了，导
致用户看到的页面"看起来啥都没发生"。

### 1.1 点 "安装 VS Code" 没反应

**症状**：用户点按钮 → 页面立刻无变化 → 用户疑惑是不是点错了 → 再点一次。

**原因**：`handleVSCodeInstall` (server.go:110) 启 SSE stream 立刻返回，前
端 `streamProgress` (app.js:71-80) 把每条 progress event 只 `console.log`
了，没渲染到页面。当 EnsureVSCode 走 early-return（VS Code 已装），SSE
不发任何 event 就关闭 — 前端就完全静默。

**修复**：
- 给每个步骤一个 status 区（"进行中..." / "已完成 ✓" / "失败: <原因>"）
- streamProgress 收到 event 后在 status 区显示当前 stage 文本
- 步骤完成时改成绿色 ✓

### 1.2 错误用 `alert` 弹，关掉就丢

**症状**：登录失败时弹个 alert（如"登录失败: invalid character '<'..."），
用户关掉后页面没保留这个错误，看上去又像啥都没发生。

**修复**：错误持久显示在对应步骤的 status 区，加"重试"按钮。

### 1.3 没有 step 进度条

**症状**：用户不知道当前在 5 步里的哪一步。

**修复**：顶部加 "步骤 N/5" 计数 / progress bar。

### 1.4 onboarding 完成后没有终态反馈

**症状**：`onboarding_status=complete` 后页面没自动跳"已完成"页 / 关闭按钮。

**修复**：refreshState 检到 complete 时切到完成态视图（"全部完成! 现在可以
双击桌面 agentserver-vscode 快捷方式开始使用"）。

### 1.5 "安装 VS Code" / "配置 VS Code" 步骤之间没有清晰区分

**症状**：实测中 VS Code 已装，"vscode_installed" 立刻完成，紧接着
"vscode_configured" 也自动完成。用户没有时间反应。

**修复**：要么改成单按钮 "配置 VS Code"（合并两步），要么显式串联中加进度提示。

---

## 2. access_token 1 小时过期

**症状**：当前实现把 PKCE access_token 直接写进 `OPENAI_API_KEY`
（`internal/ui/orchestrator_real.go` PollModelserverLogin）。Hydra 默认
access_token TTL 是 3600s。一小时后 codex 调 `https://code.ai.cs.ac.cn/v1`
会拿到 401。

**当前 workaround**：用户重新双击桌面快捷方式 → 触发完整 onboarding 流程
（state.json 已存在但 `onboarding_status=complete`，所以 launcher 走
`execVSCode` 而不是重新跑 onboarding）→ ❌ **会失败**：launcher 看到
onboarding 已完成就直接 exec VSCode，不会重新登录刷 token。

实际现在的"换 token"路径是手动的：删掉 state.json + 重启 launcher，重新
走 5 步。这不可接受。

**应做的修复**：
- 把 `tok.RefreshToken` 持久化到 keyring（key 名 `modelserver_refresh_token`）
- 起一个后台 service（schtasks / Windows service / launcher 子进程）每
  50 分钟拿 refresh_token 调 `POST https://codeapi.cs.ac.cn/oauth2/token
  grant_type=refresh_token`，把新 access_token 写回 keyring + setx
- 或者：launcher 在 exec VSCode 之前检测 token 是否快过期，过期就刷一次

**注意**：refresh_token 自己也会过期（Hydra 默认 30 天）。30 天后用户必须
重新走完整 PKCE。这个 UX 可以接受。

**同样适用于 agentserver_ws_api_key** — 那个也是 device-flow access_token。

---

## 3. loom 启动时如何 discover workspace

**背景**：项目描述 (项目描述.md:14) 要求"根据 https://github.com/agentserver/loom，
自动运行 driver 和 slave，加到 https://agent.cs.ac.cn/ workspace 中，
安装 loom 里面的 skills（包括 driver skill）"。

当前 onboarding 走完后我们手里只有：
- `agentserver_ws_api_key` = 一个 Hydra access_token（不是 workspace API key）
- **没有 workspace_id** — `state.Agentserver.WorkspaceID` 为空

用户在 Hydra device verify 后选了哪个 workspace，agentserver 那边记下了，
但没有 API 把它返回给我们（admin API 拒绝 OAuth bearer）。

**loom 接力时必须**：
1. 用 access_token 调一个**接受 OAuth bearer 的** agentserver endpoint
   去查"我（这个 token 持有者）刚才选的 workspace 是啥"
2. 或者让 user 在 onboarding UI 里手动选 workspace（绕开 OAuth 那一步的
   workspace 选择）

需要查 agentserver 源码 / 问 agentserver 维护者哪个 endpoint 接受 OAuth
bearer 且能返回当前用户 workspace 列表。

**临时可做的事**：调用 `GET /api/workspaces` 用 token 测试 — 当前看到 401，
但也许换个 endpoint 路径就行（比如 `/api/user/me/workspaces` 或类似的
"user-scoped" 路径）。

---

## 4. (非 bug, 但值得记) launcher session 0 vs session 1 隔离

SSH 启动的 launcher 跑在 Windows session 0（服务会话），spawn 出来的浏览
器也在 session 0，用户在物理桌面（session 1）看不到。

**结论**：自动化 E2E 测试**必须**通过桌面快捷方式或 schtasks 在 session 1
里启动 launcher，SSH 直接启动跑不通真实用户场景。

这次测试用了 hybrid 方式：用户在物理桌面双击快捷方式（session 1），我从
SSH 通过 HTTP API（`/api/state` + `/api/step/*`）远程观察。

未来 CI 自动化要考虑：scheduled task with `/RU 61414 /SC ONCE /RL HIGHEST`
启动 launcher，然后 webdriver 通过 RDP / VNC 驱动 OAuth。
