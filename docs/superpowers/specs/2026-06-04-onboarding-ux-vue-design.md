# Onboarding 前端重写为 Vue + Element Plus

**Date**: 2026-06-04
**Status**: Design approved, ready for implementation plan
**Scope**: 仅 `internal/ui/assets/` 前端 (从 92 行 vanilla JS → Vue 3 + Element Plus SPA)
            + `handleASLogin` 后端返回结构对齐 + EnsureVSCode/ConfigureVSCode 补 progress event
            + Makefile 加 `ui-build` target

## Motivation

2026-06-04 Windows 测试机 E2E 暴露了一组 UX 问题（详见
`docs/superpowers/notes/2026-06-04-pkce-e2e-followups.md` 第 1 节）：

- 点 "安装 VS Code" 看起来没反应（EnsureVSCode early-return + SSE event
  只 console.log）
- 错误用浏览器 `alert()` 弹，关掉就丢
- 没有 step 进度提示
- 完成后页面没变化、没引导
- "用户码" alert 冗余（agentserver 的 verification_uri_complete 已经带
  user_code，Hydra 同意页会自动预填）
- agentserver 那条登录路径与 modelserver 不必要地分叉

这些不是 PKCE PR 引入的 bug，是已有的 UX 债。但 PKCE 后 UI 是用户直接
看到的第一道东西，值得现在还清。

同时把前端工程化（Vue + Vite + TypeScript + Element Plus）— 一次性投入
toolchain，后续加任何 UI（如 agentctl-web、loom 状态面板）有现成的脚手架。

## Architecture

### Scope of change

```
internal/ui/
  assets/                       DELETED (旧 vanilla SPA)
+ assets/dist/                  Vite 产物，go:embed 目标，gitignored
+ web/                          新 Vite + Vue 工程
+   package.json
+   tsconfig.json
+   vite.config.ts              outDir: ../assets/dist, server.proxy for dev
+   index.html
+   src/
+     main.ts
+     App.vue
+     api.ts                    fetch 包装 + 类型
+     stepConfig.ts             STEPS const
+     composables/
+       useOnboarding.ts        状态机
+       useSSE.ts               EventSource 包装
+     components/
+       StepCard.vue
+       OauthStep.vue
+       ProgressStep.vue
+       ActionStep.vue
+       ErrorPanel.vue
+       SuccessBanner.vue
+     styles.css                Element Plus 主题变量覆盖
  server.go                     handleASLogin: 返 {state:"started", oauth_url}
                                handleMSLogin: 同时也补 oauth_url 字段 (现在没返)
+ server.go                     新增 handleLaunchVSCode (POST /api/launch-vscode)
                                新增 graceful shutdown 路径 (供 launch-vscode 触发)
  orchestrator.go               接口加 LaunchAndShutdown(ctx) error 方法
  orchestrator_real.go          实现 LaunchAndShutdown: execVSCode + 推 shutdown signal
                                EnsureVSCode/ConfigureVSCode: early-return 时发 ProgressEvent
                                LoginModelserver / LoginAgentserver 都改成返回 (oauth_url string, error)
                                  因为前端要拿 URL 做 "未自动打开?" 链接
  Makefile                      加 ui-build target; cross-windows / package 依赖 ui-build
  scripts/package-windows-zip.sh 加 pre-flight 检查 dist/index.html
.gitignore                      加 internal/ui/web/node_modules/ + internal/ui/assets/dist/
```

### Invariants

- 后端 HTTP API 路径不变：`/api/state`, `/api/step/*`, `/api/finalize`,
  `/api/abort`, `/api/events`
- 后端 orchestrator 业务流程不变：still does ReservePort/StartPKCE for MS,
  device-code for AS, etc.
- 中文 UI；不引 i18n
- launcher 二进制大小变化可忽略（embed Vue+EP 产物约 +300KB）

## Components

### Front-end (`internal/ui/web/`)

技术选型：

| 项 | 选择 | 理由 |
|---|---|---|
| 框架 | Vue 3 (Composition API + `<script setup>`) | 用户偏好 |
| 语言 | TypeScript | 安全性 / 重构友好 |
| UI 库 | Element Plus (按需引入) | 中文文档全，组件齐 |
| Build | Vite | Vue 官方工具链 |
| 状态管理 | composables (reactive/ref) | 5 个 step 状态不到 Pinia 的复杂度 |
| 测试 | Vitest | Vite 原生配套 |

#### 组件职责表

| 文件 | 职责 | 不做 |
|---|---|---|
| `App.vue` | 顶层布局；根据 `serverState.onboarding_status` 切换 banner / steps / 完成态 | 不调 API |
| `composables/useOnboarding.ts` | 状态机：runStep / retry / autoAdvance / current 计算 | 不渲染 |
| `composables/useSSE.ts` | `/api/events?stream=ID` 的 EventSource 包装，push 到 ref<ProgressEvent[]> | 不解析业务 |
| `api.ts` | 所有 fetch 收敛；类型：`ServerState`, `StepStatus`, `ProgressEvent`, `OnboardingError` | 不知道 UI |
| `stepConfig.ts` | `STEPS` 常量：5 项 (id, label, kind, autoStart) | — |
| `components/StepCard.vue` | 卡片壳：图标 + label + active/done/error 样式；`<slot name="action">` | 不调 API |
| `components/OauthStep.vue` | MS/AS 通用：调 `POST /api/step/{id}` → spinner + "请在浏览器中完成登录" + `<a href=oauth_url>浏览器没自动打开?</a>` + poll `/status` | 不知道是哪个 OAuth |
| `components/ProgressStep.vue` | vscode_install：调 POST，开 SSE，渲染 stage 文字 + `<el-progress>` (有 total 时) | 不发请求拼接逻辑 |
| `components/ActionStep.vue` | vscode_configure / finalize：POST 一次同步等结果 | 不轮询 |
| `components/ErrorPanel.vue` | 红边框 + 错误文字 + `<el-button>重试</el-button>` + 可折叠详情 | 不知道是哪个 step |
| `components/SuccessBanner.vue` | 顶部成功 banner + "立即打开 VS Code" + "关闭" 按钮 | 不知道 step 状态 |

#### 状态机 (`useOnboarding.ts`)

```typescript
type StepStatus = 'pending' | 'active' | 'in_progress' | 'success' | 'error';

interface StepRuntime {
  status: StepStatus;
  errorMessage?: string;
  errorDetail?: string;   // 折叠区
  stage?: string;
  percent?: number;       // 0-100; undefined = indeterminate
  oauthUrl?: string;
}

interface StepDef {
  id: string;
  label: string;
  kind: 'oauth' | 'progress' | 'action';
  autoStart: boolean;
  runtime: StepRuntime;
}

const state = reactive({
  steps: STEPS.map(s => ({ ...s, runtime: { status: 'pending' } as StepRuntime })),
  serverState: null as ServerState | null,
  connectionError: null as string | null,
});

const current = computed(() => state.steps.find(s => s.runtime.status !== 'success'));

async function refreshState() { /* GET /api/state, sync per-step status from completed_steps */ }
async function runStep(stepId: string) { /* dispatch to OauthStep/ProgressStep/ActionStep helper */ }
async function retry(stepId: string)  { /* clear error, runStep again */ }

// auto-advance hook
function onStepSuccess(stepId: string) {
  const next = state.steps[stepIndex(stepId) + 1];
  if (next && next.autoStart && next.runtime.status === 'pending') {
    next.runtime.status = 'active';
    runStep(next.id);
  }
}
```

#### `STEPS` (stepConfig.ts)

```typescript
export const STEPS: Array<Omit<StepDef, 'runtime'>> = [
  { id: 'modelserver_login',  label: '登录 modelserver',     kind: 'oauth',    autoStart: false },
  { id: 'agentserver_login',  label: '登录 agentserver',     kind: 'oauth',    autoStart: false },
  { id: 'vscode_install',     label: '安装 VS Code',          kind: 'progress', autoStart: true  },
  { id: 'vscode_configure',   label: '配置 VS Code 与 codex', kind: 'action',   autoStart: true  },
  { id: 'finalize',           label: '完成配置',              kind: 'action',   autoStart: false },
];
```

### Back-end changes

#### `internal/ui/server.go`

```go
// Before:
func (s *server) handleMSLogin(w http.ResponseWriter, r *http.Request) {
    if err := s.o.LoginModelserver(r.Context()); err != nil { writeErr(w, 500, err); return }
    writeJSON(w, 200, map[string]string{"state": "started"})
}

// After:
func (s *server) handleMSLogin(w http.ResponseWriter, r *http.Request) {
    oauthURL, err := s.o.LoginModelserver(r.Context())
    if err != nil { writeErr(w, 500, err); return }
    writeJSON(w, 200, map[string]string{"state": "started", "oauth_url": oauthURL})
}

// handleASLogin: same shape. No longer returns DeviceCodeChallenge to front-end.
func (s *server) handleASLogin(w http.ResponseWriter, r *http.Request) {
    oauthURL, err := s.o.LoginAgentserver(r.Context())
    if err != nil { writeErr(w, 500, err); return }
    writeJSON(w, 200, map[string]string{"state": "started", "oauth_url": oauthURL})
}

// New: launch + shutdown
func (s *server) handleLaunchVSCode(w http.ResponseWriter, r *http.Request) {
    if err := s.o.LaunchAndShutdown(r.Context()); err != nil { writeErr(w, 500, err); return }
    writeJSON(w, 200, map[string]string{"state": "launching"})
    // shutdown is triggered async from inside LaunchAndShutdown
}
```

#### `internal/ui/orchestrator.go`

```go
type Orchestrator interface {
    // ... existing methods unchanged except:

    // CHANGED return: was `error`, now `(oauthURL string, err error)`.
    // The URL was already available internally as sess.AuthURL (MS) or
    // ch.VerificationURIComplete (AS); we just surface it to the front-end
    // so it can render the "browser didn't open?" fallback link.
    LoginModelserver(ctx context.Context) (oauthURL string, err error)
    LoginAgentserver(ctx context.Context) (oauthURL string, err error)

    PollModelserverLogin(ctx context.Context) (modelserver.APIKey, error)     // unchanged
    PollAgentserverLogin(ctx context.Context) (agentserver.WorkspaceAPIKey, error) // unchanged

    // NEW: launch VS Code (spawn) and request graceful shutdown of the
    // onboarding HTTP server via Deps.Shutdown. Returns once VS Code is
    // spawned; shutdown is async (500ms delay so HTTP response can flush).
    LaunchAndShutdown(ctx context.Context) error

    // ... rest unchanged
}

type Deps struct {
    // ... existing fields ...

    // NEW: callback the launcher passes in to request its own HTTP server
    // be shut down. Triggered by LaunchAndShutdown.
    Shutdown func()
}
```

#### `internal/ui/orchestrator_real.go`

- `LoginModelserver`: build & return `sess.AuthURL` alongside starting the listener
- `LoginAgentserver`: return `r.asChallenge.VerificationURIComplete`
- `EnsureVSCode`: when `det.Installed`, send `ProgressEvent{Stage:"detected", Msg:"已检测到 VS Code "+det.Version+", 跳过下载"}` to the progress channel before returning
- `ConfigureVSCode`: similar early-return event when skipping codex download (already-bundled path)
- `LaunchAndShutdown`: call `execVSCode` (spawn), then invoke the shutdown
  callback (provided via `Deps.Shutdown`) which closes the launcher's HTTP
  server gracefully.

Launcher main.go switches from `http.Serve(ln, handler)` (blocking forever)
to an explicit `http.Server` whose `Shutdown(ctx)` can be triggered:

```go
srv := &http.Server{Handler: handler}
deps.Shutdown = func() {
    // give the in-flight POST /api/launch-vscode response 500ms to flush
    go func() {
        time.Sleep(500 * time.Millisecond)
        _ = srv.Shutdown(context.Background())
    }()
}
return srv.Serve(ln)  // returns http.ErrServerClosed when Shutdown completes
```

After `srv.Serve` returns, `run()` returns nil → main() exits cleanly with
all defers run.

### Build pipeline

#### `Makefile`

```makefile
# NEW
ui-build:
	cd internal/ui/web && npm ci && npm run build

# CHANGED
cross-windows: ui-build
	# ... existing
package: cross-windows ext-build
	# ... existing (ext-build separately for vsix)
```

#### `internal/ui/web/package.json`

```json
{
  "name": "agentserver-vscode-onboarding-ui",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vue-tsc --noEmit && vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "vue": "^3.5.0",
    "element-plus": "^2.10.0",
    "@element-plus/icons-vue": "^2.3.1"
  },
  "devDependencies": {
    "@vitejs/plugin-vue": "^5.2.0",
    "typescript": "^5.7.0",
    "vue-tsc": "^2.2.0",
    "vite": "^7.0.0",
    "vitest": "^2.1.0",
    "@vue/test-utils": "^2.4.0",
    "unplugin-auto-import": "^0.19.0",
    "unplugin-vue-components": "^0.28.0"
  }
}
```

#### `internal/ui/web/vite.config.ts`

```typescript
import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import AutoImport from 'unplugin-auto-import/vite';
import Components from 'unplugin-vue-components/vite';
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers';

export default defineConfig({
  plugins: [
    vue(),
    AutoImport({ resolvers: [ElementPlusResolver()] }),
    Components({ resolvers: [ElementPlusResolver()] }),
  ],
  build: {
    outDir: '../assets/dist',
    emptyOutDir: true,
    target: 'es2020',
  },
  server: {
    port: 5173,
    proxy: { '/api': 'http://127.0.0.1:8080' }, // dev: configurable
  },
});
```

#### `scripts/package-windows-zip.sh` pre-flight

```bash
for f in ... \
         internal/ui/assets/dist/index.html \
         ...; do
  [[ -e "$f" ]] || { echo "missing: $f (run: make ui-build)"; exit 1; }
done
```

## Data Flow (happy path)

```
[Launcher 启动]
  serveOnboarding → ln.Listen 127.0.0.1:RAND
  go browser.Open(url)
        ↓
[浏览器 GET /]
  Go FileServer 走 //go:embed → assets/dist/index.html (Vue 入口)
        ↓
[main.ts createApp(App).mount('#app')]
        ↓
[App.vue onMounted → useOnboarding.init()]
  refreshState: GET /api/state → {completed_steps: []}
  推 step status: modelserver_login = active, 其余 = pending
        ↓
[渲染 StepCard list; current 高亮]
        ↓
[用户点 MS step 的 "开始" 按钮]
  OauthStep.handleStart → useOnboarding.runStep('modelserver_login')
    runtime.status='in_progress'; stage='正在打开浏览器...'
    POST /api/step/modelserver_login → {state:"started", oauth_url:"https://codeapi..."}
    runtime.oauthUrl = oauth_url; stage = '请在浏览器中完成登录'
        ↓
[浏览器 (后端 go OpenBrowser 触发的) 跳 Hydra OAuth 页 → 用户扫码同意]
        ↓
[pollLoop every 3s → GET /api/step/modelserver_login/status]
  ... waiting ... waiting ... success
        ↓
[useOnboarding 收到 success]
  runtime.status='success'
  refreshState → completed_steps += 'modelserver_login'
  onStepSuccess('modelserver_login') → 下一步 agentserver_login.autoStart=false
                                       → 只 highlight, 不自动跑
        ↓
[用户点 AS step → 同上流程]
        ↓
[AS 完成 → vscode_install.autoStart=true → 自动 runStep]
  POST /api/step/vscode_install → {stream_id}
  new EventSource('/api/events?stream='+id)
  收事件:
    {stage:"detected", msg:"已检测到 VS Code 1.96.0, 跳过下载"}
       → ProgressStep 显示文字, hide progress bar
    OR
    {stage:"downloading", downloaded:124, total:287}
       → progress bar 43%
  SSE close → success
        ↓
[autoStart 链继续: vscode_configure]
  POST → {state:"success"} or error
        ↓
[finalize.autoStart=false → 等用户点 "完成"]
  POST /api/finalize → {state:"complete"}
  refreshState → onboarding_status="complete"
        ↓
[App.vue 检到 complete]
  SuccessBanner 渲染在顶部
  step list 全 ✓ 灰色保留显示
        ↓
[用户点 "立即打开 VS Code"]
  POST /api/launch-vscode → orchestrator.LaunchAndShutdown
    execVSCode (spawn)
    deps.Shutdown() → 触发 launcher 关闭 channel
    return 200 {state:"launching"}
  banner 切到 "VS Code 已启动. 此窗口可关闭."
        ↓
[500ms 后 launcher os.Exit(0)]
  浏览器后续 fetchState 全 fail → UI 灰化 / 显示"会话已结束"
```

## Error Handling

| 触发 | UI 表现 | 用户操作 |
|---|---|---|
| `fetchState` 失败（断网 / 后端挂） | 顶部红 banner + step 灰化 | 自动 5s 重连；可手动重试 |
| OAuth poll `{error: ...}` | runtime.status='error'; ErrorPanel | "重试" → 重发 POST |
| oauth_url 字段缺失（兼容旧 launcher） | 不显示 fallback 链接 | 自动 fallback |
| SSE 收 error event | ErrorPanel + bar 消失 | 重试 → 后端 DownloadResumable 续传 |
| `action` step 返 500 | response.text() 进 ErrorPanel | 重试 |
| autoStart 链中失败 | 错的那步停在 error，下游不自动 | 重试该步成功后继续 |
| `launch-vscode` 失败 | banner: "启动失败: X. 双击桌面快捷方式" | 关 tab |
| launch-vscode 之后 launcher 挂掉（预期） | UI 终态冻结 | 关 tab |

## Testing

| 层 | 内容 | 工具 |
|---|---|---|
| Vitest 单元 | useOnboarding 状态机；api.ts 错误抛 OnboardingError；autoAdvance 链 | vitest + fake-timers |
| Vitest 组件 | StepCard 4 种 status class；ErrorPanel slot；SuccessBanner emit | @vue/test-utils |
| Go server_test | `TestServerStepEndpoint` 改：MS/AS 都断 `body.state == "started"` 且 `body.oauth_url != ""` | 现有 noopOrchestrator |
| Go orchestrator_real_test | 加 `TestLoginModelserver_ReturnsOAuthURL` / `TestLoginAgentserver_ReturnsOAuthURL` | 已有 fake servers |
| Go integration tests | 不改（HTTP 直驱，前端不参与） | — |
| Manual Windows E2E | 重新部署 launcher，跑 5 step 看 UX | SSH + monitor |

不引 Playwright — 投入产出不值。Vitest 覆盖前端核心逻辑。

## Documentation

| 文件 | 改动 |
|---|---|
| `internal/ui/web/README.md` | 新增：dev / build / 组件层次 / 怎么 mock 后端 |
| `Makefile` help | 加 `make ui-build` 说明 |
| `docs/superpowers/notes/2026-06-04-pkce-e2e-followups.md` | 划掉第 1 节（已解决） |
| `.gitignore` | + `internal/ui/web/node_modules/` + `internal/ui/assets/dist/` |

## Out of scope (YAGNI)

- Pinia / Vue Router / i18n / dark mode / a11y audit / PWA
- Playwright E2E
- 重做 Element Plus 整体主题（只覆盖必要变量）
- onboarding 重置的 UI 按钮（agentctl reconfigure 已够）
- "立即打开 VS Code" 之外的其他终态动作（如 "再开一个 VS Code" / "重新登录"）
- refresh_token 心跳（独立 spec，详见
  `docs/superpowers/notes/2026-06-04-pkce-e2e-followups.md` 第 2 节）

## Risks and Assumptions

- **打包环境必须有 Node.js + npm**。现有 `ext-build` 已经要求了，加 `ui-build`
  是相同 toolchain。
- **Element Plus bundle 大小约 200KB**（按需引入，Vite tree-shaking）。
  相对 launcher.exe 10MB + bundled codex.exe 246MB 可忽略。
- **Vue 3 / Element Plus 兼容 Chromium 87+** → Edge 148 (Windows 测试机版本) OK。
- **Vite dev server 跑在 5173 端口**，与生产 launcher 跑的 RAND 端口无冲突。
  Dev 时需要手动设置 vite proxy 目标到当前 launcher 端口。
- **handleASLogin 返回结构变化** → 旧版前端如果还在跑会 break。这是单向
  改动，不维护双向兼容（前后端在同一个 binary 里发布）。
- **launcher 自杀路径** 是新增的：要确保 `http.Serve` 收到 shutdown 后能
  优雅关闭，且 launch-vscode 的 HTTP 响应能在 shutdown 前完整 flush。
