# 请帮 agentserver-vscode 安装包 配通 modelserver device flow (v4 修订)

(2026-06-03 v4 修订 — 前两版我看源码看快了,搞出两个错误。这是查完
`internal/admin/device_flow.go` 完整实现之后的正确版本。)

---

## 前情提要 + 已经做错的两件事(请先回滚)

### ❌ 错误 1: Hydra `urls.device.verification`

v2 版让你在 Hydra 配:

```yaml
urls:
  device:
    verification: https://codeapi.cs.ac.cn/oauth2/device/verify   # ← 删掉
```

请回滚 / 删掉这一项。它是给 Hydra 原生 device flow 用的,我们走的是
modelserver 自己包了一层的 wrapper,不需要这个。配了反而让浏览器访问
`/oauth2/device/verify` 时进入 302 死循环 (Hydra 重定向到自己)。

### ❌ 错误 2: 为安装包注册的 public client `5321f7e6-3d79-4ac9-a742-04809dbf9025`

这个 client 跟 modelserver 的 device-flow wrapper 完全无关 — 我之前以为
是直连 Hydra 用的,搞错了。可以删掉,也可以留着不管 (它不影响任何东西)。

---

## 正确的方案:配一个 confidential client 给 modelserver 自己用

### 这个 wrapper 的真实架构

读了 `internal/admin/device_flow.go` (整个文件) 之后,modelserver 的
`/oauth/device/*` 5 个端点**不是**真的 RFC 8628 device flow,而是
"看起来像 device flow 的 API",底层其实是 modelserver server 拿着一个
confidential OAuth client 在中间帮我们对着 Hydra 跑 authorization-code:

```
   CLI                  modelserver wrapper                Hydra
    │
    │ POST /oauth/device/code (任意 client_id, 不参与 Hydra)
    │ ─────────────────────► HandleDeviceAuthorize
    │                         · 生成 device_code + user_code
    │                         · 存 DB (储 client_id 字段只为审计)
    │ ◄───────────────────── 返回 {device_code, user_code,
    │                                 verification_uri=/oauth/device, ...}
    │
   (用户) GET /oauth/device?user_code=XXX
    │ ─────────────────────► HandleVerificationPage (渲染 device_verify.html)
   (用户) POST /oauth/device  (提交 user_code,触发后端跳 Hydra OAuth)
    │ ─────────────────────► HandleVerifyUserCode
    │                         · 用 dfCfg.ClientID 拼 authURL =
    │                           {hydraPublicURL}/oauth2/auth?response_type=code
    │                           &client_id={dfCfg.ClientID}&...
    │ ◄───────302──────── 重定向用户去 Hydra 登录
    │
   (用户在 Hydra 登录、选 project、同意)
    │
    │ ──Hydra 重定向回──► GET /oauth/device/callback?code=...&state=...
    │                       HandleCallback
    │                       · 用 dfCfg.ClientID + dfCfg.ClientSecret + code
    │                       · 调 Hydra /oauth2/token 换 access_token
    │                       · 把 token 存进 device_code 记录
    │
    │ POST /oauth/device/token (CLI 轮询)
    │ ─────────────────────► HandleTokenPoll
    │ ◄───────────────────── 返回 access_token + refresh_token
```

**关键发现**:wrapper 里(`device_flow.go:281`)有这一行:

```go
tokenResp, err := exchangeAuthCode(
    ctx, h.httpClient, hydraPublicURL,
    dfCfg.ClientID, dfCfg.ClientSecret,   // ← 必须 confidential client
    code, redirectURI,
)
```

`dfCfg.ClientSecret` 必须存在并且对得上 Hydra,否则换不到 token。

### 这意味着需要在 Hydra 里建一个 **新的、专给 modelserver server 用的 confidential client**

字段 (在你们 OAuth Clients 后台填):

| 字段 | 值 |
|---|---|
| **Client Name** | `modelserver-device-flow-wrapper` (或类似命名,跟用户安装包用的 client 区分) |
| **Client ID** | (autogenerate 即可,我不需要) |
| **Grant Types** | ☑ Authorization Code · ☑ Refresh Token (Device Code **不要**, Client Credentials **不要**) |
| **Response Types** | ☑ Code |
| **Redirect URIs** | `https://codeapi.cs.ac.cn/oauth/device/callback` |
| **Scope** | `project:inference offline_access` |
| **Token Endpoint Auth Method** | `client_secret_post` (这次**真的需要 secret**) |

注册后请回传给我:
- `client_id`
- `client_secret`

我会把这两个值给你贴到下面 modelserver config 那一节。

### modelserver config 改动

在 modelserver 的 `config.yml` (或 Helm values / env) 加上:

```yaml
auth:
  oauth:
    hydra:
      # ...其他 hydra 字段 (admin_url / public_url) 应该已经有了...

      device_flow:
        client_id: <上面新建的 confidential client_id>
        client_secret: <上面新建的 client_secret>
        # 默认值: code_ttl=600 (秒), poll_interval=5 (秒), 不需要改
```

源码字段定义见 `internal/config/config.go:78-85`:

```go
type DeviceFlowConfig struct {
    ClientID     string `yaml:"client_id"     mapstructure:"client_id"`
    ClientSecret string `yaml:"client_secret" mapstructure:"client_secret"`
    CodeTTL      int    `yaml:"code_ttl"      mapstructure:"code_ttl"`
    PollInterval int    `yaml:"poll_interval" mapstructure:"poll_interval"`
}
```

路由触发条件 (`internal/admin/routes.go:52`):

```go
if cfg.Auth.OAuth.Hydra.DeviceFlow.ClientID != "" {
    // ...注册 5 个 /oauth/device/* 路由...
}
```

只要 `ClientID` 非空就会注册。`ClientSecret` 是后面真正调 Hydra 时用。

### 让 modelserver 加载新配置

按你们平时改 modelserver config 后激活的方式即可 — reload signal / restart
pod / k8s rollout 任一都行,你最清楚。完成后路由 `/oauth/device/code` 会
从当前的 404 变成 200。

---

## 安装包侧:用任意 client_id

注册过的 `5321f7e6-3d79-4ac9-a742-04809dbf9025` (token_endpoint_auth_method=none)
其实没有任何角色:

- modelserver wrapper 不用它 (用的是新建的 confidential 那个)
- modelserver wrapper 的 CLI 调用 `POST /oauth/device/code` 时**接受任何
  client_id 字符串**,只存进数据库做审计,**不参与 Hydra 通信**

最简洁的安装包代码可以传 `client_id=agentserver-vscode` (固定字符串,完
全不在 Hydra 注册) — modelserver 反正不校验。要不要把 `5321f7e6-...`
那个 client 删了,你随意。

---

## 启用后的验证 (运维改完告诉我,我自己跑)

```bash
# 1. 端点出现
curl -X POST 'https://codeapi.cs.ac.cn/oauth/device/code' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'client_id=agentserver-vscode&scope=project:inference+offline_access'

# 期望: 200, JSON 含 {device_code, user_code, verification_uri, verification_uri_complete}
# verification_uri 应该是 https://codeapi.cs.ac.cn/oauth/device (不是 /oauth2/...)

# 2. 用户能正常登录
# 把 verification_uri_complete 在浏览器打开 → 应该看到带登录入口的页面,不死循环

# 3. CLI 轮询拿 token
curl -X POST 'https://codeapi.cs.ac.cn/oauth/device/token' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=urn:ietf:params:oauth:grant-type:device_code&client_id=agentserver-vscode&device_code=<上一步拿到的>'

# 期望: 200, JSON 含 {access_token, refresh_token, expires_in, token_type}
```

---

## 完整用户体验 (启用后)

```
1. 用户双击桌面 agentserver-vscode 快捷方式
2. 浏览器弹本地引导页 (127.0.0.1:RANDPORT)
3. 用户点 "登录 modelserver"
4. 新标签 → https://codeapi.cs.ac.cn/oauth/device?user_code=XXX
5. 用户在 modelserver 站登录 + 选 project + 同意
6. 后台: 安装包轮询拿到 access_token
7. 后台: 写 ~/.codex/config.toml + setx OPENAI_API_KEY
8. 完成。codex 直接能调 modelserver LLM
```

零复制粘贴。

---

## 参考

- modelserver device-flow wrapper 实现:
  `https://github.com/modelserver/modelserver/blob/main/internal/admin/device_flow.go`
- 路由注册:
  `https://github.com/modelserver/modelserver/blob/main/internal/admin/routes.go` (搜 `DeviceFlow`)
- DeviceFlowConfig 字段定义:
  `https://github.com/modelserver/modelserver/blob/main/internal/config/config.go` (搜 `DeviceFlowConfig`)
- RFC 8628:
  https://datatracker.ietf.org/doc/html/rfc8628
