# 请帮我在 modelserver (code.cs.ac.cn) 启用 Device Flow + 注册一个 OAuth Client

为 `agentserver-vscode` 安装包的"一键登录"做后端配置。这是 RFC 8628
"OAuth 2.0 Device Authorization Grant" 标准做法 — GitHub CLI 的
`gh auth login`、Azure CLI 的 `az login`、kubectl 装 OIDC 都用同一套。

需要做两件事:

1. **在 modelserver 的 config 里启用 Device Flow** (现在生产环境是关的,
   `POST https://codeapi.cs.ac.cn/oauth/device/code` 返回 404 就是因为
   `internal/admin/routes.go:230` 的 `if cfg.Auth.OAuth.Hydra.DeviceFlow.ClientID != ""`
   守卫不通过)
2. **在管理后台 "OAuth Clients" 注册一个 device flow client**

---

## 第一步:启用 Device Flow 配置

modelserver 的 `config.yml` 里(或者 Helm values / env,看你们怎么注的),
找到 `auth.oauth.hydra.device_flow` 一节,填入:

```yaml
auth:
  oauth:
    hydra:
      device_flow:
        # 必填:与下面 OAuth Client 注册的 Client ID 完全一致。
        client_id: agentserver-vscode

        # 可选,默认就好。device_code 有效期 (秒)、轮询间隔 (秒)。
        # code_ttl: 600
        # poll_interval: 5
```

`client_id` 不能留空 — 留空的话路由根本不会注册,我们就拿不到 device-flow
端点。

启用后,这些路由会出现在 `https://codeapi.cs.ac.cn/`:

```
POST /oauth/device/code        — 安装包请求 device_code + user_code
GET  /oauth/device             — 用户浏览器去的验证页 (modelserver 自带 UI)
POST /oauth/device             — 用户在验证页提交 user_code
GET  /oauth/device/callback    — Hydra 登录完后内部回调
POST /oauth/device/token       — 安装包轮询拿 access_token
```

我只用第 1、5 两个,中间三个是 modelserver 自己渲染的 UI / Hydra 内部流转,
不用我管。

---

## 第二步:注册 OAuth Client

modelserver 管理后台 → **OAuth Clients** → **Create OAuth Client**,
逐项填:

### Client Name (必填)

```
agentserver-vscode
```

### Client ID

请填**固定值**(不要 auto-generate):

```
agentserver-vscode
```

固定值的原因:这个值要跟第一步 config.yml 里 `device_flow.client_id`
完全一致;同时要写进安装包源码每次发版用同一个。auto-generate 一个 UUID
要我去改源码重打包,没必要。

### Redirect URIs

**留空。** Device flow 不需要 redirect。这是 device flow 相对 authorization
code flow 的最大优势 — 用户机器不用开本地 HTTP server 接 callback。

### Grant Types

只勾这两个:

- ☐ Authorization Code   ← **不要**
- ☑ **Device Code**      ← 必须
- ☑ **Refresh Token**    ← 必须 (offline_access 让 token 可以 1h 续期)
- ☐ Client Credentials   ← **不要**

### Response Types

**全不勾。** Device flow 不用 response_type 参数。如果系统强制要选一个,
随便给个 `code` 也无所谓 — Device flow 走不到 response_type 这一步。

- ☐ Code
- ☐ Token

### Scope

```
project:inference offline_access
```

(modelserver device_flow handler 默认就是这两个 scope,见
`internal/admin/device_flow.go:122`:
```go
if len(scopes) == 0 {
    scopes = []string{"project:inference", "offline_access"}
}
```
跟 agentserver connect modelserver 时申请的也一样。)

### Token Endpoint Auth Method

```
none
```

⚠️ **请务必从默认的 `client_secret_post` 改成 `none`**。

理由:安装包是 RFC 8252 native public client,装在每个用户的 Windows
机器上。代码可反编译,流量可抓包,**根本无法保密 client_secret**。
正确做法是不发 secret,安全靠以下三层:

1. **用户必须主动同意** — 每次 device flow 都会让用户去浏览器输入 user_code
   并登录确认,看到 modelserver 的 consent page。攻击者拿走 client_id 也
   生成不了 token,因为 token 必须有真实用户同意才能签发。
2. **PKCE-free 但 user_code 等效** — device flow 的 user_code 短期一次性,
   只在合法用户浏览器里出现,攻击者拿不到。
3. **scope 已最小化** — 只能调用 inference 端点,做不了管理操作。

如果系统下拉框里没有 `none` 选项,退而求其次用 `client_secret_post`,
生成 secret 后**把 secret 回传给我**,我写进安装包 (知道这只是名义上
的 secret,实际靠用户授权保护)。

### (可选) Audience

如果有 "Audience" 字段,留空,或者填:

```
https://codeapi.cs.ac.cn
```

---

## 创建后请回传给我

1. **Client ID** — 应该就是 `agentserver-vscode`,确认一下
2. **Client Secret** — **只有** Token Endpoint Auth Method 不是 `none`
   时才有;`none` 的情况下没有
3. **Device authorization endpoint** — 我猜
   `https://codeapi.cs.ac.cn/oauth/device/code`,请确认
4. **Token endpoint** — 我猜
   `https://codeapi.cs.ac.cn/oauth/device/token`,请确认
5. **Verification URI** — `POST /oauth/device/code` 返回里会有
   `verification_uri` 字段。从源码看应该是
   `https://codeapi.cs.ac.cn/oauth/device`,请确认是不是这个
   (或者帮我从 device_flow.go 的 `verificationURI` 变量看一眼实际值)

---

## 安装包完成后的用户体验

```
1. 用户双击桌面快捷方式 agentserver-vscode
2. 浏览器弹出引导页 (本地 127.0.0.1:RANDPORT)
3. 用户点 "登录 modelserver"
4. 引导页跳出一个新标签页:
   https://codeapi.cs.ac.cn/oauth/device?user_code=ABCD-EFGH
5. 用户在 modelserver 网站登录 (扫码/邮箱/GitHub/OIDC,任意配好的方式)
6. modelserver 让用户选 project
7. 用户点 "同意"
8. (后台) 安装包 poll /oauth/device/token,拿到 access_token
9. (后台) 安装包写 ~/.codex/config.toml + setx OPENAI_API_KEY=<token>
10. 完成。codex 可以直接调 modelserver 的 LLM
```

**全程零复制粘贴。** 用户只在浏览器里做两件事:登录 + 选 project + 点同意。

---

## 安全说明 (回答常见疑问)

- ✅ **client_id 写在安装包里不算泄露**:OAuth 2.0 public client 设计就
  允许 client_id 公开,GitHub CLI 的 client_id `178c6fc778ccc68e1d6a` 就
  硬编码在源码里 — [示例](https://github.com/cli/oauth/blob/trunk/device/device_flow.go)。
- ✅ **拿到 client_id 也不能伪造 token**:每个 token 都对应一次真实用户
  在浏览器 consent page 上的同意。攻击者就算拿走 client_id 也只能让某个
  合法用户去走 OAuth — 用户能看清这是 `agentserver-vscode` 在请求,可以
  拒绝。
- ✅ **限流按用户算**:modelserver 按 `sub` (user_id) 限流,不按 client_id。
- ⚠️ **唯一风险是冒名** — 别人能做一个软件,弹 OAuth 框声称是
  `agentserver-vscode`。这是 consent page 上 client name 是否清晰的问题,
  不是 secret 泄露问题。

---

## 参考

- modelserver device-flow handler 源码:
  `https://github.com/modelserver/modelserver/blob/main/internal/admin/device_flow.go`
- modelserver 路由注册:
  `https://github.com/modelserver/modelserver/blob/main/internal/admin/routes.go` (搜 `DeviceFlow`)
- RFC 8628 (OAuth Device Authorization Grant):
  https://datatracker.ietf.org/doc/html/rfc8628
- RFC 8252 (OAuth 2.0 for Native Apps):
  https://datatracker.ietf.org/doc/html/rfc8252
- agentserver 同样模式注册的 Hydra client (供对照):
  `https://github.com/agentserver/agentserver/blob/main/deploy/helm/agentserver/templates/hydra.yaml`
  (client_id = `agentserver-agent-cli`, 也是 public + device_code)
